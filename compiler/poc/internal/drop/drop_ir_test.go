package drop_test

import (
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/drop"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// TestPhase2B3NeedsDropAndGlueClassification 覆盖 §15 全部类型分类规则、精确字段序以及 !IsCopy != NeedsDrop 原则。
func TestPhase2B3NeedsDropAndGlueClassification(t *testing.T) {
	tctx := types.NewContext()
	symTab := symbols.NewTable(tctx)
	reg := drop.NewResourceRegistry()
	sp := diagnostic.Span{File: "drop_classify.won"}

	// 1. 标量、引用、裸指针与无资源结构体不需要 Drop
	nonDropTypes := []struct {
		name string
		tid  types.TypeID
	}{
		{"i32", tctx.I32},
		{"f64", tctx.F64},
		{"bool", tctx.Bool},
		{"char", tctx.Char},
		{"raw_const", tctx.RawPtr(false, tctx.I32)},
		{"raw_mut", tctx.RawPtr(true, tctx.I32)},
		{"shared_ref", tctx.Ref(false, tctx.StringT)},
		{"mut_ref_string", tctx.Ref(true, tctx.StringT)}, // 关键：!IsCopy 但不拥有资源 -> false!
		{"fn_type", tctx.Fn([]types.TypeID{tctx.StringT}, tctx.Unit)},
	}
	for _, tc := range nonDropTypes {
		if drop.NeedsDrop(tctx, symTab, reg, tc.tid) {
			t.Fatalf("%s 不应需要资源析构 (NeedsDrop=false)", tc.name)
		}
	}

	// 验证 @derive(Copy) 结构体不可拥有 Drop 胶水
	copyStructSym := symTab.AllocSymbol(symbols.SymStruct, "Point", sp)
	copyStructSym.Fields = []symbols.FieldInfo{
		{Name: "x", Type: tctx.I32, Span: sp},
		{Name: "y", Type: tctx.I32, Span: sp},
	}
	copyStructSym.FieldMap = map[string]*symbols.FieldInfo{
		"x": &copyStructSym.Fields[0],
		"y": &copyStructSym.Fields[1],
	}
	copyStructType := tctx.Struct(uint64(copyStructSym.ID), "Point", nil)
	tctx.MarkDerivedCopy(uint64(copyStructSym.ID))
	if drop.NeedsDrop(tctx, symTab, reg, copyStructType) {
		t.Fatalf("@derive(Copy) 结构体 Point 不得需要 Drop")
	}

	// 2. 内置 String -> GlueString
	cc := drop.NewClassifyContext(tctx, symTab, reg)
	gStr, needStr := cc.ResolveDropGlue(tctx.StringT)
	if !needStr || gStr == nil || gStr.Kind != drop.GlueString {
		t.Fatalf("String 期望解析出 GlueString，实际 %+v need=%v", gStr, needStr)
	}

	// 3. 含 String 与标量混合的结构体：仅追踪 String 字段，且严格按字段声明顺序排列
	docSym := symTab.AllocSymbol(symbols.SymStruct, "Document", sp)
	docSym.Fields = []symbols.FieldInfo{
		{Name: "id", Type: tctx.I32, Span: sp},
		{Name: "title", Type: tctx.StringT, Span: sp},
		{Name: "body", Type: tctx.StringT, Span: sp},
	}
	docSym.FieldMap = map[string]*symbols.FieldInfo{
		"id":    &docSym.Fields[0],
		"title": &docSym.Fields[1],
		"body":  &docSym.Fields[2],
	}
	docType := tctx.Struct(uint64(docSym.ID), "Document", nil)
	gDoc, needDoc := cc.ResolveDropGlue(docType)
	if !needDoc || gDoc.Kind != drop.GlueStruct || len(gDoc.Fields) != 2 {
		t.Fatalf("Document 期望包含 2 个 Drop 字段的 GlueStruct，实际 %+v", gDoc)
	}
	if gDoc.Fields[0].Name != "title" || gDoc.Fields[1].Name != "body" {
		t.Fatalf("Document 字段清理顺序必须遵守声明序 [title, body]，实际 [%s, %s]", gDoc.Fields[0].Name, gDoc.Fields[1].Name)
	}

	// 4. 嵌套结构体
	wrapSym := symTab.AllocSymbol(symbols.SymStruct, "Wrapper", sp)
	wrapSym.Fields = []symbols.FieldInfo{{Name: "doc", Type: docType, Span: sp}}
	wrapSym.FieldMap = map[string]*symbols.FieldInfo{"doc": &wrapSym.Fields[0]}
	wrapType := tctx.Struct(uint64(wrapSym.ID), "Wrapper", nil)
	if gWrap, needWrap := cc.ResolveDropGlue(wrapType); !needWrap || gWrap.Kind != drop.GlueStruct || len(gWrap.Fields) != 1 {
		t.Fatalf("嵌套结构体 Wrapper 期望 GlueStruct，实际 %+v", gWrap)
	}

	// 5. 含资源载荷的枚举 -> GlueEnum（仅录入带资源的变体）
	msgEnumSym := symTab.AllocSymbol(symbols.SymEnum, "Message", sp)
	msgEnumSym.Variants = []symbols.VariantInfo{
		{Name: "Empty", Kind: symbols.VariantUnit},
		{Name: "Text", Kind: symbols.VariantTuple, TupleTypes: []types.TypeID{tctx.StringT}},
	}
	msgEnumType := tctx.Enum(uint64(msgEnumSym.ID), "Message", nil)
	gEnum, needEnum := cc.ResolveDropGlue(msgEnumType)
	if !needEnum || gEnum.Kind != drop.GlueEnum || len(gEnum.Variants) != 1 || gEnum.Variants[0].Name != "Text" {
		t.Fatalf("Message 枚举期望仅 Text 变体的 GlueEnum，实际 %+v", gEnum)
	}

	// 6. 含资源的元组与定长数组
	tupType := tctx.Tuple([]types.TypeID{tctx.I32, tctx.StringT})
	if gTup, needTup := cc.ResolveDropGlue(tupType); !needTup || gTup.Kind != drop.GlueTuple || len(gTup.TupleElems) != 1 || gTup.TupleElems[0].Index != 1 {
		t.Fatalf("(i32, String) 元组期望下标 1 析构元素，实际 %+v", gTup)
	}
	arrResType := tctx.Array(tctx.StringT, 4)
	if gArr, needArr := cc.ResolveDropGlue(arrResType); !needArr || gArr.Kind != drop.GlueArray || gArr.ArrayLen != 4 {
		t.Fatalf("[String; 4] 期望 GlueArray(len=4)，实际 %+v", gArr)
	}
	if cc.NeedsDrop(tctx.Array(tctx.I32, 4)) {
		t.Fatalf("[i32; 4] 标量数组不得需要 Drop")
	}

	// 7. 扩展点与自定义 Drop 挂钩 (WON-MEM-007 内部准备，Prelude 无 Vec/Shared/Arc)
	customSym := symTab.AllocSymbol(symbols.SymStruct, "CustomHandle", sp)
	customSym.Fields = []symbols.FieldInfo{{Name: "raw", Type: tctx.I32, Span: sp}}
	customSym.FieldMap = map[string]*symbols.FieldInfo{"raw": &customSym.Fields[0]}
	customType := tctx.Struct(uint64(customSym.ID), "CustomHandle", nil)
	reg.MarkCustomDropHook(customSym.ID)
	if gCustom, needCustom := cc.ResolveDropGlue(customType); !needCustom || gCustom.Kind != drop.GlueCustomDropReserved || !cc.HasCustomDrop(customType) {
		t.Fatalf("CustomHandle 应识别为 GlueCustomDropReserved 且 HasCustomDrop=true，实际 %+v", gCustom)
	}
	if symTab.GlobalScope.Lookup("Vec") != nil || symTab.GlobalScope.Lookup("Shared") != nil || symTab.GlobalScope.Lookup("Arc") != nil {
		t.Fatalf("Phase 2B.3 不得提前将 Vec/Shared/Arc 注入标准 Prelude")
	}
}

// TestPhase2B3DropIRCloningAndSnapshots 验证 OpDrop / OpCondDrop / OpFFITransfer 的确定性快照及原 CFG 不可变隔离。
func TestPhase2B3DropIRCloningAndSnapshots(t *testing.T) {
	tctx := types.NewContext()
	symTab := symbols.NewTable(tctx)
	sp := diagnostic.Span{File: "ir_snap.won", Start: diagnostic.Position{Line: 1, Column: 1}}

	fnSym := symTab.AllocSymbol(symbols.SymFunc, "demo", sp)
	sSym := symTab.AllocSymbol(symbols.SymVar, "s", sp)
	sSym.Type = tctx.StringT

	origFn := &cfg.FuncCFG{
		Name:    "demo",
		FuncSym: fnSym,
		Entry:   0,
		Exit:    0,
		Blocks: []*cfg.BasicBlock{
			{
				ID:    0,
				Label: "entry",
				Statements: []cfg.Statement{
					{Kind: cfg.OpInitAssign, Place: cfg.Place{RootSymID: sSym.ID, RootName: "s", RootType: tctx.StringT, ElemType: tctx.StringT}, Type: tctx.StringT, Span: sp},
					{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{sSym.ID}, Span: sp},
					{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{sSym.ID}, Span: sp},
				},
				Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp},
			},
		},
	}
	origProg := &cfg.ProgramCFG{
		Functions: []*cfg.FuncCFG{origFn},
		BySymbol:  map[symbols.SymbolID]*cfg.FuncCFG{fnSym.ID: origFn},
	}

	cloned := drop.CloneProgramCFG(origProg)
	cc := drop.NewClassifyContext(tctx, symTab, nil)
	glue, _ := cc.ResolveDropGlue(tctx.StringT)

	// 在克隆 CFG 中于 OpStorageDead 之前插入 OpCondDrop 与 OpFFITransfer，验证原 CFG 完全不受影响
	cFn := cloned.Functions[0]
	blk := cFn.Blocks[0]
	blk.Statements = []cfg.Statement{
		blk.Statements[0],
		{Kind: cfg.OpFFITransfer, Place: blk.Statements[0].Place, CalleeName: "c_take", DropGlue: glue.ID, Span: sp},
		{Kind: cfg.OpCondDrop, Place: blk.Statements[0].Place, Type: tctx.StringT, DropFlag: 1, DropGlue: glue.ID, Span: sp},
		blk.Statements[1],
		blk.Statements[2],
	}

	if len(origProg.Functions[0].Blocks[0].Statements) != 3 {
		t.Fatalf("克隆修饰不得影响原始 FuncCFG 语句序列")
	}

	elab := &drop.DropElaboratedProgram{
		ElaboratedCFG: cloned,
		Functions: map[symbols.SymbolID]*drop.DropElaboratedFunc{
			fnSym.ID: {
				SymID:     fnSym.ID,
				Name:      "demo",
				CFG:       cFn,
				FlagOrder: []drop.DropFlagID{1},
				Flags: map[drop.DropFlagID]*drop.DropFlagMeta{
					1: {ID: 1, OwnerSymID: sSym.ID, Place: blk.Statements[0].Place, Type: tctx.StringT, GlueID: glue.ID, InitialVal: true},
				},
			},
		},
		GlueTable:      cc.GlueTable(),
		DropElaborated: true,
	}

	snap := elab.Dump()
	for _, want := range []string{"DropElaborated=true", "flag df1 place=s#", "FFITransfer s#", "CondDrop s#"} {
		if !strings.Contains(snap, want) {
			t.Fatalf("快照缺少期望片段 %q:\n%s", want, snap)
		}
	}
}

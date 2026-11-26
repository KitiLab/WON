package drop_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/drop"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

type mockSemView struct {
	tctx   *types.Context
	symTab *symbols.Table
}

func (m *mockSemView) GetTypes() *types.Context                 { return m.tctx }
func (m *mockSemView) GetSymbols() *symbols.Table               { return m.symTab }
func (m *mockSemView) ExprType(ast.Expr) types.TypeID           { return types.Invalid }
func (m *mockSemView) CallTarget(ast.Expr) *symbols.Symbol      { return nil }
func (m *mockSemView) FieldRef(ast.Node) *symbols.FieldInfo     { return nil }
func (m *mockSemView) VariantRef(ast.Node) *symbols.VariantInfo { return nil }

func checkAndElaborateSource(t *testing.T, src string) (*checker.TypedProgram, *diagnostic.Report) {
	t.Helper()
	rep := diagnostic.NewReport()
	lx := lexer.New("drop_test.won", src, rep)
	p := parser.New(lx, rep, "drop_test.won")
	prog := p.ParseProgram()
	if rep.HasErrors() {
		return nil, rep
	}
	chk := checker.New(rep, nil)
	tp := chk.CheckProgram(prog)
	return tp, rep
}

func TestPhase2B3DeterministicDropElaborationCoreCases(t *testing.T) {
	tests := []struct {
		name          string
		src           string
		wantErrors    bool
		wantCode      string
		checkSnapshot func(t *testing.T, tp *checker.TypedProgram, snap string)
	}{
		{
			name: "copy_and_refs_no_drop",
			src: `fn foo(r: &i64) -> i64:
    let x: i64 = 10
    let y = *r
    return x + y
`,
			checkSnapshot: func(t *testing.T, tp *checker.TypedProgram, snap string) {
				if strings.Contains(snap, "    Drop ") || strings.Contains(snap, "    CondDrop ") {
					t.Fatalf("纯 Copy/借用函数不应发出 Drop/CondDrop:\n%s", snap)
				}
			},
		},
		{
			name: "lifo_reverse_declaration_order",
			src: `fn make_s() -> String:
    return String::from("x")

fn order_test():
    let first = make_s()
    let second = make_s()
    let third = make_s()
`,
			checkSnapshot: func(t *testing.T, tp *checker.TypedProgram, snap string) {
				if len(tp.DropPlan.Functions) == 0 {
					t.Fatalf("期望函数存在 Drop 计划")
				}
				idxThird := strings.Index(snap, "Drop third#")
				idxSecond := strings.Index(snap, "Drop second#")
				idxFirst := strings.Index(snap, "Drop first#")
				if idxThird < 0 || idxSecond < 0 || idxFirst < 0 {
					t.Fatalf("期望三个 String 局部变量均发出无条件 Drop:\n%s", snap)
				}
				if !(idxThird < idxSecond && idxSecond < idxFirst) {
					t.Fatalf("期望 LIFO 声明逆序 (third -> second -> first):\n%s", snap)
				}
				if strings.Contains(snap, "flag df") {
					t.Fatalf("确定初始化的非逃逸局部变量不应分配 DropFlag:\n%s", snap)
				}
			},
		},
		{
			name: "moved_excluded_from_drop",
			src: `fn consume(s: String):
    let _u = 0

fn transfer():
    let s = String::from("hello")
    consume(s)
`,
			checkSnapshot: func(t *testing.T, tp *checker.TypedProgram, snap string) {
				fn := tp.CFG.Functions[1]
				df := tp.DropPlan.Functions[fn.FuncSym.ID]
				fnSnap := df.CFG.Dump()
				if strings.Contains(fnSnap, "Drop s#") || strings.Contains(fnSnap, "CondDrop s#") {
					t.Fatalf("已移动变量 s 绝不能再次发出 Drop:\n%s", fnSnap)
				}
			},
		},
		{
			name: "conditional_move_uses_drop_flag",
			src: `fn consume(s: String):
    let _u = 0

fn cond_drop(flag: bool):
    let s = String::from("hello")
    if flag:
        consume(s)
`,
			checkSnapshot: func(t *testing.T, tp *checker.TypedProgram, snap string) {
				if !strings.Contains(snap, "flag df1 place=s#") {
					t.Fatalf("期望仅为 MaybeInit 的 s 分配 df1:\n%s", snap)
				}
				if !strings.Contains(snap, "CondDrop s#") || !strings.Contains(snap, "flag=df1") {
					t.Fatalf("期望发出基于 df1 的 CondDrop:\n%s", snap)
				}
				// 检查 DropFlagEvent 含 FlagDecl, FlagSetTrue, FlagSetFalse, FlagCheck
				var foundDecl, foundTrue, foundFalse, foundCheck bool
				for _, df := range tp.DropPlan.Functions {
					for _, ev := range df.FlagEvents {
						switch ev.Op {
						case drop.FlagDecl:
							foundDecl = true
						case drop.FlagSetTrue:
							foundTrue = true
						case drop.FlagSetFalse:
							foundFalse = true
						case drop.FlagCheck:
							foundCheck = true
						}
					}
				}
				if !foundDecl || !foundTrue || !foundFalse || !foundCheck {
					t.Fatalf("DropFlagEvent 缺少必备状态事件 decl=%v true=%v false=%v check=%v", foundDecl, foundTrue, foundFalse, foundCheck)
				}
			},
		},
		{
			name: "early_return_and_owned_param",
			src: `fn ret_early(cond: bool, p: String) -> String:
    let local_keep = String::from("inner")
    if cond:
        return p
    let second = String::from("b")
    return second
`,
			checkSnapshot: func(t *testing.T, tp *checker.TypedProgram, snap string) {
				// early return 返回 p：应销毁 local_keep，不得销毁已移出的 p；
				// 正常结尾返回 second：应销毁 local_keep 与未被移动的 owned param p。
				if !strings.Contains(snap, "Drop local_keep#") {
					t.Fatalf("早退或常规出口均应释放局部 local_keep:\n%s", snap)
				}
				if !strings.Contains(snap, "Drop p#") {
					t.Fatalf("未走早退返回路径的参数 p 必须在函数出口释放:\n%s", snap)
				}
				if strings.Contains(snap, "Drop second#") {
					t.Fatalf("被作为返回值移出的 second 不应被 Drop:\n%s", snap)
				}
			},
		},
		{
			name: "reassignment_replacement_variants",
			src: `fn consume(s: String):
    let _u = 0

fn reassign_init():
    var s = String::from("a")
    s = String::from("b")

fn reassign_after_move():
    var s = String::from("a")
    consume(s)
    s = String::from("b")

fn reassign_maybe(flag: bool):
    var s = String::from("a")
    if flag:
        consume(s)
    s = String::from("b")
`,
			checkSnapshot: func(t *testing.T, tp *checker.TypedProgram, snap string) {
				for _, fn := range tp.DropPlan.ElaboratedCFG.Functions {
					fnDump := fn.Dump()
					switch fn.Name {
					case "reassign_init":
						if strings.Count(fnDump, "Drop s#") != 2 {
							t.Fatalf("reassign_init 期望在重赋值前与作用域退出时各发出 1 次 Drop s (共 2 次):\n%s", fnDump)
						}
					case "reassign_after_move":
						if strings.Count(fnDump, "Drop s#") != 1 {
							t.Fatalf("reassign_after_move 移出后重赋值仅在作用域退出时 Drop 1 次:\n%s", fnDump)
						}
					case "reassign_maybe":
						if !strings.Contains(fnDump, "CondDrop s#") || !strings.Contains(fnDump, "flag=df1") {
							t.Fatalf("reassign_maybe 在覆盖前应触发 CondDrop:\n%s", fnDump)
						}
					}
				}
			},
		},
		{
			name: "loop_break_continue_only_exit_inner_scopes",
			src: `fn loop_cleanup(limit: i64):
    let outer = String::from("keep")
    var i: i64 = 0
    while i < limit:
        let iter_s = String::from("item")
        i = i + 1
        if i == 2:
            continue
        if i == 5:
            break
`,
			checkSnapshot: func(t *testing.T, tp *checker.TypedProgram, snap string) {
				if strings.Count(snap, "Drop iter_s#") < 2 {
					t.Fatalf("while 循环体内 continue/break/常规尾部均应销毁 iter_s:\n%s", snap)
				}
				if strings.Count(snap, "Drop outer#") != 1 {
					t.Fatalf("outer 只能在循环外函尾销毁 1 次，不能被 continue/break 提前释放:\n%s", snap)
				}
			},
		},
		{
			name: "partial_struct_move_and_field_cleanup",
			src: `struct Pair:
    first: String
    second: String

fn consume(s: String):
    let _u = 0

fn partial_pair(cond: bool):
    let p = Pair { first: String::from("a"), second: String::from("b") }
    if cond:
        consume(p.first)
`,
			checkSnapshot: func(t *testing.T, tp *checker.TypedProgram, snap string) {
				if !strings.Contains(snap, "CondDrop p#") || !strings.Contains(snap, ".first ") || !strings.Contains(snap, "flag=df1") {
					t.Fatalf("期望部分移动的 p.first 使用 CondDrop:\n%s", snap)
				}
				if !strings.Contains(snap, "Drop p#") || !strings.Contains(snap, ".second ") {
					t.Fatalf("期望仍存活的 p.second 使用无条件 Drop:\n%s", snap)
				}
				if strings.Contains(snap, "glue.Struct.Pair") {
					for _, line := range strings.Split(snap, "\n") {
						if strings.Contains(line, "Drop p#") && !strings.Contains(line, ".second") && !strings.Contains(line, ".first") {
							t.Fatalf("部分移动后的结构体禁止整体 Drop:\n%s", snap)
						}
					}
				}
			},
		},
		{
			name: "partial_aggregate_construction_try_early_return",
			src: `struct Pair:
    first: String
    second: String

enum ResErr:
    Fail

fn fail_str(ok: bool) -> Result[String, ResErr]:
    if ok:
        return Result::Ok(String::from("ok"))
    return Result::Err(ResErr::Fail)

fn build_pair(ok: bool) -> Result[Pair, ResErr]:
    let p = Pair { first: String::from("first_res"), second: fail_str(ok)? }
    return Result::Ok(p)
`,
			checkSnapshot: func(t *testing.T, tp *checker.TypedProgram, snap string) {
				// 当 second: fail_str(ok)? 早退时，前置已完成求值的 __agg_tmp_1 必须被释放，后置 second 未初始化绝不释放
				if !strings.Contains(snap, "Drop __agg_tmp_") {
					t.Fatalf("含 '?' 的聚合字面量早退路径必须析构已初始化的前置成员暂存槽:\n%s", snap)
				}
			},
		},
		{
			name: "rejected_program_does_not_certify_drop",
			src: `fn bad_uam():
    let s = String::from("hi")
    let a = s
    let b = s
`,
			wantErrors: true,
			wantCode:   "WON-MEM-001",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tp, rep := checkAndElaborateSource(t, tc.src)
			if tc.wantErrors {
				if !rep.HasErrors() {
					t.Fatalf("期望报错 %s，实际通过", tc.wantCode)
				}
				if tp != nil && (tp.OwnershipChecked || tp.DropElaborated) {
					t.Fatalf("报错程序禁止通过 DropElaborated/OwnershipChecked 认证 (OC=%v DE=%v)", tp.OwnershipChecked, tp.DropElaborated)
				}
				if tc.wantCode != "" && !strings.Contains(rep.String(), tc.wantCode) {
					t.Fatalf("期望诊断码 %s，实际:\n%s", tc.wantCode, rep.String())
				}
				return
			}
			if rep.HasErrors() {
				t.Fatalf("意外编译报错:\n%s", rep.String())
			}
			if tp == nil || !tp.OwnershipChecked || !tp.DropElaborated || tp.DropPlan == nil {
				t.Fatalf("期望 OwnershipChecked && DropElaborated 为 true, tp=%+v", tp)
			}
			snap1 := tp.DropPlan.Dump()
			// 验证确定性快照
			tp2, rep2 := checkAndElaborateSource(t, tc.src)
			if rep2.HasErrors() || tp2.DropPlan.Dump() != snap1 {
				t.Fatalf("Drop 展开结果不具备确定性！\n=== 1 ===\n%s\n=== 2 ===\n%s", snap1, tp2.DropPlan.Dump())
			}
			if tc.checkSnapshot != nil {
				tc.checkSnapshot(t, tp, snap1)
			}
		})
	}
}

func TestPhase2B3CustomDropPartialMoveRejected(t *testing.T) {
	tctx := types.NewContext()
	symTab := symbols.NewTable(tctx)
	sp := diagnostic.Span{File: "custom.won", Start: diagnostic.Position{Line: 1, Column: 1, Byte: 0}, End: diagnostic.Position{Line: 1, Column: 20, Byte: 20}}

	guardSym := symTab.AllocSymbol(symbols.SymStruct, "CustomGuard", sp)
	guardSym.Fields = []symbols.FieldInfo{
		{Name: "a", Type: tctx.StringT, Span: sp},
		{Name: "b", Type: tctx.StringT, Span: sp},
	}
	guardSym.FieldMap = map[string]*symbols.FieldInfo{
		"a": &guardSym.Fields[0],
		"b": &guardSym.Fields[1],
	}
	guardType := tctx.Struct(uint64(guardSym.ID), "CustomGuard", nil)
	guardSym.Type = guardType

	reg := drop.NewResourceRegistry()
	reg.MarkCustomDropHook(guardSym.ID)

	fnSym := symTab.AllocSymbol(symbols.SymFunc, "partial_custom", sp)
	gVar := symTab.AllocSymbol(symbols.SymVar, "g", sp)
	gVar.Type = guardType
	sinkSym := symTab.AllocSymbol(symbols.SymVar, "sink", sp)
	sinkSym.Type = tctx.StringT

	progCFG := &cfg.ProgramCFG{
		Functions: []*cfg.FuncCFG{
			{
				Name:       "partial_custom",
				FuncSym:    fnSym,
				ReturnType: tctx.Unit,
				Entry:      0,
				Exit:       1,
				LocalOrder: []symbols.SymbolID{gVar.ID, sinkSym.ID},
				Locals: map[symbols.SymbolID]*cfg.LocalMeta{
					gVar.ID:    {Sym: gVar, Type: guardType, ScopeID: 1, HasInit: true, Span: sp},
					sinkSym.ID: {Sym: sinkSym, Type: tctx.StringT, ScopeID: 1, HasInit: true, Span: sp},
				},
				Blocks: []*cfg.BasicBlock{
					{
						ID:         0,
						Label:      "entry",
						Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: cfg.Place{RootSymID: gVar.ID, RootName: "g", RootType: guardType, ElemType: guardType}, Type: guardType, Span: sp},
							{Kind: cfg.OpMove, Place: cfg.Place{RootSymID: gVar.ID, RootName: "g", RootType: guardType, ElemType: tctx.StringT, Projections: []cfg.Projection{{Kind: cfg.ProjField, FieldName: "a", FieldIndex: 0, ElemType: tctx.StringT}}}, Type: tctx.StringT, Span: sp},
							{Kind: cfg.OpInitAssign, Place: cfg.Place{RootSymID: sinkSym.ID, RootName: "sink", RootType: tctx.StringT, ElemType: tctx.StringT}, Type: tctx.StringT, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{gVar.ID, sinkSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{gVar.ID, sinkSym.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			},
		},
	}

	view := &mockSemView{tctx: tctx, symTab: symTab}
	rep := diagnostic.NewReport()
	plan := drop.ElaborateProgram(progCFG, view, reg, rep)
	if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-MEM-007") {
		t.Fatalf("对自定义 Drop 结构体部分移动必须触发 WON-MEM-007，实际:\n%s", rep.String())
	}
	if plan.DropElaborated {
		t.Fatalf("触发 WON-MEM-007 时 DropElaborated 必须保持 false")
	}
}

func TestPhase2B3VerifierFailClosedInvariants(t *testing.T) {
	tctx := types.NewContext()
	symTab := symbols.NewTable(tctx)
	sp := diagnostic.Span{File: "verify.won", Start: diagnostic.Position{Line: 1, Column: 1, Byte: 0}, End: diagnostic.Position{Line: 1, Column: 10, Byte: 10}}

	cc := drop.NewClassifyContext(tctx, symTab, nil)
	strGlue, _ := cc.ResolveDropGlue(tctx.StringT)

	buildBaseFn := func() (*cfg.FuncCFG, symbols.SymbolID, symbols.SymbolID, cfg.Place, cfg.Place) {
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "inv_fn", sp)
		s1 := symTab.AllocSymbol(symbols.SymVar, "s1", sp)
		s1.Type = tctx.StringT
		s2 := symTab.AllocSymbol(symbols.SymVar, "s2", sp)
		s2.Type = tctx.StringT
		p1 := cfg.Place{RootSymID: s1.ID, RootName: "s1", RootType: tctx.StringT, ElemType: tctx.StringT}
		p2 := cfg.Place{RootSymID: s2.ID, RootName: "s2", RootType: tctx.StringT, ElemType: tctx.StringT}
		fn := &cfg.FuncCFG{
			Name:       "inv_fn",
			FuncSym:    fnSym,
			ReturnType: tctx.Unit,
			Entry:      0,
			Exit:       1,
			LocalOrder: []symbols.SymbolID{s1.ID, s2.ID},
			Locals: map[symbols.SymbolID]*cfg.LocalMeta{
				s1.ID: {Sym: s1, Type: tctx.StringT, ScopeID: 1, HasInit: true, Span: sp},
				s2.ID: {Sym: s2, Type: tctx.StringT, ScopeID: 1, HasInit: true, Span: sp},
			},
			Blocks: []*cfg.BasicBlock{
				{ID: 0, Label: "entry", Successors: []cfg.BlockID{1}, Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp}},
				{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
			},
		}
		return fn, s1.ID, s2.ID, p1, p2
	}

	view := &mockSemView{tctx: tctx, symTab: symTab}

	cases := []struct {
		name  string
		setup func() (*cfg.FuncCFG, *drop.DropElaboratedFunc)
	}{
		{
			name: "double_drop",
			setup: func() (*cfg.FuncCFG, *drop.DropElaboratedFunc) {
				fn, s1, _, p1, _ := buildBaseFn()
				fn.Blocks[0].Statements = []cfg.Statement{
					{Kind: cfg.OpInitAssign, Place: p1, Type: tctx.StringT, Span: sp},
					{Kind: cfg.OpDrop, Place: p1, Type: tctx.StringT, DropGlue: strGlue.ID, Span: sp},
					{Kind: cfg.OpDrop, Place: p1, Type: tctx.StringT, DropGlue: strGlue.ID, Span: sp},
					{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{s1}, Span: sp},
				}
				return fn, &drop.DropElaboratedFunc{SymID: fn.FuncSym.ID, Name: fn.Name, CFG: fn, Flags: map[drop.DropFlagID]*drop.DropFlagMeta{}}
			},
		},
		{
			name: "drop_after_move",
			setup: func() (*cfg.FuncCFG, *drop.DropElaboratedFunc) {
				fn, s1, _, p1, _ := buildBaseFn()
				fn.Blocks[0].Statements = []cfg.Statement{
					{Kind: cfg.OpInitAssign, Place: p1, Type: tctx.StringT, Span: sp},
					{Kind: cfg.OpMove, Place: p1, Type: tctx.StringT, Span: sp},
					{Kind: cfg.OpDrop, Place: p1, Type: tctx.StringT, DropGlue: strGlue.ID, Span: sp},
					{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{s1}, Span: sp},
				}
				return fn, &drop.DropElaboratedFunc{SymID: fn.FuncSym.ID, Name: fn.Name, CFG: fn, Flags: map[drop.DropFlagID]*drop.DropFlagMeta{}}
			},
		},
		{
			name: "drop_after_storage_dead",
			setup: func() (*cfg.FuncCFG, *drop.DropElaboratedFunc) {
				fn, s1, _, p1, _ := buildBaseFn()
				fn.Blocks[0].Statements = []cfg.Statement{
					{Kind: cfg.OpInitAssign, Place: p1, Type: tctx.StringT, Span: sp},
					{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{s1}, Span: sp},
					{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{s1}, Span: sp},
					{Kind: cfg.OpDrop, Place: p1, Type: tctx.StringT, DropGlue: strGlue.ID, Span: sp},
				}
				return fn, &drop.DropElaboratedFunc{SymID: fn.FuncSym.ID, Name: fn.Name, CFG: fn, Flags: map[drop.DropFlagID]*drop.DropFlagMeta{}}
			},
		},
		{
			name: "wrong_declaration_order_lifo",
			setup: func() (*cfg.FuncCFG, *drop.DropElaboratedFunc) {
				fn, s1, s2, p1, p2 := buildBaseFn()
				fn.Blocks[0].Statements = []cfg.Statement{
					{Kind: cfg.OpInitAssign, Place: p1, Type: tctx.StringT, Span: sp},
					{Kind: cfg.OpInitAssign, Place: p2, Type: tctx.StringT, Span: sp},
					{Kind: cfg.OpDrop, Place: p1, Type: tctx.StringT, DropGlue: strGlue.ID, Span: sp},
					{Kind: cfg.OpDrop, Place: p2, Type: tctx.StringT, DropGlue: strGlue.ID, Span: sp},
					{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{s1, s2}, Span: sp},
				}
				return fn, &drop.DropElaboratedFunc{SymID: fn.FuncSym.ID, Name: fn.Name, CFG: fn, Flags: map[drop.DropFlagID]*drop.DropFlagMeta{}}
			},
		},
		{
			name: "cond_drop_missing_flag",
			setup: func() (*cfg.FuncCFG, *drop.DropElaboratedFunc) {
				fn, s1, _, p1, _ := buildBaseFn()
				fn.Blocks[0].Statements = []cfg.Statement{
					{Kind: cfg.OpUninitDecl, Place: p1, Type: tctx.StringT, Span: sp},
					{Kind: cfg.OpCondDrop, Place: p1, Type: tctx.StringT, DropFlag: 0, DropGlue: strGlue.ID, Span: sp},
					{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{s1}, Span: sp},
				}
				return fn, &drop.DropElaboratedFunc{SymID: fn.FuncSym.ID, Name: fn.Name, CFG: fn, Flags: map[drop.DropFlagID]*drop.DropFlagMeta{}}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fn, df := tc.setup()
			rep := diagnostic.NewReport()
			ok := drop.VerifyElaboratedFuncForTest(fn, df, cc, view, rep)
			if ok || !rep.HasErrors() || !strings.Contains(rep.String(), "WON-INT-001") {
				t.Fatalf("%s 违例必须触发 WON-INT-001 且返回 false，实际 ok=%v err=%s", tc.name, ok, rep.String())
			}
		})
	}
	_ = fmt.Sprintf("verified %d invariant failure modes", len(cases))
}

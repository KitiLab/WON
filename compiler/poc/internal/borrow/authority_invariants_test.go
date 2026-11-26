package borrow

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

type stubBorrowSemView struct {
	types *types.Context
	syms  *symbols.Table
}

func (s *stubBorrowSemView) GetTypes() *types.Context            { return s.types }
func (s *stubBorrowSemView) GetSymbols() *symbols.Table          { return s.syms }
func (s *stubBorrowSemView) ExprType(ast.Expr) types.TypeID      { return types.Invalid }
func (s *stubBorrowSemView) CallTarget(ast.Expr) *symbols.Symbol { return nil }
func (s *stubBorrowSemView) FieldRef(ast.Node) *symbols.FieldInfo {
	return nil
}
func (s *stubBorrowSemView) VariantRef(ast.Node) *symbols.VariantInfo {
	return nil
}

func assertReportHasCode(t *testing.T, name string, rep *diagnostic.Report, code string) {
	t.Helper()
	if !rep.HasErrors() {
		t.Fatalf("%s 预期触发 %s，实际无错误", name, code)
	}
	for _, d := range rep.Diagnostics {
		if d.Code == code {
			return
		}
	}
	t.Fatalf("%s 期望诊断码 %s，实际:\n%s", name, code, rep.String())
}

// TestPhase2B2_2AuthorityResultAndParamChainInvariants 覆盖 Section 2 & 4：
// 验证 AccessAuthority 枚举区分、缺失元数据 Fail-Closed (WON-INT-001) 与投影类型一致性检查。
func TestPhase2B2_2AuthorityResultAndParamChainInvariants(t *testing.T) {
	tctx := types.NewContext()
	symTab := symbols.NewTable(tctx)
	sp := diagnostic.Span{File: "auth.won", Start: diagnostic.Position{Line: 1, Column: 1}, End: diagnostic.Position{Line: 1, Column: 10}}

	holderSym := symTab.AllocSymbol(symbols.SymStruct, "Holder", sp)
	holderSym.FieldMap = map[string]*symbols.FieldInfo{
		"value": {Name: "value", Type: tctx.I32, IsVar: true, Span: sp},
	}
	holderType := tctx.Struct(uint64(holderSym.ID), "Holder", nil)

	refMutI32 := tctx.Ref(true, tctx.I32)
	refSharedI32 := tctx.Ref(false, tctx.I32)
	refMutHolder := tctx.Ref(true, holderType)
	rawMutI32 := tctx.RawPtr(true, tctx.I32)
	refTuple := tctx.Ref(true, tctx.Tuple([]types.TypeID{tctx.I32, refSharedI32}))

	fnSym := symTab.AllocSymbol(symbols.SymFunc, "test_fn", sp)
	fnSym.ParamTypes = []types.TypeID{refMutI32, refSharedI32, refMutHolder, rawMutI32, refTuple}
	fnSym.ParamNames = []string{"m", "s", "h", "raw", "tup"}

	fn := &cfg.FuncCFG{Name: "test_fn", FuncSym: fnSym}

	// 1. 合法可变参数与合法结构体字段投影
	outMut := validateParamDerefChainAuthority(fn, 0, cfg.Place{
		Projections: []cfg.Projection{{Kind: cfg.ProjDeref, ElemType: tctx.I32}},
	}, tctx, symTab)
	if outMut.Internal || outMut.Authority != AuthorityMutable {
		t.Fatalf("合法 &mut 参数期望 AuthorityMutable，实际 %+v", outMut)
	}

	outField := validateParamDerefChainAuthority(fn, 2, cfg.Place{
		Projections: []cfg.Projection{
			{Kind: cfg.ProjDeref, ElemType: holderType},
			{Kind: cfg.ProjField, FieldName: "value", ElemType: tctx.I32},
		},
	}, tctx, symTab)
	if outField.Internal || outField.Authority != AuthorityMutable {
		t.Fatalf("合法 &mut Holder.value 投影期望 AuthorityMutable，实际 %+v", outField)
	}

	// 2. 共享参数 -> AuthorityShared (非 Internal)
	outShared := validateParamDerefChainAuthority(fn, 1, cfg.Place{
		Projections: []cfg.Projection{{Kind: cfg.ProjDeref, ElemType: tctx.I32}},
	}, tctx, symTab)
	if outShared.Internal || outShared.Authority != AuthorityShared {
		t.Fatalf("共享参数期望 AuthorityShared，实际 %+v", outShared)
	}

	// 3. *mut 原生指针 -> AuthorityUnsafeRawPtr
	outRaw := validateParamDerefChainAuthority(fn, 3, cfg.Place{
		Projections: []cfg.Projection{{Kind: cfg.ProjDeref, ElemType: tctx.I32}},
	}, tctx, symTab)
	if outRaw.Internal || outRaw.Authority != AuthorityUnsafeRawPtr || !outRaw.Authority.PermitsMutableAccess() {
		t.Fatalf("*mut 参数期望 AuthorityUnsafeRawPtr，实际 %+v", outRaw)
	}

	// 4. 元组投影里嵌套的共享 &i32 再解引用 -> AuthorityShared
	outTupleShared := validateParamDerefChainAuthority(fn, 4, cfg.Place{
		Projections: []cfg.Projection{
			{Kind: cfg.ProjTupleField, FieldIndex: 1, ElemType: refSharedI32},
			{Kind: cfg.ProjDeref, ElemType: tctx.I32},
		},
	}, tctx, symTab)
	if outTupleShared.Internal || outTupleShared.Authority != AuthorityShared {
		t.Fatalf("经由元组内共享引用解引用期望 AuthorityShared，实际 %+v", outTupleShared)
	}

	// 4.5 标量参数、无引用结构体、包含共享/可变引用字段的结构体、嵌套字段、未投影聚合与非法泛型替换
	plainStructSym := symTab.AllocSymbol(symbols.SymStruct, "Plain", sp)
	plainStructSym.FieldMap = map[string]*symbols.FieldInfo{"v": {Name: "v", Type: tctx.I32, Span: sp}}
	plainStructType := tctx.Struct(uint64(plainStructSym.ID), "Plain", nil)

	mixedStructSym := symTab.AllocSymbol(symbols.SymStruct, "MixedBox", sp)
	mixedStructSym.FieldMap = map[string]*symbols.FieldInfo{
		"ro": {Name: "ro", Type: refSharedI32, Span: sp},
		"rw": {Name: "rw", Type: refMutI32, Span: sp},
	}
	mixedStructType := tctx.Struct(uint64(mixedStructSym.ID), "MixedBox", nil)

	outerStructSym := symTab.AllocSymbol(symbols.SymStruct, "OuterBox", sp)
	outerStructSym.FieldMap = map[string]*symbols.FieldInfo{
		"inner": {Name: "inner", Type: mixedStructType, Span: sp},
	}
	outerStructType := tctx.Struct(uint64(outerStructSym.ID), "OuterBox", nil)

	genBoxSym := symTab.AllocSymbol(symbols.SymStruct, "GenBox", sp)
	tpSym := symTab.AllocSymbol(symbols.SymTypeParam, "T", sp)
	genBoxSym.GenericParamIDs = []symbols.SymbolID{tpSym.ID}
	genBoxSym.FieldMap = map[string]*symbols.FieldInfo{
		"item": {Name: "item", Type: tctx.TypeParam(uint64(tpSym.ID), "T"), Span: sp},
	}
	// 故意传入 0 个 GenericArgs（与 GenericParamIDs=1 不匹配）及包含 Invalid 泛型实参
	badGenCountType := tctx.Struct(uint64(genBoxSym.ID), "GenBox", nil)
	badGenInvalidArgType := tctx.Struct(uint64(genBoxSym.ID), "GenBox", []types.TypeID{types.Invalid})

	constRawI32 := tctx.RawPtr(false, tctx.I32)
	extFnSym := symTab.AllocSymbol(symbols.SymFunc, "ext_param_cases", sp)
	extFnSym.ParamTypes = []types.TypeID{
		tctx.I32,             // 0: 标量
		plainStructType,      // 1: 无引用结构体
		mixedStructType,      // 2: 含 & 与 &mut 的结构体
		outerStructType,      // 3: 嵌套结构体
		constRawI32,          // 4: *const i32
		badGenCountType,      // 5: 泛型参数数量失配
		badGenInvalidArgType, // 6: 泛型实参 Invalid
	}
	extFn := &cfg.FuncCFG{Name: "ext_param_cases", FuncSym: extFnSym}

	if out := validateParamDerefChainAuthority(extFn, 0, cfg.Place{}, tctx, symTab); !out.Internal || out.Authority != AuthorityUnknown {
		t.Fatalf("标量参数不得授予可变引用权限，实际 %+v", out)
	}
	if out := validateParamDerefChainAuthority(extFn, 1, cfg.Place{}, tctx, symTab); !out.Internal || out.Authority != AuthorityUnknown {
		t.Fatalf("无引用结构体参数不得授予可变引用权限，实际 %+v", out)
	}
	if out := validateParamDerefChainAuthority(extFn, 1, cfg.Place{Projections: []cfg.Projection{{Kind: cfg.ProjField, FieldName: "v", ElemType: tctx.I32}}}, tctx, symTab); !out.Internal || out.Authority != AuthorityUnknown {
		t.Fatalf("无引用结构体投影至 i32 不得授予可变引用权限，实际 %+v", out)
	}
	if out := validateParamDerefChainAuthority(extFn, 2, cfg.Place{}, tctx, symTab); !out.Internal || out.Authority != AuthorityUnknown {
		t.Fatalf("未精确定位引用字段的 MixedBox 聚合不得默认授予可变权限，实际 %+v", out)
	}
	if out := validateParamDerefChainAuthority(extFn, 2, cfg.Place{Projections: []cfg.Projection{{Kind: cfg.ProjField, FieldName: "ro", ElemType: refSharedI32}}}, tctx, symTab); out.Internal || out.Authority != AuthorityShared {
		t.Fatalf("MixedBox.ro (&i32) 期望 AuthorityShared，实际 %+v", out)
	}
	if out := validateParamDerefChainAuthority(extFn, 2, cfg.Place{Projections: []cfg.Projection{{Kind: cfg.ProjField, FieldName: "rw", ElemType: refMutI32}}}, tctx, symTab); out.Internal || out.Authority != AuthorityMutable {
		t.Fatalf("MixedBox.rw (&mut i32) 期望 AuthorityMutable，实际 %+v", out)
	}
	if out := validateParamDerefChainAuthority(extFn, 3, cfg.Place{Projections: []cfg.Projection{
		{Kind: cfg.ProjField, FieldName: "inner", ElemType: mixedStructType},
		{Kind: cfg.ProjField, FieldName: "rw", ElemType: refMutI32},
	}}, tctx, symTab); out.Internal || out.Authority != AuthorityMutable {
		t.Fatalf("嵌套结构体 OuterBox.inner.rw 期望 AuthorityMutable，实际 %+v", out)
	}
	if out := validateParamDerefChainAuthority(extFn, 4, cfg.Place{Projections: []cfg.Projection{{Kind: cfg.ProjDeref, ElemType: tctx.I32}}}, tctx, symTab); out.Internal || out.Authority != AuthorityShared {
		t.Fatalf("*const 原生指针期望 AuthorityShared，实际 %+v", out)
	}
	if out := validateParamDerefChainAuthority(extFn, 5, cfg.Place{Projections: []cfg.Projection{{Kind: cfg.ProjField, FieldName: "item", ElemType: tctx.I32}}}, tctx, symTab); !out.Internal || out.Authority != AuthorityUnknown {
		t.Fatalf("泛型实参个数不匹配应返回 Internal/Unknown，实际 %+v", out)
	}
	if out := validateParamDerefChainAuthority(extFn, 6, cfg.Place{Projections: []cfg.Projection{{Kind: cfg.ProjField, FieldName: "item", ElemType: tctx.I32}}}, tctx, symTab); !out.Internal || out.Authority != AuthorityUnknown {
		t.Fatalf("泛型实参 Invalid 应返回 Internal/Unknown，实际 %+v", out)
	}

	// 5. 各类元数据缺失/损坏 -> AuthorityUnknown + Internal=true (WON-INT-001)
	if out := validateParamDerefChainAuthority(nil, 0, cfg.Place{}, tctx, symTab); !out.Internal || out.Authority != AuthorityUnknown {
		t.Fatalf("nil fn 应返回 Internal/Unknown，实际 %+v", out)
	}
	if out := validateParamDerefChainAuthority(&cfg.FuncCFG{Name: "nofn"}, 0, cfg.Place{}, tctx, symTab); !out.Internal || out.Authority != AuthorityUnknown {
		t.Fatalf("缺失 FuncSym 应返回 Internal/Unknown，实际 %+v", out)
	}
	if out := validateParamDerefChainAuthority(fn, 0, cfg.Place{}, nil, symTab); !out.Internal || out.Authority != AuthorityUnknown {
		t.Fatalf("缺失 TypeContext 应返回 Internal/Unknown，实际 %+v", out)
	}
	if out := validateParamDerefChainAuthority(fn, -1, cfg.Place{}, tctx, symTab); !out.Internal || out.Authority != AuthorityUnknown {
		t.Fatalf("负下标应返回 Internal/Unknown，实际 %+v", out)
	}
	if out := validateParamDerefChainAuthority(fn, 99, cfg.Place{}, tctx, symTab); !out.Internal || out.Authority != AuthorityUnknown {
		t.Fatalf("越界下标应返回 Internal/Unknown，实际 %+v", out)
	}
	badTypeFnSym := symTab.AllocSymbol(symbols.SymFunc, "bad_type", sp)
	badTypeFnSym.ParamTypes = []types.TypeID{types.Invalid}
	if out := validateParamDerefChainAuthority(&cfg.FuncCFG{Name: "bad_type", FuncSym: badTypeFnSym}, 0, cfg.Place{}, tctx, symTab); !out.Internal || out.Authority != AuthorityUnknown {
		t.Fatalf("Invalid 参数类型应返回 Internal/Unknown，实际 %+v", out)
	}
	if out := validateParamDerefChainAuthority(fn, 0, cfg.Place{
		Projections: []cfg.Projection{{Kind: cfg.ProjectionKind(99)}},
	}, tctx, symTab); !out.Internal || out.Authority != AuthorityUnknown {
		t.Fatalf("未知 ProjectionKind 应返回 Internal/Unknown，实际 %+v", out)
	}
	if out := validateParamDerefChainAuthority(fn, 2, cfg.Place{
		Projections: []cfg.Projection{
			{Kind: cfg.ProjDeref, ElemType: holderType},
			{Kind: cfg.ProjField, FieldName: "missing_field", ElemType: tctx.I32},
		},
	}, tctx, symTab); !out.Internal || out.Authority != AuthorityUnknown {
		t.Fatalf("不存在的字段投影应返回 Internal/Unknown，实际 %+v", out)
	}
	if out := validateParamDerefChainAuthority(fn, 2, cfg.Place{
		Projections: []cfg.Projection{
			{Kind: cfg.ProjDeref, ElemType: holderType},
			{Kind: cfg.ProjField, FieldName: "value", ElemType: tctx.Bool},
		},
	}, tctx, symTab); !out.Internal || out.Authority != AuthorityUnknown {
		t.Fatalf("字段投影 ElemType 不一致应返回 Internal/Unknown，实际 %+v", out)
	}
}

// TestPhase2B2_2ParentLoanAndOriginSetFailClosed 覆盖 Section 3 & 4：
// 验证 ParentLoans 身份校验（零 ID、缺失、跨函数、环、逆序）以及 OriginSet 空/Unknown/Static/RawPtr 校验。
func TestPhase2B2_2ParentLoanAndOriginSetFailClosed(t *testing.T) {
	tctx := types.NewContext()
	symTab := symbols.NewTable(tctx)
	sp := diagnostic.Span{File: "loans.won", Start: diagnostic.Position{Line: 5, Column: 1}, End: diagnostic.Position{Line: 5, Column: 10}}

	fnSym := symTab.AllocSymbol(symbols.SymFunc, "owner", sp)
	fnSym.ParamTypes = []types.TypeID{tctx.I32}
	fnSym.ReturnType = tctx.Unit
	otherSym := symTab.AllocSymbol(symbols.SymFunc, "other", sp)

	xSym := symTab.AllocSymbol(symbols.SymVar, "x", sp)
	xSym.Type = tctx.I32
	rSym := symTab.AllocSymbol(symbols.SymVar, "r", sp)
	rSym.Type = tctx.Ref(true, tctx.I32)

	baseFn := func() *cfg.FuncCFG {
		return &cfg.FuncCFG{
			Name:    "owner",
			FuncSym: fnSym,
			Entry:   0,
			Locals: map[symbols.SymbolID]*cfg.LocalMeta{
				xSym.ID: {Sym: xSym, Type: tctx.I32},
				rSym.ID: {Sym: rSym, Type: tctx.Ref(true, tctx.I32)},
			},
			Blocks: []*cfg.BasicBlock{
				{
					ID:    0,
					Label: "entry",
					Statements: []cfg.Statement{
						{Kind: cfg.OpNop, Span: sp},
						{Kind: cfg.OpNop, Span: sp},
					},
					Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp},
				},
			},
		}
	}

	rPlace := cfg.Place{RootSymID: rSym.ID, RootName: "r"}
	xPlace := cfg.Place{RootSymID: xSym.ID, RootName: "x"}
	derefR := cfg.Place{RootSymID: rSym.ID, RootName: "r", Projections: []cfg.Projection{{Kind: cfg.ProjDeref, ElemType: tctx.I32}}}

	// 1. 合法可重借用父借用
	fn := baseFn()
	flow := &FuncLoanFlow{
		FuncSymID: fnSym.ID,
		LoanByID: map[cfg.LoanID]*LoanInfo{
			1: {ID: 1, FuncSymID: fnSym.ID, Kind: cfg.BorrowMut, Place: xPlace, Type: tctx.Ref(true, tctx.I32), IsMut: true, CreatedAt: cfg.ProgramPoint{Block: 0, StmtIdx: 0}, Carriers: map[symbols.SymbolID]bool{rSym.ID: true}},
			2: {ID: 2, FuncSymID: fnSym.ID, Kind: cfg.BorrowReborrowMut, Place: derefR, Type: tctx.Ref(true, tctx.I32), IsMut: true, IsReborrow: true, ParentPlace: rPlace, ParentLoans: map[cfg.LoanID]bool{1: true}, CreatedAt: cfg.ProgramPoint{Block: 0, StmtIdx: 1}},
		},
	}
	if out := validateParentLoansIdentity(fn, flow, flow.LoanByID[2], []bool{true}, tctx); out.Internal || out.Authority != AuthorityMutable {
		t.Fatalf("合法重借用父借用校验失败: %+v", out)
	}

	// 2. 缺失父 LoanInfo -> WON-INT-001
	flowMissing := &FuncLoanFlow{
		FuncSymID: fnSym.ID,
		LoanByID: map[cfg.LoanID]*LoanInfo{
			2: {ID: 2, FuncSymID: fnSym.ID, Kind: cfg.BorrowReborrowMut, Place: derefR, IsMut: true, IsReborrow: true, ParentPlace: rPlace, ParentLoans: map[cfg.LoanID]bool{99: true}, CreatedAt: cfg.ProgramPoint{Block: 0, StmtIdx: 1}},
		},
	}
	if out := validateParentLoansIdentity(fn, flowMissing, flowMissing.LoanByID[2], []bool{true}, tctx); !out.Internal {
		t.Fatalf("缺失父 LoanInfo 应触发 Internal/WON-INT-001，实际 %+v", out)
	}

	// 3. 零值 InvalidLoan (0) -> WON-INT-001
	flowZero := &FuncLoanFlow{
		FuncSymID: fnSym.ID,
		LoanByID: map[cfg.LoanID]*LoanInfo{
			2: {ID: 2, FuncSymID: fnSym.ID, Kind: cfg.BorrowReborrowMut, Place: derefR, IsMut: true, IsReborrow: true, ParentPlace: rPlace, ParentLoans: map[cfg.LoanID]bool{0: true}, CreatedAt: cfg.ProgramPoint{Block: 0, StmtIdx: 1}},
		},
	}
	if out := validateParentLoansIdentity(fn, flowZero, flowZero.LoanByID[2], []bool{true}, tctx); !out.Internal {
		t.Fatalf("零父 LoanID 应触发 Internal/WON-INT-001，实际 %+v", out)
	}

	// 4. 跨函数父借用 -> WON-INT-001
	flowForeign := &FuncLoanFlow{
		FuncSymID: fnSym.ID,
		LoanByID: map[cfg.LoanID]*LoanInfo{
			1: {ID: 1, FuncSymID: otherSym.ID, Kind: cfg.BorrowMut, Place: xPlace, IsMut: true, CreatedAt: cfg.ProgramPoint{Block: 0, StmtIdx: 0}},
			2: {ID: 2, FuncSymID: fnSym.ID, Kind: cfg.BorrowReborrowMut, Place: derefR, IsMut: true, IsReborrow: true, ParentPlace: rPlace, ParentLoans: map[cfg.LoanID]bool{1: true}, CreatedAt: cfg.ProgramPoint{Block: 0, StmtIdx: 1}},
		},
	}
	if out := validateParentLoansIdentity(fn, flowForeign, flowForeign.LoanByID[2], []bool{true}, tctx); !out.Internal {
		t.Fatalf("跨函数父 LoanInfo 应触发 Internal/WON-INT-001，实际 %+v", out)
	}

	// 5. 父借用环 (L2 -> L3 -> L2) -> WON-INT-001
	flowCycle := &FuncLoanFlow{
		FuncSymID: fnSym.ID,
		LoanByID: map[cfg.LoanID]*LoanInfo{
			2: {ID: 2, FuncSymID: fnSym.ID, Kind: cfg.BorrowReborrowMut, Place: derefR, IsMut: true, IsReborrow: true, ParentPlace: rPlace, ParentLoans: map[cfg.LoanID]bool{3: true}, CreatedAt: cfg.ProgramPoint{Block: 0, StmtIdx: 1}},
			3: {ID: 3, FuncSymID: fnSym.ID, Kind: cfg.BorrowReborrowMut, Place: derefR, IsMut: true, IsReborrow: true, ParentPlace: rPlace, ParentLoans: map[cfg.LoanID]bool{2: true}, CreatedAt: cfg.ProgramPoint{Block: 0, StmtIdx: 0}},
		},
	}
	if out := validateParentLoansIdentity(fn, flowCycle, flowCycle.LoanByID[2], []bool{true}, tctx); !out.Internal {
		t.Fatalf("重借用父子环应触发 Internal/WON-INT-001，实际 %+v", out)
	}

	// 6. 顺序倒置（直达块中父创建在子之后） -> WON-INT-001
	flowOutOfOrder := &FuncLoanFlow{
		FuncSymID: fnSym.ID,
		LoanByID: map[cfg.LoanID]*LoanInfo{
			1: {ID: 1, FuncSymID: fnSym.ID, Kind: cfg.BorrowMut, Place: xPlace, IsMut: true, CreatedAt: cfg.ProgramPoint{Block: 0, StmtIdx: 1}},
			2: {ID: 2, FuncSymID: fnSym.ID, Kind: cfg.BorrowReborrowMut, Place: derefR, IsMut: true, IsReborrow: true, ParentPlace: rPlace, ParentLoans: map[cfg.LoanID]bool{1: true}, CreatedAt: cfg.ProgramPoint{Block: 0, StmtIdx: 0}},
		},
	}
	if out := validateParentLoansIdentity(fn, flowOutOfOrder, flowOutOfOrder.LoanByID[2], []bool{true}, tctx); !out.Internal {
		t.Fatalf("创建顺序倒置应触发 Internal/WON-INT-001，实际 %+v", out)
	}

	// 7. 共享父借用 -> 可变子借用 -> AuthorityShared (WON-MEM-004, 非 Internal)；共享父 -> 共享子 -> 允许
	flowSharedParent := &FuncLoanFlow{
		FuncSymID: fnSym.ID,
		LoanByID: map[cfg.LoanID]*LoanInfo{
			1: {ID: 1, FuncSymID: fnSym.ID, Kind: cfg.BorrowShared, Place: xPlace, Type: tctx.Ref(false, tctx.I32), IsMut: false, CreatedAt: cfg.ProgramPoint{Block: 0, StmtIdx: 0}},
			2: {ID: 2, FuncSymID: fnSym.ID, Kind: cfg.BorrowReborrowMut, Place: derefR, Type: tctx.Ref(true, tctx.I32), IsMut: true, IsReborrow: true, ParentPlace: rPlace, ParentLoans: map[cfg.LoanID]bool{1: true}, CreatedAt: cfg.ProgramPoint{Block: 0, StmtIdx: 1}},
			3: {ID: 3, FuncSymID: fnSym.ID, Kind: cfg.BorrowReborrowShared, Place: derefR, Type: tctx.Ref(false, tctx.I32), IsMut: false, IsReborrow: true, ParentPlace: rPlace, ParentLoans: map[cfg.LoanID]bool{1: true}, CreatedAt: cfg.ProgramPoint{Block: 0, StmtIdx: 1}},
		},
	}
	if out := validateParentLoansIdentity(fn, flowSharedParent, flowSharedParent.LoanByID[2], []bool{true}, tctx); out.Internal || out.Authority != AuthorityShared {
		t.Fatalf("共享父借用->可变子期望 AuthorityShared，实际 %+v", out)
	}
	if out := validateParentLoansIdentity(fn, flowSharedParent, flowSharedParent.LoanByID[3], []bool{true}, tctx); out.Internal || out.Authority != AuthorityMutable {
		t.Fatalf("共享父借用->共享子重借用应校验合法，实际 %+v", out)
	}

	// 7.5 强制 Parent/Child LoanInfo 类型、BorrowKind、FromRawPtr 与统一 FunctionID 不变量
	metaFailCases := []struct {
		name  string
		modFn func(*cfg.FuncCFG, *FuncLoanFlow) (*LoanInfo, *types.Context)
	}{
		{"ParentTypeInvalid", func(f *cfg.FuncCFG, fl *FuncLoanFlow) (*LoanInfo, *types.Context) {
			fl.LoanByID[1].Type = types.Invalid
			return fl.LoanByID[2], tctx
		}},
		{"NilTypeContext", func(f *cfg.FuncCFG, fl *FuncLoanFlow) (*LoanInfo, *types.Context) {
			return fl.LoanByID[2], nil
		}},
		{"ParentTypeScalar", func(f *cfg.FuncCFG, fl *FuncLoanFlow) (*LoanInfo, *types.Context) {
			fl.LoanByID[1].Type = tctx.I32
			return fl.LoanByID[2], tctx
		}},
		{"ParentElemTypeInvalid", func(f *cfg.FuncCFG, fl *FuncLoanFlow) (*LoanInfo, *types.Context) {
			fl.LoanByID[1].Type = tctx.Ref(true, types.Invalid)
			return fl.LoanByID[2], tctx
		}},
		{"ParentIsMutContradictsType", func(f *cfg.FuncCFG, fl *FuncLoanFlow) (*LoanInfo, *types.Context) {
			fl.LoanByID[1].Type = tctx.Ref(false, tctx.I32)
			fl.LoanByID[1].IsMut = true
			return fl.LoanByID[2], tctx
		}},
		{"ParentBorrowKindContradictsMut", func(f *cfg.FuncCFG, fl *FuncLoanFlow) (*LoanInfo, *types.Context) {
			fl.LoanByID[1].Kind = cfg.BorrowShared
			fl.LoanByID[1].IsMut = true
			fl.LoanByID[1].Type = tctx.Ref(true, tctx.I32)
			return fl.LoanByID[2], tctx
		}},
		{"ChildTypeInvalid", func(f *cfg.FuncCFG, fl *FuncLoanFlow) (*LoanInfo, *types.Context) {
			fl.LoanByID[2].Type = types.Invalid
			return fl.LoanByID[2], tctx
		}},
		{"ParentFuncSymIDZero", func(f *cfg.FuncCFG, fl *FuncLoanFlow) (*LoanInfo, *types.Context) {
			fl.LoanByID[1].FuncSymID = 0
			return fl.LoanByID[2], tctx
		}},
		{"FlowFuncSymIDZero", func(f *cfg.FuncCFG, fl *FuncLoanFlow) (*LoanInfo, *types.Context) {
			fl.FuncSymID = 0
			return fl.LoanByID[2], tctx
		}},
		{"FlowFuncSymIDMismatchFn", func(f *cfg.FuncCFG, fl *FuncLoanFlow) (*LoanInfo, *types.Context) {
			fl.FuncSymID = otherSym.ID
			return fl.LoanByID[2], tctx
		}},
		{"FnFuncSymNil", func(f *cfg.FuncCFG, fl *FuncLoanFlow) (*LoanInfo, *types.Context) {
			f.FuncSym = nil
			return fl.LoanByID[2], tctx
		}},
		{"FromRawPtrForgedOnSafeMutLoan", func(f *cfg.FuncCFG, fl *FuncLoanFlow) (*LoanInfo, *types.Context) {
			fl.LoanByID[1].FromRawPtr = true
			return fl.LoanByID[2], tctx
		}},
		{"FromRawPtrForgedOnSafeSharedLoan", func(f *cfg.FuncCFG, fl *FuncLoanFlow) (*LoanInfo, *types.Context) {
			fl.LoanByID[1].Kind = cfg.BorrowShared
			fl.LoanByID[1].IsMut = false
			fl.LoanByID[1].Type = tctx.Ref(false, tctx.I32)
			fl.LoanByID[1].FromRawPtr = true
			return fl.LoanByID[2], tctx
		}},
	}
	for _, mc := range metaFailCases {
		t.Run(mc.name, func(t *testing.T) {
			f := baseFn()
			fl := &FuncLoanFlow{
				FuncSymID: fnSym.ID,
				LoanByID: map[cfg.LoanID]*LoanInfo{
					1: {ID: 1, FuncSymID: fnSym.ID, Kind: cfg.BorrowMut, Place: xPlace, Type: tctx.Ref(true, tctx.I32), IsMut: true, CreatedAt: cfg.ProgramPoint{Block: 0, StmtIdx: 0}, Carriers: map[symbols.SymbolID]bool{rSym.ID: true}},
					2: {ID: 2, FuncSymID: fnSym.ID, Kind: cfg.BorrowReborrowMut, Place: derefR, Type: tctx.Ref(true, tctx.I32), IsMut: true, IsReborrow: true, ParentPlace: rPlace, ParentLoans: map[cfg.LoanID]bool{1: true}, CreatedAt: cfg.ProgramPoint{Block: 0, StmtIdx: 1}},
				},
			}
			ch, useTctx := mc.modFn(f, fl)
			if out := validateParentLoansIdentity(f, fl, ch, []bool{true}, useTctx); !out.Internal || out.Authority != AuthorityUnknown {
				t.Fatalf("%s 必须触发 Internal/WON-INT-001，实际 %+v", mc.name, out)
			}
		})
	}

	// 8. OriginSet 空 / Unknown / Static / 原生指针一致性校验
	if out := validateCarrierOriginSet(fn, flow, OriginSet{}, derefR, true, true, tctx, symTab); !out.Internal {
		t.Fatalf("需要父载体的空 OriginSet 应触发 Internal/WON-INT-001，实际 %+v", out)
	}
	if out := validateCarrierOriginSet(fn, flow, OriginSet{Unknown: true}, derefR, true, true, tctx, symTab); !out.Internal || out.Authority.PermitsMutableAccess() {
		t.Fatalf("无原生指针背书的孤立 Unknown OriginSet 必须触发 Internal 且不得授权可变访问，实际 %+v", out)
	}
	if out := validateCarrierOriginSet(fn, flow, OriginSet{Static: true}, derefR, true, true, tctx, symTab); out.Authority != AuthorityShared {
		t.Fatalf("Static 来源请求可变权限应返回 AuthorityShared，实际 %+v", out)
	}
	rawPtrSym := symTab.AllocSymbol(symbols.SymVar, "p_raw", sp)
	rawPtrSym.Type = tctx.RawPtr(true, tctx.I32)
	derefRawMut := cfg.Place{RootSymID: rawPtrSym.ID, RootName: "p_raw", RootType: tctx.RawPtr(true, tctx.I32), ElemType: tctx.I32, Projections: []cfg.Projection{{Kind: cfg.ProjDeref, ElemType: tctx.I32}}}
	rawFlow := &FuncLoanFlow{
		FuncSymID: fnSym.ID,
		LoanByID: map[cfg.LoanID]*LoanInfo{
			10: {ID: 10, FuncSymID: fnSym.ID, Kind: cfg.BorrowMut, Place: derefRawMut, Type: tctx.Ref(true, tctx.I32), IsMut: true, FromRawPtr: true},
			11: {ID: 11, FuncSymID: fnSym.ID, Kind: cfg.BorrowMut, Place: derefRawMut, Type: tctx.Ref(true, tctx.I32), IsMut: true, FromRawPtr: false},
		},
	}
	if out := validateCarrierOriginSet(fn, rawFlow, OriginSet{Loans: map[cfg.LoanID]bool{10: true}, Unknown: true}, derefRawMut, true, true, tctx, symTab); out.Internal || out.Authority != AuthorityUnsafeRawPtr {
		t.Fatalf("*mut 原生指针贷款背书的 OriginSet 期望 AuthorityUnsafeRawPtr，实际 %+v", out)
	}
	if out := validateCarrierOriginSet(fn, rawFlow, OriginSet{Loans: map[cfg.LoanID]bool{11: true}, Unknown: true}, derefRawMut, true, true, tctx, symTab); !out.Internal {
		t.Fatalf("原生指针解引用借用缺少 FromRawPtr 标记应触发 Internal/WON-INT-001，实际 %+v", out)
	}
}

// TestPhase2B2_2ReturnCallAndOpaqueMetadataFailClosed 覆盖 Section 5, 6, 7：
// 验证 checkReturnOriginsValid、checkCallProvenanceAndDisjointness 与 checkOpaqueSignatures 对损坏/缺失元数据严格触发 WON-INT-001。
func TestPhase2B2_2ReturnCallAndOpaqueMetadataFailClosed(t *testing.T) {
	tctx := types.NewContext()
	symTab := symbols.NewTable(tctx)
	sp := diagnostic.Span{File: "meta.won", Start: diagnostic.Position{Line: 3, Column: 1}, End: diagnostic.Position{Line: 3, Column: 15}}

	refMut := tctx.Ref(true, tctx.I32)
	fnSym := symTab.AllocSymbol(symbols.SymFunc, "ret_mut", sp)
	fnSym.ParamTypes = []types.TypeID{refMut}
	fnSym.ParamNames = []string{"p"}
	fnSym.ReturnType = refMut

	flow := &FuncLoanFlow{FuncSymID: fnSym.ID, LoanByID: map[cfg.LoanID]*LoanInfo{}}
	st := cfg.Statement{Kind: cfg.OpReturn, Span: sp}

	// 1. checkReturnOriginsValid: FuncSym == nil -> WON-INT-001
	rep1 := diagnostic.NewReport()
	ok := checkReturnOriginsValid(&cfg.FuncCFG{Name: "ret_mut", FuncSym: nil}, st, "", true, OriginSet{Params: map[int]bool{0: true}}, flow, tctx, symTab, rep1, map[diagDedupKey]bool{})
	if ok {
		t.Fatalf("FuncSym==nil 不得放行")
	}
	assertReportHasCode(t, "ReturnMissingFuncSym", rep1, "WON-INT-001")

	// 2. checkReturnOriginsValid: 越界 pIdx -> WON-INT-001
	rep2 := diagnostic.NewReport()
	ok = checkReturnOriginsValid(&cfg.FuncCFG{Name: "ret_mut", FuncSym: fnSym}, st, "", true, OriginSet{Params: map[int]bool{3: true}}, flow, tctx, symTab, rep2, map[diagDedupKey]bool{})
	if ok {
		t.Fatalf("越界 ParamIdx 不得放行")
	}
	assertReportHasCode(t, "ReturnInvalidParamIdx", rep2, "WON-INT-001")

	// 3. checkReturnOriginsValid: Static 返回 &mut -> WON-MEM-004
	rep3 := diagnostic.NewReport()
	checkReturnOriginsValid(&cfg.FuncCFG{Name: "ret_mut", FuncSym: fnSym}, st, "", true, OriginSet{Static: true}, flow, tctx, symTab, rep3, map[diagDedupKey]bool{})
	assertReportHasCode(t, "ReturnStaticAsMut", rep3, "WON-MEM-004")

	// 4. checkCallProvenanceAndDisjointness: 缺失 ArgTemps ValueID 或无 OriginSet -> WON-INT-001
	calleeSym := symTab.AllocSymbol(symbols.SymFunc, "pick2", sp)
	calleeSym.ParamTypes = []types.TypeID{refMut, refMut}
	calleeSym.ReturnType = refMut

	calleeCFG := &cfg.FuncCFG{Name: "pick2", FuncSym: calleeSym}
	callerCFG := &cfg.FuncCFG{Name: "caller", FuncSym: fnSym}
	progCFG := &cfg.ProgramCFG{
		Functions: []*cfg.FuncCFG{calleeCFG, callerCFG},
		BySymbol: map[symbols.SymbolID]*cfg.FuncCFG{
			calleeSym.ID: calleeCFG,
			fnSym.ID:     callerCFG,
		},
	}
	summaries := map[symbols.SymbolID]*FuncSummary{
		calleeSym.ID: {
			SymID:       calleeSym.ID,
			Name:        "pick2",
			ReturnsRef:  true,
			ReturnIsMut: true,
			FieldProvs: map[string]*FieldProvenance{
				"": {Path: "", IsMut: true, ParamIndices: map[int]bool{0: true, 1: true}},
			},
		},
	}

	rep4 := diagnostic.NewReport()
	fwd4 := newCarrierEnv()
	fwd4.temps[1] = OriginSet{Params: map[int]bool{0: true}}
	callMissingTemp := cfg.Statement{
		Kind:       cfg.OpCall,
		Callee:     calleeSym,
		CalleeName: "pick2",
		ArgTemps:   []cfg.ValueID{1, cfg.InvalidValue},
		ValTemp:    3,
		Type:       refMut,
		Span:       sp,
	}
	checkCallProvenanceAndDisjointness(progCFG, callerCFG, callMissingTemp, fwd4, flow, summaries, tctx, symTab, rep4, map[diagDedupKey]bool{})
	assertReportHasCode(t, "CallMissingCandidateArgTemp", rep4, "WON-INT-001")

	rep5 := diagnostic.NewReport()
	badIndexSummaries := map[symbols.SymbolID]*FuncSummary{
		calleeSym.ID: {
			SymID:       calleeSym.ID,
			Name:        "pick2",
			ReturnsRef:  true,
			ReturnIsMut: true,
			FieldProvs: map[string]*FieldProvenance{
				"": {Path: "", IsMut: true, ParamIndices: map[int]bool{0: true, 5: true}},
			},
		},
	}
	callValidTemps := cfg.Statement{
		Kind:       cfg.OpCall,
		Callee:     calleeSym,
		CalleeName: "pick2",
		ArgTemps:   []cfg.ValueID{1, 2},
		ValTemp:    3,
		Type:       refMut,
		Span:       sp,
	}
	fwd5 := newCarrierEnv()
	fwd5.temps[1] = OriginSet{Params: map[int]bool{0: true}}
	fwd5.temps[2] = OriginSet{Params: map[int]bool{0: true}}
	checkCallProvenanceAndDisjointness(progCFG, callerCFG, callValidTemps, fwd5, flow, badIndexSummaries, tctx, symTab, rep5, map[diagDedupKey]bool{})
	assertReportHasCode(t, "CallOutOfRangeCandidateIdx", rep5, "WON-INT-001")

	// 5. checkOpaqueSignatures: 缺失 ReturnType 或 ParamType -> WON-INT-001
	tctxOpaque := types.NewContext()
	symOpaque := symbols.NewTable(tctxOpaque)
	badOp := symOpaque.AllocSymbol(symbols.SymFunc, "opaque_missing_type", sp)
	badOp.ParamTypes = []types.TypeID{types.Invalid}
	badOp.ReturnType = tctxOpaque.Ref(false, tctxOpaque.I32)
	rep6 := diagnostic.NewReport()
	checkOpaqueSignatures(&cfg.ProgramCFG{}, symOpaque, tctxOpaque, rep6, map[diagDedupKey]bool{})
	assertReportHasCode(t, "OpaqueMissingParamType", rep6, "WON-INT-001")
}

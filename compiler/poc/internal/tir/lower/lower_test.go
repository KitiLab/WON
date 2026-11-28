package lower_test

import (
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/drop"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/tir"
	"github.com/KitiLab/WON/compiler/poc/internal/tir/lower"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

func compileAndLower(t *testing.T, src string, bindings map[string]*bindgen.BindingMeta) (*checker.TypedProgram, *tir.Program, *diagnostic.Report) {
	t.Helper()
	rep := diagnostic.NewReport()
	lex := lexer.New("tir_test.won", src, rep)
	p := parser.New(lex, rep, "tir_test.won")
	prog := p.ParseProgram()
	if rep.HasErrors() {
		return nil, nil, rep
	}
	chk := checker.New(rep, bindings)
	tp := chk.CheckProgram(prog)
	if rep.HasErrors() {
		return tp, nil, rep
	}
	tirProg, err := lower.LowerProgram(tp, bindings)
	if err != nil {
		t.Fatalf("合法源码 TIR 降级失败: %v\n源码:\n%s", err, src)
	}
	if errs := tir.Verify(tirProg); len(errs) != 0 {
		t.Fatalf("生成的 TIR 未通过 tir.Verify:\n%v\nDump:\n%s", tir.VerifyErrorSummary(errs), tirProg.Dump())
	}
	return tp, tirProg, rep
}

func TestPhase2C0_LowerIRConstructionAndControlFlow(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantDump []string
	}{
		{
			name:     "00_empty_module",
			src:      ``,
			wantDump: []string{"tir.Program ownership_ok=true drop_ok=true"},
		},
		{
			name: "01_empty_minimal_fn",
			src: `fn empty():
    return
`,
			wantDump: []string{"fn @f1 empty() -> ", "Return"},
		},
		{
			name: "02_07_params_ops_casts_calls_compound_tail_ret",
			src: `fn tail_add(x: i32) -> i32:
    x + 1

fn helper(a: i32, b: i32) -> i32:
    let x = -a
    let y = ~b
    let z = (x + y) * 2
    return z

fn main() -> i32:
    var total: i32 = 10
    let uninit_flag: bool
    uninit_flag = not false
    let c: i64 = (total as i64) + 1i64
    if uninit_flag and (c > 0i64):
        total += helper(tail_add(2), 4)
    else:
        total = 0
    return total
`,
			wantDump: []string{
				"Binary +",
				"Unary -",
				"Unary ~",
				"Binary *",
				"Cast[Numeric]",
				"BranchIf",
				"Call @f1(tail_add)",
				"Call @f2(helper)",
			},
		},
		{
			name: "09_13_structs_tuples_enums_arrays_projections_ptrs",
			src: `struct Pair:
    first: i32
    second: String

enum Shape:
    Zero
    Circle(f64)
    Rect:
        w: i32
        h: i32

fn inspect(p: &Pair, mut_p: &mut Pair, raw: *const u8, mut_raw: *mut u8, idx: usize) -> i32:
    let s1 = String::from("a")
    let mk_pair = Pair { first: 7i32, second: s1 }
    let a = [1i32, 2i32, 3i32]
    let b = [0u8; 8]
    let tup = (10i32, true)
    let (tw, tb) = tup
    let f = p.first
    let v0 = a[0]
    let vi = a[idx]
    let u = Shape::Zero
    let c = Shape::Circle(2.5)
    let r = Shape::Rect { w: 4, h: 5 }
    let first_copy = mut_p.first
    return f + v0 + vi + first_copy + tw + mk_pair.first
`,
			wantDump: []string{
				"Aggregate[Struct]",
				"Aggregate[Array]",
				"Aggregate[ArrayRepeat",
				"Aggregate[Tuple]",
				"Extract ",
				"Aggregate[EnumVariant variant=Zero#0]",
				"Aggregate[EnumVariant variant=Circle#1]",
				"Aggregate[EnumVariant variant=Rect#2]",
				".first",
				"[0]",
			},
		},
		{
			name: "16_17_20_21_while_break_continue_early_return_unreachable",
			src: `fn loops(limit: i32) -> i32:
    var i: i32 = 0
    while i < limit:
        i += 1
        if i == 2:
            continue
        if i == 5:
            break
    if i > 3:
        return i
    return 0
`,
			wantDump: []string{"BranchIf", "Goto bb", "Return"},
		},
		{
			name: "18_match_with_arms_and_guard",
			src: `enum Msg:
    Empty
    Code(i32)

fn eval_msg(m: Msg) -> i32:
    let base: i32 = 1
    let out: i32 = match m:
        Msg::Empty =>
            0i32
        Msg::Code(n) if n > 10 =>
            n + base
        Msg::Code(n) =>
            n
    return out
`,
			wantDump: []string{"Switch", "BranchIf", "ValueAssign"},
		},
		{
			name: "19_option_result_try_propagation",
			src: `fn unwrap_or_err(opt: Option[i32], res: Result[i32, i32]) -> Result[i32, i32]:
    let v = res?
    let sum = v + 1
    return Result::Ok(sum)

fn opt_chain(opt: Option[i32]) -> Option[i32]:
    let inner = opt?
    return Option::Some(inner + 2)
`,
			wantDump: []string{"Switch", "__try_unwrap", "__try_propagate_err"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, tirProg1, rep := compileAndLower(t, tc.src, nil)
			if rep.HasErrors() {
				t.Fatalf("意外编译错误:\n%s", rep.String())
			}
			_, tirProg2, _ := compileAndLower(t, tc.src, nil)
			d1 := tirProg1.Dump()
			d2 := tirProg2.Dump()
			if d1 != d2 {
				t.Fatalf("前端->TIR 降级必须完全确定性!\n=== 1 ===\n%s\n=== 2 ===\n%s", d1, d2)
			}
			for _, w := range tc.wantDump {
				if !strings.Contains(d1, w) {
					t.Fatalf("TIR Dump 缺少期望构造 %q:\n%s", w, d1)
				}
			}
		})
	}
}

func TestPhase2C0_LowerOwnershipAndDropPreservation(t *testing.T) {
	src := `struct Boxed:
    a: String
    b: String

fn pick_ref(x: &String) -> &String:
    return x

fn reborrow_demo(r: &mut String):
    let r2 = &mut *r
    r2.push_str("!")

fn loop_cleanup(flag: bool):
    while flag:
        let local_s = String::from("in_loop")
        println(local_s.len())
        if flag:
            break

fn ownership_and_drop(cond: bool, flag_box: bool) -> String:
    let static_view = "hello"
    var s1 = String::from("one")
    var s2 = String::from("two")
    let r = pick_ref(&s1)
    println(r)
    reborrow_demo(&mut s1)
    let moved_s2 = s2
    println(moved_s2)
    s2 = String::from("reinit_s2")
    var s3: String
    if cond:
        s3 = String::from("cond_init")
    else:
        println(static_view)
    s1 = String::from("replaced")
    let bx = Boxed { a: s1, b: s2 }
    if flag_box:
        let moved_a = bx.a
        println(moved_a)
    let res = String::from("ret")
    return res
`
	_, tirProg, rep := compileAndLower(t, src, nil)
	if rep.HasErrors() {
		t.Fatalf("合法所有权/Drop 用例失败:\n%s", rep.String())
	}
	dump := tirProg.Dump()
	for _, want := range []string{
		"glue g",
		"StaticRef \"hello\"",
		"Borrow[Shared loan=L",
		"Borrow[ReborrowMut loan=L",
		"Assign ",
		"CondDrop ",
		"DropFlagDecl ",
		"DropFlagSet ",
		"Drop _l",
	} {
		if !strings.Contains(dump, want) {
			t.Fatalf("TIR 所有权/Drop 输出缺少 %q:\n%s", want, dump)
		}
	}

	// 验证 Partial Aggregate '?' 清理保留前置成员的 Drop/Flag
	aggTrySrc := `struct Holder:
    name: String
    code: i32

fn parse_code(ok: bool) -> Result[i32, i32]:
    if ok:
        return Result::Ok(7)
    return Result::Err(1)

fn build_holder(ok: bool) -> Result[Holder, i32]:
    let h = Holder { name: String::from("ok"), code: parse_code(ok)? }
    return Result::Ok(h)
`
	_, aggTIR, repAgg := compileAndLower(t, aggTrySrc, nil)
	if repAgg.HasErrors() {
		t.Fatalf("Partial aggregate '?' 测试失败:\n%s", repAgg.String())
	}
	aggDump := aggTIR.Dump()
	if !strings.Contains(aggDump, "__agg_tmp_") {
		t.Fatalf("Partial aggregate '?' 应保留前置临时资源的局部清理槽:\n%s", aggDump)
	}
}

func TestPhase2C0_LowerFFIContractsAndPreservation(t *testing.T) {
	bindings := map[string]*bindgen.BindingMeta{
		"demo": {
			HeaderPath: "demo.h",
			Functions: map[string]bindgen.FunctionSig{
				"create_handle": {Name: "create_handle", ReturnType: "void", ParamTypes: nil},
				"borrow_use":    {Name: "borrow_use", ReturnType: "void", ParamTypes: []string{"unsigned char *"}},
				"borrow_mut":    {Name: "borrow_mut", ReturnType: "void", ParamTypes: []string{"unsigned char *"}},
				"take_owned":    {Name: "take_owned", ReturnType: "void", ParamTypes: []string{"unsigned char *"}},
			},
			Contracts: &bindgen.FFIContractCatalog{
				SchemaVersion: bindgen.SupportedFFIContractSchemaVersion,
				HeaderPath:    "demo.h",
				Namespace:     "demo",
				Functions: map[string]bindgen.FFIFuncContract{
					"borrow_use": {
						ContractID: "demo.borrow_use",
						Params:     []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "p", Ownership: bindgen.FFIBorrowedShared}},
						Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
					},
					"borrow_mut": {
						ContractID: "demo.borrow_mut",
						Params:     []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "p", Ownership: bindgen.FFIBorrowedMut}},
						Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
					},
					"take_owned": {
						ContractID: "demo.take_owned",
						Params:     []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "p", Ownership: bindgen.FFIOwnedTransfer}},
						Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
					},
				},
			},
		},
	}

	src := `fn f(p: *mut u8, mut q: *mut u8):
    unsafe:
        demo::borrow_use(p)
        demo::borrow_mut(q)
        demo::take_owned(p)
`
	_, tirProg, rep := compileAndLower(t, src, bindings)
	if rep.HasErrors() {
		t.Fatalf("FFI 契约用例编译失败:\n%s", rep.String())
	}
	dump := tirProg.Dump()
	for _, want := range []string{
		"ffi_catalog ns=demo schema=1.0 header=\"demo.h\"",
		"ffi_contract ",
		"ForeignCall demo::borrow_use",
		"ForeignCall demo::borrow_mut",
		"ForeignCall demo::take_owned",
		"OwnershipTransfer ",
	} {
		if !strings.Contains(dump, want) {
			t.Fatalf("FFI TIR Dump 缺少 %q:\n%s", want, dump)
		}
	}

	// 验证 FFI opaque handle 外部资源胶水与 BorrowsParam >= 0 契约元数据保留
	opaqueBindings := map[string]*bindgen.BindingMeta{
		"reslib": {
			HeaderPath: "reslib.h",
			Functions: map[string]bindgen.FunctionSig{
				"open_handle":  {Name: "open_handle", ReturnType: "void*", ParamTypes: nil},
				"close_handle": {Name: "close_handle", ReturnType: "void", ParamTypes: []string{"void*"}},
			},
			Contracts: &bindgen.FFIContractCatalog{
				SchemaVersion: bindgen.SupportedFFIContractSchemaVersion,
				HeaderPath:    "reslib.h",
				Namespace:     "reslib",
				OpaqueTypes: map[string]bindgen.FFIReturnContract{
					"CHandle": {
						Ownership:    bindgen.FFIOpaqueHandle,
						TypeName:     "CHandle",
						ReleaseFn:    "reslib::close_handle",
						BorrowsParam: -1,
					},
				},
				Functions: map[string]bindgen.FFIFuncContract{
					"close_handle": {
						ContractID: "reslib.close_handle",
						Params:     []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "h", Ownership: bindgen.FFIOwnedTransfer}},
						Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
					},
					"peek_field": {
						ContractID: "reslib.peek_field",
						Params:     []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "h", Ownership: bindgen.FFIBorrowedShared}},
						Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFIBorrowedShared, BorrowsParam: 0},
					},
				},
			},
		},
	}
	handleSrc := `struct CHandle:
    fd: i64

fn use_handle(cond: bool):
    var h = CHandle { fd: 3 }
    if cond:
        return
    println(h.fd)
`
	_, handleTIR, repH := compileAndLower(t, handleSrc, opaqueBindings)
	if repH.HasErrors() {
		t.Fatalf("Opaque handle TIR 降级失败:\n%s", repH.String())
	}
	hDump := handleTIR.Dump()
	for _, want := range []string{
		"[opaque_foreign ns=reslib]",
		"kind=ForeignResource",
		"release=reslib::close_handle",
		"borrows_param=0",
	} {
		if !strings.Contains(hDump, want) {
			t.Fatalf("Opaque handle TIR Dump 缺少 %q:\n%s", want, hDump)
		}
	}

	// 验证支持 BorrowsParam >= 0 返回借用的合成 CFG 无 AST Bridge 直接降级至 TIR
	tctx := types.NewContext()
	symTab := symbols.NewTable(tctx)
	sp := diagnostic.Span{File: "synth_ffi.won", Start: diagnostic.Position{Line: 1, Column: 1}, End: diagnostic.Position{Line: 1, Column: 10}}
	hStruct := symTab.AllocSymbol(symbols.SymStruct, "CHandle", sp)
	hStruct.Fields = []symbols.FieldInfo{{Name: "fd", Type: tctx.I64, Span: sp}}
	hStruct.FieldMap = map[string]*symbols.FieldInfo{"fd": &hStruct.Fields[0]}
	hType := tctx.Struct(uint64(hStruct.ID), "CHandle", nil)
	hStruct.Type = hType
	symTab.GlobalScope.Symbols["CHandle"] = hStruct

	fnSym := symTab.AllocSymbol(symbols.SymFunc, "peek_synth", sp)
	fnSym.ReturnType = tctx.Unit
	hVar := symTab.AllocSymbol(symbols.SymVar, "h", sp)
	hVar.Type = hType
	refI64 := tctx.Ref(false, tctx.I64)
	rVar := symTab.AllocSymbol(symbols.SymVar, "r", sp)
	rVar.Type = refI64
	hPlace := cfg.Place{RootSymID: hVar.ID, RootName: "h", RootType: hType, ElemType: hType, Span: sp}
	rPlace := cfg.Place{RootSymID: rVar.ID, RootName: "r", RootType: refI64, ElemType: refI64, Span: sp}
	fnCFG := &cfg.FuncCFG{
		Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
		LocalOrder: []symbols.SymbolID{hVar.ID, rVar.ID},
		Locals: map[symbols.SymbolID]*cfg.LocalMeta{
			hVar.ID: {Sym: hVar, Type: hType, ScopeID: 1, HasInit: true, Span: sp},
			rVar.ID: {Sym: rVar, Type: refI64, ScopeID: 1, HasInit: true, Span: sp},
		},
		Blocks: []*cfg.BasicBlock{
			{
				ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
				Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
				Statements: []cfg.Statement{
					{Kind: cfg.OpInitAssign, Place: hPlace, Type: hType, Span: sp},
					{Kind: cfg.OpCall, CalleeName: "reslib::peek_field", ArgTemps: []cfg.ValueID{0}, ArgPlaces: []cfg.Place{hPlace}, ValTemp: 1, Type: refI64, Span: sp},
					{Kind: cfg.OpInitAssign, Place: rPlace, SrcTemp: 1, Type: refI64, Span: sp},
					{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{hVar.ID, rVar.ID}, Span: sp},
					{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{hVar.ID, rVar.ID}, Span: sp},
				},
			},
			{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
		},
	}
	progCFG := &cfg.ProgramCFG{Functions: []*cfg.FuncCFG{fnCFG}, BySymbol: map[symbols.SymbolID]*cfg.FuncCFG{fnSym.ID: fnCFG}}
	tp := checker.NewTypedProgram(nil, tctx, symTab)
	tp.CFG = progCFG
	repS := diagnostic.NewReport()
	synthBindings := map[string]*bindgen.BindingMeta{
		"reslib": {
			HeaderPath: "reslib.h",
			Functions: map[string]bindgen.FunctionSig{
				"close_handle": {Name: "close_handle", ReturnType: "void", ParamTypes: []string{"void*"}},
				"peek_field":   {Name: "peek_field", ReturnType: "const int64_t *", ParamTypes: []string{"const void*"}},
			},
			Contracts: opaqueBindings["reslib"].Contracts,
		},
	}
	tp.DropPlan = drop.ElaborateProgramWithFFI(progCFG, tp, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{opaqueBindings["reslib"].Contracts}, repS)
	if repS.HasErrors() || tp.DropPlan == nil || !tp.DropPlan.DropElaborated {
		t.Fatalf("合成 FFI BorrowsParam 用例 Drop 展开失败:\n%s", repS.String())
	}
	tp.DropElaborated = true
	tp.OwnershipChecked = true
	synthTIR, err := lower.LowerProgram(tp, synthBindings)
	if err != nil {
		t.Fatalf("合成 FFI BorrowsParam 用例 TIR 降级失败: %v", err)
	}
	sDump := synthTIR.Dump()
	if !strings.Contains(sDump, "ForeignCall reslib::peek_field") || !strings.Contains(sDump, "borrows_param=0") || !strings.Contains(sDump, "release=reslib::close_handle") {
		t.Fatalf("合成 FFI BorrowsParam TIR Dump 缺少期望信息:\n%s", sDump)
	}
}

func TestPhase2C0_LowerFailClosedRejectsInvalidInputsAndBadSource(t *testing.T) {
	// 1. nil TypedProgram / 未通过 OwnershipChecked / DropElaborated 必须拒绝
	if _, err := lower.LowerProgram(nil, nil); err == nil {
		t.Fatalf("nil TypedProgram 必须被 LowerProgram 拒绝")
	}

	okSrc := `fn ok_fn() -> i32:
    return 1
`
	tp, _, _ := compileAndLower(t, okSrc, nil)

	unverified := *tp
	unverified.OwnershipChecked = false
	if _, err := lower.LowerProgram(&unverified, nil); err == nil {
		t.Fatalf("OwnershipChecked=false 时必须拒绝降级")
	}

	noDrop := *tp
	noDrop.DropElaborated = false
	if _, err := lower.LowerProgram(&noDrop, nil); err == nil {
		t.Fatalf("DropElaborated=false 时必须拒绝降级")
	}

	// 2. 无效 FFI 契约（错误 schema_version）在 LowerProgram 入口仍 fail-closed
	badBindings := map[string]*bindgen.BindingMeta{
		"demo": {
			HeaderPath: "demo.h",
			Contracts: &bindgen.FFIContractCatalog{
				SchemaVersion: "99.0",
				HeaderPath:    "demo.h",
				Namespace:     "demo",
			},
		},
	}
	if _, err := lower.LowerProgram(tp, badBindings); err == nil {
		t.Fatalf("非法 FFI 契约目录必须在 LowerProgram 入口拒绝")
	}

	// 3. 违背所有权或 unsafe 边界的源码绝不能产出已验证 TIR
	badSources := []struct {
		name string
		src  string
	}{
		{
			name: "use_after_move",
			src: `fn bad() -> u64:
    let s = String::from("a")
    let t = s
    return s.len()
`,
		},
		{
			name: "borrow_conflict",
			src: `fn bad():
    var s = String::from("a")
    let r1 = &s
    let r2 = &mut s
    println(r1)
`,
		},
		{
			name: "unsafe_ffi_outside_unsafe_block",
			src: `fn bad(p: *mut u8):
    zlib::compressBound(10)
`,
		},
	}

	for _, bs := range badSources {
		t.Run(bs.name, func(t *testing.T) {
			rep := diagnostic.NewReport()
			lex := lexer.New("bad.won", bs.src, rep)
			p := parser.New(lex, rep, "bad.won")
			prog := p.ParseProgram()
			chk := checker.New(rep, nil)
			badTP := chk.CheckProgram(prog)
			if !rep.HasErrors() && badTP != nil && badTP.OwnershipChecked {
				t.Fatalf("非法源码 %s 应当被语义/所有权检查拒绝", bs.name)
			}
			if badTP != nil {
				if _, err := lower.LowerProgram(badTP, nil); err == nil {
					t.Fatalf("非法源码 %s 绝不得生成合法 TIR", bs.name)
				}
			}
		})
	}
}

func TestLowerEnumSwitchProjDowncastAndNestedPatterns(t *testing.T) {
	src := `
struct Pair:
    a: i32
    b: i32

enum Shape:
    Empty
    TupleBox((i32, i32))
    StructBox:
        p: Pair

fn main() -> i32:
    let ((x, y), z): ((i32, i32), i32) = ((1, 2), 3)
    let e1 = Shape::TupleBox((x, y))
    let r1: i32 = match e1:
        Shape::Empty => 0
        Shape::TupleBox((u, v)) => u + v + z
        Shape::StructBox { p } => p.a + p.b
    let e2 = Shape::StructBox { p: Pair { a: 10, b: 20 } }
    let r2: i32 = match &e2:
        Shape::Empty => 0
        Shape::TupleBox(_) => 0
        Shape::StructBox { p: ref pr } => pr.a + pr.b
    return r1 + r2
`
	_, tirProg, rep := compileAndLower(t, src, nil)
	if rep.HasErrors() {
		t.Fatalf("语义检查失败: %s", rep.String())
	}
	dump := tirProg.Dump()
	if errs := tir.Verify(tirProg); len(errs) > 0 {
		t.Fatalf("TIR 验证失败: %v\n%s", errs, dump)
	}
	if !strings.Contains(dump, "@TupleBox#1") || !strings.Contains(dump, "@StructBox#2") {
		t.Fatalf("TIR 必须显式包含枚举变体 ProjDowncast 投影:\n%s", dump)
	}
	var mainFn *tir.Function
	for _, fn := range tirProg.Functions {
		if fn != nil && fn.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil {
		t.Fatalf("找不到 main TIR 函数")
	}
	foundEnumSwitch := false
	foundDowncastExtract := false
	foundDowncastBorrow := false
	for _, blk := range mainFn.Blocks {
		if blk.Terminator.Kind == tir.TermSwitch {
			for _, c := range blk.Terminator.Cases {
				if c.Kind == tir.CaseEnumVariant && c.VariantIndex > 0 {
					foundEnumSwitch = true
				}
			}
		}
		for _, inst := range blk.Instructions {
			if inst.Kind == tir.OpExtract && inst.ExtractProj.Kind == tir.ProjDowncast {
				foundDowncastExtract = true
			}
			if inst.Kind == tir.OpBorrow {
				for _, pr := range inst.Place.Projections {
					if pr.Kind == tir.ProjDowncast {
						foundDowncastBorrow = true
					}
				}
			}
		}
	}
	if !foundEnumSwitch || !foundDowncastExtract || !foundDowncastBorrow {
		t.Fatalf("Stage B TIR 降级不完整: enumSwitch=%v downcastExtract=%v downcastBorrow=%v\n%s",
			foundEnumSwitch, foundDowncastExtract, foundDowncastBorrow, dump)
	}
}

package borrow_test

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/borrow"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

func inferProvenanceFromSource(t *testing.T, src string) (*checker.TypedProgram, *borrow.ProvenanceResult) {
	t.Helper()
	rep := diagnostic.NewReport()
	lex := lexer.New("prov_test.won", src, rep)
	p := parser.New(lex, rep, "prov_test.won")
	prog := p.ParseProgram()
	if rep.HasErrors() {
		t.Fatalf("解析失败:\n%s", rep.String())
	}
	chk := checker.New(rep, nil)
	tp := chk.CheckProgram(prog)
	if rep.HasErrors() || tp == nil || tp.CFG == nil {
		t.Fatalf("检查失败:\n%s", rep.String())
	}
	prov := borrow.InferAndVerifyProvenance(tp.CFG, tp, rep)
	if rep.HasErrors() || prov == nil {
		t.Fatalf("溯源推理失败:\n%s", rep.String())
	}
	return tp, prov
}

// TestPhase2B2CandidateProvenancePositive 验证全部单来源、Static、Strategy B 多来源、嵌套转发、SCC 递归、结构体字段与可变候选正例。
func TestPhase2B2CandidateProvenancePositive(t *testing.T) {
	// 1. 单输入参数返回值 + 返回后在调用方正确关联参数借用期与 NLL 释放
	assertPass(t, "SingleParamReturnPass", `
fn identity(value: &String) -> &String:
    return value

fn main():
    var s = String::from("ok")
    let r = identity(&s)
    println(r)
    s.push_str("!")
`)

	// 2. 静态字面量引用 (&str) 返回，不绑定任何局部变量生命期
	assertPass(t, "StaticLiteralReturnPass", `
fn greeting() -> &str:
    return "hello_won"

fn main():
    let g = greeting()
    println(g)
`)

	// 3 & 4. Strategy B 多输入候选集合 choose(cond, a, b) -> {Param(1), Param(2)}，且用完即释放双端锁
	assertPass(t, "StrategyBMultiOriginUsedAndReleasedNLL", `
fn choose(cond: bool, a: &String, b: &String) -> &String:
    if cond:
        return a
    return b

fn main(c: bool):
    var x = String::from("x")
    var y = String::from("y")
    let r = choose(c, &x, &y)
    println(r)
    x.push_str("1")
    y.push_str("2")
`)

	// 验证 precise summary 仅锁实际可能返回的参数（b 不在 RetProv(first_only) 内）
	assertPass(t, "ExactParamSelectionDoesNotOverlockUnreturnedArg", `
fn first_only(a: &String, b: &String) -> &String:
    return a

fn main():
    var x = String::from("x")
    var y = String::from("y")
    let r = first_only(&x, &y)
    y.push_str("!")
    println(r)
`)

	// 5. 嵌套调用传播 forward -> choose
	srcNested := `
fn choose(cond: bool, a: &String, b: &String) -> &String:
    if cond:
        return a
    return b

fn forward(cond: bool, a: &String, b: &String) -> &String:
    return choose(cond, a, b)

fn main(c: bool):
    var x = String::from("x")
    var y = String::from("y")
    let r = forward(c, &x, &y)
    println(r)
`
	tp, prov := inferProvenanceFromSource(t, srcNested)
	var fwdSum *borrow.FuncSummary
	for _, fn := range tp.CFG.Functions {
		if fn.Name == "forward" {
			fwdSum = prov.Summaries[fn.FuncSym.ID]
		}
	}
	if fwdSum == nil || !fwdSum.ParamIndices[1] || !fwdSum.ParamIndices[2] || fwdSum.ParamIndices[0] {
		t.Fatalf("forward 候选摘要期望 {1, 2}，实际 %+v", fwdSum)
	}

	// 6. 直接自递归与互递归 SCC 摘要固定点收敛 (互换参数递归需同时发现两个参数下标)
	srcRec := `
fn ping(n: i32, a: &String, b: &String) -> &String:
    if n <= 0i32:
        return a
    return pong(n - 1i32, a, b)

fn pong(n: i32, a: &String, b: &String) -> &String:
    if n <= 0i32:
        return b
    return ping(n - 1i32, a, b)

fn rec_swap(n: i32, a: &String, b: &String) -> &String:
    if n <= 0i32:
        return a
    return rec_swap(n - 1i32, b, a)
`
	tpRec, provRec := inferProvenanceFromSource(t, srcRec)
	for _, fn := range tpRec.CFG.Functions {
		sum := provRec.Summaries[fn.FuncSym.ID]
		got := sum.SortedParamIndices()
		if len(got) != 2 || got[0] != 1 || got[1] != 2 {
			t.Fatalf("递归函数 %s 候选摘要期望 [1 2]，实际 %v", fn.Name, got)
		}
	}
	if len(provRec.SCCs) == 0 {
		t.Fatalf("期望非空 SCC 列表")
	}

	// 7. 返回引用参数成员字段 (&h.val)
	assertPass(t, "ReturnFieldOfBorrowedParamPass", `
struct Holder:
    val: String

fn field_ref(h: &Holder) -> &String:
    return &h.val

fn main():
    let h = Holder { val: String::from("inner") }
    let fv = field_ref(&h)
    println(fv)
`)

	// 8. 返回单个 &mut 参数，以及 9. 两两互不重叠字段候选的可变引用返回
	assertPass(t, "SingleAndDisjointMutReturnPass", `
struct Pair:
    var left: String
    var right: String

fn id_mut(x: &mut String) -> &mut String:
    return x

fn pick_mut(c: bool, a: &mut String, b: &mut String) -> &mut String:
    if c:
        return a
    return b

fn main(cond: bool):
    var p = Pair { left: String::from("l"), right: String::from("r") }
    let r1 = pick_mut(cond, &mut p.left, &mut p.right)
    r1.push_str("!")
    let single = id_mut(&mut p.left)
    single.push_str("?")
`)
}

// TestPhase2B2CandidateProvenanceNegative 验证全部局部逃逸、候选锁冲突、不透明多引用 WON-MEM-002 与可变候选重叠 WON-MEM-005 负例。
func TestPhase2B2CandidateProvenanceNegative(t *testing.T) {
	// 1. 直接返回局部栈引用 (WON-MEM-003)
	assertFailCode(t, "ReturnLocalRefRejectedMem003", `
fn invalid() -> &String:
    let value = String::from("temp")
    return &value
`, "WON-MEM-003")

	// 2. 返回局部结构体字段引用 (WON-MEM-003)
	assertFailCode(t, "ReturnLocalFieldRefRejectedMem003", `
struct Wrap:
    item: String

fn invalid_field() -> &String:
    let w = Wrap { item: String::from("field") }
    return &w.item
`, "WON-MEM-003")

	// 3 & 4. 作用域结束而外层引用仍存活 / 在作用域外使用 (WON-MEM-003)
	assertFailCode(t, "UseAfterInnerOwnerScopeExitMem003", `
fn main():
    let fb = String::from("fb")
    var out = &fb
    if true:
        let inner = String::from("dead")
        out = &inner
    println(out)
`, "WON-MEM-003")

	// 5. 函数指针参数 / 不透明多引用返回签名 (WON-MEM-002)
	assertFailCode(t, "FnPtrMultiRefParamOpaqueRejectedMem002", `
fn run_cb(picker: fn(&String, &String) -> &String):
    println("checked")
`, "WON-MEM-002")

	// 6. 零引用输入的不透明函数签名返回引用，拒绝隐式 Static (WON-MEM-002)
	assertFailCode(t, "FnPtrZeroRefParamOpaqueRejectedMem002", `
fn run_gen(mk: fn() -> &String):
    println("checked")
`, "WON-MEM-002")

	// 验证真实无函数体 AST FunctionDecl (d.Body == nil) 的跨边界 WON-MEM-002
	t.Run("BodylessFunctionDeclMultiRefRejectedMem002", func(t *testing.T) {
		tctx := types.NewContext()
		symTab := symbols.NewTable(tctx)
		sp := diagnostic.Span{File: "opaque.won", Start: diagnostic.Position{Line: 1, Column: 1, Byte: 0}, End: diagnostic.Position{Line: 1, Column: 30, Byte: 29}}
		refStr := tctx.Ref(false, tctx.StringT)
		opFn := symTab.AllocSymbol(symbols.SymFunc, "opaque_pick", sp)
		opFn.ParamTypes = []types.TypeID{refStr, refStr}
		opFn.ReturnType = refStr
		symTab.GlobalScope.Insert(opFn)
		rep := diagnostic.NewReport()
		borrow.InferAndVerifyProvenance(&cfg.ProgramCFG{}, &stubSemanticView{types: tctx, syms: symTab}, rep)
		found := false
		for _, d := range rep.Diagnostics {
			if d.Code == "WON-MEM-002" {
				found = true
			}
		}
		if !found {
			t.Fatalf("无函数体多引用返回声明应报 WON-MEM-002，实际:\n%s", rep.String())
		}
	})

	// 7. 可变多来源候选集在调用点指向重叠位置 (WON-MEM-005)
	assertFailCode(t, "MutCandidateOriginsOverlapRejectedMem005", `
struct Pair:
    var left: String
    var right: String

fn pick_mut_pair(c: bool, a: &mut Pair, b: &mut String) -> &mut String:
    if c:
        return &mut a.left
    return b

fn main(c: bool):
    var p = Pair { left: String::from("l"), right: String::from("r") }
    let r = pick_mut_pair(c, &mut p, &mut p.left)
    r.push_str("!")
`, "WON-MEM-005")

	// 8. 不可变引用试图通过 &mut 返回签名返回 (WON-TYP-007 / WON-MUT-001)
	assertFailCode(t, "MutReturnFromSharedRefRejected", `
fn bad(x: &String) -> &mut String:
    return x
`, "WON-TYP-007")
	assertFailCode(t, "MutReborrowFromSharedRefRejectedMem004", `
fn bad_reborrow(x: &String) -> &mut String:
    return &mut *x
`, "WON-MEM-004")

	// 9. 一条分支返回参数、另一条分支返回局部栈引用：整体必须按 WON-MEM-003 拒绝！
	assertFailCode(t, "MixedBranchWithOneLocalReturnRejectedMem003", `
fn maybe_escape(c: bool, p: &String) -> &String:
    let local = String::from("local")
    if c:
        return p
    return &local
`, "WON-MEM-003")

	// 10. 嵌套调用传播下如果调用者试图返回包含局部变量的候选源，亦由 WON-MEM-003 拦截
	assertFailCode(t, "HelperCandidateContainingLocalRejectedMem003", `
fn choose(c: bool, a: &String, b: &String) -> &String:
    if c:
        return a
    return b

fn escape_through_helper(c: bool, p: &String) -> &String:
    let local = String::from("inner")
    return choose(c, &local, p)
`, "WON-MEM-003")

	// 多输入候选锁定全部非局部实参：修改未在分支走通的候选实参同样报错！
	assertFailCode(t, "StrategyBMultiOriginLocksBothCandidates", `
fn choose(c: bool, a: &String, b: &String) -> &String:
    if c:
        return a
    return b

fn main(c: bool):
    var x = String::from("x")
    var y = String::from("y")
    let r = choose(c, &x, &y)
    y.push_str("!")
    println(r)
`, "WON-MEM-004")

	// 通过 forward 转发 choose 后，调用点仍锁定所有原始候选变量
	assertFailCode(t, "NestedForwardKeepsAllCandidatesLocked", `
fn choose(c: bool, a: &String, b: &String) -> &String:
    if c:
        return a
    return b

fn forward(c: bool, a: &String, b: &String) -> &String:
    return choose(c, a, b)

fn main(c: bool):
    var x = String::from("x")
    var y = String::from("y")
    let r = forward(c, &x, &y)
    x.push_str("!")
    println(r)
`, "WON-MEM-004")

	// unsafe 解引用裸指针产生的引用不可越出安全函数返回 (WON-MEM-003)
	assertFailCode(t, "RawPtrDerefReturnEscapeRejectedMem003", `
fn raw_escape(p: *const String) -> &String:
    return unsafe:
        &*p
`, "WON-MEM-003")
}

// TestPhase2B2StructuralBorrowInvariants 验证借用表、NLL 与溯源摘要的关键结构不变量及不可达块隔离。
func TestPhase2B2StructuralBorrowInvariants(t *testing.T) {
	src := `
struct Pair:
    var left: String
    var right: String

fn pick(c: bool, a: &String, b: &String) -> &String:
    if c:
        return a
    return b

fn main(c: bool):
    var p = Pair { left: String::from("a"), right: String::from("b") }
    let r = pick(c, &p.left, &p.right)
    println(r)
    p.left.push_str("!")
    p.right.push_str("?")
    return
    let dead_local = String::from("dead")
    let dead_ref = &dead_local
`
	tp, prov := inferProvenanceFromSource(t, src)
	mainCFG := tp.CFG.Functions[1]
	flow := prov.Flows[mainCFG.FuncSym.ID]

	// 1. LoanID 在函数内全局唯一且每个借用具备有效创建点
	seenID := make(map[cfg.LoanID]bool)
	for _, li := range flow.Loans {
		if li.ID == cfg.InvalidLoan || seenID[li.ID] {
			t.Fatalf("非法或重复 LoanID: %+v", li)
		}
		seenID[li.ID] = true
		if li.CreatedAt.Block < 0 || int(li.CreatedAt.Block) >= len(mainCFG.Blocks) {
			t.Fatalf("借用 L%d 缺少合法 CFG 创建点: %+v", li.ID, li.CreatedAt)
		}
	}

	// 2. 不可达块内的 dead_ref 不应污染有效活跃借用状态
	rep := diagnostic.NewReport()
	nll := borrow.AnalyzeBorrowConflicts(mainCFG, flow, tp, rep)
	if rep.HasErrors() || nll == nil {
		t.Fatalf("含合法死块程序 NLL 校验失败:\n%s", rep.String())
	}

	// 3. 确定性复验
	_, prov2 := inferProvenanceFromSource(t, src)
	pickSum1 := prov.Summaries[tp.CFG.Functions[0].FuncSym.ID].SortedParamIndices()
	pickSum2 := prov2.Summaries[tp.CFG.Functions[0].FuncSym.ID].SortedParamIndices()
	if len(pickSum1) != len(pickSum2) || pickSum1[0] != pickSum2[0] || pickSum1[1] != pickSum2[1] {
		t.Fatalf("两次独立推理摘要不一致: %v vs %v", pickSum1, pickSum2)
	}
}

package borrow_test

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/borrow"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
)

// runNLLForFunction 运行前端检查并提取指定函数的 NLL 活跃域求解点集，供精确点级边界测试使用。
func runNLLForFunction(t *testing.T, src, fnName string) (*cfg.FuncCFG, *borrow.FuncLoanFlow, *borrow.NLLResult) {
	t.Helper()
	rep := diagnostic.NewReport()
	lex := lexer.New("nll_pts.won", src, rep)
	p := parser.New(lex, rep, "nll_pts.won")
	prog := p.ParseProgram()
	if rep.HasErrors() {
		t.Fatalf("解析失败:\n%s", rep.String())
	}
	chk := checker.New(rep, nil)
	tp := chk.CheckProgram(prog)
	if rep.HasErrors() || tp == nil || tp.CFG == nil {
		t.Fatalf("检查失败:\n%s", rep.String())
	}
	for _, fn := range tp.CFG.Functions {
		if fn.Name == fnName {
			flow := borrow.BuildLoanFlow(fn, tp, rep)
			nll := borrow.AnalyzeBorrowConflicts(fn, flow, tp, rep)
			if rep.HasErrors() || nll == nil {
				t.Fatalf("NLL 计算失败:\n%s", rep.String())
			}
			return fn, flow, nll
		}
	}
	t.Fatalf("未找到函数 %s", fnName)
	return nil, nil, nil
}

// TestPhase2B2SharedAndExclusiveBorrows 验证共享借用、独占借用、字段拆分、数组下标保守性及所有者访问排他规则。
func TestPhase2B2SharedAndExclusiveBorrows(t *testing.T) {
	// 1. 共享借用正例：单一共享、多重共享、Struct 字段并行共享、Copy 复制多载体、Last-Use 后修改
	assertPass(t, "SharedMultipleCoexist", `
fn main():
    let s = String::from("hello")
    let r1 = &s
    let r2 = &s
    let r3 = r1
    println(r1)
    println(r2)
    println(r3)
`)

	assertPass(t, "SharedEndsBeforeMutationNLL", `
fn main():
    var value = String::from("hello")
    let reference = &value
    println(reference)
    value.push_str("!")
`)

	assertPass(t, "SharedCopyMultipleCarriersEndBeforeMutation", `
fn main():
    var value = String::from("copy")
    let r1 = &value
    let r2 = r1
    println(r1)
    println(r2)
    value.push_str("!")
`)

	// 2. 共享/独占冲突负例 (WON-MEM-004 / WON-MEM-005 / WON-MEM-006)
	assertFailCode(t, "MutBorrowWhileSharedLive", `
fn main():
    var x = String::from("x")
    let r = &x
    let m = &mut x
    println(r)
    m.push_str("!")
`, "WON-MEM-004")

	assertFailCode(t, "SharedBorrowWhileMutLive", `
fn main():
    var x = String::from("x")
    let m = &mut x
    let r = &x
    m.push_str("!")
    println(r)
`, "WON-MEM-004")

	assertFailCode(t, "MutationAfterSharedIfStillLiveLater", `
fn main():
    var value = String::from("fail")
    let reference = &value
    println(reference)
    value.push_str("!")
    println(reference)
`, "WON-MEM-004")

	assertFailCode(t, "MoveWhileSharedBorrowLive", `
fn consume(s: String):
    println(s)

fn main():
    let x = String::from("move")
    let r = &x
    consume(x)
    println(r)
`, "WON-MEM-006")

	assertFailCode(t, "OwnerReadDuringExclusiveBorrow", `
fn main():
    var x: i32 = 10
    let m = &mut x
    let copy_read = x
    *m = 20
`, "WON-MEM-004")

	assertFailCode(t, "OwnerAssignDuringSharedBorrow", `
fn main():
    var x: i32 = 10
    let r = &x
    x = 20
    let y = *r
`, "WON-MEM-006")

	assertFailCode(t, "OwnerAssignDuringExclusiveBorrow", `
fn main():
    var x: i32 = 10
    let m = &mut x
    x = 20
    *m = 30
`, "WON-MEM-006")

	assertFailCode(t, "MoveWhileExclusiveBorrowLive", `
fn consume(s: String):
    println(s)

fn main():
    var x = String::from("mv")
    let m = &mut x
    consume(x)
    m.push_str("!")
`, "WON-MEM-006")

	// 3. 独占借用正例：单可变借用、自身写入、字段拆分、顺序先后、重初始化后可变借用
	assertPass(t, "MutateThroughOwnReferenceAllowed", `
fn main():
    var x: i32 = 1
    let m = &mut x
    *m = 2
    *m += 3
`)

	assertPass(t, "DisjointStructFieldsMutBorrows", `
struct Pair:
    var left: String
    var right: String

fn main():
    var pair = Pair {
        left: String::from("left"),
        right: String::from("right")
    }
    let left = &mut pair.left
    let right = &mut pair.right
    left.push_str("!")
    right.push_str("?")
`)

	assertPass(t, "SequentialMutBorrowsAfterNLLTermination", `
fn main():
    var s = String::from("seq")
    let m1 = &mut s
    m1.push_str("1")
    let m2 = &mut s
    m2.push_str("2")
`)

	assertPass(t, "MutBorrowAfterReinitPlace", `
fn eat(s: String):
    println(s)

fn main():
    var s = String::from("one")
    eat(s)
    s = String::from("two")
    let m = &mut s
    m.push_str("!")
`)

	// 4. 重复可变借用与整包-字段冲突负例 (WON-MEM-005 / WON-MEM-004)
	assertFailCode(t, "DoubleMutBorrowRejected", `
fn main():
    var s = String::from("dup")
    let m1 = &mut s
    let m2 = &mut s
    m1.push_str("1")
    m2.push_str("2")
`, "WON-MEM-005")

	assertFailCode(t, "WholeMutConflictWithFieldShared", `
struct Pair:
    var left: String
    var right: String

fn main():
    var pair = Pair {
        left: String::from("l"),
        right: String::from("r")
    }
    let whole = &mut pair
    let left = &pair.left
    let _n = whole.right.len()
    println(left)
`, "WON-MEM-004")

	assertPass(t, "ConstArrayIndicesDisjointMutBorrow", `
fn main():
    var arr: [i32; 2] = [1, 2]
    let a = &mut arr[0]
    let b = &mut arr[1]
    *a = 10
    *b = 20
`)

	assertFailCode(t, "DynamicArrayIndicesConservativeMutConflict", `
fn pick(i: usize, j: usize):
    var arr: [i32; 4] = [1, 2, 3, 4]
    let a = &mut arr[i]
    let b = &mut arr[j]
    *a = 10
    *b = 20
`, "WON-MEM-005")
}

// TestPhase2B2NLLControlFlowAndPointSets 验证分支/循环/早退/重赋值/Match/作用域与精确 CFG 活跃点集边界。
func TestPhase2B2NLLControlFlowAndPointSets(t *testing.T) {
	assertPass(t, "BranchOnlyUseTerminatesBeforePostMergeMut", `
fn main(cond: bool):
    var value = String::from("branch")
    let reference = &value
    if cond:
        println(reference)
    value.push_str("!")
`)

	assertFailCode(t, "BranchUsePlusPostMutUseRejected", `
fn main(cond: bool):
    var value = String::from("branch")
    let reference = &value
    if cond:
        println(reference)
    value.push_str("!")
    println(reference)
`, "WON-MEM-004")

	assertPass(t, "LoopBorrowEndsBeforePostLoopMutation", `
fn main(cond: bool):
    var value = String::from("loop")
    let reference = &value
    while cond:
        println(reference)
        break
    value.push_str("!")
`)

	assertFailCode(t, "LoopMutationAcrossLiveBorrowRejected", `
fn main(cond: bool):
    var value = String::from("loop")
    let reference = &value
    while cond:
        value.push_str("!")
        println(reference)
`, "WON-MEM-004")

	assertPass(t, "ReassignRefVarKillsOldLoanBeforeOldOwnerMut", `
fn main():
    var a = String::from("a")
    var b = String::from("b")
    var r = &a
    println(r)
    r = &b
    a.push_str("!")
    println(r)
`)

	assertFailCode(t, "ReassignRefVarOnOnlyOneBranchKeepsOldLoanLive", `
fn main(cond: bool):
    var a = String::from("a")
    var b = String::from("b")
    var r = &a
    if cond:
        r = &b
    a.push_str("!")
    println(r)
`, "WON-MEM-004")

	assertFailCode(t, "MoveMutRefTransfersLoanAndStillConflicts", `
fn main():
    var x = String::from("mvref")
    var m1 = &mut x
    let m2 = m1
    let r = &x
    m2.push_str("!")
    println(r)
`, "WON-MEM-004")

	assertPass(t, "TemporaryCallBorrowEndsAfterCall", `
fn consume_reference(v: &String):
    println(v)

fn main():
    var text = String::from("hello")
    consume_reference(&text)
    text.push_str("!")
`)

	assertPass(t, "InnerScopeBorrowDiesBeforeScopeExit", `
fn main():
    if true:
        let inner = String::from("ok")
        let r = &inner
        println(r)
`)

	assertFailCode(t, "InnerOwnerScopeExitWhileOuterRefLiveEmitsMem003", `
fn main():
    let fallback = String::from("fb")
    var outer = &fallback
    if true:
        let inner = String::from("boom")
        outer = &inner
    println(outer)
`, "WON-MEM-003")

	assertFailCode(t, "MatchSubjectRefBorrowWhileMutBorrowLiveRejected", `
enum OptStr:
    Some(String)
    None

fn main():
    var opt = OptStr::Some(String::from("arm"))
    let m = &mut opt
    match opt:
        OptStr::Some(ref v) => println(v)
        OptStr::None => println("none")
    println(m)
`, "WON-MEM-004")

	// 验证精确 CFG 活跃点集：r 最后一次使用之后，L1 不再出现在后继语句点的 PointLiveLoans 中
	srcPts := `
fn nll_points():
    var v = String::from("pts")
    let r = &v
    println(r)
    v.push_str("!")
`
	fnCFG, flow, nll := runNLLForFunction(t, srcPts, "nll_points")
	var loanR, loanMut cfg.LoanID
	for _, li := range flow.Loans {
		if !li.IsMut {
			loanR = li.ID
		} else {
			loanMut = li.ID
		}
	}
	if loanR == 0 || loanMut == 0 {
		t.Fatalf("期望存在共享借用 loanR 与可变借用 loanMut，实际 Loans=%d", len(flow.Loans))
	}
	// 查找 v.push_str("!") 的 BorrowMut 语句所在 CFG 点：该点及之后 loanR 不得再活跃！
	mutStmt := flow.LoanByID[loanMut].CreatedAt
	for _, lid := range nll.PointLiveLoans[mutStmt] {
		if lid == loanR {
			t.Fatalf("违反 NLL 精确点边界：在最后一次使用 r 之后的语句 %+v 上仍保留活跃借用 L%d", mutStmt, loanR)
		}
	}
	// 同时在 println(r) 前一语句（OpCopy r），loanR 必须处于活跃状态
	blk := fnCFG.Blocks[mutStmt.Block]
	sawActiveBeforeLastUse := false
	for stIdx, st := range blk.Statements {
		if st.Kind == cfg.OpBorrowShared && st.LoanID == loanR {
			pt := cfg.ProgramPoint{Block: mutStmt.Block, StmtIdx: stIdx}
			for _, lid := range nll.PointLiveLoans[pt] {
				if lid == loanR {
					sawActiveBeforeLastUse = true
				}
			}
		}
	}
	if !sawActiveBeforeLastUse {
		t.Errorf("在借用创建点后到最后一次使用前，L%d 必须出现在 NLL 活跃集合内", loanR)
	}
}

// TestPhase2B2ReborrowingAndMethodReceivers 验证父子重借用冻结与解冻、结构体重借用拆分及确定性先左后右方法借用。
func TestPhase2B2ReborrowingAndMethodReceivers(t *testing.T) {
	assertPass(t, "ReborrowSharedFromMutUnfreezesParentAfterChildLastUse", `
fn inspect(value: &String):
    println(value)

fn main():
    var text = String::from("hello")
    let parent = &mut text
    let child = &*parent
    inspect(child)
    parent.push_str("!")
`)

	assertFailCode(t, "ParentMutMethodWhileChildReborrowLiveRejected", `
fn inspect(value: &String):
    println(value)

fn main():
    var text = String::from("hello")
    let parent = &mut text
    let child = &*parent
    parent.push_str("!")
    inspect(child)
`, "WON-MEM-004")

	assertFailCode(t, "ParentSharedMethodWhileChildFromMutReborrowLiveRejected", `
fn inspect(value: &String):
    println(value)

fn main():
    var text = String::from("hello")
    let parent = &mut text
    let child = &*parent
    let _n = parent.len()
    inspect(child)
`, "WON-MEM-004")

	assertPass(t, "MultipleSharedReborrowsFromSharedParentCoexist", `
fn inspect(v: &String):
    println(v)

fn main():
    let text = String::from("shared")
    let p = &text
    let c1 = &*p
    let c2 = &*p
    inspect(p)
    inspect(c1)
    inspect(c2)
`)

	assertPass(t, "NestedReborrowChainRespectsLifetimes", `
fn main():
    var text = String::from("chain")
    let m1 = &mut text
    let m2 = &mut *m1
    let c3 = &*m2
    println(c3)
    m2.push_str("2")
    m1.push_str("1")
`)

	assertPass(t, "SplitFieldReborrowsThroughMutStructRef", `
struct Pair:
    var left: String
    var right: String

fn main():
    var pair = Pair { left: String::from("a"), right: String::from("b") }
    let p = &mut pair
    let l = &mut p.left
    let r = &mut p.right
    l.push_str("!")
    r.push_str("?")
`)

	// Method receiver-first borrowing: vec.push(vec.len()) rejected with WON-MEM-004; intermediate var succeeds!
	assertFailCode(t, "ReceiverFirstVecPushLenConflictRejectedMem004", `
struct IntList:
    var count: u64

impl IntList:
    fn len(&self) -> u64:
        return self.count

    fn push(&mut self, val: u64):
        self.count += val

fn main():
    var vec = IntList { count: 0u64 }
    vec.push(vec.len())
`, "WON-MEM-004")

	assertPass(t, "ReceiverFirstFixedWithIntermediateLocal", `
struct IntList:
    var count: u64

impl IntList:
    fn len(&self) -> u64:
        return self.count

    fn push(&mut self, val: u64):
        self.count += val

fn main():
    var vec = IntList { count: 0u64 }
    let size = vec.len()
    vec.push(size)
`)

	assertFailCode(t, "StringPushStrWithSelfBorrowArgRejected", `
fn view_str(s: &String) -> &str:
    return "x"

fn main():
    var text = String::from("abc")
    text.push_str(view_str(&text))
`, "WON-MEM-004")
}

package borrow_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
)

func runCheck(src string) *diagnostic.Report {
	rep := diagnostic.NewReport()
	lex := lexer.New("borrow_test.won", src, rep)
	p := parser.New(lex, rep, "borrow_test.won")
	prog := p.ParseProgram()
	if rep.HasErrors() {
		return rep
	}
	chk := checker.New(rep, nil)
	chk.CheckProgram(prog)
	return rep
}

func assertPass(t *testing.T, name, src string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		rep := runCheck(src)
		if rep.HasErrors() {
			t.Fatalf("%s 预期通过校验，实际报错:\n%s", name, rep.String())
		}
	})
}

func assertFailCode(t *testing.T, name, src, wantCode string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		rep := runCheck(src)
		if !rep.HasErrors() {
			t.Fatalf("%s 预期失败并报 %s，但实际通过", name, wantCode)
		}
		for _, d := range rep.Diagnostics {
			if d.Code == wantCode {
				if d.Span.Start.Line <= 0 || d.Span.Start.Column <= 0 {
					t.Errorf("%s 诊断 %s 缺少有效坐标: %+v", name, wantCode, d.Span)
				}
				return
			}
		}
		t.Fatalf("%s 期望诊断码 %s，实际:\n%s", name, wantCode, rep.String())
	})
}

// TestPhase2B1CopyAndDeriveCopy 验证 Copy 分类与 @derive(Copy) 正反用例
func TestPhase2B1CopyAndDeriveCopy(t *testing.T) {
	assertPass(t, "PrimitiveCopyReuse", `
fn add(a: i32, b: i32) -> i32:
    return a + b

fn main() -> i32:
    let x: i32 = 20
    let y = x
    return add(x, y)
`)

	assertPass(t, "CopyArrayAndTupleReuse", `
fn main() -> i32:
    let arr: [i32; 2] = [1, 2]
    let arr2 = arr
    let tup = (10, 20)
    let tup2 = tup
    return arr[0] + arr2[1]
`)

	assertPass(t, "ValidDerivedCopyStructAndEnum", `
@derive(Copy)
struct Inner:
    v: i32

@derive(Copy)
struct Outer:
    inner: Inner
    flag: bool

@derive(Copy)
enum State:
    Idle
    Active(Inner)

fn consume_outer(o: Outer) -> i32:
    return o.inner.v

fn main() -> i32:
    let s = Outer { inner: Inner { v: 7 }, flag: true }
    let copy_s = s
    let a = consume_outer(s)
    let b = consume_outer(copy_s)
    let st = State::Active(Inner { v: 3 })
    let st2 = st
    return a + b
`)

	assertFailCode(t, "RejectDefaultStructMoveAsCopy", `
struct Pair:
    x: i32
    y: i32

fn consume(p: Pair) -> i32:
    return p.x

fn main() -> i32:
    let p = Pair { x: 1, y: 2 }
    let a = consume(p)
    return consume(p) + a
`, "WON-MEM-001")

	assertFailCode(t, "RejectDefaultEnumMoveAsCopy", `
enum Kind:
    A
    B(i32)

fn consume(k: Kind):
    let _k = k

fn main():
    let k = Kind::B(1)
    consume(k)
    consume(k)
`, "WON-MEM-001")

	assertFailCode(t, "RejectDeriveCopyWithStringField", `
@derive(Copy)
struct BadCopy:
    name: String
    id: i32
`, "WON-MEM-011")

	assertFailCode(t, "RejectDeriveCopyEnumWithStringPayload", `
@derive(Copy)
enum BadEnum:
    Ok
    Msg(String)
`, "WON-MEM-011")

	assertFailCode(t, "RejectGenericDeriveCopy", `
@derive(Copy)
struct Boxed[T]:
    val: T
`, "WON-MEM-011")

	assertFailCode(t, "RejectCyclicDeriveCopy", `
@derive(Copy)
struct CycleA:
    b: CycleB

@derive(Copy)
struct CycleB:
    a: CycleA
`, "WON-MEM-011")
}

// TestPhase2B1DefiniteInitAndMoves 验证未初始化声明、Move、重初始化、分支合流及循环不动点
func TestPhase2B1DefiniteInitAndMoves(t *testing.T) {
	assertPass(t, "UninitLetInitializedAcrossAllBranches", `
fn select(cond: bool) -> i32:
    let result: i32
    if cond:
        result = 10
    else:
        result = 20
    return result
`)

	assertFailCode(t, "UninitLetReadBeforeInitOnElsePath", `
fn select(cond: bool) -> i32:
    let result: i32
    if cond:
        result = 10
    return result
`, "WON-MEM-010")

	assertFailCode(t, "UninitLetReassignAfterInitForbidden", `
fn main() -> i32:
    let x: i32
    x = 1
    x = 2
    return x
`, "WON-MUT-001")

	assertFailCode(t, "UninitLetAmbiguousAssignAfterBranch", `
fn test(cond: bool) -> i32:
    let x: i32
    if cond:
        x = 1
    x = 2
    return x
`, "WON-MUT-001")

	assertFailCode(t, "UninitVarReadBeforeInit", `
fn main() -> usize:
    var buf: String
    return buf.len()
`, "WON-MEM-010")

	assertFailCode(t, "UseAfterMoveString", `
fn consume(s: String):
    println(s)

fn main() -> usize:
    let s1 = String::from("won")
    let s2 = s1
    consume(s2)
    return s1.len()
`, "WON-MEM-001")

	assertPass(t, "ReinitVarAfterMove", `
fn consume(s: String):
    println(s)

fn main() -> usize:
    var s = String::from("first")
    consume(s)
    s = String::from("second")
    return s.len()
`)

	assertFailCode(t, "RejectReinitMovedImmutableLet", `
fn consume(s: String):
    println(s)

fn main():
    let s = String::from("first")
    consume(s)
    s = String::from("second")
`, "WON-MUT-001")

	assertFailCode(t, "MoveInOneBranchInvalidatesAfterMerge", `
fn consume(s: String):
    println(s)

fn test(cond: bool) -> usize:
    let s = String::from("res")
    if cond:
        consume(s)
    else:
        println("keep")
    return s.len()
`, "WON-MEM-001")

	assertPass(t, "UninitStringInitializedInAllBranches", `
fn test(cond: bool) -> usize:
    let s: String
    if cond:
        s = String::from("a")
    else:
        s = String::from("b")
    return s.len()
`)

	assertFailCode(t, "LoopMoveWithoutReinit", `
fn consume(s: String):
    println(s)

fn test(cond: bool) -> usize:
    let s = String::from("loop")
    while cond:
        consume(s)
    return s.len()
`, "WON-MEM-001")

	assertPass(t, "LoopMoveWithReinitInsideBody", `
fn consume(s: String):
    println(s)

fn test(cond: bool) -> usize:
    var s = String::from("start")
    while cond:
        consume(s)
        s = String::from("next")
    return s.len()
`)

	assertFailCode(t, "LoopMayExecuteZeroTimesLeavesUninit", `
fn test(cond: bool) -> usize:
    var s: String
    while cond:
        s = String::from("maybe")
    return s.len()
`, "WON-MEM-010")

	assertPass(t, "EarlyReturnInsideLoopBranch", `
fn consume(s: String):
    println(s)

fn test(cond: bool) -> usize:
    let s = String::from("safe")
    while cond:
        if cond:
            consume(s)
            return 0
    return s.len()
`)

	assertPass(t, "UnreachableCodeDoesNotPolluteInit", `
fn test() -> i32:
    return 10
    let never: i32
    return never
`)
}

// TestPhase2B1PartialMovesAndDerefs 验证结构体字段级部分移动与解引用禁止移出
func TestPhase2B1PartialMovesAndDerefs(t *testing.T) {
	assertPass(t, "PartialMoveSiblingFieldRemainsUsable", `
struct PairStr:
    var a: String
    var b: String

fn main() -> usize:
    let p = PairStr { a: String::from("x"), b: String::from("y") }
    let moved_a = p.a
    return moved_a.len() + p.b.len()
`)

	assertPass(t, "PartialMoveReinitRestoresWholeStruct", `
struct PairStr:
    var a: String
    var b: String

fn sink(p: PairStr) -> usize:
    return p.a.len() + p.b.len()

fn main() -> usize:
    var p = PairStr { a: String::from("x"), b: String::from("y") }
    let moved_a = p.a
    p.a = String::from("restored")
    return moved_a.len() + sink(p)
`)

	assertFailCode(t, "RejectWholeStructReadAfterPartialMove", `
struct PairStr:
    a: String
    b: String

fn sink(p: PairStr):
    let _p = p

fn main():
    let p = PairStr { a: String::from("x"), b: String::from("y") }
    let _moved = p.a
    sink(p)
`, "WON-MEM-001")

	assertFailCode(t, "RejectMovedFieldRead", `
struct PairStr:
    a: String
    b: String

fn main() -> usize:
    let p = PairStr { a: String::from("x"), b: String::from("y") }
    let _moved = p.a
    return p.a.len()
`, "WON-MEM-001")

	assertFailCode(t, "RejectMoveThroughDeref", `
fn steal(r: &String) -> String:
    return *r
`, "WON-MEM-008")

	assertFailCode(t, "RejectMoveFieldThroughRef", `
struct Holder:
    name: String

fn steal_field(h: &Holder) -> String:
    return h.name
`, "WON-MEM-008")

	assertPass(t, "CopyFieldReadThroughRefAllowed", `
struct Holder:
    name: String
    id: i32

fn get_id(h: &Holder) -> i32:
    return h.id
`)
}

// buildDeepStructProgram 构造指定嵌套深度的结构体链源码（包含可移动叶节点 leaf 与可拷贝兄弟节点 sib）。
func buildDeepStructProgram(depth int, useLeafAfterMove, readWholeAfterMove, readSiblingAfterMove, reinitLeaf bool) string {
	var sb strings.Builder
	sb.WriteString("struct Leaf:\n    var val: String\n    var sib: String\n\n")
	for d := depth - 1; d >= 1; d-- {
		sb.WriteString(fmt.Sprintf("struct L%d:\n    var inner: %s\n\n", d, func() string {
			if d == depth-1 {
				return "Leaf"
			}
			return fmt.Sprintf("L%d", d+1)
		}()))
	}

	chainInit := "Leaf { val: String::from(\"leaf\"), sib: String::from(\"sib\") }"
	for d := depth - 1; d >= 1; d-- {
		chainInit = fmt.Sprintf("L%d { inner: %s }", d, chainInit)
	}

	rootType := "Leaf"
	if depth > 1 {
		rootType = "L1"
	}

	var leafPath strings.Builder
	var sibPath strings.Builder
	leafPath.WriteString("root")
	sibPath.WriteString("root")
	for d := 1; d < depth; d++ {
		leafPath.WriteString(".inner")
		sibPath.WriteString(".inner")
	}
	leafPath.WriteString(".val")
	sibPath.WriteString(".sib")

	sb.WriteString(fmt.Sprintf("fn consume_root(r: %s) -> usize:\n    return 1usize\n\n", rootType))
	sb.WriteString("fn main() -> usize:\n")
	sb.WriteString(fmt.Sprintf("    var root = %s\n", chainInit))
	sb.WriteString(fmt.Sprintf("    let moved = %s\n", leafPath.String()))
	if reinitLeaf {
		sb.WriteString(fmt.Sprintf("    %s = String::from(\"restored\")\n", leafPath.String()))
	}
	if useLeafAfterMove {
		sb.WriteString(fmt.Sprintf("    return %s.len()\n", leafPath.String()))
	} else if readWholeAfterMove {
		sb.WriteString("    return moved.len() + consume_root(root)\n")
	} else if readSiblingAfterMove {
		sb.WriteString(fmt.Sprintf("    return moved.len() + %s.len()\n", sibPath.String()))
	} else {
		sb.WriteString("    return moved.len()\n")
	}
	return sb.String()
}

// TestPhase2B11DeepNestedStructTracking 验证消除深度截断限制后 1/8/9/16/24 层嵌套结构体的精确部分移动与重初始化恢复。
func TestPhase2B11DeepNestedStructTracking(t *testing.T) {
	depths := []int{1, 8, 9, 16, 24}
	for _, d := range depths {
		label := fmt.Sprintf("Depth%d", d)
		assertFailCode(t, label+"MovedLeafReadRejected", buildDeepStructProgram(d, true, false, false, false), "WON-MEM-001")
		assertFailCode(t, label+"WholeStructAfterLeafMoveRejected", buildDeepStructProgram(d, false, true, false, false), "WON-MEM-001")
		assertPass(t, label+"SiblingReadAllowed", buildDeepStructProgram(d, false, false, true, false))
		assertPass(t, label+"ReinitRestoresWholeStruct", buildDeepStructProgram(d, false, true, false, true))

		// 确定性复验
		src := buildDeepStructProgram(d, false, true, false, false)
		rep1, _ := runCheck(src).ToJSON()
		rep2, _ := runCheck(src).ToJSON()
		if rep1 != rep2 {
			t.Fatalf("%s 重复分析诊断 JSON 不一致:\n1:%s\n2:%s", label, rep1, rep2)
		}
	}

	// 通过 &Option / &Node 间接递归引用结构体不得导致惰性字段展开栈溢出
	assertPass(t, "RecursiveIndirectedStructSafe", `
enum NextNode:
    End
    More(&Node)

struct Node:
    head: String
    next: NextNode

fn inspect(n: &Node) -> usize:
    return n.head.len()
`)
}

// TestPhase2B11MatchArmOwnership 验证 match 按分支独立降维（纯引用 vs 按值移动 vs 混合分支 vs 部分字段解构 vs 守卫落空）。
func TestPhase2B11MatchArmOwnership(t *testing.T) {
	assertPass(t, "RefOnlyMatchPreservesSubject", `
enum OptStr:
    Some(String)
    None

fn use_opt(o: OptStr) -> usize:
    return 1usize

fn main() -> usize:
    let opt = OptStr::Some(String::from("hello"))
    let n = match opt:
        OptStr::Some(ref r) => r.len()
        OptStr::None => 0usize
    return n + use_opt(opt)
`)

	assertFailCode(t, "ByValueMatchMovesSubject", `
enum OptStr:
    Some(String)
    None

fn eat(s: String) -> usize:
    return s.len()

fn use_opt(o: OptStr) -> usize:
    return 1usize

fn main() -> usize:
    let opt = OptStr::Some(String::from("bye"))
    let n = match opt:
        OptStr::Some(v) => eat(v)
        OptStr::None => 0usize
    return n + use_opt(opt)
`, "WON-MEM-001")

	assertFailCode(t, "MixedRefAndByValueArmMarksPossiblyMoved", `
enum OptStr:
    Some(String)
    None

fn eat(s: String) -> usize:
    return s.len()

fn main() -> usize:
    let opt = OptStr::Some(String::from("mix"))
    let flag = true
    let n = match opt:
        OptStr::Some(ref r) if flag => r.len()
        OptStr::Some(v) => eat(v)
        OptStr::None => 0usize
    let again = match opt:
        OptStr::Some(v) => eat(v)
        OptStr::None => 0usize
    return n + again
`, "WON-MEM-001")

	assertPass(t, "ReinitVarSubjectAfterMovingMatchAllowed", `
enum OptStr:
    Some(String)
    None

fn eat(s: String) -> usize:
    return s.len()

fn main() -> usize:
    var opt = OptStr::Some(String::from("first"))
    let n = match opt:
        OptStr::Some(v) => eat(v)
        OptStr::None => 0usize
    opt = OptStr::Some(String::from("second"))
    let m = match opt:
        OptStr::Some(ref r) => r.len()
        OptStr::None => 0usize
    return n + m
`)

	assertPass(t, "DirectStructPatternPartialMovePreservesRefField", `
struct PairStr:
    a: String
    b: String

fn eat(s: String) -> usize:
    return s.len()

fn main() -> usize:
    let pair = PairStr { a: String::from("aa"), b: String::from("bb") }
    let x = match pair:
        PairStr { a, b: ref rb } => eat(a) + rb.len()
    return x + pair.b.len()
`)

	assertFailCode(t, "DirectStructPatternMovedFieldAndWholeAggregateRejected", `
struct PairStr:
    a: String
    b: String

fn eat(s: String) -> usize:
    return s.len()

fn sink_pair(p: PairStr) -> usize:
    return p.b.len()

fn main() -> usize:
    let pair = PairStr { a: String::from("aa"), b: String::from("bb") }
    let _x = match pair:
        PairStr { a, b: ref rb } => eat(a) + rb.len()
    return sink_pair(pair)
`, "WON-MEM-001")

	assertPass(t, "GuardRefArmFallthroughToWildcardPreservesSubject", `
enum OptStr:
    Some(String)
    None

fn sink(o: OptStr) -> usize:
    return 1usize

fn main() -> usize:
    let opt = OptStr::Some(String::from("guard"))
    let len = match opt:
        OptStr::Some(ref r) if r.len() > 2usize => r.len()
        _ => 0usize
    return len + sink(opt)
`)

	assertFailCode(t, "MoveOutOfRefMatchedStructRejected", `
struct BoxStr:
    inner: String

fn steal(b: &BoxStr) -> String:
    return match b:
        BoxStr { inner } => inner
`, "WON-MEM-008")
}

package borrow_test

import (
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
fn main() -> u64:
    var buf: String
    return buf.len()
`, "WON-MEM-010")

	assertFailCode(t, "UseAfterMoveString", `
fn consume(s: String):
    println(s)

fn main() -> u64:
    let s1 = String::from("won")
    let s2 = s1
    consume(s2)
    return s1.len()
`, "WON-MEM-001")

	assertPass(t, "ReinitVarAfterMove", `
fn consume(s: String):
    println(s)

fn main() -> u64:
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

fn test(cond: bool) -> u64:
    let s = String::from("res")
    if cond:
        consume(s)
    else:
        println("keep")
    return s.len()
`, "WON-MEM-001")

	assertPass(t, "UninitStringInitializedInAllBranches", `
fn test(cond: bool) -> u64:
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

fn test(cond: bool) -> u64:
    let s = String::from("loop")
    while cond:
        consume(s)
    return s.len()
`, "WON-MEM-001")

	assertPass(t, "LoopMoveWithReinitInsideBody", `
fn consume(s: String):
    println(s)

fn test(cond: bool) -> u64:
    var s = String::from("start")
    while cond:
        consume(s)
        s = String::from("next")
    return s.len()
`)

	assertFailCode(t, "LoopMayExecuteZeroTimesLeavesUninit", `
fn test(cond: bool) -> u64:
    var s: String
    while cond:
        s = String::from("maybe")
    return s.len()
`, "WON-MEM-010")

	assertPass(t, "EarlyReturnInsideLoopBranch", `
fn consume(s: String):
    println(s)

fn test(cond: bool) -> u64:
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

fn main() -> u64:
    let p = PairStr { a: String::from("x"), b: String::from("y") }
    let moved_a = p.a
    return moved_a.len() + p.b.len()
`)

	assertPass(t, "PartialMoveReinitRestoresWholeStruct", `
struct PairStr:
    var a: String
    var b: String

fn sink(p: PairStr) -> u64:
    return p.a.len() + p.b.len()

fn main() -> u64:
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

fn main() -> u64:
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

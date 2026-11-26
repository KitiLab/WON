package borrow_test

import "testing"

// TestPhase2B12MixedAggregatePatterns 验证混合引用/按值/Copy 聚合模式的所有权行为与显式 WON-SEM-010 边界。
func TestPhase2B12MixedAggregatePatterns(t *testing.T) {
	// 1. 仅含引用绑定的非直接枚举与元组模式：保留 Subject 所有权且不得丢失借用事件
	assertPass(t, "OpaqueEnumRefOnlyPreservesSubject", `
enum Dual:
    Pair(String, String)
    Empty

fn keep_dual(d: Dual) -> u64:
    return 1u64

fn main() -> u64:
    let d = Dual::Pair(String::from("left"), String::from("right"))
    let n = match d:
        Dual::Pair(ref a, ref b) => a.len() + b.len()
        Dual::Empty => 0u64
    return n + keep_dual(d)
`)

	assertPass(t, "OpaqueTupleRefAndMutRefOnlyPreservesSubject", `
fn take_pair(p: (String, String)) -> u64:
    return 1u64

fn main() -> u64:
    var pair = (String::from("x"), String::from("yy"))
    let total = match pair:
        (ref a, ref mut b) => a.len() + b.len()
    return total + take_pair(pair)
`)

	// 2. 仅含按值非 Copy 绑定的枚举与元组模式：按值移出主体，后续再用报 WON-MEM-001
	assertFailCode(t, "OpaqueEnumByValueMovesSubject", `
enum Dual:
    Pair(String, String)
    Empty

fn eat(s: String) -> u64:
    return s.len()

fn take_dual(d: Dual) -> u64:
    return 1u64

fn main() -> u64:
    let d = Dual::Pair(String::from("a"), String::from("b"))
    let n = match d:
        Dual::Pair(a, b) => eat(a) + eat(b)
        Dual::Empty => 0u64
    return n + take_dual(d)
`, "WON-MEM-001")

	assertFailCode(t, "OpaqueTupleByValueMovesSubject", `
fn eat(s: String) -> u64:
    return s.len()

fn take_pair(p: (String, String)) -> u64:
    return 1u64

fn main() -> u64:
    let pair = (String::from("a"), String::from("b"))
    let n = match pair:
        (a, b) => eat(a) + eat(b)
    return n + take_pair(pair)
`, "WON-MEM-001")

	// 3. 非直接聚合模式混合 shared-ref 与非 Copy 按值移动：显式 WON-SEM-010（绝不静默丢弃 Borrow 且不伪装为 WON-MEM-001）
	assertFailCode(t, "OpaqueEnumSharedRefPlusMoveRejected", `
enum Dual:
    Pair(String, String)
    Empty

fn eat(s: String) -> u64:
    return s.len()

fn main() -> u64:
    let d = Dual::Pair(String::from("ref"), String::from("own"))
    return match d:
        Dual::Pair(ref a, b) => a.len() + eat(b)
        Dual::Empty => 0u64
`, "WON-SEM-010")

	assertFailCode(t, "OpaqueTupleSharedRefPlusMoveRejected", `
fn eat(s: String) -> u64:
    return s.len()

fn main() -> u64:
    let pair = (String::from("ref"), String::from("own"))
    return match pair:
        (ref a, b) => a.len() + eat(b)
`, "WON-SEM-010")

	assertFailCode(t, "EnumStructVariantSharedRefPlusMoveRejected", `
enum Boxed:
    Item:
        left: String
        right: String
    Empty

fn eat(s: String) -> u64:
    return s.len()

fn main() -> u64:
    let b = Boxed::Empty
    return match b:
        Boxed::Item { left: ref l, right } => l.len() + eat(right)
        Boxed::Empty => 0u64
`, "WON-SEM-010")

	// 4. 非直接聚合模式混合 ref mut 与非 Copy 按值移动：显式 WON-SEM-010
	assertFailCode(t, "OpaqueEnumMutRefPlusMoveRejected", `
enum Dual:
    Pair(String, String)
    Empty

fn eat(s: String) -> u64:
    return s.len()

fn main() -> u64:
    var d = Dual::Pair(String::from("mutref"), String::from("own"))
    return match d:
        Dual::Pair(ref mut a, b) => a.len() + eat(b)
        Dual::Empty => 0u64
`, "WON-SEM-010")

	assertFailCode(t, "OpaqueTupleMutRefPlusMoveRejected", `
fn eat(s: String) -> u64:
    return s.len()

fn main() -> u64:
    var pair = (String::from("mutref"), String::from("own"))
    return match pair:
        (ref mut a, b) => a.len() + eat(b)
`, "WON-SEM-010")

	// 5. 直接结构体模式精确字段拆分（shared-ref / ref mut + 按值部分移动）
	assertPass(t, "DirectStructMixedRefMutAndMovePreservesBorrowedField", `
struct PairStr:
    var a: String
    var b: String

fn eat(s: String) -> u64:
    return s.len()

fn main() -> u64:
    var pair = PairStr { a: String::from("mut"), b: String::from("move") }
    let x = match pair:
        PairStr { a: ref mut ra, b } => ra.len() + eat(b)
    return x + pair.a.len()
`)

	// 6. 嵌套混合模式：直接嵌套结构体混合合法通过；直接结构体内部嵌套非法混合元组显式报 WON-SEM-010
	assertPass(t, "NestedDirectStructMixedRefAndPartialMove", `
struct Inner:
    a: String
    b: String

struct Outer:
    inner: Inner
    tag: String

fn eat(s: String) -> u64:
    return s.len()

fn main() -> u64:
    let o = Outer {
        inner: Inner { a: String::from("aa"), b: String::from("bb") },
        tag: String::from("tag")
    }
    let res = match o:
        Outer { inner: Inner { a, b: ref rb }, tag: ref rt } => eat(a) + rb.len() + rt.len()
    return res + o.inner.b.len() + o.tag.len()
`)

	assertFailCode(t, "NestedDirectStructWithOpaqueMixedSubpatternRejected", `
struct WrapTuple:
    pair: (String, String)
    id: i32

fn eat(s: String) -> u64:
    return s.len()

fn main() -> u64:
    let w = WrapTuple { pair: (String::from("a"), String::from("b")), id: 1 }
    return match w:
        WrapTuple { pair: (ref a, b), id } => a.len() + eat(b)
`, "WON-SEM-010")

	// 7. Copy 字段与非 Copy 字段混合（直接结构体部分移动后 Copy 字段仍可读取；纯 Copy 与 ref 共存不触发移动）
	assertPass(t, "DirectStructCopyBesideNonCopyFieldPreservesCopyField", `
struct Mixed:
    count: i32
    label: String

fn eat(s: String) -> u64:
    return s.len()

fn main() -> i32:
    let m = Mixed { count: 7, label: String::from("txt") }
    let _len = match m:
        Mixed { count, label } => eat(label)
    return m.count
`)

	assertPass(t, "OpaqueTupleRefPlusCopyBindingPreservesSubject", `
fn use_tup(t: (String, i32)) -> i32:
    return 1

fn main() -> i32:
    let t: (String, i32) = (String::from("keep"), 42i32)
    let _v = match t:
        (ref s, c) => c + 1i32
    return use_tup(t)
`)

	assertFailCode(t, "OpaqueTupleCopyBesideNonCopyMovesAggregate", `
fn eat(s: String) -> u64:
    return s.len()

fn use_tup(t: (String, i32)) -> u64:
    return 1u64

fn main() -> u64:
    let t: (String, i32) = (String::from("gone"), 9i32)
    let n = match t:
        (s, c) => eat(s)
    return n + use_tup(t)
`, "WON-MEM-001")
}

// TestPhase2B12GuardedPatternOwnership 验证带守卫 match 分支的控制流顺序、Copy/Ref 守卫通过性及守卫消费保守拒绝契约。
func TestPhase2B12GuardedPatternOwnership(t *testing.T) {
	// 1. 仅引用绑定带守卫：守卫失败流转不污染后续分支及主体
	assertPass(t, "GuardedRefOnlyArmPass", `
enum OptStr:
    Some(String)
    None

fn take(o: OptStr) -> u64:
    return 1u64

fn main() -> u64:
    let opt = OptStr::Some(String::from("guarded"))
    let n = match opt:
        OptStr::Some(ref r) if r.len() > 2u64 => r.len()
        OptStr::Some(ref r) => r.len()
        OptStr::None => 0u64
    return n + take(opt)
`)

	// 2. Copy 类型按值绑定带守卫：允许并在守卫失败时完整保留主体与后续复用
	assertPass(t, "GuardedByValueCopyBindingAllowed", `
enum OptNum:
    Some(i32)
    None

struct Point:
    x: i32
    y: i32

fn main() -> i32:
    let opt = OptNum::Some(12)
    let a = match opt:
        OptNum::Some(v) if v > 10 => v
        OptNum::Some(v) => v + 1
        OptNum::None => 0
    let p = Point { x: 5, y: 3 }
    let b = match p:
        Point { x, y } if x > y => x
        Point { x, y } => y
    return a + b + p.x
`)

	// 3. 非 Copy 按值绑定带守卫（枚举 / 元组 / 直接结构体）：显式 WON-SEM-010，禁止守卫前提前 Move
	assertFailCode(t, "GuardedNonCopyEnumMoveRejectedSem010", `
enum OptStr:
    Some(String)
    None

fn eat(s: String) -> u64:
    return s.len()

fn take(o: OptStr) -> u64:
    return 1u64

fn main() -> u64:
    let opt = OptStr::Some(String::from("secret"))
    let n = match opt:
        OptStr::Some(s) if s.len() > 0u64 => eat(s)
        _ => take(opt)
    return n
`, "WON-SEM-010")

	assertFailCode(t, "GuardedNonCopyStructMoveRejectedSem010", `
struct PairStr:
    a: String
    b: String

fn eat(s: String) -> u64:
    return s.len()

fn main() -> u64:
    let p = PairStr { a: String::from("a"), b: String::from("b") }
    return match p:
        PairStr { a, b: ref rb } if rb.len() > 0u64 => eat(a)
        _ => 0u64
`, "WON-SEM-010")

	assertFailCode(t, "GuardedNonCopyIdentMoveRejectedSem010", `
fn eat(s: String) -> u64:
    return s.len()

fn main() -> u64:
    let s = String::from("ident")
    return match s:
        v if v.len() > 0u64 => eat(v)
        _ => 0u64
`, "WON-SEM-010")

	// 4 & 5 & 6. 守卫失败并落空至后续按值或主体复用分支，主体未被提前移走
	assertPass(t, "GuardFailFallsThroughToSubjectArmSafely", `
enum OptStr:
    Some(String)
    None

fn take(o: OptStr) -> u64:
    return 1u64

fn main() -> u64:
    let opt = OptStr::Some(String::from("fallthrough"))
    let flag = false
    let res = match opt:
        OptStr::Some(ref r) if flag => r.len()
        _ => take(opt)
    return res
`)

	// 7. 多重连续带守卫分支级联落空后仍保留完整主体
	assertPass(t, "MultipleGuardedArmsCascadePreserveSubject", `
enum OptStr:
    Some(String)
    None

fn take(o: OptStr) -> u64:
    return 1u64

fn main() -> u64:
    let opt = OptStr::Some(String::from("cascade"))
    let res = match opt:
        OptStr::Some(ref r) if r.len() > 100u64 => 1u64
        OptStr::Some(ref r) if r.len() > 50u64 => 2u64
        OptStr::Some(ref r) if r.len() > 10u64 => 3u64
        _ => take(opt)
    return res
`)

	// 8. 嵌套守卫与分支内嵌套带守卫 match 均正确收敛
	assertPass(t, "NestedGuardedMatchPass", `
enum OptStr:
    Some(String)
    None

fn take(o: OptStr) -> u64:
    return 1u64

fn main() -> u64:
    let opt = OptStr::Some(String::from("nested"))
    let mode = true
    let out = match opt:
        OptStr::Some(ref r) if r.len() > 2u64 =>
            match mode:
                true if r.len() < 100u64 => r.len()
                _ => 0u64
        _ => 0u64
    return out + take(opt)
`)
}

package borrow_test

import "testing"

// TestPhase2B2_2UserFacingClosure 覆盖 Phase 2B.2.2 全部源码级正反例要求（经由真实 parser -> checker -> cfg -> borrow 管线）：
// 包含 *mut 原生指针显式权限与保守别名冲突、共享/独占方法与聚合候选不重叠检验。
func TestPhase2B2_2UserFacingClosure(t *testing.T) {
	// 1. 合法显式 *mut T unsafe 路径：无重叠借用冲突且未逃逸安全返回值时通过
	assertPass(t, "ValidExplicitUnsafeMutRawPtrWrite", `
fn write_raw(p: *mut i32):
    unsafe:
        *p = 42

fn main():
    var x: i32 = 1
    let rp = &mut x as *mut i32
    write_raw(rp)
`)

	// 2. 从 *mut 原生指针衍生可变重借用若与同一函数内活跃借用重叠，保守按 Unknown 别名冲突拒绝
	assertFailCode(t, "RawPtrDerivedMutBorrowConservativelyOverlapsActiveLoan", `
fn sink_mut(a: &mut i32, b: &mut i32):
    *a = 2
    *b = 3

fn conflict_raw(p: *mut i32):
    var other: i32 = 10
    let r = &mut other
    unsafe:
        let raw_ref = &mut *p
        sink_mut(raw_ref, r)
`, "WON-MEM-005")

	// 3. *mut 原生指针衍生可变引用在活跃期与传入候选并列调用 pick_mut 时按未知别名保守拒绝 (WON-MEM-005)
	assertFailCode(t, "RawPtrUnknownAliasInCandidateDisjointnessRejected", `
fn pick_mut(c: bool, a: &mut i32, b: &mut i32) -> &mut i32:
    if c:
        return a
    return b

fn main(c: bool, p: *mut i32):
    var x: i32 = 1
    unsafe:
        let r = pick_mut(c, &mut *p, &mut x)
        *r = 2
`, "WON-MEM-005")

	// 4. 聚合返回字段中同时存在共享 (&i32) 与独占 (&mut i32) 候选，若不同字段可能别名同一变量则拒绝
	assertFailCode(t, "AggregateCrossFieldDifferentParamsOverlappingAlias", `
struct SplitView:
    ro: &i32
    rw: &mut i32

struct TwoVars:
    var v: i32

fn build_split(a: &i32, b: &mut i32) -> SplitView:
    return SplitView { ro: a, rw: b }

fn main():
    var t = TwoVars { v: 5 }
    let s = build_split(&t.v, &mut t.v)
    *s.rw = 9
    println(*s.ro)
`, "WON-MEM-004")

	// 5. 合法聚合跨字段互不重叠字段 (&p.a vs &mut p.b) 与方法精确 self 下标正例
	assertPass(t, "AggregateDisjointFieldsAndMethodSelfPass", `
struct Pair:
    var a: i32
    var b: i32

struct SplitView:
    ro: &i32
    rw: &mut i32

impl Pair:
    fn borrow_a_mut(&mut self, ignored: &i32) -> &mut i32:
        return &mut self.a

fn make_split(ro: &i32, rw: &mut i32) -> SplitView:
    return SplitView { ro: ro, rw: rw }

fn main():
    var p = Pair { a: 1, b: 2 }
    var extra: i32 = 3
    let ma = p.borrow_a_mut(&extra)
    extra = 4
    *ma = 10
    let sv = make_split(&p.a, &mut p.b)
    println(*sv.ro)
    *sv.rw = 20
`)

	// 6. Verify provenance summaries preserve exact self=0 & explicit arg indices
	srcMethod := `
struct Pair:
    var a: i32
    var b: i32

impl Pair:
    fn select(&mut self, c: bool, ext: &mut i32) -> &mut i32:
        if c:
            return &mut self.a
        return ext
`
	tp, prov := inferProvenanceFromSource(t, srcMethod)
	for _, fn := range tp.CFG.Functions {
		if fn.Name == "select" {
			sum := prov.Summaries[fn.FuncSym.ID]
			got := sum.FieldProvs[""].SortedParamIndices()
			if len(got) != 2 || got[0] != 0 || got[1] != 2 || !sum.ReturnIsMut {
				t.Fatalf("Pair::select 期望 ReturnIsMut=true 且候选 [0 2]，实际 ReturnIsMut=%v cands=%v", sum.ReturnIsMut, got)
			}
		}
	}

	// 7. 混合结构体参数：嵌套投影提取 &mut 字段合法通过；混合分支混入 shared 字段作 &mut 返回被 ParamPaths 精确拦截
	assertPass(t, "NestedStructParamProjectedMutRefReturnPass", `
struct Inner:
    rw: &mut i32
    ro: &i32

struct Outer:
    inner: Inner

fn pick_nested_mut(o: Outer) -> &mut i32:
    return o.inner.rw
`)

	assertFailCode(t, "MixedStructParamSharedFieldReturnedViaIntermediateMutRejected", `
struct Mixed:
    ro: &i32
    rw: &mut i32

fn sneaky_field(c: bool, m: Mixed, fallback: &mut i32) -> &mut i32:
    let r = m.ro
    if c:
        return &mut *r
    return fallback
`, "WON-MEM-004")

	// 8. *const T 与 *mut T 在函数返回安全引用时必须拒绝 (WON-MEM-003)
	assertFailCode(t, "RawConstPtrParamCannotReturnSafeRef", `
fn bad_raw_const(p: *const i32) -> &i32:
    return unsafe:
        &*p
`, "WON-MEM-003")

	assertFailCode(t, "RawMutPtrParamCannotReturnSafeMutRef", `
fn bad_raw_mut(p: *mut i32) -> &mut i32:
    return unsafe:
        &mut *p
`, "WON-MEM-003")
}

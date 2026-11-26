package borrow_test

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/borrow"
	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
)

// TestPhase2B2_1MutabilityAndReborrowClosure 覆盖 Critical Issue A：
// 严格区分绑定可变性与引用权限，拒绝从共享不可变引用 (&T) 创建任何形式的 &mut 重借用或可变访问。
func TestPhase2B2_1MutabilityAndReborrowClosure(t *testing.T) {
	// 1. 直接或间接通过不可变 &T 重借用为 &mut T -> WON-MEM-004
	assertFailCode(t, "SharedParamMutReborrow", `
fn bad(p: &i32):
    let q = &mut *p
    *q = 42
`, "WON-MEM-004")

	assertFailCode(t, "SharedParamMutReborrowReturn", `
fn bad(p: &String) -> &mut String:
    return &mut *p
`, "WON-MEM-004")

	assertFailCode(t, "LocalSharedRefMutReborrow", `
fn bad():
    var x: i32 = 1
    let r = &x
    let q = &mut *r
    *q = 2
`, "WON-MEM-004")

	assertFailCode(t, "VarBindingHoldingSharedRefCannotMutDeref", `
fn bad():
    var x: i32 = 1
    var r = &x
    let q = &mut *r
    *q = 2
`, "WON-MEM-004")

	assertFailCode(t, "CopiedSharedRefCannotMutReborrow", `
fn bad():
    let x: i32 = 1
    let r1 = &x
    let r2 = r1
    let q = &mut *r2
`, "WON-MEM-004")

	assertFailCode(t, "SharedStructRefFieldMutReborrow", `
struct Holder:
    var value: i32

fn bad(h: &Holder):
    let q = &mut h.value
    *q = 99
`, "WON-MEM-004")

	assertFailCode(t, "SharedStructRefStarFieldMutReborrow", `
struct Holder:
    var value: i32

fn bad(h: &Holder):
    let q = &mut (*h).value
    *q = 99
`, "WON-MEM-004")

	assertFailCode(t, "NestedMutOfSharedDeref", `
fn bad(pp: &mut &i32):
    let q = &mut **pp
`, "WON-MEM-004")

	assertFailCode(t, "NestedSharedOfMutDeref", `
fn bad():
    var x: i32 = 1
    var p = &mut x
    let pp = &p
    let q = &mut **pp
`, "WON-MEM-004")

	assertFailCode(t, "DowngradedMutReborrowThenMutReborrow", `
fn bad(r: &mut i32):
    let s = &*r
    let q = &mut *s
`, "WON-MEM-004")

	assertFailCode(t, "StructHoldingSharedRefFieldMutReborrow", `
struct RefBox:
    r: &i32

fn bad():
    var x: i32 = 1
    let b = RefBox { r: &x }
    let q = &mut *b.r
`, "WON-MEM-004")

	// 2. 通过 var 引用绑定或嵌套不可变外层引用发起写操作 / 可变方法调用 -> WON-MUT-001 / WON-ADT-004
	assertFailCode(t, "VarBindingSharedStringPushStr", `
fn bad():
    var s = String::from("hi")
    var r = &s
    r.push_str("!")
`, "WON-MUT-001")

	assertFailCode(t, "NestedSharedOfMutMethodCall", `
fn bad():
    var s = String::from("hi")
    var p = &mut s
    let pp = &p
    (*pp).push_str("!")
`, "WON-MUT-001")

	assertFailCode(t, "NestedSharedOfMutDerefAssign", `
fn bad():
    var x: i32 = 1
    var p = &mut x
    let pp = &p
    *(*pp) = 42
`, "WON-MUT-001")

	assertFailCode(t, "VarBindingSharedStructFieldWrite", `
struct Holder:
    var value: i32

fn bad(h: Holder):
    var r = &h
    r.value = 10
`, "WON-ADT-004")

	// 3. 合法可变与共享重借用正例
	assertPass(t, "MutReborrowFromMutParamPass", `
fn good(p: &mut i32):
    let q = &mut *p
    *q = 42
`)

	assertPass(t, "LetBindingMutRefAllowsMutReborrow", `
fn main():
    var x: i32 = 10
    let p = &mut x
    let q = &mut *p
    *q = 42
    println(*p)
`)

	assertPass(t, "MutHolderFieldMutReborrowPass", `
struct Holder:
    var value: i32

fn good(h: &mut Holder):
    let q = &mut h.value
    *q = 42

fn main():
    var h = Holder { value: 1 }
    good(&mut h)
`)

	assertPass(t, "SharedReborrowFromMutParamPass", `
fn view_val(p: &mut i32) -> &i32:
    return &*p
`)

	assertPass(t, "NestedMutOfMutDerefPass", `
fn good():
    var x: i32 = 1
    var p = &mut x
    let pp = &mut p
    let q = &mut **pp
    *q = 7
`)

	assertPass(t, "MutHolderStarFieldAssignPass", `
struct Holder:
    var value: i32

fn good(h: &mut Holder):
    (*h).value = 77
`)
}

// TestPhase2B2_1MethodSelfProvenanceClosure 覆盖 Critical Issue B：
// 验证 HasSelf 方法的参数下标与 ArgTemps/EffectiveParamType 规范对齐 (self=0, explicit args=1..N)。
func TestPhase2B2_1MethodSelfProvenanceClosure(t *testing.T) {
	// 1. 验证方法摘要 Exact ParamIndices：self(0) 与显式参数(>=1) 不偏移
	srcMethods := `
struct Counter:
    var value: i32
    label: String

impl Counter:
    fn get_val(&self) -> &i32:
        return &self.value

    fn get_mut(&mut self) -> &mut i32:
        return &mut self.value

    fn pick(&self, cond: bool, other: &String) -> &String:
        if cond:
            return &self.label
        return other

    fn first_only(&self, other: &String) -> &String:
        return &self.label

    fn second_only(&self, other: &String) -> &String:
        return other
`
	tp, prov := inferProvenanceFromSource(t, srcMethods)
	expectMethodParams := map[string][]int{
		"get_val":     {0},
		"get_mut":     {0},
		"pick":        {0, 2},
		"first_only":  {0},
		"second_only": {1},
	}
	for _, fn := range tp.CFG.Functions {
		exp, ok := expectMethodParams[fn.Name]
		if !ok {
			continue
		}
		sum := prov.Summaries[fn.FuncSym.ID]
		fp := sum.FieldProvs[""]
		got := fp.SortedParamIndices()
		if len(got) != len(exp) {
			t.Fatalf("方法 %s 期望候选下标 %v，实际 %v", fn.Name, exp, got)
		}
		for i := range exp {
			if got[i] != exp[i] {
				t.Fatalf("方法 %s 期望候选下标 %v，实际 %v", fn.Name, exp, got)
			}
		}
	}

	// 2. 调用点精密锁定：仅返回 self 时可修改未选中的 other；释放返回借用后可修改 self
	assertPass(t, "MethodFirstOnlyDoesNotOverlockOther", `
struct Box:
    var label: String

impl Box:
    fn first(&self, other: &String) -> &String:
        return &self.label

fn main():
    var b = Box { label: String::from("a") }
    var other = String::from("b")
    let r = b.first(&other)
    other.push_str("!")
    println(r)
    b.label.push_str("?")
`)

	// 3. 负例：&self + 后面携带 &mut 参数，试图通过 self 返回 &mut 必须捕获为 WON-MEM-004（修复越位读到 arg0）
	assertFailCode(t, "MethodImmutableSelfWithMutArgCannotReturnMut", `
struct Counter:
    var value: i32

impl Counter:
    fn bad(&self, extra: &mut i32) -> &mut i32:
        return &mut self.value
`, "WON-MEM-004")

	// 4. 负例：&mut self 试图从后面的不可变显式参数 other 返回 &mut（修复越界跳过 ParamTypes 检查）
	assertFailCode(t, "MethodMutSelfCannotReturnMutFromImmutableOther", `
struct Counter:
    var value: i32

impl Counter:
    fn bad(&mut self, other: &i32) -> &mut i32:
        return &mut *other
`, "WON-MEM-004")

	// 5. 负例：方法内返回局部变量或值接收者 self 的引用 -> WON-MEM-003
	assertFailCode(t, "MethodByValueSelfReturnEscape", `
struct Counter:
    label: String

impl Counter:
    fn bad(self) -> &String:
        return &self.label
`, "WON-MEM-003")

	assertFailCode(t, "MethodLocalVarReturnEscape", `
struct Counter:
    label: String

impl Counter:
    fn bad(&self) -> &String:
        let local = String::from("temp")
        return &local
`, "WON-MEM-003")

	// 6. 负例：多候选方法在调用点返回借用仍然锁定 self 与 other（直到 last use 之后）
	assertFailCode(t, "MethodPickStillLocksSelf", `
struct Box:
    var label: String

impl Box:
    fn pick(&self, c: bool, other: &String) -> &String:
        if c:
            return &self.label
        return other

fn main(c: bool):
    var b = Box { label: String::from("a") }
    var other = String::from("b")
    let r = b.pick(c, &other)
    b.label.push_str("!")
    println(r)
`, "WON-MEM-004")

	assertFailCode(t, "MethodPickStillLocksOther", `
struct Box:
    var label: String

impl Box:
    fn pick(&self, c: bool, other: &String) -> &String:
        if c:
            return &self.label
        return other

fn main(c: bool):
    var b = Box { label: String::from("a") }
    var other = String::from("b")
    let r = b.pick(c, &other)
    other.push_str("!")
    println(r)
`, "WON-MEM-004")
}

// TestPhase2B2_1AggregateReturnProvenanceClosure 覆盖 Critical Issue C：
// 验证按结构体字段 / 元组元素路径拆分候选溯源与独立可变性，以及不可安全投影含引用聚合的 WON-SEM-010 拒止。
func TestPhase2B2_1AggregateReturnProvenanceClosure(t *testing.T) {
	// 1. 验证 MixedStruct / Tuple 按字段记录独立候选与 IsMut
	srcMixed := `
struct Mixed:
    left: &i32
    right: &mut i32

struct Pair:
    var x: i32
    var y: i32

fn make_mixed(a: &i32, b: &mut i32) -> Mixed:
    return Mixed { left: a, right: b }

fn split_tuple(p: &mut Pair) -> (&i32, &mut i32):
    return (&p.x, &mut p.y)

fn static_and_mut(b: &mut i32) -> (&str, &mut i32):
    return ("won", b)
`
	tp, prov := inferProvenanceFromSource(t, srcMixed)
	for _, fn := range tp.CFG.Functions {
		sum := prov.Summaries[fn.FuncSym.ID]
		switch fn.Name {
		case "make_mixed":
			fl := sum.FieldProvs["left"]
			fr := sum.FieldProvs["right"]
			if fl == nil || fl.IsMut || !fl.ParamIndices[0] || fl.ParamIndices[1] {
				t.Fatalf("make_mixed.left 摘要异常: %+v", fl)
			}
			if fr == nil || !fr.IsMut || !fr.ParamIndices[1] || fr.ParamIndices[0] {
				t.Fatalf("make_mixed.right 摘要异常: %+v", fr)
			}
		case "split_tuple":
			f0 := sum.FieldProvs["[0]"]
			f1 := sum.FieldProvs["[1]"]
			if f0 == nil || f0.IsMut || !f0.ParamIndices[0] {
				t.Fatalf("split_tuple[0] 摘要异常: %+v", f0)
			}
			if f1 == nil || !f1.IsMut || !f1.ParamIndices[0] {
				t.Fatalf("split_tuple[1] 摘要异常: %+v", f1)
			}
		case "static_and_mut":
			f0 := sum.FieldProvs["[0]"]
			f1 := sum.FieldProvs["[1]"]
			if f0 == nil || f0.IsMut || !f0.HasStatic {
				t.Fatalf("static_and_mut[0] 摘要异常: %+v", f0)
			}
			if f1 == nil || !f1.IsMut || !f1.ParamIndices[0] {
				t.Fatalf("static_and_mut[1] 摘要异常: %+v", f1)
			}
		}
	}

	// 2. 正例：通过混合结构体字段拆分精确跟踪两个底层变量的独立借用期与 NLL 释放
	assertPass(t, "MixedStructReturnIndependentFieldLifetimes", `
struct Mixed:
    left: &i32
    right: &mut i32

fn make_mixed(a: &i32, b: &mut i32) -> Mixed:
    return Mixed { left: a, right: b }

fn main():
    var x: i32 = 10
    var y: i32 = 20
    let m = make_mixed(&x, &mut y)
    println(*m.left)
    x = 11
    *m.right = 99
`)

	// 3. 负例：聚合内局部引用逃逸 -> WON-MEM-003
	assertFailCode(t, "TupleLocalEscapeRejected", `
fn esc(a: &String) -> (&String, &String):
    let local = String::from("tmp")
    return (a, &local)
`, "WON-MEM-003")

	assertFailCode(t, "StructFieldLocalEscapeRejected", `
struct Mixed:
    left: &i32
    right: &mut i32

fn esc(b: &mut i32) -> Mixed:
    let local: i32 = 1
    return Mixed { left: &local, right: b }
`, "WON-MEM-003")

	// 4. 负例：在仍持有 m.right 可变字段活跃借用时写底层 y -> WON-MEM-006
	assertFailCode(t, "MixedStructMutFieldStillLocked", `
struct Mixed:
    left: &i32
    right: &mut i32

fn make_mixed(a: &i32, b: &mut i32) -> Mixed:
    return Mixed { left: a, right: b }

fn main():
    var x: i32 = 10
    var y: i32 = 20
    let m = make_mixed(&x, &mut y)
    y = 5
    *m.right = 99
`, "WON-MEM-006")

	// 5. 负例：含引用多候选择一对重叠实参传入聚合返回 -> WON-MEM-005
	assertFailCode(t, "AggregateMutCandidateOverlapRejected", `
struct Pair:
    var left: i32
    var right: i32

struct MutBox:
    item: &mut i32

fn choose_box(c: bool, a: &mut Pair, b: &mut i32) -> MutBox:
    if c:
        return MutBox { item: &mut a.left }
    return MutBox { item: b }

fn main(c: bool):
    var p = Pair { left: 1, right: 2 }
    let mb = choose_box(c, &mut p, &mut p.left)
    *mb.item = 42
`, "WON-MEM-005")

	// 6. 负例：不可投影的含引用枚举 / 数组 / 切片返回按 Fail-Closed 拒止 -> WON-SEM-010
	assertFailCode(t, "EnumWithNonStrViewRefRejected", `
enum RefOpt:
    Some(&String)
    None

fn wrap_ref(s: &String) -> RefOpt:
    return RefOpt::Some(s)
`, "WON-SEM-010")

	assertFailCode(t, "OptionMutRefReturnRejected", `
fn bad_opt(x: &mut i32) -> Option[&mut i32]:
    return Option::Some(x)
`, "WON-SEM-010")

	assertFailCode(t, "ArrayWithRefReturnRejected", `
fn bad_arr(a: &String, b: &String) -> [&String; 2]:
    return [a, b]
`, "WON-SEM-010")
}

// TestPhase2B2_1OpaqueBoundaryClosure 覆盖 Section 5：
// 严格禁止无体声明或函数指针借由 &str (StrView) 漏洞假定 Static 溯源，覆盖无体方法与结构体字段函数指针。
func TestPhase2B2_1OpaqueBoundaryClosure(t *testing.T) {
	// 1. 零输入函数指针返回 &str 不再免除 WON-MEM-002
	assertFailCode(t, "FnPtrZeroParamStrViewRejected", `
fn register(f: fn() -> &str):
    println("cb")
`, "WON-MEM-002")

	// 2. 结构体字段内部包含不透明多引用函数指针 -> WON-MEM-002
	assertFailCode(t, "StructFieldFnPtrMultiRefRejected", `
struct Hooks:
    picker: fn(&String, &String) -> &String
`, "WON-MEM-002")

	// 3. 单共享输入函数指针却返回 &mut -> WON-MEM-004
	assertFailCode(t, "FnPtrSharedInputMutReturnRejected", `
fn bad(cb: fn(&String) -> &mut String):
    println("nope")
`, "WON-MEM-004")

	// 4. 真实无函数体 (d.Body == nil) 的声明函数返回 &str (0 引用输入) 与多引用无体方法 -> WON-MEM-002
	assertBodylessRejected := func(name, src string, customBodyless func(prog *ast.Program)) {
		t.Run(name, func(t *testing.T) {
			rep := diagnostic.NewReport()
			lex := lexer.New(name+".won", src, rep)
			p := parser.New(lex, rep, name+".won")
			prog := p.ParseProgram()
			if rep.HasErrors() {
				t.Fatalf("解析失败:\n%s", rep.String())
			}
			customBodyless(prog)
			chk := checker.New(rep, nil)
			tp := chk.CheckProgram(prog)
			if tp != nil && tp.CFG != nil {
				borrow.AnalyzeProgram(tp.CFG, tp, rep)
			}
			for _, d := range rep.Diagnostics {
				if d.Code == "WON-MEM-002" {
					return
				}
			}
			t.Fatalf("%s 期望 WON-MEM-002，实际:\n%s", name, rep.String())
		})
	}

	assertBodylessRejected("BodylessZeroParamStrViewFunc", `
fn ext_str() -> &str:
    return "stub"
`, func(prog *ast.Program) {
		for _, decl := range prog.Decls {
			if fd, ok := decl.(*ast.FunctionDecl); ok {
				fd.Body = nil
			}
		}
	})

	assertBodylessRejected("BodylessMethodTwoRefs", `
struct Box:
    val: String

impl Box:
    fn ext_pick(&self, other: &String) -> &String:
        return other
`, func(prog *ast.Program) {
		for _, decl := range prog.Decls {
			if ib, ok := decl.(*ast.ImplBlock); ok {
				for _, m := range ib.Methods {
					m.Body = nil
				}
			}
		}
	})

	// 5. 正例：拥有可分析函数体的 &str 函数、单输入函数指针与单 &mut 函数指针通过
	assertPass(t, "BodyWithStrLiteralAndSingleRefFnPtrs", `
fn ok_lit() -> &str:
    return "won"

fn use_single(cb: fn(&String) -> &String, s: &String) -> &String:
    return s

fn use_mut(cb: fn(&mut String) -> &mut String, s: &mut String) -> &mut String:
    return s
`)
}

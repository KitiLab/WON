package checker

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
)

func TestRawPointerCreationSafeAndDereferenceRequiresUnsafe(t *testing.T) {
	validSrc := `
fn create_and_deref(addr: usize, val: i32) -> i32:
    var mval: i32 = 1
    # 构造原生指针是安全的，允许在 unsafe 块外执行
    let p_const: *const i32 = addr as *const i32
    let p_from_ref = &val as *const i32
    let p_mut = &mut mval as *mut i32

    unsafe:
        *p_mut = 77
        let a = *p_const
        let b = *p_from_ref
        return a + b + *p_mut
`
	_, repOK := runCheckSource(t, validSrc)
	if repOK.HasErrors() {
		t.Fatalf("安全创建原生指针及在 unsafe 内解引用不应报错:\n%s", repOK.String())
	}

	invalidCases := []struct {
		name string
		src  string
		code string
	}{
		{
			name: "ConstPtrDerefOutsideUnsafe",
			src:  "fn read_val(ptr: *const i32) -> i32:\n    return *ptr\n",
			code: "WON-SEC-002",
		},
		{
			name: "MutPtrDerefOutsideUnsafe",
			src:  "fn read_mut(ptr: *mut i32) -> i32:\n    return *ptr\n",
			code: "WON-SEC-002",
		},
		{
			name: "NestedDerefOutsideUnsafe",
			src:  "fn calc(ptr: *const i32) -> i32:\n    return 1 + *ptr\n",
			code: "WON-SEC-002",
		},
		{
			name: "WriteThroughMutPtrOutsideUnsafe",
			src:  "fn write_mut(ptr: *mut i32):\n    *ptr = 10\n",
			code: "WON-SEC-002",
		},
		{
			name: "PointerArithmeticRejected",
			src:  "fn ptr_add(ptr: *const i32) -> *const i32:\n    unsafe:\n        return ptr + 1\n",
			code: "WON-TYP-002",
		},
		{
			name: "ImmutableRefToMutPtrCastRejected",
			src:  "fn bad_cast(x: i32) -> *mut i32:\n    return &x as *mut i32\n",
			code: "WON-TYP-005",
		},
		{
			name: "I32ToRawPtrCastRejected",
			src:  "fn bad_int_cast(x: i32) -> *const i32:\n    return x as *const i32\n",
			code: "WON-TYP-005",
		},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			_, report := runCheckSource(t, tc.src)
			assertHasDiagCode(t, report, tc.code)
		})
	}
}

func TestMethodSignatureValidationNoDuplicateDiagnostics(t *testing.T) {
	// 1. 内置方法严格校验实参数量、实参类型与接收者可变性
	builtinInvalid := []struct {
		name string
		src  string
		code string
	}{
		{"LenWithExtraArg", "fn main():\n    let t = \"hello\"\n    let l = t.len(123)\n", "WON-TYP-001"},
		{"AsPtrWithExtraArg", "fn main():\n    let t = \"hello\"\n    let p = t.as_ptr(10)\n", "WON-TYP-001"},
		{"AsBytesWithArg", "fn main():\n    let t = \"hello\"\n    let b = t.as_bytes(1)\n", "WON-TYP-001"},
		{"IsEmptyWithArg", "fn main():\n    let t = \"hello\"\n    let e = t.is_empty(false)\n", "WON-TYP-001"},
		{"PushStrMissingArg", "fn main():\n    var s = String::from(\"hi\")\n    s.push_str()\n", "WON-TYP-001"},
		{"PushStrWrongArgType", "fn main():\n    var s = String::from(\"hi\")\n    s.push_str(42)\n", "WON-TYP-001"},
		{"PushStrImmutableReceiver", "fn main():\n    let s = String::from(\"hi\")\n    s.push_str(\"!\")\n", "WON-MUT-001"},
		{"AsMutPtrOnStrViewRejected", "fn main():\n    let t = \"hello\"\n    let p = t.as_mut_ptr()\n", "WON-NAM-006"},
	}
	for _, tc := range builtinInvalid {
		t.Run(tc.name, func(t *testing.T) {
			_, rep := runCheckSource(t, tc.src)
			assertHasDiagCode(t, rep, tc.code)
			if len(rep.Diagnostics) != 1 {
				t.Errorf("用例 %s 应当仅产出 1 条准确诊断，实际产出 %d 条:\n%s", tc.name, len(rep.Diagnostics), rep.String())
			}
		})
	}

	// 2. 用户自定义方法完整校验 (合法接收者、上下文整型字面量、CallTargets 记录、仅检查一次参数)
	userValidSrc := `
struct Counter:
    var value: i32

impl Counter:
    fn create(init: i32) -> Counter:
        return Counter { value: init }

    fn add(&mut self, amount: i32) -> unit:
        self.value += amount

    fn add_small(&self, byte_delta: u8) -> u8:
        return byte_delta

fn main() -> u8:
    var c = Counter::create(10)
    c.add(5)
    return c.add_small(255)
`
	tp, repUserOK := runCheckSource(t, userValidSrc)
	if repUserOK.HasErrors() {
		t.Fatalf("合法用户方法调用校验失败:\n%s", repUserOK.String())
	}
	foundCallTarget := false
	for expr, sym := range tp.CallTargets {
		if mc, ok := expr.(*ast.MethodCallExpr); ok && mc.Method == "add" && sym != nil && sym.Name == "add" {
			foundCallTarget = true
		}
	}
	if !foundCallTarget {
		t.Errorf("TypedProgram.CallTargets 未记录用户方法 Counter.add 调用目标")
	}

	// 3. 用户自定义方法非法用例且验证零重复诊断
	userInvalid := []struct {
		name      string
		src       string
		code      string
		wantCount int
	}{
		{
			name:      "MutSelfOnImmutableInstance",
			src:       "struct C:\n    var v: i32\nimpl C:\n    fn add(&mut self, a: i32):\n        self.v += a\nfn main():\n    let c = C { v: 1 }\n    c.add(2)\n",
			code:      "WON-MUT-001",
			wantCount: 1,
		},
		{
			name:      "MissingArgument",
			src:       "struct C:\n    var v: i32\nimpl C:\n    fn add(&mut self, a: i32):\n        self.v += a\nfn main():\n    var c = C { v: 1 }\n    c.add()\n",
			code:      "WON-TYP-001",
			wantCount: 1,
		},
		{
			name:      "ExtraArgument",
			src:       "struct C:\n    var v: i32\nimpl C:\n    fn add(&mut self, a: i32):\n        self.v += a\nfn main():\n    var c = C { v: 1 }\n    c.add(1, 2)\n",
			code:      "WON-TYP-001",
			wantCount: 1,
		},
		{
			name:      "WrongArgumentTypeCheckedOnce",
			src:       "struct C:\n    var v: i32\nimpl C:\n    fn add(&mut self, a: i32):\n        self.v += a\nfn main():\n    var c = C { v: 1 }\n    c.add(\"nope\")\n",
			code:      "WON-TYP-001",
			wantCount: 1,
		},
		{
			name:      "ContextualLiteralOverflowCheckedOnce",
			src:       "struct C:\n    v: i32\nimpl C:\n    fn take(&self, b: u8):\n        return\nfn main():\n    let c = C { v: 1 }\n    c.take(256)\n",
			code:      "WON-TYP-003",
			wantCount: 1,
		},
		{
			name:      "StaticMethodCalledOnInstance",
			src:       "struct C:\n    v: i32\nimpl C:\n    fn create() -> C:\n        return C { v: 0 }\nfn main():\n    let c = C { v: 1 }\n    let x = c.create()\n",
			code:      "WON-NAM-006",
			wantCount: 1,
		},
		{
			name:      "UnknownMethod",
			src:       "struct C:\n    v: i32\nfn main():\n    let c = C { v: 1 }\n    let x = c.missing()\n",
			code:      "WON-NAM-006",
			wantCount: 1,
		},
	}

	for _, tc := range userInvalid {
		t.Run(tc.name, func(t *testing.T) {
			_, rep := runCheckSource(t, tc.src)
			assertHasDiagCode(t, rep, tc.code)
			if len(rep.Diagnostics) != tc.wantCount {
				t.Errorf("期望产出 %d 条诊断（不得因二次检查参数产生重复错误），实际为 %d 条:\n%s", tc.wantCount, len(rep.Diagnostics), rep.String())
			}
		})
	}
}

func TestMatchJoinAndComparisonTypeSafety(t *testing.T) {
	// 验证 &mut i32 与 &i32 分支联结后的整体 match 表达式类型确实为 &i32（而非误保持第一分支类型 &mut i32）
	joinSrc := `
fn pick(flag: bool, a: &mut i32, b: &i32) -> &i32:
    let joined = match flag:
        true => a
        false => b
    return joined
`
	tp, rep := runCheckSource(t, joinSrc)
	if rep.HasErrors() {
		t.Fatalf("&mut T 与 &T 在 match 中联结为 &T 应当通过校验:\n%s", rep.String())
	}

	verifiedMatchType := false
	for expr, tid := range tp.ExprTypes {
		if _, ok := expr.(*ast.MatchExpr); ok {
			if tp.Types.Format(tid) != "&i32" {
				t.Fatalf("match 表达式最终推断类型必须为 '&i32'，实际为 '%s'", tp.Types.Format(tid))
			}
			verifiedMatchType = true
		}
	}
	if !verifiedMatchType {
		t.Errorf("未在 TypedProgram.ExprTypes 中找到 MatchExpr 记录")
	}

	// 验证上下文整数联结与一致比较通过
	validJoinSrc := `
fn test_cmp_and_match(flag: bool, a: i32, b: i32) -> u8:
    let same_cmp = a == b and a < 10
    let val: u8 = match same_cmp:
        true => 1
        false => 255
    return val
`
	_, rep2 := runCheckSource(t, validJoinSrc)
	if rep2.HasErrors() {
		t.Fatalf("合法上下文整型 match 与同类比较不应报错:\n%s", rep2.String())
	}

	invalidJoinAndCmp := []struct {
		name string
		src  string
		code string
	}{
		{
			name: "ReverseMutabilityAssignmentFromMatchRejected",
			src:  "fn pick(flag: bool, a: &mut i32, b: &i32) -> &mut i32:\n    return match flag:\n        true => a\n        false => b\n",
			code: "WON-MAT-003",
		},
		{
			name: "ReverseMutabilityLetFromMatchRejected",
			src:  "fn pick(flag: bool, a: &mut i32, b: &i32):\n    let r: &mut i32 = match flag:\n        true => a\n        false => b\n",
			code: "WON-MAT-003",
		},
		{
			name: "IncompatibleIntWidthsInMatch",
			src:  "fn f(b: bool) -> i64:\n    return match b:\n        true => 1i32\n        false => 2i64\n",
			code: "WON-MAT-003",
		},
		{
			name: "IncompatibleTupleArmsInMatch",
			src:  "fn f(b: bool):\n    let t = match b:\n        true => (1i32, 2i32)\n        false => (1i64, 2i32)\n",
			code: "WON-MAT-003",
		},
		{
			name: "IncompatibleStructArmsInMatch",
			src:  "struct A:\n    x: i32\nstruct B:\n    x: i32\nfn f(flag: bool):\n    let x = match flag:\n        true => A { x: 1 }\n        false => B { x: 2 }\n",
			code: "WON-MAT-003",
		},
		{
			name: "ComparisonIncompatibleNumericWidths",
			src:  "fn f(a: i32, b: i64) -> bool:\n    return a == b\n",
			code: "WON-TYP-001",
		},
		{
			name: "OrderingComparisonIncompatibleWidths",
			src:  "fn f(a: i32, b: i64) -> bool:\n    return a < b\n",
			code: "WON-TYP-001",
		},
		{
			name: "InvalidNestedRefMutabilityComparison",
			src:  "fn f(a: &(&mut i32), b: &(&i32)) -> bool:\n    return a == b\n",
			code: "WON-TYP-001",
		},
		{
			name: "StructEqualityUnsupportedWithoutTrait",
			src:  "struct S:\n    v: i32\nfn f(a: S, b: S) -> bool:\n    return a == b\n",
			code: "WON-TYP-002",
		},
	}

	for _, tc := range invalidJoinAndCmp {
		t.Run(tc.name, func(t *testing.T) {
			_, repErr := runCheckSource(t, tc.src)
			assertHasDiagCode(t, repErr, tc.code)
			for _, d := range repErr.Diagnostics {
				if d.Span.Start.Line < 1 || d.Span.Start.Column < 1 {
					t.Errorf("诊断 %s 缺少有效源码坐标: %+v", d.Code, diagnostic.Position(d.Span.Start))
				}
			}
		})
	}
}

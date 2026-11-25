package checker

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
)

func runCheckSource(t *testing.T, src string) (*TypedProgram, *diagnostic.Report) {
	t.Helper()
	report := diagnostic.NewReport()
	lex := lexer.New("semantic_test.won", src, report)
	p := parser.New(lex, report, "semantic_test.won")
	prog := p.ParseProgram()
	if report.HasErrors() {
		t.Fatalf("期望测试用例先通过词法与语法解析，却出现语法错误:\n%s", report.String())
	}
	chk := New(report, nil)
	tp := chk.CheckProgram(prog)
	return tp, report
}

func assertHasDiagCode(t *testing.T, report *diagnostic.Report, expectedCode string) {
	t.Helper()
	for _, d := range report.Diagnostics {
		if d.Code == expectedCode {
			if d.Span.Start.Line < 1 || d.Span.Start.Column < 1 {
				t.Errorf("诊断 %s 缺少有效源码坐标: %+v", expectedCode, d.Span)
			}
			return
		}
	}
	t.Fatalf("期望捕获诊断码 %s，实际输出:\n%s", expectedCode, report.String())
}

func TestTwoPassForwardCallsAndRecursion(t *testing.T) {
	src := `
fn main() -> i32:
    return add_one(41)

fn add_one(n: i32) -> i32:
    if n <= 0:
        return 0
    return n + 1
`
	tp, report := runCheckSource(t, src)
	if report.HasErrors() {
		t.Fatalf("合法的前向函数调用不应报错:\n%s", report.String())
	}
	if !tp.CodegenSupported {
		t.Errorf("纯函数/整数运算应兼容当前 C11 后端")
	}
}

func TestBidirectionalInferenceAndLiteralOverflow(t *testing.T) {
	validSrc := `
fn main() -> i32:
    let a: u8 = 255
    let b: i8 = -128
    let c = a as i32
    return c + (b as i32)
`
	_, report := runCheckSource(t, validSrc)
	if report.HasErrors() {
		t.Fatalf("合法上下文整数字面量与显式转型不应报错:\n%s", report.String())
	}

	overflowSrc := `
fn main():
    let bad: u8 = 300
`
	_, repErr := runCheckSource(t, overflowSrc)
	assertHasDiagCode(t, repErr, "WON-TYP-003")
}

func TestRejectUnsizedDSTByValue(t *testing.T) {
	src := `
fn main():
    let s: str = "hello"
`
	_, report := runCheckSource(t, src)
	assertHasDiagCode(t, report, "WON-TYP-006")
}

func TestSameScopeDuplicateVsNestedShadowing(t *testing.T) {
	shadowOK := `
fn main() -> i32:
    let x: i32 = 10
    if true:
        let x: bool = false
        if !x:
            return 1
    return x
`
	_, repOK := runCheckSource(t, shadowOK)
	if repOK.HasErrors() {
		t.Fatalf("嵌套子作用域遮蔽应通过校验:\n%s", repOK.String())
	}

	dupErr := `
fn main():
    let x = 1
    let x = 2
`
	_, repDup := runCheckSource(t, dupErr)
	assertHasDiagCode(t, repDup, "WON-NAM-002")

	dupFnErr := `
fn helper():
    return
fn helper():
    return
`
	_, repFnDup := runCheckSource(t, dupFnErr)
	assertHasDiagCode(t, repFnDup, "WON-NAM-002")
}

func TestNameAndImportErrors(t *testing.T) {
	src := `
import nonexistent::pkg

fn main(x: MissingType):
    let y = unknown_var + 1
`
	report := diagnostic.NewReport()
	lex := lexer.New("names.won", src, report)
	p := parser.New(lex, report, "names.won")
	prog := p.ParseProgram()
	if report.HasErrors() {
		t.Fatalf("解析失败:\n%s", report.String())
	}
	chk := New(report, nil)
	chk.CheckProgram(prog)

	assertHasDiagCode(t, report, "WON-NAM-004")
	assertHasDiagCode(t, report, "WON-NAM-003")
	assertHasDiagCode(t, report, "WON-NAM-001")
}

func TestStrictTypeMismatchAndNoImplicitNumericCoercion(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
	}{
		{
			name: "StringToI32",
			src:  "fn main() -> i32:\n    let count: i32 = \"hello\"\n    return count\n",
			code: "WON-TYP-001",
		},
		{
			name: "MixedI32AndI64",
			src:  "fn add(a: i32, b: i64) -> i64:\n    return a + b\n",
			code: "WON-TYP-001",
		},
		{
			name: "NonBoolCondition",
			src:  "fn main():\n    if 42:\n        return\n",
			code: "WON-TYP-002",
		},
		{
			name: "InvalidExplicitCast",
			src:  "fn main():\n    let s = true as &str\n",
			code: "WON-TYP-005",
		},
		{
			name: "ReturnTypeMismatch",
			src:  "fn main() -> i32:\n    return \"nope\"\n",
			code: "WON-TYP-007",
		},
		{
			name: "BreakOutsideLoop",
			src:  "fn main():\n    break\n",
			code: "WON-NAM-005",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, report := runCheckSource(t, tc.src)
			assertHasDiagCode(t, report, tc.code)
		})
	}
}

func TestStructsMethodsAndFieldMutability(t *testing.T) {
	validSrc := `
struct Point:
    x: i64
    var y: i64

impl Point:
    fn sum(self) -> i64:
        return self.x + self.y

fn main() -> i64:
    let x: i64 = 3
    var p = Point { x, y: 4 }
    p.y = 10
    return p.sum()
`
	tp, report := runCheckSource(t, validSrc)
	if report.HasErrors() {
		t.Fatalf("合法结构体与固有方法校验失败:\n%s", report.String())
	}
	if tp.CodegenSupported {
		t.Errorf("使用 struct 与 impl 方法的程序必须标记 CodegenSupported = false 以防止 C 后端静默错误生成")
	}

	missingFieldSrc := `
struct Pair:
    a: i32
    b: i32
fn main():
    let p = Pair { a: 1 }
`
	_, repMissing := runCheckSource(t, missingFieldSrc)
	assertHasDiagCode(t, repMissing, "WON-ADT-002")

	immFieldMutSrc := `
struct Pair:
    a: i32
fn main():
    let p = Pair { a: 1 }
    p.a = 2
`
	_, repImm := runCheckSource(t, immFieldMutSrc)
	assertHasDiagCode(t, repImm, "WON-ADT-004")

	unknownMethodSrc := `
struct Box:
    v: i32
fn main():
    let b = Box { v: 1 }
    let r = b.nonexistent()
`
	_, repMeth := runCheckSource(t, unknownMethodSrc)
	assertHasDiagCode(t, repMeth, "WON-NAM-006")
}

func TestEnumsAndMatchExhaustiveness(t *testing.T) {
	validSrc := `
enum State:
    Idle
    Running(i32)

fn eval(s: State) -> i32:
    return match s:
        State::Idle => 0
        State::Running(code) => code

fn eval_bool(flag: bool) -> i32:
    return match flag:
        true => 1
        false => 0
`
	_, report := runCheckSource(t, validSrc)
	if report.HasErrors() {
		t.Fatalf("穷尽性匹配校验失败:\n%s", report.String())
	}

	nonExhaustiveSrc := `
enum State:
    Idle
    Running(i32)

fn eval(s: State) -> i32:
    return match s:
        State::Idle => 0
`
	_, repNonExh := runCheckSource(t, nonExhaustiveSrc)
	assertHasDiagCode(t, repNonExh, "WON-MAT-001")

	unreachableArmSrc := `
fn test_bool(flag: bool) -> i32:
    return match flag:
        true => 1
        false => 0
        _ => 2
`
	_, repUnreach := runCheckSource(t, unreachableArmSrc)
	assertHasDiagCode(t, repUnreach, "WON-MAT-004")

	armMismatchSrc := `
fn test_arm(flag: bool) -> i32:
    return match flag:
        true => 1
        false => "bad"
`
	_, repArm := runCheckSource(t, armMismatchSrc)
	assertHasDiagCode(t, repArm, "WON-MAT-003")
}

func TestOptionResultAndTryPropagation(t *testing.T) {
	validSrc := `
fn step_ok(v: i32) -> Result[i32, &str]:
    if v < 0:
        return Result::Err("negative")
    return Result::Ok(v + 1)

fn pipeline(v: i32) -> Result[i32, &str]:
    let next = step_ok(v)?
    return Result::Ok(next * 2)

fn opt_chain(o: Option[i32]) -> Option[i32]:
    let val = o?
    return Option::Some(val + 1)
`
	_, report := runCheckSource(t, validSrc)
	if report.HasErrors() {
		t.Fatalf("合法 Result/Option ? 错误传播校验失败:\n%s", report.String())
	}

	errMismatchSrc := `
fn inner() -> Result[i32, i32]:
    return Result::Err(5)

fn outer() -> Result[i32, bool]:
    let x = inner()?
    return Result::Ok(x)
`
	_, repErrMis := runCheckSource(t, errMismatchSrc)
	assertHasDiagCode(t, repErrMis, "WON-ERR-001")

	tryOutsideResultFn := `
fn inner() -> Result[i32, i32]:
    return Result::Ok(1)

fn bad() -> i32:
    let x = inner()?
    return x
`
	_, repOut := runCheckSource(t, tryOutsideResultFn)
	assertHasDiagCode(t, repOut, "WON-ERR-001")
}

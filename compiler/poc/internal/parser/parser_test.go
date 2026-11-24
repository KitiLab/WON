package parser

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
)

func parseSource(t *testing.T, src string) (*ast.Program, *diagnostic.Report) {
	t.Helper()
	report := diagnostic.NewReport()
	lex := lexer.New("test.won", src, report)
	p := New(lex, report, "test.won")
	prog := p.ParseProgram()
	return prog, report
}

func TestParserCanonicalProgramAndDeclarations(t *testing.T) {
	src := `
#import <zlib.h>
import std::fs as fs
from math import identity, Matrix

pub type ByteSlice = [u8]

pub struct Point[T]:
    pub x: T
    var y: T

pub enum Shape:
    Circle(f64)
    Rectangle:
        width: f64
        height: f64

pub trait Drawable:
    fn draw(&self)

impl Drawable for Point[f64]:
    fn draw(&self):
        println("x={self.x}")

fn main() -> i32:
    var buf: [u8; 256] = [0u8; 256]
    let len = 256
    while len > 0:
        for i in 0..10:
            if i == 5:
                continue
            elif i == 8:
                break
        return 0
    return 0
`
	prog, report := parseSource(t, src)
	if report.HasErrors() {
		t.Fatalf("合法规范 WON 程序不应报错:\n%s", report.String())
	}

	if len(prog.Imports) != 1 || prog.Imports[0].HeaderPath != "<zlib.h>" {
		t.Errorf("期望解析 1 个 #import <zlib.h>")
	}
	if len(prog.ModuleImports) != 2 {
		t.Errorf("期望解析 2 个模块导入，实际为 %d", len(prog.ModuleImports))
	}
	if len(prog.Functions) != 1 || prog.Functions[0].Name != "main" {
		t.Errorf("期望解析顶层 main 函数")
	}
	// Imports + 2 module imports + type alias + struct + enum + trait + impl + fn = 9
	if len(prog.Decls) != 9 {
		t.Errorf("期望顶层共有 9 项声明，实际为 %d", len(prog.Decls))
	}
}

func TestParserExpressionPrecedenceAndAssociativity(t *testing.T) {
	src := `
fn test_prec(a: i32, b: i32) -> bool:
    let math = 1 + 2 * 3
    let cast_mul = 10 * a as i64
    let unary_cast = -a as i64
    let cmp_logic = a + 1 < b and b != 0 or not false
    let rng = 0..a + 1
    let chained = items[0].process()?.value
    return cmp_logic
`
	prog, report := parseSource(t, src)
	if report.HasErrors() {
		t.Fatalf("合法优先级测试解析失败:\n%s", report.String())
	}

	stmts := prog.Functions[0].Body.Statements
	// 1. 1 + 2 * 3 => BinaryExpr(+, 1, BinaryExpr(*, 2, 3))
	let1 := stmts[0].(*ast.LetStmt)
	addExpr, ok := let1.Init.(*ast.BinaryExpr)
	if !ok || addExpr.Op != "+" {
		t.Fatalf("期望顶层运算符为 '+'，实际为 %T", let1.Init)
	}
	if mulExpr, ok := addExpr.Right.(*ast.BinaryExpr); !ok || mulExpr.Op != "*" {
		t.Errorf("期望右侧先结合乘法 '*'，实际为 %T", addExpr.Right)
	}

	// 2. 10 * a as i64 => BinaryExpr(*, 10, CastExpr(a, i64))
	let2 := stmts[1].(*ast.LetStmt)
	mulCast, ok := let2.Init.(*ast.BinaryExpr)
	if !ok || mulCast.Op != "*" {
		t.Fatalf("期望乘法 '*' 在转换 'as' 之外层，实际为 %T", let2.Init)
	}
	if _, ok := mulCast.Right.(*ast.CastExpr); !ok {
		t.Errorf("期望 'a as i64' 作为乘法右操作数，实际为 %T", mulCast.Right)
	}

	// 3. -a as i64 => CastExpr(UnaryExpr(-, a), i64)
	let3 := stmts[2].(*ast.LetStmt)
	castUnary, ok := let3.Init.(*ast.CastExpr)
	if !ok {
		t.Fatalf("期望顶层为 CastExpr，实际为 %T", let3.Init)
	}
	if _, ok := castUnary.Expr.(*ast.UnaryExpr); !ok {
		t.Errorf("期望一元负号优先于 'as' 转换，实际内部为 %T", castUnary.Expr)
	}

	// 4. 0..a + 1 => RangeExpr(0, BinaryExpr(+, a, 1))
	let5 := stmts[4].(*ast.LetStmt)
	rng, ok := let5.Init.(*ast.RangeExpr)
	if !ok {
		t.Fatalf("期望 RangeExpr，实际为 %T", let5.Init)
	}
	if _, ok := rng.End.(*ast.BinaryExpr); !ok {
		t.Errorf("期望范围右侧先计算加法 'a + 1'，实际为 %T", rng.End)
	}

	// 5. items[0].process()?.value => FieldAccessExpr(TryExpr(MethodCallExpr(IndexExpr(items, 0), process)), value)
	let6 := stmts[5].(*ast.LetStmt)
	fa, ok := let6.Init.(*ast.FieldAccessExpr)
	if !ok || fa.Field != "value" {
		t.Fatalf("期望后缀链最外层为 .value 字段访问，实际为 %T", let6.Init)
	}
	tr, ok := fa.Object.(*ast.TryExpr)
	if !ok {
		t.Fatalf("期望 FieldAccess 内部为 TryExpr '?'，实际为 %T", fa.Object)
	}
	if _, ok := tr.Expr.(*ast.MethodCallExpr); !ok {
		t.Errorf("期望 '?' 作用对象为 MethodCallExpr，实际为 %T", tr.Expr)
	}
}

func TestParserMatchAndConcurrencyAndInterpolation(t *testing.T) {
	src := `
fn complex(ev: WebEvent):
    match ev:
        WebEvent::PageLoad => println("Loaded")
        WebEvent::Click { x, y } if x > 0 =>
            println("Click at ({x + 1}, {y})")

    scope s:
        let t = s.spawn:
            compute(1, 2)
        t.join()?
`
	prog, report := parseSource(t, src)
	if report.HasErrors() {
		t.Fatalf("复杂模式匹配与结构化并发语法解析失败:\n%s", report.String())
	}

	fn := prog.Functions[0]
	if len(fn.Body.Statements) != 2 {
		t.Fatalf("期望函数包含 2 条主语句 (match 和 scope)，实际为 %d", len(fn.Body.Statements))
	}
	esMatch := fn.Body.Statements[0].(*ast.ExprStmt)
	mExpr, ok := esMatch.Expr.(*ast.MatchExpr)
	if !ok || len(mExpr.Arms) != 2 {
		t.Fatalf("期望 MatchExpr 包含 2 个分支，实际为 %T", esMatch.Expr)
	}
	if mExpr.Arms[1].Guard == nil {
		t.Errorf("期望第 2 个 match 分支保留守卫条件 'if x > 0'")
	}

	esScope := fn.Body.Statements[1].(*ast.ExprStmt)
	scExpr, ok := esScope.Expr.(*ast.ScopeExpr)
	if !ok || scExpr.Name != "s" {
		t.Fatalf("期望 ScopeExpr(name='s')，实际为 %T", esScope.Expr)
	}
}

func TestParserRejectsLegacyBraceSyntaxAndErrors(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantCode string
	}{
		{
			name: "LegacyBraceBlockFunction",
			src: `fn main() -> i32 {
    return 0;
}`,
			wantCode: "WON-SYN-005",
		},
		{
			name: "LegacyLetMut",
			src: `fn main():
    let mut x = 10
`,
			wantCode: "WON-SYN-009",
		},
		{
			name: "MissingIndentedBlock",
			src: `fn main():
fn other():
    return
`,
			wantCode: "WON-SYN-004",
		},
		{
			name: "UnexpectedIndent",
			src: `fn main():
    let x = 1
        let y = 2
`,
			wantCode: "WON-SYN-002",
		},
		{
			name: "MissingClosingParen",
			src: `fn main():
    let x = foo(1, 2
`,
			wantCode: "WON-SYN-007",
		},
		{
			name: "InvalidAssignTarget",
			src: `fn main():
    1 + 2 = 3
`,
			wantCode: "WON-SYN-009",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, report := parseSource(t, tc.src)
			if !report.HasErrors() {
				t.Fatalf("%s 期望报告语法错误，但实际未报错", tc.name)
			}
			found := false
			for _, d := range report.Diagnostics {
				if d.Code == tc.wantCode {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s 期望诊断码 %s，实际输出:\n%s", tc.name, tc.wantCode, report.String())
			}
		})
	}
}

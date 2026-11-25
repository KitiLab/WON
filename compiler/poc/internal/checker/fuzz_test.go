package checker

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
)

// FuzzLexerParse 对词法与语法解析器执行任意字节及恶意缩进模糊测试，验证零 Panic 崩溃且不产生零行号幽灵坐标
func FuzzLexerParse(f *testing.F) {
	seeds := []string{
		"fn main() -> i32:\n    return 0\n",
		"fn add(a: i32, b: i32) -> i32:\n    return a + b\n",
		"\xff\xfe\xfd",
		"struct Point:\n    x: f64\n    var y: f64\n",
		"enum Color:\n    Red\n    Rgb(u8, u8, u8)\n",
		"fn foo():\n\tlet x = \"unterminated\n",
		"match x:\n    Some(v) => v\n    None => 0\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		report := diagnostic.NewReport()
		lex := lexer.New("fuzz.won", input, report)
		p := parser.New(lex, report, "fuzz.won")
		_ = p.ParseProgram()
		for _, d := range report.Diagnostics {
			if d.Span.Start.Line < 1 || d.Span.Start.Column < 1 {
				t.Fatalf("诊断坐标越界: %+v", d)
			}
		}
	})
}

// FuzzSemanticChecker 对完整前端管线 (Lexer -> Parser -> TypeChecker) 执行随机与变异源码模糊测试，验证语义分析阶段永不 Panic 或死循环
func FuzzSemanticChecker(f *testing.F) {
	seeds := []string{
		"fn main() -> i32:\n    let x: i32 = 42\n    return x\n",
		"fn main():\n    let x: i32 = \"bad\"\n",
		"struct S:\n    x: i32\nfn main():\n    let s = S { x: 1 }\n",
		"enum E:\n    A\n    B(i32)\nfn f(e: E) -> i32:\n    match e:\n        E::A => 1\n",
		"fn f(x: Result[i32, bool]) -> Result[i32, bool]:\n    let v = x?\n    return Result::Ok(v)\n",
		"fn dup():\n    let x = 1\n    let x = 2\n",
		"fn bad_utf8():\n    let x = \"\x80\"\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		report := diagnostic.NewReport()
		lex := lexer.New("fuzz.won", input, report)
		p := parser.New(lex, report, "fuzz.won")
		prog := p.ParseProgram()
		if !report.HasErrors() && prog != nil {
			chk := New(report, nil)
			_ = chk.CheckProgram(prog)
		}
	})
}

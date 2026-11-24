package lexer

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/token"
)

func TestLexerCanonicalIndentation(t *testing.T) {
	src := `#import <zlib.h>
fn main() -> i32:
    let x = 10

    # 注释行不应改变缩进
    if x > 5:
        print(x)

    return 0
`
	report := diagnostic.NewReport()
	lex := New("test.won", src, report)

	expected := []token.Kind{
		token.CIMPORT, token.NEWLINE,
		token.FN, token.IDENT, token.LPAREN, token.RPAREN, token.RARROW, token.IDENT, token.COLON, token.NEWLINE,
		token.INDENT,
		token.LET, token.IDENT, token.ASSIGN, token.INT, token.NEWLINE,
		token.IF, token.IDENT, token.GT, token.INT, token.COLON, token.NEWLINE,
		token.INDENT,
		token.IDENT, token.LPAREN, token.IDENT, token.RPAREN, token.NEWLINE,
		token.DEDENT,
		token.RETURN, token.INT, token.NEWLINE,
		token.DEDENT,
		token.EOF,
	}

	for i, exp := range expected {
		tok := lex.NextToken()
		if tok.Kind != exp {
			t.Fatalf("[%d] 期望 Token %s, 实际得到 %s (%q) 位于 %d:%d",
				i, exp, tok.Kind, tok.Lexeme, tok.Span.Start.Line, tok.Span.Start.Column)
		}
	}

	if report.HasErrors() {
		t.Fatalf("合法缩进源码不应产生错误: %s", report.String())
	}
}

func TestLexerMultipleDedentsAndEOFWithoutNewline(t *testing.T) {
	src := "fn a():\n    if true:\n        if false:\n            return 1"
	report := diagnostic.NewReport()
	lex := New("nested.won", src, report)

	dedentCount := 0
	indentCount := 0
	for {
		tok := lex.NextToken()
		if tok.Kind == token.INDENT {
			indentCount++
		} else if tok.Kind == token.DEDENT {
			dedentCount++
		} else if tok.Kind == token.EOF {
			break
		}
	}

	if indentCount != 3 {
		t.Errorf("期望 3 个 INDENT，实际为 %d", indentCount)
	}
	if dedentCount != 3 {
		t.Errorf("期望 EOF 时自动闭合发出 3 个 DEDENT，实际为 %d", dedentCount)
	}
	if report.HasErrors() {
		t.Fatalf("无尾随换行的嵌套合法缩进不应报错: %s", report.String())
	}
}

func TestLexerTabRejectionWONSyn001(t *testing.T) {
	src := "fn foo():\n\tlet x = 1\n"
	report := diagnostic.NewReport()
	lex := New("tab.won", src, report)

	for tok := lex.NextToken(); tok.Kind != token.EOF; tok = lex.NextToken() {
	}

	if !report.HasErrors() {
		t.Fatalf("前导制表符缩进应当被 WON-SYN-001 拦截")
	}
	if report.Diagnostics[0].Code != "WON-SYN-001" {
		t.Errorf("期望诊断码 WON-SYN-001，实际为 %s", report.Diagnostics[0].Code)
	}
}

func TestLexerUnexpectedIndentAndInconsistentDedent(t *testing.T) {
	t.Run("NonFourSpaceIndent", func(t *testing.T) {
		src := "fn foo():\n  let x = 1\n"
		report := diagnostic.NewReport()
		lex := New("two_space.won", src, report)
		for tok := lex.NextToken(); tok.Kind != token.EOF; tok = lex.NextToken() {
		}
		if !report.HasErrors() || report.Diagnostics[0].Code != "WON-SYN-002" {
			t.Errorf("期望诊断码 WON-SYN-002，实际输出: %s", report.String())
		}
	})

	t.Run("InconsistentDedent", func(t *testing.T) {
		src := "fn foo():\n    if true:\n        let x = 1\n      let y = 2\n"
		report := diagnostic.NewReport()
		lex := New("bad_dedent.won", src, report)
		for tok := lex.NextToken(); tok.Kind != token.EOF; tok = lex.NextToken() {
		}
		found := false
		for _, d := range report.Diagnostics {
			if d.Code == "WON-SYN-003" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("期望不一致缩进回退错误 WON-SYN-003，实际输出: %s", report.String())
		}
	})
}

func TestLexerMultilineDelimitersAndLineContinuation(t *testing.T) {
	src := `fn test():
    let arr = [
        1,
        2,
        3
    ]
    let sum = 1 + \
        2 + \
        3
`
	report := diagnostic.NewReport()
	lex := New("multiline.won", src, report)

	indentCount := 0
	for tok := lex.NextToken(); tok.Kind != token.EOF; tok = lex.NextToken() {
		if tok.Kind == token.INDENT {
			indentCount++
		}
	}

	if report.HasErrors() {
		t.Fatalf("多行括号表达式与反斜杠续行不应报错: %s", report.String())
	}
	if indentCount != 1 {
		t.Errorf("多行列表与续行内部不应触发额外的 INDENT，期望总 INDENT=1，实际为 %d", indentCount)
	}
}

func TestLexerNumericLiteralsStringsAndUnicode(t *testing.T) {
	src := `let 计数 = 1_000_000i64
let byte_val = b'A'
let char_val = '\n'
let hex_val = 0xFF_FFu32
let oct_val = 0o755
let bin_val = 0b1010_0101u8
let flt_val = 3.141_59f64
let raw_str = r"no \escape {here}"
let interp = "中文: {计数}"
let rng = 0..10
`
	report := diagnostic.NewReport()
	lex := New("literals.won", src, report)

	kinds := make([]token.Kind, 0)
	for tok := lex.NextToken(); tok.Kind != token.EOF; tok = lex.NextToken() {
		kinds = append(kinds, tok.Kind)
	}

	if report.HasErrors() {
		t.Fatalf("合法字面量与 Unicode 标识符不应产生词法错误: %s", report.String())
	}

	foundRange := false
	foundInterp := false
	foundFloat := false
	for _, k := range kinds {
		if k == token.DOTDOT {
			foundRange = true
		}
		if k == token.INTERP_STRING {
			foundInterp = true
		}
		if k == token.FLOAT {
			foundFloat = true
		}
	}
	if !foundRange || !foundInterp || !foundFloat {
		t.Errorf("期望正确识别范围 '..'(%v)、插值字符串(%v) 与浮点数(%v)", foundRange, foundInterp, foundFloat)
	}
}

func TestLexerInvalidCharactersAndStrings(t *testing.T) {
	t.Run("InvalidChar", func(t *testing.T) {
		report := diagnostic.NewReport()
		lex := New("test.won", "let a = @\n", report)
		for tok := lex.NextToken(); tok.Kind != token.EOF; tok = lex.NextToken() {
		}
		if !report.HasErrors() || report.Diagnostics[0].Code != "WON-LEX-002" {
			t.Errorf("期望错误码 WON-LEX-002, 实际: %s", report.String())
		}
	})

	t.Run("UnclosedString", func(t *testing.T) {
		report := diagnostic.NewReport()
		lex := New("test.won", "let s = \"unclosed string\n", report)
		for tok := lex.NextToken(); tok.Kind != token.EOF; tok = lex.NextToken() {
		}
		if !report.HasErrors() || report.Diagnostics[0].Code != "WON-LEX-001" {
			t.Errorf("期望错误码 WON-LEX-001, 实际: %s", report.String())
		}
	})

	t.Run("InvalidInterpolation", func(t *testing.T) {
		report := diagnostic.NewReport()
		lex := New("test.won", "let s = \"bad {interp\"\n", report)
		for tok := lex.NextToken(); tok.Kind != token.EOF; tok = lex.NextToken() {
		}
		if !report.HasErrors() || report.Diagnostics[0].Code != "WON-SYN-007" {
			t.Errorf("期望未闭合插值报错 WON-SYN-007, 实际: %s", report.String())
		}
	})
}

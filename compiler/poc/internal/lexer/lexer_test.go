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

func TestLexerUTF8ValidationAndExactByteOffsets(t *testing.T) {
	t.Run("ValidASCIIAndMultibytePositions", func(t *testing.T) {
		// "let 变量 = 1\n" -> 'let ' (4B), '变量' (6B, col 5..7), ' = ' (3B), '1' (byte 13, col 10)
		src := "let 变量 = 1\n"
		report := diagnostic.NewReport()
		lex := New("utf8_pos.won", src, report)
		if report.HasErrors() {
			t.Fatalf("合法 UTF-8 不应报错: %s", report.String())
		}
		toks := make([]token.Token, 0)
		for tok := lex.NextToken(); tok.Kind != token.EOF; tok = lex.NextToken() {
			toks = append(toks, tok)
		}
		// toks[3] 为 '1'
		intTok := toks[3]
		if intTok.Kind != token.INT || intTok.Span.Start.Byte != 13 || intTok.Span.Start.Column != 10 || intTok.Span.Start.Line != 1 {
			t.Errorf("期望整数 '1' 位于 byte=13, line=1, col=10，实际为 %+v", intTok.Span.Start)
		}
	})

	t.Run("ValidEncodedUFFFD", func(t *testing.T) {
		// 合法的三字节 UTF-8 编码的 U+FFFD (\xEF\xBF\xBD) 在字符串内不应报错且占 3 字节
		src := "let s = \"\xef\xbf\xbd\"\nlet x = 1\n"
		report := diagnostic.NewReport()
		lex := New("valid_fffd.won", src, report)
		var letX token.Token
		for tok := lex.NextToken(); tok.Kind != token.EOF; tok = lex.NextToken() {
			if tok.Kind == token.LET && tok.Span.Start.Line == 2 {
				letX = tok
			}
		}
		if report.HasErrors() {
			t.Fatalf("合法编码的 U+FFFD 不应报错: %s", report.String())
		}
		// "let s = \"" (9B) + U+FFFD (3B) + "\"\n" (2B) = 14B
		if letX.Span.Start.Byte != 14 {
			t.Errorf("合法 U+FFFD 后下一行起始字节应为 14，实际为 %d", letX.Span.Start.Byte)
		}
	})

	t.Run("CRLFWithUnicode", func(t *testing.T) {
		src := "let s = \"世界\"\r\nlet y = 2\r\n"
		report := diagnostic.NewReport()
		lex := New("crlf.won", src, report)
		var letY token.Token
		for tok := lex.NextToken(); tok.Kind != token.EOF; tok = lex.NextToken() {
			if tok.Kind == token.LET && tok.Span.Start.Line == 2 {
				letY = tok
			}
		}
		if report.HasErrors() {
			t.Fatalf("含 Unicode 的 CRLF 输入不应报错: %s", report.String())
		}
		// "let s = \"" (9) + "世界" (6) + "\"\r\n" (3) = 18
		if letY.Span.Start.Byte != 18 || letY.Span.Start.Line != 2 || letY.Span.Start.Column != 1 {
			t.Errorf("期望第 2 行起始位于 byte=18, line=2, col=1，实际为 %+v", letY.Span.Start)
		}
	})

	malformedCases := []struct {
		name     string
		src      string
		wantByte int
		wantLine int
		wantCol  int
	}{
		{"IsolatedContinuationByte", "let a = 1\n\x80let b = 2\n", 10, 2, 1},
		{"TruncatedTwoByteSeq", "let x = \"\xc3\"\n", 9, 1, 10},
		{"TruncatedThreeByteSeq", "let x = \"\xe2\x82\"\n", 9, 1, 10},
		{"InvalidLeadingByteFF", "let x = 1\nlet y = \xff\n", 18, 2, 9},
		{"InvalidAfterMultibyteText", "let 世 = 1\nlet b = \xff\n", 20, 2, 9},
		{"InvalidAfterMultipleLines", "fn f():\n    let x = 1\n    let y = \xfe\n", 34, 3, 13},
	}

	for _, tc := range malformedCases {
		t.Run(tc.name, func(t *testing.T) {
			report := diagnostic.NewReport()
			lex := New("bad_utf8.won", tc.src, report)
			for tok := lex.NextToken(); tok.Kind != token.EOF; tok = lex.NextToken() {
			}
			if !report.HasErrors() {
				t.Fatalf("%s 应当检测到非法的 UTF-8 字节序列", tc.name)
			}
			d := report.Diagnostics[0]
			if d.Code != "WON-LEX-004" {
				t.Errorf("%s 期望诊断码 WON-LEX-004，实际为 %s (%s)", tc.name, d.Code, d.Message)
			}
			if d.Span.Start.Byte != tc.wantByte || d.Span.Start.Line != tc.wantLine || d.Span.Start.Column != tc.wantCol {
				t.Errorf("%s 位置不匹配: 期望 byte=%d, line=%d, col=%d; 实际 byte=%d, line=%d, col=%d",
					tc.name, tc.wantByte, tc.wantLine, tc.wantCol, d.Span.Start.Byte, d.Span.Start.Line, d.Span.Start.Column)
			}
		})
	}

	t.Run("NoByteOffsetDriftAfterInvalidByteFollowingMultibyte", func(t *testing.T) {
		// "let 世 = 1 # \xff ok\nlet z = 3\n"
		// "let " (4) + "世" (3) + " = 1 # " (7) + "\xff" (1) + " ok\n" (4) = 19
		// 若错误按 U+FFFD(3B) 计算则第2行会偏移为 21；真实字节偏移必须为 19！
		src := "let 世 = 1 # \xff ok\nlet z = 3\n"
		report := diagnostic.NewReport()
		lex := New("no_drift.won", src, report)
		var letZ token.Token
		for tok := lex.NextToken(); tok.Kind != token.EOF; tok = lex.NextToken() {
			if tok.Kind == token.LET && tok.Span.Start.Line == 2 {
				letZ = tok
			}
		}
		if len(report.Diagnostics) == 0 || report.Diagnostics[0].Code != "WON-LEX-004" {
			t.Fatalf("应报告 WON-LEX-004")
		}
		if letZ.Span.Start.Byte != 19 || letZ.Span.Start.Line != 2 || letZ.Span.Start.Column != 1 {
			t.Errorf("非法 UTF-8 字节后发生偏移漂移！期望第 2 行 let 位于 byte=19, line=2, col=1，实际 %+v", letZ.Span.Start)
		}
	})
}

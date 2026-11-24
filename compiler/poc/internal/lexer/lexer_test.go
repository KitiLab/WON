package lexer

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/token"
)

func TestLexerValidTokens(t *testing.T) {
	src := `
#import <zlib.h>
fn main() -> i32 {
    let mut x: i32 = 42;
    let s = "hello\nworld";
    if (x == 42) {
        return 0;
    }
}
`
	report := diagnostic.NewReport()
	lex := New("test.won", src, report)

	expectedTokens := []token.Kind{
		token.IMPORT,
		token.FN, token.IDENT, token.LPAREN, token.RPAREN, token.RARROW, token.IDENT, token.LBRACE,
		token.LET, token.MUT, token.IDENT, token.COLON, token.IDENT, token.ASSIGN, token.INT, token.SEMICOLON,
		token.LET, token.IDENT, token.ASSIGN, token.STRING, token.SEMICOLON,
		token.IF, token.LPAREN, token.IDENT, token.EQ, token.INT, token.RPAREN, token.LBRACE,
		token.RETURN, token.INT, token.SEMICOLON,
		token.RBRACE,
		token.RBRACE,
		token.EOF,
	}

	for i, expected := range expectedTokens {
		tok := lex.NextToken()
		if tok.Kind != expected {
			t.Fatalf("[%d] 期望 Token %s, 实际得到 %s (%q) 位于 %d:%d",
				i, expected, tok.Kind, tok.Lexeme, tok.Span.Start.Line, tok.Span.Start.Column)
		}
	}

	if report.HasErrors() {
		t.Fatalf("正常代码词法分析不应产生错误: %s", report.String())
	}
}

func TestLexerInvalidCharacters(t *testing.T) {
	src := `let a = @;`
	report := diagnostic.NewReport()
	lex := New("test.won", src, report)

	for tok := lex.NextToken(); tok.Kind != token.EOF; tok = lex.NextToken() {
		// 消费所有 token
	}

	if !report.HasErrors() {
		t.Fatalf("遇到非法字符 '@' 应当产生错误诊断")
	}

	d := report.Diagnostics[0]
	if d.Code != "WON-LEX-002" {
		t.Errorf("期望错误码 WON-LEX-002, 实际为 %s", d.Code)
	}
}

func TestLexerUnclosedString(t *testing.T) {
	src := `let s = "unclosed string;`
	report := diagnostic.NewReport()
	lex := New("test.won", src, report)

	for tok := lex.NextToken(); tok.Kind != token.EOF; tok = lex.NextToken() {
	}

	if !report.HasErrors() {
		t.Fatalf("未闭合字符串应产生错误诊断")
	}

	d := report.Diagnostics[0]
	if d.Code != "WON-LEX-001" {
		t.Errorf("期望错误码 WON-LEX-001, 实际为 %s", d.Code)
	}
}

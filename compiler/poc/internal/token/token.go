// Package token 定义了 WON 语言词法单元类型与结构
package token

import (
	"fmt"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
)

// Kind 表示词法单元类型代码
type Kind int

const (
	EOF Kind = iota
	ILLEGAL

	// 字面量
	IDENT
	INT
	STRING

	// 关键字
	FN
	LET
	MUT
	IF
	ELSE
	WHILE
	RETURN
	UNSAFE
	IMPORT

	// 运算符与分隔符
	PLUS        // +
	MINUS       // -
	STAR        // *
	SLASH       // /
	ASSIGN      // =
	EQ          // ==
	NOTEQ       // !=
	LT          // <
	LTEQ        // <=
	GT          // >
	GTEQ        // >=
	NOT         // !
	AMP         // &
	COLONCOLON  // ::
	COLON       // :
	SEMICOLON   // ;
	COMMA       // ,
	DOT         // .
	RARROW      // ->
	LPAREN      // (
	RPAREN      // )
	LBRACE      // {
	RBRACE      // }
	LBRACKET    // [
	RBRACKET    // ]
	HASH        // #
)

var kindNames = map[Kind]string{
	EOF:        "EOF",
	ILLEGAL:    "ILLEGAL",
	IDENT:      "IDENT",
	INT:        "INT",
	STRING:     "STRING",
	FN:         "fn",
	LET:        "let",
	MUT:        "mut",
	IF:         "if",
	ELSE:       "else",
	RETURN:     "return",
	UNSAFE:     "unsafe",
	IMPORT:     "import",
	PLUS:       "+",
	MINUS:      "-",
	STAR:       "*",
	SLASH:      "/",
	ASSIGN:     "=",
	EQ:         "==",
	NOTEQ:      "!=",
	LT:         "<",
	LTEQ:       "<=",
	GT:         ">",
	GTEQ:       ">=",
	NOT:        "!",
	AMP:        "&",
	COLONCOLON: "::",
	COLON:      ":",
	SEMICOLON:  ";",
	COMMA:      ",",
	DOT:        ".",
	RARROW:     "->",
	LPAREN:     "(",
	RPAREN:     ")",
	LBRACE:     "{",
	RBRACE:     "}",
	LBRACKET:   "[",
	RBRACKET:   "]",
	HASH:       "#",
}

// String 返回词法类型的字面名称
func (k Kind) String() string {
	if name, ok := kindNames[k]; ok {
		return name
	}
	return fmt.Sprintf("Token(%d)", k)
}

// Keywords 关键字映射表
var Keywords = map[string]Kind{
	"fn":     FN,
	"let":    LET,
	"mut":    MUT,
	"if":     IF,
	"else":   ELSE,
	"while":  WHILE,
	"return": RETURN,
	"unsafe": UNSAFE,
	"import": IMPORT,
}

// LookupIdent 识别标识符是否属于保留关键字
func LookupIdent(ident string) Kind {
	if tok, ok := Keywords[ident]; ok {
		return tok
	}
	return IDENT
}

// Token 表示词法分析器产生的单项词元
type Token struct {
	Kind   Kind
	Lexeme string
	Span   diagnostic.Span
}

func (t Token) String() string {
	return fmt.Sprintf("Token(%s, %q, %d:%d)", t.Kind, t.Lexeme, t.Span.Start.Line, t.Span.Start.Column)
}

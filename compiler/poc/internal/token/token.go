// Package token 定义了 WON 语言标准词法单元类型与结构
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
	NEWLINE
	INDENT
	DEDENT

	// 字面量与标识符
	IDENT
	INT
	FLOAT
	CHAR
	BYTE
	STRING
	RAW_STRING
	INTERP_STRING

	// 预处理指令
	CIMPORT // #import <header.h> 或 #import "header.h"

	// 关键字
	AND
	AS
	BREAK
	CONTINUE
	ELIF
	ELSE
	ENUM
	FALSE
	FN
	FOR
	FROM
	IF
	IMPL
	IMPORT
	IN
	LET
	MATCH
	MOD
	MUT
	NOT_KW
	OR
	PUB
	REF
	RETURN
	SCOPE
	SELF
	SELF_TYPE
	SPAWN
	STRUCT
	TRAIT
	TRUE
	TYPE
	UNSAFE
	VAR
	WHILE

	// 算术与位运算符号
	PLUS       // +
	MINUS      // -
	STAR       // *
	SLASH      // /
	PERCENT    // %
	ASSIGN     // =
	PLUSEQ     // +=
	MINUSEQ    // -=
	STAREQ     // *=
	SLASHEQ    // /=
	PERCENTEQ  // %=
	AMPEQ      // &=
	PIPEEQ     // |=
	CARETEQ    // ^=
	LSHIFTEQ   // <<=
	RSHIFTEQ   // >>=
	EQ         // ==
	NOTEQ      // !=
	LT         // <
	LTEQ       // <=
	GT         // >
	GTEQ       // >=
	NOT        // !
	TILDE      // ~
	AMP        // &
	PIPE       // |
	CARET      // ^
	LSHIFT     // <<
	RSHIFT     // >>
	DOTDOT     // ..
	DOTDOTEQ   // ..=
	COLONCOLON // ::
	COLON      // :
	SEMICOLON  // ;
	COMMA      // ,
	DOT        // .
	RARROW     // ->
	FATARROW   // =>
	QUESTION   // ?
	HASH       // #
	AT         // @

	// 界定符
	LPAREN   // (
	RPAREN   // )
	LBRACE   // {
	RBRACE   // }
	LBRACKET // [
	RBRACKET // ]
)

var kindNames = map[Kind]string{
	EOF:           "EOF",
	ILLEGAL:       "ILLEGAL",
	NEWLINE:       "NEWLINE",
	INDENT:        "INDENT",
	DEDENT:        "DEDENT",
	IDENT:         "IDENT",
	INT:           "INT",
	FLOAT:         "FLOAT",
	CHAR:          "CHAR",
	BYTE:          "BYTE",
	STRING:        "STRING",
	RAW_STRING:    "RAW_STRING",
	INTERP_STRING: "INTERP_STRING",
	CIMPORT:       "#import",
	AND:           "and",
	AS:            "as",
	BREAK:         "break",
	CONTINUE:      "continue",
	ELIF:          "elif",
	ELSE:          "else",
	ENUM:          "enum",
	FALSE:         "false",
	FN:            "fn",
	FOR:           "for",
	FROM:          "from",
	IF:            "if",
	IMPL:          "impl",
	IMPORT:        "import",
	IN:            "in",
	LET:           "let",
	MATCH:         "match",
	MOD:           "mod",
	MUT:           "mut",
	NOT_KW:        "not",
	OR:            "or",
	PUB:           "pub",
	REF:           "ref",
	RETURN:        "return",
	SCOPE:         "scope",
	SELF:          "self",
	SELF_TYPE:     "Self",
	SPAWN:         "spawn",
	STRUCT:        "struct",
	TRAIT:         "trait",
	TRUE:          "true",
	TYPE:          "type",
	UNSAFE:        "unsafe",
	VAR:           "var",
	WHILE:         "while",
	PLUS:          "+",
	MINUS:         "-",
	STAR:          "*",
	SLASH:         "/",
	PERCENT:       "%",
	ASSIGN:        "=",
	PLUSEQ:        "+=",
	MINUSEQ:       "-=",
	STAREQ:        "*=",
	SLASHEQ:       "/=",
	PERCENTEQ:     "%=",
	AMPEQ:         "&=",
	PIPEEQ:        "|=",
	CARETEQ:       "^=",
	LSHIFTEQ:      "<<=",
	RSHIFTEQ:      ">>=",
	EQ:            "==",
	NOTEQ:         "!=",
	LT:            "<",
	LTEQ:          "<=",
	GT:            ">",
	GTEQ:          ">=",
	NOT:           "!",
	TILDE:         "~",
	AMP:           "&",
	PIPE:          "|",
	CARET:         "^",
	LSHIFT:        "<<",
	RSHIFT:        ">>",
	DOTDOT:        "..",
	DOTDOTEQ:      "..=",
	COLONCOLON:    "::",
	COLON:         ":",
	SEMICOLON:     ";",
	COMMA:         ",",
	DOT:           ".",
	RARROW:        "->",
	FATARROW:      "=>",
	QUESTION:      "?",
	HASH:          "#",
	AT:            "@",
	LPAREN:        "(",
	RPAREN:        ")",
	LBRACE:        "{",
	RBRACE:        "}",
	LBRACKET:      "[",
	RBRACKET:      "]",
}

// String 返回词法类型的字面名称
func (k Kind) String() string {
	if name, ok := kindNames[k]; ok {
		return name
	}
	return fmt.Sprintf("Token(%d)", k)
}

// Keywords 规范关键字映射表
var Keywords = map[string]Kind{
	"and":      AND,
	"as":       AS,
	"break":    BREAK,
	"continue": CONTINUE,
	"elif":     ELIF,
	"else":     ELSE,
	"enum":     ENUM,
	"false":    FALSE,
	"fn":       FN,
	"for":      FOR,
	"if":       IF,
	"impl":     IMPL,
	"import":   IMPORT,
	"in":       IN,
	"let":      LET,
	"match":    MATCH,
	"mod":      MOD,
	"mut":      MUT,
	"not":      NOT_KW,
	"or":       OR,
	"pub":      PUB,
	"ref":      REF,
	"return":   RETURN,
	"scope":    SCOPE,
	"self":     SELF,
	"Self":     SELF_TYPE,
	"spawn":    SPAWN,
	"struct":   STRUCT,
	"trait":    TRAIT,
	"true":     TRUE,
	"type":     TYPE,
	"unsafe":   UNSAFE,
	"var":      VAR,
	"while":    WHILE,
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

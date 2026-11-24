// Package lexer 实现了 WON Phase 0B 源码词法分析器
package lexer

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/token"
)

// Lexer 词法分析器实例结构
type Lexer struct {
	file   string
	src    string
	runes  []rune
	pos    int // 当前在 runes 中的索引
	offset int // 当前字节偏移
	line   int // 当前行号（从 1 开始）
	col    int // 当前列号（从 1 开始）
	report *diagnostic.Report
}

// New 创建词法分析器
func New(file, src string, report *diagnostic.Report) *Lexer {
	return &Lexer{
		file:   file,
		src:    src,
		runes:  []rune(src),
		pos:    0,
		offset: 0,
		line:   1,
		col:    1,
		report: report,
	}
}

// currentPos 获取当前位置快照
func (l *Lexer) currentPos() diagnostic.Position {
	return diagnostic.Position{
		Line:   l.line,
		Column: l.col,
		Byte:   l.offset,
	}
}

// peek 查看当前字符，不移动游标
func (l *Lexer) peek() rune {
	if l.pos >= len(l.runes) {
		return 0
	}
	return l.runes[l.pos]
}

// peekNext 查看下一个字符
func (l *Lexer) peekNext() rune {
	if l.pos+1 >= len(l.runes) {
		return 0
	}
	return l.runes[l.pos+1]
}

// advance 消耗并返回当前字符，更新行列和字节偏移
func (l *Lexer) advance() rune {
	if l.pos >= len(l.runes) {
		return 0
	}
	r := l.runes[l.pos]
	l.pos++
	l.offset += len(string(r))
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

// skipWhitespaceAndComments 跳过空白字符及行内/块注释
func (l *Lexer) skipWhitespaceAndComments() {
	for {
		ch := l.peek()
		if ch == 0 {
			break
		}
		if unicode.IsSpace(ch) {
			l.advance()
			continue
		}
		if ch == '/' && l.peekNext() == '/' {
			// 单行注释
			l.advance()
			l.advance()
			for l.peek() != 0 && l.peek() != '\n' {
				l.advance()
			}
			continue
		}
		if ch == '/' && l.peekNext() == '*' {
			// 块注释
			l.advance()
			l.advance()
			for l.peek() != 0 {
				if l.peek() == '*' && l.peekNext() == '/' {
					l.advance()
					l.advance()
					break
				}
				l.advance()
			}
			continue
		}
		break
	}
}

// NextToken 提取下一个词法单元
func (l *Lexer) NextToken() token.Token {
	l.skipWhitespaceAndComments()

	start := l.currentPos()
	ch := l.peek()

	if ch == 0 {
		return token.Token{
			Kind:   token.EOF,
			Lexeme: "",
			Span:   diagnostic.Span{File: l.file, Start: start, End: start},
		}
	}

	// 预处理引入指令: #import <zlib.h> 或 #import "header.h"
	if ch == '#' {
		l.advance() // 消耗 '#'
		var kw strings.Builder
		for unicode.IsLetter(l.peek()) {
			kw.WriteRune(l.advance())
		}
		if kw.String() == "import" {
			l.skipWhitespaceAndComments()
			nextCh := l.peek()
			var pathSB strings.Builder
			if nextCh == '<' {
				pathSB.WriteRune(l.advance())
				for l.peek() != 0 && l.peek() != '\n' && l.peek() != '>' {
					pathSB.WriteRune(l.advance())
				}
				if l.peek() == '>' {
					pathSB.WriteRune(l.advance())
				}
			} else if nextCh == '"' {
				pathSB.WriteRune(l.advance())
				for l.peek() != 0 && l.peek() != '\n' && l.peek() != '"' {
					pathSB.WriteRune(l.advance())
				}
				if l.peek() == '"' {
					pathSB.WriteRune(l.advance())
				}
			}
			return token.Token{
				Kind:   token.IMPORT,
				Lexeme: pathSB.String(),
				Span:   diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
			}
		}
		return token.Token{
			Kind:   token.HASH,
			Lexeme: "#",
			Span:   diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
		}
	}

	// 标识符或关键字
	if ch == '_' || unicode.IsLetter(ch) {
		var sb strings.Builder
		for unicode.IsLetter(l.peek()) || unicode.IsDigit(l.peek()) || l.peek() == '_' {
			sb.WriteRune(l.advance())
		}
		lexeme := sb.String()
		kind := token.LookupIdent(lexeme)
		return token.Token{
			Kind:   kind,
			Lexeme: lexeme,
			Span:   diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
		}
	}

	// 数字字面量（十进制或十六进制）
	if unicode.IsDigit(ch) {
		var sb strings.Builder
		if ch == '0' && (l.peekNext() == 'x' || l.peekNext() == 'X') {
			sb.WriteRune(l.advance())
			sb.WriteRune(l.advance())
			for unicode.Is(unicode.Hex_Digit, l.peek()) || l.peek() == '_' {
				r := l.advance()
				if r != '_' {
					sb.WriteRune(r)
				}
			}
		} else {
			for unicode.IsDigit(l.peek()) || l.peek() == '_' {
				r := l.advance()
				if r != '_' {
					sb.WriteRune(r)
				}
			}
		}
		return token.Token{
			Kind:   token.INT,
			Lexeme: sb.String(),
			Span:   diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
		}
	}

	// 字符串字面量
	if ch == '"' {
		l.advance() // 消耗开头的引号
		var sb strings.Builder
		for {
			r := l.peek()
			if r == 0 || r == '\n' {
				l.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-LEX-001",
					Severity: "error",
					Message:  "未闭合的字符串字面量",
					File:     l.file,
					Span:     diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
				})
				break
			}
			if r == '"' {
				l.advance() // 消耗结束的引号
				break
			}
			if r == '\\' {
				l.advance()
				escaped := l.advance()
				switch escaped {
				case 'n':
					sb.WriteRune('\n')
				case 't':
					sb.WriteRune('\t')
				case 'r':
					sb.WriteRune('\r')
				case '\\':
					sb.WriteRune('\\')
				case '"':
					sb.WriteRune('"')
				case '0':
					sb.WriteRune(0)
				default:
					sb.WriteRune(escaped)
				}
				continue
			}
			sb.WriteRune(l.advance())
		}
		return token.Token{
			Kind:   token.STRING,
			Lexeme: sb.String(),
			Span:   diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
		}
	}

	// 符号处理
	l.advance() // 消耗首字符
	switch ch {
	case '+':
		return token.Token{Kind: token.PLUS, Lexeme: "+", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case '-':
		if l.peek() == '>' {
			l.advance()
			return token.Token{Kind: token.RARROW, Lexeme: "->", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
		}
		return token.Token{Kind: token.MINUS, Lexeme: "-", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case '*':
		return token.Token{Kind: token.STAR, Lexeme: "*", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case '/':
		return token.Token{Kind: token.SLASH, Lexeme: "/", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case '=':
		if l.peek() == '=' {
			l.advance()
			return token.Token{Kind: token.EQ, Lexeme: "==", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
		}
		return token.Token{Kind: token.ASSIGN, Lexeme: "=", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case '!':
		if l.peek() == '=' {
			l.advance()
			return token.Token{Kind: token.NOTEQ, Lexeme: "!=", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
		}
		return token.Token{Kind: token.NOT, Lexeme: "!", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case '<':
		if l.peek() == '=' {
			l.advance()
			return token.Token{Kind: token.LTEQ, Lexeme: "<=", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
		}
		return token.Token{Kind: token.LT, Lexeme: "<", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case '>':
		if l.peek() == '=' {
			l.advance()
			return token.Token{Kind: token.GTEQ, Lexeme: ">=", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
		}
		return token.Token{Kind: token.GT, Lexeme: ">", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case '&':
		return token.Token{Kind: token.AMP, Lexeme: "&", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case ':':
		if l.peek() == ':' {
			l.advance()
			return token.Token{Kind: token.COLONCOLON, Lexeme: "::", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
		}
		return token.Token{Kind: token.COLON, Lexeme: ":", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case ';':
		return token.Token{Kind: token.SEMICOLON, Lexeme: ";", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case ',':
		return token.Token{Kind: token.COMMA, Lexeme: ",", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case '.':
		return token.Token{Kind: token.DOT, Lexeme: ".", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case '(':
		return token.Token{Kind: token.LPAREN, Lexeme: "(", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case ')':
		return token.Token{Kind: token.RPAREN, Lexeme: ")", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case '{':
		return token.Token{Kind: token.LBRACE, Lexeme: "{", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case '}':
		return token.Token{Kind: token.RBRACE, Lexeme: "}", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case '[':
		return token.Token{Kind: token.LBRACKET, Lexeme: "[", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	case ']':
		return token.Token{Kind: token.RBRACKET, Lexeme: "]", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	default:
		l.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-LEX-002",
			Severity: "error",
			Message:  fmt.Sprintf("未识别的非法字符: %q", ch),
			File:     l.file,
			Span:     diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
		})
		return token.Token{
			Kind:   token.ILLEGAL,
			Lexeme: string(ch),
			Span:   diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
		}
	}
}

// ParseIntLiteral 将字符串形式的数字转换为 int64
func ParseIntLiteral(raw string) (int64, error) {
	clean := strings.ReplaceAll(raw, "_", "")
	if strings.HasPrefix(clean, "0x") || strings.HasPrefix(clean, "0X") {
		return strconv.ParseInt(clean[2:], 16, 64)
	}
	return strconv.ParseInt(clean, 10, 64)
}

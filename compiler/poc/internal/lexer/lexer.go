// Package lexer 实现了基于越位规则 (Off-side Rule) 的 WON 规范词法分析器
// 支持 4 空格缩进栈、合成 INDENT/DEDENT/NEWLINE 词法单元、多行界定符追踪与严格制表符拦截 (WON-SYN-001)。
package lexer

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/token"
)

// Lexer 规范缩进感知词法分析器
type Lexer struct {
	file        string
	src         string
	runes       []rune
	pos         int // 当前在 runes 中的索引
	offset      int // 当前 UTF-8 字节偏移量
	line        int // 当前行号（从 1 开始）
	col         int // 当前列号（从 1 开始）
	report      *diagnostic.Report
	indentStack []int         // 缩进深度栈，初始为 [0]
	pending     []token.Token // 待发出的多级 DEDENT 或挂起词法单元队列
	atLineStart bool          // 当前游标是否位于物理行的行首（尚未判定缩进）
	delimDepth  int           // 当前开启的 () / [] / {} 嵌套深度
}

// New 创建规范词法分析器实例
func New(file, src string, report *diagnostic.Report) *Lexer {
	return &Lexer{
		file:        file,
		src:         src,
		runes:       []rune(src),
		pos:         0,
		offset:      0,
		line:        1,
		col:         1,
		report:      report,
		indentStack: []int{0},
		pending:     make([]token.Token, 0),
		atLineStart: true,
		delimDepth:  0,
	}
}

// NewSubLexer 用于对字符串插值子片段进行独立词法分析并维持原有位置基准
func NewSubLexer(file, src string, base diagnostic.Position, report *diagnostic.Report) *Lexer {
	return &Lexer{
		file:        file,
		src:         src,
		runes:       []rune(src),
		pos:         0,
		offset:      base.Byte,
		line:        base.Line,
		col:         base.Column,
		report:      report,
		indentStack: []int{0},
		pending:     make([]token.Token, 0),
		atLineStart: false,
		delimDepth:  1, // 防止子片段内部产生不必要的块级缩进控制
	}
}

func (l *Lexer) currentPos() diagnostic.Position {
	return diagnostic.Position{
		Line:   l.line,
		Column: l.col,
		Byte:   l.offset,
	}
}

func (l *Lexer) peek() rune {
	if l.pos >= len(l.runes) {
		return 0
	}
	return l.runes[l.pos]
}

func (l *Lexer) peekAt(ahead int) rune {
	idx := l.pos + ahead
	if idx >= len(l.runes) || idx < 0 {
		return 0
	}
	return l.runes[idx]
}

func (l *Lexer) advance() rune {
	if l.pos >= len(l.runes) {
		return 0
	}
	r := l.runes[l.pos]
	l.pos++
	n := utf8.RuneLen(r)
	if n < 0 {
		n = 1
	}
	l.offset += n
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

func (l *Lexer) emitError(code, msg string, span diagnostic.Span) {
	if l.report != nil {
		l.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     code,
			Severity: "error",
			Message:  msg,
			File:     l.file,
			Span:     span,
		})
	}
}

// isCImportAtCursor 检查当前 '#' 是否为 #import 指令而非 # 注释
func (l *Lexer) isCImportAtCursor() bool {
	if l.peek() != '#' {
		return false
	}
	target := "import"
	for i, expected := range target {
		if l.peekAt(i+1) != expected {
			return false
		}
	}
	after := l.peekAt(len(target) + 1)
	return after == ' ' || after == '\t' || after == '<' || after == '"'
}

func (l *Lexer) skipLineComment() {
	for l.peek() != 0 && l.peek() != '\n' {
		l.advance()
	}
}

func (l *Lexer) skipBlockComment(startPos diagnostic.Position) {
	// 消费开头的 "/*"
	l.advance()
	l.advance()
	depth := 1
	for depth > 0 && l.peek() != 0 {
		if l.peek() == '/' && l.peekAt(1) == '*' {
			l.advance()
			l.advance()
			depth++
			continue
		}
		if l.peek() == '*' && l.peekAt(1) == '/' {
			l.advance()
			l.advance()
			depth--
			continue
		}
		l.advance()
	}
	if depth > 0 {
		l.emitError("WON-LEX-003", "未闭合的块注释 /* ... */", diagnostic.Span{
			File:  l.file,
			Start: startPos,
			End:   l.currentPos(),
		})
	}
}

// processLineStartIndentation 处理行首空白、忽略空行/纯注释行，并计算合成 INDENT/DEDENT 词法单元
// 若需要直接返回挂起的词法单元（如 INDENT/DEDENT/EOF），则返回 (tok, true)
func (l *Lexer) processLineStartIndentation() (token.Token, bool) {
	for {
		lineStartPos := l.currentPos()
		spaces := 0
		tabReported := false

		for {
			ch := l.peek()
			if ch == ' ' {
				spaces++
				l.advance()
			} else if ch == '\t' {
				tabStart := l.currentPos()
				l.advance()
				if !tabReported {
					tabReported = true
					l.emitError("WON-SYN-001", "前导缩进中严禁使用制表符 (Tab)，请统一使用 4 个空格进行缩进", diagnostic.Span{
						File:  l.file,
						Start: tabStart,
						End:   l.currentPos(),
					})
				}
				spaces += 4 // 容错推进以防止引发连环错位报告
			} else {
				break
			}
		}

		ch := l.peek()

		// 处理空白行 (\r\n 或 \n)
		if ch == '\r' && l.peekAt(1) == '\n' {
			l.advance()
			l.advance()
			continue
		}
		if ch == '\n' {
			l.advance()
			continue
		}

		// 处理纯行注释 (# 或 //，排除 #import 指令)
		if (ch == '#' && !l.isCImportAtCursor()) || (ch == '/' && l.peekAt(1) == '/') {
			l.skipLineComment()
			if l.peek() == '\r' && l.peekAt(1) == '\n' {
				l.advance()
			}
			if l.peek() == '\n' {
				l.advance()
			}
			continue
		}

		// 处理行首块注释：若注释后至行尾无代码，视为空注释行跳过
		if ch == '/' && l.peekAt(1) == '*' {
			l.skipBlockComment(l.currentPos())
			// 跳过注释后的行内水平空白
			for l.peek() == ' ' || l.peek() == '\t' {
				l.advance()
			}
			next := l.peek()
			if next == '\n' || (next == '\r' && l.peekAt(1) == '\n') || (next == '#' && !l.isCImportAtCursor()) || (next == '/' && l.peekAt(1) == '/') {
				if next == '#' || next == '/' {
					l.skipLineComment()
				}
				if l.peek() == '\r' && l.peekAt(1) == '\n' {
					l.advance()
				}
				if l.peek() == '\n' {
					l.advance()
				}
				continue
			}
			if next == 0 {
				ch = 0
			}
		}

		// 到达文件末尾 (EOF)：释放缩进栈中的所有未闭合作用域，发出对应数量 DEDENT
		if ch == 0 {
			eofPos := l.currentPos()
			for len(l.indentStack) > 1 {
				l.indentStack = l.indentStack[:len(l.indentStack)-1]
				l.pending = append(l.pending, token.Token{
					Kind:   token.DEDENT,
					Lexeme: "",
					Span:   diagnostic.Span{File: l.file, Start: eofPos, End: eofPos},
				})
			}
			l.pending = append(l.pending, token.Token{
				Kind:   token.EOF,
				Lexeme: "",
				Span:   diagnostic.Span{File: l.file, Start: eofPos, End: eofPos},
			})
			tok := l.pending[0]
			l.pending = l.pending[1:]
			return tok, true
		}

		// 真正有效代码行的起始点
		l.atLineStart = false
		indentEndPos := l.currentPos()
		top := l.indentStack[len(l.indentStack)-1]

		if spaces == top {
			return token.Token{}, false
		}

		if spaces > top {
			if (spaces-top)%4 != 0 || spaces%4 != 0 {
				l.emitError("WON-SYN-002", fmt.Sprintf("缩进级别必须为 4 个空格的整数倍 (当前行首空格数: %d，外层基准: %d)", spaces, top), diagnostic.Span{
					File:  l.file,
					Start: lineStartPos,
					End:   indentEndPos,
				})
			}
			l.indentStack = append(l.indentStack, spaces)
			return token.Token{
				Kind:   token.INDENT,
				Lexeme: "",
				Span:   diagnostic.Span{File: l.file, Start: lineStartPos, End: indentEndPos},
			}, true
		}

		// spaces < top: 弹出栈顶直至匹配外层缩进
		for len(l.indentStack) > 1 && l.indentStack[len(l.indentStack)-1] > spaces {
			l.indentStack = l.indentStack[:len(l.indentStack)-1]
			l.pending = append(l.pending, token.Token{
				Kind:   token.DEDENT,
				Lexeme: "",
				Span:   diagnostic.Span{File: l.file, Start: lineStartPos, End: indentEndPos},
			})
		}

		if l.indentStack[len(l.indentStack)-1] != spaces {
			l.emitError("WON-SYN-003", fmt.Sprintf("缩进回退列数 (%d) 与任何外层有效缩进基准不一致", spaces), diagnostic.Span{
				File:  l.file,
				Start: lineStartPos,
				End:   indentEndPos,
			})
		}

		if len(l.pending) > 0 {
			tok := l.pending[0]
			l.pending = l.pending[1:]
			return tok, true
		}

		return token.Token{}, false
	}
}

// NextToken 提取下一个规范词法单元
func (l *Lexer) NextToken() token.Token {
	if len(l.pending) > 0 {
		tok := l.pending[0]
		l.pending = l.pending[1:]
		return tok
	}

	if l.atLineStart && l.delimDepth == 0 {
		if tok, hasTok := l.processLineStartIndentation(); hasTok {
			return tok
		}
	}

	// 跳过词元之间的水平空白、显式续行符 '\'、以及多行界定符内的换行符
	for {
		ch := l.peek()
		if ch == 0 {
			break
		}
		if ch == ' ' || ch == '\t' {
			l.advance()
			continue
		}
		if ch == '\r' && l.peekAt(1) == '\n' {
			if l.delimDepth > 0 {
				l.advance()
				l.advance()
				continue
			}
			start := l.currentPos()
			l.advance()
			l.advance()
			l.atLineStart = true
			return token.Token{
				Kind:   token.NEWLINE,
				Lexeme: "\n",
				Span:   diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
			}
		}
		if ch == '\n' {
			if l.delimDepth > 0 {
				l.advance()
				continue
			}
			start := l.currentPos()
			l.advance()
			l.atLineStart = true
			return token.Token{
				Kind:   token.NEWLINE,
				Lexeme: "\n",
				Span:   diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
			}
		}
		// 显式行续接 '\'
		if ch == '\\' {
			look := 1
			for l.peekAt(look) == ' ' || l.peekAt(look) == '\t' {
				look++
			}
			if l.peekAt(look) == '\r' && l.peekAt(look+1) == '\n' {
				for i := 0; i < look+2; i++ {
					l.advance()
				}
				continue
			}
			if l.peekAt(look) == '\n' {
				for i := 0; i < look+1; i++ {
					l.advance()
				}
				continue
			}
		}
		// 行尾注释 (# 或 //)
		if ch == '#' && !l.isCImportAtCursor() {
			l.skipLineComment()
			continue
		}
		if ch == '/' && l.peekAt(1) == '/' {
			l.skipLineComment()
			continue
		}
		if ch == '/' && l.peekAt(1) == '*' {
			l.skipBlockComment(l.currentPos())
			continue
		}
		break
	}

	start := l.currentPos()
	ch := l.peek()

	// 文件尾处理：若当前处于有效非空逻辑行结尾，先补发一个合成 NEWLINE，随后触发缩进清空
	if ch == 0 {
		if !l.atLineStart && l.delimDepth == 0 {
			l.atLineStart = true
			return token.Token{
				Kind:   token.NEWLINE,
				Lexeme: "",
				Span:   diagnostic.Span{File: l.file, Start: start, End: start},
			}
		}
		for len(l.indentStack) > 1 {
			l.indentStack = l.indentStack[:len(l.indentStack)-1]
			l.pending = append(l.pending, token.Token{
				Kind:   token.DEDENT,
				Lexeme: "",
				Span:   diagnostic.Span{File: l.file, Start: start, End: start},
			})
		}
		if len(l.pending) > 0 {
			l.pending = append(l.pending, token.Token{
				Kind:   token.EOF,
				Lexeme: "",
				Span:   diagnostic.Span{File: l.file, Start: start, End: start},
			})
			tok := l.pending[0]
			l.pending = l.pending[1:]
			return tok
		}
		return token.Token{
			Kind:   token.EOF,
			Lexeme: "",
			Span:   diagnostic.Span{File: l.file, Start: start, End: start},
		}
	}

	// #import 头文件引入指令
	if ch == '#' && l.isCImportAtCursor() {
		l.advance() // 消耗 '#'
		for unicode.IsLetter(l.peek()) {
			l.advance() // 消耗 "import"
		}
		for l.peek() == ' ' || l.peek() == '\t' {
			l.advance()
		}
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
			Kind:   token.CIMPORT,
			Lexeme: pathSB.String(),
			Span:   diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
		}
	}

	// 原始字符串字面量 r"..."
	if ch == 'r' && l.peekAt(1) == '"' {
		return l.scanRawString(start)
	}

	// 字节字面量 b'a'
	if ch == 'b' && l.peekAt(1) == '\'' {
		return l.scanByteLiteral(start)
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

	// 数字字面量（整型：十/十六/八/二进制，浮点型，支持 _ 分隔符与显式类型后缀）
	if unicode.IsDigit(ch) {
		return l.scanNumber(start)
	}

	// 字符字面量 'a'
	if ch == '\'' {
		return l.scanCharLiteral(start)
	}

	// 字符串字面量（支持普通与 {expr} 插值格式验证）
	if ch == '"' {
		return l.scanString(start)
	}

	// 运算符与界定符
	l.advance() // 消耗首字符
	switch ch {
	case '+':
		if l.peek() == '=' {
			l.advance()
			return l.makeTok(token.PLUSEQ, "+=", start)
		}
		return l.makeTok(token.PLUS, "+", start)
	case '-':
		if l.peek() == '>' {
			l.advance()
			return l.makeTok(token.RARROW, "->", start)
		}
		if l.peek() == '=' {
			l.advance()
			return l.makeTok(token.MINUSEQ, "-=", start)
		}
		return l.makeTok(token.MINUS, "-", start)
	case '*':
		if l.peek() == '=' {
			l.advance()
			return l.makeTok(token.STAREQ, "*=", start)
		}
		return l.makeTok(token.STAR, "*", start)
	case '/':
		if l.peek() == '=' {
			l.advance()
			return l.makeTok(token.SLASHEQ, "/=", start)
		}
		return l.makeTok(token.SLASH, "/", start)
	case '%':
		if l.peek() == '=' {
			l.advance()
			return l.makeTok(token.PERCENTEQ, "%=", start)
		}
		return l.makeTok(token.PERCENT, "%", start)
	case '=':
		if l.peek() == '=' {
			l.advance()
			return l.makeTok(token.EQ, "==", start)
		}
		if l.peek() == '>' {
			l.advance()
			return l.makeTok(token.FATARROW, "=>", start)
		}
		return l.makeTok(token.ASSIGN, "=", start)
	case '!':
		if l.peek() == '=' {
			l.advance()
			return l.makeTok(token.NOTEQ, "!=", start)
		}
		return l.makeTok(token.NOT, "!", start)
	case '~':
		return l.makeTok(token.TILDE, "~", start)
	case '<':
		if l.peek() == '<' {
			l.advance()
			if l.peek() == '=' {
				l.advance()
				return l.makeTok(token.LSHIFTEQ, "<<=", start)
			}
			return l.makeTok(token.LSHIFT, "<<", start)
		}
		if l.peek() == '=' {
			l.advance()
			return l.makeTok(token.LTEQ, "<=", start)
		}
		return l.makeTok(token.LT, "<", start)
	case '>':
		if l.peek() == '>' {
			l.advance()
			if l.peek() == '=' {
				l.advance()
				return l.makeTok(token.RSHIFTEQ, ">>=", start)
			}
			return l.makeTok(token.RSHIFT, ">>", start)
		}
		if l.peek() == '=' {
			l.advance()
			return l.makeTok(token.GTEQ, ">=", start)
		}
		return l.makeTok(token.GT, ">", start)
	case '&':
		if l.peek() == '=' {
			l.advance()
			return l.makeTok(token.AMPEQ, "&=", start)
		}
		return l.makeTok(token.AMP, "&", start)
	case '|':
		if l.peek() == '=' {
			l.advance()
			return l.makeTok(token.PIPEEQ, "|=", start)
		}
		return l.makeTok(token.PIPE, "|", start)
	case '^':
		if l.peek() == '=' {
			l.advance()
			return l.makeTok(token.CARETEQ, "^=", start)
		}
		return l.makeTok(token.CARET, "^", start)
	case ':':
		if l.peek() == ':' {
			l.advance()
			return l.makeTok(token.COLONCOLON, "::", start)
		}
		return l.makeTok(token.COLON, ":", start)
	case ';':
		return l.makeTok(token.SEMICOLON, ";", start)
	case ',':
		return l.makeTok(token.COMMA, ",", start)
	case '.':
		if l.peek() == '.' {
			l.advance()
			if l.peek() == '=' {
				l.advance()
				return l.makeTok(token.DOTDOTEQ, "..=", start)
			}
			return l.makeTok(token.DOTDOT, "..", start)
		}
		return l.makeTok(token.DOT, ".", start)
	case '?':
		return l.makeTok(token.QUESTION, "?", start)
	case '#':
		return l.makeTok(token.HASH, "#", start)
	case '(':
		l.delimDepth++
		return l.makeTok(token.LPAREN, "(", start)
	case ')':
		if l.delimDepth > 0 {
			l.delimDepth--
		}
		return l.makeTok(token.RPAREN, ")", start)
	case '{':
		l.delimDepth++
		return l.makeTok(token.LBRACE, "{", start)
	case '}':
		if l.delimDepth > 0 {
			l.delimDepth--
		}
		return l.makeTok(token.RBRACE, "}", start)
	case '[':
		l.delimDepth++
		return l.makeTok(token.LBRACKET, "[", start)
	case ']':
		if l.delimDepth > 0 {
			l.delimDepth--
		}
		return l.makeTok(token.RBRACKET, "]", start)
	default:
		l.emitError("WON-LEX-002", fmt.Sprintf("未识别的非法字符: %q", ch), diagnostic.Span{
			File:  l.file,
			Start: start,
			End:   l.currentPos(),
		})
		return l.makeTok(token.ILLEGAL, string(ch), start)
	}
}

func (l *Lexer) makeTok(kind token.Kind, lexeme string, start diagnostic.Position) token.Token {
	return token.Token{
		Kind:   kind,
		Lexeme: lexeme,
		Span:   diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
	}
}

var validIntSuffixes = map[string]bool{
	"i8": true, "i16": true, "i32": true, "i64": true, "i128": true, "isize": true,
	"u8": true, "u16": true, "u32": true, "u64": true, "u128": true, "usize": true,
}

var validFloatSuffixes = map[string]bool{
	"f32": true, "f64": true,
}

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func isOctDigit(r rune) bool {
	return r >= '0' && r <= '7'
}

func isBinDigit(r rune) bool {
	return r == '0' || r == '1'
}

func (l *Lexer) scanNumber(start diagnostic.Position) token.Token {
	var sb strings.Builder
	ch := l.peek()
	isFloat := false

	if ch == '0' && (l.peekAt(1) == 'x' || l.peekAt(1) == 'X') {
		sb.WriteRune(l.advance())
		sb.WriteRune(l.advance())
		digits := 0
		for isHexDigit(l.peek()) || l.peek() == '_' {
			r := l.advance()
			sb.WriteRune(r)
			if r != '_' {
				digits++
			}
		}
		if digits == 0 {
			l.emitError("WON-SYN-008", "非法的十六进制整数字面量 (缺少数字)", diagnostic.Span{
				File: l.file, Start: start, End: l.currentPos(),
			})
		}
	} else if ch == '0' && (l.peekAt(1) == 'o' || l.peekAt(1) == 'O') {
		sb.WriteRune(l.advance())
		sb.WriteRune(l.advance())
		digits := 0
		for isOctDigit(l.peek()) || l.peek() == '_' {
			r := l.advance()
			sb.WriteRune(r)
			if r != '_' {
				digits++
			}
		}
		if digits == 0 || unicode.IsDigit(l.peek()) {
			for unicode.IsDigit(l.peek()) {
				sb.WriteRune(l.advance())
			}
			l.emitError("WON-SYN-008", "非法的八进制整数字面量", diagnostic.Span{
				File: l.file, Start: start, End: l.currentPos(),
			})
		}
	} else if ch == '0' && (l.peekAt(1) == 'b' || l.peekAt(1) == 'B') {
		sb.WriteRune(l.advance())
		sb.WriteRune(l.advance())
		digits := 0
		for isBinDigit(l.peek()) || l.peek() == '_' {
			r := l.advance()
			sb.WriteRune(r)
			if r != '_' {
				digits++
			}
		}
		if digits == 0 || unicode.IsDigit(l.peek()) {
			for unicode.IsDigit(l.peek()) {
				sb.WriteRune(l.advance())
			}
			l.emitError("WON-SYN-008", "非法的二进制整数字面量", diagnostic.Span{
				File: l.file, Start: start, End: l.currentPos(),
			})
		}
	} else {
		for unicode.IsDigit(l.peek()) || l.peek() == '_' {
			sb.WriteRune(l.advance())
		}
		// 仅当 '.' 后面紧跟数字时视为小数点，确保 "0..10" 与 "1.len()" 不受冲突
		if l.peek() == '.' && unicode.IsDigit(l.peekAt(1)) {
			isFloat = true
			sb.WriteRune(l.advance()) // '.'
			for unicode.IsDigit(l.peek()) || l.peek() == '_' {
				sb.WriteRune(l.advance())
			}
		}
		// 科学计数法指数部分
		if l.peek() == 'e' || l.peek() == 'E' {
			isFloat = true
			sb.WriteRune(l.advance())
			if l.peek() == '+' || l.peek() == '-' {
				sb.WriteRune(l.advance())
			}
			expDigits := 0
			for unicode.IsDigit(l.peek()) || l.peek() == '_' {
				r := l.advance()
				sb.WriteRune(r)
				if r != '_' {
					expDigits++
				}
			}
			if expDigits == 0 {
				l.emitError("WON-SYN-008", "非法的浮点字面量 (指数部分缺少数字)", diagnostic.Span{
					File: l.file, Start: start, End: l.currentPos(),
				})
			}
		}
	}

	// 读取可选类型后缀
	if unicode.IsLetter(l.peek()) || l.peek() == '_' {
		var suffixSB strings.Builder
		suffixStart := l.currentPos()
		for unicode.IsLetter(l.peek()) || unicode.IsDigit(l.peek()) || l.peek() == '_' {
			suffixSB.WriteRune(l.advance())
		}
		suffix := suffixSB.String()
		if isFloat {
			if !validFloatSuffixes[suffix] {
				l.emitError("WON-SYN-008", fmt.Sprintf("非法的浮点数类型后缀: %q", suffix), diagnostic.Span{
					File: l.file, Start: suffixStart, End: l.currentPos(),
				})
			}
		} else {
			if validFloatSuffixes[suffix] {
				isFloat = true
			} else if !validIntSuffixes[suffix] {
				l.emitError("WON-SYN-008", fmt.Sprintf("非法的整数类型后缀: %q", suffix), diagnostic.Span{
					File: l.file, Start: suffixStart, End: l.currentPos(),
				})
			}
		}
		sb.WriteString(suffix)
	}

	kind := token.INT
	if isFloat {
		kind = token.FLOAT
	}
	return token.Token{
		Kind:   kind,
		Lexeme: sb.String(),
		Span:   diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
	}
}

func (l *Lexer) scanEscapeSeq(start diagnostic.Position) (rune, bool) {
	l.advance() // 消耗 '\'
	escaped := l.advance()
	switch escaped {
	case 'n':
		return '\n', true
	case 't':
		return '\t', true
	case 'r':
		return '\r', true
	case '\\':
		return '\\', true
	case '"':
		return '"', true
	case '\'':
		return '\'', true
	case '0':
		return 0, true
	case '{':
		return '{', true
	case '}':
		return '}', true
	case 'x':
		h1, h2 := l.peek(), l.peekAt(1)
		if !isHexDigit(h1) || !isHexDigit(h2) {
			l.emitError("WON-SYN-008", "非法的十六进制转义序列 (\\xHH)", diagnostic.Span{
				File: l.file, Start: start, End: l.currentPos(),
			})
			return 0, false
		}
		l.advance()
		l.advance()
		val, _ := strconv.ParseInt(string([]rune{h1, h2}), 16, 32)
		return rune(val), true
	case 'u':
		if l.peek() != '{' {
			l.emitError("WON-SYN-008", "Unicode 转义必须采用 \\u{...} 格式", diagnostic.Span{
				File: l.file, Start: start, End: l.currentPos(),
			})
			return 0, false
		}
		l.advance() // '{'
		var hexSB strings.Builder
		for isHexDigit(l.peek()) {
			hexSB.WriteRune(l.advance())
		}
		if l.peek() != '}' || hexSB.Len() == 0 {
			l.emitError("WON-SYN-008", "非法的 Unicode 转义序列", diagnostic.Span{
				File: l.file, Start: start, End: l.currentPos(),
			})
			if l.peek() == '}' {
				l.advance()
			}
			return 0, false
		}
		l.advance() // '}'
		val, err := strconv.ParseInt(hexSB.String(), 16, 32)
		if err != nil || !utf8.ValidRune(rune(val)) {
			l.emitError("WON-SYN-008", "无效的 Unicode 标量值", diagnostic.Span{
				File: l.file, Start: start, End: l.currentPos(),
			})
			return 0, false
		}
		return rune(val), true
	default:
		l.emitError("WON-SYN-008", fmt.Sprintf("未知的转义字符: \\%c", escaped), diagnostic.Span{
			File: l.file, Start: start, End: l.currentPos(),
		})
		return escaped, false
	}
}

func (l *Lexer) scanRawString(start diagnostic.Position) token.Token {
	l.advance() // 'r'
	l.advance() // '"'
	var sb strings.Builder
	for {
		r := l.peek()
		if r == 0 {
			l.emitError("WON-LEX-001", "未闭合的原始字符串字面量", diagnostic.Span{
				File: l.file, Start: start, End: l.currentPos(),
			})
			break
		}
		if r == '"' {
			l.advance()
			break
		}
		sb.WriteRune(l.advance())
	}
	return token.Token{
		Kind:   token.RAW_STRING,
		Lexeme: sb.String(),
		Span:   diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
	}
}

func (l *Lexer) scanCharLiteral(start diagnostic.Position) token.Token {
	l.advance() // '\''
	var val rune
	if l.peek() == 0 || l.peek() == '\n' {
		l.emitError("WON-SYN-008", "未闭合的字符字面量", diagnostic.Span{
			File: l.file, Start: start, End: l.currentPos(),
		})
		return token.Token{Kind: token.CHAR, Lexeme: "", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	}
	if l.peek() == '\\' {
		val, _ = l.scanEscapeSeq(start)
	} else {
		val = l.advance()
	}
	if l.peek() != '\'' {
		l.emitError("WON-SYN-008", "字符字面量缺少闭合单引号 '\\''", diagnostic.Span{
			File: l.file, Start: start, End: l.currentPos(),
		})
		for l.peek() != 0 && l.peek() != '\n' && l.peek() != '\'' {
			l.advance()
		}
	}
	if l.peek() == '\'' {
		l.advance()
	}
	return token.Token{
		Kind:   token.CHAR,
		Lexeme: string(val),
		Span:   diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
	}
}

func (l *Lexer) scanByteLiteral(start diagnostic.Position) token.Token {
	l.advance() // 'b'
	l.advance() // '\''
	var val rune
	if l.peek() == 0 || l.peek() == '\n' {
		l.emitError("WON-SYN-008", "未闭合的字节字面量", diagnostic.Span{
			File: l.file, Start: start, End: l.currentPos(),
		})
		return token.Token{Kind: token.BYTE, Lexeme: "", Span: diagnostic.Span{File: l.file, Start: start, End: l.currentPos()}}
	}
	if l.peek() == '\\' {
		val, _ = l.scanEscapeSeq(start)
	} else {
		val = l.advance()
		if val > 0x7E || val < 0x20 {
			l.emitError("WON-SYN-008", "字节字面量仅允许可打印 ASCII 字符 (0x20..0x7E)", diagnostic.Span{
				File: l.file, Start: start, End: l.currentPos(),
			})
		}
	}
	if l.peek() != '\'' {
		l.emitError("WON-SYN-008", "字节字面量缺少闭合单引号 '\\''", diagnostic.Span{
			File: l.file, Start: start, End: l.currentPos(),
		})
		for l.peek() != 0 && l.peek() != '\n' && l.peek() != '\'' {
			l.advance()
		}
	}
	if l.peek() == '\'' {
		l.advance()
	}
	return token.Token{
		Kind:   token.BYTE,
		Lexeme: string(byte(val)),
		Span:   diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
	}
}

func (l *Lexer) scanString(start diagnostic.Position) token.Token {
	l.advance() // 消耗开头的 '"'
	var sb strings.Builder
	var rawInterpSB strings.Builder
	hasInterpolation := false

	for {
		r := l.peek()
		if r == 0 || r == '\n' {
			l.emitError("WON-LEX-001", "未闭合的字符串字面量", diagnostic.Span{
				File: l.file, Start: start, End: l.currentPos(),
			})
			break
		}
		if r == '"' {
			l.advance()
			break
		}
		if r == '\\' {
			escPos := l.currentPos()
			chVal, _ := l.scanEscapeSeq(escPos)
			if chVal == '{' {
				sb.WriteRune('{')
				rawInterpSB.WriteString(`\{`)
			} else if chVal == '}' {
				sb.WriteRune('}')
				rawInterpSB.WriteString(`\}`)
			} else {
				sb.WriteRune(chVal)
				rawInterpSB.WriteRune(chVal)
			}
			continue
		}
		if r == '{' {
			hasInterpolation = true
			interpStart := l.currentPos()
			rawInterpSB.WriteRune(l.advance()) // '{'
			braceDepth := 1
			inInnerStr := false
			for braceDepth > 0 {
				inner := l.peek()
				if inner == 0 || inner == '\n' {
					l.emitError("WON-SYN-007", "字符串插值分隔符 '{' 未正确闭合", diagnostic.Span{
						File: l.file, Start: interpStart, End: l.currentPos(),
					})
					break
				}
				if inner == '"' {
					inInnerStr = !inInnerStr
					rawInterpSB.WriteRune(l.advance())
					continue
				}
				if !inInnerStr {
					if inner == '{' {
						braceDepth++
					} else if inner == '}' {
						braceDepth--
						rawInterpSB.WriteRune(l.advance())
						if braceDepth == 0 {
							break
						}
						continue
					}
				} else if inner == '\\' {
					rawInterpSB.WriteRune(l.advance())
					if l.peek() != 0 {
						rawInterpSB.WriteRune(l.advance())
					}
					continue
				}
				rawInterpSB.WriteRune(l.advance())
			}
			continue
		}
		if r == '}' {
			errStart := l.currentPos()
			l.advance()
			l.emitError("WON-SYN-007", "字符串中出现未匹配的孤立插值闭合符 '}' (如需字面量请使用 \\})", diagnostic.Span{
				File: l.file, Start: errStart, End: l.currentPos(),
			})
			continue
		}
		ch := l.advance()
		sb.WriteRune(ch)
		rawInterpSB.WriteRune(ch)
	}

	if hasInterpolation {
		return token.Token{
			Kind:   token.INTERP_STRING,
			Lexeme: rawInterpSB.String(),
			Span:   diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
		}
	}
	return token.Token{
		Kind:   token.STRING,
		Lexeme: sb.String(),
		Span:   diagnostic.Span{File: l.file, Start: start, End: l.currentPos()},
	}
}

// ParseIntLiteral 将带有进制前缀与类型后缀的字符串数字转换为数值与类型后缀
func ParseIntLiteral(raw string) (int64, error) {
	val, _, err := ParseIntLiteralWithSuffix(raw)
	return val, err
}

// ParseIntLiteralWithSuffix 提取整数字面量值与后缀
func ParseIntLiteralWithSuffix(raw string) (int64, string, error) {
	clean := strings.ReplaceAll(raw, "_", "")
	suffix := ""
	// 检查已知后缀，优先匹配长后缀
	orderedSuffixes := []string{"isize", "usize", "i128", "u128", "i16", "u16", "i32", "u32", "i64", "u64", "i8", "u8"}
	for _, s := range orderedSuffixes {
		if strings.HasSuffix(clean, s) {
			suffix = s
			clean = strings.TrimSuffix(clean, s)
			break
		}
	}

	if strings.HasPrefix(clean, "0x") || strings.HasPrefix(clean, "0X") {
		val, err := strconv.ParseInt(clean[2:], 16, 64)
		return val, suffix, err
	}
	if strings.HasPrefix(clean, "0o") || strings.HasPrefix(clean, "0O") {
		val, err := strconv.ParseInt(clean[2:], 8, 64)
		return val, suffix, err
	}
	if strings.HasPrefix(clean, "0b") || strings.HasPrefix(clean, "0B") {
		val, err := strconv.ParseInt(clean[2:], 2, 64)
		return val, suffix, err
	}
	val, err := strconv.ParseInt(clean, 10, 64)
	return val, suffix, err
}

// ParseFloatLiteralWithSuffix 提取浮点字面量值与后缀
func ParseFloatLiteralWithSuffix(raw string) (float64, string, error) {
	clean := strings.ReplaceAll(raw, "_", "")
	suffix := ""
	for _, s := range []string{"f32", "f64"} {
		if strings.HasSuffix(clean, s) {
			suffix = s
			clean = strings.TrimSuffix(clean, s)
			break
		}
	}
	val, err := strconv.ParseFloat(clean, 64)
	return val, suffix, err
}

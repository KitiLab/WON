// Package parser 实现了 WON Phase 0B 递归下降语法解析器
package parser

import (
	"fmt"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/token"
)

// Parser 递归下降语法解析器实例
type Parser struct {
	lex     *lexer.Lexer
	cur     token.Token
	peek    token.Token
	report  *diagnostic.Report
	hadErr  bool
	srcFile string
}

// New 创建新的语法解析器
func New(lex *lexer.Lexer, report *diagnostic.Report, srcFile string) *Parser {
	p := &Parser{
		lex:     lex,
		report:  report,
		srcFile: srcFile,
	}
	p.advance()
	p.advance()
	return p
}

func (p *Parser) advance() {
	p.cur = p.peek
	p.peek = p.lex.NextToken()
}

func (p *Parser) curIs(k token.Kind) bool {
	return p.cur.Kind == k
}

func (p *Parser) peekIs(k token.Kind) bool {
	return p.peek.Kind == k
}

func (p *Parser) expect(k token.Kind, msg string) bool {
	if p.curIs(k) {
		p.advance()
		return true
	}
	p.emitError("WON-SYN-001", fmt.Sprintf("%s (实际遇到 %s: %q)", msg, p.cur.Kind, p.cur.Lexeme), p.cur.Span)
	return false
}

func (p *Parser) emitError(code, message string, span diagnostic.Span) {
	p.hadErr = true
	p.report.AddDiagnostic(diagnostic.Diagnostic{
		Code:     code,
		Severity: "error",
		Message:  message,
		File:     p.srcFile,
		Span:     span,
	})
}

// ParseProgram 解析整个 WON 顶层程序
func (p *Parser) ParseProgram() *ast.Program {
	prog := &ast.Program{
		Imports:   make([]*ast.ImportDecl, 0),
		Functions: make([]*ast.FunctionDecl, 0),
		Span:      diagnostic.Span{File: p.srcFile, Start: p.cur.Span.Start},
	}

	for !p.curIs(token.EOF) {
		if p.curIs(token.IMPORT) {
			imp := &ast.ImportDecl{
				HeaderPath: p.cur.Lexeme,
				Span:       p.cur.Span,
			}
			prog.Imports = append(prog.Imports, imp)
			p.advance()
			continue
		}

		if p.curIs(token.FN) {
			fn := p.parseFunction()
			if fn != nil {
				prog.Functions = append(prog.Functions, fn)
			}
			continue
		}

		// 遇到未知顶层记号，报错并跳过
		p.emitError("WON-SYN-002", fmt.Sprintf("未知的顶层声明关键字: %q", p.cur.Lexeme), p.cur.Span)
		p.advance()
	}

	prog.Span.End = p.cur.Span.End
	return prog
}

// parseFunction 解析 fn name(params) -> RetType { ... }
func (p *Parser) parseFunction() *ast.FunctionDecl {
	fnSpan := p.cur.Span
	p.advance() // 消耗 'fn'

	if !p.curIs(token.IDENT) {
		p.emitError("WON-SYN-005", "函数声明缺少函数名称标识符", p.cur.Span)
		return nil
	}
	fnName := p.cur.Lexeme
	p.advance()

	if !p.expect(token.LPAREN, "函数名后期望跟随形参列表 '('") {
		return nil
	}

	params := make([]*ast.Param, 0)
	for !p.curIs(token.RPAREN) && !p.curIs(token.EOF) {
		if !p.curIs(token.IDENT) {
			p.emitError("WON-SYN-006", "形参期望标识符名称", p.cur.Span)
			break
		}
		paramName := p.cur.Lexeme
		paramSpan := p.cur.Span
		p.advance()

		if !p.expect(token.COLON, "形参名后期望类型分隔符 ':'") {
			break
		}

		paramType := p.parseTypeString()
		params = append(params, &ast.Param{
			Name: paramName,
			Type: paramType,
			Span: diagnostic.Span{File: p.srcFile, Start: paramSpan.Start, End: p.cur.Span.End},
		})

		if p.curIs(token.COMMA) {
			p.advance()
		} else {
			break
		}
	}

	if !p.expect(token.RPAREN, "形参列表缺少闭合括号 ')'") {
		return nil
	}

	retType := "void"
	if p.curIs(token.RARROW) {
		p.advance() // 消耗 '->'
		retType = p.parseTypeString()
	}

	if !p.curIs(token.LBRACE) {
		p.emitError("WON-SYN-007", "函数体必须以花括号 '{' 开始", p.cur.Span)
		return nil
	}

	body := p.parseBlock()
	return &ast.FunctionDecl{
		Name:       fnName,
		Params:     params,
		ReturnType: retType,
		Body:       body,
		Span:       diagnostic.Span{File: p.srcFile, Start: fnSpan.Start, End: body.Span.End},
	}
}

// parseTypeString 解析基本类型字符串，如 i32, u64, [u8; 256], *mut u8
func (p *Parser) parseTypeString() string {
	if p.curIs(token.LBRACKET) {
		p.advance() // 消耗 '['
		elemType := p.parseTypeString()
		p.expect(token.SEMICOLON, "定长数组类型期望跟随 ';'")
		size := p.cur.Lexeme
		p.expect(token.INT, "定长数组大小期望为整数常量")
		p.expect(token.RBRACKET, "定长数组类型缺少闭合 ']'")
		return fmt.Sprintf("[%s; %s]", elemType, size)
	}

	if p.curIs(token.IDENT) {
		t := p.cur.Lexeme
		p.advance()
		return t
	}

	// 容错处理
	t := p.cur.Lexeme
	p.advance()
	return t
}

// parseBlock 解析代码块 { stmts... }
func (p *Parser) parseBlock() *ast.BlockStmt {
	startSpan := p.cur.Span
	p.advance() // 消耗 '{'

	stmts := make([]ast.Stmt, 0)
	for !p.curIs(token.RBRACE) && !p.curIs(token.EOF) {
		s := p.parseStatement()
		if s != nil {
			stmts = append(stmts, s)
		}
	}

	endSpan := p.cur.Span
	p.expect(token.RBRACE, "代码块缺少闭合花括号 '}'")

	return &ast.BlockStmt{
		Statements: stmts,
		Span:       diagnostic.Span{File: p.srcFile, Start: startSpan.Start, End: endSpan.End},
	}
}

// parseStatement 解析单条语句
func (p *Parser) parseStatement() ast.Stmt {
	if p.curIs(token.LET) {
		return p.parseLet()
	}
	if p.curIs(token.IF) {
		return p.parseIf()
	}
	if p.curIs(token.WHILE) {
		return p.parseWhile()
	}
	if p.curIs(token.RETURN) {
		return p.parseReturn()
	}

	// 检查是否为赋值语句: ident = expr;
	if p.curIs(token.IDENT) && p.peekIs(token.ASSIGN) {
		name := p.cur.Lexeme
		startSpan := p.cur.Span
		p.advance() // 消耗 ident
		p.advance() // 消耗 '='
		val := p.parseExpression()
		p.expect(token.SEMICOLON, "赋值语句结尾期望分号 ';'")
		return &ast.AssignStmt{
			Target: name,
			Value:  val,
			Span:   diagnostic.Span{File: p.srcFile, Start: startSpan.Start, End: p.cur.Span.End},
		}
	}

	// 表达式语句（支持末尾表达式无分号作为代码块求值返回值）
	expr := p.parseExpression()
	if _, ok := expr.(*ast.UnsafeExpr); ok {
		if p.curIs(token.SEMICOLON) {
			p.advance()
		}
		return &ast.ExprStmt{
			Expr: expr,
			Span: expr.GetSpan(),
		}
	}
	if p.curIs(token.SEMICOLON) {
		p.advance()
	} else if !p.curIs(token.RBRACE) {
		p.expect(token.SEMICOLON, "表达式语句结尾期望分号 ';'")
	}

	return &ast.ExprStmt{
		Expr: expr,
		Span: expr.GetSpan(),
	}
}

// parseLet 解析 let [mut] name [: Type] = init;
func (p *Parser) parseLet() *ast.LetStmt {
	letSpan := p.cur.Span
	p.advance() // 消耗 'let'

	isMut := false
	if p.curIs(token.MUT) {
		isMut = true
		p.advance() // 消耗 'mut'
	}

	if !p.curIs(token.IDENT) {
		p.emitError("WON-SYN-008", "let 绑定语句缺少变量名标识符", p.cur.Span)
		return nil
	}
	name := p.cur.Lexeme
	p.advance()

	var typeStr string
	if p.curIs(token.COLON) {
		p.advance() // 消耗 ':'
		typeStr = p.parseTypeString()
	}

	if !p.expect(token.ASSIGN, "变量声明必须使用 '=' 赋予初始值") {
		return nil
	}

	initExpr := p.parseExpression()
	p.expect(token.SEMICOLON, "let 语句结尾期望分号 ';'")

	return &ast.LetStmt{
		IsMut:        isMut,
		Name:         name,
		Type:         typeStr,
		Init:         initExpr,
		LetTokenSpan: letSpan,
		Span:         diagnostic.Span{File: p.srcFile, Start: letSpan.Start, End: p.cur.Span.End},
	}
}

// parseIf 解析 if cond { then } [else { else }]
func (p *Parser) parseIf() *ast.IfStmt {
	ifSpan := p.cur.Span
	p.advance() // 消耗 'if'

	cond := p.parseExpression()

	if !p.curIs(token.LBRACE) {
		p.emitError("WON-SYN-009", "if 条件后必须紧随花括号代码块 '{'", p.cur.Span)
		return nil
	}
	thenBlock := p.parseBlock()

	var elseStmt ast.Stmt
	if p.curIs(token.ELSE) {
		p.advance() // 消耗 'else'
		if p.curIs(token.IF) {
			elseStmt = p.parseIf()
		} else if p.curIs(token.LBRACE) {
			elseStmt = p.parseBlock()
		} else {
			p.emitError("WON-SYN-010", "else 后必须跟随 'if' 或花括号代码块 '{'", p.cur.Span)
		}
	}

	return &ast.IfStmt{
		Cond: cond,
		Then: thenBlock,
		Else: elseStmt,
		Span: diagnostic.Span{File: p.srcFile, Start: ifSpan.Start, End: p.cur.Span.End},
	}
}

// parseWhile 解析 while cond { body }
func (p *Parser) parseWhile() *ast.WhileStmt {
	whileSpan := p.cur.Span
	p.advance() // 消耗 'while'

	cond := p.parseExpression()

	if !p.curIs(token.LBRACE) {
		p.emitError("WON-SYN-016", "while 条件后必须紧随花括号代码块 '{'", p.cur.Span)
		return nil
	}
	body := p.parseBlock()

	return &ast.WhileStmt{
		Cond: cond,
		Body: body,
		Span: diagnostic.Span{File: p.srcFile, Start: whileSpan.Start, End: body.Span.End},
	}
}

// parseReturn 解析 return [expr];
func (p *Parser) parseReturn() *ast.ReturnStmt {
	retSpan := p.cur.Span
	p.advance() // 消耗 'return'

	var val ast.Expr
	if !p.curIs(token.SEMICOLON) {
		val = p.parseExpression()
	}
	p.expect(token.SEMICOLON, "return 语句结尾期望分号 ';'")

	return &ast.ReturnStmt{
		Value: val,
		Span:  diagnostic.Span{File: p.srcFile, Start: retSpan.Start, End: p.cur.Span.End},
	}
}

// parseExpression 顶层表达式解析入口（支持比较与算术二元优先级）
func (p *Parser) parseExpression() ast.Expr {
	return p.parseEquality()
}

func (p *Parser) parseEquality() ast.Expr {
	expr := p.parseRelational()
	for p.curIs(token.EQ) || p.curIs(token.NOTEQ) {
		op := p.cur.Lexeme
		p.advance()
		right := p.parseRelational()
		expr = &ast.BinaryExpr{
			Left:  expr,
			Op:    op,
			Right: right,
			Span:  diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: right.GetSpan().End},
		}
	}
	return expr
}

func (p *Parser) parseRelational() ast.Expr {
	expr := p.parseAdditive()
	for p.curIs(token.LT) || p.curIs(token.LTEQ) || p.curIs(token.GT) || p.curIs(token.GTEQ) {
		op := p.cur.Lexeme
		p.advance()
		right := p.parseAdditive()
		expr = &ast.BinaryExpr{
			Left:  expr,
			Op:    op,
			Right: right,
			Span:  diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: right.GetSpan().End},
		}
	}
	return expr
}

func (p *Parser) parseAdditive() ast.Expr {
	expr := p.parseMultiplicative()
	for p.curIs(token.PLUS) || p.curIs(token.MINUS) {
		op := p.cur.Lexeme
		p.advance()
		right := p.parseMultiplicative()
		expr = &ast.BinaryExpr{
			Left:  expr,
			Op:    op,
			Right: right,
			Span:  diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: right.GetSpan().End},
		}
	}
	return expr
}

func (p *Parser) parseMultiplicative() ast.Expr {
	expr := p.parseUnary()
	for p.curIs(token.STAR) || p.curIs(token.SLASH) {
		op := p.cur.Lexeme
		p.advance()
		right := p.parseUnary()
		expr = &ast.BinaryExpr{
			Left:  expr,
			Op:    op,
			Right: right,
			Span:  diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: right.GetSpan().End},
		}
	}
	return expr
}

func (p *Parser) parseUnary() ast.Expr {
	if p.curIs(token.NOT) || p.curIs(token.MINUS) {
		op := p.cur.Lexeme
		startSpan := p.cur.Span
		p.advance()
		sub := p.parseUnary()
		return &ast.UnaryExpr{
			Op:   op,
			Expr: sub,
			Span: diagnostic.Span{File: p.srcFile, Start: startSpan.Start, End: sub.GetSpan().End},
		}
	}

	if p.curIs(token.AMP) {
		startSpan := p.cur.Span
		p.advance() // 消耗 '&'
		isMut := false
		if p.curIs(token.MUT) {
			isMut = true
			p.advance() // 消耗 'mut'
		}
		sub := p.parseUnary()
		return &ast.BorrowExpr{
			IsMut: isMut,
			Expr:  sub,
			Span:  diagnostic.Span{File: p.srcFile, Start: startSpan.Start, End: sub.GetSpan().End},
		}
	}

	return p.parsePostfix()
}

func (p *Parser) parsePostfix() ast.Expr {
	expr := p.parsePrimary()

	for {
		if p.curIs(token.DOT) {
			p.advance() // 消耗 '.'
			if !p.curIs(token.IDENT) {
				p.emitError("WON-SYN-011", "方法调用期待方法名称标识符", p.cur.Span)
				break
			}
			method := p.cur.Lexeme
			p.advance()
			if p.curIs(token.LPAREN) {
				p.advance()
				args := p.parseArgList()
				endSpan := p.cur.Span
				p.expect(token.RPAREN, "方法调用缺少闭合括号 ')'")
				expr = &ast.MethodCallExpr{
					Receiver: expr,
					Method:   method,
					Args:     args,
					Span:     diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: endSpan.End},
				}
			} else {
				// 视为无参属性访问方法
				expr = &ast.MethodCallExpr{
					Receiver: expr,
					Method:   method,
					Args:     nil,
					Span:     diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: p.cur.Span.End},
				}
			}
			continue
		}
		break
	}

	return expr
}

func (p *Parser) parsePrimary() ast.Expr {
	startSpan := p.cur.Span

	// 括号分组表达式
	if p.curIs(token.LPAREN) {
		p.advance()
		expr := p.parseExpression()
		p.expect(token.RPAREN, "缺少闭合括号 ')'")
		return expr
	}

	// 整型字面量
	if p.curIs(token.INT) {
		raw := p.cur.Lexeme
		p.advance()
		val, _ := lexer.ParseIntLiteral(raw)
		return &ast.IntegerLiteral{
			Value: val,
			Raw:   raw,
			Span:  startSpan,
		}
	}

	// 字符串字面量
	if p.curIs(token.STRING) {
		val := p.cur.Lexeme
		p.advance()
		return &ast.StringLiteral{
			Value: val,
			Span:  startSpan,
		}
	}

	// 不安全代码块表达式: unsafe { ... }
	if p.curIs(token.UNSAFE) {
		p.advance() // 消耗 'unsafe'
		if !p.curIs(token.LBRACE) {
			p.emitError("WON-SYN-012", "unsafe 关键字后必须紧随花括号代码块 '{'", p.cur.Span)
			return &ast.Identifier{Name: "unsafe_err", Span: startSpan}
		}
		block := p.parseBlock()
		return &ast.UnsafeExpr{
			Block: block,
			Span:  diagnostic.Span{File: p.srcFile, Start: startSpan.Start, End: block.Span.End},
		}
	}

	// 数组字面量: [0; 256]
	if p.curIs(token.LBRACKET) {
		p.advance() // 消耗 '['
		elem := p.parseExpression()
		if p.curIs(token.SEMICOLON) {
			p.advance() // 消耗 ';'
			countVal := int64(0)
			if p.curIs(token.INT) {
				countVal, _ = lexer.ParseIntLiteral(p.cur.Lexeme)
				p.advance()
			} else {
				p.emitError("WON-SYN-013", "数组重复填充大小必须为整数常量", p.cur.Span)
			}
			endSpan := p.cur.Span
			p.expect(token.RBRACKET, "数组字面量缺少闭合括号 ']'")
			return &ast.ArrayRepeatLiteral{
				Elem:  elem,
				Count: countVal,
				Span:  diagnostic.Span{File: p.srcFile, Start: startSpan.Start, End: endSpan.End},
			}
		}
		p.expect(token.RBRACKET, "数组字面量缺少闭合括号 ']'")
		return elem
	}

	// 跨命名空间调用 (zlib::compress(...)) 或标识符 / 调用
	if p.curIs(token.IDENT) {
		name := p.cur.Lexeme
		p.advance()

		if p.curIs(token.COLONCOLON) {
			p.advance() // 消耗 '::'
			if !p.curIs(token.IDENT) {
				p.emitError("WON-SYN-014", "命名空间 '::' 后期望函数名标识符", p.cur.Span)
				return &ast.Identifier{Name: name, Span: startSpan}
			}
			fnName := p.cur.Lexeme
			p.advance()
			p.expect(token.LPAREN, "函数调用缺少参数起始括号 '('")
			args := p.parseArgList()
			endSpan := p.cur.Span
			p.expect(token.RPAREN, "函数调用缺少参数结束括号 ')'")
			return &ast.NamespacedCallExpr{
				Namespace: name,
				Function:  fnName,
				Args:      args,
				Span:      diagnostic.Span{File: p.srcFile, Start: startSpan.Start, End: endSpan.End},
			}
		}

		if p.curIs(token.LPAREN) {
			p.advance()
			args := p.parseArgList()
			endSpan := p.cur.Span
			p.expect(token.RPAREN, "函数调用缺少参数结束括号 ')'")
			return &ast.CallExpr{
				Callee: name,
				Args:   args,
				Span:   diagnostic.Span{File: p.srcFile, Start: startSpan.Start, End: endSpan.End},
			}
		}

		return &ast.Identifier{
			Name: name,
			Span: startSpan,
		}
	}

	p.emitError("WON-SYN-015", fmt.Sprintf("未预期的表达式语法标记: %q", p.cur.Lexeme), startSpan)
	p.advance()
	return &ast.Identifier{Name: "err", Span: startSpan}
}

func (p *Parser) parseArgList() []ast.Expr {
	args := make([]ast.Expr, 0)
	for !p.curIs(token.RPAREN) && !p.curIs(token.EOF) {
		args = append(args, p.parseExpression())
		if p.curIs(token.COMMA) {
			p.advance()
		} else {
			break
		}
	}
	return args
}

// Package parser 实现了 WON Draft 0.1 规范递归下降与 Pratt 优先级爬坡语法分析器
// 支持缩进作用域 (INDENT/DEDENT)、所有顶层声明、模式匹配、并发原语以及高精度源码 Span 追踪。
package parser

import (
	"fmt"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/token"
)

// Parser 规范语法分析器实例
type Parser struct {
	tokens      []token.Token
	pos         int
	report      *diagnostic.Report
	hadErr      bool
	srcFile     string
	noStructLit bool // 在控制流头部表达式 (if/while/for/match) 中禁用隐式结构体字面量，防止将旧式花括号块误解析为结构体
}

// New 创建规范语法分析器并预读取词法流以支持无损前瞻消歧
func New(lex *lexer.Lexer, report *diagnostic.Report, srcFile string) *Parser {
	tokens := make([]token.Token, 0, 128)
	for {
		tok := lex.NextToken()
		tokens = append(tokens, tok)
		if tok.Kind == token.EOF {
			break
		}
	}
	return &Parser{
		tokens:  tokens,
		pos:     0,
		report:  report,
		srcFile: srcFile,
	}
}

func (p *Parser) cur() token.Token {
	if p.pos >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[p.pos]
}

func (p *Parser) peekAt(offset int) token.Token {
	idx := p.pos + offset
	if idx < 0 {
		return p.tokens[0]
	}
	if idx >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[idx]
}

func (p *Parser) curIs(k token.Kind) bool {
	return p.cur().Kind == k
}

func (p *Parser) peekIs(k token.Kind) bool {
	return p.peekAt(1).Kind == k
}

func (p *Parser) advance() token.Token {
	tok := p.cur()
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	return tok
}

func (p *Parser) prevEnd() diagnostic.Position {
	if p.pos > 0 {
		return p.tokens[p.pos-1].Span.End
	}
	return p.cur().Span.Start
}

func (p *Parser) emitError(code, message string, span diagnostic.Span) {
	p.hadErr = true
	if p.report != nil {
		p.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     code,
			Severity: "error",
			Message:  message,
			File:     p.srcFile,
			Span:     span,
		})
	}
}

func (p *Parser) expect(k token.Kind, code, msg string) bool {
	if p.curIs(k) {
		p.advance()
		return true
	}
	p.emitError(code, fmt.Sprintf("%s (实际遇到 %s: %q)", msg, p.cur().Kind, p.cur().Lexeme), p.cur().Span)
	return false
}

func (p *Parser) skipNewlines() {
	for p.curIs(token.NEWLINE) {
		p.advance()
	}
}

func (p *Parser) expectNewlineOrEnd(code, msg string) {
	if p.curIs(token.NEWLINE) {
		p.advance()
		return
	}
	if p.curIs(token.DEDENT) || p.curIs(token.EOF) {
		return
	}
	if p.curIs(token.SEMICOLON) {
		p.emitError("WON-SYN-009", "规范 WON 语法无需语句末尾分号 ';'", p.cur().Span)
		p.advance()
		if p.curIs(token.NEWLINE) {
			p.advance()
		}
		return
	}
	p.emitError(code, fmt.Sprintf("%s (实际遇到 %s: %q)", msg, p.cur().Kind, p.cur().Lexeme), p.cur().Span)
	p.synchronizeToLineEnd()
}

func (p *Parser) synchronizeToLineEnd() {
	for !p.curIs(token.NEWLINE) && !p.curIs(token.DEDENT) && !p.curIs(token.EOF) {
		p.advance()
	}
	if p.curIs(token.NEWLINE) {
		p.advance()
	}
}

// endsWithBlock 判断表达式是否以缩进块体结尾（如 unsafe: Block, match ..., if ..., scope ...），
// 该类表达式已由内部 parseBlock 消耗了闭合 DEDENT，外层语句无需额外换行符。
func endsWithBlock(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *ast.UnsafeExpr, *ast.IfStmt, *ast.WhileStmt, *ast.ForStmt, *ast.MatchExpr, *ast.ScopeExpr:
		return true
	case *ast.SpawnExpr:
		return e.Body != nil
	case *ast.BinaryExpr:
		return endsWithBlock(e.Right)
	case *ast.UnaryExpr:
		return endsWithBlock(e.Expr)
	case *ast.RangeExpr:
		return endsWithBlock(e.End)
	default:
		return false
	}
}

// ParseProgram 解析整个 WON 源文件顶层项目列表
func (p *Parser) ParseProgram() *ast.Program {
	prog := &ast.Program{
		Imports:       make([]*ast.ImportDecl, 0),
		ModuleImports: make([]*ast.ModuleImportDecl, 0),
		Functions:     make([]*ast.FunctionDecl, 0),
		Decls:         make([]ast.Decl, 0),
		Span:          diagnostic.Span{File: p.srcFile, Start: p.cur().Span.Start},
	}

	for !p.curIs(token.EOF) {
		p.skipNewlines()
		if p.curIs(token.EOF) {
			break
		}

		// 处理孤立 DEDENT
		if p.curIs(token.DEDENT) {
			p.advance()
			continue
		}

		// 捕获顶层意外缩进
		if p.curIs(token.INDENT) {
			p.emitError("WON-SYN-002", "顶层作用域出现未预期的缩进", p.cur().Span)
			p.advance()
			continue
		}

		// C 头文件引入指令: #import <header.h>
		if p.curIs(token.CIMPORT) {
			imp := &ast.ImportDecl{
				HeaderPath: p.cur().Lexeme,
				Span:       p.cur().Span,
			}
			prog.Imports = append(prog.Imports, imp)
			prog.Decls = append(prog.Decls, imp)
			p.advance()
			p.expectNewlineOrEnd("WON-SYN-005", "#import 指令后期望换行")
			continue
		}

		// WON 模块导入: import ... 或 from ... import ...
		if p.curIs(token.IMPORT) || (p.curIs(token.IDENT) && p.cur().Lexeme == "from") {
			modImp := p.parseModuleImport()
			if modImp != nil {
				prog.ModuleImports = append(prog.ModuleImports, modImp)
				prog.Decls = append(prog.Decls, modImp)
			}
			continue
		}

		decl := p.parseItem()
		if decl != nil {
			prog.Decls = append(prog.Decls, decl)
			if fn, ok := decl.(*ast.FunctionDecl); ok {
				prog.Functions = append(prog.Functions, fn)
			}
		}
	}

	prog.Span.End = p.cur().Span.End
	return prog
}

// parseModuleImport 解析 import path [as alias] 或 from path import (* | ident_list)
func (p *Parser) parseModuleImport() *ast.ModuleImportDecl {
	start := p.cur().Span.Start
	if p.curIs(token.IMPORT) {
		p.advance()
		path := p.parseDotOrColonPath()
		alias := ""
		if p.curIs(token.AS) {
			p.advance()
			if p.curIs(token.IDENT) {
				alias = p.cur().Lexeme
				p.advance()
			} else {
				p.emitError("WON-SYN-005", "import ... as 后期望模块别名标识符", p.cur().Span)
			}
		}
		end := p.prevEnd()
		p.expectNewlineOrEnd("WON-SYN-005", "import 声明后期望换行")
		return &ast.ModuleImportDecl{
			IsFrom: false,
			Path:   path,
			Alias:  alias,
			Span:   diagnostic.Span{File: p.srcFile, Start: start, End: end},
		}
	}

	// from path import ...
	p.advance() // 消耗 'from'
	path := p.parseDotOrColonPath()
	p.expect(token.IMPORT, "WON-SYN-005", "from 路径后期望关键字 'import'")
	wildcard := false
	items := make([]string, 0)
	if p.curIs(token.STAR) {
		wildcard = true
		p.advance()
	} else {
		for {
			if !p.curIs(token.IDENT) {
				p.emitError("WON-SYN-005", "from ... import 后期望导入符号标识符或 '*'", p.cur().Span)
				break
			}
			items = append(items, p.cur().Lexeme)
			p.advance()
			if p.curIs(token.COMMA) {
				p.advance()
			} else {
				break
			}
		}
	}
	end := p.prevEnd()
	p.expectNewlineOrEnd("WON-SYN-005", "from import 声明后期望换行")
	return &ast.ModuleImportDecl{
		IsFrom:   true,
		Path:     path,
		Items:    items,
		Wildcard: wildcard,
		Span:     diagnostic.Span{File: p.srcFile, Start: start, End: end},
	}
}

func (p *Parser) parseDotOrColonPath() []string {
	parts := make([]string, 0, 4)
	if !p.curIs(token.IDENT) {
		p.emitError("WON-SYN-005", "期望模块路径标识符", p.cur().Span)
		return parts
	}
	parts = append(parts, p.cur().Lexeme)
	p.advance()
	for p.curIs(token.COLONCOLON) || p.curIs(token.DOT) {
		p.advance()
		if !p.curIs(token.IDENT) {
			p.emitError("WON-SYN-005", "路径分隔符后期望标识符", p.cur().Span)
			break
		}
		parts = append(parts, p.cur().Lexeme)
		p.advance()
	}
	return parts
}

// parseItem 解析顶层项 [pub] (fn | struct | enum | trait | impl | type | mod)
func (p *Parser) parseItem() ast.Decl {
	startSpan := p.cur().Span
	isPub := false
	if p.curIs(token.PUB) {
		isPub = true
		p.advance()
	}

	switch p.cur().Kind {
	case token.FN:
		fn := p.parseFunction(isPub)
		if fn == nil {
			return nil
		}
		return fn
	case token.STRUCT:
		return p.parseStruct(isPub, startSpan.Start)
	case token.ENUM:
		return p.parseEnum(isPub, startSpan.Start)
	case token.TRAIT:
		return p.parseTrait(isPub, startSpan.Start)
	case token.IMPL:
		return p.parseImpl(startSpan.Start)
	case token.TYPE:
		return p.parseTypeAlias(isPub, startSpan.Start)
	case token.MOD:
		return p.parseMod(isPub, startSpan.Start)
	case token.LBRACE:
		p.emitError("WON-SYN-005", "规范 WON 语法严禁使用花括号 '{' 作为块定界符，请使用冒号 ':' 后接 4 空格缩进体", p.cur().Span)
		p.advance()
		return nil
	default:
		p.emitError("WON-SYN-005", fmt.Sprintf("未知或非法的顶层声明关键字: %q", p.cur().Lexeme), p.cur().Span)
		p.synchronizeToLineEnd()
		return nil
	}
}

// parseGenericParams 解析泛型参数列表 [T: Bound1 + Bound2, U]
func (p *Parser) parseGenericParams() []*ast.GenericParam {
	if !p.curIs(token.LBRACKET) {
		return nil
	}
	p.advance() // '['
	params := make([]*ast.GenericParam, 0)
	for !p.curIs(token.RBRACKET) && !p.curIs(token.EOF) {
		if !p.curIs(token.IDENT) {
			p.emitError("WON-SYN-005", "泛型形参声明期望类型参数名称标识符", p.cur().Span)
			for !p.curIs(token.RBRACKET) && !p.curIs(token.COMMA) && !p.curIs(token.EOF) && !p.curIs(token.NEWLINE) {
				p.advance()
			}
			if p.curIs(token.COMMA) {
				p.advance()
				continue
			}
			break
		}
		gStart := p.cur().Span.Start
		name := p.cur().Lexeme
		p.advance()
		var bounds []ast.TypeNode
		if p.curIs(token.COLON) {
			p.advance()
			for {
				b := p.parseType()
				if b != nil {
					bounds = append(bounds, b)
				}
				if p.curIs(token.PLUS) {
					p.advance()
				} else {
					break
				}
			}
		}
		params = append(params, &ast.GenericParam{
			Name:   name,
			Bounds: bounds,
			Span:   diagnostic.Span{File: p.srcFile, Start: gStart, End: p.prevEnd()},
		})
		if p.curIs(token.COMMA) {
			p.advance()
		} else {
			break
		}
	}
	p.expect(token.RBRACKET, "WON-SYN-007", "泛型参数列表缺少闭合 ']'")
	return params
}

// parseGenericArgs 解析泛型实参列表 [Type1, Type2]
func (p *Parser) parseGenericArgs() []ast.TypeNode {
	if !p.curIs(token.LBRACKET) {
		return nil
	}
	p.advance() // '['
	args := make([]ast.TypeNode, 0)
	if p.curIs(token.RBRACKET) {
		p.emitError("WON-SYN-005", "泛型实参列表不能为空 '[]'", p.cur().Span)
		p.advance()
		return args
	}
	for !p.curIs(token.RBRACKET) && !p.curIs(token.EOF) {
		t := p.parseType()
		if t == nil {
			break
		}
		args = append(args, t)
		if p.curIs(token.COMMA) {
			p.advance()
		} else {
			break
		}
	}
	p.expect(token.RBRACKET, "WON-SYN-007", "泛型实参列表缺少闭合 ']'")
	return args
}

// parseType 解析结构化类型节点，遵循 Draft 0.1 EBNF 第 3 节
func (p *Parser) parseType() ast.TypeNode {
	start := p.cur().Span.Start

	// &T 或 &mut T
	if p.curIs(token.AMP) {
		p.advance()
		isMut := false
		if p.curIs(token.MUT) {
			isMut = true
			p.advance()
		}
		elem := p.parseType()
		return &ast.RefType{
			IsMut: isMut,
			Elem:  elem,
			Span:  diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
		}
	}

	// *const T 或 *mut T
	if p.curIs(token.STAR) {
		p.advance()
		isMut := false
		if p.curIs(token.MUT) {
			isMut = true
			p.advance()
		} else if p.curIs(token.IDENT) && p.cur().Lexeme == "const" {
			p.advance()
		} else {
			p.emitError("WON-SYN-005", "原生指针类型 '*' 后必须明确指定 'const' 或 'mut'", p.cur().Span)
		}
		elem := p.parseType()
		return &ast.RawPtrType{
			IsMut: isMut,
			Elem:  elem,
			Span:  diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
		}
	}

	// [T; N] 定长数组类型 或 [T] 切片类型
	if p.curIs(token.LBRACKET) {
		p.advance()
		elem := p.parseType()
		if p.curIs(token.SEMICOLON) {
			p.advance()
			size := p.cur().Lexeme
			p.expect(token.INT, "WON-SYN-005", "定长数组大小期望为整数字面量")
			p.expect(token.RBRACKET, "WON-SYN-007", "定长数组类型缺少闭合 ']'")
			return &ast.ArrayType{
				Elem: elem,
				Size: size,
				Span: diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
			}
		}
		p.expect(token.RBRACKET, "WON-SYN-007", "切片类型缺少闭合 ']'")
		return &ast.SliceType{
			Elem: elem,
			Span: diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
		}
	}

	// (T1, T2) 元组类型或单元类型 ()
	if p.curIs(token.LPAREN) {
		p.advance()
		elems := make([]ast.TypeNode, 0)
		for !p.curIs(token.RPAREN) && !p.curIs(token.EOF) {
			elems = append(elems, p.parseType())
			if p.curIs(token.COMMA) {
				p.advance()
			} else {
				break
			}
		}
		p.expect(token.RPAREN, "WON-SYN-007", "元组类型缺少闭合 ')'")
		return &ast.TupleType{
			Elems: elems,
			Span:  diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
		}
	}

	// 函数签名类型 fn(T1, T2) -> Ret
	if p.curIs(token.FN) {
		p.advance()
		p.expect(token.LPAREN, "WON-SYN-005", "函数类型期望 '('")
		params := make([]ast.TypeNode, 0)
		for !p.curIs(token.RPAREN) && !p.curIs(token.EOF) {
			params = append(params, p.parseType())
			if p.curIs(token.COMMA) {
				p.advance()
			} else {
				break
			}
		}
		p.expect(token.RPAREN, "WON-SYN-007", "函数类型缺少闭合 ')'")
		var ret ast.TypeNode
		if p.curIs(token.RARROW) {
			p.advance()
			ret = p.parseType()
		}
		return &ast.FnType{
			Params:     params,
			ReturnType: ret,
			Span:       diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
		}
	}

	// 具名路径类型 Path [GenericArgs]
	if p.curIs(token.IDENT) || p.curIs(token.SELF_TYPE) || p.curIs(token.COLONCOLON) {
		path := make([]string, 0, 2)
		if p.curIs(token.COLONCOLON) {
			p.advance()
		}
		if !p.curIs(token.IDENT) && !p.curIs(token.SELF_TYPE) {
			p.emitError("WON-SYN-005", "期望类型名称标识符", p.cur().Span)
			return &ast.NamedType{Path: []string{"err"}, Span: p.cur().Span}
		}
		path = append(path, p.cur().Lexeme)
		p.advance()
		for p.curIs(token.COLONCOLON) {
			p.advance()
			if !p.curIs(token.IDENT) && !p.curIs(token.SELF_TYPE) {
				p.emitError("WON-SYN-005", "'::' 后期望类型名", p.cur().Span)
				break
			}
			path = append(path, p.cur().Lexeme)
			p.advance()
		}
		var gArgs []ast.TypeNode
		if p.curIs(token.LBRACKET) {
			gArgs = p.parseGenericArgs()
		}
		return &ast.NamedType{
			Path:        path,
			GenericArgs: gArgs,
			Span:        diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
		}
	}

	p.emitError("WON-SYN-005", fmt.Sprintf("非法的类型标注: %q", p.cur().Lexeme), p.cur().Span)
	tok := p.advance()
	return &ast.NamedType{Path: []string{tok.Lexeme}, Span: tok.Span}
}

// parseFunction 解析规范函数声明: fn name[T](params) -> Ret: NEWLINE Block
func (p *Parser) parseFunction(isPub bool) *ast.FunctionDecl {
	fnSpan := p.cur().Span
	p.advance() // 'fn'

	if !p.curIs(token.IDENT) {
		p.emitError("WON-SYN-005", "函数声明缺少函数名称标识符", p.cur().Span)
		p.synchronizeToLineEnd()
		return nil
	}
	fnName := p.cur().Lexeme
	p.advance()

	var genParams []*ast.GenericParam
	if p.curIs(token.LBRACKET) {
		genParams = p.parseGenericParams()
	}

	if !p.expect(token.LPAREN, "WON-SYN-005", "函数名后期望形参列表 '('") {
		p.synchronizeToLineEnd()
		return nil
	}

	params := p.parseParamList()
	p.expect(token.RPAREN, "WON-SYN-007", "函数形参列表缺少闭合 ')'")

	retTypeStr := "void"
	var retTypeAST ast.TypeNode
	if p.curIs(token.RARROW) {
		p.advance()
		retTypeAST = p.parseType()
		if retTypeAST != nil {
			retTypeStr = retTypeAST.String()
		}
	}

	// 检测并显式拒绝 Phase 0 旧式花括号函数体
	if p.curIs(token.LBRACE) {
		p.emitError("WON-SYN-005", "函数体必须使用冒号 ':' 后接缩进代码块，规范 WON 语法禁止花括号 '{ ... }' 函数体", p.cur().Span)
		p.synchronizePastLegacyBraces()
		return nil
	}

	if !p.expect(token.COLON, "WON-SYN-004", "函数签名后期望冒号 ':' 引入缩进函数体") {
		p.synchronizeToLineEnd()
		return nil
	}

	body := p.parseIndentedBlock()
	return &ast.FunctionDecl{
		IsPub:         isPub,
		Name:          fnName,
		GenericParams: genParams,
		Params:        params,
		ReturnType:    retTypeStr,
		ReturnTypeAST: retTypeAST,
		Body:          body,
		Span:          diagnostic.Span{File: p.srcFile, Start: fnSpan.Start, End: body.Span.End},
	}
}

func (p *Parser) synchronizePastLegacyBraces() {
	depth := 0
	for !p.curIs(token.EOF) {
		if p.curIs(token.LBRACE) {
			depth++
		} else if p.curIs(token.RBRACE) {
			depth--
			if depth <= 0 {
				p.advance()
				return
			}
		}
		p.advance()
	}
}

func (p *Parser) parseParamList() []*ast.Param {
	params := make([]*ast.Param, 0)
	for !p.curIs(token.RPAREN) && !p.curIs(token.EOF) {
		pStart := p.cur().Span.Start

		// self / &self / &mut self 接收者处理
		if p.curIs(token.SELF) {
			p.advance()
			params = append(params, &ast.Param{
				IsSelf: true,
				Name:   "self",
				Type:   "Self",
				Span:   diagnostic.Span{File: p.srcFile, Start: pStart, End: p.prevEnd()},
			})
		} else if p.curIs(token.AMP) && (p.peekIs(token.SELF) || (p.peekIs(token.MUT) && p.peekAt(2).Kind == token.SELF)) {
			p.advance() // '&'
			isMut := false
			if p.curIs(token.MUT) {
				isMut = true
				p.advance()
			}
			p.advance() // 'self'
			tStr := "&Self"
			if isMut {
				tStr = "&mut Self"
			}
			params = append(params, &ast.Param{
				IsSelf: true,
				IsRef:  true,
				IsMut:  isMut,
				Name:   "self",
				Type:   tStr,
				Span:   diagnostic.Span{File: p.srcFile, Start: pStart, End: p.prevEnd()},
			})
		} else {
			isMut := false
			if p.curIs(token.MUT) {
				isMut = true
				p.advance()
			}
			if !p.curIs(token.IDENT) {
				p.emitError("WON-SYN-005", "函数形参期望标识符名称", p.cur().Span)
				break
			}
			pName := p.cur().Lexeme
			p.advance()
			if !p.expect(token.COLON, "WON-SYN-005", "形参名后期望类型分隔符 ':'") {
				break
			}
			tNode := p.parseType()
			tStr := ""
			if tNode != nil {
				tStr = tNode.String()
			}
			params = append(params, &ast.Param{
				IsMut:   isMut,
				Name:    pName,
				Type:    tStr,
				TypeAST: tNode,
				Span:    diagnostic.Span{File: p.srcFile, Start: pStart, End: p.prevEnd()},
			})
		}

		if p.curIs(token.COMMA) {
			p.advance()
		} else {
			break
		}
	}
	return params
}

// parseStruct 解析 struct Name[T]: NEWLINE INDENT { StructField } DEDENT
func (p *Parser) parseStruct(isPub bool, start diagnostic.Position) *ast.StructDecl {
	p.advance() // 'struct'
	if !p.curIs(token.IDENT) {
		p.emitError("WON-SYN-005", "struct 声明缺少结构体名称", p.cur().Span)
		p.synchronizeToLineEnd()
		return nil
	}
	name := p.cur().Lexeme
	p.advance()
	genParams := p.parseGenericParams()

	if !p.expect(token.COLON, "WON-SYN-004", "struct 签名后期望冒号 ':' 引入字段列表") {
		p.synchronizeToLineEnd()
		return nil
	}
	p.expectNewlineOrEnd("WON-SYN-004", "struct ':' 后期望换行与缩进字段体")
	p.skipNewlines()

	fields := make([]*ast.StructField, 0)
	if !p.curIs(token.INDENT) {
		p.emitError("WON-SYN-004", "struct 结构体缺少缩进字段定义", p.cur().Span)
	} else {
		p.advance() // INDENT
		for !p.curIs(token.DEDENT) && !p.curIs(token.EOF) {
			p.skipNewlines()
			if p.curIs(token.DEDENT) || p.curIs(token.EOF) {
				break
			}
			f := p.parseStructField()
			if f != nil {
				fields = append(fields, f)
			}
		}
		if p.curIs(token.DEDENT) {
			p.advance()
		}
	}
	return &ast.StructDecl{
		IsPub:         isPub,
		Name:          name,
		GenericParams: genParams,
		Fields:        fields,
		Span:          diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
	}
}

func (p *Parser) parseStructField() *ast.StructField {
	fStart := p.cur().Span.Start
	isPub := false
	isVar := false
	if p.curIs(token.PUB) {
		isPub = true
		p.advance()
	}
	if p.curIs(token.VAR) {
		isVar = true
		p.advance()
	}
	if !p.curIs(token.IDENT) {
		p.emitError("WON-SYN-005", "结构体字段期望标识符名称", p.cur().Span)
		p.synchronizeToLineEnd()
		return nil
	}
	name := p.cur().Lexeme
	p.advance()
	p.expect(token.COLON, "WON-SYN-005", "字段名后期望 ':'")
	ft := p.parseType()
	end := p.prevEnd()
	p.expectNewlineOrEnd("WON-SYN-005", "结构体字段定义结尾期望换行")
	return &ast.StructField{
		IsPub: isPub,
		IsVar: isVar,
		Name:  name,
		Type:  ft,
		Span:  diagnostic.Span{File: p.srcFile, Start: fStart, End: end},
	}
}

// parseEnum 解析 enum Name[T]: NEWLINE INDENT { EnumVariant } DEDENT
func (p *Parser) parseEnum(isPub bool, start diagnostic.Position) *ast.EnumDecl {
	p.advance() // 'enum'
	if !p.curIs(token.IDENT) {
		p.emitError("WON-SYN-005", "enum 声明缺少枚举名称", p.cur().Span)
		p.synchronizeToLineEnd()
		return nil
	}
	name := p.cur().Lexeme
	p.advance()
	genParams := p.parseGenericParams()

	if !p.expect(token.COLON, "WON-SYN-004", "enum 签名后期望冒号 ':'") {
		p.synchronizeToLineEnd()
		return nil
	}
	p.expectNewlineOrEnd("WON-SYN-004", "enum ':' 后期望换行")
	p.skipNewlines()

	variants := make([]*ast.EnumVariant, 0)
	if !p.curIs(token.INDENT) {
		p.emitError("WON-SYN-004", "enum 枚举缺少缩进变体列表", p.cur().Span)
	} else {
		p.advance()
		for !p.curIs(token.DEDENT) && !p.curIs(token.EOF) {
			p.skipNewlines()
			if p.curIs(token.DEDENT) || p.curIs(token.EOF) {
				break
			}
			vStart := p.cur().Span.Start
			if !p.curIs(token.IDENT) {
				p.emitError("WON-SYN-005", "枚举变体期望标识符名称", p.cur().Span)
				p.synchronizeToLineEnd()
				continue
			}
			vName := p.cur().Lexeme
			p.advance()
			var tupleTypes []ast.TypeNode
			var structFields []*ast.StructField

			if p.curIs(token.LPAREN) {
				p.advance()
				for !p.curIs(token.RPAREN) && !p.curIs(token.EOF) {
					tupleTypes = append(tupleTypes, p.parseType())
					if p.curIs(token.COMMA) {
						p.advance()
					} else {
						break
					}
				}
				p.expect(token.RPAREN, "WON-SYN-007", "元组变体缺少闭合 ')'")
				p.expectNewlineOrEnd("WON-SYN-005", "枚举变体结尾期望换行")
			} else if p.curIs(token.COLON) {
				p.advance()
				p.expectNewlineOrEnd("WON-SYN-004", "结构体变体 ':' 后期望换行")
				p.skipNewlines()
				if !p.curIs(token.INDENT) {
					p.emitError("WON-SYN-004", "结构体枚举变体缺少缩进字段体", p.cur().Span)
				} else {
					p.advance()
					for !p.curIs(token.DEDENT) && !p.curIs(token.EOF) {
						p.skipNewlines()
						if p.curIs(token.DEDENT) || p.curIs(token.EOF) {
							break
						}
						f := p.parseStructField()
						if f != nil {
							structFields = append(structFields, f)
						}
					}
					if p.curIs(token.DEDENT) {
						p.advance()
					}
				}
			} else {
				p.expectNewlineOrEnd("WON-SYN-005", "枚举变体结尾期望换行")
			}

			variants = append(variants, &ast.EnumVariant{
				Name:         vName,
				TupleTypes:   tupleTypes,
				StructFields: structFields,
				Span:         diagnostic.Span{File: p.srcFile, Start: vStart, End: p.prevEnd()},
			})
		}
		if p.curIs(token.DEDENT) {
			p.advance()
		}
	}

	return &ast.EnumDecl{
		IsPub:         isPub,
		Name:          name,
		GenericParams: genParams,
		Variants:      variants,
		Span:          diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
	}
}

// parseTrait 解析 trait Name[T]: NEWLINE INDENT { TraitItem } DEDENT
func (p *Parser) parseTrait(isPub bool, start diagnostic.Position) *ast.TraitDecl {
	p.advance() // 'trait'
	if !p.curIs(token.IDENT) {
		p.emitError("WON-SYN-005", "trait 声明缺少名称标识符", p.cur().Span)
		p.synchronizeToLineEnd()
		return nil
	}
	name := p.cur().Lexeme
	p.advance()
	genParams := p.parseGenericParams()

	if !p.expect(token.COLON, "WON-SYN-004", "trait 签名后期望冒号 ':'") {
		p.synchronizeToLineEnd()
		return nil
	}
	p.expectNewlineOrEnd("WON-SYN-004", "trait ':' 后期望换行")
	p.skipNewlines()

	methods := make([]*ast.FunctionDecl, 0)
	if !p.curIs(token.INDENT) {
		p.emitError("WON-SYN-004", "trait 缺少缩进方法定义列表", p.cur().Span)
	} else {
		p.advance()
		for !p.curIs(token.DEDENT) && !p.curIs(token.EOF) {
			p.skipNewlines()
			if p.curIs(token.DEDENT) || p.curIs(token.EOF) {
				break
			}
			mPub := false
			if p.curIs(token.PUB) {
				mPub = true
				p.advance()
			}
			if !p.curIs(token.FN) {
				p.emitError("WON-SYN-005", "trait 块内部期望 'fn' 方法签名", p.cur().Span)
				p.synchronizeToLineEnd()
				continue
			}
			fnStart := p.cur().Span.Start
			p.advance() // 'fn'
			if !p.curIs(token.IDENT) {
				p.emitError("WON-SYN-005", "trait 方法缺少名称", p.cur().Span)
				p.synchronizeToLineEnd()
				continue
			}
			mName := p.cur().Lexeme
			p.advance()
			mGen := p.parseGenericParams()
			p.expect(token.LPAREN, "WON-SYN-005", "trait 方法期望 '('")
			mParams := p.parseParamList()
			p.expect(token.RPAREN, "WON-SYN-007", "trait 方法缺少闭合 ')'")
			retStr := "void"
			var retAST ast.TypeNode
			if p.curIs(token.RARROW) {
				p.advance()
				retAST = p.parseType()
				if retAST != nil {
					retStr = retAST.String()
				}
			}
			var body *ast.BlockStmt
			if p.curIs(token.COLON) {
				body = p.parseIndentedBlock()
			} else {
				p.expectNewlineOrEnd("WON-SYN-005", "trait 方法签名后期望换行或 ':' 默认实现")
			}
			methods = append(methods, &ast.FunctionDecl{
				IsPub:         mPub,
				Name:          mName,
				GenericParams: mGen,
				Params:        mParams,
				ReturnType:    retStr,
				ReturnTypeAST: retAST,
				Body:          body,
				Span:          diagnostic.Span{File: p.srcFile, Start: fnStart, End: p.prevEnd()},
			})
		}
		if p.curIs(token.DEDENT) {
			p.advance()
		}
	}
	return &ast.TraitDecl{
		IsPub:         isPub,
		Name:          name,
		GenericParams: genParams,
		Methods:       methods,
		Span:          diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
	}
}

// parseImpl 解析 impl [GenericParams] [TraitType for] TargetType: NEWLINE INDENT { FunctionDecl } DEDENT
func (p *Parser) parseImpl(start diagnostic.Position) *ast.ImplBlock {
	p.advance() // 'impl'
	genParams := p.parseGenericParams()
	firstType := p.parseType()

	var traitType ast.TypeNode
	targetType := firstType
	if p.curIs(token.FOR) {
		p.advance()
		traitType = firstType
		targetType = p.parseType()
	}

	if !p.expect(token.COLON, "WON-SYN-004", "impl 签名后期望冒号 ':'") {
		p.synchronizeToLineEnd()
		return nil
	}
	p.expectNewlineOrEnd("WON-SYN-004", "impl ':' 后期望换行")
	p.skipNewlines()

	methods := make([]*ast.FunctionDecl, 0)
	if !p.curIs(token.INDENT) {
		p.emitError("WON-SYN-004", "impl 实现块缺少缩进方法列表", p.cur().Span)
	} else {
		p.advance()
		for !p.curIs(token.DEDENT) && !p.curIs(token.EOF) {
			p.skipNewlines()
			if p.curIs(token.DEDENT) || p.curIs(token.EOF) {
				break
			}
			mPub := false
			if p.curIs(token.PUB) {
				mPub = true
				p.advance()
			}
			if !p.curIs(token.FN) {
				p.emitError("WON-SYN-005", "impl 块内部期望 'fn' 方法实现", p.cur().Span)
				p.synchronizeToLineEnd()
				continue
			}
			fn := p.parseFunction(mPub)
			if fn != nil {
				methods = append(methods, fn)
			}
		}
		if p.curIs(token.DEDENT) {
			p.advance()
		}
	}

	return &ast.ImplBlock{
		GenericParams: genParams,
		TraitType:     traitType,
		TargetType:    targetType,
		Methods:       methods,
		Span:          diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
	}
}

func (p *Parser) parseTypeAlias(isPub bool, start diagnostic.Position) *ast.TypeAliasDecl {
	p.advance() // 'type'
	if !p.curIs(token.IDENT) {
		p.emitError("WON-SYN-005", "type 别名声明缺少名称", p.cur().Span)
		p.synchronizeToLineEnd()
		return nil
	}
	name := p.cur().Lexeme
	p.advance()
	genParams := p.parseGenericParams()
	p.expect(token.ASSIGN, "WON-SYN-005", "类型别名声明缺少 '='")
	target := p.parseType()
	end := p.prevEnd()
	p.expectNewlineOrEnd("WON-SYN-005", "type 别名声明后期望换行")
	return &ast.TypeAliasDecl{
		IsPub:         isPub,
		Name:          name,
		GenericParams: genParams,
		TargetType:    target,
		Span:          diagnostic.Span{File: p.srcFile, Start: start, End: end},
	}
}

func (p *Parser) parseMod(isPub bool, start diagnostic.Position) *ast.ModuleDecl {
	p.advance() // 'mod'
	if !p.curIs(token.IDENT) {
		p.emitError("WON-SYN-005", "mod 声明缺少模块名", p.cur().Span)
		p.synchronizeToLineEnd()
		return nil
	}
	name := p.cur().Lexeme
	p.advance()
	var decls []ast.Decl
	if p.curIs(token.COLON) {
		p.advance()
		p.expectNewlineOrEnd("WON-SYN-004", "mod ':' 后期望换行")
		p.skipNewlines()
		if !p.curIs(token.INDENT) {
			p.emitError("WON-SYN-004", "mod 块缺少缩进声明体", p.cur().Span)
		} else {
			p.advance()
			for !p.curIs(token.DEDENT) && !p.curIs(token.EOF) {
				p.skipNewlines()
				if p.curIs(token.DEDENT) || p.curIs(token.EOF) {
					break
				}
				if p.curIs(token.IMPORT) || (p.curIs(token.IDENT) && p.cur().Lexeme == "from") {
					if mi := p.parseModuleImport(); mi != nil {
						decls = append(decls, mi)
					}
				} else {
					if d := p.parseItem(); d != nil {
						decls = append(decls, d)
					}
				}
			}
			if p.curIs(token.DEDENT) {
				p.advance()
			}
		}
	} else {
		p.expectNewlineOrEnd("WON-SYN-005", "外部模块声明后期望换行或 ':'")
	}
	return &ast.ModuleDecl{
		IsPub: isPub,
		Name:  name,
		Decls: decls,
		Span:  diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
	}
}

// parseIndentedBlock 期望 ":" NEWLINE INDENT { Statement } DEDENT
func (p *Parser) parseIndentedBlock() *ast.BlockStmt {
	startPos := p.cur().Span.Start
	if p.curIs(token.COLON) {
		p.advance()
	}
	p.expectNewlineOrEnd("WON-SYN-004", "代码块冒号 ':' 后期望换行")
	p.skipNewlines()

	if !p.curIs(token.INDENT) {
		p.emitError("WON-SYN-004", "期望缩进代码块体 (缺少 4 空格缩进)", p.cur().Span)
		return &ast.BlockStmt{
			Statements: make([]ast.Stmt, 0),
			Span:       diagnostic.Span{File: p.srcFile, Start: startPos, End: p.cur().Span.Start},
		}
	}
	p.advance() // 消耗 INDENT

	stmts := make([]ast.Stmt, 0)
	for !p.curIs(token.DEDENT) && !p.curIs(token.EOF) {
		p.skipNewlines()
		if p.curIs(token.DEDENT) || p.curIs(token.EOF) {
			break
		}
		if p.curIs(token.INDENT) {
			p.emitError("WON-SYN-002", "块体内出现未预期的意外缩进", p.cur().Span)
			p.advance()
			continue
		}
		s := p.parseStatement()
		if s != nil {
			stmts = append(stmts, s)
		}
	}

	endPos := p.prevEnd()
	if p.curIs(token.DEDENT) {
		endPos = p.cur().Span.End
		p.advance()
	}
	return &ast.BlockStmt{
		Statements: stmts,
		Span:       diagnostic.Span{File: p.srcFile, Start: startPos, End: endPos},
	}
}

func (p *Parser) parseStatement() ast.Stmt {
	switch p.cur().Kind {
	case token.LET:
		return p.parseBindingStmt(false)
	case token.VAR:
		return p.parseBindingStmt(true)
	case token.RETURN:
		return p.parseReturn()
	case token.BREAK:
		return p.parseBreak()
	case token.CONTINUE:
		return p.parseContinue()
	case token.LBRACE:
		p.emitError("WON-SYN-005", "规范 WON 语法禁止使用花括号 '{' 开启语句块", p.cur().Span)
		p.synchronizeToLineEnd()
		return nil
	case token.RBRACE:
		p.emitError("WON-SYN-005", "发现孤立的非预期闭合花括号 '}'", p.cur().Span)
		p.advance()
		return nil
	}

	// 解析表达式并检查是否为赋值语句或纯表达式语句
	expr := p.parseExpression()
	if expr == nil {
		p.synchronizeToLineEnd()
		return nil
	}

	if p.isAssignOp(p.cur().Kind) {
		op := p.cur().Lexeme
		opSpan := p.cur().Span
		p.advance()

		targetName := ""
		if id, ok := expr.(*ast.Identifier); ok {
			targetName = id.Name
		} else if !isAssignableExpr(expr) {
			p.emitError("WON-SYN-009", fmt.Sprintf("非法的赋值左值目标: 不能对右值表达式执行 '%s' 赋值", op), opSpan)
		}

		val := p.parseExpression()
		if !endsWithBlock(val) {
			p.expectNewlineOrEnd("WON-SYN-005", "赋值语句结尾期望换行")
		}
		return &ast.AssignStmt{
			Target:     targetName,
			TargetExpr: expr,
			Op:         op,
			Value:      val,
			Span:       diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: p.prevEnd()},
		}
	}

	if !endsWithBlock(expr) {
		p.expectNewlineOrEnd("WON-SYN-005", "语句结尾期望换行")
	}
	// If/While/For 语句直接作为顶层 Stmt 返回，保留与 Checker 和 Codegen 的一致视图
	switch s := expr.(type) {
	case *ast.IfStmt:
		return s
	case *ast.WhileStmt:
		return s
	case *ast.ForStmt:
		return s
	}
	return &ast.ExprStmt{
		Expr: expr,
		Span: expr.GetSpan(),
	}
}

func (p *Parser) isAssignOp(k token.Kind) bool {
	switch k {
	case token.ASSIGN, token.PLUSEQ, token.MINUSEQ, token.STAREQ, token.SLASHEQ,
		token.PERCENTEQ, token.AMPEQ, token.PIPEEQ, token.CARETEQ, token.LSHIFTEQ, token.RSHIFTEQ:
		return true
	default:
		return false
	}
}

func isAssignableExpr(e ast.Expr) bool {
	switch u := e.(type) {
	case *ast.Identifier, *ast.FieldAccessExpr, *ast.IndexExpr:
		return true
	case *ast.UnaryExpr:
		return u.Op == "*"
	default:
		return false
	}
}

// parseBindingStmt 解析 let pat [: Type] = init 或 var pat [: Type] = init
func (p *Parser) parseBindingStmt(isVar bool) *ast.LetStmt {
	kwTok := p.advance() // 'let' 或 'var'

	// 检查并明确拦截 Phase 0 遗留语法 "let mut"
	isMut := isVar
	if !isVar && p.curIs(token.MUT) {
		p.emitError("WON-SYN-009", "Phase 0 临时语法 'let mut' 已废弃，请改用规范可变声明关键字 'var'", p.cur().Span)
		isMut = true
		p.advance()
	}

	pat := p.parsePattern()
	name := ""
	if ip, ok := pat.(*ast.IdentPattern); ok {
		name = ip.Name
	} else if _, ok := pat.(*ast.WildcardPattern); ok {
		name = "_"
	}

	var typeStr string
	var typeAST ast.TypeNode
	if p.curIs(token.COLON) {
		p.advance()
		typeAST = p.parseType()
		if typeAST != nil {
			typeStr = typeAST.String()
		}
	}

	if !p.expect(token.ASSIGN, "WON-SYN-009", "变量绑定声明必须包含 '=' 与初始表达式") {
		p.synchronizeToLineEnd()
		return nil
	}

	initExpr := p.parseExpression()
	if !endsWithBlock(initExpr) {
		p.expectNewlineOrEnd("WON-SYN-005", "变量绑定语句结尾期望换行")
	}

	return &ast.LetStmt{
		IsMut:        isMut,
		Keyword:      kwTok.Lexeme,
		Name:         name,
		Pattern:      pat,
		Type:         typeStr,
		TypeAST:      typeAST,
		Init:         initExpr,
		LetTokenSpan: kwTok.Span,
		Span:         diagnostic.Span{File: p.srcFile, Start: kwTok.Span.Start, End: p.prevEnd()},
	}
}

func (p *Parser) parseReturn() *ast.ReturnStmt {
	start := p.cur().Span.Start
	p.advance() // 'return'
	var val ast.Expr
	if !p.curIs(token.NEWLINE) && !p.curIs(token.DEDENT) && !p.curIs(token.EOF) && !p.curIs(token.SEMICOLON) {
		val = p.parseExpression()
	}
	if !endsWithBlock(val) {
		p.expectNewlineOrEnd("WON-SYN-005", "return 语句结尾期望换行")
	}
	return &ast.ReturnStmt{
		Value: val,
		Span:  diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
	}
}

func (p *Parser) parseBreak() *ast.BreakStmt {
	start := p.cur().Span.Start
	p.advance() // 'break'
	var val ast.Expr
	if !p.curIs(token.NEWLINE) && !p.curIs(token.DEDENT) && !p.curIs(token.EOF) {
		val = p.parseExpression()
	}
	p.expectNewlineOrEnd("WON-SYN-005", "break 语句结尾期望换行")
	return &ast.BreakStmt{
		Value: val,
		Span:  diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
	}
}

func (p *Parser) parseContinue() *ast.ContinueStmt {
	tok := p.advance()
	p.expectNewlineOrEnd("WON-SYN-005", "continue 语句结尾期望换行")
	return &ast.ContinueStmt{
		Span: tok.Span,
	}
}

// --- 表达式 Pratt / 优先级爬坡解析 ---

func (p *Parser) parseExpression() ast.Expr {
	return p.parseRange()
}

func (p *Parser) parseHeaderExpression() ast.Expr {
	prev := p.noStructLit
	p.noStructLit = true
	expr := p.parseExpression()
	p.noStructLit = prev
	return expr
}

func (p *Parser) canStartExpr(k token.Kind) bool {
	switch k {
	case token.INT, token.FLOAT, token.TRUE, token.FALSE, token.CHAR, token.BYTE,
		token.STRING, token.RAW_STRING, token.INTERP_STRING,
		token.IDENT, token.SELF, token.SELF_TYPE, token.COLONCOLON,
		token.LPAREN, token.LBRACKET,
		token.MINUS, token.NOT, token.NOT_KW, token.TILDE, token.AMP, token.STAR,
		token.DOTDOT, token.DOTDOTEQ,
		token.IF, token.MATCH, token.WHILE, token.FOR, token.SCOPE, token.SPAWN, token.UNSAFE:
		return true
	default:
		return false
	}
}

func (p *Parser) parseRange() ast.Expr {
	if p.curIs(token.DOTDOT) || p.curIs(token.DOTDOTEQ) {
		opTok := p.advance()
		inclusive := opTok.Kind == token.DOTDOTEQ
		var end ast.Expr
		if p.canStartExpr(p.cur().Kind) {
			end = p.parseLogicalOr()
		} else {
			p.emitError("WON-SYN-009", "前缀范围运算符后期望结束边界表达式", opTok.Span)
		}
		return &ast.RangeExpr{
			Start:     nil,
			End:       end,
			Inclusive: inclusive,
			Span:      diagnostic.Span{File: p.srcFile, Start: opTok.Span.Start, End: p.prevEnd()},
		}
	}

	left := p.parseLogicalOr()
	if p.curIs(token.DOTDOT) || p.curIs(token.DOTDOTEQ) {
		opTok := p.advance()
		inclusive := opTok.Kind == token.DOTDOTEQ
		var right ast.Expr
		if p.canStartExpr(p.cur().Kind) {
			right = p.parseLogicalOr()
		} else if inclusive {
			p.emitError("WON-SYN-009", "闭区间范围运算符 '..=' 后必须包含结束表达式", opTok.Span)
		}
		if p.curIs(token.DOTDOT) || p.curIs(token.DOTDOTEQ) {
			p.emitError("WON-SYN-009", "非法的链式范围表达式", p.cur().Span)
		}
		return &ast.RangeExpr{
			Start:     left,
			End:       right,
			Inclusive: inclusive,
			Span:      diagnostic.Span{File: p.srcFile, Start: left.GetSpan().Start, End: p.prevEnd()},
		}
	}
	return left
}

func (p *Parser) parseLogicalOr() ast.Expr {
	expr := p.parseLogicalAnd()
	for p.curIs(token.OR) {
		op := p.advance().Lexeme
		right := p.parseLogicalAnd()
		expr = &ast.BinaryExpr{
			Left: expr, Op: op, Right: right,
			Span: diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: right.GetSpan().End},
		}
	}
	return expr
}

func (p *Parser) parseLogicalAnd() ast.Expr {
	expr := p.parseBitOr()
	for p.curIs(token.AND) {
		op := p.advance().Lexeme
		right := p.parseBitOr()
		expr = &ast.BinaryExpr{
			Left: expr, Op: op, Right: right,
			Span: diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: right.GetSpan().End},
		}
	}
	return expr
}

func (p *Parser) parseBitOr() ast.Expr {
	expr := p.parseBitXor()
	for p.curIs(token.PIPE) {
		op := p.advance().Lexeme
		right := p.parseBitXor()
		expr = &ast.BinaryExpr{
			Left: expr, Op: op, Right: right,
			Span: diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: right.GetSpan().End},
		}
	}
	return expr
}

func (p *Parser) parseBitXor() ast.Expr {
	expr := p.parseBitAnd()
	for p.curIs(token.CARET) {
		op := p.advance().Lexeme
		right := p.parseBitAnd()
		expr = &ast.BinaryExpr{
			Left: expr, Op: op, Right: right,
			Span: diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: right.GetSpan().End},
		}
	}
	return expr
}

func (p *Parser) parseBitAnd() ast.Expr {
	expr := p.parseEquality()
	for p.curIs(token.AMP) {
		op := p.advance().Lexeme
		right := p.parseEquality()
		expr = &ast.BinaryExpr{
			Left: expr, Op: op, Right: right,
			Span: diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: right.GetSpan().End},
		}
	}
	return expr
}

func (p *Parser) parseEquality() ast.Expr {
	expr := p.parseRelational()
	for p.curIs(token.EQ) || p.curIs(token.NOTEQ) {
		op := p.advance().Lexeme
		right := p.parseRelational()
		expr = &ast.BinaryExpr{
			Left: expr, Op: op, Right: right,
			Span: diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: right.GetSpan().End},
		}
	}
	return expr
}

func (p *Parser) parseRelational() ast.Expr {
	expr := p.parseShift()
	for p.curIs(token.LT) || p.curIs(token.LTEQ) || p.curIs(token.GT) || p.curIs(token.GTEQ) || p.curIs(token.IN) {
		op := p.advance().Lexeme
		right := p.parseShift()
		expr = &ast.BinaryExpr{
			Left: expr, Op: op, Right: right,
			Span: diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: right.GetSpan().End},
		}
	}
	return expr
}

func (p *Parser) parseShift() ast.Expr {
	expr := p.parseAdditive()
	for p.curIs(token.LSHIFT) || p.curIs(token.RSHIFT) {
		op := p.advance().Lexeme
		right := p.parseAdditive()
		expr = &ast.BinaryExpr{
			Left: expr, Op: op, Right: right,
			Span: diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: right.GetSpan().End},
		}
	}
	return expr
}

func (p *Parser) parseAdditive() ast.Expr {
	expr := p.parseMultiplicative()
	for p.curIs(token.PLUS) || p.curIs(token.MINUS) {
		op := p.advance().Lexeme
		right := p.parseMultiplicative()
		expr = &ast.BinaryExpr{
			Left: expr, Op: op, Right: right,
			Span: diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: right.GetSpan().End},
		}
	}
	return expr
}

func (p *Parser) parseMultiplicative() ast.Expr {
	expr := p.parseCast()
	for p.curIs(token.STAR) || p.curIs(token.SLASH) || p.curIs(token.PERCENT) {
		op := p.advance().Lexeme
		right := p.parseCast()
		expr = &ast.BinaryExpr{
			Left: expr, Op: op, Right: right,
			Span: diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: right.GetSpan().End},
		}
	}
	return expr
}

func (p *Parser) parseCast() ast.Expr {
	expr := p.parseUnary()
	for p.curIs(token.AS) {
		p.advance()
		target := p.parseType()
		expr = &ast.CastExpr{
			Expr:       expr,
			TargetType: target,
			Span:       diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: p.prevEnd()},
		}
	}
	return expr
}

func (p *Parser) parseUnary() ast.Expr {
	if p.curIs(token.NOT) || p.curIs(token.NOT_KW) || p.curIs(token.MINUS) || p.curIs(token.TILDE) || p.curIs(token.STAR) {
		tok := p.advance()
		sub := p.parseUnary()
		return &ast.UnaryExpr{
			Op:   tok.Lexeme,
			Expr: sub,
			Span: diagnostic.Span{File: p.srcFile, Start: tok.Span.Start, End: sub.GetSpan().End},
		}
	}

	if p.curIs(token.AMP) {
		start := p.cur().Span.Start
		p.advance()
		isMut := false
		if p.curIs(token.MUT) {
			isMut = true
			p.advance()
		}
		sub := p.parseUnary()
		return &ast.BorrowExpr{
			IsMut: isMut,
			Expr:  sub,
			Span:  diagnostic.Span{File: p.srcFile, Start: start, End: sub.GetSpan().End},
		}
	}

	return p.parsePostfix()
}

// isGenericCallLookahead 检查当前 '[' ... ']' 之后是否紧随 '('，用于消歧函数泛型实参与数组下标访问
func (p *Parser) isGenericCallLookahead() bool {
	if !p.curIs(token.LBRACKET) {
		return false
	}
	depth := 0
	offset := 0
	for {
		tok := p.peekAt(offset)
		if tok.Kind == token.EOF {
			return false
		}
		if tok.Kind == token.LBRACKET {
			depth++
		} else if tok.Kind == token.RBRACKET {
			depth--
			if depth == 0 {
				return p.peekAt(offset+1).Kind == token.LPAREN
			}
		}
		offset++
	}
}

func (p *Parser) parsePostfix() ast.Expr {
	expr := p.parsePrimary()

	for {
		// ? 错误传播后缀
		if p.curIs(token.QUESTION) {
			tok := p.advance()
			expr = &ast.TryExpr{
				Expr: expr,
				Span: diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: tok.Span.End},
			}
			continue
		}

		// 泛型实参调用 func[T](args...) 或普通函数调用 expr(args...)
		if p.isGenericCallLookahead() {
			gArgs := p.parseGenericArgs()
			p.expect(token.LPAREN, "WON-SYN-005", "泛型调用缺少 '('")
			args := p.parseArgList()
			end := p.cur().Span.End
			p.expect(token.RPAREN, "WON-SYN-007", "函数调用缺少闭合 ')'")
			calleeName := ""
			if id, ok := expr.(*ast.Identifier); ok {
				calleeName = id.Name
			}
			expr = &ast.CallExpr{
				Callee:      calleeName,
				CalleeExpr:  expr,
				GenericArgs: gArgs,
				Args:        args,
				Span:        diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: end},
			}
			continue
		}

		if p.curIs(token.LPAREN) {
			p.advance()
			args := p.parseArgList()
			end := p.cur().Span.End
			p.expect(token.RPAREN, "WON-SYN-007", "函数调用缺少闭合 ')'")

			// 兼容两段命名空间调用 zlib::compress(...)
			if path, ok := expr.(*ast.PathExpr); ok && len(path.Segments) == 2 && len(path.GenericArgs) == 0 {
				expr = &ast.NamespacedCallExpr{
					Namespace: path.Segments[0],
					Function:  path.Segments[1],
					Args:      args,
					Span:      diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: end},
				}
			} else {
				calleeName := ""
				if id, ok := expr.(*ast.Identifier); ok {
					calleeName = id.Name
				}
				expr = &ast.CallExpr{
					Callee:     calleeName,
					CalleeExpr: expr,
					Args:       args,
					Span:       diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: end},
				}
			}
			continue
		}

		// 成员访问 .field 或方法调用 .method[T](...) 或作用域派生 s.spawn:
		if p.curIs(token.DOT) {
			p.advance()
			if p.curIs(token.SPAWN) {
				nursery := ""
				if id, ok := expr.(*ast.Identifier); ok {
					nursery = id.Name
				}
				p.advance() // 'spawn'
				if p.curIs(token.COLON) {
					block := p.parseIndentedBlock()
					expr = &ast.SpawnExpr{
						Nursery: nursery,
						Body:    block,
						Span:    diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: block.Span.End},
					}
				} else if p.curIs(token.LPAREN) {
					p.advance()
					arg := p.parseExpression()
					end := p.cur().Span.End
					p.expect(token.RPAREN, "WON-SYN-007", "spawn(...) 缺少闭合 ')'")
					expr = &ast.SpawnExpr{
						Nursery: nursery,
						Arg:     arg,
						Span:    diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: end},
					}
				} else {
					p.emitError("WON-SYN-004", "spawn 后期望 ':' 缩进块或 '(' 表达式", p.cur().Span)
				}
				continue
			}

			if !p.curIs(token.IDENT) && !p.curIs(token.FROM) {
				p.emitError("WON-SYN-005", "'.' 后期望成员或方法名标识符", p.cur().Span)
				break
			}
			member := p.cur().Lexeme
			mEnd := p.cur().Span.End
			p.advance()

			var gArgs []ast.TypeNode
			if p.isGenericCallLookahead() {
				gArgs = p.parseGenericArgs()
			}
			if p.curIs(token.LPAREN) {
				p.advance()
				args := p.parseArgList()
				end := p.cur().Span.End
				p.expect(token.RPAREN, "WON-SYN-007", "方法调用缺少闭合 ')'")
				expr = &ast.MethodCallExpr{
					Receiver:    expr,
					Method:      member,
					GenericArgs: gArgs,
					Args:        args,
					Span:        diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: end},
				}
			} else {
				expr = &ast.FieldAccessExpr{
					Object: expr,
					Field:  member,
					Span:   diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: mEnd},
				}
			}
			continue
		}

		// 下标或切片访问 arr[idx] / arr[a..b]
		if p.curIs(token.LBRACKET) {
			p.advance()
			idxExpr := p.parseExpression()
			end := p.cur().Span.End
			p.expect(token.RBRACKET, "WON-SYN-007", "下标索引缺少闭合 ']'")
			expr = &ast.IndexExpr{
				Object: expr,
				Index:  idxExpr,
				Span:   diagnostic.Span{File: p.srcFile, Start: expr.GetSpan().Start, End: end},
			}
			continue
		}

		break
	}

	return expr
}

func (p *Parser) parsePrimary() ast.Expr {
	tok := p.cur()
	start := tok.Span.Start

	switch tok.Kind {
	case token.INT:
		p.advance()
		val, suffix, _ := lexer.ParseIntLiteralWithSuffix(tok.Lexeme)
		return &ast.IntegerLiteral{Value: val, Raw: tok.Lexeme, Suffix: suffix, Span: tok.Span}

	case token.FLOAT:
		p.advance()
		val, suffix, _ := lexer.ParseFloatLiteralWithSuffix(tok.Lexeme)
		return &ast.FloatLiteral{Value: val, Raw: tok.Lexeme, Suffix: suffix, Span: tok.Span}

	case token.TRUE:
		p.advance()
		return &ast.BoolLiteral{Value: true, Span: tok.Span}

	case token.FALSE:
		p.advance()
		return &ast.BoolLiteral{Value: false, Span: tok.Span}

	case token.CHAR:
		p.advance()
		var r rune
		for _, ch := range tok.Lexeme {
			r = ch
			break
		}
		return &ast.CharLiteral{Value: r, Raw: tok.Lexeme, Span: tok.Span}

	case token.BYTE:
		p.advance()
		var b byte
		if len(tok.Lexeme) > 0 {
			b = tok.Lexeme[0]
		}
		return &ast.ByteLiteral{Value: b, Raw: tok.Lexeme, Span: tok.Span}

	case token.STRING:
		p.advance()
		return &ast.StringLiteral{Value: tok.Lexeme, IsRaw: false, Span: tok.Span}

	case token.RAW_STRING:
		p.advance()
		return &ast.StringLiteral{Value: tok.Lexeme, IsRaw: true, Span: tok.Span}

	case token.INTERP_STRING:
		p.advance()
		return p.parseInterpolatedString(tok)

	case token.LPAREN:
		return p.parseTupleOrParen()

	case token.LBRACKET:
		return p.parseArrayLiteral()

	case token.IF:
		return p.parseIf()

	case token.WHILE:
		return p.parseWhile()

	case token.FOR:
		return p.parseFor()

	case token.MATCH:
		return p.parseMatch()

	case token.SCOPE:
		return p.parseScope()

	case token.SPAWN:
		p.advance()
		if p.curIs(token.COLON) {
			block := p.parseIndentedBlock()
			return &ast.SpawnExpr{
				Body: block,
				Span: diagnostic.Span{File: p.srcFile, Start: start, End: block.Span.End},
			}
		}
		p.expect(token.LPAREN, "WON-SYN-004", "spawn 后期望 ':' 缩进块或 '('")
		arg := p.parseExpression()
		end := p.cur().Span.End
		p.expect(token.RPAREN, "WON-SYN-007", "spawn(...) 缺少闭合 ')'")
		return &ast.SpawnExpr{
			Arg:  arg,
			Span: diagnostic.Span{File: p.srcFile, Start: start, End: end},
		}

	case token.UNSAFE:
		p.advance()
		if p.curIs(token.LBRACE) {
			p.emitError("WON-SYN-005", "unsafe 块必须采用规范语法 'unsafe:' 后接缩进体，禁止使用 '{ ... }'", p.cur().Span)
			p.synchronizePastLegacyBraces()
			return &ast.Identifier{Name: "unsafe_err", Span: tok.Span}
		}
		if !p.expect(token.COLON, "WON-SYN-004", "unsafe 关键字后必须紧跟冒号 ':'") {
			return &ast.Identifier{Name: "unsafe_err", Span: tok.Span}
		}
		block := p.parseIndentedBlock()
		return &ast.UnsafeExpr{
			Block: block,
			Span:  diagnostic.Span{File: p.srcFile, Start: start, End: block.Span.End},
		}

	case token.IDENT, token.SELF, token.SELF_TYPE, token.COLONCOLON:
		return p.parsePathOrStructOrIdent()

	default:
		p.emitError("WON-SYN-006", fmt.Sprintf("期望表达式，实际遇到非法标记 %s (%q)", tok.Kind, tok.Lexeme), tok.Span)
		p.advance()
		return &ast.Identifier{Name: "err", Span: tok.Span}
	}
}

func (p *Parser) parseInterpolatedString(tok token.Token) ast.Expr {
	raw := tok.Lexeme
	parts := make([]ast.InterpPart, 0)
	var litBuf strings.Builder

	i := 0
	for i < len(raw) {
		if raw[i] == '\\' && i+1 < len(raw) && (raw[i+1] == '{' || raw[i+1] == '}') {
			litBuf.WriteByte(raw[i+1])
			i += 2
			continue
		}
		if raw[i] == '{' {
			if litBuf.Len() > 0 {
				parts = append(parts, ast.InterpPart{IsExpr: false, Literal: litBuf.String()})
				litBuf.Reset()
			}
			i++ // 跳过 '{'
			exprStart := i
			depth := 1
			inStr := false
			colonIdx := -1
			for i < len(raw) && depth > 0 {
				ch := raw[i]
				if ch == '"' {
					inStr = !inStr
				} else if !inStr {
					if ch == '{' {
						depth++
					} else if ch == '}' {
						depth--
						if depth == 0 {
							break
						}
					} else if ch == ':' && depth == 1 && colonIdx == -1 {
						// 避免将命名空间 '::' 误判为格式说明符 ':'
						if i+1 < len(raw) && raw[i+1] == ':' {
							i += 2
							continue
						}
						if i > 0 && raw[i-1] == ':' {
							i++
							continue
						}
						colonIdx = i
					}
				} else if ch == '\\' && i+1 < len(raw) {
					i++
				}
				i++
			}
			exprText := raw[exprStart:i]
			fmtSpec := ""
			if colonIdx != -1 && colonIdx >= exprStart {
				exprText = raw[exprStart:colonIdx]
				fmtSpec = raw[colonIdx+1 : i]
			}
			if strings.TrimSpace(exprText) == "" {
				p.emitError("WON-SYN-006", "字符串插值 '{ }' 内部缺少有效表达式", tok.Span)
			} else {
				subLex := lexer.NewSubLexer(p.srcFile, exprText, tok.Span.Start, p.report)
				subParser := New(subLex, p.report, p.srcFile)
				subExpr := subParser.parseExpression()
				if !subParser.curIs(token.EOF) {
					p.emitError("WON-SYN-005", "字符串插值表达式包含多余的未消费标记", subParser.cur().Span)
				}
				parts = append(parts, ast.InterpPart{
					IsExpr:     true,
					Expr:       subExpr,
					FormatSpec: fmtSpec,
				})
			}
			if i < len(raw) && raw[i] == '}' {
				i++
			}
			continue
		}
		litBuf.WriteByte(raw[i])
		i++
	}
	if litBuf.Len() > 0 {
		parts = append(parts, ast.InterpPart{IsExpr: false, Literal: litBuf.String()})
	}

	return &ast.InterpolatedStringExpr{
		Parts: parts,
		Raw:   raw,
		Span:  tok.Span,
	}
}

func (p *Parser) parseTupleOrParen() ast.Expr {
	start := p.cur().Span.Start
	p.advance() // '('
	if p.curIs(token.RPAREN) {
		end := p.cur().Span.End
		p.advance()
		return &ast.TupleLiteral{Elems: nil, Span: diagnostic.Span{File: p.srcFile, Start: start, End: end}}
	}
	first := p.parseExpression()
	if p.curIs(token.COMMA) {
		elems := []ast.Expr{first}
		for p.curIs(token.COMMA) {
			p.advance()
			if p.curIs(token.RPAREN) {
				break
			}
			elems = append(elems, p.parseExpression())
		}
		end := p.cur().Span.End
		p.expect(token.RPAREN, "WON-SYN-007", "元组字面量缺少闭合 ')'")
		return &ast.TupleLiteral{Elems: elems, Span: diagnostic.Span{File: p.srcFile, Start: start, End: end}}
	}
	p.expect(token.RPAREN, "WON-SYN-007", "分组表达式缺少闭合 ')'")
	return first
}

func (p *Parser) parseArrayLiteral() ast.Expr {
	start := p.cur().Span.Start
	p.advance() // '['
	if p.curIs(token.RBRACKET) {
		end := p.cur().Span.End
		p.advance()
		return &ast.ArrayLiteral{Elems: nil, Span: diagnostic.Span{File: p.srcFile, Start: start, End: end}}
	}

	first := p.parseExpression()
	if p.curIs(token.SEMICOLON) {
		p.advance()
		countExpr := p.parseExpression()
		var countVal int64
		if il, ok := countExpr.(*ast.IntegerLiteral); ok {
			countVal = il.Value
		}
		end := p.cur().Span.End
		p.expect(token.RBRACKET, "WON-SYN-007", "数组重复填充字面量缺少闭合 ']'")
		return &ast.ArrayRepeatLiteral{
			Elem:      first,
			Count:     countVal,
			CountExpr: countExpr,
			Span:      diagnostic.Span{File: p.srcFile, Start: start, End: end},
		}
	}

	elems := []ast.Expr{first}
	for p.curIs(token.COMMA) {
		p.advance()
		if p.curIs(token.RBRACKET) {
			break
		}
		elems = append(elems, p.parseExpression())
	}
	end := p.cur().Span.End
	p.expect(token.RBRACKET, "WON-SYN-007", "数组字面量缺少闭合 ']'")
	return &ast.ArrayLiteral{
		Elems: elems,
		Span:  diagnostic.Span{File: p.srcFile, Start: start, End: end},
	}
}

func (p *Parser) parsePathOrStructOrIdent() ast.Expr {
	start := p.cur().Span.Start
	segments := make([]string, 0, 2)
	if p.curIs(token.COLONCOLON) {
		p.advance()
	}
	if !p.curIs(token.IDENT) && !p.curIs(token.SELF) && !p.curIs(token.SELF_TYPE) {
		p.emitError("WON-SYN-005", "路径表达式期望标识符", p.cur().Span)
		return &ast.Identifier{Name: "err", Span: p.cur().Span}
	}
	segments = append(segments, p.cur().Lexeme)
	p.advance()

	for p.curIs(token.COLONCOLON) {
		p.advance()
		if !p.curIs(token.IDENT) && !p.curIs(token.SELF_TYPE) && !p.curIs(token.FROM) {
			p.emitError("WON-SYN-005", "'::' 后期望路径段标识符", p.cur().Span)
			break
		}
		segments = append(segments, p.cur().Lexeme)
		p.advance()
	}

	var genArgs []ast.TypeNode
	if p.isGenericCallLookahead() {
		genArgs = p.parseGenericArgs()
	}

	// 检查结构体构造字面量: Path { field: expr, ... }
	if !p.noStructLit && p.curIs(token.LBRACE) && len(genArgs) == 0 {
		p.advance() // '{'
		fields := make([]*ast.FieldInit, 0)
		for !p.curIs(token.RBRACE) && !p.curIs(token.EOF) {
			fStart := p.cur().Span.Start
			if !p.curIs(token.IDENT) {
				p.emitError("WON-SYN-005", "结构体初始化列表期望字段名标识符", p.cur().Span)
				break
			}
			fName := p.cur().Lexeme
			p.advance()
			var fVal ast.Expr
			if p.curIs(token.COLON) {
				p.advance()
				fVal = p.parseExpression()
			}
			fields = append(fields, &ast.FieldInit{
				Name:  fName,
				Value: fVal,
				Span:  diagnostic.Span{File: p.srcFile, Start: fStart, End: p.prevEnd()},
			})
			if p.curIs(token.COMMA) {
				p.advance()
			} else {
				break
			}
		}
		end := p.cur().Span.End
		p.expect(token.RBRACE, "WON-SYN-007", "结构体构造字面量缺少闭合 '}'")
		return &ast.StructLiteral{
			TypePath: segments,
			Fields:   fields,
			Span:     diagnostic.Span{File: p.srcFile, Start: start, End: end},
		}
	}

	end := p.prevEnd()
	if len(segments) == 1 && len(genArgs) == 0 {
		return &ast.Identifier{
			Name: segments[0],
			Span: diagnostic.Span{File: p.srcFile, Start: start, End: end},
		}
	}
	return &ast.PathExpr{
		Segments:    segments,
		GenericArgs: genArgs,
		Span:        diagnostic.Span{File: p.srcFile, Start: start, End: end},
	}
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

// --- 控制流语句与表达式 ---

func (p *Parser) parseIf() *ast.IfStmt {
	start := p.cur().Span.Start
	p.advance() // 'if'
	cond := p.parseHeaderExpression()

	if p.curIs(token.LBRACE) {
		p.emitError("WON-SYN-005", "if 条件后必须紧跟冒号 ':' 与缩进代码块，严禁使用花括号 '{'", p.cur().Span)
		p.synchronizePastLegacyBraces()
		return &ast.IfStmt{Cond: cond, Then: &ast.BlockStmt{}, Span: diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()}}
	}

	if !p.expect(token.COLON, "WON-SYN-004", "if 条件后期望冒号 ':'") {
		p.synchronizeToLineEnd()
		return &ast.IfStmt{Cond: cond, Then: &ast.BlockStmt{}, Span: diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()}}
	}
	thenBlock := p.parseIndentedBlock()

	var elseStmt ast.Stmt
	if p.curIs(token.ELIF) {
		elseStmt = p.parseElif()
	} else if p.curIs(token.ELSE) {
		p.advance()
		if p.curIs(token.LBRACE) {
			p.emitError("WON-SYN-005", "else 后必须紧跟冒号 ':' 与缩进代码块，严禁使用花括号 '{'", p.cur().Span)
			p.synchronizePastLegacyBraces()
		} else if p.expect(token.COLON, "WON-SYN-004", "else 后期望冒号 ':'") {
			elseStmt = p.parseIndentedBlock()
		}
	}

	return &ast.IfStmt{
		Cond: cond,
		Then: thenBlock,
		Else: elseStmt,
		Span: diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
	}
}

func (p *Parser) parseElif() *ast.IfStmt {
	start := p.cur().Span.Start
	p.advance() // 'elif'
	cond := p.parseHeaderExpression()
	if !p.expect(token.COLON, "WON-SYN-004", "elif 条件后期望冒号 ':'") {
		p.synchronizeToLineEnd()
		return &ast.IfStmt{Cond: cond, Then: &ast.BlockStmt{}, Span: diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()}}
	}
	thenBlock := p.parseIndentedBlock()

	var elseStmt ast.Stmt
	if p.curIs(token.ELIF) {
		elseStmt = p.parseElif()
	} else if p.curIs(token.ELSE) {
		p.advance()
		if p.expect(token.COLON, "WON-SYN-004", "else 后期望冒号 ':'") {
			elseStmt = p.parseIndentedBlock()
		}
	}

	return &ast.IfStmt{
		Cond: cond,
		Then: thenBlock,
		Else: elseStmt,
		Span: diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
	}
}

func (p *Parser) parseWhile() *ast.WhileStmt {
	start := p.cur().Span.Start
	p.advance() // 'while'
	cond := p.parseHeaderExpression()

	if p.curIs(token.LBRACE) {
		p.emitError("WON-SYN-005", "while 条件后必须紧跟冒号 ':' 与缩进代码块，严禁使用花括号 '{'", p.cur().Span)
		p.synchronizePastLegacyBraces()
		return &ast.WhileStmt{Cond: cond, Body: &ast.BlockStmt{}, Span: diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()}}
	}

	if !p.expect(token.COLON, "WON-SYN-004", "while 条件后期望冒号 ':'") {
		p.synchronizeToLineEnd()
		return &ast.WhileStmt{Cond: cond, Body: &ast.BlockStmt{}, Span: diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()}}
	}
	body := p.parseIndentedBlock()
	return &ast.WhileStmt{
		Cond: cond,
		Body: body,
		Span: diagnostic.Span{File: p.srcFile, Start: start, End: body.Span.End},
	}
}

func (p *Parser) parseFor() *ast.ForStmt {
	start := p.cur().Span.Start
	p.advance() // 'for'
	pat := p.parsePattern()
	p.expect(token.IN, "WON-SYN-005", "for 循环模式后期望关键字 'in'")
	iter := p.parseHeaderExpression()
	if !p.expect(token.COLON, "WON-SYN-004", "for 循环头后期望冒号 ':'") {
		p.synchronizeToLineEnd()
		return &ast.ForStmt{Pattern: pat, Iter: iter, Body: &ast.BlockStmt{}, Span: diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()}}
	}
	body := p.parseIndentedBlock()
	return &ast.ForStmt{
		Pattern: pat,
		Iter:    iter,
		Body:    body,
		Span:    diagnostic.Span{File: p.srcFile, Start: start, End: body.Span.End},
	}
}

func (p *Parser) parseMatch() *ast.MatchExpr {
	start := p.cur().Span.Start
	p.advance() // 'match'
	subject := p.parseHeaderExpression()

	if !p.expect(token.COLON, "WON-SYN-004", "match 表达式后期望冒号 ':'") {
		p.synchronizeToLineEnd()
		return &ast.MatchExpr{Subject: subject, Span: diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()}}
	}
	p.expectNewlineOrEnd("WON-SYN-004", "match ':' 后期望换行")
	p.skipNewlines()

	arms := make([]*ast.MatchArm, 0)
	if !p.curIs(token.INDENT) {
		p.emitError("WON-SYN-004", "match 表达式缺少缩进匹配分支", p.cur().Span)
	} else {
		p.advance()
		for !p.curIs(token.DEDENT) && !p.curIs(token.EOF) {
			p.skipNewlines()
			if p.curIs(token.DEDENT) || p.curIs(token.EOF) {
				break
			}
			aStart := p.cur().Span.Start
			pat := p.parsePattern()
			var guard ast.Expr
			if p.curIs(token.IF) {
				p.advance()
				guard = p.parseExpression()
			}
			p.expect(token.FATARROW, "WON-SYN-005", "match 分支模式后期望 '=>'")

			var armBody *ast.BlockStmt
			if p.curIs(token.NEWLINE) {
				armBody = p.parseIndentedBlock()
			} else {
				armExpr := p.parseExpression()
				p.expectNewlineOrEnd("WON-SYN-005", "单行 match 分支末尾期望换行")
				armBody = &ast.BlockStmt{
					Statements: []ast.Stmt{&ast.ExprStmt{Expr: armExpr, Span: armExpr.GetSpan()}},
					Span:       armExpr.GetSpan(),
				}
			}

			arms = append(arms, &ast.MatchArm{
				Pattern: pat,
				Guard:   guard,
				Body:    armBody,
				Span:    diagnostic.Span{File: p.srcFile, Start: aStart, End: armBody.Span.End},
			})
		}
		if p.curIs(token.DEDENT) {
			p.advance()
		}
	}

	return &ast.MatchExpr{
		Subject: subject,
		Arms:    arms,
		Span:    diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
	}
}

func (p *Parser) parseScope() *ast.ScopeExpr {
	start := p.cur().Span.Start
	p.advance() // 'scope'
	name := ""
	if p.curIs(token.IDENT) {
		name = p.cur().Lexeme
		p.advance()
	}
	if !p.expect(token.COLON, "WON-SYN-004", "scope 声明后期望冒号 ':'") {
		p.synchronizeToLineEnd()
		return &ast.ScopeExpr{Name: name, Body: &ast.BlockStmt{}, Span: diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()}}
	}
	body := p.parseIndentedBlock()
	return &ast.ScopeExpr{
		Name: name,
		Body: body,
		Span: diagnostic.Span{File: p.srcFile, Start: start, End: body.Span.End},
	}
}

// --- 模式匹配解析 ---

func (p *Parser) parsePattern() ast.Pattern {
	tok := p.cur()
	start := tok.Span.Start

	// 字面量模式
	if tok.Kind == token.INT || tok.Kind == token.FLOAT || tok.Kind == token.STRING ||
		tok.Kind == token.CHAR || tok.Kind == token.BYTE || tok.Kind == token.TRUE || tok.Kind == token.FALSE {
		lit := p.parsePrimary()
		return &ast.LiteralPattern{Literal: lit, Span: lit.GetSpan()}
	}
	if tok.Kind == token.MINUS && (p.peekIs(token.INT) || p.peekIs(token.FLOAT)) {
		p.advance()
		lit := p.parsePrimary()
		return &ast.LiteralPattern{
			Literal: &ast.UnaryExpr{Op: "-", Expr: lit, Span: diagnostic.Span{File: p.srcFile, Start: start, End: lit.GetSpan().End}},
			Span:    diagnostic.Span{File: p.srcFile, Start: start, End: lit.GetSpan().End},
		}
	}

	// 元组模式 (p1, p2)
	if tok.Kind == token.LPAREN {
		p.advance()
		elems := make([]ast.Pattern, 0)
		for !p.curIs(token.RPAREN) && !p.curIs(token.EOF) {
			elems = append(elems, p.parsePattern())
			if p.curIs(token.COMMA) {
				p.advance()
			} else {
				break
			}
		}
		end := p.cur().Span.End
		p.expect(token.RPAREN, "WON-SYN-007", "元组解构模式缺少闭合 ')'")
		return &ast.TuplePattern{Elems: elems, Span: diagnostic.Span{File: p.srcFile, Start: start, End: end}}
	}

	// 切片模式 [p1, p2]
	if tok.Kind == token.LBRACKET {
		p.advance()
		elems := make([]ast.Pattern, 0)
		for !p.curIs(token.RBRACKET) && !p.curIs(token.EOF) {
			elems = append(elems, p.parsePattern())
			if p.curIs(token.COMMA) {
				p.advance()
			} else {
				break
			}
		}
		end := p.cur().Span.End
		p.expect(token.RBRACKET, "WON-SYN-007", "切片解构模式缺少闭合 ']'")
		return &ast.SlicePattern{Elems: elems, Span: diagnostic.Span{File: p.srcFile, Start: start, End: end}}
	}

	// 变量绑定模式 [ref] [mut] ident 或 枚举/结构体路径模式
	isRef := false
	isMut := false
	if p.curIs(token.REF) {
		isRef = true
		p.advance()
	}
	if p.curIs(token.MUT) {
		isMut = true
		p.advance()
	}

	if !p.curIs(token.IDENT) && !p.curIs(token.SELF_TYPE) {
		p.emitError("WON-SYN-005", fmt.Sprintf("模式匹配期望合法模式或标识符，实际遇到 %q", p.cur().Lexeme), p.cur().Span)
		p.advance()
		return &ast.WildcardPattern{Span: tok.Span}
	}

	first := p.cur().Lexeme
	p.advance()

	if first == "_" && !isRef && !isMut && !p.curIs(token.COLONCOLON) {
		return &ast.WildcardPattern{Span: tok.Span}
	}

	if !isRef && !isMut && p.curIs(token.COLONCOLON) {
		segments := []string{first}
		for p.curIs(token.COLONCOLON) {
			p.advance()
			if !p.curIs(token.IDENT) {
				p.emitError("WON-SYN-005", "路径模式 '::' 后期望标识符", p.cur().Span)
				break
			}
			segments = append(segments, p.cur().Lexeme)
			p.advance()
		}

		// EnumPattern Path(pat1, pat2)
		if p.curIs(token.LPAREN) {
			p.advance()
			args := make([]ast.Pattern, 0)
			for !p.curIs(token.RPAREN) && !p.curIs(token.EOF) {
				args = append(args, p.parsePattern())
				if p.curIs(token.COMMA) {
					p.advance()
				} else {
					break
				}
			}
			end := p.cur().Span.End
			p.expect(token.RPAREN, "WON-SYN-007", "枚举模式缺少闭合 ')'")
			return &ast.EnumPattern{Path: segments, Args: args, Span: diagnostic.Span{File: p.srcFile, Start: start, End: end}}
		}

		// StructPattern Path { f1, f2: pat }
		if p.curIs(token.LBRACE) {
			p.advance()
			fields := make([]*ast.FieldPattern, 0)
			for !p.curIs(token.RBRACE) && !p.curIs(token.EOF) {
				fStart := p.cur().Span.Start
				if !p.curIs(token.IDENT) {
					p.emitError("WON-SYN-005", "结构体模式期望字段名", p.cur().Span)
					break
				}
				fName := p.cur().Lexeme
				p.advance()
				var subPat ast.Pattern
				if p.curIs(token.COLON) {
					p.advance()
					subPat = p.parsePattern()
				}
				fields = append(fields, &ast.FieldPattern{
					Name:    fName,
					Pattern: subPat,
					Span:    diagnostic.Span{File: p.srcFile, Start: fStart, End: p.prevEnd()},
				})
				if p.curIs(token.COMMA) {
					p.advance()
				} else {
					break
				}
			}
			end := p.cur().Span.End
			p.expect(token.RBRACE, "WON-SYN-007", "结构体解构模式缺少闭合 '}'")
			return &ast.StructPattern{Path: segments, Fields: fields, Span: diagnostic.Span{File: p.srcFile, Start: start, End: end}}
		}

		return &ast.EnumPattern{Path: segments, Span: diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()}}
	}

	// 简单变体直接携带括号，如 Some(x) 或 Circle(r)
	if !isRef && !isMut && p.curIs(token.LPAREN) {
		p.advance()
		args := make([]ast.Pattern, 0)
		for !p.curIs(token.RPAREN) && !p.curIs(token.EOF) {
			args = append(args, p.parsePattern())
			if p.curIs(token.COMMA) {
				p.advance()
			} else {
				break
			}
		}
		end := p.cur().Span.End
		p.expect(token.RPAREN, "WON-SYN-007", "枚举模式缺少闭合 ')'")
		return &ast.EnumPattern{Path: []string{first}, Args: args, Span: diagnostic.Span{File: p.srcFile, Start: start, End: end}}
	}

	// 简单结构体模式 Point { x, y }
	if !isRef && !isMut && p.curIs(token.LBRACE) {
		p.advance()
		fields := make([]*ast.FieldPattern, 0)
		for !p.curIs(token.RBRACE) && !p.curIs(token.EOF) {
			fStart := p.cur().Span.Start
			if !p.curIs(token.IDENT) {
				break
			}
			fName := p.cur().Lexeme
			p.advance()
			var subPat ast.Pattern
			if p.curIs(token.COLON) {
				p.advance()
				subPat = p.parsePattern()
			}
			fields = append(fields, &ast.FieldPattern{Name: fName, Pattern: subPat, Span: diagnostic.Span{File: p.srcFile, Start: fStart, End: p.prevEnd()}})
			if p.curIs(token.COMMA) {
				p.advance()
			} else {
				break
			}
		}
		end := p.cur().Span.End
		p.expect(token.RBRACE, "WON-SYN-007", "结构体解构模式缺少闭合 '}'")
		return &ast.StructPattern{Path: []string{first}, Fields: fields, Span: diagnostic.Span{File: p.srcFile, Start: start, End: end}}
	}

	return &ast.IdentPattern{
		IsRef: isRef,
		IsMut: isMut,
		Name:  first,
		Span:  diagnostic.Span{File: p.srcFile, Start: start, End: p.prevEnd()},
	}
}

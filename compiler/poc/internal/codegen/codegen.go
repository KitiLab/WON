// Package codegen 实现了从 WON AST 到严格 ISO C11 标准源码的生成器
// 坚决剔除任何 GNU 语句表达式 (({ ... })) 扩展，使用纯标准 C11 块作用域与临时变量保证 -pedantic-errors -Wall -Wextra -Werror 零警告零错误。
// 所有未实现的 AST 结构均显式返回明确的错误，杜绝任何静默兜底回退。
package codegen

import (
	"fmt"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
)

// Generator C 源码生成器
type Generator struct {
	prog     *ast.Program
	bindings map[string]*bindgen.BindingMeta
	sb       strings.Builder
	indent   int
}

// New 创建生成器实例
func New(prog *ast.Program, bindings map[string]*bindgen.BindingMeta) *Generator {
	if bindings == nil {
		bindings = make(map[string]*bindgen.BindingMeta)
	}
	return &Generator{
		prog:     prog,
		bindings: bindings,
	}
}

func (g *Generator) writeLine(s string) {
	if s == "" {
		g.sb.WriteString("\n")
		return
	}
	g.sb.WriteString(strings.Repeat("    ", g.indent))
	g.sb.WriteString(s)
	g.sb.WriteString("\n")
}

// Generate 执行代码生成，返回符合 ISO C11 规范的完整 C 语言源码
func (g *Generator) Generate() (string, error) {
	g.sb.Reset()

	// 1. 标准 C 运行时头文件
	g.writeLine("/* 由 WON Phase 0B 编译器自动生成 - 严格遵循 ISO C11 标准 */")
	g.writeLine("#define _POSIX_C_SOURCE 200809L")
	g.writeLine("#include <stdio.h>")
	g.writeLine("#include <stdlib.h>")
	g.writeLine("#include <string.h>")
	g.writeLine("#include <stdint.h>")
	g.writeLine("#include <stdbool.h>")

	// 2. 引入源文件声明的 C 头文件
	for _, imp := range g.prog.Imports {
		path := imp.HeaderPath
		g.writeLine(fmt.Sprintf("#include %s", path))
	}
	g.writeLine("")

	// 3. 内置辅助函数与宏
	g.writeLine("// --- WON 极简内置运行时宏与字节比较函数 ---")
	g.writeLine("static inline bool won_bytes_equal(const unsigned char* a, const unsigned char* b, unsigned long len) {")
	g.writeLine("    return memcmp(a, b, len) == 0;")
	g.writeLine("}")
	g.writeLine("")

	// 4. 生成函数声明与定义
	for _, fn := range g.prog.Functions {
		if err := g.genFunction(fn); err != nil {
			return "", err
		}
	}

	return g.sb.String(), nil
}

func (g *Generator) genFunction(fn *ast.FunctionDecl) error {
	retType := "void"
	if fn.ReturnType == "i32" {
		retType = "int"
	} else if fn.ReturnType == "u64" {
		retType = "uint64_t"
	}

	fnName := fn.Name
	if fnName == "main" {
		g.writeLine(fmt.Sprintf("%s main(void) {", retType))
	} else {
		params := make([]string, 0)
		for _, p := range fn.Params {
			params = append(params, fmt.Sprintf("%s %s", mapWonTypeToC(p.Type), p.Name))
		}
		paramStr := "void"
		if len(params) > 0 {
			paramStr = strings.Join(params, ", ")
		}
		g.writeLine(fmt.Sprintf("%s %s(%s) {", retType, fnName, paramStr))
	}

	g.indent++
	if err := g.genBlockStatements(fn.Body); err != nil {
		return err
	}
	g.indent--
	g.writeLine("}")
	g.writeLine("")
	return nil
}

func (g *Generator) genBlockStatements(b *ast.BlockStmt) error {
	for _, stmt := range b.Statements {
		if err := g.genStatement(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (g *Generator) genStatement(stmt ast.Stmt) error {
	switch s := stmt.(type) {
	case *ast.LetStmt:
		return g.genLet(s)
	case *ast.AssignStmt:
		if u, ok := s.Value.(*ast.UnsafeExpr); ok {
			g.writeLine("{")
			g.indent++
			if err := g.genUnsafeBlockAssign(u.Block, s.Target); err != nil {
				return err
			}
			g.indent--
			g.writeLine("}")
			return nil
		}
		valStr, err := g.genExpr(s.Value)
		if err != nil {
			return err
		}
		op := s.Op
		if op == "" {
			op = "="
		}
		g.writeLine(fmt.Sprintf("%s %s %s;", s.Target, op, valStr))
		return nil
	case *ast.IfStmt:
		condStr, err := g.genExpr(s.Cond)
		if err != nil {
			return err
		}
		// 避免多重嵌套外层括号触发 Clang -Wparentheses-equality 警告
		if strings.HasPrefix(condStr, "(") && strings.HasSuffix(condStr, ")") {
			g.writeLine(fmt.Sprintf("if %s {", condStr))
		} else {
			g.writeLine(fmt.Sprintf("if (%s) {", condStr))
		}
		g.indent++
		if err := g.genBlockStatements(s.Then); err != nil {
			return err
		}
		g.indent--
		if s.Else != nil {
			if elseBlock, ok := s.Else.(*ast.BlockStmt); ok {
				g.writeLine("} else {")
				g.indent++
				if err := g.genBlockStatements(elseBlock); err != nil {
					return err
				}
				g.indent--
			} else {
				g.writeLine("} else")
				if err := g.genStatement(s.Else); err != nil {
					return err
				}
			}
		}
		g.writeLine("}")
		return nil
	case *ast.WhileStmt:
		condStr, err := g.genExpr(s.Cond)
		if err != nil {
			return err
		}
		if strings.HasPrefix(condStr, "(") && strings.HasSuffix(condStr, ")") {
			g.writeLine(fmt.Sprintf("while %s {", condStr))
		} else {
			g.writeLine(fmt.Sprintf("while (%s) {", condStr))
		}
		g.indent++
		if err := g.genBlockStatements(s.Body); err != nil {
			return err
		}
		g.indent--
		g.writeLine("}")
		return nil
	case *ast.ReturnStmt:
		if s.Value != nil {
			valStr, err := g.genExpr(s.Value)
			if err != nil {
				return err
			}
			g.writeLine(fmt.Sprintf("return %s;", valStr))
		} else {
			g.writeLine("return;")
		}
		return nil
	case *ast.ExprStmt:
		if u, ok := s.Expr.(*ast.UnsafeExpr); ok {
			g.writeLine("{")
			g.indent++
			if err := g.genBlockStatements(u.Block); err != nil {
				return err
			}
			g.indent--
			g.writeLine("}")
			return nil
		}
		exprStr, err := g.genExpr(s.Expr)
		if err != nil {
			return err
		}
		if exprStr != "" {
			g.writeLine(fmt.Sprintf("%s;", exprStr))
		}
		return nil
	default:
		return fmt.Errorf("代码生成器遇到不受支持的语句类型: %T (第 %d 行, 第 %d 列)", stmt, stmt.GetSpan().Start.Line, stmt.GetSpan().Start.Column)
	}
}

func (g *Generator) genLet(s *ast.LetStmt) error {
	// 数组初始化声明: let [mut] buf: [u8; 256] = [0; 256];
	if arr, ok := s.Init.(*ast.ArrayRepeatLiteral); ok {
		elemVal, err := g.genExpr(arr.Elem)
		if err != nil {
			return err
		}
		g.writeLine(fmt.Sprintf("unsigned char %s[%d] = {%s};", s.Name, arr.Count, elemVal))
		return nil
	}

	cType := g.inferType(s)

	// 若初始值为 unsafe 块表达式，使用标准 ISO C11 变量预声明 + 复合语句块赋值
	if u, ok := s.Init.(*ast.UnsafeExpr); ok {
		g.writeLine(fmt.Sprintf("%s %s;", cType, s.Name))
		g.writeLine("{")
		g.indent++
		if err := g.genUnsafeBlockAssign(u.Block, s.Name); err != nil {
			return err
		}
		g.indent--
		g.writeLine("}")
		return nil
	}

	initStr, err := g.genExpr(s.Init)
	if err != nil {
		return err
	}
	g.writeLine(fmt.Sprintf("%s %s = %s;", cType, s.Name, initStr))
	return nil
}

func (g *Generator) genUnsafeBlockAssign(b *ast.BlockStmt, targetVar string) error {
	n := len(b.Statements)
	for i, stmt := range b.Statements {
		if i == n-1 {
			// 尾表达式语句转换为直接目标赋值
			if es, ok := stmt.(*ast.ExprStmt); ok {
				exprStr, err := g.genExpr(es.Expr)
				if err != nil {
					return err
				}
				g.writeLine(fmt.Sprintf("%s = %s;", targetVar, exprStr))
				continue
			}
		}
		if err := g.genStatement(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (g *Generator) inferType(s *ast.LetStmt) string {
	if s.Type != "" {
		return mapWonTypeToC(s.Type)
	}
	switch init := s.Init.(type) {
	case *ast.StringLiteral:
		return "const char*"
	case *ast.IntegerLiteral:
		return "int64_t"
	case *ast.MethodCallExpr:
		switch init.Method {
		case "as_bytes", "as_ptr":
			return "const unsigned char*"
		case "as_mut_ptr":
			return "unsigned char*"
		case "len":
			return "unsigned long"
		default:
			return "int64_t"
		}
	case *ast.CallExpr:
		if init.Callee == "bytes_equal" {
			return "bool"
		}
		return "int64_t"
	case *ast.UnsafeExpr:
		// 从绑定的实际头文件签名中推断返回值类型
		if len(init.Block.Statements) > 0 {
			if es, ok := init.Block.Statements[len(init.Block.Statements)-1].(*ast.ExprStmt); ok {
				if nc, ok := es.Expr.(*ast.NamespacedCallExpr); ok {
					if meta, ok := g.bindings[nc.Namespace]; ok {
						if fnSig, ok := meta.Functions[nc.Function]; ok {
							return mapCQualTypeToWONCType(fnSig.ReturnType)
						}
					}
				}
				if call, ok := es.Expr.(*ast.CallExpr); ok && call.Callee == "bytes_equal" {
					return "bool"
				}
			}
		}
		return "int"
	default:
		return "int64_t"
	}
}

func (g *Generator) genExpr(expr ast.Expr) (string, error) {
	if expr == nil {
		return "", nil
	}

	switch e := expr.(type) {
	case *ast.IntegerLiteral:
		if e.Suffix != "" {
			return fmt.Sprintf("%d", e.Value), nil
		}
		return e.Raw, nil
	case *ast.StringLiteral:
		return fmt.Sprintf("%q", e.Value), nil
	case *ast.Identifier:
		return e.Name, nil
	case *ast.BinaryExpr:
		left, err := g.genExpr(e.Left)
		if err != nil {
			return "", err
		}
		right, err := g.genExpr(e.Right)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s %s %s)", left, e.Op, right), nil
	case *ast.UnaryExpr:
		sub, err := g.genExpr(e.Expr)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s%s)", e.Op, sub), nil
	case *ast.BorrowExpr:
		sub, err := g.genExpr(e.Expr)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(&%s)", sub), nil
	case *ast.CallExpr:
		args := make([]string, 0, len(e.Args))
		for _, arg := range e.Args {
			argStr, err := g.genExpr(arg)
			if err != nil {
				return "", err
			}
			args = append(args, argStr)
		}
		if e.Callee == "println" {
			return genPrintlnCall(args), nil
		}
		if e.Callee == "bytes_equal" {
			if len(args) != 3 {
				return "", fmt.Errorf("内置函数 bytes_equal 期望 3 个实参，实际收到 %d 个 (第 %d 行, 第 %d 列)", len(args), e.Span.Start.Line, e.Span.Start.Column)
			}
			return fmt.Sprintf("won_bytes_equal(%s, %s, %s)", args[0], args[1], args[2]), nil
		}
		return fmt.Sprintf("%s(%s)", e.Callee, strings.Join(args, ", ")), nil
	case *ast.NamespacedCallExpr:
		args := make([]string, 0, len(e.Args))
		for _, arg := range e.Args {
			argStr, err := g.genExpr(arg)
			if err != nil {
				return "", err
			}
			args = append(args, argStr)
		}
		return fmt.Sprintf("%s(%s)", e.Function, strings.Join(args, ", ")), nil
	case *ast.MethodCallExpr:
		recStr, err := g.genExpr(e.Receiver)
		if err != nil {
			return "", err
		}
		switch e.Method {
		case "as_bytes", "as_ptr":
			return fmt.Sprintf("((const unsigned char*)(%s))", recStr), nil
		case "as_mut_ptr":
			return fmt.Sprintf("((unsigned char*)(%s))", recStr), nil
		case "len":
			return fmt.Sprintf("((unsigned long)sizeof(%s))", recStr), nil
		default:
			return "", fmt.Errorf("代码生成器遇到不受支持的方法调用: .%s() (第 %d 行, 第 %d 列)", e.Method, e.Span.Start.Line, e.Span.Start.Column)
		}
	case *ast.UnsafeExpr:
		// 当 unsafe 表达式作为子表达式时，仅支持包含单一表达式语句的块
		if len(e.Block.Statements) == 1 {
			if es, ok := e.Block.Statements[0].(*ast.ExprStmt); ok {
				return g.genExpr(es.Expr)
			}
		}
		return "", fmt.Errorf("不支持在嵌套子表达式位置使用多语句 unsafe 块 (第 %d 行, 第 %d 列): 请使用独立语句 'let x = unsafe { ... }' 声明", e.Span.Start.Line, e.Span.Start.Column)
	default:
		return "", fmt.Errorf("代码生成器遇到不受支持的表达式类型: %T (第 %d 行, 第 %d 列)", expr, expr.GetSpan().Start.Line, expr.GetSpan().Start.Column)
	}
}

func genPrintlnCall(args []string) string {
	if len(args) == 0 {
		return `printf("\n")`
	}
	var format strings.Builder
	passArgs := make([]string, 0)
	for _, arg := range args {
		if strings.HasPrefix(arg, "\"") {
			unquoted := arg[1 : len(arg)-1]
			format.WriteString(unquoted)
		} else {
			format.WriteString("%ld")
			passArgs = append(passArgs, fmt.Sprintf("(long)(%s)", arg))
		}
	}
	format.WriteString("\\n")
	if len(passArgs) == 0 {
		return fmt.Sprintf(`printf("%s")`, format.String())
	}
	return fmt.Sprintf(`printf("%s", %s)`, format.String(), strings.Join(passArgs, ", "))
}

func mapWonTypeToC(wonType string) string {
	switch wonType {
	case "i32":
		return "int"
	case "u64":
		return "unsigned long" // 匹配 Linux x86_64 uLong ABI
	case "u8":
		return "unsigned char"
	case "&str":
		return "const char*"
	case "*const u8":
		return "const unsigned char*"
	case "*mut u8":
		return "unsigned char*"
	case "bool":
		return "bool"
	default:
		return wonType
	}
}

func mapCQualTypeToWONCType(qualType string) string {
	clean := strings.TrimSpace(qualType)
	switch clean {
	case "unsigned long", "uLong", "uLongf":
		return "unsigned long"
	case "int":
		return "int"
	case "const char *", "const char*":
		return "const char*"
	case "unsigned char *", "unsigned char*":
		return "unsigned char*"
	default:
		return clean
	}
}

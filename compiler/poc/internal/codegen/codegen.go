// Package codegen 实现了从 WON AST 到标准 ISO C99/C11 源码的生成器
package codegen

import (
	"fmt"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
)

// Generator C 源码生成器
type Generator struct {
	prog    *ast.Program
	binding *bindgen.BindingMeta
	sb      strings.Builder
	indent  int
}

// New 创建生成器实例
func New(prog *ast.Program, binding *bindgen.BindingMeta) *Generator {
	return &Generator{
		prog:    prog,
		binding: binding,
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

// Generate 执行代码生成，返回完整 C 语言源码
func (g *Generator) Generate() (string, error) {
	g.sb.Reset()

	// 1. 标准 C 运行时头文件
	g.writeLine("/* 由 WON Phase 0B 编译器自动生成 - 请勿手动修改 */")
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
	g.writeLine("// --- WON 极简内置运行时宏与辅助函数 ---")
	g.writeLine("#define won_assert_eq(a, b, msg) do { \\")
	g.writeLine("    if ((a) != (b)) { \\")
	g.writeLine("        fprintf(stderr, \"[WON PANIC] 断言失败: %s (实际: %ld != 预期: %ld)\\n\", (msg), (long)(a), (long)(b)); \\")
	g.writeLine("        exit(1); \\")
	g.writeLine("    } \\")
	g.writeLine("} while(0)")
	g.writeLine("")

	// 4. 生成函数声明与定义
	for _, fn := range g.prog.Functions {
		g.genFunction(fn)
	}

	return g.sb.String(), nil
}

func (g *Generator) genFunction(fn *ast.FunctionDecl) {
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
	g.genBlockStatements(fn.Body)
	g.indent--
	g.writeLine("}")
	g.writeLine("")
}

func (g *Generator) genBlockStatements(b *ast.BlockStmt) {
	for _, stmt := range b.Statements {
		g.genStatement(stmt)
	}
}

func (g *Generator) genStatement(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.LetStmt:
		g.genLet(s)
	case *ast.AssignStmt:
		valStr := g.genExpr(s.Value)
		g.writeLine(fmt.Sprintf("%s = %s;", s.Target, valStr))
	case *ast.IfStmt:
		condStr := g.genExpr(s.Cond)
		g.writeLine(fmt.Sprintf("if (%s) {", condStr))
		g.indent++
		g.genBlockStatements(s.Then)
		g.indent--
		if s.Else != nil {
			if elseBlock, ok := s.Else.(*ast.BlockStmt); ok {
				g.writeLine("} else {")
				g.indent++
				g.genBlockStatements(elseBlock)
				g.indent--
			} else {
				g.writeLine("} else")
				g.genStatement(s.Else)
			}
		} else {
			g.writeLine("}")
		}
	case *ast.WhileStmt:
		condStr := g.genExpr(s.Cond)
		g.writeLine(fmt.Sprintf("while (%s) {", condStr))
		g.indent++
		g.genBlockStatements(s.Body)
		g.indent--
		g.writeLine("}")
	case *ast.ReturnStmt:
		if s.Value != nil {
			g.writeLine(fmt.Sprintf("return %s;", g.genExpr(s.Value)))
		} else {
			g.writeLine("return;")
		}
	case *ast.ExprStmt:
		if u, ok := s.Expr.(*ast.UnsafeExpr); ok {
			g.writeLine("{")
			g.indent++
			g.genBlockStatements(u.Block)
			g.indent--
			g.writeLine("}")
			return
		}
		exprStr := g.genExpr(s.Expr)
		if exprStr != "" {
			g.writeLine(fmt.Sprintf("%s;", exprStr))
		}
	}
}

func (g *Generator) genLet(s *ast.LetStmt) {
	// 检查是否为数组重复声明: let [mut] buf: [u8; 256] = [0; 256];
	if arr, ok := s.Init.(*ast.ArrayRepeatLiteral); ok {
		elemVal := g.genExpr(arr.Elem)
		g.writeLine(fmt.Sprintf("unsigned char %s[%d] = {%s};", s.Name, arr.Count, elemVal))
		return
	}

	cType := "int64_t"
	if s.Type != "" {
		cType = mapWonTypeToC(s.Type)
	} else {
		switch init := s.Init.(type) {
		case *ast.StringLiteral:
			cType = "const char*"
		case *ast.IntegerLiteral:
			cType = "int64_t"
		case *ast.MethodCallExpr:
			switch init.Method {
			case "as_bytes", "as_ptr":
				cType = "const unsigned char*"
			case "as_mut_ptr":
				cType = "unsigned char*"
			case "len":
				cType = "unsigned long"
			default:
				cType = "int64_t"
			}
		case *ast.UnsafeExpr:
			cType = "int"
			if len(init.Block.Statements) > 0 {
				if es, ok := init.Block.Statements[len(init.Block.Statements)-1].(*ast.ExprStmt); ok {
					if nc, ok := es.Expr.(*ast.NamespacedCallExpr); ok {
						if nc.Function == "compressBound" {
							cType = "unsigned long"
						}
					}
				}
			}
		default:
			cType = "int64_t"
		}
	}

	initStr := g.genExpr(s.Init)
	g.writeLine(fmt.Sprintf("%s %s = %s;", cType, s.Name, initStr))
}

func (g *Generator) genExpr(expr ast.Expr) string {
	if expr == nil {
		return ""
	}

	switch e := expr.(type) {
	case *ast.IntegerLiteral:
		return e.Raw
	case *ast.StringLiteral:
		return fmt.Sprintf("%q", e.Value)
	case *ast.Identifier:
		return e.Name
	case *ast.BinaryExpr:
		left := g.genExpr(e.Left)
		right := g.genExpr(e.Right)
		return fmt.Sprintf("(%s %s %s)", left, e.Op, right)
	case *ast.UnaryExpr:
		sub := g.genExpr(e.Expr)
		return fmt.Sprintf("(%s%s)", e.Op, sub)
	case *ast.BorrowExpr:
		sub := g.genExpr(e.Expr)
		return fmt.Sprintf("(&%s)", sub)
	case *ast.CallExpr:
		args := make([]string, 0)
		for _, arg := range e.Args {
			args = append(args, g.genExpr(arg))
		}
		if e.Callee == "println" {
			return genPrintlnCall(args)
		}
		return fmt.Sprintf("%s(%s)", e.Callee, strings.Join(args, ", "))
	case *ast.NamespacedCallExpr:
		args := make([]string, 0)
		for _, arg := range e.Args {
			args = append(args, g.genExpr(arg))
		}
		return fmt.Sprintf("%s(%s)", e.Function, strings.Join(args, ", "))
	case *ast.MethodCallExpr:
		recStr := g.genExpr(e.Receiver)
		switch e.Method {
		case "as_bytes", "as_ptr":
			return fmt.Sprintf("((const unsigned char*)(%s))", recStr)
		case "as_mut_ptr":
			return fmt.Sprintf("((unsigned char*)(%s))", recStr)
		case "len":
			return fmt.Sprintf("((unsigned long)sizeof(%s))", recStr)
		default:
			return fmt.Sprintf("%s.%s()", recStr, e.Method)
		}
	case *ast.UnsafeExpr:
		var inner strings.Builder
		inner.WriteString("({\n")
		indentStr := strings.Repeat("    ", g.indent+1)
		for _, stmt := range e.Block.Statements {
			if es, ok := stmt.(*ast.ExprStmt); ok {
				inner.WriteString(fmt.Sprintf("%s%s;\n", indentStr, g.genExpr(es.Expr)))
			} else {
				// 普通语句
				inner.WriteString(fmt.Sprintf("%s/* stmt */;\n", indentStr))
			}
		}
		inner.WriteString(strings.Repeat("    ", g.indent) + "})")
		return inner.String()
	}
	return ""
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
	if len(passArgs) > 0 {
		return fmt.Sprintf(`printf("%s", %s)`, format.String(), strings.Join(passArgs, ", "))
	}
	return fmt.Sprintf(`printf("%s")`, format.String())
}

func mapWonTypeToC(wonType string) string {
	switch wonType {
	case "i32":
		return "int"
	case "u32":
		return "uint32_t"
	case "i64":
		return "int64_t"
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
	default:
		return wonType
	}
}

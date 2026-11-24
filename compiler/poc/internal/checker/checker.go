// Package checker 实现了 WON Phase 0B 极简语义分析与可变性检查器
// 重点验证变量作用域、可变性借用约束 (WON-MUT-001)、C FFI 绑定签名验证 (WON-FFI) 以及安全边界 (WON-SEC-001)。
package checker

import (
	"fmt"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
)

// VarInfo 记录变量符号信息
type VarInfo struct {
	Name         string
	IsMut        bool
	Type         string
	DeclSpan     diagnostic.Span
	LetTokenSpan diagnostic.Span
}

// Scope 词法作用域符号表
type Scope struct {
	parent *Scope
	vars   map[string]*VarInfo
}

func newScope(parent *Scope) *Scope {
	return &Scope{
		parent: parent,
		vars:   make(map[string]*VarInfo),
	}
}

func (s *Scope) lookup(name string) *VarInfo {
	if info, ok := s.vars[name]; ok {
		return info
	}
	if s.parent != nil {
		return s.parent.lookup(name)
	}
	return nil
}

// Checker 语义分析器
type Checker struct {
	report   *diagnostic.Report
	inUnsafe bool
	bindings map[string]*bindgen.BindingMeta // 引入的 C 库绑定元数据映射 (如 "zlib" -> meta)
}

// New 创建语义分析器实例，注入已解析的 C 头文件绑定元数据
func New(report *diagnostic.Report, bindings map[string]*bindgen.BindingMeta) *Checker {
	if bindings == nil {
		bindings = make(map[string]*bindgen.BindingMeta)
	}
	return &Checker{
		report:   report,
		bindings: bindings,
	}
}

// CheckProgram 检查整个 AST 程序
func (c *Checker) CheckProgram(prog *ast.Program) {
	for _, fn := range prog.Functions {
		c.checkFunction(fn)
	}
}

func (c *Checker) checkFunction(fn *ast.FunctionDecl) {
	scope := newScope(nil)
	for _, p := range fn.Params {
		scope.vars[p.Name] = &VarInfo{
			Name:     p.Name,
			IsMut:    false,
			Type:     p.Type,
			DeclSpan: p.Span,
		}
	}
	c.checkBlock(fn.Body, scope)
}

func (c *Checker) checkBlock(b *ast.BlockStmt, scope *Scope) {
	blockScope := newScope(scope)
	for _, stmt := range b.Statements {
		c.checkStatement(stmt, blockScope)
	}
}

func (c *Checker) checkStatement(stmt ast.Stmt, scope *Scope) {
	switch s := stmt.(type) {
	case *ast.LetStmt:
		c.checkExpression(s.Init, scope)
		scope.vars[s.Name] = &VarInfo{
			Name:         s.Name,
			IsMut:        s.IsMut,
			Type:         s.Type,
			DeclSpan:     s.Span,
			LetTokenSpan: s.LetTokenSpan,
		}

	case *ast.AssignStmt:
		c.checkExpression(s.Value, scope)
		v := scope.lookup(s.Target)
		if v == nil {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-SEM-001",
				Severity: "error",
				Message:  fmt.Sprintf("未定义的未声明变量: %q", s.Target),
				File:     s.Span.File,
				Span:     s.Span,
			})
			return
		}
		if !v.IsMut {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-MUT-001",
				Severity:    "error",
				Message:     fmt.Sprintf("不能给不可变变量 '%s' 赋值", s.Target),
				File:        s.Span.File,
				Span:        s.Span,
				Explanation: fmt.Sprintf("变量 '%s' 通过 'let' 声明为只读。WON 默认所有绑定不可变，若需修改请声明为 'let mut'。", s.Target),
				Fixes: []diagnostic.Fix{
					{
						Description: fmt.Sprintf("将变量 '%s' 声明修改为可变 ('let mut')", s.Target),
						Edits: []diagnostic.Edit{
							{
								Start:       v.LetTokenSpan.Start.Byte,
								End:         v.LetTokenSpan.End.Byte,
								Replacement: "let mut",
							},
						},
					},
				},
			})
		}

	case *ast.IfStmt:
		c.checkExpression(s.Cond, scope)
		c.checkBlock(s.Then, scope)
		if s.Else != nil {
			if elseBlock, ok := s.Else.(*ast.BlockStmt); ok {
				c.checkBlock(elseBlock, scope)
			} else {
				c.checkStatement(s.Else, scope)
			}
		}

	case *ast.WhileStmt:
		c.checkExpression(s.Cond, scope)
		c.checkBlock(s.Body, scope)

	case *ast.ReturnStmt:
		if s.Value != nil {
			c.checkExpression(s.Value, scope)
		}

	case *ast.ExprStmt:
		c.checkExpression(s.Expr, scope)
	}
}

func (c *Checker) checkExpression(expr ast.Expr, scope *Scope) {
	if expr == nil {
		return
	}

	switch e := expr.(type) {
	case *ast.IntegerLiteral, *ast.StringLiteral:
		// 字面量天然合法

	case *ast.ArrayRepeatLiteral:
		c.checkExpression(e.Elem, scope)

	case *ast.Identifier:
		if scope.lookup(e.Name) == nil {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-SEM-001",
				Severity: "error",
				Message:  fmt.Sprintf("使用未声明的变量: %q", e.Name),
				File:     e.Span.File,
				Span:     e.Span,
			})
		}

	case *ast.BorrowExpr:
		c.checkExpression(e.Expr, scope)
		if e.IsMut {
			if ident, ok := e.Expr.(*ast.Identifier); ok {
				v := scope.lookup(ident.Name)
				if v != nil && !v.IsMut {
					c.report.AddDiagnostic(diagnostic.Diagnostic{
						Code:        "WON-MUT-001",
						Severity:    "error",
						Message:     fmt.Sprintf("无法对不可变变量 '%s' 进行可变借用 (&mut)", ident.Name),
						File:        e.Span.File,
						Span:        e.Span,
						Explanation: fmt.Sprintf("变量 '%s' 使用 'let' 声明为不可变。向需要修改内容的函数传递 &mut 引用要求其定义带有 'mut' 标记。", ident.Name),
						Fixes: []diagnostic.Fix{
							{
								Description: fmt.Sprintf("将变量 '%s' 声明修改为可变 ('let mut')", ident.Name),
								Edits: []diagnostic.Edit{
									{
										Start:       v.LetTokenSpan.Start.Byte,
										End:         v.LetTokenSpan.End.Byte,
										Replacement: "let mut",
									},
								},
							},
						},
					})
				}
			}
		}

	case *ast.BinaryExpr:
		c.checkExpression(e.Left, scope)
		c.checkExpression(e.Right, scope)

	case *ast.UnaryExpr:
		c.checkExpression(e.Expr, scope)

	case *ast.CallExpr:
		for _, arg := range e.Args {
			c.checkExpression(arg, scope)
		}
		// 校验内置 bytes_equal 参数个数
		if e.Callee == "bytes_equal" {
			if len(e.Args) != 3 {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-TYP-001",
					Severity: "error",
					Message:  fmt.Sprintf("内置函数 bytes_equal 期望 3 个参数 (ptr1, ptr2, len)，实际传入 %d 个", len(e.Args)),
					File:     e.Span.File,
					Span:     e.Span,
				})
			}
		}

	case *ast.NamespacedCallExpr:
		for _, arg := range e.Args {
			c.checkExpression(arg, scope)
		}

		// 1. 验证外部命名空间是否已绑定引入
		meta, exists := c.bindings[e.Namespace]
		if !exists {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-FFI-001",
				Severity:    "error",
				Message:     fmt.Sprintf("未导入的外部 C 命名空间: %q (请在源文件头部使用 #import 引入)", e.Namespace),
				File:        e.Span.File,
				Span:        e.Span,
				Explanation: fmt.Sprintf("无法解析命名空间 '%s'。WON 要求通过 #import 指令显式引入 C 头文件并建立绑定映射。", e.Namespace),
			})
			return
		}

		// 2. 验证外部函数符号是否存在于提取的头文件元数据中
		fnSig, found := meta.Functions[e.Function]
		if !found {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-FFI-002",
				Severity:    "error",
				Message:     fmt.Sprintf("在 C 头文件 '%s' 中未找到导出的函数符号: %q", meta.HeaderPath, e.Function),
				File:        e.Span.File,
				Span:        e.Span,
				Explanation: fmt.Sprintf("从头文件 '%s' 提取的真实声明符号列表中不存在函数 '%s'。", meta.HeaderPath, e.Function),
			})
			return
		}

		// 3. 验证参数数量是否与 C 函数原型匹配
		if len(e.Args) != len(fnSig.ParamTypes) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-FFI-003",
				Severity:    "error",
				Message:     fmt.Sprintf("外部 C 函数 '%s::%s' 参数数量不匹配: 期望 %d 个参数，实际传入 %d 个", e.Namespace, e.Function, len(fnSig.ParamTypes), len(e.Args)),
				File:        e.Span.File,
				Span:        e.Span,
				Explanation: fmt.Sprintf("根据头文件解析的真实函数签名: %s(%s)，期望参数个数为 %d。", e.Function, strings.Join(fnSig.ParamTypes, ", "), len(fnSig.ParamTypes)),
			})
		}

		// 4. 跨 C 命名空间调用，必须在显式 unsafe 块内
		if !c.inUnsafe {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-SEC-001",
				Severity:    "error",
				Message:     fmt.Sprintf("调用外部 C 库函数 '%s::%s' 必须在显式 unsafe 代码块内", e.Namespace, e.Function),
				File:        e.Span.File,
				Span:        e.Span,
				Explanation: "所有未经编译器证明内存安全的底层 C FFI 函数必须被包裹在 unsafe { ... } 中，确保审计边界清晰。",
			})
		}

	case *ast.MethodCallExpr:
		c.checkExpression(e.Receiver, scope)
		for _, arg := range e.Args {
			c.checkExpression(arg, scope)
		}
		if e.Method == "as_mut_ptr" {
			if ident, ok := e.Receiver.(*ast.Identifier); ok {
				v := scope.lookup(ident.Name)
				if v != nil && !v.IsMut {
					c.report.AddDiagnostic(diagnostic.Diagnostic{
						Code:        "WON-MUT-001",
						Severity:    "error",
						Message:     fmt.Sprintf("无法对不可变变量 '%s' 调用可变指针方法 (as_mut_ptr)", ident.Name),
						File:        e.Span.File,
						Span:        e.Span,
						Explanation: fmt.Sprintf("变量 '%s' 使用 'let' 声明为不可变。获取可变原生指针 (*mut) 要求其定义带有 'mut' 标记。", ident.Name),
						Fixes: []diagnostic.Fix{
							{
								Description: fmt.Sprintf("将变量 '%s' 声明修改为可变 ('let mut')", ident.Name),
								Edits: []diagnostic.Edit{
									{
										Start:       v.LetTokenSpan.Start.Byte,
										End:         v.LetTokenSpan.End.Byte,
										Replacement: "let mut",
									},
								},
							},
						},
					})
				}
			}
		}

	case *ast.UnsafeExpr:
		prev := c.inUnsafe
		c.inUnsafe = true
		c.checkBlock(e.Block, scope)
		c.inUnsafe = prev
	}
}

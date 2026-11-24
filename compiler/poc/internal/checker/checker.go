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
		varType := s.Type
		if varType == "" {
			varType = c.inferExprType(s.Init, scope)
		}
		// 校验不能将返回值为 void 的外部 C 函数调用结果赋值给变量 (WON-FFI-006)
		if varType == "void" {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-FFI-006",
				Severity:    "error",
				Message:     fmt.Sprintf("不能将返回值为 void 的外部调用结果赋值给变量 '%s'", s.Name),
				File:        s.Span.File,
				Span:        s.Span,
				Explanation: "外部 C 函数原型声明返回值为 void，不能作为变量绑定的右值表达式。",
			})
		}
		scope.vars[s.Name] = &VarInfo{
			Name:         s.Name,
			IsMut:        s.IsMut,
			Type:         varType,
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

		// 4. 验证是否为 Phase 0B 原型显式支持并能验证 ABI 的 C 函数
		if e.Namespace == "zlib" && !isSupportedZlibFunc(e.Function) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-FFI-005",
				Severity:    "error",
				Message:     fmt.Sprintf("C 函数 '%s::%s' 的签名或类型在 Phase 0B 原型中暂不支持", e.Namespace, e.Function),
				File:        e.Span.File,
				Span:        e.Span,
				Explanation: "Phase 0B 原型仅显式验证并支持核心标量与原生指针 C 接口 (compressBound, compress, uncompress, zlibVersion)。",
			})
			return
		}

		// 5. 严格验证实参表达式类型与 C 原型形参类型匹配 (保留 const/mut 指针与标量区分)
		for i, arg := range e.Args {
			if i >= len(fnSig.ParamTypes) {
				break
			}
			expectedCType := fnSig.ParamTypes[i]
			argType := c.inferExprType(arg, scope)
			if !c.isTypeCompatible(argType, expectedCType) {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:        "WON-FFI-004",
					Severity:    "error",
					Message:     fmt.Sprintf("外部 C 函数 '%s::%s' 第 %d 个参数类型不匹配: 期望 %s，实际传入 %s", e.Namespace, e.Function, i+1, expectedCType, argType),
					File:        e.Span.File,
					Span:        arg.GetSpan(),
					Explanation: fmt.Sprintf("Linux x86_64 C ABI 要求实参类型与 C 原型严格对齐。不能将 WON 类型 '%s' 传递给期望 '%s' 的 C 参数。", argType, expectedCType),
				})
			}
		}

		// 6. 跨 C 命名空间调用，必须在显式 unsafe 块内
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

func isSupportedZlibFunc(name string) bool {
	switch name {
	case "compress", "uncompress", "compressBound", "zlibVersion":
		return true
	default:
		return false
	}
}

// inferExprType 推断 WON 表达式的基础语义类型，用于 C ABI 参数匹配校验
func (c *Checker) inferExprType(expr ast.Expr, scope *Scope) string {
	if expr == nil {
		return "unknown"
	}
	switch e := expr.(type) {
	case *ast.IntegerLiteral:
		return "u64"
	case *ast.StringLiteral:
		return "&str"
	case *ast.Identifier:
		v := scope.lookup(e.Name)
		if v != nil && v.Type != "" {
			return v.Type
		}
		return "unknown"
	case *ast.ArrayRepeatLiteral:
		return fmt.Sprintf("[u8; %d]", e.Count)
	case *ast.MethodCallExpr:
		switch e.Method {
		case "as_mut_ptr":
			return "*mut u8"
		case "as_ptr", "as_bytes":
			return "*const u8"
		case "len":
			return "u64"
		default:
			return "unknown"
		}
	case *ast.BorrowExpr:
		if ident, ok := e.Expr.(*ast.Identifier); ok {
			v := scope.lookup(ident.Name)
			if v != nil {
				innerType := v.Type
				if innerType == "" {
					innerType = "unknown"
				}
				if e.IsMut {
					return "*mut " + innerType
				}
				return "*const " + innerType
			}
		}
		return "unknown"
	case *ast.UnsafeExpr:
		if len(e.Block.Statements) > 0 {
			if es, ok := e.Block.Statements[len(e.Block.Statements)-1].(*ast.ExprStmt); ok {
				return c.inferExprType(es.Expr, scope)
			}
		}
		return "unknown"
	case *ast.NamespacedCallExpr:
		if meta, ok := c.bindings[e.Namespace]; ok {
			if sig, ok := meta.Functions[e.Function]; ok {
				switch sig.ReturnType {
				case "int":
					return "i32"
				case "unsigned long", "uLong":
					return "u64"
				case "const char *":
					return "*const u8"
				case "void":
					return "void"
				default:
					return sig.ReturnType
				}
			}
		}
		return "unknown"
	case *ast.CallExpr:
		if e.Callee == "bytes_equal" {
			return "bool"
		}
		return "unknown"
	case *ast.BinaryExpr:
		return "u64"
	default:
		return "unknown"
	}
}

// isTypeCompatible 针对 Linux x86_64 C ABI 验证 WON 实参类型与 C 形参原型的一致性
func (c *Checker) isTypeCompatible(wonType, expectedCType string) bool {
	cleanC := strings.TrimSpace(expectedCType)
	switch cleanC {
	case "unsigned char *", "Bytef *", "Byte *":
		// 明确要求可变字节指针
		return wonType == "*mut u8" || wonType == "*mut [u8]" || strings.HasPrefix(wonType, "*mut [u8;")
	case "const unsigned char *", "const Bytef *", "const Byte *":
		// 可接受只读指针或可变指针 (协变)
		return wonType == "*const u8" || wonType == "*mut u8" || wonType == "*const [u8]" || strings.HasPrefix(wonType, "*const [u8;")
	case "unsigned long *", "uLongf *", "uLong *":
		// 明确要求指向无符号长整数的可变指针
		return wonType == "*mut u64" || wonType == "*mut unsigned long"
	case "const unsigned long *":
		return wonType == "*const u64" || wonType == "*mut u64"
	case "unsigned long", "uLong", "uInt", "int", "int64_t", "size_t":
		// 必须为整数值，坚决拒绝任何指针或字符串
		return wonType == "u64" || wonType == "i32" || wonType == "i64" || wonType == "u32"
	case "const char *":
		return wonType == "&str" || wonType == "*const u8"
	default:
		return false
	}
}

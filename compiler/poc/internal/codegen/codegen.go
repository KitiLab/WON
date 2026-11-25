// Package codegen 实现了从经过语义验证的 WON AST / TypedProgram 到严格 ISO C11 标准源码的生成器
// 坚决剔除任何 GNU 语句表达式与非标准扩展，采用显式 fail-closed 类型映射、精确 Typed 整型常量宏与合法 C11 main ABI。
package codegen

import (
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// Generator C11 源码生成器
type Generator struct {
	prog     *ast.Program
	typed    *checker.TypedProgram
	bindings map[string]*bindgen.BindingMeta
	sb       strings.Builder
	indent   int
}

// New 创建基于语义校验结果的 C11 代码生成器（兼容未显式传入 TypedProgram 的直接调用场景）
func New(prog *ast.Program, bindings map[string]*bindgen.BindingMeta) *Generator {
	if bindings == nil {
		bindings = make(map[string]*bindgen.BindingMeta)
	}
	var tp *checker.TypedProgram
	if prog != nil {
		rep := diagnostic.NewReport()
		chk := checker.New(rep, bindings)
		tp = chk.CheckProgram(prog)
	}
	return &Generator{
		prog:     prog,
		typed:    tp,
		bindings: bindings,
	}
}

// NewTyped 创建绑定了外部已验证 TypedProgram 的 C11 代码生成器，确保后端严格复用前端规范 TypeID
func NewTyped(prog *ast.Program, typed *checker.TypedProgram, bindings map[string]*bindgen.BindingMeta) *Generator {
	if bindings == nil {
		bindings = make(map[string]*bindgen.BindingMeta)
	}
	return &Generator{
		prog:     prog,
		typed:    typed,
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

// Generate 执行代码生成，返回严格符合 ISO C11 规范的 C 语言源码
func (g *Generator) Generate() (string, error) {
	g.sb.Reset()

	// 1. 标准 C11 运行时与固定宽度类型头文件
	g.writeLine("/* 由 WON Phase 2A.2 编译器自动生成 - 严格遵循 ISO C11 标准 (Linux x86_64 LP64 ABI) */")
	g.writeLine("#define _POSIX_C_SOURCE 200809L")
	g.writeLine("#include <stdio.h>")
	g.writeLine("#include <stdlib.h>")
	g.writeLine("#include <stddef.h>")
	g.writeLine("#include <stdint.h>")
	g.writeLine("#include <stdbool.h>")
	g.writeLine("#include <inttypes.h>")
	g.writeLine("#include <string.h>")
	g.writeLine("_Static_assert(sizeof(void*) == 8 && sizeof(long) == 8, \"WON C11 backend targets 64-bit LP64 ABI\");")

	// 2. 引入源文件声明的 C 头文件
	for _, imp := range g.prog.Imports {
		g.writeLine(fmt.Sprintf("#include %s", imp.HeaderPath))
	}
	g.writeLine("")

	// 3. 按需生成内置辅助函数，避免 Clang -Werror=unused-function 警告
	if programUsesCallee(g.prog, "bytes_equal") {
		g.writeLine("// --- WON 内置安全字节比较辅助函数 ---")
		g.writeLine("static inline bool won_bytes_equal(const uint8_t* a, const uint8_t* b, unsigned long len) {")
		g.writeLine("    return memcmp(a, b, len) == 0;")
		g.writeLine("}")
		g.writeLine("")
	}

	// 4. 提前输出所有非 main 用户函数原型声明以支持前向相互调用
	for _, fn := range g.prog.Functions {
		if fn.Name == "main" {
			continue
		}
		sig, _, err := g.buildFuncCSignature(fn)
		if err != nil {
			return "", err
		}
		g.writeLine(sig + ";")
	}
	if len(g.prog.Functions) > 1 {
		g.writeLine("")
	}

	// 5. 生成函数定义体
	for _, fn := range g.prog.Functions {
		if err := g.genFunction(fn); err != nil {
			return "", err
		}
	}

	return g.sb.String(), nil
}

func (g *Generator) lookupFnSymbol(fn *ast.FunctionDecl) *symbols.Symbol {
	if g.typed == nil || g.typed.Symbols == nil {
		return nil
	}
	if sym, ok := g.typed.Symbols.NodeSymbols[fn]; ok && sym != nil {
		return sym
	}
	return g.typed.Symbols.GlobalScope.LookupLocal(fn.Name)
}

func (g *Generator) buildFuncCSignature(fn *ast.FunctionDecl) (sig string, isUnitMain bool, err error) {
	if g.typed == nil || g.typed.Types == nil {
		return "", false, fmt.Errorf("缺少 TypedProgram 语义类型上下文，无法生成函数 '%s'", fn.Name)
	}
	fnSym := g.lookupFnSymbol(fn)
	if fnSym == nil {
		return "", false, fmt.Errorf("无法找到函数 '%s' 的已解析符号", fn.Name)
	}

	retC, err := MapTypeToC(g.typed.Types, fnSym.ReturnType, PosReturn)
	if err != nil {
		return "", false, fmt.Errorf("函数 '%s' 返回类型不支持 C11 生成: %w", fn.Name, err)
	}

	if fn.Name == "main" {
		if len(fn.Params) != 0 {
			return "", false, fmt.Errorf("程序入口 'main' 不允许携带参数")
		}
		if fnSym.ReturnType == g.typed.Types.I32 {
			return "int main(void)", false, nil
		}
		if fnSym.ReturnType == g.typed.Types.Unit {
			return "static void won_user_main(void)", true, nil
		}
		return "", false, fmt.Errorf("C11 宿主环境 'main' 入口不支持返回类型 '%s'", g.typed.Types.Format(fnSym.ReturnType))
	}

	params := make([]string, 0, len(fn.Params))
	for i, p := range fn.Params {
		if i >= len(fnSym.ParamTypes) {
			return "", false, fmt.Errorf("函数 '%s' 形参元数据不一致", fn.Name)
		}
		pC, err := MapTypeToC(g.typed.Types, fnSym.ParamTypes[i], PosParam)
		if err != nil {
			return "", false, fmt.Errorf("函数 '%s' 参数 '%s' 不支持 C11 生成: %w", fn.Name, p.Name, err)
		}
		params = append(params, fmt.Sprintf("%s %s", pC, p.Name))
	}
	paramStr := "void"
	if len(params) > 0 {
		paramStr = strings.Join(params, ", ")
	}
	return fmt.Sprintf("%s %s(%s)", retC, fn.Name, paramStr), false, nil
}

func (g *Generator) genFunction(fn *ast.FunctionDecl) error {
	sig, isUnitMain, err := g.buildFuncCSignature(fn)
	if err != nil {
		return err
	}

	g.writeLine(sig + " {")
	g.indent++
	if err := g.genBlockStatements(fn.Body); err != nil {
		return err
	}
	g.indent--
	g.writeLine("}")
	g.writeLine("")

	// 若 WON 入口声明为 `fn main():` (unit)，通过标准 ISO C11 `int main(void)` 包装并确定性返回 0
	if isUnitMain {
		g.writeLine("int main(void) {")
		g.indent++
		g.writeLine("won_user_main();")
		g.writeLine("return 0;")
		g.indent--
		g.writeLine("}")
		g.writeLine("")
	}

	return nil
}

func (g *Generator) genBlockStatements(b *ast.BlockStmt) error {
	if b == nil {
		return nil
	}
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
		targetStr := s.Target
		if s.TargetExpr != nil {
			if ue, ok := s.TargetExpr.(*ast.UnaryExpr); ok && ue.Op == "*" {
				innerStr, err := g.genExpr(ue.Expr)
				if err != nil {
					return err
				}
				targetStr = fmt.Sprintf("(*(%s))", innerStr)
			} else if id, ok := s.TargetExpr.(*ast.Identifier); ok {
				targetStr = id.Name
			} else {
				return fmt.Errorf("代码生成器遇到不受支持的左值赋值目标: %T (第 %d 行, 第 %d 列)", s.TargetExpr, s.Span.Start.Line, s.Span.Start.Column)
			}
		}

		op := s.Op
		if op == "" {
			op = "="
		}

		if u, ok := s.Value.(*ast.UnsafeExpr); ok {
			if op != "=" {
				return fmt.Errorf("复合赋值 '%s' 右侧暂不支持多语句 unsafe 块 (第 %d 行, 第 %d 列)", op, s.Span.Start.Line, s.Span.Start.Column)
			}
			g.writeLine("{")
			g.indent++
			if err := g.genUnsafeBlockAssign(u.Block, targetStr); err != nil {
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
		g.writeLine(fmt.Sprintf("%s %s %s;", targetStr, op, valStr))
		return nil

	case *ast.IfStmt:
		condStr, err := g.genExpr(s.Cond)
		if err != nil {
			return err
		}
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
				return nil
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

	case *ast.BreakStmt:
		g.writeLine("break;")
		return nil

	case *ast.ContinueStmt:
		g.writeLine("continue;")
		return nil

	case *ast.ReturnStmt:
		if s.Value != nil {
			retT := g.exprType(s.Value)
			if g.typed == nil || g.typed.Types == nil || retT == types.Invalid {
				return fmt.Errorf("return 表达式缺少已解析语义类型，无法安全生成 C11 代码 (第 %d 行, 第 %d 列)", s.Span.Start.Line, s.Span.Start.Column)
			}
			valStr, err := g.genExpr(s.Value)
			if err != nil {
				return err
			}
			// 若返还表达式类型为 unit (C 中为 void)，必须拆分为表达式求值语句 + 裸 return;，
			// 确保副作用求值一次且严格满足 ISO C11 §6.8.6.4 (void 函数不得带值 return)。
			if retT == g.typed.Types.Unit {
				if valStr != "" {
					g.writeLine(fmt.Sprintf("%s;", valStr))
				}
				g.writeLine("return;")
				return nil
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

func (g *Generator) lookupLetType(s *ast.LetStmt) types.TypeID {
	if g.typed == nil {
		return types.Invalid
	}
	if g.typed.Symbols != nil {
		if sym, ok := g.typed.Symbols.NodeSymbols[s]; ok && sym != nil {
			return sym.Type
		}
		if s.Pattern != nil {
			if sym, ok := g.typed.Symbols.NodeSymbols[s.Pattern]; ok && sym != nil {
				return sym.Type
			}
		}
	}
	if s.Init != nil {
		if tid, ok := g.typed.ExprTypes[s.Init]; ok {
			return tid
		}
	}
	return types.Invalid
}

func (g *Generator) genLet(s *ast.LetStmt) error {
	varType := g.lookupLetType(s)
	if g.typed == nil || g.typed.Types == nil || varType == types.Invalid {
		return fmt.Errorf("局部变量 '%s' 缺少已解析规范类型，拒绝盲目代码生成 (第 %d 行, 第 %d 列)", s.Name, s.Span.Start.Line, s.Span.Start.Column)
	}

	// 1. 局部定长重复初始化数组: var buf: [T; N] = [v; N]
	if arr, ok := s.Init.(*ast.ArrayRepeatLiteral); ok {
		arrInfo := g.typed.Types.Info(varType)
		if arrInfo.Kind != types.KindArray || arr.Count < 0 {
			return fmt.Errorf("数组初始化 '%s' 类型或维度不受支持", s.Name)
		}
		elemC, err := MapTypeToC(g.typed.Types, arrInfo.Elem, PosValue)
		if err != nil {
			return fmt.Errorf("数组 '%s' 元素类型不支持 C11 生成: %w", s.Name, err)
		}
		elemVal, err := g.genExpr(arr.Elem)
		if err != nil {
			return err
		}
		// 若全 0 初始化直接使用 `{0}` 填充；否则使用 {0} + 确定性循环填充所有元素，防止 C `{val}` 只初始化第 0 项
		if il, isInt := arr.Elem.(*ast.IntegerLiteral); isInt && il.Valid && il.Value != nil && il.Value.Sign() == 0 {
			g.writeLine(fmt.Sprintf("%s %s[%d] = {0};", elemC, s.Name, arr.Count))
		} else if bl, isBool := arr.Elem.(*ast.BoolLiteral); isBool && !bl.Value {
			g.writeLine(fmt.Sprintf("%s %s[%d] = {0};", elemC, s.Name, arr.Count))
		} else {
			g.writeLine(fmt.Sprintf("%s %s[%d] = {0};", elemC, s.Name, arr.Count))
			g.writeLine(fmt.Sprintf("for (size_t _won_i = 0; _won_i < ((size_t)UINT64_C(%d)); ++_won_i) { %s[_won_i] = %s; }", arr.Count, s.Name, elemVal))
		}
		return nil
	}

	// 2. Phase 0B .as_bytes() 兼容初始化视图: const uint8_t* input_bytes = ((const uint8_t*)(msg));
	var cType string
	if mc, ok := s.Init.(*ast.MethodCallExpr); ok && mc.Method == "as_bytes" {
		cType = "const uint8_t*"
	} else {
		var err error
		cType, err = MapTypeToC(g.typed.Types, varType, PosLocal)
		if err != nil {
			return fmt.Errorf("局部变量 '%s' 类型不支持 C11 生成: %w", s.Name, err)
		}
	}

	// 3. 显式类型的未初始化局部标量声明（已通过 Phase 2B.1 确定性初始化证明）
	if s.Init == nil {
		g.writeLine(fmt.Sprintf("%s %s;", cType, s.Name))
		return nil
	}

	// 4. 若初始值为 unsafe 块表达式，采用标准 ISO C11 变量预声明 + 块赋值
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

func (g *Generator) exprType(expr ast.Expr) types.TypeID {
	if g.typed == nil {
		return types.Invalid
	}
	return g.typed.ExprTypes[expr]
}

// emitTypedIntConstant 按照语义检查确定的具体 TypeID 与精确值生成严格 ISO C11 字面量表达式，
// 确保所有宽度与带符号最小值 (-2^(N-1)) 以及 u64 最大值均零警告、零未定义行为通过 -pedantic-errors。
func (g *Generator) emitTypedIntConstant(val *big.Int, tid types.TypeID, span diagnostic.Span) (string, error) {
	if g.typed == nil || g.typed.Types == nil || tid == types.Invalid {
		return "", fmt.Errorf("整数字面量缺少已解析语义类型 (第 %d 行, 第 %d 列)", span.Start.Line, span.Start.Column)
	}
	if val == nil {
		return "", fmt.Errorf("整数字面量值为空 (第 %d 行, 第 %d 列)", span.Start.Line, span.Start.Column)
	}
	tctx := g.typed.Types
	if !tctx.FitsBigIntLiteral(val, tid) {
		return "", fmt.Errorf("整型常量 %s 超出类型 '%s' 取值范围", val.String(), tctx.Format(tid))
	}

	info := tctx.Info(tid)
	switch info.Kind {
	case types.KindI8:
		if val.Int64() == math.MinInt8 {
			return "INT8_MIN", nil
		}
		if val.Sign() < 0 {
			abs := new(big.Int).Neg(val)
			return fmt.Sprintf("((int8_t)(-INT8_C(%s)))", abs.String()), nil
		}
		return fmt.Sprintf("((int8_t)INT8_C(%s))", val.String()), nil

	case types.KindI16:
		if val.Int64() == math.MinInt16 {
			return "INT16_MIN", nil
		}
		if val.Sign() < 0 {
			abs := new(big.Int).Neg(val)
			return fmt.Sprintf("((int16_t)(-INT16_C(%s)))", abs.String()), nil
		}
		return fmt.Sprintf("((int16_t)INT16_C(%s))", val.String()), nil

	case types.KindI32:
		if val.Int64() == math.MinInt32 {
			return "INT32_MIN", nil
		}
		if val.Sign() < 0 {
			abs := new(big.Int).Neg(val)
			return fmt.Sprintf("(-INT32_C(%s))", abs.String()), nil
		}
		return fmt.Sprintf("INT32_C(%s)", val.String()), nil

	case types.KindI64:
		if val.IsInt64() && val.Int64() == math.MinInt64 {
			return "INT64_MIN", nil
		}
		if val.Sign() < 0 {
			abs := new(big.Int).Neg(val)
			return fmt.Sprintf("(-INT64_C(%s))", abs.String()), nil
		}
		return fmt.Sprintf("INT64_C(%s)", val.String()), nil

	case types.KindISize:
		if val.IsInt64() && val.Int64() == math.MinInt64 {
			return "PTRDIFF_MIN", nil
		}
		if val.Sign() < 0 {
			abs := new(big.Int).Neg(val)
			return fmt.Sprintf("((ptrdiff_t)(-INT64_C(%s)))", abs.String()), nil
		}
		return fmt.Sprintf("((ptrdiff_t)INT64_C(%s))", val.String()), nil

	case types.KindU8:
		return fmt.Sprintf("((uint8_t)UINT8_C(%s))", val.String()), nil

	case types.KindU16:
		return fmt.Sprintf("((uint16_t)UINT16_C(%s))", val.String()), nil

	case types.KindU32:
		return fmt.Sprintf("UINT32_C(%s)", val.String()), nil

	case types.KindU64:
		return fmt.Sprintf("UINT64_C(%s)", val.String()), nil

	case types.KindUSize:
		return fmt.Sprintf("((size_t)UINT64_C(%s))", val.String()), nil

	default:
		return "", fmt.Errorf("整型常量目标类型 '%s' 不受当前 C11 后端支持", tctx.Format(tid))
	}
}

func (g *Generator) genExpr(expr ast.Expr) (string, error) {
	if expr == nil {
		return "", nil
	}

	switch e := expr.(type) {
	case *ast.IntegerLiteral:
		if !e.Valid || e.Value == nil {
			return "", fmt.Errorf("非法的整数字面量 %q (第 %d 行, 第 %d 列)", e.Raw, e.Span.Start.Line, e.Span.Start.Column)
		}
		return g.emitTypedIntConstant(e.Value, g.exprType(e), e.Span)

	case *ast.FloatLiteral:
		tid := g.exprType(e)
		valStr := strings.ReplaceAll(e.Raw, "_", "")
		if e.Suffix != "" {
			valStr = strings.TrimSuffix(valStr, e.Suffix)
		}
		if g.typed != nil && g.typed.Types != nil && tid == g.typed.Types.F32 {
			return valStr + "f", nil
		}
		return valStr, nil

	case *ast.BoolLiteral:
		if e.Value {
			return "true", nil
		}
		return "false", nil

	case *ast.CharLiteral:
		return fmt.Sprintf("((uint32_t)UINT32_C(%d))", uint32(e.Value)), nil

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
		cOp := e.Op
		switch cOp {
		case "and":
			cOp = "&&"
		case "or":
			cOp = "||"
		}
		return fmt.Sprintf("(%s %s %s)", left, cOp, right), nil

	case *ast.UnaryExpr:
		// 特殊处理带符号整数常量 `-lit`，确保 -9223372036854775808i64 等最小值发出 INT64_MIN 等精确宏
		if e.Op == "-" {
			if il, isIntLit := e.Expr.(*ast.IntegerLiteral); isIntLit {
				if !il.Valid || il.Value == nil {
					return "", fmt.Errorf("非法的整数字面量 %q (第 %d 行, 第 %d 列)", il.Raw, il.Span.Start.Line, il.Span.Start.Column)
				}
				negVal := new(big.Int).Neg(il.Value)
				return g.emitTypedIntConstant(negVal, g.exprType(e), e.Span)
			}
		}
		sub, err := g.genExpr(e.Expr)
		if err != nil {
			return "", err
		}
		cOp := e.Op
		if cOp == "not" {
			cOp = "!"
		}
		return fmt.Sprintf("(%s%s)", cOp, sub), nil

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
			return g.genPrintlnCall(e.Args, args), nil
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
		case "as_bytes":
			return fmt.Sprintf("((const uint8_t*)(%s))", recStr), nil
		case "as_ptr":
			recT := g.exprType(e.Receiver)
			if g.typed != nil && g.typed.Types != nil {
				recInfo := g.typed.Types.Info(recT)
				if recInfo.Kind == types.KindArray {
					elemC, err := MapTypeToC(g.typed.Types, recInfo.Elem, PosValue)
					if err != nil {
						return "", err
					}
					return fmt.Sprintf("((const %s*)(%s))", elemC, recStr), nil
				}
			}
			return fmt.Sprintf("((const uint8_t*)(%s))", recStr), nil
		case "as_mut_ptr":
			recT := g.exprType(e.Receiver)
			if g.typed != nil && g.typed.Types != nil {
				recInfo := g.typed.Types.Info(recT)
				if recInfo.Kind == types.KindArray {
					elemC, err := MapTypeToC(g.typed.Types, recInfo.Elem, PosValue)
					if err != nil {
						return "", err
					}
					return fmt.Sprintf("((%s*)(%s))", elemC, recStr), nil
				}
			}
			return fmt.Sprintf("((uint8_t*)(%s))", recStr), nil
		case "len":
			recT := g.exprType(e.Receiver)
			if g.typed != nil && g.typed.Types != nil && g.typed.Types.Info(recT).Kind == types.KindArray {
				return fmt.Sprintf("UINT64_C(%d)", g.typed.Types.Info(recT).Len), nil
			}
			return "", fmt.Errorf("当前 C11 后端仅允许在已知尺寸数组上调用 .len() (第 %d 行, 第 %d 列)", e.Span.Start.Line, e.Span.Start.Column)
		default:
			return "", fmt.Errorf("代码生成器遇到不受支持的方法调用: .%s() (第 %d 行, 第 %d 列)", e.Method, e.Span.Start.Line, e.Span.Start.Column)
		}

	case *ast.UnsafeExpr:
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

func (g *Generator) genPrintlnCall(argExprs []ast.Expr, args []string) string {
	if len(args) == 0 {
		return `printf("\n")`
	}
	var format strings.Builder
	passArgs := make([]string, 0)
	for i, arg := range args {
		if _, isStrLit := argExprs[i].(*ast.StringLiteral); isStrLit && len(arg) >= 2 && strings.HasPrefix(arg, "\"") {
			unquoted := arg[1 : len(arg)-1]
			unquoted = strings.ReplaceAll(unquoted, "%", "%%")
			format.WriteString(unquoted)
			continue
		}

		tid := g.exprType(argExprs[i])
		if g.typed != nil && g.typed.Types != nil && tid != types.Invalid {
			tctx := g.typed.Types
			info := tctx.Info(tid)
			switch {
			case tid == tctx.StrView:
				format.WriteString("%s")
				passArgs = append(passArgs, arg)
			case info.Kind == types.KindBool:
				format.WriteString("%s")
				passArgs = append(passArgs, fmt.Sprintf("((%s) ? \"true\" : \"false\")", arg))
			case info.Kind == types.KindChar:
				format.WriteString("%u")
				passArgs = append(passArgs, fmt.Sprintf("(unsigned int)(%s)", arg))
			case tctx.IsUnsigned(tid):
				format.WriteString("%llu")
				passArgs = append(passArgs, fmt.Sprintf("(unsigned long long)(%s)", arg))
			case tctx.IsSigned(tid):
				format.WriteString("%lld")
				passArgs = append(passArgs, fmt.Sprintf("(long long)(%s)", arg))
			case tctx.IsFloat(tid):
				format.WriteString("%g")
				passArgs = append(passArgs, fmt.Sprintf("(double)(%s)", arg))
			case info.Kind == types.KindRawPtr || info.Kind == types.KindRef:
				format.WriteString("%p")
				passArgs = append(passArgs, fmt.Sprintf("(const void*)(%s)", arg))
			default:
				format.WriteString("%lld")
				passArgs = append(passArgs, fmt.Sprintf("(long long)(%s)", arg))
			}
		} else {
			format.WriteString("%lld")
			passArgs = append(passArgs, fmt.Sprintf("(long long)(%s)", arg))
		}
	}
	format.WriteString("\\n")
	if len(passArgs) == 0 {
		return fmt.Sprintf(`printf("%s")`, format.String())
	}
	return fmt.Sprintf(`printf("%s", %s)`, format.String(), strings.Join(passArgs, ", "))
}

func programUsesCallee(prog *ast.Program, callee string) bool {
	if prog == nil {
		return false
	}
	var walkBlock func(*ast.BlockStmt) bool
	var walkStmt func(ast.Stmt) bool
	var walkExpr func(ast.Expr) bool

	walkExpr = func(expr ast.Expr) bool {
		if expr == nil {
			return false
		}
		switch e := expr.(type) {
		case *ast.CallExpr:
			if e.Callee == callee {
				return true
			}
			for _, a := range e.Args {
				if walkExpr(a) {
					return true
				}
			}
		case *ast.BinaryExpr:
			return walkExpr(e.Left) || walkExpr(e.Right)
		case *ast.UnaryExpr:
			return walkExpr(e.Expr)
		case *ast.BorrowExpr:
			return walkExpr(e.Expr)
		case *ast.NamespacedCallExpr:
			for _, a := range e.Args {
				if walkExpr(a) {
					return true
				}
			}
		case *ast.MethodCallExpr:
			if walkExpr(e.Receiver) {
				return true
			}
			for _, a := range e.Args {
				if walkExpr(a) {
					return true
				}
			}
		case *ast.UnsafeExpr:
			return walkBlock(e.Block)
		}
		return false
	}

	walkStmt = func(stmt ast.Stmt) bool {
		if stmt == nil {
			return false
		}
		switch s := stmt.(type) {
		case *ast.LetStmt:
			return walkExpr(s.Init)
		case *ast.AssignStmt:
			return walkExpr(s.Value)
		case *ast.IfStmt:
			return walkExpr(s.Cond) || walkBlock(s.Then) || walkStmt(s.Else)
		case *ast.WhileStmt:
			return walkExpr(s.Cond) || walkBlock(s.Body)
		case *ast.ReturnStmt:
			return walkExpr(s.Value)
		case *ast.ExprStmt:
			return walkExpr(s.Expr)
		case *ast.BlockStmt:
			return walkBlock(s)
		}
		return false
	}

	walkBlock = func(b *ast.BlockStmt) bool {
		if b == nil {
			return false
		}
		for _, st := range b.Statements {
			if walkStmt(st) {
				return true
			}
		}
		return false
	}

	for _, fn := range prog.Functions {
		if walkBlock(fn.Body) {
			return true
		}
	}
	return false
}

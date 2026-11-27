package checker

import (
	"fmt"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

type capTypePos int

const (
	capPosLocal capTypePos = iota
	capPosParam
	capPosReturn
	capPosValue
)

// verifyBackendCapabilities 对已完成语义检查的程序执行严格的 C11 后端能力子集审核。
// 任何不被当前 C11 后端完整支持的声明、类型、入口点 ABI、表达式或操作符都会标记 CodegenSupported = false，
// 从而在 wonc build 生成任何 C 代码前给出精确的 WON-GEN-002 诊断。
func (c *Checker) verifyBackendCapabilities(prog *ast.Program) {
	if prog == nil || c.typed == nil || !c.typed.CodegenSupported {
		return
	}

	if len(prog.ModuleImports) > 0 {
		c.typed.markCodegenUnsupported("外部 WON 模块导入尚未在 C11 后端实现代码生成", prog.ModuleImports[0].Span)
		return
	}
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.StructDecl:
			c.typed.markCodegenUnsupported("struct 声明与字段布局尚未在 C11 后端实现代码生成", d.Span)
			return
		case *ast.EnumDecl:
			c.typed.markCodegenUnsupported("enum 代数数据类型尚未在 C11 后端实现代码生成", d.Span)
			return
		case *ast.TraitDecl:
			c.typed.markCodegenUnsupported("trait 特质定义尚未在 C11 后端实现代码生成", d.Span)
			return
		case *ast.ImplBlock:
			c.typed.markCodegenUnsupported("impl 方法块尚未在 C11 后端实现代码生成", d.Span)
			return
		case *ast.TypeAliasDecl:
			c.typed.markCodegenUnsupported("type 别名尚未在当前 C11 后端实现降低", d.Span)
			return
		case *ast.ModuleDecl:
			c.typed.markCodegenUnsupported("mod 内联模块尚未在当前 C11 后端实现代码生成", d.Span)
			return
		}
	}

	sliceAliasVars := make(map[symbols.SymbolID]bool)
	for _, fn := range prog.Functions {
		c.verifyFnBackendCaps(fn, sliceAliasVars)
		if !c.typed.CodegenSupported {
			return
		}
	}
}

func (c *Checker) verifyFnBackendCaps(fn *ast.FunctionDecl, sliceAliasVars map[symbols.SymbolID]bool) {
	if fn == nil {
		return
	}
	fnSym := c.symbols.NodeSymbols[fn]
	if fnSym == nil {
		fnSym = c.symbols.GlobalScope.LookupLocal(fn.Name)
	}
	if fnSym == nil {
		c.typed.markCodegenUnsupported(fmt.Sprintf("函数 '%s' 缺少符号表元数据", fn.Name), fn.Span)
		return
	}
	if len(fn.GenericParams) > 0 {
		c.typed.markCodegenUnsupported(fmt.Sprintf("泛型函数 '%s' 尚未在 C11 后端实现单态化", fn.Name), fn.Span)
		return
	}

	// 验证 C11 宿主环境下的 main 入口点 ABI 规范
	if fn.Name == "main" {
		if len(fn.Params) != 0 {
			c.typed.markCodegenUnsupported("C11 后端要求程序入口 'main' 不得接收参数", fn.Span)
			return
		}
		if fnSym.ReturnType != c.types.I32 && fnSym.ReturnType != c.types.Unit {
			c.typed.markCodegenUnsupported(fmt.Sprintf("C11 宿主环境 'main' 入口仅支持返回 'i32' 或 'unit'，当前返回 '%s'", c.types.Format(fnSym.ReturnType)), fn.Span)
			return
		}
	}

	for i, pt := range fnSym.ParamTypes {
		var span = fn.Span
		if i < len(fn.Params) {
			span = fn.Params[i].Span
		}
		if reason := c.c11UnsupportedTypeReason(pt, capPosParam); reason != "" {
			c.typed.markCodegenUnsupported(fmt.Sprintf("函数 '%s' 形参 #%d %s", fn.Name, i+1, reason), span)
			return
		}
	}

	if reason := c.c11UnsupportedTypeReason(fnSym.ReturnType, capPosReturn); reason != "" {
		span := fn.Span
		if fn.ReturnTypeAST != nil {
			span = fn.ReturnTypeAST.GetSpan()
		}
		c.typed.markCodegenUnsupported(fmt.Sprintf("函数 '%s' 返回值 %s", fn.Name, reason), span)
		return
	}

	if fn.Body != nil {
		c.verifyBlockBackendCaps(fn.Body, sliceAliasVars, false)
	}
}

func (c *Checker) verifyBlockBackendCaps(block *ast.BlockStmt, sliceAliasVars map[symbols.SymbolID]bool, inNestedExpr bool) {
	if block == nil || !c.typed.CodegenSupported {
		return
	}
	for _, stmt := range block.Statements {
		c.verifyStmtBackendCaps(stmt, sliceAliasVars, inNestedExpr)
		if !c.typed.CodegenSupported {
			return
		}
	}
}

func (c *Checker) verifyStmtBackendCaps(stmt ast.Stmt, sliceAliasVars map[symbols.SymbolID]bool, inNestedExpr bool) {
	if stmt == nil || !c.typed.CodegenSupported {
		return
	}
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		c.verifyBlockBackendCaps(s, sliceAliasVars, inNestedExpr)

	case *ast.LetStmt:
		if s.Pattern != nil {
			if _, ok := s.Pattern.(*ast.IdentPattern); !ok {
				if _, isW := s.Pattern.(*ast.WildcardPattern); !isW {
					c.typed.markCodegenUnsupported("let/var 解构模式绑定尚未在 C11 后端实现代码生成", s.Span)
					return
				}
			}
		}

		sym := c.symbols.NodeSymbols[s]
		if sym == nil && s.Pattern != nil {
			sym = c.symbols.NodeSymbols[s.Pattern]
		}
		varType := types.Invalid
		if sym != nil {
			varType = sym.Type
		} else if s.Init != nil {
			varType = c.typed.ExprTypes[s.Init]
		}

		// 特例兼容 Phase 0B 模式: let input_bytes = msg.as_bytes()，仅当随后用于 .as_ptr() 调用时可降低为 const uint8_t*
		if mc, ok := s.Init.(*ast.MethodCallExpr); ok && mc.Method == "as_bytes" && sym != nil {
			sliceAliasVars[sym.ID] = true
			c.verifyExprBackendCaps(mc.Receiver, sliceAliasVars, true)
			return
		}

		if arr, ok := s.Init.(*ast.ArrayRepeatLiteral); ok {
			arrInfo := c.types.Info(varType)
			if arrInfo.Kind != types.KindArray || arr.Count < 0 || !c.isC11Scalar(arrInfo.Elem) {
				c.typed.markCodegenUnsupported(fmt.Sprintf("局部数组声明类型 '%s' 尚未在当前 C11 后端实现代码生成", c.types.Format(varType)), s.Span)
				return
			}
			c.verifyExprBackendCaps(arr.Elem, sliceAliasVars, true)
			return
		}

		if reason := c.c11UnsupportedTypeReason(varType, capPosLocal); reason != "" {
			c.typed.markCodegenUnsupported(fmt.Sprintf("局部变量 '%s' %s", s.Name, reason), s.Span)
			return
		}

		if s.Init == nil {
			return
		}
		if u, ok := s.Init.(*ast.UnsafeExpr); ok {
			c.verifyBlockBackendCaps(u.Block, sliceAliasVars, false)
			return
		}
		c.verifyExprBackendCaps(s.Init, sliceAliasVars, true)

	case *ast.AssignStmt:
		lhs := s.TargetExpr
		if lhs == nil && s.Target != "" {
			lhs = &ast.Identifier{Name: s.Target, Span: s.Span}
		}
		switch lhsNode := lhs.(type) {
		case *ast.Identifier:
			// 合法直接变量赋值
		case *ast.UnaryExpr:
			if lhsNode.Op != "*" {
				c.typed.markCodegenUnsupported("不支持该左值表达式的 C11 赋值代码生成", s.Span)
				return
			}
			c.verifyExprBackendCaps(lhsNode.Expr, sliceAliasVars, true)
		default:
			c.typed.markCodegenUnsupported("成员字段/数组下标赋值尚未在 C11 后端实现代码生成", s.Span)
			return
		}

		if s.Op == "%=" {
			vt := c.typed.ExprTypes[s.Value]
			if vt != types.Invalid && c.types.IsFloat(vt) {
				c.typed.markCodegenUnsupported("ISO C11 不支持对浮点类型执行 '%%=' 取模赋值", s.Span)
				return
			}
		}
		if u, ok := s.Value.(*ast.UnsafeExpr); ok {
			c.verifyBlockBackendCaps(u.Block, sliceAliasVars, false)
			return
		}
		c.verifyExprBackendCaps(s.Value, sliceAliasVars, true)

	case *ast.IfStmt:
		c.verifyExprBackendCaps(s.Cond, sliceAliasVars, true)
		c.verifyBlockBackendCaps(s.Then, sliceAliasVars, false)
		if s.Else != nil {
			c.verifyStmtBackendCaps(s.Else, sliceAliasVars, false)
		}

	case *ast.WhileStmt:
		c.verifyExprBackendCaps(s.Cond, sliceAliasVars, true)
		c.verifyBlockBackendCaps(s.Body, sliceAliasVars, false)

	case *ast.BreakStmt, *ast.ContinueStmt:
		// C11 直接对应 break; / continue;

	case *ast.ReturnStmt:
		if s.Value != nil {
			c.verifyExprBackendCaps(s.Value, sliceAliasVars, true)
		}

	case *ast.ExprStmt:
		if u, ok := s.Expr.(*ast.UnsafeExpr); ok {
			c.verifyBlockBackendCaps(u.Block, sliceAliasVars, false)
			return
		}
		c.verifyExprBackendCaps(s.Expr, sliceAliasVars, inNestedExpr)

	default:
		c.typed.markCodegenUnsupported(fmt.Sprintf("语句 %T 尚未在当前 C11 后端实现代码生成", stmt), stmt.GetSpan())
	}
}

func (c *Checker) verifyExprBackendCaps(expr ast.Expr, sliceAliasVars map[symbols.SymbolID]bool, inNestedExpr bool) {
	if expr == nil || !c.typed.CodegenSupported {
		return
	}

	tid := c.typed.ExprTypes[expr]

	switch e := expr.(type) {
	case *ast.IntegerLiteral:
		if !e.Valid || e.Value == nil {
			c.typed.markCodegenUnsupported("非法的整数字面量无法生成 C11 代码", e.Span)
			return
		}
		if reason := c.c11UnsupportedTypeReason(tid, capPosValue); reason != "" {
			c.typed.markCodegenUnsupported(fmt.Sprintf("整型字面量 '%s' %s", e.Raw, reason), e.Span)
		}

	case *ast.FloatLiteral, *ast.BoolLiteral, *ast.CharLiteral, *ast.StringLiteral:
		if reason := c.c11UnsupportedTypeReason(tid, capPosValue); reason != "" {
			c.typed.markCodegenUnsupported(fmt.Sprintf("字面量表达式 %s", reason), e.GetSpan())
		}

	case *ast.Identifier:
		sym := c.symbols.NodeSymbols[e]
		if sym != nil && sliceAliasVars[sym.ID] {
			c.typed.markCodegenUnsupported(fmt.Sprintf("字节切片视图变量 '%s' (&[u8]) 在当前 C11 后端仅允许调用 .as_ptr()", e.Name), e.Span)
			return
		}
		if reason := c.c11UnsupportedTypeReason(tid, capPosValue); reason != "" {
			c.typed.markCodegenUnsupported(fmt.Sprintf("标识符 '%s' %s", e.Name, reason), e.Span)
		}

	case *ast.UnaryExpr:
		if e.Op == "-" {
			if _, isIntLit := e.Expr.(*ast.IntegerLiteral); isIntLit {
				if reason := c.c11UnsupportedTypeReason(tid, capPosValue); reason != "" {
					c.typed.markCodegenUnsupported(fmt.Sprintf("有符号最小整数常量 %s", reason), e.Span)
				}
				return
			}
		}
		c.verifyExprBackendCaps(e.Expr, sliceAliasVars, true)
		if reason := c.c11UnsupportedTypeReason(tid, capPosValue); reason != "" {
			c.typed.markCodegenUnsupported(fmt.Sprintf("一元表达式 '%s' 结果 %s", e.Op, reason), e.Span)
		}

	case *ast.BorrowExpr:
		c.verifyExprBackendCaps(e.Expr, sliceAliasVars, true)
		if reason := c.c11UnsupportedTypeReason(tid, capPosValue); reason != "" {
			c.typed.markCodegenUnsupported(fmt.Sprintf("引用表达式 %s", reason), e.Span)
		}

	case *ast.BinaryExpr:
		c.verifyExprBackendCaps(e.Left, sliceAliasVars, true)
		c.verifyExprBackendCaps(e.Right, sliceAliasVars, true)
		if !c.typed.CodegenSupported {
			return
		}
		lt := c.typed.ExprTypes[e.Left]
		rt := c.typed.ExprTypes[e.Right]
		if e.Op == "%" && (c.types.IsFloat(lt) || c.types.IsFloat(rt)) {
			c.typed.markCodegenUnsupported("ISO C11 不支持对浮点类型 (f32/f64) 直接使用 '%%' 取模运算符", e.Span)
			return
		}
		if e.Op == "==" || e.Op == "!=" {
			if lt == c.types.StrView || rt == c.types.StrView ||
				c.types.Info(lt).Kind == types.KindUnit ||
				c.types.Info(lt).Kind == types.KindString ||
				c.types.Info(lt).Kind == types.KindEnum {
				c.typed.markCodegenUnsupported(fmt.Sprintf("类型 '%s' 的值相等比较 '%s' 尚未在当前 C11 后端实现", c.types.Format(lt), e.Op), e.Span)
				return
			}
		}

	case *ast.CallExpr:
		switch e.Callee {
		case "println", "print":
			if e.Callee == "print" {
				c.typed.markCodegenUnsupported("内置 print 尚未在当前 C11 后端实现 (请使用 println)", e.Span)
				return
			}
			for _, arg := range e.Args {
				c.verifyExprBackendCaps(arg, sliceAliasVars, true)
			}
			return
		case "bytes_equal":
			for _, arg := range e.Args {
				c.verifyExprBackendCaps(arg, sliceAliasVars, true)
			}
			return
		case "panic", "assert":
			c.typed.markCodegenUnsupported(fmt.Sprintf("内置宏/函数 '%s' 尚未在当前 C11 后端实现", e.Callee), e.Span)
			return
		}
		sym := c.typed.CallTargets[e]
		if sym == nil || sym.Kind == symbols.SymEnumVariant {
			c.typed.markCodegenUnsupported(fmt.Sprintf("函数或变体构造 '%s' 尚未在当前 C11 后端实现", e.Callee), e.Span)
			return
		}
		for _, arg := range e.Args {
			c.verifyExprBackendCaps(arg, sliceAliasVars, true)
		}
		if reason := c.c11UnsupportedTypeReason(tid, capPosValue); reason != "" && tid != c.types.Unit {
			c.typed.markCodegenUnsupported(fmt.Sprintf("调用 '%s' 返回 %s", e.Callee, reason), e.Span)
		}

	case *ast.NamespacedCallExpr:
		for _, arg := range e.Args {
			c.verifyExprBackendCaps(arg, sliceAliasVars, true)
		}
		if reason := c.c11UnsupportedTypeReason(tid, capPosValue); reason != "" && tid != c.types.Unit {
			c.typed.markCodegenUnsupported(fmt.Sprintf("FFI 调用 '%s::%s' %s", e.Namespace, e.Function, reason), e.Span)
		}

	case *ast.MethodCallExpr:
		if len(e.Args) > 0 {
			c.typed.markCodegenUnsupported(fmt.Sprintf("带实参的方法 '.%s(...)' 尚未在 C11 后端实现", e.Method), e.Span)
			return
		}
		// 若接收者为 .as_bytes() 别名变量且调用 .as_ptr()，则属于明确支持的 Phase 0B C11 习惯用法
		if e.Method == "as_ptr" {
			if id, ok := e.Receiver.(*ast.Identifier); ok {
				if sym := c.symbols.NodeSymbols[id]; sym != nil && sliceAliasVars[sym.ID] {
					return
				}
			}
			if innerMC, ok := e.Receiver.(*ast.MethodCallExpr); ok && innerMC.Method == "as_bytes" {
				c.verifyExprBackendCaps(innerMC.Receiver, sliceAliasVars, true)
				return
			}
		}

		recT := c.typed.ExprTypes[e.Receiver]
		recInfo := c.types.Info(recT)
		isScalarArray := recInfo.Kind == types.KindArray && c.isC11Scalar(recInfo.Elem)

		// 若接收者是定长标量数组的标识符，则允许作为 .as_ptr() / .as_mut_ptr() / .len() 的方法接收者
		if _, isIdent := e.Receiver.(*ast.Identifier); !(isIdent && isScalarArray) {
			c.verifyExprBackendCaps(e.Receiver, sliceAliasVars, true)
			if !c.typed.CodegenSupported {
				return
			}
		}

		switch e.Method {
		case "as_bytes":
			if recT != c.types.StrView {
				c.typed.markCodegenUnsupported("目前仅支持在字符串字面量/视图 (&str) 上调用 .as_bytes()", e.Span)
			}
		case "as_ptr":
			if recT != c.types.StrView && !isScalarArray {
				c.typed.markCodegenUnsupported(fmt.Sprintf("当前 C11 后端暂不支持在 '%s' 上调用 .as_ptr()", c.types.Format(recT)), e.Span)
			}
		case "as_mut_ptr":
			if !isScalarArray {
				c.typed.markCodegenUnsupported(fmt.Sprintf("当前 C11 后端仅支持在可变局部标量数组上调用 .as_mut_ptr()，不支持 '%s'", c.types.Format(recT)), e.Span)
			}
		case "len":
			if !isScalarArray {
				c.typed.markCodegenUnsupported(fmt.Sprintf("当前 C11 后端仅支持定长标量数组 [T; N].len()，不支持对 '%s' 调用 .len() (禁止对指针使用 sizeof)", c.types.Format(recT)), e.Span)
			}
		default:
			c.typed.markCodegenUnsupported(fmt.Sprintf("方法调用 '.%s()' 尚未在当前 C11 后端实现代码生成", e.Method), e.Span)
		}

	case *ast.UnsafeExpr:
		if inNestedExpr {
			if e.Block == nil || len(e.Block.Statements) != 1 {
				c.typed.markCodegenUnsupported("不支持在嵌套子表达式位置使用多语句 unsafe 块: 请使用独立语句 'let x = unsafe { ... }' 声明", e.Span)
				return
			}
			if es, ok := e.Block.Statements[0].(*ast.ExprStmt); ok {
				c.verifyExprBackendCaps(es.Expr, sliceAliasVars, true)
				return
			}
			c.typed.markCodegenUnsupported("嵌套子表达式 unsafe 块必须仅包含单个表达式语句", e.Span)
			return
		}
		c.verifyBlockBackendCaps(e.Block, sliceAliasVars, false)

	default:
		c.typed.markCodegenUnsupported(fmt.Sprintf("表达式 '%T' 尚未在当前 C11 后端实现代码生成", expr), expr.GetSpan())
	}
}

func (c *Checker) isC11Scalar(id types.TypeID) bool {
	if id == types.Invalid {
		return false
	}
	k := c.types.Info(id).Kind
	switch k {
	case types.KindBool, types.KindChar,
		types.KindI8, types.KindI16, types.KindI32, types.KindI64, types.KindISize,
		types.KindU8, types.KindU16, types.KindU32, types.KindU64, types.KindUSize,
		types.KindF32, types.KindF64:
		return true
	default:
		return false
	}
}

func (c *Checker) c11UnsupportedTypeReason(id types.TypeID, pos capTypePos) string {
	if id == types.Invalid {
		return "类型无效或未解析"
	}
	info := c.types.Info(id)
	switch info.Kind {
	case types.KindNever:
		return ""
	case types.KindUnit:
		if pos == capPosReturn {
			return ""
		}
		return "类型为 'unit'，不能在非返回值上下文作为 C11 对象使用"
	case types.KindI128, types.KindU128:
		return fmt.Sprintf("使用 128 位整数类型 '%s'，目前无法可移植降低至严格 ISO C11", c.types.Format(id))
	case types.KindRef:
		if id == c.types.StrView {
			return ""
		}
		if c.isC11Scalar(info.Elem) || c.opaqueForeignTypes[info.Elem] != "" {
			return ""
		}
		return fmt.Sprintf("使用引用类型 '%s'，当前 C11 后端尚不支持该底层目标类型", c.types.Format(id))
	case types.KindRawPtr:
		if c.isC11Scalar(info.Elem) || c.types.Info(info.Elem).Kind == types.KindUnit || c.opaqueForeignTypes[info.Elem] != "" {
			return ""
		}
		return fmt.Sprintf("使用原生指针类型 '%s'，当前 C11 后端尚不支持该指向类型", c.types.Format(id))
	case types.KindStruct:
		if c.opaqueForeignTypes[id] != "" {
			return ""
		}
		return fmt.Sprintf("使用结构体类型 '%s'，尚未在当前 C11 后端实现映射与代码生成", c.types.Format(id))
	case types.KindArray:
		return fmt.Sprintf("使用定长数组类型 '%s'，不能按值作为参数、返回值或常规表达式传递", c.types.Format(id))
	default:
		if c.isC11Scalar(id) {
			return ""
		}
		return fmt.Sprintf("使用类型 '%s'，尚未在当前 C11 后端实现映射与代码生成", c.types.Format(id))
	}
}

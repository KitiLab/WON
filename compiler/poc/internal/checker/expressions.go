package checker

import (
	"fmt"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// checkExpr 对表达式执行双向类型推导与语义检验，并将解析得到的 TypeID 记录至 TypedProgram
func (c *Checker) checkExpr(expr ast.Expr, expected types.TypeID, scope *symbols.Scope) types.TypeID {
	if expr == nil {
		return c.types.Unit
	}

	tid := c.evalExpr(expr, expected, scope)
	if c.typed != nil {
		c.typed.ExprTypes[expr] = tid
	}
	return tid
}

func (c *Checker) evalExpr(expr ast.Expr, expected types.TypeID, scope *symbols.Scope) types.TypeID {
	switch e := expr.(type) {
	case *ast.IntegerLiteral:
		return c.checkIntegerLiteral(e, expected)

	case *ast.FloatLiteral:
		if e.Suffix != "" {
			return c.symbols.ResolveTypeString(e.Suffix, scope)
		}
		if expected != types.Invalid && c.types.IsFloat(expected) {
			return expected
		}
		return c.types.F64

	case *ast.BoolLiteral:
		return c.types.Bool

	case *ast.CharLiteral:
		return c.types.Char

	case *ast.ByteLiteral:
		return c.types.U8

	case *ast.StringLiteral:
		return c.types.StrView

	case *ast.InterpolatedStringExpr:
		for _, part := range e.Parts {
			if part.IsExpr && part.Expr != nil {
				c.checkExpr(part.Expr, types.Invalid, scope)
			}
		}
		c.typed.markCodegenUnsupported("插值字符串格式化尚未在 C11 后端实现代码生成", e.Span)
		return c.types.StringT

	case *ast.ArrayRepeatLiteral:
		expectedElem := types.Invalid
		if expected != types.Invalid && c.types.Info(expected).Kind == types.KindArray {
			expectedElem = c.types.Info(expected).Elem
		}
		elemType := c.checkExpr(e.Elem, expectedElem, scope)
		if e.Count < 0 {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-004",
				Severity: "error",
				Message:  fmt.Sprintf("定长数组重复长度不能为负数 (%d)", e.Count),
				File:     e.Span.File,
				Span:     e.Span,
			})
			return types.Invalid
		}
		return c.types.Array(elemType, e.Count)

	case *ast.ArrayLiteral:
		c.typed.markCodegenUnsupported("枚举式数组字面量尚未在 C11 后端实现代码生成", e.Span)
		if len(e.Elems) == 0 {
			if expected != types.Invalid && c.types.Info(expected).Kind == types.KindArray {
				return expected
			}
			return c.types.Array(types.Invalid, 0)
		}
		expectedElem := types.Invalid
		if expected != types.Invalid && c.types.Info(expected).Kind == types.KindArray {
			expectedElem = c.types.Info(expected).Elem
		}
		firstType := c.checkExpr(e.Elems[0], expectedElem, scope)
		for i := 1; i < len(e.Elems); i++ {
			et := c.checkExpr(e.Elems[i], firstType, scope)
			if firstType != types.Invalid && et != types.Invalid && !c.types.CanAssign(et, firstType) {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-TYP-001",
					Severity: "error",
					Message:  fmt.Sprintf("数组元素类型不一致: 期望 '%s', 实际为 '%s'", c.types.Format(firstType), c.types.Format(et)),
					File:     e.Elems[i].GetSpan().File,
					Span:     e.Elems[i].GetSpan(),
				})
			}
		}
		return c.types.Array(firstType, int64(len(e.Elems)))

	case *ast.TupleLiteral:
		if len(e.Elems) == 0 {
			return c.types.Unit
		}
		c.typed.markCodegenUnsupported("多元素元组尚未在 C11 后端实现代码生成", e.Span)
		var expElems []types.TypeID
		if expected != types.Invalid && c.types.Info(expected).Kind == types.KindTuple {
			expElems = c.types.Info(expected).Elems
		}
		elemTypes := make([]types.TypeID, len(e.Elems))
		for i, sub := range e.Elems {
			exp := types.Invalid
			if i < len(expElems) {
				exp = expElems[i]
			}
			elemTypes[i] = c.checkExpr(sub, exp, scope)
		}
		return c.types.Tuple(elemTypes)

	case *ast.Identifier:
		return c.checkIdentifier(e, expected, scope)

	case *ast.PathExpr:
		return c.checkPathExpr(e, expected, scope)

	case *ast.StructLiteral:
		c.typed.markCodegenUnsupported("结构体/枚举结构体字面量尚未在 C11 后端实现代码生成", e.Span)
		return c.checkStructLiteral(e, expected, scope)

	case *ast.FieldAccessExpr:
		c.typed.markCodegenUnsupported("结构体成员字段访问尚未在 C11 后端实现代码生成", e.Span)
		objType := c.checkExpr(e.Object, types.Invalid, scope)
		fi := c.resolveField(objType, e.Field, e.Span)
		if fi != nil {
			c.typed.FieldRefs[e] = fi
			return fi.Type
		}
		return types.Invalid

	case *ast.IndexExpr:
		c.typed.markCodegenUnsupported("下标索引与切片尚未在 C11 后端实现代码生成", e.Span)
		objType := c.checkExpr(e.Object, types.Invalid, scope)
		idxType := c.checkExpr(e.Index, c.types.USize, scope)
		if _, isRng := e.Index.(*ast.RangeExpr); isRng {
			elem := c.unwrapIndexElement(objType, e.Span)
			if elem == types.Invalid {
				return types.Invalid
			}
			return c.types.Ref(false, c.types.Slice(elem))
		}
		if idxType != types.Invalid && !c.types.IsInteger(idxType) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-002",
				Severity: "error",
				Message:  fmt.Sprintf("下标索引表达式必须为整数类型，实际为 '%s'", c.types.Format(idxType)),
				File:     e.Index.GetSpan().File,
				Span:     e.Index.GetSpan(),
			})
		}
		return c.unwrapIndexElement(objType, e.Span)

	case *ast.RangeExpr:
		c.typed.markCodegenUnsupported("范围表达式 '..' 尚未在 C11 后端实现代码生成", e.Span)
		elemExp := types.Invalid
		if expected != types.Invalid && c.types.IsInteger(expected) {
			elemExp = expected
		}
		var startT, endT types.TypeID
		if e.Start != nil {
			startT = c.checkExpr(e.Start, elemExp, scope)
			if startT != types.Invalid && !c.types.IsInteger(startT) {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-TYP-002",
					Severity: "error",
					Message:  fmt.Sprintf("范围表达式起点必须为整数类型，实际为 '%s'", c.types.Format(startT)),
					File:     e.Start.GetSpan().File,
					Span:     e.Start.GetSpan(),
				})
			}
		}
		if e.End != nil {
			hint := startT
			if hint == types.Invalid {
				hint = elemExp
			}
			endT = c.checkExpr(e.End, hint, scope)
			if endT != types.Invalid && !c.types.IsInteger(endT) {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-TYP-002",
					Severity: "error",
					Message:  fmt.Sprintf("范围表达式终点必须为整数类型，实际为 '%s'", c.types.Format(endT)),
					File:     e.End.GetSpan().File,
					Span:     e.End.GetSpan(),
				})
			}
		}
		if startT != types.Invalid && endT != types.Invalid && startT != endT {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-001",
				Severity: "error",
				Message:  fmt.Sprintf("范围上下界类型不一致: '%s' 与 '%s'", c.types.Format(startT), c.types.Format(endT)),
				File:     e.Span.File,
				Span:     e.Span,
			})
		}
		if startT != types.Invalid {
			return startT
		}
		if endT != types.Invalid {
			return endT
		}
		return c.types.I64

	case *ast.CastExpr:
		c.typed.markCodegenUnsupported("显式类型转换 'as' 尚未在 C11 后端实现代码生成", e.Span)
		srcType := c.checkExpr(e.Expr, types.Invalid, scope)
		targetType := c.resolveAstType(e.TargetType, scope)
		if srcType != types.Invalid && targetType != types.Invalid && !c.types.CanCast(srcType, targetType) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-005",
				Severity: "error",
				Message:  fmt.Sprintf("非法的显式类型转换: 无法通过 'as' 将 '%s' 转换为 '%s'", c.types.Format(srcType), c.types.Format(targetType)),
				File:     e.Span.File,
				Span:     e.Span,
			})
		}
		return targetType

	case *ast.UnaryExpr:
		return c.checkUnaryExpr(e, expected, scope)

	case *ast.BorrowExpr:
		return c.checkBorrowExpr(e, scope)

	case *ast.BinaryExpr:
		return c.checkBinaryExpr(e, expected, scope)

	case *ast.TryExpr:
		c.typed.markCodegenUnsupported("'?' 错误传播操作符尚未在 C11 后端实现代码生成", e.Span)
		return c.checkTryExpr(e, scope)

	case *ast.CallExpr:
		return c.checkCallExpr(e, expected, scope)

	case *ast.NamespacedCallExpr:
		return c.checkNamespacedCallExpr(e, expected, scope)

	case *ast.MethodCallExpr:
		return c.checkMethodCallExpr(e, scope)

	case *ast.UnsafeExpr:
		prev := c.inUnsafe
		c.inUnsafe = true
		bt := c.checkBlock(e.Block, expected, scope)
		c.inUnsafe = prev
		return bt

	case *ast.IfStmt:
		c.typed.markCodegenUnsupported("表达式位置 if/else 求值尚未在 C11 后端实现代码生成", e.Span)
		return c.checkIfStmt(e, expected, scope)

	case *ast.MatchExpr:
		return c.checkMatchExpr(e, expected, scope)

	case *ast.ScopeExpr, *ast.SpawnExpr:
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-SEM-010",
			Severity:    "error",
			Message:     "结构化并发原语 (scope/spawn) 已通过语法解析，但并发校验与运行时将在后续阶段实现",
			File:        e.GetSpan().File,
			Span:        e.GetSpan(),
			Explanation: "Phase 2A 不包含 Send/Sync 线程安全分析及协程/线程调度器实现。",
		})
		return types.Invalid

	default:
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-SEM-010",
			Severity: "error",
			Message:  fmt.Sprintf("表达式 %T 尚未在 Phase 2A 实现语义检查", expr),
			File:     expr.GetSpan().File,
			Span:     expr.GetSpan(),
		})
		return types.Invalid
	}
}

func (c *Checker) checkIntegerLiteral(e *ast.IntegerLiteral, expected types.TypeID) types.TypeID {
	if e.Suffix != "" {
		tid := c.symbols.ResolveTypeString(e.Suffix, nil)
		if tid == types.Invalid {
			return c.types.I64
		}
		if !c.types.FitsIntegerLiteral(e.Value, tid) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-003",
				Severity: "error",
				Message:  fmt.Sprintf("整数字面量 '%s' 溢出目标类型 '%s' 的表示范围", e.Raw, c.types.Format(tid)),
				File:     e.Span.File,
				Span:     e.Span,
			})
		}
		return tid
	}

	if expected != types.Invalid && c.types.IsInteger(expected) {
		if !c.types.FitsIntegerLiteral(e.Value, expected) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-TYP-003",
				Severity:    "error",
				Message:     fmt.Sprintf("整数字面量 %d 超出上下文期望类型 '%s' 的合法取值范围", e.Value, c.types.Format(expected)),
				File:        e.Span.File,
				Span:        e.Span,
				Explanation: "常量溢出拒绝属于编译期静态安全保证 (WON-TYP-003)。",
			})
		}
		return expected
	}
	return c.types.I64
}

func (c *Checker) checkIdentifier(e *ast.Identifier, expected types.TypeID, scope *symbols.Scope) types.TypeID {
	sym := scope.Lookup(e.Name)
	if sym == nil {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-NAM-001",
			Severity:    "error",
			Message:     fmt.Sprintf("使用未声明的标识符: %q", e.Name),
			File:        e.Span.File,
			Span:        e.Span,
			Explanation: fmt.Sprintf("当前作用域及其父作用域内均未定义标识符 '%s'。", e.Name),
		})
		return types.Invalid
	}

	c.symbols.NodeSymbols[e] = sym

	if sym.Kind == symbols.SymEnumVariant {
		c.typed.markCodegenUnsupported("枚举单元变体构造尚未在 C11 后端实现代码生成", e.Span)
		if sym.Name == "None" {
			if elem, ok := c.types.IsOption(expected); ok {
				return c.types.Option(elem)
			}
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-ERR-003",
				Severity: "error",
				Message:  "无法仅从孤立 'None' 推断 Option[T] 类型参数，请提供上下文类型标注",
				File:     e.Span.File,
				Span:     e.Span,
			})
			return types.Invalid
		}
	}
	return sym.Type
}

func (c *Checker) checkPathExpr(e *ast.PathExpr, expected types.TypeID, scope *symbols.Scope) types.TypeID {
	if len(e.Segments) == 2 {
		ownerName := e.Segments[0]
		memberName := e.Segments[1]
		if ownerName == "Option" && memberName == "None" {
			c.typed.markCodegenUnsupported("Option::None 尚未在 C11 后端实现代码生成", e.Span)
			if elem, ok := c.types.IsOption(expected); ok {
				return c.types.Option(elem)
			}
			if scope.OwnerFn != nil {
				if elem, ok := c.types.IsOption(scope.OwnerFn.ReturnType); ok {
					return c.types.Option(elem)
				}
			}
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-ERR-003",
				Severity: "error",
				Message:  "无法单独推断 Option::None 的具体元素类型 T，需要显式类型上下文",
				File:     e.Span.File,
				Span:     e.Span,
			})
			return types.Invalid
		}

		sym := scope.Lookup(ownerName)
		if sym == nil {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-NAM-003",
				Severity: "error",
				Message:  fmt.Sprintf("未知的类型或命名空间: %q", ownerName),
				File:     e.Span.File,
				Span:     e.Span,
			})
			return types.Invalid
		}
		if sym.Kind == symbols.SymEnum {
			vi, exists := sym.VariantMap[memberName]
			if !exists {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-ADT-005",
					Severity: "error",
					Message:  fmt.Sprintf("枚举 '%s' 中不存在变体 '%s'", ownerName, memberName),
					File:     e.Span.File,
					Span:     e.Span,
				})
				return types.Invalid
			}
			c.typed.VariantRefs[e] = vi
			c.typed.markCodegenUnsupported("枚举变体值尚未在 C11 后端实现代码生成", e.Span)
			if vi.Kind != symbols.VariantUnit {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-ADT-006",
					Severity: "error",
					Message:  fmt.Sprintf("枚举变体 '%s::%s' 携带载荷，不能省略构造参数", ownerName, memberName),
					File:     e.Span.File,
					Span:     e.Span,
				})
				return types.Invalid
			}
			return sym.Type
		}
	}

	c.report.AddDiagnostic(diagnostic.Diagnostic{
		Code:     "WON-NAM-001",
		Severity: "error",
		Message:  fmt.Sprintf("无法解析限定路径表达式: %q", strings.Join(e.Segments, "::")),
		File:     e.Span.File,
		Span:     e.Span,
	})
	return types.Invalid
}

func (c *Checker) checkStructLiteral(e *ast.StructLiteral, expected types.TypeID, scope *symbols.Scope) types.TypeID {
	// 支持 Enum::StructVariant { f: val } 构造形式
	if len(e.TypePath) == 2 {
		enumSym := scope.Lookup(e.TypePath[0])
		if enumSym != nil && enumSym.Kind == symbols.SymEnum {
			vi, ok := enumSym.VariantMap[e.TypePath[1]]
			if !ok {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-ADT-005",
					Severity: "error",
					Message:  fmt.Sprintf("枚举 '%s' 中不存在变体 '%s'", enumSym.Name, e.TypePath[1]),
					File:     e.Span.File,
					Span:     e.Span,
				})
				return types.Invalid
			}
			c.typed.VariantRefs[e] = vi
			c.checkFieldInitializers(enumSym.Name+"::"+vi.Name, vi.StructFields, vi.FieldMap, e.Fields, scope, e.Span)
			return enumSym.Type
		}
	}

	if len(e.TypePath) != 1 {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-NAM-003",
			Severity: "error",
			Message:  fmt.Sprintf("未知的结构体类型路径: %q", strings.Join(e.TypePath, "::")),
			File:     e.Span.File,
			Span:     e.Span,
		})
		return types.Invalid
	}

	sym := scope.Lookup(e.TypePath[0])
	if sym == nil || sym.Kind != symbols.SymStruct {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-NAM-003",
			Severity: "error",
			Message:  fmt.Sprintf("未定义的结构体类型: %q", e.TypePath[0]),
			File:     e.Span.File,
			Span:     e.Span,
		})
		return types.Invalid
	}

	// 支持泛型结构体通过上下文 expected 实例化
	subst := make(map[uint64]types.TypeID)
	resultType := sym.Type
	if len(sym.GenericParamIDs) > 0 && expected != types.Invalid {
		expInfo := c.types.Info(expected)
		if expInfo.Kind == types.KindStruct && expInfo.SymbolID == uint64(sym.ID) && len(expInfo.GenericArgs) == len(sym.GenericParamIDs) {
			for i, gid := range sym.GenericParamIDs {
				subst[uint64(gid)] = expInfo.GenericArgs[i]
			}
			resultType = expected
		}
	}

	fields := make([]symbols.FieldInfo, len(sym.Fields))
	fMap := make(map[string]*symbols.FieldInfo, len(sym.Fields))
	for i, f := range sym.Fields {
		ft := c.types.Substitute(f.Type, subst)
		fields[i] = symbols.FieldInfo{Name: f.Name, Type: ft, IsPub: f.IsPub, IsVar: f.IsVar, Span: f.Span}
		fMap[f.Name] = &fields[i]
	}

	c.checkFieldInitializers(sym.Name, fields, fMap, e.Fields, scope, e.Span)
	return resultType
}

func (c *Checker) checkFieldInitializers(typeName string, fields []symbols.FieldInfo, fieldMap map[string]*symbols.FieldInfo, inits []*ast.FieldInit, scope *symbols.Scope, litSpan diagnostic.Span) {
	seen := make(map[string]bool, len(inits))
	for _, fi := range inits {
		if seen[fi.Name] {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-ADT-003",
				Severity: "error",
				Message:  fmt.Sprintf("结构体字面量 '%s' 包含重复的字段初始化: '%s'", typeName, fi.Name),
				File:     fi.Span.File,
				Span:     fi.Span,
			})
			continue
		}
		seen[fi.Name] = true

		defField, exists := fieldMap[fi.Name]
		if !exists {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-ADT-001",
				Severity: "error",
				Message:  fmt.Sprintf("结构体 '%s' 不存在名为 '%s' 的字段", typeName, fi.Name),
				File:     fi.Span.File,
				Span:     fi.Span,
			})
			if fi.Value != nil {
				c.checkExpr(fi.Value, types.Invalid, scope)
			}
			continue
		}

		c.typed.FieldRefs[fi] = defField
		var valType types.TypeID
		if fi.Value != nil {
			valType = c.checkExpr(fi.Value, defField.Type, scope)
		} else {
			// 字段简写 Point { x, y } 查找同名变量
			sym := scope.Lookup(fi.Name)
			if sym == nil {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-NAM-001",
					Severity: "error",
					Message:  fmt.Sprintf("字段初始化简写 '%s' 引用了未声明的局部变量", fi.Name),
					File:     fi.Span.File,
					Span:     fi.Span,
				})
				continue
			}
			valType = sym.Type
		}

		if valType != types.Invalid && defField.Type != types.Invalid && !c.types.CanAssign(valType, defField.Type) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-001",
				Severity: "error",
				Message:  fmt.Sprintf("字段 '%s.%s' 类型不匹配: 期望 '%s'，实际为 '%s'", typeName, fi.Name, c.types.Format(defField.Type), c.types.Format(valType)),
				File:     fi.Span.File,
				Span:     fi.Span,
			})
		}
	}

	// 检查是否缺少必填字段
	for _, f := range fields {
		if !seen[f.Name] {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-ADT-002",
				Severity: "error",
				Message:  fmt.Sprintf("构造结构体 '%s' 缺少必填字段 '%s'", typeName, f.Name),
				File:     litSpan.File,
				Span:     litSpan,
			})
		}
	}
}

func (c *Checker) resolveField(objType types.TypeID, fieldName string, span diagnostic.Span) *symbols.FieldInfo {
	if objType == types.Invalid {
		return nil
	}
	baseID := objType
	info := c.types.Info(baseID)
	if info.Kind == types.KindRef {
		baseID = info.Elem
		info = c.types.Info(baseID)
	}
	if info.Kind != types.KindStruct {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-ADT-001",
			Severity: "error",
			Message:  fmt.Sprintf("类型 '%s' 不支持成员字段访问 '.%s'", c.types.Format(objType), fieldName),
			File:     span.File,
			Span:     span,
		})
		return nil
	}
	sym := c.symbols.ByID[symbols.SymbolID(info.SymbolID)]
	if sym == nil {
		return nil
	}
	fi, ok := sym.FieldMap[fieldName]
	if !ok {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-ADT-001",
			Severity: "error",
			Message:  fmt.Sprintf("结构体 '%s' 没有名为 '%s' 的字段", sym.Name, fieldName),
			File:     span.File,
			Span:     span,
		})
		return nil
	}

	if len(sym.GenericParamIDs) == 0 || len(info.GenericArgs) != len(sym.GenericParamIDs) {
		return fi
	}
	subst := make(map[uint64]types.TypeID, len(sym.GenericParamIDs))
	for i, pid := range sym.GenericParamIDs {
		subst[uint64(pid)] = info.GenericArgs[i]
	}
	return &symbols.FieldInfo{
		Name:  fi.Name,
		Type:  c.types.Substitute(fi.Type, subst),
		IsPub: fi.IsPub,
		IsVar: fi.IsVar,
		Span:  fi.Span,
	}
}

func (c *Checker) unwrapIndexElement(objType types.TypeID, span diagnostic.Span) types.TypeID {
	if objType == types.Invalid {
		return types.Invalid
	}
	info := c.types.Info(objType)
	if info.Kind == types.KindRef {
		info = c.types.Info(info.Elem)
	}
	if info.Kind == types.KindArray || info.Kind == types.KindSlice {
		return info.Elem
	}
	c.report.AddDiagnostic(diagnostic.Diagnostic{
		Code:     "WON-TYP-002",
		Severity: "error",
		Message:  fmt.Sprintf("类型 '%s' 不支持下标索引访问", c.types.Format(objType)),
		File:     span.File,
		Span:     span,
	})
	return types.Invalid
}

func (c *Checker) checkUnaryExpr(e *ast.UnaryExpr, expected types.TypeID, scope *symbols.Scope) types.TypeID {
	switch e.Op {
	case "-":
		if intLit, isLit := e.Expr.(*ast.IntegerLiteral); isLit && intLit.Suffix == "" && expected != types.Invalid && c.types.IsInteger(expected) {
			if c.types.IsSigned(expected) {
				negVal := -intLit.Value
				if !c.types.FitsIntegerLiteral(negVal, expected) {
					c.report.AddDiagnostic(diagnostic.Diagnostic{
						Code:        "WON-TYP-003",
						Severity:    "error",
						Message:     fmt.Sprintf("负整数字面量 -%d 超出上下文期望类型 '%s' 的合法取值范围", intLit.Value, c.types.Format(expected)),
						File:        e.Span.File,
						Span:        e.Span,
						Explanation: "常量溢出拒绝属于编译期静态安全保证 (WON-TYP-003)。",
					})
				}
				c.typed.ExprTypes[intLit] = expected
				return expected
			}
		}
		sub := c.checkExpr(e.Expr, expected, scope)
		if sub != types.Invalid && !c.types.IsNumeric(sub) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-002",
				Severity: "error",
				Message:  fmt.Sprintf("一元负号 '-' 不能应用于非数值类型 '%s'", c.types.Format(sub)),
				File:     e.Span.File,
				Span:     e.Span,
			})
			return types.Invalid
		}
		if sub != types.Invalid && c.types.IsUnsigned(sub) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-002",
				Severity: "error",
				Message:  fmt.Sprintf("一元负号 '-' 不能应用于无符号整数类型 '%s'", c.types.Format(sub)),
				File:     e.Span.File,
				Span:     e.Span,
			})
			return types.Invalid
		}
		return sub

	case "not", "!":
		sub := c.checkExpr(e.Expr, c.types.Bool, scope)
		if sub != types.Invalid && sub != c.types.Bool {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-002",
				Severity: "error",
				Message:  fmt.Sprintf("逻辑非运算符 '%s' 只能作用于 bool 类型，实际为 '%s'", e.Op, c.types.Format(sub)),
				File:     e.Span.File,
				Span:     e.Span,
			})
			return types.Invalid
		}
		return c.types.Bool

	case "~":
		sub := c.checkExpr(e.Expr, expected, scope)
		if sub != types.Invalid && !c.types.IsInteger(sub) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-002",
				Severity: "error",
				Message:  fmt.Sprintf("按位取反 '~' 只能作用于整数类型，实际为 '%s'", c.types.Format(sub)),
				File:     e.Span.File,
				Span:     e.Span,
			})
			return types.Invalid
		}
		return sub

	case "*":
		sub := c.checkExpr(e.Expr, types.Invalid, scope)
		sInfo := c.types.Info(sub)
		if sInfo.Kind == types.KindRef || sInfo.Kind == types.KindRawPtr {
			return sInfo.Elem
		}
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-TYP-002",
			Severity: "error",
			Message:  fmt.Sprintf("不能对非指针/非引用类型 '%s' 执行解引用 '*'", c.types.Format(sub)),
			File:     e.Span.File,
			Span:     e.Span,
		})
		return types.Invalid
	}
	return c.checkExpr(e.Expr, expected, scope)
}

func (c *Checker) checkBorrowExpr(e *ast.BorrowExpr, scope *symbols.Scope) types.TypeID {
	sub := c.checkExpr(e.Expr, types.Invalid, scope)
	if e.IsMut {
		if ident, ok := e.Expr.(*ast.Identifier); ok {
			sym := scope.Lookup(ident.Name)
			if sym != nil && !sym.IsMut {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:        "WON-MUT-001",
					Severity:    "error",
					Message:     fmt.Sprintf("无法对不可变变量 '%s' 进行可变借用 (&mut)", ident.Name),
					File:        e.Span.File,
					Span:        e.Span,
					Explanation: fmt.Sprintf("变量 '%s' 使用 'let' 声明为不可变。向需要修改内容的函数传递 &mut 引用要求使用 'var' 声明。", ident.Name),
					Fixes: []diagnostic.Fix{
						{
							Description: fmt.Sprintf("将变量 '%s' 声明修改为可变 ('var')", ident.Name),
							Edits: []diagnostic.Edit{
								{
									Start:       sym.LetTokenSpan.Start.Byte,
									End:         sym.LetTokenSpan.End.Byte,
									Replacement: "var",
								},
							},
						},
					},
				})
			}
		}
	}
	if sub == types.Invalid {
		return types.Invalid
	}
	return c.types.Ref(e.IsMut, sub)
}

func (c *Checker) checkBinaryExpr(e *ast.BinaryExpr, expected types.TypeID, scope *symbols.Scope) types.TypeID {
	switch e.Op {
	case "and", "or":
		lt := c.checkExpr(e.Left, c.types.Bool, scope)
		rt := c.checkExpr(e.Right, c.types.Bool, scope)
		if lt != types.Invalid && lt != c.types.Bool {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-002",
				Severity: "error",
				Message:  fmt.Sprintf("逻辑运算符 '%s' 左操作数必须为 bool，实际为 '%s'", e.Op, c.types.Format(lt)),
				File:     e.Left.GetSpan().File,
				Span:     e.Left.GetSpan(),
			})
		}
		if rt != types.Invalid && rt != c.types.Bool {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-002",
				Severity: "error",
				Message:  fmt.Sprintf("逻辑运算符 '%s' 右操作数必须为 bool，实际为 '%s'", e.Op, c.types.Format(rt)),
				File:     e.Right.GetSpan().File,
				Span:     e.Right.GetSpan(),
			})
		}
		return c.types.Bool

	case "==", "!=", "<", "<=", ">", ">=":
		// 若一侧为无后缀字面量，先推断非字面量侧并向下传播预期类型
		lt, rt := c.checkComparableOperands(e.Left, e.Right, scope)
		if e.Op != "==" && e.Op != "!=" {
			if lt != types.Invalid && !c.types.IsNumeric(lt) && c.types.Info(lt).Kind != types.KindChar {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-TYP-002",
					Severity: "error",
					Message:  fmt.Sprintf("关系比较运算符 '%s' 不支持类型 '%s'", e.Op, c.types.Format(lt)),
					File:     e.Span.File,
					Span:     e.Span,
				})
			}
		}
		if lt != types.Invalid && rt != types.Invalid && !c.types.CanAssign(rt, lt) && !c.types.CanAssign(lt, rt) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-001",
				Severity: "error",
				Message:  fmt.Sprintf("比较运算符 '%s' 两侧操作数类型不兼容: '%s' 与 '%s'", e.Op, c.types.Format(lt), c.types.Format(rt)),
				File:     e.Span.File,
				Span:     e.Span,
			})
		}
		return c.types.Bool

	case "+", "-", "*", "/", "%":
		lt, rt := c.checkSymmetricOperands(e.Left, e.Right, expected, scope)
		if lt != types.Invalid && !c.types.IsNumeric(lt) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-002",
				Severity: "error",
				Message:  fmt.Sprintf("算术运算符 '%s' 不能作用于非数值类型 '%s'", e.Op, c.types.Format(lt)),
				File:     e.Left.GetSpan().File,
				Span:     e.Left.GetSpan(),
			})
			return types.Invalid
		}
		if lt != types.Invalid && rt != types.Invalid && lt != rt {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-001",
				Severity: "error",
				Message:  fmt.Sprintf("算术运算符 '%s' 两侧操作数类型不匹配: '%s' 与 '%s' (禁止隐式转换)", e.Op, c.types.Format(lt), c.types.Format(rt)),
				File:     e.Span.File,
				Span:     e.Span,
			})
			return types.Invalid
		}
		return lt

	case "&", "|", "^", "<<", ">>":
		if e.Op == "<<" || e.Op == ">>" {
			lt := c.checkExpr(e.Left, expected, scope)
			rt := c.checkExpr(e.Right, types.Invalid, scope)
			if lt != types.Invalid && !c.types.IsInteger(lt) {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-TYP-002",
					Severity: "error",
					Message:  fmt.Sprintf("位移运算符 '%s' 左操作数必须为整数类型，实际为 '%s'", e.Op, c.types.Format(lt)),
					File:     e.Left.GetSpan().File,
					Span:     e.Left.GetSpan(),
				})
				return types.Invalid
			}
			if rt != types.Invalid && !c.types.IsInteger(rt) {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-TYP-002",
					Severity: "error",
					Message:  fmt.Sprintf("位移位数必须为整数类型，实际为 '%s'", c.types.Format(rt)),
					File:     e.Right.GetSpan().File,
					Span:     e.Right.GetSpan(),
				})
			}
			return lt
		}

		lt, rt := c.checkSymmetricOperands(e.Left, e.Right, expected, scope)
		if lt != types.Invalid && !c.types.IsInteger(lt) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-002",
				Severity: "error",
				Message:  fmt.Sprintf("位运算符 '%s' 只能作用于整数类型，实际为 '%s'", e.Op, c.types.Format(lt)),
				File:     e.Left.GetSpan().File,
				Span:     e.Left.GetSpan(),
			})
			return types.Invalid
		}
		if lt != types.Invalid && rt != types.Invalid && lt != rt {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-001",
				Severity: "error",
				Message:  fmt.Sprintf("位运算符 '%s' 两侧整数类型必须一致: '%s' 与 '%s'", e.Op, c.types.Format(lt), c.types.Format(rt)),
				File:     e.Span.File,
				Span:     e.Span,
			})
			return types.Invalid
		}
		return lt
	}

	return types.Invalid
}

func isUnsuffixedLiteral(e ast.Expr) bool {
	if il, ok := e.(*ast.IntegerLiteral); ok && il.Suffix == "" {
		return true
	}
	if fl, ok := e.(*ast.FloatLiteral); ok && fl.Suffix == "" {
		return true
	}
	return false
}

func (c *Checker) checkComparableOperands(left, right ast.Expr, scope *symbols.Scope) (types.TypeID, types.TypeID) {
	if isUnsuffixedLiteral(left) && !isUnsuffixedLiteral(right) {
		rt := c.checkExpr(right, types.Invalid, scope)
		lt := c.checkExpr(left, rt, scope)
		return lt, rt
	}
	lt := c.checkExpr(left, types.Invalid, scope)
	hint := types.Invalid
	if isUnsuffixedLiteral(right) {
		hint = lt
	}
	rt := c.checkExpr(right, hint, scope)
	return lt, rt
}

func (c *Checker) checkSymmetricOperands(left, right ast.Expr, expected types.TypeID, scope *symbols.Scope) (types.TypeID, types.TypeID) {
	if expected != types.Invalid {
		lt := c.checkExpr(left, expected, scope)
		rt := c.checkExpr(right, expected, scope)
		return lt, rt
	}
	return c.checkComparableOperands(left, right, scope)
}

func (c *Checker) checkTryExpr(e *ast.TryExpr, scope *symbols.Scope) types.TypeID {
	subType := c.checkExpr(e.Expr, types.Invalid, scope)
	if subType == types.Invalid {
		return types.Invalid
	}
	if scope.OwnerFn == nil {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-ERR-001",
			Severity: "error",
			Message:  "'?' 错误传播操作符只能用在函数体内",
			File:     e.Span.File,
			Span:     e.Span,
		})
		return types.Invalid
	}

	fnRet := scope.OwnerFn.ReturnType

	// 1. Option[T]? 校验
	if optElem, ok := c.types.IsOption(subType); ok {
		if _, retIsOpt := c.types.IsOption(fnRet); !retIsOpt {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-ERR-001",
				Severity:    "error",
				Message:     fmt.Sprintf("不能在返回 '%s' 的函数内使用 '?' 传播 Option[%s]", c.types.Format(fnRet), c.types.Format(optElem)),
				File:        e.Span.File,
				Span:        e.Span,
				Explanation: "对 Option[T] 使用 '?' 要求外层宿主函数也返回 Option[U]。",
			})
			return types.Invalid
		}
		return optElem
	}

	// 2. Result[T, E]? 校验（Phase 2A 要求错误类型 E 严格一致）
	if okType, errType, ok := c.types.IsResult(subType); ok {
		_, fnErrType, retIsRes := c.types.IsResult(fnRet)
		if !retIsRes {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-ERR-001",
				Severity:    "error",
				Message:     fmt.Sprintf("不能在返回 '%s' 的函数内使用 '?' 传播 Result", c.types.Format(fnRet)),
				File:        e.Span.File,
				Span:        e.Span,
				Explanation: "对 Result[T, E] 使用 '?' 要求外层宿主函数返回兼容的 Result[U, E]。",
			})
			return types.Invalid
		}
		if errType != types.Invalid && fnErrType != types.Invalid && !c.types.Equal(errType, fnErrType) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-ERR-001",
				Severity:    "error",
				Message:     fmt.Sprintf("'?' 错误类型不匹配: 表达式错误类型为 '%s'，函数声明错误类型为 '%s'", c.types.Format(errType), c.types.Format(fnErrType)),
				File:        e.Span.File,
				Span:        e.Span,
				Explanation: "Phase 2A 要求 '?' 传播的源错误类型与宿主函数返回错误类型严格相同（From/Into 自动转换将在后续 Trait Solver 阶段支持）。",
			})
			return types.Invalid
		}
		return okType
	}

	c.report.AddDiagnostic(diagnostic.Diagnostic{
		Code:     "WON-ERR-001",
		Severity: "error",
		Message:  fmt.Sprintf("'?' 操作符只能作用于 Option[T] 或 Result[T, E]，实际类型为 '%s'", c.types.Format(subType)),
		File:     e.Span.File,
		Span:     e.Span,
	})
	return types.Invalid
}

func (c *Checker) checkCallExpr(e *ast.CallExpr, expected types.TypeID, scope *symbols.Scope) types.TypeID {
	// 处理内置核心函数或枚举直接构造器 (Some / Ok / Err / println / bytes_equal / 用户函数)
	calleeName := e.Callee
	if calleeName == "" && e.CalleeExpr != nil {
		if id, ok := e.CalleeExpr.(*ast.Identifier); ok {
			calleeName = id.Name
		}
	}

	switch calleeName {
	case "println", "print":
		for _, arg := range e.Args {
			c.checkExpr(arg, types.Invalid, scope)
		}
		return c.types.Unit
	case "panic":
		for _, arg := range e.Args {
			c.checkExpr(arg, c.types.StrView, scope)
		}
		return c.types.Never
	case "assert":
		if len(e.Args) != 1 {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-001",
				Severity: "error",
				Message:  fmt.Sprintf("内置函数 assert 期望 1 个布尔参数，实际传入 %d 个", len(e.Args)),
				File:     e.Span.File,
				Span:     e.Span,
			})
		} else {
			argT := c.checkExpr(e.Args[0], c.types.Bool, scope)
			if argT != types.Invalid && argT != c.types.Bool {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-TYP-001",
					Severity: "error",
					Message:  fmt.Sprintf("assert 条件必须为 bool，实际为 '%s'", c.types.Format(argT)),
					File:     e.Args[0].GetSpan().File,
					Span:     e.Args[0].GetSpan(),
				})
			}
		}
		c.typed.markCodegenUnsupported("assert 尚未在 C11 后端实现", e.Span)
		return c.types.Unit

	case "bytes_equal":
		if len(e.Args) != 3 {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-001",
				Severity: "error",
				Message:  fmt.Sprintf("内置函数 bytes_equal 期望 3 个参数 (ptr1, ptr2, len)，实际传入 %d 个", len(e.Args)),
				File:     e.Span.File,
				Span:     e.Span,
			})
			for _, arg := range e.Args {
				c.checkExpr(arg, types.Invalid, scope)
			}
			return c.types.Bool
		}
		ptrT := c.types.RawPtr(false, c.types.U8)
		a0 := c.checkExpr(e.Args[0], ptrT, scope)
		a1 := c.checkExpr(e.Args[1], ptrT, scope)
		a2 := c.checkExpr(e.Args[2], c.types.U64, scope)
		if a0 != types.Invalid && !c.types.CanAssign(a0, ptrT) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code: "WON-TYP-001", Severity: "error",
				Message: fmt.Sprintf("bytes_equal 第 1 个参数类型不匹配: 期望 '*const u8'，实际为 '%s'", c.types.Format(a0)),
				File:    e.Args[0].GetSpan().File, Span: e.Args[0].GetSpan(),
			})
		}
		if a1 != types.Invalid && !c.types.CanAssign(a1, ptrT) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code: "WON-TYP-001", Severity: "error",
				Message: fmt.Sprintf("bytes_equal 第 2 个参数类型不匹配: 期望 '*const u8'，实际为 '%s'", c.types.Format(a1)),
				File:    e.Args[1].GetSpan().File, Span: e.Args[1].GetSpan(),
			})
		}
		if a2 != types.Invalid && a2 != c.types.U64 {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code: "WON-TYP-001", Severity: "error",
				Message: fmt.Sprintf("bytes_equal 第 3 个长度参数类型不匹配: 期望 'u64'，实际为 '%s'", c.types.Format(a2)),
				File:    e.Args[2].GetSpan().File, Span: e.Args[2].GetSpan(),
			})
		}
		return c.types.Bool

	case "Some":
		c.typed.markCodegenUnsupported("Some(...) 构造尚未在 C11 后端实现", e.Span)
		return c.checkOptionSomeCall(e.Args, expected, scope, e.Span)

	case "Ok":
		c.typed.markCodegenUnsupported("Ok(...) 构造尚未在 C11 后端实现", e.Span)
		return c.checkResultOkCall(e.Args, expected, scope, e.Span)

	case "Err":
		c.typed.markCodegenUnsupported("Err(...) 构造尚未在 C11 后端实现", e.Span)
		return c.checkResultErrCall(e.Args, expected, scope, e.Span)
	}

	if calleeName != "" {
		sym := scope.Lookup(calleeName)
		if sym == nil {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-NAM-001",
				Severity:    "error",
				Message:     fmt.Sprintf("调用未定义的未知函数: %q", calleeName),
				File:        e.Span.File,
				Span:        e.Span,
				Explanation: fmt.Sprintf("当前作用域内未找到名为 '%s' 的函数定义。", calleeName),
			})
			for _, arg := range e.Args {
				c.checkExpr(arg, types.Invalid, scope)
			}
			return types.Invalid
		}
		if sym.Kind != symbols.SymFunc && sym.Kind != symbols.SymMethod && sym.Kind != symbols.SymBuiltin {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-NAM-005",
				Severity: "error",
				Message:  fmt.Sprintf("标识符 '%s' 不是可调用函数", calleeName),
				File:     e.Span.File,
				Span:     e.Span,
			})
			return types.Invalid
		}
		c.typed.CallTargets[e] = sym
		if len(sym.GenericParamIDs) > 0 && len(e.GenericArgs) == 0 {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-SEM-010",
				Severity:    "error",
				Message:     fmt.Sprintf("泛型函数 '%s' 的自动类型实参推断尚未在 Phase 2A 实现", sym.Name),
				File:        e.Span.File,
				Span:        e.Span,
				Explanation: "Phase 2A 支持已知泛型结构体/枚举实例与显式类型解析；泛型函数实参自动推导将在后续里程碑完成。",
			})
			return sym.ReturnType
		}
		if len(e.GenericArgs) > 0 && len(e.GenericArgs) != len(sym.GenericParamIDs) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-ADT-007",
				Severity: "error",
				Message:  fmt.Sprintf("函数 '%s' 期望 %d 个泛型实参，实际传入 %d 个", sym.Name, len(sym.GenericParamIDs), len(e.GenericArgs)),
				File:     e.Span.File,
				Span:     e.Span,
			})
		}

		c.checkCallArgsAgainstSignature(sym.Name, sym.ParamTypes, e.Args, scope, e.Span)
		if calleeName != "main" {
			// 允许用户函数调用若使用 C11 支持则留给 C11 后端决定，此处主要验证实参
		}
		return sym.ReturnType
	}

	c.report.AddDiagnostic(diagnostic.Diagnostic{
		Code:     "WON-NAM-005",
		Severity: "error",
		Message:  "不支持或未解析的函数调用目标",
		File:     e.Span.File,
		Span:     e.Span,
	})
	return types.Invalid
}

func (c *Checker) checkCallArgsAgainstSignature(fnName string, paramTypes []types.TypeID, args []ast.Expr, scope *symbols.Scope, callSpan diagnostic.Span) {
	if len(args) != len(paramTypes) {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-TYP-001",
			Severity: "error",
			Message:  fmt.Sprintf("函数 '%s' 参数数量不匹配: 期望 %d 个参数，实际传入 %d 个", fnName, len(paramTypes), len(args)),
			File:     callSpan.File,
			Span:     callSpan,
		})
	}
	for i, arg := range args {
		exp := types.Invalid
		if i < len(paramTypes) {
			exp = paramTypes[i]
		}
		argType := c.checkExpr(arg, exp, scope)
		if exp != types.Invalid && argType != types.Invalid && !c.types.CanAssign(argType, exp) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-001",
				Severity: "error",
				Message:  fmt.Sprintf("函数 '%s' 第 %d 个参数类型不匹配: 期望 '%s'，实际传入 '%s'", fnName, i+1, c.types.Format(exp), c.types.Format(argType)),
				File:     arg.GetSpan().File,
				Span:     arg.GetSpan(),
			})
		}
	}
}

func (c *Checker) checkOptionSomeCall(args []ast.Expr, expected types.TypeID, scope *symbols.Scope, span diagnostic.Span) types.TypeID {
	if len(args) != 1 {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-ERR-003",
			Severity: "error",
			Message:  fmt.Sprintf("Option::Some(...) 期望 1 个参数，实际传入 %d 个", len(args)),
			File:     span.File,
			Span:     span,
		})
		return types.Invalid
	}
	expElem := types.Invalid
	if elem, ok := c.types.IsOption(expected); ok {
		expElem = elem
	} else if scope.OwnerFn != nil {
		if elem, ok := c.types.IsOption(scope.OwnerFn.ReturnType); ok {
			expElem = elem
		}
	}
	argType := c.checkExpr(args[0], expElem, scope)
	if expElem != types.Invalid && argType != types.Invalid && !c.types.CanAssign(argType, expElem) {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-ERR-003",
			Severity: "error",
			Message:  fmt.Sprintf("Option::Some 参数类型不匹配: 期望 '%s'，实际为 '%s'", c.types.Format(expElem), c.types.Format(argType)),
			File:     args[0].GetSpan().File,
			Span:     args[0].GetSpan(),
		})
	}
	if expElem != types.Invalid {
		return c.types.Option(expElem)
	}
	return c.types.Option(argType)
}

func (c *Checker) checkResultOkCall(args []ast.Expr, expected types.TypeID, scope *symbols.Scope, span diagnostic.Span) types.TypeID {
	if len(args) != 1 {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-ERR-003",
			Severity: "error",
			Message:  fmt.Sprintf("Result::Ok(...) 期望 1 个参数，实际传入 %d 个", len(args)),
			File:     span.File,
			Span:     span,
		})
		return types.Invalid
	}
	expOk, expErr := types.Invalid, types.Invalid
	if okT, errT, ok := c.types.IsResult(expected); ok {
		expOk, expErr = okT, errT
	} else if scope.OwnerFn != nil {
		if okT, errT, ok := c.types.IsResult(scope.OwnerFn.ReturnType); ok {
			expOk, expErr = okT, errT
		}
	}
	valType := c.checkExpr(args[0], expOk, scope)
	if expOk != types.Invalid && valType != types.Invalid && !c.types.CanAssign(valType, expOk) {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-ERR-003",
			Severity: "error",
			Message:  fmt.Sprintf("Result::Ok 参数类型不匹配: 期望 '%s'，实际为 '%s'", c.types.Format(expOk), c.types.Format(valType)),
			File:     args[0].GetSpan().File,
			Span:     args[0].GetSpan(),
		})
	}
	if expErr == types.Invalid {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-ERR-003",
			Severity: "error",
			Message:  "无法单独从 Ok(...) 推断 Result[T, E] 的错误类型 E，请提供显式返回类型或变量类型标注",
			File:     span.File,
			Span:     span,
		})
		return types.Invalid
	}
	if expOk != types.Invalid {
		return c.types.Result(expOk, expErr)
	}
	return c.types.Result(valType, expErr)
}

func (c *Checker) checkResultErrCall(args []ast.Expr, expected types.TypeID, scope *symbols.Scope, span diagnostic.Span) types.TypeID {
	if len(args) != 1 {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-ERR-003",
			Severity: "error",
			Message:  fmt.Sprintf("Result::Err(...) 期望 1 个参数，实际传入 %d 个", len(args)),
			File:     span.File,
			Span:     span,
		})
		return types.Invalid
	}
	expOk, expErr := types.Invalid, types.Invalid
	if okT, errT, ok := c.types.IsResult(expected); ok {
		expOk, expErr = okT, errT
	} else if scope.OwnerFn != nil {
		if okT, errT, ok := c.types.IsResult(scope.OwnerFn.ReturnType); ok {
			expOk, expErr = okT, errT
		}
	}
	errType := c.checkExpr(args[0], expErr, scope)
	if expErr != types.Invalid && errType != types.Invalid && !c.types.CanAssign(errType, expErr) {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-ERR-003",
			Severity: "error",
			Message:  fmt.Sprintf("Result::Err 参数类型不匹配: 期望 '%s'，实际为 '%s'", c.types.Format(expErr), c.types.Format(errType)),
			File:     args[0].GetSpan().File,
			Span:     args[0].GetSpan(),
		})
	}
	if expOk == types.Invalid {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-ERR-003",
			Severity: "error",
			Message:  "无法单独从 Err(...) 推断 Result[T, E] 的成功类型 T，请提供显式返回类型或变量类型标注",
			File:     span.File,
			Span:     span,
		})
		return types.Invalid
	}
	if expErr != types.Invalid {
		return c.types.Result(expOk, expErr)
	}
	return c.types.Result(expOk, errType)
}

func (c *Checker) checkNamespacedCallExpr(e *ast.NamespacedCallExpr, expected types.TypeID, scope *symbols.Scope) types.TypeID {
	// 1. 优先检查是否为内置或用户类型关联方法 / 枚举变体构造 (Option::Some, Result::Ok, String::from, User::new, Enum::Variant)
	if e.Namespace == "Option" {
		if e.Function == "Some" {
			c.typed.markCodegenUnsupported("Option::Some 尚未在 C11 后端实现", e.Span)
			return c.checkOptionSomeCall(e.Args, expected, scope, e.Span)
		}
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code: "WON-ADT-005", Severity: "error",
			Message: fmt.Sprintf("Option 上不存在构造器 '%s'", e.Function),
			File:    e.Span.File, Span: e.Span,
		})
		return types.Invalid
	}
	if e.Namespace == "Result" {
		c.typed.markCodegenUnsupported("Result 构造尚未在 C11 后端实现", e.Span)
		if e.Function == "Ok" {
			return c.checkResultOkCall(e.Args, expected, scope, e.Span)
		}
		if e.Function == "Err" {
			return c.checkResultErrCall(e.Args, expected, scope, e.Span)
		}
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code: "WON-ADT-005", Severity: "error",
			Message: fmt.Sprintf("Result 上不存在构造器 '%s'", e.Function),
			File:    e.Span.File, Span: e.Span,
		})
		return types.Invalid
	}

	if sym := scope.Lookup(e.Namespace); sym != nil && (sym.Kind == symbols.SymStruct || sym.Kind == symbols.SymEnum) {
		// 枚举带参数变体构造 Status::Failed("...")
		if sym.Kind == symbols.SymEnum {
			if vi, ok := sym.VariantMap[e.Function]; ok {
				c.typed.VariantRefs[e] = vi
				c.typed.markCodegenUnsupported("枚举构造器尚未在 C11 后端实现", e.Span)
				if vi.Kind != symbols.VariantTuple {
					c.report.AddDiagnostic(diagnostic.Diagnostic{
						Code: "WON-ADT-006", Severity: "error",
						Message: fmt.Sprintf("枚举变体 '%s::%s' 不是元组变体，不能用 '()' 构造", sym.Name, e.Function),
						File:    e.Span.File, Span: e.Span,
					})
					return types.Invalid
				}
				c.checkCallArgsAgainstSignature(sym.Name+"::"+vi.Name, vi.TupleTypes, e.Args, scope, e.Span)
				return sym.Type
			}
		}
		// 静态关联函数 (String::from, Point::new)
		if mSym, ok := sym.Methods[e.Function]; ok {
			c.typed.CallTargets[e] = mSym
			c.typed.markCodegenUnsupported("关联静态方法尚未在 C11 后端实现", e.Span)
			c.checkCallArgsAgainstSignature(sym.Name+"::"+mSym.Name, mSym.ParamTypes, e.Args, scope, e.Span)
			return mSym.ReturnType
		}
		if sym.Kind == symbols.SymEnum {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code: "WON-ADT-005", Severity: "error",
				Message: fmt.Sprintf("枚举 '%s' 上未定义变体或方法 '%s'", sym.Name, e.Function),
				File:    e.Span.File, Span: e.Span,
			})
		} else {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code: "WON-NAM-006", Severity: "error",
				Message: fmt.Sprintf("结构体 '%s' 上不存在关联函数 '%s'", sym.Name, e.Function),
				File:    e.Span.File, Span: e.Span,
			})
		}
		return types.Invalid
	}

	// 2. 否则执行原有的真实 C 头文件 FFI 命名空间检查 (zlib::compress 等)
	return c.checkForeignFFINamespacedCall(e, scope)
}

func (c *Checker) checkForeignFFINamespacedCall(e *ast.NamespacedCallExpr, scope *symbols.Scope) types.TypeID {
	meta, exists := c.bindings[e.Namespace]
	if !exists {
		for _, arg := range e.Args {
			c.checkExpr(arg, types.Invalid, scope)
		}
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-FFI-001",
			Severity:    "error",
			Message:     fmt.Sprintf("未导入的外部 C 命名空间: %q (请在源文件头部使用 #import 引入)", e.Namespace),
			File:        e.Span.File,
			Span:        e.Span,
			Explanation: fmt.Sprintf("无法解析命名空间 '%s'。WON 要求通过 #import 指令显式引入 C 头文件并建立绑定映射。", e.Namespace),
		})
		return types.Invalid
	}

	fnSig, found := meta.Functions[e.Function]
	if !found {
		for _, arg := range e.Args {
			c.checkExpr(arg, types.Invalid, scope)
		}
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-FFI-002",
			Severity:    "error",
			Message:     fmt.Sprintf("在 C 头文件 '%s' 中未找到导出的函数符号: %q", meta.HeaderPath, e.Function),
			File:        e.Span.File,
			Span:        e.Span,
			Explanation: fmt.Sprintf("从头文件 '%s' 提取的真实声明符号列表中不存在函数 '%s'。", meta.HeaderPath, e.Function),
		})
		return types.Invalid
	}

	if len(e.Args) != len(fnSig.ParamTypes) {
		for _, arg := range e.Args {
			c.checkExpr(arg, types.Invalid, scope)
		}
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-FFI-003",
			Severity:    "error",
			Message:     fmt.Sprintf("外部 C 函数 '%s::%s' 参数数量不匹配: 期望 %d 个参数，实际传入 %d 个", e.Namespace, e.Function, len(fnSig.ParamTypes), len(e.Args)),
			File:        e.Span.File,
			Span:        e.Span,
			Explanation: fmt.Sprintf("根据头文件解析的真实函数签名: %s(%s)，期望参数个数为 %d。", e.Function, strings.Join(fnSig.ParamTypes, ", "), len(fnSig.ParamTypes)),
		})
		return types.Invalid
	}

	if e.Namespace == "zlib" && !isSupportedZlibFunc(e.Function) {
		for _, arg := range e.Args {
			c.checkExpr(arg, types.Invalid, scope)
		}
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-FFI-005",
			Severity:    "error",
			Message:     fmt.Sprintf("C 函数 '%s::%s' 的签名或类型在当前 FFI 原型中暂不支持", e.Namespace, e.Function),
			File:        e.Span.File,
			Span:        e.Span,
			Explanation: "当前 FFI 运行原型显式验证核心标量与原生指针 C 接口 (compressBound, compress, uncompress, zlibVersion)。",
		})
		return types.Invalid
	}

	for i, arg := range e.Args {
		if i >= len(fnSig.ParamTypes) {
			break
		}
		expectedCType := fnSig.ParamTypes[i]
		expectedWonType := c.mapExpectedCTypeToTypeID(expectedCType)
		argType := c.checkExpr(arg, expectedWonType, scope)
		if !c.isCanonicalTypeCompatibleWithC(argType, expectedCType) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-FFI-004",
				Severity:    "error",
				Message:     fmt.Sprintf("外部 C 函数 '%s::%s' 第 %d 个参数类型不匹配: 期望 %s，实际传入 %s", e.Namespace, e.Function, i+1, expectedCType, c.types.Format(argType)),
				File:        arg.GetSpan().File,
				Span:        arg.GetSpan(),
				Explanation: fmt.Sprintf("Linux x86_64 C ABI 要求实参类型与 C 原型严格对齐。不能将 WON 类型 '%s' 传递给期望 '%s' 的 C 参数。", c.types.Format(argType), expectedCType),
			})
		}
	}

	if !c.inUnsafe {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-SEC-001",
			Severity:    "error",
			Message:     fmt.Sprintf("调用外部 C 库函数 '%s::%s' 必须在显式 unsafe 代码块内", e.Namespace, e.Function),
			File:        e.Span.File,
			Span:        e.Span,
			Explanation: "所有未经编译器证明内存安全的底层 C FFI 函数必须被包裹在 'unsafe:' 块中，确保审计边界清晰。",
		})
	}

	return c.mapCReturnTypeToTypeID(fnSig.ReturnType)
}

func isSupportedZlibFunc(name string) bool {
	switch name {
	case "compress", "uncompress", "compressBound", "zlibVersion":
		return true
	default:
		return false
	}
}

func (c *Checker) mapExpectedCTypeToTypeID(cType string) types.TypeID {
	clean := strings.TrimSpace(cType)
	switch clean {
	case "unsigned char *", "Bytef *", "Byte *":
		return c.types.RawPtr(true, c.types.U8)
	case "const unsigned char *", "const Bytef *", "const Byte *":
		return c.types.RawPtr(false, c.types.U8)
	case "unsigned long *", "uLongf *", "uLong *":
		return c.types.RawPtr(true, c.types.U64)
	case "const unsigned long *":
		return c.types.RawPtr(false, c.types.U64)
	case "unsigned long", "uLong", "size_t":
		return c.types.U64
	case "uInt", "unsigned int":
		return c.types.U32
	case "int":
		return c.types.I32
	case "int64_t":
		return c.types.I64
	case "const char *":
		return c.types.StrView
	default:
		return types.Invalid
	}
}

func (c *Checker) mapCReturnTypeToTypeID(ret string) types.TypeID {
	switch strings.TrimSpace(ret) {
	case "int":
		return c.types.I32
	case "unsigned long", "uLong":
		return c.types.U64
	case "const char *", "const char*":
		return c.types.RawPtr(false, c.types.U8)
	case "void":
		return c.types.Unit
	default:
		return types.Invalid
	}
}

func (c *Checker) isCanonicalTypeCompatibleWithC(wonType types.TypeID, expectedCType string) bool {
	if wonType == types.Invalid {
		return true
	}
	info := c.types.Info(wonType)
	cleanC := strings.TrimSpace(expectedCType)
	switch cleanC {
	case "unsigned char *", "Bytef *", "Byte *":
		// 明确要求可变字节指针 (*mut u8, &mut u8 或可变数组切片引用)
		if (info.Kind != types.KindRawPtr && info.Kind != types.KindRef) || !info.IsMut {
			return false
		}
		ei := c.types.Info(info.Elem)
		return ei.Kind == types.KindU8 || ((ei.Kind == types.KindArray || ei.Kind == types.KindSlice) && ei.Elem == c.types.U8)
	case "const unsigned char *", "const Bytef *", "const Byte *":
		// 可接受只读指针/引用或可变指针/引用 (协变)
		if info.Kind != types.KindRawPtr && info.Kind != types.KindRef {
			return false
		}
		ei := c.types.Info(info.Elem)
		return ei.Kind == types.KindU8 || ((ei.Kind == types.KindArray || ei.Kind == types.KindSlice) && ei.Elem == c.types.U8)
	case "unsigned long *", "uLongf *", "uLong *":
		// 明确要求指向 u64 的可变指针或可变引用 (*mut u64 / &mut u64)
		return (info.Kind == types.KindRawPtr || info.Kind == types.KindRef) && info.IsMut && info.Elem == c.types.U64
	case "const unsigned long *":
		return (info.Kind == types.KindRawPtr || info.Kind == types.KindRef) && info.Elem == c.types.U64
	case "unsigned long", "uLong", "uInt", "int", "int64_t", "size_t":
		return wonType == c.types.U64 || wonType == c.types.I32 || wonType == c.types.I64 || wonType == c.types.U32
	case "const char *":
		return wonType == c.types.StrView || (info.Kind == types.KindRawPtr && !info.IsMut && info.Elem == c.types.U8)
	default:
		return false
	}
}

func (c *Checker) checkMethodCallExpr(e *ast.MethodCallExpr, scope *symbols.Scope) types.TypeID {
	recType := c.checkExpr(e.Receiver, types.Invalid, scope)
	for _, arg := range e.Args {
		_ = c.checkExpr(arg, types.Invalid, scope)
	}

	// 1. 内置基础类型方法支持 (&str, String, [T; N], &[T], *const u8)
	switch e.Method {
	case "as_mut_ptr":
		if ident, ok := e.Receiver.(*ast.Identifier); ok {
			sym := scope.Lookup(ident.Name)
			if sym != nil && !sym.IsMut {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:        "WON-MUT-001",
					Severity:    "error",
					Message:     fmt.Sprintf("无法对不可变变量 '%s' 调用可变指针方法 (as_mut_ptr)", ident.Name),
					File:        e.Span.File,
					Span:        e.Span,
					Explanation: fmt.Sprintf("变量 '%s' 使用 'let' 声明为不可变。获取可变原生指针 (*mut) 要求使用 'var' 声明。", ident.Name),
					Fixes: []diagnostic.Fix{
						{
							Description: fmt.Sprintf("将变量 '%s' 声明修改为可变 ('var')", ident.Name),
							Edits: []diagnostic.Edit{
								{
									Start:       sym.LetTokenSpan.Start.Byte,
									End:         sym.LetTokenSpan.End.Byte,
									Replacement: "var",
								},
							},
						},
					},
				})
			}
		}
		return c.types.RawPtr(true, c.types.U8)

	case "as_ptr":
		return c.types.RawPtr(false, c.types.U8)

	case "as_bytes":
		return c.types.Ref(false, c.types.Slice(c.types.U8))

	case "len":
		return c.types.U64

	case "is_empty":
		return c.types.Bool

	case "push_str":
		if ident, ok := e.Receiver.(*ast.Identifier); ok {
			sym := scope.Lookup(ident.Name)
			if sym != nil && !sym.IsMut && !(c.types.Info(sym.Type).Kind == types.KindRef && c.types.Info(sym.Type).IsMut) {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-MUT-001",
					Severity: "error",
					Message:  fmt.Sprintf("不能在不可变绑定 '%s' 上调用可变方法 'push_str'", ident.Name),
					File:     e.Span.File,
					Span:     e.Span,
				})
			}
		}
		c.typed.markCodegenUnsupported("String::push_str 尚未在 C11 后端实现", e.Span)
		return c.types.Unit
	}

	// 2. 解引用接收者查找用户自定义 struct / enum 上的固有 impl 方法
	if recType == types.Invalid {
		return types.Invalid
	}
	baseID := recType
	recInfo := c.types.Info(baseID)
	if recInfo.Kind == types.KindRef {
		baseID = recInfo.Elem
		recInfo = c.types.Info(baseID)
	}
	if recInfo.Kind != types.KindStruct && recInfo.Kind != types.KindEnum {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-NAM-006",
			Severity: "error",
			Message:  fmt.Sprintf("类型 '%s' 上不存在名为 '%s' 的固有方法", c.types.Format(recType), e.Method),
			File:     e.Span.File,
			Span:     e.Span,
		})
		return types.Invalid
	}

	ownerSym := c.symbols.ByID[symbols.SymbolID(recInfo.SymbolID)]
	if ownerSym == nil {
		return types.Invalid
	}
	methodSym, ok := ownerSym.Methods[e.Method]
	if !ok {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-NAM-006",
			Severity: "error",
			Message:  fmt.Sprintf("类型 '%s' 没有名为 '%s' 的固有方法", ownerSym.Name, e.Method),
			File:     e.Span.File,
			Span:     e.Span,
		})
		return types.Invalid
	}
	if !methodSym.HasSelf {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-NAM-006",
			Severity: "error",
			Message:  fmt.Sprintf("'%s::%s' 为静态关联函数（无 self 接收者），不能作为实例方法调用", ownerSym.Name, e.Method),
			File:     e.Span.File,
			Span:     e.Span,
		})
		return types.Invalid
	}
	if methodSym.SelfIsMut && !c.isReceiverMutable(e.Receiver, recType, scope) {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-MUT-001",
			Severity: "error",
			Message:  fmt.Sprintf("调用可变接收者 (&mut self) 方法 '%s' 需要可变接收者", e.Method),
			File:     e.Span.File,
			Span:     e.Span,
		})
	}

	c.typed.CallTargets[e] = methodSym
	c.typed.markCodegenUnsupported("固有实例方法调用尚未在 C11 后端实现", e.Span)
	c.checkCallArgsAgainstSignature(ownerSym.Name+"."+methodSym.Name, methodSym.ParamTypes, e.Args, scope, e.Span)
	return methodSym.ReturnType
}

func (c *Checker) isReceiverMutable(recv ast.Expr, recType types.TypeID, scope *symbols.Scope) bool {
	info := c.types.Info(recType)
	if info.Kind == types.KindRef {
		return info.IsMut
	}
	return c.isLValueRootMutable(recv, scope)
}

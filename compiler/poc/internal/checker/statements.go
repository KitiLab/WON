package checker

import (
	"fmt"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

func (c *Checker) checkBlock(b *ast.BlockStmt, expectedRet types.TypeID, parent *symbols.Scope) types.TypeID {
	if b == nil {
		return c.types.Unit
	}
	blockScope := symbols.NewScope(symbols.ScopeBlock, parent)
	lastType := c.types.Unit
	diverged := false

	for i, stmt := range b.Statements {
		isLast := i == len(b.Statements)-1
		stmtExpected := types.Invalid
		if isLast {
			if _, ok := stmt.(*ast.ExprStmt); ok {
				stmtExpected = expectedRet
			} else if _, ok := stmt.(*ast.IfStmt); ok {
				stmtExpected = expectedRet
			} else if _, ok := stmt.(*ast.MatchExpr); ok {
				stmtExpected = expectedRet
			}
		}
		st := c.checkStatement(stmt, stmtExpected, blockScope)
		if c.types.Info(st).Kind == types.KindNever {
			diverged = true
			lastType = c.types.Never
		} else if !diverged {
			lastType = st
		}
	}
	return lastType
}

func (c *Checker) checkStatement(stmt ast.Stmt, expected types.TypeID, scope *symbols.Scope) types.TypeID {
	if stmt == nil {
		return c.types.Unit
	}

	switch s := stmt.(type) {
	case *ast.LetStmt:
		c.checkLetStmt(s, scope)
		return c.types.Unit

	case *ast.AssignStmt:
		c.checkAssignStmt(s, scope)
		return c.types.Unit

	case *ast.IfStmt:
		return c.checkIfStmt(s, expected, scope)

	case *ast.WhileStmt:
		condType := c.checkExpr(s.Cond, c.types.Bool, scope)
		if condType != types.Invalid && condType != c.types.Bool {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-TYP-002",
				Severity:    "error",
				Message:     fmt.Sprintf("while 循环条件必须为布尔类型 (bool)，实际表达式类型为 '%s'", c.types.Format(condType)),
				File:        s.Cond.GetSpan().File,
				Span:        s.Cond.GetSpan(),
				Explanation: "WON 禁止将整数或其他非布尔类型隐式当作布尔条件求值。",
			})
		}
		c.loopDepth++
		c.checkBlock(s.Body, types.Invalid, scope)
		c.loopDepth--
		return c.types.Unit

	case *ast.ForStmt:
		c.checkForStmt(s, scope)
		c.typed.markCodegenUnsupported("for 迭代循环尚未在 C11 后端实现代码生成", s.Span)
		return c.types.Unit

	case *ast.ReturnStmt:
		c.checkReturnStmt(s, scope)
		return c.types.Never

	case *ast.BreakStmt:
		if c.loopDepth <= 0 {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-NAM-005",
				Severity: "error",
				Message:  "'break' 语句只能出现在 while 或 for 循环体内部",
				File:     s.Span.File,
				Span:     s.Span,
			})
		}
		if s.Value != nil {
			c.checkExpr(s.Value, types.Invalid, scope)
		}
		c.typed.markCodegenUnsupported("break 语句尚未在 C11 后端实现代码生成", s.Span)
		return c.types.Never

	case *ast.ContinueStmt:
		if c.loopDepth <= 0 {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-NAM-005",
				Severity: "error",
				Message:  "'continue' 语句只能出现在 while 或 for 循环体内部",
				File:     s.Span.File,
				Span:     s.Span,
			})
		}
		c.typed.markCodegenUnsupported("continue 语句尚未在 C11 后端实现代码生成", s.Span)
		return c.types.Never

	case *ast.MatchExpr:
		return c.checkMatchExpr(s, expected, scope)

	case *ast.ScopeExpr:
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-SEM-010",
			Severity:    "error",
			Message:     "结构化并发作用域 'scope' 已通过语法解析，但并发安全与调度器运行时尚未在 Phase 2A 实现",
			File:        s.Span.File,
			Span:        s.Span,
			Explanation: "并发语义检查 (Send/Sync) 与运行时调度器属于后续里程碑。",
		})
		return types.Invalid

	case *ast.SpawnExpr:
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-SEM-010",
			Severity:    "error",
			Message:     "并发任务派生 'spawn' 已通过语法解析，但并发安全与调度器运行时尚未在 Phase 2A 实现",
			File:        s.Span.File,
			Span:        s.Span,
			Explanation: "并发语义检查 (Send/Sync) 与运行时调度器属于后续里程碑。",
		})
		return types.Invalid

	case *ast.ExprStmt:
		return c.checkExpr(s.Expr, expected, scope)

	default:
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-SEM-010",
			Severity: "error",
			Message:  fmt.Sprintf("语句类型 %T 尚未在 Phase 2A 实现语义检查", stmt),
			File:     stmt.GetSpan().File,
			Span:     stmt.GetSpan(),
		})
		return types.Invalid
	}
}

func (c *Checker) checkLetStmt(s *ast.LetStmt, scope *symbols.Scope) {
	declType := types.Invalid
	if s.TypeAST != nil {
		declType = c.resolveAstType(s.TypeAST, scope)
		c.validateSizedType(declType, s.TypeAST.GetSpan(), "局部变量")
	} else if s.Type != "" {
		declType = c.symbols.ResolveTypeString(s.Type, scope)
		c.validateSizedType(declType, s.Span, "局部变量")
	}

	initType := c.checkExpr(s.Init, declType, scope)

	// 校验不能将返回值为 void 的外部 C 函数调用结果赋值给变量 (WON-FFI-006)
	if initType == c.types.Unit && c.isVoidFFIExpr(s.Init) {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-FFI-006",
			Severity:    "error",
			Message:     fmt.Sprintf("不能将返回值为 void 的外部调用结果赋值给变量 '%s'", s.Name),
			File:        s.Span.File,
			Span:        s.Span,
			Explanation: "外部 C 函数原型声明返回值为 void，不能作为变量绑定的右值表达式。",
		})
	}

	varType := declType
	if varType == types.Invalid {
		varType = initType
		c.validateSizedType(varType, s.Span, "局部变量")
	} else if initType != types.Invalid && !c.types.CanAssign(initType, varType) {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-TYP-001",
			Severity:    "error",
			Message:     fmt.Sprintf("类型不匹配: 无法将 '%s' 类型的值初始化给类型为 '%s' 的变量", c.types.Format(initType), c.types.Format(varType)),
			File:        s.Init.GetSpan().File,
			Span:        s.Init.GetSpan(),
			Explanation: "WON 禁止隐式宽化、窄化或跨类型转换；若确需转换请使用显式 'as' 操作符。",
		})
	}

	// 将模式内声明的局部绑定插入当前词法作用域
	if s.Pattern != nil {
		c.bindLetPattern(s.Pattern, varType, s.IsMut, s.LetTokenSpan, scope)
	} else if s.Name != "" && s.Name != "_" {
		sym := c.symbols.AllocSymbol(symbols.SymVar, s.Name, s.Span)
		sym.IsMut = s.IsMut
		sym.Type = varType
		sym.LetTokenSpan = s.LetTokenSpan
		sym.Defined = true
		if existing, ok := scope.Insert(sym); !ok {
			c.emitDuplicateDecl(s.Name, s.Span, existing.Span)
		}
	}
}

func (c *Checker) bindLetPattern(pat ast.Pattern, targetType types.TypeID, isMut bool, letTokSpan diagnostic.Span, scope *symbols.Scope) {
	switch p := pat.(type) {
	case *ast.WildcardPattern:
		// 忽略绑定

	case *ast.IdentPattern:
		sym := c.symbols.AllocSymbol(symbols.SymVar, p.Name, p.Span)
		sym.IsMut = isMut || p.IsMut
		sym.Type = targetType
		sym.LetTokenSpan = letTokSpan
		sym.Defined = true
		if existing, ok := scope.Insert(sym); !ok {
			c.emitDuplicateDecl(p.Name, p.Span, existing.Span)
		}
		c.symbols.NodeSymbols[p] = sym

	case *ast.TuplePattern:
		if targetType == types.Invalid {
			for _, elemPat := range p.Elems {
				c.bindLetPattern(elemPat, types.Invalid, isMut, letTokSpan, scope)
			}
			return
		}
		tInfo := c.types.Info(targetType)
		if tInfo.Kind != types.KindTuple || len(tInfo.Elems) != len(p.Elems) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-MAT-002",
				Severity: "error",
				Message:  fmt.Sprintf("元组解构模式元素个数 (%d) 与右侧类型 '%s' 不匹配", len(p.Elems), c.types.Format(targetType)),
				File:     p.Span.File,
				Span:     p.Span,
			})
			return
		}
		for i, elemPat := range p.Elems {
			c.bindLetPattern(elemPat, tInfo.Elems[i], isMut, letTokSpan, scope)
		}

	default:
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-MAT-002",
			Severity: "error",
			Message:  fmt.Sprintf("let/var 声明绑定仅支持标识符、通配符或不可反驳元组模式，暂不支持 %T", pat),
			File:     pat.GetSpan().File,
			Span:     pat.GetSpan(),
		})
	}
}

func (c *Checker) isVoidFFIExpr(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	if u, ok := expr.(*ast.UnsafeExpr); ok && u.Block != nil && len(u.Block.Statements) > 0 {
		if es, ok := u.Block.Statements[len(u.Block.Statements)-1].(*ast.ExprStmt); ok {
			expr = es.Expr
		}
	}
	if nc, ok := expr.(*ast.NamespacedCallExpr); ok {
		if meta, ok := c.bindings[nc.Namespace]; ok {
			if sig, ok := meta.Functions[nc.Function]; ok {
				return sig.ReturnType == "void"
			}
		}
	}
	return false
}

func (c *Checker) checkAssignStmt(s *ast.AssignStmt, scope *symbols.Scope) {
	var targetType types.TypeID

	targetExpr := s.TargetExpr
	if targetExpr == nil && s.Target != "" {
		targetExpr = &ast.Identifier{Name: s.Target, Span: s.Span}
	}

	switch lhs := targetExpr.(type) {
	case *ast.Identifier:
		sym := scope.Lookup(lhs.Name)
		if sym == nil {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-NAM-001",
				Severity: "error",
				Message:  fmt.Sprintf("未声明的标识符: %q", lhs.Name),
				File:     lhs.Span.File,
				Span:     lhs.Span,
			})
			c.checkExpr(s.Value, types.Invalid, scope)
			return
		}
		targetType = sym.Type
		if !sym.IsMut {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-MUT-001",
				Severity:    "error",
				Message:     fmt.Sprintf("不能给不可变变量 '%s' 赋值", lhs.Name),
				File:        s.Span.File,
				Span:        s.Span,
				Explanation: fmt.Sprintf("变量 '%s' 通过 'let' 声明为只读。WON 默认所有绑定不可变，若需修改请使用 'var' 声明。", lhs.Name),
				Fixes: []diagnostic.Fix{
					{
						Description: fmt.Sprintf("将变量 '%s' 声明修改为可变 ('var')", lhs.Name),
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

	case *ast.FieldAccessExpr:
		objType := c.checkExpr(lhs.Object, types.Invalid, scope)
		fieldInfo := c.resolveField(objType, lhs.Field, lhs.Span)
		if fieldInfo != nil {
			targetType = fieldInfo.Type
			if !fieldInfo.IsVar {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:        "WON-ADT-004",
					Severity:    "error",
					Message:     fmt.Sprintf("不能修改不可变结构体字段 '%s' (请使用 'var %s: ...' 声明字段可变)", lhs.Field, lhs.Field),
					File:        lhs.Span.File,
					Span:        lhs.Span,
					Explanation: "即使结构体实例本身绑定为可变，未标注 'var' 的结构体字段在语言规范中默认禁止就地修改。",
				})
			}
			if !c.isLValueRootMutable(lhs.Object, scope) {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:        "WON-ADT-004",
					Severity:    "error",
					Message:     fmt.Sprintf("不能通过不可变绑定修改字段 '%s'", lhs.Field),
					File:        lhs.Span.File,
					Span:        lhs.Span,
					Explanation: "修改结构体字段需要结构体所在绑定使用 'var' 或通过 '&mut' 引用访问。",
				})
			}
		}

	case *ast.IndexExpr:
		objType := c.checkExpr(lhs.Object, types.Invalid, scope)
		idxType := c.checkExpr(lhs.Index, c.types.USize, scope)
		if idxType != types.Invalid && !c.types.IsInteger(idxType) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-002",
				Severity: "error",
				Message:  fmt.Sprintf("数组下标必须为整数类型，实际为 '%s'", c.types.Format(idxType)),
				File:     lhs.Index.GetSpan().File,
				Span:     lhs.Index.GetSpan(),
			})
		}
		targetType = c.unwrapIndexElement(objType, lhs.Span)
		if !c.isLValueRootMutable(lhs.Object, scope) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-MUT-001",
				Severity: "error",
				Message:  "不能修改不可变数组或切片元素",
				File:     lhs.Span.File,
				Span:     lhs.Span,
			})
		}

	case *ast.UnaryExpr:
		if lhs.Op == "*" {
			ptrType := c.checkExpr(lhs.Expr, types.Invalid, scope)
			pInfo := c.types.Info(ptrType)
			if pInfo.Kind == types.KindRef || pInfo.Kind == types.KindRawPtr {
				targetType = pInfo.Elem
				if !pInfo.IsMut {
					c.report.AddDiagnostic(diagnostic.Diagnostic{
						Code:     "WON-MUT-001",
						Severity: "error",
						Message:  fmt.Sprintf("不能通过只读指针/引用 '%s' 写入数据", c.types.Format(ptrType)),
						File:     lhs.Span.File,
						Span:     lhs.Span,
					})
				}
			}
		}
	}

	valType := c.checkExpr(s.Value, targetType, scope)
	if s.Op != "" && s.Op != "=" {
		// 复合赋值 +=, -= 等要求数值操作数匹配
		if targetType != types.Invalid && !c.types.IsNumeric(targetType) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-002",
				Severity: "error",
				Message:  fmt.Sprintf("复合赋值运算符 '%s' 只能作用于数值类型，不能用于 '%s'", s.Op, c.types.Format(targetType)),
				File:     s.Span.File,
				Span:     s.Span,
			})
		}
	}
	if targetType != types.Invalid && valType != types.Invalid && !c.types.CanAssign(valType, targetType) {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-TYP-001",
			Severity: "error",
			Message:  fmt.Sprintf("赋值类型不匹配: 无法将 '%s' 赋值给 '%s'", c.types.Format(valType), c.types.Format(targetType)),
			File:     s.Value.GetSpan().File,
			Span:     s.Value.GetSpan(),
		})
	}
}

func (c *Checker) isLValueRootMutable(expr ast.Expr, scope *symbols.Scope) bool {
	switch e := expr.(type) {
	case *ast.Identifier:
		sym := scope.Lookup(e.Name)
		if sym == nil {
			return false
		}
		if sym.IsMut {
			return true
		}
		// 若标识符本身为 &mut T 参数或引用变量，则通过它可修改内部数据
		tInfo := c.types.Info(sym.Type)
		return (tInfo.Kind == types.KindRef || tInfo.Kind == types.KindRawPtr) && tInfo.IsMut
	case *ast.FieldAccessExpr:
		return c.isLValueRootMutable(e.Object, scope)
	case *ast.IndexExpr:
		return c.isLValueRootMutable(e.Object, scope)
	default:
		return false
	}
}

func (c *Checker) checkIfStmt(s *ast.IfStmt, expected types.TypeID, scope *symbols.Scope) types.TypeID {
	condType := c.checkExpr(s.Cond, c.types.Bool, scope)
	if condType != types.Invalid && condType != c.types.Bool {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-TYP-002",
			Severity:    "error",
			Message:     fmt.Sprintf("if 条件必须为布尔类型 (bool)，实际表达式类型为 '%s'", c.types.Format(condType)),
			File:        s.Cond.GetSpan().File,
			Span:        s.Cond.GetSpan(),
			Explanation: "WON 要求条件控制流表达式严格计算为 bool 类型，禁止非零整数自动转布尔。",
		})
	}

	thenType := c.checkBlock(s.Then, expected, scope)
	if s.Else == nil {
		if expected != types.Invalid && expected != c.types.Unit && c.types.Info(thenType).Kind != types.KindNever && thenType != c.types.Unit {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-001",
				Severity: "error",
				Message:  fmt.Sprintf("期望得到值类型 '%s'，但缺少 'else' 分支的 if 表达式在条件为假时将返回 unit", c.types.Format(expected)),
				File:     s.Span.File,
				Span:     s.Span,
			})
		}
		if c.types.Info(thenType).Kind == types.KindNever {
			return c.types.Unit
		}
		return thenType
	}

	var elseType types.TypeID
	if elseBlock, ok := s.Else.(*ast.BlockStmt); ok {
		elseType = c.checkBlock(elseBlock, expected, scope)
	} else {
		elseType = c.checkStatement(s.Else, expected, scope)
	}

	if c.types.Info(thenType).Kind == types.KindNever {
		return elseType
	}
	if c.types.Info(elseType).Kind == types.KindNever {
		return thenType
	}
	if !c.types.CanAssign(elseType, thenType) && !c.types.CanAssign(thenType, elseType) {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-TYP-001",
			Severity: "error",
			Message:  fmt.Sprintf("if/else 分支返回类型不一致: 'then' 为 '%s', 'else' 为 '%s'", c.types.Format(thenType), c.types.Format(elseType)),
			File:     s.Span.File,
			Span:     s.Span,
		})
		return types.Invalid
	}
	if expected != types.Invalid && c.types.CanAssign(elseType, expected) {
		return expected
	}
	return thenType
}

func (c *Checker) checkForStmt(s *ast.ForStmt, scope *symbols.Scope) {
	iterType := c.checkExpr(s.Iter, types.Invalid, scope)
	elemType := types.Invalid

	if _, isRange := s.Iter.(*ast.RangeExpr); isRange {
		elemType = iterType
		if elemType == types.Invalid {
			elemType = c.types.I64
		}
	} else if iterType != types.Invalid {
		tInfo := c.types.Info(iterType)
		if tInfo.Kind == types.KindRef {
			tInfo = c.types.Info(tInfo.Elem)
		}
		if tInfo.Kind == types.KindArray || tInfo.Kind == types.KindSlice {
			elemType = tInfo.Elem
		} else {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-002",
				Severity: "error",
				Message:  fmt.Sprintf("for 循环期望范围或数组/切片可迭代对象，实际遇到 '%s'", c.types.Format(iterType)),
				File:     s.Iter.GetSpan().File,
				Span:     s.Iter.GetSpan(),
			})
		}
	}

	bodyScope := symbols.NewScope(symbols.ScopeBlock, scope)
	if s.Pattern != nil {
		c.bindLetPattern(s.Pattern, elemType, false, s.Span, bodyScope)
	}
	c.loopDepth++
	c.checkBlock(s.Body, types.Invalid, bodyScope)
	c.loopDepth--
}

func (c *Checker) checkReturnStmt(s *ast.ReturnStmt, scope *symbols.Scope) {
	if scope.OwnerFn == nil {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-NAM-005",
			Severity: "error",
			Message:  "'return' 语句不能出现在函数体外部",
			File:     s.Span.File,
			Span:     s.Span,
		})
		return
	}
	expectedRet := scope.OwnerFn.ReturnType
	if s.Value == nil {
		if expectedRet != c.types.Unit && expectedRet != types.Invalid {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-TYP-007",
				Severity: "error",
				Message:  fmt.Sprintf("函数声明返回类型为 '%s'，return 语句缺少返回值", c.types.Format(expectedRet)),
				File:     s.Span.File,
				Span:     s.Span,
			})
		}
		return
	}

	valType := c.checkExpr(s.Value, expectedRet, scope)
	if valType != types.Invalid && expectedRet != types.Invalid && !c.types.CanAssign(valType, expectedRet) {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-TYP-007",
			Severity:    "error",
			Message:     fmt.Sprintf("return 返回值类型不匹配: 期望 '%s'，实际为 '%s'", c.types.Format(expectedRet), c.types.Format(valType)),
			File:        s.Value.GetSpan().File,
			Span:        s.Value.GetSpan(),
			Explanation: "函数返回值必须严格匹配函数签名的返回类型声明，禁止隐式数值转换。",
		})
	}
}

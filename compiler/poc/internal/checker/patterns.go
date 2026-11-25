package checker

import (
	"fmt"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// matchCoverage 跟踪无守卫分支对有限值域类型 (bool / Enum / Option / Result) 的覆盖状态
type matchCoverage struct {
	hasWildcard bool
	boolTrue    bool
	boolFalse   bool
	variants    map[string]bool
}

func (c *Checker) checkMatchExpr(m *ast.MatchExpr, expected types.TypeID, scope *symbols.Scope) types.TypeID {
	c.typed.markCodegenUnsupported("match 模式匹配尚未在 C11 后端实现代码生成", m.Span)

	subjType := c.checkExpr(m.Subject, types.Invalid, scope)
	cov := matchCoverage{variants: make(map[string]bool)}

	joined := types.Invalid
	hadNonNever := false

	for _, arm := range m.Arms {
		armScope := symbols.NewScope(symbols.ScopeMatchArm, scope)
		patCoveredKey, isWildcard := c.checkPatternAgainstType(arm.Pattern, subjType, armScope)

		// 仅无守卫分支才计入穷尽性覆盖与冗余检测
		hasGuard := arm.Guard != nil
		if !hasGuard && subjType != types.Invalid {
			if cov.hasWildcard || c.isDomainAlreadyCovered(subjType, cov) {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:        "WON-MAT-004",
					Severity:    "error",
					Message:     "不可达的模式匹配分支：之前的无条件分支已完全覆盖所有剩余情况",
					File:        arm.Span.File,
					Span:        arm.Span,
					Explanation: "之前的通配符 '_' 或有限值域的全部变体已经完全覆盖该类型。",
				})
			} else if isWildcard {
				cov.hasWildcard = true
			} else if patCoveredKey != "" {
				if cov.variants[patCoveredKey] {
					c.report.AddDiagnostic(diagnostic.Diagnostic{
						Code:     "WON-MAT-004",
						Severity: "error",
						Message:  fmt.Sprintf("重复的模式匹配分支: '%s' 已被前面的无守卫分支完全覆盖", patCoveredKey),
						File:     arm.Span.File,
						Span:     arm.Span,
					})
				}
				cov.variants[patCoveredKey] = true
				if patCoveredKey == "true" {
					cov.boolTrue = true
				} else if patCoveredKey == "false" {
					cov.boolFalse = true
				}
			}
		}

		if arm.Guard != nil {
			guardType := c.checkExpr(arm.Guard, c.types.Bool, armScope)
			if guardType != types.Invalid && guardType != c.types.Bool {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-TYP-002",
					Severity: "error",
					Message:  fmt.Sprintf("match 分支守卫条件必须为 bool 类型，实际为 '%s'", c.types.Format(guardType)),
					File:     arm.Guard.GetSpan().File,
					Span:     arm.Guard.GetSpan(),
				})
			}
		}

		// 若外层提供 expected 类型则向下传播；否则在已确定前序非发散类型时传播上下文
		armHint := expected
		if armHint == types.Invalid {
			armHint = joined
		}
		armType := c.checkBlock(arm.Body, armHint, armScope)
		if c.types.Info(armType).Kind == types.KindNever {
			continue
		}

		if expected != types.Invalid {
			if armType != types.Invalid && !c.types.CanAssign(armType, expected) {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:        "WON-MAT-003",
					Severity:    "error",
					Message:     fmt.Sprintf("match 分支返回类型不兼容: 期望 '%s'，本分支返回 '%s'", c.types.Format(expected), c.types.Format(armType)),
					File:        arm.Body.Span.File,
					Span:        arm.Body.Span,
					Explanation: "match 表达式所有非发散分支必须能够生成外层期望类型。",
				})
			}
			hadNonNever = true
			joined = expected
			continue
		}

		if !hadNonNever {
			joined = armType
			hadNonNever = true
			continue
		}

		common, ok := c.types.CommonType(joined, armType)
		if !ok {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-MAT-003",
				Severity:    "error",
				Message:     fmt.Sprintf("match 分支结果类型无法求取公共类型: 先前公共类型为 '%s'，本分支返回 '%s'", c.types.Format(joined), c.types.Format(armType)),
				File:        arm.Body.Span.File,
				Span:        arm.Body.Span,
				Explanation: "match 表达式各分支必须拥有完全一致的类型或合法的严格公共超类型（例如 &mut T 与 &T 联结为 &T）。",
			})
		} else {
			joined = common
		}
	}

	// 验证有限类型与无限类型穷尽性 (WON-MAT-001)
	if subjType != types.Invalid {
		c.verifyMatchExhaustiveness(subjType, cov, m.Span)
	}

	if !hadNonNever {
		if expected != types.Invalid {
			return expected
		}
		return c.types.Never
	}
	return joined
}

// checkPatternAgainstType 检查模式是否与目标类型兼容并绑定变量；
// 返回 (唯一覆盖标签键, 是否完全通配)。注意仅在简单模式且覆盖整个值或整个变体时返回非空 key。
func (c *Checker) checkPatternAgainstType(pat ast.Pattern, targetType types.TypeID, scope *symbols.Scope) (string, bool) {
	if pat == nil || targetType == types.Invalid {
		return "", false
	}
	// 自动透传解引用引用以支持对 &Enum 或 &Struct 进行 match (例如 calculate_area(shape: &Shape))
	baseType := targetType
	if c.types.Info(baseType).Kind == types.KindRef {
		baseType = c.types.Info(baseType).Elem
	}
	baseInfo := c.types.Info(baseType)

	switch p := pat.(type) {
	case *ast.WildcardPattern:
		return "_", true

	case *ast.IdentPattern:
		// 检查名称是否恰好指向单元枚举变体 (如 None / Ready)
		if !p.IsMut && !p.IsRef && baseInfo.Kind == types.KindEnum {
			ownerSym := c.symbols.ByID[symbols.SymbolID(baseInfo.SymbolID)]
			if ownerSym != nil {
				if vi, ok := ownerSym.VariantMap[p.Name]; ok {
					if vi.Kind != symbols.VariantUnit {
						c.report.AddDiagnostic(diagnostic.Diagnostic{
							Code:     "WON-MAT-002",
							Severity: "error",
							Message:  fmt.Sprintf("枚举变体 '%s' 携带载荷，模式中必须显式解构字段", p.Name),
							File:     p.Span.File,
							Span:     p.Span,
						})
					}
					c.typed.VariantRefs[p] = vi
					return p.Name, false
				}
			}
		}

		// 否则视为全覆盖变量绑定模式
		sym := c.symbols.AllocSymbol(symbols.SymVar, p.Name, p.Span)
		sym.IsMut = p.IsMut
		sym.Type = targetType
		sym.Defined = true
		scope.Insert(sym)
		c.symbols.NodeSymbols[p] = sym
		return p.Name, true

	case *ast.LiteralPattern:
		litType := c.checkExpr(p.Literal, baseType, scope)
		if litType != types.Invalid && !c.types.CanAssign(litType, baseType) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-MAT-002",
				Severity: "error",
				Message:  fmt.Sprintf("字面量模式类型 '%s' 与被匹配目标类型 '%s' 不兼容", c.types.Format(litType), c.types.Format(baseType)),
				File:     p.Span.File,
				Span:     p.Span,
			})
			return "", false
		}
		if bl, ok := p.Literal.(*ast.BoolLiteral); ok {
			if bl.Value {
				return "true", false
			}
			return "false", false
		}
		return "", false

	case *ast.TuplePattern:
		if baseInfo.Kind != types.KindTuple || len(baseInfo.Elems) != len(p.Elems) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-MAT-002",
				Severity: "error",
				Message:  fmt.Sprintf("元组模式长度 (%d) 与匹配目标类型 '%s' 不兼容", len(p.Elems), c.types.Format(baseType)),
				File:     p.Span.File,
				Span:     p.Span,
			})
			return "", false
		}
		allWildcard := true
		for i, ep := range p.Elems {
			_, ew := c.checkPatternAgainstType(ep, baseInfo.Elems[i], scope)
			if !ew {
				allWildcard = false
			}
		}
		return "", allWildcard

	case *ast.EnumPattern:
		if baseInfo.Kind != types.KindEnum {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-MAT-002",
				Severity: "error",
				Message:  fmt.Sprintf("类型 '%s' 不是枚举类型，无法应用枚举模式 '%s'", c.types.Format(baseType), strings.Join(p.Path, "::")),
				File:     p.Span.File,
				Span:     p.Span,
			})
			return "", false
		}
		enumSym := c.symbols.ByID[symbols.SymbolID(baseInfo.SymbolID)]
		if enumSym == nil {
			return "", false
		}
		variantName := p.Path[len(p.Path)-1]
		if len(p.Path) == 2 && p.Path[0] != enumSym.Name {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-MAT-002",
				Severity: "error",
				Message:  fmt.Sprintf("枚举路径 '%s' 与期望枚举类型 '%s' 不匹配", p.Path[0], enumSym.Name),
				File:     p.Span.File,
				Span:     p.Span,
			})
			return "", false
		}
		vi, ok := enumSym.VariantMap[variantName]
		if !ok {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-ADT-005",
				Severity: "error",
				Message:  fmt.Sprintf("枚举 '%s' 不存在变体 '%s'", enumSym.Name, variantName),
				File:     p.Span.File,
				Span:     p.Span,
			})
			return "", false
		}
		c.typed.VariantRefs[p] = vi

		// 执行泛型实参替换 (支撑 Option[i32], Result[T, E] 及泛型枚举)
		subst := make(map[uint64]types.TypeID)
		if len(enumSym.GenericParamIDs) == len(baseInfo.GenericArgs) {
			for i, gid := range enumSym.GenericParamIDs {
				subst[uint64(gid)] = baseInfo.GenericArgs[i]
			}
		}

		if len(p.Args) != len(vi.TupleTypes) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-ADT-006",
				Severity: "error",
				Message:  fmt.Sprintf("枚举变体 '%s::%s' 期望 %d 个载荷参数，模式提供 %d 个", enumSym.Name, variantName, len(vi.TupleTypes), len(p.Args)),
				File:     p.Span.File,
				Span:     p.Span,
			})
			return variantName, false
		}

		allSimple := true
		for i, argPat := range p.Args {
			pt := c.types.Substitute(vi.TupleTypes[i], subst)
			_, isW := c.checkPatternAgainstType(argPat, pt, scope)
			if !isW {
				allSimple = false
			}
		}
		if allSimple {
			return variantName, false
		}
		return "", false

	case *ast.StructPattern:
		// 既可匹配命名结构体，也可匹配枚举的结构体变体 (Shape::Rectangle { width, height })
		if len(p.Path) == 2 && baseInfo.Kind == types.KindEnum {
			enumSym := c.symbols.ByID[symbols.SymbolID(baseInfo.SymbolID)]
			if enumSym == nil || p.Path[0] != enumSym.Name {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-MAT-002",
					Severity: "error",
					Message:  fmt.Sprintf("枚举路径 '%s' 与期望类型 '%s' 不匹配", strings.Join(p.Path, "::"), c.types.Format(baseType)),
					File:     p.Span.File,
					Span:     p.Span,
				})
				return "", false
			}
			vName := p.Path[1]
			vi, ok := enumSym.VariantMap[vName]
			if !ok {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-ADT-005",
					Severity: "error",
					Message:  fmt.Sprintf("枚举 '%s' 不存在变体 '%s'", enumSym.Name, vName),
					File:     p.Span.File,
					Span:     p.Span,
				})
				return "", false
			}
			c.typed.VariantRefs[p] = vi
			allSimple := c.checkStructPatternFields(enumSym.Name+"::"+vName, vi.FieldMap, nil, p.Fields, scope)
			if allSimple {
				return vName, false
			}
			return "", false
		}

		if baseInfo.Kind != types.KindStruct {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-MAT-002",
				Severity: "error",
				Message:  fmt.Sprintf("结构体模式 '%s' 与匹配目标类型 '%s' 不兼容", strings.Join(p.Path, "::"), c.types.Format(baseType)),
				File:     p.Span.File,
				Span:     p.Span,
			})
			return "", false
		}
		stSym := c.symbols.ByID[symbols.SymbolID(baseInfo.SymbolID)]
		if stSym == nil || (len(p.Path) > 0 && p.Path[len(p.Path)-1] != stSym.Name) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-MAT-002",
				Severity: "error",
				Message:  fmt.Sprintf("结构体模式名称与目标结构体 '%s' 不匹配", c.types.Format(baseType)),
				File:     p.Span.File,
				Span:     p.Span,
			})
			return "", false
		}
		subst := make(map[uint64]types.TypeID)
		for i, gid := range stSym.GenericParamIDs {
			if i < len(baseInfo.GenericArgs) {
				subst[uint64(gid)] = baseInfo.GenericArgs[i]
			}
		}
		allSimple := c.checkStructPatternFields(stSym.Name, stSym.FieldMap, subst, p.Fields, scope)
		if allSimple {
			return stSym.Name, true
		}
		return "", false
	}

	return "", false
}

func (c *Checker) checkStructPatternFields(ownerName string, fieldMap map[string]*symbols.FieldInfo, subst map[uint64]types.TypeID, fields []*ast.FieldPattern, scope *symbols.Scope) bool {
	allSimple := true
	for _, fp := range fields {
		defF, ok := fieldMap[fp.Name]
		if !ok {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-ADT-001",
				Severity: "error",
				Message:  fmt.Sprintf("'%s' 中不存在字段 '%s'", ownerName, fp.Name),
				File:     fp.Span.File,
				Span:     fp.Span,
			})
			allSimple = false
			continue
		}
		ft := c.types.Substitute(defF.Type, subst)
		if fp.Pattern != nil {
			_, isW := c.checkPatternAgainstType(fp.Pattern, ft, scope)
			if !isW {
				allSimple = false
			}
		} else {
			sym := c.symbols.AllocSymbol(symbols.SymVar, fp.Name, fp.Span)
			sym.Type = ft
			sym.Defined = true
			scope.Insert(sym)
		}
	}
	return allSimple
}

func (c *Checker) isDomainAlreadyCovered(subjType types.TypeID, cov matchCoverage) bool {
	if cov.hasWildcard {
		return true
	}
	baseType := subjType
	if c.types.Info(baseType).Kind == types.KindRef {
		baseType = c.types.Info(baseType).Elem
	}
	info := c.types.Info(baseType)
	switch info.Kind {
	case types.KindBool:
		return cov.boolTrue && cov.boolFalse
	case types.KindEnum:
		enumSym := c.symbols.ByID[symbols.SymbolID(info.SymbolID)]
		if enumSym == nil || len(enumSym.Variants) == 0 {
			return false
		}
		for _, v := range enumSym.Variants {
			if !cov.variants[v.Name] {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (c *Checker) verifyMatchExhaustiveness(subjType types.TypeID, cov matchCoverage, span diagnostic.Span) {
	if cov.hasWildcard {
		return
	}
	baseType := subjType
	if c.types.Info(baseType).Kind == types.KindRef {
		baseType = c.types.Info(baseType).Elem
	}
	info := c.types.Info(baseType)

	switch info.Kind {
	case types.KindBool:
		var missing []string
		if !cov.boolTrue {
			missing = append(missing, "true")
		}
		if !cov.boolFalse {
			missing = append(missing, "false")
		}
		if len(missing) > 0 {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-MAT-001",
				Severity:    "error",
				Message:     fmt.Sprintf("match 布尔匹配不穷尽，缺少分支: %s", strings.Join(missing, ", ")),
				File:        span.File,
				Span:        span,
				Explanation: "对 bool 进行模式匹配必须覆盖 true 与 false，或提供无守卫通配符 '_' 分支。",
			})
		}

	case types.KindEnum:
		enumSym := c.symbols.ByID[symbols.SymbolID(info.SymbolID)]
		if enumSym == nil {
			return
		}
		var missing []string
		for _, v := range enumSym.Variants {
			if !cov.variants[v.Name] {
				missing = append(missing, v.Name)
			}
		}
		if len(missing) > 0 {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-MAT-001",
				Severity:    "error",
				Message:     fmt.Sprintf("对枚举 '%s' 的模式匹配不穷尽，未覆盖变体: %s", enumSym.Name, strings.Join(missing, ", ")),
				File:        span.File,
				Span:        span,
				Explanation: "必须覆盖所有枚举变体，或添加无守卫兜底分支 '_'（注意带有 if 守卫的分支不计入穷尽覆盖）。",
			})
		}

	default:
		// 整数、字符串、浮点数等开放值域必须存在无守卫通配分支
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-MAT-001",
			Severity:    "error",
			Message:     fmt.Sprintf("对开放类型 '%s' 的模式匹配不穷尽，缺少无守卫通配分支 '_'", c.types.Format(baseType)),
			File:        span.File,
			Span:        span,
			Explanation: "整数、字符串等非有限枚举类型必须提供无条件通配分支以确保所有运行时情况均被处理。",
		})
	}
}

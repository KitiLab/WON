package checker

import (
	"fmt"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// collectAndResolveDeclarations 执行两阶段顶层声明收集与签名/类型解析
// 阶段 1: 注册所有具名类型与顶层函数符号（支持前向相互调用和递归）。
// 阶段 2: 解析类型别名、结构体字段、枚举变体载荷、函数签名以及固有 impl 方法关联。
func (c *Checker) collectAndResolveDeclarations(prog *ast.Program) {
	scope := c.symbols.GlobalScope

	// 拒绝未实现的多文件外部 WON 模块导入 (WON-NAM-004)
	for _, imp := range prog.ModuleImports {
		pathStr := strings.Join(imp.Path, "::")
		if modSym := scope.Lookup(imp.Path[0]); modSym != nil && modSym.Kind == symbols.SymModule {
			continue
		}
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-NAM-004",
			Severity:    "error",
			Message:     fmt.Sprintf("无法解析外部模块导入: %q (多文件跨包模块加载器尚未在 Phase 2A 实现)", pathStr),
			File:        imp.Span.File,
			Span:        imp.Span,
			Explanation: "当前里程碑支持单编译单元内的符号与类型解析以及 #import C 头文件导入；外部 WON 模块解析将在后续包系统里程碑中实现。",
		})
	}

	// Pass 1: 收集所有顶层声明名并检测同作用域重复定义
	type pendingStruct struct {
		decl *ast.StructDecl
		sym  *symbols.Symbol
		gs   *symbols.Scope
	}
	type pendingEnum struct {
		decl *ast.EnumDecl
		sym  *symbols.Symbol
		gs   *symbols.Scope
	}
	type pendingAlias struct {
		decl *ast.TypeAliasDecl
		sym  *symbols.Symbol
		gs   *symbols.Scope
	}
	type pendingFunc struct {
		decl *ast.FunctionDecl
		sym  *symbols.Symbol
		gs   *symbols.Scope
	}

	var pStructs []pendingStruct
	var pEnums []pendingEnum
	var pAliases []pendingAlias
	var pFuncs []pendingFunc
	var pImpls []*ast.ImplBlock

	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.ImportDecl, *ast.ModuleImportDecl:
			// 已预处理

		case *ast.StructDecl:
			sym := c.symbols.AllocSymbol(symbols.SymStruct, d.Name, d.Span)
			sym.IsPub = d.IsPub
			sym.Decl = d
			gScope := c.bindGenericParams(sym, d.GenericParams, scope)
			genArgs := c.genericParamTypeIDs(sym)
			sym.Type = c.types.Struct(uint64(sym.ID), sym.Name, genArgs)
			if existing, ok := scope.Insert(sym); !ok {
				c.emitDuplicateDecl(d.Name, d.Span, existing.Span)
			}
			pStructs = append(pStructs, pendingStruct{decl: d, sym: sym, gs: gScope})
			c.typed.markCodegenUnsupported("struct 声明与字段布局尚未在 C11 后端实现代码生成", d.Span)

		case *ast.EnumDecl:
			sym := c.symbols.AllocSymbol(symbols.SymEnum, d.Name, d.Span)
			sym.IsPub = d.IsPub
			sym.Decl = d
			sym.VariantMap = make(map[string]*symbols.VariantInfo)
			gScope := c.bindGenericParams(sym, d.GenericParams, scope)
			genArgs := c.genericParamTypeIDs(sym)
			sym.Type = c.types.Enum(uint64(sym.ID), sym.Name, genArgs)
			if existing, ok := scope.Insert(sym); !ok {
				c.emitDuplicateDecl(d.Name, d.Span, existing.Span)
			}
			pEnums = append(pEnums, pendingEnum{decl: d, sym: sym, gs: gScope})
			c.typed.markCodegenUnsupported("enum 代数数据类型尚未在 C11 后端实现代码生成", d.Span)

		case *ast.TypeAliasDecl:
			sym := c.symbols.AllocSymbol(symbols.SymTypeAlias, d.Name, d.Span)
			sym.IsPub = d.IsPub
			sym.Decl = d
			gScope := c.bindGenericParams(sym, d.GenericParams, scope)
			if existing, ok := scope.Insert(sym); !ok {
				c.emitDuplicateDecl(d.Name, d.Span, existing.Span)
			}
			pAliases = append(pAliases, pendingAlias{decl: d, sym: sym, gs: gScope})

		case *ast.FunctionDecl:
			sym := c.symbols.AllocSymbol(symbols.SymFunc, d.Name, d.Span)
			sym.IsPub = d.IsPub
			sym.Decl = d
			gScope := c.bindGenericParams(sym, d.GenericParams, scope)
			if existing, ok := scope.Insert(sym); !ok {
				c.emitDuplicateDecl(d.Name, d.Span, existing.Span)
			}
			pFuncs = append(pFuncs, pendingFunc{decl: d, sym: sym, gs: gScope})
			if len(d.GenericParams) > 0 {
				c.typed.markCodegenUnsupported("泛型函数单态化尚未在 C11 后端实现代码生成", d.Span)
			}

		case *ast.ImplBlock:
			pImpls = append(pImpls, d)
			c.typed.markCodegenUnsupported("impl 关联方法实现尚未在 C11 后端实现代码生成", d.Span)

		case *ast.TraitDecl:
			sym := c.symbols.AllocSymbol(symbols.SymTrait, d.Name, d.Span)
			sym.IsPub = d.IsPub
			sym.Decl = d
			if existing, ok := scope.Insert(sym); !ok {
				c.emitDuplicateDecl(d.Name, d.Span, existing.Span)
			}
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-SEM-010",
				Severity:    "error",
				Message:     fmt.Sprintf("特质声明 'trait %s' 已成功解析，但完整特质求解器 (Trait Solver) 尚未在 Phase 2A 实现", d.Name),
				File:        d.Span.File,
				Span:        d.Span,
				Explanation: "Phase 2A 重点解决符号表、核心类型检查与结构体/枚举/模式穷尽性校验；特质与泛型约束求解属于后续里程碑。",
			})

		case *ast.ModuleDecl:
			modSym := c.symbols.AllocSymbol(symbols.SymModule, d.Name, d.Span)
			modSym.IsPub = d.IsPub
			modSym.Decl = d
			if existing, ok := scope.Insert(modSym); !ok {
				c.emitDuplicateDecl(d.Name, d.Span, existing.Span)
			}
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-SEM-010",
				Severity: "error",
				Message:  fmt.Sprintf("内联模块声明 'mod %s' 的完整隔离命名空间与多文件包解析将在后续里程碑实现", d.Name),
				File:     d.Span.File,
				Span:     d.Span,
			})
		}
	}

	// Pass 2A: 解析类型别名
	for _, pa := range pAliases {
		pa.sym.Type = c.resolveAstType(pa.decl.TargetType, pa.gs)
	}

	// Pass 2B: 解析结构体字段类型与可见性/可变性
	for _, ps := range pStructs {
		c.resolveStructFields(ps.decl, ps.sym, ps.gs)
	}

	// Pass 2C: 解析枚举变体定义与载荷类型
	for _, pe := range pEnums {
		c.resolveEnumVariants(pe.decl, pe.sym, pe.gs)
	}

	// Pass 2D: 解析顶层函数参数与返回类型签名
	for _, pf := range pFuncs {
		c.resolveFunctionSignature(pf.decl, pf.sym, pf.gs)
	}

	// Pass 2E: 解析固有 impl 块并将其方法绑定至对应 Struct/Enum
	for _, ib := range pImpls {
		c.resolveImplBlock(ib, scope)
	}
}

func (c *Checker) bindGenericParams(owner *symbols.Symbol, params []*ast.GenericParam, parent *symbols.Scope) *symbols.Scope {
	if len(params) == 0 {
		return parent
	}
	gScope := symbols.NewScope(symbols.ScopeImpl, parent)
	for _, gp := range params {
		if len(gp.Bounds) > 0 {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-SEM-010",
				Severity:    "error",
				Message:     fmt.Sprintf("泛型参数 '%s' 的特质边界约束求解尚未在 Phase 2A 实现", gp.Name),
				File:        gp.Span.File,
				Span:        gp.Span,
				Explanation: "Phase 2A 支持核心泛型类型参数标识与 Option/Result 实例化；完整特质约束求解留待后续阶段。",
			})
		}
		gpSym := c.symbols.AllocSymbol(symbols.SymTypeParam, gp.Name, gp.Span)
		gpSym.Type = c.types.TypeParam(uint64(gpSym.ID), gp.Name)
		if _, ok := gScope.Insert(gpSym); !ok {
			c.emitDuplicateDecl(gp.Name, gp.Span, gp.Span)
		}
		owner.GenericParamIDs = append(owner.GenericParamIDs, gpSym.ID)
		owner.GenericParamNames = append(owner.GenericParamNames, gp.Name)
	}
	return gScope
}

func (c *Checker) genericParamTypeIDs(sym *symbols.Symbol) []types.TypeID {
	ids := make([]types.TypeID, len(sym.GenericParamIDs))
	for i, gid := range sym.GenericParamIDs {
		ids[i] = c.symbols.ByID[gid].Type
	}
	return ids
}

func (c *Checker) resolveAstType(tn ast.TypeNode, scope *symbols.Scope) types.TypeID {
	id := c.symbols.ResolveTypeNode(tn, scope, c.report)
	if tn != nil {
		c.markCodegenIfUnsupportedType(id, tn.GetSpan())
	}
	return id
}

func (c *Checker) validateSizedType(id types.TypeID, span diagnostic.Span, ctxDesc string) {
	if id != types.Invalid && !c.types.IsSized(id) {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-TYP-006",
			Severity:    "error",
			Message:     fmt.Sprintf("无尺寸动态类型 '%s' 不能直接按值存储于%s (请使用 '&%s' 或 'String')", c.types.Format(id), ctxDesc, c.types.Format(id)),
			File:        span.File,
			Span:        span,
			Explanation: "依据三层字符串与切片模型规范，str 与 [T] 属于无编译期固定大小类型 (DST)，必须通过引用 (&str / &[T]) 或所有权容器 (String / Vec[T]) 持有。",
		})
	}
}

func (c *Checker) resolveStructFields(decl *ast.StructDecl, sym *symbols.Symbol, scope *symbols.Scope) {
	for _, f := range decl.Fields {
		ft := c.resolveAstType(f.Type, scope)
		c.validateSizedType(ft, f.Span, "结构体字段")
		if _, dup := sym.FieldMap[f.Name]; dup {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-ADT-003",
				Severity: "error",
				Message:  fmt.Sprintf("结构体 '%s' 包含重复字段名 '%s'", sym.Name, f.Name),
				File:     f.Span.File,
				Span:     f.Span,
			})
			continue
		}
		info := symbols.FieldInfo{
			Name:  f.Name,
			Type:  ft,
			IsPub: f.IsPub,
			IsVar: f.IsVar,
			Span:  f.Span,
		}
		sym.Fields = append(sym.Fields, info)
		idx := len(sym.Fields) - 1
		sym.FieldMap[f.Name] = &sym.Fields[idx]
	}
}

func (c *Checker) resolveEnumVariants(decl *ast.EnumDecl, sym *symbols.Symbol, scope *symbols.Scope) {
	for _, v := range decl.Variants {
		if _, dup := sym.VariantMap[v.Name]; dup {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-ADT-006",
				Severity: "error",
				Message:  fmt.Sprintf("枚举 '%s' 包含重复变体 '%s'", sym.Name, v.Name),
				File:     v.Span.File,
				Span:     v.Span,
			})
			continue
		}
		varSym := c.symbols.AllocSymbol(symbols.SymEnumVariant, v.Name, v.Span)
		varSym.OwnerSymID = sym.ID
		varSym.OwnerType = sym.Type

		vi := symbols.VariantInfo{
			SymbolID: varSym.ID,
			Name:     v.Name,
			Kind:     symbols.VariantUnit,
			FieldMap: make(map[string]*symbols.FieldInfo),
			Span:     v.Span,
		}

		if len(v.TupleTypes) > 0 {
			vi.Kind = symbols.VariantTuple
			vi.TupleTypes = make([]types.TypeID, len(v.TupleTypes))
			for i, tt := range v.TupleTypes {
				pt := c.resolveAstType(tt, scope)
				c.validateSizedType(pt, tt.GetSpan(), "枚举变体载荷")
				vi.TupleTypes[i] = pt
			}
		} else if len(v.StructFields) > 0 {
			vi.Kind = symbols.VariantStruct
			for _, sf := range v.StructFields {
				ft := c.resolveAstType(sf.Type, scope)
				c.validateSizedType(ft, sf.Span, "结构体枚举变体字段")
				if _, dup := vi.FieldMap[sf.Name]; dup {
					c.report.AddDiagnostic(diagnostic.Diagnostic{
						Code:     "WON-ADT-003",
						Severity: "error",
						Message:  fmt.Sprintf("枚举变体 '%s::%s' 包含重复字段 '%s'", sym.Name, v.Name, sf.Name),
						File:     sf.Span.File,
						Span:     sf.Span,
					})
					continue
				}
				fi := symbols.FieldInfo{
					Name:  sf.Name,
					Type:  ft,
					IsPub: sf.IsPub,
					IsVar: sf.IsVar,
					Span:  sf.Span,
				}
				vi.StructFields = append(vi.StructFields, fi)
				idx := len(vi.StructFields) - 1
				vi.FieldMap[sf.Name] = &vi.StructFields[idx]
			}
		}

		sym.Variants = append(sym.Variants, vi)
		idx := len(sym.Variants) - 1
		sym.VariantMap[v.Name] = &sym.Variants[idx]
	}
}

func (c *Checker) resolveFunctionSignature(decl *ast.FunctionDecl, sym *symbols.Symbol, scope *symbols.Scope) {
	paramTypes := make([]types.TypeID, 0, len(decl.Params))
	paramNames := make([]string, 0, len(decl.Params))
	paramMut := make([]bool, 0, len(decl.Params))

	for _, p := range decl.Params {
		if p.IsSelf {
			sym.HasSelf = true
			sym.SelfIsRef = p.IsRef
			sym.SelfIsMut = p.IsMut
			continue
		}
		var pt types.TypeID
		if p.TypeAST != nil {
			pt = c.resolveAstType(p.TypeAST, scope)
		} else {
			pt = c.symbols.ResolveTypeString(p.Type, scope)
		}
		c.validateSizedType(pt, p.Span, "函数参数")
		paramTypes = append(paramTypes, pt)
		paramNames = append(paramNames, p.Name)
		paramMut = append(paramMut, p.IsMut)
	}

	retType := c.types.Unit
	if decl.ReturnTypeAST != nil {
		retType = c.resolveAstType(decl.ReturnTypeAST, scope)
		c.validateSizedType(retType, decl.ReturnTypeAST.GetSpan(), "函数返回值")
	} else if decl.ReturnType != "" && decl.ReturnType != "void" && decl.ReturnType != "unit" {
		retType = c.symbols.ResolveTypeString(decl.ReturnType, scope)
	}

	sym.ParamNames = paramNames
	sym.ParamTypes = paramTypes
	sym.ParamIsMut = paramMut
	sym.ReturnType = retType
	sym.Type = c.types.Fn(paramTypes, retType)
	sym.Defined = true
}

func (c *Checker) resolveImplBlock(ib *ast.ImplBlock, scope *symbols.Scope) {
	// 严格禁止将 trait impl 静默当作固有方法处理
	if ib.TraitType != nil {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-SEM-010",
			Severity:    "error",
			Message:     fmt.Sprintf("特质实现块 'impl %s for ...' 已通过语法解析，但特质分派与验证将在后续里程碑实现", ib.TraitType.String()),
			File:        ib.Span.File,
			Span:        ib.Span,
			Explanation: "编译器不会将特质方法静默降级为固有方法；完整特质实现校验将在 Trait Solver 阶段完成。",
		})
		return
	}

	// 将 Self 绑定至目标类型以便方法签名解析
	implScope := symbols.NewScope(symbols.ScopeImpl, scope)
	if len(ib.GenericParams) > 0 {
		for _, gp := range ib.GenericParams {
			gpSym := c.symbols.AllocSymbol(symbols.SymTypeParam, gp.Name, gp.Span)
			gpSym.Type = c.types.TypeParam(uint64(gpSym.ID), gp.Name)
			implScope.Insert(gpSym)
		}
	}

	targetTypeID := c.resolveAstType(ib.TargetType, implScope)
	if targetTypeID == types.Invalid {
		return
	}
	targetInfo := c.types.Info(targetTypeID)
	if targetInfo.Kind != types.KindStruct && targetInfo.Kind != types.KindEnum {
		c.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:     "WON-TYP-001",
			Severity: "error",
			Message:  fmt.Sprintf("只能为命名结构体或枚举实现固有方法，不能为 '%s' 实现", c.types.Format(targetTypeID)),
			File:     ib.Span.File,
			Span:     ib.Span,
		})
		return
	}

	ownerSym := c.symbols.ByID[symbols.SymbolID(targetInfo.SymbolID)]
	if ownerSym == nil {
		return
	}

	// 注册 Self 类型别名指向目标结构体/枚举
	selfSym := c.symbols.AllocSymbol(symbols.SymTypeAlias, "Self", ib.TargetType.GetSpan())
	selfSym.Type = targetTypeID
	implScope.Insert(selfSym)

	for _, m := range ib.Methods {
		mSym := c.symbols.AllocSymbol(symbols.SymMethod, m.Name, m.Span)
		mSym.IsPub = m.IsPub
		mSym.OwnerSymID = ownerSym.ID
		mSym.OwnerType = targetTypeID
		mSym.Decl = m
		mScope := c.bindGenericParams(mSym, m.GenericParams, implScope)
		c.resolveFunctionSignature(m, mSym, mScope)
		if existing, exists := ownerSym.Methods[m.Name]; exists {
			c.emitDuplicateDecl(fmt.Sprintf("%s::%s", ownerSym.Name, m.Name), m.Span, existing.Span)
		} else {
			ownerSym.Methods[m.Name] = mSym
		}
	}
}

func (c *Checker) emitDuplicateDecl(name string, newSpan, prevSpan diagnostic.Span) {
	c.report.AddDiagnostic(diagnostic.Diagnostic{
		Code:        "WON-NAM-002",
		Severity:    "error",
		Message:     fmt.Sprintf("同一词法作用域内存在重复声明的标识符: %q", name),
		File:        newSpan.File,
		Span:        newSpan,
		Explanation: fmt.Sprintf("符号 '%s' 已在第 %d 行第 %d 列声明过。同层作用域禁止隐式重复声明。", name, prevSpan.Start.Line, prevSpan.Start.Column),
	})
}

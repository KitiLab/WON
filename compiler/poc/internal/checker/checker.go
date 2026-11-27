package checker

import (
	"fmt"
	"sort"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/borrow"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/drop"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// Checker 实现了 WON Phase 2A 核心静态语义检查与类型系统校验器
type Checker struct {
	report    *diagnostic.Report
	bindings  map[string]*bindgen.BindingMeta
	types     *types.Context
	symbols   *symbols.Table
	typed     *TypedProgram
	inUnsafe  bool
	loopDepth int
}

// New 创建语义分析器实例，注入已解析的 C 头文件绑定元数据
func New(report *diagnostic.Report, bindings map[string]*bindgen.BindingMeta) *Checker {
	if bindings == nil {
		bindings = make(map[string]*bindgen.BindingMeta)
	}
	tctx := types.NewContext()
	symTab := symbols.NewTable(tctx)
	return &Checker{
		report:   report,
		bindings: bindings,
		types:    tctx,
		symbols:  symTab,
	}
}

// CheckProgram 对整个 AST 程序执行两阶段声明收集、名称解析与双向类型校验，返回类型化语义模型
func (c *Checker) CheckProgram(prog *ast.Program) *TypedProgram {
	if prog == nil {
		return nil
	}
	c.typed = NewTypedProgram(prog, c.types, c.symbols)

	// Stage 1 & 2: 顶层声明收集与签名/类型解析
	c.collectAndResolveDeclarations(prog)

	// Stage 3: 检查所有顶层函数与固有方法体
	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.FunctionDecl:
			fnSym := c.symbols.GlobalScope.LookupLocal(d.Name)
			c.checkFunctionBody(d, fnSym, c.symbols.GlobalScope)
		case *ast.ImplBlock:
			if d.TraitType == nil {
				c.checkImplMethods(d)
			}
		}
	}

	// Stage 4: Phase 2B.1~2B.3 所有权 CFG 构建、借用/溯源校验与确定性 Drop 展开
	if !c.report.HasErrors() {
		progCFG := cfg.BuildProgram(prog, c.typed, c.report)
		c.typed.CFG = progCFG
		resReg := drop.NewResourceRegistry()
		nsKeys := make([]string, 0, len(c.bindings))
		for ns := range c.bindings {
			nsKeys = append(nsKeys, ns)
		}
		sort.Strings(nsKeys)
		var ffiCatalogs []*bindgen.FFIContractCatalog
		for _, ns := range nsKeys {
			bm := c.bindings[ns]
			if bm != nil && bm.Contracts != nil {
				ffiCatalogs = append(ffiCatalogs, bm.Contracts)
			}
		}
		if len(ffiCatalogs) > 0 {
			if err := bindgen.ValidateBindingContracts(c.bindings); err != nil {
				c.report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-INT-001",
					Severity: "error",
					Message:  fmt.Sprintf("FFI 所有权契约校验失败: %v", err),
				})
			} else if !c.report.HasErrors() {
				dropPlan := drop.ElaborateProgramWithFFI(progCFG, c.typed, resReg, ffiCatalogs, c.report)
				c.typed.DropPlan = dropPlan
				c.typed.DropElaborated = dropPlan != nil && dropPlan.DropElaborated && !c.report.HasErrors()
			}
		} else {
			if !c.report.HasErrors() {
				borrow.AnalyzeProgram(progCFG, c.typed, c.report)
			}
			if !c.report.HasErrors() {
				dropPlan := drop.ElaborateProgram(progCFG, c.typed, resReg, c.report)
				c.typed.DropPlan = dropPlan
				c.typed.DropElaborated = dropPlan != nil && dropPlan.DropElaborated && !c.report.HasErrors()
			}
		}
		c.typed.OwnershipChecked = !c.report.HasErrors() && c.typed.DropElaborated
	}

	// Stage 5: 若尚未存在语义/词法/所有权错误，执行严谨的 C11 后端能力契约审核 (WON-GEN-002 边界)
	if !c.report.HasErrors() {
		c.verifyBackendCapabilities(prog)
	}

	return c.typed
}

func (c *Checker) checkFunctionBody(fn *ast.FunctionDecl, fnSym *symbols.Symbol, parentScope *symbols.Scope) {
	if fn.Body == nil || fnSym == nil {
		return
	}

	fnScope := symbols.NewScope(symbols.ScopeFunction, parentScope)
	fnScope.OwnerFn = fnSym

	// 绑定泛型形参至函数局部作用域
	for _, gid := range fnSym.GenericParamIDs {
		gpSym := c.symbols.ByID[gid]
		fnScope.Insert(gpSym)
	}

	// 绑定函数参数到函数作用域
	paramIdx := 0
	for _, p := range fn.Params {
		if p.IsSelf {
			selfType := fnSym.OwnerType
			if p.IsRef {
				selfType = c.types.Ref(p.IsMut, selfType)
			}
			selfSym := c.symbols.AllocSymbol(symbols.SymParam, "self", p.Span)
			selfSym.Type = selfType
			selfSym.IsMut = p.IsMut
			selfSym.Defined = true
			selfSym.DeclHasInit = true
			fnScope.Insert(selfSym)
			c.symbols.NodeSymbols[p] = selfSym
			continue
		}
		if paramIdx < len(fnSym.ParamTypes) {
			pSym := c.symbols.AllocSymbol(symbols.SymParam, p.Name, p.Span)
			pSym.Type = fnSym.ParamTypes[paramIdx]
			pSym.IsMut = p.IsMut
			pSym.Defined = true
			pSym.DeclHasInit = true
			if existing, ok := fnScope.Insert(pSym); !ok {
				c.emitDuplicateDecl(p.Name, p.Span, existing.Span)
			}
			c.symbols.NodeSymbols[p] = pSym
		}
		paramIdx++
	}

	// 校验函数体并返回类型比对
	bodyType := c.checkBlock(fn.Body, fnSym.ReturnType, fnScope)
	if bodyType != types.Invalid && c.types.Info(bodyType).Kind != types.KindNever {
		// 如果末尾存在非分号隐式返回表达式，需比对返回类型；若仅由内部语句完成则返回 unit 需与声明一致
		hasImplicitTail := false
		if len(fn.Body.Statements) > 0 {
			if _, ok := fn.Body.Statements[len(fn.Body.Statements)-1].(*ast.ExprStmt); ok {
				hasImplicitTail = true
			}
		}
		if hasImplicitTail && !c.types.CanAssign(bodyType, fnSym.ReturnType) {
			c.report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-TYP-007",
				Severity:    "error",
				Message:     "函数末尾表达式类型 '" + c.types.Format(bodyType) + "' 与声明返回类型 '" + c.types.Format(fnSym.ReturnType) + "' 不兼容",
				File:        fn.Body.Span.File,
				Span:        fn.Body.Span,
				Explanation: "函数体最后一个表达式的值类型必须与函数签名的 -> ReturnType 严格匹配。",
			})
		}
	}
}

func (c *Checker) checkImplMethods(ib *ast.ImplBlock) {
	implScope := symbols.NewScope(symbols.ScopeImpl, c.symbols.GlobalScope)
	for _, gp := range ib.GenericParams {
		gpSym := c.symbols.AllocSymbol(symbols.SymTypeParam, gp.Name, gp.Span)
		gpSym.Type = c.types.TypeParam(uint64(gpSym.ID), gp.Name)
		implScope.Insert(gpSym)
	}
	targetTypeID := c.resolveAstType(ib.TargetType, implScope)
	if targetTypeID == types.Invalid {
		return
	}
	tInfo := c.types.Info(targetTypeID)
	ownerSym := c.symbols.ByID[symbols.SymbolID(tInfo.SymbolID)]
	if ownerSym == nil {
		return
	}

	selfSym := c.symbols.AllocSymbol(symbols.SymTypeAlias, "Self", ib.TargetType.GetSpan())
	selfSym.Type = targetTypeID
	implScope.Insert(selfSym)

	for _, m := range ib.Methods {
		mSym := ownerSym.Methods[m.Name]
		c.checkFunctionBody(m, mSym, implScope)
	}
}

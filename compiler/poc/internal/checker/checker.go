package checker

import (
	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
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
			fnScope.Insert(selfSym)
			continue
		}
		if paramIdx < len(fnSym.ParamTypes) {
			pSym := c.symbols.AllocSymbol(symbols.SymParam, p.Name, p.Span)
			pSym.Type = fnSym.ParamTypes[paramIdx]
			pSym.IsMut = p.IsMut
			pSym.Defined = true
			if existing, ok := fnScope.Insert(pSym); !ok {
				c.emitDuplicateDecl(p.Name, p.Span, existing.Span)
			}
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

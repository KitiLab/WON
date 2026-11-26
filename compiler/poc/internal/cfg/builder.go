package cfg

import (
	"fmt"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// SemanticView 抽象 CFG 构建所需的前端类型化查询视图，避免与 checker 包产生循环导入。
type SemanticView interface {
	GetTypes() *types.Context
	GetSymbols() *symbols.Table
	ExprType(expr ast.Expr) types.TypeID
	CallTarget(expr ast.Expr) *symbols.Symbol
	FieldRef(node ast.Node) *symbols.FieldInfo
	VariantRef(node ast.Node) *symbols.VariantInfo
}

type scopeFrame struct {
	id     int
	locals []symbols.SymbolID
}

type loopFrame struct {
	breakBB     BlockID
	continueBB  BlockID
	scopeDepth  int
	breakEdges  bool
	continueHit bool
}

type funcBuilder struct {
	view      SemanticView
	report    *diagnostic.Report
	fn        *FuncCFG
	curBB     BlockID
	nextScope int
	scopes    []scopeFrame
	loops     []loopFrame
}

// BuildProgram 基于已完成语义检查的 AST 与 SemanticView 构建全程序所有权 CFG。
// 遇到未处理节点、缺失符号绑定或无法解析的位置时触发 WON-INT-001，绝不静默跳过。
func BuildProgram(prog *ast.Program, view SemanticView, report *diagnostic.Report) *ProgramCFG {
	res := &ProgramCFG{
		Functions: make([]*FuncCFG, 0, len(prog.Functions)),
		BySymbol:  make(map[symbols.SymbolID]*FuncCFG),
	}
	if prog == nil || view == nil || view.GetSymbols() == nil {
		return res
	}
	symTab := view.GetSymbols()

	for _, decl := range prog.Decls {
		switch d := decl.(type) {
		case *ast.ImportDecl, *ast.ModuleImportDecl, *ast.StructDecl, *ast.EnumDecl, *ast.TypeAliasDecl:
			// 非可执行顶层类型或引入声明无需构建函数体 CFG

		case *ast.FunctionDecl:
			if d.Body == nil {
				continue
			}
			fnSym := symTab.GlobalScope.LookupLocal(d.Name)
			if fnSym == nil {
				if report != nil {
					report.AddDiagnostic(diagnostic.Diagnostic{
						Code:        "WON-INT-001",
						Severity:    "error",
						Message:     fmt.Sprintf("顶层函数 '%s' 在构建 CFG 时缺少全局符号表记录", d.Name),
						File:        d.Span.File,
						Span:        d.Span,
						Explanation: "Phase 2B.1 所有权 CFG 要求所有可分析函数已在符号表内解析登记。",
					})
				}
				continue
			}
			fcfg := buildFunction(d.Name, fnSym, d, view, report)
			res.Functions = append(res.Functions, fcfg)
			res.BySymbol[fnSym.ID] = fcfg

		case *ast.ImplBlock:
			if d.TraitType != nil {
				continue
			}
			for _, m := range d.Methods {
				if m.Body == nil {
					continue
				}
				mSym := symTab.NodeSymbols[m]
				if mSym == nil {
					mSym = findMethodSymbol(d, m.Name, symTab)
				}
				if mSym == nil {
					if report != nil {
						report.AddDiagnostic(diagnostic.Diagnostic{
							Code:        "WON-INT-001",
							Severity:    "error",
							Message:     fmt.Sprintf("固有方法 '%s' 在构建 CFG 时缺少方法符号记录", m.Name),
							File:        m.Span.File,
							Span:        m.Span,
							Explanation: "Phase 2B.1 所有权 CFG 要求所有 impl 方法已在类型符号表中关联。",
						})
					}
					continue
				}
				qName := mSym.Name
				if mSym.OwnerSymID != 0 {
					if owner := symTab.ByID[mSym.OwnerSymID]; owner != nil {
						qName = owner.Name + "::" + mSym.Name
					}
				}
				fcfg := buildFunction(qName, mSym, m, view, report)
				res.Functions = append(res.Functions, fcfg)
				res.BySymbol[mSym.ID] = fcfg
			}

		case *ast.TraitDecl, *ast.ModuleDecl:
			// 已在声明检查阶段发出 WON-SEM-010

		default:
			if report != nil {
				report.AddDiagnostic(diagnostic.Diagnostic{
					Code:        "WON-INT-001",
					Severity:    "error",
					Message:     fmt.Sprintf("顶层声明类型 %T 在所有权 CFG 构建中未处理", decl),
					File:        decl.GetSpan().File,
					Span:        decl.GetSpan(),
					Explanation: "编译器 Fail-Closed 守卫拦截了未知顶层声明节点。",
				})
			}
		}
	}

	return res
}

func findMethodSymbol(ib *ast.ImplBlock, methodName string, symTab *symbols.Table) *symbols.Symbol {
	nt, ok := ib.TargetType.(*ast.NamedType)
	if !ok || len(nt.Path) == 0 {
		return nil
	}
	owner := symTab.GlobalScope.Lookup(nt.Path[len(nt.Path)-1])
	if owner == nil || owner.Methods == nil {
		return nil
	}
	return owner.Methods[methodName]
}

func buildFunction(name string, fnSym *symbols.Symbol, decl *ast.FunctionDecl, view SemanticView, report *diagnostic.Report) *FuncCFG {
	f := &FuncCFG{
		Name:    name,
		FuncSym: fnSym,
		Decl:    decl,
		Locals:  make(map[symbols.SymbolID]*LocalMeta),
	}
	b := &funcBuilder{
		view:   view,
		report: report,
		fn:     f,
	}

	entry := b.newBlock("entry")
	f.Entry = entry
	exit := b.newBlock("exit")
	f.Exit = exit
	f.Blocks[exit].Terminator = Terminator{Kind: TermReturn, Span: decl.Span}

	b.curBB = entry
	rootScope := b.pushScope(decl.Span)

	if decl.Body != nil {
		b.registerFunctionParams(decl)
		b.lowerBlockStatements(decl.Body)
	}

	if !b.isTerminated(b.curBB) {
		b.popScope(rootScope, decl.Span)
		b.terminateGoto(b.curBB, exit, decl.Span)
	} else {
		b.discardTopScope()
	}

	b.finalizeEdges()
	return f
}

func (b *funcBuilder) emitInternalError(span diagnostic.Span, msg string) {
	if b.report == nil {
		return
	}
	b.report.AddDiagnostic(diagnostic.Diagnostic{
		Code:        "WON-INT-001",
		Severity:    "error",
		Message:     fmt.Sprintf("所有权 CFG 构建失败 (函数 '%s'): %s", b.fn.Name, msg),
		File:        span.File,
		Span:        span,
		Explanation: "编译器拒绝在存在未处理语法节点或缺失符号绑定的情况下认证所有权状态。",
	})
}

// emitUnsupportedError 对 Phase 2B.1 尚未提供等价安全 CFG 表示的受限语言结构发出 WON-SEM-010，避免误报为用户所有权违例。
func (b *funcBuilder) emitUnsupportedError(span diagnostic.Span, msg, explanation string) {
	if b.report == nil {
		return
	}
	b.report.AddDiagnostic(diagnostic.Diagnostic{
		Code:        "WON-SEM-010",
		Severity:    "error",
		Message:     msg,
		File:        span.File,
		Span:        span,
		Explanation: explanation,
	})
}

func (b *funcBuilder) registerFunctionParams(decl *ast.FunctionDecl) {
	symTab := b.view.GetSymbols()
	for _, p := range decl.Params {
		matched := symTab.NodeSymbols[p]
		if matched == nil {
			pName := p.Name
			if p.IsSelf {
				pName = "self"
			}
			for _, sym := range symTab.All {
				if sym.Kind == symbols.SymParam && sym.Name == pName && sym.Span == p.Span {
					matched = sym
					break
				}
			}
		}
		if matched == nil {
			b.emitInternalError(p.Span, fmt.Sprintf("函数参数 '%s' 缺少已解析符号记录", p.Name))
			continue
		}
		b.registerLocal(matched, true, true, 0)
		b.emit(Statement{
			Kind:  OpInitAssign,
			Place: b.placeForSym(matched, p.Span),
			Type:  matched.Type,
			Span:  p.Span,
		})
	}
}

func (b *funcBuilder) registerLocal(sym *symbols.Symbol, isParam bool, hasInit bool, scopeID int) {
	if sym == nil {
		return
	}
	if _, exists := b.fn.Locals[sym.ID]; exists {
		return
	}
	b.fn.LocalOrder = append(b.fn.LocalOrder, sym.ID)
	b.fn.Locals[sym.ID] = &LocalMeta{
		Sym:     sym,
		Type:    sym.Type,
		IsParam: isParam,
		IsMut:   sym.IsMut,
		HasInit: hasInit,
		ScopeID: scopeID,
		Span:    sym.Span,
	}
}

func (b *funcBuilder) newBlock(label string) BlockID {
	id := BlockID(len(b.fn.Blocks))
	b.fn.Blocks = append(b.fn.Blocks, &BasicBlock{
		ID:    id,
		Label: label,
	})
	return id
}

func (b *funcBuilder) emit(stmt Statement) {
	if b.curBB == InvalidBlock || b.isTerminated(b.curBB) {
		return
	}
	b.fn.Blocks[b.curBB].Statements = append(b.fn.Blocks[b.curBB].Statements, stmt)
}

func (b *funcBuilder) isTerminated(bb BlockID) bool {
	if bb == InvalidBlock || int(bb) >= len(b.fn.Blocks) {
		return true
	}
	return b.fn.Blocks[bb].Terminator.Kind != TermNone
}

func (b *funcBuilder) terminateGoto(from, to BlockID, span diagnostic.Span) {
	if from == InvalidBlock || b.isTerminated(from) {
		return
	}
	b.fn.Blocks[from].Terminator = Terminator{
		Kind:    TermGoto,
		Targets: []BlockID{to},
		Span:    span,
	}
}

func (b *funcBuilder) terminateSwitch(from BlockID, targets []BlockID, span diagnostic.Span) {
	if from == InvalidBlock || b.isTerminated(from) {
		return
	}
	cp := make([]BlockID, len(targets))
	copy(cp, targets)
	b.fn.Blocks[from].Terminator = Terminator{
		Kind:    TermSwitchInt,
		Targets: cp,
		Span:    span,
	}
}

func (b *funcBuilder) terminateReturn(from BlockID, span diagnostic.Span) {
	if from == InvalidBlock || b.isTerminated(from) {
		return
	}
	b.fn.Blocks[from].Terminator = Terminator{
		Kind:    TermReturn,
		Targets: []BlockID{b.fn.Exit},
		Span:    span,
	}
}

func (b *funcBuilder) pushScope(span diagnostic.Span) int {
	b.nextScope++
	sid := b.nextScope
	b.scopes = append(b.scopes, scopeFrame{id: sid})
	b.emit(Statement{Kind: OpScopeEnter, ScopeID: sid, Span: span})
	return sid
}

func (b *funcBuilder) popScope(expectedID int, span diagnostic.Span) {
	if len(b.scopes) == 0 {
		return
	}
	top := b.scopes[len(b.scopes)-1]
	b.scopes = b.scopes[:len(b.scopes)-1]
	localsCopy := make([]symbols.SymbolID, len(top.locals))
	copy(localsCopy, top.locals)
	b.emit(Statement{
		Kind:    OpScopeExit,
		ScopeID: top.id,
		Locals:  localsCopy,
		Span:    span,
	})
	_ = expectedID
}

func (b *funcBuilder) discardTopScope() {
	if len(b.scopes) > 0 {
		b.scopes = b.scopes[:len(b.scopes)-1]
	}
}

func (b *funcBuilder) emitScopeExitsDownTo(depth int, span diagnostic.Span) {
	for i := len(b.scopes) - 1; i >= depth; i-- {
		frame := b.scopes[i]
		localsCopy := make([]symbols.SymbolID, len(frame.locals))
		copy(localsCopy, frame.locals)
		b.emit(Statement{
			Kind:    OpScopeExit,
			ScopeID: frame.id,
			Locals:  localsCopy,
			Span:    span,
		})
	}
}

func (b *funcBuilder) addLocalToTopScope(symID symbols.SymbolID) {
	if len(b.scopes) > 0 {
		idx := len(b.scopes) - 1
		b.scopes[idx].locals = append(b.scopes[idx].locals, symID)
	}
}

func (b *funcBuilder) placeForSym(sym *symbols.Symbol, span diagnostic.Span) Place {
	return Place{
		RootSymID: sym.ID,
		RootName:  sym.Name,
		RootType:  sym.Type,
		ElemType:  sym.Type,
		RootMut:   sym.IsMut,
		IsParam:   sym.Kind == symbols.SymParam,
		Span:      span,
	}
}

func (b *funcBuilder) lowerBlock(blk *ast.BlockStmt) {
	if blk == nil {
		return
	}
	sid := b.pushScope(blk.Span)
	b.lowerBlockStatements(blk)
	if !b.isTerminated(b.curBB) {
		b.popScope(sid, blk.Span)
	} else {
		b.discardTopScope()
	}
}

func (b *funcBuilder) lowerBlockStatements(blk *ast.BlockStmt) {
	if blk == nil {
		return
	}
	for _, stmt := range blk.Statements {
		if b.isTerminated(b.curBB) {
			// 后续语句位于不可达死块，保留基本块节点但不连接任何可达前驱
			deadBB := b.newBlock("unreachable")
			b.curBB = deadBB
		}
		b.lowerStmt(stmt)
	}
}

func (b *funcBuilder) lowerStmt(stmt ast.Stmt) {
	if stmt == nil {
		return
	}
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		b.lowerBlock(s)

	case *ast.LetStmt:
		b.lowerLetStmt(s)

	case *ast.AssignStmt:
		b.lowerAssignStmt(s)

	case *ast.IfStmt:
		b.lowerIfStmt(s)

	case *ast.WhileStmt:
		b.lowerWhileStmt(s)

	case *ast.ForStmt:
		b.lowerForStmt(s)

	case *ast.MatchExpr:
		b.lowerMatchExpr(s)

	case *ast.ReturnStmt:
		if s.Value != nil {
			b.lowerExprValue(s.Value)
			if p, ok := b.extractPlace(s.Value); ok {
				b.emit(Statement{
					Kind:  OpReturn,
					Place: p,
					Type:  b.view.ExprType(s.Value),
					Span:  s.Span,
				})
			}
		}
		b.emitScopeExitsDownTo(0, s.Span)
		b.terminateReturn(b.curBB, s.Span)

	case *ast.BreakStmt:
		if s.Value != nil {
			b.lowerExprValue(s.Value)
		}
		if len(b.loops) == 0 {
			b.emitInternalError(s.Span, "break 语句缺少外层循环上下文")
			return
		}
		lf := &b.loops[len(b.loops)-1]
		lf.breakEdges = true
		b.emitScopeExitsDownTo(lf.scopeDepth, s.Span)
		b.terminateGoto(b.curBB, lf.breakBB, s.Span)

	case *ast.ContinueStmt:
		if len(b.loops) == 0 {
			b.emitInternalError(s.Span, "continue 语句缺少外层循环上下文")
			return
		}
		lf := &b.loops[len(b.loops)-1]
		lf.continueHit = true
		b.emitScopeExitsDownTo(lf.scopeDepth, s.Span)
		b.terminateGoto(b.curBB, lf.continueBB, s.Span)

	case *ast.ExprStmt:
		b.lowerExprValue(s.Expr)

	case *ast.ScopeExpr, *ast.SpawnExpr:
		b.emitUnsupportedError(
			s.GetSpan(),
			"并发控制流原语 (scope/spawn) 尚未在当前所有权阶段启用",
			"结构化并发所有权校验属于后续独立里程碑。",
		)

	default:
		b.emitInternalError(stmt.GetSpan(), fmt.Sprintf("Encountered unhandled statement node %T in ownership CFG", stmt))
	}
}

func (b *funcBuilder) lowerLetStmt(s *ast.LetStmt) {
	curScopeID := 0
	if len(b.scopes) > 0 {
		curScopeID = b.scopes[len(b.scopes)-1].id
	}

	if s.Init == nil {
		sym := b.lookupDeclSym(s)
		if sym == nil {
			b.emitInternalError(s.Span, fmt.Sprintf("未初始化变量绑定 '%s' 缺少符号解析记录", s.Name))
			return
		}
		b.registerLocal(sym, false, false, curScopeID)
		b.addLocalToTopScope(sym.ID)
		b.emit(Statement{
			Kind:  OpUninitDecl,
			Place: b.placeForSym(sym, s.Span),
			Type:  sym.Type,
			Span:  s.Span,
		})
		return
	}

	b.lowerExprValue(s.Init)

	if s.Pattern != nil {
		b.registerPatternLocals(s.Pattern, true, curScopeID, s.Span)
	} else if s.Name != "" && s.Name != "_" {
		sym := b.lookupDeclSym(s)
		if sym == nil {
			b.emitInternalError(s.Span, fmt.Sprintf("变量绑定 '%s' 缺少符号解析记录", s.Name))
			return
		}
		b.registerLocal(sym, false, true, curScopeID)
		b.addLocalToTopScope(sym.ID)
		b.emit(Statement{
			Kind:  OpInitAssign,
			Place: b.placeForSym(sym, s.Span),
			Type:  sym.Type,
			Span:  s.Span,
		})
	}
}

func (b *funcBuilder) lookupDeclSym(s *ast.LetStmt) *symbols.Symbol {
	symTab := b.view.GetSymbols()
	if sym := symTab.NodeSymbols[s]; sym != nil {
		return sym
	}
	if s.Pattern != nil {
		if sym := symTab.NodeSymbols[s.Pattern]; sym != nil {
			return sym
		}
	}
	return nil
}

func (b *funcBuilder) registerPatternLocals(pat ast.Pattern, initNow bool, curScopeID int, span diagnostic.Span) {
	if pat == nil {
		return
	}
	symTab := b.view.GetSymbols()
	switch p := pat.(type) {
	case *ast.WildcardPattern, *ast.LiteralPattern:
		return

	case *ast.IdentPattern:
		if b.view.VariantRef(p) != nil {
			return
		}
		sym := symTab.NodeSymbols[p]
		if sym == nil {
			b.emitInternalError(p.Span, fmt.Sprintf("模式绑定标识符 '%s' 缺少 SymbolID 映射", p.Name))
			return
		}
		b.registerLocal(sym, false, initNow, curScopeID)
		b.addLocalToTopScope(sym.ID)
		op := OpInitAssign
		if !initNow {
			op = OpUninitDecl
		}
		b.emit(Statement{
			Kind:  op,
			Place: b.placeForSym(sym, p.Span),
			Type:  sym.Type,
			Span:  p.Span,
		})

	case *ast.TuplePattern:
		for _, sub := range p.Elems {
			b.registerPatternLocals(sub, initNow, curScopeID, span)
		}

	case *ast.StructPattern:
		for _, fp := range p.Fields {
			if fp.Pattern != nil {
				b.registerPatternLocals(fp.Pattern, initNow, curScopeID, fp.Span)
			} else {
				sym := symTab.NodeSymbols[fp]
				if sym == nil {
					b.emitInternalError(fp.Span, fmt.Sprintf("结构体简写模式字段 '%s' 缺少 SymbolID 映射", fp.Name))
					return
				}
				b.registerLocal(sym, false, initNow, curScopeID)
				b.addLocalToTopScope(sym.ID)
				op := OpInitAssign
				if !initNow {
					op = OpUninitDecl
				}
				b.emit(Statement{
					Kind:  op,
					Place: b.placeForSym(sym, fp.Span),
					Type:  sym.Type,
					Span:  fp.Span,
				})
			}
		}

	case *ast.EnumPattern:
		for _, arg := range p.Args {
			b.registerPatternLocals(arg, initNow, curScopeID, span)
		}

	case *ast.SlicePattern:
		for _, elem := range p.Elems {
			b.registerPatternLocals(elem, initNow, curScopeID, span)
		}

	default:
		b.emitInternalError(pat.GetSpan(), fmt.Sprintf("模式声明注册遇到未受支持的模式类型 %T", pat))
	}
}

func (b *funcBuilder) lowerAssignStmt(s *ast.AssignStmt) {
	lhsExpr := s.TargetExpr
	if lhsExpr == nil && s.Target != "" {
		lhsExpr = &ast.Identifier{Name: s.Target, Span: s.Span}
	}

	lhsPlace, hasPlace := b.extractPlace(lhsExpr)
	if !hasPlace {
		b.emitInternalError(s.Span, fmt.Sprintf("赋值目标表达式 (%T) 无法解析为所有权存储位置", lhsExpr))
		return
	}
	isCompound := s.Op != "" && s.Op != "="

	b.lowerAssignTargetSubExprs(lhsExpr, isCompound, lhsPlace, s.Span)

	if isCompound {
		b.emit(Statement{
			Kind:  OpRead,
			Place: lhsPlace,
			Type:  lhsPlace.ElemType,
			Span:  lhsExpr.GetSpan(),
		})
	}

	b.lowerExprValue(s.Value)

	b.emit(Statement{
		Kind:  OpAssign,
		Place: lhsPlace,
		Type:  lhsPlace.ElemType,
		Span:  s.Span,
	})
}

func (b *funcBuilder) lowerAssignTargetSubExprs(lhs ast.Expr, isCompound bool, lhsPlace Place, span diagnostic.Span) {
	switch e := lhs.(type) {
	case *ast.Identifier:
		// 根变量本身将由右侧求值后的 OpAssign 写入

	case *ast.IndexExpr:
		if objPlace, ok := b.extractPlace(e.Object); ok {
			b.emit(Statement{
				Kind:  OpRead,
				Place: objPlace,
				Type:  objPlace.ElemType,
				Span:  e.Object.GetSpan(),
			})
		} else {
			b.lowerExprValue(e.Object)
		}
		b.lowerExprValue(e.Index)

	case *ast.FieldAccessExpr:
		b.lowerAssignTargetSubExprs(e.Object, isCompound, lhsPlace, span)

	case *ast.UnaryExpr:
		if e.Op != "*" {
			b.emitInternalError(e.Span, fmt.Sprintf("赋值左值遇到非解引用一元操作符 %q", e.Op))
			return
		}
		if ptrPlace, ok := b.extractPlace(e.Expr); ok {
			b.emit(Statement{
				Kind:  OpRead,
				Place: ptrPlace,
				Type:  ptrPlace.ElemType,
				Span:  e.Expr.GetSpan(),
			})
		} else {
			b.lowerExprValue(e.Expr)
		}

	default:
		b.emitInternalError(lhs.GetSpan(), fmt.Sprintf("赋值左值包含未处理节点 %T", lhs))
	}
}

func (b *funcBuilder) lowerIfStmt(s *ast.IfStmt) {
	b.lowerExprValue(s.Cond)
	condBB := b.curBB
	thenBB := b.newBlock("if.then")
	elseBB := b.newBlock("if.else")
	mergeBB := b.newBlock("if.merge")

	b.terminateSwitch(condBB, []BlockID{thenBB, elseBB}, s.Span)

	b.curBB = thenBB
	b.lowerBlock(s.Then)
	thenEnd := b.curBB
	thenClosed := b.isTerminated(thenEnd)
	if !thenClosed {
		b.terminateGoto(thenEnd, mergeBB, s.Span)
	}

	b.curBB = elseBB
	if s.Else != nil {
		b.lowerStmt(s.Else)
	}
	elseEnd := b.curBB
	elseClosed := b.isTerminated(elseEnd)
	if !elseClosed {
		b.terminateGoto(elseEnd, mergeBB, s.Span)
	}

	if thenClosed && elseClosed {
		b.fn.Blocks[mergeBB].Terminator = Terminator{Kind: TermUnreachable, Span: s.Span}
	}
	b.curBB = mergeBB
}

func (b *funcBuilder) lowerWhileStmt(s *ast.WhileStmt) {
	headerBB := b.newBlock("while.cond")
	bodyBB := b.newBlock("while.body")
	exitBB := b.newBlock("while.exit")

	b.terminateGoto(b.curBB, headerBB, s.Span)

	b.curBB = headerBB
	b.lowerExprValue(s.Cond)
	b.terminateSwitch(headerBB, []BlockID{bodyBB, exitBB}, s.Cond.GetSpan())

	b.loops = append(b.loops, loopFrame{
		breakBB:    exitBB,
		continueBB: headerBB,
		scopeDepth: len(b.scopes),
	})

	b.curBB = bodyBB
	b.lowerBlock(s.Body)
	if !b.isTerminated(b.curBB) {
		b.terminateGoto(b.curBB, headerBB, s.Span)
	}

	b.loops = b.loops[:len(b.loops)-1]
	b.curBB = exitBB
}

func (b *funcBuilder) lowerForStmt(s *ast.ForStmt) {
	b.lowerExprValue(s.Iter)
	headerBB := b.newBlock("for.header")
	bodyBB := b.newBlock("for.body")
	exitBB := b.newBlock("for.exit")

	b.terminateGoto(b.curBB, headerBB, s.Span)
	b.terminateSwitch(headerBB, []BlockID{bodyBB, exitBB}, s.Span)

	b.loops = append(b.loops, loopFrame{
		breakBB:    exitBB,
		continueBB: headerBB,
		scopeDepth: len(b.scopes),
	})

	b.curBB = bodyBB
	sid := b.pushScope(s.Span)
	if s.Pattern != nil {
		b.registerPatternLocals(s.Pattern, true, sid, s.Span)
	}
	b.lowerBlockStatements(s.Body)
	if !b.isTerminated(b.curBB) {
		b.popScope(sid, s.Span)
		b.terminateGoto(b.curBB, headerBB, s.Span)
	} else {
		b.discardTopScope()
	}

	b.loops = b.loops[:len(b.loops)-1]
	b.curBB = exitBB
}

// lowerMatchExpr 为 match 表达式的每个分支单独降低所有权语义，精确区分引用模式分支与按值移动分支，并忠实表达守卫落空流转。
func (b *funcBuilder) lowerMatchExpr(m *ast.MatchExpr) {
	subjPlace, hasSubjPlace := b.extractPlace(m.Subject)
	if hasSubjPlace {
		// 在分发点仅检查标签判别式可读性，绝不在进入具体分支前提前吞并整个主体
		b.lowerPlacePrereqs(m.Subject, subjPlace)
		b.emit(Statement{
			Kind:  OpRead,
			Place: subjPlace,
			Type:  b.view.ExprType(m.Subject),
			Span:  m.Subject.GetSpan(),
		})
	} else {
		b.lowerExprValue(m.Subject)
	}

	switchBB := b.curBB
	mergeBB := b.newBlock("match.merge")

	if len(m.Arms) == 0 {
		b.terminateGoto(switchBB, mergeBB, m.Span)
		b.curBB = mergeBB
		return
	}

	armTests := make([]BlockID, len(m.Arms))
	for i := range m.Arms {
		armTests[i] = b.newBlock(fmt.Sprintf("match.arm%d.pat", i))
	}
	b.terminateSwitch(switchBB, armTests, m.Span)

	subjType := b.view.ExprType(m.Subject)
	if subjType == types.Invalid && hasSubjPlace {
		subjType = subjPlace.ElemType
	}

	allDiverged := true
	for i, arm := range m.Arms {
		b.curBB = armTests[i]
		sid := b.pushScope(arm.Span)

		// 保守拒绝带守卫 'if' 的非 Copy 按值消费模式，防止守卫失败前提前提交不可逆的 OpMove 污染后续分支状态。
		if arm.Guard != nil && b.patternConsumesNonCopy(arm.Pattern) {
			b.emitUnsupportedError(
				arm.Pattern.GetSpan(),
				"Phase 2B.1.2 暂不支持在带守卫 'if' 条件的 match 分支中按值移出非 Copy 绑定",
				"当前尚未建立独立的守卫期临时借用语义；若提前提交 OpMove，守卫失败落空至后续分支时将产生错误的移动状态。请改用 'ref' 借用绑定或将条件判断移入分支体内部。",
			)
			b.popScope(sid, arm.Span)
			b.terminateGoto(b.curBB, mergeBB, arm.Span)
			allDiverged = false
			continue
		}

		if arm.Pattern != nil {
			b.applyArmPatternOwnership(arm.Pattern, subjPlace, hasSubjPlace, subjType, sid)
		}

		if arm.Guard != nil {
			b.lowerExprValue(arm.Guard)
			guardBB := b.curBB
			armBodyBB := b.newBlock(fmt.Sprintf("match.arm%d.body", i))
			guardFailBB := b.newBlock(fmt.Sprintf("match.arm%d.gfail", i))
			b.terminateSwitch(guardBB, []BlockID{armBodyBB, guardFailBB}, arm.Guard.GetSpan())

			// 守卫失败路径先退出当前分支绑定作用域，再汇入后续候选分支或退出合并点
			b.curBB = guardFailBB
			b.popScopeKeepFrame(arm.Guard.GetSpan())
			if i+1 < len(m.Arms) {
				b.terminateGoto(guardFailBB, armTests[i+1], arm.Guard.GetSpan())
			} else {
				b.terminateGoto(guardFailBB, mergeBB, arm.Guard.GetSpan())
				allDiverged = false
			}

			b.curBB = armBodyBB
		}

		b.lowerBlockStatements(arm.Body)
		if !b.isTerminated(b.curBB) {
			allDiverged = false
			b.popScope(sid, arm.Span)
			b.terminateGoto(b.curBB, mergeBB, arm.Span)
		} else {
			b.discardTopScope()
		}
	}

	if allDiverged {
		b.fn.Blocks[mergeBB].Terminator = Terminator{Kind: TermUnreachable, Span: m.Span}
	}
	b.curBB = mergeBB
}

func (b *funcBuilder) popScopeKeepFrame(span diagnostic.Span) {
	if len(b.scopes) == 0 {
		return
	}
	top := b.scopes[len(b.scopes)-1]
	localsCopy := make([]symbols.SymbolID, len(top.locals))
	copy(localsCopy, top.locals)
	b.emit(Statement{
		Kind:    OpScopeExit,
		ScopeID: top.id,
		Locals:  localsCopy,
		Span:    span,
	})
}

// applyArmPatternOwnership 按单个 match 分支的实际模式种类生成独立的所有权/移动/借用事件与绑定初始化。
func (b *funcBuilder) applyArmPatternOwnership(pat ast.Pattern, place Place, hasPlace bool, patType types.TypeID, scopeID int) {
	if pat == nil {
		return
	}
	symTab := b.view.GetSymbols()
	tctx := b.view.GetTypes()
	baseType := patType
	isThroughRef := false
	if baseType != types.Invalid && tctx.Info(baseType).Kind == types.KindRef {
		isThroughRef = true
		baseType = tctx.Info(baseType).Elem
	}

	switch p := pat.(type) {
	case *ast.WildcardPattern, *ast.LiteralPattern:
		return

	case *ast.IdentPattern:
		if b.view.VariantRef(p) != nil {
			// 单元枚举变体仅检查判别式，不移出主体
			return
		}
		sym := symTab.NodeSymbols[p]
		if sym == nil {
			b.emitInternalError(p.Span, fmt.Sprintf("match 分支绑定 '%s' 缺少 SymbolID", p.Name))
			return
		}
		if hasPlace {
			if p.IsRef {
				op := OpBorrowShared
				if p.IsMut {
					op = OpBorrowMut
				}
				b.emit(Statement{Kind: OpRead, Place: place, Type: place.ElemType, Span: p.Span})
				b.emit(Statement{Kind: op, Place: place, Type: sym.Type, Span: p.Span})
			} else if sym.Type != types.Invalid && !tctx.IsCopy(sym.Type) {
				if isThroughRef || place.HasDeref() || place.HasIndex() {
					// 通过引用/解引用匹配出的非 Copy 值按值绑定与直接解引用移出一致，触发 WON-MEM-008 防线
					pEmit := place
					if isThroughRef && !place.HasDeref() {
						pEmit = place.AppendProj(Projection{Kind: ProjDeref, ElemType: sym.Type}, sym.Type, p.Span)
					}
					b.emit(Statement{Kind: OpMove, Place: pEmit, Type: sym.Type, Span: p.Span})
				} else {
					b.emit(Statement{Kind: OpMove, Place: place, Type: sym.Type, Span: p.Span})
				}
			} else {
				// Copy 类型按值绑定同样记录显式 OpCopy 源读取，确保每个局部变量均有对应的源所有权操作且不产生伪 Move
				b.emit(Statement{Kind: OpCopy, Place: place, Type: sym.Type, Span: p.Span})
			}
		}
		b.registerLocal(sym, false, true, scopeID)
		b.addLocalToTopScope(sym.ID)
		b.emit(Statement{
			Kind:  OpInitAssign,
			Place: b.placeForSym(sym, p.Span),
			Type:  sym.Type,
			Span:  p.Span,
		})

	case *ast.StructPattern:
		// 区分 1) 枚举结构体变体 vs 2) 直接结构体字段部分匹配
		if b.view.VariantRef(p) != nil {
			b.lowerOpaqueAggregatePattern(p, place, hasPlace, isThroughRef, scopeID)
			return
		}

		// 直接结构体解构拥有精确的 ProjField 投影标识：逐字段处理借用、Copy 读取与按字段部分移动
		for _, fp := range p.Fields {
			fieldPlace := place
			fType := findStructFieldType(baseType, fp.Name, symTab, tctx)
			if hasPlace {
				if isThroughRef && !place.HasDeref() {
					fieldPlace = place.AppendProj(Projection{Kind: ProjDeref, ElemType: baseType}, baseType, fp.Span)
				}
				fieldPlace = fieldPlace.AppendField(fp.Name, -1, fType, fp.Span)
			}

			if fp.Pattern == nil {
				sym := symTab.NodeSymbols[fp]
				if sym == nil {
					b.emitInternalError(fp.Span, fmt.Sprintf("结构体解构简写字段 '%s' 缺少 SymbolID", fp.Name))
					continue
				}
				if hasPlace {
					if sym.Type != types.Invalid && !tctx.IsCopy(sym.Type) {
						b.emit(Statement{Kind: OpMove, Place: fieldPlace, Type: sym.Type, Span: fp.Span})
					} else {
						b.emit(Statement{Kind: OpCopy, Place: fieldPlace, Type: sym.Type, Span: fp.Span})
					}
				}
				b.registerLocal(sym, false, true, scopeID)
				b.addLocalToTopScope(sym.ID)
				b.emit(Statement{
					Kind:  OpInitAssign,
					Place: b.placeForSym(sym, fp.Span),
					Type:  sym.Type,
					Span:  fp.Span,
				})
				continue
			}

			b.applyArmPatternOwnership(fp.Pattern, fieldPlace, hasPlace, fType, scopeID)
		}

	case *ast.EnumPattern, *ast.TuplePattern, *ast.SlicePattern:
		// 尚未实现 ProjDowncast / ProjTupleField 的聚合解构模式：保持借用与移动事件完整，或对不支持混合体执行保守拒绝
		b.lowerOpaqueAggregatePattern(p, place, hasPlace, isThroughRef, scopeID)

	default:
		b.emitInternalError(pat.GetSpan(), fmt.Sprintf("match 模式匹配遇到未处理的模式节点 %T", pat))
	}
}

func (b *funcBuilder) patternConsumesNonCopy(pat ast.Pattern) bool {
	if pat == nil {
		return false
	}
	symTab := b.view.GetSymbols()
	tctx := b.view.GetTypes()
	switch p := pat.(type) {
	case *ast.WildcardPattern, *ast.LiteralPattern:
		return false
	case *ast.IdentPattern:
		if p.IsRef || b.view.VariantRef(p) != nil {
			return false
		}
		sym := symTab.NodeSymbols[p]
		return sym != nil && sym.Type != types.Invalid && !tctx.IsCopy(sym.Type)
	case *ast.TuplePattern:
		for _, el := range p.Elems {
			if b.patternConsumesNonCopy(el) {
				return true
			}
		}
		return false
	case *ast.EnumPattern:
		for _, arg := range p.Args {
			if b.patternConsumesNonCopy(arg) {
				return true
			}
		}
		return false
	case *ast.StructPattern:
		for _, fp := range p.Fields {
			if fp.Pattern != nil {
				if b.patternConsumesNonCopy(fp.Pattern) {
					return true
				}
			} else if sym := symTab.NodeSymbols[fp]; sym != nil && sym.Type != types.Invalid && !tctx.IsCopy(sym.Type) {
				return true
			}
		}
		return false
	case *ast.SlicePattern:
		for _, el := range p.Elems {
			if b.patternConsumesNonCopy(el) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (b *funcBuilder) lowerOpaqueAggregatePattern(pat ast.Pattern, place Place, hasPlace bool, isThroughRef bool, scopeID int) {
	tctx := b.view.GetTypes()
	symTab := b.view.GetSymbols()

	var bindings []*symbols.Symbol
	var borrowOps []Statement
	hasRef := false
	hasCopyBind := false
	hasNonCopyMove := false

	var walk func(node ast.Pattern)
	walk = func(node ast.Pattern) {
		if node == nil {
			return
		}
		switch p := node.(type) {
		case *ast.WildcardPattern, *ast.LiteralPattern:
			return
		case *ast.IdentPattern:
			if b.view.VariantRef(p) != nil {
				return
			}
			sym := symTab.NodeSymbols[p]
			if sym == nil {
				b.emitInternalError(p.Span, fmt.Sprintf("嵌套模式绑定 '%s' 缺少 SymbolID", p.Name))
				return
			}
			bindings = append(bindings, sym)
			if p.IsRef {
				hasRef = true
				op := OpBorrowShared
				if p.IsMut {
					op = OpBorrowMut
				}
				if hasPlace {
					borrowOps = append(borrowOps, Statement{Kind: op, Place: place, Type: sym.Type, Span: p.Span})
				}
			} else if sym.Type != types.Invalid && !tctx.IsCopy(sym.Type) {
				hasNonCopyMove = true
			} else {
				hasCopyBind = true
			}
		case *ast.TuplePattern:
			for _, el := range p.Elems {
				walk(el)
			}
		case *ast.EnumPattern:
			for _, arg := range p.Args {
				walk(arg)
			}
		case *ast.StructPattern:
			for _, fp := range p.Fields {
				if fp.Pattern != nil {
					walk(fp.Pattern)
				} else if sym := symTab.NodeSymbols[fp]; sym != nil {
					bindings = append(bindings, sym)
					if sym.Type != types.Invalid && !tctx.IsCopy(sym.Type) {
						hasNonCopyMove = true
					} else {
						hasCopyBind = true
					}
				} else {
					b.emitInternalError(fp.Span, fmt.Sprintf("嵌套结构体简写字段 '%s' 缺少 SymbolID", fp.Name))
				}
			}
		case *ast.SlicePattern:
			for _, el := range p.Elems {
				walk(el)
			}
		default:
			b.emitInternalError(node.GetSpan(), fmt.Sprintf("聚合模式内部包含未处理的节点 %T", node))
		}
	}

	walk(pat)

	// 无子元素投影的非直接聚合模式若同时混合 ref 借用与非 Copy 移动，
	// 会在同一根位置上既 Borrow 又 Move 导致语义矛盾，故保守发出 WON-SEM-010 拒绝认证，绝不静默丢弃借用事件。
	if hasRef && hasNonCopyMove {
		b.emitUnsupportedError(
			pat.GetSpan(),
			"Phase 2B.1.2 暂不支持在枚举/元组/切片聚合模式中同时混合 'ref' 借用与非 Copy 按值移动绑定",
			"当前 CFG 仅对直接结构体模式提供字段级 ProjField 拆分；请统一改用 'ref' 借用所有字段，或拆分为直接结构体字段匹配。",
		)
		return
	}

	if hasPlace {
		if hasNonCopyMove {
			movePlace := place
			if isThroughRef && !place.HasDeref() {
				movePlace = place.AppendProj(Projection{Kind: ProjDeref, ElemType: place.ElemType}, place.ElemType, pat.GetSpan())
			}
			b.emit(Statement{Kind: OpMove, Place: movePlace, Type: place.ElemType, Span: pat.GetSpan()})
		} else {
			if hasRef {
				b.emit(Statement{Kind: OpRead, Place: place, Type: place.ElemType, Span: pat.GetSpan()})
				for _, bop := range borrowOps {
					b.emit(bop)
				}
			}
			if hasCopyBind {
				b.emit(Statement{Kind: OpCopy, Place: place, Type: place.ElemType, Span: pat.GetSpan()})
			}
		}
	}

	for _, sym := range bindings {
		b.registerLocal(sym, false, true, scopeID)
		b.addLocalToTopScope(sym.ID)
		b.emit(Statement{
			Kind:  OpInitAssign,
			Place: b.placeForSym(sym, sym.Span),
			Type:  sym.Type,
			Span:  sym.Span,
		})
	}
}

func findStructFieldType(structType types.TypeID, fieldName string, symTab *symbols.Table, tctx *types.Context) types.TypeID {
	if structType == types.Invalid || tctx == nil || symTab == nil {
		return types.Invalid
	}
	info := tctx.Info(structType)
	if info.Kind != types.KindStruct {
		return types.Invalid
	}
	sym := symTab.ByID[symbols.SymbolID(info.SymbolID)]
	if sym == nil || sym.FieldMap == nil {
		return types.Invalid
	}
	fi := sym.FieldMap[fieldName]
	if fi == nil {
		return types.Invalid
	}
	if len(sym.GenericParamIDs) > 0 && len(sym.GenericParamIDs) == len(info.GenericArgs) {
		subst := make(map[uint64]types.TypeID, len(info.GenericArgs))
		for i, gid := range sym.GenericParamIDs {
			subst[uint64(gid)] = info.GenericArgs[i]
		}
		return tctx.Substitute(fi.Type, subst)
	}
	return fi.Type
}

// lowerExprValue 按所有权与求值顺序降低任意右值表达式，并在末尾对任何未知 AST 表达式触发 WON-INT-001。
func (b *funcBuilder) lowerExprValue(expr ast.Expr) {
	if expr == nil {
		return
	}

	if place, ok := b.extractPlace(expr); ok {
		b.lowerPlaceValueAccess(expr, place)
		return
	}

	switch e := expr.(type) {
	case *ast.IntegerLiteral, *ast.FloatLiteral, *ast.BoolLiteral, *ast.CharLiteral, *ast.ByteLiteral, *ast.StringLiteral, *ast.PathExpr:
		return

	case *ast.InterpolatedStringExpr:
		for _, part := range e.Parts {
			if part.IsExpr && part.Expr != nil {
				b.lowerExprValue(part.Expr)
			}
		}

	case *ast.ArrayLiteral:
		for _, el := range e.Elems {
			b.lowerExprValue(el)
		}

	case *ast.ArrayRepeatLiteral:
		b.lowerExprValue(e.Elem)

	case *ast.TupleLiteral:
		for _, el := range e.Elems {
			b.lowerExprValue(el)
		}

	case *ast.StructLiteral:
		for _, f := range e.Fields {
			if f.Value != nil {
				b.lowerExprValue(f.Value)
			}
		}

	case *ast.Identifier:
		// 非局部/参数标识符（如枚举单元构造器 / 函数名），检查符号确实已解析
		if sym := b.view.GetSymbols().NodeSymbols[e]; sym == nil {
			b.emitInternalError(e.Span, fmt.Sprintf("标识符表达式 '%s' 缺少已解析符号记录", e.Name))
		}

	case *ast.FieldAccessExpr:
		b.lowerExprValue(e.Object)

	case *ast.IndexExpr:
		b.lowerExprValue(e.Object)
		if rng, isRng := e.Index.(*ast.RangeExpr); isRng {
			if rng.Start != nil {
				b.lowerExprValue(rng.Start)
			}
			if rng.End != nil {
				b.lowerExprValue(rng.End)
			}
		} else {
			b.lowerExprValue(e.Index)
		}

	case *ast.RangeExpr:
		b.lowerExprValue(e.Start)
		b.lowerExprValue(e.End)

	case *ast.CastExpr:
		b.lowerExprValue(e.Expr)

	case *ast.UnaryExpr:
		b.lowerExprValue(e.Expr)

	case *ast.BorrowExpr:
		targetPlace, ok := b.extractPlace(e.Expr)
		if ok {
			b.lowerPlaceAddressSubExprs(e.Expr)
			opKind := OpBorrowShared
			if e.IsMut {
				opKind = OpBorrowMut
			}
			b.emit(Statement{
				Kind:  OpRead,
				Place: targetPlace,
				Type:  targetPlace.ElemType,
				Span:  e.Span,
			})
			b.emit(Statement{
				Kind:  opKind,
				Place: targetPlace,
				Type:  b.view.ExprType(e),
				Span:  e.Span,
			})
		} else {
			b.lowerExprValue(e.Expr)
		}

	case *ast.BinaryExpr:
		if e.Op == "and" || e.Op == "or" {
			b.lowerExprValue(e.Left)
			condBB := b.curBB
			rhsBB := b.newBlock("logic.rhs")
			mergeBB := b.newBlock("logic.merge")
			b.terminateSwitch(condBB, []BlockID{rhsBB, mergeBB}, e.Span)
			b.curBB = rhsBB
			b.lowerExprValue(e.Right)
			if !b.isTerminated(b.curBB) {
				b.terminateGoto(b.curBB, mergeBB, e.Span)
			}
			b.curBB = mergeBB
			return
		}
		b.lowerExprValue(e.Left)
		b.lowerExprValue(e.Right)

	case *ast.TryExpr:
		b.lowerExprValue(e.Expr)
		tryBB := b.curBB
		okBB := b.newBlock("try.ok")
		errBB := b.newBlock("try.err")
		b.terminateSwitch(tryBB, []BlockID{okBB, errBB}, e.Span)
		b.curBB = errBB
		b.emitScopeExitsDownTo(0, e.Span)
		b.terminateReturn(errBB, e.Span)
		b.curBB = okBB

	case *ast.CallExpr:
		for _, arg := range e.Args {
			b.lowerExprValue(arg)
		}

	case *ast.NamespacedCallExpr:
		for _, arg := range e.Args {
			b.lowerExprValue(arg)
		}

	case *ast.MethodCallExpr:
		calSym := b.view.CallTarget(e)
		if calSym != nil && calSym.HasSelf && !calSym.SelfIsRef {
			b.lowerExprValue(e.Receiver)
		} else if p, ok := b.extractPlace(e.Receiver); ok {
			b.lowerPlaceAddressSubExprs(e.Receiver)
			b.emit(Statement{
				Kind:  OpRead,
				Place: p,
				Type:  p.ElemType,
				Span:  e.Receiver.GetSpan(),
			})
			opKind := OpBorrowShared
			if (calSym != nil && calSym.SelfIsMut) || e.Method == "push_str" || e.Method == "as_mut_ptr" {
				opKind = OpBorrowMut
			}
			b.emit(Statement{
				Kind:  opKind,
				Place: p,
				Type:  p.ElemType,
				Span:  e.Span,
			})
		} else {
			b.lowerExprValue(e.Receiver)
		}
		for _, arg := range e.Args {
			b.lowerExprValue(arg)
		}

	case *ast.UnsafeExpr:
		b.lowerBlock(e.Block)

	case *ast.IfStmt:
		b.lowerIfStmt(e)

	case *ast.WhileStmt:
		b.lowerWhileStmt(e)

	case *ast.ForStmt:
		b.lowerForStmt(e)

	case *ast.MatchExpr:
		b.lowerMatchExpr(e)

	case *ast.ScopeExpr, *ast.SpawnExpr:
		b.emitUnsupportedError(
			expr.GetSpan(),
			"结构化并发原语 (scope/spawn) 尚未在当前所有权阶段启用",
			"结构化并发所有权校验属于后续独立里程碑。",
		)

	default:
		b.emitInternalError(expr.GetSpan(), fmt.Sprintf("Encountered unhandled expression node %T in ownership CFG", expr))
	}
}

func (b *funcBuilder) lowerPlaceAddressSubExprs(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.Identifier:
		return
	case *ast.FieldAccessExpr:
		b.lowerPlaceAddressSubExprs(e.Object)
	case *ast.IndexExpr:
		b.lowerPlaceAddressSubExprs(e.Object)
		b.lowerExprValue(e.Index)
	case *ast.UnaryExpr:
		if e.Op == "*" {
			b.lowerPlaceAddressSubExprs(e.Expr)
		}
	default:
		b.emitInternalError(expr.GetSpan(), fmt.Sprintf("取址路径包含未处理节点 %T", expr))
	}
}

func (b *funcBuilder) lowerPlaceValueAccess(expr ast.Expr, place Place) {
	b.lowerPlacePrereqs(expr, place)

	exprType := b.view.ExprType(expr)
	if exprType == types.Invalid {
		exprType = place.ElemType
	}

	if exprType != types.Invalid && b.view.GetTypes().IsCopy(exprType) {
		b.emit(Statement{
			Kind:  OpCopy,
			Place: place,
			Type:  exprType,
			Span:  expr.GetSpan(),
		})
		return
	}

	b.emit(Statement{
		Kind:  OpMove,
		Place: place,
		Type:  exprType,
		Span:  expr.GetSpan(),
	})
}

func (b *funcBuilder) lowerPlacePrereqs(expr ast.Expr, place Place) {
	switch e := expr.(type) {
	case *ast.IndexExpr:
		b.lowerPlacePrereqs(e.Object, place)
		b.lowerExprValue(e.Index)
	case *ast.FieldAccessExpr:
		b.lowerPlacePrereqs(e.Object, place)
	case *ast.UnaryExpr:
		if e.Op == "*" {
			if ptrPlace, ok := b.extractPlace(e.Expr); ok {
				b.emit(Statement{
					Kind:  OpRead,
					Place: ptrPlace,
					Type:  ptrPlace.ElemType,
					Span:  e.Expr.GetSpan(),
				})
			} else {
				b.lowerExprValue(e.Expr)
			}
		}
	}
}

// extractPlace 将左值/位置表达式递归解析为静态 Place。
func (b *funcBuilder) extractPlace(expr ast.Expr) (Place, bool) {
	if expr == nil {
		return Place{}, false
	}
	symTab := b.view.GetSymbols()
	tctx := b.view.GetTypes()

	switch e := expr.(type) {
	case *ast.Identifier:
		sym := symTab.NodeSymbols[e]
		if sym == nil || (sym.Kind != symbols.SymVar && sym.Kind != symbols.SymParam) {
			return Place{}, false
		}
		return b.placeForSym(sym, e.Span), true

	case *ast.FieldAccessExpr:
		basePlace, ok := b.extractPlace(e.Object)
		if !ok {
			return Place{}, false
		}
		baseType := b.view.ExprType(e.Object)
		if baseType == types.Invalid {
			baseType = basePlace.ElemType
		}
		baseInfo := tctx.Info(baseType)
		if baseInfo.Kind == types.KindRef || baseInfo.Kind == types.KindRawPtr {
			basePlace = basePlace.AppendProj(Projection{
				Kind:     ProjDeref,
				ElemType: baseInfo.Elem,
			}, baseInfo.Elem, e.Span)
		}

		var elemType types.TypeID
		if fi := b.view.FieldRef(e); fi != nil {
			elemType = fi.Type
		} else {
			elemType = b.view.ExprType(e)
		}
		return basePlace.AppendField(e.Field, -1, elemType, e.Span), true

	case *ast.UnaryExpr:
		if e.Op != "*" {
			return Place{}, false
		}
		ptrPlace, ok := b.extractPlace(e.Expr)
		if !ok {
			return Place{}, false
		}
		elemType := b.view.ExprType(e)
		return ptrPlace.AppendProj(Projection{
			Kind:     ProjDeref,
			ElemType: elemType,
		}, elemType, e.Span), true

	case *ast.IndexExpr:
		if _, isRng := e.Index.(*ast.RangeExpr); isRng {
			return Place{}, false
		}
		arrPlace, ok := b.extractPlace(e.Object)
		if !ok {
			return Place{}, false
		}
		elemType := b.view.ExprType(e)
		if lit, isLit := e.Index.(*ast.IntegerLiteral); isLit && lit.Valid && lit.Value != nil && lit.Value.IsInt64() {
			return arrPlace.AppendProj(Projection{
				Kind:       ProjConstIdx,
				ConstIndex: lit.Value.Int64(),
				ElemType:   elemType,
			}, elemType, e.Span), true
		}
		var idxSym symbols.SymbolID
		if id, isIdent := e.Index.(*ast.Identifier); isIdent {
			if sym := symTab.NodeSymbols[id]; sym != nil {
				idxSym = sym.ID
			}
		}
		return arrPlace.AppendProj(Projection{
			Kind:     ProjIndex,
			IndexSym: idxSym,
			ElemType: elemType,
		}, elemType, e.Span), true
	}

	return Place{}, false
}

func (b *funcBuilder) finalizeEdges() {
	for _, blk := range b.fn.Blocks {
		blk.Successors = nil
		blk.Predecessors = nil
	}
	for _, blk := range b.fn.Blocks {
		seen := make(map[BlockID]bool, len(blk.Terminator.Targets))
		for _, tgt := range blk.Terminator.Targets {
			if tgt >= 0 && int(tgt) < len(b.fn.Blocks) && !seen[tgt] {
				seen[tgt] = true
				blk.Successors = append(blk.Successors, tgt)
				b.fn.Blocks[tgt].Predecessors = append(b.fn.Blocks[tgt].Predecessors, blk.ID)
			}
		}
	}
}

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
	view       SemanticView
	report     *diagnostic.Report
	fn         *FuncCFG
	curBB      BlockID
	nextScope  int
	tempSymSeq int
	scopes     []scopeFrame
	loops      []loopFrame
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
	var retType types.TypeID
	if fnSym != nil {
		retType = fnSym.ReturnType
	}
	f := &FuncCFG{
		Name:       name,
		FuncSym:    fnSym,
		Decl:       decl,
		ReturnType: retType,
		Locals:     make(map[symbols.SymbolID]*LocalMeta),
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

func (b *funcBuilder) nextTemp() ValueID {
	b.fn.NextTemp++
	return b.fn.NextTemp
}

func (b *funcBuilder) nextLoan() LoanID {
	b.fn.NextLoan++
	return b.fn.NextLoan
}

// isRefCarryingType 判断类型自身是否属于引用，或递归结构体/元组/数组/枚举中包含借用引用载体。
func (b *funcBuilder) isRefCarryingType(tid types.TypeID) bool {
	if tid == types.Invalid || b.view == nil || b.view.GetTypes() == nil {
		return false
	}
	visited := make(map[types.TypeID]bool, 8)
	return b.hasRefRec(tid, visited)
}

func (b *funcBuilder) hasRefRec(tid types.TypeID, visited map[types.TypeID]bool) bool {
	if tid == types.Invalid || visited[tid] {
		return false
	}
	visited[tid] = true
	tctx := b.view.GetTypes()
	symTab := b.view.GetSymbols()
	info := tctx.Info(tid)
	switch info.Kind {
	case types.KindRef:
		return true
	case types.KindTuple, types.KindSlice:
		if info.Elem != types.Invalid && b.hasRefRec(info.Elem, visited) {
			return true
		}
		for _, el := range info.Elems {
			if b.hasRefRec(el, visited) {
				return true
			}
		}
		return false
	case types.KindArray:
		return b.hasRefRec(info.Elem, visited)
	case types.KindStruct:
		if symTab == nil {
			return false
		}
		if sym := symTab.ByID[symbols.SymbolID(info.SymbolID)]; sym != nil {
			for _, f := range sym.Fields {
				ft := f.Type
				if len(sym.GenericParamIDs) > 0 && len(sym.GenericParamIDs) == len(info.GenericArgs) {
					subst := make(map[uint64]types.TypeID, len(info.GenericArgs))
					for i, gid := range sym.GenericParamIDs {
						subst[uint64(gid)] = info.GenericArgs[i]
					}
					ft = tctx.Substitute(ft, subst)
				}
				if b.hasRefRec(ft, visited) {
					return true
				}
			}
		}
		return false
	case types.KindEnum:
		for _, ga := range info.GenericArgs {
			if b.hasRefRec(ga, visited) {
				return true
			}
		}
		if symTab != nil {
			if sym := symTab.ByID[symbols.SymbolID(info.SymbolID)]; sym != nil {
				for _, v := range sym.Variants {
					for _, tp := range v.TupleTypes {
						if b.hasRefRec(tp, visited) {
							return true
						}
					}
					for _, sf := range v.StructFields {
						if b.hasRefRec(sf.Type, visited) {
							return true
						}
					}
				}
			}
		}
		return false
	default:
		return false
	}
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
	for idx, p := range decl.Params {
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
		b.registerLocal(matched, true, idx, true, 0)
		b.emit(Statement{
			Kind:  OpInitAssign,
			Place: b.placeForSym(matched, p.Span),
			Type:  matched.Type,
			Span:  p.Span,
		})
	}
}

func (b *funcBuilder) registerLocal(sym *symbols.Symbol, isParam bool, paramIdx int, hasInit bool, scopeID int) {
	if sym == nil {
		return
	}
	if _, exists := b.fn.Locals[sym.ID]; exists {
		return
	}
	b.fn.LocalOrder = append(b.fn.LocalOrder, sym.ID)
	b.fn.Locals[sym.ID] = &LocalMeta{
		Sym:      sym,
		Type:     sym.Type,
		IsParam:  isParam,
		ParamIdx: paramIdx,
		IsMut:    sym.IsMut,
		HasInit:  hasInit,
		ScopeID:  scopeID,
		Span:     sym.Span,
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
	if len(localsCopy) > 0 {
		deadCopy := make([]symbols.SymbolID, len(localsCopy))
		copy(deadCopy, localsCopy)
		b.emit(Statement{
			Kind:    OpStorageDead,
			ScopeID: top.id,
			Locals:  deadCopy,
			Span:    span,
		})
	}
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
		if len(localsCopy) > 0 {
			deadCopy := make([]symbols.SymbolID, len(localsCopy))
			copy(deadCopy, localsCopy)
			b.emit(Statement{
				Kind:    OpStorageDead,
				ScopeID: frame.id,
				Locals:  deadCopy,
				Span:    span,
			})
		}
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
			retTemp := b.lowerExprVal(s.Value)
			retPlace, _ := b.extractPlace(s.Value)
			b.emit(Statement{
				Kind:    OpReturn,
				Place:   retPlace,
				SrcTemp: retTemp,
				Type:    b.view.ExprType(s.Value),
				Span:    s.Span,
			})
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
		b.registerLocal(sym, false, -1, false, curScopeID)
		b.addLocalToTopScope(sym.ID)
		b.emit(Statement{
			Kind:  OpUninitDecl,
			Place: b.placeForSym(sym, s.Span),
			Type:  sym.Type,
			Span:  s.Span,
		})
		return
	}

	initTemp := b.lowerExprVal(s.Init)

	if s.Pattern != nil {
		b.registerPatternLocals(s.Pattern, true, curScopeID, initTemp, s.Span)
	} else if s.Name != "" && s.Name != "_" {
		sym := b.lookupDeclSym(s)
		if sym == nil {
			b.emitInternalError(s.Span, fmt.Sprintf("变量绑定 '%s' 缺少符号解析记录", s.Name))
			return
		}
		b.registerLocal(sym, false, -1, true, curScopeID)
		b.addLocalToTopScope(sym.ID)
		b.emit(Statement{
			Kind:    OpInitAssign,
			Place:   b.placeForSym(sym, s.Span),
			SrcTemp: initTemp,
			Type:    sym.Type,
			Span:    s.Span,
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

func (b *funcBuilder) registerPatternLocalsWithSubProj(pat ast.Pattern, initNow bool, curScopeID int, srcTemp ValueID, proj Projection, span diagnostic.Span) {
	if ip, ok := pat.(*ast.IdentPattern); ok && b.view.VariantRef(ip) == nil {
		symTab := b.view.GetSymbols()
		sym := symTab.NodeSymbols[ip]
		if sym == nil {
			b.emitInternalError(ip.Span, fmt.Sprintf("模式绑定标识符 '%s' 缺少 SymbolID 映射", ip.Name))
			return
		}
		b.registerLocal(sym, false, -1, initNow, curScopeID)
		b.addLocalToTopScope(sym.ID)
		op := OpInitAssign
		if !initNow {
			op = OpUninitDecl
		}
		var pPlace Place
		if srcTemp != InvalidValue {
			pPlace = Place{Projections: []Projection{proj}}
		}
		b.emit(Statement{
			Kind:        op,
			Place:       b.placeForSym(sym, ip.Span),
			SrcTemp:     srcTemp,
			ParentPlace: pPlace,
			Type:        sym.Type,
			Span:        ip.Span,
		})
		return
	}
	b.registerPatternLocals(pat, initNow, curScopeID, srcTemp, span)
}

func (b *funcBuilder) registerPatternLocals(pat ast.Pattern, initNow bool, curScopeID int, srcTemp ValueID, span diagnostic.Span) {
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
		b.registerLocal(sym, false, -1, initNow, curScopeID)
		b.addLocalToTopScope(sym.ID)
		op := OpInitAssign
		if !initNow {
			op = OpUninitDecl
		}
		b.emit(Statement{
			Kind:    op,
			Place:   b.placeForSym(sym, p.Span),
			SrcTemp: srcTemp,
			Type:    sym.Type,
			Span:    p.Span,
		})

	case *ast.TuplePattern:
		for idx, sub := range p.Elems {
			b.registerPatternLocalsWithSubProj(sub, initNow, curScopeID, srcTemp, Projection{Kind: ProjTupleField, FieldIndex: idx}, span)
		}

	case *ast.StructPattern:
		for _, fp := range p.Fields {
			subProj := Projection{Kind: ProjField, FieldName: fp.Name}
			if fp.Pattern != nil {
				b.registerPatternLocalsWithSubProj(fp.Pattern, initNow, curScopeID, srcTemp, subProj, fp.Span)
			} else {
				sym := symTab.NodeSymbols[fp]
				if sym == nil {
					b.emitInternalError(fp.Span, fmt.Sprintf("结构体简写模式字段 '%s' 缺少 SymbolID 映射", fp.Name))
					return
				}
				b.registerLocal(sym, false, -1, initNow, curScopeID)
				b.addLocalToTopScope(sym.ID)
				op := OpInitAssign
				if !initNow {
					op = OpUninitDecl
				}
				var pPlace Place
				if srcTemp != InvalidValue {
					pPlace = Place{Projections: []Projection{subProj}}
				}
				b.emit(Statement{
					Kind:        op,
					Place:       b.placeForSym(sym, fp.Span),
					SrcTemp:     srcTemp,
					ParentPlace: pPlace,
					Type:        sym.Type,
					Span:        fp.Span,
				})
			}
		}

	case *ast.EnumPattern:
		for _, arg := range p.Args {
			b.registerPatternLocals(arg, initNow, curScopeID, srcTemp, span)
		}

	case *ast.SlicePattern:
		for _, elem := range p.Elems {
			b.registerPatternLocals(elem, initNow, curScopeID, srcTemp, span)
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

	valTemp := b.lowerExprVal(s.Value)

	b.emit(Statement{
		Kind:    OpAssign,
		Place:   lhsPlace,
		SrcTemp: valTemp,
		Type:    lhsPlace.ElemType,
		Span:    s.Span,
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
	b.lowerIfExpr(s, InvalidValue, types.Invalid)
}

func (b *funcBuilder) lowerIfExpr(s *ast.IfStmt, destTemp ValueID, destType types.TypeID) {
	b.lowerExprValue(s.Cond)
	condBB := b.curBB
	thenBB := b.newBlock("if.then")
	elseBB := b.newBlock("if.else")
	mergeBB := b.newBlock("if.merge")

	b.terminateSwitch(condBB, []BlockID{thenBB, elseBB}, s.Span)

	b.curBB = thenBB
	if destTemp != InvalidValue {
		b.lowerBlockWithResult(s.Then, destTemp, destType)
	} else {
		b.lowerBlock(s.Then)
	}
	thenEnd := b.curBB
	thenClosed := b.isTerminated(thenEnd)
	if !thenClosed {
		b.terminateGoto(thenEnd, mergeBB, s.Span)
	}

	b.curBB = elseBB
	if s.Else != nil {
		if blk, isBlk := s.Else.(*ast.BlockStmt); isBlk && destTemp != InvalidValue {
			b.lowerBlockWithResult(blk, destTemp, destType)
		} else if innerIf, isIf := s.Else.(*ast.IfStmt); isIf && destTemp != InvalidValue {
			b.lowerIfExpr(innerIf, destTemp, destType)
		} else {
			b.lowerStmt(s.Else)
		}
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

func (b *funcBuilder) lowerBlockWithResult(blk *ast.BlockStmt, destTemp ValueID, destType types.TypeID) {
	if blk == nil {
		return
	}
	sid := b.pushScope(blk.Span)
	b.lowerBlockStmtsWithResult(blk, destTemp, destType)
	if !b.isTerminated(b.curBB) {
		b.popScope(sid, blk.Span)
	} else {
		b.discardTopScope()
	}
}

func (b *funcBuilder) lowerBlockStmtsWithResult(blk *ast.BlockStmt, destTemp ValueID, destType types.TypeID) {
	if blk == nil {
		return
	}
	for i, stmt := range blk.Statements {
		if b.isTerminated(b.curBB) {
			deadBB := b.newBlock("unreachable")
			b.curBB = deadBB
		}
		if destTemp != InvalidValue && i == len(blk.Statements)-1 {
			if es, isExpr := stmt.(*ast.ExprStmt); isExpr {
				if val := b.lowerExprVal(es.Expr); val != InvalidValue {
					b.emit(Statement{
						Kind:    OpTempAssign,
						ValTemp: destTemp,
						SrcTemp: val,
						Type:    destType,
						Span:    es.Span,
					})
				}
				continue
			}
		}
		b.lowerStmt(stmt)
	}
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
		b.registerPatternLocals(s.Pattern, true, sid, InvalidValue, s.Span)
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
	b.lowerMatchExprVal(m, InvalidValue, types.Invalid)
}

func (b *funcBuilder) lowerMatchExprVal(m *ast.MatchExpr, destTemp ValueID, destType types.TypeID) {
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

		b.lowerBlockStmtsWithResult(arm.Body, destTemp, destType)
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
	if len(localsCopy) > 0 {
		deadCopy := make([]symbols.SymbolID, len(localsCopy))
		copy(deadCopy, localsCopy)
		b.emit(Statement{
			Kind:    OpStorageDead,
			ScopeID: top.id,
			Locals:  deadCopy,
			Span:    span,
		})
	}
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
		var initSrcTemp ValueID
		if hasPlace {
			if p.IsRef {
				op := OpBorrowShared
				bk := BorrowShared
				if p.IsMut {
					op = OpBorrowMut
					bk = BorrowMut
				}
				borrowPlace := place
				var parentPlace Place
				reborrow := false
				if isThroughRef && !place.HasDeref() {
					borrowPlace = place.AppendProj(Projection{Kind: ProjDeref, ElemType: sym.Type}, sym.Type, p.Span)
				}
				if pPlace, isRB := b.extractReborrowParent(borrowPlace); isRB {
					reborrow = true
					parentPlace = pPlace
					op = OpReborrow
					if p.IsMut {
						bk = BorrowReborrowMut
					} else {
						bk = BorrowReborrowShared
					}
				}
				initSrcTemp = b.nextTemp()
				b.emit(Statement{Kind: OpRead, Place: place, Type: place.ElemType, Span: p.Span})
				b.emit(Statement{
					Kind:        op,
					Place:       borrowPlace,
					Type:        sym.Type,
					LoanID:      b.nextLoan(),
					BorrowKind:  bk,
					IsReborrow:  reborrow,
					ParentPlace: parentPlace,
					ValTemp:     initSrcTemp,
					Span:        p.Span,
				})
			} else if sym.Type != types.Invalid && !tctx.IsCopy(sym.Type) {
				if isThroughRef || place.HasDeref() || place.HasIndex() {
					// 通过引用/解引用匹配出的非 Copy 值按值绑定与直接解引用移出一致，触发 WON-MEM-008 防线
					pEmit := place
					if isThroughRef && !place.HasDeref() {
						pEmit = place.AppendProj(Projection{Kind: ProjDeref, ElemType: sym.Type}, sym.Type, p.Span)
					}
					if b.isRefCarryingType(sym.Type) {
						initSrcTemp = b.nextTemp()
					}
					b.emit(Statement{Kind: OpMove, Place: pEmit, Type: sym.Type, ValTemp: initSrcTemp, Span: p.Span})
				} else {
					if b.isRefCarryingType(sym.Type) {
						initSrcTemp = b.nextTemp()
					}
					b.emit(Statement{Kind: OpMove, Place: place, Type: sym.Type, ValTemp: initSrcTemp, Span: p.Span})
				}
			} else {
				// Copy 类型按值绑定同样记录显式 OpCopy 源读取，确保每个局部变量均有对应的源所有权操作且不产生伪 Move
				if b.isRefCarryingType(sym.Type) {
					initSrcTemp = b.nextTemp()
				}
				b.emit(Statement{Kind: OpCopy, Place: place, Type: sym.Type, ValTemp: initSrcTemp, Span: p.Span})
			}
		}
		b.registerLocal(sym, false, -1, true, scopeID)
		b.addLocalToTopScope(sym.ID)
		b.emit(Statement{
			Kind:    OpInitAssign,
			Place:   b.placeForSym(sym, p.Span),
			SrcTemp: initSrcTemp,
			Type:    sym.Type,
			Span:    p.Span,
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
				var fieldValTemp ValueID
				if hasPlace {
					if b.isRefCarryingType(sym.Type) {
						fieldValTemp = b.nextTemp()
					}
					if sym.Type != types.Invalid && !tctx.IsCopy(sym.Type) {
						b.emit(Statement{Kind: OpMove, Place: fieldPlace, Type: sym.Type, ValTemp: fieldValTemp, Span: fp.Span})
					} else {
						b.emit(Statement{Kind: OpCopy, Place: fieldPlace, Type: sym.Type, ValTemp: fieldValTemp, Span: fp.Span})
					}
				}
				b.registerLocal(sym, false, -1, true, scopeID)
				b.addLocalToTopScope(sym.ID)
				b.emit(Statement{
					Kind:    OpInitAssign,
					Place:   b.placeForSym(sym, fp.Span),
					SrcTemp: fieldValTemp,
					Type:    sym.Type,
					Span:    fp.Span,
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

	type patBind struct {
		sym     *symbols.Symbol
		srcTemp ValueID
	}
	var bindings []patBind
	var borrowOps []Statement
	hasRef := false
	hasCopyBind := false
	hasNonCopyMove := false

	borrowPlace := place
	var parentPlace Place
	isReborrow := false
	if hasPlace {
		if isThroughRef && !place.HasDeref() {
			borrowPlace = place.AppendProj(Projection{Kind: ProjDeref, ElemType: place.ElemType}, place.ElemType, pat.GetSpan())
		}
		if pPlace, isRB := b.extractReborrowParent(borrowPlace); isRB {
			isReborrow = true
			parentPlace = pPlace
		}
	}

	var aggReadTemp ValueID
	ensureAggTemp := func() ValueID {
		if aggReadTemp == InvalidValue {
			aggReadTemp = b.nextTemp()
		}
		return aggReadTemp
	}

	var walk func(node ast.Pattern, elemPlace Place)
	walk = func(node ast.Pattern, elemPlace Place) {
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
			if p.IsRef {
				hasRef = true
				op := OpBorrowShared
				bk := BorrowShared
				if p.IsMut {
					op = OpBorrowMut
					bk = BorrowMut
				}
				if isReborrow {
					op = OpReborrow
					if p.IsMut {
						bk = BorrowReborrowMut
					} else {
						bk = BorrowReborrowShared
					}
				}
				var bt ValueID
				if hasPlace {
					bt = b.nextTemp()
					borrowOps = append(borrowOps, Statement{
						Kind:        op,
						Place:       elemPlace,
						Type:        sym.Type,
						LoanID:      b.nextLoan(),
						BorrowKind:  bk,
						IsReborrow:  isReborrow,
						ParentPlace: parentPlace,
						ValTemp:     bt,
						Span:        p.Span,
					})
				}
				bindings = append(bindings, patBind{sym: sym, srcTemp: bt})
			} else if sym.Type != types.Invalid && !tctx.IsCopy(sym.Type) {
				hasNonCopyMove = true
				var st ValueID
				if hasPlace && b.isRefCarryingType(sym.Type) {
					st = ensureAggTemp()
				}
				bindings = append(bindings, patBind{sym: sym, srcTemp: st})
			} else {
				hasCopyBind = true
				var st ValueID
				if hasPlace && b.isRefCarryingType(sym.Type) {
					st = ensureAggTemp()
				}
				bindings = append(bindings, patBind{sym: sym, srcTemp: st})
			}
		case *ast.TuplePattern:
			for idx, el := range p.Elems {
				nextPlace := elemPlace
				if hasPlace {
					nextPlace = elemPlace.AppendProj(Projection{Kind: ProjTupleField, FieldIndex: idx}, types.Invalid, el.GetSpan())
				}
				walk(el, nextPlace)
			}
		case *ast.EnumPattern:
			for _, arg := range p.Args {
				walk(arg, elemPlace)
			}
		case *ast.StructPattern:
			for _, fp := range p.Fields {
				if fp.Pattern != nil {
					walk(fp.Pattern, elemPlace)
				} else if sym := symTab.NodeSymbols[fp]; sym != nil {
					var st ValueID
					if sym.Type != types.Invalid && !tctx.IsCopy(sym.Type) {
						hasNonCopyMove = true
					} else {
						hasCopyBind = true
					}
					if hasPlace && b.isRefCarryingType(sym.Type) {
						st = ensureAggTemp()
					}
					bindings = append(bindings, patBind{sym: sym, srcTemp: st})
				} else {
					b.emitInternalError(fp.Span, fmt.Sprintf("嵌套结构体简写字段 '%s' 缺少 SymbolID", fp.Name))
				}
			}
		case *ast.SlicePattern:
			for _, el := range p.Elems {
				walk(el, elemPlace)
			}
		default:
			b.emitInternalError(node.GetSpan(), fmt.Sprintf("聚合模式内部包含未处理的节点 %T", node))
		}
	}

	walk(pat, borrowPlace)

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
			b.emit(Statement{Kind: OpMove, Place: movePlace, Type: place.ElemType, ValTemp: aggReadTemp, Span: pat.GetSpan()})
		} else {
			if hasRef {
				b.emit(Statement{Kind: OpRead, Place: place, Type: place.ElemType, Span: pat.GetSpan()})
				for _, bop := range borrowOps {
					b.emit(bop)
				}
			}
			if hasCopyBind {
				b.emit(Statement{Kind: OpCopy, Place: place, Type: place.ElemType, ValTemp: aggReadTemp, Span: pat.GetSpan()})
			}
		}
	}

	for _, bd := range bindings {
		b.registerLocal(bd.sym, false, -1, true, scopeID)
		b.addLocalToTopScope(bd.sym.ID)
		b.emit(Statement{
			Kind:    OpInitAssign,
			Place:   b.placeForSym(bd.sym, bd.sym.Span),
			SrcTemp: bd.srcTemp,
			Type:    bd.sym.Type,
			Span:    bd.sym.Span,
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
	_ = b.lowerExprVal(expr)
}

func (b *funcBuilder) lowerExprVal(expr ast.Expr) ValueID {
	if expr == nil {
		return InvalidValue
	}

	if place, ok := b.extractPlace(expr); ok {
		return b.lowerPlaceValueAccess(expr, place)
	}

	exprType := b.view.ExprType(expr)
	switch e := expr.(type) {
	case *ast.IntegerLiteral, *ast.FloatLiteral, *ast.BoolLiteral, *ast.CharLiteral, *ast.ByteLiteral, *ast.PathExpr:
		return InvalidValue

	case *ast.StringLiteral:
		if b.isRefCarryingType(exprType) {
			v := b.nextTemp()
			b.emit(Statement{
				Kind:    OpStaticRef,
				ValTemp: v,
				Type:    exprType,
				Span:    e.Span,
			})
			return v
		}
		return InvalidValue

	case *ast.InterpolatedStringExpr:
		for _, part := range e.Parts {
			if part.IsExpr && part.Expr != nil {
				b.lowerExprValue(part.Expr)
			}
		}
		return InvalidValue

	case *ast.ArrayLiteral:
		if v, ok := b.lowerAggregateExprsWithCleanup(e.Elems, func(i int, tv ValueID) AggTemp {
			return AggTemp{Index: i, Temp: tv}
		}, exprType, e.Span); ok {
			return v
		}
		var aggTemps []AggTemp
		for idx, el := range e.Elems {
			if tv := b.lowerExprVal(el); tv != InvalidValue {
				aggTemps = append(aggTemps, AggTemp{Index: idx, Temp: tv})
			}
		}
		if len(aggTemps) > 0 || b.isRefCarryingType(exprType) {
			v := b.nextTemp()
			b.emit(Statement{Kind: OpAggregate, AggTemps: aggTemps, ValTemp: v, Type: exprType, Span: e.Span})
			return v
		}
		return InvalidValue

	case *ast.ArrayRepeatLiteral:
		var aggTemps []AggTemp
		if tv := b.lowerExprVal(e.Elem); tv != InvalidValue {
			aggTemps = append(aggTemps, AggTemp{Index: -1, Temp: tv})
		}
		if len(aggTemps) > 0 || b.isRefCarryingType(exprType) {
			v := b.nextTemp()
			b.emit(Statement{Kind: OpAggregate, AggTemps: aggTemps, ValTemp: v, Type: exprType, Span: e.Span})
			return v
		}
		return InvalidValue

	case *ast.TupleLiteral:
		if v, ok := b.lowerAggregateExprsWithCleanup(e.Elems, func(i int, tv ValueID) AggTemp {
			return AggTemp{Index: i, Temp: tv}
		}, exprType, e.Span); ok {
			return v
		}
		var aggTemps []AggTemp
		for idx, el := range e.Elems {
			if tv := b.lowerExprVal(el); tv != InvalidValue {
				aggTemps = append(aggTemps, AggTemp{Index: idx, Temp: tv})
			}
		}
		if len(aggTemps) > 0 || b.isRefCarryingType(exprType) {
			v := b.nextTemp()
			b.emit(Statement{Kind: OpAggregate, AggTemps: aggTemps, ValTemp: v, Type: exprType, Span: e.Span})
			return v
		}
		return InvalidValue

	case *ast.StructLiteral:
		fieldExprs := make([]ast.Expr, 0, len(e.Fields))
		fieldNames := make([]string, 0, len(e.Fields))
		for _, f := range e.Fields {
			if f.Value != nil {
				fieldExprs = append(fieldExprs, f.Value)
				fieldNames = append(fieldNames, f.Name)
			}
		}
		if v, ok := b.lowerAggregateExprsWithCleanup(fieldExprs, func(i int, tv ValueID) AggTemp {
			return AggTemp{FieldName: fieldNames[i], Index: i, Temp: tv}
		}, exprType, e.Span); ok {
			return v
		}
		var aggTemps []AggTemp
		for idx, f := range e.Fields {
			if f.Value != nil {
				if tv := b.lowerExprVal(f.Value); tv != InvalidValue {
					aggTemps = append(aggTemps, AggTemp{FieldName: f.Name, Index: idx, Temp: tv})
				}
			}
		}
		if len(aggTemps) > 0 || b.isRefCarryingType(exprType) {
			v := b.nextTemp()
			b.emit(Statement{Kind: OpAggregate, AggTemps: aggTemps, ValTemp: v, Type: exprType, Span: e.Span})
			return v
		}
		return InvalidValue

	case *ast.Identifier:
		// 非局部/参数标识符（如枚举单元构造器 / 函数名），检查符号确实已解析
		if sym := b.view.GetSymbols().NodeSymbols[e]; sym == nil {
			b.emitInternalError(e.Span, fmt.Sprintf("标识符表达式 '%s' 缺少已解析符号记录", e.Name))
		}
		return InvalidValue

	case *ast.FieldAccessExpr:
		return b.lowerExprVal(e.Object)

	case *ast.IndexExpr:
		objVal := b.lowerExprVal(e.Object)
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
		return objVal

	case *ast.RangeExpr:
		b.lowerExprValue(e.Start)
		b.lowerExprValue(e.End)
		return InvalidValue

	case *ast.CastExpr:
		b.lowerExprValue(e.Expr)
		return InvalidValue

	case *ast.UnaryExpr:
		return b.lowerExprVal(e.Expr)

	case *ast.BorrowExpr:
		targetPlace, ok := b.extractPlace(e.Expr)
		if ok {
			b.lowerPlaceAddressSubExprs(e.Expr)
			opKind := OpBorrowShared
			bk := BorrowShared
			if e.IsMut {
				opKind = OpBorrowMut
				bk = BorrowMut
			}
			var parentPlace Place
			isReborrow := false
			if pPlace, isRB := b.extractReborrowParent(targetPlace); isRB {
				isReborrow = true
				parentPlace = pPlace
				opKind = OpReborrow
				if e.IsMut {
					bk = BorrowReborrowMut
				} else {
					bk = BorrowReborrowShared
				}
			}
			v := b.nextTemp()
			b.emit(Statement{
				Kind:  OpRead,
				Place: targetPlace,
				Type:  targetPlace.ElemType,
				Span:  e.Span,
			})
			b.emit(Statement{
				Kind:        opKind,
				Place:       targetPlace,
				Type:        exprType,
				LoanID:      b.nextLoan(),
				BorrowKind:  bk,
				IsReborrow:  isReborrow,
				ParentPlace: parentPlace,
				ValTemp:     v,
				Span:        e.Span,
			})
			return v
		}
		return b.lowerExprVal(e.Expr)

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
			return InvalidValue
		}
		b.lowerExprValue(e.Left)
		b.lowerExprValue(e.Right)
		return InvalidValue

	case *ast.TryExpr:
		inner := b.lowerExprVal(e.Expr)
		tryBB := b.curBB
		okBB := b.newBlock("try.ok")
		errBB := b.newBlock("try.err")
		b.terminateSwitch(tryBB, []BlockID{okBB, errBB}, e.Span)
		b.curBB = errBB
		b.emitScopeExitsDownTo(0, e.Span)
		b.terminateReturn(errBB, e.Span)
		b.curBB = okBB
		return inner

	case *ast.CallExpr:
		if e.Callee == "" && e.CalleeExpr != nil {
			b.lowerExprValue(e.CalleeExpr)
		}
		calSym := b.view.CallTarget(e)
		return b.emitCallExpr(calSym, e.Callee, e.Args, exprType, e.Span, nil)

	case *ast.NamespacedCallExpr:
		calSym := b.view.CallTarget(e)
		name := e.Namespace + "::" + e.Function
		return b.emitCallExpr(calSym, name, e.Args, exprType, e.Span, nil)

	case *ast.MethodCallExpr:
		calSym := b.view.CallTarget(e)
		var recvPtr *ValueID
		if calSym != nil && calSym.HasSelf && !calSym.SelfIsRef {
			rv := b.lowerExprVal(e.Receiver)
			if rv != InvalidValue {
				recvPtr = &rv
			}
		} else if p, ok := b.extractPlace(e.Receiver); ok {
			b.lowerPlaceAddressSubExprs(e.Receiver)
			b.emit(Statement{
				Kind:  OpRead,
				Place: p,
				Type:  p.ElemType,
				Span:  e.Receiver.GetSpan(),
			})
			isMut := (calSym != nil && calSym.SelfIsMut) || e.Method == "push_str" || e.Method == "as_mut_ptr"
			borrowTarget := p
			var parentPlace Place
			isReborrow := false
			recvType := b.view.ExprType(e.Receiver)
			if recvType == types.Invalid {
				recvType = p.ElemType
			}
			recvInfo := b.view.GetTypes().Info(recvType)
			if recvInfo.Kind == types.KindRef {
				// 当方法调用在 &T / &mut T 引用绑定上时，隐式自动解引用形成对其所借存储的重借用
				isReborrow = true
				parentPlace = p
				borrowTarget = p.AppendProj(Projection{Kind: ProjDeref, ElemType: recvInfo.Elem}, recvInfo.Elem, e.Span)
			} else if pPlace, isRB := b.extractReborrowParent(p); isRB {
				isReborrow = true
				parentPlace = pPlace
			}
			opKind := OpBorrowShared
			bk := BorrowShared
			if isMut {
				opKind = OpBorrowMut
				bk = BorrowMut
			}
			if isReborrow {
				opKind = OpReborrow
				if isMut {
					bk = BorrowReborrowMut
				} else {
					bk = BorrowReborrowShared
				}
			}
			rv := b.nextTemp()
			borrowType := borrowTarget.ElemType
			if tctx := b.view.GetTypes(); tctx != nil && borrowTarget.ElemType != types.Invalid {
				borrowType = tctx.Ref(isMut, borrowTarget.ElemType)
			}
			b.emit(Statement{
				Kind:        opKind,
				Place:       borrowTarget,
				Type:        borrowType,
				LoanID:      b.nextLoan(),
				BorrowKind:  bk,
				IsReborrow:  isReborrow,
				ParentPlace: parentPlace,
				ValTemp:     rv,
				Span:        e.Span,
			})
			recvPtr = &rv
		} else {
			rv := b.lowerExprVal(e.Receiver)
			if rv != InvalidValue {
				recvPtr = &rv
			}
		}
		return b.emitCallExpr(calSym, e.Method, e.Args, exprType, e.Span, recvPtr)

	case *ast.UnsafeExpr:
		if b.isRefCarryingType(exprType) {
			v := b.nextTemp()
			b.lowerBlockWithResult(e.Block, v, exprType)
			return v
		}
		b.lowerBlock(e.Block)
		return InvalidValue

	case *ast.IfStmt:
		if b.isRefCarryingType(exprType) {
			v := b.nextTemp()
			b.lowerIfExpr(e, v, exprType)
			return v
		}
		b.lowerIfStmt(e)
		return InvalidValue

	case *ast.WhileStmt:
		b.lowerWhileStmt(e)
		return InvalidValue

	case *ast.ForStmt:
		b.lowerForStmt(e)
		return InvalidValue

	case *ast.MatchExpr:
		if b.isRefCarryingType(exprType) {
			v := b.nextTemp()
			b.lowerMatchExprVal(e, v, exprType)
			return v
		}
		b.lowerMatchExpr(e)
		return InvalidValue

	case *ast.ScopeExpr, *ast.SpawnExpr:
		b.emitUnsupportedError(
			expr.GetSpan(),
			"结构化并发原语 (scope/spawn) 尚未在当前所有权阶段启用",
			"结构化并发所有权校验属于后续独立里程碑。",
		)
		return InvalidValue

	default:
		b.emitInternalError(expr.GetSpan(), fmt.Sprintf("Encountered unhandled expression node %T in ownership CFG", expr))
		return InvalidValue
	}
}

// lowerAggregateExprsWithCleanup 处理包含前置资源成员且后续存在 '?' 早退的聚合字面量。
// 将早先完成求值的非 Copy 成员暂存至受作用域托管的合成局部槽位：一旦后续字段触发 '?' 早退，
// OpScopeExit 仅清理已完成初始化的前置成员；若全部成员顺利就绪，则统一 OpMove 移入完整聚合。
func (b *funcBuilder) lowerAggregateExprsWithCleanup(exprs []ast.Expr, buildItem func(int, ValueID) AggTemp, exprType types.TypeID, span diagnostic.Span) (ValueID, bool) {
	if len(exprs) < 2 || b.view == nil || b.view.GetTypes() == nil || b.view.GetSymbols() == nil {
		return InvalidValue, false
	}
	tctx := b.view.GetTypes()
	symTab := b.view.GetSymbols()

	lastTryIdx := -1
	for i, ex := range exprs {
		if containsTryExpr(ex) {
			lastTryIdx = i
		}
	}
	if lastTryIdx <= 0 {
		return InvalidValue, false
	}

	hasPriorOwned := false
	for i := 0; i < lastTryIdx; i++ {
		et := b.view.ExprType(exprs[i])
		if et != types.Invalid && !tctx.IsCopy(et) && !b.isRefCarryingType(et) {
			hasPriorOwned = true
			break
		}
	}
	if !hasPriorOwned {
		return InvalidValue, false
	}

	tmpScope := b.pushScope(span)
	type heldTemp struct {
		place Place
		typ   types.TypeID
		span  diagnostic.Span
	}
	var held []heldTemp
	var aggTemps []AggTemp

	for i, ex := range exprs {
		tv := b.lowerExprVal(ex)
		if tv != InvalidValue {
			aggTemps = append(aggTemps, buildItem(i, tv))
		}
		if b.isTerminated(b.curBB) {
			break
		}
		et := b.view.ExprType(ex)
		if i < lastTryIdx && et != types.Invalid && !tctx.IsCopy(et) && !b.isRefCarryingType(et) {
			b.tempSymSeq++
			tmpSym := symTab.AllocSymbol(symbols.SymVar, fmt.Sprintf("__agg_tmp_%d", b.tempSymSeq), ex.GetSpan())
			tmpSym.Type = et
			tmpSym.IsMut = true
			b.registerLocal(tmpSym, false, -1, true, tmpScope)
			b.addLocalToTopScope(tmpSym.ID)
			tmpPlace := b.placeForSym(tmpSym, ex.GetSpan())
			b.emit(Statement{
				Kind:    OpInitAssign,
				Place:   tmpPlace,
				SrcTemp: tv,
				Type:    et,
				Span:    ex.GetSpan(),
			})
			held = append(held, heldTemp{place: tmpPlace, typ: et, span: ex.GetSpan()})
		}
	}

	if !b.isTerminated(b.curBB) {
		for _, h := range held {
			b.emit(Statement{
				Kind:  OpMove,
				Place: h.place,
				Type:  h.typ,
				Span:  h.span,
			})
		}
		b.popScope(tmpScope, span)
	} else {
		b.discardTopScope()
	}

	var out ValueID
	if len(aggTemps) > 0 || b.isRefCarryingType(exprType) {
		out = b.nextTemp()
		b.emit(Statement{Kind: OpAggregate, AggTemps: aggTemps, ValTemp: out, Type: exprType, Span: span})
	}
	return out, true
}

func containsTryExpr(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *ast.TryExpr:
		return true
	case *ast.UnaryExpr:
		return containsTryExpr(e.Expr)
	case *ast.BorrowExpr:
		return containsTryExpr(e.Expr)
	case *ast.CastExpr:
		return containsTryExpr(e.Expr)
	case *ast.FieldAccessExpr:
		return containsTryExpr(e.Object)
	case *ast.IndexExpr:
		return containsTryExpr(e.Object) || containsTryExpr(e.Index)
	case *ast.BinaryExpr:
		return containsTryExpr(e.Left) || containsTryExpr(e.Right)
	case *ast.RangeExpr:
		return containsTryExpr(e.Start) || containsTryExpr(e.End)
	case *ast.CallExpr:
		if containsTryExpr(e.CalleeExpr) {
			return true
		}
		for _, a := range e.Args {
			if containsTryExpr(a) {
				return true
			}
		}
	case *ast.NamespacedCallExpr:
		for _, a := range e.Args {
			if containsTryExpr(a) {
				return true
			}
		}
	case *ast.MethodCallExpr:
		if containsTryExpr(e.Receiver) {
			return true
		}
		for _, a := range e.Args {
			if containsTryExpr(a) {
				return true
			}
		}
	case *ast.ArrayLiteral:
		for _, el := range e.Elems {
			if containsTryExpr(el) {
				return true
			}
		}
	case *ast.ArrayRepeatLiteral:
		return containsTryExpr(e.Elem)
	case *ast.TupleLiteral:
		for _, el := range e.Elems {
			if containsTryExpr(el) {
				return true
			}
		}
	case *ast.StructLiteral:
		for _, f := range e.Fields {
			if containsTryExpr(f.Value) {
				return true
			}
		}
	}
	return false
}

func (b *funcBuilder) emitCallExpr(calSym *symbols.Symbol, fallbackName string, args []ast.Expr, exprType types.TypeID, span diagnostic.Span, selfTemp *ValueID) ValueID {
	argTemps := make([]ValueID, 0, len(args)+1)
	argPlaces := make([]Place, 0, len(args)+1)
	if selfTemp != nil {
		argTemps = append(argTemps, *selfTemp)
		argPlaces = append(argPlaces, Place{})
	}
	for _, arg := range args {
		argTemps = append(argTemps, b.lowerExprVal(arg))
		if p, ok := b.extractPlace(arg); ok {
			argPlaces = append(argPlaces, p)
		} else {
			argPlaces = append(argPlaces, Place{})
		}
	}
	name := fallbackName
	if calSym != nil && calSym.Name != "" {
		name = calSym.Name
	}
	var retTemp ValueID
	if b.isRefCarryingType(exprType) {
		retTemp = b.nextTemp()
	}
	b.emit(Statement{
		Kind:       OpCall,
		Callee:     calSym,
		CalleeName: name,
		ArgTemps:   argTemps,
		ArgPlaces:  argPlaces,
		ValTemp:    retTemp,
		Type:       exprType,
		Span:       span,
	})
	return retTemp
}

// extractReborrowParent 判断基于 Place 的取址或方法调用是否穿过安全引用 (&T / &mut T) 形成重借用关系。
// 穿过裸指针 (*const T / *mut T) 解引用在 unsafe 中创建独立借用而非父借用衍生重借用。
func (b *funcBuilder) extractReborrowParent(place Place) (Place, bool) {
	parent, hasDeref := place.PrefixBeforeLastDeref()
	if !hasDeref || b.view == nil || b.view.GetTypes() == nil {
		return Place{}, false
	}
	pType := parent.ElemType
	if pType == types.Invalid {
		return Place{}, false
	}
	if b.view.GetTypes().Info(pType).Kind == types.KindRef {
		return parent, true
	}
	return Place{}, false
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

func (b *funcBuilder) lowerPlaceValueAccess(expr ast.Expr, place Place) ValueID {
	b.lowerPlacePrereqs(expr, place)

	exprType := b.view.ExprType(expr)
	if exprType == types.Invalid {
		exprType = place.ElemType
	}

	var valTemp ValueID
	if b.isRefCarryingType(exprType) {
		valTemp = b.nextTemp()
	}

	if exprType != types.Invalid && b.view.GetTypes().IsCopy(exprType) {
		b.emit(Statement{
			Kind:    OpCopy,
			Place:   place,
			Type:    exprType,
			ValTemp: valTemp,
			Span:    expr.GetSpan(),
		})
		return valTemp
	}

	b.emit(Statement{
		Kind:    OpMove,
		Place:   place,
		Type:    exprType,
		ValTemp: valTemp,
		Span:    expr.GetSpan(),
	})
	return valTemp
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

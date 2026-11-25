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
	fn         *FuncCFG
	curBB      BlockID
	nextScope  int
	scopes     []scopeFrame
	loops      []loopFrame
	declBySym  map[symbols.SymbolID]*ast.LetStmt
	inValBlock bool
}

// BuildProgram 基于已完成语义检查的 AST 与 SemanticView 构建全程序所有权 CFG。
func BuildProgram(prog *ast.Program, view SemanticView) *ProgramCFG {
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
		case *ast.FunctionDecl:
			if d.Body == nil {
				continue
			}
			fnSym := symTab.GlobalScope.LookupLocal(d.Name)
			if fnSym == nil {
				continue
			}
			fcfg := buildFunction(d.Name, fnSym, d, view)
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
					continue
				}
				qName := mSym.Name
				if mSym.OwnerSymID != 0 {
					if owner := symTab.ByID[mSym.OwnerSymID]; owner != nil {
						qName = owner.Name + "::" + mSym.Name
					}
				}
				fcfg := buildFunction(qName, mSym, m, view)
				res.Functions = append(res.Functions, fcfg)
				res.BySymbol[mSym.ID] = fcfg
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

func buildFunction(name string, fnSym *symbols.Symbol, decl *ast.FunctionDecl, view SemanticView) *FuncCFG {
	f := &FuncCFG{
		Name:    name,
		FuncSym: fnSym,
		Decl:    decl,
		Locals:  make(map[symbols.SymbolID]*LocalMeta),
	}
	b := &funcBuilder{
		view:      view,
		fn:        f,
		declBySym: make(map[symbols.SymbolID]*ast.LetStmt),
	}

	entry := b.newBlock("entry")
	f.Entry = entry
	exit := b.newBlock("exit")
	f.Exit = exit
	f.Blocks[exit].Terminator = Terminator{Kind: TermReturn, Span: decl.Span}

	b.curBB = entry
	rootScope := b.pushScope(decl.Span)

	// 登记参数符号：所有参数在函数入口处默认已初始化
	if decl.Body != nil {
		b.registerFunctionParams(decl, fnSym)
		b.lowerBlockStatements(decl.Body)
	}

	// 若正常路径未以显式 return/发散终结，则关闭函数根作用域并跳转到出口
	if !b.isTerminated(b.curBB) {
		b.popScope(rootScope, decl.Span)
		b.terminateGoto(b.curBB, exit, decl.Span)
	} else {
		b.discardTopScope()
	}

	b.finalizeEdges()
	return f
}

func (b *funcBuilder) registerFunctionParams(decl *ast.FunctionDecl, fnSym *symbols.Symbol) {
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
	_ = fnSym
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
			// 后续语句处于不可达基本块，仍创建独立死块以维持 CFG 完整性，且不接自前驱
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
		if len(b.loops) > 0 {
			lf := &b.loops[len(b.loops)-1]
			lf.breakEdges = true
			b.emitScopeExitsDownTo(lf.scopeDepth, s.Span)
			b.terminateGoto(b.curBB, lf.breakBB, s.Span)
		}

	case *ast.ContinueStmt:
		if len(b.loops) > 0 {
			lf := &b.loops[len(b.loops)-1]
			lf.continueHit = true
			b.emitScopeExitsDownTo(lf.scopeDepth, s.Span)
			b.terminateGoto(b.curBB, lf.continueBB, s.Span)
		}

	case *ast.ExprStmt:
		b.lowerExprValue(s.Expr)
	}
}

func (b *funcBuilder) lowerLetStmt(s *ast.LetStmt) {
	curScopeID := 0
	if len(b.scopes) > 0 {
		curScopeID = b.scopes[len(b.scopes)-1].id
	}

	if s.Init == nil {
		// 显式类型标注的未初始化声明: let x: Type / var x: Type
		sym := b.lookupDeclSym(s)
		if sym == nil {
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

	// 若 RHS 是简单变量/字段位置或元组，需先求值 RHS（产生 Copy / Move 事件）再初始化目标绑定
	b.lowerExprValue(s.Init)

	if s.Pattern != nil {
		b.registerPatternLocals(s.Pattern, true, curScopeID, s.Span)
	} else if sym := b.lookupDeclSym(s); sym != nil {
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
	symTab := b.view.GetSymbols()
	switch p := pat.(type) {
	case *ast.IdentPattern:
		if sym := symTab.NodeSymbols[p]; sym != nil {
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
		}
	case *ast.TuplePattern:
		for _, sub := range p.Elems {
			b.registerPatternLocals(sub, initNow, curScopeID, span)
		}
	case *ast.StructPattern:
		for _, fp := range p.Fields {
			if fp.Pattern != nil {
				b.registerPatternLocals(fp.Pattern, initNow, curScopeID, fp.Span)
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
	}
}

func (b *funcBuilder) lowerAssignStmt(s *ast.AssignStmt) {
	lhsExpr := s.TargetExpr
	if lhsExpr == nil && s.Target != "" {
		lhsExpr = &ast.Identifier{Name: s.Target, Span: s.Span}
	}

	lhsPlace, hasPlace := b.extractPlace(lhsExpr)
	isCompound := s.Op != "" && s.Op != "="

	// 若左值包含下标表达式，先求值索引表达式；若包含解引用，需先读取指针/引用根
	b.lowerAssignTargetSubExprs(lhsExpr, isCompound, lhsPlace, hasPlace, s.Span)

	// 复合赋值先对原值触发读访问检查，随后再求值右侧表达式
	if isCompound && hasPlace {
		b.emit(Statement{
			Kind:  OpRead,
			Place: lhsPlace,
			Type:  lhsPlace.ElemType,
			Span:  lhsExpr.GetSpan(),
		})
	}

	// 求值 RHS（若写入整个变量 x = consume(x)，先在右侧消费，再把左侧标记为已初始化！）
	b.lowerExprValue(s.Value)

	if hasPlace {
		if len(lhsPlace.Projections) == 0 && !isCompound {
			b.emit(Statement{
				Kind:  OpAssign,
				Place: lhsPlace,
				Type:  lhsPlace.RootType,
				Span:  s.Span,
			})
		} else {
			b.emit(Statement{
				Kind:  OpAssign,
				Place: lhsPlace,
				Type:  lhsPlace.ElemType,
				Span:  s.Span,
			})
		}
	}
}

func (b *funcBuilder) lowerAssignTargetSubExprs(lhs ast.Expr, isCompound bool, lhsPlace Place, hasPlace bool, span diagnostic.Span) {
	switch e := lhs.(type) {
	case *ast.IndexExpr:
		// 仅检查数组根位置已初始化并求值下标，不可把整块数组当右值 OpMove
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
		b.lowerAssignTargetSubExprs(e.Object, isCompound, lhsPlace, hasPlace, span)
	case *ast.UnaryExpr:
		if e.Op == "*" {
			// 通过 *ptr 写入仅读取 ptr 本身，不可消费 ptr（例如 &mut T 本身非 Copy）
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
	_ = isCompound
	_ = lhsPlace
	_ = hasPlace
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
		b.curBB = mergeBB
	} else {
		b.curBB = mergeBB
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

func patternUsesRef(pat ast.Pattern) bool {
	if pat == nil {
		return false
	}
	switch p := pat.(type) {
	case *ast.IdentPattern:
		return p.IsRef
	case *ast.TuplePattern:
		for _, sub := range p.Elems {
			if patternUsesRef(sub) {
				return true
			}
		}
	case *ast.StructPattern:
		for _, fp := range p.Fields {
			if patternUsesRef(fp.Pattern) {
				return true
			}
		}
	case *ast.EnumPattern:
		for _, arg := range p.Args {
			if patternUsesRef(arg) {
				return true
			}
		}
	case *ast.SlicePattern:
		for _, elem := range p.Elems {
			if patternUsesRef(elem) {
				return true
			}
		}
	}
	return false
}

func (b *funcBuilder) lowerMatchExpr(m *ast.MatchExpr) {
	usesRef := false
	for _, arm := range m.Arms {
		if patternUsesRef(arm.Pattern) {
			usesRef = true
			break
		}
	}
	if usesRef {
		if p, ok := b.extractPlace(m.Subject); ok {
			b.emit(Statement{
				Kind:  OpRead,
				Place: p,
				Type:  p.ElemType,
				Span:  m.Subject.GetSpan(),
			})
		} else {
			b.lowerExprValue(m.Subject)
		}
	} else {
		b.lowerExprValue(m.Subject)
	}
	switchBB := b.curBB
	mergeBB := b.newBlock("match.merge")

	armEntries := make([]BlockID, 0, len(m.Arms))
	allDiverged := true
	for i := range m.Arms {
		armEntries = append(armEntries, b.newBlock(fmt.Sprintf("match.arm%d", i)))
	}
	if len(armEntries) == 0 {
		b.terminateGoto(switchBB, mergeBB, m.Span)
		b.curBB = mergeBB
		return
	}
	b.terminateSwitch(switchBB, armEntries, m.Span)

	for i, arm := range m.Arms {
		b.curBB = armEntries[i]
		sid := b.pushScope(arm.Span)
		if arm.Pattern != nil {
			b.registerPatternLocals(arm.Pattern, true, sid, arm.Span)
		}
		if arm.Guard != nil {
			b.lowerExprValue(arm.Guard)
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

// lowerExprValue 按所有权与求值顺序降低任意右值表达式。
func (b *funcBuilder) lowerExprValue(expr ast.Expr) {
	if expr == nil {
		return
	}

	// 若表达式直接映射到存储位置 Place，则在此统一产生 Copy / Move 事件，禁止对父结构体重复产生整体 Move。
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

	case *ast.FieldAccessExpr:
		// 仅当基表达式为临时值（非 Place）时走到此分支
		b.lowerExprValue(e.Object)

	case *ast.IndexExpr:
		// 仅当基表达式为临时值或切片范围表达式时走到此分支
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
		// 若为 *ptr，当 ptr 为 Place 时已在上面 extractPlace 分支处理；此处仅处理非 Place 指针临时值或普通一元运算
		b.lowerExprValue(e.Expr)

	case *ast.BorrowExpr:
		// 借用表达式不消费/移动源值；在 Phase 2B.1 先检查基础目标可读/已初始化并预留 Borrow 事件
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
		// 遵循 ADR-011 批准的 receiver-first 求值策略：按方法接收者方式处理
		calSym := b.view.CallTarget(e)
		if calSym != nil && calSym.HasSelf && !calSym.SelfIsRef {
			// 值接收者 self: 按实参类型执行 Copy 或 Move
			b.lowerExprValue(e.Receiver)
		} else if p, ok := b.extractPlace(e.Receiver); ok {
			// &self / &mut self（含内建方法）：读取/借用接收者（不移动其所有权！）
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

	case *ast.MatchExpr:
		b.lowerMatchExpr(e)
	}
}

func (b *funcBuilder) lowerPlaceAddressSubExprs(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.FieldAccessExpr:
		b.lowerPlaceAddressSubExprs(e.Object)
	case *ast.IndexExpr:
		b.lowerPlaceAddressSubExprs(e.Object)
		b.lowerExprValue(e.Index)
	case *ast.UnaryExpr:
		if e.Op == "*" {
			b.lowerPlaceAddressSubExprs(e.Expr)
		}
	}
}

func (b *funcBuilder) lowerPlaceValueAccess(expr ast.Expr, place Place) {
	// 先计算路径中的动态索引及解引用前置根位置读检查
	b.lowerPlacePrereqs(expr, place)

	exprType := b.view.ExprType(expr)
	if exprType == types.Invalid {
		exprType = place.ElemType
	}

	// Copy 类型永远产生 OpCopy，不消费源绑定；非 Copy 产生 OpMove
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
	// 对路径上的下标表达式进行正常读求值
	switch e := expr.(type) {
	case *ast.IndexExpr:
		b.lowerPlacePrereqs(e.Object, place)
		b.lowerExprValue(e.Index)
	case *ast.FieldAccessExpr:
		b.lowerPlacePrereqs(e.Object, place)
	case *ast.UnaryExpr:
		if e.Op == "*" {
			// 解引用本身要求指针/引用变量本身处于已初始化/未移动状态
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
		// 若基表达式为引用/原生指针，结构体成员访问隐式附带解引用投影
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

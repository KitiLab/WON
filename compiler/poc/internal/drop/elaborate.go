package drop

import (
	"fmt"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/borrow"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// ElaborateProgram 在通过 Phase 2B.1/2B.2 验证的 CFG 之上执行全程序确定性 Drop 展开与自验证。
// 原生 ProgramCFG 保持完全只读不可变，所有 OpDrop / OpCondDrop / DropFlag / FFI 转移结果均落在独立克隆视图中。
func ElaborateProgram(progCFG *cfg.ProgramCFG, view cfg.SemanticView, reg *ResourceRegistry, report *diagnostic.Report) *DropElaboratedProgram {
	if progCFG == nil || view == nil || view.GetTypes() == nil || view.GetSymbols() == nil {
		if report != nil {
			report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-INT-001",
				Severity: "error",
				Message:  "Drop Elaboration 缺乏有效的 CFG 或语义视图上下文，按 Fail-Closed 策略拒绝认证",
			})
		}
		return &DropElaboratedProgram{DropElaborated: false}
	}
	cloned := CloneProgramCFG(progCFG)
	return elaborateStagedProgramWithFFI(cloned, view, reg, nil, report)
}

func elaborateStagedProgramWithFFI(cloned *cfg.ProgramCFG, view cfg.SemanticView, reg *ResourceRegistry, opaqueTypes map[string]bindgen.FFIReturnContract, report *diagnostic.Report) *DropElaboratedProgram {
	if cloned == nil || view == nil || view.GetTypes() == nil || view.GetSymbols() == nil {
		if report != nil {
			report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-INT-001",
				Severity: "error",
				Message:  "Drop Elaboration 缺乏有效的 CFG 或语义视图上下文，按 Fail-Closed 策略拒绝认证",
			})
		}
		return &DropElaboratedProgram{DropElaborated: false}
	}

	cc := NewClassifyContext(view.GetTypes(), view.GetSymbols(), reg)

	res := &DropElaboratedProgram{
		ElaboratedCFG:  cloned,
		Functions:      make(map[symbols.SymbolID]*DropElaboratedFunc, len(cloned.Functions)),
		GlueTable:      make(map[string]*DropGlue),
		DropElaborated: false,
	}

	allOK := true
	for _, fn := range cloned.Functions {
		if fn == nil {
			continue
		}
		trace, ok := borrow.SolveMoveInitTraceWithFFI(fn, view, opaqueTypes, report)
		if !ok || trace == nil {
			allOK = false
			continue
		}
		df, fnOK := elaborateFunc(fn, trace, cc, view, opaqueTypes, report)
		if fn.FuncSym != nil {
			res.Functions[fn.FuncSym.ID] = df
		}
		if !fnOK || !verifyElaboratedFunc(fn, df, cc, view, report) {
			allOK = false
		}
	}

	// 将分类器已解析的全部 DropGlue 收录至程序级目录，便于下游 TIR/后端查询
	for gid, g := range cc.glues {
		if g != nil {
			res.GlueTable[gid] = g
		}
	}

	if report != nil && report.HasErrors() {
		allOK = false
	}
	res.DropElaborated = allOK
	return res
}

type funcElaborator struct {
	fn          *cfg.FuncCFG
	trace       *borrow.MoveInitTrace
	cc          *ClassifyContext
	view        cfg.SemanticView
	symTab      *symbols.Table
	tctx        *types.Context
	opaqueTypes map[string]bindgen.FFIReturnContract
	report      *diagnostic.Report
	df          *DropElaboratedFunc
	flagByKey   map[string]DropFlagID
	nextFlag    uint32
	hadError    bool
}

func elaborateFunc(fn *cfg.FuncCFG, trace *borrow.MoveInitTrace, cc *ClassifyContext, view cfg.SemanticView, opaqueTypes map[string]bindgen.FFIReturnContract, report *diagnostic.Report) (*DropElaboratedFunc, bool) {
	var symID symbols.SymbolID
	if fn.FuncSym != nil {
		symID = fn.FuncSym.ID
	}
	df := &DropElaboratedFunc{
		SymID:        symID,
		Name:         fn.Name,
		CFG:          fn,
		Flags:        make(map[DropFlagID]*DropFlagMeta),
		FFIContracts: make(map[cfg.ProgramPoint]string),
	}
	el := &funcElaborator{
		fn:          fn,
		trace:       trace,
		cc:          cc,
		view:        view,
		symTab:      view.GetSymbols(),
		tctx:        view.GetTypes(),
		opaqueTypes: opaqueTypes,
		report:      report,
		df:          df,
		flagByKey:   make(map[string]DropFlagID),
	}

	// 预先判定该函数各基本块路径上是否经由 rootScope(ScopeID==1) 退出（标准函数均有 rootScope，独立构建单测可能省略）
	hasRootScopeExit := false
	for bbIdx, blk := range fn.Blocks {
		if int(bbIdx) < len(trace.Reachable) && trace.Reachable[bbIdx] && blk != nil {
			for _, st := range blk.Statements {
				if st.Kind == cfg.OpScopeExit && st.ScopeID == 1 {
					hasRootScopeExit = true
					break
				}
			}
		}
		if hasRootScopeExit {
			break
		}
	}

	ownedParams := el.collectOwnedParams()

	// 按基本块确定性顺序重写语句流：在 OpAssign/OpReinit 前释放旧资源、在 OpScopeExit 前倒序析构作用域局部变量、
	// 在根作用域退出或 TermReturn 前清理仍活着的按值传入所有权参数。
	for bbIdx, blk := range fn.Blocks {
		bb := cfg.BlockID(bbIdx)
		if int(bbIdx) >= len(trace.Reachable) || !trace.Reachable[bbIdx] || blk == nil {
			continue
		}

		env := trace.CloneInEnv(bb)
		outStmts := make([]cfg.Statement, 0, len(blk.Statements)+4)

		for _, st := range blk.Statements {
			switch st.Kind {
			case cfg.OpMove:
				el.checkPartialMoveCustomDrop(st)
				outStmts = append(outStmts, st)

			case cfg.OpAssign, cfg.OpReinit:
				if st.Kind == cfg.OpAssign {
					replaces := el.elaborateReassignmentDrop(st, env)
					outStmts = append(outStmts, replaces...)
				}
				outStmts = append(outStmts, st)

			case cfg.OpScopeExit:
				scopeDrops := el.elaborateScopeExitDrops(st, env)
				outStmts = append(outStmts, scopeDrops...)
				outStmts = append(outStmts, st)

			default:
				outStmts = append(outStmts, st)
			}

			borrow.StepStatementWithFFI(fn.Name, env, st, view, el.opaqueTypes)

			// 标准函数体在 ScopeID==1 的 OpScopeExit 后、TermReturn / goto exit 前清理按值参数（确保体部局部变量先于参数析构）
			if st.Kind == cfg.OpScopeExit && st.ScopeID == 1 && len(ownedParams) > 0 {
				paramDrops := el.elaborateParamDrops(ownedParams, env, st.Span)
				outStmts = append(outStmts, paramDrops...)
			}
		}

		// 对于未经过 ScopeID==1 的合成 CFG，在含返回终结器的可达块末尾清理仍活着的按值参数
		if !hasRootScopeExit && blk.Terminator.Kind == cfg.TermReturn && len(ownedParams) > 0 {
			paramDrops := el.elaborateParamDrops(ownedParams, env, blk.Terminator.Span)
			outStmts = append(outStmts, paramDrops...)
		}

		blk.Statements = outStmts
	}

	el.emitDropFlagEvents()
	return df, !el.hadError
}

func (el *funcElaborator) collectOwnedParams() []symbols.SymbolID {
	params := make([]symbols.SymbolID, 0)
	for _, sid := range el.fn.LocalOrder {
		meta := el.fn.Locals[sid]
		if meta != nil && meta.IsParam && meta.ScopeID == 0 && el.cc.NeedsDrop(meta.Type) {
			params = append(params, sid)
		}
	}
	return params
}

func (el *funcElaborator) checkPartialMoveCustomDrop(st cfg.Statement) {
	fields, ok := st.Place.FieldPath()
	if !ok || len(fields) == 0 || st.Place.RootType == types.Invalid {
		return
	}
	curType := st.Place.RootType
	if el.cc.HasCustomDrop(curType) {
		el.emitErr("WON-MEM-007", st.Span,
			fmt.Sprintf("禁止对带有自定义 Drop 契约的类型 '%s' 执行部分字段移动 ('%s')", el.tctx.Format(curType), st.Place.String()),
			"带自定义 Drop 语义的类型要求在析构时读取完整的结构体视图，因此不能部分移出其成员所有权。")
		return
	}
	for _, fName := range fields[:len(fields)-1] {
		nextType := resolveFieldType(curType, fName, el.symTab, el.tctx)
		if nextType == types.Invalid {
			break
		}
		curType = nextType
		if el.cc.HasCustomDrop(curType) {
			el.emitErr("WON-MEM-007", st.Span,
				fmt.Sprintf("禁止从嵌套自定义 Drop 类型字段 '%s' 中执行部分字段移动", el.tctx.Format(curType)),
				"带自定义 Drop 语义的内部成员不可在宿主保持部分存活期间拆散所有权。")
			return
		}
	}
}

func (el *funcElaborator) elaborateReassignmentDrop(st cfg.Statement, env map[symbols.SymbolID]*borrow.PlaceState) []cfg.Statement {
	if st.Place.RootSymID == 0 {
		return nil
	}
	if !el.cc.NeedsDrop(st.Place.ElemType) {
		return nil
	}
	// 通过可变引用或切片/数组下标覆写元素时，旧元素视为在位有效值直接触发替换析构
	if st.Place.HasDeref() || st.Place.HasIndex() {
		glue, need := el.cc.ResolveDropGlue(st.Place.ElemType)
		if !need || glue == nil {
			return nil
		}
		return []cfg.Statement{{
			Kind:     cfg.OpDrop,
			Place:    st.Place,
			Type:     st.Place.ElemType,
			DropGlue: glue.ID,
			Span:     st.Span,
		}}
	}

	rootState := env[st.Place.RootSymID]
	if rootState == nil {
		return nil
	}
	fields, ok := st.Place.FieldPath()
	if !ok {
		return nil
	}
	targetState, err := resolveStatePathReadOnly(rootState, fields, el.symTab, el.tctx)
	if err != nil || targetState == nil {
		el.emitErr("WON-INT-001", st.Span, fmt.Sprintf("重赋值替换析构无法解析目标状态 '%s': %v", st.Place.String(), err), "")
		return nil
	}
	return el.emitCleanupForState(st.Place, targetState, st.Span)
}

func (el *funcElaborator) elaborateScopeExitDrops(st cfg.Statement, env map[symbols.SymbolID]*borrow.PlaceState) []cfg.Statement {
	var drops []cfg.Statement
	// 严格按声明逆序 (LIFO) 销毁作用域中的局部变量
	for i := len(st.Locals) - 1; i >= 0; i-- {
		sid := st.Locals[i]
		meta := el.fn.Locals[sid]
		if meta == nil || !el.cc.NeedsDrop(meta.Type) {
			continue
		}
		ps := env[sid]
		if ps == nil {
			el.emitErr("WON-INT-001", st.Span, fmt.Sprintf("作用域退出时缺少资源局部变量 '#%d' 的所有权追踪状态", sid), "")
			continue
		}
		p := placeFromLocalMeta(sid, meta, st.Span)
		drops = append(drops, el.emitCleanupForState(p, ps, st.Span)...)
	}
	return drops
}

func (el *funcElaborator) elaborateParamDrops(params []symbols.SymbolID, env map[symbols.SymbolID]*borrow.PlaceState, span diagnostic.Span) []cfg.Statement {
	var drops []cfg.Statement
	// 严格按参数声明逆序释放仍在函数尾部持有所有权的形参资源
	for i := len(params) - 1; i >= 0; i-- {
		sid := params[i]
		meta := el.fn.Locals[sid]
		if meta == nil || !el.cc.NeedsDrop(meta.Type) {
			continue
		}
		ps := env[sid]
		if ps == nil {
			continue
		}
		p := placeFromLocalMeta(sid, meta, span)
		drops = append(drops, el.emitCleanupForState(p, ps, span)...)
		delete(env, sid)
	}
	return drops
}

func (el *funcElaborator) emitCleanupForState(place cfg.Place, ps *borrow.PlaceState, span diagnostic.Span) []cfg.Statement {
	if ps == nil {
		return nil
	}
	tid := ps.Type
	if tid == types.Invalid {
		tid = place.ElemType
	}
	if !el.cc.NeedsDrop(tid) {
		return nil
	}

	canUninit, canInit, canOwned, canMoved, canNonOwningCopy, _, _ := ps.SummaryOwnership()
	if !canInit || !canOwned {
		// 确定未初始化、确定已移动或非拥有裸指针副本：严禁发出任何析构调用，也绝不分配冗余 DropFlag
		return nil
	}

	info := el.tctx.Info(tid)
	if len(ps.Fields) > 0 {
		if info.Kind == types.KindEnum {
			el.emitErr("WON-INT-001", span, fmt.Sprintf("枚举类型 '%s' 出现非法按字段拆分状态", el.tctx.Format(tid)), "")
			return nil
		}
		if info.Kind == types.KindStruct {
			// 若各已展开字段全部一致处于确定 Init 拥有状态，整体发出单一 OpDrop，避免无意义碎裂
			if !canUninit && !canMoved && !canNonOwningCopy {
				glue, need := el.cc.ResolveDropGlue(tid)
				if need && glue != nil {
					return []cfg.Statement{{
						Kind:     cfg.OpDrop,
						Place:    place,
						Type:     tid,
						DropGlue: glue.ID,
						Span:     span,
					}}
				}
				return nil
			}
			// 存在部分字段移出/可能部分初始化，校验 custom Drop 并按声明序逐字段精准析构
			if el.cc.HasCustomDrop(tid) {
				el.emitErr("WON-MEM-007", span,
					fmt.Sprintf("禁止对带自定义 Drop 语义的类型 '%s' 在部分字段已移动状态下执行析构", el.tctx.Format(tid)),
					"实现自定义 Drop 的类型不允许部分移动；必须整体转移或完整保持存活。")
				return nil
			}
			sym := el.symTab.ByID[symbols.SymbolID(info.SymbolID)]
			if sym == nil {
				el.emitErr("WON-INT-001", span, fmt.Sprintf("结构化部分析构找不到类型 '%s' 的符号定义", el.tctx.Format(tid)), "")
				return nil
			}
			subst := buildStructSubst(sym, info)
			var stmts []cfg.Statement
			for idx, f := range sym.Fields {
				ft := f.Type
				if subst != nil {
					ft = el.tctx.Substitute(ft, subst)
				}
				fs := ps.Fields[f.Name]
				if fs == nil {
					fs = &borrow.PlaceState{
						CanBeUninit: ps.CanBeUninit,
						CanBeInit:   ps.CanBeInit,
						CanBeMoved:  ps.CanBeMoved,
						DeclSpan:    ps.DeclSpan,
						Type:        ft,
					}
				}
				fPlace := place.AppendProj(cfg.Projection{
					Kind:       cfg.ProjField,
					FieldName:  f.Name,
					FieldIndex: idx,
					ElemType:   ft,
				}, ft, span)
				stmts = append(stmts, el.emitCleanupForState(fPlace, fs, span)...)
			}
			return stmts
		}
	}

	glue, need := el.cc.ResolveDropGlue(tid)
	if !need || glue == nil {
		return nil
	}

	if canOwned && !canUninit && !canMoved && !canNonOwningCopy {
		return []cfg.Statement{{
			Kind:     cfg.OpDrop,
			Place:    place,
			Type:     tid,
			DropGlue: glue.ID,
			Span:     span,
		}}
	}

	fid := el.ensureDropFlag(place, tid, glue.ID, span)
	return []cfg.Statement{{
		Kind:     cfg.OpCondDrop,
		Place:    place,
		Type:     tid,
		DropFlag: uint32(fid),
		DropGlue: glue.ID,
		Span:     span,
	}}
}

func (el *funcElaborator) ensureDropFlag(place cfg.Place, tid types.TypeID, glueID string, span diagnostic.Span) DropFlagID {
	key := canonicalPlaceKey(place)
	if existing, ok := el.flagByKey[key]; ok {
		return existing
	}
	el.nextFlag++
	fid := DropFlagID(el.nextFlag)
	el.flagByKey[key] = fid
	meta := &DropFlagMeta{
		ID:         fid,
		OwnerSymID: place.RootSymID,
		Place:      place,
		Type:       tid,
		GlueID:     glueID,
		InitialVal: false,
		IsField:    len(place.Projections) > 0,
		Span:       span,
	}
	el.df.Flags[fid] = meta
	el.df.FlagOrder = append(el.df.FlagOrder, fid)
	return fid
}

func (el *funcElaborator) emitDropFlagEvents() {
	if len(el.df.FlagOrder) == 0 {
		return
	}
	var events []DropFlagEvent
	// 1. 函数入口声明全部必要的条件 Drop Flag（默认 false）
	for _, fid := range el.df.FlagOrder {
		fm := el.df.Flags[fid]
		events = append(events, DropFlagEvent{
			Point: cfg.ProgramPoint{Block: el.fn.Entry, StmtIdx: 0},
			Flag:  fid,
			Op:    FlagDecl,
			Place: fm.Place,
			Span:  fm.Span,
		})
	}

	// 2. 扫描各可达块最终指令流，为涉及的标志生成 FlagSetTrue / FlagSetFalse / FlagCheck 事件
	for bbIdx, blk := range el.fn.Blocks {
		if int(bbIdx) >= len(el.trace.Reachable) || !el.trace.Reachable[bbIdx] || blk == nil {
			continue
		}
		for sIdx, st := range blk.Statements {
			pt := cfg.ProgramPoint{Block: blk.ID, StmtIdx: sIdx}
			switch st.Kind {
			case cfg.OpInitAssign, cfg.OpStorageDecl, cfg.OpAssign, cfg.OpReinit:
				flagOp := FlagSetTrue
				if st.CopyPlace.RootSymID != 0 && (borrow.IsTrackableRawCopy(st.Type, st.CopyPlace, el.tctx, el.symTab, el.opaqueTypes) || el.cc.NeedsDrop(st.Type)) {
					flagOp = FlagSetFalse
				}
				for _, fid := range el.df.FlagOrder {
					fm := el.df.Flags[fid]
					if placeCoversFlag(st.Place, fm.Place) {
						events = append(events, DropFlagEvent{
							Point: pt,
							Flag:  fid,
							Op:    flagOp,
							Place: fm.Place,
							Span:  st.Span,
						})
					}
				}
			case cfg.OpMove, cfg.OpFFITransfer:
				for _, fid := range el.df.FlagOrder {
					fm := el.df.Flags[fid]
					if placeCoversFlag(st.Place, fm.Place) {
						events = append(events, DropFlagEvent{
							Point: pt,
							Flag:  fid,
							Op:    FlagSetFalse,
							Place: fm.Place,
							Span:  st.Span,
						})
					}
				}
			case cfg.OpCondDrop:
				fid := DropFlagID(st.DropFlag)
				if fm := el.df.Flags[fid]; fm != nil {
					events = append(events,
						DropFlagEvent{Point: pt, Flag: fid, Op: FlagCheck, Place: fm.Place, Span: st.Span},
						DropFlagEvent{Point: pt, Flag: fid, Op: FlagSetFalse, Place: fm.Place, Span: st.Span},
					)
				}
			case cfg.OpDrop:
				for _, fid := range el.df.FlagOrder {
					fm := el.df.Flags[fid]
					if placeCoversFlag(st.Place, fm.Place) {
						events = append(events, DropFlagEvent{
							Point: pt,
							Flag:  fid,
							Op:    FlagSetFalse,
							Place: fm.Place,
							Span:  st.Span,
						})
					}
				}
			}
		}
	}
	el.df.FlagEvents = events
}

func (el *funcElaborator) emitErr(code string, span diagnostic.Span, msg, exp string) {
	el.hadError = true
	if el.report != nil {
		el.report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        code,
			Severity:    "error",
			Message:     msg,
			File:        span.File,
			Span:        span,
			Explanation: exp,
		})
	}
}

func canonicalPlaceKey(p cfg.Place) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("sym#%d", p.RootSymID))
	for _, proj := range p.Projections {
		switch proj.Kind {
		case cfg.ProjField:
			sb.WriteByte('.')
			sb.WriteString(proj.FieldName)
		case cfg.ProjTupleField:
			sb.WriteString(fmt.Sprintf(".%d", proj.FieldIndex))
		case cfg.ProjDeref:
			sb.WriteString(".*")
		case cfg.ProjConstIdx:
			sb.WriteString(fmt.Sprintf("[%d]", proj.ConstIndex))
		case cfg.ProjIndex:
			sb.WriteString("[?]")
		case cfg.ProjDowncast:
			sb.WriteString(fmt.Sprintf("@v%d", proj.VariantIdx))
		}
	}
	return sb.String()
}

func placeCoversFlag(stmtPlace, flagPlace cfg.Place) bool {
	if stmtPlace.RootSymID == 0 || stmtPlace.RootSymID != flagPlace.RootSymID {
		return false
	}
	if len(stmtPlace.Projections) > len(flagPlace.Projections) {
		return false
	}
	for i, sp := range stmtPlace.Projections {
		fp := flagPlace.Projections[i]
		if sp.Kind != fp.Kind {
			return false
		}
		switch sp.Kind {
		case cfg.ProjField:
			if sp.FieldName != fp.FieldName {
				return false
			}
		case cfg.ProjTupleField:
			if sp.FieldIndex != fp.FieldIndex {
				return false
			}
		case cfg.ProjConstIdx:
			if sp.ConstIndex != fp.ConstIndex {
				return false
			}
		}
	}
	return true
}

func placeFromLocalMeta(sid symbols.SymbolID, meta *cfg.LocalMeta, span diagnostic.Span) cfg.Place {
	name := fmt.Sprintf("#%d", sid)
	isParam := false
	isMut := false
	var typ types.TypeID
	if meta != nil {
		typ = meta.Type
		isParam = meta.IsParam
		isMut = meta.IsMut
		if meta.Sym != nil && meta.Sym.Name != "" {
			name = meta.Sym.Name
		}
	}
	return cfg.Place{
		RootSymID: sid,
		RootName:  name,
		RootType:  typ,
		ElemType:  typ,
		RootMut:   isMut,
		IsParam:   isParam,
		Span:      span,
	}
}

func resolveFieldType(structType types.TypeID, fieldName string, symTab *symbols.Table, tctx *types.Context) types.TypeID {
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
	if subst := buildStructSubst(sym, info); subst != nil {
		return tctx.Substitute(fi.Type, subst)
	}
	return fi.Type
}

func buildStructSubst(sym *symbols.Symbol, info types.TypeInfo) map[uint64]types.TypeID {
	if sym == nil || len(sym.GenericParamIDs) == 0 || len(sym.GenericParamIDs) != len(info.GenericArgs) {
		return nil
	}
	subst := make(map[uint64]types.TypeID, len(info.GenericArgs))
	for i, gid := range sym.GenericParamIDs {
		subst[uint64(gid)] = info.GenericArgs[i]
	}
	return subst
}

// resolveStatePathReadOnly 沿着字段路径查询只读字段状态（必要时生成未展开父节点的只读视图），绝不就地篡改原 env。
func resolveStatePathReadOnly(root *borrow.PlaceState, fields []string, symTab *symbols.Table, tctx *types.Context) (*borrow.PlaceState, error) {
	cur := root
	for _, fName := range fields {
		if cur == nil {
			return nil, fmt.Errorf("字段路径 '%s' 遇到空状态节点", fName)
		}
		if cur.Fields != nil {
			next, ok := cur.Fields[fName]
			if !ok || next == nil {
				return nil, fmt.Errorf("结构体 '%s' 状态中缺失字段 '%s'", tctx.Format(cur.Type), fName)
			}
			cur = next
			continue
		}
		ft := resolveFieldType(cur.Type, fName, symTab, tctx)
		if ft == types.Invalid {
			return nil, fmt.Errorf("结构体 '%s' 不存在字段 '%s'", tctx.Format(cur.Type), fName)
		}
		cur = &borrow.PlaceState{
			CanBeUninit: cur.CanBeUninit,
			CanBeInit:   cur.CanBeInit,
			CanBeMoved:  cur.CanBeMoved,
			MoveSpans:   append([]diagnostic.Span(nil), cur.MoveSpans...),
			DeclSpan:    cur.DeclSpan,
			Type:        ft,
		}
	}
	return cur, nil
}

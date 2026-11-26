package borrow

import (
	"fmt"
	"sort"

	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// NLLState 表示单个控制流点上“向后可达未来仍可能被读取”的引用临时值 (ValueID) 与载体存储位置 (Place.Key)。
type NLLState struct {
	Temps  map[cfg.ValueID]bool
	Places map[string]bool
}

func newNLLState() NLLState {
	return NLLState{
		Temps:  make(map[cfg.ValueID]bool),
		Places: make(map[string]bool),
	}
}

func (s NLLState) clone() NLLState {
	cp := NLLState{
		Temps:  make(map[cfg.ValueID]bool, len(s.Temps)),
		Places: make(map[string]bool, len(s.Places)),
	}
	for t := range s.Temps {
		cp.Temps[t] = true
	}
	for p := range s.Places {
		cp.Places[p] = true
	}
	return cp
}

func (s *NLLState) union(other NLLState) {
	if s.Temps == nil {
		s.Temps = make(map[cfg.ValueID]bool)
	}
	if s.Places == nil {
		s.Places = make(map[string]bool)
	}
	for t := range other.Temps {
		s.Temps[t] = true
	}
	for p := range other.Places {
		s.Places[p] = true
	}
}

func (s NLLState) equals(other NLLState) bool {
	if len(s.Temps) != len(other.Temps) || len(s.Places) != len(other.Places) {
		return false
	}
	for t := range s.Temps {
		if !other.Temps[t] {
			return false
		}
	}
	for p := range s.Places {
		if !other.Places[p] {
			return false
		}
	}
	return true
}

// NLLResult 保存向后非词法活跃域求解结果及每个语句执行点上的精确活跃借用集合，便于诊断与单元测试断言。
type NLLResult struct {
	BlockLiveIn     []NLLState
	BlockLiveOut    []NLLState
	PointLiveLoans  map[cfg.ProgramPoint][]cfg.LoanID
	LoanActiveAtPts map[cfg.LoanID][]cfg.ProgramPoint
}

// AnalyzeBorrowConflicts 执行 Stage B：向后 NLL 活跃域数据流求解、XOR 借用冲突检测、重借用冻结检查及作用域生命周期结束校验。
func AnalyzeBorrowConflicts(fn *cfg.FuncCFG, flow *FuncLoanFlow, view cfg.SemanticView, report *diagnostic.Report) *NLLResult {
	if fn == nil || flow == nil || len(fn.Blocks) == 0 || fn.Entry < 0 {
		return nil
	}

	reachable := computeReachable(fn)
	liveIn := make([]NLLState, len(fn.Blocks))
	liveOut := make([]NLLState, len(fn.Blocks))
	for i := range fn.Blocks {
		liveIn[i] = newNLLState()
		liveOut[i] = newNLLState()
	}

	// 1. 在可达控制流子图上执行确定性向后活跃域不动点迭代
	maxIterations := len(fn.Blocks)*(len(fn.Locals)*4+32) + 256
	changed := true
	iters := 0
	for changed {
		iters++
		if iters > maxIterations {
			var sp diagnostic.Span
			if fn.Decl != nil {
				sp = fn.Decl.GetSpan()
			}
			emitFlowViolation(fn, report, sp, fmt.Sprintf("NLL 向后活跃域不动点迭代在 %d 轮后仍未收敛", maxIterations))
			return nil
		}
		changed = false
		// 逆序遍历基本块加速向后收敛，结果仍完全不依赖块序列
		for bbIdx := len(fn.Blocks) - 1; bbIdx >= 0; bbIdx-- {
			bb := cfg.BlockID(bbIdx)
			if !reachable[bb] {
				continue
			}
			blk := fn.Blocks[bbIdx]
			out := newNLLState()
			for _, succ := range blk.Successors {
				if reachable[succ] {
					out.union(liveIn[succ])
				}
			}
			liveOut[bb] = out
			in := out.clone()
			for stIdx := len(blk.Statements) - 1; stIdx >= 0; stIdx-- {
				backwardTransferStmt(&in, blk.Statements[stIdx])
			}
			if !liveIn[bb].equals(in) {
				liveIn[bb] = in
				changed = true
			}
		}
	}

	res := &NLLResult{
		BlockLiveIn:     liveIn,
		BlockLiveOut:    liveOut,
		PointLiveLoans:  make(map[cfg.ProgramPoint][]cfg.LoanID),
		LoanActiveAtPts: make(map[cfg.LoanID][]cfg.ProgramPoint),
	}

	emitted := make(map[diagDedupKey]bool)
	var tctx *types.Context
	var symTab *symbols.Table
	if view != nil {
		tctx = view.GetTypes()
		symTab = view.GetSymbols()
	}

	// 2. 沿前向载具流 + 向后活跃域逐块逐语句检验借用排他性、作用域生命周期与重借用冻结不变量
	for bbIdx, blk := range fn.Blocks {
		bb := cfg.BlockID(bbIdx)
		if !reachable[bb] {
			continue
		}

		// 重建每条语句前后的精确前向载体环境，并与向后活跃集取交集得到真实活跃 Loan
		fwd := flow.InStates[bb].clone()
		n := len(blk.Statements)
		afterNLL := make([]NLLState, n)
		curBack := liveOut[bb].clone()
		for i := n - 1; i >= 0; i-- {
			afterNLL[i] = curBack.clone()
			backwardTransferStmt(&curBack, blk.Statements[i])
		}

		for stIdx, st := range blk.Statements {
			pt := cfg.ProgramPoint{Block: bb, StmtIdx: stIdx}
			liveAfter := afterNLL[stIdx]
			activeAtEntry := resolveActiveLoans(fwd, liveAfter)

			checkStmtBorrowConflicts(fn, blk, stIdx, st, fwd, liveAfter, activeAtEntry, flow, tctx, symTab, report, emitted)

			// 按前向规则推进 fwd 至语句执行后状态：记录语句结束后的精确 NLL 存活借用集合，并检查作用域退出生命期
			stepSingleCarrierStmt(fn, st, fwd, flow, view)
			activePostStmt := resolveActiveLoans(fwd, liveAfter)
			sortedActive := sortedLoanIDs(activePostStmt)
			res.PointLiveLoans[pt] = sortedActive
			for _, lid := range sortedActive {
				res.LoanActiveAtPts[lid] = append(res.LoanActiveAtPts[lid], pt)
			}

			if st.Kind == cfg.OpScopeExit || st.Kind == cfg.OpStorageDead {
				checkScopeExitLoans(st, st.Locals, activePostStmt, flow, symTab, report, emitted)
			}
		}
	}

	return res
}

func backwardTransferStmt(s *NLLState, st cfg.Statement) {
	switch st.Kind {
	case cfg.OpScopeExit, cfg.OpStorageDead:
		// 离开作用域后局部变量槽位不可再向后读取；但若其所指外层借用仍被后续语句读取，由调用者载体延续其存活
		for _, sid := range st.Locals {
			clearNLLSymPlaces(s, sid)
		}

	case cfg.OpUninitDecl:
		clearNLLPlacePrefix(s, st.Place)

	case cfg.OpInitAssign, cfg.OpAssign, cfg.OpReinit:
		// 完全写入覆盖目标槽位，杀死先前的活跃依赖（若是局部重赋值如 r = &y，旧 loan 在此精确终结）
		delete(s.Places, st.Place.Key())
		if len(st.Place.Projections) == 0 {
			clearNLLPlacePrefix(s, st.Place)
		} else if st.Place.HasDeref() {
			// 通过 *r = val 写入必须读取载体 r 本身
			if carrier, hasD := st.Place.PrefixBeforeLastDeref(); hasD {
				markPlaceLive(s, carrier)
			}
		}
		if st.SrcTemp != cfg.InvalidValue {
			s.Temps[st.SrcTemp] = true
		}

	case cfg.OpTempAssign:
		delete(s.Temps, st.ValTemp)
		if st.SrcTemp != cfg.InvalidValue {
			s.Temps[st.SrcTemp] = true
		}

	case cfg.OpStaticRef:
		delete(s.Temps, st.ValTemp)

	case cfg.OpAggregate:
		delete(s.Temps, st.ValTemp)
		for _, item := range st.AggTemps {
			if item.Temp != cfg.InvalidValue {
				s.Temps[item.Temp] = true
			}
		}

	case cfg.OpCall:
		if st.ValTemp != cfg.InvalidValue {
			delete(s.Temps, st.ValTemp)
		}
		for _, argT := range st.ArgTemps {
			if argT != cfg.InvalidValue {
				s.Temps[argT] = true
			}
		}

	case cfg.OpReturn:
		if st.SrcTemp != cfg.InvalidValue {
			s.Temps[st.SrcTemp] = true
		}
		if st.Place.RootSymID != 0 {
			markPlaceLive(s, st.Place)
		}

	case cfg.OpBorrowShared, cfg.OpBorrowMut, cfg.OpReborrow:
		if st.ValTemp != cfg.InvalidValue {
			delete(s.Temps, st.ValTemp)
		}
		if st.IsReborrow && st.ParentPlace.RootSymID != 0 {
			markPlaceLive(s, st.ParentPlace)
		}

	case cfg.OpRead, cfg.OpCopy, cfg.OpMove:
		if st.ValTemp != cfg.InvalidValue {
			delete(s.Temps, st.ValTemp)
		}
		if st.Place.RootSymID != 0 {
			// 解引用或读取载体都会保活该载体的前缀引用
			if carrier, hasD := st.Place.PrefixBeforeLastDeref(); hasD {
				markPlaceLive(s, carrier)
			} else {
				markPlaceLive(s, st.Place)
			}
		}
	}
}

func markPlaceLive(s *NLLState, p cfg.Place) {
	if p.RootSymID == 0 {
		return
	}
	s.Places[p.Key()] = true
}

func clearNLLPlacePrefix(s *NLLState, p cfg.Place) {
	key := p.Key()
	delete(s.Places, key)
	for k := range s.Places {
		if len(k) > len(key) && k[:len(key)] == key && (k[len(key)] == '.' || k[len(key)] == '[') {
			delete(s.Places, k)
		}
	}
}

func clearNLLSymPlaces(s *NLLState, sid symbols.SymbolID) {
	if sid == 0 {
		return
	}
	tag := fmt.Sprintf("#%d", sid)
	for k := range s.Places {
		if keyMatchesSymTag(k, tag) {
			delete(s.Places, k)
		}
	}
}

func keyMatchesSymTag(k, tag string) bool {
	if len(k) < len(tag) {
		return false
	}
	for i := 0; i <= len(k)-len(tag); i++ {
		if k[i:i+len(tag)] == tag && (i+len(tag) == len(k) || k[i+len(tag)] == '.' || k[i+len(tag)] == '[') {
			return true
		}
	}
	return false
}

func resolveActiveLoans(fwd *carrierEnv, live NLLState) map[cfg.LoanID]bool {
	active := make(map[cfg.LoanID]bool)
	if fwd == nil {
		return active
	}
	for tid := range live.Temps {
		if orig, ok := fwd.temps[tid]; ok {
			for lid := range orig.Loans {
				active[lid] = true
			}
		}
	}
	for pKey := range live.Places {
		if orig, ok := fwd.places[pKey]; ok {
			for lid := range orig.Loans {
				active[lid] = true
			}
		}
		// 若活跃位置为父结构体本身，聚合其字段借用；若活跃位置是字段但载体只记录于根，亦保守合并
		for k, orig := range fwd.places {
			if len(k) > len(pKey) && k[:len(pKey)] == pKey && (k[len(pKey)] == '.' || k[len(pKey)] == '[') {
				for lid := range orig.Loans {
					active[lid] = true
				}
			}
		}
	}
	return active
}

func sortedLoanIDs(m map[cfg.LoanID]bool) []cfg.LoanID {
	out := make([]cfg.LoanID, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func stepSingleCarrierStmt(fn *cfg.FuncCFG, st cfg.Statement, fwd *carrierEnv, flow *FuncLoanFlow, view cfg.SemanticView) {
	blk := &cfg.BasicBlock{Statements: []cfg.Statement{st}}
	transferCarrierBlock(fn, blk, fwd, flow, view, false, nil)
}

// resolvedTarget 表示重借用穿透之后所指代的底层所有者存储位置集合。
type resolvedTarget struct {
	place    cfg.Place
	paramIdx int  // >=0 表示源于形参 Param(paramIdx)
	unknown  bool // 源于裸指针或未知外部来源
}

func resolveLoanUnderlyingTargets(flow *FuncLoanFlow, lid cfg.LoanID, visited map[cfg.LoanID]bool) []resolvedTarget {
	if visited == nil {
		visited = make(map[cfg.LoanID]bool)
	}
	if visited[lid] {
		return nil
	}
	visited[lid] = true
	li := flow.LoanByID[lid]
	if li == nil {
		return nil
	}
	if li.FromRawPtr {
		return []resolvedTarget{{place: li.Place, paramIdx: -1, unknown: true}}
	}
	if !li.IsReborrow {
		return []resolvedTarget{{place: li.Place, paramIdx: -1}}
	}

	suffix := suffixAfterLastDeref(li.Place)
	var out []resolvedTarget
	for pid := range li.ParentLoans {
		parentTargets := resolveLoanUnderlyingTargets(flow, pid, visited)
		for _, pt := range parentTargets {
			if pt.unknown {
				out = append(out, resolvedTarget{place: li.Place, paramIdx: -1, unknown: true})
				continue
			}
			combined := appendProjections(pt.place, suffix)
			out = append(out, resolvedTarget{place: combined, paramIdx: pt.paramIdx})
		}
	}
	for pIdx := range li.ParentParams {
		root := cfg.Place{
			RootSymID:   li.ParentPlace.RootSymID,
			RootName:    li.ParentPlace.RootName,
			Projections: suffix,
		}
		out = append(out, resolvedTarget{place: root, paramIdx: pIdx})
	}
	if len(out) == 0 {
		return []resolvedTarget{{place: li.Place, paramIdx: -1}}
	}
	return out
}

func suffixAfterLastDeref(p cfg.Place) []cfg.Projection {
	lastDeref := -1
	for i, proj := range p.Projections {
		if proj.Kind == cfg.ProjDeref {
			lastDeref = i
		}
	}
	if lastDeref < 0 || lastDeref+1 >= len(p.Projections) {
		return nil
	}
	cp := make([]cfg.Projection, len(p.Projections)-(lastDeref+1))
	copy(cp, p.Projections[lastDeref+1:])
	return cp
}

func appendProjections(base cfg.Place, suffix []cfg.Projection) cfg.Place {
	if len(suffix) == 0 {
		return base
	}
	projs := make([]cfg.Projection, len(base.Projections)+len(suffix))
	copy(projs, base.Projections)
	copy(projs[len(base.Projections):], suffix)
	res := base
	res.Projections = projs
	return res
}

func resolvePlaceUnderlyingTargets(fwd *carrierEnv, flow *FuncLoanFlow, p cfg.Place) []resolvedTarget {
	if pref, hasD := p.PrefixBeforeLastDeref(); hasD {
		suffix := suffixAfterLastDeref(p)
		orig := lookupPlaceOrigins(fwd, pref)
		if orig.Unknown {
			return []resolvedTarget{{place: p, paramIdx: -1, unknown: true}}
		}
		var out []resolvedTarget
		for lid := range orig.Loans {
			for _, t := range resolveLoanUnderlyingTargets(flow, lid, nil) {
				out = append(out, resolvedTarget{place: appendProjections(t.place, suffix), paramIdx: t.paramIdx, unknown: t.unknown})
			}
		}
		for pIdx := range orig.Params {
			out = append(out, resolvedTarget{place: cfg.Place{RootSymID: pref.RootSymID, RootName: pref.RootName, Projections: suffix}, paramIdx: pIdx})
		}
		if len(out) > 0 {
			return out
		}
	}
	return []resolvedTarget{{place: p, paramIdx: -1}}
}

func targetsOverlap(a, b []resolvedTarget) bool {
	for _, ta := range a {
		for _, tb := range b {
			if ta.unknown || tb.unknown {
				return true
			}
			if ta.paramIdx >= 0 && tb.paramIdx >= 0 {
				if ta.paramIdx == tb.paramIdx {
					pa := cfg.Place{RootSymID: symbols.SymbolID(ta.paramIdx + 1), Projections: ta.place.Projections}
					pb := cfg.Place{RootSymID: symbols.SymbolID(tb.paramIdx + 1), Projections: tb.place.Projections}
					if pa.Overlaps(pb) {
						return true
					}
				}
				continue
			}
			if ta.place.Overlaps(tb.place) {
				return true
			}
		}
	}
	return false
}

func isAncestorLoan(flow *FuncLoanFlow, candidateAnc, child cfg.LoanID, visited map[cfg.LoanID]bool) bool {
	if candidateAnc == child {
		return false
	}
	if visited == nil {
		visited = make(map[cfg.LoanID]bool)
	}
	if visited[child] {
		return false
	}
	visited[child] = true
	li := flow.LoanByID[child]
	if li == nil || !li.IsReborrow {
		return false
	}
	if li.ParentLoans[candidateAnc] {
		return true
	}
	for pid := range li.ParentLoans {
		if isAncestorLoan(flow, candidateAnc, pid, visited) {
			return true
		}
	}
	return false
}

// isAuthorizedThroughCarrier 判断当前经由载体 carrierOrig 发起的访问是否对已激活借用 activeID 具备合法授权。
// 当 activeID 为当前载体所持借用或其严格祖先借用时授权成立；若 activeID 为当前载体的子孙重借用，则父载体正被冻结，返回 false。
func isAuthorizedThroughCarrier(flow *FuncLoanFlow, carrierOrig OriginSet, activeID cfg.LoanID) bool {
	if carrierOrig.isEmpty() {
		return false
	}
	if carrierOrig.Loans[activeID] {
		return true
	}
	for cid := range carrierOrig.Loans {
		if isAncestorLoan(flow, activeID, cid, nil) {
			return true
		}
	}
	return false
}

// hasMutableFrozenAncestor 检查子借用 childID 在通向 carrierOrig 的路径上是否存在被冻结的可变祖先借用。
func hasMutableFrozenAncestor(flow *FuncLoanFlow, carrierOrig OriginSet, childID cfg.LoanID) bool {
	for cid := range carrierOrig.Loans {
		if isAncestorLoan(flow, cid, childID, nil) {
			if li := flow.LoanByID[cid]; li != nil && li.IsMut {
				return true
			}
		}
	}
	return false
}

func checkStmtBorrowConflicts(
	fn *cfg.FuncCFG,
	blk *cfg.BasicBlock,
	stIdx int,
	st cfg.Statement,
	fwd *carrierEnv,
	liveAfter NLLState,
	activeAfter map[cfg.LoanID]bool,
	flow *FuncLoanFlow,
	tctx *types.Context,
	symTab *symbols.Table,
	report *diagnostic.Report,
	emitted map[diagDedupKey]bool,
) {
	// 1. 先检查不依赖本地活跃 Loan 集合的权限不变量：禁止从共享不可变引用 (&T) 重借用或解引用写入 &mut
	switch st.Kind {
	case cfg.OpBorrowShared, cfg.OpBorrowMut, cfg.OpReborrow:
		if selfLoan := flow.LoanByID[st.LoanID]; selfLoan != nil && selfLoan.IsMut && selfLoan.IsReborrow {
			if !hasMutableReborrowAuthority(fn, flow, fwd, selfLoan, tctx, symTab) {
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-MEM-004",
					Severity:    "error",
					Message:     fmt.Sprintf("不能从共享不可变引用创建可变借用 '%s'", formatPlaceDisplay(selfLoan.Place)),
					File:        st.Span.File,
					Span:        st.Span,
					Explanation: "父引用仅持有共享只读 (&T) 权限，WON 禁止通过重借用从共享引用制造独占可变引用 (&mut T)。",
				})
				return
			}
		}

	case cfg.OpAssign, cfg.OpReinit:
		if st.Place.RootSymID != 0 && st.Place.HasDeref() {
			pref, _ := st.Place.PrefixBeforeLastDeref()
			carrierOrig := lookupReborrowParentOrigins(fwd, flow, pref)
			if !hasMutableCarrierOrigins(fn, flow, carrierOrig, st.Place, tctx, symTab) {
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-MEM-004",
					Severity:    "error",
					Message:     fmt.Sprintf("不能通过只读共享引用解引用修改 '%s'", formatPlaceDisplay(st.Place)),
					File:        st.Span.File,
					Span:        st.Span,
					Explanation: "解引用链上的父载体为共享只读 (&T) 引用，禁止写入数据。",
				})
				return
			}
		}
	}

	if len(activeAfter) == 0 {
		return
	}
	sortedActive := sortedLoanIDs(activeAfter)

	switch st.Kind {
	case cfg.OpBorrowShared, cfg.OpBorrowMut, cfg.OpReborrow:
		selfLoan := flow.LoanByID[st.LoanID]
		if selfLoan == nil {
			return
		}
		selfTargets := resolveLoanUnderlyingTargets(flow, st.LoanID, nil)
		var carrierOrig OriginSet
		if st.IsReborrow && st.ParentPlace.RootSymID != 0 {
			carrierOrig = lookupPlaceOrigins(fwd, st.ParentPlace)
		}

		for _, actID := range sortedActive {
			if actID == st.LoanID {
				continue
			}
			actLoan := flow.LoanByID[actID]
			if actLoan == nil {
				continue
			}
			if st.IsReborrow && isAuthorizedThroughCarrier(flow, carrierOrig, actID) {
				continue
			}
			actTargets := resolveLoanUnderlyingTargets(flow, actID, nil)
			if !targetsOverlap(selfTargets, actTargets) {
				continue
			}

			parentFrozen := st.IsReborrow && hasMutableFrozenAncestor(flow, carrierOrig, actID)
			if !selfLoan.IsMut && !actLoan.IsMut && !parentFrozen {
				continue
			}

			disp := formatPlaceDisplay(selfLoan.Place)
			actDisp := formatPlaceDisplay(actLoan.Place)
			if selfLoan.IsMut && actLoan.IsMut {
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-MEM-005",
					Severity:    "error",
					Message:     fmt.Sprintf("不能同时对 '%s' 创建多个活跃的可变借用（与 %d:%d 处的 '%s' 冲突）", disp, actLoan.Span.Start.Line, actLoan.Span.Start.Column, actDisp),
					File:        st.Span.File,
					Span:        st.Span,
					Explanation: fmt.Sprintf("同一存储位置在任一时刻仅允许存在唯一活跃的 &mut 借用；先前位于 %d:%d 的 &mut 借用在此处仍处于活跃期（NLL 未终结）。建议拆分为互不重叠的结构体字段或等待前一借用最后一次使用结束后再发起。", actLoan.Span.Start.Line, actLoan.Span.Start.Column),
				})
				return
			}
			emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
				Code:        "WON-MEM-004",
				Severity:    "error",
				Message:     fmt.Sprintf("借用排他性冲突: 无法在此处借用 '%s'（与 %d:%d 处 '%s' 的活跃借用冲突）", disp, actLoan.Span.Start.Line, actLoan.Span.Start.Column, actDisp),
				File:        st.Span.File,
				Span:        st.Span,
				Explanation: fmt.Sprintf("WON 所有权模型要求共享借用 (&T) 与独占可变借用 (&mut T) 严格互斥，且活跃子重借用期间父可变借用处于冻结状态。先前位于 %d:%d 的借用在此仍处于 NLL 活跃期；请调整最后一次使用顺序或通过中间变量分离冲突操作。", actLoan.Span.Start.Line, actLoan.Span.Start.Column),
			})
			return
		}

	case cfg.OpAssign, cfg.OpReinit:
		if st.Place.RootSymID == 0 {
			return
		}
		var carrierOrig OriginSet
		var writeTargets []resolvedTarget
		if st.Place.HasDeref() {
			pref, _ := st.Place.PrefixBeforeLastDeref()
			carrierOrig = lookupPlaceOrigins(fwd, pref)
			writeTargets = resolvePlaceUnderlyingTargets(fwd, flow, st.Place)
		} else {
			writeTargets = []resolvedTarget{{place: st.Place, paramIdx: -1}}
		}

		for _, actID := range sortedActive {
			actLoan := flow.LoanByID[actID]
			if actLoan == nil {
				continue
			}
			if st.Place.HasDeref() && isAuthorizedThroughCarrier(flow, carrierOrig, actID) {
				continue
			}
			actTargets := resolveLoanUnderlyingTargets(flow, actID, nil)
			if !targetsOverlap(writeTargets, actTargets) {
				continue
			}
			emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
				Code:        "WON-MEM-006",
				Severity:    "error",
				Message:     fmt.Sprintf("不能在被借用期间修改或重新赋值 '%s'（与 %d:%d 处的借用冲突）", formatPlaceDisplay(st.Place), actLoan.Span.Start.Line, actLoan.Span.Start.Column),
				File:        st.Span.File,
				Span:        st.Span,
				Explanation: fmt.Sprintf("目标位置正处于 %d:%d 处发起的借用活跃区内，直接修改或重赋值将破坏借用稳定性。请在引用最后一次使用完毕后再执行修改。", actLoan.Span.Start.Line, actLoan.Span.Start.Column),
			})
			return
		}

	case cfg.OpMove:
		if st.Place.RootSymID == 0 {
			return
		}
		// 移动 &mut T 引用绑定自身（如 let m2 = m1）仅转移载体，不移动其指向的被借用所有者
		if st.Type != types.Invalid && tctx != nil && tctx.Info(st.Type).Kind == types.KindRef && len(st.Place.Projections) == 0 {
			if _, isRefCarrier := fwd.places[st.Place.Key()]; isRefCarrier {
				return
			}
		}
		moveTargets := resolvePlaceUnderlyingTargets(fwd, flow, st.Place)
		for _, actID := range sortedActive {
			actLoan := flow.LoanByID[actID]
			if actLoan == nil {
				continue
			}
			actTargets := resolveLoanUnderlyingTargets(flow, actID, nil)
			if !targetsOverlap(moveTargets, actTargets) {
				continue
			}
			emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
				Code:        "WON-MEM-006",
				Severity:    "error",
				Message:     fmt.Sprintf("不能在存在活跃借用期间移出 '%s' 的所有权（与 %d:%d 处的借用冲突）", formatPlaceDisplay(st.Place), actLoan.Span.Start.Line, actLoan.Span.Start.Column),
				File:        st.Span.File,
				Span:        st.Span,
				Explanation: fmt.Sprintf("在 %d:%d 处创建的借用在此处仍然活跃，禁止移走或销毁目标值。", actLoan.Span.Start.Line, actLoan.Span.Start.Column),
			})
			return
		}

	case cfg.OpRead, cfg.OpCopy:
		if st.Place.RootSymID == 0 {
			return
		}
		// 若此 OpRead 紧接同位置的 OpBorrowShared / OpBorrowMut / OpReborrow（仅作 2B.1 Init 前置探测），将排他判定留给紧随的借用语句自身，以精确区分 WON-MEM-004 与 WON-MEM-005
		if st.Kind == cfg.OpRead && stIdx+1 < len(blk.Statements) {
			nextSt := blk.Statements[stIdx+1]
			if nextSt.Kind == cfg.OpBorrowShared || nextSt.Kind == cfg.OpBorrowMut || nextSt.Kind == cfg.OpReborrow {
				return
			}
		}
		// 拷贝共享引用 &T 载体本身仅增加一条别名，不直接冲突读取底层对象
		if !st.Place.HasDeref() && st.Type != types.Invalid && tctx != nil && tctx.Info(st.Type).Kind == types.KindRef {
			return
		}

		var carrierOrig OriginSet
		var readTargets []resolvedTarget
		if st.Place.HasDeref() {
			pref, _ := st.Place.PrefixBeforeLastDeref()
			carrierOrig = lookupPlaceOrigins(fwd, pref)
			readTargets = resolvePlaceUnderlyingTargets(fwd, flow, st.Place)
		} else {
			readTargets = []resolvedTarget{{place: st.Place, paramIdx: -1}}
		}

		for _, actID := range sortedActive {
			actLoan := flow.LoanByID[actID]
			if actLoan == nil {
				continue
			}
			if st.Place.HasDeref() && isAuthorizedThroughCarrier(flow, carrierOrig, actID) {
				continue
			}
			actTargets := resolveLoanUnderlyingTargets(flow, actID, nil)
			if !targetsOverlap(readTargets, actTargets) {
				continue
			}
			parentFrozen := st.Place.HasDeref() && hasMutableFrozenAncestor(flow, carrierOrig, actID)
			if !actLoan.IsMut && !parentFrozen {
				continue
			}
			emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
				Code:        "WON-MEM-004",
				Severity:    "error",
				Message:     fmt.Sprintf("独占借用期间禁止直接访问 '%s'（与 %d:%d 处的借用冲突）", formatPlaceDisplay(st.Place), actLoan.Span.Start.Line, actLoan.Span.Start.Column),
				File:        st.Span.File,
				Span:        st.Span,
				Explanation: fmt.Sprintf("目标位置正被 %d:%d 处的活跃借用占用或冻结，禁止绕过该活跃引用进行直接读取或复制。", actLoan.Span.Start.Line, actLoan.Span.Start.Column),
			})
			return
		}
	}
	_ = symTab
	_ = liveAfter
}

func hasMutableReborrowAuthority(fn *cfg.FuncCFG, flow *FuncLoanFlow, fwd *carrierEnv, selfLoan *LoanInfo, tctx *types.Context, symTab *symbols.Table) bool {
	if selfLoan == nil {
		return false
	}
	for pid := range selfLoan.ParentLoans {
		if pl := flow.LoanByID[pid]; pl != nil && !pl.IsMut {
			return false
		}
	}
	for pIdx := range selfLoan.ParentParams {
		if !paramDerefChainMutable(fn, pIdx, selfLoan.Place, tctx, symTab) {
			return false
		}
	}
	if selfLoan.ParentPlace.RootSymID != 0 {
		parentOrig := lookupReborrowParentOrigins(fwd, flow, selfLoan.ParentPlace)
		if !hasMutableCarrierOrigins(fn, flow, parentOrig, selfLoan.Place, tctx, symTab) {
			return false
		}
	}
	return true
}

func hasMutableCarrierOrigins(fn *cfg.FuncCFG, flow *FuncLoanFlow, orig OriginSet, derefPlace cfg.Place, tctx *types.Context, symTab *symbols.Table) bool {
	for pid := range orig.Loans {
		if pl := flow.LoanByID[pid]; pl != nil && !pl.IsMut {
			return false
		}
	}
	for pIdx := range orig.Params {
		if !paramDerefChainMutable(fn, pIdx, derefPlace, tctx, symTab) {
			return false
		}
	}
	return true
}

func paramDerefChainMutable(fn *cfg.FuncCFG, pIdx int, derefPlace cfg.Place, tctx *types.Context, symTab *symbols.Table) bool {
	if fn == nil || fn.FuncSym == nil || tctx == nil || pIdx < 0 {
		return true
	}
	curType := fn.FuncSym.EffectiveParamType(pIdx, tctx)
	if curType == types.Invalid {
		return true
	}
	firstInfo := tctx.Info(curType)
	if firstInfo.Kind == types.KindRef && !firstInfo.IsMut {
		return false
	}
	for _, proj := range derefPlace.Projections {
		info := tctx.Info(curType)
		switch proj.Kind {
		case cfg.ProjDeref:
			if (info.Kind == types.KindRef || info.Kind == types.KindRawPtr) && !info.IsMut {
				return false
			}
			if info.Kind == types.KindRef || info.Kind == types.KindRawPtr {
				curType = info.Elem
			} else if proj.ElemType != types.Invalid {
				curType = proj.ElemType
			}
		case cfg.ProjField:
			if (info.Kind == types.KindRef || info.Kind == types.KindRawPtr) && !info.IsMut {
				return false
			}
			if info.Kind == types.KindRef || info.Kind == types.KindRawPtr {
				curType = info.Elem
			}
			if curType != types.Invalid && symTab != nil {
				cInfo := tctx.Info(curType)
				if cInfo.Kind == types.KindStruct {
					if sym := symTab.ByID[symbols.SymbolID(cInfo.SymbolID)]; sym != nil {
						if fi := sym.FieldMap[proj.FieldName]; fi != nil {
							curType = fi.Type
							continue
						}
					}
				}
			}
			curType = proj.ElemType
		case cfg.ProjConstIdx, cfg.ProjIndex, cfg.ProjTupleField:
			if (info.Kind == types.KindRef || info.Kind == types.KindRawPtr) && !info.IsMut {
				return false
			}
			curType = proj.ElemType
		}
	}
	return true
}

func checkScopeExitLoans(
	st cfg.Statement,
	exiting []symbols.SymbolID,
	activeAfter map[cfg.LoanID]bool,
	flow *FuncLoanFlow,
	symTab *symbols.Table,
	report *diagnostic.Report,
	emitted map[diagDedupKey]bool,
) {
	if len(exiting) == 0 || len(activeAfter) == 0 {
		return
	}
	exitingSet := make(map[symbols.SymbolID]bool, len(exiting))
	for _, sid := range exiting {
		exitingSet[sid] = true
	}
	for _, actID := range sortedLoanIDs(activeAfter) {
		actLoan := flow.LoanByID[actID]
		if actLoan == nil {
			continue
		}
		for _, tgt := range resolveLoanUnderlyingTargets(flow, actID, nil) {
			if tgt.paramIdx < 0 && !tgt.unknown && exitingSet[tgt.place.RootSymID] {
				ownerName := tgt.place.RootName
				var ownerSpan diagnostic.Span
				if symTab != nil {
					if sym := symTab.ByID[tgt.place.RootSymID]; sym != nil {
						ownerName = sym.Name
						ownerSpan = sym.Span
					}
				}
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-MEM-003",
					Severity:    "error",
					Message:     fmt.Sprintf("被借用局部变量 '%s' 的生命周期不足: 引用仍需存活但所有者已在此退出作用域", ownerName),
					File:        actLoan.Span.File,
					Span:        actLoan.Span,
					Explanation: fmt.Sprintf("局部变量 '%s'（声明于 %d:%d）在此作用域边界 (%d:%d) 结束存储生命期，但该借用此后仍可能被读取。请延长所有者作用域或改为返回所有权值。", ownerName, ownerSpan.Start.Line, ownerSpan.Start.Column, st.Span.Start.Line, st.Span.Start.Column),
				})
				return
			}
		}
	}
}

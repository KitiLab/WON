package borrow

import (
	"fmt"
	"sort"

	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// FuncSummary 记录单个已检查函数引用返回值的 Strategy B 候选溯源摘要 (RetProv(f) ⊆ {Param(i), Static})。
type FuncSummary struct {
	SymID        symbols.SymbolID
	Name         string
	ReturnsRef   bool
	ReturnIsMut  bool
	ParamIndices map[int]bool
	HasStatic    bool
	Unknown      bool
}

func newFuncSummary(fn *cfg.FuncCFG, tctx *types.Context, symTab *symbols.Table) *FuncSummary {
	var symID symbols.SymbolID
	if fn.FuncSym != nil {
		symID = fn.FuncSym.ID
	}
	retRef := typeHasRef(tctx, nil, fn.ReturnType, symTab)
	retMut := false
	if fn.ReturnType != types.Invalid && tctx != nil {
		info := tctx.Info(fn.ReturnType)
		retMut = info.Kind == types.KindRef && info.IsMut
	}
	return &FuncSummary{
		SymID:        symID,
		Name:         fn.Name,
		ReturnsRef:   retRef,
		ReturnIsMut:  retMut,
		ParamIndices: make(map[int]bool),
	}
}

func (s *FuncSummary) clone() *FuncSummary {
	cp := &FuncSummary{
		SymID:        s.SymID,
		Name:         s.Name,
		ReturnsRef:   s.ReturnsRef,
		ReturnIsMut:  s.ReturnIsMut,
		ParamIndices: make(map[int]bool, len(s.ParamIndices)),
		HasStatic:    s.HasStatic,
		Unknown:      s.Unknown,
	}
	for k := range s.ParamIndices {
		cp.ParamIndices[k] = true
	}
	return cp
}

func (s *FuncSummary) equals(other *FuncSummary) bool {
	if s == nil || other == nil {
		return s == other
	}
	if s.ReturnsRef != other.ReturnsRef || s.ReturnIsMut != other.ReturnIsMut ||
		s.HasStatic != other.HasStatic || s.Unknown != other.Unknown ||
		len(s.ParamIndices) != len(other.ParamIndices) {
		return false
	}
	for k := range s.ParamIndices {
		if !other.ParamIndices[k] {
			return false
		}
	}
	return true
}

// SortedParamIndices 按确定性升序返回摘要中的所有候选参数下标。
func (s *FuncSummary) SortedParamIndices() []int {
	res := make([]int, 0, len(s.ParamIndices))
	for k := range s.ParamIndices {
		res = append(res, k)
	}
	sort.Ints(res)
	return res
}

// ProvenanceResult 保存全程序调用图 SCC 拓扑、收敛后的候选返回摘要以及精确化后的每函数载体流。
type ProvenanceResult struct {
	Summaries map[symbols.SymbolID]*FuncSummary
	Flows     map[symbols.SymbolID]*FuncLoanFlow
	SCCs      [][]symbols.SymbolID
}

// InferAndVerifyProvenance 执行 Stage C：
// 1) 拦截不透明/无函数体多引用签名返回 (WON-MEM-002)；
// 2) 构建确定性调用图并按 Tarjan 逆拓扑 SCC 不动点求解 RetProv(f)；
// 3) 检测所有可达 return 路径上的局部引用逃逸 (WON-MEM-003) 及调用点可变候选重叠 (WON-MEM-005)；
// 4) 使用收敛摘要重构全程序精确引用载体流供 NLL 与排他检查消费。
func InferAndVerifyProvenance(progCFG *cfg.ProgramCFG, view cfg.SemanticView, report *diagnostic.Report) *ProvenanceResult {
	if progCFG == nil || view == nil || report == nil {
		return nil
	}
	tctx := view.GetTypes()
	symTab := view.GetSymbols()

	res := &ProvenanceResult{
		Summaries: make(map[symbols.SymbolID]*FuncSummary, len(progCFG.Functions)),
		Flows:     make(map[symbols.SymbolID]*FuncLoanFlow, len(progCFG.Functions)),
	}

	// 1. 先检查声明级与符号表中存在的所有无函数体 / 函数指针签名是否触发 WON-MEM-002
	emitted := make(map[diagDedupKey]bool)
	checkOpaqueSignatures(progCFG, symTab, tctx, report, emitted)

	// 2. 初始化函数摘要表并验证初始 Stage A 借用流不变量
	for _, fn := range progCFG.Functions {
		if fn.FuncSym == nil {
			continue
		}
		res.Summaries[fn.FuncSym.ID] = newFuncSummary(fn, tctx, symTab)
		flow := buildLoanFlowWithSummaries(fn, view, nil, report)
		if flow == nil || report.HasErrors() {
			return nil
		}
		res.Flows[fn.FuncSym.ID] = flow
	}

	// 3. 构建确定性调用图并求解 Tarjan 强连通分量 (被调用者在拓扑顺序上先于调用者)
	sccs := computeCallGraphSCCs(progCFG)
	res.SCCs = sccs

	for _, scc := range sccs {
		fnInSCC := make([]*cfg.FuncCFG, 0, len(scc))
		maxParams := 0
		for _, sid := range scc {
			if fn := progCFG.BySymbol[sid]; fn != nil {
				fnInSCC = append(fnInSCC, fn)
				if fn.FuncSym != nil && len(fn.FuncSym.ParamTypes) > maxParams {
					maxParams = len(fn.FuncSym.ParamTypes)
				}
			}
		}
		if len(fnInSCC) == 0 {
			continue
		}
		budget := len(fnInSCC)*(maxParams+8) + 64
		changed := true
		iters := 0
		for changed {
			iters++
			if iters > budget {
				fn0 := fnInSCC[0]
				var sp diagnostic.Span
				if fn0.Decl != nil {
					sp = fn0.Decl.GetSpan()
				}
				report.AddDiagnostic(diagnostic.Diagnostic{
					Code:        "WON-INT-001",
					Severity:    "error",
					Message:     fmt.Sprintf("候选引用溯源 SCC 不动点迭代在函数 '%s' 所在分量超过安全上限 (%d 轮) 仍未收敛", fn0.Name, budget),
					File:        sp.File,
					Span:        sp,
					Explanation: "编译器内部过程间引用溯源摘要未按预期单调收敛，按 Fail-Closed 策略拒绝认证。",
				})
				return nil
			}
			changed = false
			for _, fn := range fnInSCC {
				sid := fn.FuncSym.ID
				refinedFlow := buildLoanFlowWithSummaries(fn, view, res.Summaries, report)
				if refinedFlow == nil || report.HasErrors() {
					return nil
				}
				res.Flows[sid] = refinedFlow
				curSum := res.Summaries[sid]
				if !curSum.ReturnsRef {
					continue
				}
				nextSum := collectFunctionReturnSummary(fn, refinedFlow, view)
				if !curSum.equals(nextSum) {
					res.Summaries[sid] = nextSum
					changed = true
				}
			}
		}
	}

	// 4. 在最终收敛摘要下检验全部函数返回逃逸 (WON-MEM-003) 与调用点不透明签名 / 可变候选不重叠 (WON-MEM-002 / WON-MEM-005)
	for _, fn := range progCFG.Functions {
		if fn.FuncSym == nil {
			continue
		}
		flow := buildLoanFlowWithSummaries(fn, view, res.Summaries, report)
		if flow == nil || report.HasErrors() {
			return nil
		}
		res.Flows[fn.FuncSym.ID] = flow
		sum := res.Summaries[fn.FuncSym.ID]
		validateFunctionProvenance(progCFG, fn, flow, sum, res.Summaries, view, tctx, symTab, report, emitted)
	}

	return res
}

func checkOpaqueSignatures(progCFG *cfg.ProgramCFG, symTab *symbols.Table, tctx *types.Context, report *diagnostic.Report, emitted map[diagDedupKey]bool) {
	if symTab == nil || tctx == nil || report == nil {
		return
	}
	for _, sym := range symTab.All {
		if sym == nil {
			continue
		}
		// 1) 无 CFG 函数体的声明函数/方法（跳过已由 WON-SEM-010 拦截的 Trait 或未知外围内建）
		if (sym.Kind == symbols.SymFunc || sym.Kind == symbols.SymMethod) && progCFG.BySymbol[sym.ID] == nil {
			// Skip AST without body only if it returns reference
			if typeHasRef(tctx, nil, sym.ReturnType, symTab) {
				refCount := countRefParams(sym.ParamTypes, tctx, symTab)
				if sym.HasSelf && sym.SelfIsRef {
					refCount++
				}
				if refCount >= 2 {
					emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
						Code:        "WON-MEM-002",
						Severity:    "error",
						Message:     fmt.Sprintf("无函数体可调用对象 '%s' 接收 %d 个引用输入并返回引用，候选来源存在歧义", sym.Name, refCount),
						File:        sym.Span.File,
						Span:        sym.Span,
						Explanation: "ADR-011 规定无函数体可供过程间分析的多引用返回签名属于不安全歧义边界；请返回拥有所有权的类型、单引用输入，或改用可分析函数体。",
					})
				} else if refCount == 0 && sym.ReturnType != tctx.StrView {
					emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
						Code:        "WON-MEM-002",
						Severity:    "error",
						Message:     fmt.Sprintf("无函数体可调用对象 '%s' 无引用输入却返回引用 '%s'，无法证明非静态生命周期安全性", sym.Name, tctx.Format(sym.ReturnType)),
						File:        sym.Span.File,
						Span:        sym.Span,
						Explanation: "编译器拒绝为无引用输入的无函数体签名隐式假设 Static 溯源。",
					})
				}
			}
			continue
		}

		// 2) 函数指针类型参数、局部变量或结构体字段 fn(P1, P2, ...) -> &R（不含已有函数体登记的具体实体函数）
		if sym.Kind != symbols.SymFunc && sym.Kind != symbols.SymMethod && sym.Kind != symbols.SymBuiltin && sym.Type != types.Invalid {
			info := tctx.Info(sym.Type)
			if info.Kind == types.KindFn && typeHasRef(tctx, nil, info.Return, symTab) {
				refCount := countRefParams(info.Params, tctx, symTab)
				if refCount >= 2 || (refCount == 0 && info.Return != tctx.StrView) {
					emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
						Code:        "WON-MEM-002",
						Severity:    "error",
						Message:     fmt.Sprintf("不透明函数指针签名 '%s' (%s) 的引用返回来源不可判定", sym.Name, tctx.Format(sym.Type)),
						File:        sym.Span.File,
						Span:        sym.Span,
						Explanation: "ADR-011 规定函数指针与无体接口在 >=2 个输入引用或无引用输入返回借用时必须报 WON-MEM-002；请返回拥有所有权值。",
					})
				}
			}
		}
	}
}

func countRefParams(paramTypes []types.TypeID, tctx *types.Context, symTab *symbols.Table) int {
	cnt := 0
	for _, pt := range paramTypes {
		if typeHasRef(tctx, nil, pt, symTab) {
			cnt++
		}
	}
	return cnt
}

func computeCallGraphSCCs(progCFG *cfg.ProgramCFG) [][]symbols.SymbolID {
	// 按 FuncCFG 在 ProgramCFG.Functions 中的确定性顺序建立节点表与去重邻接表
	nodes := make([]symbols.SymbolID, 0, len(progCFG.Functions))
	nodeExists := make(map[symbols.SymbolID]bool, len(progCFG.Functions))
	for _, fn := range progCFG.Functions {
		if fn.FuncSym != nil && !nodeExists[fn.FuncSym.ID] {
			nodeExists[fn.FuncSym.ID] = true
			nodes = append(nodes, fn.FuncSym.ID)
		}
	}

	adj := make(map[symbols.SymbolID][]symbols.SymbolID, len(nodes))
	for _, fn := range progCFG.Functions {
		if fn.FuncSym == nil {
			continue
		}
		u := fn.FuncSym.ID
		seenEdge := make(map[symbols.SymbolID]bool)
		reach := computeReachable(fn)
		for bbIdx, blk := range fn.Blocks {
			if !reach[bbIdx] {
				continue
			}
			for _, st := range blk.Statements {
				if st.Kind == cfg.OpCall && st.Callee != nil && nodeExists[st.Callee.ID] && !seenEdge[st.Callee.ID] {
					seenEdge[st.Callee.ID] = true
					adj[u] = append(adj[u], st.Callee.ID)
				}
			}
		}
	}

	// 标准 Tarjan SCC 算法天然产出“被调用 SCC 在前、调用者 SCC 在后”的逆拓扑序
	index := 0
	indices := make(map[symbols.SymbolID]int, len(nodes))
	lowlink := make(map[symbols.SymbolID]int, len(nodes))
	onStack := make(map[symbols.SymbolID]bool, len(nodes))
	var stack []symbols.SymbolID
	var sccs [][]symbols.SymbolID

	var strongConnect func(v symbols.SymbolID)
	strongConnect = func(v symbols.SymbolID) {
		index++
		indices[v] = index
		lowlink[v] = index
		stack = append(stack, v)
		onStack[v] = true

		for _, w := range adj[v] {
			if indices[w] == 0 {
				strongConnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] {
				if indices[w] < lowlink[v] {
					lowlink[v] = indices[w]
				}
			}
		}

		if lowlink[v] == indices[v] {
			var comp []symbols.SymbolID
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			sccs = append(sccs, comp)
		}
	}

	for _, n := range nodes {
		if indices[n] == 0 {
			strongConnect(n)
		}
	}
	return sccs
}

func collectFunctionReturnSummary(fn *cfg.FuncCFG, flow *FuncLoanFlow, view cfg.SemanticView) *FuncSummary {
	sum := newFuncSummary(fn, view.GetTypes(), view.GetSymbols())
	reachable := computeReachable(fn)
	for bbIdx, blk := range fn.Blocks {
		if !reachable[bbIdx] {
			continue
		}
		fwd := flow.InStates[bbIdx].clone()
		for _, st := range blk.Statements {
			if st.Kind == cfg.OpReturn {
				orig := extractReturnOrigin(fwd, st)
				for pIdx := range orig.Params {
					sum.ParamIndices[pIdx] = true
				}
				for lid := range orig.Loans {
					for _, tgt := range resolveLoanUnderlyingTargets(flow, lid, nil) {
						if tgt.paramIdx >= 0 {
							sum.ParamIndices[tgt.paramIdx] = true
						} else if tgt.unknown {
							sum.Unknown = true
						}
					}
				}
				if orig.Static {
					sum.HasStatic = true
				}
				if orig.Unknown {
					sum.Unknown = true
				}
			}
			stepSingleCarrierStmt(fn, st, fwd, flow, view)
		}
	}
	return sum
}

func extractReturnOrigin(fwd *carrierEnv, st cfg.Statement) OriginSet {
	res := newOriginSet()
	if st.SrcTemp != cfg.InvalidValue {
		if o, ok := fwd.temps[st.SrcTemp]; ok {
			res.merge(o)
		}
	}
	if st.Place.RootSymID != 0 {
		res.merge(lookupPlaceOrigins(fwd, st.Place))
	}
	return res
}

func validateFunctionProvenance(
	progCFG *cfg.ProgramCFG,
	fn *cfg.FuncCFG,
	flow *FuncLoanFlow,
	sum *FuncSummary,
	summaries map[symbols.SymbolID]*FuncSummary,
	view cfg.SemanticView,
	tctx *types.Context,
	symTab *symbols.Table,
	report *diagnostic.Report,
	emitted map[diagDedupKey]bool,
) {
	reachable := computeReachable(fn)
	for bbIdx, blk := range fn.Blocks {
		if !reachable[bbIdx] {
			continue
		}
		fwd := flow.InStates[bbIdx].clone()
		for _, st := range blk.Statements {
			switch st.Kind {
			case cfg.OpReturn:
				if sum.ReturnsRef {
					orig := extractReturnOrigin(fwd, st)
					checkReturnOriginsValid(fn, st, orig, flow, sum, tctx, symTab, report, emitted)
				}

			case cfg.OpCall:
				checkCallProvenanceAndDisjointness(progCFG, fn, st, fwd, flow, summaries, tctx, symTab, report, emitted)
			}
			stepSingleCarrierStmt(fn, st, fwd, flow, view)
		}
	}
}

func checkReturnOriginsValid(
	fn *cfg.FuncCFG,
	st cfg.Statement,
	orig OriginSet,
	flow *FuncLoanFlow,
	sum *FuncSummary,
	tctx *types.Context,
	symTab *symbols.Table,
	report *diagnostic.Report,
	emitted map[diagDedupKey]bool,
) {
	for _, lid := range orig.SortedLoans() {
		li := flow.LoanByID[lid]
		targets := resolveLoanUnderlyingTargets(flow, lid, nil)
		for _, tgt := range targets {
			if tgt.unknown {
				var sp diagnostic.Span
				if li != nil {
					sp = li.Span
				}
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-MEM-003",
					Severity:    "error",
					Message:     fmt.Sprintf("函数 '%s' 试图返回源自裸指针解引用或未知生命周期的引用", fn.Name),
					File:        st.Span.File,
					Span:        st.Span,
					Explanation: fmt.Sprintf("位于 %d:%d 的借用不具备可证的输入参数或静态生命周期来源，禁止通过安全引用返回逃逸。", sp.Start.Line, sp.Start.Column),
				})
				return
			}
			if tgt.paramIdx < 0 {
				localName := tgt.place.RootName
				var declSpan diagnostic.Span
				if symTab != nil {
					if sym := symTab.ByID[tgt.place.RootSymID]; sym != nil {
						localName = sym.Name
						declSpan = sym.Span
					}
				}
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-MEM-003",
					Severity:    "error",
					Message:     fmt.Sprintf("局部引用逃逸: 不能从函数 '%s' 返回局部变量 '%s' 的引用", fn.Name, localName),
					File:        st.Span.File,
					Span:        st.Span,
					Explanation: fmt.Sprintf("局部变量 '%s'（声明于 %d:%d）在函数返回时释放栈存储，返回的引用将悬垂失效。请改为返回拥有所有权的值。", localName, declSpan.Start.Line, declSpan.Start.Column),
				})
				return
			}
			if sum.ReturnIsMut && li != nil && !li.IsMut {
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-MEM-004",
					Severity:    "error",
					Message:     fmt.Sprintf("不能将共享不可变借用作为函数 '%s' 的 '&mut' 引用返回", fn.Name),
					File:        st.Span.File,
					Span:        st.Span,
					Explanation: "不可变借用源无法升级为可变返回引用。",
				})
				return
			}
		}
	}

	if sum.ReturnIsMut {
		for _, pIdx := range orig.SortedParams() {
			if fn.FuncSym != nil && pIdx >= 0 && pIdx < len(fn.FuncSym.ParamTypes) && tctx != nil {
				pt := fn.FuncSym.ParamTypes[pIdx]
				pInfo := tctx.Info(pt)
				if pInfo.Kind == types.KindRef && !pInfo.IsMut {
					emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
						Code:        "WON-MEM-004",
						Severity:    "error",
						Message:     fmt.Sprintf("函数 '%s' 不能将不可变引用参数 #%d 作为 '&mut' 返回", fn.Name, pIdx+1),
						File:        st.Span.File,
						Span:        st.Span,
						Explanation: "只有具备 &mut 独占可变权限的来源才可作为 &mut 返回候选。",
					})
					return
				}
			}
		}
	}
}

func checkCallProvenanceAndDisjointness(
	progCFG *cfg.ProgramCFG,
	fn *cfg.FuncCFG,
	st cfg.Statement,
	fwd *carrierEnv,
	flow *FuncLoanFlow,
	summaries map[symbols.SymbolID]*FuncSummary,
	tctx *types.Context,
	symTab *symbols.Table,
	report *diagnostic.Report,
	emitted map[diagDedupKey]bool,
) {
	if st.ValTemp == cfg.InvalidValue {
		return
	}

	// 1. 若调用不透明/无体目标并返回引用（如无 body 的外部函数，排除单引用内建如 .as_bytes()）
	if st.Callee == nil || progCFG.BySymbol[st.Callee.ID] == nil {
		refArgCount := 0
		for _, at := range st.ArgTemps {
			if at != cfg.InvalidValue {
				if o, ok := fwd.temps[at]; ok && !o.isEmpty() {
					refArgCount++
				}
			}
		}
		if refArgCount >= 2 {
			emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
				Code:        "WON-MEM-002",
				Severity:    "error",
				Message:     fmt.Sprintf("调用不透明多引用函数 '%s' 返回引用时存在歧义候选来源", st.CalleeName),
				File:        st.Span.File,
				Span:        st.Span,
				Explanation: "无函数体或函数指针调用接收多个引用实参并返回引用时，无法唯一确定返回借用来源 (WON-MEM-002)。",
			})
			return
		}
		return
	}

	// 2. 若返回类型为 &mut T 且候选集包含 >=2 个候选实参，必须证明所选可变候选底层互不重叠
	calSum := summaries[st.Callee.ID]
	if calSum == nil || !calSum.ReturnIsMut {
		return
	}
	candIndices := calSum.SortedParamIndices()
	if len(candIndices) < 2 {
		return
	}

	type candTargetGroup struct {
		argIdx  int
		targets []resolvedTarget
	}
	var groups []candTargetGroup
	for _, pIdx := range candIndices {
		if pIdx < 0 || pIdx >= len(st.ArgTemps) {
			continue
		}
		argT := st.ArgTemps[pIdx]
		if argT == cfg.InvalidValue {
			continue
		}
		argOrig := fwd.temps[argT]
		var tgts []resolvedTarget
		for lid := range argOrig.Loans {
			tgts = append(tgts, resolveLoanUnderlyingTargets(flow, lid, nil)...)
		}
		for parIdx := range argOrig.Params {
			tgts = append(tgts, resolvedTarget{paramIdx: parIdx})
		}
		if argOrig.Unknown {
			tgts = append(tgts, resolvedTarget{paramIdx: -1, unknown: true})
		}
		groups = append(groups, candTargetGroup{argIdx: pIdx, targets: tgts})
	}

	for i := 0; i < len(groups); i++ {
		for j := i + 1; j < len(groups); j++ {
			if targetsOverlap(groups[i].targets, groups[j].targets) {
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-MEM-005",
					Severity:    "error",
					Message:     fmt.Sprintf("调用 '%s' 返回 '&mut' 引用时第 %d 与第 %d 个候选可变实参可能指向重叠存储", st.CalleeName, groups[i].argIdx+1, groups[j].argIdx+1),
					File:        st.Span.File,
					Span:        st.Span,
					Explanation: "Strategy B 要求可变返回候选集合中的所有候选实参必须在底层两两互不重叠 (disjoint)，否则拒绝以防产生双可变别名。",
				})
				return
			}
		}
	}
	_ = tctx
	_ = symTab
	_ = fn
}

package borrow

import (
	"fmt"
	"sort"

	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// FieldProvenance 描述返回类型中特定可投影叶子路径（"" 表示直接引用，"left" / "[0]" 等表示聚合字段路径）的候选溯源与可变性。
type FieldProvenance struct {
	Path         string
	IsMut        bool
	ParamIndices map[int]bool
	HasStatic    bool
	Unknown      bool
}

func (fp *FieldProvenance) clone() *FieldProvenance {
	cp := &FieldProvenance{
		Path:         fp.Path,
		IsMut:        fp.IsMut,
		ParamIndices: make(map[int]bool, len(fp.ParamIndices)),
		HasStatic:    fp.HasStatic,
		Unknown:      fp.Unknown,
	}
	for k := range fp.ParamIndices {
		cp.ParamIndices[k] = true
	}
	return cp
}

func (fp *FieldProvenance) equals(other *FieldProvenance) bool {
	if fp == nil || other == nil {
		return fp == other
	}
	if fp.Path != other.Path || fp.IsMut != other.IsMut || fp.HasStatic != other.HasStatic || fp.Unknown != other.Unknown || len(fp.ParamIndices) != len(other.ParamIndices) {
		return false
	}
	for k := range fp.ParamIndices {
		if !other.ParamIndices[k] {
			return false
		}
	}
	return true
}

func (fp *FieldProvenance) SortedParamIndices() []int {
	res := make([]int, 0, len(fp.ParamIndices))
	for k := range fp.ParamIndices {
		res = append(res, k)
	}
	sort.Ints(res)
	return res
}

// FuncSummary 记录单个已检查函数引用返回值的 Strategy B 候选溯源摘要 (RetProv(f) ⊆ {Param(i), Static})。
type FuncSummary struct {
	SymID             symbols.SymbolID
	Name              string
	ReturnsRef        bool
	ReturnIsMut       bool
	ParamIndices      map[int]bool
	HasStatic         bool
	Unknown           bool
	FieldProvs        map[string]*FieldProvenance
	UnsupportedAggRef bool
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
	sum := &FuncSummary{
		SymID:        symID,
		Name:         fn.Name,
		ReturnsRef:   retRef,
		ReturnIsMut:  retMut,
		ParamIndices: make(map[int]bool),
		FieldProvs:   make(map[string]*FieldProvenance),
	}
	if retRef {
		leaves, unsup := collectTypeRefLeaves(tctx, symTab, fn.ReturnType, "")
		sum.UnsupportedAggRef = unsup
		for _, leaf := range leaves {
			sum.FieldProvs[leaf.Path] = &FieldProvenance{
				Path:         leaf.Path,
				IsMut:        leaf.IsMut,
				ParamIndices: make(map[int]bool),
			}
		}
	}
	return sum
}

func (s *FuncSummary) clone() *FuncSummary {
	cp := &FuncSummary{
		SymID:             s.SymID,
		Name:              s.Name,
		ReturnsRef:        s.ReturnsRef,
		ReturnIsMut:       s.ReturnIsMut,
		ParamIndices:      make(map[int]bool, len(s.ParamIndices)),
		HasStatic:         s.HasStatic,
		Unknown:           s.Unknown,
		FieldProvs:        make(map[string]*FieldProvenance, len(s.FieldProvs)),
		UnsupportedAggRef: s.UnsupportedAggRef,
	}
	for k := range s.ParamIndices {
		cp.ParamIndices[k] = true
	}
	for k, fp := range s.FieldProvs {
		if fp != nil {
			cp.FieldProvs[k] = fp.clone()
		}
	}
	return cp
}

func (s *FuncSummary) equals(other *FuncSummary) bool {
	if s == nil || other == nil {
		return s == other
	}
	if s.ReturnsRef != other.ReturnsRef || s.ReturnIsMut != other.ReturnIsMut ||
		s.HasStatic != other.HasStatic || s.Unknown != other.Unknown ||
		s.UnsupportedAggRef != other.UnsupportedAggRef ||
		len(s.ParamIndices) != len(other.ParamIndices) ||
		len(s.FieldProvs) != len(other.FieldProvs) {
		return false
	}
	for k := range s.ParamIndices {
		if !other.ParamIndices[k] {
			return false
		}
	}
	for k, fp := range s.FieldProvs {
		if !fp.equals(other.FieldProvs[k]) {
			return false
		}
	}
	return true
}

func (s *FuncSummary) SortedFieldKeys() []string {
	keys := make([]string, 0, len(s.FieldProvs))
	for k := range s.FieldProvs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func instantiateFieldProvOrigin(fp *FieldProvenance, argTemps []cfg.ValueID, env *carrierEnv) OriginSet {
	out := newOriginSet()
	if fp == nil {
		return out
	}
	for pIdx := range fp.ParamIndices {
		if pIdx >= 0 && pIdx < len(argTemps) && argTemps[pIdx] != cfg.InvalidValue {
			if argOrig, ok := env.temps[argTemps[pIdx]]; ok {
				out.merge(argOrig)
			}
		}
	}
	if fp.HasStatic {
		out.Static = true
	}
	if fp.Unknown {
		out.Unknown = true
	}
	return out
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
				if fn.FuncSym != nil && fn.FuncSym.EffectiveParamCount() > maxParams {
					maxParams = fn.FuncSym.EffectiveParamCount()
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
				if !curSum.ReturnsRef || curSum.UnsupportedAggRef {
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
	checkFnType := func(name string, span diagnostic.Span, paramTypes []types.TypeID, retType types.TypeID, label string) {
		if retType == types.Invalid {
			emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
				Code:        "WON-INT-001",
				Severity:    "error",
				Message:     fmt.Sprintf("%s '%s' 缺少有效的返回类型元数据", label, name),
				File:        span.File,
				Span:        span,
				Explanation: "不透明边界校验需要完整的返回类型信息；缺少有效 TypeID 时按 Fail-Closed 原则拒绝认证。",
			})
			return
		}
		for idx, pt := range paramTypes {
			if pt == types.Invalid {
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-INT-001",
					Severity:    "error",
					Message:     fmt.Sprintf("%s '%s' 第 %d 个规范形参缺少有效类型元数据", label, name, idx),
					File:        span.File,
					Span:        span,
					Explanation: "缺少有效参数类型元数据时无法证明引用输入数量与可变性权限，按 Fail-Closed 原则拒绝认证。",
				})
				return
			}
		}
		if !typeHasRef(tctx, nil, retType, symTab) {
			return
		}
		leaves, unsup := collectTypeRefLeaves(tctx, symTab, retType, "")
		if unsup {
			emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
				Code:        "WON-SEM-010",
				Severity:    "error",
				Message:     fmt.Sprintf("%s '%s' 返回不支持按字段投影分析的含引用聚合 (%s)", label, name, tctx.Format(retType)),
				File:        span.File,
				Span:        span,
				Explanation: "不透明接口不允许返回无法按字段拆分的含引用枚举、数组或切片。",
			})
			return
		}
		refCount, mutRefCount := countRefParamCapabilities(paramTypes, tctx, symTab)
		if refCount >= 2 || refCount == 0 {
			emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
				Code:        "WON-MEM-002",
				Severity:    "error",
				Message:     fmt.Sprintf("%s '%s' (%s) 接收 %d 个引用输入并返回引用，候选来源或生命期不可判定", label, name, tctx.Format(retType), refCount),
				File:        span.File,
				Span:        span,
				Explanation: "ADR-011 规定无函数体可分析或函数指针边界在 >=2 个输入引用或无引用输入返回借用时必须报 WON-MEM-002（不隐式假定 &str 等为 Static）；请改为返回拥有所有权值。",
			})
			return
		}
		for _, leaf := range leaves {
			if leaf.IsMut && mutRefCount == 0 {
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-MEM-004",
					Severity:    "error",
					Message:     fmt.Sprintf("%s '%s' 无 &mut 引用输入却试图返回可变引用 (%s)", label, name, tctx.Format(retType)),
					File:        span.File,
					Span:        span,
					Explanation: "只有具备 &mut 独占可变权限的输入参数才可作为不透明接口 &mut 返回的潜在来源。",
				})
				return
			}
		}
	}

	var scanNestedFnTypes func(name string, span diagnostic.Span, tid types.TypeID, visited map[types.TypeID]bool)
	scanNestedFnTypes = func(name string, span diagnostic.Span, tid types.TypeID, visited map[types.TypeID]bool) {
		if tid == types.Invalid || visited[tid] {
			return
		}
		visited[tid] = true
		info := tctx.Info(tid)
		switch info.Kind {
		case types.KindFn:
			checkFnType(name, span, info.Params, info.Return, "不透明函数指针签名")
		case types.KindTuple, types.KindArray, types.KindSlice:
			scanNestedFnTypes(name, span, info.Elem, visited)
			for _, el := range info.Elems {
				scanNestedFnTypes(name, span, el, visited)
			}
		case types.KindRef, types.KindRawPtr:
			scanNestedFnTypes(name, span, info.Elem, visited)
		}
	}

	for _, sym := range symTab.All {
		if sym == nil {
			continue
		}
		// 1) 无 CFG 函数体的声明函数/方法（跳过已由 WON-SEM-010 拦截的 Trait 或未知外围内建）
		if (sym.Kind == symbols.SymFunc || sym.Kind == symbols.SymMethod) && progCFG.BySymbol[sym.ID] == nil {
			nParams := sym.EffectiveParamCount()
			effParams := make([]types.TypeID, nParams)
			for i := 0; i < nParams; i++ {
				effParams[i] = sym.EffectiveParamType(i, tctx)
			}
			checkFnType(sym.Name, sym.Span, effParams, sym.ReturnType, "无函数体可调用对象")
			continue
		}

		// 2) 函数指针类型参数、局部变量、全局变量及结构体/枚举成员中的函数指针字段
		if sym.Kind != symbols.SymFunc && sym.Kind != symbols.SymMethod && sym.Kind != symbols.SymBuiltin && sym.Type != types.Invalid {
			scanNestedFnTypes(sym.Name, sym.Span, sym.Type, make(map[types.TypeID]bool, 4))
		}
		if sym.Kind == symbols.SymStruct {
			for _, f := range sym.Fields {
				scanNestedFnTypes(sym.Name+"."+f.Name, f.Span, f.Type, make(map[types.TypeID]bool, 4))
			}
		}
		if sym.Kind == symbols.SymEnum {
			for _, v := range sym.Variants {
				for _, tt := range v.TupleTypes {
					scanNestedFnTypes(sym.Name+"::"+v.Name, v.Span, tt, make(map[types.TypeID]bool, 4))
				}
				for _, sf := range v.StructFields {
					scanNestedFnTypes(sym.Name+"::"+v.Name+"."+sf.Name, sf.Span, sf.Type, make(map[types.TypeID]bool, 4))
				}
			}
		}
	}
}

func countRefParamCapabilities(paramTypes []types.TypeID, tctx *types.Context, symTab *symbols.Table) (refCount int, mutRefCount int) {
	for _, pt := range paramTypes {
		if typeHasRef(tctx, nil, pt, symTab) {
			refCount++
			leaves, _ := collectTypeRefLeaves(tctx, symTab, pt, "")
			for _, leaf := range leaves {
				if leaf.IsMut {
					mutRefCount++
					break
				}
			}
		}
	}
	return refCount, mutRefCount
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

func mergeOriginIntoSummary(orig OriginSet, flow *FuncLoanFlow, params map[int]bool, hasStatic, hasUnknown *bool) {
	for pIdx := range orig.Params {
		params[pIdx] = true
	}
	for lid := range orig.Loans {
		for _, tgt := range resolveLoanUnderlyingTargets(flow, lid, nil) {
			if tgt.paramIdx >= 0 {
				params[tgt.paramIdx] = true
			} else if tgt.unknown {
				*hasUnknown = true
			}
		}
	}
	if orig.Static {
		*hasStatic = true
	}
	if orig.Unknown {
		*hasUnknown = true
	}
}

func extractReturnLeafOrigin(fwd *carrierEnv, st cfg.Statement, leafPath string) OriginSet {
	if leafPath == "" {
		return extractReturnOrigin(fwd, st)
	}
	if st.SrcTemp != cfg.InvalidValue {
		if fmap, ok := fwd.aggFields[st.SrcTemp]; ok {
			if o, ok := fmap[leafPath]; ok {
				return o.clone()
			}
		}
	}
	if st.Place.RootSymID != 0 {
		subPlace := placeForFieldPath(st.Place, leafPath, st.Span)
		if o := lookupPlaceOrigins(fwd, subPlace); !o.isEmpty() {
			return o
		}
	}
	return extractReturnOrigin(fwd, st)
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
				if len(sum.FieldProvs) == 1 && sum.FieldProvs[""] != nil {
					orig := extractReturnOrigin(fwd, st)
					fp := sum.FieldProvs[""]
					mergeOriginIntoSummary(orig, flow, sum.ParamIndices, &sum.HasStatic, &sum.Unknown)
					mergeOriginIntoSummary(orig, flow, fp.ParamIndices, &fp.HasStatic, &fp.Unknown)
				} else {
					for _, path := range sum.SortedFieldKeys() {
						fp := sum.FieldProvs[path]
						orig := extractReturnLeafOrigin(fwd, st, path)
						mergeOriginIntoSummary(orig, flow, fp.ParamIndices, &fp.HasStatic, &fp.Unknown)
						mergeOriginIntoSummary(orig, flow, sum.ParamIndices, &sum.HasStatic, &sum.Unknown)
					}
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
	if sum != nil && sum.UnsupportedAggRef {
		var sp diagnostic.Span
		if fn.Decl != nil {
			sp = fn.Decl.GetSpan()
		}
		emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
			Code:        "WON-SEM-010",
			Severity:    "error",
			Message:     fmt.Sprintf("函数 '%s' 的返回类型 '%s' 包含暂不支持按字段投影分析的含引用枚举、数组或切片聚合", fn.Name, tctx.Format(fn.ReturnType)),
			File:        sp.File,
			Span:        sp,
			Explanation: "编译器目前仅对直接引用与结构体/元组返回提供精确的按字段候选溯源验证；含引用枚举/数组/切片返回按 Fail-Closed 策略拒止。",
		})
		return
	}

	reachable := computeReachable(fn)
	for bbIdx, blk := range fn.Blocks {
		if !reachable[bbIdx] {
			continue
		}
		fwd := flow.InStates[bbIdx].clone()
		for _, st := range blk.Statements {
			switch st.Kind {
			case cfg.OpReturn:
				if sum != nil && sum.ReturnsRef {
					checkReturnProvenanceLeaves(fn, st, fwd, flow, sum, tctx, symTab, report, emitted)
				}

			case cfg.OpCall:
				checkCallProvenanceAndDisjointness(progCFG, fn, st, fwd, flow, summaries, tctx, symTab, report, emitted)
			}
			stepSingleCarrierStmt(fn, st, fwd, flow, view)
		}
	}
}

func checkReturnProvenanceLeaves(
	fn *cfg.FuncCFG,
	st cfg.Statement,
	fwd *carrierEnv,
	flow *FuncLoanFlow,
	sum *FuncSummary,
	tctx *types.Context,
	symTab *symbols.Table,
	report *diagnostic.Report,
	emitted map[diagDedupKey]bool,
) {
	if len(sum.FieldProvs) == 0 || (len(sum.FieldProvs) == 1 && sum.FieldProvs[""] != nil) {
		orig := extractReturnOrigin(fwd, st)
		checkReturnOriginsValid(fn, st, "", sum.ReturnIsMut, orig, flow, tctx, symTab, report, emitted)
		return
	}
	for _, path := range sum.SortedFieldKeys() {
		fp := sum.FieldProvs[path]
		orig := extractReturnLeafOrigin(fwd, st, path)
		if !checkReturnOriginsValid(fn, st, path, fp.IsMut, orig, flow, tctx, symTab, report, emitted) {
			return
		}
	}
}

func checkReturnOriginsValid(
	fn *cfg.FuncCFG,
	st cfg.Statement,
	fieldPath string,
	isMut bool,
	orig OriginSet,
	flow *FuncLoanFlow,
	tctx *types.Context,
	symTab *symbols.Table,
	report *diagnostic.Report,
	emitted map[diagDedupKey]bool,
) bool {
	if fn == nil || flow == nil {
		emitInternalFlowDiag(fn, report, emitted, st.Span, "返回溯源校验缺少 FuncCFG 或 FuncLoanFlow")
		return false
	}

	for _, lid := range orig.SortedLoans() {
		if lid == cfg.InvalidLoan {
			emitInternalFlowDiag(fn, report, emitted, st.Span, "返回溯源集合包含无效 LoanID (L0)")
			return false
		}
		li := flow.LoanByID[lid]
		if li == nil {
			emitInternalFlowDiag(fn, report, emitted, st.Span, fmt.Sprintf("返回溯源集合引用了不存在的借用 L%d", lid))
			return false
		}
		if flow.FuncSymID != 0 && li.FuncSymID != flow.FuncSymID {
			emitInternalFlowDiag(fn, report, emitted, st.Span, fmt.Sprintf("返回溯源集合包含跨函数借用 L%d", lid))
			return false
		}
		targets := resolveLoanUnderlyingTargets(flow, lid, nil)
		for _, tgt := range targets {
			if tgt.unknown {
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-MEM-003",
					Severity:    "error",
					Message:     fmt.Sprintf("函数 '%s' 试图返回源自裸指针解引用或未知生命周期的引用", fn.Name),
					File:        st.Span.File,
					Span:        st.Span,
					Explanation: fmt.Sprintf("位于 %d:%d 的借用不具备可证的输入参数或静态生命周期来源，禁止通过安全引用返回逃逸。", li.Span.Start.Line, li.Span.Start.Column),
				})
				return false
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
				return false
			}
			if isMut && !li.IsMut {
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-MEM-004",
					Severity:    "error",
					Message:     fmt.Sprintf("不能将共享不可变借用作为函数 '%s' 的 '&mut' 引用返回", fn.Name),
					File:        st.Span.File,
					Span:        st.Span,
					Explanation: "不可变借用源无法升级为可变返回引用。",
				})
				return false
			}
			// 同时验证该借用穿透的父参数具备有效元数据（及可变返回所需的独占权限）
			pOut := validateParamDerefChainAuthority(fn, tgt.paramIdx, tgt.place, tctx, symTab)
			if pOut.Internal {
				emitInternalFlowDiag(fn, report, emitted, st.Span, pOut.Detail)
				return false
			}
			if isMut && pOut.Authority == AuthorityShared {
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-MEM-004",
					Severity:    "error",
					Message:     fmt.Sprintf("不能将源自不可变参数的借用作为函数 '%s' 的 '&mut' 返回", fn.Name),
					File:        st.Span.File,
					Span:        st.Span,
					Explanation: "底层来源形参仅具备共享 (&T) 权限，禁止升级为 &mut 返回。",
				})
				return false
			}
		}
	}

	if orig.Unknown {
		emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
			Code:        "WON-MEM-003",
			Severity:    "error",
			Message:     fmt.Sprintf("函数 '%s' 返回值包含无法证明来源安全性的引用成员 (%s)", fn.Name, fieldPath),
			File:        st.Span.File,
			Span:        st.Span,
			Explanation: "返回路径包含不可证的引用来源，编译器按 Fail-Closed 策略拒绝悬垂风险。",
		})
		return false
	}

	if isMut && orig.Static {
		emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
			Code:        "WON-MEM-004",
			Severity:    "error",
			Message:     fmt.Sprintf("函数 '%s' 不能将静态不可变引用作为 '&mut' 返回", fn.Name),
			File:        st.Span.File,
			Span:        st.Span,
			Explanation: "Static 字符串字面量等静态引用为只读共享语义，不能作为 &mut 返回。",
		})
		return false
	}

	paramCands := orig.SortedParams()
	if len(paramCands) > 0 {
		if fn.FuncSym == nil || tctx == nil {
			emitInternalFlowDiag(fn, report, emitted, st.Span, "验证参数返回溯源时缺少 FuncSym 或 TypeContext")
			return false
		}
		paramCount := fn.FuncSym.EffectiveParamCount()
		for _, pIdx := range paramCands {
			if pIdx < 0 || pIdx >= paramCount {
				emitInternalFlowDiag(fn, report, emitted, st.Span, fmt.Sprintf("返回溯源形参下标 %d 超出有效范围 [0, %d)", pIdx, paramCount))
				return false
			}
			pt := fn.FuncSym.EffectiveParamType(pIdx, tctx)
			if pt == types.Invalid {
				emitInternalFlowDiag(fn, report, emitted, st.Span, fmt.Sprintf("返回溯源形参 #%d 缺少有效类型 (Invalid)", pIdx))
				return false
			}
			pInfo := tctx.Info(pt)
			if isMut {
				pLabel := fn.FuncSym.EffectiveParamName(pIdx)
				if pLabel == "" {
					pLabel = fmt.Sprintf("#%d", pIdx+1)
				}
				switch pInfo.Kind {
				case types.KindRef:
					if !pInfo.IsMut {
						emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
							Code:        "WON-MEM-004",
							Severity:    "error",
							Message:     fmt.Sprintf("函数 '%s' 不能将不可变引用参数 '%s' 作为 '&mut' 返回", fn.Name, pLabel),
							File:        st.Span.File,
							Span:        st.Span,
							Explanation: "只有具备 &mut 独占可变权限的来源才可作为 &mut 返回候选。",
						})
						return false
					}
				case types.KindRawPtr:
					emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
						Code:        "WON-MEM-003",
						Severity:    "error",
						Message:     fmt.Sprintf("函数 '%s' 不能将原生指针参数 '%s' 直接作为安全 '&mut' 返回", fn.Name, pLabel),
						File:        st.Span.File,
						Span:        st.Span,
						Explanation: "原生指针来源缺乏安全生命周期保证，禁止直接逃逸为安全引用返回值。",
					})
					return false
				default:
					// 若参数本身为按值传入且包含引用字段的结构体/元组，只有其内部确实携带 &mut 叶子时才可起源为 &mut 返回
					leaves, _ := collectTypeRefLeaves(tctx, symTab, pt, "")
					hasMutLeaf := false
					for _, lf := range leaves {
						if lf.IsMut {
							hasMutLeaf = true
							break
						}
					}
					if !hasMutLeaf {
						emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
							Code:        "WON-MEM-004",
							Severity:    "error",
							Message:     fmt.Sprintf("函数 '%s' 参数 '%s' 不具备可导出的 '&mut' 来源权限", fn.Name, pLabel),
							File:        st.Span.File,
							Span:        st.Span,
							Explanation: "按值或仅包含只读引用的参数无法授权 &mut 引用返回。",
						})
						return false
					}
				}
				if st.Place.RootSymID != 0 && len(st.Place.Projections) > 0 {
					pOut := validateParamDerefChainAuthority(fn, pIdx, st.Place, tctx, symTab)
					if pOut.Internal {
						emitInternalFlowDiag(fn, report, emitted, st.Span, pOut.Detail)
						return false
					}
					if pOut.Authority == AuthorityShared {
						emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
							Code:        "WON-MEM-004",
							Severity:    "error",
							Message:     fmt.Sprintf("函数 '%s' 返回路径包含只读共享外层引用，禁止作为 '&mut' 返回", fn.Name),
							File:        st.Span.File,
							Span:        st.Span,
							Explanation: "经由共享外层引用的投影仅具只读权限。",
						})
						return false
					}
					if pOut.Authority == AuthorityUnsafeRawPtr {
						emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
							Code:        "WON-MEM-003",
							Severity:    "error",
							Message:     fmt.Sprintf("函数 '%s' 试图通过原生指针路径返回安全 '&mut' 引用", fn.Name),
							File:        st.Span.File,
							Span:        st.Span,
							Explanation: "原生指针解引用来源禁止直接逃逸为安全引用返回值。",
						})
						return false
					}
				}
			}
		}
	}
	return true
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

	// 1. 若调用不透明/无体目标并返回引用（排除已分析内建如 .as_bytes() 与枚举变体构造器 Result::Ok/Err, Option::Some/None）
	if st.Callee == nil || progCFG.BySymbol[st.Callee.ID] == nil {
		if st.Type == types.Invalid {
			emitInternalFlowDiag(fn, report, emitted, st.Span, fmt.Sprintf("调用 '%s' 携带非零 ValTemp v%d 但缺少返回类型元数据", st.CalleeName, st.ValTemp))
			return
		}
		if (st.Callee == nil || st.Callee.Kind != symbols.SymBuiltin) && !isPureEnumVariantCall(st) && typeHasRef(tctx, nil, st.Type, symTab) {
			refArgCount := 0
			for _, at := range st.ArgTemps {
				if at != cfg.InvalidValue {
					if o, ok := fwd.temps[at]; ok && !o.isEmpty() {
						refArgCount++
					}
				}
			}
			if refArgCount >= 2 || refArgCount == 0 {
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-MEM-002",
					Severity:    "error",
					Message:     fmt.Sprintf("调用不透明/无体函数 '%s' 返回引用时（传入 %d 个引用实参）来源不可判定", st.CalleeName, refArgCount),
					File:        st.Span.File,
					Span:        st.Span,
					Explanation: "无函数体或函数指针调用在返回引用且引用实参个数为 0 或 >=2 时无法确定唯一来源 (WON-MEM-002)。",
				})
				return
			}
		}
		return
	}

	calSum := summaries[st.Callee.ID]
	if calSum == nil || len(calSum.FieldProvs) == 0 {
		return
	}

	resolveCandidateArgTargets := func(pIdx int, requireMutAuth bool) ([]resolvedTarget, authorityCheckOutcome) {
		if st.Callee == nil {
			return nil, internalAuthorityError("调用摘要存在但 Callee 符号为空")
		}
		effCount := st.Callee.EffectiveParamCount()
		if pIdx < 0 || pIdx >= effCount {
			return nil, internalAuthorityError(fmt.Sprintf("摘要候选下标 %d 超出被调函数 '%s' 规范参数范围 [0, %d)", pIdx, st.CalleeName, effCount))
		}
		if pIdx >= len(st.ArgTemps) {
			return nil, internalAuthorityError(fmt.Sprintf("摘要候选下标 %d 超出调用 '%s' 实参切片长度 %d", pIdx, st.CalleeName, len(st.ArgTemps)))
		}
		argT := st.ArgTemps[pIdx]
		if argT == cfg.InvalidValue {
			return nil, internalAuthorityError(fmt.Sprintf("调用 '%s' 候选引用实参 #%d 缺少有效临时值 ValueID", st.CalleeName, pIdx))
		}
		argOrig, exists := fwd.temps[argT]
		if !exists || argOrig.isEmpty() {
			return nil, internalAuthorityError(fmt.Sprintf("调用 '%s' 候选实参 v%d 缺少调用点载体溯源 OriginSet", st.CalleeName, argT))
		}

		// 校验实参原点集合有效性（及可变候选的可变访问权限）
		oCheck := validateCarrierOriginSet(fn, flow, argOrig, cfg.Place{}, true, requireMutAuth, tctx, symTab)
		if oCheck.Internal {
			return nil, oCheck
		}
		if requireMutAuth && !oCheck.Authority.PermitsMutableAccess() {
			return nil, sharedAuthority(fmt.Sprintf("调用 '%s' 候选实参 #%d 不具备 &mut 访问权限", st.CalleeName, pIdx))
		}

		var tgts []resolvedTarget
		for _, lid := range argOrig.SortedLoans() {
			resolved := resolveLoanUnderlyingTargets(flow, lid, nil)
			if len(resolved) == 0 {
				return nil, internalAuthorityError(fmt.Sprintf("调用 '%s' 候选实参借用 L%d 未解析出任何底层目标", st.CalleeName, lid))
			}
			tgts = append(tgts, resolved...)
		}
		for _, parIdx := range argOrig.SortedParams() {
			tgts = append(tgts, resolvedTarget{paramIdx: parIdx})
		}
		if argOrig.Unknown {
			tgts = append(tgts, resolvedTarget{paramIdx: -1, unknown: true})
		}
		if argOrig.Static {
			if requireMutAuth {
				return nil, sharedAuthority(fmt.Sprintf("调用 '%s' 可变候选实参 #%d 不得源自 Static", st.CalleeName, pIdx))
			}
			// 静态共享来源可与调用者局部可变目标区分（仅以唯一哨兵 RootName 标识静态来源，不伪造为空切片）
			tgts = append(tgts, resolvedTarget{place: cfg.Place{RootName: "<static>"}, paramIdx: -1})
		}
		if len(tgts) == 0 {
			return nil, internalAuthorityError(fmt.Sprintf("调用 '%s' 候选实参 #%d 底层目标集合为空，拒绝以 targetsOverlap(nil, nil) 认证互不重叠", st.CalleeName, pIdx))
		}
		return tgts, okAuthority(oCheck.Authority)
	}

	keys := calSum.SortedFieldKeys()
	resolvedCache := make(map[int][]resolvedTarget)
	getTargets := func(pIdx int, requireMutAuth bool) ([]resolvedTarget, bool) {
		tgts, out := resolveCandidateArgTargets(pIdx, requireMutAuth)
		if out.Internal {
			emitInternalFlowDiag(fn, report, emitted, st.Span, out.Detail)
			return nil, false
		}
		if requireMutAuth && !out.Authority.PermitsMutableAccess() {
			emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
				Code:        "WON-MEM-004",
				Severity:    "error",
				Message:     out.Detail,
				File:        st.Span.File,
				Span:        st.Span,
				Explanation: "返回 &mut 借用候选位置要求全部可能实参具备独占 &mut 访问权限。",
			})
			return nil, false
		}
		resolvedCache[pIdx] = tgts
		return tgts, true
	}

	// 2a. 先验证并解析全部候选实参，对单返回位置（标量 &mut 或聚合的某个 &mut 成员）若有 >=2 个候选实参则证明两两互不重叠
	for _, k := range keys {
		fp := calSum.FieldProvs[k]
		if fp == nil {
			continue
		}
		cands := fp.SortedParamIndices()
		for _, pIdx := range cands {
			if _, ok := getTargets(pIdx, fp.IsMut); !ok {
				return
			}
		}
		if !fp.IsMut {
			continue
		}
		for i := 0; i < len(cands); i++ {
			ti := resolvedCache[cands[i]]
			for j := i + 1; j < len(cands); j++ {
				tj := resolvedCache[cands[j]]
				if len(ti) == 0 || len(tj) == 0 {
					emitInternalFlowDiag(fn, report, emitted, st.Span, fmt.Sprintf("调用 '%s' 候选目标解析结果为空", st.CalleeName))
					return
				}
				if targetsOverlap(ti, tj) {
					emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
						Code:        "WON-MEM-005",
						Severity:    "error",
						Message:     fmt.Sprintf("调用 '%s' 返回可变引用成员 '%s' 时第 %d 与第 %d 个候选实参可能指向重叠存储", st.CalleeName, k, cands[i]+1, cands[j]+1),
						File:        st.Span.File,
						Span:        st.Span,
						Explanation: "Strategy B 要求可变返回候选集合中的所有候选实参必须在底层两两互不重叠 (disjoint)，否则拒绝以防产生双可变别名。",
					})
					return
				}
			}
		}
	}

	// 2b. 聚合多字段跨成员别名校验：若字段 k1 与 k2 (k1 != k2) 至少其一为 &mut，则来自不同参数 p1 != p2 的候选实参也不得指向重叠存储
	for i := 0; i < len(keys); i++ {
		fp1 := calSum.FieldProvs[keys[i]]
		if fp1 == nil {
			continue
		}
		cands1 := fp1.SortedParamIndices()
		for j := i + 1; j < len(keys); j++ {
			fp2 := calSum.FieldProvs[keys[j]]
			if fp2 == nil || (!fp1.IsMut && !fp2.IsMut) {
				continue
			}
			cands2 := fp2.SortedParamIndices()
			for _, p1 := range cands1 {
				t1 := resolvedCache[p1]
				for _, p2 := range cands2 {
					if p1 == p2 {
						continue
					}
					t2 := resolvedCache[p2]
					if len(t1) == 0 || len(t2) == 0 {
						emitInternalFlowDiag(fn, report, emitted, st.Span, fmt.Sprintf("调用 '%s' 跨字段候选目标解析结果为空", st.CalleeName))
						return
					}
					if targetsOverlap(t1, t2) {
						code := "WON-MEM-004"
						if fp1.IsMut && fp2.IsMut {
							code = "WON-MEM-005"
						}
						emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
							Code:        code,
							Severity:    "error",
							Message:     fmt.Sprintf("调用 '%s' 返回含可变引用聚合时成员 '%s' 与 '%s' 的候选实参存在重叠冲突", st.CalleeName, keys[i], keys[j]),
							File:        st.Span.File,
							Span:        st.Span,
							Explanation: "返回值聚合中的可变成员与其他引用成员不得指向同一底层存储位置。",
						})
						return
					}
				}
			}
		}
	}
}

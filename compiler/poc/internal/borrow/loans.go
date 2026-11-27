package borrow

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// OriginSet 描述单个引用临时值或存储载体位置在特定控制流点可携带的借用、形参与静态溯源集合。
// 采用有限确定性集合格，满足单调有限高迭代收敛保证。
type OriginSet struct {
	Loans      map[cfg.LoanID]bool
	Params     map[int]bool
	ParamPaths map[int]map[string]cfg.Place
	Static     bool
	Unknown    bool
}

func newOriginSet() OriginSet {
	return OriginSet{
		Loans:      make(map[cfg.LoanID]bool),
		Params:     make(map[int]bool),
		ParamPaths: make(map[int]map[string]cfg.Place),
	}
}

func (o *OriginSet) recordParamPath(pIdx int, place cfg.Place) {
	if o.Params == nil {
		o.Params = make(map[int]bool)
	}
	o.Params[pIdx] = true
	if o.ParamPaths == nil {
		o.ParamPaths = make(map[int]map[string]cfg.Place)
	}
	pm := o.ParamPaths[pIdx]
	if pm == nil {
		pm = make(map[string]cfg.Place, 1)
		o.ParamPaths[pIdx] = pm
	}
	pm[place.Key()] = place
}

func (o OriginSet) isEmpty() bool {
	return len(o.Loans) == 0 && len(o.Params) == 0 && !o.Static && !o.Unknown
}

func (o OriginSet) clone() OriginSet {
	cp := OriginSet{
		Loans:      make(map[cfg.LoanID]bool, len(o.Loans)),
		Params:     make(map[int]bool, len(o.Params)),
		ParamPaths: make(map[int]map[string]cfg.Place, len(o.ParamPaths)),
		Static:     o.Static,
		Unknown:    o.Unknown,
	}
	for id := range o.Loans {
		cp.Loans[id] = true
	}
	for p := range o.Params {
		cp.Params[p] = true
	}
	for p, pm := range o.ParamPaths {
		inner := make(map[string]cfg.Place, len(pm))
		for k, pl := range pm {
			inner[k] = pl
		}
		cp.ParamPaths[p] = inner
	}
	return cp
}

func (o *OriginSet) merge(other OriginSet) {
	if o.Loans == nil {
		o.Loans = make(map[cfg.LoanID]bool)
	}
	if o.Params == nil {
		o.Params = make(map[int]bool)
	}
	if o.ParamPaths == nil && len(other.ParamPaths) > 0 {
		o.ParamPaths = make(map[int]map[string]cfg.Place, len(other.ParamPaths))
	}
	for id := range other.Loans {
		o.Loans[id] = true
	}
	for p := range other.Params {
		o.Params[p] = true
	}
	for p, pm := range other.ParamPaths {
		dst := o.ParamPaths[p]
		if dst == nil {
			dst = make(map[string]cfg.Place, len(pm))
			o.ParamPaths[p] = dst
		}
		for k, pl := range pm {
			dst[k] = pl
		}
	}
	if other.Static {
		o.Static = true
	}
	if other.Unknown {
		o.Unknown = true
	}
}

func (o OriginSet) equals(other OriginSet) bool {
	if o.Static != other.Static || o.Unknown != other.Unknown || len(o.Loans) != len(other.Loans) || len(o.Params) != len(other.Params) || len(o.ParamPaths) != len(other.ParamPaths) {
		return false
	}
	for id := range o.Loans {
		if !other.Loans[id] {
			return false
		}
	}
	for p := range o.Params {
		if !other.Params[p] {
			return false
		}
	}
	for p, pm := range o.ParamPaths {
		opm := other.ParamPaths[p]
		if len(pm) != len(opm) {
			return false
		}
		for k := range pm {
			if _, ok := opm[k]; !ok {
				return false
			}
		}
	}
	return true
}

// SortedLoans 按确定性升序返回当前集合中包含的全部 LoanID。
func (o OriginSet) SortedLoans() []cfg.LoanID {
	res := make([]cfg.LoanID, 0, len(o.Loans))
	for id := range o.Loans {
		res = append(res, id)
	}
	sort.Slice(res, func(i, j int) bool { return res[i] < res[j] })
	return res
}

// SortedParams 按确定性升序返回当前集合中包含的全部参数溯源下标。
func (o OriginSet) SortedParams() []int {
	res := make([]int, 0, len(o.Params))
	for p := range o.Params {
		res = append(res, p)
	}
	sort.Ints(res)
	return res
}

// LoanInfo 记录单次借用创建事件的完整类型化身份、重借用父关系与关联载体。
type LoanInfo struct {
	ID           cfg.LoanID
	FuncSymID    symbols.SymbolID
	Kind         cfg.BorrowKind
	Place        cfg.Place
	Type         types.TypeID
	IsMut        bool
	IsReborrow   bool
	ParentPlace  cfg.Place
	ParentLoans  map[cfg.LoanID]bool
	ParentParams map[int]bool
	FromRawPtr   bool
	CreatedAt    cfg.ProgramPoint
	Span         diagnostic.Span
	InitialTemp  cfg.ValueID
	Carriers     map[symbols.SymbolID]bool
	CarrierTemps map[cfg.ValueID]bool
}

// carrierEnv 记录控制流点上所有临时值 (ValueID) 与存储位置 (Place.Key) 当前持有的引用溯源集合。
type carrierEnv struct {
	temps     map[cfg.ValueID]OriginSet
	places    map[string]OriginSet
	aggFields map[cfg.ValueID]map[string]OriginSet
}

func newCarrierEnv() *carrierEnv {
	return &carrierEnv{
		temps:     make(map[cfg.ValueID]OriginSet),
		places:    make(map[string]OriginSet),
		aggFields: make(map[cfg.ValueID]map[string]OriginSet),
	}
}

func (e *carrierEnv) clone() *carrierEnv {
	cp := &carrierEnv{
		temps:     make(map[cfg.ValueID]OriginSet, len(e.temps)),
		places:    make(map[string]OriginSet, len(e.places)),
		aggFields: make(map[cfg.ValueID]map[string]OriginSet, len(e.aggFields)),
	}
	for k, v := range e.temps {
		cp.temps[k] = v.clone()
	}
	for k, v := range e.places {
		cp.places[k] = v.clone()
	}
	for vID, fmap := range e.aggFields {
		inner := make(map[string]OriginSet, len(fmap))
		for fn, orig := range fmap {
			inner[fn] = orig.clone()
		}
		cp.aggFields[vID] = inner
	}
	return cp
}

func (e *carrierEnv) equals(other *carrierEnv) bool {
	if len(e.temps) != len(other.temps) || len(e.places) != len(other.places) || len(e.aggFields) != len(other.aggFields) {
		return false
	}
	for k, v := range e.temps {
		if ov, ok := other.temps[k]; !ok || !v.equals(ov) {
			return false
		}
	}
	for k, v := range e.places {
		if ov, ok := other.places[k]; !ok || !v.equals(ov) {
			return false
		}
	}
	for vID, fmap := range e.aggFields {
		ofmap, ok := other.aggFields[vID]
		if !ok || len(fmap) != len(ofmap) {
			return false
		}
		for fn, orig := range fmap {
			if oorig, fok := ofmap[fn]; !fok || !orig.equals(oorig) {
				return false
			}
		}
	}
	return true
}

func joinCarrierEnvs(envs []*carrierEnv) *carrierEnv {
	res := newCarrierEnv()
	for _, env := range envs {
		if env == nil {
			continue
		}
		for v, o := range env.temps {
			cur := res.temps[v]
			cur.merge(o)
			res.temps[v] = cur
		}
		for k, o := range env.places {
			cur := res.places[k]
			cur.merge(o)
			res.places[k] = cur
		}
		for vID, fmap := range env.aggFields {
			dst := res.aggFields[vID]
			if dst == nil {
				dst = make(map[string]OriginSet, len(fmap))
				res.aggFields[vID] = dst
			}
			for fn, o := range fmap {
				cur := dst[fn]
				cur.merge(o)
				dst[fn] = cur
			}
		}
	}
	return res
}

// FuncLoanFlow 汇总函数内部全部显式 LoanID、重借用父子拓扑及各控制流节点的引用载体溯源流。
type FuncLoanFlow struct {
	FuncName     string
	FuncSymID    symbols.SymbolID
	Loans        []*LoanInfo
	LoanByID     map[cfg.LoanID]*LoanInfo
	TempOrigins  map[cfg.ValueID]OriginSet
	InStates     []*carrierEnv
	OutStates    []*carrierEnv
	Summaries    map[symbols.SymbolID]*FuncSummary
	FFIContracts map[string]bindgen.FFIFuncContract
}

// BuildLoanFlow 对单个函数 CFG 构建并校验 Phase 2B.2 Stage A 的借用身份表与引用载体前向流。
// 若发现重复/非法 LoanID、重借用缺少父关系或不动点未收敛，按 Fail-Closed 原则发出 WON-INT-001。
func BuildLoanFlow(fn *cfg.FuncCFG, view cfg.SemanticView, report *diagnostic.Report) *FuncLoanFlow {
	return buildLoanFlowWithSummaries(fn, view, nil, report)
}

func buildLoanFlowWithSummaries(fn *cfg.FuncCFG, view cfg.SemanticView, summaries map[symbols.SymbolID]*FuncSummary, report *diagnostic.Report) *FuncLoanFlow {
	return buildLoanFlowWithSummariesAndFFI(fn, view, summaries, nil, report)
}

func buildLoanFlowWithSummariesAndFFI(fn *cfg.FuncCFG, view cfg.SemanticView, summaries map[symbols.SymbolID]*FuncSummary, ffiContracts map[string]bindgen.FFIFuncContract, report *diagnostic.Report) *FuncLoanFlow {
	if fn == nil || len(fn.Blocks) == 0 || fn.Entry < 0 {
		return nil
	}
	flow := &FuncLoanFlow{
		FuncName:     fn.Name,
		LoanByID:     make(map[cfg.LoanID]*LoanInfo),
		TempOrigins:  make(map[cfg.ValueID]OriginSet),
		InStates:     make([]*carrierEnv, len(fn.Blocks)),
		OutStates:    make([]*carrierEnv, len(fn.Blocks)),
		Summaries:    summaries,
		FFIContracts: ffiContracts,
	}
	if fn.FuncSym != nil {
		flow.FuncSymID = fn.FuncSym.ID
	}

	var tctx *types.Context
	if view != nil {
		tctx = view.GetTypes()
	}

	// 1. 按确定性块顺序采集全部借用事件并校验 LoanID 唯一性与有效创建位置
	for bbIdx, blk := range fn.Blocks {
		for stIdx, st := range blk.Statements {
			if st.Kind != cfg.OpBorrowShared && st.Kind != cfg.OpBorrowMut && st.Kind != cfg.OpReborrow {
				continue
			}
			if st.LoanID == cfg.InvalidLoan {
				emitFlowViolation(fn, report, st.Span, "借用事件缺少有效的非零 LoanID")
				return nil
			}
			if _, dup := flow.LoanByID[st.LoanID]; dup {
				emitFlowViolation(fn, report, st.Span, fmt.Sprintf("函数 '%s' 内存在重复 LoanID L%d", fn.Name, st.LoanID))
				return nil
			}
			isMut := st.BorrowKind == cfg.BorrowMut || st.BorrowKind == cfg.BorrowReborrowMut
			fromRaw := false
			if !st.IsReborrow && st.Place.HasDeref() {
				if pref, hasD := st.Place.PrefixBeforeLastDeref(); hasD && tctx != nil && pref.ElemType != types.Invalid {
					if tctx.Info(pref.ElemType).Kind == types.KindRawPtr {
						fromRaw = true
					}
				}
			}
			li := &LoanInfo{
				ID:           st.LoanID,
				FuncSymID:    flow.FuncSymID,
				Kind:         st.BorrowKind,
				Place:        st.Place,
				Type:         st.Type,
				IsMut:        isMut,
				IsReborrow:   st.IsReborrow,
				ParentPlace:  st.ParentPlace,
				ParentLoans:  make(map[cfg.LoanID]bool),
				ParentParams: make(map[int]bool),
				FromRawPtr:   fromRaw,
				CreatedAt:    cfg.ProgramPoint{Block: cfg.BlockID(bbIdx), StmtIdx: stIdx},
				Span:         st.Span,
				InitialTemp:  st.ValTemp,
				Carriers:     make(map[symbols.SymbolID]bool),
				CarrierTemps: make(map[cfg.ValueID]bool),
			}
			if st.ValTemp != cfg.InvalidValue {
				li.CarrierTemps[st.ValTemp] = true
			}
			flow.LoanByID[li.ID] = li
			flow.Loans = append(flow.Loans, li)
		}
	}

	reachable := computeReachable(fn)
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
			emitFlowViolation(fn, report, sp, fmt.Sprintf("引用载体数据流不动点在 %d 轮迭代后仍未收敛", maxIterations))
			return nil
		}
		changed = false
		for bbIdx, blk := range fn.Blocks {
			bb := cfg.BlockID(bbIdx)
			if !reachable[bb] {
				continue
			}
			var merged *carrierEnv
			if bb == fn.Entry {
				merged = newCarrierEnv()
			} else {
				preds := make([]*carrierEnv, 0, len(blk.Predecessors))
				for _, pred := range blk.Predecessors {
					if reachable[pred] && flow.OutStates[pred] != nil {
						preds = append(preds, flow.OutStates[pred])
					}
				}
				merged = joinCarrierEnvs(preds)
			}
			flow.InStates[bb] = merged
			cur := merged.clone()
			transferCarrierBlock(fn, blk, cur, flow, view, false, nil)
			if flow.OutStates[bb] == nil || !flow.OutStates[bb].equals(cur) {
				flow.OutStates[bb] = cur
				changed = true
			}
		}
	}

	// 收敛后按确定性顺序采集所有借用的父关系与引用载体绑定，并验证 Stage A 结构不变量
	for bbIdx, blk := range fn.Blocks {
		bb := cfg.BlockID(bbIdx)
		if !reachable[bb] {
			continue
		}
		cur := flow.InStates[bb].clone()
		transferCarrierBlock(fn, blk, cur, flow, view, true, report)
	}

	sort.Slice(flow.Loans, func(i, j int) bool { return flow.Loans[i].ID < flow.Loans[j].ID })
	return flow
}

func emitFlowViolation(fn *cfg.FuncCFG, report *diagnostic.Report, span diagnostic.Span, detail string) {
	if report == nil {
		return
	}
	report.AddDiagnostic(diagnostic.Diagnostic{
		Code:        "WON-INT-001",
		Severity:    "error",
		Message:     fmt.Sprintf("借用载体流内部不变量违例 (函数 '%s'): %s", fn.Name, detail),
		File:        span.File,
		Span:        span,
		Explanation: "编译器在构建 Stage A 借用标识与引用载体流时检测到内部不一致，按 Fail-Closed 原则拒绝认证。",
	})
}

func transferCarrierBlock(fn *cfg.FuncCFG, blk *cfg.BasicBlock, env *carrierEnv, flow *FuncLoanFlow, view cfg.SemanticView, collect bool, report *diagnostic.Report) {
	var tctx *types.Context
	if view != nil {
		tctx = view.GetTypes()
	}

	for _, st := range blk.Statements {
		switch st.Kind {
		case cfg.OpStaticRef:
			if st.ValTemp != cfg.InvalidValue {
				o := env.temps[st.ValTemp]
				o.Static = true
				env.temps[st.ValTemp] = o
				if collect {
					cur := flow.TempOrigins[st.ValTemp]
					cur.merge(o)
					flow.TempOrigins[st.ValTemp] = cur
				}
			}

		case cfg.OpBorrowShared, cfg.OpBorrowMut, cfg.OpReborrow:
			li := flow.LoanByID[st.LoanID]
			o := newOriginSet()
			o.Loans[st.LoanID] = true
			if li != nil && li.FromRawPtr {
				o.Unknown = true
			}
			if st.IsReborrow {
				parentOrig := lookupReborrowParentOrigins(env, flow, st.ParentPlace)
				if collect && li != nil {
					if parentOrig.isEmpty() {
						emitFlowViolation(fn, report, st.Span, fmt.Sprintf("重借用 L%d 找不到父位置 '%s' 的引用载体溯源", st.LoanID, st.ParentPlace.String()))
					} else {
						for pid := range parentOrig.Loans {
							li.ParentLoans[pid] = true
						}
						for pIdx := range parentOrig.Params {
							li.ParentParams[pIdx] = true
						}
					}
				}
				// 子重借用继承父借用的参数（含完整投影路径）/静态/未知底层溯源，同时保留自身 LoanID
				for pIdx := range parentOrig.Params {
					o.Params[pIdx] = true
					if pm, hasPaths := parentOrig.ParamPaths[pIdx]; hasPaths && len(pm) > 0 {
						for _, sp := range pm {
							o.recordParamPath(pIdx, sp)
						}
					} else if st.ParentPlace.RootSymID != 0 {
						o.recordParamPath(pIdx, st.ParentPlace)
					}
				}
				if parentOrig.Static {
					o.Static = true
				}
				if parentOrig.Unknown {
					o.Unknown = true
				}
			}
			if st.ValTemp != cfg.InvalidValue {
				cur := env.temps[st.ValTemp]
				cur.merge(o)
				env.temps[st.ValTemp] = cur
				if collect {
					fc := flow.TempOrigins[st.ValTemp]
					fc.merge(cur)
					flow.TempOrigins[st.ValTemp] = fc
					for id := range cur.Loans {
						if l := flow.LoanByID[id]; l != nil {
							l.CarrierTemps[st.ValTemp] = true
						}
					}
				}
			}

		case cfg.OpCopy, cfg.OpMove:
			if st.ValTemp != cfg.InvalidValue {
				srcOrig := lookupPlaceOrigins(env, st.Place)
				cur := env.temps[st.ValTemp]
				cur.merge(srcOrig)
				env.temps[st.ValTemp] = cur
				// 若拷贝/移动的是包含字段载体的聚合值（例如 Struct/Tuple 局部变量），同步提取子字段溯源至 ValTemp
				if st.Place.RootSymID != 0 {
					prefix := st.Place.Key()
					for pk, pv := range env.places {
						if len(pk) > len(prefix) && pk[:len(prefix)] == prefix {
							var rel string
							switch pk[len(prefix)] {
							case '.':
								rel = pk[len(prefix)+1:]
							case '[':
								rel = pk[len(prefix):]
							}
							if rel != "" {
								dst := env.aggFields[st.ValTemp]
								if dst == nil {
									dst = make(map[string]OriginSet)
									env.aggFields[st.ValTemp] = dst
								}
								cf := dst[rel]
								cf.merge(pv)
								dst[rel] = cf
							}
						}
					}
				}
				if collect {
					fc := flow.TempOrigins[st.ValTemp]
					fc.merge(cur)
					flow.TempOrigins[st.ValTemp] = fc
					for id := range cur.Loans {
						if l := flow.LoanByID[id]; l != nil {
							l.CarrierTemps[st.ValTemp] = true
						}
					}
				}
			}
			if st.Kind == cfg.OpMove && st.Place.RootSymID != 0 {
				clearPlaceOrigins(env, st.Place)
			}

		case cfg.OpAggregate:
			if st.ValTemp != cfg.InvalidValue {
				unionOrig := newOriginSet()
				fmap := env.aggFields[st.ValTemp]
				if fmap == nil {
					fmap = make(map[string]OriginSet, len(st.AggTemps))
					env.aggFields[st.ValTemp] = fmap
				}
				for _, item := range st.AggTemps {
					if item.Temp == cfg.InvalidValue {
						continue
					}
					itemOrig := env.temps[item.Temp]
					unionOrig.merge(itemOrig)
					key := item.FieldName
					if key == "" {
						key = fmt.Sprintf("[%d]", item.Index)
					}
					curF := fmap[key]
					curF.merge(itemOrig)
					fmap[key] = curF
					// 嵌套聚合字段展开为完整投影路径（如 box.left / outer.[0]）
					if subMap, hasSub := env.aggFields[item.Temp]; hasSub {
						for subKey, subOrig := range subMap {
							fullKey := joinAggFieldKey(key, subKey)
							cf := fmap[fullKey]
							cf.merge(subOrig)
							fmap[fullKey] = cf
						}
					}
				}
				cur := env.temps[st.ValTemp]
				cur.merge(unionOrig)
				env.temps[st.ValTemp] = cur
				if collect {
					fc := flow.TempOrigins[st.ValTemp]
					fc.merge(cur)
					flow.TempOrigins[st.ValTemp] = fc
				}
			}

		case cfg.OpTempAssign:
			if st.ValTemp != cfg.InvalidValue && st.SrcTemp != cfg.InvalidValue {
				srcOrig := env.temps[st.SrcTemp]
				cur := env.temps[st.ValTemp]
				cur.merge(srcOrig)
				env.temps[st.ValTemp] = cur
				if fmap, ok := env.aggFields[st.SrcTemp]; ok {
					dst := env.aggFields[st.ValTemp]
					if dst == nil {
						dst = make(map[string]OriginSet, len(fmap))
						env.aggFields[st.ValTemp] = dst
					}
					for fnKey, o := range fmap {
						cf := dst[fnKey]
						cf.merge(o)
						dst[fnKey] = cf
					}
				}
				if collect {
					fc := flow.TempOrigins[st.ValTemp]
					fc.merge(cur)
					flow.TempOrigins[st.ValTemp] = fc
					for id := range cur.Loans {
						if l := flow.LoanByID[id]; l != nil {
							l.CarrierTemps[st.ValTemp] = true
						}
					}
				}
			}

		case cfg.OpInitAssign, cfg.OpAssign, cfg.OpReinit:
			meta := fn.Locals[st.Place.RootSymID]
			if meta != nil && meta.IsParam && st.Kind == cfg.OpInitAssign && st.SrcTemp == cfg.InvalidValue {
				// 参数入口绑定：若参数自身为引用或包含引用成员，注入 Param(idx) 及精确投影路径溯源
				if meta.Type != types.Invalid && tctx != nil && typeHasRef(tctx, nil, meta.Type, view.GetSymbols()) {
					o := newOriginSet()
					o.recordParamPath(meta.ParamIdx, st.Place)
					env.places[st.Place.Key()] = o
					leaves, _ := collectTypeRefLeaves(tctx, view.GetSymbols(), meta.Type, "")
					for _, leaf := range leaves {
						if leaf.Path != "" {
							childPlace := placeForFieldPath(st.Place, leaf.Path, st.Span)
							co := newOriginSet()
							co.recordParamPath(meta.ParamIdx, childPlace)
							env.places[childPlace.Key()] = co
						}
					}
				}
				continue
			}
			// 局部或字段赋值写入
			if st.SrcTemp != cfg.InvalidValue {
				var assignOrig OriginSet
				// 若来自元组/结构体模式解构字段投影，精确提取对应子域溯源
				if len(st.ParentPlace.Projections) == 1 {
					if fmap, hasAgg := env.aggFields[st.SrcTemp]; hasAgg {
						p0 := st.ParentPlace.Projections[0]
						key := p0.FieldName
						if p0.Kind == cfg.ProjTupleField {
							key = fmt.Sprintf("[%d]", p0.FieldIndex)
						}
						if sub, ok := fmap[key]; ok {
							assignOrig = sub.clone()
						}
					}
				}
				if assignOrig.isEmpty() {
					assignOrig = env.temps[st.SrcTemp].clone()
				}
				env.places[st.Place.Key()] = assignOrig
				// 若来源值本身具备字段分解结构（如 let w = RefBox { a: &x, b: &mut y }），一并展开至多级投影载体
				if fmap, hasAgg := env.aggFields[st.SrcTemp]; hasAgg && len(st.ParentPlace.Projections) == 0 {
					for fKey, fOrig := range fmap {
						childPlace := placeForFieldPath(st.Place, fKey, st.Span)
						env.places[childPlace.Key()] = fOrig.clone()
					}
				}
				if collect {
					for id := range assignOrig.Loans {
						if l := flow.LoanByID[id]; l != nil {
							l.Carriers[st.Place.RootSymID] = true
						}
					}
				}
			} else if st.Kind == cfg.OpAssign || st.Kind == cfg.OpReinit {
				delete(env.places, st.Place.Key())
			}

		case cfg.OpCall:
			if st.ValTemp != cfg.InvalidValue {
				retOrig := newOriginSet()
				usedSummary := false
				if st.Callee != nil && flow.Summaries != nil {
					if sum, ok := flow.Summaries[st.Callee.ID]; ok && sum != nil {
						usedSummary = true
						for pIdx := range sum.ParamIndices {
							if pIdx >= 0 && pIdx < len(st.ArgTemps) && st.ArgTemps[pIdx] != cfg.InvalidValue {
								if argOrig, ok := env.temps[st.ArgTemps[pIdx]]; ok {
									retOrig.merge(argOrig)
								}
							}
						}
						if sum.HasStatic {
							retOrig.Static = true
						}
						if sum.Unknown {
							retOrig.Unknown = true
						}
						// 若摘要携带按投影路径拆分的聚合字段溯源，逐字段实例化至 aggFields
						if len(sum.FieldProvs) > 0 {
							for fPath, fp := range sum.FieldProvs {
								if fPath == "" || fp == nil {
									continue
								}
								fo := instantiateFieldProvOrigin(fp, st.ArgTemps, env)
								dst := env.aggFields[st.ValTemp]
								if dst == nil {
									dst = make(map[string]OriginSet, len(sum.FieldProvs))
									env.aggFields[st.ValTemp] = dst
								}
								cf := dst[fPath]
								cf.merge(fo)
								dst[fPath] = cf
							}
						}
					}
				}
				if !usedSummary && st.Callee == nil && flow.FFIContracts != nil {
					if fc, hasFFI := flow.FFIContracts[st.CalleeName]; hasFFI {
						usedSummary = true
						if fc.Return.BorrowsParam >= 0 && fc.Return.BorrowsParam < len(st.ArgTemps) && st.ArgTemps[fc.Return.BorrowsParam] != cfg.InvalidValue {
							if argOrig, ok := env.temps[st.ArgTemps[fc.Return.BorrowsParam]]; ok && !argOrig.isEmpty() {
								retOrig.merge(argOrig)
							} else {
								retOrig.Unknown = true
							}
						} else {
							retOrig.Unknown = true
						}
					}
				}
				if !usedSummary {
					// 无过程间摘要（Stage A 独立模式或单引用内建/不透明调用）：保留关联引用实参来源
					hasArgOrigin := false
					for _, argT := range st.ArgTemps {
						if argT == cfg.InvalidValue {
							continue
						}
						if argOrig, ok := env.temps[argT]; ok && !argOrig.isEmpty() {
							retOrig.merge(argOrig)
							hasArgOrigin = true
						}
					}
					if !hasArgOrigin && !isPureEnumVariantCall(st) {
						retOrig.Unknown = true
					}
				}
				cur := env.temps[st.ValTemp]
				cur.merge(retOrig)
				env.temps[st.ValTemp] = cur
				if collect {
					fc := flow.TempOrigins[st.ValTemp]
					fc.merge(cur)
					flow.TempOrigins[st.ValTemp] = fc
					for id := range cur.Loans {
						if l := flow.LoanByID[id]; l != nil {
							l.CarrierTemps[st.ValTemp] = true
						}
					}
				}
			}

		case cfg.OpReturn:
			if st.SrcTemp != cfg.InvalidValue {
				retOrig := env.temps[st.SrcTemp]
				if collect {
					fc := flow.TempOrigins[st.SrcTemp]
					fc.merge(retOrig)
					flow.TempOrigins[st.SrcTemp] = fc
				}
			}

		case cfg.OpScopeExit, cfg.OpStorageDead:
			for _, sid := range st.Locals {
				clearSymOrigins(env, sid)
			}
		}
	}
}

func lookupPlaceOrigins(env *carrierEnv, place cfg.Place) OriginSet {
	if env == nil || place.RootSymID == 0 {
		return OriginSet{}
	}
	exactKey := place.Key()
	if orig, ok := env.places[exactKey]; ok && !orig.isEmpty() {
		return orig.clone()
	}
	// 若查询父位置（如结构体整体）或未单独拆分元素（如动态下标 arr[i]），保守聚合所有子投影与根载体
	res := newOriginSet()
	prefix := fmt.Sprintf("%s#%d", place.RootName, place.RootSymID)
	for k, orig := range env.places {
		if k == exactKey || (len(k) > len(exactKey) && k[:len(exactKey)] == exactKey) || (place.HasIndex() && len(k) >= len(prefix) && k[:len(prefix)] == prefix) {
			res.merge(orig)
		}
	}
	return res
}

func clearPlaceOrigins(env *carrierEnv, place cfg.Place) {
	if env == nil || place.RootSymID == 0 {
		return
	}
	exactKey := place.Key()
	delete(env.places, exactKey)
	for k := range env.places {
		if len(k) > len(exactKey) && k[:len(exactKey)] == exactKey && (k[len(exactKey)] == '.' || k[len(exactKey)] == '[') {
			delete(env.places, k)
		}
	}
}

func clearSymOrigins(env *carrierEnv, sid symbols.SymbolID) {
	if env == nil || sid == 0 {
		return
	}
	tag := fmt.Sprintf("#%d", sid)
	for k := range env.places {
		if len(k) >= len(tag) {
			for i := 0; i <= len(k)-len(tag); i++ {
				if k[i:i+len(tag)] == tag && (i+len(tag) == len(k) || k[i+len(tag)] == '.' || k[i+len(tag)] == '[') {
					delete(env.places, k)
					break
				}
			}
		}
	}
}

func typeHasRef(tctx *types.Context, visited map[types.TypeID]bool, tid types.TypeID, symTab *symbols.Table) bool {
	if tid == types.Invalid || tctx == nil {
		return false
	}
	if visited == nil {
		visited = make(map[types.TypeID]bool, 8)
	}
	if visited[tid] {
		return false
	}
	visited[tid] = true
	info := tctx.Info(tid)
	switch info.Kind {
	case types.KindRef:
		return true
	case types.KindArray, types.KindSlice:
		return typeHasRef(tctx, visited, info.Elem, symTab)
	case types.KindTuple:
		for _, el := range info.Elems {
			if typeHasRef(tctx, visited, el, symTab) {
				return true
			}
		}
		return false
	case types.KindStruct:
		if symTab != nil {
			if sym := symTab.ByID[symbols.SymbolID(info.SymbolID)]; sym != nil {
				for _, f := range sym.Fields {
					if typeHasRef(tctx, visited, f.Type, symTab) {
						return true
					}
				}
			}
		}
		return false
	case types.KindEnum:
		for _, ga := range info.GenericArgs {
			if typeHasRef(tctx, visited, ga, symTab) {
				return true
			}
		}
		if symTab != nil {
			if sym := symTab.ByID[symbols.SymbolID(info.SymbolID)]; sym != nil {
				for _, v := range sym.Variants {
					for _, tt := range v.TupleTypes {
						if typeHasRef(tctx, visited, tt, symTab) {
							return true
						}
					}
					for _, sf := range v.StructFields {
						if typeHasRef(tctx, visited, sf.Type, symTab) {
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

type refLeafSpec struct {
	Path  string
	IsMut bool
}

func joinAggFieldKey(prefix, child string) string {
	if prefix == "" {
		return child
	}
	if strings.HasPrefix(child, "[") {
		return prefix + child
	}
	return prefix + "." + child
}

func placeForFieldPath(base cfg.Place, path string, span diagnostic.Span) cfg.Place {
	cur := base
	rest := path
	for rest != "" {
		if rest[0] == '.' {
			rest = rest[1:]
			continue
		}
		if rest[0] == '[' {
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				break
			}
			idxStr := rest[1:end]
			if idxVal, err := strconv.ParseInt(idxStr, 10, 64); err == nil {
				cur = cur.AppendProj(cfg.Projection{Kind: cfg.ProjConstIdx, ConstIndex: idxVal}, types.Invalid, span)
			}
			rest = rest[end+1:]
			continue
		}
		nextDot := strings.IndexByte(rest, '.')
		nextBrk := strings.IndexByte(rest, '[')
		sep := -1
		if nextDot >= 0 && (nextBrk < 0 || nextDot < nextBrk) {
			sep = nextDot
		} else if nextBrk >= 0 {
			sep = nextBrk
		}
		var seg string
		if sep < 0 {
			seg = rest
			rest = ""
		} else {
			seg = rest[:sep]
			rest = rest[sep:]
		}
		if seg != "" {
			cur = cur.AppendField(seg, -1, types.Invalid, span)
		}
	}
	return cur
}

// collectTypeRefLeaves 枚举返回类型中全部引用叶子投影路径；若存在不可精确投影拆分的含引用聚合（Enum/Array/Slice），返回 unsupported=true。
func collectTypeRefLeaves(tctx *types.Context, symTab *symbols.Table, tid types.TypeID, prefix string) ([]refLeafSpec, bool) {
	return collectTypeRefLeavesRec(tctx, symTab, tid, prefix, make(map[types.TypeID]bool, 8))
}

func collectTypeRefLeavesRec(tctx *types.Context, symTab *symbols.Table, tid types.TypeID, prefix string, activePath map[types.TypeID]bool) ([]refLeafSpec, bool) {
	if tid == types.Invalid || tctx == nil {
		return nil, false
	}
	info := tctx.Info(tid)
	switch info.Kind {
	case types.KindRef:
		return []refLeafSpec{{Path: prefix, IsMut: info.IsMut}}, false

	case types.KindArray, types.KindSlice:
		if typeHasRef(tctx, nil, info.Elem, symTab) {
			return nil, true
		}
		return nil, false

	case types.KindEnum:
		if activePath[tid] {
			return nil, false
		}
		activePath[tid] = true
		defer delete(activePath, tid)
		if !typeHasRef(tctx, nil, tid, symTab) {
			return nil, false
		}
		// Result[T, &str] 等仅含不可变字符串视图 (&str) 的 Result/Option 保持纯共享 Static/Arg 语义，
		// 含 &mut 或任意一般引用载荷的枚举/容器按不支持精准投影的含引用聚合 Fail-Closed (WON-SEM-010) 拒止。
		if isEnumWithOnlyStrView(tctx, tid, symTab) {
			return []refLeafSpec{{Path: prefix, IsMut: false}}, false
		}
		return nil, true

	case types.KindTuple:
		var leaves []refLeafSpec
		for i, el := range info.Elems {
			sub, unsup := collectTypeRefLeavesRec(tctx, symTab, el, joinAggFieldKey(prefix, fmt.Sprintf("[%d]", i)), activePath)
			if unsup {
				return nil, true
			}
			leaves = append(leaves, sub...)
		}
		return leaves, false

	case types.KindStruct:
		if symTab == nil {
			return nil, false
		}
		if activePath[tid] {
			return nil, false
		}
		activePath[tid] = true
		defer delete(activePath, tid)
		sym := symTab.ByID[symbols.SymbolID(info.SymbolID)]
		if sym == nil {
			return nil, false
		}
		var subst map[uint64]types.TypeID
		if len(sym.GenericParamIDs) > 0 && len(sym.GenericParamIDs) == len(info.GenericArgs) {
			subst = make(map[uint64]types.TypeID, len(info.GenericArgs))
			for i, gid := range sym.GenericParamIDs {
				subst[uint64(gid)] = info.GenericArgs[i]
			}
		}
		var leaves []refLeafSpec
		for _, f := range sym.Fields {
			ft := f.Type
			if subst != nil {
				ft = tctx.Substitute(ft, subst)
			}
			sub, unsup := collectTypeRefLeavesRec(tctx, symTab, ft, joinAggFieldKey(prefix, f.Name), activePath)
			if unsup {
				return nil, true
			}
			leaves = append(leaves, sub...)
		}
		return leaves, false

	default:
		return nil, false
	}
}

// lookupReborrowParentOrigins 查找重借用父位置（支持多层解引用如 **pp -> *pp -> pp）的引用载体溯源。
func lookupReborrowParentOrigins(env *carrierEnv, flow *FuncLoanFlow, parentPlace cfg.Place) OriginSet {
	orig := lookupPlaceOrigins(env, parentPlace)
	if !orig.isEmpty() || !parentPlace.HasDeref() {
		return orig
	}
	cur := parentPlace
	for cur.HasDeref() {
		pref, ok := cur.PrefixBeforeLastDeref()
		if !ok {
			break
		}
		pOrig := lookupPlaceOrigins(env, pref)
		if !pOrig.isEmpty() {
			// 通过外层引用载体解引用后尝试解析其指向的内部载体位置（如 pp -> p）
			resolved := newOriginSet()
			for lid := range pOrig.Loans {
				if li := flow.LoanByID[lid]; li != nil {
					if inner := lookupPlaceOrigins(env, li.Place); !inner.isEmpty() {
						resolved.merge(inner)
					} else {
						resolved.Loans[lid] = true
					}
				}
			}
			for pIdx := range pOrig.Params {
				resolved.Params[pIdx] = true
			}
			if pOrig.Static {
				resolved.Static = true
			}
			if pOrig.Unknown {
				resolved.Unknown = true
			}
			if !resolved.isEmpty() {
				return resolved
			}
			return pOrig
		}
		cur = pref
	}
	return orig
}

func isPureEnumVariantCall(st cfg.Statement) bool {
	if st.Callee != nil && st.Callee.Kind == symbols.SymEnumVariant {
		return true
	}
	switch st.CalleeName {
	case "Result::Ok", "Result::Err", "Option::Some", "Option::None":
		return true
	}
	return false
}

func isEnumWithOnlyStrView(tctx *types.Context, enumTID types.TypeID, symTab *symbols.Table) bool {
	if tctx == nil || tctx.StrView == types.Invalid {
		return false
	}
	info := tctx.Info(enumTID)
	if info.Kind != types.KindEnum {
		return false
	}
	for _, ga := range info.GenericArgs {
		if ga == tctx.StrView {
			continue
		}
		if typeHasRef(tctx, nil, ga, symTab) {
			return false
		}
	}
	if symTab != nil {
		if sym := symTab.ByID[symbols.SymbolID(info.SymbolID)]; sym != nil {
			for _, v := range sym.Variants {
				for _, tt := range v.TupleTypes {
					if tt != tctx.StrView && typeHasRef(tctx, nil, tt, symTab) {
						return false
					}
				}
				for _, sf := range v.StructFields {
					if sf.Type != tctx.StrView && typeHasRef(tctx, nil, sf.Type, symTab) {
						return false
					}
				}
			}
		}
	}
	return true
}

// Package borrow 实现了 WON Phase 2B.1 所有权数据流分析器（确定性初始化、移动、重初始化与部分移动校验）。
// 本阶段基于 internal/cfg 与有限三比特格 (Uninit/Init/Moved 集合) 在控制流图上做单调不动点求解，
// 为 Phase 2B.2 借用/NLL 区域求解及 Phase 2B.3 析构插入保留清晰边界。
package borrow

import (
	"sort"

	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// PlaceState 采用有限子集格精确追踪绑定或其子字段在合并控制流后可能处于的所有状态，
// 从而在控制流合流点严格保留“可能未初始化”与“可能已移动”的区分。
type PlaceState struct {
	CanBeUninit bool
	CanBeInit   bool
	CanBeMoved  bool
	MoveSpans   []diagnostic.Span
	DeclSpan    diagnostic.Span
	Type        types.TypeID
	Fields      map[string]*PlaceState
}

type funcEnv map[symbols.SymbolID]*PlaceState

func newPlaceState(t types.TypeID, declSpan diagnostic.Span, initNow bool, symTab *symbols.Table, tctx *types.Context) *PlaceState {
	st := &PlaceState{
		CanBeUninit: !initNow,
		CanBeInit:   initNow,
		DeclSpan:    declSpan,
		Type:        t,
	}
	initStructFieldsIfAny(st, t, initNow, declSpan, symTab, tctx, 0)
	return st
}

func initStructFieldsIfAny(st *PlaceState, t types.TypeID, initNow bool, declSpan diagnostic.Span, symTab *symbols.Table, tctx *types.Context, depth int) {
	if st == nil || t == types.Invalid || tctx == nil || symTab == nil || depth > 8 {
		return
	}
	info := tctx.Info(t)
	if info.Kind != types.KindStruct || len(info.GenericArgs) > 0 {
		return
	}
	sym := symTab.ByID[symbols.SymbolID(info.SymbolID)]
	if sym == nil || len(sym.Fields) == 0 {
		return
	}
	st.Fields = make(map[string]*PlaceState, len(sym.Fields))
	for _, f := range sym.Fields {
		fst := &PlaceState{
			CanBeUninit: !initNow,
			CanBeInit:   initNow,
			DeclSpan:    declSpan,
			Type:        f.Type,
		}
		initStructFieldsIfAny(fst, f.Type, initNow, declSpan, symTab, tctx, depth+1)
		st.Fields[f.Name] = fst
	}
}

func (ps *PlaceState) clone() *PlaceState {
	if ps == nil {
		return nil
	}
	cp := &PlaceState{
		CanBeUninit: ps.CanBeUninit,
		CanBeInit:   ps.CanBeInit,
		CanBeMoved:  ps.CanBeMoved,
		DeclSpan:    ps.DeclSpan,
		Type:        ps.Type,
	}
	if len(ps.MoveSpans) > 0 {
		cp.MoveSpans = make([]diagnostic.Span, len(ps.MoveSpans))
		copy(cp.MoveSpans, ps.MoveSpans)
	}
	if len(ps.Fields) > 0 {
		cp.Fields = make(map[string]*PlaceState, len(ps.Fields))
		for k, v := range ps.Fields {
			cp.Fields[k] = v.clone()
		}
	}
	return cp
}

func cloneEnv(env funcEnv) funcEnv {
	cp := make(funcEnv, len(env))
	for k, v := range env {
		cp[k] = v.clone()
	}
	return cp
}

// markMoved 将当前节点及其全部子字段统一置为明确 Moved 状态并记录移出源码位置。
func (ps *PlaceState) markMoved(span diagnostic.Span) {
	if ps == nil {
		return
	}
	ps.CanBeUninit = false
	ps.CanBeInit = false
	ps.CanBeMoved = true
	ps.MoveSpans = appendUniqueSpan(ps.MoveSpans, span)
	for _, f := range ps.Fields {
		f.markMoved(span)
	}
}

// markInit 将当前节点及其全部子字段统一恢复为明确已初始化状态。
func (ps *PlaceState) markInit() {
	if ps == nil {
		return
	}
	ps.CanBeUninit = false
	ps.CanBeInit = true
	ps.CanBeMoved = false
	ps.MoveSpans = nil
	for _, f := range ps.Fields {
		f.markInit()
	}
}

// summaryState 递归汇总结构体叶子字段（或标量自身）的聚合状态，用于校验整块聚合读取与移动。
func (ps *PlaceState) summaryState() (canUninit, canInit, canMoved bool, moveSpans []diagnostic.Span) {
	if ps == nil {
		return false, false, false, nil
	}
	if len(ps.Fields) == 0 {
		return ps.CanBeUninit, ps.CanBeInit, ps.CanBeMoved, ps.MoveSpans
	}
	allInit := true
	anyInit := false
	keys := make([]string, 0, len(ps.Fields))
	for k := range ps.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		u, i, m, ms := ps.Fields[k].summaryState()
		if u {
			canUninit = true
			allInit = false
		}
		if m {
			canMoved = true
			allInit = false
			for _, sp := range ms {
				moveSpans = appendUniqueSpan(moveSpans, sp)
			}
		}
		if i {
			anyInit = true
		} else {
			allInit = false
		}
	}
	canInit = allInit || (anyInit && (canUninit || canMoved))
	return canUninit, canInit, canMoved, moveSpans
}

// joinPlaceState 按格上确界逐位合并两个状态，确保控制流合并点不丢失任何路径原因。
func joinPlaceState(a, b *PlaceState) *PlaceState {
	if a == nil {
		return b.clone()
	}
	if b == nil {
		return a.clone()
	}
	res := &PlaceState{
		CanBeUninit: a.CanBeUninit || b.CanBeUninit,
		CanBeInit:   a.CanBeInit || b.CanBeInit,
		CanBeMoved:  a.CanBeMoved || b.CanBeMoved,
		DeclSpan:    a.DeclSpan,
		Type:        a.Type,
	}
	if res.DeclSpan.Start.Line == 0 {
		res.DeclSpan = b.DeclSpan
	}
	if res.Type == types.Invalid {
		res.Type = b.Type
	}
	for _, sp := range a.MoveSpans {
		res.MoveSpans = appendUniqueSpan(res.MoveSpans, sp)
	}
	for _, sp := range b.MoveSpans {
		res.MoveSpans = appendUniqueSpan(res.MoveSpans, sp)
	}

	if len(a.Fields) > 0 || len(b.Fields) > 0 {
		res.Fields = make(map[string]*PlaceState, len(a.Fields)+len(b.Fields))
		for k, fa := range a.Fields {
			if fb, ok := b.Fields[k]; ok {
				res.Fields[k] = joinPlaceState(fa, fb)
			} else {
				res.Fields[k] = fa.clone()
			}
		}
		for k, fb := range b.Fields {
			if _, ok := res.Fields[k]; !ok {
				res.Fields[k] = fb.clone()
			}
		}
	}
	return res
}

func joinEnvs(envs []funcEnv) funcEnv {
	out := make(funcEnv)
	for _, e := range envs {
		for symID, ps := range e {
			out[symID] = joinPlaceState(out[symID], ps)
		}
	}
	return out
}

func equalPlaceState(a, b *PlaceState) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.CanBeUninit != b.CanBeUninit || a.CanBeInit != b.CanBeInit || a.CanBeMoved != b.CanBeMoved {
		return false
	}
	if len(a.MoveSpans) != len(b.MoveSpans) {
		return false
	}
	for i := range a.MoveSpans {
		if a.MoveSpans[i] != b.MoveSpans[i] {
			return false
		}
	}
	if len(a.Fields) != len(b.Fields) {
		return false
	}
	for k, fa := range a.Fields {
		fb, ok := b.Fields[k]
		if !ok || !equalPlaceState(fa, fb) {
			return false
		}
	}
	return true
}

func equalEnvs(a, b funcEnv) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || !equalPlaceState(va, vb) {
			return false
		}
	}
	return true
}

func appendUniqueSpan(spans []diagnostic.Span, sp diagnostic.Span) []diagnostic.Span {
	if sp.Start.Line == 0 && sp.Start.Byte == 0 {
		return spans
	}
	for _, existing := range spans {
		if existing == sp {
			return spans
		}
	}
	const maxTrackedMoveSpans = 4
	if len(spans) >= maxTrackedMoveSpans {
		return spans
	}
	out := make([]diagnostic.Span, len(spans)+1)
	copy(out, spans)
	out[len(spans)] = sp
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Start.Byte != out[j].Start.Byte {
			return out[i].Start.Byte < out[j].Start.Byte
		}
		return out[i].Start.Line < out[j].Start.Line
	})
	return out
}

// Package borrow 实现了 WON Phase 2B.1 所有权数据流分析器（确定性初始化、移动、重初始化与部分移动校验）。
// 本阶段基于 internal/cfg 与有限三比特格 (CanBeUninit / CanBeInit / CanBeMoved) 在控制流图上做单调不动点求解，
// 采用按路径按需惰性结构体字段展开，消除任意固定深度限制并保持严格快速失败 (fail-closed) 语义。
package borrow

import (
	"fmt"
	"sort"

	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// PlaceState 采用有限子集格精确追踪绑定或其子字段在合并控制流后可能处于的所有状态，
// 从而在控制流合流点严格保留“可能未初始化”、“可能已移动”与“非拥有裸指针副本”的区分。
type PlaceState struct {
	CanBeUninit        bool
	CanBeInit          bool
	CanBeOwned         bool
	CanBeMoved         bool
	CanBeNonOwningCopy bool
	CopyAliasRoots     map[symbols.SymbolID]bool
	MoveSpans          []diagnostic.Span
	DeclSpan           diagnostic.Span
	Type               types.TypeID
	Fields             map[string]*PlaceState
}

type funcEnv map[symbols.SymbolID]*PlaceState

func newPlaceState(t types.TypeID, declSpan diagnostic.Span, initNow bool) *PlaceState {
	return &PlaceState{
		CanBeUninit: !initNow,
		CanBeInit:   initNow,
		CanBeOwned:  initNow,
		DeclSpan:    declSpan,
		Type:        t,
	}
}

// uniformChild 根据父节点当前的统一状态构造尚未惰性展开的子字段状态快照。
func (ps *PlaceState) uniformChild(fieldType types.TypeID) *PlaceState {
	child := &PlaceState{
		CanBeUninit:        ps.CanBeUninit,
		CanBeInit:          ps.CanBeInit,
		CanBeOwned:         ps.CanBeOwned,
		CanBeMoved:         ps.CanBeMoved,
		CanBeNonOwningCopy: ps.CanBeNonOwningCopy,
		DeclSpan:           ps.DeclSpan,
		Type:               fieldType,
	}
	if len(ps.CopyAliasRoots) > 0 {
		child.CopyAliasRoots = cloneSymSet(ps.CopyAliasRoots)
	}
	if len(ps.MoveSpans) > 0 {
		child.MoveSpans = make([]diagnostic.Span, len(ps.MoveSpans))
		copy(child.MoveSpans, ps.MoveSpans)
	}
	return child
}

// expandDirectFields 仅对当前结构体节点的直接子字段执行一层惰性展开，
// 不设固定递归深度限制，也不因前向预展开循环结构体而触发栈溢出。
func (ps *PlaceState) expandDirectFields(symTab *symbols.Table, tctx *types.Context) error {
	if ps == nil {
		return fmt.Errorf("内部状态为空指针")
	}
	if ps.Fields != nil {
		return nil
	}
	if ps.Type == types.Invalid || tctx == nil || symTab == nil {
		return fmt.Errorf("结构体字段展开缺少有效类型上下文 (type=%d)", ps.Type)
	}
	info := tctx.Info(ps.Type)
	if info.Kind != types.KindStruct {
		return fmt.Errorf("对非结构体类型 '%s' 请求字段展开", tctx.Format(ps.Type))
	}
	sym := symTab.ByID[symbols.SymbolID(info.SymbolID)]
	if sym == nil {
		return fmt.Errorf("结构体类型 '%s' (symID=%d) 在符号表中缺失", tctx.Format(ps.Type), info.SymbolID)
	}

	// 支持泛型实例化结构体字段类型替换
	var subst map[uint64]types.TypeID
	if len(sym.GenericParamIDs) > 0 && len(sym.GenericParamIDs) == len(info.GenericArgs) {
		subst = make(map[uint64]types.TypeID, len(info.GenericArgs))
		for i, gid := range sym.GenericParamIDs {
			subst[uint64(gid)] = info.GenericArgs[i]
		}
	}

	ps.Fields = make(map[string]*PlaceState, len(sym.Fields))
	for _, f := range sym.Fields {
		ft := f.Type
		if subst != nil {
			ft = tctx.Substitute(ft, subst)
		}
		ps.Fields[f.Name] = ps.uniformChild(ft)
	}
	return nil
}

// Clone 深拷贝当前 PlaceState（包含字段子树与 MoveSpans），供外部阶段如 Drop 展开只读消费。
func (ps *PlaceState) Clone() *PlaceState {
	return ps.clone()
}

// SummaryState 汇总节点及其已展开子树的可达初始化/移动状态。
func (ps *PlaceState) SummaryState() (canUninit, canInit, canMoved bool, moveSpans []diagnostic.Span) {
	return ps.summaryState()
}

// SummaryOwnership 汇总节点及子树的可达拥有权、非拥有副本标记及别名源集合，供 Drop 与 FFI 转移消费。
func (ps *PlaceState) SummaryOwnership() (canUninit, canInit, canOwned, canMoved, canNonOwningCopy bool, aliasRoots []symbols.SymbolID, moveSpans []diagnostic.Span) {
	return ps.summaryOwnership()
}

// ExpandDirectFields 对结构体节点惰性展开一层直接字段状态，供外部状态求解器共享使用。
func (ps *PlaceState) ExpandDirectFields(symTab *symbols.Table, tctx *types.Context) error {
	return ps.expandDirectFields(symTab, tctx)
}

func (ps *PlaceState) clone() *PlaceState {
	if ps == nil {
		return nil
	}
	cp := &PlaceState{
		CanBeUninit:        ps.CanBeUninit,
		CanBeInit:          ps.CanBeInit,
		CanBeOwned:         ps.CanBeOwned,
		CanBeMoved:         ps.CanBeMoved,
		CanBeNonOwningCopy: ps.CanBeNonOwningCopy,
		DeclSpan:           ps.DeclSpan,
		Type:               ps.Type,
	}
	if len(ps.CopyAliasRoots) > 0 {
		cp.CopyAliasRoots = cloneSymSet(ps.CopyAliasRoots)
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

// markMoved 将当前节点及其已展开的全部子字段统一置为明确 Moved 状态，
// 后续若再访问更深层未展开字段，将从该节点的 Moved 状态继承。
func (ps *PlaceState) markMoved(span diagnostic.Span) {
	if ps == nil {
		return
	}
	ps.CanBeUninit = false
	ps.CanBeInit = false
	ps.CanBeOwned = false
	ps.CanBeNonOwningCopy = false
	ps.CanBeMoved = true
	ps.CopyAliasRoots = nil
	ps.MoveSpans = appendUniqueSpan(ps.MoveSpans, span)
	for _, f := range ps.Fields {
		f.markMoved(span)
	}
}

// markInit 将当前节点及其全部子字段恢复为明确已初始化且具备所有权的状态。
func (ps *PlaceState) markInit() {
	if ps == nil {
		return
	}
	ps.CanBeUninit = false
	ps.CanBeInit = true
	ps.CanBeOwned = true
	ps.CanBeNonOwningCopy = false
	ps.CanBeMoved = false
	ps.CopyAliasRoots = nil
	ps.MoveSpans = nil
	for _, f := range ps.Fields {
		f.markInit()
	}
}

// markNonOwningCopy 将当前节点标记为通过裸指针/句柄 Copy 得到的非拥有别名，不允许自动释放或 @owned 移交。
func (ps *PlaceState) markNonOwningCopy(roots map[symbols.SymbolID]bool) {
	if ps == nil {
		return
	}
	ps.CanBeUninit = false
	ps.CanBeInit = true
	ps.CanBeOwned = false
	ps.CanBeNonOwningCopy = true
	ps.CanBeMoved = false
	ps.MoveSpans = nil
	ps.CopyAliasRoots = cloneSymSet(roots)
	for _, f := range ps.Fields {
		f.markNonOwningCopy(roots)
	}
}

func (ps *PlaceState) summaryState() (canUninit, canInit, canMoved bool, moveSpans []diagnostic.Span) {
	canUninit, canInit, _, canMoved, _, _, moveSpans = ps.summaryOwnership()
	return canUninit, canInit, canMoved, moveSpans
}

// summaryOwnership 迭代/递归汇总节点的精确可达初始化、所有权、移动与裸指针别名状态。
func (ps *PlaceState) summaryOwnership() (canUninit, canInit, canOwned, canMoved, canNonOwningCopy bool, aliasRoots []symbols.SymbolID, moveSpans []diagnostic.Span) {
	if ps == nil {
		return false, false, false, false, false, nil, nil
	}
	if len(ps.Fields) == 0 {
		return ps.CanBeUninit, ps.CanBeInit, ps.CanBeOwned, ps.CanBeMoved, ps.CanBeNonOwningCopy, sortedSymIDs(ps.CopyAliasRoots), ps.MoveSpans
	}
	allInit := true
	anyInit := false
	rootSet := make(map[symbols.SymbolID]bool)
	keys := make([]string, 0, len(ps.Fields))
	for k := range ps.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		u, i, o, m, noc, ars, ms := ps.Fields[k].summaryOwnership()
		if u {
			canUninit = true
			allInit = false
		}
		if o {
			canOwned = true
		}
		if noc {
			canNonOwningCopy = true
		}
		for _, ar := range ars {
			rootSet[ar] = true
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
	return canUninit, canInit, canOwned, canMoved, canNonOwningCopy, sortedSymIDs(rootSet), moveSpans
}

// joinPlaceState 按格上确界逐位合并两个状态；若仅一侧惰性展开了 Fields，
// 将另一侧的统一节点语义下推并合并至每个已展开子字段，确保合流点严密无损、满足交换律与结合律。
func joinPlaceState(a, b *PlaceState) *PlaceState {
	if a == nil {
		return b.clone()
	}
	if b == nil {
		return a.clone()
	}
	res := &PlaceState{
		CanBeUninit:        a.CanBeUninit || b.CanBeUninit,
		CanBeInit:          a.CanBeInit || b.CanBeInit,
		CanBeOwned:         a.CanBeOwned || b.CanBeOwned,
		CanBeMoved:         a.CanBeMoved || b.CanBeMoved,
		CanBeNonOwningCopy: a.CanBeNonOwningCopy || b.CanBeNonOwningCopy,
		DeclSpan:           a.DeclSpan,
		Type:               a.Type,
	}
	if len(a.CopyAliasRoots) > 0 || len(b.CopyAliasRoots) > 0 {
		res.CopyAliasRoots = make(map[symbols.SymbolID]bool, len(a.CopyAliasRoots)+len(b.CopyAliasRoots))
		for k := range a.CopyAliasRoots {
			res.CopyAliasRoots[k] = true
		}
		for k := range b.CopyAliasRoots {
			res.CopyAliasRoots[k] = true
		}
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

	if a.Fields != nil || b.Fields != nil {
		capHint := len(a.Fields)
		if len(b.Fields) > capHint {
			capHint = len(b.Fields)
		}
		res.Fields = make(map[string]*PlaceState, capHint)
		if a.Fields != nil {
			for k, fa := range a.Fields {
				var fb *PlaceState
				if b.Fields != nil {
					fb = b.Fields[k]
				} else {
					fb = b.uniformChild(fa.Type)
				}
				res.Fields[k] = joinPlaceState(fa, fb)
			}
		}
		if b.Fields != nil {
			for k, fb := range b.Fields {
				if _, done := res.Fields[k]; done {
					continue
				}
				var fa *PlaceState
				if a.Fields != nil {
					fa = a.Fields[k]
				} else {
					fa = a.uniformChild(fb.Type)
				}
				res.Fields[k] = joinPlaceState(fa, fb)
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
	if a.CanBeUninit != b.CanBeUninit || a.CanBeInit != b.CanBeInit ||
		a.CanBeOwned != b.CanBeOwned || a.CanBeMoved != b.CanBeMoved ||
		a.CanBeNonOwningCopy != b.CanBeNonOwningCopy {
		return false
	}
	if !equalSymSets(a.CopyAliasRoots, b.CopyAliasRoots) {
		return false
	}
	if !equalSpanSlices(a.MoveSpans, b.MoveSpans) {
		return false
	}
	if a.Fields == nil && b.Fields == nil {
		return true
	}
	if a.Fields != nil && b.Fields != nil {
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
	// 当仅一侧展开时，校验所有已展开子字段是否与未展开父节点一致
	if a.Fields != nil {
		for _, fa := range a.Fields {
			if !equalPlaceState(fa, b.uniformChild(fa.Type)) {
				return false
			}
		}
		return true
	}
	for _, fb := range b.Fields {
		if !equalPlaceState(a.uniformChild(fb.Type), fb) {
			return false
		}
	}
	return true
}

func cloneSymSet(src map[symbols.SymbolID]bool) map[symbols.SymbolID]bool {
	if len(src) == 0 {
		return nil
	}
	cp := make(map[symbols.SymbolID]bool, len(src))
	for k := range src {
		cp[k] = true
	}
	return cp
}

func equalSymSets(a, b map[symbols.SymbolID]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func sortedSymIDs(m map[symbols.SymbolID]bool) []symbols.SymbolID {
	if len(m) == 0 {
		return nil
	}
	out := make([]symbols.SymbolID, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func equalSpanSlices(a, b []diagnostic.Span) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
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

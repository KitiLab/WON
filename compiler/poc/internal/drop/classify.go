// Package drop 实现 WON Phase 2B.3 的资源析构分类、结构化 Drop Glue 描述符、
// Drop Flag 状态数据流与跨控制流确定性析构插入 (Drop Elaboration)。
package drop

import (
	"fmt"
	"sort"

	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// DropGlueKind 严格区分编译器管理的结构化清理胶水种类，避免使用非类型化字符串猜测清理方式。
type DropGlueKind uint8

const (
	GlueNone               DropGlueKind = iota
	GlueString                          // 内置堆分配 String 资源释放
	GlueStruct                          // 结构体按字段声明顺序结构化逐字段析构
	GlueEnum                            // 枚举按活跃变体标签派发载荷析构
	GlueTuple                           // 元组按成员下标顺序析构
	GlueArray                           // 定长数组按元素下标顺序循环析构
	GlueForeignResource                 // 来自显式 FFI 所有权契约的外部资源释放胶水
	GlueRegisteredResource              // 预留：通过 Registry 显式注册的未来内建/标准库资源类型 (Vec/Shared/Arc 等)
	GlueCustomDropReserved              // 预留：未来 Trait Solver 阶段的 user `impl Drop`（当前语言层面仍由 WON-SEM-010 拦截）
)

// String 返回 DropGlueKind 的确定性可读名称。
func (k DropGlueKind) String() string {
	switch k {
	case GlueString:
		return "String"
	case GlueStruct:
		return "Struct"
	case GlueEnum:
		return "Enum"
	case GlueTuple:
		return "Tuple"
	case GlueArray:
		return "Array"
	case GlueForeignResource:
		return "ForeignResource"
	case GlueRegisteredResource:
		return "RegisteredResource"
	case GlueCustomDropReserved:
		return "CustomDrop(reserved)"
	default:
		return "None"
	}
}

// DropGlueField 描述结构体或变体中单个需要析构的字段（严格保留声明序）。
type DropGlueField struct {
	Name   string
	Index  int
	Type   types.TypeID
	GlueID string
}

// DropGlueVariant 描述枚举中单个携带需析构载荷的变体。
type DropGlueVariant struct {
	Name         string
	Index        int
	TupleElems   []DropGlueElem
	StructFields []DropGlueField
}

// DropGlueElem 描述元组中的单个需析构元素。
type DropGlueElem struct {
	Index  int
	Type   types.TypeID
	GlueID string
}

// DropGlue 表示单一具体类型对应的确定性资源析构方案。
type DropGlue struct {
	ID            string
	Kind          DropGlueKind
	Type          types.TypeID
	TypeName      string
	Fields        []DropGlueField   // GlueStruct
	Variants      []DropGlueVariant // GlueEnum
	TupleElems    []DropGlueElem    // GlueTuple
	ElemType      types.TypeID      // GlueArray
	ArrayLen      int64             // GlueArray
	ElemGlueID    string            // GlueArray
	ReleaseFn     string            // GlueForeignResource / GlueRegisteredResource
	CustomDropSym symbols.SymbolID  // GlueCustomDropReserved (WON-MEM-007 内部契约挂钩)
}

// ResourceExtension 描述通过显式扩展注册点登记的未来或外部托管资源类型。
// Phase 2B.3 仅建立扩展点，不将 Vec/Shared/Arc/Mutex/RwLock 注入当前 Prelude。
type ResourceExtension struct {
	Kind      DropGlueKind
	ReleaseFn string
	Custom    bool
	SymID     symbols.SymbolID
}

// ResourceRegistry 保存扩展资源与自定义 Drop 内部契约元数据，供 Drop 分类与 WON-MEM-007 守卫复用。
type ResourceRegistry struct {
	byTypeID   map[types.TypeID]ResourceExtension
	customSyms map[symbols.SymbolID]bool
	byTypeName map[string]ResourceExtension
}

// NewResourceRegistry 创建空的资源扩展注册表。
func NewResourceRegistry() *ResourceRegistry {
	return &ResourceRegistry{
		byTypeID:   make(map[types.TypeID]ResourceExtension),
		customSyms: make(map[symbols.SymbolID]bool),
		byTypeName: make(map[string]ResourceExtension),
	}
}

// RegisterType 注册特定 TypeID 对应的资源释放胶水（供未来 Vec/Shared/Arc 或显式 FFI 句柄接入）。
func (r *ResourceRegistry) RegisterType(tid types.TypeID, ext ResourceExtension) {
	if r == nil || tid == types.Invalid {
		return
	}
	r.byTypeID[tid] = ext
	if ext.Custom && ext.SymID != 0 {
		r.customSyms[ext.SymID] = true
	}
}

// MarkCustomDropHook 标记内部合成或未来 trait 阶段的自定义 Drop 结构体/枚举符号 ID，用于验证 WON-MEM-007 禁止部分移出。
func (r *ResourceRegistry) MarkCustomDropHook(symID symbols.SymbolID) {
	if r != nil && symID != 0 {
		r.customSyms[symID] = true
	}
}

// IsCustomDropSym 判断符号是否声明或挂钩了自定义根级 Drop 析构器。
func (r *ResourceRegistry) IsCustomDropSym(symID symbols.SymbolID) bool {
	if r == nil || symID == 0 {
		return false
	}
	return r.customSyms[symID]
}

// ClassifyContext 保存确定性的类型 -> DropGlue 缓存表，保证中心化 `NeedsDrop` 唯一真源。
type ClassifyContext struct {
	Types    *types.Context
	Symbols  *symbols.Table
	Registry *ResourceRegistry
	glues    map[string]*DropGlue
	byType   map[types.TypeID]string // 空串表示明确无需 Drop
	pending  map[types.TypeID]bool
}

// NewClassifyContext 创建与当前 TypeContext / SymbolTable 绑定的集中式资源分类上下文。
func NewClassifyContext(tctx *types.Context, symTab *symbols.Table, reg *ResourceRegistry) *ClassifyContext {
	return &ClassifyContext{
		Types:    tctx,
		Symbols:  symTab,
		Registry: reg,
		glues:    make(map[string]*DropGlue),
		byType:   make(map[types.TypeID]string),
		pending:  make(map[types.TypeID]bool),
	}
}

// NeedsDrop 是全编译器唯一规范的“类型是否需要运行期/确定性资源清理”判定函数。
// 严格区分 !IsCopy 与 NeedsDrop：例如 &mut T 为 !IsCopy 但完全不拥有资源 (NeedsDrop == false)。
func NeedsDrop(tctx *types.Context, symTab *symbols.Table, reg *ResourceRegistry, tid types.TypeID) bool {
	cc := NewClassifyContext(tctx, symTab, reg)
	return cc.NeedsDrop(tid)
}

// NeedsDrop 判断单个 TypeID 是否拥有需要编译器管理的资源析构行为。
func (c *ClassifyContext) NeedsDrop(tid types.TypeID) bool {
	_, ok := c.ResolveDropGlue(tid)
	return ok
}

// HasCustomDrop 判断给定类型是否带有自定义根级 Drop 契约（用于 WON-MEM-007 部分移出守卫）。
func (c *ClassifyContext) HasCustomDrop(tid types.TypeID) bool {
	if c == nil || c.Types == nil || c.Registry == nil || tid == types.Invalid {
		return false
	}
	if ext, ok := c.Registry.byTypeID[tid]; ok && ext.Custom {
		return true
	}
	info := c.Types.Info(tid)
	if info.SymbolID != 0 && c.Registry.IsCustomDropSym(symbols.SymbolID(info.SymbolID)) {
		return true
	}
	return false
}

// GlueTable 返回已解析胶水表的确定性副本。
func (c *ClassifyContext) GlueTable() map[string]*DropGlue {
	out := make(map[string]*DropGlue, len(c.glues))
	for k, v := range c.glues {
		out[k] = v
	}
	return out
}

// ResolveDropGlue 为给定类型解析或生成结构化 DropGlue 描述符；无需清理（如标量、&T、&mut T、*T、fn、无资源聚合）时返回 (nil, false)。
func (c *ClassifyContext) ResolveDropGlue(tid types.TypeID) (*DropGlue, bool) {
	if c == nil || c.Types == nil || tid == types.Invalid {
		return nil, false
	}
	if cachedID, seen := c.byType[tid]; seen {
		if cachedID == "" {
			return nil, false
		}
		return c.glues[cachedID], true
	}
	if c.pending[tid] {
		// 递归结构体直接按值嵌套属于 WON-TYP 阻断场景，通过 pending 守卫防止无限展开
		return nil, false
	}

	// 明确非拥有类型：标量、&T、&mut T、*const T、*mut T、fn(...) 与所有 Copy 类型均不持有析构资源
	info := c.Types.Info(tid)
	switch {
	case info.Kind == types.KindRef, info.Kind == types.KindRawPtr, info.Kind == types.KindFn,
		info.Kind == types.KindBool, info.Kind == types.KindChar, info.Kind == types.KindUnit, info.Kind == types.KindNever,
		info.Kind == types.KindStr, info.Kind == types.KindSlice,
		c.Types.IsInteger(tid), c.Types.IsFloat(tid):
		if c.Registry != nil {
			if ext, ok := c.Registry.byTypeID[tid]; ok {
				return c.registerExtGlue(tid, ext)
			}
		}
		c.byType[tid] = ""
		return nil, false
	}

	if tid == c.Types.StringT || info.Kind == types.KindString {
		gid := fmt.Sprintf("glue.String#%d", tid)
		g := &DropGlue{
			ID:       gid,
			Kind:     GlueString,
			Type:     tid,
			TypeName: "String",
		}
		c.glues[gid] = g
		c.byType[tid] = gid
		return g, true
	}

	if c.Registry != nil {
		if ext, ok := c.Registry.byTypeID[tid]; ok {
			return c.registerExtGlue(tid, ext)
		}
		if info.SymbolID != 0 && c.Registry.IsCustomDropSym(symbols.SymbolID(info.SymbolID)) {
			gid := fmt.Sprintf("glue.CustomDrop#%d", tid)
			g := &DropGlue{
				ID:            gid,
				Kind:          GlueCustomDropReserved,
				Type:          tid,
				TypeName:      c.Types.Format(tid),
				CustomDropSym: symbols.SymbolID(info.SymbolID),
			}
			c.glues[gid] = g
			c.byType[tid] = gid
			return g, true
		}
	}

	// @derive(Copy) 类型依据语言规范严格禁止携带任何析构需求
	if c.Types.IsCopy(tid) {
		c.byType[tid] = ""
		return nil, false
	}

	c.pending[tid] = true
	defer delete(c.pending, tid)

	switch info.Kind {
	case types.KindStruct:
		if c.Symbols == nil {
			c.byType[tid] = ""
			return nil, false
		}
		sym := c.Symbols.ByID[symbols.SymbolID(info.SymbolID)]
		if sym == nil {
			c.byType[tid] = ""
			return nil, false
		}
		var subst map[uint64]types.TypeID
		if len(sym.GenericParamIDs) > 0 && len(sym.GenericParamIDs) == len(info.GenericArgs) {
			subst = make(map[uint64]types.TypeID, len(info.GenericArgs))
			for i, gid := range sym.GenericParamIDs {
				subst[uint64(gid)] = info.GenericArgs[i]
			}
		}
		var dropFields []DropGlueField
		for fIdx, f := range sym.Fields {
			ft := f.Type
			if subst != nil {
				ft = c.Types.Substitute(ft, subst)
			}
			if fg, need := c.ResolveDropGlue(ft); need {
				dropFields = append(dropFields, DropGlueField{
					Name:   f.Name,
					Index:  fIdx,
					Type:   ft,
					GlueID: fg.ID,
				})
			}
		}
		if len(dropFields) == 0 {
			c.byType[tid] = ""
			return nil, false
		}
		gid := fmt.Sprintf("glue.Struct.%s#%d", sym.Name, tid)
		g := &DropGlue{
			ID:       gid,
			Kind:     GlueStruct,
			Type:     tid,
			TypeName: c.Types.Format(tid),
			Fields:   dropFields, // 严格保留字段声明序
		}
		c.glues[gid] = g
		c.byType[tid] = gid
		return g, true

	case types.KindEnum:
		if c.Symbols == nil {
			c.byType[tid] = ""
			return nil, false
		}
		sym := c.Symbols.ByID[symbols.SymbolID(info.SymbolID)]
		if sym == nil {
			c.byType[tid] = ""
			return nil, false
		}
		var subst map[uint64]types.TypeID
		if len(sym.GenericParamIDs) > 0 && len(sym.GenericParamIDs) == len(info.GenericArgs) {
			subst = make(map[uint64]types.TypeID, len(info.GenericArgs))
			for i, gid := range sym.GenericParamIDs {
				subst[uint64(gid)] = info.GenericArgs[i]
			}
		}
		var variants []DropGlueVariant
		for vIdx, v := range sym.Variants {
			var tupleElems []DropGlueElem
			for eIdx, pt := range v.TupleTypes {
				if subst != nil {
					pt = c.Types.Substitute(pt, subst)
				}
				if eg, need := c.ResolveDropGlue(pt); need {
					tupleElems = append(tupleElems, DropGlueElem{Index: eIdx, Type: pt, GlueID: eg.ID})
				}
			}
			var structFields []DropGlueField
			for fIdx, sf := range v.StructFields {
				ft := sf.Type
				if subst != nil {
					ft = c.Types.Substitute(ft, subst)
				}
				if fg, need := c.ResolveDropGlue(ft); need {
					structFields = append(structFields, DropGlueField{Name: sf.Name, Index: fIdx, Type: ft, GlueID: fg.ID})
				}
			}
			if len(tupleElems) > 0 || len(structFields) > 0 {
				variants = append(variants, DropGlueVariant{
					Name:         v.Name,
					Index:        vIdx,
					TupleElems:   tupleElems,
					StructFields: structFields,
				})
			}
		}
		if len(variants) == 0 {
			c.byType[tid] = ""
			return nil, false
		}
		gid := fmt.Sprintf("glue.Enum.%s#%d", sym.Name, tid)
		g := &DropGlue{
			ID:       gid,
			Kind:     GlueEnum,
			Type:     tid,
			TypeName: c.Types.Format(tid),
			Variants: variants,
		}
		c.glues[gid] = g
		c.byType[tid] = gid
		return g, true

	case types.KindTuple:
		var elems []DropGlueElem
		for i, el := range info.Elems {
			if eg, need := c.ResolveDropGlue(el); need {
				elems = append(elems, DropGlueElem{Index: i, Type: el, GlueID: eg.ID})
			}
		}
		if len(elems) == 0 {
			c.byType[tid] = ""
			return nil, false
		}
		gid := fmt.Sprintf("glue.Tuple#%d", tid)
		g := &DropGlue{
			ID:         gid,
			Kind:       GlueTuple,
			Type:       tid,
			TypeName:   c.Types.Format(tid),
			TupleElems: elems,
		}
		c.glues[gid] = g
		c.byType[tid] = gid
		return g, true

	case types.KindArray:
		eg, need := c.ResolveDropGlue(info.Elem)
		if !need {
			c.byType[tid] = ""
			return nil, false
		}
		gid := fmt.Sprintf("glue.Array[%d]#%d", info.Len, tid)
		g := &DropGlue{
			ID:         gid,
			Kind:       GlueArray,
			Type:       tid,
			TypeName:   c.Types.Format(tid),
			ElemType:   info.Elem,
			ArrayLen:   info.Len,
			ElemGlueID: eg.ID,
		}
		c.glues[gid] = g
		c.byType[tid] = gid
		return g, true
	}

	c.byType[tid] = ""
	return nil, false
}

func (c *ClassifyContext) registerExtGlue(tid types.TypeID, ext ResourceExtension) (*DropGlue, bool) {
	if ext.Kind == GlueNone {
		c.byType[tid] = ""
		return nil, false
	}
	gid := fmt.Sprintf("glue.%s.%s#%d", ext.Kind.String(), ext.ReleaseFn, tid)
	g := &DropGlue{
		ID:            gid,
		Kind:          ext.Kind,
		Type:          tid,
		TypeName:      c.Types.Format(tid),
		ReleaseFn:     ext.ReleaseFn,
		CustomDropSym: ext.SymID,
	}
	c.glues[gid] = g
	c.byType[tid] = gid
	return g, true
}

// SortedGlueIDs 按确定性字典序返回 GlueTable 中的全部胶水标识。
func SortedGlueIDs(m map[string]*DropGlue) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

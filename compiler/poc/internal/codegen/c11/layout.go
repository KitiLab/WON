package c11

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/tir"
)

// FieldLayout 描述结构体、元组或枚举变体中单个逻辑成员在目标 ABI 下的物理排布结果。
type FieldLayout struct {
	LogicalIndex  int
	PhysicalIndex int
	Name          string
	CName         string
	Type          tir.TypeID
	Offset        uint64
	Size          uint64
	Align         uint64
	IsZST         bool
	Omitted       bool
}

// VariantLayout 描述枚举单一变体的判别式与载荷子结构布局。
type VariantLayout struct {
	Index           int
	Name            string
	Discriminant    uint64
	HasDiscriminant bool
	HasPayload      bool
	IsStructVariant bool
	Size            uint64
	Align           uint64
	CFieldName      string
	Fields          []FieldLayout
	PhysicalFields  []int
}

// TypeLayout 保存单个具体 TIR 类型在 Linux x86_64 LP64 下的确定性 C11 物理布局与映射索引。
type TypeLayout struct {
	Type             tir.TypeID
	Kind             tir.TypeKind
	CName            string
	NeedsTypedef     bool
	IsZST            bool
	LogicalZeroSized bool
	IsUninhabited    bool
	IsUnitOnlyEnum   bool
	IsOpaque         bool
	IsStrView        bool
	IsStringType     bool
	Size             uint64
	Align            uint64
	TagCType         string
	TagSize          uint64
	TagAlign         uint64
	MaxDiscriminant  uint64
	PayloadOffset    uint64
	PayloadSize      uint64
	PayloadAlign     uint64
	Fields           []FieldLayout
	PhysicalFields   []int
	ElemType         tir.TypeID
	ArrayLen         int64
	Variants         []VariantLayout
}

// TargetLayout 缓存并按拓扑依赖顺序管理编译单元的确定性 C11 聚合物理布局表 (ADR-012 D5/D6)。
type TargetLayout struct {
	Target       Target
	Types        *tir.TypeTable
	Layouts      map[tir.TypeID]*TypeLayout
	OrderedTypes []tir.TypeID
	orderedSet   map[tir.TypeID]bool
}

// NewTargetLayout 基于已认证目标配置与类型表创建空布局引擎并验证目标平台限制。
func NewTargetLayout(target Target, tt *tir.TypeTable) (*TargetLayout, error) {
	if err := target.Validate(); err != nil {
		return nil, err
	}
	if tt == nil || tt.Len() < 1 {
		return nil, &CapabilityError{
			Code:    "WON-INT-001",
			Message: "TargetLayout 缺少有效的 TIR TypeTable",
		}
	}
	return &TargetLayout{
		Target:     target,
		Types:      tt,
		Layouts:    make(map[tir.TypeID]*TypeLayout, tt.Len()),
		orderedSet: make(map[tir.TypeID]bool, tt.Len()),
	}, nil
}

// BuildTargetLayout 为类型表中所有非泛型具体聚合类型预先计算并校验完整物理布局。
func BuildTargetLayout(target Target, tt *tir.TypeTable) (*TargetLayout, error) {
	tl, err := NewTargetLayout(target, tt)
	if err != nil {
		return nil, err
	}
	for id := tir.TypeID(1); int(id) < tt.Len(); id++ {
		if !isPrecomputableLayoutCandidate(tt, id) {
			continue
		}
		if _, err := tl.LayoutOf(id); err != nil {
			return nil, err
		}
	}
	return tl, nil
}

func isPrecomputableLayoutCandidate(tt *tir.TypeTable, id tir.TypeID) bool {
	info := tt.Info(id)
	if hasTypeParamVisited(tt, id, make(map[tir.TypeID]bool, 8)) {
		return false
	}
	switch info.Kind {
	case tir.TypeStruct, tir.TypeTuple, tir.TypeArray, tir.TypeEnum, tir.TypeString:
		return true
	case tir.TypeRef:
		return !info.IsMut && tt.Info(info.Elem).Kind == tir.TypeStr
	default:
		return false
	}
}

func hasTypeParamVisited(tt *tir.TypeTable, id tir.TypeID, seen map[tir.TypeID]bool) bool {
	if !tt.Valid(id) || seen[id] {
		return false
	}
	seen[id] = true
	defer delete(seen, id)
	info := tt.Info(id)
	switch info.Kind {
	case tir.TypeTypeParam:
		return true
	case tir.TypeRef, tir.TypeRawPtr, tir.TypeArray, tir.TypeSlice:
		return hasTypeParamVisited(tt, info.Elem, seen)
	case tir.TypeTuple:
		for _, el := range info.Elems {
			if hasTypeParamVisited(tt, el, seen) {
				return true
			}
		}
	case tir.TypeStruct:
		for _, ga := range info.GenericArgs {
			if hasTypeParamVisited(tt, ga, seen) {
				return true
			}
		}
		for _, f := range info.Fields {
			if hasTypeParamVisited(tt, f.Type, seen) {
				return true
			}
		}
	case tir.TypeEnum:
		for _, ga := range info.GenericArgs {
			if hasTypeParamVisited(tt, ga, seen) {
				return true
			}
		}
		for _, v := range info.Variants {
			for _, te := range v.TupleElems {
				if hasTypeParamVisited(tt, te, seen) {
					return true
				}
			}
			for _, sf := range v.StructFields {
				if hasTypeParamVisited(tt, sf.Type, seen) {
					return true
				}
			}
		}
	case tir.TypeFn:
		for _, p := range info.Params {
			if hasTypeParamVisited(tt, p, seen) {
				return true
			}
		}
		return hasTypeParamVisited(tt, info.Return, seen)
	}
	return false
}

// LayoutOf 计算并缓存指定 TIR 类型在目标 ABI 下的精确物理布局；若存在递归值循环、溢出或不支持类型则 Fail-Closed 返回错误。
func (tl *TargetLayout) LayoutOf(id tir.TypeID) (*TypeLayout, error) {
	if tl == nil || tl.Types == nil || !tl.Types.Valid(id) {
		return nil, newCapErr("WON-GEN-002", fmt.Sprintf("布局引擎收到无效 TypeID %d", id), tir.SourceSpan{}, "")
	}
	if cached, ok := tl.Layouts[id]; ok {
		return cached, nil
	}
	visiting := make(map[tir.TypeID]bool, 16)
	return tl.computeLayout(id, visiting)
}

func (tl *TargetLayout) computeLayout(id tir.TypeID, visiting map[tir.TypeID]bool) (*TypeLayout, error) {
	if cached, ok := tl.Layouts[id]; ok {
		return cached, nil
	}
	if visiting[id] {
		return nil, newCapErr("WON-GEN-002", fmt.Sprintf("类型 '%s' 存在直接按值递归定义，无法计算有限对象大小", tl.Types.Format(id)), tir.SourceSpan{}, "")
	}
	info := tl.Types.Info(id)
	if hasTypeParamVisited(tl.Types, id, make(map[tir.TypeID]bool, 8)) {
		return nil, newCapErr("WON-GEN-002", fmt.Sprintf("未单态化泛型类型 '%s' 无法确定原生布局", tl.Types.Format(id)), tir.SourceSpan{}, "")
	}

	visiting[id] = true
	defer delete(visiting, id)

	ptrSize := tl.Target.PtrSizeBytes()
	var lay *TypeLayout

	switch info.Kind {
	case tir.TypeUnit:
		lay = &TypeLayout{
			Type:             id,
			Kind:             info.Kind,
			CName:            "_won_zst_t",
			NeedsTypedef:     true,
			IsZST:            true,
			LogicalZeroSized: true,
			Size:             1,
			Align:            1,
		}
	case tir.TypeBool, tir.TypeChar, tir.TypeInt, tir.TypeFloat:
		cName, sz, al, err := primitiveSizeAlign(info, ptrSize)
		if err != nil {
			return nil, err
		}
		lay = &TypeLayout{
			Type:  id,
			Kind:  info.Kind,
			CName: cName,
			Size:  sz,
			Align: al,
		}
	case tir.TypeRawPtr:
		cName, err := MapType(tl.Types, id, PosValue)
		if err != nil {
			return nil, err
		}
		lay = &TypeLayout{
			Type:  id,
			Kind:  info.Kind,
			CName: cName,
			Size:  ptrSize,
			Align: ptrSize,
		}
	case tir.TypeRef:
		elem := tl.Types.Info(info.Elem)
		if elem.Kind == tir.TypeStr {
			if info.IsMut {
				return nil, newCapErr("WON-GEN-002", "可变字符串视图 '&mut str' 尚未在本阶段开放原生布局/执行", tir.SourceSpan{}, "")
			}
			lay = &TypeLayout{
				Type:         id,
				Kind:         info.Kind,
				CName:        "_won_str_t",
				NeedsTypedef: true,
				IsStrView:    true,
				Size:         ptrSize * 2,
				Align:        ptrSize,
			}
		} else if elem.Kind == tir.TypeSlice {
			cName, err := MapType(tl.Types, id, PosValue)
			if err != nil {
				return nil, err
			}
			lay = &TypeLayout{
				Type:  id,
				Kind:  info.Kind,
				CName: cName,
				Size:  ptrSize,
				Align: ptrSize,
			}
		} else {
			// &T / &mut T 在目标 C11 中表示单一对象指针；对有尺寸元素类型提前解析点目标布局以保证 typedef 依赖完整。
			elemLay, err := tl.computeLayout(info.Elem, visiting)
			if err != nil {
				return nil, err
			}
			cName := fmt.Sprintf("%s*", elemLay.CName)
			if !info.IsMut {
				cName = fmt.Sprintf("const %s*", elemLay.CName)
			}
			if elem.Kind == tir.TypeUnit {
				cName = "void*"
				if !info.IsMut {
					cName = "const void*"
				}
			}
			lay = &TypeLayout{
				Type:  id,
				Kind:  info.Kind,
				CName: cName,
				Size:  ptrSize,
				Align: ptrSize,
			}
		}
	case tir.TypeString:
		lay = &TypeLayout{
			Type:         id,
			Kind:         info.Kind,
			CName:        "_won_string_t",
			NeedsTypedef: true,
			IsStringType: true,
			Size:         ptrSize * 3,
			Align:        ptrSize,
		}
	case tir.TypeStruct:
		if info.IsOpaqueForeign {
			if info.Name == "" {
				return nil, newCapErr("WON-GEN-002", "不透明外部结构体缺少有效 C 标识符", tir.SourceSpan{}, "")
			}
			lay = &TypeLayout{
				Type:     id,
				Kind:     info.Kind,
				CName:    info.Name,
				IsOpaque: true,
			}
		} else {
			spec := make([]structSpecField, len(info.Fields))
			for i, f := range info.Fields {
				spec[i] = structSpecField{LogicalIndex: i, Name: f.Name, Type: f.Type}
			}
			fields, physOrder, totSize, maxAlign, allZST, err := tl.layoutFieldsL2(spec, false, visiting)
			if err != nil {
				return nil, err
			}
			if allZST {
				lay = &TypeLayout{
					Type:             id,
					Kind:             info.Kind,
					CName:            "_won_zst_t",
					NeedsTypedef:     true,
					IsZST:            true,
					LogicalZeroSized: true,
					Size:             1,
					Align:            1,
					Fields:           fields,
				}
			} else {
				lay = &TypeLayout{
					Type:           id,
					Kind:           info.Kind,
					CName:          fmt.Sprintf("_won_s_%d_t", id),
					NeedsTypedef:   true,
					Size:           totSize,
					Align:          maxAlign,
					Fields:         fields,
					PhysicalFields: physOrder,
				}
			}
		}
	case tir.TypeTuple:
		spec := make([]structSpecField, len(info.Elems))
		for i, el := range info.Elems {
			spec[i] = structSpecField{LogicalIndex: i, Name: fmt.Sprintf("_f%d", i), Type: el}
		}
		fields, physOrder, totSize, maxAlign, allZST, err := tl.layoutFieldsL2(spec, true, visiting)
		if err != nil {
			return nil, err
		}
		if allZST {
			lay = &TypeLayout{
				Type:             id,
				Kind:             info.Kind,
				CName:            "_won_zst_t",
				NeedsTypedef:     true,
				IsZST:            true,
				LogicalZeroSized: true,
				Size:             1,
				Align:            1,
				Fields:           fields,
			}
		} else {
			lay = &TypeLayout{
				Type:           id,
				Kind:           info.Kind,
				CName:          fmt.Sprintf("_won_tup_%d_t", id),
				NeedsTypedef:   true,
				Size:           totSize,
				Align:          maxAlign,
				Fields:         fields,
				PhysicalFields: physOrder,
			}
		}
	case tir.TypeArray:
		if info.ArrayLen < 0 {
			return nil, newCapErr("WON-GEN-002", fmt.Sprintf("定长数组 '%s' 长度为负数 %d", tl.Types.Format(id), info.ArrayLen), tir.SourceSpan{}, "")
		}
		elemLay, err := tl.computeLayout(info.Elem, visiting)
		if err != nil {
			return nil, err
		}
		if info.ArrayLen == 0 || elemLay.IsZST || elemLay.LogicalZeroSized {
			lay = &TypeLayout{
				Type:             id,
				Kind:             info.Kind,
				CName:            fmt.Sprintf("_won_arr_%d_t", id),
				NeedsTypedef:     true,
				IsZST:            true,
				LogicalZeroSized: true,
				Size:             1,
				Align:            1,
				ElemType:         info.Elem,
				ArrayLen:         info.ArrayLen,
			}
		} else {
			totSize, ok := tl.checkedMul(uint64(info.ArrayLen), elemLay.Size)
			if !ok {
				return nil, newCapErr("WON-GEN-002", fmt.Sprintf("定长数组 '%s' 字节尺寸溢出 PTRDIFF_MAX", tl.Types.Format(id)), tir.SourceSpan{}, "")
			}
			lay = &TypeLayout{
				Type:         id,
				Kind:         info.Kind,
				CName:        fmt.Sprintf("_won_arr_%d_t", id),
				NeedsTypedef: true,
				Size:         totSize,
				Align:        elemLay.Align,
				ElemType:     info.Elem,
				ArrayLen:     info.ArrayLen,
			}
		}
	case tir.TypeEnum:
		var err error
		lay, err = tl.layoutEnum(id, info, visiting)
		if err != nil {
			return nil, err
		}
	case tir.TypeFn:
		lay = &TypeLayout{
			Type:  id,
			Kind:  info.Kind,
			Size:  ptrSize,
			Align: ptrSize,
		}
	default:
		return nil, newCapErr("WON-GEN-002", fmt.Sprintf("类型 '%s' 不支持确定性物理布局", tl.Types.Format(id)), tir.SourceSpan{}, "")
	}

	tl.Layouts[id] = lay
	if lay.NeedsTypedef && !tl.orderedSet[id] {
		tl.orderedSet[id] = true
		tl.OrderedTypes = append(tl.OrderedTypes, id)
	}
	return lay, nil
}

type structSpecField struct {
	LogicalIndex int
	Name         string
	Type         tir.TypeID
}

// layoutFieldsL2 按照 ADR-012 D5 执行 L2 降序对齐布局：先消去 ZST 字段，再按 Align 降序、LogicalIndex 升序排列。
func (tl *TargetLayout) layoutFieldsL2(spec []structSpecField, tupleNamed bool, visiting map[tir.TypeID]bool) ([]FieldLayout, []int, uint64, uint64, bool, error) {
	fields := make([]FieldLayout, len(spec))
	activeIndices := make([]int, 0, len(spec))

	for i, s := range spec {
		subLay, err := tl.computeLayout(s.Type, visiting)
		if err != nil {
			return nil, nil, 0, 0, false, err
		}
		if subLay.IsOpaque {
			// 允许结构体/枚举包装已验证的不透明 C 外部句柄（例如 ResHandle）
			if _, ok := tl.Layouts[s.Type]; !ok {
				tl.Layouts[s.Type] = subLay
			}
		}
		cName := fmt.Sprintf("_f%d", s.LogicalIndex)
		if !tupleNamed {
			cName = SafeCFieldName(s.Name)
		}
		fl := FieldLayout{
			LogicalIndex:  s.LogicalIndex,
			PhysicalIndex: -1,
			Name:          s.Name,
			CName:         cName,
			Type:          s.Type,
			Size:          subLay.Size,
			Align:         subLay.Align,
			IsZST:         subLay.IsZST || subLay.LogicalZeroSized,
		}
		if subLay.IsOpaque {
			// 不透明外部 C struct 默认视为非 ZST（具体大小由 C 编译器 sizeof 保证；仅用于组合体/胶水分析时保守按 8B 指针估计）
			fl.IsZST = false
			if fl.Align == 0 {
				fl.Align = tl.Target.PtrSizeBytes()
			}
			if fl.Size == 0 {
				fl.Size = tl.Target.PtrSizeBytes()
			}
		}
		if fl.IsZST {
			fl.Omitted = true
			fl.Size = 0
			fl.Align = 1
		} else {
			activeIndices = append(activeIndices, i)
		}
		fields[i] = fl
	}

	if len(activeIndices) == 0 {
		return fields, nil, 1, 1, true, nil
	}

	sort.Slice(activeIndices, func(a, b int) bool {
		fa := fields[activeIndices[a]]
		fb := fields[activeIndices[b]]
		if fa.Align != fb.Align {
			return fa.Align > fb.Align
		}
		return fa.LogicalIndex < fb.LogicalIndex
	})

	cursor := uint64(0)
	maxAlign := uint64(1)
	for physIdx, logIdx := range activeIndices {
		fl := &fields[logIdx]
		aligned, ok := tl.checkedAlignUp(cursor, fl.Align)
		if !ok {
			return nil, nil, 0, 0, false, newCapErr("WON-GEN-002", "结构体/元组对齐偏移溢出 PTRDIFF_MAX", tir.SourceSpan{}, "")
		}
		fl.PhysicalIndex = physIdx
		fl.Offset = aligned
		nextCursor, ok := tl.checkedAdd(aligned, fl.Size)
		if !ok {
			return nil, nil, 0, 0, false, newCapErr("WON-GEN-002", "结构体/元组字段累积大小溢出 PTRDIFF_MAX", tir.SourceSpan{}, "")
		}
		cursor = nextCursor
		if fl.Align > maxAlign {
			maxAlign = fl.Align
		}
	}

	totalSize, ok := tl.checkedAlignUp(cursor, maxAlign)
	if !ok {
		return nil, nil, 0, 0, false, newCapErr("WON-GEN-002", "结构体/元组尾部填充尺寸溢出 PTRDIFF_MAX", tir.SourceSpan{}, "")
	}
	return fields, activeIndices, totalSize, maxAlign, false, nil
}

func (tl *TargetLayout) layoutEnum(id tir.TypeID, info tir.TypeInfo, visiting map[tir.TypeID]bool) (*TypeLayout, error) {
	variants := make([]VariantLayout, len(info.Variants))
	seenDisc := make(map[uint64]string, len(info.Variants))
	var maxDisc uint64
	anyPayload := false
	maxPayloadSize := uint64(0)
	maxPayloadAlign := uint64(1)

	for vi, v := range info.Variants {
		disc := uint64(vi)
		if v.Index >= 0 {
			disc = uint64(v.Index)
		}
		if v.HasDiscriminant {
			parsed, ok := parseUnsignedDiscriminant(v.DiscriminantText)
			if !ok {
				return nil, newCapErr("WON-GEN-002", fmt.Sprintf("枚举 '%s' 变体 '%s' 判别式 %q 无法表示为无符号 uint64", info.Name, v.Name, v.DiscriminantText), tir.SourceSpan{}, "")
			}
			disc = parsed
		}
		if prev, dup := seenDisc[disc]; dup {
			return nil, newCapErr("WON-GEN-002", fmt.Sprintf("枚举 '%s' 变体 '%s' 与 '%s' 存在重复判别式 %d", info.Name, v.Name, prev, disc), tir.SourceSpan{}, "")
		}
		seenDisc[disc] = v.Name
		if disc > maxDisc {
			maxDisc = disc
		}

		var spec []structSpecField
		isStruct := v.Kind == tir.VariantStruct
		if isStruct {
			spec = make([]structSpecField, len(v.StructFields))
			for fi, sf := range v.StructFields {
				spec[fi] = structSpecField{LogicalIndex: fi, Name: sf.Name, Type: sf.Type}
			}
		} else {
			spec = make([]structSpecField, len(v.TupleElems))
			for ti, te := range v.TupleElems {
				spec[ti] = structSpecField{LogicalIndex: ti, Name: fmt.Sprintf("_f%d", ti), Type: te}
			}
		}

		fields, physOrder, vSize, vAlign, allZST, err := tl.layoutFieldsL2(spec, !isStruct, visiting)
		if err != nil {
			return nil, err
		}
		vl := VariantLayout{
			Index:           vi,
			Name:            v.Name,
			Discriminant:    disc,
			HasDiscriminant: v.HasDiscriminant,
			HasPayload:      !allZST && len(physOrder) > 0,
			IsStructVariant: isStruct,
			CFieldName:      fmt.Sprintf("v%d", vi),
			Fields:          fields,
			PhysicalFields:  physOrder,
		}
		if vl.HasPayload {
			anyPayload = true
			vl.Size = vSize
			vl.Align = vAlign
			if vSize > maxPayloadSize {
				maxPayloadSize = vSize
			}
			if vAlign > maxPayloadAlign {
				maxPayloadAlign = vAlign
			}
		}
		variants[vi] = vl
	}

	tagCType, tagSize, tagAlign := chooseEnumTagType(maxDisc)
	cName := fmt.Sprintf("_won_enum_%d_t", id)

	if len(variants) == 0 {
		return &TypeLayout{
			Type:            id,
			Kind:            info.Kind,
			CName:           cName,
			NeedsTypedef:    true,
			IsUninhabited:   true,
			Size:            1,
			Align:           1,
			TagCType:        "uint8_t",
			TagSize:         1,
			TagAlign:        1,
			MaxDiscriminant: 0,
		}, nil
	}

	if !anyPayload {
		return &TypeLayout{
			Type:            id,
			Kind:            info.Kind,
			CName:           cName,
			NeedsTypedef:    true,
			IsUnitOnlyEnum:  true,
			Size:            tagSize,
			Align:           tagAlign,
			TagCType:        tagCType,
			TagSize:         tagSize,
			TagAlign:        tagAlign,
			MaxDiscriminant: maxDisc,
			Variants:        variants,
		}, nil
	}

	payloadOffset, ok := tl.checkedAlignUp(tagSize, maxPayloadAlign)
	if !ok {
		return nil, newCapErr("WON-GEN-002", fmt.Sprintf("枚举 '%s' 载荷偏移溢出 PTRDIFF_MAX", info.Name), tir.SourceSpan{}, "")
	}
	end, ok := tl.checkedAdd(payloadOffset, maxPayloadSize)
	if !ok {
		return nil, newCapErr("WON-GEN-002", fmt.Sprintf("枚举 '%s' 总字节尺寸溢出 PTRDIFF_MAX", info.Name), tir.SourceSpan{}, "")
	}
	enumAlign := tagAlign
	if maxPayloadAlign > enumAlign {
		enumAlign = maxPayloadAlign
	}
	totalSize, ok := tl.checkedAlignUp(end, enumAlign)
	if !ok {
		return nil, newCapErr("WON-GEN-002", fmt.Sprintf("枚举 '%s' 对齐总尺寸溢出 PTRDIFF_MAX", info.Name), tir.SourceSpan{}, "")
	}

	return &TypeLayout{
		Type:            id,
		Kind:            info.Kind,
		CName:           cName,
		NeedsTypedef:    true,
		Size:            totalSize,
		Align:           enumAlign,
		TagCType:        tagCType,
		TagSize:         tagSize,
		TagAlign:        tagAlign,
		MaxDiscriminant: maxDisc,
		PayloadOffset:   payloadOffset,
		PayloadSize:     maxPayloadSize,
		PayloadAlign:    maxPayloadAlign,
		Variants:        variants,
	}, nil
}

func chooseEnumTagType(maxDisc uint64) (cType string, size uint64, align uint64) {
	switch {
	case maxDisc <= 0xff:
		return "uint8_t", 1, 1
	case maxDisc <= 0xffff:
		return "uint16_t", 2, 2
	case maxDisc <= 0xffffffff:
		return "uint32_t", 4, 4
	default:
		return "uint64_t", 8, 8
	}
}

func parseUnsignedDiscriminant(raw string) (uint64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		return 0, false
	}
	val, err := strconv.ParseUint(s, 0, 64)
	if err != nil {
		return 0, false
	}
	return val, true
}

func primitiveSizeAlign(info tir.TypeInfo, ptrSize uint64) (string, uint64, uint64, error) {
	switch info.Kind {
	case tir.TypeBool:
		return "bool", 1, 1, nil
	case tir.TypeChar:
		return "uint32_t", 4, 4, nil
	case tir.TypeFloat:
		switch info.Bits {
		case 32:
			return "float", 4, 4, nil
		case 64:
			return "double", 8, 8, nil
		default:
			return "", 0, 0, newCapErr("WON-GEN-002", fmt.Sprintf("不支持的浮点位宽 f%d", info.Bits), tir.SourceSpan{}, "")
		}
	case tir.TypeInt:
		if info.IsPtrSized {
			if info.Signed {
				return "ptrdiff_t", ptrSize, ptrSize, nil
			}
			return "size_t", ptrSize, ptrSize, nil
		}
		switch info.Bits {
		case 8:
			if info.Signed {
				return "int8_t", 1, 1, nil
			}
			return "uint8_t", 1, 1, nil
		case 16:
			if info.Signed {
				return "int16_t", 2, 2, nil
			}
			return "uint16_t", 2, 2, nil
		case 32:
			if info.Signed {
				return "int32_t", 4, 4, nil
			}
			return "uint32_t", 4, 4, nil
		case 64:
			if info.Signed {
				return "int64_t", 8, 8, nil
			}
			return "uint64_t", 8, 8, nil
		default:
			prefix := "u"
			if info.Signed {
				prefix = "i"
			}
			return "", 0, 0, newCapErr("WON-GEN-002", fmt.Sprintf("ISO C11 后端不支持整数类型 %s%d", prefix, info.Bits), tir.SourceSpan{}, "")
		}
	}
	return "", 0, 0, newCapErr("WON-GEN-002", fmt.Sprintf("非标量基础类型 %v", info.Kind), tir.SourceSpan{}, "")
}

func (tl *TargetLayout) checkedAdd(a, b uint64) (uint64, bool) {
	maxObj := tl.Target.MaxObjectSizeBytes()
	if a > maxObj || b > maxObj-a {
		return 0, false
	}
	return a + b, true
}

func (tl *TargetLayout) checkedMul(a, b uint64) (uint64, bool) {
	maxObj := tl.Target.MaxObjectSizeBytes()
	if a == 0 || b == 0 {
		return 0, true
	}
	if a > maxObj/b {
		return 0, false
	}
	return a * b, true
}

func (tl *TargetLayout) checkedAlignUp(val, align uint64) (uint64, bool) {
	if align <= 1 {
		return val, val <= tl.Target.MaxObjectSizeBytes()
	}
	rem := val % align
	if rem == 0 {
		return val, val <= tl.Target.MaxObjectSizeBytes()
	}
	return tl.checkedAdd(val, align-rem)
}

var c11ReservedNames = map[string]bool{
	"auto": true, "break": true, "case": true, "char": true, "const": true,
	"continue": true, "default": true, "do": true, "double": true, "else": true,
	"enum": true, "extern": true, "float": true, "for": true, "goto": true,
	"if": true, "inline": true, "int": true, "long": true, "register": true,
	"restrict": true, "return": true, "short": true, "signed": true, "sizeof": true,
	"static": true, "struct": true, "switch": true, "typedef": true, "union": true,
	"unsigned": true, "void": true, "volatile": true, "while": true,
	"_Alignas": true, "_Alignof": true, "_Atomic": true, "_Bool": true,
	"_Complex": true, "_Generic": true, "_Imaginary": true, "_Noreturn": true,
	"_Static_assert": true, "_Thread_local": true,
	"bool": true, "true": true, "false": true, "NULL": true,
	"tag": true, "payload": true, "elems": true,
}

// SafeCFieldName 将 WON 字段名转换为在严格 ISO C11 中不与关键字或内部成员冲突的合法标识符。
func SafeCFieldName(name string) string {
	if name == "" {
		return "_wf_anon"
	}
	if c11ReservedNames[name] || strings.HasPrefix(name, "_") {
		return "_wf_" + name
	}
	return name
}

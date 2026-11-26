// Package cfg 实现了 WON Phase 2B.1 所有权控制流图 (Ownership CFG) 与位置投影抽象。
// 该包直接消费 TypedProgram 中已解析的符号与规范类型标识，为 Move/初始化数据流及后续 Phase 2B.2 借用分析、Phase 2C TIR 提供持久化的结构化基石。
package cfg

import (
	"fmt"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// ProjectionKind 定义存储位置 (Place) 的路径投影类别。
// 明确区分 Phase 2B.1 已启用投影与留待 Phase 2B.2+ 借用/枚举分析使用的预留投影。
type ProjectionKind uint8

const (
	ProjField      ProjectionKind = iota // 结构体字段访问: .field（Phase 2B.1 已启用）
	ProjTupleField                       // 元组下标字段: .0（结构体化预留）
	ProjConstIdx                         // 常量整数数组下标: [k]（保守聚合追踪）
	ProjIndex                            // 动态下标索引: [idx]（保守别名投影）
	ProjDeref                            // 指针/引用解引用: *p（Phase 2B.1 用于拦截 *r 移出与读取根引用）
	ProjDowncast                         // 枚举变体向下转型载荷（预留给后续变体细粒度分析）
)

// Projection 描述基于根绑定的一级存储路径投影。
type Projection struct {
	Kind       ProjectionKind
	FieldName  string
	FieldIndex int
	VariantIdx int
	ConstIndex int64
	IndexSym   symbols.SymbolID
	ElemType   types.TypeID
}

// Place 表示由已解析符号 ID 与有序投影序列构成的精确内存位置标识。
// 严格绑定至 symbols.SymbolID，不依赖词法名称字符串比较。
type Place struct {
	RootSymID   symbols.SymbolID
	RootName    string
	RootType    types.TypeID
	ElemType    types.TypeID
	RootMut     bool
	IsParam     bool
	Projections []Projection
	Span        diagnostic.Span
}

// HasDeref 判断位置路径中是否包含解引用投影 (*p / r.field 等)。
func (p Place) HasDeref() bool {
	for _, proj := range p.Projections {
		if proj.Kind == ProjDeref {
			return true
		}
	}
	return false
}

// HasDynamicIndex 判断位置路径中是否包含数组/切片下标索引投影。
func (p Place) HasIndex() bool {
	for _, proj := range p.Projections {
		if proj.Kind == ProjIndex || proj.Kind == ProjConstIdx {
			return true
		}
	}
	return false
}

// FieldPath 返回连续结构体字段投影的名称切片（不含解引用或下标投影）。
// 若路径包含非结构体字段投影，则 ok 返回 false。
func (p Place) FieldPath() ([]string, bool) {
	if len(p.Projections) == 0 {
		return nil, true
	}
	fields := make([]string, 0, len(p.Projections))
	for _, proj := range p.Projections {
		if proj.Kind != ProjField {
			return nil, false
		}
		fields = append(fields, proj.FieldName)
	}
	return fields, true
}

// RootPlace 返回剥离所有投影后的根变量位置。
func (p Place) RootPlace() Place {
	return Place{
		RootSymID: p.RootSymID,
		RootName:  p.RootName,
		RootType:  p.RootType,
		ElemType:  p.RootType,
		RootMut:   p.RootMut,
		IsParam:   p.IsParam,
		Span:      p.Span,
	}
}

// PrefixBeforeLastDeref 返回最后一个解引用投影 (* / Deref) 之前的引用持有者位置。
// 例如 &*parent 返回 parent；&*wrapper.ref 返回 wrapper.ref。
func (p Place) PrefixBeforeLastDeref() (Place, bool) {
	lastDeref := -1
	for i, proj := range p.Projections {
		if proj.Kind == ProjDeref {
			lastDeref = i
		}
	}
	if lastDeref < 0 {
		return Place{}, false
	}
	elemType := p.RootType
	var projs []Projection
	if lastDeref > 0 {
		projs = make([]Projection, lastDeref)
		copy(projs, p.Projections[:lastDeref])
		elemType = projs[lastDeref-1].ElemType
	}
	return Place{
		RootSymID:   p.RootSymID,
		RootName:    p.RootName,
		RootType:    p.RootType,
		ElemType:    elemType,
		RootMut:     p.RootMut,
		IsParam:     p.IsParam,
		Projections: projs,
		Span:        p.Span,
	}, true
}

// Key 返回不依赖源码 Span 的确定性位置唯一键，供借用流追踪与重叠判别索引使用。
func (p Place) Key() string {
	return p.String()
}

// AppendField 返回在当前位置末尾追加结构体字段投影的新 Place。
func (p Place) AppendField(fieldName string, fieldIdx int, elemType types.TypeID, span diagnostic.Span) Place {
	projs := make([]Projection, len(p.Projections)+1)
	copy(projs, p.Projections)
	projs[len(p.Projections)] = Projection{
		Kind:       ProjField,
		FieldName:  fieldName,
		FieldIndex: fieldIdx,
		ElemType:   elemType,
	}
	return Place{
		RootSymID:   p.RootSymID,
		RootName:    p.RootName,
		RootType:    p.RootType,
		ElemType:    elemType,
		RootMut:     p.RootMut,
		IsParam:     p.IsParam,
		Projections: projs,
		Span:        span,
	}
}

// AppendProj 返回在当前位置末尾追加任意投影的新 Place。
func (p Place) AppendProj(proj Projection, elemType types.TypeID, span diagnostic.Span) Place {
	projs := make([]Projection, len(p.Projections)+1)
	copy(projs, p.Projections)
	projs[len(p.Projections)] = proj
	return Place{
		RootSymID:   p.RootSymID,
		RootName:    p.RootName,
		RootType:    p.RootType,
		ElemType:    elemType,
		RootMut:     p.RootMut,
		IsParam:     p.IsParam,
		Projections: projs,
		Span:        span,
	}
}

// Overlaps 判断两个 Place 在无指针别名假设下的静态路径前缀是否可能重叠。
func (p Place) Overlaps(other Place) bool {
	if p.RootSymID == 0 || other.RootSymID == 0 || p.RootSymID != other.RootSymID {
		return false
	}
	minLen := len(p.Projections)
	if len(other.Projections) < minLen {
		minLen = len(other.Projections)
	}
	for i := 0; i < minLen; i++ {
		a := p.Projections[i]
		b := other.Projections[i]
		if a.Kind != b.Kind {
			// 数组常量与动态下标保守视为可能重叠
			if (a.Kind == ProjIndex || a.Kind == ProjConstIdx) && (b.Kind == ProjIndex || b.Kind == ProjConstIdx) {
				return true
			}
			return true
		}
		switch a.Kind {
		case ProjField:
			if a.FieldName != b.FieldName {
				return false
			}
		case ProjTupleField:
			if a.FieldIndex != b.FieldIndex {
				return false
			}
		case ProjDowncast:
			if a.VariantIdx != b.VariantIdx {
				return false
			}
		case ProjIndex:
			// 动态下标保守认为可能与任何同级数组下标重叠
			return true
		case ProjConstIdx:
			if a.ConstIndex != b.ConstIndex {
				return false
			}
		}
	}
	return true
}

// String 返回便于调试与 CFG 快照测试的确定性文本表示。
func (p Place) String() string {
	var sb strings.Builder
	sb.WriteString(p.RootName)
	sb.WriteString(fmt.Sprintf("#%d", p.RootSymID))
	for _, proj := range p.Projections {
		switch proj.Kind {
		case ProjField:
			sb.WriteByte('.')
			sb.WriteString(proj.FieldName)
		case ProjTupleField:
			sb.WriteString(fmt.Sprintf(".%d", proj.FieldIndex))
		case ProjConstIdx:
			sb.WriteString(fmt.Sprintf("[%d]", proj.ConstIndex))
		case ProjIndex:
			sb.WriteString("[*]")
		case ProjDeref:
			sb.WriteString(".*")
		case ProjDowncast:
			sb.WriteString(fmt.Sprintf("@v%d", proj.VariantIdx))
		}
	}
	return sb.String()
}

package tir

import (
	"fmt"
	"strings"
)

// LocalID 表示函数内参数或局部变量存储槽位的确定性唯一编号 (0 表示无效槽位)。
type LocalID uint32

// InvalidLocal 表示无效或未绑定的局部存储标识。
const InvalidLocal LocalID = 0

// ProjectionKind 定义 TIR 存储路径的结构化投影步骤。
type ProjectionKind uint8

const (
	ProjField      ProjectionKind = iota // 结构体具名/定序字段: .field
	ProjTupleField                       // 元组下标字段: .0
	ProjConstIndex                       // 定长常量数组下标: [k]
	ProjDynIndex                         // 动态索引访问: [idx]
	ProjDeref                            // 引用或原生指针解引用: .*
	ProjDowncast                         // 枚举变体向下转型载荷: @variant
)

// Projection 描述基于基址的一级结构化内存路径投影。
type Projection struct {
	Kind         ProjectionKind
	FieldName    string
	FieldIndex   int
	VariantIndex int
	VariantName  string
	ConstIndex   int64
	IndexValue   ValueID // ProjDynIndex 对应的计算下标寄存器（可选）
	IndexLocal   LocalID // ProjDynIndex 对应的局部变量索引（可选，用于纯所有权 CFG 回退）
	ElemType     TypeID
}

// Place 表示由确定性 LocalID 与结构化投影序列构成的后端无关存储位置。
type Place struct {
	Local       LocalID
	LocalName   string
	RootType    TypeID
	ElemType    TypeID
	Projections []Projection
}

// IsValid 判断 Place 是否绑定了合法的局部存储根。
func (p Place) IsValid() bool {
	return p.Local != InvalidLocal && p.RootType != InvalidType && p.ElemType != InvalidType
}

// Clone 返回深拷贝后的 Place，防止跨基本块或跨 Pass 共享投影切片引发篡改。
func (p Place) Clone() Place {
	cp := p
	if len(p.Projections) > 0 {
		cp.Projections = make([]Projection, len(p.Projections))
		copy(cp.Projections, p.Projections)
	}
	return cp
}

// String 生成确定性的可读存储路径表示（仅用于 Canonical Dump 与诊断）。
func (p Place) String() string {
	var sb strings.Builder
	if p.LocalName != "" {
		fmt.Fprintf(&sb, "_l%d(%s)", p.Local, p.LocalName)
	} else {
		fmt.Fprintf(&sb, "_l%d", p.Local)
	}
	for _, proj := range p.Projections {
		switch proj.Kind {
		case ProjField:
			if proj.FieldIndex >= 0 {
				fmt.Fprintf(&sb, ".%s#%d", proj.FieldName, proj.FieldIndex)
			} else {
				fmt.Fprintf(&sb, ".%s", proj.FieldName)
			}
		case ProjTupleField:
			fmt.Fprintf(&sb, ".%d", proj.FieldIndex)
		case ProjConstIndex:
			fmt.Fprintf(&sb, "[%d]", proj.ConstIndex)
		case ProjDynIndex:
			if proj.IndexValue != InvalidValue {
				fmt.Fprintf(&sb, "[%%%d]", proj.IndexValue)
			} else if proj.IndexLocal != InvalidLocal {
				fmt.Fprintf(&sb, "[_l%d]", proj.IndexLocal)
			} else {
				sb.WriteString("[*]")
			}
		case ProjDeref:
			sb.WriteString(".*")
		case ProjDowncast:
			if proj.VariantName != "" {
				fmt.Fprintf(&sb, "@%s#%d", proj.VariantName, proj.VariantIndex)
			} else {
				fmt.Fprintf(&sb, "@v%d", proj.VariantIndex)
			}
		}
	}
	return sb.String()
}

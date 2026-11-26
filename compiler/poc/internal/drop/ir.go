package drop

import (
	"fmt"
	"sort"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// DropFlagID 表示单个条件析构路径对应的栈 resident bool 标志编号 (0 表示无条件 / 无效标志)。
type DropFlagID uint32

// InvalidDropFlag 表示不依赖运行期标志的无条件状态。
const InvalidDropFlag DropFlagID = 0

// DropFlagOpKind 定义条件 Drop Flag 在 CFG 语句流中的显式状态操作。
type DropFlagOpKind uint8

const (
	FlagDecl     DropFlagOpKind = iota // 在函数入口声明栈驻留 bool 标志（默认 false：未拥有已初始化资源）
	FlagInit                           // 确定初始化或路径条件绑定时显式置为初始状态
	FlagSetTrue                        // 初始化 / 重初始化成功后置 true
	FlagSetFalse                       // 所有权移交 / FFI 转移 / 析构后置 false
	FlagCheck                          // 条件析构 OpCondDrop 检查标志是否为 true
)

// DropFlagMeta 描述单个 DropFlagID 所对应的精确存储位置（根变量或结构体子字段投影）及初始默认值。
type DropFlagMeta struct {
	ID         DropFlagID
	OwnerSymID symbols.SymbolID
	Place      cfg.Place
	Type       types.TypeID
	GlueID     string
	InitialVal bool
	IsField    bool
	Span       diagnostic.Span
}

// DropFlagEvent 记录在特定 CFG 基本块语句位置发出的类型化标志维护事件（供后续 ADR-009 TIR 降级消费）。
type DropFlagEvent struct {
	Point cfg.ProgramPoint
	Flag  DropFlagID
	Op    DropFlagOpKind
	Place cfg.Place
	Span  diagnostic.Span
}

// DropElaboratedFunc 保存单个函数经过只读克隆与 Drop 展开后的完整析构视图，不破坏原始 ProgramCFG。
type DropElaboratedFunc struct {
	SymID        symbols.SymbolID
	Name         string
	CFG          *cfg.FuncCFG // 独立深拷贝并插入了 OpDrop / OpCondDrop / OpFFITransfer 的 CFG
	Flags        map[DropFlagID]*DropFlagMeta
	FlagOrder    []DropFlagID
	FlagEvents   []DropFlagEvent
	FFIContracts map[cfg.ProgramPoint]string
}

// DropElaboratedProgram 是 Phase 2B.3 产出的不可变资源生命周期验证结果，直接衔接未来 ADR-009 TIR 降级。
type DropElaboratedProgram struct {
	ElaboratedCFG  *cfg.ProgramCFG
	Functions      map[symbols.SymbolID]*DropElaboratedFunc
	GlueTable      map[string]*DropGlue
	DropElaborated bool
}

// Dump 生成确定性的完整 Drop 展开快照文本（包含胶水目录、Drop Flags 与各函数 CFG 析构事件）。
func (p *DropElaboratedProgram) Dump() string {
	if p == nil {
		return "<nil DropElaboratedProgram>"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("DropElaborated=%v\n", p.DropElaborated))
	for _, gid := range SortedGlueIDs(p.GlueTable) {
		g := p.GlueTable[gid]
		sb.WriteString(fmt.Sprintf("glue %s kind=%s type=%s\n", g.ID, g.Kind, g.TypeName))
	}
	if p.ElaboratedCFG != nil {
		for _, fn := range p.ElaboratedCFG.Functions {
			if fn.FuncSym != nil {
				if df, ok := p.Functions[fn.FuncSym.ID]; ok && len(df.FlagOrder) > 0 {
					for _, fid := range df.FlagOrder {
						fm := df.Flags[fid]
						sb.WriteString(fmt.Sprintf("flag df%d place=%s init=%v glue=%s\n", fid, fm.Place.String(), fm.InitialVal, fm.GlueID))
					}
				}
			}
			sb.WriteString(fn.Dump())
		}
	}
	return sb.String()
}

// CloneProgramCFG 对已验证的 ProgramCFG 执行深拷贝，确保 Phase 2B.1/2B.2 原始移动与借用视图保持完全不可变可审计。
func CloneProgramCFG(src *cfg.ProgramCFG) *cfg.ProgramCFG {
	if src == nil {
		return nil
	}
	dst := &cfg.ProgramCFG{
		Functions: make([]*cfg.FuncCFG, 0, len(src.Functions)),
		BySymbol:  make(map[symbols.SymbolID]*cfg.FuncCFG, len(src.BySymbol)),
	}
	for _, fn := range src.Functions {
		cfn := CloneFuncCFG(fn)
		dst.Functions = append(dst.Functions, cfn)
		if cfn != nil && cfn.FuncSym != nil {
			dst.BySymbol[cfn.FuncSym.ID] = cfn
		}
	}
	return dst
}

// CloneFuncCFG 深拷贝单个 FuncCFG 及其基本块、语句与局部元数据映射。
func CloneFuncCFG(src *cfg.FuncCFG) *cfg.FuncCFG {
	if src == nil {
		return nil
	}
	cp := &cfg.FuncCFG{
		Name:       src.Name,
		FuncSym:    src.FuncSym,
		Decl:       src.Decl,
		ReturnType: src.ReturnType,
		Entry:      src.Entry,
		Exit:       src.Exit,
		Blocks:     make([]*cfg.BasicBlock, len(src.Blocks)),
		LocalOrder: append([]symbols.SymbolID(nil), src.LocalOrder...),
		Locals:     make(map[symbols.SymbolID]*cfg.LocalMeta, len(src.Locals)),
		NextTemp:   src.NextTemp,
		NextLoan:   src.NextLoan,
	}
	for sid, meta := range src.Locals {
		if meta != nil {
			m := *meta
			cp.Locals[sid] = &m
		}
	}
	for i, blk := range src.Blocks {
		if blk == nil {
			continue
		}
		cblk := &cfg.BasicBlock{
			ID:           blk.ID,
			Label:        blk.Label,
			Statements:   make([]cfg.Statement, len(blk.Statements)),
			Predecessors: append([]cfg.BlockID(nil), blk.Predecessors...),
			Successors:   append([]cfg.BlockID(nil), blk.Successors...),
			Terminator: cfg.Terminator{
				Kind:    blk.Terminator.Kind,
				Targets: append([]cfg.BlockID(nil), blk.Terminator.Targets...),
				Span:    blk.Terminator.Span,
			},
		}
		for j, st := range blk.Statements {
			cst := st
			if len(st.Locals) > 0 {
				cst.Locals = append([]symbols.SymbolID(nil), st.Locals...)
			}
			if len(st.ArgTemps) > 0 {
				cst.ArgTemps = append([]cfg.ValueID(nil), st.ArgTemps...)
			}
			if len(st.ArgPlaces) > 0 {
				cst.ArgPlaces = make([]cfg.Place, len(st.ArgPlaces))
				for k, ap := range st.ArgPlaces {
					cst.ArgPlaces[k] = ap
					if len(ap.Projections) > 0 {
						cst.ArgPlaces[k].Projections = append([]cfg.Projection(nil), ap.Projections...)
					}
				}
			}
			if len(st.AggTemps) > 0 {
				cst.AggTemps = append([]cfg.AggTemp(nil), st.AggTemps...)
			}
			if len(st.Place.Projections) > 0 {
				cst.Place.Projections = append([]cfg.Projection(nil), st.Place.Projections...)
			}
			if len(st.ParentPlace.Projections) > 0 {
				cst.ParentPlace.Projections = append([]cfg.Projection(nil), st.ParentPlace.Projections...)
			}
			cblk.Statements[j] = cst
		}
		cp.Blocks[i] = cblk
	}
	return cp
}

// SortFuncIDs 按确定性整数序排列 FuncSym ID。
func SortFuncIDs(m map[symbols.SymbolID]*DropElaboratedFunc) []symbols.SymbolID {
	out := make([]symbols.SymbolID, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

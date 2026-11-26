package cfg

import (
	"fmt"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// BlockID 表示函数 CFG 内基本块的唯一确定性整数编号。
type BlockID int

// InvalidBlock 表示未连接的空块标识。
const InvalidBlock BlockID = -1

// ValueID 表示函数内 CFG 引用表达式或临时载体的确定性唯一编号 (0 表示非引用或无临时值)。
type ValueID uint32

// InvalidValue 表示空或非引用跟踪临时值编号。
const InvalidValue ValueID = 0

// LoanID 表示函数内单次借用事件生成的唯一借用标识 (0 表示无效借用)。
type LoanID uint32

// InvalidLoan 表示空或未绑定借用编号。
const InvalidLoan LoanID = 0

// BorrowKind 显式区分共享借用、独占可变借用及重借用派生种类。
type BorrowKind uint8

const (
	BorrowShared         BorrowKind = iota // 不可变共享借用 (&Place)
	BorrowMut                              // 独占可变借用 (&mut Place)
	BorrowReborrowShared                   // 共享重借用 (&*r / r.method())
	BorrowReborrowMut                      // 可变重借用 (&mut *r / r.mut_method())
)

// ProgramPoint 精确定位函数控制流图中的单条有序语句位置。
type ProgramPoint struct {
	Block   BlockID
	StmtIdx int
}

// AggTemp 描述复合字面量在构造时注入的字段或下标级引用临时值。
type AggTemp struct {
	FieldName string
	Index     int
	Temp      ValueID
}

// OpKind 定义 CFG 语句事件的操作类别。
type OpKind uint8

const (
	// Phase 2B.1 核心所有权、初始化与移动操作
	OpStorageDecl OpKind = iota // 声明局部存储槽位
	OpUninitDecl                // 声明显式类型标注但无初始值的绑定
	OpInitAssign                // 绑定初始化写入（包含参数或 let/var 首次赋值）
	OpRead                      // 只读访问某个存储位置（如检查复合赋值左值或读取解引用根指针）
	OpCopy                      // 按位拷贝一个 Copy 类型位置（不使源位置失效）
	OpMove                      // 所有权转移一个非 Copy 类型位置（使源位置进入 Moved）
	OpAssign                    // 向位置执行完整赋值写入（恢复 Init 状态或覆盖现有值）
	OpReinit                    // 对处于 Moved 状态的 var 绑定重新赋值初始化
	OpReturn                    // 函数返回位置或引用临时值传输
	OpScopeEnter                // 进入词法作用域边界
	OpScopeExit                 // 离开词法作用域边界（携带作用域内需结束生命周期的局部符号列表）
	OpStorageDead               // 局部变量存储槽位生命周期结束标记
	OpNop                       // 空操作占位

	// Phase 2B.2 借用事件与引用值载体流动操作
	OpBorrowShared // 创建共享借用 &Place -> ValTemp (携带 LoanID)
	OpBorrowMut    // 创建独占可变借用 &mut Place -> ValTemp (携带 LoanID)
	OpReborrow     // 从父引用载体派生子借用 &*r / &mut *r -> ValTemp (携带 LoanID 与 ParentPlace)
	OpStaticRef    // 产生 Static 溯源的静态字符串视图引用 &str -> ValTemp
	OpCall         // 函数或方法调用：按求值顺序消费 ArgTemps（含 &self/&mut self）并产出 ValTemp
	OpAggregate    // 构造成员包含引用载体的结构体/元组/数组聚合值 -> ValTemp
	OpTempAssign   // 在控制流合流点前转发/汇合分支临时引用值 (SrcTemp -> ValTemp)

	// Phase 2B.3 预留析构与 FFI 生命周期事件
	OpDrop        // 预留：Phase 2B.3 确定性析构插入点
	OpCondDrop    // 预留：Phase 2B.3 基于 drop flag 的条件析构点
	OpFFITransfer // 预留：Phase 2B.3 FFI @owned 所有权移交点
)

// Statement 表示基本块中的单条有序所有权及借用载体事件。
type Statement struct {
	Kind    OpKind
	Place   Place
	Type    types.TypeID
	ScopeID int
	Locals  []symbols.SymbolID // 仅用于 OpScopeExit / OpStorageDead
	Span    diagnostic.Span

	// Phase 2B.2 借用标识、重借用父关系与引用载体临时值关联
	LoanID      LoanID
	BorrowKind  BorrowKind
	IsReborrow  bool
	ParentPlace Place
	ValTemp     ValueID
	SrcTemp     ValueID
	Callee      *symbols.Symbol
	CalleeName  string
	ArgTemps    []ValueID
	AggTemps    []AggTemp
}

// TermKind 定义基本块终结跳转指令类别。
type TermKind uint8

const (
	TermNone        TermKind = iota // 尚未封口（内部构建期临时状态）
	TermGoto                        // 无条件跳转至单一目标块
	TermSwitchInt                   // 多路条件分支跳转（if / while / match / ?）
	TermReturn                      // 函数返回出口
	TermUnreachable                 // 发散或死代码块出口
)

// Terminator 描述基本块末端的显式控制流转移边缘。
type Terminator struct {
	Kind    TermKind
	Targets []BlockID
	Span    diagnostic.Span
}

// BasicBlock 表示包含线性所有权事件序列与单出口终结指令的基本块。
type BasicBlock struct {
	ID           BlockID
	Label        string
	Statements   []Statement
	Terminator   Terminator
	Predecessors []BlockID
	Successors   []BlockID
}

// LocalMeta 记录函数内部出现的参数或局部绑定元数据，保持确定性索引序。
type LocalMeta struct {
	Sym      *symbols.Symbol
	Type     types.TypeID
	IsParam  bool
	ParamIdx int
	IsMut    bool
	HasInit  bool
	ScopeID  int
	Span     diagnostic.Span
}

// FuncCFG 表示单个函数或固有方法的完整所有权控制流图。
type FuncCFG struct {
	Name       string
	FuncSym    *symbols.Symbol
	Decl       *ast.FunctionDecl
	ReturnType types.TypeID
	Entry      BlockID
	Exit       BlockID
	Blocks     []*BasicBlock
	LocalOrder []symbols.SymbolID
	Locals     map[symbols.SymbolID]*LocalMeta
	NextTemp   ValueID
	NextLoan   LoanID
}

// ProgramCFG 汇聚编译单元内所有顶层函数与固有方法的所有权 CFG。
type ProgramCFG struct {
	Functions []*FuncCFG
	BySymbol  map[symbols.SymbolID]*FuncCFG
}

// String 返回单个基本块操作码的可读名称。
func (k OpKind) String() string {
	switch k {
	case OpStorageDecl:
		return "StorageDecl"
	case OpUninitDecl:
		return "UninitDecl"
	case OpInitAssign:
		return "InitAssign"
	case OpRead:
		return "Read"
	case OpCopy:
		return "Copy"
	case OpMove:
		return "Move"
	case OpAssign:
		return "Assign"
	case OpReinit:
		return "Reinit"
	case OpReturn:
		return "Return"
	case OpScopeEnter:
		return "ScopeEnter"
	case OpScopeExit:
		return "ScopeExit"
	case OpStorageDead:
		return "StorageDead"
	case OpBorrowShared:
		return "BorrowShared"
	case OpBorrowMut:
		return "BorrowMut"
	case OpReborrow:
		return "Reborrow"
	case OpStaticRef:
		return "StaticRef"
	case OpCall:
		return "Call"
	case OpAggregate:
		return "Aggregate"
	case OpTempAssign:
		return "TempAssign"
	case OpDrop:
		return "Drop(reserved)"
	case OpCondDrop:
		return "CondDrop(reserved)"
	case OpFFITransfer:
		return "FFITransfer(reserved)"
	default:
		return "Nop"
	}
}

// Dump 生成确定性的 FuncCFG 文本快照，用于单元测试与诊断调试。
func (f *FuncCFG) Dump() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("fn %s (entry=bb%d, exit=bb%d):\n", f.Name, f.Entry, f.Exit))
	for _, b := range f.Blocks {
		sb.WriteString(fmt.Sprintf("  bb%d (%s) preds=%v succs=%v:\n", b.ID, b.Label, b.Predecessors, b.Successors))
		for _, st := range b.Statements {
			switch st.Kind {
			case OpScopeEnter, OpScopeExit, OpStorageDead:
				sb.WriteString(fmt.Sprintf("    %s scope=%d locals=%v\n", st.Kind, st.ScopeID, st.Locals))
			case OpBorrowShared, OpBorrowMut, OpReborrow:
				sb.WriteString(fmt.Sprintf("    %s %s loan=L%d dst=v%d", st.Kind, st.Place.String(), st.LoanID, st.ValTemp))
				if st.IsReborrow {
					sb.WriteString(fmt.Sprintf(" parent=%s", st.ParentPlace.String()))
				}
				sb.WriteByte('\n')
			case OpStaticRef:
				sb.WriteString(fmt.Sprintf("    StaticRef dst=v%d\n", st.ValTemp))
			case OpCall:
				sb.WriteString(fmt.Sprintf("    Call %s args=%v dst=v%d\n", st.CalleeName, st.ArgTemps, st.ValTemp))
			case OpAggregate:
				sb.WriteString(fmt.Sprintf("    Aggregate items=%v dst=v%d\n", st.AggTemps, st.ValTemp))
			case OpTempAssign:
				sb.WriteString(fmt.Sprintf("    TempAssign v%d <- v%d\n", st.ValTemp, st.SrcTemp))
			default:
				sb.WriteString(fmt.Sprintf("    %s %s", st.Kind, st.Place.String()))
				if st.SrcTemp != InvalidValue {
					sb.WriteString(fmt.Sprintf(" src=v%d", st.SrcTemp))
				}
				if st.ValTemp != InvalidValue {
					sb.WriteString(fmt.Sprintf(" dst=v%d", st.ValTemp))
				}
				sb.WriteByte('\n')
			}
		}
		sb.WriteString(fmt.Sprintf("    -> %s %v\n", termKindName(b.Terminator.Kind), b.Terminator.Targets))
	}
	return sb.String()
}

func termKindName(k TermKind) string {
	switch k {
	case TermGoto:
		return "Goto"
	case TermSwitchInt:
		return "Switch"
	case TermReturn:
		return "Return"
	case TermUnreachable:
		return "Unreachable"
	default:
		return "None"
	}
}

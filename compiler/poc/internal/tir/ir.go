package tir

import "fmt"

// ValueID 表示函数作用域内确定性的 TIR 值/临时寄存器编号 (0 表示无效或无返回值)。
type ValueID uint32

// InvalidValue 表示空值寄存器编号。
const InvalidValue ValueID = 0

// FunctionID 表示编译单元内函数的确定性唯一编号 (0 表示无效)。
type FunctionID uint32

// InvalidFunc 表示未绑定内部函数编号。
const InvalidFunc FunctionID = 0

// BlockID 表示函数 CFG 内的基本块唯一编号 (-1 表示无效)。
type BlockID int

// InvalidBlock 表示未连接的空基本块编号。
const InvalidBlock BlockID = -1

// LoanID 表示借用事件继承自前端借用检查器的确定性借用编号 (0 表示无)。
type LoanID uint32

// DropFlagID 表示条件析构栈标志的确定性编号 (0 表示无条件)。
type DropFlagID uint32

// InvalidDropFlag 表示不依赖运行期标志的无条件状态。
const InvalidDropFlag DropFlagID = 0

// DropGlueID 表示编译单元析构胶水表中的确定性胶水编号 (0 表示无效)。
type DropGlueID uint32

// InvalidDropGlue 表示无效或无需析构的胶水编号。
const InvalidDropGlue DropGlueID = 0

// ContractID 表示经过校验的 FFI 所有权契约表编号 (0 表示无契约)。
type ContractID uint32

// InvalidContract 表示无或未绑定 FFI 契约。
const InvalidContract ContractID = 0

// SourceSpan 保留独立于前端 diagnostic 包的源码物理位置，供后续调试信息与诊断回溯消费。
type SourceSpan struct {
	File      string
	StartLine int
	StartCol  int
	StartByte int
	EndLine   int
	EndCol    int
	EndByte   int
}

// String 返回紧凑的确定性源码位置标签。
func (s SourceSpan) String() string {
	if s.File == "" && s.StartLine == 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d:%d", s.File, s.StartLine, s.StartCol)
}

// LocalDecl 记录函数形参或局部变量的后端无关存储描述。
type LocalDecl struct {
	ID       LocalID
	Name     string
	Type     TypeID
	IsParam  bool
	ParamIdx int
	IsMut    bool
	HasInit  bool
	ScopeID  int
	Span     SourceSpan
}

// ValueDecl 记录函数内每个已分配 ValueID 的类型和定义属性（SingleDef 或 JoinTemp）。
type ValueDecl struct {
	ID         ValueID
	Type       TypeID
	IsJoinTemp bool
	Span       SourceSpan
}

// ConstKind 定义 TIR 常量字面量种类。
type ConstKind uint8

const (
	ConstUnit ConstKind = iota
	ConstBool
	ConstInt
	ConstFloat
	ConstChar
	ConstByte
	ConstString
)

// Constant 表示无损、后端无关的常量数据。
type Constant struct {
	Kind     ConstKind
	BoolVal  bool
	IntText  string // 无损十进制表示（支持完整 i128/u128）
	FloatVal float64
	FloatRaw string
	RuneVal  rune
	ByteVal  byte
	StrVal   string
}

// String 返回常量值的确定性可读文本。
func (c Constant) String() string {
	switch c.Kind {
	case ConstUnit:
		return "()"
	case ConstBool:
		return fmt.Sprintf("%v", c.BoolVal)
	case ConstInt:
		return c.IntText
	case ConstFloat:
		if c.FloatRaw != "" {
			return c.FloatRaw
		}
		return fmt.Sprintf("%g", c.FloatVal)
	case ConstChar:
		return fmt.Sprintf("%q", c.RuneVal)
	case ConstByte:
		return fmt.Sprintf("b'\\x%02x'", c.ByteVal)
	case ConstString:
		return fmt.Sprintf("%q", c.StrVal)
	default:
		return "<const>"
	}
}

// OperandKind 定义指令与终结器操作数来源类型。
type OperandKind uint8

const (
	OperandInvalid OperandKind = iota
	OperandValue               // 读取 ValueID 寄存器值
	OperandConst               // 内联立即常量
	OperandPlace               // 直接引用内存位置 Place
	OperandFunc                // 内部函数符号引用
)

// Operand 表示带显式类型标注的 TIR 操作数。
type Operand struct {
	Kind   OperandKind
	Type   TypeID
	Value  ValueID
	Const  Constant
	Place  Place
	FuncID FunctionID
}

// ConstOperand 构造常量操作数。
func ConstOperand(tid TypeID, c Constant) Operand {
	return Operand{Kind: OperandConst, Type: tid, Const: c}
}

// ValueOperand 构造寄存器值操作数。
func ValueOperand(tid TypeID, v ValueID) Operand {
	return Operand{Kind: OperandValue, Type: tid, Value: v}
}

// PlaceOperand 构造存储位置操作数。
func PlaceOperand(p Place) Operand {
	return Operand{Kind: OperandPlace, Type: p.ElemType, Place: p}
}

// FuncOperand 构造内部函数引用操作数。
func FuncOperand(tid TypeID, fid FunctionID) Operand {
	return Operand{Kind: OperandFunc, Type: tid, FuncID: fid}
}

// UnaryOp 定义后端无关的一元计算操作符。
type UnaryOp uint8

const (
	UnaryNeg    UnaryOp = iota // 数值取负 (-)
	UnaryNot                   // 布尔逻辑非 (not / !)
	UnaryBitNot                // 整数按位取反 (~)
)

// String 返回一元操作符可读符号。
func (u UnaryOp) String() string {
	switch u {
	case UnaryNeg:
		return "-"
	case UnaryNot:
		return "not"
	case UnaryBitNot:
		return "~"
	default:
		return "<?unary>"
	}
}

// BinaryOp 定义后端无关的二元算术、位运算与比较操作符。
type BinaryOp uint8

const (
	BinAdd BinaryOp = iota
	BinSub
	BinMul
	BinDiv
	BinMod
	BinBitAnd
	BinBitOr
	BinBitXor
	BinShl
	BinShr
	BinEq
	BinNe
	BinLt
	BinLe
	BinGt
	BinGe
)

// String 返回二元操作符可读名称。
func (b BinaryOp) String() string {
	switch b {
	case BinAdd:
		return "+"
	case BinSub:
		return "-"
	case BinMul:
		return "*"
	case BinDiv:
		return "/"
	case BinMod:
		return "%"
	case BinBitAnd:
		return "&"
	case BinBitOr:
		return "|"
	case BinBitXor:
		return "^"
	case BinShl:
		return "<<"
	case BinShr:
		return ">>"
	case BinEq:
		return "=="
	case BinNe:
		return "!="
	case BinLt:
		return "<"
	case BinLe:
		return "<="
	case BinGt:
		return ">"
	case BinGe:
		return ">="
	default:
		return "<?bin>"
	}
}

// IsComparison 判断二元运算是否为返回布尔值的比较操作。
func (b BinaryOp) IsComparison() bool {
	return b >= BinEq && b <= BinGe
}

// CastKind 定义显式 `as` 类型转换语义类别。
type CastKind uint8

const (
	CastNumeric     CastKind = iota // 数值位宽/符号/浮点转换
	CastPtrToPtr                    // 原生指针可变性或元素指针类型转换
	CastPtrToInt                    // 指针转 usize/整数
	CastIntToPtr                    // 整数转原生指针
	CastRefToRawPtr                 // 引用显式转原生指针
)

// String 返回 CastKind 可读名。
func (c CastKind) String() string {
	switch c {
	case CastNumeric:
		return "Numeric"
	case CastPtrToPtr:
		return "PtrToPtr"
	case CastPtrToInt:
		return "PtrToInt"
	case CastIntToPtr:
		return "IntToPtr"
	case CastRefToRawPtr:
		return "RefToRawPtr"
	default:
		return "UnknownCast"
	}
}

// AggregateKind 定义聚合值构造操作类别。
type AggregateKind uint8

const (
	AggStruct AggregateKind = iota
	AggTuple
	AggArray
	AggArrayRepeat
	AggEnumVariant
	AggRange
	AggInterpString
)

// String 返回 AggregateKind 可读名。
func (a AggregateKind) String() string {
	switch a {
	case AggStruct:
		return "Struct"
	case AggTuple:
		return "Tuple"
	case AggArray:
		return "Array"
	case AggArrayRepeat:
		return "ArrayRepeat"
	case AggEnumVariant:
		return "EnumVariant"
	case AggRange:
		return "Range"
	case AggInterpString:
		return "InterpString"
	default:
		return "UnknownAgg"
	}
}

// AggregateItem 描述聚合构造中的单个有序成员操作数。
type AggregateItem struct {
	FieldName  string
	FieldIndex int
	Value      Operand
}

// BorrowKind 定义共享借用、独占可变借用及重借用派生类别。
type BorrowKind uint8

const (
	BorrowShared BorrowKind = iota
	BorrowMut
	BorrowReborrowShared
	BorrowReborrowMut
)

// String 返回 BorrowKind 可读名。
func (b BorrowKind) String() string {
	switch b {
	case BorrowShared:
		return "Shared"
	case BorrowMut:
		return "Mut"
	case BorrowReborrowShared:
		return "ReborrowShared"
	case BorrowReborrowMut:
		return "ReborrowMut"
	default:
		return "UnknownBorrow"
	}
}

// BuiltinKind 区分语言内建函数与受控内建方法原语。
type BuiltinKind uint8

const (
	BuiltinOther BuiltinKind = iota
	BuiltinPrint
	BuiltinPrintln
	BuiltinPanic
	BuiltinAssert
	BuiltinBytesEqual
	BuiltinStringFrom
	BuiltinAsPtr
	BuiltinAsMutPtr
	BuiltinAsBytes
	BuiltinLen
	BuiltinCapacity
	BuiltinIsEmpty
	BuiltinPushStr
	BuiltinTryUnwrap
)

// DropGlueKind 定义确定性资源清理胶水分类。
type DropGlueKind uint8

const (
	GlueNone DropGlueKind = iota
	GlueString
	GlueStruct
	GlueEnum
	GlueTuple
	GlueArray
	GlueForeignResource
	GlueRegisteredResource
	GlueCustomDropReserved
)

// String 返回 DropGlueKind 可读名。
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
		return "CustomDropReserved"
	default:
		return "None"
	}
}

// DropGlueField 描述结构体/变体成员的清理胶水引用。
type DropGlueField struct {
	Name   string
	Index  int
	Type   TypeID
	GlueID DropGlueID
}

// DropGlueElem 描述元组/数组成员的清理胶水引用。
type DropGlueElem struct {
	Index  int
	Type   TypeID
	GlueID DropGlueID
}

// DropGlueVariant 描述枚举变体载荷的清理方案。
type DropGlueVariant struct {
	Name         string
	Index        int
	TupleElems   []DropGlueElem
	StructFields []DropGlueField
}

// DropGlueDesc 表示单一具体类型的后端无关析构胶水方案。
type DropGlueDesc struct {
	ID            DropGlueID
	CanonicalName string
	Kind          DropGlueKind
	Type          TypeID
	TypeName      string
	Fields        []DropGlueField
	Variants      []DropGlueVariant
	TupleElems    []DropGlueElem
	ElemType      TypeID
	ArrayLen      int64
	ElemGlueID    DropGlueID
	ReleaseSymbol string // 仅记录逻辑释放函数符号（非 C 语句模板）
}

// DropFlag 描述函数栈驻留的条件析构布尔标志元数据。
type DropFlag struct {
	ID         DropFlagID
	Name       string
	OwnerLocal LocalID
	Place      Place
	Type       TypeID
	GlueID     DropGlueID
	Initial    bool
	Span       SourceSpan
}

// FFIOwnership 保留经过校验的 FFI 参数/返回值所有权语义标签。
type FFIOwnership uint8

const (
	FFIRawUnmanaged FFIOwnership = iota
	FFIOwnedTransfer
	FFIBorrowShared
	FFIBorrowMut
	FFIOwnedReturn
	FFIOpaqueHandle
	FFIUnknown
)

// String 返回 FFIOwnership 可读名。
func (o FFIOwnership) String() string {
	switch o {
	case FFIOwnedTransfer:
		return "owned_transfer"
	case FFIBorrowShared:
		return "borrow"
	case FFIBorrowMut:
		return "borrow_mut"
	case FFIOwnedReturn:
		return "owned_return"
	case FFIOpaqueHandle:
		return "opaque_handle"
	case FFIRawUnmanaged:
		return "raw_unmanaged"
	default:
		return "unknown"
	}
}

// ForeignHeaderDesc 记录经前端解析并验证的外部头文件引入元数据（后端无关导入标识）。
type ForeignHeaderDesc struct {
	HeaderPath string
}

// FFICatalogDesc 记录经过 Phase 2B.3.2 校验的外部 FFI 目录信封身份。
type FFICatalogDesc struct {
	Namespace     string
	SchemaVersion string
	HeaderPath    string
}

// FFIParamDesc 记录单个 FFI 形参的后端无关所有权及借用契约。
type FFIParamDesc struct {
	ParamIndex int
	ParamName  string
	CType      string
	Ownership  FFIOwnership
}

// FFIContractDesc 记录经由编译前端认证的单条 FFI 函数所有权契约。
type FFIContractDesc struct {
	ID                  ContractID
	ContractKey         string
	Namespace           string
	Function            string
	HeaderPath          string
	SchemaVersion       string
	CallingConv         string
	Params              []FFIParamDesc
	ReturnOwnership     FFIOwnership
	ReturnCType         string
	ReturnTypeName      string
	ReturnReleaseSymbol string
	BorrowsParam        int
}

// OpKind 定义 TIR 指令族（最小自洽且显式覆盖存储、计算、所有权、调用与生命周期语义）。
type OpKind uint8

const (
	// 存储与内存生命周期
	OpStorageLive OpKind = iota
	OpStorageDead
	OpScopeEnter
	OpScopeExit
	OpInit
	OpLoad
	OpStore
	OpComputeAddr

	// 值计算
	OpConst
	OpUnary
	OpBinary
	OpCast
	OpValueAssign
	OpAggregateConstruct
	OpExtract

	// 所有权与借用事件
	OpCopy
	OpMove
	OpAssign
	OpBorrow
	OpStaticRef
	OpOwnershipTransfer

	// 内部调用、内建调用与受控 FFI 调用
	OpCall
	OpBuiltinCall
	OpForeignCall

	// 确定性资源析构与标志维护
	OpDrop
	OpCondDrop
	OpDropFlagDecl
	OpDropFlagSet
	OpDropFlagCheck
)

// String 返回 OpKind 的确定性可读助记符。
func (k OpKind) String() string {
	switch k {
	case OpStorageLive:
		return "StorageLive"
	case OpStorageDead:
		return "StorageDead"
	case OpScopeEnter:
		return "ScopeEnter"
	case OpScopeExit:
		return "ScopeExit"
	case OpInit:
		return "Init"
	case OpLoad:
		return "Load"
	case OpStore:
		return "Store"
	case OpComputeAddr:
		return "ComputeAddr"
	case OpConst:
		return "Const"
	case OpUnary:
		return "Unary"
	case OpBinary:
		return "Binary"
	case OpCast:
		return "Cast"
	case OpValueAssign:
		return "ValueAssign"
	case OpAggregateConstruct:
		return "AggregateConstruct"
	case OpExtract:
		return "Extract"
	case OpCopy:
		return "Copy"
	case OpMove:
		return "Move"
	case OpAssign:
		return "Assign"
	case OpBorrow:
		return "Borrow"
	case OpStaticRef:
		return "StaticRef"
	case OpOwnershipTransfer:
		return "OwnershipTransfer"
	case OpCall:
		return "Call"
	case OpBuiltinCall:
		return "BuiltinCall"
	case OpForeignCall:
		return "ForeignCall"
	case OpDrop:
		return "Drop"
	case OpCondDrop:
		return "CondDrop"
	case OpDropFlagDecl:
		return "DropFlagDecl"
	case OpDropFlagSet:
		return "DropFlagSet"
	case OpDropFlagCheck:
		return "DropFlagCheck"
	default:
		return "InvalidOp"
	}
}

// Instruction 表示基本块内单条显式类型化 TIR 指令。
type Instruction struct {
	Kind OpKind
	Type TypeID
	Dest ValueID
	Span SourceSpan

	// 存储、位置与作用域关联
	Local       LocalID
	ScopeID     int
	Locals      []LocalID
	Place       Place
	TargetPlace Place

	// 操作数与计算字段
	Const       Constant
	UnaryOp     UnaryOp
	BinaryOp    BinaryOp
	CastKind    CastKind
	Src         Operand
	Left        Operand
	Right       Operand
	IsReinit    bool
	IsInclusive bool

	// 聚合构造与抽取字段
	AggKind     AggregateKind
	AggItems    []AggregateItem
	RepeatLen   int64
	VariantIdx  int
	VariantName string
	ExtractProj Projection

	// 借用元数据
	BorrowKind  BorrowKind
	LoanID      LoanID
	ParentPlace *Place

	// 调用与 FFI 元数据
	CalleeFunc      FunctionID
	CalleeNamespace string
	CalleeName      string
	CallingConv     string
	BuiltinKind     BuiltinKind
	Args            []Operand
	ArgPlaces       []Place
	ArgOwnership    []FFIOwnership
	ReturnOwnership FFIOwnership
	ContractID      ContractID
	HasContract     bool
	InUnsafe        bool

	// Drop 胶水与标志维护
	GlueID   DropGlueID
	FlagID   DropFlagID
	FlagBool bool
}

// TermKind 定义 TIR 基本块终结跳转指令。
type TermKind uint8

const (
	TermNone TermKind = iota
	TermGoto
	TermBranchIf
	TermSwitch
	TermReturn
	TermUnreachable
)

// SwitchKind 记录多路分支原本控制流结构来源（便于优化及多后端降低）。
type SwitchKind uint8

const (
	SwitchGeneric SwitchKind = iota
	SwitchMatch
	SwitchTry
	SwitchLoopIter
)

// CaseKind 定义 Switch 跳转分支的判定条件类别。
type CaseKind uint8

const (
	CaseInt CaseKind = iota
	CaseEnumVariant
	CasePatternArm
	CaseTryOk
	CaseTryErr
	CaseLoopNext
	CaseLoopDone
)

// SwitchCase 描述多路分发终结器的单个明确分支标签及目标块。
type SwitchCase struct {
	Kind         CaseKind
	IntValue     int64
	VariantIndex int
	Label        string
	Guarded      bool
	Target       BlockID
}

// Terminator 描述基本块末端的唯一显式控制流转移指令。
type Terminator struct {
	Kind       TermKind
	Target     BlockID
	Cond       Operand
	ThenBlock  BlockID
	ElseBlock  BlockID
	SwitchKind SwitchKind
	Cases      []SwitchCase
	DefaultBlk BlockID
	ReturnVal  *Operand
	IsTrap     bool
	TrapReason string
	Span       SourceSpan
}

// BasicBlock 表示由线性 TIR 指令列表与唯一 Terminator 构成的基本块。
type BasicBlock struct {
	ID           BlockID
	Label        string
	Instructions []Instruction
	Terminator   Terminator
	Predecessors []BlockID
	Successors   []BlockID
}

// Function 表示经过完整验证的后端无关 TIR 函数。
type Function struct {
	ID          FunctionID
	Name        string
	CallingConv string
	ParamTypes  []TypeID
	ReturnType  TypeID
	EntryBlock  BlockID
	ExitBlock   BlockID
	Locals      []LocalDecl
	Values      []ValueDecl
	DropFlags   []DropFlag
	Blocks      []*BasicBlock
	Span        SourceSpan
}

// LocalByID 按 LocalID 查找局部存储声明。
func (f *Function) LocalByID(id LocalID) (LocalDecl, bool) {
	if f == nil || id == InvalidLocal {
		return LocalDecl{}, false
	}
	idx := int(id) - 1
	if idx >= 0 && idx < len(f.Locals) && f.Locals[idx].ID == id {
		return f.Locals[idx], true
	}
	for _, l := range f.Locals {
		if l.ID == id {
			return l, true
		}
	}
	return LocalDecl{}, false
}

// ValueByID 按 ValueID 查找寄存器声明。
func (f *Function) ValueByID(id ValueID) (ValueDecl, bool) {
	if f == nil || id == InvalidValue {
		return ValueDecl{}, false
	}
	idx := int(id) - 1
	if idx >= 0 && idx < len(f.Values) && f.Values[idx].ID == id {
		return f.Values[idx], true
	}
	for _, v := range f.Values {
		if v.ID == id {
			return v, true
		}
	}
	return ValueDecl{}, false
}

// DropFlagByID 按 DropFlagID 查找标志定义。
func (f *Function) DropFlagByID(id DropFlagID) (DropFlag, bool) {
	if f == nil || id == InvalidDropFlag {
		return DropFlag{}, false
	}
	for _, df := range f.DropFlags {
		if df.ID == id {
			return df, true
		}
	}
	return DropFlag{}, false
}

// BlockByID 按 BlockID 查找基本块。
func (f *Function) BlockByID(id BlockID) (*BasicBlock, bool) {
	if f == nil || id < 0 || int(id) >= len(f.Blocks) {
		return nil, false
	}
	b := f.Blocks[id]
	if b == nil || b.ID != id {
		for _, cand := range f.Blocks {
			if cand != nil && cand.ID == id {
				return cand, true
			}
		}
		return nil, false
	}
	return b, true
}

// Program 表示编译单元级的后端无关 TIR 模块容器。
type Program struct {
	Types          *TypeTable
	Functions      []*Function
	DropGlues      []DropGlueDesc
	ForeignHeaders []ForeignHeaderDesc
	FFICatalogs    []FFICatalogDesc
	FFIContracts   []FFIContractDesc
	SourceFiles    []string
	OwnershipOK    bool
	DropOK         bool
}

// FunctionByID 按 FunctionID 查找函数定义。
func (p *Program) FunctionByID(id FunctionID) (*Function, bool) {
	if p == nil || id == InvalidFunc {
		return nil, false
	}
	for _, fn := range p.Functions {
		if fn != nil && fn.ID == id {
			return fn, true
		}
	}
	return nil, false
}

// GlueByID 按 DropGlueID 查找析构胶水描述符。
func (p *Program) GlueByID(id DropGlueID) (DropGlueDesc, bool) {
	if p == nil || id == InvalidDropGlue {
		return DropGlueDesc{}, false
	}
	for _, g := range p.DropGlues {
		if g.ID == id {
			return g, true
		}
	}
	return DropGlueDesc{}, false
}

// ContractByID 按 ContractID 查找 FFI 所有权契约描述符。
func (p *Program) ContractByID(id ContractID) (FFIContractDesc, bool) {
	if p == nil || id == InvalidContract {
		return FFIContractDesc{}, false
	}
	for _, c := range p.FFIContracts {
		if c.ID == id {
			return c, true
		}
	}
	return FFIContractDesc{}, false
}

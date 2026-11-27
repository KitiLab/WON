package cfg

import (
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// BridgeValID 为 Phase 2C TIR 降级提供独立于 Phase 2B 引用跟踪 Temp 的确定性表达式值编号 (0 表示无效)。
// 该编号绝不改变 FuncCFG.NextTemp 或 FuncCFG.Dump() 快照。
type BridgeValID uint32

// InvalidBridgeVal 表示空或未产生值的表达式编号。
const InvalidBridgeVal BridgeValID = 0

// BridgeValDecl 记录每个 BridgeValID 的规范类型与合流属性，确保下游 TIR 生成具备精确类型表。
type BridgeValDecl struct {
	ID         BridgeValID
	Type       types.TypeID
	IsJoinTemp bool
	Span       diagnostic.Span
}

// ComputeKind 定义所有权 CFG 语句之外附加的纯值计算操作种类，供 TIR 降级器精确还原表达式树顺序。
type ComputeKind uint8

const (
	CompConst        ComputeKind = iota // 字面量常量 (int/float/bool/char/byte/str/unit)
	CompUnary                           // 一元运算 (- / not / ! / ~ / *)
	CompBinary                          // 二元运算 (+,-,*,/,%,&,|,^,<<,>>,==,!=,<,<=,>,>=)
	CompCast                            // 显式类型转换 (expr as Type)
	CompEnumUnit                        // 单元枚举变体值 (Option::None, Color::Red 等)
	CompFuncRef                         // 具名函数值引用
	CompAggregate                       // 非引用聚合构造 (struct / tuple / array / array_repeat / enum_struct)
	CompExtractField                    // 右值结构体/元组字段抽取
	CompExtractIndex                    // 右值数组/切片索引或范围切片
	CompRange                           // 范围表达式 (start..end / start..=end)
	CompInterpString                    // 插值字符串装配
	CompTryUnwrap                       // '?' 成功分支解包 Ok(v) / Some(v)
)

// BridgeAggItem 描述聚合或插值构造中单个成员在 Bridge 中的值或位置来源。
type BridgeAggItem struct {
	FieldName  string
	FieldIndex int
	Value      BridgeValID
	Shorthand  Place // 若为 Point { x } 字段简写形式，保留精确局部存储位置
}

// BridgeInterpPart 描述插值字符串的单个常量文本或内嵌表达式片段。
type BridgeInterpPart struct {
	IsExpr  bool
	Literal string
	Value   BridgeValID
}

// ComputeOp 表示一条附着在 CFG 语句或终结器之前按求值顺序执行的纯计算步骤。
type ComputeOp struct {
	Kind ComputeKind
	Dest BridgeValID
	Type types.TypeID
	Span diagnostic.Span

	// 常量字段
	ConstKind string // "unit"|"bool"|"int"|"float"|"char"|"byte"|"string"
	BoolVal   bool
	IntText   string
	FloatVal  float64
	FloatRaw  string
	RuneVal   rune
	ByteVal   byte
	StrVal    string

	// 操作符与操作数
	Op          string
	Src         BridgeValID
	Left        BridgeValID
	Right       BridgeValID
	TargetType  types.TypeID
	FuncSym     *symbols.Symbol
	VariantInfo *symbols.VariantInfo
	VariantIdx  int
	VariantName string

	// 聚合与抽取
	AggKind     string // "struct"|"tuple"|"array"|"array_repeat"|"enum_variant"
	AggItems    []BridgeAggItem
	RepeatLen   int64
	FieldName   string
	FieldIdx    int
	RangeStart  BridgeValID
	RangeEnd    BridgeValID
	IsRange     bool
	IsInclusive bool
	InterpParts []BridgeInterpPart
}

// CallCategory 区分普通内部调用、内建原语、枚举元组变体构造、内建方法及受控 FFI 调用。
type CallCategory uint8

const (
	CallNormal CallCategory = iota
	CallBuiltin
	CallEnumVariant
	CallMethodBuiltin
	CallForeignFFI
)

// BridgePatternBind 记录 match/let/for 模式绑定到源表达式或负载投影的精确拆解关系。
type BridgePatternBind struct {
	SourceVal   BridgeValID
	VariantName string
	VariantIdx  int
	FieldName   string
	TupleIndex  int
	HasSubProj  bool
}

// StatementBridge 为 cfg.Statement 提供无损 TIR 语义桥接信息（不影响 Dump 快照）。
type StatementBridge struct {
	PreComputes        []ComputeOp
	ResultVal          BridgeValID
	SrcVal             BridgeValID
	IndexVals          []BridgeValID
	ArgVals            []BridgeValID
	CallCat            CallCategory
	ForeignNS          string
	VariantInfo        *symbols.VariantInfo
	VariantIdx         int
	AssignOp           string
	PatternBind        *BridgePatternBind
	ImplicitTailRetVal BridgeValID
}

// BridgeSwitchKind 标识条件终结器原本的语言控制流语义类别。
type BridgeSwitchKind uint8

const (
	BridgeSwitchNone BridgeSwitchKind = iota
	BridgeSwitchIf
	BridgeSwitchWhile
	BridgeSwitchFor
	BridgeSwitchLogicAnd
	BridgeSwitchLogicOr
	BridgeSwitchMatch
	BridgeSwitchMatchGuard
	BridgeSwitchTry
)

// BridgeMatchCase 描述 Switch 终结器每个分支目标的明确标签与模式元数据。
type BridgeMatchCase struct {
	Label        string
	VariantIndex int
	IntValue     int64
	IsIntLiteral bool
	Guarded      bool
	Target       BlockID
}

// TerminatorBridge 为 cfg.Terminator 提供显式条件操作数、分支合流赋值与返回源值。
type TerminatorBridge struct {
	PreComputes        []ComputeOp
	SwitchKind         BridgeSwitchKind
	CondVal            BridgeValID
	YieldDest          BridgeValID
	YieldVal           BridgeValID
	ShortCircuitEnable bool
	ShortCircuitVal    bool
	MatchCases         []BridgeMatchCase
	RetVal             BridgeValID
	TryErrSourceVal    BridgeValID
	IsTrap             bool
	TrapReason         string
}

// CloneStatementBridge 深拷贝 StatementBridge，供 Drop CFG 克隆使用。
func CloneStatementBridge(b *StatementBridge) *StatementBridge {
	if b == nil {
		return nil
	}
	cp := *b
	if len(b.PreComputes) > 0 {
		cp.PreComputes = make([]ComputeOp, len(b.PreComputes))
		for i, c := range b.PreComputes {
			cp.PreComputes[i] = c
			if len(c.AggItems) > 0 {
				cp.PreComputes[i].AggItems = append([]BridgeAggItem(nil), c.AggItems...)
			}
			if len(c.InterpParts) > 0 {
				cp.PreComputes[i].InterpParts = append([]BridgeInterpPart(nil), c.InterpParts...)
			}
		}
	}
	if len(b.IndexVals) > 0 {
		cp.IndexVals = append([]BridgeValID(nil), b.IndexVals...)
	}
	if len(b.ArgVals) > 0 {
		cp.ArgVals = append([]BridgeValID(nil), b.ArgVals...)
	}
	if b.PatternBind != nil {
		pb := *b.PatternBind
		cp.PatternBind = &pb
	}
	return &cp
}

// CloneTerminatorBridge 深拷贝 TerminatorBridge，供 Drop CFG 克隆使用。
func CloneTerminatorBridge(b *TerminatorBridge) *TerminatorBridge {
	if b == nil {
		return nil
	}
	cp := *b
	if len(b.PreComputes) > 0 {
		cp.PreComputes = make([]ComputeOp, len(b.PreComputes))
		for i, c := range b.PreComputes {
			cp.PreComputes[i] = c
			if len(c.AggItems) > 0 {
				cp.PreComputes[i].AggItems = append([]BridgeAggItem(nil), c.AggItems...)
			}
			if len(c.InterpParts) > 0 {
				cp.PreComputes[i].InterpParts = append([]BridgeInterpPart(nil), c.InterpParts...)
			}
		}
	}
	if len(b.MatchCases) > 0 {
		cp.MatchCases = append([]BridgeMatchCase(nil), b.MatchCases...)
	}
	return &cp
}

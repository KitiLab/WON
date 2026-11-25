// Package symbols 定义了 WON 编译器的符号实体、词法作用域链及两阶段声明解析器
package symbols

import (
	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// SymbolID 表示编译器会话中符号的唯一递增编号
type SymbolID uint64

// SymbolKind 表示符号的语义分类
type SymbolKind int

const (
	SymUnknown     SymbolKind = iota
	SymFunc                   // 顶层或模块函数
	SymMethod                 // 结构体或枚举的固有方法
	SymStruct                 // 用户定义结构体类型
	SymEnum                   // 用户定义或内置枚举类型
	SymEnumVariant            // 枚举变体构造器或常量
	SymTrait                  // 特质接口声明
	SymTypeAlias              // 类型别名
	SymTypeParam              // 泛型类型形参
	SymVar                    // 局部变量
	SymParam                  // 函数形参
	SymModule                 // 内联或外部模块
	SymBuiltin                // 编译器内置函数 (println, bytes_equal, Some, Ok, Err, None)
)

// VariantKind 区分枚举变体的载荷形式
type VariantKind int

const (
	VariantUnit   VariantKind = iota // 无载荷单元变体，如 None, Pending
	VariantTuple                     // 元组载荷变体，如 Some(T), Circle(f64)
	VariantStruct                    // 结构体字段载荷变体，如 Click: x: i64, y: i64
)

// FieldInfo 记录结构体或结构体枚举变体的字段语义元数据
type FieldInfo struct {
	Name  string
	Type  types.TypeID
	IsPub bool
	IsVar bool
	Span  diagnostic.Span
}

// VariantInfo 记录枚举变体的完整签名与载荷信息
type VariantInfo struct {
	SymbolID     SymbolID
	Name         string
	Kind         VariantKind
	TupleTypes   []types.TypeID
	StructFields []FieldInfo
	FieldMap     map[string]*FieldInfo
	Span         diagnostic.Span
}

// Symbol 表示一个经过解析的命名实体及其类型与声明位置
type Symbol struct {
	ID           SymbolID
	Kind         SymbolKind
	Name         string
	Type         types.TypeID
	Span         diagnostic.Span
	LetTokenSpan diagnostic.Span // 用于针对 let 声明生成 var 自动修复建议
	IsMut        bool
	IsPub        bool
	Defined      bool // 局部变量是否已完成声明并初始化

	// 函数与方法签名详情
	ParamNames []string
	ParamTypes []types.TypeID
	ParamIsMut []bool
	ReturnType types.TypeID
	HasSelf    bool
	SelfIsRef  bool
	SelfIsMut  bool
	OwnerSymID SymbolID     // 若为方法或变体，指向其所属 Struct / Enum 符号 ID
	OwnerType  types.TypeID // 所属类型的标准 TypeID

	// 泛型声明参数
	GenericParamIDs   []SymbolID
	GenericParamNames []string

	// 结构体字段元数据
	Fields   []FieldInfo
	FieldMap map[string]*FieldInfo

	// 枚举变体元数据
	Variants   []VariantInfo
	VariantMap map[string]*VariantInfo

	// 关联方法映射（仅包含固有 impl 方法，严格区分 trait 方法）
	Methods map[string]*Symbol

	// 指向原始 AST 声明节点（可为 nil）
	Decl ast.Node
}

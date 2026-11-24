// Package ast 定义了 WON 规范语法树的完整抽象语法树 (AST) 节点模型
// 保留源码物理 Span 信息以支撑高精度结构化诊断，并兼容 Phase 0B 语义校验与 C11 代码生成接口。
package ast

import (
	"fmt"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
)

// Node 表示抽象语法树的基础节点接口
type Node interface {
	GetSpan() diagnostic.Span
}

// Decl 表示顶层声明节点接口
type Decl interface {
	Node
	declNode()
}

// Stmt 表示语句节点接口
type Stmt interface {
	Node
	stmtNode()
}

// Expr 表示表达式节点接口
type Expr interface {
	Node
	exprNode()
}

// TypeNode 表示结构化类型签名节点接口
type TypeNode interface {
	Node
	typeNode()
	String() string
}

// Pattern 表示解构与模式匹配节点接口
type Pattern interface {
	Node
	patternNode()
}

// Program 是源文件的顶层根节点
type Program struct {
	Imports       []*ImportDecl       // #import C 头文件引入指令
	ModuleImports []*ModuleImportDecl // WON 模块引入声明
	Functions     []*FunctionDecl     // 顶层函数声明（兼容既有 checker/codegen）
	Decls         []Decl              // 按源码顺序记录的所有顶层项目
	Span          diagnostic.Span
}

func (p *Program) GetSpan() diagnostic.Span { return p.Span }

// --- 顶层声明与模块节点 ---

// ImportDecl 表示 C 头文件引入指令，如 #import <zlib.h>
type ImportDecl struct {
	HeaderPath string
	Span       diagnostic.Span
}

func (i *ImportDecl) GetSpan() diagnostic.Span { return i.Span }
func (i *ImportDecl) declNode()                {}

// ModuleImportDecl 表示 WON 模块引入声明: import std::fs [as fs] 或 from math import a, b
type ModuleImportDecl struct {
	IsFrom   bool
	Path     []string
	Alias    string
	Items    []string
	Wildcard bool
	Span     diagnostic.Span
}

func (m *ModuleImportDecl) GetSpan() diagnostic.Span { return m.Span }
func (m *ModuleImportDecl) declNode()                {}

// GenericParam 表示泛型形参: T [: Trait1 + Trait2]
type GenericParam struct {
	Name   string
	Bounds []TypeNode
	Span   diagnostic.Span
}

func (g *GenericParam) GetSpan() diagnostic.Span { return g.Span }

// Param 表示函数形参（含 self / &self / &mut self 接收者）
type Param struct {
	IsMut   bool
	IsSelf  bool
	IsRef   bool
	Name    string
	Type    string // 类型文本串（兼容旧后端）
	TypeAST TypeNode
	Span    diagnostic.Span
}

func (p *Param) GetSpan() diagnostic.Span { return p.Span }

// FunctionDecl 表示函数或方法声明
type FunctionDecl struct {
	IsPub         bool
	Name          string
	GenericParams []*GenericParam
	Params        []*Param
	ReturnType    string // 返回类型文本串（兼容旧后端）
	ReturnTypeAST TypeNode
	Body          *BlockStmt // 若为 trait 无默认体方法，则 Body 可为 nil
	Span          diagnostic.Span
}

func (f *FunctionDecl) GetSpan() diagnostic.Span { return f.Span }
func (f *FunctionDecl) declNode()                {}

// StructField 表示结构体或结构体枚举变体的字段
type StructField struct {
	IsPub bool
	IsVar bool
	Name  string
	Type  TypeNode
	Span  diagnostic.Span
}

func (s *StructField) GetSpan() diagnostic.Span { return s.Span }

// StructDecl 表示结构体定义
type StructDecl struct {
	IsPub         bool
	Name          string
	GenericParams []*GenericParam
	Fields        []*StructField
	Span          diagnostic.Span
}

func (s *StructDecl) GetSpan() diagnostic.Span { return s.Span }
func (s *StructDecl) declNode()                {}

// EnumVariant 表示枚举变体（单元、元组载荷或结构体字段载荷）
type EnumVariant struct {
	Name         string
	TupleTypes   []TypeNode
	StructFields []*StructField
	Span         diagnostic.Span
}

func (e *EnumVariant) GetSpan() diagnostic.Span { return e.Span }

// EnumDecl 表示代数数据类型枚举定义
type EnumDecl struct {
	IsPub         bool
	Name          string
	GenericParams []*GenericParam
	Variants      []*EnumVariant
	Span          diagnostic.Span
}

func (e *EnumDecl) GetSpan() diagnostic.Span { return e.Span }
func (e *EnumDecl) declNode()                {}

// TraitDecl 表示特质接口定义
type TraitDecl struct {
	IsPub         bool
	Name          string
	GenericParams []*GenericParam
	Methods       []*FunctionDecl
	Span          diagnostic.Span
}

func (t *TraitDecl) GetSpan() diagnostic.Span { return t.Span }
func (t *TraitDecl) declNode()                {}

// ImplBlock 表示特质实现块或固有方法实现块
type ImplBlock struct {
	GenericParams []*GenericParam
	TraitType     TypeNode // 若为固有 impl，则为 nil
	TargetType    TypeNode
	Methods       []*FunctionDecl
	Span          diagnostic.Span
}

func (i *ImplBlock) GetSpan() diagnostic.Span { return i.Span }
func (i *ImplBlock) declNode()                {}

// TypeAliasDecl 表示类型别名定义: [pub] type Alias = Target
type TypeAliasDecl struct {
	IsPub         bool
	Name          string
	GenericParams []*GenericParam
	TargetType    TypeNode
	Span          diagnostic.Span
}

func (t *TypeAliasDecl) GetSpan() diagnostic.Span { return t.Span }
func (t *TypeAliasDecl) declNode()                {}

// ModuleDecl 表示内联模块声明: mod name: ...
type ModuleDecl struct {
	IsPub bool
	Name  string
	Decls []Decl
	Span  diagnostic.Span
}

func (m *ModuleDecl) GetSpan() diagnostic.Span { return m.Span }
func (m *ModuleDecl) declNode()                {}

// --- 类型签名节点 ---

// NamedType 表示具名路径类型与泛型实参，如 i32, Vec[T], std::io::Error
type NamedType struct {
	Path        []string
	GenericArgs []TypeNode
	Span        diagnostic.Span
}

func (n *NamedType) GetSpan() diagnostic.Span { return n.Span }
func (n *NamedType) typeNode()                {}
func (n *NamedType) String() string {
	base := strings.Join(n.Path, "::")
	if len(n.GenericArgs) == 0 {
		return base
	}
	args := make([]string, len(n.GenericArgs))
	for i, a := range n.GenericArgs {
		args[i] = a.String()
	}
	return fmt.Sprintf("%s[%s]", base, strings.Join(args, ", "))
}

// RefType 表示不可变或可变借用类型: &T 或 &mut T
type RefType struct {
	IsMut bool
	Elem  TypeNode
	Span  diagnostic.Span
}

func (r *RefType) GetSpan() diagnostic.Span { return r.Span }
func (r *RefType) typeNode()                {}
func (r *RefType) String() string {
	if r.IsMut {
		return "&mut " + r.Elem.String()
	}
	return "&" + r.Elem.String()
}

// RawPtrType 表示非托管原生指针类型: *const T 或 *mut T
type RawPtrType struct {
	IsMut bool
	Elem  TypeNode
	Span  diagnostic.Span
}

func (r *RawPtrType) GetSpan() diagnostic.Span { return r.Span }
func (r *RawPtrType) typeNode()                {}
func (r *RawPtrType) String() string {
	if r.IsMut {
		return "*mut " + r.Elem.String()
	}
	return "*const " + r.Elem.String()
}

// ArrayType 表示定长数组类型: [T; N]
type ArrayType struct {
	Elem TypeNode
	Size string
	Span diagnostic.Span
}

func (a *ArrayType) GetSpan() diagnostic.Span { return a.Span }
func (a *ArrayType) typeNode()                {}
func (a *ArrayType) String() string {
	return fmt.Sprintf("[%s; %s]", a.Elem.String(), a.Size)
}

// SliceType 表示动态切片类型: [T]
type SliceType struct {
	Elem TypeNode
	Span diagnostic.Span
}

func (s *SliceType) GetSpan() diagnostic.Span { return s.Span }
func (s *SliceType) typeNode()                {}
func (s *SliceType) String() string {
	return fmt.Sprintf("[%s]", s.Elem.String())
}

// TupleType 表示元组类型: (T1, T2)
type TupleType struct {
	Elems []TypeNode
	Span  diagnostic.Span
}

func (t *TupleType) GetSpan() diagnostic.Span { return t.Span }
func (t *TupleType) typeNode()                {}
func (t *TupleType) String() string {
	parts := make([]string, len(t.Elems))
	for i, e := range t.Elems {
		parts[i] = e.String()
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// FnType 表示函数签名类型: fn(T1, T2) -> Ret
type FnType struct {
	Params     []TypeNode
	ReturnType TypeNode
	Span       diagnostic.Span
}

func (f *FnType) GetSpan() diagnostic.Span { return f.Span }
func (f *FnType) typeNode()                {}
func (f *FnType) String() string {
	parts := make([]string, len(f.Params))
	for i, p := range f.Params {
		parts[i] = p.String()
	}
	if f.ReturnType != nil {
		return fmt.Sprintf("fn(%s) -> %s", strings.Join(parts, ", "), f.ReturnType.String())
	}
	return fmt.Sprintf("fn(%s)", strings.Join(parts, ", "))
}

// --- 语句与代码块节点 ---

// BlockStmt 表示缩进控制流代码块
type BlockStmt struct {
	Statements []Stmt
	Span       diagnostic.Span
}

func (b *BlockStmt) GetSpan() diagnostic.Span { return b.Span }
func (b *BlockStmt) stmtNode()                {}

// LetStmt 表示规范变量声明: let pat [: Type] = init 或 var pat [: Type] = init
type LetStmt struct {
	IsMut        bool
	Keyword      string          // "let" 或 "var"
	Name         string          // 简单标识符模式下的变量名（兼容旧 checker/codegen）
	Pattern      Pattern         // 完整解构模式
	Type         string          // 类型字符串（兼容旧 checker/codegen）
	TypeAST      TypeNode        // 结构化类型节点
	Init         Expr            // 初始值表达式
	LetTokenSpan diagnostic.Span // let 关键字物理位置（供修复生成）
	Span         diagnostic.Span
}

func (l *LetStmt) GetSpan() diagnostic.Span { return l.Span }
func (l *LetStmt) stmtNode()                {}

// AssignStmt 表示赋值与复合赋值语句: lhs (= | += | -= | ...) value
type AssignStmt struct {
	Target     string // 简单标识符场景下的名称
	TargetExpr Expr   // 完整可赋值左值表达式（支持字段/索引/解引用）
	Op         string // "=" | "+=" | "-=" 等
	Value      Expr
	Span       diagnostic.Span
}

func (a *AssignStmt) GetSpan() diagnostic.Span { return a.Span }
func (a *AssignStmt) stmtNode()                {}

// IfStmt 表示条件分支语句或表达式: if cond: Block {elif ...} [else: Block]
type IfStmt struct {
	Cond Expr
	Then *BlockStmt
	Else Stmt // 可为 *BlockStmt 或 *IfStmt
	Span diagnostic.Span
}

func (i *IfStmt) GetSpan() diagnostic.Span { return i.Span }
func (i *IfStmt) stmtNode()                {}
func (i *IfStmt) exprNode()                {}

// WhileStmt 表示循环语句: while cond: Block
type WhileStmt struct {
	Cond Expr
	Body *BlockStmt
	Span diagnostic.Span
}

func (w *WhileStmt) GetSpan() diagnostic.Span { return w.Span }
func (w *WhileStmt) stmtNode()                {}
func (w *WhileStmt) exprNode()                {}

// ForStmt 表示迭代循环语句: for pattern in iter: Block
type ForStmt struct {
	Pattern Pattern
	Iter    Expr
	Body    *BlockStmt
	Span    diagnostic.Span
}

func (f *ForStmt) GetSpan() diagnostic.Span { return f.Span }
func (f *ForStmt) stmtNode()                {}
func (f *ForStmt) exprNode()                {}

// ReturnStmt 表示返回语句
type ReturnStmt struct {
	Value Expr // 可为空
	Span  diagnostic.Span
}

func (r *ReturnStmt) GetSpan() diagnostic.Span { return r.Span }
func (r *ReturnStmt) stmtNode()                {}

// BreakStmt 表示跳出循环语句
type BreakStmt struct {
	Value Expr // 可为空
	Span  diagnostic.Span
}

func (b *BreakStmt) GetSpan() diagnostic.Span { return b.Span }
func (b *BreakStmt) stmtNode()                {}

// ContinueStmt 表示继续下一轮循环语句
type ContinueStmt struct {
	Span diagnostic.Span
}

func (c *ContinueStmt) GetSpan() diagnostic.Span { return c.Span }
func (c *ContinueStmt) stmtNode()                {}

// ExprStmt 表示表达式语句（或代码块的隐式末尾返回值表达式）
type ExprStmt struct {
	Expr Expr
	Span diagnostic.Span
}

func (e *ExprStmt) GetSpan() diagnostic.Span { return e.Span }
func (e *ExprStmt) stmtNode()                {}

// --- 表达式节点 ---

// IntegerLiteral 表示整型字面量
type IntegerLiteral struct {
	Value  int64
	Raw    string
	Suffix string
	Span   diagnostic.Span
}

func (i *IntegerLiteral) GetSpan() diagnostic.Span { return i.Span }
func (i *IntegerLiteral) exprNode()                {}

// FloatLiteral 表示浮点字面量
type FloatLiteral struct {
	Value  float64
	Raw    string
	Suffix string
	Span   diagnostic.Span
}

func (f *FloatLiteral) GetSpan() diagnostic.Span { return f.Span }
func (f *FloatLiteral) exprNode()                {}

// BoolLiteral 表示布尔字面量 true/false
type BoolLiteral struct {
	Value bool
	Span  diagnostic.Span
}

func (b *BoolLiteral) GetSpan() diagnostic.Span { return b.Span }
func (b *BoolLiteral) exprNode()                {}

// CharLiteral 表示字符字面量 'a'
type CharLiteral struct {
	Value rune
	Raw   string
	Span  diagnostic.Span
}

func (c *CharLiteral) GetSpan() diagnostic.Span { return c.Span }
func (c *CharLiteral) exprNode()                {}

// ByteLiteral 表示单字节字面量 b'a'
type ByteLiteral struct {
	Value byte
	Raw   string
	Span  diagnostic.Span
}

func (b *ByteLiteral) GetSpan() diagnostic.Span { return b.Span }
func (b *ByteLiteral) exprNode()                {}

// StringLiteral 表示普通或原始字符串字面量
type StringLiteral struct {
	Value string
	IsRaw bool
	Span  diagnostic.Span
}

func (s *StringLiteral) GetSpan() diagnostic.Span { return s.Span }
func (s *StringLiteral) exprNode()                {}

// InterpPart 表示字符串插值的单个片段（纯文本或嵌入式表达式）
type InterpPart struct {
	IsExpr     bool
	Literal    string
	Expr       Expr
	FormatSpec string
}

// InterpolatedStringExpr 表示包含 {expr} 的插值字符串表达式
type InterpolatedStringExpr struct {
	Parts []InterpPart
	Raw   string
	Span  diagnostic.Span
}

func (i *InterpolatedStringExpr) GetSpan() diagnostic.Span { return i.Span }
func (i *InterpolatedStringExpr) exprNode()                {}

// ArrayRepeatLiteral 表示重复填充的数组字面量，如 [0; 256] 或 [0u8; 1024]
type ArrayRepeatLiteral struct {
	Elem      Expr
	Count     int64
	CountExpr Expr
	Span      diagnostic.Span
}

func (a *ArrayRepeatLiteral) GetSpan() diagnostic.Span { return a.Span }
func (a *ArrayRepeatLiteral) exprNode()                {}

// ArrayLiteral 表示枚举元素数组字面量: [1, 2, 3]
type ArrayLiteral struct {
	Elems []Expr
	Span  diagnostic.Span
}

func (a *ArrayLiteral) GetSpan() diagnostic.Span { return a.Span }
func (a *ArrayLiteral) exprNode()                {}

// TupleLiteral 表示元组字面量: (a, b) 或 ()
type TupleLiteral struct {
	Elems []Expr
	Span  diagnostic.Span
}

func (t *TupleLiteral) GetSpan() diagnostic.Span { return t.Span }
func (t *TupleLiteral) exprNode()                {}

// FieldInit 表示结构体字面量的单项字段初始化
type FieldInit struct {
	Name  string
	Value Expr // 若为缩写语法 Point { x }，则可为 nil
	Span  diagnostic.Span
}

// StructLiteral 表示结构体构造字面量: Point { x: 1.0, y: 2.0 }
type StructLiteral struct {
	TypePath []string
	Fields   []*FieldInit
	Span     diagnostic.Span
}

func (s *StructLiteral) GetSpan() diagnostic.Span { return s.Span }
func (s *StructLiteral) exprNode()                {}

// Identifier 表示简单标识符表达式
type Identifier struct {
	Name string
	Span diagnostic.Span
}

func (i *Identifier) GetSpan() diagnostic.Span { return i.Span }
func (i *Identifier) exprNode()                {}

// PathExpr 表示多段命名空间路径表达式，如 Result::Ok, Option::None, std::env::args
type PathExpr struct {
	Segments    []string
	GenericArgs []TypeNode
	Span        diagnostic.Span
}

func (p *PathExpr) GetSpan() diagnostic.Span { return p.Span }
func (p *PathExpr) exprNode()                {}

// BinaryExpr 表示二元算术、位运算或逻辑比较表达式
type BinaryExpr struct {
	Left  Expr
	Op    string
	Right Expr
	Span  diagnostic.Span
}

func (b *BinaryExpr) GetSpan() diagnostic.Span { return b.Span }
func (b *BinaryExpr) exprNode()                {}

// RangeExpr 表示范围表达式: a..b, a..=b, ..b, a..
type RangeExpr struct {
	Start     Expr
	End       Expr
	Inclusive bool
	Span      diagnostic.Span
}

func (r *RangeExpr) GetSpan() diagnostic.Span { return r.Span }
func (r *RangeExpr) exprNode()                {}

// CastExpr 表示显式类型转换: expr as Type
type CastExpr struct {
	Expr       Expr
	TargetType TypeNode
	Span       diagnostic.Span
}

func (c *CastExpr) GetSpan() diagnostic.Span { return c.Span }
func (c *CastExpr) exprNode()                {}

// UnaryExpr 表示一元运算符表达式 (-, not, !, ~, *)
type UnaryExpr struct {
	Op   string
	Expr Expr
	Span diagnostic.Span
}

func (u *UnaryExpr) GetSpan() diagnostic.Span { return u.Span }
func (u *UnaryExpr) exprNode()                {}

// BorrowExpr 表示借用 / 取地址表达式: &x 或 &mut x
type BorrowExpr struct {
	IsMut bool
	Expr  Expr
	Span  diagnostic.Span
}

func (b *BorrowExpr) GetSpan() diagnostic.Span { return b.Span }
func (b *BorrowExpr) exprNode()                {}

// TryExpr 表示错误传播后缀表达式: expr?
type TryExpr struct {
	Expr Expr
	Span diagnostic.Span
}

func (t *TryExpr) GetSpan() diagnostic.Span { return t.Span }
func (t *TryExpr) exprNode()                {}

// CallExpr 表示常规或泛化的函数调用
type CallExpr struct {
	Callee      string
	CalleeExpr  Expr
	GenericArgs []TypeNode
	Args        []Expr
	Span        diagnostic.Span
}

func (c *CallExpr) GetSpan() diagnostic.Span { return c.Span }
func (c *CallExpr) exprNode()                {}

// NamespacedCallExpr 表示两段命名空间函数调用，如 zlib::compress(...)
type NamespacedCallExpr struct {
	Namespace string
	Function  string
	Args      []Expr
	Span      diagnostic.Span
}

func (n *NamespacedCallExpr) GetSpan() diagnostic.Span { return n.Span }
func (n *NamespacedCallExpr) exprNode()                {}

// MethodCallExpr 表示方法调用: receiver.method[T](args...)
type MethodCallExpr struct {
	Receiver    Expr
	Method      string
	GenericArgs []TypeNode
	Args        []Expr
	Span        diagnostic.Span
}

func (m *MethodCallExpr) GetSpan() diagnostic.Span { return m.Span }
func (m *MethodCallExpr) exprNode()                {}

// FieldAccessExpr 表示结构体成员或元组字段访问: obj.field
type FieldAccessExpr struct {
	Object Expr
	Field  string
	Span   diagnostic.Span
}

func (f *FieldAccessExpr) GetSpan() diagnostic.Span { return f.Span }
func (f *FieldAccessExpr) exprNode()                {}

// IndexExpr 表示数组或切片下标访问: arr[index]
type IndexExpr struct {
	Object Expr
	Index  Expr
	Span   diagnostic.Span
}

func (i *IndexExpr) GetSpan() diagnostic.Span { return i.Span }
func (i *IndexExpr) exprNode()                {}

// UnsafeExpr 表示显式不安全缩进代码块: unsafe: Block
type UnsafeExpr struct {
	Block *BlockStmt
	Span  diagnostic.Span
}

func (u *UnsafeExpr) GetSpan() diagnostic.Span { return u.Span }
func (u *UnsafeExpr) exprNode()                {}

// MatchArm 表示模式匹配的单个分支
type MatchArm struct {
	Pattern Pattern
	Guard   Expr
	Body    *BlockStmt
	Span    diagnostic.Span
}

// MatchExpr 表示模式匹配表达式/语句: match subject: ...
type MatchExpr struct {
	Subject Expr
	Arms    []*MatchArm
	Span    diagnostic.Span
}

func (m *MatchExpr) GetSpan() diagnostic.Span { return m.Span }
func (m *MatchExpr) exprNode()                {}
func (m *MatchExpr) stmtNode()                {}

// ScopeExpr 表示结构化并发作用域: scope s: Block
type ScopeExpr struct {
	Name string
	Body *BlockStmt
	Span diagnostic.Span
}

func (s *ScopeExpr) GetSpan() diagnostic.Span { return s.Span }
func (s *ScopeExpr) exprNode()                {}
func (s *ScopeExpr) stmtNode()                {}

// SpawnExpr 表示在作用域内派生的并发任务: s.spawn: Block 或 spawn(expr)
type SpawnExpr struct {
	Nursery string
	Body    *BlockStmt
	Arg     Expr
	Span    diagnostic.Span
}

func (s *SpawnExpr) GetSpan() diagnostic.Span { return s.Span }
func (s *SpawnExpr) exprNode()                {}
func (s *SpawnExpr) stmtNode()                {}

// --- 模式节点 ---

// WildcardPattern 表示通配符模式 _
type WildcardPattern struct {
	Span diagnostic.Span
}

func (w *WildcardPattern) GetSpan() diagnostic.Span { return w.Span }
func (w *WildcardPattern) patternNode()             {}

// IdentPattern 表示变量绑定模式: [ref] [mut] ident
type IdentPattern struct {
	IsRef bool
	IsMut bool
	Name  string
	Span  diagnostic.Span
}

func (i *IdentPattern) GetSpan() diagnostic.Span { return i.Span }
func (i *IdentPattern) patternNode()             {}

// LiteralPattern 表示字面量匹配模式
type LiteralPattern struct {
	Literal Expr
	Span    diagnostic.Span
}

func (l *LiteralPattern) GetSpan() diagnostic.Span { return l.Span }
func (l *LiteralPattern) patternNode()             {}

// TuplePattern 表示元组解构模式: (a, b)
type TuplePattern struct {
	Elems []Pattern
	Span  diagnostic.Span
}

func (t *TuplePattern) GetSpan() diagnostic.Span { return t.Span }
func (t *TuplePattern) patternNode()             {}

// FieldPattern 表示结构体解构字段模式
type FieldPattern struct {
	Name    string
	Pattern Pattern // 若为简写如 { x, y }，可为 nil
	Span    diagnostic.Span
}

// StructPattern 表示结构体解构模式: Shape::Rectangle { width, height }
type StructPattern struct {
	Path   []string
	Fields []*FieldPattern
	Span   diagnostic.Span
}

func (s *StructPattern) GetSpan() diagnostic.Span { return s.Span }
func (s *StructPattern) patternNode()             {}

// EnumPattern 表示枚举变体解构模式: Shape::Circle(radius)
type EnumPattern struct {
	Path []string
	Args []Pattern
	Span diagnostic.Span
}

func (e *EnumPattern) GetSpan() diagnostic.Span { return e.Span }
func (e *EnumPattern) patternNode()             {}

// SlicePattern 表示切片解构模式: [head, tail]
type SlicePattern struct {
	Elems []Pattern
	Span  diagnostic.Span
}

func (s *SlicePattern) GetSpan() diagnostic.Span { return s.Span }
func (s *SlicePattern) patternNode()             {}

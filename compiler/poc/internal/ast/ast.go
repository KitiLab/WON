// Package ast 定义了 WON Phase 0B 编译器的抽象语法树节点模型
package ast

import (
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
)

// Node 表示抽象语法树的基础节点接口
type Node interface {
	GetSpan() diagnostic.Span
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

// Program 是源文件的顶层根节点
type Program struct {
	Imports   []*ImportDecl
	Functions []*FunctionDecl
	Span      diagnostic.Span
}

func (p *Program) GetSpan() diagnostic.Span { return p.Span }

// ImportDecl 表示头文件引入声明，如 #import <zlib.h>
type ImportDecl struct {
	HeaderPath string // 例如 "<zlib.h>" 或 "\"myheader.h\""
	Span       diagnostic.Span
}

func (i *ImportDecl) GetSpan() diagnostic.Span { return i.Span }

// Param 表示函数形参
type Param struct {
	Name string
	Type string
	Span diagnostic.Span
}

func (p *Param) GetSpan() diagnostic.Span { return p.Span }

// FunctionDecl 表示函数定义
type FunctionDecl struct {
	Name       string
	Params     []*Param
	ReturnType string
	Body       *BlockStmt
	Span       diagnostic.Span
}

func (f *FunctionDecl) GetSpan() diagnostic.Span { return f.Span }

// BlockStmt 表示由花括号包围的代码块
type BlockStmt struct {
	Statements []Stmt
	Span       diagnostic.Span
}

func (b *BlockStmt) GetSpan() diagnostic.Span { return b.Span }
func (b *BlockStmt) stmtNode()                {}

// LetStmt 表示变量绑定语句: let [mut] name [: Type] = init;
type LetStmt struct {
	IsMut        bool
	Name         string
	Type         string          // 可选类型标注，可为空
	Init         Expr            // 初始值表达式
	LetTokenSpan diagnostic.Span // "let" 关键字的物理位置（用于修复生成）
	Span         diagnostic.Span
}

func (l *LetStmt) GetSpan() diagnostic.Span { return l.Span }
func (l *LetStmt) stmtNode()                {}

// AssignStmt 表示变量赋值语句: name = value;
type AssignStmt struct {
	Target string
	Value  Expr
	Span   diagnostic.Span
}

func (a *AssignStmt) GetSpan() diagnostic.Span { return a.Span }
func (a *AssignStmt) stmtNode()                {}

// IfStmt 表示条件分支语句
type IfStmt struct {
	Cond Expr
	Then *BlockStmt
	Else Stmt // 可为 *BlockStmt 或 *IfStmt
	Span diagnostic.Span
}

func (i *IfStmt) GetSpan() diagnostic.Span { return i.Span }
func (i *IfStmt) stmtNode()                {}

// WhileStmt 表示循环语句
type WhileStmt struct {
	Cond Expr
	Body *BlockStmt
	Span diagnostic.Span
}

func (w *WhileStmt) GetSpan() diagnostic.Span { return w.Span }
func (w *WhileStmt) stmtNode()                {}

// ReturnStmt 表示返回语句
type ReturnStmt struct {
	Value Expr // 可为空
	Span  diagnostic.Span
}

func (r *ReturnStmt) GetSpan() diagnostic.Span { return r.Span }
func (r *ReturnStmt) stmtNode()                {}

// ExprStmt 表示表达式语句
type ExprStmt struct {
	Expr Expr
	Span diagnostic.Span
}

func (e *ExprStmt) GetSpan() diagnostic.Span { return e.Span }
func (e *ExprStmt) stmtNode()                {}

// --- 表达式节点 ---

// IntegerLiteral 表示整型字面量
type IntegerLiteral struct {
	Value int64
	Raw   string
	Span  diagnostic.Span
}

func (i *IntegerLiteral) GetSpan() diagnostic.Span { return i.Span }
func (i *IntegerLiteral) exprNode()                {}

// StringLiteral 表示字符串字面量
type StringLiteral struct {
	Value string
	Span  diagnostic.Span
}

func (s *StringLiteral) GetSpan() diagnostic.Span { return s.Span }
func (s *StringLiteral) exprNode()                {}

// ArrayRepeatLiteral 表示重复填充的数组字面量，如 [0; 256]
type ArrayRepeatLiteral struct {
	Elem  Expr
	Count int64
	Span  diagnostic.Span
}

func (a *ArrayRepeatLiteral) GetSpan() diagnostic.Span { return a.Span }
func (a *ArrayRepeatLiteral) exprNode()                {}

// Identifier 表示标识符表达式
type Identifier struct {
	Name string
	Span diagnostic.Span
}

func (i *Identifier) GetSpan() diagnostic.Span { return i.Span }
func (i *Identifier) exprNode()                {}

// BinaryExpr 表示二元算术或逻辑比较表达式
type BinaryExpr struct {
	Left  Expr
	Op    string
	Right Expr
	Span  diagnostic.Span
}

func (b *BinaryExpr) GetSpan() diagnostic.Span { return b.Span }
func (b *BinaryExpr) exprNode()                {}

// UnaryExpr 表示一元取反或负号表达式
type UnaryExpr struct {
	Op   string
	Expr Expr
	Span diagnostic.Span
}

func (u *UnaryExpr) GetSpan() diagnostic.Span { return u.Span }
func (u *UnaryExpr) exprNode()                {}

// BorrowExpr 表示借用 / 取地址表达式，如 &x 或 &mut x
type BorrowExpr struct {
	IsMut bool
	Expr  Expr
	Span  diagnostic.Span
}

func (b *BorrowExpr) GetSpan() diagnostic.Span { return b.Span }
func (b *BorrowExpr) exprNode()                {}

// CallExpr 表示常规函数调用
type CallExpr struct {
	Callee string
	Args   []Expr
	Span   diagnostic.Span
}

func (c *CallExpr) GetSpan() diagnostic.Span { return c.Span }
func (c *CallExpr) exprNode()                {}

// NamespacedCallExpr 表示跨命名空间调用，如 zlib::compress(...)
type NamespacedCallExpr struct {
	Namespace string
	Function  string
	Args      []Expr
	Span      diagnostic.Span
}

func (n *NamespacedCallExpr) GetSpan() diagnostic.Span { return n.Span }
func (n *NamespacedCallExpr) exprNode()                {}

// MethodCallExpr 表示内置方法调用，如 message.as_bytes() 或 buf.as_mut_ptr()
type MethodCallExpr struct {
	Receiver Expr
	Method   string
	Args     []Expr
	Span     diagnostic.Span
}

func (m *MethodCallExpr) GetSpan() diagnostic.Span { return m.Span }
func (m *MethodCallExpr) exprNode()                {}

// UnsafeExpr 表示显式不安全代码块表达式: unsafe { ... }
type UnsafeExpr struct {
	Block *BlockStmt
	Span  diagnostic.Span
}

func (u *UnsafeExpr) GetSpan() diagnostic.Span { return u.Span }
func (u *UnsafeExpr) exprNode()                {}

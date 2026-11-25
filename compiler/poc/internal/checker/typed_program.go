// Package checker 实现了 WON 编译器的静态语义分析引擎
// 包含两阶段声明收集、层次化符号解析、规范类型系统驻留、双向类型推导、结构体/枚举语义、模式穷尽性校验以及 Phase 0B FFI 安全契约验证。
package checker

import (
	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// TypedProgram 表示语义分析通过后生成的类型化程序模型
// 该抽象与具体后端（C11/LLVM/QBE）解耦，作为后续降级为控制流图 TIR (ADR-009) 的正式前端输入边界。
type TypedProgram struct {
	AST         *ast.Program
	Types       *types.Context
	Symbols     *symbols.Table
	ExprTypes   map[ast.Expr]types.TypeID
	CallTargets map[ast.Expr]*symbols.Symbol
	FieldRefs   map[ast.Node]*symbols.FieldInfo
	VariantRefs map[ast.Node]*symbols.VariantInfo

	// CodegenSupported 标记该程序是否仅使用当前 Phase 0B/1B ISO C11 代码生成器已支持落地的语言子集。
	// 当包含 Struct/Enum/Match/? 等已完成 Phase 2A 语义校验但尚未实现 C11 代码生成的结构时，
	// 编译器必须在 wonc build 阶段显式拒止而非静默生成错误 C 代码。
	CodegenSupported   bool
	UnsupportedReason  string
	UnsupportedCodegen diagnostic.Span
}

// NewTypedProgram 创建初始类型化语义结果容器
func NewTypedProgram(prog *ast.Program, tctx *types.Context, symTab *symbols.Table) *TypedProgram {
	return &TypedProgram{
		AST:              prog,
		Types:            tctx,
		Symbols:          symTab,
		ExprTypes:        make(map[ast.Expr]types.TypeID, 128),
		CallTargets:      make(map[ast.Expr]*symbols.Symbol, 64),
		FieldRefs:        make(map[ast.Node]*symbols.FieldInfo, 64),
		VariantRefs:      make(map[ast.Node]*symbols.VariantInfo, 32),
		CodegenSupported: true,
	}
}

func (tp *TypedProgram) markCodegenUnsupported(reason string, span diagnostic.Span) {
	if tp.CodegenSupported {
		tp.CodegenSupported = false
		tp.UnsupportedReason = reason
		tp.UnsupportedCodegen = span
	}
}

package borrow

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

type whiteboxView struct {
	types *types.Context
	syms  *symbols.Table
}

func (w *whiteboxView) GetTypes() *types.Context            { return w.types }
func (w *whiteboxView) GetSymbols() *symbols.Table          { return w.syms }
func (w *whiteboxView) ExprType(ast.Expr) types.TypeID      { return types.Invalid }
func (w *whiteboxView) CallTarget(ast.Expr) *symbols.Symbol { return nil }
func (w *whiteboxView) FieldRef(ast.Node) *symbols.FieldInfo {
	return nil
}
func (w *whiteboxView) VariantRef(ast.Node) *symbols.VariantInfo {
	return nil
}

// TestFixedPointNonConvergenceDiagnostic 验证不动点达到迭代预算上限未收敛时发出 WON-INT-001（而非 WON-MEM-001）。
func TestFixedPointNonConvergenceDiagnostic(t *testing.T) {
	tctx := types.NewContext()
	symTab := symbols.NewTable(tctx)
	sp := diagnostic.Span{File: "nonconv.won", Start: diagnostic.Position{Line: 1, Column: 1, Byte: 0}, End: diagnostic.Position{Line: 1, Column: 8, Byte: 7}}
	v := symTab.AllocSymbol(symbols.SymVar, "v", sp)
	v.Type = tctx.StringT

	fn := &cfg.FuncCFG{
		Name:   "non_converging",
		Decl:   &ast.FunctionDecl{Name: "non_converging", Span: sp},
		Entry:  0,
		Locals: map[symbols.SymbolID]*cfg.LocalMeta{v.ID: {Sym: v}},
		Blocks: []*cfg.BasicBlock{
			{
				ID:    0,
				Label: "entry",
				Statements: []cfg.Statement{
					{Kind: cfg.OpInitAssign, Place: cfg.Place{RootSymID: v.ID, RootName: "v", ElemType: tctx.StringT}, Type: tctx.StringT, Span: sp},
				},
				Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp},
			},
		},
	}

	rep := diagnostic.NewReport()
	// 人为设定迭代预算为 0，必在首轮触发超限保护
	analyzeFunctionWithBudget(fn, &whiteboxView{types: tctx, syms: symTab}, rep, 0)
	if !rep.HasErrors() {
		t.Fatal("超限不动点预期发出错误，但实际未报错")
	}
	hasInt := false
	for _, d := range rep.Diagnostics {
		if d.Code == "WON-MEM-001" {
			t.Fatalf("不动点未收敛绝不能伪装为 WON-MEM-001，实际诊断:\n%s", rep.String())
		}
		if d.Code == "WON-INT-001" {
			hasInt = true
		}
	}
	if !hasInt {
		t.Fatalf("不动点未收敛期望发出 WON-INT-001，实际:\n%s", rep.String())
	}
}

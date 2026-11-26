package borrow_test

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/borrow"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

type stubSemanticView struct {
	types *types.Context
	syms  *symbols.Table
}

func (s *stubSemanticView) GetTypes() *types.Context            { return s.types }
func (s *stubSemanticView) GetSymbols() *symbols.Table          { return s.syms }
func (s *stubSemanticView) ExprType(ast.Expr) types.TypeID      { return types.Invalid }
func (s *stubSemanticView) CallTarget(ast.Expr) *symbols.Symbol { return nil }
func (s *stubSemanticView) FieldRef(ast.Node) *symbols.FieldInfo {
	return nil
}
func (s *stubSemanticView) VariantRef(ast.Node) *symbols.VariantInfo {
	return nil
}

func assertHasInternalDiag(t *testing.T, name string, rep *diagnostic.Report) {
	t.Helper()
	if !rep.HasErrors() {
		t.Fatalf("%s 预期触发 WON-INT-001 Fail-Closed 错误，但未报错", name)
	}
	for _, d := range rep.Diagnostics {
		if d.Code == "WON-INT-001" {
			return
		}
	}
	t.Fatalf("%s 期望诊断码 WON-INT-001，实际:\n%s", name, rep.String())
}

// TestFailClosedOwnershipInvariants 验证任何缺失状态、非法符号或未知投影/操作均严格发出 WON-INT-001 拒绝认证。
func TestFailClosedOwnershipInvariants(t *testing.T) {
	tctx := types.NewContext()
	symTab := symbols.NewTable(tctx)
	sp := diagnostic.Span{
		File:  "synthetic.won",
		Start: diagnostic.Position{Line: 10, Column: 5, Byte: 100},
		End:   diagnostic.Position{Line: 10, Column: 12, Byte: 107},
	}

	validVar := symTab.AllocSymbol(symbols.SymVar, "x", sp)
	validVar.Type = tctx.StringT

	// 创建带单个字段 Struct 类型以测试部分读取与无效投影分支
	structSym := symTab.AllocSymbol(symbols.SymStruct, "Box", sp)
	structSym.Fields = []symbols.FieldInfo{{Name: "item", Type: tctx.StringT}}
	boxType := tctx.Struct(uint64(structSym.ID), "Box", nil)
	boxVar := symTab.AllocSymbol(symbols.SymVar, "b", sp)
	boxVar.Type = boxType

	view := &stubSemanticView{types: tctx, syms: symTab}

	runSynthetic := func(name string, stmts []cfg.Statement) {
		t.Run(name, func(t *testing.T) {
			rep := diagnostic.NewReport()
			progCFG := &cfg.ProgramCFG{
				Functions: []*cfg.FuncCFG{
					{
						Name:   name,
						Entry:  0,
						Locals: map[symbols.SymbolID]*cfg.LocalMeta{},
						Blocks: []*cfg.BasicBlock{
							{
								ID:         0,
								Label:      "entry",
								Statements: stmts,
								Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp},
							},
						},
					},
				},
			}
			borrow.AnalyzeProgram(progCFG, view, rep)
			assertHasInternalDiag(t, name, rep)
		})
	}

	// 1. 无前置声明的 OpRead
	runSynthetic("ReadWithoutDecl", []cfg.Statement{
		{Kind: cfg.OpRead, Place: cfg.Place{RootSymID: validVar.ID, RootName: "x", ElemType: tctx.StringT}, Type: tctx.StringT, Span: sp},
	})

	// 2. 无根状态的 OpMove
	runSynthetic("MoveWithoutRootState", []cfg.Statement{
		{Kind: cfg.OpMove, Place: cfg.Place{RootSymID: validVar.ID, RootName: "x", ElemType: tctx.StringT}, Type: tctx.StringT, Span: sp},
	})

	// 3. 无根状态的 OpAssign（不得再静默分配 Init 状态掩盖内部缺失）
	runSynthetic("AssignWithoutRootState", []cfg.Statement{
		{Kind: cfg.OpAssign, Place: cfg.Place{RootSymID: validVar.ID, RootName: "x", ElemType: tctx.StringT}, Type: tctx.StringT, Span: sp},
	})

	// 4. 在未注册根绑定上读取结构体字段投影
	runSynthetic("ProjectedReadOnUnregisteredRoot", []cfg.Statement{
		{
			Kind: cfg.OpRead,
			Place: cfg.Place{
				RootSymID:   boxVar.ID,
				RootName:    "b",
				ElemType:    tctx.StringT,
				Projections: []cfg.Projection{{Kind: cfg.ProjField, FieldName: "item", ElemType: tctx.StringT}},
			},
			Type: tctx.StringT,
			Span: sp,
		},
	})

	// 5. 零值或未知 SymbolID
	runSynthetic("ZeroSymbolID", []cfg.Statement{
		{Kind: cfg.OpRead, Place: cfg.Place{RootSymID: 0, RootName: "ghost", ElemType: tctx.I32}, Type: tctx.I32, Span: sp},
	})
	runSynthetic("UnknownSymbolID", []cfg.Statement{
		{Kind: cfg.OpInitAssign, Place: cfg.Place{RootSymID: 99999, RootName: "dangling", ElemType: tctx.I32}, Type: tctx.I32, Span: sp},
	})

	// 6. 已注册符号但访问不存在字段名或尚未支持的元组/枚举投影
	runSynthetic("UnresolvableFieldProjection", []cfg.Statement{
		{Kind: cfg.OpInitAssign, Place: cfg.Place{RootSymID: boxVar.ID, RootName: "b", ElemType: boxType}, Type: boxType, Span: sp},
		{
			Kind: cfg.OpRead,
			Place: cfg.Place{
				RootSymID:   boxVar.ID,
				RootName:    "b",
				ElemType:    tctx.StringT,
				Projections: []cfg.Projection{{Kind: cfg.ProjField, FieldName: "nonexistent", ElemType: tctx.StringT}},
			},
			Type: tctx.StringT,
			Span: sp,
		},
	})
	runSynthetic("UnsupportedTupleProjection", []cfg.Statement{
		{Kind: cfg.OpInitAssign, Place: cfg.Place{RootSymID: boxVar.ID, RootName: "b", ElemType: boxType}, Type: boxType, Span: sp},
		{
			Kind: cfg.OpRead,
			Place: cfg.Place{
				RootSymID:   boxVar.ID,
				RootName:    "b",
				ElemType:    tctx.StringT,
				Projections: []cfg.Projection{{Kind: cfg.ProjTupleField, FieldIndex: 0, ElemType: tctx.StringT}},
			},
			Type: tctx.StringT,
			Span: sp,
		},
	})

	// 7. 未知/未支持的 CFG OpKind 默认守卫
	runSynthetic("UnknownOpKind", []cfg.Statement{
		{Kind: cfg.OpKind(255), Place: cfg.Place{RootSymID: validVar.ID, RootName: "x"}, Span: sp},
	})
}

// TestUnreachableBlockExclusion 验证不可达块内的内部缺陷状态不会污染后续或入口分析，且可控合流图必然有限收敛。
func TestUnreachableBlockExclusionAndConvergence(t *testing.T) {
	tctx := types.NewContext()
	symTab := symbols.NewTable(tctx)
	sp := diagnostic.Span{
		File:  "reach.won",
		Start: diagnostic.Position{Line: 3, Column: 1, Byte: 20},
		End:   diagnostic.Position{Line: 3, Column: 8, Byte: 27},
	}
	v := symTab.AllocSymbol(symbols.SymVar, "v", sp)
	v.Type = tctx.StringT
	view := &stubSemanticView{types: tctx, syms: symTab}

	rep := diagnostic.NewReport()
	progCFG := &cfg.ProgramCFG{
		Functions: []*cfg.FuncCFG{
			{
				Name:   "dead_tail_excluded",
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
					{
						ID:    1,
						Label: "unreachable.corrupt",
						Statements: []cfg.Statement{
							{Kind: cfg.OpRead, Place: cfg.Place{RootSymID: 88888, RootName: "dead_ghost"}, Span: sp},
						},
						Terminator: cfg.Terminator{Kind: cfg.TermUnreachable, Span: sp},
					},
				},
			},
		},
	}

	borrow.AnalyzeProgram(progCFG, view, rep)
	if rep.HasErrors() {
		t.Fatalf("不可达基本块不应产生错误诊断:\n%s", rep.String())
	}
}

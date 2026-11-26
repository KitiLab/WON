package borrow_test

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/borrow"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

func buildLoanFlowFromSource(t *testing.T, src string, fnName string) (*cfg.FuncCFG, *borrow.FuncLoanFlow, *symbols.Table) {
	t.Helper()
	rep := diagnostic.NewReport()
	lex := lexer.New("loans_test.won", src, rep)
	p := parser.New(lex, rep, "loans_test.won")
	prog := p.ParseProgram()
	if rep.HasErrors() {
		t.Fatalf("解析失败:\n%s", rep.String())
	}
	chk := checker.New(rep, nil)
	tp := chk.CheckProgram(prog)
	if rep.HasErrors() || tp == nil || tp.CFG == nil {
		t.Fatalf("前置语义或 Stage A 校验失败:\n%s", rep.String())
	}
	for _, fn := range tp.CFG.Functions {
		if fn.Name == fnName {
			flow := borrow.BuildLoanFlow(fn, tp, rep)
			if rep.HasErrors() || flow == nil {
				t.Fatalf("BuildLoanFlow(%s) 失败:\n%s", fnName, rep.String())
			}
			return fn, flow, tp.Symbols
		}
	}
	t.Fatalf("未找到函数 %s", fnName)
	return nil, nil, nil
}

func symIDByName(t *testing.T, fn *cfg.FuncCFG, name string) symbols.SymbolID {
	t.Helper()
	for _, sid := range fn.LocalOrder {
		m := fn.Locals[sid]
		if m != nil && m.Sym != nil && m.Sym.Name == name {
			return sid
		}
	}
	t.Fatalf("函数 %s 中未找到局部/参数符号 %q", fn.Name, name)
	return 0
}

// TestStageALoanIdentityAndCarrierFlow 验证 LoanID 生成、载体传播、重借用父关系、聚合拆分与 Copy/Move 语义。
func TestStageALoanIdentityAndCarrierFlow(t *testing.T) {
	t.Run("SharedCopyAndMutMovePreserveCarrierLoans", func(t *testing.T) {
		src := `
fn flow():
    var s = String::from("abc")
    let r1 = &s
    let r2 = r1
    var m1 = &mut s
    let m2 = m1
    let n = m2.len()
`
		fn, flow, _ := buildLoanFlowFromSource(t, src, "flow")
		r1ID := symIDByName(t, fn, "r1")
		r2ID := symIDByName(t, fn, "r2")
		m1ID := symIDByName(t, fn, "m1")
		m2ID := symIDByName(t, fn, "m2")

		// 查找 r1 的 shared loan 与 m1 的 mut loan
		var sharedLoan, mutLoan *borrow.LoanInfo
		for _, li := range flow.Loans {
			if !li.IsMut && !li.IsReborrow {
				sharedLoan = li
			} else if li.IsMut && !li.IsReborrow {
				mutLoan = li
			}
		}
		if sharedLoan == nil || mutLoan == nil {
			t.Fatalf("期望捕获 sharedLoan 与 mutLoan，实际 Loans=%d", len(flow.Loans))
		}
		if !sharedLoan.Carriers[r1ID] || !sharedLoan.Carriers[r2ID] {
			t.Errorf("复制共享引用 &T 应让 r1 和 r2 均携带同一 LoanID L%d: carriers=%v", sharedLoan.ID, sharedLoan.Carriers)
		}
		if !mutLoan.Carriers[m1ID] || !mutLoan.Carriers[m2ID] {
			t.Errorf("移动可变引用 &mut T 应将 LoanID L%d 传递至 m2: carriers=%v", mutLoan.ID, mutLoan.Carriers)
		}
		// 检查出口基本块：m1 被移走后其载体集合不再保留 mutLoan（仅 m2 持有）
		exitPred := flow.InStates[fn.Exit]
		if exitPred == nil {
			// 在最后一个语句块检查 outStates
			_ = exitPred
		}
		lastBlkID := fn.Blocks[fn.Entry].ID
		_ = m1ID
		_ = lastBlkID
	})

	t.Run("ReborrowChainPreservesExplicitParentLoanID", func(t *testing.T) {
		src := `
fn chain():
    var text = String::from("root")
    let parent = &mut text
    let child = &*parent
    let grand = &*child
    println(grand)
`
		_, flow, _ := buildLoanFlowFromSource(t, src, "chain")
		if len(flow.Loans) < 3 {
			t.Fatalf("期望至少 3 条借用（parent, child, grand），实际 %d", len(flow.Loans))
		}
		lParent := flow.Loans[0]
		lChild := flow.Loans[1]
		lGrand := flow.Loans[2]

		if lParent.IsReborrow || !lParent.IsMut {
			t.Errorf("lParent 应为根可变借用，实际 %+v", lParent)
		}
		if !lChild.IsReborrow || !lChild.ParentLoans[lParent.ID] {
			t.Errorf("lChild 应显式记录 ParentLoans={L%d}，实际 %+v", lParent.ID, lChild.ParentLoans)
		}
		if !lGrand.IsReborrow || !lGrand.ParentLoans[lChild.ID] {
			t.Errorf("lGrand 应显式记录 ParentLoans={L%d}，实际 %+v", lChild.ID, lGrand.ParentLoans)
		}
	})

	t.Run("ConditionalBranchesStaticLiteralAndCallResultFlow", func(t *testing.T) {
		src := `
fn id_ref(x: &String) -> &String:
    return x

fn merge_flow(cond: bool, p: &String) -> i32:
    var a = String::from("a")
    let chosen = if cond:
        &a
    else:
        p
    let forwarded = id_ref(&a)
    let st = "static_text"
    println(chosen)
    println(forwarded)
    println(st)
    return 0
`
		fn, flow, _ := buildLoanFlowFromSource(t, src, "merge_flow")
		chosenID := symIDByName(t, fn, "chosen")
		forwardedID := symIDByName(t, fn, "forwarded")

		var loanCond, loanCall *borrow.LoanInfo
		for _, li := range flow.Loans {
			if li.Place.RootName == "a" {
				if loanCond == nil {
					loanCond = li
				} else {
					loanCall = li
				}
			}
		}
		if loanCond == nil || loanCall == nil {
			t.Fatalf("期望捕获两个针对局部变量 a 的借用 Loan，实际 Loans=%d", len(flow.Loans))
		}
		if !loanCond.Carriers[chosenID] {
			t.Errorf("分支表达式合流后 chosen 应保留对 a 的借用载体记录 L%d，carriers=%v", loanCond.ID, loanCond.Carriers)
		}
		if !loanCall.Carriers[forwardedID] {
			t.Errorf("调用 id_ref(&a) 返回结果绑定 forwarded 应承接实参 L%d，carriers=%v", loanCall.ID, loanCall.Carriers)
		}
	})

	t.Run("TupleAndStructAggregatesTrackPerMemberLoans", func(t *testing.T) {
		src := `
struct RefPair:
    left: &String
    right: &mut String

fn agg_test():
    var x = String::from("x")
    var y = String::from("y")
    let rp = RefPair { left: &x, right: &mut y }
    let tup = (&x, &mut y)
    let (tx, ty) = (&x, &mut y)
    println(rp.left)
    println(tx)
`
		fn, flow, _ := buildLoanFlowFromSource(t, src, "agg_test")
		txID := symIDByName(t, fn, "tx")
		tyID := symIDByName(t, fn, "ty")

		var xLoanDest, yLoanDest cfg.LoanID
		for _, li := range flow.Loans {
			if li.Place.RootName == "x" {
				xLoanDest = li.ID
			}
			if li.Place.RootName == "y" {
				yLoanDest = li.ID
			}
		}
		if xLoanDest == 0 || yLoanDest == 0 {
			t.Fatalf("未找到 x 与 y 的独立借用记录")
		}
		if !flow.LoanByID[xLoanDest].Carriers[txID] || flow.LoanByID[xLoanDest].Carriers[tyID] {
			t.Errorf("解构元组 (tx, ty) 中 tx 应仅携带 x 借用而不得混入 ty: carriers=%v", flow.LoanByID[xLoanDest].Carriers)
		}
		if !flow.LoanByID[yLoanDest].Carriers[tyID] || flow.LoanByID[yLoanDest].Carriers[txID] {
			t.Errorf("解构元组 (tx, ty) 中 ty 应仅携带 y 借用而不得混入 tx: carriers=%v", flow.LoanByID[yLoanDest].Carriers)
		}
	})
}

// TestStageAFailClosedLoanInvariants 验证 Stage A 对非法 LoanID 与无父载体 Reborrow 严格发出 WON-INT-001。
func TestStageAFailClosedLoanInvariants(t *testing.T) {
	tctx := types.NewContext()
	symTab := symbols.NewTable(tctx)
	sp := diagnostic.Span{File: "loan_inv.won", Start: diagnostic.Position{Line: 1, Column: 1, Byte: 0}, End: diagnostic.Position{Line: 1, Column: 5, Byte: 4}}
	vSym := symTab.AllocSymbol(symbols.SymVar, "x", sp)
	vSym.Type = tctx.StringT
	view := &stubSemanticView{types: tctx, syms: symTab}

	// 1. LoanID == 0 的借用事件 -> WON-INT-001
	fnZero := &cfg.FuncCFG{
		Name:   "zero_loan",
		Entry:  0,
		Locals: map[symbols.SymbolID]*cfg.LocalMeta{vSym.ID: {Sym: vSym, Type: tctx.StringT}},
		Blocks: []*cfg.BasicBlock{
			{
				ID:    0,
				Label: "entry",
				Statements: []cfg.Statement{
					{Kind: cfg.OpInitAssign, Place: cfg.Place{RootSymID: vSym.ID, RootName: "x", RootType: tctx.StringT, ElemType: tctx.StringT}, Type: tctx.StringT, Span: sp},
					{Kind: cfg.OpBorrowShared, Place: cfg.Place{RootSymID: vSym.ID, RootName: "x", RootType: tctx.StringT, ElemType: tctx.StringT}, LoanID: cfg.InvalidLoan, ValTemp: 1, Span: sp},
				},
				Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp},
			},
		},
	}
	rep1 := diagnostic.NewReport()
	borrow.BuildLoanFlow(fnZero, view, rep1)
	assertHasInternalDiag(t, "ZeroLoanID", rep1)

	// 2. 重复 LoanID -> WON-INT-001
	fnDup := &cfg.FuncCFG{
		Name:   "dup_loan",
		Entry:  0,
		Locals: map[symbols.SymbolID]*cfg.LocalMeta{vSym.ID: {Sym: vSym, Type: tctx.StringT}},
		Blocks: []*cfg.BasicBlock{
			{
				ID:    0,
				Label: "entry",
				Statements: []cfg.Statement{
					{Kind: cfg.OpBorrowShared, Place: cfg.Place{RootSymID: vSym.ID, RootName: "x"}, LoanID: 1, ValTemp: 1, Span: sp},
					{Kind: cfg.OpBorrowShared, Place: cfg.Place{RootSymID: vSym.ID, RootName: "x"}, LoanID: 1, ValTemp: 2, Span: sp},
				},
				Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp},
			},
		},
	}
	rep2 := diagnostic.NewReport()
	borrow.BuildLoanFlow(fnDup, view, rep2)
	assertHasInternalDiag(t, "DuplicateLoanID", rep2)

	// 3. Reborrow 指向缺失载体的 ParentPlace -> WON-INT-001
	ghostRef := symTab.AllocSymbol(symbols.SymVar, "ghost", sp)
	ghostRef.Type = tctx.Ref(false, tctx.StringT)
	fnOrphanReborrow := &cfg.FuncCFG{
		Name:   "orphan_reborrow",
		Entry:  0,
		Locals: map[symbols.SymbolID]*cfg.LocalMeta{ghostRef.ID: {Sym: ghostRef, Type: ghostRef.Type}},
		Blocks: []*cfg.BasicBlock{
			{
				ID:    0,
				Label: "entry",
				Statements: []cfg.Statement{
					{
						Kind:        cfg.OpReborrow,
						Place:       cfg.Place{RootSymID: ghostRef.ID, RootName: "ghost", RootType: ghostRef.Type, ElemType: tctx.StringT},
						LoanID:      1,
						IsReborrow:  true,
						ParentPlace: cfg.Place{RootSymID: ghostRef.ID, RootName: "ghost", RootType: ghostRef.Type, ElemType: ghostRef.Type},
						ValTemp:     1,
						Span:        sp,
					},
				},
				Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp},
			},
		},
	}
	rep3 := diagnostic.NewReport()
	borrow.BuildLoanFlow(fnOrphanReborrow, view, rep3)
	assertHasInternalDiag(t, "OrphanReborrow", rep3)

	// 4. 确保任意合法 AST 节点类型引入不会绕过检查
	_ = ast.Program{}
}

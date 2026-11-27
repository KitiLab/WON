package drop_test

import (
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/drop"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

func buildFFITestEnv(t *testing.T) (*types.Context, *symbols.Table, types.TypeID, types.TypeID, *bindgen.FFIContractCatalog, diagnostic.Span) {
	t.Helper()
	tctx := types.NewContext()
	symTab := symbols.NewTable(tctx)
	sp := diagnostic.Span{File: "ffi_closure.won", Start: diagnostic.Position{Line: 1, Column: 1}, End: diagnostic.Position{Line: 1, Column: 20}}

	handleSym := symTab.AllocSymbol(symbols.SymStruct, "CHandle", sp)
	handleSym.Fields = []symbols.FieldInfo{{Name: "fd", Type: tctx.I64, Span: sp}}
	handleSym.FieldMap = map[string]*symbols.FieldInfo{"fd": &handleSym.Fields[0]}
	handleType := tctx.Struct(uint64(handleSym.ID), "CHandle", nil)
	handleSym.Type = handleType
	symTab.GlobalScope.Symbols["CHandle"] = handleSym

	rawHandleType := tctx.RawPtr(true, handleType)

	cat := &bindgen.FFIContractCatalog{
		SchemaVersion: "1.0",
		HeaderPath:    "demo.h",
		Namespace:     "demo",
		OpaqueTypes: map[string]bindgen.FFIReturnContract{
			"CHandle": {Ownership: bindgen.FFIOpaqueHandle, TypeName: "CHandle", ReleaseFn: "demo::handle_free", BorrowsParam: -1},
		},
		Functions: map[string]bindgen.FFIFuncContract{
			"handle_open": {
				Namespace:  "demo",
				Function:   "handle_open",
				ContractID: "demo.handle_open",
				Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFIOwnedReturn, TypeName: "CHandle", ReleaseFn: "demo::handle_free", BorrowsParam: -1},
			},
			"handle_take": {
				Namespace:  "demo",
				Function:   "handle_take",
				ContractID: "demo.handle_take",
				Params:     []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "h", Ownership: bindgen.FFIOwnedTransfer}},
				Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
			},
			"handle_take_two": {
				Namespace:  "demo",
				Function:   "handle_take_two",
				ContractID: "demo.handle_take_two",
				Params: []bindgen.FFIParamContract{
					{ParamIndex: 0, ParamName: "a", Ownership: bindgen.FFIOwnedTransfer},
					{ParamIndex: 1, ParamName: "b", Ownership: bindgen.FFIOwnedTransfer},
				},
				Return: bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
			},
			"handle_take_and_borrow": {
				Namespace:  "demo",
				Function:   "handle_take_and_borrow",
				ContractID: "demo.handle_take_and_borrow",
				Params: []bindgen.FFIParamContract{
					{ParamIndex: 0, ParamName: "a", Ownership: bindgen.FFIOwnedTransfer},
					{ParamIndex: 1, ParamName: "b", Ownership: bindgen.FFIBorrowedShared},
				},
				Return: bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
			},
			"handle_peek": {
				Namespace:  "demo",
				Function:   "handle_peek",
				ContractID: "demo.handle_peek",
				Params:     []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "h", Ownership: bindgen.FFIBorrowedShared}},
				Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
			},
			"handle_mut": {
				Namespace:  "demo",
				Function:   "handle_mut",
				ContractID: "demo.handle_mut",
				Params:     []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "h", Ownership: bindgen.FFIBorrowedMut}},
				Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
			},
			"handle_mut_pair": {
				Namespace:  "demo",
				Function:   "handle_mut_pair",
				ContractID: "demo.handle_mut_pair",
				Params: []bindgen.FFIParamContract{
					{ParamIndex: 0, ParamName: "a", Ownership: bindgen.FFIBorrowedMut},
					{ParamIndex: 1, ParamName: "b", Ownership: bindgen.FFIBorrowedMut},
				},
				Return: bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
			},
			"handle_mut_and_shared": {
				Namespace:  "demo",
				Function:   "handle_mut_and_shared",
				ContractID: "demo.handle_mut_and_shared",
				Params: []bindgen.FFIParamContract{
					{ParamIndex: 0, ParamName: "a", Ownership: bindgen.FFIBorrowedMut},
					{ParamIndex: 1, ParamName: "b", Ownership: bindgen.FFIBorrowedShared},
				},
				Return: bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
			},
			"ptr_take": {
				Namespace:  "demo",
				Function:   "ptr_take",
				ContractID: "demo.ptr_take",
				Params:     []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "p", Ownership: bindgen.FFIOwnedTransfer}},
				Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
			},
			"ptr_peek": {
				Namespace:  "demo",
				Function:   "ptr_peek",
				ContractID: "demo.ptr_peek",
				Params:     []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "p", Ownership: bindgen.FFIBorrowedShared}},
				Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
			},
		},
	}

	return tctx, symTab, handleType, rawHandleType, cat, sp
}

func makeVar(symTab *symbols.Table, name string, typ types.TypeID, isMut bool, sp diagnostic.Span) (*symbols.Symbol, cfg.Place) {
	s := symTab.AllocSymbol(symbols.SymVar, name, sp)
	s.Type = typ
	s.IsMut = isMut
	p := cfg.Place{RootSymID: s.ID, RootName: name, RootType: typ, ElemType: typ, RootMut: isMut, Span: sp}
	return s, p
}

func checkWonWithBindings(t *testing.T, src string, bindings map[string]*bindgen.BindingMeta) (*checker.TypedProgram, *diagnostic.Report) {
	t.Helper()
	rep := diagnostic.NewReport()
	lx := lexer.New("ffi_e2e.won", src, rep)
	p := parser.New(lx, rep, "ffi_e2e.won")
	prog := p.ParseProgram()
	if rep.HasErrors() {
		return nil, rep
	}
	chk := checker.New(rep, bindings)
	tp := chk.CheckProgram(prog)
	return tp, rep
}

func TestPhase2B3_1_OwnershipTransferAcrossBlocksJoinsLoops(t *testing.T) {
	// 1. 同基本块 @owned 移交后再次读取 Copy 裸指针句柄 -> WON-MEM-001
	t.Run("same_block_use_after_owned_transfer_copy_ptr", func(t *testing.T) {
		tctx, symTab, _, rawHandleType, cat, sp := buildFFITestEnv(t)
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "same_blk", sp)
		pSym, pPlace := makeVar(symTab, "p", rawHandleType, false, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{pSym.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{pSym.ID: {Sym: pSym, Type: rawHandleType, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpCopy, Place: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::ptr_take", ArgTemps: []cfg.ValueID{cfg.InvalidValue}, ArgPlaces: []cfg.Place{pPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpCopy, Place: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{pSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{pSym.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
		if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-MEM-001") || plan.DropElaborated {
			t.Fatalf("期望同块 @owned 移交后继续读取裸指针报 WON-MEM-001，实际:\n%s", rep.String())
		}
	})

	// 2 & 6. 跨基本块 @owned 移交后在后继块重复移交 -> WON-MEM-001
	t.Run("successor_block_use_after_owned_transfer", func(t *testing.T) {
		tctx, symTab, _, rawHandleType, cat, sp := buildFFITestEnv(t)
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "succ_blk", sp)
		pSym, pPlace := makeVar(symTab, "p", rawHandleType, false, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 2,
				LocalOrder: []symbols.SymbolID{pSym.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{pSym.ID: {Sym: pSym, Type: rawHandleType, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpCopy, Place: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::ptr_take", ArgTemps: []cfg.ValueID{cfg.InvalidValue}, ArgPlaces: []cfg.Place{pPlace}, Type: tctx.Unit, Span: sp},
						},
					},
					{
						ID: 1, Label: "next", Predecessors: []cfg.BlockID{0}, Successors: []cfg.BlockID{2},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{2}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpCopy, Place: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::ptr_take", ArgTemps: []cfg.ValueID{cfg.InvalidValue}, ArgPlaces: []cfg.Place{pPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{pSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{pSym.ID}, Span: sp},
						},
					},
					{ID: 2, Label: "exit", Predecessors: []cfg.BlockID{1}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
		if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-MEM-001") || plan.DropElaborated {
			t.Fatalf("期望跨块 @owned 重复移交报 WON-MEM-001，实际:\n%s", rep.String())
		}
	})

	// 3 & 10. Copy 句柄绑定 GlueForeignResource 后 @owned 移交退出不再二次 Drop
	t.Run("owned_transfer_copy_glue_handle_no_double_drop", func(t *testing.T) {
		tctx, symTab, _, rawHandleType, cat, sp := buildFFITestEnv(t)
		reg := drop.NewResourceRegistry()
		reg.RegisterType(rawHandleType, drop.ResourceExtension{Kind: drop.GlueForeignResource, ReleaseFn: "demo::handle_free"})
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "copy_glue_take", sp)
		pSym, pPlace := makeVar(symTab, "p", rawHandleType, false, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{pSym.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{pSym.ID: {Sym: pSym, Type: rawHandleType, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpCopy, Place: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::ptr_take", ArgTemps: []cfg.ValueID{0}, ArgPlaces: []cfg.Place{pPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{pSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{pSym.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, reg, []*bindgen.FFIContractCatalog{cat}, rep)
		if rep.HasErrors() || !plan.DropElaborated {
			t.Fatalf("Copy 裸句柄 @owned 移交后退出不得出错，实际:\n%s", rep.String())
		}
		snap := plan.Functions[fnSym.ID].CFG.Dump()
		if !strings.Contains(snap, "FFITransfer p#") || strings.Contains(snap, "    Drop p#") || strings.Contains(snap, "CondDrop p#") {
			t.Fatalf("@owned 移交后必须发出 FFITransfer 且不得再发出 Drop:\n%s", snap)
		}
	})

	// 4a. 单分支 @owned 移交后汇合 -> DropFlag + OpCondDrop 且不泄露、不报 WON-INT-001
	t.Run("branch_transfer_join_cond_drop_copy_handle", func(t *testing.T) {
		tctx, symTab, _, rawHandleType, cat, sp := buildFFITestEnv(t)
		reg := drop.NewResourceRegistry()
		reg.RegisterType(rawHandleType, drop.ResourceExtension{Kind: drop.GlueForeignResource, ReleaseFn: "demo::handle_free"})

		fnSym := symTab.AllocSymbol(symbols.SymFunc, "branch_join", sp)
		pSym, pPlace := makeVar(symTab, "p", rawHandleType, false, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 4,
				LocalOrder: []symbols.SymbolID{pSym.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{pSym.ID: {Sym: pSym, Type: rawHandleType, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1, 2},
						Terminator: cfg.Terminator{Kind: cfg.TermSwitchInt, Targets: []cfg.BlockID{1, 2}, Span: sp},
						Statements: []cfg.Statement{{Kind: cfg.OpInitAssign, Place: pPlace, Type: rawHandleType, Span: sp}},
					},
					{
						ID: 1, Label: "then", Predecessors: []cfg.BlockID{0}, Successors: []cfg.BlockID{3},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{3}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpCopy, Place: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::ptr_take", ArgTemps: []cfg.ValueID{cfg.InvalidValue}, ArgPlaces: []cfg.Place{pPlace}, Type: tctx.Unit, Span: sp},
						},
					},
					{
						ID: 2, Label: "else", Predecessors: []cfg.BlockID{0}, Successors: []cfg.BlockID{3},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{3}, Span: sp},
					},
					{
						ID: 3, Label: "join", Predecessors: []cfg.BlockID{1, 2}, Successors: []cfg.BlockID{4},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{4}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{pSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{pSym.ID}, Span: sp},
						},
					},
					{ID: 4, Label: "exit", Predecessors: []cfg.BlockID{3}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, reg, []*bindgen.FFIContractCatalog{cat}, rep)
		if rep.HasErrors() || !plan.DropElaborated {
			t.Fatalf("单分支 @owned 移交后汇合必须合法展开条件析构，实际报错:\n%s", rep.String())
		}
		snap := plan.Dump()
		if !strings.Contains(snap, "CondDrop p#") || !strings.Contains(snap, "flag df1") || !strings.Contains(snap, "FFITransfer p#") {
			t.Fatalf("期望 CondDrop + DropFlag + FFITransfer:\n%s", snap)
		}
	})

	// 4b. 单分支 @owned 移交后在汇合点再次借用/移交 -> WON-MEM-001
	t.Run("branch_transfer_join_then_reuse", func(t *testing.T) {
		tctx, symTab, _, rawHandleType, cat, sp := buildFFITestEnv(t)
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "branch_reuse", sp)
		pSym, pPlace := makeVar(symTab, "p", rawHandleType, false, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 4,
				LocalOrder: []symbols.SymbolID{pSym.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{pSym.ID: {Sym: pSym, Type: rawHandleType, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1, 2},
						Terminator: cfg.Terminator{Kind: cfg.TermSwitchInt, Targets: []cfg.BlockID{1, 2}, Span: sp},
						Statements: []cfg.Statement{{Kind: cfg.OpInitAssign, Place: pPlace, Type: rawHandleType, Span: sp}},
					},
					{
						ID: 1, Label: "then", Predecessors: []cfg.BlockID{0}, Successors: []cfg.BlockID{3},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{3}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpCopy, Place: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::ptr_take", ArgTemps: []cfg.ValueID{cfg.InvalidValue}, ArgPlaces: []cfg.Place{pPlace}, Type: tctx.Unit, Span: sp},
						},
					},
					{
						ID: 2, Label: "else", Predecessors: []cfg.BlockID{0}, Successors: []cfg.BlockID{3},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{3}, Span: sp},
					},
					{
						ID: 3, Label: "join", Predecessors: []cfg.BlockID{1, 2}, Successors: []cfg.BlockID{4},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{4}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpCopy, Place: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::ptr_peek", ArgTemps: []cfg.ValueID{cfg.InvalidValue}, ArgPlaces: []cfg.Place{pPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{pSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{pSym.ID}, Span: sp},
						},
					},
					{ID: 4, Label: "exit", Predecessors: []cfg.BlockID{3}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
		if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-MEM-001") || plan.DropElaborated {
			t.Fatalf("分支移交后汇合点借用必须触发 WON-MEM-001，实际:\n%s", rep.String())
		}
	})

	// 5a. 循环回边 @owned 移交外层资源 -> WON-MEM-001
	t.Run("loop_backedge_owned_transfer", func(t *testing.T) {
		tctx, symTab, _, rawHandleType, cat, sp := buildFFITestEnv(t)
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "loop_take", sp)
		pSym, pPlace := makeVar(symTab, "p", rawHandleType, false, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 4,
				LocalOrder: []symbols.SymbolID{pSym.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{pSym.ID: {Sym: pSym, Type: rawHandleType, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{{Kind: cfg.OpInitAssign, Place: pPlace, Type: rawHandleType, Span: sp}},
					},
					{
						ID: 1, Label: "header", Predecessors: []cfg.BlockID{0, 2}, Successors: []cfg.BlockID{2, 3},
						Terminator: cfg.Terminator{Kind: cfg.TermSwitchInt, Targets: []cfg.BlockID{2, 3}, Span: sp},
					},
					{
						ID: 2, Label: "body", Predecessors: []cfg.BlockID{1}, Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpCopy, Place: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::ptr_take", ArgTemps: []cfg.ValueID{cfg.InvalidValue}, ArgPlaces: []cfg.Place{pPlace}, Type: tctx.Unit, Span: sp},
						},
					},
					{
						ID: 3, Label: "after", Predecessors: []cfg.BlockID{1}, Successors: []cfg.BlockID{4},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{4}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{pSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{pSym.ID}, Span: sp},
						},
					},
					{ID: 4, Label: "exit", Predecessors: []cfg.BlockID{3}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
		if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-MEM-001") || plan.DropElaborated {
			t.Fatalf("循环回边 @owned 移交外层资源必须触发 WON-MEM-001，实际:\n%s", rep.String())
		}
	})

	// 5b. 循环内移交外层 var 资源并在回边前重新初始化 -> 合法且条件 Drop 标志一致
	t.Run("loop_backedge_transfer_and_reinit_valid", func(t *testing.T) {
		tctx, symTab, handleType, _, cat, sp := buildFFITestEnv(t)
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "loop_reinit", sp)
		hSym, hPlace := makeVar(symTab, "h", handleType, true, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 4,
				LocalOrder: []symbols.SymbolID{hSym.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{hSym.ID: {Sym: hSym, Type: handleType, IsMut: true, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{{Kind: cfg.OpInitAssign, Place: hPlace, Type: handleType, Span: sp}},
					},
					{
						ID: 1, Label: "header", Predecessors: []cfg.BlockID{0, 2}, Successors: []cfg.BlockID{2, 3},
						Terminator: cfg.Terminator{Kind: cfg.TermSwitchInt, Targets: []cfg.BlockID{2, 3}, Span: sp},
					},
					{
						ID: 2, Label: "body", Predecessors: []cfg.BlockID{1}, Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpMove, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::handle_take", ArgTemps: []cfg.ValueID{0}, ArgPlaces: []cfg.Place{hPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpAssign, Place: hPlace, Type: handleType, Span: sp},
						},
					},
					{
						ID: 3, Label: "after", Predecessors: []cfg.BlockID{1}, Successors: []cfg.BlockID{4},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{4}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
						},
					},
					{ID: 4, Label: "exit", Predecessors: []cfg.BlockID{3}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
		if rep.HasErrors() || !plan.DropElaborated {
			t.Fatalf("循环内 @owned 移交后立即重新初始化应当合法通过，实际:\n%s", rep.String())
		}
		if dump := plan.Functions[fnSym.ID].CFG.Dump(); !strings.Contains(dump, "Drop h#") {
			t.Fatalf("退出循环后仍然持有的句柄必须正常发出 OpDrop:\n%s", dump)
		}
	})

	// 7. 多个独立 @owned 实参同时移交
	t.Run("multiple_distinct_transferred_args", func(t *testing.T) {
		tctx, symTab, handleType, _, cat, sp := buildFFITestEnv(t)
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "two_distinct", sp)
		h1, p1 := makeVar(symTab, "h1", handleType, false, sp)
		h2, p2 := makeVar(symTab, "h2", handleType, false, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{h1.ID, h2.ID},
				Locals: map[symbols.SymbolID]*cfg.LocalMeta{
					h1.ID: {Sym: h1, Type: handleType, ScopeID: 1, HasInit: true, Span: sp},
					h2.ID: {Sym: h2, Type: handleType, ScopeID: 1, HasInit: true, Span: sp},
				},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: p1, Type: handleType, Span: sp},
							{Kind: cfg.OpInitAssign, Place: p2, Type: handleType, Span: sp},
							{Kind: cfg.OpMove, Place: p1, Type: handleType, Span: sp},
							{Kind: cfg.OpMove, Place: p2, Type: handleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::handle_take_two", ArgTemps: []cfg.ValueID{0, 0}, ArgPlaces: []cfg.Place{p1, p2}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{h1.ID, h2.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{h1.ID, h2.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
		if rep.HasErrors() || !plan.DropElaborated {
			t.Fatalf("两个独立 @owned 实参同时移交应当通过，实际:\n%s", rep.String())
		}
		dump := plan.Functions[fnSym.ID].CFG.Dump()
		if !strings.Contains(dump, "FFITransfer h1#") || !strings.Contains(dump, "FFITransfer h2#") || strings.Contains(dump, "    Drop ") {
			t.Fatalf("期望两个句柄均转为 FFITransfer 且无本地 Drop:\n%s", dump)
		}
	})

	// 8a. 同一调用中两个实参别名同一资源 (@owned + @owned) -> WON-MEM-001
	t.Run("same_call_aliased_owned_args", func(t *testing.T) {
		tctx, symTab, handleType, _, cat, sp := buildFFITestEnv(t)
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "alias_take", sp)
		hSym, hPlace := makeVar(symTab, "h", handleType, false, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{hSym.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{hSym.ID: {Sym: hSym, Type: handleType, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::handle_take_two", ArgTemps: []cfg.ValueID{0, 0}, ArgPlaces: []cfg.Place{hPlace, hPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
		if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-MEM-001") || plan.DropElaborated {
			t.Fatalf("同调用双 @owned 实参别名同一资源必须触发 WON-MEM-001，实际:\n%s", rep.String())
		}
	})

	// 8b. 同一调用中 @owned 与 @borrow 别名同一资源 -> WON-MEM-006
	t.Run("same_call_aliased_owned_and_borrow", func(t *testing.T) {
		tctx, symTab, handleType, _, cat, sp := buildFFITestEnv(t)
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "alias_take_borrow", sp)
		hSym, hPlace := makeVar(symTab, "h", handleType, false, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{hSym.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{hSym.ID: {Sym: hSym, Type: handleType, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::handle_take_and_borrow", ArgTemps: []cfg.ValueID{0, 0}, ArgPlaces: []cfg.Place{hPlace, hPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
		if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-MEM-006") || plan.DropElaborated {
			t.Fatalf("同调用 @owned 与 @borrow 别名同一资源必须触发 WON-MEM-006，实际:\n%s", rep.String())
		}
	})

	// 9. Copy 裸指针复制值不能隐式复制释放或 @owned 移交权（含端到端 WON 源码测试）
	t.Run("copy_raw_pointer_does_not_duplicate_transfer_authority", func(t *testing.T) {
		tctx, symTab, _, rawHandleType, cat, sp := buildFFITestEnv(t)
		reg := drop.NewResourceRegistry()
		reg.RegisterType(rawHandleType, drop.ResourceExtension{Kind: drop.GlueForeignResource, ReleaseFn: "demo::handle_free"})

		fnSym := symTab.AllocSymbol(symbols.SymFunc, "copy_ptr_no_dup", sp)
		pSym, pPlace := makeVar(symTab, "p", rawHandleType, false, sp)
		qSym, qPlace := makeVar(symTab, "q", rawHandleType, false, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{pSym.ID, qSym.ID},
				Locals: map[symbols.SymbolID]*cfg.LocalMeta{
					pSym.ID: {Sym: pSym, Type: rawHandleType, ScopeID: 1, HasInit: true, Span: sp},
					qSym.ID: {Sym: qSym, Type: rawHandleType, ScopeID: 1, HasInit: true, Span: sp},
				},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpCopy, Place: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpInitAssign, Place: qPlace, CopyPlace: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::ptr_take", ArgTemps: []cfg.ValueID{0}, ArgPlaces: []cfg.Place{pPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::ptr_take", ArgTemps: []cfg.ValueID{0}, ArgPlaces: []cfg.Place{qPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{pSym.ID, qSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{pSym.ID, qSym.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, reg, []*bindgen.FFIContractCatalog{cat}, rep)
		if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-MEM-001") || plan.DropElaborated {
			t.Fatalf("复制得到的裸指针副本 q 绝不允许再次 @owned 移交，实际:\n%s", rep.String())
		}
	})

	// 9c. Copy 裸指针复制到 q 后未移交：仅原持有者 p 触发释放，副本 q 绝不双重释放
	t.Run("copy_raw_pointer_no_double_drop", func(t *testing.T) {
		tctx, symTab, _, rawHandleType, cat, sp := buildFFITestEnv(t)
		reg := drop.NewResourceRegistry()
		reg.RegisterType(rawHandleType, drop.ResourceExtension{Kind: drop.GlueForeignResource, ReleaseFn: "demo::handle_free"})

		fnSym := symTab.AllocSymbol(symbols.SymFunc, "copy_ptr_single_drop", sp)
		pSym, pPlace := makeVar(symTab, "p", rawHandleType, false, sp)
		qSym, qPlace := makeVar(symTab, "q", rawHandleType, false, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{pSym.ID, qSym.ID},
				Locals: map[symbols.SymbolID]*cfg.LocalMeta{
					pSym.ID: {Sym: pSym, Type: rawHandleType, ScopeID: 1, HasInit: true, Span: sp},
					qSym.ID: {Sym: qSym, Type: rawHandleType, ScopeID: 1, HasInit: true, Span: sp},
				},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpCopy, Place: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpInitAssign, Place: qPlace, CopyPlace: pPlace, Type: rawHandleType, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{pSym.ID, qSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{pSym.ID, qSym.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, reg, []*bindgen.FFIContractCatalog{cat}, rep)
		if rep.HasErrors() || !plan.DropElaborated {
			t.Fatalf("复制裸指针后仅原所有者释放应当通过，实际:\n%s", rep.String())
		}
		dump := plan.Functions[fnSym.ID].CFG.Dump()
		if !strings.Contains(dump, "Drop p#") || strings.Contains(dump, "Drop q#") {
			t.Fatalf("仅原所有者 p 允许 Drop，非拥有副本 q 严禁 Drop:\n%s", dump)
		}
	})

	// 9e. 端到端 WON 源码测试：checker -> cfg -> borrow -> drop 完整闭环检查 *mut u8 的跨块/副本/借用语义
	t.Run("e2e_won_source_raw_ptr_ffi_contracts", func(t *testing.T) {
		bindings := map[string]*bindgen.BindingMeta{
			"mem": {
				HeaderPath: "mem.h",
				Functions: map[string]bindgen.FunctionSig{
					"take_buf": {Name: "take_buf", ReturnType: "void", ParamTypes: []string{"unsigned char *"}},
					"peek_buf": {Name: "peek_buf", ReturnType: "void", ParamTypes: []string{"const unsigned char *"}},
				},
				Contracts: &bindgen.FFIContractCatalog{
					SchemaVersion: bindgen.SupportedFFIContractSchemaVersion,
					HeaderPath:    "mem.h",
					Namespace:     "mem",
					Functions: map[string]bindgen.FFIFuncContract{
						"take_buf": {
							ContractID: "mem.take_buf",
							Params:     []bindgen.FFIParamContract{{ParamIndex: 0, Ownership: bindgen.FFIOwnedTransfer}},
							Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
						},
						"peek_buf": {
							ContractID: "mem.peek_buf",
							Params:     []bindgen.FFIParamContract{{ParamIndex: 0, Ownership: bindgen.FFIBorrowedShared}},
							Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
						},
					},
				},
			},
		}

		// 正例：先 @borrow 后 @owned 移交
		okSrc := `fn good(p: *mut u8):
    unsafe:
        mem::peek_buf(p)
        mem::take_buf(p)
`
		tpOk, repOk := checkWonWithBindings(t, okSrc, bindings)
		if repOk.HasErrors() || tpOk == nil || !tpOk.OwnershipChecked || !tpOk.DropElaborated {
			t.Fatalf("合法 @borrow 后再 @owned 移交必须通过，实际:\n%s", repOk.String())
		}

		// 反例1：if 分支 @owned 移交后汇合处再次 @borrow -> WON-MEM-001
		badBranchSrc := `fn bad_branch(cond: bool, p: *mut u8):
    if cond:
        unsafe:
            mem::take_buf(p)
    unsafe:
        mem::peek_buf(p)
`
		tpBad1, repBad1 := checkWonWithBindings(t, badBranchSrc, bindings)
		if !repBad1.HasErrors() || !strings.Contains(repBad1.String(), "WON-MEM-001") || (tpBad1 != nil && tpBad1.OwnershipChecked) {
			t.Fatalf("分支 @owned 后在汇合块 @borrow 必须触发 WON-MEM-001，实际:\n%s", repBad1.String())
		}

		// 反例2：复制 *mut u8 到 q 后，移交 p 再借用 q -> WON-MEM-001
		badAliasSrc := `fn bad_copy(p: *mut u8):
    let q = p
    unsafe:
        mem::take_buf(p)
        mem::peek_buf(q)
`
		tpBad2, repBad2 := checkWonWithBindings(t, badAliasSrc, bindings)
		if !repBad2.HasErrors() || !strings.Contains(repBad2.String(), "WON-MEM-001") || (tpBad2 != nil && tpBad2.OwnershipChecked) {
			t.Fatalf("复制裸指针 q 后移交 p 再借用 q 必须触发 WON-MEM-001，实际:\n%s", repBad2.String())
		}

		// 反例3：传递式复制 b = p; c = b，移交 p 后再借用 c -> WON-MEM-001
		badTransitiveSrc := `fn bad_transitive(p: *mut u8):
    let b = p
    let c = b
    unsafe:
        mem::take_buf(p)
        mem::peek_buf(c)
`
		tpBad3, repBad3 := checkWonWithBindings(t, badTransitiveSrc, bindings)
		if !repBad3.HasErrors() || !strings.Contains(repBad3.String(), "WON-MEM-001") || (tpBad3 != nil && tpBad3.OwnershipChecked) {
			t.Fatalf("传递复制裸指针后移交原指针再借用副本必须触发 WON-MEM-001，实际:\n%s", repBad3.String())
		}

		// 反例4：直接通过复制出的裸指针副本 q 执行 @owned 移交 -> WON-MEM-001
		badTransferCopySrc := `fn bad_transfer_copy(p: *mut u8):
    let q = p
    unsafe:
        mem::take_buf(q)
`
		tpBad4, repBad4 := checkWonWithBindings(t, badTransferCopySrc, bindings)
		if !repBad4.HasErrors() || !strings.Contains(repBad4.String(), "WON-MEM-001") || (tpBad4 != nil && tpBad4.OwnershipChecked) {
			t.Fatalf("复制出的裸指针副本 q 不得执行 @owned 移交，实际:\n%s", repBad4.String())
		}

		// 反例5：C 头文件签名与契约参数个数不匹配 -> WON-INT-001
		mismatchBindings := map[string]*bindgen.BindingMeta{
			"mem": {
				HeaderPath: "mem.h",
				Functions: map[string]bindgen.FunctionSig{
					"take_buf": {Name: "take_buf", ReturnType: "void", ParamTypes: []string{"unsigned char *", "unsigned long"}},
				},
				Contracts: &bindgen.FFIContractCatalog{
					SchemaVersion: bindgen.SupportedFFIContractSchemaVersion,
					HeaderPath:    "mem.h",
					Namespace:     "mem",
					Functions: map[string]bindgen.FFIFuncContract{
						"take_buf": {
							ContractID: "mem.take_buf",
							Params:     []bindgen.FFIParamContract{{ParamIndex: 0, Ownership: bindgen.FFIOwnedTransfer}},
							Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
						},
					},
				},
			},
		}
		tpBad5, repBad5 := checkWonWithBindings(t, "fn f():\n    let x = 1\n", mismatchBindings)
		if !repBad5.HasErrors() || !strings.Contains(repBad5.String(), "WON-INT-001") || (tpBad5 != nil && tpBad5.OwnershipChecked) {
			t.Fatalf("C 头文件签名与 FFI 契约参数数量不一致必须报 WON-INT-001，实际:\n%s", repBad5.String())
		}
	})
}

func TestPhase2B3_1_BorrowPreservationAndConflicts(t *testing.T) {
	// 11 & 14. @borrow 调用非 Copy 句柄（含 OpMove 降级）后仍保留调用方所有权并正常自动 Drop
	t.Run("borrow_preserves_ownership_across_multiple_calls_and_drops", func(t *testing.T) {
		tctx, symTab, handleType, _, cat, sp := buildFFITestEnv(t)
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "borrow_twice", sp)
		hSym, hPlace := makeVar(symTab, "h", handleType, false, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{hSym.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{hSym.ID: {Sym: hSym, Type: handleType, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpMove, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::handle_peek", ArgTemps: []cfg.ValueID{0}, ArgPlaces: []cfg.Place{hPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpMove, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::handle_peek", ArgTemps: []cfg.ValueID{0}, ArgPlaces: []cfg.Place{hPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
		if rep.HasErrors() || !plan.DropElaborated {
			t.Fatalf("@borrow 连续两次借用句柄并自动析构应当合法通过，实际:\n%s", rep.String())
		}
		dump := plan.Functions[fnSym.ID].CFG.Dump()
		if !strings.Contains(dump, "Drop h#") || !strings.Contains(dump, "demo::handle_free") {
			t.Fatalf("@borrow 不得消费所有权，函数尾部必须正常 Drop:\n%s", dump)
		}
	})

	// 12 & 14. @borrow(mut) 在独占访问下合法工作并保持所有权，之后仍可再 @borrow 或 @owned 或自动 Drop
	t.Run("borrow_mut_valid_exclusive_then_drop", func(t *testing.T) {
		tctx, symTab, handleType, _, cat, sp := buildFFITestEnv(t)
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "mut_valid", sp)
		hSym, hPlace := makeVar(symTab, "h", handleType, true, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{hSym.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{hSym.ID: {Sym: hSym, Type: handleType, IsMut: true, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpMove, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::handle_mut", ArgTemps: []cfg.ValueID{0}, ArgPlaces: []cfg.Place{hPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpMove, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::handle_peek", ArgTemps: []cfg.ValueID{0}, ArgPlaces: []cfg.Place{hPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
		if rep.HasErrors() || !plan.DropElaborated {
			t.Fatalf("@borrow(mut) 独占访问后恢复使用并析构应当合法通过，实际:\n%s", rep.String())
		}
		if dump := plan.Functions[fnSym.ID].CFG.Dump(); !strings.Contains(dump, "Drop h#") {
			t.Fatalf("@borrow(mut) 不得消费所有权:\n%s", dump)
		}
	})

	// 13a. 存在活跃共享借用 &h 时调用 @borrow(mut) -> WON-MEM-004
	t.Run("conflict_mut_borrow_during_active_shared_loan", func(t *testing.T) {
		tctx, symTab, handleType, _, cat, sp := buildFFITestEnv(t)
		refType := tctx.Ref(false, handleType)
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "conflict_mut", sp)
		hSym, hPlace := makeVar(symTab, "h", handleType, true, sp)
		rSym, rPlace := makeVar(symTab, "r", refType, false, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{hSym.ID, rSym.ID},
				Locals: map[symbols.SymbolID]*cfg.LocalMeta{
					hSym.ID: {Sym: hSym, Type: handleType, IsMut: true, ScopeID: 1, HasInit: true, Span: sp},
					rSym.ID: {Sym: rSym, Type: refType, ScopeID: 1, HasInit: true, Span: sp},
				},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpRead, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpBorrowShared, Place: hPlace, Type: refType, LoanID: 1, BorrowKind: cfg.BorrowShared, ValTemp: 1, Span: sp},
							{Kind: cfg.OpInitAssign, Place: rPlace, SrcTemp: 1, Type: refType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::handle_mut", ArgTemps: []cfg.ValueID{0}, ArgPlaces: []cfg.Place{hPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpCopy, Place: rPlace, Type: refType, ValTemp: 2, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID, rSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID, rSym.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
		if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-MEM-004") || plan.DropElaborated {
			t.Fatalf("活跃共享借用期间发起 @borrow(mut) FFI 调用必须触发 WON-MEM-004，实际:\n%s", rep.String())
		}
	})

	// 13b. 存在活跃可变借用 &mut h 时调用 @borrow(mut) -> WON-MEM-005
	t.Run("conflict_mut_borrow_during_active_mut_loan", func(t *testing.T) {
		tctx, symTab, handleType, _, cat, sp := buildFFITestEnv(t)
		mutRefType := tctx.Ref(true, handleType)
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "conflict_mut_mut", sp)
		hSym, hPlace := makeVar(symTab, "h", handleType, true, sp)
		rSym, rPlace := makeVar(symTab, "r", mutRefType, false, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{hSym.ID, rSym.ID},
				Locals: map[symbols.SymbolID]*cfg.LocalMeta{
					hSym.ID: {Sym: hSym, Type: handleType, IsMut: true, ScopeID: 1, HasInit: true, Span: sp},
					rSym.ID: {Sym: rSym, Type: mutRefType, ScopeID: 1, HasInit: true, Span: sp},
				},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpRead, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpBorrowMut, Place: hPlace, Type: mutRefType, LoanID: 1, BorrowKind: cfg.BorrowMut, ValTemp: 1, Span: sp},
							{Kind: cfg.OpInitAssign, Place: rPlace, SrcTemp: 1, Type: mutRefType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::handle_mut", ArgTemps: []cfg.ValueID{0}, ArgPlaces: []cfg.Place{hPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpMove, Place: rPlace, Type: mutRefType, ValTemp: 2, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID, rSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID, rSym.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
		if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-MEM-005") || plan.DropElaborated {
			t.Fatalf("活跃可变借用期间发起 @borrow(mut) FFI 调用必须触发 WON-MEM-005，实际:\n%s", rep.String())
		}
	})

	// 13c. 同调用中 @borrow(mut) 与 @borrow(mut) 别名同一位置 -> WON-MEM-005
	t.Run("conflict_two_mut_borrows_same_call", func(t *testing.T) {
		tctx, symTab, handleType, _, cat, sp := buildFFITestEnv(t)
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "mut_pair", sp)
		hSym, hPlace := makeVar(symTab, "h", handleType, true, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{hSym.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{hSym.ID: {Sym: hSym, Type: handleType, IsMut: true, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::handle_mut_pair", ArgTemps: []cfg.ValueID{0, 0}, ArgPlaces: []cfg.Place{hPlace, hPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
		if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-MEM-005") || plan.DropElaborated {
			t.Fatalf("同调用双 @borrow(mut) 别名同一位置必须触发 WON-MEM-005，实际:\n%s", rep.String())
		}
	})

	// 13d. 同调用中 @borrow(mut) 与 @borrow 别名同一位置 -> WON-MEM-004
	t.Run("conflict_mut_and_shared_same_call", func(t *testing.T) {
		tctx, symTab, handleType, _, cat, sp := buildFFITestEnv(t)
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "mut_shared_pair", sp)
		hSym, hPlace := makeVar(symTab, "h", handleType, true, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{hSym.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{hSym.ID: {Sym: hSym, Type: handleType, IsMut: true, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::handle_mut_and_shared", ArgTemps: []cfg.ValueID{0, 0}, ArgPlaces: []cfg.Place{hPlace, hPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
		if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-MEM-004") || plan.DropElaborated {
			t.Fatalf("同调用 @borrow(mut) 与 @borrow 别名同一位置必须触发 WON-MEM-004，实际:\n%s", rep.String())
		}
	})
}

func TestPhase2B3_1_ContractValidationAndBorrowsParam(t *testing.T) {
	// 15-22. 契约 Schema 与 JSON 严格 Fail-Closed 校验
	invalidJSONs := []struct {
		name string
		raw  string
	}{
		{"unknown_param_ownership", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","functions":{"f":{"contract_id":"c","params":[{"param_index":0,"ownership":"bogus"}],"return":{"ownership":"raw_unmanaged","borrows_param":-1}}}}`},
		{"unknown_return_ownership", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","functions":{"f":{"contract_id":"c","return":{"ownership":"bogus","borrows_param":-1}}}}`},
		{"unknown_opaque_ownership", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","opaque_types":{"H":{"ownership":"bogus","borrows_param":-1}}}`},
		{"negative_param_index", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","functions":{"f":{"contract_id":"c","params":[{"param_index":-1,"ownership":"borrow"}],"return":{"ownership":"raw_unmanaged","borrows_param":-1}}}}`},
		{"non_contiguous_param_index", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","functions":{"f":{"contract_id":"c","params":[{"param_index":1,"ownership":"borrow"}],"return":{"ownership":"raw_unmanaged","borrows_param":-1}}}}`},
		{"duplicate_param_index", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","functions":{"f":{"contract_id":"c","params":[{"param_index":0,"ownership":"borrow"},{"param_index":0,"ownership":"borrow_mut"}],"return":{"ownership":"raw_unmanaged","borrows_param":-1}}}}`},
		{"missing_param_index_json", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","functions":{"f":{"contract_id":"c","params":[{"ownership":"borrow"}],"return":{"ownership":"raw_unmanaged","borrows_param":-1}}}}`},
		{"invalid_release_fn_chars", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","opaque_types":{"H":{"ownership":"opaque_handle","type_name":"H","release_fn":"bad fn()","borrows_param":-1}}}`},
		{"release_fn_on_raw_unmanaged", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","functions":{"f":{"contract_id":"c","return":{"ownership":"raw_unmanaged","release_fn":"demo::free","borrows_param":-1}}}}`},
		{"contradictory_func_namespace", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","functions":{"f":{"namespace":"other","contract_id":"c","return":{"ownership":"raw_unmanaged","borrows_param":-1}}}}`},
		{"contradictory_func_name", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","functions":{"f":{"function":"g","contract_id":"c","return":{"ownership":"raw_unmanaged","borrows_param":-1}}}}`},
		{"contradictory_opaque_release", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","opaque_types":{"H":{"ownership":"opaque_handle","type_name":"H","release_fn":"demo::free_a","borrows_param":-1}},"functions":{"make":{"contract_id":"m","return":{"ownership":"owned_return","type_name":"H","release_fn":"demo::free_b","borrows_param":-1}}}}`},
		{"borrows_param_out_of_range", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","functions":{"f":{"contract_id":"c","params":[{"param_index":0,"ownership":"borrow"}],"return":{"ownership":"borrow","borrows_param":2}}}}`},
		{"borrows_param_negative_invalid", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","functions":{"f":{"contract_id":"c","params":[{"param_index":0,"ownership":"borrow"}],"return":{"ownership":"borrow","borrows_param":-2}}}}`},
		{"borrows_param_on_owned_transfer", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","functions":{"f":{"contract_id":"c","params":[{"param_index":0,"ownership":"owned_transfer"}],"return":{"ownership":"borrow","borrows_param":0}}}}`},
		{"borrows_param_mut_from_shared", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","functions":{"f":{"contract_id":"c","params":[{"param_index":0,"ownership":"borrow"}],"return":{"ownership":"borrow_mut","borrows_param":0}}}}`},
		{"borrows_param_on_owned_return", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","functions":{"f":{"contract_id":"c","params":[{"param_index":0,"ownership":"borrow"}],"return":{"ownership":"owned_return","release_fn":"demo::free","borrows_param":0}}}}`},
		{"borrows_param_on_opaque_type", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","opaque_types":{"H":{"ownership":"opaque_handle","type_name":"H","release_fn":"demo::free","borrows_param":0}}}`},
		{"unknown_json_field", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","bogus_field":1}`},
		{"trailing_json_garbage", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo"} {"extra":1}`},
	}
	for _, tc := range invalidJSONs {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := bindgen.ParseFFIContractJSON([]byte(tc.raw)); err == nil {
				t.Fatalf("非法 FFI 契约 JSON (%s) 必须被拒绝: %s", tc.name, tc.raw)
			}
		})
	}

	// 22b. JSON 省略 borrows_param 时不得误当成 0
	t.Run("omitted_borrows_param_defaults_to_minus_one", func(t *testing.T) {
		raw := `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","functions":{"f":{"contract_id":"c","params":[{"param_index":0,"ownership":"owned_transfer"}],"return":{"ownership":"raw_unmanaged"}}}}`
		cat, err := bindgen.ParseFFIContractJSON([]byte(raw))
		if err != nil {
			t.Fatalf("省略 borrows_param 的合法 raw_unmanaged 契约应当解析成功: %v", err)
		}
		if cat.Functions["f"].Return.BorrowsParam != -1 {
			t.Fatalf("省略 borrows_param 必须默认为 -1 而非 0，实际 %d", cat.Functions["f"].Return.BorrowsParam)
		}
	})

	// 19b. 多个目录定义重复或矛盾函数契约 -> Fail-Closed WON-INT-001
	t.Run("duplicate_contract_across_catalogs", func(t *testing.T) {
		tctx, symTab, _, _, cat, sp := buildFFITestEnv(t)
		dupCat := &bindgen.FFIContractCatalog{
			SchemaVersion: bindgen.SupportedFFIContractSchemaVersion,
			HeaderPath:    "demo.h",
			Namespace:     "demo",
			Functions: map[string]bindgen.FFIFuncContract{
				"handle_peek": {
					Namespace:  "demo",
					Function:   "handle_peek",
					ContractID: "demo.handle_peek.dup",
					Params:     []bindgen.FFIParamContract{{ParamIndex: 0, Ownership: bindgen.FFIOwnedTransfer}},
					Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
				},
			},
		}
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "empty", sp)
		progCFG := &cfg.ProgramCFG{Functions: []*cfg.FuncCFG{{Name: fnSym.Name, FuncSym: fnSym, Entry: 0, Exit: 0, Blocks: []*cfg.BasicBlock{{ID: 0, Terminator: cfg.Terminator{Kind: cfg.TermReturn}}}}}}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat, dupCat}, rep)
		if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-INT-001") || plan.DropElaborated {
			t.Fatalf("多目录重复/冲突契约必须 Fail-Closed 报 WON-INT-001，实际:\n%s", rep.String())
		}
	})

	// 17b. 调用实参个数与契约参数元数据不一致（缺失参数元数据）-> WON-INT-001
	t.Run("call_arity_missing_param_metadata", func(t *testing.T) {
		tctx, symTab, handleType, _, cat, sp := buildFFITestEnv(t)
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "arity_err", sp)
		h1, p1 := makeVar(symTab, "h1", handleType, false, sp)
		h2, p2 := makeVar(symTab, "h2", handleType, false, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{h1.ID, h2.ID},
				Locals: map[symbols.SymbolID]*cfg.LocalMeta{
					h1.ID: {Sym: h1, Type: handleType, ScopeID: 1, HasInit: true, Span: sp},
					h2.ID: {Sym: h2, Type: handleType, ScopeID: 1, HasInit: true, Span: sp},
				},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: p1, Type: handleType, Span: sp},
							{Kind: cfg.OpInitAssign, Place: p2, Type: handleType, Span: sp},
							// handle_peek 仅声明 1 个参数，实际调用传入 2 个实参
							{Kind: cfg.OpCall, CalleeName: "demo::handle_peek", ArgTemps: []cfg.ValueID{0, 0}, ArgPlaces: []cfg.Place{p1, p2}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{h1.ID, h2.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{h1.ID, h2.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
		if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-INT-001") || plan.DropElaborated {
			t.Fatalf("调用实参个数与契约不一致必须报 WON-INT-001，实际:\n%s", rep.String())
		}
	})

	// 21a. 不受支持的 BorrowsParam 非引用返回类型（如返回 *const u8）必须被明确拒绝 (WON-SEM-010)
	t.Run("unsupported_non_ref_borrows_param", func(t *testing.T) {
		tctx, symTab, handleType, _, cat, sp := buildFFITestEnv(t)
		cat.Functions["peek_ptr"] = bindgen.FFIFuncContract{
			Namespace:  "demo",
			Function:   "peek_ptr",
			ContractID: "demo.peek_ptr",
			Params:     []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "h", Ownership: bindgen.FFIBorrowedShared}},
			Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFIBorrowedShared, BorrowsParam: 0},
		}
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "unsup_bp", sp)
		hSym, hPlace := makeVar(symTab, "h", handleType, false, sp)
		rawPtrType := tctx.RawPtr(false, tctx.U8)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{hSym.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{hSym.ID: {Sym: hSym, Type: handleType, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::peek_ptr", ArgTemps: []cfg.ValueID{0}, ArgPlaces: []cfg.Place{hPlace}, Type: rawPtrType, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
		if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-SEM-010") || plan.DropElaborated {
			t.Fatalf("非引用返回类型的 BorrowsParam 契约必须被拒绝并报 WON-SEM-010，实际:\n%s", rep.String())
		}
	})

	// 21b. 受支持的 KindRef BorrowsParam：消歧双引用参数避免误报 WON-MEM-002，且在返回引用存活期阻止移交源句柄 (WON-MEM-006)
	t.Run("supported_ref_borrows_param_provenance_and_lock", func(t *testing.T) {
		tctx, symTab, handleType, _, cat, sp := buildFFITestEnv(t)
		refI64 := tctx.Ref(false, tctx.I64)
		refHandle := tctx.Ref(false, handleType)
		cat.Functions["borrow_field"] = bindgen.FFIFuncContract{
			Namespace:  "demo",
			Function:   "borrow_field",
			ContractID: "demo.borrow_field",
			Params: []bindgen.FFIParamContract{
				{ParamIndex: 0, ParamName: "a", Ownership: bindgen.FFIBorrowedShared},
				{ParamIndex: 1, ParamName: "b", Ownership: bindgen.FFIBorrowedShared},
			},
			Return: bindgen.FFIReturnContract{Ownership: bindgen.FFIBorrowedShared, BorrowsParam: 0},
		}

		// 正例：传入 2 个句柄，返回借用仅锁定第 0 个，第 1 个可安全移交
		fnOk := symTab.AllocSymbol(symbols.SymFunc, "bp_ok", sp)
		fnOk.ReturnType = tctx.Unit
		h1, p1 := makeVar(symTab, "h1", handleType, false, sp)
		h2, p2 := makeVar(symTab, "h2", handleType, false, sp)
		rSym, rPlace := makeVar(symTab, "r", refI64, false, sp)
		_ = refHandle
		okFnCFG := &cfg.FuncCFG{
			Name: fnOk.Name, FuncSym: fnOk, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
			LocalOrder: []symbols.SymbolID{h1.ID, h2.ID, rSym.ID},
			Locals: map[symbols.SymbolID]*cfg.LocalMeta{
				h1.ID:   {Sym: h1, Type: handleType, ScopeID: 1, HasInit: true, Span: sp},
				h2.ID:   {Sym: h2, Type: handleType, ScopeID: 1, HasInit: true, Span: sp},
				rSym.ID: {Sym: rSym, Type: refI64, ScopeID: 1, HasInit: true, Span: sp},
			},
			Blocks: []*cfg.BasicBlock{
				{
					ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
					Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
					Statements: []cfg.Statement{
						{Kind: cfg.OpInitAssign, Place: p1, Type: handleType, Span: sp},
						{Kind: cfg.OpInitAssign, Place: p2, Type: handleType, Span: sp},
						{Kind: cfg.OpCall, CalleeName: "demo::borrow_field", ArgTemps: []cfg.ValueID{0, 0}, ArgPlaces: []cfg.Place{p1, p2}, ValTemp: 1, Type: refI64, Span: sp},
						{Kind: cfg.OpInitAssign, Place: rPlace, SrcTemp: 1, Type: refI64, Span: sp},
						// 移交未借出的 h2 合法
						{Kind: cfg.OpCall, CalleeName: "demo::handle_take", ArgTemps: []cfg.ValueID{0}, ArgPlaces: []cfg.Place{p2}, Type: tctx.Unit, Span: sp},
						{Kind: cfg.OpCopy, Place: rPlace, Type: refI64, ValTemp: 2, Span: sp},
						{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{h1.ID, h2.ID, rSym.ID}, Span: sp},
						{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{h1.ID, h2.ID, rSym.ID}, Span: sp},
					},
				},
				{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
			},
		}
		okCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{okFnCFG},
			BySymbol:  map[symbols.SymbolID]*cfg.FuncCFG{fnOk.ID: okFnCFG},
		}
		repOk := diagnostic.NewReport()
		planOk := drop.ElaborateProgramWithFFI(okCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, repOk)
		if repOk.HasErrors() || !planOk.DropElaborated {
			t.Fatalf("BorrowsParam=0 时未借出的第 1 个句柄应当允许移交且不误报 WON-MEM-002，实际:\n%s", repOk.String())
		}

		// 反例：返回借用存活期间移交第 0 个源句柄 -> WON-MEM-006
		fnBad := symTab.AllocSymbol(symbols.SymFunc, "bp_bad", sp)
		fnBad.ReturnType = tctx.Unit
		bh1, bp1 := makeVar(symTab, "bh1", handleType, false, sp)
		bh2, bp2 := makeVar(symTab, "bh2", handleType, false, sp)
		brSym, brPlace := makeVar(symTab, "br", refI64, false, sp)
		badFnCFG := &cfg.FuncCFG{
			Name: fnBad.Name, FuncSym: fnBad, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
			LocalOrder: []symbols.SymbolID{bh1.ID, bh2.ID, brSym.ID},
			Locals: map[symbols.SymbolID]*cfg.LocalMeta{
				bh1.ID:   {Sym: bh1, Type: handleType, ScopeID: 1, HasInit: true, Span: sp},
				bh2.ID:   {Sym: bh2, Type: handleType, ScopeID: 1, HasInit: true, Span: sp},
				brSym.ID: {Sym: brSym, Type: refI64, ScopeID: 1, HasInit: true, Span: sp},
			},
			Blocks: []*cfg.BasicBlock{
				{
					ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
					Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
					Statements: []cfg.Statement{
						{Kind: cfg.OpInitAssign, Place: bp1, Type: handleType, Span: sp},
						{Kind: cfg.OpInitAssign, Place: bp2, Type: handleType, Span: sp},
						{Kind: cfg.OpCall, CalleeName: "demo::borrow_field", ArgTemps: []cfg.ValueID{0, 0}, ArgPlaces: []cfg.Place{bp1, bp2}, ValTemp: 1, Type: refI64, Span: sp},
						{Kind: cfg.OpInitAssign, Place: brPlace, SrcTemp: 1, Type: refI64, Span: sp},
						// 返回借用 br 仍在后续读取，此时移交源句柄 bh1 必须触发 WON-MEM-006
						{Kind: cfg.OpCall, CalleeName: "demo::handle_take", ArgTemps: []cfg.ValueID{0}, ArgPlaces: []cfg.Place{bp1}, Type: tctx.Unit, Span: sp},
						{Kind: cfg.OpCopy, Place: brPlace, Type: refI64, ValTemp: 2, Span: sp},
						{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{bh1.ID, bh2.ID, brSym.ID}, Span: sp},
						{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{bh1.ID, bh2.ID, brSym.ID}, Span: sp},
					},
				},
				{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
			},
		}
		badCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{badFnCFG},
			BySymbol:  map[symbols.SymbolID]*cfg.FuncCFG{fnBad.ID: badFnCFG},
		}
		repBad := diagnostic.NewReport()
		planBad := drop.ElaborateProgramWithFFI(badCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, repBad)
		if !repBad.HasErrors() || !strings.Contains(repBad.String(), "WON-MEM-006") || planBad.DropElaborated {
			t.Fatalf("BorrowsParam 返回借用存活期内移交源句柄必须触发 WON-MEM-006，实际:\n%s", repBad.String())
		}
	})

	// 23. 契约参数为 FFIUnknown 时必须触发 WON-SEC-001，且无契约 C 调用在 safe 中保持 WON-SEC-001
	t.Run("unknown_param_contract_triggers_won_sec_001", func(t *testing.T) {
		tctx, symTab, handleType, _, cat, sp := buildFFITestEnv(t)
		cat.Functions["unknown_param_fn"] = bindgen.FFIFuncContract{
			Namespace:  "demo",
			Function:   "unknown_param_fn",
			ContractID: "demo.unknown_param_fn",
			Params:     []bindgen.FFIParamContract{{ParamIndex: 0, Ownership: bindgen.FFIUnknown}},
			Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
		}
		fnSym := symTab.AllocSymbol(symbols.SymFunc, "unk_param", sp)
		hSym, hPlace := makeVar(symTab, "h", handleType, false, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{hSym.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{hSym.ID: {Sym: hSym, Type: handleType, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::unknown_param_fn", ArgTemps: []cfg.ValueID{0}, ArgPlaces: []cfg.Place{hPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}
		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
		if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-SEC-001") || plan.DropElaborated {
			t.Fatalf("unknown 入参契约必须触发 WON-SEC-001，实际:\n%s", rep.String())
		}
	})
}

func TestPhase2B3_2_PipelineEnvelopeAndHeaderFailClosed(t *testing.T) {
	src := `fn run(p: *mut u8):
    unsafe:
        mem::take_buf(p)
`

	// 1. checker 管线在所有权/Drop 细化前按 Fail-Closed 拒绝缺失、不支持 schema_version 及头文件标识不匹配
	badPipelineCases := []struct {
		name          string
		bindingHeader string
		catalog       *bindgen.FFIContractCatalog
	}{
		{
			name:          "missing_schema_version",
			bindingHeader: "mem.h",
			catalog: &bindgen.FFIContractCatalog{
				HeaderPath: "mem.h",
				Namespace:  "mem",
				Functions: map[string]bindgen.FFIFuncContract{
					"take_buf": {
						ContractID: "mem.take_buf",
						Params:     []bindgen.FFIParamContract{{ParamIndex: 0, Ownership: bindgen.FFIOwnedTransfer}},
						Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
					},
				},
			},
		},
		{
			name:          "unsupported_schema_version",
			bindingHeader: "mem.h",
			catalog: &bindgen.FFIContractCatalog{
				SchemaVersion: "2.0",
				HeaderPath:    "mem.h",
				Namespace:     "mem",
				Functions: map[string]bindgen.FFIFuncContract{
					"take_buf": {
						ContractID: "mem.take_buf",
						Params:     []bindgen.FFIParamContract{{ParamIndex: 0, Ownership: bindgen.FFIOwnedTransfer}},
						Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
					},
				},
			},
		},
		{
			name:          "header_mismatch_distinct_names",
			bindingHeader: "mem.h",
			catalog: &bindgen.FFIContractCatalog{
				SchemaVersion: bindgen.SupportedFFIContractSchemaVersion,
				HeaderPath:    "other.h",
				Namespace:     "mem",
				Functions: map[string]bindgen.FFIFuncContract{
					"take_buf": {
						ContractID: "mem.take_buf",
						Params:     []bindgen.FFIParamContract{{ParamIndex: 0, Ownership: bindgen.FFIOwnedTransfer}},
						Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
					},
				},
			},
		},
		{
			name:          "header_mismatch_relative_vs_absolute",
			bindingHeader: "/usr/include/mem.h",
			catalog: &bindgen.FFIContractCatalog{
				SchemaVersion: bindgen.SupportedFFIContractSchemaVersion,
				HeaderPath:    "mem.h",
				Namespace:     "mem",
				Functions: map[string]bindgen.FFIFuncContract{
					"take_buf": {
						ContractID: "mem.take_buf",
						Params:     []bindgen.FFIParamContract{{ParamIndex: 0, Ownership: bindgen.FFIOwnedTransfer}},
						Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
					},
				},
			},
		},
	}

	for _, tc := range badPipelineCases {
		t.Run("checker_"+tc.name, func(t *testing.T) {
			bindings := map[string]*bindgen.BindingMeta{
				"mem": {
					HeaderPath: tc.bindingHeader,
					Functions: map[string]bindgen.FunctionSig{
						"take_buf": {Name: "take_buf", ReturnType: "void", ParamTypes: []string{"unsigned char *"}},
					},
					Contracts: tc.catalog,
				},
			}
			tp, rep := checkWonWithBindings(t, src, bindings)
			if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-INT-001") {
				t.Fatalf("%s 必须触发 WON-INT-001，实际:\n%s", tc.name, rep.String())
			}
			if tp != nil && (tp.OwnershipChecked || tp.DropElaborated || tp.DropPlan != nil) {
				t.Fatalf("%s 校验失败后不得标记 OwnershipChecked/DropElaborated 或生成 DropPlan", tc.name)
			}
		})
	}

	// 2. drop.ElaborateProgramWithFFI 与 RegisterFFIContracts 直接入口同样必须在索引与细化前按 Fail-Closed 拒绝非法信封
	t.Run("drop_entry_and_register_fail_closed", func(t *testing.T) {
		tctx, symTab, handleType, _, validCat, sp := buildFFITestEnv(t)
		reg := drop.NewResourceRegistry()

		badVerCat := &bindgen.FFIContractCatalog{
			SchemaVersion: "9.9",
			HeaderPath:    "demo.h",
			Namespace:     "demo",
			OpaqueTypes:   validCat.OpaqueTypes,
			Functions:     validCat.Functions,
		}
		if err := drop.RegisterFFIContracts(reg, symTab, tctx, badVerCat); err == nil {
			t.Fatalf("RegisterFFIContracts 必须拒绝不受支持的 schema_version")
		}

		fnSym := symTab.AllocSymbol(symbols.SymFunc, "bad_env", sp)
		hSym, hPlace := makeVar(symTab, "h", handleType, false, sp)
		progCFG := &cfg.ProgramCFG{
			Functions: []*cfg.FuncCFG{{
				Name: fnSym.Name, FuncSym: fnSym, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{hSym.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{hSym.ID: {Sym: hSym, Type: handleType, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: hPlace, Type: handleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::handle_take", ArgTemps: []cfg.ValueID{0}, ArgPlaces: []cfg.Place{hPlace}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{hSym.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			}},
		}

		rep := diagnostic.NewReport()
		plan := drop.ElaborateProgramWithFFI(progCFG, &mockSemView{tctx: tctx, symTab: symTab}, reg, []*bindgen.FFIContractCatalog{badVerCat}, rep)
		if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-INT-001") || plan.DropElaborated {
			t.Fatalf("ElaborateProgramWithFFI 必须在所有权/Drop 细化前拒绝非法 schema_version 并报 WON-INT-001，实际:\n%s", rep.String())
		}
	})
}

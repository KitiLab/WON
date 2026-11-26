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

func TestPhase2B3FFIContractSchemaValidation(t *testing.T) {
	validJSON := `{
		"schema_version": "1.0",
		"header_path": "/usr/include/demo_c.h",
		"namespace": "democ",
		"opaque_types": {
			"CStreamHandle": {
				"ownership": "opaque_handle",
				"type_name": "CStreamHandle",
				"release_fn": "democ::stream_free",
				"borrows_param": -1
			}
		},
		"functions": {
			"stream_open": {
				"namespace": "democ",
				"function": "stream_open",
				"contract_id": "democ.stream_open",
				"certified_safe": false,
				"return": {
					"ownership": "owned_return",
					"type_name": "CStreamHandle",
					"release_fn": "democ::stream_free",
					"borrows_param": -1
				}
			},
			"stream_close": {
				"namespace": "democ",
				"function": "stream_close",
				"contract_id": "democ.stream_close",
				"params": [
					{"param_index": 0, "param_name": "s", "ownership": "owned_transfer"}
				],
				"return": {"ownership": "raw_unmanaged", "borrows_param": -1}
			}
		}
	}`
	cat, err := bindgen.ParseFFIContractJSON([]byte(validJSON))
	if err != nil || cat.Namespace != "democ" || len(cat.Functions) != 2 {
		t.Fatalf("合法 FFI 所有权契约解析失败: err=%v cat=%+v", err, cat)
	}

	// 缺失 release_fn 的 owned_return / opaque_handle 必须 Fail-Closed 报错
	badCases := []string{
		`{"namespace":"democ","opaque_types":{"Bad":{"ownership":"opaque_handle"}}}`,
		`{"namespace":"democ","functions":{"create":{"contract_id":"c","return":{"ownership":"owned_return"}}}}`,
		`{"namespace":"democ","functions":{"bad_safe":{"contract_id":"b","certified_safe":true,"params":[{"param_index":0,"ownership":"unknown"}],"return":{"ownership":"raw_unmanaged"}}}}`,
	}
	for idx, raw := range badCases {
		if _, err := bindgen.ParseFFIContractJSON([]byte(raw)); err == nil {
			t.Fatalf("非法 FFI 契约用例 #%d 必须被 Validate() 拒绝", idx)
		}
	}
}

func TestPhase2B3FFIHandleLifecycleAndTransfer(t *testing.T) {
	tctx := types.NewContext()
	symTab := symbols.NewTable(tctx)
	sp := diagnostic.Span{File: "ffi_drop.won", Start: diagnostic.Position{Line: 1, Column: 1}, End: diagnostic.Position{Line: 1, Column: 20}}

	handleSym := symTab.AllocSymbol(symbols.SymStruct, "CHandle", sp)
	handleSym.Fields = []symbols.FieldInfo{{Name: "fd", Type: tctx.I64, Span: sp}}
	handleSym.FieldMap = map[string]*symbols.FieldInfo{"fd": &handleSym.Fields[0]}
	handleType := tctx.Struct(uint64(handleSym.ID), "CHandle", nil)
	handleSym.Type = handleType
	symTab.GlobalScope.Symbols["CHandle"] = handleSym

	cat := &bindgen.FFIContractCatalog{
		SchemaVersion: "1.0",
		HeaderPath:    "demo.h",
		Namespace:     "demo",
		OpaqueTypes: map[string]bindgen.FFIReturnContract{
			"CHandle": {Ownership: bindgen.FFIOpaqueHandle, TypeName: "CHandle", ReleaseFn: "demo::handle_free", BorrowsParam: -1},
		},
		Functions: map[string]bindgen.FFIFuncContract{
			"handle_take": {
				Namespace:  "demo",
				Function:   "handle_take",
				ContractID: "demo.handle_take",
				Params:     []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "h", Ownership: bindgen.FFIOwnedTransfer}},
				Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
			},
			"handle_peek": {
				Namespace:  "demo",
				Function:   "handle_peek",
				ContractID: "demo.handle_peek",
				Params:     []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "h", Ownership: bindgen.FFIBorrowedShared}},
				Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
			},
		},
	}

	view := &mockSemView{tctx: tctx, symTab: symTab}

	// Case 1: handle_peek (@borrow) 后仍在本地持有，函数出口按 GlueForeignResource(demo::handle_free) 释放
	fnSym1 := symTab.AllocSymbol(symbols.SymFunc, "borrow_and_auto_drop", sp)
	h1 := symTab.AllocSymbol(symbols.SymVar, "h1", sp)
	h1.Type = handleType
	p1 := cfg.Place{RootSymID: h1.ID, RootName: "h1", RootType: handleType, ElemType: handleType}

	// Case 2: handle_take (@owned) 后触发 OpFFITransfer，函数出口不得再次 OpDrop
	fnSym2 := symTab.AllocSymbol(symbols.SymFunc, "transfer_no_double_drop", sp)
	h2 := symTab.AllocSymbol(symbols.SymVar, "h2", sp)
	h2.Type = handleType
	p2 := cfg.Place{RootSymID: h2.ID, RootName: "h2", RootType: handleType, ElemType: handleType}

	progCFG := &cfg.ProgramCFG{
		Functions: []*cfg.FuncCFG{
			{
				Name: fnSym1.Name, FuncSym: fnSym1, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{h1.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{h1.ID: {Sym: h1, Type: handleType, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: p1, Type: handleType, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::handle_peek", ArgTemps: []cfg.ValueID{cfg.InvalidValue}, ArgPlaces: []cfg.Place{p1}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{h1.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{h1.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			},
			{
				Name: fnSym2.Name, FuncSym: fnSym2, ReturnType: tctx.Unit, Entry: 0, Exit: 1,
				LocalOrder: []symbols.SymbolID{h2.ID},
				Locals:     map[symbols.SymbolID]*cfg.LocalMeta{h2.ID: {Sym: h2, Type: handleType, ScopeID: 1, HasInit: true, Span: sp}},
				Blocks: []*cfg.BasicBlock{
					{
						ID: 0, Label: "entry", Successors: []cfg.BlockID{1},
						Terminator: cfg.Terminator{Kind: cfg.TermGoto, Targets: []cfg.BlockID{1}, Span: sp},
						Statements: []cfg.Statement{
							{Kind: cfg.OpInitAssign, Place: p2, Type: handleType, Span: sp},
							{Kind: cfg.OpMove, Place: p2, Type: handleType, ValTemp: 1, Span: sp},
							{Kind: cfg.OpCall, CalleeName: "demo::handle_take", ArgTemps: []cfg.ValueID{1}, ArgPlaces: []cfg.Place{p2}, Type: tctx.Unit, Span: sp},
							{Kind: cfg.OpScopeExit, ScopeID: 1, Locals: []symbols.SymbolID{h2.ID}, Span: sp},
							{Kind: cfg.OpStorageDead, ScopeID: 1, Locals: []symbols.SymbolID{h2.ID}, Span: sp},
						},
					},
					{ID: 1, Label: "exit", Predecessors: []cfg.BlockID{0}, Terminator: cfg.Terminator{Kind: cfg.TermReturn, Span: sp}},
				},
			},
		},
	}

	rep := diagnostic.NewReport()
	plan := drop.ElaborateProgramWithFFI(progCFG, view, drop.NewResourceRegistry(), []*bindgen.FFIContractCatalog{cat}, rep)
	if rep.HasErrors() || !plan.DropElaborated {
		t.Fatalf("FFI 生命周期展开失败:\n%s", rep.String())
	}

	snap := plan.Dump()
	if !strings.Contains(snap, "kind=ForeignResource") {
		t.Fatalf("期望生成 ForeignResource DropGlue:\n%s", snap)
	}

	dump1 := plan.Functions[fnSym1.ID].CFG.Dump()
	if !strings.Contains(dump1, "Drop h1") || !strings.Contains(dump1, "demo::handle_free") {
		t.Fatalf("@borrow 调用后本地句柄必须按 demo::handle_free 自动 Drop:\n%s", dump1)
	}

	dump2 := plan.Functions[fnSym2.ID].CFG.Dump()
	if !strings.Contains(dump2, "FFITransfer h2") || strings.Contains(dump2, "    Drop h2") {
		t.Fatalf("@owned 移交 C 侧必须生成 OpFFITransfer 且跳过本地 OpDrop:\n%s", dump2)
	}
	if len(plan.Functions[fnSym2.ID].FFIContracts) == 0 {
		t.Fatalf("期望记录 FFIContracts 审计映射")
	}
}

func TestPhase2B3UnknownFFIFailsClosedOutsideUnsafe(t *testing.T) {
	// 无显式契约的未知 C 调用在 safe 函数内必须严格触发 WON-SEC-001，绝不静默放行
	src := `#import <zlib.h>
fn unsafe_call() -> u64:
    return zlib::compressBound(16 as u64)
`
	rep := diagnostic.NewReport()
	lx := lexer.New("unknown_ffi.won", src, rep)
	p := parser.New(lx, rep, "unknown_ffi.won")
	prog := p.ParseProgram()
	if rep.HasErrors() {
		t.Fatalf("解析失败: %s", rep.String())
	}

	bindings := map[string]*bindgen.BindingMeta{
		"zlib": {
			HeaderPath: "/usr/include/zlib.h",
			Functions: map[string]bindgen.FunctionSig{
				"compressBound": {
					Name:       "compressBound",
					QualType:   "unsigned long (unsigned long)",
					ReturnType: "unsigned long",
					ParamTypes: []string{"unsigned long"},
				},
			},
		},
	}
	chk := checker.New(rep, bindings)
	tp := chk.CheckProgram(prog)
	if !rep.HasErrors() || !strings.Contains(rep.String(), "WON-SEC-001") {
		t.Fatalf("缺乏 FFI 契约时在 unsafe 外调用 C 函数必须触发 WON-SEC-001:\n%s", rep.String())
	}
	if tp.OwnershipChecked || tp.DropElaborated {
		t.Fatalf("存在 WON-SEC-001 时禁止认证 OwnershipChecked / DropElaborated")
	}
}

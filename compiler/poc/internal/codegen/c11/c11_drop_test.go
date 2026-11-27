package c11_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/codegen/c11"
	"github.com/KitiLab/WON/compiler/poc/internal/tir"
)

func buildOpaqueResourceBindings() (hdrContent string, bindings map[string]*bindgen.BindingMeta) {
	hdrContent = `
#ifndef RES_DEMO_H
#define RES_DEMO_H
#include <stdint.h>

typedef struct {
    int64_t id;
} ResHandle;

static int64_t g_res_open_calls = 0;
static int64_t g_res_free_calls = 0;
static int64_t g_res_take_calls = 0;
static int64_t g_res_peek_calls = 0;
static int64_t g_res_live[64] = {0};

static inline ResHandle res_open(int64_t id) {
    g_res_open_calls++;
    if (id >= 0 && id < 64) {
        g_res_live[id]++;
    }
    ResHandle h;
    h.id = id;
    return h;
}

static inline void res_free(ResHandle h) {
    g_res_free_calls++;
    if (h.id >= 0 && h.id < 64) {
        g_res_live[h.id]--;
    }
}

static inline void res_take(ResHandle h) {
    g_res_take_calls++;
    if (h.id >= 0 && h.id < 64) {
        g_res_live[h.id]--;
    }
}

static inline int32_t res_peek(ResHandle h) {
    g_res_peek_calls++;
    if (h.id >= 0 && h.id < 64 && g_res_live[h.id] == 1) {
        return (int32_t)h.id;
    }
    return -1;
}

static inline int64_t res_open_count(void) { return g_res_open_calls; }
static inline int64_t res_free_count(void) { return g_res_free_calls; }
static inline int64_t res_take_count(void) { return g_res_take_calls; }
static inline int64_t res_peek_count(void) { return g_res_peek_calls; }
static inline int32_t res_all_closed_cleanly(void) {
    for (int i = 0; i < 64; i++) {
        if (g_res_live[i] != 0) {
            return 0;
        }
    }
    return 1;
}
#endif
`

	cat := &bindgen.FFIContractCatalog{
		SchemaVersion: bindgen.SupportedFFIContractSchemaVersion,
		HeaderPath:    "res_demo.h",
		Namespace:     "res",
		OpaqueTypes: map[string]bindgen.FFIReturnContract{
			"ResHandle": {
				Ownership:    bindgen.FFIOpaqueHandle,
				TypeName:     "ResHandle",
				ReleaseFn:    "res::res_free",
				BorrowsParam: -1,
			},
		},
		Functions: map[string]bindgen.FFIFuncContract{
			"res_open": {
				Namespace:  "res",
				Function:   "res_open",
				ContractID: "res.res_open",
				Params:     []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "id", Ownership: bindgen.FFINonOwningRaw}},
				Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFIOwnedReturn, TypeName: "ResHandle", ReleaseFn: "res::res_free", BorrowsParam: -1},
			},
			"res_take": {
				Namespace:  "res",
				Function:   "res_take",
				ContractID: "res.res_take",
				Params:     []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "h", Ownership: bindgen.FFIOwnedTransfer}},
				Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
			},
			"res_peek": {
				Namespace:  "res",
				Function:   "res_peek",
				ContractID: "res.res_peek",
				Params:     []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "h", Ownership: bindgen.FFIBorrowedShared}},
				Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
			},
			"res_open_count":         {Namespace: "res", Function: "res_open_count", ContractID: "res.res_open_count", Return: bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1}},
			"res_free_count":         {Namespace: "res", Function: "res_free_count", ContractID: "res.res_free_count", Return: bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1}},
			"res_take_count":         {Namespace: "res", Function: "res_take_count", ContractID: "res.res_take_count", Return: bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1}},
			"res_peek_count":         {Namespace: "res", Function: "res_peek_count", ContractID: "res.res_peek_count", Return: bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1}},
			"res_all_closed_cleanly": {Namespace: "res", Function: "res_all_closed_cleanly", ContractID: "res.res_all_closed_cleanly", Return: bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1}},
		},
	}

	bindings = map[string]*bindgen.BindingMeta{
		"res": {
			HeaderPath: "res_demo.h",
			Functions: map[string]bindgen.FunctionSig{
				"res_open":               {Name: "res_open", ReturnType: "ResHandle", ParamTypes: []string{"int64_t"}},
				"res_free":               {Name: "res_free", ReturnType: "void", ParamTypes: []string{"ResHandle"}},
				"res_take":               {Name: "res_take", ReturnType: "void", ParamTypes: []string{"ResHandle"}},
				"res_peek":               {Name: "res_peek", ReturnType: "int32_t", ParamTypes: []string{"ResHandle"}},
				"res_open_count":         {Name: "res_open_count", ReturnType: "int64_t"},
				"res_free_count":         {Name: "res_free_count", ReturnType: "int64_t"},
				"res_take_count":         {Name: "res_take_count", ReturnType: "int64_t"},
				"res_peek_count":         {Name: "res_peek_count", ReturnType: "int64_t"},
				"res_all_closed_cleanly": {Name: "res_all_closed_cleanly", ReturnType: "int32_t"},
			},
			Contracts: cat,
		},
	}
	return hdrContent, bindings
}

func TestC11NativeForeignResourceDropExecutionAllPaths(t *testing.T) {
	hdrContent, bindings := buildOpaqueResourceBindings()
	hdrDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(hdrDir, "res_demo.h"), []byte(hdrContent), 0644); err != nil {
		t.Fatalf("写入头文件失败: %v", err)
	}

	src := `
#import "res_demo.h"

fn scope_and_borrow(id: i64) -> i32:
    let h = unsafe:
        res::res_open(id)
    let seen = unsafe:
        res::res_peek(h)
    return seen

fn early_return_and_cond_transfer(cond: bool, id: i64) -> i32:
    let h = unsafe:
        res::res_open(id)
    if cond:
        unsafe:
            res::res_take(h)
        return 10
    let seen = unsafe:
        res::res_peek(h)
    return seen

fn conditional_init_case(cond: bool, id: i64):
    var h: ResHandle
    if cond:
        h = unsafe:
            res::res_open(id)
        let v = unsafe:
            res::res_peek(h)
        if v < 0:
            return

fn reassign_case(id1: i64, id2: i64) -> i32:
    var h = unsafe:
        res::res_open(id1)
    let first = unsafe:
        res::res_peek(h)
    h = unsafe:
        res::res_open(id2)
    let second = unsafe:
        res::res_peek(h)
    return first + second

fn transfer_then_reinit_partial(cond: bool, id1: i64, id2: i64):
    var h = unsafe:
        res::res_open(id1)
    if cond:
        unsafe:
            res::res_take(h)
    else:
        let _s = unsafe:
            res::res_peek(h)
    h = unsafe:
        res::res_open(id2)

fn main() -> i32:
    if scope_and_borrow(1) != 1:
        return 1
    if early_return_and_cond_transfer(true, 2) != 10:
        return 2
    if early_return_and_cond_transfer(false, 3) != 3:
        return 3
    conditional_init_case(true, 4)
    conditional_init_case(false, 5)
    if reassign_case(6, 7) != 13:
        return 4
    transfer_then_reinit_partial(true, 8, 9)
    transfer_then_reinit_partial(false, 10, 11)

    let opens = unsafe:
        res::res_open_count()
    let frees = unsafe:
        res::res_free_count()
    let takes = unsafe:
        res::res_take_count()
    let peeks = unsafe:
        res::res_peek_count()
    let clean = unsafe:
        res::res_all_closed_cleanly()

    println("OPENS=", opens, " FREES=", frees, " TAKES=", takes, " PEEKS=", peeks, " CLEAN=", clean)
    if opens != 10 or takes != 2 or frees != 8 or peeks != 6 or clean != 1:
        return 5
    return 0
`
	tirProg, _ := lowerSourceToTIR(t, src, bindings)
	cCode, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)
	if err != nil {
		t.Fatalf("TIR->C11 Drop 代码生成失败: %v", err)
	}

	if !strings.Contains(cCode, "res_free(") {
		t.Fatalf("生成的 C 源码必须包含真实 res_free(...) 析构调用:\n%s", cCode)
	}
	if !strings.Contains(cCode, "bool _won_df") {
		t.Fatalf("生成的 C 源码必须包含条件初始化 DropFlag:\n%s", cCode)
	}

	gccOut, clangOut := compileStrictGCCAndClang(t, cCode, []string{"-I", hdrDir}, nil)
	if gccOut != clangOut || !strings.Contains(gccOut, "OPENS=10 FREES=8 TAKES=2 PEEKS=6 CLEAN=1") {
		t.Fatalf("Drop 执行计数验证失败:\ngcc=%q\nclang=%q", gccOut, clangOut)
	}
}

func TestC11DropRejectsUnsupportedGluesFailClosed(t *testing.T) {
	tt := tir.NewTypeTable()
	tI32 := tt.Int(true, 32, false, "i32")
	tStr := tt.StringType()
	tRes := tt.ReserveStruct("OpaqueBad", "BadRes", nil, false, true, "bad")
	tt.DefineStruct(tRes, nil)

	tests := []struct {
		name string
		prog *tir.Program
		want string
	}{
		{
			name: "GlueStringRejected",
			want: "WON-GEN-002",
			prog: &tir.Program{
				Types:       tt,
				OwnershipOK: true,
				DropOK:      true,
				DropGlues:   []tir.DropGlueDesc{{ID: 1, CanonicalName: "g_str", Kind: tir.GlueString, Type: tStr}},
				Functions: []*tir.Function{{
					ID: 1, Name: "main", ReturnType: tI32, EntryBlock: 0, ExitBlock: 0,
					Blocks: []*tir.BasicBlock{{
						ID:         0,
						Label:      "entry",
						Terminator: tir.Terminator{Kind: tir.TermReturn, ReturnVal: ptrOp(tir.ConstOperand(tI32, tir.Constant{Kind: tir.ConstInt, IntText: "0"}))},
					}},
				}},
			},
		},
		{
			name: "ForeignResourceWithoutHeaderRejected",
			want: "WON-GEN-002",
			prog: &tir.Program{
				Types:       tt,
				OwnershipOK: true,
				DropOK:      true,
				DropGlues:   []tir.DropGlueDesc{{ID: 1, CanonicalName: "g_bad", Kind: tir.GlueForeignResource, Type: tRes, ReleaseSymbol: "bad::free_res"}},
				Functions: []*tir.Function{{
					ID: 1, Name: "main", ReturnType: tI32, EntryBlock: 0, ExitBlock: 0,
					Blocks: []*tir.BasicBlock{{
						ID:         0,
						Label:      "entry",
						Terminator: tir.Terminator{Kind: tir.TermReturn, ReturnVal: ptrOp(tir.ConstOperand(tI32, tir.Constant{Kind: tir.ConstInt, IntText: "0"}))},
					}},
				}},
			},
		},
	}

	em := c11.NewEmitter(c11.DefaultLinuxX86_64())
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := em.Emit(tc.prog)
			var capErr *c11.CapabilityError
			if !errors.As(err, &capErr) || capErr.Code != tc.want {
				t.Fatalf("期望 %s，实际: %T %v", tc.want, err, err)
			}
		})
	}
}

func ptrOp(op tir.Operand) *tir.Operand { return &op }

func TestC11LoopDropReinitUnitReturnAndDeref(t *testing.T) {
	hdrContent, bindings := buildOpaqueResourceBindings()
	hdrContent += `
static int g_void_hook_calls = 0;
static inline void void_hook(void) { g_void_hook_calls++; }
static inline int get_void_hook_calls(void) { return g_void_hook_calls; }
`
	bindings["res"].Functions["void_hook"] = bindgen.FunctionSig{Name: "void_hook", ReturnType: "void"}
	bindings["res"].Functions["get_void_hook_calls"] = bindgen.FunctionSig{Name: "get_void_hook_calls", ReturnType: "int"}
	bindings["res"].Contracts.Functions["void_hook"] = bindgen.FFIFuncContract{
		Namespace: "res", Function: "void_hook", ContractID: "res.void_hook",
		Return: bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
	}
	bindings["res"].Contracts.Functions["get_void_hook_calls"] = bindgen.FFIFuncContract{
		Namespace: "res", Function: "get_void_hook_calls", ContractID: "res.get_void_hook_calls",
		Return: bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
	}

	hdrDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(hdrDir, "res_demo.h"), []byte(hdrContent), 0644); err != nil {
		t.Fatalf("写入头文件失败: %v", err)
	}

	src := `
#import "res_demo.h"

fn bump_ptr(p: *mut i32):
    unsafe:
        *p = *p + 10

fn run_hook_delegated() -> unit:
    return unsafe:
        res::void_hook()

fn loop_drop_test():
    var i: i64 = 0
    while i < 3:
        var h = unsafe:
            res::res_open(i + 1)
        if i == 1:
            unsafe:
                res::res_take(h)
        i = i + 1

fn main() -> unit:
    var n: i32 = 5
    let p: *mut i32 = &mut n as *mut i32
    bump_ptr(p)
    run_hook_delegated()
    loop_drop_test()

    let hooks = unsafe:
        res::get_void_hook_calls()
    let opens = unsafe:
        res::res_open_count()
    let frees = unsafe:
        res::res_free_count()
    let takes = unsafe:
        res::res_take_count()
    let clean = unsafe:
        res::res_all_closed_cleanly()

    if n != 15 or hooks != 1 or opens != 3 or frees != 2 or takes != 1 or clean != 1:
        println("FAIL n=", n, " hooks=", hooks, " opens=", opens, " frees=", frees, " takes=", takes, " clean=", clean)
        return
    println("LOOP_UNIT_DEREF_OK n=", n, " clean=", clean)
`
	tirProg, _ := lowerSourceToTIR(t, src, bindings)
	cCode, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)
	if err != nil {
		t.Fatalf("循环 Drop / unit 返回 / 指针解引用发射失败: %v", err)
	}

	if strings.Contains(cCode, "void main(") || strings.Contains(cCode, "return void_hook()") {
		t.Fatalf("违背 ISO C11 void ABI:\n%s", cCode)
	}
	if !strings.Contains(cCode, "static void won_user_main(void)") {
		t.Fatalf("unit 返回 main 缺少 won_user_main 包装:\n%s", cCode)
	}

	gccOut, clangOut := compileStrictGCCAndClang(t, cCode, []string{"-I", hdrDir}, nil)
	if gccOut != clangOut || !strings.Contains(gccOut, "LOOP_UNIT_DEREF_OK n=15 clean=1") {
		t.Fatalf("运行断言失败:\ngcc=%q\nclang=%q", gccOut, clangOut)
	}
}

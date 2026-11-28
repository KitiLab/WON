package c11_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/codegen/c11"
)

func TestC11StageC_StructsTuplesArraysAndZSTExecution(t *testing.T) {
	src := `
struct Empty:
    z: [u8; 0]

struct L2Mixed:
    var tag: u8
    big: u64
    empty: Empty
    var mid: i32
    msg: &str

fn make_mixed(msg: &str) -> L2Mixed:
    return L2Mixed { tag: 7u8, big: 1000u64, empty: Empty { z: [0u8; 0] }, mid: -5i32, msg: msg }

fn sum_pair(p: (i32, i64)) -> i64:
    let (a, b) = p
    return (a as i64) + b

fn sum_arr_by_val(a: [i32; 4]) -> i32:
    return a[0] + a[1] + a[2] + a[3]

fn zst_roundtrip(e: Empty, za: [u8; 0]) -> Empty:
    return e

fn main() -> i32:
    var m = make_mixed("hello-agg")
    m.mid = m.mid + 15
    if m.tag != 7u8 or m.big != 1000u64 or m.mid != 10:
        return 1

    let rm: &mut L2Mixed = &mut m
    rm.tag = 9u8
    if m.tag != 9u8:
        return 2

    let ((p1, p2), p3): ((i32, i32), i32) = ((11, 22), 33)
    if p1 + p2 + p3 != 66:
        return 3
    if sum_pair((10, 20i64)) != 30:
        return 4

    var arr: [i32; 4] = [1, 2, 3, 4]
    var dyn_i: usize = 2
    arr[dyn_i] = 30
    let rep: [i32; 4] = [5; 4]
    if sum_arr_by_val(arr) != 37 or sum_arr_by_val(rep) != 20:
        return 5

    let e0 = Empty { z: [0u8; 0] }
    let za0: [u8; 0] = [0u8; 0]
    let e1 = zst_roundtrip(e0, za0)
    let _ignore = e1
    println("AGG=", m.tag, " ", m.big, " ", m.mid, " ", m.msg, " LEN=", za0.len())
    return 0
`
	tirProg, _ := lowerSourceToTIR(t, src, nil)
	cCode, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)
	if err != nil {
		t.Fatalf("Stage C C11 生成失败: %v", err)
	}
	if !strings.Contains(cCode, "_Static_assert(") {
		t.Fatalf("生成的 C11 必须包含聚合布局 _Static_assert:\n%s", cCode)
	}
	gccOut, clangOut := compileStrictGCCAndClang(t, cCode, nil, nil)
	if gccOut != clangOut || !strings.Contains(gccOut, "AGG=9 1000 10 hello-agg LEN=0") {
		t.Fatalf("Stage C 聚合执行输出异常:\ngcc=%q\nclang=%q", gccOut, clangOut)
	}
}

func TestC11StageD_EnumsOptionResultMatchAndTryExecution(t *testing.T) {
	src := `
enum Color:
    Red
    Green
    Blue

struct BoxVal:
    a: i32
    b: i32

enum Shape:
    Empty
    TupleBox((i32, i32))
    StructBox:
        bx: BoxVal

fn try_chain(flag: bool) -> Result[i32, i32]:
    if flag:
        return Result::Ok(42)
    return Result::Err(-7)

fn call_try(flag: bool) -> Result[i32, i32]:
    let v = try_chain(flag)?
    return Result::Ok(v + 8)

fn opt_chain(flag: bool) -> Option[i32]:
    if flag:
        return Option::Some(15)
    return Option::None

fn call_opt(flag: bool) -> Option[i32]:
    let v = opt_chain(flag)?
    return Option::Some(v * 2)

fn match_int_and_bool(n: i32, b: bool) -> i32:
    let x: i32 = match n:
        1 => 100
        2 if b => 200
        2 => 250
        _ => 300
    let y: i32 = match b:
        true => 10
        false => 20
    return x + y

fn main() -> i32:
    let c = Color::Blue
    let c_code: i32 = match c:
        Color::Red => 1
        Color::Green => 2
        Color::Blue => 3
    if c_code != 3:
        return 1

    let s1 = Shape::TupleBox((4, 6))
    let r1: i32 = match s1:
        Shape::Empty => 0
        Shape::TupleBox((u, v)) => u + v
        Shape::StructBox { bx } => bx.a + bx.b
    if r1 != 10:
        return 2

    let s2 = Shape::StructBox { bx: BoxVal { a: 12, b: 18 } }
    let r2: i32 = match &s2:
        Shape::Empty => 0
        Shape::TupleBox(_) => 0
        Shape::StructBox { bx: ref rb } => rb.a + rb.b
    if r2 != 30:
        return 3

    let r_ok: i32 = match call_try(true):
        Result::Ok(v) => v
        Result::Err(e) => e
    let r_err: i32 = match call_try(false):
        Result::Ok(v) => v
        Result::Err(e) => e
    if r_ok != 50 or r_err != -7:
        return 4

    let o_some: i32 = match call_opt(true):
        Option::Some(v) => v
        Option::None => -1
    let o_none: i32 = match call_opt(false):
        Option::Some(v) => v
        Option::None => -1
    if o_some != 30 or o_none != -1:
        return 5

    if match_int_and_bool(2, true) != 210 or match_int_and_bool(2, false) != 270:
        return 6

    println("ENUM_OK")
    return 0
`
	tirProg, _ := lowerSourceToTIR(t, src, nil)
	cCode, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)
	if err != nil {
		t.Fatalf("Stage D C11 生成失败: %v", err)
	}
	gccOut, clangOut := compileStrictGCCAndClang(t, cCode, nil, nil)
	if gccOut != clangOut || !strings.Contains(gccOut, "ENUM_OK") {
		t.Fatalf("Stage D 枚举/match/? 执行异常:\ngcc=%q\nclang=%q", gccOut, clangOut)
	}
}

func buildOrderedDropLogBindings() (hdrContent string, bindings map[string]*bindgen.BindingMeta) {
	hdrContent = `
#ifndef DROP_LOG_H
#define DROP_LOG_H
#include <stdint.h>

typedef struct {
    int64_t id;
} TrackRes;

static int64_t g_drop_log[128];
static int64_t g_drop_len = 0;
static int64_t g_live_mask = 0;

static inline TrackRes tr_open(int64_t id) {
    if (id >= 0 && id < 60) {
        g_live_mask |= (INT64_C(1) << id);
    }
    TrackRes r;
    r.id = id;
    return r;
}

static inline void tr_free(TrackRes r) {
    if (g_drop_len < 128) {
        g_drop_log[g_drop_len++] = r.id;
    }
    if (r.id >= 0 && r.id < 60) {
        g_live_mask &= ~(INT64_C(1) << r.id);
    }
}

static inline void tr_take(TrackRes r) {
    if (r.id >= 0 && r.id < 60) {
        g_live_mask &= ~(INT64_C(1) << r.id);
    }
}

static inline int64_t tr_drop_count(void) { return g_drop_len; }
static inline int64_t tr_drop_at(int64_t idx) { return g_drop_log[idx]; }
static inline int64_t tr_live_mask(void) { return g_live_mask; }
static inline void tr_reset_log(void) { g_drop_len = 0; g_live_mask = 0; }
#endif
`
	cat := &bindgen.FFIContractCatalog{
		SchemaVersion: bindgen.SupportedFFIContractSchemaVersion,
		HeaderPath:    "drop_log.h",
		Namespace:     "tr",
		OpaqueTypes: map[string]bindgen.FFIReturnContract{
			"TrackRes": {
				Ownership:    bindgen.FFIOpaqueHandle,
				TypeName:     "TrackRes",
				ReleaseFn:    "tr::tr_free",
				BorrowsParam: -1,
			},
		},
		Functions: map[string]bindgen.FFIFuncContract{
			"tr_open": {
				Namespace: "tr", Function: "tr_open", ContractID: "tr.tr_open",
				Params: []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "id", Ownership: bindgen.FFINonOwningRaw}},
				Return: bindgen.FFIReturnContract{Ownership: bindgen.FFIOwnedReturn, TypeName: "TrackRes", ReleaseFn: "tr::tr_free", BorrowsParam: -1},
			},
			"tr_take": {
				Namespace: "tr", Function: "tr_take", ContractID: "tr.tr_take",
				Params: []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "r", Ownership: bindgen.FFIOwnedTransfer}},
				Return: bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
			},
			"tr_free": {
				Namespace: "tr", Function: "tr_free", ContractID: "tr.tr_free",
				Params: []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "r", Ownership: bindgen.FFIOwnedTransfer}},
				Return: bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
			},
			"tr_drop_count": {Namespace: "tr", Function: "tr_drop_count", ContractID: "tr.tr_drop_count", Return: bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1}},
			"tr_drop_at":    {Namespace: "tr", Function: "tr_drop_at", ContractID: "tr.tr_drop_at", Params: []bindgen.FFIParamContract{{ParamIndex: 0, ParamName: "idx", Ownership: bindgen.FFINonOwningRaw}}, Return: bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1}},
			"tr_live_mask":  {Namespace: "tr", Function: "tr_live_mask", ContractID: "tr.tr_live_mask", Return: bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1}},
			"tr_reset_log":  {Namespace: "tr", Function: "tr_reset_log", ContractID: "tr.tr_reset_log", Return: bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1}},
		},
	}
	if err := cat.Validate(); err != nil {
		panic(err)
	}
	meta := &bindgen.BindingMeta{
		HeaderPath: "drop_log.h",
		Contracts:  cat,
		Functions: map[string]bindgen.FunctionSig{
			"tr_open":       {Name: "tr_open", ReturnType: "TrackRes", ParamTypes: []string{"int64_t"}},
			"tr_take":       {Name: "tr_take", ReturnType: "void", ParamTypes: []string{"TrackRes"}},
			"tr_free":       {Name: "tr_free", ReturnType: "void", ParamTypes: []string{"TrackRes"}},
			"tr_drop_count": {Name: "tr_drop_count", ReturnType: "int64_t"},
			"tr_drop_at":    {Name: "tr_drop_at", ReturnType: "int64_t", ParamTypes: []string{"int64_t"}},
			"tr_live_mask":  {Name: "tr_live_mask", ReturnType: "int64_t"},
			"tr_reset_log":  {Name: "tr_reset_log", ReturnType: "void"},
		},
	}
	return hdrContent, map[string]*bindgen.BindingMeta{"tr": meta}
}

func TestC11StageE_AggregateDropOrderPartialMoveEnumInactiveAndTryCleanup(t *testing.T) {
	hdrContent, bindings := buildOrderedDropLogBindings()
	hdrDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(hdrDir, "drop_log.h"), []byte(hdrContent), 0o600); err != nil {
		t.Fatal(err)
	}

	src := `
#import "drop_log.h"

struct FirstLowAlign:
    tag: u8
    r: TrackRes

struct SecondHighAlign:
    r: TrackRes

# 外层通过 OrderedHolder 验证结构体字段严格按声明序 0 -> 1 析构，并用 partial move / enum / tuple / array 覆盖全场景
struct OrderedHolder:
    var r1: TrackRes
    pad: u64
    r2: TrackRes

enum ResChoice:
    Empty
    One(TrackRes)
    Two:
        x: TrackRes
        y: TrackRes

fn fail_on(b: bool) -> Result[TrackRes, i32]:
    if b:
        return Result::Err(-1)
    let h = unsafe:
        tr::tr_open(5)
    return Result::Ok(h)

fn partial_agg_try(should_fail: bool) -> Result[OrderedHolder, i32]:
    let h1 = unsafe:
        tr::tr_open(3)
    let r = OrderedHolder {
        r1: h1,
        pad: 99u64,
        r2: fail_on(should_fail)?,
    }
    return Result::Ok(r)

fn drop_order_scope():
    let h1 = unsafe:
        tr::tr_open(10)
    let h2 = unsafe:
        tr::tr_open(20)
    let h = OrderedHolder {
        r1: h1,
        pad: 0u64,
        r2: h2,
    }
    let _p = h.pad

fn partial_move_and_reinit():
    let h1 = unsafe:
        tr::tr_open(1)
    let h2 = unsafe:
        tr::tr_open(2)
    var h = OrderedHolder {
        r1: h1,
        pad: 0u64,
        r2: h2,
    }
    unsafe:
        tr::tr_take(h.r1)
    let h4 = unsafe:
        tr::tr_open(4)
    h.r1 = h4

fn tuple_and_array_drop():
    let a = unsafe:
        tr::tr_open(30)
    let b = unsafe:
        tr::tr_open(31)
    let tup = (a, b)
    let c = unsafe:
        tr::tr_open(40)
    let d = unsafe:
        tr::tr_open(41)
    let arr: [TrackRes; 2] = [c, d]
    let _keep = (tup, arr)

fn enum_inactive_never_dropped(mode: i32):
    let e = if mode == 0:
        ResChoice::Empty
    elif mode == 1:
        let r50 = unsafe:
            tr::tr_open(50)
        ResChoice::One(r50)
    else:
        let r51 = unsafe:
            tr::tr_open(51)
        let r52 = unsafe:
            tr::tr_open(52)
        ResChoice::Two { x: r51, y: r52 }
    match e:
        ResChoice::Empty =>
            println("EMPTY")
        ResChoice::One(a) =>
            unsafe:
                tr::tr_take(a)
        ResChoice::Two { x, y } =>
            let _x = x
            let _y = y

fn main() -> i32:
    unsafe:
        tr::tr_reset_log()
    drop_order_scope()
    let c1 = unsafe:
        tr::tr_drop_count()
    let d0 = unsafe:
        tr::tr_drop_at(0)
    let d1 = unsafe:
        tr::tr_drop_at(1)
    if c1 != 2 or d0 != 10 or d1 != 20:
        return 1

    unsafe:
        tr::tr_reset_log()
    partial_move_and_reinit()
    let c2 = unsafe:
        tr::tr_drop_count()
    let p0 = unsafe:
        tr::tr_drop_at(0)
    let p1 = unsafe:
        tr::tr_drop_at(1)
    if c2 != 2 or p0 != 4 or p1 != 2:
        return 2

    unsafe:
        tr::tr_reset_log()
    tuple_and_array_drop()
    let c3 = unsafe:
        tr::tr_drop_count()
    if c3 != 4:
        return 3
    let t0 = unsafe:
        tr::tr_drop_at(0)
    let t1 = unsafe:
        tr::tr_drop_at(1)
    let a0 = unsafe:
        tr::tr_drop_at(2)
    let a1 = unsafe:
        tr::tr_drop_at(3)
    if t0 != 30 or t1 != 31 or a0 != 40 or a1 != 41:
        return 4

    unsafe:
        tr::tr_reset_log()
    enum_inactive_never_dropped(0)
    let ec0 = unsafe:
        tr::tr_drop_count()
    if ec0 != 0:
        return 5
    enum_inactive_never_dropped(1)
    let ec1 = unsafe:
        tr::tr_drop_count()
    if ec1 != 0:
        return 6
    enum_inactive_never_dropped(2)
    let ec2 = unsafe:
        tr::tr_drop_count()
    if ec2 != 2:
        return 7

    unsafe:
        tr::tr_reset_log()
    let r_fail = partial_agg_try(true)
    match r_fail:
        Result::Ok(_) =>
            return 8
        Result::Err(_) =>
            println("TRY_ERR_OK")
    let fc = unsafe:
        tr::tr_drop_count()
    let fa0 = unsafe:
        tr::tr_drop_at(0)
    if fc != 1 or fa0 != 3:
        return 9

    let live = unsafe:
        tr::tr_live_mask()
    if live != 0:
        return 10
    println("DROP_STAGE_E_PASS")
    return 0
`
	tirProg, _ := lowerSourceToTIR(t, src, bindings)
	cCode, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)
	if err != nil {
		t.Fatalf("Stage E C11 生成失败: %v", err)
	}
	gccOut, clangOut := compileStrictGCCAndClang(t, cCode, []string{"-I", hdrDir}, nil)
	if gccOut != clangOut || !strings.Contains(gccOut, "DROP_STAGE_E_PASS") {
		t.Fatalf("Stage E 析构顺序/部分移动/枚举/try 验证失败:\ngcc=%q\nclang=%q", gccOut, clangOut)
	}

	// 若本机 GCC 支持 ASan + UBSan，追加运行净室检查
	tmp := t.TempDir()
	cFile := filepath.Join(tmp, "asan_drop.c")
	binFile := filepath.Join(tmp, "asan_drop")
	if err := os.WriteFile(cFile, []byte(cCode), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("gcc", "-std=c11", "-fsanitize=address,undefined", "-fno-omit-frame-pointer", "-g", "-I", hdrDir, cFile, "-o", binFile)
	if out, err := cmd.CombinedOutput(); err == nil {
		runCmd := exec.Command(binFile)
		if runOut, rerr := runCmd.CombinedOutput(); rerr != nil {
			t.Fatalf("ASan+UBSan 运行失败: %v\n%s", rerr, runOut)
		}
	} else {
		t.Logf("跳过 ASan+UBSan（编译器运行时未启用: %v: %s）", err, strings.TrimSpace(string(out)))
	}
}

func TestC11StageF_StrViewFatLayoutEmptySentinelAndFFIGates(t *testing.T) {
	src := `
struct LabelBox:
    tag: u8
    text: &str

fn echo_str(s: &str) -> &str:
    return s

fn main() -> i32:
    let empty: &str = ""
    if not empty.is_empty() or empty.len() != 0:
        return 1
    let full = echo_str("won-str-2c21")
    if full.is_empty() or full.len() != 12:
        return 2
    let box = LabelBox { tag: 5u8, text: full }
    if box.text.len() != 12:
        return 3
    let raw = box.text.as_ptr()
    let raw2 = box.text.as_bytes().as_ptr()
    if not bytes_equal(raw, raw2, box.text.len() as u64):
        return 4
    println("STR=", empty, "|", box.text, " LEN=", box.text.len())
    return 0
`
	tirProg, _ := lowerSourceToTIR(t, src, nil)
	cCode, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)
	if err != nil {
		t.Fatalf("Stage F &str C11 生成失败: %v", err)
	}
	if !strings.Contains(cCode, "_won_empty_bytes") || !strings.Contains(cCode, "_won_str_t") {
		t.Fatalf("生成的 C11 必须包含 _won_str_t 与空串哨兵 _won_empty_bytes:\n%s", cCode)
	}
	gccOut, clangOut := compileStrictGCCAndClang(t, cCode, nil, nil)
	if gccOut != clangOut || !strings.Contains(gccOut, "STR=|won-str-2c21 LEN=12") {
		t.Fatalf("Stage F &str 执行失败:\ngcc=%q\nclang=%q", gccOut, clangOut)
	}

	// 验证 ADR-012 D4: 禁止将通用 &str 隐式传递至 C const char* FFI 边界
	hdrContent := `#ifndef CSTR_TEST_H
#define CSTR_TEST_H
static inline int take_cstr(const char* p) { return p ? (int)p[0] : -1; }
#endif
`
	hdrDir := t.TempDir()
	_ = hdrDir
	bindings := map[string]*bindgen.BindingMeta{
		"cstr": {
			HeaderPath: "cstr_test.h",
			Functions: map[string]bindgen.FunctionSig{
				"take_cstr": {Name: "take_cstr", ReturnType: "int", ParamTypes: []string{"const char *"}},
			},
		},
	}
	badFFISrc := `
fn main() -> i32:
    let s: &str = "abc"
    let r = unsafe:
        cstr::take_cstr(s)
    return r
`
	_ = hdrContent
	badTIR, _ := lowerSourceToTIR(t, badFFISrc, bindings)
	_, err = c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(badTIR)
	if err == nil {
		t.Fatalf("将通用 &str 隐式传入 C FFI const char* 必须 Fail-Closed 拒绝")
	}
	var capErr *c11.CapabilityError
	if !errors.As(err, &capErr) || capErr.Code != "WON-GEN-002" {
		t.Fatalf("期望 WON-GEN-002，实际: %T %v", err, err)
	}
}

func TestC11DynamicArrayBoundsTrap(t *testing.T) {
	src := `
fn main() -> i32:
    let a: [i32; 3] = [10, 20, 30]
    let idx: usize = 5
    return a[idx]
`
	tirProg, _ := lowerSourceToTIR(t, src, nil)
	cCode, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)
	if err != nil {
		t.Fatalf("C11 生成失败: %v", err)
	}
	tmp := t.TempDir()
	cPath := filepath.Join(tmp, "oob.c")
	binPath := filepath.Join(tmp, "oob")
	if err := os.WriteFile(cPath, []byte(cCode), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("gcc", "-std=c11", "-pedantic-errors", "-Wall", "-Wextra", "-Werror", cPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("GCC 编译失败: %v\n%s", err, out)
	}
	if out, err := exec.Command(binPath).CombinedOutput(); err == nil {
		t.Fatalf("越界访问必须触发 abort() 非零退出，实际正常返回: %s", out)
	}
}

package c11_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/codegen"
	"github.com/KitiLab/WON/compiler/poc/internal/codegen/c11"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
	"github.com/KitiLab/WON/compiler/poc/internal/tir"
	"github.com/KitiLab/WON/compiler/poc/internal/tir/lower"
)

func lowerSourceToTIR(t *testing.T, src string, bindings map[string]*bindgen.BindingMeta) (*tir.Program, *codegen.Generator) {
	t.Helper()
	rep := diagnostic.NewReport()
	lex := lexer.New("c11_test.won", src, rep)
	p := parser.New(lex, rep, "c11_test.won")
	prog := p.ParseProgram()
	if rep.HasErrors() {
		t.Fatalf("语法解析失败:\n%s", rep.String())
	}
	chk := checker.New(rep, bindings)
	tp := chk.CheckProgram(prog)
	if rep.HasErrors() {
		t.Fatalf("语义与所有权检查失败:\n%s", rep.String())
	}
	tirProg, err := lower.LowerProgram(tp, bindings)
	if err != nil {
		t.Fatalf("TIR 降级失败: %v", err)
	}
	return tirProg, codegen.NewTyped(prog, tp, bindings)
}

func compileStrictGCCAndClang(t *testing.T, cCode string, extraFlags []string, extraLibs []string) (gccOut, clangOut string) {
	t.Helper()
	tmpDir := t.TempDir()
	cFile := filepath.Join(tmpDir, "out.c")
	if err := os.WriteFile(cFile, []byte(cCode), 0644); err != nil {
		t.Fatalf("写入 C 文件失败: %v", err)
	}

	flags := append([]string{"-std=c11", "-pedantic-errors", "-Wall", "-Wextra", "-Werror"}, extraFlags...)
	outputs := make([]string, 0, 2)
	for _, cc := range []string{"gcc", "clang"} {
		binPath := filepath.Join(tmpDir, "bin_"+cc)
		args := append(append([]string{}, flags...), cFile, "-o", binPath)
		args = append(args, extraLibs...)
		if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s 严格 ISO C11 编译失败: %v\n输出:\n%s\nC 源码:\n%s", cc, err, string(out), cCode)
		}
		runOut, err := exec.Command(binPath).CombinedOutput()
		if err != nil {
			t.Fatalf("%s 二进制运行失败 (非 0 返回): %v\n输出:\n%s\nC 源码:\n%s", cc, err, string(runOut), cCode)
		}
		outputs = append(outputs, string(runOut))
	}
	return outputs[0], outputs[1]
}

func TestC11MapTypeFailClosedAndTargetGate(t *testing.T) {
	tt := tir.NewTypeTable()
	tI32 := tt.Int(true, 32, false, "i32")
	tI128 := tt.Int(true, 128, false, "i128")
	tUnit := tt.Unit()
	tStr := tt.Str()
	tStrView := tt.Ref(false, tStr)
	tString := tt.StringType()
	tArr := tt.Array(tt.Int(false, 8, false, "u8"), 16)

	valid := []struct {
		id  tir.TypeID
		pos c11.TypePos
		c   string
	}{
		{tUnit, c11.PosReturn, "void"},
		{tI32, c11.PosLocal, "int32_t"},
		{tt.Int(true, 0, true, "isize"), c11.PosParam, "ptrdiff_t"},
		{tt.Int(false, 0, true, "usize"), c11.PosReturn, "size_t"},
		{tt.Float(32), c11.PosLocal, "float"},
		{tt.Float(64), c11.PosReturn, "double"},
		{tt.Bool(), c11.PosLocal, "bool"},
		{tt.Char(), c11.PosLocal, "uint32_t"},
		{tStrView, c11.PosParam, "const char*"},
		{tt.RawPtr(true, tUnit), c11.PosParam, "void*"},
		{tt.RawPtr(false, tUnit), c11.PosParam, "const void*"},
	}
	for _, tc := range valid {
		got, err := c11.MapType(tt, tc.id, tc.pos)
		if err != nil || got != tc.c {
			t.Fatalf("MapType(%s, %d) = (%q, %v), want (%q, nil)", tt.Format(tc.id), tc.pos, got, err, tc.c)
		}
	}

	invalid := []struct {
		id  tir.TypeID
		pos c11.TypePos
	}{
		{tir.InvalidType, c11.PosLocal},
		{tUnit, c11.PosLocal},
		{tUnit, c11.PosParam},
		{tI128, c11.PosLocal},
		{tString, c11.PosLocal},
		{tArr, c11.PosParam},
		{tArr, c11.PosReturn},
	}
	for _, tc := range invalid {
		if got, err := c11.MapType(tt, tc.id, tc.pos); err == nil {
			t.Fatalf("MapType(%s, %d) 必须 Fail-Closed 报错，却返回 %q", tt.Format(tc.id), tc.pos, got)
		}
	}

	// 非法 Target 与未认证 TIR 必须 Fail-Closed
	badTarget := c11.Target{OS: "windows", Arch: "x86_64", PointerBits: 64, LongBits: 32}
	if err := badTarget.Validate(); err == nil {
		t.Fatalf("非 linux/x86_64 LP64 目标必须被拒绝")
	}

	em := c11.NewEmitter(c11.DefaultLinuxX86_64())
	if _, err := em.Emit(&tir.Program{Types: tir.NewTypeTable(), OwnershipOK: false, DropOK: true}); err == nil {
		t.Fatalf("未通过所有权认证的 TIR 必须被 Emit Fail-Closed 拒绝")
	}
}

func TestC11CapabilitiesRejectUnsupportedWithWONGEN002(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"I128Local", "fn main() -> i32:\n    let x: i128 = 1\n    return 0\n"},
		{"U128Return", "fn big() -> u128:\n    return 1\nfn main() -> i32:\n    return 0\n"},
		{"UnitParam", "fn bad(u: unit):\n    return\nfn main() -> i32:\n    return 0\n"},
		{"UnitLocal", "fn main() -> i32:\n    let u: unit = ()\n    return 0\n"},
		{"MainReturnsBool", "fn main() -> bool:\n    return true\n"},
		{"MainTakesParam", "fn main(x: i32) -> i32:\n    return x\n"},
		{"FloatModulo", "fn main() -> i32:\n    let x: f64 = 5.0 % 2.0\n    return 0\n"},
		{"StrEquality", "fn main() -> i32:\n    let eq = \"a\" == \"b\"\n    return 0\n"},
		{"StrLen", "fn main() -> i32:\n    let s = \"abc\"\n    let l = s.len()\n    return 0\n"},
		{"StringLocal", "fn main() -> i32:\n    let s = String::from(\"x\")\n    return 0\n"},
		{"StructVar", "struct S:\n    v: i32\nfn main() -> i32:\n    let s = S { v: 1 }\n    return s.v\n"},
		{"EnumMatch", "enum E:\n    A\n    B(i32)\nfn main() -> i32:\n    let e = E::B(1)\n    return match e:\n        E::A => 0\n        E::B(x) => x\n"},
		{"ForLoop", "fn main() -> i32:\n    for i in 0..3:\n        return 0\n    return 0\n"},
		{"UnitLiteralReturn", "fn main() -> unit:\n    return ()\n"},
		{"CastOp", "fn main() -> i32:\n    let x: i64 = 1\n    return x as i32\n"},
	}

	// 注意：CastOp 是实际支持 `as` 数值转换还是被门控？若 TIR 已支持 OpCast 让我们看情况分开测试；此处先只测试明确未实现特性
	cases = cases[:len(cases)-1]

	em := c11.NewEmitter(c11.DefaultLinuxX86_64())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tirProg, _ := lowerSourceToTIR(t, tc.src, nil)
			_, err := em.Emit(tirProg)
			if err == nil {
				t.Fatalf("用例 %s 应当被 TIR->C11 能力网关拦截 (WON-GEN-002)，却生成成功", tc.name)
			}
			var capErr *c11.CapabilityError
			if !errors.As(err, &capErr) || capErr.Code != "WON-GEN-002" {
				t.Fatalf("用例 %s 期望 CapabilityError{WON-GEN-002}，实际: %T %v", tc.name, err, err)
			}
		})
	}
}

func TestC11PrimitivesControlFlowShadowingAndDifferentialVsLegacy(t *testing.T) {
	src := `
fn fib(n: i32) -> i32:
    if n <= 1:
        return n
    return fib(n - 1) + fib(n - 2)

fn sum_while(limit: i32) -> i32:
    var i: i32 = 0
    var acc: i32 = 0
    while i < limit:
        i = i + 1
        if i == 3:
            continue
        if i == 7:
            break
        acc += i
    return acc

fn short_circuit(a: bool, b: bool) -> bool:
    return (a and b) or (not a and not b)

fn boundary_check(a: i8, d: u64, is_min: isize, us_max: usize) -> i32:
    if a != -128:
        return 1
    if d != 18446744073709551615:
        return 2
    if is_min != -9223372036854775808 or us_max != 18446744073709551615:
        return 3
    return 0

fn main() -> i32:
    let a_min: i8 = -128
    let d_max: u64 = 18446744073709551615u64
    let is_min: isize = -9223372036854775808
    let us_max: usize = 18446744073709551615
    if boundary_check(a_min, d_max, is_min, us_max) != 0:
        return 10

    let x: i32 = 10
    if true:
        let x: i32 = 20
        if x != 20:
            return 11
    if x != 10:
        return 12

    let f = fib(7)
    let w = sum_while(10)
    let sc1 = short_circuit(true, true)
    let sc2 = short_circuit(false, true)
    println("RES ", f, " ", w, " ", sc1, " ", sc2)
    if f == 13 and w == 18 and sc1 and not sc2:
        return 0
    return 20
`
	tirProg, legacyGen := lowerSourceToTIR(t, src, nil)
	cNew, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)
	if err != nil {
		t.Fatalf("TIR->C11 发射失败: %v", err)
	}
	cOld, err := legacyGen.Generate()
	if err != nil {
		t.Fatalf("参考 AST 生成器失败: %v", err)
	}

	if !strings.Contains(cNew, "INT8_MIN") || !strings.Contains(cNew, "PTRDIFF_MIN") || !strings.Contains(cNew, "UINT64_C(18446744073709551615)") {
		t.Fatalf("C11 输出缺少精确整数边界宏:\n%s", cNew)
	}
	if strings.Contains(cNew, "({") || strings.Contains(cNew, "})") {
		t.Fatalf("C11 输出包含非标准 GNU 语句表达式:\n%s", cNew)
	}

	gNew, clNew := compileStrictGCCAndClang(t, cNew, nil, nil)
	gOld, clOld := compileStrictGCCAndClang(t, cOld, nil, nil)
	if gNew != clNew || gNew != gOld || gNew != clOld {
		t.Fatalf("差分测试输出不一致:\nTIR(gcc)=%q\nTIR(clang)=%q\nLegacy(gcc)=%q\nLegacy(clang)=%q", gNew, clNew, gOld, clOld)
	}
	if !strings.Contains(gNew, "RES 13 18 true false") {
		t.Fatalf("未匹配预期运行时输出: %q", gNew)
	}
}

func TestC11RealZlibRoundTripFromTIR(t *testing.T) {
	content, err := os.ReadFile("../../../../../examples/zlib_compression.won")
	if err != nil {
		t.Fatalf("读取 zlib_compression.won 失败: %v", err)
	}
	meta, err := bindgen.ExtractZlibBindings("/usr/include/zlib.h")
	if err != nil {
		t.Fatalf("提取 zlib 绑定失败: %v", err)
	}
	bindings := map[string]*bindgen.BindingMeta{"zlib": meta}

	tirProg, legacyGen := lowerSourceToTIR(t, string(content), bindings)
	cNew, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)
	if err != nil {
		t.Fatalf("zlib TIR->C11 发射失败: %v", err)
	}
	cOld, err := legacyGen.Generate()
	if err != nil {
		t.Fatalf("zlib legacy 生成失败: %v", err)
	}

	gNew, clNew := compileStrictGCCAndClang(t, cNew, nil, []string{"-lz"})
	gOld, _ := compileStrictGCCAndClang(t, cOld, nil, []string{"-lz"})
	if gNew != clNew || gNew != gOld {
		t.Fatalf("zlib 差分执行输出不一致:\nTIR=%q\nLegacy=%q", gNew, gOld)
	}
	if !strings.Contains(gNew, "Zlib round-trip verification: PASS") || !strings.Contains(gNew, "Byte-for-byte verification: PASS") {
		t.Fatalf("zlib 缺少通过输出:\n%s", gNew)
	}
}

func TestC11Phase2C1_1_ReturnCompletenessAndDefenseInDepth(t *testing.T) {
	// 8. 合法非 unit 返回生成正确的 C return 表达式
	t.Run("valid_non_unit_return_emits_exact_expr", func(t *testing.T) {
		tt := tir.NewTypeTable()
		tI32 := tt.Int(true, 32, false, "i32")
		p := &tir.Program{
			Types:       tt,
			OwnershipOK: true,
			DropOK:      true,
			Functions: []*tir.Function{{
				ID: 1, Name: "main", CallingConv: "WON", ReturnType: tI32, EntryBlock: 0, ExitBlock: 0,
				Blocks: []*tir.BasicBlock{{
					ID:         0,
					Label:      "entry",
					Terminator: tir.Terminator{Kind: tir.TermReturn, ReturnVal: &tir.Operand{Kind: tir.OperandConst, Type: tI32, Const: tir.Constant{Kind: tir.ConstInt, IntText: "42"}}},
				}},
			}},
		}
		cCode, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(p)
		if err != nil {
			t.Fatalf("合法非 unit TIR 发射失败: %v", err)
		}
		if !strings.Contains(cCode, "return INT32_C(42);") {
			t.Fatalf("期望发射精确返回表达式 INT32_C(42)，实际:\n%s", cCode)
		}
	})

	// 9 & 10. 畸形非 unit 缺失 ReturnVal 在发射前被拒绝，绝不伪造 return 0;
	t.Run("malformed_non_unit_missing_return_rejected_no_zero_synth", func(t *testing.T) {
		tt := tir.NewTypeTable()
		tI32 := tt.Int(true, 32, false, "i32")
		p := &tir.Program{
			Types:       tt,
			OwnershipOK: true,
			DropOK:      true,
			Functions: []*tir.Function{{
				ID: 1, Name: "main", CallingConv: "WON", ReturnType: tI32, EntryBlock: 0, ExitBlock: 0,
				Blocks: []*tir.BasicBlock{{
					ID:         0,
					Label:      "entry",
					Terminator: tir.Terminator{Kind: tir.TermReturn, ReturnVal: nil},
				}},
			}},
		}
		cCode, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(p)
		if err == nil {
			t.Fatalf("缺失 ReturnVal 的非 unit TIR 必须被 Emit 拒绝，却伪造生成 C 代码:\n%s", cCode)
		}
		var capErr *c11.CapabilityError
		if !errors.As(err, &capErr) || capErr.Code != "WON-INT-001" {
			t.Fatalf("期望 CapabilityError{WON-INT-001}，实际: %T %v", err, err)
		}
		if strings.Contains(cCode, "return 0;") {
			t.Fatalf("失败时不得返回含合成 return 0; 的部分 C 代码:\n%s", cCode)
		}
	})

	// 11 & 12. 合法 unit 裸 return 与 unit 尾表达式严格只求值一次且保持有效 C return;
	t.Run("unit_bare_return_and_single_eval_tail_preserved", func(t *testing.T) {
		hdrDir := t.TempDir()
		hdr := `
static int g_eval_ticks = 0;
static inline void tick_once(void) { g_eval_ticks++; }
static inline int get_ticks(void) { return g_eval_ticks; }
`
		if err := os.WriteFile(filepath.Join(hdrDir, "tick_unit.h"), []byte(hdr), 0644); err != nil {
			t.Fatalf("写入测试头文件失败: %v", err)
		}
		bindings := map[string]*bindgen.BindingMeta{
			"tick_unit": {
				HeaderPath: "tick_unit.h",
				Functions: map[string]bindgen.FunctionSig{
					"tick_once": {Name: "tick_once", ReturnType: "void"},
					"get_ticks": {Name: "get_ticks", ReturnType: "int"},
				},
			},
		}
		src := `
#import "tick_unit.h"

fn bare():
    return

fn tail_expr() -> unit:
    unsafe:
        tick_unit::tick_once()

fn delegated() -> unit:
    return unsafe:
        tick_unit::tick_once()

fn main() -> unit:
    bare()
    tail_expr()
    delegated()
    let total = unsafe:
        tick_unit::get_ticks()
    println("TICKS=", total)
`
		tirProg, _ := lowerSourceToTIR(t, src, bindings)
		cCode, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)
		if err != nil {
			t.Fatalf("合法 unit 程序 C11 发射失败: %v", err)
		}
		if strings.Contains(cCode, "return tick_once()") {
			t.Fatalf("void 函数不得带值 return:\n%s", cCode)
		}
		gOut, clOut := compileStrictGCCAndClang(t, cCode, []string{"-I", hdrDir}, nil)
		if gOut != clOut || !strings.Contains(gOut, "TICKS=2") {
			t.Fatalf("期望 TICKS=2（每个 unit 调用求值恰好一次），实际 gcc=%q clang=%q", gOut, clOut)
		}
	})
}

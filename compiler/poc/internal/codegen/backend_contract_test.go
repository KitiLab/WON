package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

func compileWithGCCAndClangStrict(t *testing.T, cCode string, runExec bool) {
	t.Helper()
	tmpDir := t.TempDir()
	cFile := filepath.Join(tmpDir, "out.c")
	if err := os.WriteFile(cFile, []byte(cCode), 0644); err != nil {
		t.Fatalf("写入 C 文件失败: %v", err)
	}

	flags := []string{"-std=c11", "-pedantic-errors", "-Wall", "-Wextra", "-Werror"}
	for _, cc := range []string{"gcc", "clang"} {
		binPath := filepath.Join(tmpDir, "bin_"+cc)
		args := append(append([]string{}, flags...), cFile, "-o", binPath)
		out, err := exec.Command(cc, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s 严格 ISO C11 编译失败: %v\n输出:\n%s\n源码:\n%s", cc, err, string(out), cCode)
		}
		if runExec {
			runOut, err := exec.Command(binPath).CombinedOutput()
			if err != nil {
				t.Fatalf("%s 编译产物执行失败 (非 0 返回): %v\n输出:\n%s\n源码:\n%s", cc, err, string(runOut), cCode)
			}
		}
	}
}

func checkAndGen(t *testing.T, src string) (string, *checker.TypedProgram) {
	t.Helper()
	rep := diagnostic.NewReport()
	lex := lexer.New("backend_test.won", src, rep)
	p := parser.New(lex, rep, "backend_test.won")
	prog := p.ParseProgram()
	if rep.HasErrors() {
		t.Fatalf("解析失败:\n%s", rep.String())
	}
	chk := checker.New(rep, nil)
	tp := chk.CheckProgram(prog)
	if rep.HasErrors() {
		t.Fatalf("语义校验失败:\n%s", rep.String())
	}
	if !tp.CodegenSupported {
		t.Fatalf("期望支持 C11 代码生成，但被拒止: %s", tp.UnsupportedReason)
	}
	gen := NewTyped(prog, tp, nil)
	cCode, err := gen.Generate()
	if err != nil {
		t.Fatalf("C 代码生成失败: %v", err)
	}
	return cCode, tp
}

func TestMapTypeToCFailClosedAndPositionalRules(t *testing.T) {
	ctx := types.NewContext()

	valid := []struct {
		id  types.TypeID
		pos TypePos
		c   string
	}{
		{ctx.I8, PosLocal, "int8_t"},
		{ctx.I16, PosParam, "int16_t"},
		{ctx.I32, PosReturn, "int32_t"},
		{ctx.I64, PosLocal, "int64_t"},
		{ctx.U8, PosLocal, "uint8_t"},
		{ctx.U16, PosParam, "uint16_t"},
		{ctx.U32, PosReturn, "uint32_t"},
		{ctx.U64, PosLocal, "uint64_t"},
		{ctx.ISize, PosLocal, "ptrdiff_t"},
		{ctx.USize, PosReturn, "size_t"},
		{ctx.F32, PosLocal, "float"},
		{ctx.F64, PosReturn, "double"},
		{ctx.Bool, PosLocal, "bool"},
		{ctx.Char, PosLocal, "uint32_t"},
		{ctx.Unit, PosReturn, "void"},
		{ctx.StrView, PosParam, "const char*"},
		{ctx.RawPtr(false, ctx.I32), PosParam, "const int32_t*"},
		{ctx.RawPtr(true, ctx.U64), PosParam, "uint64_t*"},
		{ctx.Ref(false, ctx.I32), PosParam, "const int32_t*"},
		{ctx.Ref(true, ctx.U8), PosParam, "uint8_t*"},
	}

	for _, tc := range valid {
		got, err := MapTypeToC(ctx, tc.id, tc.pos)
		if err != nil || got != tc.c {
			t.Errorf("MapTypeToC(%s, %d) = (%q, %v), want (%q, nil)", ctx.Format(tc.id), tc.pos, got, err, tc.c)
		}
	}

	invalid := []struct {
		id  types.TypeID
		pos TypePos
	}{
		{types.Invalid, PosLocal},
		{ctx.Unit, PosLocal},
		{ctx.Unit, PosParam},
		{ctx.Unit, PosValue},
		{ctx.I128, PosLocal},
		{ctx.U128, PosParam},
		{ctx.I128, PosReturn},
		{ctx.StringT, PosLocal},
		{ctx.Array(ctx.U8, 8), PosParam},
		{ctx.Array(ctx.U8, 8), PosReturn},
		{ctx.Tuple([]types.TypeID{ctx.I32, ctx.Bool}), PosReturn},
	}

	for _, tc := range invalid {
		if got, err := MapTypeToC(ctx, tc.id, tc.pos); err == nil {
			t.Errorf("MapTypeToC(%s, %d) 必须报错 fail-closed，却返回 %q", ctx.Format(tc.id), tc.pos, got)
		}
	}
}

func TestBackendIntegerBoundaryConstantEmissionGCCAndClang(t *testing.T) {
	src := `
fn check_params(a: i8, b: u8, c: i64, d: u64, e: isize, f: usize) -> i32:
    if a != -128 or b != 255:
        return 1
    if c != -9223372036854775808 or d != 18446744073709551615:
        return 2
    if e != -9223372036854775808 or f != 18446744073709551615:
        return 3
    return 0

fn main() -> i32:
    let a_min: i8 = -128
    let a_max: i8 = 127
    let b_min: i16 = -32768
    let b_max: i16 = 32767
    let c_min: i32 = -2147483648
    let c_max: i32 = 2147483647
    let d_min = -9223372036854775808i64
    let d_max = 9223372036854775807i64

    let ua: u8 = 255
    let ub: u16 = 65535
    let uc = 4_294_967_295u32
    let ud = 18446744073709551615u64
    let uh: u64 = 0xffff_ffff_ffff_ffff

    let is_min: isize = -9223372036854775808
    let us_max: usize = 18446744073709551615

    if a_min + a_max != -1 or b_min + b_max != -1 or c_min + c_max != -1:
        return 10
    if d_min >= d_max:
        return 12
    if ua != 255 or ub != 65535 or uc != 4294967295 or ud != uh:
        return 11
    return check_params(a_min, ua, d_min, ud, is_min, us_max)
`
	cCode, _ := checkAndGen(t, src)

	for _, wantMacro := range []string{
		"INT8_MIN",
		"INT16_MIN",
		"INT32_MIN",
		"INT64_MIN",
		"PTRDIFF_MIN",
		"UINT64_C(18446744073709551615)",
		"((uint8_t)UINT8_C(255))",
	} {
		if !strings.Contains(cCode, wantMacro) {
			t.Errorf("期望生成的 C 源码包含安全宽度宏 '%s'，实际 C:\n%s", wantMacro, cCode)
		}
	}

	compileWithGCCAndClangStrict(t, cCode, true)
}

func TestBackendSupportedPrimitivesAndUnitMainABI(t *testing.T) {
	unitMainSrc := `
fn add_f64(x: f64, y: f64) -> f64:
    return x + y

fn toggle(b: bool) -> bool:
    return not b

fn take_char(c: char) -> char:
    return c

fn main():
    let f1: f32 = 1.5
    let f2: f64 = add_f64(2.5, 3.5)
    let ok = toggle(false)
    let ch = take_char('A')
    var arr: [u8; 4] = [65; 4]
    let len = arr.len()
    let same = bytes_equal(arr.as_ptr(), arr.as_ptr(), len)
    if f1 > 1.0 and f2 > 5.0 and ok and ch == 'A' and len == 4 and same:
        println("ok unit main")
`
	cCode, _ := checkAndGen(t, unitMainSrc)
	if strings.Contains(cCode, "void main(") {
		t.Fatalf("严格 ISO C11 禁止发射 void main():\n%s", cCode)
	}
	if !strings.Contains(cCode, "static void won_user_main(void)") || !strings.Contains(cCode, "int main(void)") {
		t.Fatalf("unit 返回 main 必须使用 won_user_main + int main(void) 包装:\n%s", cCode)
	}

	compileWithGCCAndClangStrict(t, cCode, true)
}

func TestBackendUnsupportedCasesRejectBeforeCodegen(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"I128Local", "fn main() -> i32:\n    let x: i128 = 1\n    return 0\n"},
		{"U128Return", "fn big() -> u128:\n    return 1\nfn main() -> i32:\n    return 0\n"},
		{"UnitParam", "fn bad(u: unit):\n    return\nfn main() -> i32:\n    return 0\n"},
		{"UnitLocal", "fn main() -> i32:\n    let u: unit = ()\n    return 0\n"},
		{"MainReturnsBool", "fn main() -> bool:\n    return true\n"},
		{"MainReturnsU64", "fn main() -> u64:\n    return 0\n"},
		{"MainTakesParam", "fn main(x: i32) -> i32:\n    return x\n"},
		{"FloatModulo", "fn main() -> i32:\n    let x: f64 = 5.0 % 2.0\n    return 0\n"},
		{"StrEqualityUnsupported", "fn main() -> i32:\n    let same = \"a\" == \"b\"\n    return 0\n"},
		{"StrViewLenUnsupported", "fn main() -> i32:\n    let s = \"abc\"\n    let l = s.len()\n    return 0\n"},
		{"StringLocal", "fn main() -> i32:\n    let s = String::from(\"x\")\n    return 0\n"},
		{"StructDecl", "struct S:\n    v: i32\nfn main() -> i32:\n    return 0\n"},
		{"EnumDecl", "enum E:\n    A\nfn main() -> i32:\n    return 0\n"},
		{"ArrayReturn", "fn arr() -> [u8; 4]:\n    var b: [u8; 4] = [0; 4]\n    return b\nfn main() -> i32:\n    return 0\n"},
		{"SliceParam", "fn take(s: &[u8]):\n    return\nfn main() -> i32:\n    return 0\n"},
		{"ForLoop", "fn main() -> i32:\n    for i in 0..3:\n        return i as i32\n    return 0\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := diagnostic.NewReport()
			lex := lexer.New("gate_test.won", tc.src, rep)
			p := parser.New(lex, rep, "gate_test.won")
			prog := p.ParseProgram()
			if rep.HasErrors() {
				t.Fatalf("语法解析失败:\n%s", rep.String())
			}
			chk := checker.New(rep, nil)
			tp := chk.CheckProgram(prog)
			if rep.HasErrors() {
				t.Fatalf("期望用例通过 wonc check 语义分析后再在后端拒止，却在 check 报错:\n%s", rep.String())
			}
			if tp.CodegenSupported {
				t.Fatalf("用例 %s 本应在后端拒止，却标记 CodegenSupported = true", tc.name)
			}
			if tp.UnsupportedReason == "" || tp.UnsupportedCodegen.Start.Line < 1 {
				t.Errorf("用例 %s 缺少明确的拒止原因或源码位置: reason=%q span=%+v", tc.name, tp.UnsupportedReason, tp.UnsupportedCodegen)
			}
		})
	}
}

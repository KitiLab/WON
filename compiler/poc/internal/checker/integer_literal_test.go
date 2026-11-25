package checker

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
)

func TestFullWidthIntegerLiteralBoundariesValid(t *testing.T) {
	src := `
fn main():
    # 有符号整型精确极值 (min & max)
    let min_i8: i8 = -128
    let max_i8: i8 = 127
    let min_i8_s = -128i8

    let min_i16: i16 = -32768
    let max_i16: i16 = 32767

    let min_i32: i32 = -2147483648
    let max_i32: i32 = 2147483647

    let min_i64: i64 = -9223372036854775808
    let max_i64: i64 = 9223372036854775807

    let min_i128: i128 = -170141183460469231731687303715884105728
    let max_i128: i128 = 170141183460469231731687303715884105727

    let min_isize: isize = -9223372036854775808
    let max_isize: isize = 9223372036854775807

    # 无符号整型精确极值 (0 .. 2^N - 1)
    let max_u8: u8 = 255
    let max_u16: u16 = 65535
    let max_u32: u32 = 4294967295
    let max_u64: u64 = 18446744073709551615
    let max_u128: u128 = 340282366920938463463374607431768211455
    let max_usize: usize = 18446744073709551615

    # 多进制、下划线分隔符与后缀组合
    let hex_u128: u128 = 0xffff_ffff_ffff_ffff_ffff_ffff_ffff_ffff
    let hex_suff = 0xFFu8
    let oct_val: u16 = 0o17_7777
    let bin_val: u8 = 0b1111_1111
    let suff_u64 = 18446744073709551615u64
`
	tp, report := runCheckSource(t, src)
	if report.HasErrors() {
		t.Fatalf("合法全宽整数极值及格式用例不应报错:\n%s", report.String())
	}
	if tp.CodegenSupported {
		t.Errorf("包含 i128/u128 类型的程序必须显式阻断 C11 代码生成 (CodegenSupported == false)")
	}
}

func TestIntegerLiteralOverflowsAndInvalidNegativeAssignments(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"AboveMaxI8", "fn main():\n    let x: i8 = 128\n"},
		{"BelowMinI8", "fn main():\n    let x: i8 = -129\n"},
		{"AboveMaxI16", "fn main():\n    let x: i16 = 32768\n"},
		{"BelowMinI16", "fn main():\n    let x: i16 = -32769\n"},
		{"AboveMaxI32", "fn main():\n    let x: i32 = 2147483648\n"},
		{"BelowMinI32", "fn main():\n    let x: i32 = -2147483649\n"},
		{"AboveMaxI64", "fn main():\n    let x: i64 = 9223372036854775808\n"},
		{"BelowMinI64", "fn main():\n    let x: i64 = -9223372036854775809\n"},
		{"AboveMaxI128", "fn main():\n    let x: i128 = 170141183460469231731687303715884105728\n"},
		{"BelowMinI128", "fn main():\n    let x: i128 = -170141183460469231731687303715884105729\n"},
		{"AboveMaxU8", "fn main():\n    let x: u8 = 256\n"},
		{"AboveMaxU16", "fn main():\n    let x: u16 = 65536\n"},
		{"AboveMaxU32", "fn main():\n    let x: u32 = 4294967296\n"},
		{"AboveMaxU64", "fn main():\n    let x: u64 = 18446744073709551616\n"},
		{"AboveMaxU128", "fn main():\n    let x: u128 = 340282366920938463463374607431768211456\n"},
		{"NegativeU8", "fn main():\n    let x: u8 = -1\n"},
		{"NegativeU64", "fn main():\n    let x: u64 = -1\n"},
		{"NegativeU128", "fn main():\n    let x: u128 = -1\n"},
		{"DefaultI64OverflowHexU128", "fn main():\n    let x = 0xffffffffffffffffffffffffffffffff\n"},
		{"BinBoundaryOverflow", "fn main():\n    let x: u8 = 0b1_0000_0000\n"},
		{"OctBoundaryOverflow", "fn main():\n    let x: u8 = 0o400\n"},
		{"SuffixedOverflow", "fn main():\n    let x = 256u8\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, report := runCheckSource(t, tc.src)
			assertHasDiagCode(t, report, "WON-TYP-003")
		})
	}
}

func TestIntegerLiteralParseErrorsNotSilentlyZeroed(t *testing.T) {
	badLiterals := []struct {
		name string
		src  string
	}{
		{"HexMissingDigits", "fn main():\n    let x: u8 = 0x\n"},
		{"InvalidHexSuffix", "fn main():\n    let x: u8 = 0x10g\n"},
		{"InvalidOctalDigit", "fn main():\n    let x: u8 = 0o89\n"},
		{"InvalidBinaryDigit", "fn main():\n    let x: u8 = 0b102\n"},
		{"InvalidIntSuffix", "fn main():\n    let x = 42foo\n"},
	}

	for _, tc := range badLiterals {
		t.Run(tc.name, func(t *testing.T) {
			report := diagnostic.NewReport()
			lex := lexer.New("bad_int.won", tc.src, report)
			p := parser.New(lex, report, "bad_int.won")
			prog := p.ParseProgram()

			assertHasDiagCode(t, report, "WON-SYN-008")

			// 运行语义校验时不得把解析失败的数字静默降级为 0 通过溢出检测，也不应报出无关 WON-TYP-003
			chk := New(report, nil)
			chk.CheckProgram(prog)
			for _, d := range report.Diagnostics {
				if d.Code == "WON-TYP-003" {
					t.Errorf("解析失败的字面量不应再触发伪溢出错误 WON-TYP-003: %+v", d)
				}
			}
		})
	}
}

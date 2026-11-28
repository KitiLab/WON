package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
)

func mockZlibForFixtures() map[string]*bindgen.BindingMeta {
	return map[string]*bindgen.BindingMeta{
		"zlib": {
			HeaderPath: "/usr/include/zlib.h",
			Functions: map[string]bindgen.FunctionSig{
				"compressBound": {
					Name:       "compressBound",
					ReturnType: "unsigned long",
					ParamTypes: []string{"unsigned long"},
				},
				"compress": {
					Name:       "compress",
					ReturnType: "int",
					ParamTypes: []string{"unsigned char *", "unsigned long *", "const unsigned char *", "unsigned long"},
				},
				"uncompress": {
					Name:       "uncompress",
					ReturnType: "int",
					ParamTypes: []string{"unsigned char *", "unsigned long *", "const unsigned char *", "unsigned long"},
				},
			},
		},
	}
}

// TestPhase2ASemanticValidFixtures 验证全部 Phase 2A 规范合法用例均可通过完整 Lexer -> Parser -> Semantic Checker 管线
func TestPhase2ASemanticValidFixtures(t *testing.T) {
	validSemantic := []string{
		"tests/language-spec/valid/basics/hello.won",
		"tests/language-spec/valid/types/struct_and_enum.won",
		"tests/language-spec/valid/ffi/zlib_interop.won",
		"tests/language-spec/valid/names/forward_calls_and_shadowing.won",
		"tests/language-spec/valid/enums/option_result_try.won",
		"tests/language-spec/valid/patterns/match_variants_and_bool.won",
		"tests/language-spec/valid/types/full_width_integers.won",
	}

	bindings := mockZlibForFixtures()
	for _, relPath := range validSemantic {
		t.Run(filepath.Base(relPath), func(t *testing.T) {
			fullPath := filepath.Join("..", "..", "..", relPath)
			content, err := os.ReadFile(fullPath)
			if err != nil {
				t.Fatalf("读取合法夹具失败: %v", err)
			}
			report := diagnostic.NewReport()
			lex := lexer.New(relPath, string(content), report)
			p := parser.New(lex, report, relPath)
			prog := p.ParseProgram()
			if report.HasErrors() {
				t.Fatalf("合法语义夹具 %s 解析阶段意外报错:\n%s", relPath, report.String())
			}
			chk := checker.New(report, bindings)
			tp := chk.CheckProgram(prog)
			if report.HasErrors() {
				t.Fatalf("合法语义夹具 %s 语义校验报错:\n%s", relPath, report.String())
			}
			if tp == nil || tp.Types == nil || tp.Symbols == nil {
				t.Fatalf("CheckProgram 未返回完整的 TypedProgram 语义模型")
			}
		})
	}
}

// TestPhase2ASemanticInvalidFixtures 验证全部语义与 UTF-8 负例在正确编译阶段产出准确诊断码与非零坐标
func TestPhase2ASemanticInvalidFixtures(t *testing.T) {
	cases := []struct {
		file      string
		expectLex bool // 若为 true 则应在词法阶段拦截，否则应无错通过 parse 而在 check 阶段拦截
		wantCodes []string
	}{
		{"tests/language-spec/invalid/utf8/invalid_utf8.won", true, []string{"WON-LEX-004"}},
		{"tests/language-spec/invalid/names/undefined_identifier.won", false, []string{"WON-NAM-001"}},
		{"tests/language-spec/invalid/names/duplicate_declaration.won", false, []string{"WON-NAM-002"}},
		{"tests/language-spec/invalid/names/unknown_type.won", false, []string{"WON-NAM-003"}},
		{"tests/language-spec/invalid/types/implicit_coercion.won", false, []string{"WON-TYP-001"}},
		{"tests/language-spec/invalid/types/non_bool_condition.won", false, []string{"WON-TYP-002"}},
		{"tests/language-spec/invalid/types/integer_literal_overflow.won", false, []string{"WON-TYP-003"}},
		{"tests/language-spec/invalid/types/int_overflow_u128.won", false, []string{"WON-TYP-003"}},
		{"tests/language-spec/invalid/types/unsized_str_by_value.won", false, []string{"WON-TYP-006"}},
		{"tests/language-spec/invalid/types/invalid_method_args.won", false, []string{"WON-TYP-001"}},
		{"tests/language-spec/invalid/structs/missing_and_immutable_field.won", false, []string{"WON-ADT-002", "WON-ADT-004"}},
		{"tests/language-spec/invalid/patterns/non_exhaustive_match.won", false, []string{"WON-MAT-001"}},
		{"tests/language-spec/invalid/patterns/match_incompatible_join.won", false, []string{"WON-MAT-003"}},
		{"tests/language-spec/invalid/errors/mismatched_try_error.won", false, []string{"WON-ERR-001"}},
		{"tests/language-spec/invalid/ffi/foreign_call_outside_unsafe.won", false, []string{"WON-SEC-001"}},
		{"tests/language-spec/invalid/ffi/raw_ptr_deref_outside_unsafe.won", false, []string{"WON-SEC-002"}},
	}

	bindings := mockZlibForFixtures()
	for _, tc := range cases {
		t.Run(filepath.Base(tc.file), func(t *testing.T) {
			fullPath := filepath.Join("..", "..", "..", tc.file)
			content, err := os.ReadFile(fullPath)
			if err != nil {
				t.Fatalf("读取负例夹具失败: %v", err)
			}
			report := diagnostic.NewReport()
			lex := lexer.New(tc.file, string(content), report)
			p := parser.New(lex, report, tc.file)
			prog := p.ParseProgram()

			if tc.expectLex {
				if !report.HasErrors() {
					t.Fatalf("词法负例 %s 应当在词法阶段报错", tc.file)
				}
			} else {
				if report.HasErrors() {
					t.Fatalf("语义负例 %s 应当无错通过 parse 阶段后再在 check 阶段报错，实际 parse 错误:\n%s", tc.file, report.String())
				}
				chk := checker.New(report, bindings)
				chk.CheckProgram(prog)
				if !report.HasErrors() {
					t.Fatalf("语义负例 %s 在 check 阶段未报出预期错误", tc.file)
				}
			}

			gotCodes := make(map[string]bool)
			for _, d := range report.Diagnostics {
				gotCodes[d.Code] = true
				if d.Span.Start.Line < 1 || d.Span.Start.Column < 1 {
					t.Errorf("%s 诊断 %s 缺少有效的源码行列坐标: %+v", tc.file, d.Code, d.Span)
				}
			}
			for _, want := range tc.wantCodes {
				if !gotCodes[want] {
					t.Errorf("%s 期望捕获诊断码 %s，实际诊断:\n%s", tc.file, want, report.String())
				}
			}
		})
	}
}

// TestCLISemanticCheckAndCodegenGate 验证 wonc check 与 wonc build 的阶段性拒止边界
func TestCLISemanticCheckAndCodegenGate(t *testing.T) {
	woncBin := filepath.Join("..", "bin", "wonc")

	// 1. wonc check 在规范合法用例与示例上应返回 0
	for _, rel := range []string{
		"examples/zlib_compression.won",
		"tests/language-spec/valid/basics/hello.won",
		"tests/language-spec/valid/types/struct_and_enum.won",
		"tests/language-spec/valid/types/full_width_integers.won",
		"tests/language-spec/valid/enums/option_result_try.won",
	} {
		out, err := exec.Command(woncBin, "check", filepath.Join("..", "..", "..", rel)).CombinedOutput()
		if err != nil {
			t.Fatalf("wonc check %s 失败: %v\n%s", rel, err, string(out))
		}
	}

	// 1b. unit 返回值的 hello.won 通过 wonc build 生成标准 C11 并正常返回退出码 0
	helloBin := filepath.Join(t.TempDir(), "hello_bin")
	if out, err := exec.Command(woncBin, "build", "-o", helloBin, filepath.Join("..", "..", "..", "tests/language-spec/valid/basics/hello.won")).CombinedOutput(); err != nil {
		t.Fatalf("wonc build hello.won 失败: %v\n%s", err, string(out))
	}
	if runOut, err := exec.Command(helloBin).CombinedOutput(); err != nil || !strings.Contains(string(runOut), "Hello, WON World!") {
		t.Fatalf("hello_bin 运行失败: err=%v out=%s", err, string(runOut))
	}

	// 1c. 验证 Phase 2C.2.1 引入的 Option / Result / 模式匹配 / ? 运算符完整代码生成与执行
	optBin := filepath.Join(t.TempDir(), "opt_bin")
	if out, err := exec.Command(woncBin, "build", "-o", optBin, filepath.Join("..", "..", "..", "tests/language-spec/valid/enums/option_result_try.won")).CombinedOutput(); err != nil {
		t.Fatalf("wonc build option_result_try.won 失败: %v\n%s", err, string(out))
	}
	cmdOpt := exec.Command(optBin)
	_ = cmdOpt.Run()
	if cmdOpt.ProcessState.ExitCode() != 7 {
		t.Fatalf("opt_bin 运行退出码期望为 7 (process_pair 返回 Result::Ok(7)), 实际: %d", cmdOpt.ProcessState.ExitCode())
	}

	// 2. wonc check 在语义负例上返回非 0 并输出规范诊断码
	for _, tc := range []struct {
		rel  string
		code string
	}{
		{"tests/language-spec/invalid/patterns/non_exhaustive_match.won", "WON-MAT-001"},
		{"tests/language-spec/invalid/ffi/raw_ptr_deref_outside_unsafe.won", "WON-SEC-002"},
		{"tests/language-spec/invalid/types/invalid_method_args.won", "WON-TYP-001"},
		{"tests/language-spec/invalid/types/int_overflow_u128.won", "WON-TYP-003"},
	} {
		negOut, err := exec.Command(woncBin, "check", "--format=json", filepath.Join("..", "..", "..", tc.rel)).CombinedOutput()
		if err == nil {
			t.Fatalf("wonc check 在负例 %s 上应返回非 0 退出码", tc.rel)
		}
		if !strings.Contains(string(negOut), tc.code) {
			t.Errorf("%s JSON 输出应包含 %s, 实际: %s", tc.rel, tc.code, string(negOut))
		}
	}

	// 3. wonc build 对尚无 C11 代码生成实现的程序必须以 WON-GEN-002 明确拒止且不得落盘产生任何 .won.c 文件
	tmpWork := t.TempDir()
	for _, rel := range []string{
		"tests/language-spec/valid/types/struct_and_enum.won",
		"tests/language-spec/valid/types/full_width_integers.won",
	} {
		absPath, _ := filepath.Abs(filepath.Join("..", "..", "..", rel))
		absBin, _ := filepath.Abs(woncBin)
		cmd := exec.Command(absBin, "build", absPath)
		cmd.Dir = tmpWork
		buildOut, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("wonc build 应当拒止暂未实现 C11 代码生成的 %s", rel)
		}
		if !strings.Contains(string(buildOut), "WON-GEN-002") {
			t.Errorf("wonc build 拒止 %s 输出中应包含 WON-GEN-002, 实际: %s", rel, string(buildOut))
		}
		baseC := strings.TrimSuffix(filepath.Base(rel), ".won") + ".won.c"
		if _, statErr := os.Stat(filepath.Join(tmpWork, baseC)); statErr == nil {
			t.Errorf("wonc build 在发出 WON-GEN-002 后禁止生成残留 C 文件 %s", baseC)
		}
	}

	// 4. 验证不受支持的 unit 值 return 表达式在 wonc build 被 WON-GEN-002 拦截且不产生任何 .won.c 文件
	badUnitWon := filepath.Join(tmpWork, "bad_unit_ret.won")
	if err := os.WriteFile(badUnitWon, []byte("fn main() -> unit:\n    return ()\n"), 0644); err != nil {
		t.Fatalf("写入临时文件失败: %v", err)
	}
	absBin, _ := filepath.Abs(woncBin)
	cmdUnit := exec.Command(absBin, "build", badUnitWon)
	cmdUnit.Dir = tmpWork
	unitOut, err := cmdUnit.CombinedOutput()
	if err == nil {
		t.Fatalf("wonc build 应当以 WON-GEN-002 拒止不受支持的 unit 返回表达式")
	}
	if !strings.Contains(string(unitOut), "WON-GEN-002") {
		t.Errorf("期望输出 WON-GEN-002, 实际: %s", string(unitOut))
	}
	if _, statErr := os.Stat(filepath.Join(tmpWork, "bad_unit_ret.won.c")); statErr == nil {
		t.Errorf("wonc build 在 WON-GEN-002 拦截 bad_unit_ret.won 后不得产生 .won.c 文件")
	}
}

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
)

func parseFixtureFile(t *testing.T, relPath string) *diagnostic.Report {
	t.Helper()
	fullPath := filepath.Join("..", "..", "..", relPath)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("读取夹具文件 %s 失败: %v", fullPath, err)
	}
	report := diagnostic.NewReport()
	lex := lexer.New(relPath, string(content), report)
	p := parser.New(lex, report, relPath)
	_ = p.ParseProgram()
	return report
}

// TestSpecFixturesValid 验证 tests/language-spec/valid 下的所有规范合法示例均可通过词法分析与语法解析
func TestSpecFixturesValid(t *testing.T) {
	validFixtures := []string{
		"tests/language-spec/valid/basics/hello.won",
		"tests/language-spec/valid/types/struct_and_enum.won",
		"tests/language-spec/valid/ownership/borrow_and_move.won",
		"tests/language-spec/valid/errors/result_propagation.won",
		"tests/language-spec/valid/concurrency/structured_scope.won",
		"tests/language-spec/valid/ffi/zlib_interop.won",
	}

	for _, f := range validFixtures {
		t.Run(filepath.Base(f), func(t *testing.T) {
			report := parseFixtureFile(t, f)
			if report.HasErrors() {
				t.Fatalf("规范合法用例 %s 解析失败:\n%s", f, report.String())
			}
			t.Logf("PASS: %s 词法分析与语法解析通过 (0 errors)", f)
		})
	}
}

// TestSpecFixturesInvalidSyntax 验证所有语法负例均被词法/语法前端明确拒绝并返回预期的错误码家族
func TestSpecFixturesInvalidSyntax(t *testing.T) {
	cases := []struct {
		file     string
		wantCode string
	}{
		{"tests/language-spec/invalid/syntax/tab_indentation.won", "WON-SYN-001"},
		{"tests/language-spec/invalid/syntax/missing_block_body.won", "WON-SYN-004"},
		{"tests/language-spec/invalid/syntax/unexpected_indentation.won", "WON-SYN-002"},
		{"tests/language-spec/invalid/syntax/inconsistent_dedentation.won", "WON-SYN-003"},
		{"tests/language-spec/invalid/syntax/missing_closing_delimiter.won", "WON-SYN-007"},
		{"tests/language-spec/invalid/syntax/invalid_assignment_target.won", "WON-SYN-009"},
		{"tests/language-spec/invalid/syntax/malformed_generic_args.won", "WON-SYN-005"},
		{"tests/language-spec/invalid/syntax/invalid_interpolation_delimiter.won", "WON-SYN-007"},
		{"tests/language-spec/invalid/syntax/invalid_type_annotation.won", "WON-SYN-005"},
		{"tests/language-spec/invalid/syntax/malformed_range_expression.won", "WON-SYN-009"},
		{"tests/language-spec/invalid/syntax/malformed_function_declaration.won", "WON-SYN-005"},
		{"tests/language-spec/invalid/syntax/brace_block_syntax.won", "WON-SYN-005"},
	}

	for _, tc := range cases {
		t.Run(filepath.Base(tc.file), func(t *testing.T) {
			report := parseFixtureFile(t, tc.file)
			if !report.HasErrors() {
				t.Fatalf("语法负例 %s 应当报错但实际通过解析", tc.file)
			}
			found := false
			for _, d := range report.Diagnostics {
				if d.Code == tc.wantCode {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s 期望诊断码 %s，实际诊断输出:\n%s", tc.file, tc.wantCode, report.String())
			} else {
				t.Logf("PASS: %s 成功拦截，诊断码 %s 匹配", tc.file, tc.wantCode)
			}
		})
	}
}

// TestSpecFixturesSemanticNegativeClassification 验证语义负例均具有合法的语法结构（可无错通过词法/语法前端）
// 其中已实现的 FFI unsafe 校验可进一步捕获 WON-SEC-001；未实现的借用/并发语义明确留待 Phase 2/3。
func TestSpecFixturesSemanticNegativeClassification(t *testing.T) {
	semanticNegatives := []string{
		"tests/language-spec/invalid/types/implicit_coercion.won",
		"tests/language-spec/invalid/ownership/use_after_move.won",
		"tests/language-spec/invalid/concurrency/race_condition_capture.won",
		"tests/language-spec/invalid/ffi/foreign_call_outside_unsafe.won",
	}

	for _, f := range semanticNegatives {
		t.Run(filepath.Base(f), func(t *testing.T) {
			report := parseFixtureFile(t, f)
			if report.HasErrors() {
				t.Fatalf("语义负例 %s 的语法本身是合法的，前端不应误报语法错误:\n%s", f, report.String())
			}
		})
	}

	// 对已实现的 FFI 安全边界检查运行语义分析，验证 WON-SEC-001
	t.Run("VerifyFFIUnsafeBoundaryEnforcement", func(t *testing.T) {
		relPath := "tests/language-spec/invalid/ffi/foreign_call_outside_unsafe.won"
		fullPath := filepath.Join("..", "..", "..", relPath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		report := diagnostic.NewReport()
		lex := lexer.New(relPath, string(content), report)
		p := parser.New(lex, report, relPath)
		prog := p.ParseProgram()
		if report.HasErrors() {
			t.Fatalf("语法解析意外报错: %s", report.String())
		}

		mockZlib := map[string]*bindgen.BindingMeta{
			"zlib": {
				HeaderPath: "/usr/include/zlib.h",
				Functions: map[string]bindgen.FunctionSig{
					"compressBound": {
						Name:       "compressBound",
						ReturnType: "unsigned long",
						ParamTypes: []string{"unsigned long"},
					},
				},
			},
		}
		chk := checker.New(report, mockZlib)
		chk.CheckProgram(prog)
		if !report.HasErrors() {
			t.Fatalf("unsafe 外部调用应触发 WON-SEC-001 错误")
		}
		foundSec := false
		for _, d := range report.Diagnostics {
			if d.Code == "WON-SEC-001" {
				foundSec = true
				break
			}
		}
		if !foundSec {
			t.Errorf("期望错误码 WON-SEC-001，实际输出:\n%s", report.String())
		}
	})
}

// TestCanonicalSyntaxExamplesFromSpec 自动从 docs/language-spec/syntax-and-grammar.md 提取 20 个规范示例片段并校验全部通过前端解析
func TestCanonicalSyntaxExamplesFromSpec(t *testing.T) {
	specPath := filepath.Join("..", "..", "..", "docs", "language-spec", "syntax-and-grammar.md")
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("读取规范文档失败: %v", err)
	}

	lines := strings.Split(string(data), "\n")
	inSection5 := false
	inWonBlock := false
	var currentCode strings.Builder
	exampleIdx := 0

	for _, line := range lines {
		if strings.HasPrefix(line, "## 5. Comprehensive Canonical Syntax Examples") {
			inSection5 = true
			continue
		}
		if strings.HasPrefix(line, "## 6. ") {
			break
		}
		if !inSection5 {
			continue
		}

		if strings.HasPrefix(line, "```won") {
			inWonBlock = true
			currentCode.Reset()
			continue
		}
		if inWonBlock && strings.HasPrefix(line, "```") {
			inWonBlock = false
			exampleIdx++
			codeStr := currentCode.String()
			report := diagnostic.NewReport()
			lex := lexer.New("canonical_example.won", codeStr, report)
			p := parser.New(lex, report, "canonical_example.won")
			_ = p.ParseProgram()
			if report.HasErrors() {
				t.Errorf("规范文档第 %d 个示例语法解析失败:\n源码:\n%s\n错误:\n%s", exampleIdx, codeStr, report.String())
			}
			continue
		}
		if inWonBlock {
			currentCode.WriteString(line)
			currentCode.WriteByte('\n')
		}
	}

	if exampleIdx != 20 {
		t.Fatalf("预期从第 5 节提取 20 个规范示例，实际提取数量为 %d", exampleIdx)
	}
	t.Logf("PASS: 已从 docs/language-spec/syntax-and-grammar.md 提取并无错解析全部 20/20 规范示例！")
}

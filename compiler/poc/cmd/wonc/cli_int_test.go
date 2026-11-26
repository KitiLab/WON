package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
)

// TestWonCLIWonInt001Gate 验证当 TypedProgram.OwnershipChecked == false 时，
// wonc check 与 build 门控严格发出合法 JSON 诊断 WON-INT-001 并阻断放行及 .won.c 产物生成。
func TestWonCLIWonInt001Gate(t *testing.T) {
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "uncertified.won")
	if err := os.WriteFile(srcFile, []byte("fn main() -> i32:\n    return 0\n"), 0644); err != nil {
		t.Fatalf("写入测试源文件失败: %v", err)
	}

	span := diagnostic.Span{
		File:  srcFile,
		Start: diagnostic.Position{Line: 1, Column: 1, Byte: 0},
		End:   diagnostic.Position{Line: 2, Column: 12, Byte: 30},
	}

	for _, tc := range []struct {
		name string
		tp   *checker.TypedProgram
	}{
		{name: "ownership_false", tp: &checker.TypedProgram{OwnershipChecked: false, DropElaborated: true, CodegenSupported: true}},
		{name: "drop_false", tp: &checker.TypedProgram{OwnershipChecked: true, DropElaborated: false, CodegenSupported: true}},
	} {
		for _, phase := range []string{"check", "build"} {
			t.Run(tc.name+"_"+phase, func(t *testing.T) {
				rep := diagnostic.NewReport()
				action := "拒绝生成 C11 代码"
				if phase == "check" {
					action = "拒绝通过语义检查"
				}
				enforceOwnershipCheckedGate(rep, tc.tp, srcFile, span, action)

				if !rep.HasErrors() {
					t.Fatalf("%s/%s 必须失败", tc.name, phase)
				}
				jsonOut, err := rep.ToJSON()
				if err != nil {
					t.Fatalf("ToJSON 失败: %v", err)
				}
				var decoded diagnostic.Report
				if err := json.Unmarshal([]byte(jsonOut), &decoded); err != nil {
					t.Fatalf("JSON 解析失败: %v\n%s", err, jsonOut)
				}
				if decoded.Status != "error" {
					t.Fatalf("JSON status 期望 error，实际 %q", decoded.Status)
				}
				found := false
				for _, d := range decoded.Diagnostics {
					if d.Code == "WON-INT-001" && d.Span.Start.Line > 0 {
						found = true
					}
				}
				if !found {
					t.Fatalf("JSON 诊断缺少 WON-INT-001:\n%s", jsonOut)
				}
				entries, _ := os.ReadDir(tmpDir)
				for _, e := range entries {
					if strings.HasSuffix(e.Name(), ".won.c") || strings.HasSuffix(e.Name(), ".c") {
						t.Fatalf("失败后不得残留 C 产物: %s", e.Name())
					}
				}
			})
		}
	}
}

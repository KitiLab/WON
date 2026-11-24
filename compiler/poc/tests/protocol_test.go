package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
)

func TestMain(m *testing.M) {
	// 确保测试前 bin/wonc 存在且最新
	cmd := exec.Command("go", "build", "-o", "../bin/wonc", "../cmd/wonc")
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("Failed to build wonc for tests: " + string(out))
	}
	code := m.Run()
	os.Exit(code)
}

// TestJSONDiagnosticsProtocol 验证编译器输出的结构化 JSON 诊断协议完整性
func TestJSONDiagnosticsProtocol(t *testing.T) {
	woncBin := filepath.Join("..", "bin", "wonc")
	brokenFile := filepath.Join("..", "..", "..", "tests", "ai_repair", "broken.won")
	repairedFile := filepath.Join("..", "..", "..", "tests", "ai_repair", "auto_repaired.won")
	repairedBin := filepath.Join("..", "..", "..", "tests", "ai_repair", "auto_repaired_bin")
	defer os.Remove(repairedFile)
	defer os.Remove(repairedBin)

	// 1. 运行 check 命令获取 JSON 诊断输出
	cmd := exec.Command(woncBin, "check", "--format=json", brokenFile)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("预期 broken.won 存在语义错误，但编译器返回成功: %s", string(out))
	}

	var report diagnostic.Report
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("无法反序列化编译器 JSON 输出: %v\n原始输出:\n%s", err, string(out))
	}

	if len(report.Diagnostics) == 0 {
		t.Fatalf("预期包含诊断错误，但 diagnostics 列表为空")
	}

	diag := report.Diagnostics[0]
	if diag.Code != "WON-MUT-001" {
		t.Errorf("预期诊断码 WON-MUT-001，实际得到: %s", diag.Code)
	}
	if len(diag.Fixes) == 0 || len(diag.Fixes[0].Edits) == 0 {
		t.Fatalf("诊断报告中缺少可自动应用的修复方案 (Fixes/Edits)")
	}

	edit := diag.Fixes[0].Edits[0]
	t.Logf("成功捕获诊断 [%s]: %s", diag.Code, diag.Message)
	t.Logf("建议修改方案: %s (替换字节区间 [%d:%d] -> %q)",
		diag.Fixes[0].Description, edit.Start, edit.End, edit.Replacement)

	// 2. 模拟智能 Agent 根据 Edit 协议应用代码补丁
	origContent, err := os.ReadFile(brokenFile)
	if err != nil {
		t.Fatalf("读取原始文件失败: %v", err)
	}

	if edit.Start > len(origContent) || edit.End > len(origContent) || edit.Start > edit.End {
		t.Fatalf("修复区间的偏移量非法: [%d:%d], 文件长度: %d", edit.Start, edit.End, len(origContent))
	}

	patched := make([]byte, 0, len(origContent)+len(edit.Replacement))
	patched = append(patched, origContent[:edit.Start]...)
	patched = append(patched, []byte(edit.Replacement)...)
	patched = append(patched, origContent[edit.End:]...)

	if err := os.WriteFile(repairedFile, patched, 0644); err != nil {
		t.Fatalf("写入修复后文件失败: %v", err)
	}

	// 3. 重新对修复后的文件运行编译器检查
	checkPatchedCmd := exec.Command(woncBin, "check", "--format=json", repairedFile)
	patchedCheckOut, err := checkPatchedCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("修复后的文件编译检查失败: %v\n输出:\n%s", err, string(patchedCheckOut))
	}

	// 4. 构建原生可执行文件
	buildCmd := exec.Command(woncBin, "build", "-o", repairedBin, repairedFile)
	if buildOut, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("构建修复后文件失败: %v\n输出:\n%s", err, string(buildOut))
	}

	// 5. 执行并验证二进制运行成功
	runCmd := exec.Command(repairedBin)
	runOut, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("修复后的二进制运行失败: %v\n输出:\n%s", err, string(runOut))
	}

	t.Logf("协议与自动化闭环修复验证成功！运行输出: %s", string(runOut))
}

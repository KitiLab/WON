package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPhase2C0CLITIRIntegration 验证 wonc check / wonc check --dump-tir 与原有 WON-GEN-002 / zlib 行为共存不退化。
func TestPhase2C0CLITIRIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	woncBin := filepath.Join(tmpDir, "wonc")
	buildCmd := exec.Command("go", "build", "-o", woncBin, "../cmd/wonc")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("编译临时 wonc 失败: %v\n%s", err, string(out))
	}

	// 1. 默认 check 输出保持向后兼容，--dump-tir 输出完整确定性 TIR
	zlibFile, err := filepath.Abs(filepath.Join("..", "..", "..", "examples", "zlib_compression.won"))
	if err != nil {
		t.Fatalf("定位 zlib 示例失败: %v", err)
	}
	checkOut, err := exec.Command(woncBin, "check", zlibFile).CombinedOutput()
	if err != nil {
		t.Fatalf("wonc check zlib 失败: %v\n%s", err, string(checkOut))
	}
	if !strings.Contains(string(checkOut), "No diagnostics reported.") {
		t.Fatalf("默认 wonc check 输出不应改变，实际:\n%s", string(checkOut))
	}

	tirOut1, err := exec.Command(woncBin, "check", "--dump-tir", zlibFile).CombinedOutput()
	if err != nil {
		t.Fatalf("wonc check --dump-tir zlib 失败: %v\n%s", err, string(tirOut1))
	}
	tirOut2, err := exec.Command(woncBin, "check", "--dump-tir", zlibFile).CombinedOutput()
	if err != nil {
		t.Fatalf("二次 wonc check --dump-tir zlib 失败: %v\n%s", err, string(tirOut2))
	}
	if string(tirOut1) != string(tirOut2) {
		t.Fatalf("CLI --dump-tir 输出必须完全确定")
	}
	for _, want := range []string{
		"tir.Program ownership_ok=true drop_ok=true",
		"fn @f1 main()",
		"ForeignCall zlib::compressBound",
		"ForeignCall zlib::compress",
		"ForeignCall zlib::uncompress",
	} {
		if !strings.Contains(string(tirOut1), want) {
			t.Fatalf("zlib TIR Dump 缺少 %q:\n%s", want, string(tirOut1))
		}
	}

	// 2. 含 Struct/Enum/Drop 的程序通过 check --dump-tir 验证 TIR，且 wonc build 严格保持 WON-GEN-002
	adtSrc := filepath.Join(tmpDir, "adt_drop.won")
	src := `struct Box:
    msg: String

fn make_box(cond: bool) -> i32:
    var b = Box { msg: String::from("ok") }
    if cond:
        let moved = b.msg
        println(moved)
        return 1
    var opt_s: String
    if cond:
        opt_s = String::from("cond")
    else:
        println("skip")
    return 0

fn main() -> i32:
    return make_box(true)
`
	if err := os.WriteFile(adtSrc, []byte(src), 0644); err != nil {
		t.Fatalf("写入临时源码失败: %v", err)
	}
	adtTIR, err := exec.Command(woncBin, "check", "--dump-tir", adtSrc).CombinedOutput()
	if err != nil {
		t.Fatalf("ADT check --dump-tir 失败: %v\n%s", err, string(adtTIR))
	}
	for _, want := range []string{"type ", "glue g", "CondDrop ", "Drop "} {
		if !strings.Contains(string(adtTIR), want) {
			t.Fatalf("ADT TIR Dump 缺少 %q:\n%s", want, string(adtTIR))
		}
	}

	buildAdtCmd := exec.Command(woncBin, "build", adtSrc)
	buildAdtCmd.Dir = tmpDir
	buildOut, err := buildAdtCmd.CombinedOutput()
	if err == nil || !strings.Contains(string(buildOut), "WON-GEN-002") {
		t.Fatalf("非 C11 子集程序 wonc build 必须维持 WON-GEN-002 阻断，实际 err=%v:\n%s", err, string(buildOut))
	}
	if _, statErr := os.Stat(filepath.Join(tmpDir, "adt_drop.won.c")); statErr == nil {
		t.Fatalf("WON-GEN-002 阻断后不得在磁盘生成 adt_drop.won.c")
	}

	// 3. Phase 2C.1: 验证 wonc build --emit-c 实际经由 TIR->C11 路径产出可执行文件及 C 文件
	zlibOutBin := filepath.Join(tmpDir, "zlib_tir_bin")
	buildZlibCmd := exec.Command(woncBin, "build", "--emit-c", "-o", zlibOutBin, zlibFile)
	buildZlibCmd.Dir = tmpDir
	if out, err := buildZlibCmd.CombinedOutput(); err != nil {
		t.Fatalf("wonc build --emit-c zlib 失败: %v\n%s", err, string(out))
	}
	emittedCBytes, err := os.ReadFile(filepath.Join(tmpDir, "zlib_compression.won.c"))
	if err != nil {
		t.Fatalf("读取 --emit-c 产物失败: %v", err)
	}
	if !strings.Contains(string(emittedCBytes), "WON Phase 2C.1 TIR->C11") {
		t.Fatalf("wonc build 未使用 TIR->C11 后端输出 C 代码:\n%s", string(emittedCBytes))
	}
	runZlibOut, err := exec.Command(zlibOutBin).CombinedOutput()
	if err != nil || !strings.Contains(string(runZlibOut), "Zlib round-trip verification: PASS") {
		t.Fatalf("TIR 构建的 zlib 可执行文件运行失败: err=%v\n%s", err, string(runZlibOut))
	}
}

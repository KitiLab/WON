package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEndToEndZlibCompression(t *testing.T) {
	woncBin := filepath.Join("..", "bin", "wonc")
	wonSource := filepath.Join("..", "..", "..", "examples", "zlib_compression.won")
	outBin := filepath.Join("..", "..", "..", "examples", "zlib_compression_test_bin")
	defer os.Remove(outBin)

	// 1. 运行编译器构建
	cmdBuild := exec.Command(woncBin, "build", "-o", outBin, wonSource)
	buildOut, err := cmdBuild.CombinedOutput()
	if err != nil {
		t.Fatalf("构建 examples/zlib_compression.won 失败: %v\n输出: %s", err, string(buildOut))
	}

	// 2. 执行编译产物
	cmdRun := exec.Command(outBin)
	runOut, err := cmdRun.CombinedOutput()
	if err != nil {
		t.Fatalf("运行原生二进制失败: %v\n输出: %s", err, string(runOut))
	}

	outStr := string(runOut)
	if !strings.Contains(outStr, "Zlib round-trip verification: PASS") {
		t.Errorf("输出未包含 round-trip 验证通过标记: %s", outStr)
	}
	if !strings.Contains(outStr, "Byte-for-byte verification: PASS") {
		t.Errorf("输出未包含逐字节严格一致性验证标记: %s", outStr)
	}
	if !strings.Contains(outStr, "Decompressed size matches exactly: 57") {
		t.Errorf("输出未确认解压尺寸严格匹配: %s", outStr)
	}

	t.Logf("端到端 zlib 原生运行测试通过 (尺寸与逐字节一致性通过): \n%s", outStr)
}

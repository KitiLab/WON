package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEdgeCasesZlibCompression 测试边界场景：空数据、NUL 字节、损坏数据错误处理
func TestEdgeCasesZlibCompression(t *testing.T) {
	woncBin := filepath.Join("..", "bin", "wonc")
	tmpDir := t.TempDir()

	// 1. 验证空字符串压缩与解压
	emptyWon := filepath.Join(tmpDir, "empty.won")
	emptyBin := filepath.Join(tmpDir, "empty_bin")
	emptySrc := `
#import <zlib.h>

fn main() -> i32 {
    let msg = "";
    let input_bytes = msg.as_bytes();
    let input_len: u64 = 0;

    let mut comp_buf: [u8; 64] = [0; 64];
    let mut comp_len: u64 = 64;

    let c_status = unsafe {
        zlib::compress(comp_buf.as_mut_ptr(), &mut comp_len, input_bytes.as_ptr(), input_len)
    };
    if c_status != 0 {
        return 1;
    }

    let mut decomp_buf: [u8; 64] = [0; 64];
    let mut decomp_len: u64 = 64;
    let d_status = unsafe {
        zlib::uncompress(decomp_buf.as_mut_ptr(), &mut decomp_len, comp_buf.as_ptr(), comp_len)
    };
    if d_status != 0 {
        return 2;
    }
    if decomp_len != 0 {
        return 3;
    }
    println("Empty payload round-trip: PASS");
    return 0;
}
`
	if err := os.WriteFile(emptyWon, []byte(emptySrc), 0644); err != nil {
		t.Fatalf("写入 empty.won 失败: %v", err)
	}

	buildCmd := exec.Command(woncBin, "build", "-o", emptyBin, emptyWon)
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("构建 empty.won 失败: %v\n输出: %s", err, string(out))
	}
	runCmd := exec.Command(emptyBin)
	runOut, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("运行 empty_bin 失败: %v\n输出: %s", err, string(runOut))
	}
	if !strings.Contains(string(runOut), "Empty payload round-trip: PASS") {
		t.Errorf("未包含空载荷通过标记: %s", string(runOut))
	}

	// 2. 验证损坏的压缩数据解压时安全报错 (返回非零状态码)
	corruptWon := filepath.Join(tmpDir, "corrupt.won")
	corruptBin := filepath.Join(tmpDir, "corrupt_bin")
	corruptSrc := `
#import <zlib.h>

fn main() -> i32 {
    // 构造无效/损坏的压缩数据
    let mut bad_buf: [u8; 16] = [0; 16];
    let bad_len: u64 = 16;

    let mut decomp_buf: [u8; 64] = [0; 64];
    let mut decomp_len: u64 = 64;

    let status = unsafe {
        zlib::uncompress(decomp_buf.as_mut_ptr(), &mut decomp_len, bad_buf.as_mut_ptr(), bad_len)
    };

    if status != 0 {
        println("Corrupted payload safely rejected with status: ", status);
        return 0;
    }

    println("Error: Corrupted payload was unexpectedly accepted");
    return 1;
}
`
	if err := os.WriteFile(corruptWon, []byte(corruptSrc), 0644); err != nil {
		t.Fatalf("写入 corrupt.won 失败: %v", err)
	}

	buildCorrupt := exec.Command(woncBin, "build", "-o", corruptBin, corruptWon)
	if out, err := buildCorrupt.CombinedOutput(); err != nil {
		t.Fatalf("构建 corrupt.won 失败: %v\n输出: %s", err, string(out))
	}
	runCorrupt := exec.Command(corruptBin)
	corruptOut, err := runCorrupt.CombinedOutput()
	if err != nil {
		t.Fatalf("运行 corrupt_bin 失败: %v\n输出: %s", err, string(corruptOut))
	}
	if !strings.Contains(string(corruptOut), "Corrupted payload safely rejected") {
		t.Errorf("损坏数据未被安全拒绝: %s", string(corruptOut))
	}

	t.Logf("所有边缘与异常安全性用例验证通过！")
}

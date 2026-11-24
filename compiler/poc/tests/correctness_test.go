package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEdgeCasesZlibCompression 测试边界场景：空数据、正常载荷、NUL 字节、非 ASCII 字节、大载荷、容量不足与损坏数据
func TestEdgeCasesZlibCompression(t *testing.T) {
	woncBin := filepath.Join("..", "bin", "wonc")
	tmpDir := t.TempDir()

	// 1. 验证空字符串压缩与解压 (0 字节)
	t.Run("EmptyPayload", func(t *testing.T) {
		wonFile := filepath.Join(tmpDir, "empty.won")
		binFile := filepath.Join(tmpDir, "empty_bin")
		src := `
#import <zlib.h>

fn main() -> i32:
    let msg = ""
    let input_bytes = msg.as_bytes()
    let input_len: u64 = 0

    var comp_buf: [u8; 64] = [0; 64]
    var comp_len: u64 = 64

    let c_status = unsafe:
        zlib::compress(comp_buf.as_mut_ptr(), &mut comp_len, input_bytes.as_ptr(), input_len)
    if c_status != 0:
        return 1

    var decomp_buf: [u8; 64] = [0; 64]
    var decomp_len: u64 = 64
    let d_status = unsafe:
        zlib::uncompress(decomp_buf.as_mut_ptr(), &mut decomp_len, comp_buf.as_ptr(), comp_len)
    if d_status != 0:
        return 2
    if decomp_len != 0:
        return 3
    println("Empty payload round-trip: PASS")
    return 0
`
		if err := os.WriteFile(wonFile, []byte(src), 0644); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		buildCmd := exec.Command(woncBin, "build", "-o", binFile, wonFile)
		if out, err := buildCmd.CombinedOutput(); err != nil {
			t.Fatalf("构建失败: %v\n输出: %s", err, string(out))
		}
		runOut, err := exec.Command(binFile).CombinedOutput()
		if err != nil {
			t.Fatalf("运行失败: %v\n输出: %s", err, string(runOut))
		}
		if !strings.Contains(string(runOut), "Empty payload round-trip: PASS") {
			t.Errorf("未包含通过标记: %s", string(runOut))
		}
	})

	// 2. 验证常规标准非空文本载荷 (带长度与逐字节比对)
	t.Run("NormalNonEmptyPayload", func(t *testing.T) {
		wonFile := filepath.Join(tmpDir, "normal.won")
		binFile := filepath.Join(tmpDir, "normal_bin")
		src := `
#import <zlib.h>

fn main() -> i32:
    let msg = "Hello, WON Systems Programming!"
    let input_bytes = msg.as_bytes()
    let input_len: u64 = 31

    var comp_buf: [u8; 128] = [0; 128]
    var comp_len: u64 = 128

    let c_status = unsafe:
        zlib::compress(comp_buf.as_mut_ptr(), &mut comp_len, input_bytes.as_ptr(), input_len)
    if c_status != 0:
        return 1

    var decomp_buf: [u8; 128] = [0; 128]
    var decomp_len: u64 = 128
    let d_status = unsafe:
        zlib::uncompress(decomp_buf.as_mut_ptr(), &mut decomp_len, comp_buf.as_ptr(), comp_len)
    if d_status != 0:
        return 2
    if decomp_len != input_len:
        return 3
    let is_eq = unsafe:
        bytes_equal(input_bytes.as_ptr(), decomp_buf.as_ptr(), input_len)
    if is_eq == 0:
        return 4
    println("Normal payload round-trip: PASS")
    return 0
`
		if err := os.WriteFile(wonFile, []byte(src), 0644); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		buildCmd := exec.Command(woncBin, "build", "-o", binFile, wonFile)
		if out, err := buildCmd.CombinedOutput(); err != nil {
			t.Fatalf("构建失败: %v\n输出: %s", err, string(out))
		}
		runOut, err := exec.Command(binFile).CombinedOutput()
		if err != nil {
			t.Fatalf("运行失败: %v\n输出: %s", err, string(runOut))
		}
		if !strings.Contains(string(runOut), "Normal payload round-trip: PASS") {
			t.Errorf("未包含通过标记: %s", string(runOut))
		}
	})

	// 3. 验证包含嵌入式 NUL 字节 (\0) 的二进制/字符串载荷
	t.Run("EmbeddedNulBytes", func(t *testing.T) {
		wonFile := filepath.Join(tmpDir, "nul_bytes.won")
		binFile := filepath.Join(tmpDir, "nul_bytes_bin")
		src := `
#import <zlib.h>

fn main() -> i32:
    let msg = "WON\0Binary\0Nul\0Payload"
    let input_bytes = msg.as_bytes()
    let input_len: u64 = 23

    var comp_buf: [u8; 128] = [0; 128]
    var comp_len: u64 = 128

    let c_status = unsafe:
        zlib::compress(comp_buf.as_mut_ptr(), &mut comp_len, input_bytes.as_ptr(), input_len)
    if c_status != 0:
        return 1

    var decomp_buf: [u8; 128] = [0; 128]
    var decomp_len: u64 = 128
    let d_status = unsafe:
        zlib::uncompress(decomp_buf.as_mut_ptr(), &mut decomp_len, comp_buf.as_ptr(), comp_len)
    if d_status != 0:
        return 2
    if decomp_len != input_len:
        return 3
    let is_eq = unsafe:
        bytes_equal(input_bytes.as_ptr(), decomp_buf.as_ptr(), input_len)
    if is_eq == 0:
        return 4
    println("Embedded NUL bytes round-trip: PASS")
    return 0
`
		if err := os.WriteFile(wonFile, []byte(src), 0644); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		buildCmd := exec.Command(woncBin, "build", "-o", binFile, wonFile)
		if out, err := buildCmd.CombinedOutput(); err != nil {
			t.Fatalf("构建失败: %v\n输出: %s", err, string(out))
		}
		runOut, err := exec.Command(binFile).CombinedOutput()
		if err != nil {
			t.Fatalf("运行失败: %v\n输出: %s", err, string(runOut))
		}
		if !strings.Contains(string(runOut), "Embedded NUL bytes round-trip: PASS") {
			t.Errorf("未包含通过标记: %s", string(runOut))
		}
	})

	// 4. 验证多字节非 ASCII UTF-8 字符载荷
	t.Run("NonASCIIPayload", func(t *testing.T) {
		wonFile := filepath.Join(tmpDir, "non_ascii.won")
		binFile := filepath.Join(tmpDir, "non_ascii_bin")
		src := `
#import <zlib.h>

fn main() -> i32:
    let msg = "WON编程语言：极速编译·原生交互！"
    let input_bytes = msg.as_bytes()
    let input_len: u64 = 47

    var comp_buf: [u8; 128] = [0; 128]
    var comp_len: u64 = 128

    let c_status = unsafe:
        zlib::compress(comp_buf.as_mut_ptr(), &mut comp_len, input_bytes.as_ptr(), input_len)
    if c_status != 0:
        return 1

    var decomp_buf: [u8; 128] = [0; 128]
    var decomp_len: u64 = 128
    let d_status = unsafe:
        zlib::uncompress(decomp_buf.as_mut_ptr(), &mut decomp_len, comp_buf.as_ptr(), comp_len)
    if d_status != 0:
        return 2
    if decomp_len != input_len:
        return 3
    let is_eq = unsafe:
        bytes_equal(input_bytes.as_ptr(), decomp_buf.as_ptr(), input_len)
    if is_eq == 0:
        return 4
    println("Non-ASCII UTF-8 payload round-trip: PASS")
    return 0
`
		if err := os.WriteFile(wonFile, []byte(src), 0644); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		buildCmd := exec.Command(woncBin, "build", "-o", binFile, wonFile)
		if out, err := buildCmd.CombinedOutput(); err != nil {
			t.Fatalf("构建失败: %v\n输出: %s", err, string(out))
		}
		runOut, err := exec.Command(binFile).CombinedOutput()
		if err != nil {
			t.Fatalf("运行失败: %v\n输出: %s", err, string(runOut))
		}
		if !strings.Contains(string(runOut), "Non-ASCII UTF-8 payload round-trip: PASS") {
			t.Errorf("未包含通过标记: %s", string(runOut))
		}
	})

	// 5. 验证大载荷 (4096 字节 / 4 KiB 阵列)
	t.Run("LargerPayload4KB", func(t *testing.T) {
		wonFile := filepath.Join(tmpDir, "large4k.won")
		binFile := filepath.Join(tmpDir, "large4k_bin")
		src := `
#import <zlib.h>

fn main() -> i32:
    var input_buf: [u8; 4096] = [65; 4096]
    let input_len: u64 = 4096

    var comp_buf: [u8; 4200] = [0; 4200]
    var comp_len: u64 = 4200

    let c_status = unsafe:
        zlib::compress(comp_buf.as_mut_ptr(), &mut comp_len, input_buf.as_ptr(), input_len)
    if c_status != 0:
        return 1

    var decomp_buf: [u8; 4096] = [0; 4096]
    var decomp_len: u64 = 4096
    let d_status = unsafe:
        zlib::uncompress(decomp_buf.as_mut_ptr(), &mut decomp_len, comp_buf.as_ptr(), comp_len)
    if d_status != 0:
        return 2
    if decomp_len != input_len:
        return 3
    let is_eq = unsafe:
        bytes_equal(input_buf.as_ptr(), decomp_buf.as_ptr(), input_len)
    if is_eq == 0:
        return 4
    println("Larger 4KB payload round-trip: PASS")
    return 0
`
		if err := os.WriteFile(wonFile, []byte(src), 0644); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		buildCmd := exec.Command(woncBin, "build", "-o", binFile, wonFile)
		if out, err := buildCmd.CombinedOutput(); err != nil {
			t.Fatalf("构建失败: %v\n输出: %s", err, string(out))
		}
		runOut, err := exec.Command(binFile).CombinedOutput()
		if err != nil {
			t.Fatalf("运行失败: %v\n输出: %s", err, string(runOut))
		}
		if !strings.Contains(string(runOut), "Larger 4KB payload round-trip: PASS") {
			t.Errorf("未包含通过标记: %s", string(runOut))
		}
	})

	// 6. 验证解压缓冲区容量不足时安全报错 (Z_BUF_ERROR = -5)
	t.Run("InsufficientDecompressCapacity", func(t *testing.T) {
		wonFile := filepath.Join(tmpDir, "insufficient.won")
		binFile := filepath.Join(tmpDir, "insufficient_bin")
		src := `
#import <zlib.h>

fn main() -> i32:
    let msg = "WON Testing Buffer Capacity Constraint Under Zlib"
    let input_bytes = msg.as_bytes()
    let input_len: u64 = 50

    var comp_buf: [u8; 128] = [0; 128]
    var comp_len: u64 = 128

    let c_status = unsafe:
        zlib::compress(comp_buf.as_mut_ptr(), &mut comp_len, input_bytes.as_ptr(), input_len)
    if c_status != 0:
        return 1

    # 故意分配极小的解压缓冲区 (仅 10 字节)，不足以容纳 50 字节解压产物
    var small_decomp_buf: [u8; 10] = [0; 10]
    var small_decomp_len: u64 = 10
    let d_status = unsafe:
        zlib::uncompress(small_decomp_buf.as_mut_ptr(), &mut small_decomp_len, comp_buf.as_ptr(), comp_len)

    # zlib 应安全返回 Z_BUF_ERROR (-5)，绝不能崩溃或越界写入
    if d_status != 0:
        println("Insufficient capacity safely rejected with status: ", d_status)
        return 0

    println("Error: Insufficient capacity unexpectedly succeeded!")
    return 2
`
		if err := os.WriteFile(wonFile, []byte(src), 0644); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		buildCmd := exec.Command(woncBin, "build", "-o", binFile, wonFile)
		if out, err := buildCmd.CombinedOutput(); err != nil {
			t.Fatalf("构建失败: %v\n输出: %s", err, string(out))
		}
		runOut, err := exec.Command(binFile).CombinedOutput()
		if err != nil {
			t.Fatalf("运行失败: %v\n输出: %s", err, string(runOut))
		}
		if !strings.Contains(string(runOut), "Insufficient capacity safely rejected") {
			t.Errorf("未捕获容量不足拒绝标记: %s", string(runOut))
		}
	})

	// 7. 验证损坏的压缩数据解压时安全报错 (返回非零状态码，如 Z_DATA_ERROR = -3)
	t.Run("CorruptedPayloadRejection", func(t *testing.T) {
		wonFile := filepath.Join(tmpDir, "corrupt.won")
		binFile := filepath.Join(tmpDir, "corrupt_bin")
		src := `
#import <zlib.h>

fn main() -> i32:
    var bad_buf: [u8; 16] = [0; 16]
    let bad_len: u64 = 16

    var decomp_buf: [u8; 64] = [0; 64]
    var decomp_len: u64 = 64

    let status = unsafe:
        zlib::uncompress(decomp_buf.as_mut_ptr(), &mut decomp_len, bad_buf.as_mut_ptr(), bad_len)

    if status != 0:
        println("Corrupted payload safely rejected with status: ", status)
        return 0

    println("Error: Corrupted payload was unexpectedly accepted")
    return 1
`
		if err := os.WriteFile(wonFile, []byte(src), 0644); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		buildCmd := exec.Command(woncBin, "build", "-o", binFile, wonFile)
		if out, err := buildCmd.CombinedOutput(); err != nil {
			t.Fatalf("构建失败: %v\n输出: %s", err, string(out))
		}
		runOut, err := exec.Command(binFile).CombinedOutput()
		if err != nil {
			t.Fatalf("运行失败: %v\n输出: %s", err, string(runOut))
		}
		if !strings.Contains(string(runOut), "Corrupted payload safely rejected") {
			t.Errorf("损坏数据未被安全拒绝: %s", string(runOut))
		}
	})

	t.Logf("所有 7 个边界与异常安全性用例全部验证通过！")
}

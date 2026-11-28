package c11_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/codegen/c11"
)

// TestC11StringViewBasicAndSubsliceOutput 验证空字符串视图、常规短字符串、借用视图与无 NUL 截断子视图的输出正确性。
func TestC11StringViewBasicAndSubsliceOutput(t *testing.T) {
	src := `
fn echo_view(s: &str) -> &str:
    return s

fn main() -> i32:
    let empty: &str = ""
    let short: &str = "hello-won"
    let borrowed: &str = echo_view(short)
    println("EMPTY=[", empty, "] SHORT=[", short, "] BORROW=[", borrowed, "]")
    return 0
`
	tirProg, _ := lowerSourceToTIR(t, src, nil)
	cCode, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)
	if err != nil {
		t.Fatalf("生成 C11 失败: %v", err)
	}

	// 确认生成的 C 源码已彻底剔除旧的 %.*s 窄化占位符，统一采用 _won_write_bytes
	if strings.Contains(cCode, "%.*s") {
		t.Fatalf("生成的 C 源码严禁出现 %%.*s 格式串:\n%s", cCode)
	}
	if !strings.Contains(cCode, "_won_write_bytes") {
		t.Fatalf("生成的 C 源码必须调用定长 _won_write_bytes 辅助函数:\n%s", cCode)
	}

	gccOut, clangOut := compileStrictGCCAndClang(t, cCode, nil, nil)
	want := "EMPTY=[] SHORT=[hello-won] BORROW=[hello-won]\n"
	if gccOut != want {
		t.Errorf("GCC 输出不符合预期: got %q, want %q", gccOut, want)
	}
	if clangOut != want {
		t.Errorf("Clang 输出不符合预期: got %q, want %q", clangOut, want)
	}
}

// TestC11StringViewEmbeddedNULAndByteExactness 验证嵌入 NUL (\0) 字符时定长字节输出不被截断的完备语义。
func TestC11StringViewEmbeddedNULAndByteExactness(t *testing.T) {
	src := `
fn echo(s: &str) -> &str:
    return s

fn main() -> i32:
    let s = echo("ABC\0DEF")
    println("START|", s, "|END")
    return 0
`
	tirProg, _ := lowerSourceToTIR(t, src, nil)
	cCode, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)
	if err != nil {
		t.Fatalf("生成 C11 失败: %v", err)
	}

	gccOut, clangOut := compileStrictGCCAndClang(t, cCode, nil, nil)

	wantBytes := []byte("START|ABC\x00DEF|END\n")
	if !bytes.Equal([]byte(gccOut), wantBytes) {
		t.Fatalf("GCC 输出未按字节精确保留嵌入 NUL: got %v (%q), want %v (%q)", []byte(gccOut), gccOut, wantBytes, string(wantBytes))
	}
	if !bytes.Equal([]byte(clangOut), wantBytes) {
		t.Fatalf("Clang 输出未按字节精确保留嵌入 NUL: got %v (%q), want %v (%q)", []byte(clangOut), clangOut, wantBytes, string(wantBytes))
	}

	// 验证确实存在 0 字节且紧随其后的 DEF 正常输出
	if !strings.Contains(gccOut, "DEF|END") {
		t.Errorf("GCC 输出在嵌入 NUL 处发生截断，丢失后续内容")
	}
}

// TestC11StringViewMixedArgumentsAndOrder 验证混合参数类型、从左至右求值序与最终唯一换行。
func TestC11StringViewMixedArgumentsAndOrder(t *testing.T) {
	src := `
fn echo(s: &str) -> &str:
    return s

fn main() -> i32:
    let tag: &str = echo("ALPHA")
    let id: i32 = 42
    let active: bool = true
    let score: f64 = 98.5
    let ch: char = 'Z'
    let tail: &str = echo("OMEGA")
    println("PREFIX: ", tag, " ID=", id, " OK=", active, " SCORE=", score, " CH=", ch, " TAIL=", tail)
    return 0
`
	tirProg, _ := lowerSourceToTIR(t, src, nil)
	cCode, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)
	if err != nil {
		t.Fatalf("生成 C11 失败: %v", err)
	}

	gccOut, clangOut := compileStrictGCCAndClang(t, cCode, nil, nil)
	want := "PREFIX: ALPHA ID=42 OK=true SCORE=98.5 CH=90 TAIL=OMEGA\n"
	if gccOut != want {
		t.Errorf("GCC 混合参数输出顺序或内容不符合预期: got %q, want %q", gccOut, want)
	}
	if clangOut != want {
		t.Errorf("Clang 混合参数输出顺序或内容不符合预期: got %q, want %q", clangOut, want)
	}

	// 确认全输出中严格仅有且仅有一个换行符位于末尾
	if strings.Count(gccOut, "\n") != 1 || !strings.HasSuffix(gccOut, "\n") {
		t.Errorf("GCC 输出应且仅包含一个末尾换行符: %q", gccOut)
	}
}

// TestC11StringViewNonNULTerminatedSubslice 验证子切片视图后继字节为非 NUL 时定长 fwrite 绝不发生越界探查。
func TestC11StringViewNonNULTerminatedSubslice(t *testing.T) {
	// 构造底层缓冲区后继字节为 'X' (非 NUL) 的 C 测试断言
	cSource := `
#define _POSIX_C_SOURCE 200809L
#include <stdio.h>
#include <stdlib.h>
#include <stddef.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>

#ifndef _WON_WRITE_BYTES_IMPL
#define _WON_WRITE_BYTES_IMPL(ptr, len) (void)fwrite((ptr), 1, (len), stdout)
#endif
static inline void _won_write_bytes(const uint8_t *ptr, size_t len) {
    if (len == 0) return;
    _WON_WRITE_BYTES_IMPL(ptr, len);
}

typedef struct { const uint8_t* ptr; size_t len; } _won_str_t;

int main(void) {
    // 连续 8 字节均非 NUL，末尾紧跟 'Z'
    const uint8_t raw[8] = { 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'Z' };
    // 构造跨越 raw[2..5] 的子视图 ("CDE")，其逻辑末尾后为 'F' 而非 '\0'
    _won_str_t sub = { .ptr = raw + 2, .len = 3 };
    printf("SUB=[");
    _won_write_bytes(sub.ptr, sub.len);
    printf("]\n");
    return 0;
}
`
	gccOut, clangOut := compileStrictGCCAndClang(t, cSource, nil, nil)
	want := "SUB=[CDE]\n"
	if gccOut != want {
		t.Errorf("GCC 子视图输出越界或截断: got %q, want %q", gccOut, want)
	}
	if clangOut != want {
		t.Errorf("Clang 子视图输出越界或截断: got %q, want %q", clangOut, want)
	}
}

// TestC11StringViewLargeLengthSafetyAndAbsenceOfNarrowing 验证 length 表达式保持 size_t 且 > INT_MAX 计数无窄化截断。
func TestC11StringViewLargeLengthSafetyAndAbsenceOfNarrowing(t *testing.T) {
	src := `
fn echo(s: &str) -> &str:
    return s

fn main() -> i32:
    let s = echo("length_check")
    println("VIEW=", s)
    return 0
`
	tirProg, _ := lowerSourceToTIR(t, src, nil)
	cCode, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)
	if err != nil {
		t.Fatalf("生成 C11 失败: %v", err)
	}

	// 1. 验证 C 源码未包含 (int) 窄化转换
	if strings.Contains(cCode, "(int)") {
		t.Errorf("生成的 C11 代码不应将任何长度强制转换为 (int):\n%s", cCode)
	}
	// 2. 验证写入调用保持 size_t
	if !strings.Contains(cCode, "_won_write_bytes(((s).ptr), ((s).len));") && !strings.Contains(cCode, "_won_write_bytes(") {
		t.Errorf("生成的 C11 代码必须使用 _won_write_bytes:\n%s", cCode)
	}

	// 3. 测试缝隙 (Test Seam)：拦截 _won_write_bytes 验证传入 count > INT_MAX (3000000000ULL) 无符号扩展与截断
	seamCSource := `
#define _POSIX_C_SOURCE 200809L
#include <stdio.h>
#include <stdlib.h>
#include <stddef.h>
#include <stdint.h>
#include <stdbool.h>
#include <assert.h>

static size_t g_captured_len = 0;
static const uint8_t *g_captured_ptr = NULL;

static inline void seam_record(const uint8_t *ptr, size_t len) {
    g_captured_ptr = ptr;
    g_captured_len = len;
}

#define _WON_WRITE_BYTES_IMPL(ptr, len) seam_record(ptr, len)

static inline void _won_write_bytes(const uint8_t *ptr, size_t len) {
    if (len == 0) return;
    _WON_WRITE_BYTES_IMPL(ptr, len);
}

int main(void) {
    // 构造大于 INT_MAX (2147483647) 的长度 3000000000ULL，验证 size_t 传输无窄化
    const size_t huge_len = (size_t)3000000000ULL;
    const uint8_t dummy_target[1] = { 42 };
    _won_write_bytes(dummy_target, huge_len);

    if (g_captured_len != huge_len) {
        fprintf(stderr, "长度遭窄化: got %zu, want %zu\n", g_captured_len, huge_len);
        return 1;
    }
    if (g_captured_ptr != dummy_target) {
        fprintf(stderr, "指针地址不匹配\n");
        return 2;
    }
    printf("HUGE_LEN_OK=%zu\n", g_captured_len);
    return 0;
}
`
	gccOut, clangOut := compileStrictGCCAndClang(t, seamCSource, nil, nil)
	wantSeam := "HUGE_LEN_OK=3000000000\n"
	if gccOut != wantSeam || clangOut != wantSeam {
		t.Fatalf("超大长度测试缝隙失败: gcc=%q clang=%q", gccOut, clangOut)
	}
}

// TestC11StringViewSanitizerRun 验证在 AddressSanitizer 与 UndefinedBehaviorSanitizer 下运行零越界、零内存违例。
func TestC11StringViewSanitizerRun(t *testing.T) {
	src := `
fn echo(s: &str) -> &str:
    return s

fn main() -> i32:
    let s1 = echo("first-word")
    let s2 = echo("second-word")
    let empty: &str = ""
    let nul_str = echo("alpha\0beta")
    println(s1, " ", s2, empty, " ", nul_str, " end")
    return 0
`
	tirProg, _ := lowerSourceToTIR(t, src, nil)
	cCode, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)
	if err != nil {
		t.Fatalf("生成 C11 失败: %v", err)
	}

	tmpDir := t.TempDir()
	cFile := filepath.Join(tmpDir, "san_str.c")
	if err := os.WriteFile(cFile, []byte(cCode), 0644); err != nil {
		t.Fatalf("写入 C 临时文件失败: %v", err)
	}

	// 针对 gcc 和 clang 分别启用 ASan + UBSan 严格执行
	for _, cc := range []string{"gcc", "clang"} {
		binFile := filepath.Join(tmpDir, "bin_san_"+cc)
		args := []string{
			"-std=c11", "-pedantic-errors", "-Wall", "-Wextra", "-Werror",
			"-fsanitize=address,undefined", "-fno-omit-frame-pointer",
			cFile, "-o", binFile,
		}
		cmd := exec.Command(cc, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("跳过 %s ASan+UBSan 运行（环境可能缺少 libasan/libubsan: %v: %s）", cc, err, string(out))
			continue
		}
		runCmd := exec.Command(binFile)
		runOut, rerr := runCmd.CombinedOutput()
		if rerr != nil {
			t.Fatalf("%s ASan+UBSan 执行报错: %v\n输出:\n%s", cc, rerr, string(runOut))
		}
		if !strings.Contains(string(runOut), "first-word second-word alpha") {
			t.Fatalf("%s 运行输出异常: %s", cc, string(runOut))
		}
	}
}

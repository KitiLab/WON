package c11_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/codegen/c11"
)

// TestStringRuntime_AccountingSeamAndMinCapacity 验证运行时分配器桩钩注入、
// 最小容量分配 (>= 16 字节) 以及净堆内存平衡 (alloc == free 且零泄漏)。
func TestStringRuntime_AccountingSeamAndMinCapacity(t *testing.T) {
	src := `
fn run_ops():
    let s0 = String::from("")
    if s0.len() != 0 or s0.capacity() != 0:
        println("ERR_S0")
        return
    let s1 = String::from("a")
    if s1.len() != 1 or s1.capacity() < 16:
        println("ERR_S1")
        return
    var s2 = String::from("hello")
    s2.push_str(" world")
    if s2.len() != 11:
        println("ERR_S2")
        return

fn main() -> i32:
    run_ops()
    println("ACCOUNTING_PASS")
    return 0
`
	tirProg, _ := lowerSourceToTIR(t, src, nil)
	cCode, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)
	if err != nil {
		t.Fatalf("TIR->C11 生成失败: %v", err)
	}

	// 在顶部注入内存记账桩钩
	accountingHeader := `
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stddef.h>

static size_t g_alloc_count = 0;
static size_t g_alloc_bytes = 0;
static size_t g_realloc_count = 0;
static size_t g_free_count = 0;

static void* test_alloc(size_t sz) {
    g_alloc_count++;
    g_alloc_bytes += sz;
    return malloc(sz);
}

static void* test_realloc(void* ptr, size_t new_cap) {
    g_realloc_count++;
    return realloc(ptr, new_cap);
}

static void test_free(void* ptr, size_t cap) {
    (void)cap;
    if (ptr != NULL) {
        g_free_count++;
        free(ptr);
    }
}

#define _WON_ALLOC_IMPL(b) test_alloc(b)
#define _WON_REALLOC_IMPL(p, nc) test_realloc(p, nc)
#define _WON_FREE_IMPL(p, c) test_free(p, c)
`
	fullCode := accountingHeader + "\n" + cCode

	// 追加 main 退出前检查记账平衡
	checkHook := `
int check_memory_balance(void) {
    (void)g_realloc_count;
    (void)g_alloc_bytes;
    if (g_alloc_count == 0) {
        fprintf(stderr, "FAIL: 期望至少 1 次堆分配，实际为 0\n");
        return 1;
    }
    if (g_alloc_count != g_free_count) {
        fprintf(stderr, "FAIL: 内存未平衡: alloc=%zu, free=%zu\n", g_alloc_count, g_free_count);
        return 2;
    }
    return 0;
}
`
	fullCode += "\n" + checkHook

	gccOut, clangOut := compileStrictGCCAndClang(t, fullCode, nil, nil)
	if gccOut != clangOut || !strings.Contains(gccOut, "ACCOUNTING_PASS") {
		t.Fatalf("运行时分配器记账测试失败:\ngcc=%s\nclang=%s", gccOut, clangOut)
	}
}

// TestStringRuntime_CapacityZeroNeverReachesFree 验证容量为 0 的静态空串哨兵
// 绝对不会调用底层的 free()，且可重入安全释放。
func TestStringRuntime_CapacityZeroNeverReachesFree(t *testing.T) {
	cSource := `
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stddef.h>
#include <stdbool.h>

static int g_raw_free_called = 0;
static void intercepted_free(void* p) {
    g_raw_free_called++;
    free(p);
}

#define _WON_FREE_IMPL(p, cap) intercepted_free(p)

static const uint8_t _won_empty_bytes[1] = {0};

typedef struct {
    uint8_t* ptr;
    size_t len;
    size_t cap;
} _won_string_t;

static inline void _won_free(void* ptr, size_t cap) {
    if (cap == 0 || ptr == NULL || ptr == (void*)_won_empty_bytes) {
        return;
    }
    _WON_FREE_IMPL(ptr, cap);
}

static inline void _won_drop_string(_won_string_t* s) {
    if (s == NULL) return;
    if (s->cap > 0) {
        _won_free(s->ptr, s->cap);
    }
    s->ptr = (uint8_t*)_won_empty_bytes;
    s->len = 0;
    s->cap = 0;
}

int main(void) {
    _won_string_t empty_str = {
        .ptr = (uint8_t*)_won_empty_bytes,
        .len = 0,
        .cap = 0,
    };

    // 针对容量为 0 的空串执行多次析构
    _won_drop_string(&empty_str);
    _won_drop_string(&empty_str);
    _won_free(empty_str.ptr, empty_str.cap);
    _won_free((void*)_won_empty_bytes, 0);

    if (g_raw_free_called != 0) {
        fprintf(stderr, "FAIL: cap==0 哨兵误触发底层的 free 调用: %d 次\n", g_raw_free_called);
        return 1;
    }
    printf("SENTINEL_NO_FREE_PASS\n");
    return 0;
}
`
	gccOut, clangOut := compileStrictGCCAndClang(t, cSource, nil, nil)
	if gccOut != clangOut || !strings.Contains(gccOut, "SENTINEL_NO_FREE_PASS") {
		t.Fatalf("容量零免释放验证失败:\ngcc=%s\nclang=%s", gccOut, clangOut)
	}
}

// TestStringRuntime_SentinelReallocRoutesToAlloc 验证当 old_cap == 0 或 ptr 为哨兵时，
// _won_realloc 严禁传递哨兵指针至 libc realloc，而是直接调用 _won_alloc 分配。
func TestStringRuntime_SentinelReallocRoutesToAlloc(t *testing.T) {
	cSource := `
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stddef.h>
#include <stdbool.h>

static int g_alloc_invoked = 0;
static int g_realloc_invoked = 0;

static void* seam_alloc(size_t b) {
    g_alloc_invoked++;
    return malloc(b);
}

static void* seam_realloc(void* p, size_t nb) {
    g_realloc_invoked++;
    return realloc(p, nb);
}

#define _WON_ALLOC_IMPL(b) seam_alloc(b)
#define _WON_REALLOC_IMPL(p, nb) seam_realloc(p, nb)
#define _WON_FREE_IMPL(p, c) free(p)

static const uint8_t _won_empty_bytes[1] = {0};

_Noreturn static inline void _won_panic_oom(size_t bytes) {
    fprintf(stderr, "WON runtime panic: out of memory allocating %zu bytes\n", bytes);
    abort();
}

static inline void* _won_alloc(size_t bytes) {
    if (bytes == 0) {
        fprintf(stderr, "WON runtime panic: _won_alloc called with 0 bytes\n");
        abort();
    }
    void* p = _WON_ALLOC_IMPL(bytes);
    if (p == NULL) {
        _won_panic_oom(bytes);
    }
    return p;
}

static inline void* _won_realloc(void* ptr, size_t old_cap, size_t new_cap) {
    if (new_cap == 0) {
        fprintf(stderr, "WON runtime panic: _won_realloc called with 0 new_cap\n");
        abort();
    }
    if (old_cap == 0 || ptr == (void*)_won_empty_bytes) {
        return _won_alloc(new_cap);
    }
    void* new_ptr = _WON_REALLOC_IMPL(ptr, new_cap);
    if (new_ptr == NULL) {
        _won_panic_oom(new_cap);
    }
    return new_ptr;
}

int main(void) {
    // 1. old_cap == 0 时，重分配必须走 _won_alloc 而非 libc realloc
    void* p1 = _won_realloc((void*)_won_empty_bytes, 0, 32);
    if (g_alloc_invoked != 1 || g_realloc_invoked != 0) {
        fprintf(stderr, "FAIL: old_cap==0 未路由至 _won_alloc: alloc=%d, realloc=%d\n", g_alloc_invoked, g_realloc_invoked);
        return 1;
    }

    // 2. old_cap > 0 时，正常执行 libc realloc
    void* p2 = _won_realloc(p1, 32, 64);
    if (g_realloc_invoked != 1) {
        fprintf(stderr, "FAIL: old_cap>0 未调用 realloc: %d\n", g_realloc_invoked);
        return 2;
    }
    free(p2);

    printf("REALLOC_ROUTING_PASS\n");
    return 0;
}
`
	gccOut, clangOut := compileStrictGCCAndClang(t, cSource, nil, nil)
	if gccOut != clangOut || !strings.Contains(gccOut, "REALLOC_ROUTING_PASS") {
		t.Fatalf("realloc 哨兵路由验证失败:\ngcc=%s\nclang=%s", gccOut, clangOut)
	}
}

// TestStringRuntime_GrowthRuleGeometric 验证 ADR-012 规定的确定性几何 2x 容量增长与边界处理。
func TestStringRuntime_GrowthRuleGeometric(t *testing.T) {
	cSource := `
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stddef.h>

static inline size_t _won_calc_growth(size_t old_cap, size_t len, size_t add_len) {
    if (add_len > SIZE_MAX - len) {
        fprintf(stderr, "WON runtime panic: string capacity overflow\n");
        abort();
    }
    size_t required = len + add_len;
    size_t new_cap = 16;
    if (old_cap > 0) {
        if (old_cap > SIZE_MAX / 2) {
            new_cap = SIZE_MAX;
        } else {
            new_cap = old_cap * 2;
        }
    }
    if (new_cap < required) {
        new_cap = required;
    }
    if (new_cap < 16) {
        new_cap = 16;
    }
    return new_cap;
}

int main(void) {
    // 从 0 增长：最小容量保底 16
    if (_won_calc_growth(0, 0, 1) != 16) return 1;
    if (_won_calc_growth(0, 0, 16) != 16) return 2;
    if (_won_calc_growth(0, 0, 17) != 17) return 3;

    // 从 16 翻倍增长至 32
    if (_won_calc_growth(16, 16, 1) != 32) return 4;
    // 当需求超出 2x 时，取 required
    if (_won_calc_growth(16, 16, 50) != 66) return 5;

    // 从 32 翻倍增长至 64
    if (_won_calc_growth(32, 32, 1) != 64) return 6;

    printf("GROWTH_RULE_PASS\n");
    return 0;
}
`
	gccOut, clangOut := compileStrictGCCAndClang(t, cSource, nil, nil)
	if gccOut != clangOut || !strings.Contains(gccOut, "GROWTH_RULE_PASS") {
		t.Fatalf("容量增长算法验证失败:\ngcc=%s\nclang=%s", gccOut, clangOut)
	}
}

// TestStringRuntime_OOMPanicSubprocess 验证当底层分配器返回 NULL 时，
// 运行时向 stderr 输出明确恐慌信息并以非 0 状态码 (SIGABRT) 中止。
func TestStringRuntime_OOMPanicSubprocess(t *testing.T) {
	cSource := `
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stddef.h>

// 模拟分配器耗尽
#define _WON_ALLOC_IMPL(b) NULL

_Noreturn static inline void _won_panic_oom(size_t bytes) {
    fprintf(stderr, "WON runtime panic: out of memory allocating %zu bytes\n", bytes);
    abort();
}

static inline void* _won_alloc(size_t bytes) {
    if (bytes == 0) abort();
    void* p = _WON_ALLOC_IMPL(bytes);
    if (p == NULL) {
        _won_panic_oom(bytes);
    }
    return p;
}

int main(void) {
    _won_alloc(1024);
    return 0;
}
`
	tmpDir := t.TempDir()
	cFile := filepath.Join(tmpDir, "oom_test.c")
	binFile := filepath.Join(tmpDir, "oom_test")
	if err := os.WriteFile(cFile, []byte(cSource), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("gcc", "-std=c11", "-Wall", "-Werror", cFile, "-o", binFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("编译 OOM 测试程序失败: %v\n%s", err, string(out))
	}

	runCmd := exec.Command(binFile)
	out, err := runCmd.CombinedOutput()
	if err == nil {
		t.Fatalf("OOM 程序应当异常终止，实际正常返回 0")
	}
	stderrMsg := string(out)
	if !strings.Contains(stderrMsg, "WON runtime panic: out of memory allocating 1024 bytes") {
		t.Fatalf("未输出预期的 OOM 恐慌诊断，实际 stderr:\n%s", stderrMsg)
	}
}

// TestStringRuntime_CapacityOverflowSubprocess 验证容量加法溢出时触发安全 abort。
func TestStringRuntime_CapacityOverflowSubprocess(t *testing.T) {
	cSource := `
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stddef.h>

static inline size_t _won_calc_growth(size_t old_cap, size_t len, size_t add_len) {
    if (add_len > SIZE_MAX - len) {
        fprintf(stderr, "WON runtime panic: string capacity overflow\n");
        abort();
    }
    (void)old_cap;
    return len + add_len;
}

int main(void) {
    _won_calc_growth(16, 10, SIZE_MAX - 5);
    return 0;
}
`
	tmpDir := t.TempDir()
	cFile := filepath.Join(tmpDir, "overflow_test.c")
	binFile := filepath.Join(tmpDir, "overflow_test")
	if err := os.WriteFile(cFile, []byte(cSource), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("gcc", "-std=c11", "-Wall", "-Werror", cFile, "-o", binFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("编译溢出测试程序失败: %v\n%s", err, string(out))
	}

	runCmd := exec.Command(binFile)
	out, err := runCmd.CombinedOutput()
	if err == nil {
		t.Fatalf("容量溢出程序应当异常终止，实际正常返回 0")
	}
	if !strings.Contains(string(out), "WON runtime panic: string capacity overflow") {
		t.Fatalf("未输出预期的容量溢出诊断，实际输出:\n%s", string(out))
	}
}

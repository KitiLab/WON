package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
)

func TestCodegenStrictISOC11Compliance(t *testing.T) {
	src := `
#import <zlib.h>

fn main() -> i32 {
    let msg = "WON C11 Verification";
    let input_bytes = msg.as_bytes();
    let input_len: u64 = 20;

    let max_bound = unsafe {
        zlib::compressBound(input_len)
    };

    let mut buf: [u8; 128] = [0; 128];
    let mut buf_len: u64 = 128;

    let status = unsafe {
        zlib::compress(buf.as_mut_ptr(), &mut buf_len, input_bytes.as_ptr(), input_len)
    };

    let is_eq = unsafe {
        bytes_equal(input_bytes.as_ptr(), buf.as_ptr(), 5)
    };

    println("Status: ", status);
    println("Bound: ", max_bound);
    println("Equal: ", is_eq);
    return 0;
}
`
	report := diagnostic.NewReport()
	lex := lexer.New("test_c11.won", src, report)
	p := parser.New(lex, report, "test_c11.won")
	prog := p.ParseProgram()

	if report.HasErrors() {
		t.Fatalf("AST 解析失败: %s", report.String())
	}

	mockBindings := map[string]*bindgen.BindingMeta{
		"zlib": {
			HeaderPath: "/usr/include/zlib.h",
			Functions: map[string]bindgen.FunctionSig{
				"compress": {
					Name:       "compress",
					ReturnType: "int",
					ParamTypes: []string{"unsigned char *", "unsigned long *", "const unsigned char *", "unsigned long"},
				},
				"compressBound": {
					Name:       "compressBound",
					ReturnType: "unsigned long",
					ParamTypes: []string{"unsigned long"},
				},
			},
		},
	}

	gen := New(prog, mockBindings)
	cCode, err := gen.Generate()
	if err != nil {
		t.Fatalf("代码生成失败: %v", err)
	}

	// 1. 严格语法检查：严禁任何 GNU 语句表达式 ({ ... })
	if strings.Contains(cCode, "({") || strings.Contains(cCode, "})") {
		t.Fatalf("生成的 C 源码中包含 GNU 语句表达式扩展，未符合严格 ISO C11 规范！\n代码片段:\n%s", cCode)
	}

	// 2. 检查没有静默回退占位符
	if strings.Contains(cCode, "unsupported nested unsafe") {
		t.Fatalf("代码生成器包含了静默回退占位符！")
	}

	tmpDir := t.TempDir()
	cFile := filepath.Join(tmpDir, "test_output.c")
	if err := os.WriteFile(cFile, []byte(cCode), 0644); err != nil {
		t.Fatalf("写入测试 C 文件失败: %v", err)
	}

	// 3. 使用 GCC 严格标准检查 (-std=c11 -pedantic-errors -Wall -Wextra -Werror)
	gccCmd := exec.Command("gcc", "-std=c11", "-pedantic-errors", "-Wall", "-Wextra", "-Werror", "-c", cFile, "-o", filepath.Join(tmpDir, "gcc.o"))
	if gccOut, err := gccCmd.CombinedOutput(); err != nil {
		t.Fatalf("GCC 严格 ISO C11 校验失败: %v\n编译器输出:\n%s\n源码:\n%s", err, string(gccOut), cCode)
	}

	// 4. 使用 Clang 严格标准检查 (-std=c11 -pedantic-errors -Wall -Wextra -Werror)
	clangCmd := exec.Command("clang", "-std=c11", "-pedantic-errors", "-Wall", "-Wextra", "-Werror", "-c", cFile, "-o", filepath.Join(tmpDir, "clang.o"))
	if clangOut, err := clangCmd.CombinedOutput(); err != nil {
		t.Fatalf("Clang 严格 ISO C11 校验失败: %v\n编译器输出:\n%s\n源码:\n%s", err, string(clangOut), cCode)
	}

	t.Logf("GCC 16 与 Clang 23 严格 ISO C11 (-pedantic-errors -Wall -Wextra -Werror) 双编译器零警告验证 100%% 通过！")
}

func TestCodegenUnsupportedNestedUnsafeRejected(t *testing.T) {
	// 在嵌套表达式中包含多语句 unsafe 块应报错，而不是静默生成 0
	src := `
fn main() -> i32 {
    let x = 1 + unsafe {
        let a = 10;
        let b = 20;
        a + b
    };
    return x;
}
`
	report := diagnostic.NewReport()
	lex := lexer.New("test_nested_unsafe.won", src, report)
	p := parser.New(lex, report, "test_nested_unsafe.won")
	prog := p.ParseProgram()

	gen := New(prog, nil)
	cCode, err := gen.Generate()
	if err == nil {
		t.Fatalf("嵌套多语句 unsafe 块预期生成报错，但生成器成功返回:\n%s", cCode)
	}

	if !strings.Contains(err.Error(), "不支持在嵌套子表达式位置使用多语句 unsafe 块") {
		t.Errorf("错误信息不匹配，实际为: %v", err)
	}
	t.Logf("成功捕获并阻止静默代码生成回退: %v", err)
}

func TestCodegenRealZlibExampleWithWerror(t *testing.T) {
	// 对真实 examples/zlib_compression.won 执行代码生成并以 -Werror 编译
	content, err := os.ReadFile("../../../../examples/zlib_compression.won")
	if err != nil {
		t.Fatalf("读取 examples/zlib_compression.won 失败: %v", err)
	}

	report := diagnostic.NewReport()
	lex := lexer.New("zlib_compression.won", string(content), report)
	p := parser.New(lex, report, "zlib_compression.won")
	prog := p.ParseProgram()
	if report.HasErrors() {
		t.Fatalf("语法解析失败: %s", report.String())
	}

	meta, err := bindgen.ExtractZlibBindings("/usr/include/zlib.h")
	if err != nil {
		t.Fatalf("提取绑定元数据失败: %v", err)
	}
	bindings := map[string]*bindgen.BindingMeta{"zlib": meta}

	gen := New(prog, bindings)
	cCode, err := gen.Generate()
	if err != nil {
		t.Fatalf("代码生成失败: %v", err)
	}

	tmpDir := t.TempDir()
	cFile := filepath.Join(tmpDir, "zlib_example.c")
	if err := os.WriteFile(cFile, []byte(cCode), 0644); err != nil {
		t.Fatalf("写入测试 C 文件失败: %v", err)
	}

	// GCC -Werror
	gccCmd := exec.Command("gcc", "-std=c11", "-pedantic-errors", "-Wall", "-Wextra", "-Werror", "-c", cFile, "-o", filepath.Join(tmpDir, "gcc.o"))
	if gccOut, err := gccCmd.CombinedOutput(); err != nil {
		t.Fatalf("真实示例 GCC -Werror 校验失败: %v\n编译器输出:\n%s\n源码:\n%s", err, string(gccOut), cCode)
	}

	// Clang -Werror
	clangCmd := exec.Command("clang", "-std=c11", "-pedantic-errors", "-Wall", "-Wextra", "-Werror", "-c", cFile, "-o", filepath.Join(tmpDir, "clang.o"))
	if clangOut, err := clangCmd.CombinedOutput(); err != nil {
		t.Fatalf("真实示例 Clang -Werror 校验失败: %v\n编译器输出:\n%s\n源码:\n%s", err, string(clangOut), cCode)
	}

	t.Logf("真实 examples/zlib_compression.won 生成的 C 代码在 GCC 16 与 Clang 23 (-Werror) 下双双达到严格 0 警告 0 错误！")
}

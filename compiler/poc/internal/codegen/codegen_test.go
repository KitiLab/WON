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

	tmpDir := t.TempDir()
	cFile := filepath.Join(tmpDir, "test_output.c")
	if err := os.WriteFile(cFile, []byte(cCode), 0644); err != nil {
		t.Fatalf("写入测试 C 文件失败: %v", err)
	}

	// 2. 使用 GCC 严格标准检查 (-std=c11 -pedantic-errors -Wall -Wextra)
	gccCmd := exec.Command("gcc", "-std=c11", "-pedantic-errors", "-Wall", "-Wextra", "-c", cFile, "-o", filepath.Join(tmpDir, "gcc.o"))
	if gccOut, err := gccCmd.CombinedOutput(); err != nil {
		t.Fatalf("GCC 严格 ISO C11 校验失败: %v\n编译器输出:\n%s\n源码:\n%s", err, string(gccOut), cCode)
	}

	// 3. 使用 Clang 严格标准检查 (-std=c11 -pedantic-errors -Wall -Wextra)
	clangCmd := exec.Command("clang", "-std=c11", "-pedantic-errors", "-Wall", "-Wextra", "-c", cFile, "-o", filepath.Join(tmpDir, "clang.o"))
	if clangOut, err := clangCmd.CombinedOutput(); err != nil {
		t.Fatalf("Clang 严格 ISO C11 校验失败: %v\n编译器输出:\n%s\n源码:\n%s", err, string(clangOut), cCode)
	}

	t.Logf("GCC 16 与 Clang 23 严格 ISO C11 (-pedantic-errors -Wall -Wextra) 双编译器验证 100%% 通过！")
}

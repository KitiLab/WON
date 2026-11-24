package checker

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
)

func getMockZlibBindings() map[string]*bindgen.BindingMeta {
	return map[string]*bindgen.BindingMeta{
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
}

func TestCheckerMutabilityAndUnsafe(t *testing.T) {
	srcMutErr := `
fn main() -> i32 {
    let x = 10;
    x = 20;
    return 0;
}
`
	report := diagnostic.NewReport()
	lex := lexer.New("test_mut.won", srcMutErr, report)
	p := parser.New(lex, report, "test_mut.won")
	prog := p.ParseProgram()

	c := New(report, nil)
	c.CheckProgram(prog)

	if !report.HasErrors() {
		t.Fatalf("给不可变变量赋值应被语义检查器拦截")
	}

	diag := report.Diagnostics[0]
	if diag.Code != "WON-MUT-001" {
		t.Errorf("期望错误码 WON-MUT-001, 实际为 %s", diag.Code)
	}
	if len(diag.Fixes) == 0 {
		t.Errorf("诊断中应包含自动修复建议")
	}
}

func TestCheckerForeignCallOutsideUnsafe(t *testing.T) {
	srcUnsafeErr := `
fn main() -> i32 {
    let status = zlib::compress(0, 0, 0, 0);
    return status;
}
`
	report := diagnostic.NewReport()
	lex := lexer.New("test_unsafe.won", srcUnsafeErr, report)
	p := parser.New(lex, report, "test_unsafe.won")
	prog := p.ParseProgram()

	c := New(report, getMockZlibBindings())
	c.CheckProgram(prog)

	if !report.HasErrors() {
		t.Fatalf("在 unsafe 块外部调用 C 外部函数应被拦截")
	}

	diag := report.Diagnostics[0]
	if diag.Code != "WON-SEC-001" {
		t.Errorf("期望安全隔离错误码 WON-SEC-001, 实际为 %s", diag.Code)
	}
}

func TestCheckerUnknownNamespace(t *testing.T) {
	src := `
fn main() -> i32 {
    let s = unsafe {
        unknown_lib::foo(1)
    };
    return 0;
}
`
	report := diagnostic.NewReport()
	lex := lexer.New("test_ns.won", src, report)
	p := parser.New(lex, report, "test_ns.won")
	prog := p.ParseProgram()

	c := New(report, getMockZlibBindings())
	c.CheckProgram(prog)

	if !report.HasErrors() {
		t.Fatalf("调用未导入的 C 命名空间应被语义分析器拦截")
	}

	if report.Diagnostics[0].Code != "WON-FFI-001" {
		t.Errorf("期望错误码 WON-FFI-001, 实际为 %s", report.Diagnostics[0].Code)
	}
}

func TestCheckerUnknownSymbol(t *testing.T) {
	src := `
fn main() -> i32 {
    let s = unsafe {
        zlib::non_existent_function(1)
    };
    return 0;
}
`
	report := diagnostic.NewReport()
	lex := lexer.New("test_sym.won", src, report)
	p := parser.New(lex, report, "test_sym.won")
	prog := p.ParseProgram()

	c := New(report, getMockZlibBindings())
	c.CheckProgram(prog)

	if !report.HasErrors() {
		t.Fatalf("调用头文件中不存在的 C 符号应被语义分析器拦截")
	}

	if report.Diagnostics[0].Code != "WON-FFI-002" {
		t.Errorf("期望错误码 WON-FFI-002, 实际为 %s", report.Diagnostics[0].Code)
	}
}

func TestCheckerArgumentCountMismatch(t *testing.T) {
	src := `
fn main() -> i32 {
    let s = unsafe {
        zlib::compress(1, 2)
    };
    return 0;
}
`
	report := diagnostic.NewReport()
	lex := lexer.New("test_args.won", src, report)
	p := parser.New(lex, report, "test_args.won")
	prog := p.ParseProgram()

	c := New(report, getMockZlibBindings())
	c.CheckProgram(prog)

	if !report.HasErrors() {
		t.Fatalf("调用参数数量不匹配的 C 函数应被语义分析器拦截")
	}

	if report.Diagnostics[0].Code != "WON-FFI-003" {
		t.Errorf("期望错误码 WON-FFI-003, 实际为 %s", report.Diagnostics[0].Code)
	}
}

func TestCheckerBytesEqualArity(t *testing.T) {
	src := `
fn main() -> i32 {
    let eq = bytes_equal(1, 2);
    return 0;
}
`
	report := diagnostic.NewReport()
	lex := lexer.New("test_bytes_eq.won", src, report)
	p := parser.New(lex, report, "test_bytes_eq.won")
	prog := p.ParseProgram()

	c := New(report, nil)
	c.CheckProgram(prog)

	if !report.HasErrors() {
		t.Fatalf("调用 bytes_equal 参数不足 3 个应被拦截")
	}

	if report.Diagnostics[0].Code != "WON-TYP-001" {
		t.Errorf("期望错误码 WON-TYP-001, 实际为 %s", report.Diagnostics[0].Code)
	}
}

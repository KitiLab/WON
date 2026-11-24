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
				"uncompress": {
					Name:       "uncompress",
					ReturnType: "int",
					ParamTypes: []string{"unsigned char *", "unsigned long *", "const unsigned char *", "unsigned long"},
				},
				"zlibVersion": {
					Name:       "zlibVersion",
					ReturnType: "const char *",
					ParamTypes: []string{},
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
    let ver = zlib::zlibVersion();
    return 0;
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

	found := false
	for _, diag := range report.Diagnostics {
		if diag.Code == "WON-SEC-001" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("期望安全隔离错误码 WON-SEC-001, 实际为 %s", report.String())
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

func TestCheckerStringWhereMutablePointerRequired(t *testing.T) {
	src := `
fn main() -> i32 {
    let mut len: u64 = 10;
    let s = unsafe {
        zlib::compress("dest_string", &mut len, "src".as_ptr(), 3)
    };
    return 0;
}
`
	report := diagnostic.NewReport()
	lex := lexer.New("test_str_err.won", src, report)
	p := parser.New(lex, report, "test_str_err.won")
	prog := p.ParseProgram()

	c := New(report, getMockZlibBindings())
	c.CheckProgram(prog)

	if !report.HasErrors() {
		t.Fatalf("传递字符串给期望可变字节指针的 C 参数应被拦截")
	}

	found := false
	for _, d := range report.Diagnostics {
		if d.Code == "WON-FFI-004" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("期望类型错误码 WON-FFI-004, 实际输出: %s", report.String())
	}
}

func TestCheckerIntegerWherePointerRequired(t *testing.T) {
	src := `
fn main() -> i32 {
    let mut len: u64 = 10;
    let s = unsafe {
        zlib::compress(12345, &mut len, "src".as_ptr(), 3)
    };
    return 0;
}
`
	report := diagnostic.NewReport()
	lex := lexer.New("test_int_err.won", src, report)
	p := parser.New(lex, report, "test_int_err.won")
	prog := p.ParseProgram()

	c := New(report, getMockZlibBindings())
	c.CheckProgram(prog)

	if !report.HasErrors() {
		t.Fatalf("传递整数给期望指针的 C 参数应被拦截")
	}

	found := false
	for _, d := range report.Diagnostics {
		if d.Code == "WON-FFI-004" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("期望类型错误码 WON-FFI-004, 实际输出: %s", report.String())
	}
}

func TestCheckerImmutablePointerWhereMutablePointerRequired(t *testing.T) {
	src := `
fn main() -> i32 {
    let buf = "immutable_buffer";
    let mut len: u64 = 10;
    let s = unsafe {
        zlib::compress(buf.as_ptr(), &mut len, "src".as_ptr(), 3)
    };
    return 0;
}
`
	report := diagnostic.NewReport()
	lex := lexer.New("test_immut_ptr_err.won", src, report)
	p := parser.New(lex, report, "test_immut_ptr_err.won")
	prog := p.ParseProgram()

	c := New(report, getMockZlibBindings())
	c.CheckProgram(prog)

	if !report.HasErrors() {
		t.Fatalf("传递只读指针 (*const u8) 给期望可变指针 (*mut u8) 的 C 参数应被拦截")
	}

	found := false
	for _, d := range report.Diagnostics {
		if d.Code == "WON-FFI-004" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("期望类型错误码 WON-FFI-004, 实际输出: %s", report.String())
	}
}

func TestCheckerUnsupportedZlibFunctionType(t *testing.T) {
	mockBindings := getMockZlibBindings()
	// 注入一个 Phase 0B 原型尚未支持的函数
	mockBindings["zlib"].Functions["deflateInit_"] = bindgen.FunctionSig{
		Name:       "deflateInit_",
		QualType:   "int (void *, int, const char *, int)",
		ReturnType: "int",
		ParamTypes: []string{"void *", "int", "const char *", "int"},
	}

	src := `
fn main() -> i32 {
    let s = unsafe {
        zlib::deflateInit_(0, 0, "1.2.3", 0)
    };
    return 0;
}
`
	report := diagnostic.NewReport()
	lex := lexer.New("test_unsupported_fn.won", src, report)
	p := parser.New(lex, report, "test_unsupported_fn.won")
	prog := p.ParseProgram()

	c := New(report, mockBindings)
	c.CheckProgram(prog)

	if !report.HasErrors() {
		t.Fatalf("调用 Phase 0B 范围之外未验证的 C 符号应被拦截")
	}

	found := false
	for _, d := range report.Diagnostics {
		if d.Code == "WON-FFI-005" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("期望不受支持符号错误码 WON-FFI-005, 实际输出: %s", report.String())
	}
}

func TestCheckerVoidFunctionAssignment(t *testing.T) {
	mockBindings := getMockZlibBindings()
	mockBindings["payload"] = &bindgen.BindingMeta{
		HeaderPath: "payload_loader.h",
		Functions: map[string]bindgen.FunctionSig{
			"load_benchmark_payload": {
				Name:       "load_benchmark_payload",
				QualType:   "void (const char *, unsigned char *, unsigned long)",
				ReturnType: "void",
				ParamTypes: []string{"const char *", "unsigned char *", "unsigned long"},
			},
		},
	}

	src := `
fn main() -> i32 {
    let mut buf: [u8; 10] = [0; 10];
    let res = unsafe {
        payload::load_benchmark_payload("test.txt", buf.as_mut_ptr(), 10)
    };
    return 0;
}
`
	report := diagnostic.NewReport()
	lex := lexer.New("test_void_err.won", src, report)
	p := parser.New(lex, report, "test_void_err.won")
	prog := p.ParseProgram()

	c := New(report, mockBindings)
	c.CheckProgram(prog)

	if !report.HasErrors() {
		t.Fatalf("将返回值为 void 的外部 C 函数赋值给变量应被拦截")
	}

	found := false
	for _, d := range report.Diagnostics {
		if d.Code == "WON-FFI-006" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("期望 void 赋值错误码 WON-FFI-006, 实际输出: %s", report.String())
	}
}

func TestCheckerValidZlibCallsPass(t *testing.T) {
	src := `
fn main() -> i32 {
    let msg = "Hello";
    let input_bytes = msg.as_bytes();
    let input_len: u64 = 5;

    let max_bound = unsafe {
        zlib::compressBound(input_len)
    };

    let mut comp_buf: [u8; 64] = [0; 64];
    let mut comp_len: u64 = 64;

    let status = unsafe {
        zlib::compress(comp_buf.as_mut_ptr(), &mut comp_len, input_bytes.as_ptr(), input_len)
    };

    return 0;
}
`
	report := diagnostic.NewReport()
	lex := lexer.New("test_valid.won", src, report)
	p := parser.New(lex, report, "test_valid.won")
	prog := p.ParseProgram()

	c := New(report, getMockZlibBindings())
	c.CheckProgram(prog)

	if report.HasErrors() {
		t.Fatalf("合法的 C FFI 函数调用应完全通过语义校验，但产生错误:\n%s", report.String())
	}
}

package checker

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
)

func TestCheckerMutabilityAndUnsafe(t *testing.T) {
	// 测试未声明 mut 进行赋值与借用
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

	c := New(report)
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

	c := New(report)
	c.CheckProgram(prog)

	if !report.HasErrors() {
		t.Fatalf("在 unsafe 块外部调用 C 外部函数应被拦截")
	}

	diag := report.Diagnostics[0]
	if diag.Code != "WON-SEC-001" {
		t.Errorf("期望安全隔离错误码 WON-SEC-001, 实际为 %s", diag.Code)
	}
}

package parser

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
)

func TestParserValidProgram(t *testing.T) {
	src := `
#import <zlib.h>

fn main() -> i32 {
    let mut buf: [u8; 256] = [0; 256];
    let len = 256;
    while (len > 0) {
        return 0;
    }
    return 0;
}
`
	report := diagnostic.NewReport()
	lex := lexer.New("test.won", src, report)
	p := New(lex, report, "test.won")
	prog := p.ParseProgram()

	if report.HasErrors() {
		t.Fatalf("有效代码解析不应产生语法错误: %s", report.String())
	}

	if len(prog.Imports) != 1 || prog.Imports[0].HeaderPath != "<zlib.h>" {
		t.Errorf("期望正确解析 1 个头文件引入 <zlib.h>")
	}

	if len(prog.Functions) != 1 || prog.Functions[0].Name != "main" {
		t.Errorf("期望正确解析 main 函数")
	}
}

func TestParserInvalidSyntaxMissingBrace(t *testing.T) {
	src := `
fn main() -> i32
    return 0;
}
`
	report := diagnostic.NewReport()
	lex := lexer.New("bad.won", src, report)
	p := New(lex, report, "bad.won")
	p.ParseProgram()

	if !report.HasErrors() {
		t.Fatalf("缺少花括号的代码应被语法解析器捕获")
	}

	d := report.Diagnostics[0]
	if d.Code != "WON-SYN-007" {
		t.Errorf("期望错误码 WON-SYN-007, 实际为 %s", d.Code)
	}
}

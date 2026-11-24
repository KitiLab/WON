package codegen

import (
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
)

func TestCodegenValidC(t *testing.T) {
	src := `
#import <zlib.h>

fn main() -> i32 {
    let msg = "test";
    return 0;
}
`
	report := diagnostic.NewReport()
	lex := lexer.New("test.won", src, report)
	p := parser.New(lex, report, "test.won")
	prog := p.ParseProgram()

	gen := New(prog, nil)
	cCode, err := gen.Generate()
	if err != nil {
		t.Fatalf("代码生成失败: %v", err)
	}

	if !strings.Contains(cCode, "#include <zlib.h>") {
		t.Errorf("生成的 C 代码缺少 #include <zlib.h>")
	}
	if !strings.Contains(cCode, "int main(void) {") {
		t.Errorf("生成的 C 代码缺少 int main(void)")
	}
}

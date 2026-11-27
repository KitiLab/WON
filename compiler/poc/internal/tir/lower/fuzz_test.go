package lower_test

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
	"github.com/KitiLab/WON/compiler/poc/internal/tir"
	"github.com/KitiLab/WON/compiler/poc/internal/tir/lower"
)

// FuzzFrontendToTIR 对前端解析->语义/所有权/Drop->TIR 降级与独立验证全链路进行模糊测试。
func FuzzFrontendToTIR(f *testing.F) {
	f.Add(`fn main() -> i32:
    let a = 1 + 2
    if a > 2:
        return a
    return 0
`)
	f.Add(`fn f(cond: bool) -> String:
    var s = String::from("hi")
    if cond:
        s = String::from("ok")
    return s
`)
	f.Add(`enum E:
    A(i32)
    B
fn m(e: E) -> i32:
    return match e:
        E::A(x) =>
            x + 1
        E::B =>
            0
`)

	f.Fuzz(func(t *testing.T, src string) {
		rep1 := diagnostic.NewReport()
		p1 := parser.New(lexer.New("fuzz.won", src, rep1), rep1, "fuzz.won").ParseProgram()
		if rep1.HasErrors() || p1 == nil {
			return
		}
		tp1 := checker.New(rep1, nil).CheckProgram(p1)
		if rep1.HasErrors() || tp1 == nil || !tp1.OwnershipChecked || !tp1.DropElaborated {
			return
		}

		tir1, err1 := lower.LowerProgram(tp1, nil)
		if err1 != nil {
			t.Fatalf("已通过 OwnershipChecked/DropElaborated 的程序 TIR 降级失败: %v\n源码:\n%s", err1, src)
		}
		if errs := tir.Verify(tir1); len(errs) != 0 {
			t.Fatalf("TIR 验证失败: %v\n源码:\n%s", tir.VerifyErrorSummary(errs), src)
		}

		// 验证同源码二次编译生成的 TIR 完全确定
		rep2 := diagnostic.NewReport()
		p2 := parser.New(lexer.New("fuzz.won", src, rep2), rep2, "fuzz.won").ParseProgram()
		tp2 := checker.New(rep2, nil).CheckProgram(p2)
		tir2, err2 := lower.LowerProgram(tp2, nil)
		if err2 != nil {
			t.Fatalf("二次降级失败: %v", err2)
		}
		if tir1.Dump() != tir2.Dump() {
			t.Fatalf("TIR Dump 非确定性:\n=== 1 ===\n%s\n=== 2 ===\n%s", tir1.Dump(), tir2.Dump())
		}
	})
}

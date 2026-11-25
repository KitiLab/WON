package borrow_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
)

// FuzzOwnershipCFGAndDataflow 验证随机控制流、未初始化声明、移动与部分移动组合下分析器的无崩溃与确定性。
func FuzzOwnershipCFGAndDataflow(f *testing.F) {
	f.Add(uint8(0), uint8(1), uint8(2), uint8(3))
	f.Add(uint8(5), uint8(2), uint8(7), uint8(1))
	f.Add(uint8(3), uint8(3), uint8(4), uint8(0))

	f.Fuzz(func(t *testing.T, op1, op2, op3, op4 uint8) {
		var sb strings.Builder
		sb.WriteString("struct Box:\n    var a: String\n    var b: String\n\n")
		sb.WriteString("fn eat(s: String):\n    println(s)\n\n")
		sb.WriteString("fn fuzz_fn(cond: bool) -> u64:\n")
		if op1%2 == 0 {
			sb.WriteString("    var s: String\n")
		} else {
			sb.WriteString("    var s = String::from(\"init\")\n")
		}
		sb.WriteString("    var p = Box { a: String::from(\"x\"), b: String::from(\"y\") }\n")

		emitStep := func(code uint8, indent string) {
			switch code % 6 {
			case 0:
				sb.WriteString(indent + "s = String::from(\"set\")\n")
			case 1:
				sb.WriteString(indent + "eat(s)\n")
			case 2:
				sb.WriteString(indent + "let _pa = p.a\n")
			case 3:
				sb.WriteString(indent + "p.a = String::from(\"ra\")\n")
			case 4:
				sb.WriteString(indent + "let _pb = p.b\n")
			case 5:
				sb.WriteString(indent + "return 0u64\n")
			}
		}

		sb.WriteString("    if cond:\n")
		emitStep(op2, "        ")
		sb.WriteString("    else:\n")
		emitStep(op3, "        ")

		if op4%2 == 0 {
			sb.WriteString("    while cond:\n")
			emitStep(op4, "        ")
			sb.WriteString("        break\n")
		}
		sb.WriteString(fmt.Sprintf("    return p.b.len()\n"))

		src := sb.String()
		runOnce := func() (string, bool) {
			rep := diagnostic.NewReport()
			lex := lexer.New("fuzz_own.won", src, rep)
			p := parser.New(lex, rep, "fuzz_own.won")
			prog := p.ParseProgram()
			if !rep.HasErrors() {
				chk := checker.New(rep, nil)
				chk.CheckProgram(prog)
			}
			j, _ := rep.ToJSON()
			return j, rep.HasErrors()
		}

		out1, err1 := runOnce()
		out2, err2 := runOnce()
		if err1 != err2 || out1 != out2 {
			t.Fatalf("同一所有权源码重复分析结果不确定:\n1:%s\n2:%s", out1, out2)
		}
	})
}

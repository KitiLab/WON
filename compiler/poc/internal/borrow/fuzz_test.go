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
		} else {
			sb.WriteString("    let _m = match p:\n        Box { a: ref ra, b: _ } if cond => ra.len()\n        _ => 0u64\n")
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

// FuzzBorrowNLLAndProvenance 验证随机组合的共享/独占借用、重借用、NLL last-use、候选返回与作用域交互无崩溃与结果确定性。
func FuzzBorrowNLLAndProvenance(f *testing.F) {
	f.Add(uint8(0), uint8(1), uint8(2), uint8(3), uint8(0))
	f.Add(uint8(3), uint8(2), uint8(1), uint8(0), uint8(1))
	f.Add(uint8(2), uint8(4), uint8(3), uint8(2), uint8(2))

	f.Fuzz(func(t *testing.T, a, b, c, d, e uint8) {
		var sb strings.Builder
		sb.WriteString("struct Pair:\n    var l: String\n    var r: String\n\n")
		sb.WriteString("fn choose(cond: bool, x: &String, y: &String) -> &String:\n")
		if e%2 == 0 {
			sb.WriteString("    if cond:\n        return x\n    return y\n\n")
		} else {
			sb.WriteString("    return x\n\n")
		}
		sb.WriteString("fn harness(cond: bool) -> i32:\n")
		sb.WriteString("    var p = Pair { l: String::from(\"a\"), r: String::from(\"b\") }\n")
		sb.WriteString("    var rx = &p.l\n")
		if a%2 == 0 {
			sb.WriteString("    var mr = &mut p.r\n")
		} else {
			sb.WriteString("    var mr = &mut p.l\n")
		}
		if b%2 == 0 {
			sb.WriteString("    let child = &*mr\n    println(child)\n")
		}
		switch c % 3 {
		case 0:
			sb.WriteString("    let picked = choose(cond, rx, &p.r)\n    println(picked)\n")
		case 1:
			sb.WriteString("    p.l.push_str(\"!\")\n")
		case 2:
			sb.WriteString("    mr.push_str(\"?\")\n")
		}
		if d%2 == 0 {
			sb.WriteString("    println(rx)\n")
		} else {
			sb.WriteString("    println(mr)\n")
		}
		sb.WriteString("    return 0\n")

		src := sb.String()
		runOnce := func() (string, bool, bool) {
			rep := diagnostic.NewReport()
			lex := lexer.New("fuzz_borrow.won", src, rep)
			p := parser.New(lex, rep, "fuzz_borrow.won")
			prog := p.ParseProgram()
			checked := false
			if !rep.HasErrors() {
				chk := checker.New(rep, nil)
				tp := chk.CheckProgram(prog)
				if tp != nil {
					checked = tp.OwnershipChecked
				}
			}
			// 任何报错程序绝对不允许 OwnershipChecked == true
			if rep.HasErrors() && checked {
				t.Fatalf("报错程序错误放行了 OwnershipChecked=true:\n%s", rep.String())
			}
			j, _ := rep.ToJSON()
			return j, rep.HasErrors(), checked
		}

		j1, e1, ok1 := runOnce()
		j2, e2, ok2 := runOnce()
		if j1 != j2 || e1 != e2 || ok1 != ok2 {
			t.Fatalf("Borrow/NLL Fuzz 结果不确定:\n1:%s\n2:%s", j1, j2)
		}
	})
}

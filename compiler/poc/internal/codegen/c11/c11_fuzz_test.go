package c11_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/codegen/c11"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
	"github.com/KitiLab/WON/compiler/poc/internal/tir/lower"
)

// FuzzC11Emitter 对从任意 WON 源码降级而成的 TIR 执行纯 TIR->C11 发射器属性模糊测试：
// 1. 严禁发生 panic；
// 2. 不受支持结构必须 Fail-Closed 返回结构化 WON-GEN-002；
// 3. 合法程序生成源码严禁含 GNU 扩展或未解析占位符。
func FuzzC11Emitter(f *testing.F) {
	f.Add("fn main() -> i32:\n    return 0\n")
	f.Add("fn f(a: i32, b: bool) -> i32:\n    if b:\n        return a + 1\n    return a - 1\nfn main() -> i32:\n    return f(2, true) - 3\n")
	f.Add("struct S:\n    v: i32\nfn main() -> i32:\n    let s = S { v: 1 }\n    return s.v\n")
	f.Add("fn main() -> i32:\n    let s = String::from(\"x\")\n    println(s)\n    return 0\n")
	f.Add("fn main() -> unit:\n    return ()\n")

	em := c11.NewEmitter(c11.DefaultLinuxX86_64())
	f.Fuzz(func(t *testing.T, src string) {
		rep := diagnostic.NewReport()
		lex := lexer.New("fuzz_c11.won", src, rep)
		p := parser.New(lex, rep, "fuzz_c11.won")
		prog := p.ParseProgram()
		if rep.HasErrors() || prog == nil {
			return
		}
		chk := checker.New(rep, nil)
		tp := chk.CheckProgram(prog)
		if rep.HasErrors() || tp == nil || !tp.OwnershipChecked || !tp.DropElaborated {
			return
		}
		tirProg, err := lower.LowerProgram(tp, nil)
		if err != nil {
			return
		}
		cCode, emitErr := em.Emit(tirProg)
		if emitErr != nil {
			var capErr *c11.CapabilityError
			if !errors.As(emitErr, &capErr) || (capErr.Code != "WON-GEN-002" && capErr.Code != "WON-INT-001") {
				t.Fatalf("未结构化拒止错误: %T %v", emitErr, emitErr)
			}
			return
		}
		if strings.Contains(cCode, "({") || strings.Contains(cCode, "<invalid>") {
			t.Fatalf("生成 C 源码包含非法扩展或无效类型占位:\n%s", cCode)
		}
	})
}

// FuzzStringLifecycleAndReallocation 模糊测试随机字符串构造、追加扩容与生命周期释放的鲁棒性。
func FuzzStringLifecycleAndReallocation(f *testing.F) {
	f.Add("hello", "world")
	f.Add("", "")
	f.Add("multibyte_世界", "long_chunk_forcing_reallocation_exceeding_initial_capacity")
	f.Add("a\x00b", "c\x00d")

	em := c11.NewEmitter(c11.DefaultLinuxX86_64())
	f.Fuzz(func(t *testing.T, s1, s2 string) {
		// 过滤导致语法解析歧义的极端控制字符
		if strings.ContainsAny(s1, "\r\n\"\\") || strings.ContainsAny(s2, "\r\n\"\\") {
			return
		}
		src := "fn main() -> i32:\n" +
			"    var s = String::from(\"" + s1 + "\")\n" +
			"    s.push_str(\"" + s2 + "\")\n" +
			"    let l = s.len()\n" +
			"    let cap = s.capacity()\n" +
			"    if cap < l:\n" +
			"        return 1\n" +
			"    println(s)\n" +
			"    return 0\n"

		rep := diagnostic.NewReport()
		lex := lexer.New("fuzz_string.won", src, rep)
		p := parser.New(lex, rep, "fuzz_string.won")
		prog := p.ParseProgram()
		if rep.HasErrors() || prog == nil {
			return
		}
		chk := checker.New(rep, nil)
		tp := chk.CheckProgram(prog)
		if rep.HasErrors() || tp == nil || !tp.OwnershipChecked || !tp.DropElaborated {
			return
		}
		tirProg, err := lower.LowerProgram(tp, nil)
		if err != nil {
			return
		}
		cCode, emitErr := em.Emit(tirProg)
		if emitErr != nil {
			var capErr *c11.CapabilityError
			if !errors.As(emitErr, &capErr) || capErr.Code != "WON-GEN-002" {
				t.Fatalf("未结构化拒止: %v", emitErr)
			}
			return
		}
		if strings.Contains(cCode, "<invalid>") {
			t.Fatalf("生成源码包含无效类型标记:\n%s", cCode)
		}
	})
}

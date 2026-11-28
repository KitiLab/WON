package cfg_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
)

func buildCFGFromSource(t *testing.T, src string) *cfg.ProgramCFG {
	t.Helper()
	rep := diagnostic.NewReport()
	lex := lexer.New("cfg_test.won", src, rep)
	p := parser.New(lex, rep, "cfg_test.won")
	prog := p.ParseProgram()
	if rep.HasErrors() {
		t.Fatalf("解析失败:\n%s", rep.String())
	}
	chk := checker.New(rep, nil)
	tp := chk.CheckProgram(prog)
	if tp == nil || tp.CFG == nil {
		t.Fatalf("未生成 TypedProgram.CFG, report:\n%s", rep.String())
	}
	return tp.CFG
}

// TestCFGConstructionAndDeterminism 验证分支、循环回边、早退、作用域边界与不可达块构造的完整性及确定性
func TestCFGConstructionAndDeterminism(t *testing.T) {
	src := `
fn branch_and_loop(cond: bool, limit: i32) -> i32:
    var acc: i32 = 0
    if cond:
        var inner: i32 = 1
        acc += inner
    else:
        return 42

    while acc < limit:
        acc += 1
        if acc == 3:
            break
    return acc
    let dead: i32 = 99
`
	cfg1 := buildCFGFromSource(t, src)
	cfg2 := buildCFGFromSource(t, src)

	if len(cfg1.Functions) != 1 {
		t.Fatalf("期望 1 个函数 CFG，实际 %d", len(cfg1.Functions))
	}
	f1 := cfg1.Functions[0]
	f2 := cfg2.Functions[0]

	dump1 := f1.Dump()
	dump2 := f2.Dump()
	if dump1 != dump2 {
		t.Fatalf("相同源码多次构建 CFG 快照不一致:\n---1---\n%s\n---2---\n%s", dump1, dump2)
	}

	if !strings.Contains(dump1, "while.cond") || !strings.Contains(dump1, "if.then") || !strings.Contains(dump1, "Return") || !strings.Contains(dump1, "unreachable") {
		t.Errorf("CFG 快照缺少预期的核心块类型:\n%s", dump1)
	}

	// 验证循环回边存在 (存在指向 while.cond 的多条入边)
	foundBackEdge := false
	for _, blk := range f1.Blocks {
		if blk.Label == "while.cond" && len(blk.Predecessors) >= 2 {
			foundBackEdge = true
			break
		}
	}
	if !foundBackEdge {
		t.Errorf("while.cond 缺少循环回边前驱:\n%s", dump1)
	}

	// 验证 Place 绑定到非零 SymbolID 且包含精确 Span
	hasSymPlace := false
	for _, blk := range f1.Blocks {
		for _, st := range blk.Statements {
			if st.Place.RootSymID != 0 && st.Span.Start.Line > 0 {
				hasSymPlace = true
				break
			}
		}
	}
	if !hasSymPlace {
		t.Errorf("CFG 语句缺少解析后的非零 SymbolID 或源码 Span")
	}
}

// TestMatchPerArmLoweringSnapshots 验证 match 分支独立降维、StructPattern 字段级投影与守卫落空流转的 CFG 结构。
func TestMatchPerArmLoweringSnapshots(t *testing.T) {
	refMatchSrc := `
enum OptStr:
    Some(String)
    None

fn probe(opt: OptStr) -> usize:
    return match opt:
        OptStr::Some(ref r) => r.len()
        OptStr::None => 0usize
`
	refCFG := buildCFGFromSource(t, refMatchSrc)
	refDump := refCFG.Functions[0].Dump()
	if !strings.Contains(refDump, "BorrowShared") {
		t.Errorf("纯引用 match 分支应发出 BorrowShared 事件:\n%s", refDump)
	}
	if strings.Contains(refDump, "Move opt") {
		t.Errorf("纯引用 match 分支不应提前 Move 主体 opt:\n%s", refDump)
	}

	partialSrc := `
struct PairStr:
    a: String
    b: String

fn eat(s: String) -> usize:
    return s.len()

fn split(pair: PairStr, flag: bool) -> usize:
    return match pair:
        PairStr { a: ref ra, b: ref rb } if flag => ra.len() + rb.len()
        PairStr { a, b: ref rb } => eat(a) + rb.len()
`
	partialCFG := buildCFGFromSource(t, partialSrc)
	partialDump := partialCFG.Functions[1].Dump()
	if !strings.Contains(partialDump, "match.arm0.gfail") {
		t.Errorf("带守卫 match 应构造守卫落空基本块 match.arm0.gfail:\n%s", partialDump)
	}
	if !regexp.MustCompile(`Move pair#\d+\.a`).MatchString(partialDump) {
		t.Errorf("Direct StructPattern 非 ref 字段应对精确字段路径 pair.a 发出 OpMove:\n%s", partialDump)
	}
	if !regexp.MustCompile(`BorrowShared pair#\d+\.b`).MatchString(partialDump) {
		t.Errorf("Direct StructPattern ref 字段应对精确字段路径 pair.b 发出 OpBorrowShared:\n%s", partialDump)
	}
}

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

func buildMatchProgram(t *testing.T, src string) (*checker.TypedProgram, *diagnostic.Report) {
	t.Helper()
	rep := diagnostic.NewReport()
	lex := lexer.New("match_closure.won", src, rep)
	p := parser.New(lex, rep, "match_closure.won")
	prog := p.ParseProgram()
	if rep.HasErrors() {
		t.Fatalf("源码解析失败:\n%s", rep.String())
	}
	chk := checker.New(rep, nil)
	tp := chk.CheckProgram(prog)
	return tp, rep
}

// TestPhase2B12MatchPatternCFGInvariants 逐条验证混合模式、Copy/Move/Borrow 事件、守卫控制流与不可达分支隔离不变量。
func TestPhase2B12MatchPatternCFGInvariants(t *testing.T) {
	t.Run("OpaqueRefTupleEmitsAllBorrowEventsAndZeroMoves", func(t *testing.T) {
		src := `
fn probe(mut_flag: bool) -> u64:
    var p = (String::from("a"), String::from("b"))
    return match p:
        (ref a, ref mut b) => a.len() + b.len()
`
		tp1, rep := buildMatchProgram(t, src)
		if rep.HasErrors() || !tp1.OwnershipChecked {
			t.Fatalf("合法引用元组匹配不应失败:\n%s", rep.String())
		}
		fnCFG := tp1.CFG.Functions[0]
		dump1 := fnCFG.Dump()

		// 快照确定性校验
		tp2, _ := buildMatchProgram(t, src)
		if dump2 := tp2.CFG.Functions[0].Dump(); dump1 != dump2 {
			t.Fatalf("CFG 快照不具备确定性:\n---1---\n%s\n---2---\n%s", dump1, dump2)
		}

		// 验证两条引用绑定均产生对应 Borrow 事件且无针对主体 p 的 Move
		if !strings.Contains(dump1, "BorrowShared(reserved)") || !strings.Contains(dump1, "BorrowMut(reserved)") {
			t.Errorf("混合 & 与 &mut 引用元组必须同时保留 BorrowShared 与 BorrowMut 事件:\n%s", dump1)
		}
		if regexp.MustCompile(`Move p#`).MatchString(dump1) {
			t.Errorf("仅引用绑定的元组匹配不得对主体产生 OpMove:\n%s", dump1)
		}
	})

	t.Run("CopyBindingsEmitOpCopyNotFalseMove", func(t *testing.T) {
		src := `
struct Mixed:
    count: i32
    label: String

fn eat(s: String) -> u64:
    return s.len()

fn check_copy(m: Mixed, flag: bool) -> i32:
    let _u = match m:
        Mixed { count, label } => eat(label)
    let opt = (10i32, true)
    return match opt:
        (x, y) if flag => x
        _ => 0i32
`
		tp, rep := buildMatchProgram(t, src)
		if rep.HasErrors() || !tp.OwnershipChecked {
			t.Fatalf("Copy+NonCopy 混合直接解构及带守卫 Copy 元组应当通过:\n%s", rep.String())
		}
		dump := tp.CFG.Functions[1].Dump()
		if !regexp.MustCompile(`Copy m#\d+\.count`).MatchString(dump) {
			t.Errorf("Direct StructPattern Copy 字段 m.count 应发出 OpCopy:\n%s", dump)
		}
		if regexp.MustCompile(`Move m#\d+\.count`).MatchString(dump) {
			t.Errorf("Copy 字段 m.count 严禁发出 OpMove:\n%s", dump)
		}
		if regexp.MustCompile(`Move opt#\d+`).MatchString(dump) {
			t.Errorf("纯 Copy 元组带守卫匹配不得产生 OpMove:\n%s", dump)
		}
	})

	t.Run("MixedOpaqueAggregateFailsWithSem010AndUncertified", func(t *testing.T) {
		src := `
fn eat(s: String) -> u64:
    return s.len()

fn bad() -> u64:
    let p = (String::from("r"), String::from("m"))
    return match p:
        (ref r, m) => r.len() + eat(m)
`
		tp, rep := buildMatchProgram(t, src)
		assertDiagCode(t, "MixedOpaqueTuple", rep, "WON-SEM-010")
		if tp.OwnershipChecked {
			t.Fatalf("存在不支持混合模式时 OwnershipChecked 必须保持 false")
		}
		// 不可伪报为普通用户 Move 错误 WON-MEM-001
		for _, d := range rep.Diagnostics {
			if d.Code == "WON-MEM-001" {
				t.Fatalf("不支持的 CFG 表示不应误报为 WON-MEM-001:\n%s", rep.String())
			}
		}
	})

	t.Run("GuardedConsumingPatternEmitsSem010AndNeverCommitsPrematureMove", func(t *testing.T) {
		src := `
enum OptStr:
    Some(String)
    None

fn eat(s: String) -> u64:
    return s.len()

fn guarded_move(o: OptStr, flag: bool) -> u64:
    return match o:
        OptStr::Some(v) if flag => eat(v)
        _ => 0u64
`
		tp, rep := buildMatchProgram(t, src)
		assertDiagCode(t, "GuardedConsumingEnum", rep, "WON-SEM-010")
		if tp.OwnershipChecked {
			t.Fatalf("带守卫非 Copy 按值匹配未认证时 OwnershipChecked 必须为 false")
		}
		fnCFG := tp.CFG.Functions[1]
		dump := fnCFG.Dump()
		if regexp.MustCompile(`Move o#\d+`).MatchString(dump) {
			t.Fatalf("被拒绝的带守卫消费分支绝不能提前提交 OpMove o:\n%s", dump)
		}
	})

	t.Run("MultiGuardCascadeCleansUpOnGuardFailEdges", func(t *testing.T) {
		src := `
enum OptStr:
    Some(String)
    None

fn multi_guard(opt: OptStr, c1: bool, c2: bool) -> u64:
    return match opt:
        OptStr::Some(ref r1) if c1 => r1.len()
        OptStr::Some(ref r2) if c2 => r2.len()
        _ => 0u64
`
		tp, rep := buildMatchProgram(t, src)
		if rep.HasErrors() || !tp.OwnershipChecked {
			t.Fatalf("多重引用守卫应当通过:\n%s", rep.String())
		}
		fnCFG := tp.CFG.Functions[0]
		dump := fnCFG.Dump()

		// 验证 arm0.gfail -> arm1.pat 以及 arm1.gfail -> arm2.pat 并且两者都含 ScopeExit
		var gfail0, gfail1 *cfg.BasicBlock
		for _, blk := range fnCFG.Blocks {
			switch blk.Label {
			case "match.arm0.gfail":
				gfail0 = blk
			case "match.arm1.gfail":
				gfail1 = blk
			}
		}
		if gfail0 == nil || gfail1 == nil {
			t.Fatalf("缺少预期的多守卫落空块:\n%s", dump)
		}
		hasScopeExit := func(b *cfg.BasicBlock) bool {
			for _, st := range b.Statements {
				if st.Kind == cfg.OpScopeExit {
					return true
				}
			}
			return false
		}
		if !hasScopeExit(gfail0) || !hasScopeExit(gfail1) {
			t.Errorf("每个 guardFailBB 在跳转到下一候选分支前必须执行 OpScopeExit:\n%s", dump)
		}
	})

	t.Run("DivergingArmDoesNotContaminateReachableMergeJoin", func(t *testing.T) {
		src := `
enum OptStr:
    Some(String)
    None

fn eat(s: String) -> u64:
    return s.len()

fn take(o: OptStr) -> u64:
    return 1u64

fn diverge_arm_clean_join(flag: bool) -> u64:
    let opt = OptStr::Some(String::from("ok"))
    let _x = match flag:
        true =>
            return eat(match_helper())
        false => 0u64
    return take(opt)

fn match_helper() -> String:
    return String::from("ret")
`
		tp, rep := buildMatchProgram(t, src)
		if rep.HasErrors() || !tp.OwnershipChecked {
			t.Fatalf("发散 match 分支不应污染合流点所有权状态:\n%s", rep.String())
		}
		_ = tp
	})
}

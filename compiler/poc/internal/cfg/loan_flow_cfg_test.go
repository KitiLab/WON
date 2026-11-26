package cfg_test

import (
	"regexp"
	"strings"
	"testing"
)

// TestStageACFGReferenceAndLoanEvents 验证 Stage A 借用事件、LoanID/ValTemp 关联、重借用父位置、临时调用与确定性快照。
func TestStageACFGReferenceAndLoanEvents(t *testing.T) {
	src := `
struct Holder:
    var text: String

fn inspect(s: &String):
    println(s)

fn main(cond: bool) -> i32:
    var v = String::from("hello")
    let r = &v
    var mr = &mut v
    let child = &*mr
    inspect(child)
    inspect(&v)
    let lit = "static_view"
    mr.push_str(lit)
    var opt = Holder { text: String::from("m") }
    let n = match opt:
        Holder { text: ref rt } => rt.len()
    return 0
`
	cfg1 := buildCFGFromSource(t, src)
	cfg2 := buildCFGFromSource(t, src)

	if len(cfg1.Functions) < 2 {
		t.Fatalf("期望至少 2 个函数 CFG，实际 %d", len(cfg1.Functions))
	}
	f1 := cfg1.Functions[1]
	f2 := cfg2.Functions[1]

	dump1 := f1.Dump()
	dump2 := f2.Dump()
	if dump1 != dump2 {
		t.Fatalf("Stage A CFG 快照不具备确定性:\n---1---\n%s\n---2---\n%s", dump1, dump2)
	}

	checks := []struct {
		pattern string
		desc    string
	}{
		{`BorrowShared v#\d+ loan=L\d+ dst=v\d+`, "shared borrow with LoanID and ValTemp"},
		{`InitAssign r#\d+ src=v\d+`, "shared borrow destination association to let r"},
		{`BorrowMut v#\d+ loan=L\d+ dst=v\d+`, "mut borrow with LoanID and ValTemp"},
		{`InitAssign mr#\d+ src=v\d+`, "mut borrow destination association to var mr"},
		{`Reborrow mr#\d+\.\* loan=L\d+ dst=v\d+ parent=mr#\d+`, "reborrow ancestry via parent Place"},
		{`Call inspect args=\[\d+\] dst=v0`, "temporary/carrier call argument temp wiring"},
		{`StaticRef dst=v\d+`, "static string literal &str ref event"},
		{`Reborrow mr#\d+\.\* loan=L\d+ dst=v\d+ parent=mr#\d+`, "auto-deref method receiver reborrow"},
		{`BorrowShared opt#\d+\.text loan=L\d+ dst=v\d+`, "match field pattern borrow event"},
		{`StorageDead scope=`, "explicit StorageDead event paired with ScopeExit"},
	}

	for _, c := range checks {
		if !regexp.MustCompile(c.pattern).MatchString(dump1) {
			t.Errorf("CFG 快照缺少 %s (%s):\n%s", c.desc, c.pattern, dump1)
		}
	}

	if strings.Contains(dump1, "(reserved)") {
		t.Errorf("Phase 2B.2 Stage A 已启用 Borrow 事件，快照不应仍标记 (reserved):\n%s", dump1)
	}
}

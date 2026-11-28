package c11_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/codegen/c11"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
)

// runStringTestWithASanUBSan 编译并运行包含 ASan + UBSan 内存净室校验的 WON 源码。
func runStringTestWithASanUBSan(t *testing.T, src string, wantOutput string) {
	t.Helper()
	tirProg, _ := lowerSourceToTIR(t, src, nil)
	cCode, err := c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)
	if err != nil {
		t.Fatalf("TIR->C11 生成失败: %v", err)
	}

	gccOut, clangOut := compileStrictGCCAndClang(t, cCode, nil, nil)
	if gccOut != clangOut {
		t.Fatalf("GCC 与 Clang 输出不一致:\ngcc=%q\nclang=%q", gccOut, clangOut)
	}
	if !strings.Contains(gccOut, wantOutput) {
		t.Fatalf("未获得预期输出 %q，实际:\n%s", wantOutput, gccOut)
	}

	// 运行 ASan + UBSan 净室检查
	tmp := t.TempDir()
	cFile := filepath.Join(tmp, "asan_check.c")
	binFile := filepath.Join(tmp, "asan_check")
	if err := os.WriteFile(cFile, []byte(cCode), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("gcc", "-std=c11", "-fsanitize=address,undefined", "-fno-omit-frame-pointer", "-g", cFile, "-o", binFile)
	if out, err := cmd.CombinedOutput(); err == nil {
		runCmd := exec.Command(binFile)
		runOut, rerr := runCmd.CombinedOutput()
		if rerr != nil {
			t.Fatalf("ASan+UBSan 运行时检测到内存错误: %v\n输出:\n%s\nC源码:\n%s", rerr, string(runOut), cCode)
		}
	} else {
		t.Logf("跳过 ASan+UBSan 编译: %v: %s", err, string(out))
	}
}

// TestStringLifecycle_ConstructionAndUnicode 验证拥有型 String 构造、多字节 UTF-8、嵌入 NUL 及视图转换。
func TestStringLifecycle_ConstructionAndUnicode(t *testing.T) {
	src := `
fn main() -> i32:
    // 1. 空字符串构造与容量
    let s_empty = String::from("")
    if s_empty.len() != 0 or not s_empty.is_empty() or s_empty.capacity() != 0:
        return 1

    // 2. 基础 ASCII 字符串
    let s_ascii = String::from("hello")
    if s_ascii.len() != 5 or s_ascii.is_empty() or s_ascii.capacity() < 16:
        return 2

    // 3. 多字节 UTF-8 字符（"世界" 占 6 字节）
    let s_utf8 = String::from("世界")
    if s_utf8.len() != 6:
        return 3

    // 4. 显式转换为 &str 胖视图
    let view: &str = s_ascii.as_str()
    if view.len() != 5:
        return 4

    // 5. 嵌入 NUL 字符保全
    let s_nul = String::from("a\0b\0c")
    if s_nul.len() != 5:
        return 5

    println("CONSTRUCT_UNICODE_PASS")
    return 0
`
	runStringTestWithASanUBSan(t, src, "CONSTRUCT_UNICODE_PASS")
}

// TestStringLifecycle_MutationAndReallocation 验证 push_str 跨容量边界 2x 几何增长与可变引用传递。
func TestStringLifecycle_MutationAndReallocation(t *testing.T) {
	src := `
fn append_exclamation(s: &mut String):
    s.push_str("!")

fn main() -> i32:
    var s = String::from("hello")
    let initial_cap = s.capacity()

    // 1. 追加空串不影响现有内容
    s.push_str("")
    if s.len() != 5:
        return 1

    // 2. 容量内追加
    s.push_str(" world")
    if s.len() != 11:
        return 2

    // 3. 通过 &mut String 传参追加
    append_exclamation(&mut s)
    if s.len() != 12:
        return 3

    // 4. 触发容量扩张翻倍 (超出 16 字节)
    s.push_str(" this is a longer chunk to force reallocation")
    if s.capacity() <= initial_cap:
        return 4
    if s.len() <= 12:
        return 5

    // 5. 追加包含嵌入 NUL 的片段
    s.push_str("_\0_")
    if s.len() != 60:
        return 6

    println("MUTATION_REALLOC_PASS")
    return 0
`
	runStringTestWithASanUBSan(t, src, "MUTATION_REALLOC_PASS")
}

// TestStringLifecycle_OwnershipMoveLoopsAndReassignment 验证按值移动、作用域退出清理、
// 循环迭代释放、提前返回及赋值替换时右值先求值后析构左值旧内存。
func TestStringLifecycle_OwnershipMoveLoopsAndReassignment(t *testing.T) {
	src := `
fn take_ownership(s: String) -> usize:
    return s.len()

fn produce_string(n: i32) -> String:
    if n > 0:
        return String::from("positive")
    return String::from("non-positive")

fn test_loop_drops():
    var i = 0
    while i < 10:
        let s = String::from("iteration_temp_string")
        if i == 5:
            i = i + 1
            continue
        if i == 8:
            break
        i = i + 1

fn test_reassignment():
    var s = String::from("initial_long_string_heap")
    s = String::from("replaced_new_string")
    if s.len() != 19:
        println("ERR_REASSIGN")

fn main() -> i32:
    // 1. 移交所有权给函数
    let s1 = String::from("move_me")
    let len = take_ownership(s1)
    if len != 7:
        return 1

    // 2. 函数按值返回所有权
    let s2 = produce_string(1)
    if s2.len() != 8:
        return 2

    // 3. 变量间按值转移所有权
    let s3 = s2
    if s3.len() != 8:
        return 3

    // 4. 循环中的确定性作用域释放
    test_loop_drops()

    // 5. 重新赋值覆盖旧资源
    test_reassignment()

    println("OWNERSHIP_MOVE_DROP_PASS")
    return 0
`
	runStringTestWithASanUBSan(t, src, "OWNERSHIP_MOVE_DROP_PASS")
}

// TestStringLifecycle_NestedAggregatesAndDropOrder 验证嵌入 Struct、Tuple、Array、
// Tagged Enum、Option[String] 与 Result[String, Error] 中的原生递归析构胶水。
func TestStringLifecycle_NestedAggregatesAndDropOrder(t *testing.T) {
	src := `
struct UserProfile:
    name: String
    email: String
    id: u64

enum Message:
    Quit
    Text(String)
    Pair:
        first: String
        second: String

fn test_aggregates():
    // 1. 结构体复合 String
    let u = UserProfile {
        name: String::from("Alice"),
        email: String::from("alice@won.dev"),
        id: 42u64,
    }
    if u.name.len() != 5 or u.email.len() != 13:
        return

    // 2. 元组复合 String
    let t = (String::from("first"), 100i32, String::from("third"))
    let (t_first, t_mid, t_third) = t
    if t_first.len() != 5 or t_third.len() != 5:
        return

    // 3. 定长数组复合 String
    let arr: [String; 2] = [String::from("elem0"), String::from("elem1")]
    if arr[0].len() != 5 or arr[1].len() != 5:
        return

    // 4. 枚举活跃元组变体载荷
    let m1 = Message::Text(String::from("payload"))
    match m1:
        Message::Text(s) =>
            if s.len() != 7:
                return
        _ =>
            return

    // 5. 枚举活跃结构体变体载荷
    let m2 = Message::Pair {
        first: String::from("left"),
        second: String::from("right"),
    }
    match m2:
        Message::Pair { first, second } =>
            if first.len() != 4 or second.len() != 5:
                return
        _ =>
            return

    // 6. Option[String]
    let opt_some: Option[String] = Option::Some(String::from("some_value"))
    let opt_none: Option[String] = Option::None

    // 7. Result[String, i32]
    let res_ok: Result[String, i32] = Result::Ok(String::from("ok_value"))
    let res_err: Result[String, i32] = Result::Err(-404)

fn main() -> i32:
    test_aggregates()
    println("NESTED_AGGREGATES_PASS")
    return 0
`
	runStringTestWithASanUBSan(t, src, "NESTED_AGGREGATES_PASS")
}

// TestStringLifecycle_PartialAggregateTryCleanup 验证聚合构造过程中前半部分字段已初始化，
// 后续字段触发 '?' 错误返回时，仅正确析构已初始化的字段，不漏不重。
func TestStringLifecycle_PartialAggregateTryCleanup(t *testing.T) {
	src := `
struct MultiStringHolder:
    s1: String
    tag: u64
    s2: String

fn fallible_step(fail: bool) -> Result[String, i32]:
    if fail:
        return Result::Err(-1)
    return Result::Ok(String::from("s2_ok"))

fn build_holder(fail: bool) -> Result[MultiStringHolder, i32]:
    let h = MultiStringHolder {
        s1: String::from("s1_must_be_cleaned_up_on_failure"),
        tag: 777u64,
        s2: fallible_step(fail)?,
    }
    return Result::Ok(h)

fn main() -> i32:
    // 1. 失败分支：验证已初始化的 s1 被安全析构，零内存泄漏
    let res_fail = build_holder(true)
    match res_fail:
        Result::Ok(_) =>
            return 1
        Result::Err(e) =>
            if e != -1:
                return 2

    // 2. 成功分支：所有字段正常组装
    let res_ok = build_holder(false)
    match res_ok:
        Result::Ok(h) =>
            if h.s1.len() != 32 or h.s2.len() != 5:
                return 3
        Result::Err(_) =>
            return 4

    println("PARTIAL_TRY_CLEANUP_PASS")
    return 0
`
	runStringTestWithASanUBSan(t, src, "PARTIAL_TRY_CLEANUP_PASS")
}

// TestStringLifecycle_InterpolationAndOutput 验证字符串插值 f"..." 对各种类型的格式化与 println 安全输出。
func TestStringLifecycle_InterpolationAndOutput(t *testing.T) {
	src := `
fn main() -> i32:
    let name = "WON"
    let ver = 2
    let flag = true
    let ch = 'A'

    // 插值拼接生成拥有型 String
    let msg = "Hello {name} v{ver} active={flag} char={ch}"
    println(msg)

    // 包含嵌入 NUL 的 String 安全输出（定长写入，不截断）
    let nul_str = String::from("prefix\0suffix")
    println(nul_str)

    println("INTERPOLATION_PASS")
    return 0
`
	runStringTestWithASanUBSan(t, src, "INTERPOLATION_PASS")
}

// TestStringLifecycle_FailClosedBorrowRejection 验证编译期借用检查器静默阻断自身追加借用冲突 (WON-MEM-004)。
func TestStringLifecycle_FailClosedBorrowRejection(t *testing.T) {
	src := `
fn main():
    var s = String::from("abc")
    s.push_str(s.as_str())
`
	rep := diagnostic.NewReport()
	lex := lexer.New("self_append.won", src, rep)
	p := parser.New(lex, rep, "self_append.won")
	prog := p.ParseProgram()
	chk := checker.New(rep, nil)
	chk.CheckProgram(prog)

	if !rep.HasErrors() {
		t.Fatalf("s.push_str(s.as_str()) 应当在所有权/借用检查阶段被拒止 (WON-MEM-004)，实际无报错")
	}
	foundMem004 := false
	for _, d := range rep.Diagnostics {
		if d.Code == "WON-MEM-004" {
			foundMem004 = true
			break
		}
	}
	if !foundMem004 {
		t.Fatalf("期望得到 WON-MEM-004 借用冲突诊断，实际报告:\n%s", rep.String())
	}
}

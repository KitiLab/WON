package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
)

// TestPhase2B1CLIOwnershipAndC11Integration 验证 Phase 2B.1 所有权分析、JSON 诊断、不泄露残余 .won.c 以及 GCC/Clang 严苛 C11 构建与执行。
func TestPhase2B1CLIOwnershipAndC11Integration(t *testing.T) {
	woncBin := filepath.Join("..", "bin", "wonc")
	absBin, err := filepath.Abs(woncBin)
	if err != nil {
		t.Fatalf("定位 wonc 二进制失败: %v", err)
	}

	tmpDir := t.TempDir()

	// 1. 合法标量未初始化声明 (uninit let across all branches) 经由 GCC 与 Clang 严苛 ISO C11 编译执行均通过
	validScalar := filepath.Join(tmpDir, "uninit_scalar_valid.won")
	scalarSrc := `fn select(cond: bool) -> i32:
    let result: i32
    if cond:
        result = 17
    else:
        result = 29
    return result

fn main() -> i32:
    let a = select(true)
    let b = select(false)
    if a + b == 46:
        return 0
    return 1
`
	if err := os.WriteFile(validScalar, []byte(scalarSrc), 0644); err != nil {
		t.Fatalf("写入临时源码失败: %v", err)
	}

	// check 通过
	if out, err := exec.Command(absBin, "check", validScalar).CombinedOutput(); err != nil {
		t.Fatalf("wonc check 合法标量未初始化测试失败: %v\n%s", err, string(out))
	}

	// build -emit-c 并使用默认构建产物运行
	scalarOutBin := filepath.Join(tmpDir, "uninit_scalar_bin")
	cmdBuild := exec.Command(absBin, "build", "--emit-c", "-o", scalarOutBin, validScalar)
	cmdBuild.Dir = tmpDir
	if out, err := cmdBuild.CombinedOutput(); err != nil {
		t.Fatalf("wonc build 合法标量未初始化测试失败: %v\n%s", err, string(out))
	}
	if runOut, err := exec.Command(scalarOutBin).CombinedOutput(); err != nil {
		t.Fatalf("执行标量未初始化产物失败: %v\n%s", err, string(runOut))
	}

	// 若主机安装 gcc / clang，分别以 -std=c11 -pedantic-errors -Wall -Wextra -Werror 直接验证生成的 C 文件
	generatedC := filepath.Join(tmpDir, "uninit_scalar_valid.won.c")
	for _, cc := range []string{"gcc", "clang"} {
		if _, lookErr := exec.LookPath(cc); lookErr != nil {
			continue
		}
		ccBin := filepath.Join(tmpDir, "cc_"+cc+"_bin")
		ccCmd := exec.Command(cc, "-std=c11", "-pedantic-errors", "-Wall", "-Wextra", "-Werror", generatedC, "-o", ccBin)
		if ccOut, err := ccCmd.CombinedOutput(); err != nil {
			t.Fatalf("%s 严格 C11 编译 %s 失败: %v\n%s", cc, generatedC, err, string(ccOut))
		}
		if runOut, err := exec.Command(ccBin).CombinedOutput(); err != nil {
			t.Fatalf("%s 编译产物执行失败: %v\n%s", cc, err, string(runOut))
		}
	}

	// 2. 所有权合法但暂不支持 C11 后端的非标量程序：check 成功，build 仅触发 WON-GEN-002
	validNonScalar := filepath.Join(tmpDir, "own_ok_non_scalar.won")
	nonScalarSrc := `@derive(Copy)
struct Point:
    x: i32
    y: i32

fn main() -> u64:
    let p = Point { x: 1, y: 2 }
    let p2 = p
    var s = String::from("ok")
    println(s)
    s = String::from("reinit")
    return s.len()
`
	if err := os.WriteFile(validNonScalar, []byte(nonScalarSrc), 0644); err != nil {
		t.Fatalf("写入非标量临时文件失败: %v", err)
	}
	if out, err := exec.Command(absBin, "check", validNonScalar).CombinedOutput(); err != nil {
		t.Fatalf("wonc check 所有权合法非标量程序应通过: %v\n%s", err, string(out))
	}
	cmdNS := exec.Command(absBin, "build", validNonScalar)
	cmdNS.Dir = tmpDir
	nsOut, err := cmdNS.CombinedOutput()
	if err == nil || !strings.Contains(string(nsOut), "WON-GEN-002") {
		t.Fatalf("wonc build 非标量程序期望仅报 WON-GEN-002，实际 err=%v out=%s", err, string(nsOut))
	}

	// 3. 所有权非法程序（标量未初始化 / Move 后使用 / 不可达解引用移出 / 非法 derive(Copy)）：check 与 build 均拒绝且不留 .won.c
	negCases := []struct {
		name string
		src  string
		code string
	}{
		{
			name: "scalar_uninit",
			src:  "fn main() -> i32:\n    let x: i32\n    if false:\n        x = 1\n    return x\n",
			code: "WON-MEM-010",
		},
		{
			name: "use_after_move",
			src:  "fn eat(s: String):\n    println(s)\n\nfn main():\n    let s = String::from(\"hi\")\n    eat(s)\n    eat(s)\n",
			code: "WON-MEM-001",
		},
		{
			name: "move_out_deref",
			src:  "fn f(r: &String) -> String:\n    return *r\n",
			code: "WON-MEM-008",
		},
		{
			name: "invalid_derive_copy",
			src:  "@derive(Copy)\nstruct Bad:\n    s: String\n",
			code: "WON-MEM-011",
		},
	}

	for _, nc := range negCases {
		t.Run(nc.name, func(t *testing.T) {
			wonPath := filepath.Join(tmpDir, nc.name+".won")
			if err := os.WriteFile(wonPath, []byte(nc.src), 0644); err != nil {
				t.Fatalf("写入失败: %v", err)
			}

			chkCmd := exec.Command(absBin, "check", "--format=json", wonPath)
			chkOut, err := chkCmd.CombinedOutput()
			if err == nil {
				t.Fatalf("wonc check 在负例 %s 上必须返回非零错误码", nc.name)
			}
			var rep diagnostic.Report
			if jsonErr := json.Unmarshal(chkOut, &rep); jsonErr != nil {
				t.Fatalf("JSON 解析失败: %v\n原始输出:%s", jsonErr, string(chkOut))
			}
			found := false
			for _, d := range rep.Diagnostics {
				if d.Code == nc.code && d.Span.Start.Line > 0 {
					found = true
				}
			}
			if !found {
				t.Errorf("%s 期望诊断码 %s，实际 JSON:\n%s", nc.name, nc.code, string(chkOut))
			}

			// 确保 wonc build 拒止且不产生残留 .won.c
			bldCmd := exec.Command(absBin, "build", wonPath)
			bldCmd.Dir = tmpDir
			bldOut, bErr := bldCmd.CombinedOutput()
			if bErr == nil {
				t.Fatalf("wonc build 在负例 %s 上必须失败", nc.name)
			}
			_ = bldOut
			cArtifact := filepath.Join(tmpDir, nc.name+".won.c")
			if _, stErr := os.Stat(cArtifact); stErr == nil {
				t.Errorf("所有权失败的 %s 不得在磁盘残留中间文件 %s", nc.name, cArtifact)
			}
		})
	}
}

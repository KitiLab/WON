package drop_test

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
)

// FuzzDropElaboration 对全流程前端+借用+Drop 展开管道执行性质模糊测试：
// 1. 对任意随机/畸形输入绝不发生 panic；
// 2. 只要诊断列表出现任意 error，OwnershipChecked 与 DropElaborated 必须严格保持 false；
// 3. 当认证通过时，多次执行生成的 DropPlan.Dump() 必须完全确定性一致。
func FuzzDropElaboration(f *testing.F) {
	seeds := []string{
		`fn ok_basic():
    let s = String::from("hello")
`,
		`fn cond_move(flag: bool):
    var s = String::from("a")
    if flag:
        let t = s
    s = String::from("b")
`,
		`struct P:
    a: String
    b: String
fn consume(x: String):
    let _u = 0
fn split(c: bool):
    let p = P { a: String::from("x"), b: String::from("y") }
    if c:
        consume(p.a)
`,
		`fn bad_use():
    let s = String::from("x")
    let y = s
    let z = s
`,
		`fn bad_ret() -> &str:
    let s = String::from("temp")
    return s.as_str()
`,
		`fn loop_drops(n: i64):
    let a = String::from("outer")
    var i: i64 = 0
    while i < n:
        let b = String::from("inner")
        i = i + 1
        if i == 1:
            break
`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 4096 {
			return
		}
		runOnce := func() (tp *checker.TypedProgram, hasErr bool) {
			rep := diagnostic.NewReport()
			lx := lexer.New("fuzz_drop.won", src, rep)
			p := parser.New(lx, rep, "fuzz_drop.won")
			prog := p.ParseProgram()
			if !rep.HasErrors() {
				chk := checker.New(rep, nil)
				tp = chk.CheckProgram(prog)
			}
			return tp, rep.HasErrors()
		}

		tp1, hasErr1 := runOnce()
		if hasErr1 && tp1 != nil && (tp1.OwnershipChecked || tp1.DropElaborated) {
			t.Fatalf("存在编译错误时 OwnershipChecked/DropElaborated 绝不能为 true")
		}
		if !hasErr1 && tp1 != nil {
			if !tp1.OwnershipChecked || !tp1.DropElaborated || tp1.DropPlan == nil {
				t.Fatalf("无错误编译结果必须产出有效的 DropElaborated 计划")
			}
			tp2, hasErr2 := runOnce()
			if hasErr2 || tp2 == nil || tp2.DropPlan == nil || tp1.DropPlan.Dump() != tp2.DropPlan.Dump() {
				t.Fatalf("Drop 展开未保持确定性")
			}
		}
	})
}

// FuzzFFIContractJSON 对旁路 FFI 契约 JSON 解析与 Validate() 执行模糊测试。
func FuzzFFIContractJSON(f *testing.F) {
	f.Add(`{"namespace":"mem","functions":{"take_buf":{"contract_id":"mem.take","params":[{"param_index":0,"ownership":"owned_transfer"}],"return":{"ownership":"raw_unmanaged"}}}}`)
	f.Add(`{"namespace":"mem","functions":{"take_buf":{"contract_id":"mem.take","params":[{"param_index":0,"ownership":"bogus"}],"return":{"ownership":"raw_unmanaged"}}}}`)
	f.Add(`{"namespace":"demo","opaque_types":{"H":{"ownership":"opaque_handle","type_name":"H","release_fn":"demo::free","borrows_param":-1}}}`)

	f.Fuzz(func(t *testing.T, rawJSON string) {
		if len(rawJSON) > 4096 {
			return
		}
		if cat, err := bindgen.ParseFFIContractJSON([]byte(rawJSON)); err == nil {
			if err2 := cat.Validate(); err2 != nil {
				t.Fatalf("ParseFFIContractJSON 成功返回但 Validate 失败: %v", err2)
			}
		}
	})
}

// FuzzFFIOwnership 针对带有显式 FFI 契约的 WON 所有权分析管道执行模糊测试：
// 任意畸形或合法输入均不得 panic，出错时 OwnershipChecked/DropElaborated 必须严格保持 false。
func FuzzFFIOwnership(f *testing.F) {
	f.Add(`fn ok(p: *mut u8):
    unsafe:
        mem::take_buf(p)
`)
	f.Add(`fn bad(cond: bool, p: *mut u8):
    if cond:
        unsafe:
            mem::take_buf(p)
    unsafe:
        mem::take_buf(p)
`)
	f.Add(`fn copy_alias(p: *mut u8):
    let q = p
    unsafe:
        mem::peek_buf(p)
        mem::peek_buf(q)
`)

	bindings := map[string]*bindgen.BindingMeta{
		"mem": {
			HeaderPath: "mem.h",
			Functions: map[string]bindgen.FunctionSig{
				"take_buf": {Name: "take_buf", ReturnType: "void", ParamTypes: []string{"unsigned char *"}},
				"peek_buf": {Name: "peek_buf", ReturnType: "void", ParamTypes: []string{"const unsigned char *"}},
			},
			Contracts: &bindgen.FFIContractCatalog{
				Namespace: "mem",
				Functions: map[string]bindgen.FFIFuncContract{
					"take_buf": {
						ContractID: "mem.take_buf",
						Params:     []bindgen.FFIParamContract{{ParamIndex: 0, Ownership: bindgen.FFIOwnedTransfer}},
						Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
					},
					"peek_buf": {
						ContractID: "mem.peek_buf",
						Params:     []bindgen.FFIParamContract{{ParamIndex: 0, Ownership: bindgen.FFIBorrowedShared}},
						Return:     bindgen.FFIReturnContract{Ownership: bindgen.FFINonOwningRaw, BorrowsParam: -1},
					},
				},
			},
		},
	}

	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 4096 {
			return
		}
		runOnce := func() (*checker.TypedProgram, bool) {
			rep := diagnostic.NewReport()
			lx := lexer.New("fuzz_ffi.won", src, rep)
			p := parser.New(lx, rep, "fuzz_ffi.won")
			prog := p.ParseProgram()
			var tp *checker.TypedProgram
			if !rep.HasErrors() {
				chk := checker.New(rep, bindings)
				tp = chk.CheckProgram(prog)
			}
			return tp, rep.HasErrors()
		}
		tp1, hasErr1 := runOnce()
		if hasErr1 && tp1 != nil && (tp1.OwnershipChecked || tp1.DropElaborated) {
			t.Fatalf("存在 FFI 编译错误时 OwnershipChecked/DropElaborated 绝不能为 true")
		}
		if !hasErr1 && tp1 != nil {
			if !tp1.OwnershipChecked || !tp1.DropElaborated || tp1.DropPlan == nil {
				t.Fatalf("无错误 FFI 编译结果必须产出有效的 DropPlan")
			}
			tp2, hasErr2 := runOnce()
			if hasErr2 || tp2 == nil || tp2.DropPlan == nil || tp1.DropPlan.Dump() != tp2.DropPlan.Dump() {
				t.Fatalf("FFI Drop 展开未保持确定性")
			}
		}
	})
}

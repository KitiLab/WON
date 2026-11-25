package symbols

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

func TestPreludeRegistration(t *testing.T) {
	tctx := types.NewContext()
	tab := NewTable(tctx)

	for _, name := range []string{"Option", "Result", "Some", "None", "Ok", "Err", "String", "println", "bytes_equal"} {
		if sym := tab.GlobalScope.Lookup(name); sym == nil {
			t.Fatalf("全局前导作用域缺少内置符号 '%s'", name)
		}
	}

	strSym := tab.GlobalScope.Lookup("String")
	if _, ok := strSym.Methods["from"]; !ok {
		t.Fatalf("内置 String 结构体未注册固有静态方法 String::from")
	}
}

func TestHierarchicalScopeDuplicateVsShadowing(t *testing.T) {
	tctx := types.NewContext()
	tab := NewTable(tctx)

	fnScope := NewScope(ScopeFunction, tab.GlobalScope)
	s1 := tab.AllocSymbol(SymVar, "x", diagnostic.Span{})
	s1.Type = tctx.I32
	if existing, ok := fnScope.Insert(s1); !ok || existing != nil {
		t.Fatalf("首次插入局部符号 x 应成功")
	}

	// 同级词法作用域内重名声明必须被拒绝
	sDup := tab.AllocSymbol(SymVar, "x", diagnostic.Span{})
	sDup.Type = tctx.Bool
	if existing, ok := fnScope.Insert(sDup); ok || existing != s1 {
		t.Fatalf("同作用域重复声明 x 必须被拒绝并返回原始符号")
	}

	// 内层块作用域允许遮蔽外层符号
	blockScope := NewScope(ScopeBlock, fnScope)
	sShadow := tab.AllocSymbol(SymVar, "x", diagnostic.Span{})
	sShadow.Type = tctx.StrView
	if _, ok := blockScope.Insert(sShadow); !ok {
		t.Fatalf("内层块作用域遮蔽外层变量 x 必须被允许")
	}
	if got := blockScope.Lookup("x"); got != sShadow {
		t.Errorf("内层 Lookup('x') 应命中遮蔽符号")
	}
	if got := fnScope.Lookup("x"); got != s1 {
		t.Errorf("退出内层后外层 Lookup('x') 应保持原绑定不变")
	}
}

func TestResolveTypeNode(t *testing.T) {
	tctx := types.NewContext()
	tab := NewTable(tctx)
	report := diagnostic.NewReport()

	// 1. 解析 &str, [u8; 4], Result[i32, bool]
	strViewID := tab.ResolveTypeNode(&ast.RefType{
		IsMut: false,
		Elem:  &ast.NamedType{Path: []string{"str"}},
	}, tab.GlobalScope, report)
	if strViewID != tctx.StrView {
		t.Errorf("&str 应解析为 StrView TypeID, 实际为 %s", tctx.Format(strViewID))
	}

	arrID := tab.ResolveTypeNode(&ast.ArrayType{
		Elem: &ast.NamedType{Path: []string{"u8"}},
		Size: "4",
	}, tab.GlobalScope, report)
	if arrID != tctx.Array(tctx.U8, 4) {
		t.Errorf("[u8; 4] 解析异常: 实际为 %s", tctx.Format(arrID))
	}

	resID := tab.ResolveTypeNode(&ast.NamedType{
		Path: []string{"Result"},
		GenericArgs: []ast.TypeNode{
			&ast.NamedType{Path: []string{"i32"}},
			&ast.NamedType{Path: []string{"bool"}},
		},
	}, tab.GlobalScope, report)
	if resID != tctx.Enum(types.BuiltinResultSymbolID, "Result", []types.TypeID{tctx.I32, tctx.Bool}) {
		t.Errorf("Result[i32, bool] 解析异常: 实际为 %s", tctx.Format(resID))
	}
	if report.HasErrors() {
		t.Fatalf("合法类型节点解析不应产生诊断错误: %s", report.String())
	}

	// 2. 非法数组大小应触发 WON-TYP-004，未知类型应触发 WON-NAM-003
	tab.ResolveTypeNode(&ast.ArrayType{
		Elem: &ast.NamedType{Path: []string{"u8"}},
		Size: "bad_size",
	}, tab.GlobalScope, report)
	tab.ResolveTypeNode(&ast.NamedType{Path: []string{"MissingCustomType"}}, tab.GlobalScope, report)

	codes := make(map[string]bool)
	for _, d := range report.Diagnostics {
		codes[d.Code] = true
	}
	if !codes["WON-TYP-004"] || !codes["WON-NAM-003"] {
		t.Errorf("期望捕获 WON-TYP-004 与 WON-NAM-003, 实际诊断: %s", report.String())
	}
}

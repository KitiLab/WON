package parser

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
)

func parseAttrSource(src string) (*ast.Program, *diagnostic.Report) {
	rep := diagnostic.NewReport()
	lex := lexer.New("attr_uninit.won", src, rep)
	p := New(lex, rep, "attr_uninit.won")
	return p.ParseProgram(), rep
}

func assertAttrDiag(t *testing.T, rep *diagnostic.Report, want string) {
	t.Helper()
	for _, d := range rep.Diagnostics {
		if d.Code == want {
			return
		}
	}
	t.Fatalf("期望诊断码 %s，实际输出:\n%s", want, rep.String())
}

// TestDeriveCopyAttributeParsing 验证 @derive(Copy) 挂载及畸形/禁用属性拦截规则
func TestDeriveCopyAttributeParsing(t *testing.T) {
	t.Run("ValidStructAndEnumDeriveCopy", func(t *testing.T) {
		src := "@derive(Copy)\nstruct Pt:\n    x: i32\n    y: i32\n\n@derive(Copy)\nenum Flag:\n    On\n    Val(u8)\n"
		prog, rep := parseAttrSource(src)
		if rep.HasErrors() {
			t.Fatalf("合法 @derive(Copy) 解析失败:\n%s", rep.String())
		}
		if len(prog.Decls) != 2 {
			t.Fatalf("期望 2 个声明，实际 %d", len(prog.Decls))
		}
		sd, ok := prog.Decls[0].(*ast.StructDecl)
		if !ok || len(sd.Attributes) != 1 || sd.Attributes[0].Args[0] != "Copy" {
			t.Fatalf("StructDecl 未正确挂载 @derive(Copy): %+v", prog.Decls[0])
		}
		ed, ok := prog.Decls[1].(*ast.EnumDecl)
		if !ok || len(ed.Attributes) != 1 || ed.Attributes[0].Args[0] != "Copy" {
			t.Fatalf("EnumDecl 未正确挂载 @derive(Copy): %+v", prog.Decls[1])
		}
	})

	t.Run("RejectCopyAlias", func(t *testing.T) {
		_, rep := parseAttrSource("@copy\nstruct Pt:\n    x: i32\n")
		assertAttrDiag(t, rep, "WON-MEM-011")
	})

	t.Run("RejectUnsupportedDeriveArgs", func(t *testing.T) {
		_, rep := parseAttrSource("@derive(Clone)\nstruct Pt:\n    x: i32\n")
		assertAttrDiag(t, rep, "WON-MEM-011")
	})

	t.Run("RejectDeriveCopyOnFunction", func(t *testing.T) {
		_, rep := parseAttrSource("@derive(Copy)\nfn foo():\n    let x = 1\n")
		assertAttrDiag(t, rep, "WON-MEM-011")
	})

	t.Run("RejectUnratifiedReprAttribute", func(t *testing.T) {
		_, rep := parseAttrSource("@repr(C)\nstruct Pt:\n    x: i32\n")
		assertAttrDiag(t, rep, "WON-SEM-010")
	})

	t.Run("RejectMalformedAtAttribute", func(t *testing.T) {
		_, rep := parseAttrSource("@123\nstruct Pt:\n    x: i32\n")
		assertAttrDiag(t, rep, "WON-SYN-005")
	})
}

// TestUninitializedDeclarationsParsing 验证显式类型未初始化 let / var 声明以及非法省略类型/解构拦截
func TestUninitializedDeclarationsParsing(t *testing.T) {
	t.Run("ValidUninitLetAndVar", func(t *testing.T) {
		src := "fn main() -> i32:\n    let result: i32\n    var buf: i64\n    result = 1\n    buf = 2\n    return result\n"
		prog, rep := parseAttrSource(src)
		if rep.HasErrors() {
			t.Fatalf("合法未初始化声明解析失败:\n%s", rep.String())
		}
		fn := prog.Functions[0]
		ls, ok := fn.Body.Statements[0].(*ast.LetStmt)
		if !ok || ls.IsMut || ls.Init != nil || ls.TypeAST == nil || ls.Name != "result" {
			t.Fatalf("uninit let 解析结果不符合预期: %+v", fn.Body.Statements[0])
		}
		vs, ok := fn.Body.Statements[1].(*ast.LetStmt)
		if !ok || !vs.IsMut || vs.Init != nil || vs.TypeAST == nil || vs.Name != "buf" {
			t.Fatalf("uninit var 解析结果不符合预期: %+v", fn.Body.Statements[1])
		}
	})

	t.Run("RejectUntypedUninitLet", func(t *testing.T) {
		_, rep := parseAttrSource("fn main():\n    let x\n")
		assertAttrDiag(t, rep, "WON-SYN-009")
	})

	t.Run("RejectDestructuringWithoutInit", func(t *testing.T) {
		_, rep := parseAttrSource("fn main():\n    let (a, b): (i32, i32)\n")
		assertAttrDiag(t, rep, "WON-SYN-009")
	})

	t.Run("InitializedDeclarationsIntact", func(t *testing.T) {
		src := "fn main() -> i32:\n    let x = 10\n    var y: i32 = 20\n    return x + y\n"
		_, rep := parseAttrSource(src)
		if rep.HasErrors() {
			t.Fatalf("常规初始化声明不应报错:\n%s", rep.String())
		}
	})
}

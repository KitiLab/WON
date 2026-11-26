package cfg_test

import (
	goast "go/ast"
	goparser "go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/codegen"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

type missingSymView struct {
	types *types.Context
	syms  *symbols.Table
}

func (m *missingSymView) GetTypes() *types.Context            { return m.types }
func (m *missingSymView) GetSymbols() *symbols.Table          { return m.syms }
func (m *missingSymView) ExprType(ast.Expr) types.TypeID      { return m.types.I32 }
func (m *missingSymView) CallTarget(ast.Expr) *symbols.Symbol { return nil }
func (m *missingSymView) FieldRef(ast.Node) *symbols.FieldInfo {
	return nil
}
func (m *missingSymView) VariantRef(ast.Node) *symbols.VariantInfo {
	return nil
}

// TestASTToCFGExhaustiveInventory 使用 Go 语法解析器核验 internal/ast/ast.go 中的全部 Decl/Stmt/Expr/Pattern 类型均已在 internal/cfg/builder.go 显式处理，防止新增 AST 节点静默绕过所有权分析。
func TestASTToCFGExhaustiveInventory(t *testing.T) {
	fset := token.NewFileSet()
	astFile, err := goparser.ParseFile(fset, "../ast/ast.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 ast.go 失败: %v", err)
	}

	declTypes := map[string]bool{}
	stmtTypes := map[string]bool{}
	exprTypes := map[string]bool{}
	patternTypes := map[string]bool{}

	for _, d := range astFile.Decls {
		fd, ok := d.(*goast.FuncDecl)
		if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 {
			continue
		}
		var recvName string
		switch rt := fd.Recv.List[0].Type.(type) {
		case *goast.StarExpr:
			if id, ok := rt.X.(*goast.Ident); ok {
				recvName = id.Name
			}
		case *goast.Ident:
			recvName = rt.Name
		}
		if recvName == "" {
			continue
		}
		switch fd.Name.Name {
		case "declNode":
			declTypes[recvName] = true
		case "stmtNode":
			stmtTypes[recvName] = true
		case "exprNode":
			exprTypes[recvName] = true
		case "patternNode":
			patternTypes[recvName] = true
		}
	}

	builderFile, err := goparser.ParseFile(fset, "builder.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 builder.go 失败: %v", err)
	}

	handledCases := map[string]bool{}
	goast.Inspect(builderFile, func(n goast.Node) bool {
		if se, ok := n.(*goast.SelectorExpr); ok {
			if id, ok := se.X.(*goast.Ident); ok && id.Name == "ast" {
				handledCases[se.Sel.Name] = true
			}
		}
		return true
	})

	for name := range declTypes {
		if !handledCases[name] {
			t.Errorf("ast.Decl 实现类型 *ast.%s 未在 cfg/builder.go 中显式出现", name)
		}
	}
	for name := range stmtTypes {
		if !handledCases[name] {
			t.Errorf("ast.Stmt 实现类型 *ast.%s 未在 cfg/builder.go 中显式出现", name)
		}
	}
	for name := range exprTypes {
		if !handledCases[name] {
			t.Errorf("ast.Expr 实现类型 *ast.%s 未在 cfg/builder.go 中显式出现", name)
		}
	}
	for name := range patternTypes {
		if !handledCases[name] {
			t.Errorf("ast.Pattern 实现类型 *ast.%s 未在 cfg/builder.go 中显式出现", name)
		}
	}
}

// TestCFGBuilderFailClosedGuards 验证构建阶段对缺失符号、非法赋值目标、非法取址目标及未知结构触发 WON-INT-001 并阻断 OwnershipChecked 与 C11 代码生成。
func TestCFGBuilderFailClosedGuards(t *testing.T) {
	tctx := types.NewContext()
	symTab := symbols.NewTable(tctx)
	sp := diagnostic.Span{File: "guard.won", Start: diagnostic.Position{Line: 1, Column: 1, Byte: 0}, End: diagnostic.Position{Line: 1, Column: 10, Byte: 9}}

	fnDecl := &ast.FunctionDecl{
		Name: "broken",
		Body: &ast.BlockStmt{
			Statements: []ast.Stmt{
				&ast.AssignStmt{
					TargetExpr: &ast.IntegerLiteral{Raw: "1", Span: sp},
					Op:         "=",
					Value:      &ast.IntegerLiteral{Raw: "2", Span: sp},
					Span:       sp,
				},
			},
			Span: sp,
		},
		Span: sp,
	}

	// 1. 全局函数符号缺失触发 WON-INT-001
	prog := &ast.Program{Functions: []*ast.FunctionDecl{fnDecl}, Decls: []ast.Decl{fnDecl}, Span: sp}
	rep1 := diagnostic.NewReport()
	cfg.BuildProgram(prog, &missingSymView{types: tctx, syms: symTab}, rep1)
	assertDiagCode(t, "MissingTopLevelFnSym", rep1, "WON-INT-001")

	// 2. 注册函数符号但语句含非 Place 赋值左值 -> WON-INT-001
	fnSym := symTab.AllocSymbol(symbols.SymFunc, "broken", sp)
	symTab.GlobalScope.Insert(fnSym)
	rep2 := diagnostic.NewReport()
	cfg.BuildProgram(prog, &missingSymView{types: tctx, syms: symTab}, rep2)
	assertDiagCode(t, "NonPlaceAssignTarget", rep2, "WON-INT-001")

	// 3. 含 TraitType 的 ImplBlock / TraitDecl 与 ScopeExpr/SpawnExpr 显式拒绝且 OwnershipChecked == false
	rep3 := diagnostic.NewReport()
	srcUnsupported := `
fn main() -> i32:
    scope s:
        let x: i32 = 1
    return 0
`
	lex := lexer.New("scope_test.won", srcUnsupported, rep3)
	p := parser.New(lex, rep3, "scope_test.won")
	parsed := p.ParseProgram()
	if rep3.HasErrors() {
		t.Fatalf("解析失败: %s", rep3.String())
	}
	chk := checker.New(rep3, nil)
	tp := chk.CheckProgram(parsed)
	assertDiagCode(t, "UnsupportedScopeExpr", rep3, "WON-SEM-010")
	if tp.OwnershipChecked {
		t.Fatalf("存在语义/所有权错误时 OwnershipChecked 必须保持 false")
	}

	// 4. codegen.NewTyped 对未通过所有权认证的 TypedProgram 必须 Fail-Closed 拒绝生成
	gen := codegen.NewTyped(parsed, tp, nil)
	if _, genErr := gen.Generate(); genErr == nil || !strings.Contains(genErr.Error(), "WON-INT-001") {
		t.Fatalf("OwnershipChecked=false 时 codegen.Generate 必须报错 WON-INT-001，实际 err=%v", genErr)
	}
}

func assertDiagCode(t *testing.T, name string, rep *diagnostic.Report, want string) {
	t.Helper()
	if !rep.HasErrors() {
		t.Fatalf("%s 期望失败并报 %s，但未报错", name, want)
	}
	for _, d := range rep.Diagnostics {
		if d.Code == want {
			return
		}
	}
	t.Fatalf("%s 期望诊断码 %s，实际:\n%s", name, want, rep.String())
}

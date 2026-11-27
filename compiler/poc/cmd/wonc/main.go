// WON Phase 0B 编译器与命令行驱动入口 (wonc)
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/codegen/c11"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
	"github.com/KitiLab/WON/compiler/poc/internal/tir"
	"github.com/KitiLab/WON/compiler/poc/internal/tir/lower"
	"github.com/KitiLab/WON/compiler/poc/internal/toolchain"
)

const (
	Version   = "0.1.0-poc"
	Milestone = "Phase 0B (Proof of Concept)"
)

func main() {
	if len(os.Args) < 2 {
		printHelp()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "version":
		fmt.Printf("wonc version %s (%s)\nTarget ABI: Linux x86_64\nOwner: KitiLab\n", Version, Milestone)

	case "parse":
		runParse(os.Args[2:])

	case "check":
		runCheck(os.Args[2:])

	case "bindings":
		runBindings(os.Args[2:])

	case "build":
		runBuild(os.Args[2:])

	default:
		fmt.Fprintf(os.Stderr, "未知的命令: %q\n", command)
		printHelp()
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println("WON Phase 1B 编译器工具链 (wonc)")
	fmt.Println("用法:")
	fmt.Println("  wonc version                     查看编译器版本与目标架构")
	fmt.Println("  wonc parse [--format=json] <file> 仅执行规范词法分析与语法解析")
	fmt.Println("  wonc check [--format=json] <file> 语法与语义检查")
	fmt.Println("  wonc bindings [zlib]             从系统 C 头文件生成绑定")
	fmt.Println("  wonc build [--static] [--lto] [-o out] <file> 编译为原生机器码可执行文件")
}

func runParse(args []string) {
	fs := flag.NewFlagSet("parse", flag.ExitOnError)
	format := fs.String("format", "text", "输出格式: text | json")
	_ = fs.Parse(args)

	files := fs.Args()
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "错误: 请指定输入的 .won 源码文件")
		os.Exit(1)
	}

	srcFile := files[0]
	content, err := os.ReadFile(srcFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法读取文件 %s: %v\n", srcFile, err)
		os.Exit(1)
	}

	report := diagnostic.NewReport()
	lex := lexer.New(srcFile, string(content), report)
	p := parser.New(lex, report, srcFile)
	_ = p.ParseProgram()

	if *format == "json" {
		jsonStr, _ := report.ToJSON()
		fmt.Println(jsonStr)
	} else {
		fmt.Print(report.String())
	}

	if report.HasErrors() {
		os.Exit(1)
	}
}

// resolveBindings 遍历程序中的 #import 声明并加载对应的 C 库绑定元数据
func enforceOwnershipCheckedGate(report *diagnostic.Report, typedProg *checker.TypedProgram, srcFile string, span diagnostic.Span, actionDesc string) {
	if !report.HasErrors() && typedProg != nil && (!typedProg.OwnershipChecked || !typedProg.DropElaborated) {
		report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-INT-001",
			Severity:    "error",
			Message:     fmt.Sprintf("所有权 CFG 或 Drop 展开分析未通过内部一致性认证 (OwnershipChecked=%v, DropElaborated=%v)，%s", typedProg.OwnershipChecked, typedProg.DropElaborated, actionDesc),
			File:        srcFile,
			Span:        span,
			Explanation: "编译器遵循 Fail-Closed 原则，所有权与资源析构分析未完整通过认证前绝不放行 check 或生成目标 C 代码。",
		})
	}
}

func resolveBindings(prog *ast.Program) (map[string]*bindgen.BindingMeta, error) {
	bindings := make(map[string]*bindgen.BindingMeta)
	for _, imp := range prog.Imports {
		header := strings.Trim(imp.HeaderPath, "<>\"")
		switch header {
		case "zlib.h":
			cacheFile := findBindingCache("zlib_binding.json")
			meta, err := bindgen.GetOrExtractZlibBindings("/usr/include/zlib.h", cacheFile)
			if err != nil {
				return nil, fmt.Errorf("解析 C 头文件 <%s> 绑定失败: %w", header, err)
			}
			bindings["zlib"] = meta
		case "payload_loader.h":
			meta := &bindgen.BindingMeta{
				HeaderPath: "payload_loader.h",
				Functions: map[string]bindgen.FunctionSig{
					"load_benchmark_payload": {
						Name:       "load_benchmark_payload",
						QualType:   "void (const char *, unsigned char *, unsigned long)",
						ReturnType: "void",
						ParamTypes: []string{"const char *", "unsigned char *", "unsigned long"},
					},
				},
			}
			bindings["payload"] = meta
		default:
			return nil, fmt.Errorf("Phase 0B 暂不支持头文件 %s 的自动绑定提取 (目前仅支持 <zlib.h>)", imp.HeaderPath)
		}
	}
	return bindings, nil
}

func findBindingCache(filename string) string {
	candidates := []string{
		filepath.Join("bindings", "zlib", "generated", filename),
		filepath.Join("..", "bindings", "zlib", "generated", filename),
		filepath.Join("..", "..", "bindings", "zlib", "generated", filename),
		filepath.Join("..", "..", "..", "bindings", "zlib", "generated", filename),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return filepath.Join("bindings", "zlib", "generated", filename)
}

func runCheck(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	format := fs.String("format", "text", "输出格式: text | json")
	dumpTIR := fs.Bool("dump-tir", false, "输出经验证的后端无关 TIR 表示")
	_ = fs.Parse(args)

	files := fs.Args()
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "错误: 请指定输入的 .won 源码文件")
		os.Exit(1)
	}

	srcFile := files[0]
	content, err := os.ReadFile(srcFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法读取文件 %s: %v\n", srcFile, err)
		os.Exit(1)
	}

	report := diagnostic.NewReport()
	lex := lexer.New(srcFile, string(content), report)
	p := parser.New(lex, report, srcFile)
	prog := p.ParseProgram()

	if !report.HasErrors() {
		bindings, err := resolveBindings(prog)
		if err != nil {
			report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-FFI-001",
				Severity: "error",
				Message:  err.Error(),
				File:     srcFile,
				Span:     prog.GetSpan(),
			})
		}
		chk := checker.New(report, bindings)
		typedProg := chk.CheckProgram(prog)
		enforceOwnershipCheckedGate(report, typedProg, srcFile, prog.GetSpan(), "拒绝通过语义检查")
		if !report.HasErrors() && typedProg != nil {
			tirProg, tirErr := lower.LowerProgram(typedProg, bindings)
			if tirErr != nil {
				report.AddDiagnostic(diagnostic.Diagnostic{
					Code:        "WON-INT-001",
					Severity:    "error",
					Message:     fmt.Sprintf("TIR 降级或结构验证失败: %v", tirErr),
					File:        srcFile,
					Span:        prog.GetSpan(),
					Explanation: "编译器遵循 Fail-Closed 原则，TIR 降级与独立验证未通过时拒绝认定程序有效。",
				})
			} else if *dumpTIR && *format != "json" {
				fmt.Print(tirProg.Dump())
			}
		}
	}

	if *format == "json" {
		jsonStr, _ := report.ToJSON()
		fmt.Println(jsonStr)
	} else {
		fmt.Print(report.String())
	}

	if report.HasErrors() {
		os.Exit(1)
	}
}

func runBindings(args []string) {
	libName := "zlib"
	if len(args) > 0 {
		libName = args[0]
	}

	if libName != "zlib" {
		fmt.Fprintf(os.Stderr, "错误: Phase 0B 目前仅支持提取 zlib 绑定，暂不支持 %q\n", libName)
		os.Exit(1)
	}

	headerPath := "/usr/include/zlib.h"
	fmt.Printf("[wonc] 正在调用 Clang 语义 AST 分析系统头文件: %s ...\n", headerPath)
	meta, err := bindgen.ExtractZlibBindings(headerPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "提取绑定失败: %v\n", err)
		os.Exit(1)
	}

	jsonPath := findBindingCache("zlib_binding.json")
	outDir := filepath.Dir(jsonPath)
	_ = os.MkdirAll(outDir, 0755)

	jsonBytes, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(jsonPath, jsonBytes, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "写入绑定文件失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[wonc] 成功提取 %d 个函数声明与 %d 个类型别名。\n", len(meta.Functions), len(meta.Typedefs))
	fmt.Printf("[wonc] 目标 ABI 架构: %s, Clang: %s\n", meta.TargetTriple, meta.ClangVersion)
	fmt.Printf("[wonc] 绑定元数据已生成至: %s\n", jsonPath)
}

func runBuild(args []string) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	static := fs.Bool("static", false, "启用完全静态链接 (-static)")
	lto := fs.Bool("lto", false, "启用链接时优化 (-flto)")
	format := fs.String("format", "text", "诊断输出格式: text | json")
	output := fs.String("o", "", "输出二进制路径")
	includeDir := fs.String("I", "", "附加 C 头文件包含目录")
	keepC := fs.Bool("emit-c", false, "保留生成的中间 C 代码")
	_ = fs.Parse(args)

	files := fs.Args()
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "错误: 请指定输入的 .won 源码文件")
		os.Exit(1)
	}

	srcFile := files[0]
	content, err := os.ReadFile(srcFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法读取文件 %s: %v\n", srcFile, err)
		os.Exit(1)
	}

	report := diagnostic.NewReport()
	lex := lexer.New(srcFile, string(content), report)
	p := parser.New(lex, report, srcFile)
	prog := p.ParseProgram()

	var bindings map[string]*bindgen.BindingMeta
	var typedProg *checker.TypedProgram
	if !report.HasErrors() {
		var bindErr error
		bindings, bindErr = resolveBindings(prog)
		if bindErr != nil {
			report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-FFI-001",
				Severity: "error",
				Message:  bindErr.Error(),
				File:     srcFile,
				Span:     prog.GetSpan(),
			})
		}
		chk := checker.New(report, bindings)
		typedProg = chk.CheckProgram(prog)
	}

	enforceOwnershipCheckedGate(report, typedProg, srcFile, prog.GetSpan(), "拒绝生成 C11 代码")

	var tirProg *tir.Program
	if !report.HasErrors() && typedProg != nil {
		var tirErr error
		tirProg, tirErr = lower.LowerProgram(typedProg, bindings)
		if tirErr != nil {
			report.AddDiagnostic(diagnostic.Diagnostic{
				Code:        "WON-INT-001",
				Severity:    "error",
				Message:     fmt.Sprintf("TIR 降级或结构验证失败: %v", tirErr),
				File:        srcFile,
				Span:        prog.GetSpan(),
				Explanation: "Phase 2C.1 强制要求所有原生构建经由后端无关 TIR 降级与结构验证。",
			})
		}
	}

	// 2. 通过纯 TIR 驱动的 C11 后端执行能力校验并生成 ISO C11 源码（严禁回退至旧 AST 发射路径）
	var cSource string
	if !report.HasErrors() {
		em := c11.NewEmitter(c11.DefaultLinuxX86_64())
		var emitErr error
		cSource, emitErr = em.Emit(tirProg)
		if emitErr != nil {
			var capErr *c11.CapabilityError
			if errors.As(emitErr, &capErr) {
				errSpan := prog.GetSpan()
				if capErr.TIRSpan.StartLine > 0 {
					errSpan = diagnostic.Span{
						File:  capErr.TIRSpan.File,
						Start: diagnostic.Position{Line: capErr.TIRSpan.StartLine, Column: capErr.TIRSpan.StartCol, Byte: capErr.TIRSpan.StartByte},
						End:   diagnostic.Position{Line: capErr.TIRSpan.EndLine, Column: capErr.TIRSpan.EndCol, Byte: capErr.TIRSpan.EndByte},
					}
				}
				if errSpan.File == "" {
					errSpan.File = srcFile
				}
				report.AddDiagnostic(diagnostic.Diagnostic{
					Code:        capErr.Code,
					Severity:    "error",
					Message:     capErr.Message,
					File:        errSpan.File,
					Span:        errSpan,
					Explanation: capErr.Explanation,
				})
			} else {
				report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-GEN-001",
					Severity: "error",
					Message:  emitErr.Error(),
					File:     srcFile,
					Span:     prog.GetSpan(),
				})
			}
		}
	}

	if report.HasErrors() {
		if *format == "json" {
			jsonStr, _ := report.ToJSON()
			fmt.Println(jsonStr)
		} else {
			fmt.Print(report.String())
		}
		os.Exit(1)
	}

	baseName := strings.TrimSuffix(filepath.Base(srcFile), filepath.Ext(srcFile))
	cFile := baseName + ".won.c"
	if err := os.WriteFile(cFile, []byte(cSource), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "写入生成的中间 C 文件失败: %v\n", err)
		os.Exit(1)
	}

	outBin := *output
	if outBin == "" {
		outBin = baseName
	}

	extraCFlags := []string{"-I.", "-Ibenchmarks/phase0/payloads"}
	if *includeDir != "" {
		extraCFlags = append(extraCFlags, "-I", *includeDir)
	}
	if *lto {
		extraCFlags = append(extraCFlags, "-flto")
	}

	// 3. 原生编译链接
	toolOpts := toolchain.Options{
		CompilerPath: "gcc",
		SourcePath:   cFile,
		OutputPath:   outBin,
		Static:       *static,
		OptLevel:     "-O3",
		ExtraCFlags:  extraCFlags,
		ExtraLibs:    []string{"-lz"},
	}

	_, compileErr := toolchain.CompileNative(toolOpts)

	if !*keepC {
		_ = os.Remove(cFile)
	}

	if compileErr != nil {
		if *static && (strings.Contains(compileErr.Error(), "cannot find -l") || strings.Contains(compileErr.Error(), "have you installed the static version")) {
			fmt.Fprintf(os.Stderr, "[wonc 错误] 静态链接失败: 系统环境缺少静态库归档文件 (如 libc.a 或 libz.a，在 Fedora/RHEL 下需安装 glibc-static 与 zlib-static)。\n底层链接器报错:\n%s\n", compileErr.Error())
		} else {
			fmt.Fprintf(os.Stderr, "[wonc 编译错误]: %v\n", compileErr)
		}
		os.Exit(1)
	}

	fmt.Printf("[wonc] 成功编译生成原生机器码可执行文件: %s\n", outBin)
}

// WON Phase 0B 编译器与命令行驱动入口 (wonc)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/codegen"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/lexer"
	"github.com/KitiLab/WON/compiler/poc/internal/parser"
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
	fmt.Println("WON Phase 0B 编译器工具链 (wonc)")
	fmt.Println("用法:")
	fmt.Println("  wonc version                     查看编译器版本与目标架构")
	fmt.Println("  wonc check [--format=json] <file> 语法与语义检查")
	fmt.Println("  wonc bindings [zlib]             从系统 C 头文件生成绑定")
	fmt.Println("  wonc build [--static] [-o out] <file> 编译为原生机器码可执行文件")
}

func runCheck(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
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
	prog := p.ParseProgram()

	if !report.HasErrors() {
		chk := checker.New(report)
		chk.CheckProgram(prog)
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

	// 写入 bindings/zlib/generated/ 目录
	outDir := "bindings/zlib/generated"
	_ = os.MkdirAll(outDir, 0755)

	jsonBytes, _ := json.MarshalIndent(meta, "", "  ")
	jsonPath := filepath.Join(outDir, "zlib_binding.json")
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

	if !report.HasErrors() {
		chk := checker.New(report)
		chk.CheckProgram(prog)
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

	// 2. 生成 C 代码
	gen := codegen.New(prog, nil)
	cSource, err := gen.Generate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "生成 C 代码失败: %v\n", err)
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
		if *static && strings.Contains(compileErr.Error(), "have you installed the static version") {
			fmt.Fprintf(os.Stderr, "[wonc 错误] 静态链接失败: 系统环境缺少 glibc-static (libc.a) 或 zlib-static (libz.a)。\n底层错误:\n%s\n", compileErr.Error())
		} else {
			fmt.Fprintf(os.Stderr, "[wonc 编译错误]: %v\n", compileErr)
		}
		os.Exit(1)
	}

	fmt.Printf("[wonc] 成功编译生成原生机器码可执行文件: %s\n", outBin)
}

// Package toolchain 封装对底层 GCC/Clang 原生编译器与链接器的精确调用
package toolchain

import (
	"bytes"
	"fmt"
	"os/exec"
)

// Options 编译参数选项
type Options struct {
	CompilerPath string   // gcc 或 clang 的绝对路径
	SourcePath   string   // 输入的 C 源码路径
	OutputPath   string   // 输出的原生可执行文件路径
	Static       bool     // 是否启用静态链接 (-static)
	OptLevel     string   // 优化级别，如 "-O3"
	ExtraCFlags  []string // 附加编译选项
	ExtraLibs    []string // 附加链接库，如 "-lz"
}

// CompileNative 调用原生编译器将 C 源码编译链接为机器码二进制文件
func CompileNative(opts Options) (string, error) {
	compiler := opts.CompilerPath
	if compiler == "" {
		compiler = "gcc"
	}

	optLevel := opts.OptLevel
	if optLevel == "" {
		optLevel = "-O3"
	}

	args := []string{
		"-std=c11",
		optLevel,
		opts.SourcePath,
		"-o", opts.OutputPath,
	}

	if opts.Static {
		args = append(args, "-static")
	}

	args = append(args, opts.ExtraCFlags...)
	args = append(args, opts.ExtraLibs...)

	cmd := exec.Command(compiler, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	outputLog := fmt.Sprintf("Command: %s %v\nStdout: %s\nStderr: %s",
		compiler, args, stdout.String(), stderr.String())

	if err != nil {
		return outputLog, fmt.Errorf("原生编译器退出错误 (%w): %s", err, stderr.String())
	}

	return outputLog, nil
}

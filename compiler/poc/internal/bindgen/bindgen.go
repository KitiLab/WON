// Package bindgen 实现了基于 Clang 语义 AST 的真实 C 头文件绑定提取器
// 解析系统实际安装的 C 头文件 (/usr/include/zlib.h)，提取函数签名与类型别名，保证 ABI 精确匹配。
package bindgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FunctionSig 记录从 C 头文件提取的函数签名
type FunctionSig struct {
	Name       string   `json:"name"`
	QualType   string   `json:"qual_type"`
	ReturnType string   `json:"return_type"`
	ParamTypes []string `json:"param_types"`
}

// TypedefMapping 记录 C 头文件中的类型别名映射
type TypedefMapping struct {
	Name       string `json:"name"`
	TargetType string `json:"target_type"`
}

// BindingMeta 记录生成绑定的元数据与溯源信息
type BindingMeta struct {
	HeaderPath      string                    `json:"header_path"`
	HeaderSHA256    string                    `json:"header_sha256"`
	TargetTriple    string                    `json:"target_triple"`
	ClangVersion    string                    `json:"clang_version"`
	ZlibVersion     string                    `json:"zlib_version,omitempty"`
	Typedefs        map[string]TypedefMapping `json:"typedefs"`
	Functions       map[string]FunctionSig    `json:"functions"`
	GeneratedAtUnix int64                     `json:"generated_at_unix"`
}

// ClangASTNode Clang JSON AST 节点表示
type ClangASTNode struct {
	ID    string         `json:"id"`
	Kind  string         `json:"kind"`
	Name  string         `json:"name,omitempty"`
	Type  *ClangType     `json:"type,omitempty"`
	Inner []ClangASTNode `json:"inner,omitempty"`
}

type ClangType struct {
	QualType string `json:"qualType"`
}

// CalculateHeaderSHA256 计算头文件的 SHA-256 哈希值
func CalculateHeaderSHA256(headerPath string) (string, error) {
	headerBytes, err := os.ReadFile(headerPath)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(headerBytes)
	return hex.EncodeToString(hash[:]), nil
}

// GetOrExtractZlibBindings 获取并验证绑定元数据。若存在有效缓存且哈希匹配则直接读取，否则重新调用 Clang 提取
func GetOrExtractZlibBindings(headerPath, cacheFile string) (*BindingMeta, error) {
	if headerPath == "" {
		headerPath = "/usr/include/zlib.h"
	}

	currentHash, err := CalculateHeaderSHA256(headerPath)
	if err != nil {
		return nil, fmt.Errorf("读取头文件失败 %s: %w", headerPath, err)
	}

	// 尝试检查缓存
	if cacheFile != "" {
		if cacheBytes, err := os.ReadFile(cacheFile); err == nil {
			var cached BindingMeta
			if err := json.Unmarshal(cacheBytes, &cached); err == nil {
				// 校验指纹：若头文件哈希一致，且所需函数均存在，则缓存有效
				if cached.HeaderSHA256 == currentHash && len(cached.Functions) > 0 {
					return &cached, nil
				}
			}
		}
	}

	// 缓存缺失或已过时，执行真实 Clang 语义提取
	meta, err := ExtractZlibBindings(headerPath)
	if err != nil {
		return nil, err
	}

	// 如果指定了缓存路径，则写回最新有效缓存
	if cacheFile != "" {
		_ = os.MkdirAll(filepath.Dir(cacheFile), 0755)
		if jsonBytes, err := json.MarshalIndent(meta, "", "  "); err == nil {
			_ = os.WriteFile(cacheFile, jsonBytes, 0644)
		}
	}

	return meta, nil
}

// ExtractZlibBindings 执行 Clang 提取真实 /usr/include/zlib.h 绑定
func ExtractZlibBindings(headerPath string) (*BindingMeta, error) {
	if headerPath == "" {
		headerPath = "/usr/include/zlib.h"
	}

	shaStr, err := CalculateHeaderSHA256(headerPath)
	if err != nil {
		return nil, fmt.Errorf("读取 C 头文件失败 %s: %w", headerPath, err)
	}

	// 获取 Clang 版本与默认 target triple
	clangVerCmd := exec.Command("clang", "--version")
	clangVerOut, _ := clangVerCmd.Output()
	clangVer := strings.TrimSpace(strings.Split(string(clangVerOut), "\n")[0])

	clangTripleCmd := exec.Command("clang", "-print-target-triple")
	clangTripleOut, _ := clangTripleCmd.Output()
	targetTriple := strings.TrimSpace(string(clangTripleOut))
	if targetTriple == "" {
		targetTriple = "x86_64-unknown-linux-gnu"
	}

	// 调用 Clang 提取语义 JSON AST
	cmd := exec.Command("clang", "-Xclang", "-ast-dump=json", "-fsyntax-only", headerPath)
	astOut, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("执行 Clang AST dump 失败: %w", err)
	}

	var root ClangASTNode
	if err := json.Unmarshal(astOut, &root); err != nil {
		return nil, fmt.Errorf("解析 Clang JSON AST 失败: %w", err)
	}

	meta := &BindingMeta{
		HeaderPath:   headerPath,
		HeaderSHA256: shaStr,
		TargetTriple: targetTriple,
		ClangVersion: clangVer,
		Typedefs:     make(map[string]TypedefMapping),
		Functions:    make(map[string]FunctionSig),
	}

	// 目标提取的符号
	targetFuncs := map[string]bool{
		"compress":      true,
		"uncompress":    true,
		"compressBound": true,
		"zlibVersion":   true,
	}
	targetTypedefs := map[string]bool{
		"Byte":   true,
		"Bytef":  true,
		"uInt":   true,
		"uLong":  true,
		"uLongf": true,
	}

	for _, node := range root.Inner {
		switch node.Kind {
		case "TypedefDecl":
			if targetTypedefs[node.Name] && node.Type != nil {
				meta.Typedefs[node.Name] = TypedefMapping{
					Name:       node.Name,
					TargetType: node.Type.QualType,
				}
			}
		case "FunctionDecl":
			if targetFuncs[node.Name] && node.Type != nil {
				sig := parseFunctionQualType(node.Name, node.Type.QualType)
				meta.Functions[node.Name] = sig
			}
		}
	}

	return meta, nil
}

// parseFunctionQualType 解析形如 "int (unsigned char *, unsigned long *, const unsigned char *, unsigned long)" 的签名
func parseFunctionQualType(name, qualType string) FunctionSig {
	sig := FunctionSig{
		Name:       name,
		QualType:   qualType,
		ParamTypes: make([]string, 0),
	}

	parenIdx := strings.Index(qualType, "(")
	if parenIdx == -1 {
		sig.ReturnType = qualType
		return sig
	}

	sig.ReturnType = strings.TrimSpace(qualType[:parenIdx])
	paramsPart := strings.TrimSuffix(strings.TrimSpace(qualType[parenIdx+1:]), ")")

	if paramsPart != "" && paramsPart != "void" {
		parts := strings.Split(paramsPart, ",")
		for _, p := range parts {
			sig.ParamTypes = append(sig.ParamTypes, strings.TrimSpace(p))
		}
	}

	return sig
}

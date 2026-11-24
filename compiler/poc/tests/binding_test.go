package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
)

// TestBindingCacheFingerprintValidation 测试绑定元数据的 SHA-256 复合指纹校验与自动失效重提
func TestBindingCacheFingerprintValidation(t *testing.T) {
	headerPath := "/usr/include/zlib.h"
	compositeHash, depFiles, err := bindgen.CalculateCompositeFingerprint(headerPath)
	if err != nil {
		t.Fatalf("无法计算 %s 的复合依赖指纹: %v", headerPath, err)
	}

	if len(compositeHash) != 64 {
		t.Fatalf("复合指纹长度非法: %s", compositeHash)
	}
	if len(depFiles) == 0 {
		t.Fatalf("未发现任何依赖头文件")
	}

	tmpDir := t.TempDir()
	cacheFile := filepath.Join(tmpDir, "test_zlib_binding.json")

	// 1. 首次提取：生成真实缓存文件
	meta1, err := bindgen.GetOrExtractZlibBindings(headerPath, cacheFile)
	if err != nil {
		t.Fatalf("首次提取绑定失败: %v", err)
	}
	if meta1.HeaderSHA256 != compositeHash {
		t.Errorf("提取的元数据复合哈希不匹配: %s vs %s", meta1.HeaderSHA256, compositeHash)
	}

	// 验证缓存文件已被创建
	if _, err := os.Stat(cacheFile); err != nil {
		t.Fatalf("预期缓存文件已生成，但不存在: %v", err)
	}

	// 2. 二次读取：应直接命中有效缓存
	meta2, err := bindgen.GetOrExtractZlibBindings(headerPath, cacheFile)
	if err != nil {
		t.Fatalf("命中缓存读取失败: %v", err)
	}
	if meta2.HeaderSHA256 != compositeHash {
		t.Errorf("缓存读取的哈希不匹配: %s", meta2.HeaderSHA256)
	}

	// 3. 模拟陈旧/伪造的缓存指纹 (篡改 header_sha256)
	meta2.HeaderSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	staleBytes, _ := json.Marshal(meta2)
	if err := os.WriteFile(cacheFile, staleBytes, 0644); err != nil {
		t.Fatalf("写入陈旧缓存失败: %v", err)
	}

	// 4. 指纹失效重提：当检测到哈希不匹配时，自动废弃陈旧缓存并重新由 Clang 提取
	meta3, err := bindgen.GetOrExtractZlibBindings(headerPath, cacheFile)
	if err != nil {
		t.Fatalf("陈旧缓存重提失败: %v", err)
	}
	if meta3.HeaderSHA256 != compositeHash {
		t.Errorf("重新提取后的元数据哈希应与当前复合指纹一致: %s vs %s", meta3.HeaderSHA256, compositeHash)
	}

	// 5. 模拟损坏的 JSON 缓存文件
	if err := os.WriteFile(cacheFile, []byte("{ corrupted json content ..."), 0644); err != nil {
		t.Fatalf("写入损坏缓存失败: %v", err)
	}
	meta4, err := bindgen.GetOrExtractZlibBindings(headerPath, cacheFile)
	if err != nil {
		t.Fatalf("损坏缓存自动恢复重提失败: %v", err)
	}
	if meta4.HeaderSHA256 != compositeHash {
		t.Errorf("损坏缓存恢复后的哈希应有效: %s", meta4.HeaderSHA256)
	}

	t.Logf("C 头文件指纹防伪与自动重提机制验证通过！包含依赖文件数: %d", len(depFiles))
}

// TestTransitiveDependencyFingerprintInvalidation 验证传递包含头文件（如 zconf.h）发生变更时缓存失效重提
func TestTransitiveDependencyFingerprintInvalidation(t *testing.T) {
	tmpDir := t.TempDir()
	subHeader := filepath.Join(tmpDir, "dep_sub.h")
	mainHeader := filepath.Join(tmpDir, "dep_main.h")

	// 1. 初始化两层嵌套 C 头文件
	subContent1 := `
#ifndef DEP_SUB_H
#define DEP_SUB_H
typedef unsigned long sub_size_t;
#endif
`
	mainContent := `
#ifndef DEP_MAIN_H
#define DEP_MAIN_H
#include "dep_sub.h"
int test_func(sub_size_t n);
#endif
`
	if err := os.WriteFile(subHeader, []byte(subContent1), 0644); err != nil {
		t.Fatalf("创建 dep_sub.h 失败: %v", err)
	}
	if err := os.WriteFile(mainHeader, []byte(mainContent), 0644); err != nil {
		t.Fatalf("创建 dep_main.h 失败: %v", err)
	}

	// 计算初始复合指纹
	fp1, deps1, err := bindgen.CalculateCompositeFingerprint(mainHeader)
	if err != nil {
		t.Fatalf("计算初始指纹失败: %v", err)
	}

	// 验证探针捕获到了传递包含的 dep_sub.h
	subFound := false
	for _, d := range deps1 {
		if filepath.Base(d) == "dep_sub.h" {
			subFound = true
			break
		}
	}
	if !subFound {
		t.Fatalf("依赖探针未能捕获传递包含的 dep_sub.h，捕获列表: %v", deps1)
	}

	// 2. 模拟修改底层间接依赖头文件 dep_sub.h
	subContent2 := `
#ifndef DEP_SUB_H
#define DEP_SUB_H
typedef unsigned long long sub_size_t; /* ABI 类型定义修改 */
#endif
`
	if err := os.WriteFile(subHeader, []byte(subContent2), 0644); err != nil {
		t.Fatalf("更新 dep_sub.h 失败: %v", err)
	}

	// 重新计算主头文件的复合指纹
	fp2, _, err := bindgen.CalculateCompositeFingerprint(mainHeader)
	if err != nil {
		t.Fatalf("重新计算指纹失败: %v", err)
	}

	// 3. 断言传递包含变更必然导致复合哈希变更，防止陈旧 ABI 逃逸
	if fp1 == fp2 {
		t.Fatalf("底层间接头文件变更后复合指纹未变化！(fp1 == fp2 == %s)", fp1)
	}

	t.Logf("间接头文件变更指纹失效验证通过: 初始指纹 [%s] -> 新指纹 [%s]", fp1[:16], fp2[:16])
}

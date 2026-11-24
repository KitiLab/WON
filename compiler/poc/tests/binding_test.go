package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
)

// TestBindingCacheFingerprintValidation 测试绑定元数据的 SHA-256 指纹校验与自动失效重提
func TestBindingCacheFingerprintValidation(t *testing.T) {
	headerPath := "/usr/include/zlib.h"
	realHash, err := bindgen.CalculateHeaderSHA256(headerPath)
	if err != nil {
		t.Fatalf("无法计算 %s 的哈希: %v", headerPath, err)
	}

	tmpDir := t.TempDir()
	cacheFile := filepath.Join(tmpDir, "test_zlib_binding.json")

	// 1. 首次提取：生成真实缓存文件
	meta1, err := bindgen.GetOrExtractZlibBindings(headerPath, cacheFile)
	if err != nil {
		t.Fatalf("首次提取绑定失败: %v", err)
	}
	if meta1.HeaderSHA256 != realHash {
		t.Errorf("提取的元数据哈希不匹配: %s vs %s", meta1.HeaderSHA256, realHash)
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
	if meta2.HeaderSHA256 != realHash {
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
	if meta3.HeaderSHA256 != realHash {
		t.Errorf("重新提取后的元数据哈希应与当前头文件一致: %s vs %s", meta3.HeaderSHA256, realHash)
	}

	// 5. 模拟损坏的 JSON 缓存文件
	if err := os.WriteFile(cacheFile, []byte("{ corrupted json content ..."), 0644); err != nil {
		t.Fatalf("写入损坏缓存失败: %v", err)
	}
	meta4, err := bindgen.GetOrExtractZlibBindings(headerPath, cacheFile)
	if err != nil {
		t.Fatalf("损坏缓存自动恢复重提失败: %v", err)
	}
	if meta4.HeaderSHA256 != realHash {
		t.Errorf("损坏缓存恢复后的哈希应有效: %s", meta4.HeaderSHA256)
	}

	t.Logf("C 头文件指纹防伪与自动重提机制验证通过！")
}

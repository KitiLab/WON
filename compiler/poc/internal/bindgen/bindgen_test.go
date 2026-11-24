package bindgen

import (
	"testing"
)

func TestExtractRealZlibHeader(t *testing.T) {
	meta, err := ExtractZlibBindings("/usr/include/zlib.h")
	if err != nil {
		t.Fatalf("提取真实系统 zlib 头文件失败: %v", err)
	}

	if len(meta.HeaderSHA256) != 64 {
		t.Errorf("HeaderSHA256 应为 64 位十六进制哈希, 实际为: %s", meta.HeaderSHA256)
	}

	requiredFuncs := []string{"compress", "uncompress", "compressBound", "zlibVersion"}
	for _, fn := range requiredFuncs {
		if _, ok := meta.Functions[fn]; !ok {
			t.Errorf("缺少必须的导出的函数: %s", fn)
		}
	}

	requiredTypedefs := []string{"Bytef", "uLong", "uLongf"}
	for _, td := range requiredTypedefs {
		if _, ok := meta.Typedefs[td]; !ok {
			t.Errorf("缺少必须的类型别名: %s", td)
		}
	}

	t.Logf("成功提取 %d 个函数与 %d 个类型别名，目标 ABI: %s", len(meta.Functions), len(meta.Typedefs), meta.TargetTriple)
}

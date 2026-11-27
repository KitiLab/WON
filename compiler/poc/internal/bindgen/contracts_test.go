package bindgen_test

import (
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
)

func TestPhase2B3_2_SchemaVersionValidation(t *testing.T) {
	// 正例：规范版本 "1.0"
	valid := `{
		"schema_version": "1.0",
		"header_path": "demo.h",
		"namespace": "demo",
		"functions": {
			"ping": {
				"contract_id": "demo.ping",
				"return": {"ownership": "raw_unmanaged", "borrows_param": -1}
			}
		}
	}`
	cat, err := bindgen.ParseFFIContractJSON([]byte(valid))
	if err != nil {
		t.Fatalf("规范 schema_version=1.0 契约应当解析成功，实际失败: %v", err)
	}
	if cat.SchemaVersion != bindgen.SupportedFFIContractSchemaVersion {
		t.Fatalf("期望 SchemaVersion=%q，实际 %q", bindgen.SupportedFFIContractSchemaVersion, cat.SchemaVersion)
	}

	// 反例：缺失、空串、纯空白、格式畸形、历史/未来不受支持版本
	badVersions := []struct {
		name string
		raw  string
	}{
		{"missing_version", `{"header_path":"demo.h","namespace":"demo"}`},
		{"empty_version", `{"schema_version":"","header_path":"demo.h","namespace":"demo"}`},
		{"whitespace_version", `{"schema_version":"   ","header_path":"demo.h","namespace":"demo"}`},
		{"padded_version", `{"schema_version":" 1.0 ","header_path":"demo.h","namespace":"demo"}`},
		{"unsupported_future_version", `{"schema_version":"2.0","header_path":"demo.h","namespace":"demo"}`},
		{"unsupported_older_version", `{"schema_version":"0.9","header_path":"demo.h","namespace":"demo"}`},
		{"malformed_prefix_v", `{"schema_version":"v1.0","header_path":"demo.h","namespace":"demo"}`},
		{"malformed_patch_version", `{"schema_version":"1.0.0","header_path":"demo.h","namespace":"demo"}`},
		{"malformed_numeric_version", `{"schema_version":1.0,"header_path":"demo.h","namespace":"demo"}`},
		{"unknown_field_rejected", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo","extra":true}`},
		{"trailing_json_rejected", `{"schema_version":"1.0","header_path":"demo.h","namespace":"demo"} {"extra":1}`},
	}
	for _, tc := range badVersions {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := bindgen.ParseFFIContractJSON([]byte(tc.raw)); err == nil {
				t.Fatalf("schema_version 非法用例 %s 必须被拒绝", tc.name)
			}
		})
	}
}

func TestPhase2B3_2_HeaderIdentityValidation(t *testing.T) {
	// 1. 外部 JSON 缺失/空/空白/畸形 header_path 必须拒绝
	badHeaders := []struct {
		name string
		raw  string
	}{
		{"missing_header_path", `{"schema_version":"1.0","namespace":"demo"}`},
		{"empty_header_path", `{"schema_version":"1.0","header_path":"","namespace":"demo"}`},
		{"whitespace_header_path", `{"schema_version":"1.0","header_path":"   ","namespace":"demo"}`},
		{"padded_header_path", `{"schema_version":"1.0","header_path":" demo.h ","namespace":"demo"}`},
		{"parent_traversal", `{"schema_version":"1.0","header_path":"../demo.h","namespace":"demo"}`},
		{"nested_parent_traversal", `{"schema_version":"1.0","header_path":"include/../demo.h","namespace":"demo"}`},
		{"backslash_path", `{"schema_version":"1.0","header_path":"include\\demo.h","namespace":"demo"}`},
		{"angle_brackets", `{"schema_version":"1.0","header_path":"<demo.h>","namespace":"demo"}`},
		{"quoted_header", `{"schema_version":"1.0","header_path":"\"demo.h\"","namespace":"demo"}`},
		{"directory_only_slash", `{"schema_version":"1.0","header_path":"/usr/include/","namespace":"demo"}`},
		{"dot_header", `{"schema_version":"1.0","header_path":".","namespace":"demo"}`},
		{"root_slash_header", `{"schema_version":"1.0","header_path":"/","namespace":"demo"}`},
	}
	for _, tc := range badHeaders {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := bindgen.ParseFFIContractJSON([]byte(tc.raw)); err == nil {
				t.Fatalf("header_path 非法用例 %s 必须被拒绝", tc.name)
			}
		})
	}

	// 2. 一致的相对路径标识与绝对路径标识校验
	consistentPairs := []struct {
		name          string
		bindingHeader string
		catalogHeader string
	}{
		{"exact_relative", "demo.h", "demo.h"},
		{"clean_relative_dot_prefix", "include/demo.h", "./include/demo.h"},
		{"exact_absolute", "/usr/include/zlib.h", "/usr/include/zlib.h"},
		{"clean_absolute", "/usr/include/zlib.h", "/usr/include//./zlib.h"},
	}
	for _, tc := range consistentPairs {
		t.Run("match_"+tc.name, func(t *testing.T) {
			bindings := map[string]*bindgen.BindingMeta{
				"demo": {
					HeaderPath: tc.bindingHeader,
					Contracts: &bindgen.FFIContractCatalog{
						SchemaVersion: bindgen.SupportedFFIContractSchemaVersion,
						HeaderPath:    tc.catalogHeader,
						Namespace:     "demo",
					},
				},
			}
			if err := bindgen.ValidateBindingContracts(bindings); err != nil {
				t.Fatalf("一致的头文件标识 (%q vs %q) 应当校验通过，实际失败: %v", tc.bindingHeader, tc.catalogHeader, err)
			}
		})
	}

	// 3. 不一致或缺失的 BindingMeta.HeaderPath 与 Catalog.HeaderPath 必须拒绝
	mismatchPairs := []struct {
		name          string
		bindingHeader string
		catalogHeader string
	}{
		{"different_filename", "a.h", "b.h"},
		{"relative_vs_absolute_same_basename", "zlib.h", "/usr/include/zlib.h"},
		{"different_absolute_dirs", "/usr/include/demo.h", "/opt/vendor/demo.h"},
		{"empty_binding_header", "", "demo.h"},
		{"traversal_binding_header", "../demo.h", "demo.h"},
	}
	for _, tc := range mismatchPairs {
		t.Run("mismatch_"+tc.name, func(t *testing.T) {
			bindings := map[string]*bindgen.BindingMeta{
				"demo": {
					HeaderPath: tc.bindingHeader,
					Contracts: &bindgen.FFIContractCatalog{
						SchemaVersion: bindgen.SupportedFFIContractSchemaVersion,
						HeaderPath:    tc.catalogHeader,
						Namespace:     "demo",
					},
				},
			}
			err := bindgen.ValidateBindingContracts(bindings)
			if err == nil {
				t.Fatalf("不一致头文件标识 (%q vs %q) 必须被 ValidateBindingContracts 拒绝", tc.bindingHeader, tc.catalogHeader)
			}
			if !strings.Contains(err.Error(), "header") && !strings.Contains(err.Error(), "头文件") {
				t.Fatalf("期望头文件不一致错误提示，实际: %v", err)
			}
		})
	}

	// 4. 多目录同命名空间声明矛盾 HeaderPath 必须在索引前拒绝
	t.Run("same_namespace_conflicting_headers_in_index", func(t *testing.T) {
		c1 := &bindgen.FFIContractCatalog{
			SchemaVersion: bindgen.SupportedFFIContractSchemaVersion,
			HeaderPath:    "/usr/include/a.h",
			Namespace:     "demo",
		}
		c2 := &bindgen.FFIContractCatalog{
			SchemaVersion: bindgen.SupportedFFIContractSchemaVersion,
			HeaderPath:    "/usr/include/b.h",
			Namespace:     "demo",
		}
		if _, _, err := bindgen.IndexValidatedFFIContracts([]*bindgen.FFIContractCatalog{c1, c2}); err == nil {
			t.Fatalf("同命名空间下不同 header_path 的契约目录必须被拒绝")
		}
	})
}

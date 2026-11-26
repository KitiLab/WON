package bindgen

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FFIOwnershipKind 定义显式 FFI 边的所有权与借用契约语义类别。
// 未在旁路契约中显式声明的 C 函数一律保持 FFIUnknown（Fail-Closed，不得猜测 @owned 或释放函数）。
type FFIOwnershipKind string

const (
	FFIUnknown        FFIOwnershipKind = "unknown"
	FFIOwnedTransfer  FFIOwnershipKind = "owned_transfer" // 调用将参数所有权移交给 C（对应 @owned 入参）
	FFIBorrowedShared FFIOwnershipKind = "borrow"         // 调用期间不可变借用参数（对应 @borrow）
	FFIBorrowedMut    FFIOwnershipKind = "borrow_mut"     // 调用期间独占可变借用参数（对应 @borrow(mut)）
	FFIOwnedReturn    FFIOwnershipKind = "owned_return"   // 返回值移交新资源所有权给 WON 调用方（必须配对 ReleaseFn）
	FFIOpaqueHandle   FFIOwnershipKind = "opaque_handle"  // 受 ReleaseFn 托管的 C 不透明句柄类型
	FFINonOwningRaw   FFIOwnershipKind = "raw_unmanaged"  // 原生无所有权裸指针 (*const T / *mut T)
)

// FFIParamContract 描述单个 C 函数入参的所有权转移或借用边界契约。
type FFIParamContract struct {
	ParamIndex int              `json:"param_index"`
	ParamName  string           `json:"param_name,omitempty"`
	Ownership  FFIOwnershipKind `json:"ownership"`
	ReleaseFn  string           `json:"release_fn,omitempty"`
}

// FFIReturnContract 描述 C 函数返回值的所有权与析构绑定规则。
type FFIReturnContract struct {
	Ownership    FFIOwnershipKind `json:"ownership"`
	TypeName     string           `json:"type_name,omitempty"`
	ReleaseFn    string           `json:"release_fn,omitempty"`
	BorrowsParam int              `json:"borrows_param"` // >=0 表示返回借用源自入参下标；-1 表示独立或无借用
}

// FFIFuncContract 定义单个外部 C 函数完整的显式所有权签名契约。
type FFIFuncContract struct {
	Namespace       string             `json:"namespace"`
	Function        string             `json:"function"`
	ContractID      string             `json:"contract_id"`
	IsCertifiedSafe bool               `json:"certified_safe"`
	Params          []FFIParamContract `json:"params,omitempty"`
	Return          FFIReturnContract  `json:"return"`
}

// FFIContractCatalog 是与 Clang C 头文件元数据正交的旁路 FFI 所有权契约清单模型（供 .wonbind JSON 旁路集成）。
type FFIContractCatalog struct {
	SchemaVersion string                       `json:"schema_version"`
	HeaderPath    string                       `json:"header_path"`
	Namespace     string                       `json:"namespace"`
	OpaqueTypes   map[string]FFIReturnContract `json:"opaque_types,omitempty"`
	Functions     map[string]FFIFuncContract   `json:"functions,omitempty"`
}

// Validate 对外部加载或构建的 FFI 所有权契约执行严格一致性校验，缺失释放函数或未知形态按 Fail-Closed 报错。
func (c *FFIContractCatalog) Validate() error {
	if c == nil {
		return fmt.Errorf("FFI 所有权契约清单为空")
	}
	if strings.TrimSpace(c.Namespace) == "" {
		return fmt.Errorf("FFI 所有权契约缺少 namespace 声明")
	}
	for typeName, ot := range c.OpaqueTypes {
		if ot.Ownership == FFIOpaqueHandle || ot.Ownership == FFIOwnedReturn {
			if strings.TrimSpace(ot.ReleaseFn) == "" {
				return fmt.Errorf("不透明资源句柄 '%s' 声明了拥有权但缺少显式 release_fn", typeName)
			}
		}
	}
	for fnName, fc := range c.Functions {
		switch fc.Return.Ownership {
		case FFIOwnedReturn, FFIOpaqueHandle:
			if strings.TrimSpace(fc.Return.ReleaseFn) == "" {
				return fmt.Errorf("FFI 函数 '%s::%s' 声明返回拥有权资源但缺少显式 release_fn", c.Namespace, fnName)
			}
		case FFIUnknown, "":
			if fc.IsCertifiedSafe {
				return fmt.Errorf("FFI 函数 '%s::%s' 标记为 certified_safe 但返回所有权未明确定义", c.Namespace, fnName)
			}
		}
		seenIdx := make(map[int]bool, len(fc.Params))
		for _, p := range fc.Params {
			if p.ParamIndex < 0 {
				return fmt.Errorf("FFI 函数 '%s::%s' 包含非法参数下标 %d", c.Namespace, fnName, p.ParamIndex)
			}
			if seenIdx[p.ParamIndex] {
				return fmt.Errorf("FFI 函数 '%s::%s' 参数下标 %d 出现重复契约声明", c.Namespace, fnName, p.ParamIndex)
			}
			seenIdx[p.ParamIndex] = true
			if p.Ownership == "" {
				return fmt.Errorf("FFI 函数 '%s::%s' 参数 #%d 缺少明确的 ownership 分类", c.Namespace, fnName, p.ParamIndex)
			}
			if fc.IsCertifiedSafe && p.Ownership == FFIUnknown {
				return fmt.Errorf("FFI 函数 '%s::%s' 声明 certified_safe 时禁止存在 unknown 入参 #%d", c.Namespace, fnName, p.ParamIndex)
			}
		}
	}
	return nil
}

// ParseFFIContractJSON 解析并校验旁路 FFI 所有权契约 JSON（供工具链与未来 .wonbind 编译器旁路使用）。
func ParseFFIContractJSON(raw []byte) (*FFIContractCatalog, error) {
	var cat FFIContractCatalog
	if err := json.Unmarshal(raw, &cat); err != nil {
		return nil, fmt.Errorf("解析 FFI 所有权契约 JSON 失败: %w", err)
	}
	if err := cat.Validate(); err != nil {
		return nil, err
	}
	return &cat, nil
}

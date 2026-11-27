package bindgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
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

// UnmarshalJSON 解析入参契约时拒绝未知字段，并将缺失 param_index 标记为 -1 以防止零值静默伪装为下标 0。
func (p *FFIParamContract) UnmarshalJSON(data []byte) error {
	type rawParam struct {
		ParamIndex *int             `json:"param_index"`
		ParamName  string           `json:"param_name,omitempty"`
		Ownership  FFIOwnershipKind `json:"ownership"`
		ReleaseFn  string           `json:"release_fn,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var raw rawParam
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	if err := ensureEOF(dec); err != nil {
		return err
	}
	p.ParamName = raw.ParamName
	p.Ownership = raw.Ownership
	p.ReleaseFn = raw.ReleaseFn
	if raw.ParamIndex == nil {
		p.ParamIndex = -1
	} else {
		p.ParamIndex = *raw.ParamIndex
	}
	return nil
}

// FFIReturnContract 描述 C 函数返回值的所有权与析构绑定规则。
type FFIReturnContract struct {
	Ownership    FFIOwnershipKind `json:"ownership"`
	TypeName     string           `json:"type_name,omitempty"`
	ReleaseFn    string           `json:"release_fn,omitempty"`
	BorrowsParam int              `json:"borrows_param"` // >=0 表示返回借用源自入参下标；-1 表示独立或无借用
}

// UnmarshalJSON 解析返回值契约时拒绝未知字段，并将缺失 borrows_param 默认置为 -1，防止 JSON 零值误认为借用第 0 个参数。
func (r *FFIReturnContract) UnmarshalJSON(data []byte) error {
	type rawReturn struct {
		Ownership    FFIOwnershipKind `json:"ownership"`
		TypeName     string           `json:"type_name,omitempty"`
		ReleaseFn    string           `json:"release_fn,omitempty"`
		BorrowsParam *int             `json:"borrows_param"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var raw rawReturn
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	if err := ensureEOF(dec); err != nil {
		return err
	}
	r.Ownership = raw.Ownership
	r.TypeName = raw.TypeName
	r.ReleaseFn = raw.ReleaseFn
	if raw.BorrowsParam == nil {
		r.BorrowsParam = -1
	} else {
		r.BorrowsParam = *raw.BorrowsParam
	}
	return nil
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

// SupportedFFIContractSchemaVersion 定义当前编译器唯一支持的 FFI 所有权契约清单格式版本号。
const SupportedFFIContractSchemaVersion = "1.0"

// FFIContractCatalog 是与 Clang C 头文件元数据正交的旁路 FFI 所有权契约清单模型（供 .wonbind JSON 旁路集成）。
type FFIContractCatalog struct {
	SchemaVersion string                       `json:"schema_version"`
	HeaderPath    string                       `json:"header_path"`
	Namespace     string                       `json:"namespace"`
	OpaqueTypes   map[string]FFIReturnContract `json:"opaque_types,omitempty"`
	Functions     map[string]FFIFuncContract   `json:"functions,omitempty"`
}

// CanonicalizeFFIHeaderPath 对 FFI 契约与 C 绑定元数据中的头文件标识执行纯词法校验与规范化（不访问文件系统或解析符号链接）。
func CanonicalizeFFIHeaderPath(raw string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("FFI 所有权契约缺少 header_path 头文件标识")
	}
	if strings.TrimSpace(raw) != raw {
		return "", fmt.Errorf("FFI 头文件标识 %q 包含前导或尾随空白字符", raw)
	}
	if strings.HasSuffix(raw, "/") {
		return "", fmt.Errorf("FFI 头文件标识 %q 不能以斜杠结尾", raw)
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("FFI 头文件标识 %q 包含非法控制字符", raw)
		}
		switch r {
		case '\\', '<', '>', '"':
			return "", fmt.Errorf("FFI 头文件标识 %q 包含非法字符 %q", raw, r)
		}
	}
	for _, seg := range strings.Split(raw, "/") {
		if seg == ".." {
			return "", fmt.Errorf("FFI 头文件标识 %q 禁止包含父目录跳转段 '..'", raw)
		}
	}
	cleaned := path.Clean(raw)
	if cleaned == "" || cleaned == "." || cleaned == "/" || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("FFI 头文件标识 %q 规范化后无效 (%q)", raw, cleaned)
	}
	return cleaned, nil
}

// ValidateEnvelope 校验 FFI 所有权契约清单的外层信封元数据（schema_version 与 header_path）。
func (c *FFIContractCatalog) ValidateEnvelope() error {
	if c == nil {
		return fmt.Errorf("FFI 所有权契约清单为空")
	}
	if c.SchemaVersion != SupportedFFIContractSchemaVersion {
		if strings.TrimSpace(c.SchemaVersion) == "" {
			return fmt.Errorf("FFI 所有权契约缺少 schema_version（当前仅支持 %q）", SupportedFFIContractSchemaVersion)
		}
		return fmt.Errorf("不支持的 FFI 所有权契约 schema_version %q（当前仅支持 %q）", c.SchemaVersion, SupportedFFIContractSchemaVersion)
	}
	if _, err := CanonicalizeFFIHeaderPath(c.HeaderPath); err != nil {
		return err
	}
	return nil
}

func isValidFFIIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		isLetter := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
		isDigit := ch >= '0' && ch <= '9'
		if i == 0 && !isLetter {
			return false
		}
		if !isLetter && !isDigit {
			return false
		}
	}
	return true
}

func isValidFFISymbolRef(s string) bool {
	if s == "" || strings.TrimSpace(s) != s {
		return false
	}
	if strings.Contains(s, "::") {
		parts := strings.Split(s, "::")
		if len(parts) != 2 {
			return false
		}
		return isValidFFIIdent(parts[0]) && isValidFFIIdent(parts[1])
	}
	return isValidFFIIdent(s)
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("JSON 末尾存在多余数据")
		}
		return err
	}
	return nil
}

// Validate 对外部加载或构建的 FFI 所有权契约执行严格一致性校验，缺失版本/头文件标识、缺失释放函数、未知形态、越界下标或矛盾借用关系按 Fail-Closed 报错。
func (c *FFIContractCatalog) Validate() error {
	if err := c.ValidateEnvelope(); err != nil {
		return err
	}
	if !isValidFFIIdent(c.Namespace) {
		return fmt.Errorf("FFI 所有权契约缺少合法 namespace 声明 (%q)", c.Namespace)
	}

	opaqueKeys := make([]string, 0, len(c.OpaqueTypes))
	for k := range c.OpaqueTypes {
		opaqueKeys = append(opaqueKeys, k)
	}
	sort.Strings(opaqueKeys)
	for _, typeName := range opaqueKeys {
		ot := c.OpaqueTypes[typeName]
		if !isValidFFIIdent(typeName) {
			return fmt.Errorf("不透明类型名 '%s' 非法", typeName)
		}
		if ot.TypeName != "" && ot.TypeName != typeName {
			return fmt.Errorf("不透明类型 '%s' 的 type_name ('%s') 自相矛盾", typeName, ot.TypeName)
		}
		if ot.BorrowsParam != -1 {
			return fmt.Errorf("不透明类型 '%s' 不允许声明 borrows_param=%d (必须为 -1)", typeName, ot.BorrowsParam)
		}
		switch ot.Ownership {
		case FFIOpaqueHandle, FFIOwnedReturn:
			if !isValidFFISymbolRef(ot.ReleaseFn) {
				return fmt.Errorf("不透明资源句柄 '%s' 缺少合法的 release_fn (%q)", typeName, ot.ReleaseFn)
			}
		case FFINonOwningRaw, FFIUnknown:
			if strings.TrimSpace(ot.ReleaseFn) != "" {
				return fmt.Errorf("非拥有型不透明声明 '%s' (%s) 不得指定 release_fn (%q)", typeName, ot.Ownership, ot.ReleaseFn)
			}
		default:
			return fmt.Errorf("不透明类型 '%s' 包含未知或不支持的 ownership 分类 %q", typeName, ot.Ownership)
		}
	}

	fnKeys := make([]string, 0, len(c.Functions))
	for k := range c.Functions {
		fnKeys = append(fnKeys, k)
	}
	sort.Strings(fnKeys)
	for _, fnName := range fnKeys {
		fc := c.Functions[fnName]
		if !isValidFFIIdent(fnName) {
			return fmt.Errorf("FFI 函数名 '%s' 非法", fnName)
		}
		if fc.Namespace != "" && fc.Namespace != c.Namespace {
			return fmt.Errorf("FFI 函数 '%s' 命名空间 '%s' 与目录命名空间 '%s' 矛盾", fnName, fc.Namespace, c.Namespace)
		}
		if fc.Function != "" && fc.Function != fnName {
			return fmt.Errorf("FFI 函数条目标识 '%s' 与 function 字段 '%s' 矛盾", fnName, fc.Function)
		}
		if strings.TrimSpace(fc.ContractID) == "" {
			return fmt.Errorf("FFI 函数 '%s::%s' 缺少 contract_id", c.Namespace, fnName)
		}

		// 1. 校验入参契约列表：合法类别、无重复、连续下标 0..N-1
		seenIdx := make(map[int]FFIParamContract, len(fc.Params))
		for _, p := range fc.Params {
			if p.ParamIndex < 0 {
				return fmt.Errorf("FFI 函数 '%s::%s' 包含非法参数下标 %d", c.Namespace, fnName, p.ParamIndex)
			}
			if _, dup := seenIdx[p.ParamIndex]; dup {
				return fmt.Errorf("FFI 函数 '%s::%s' 参数下标 %d 出现重复契约声明", c.Namespace, fnName, p.ParamIndex)
			}
			switch p.Ownership {
			case FFIOwnedTransfer, FFIBorrowedShared, FFIBorrowedMut, FFINonOwningRaw, FFIUnknown:
			default:
				return fmt.Errorf("FFI 函数 '%s::%s' 参数 #%d 包含未知或不支持的 ownership 分类 %q", c.Namespace, fnName, p.ParamIndex, p.Ownership)
			}
			if fc.IsCertifiedSafe && p.Ownership == FFIUnknown {
				return fmt.Errorf("FFI 函数 '%s::%s' 声明 certified_safe 时禁止存在 unknown 入参 #%d", c.Namespace, fnName, p.ParamIndex)
			}
			if strings.TrimSpace(p.ReleaseFn) != "" {
				if p.Ownership != FFIOwnedTransfer || !isValidFFISymbolRef(p.ReleaseFn) {
					return fmt.Errorf("FFI 函数 '%s::%s' 参数 #%d 带有无效或矛盾的 release_fn (%q)", c.Namespace, fnName, p.ParamIndex, p.ReleaseFn)
				}
			}
			seenIdx[p.ParamIndex] = p
		}
		for i := 0; i < len(fc.Params); i++ {
			if _, ok := seenIdx[i]; !ok {
				return fmt.Errorf("FFI 函数 '%s::%s' 参数契约下标不连续：缺少参数 #%d 元数据", c.Namespace, fnName, i)
			}
		}

		// 2. 校验返回值契约与 BorrowsParam 关联约束
		if fc.Return.BorrowsParam < -1 {
			return fmt.Errorf("FFI 函数 '%s::%s' 包含非法 borrows_param 下标 %d", c.Namespace, fnName, fc.Return.BorrowsParam)
		}
		switch fc.Return.Ownership {
		case FFIOwnedReturn, FFIOpaqueHandle:
			if !isValidFFISymbolRef(fc.Return.ReleaseFn) {
				return fmt.Errorf("FFI 函数 '%s::%s' 声明返回拥有权资源但缺少合法 release_fn (%q)", c.Namespace, fnName, fc.Return.ReleaseFn)
			}
			if fc.Return.BorrowsParam != -1 {
				return fmt.Errorf("FFI 函数 '%s::%s' 返回拥有权资源 (%s) 时不得同时设置 borrows_param=%d", c.Namespace, fnName, fc.Return.Ownership, fc.Return.BorrowsParam)
			}
			if fc.Return.TypeName != "" {
				if ot, exists := c.OpaqueTypes[fc.Return.TypeName]; exists {
					if ot.Ownership != FFIOpaqueHandle && ot.Ownership != FFIOwnedReturn {
						return fmt.Errorf("FFI 函数 '%s::%s' 返回类型 '%s' 与 opaque_types 声明 (%s) 矛盾", c.Namespace, fnName, fc.Return.TypeName, ot.Ownership)
					}
					if ot.ReleaseFn != fc.Return.ReleaseFn {
						return fmt.Errorf("FFI 函数 '%s::%s' 返回 release_fn (%q) 与 opaque_types '%s' (%q) 矛盾", c.Namespace, fnName, fc.Return.ReleaseFn, fc.Return.TypeName, ot.ReleaseFn)
					}
				}
			}

		case FFINonOwningRaw:
			if strings.TrimSpace(fc.Return.ReleaseFn) != "" {
				return fmt.Errorf("FFI 函数 '%s::%s' 返回 raw_unmanaged 时不得指定 release_fn (%q)", c.Namespace, fnName, fc.Return.ReleaseFn)
			}
			if fc.Return.BorrowsParam != -1 {
				return fmt.Errorf("FFI 函数 '%s::%s' 返回 raw_unmanaged 时不支持 borrows_param=%d (须使用 borrow/borrow_mut)", c.Namespace, fnName, fc.Return.BorrowsParam)
			}

		case FFIUnknown:
			if fc.IsCertifiedSafe {
				return fmt.Errorf("FFI 函数 '%s::%s' 标记为 certified_safe 但返回所有权未明确定义", c.Namespace, fnName)
			}
			if strings.TrimSpace(fc.Return.ReleaseFn) != "" {
				return fmt.Errorf("FFI 函数 '%s::%s' 返回 unknown 时不得指定 release_fn (%q)", c.Namespace, fnName, fc.Return.ReleaseFn)
			}
			if fc.Return.BorrowsParam != -1 {
				return fmt.Errorf("FFI 函数 '%s::%s' 返回 unknown 时不得设置 borrows_param=%d", c.Namespace, fnName, fc.Return.BorrowsParam)
			}

		case FFIBorrowedShared, FFIBorrowedMut:
			if strings.TrimSpace(fc.Return.ReleaseFn) != "" {
				return fmt.Errorf("FFI 函数 '%s::%s' 返回借用 (%s) 时不得指定 release_fn (%q)", c.Namespace, fnName, fc.Return.Ownership, fc.Return.ReleaseFn)
			}
			if fc.Return.BorrowsParam < 0 {
				return fmt.Errorf("FFI 函数 '%s::%s' 返回借用 (%s) 时必须指定合法 borrows_param >= 0", c.Namespace, fnName, fc.Return.Ownership)
			}
			srcParam, ok := seenIdx[fc.Return.BorrowsParam]
			if !ok {
				return fmt.Errorf("FFI 函数 '%s::%s' 的 borrows_param=%d 超出参数契约范围 [0, %d)", c.Namespace, fnName, fc.Return.BorrowsParam, len(fc.Params))
			}
			if srcParam.Ownership != FFIBorrowedShared && srcParam.Ownership != FFIBorrowedMut {
				return fmt.Errorf("FFI 函数 '%s::%s' 的 borrows_param=%d 只能借用 @borrow/@borrow(mut) 参数，不能来自 %q", c.Namespace, fnName, fc.Return.BorrowsParam, srcParam.Ownership)
			}
			if fc.Return.Ownership == FFIBorrowedMut && srcParam.Ownership != FFIBorrowedMut {
				return fmt.Errorf("FFI 函数 '%s::%s' 返回可变借用时 borrows_param=%d 必须同样为 borrow_mut", c.Namespace, fnName, fc.Return.BorrowsParam)
			}

		default:
			return fmt.Errorf("FFI 函数 '%s::%s' 返回声明包含未知或不支持的 ownership 分类 %q", c.Namespace, fnName, fc.Return.Ownership)
		}
	}
	return nil
}

// IndexValidatedFFIContracts 校验并汇总多份 FFI 契约目录，按确定性键序检测重复、跨目录头文件不一致或相互矛盾的函数与不透明类型声明。
func IndexValidatedFFIContracts(catalogs []*FFIContractCatalog) (map[string]FFIFuncContract, map[string]FFIReturnContract, error) {
	fnOut := make(map[string]FFIFuncContract)
	opaqueOut := make(map[string]FFIReturnContract)
	nsHeaders := make(map[string]string)

	for _, cat := range catalogs {
		if cat == nil {
			continue
		}
		if err := cat.Validate(); err != nil {
			return nil, nil, err
		}
		canonHeader, err := CanonicalizeFFIHeaderPath(cat.HeaderPath)
		if err != nil {
			return nil, nil, err
		}
		if prevHeader, exists := nsHeaders[cat.Namespace]; exists && prevHeader != canonHeader {
			return nil, nil, fmt.Errorf("命名空间 '%s' 的多个 FFI 契约目录声明了不一致的头文件标识 %q 与 %q", cat.Namespace, prevHeader, canonHeader)
		}
		nsHeaders[cat.Namespace] = canonHeader

		otKeys := make([]string, 0, len(cat.OpaqueTypes))
		for k := range cat.OpaqueTypes {
			otKeys = append(otKeys, k)
		}
		sort.Strings(otKeys)
		for _, typeName := range otKeys {
			ot := cat.OpaqueTypes[typeName]
			if existing, dup := opaqueOut[typeName]; dup {
				if existing.Ownership != ot.Ownership || existing.ReleaseFn != ot.ReleaseFn {
					return nil, nil, fmt.Errorf("不透明类型 '%s' 在多个 FFI 契约目录中出现矛盾声明 (%s/%s vs %s/%s)", typeName, existing.Ownership, existing.ReleaseFn, ot.Ownership, ot.ReleaseFn)
				}
			}
			opaqueOut[typeName] = ot
		}

		fnKeys := make([]string, 0, len(cat.Functions))
		for k := range cat.Functions {
			fnKeys = append(fnKeys, k)
		}
		sort.Strings(fnKeys)
		for _, fnName := range fnKeys {
			fc := cat.Functions[fnName]
			if fc.Namespace == "" {
				fc.Namespace = cat.Namespace
			}
			if fc.Function == "" {
				fc.Function = fnName
			}
			fullName := fc.Namespace + "::" + fc.Function
			if _, dup := fnOut[fullName]; dup {
				return nil, nil, fmt.Errorf("FFI 函数 '%s' 出现重复或冲突契约声明", fullName)
			}
			fnOut[fullName] = fc
		}
	}
	return fnOut, opaqueOut, nil
}

// ValidateBindingContracts 在语义分析阶段交叉验证绑定元数据与 FFI 契约目录的命名空间及 C 函数原型参数个数。
func ValidateBindingContracts(bindings map[string]*BindingMeta) error {
	if len(bindings) == 0 {
		return nil
	}
	nsKeys := make([]string, 0, len(bindings))
	for ns := range bindings {
		nsKeys = append(nsKeys, ns)
	}
	sort.Strings(nsKeys)

	var catalogs []*FFIContractCatalog
	for _, ns := range nsKeys {
		bm := bindings[ns]
		if bm == nil || bm.Contracts == nil {
			continue
		}
		if err := bm.Contracts.Validate(); err != nil {
			return err
		}
		if bm.Contracts.Namespace != ns {
			return fmt.Errorf("命名空间 '%s' 绑定的 FFI 契约声明了不匹配的 namespace '%s'", ns, bm.Contracts.Namespace)
		}
		canonCatalogHeader, err := CanonicalizeFFIHeaderPath(bm.Contracts.HeaderPath)
		if err != nil {
			return err
		}
		canonBindingHeader, err := CanonicalizeFFIHeaderPath(bm.HeaderPath)
		if err != nil {
			return fmt.Errorf("命名空间 '%s' 的 C 绑定头文件标识非法: %w", ns, err)
		}
		if canonCatalogHeader != canonBindingHeader {
			return fmt.Errorf("命名空间 '%s' 的 FFI 契约头文件标识 %q 与绑定头文件 %q 不一致", ns, canonCatalogHeader, canonBindingHeader)
		}
		for fnName, fc := range bm.Contracts.Functions {
			if fnSig, exists := bm.Functions[fnName]; exists {
				if len(fc.Params) != len(fnSig.ParamTypes) {
					return fmt.Errorf("FFI 契约 '%s::%s' 参数个数 (%d) 与 C 头文件签名参数个数 (%d) 不一致", ns, fnName, len(fc.Params), len(fnSig.ParamTypes))
				}
				if fc.Return.BorrowsParam >= len(fnSig.ParamTypes) {
					return fmt.Errorf("FFI 契约 '%s::%s' 的 borrows_param=%d 超出 C 头文件参数个数 (%d)", ns, fnName, fc.Return.BorrowsParam, len(fnSig.ParamTypes))
				}
			}
		}
		catalogs = append(catalogs, bm.Contracts)
	}
	_, _, err := IndexValidatedFFIContracts(catalogs)
	return err
}

// ParseFFIContractJSON 解析并严格校验旁路 FFI 所有权契约 JSON（供工具链与未来 .wonbind 编译器旁路使用）。
func ParseFFIContractJSON(raw []byte) (*FFIContractCatalog, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("FFI 所有权契约 JSON 为空")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var cat FFIContractCatalog
	if err := dec.Decode(&cat); err != nil {
		return nil, fmt.Errorf("解析 FFI 所有权契约 JSON 失败: %w", err)
	}
	if err := ensureEOF(dec); err != nil {
		return nil, fmt.Errorf("解析 FFI 所有权契约 JSON 失败: %w", err)
	}
	if err := cat.Validate(); err != nil {
		return nil, err
	}
	return &cat, nil
}

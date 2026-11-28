// Package c11 实现 WON 编译器 Phase 2C.1 的纯 TIR 驱动严格 ISO C11 后端。
// 该包严格仅依赖 Go 标准库与 internal/tir，绝不访问 AST、符号表或前端类型上下文 (ADR-009)。
package c11

import "fmt"

// Target 描述 C11 后端面向的目标主机 ABI 参数。
type Target struct {
	OS          string
	Arch        string
	PointerBits int
	LongBits    int
}

// DefaultLinuxX86_64 返回 Phase 2C.1 认证的 Linux x86_64 LP64 目标配置。
func DefaultLinuxX86_64() Target {
	return Target{
		OS:          "linux",
		Arch:        "x86_64",
		PointerBits: 64,
		LongBits:    64,
	}
}

// Validate 对目标 ABI 参数执行 Fail-Closed 校验，拒绝未验证的非 LP64 平台配置。
func (t Target) Validate() error {
	if t.OS == "" {
		t.OS = "linux"
	}
	if t.Arch == "" {
		t.Arch = "x86_64"
	}
	if t.PointerBits == 0 {
		t.PointerBits = 64
	}
	if t.LongBits == 0 {
		t.LongBits = 64
	}
	if t.OS != "linux" || t.Arch != "x86_64" || t.PointerBits != 64 || t.LongBits != 64 {
		return &CapabilityError{
			Code:        "WON-GEN-002",
			Message:     fmt.Sprintf("当前 C11 后端仅支持 linux/x86_64 LP64 目标，暂不支持 %s/%s (%d/%d)", t.OS, t.Arch, t.PointerBits, t.LongBits),
			Explanation: "跨平台或非 64 位 LP64 ABI 目标需要独立验证数据模型后方可开放。",
		}
	}
	return nil
}

// PtrSizeBytes 返回目标平台指针字节宽度（LP64 下恒为 8）。
func (t Target) PtrSizeBytes() uint64 {
	if t.PointerBits <= 0 {
		return 8
	}
	return uint64(t.PointerBits / 8)
}

// MaxObjectSizeBytes 返回目标 ISO C11 ABI 允许的最大单个对象尺寸 (PTRDIFF_MAX)。
func (t Target) MaxObjectSizeBytes() uint64 {
	return ^uint64(0) >> 1
}

// CapabilityError 表示纯 TIR C11 后端能力网关或验证阶段检出的结构化 Fail-Closed 错误。
type CapabilityError struct {
	Code    string
	Message string
	Span    [2]int // 预留兼容位（通常消费 TIRSourceSpan）
	TIRSpan struct {
		File      string
		StartLine int
		StartCol  int
		StartByte int
		EndLine   int
		EndCol    int
		EndByte   int
	}
	Explanation string
}

// Error 实现 Go error 接口。
func (e *CapabilityError) Error() string {
	if e == nil {
		return ""
	}
	if e.TIRSpan.File != "" && e.TIRSpan.StartLine > 0 {
		return fmt.Sprintf("%s (%s:%d:%d): %s", e.Code, e.TIRSpan.File, e.TIRSpan.StartLine, e.TIRSpan.StartCol, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

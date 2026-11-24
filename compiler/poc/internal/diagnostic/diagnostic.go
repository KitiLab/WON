// Package diagnostic 实现了编译器统一的结构化诊断报告系统
// 遵循 WON 架构规范，支持人机可读终端输出与标准 JSON 序列化，供 AI Coding Agent 自动化修复。
package diagnostic

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Position 表示源码中的精确物理位置
type Position struct {
	Line   int `json:"line"`   // 行号（从 1 开始）
	Column int `json:"column"` // 列号（从 1 开始）
	Byte   int `json:"byte"`   // 字节偏移量（从 0 开始）
}

// Span 表示源码中的闭合区间范围
type Span struct {
	File  string   `json:"file"`
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Edit 表示机器可自动应用的文本替换修改
type Edit struct {
	Start       int    `json:"start"`       // 起始字节偏移
	End         int    `json:"end"`         // 结束字节偏移
	Replacement string `json:"replacement"` // 替换文本内容
}

// Fix 表示单项建议修复方案
type Fix struct {
	Description string `json:"description"` // 修复说明
	Edits       []Edit `json:"edits"`       // 具体修改切片
}

// Diagnostic 表示单条编译器诊断（错误/警告/提示）
type Diagnostic struct {
	Code        string `json:"code"`                  // 唯一错误码，如 WON-MUT-001
	Severity    string `json:"severity"`              // "error" | "warning" | "info"
	Message     string `json:"message"`               // 简要错误描述
	File        string `json:"file"`                  // 所属源文件名
	Span        Span   `json:"span"`                  // 关联源码位置
	Explanation string `json:"explanation,omitempty"` // 根因解释说明
	Fixes       []Fix  `json:"fixes,omitempty"`       // 机器可应用的修复列表
}

// Report 是对外的顶层诊断报告容器
type Report struct {
	Version     int          `json:"version"`
	Status      string       `json:"status"` // "ok" | "error"
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// NewReport 创建新的诊断报告
func NewReport() *Report {
	return &Report{
		Version:     1,
		Status:      "ok",
		Diagnostics: make([]Diagnostic, 0),
	}
}

// AddDiagnostic 添加诊断信息，若包含 error 则自动将 status 置为 error
func (r *Report) AddDiagnostic(d Diagnostic) {
	if d.Severity == "error" {
		r.Status = "error"
	}
	r.Diagnostics = append(r.Diagnostics, d)
}

// HasErrors 检查是否存在错误级别的诊断
func (r *Report) HasErrors() bool {
	for _, d := range r.Diagnostics {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}

// ToJSON 将报告序列化为格式化 JSON 字符串
func (r *Report) ToJSON() (string, error) {
	bytes, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// String 输出适合终端阅读的纯文本格式
func (r *Report) String() string {
	if len(r.Diagnostics) == 0 {
		return "No diagnostics reported."
	}
	var sb strings.Builder
	for _, d := range r.Diagnostics {
		sb.WriteString(fmt.Sprintf("[%s] %s: %s:%d:%d: %s\n",
			strings.ToUpper(d.Severity), d.Code, d.File, d.Span.Start.Line, d.Span.Start.Column, d.Message))
		if d.Explanation != "" {
			sb.WriteString(fmt.Sprintf("  Explanation: %s\n", d.Explanation))
		}
		for i, fix := range d.Fixes {
			sb.WriteString(fmt.Sprintf("  Suggestion %d: %s\n", i+1, fix.Description))
		}
	}
	return sb.String()
}

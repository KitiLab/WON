package c11

import (
	"fmt"

	"github.com/KitiLab/WON/compiler/poc/internal/tir"
)

// TypePos 标识 C11 类型映射所处的语法上下文（严格区分布局、参数、返回值与表达式值位）。
type TypePos int

const (
	PosLocal TypePos = iota
	PosParam
	PosReturn
	PosValue
)

// IsScalarOrChar 判断给定 TIR 类型描述符是否为当前 C11 后端支持直接值映射的标量或 char 基元类型。
func IsScalarOrChar(info tir.TypeInfo) bool {
	switch info.Kind {
	case tir.TypeBool, tir.TypeChar, tir.TypeFloat:
		return true
	case tir.TypeInt:
		return info.Bits != 128
	default:
		return false
	}
}

// MapType 执行 Fail-Closed 的 tir.TypeID -> Linux x86_64 LP64 ISO C11 类型字符串映射。
// 若类型尚未受当前 C11 后端支持，返回明确错误，严禁透传 WON 名称或静默回退到 void。
func MapType(tt *tir.TypeTable, tid tir.TypeID, pos TypePos) (string, error) {
	if tt == nil || !tt.Valid(tid) {
		return "", fmt.Errorf("未解析的无效 TIR 类型 (t%d) 无法映射为 ISO C11 类型", tid)
	}

	info := tt.Info(tid)
	switch info.Kind {
	case tir.TypeUnit:
		if pos == PosReturn {
			return "void", nil
		}
		return "", fmt.Errorf("WON 类型 'unit' 仅允许出现在函数返回值位置，不能作为 C11 对象类型 (pos=%d)", pos)

	case tir.TypeBool:
		return "bool", nil
	case tir.TypeChar:
		return "uint32_t", nil

	case tir.TypeInt:
		if info.IsPtrSized {
			if info.Signed {
				return "ptrdiff_t", nil
			}
			return "size_t", nil
		}
		switch info.Bits {
		case 8:
			if info.Signed {
				return "int8_t", nil
			}
			return "uint8_t", nil
		case 16:
			if info.Signed {
				return "int16_t", nil
			}
			return "uint16_t", nil
		case 32:
			if info.Signed {
				return "int32_t", nil
			}
			return "uint32_t", nil
		case 64:
			if info.Signed {
				return "int64_t", nil
			}
			return "uint64_t", nil
		default:
			return "", fmt.Errorf("%d 位整数类型 '%s' 在严格 ISO C11 中缺乏可移植原生支持", info.Bits, tt.Format(tid))
		}

	case tir.TypeFloat:
		switch info.Bits {
		case 32:
			return "float", nil
		case 64:
			return "double", nil
		default:
			return "", fmt.Errorf("不支持的浮点位宽 f%d", info.Bits)
		}

	case tir.TypeRef:
		elemInfo := tt.Info(info.Elem)
		if elemInfo.Kind == tir.TypeStr && !info.IsMut {
			return "const char*", nil
		}
		isOpaqueRef := elemInfo.Kind == tir.TypeStruct && elemInfo.IsOpaqueForeign && elemInfo.ForeignNamespace != "" && isValidCIdentifier(elemInfo.Name)
		if !IsScalarOrChar(elemInfo) && !isOpaqueRef {
			return "", fmt.Errorf("引用类型 '%s' 的目标类型尚不支持降低到当前 C11 后端", tt.Format(tid))
		}
		elemC, err := MapType(tt, info.Elem, PosValue)
		if err != nil {
			return "", err
		}
		if info.IsMut {
			return elemC + "*", nil
		}
		return "const " + elemC + "*", nil

	case tir.TypeRawPtr:
		elemInfo := tt.Info(info.Elem)
		if elemInfo.Kind == tir.TypeUnit {
			if info.IsMut {
				return "void*", nil
			}
			return "const void*", nil
		}
		if elemInfo.Kind == tir.TypeStruct && elemInfo.IsOpaqueForeign && elemInfo.Name != "" {
			if info.IsMut {
				return elemInfo.Name + "*", nil
			}
			return "const " + elemInfo.Name + "*", nil
		}
		if !IsScalarOrChar(elemInfo) {
			return "", fmt.Errorf("原生指针 '%s' 指向的目标类型尚不支持当前 C11 后端", tt.Format(tid))
		}
		elemC, err := MapType(tt, info.Elem, PosValue)
		if err != nil {
			return "", err
		}
		if info.IsMut {
			return elemC + "*", nil
		}
		return "const " + elemC + "*", nil

	case tir.TypeArray:
		return "", fmt.Errorf("定长数组类型 '%s' 不能按值作为 C 参数、返回值或标量值", tt.Format(tid))

	case tir.TypeStruct:
		// 仅允许通过 FFI 契约登记并绑定了有效外部命名空间的受管不透明外部资源句柄按其 C 声明类型名降低
		if info.IsOpaqueForeign && info.ForeignNamespace != "" && isValidCIdentifier(info.Name) {
			return info.Name, nil
		}
		return "", fmt.Errorf("结构体类型 '%s' 尚未实现 C11 布局与代码生成", tt.Format(tid))

	default:
		return "", fmt.Errorf("WON 类型 '%s' 尚未实现 ISO C11 后端类型映射", tt.Format(tid))
	}
}

func isValidCIdentifier(s string) bool {
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

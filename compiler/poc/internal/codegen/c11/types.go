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
		if elemInfo.Kind == tir.TypeStr {
			if info.IsMut {
				return "", fmt.Errorf("可变字符串视图 '&mut str' 尚未支持 C11 代码生成")
			}
			return "_won_str_t", nil
		}
		if elemInfo.Kind == tir.TypeUnit {
			if info.IsMut {
				return "void*", nil
			}
			return "const void*", nil
		}
		isOpaqueRef := elemInfo.Kind == tir.TypeStruct && elemInfo.IsOpaqueForeign && elemInfo.ForeignNamespace != "" && isValidCIdentifier(elemInfo.Name)
		if isOpaqueRef {
			if info.IsMut {
				return elemInfo.Name + "*", nil
			}
			return "const " + elemInfo.Name + "*", nil
		}
		elemC, err := MapType(tt, info.Elem, PosValue)
		if err != nil {
			return "", fmt.Errorf("引用类型 '%s' 的目标类型尚不支持降低到当前 C11 后端: %w", tt.Format(tid), err)
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
		elemC, err := MapType(tt, info.Elem, PosValue)
		if err != nil {
			return "", fmt.Errorf("原生指针 '%s' 指向的目标类型尚不支持当前 C11 后端: %w", tt.Format(tid), err)
		}
		if info.IsMut {
			return elemC + "*", nil
		}
		return "const " + elemC + "*", nil

	case tir.TypeArray, tir.TypeTuple, tir.TypeEnum:
		if hasUnsupportedNativeFeature(tt, tid, make(map[tir.TypeID]bool, 8)) {
			return "", fmt.Errorf("聚合类型 '%s' 包含当前 C11 后端尚未支持的元素或负载类型（如 String / &[T] / i128）", tt.Format(tid))
		}
		tl, err := NewTargetLayout(DefaultLinuxX86_64(), tt)
		if err != nil {
			return "", err
		}
		lay, err := tl.LayoutOf(tid)
		if err != nil {
			return "", err
		}
		return lay.CName, nil

	case tir.TypeStruct:
		// 仅允许通过 FFI 契约登记并绑定了有效外部命名空间的受管不透明外部资源句柄按其 C 声明类型名降低
		if info.IsOpaqueForeign {
			if info.ForeignNamespace != "" && isValidCIdentifier(info.Name) {
				return info.Name, nil
			}
			return "", fmt.Errorf("外部不透明结构体类型 '%s' 缺少合法命名空间或有效 C 标识符", tt.Format(tid))
		}
		if hasUnsupportedNativeFeature(tt, tid, make(map[tir.TypeID]bool, 8)) {
			return "", fmt.Errorf("结构体类型 '%s' 包含当前 C11 后端尚未支持的成员类型（如 String / &[T] / i128）", tt.Format(tid))
		}
		tl, err := NewTargetLayout(DefaultLinuxX86_64(), tt)
		if err != nil {
			return "", err
		}
		lay, err := tl.LayoutOf(tid)
		if err != nil {
			return "", err
		}
		return lay.CName, nil

	default:
		return "", fmt.Errorf("WON 类型 '%s' 尚未实现 ISO C11 后端类型映射", tt.Format(tid))
	}
}

// hasUnsupportedNativeFeature 递归检查类型是否传递包含当前阶段禁用的拥有型 String、&mut str 或非受控切片。
func hasUnsupportedNativeFeature(tt *tir.TypeTable, tid tir.TypeID, seen map[tir.TypeID]bool) bool {
	if tt == nil || !tt.Valid(tid) || seen[tid] {
		return false
	}
	seen[tid] = true
	defer delete(seen, tid)
	info := tt.Info(tid)
	switch info.Kind {
	case tir.TypeString, tir.TypeStr, tir.TypeSlice, tir.TypeTypeParam, tir.TypeNever:
		return true
	case tir.TypeInt:
		return !info.IsPtrSized && info.Bits != 8 && info.Bits != 16 && info.Bits != 32 && info.Bits != 64
	case tir.TypeFloat:
		return info.Bits != 32 && info.Bits != 64
	case tir.TypeRef:
		ei := tt.Info(info.Elem)
		if ei.Kind == tir.TypeStr {
			return info.IsMut
		}
		if ei.Kind == tir.TypeSlice {
			return true
		}
		return false
	case tir.TypeRawPtr:
		return false
	case tir.TypeArray:
		return hasUnsupportedNativeFeature(tt, info.Elem, seen)
	case tir.TypeTuple:
		for _, el := range info.Elems {
			if hasUnsupportedNativeFeature(tt, el, seen) {
				return true
			}
		}
	case tir.TypeStruct:
		if info.IsOpaqueForeign {
			return false
		}
		for _, f := range info.Fields {
			if hasUnsupportedNativeFeature(tt, f.Type, seen) {
				return true
			}
		}
	case tir.TypeEnum:
		for _, v := range info.Variants {
			for _, te := range v.TupleElems {
				if hasUnsupportedNativeFeature(tt, te, seen) {
					return true
				}
			}
			for _, sf := range v.StructFields {
				if hasUnsupportedNativeFeature(tt, sf.Type, seen) {
					return true
				}
			}
		}
	}
	return false
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

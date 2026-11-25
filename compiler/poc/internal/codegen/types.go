package codegen

import (
	"fmt"

	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// TypePos 标识 C11 类型映射所处的语法位置（函数参数、返回值、局部变量或数组元素不可混用 void）
type TypePos int

const (
	PosLocal TypePos = iota
	PosParam
	PosReturn
	PosValue
)

// MapTypeToC 执行显式 fail-closed 的 WON TypeID -> Linux x86_64 LP64 ISO C11 类型映射。
// 若目标类型或上下文位置不受当前 C11 后端支持，必须返回明确 error，严禁透传 WON 类型名或静默降级为 void。
func MapTypeToC(tctx *types.Context, tid types.TypeID, pos TypePos) (string, error) {
	if tctx == nil || tid == types.Invalid {
		return "", fmt.Errorf("未解析的无效类型 (TypeID=%d) 无法映射为 ISO C11 类型", tid)
	}

	info := tctx.Info(tid)
	switch info.Kind {
	case types.KindUnit:
		if pos == PosReturn {
			return "void", nil
		}
		return "", fmt.Errorf("WON 类型 'unit' 仅允许出现在函数返回值位置，不能作为 C11 对象类型 (pos=%d)", pos)

	case types.KindBool:
		return "bool", nil
	case types.KindChar:
		return "uint32_t", nil
	case types.KindI8:
		return "int8_t", nil
	case types.KindI16:
		return "int16_t", nil
	case types.KindI32:
		return "int32_t", nil
	case types.KindI64:
		return "int64_t", nil
	case types.KindISize:
		return "ptrdiff_t", nil
	case types.KindU8:
		return "uint8_t", nil
	case types.KindU16:
		return "uint16_t", nil
	case types.KindU32:
		return "uint32_t", nil
	case types.KindU64:
		return "uint64_t", nil
	case types.KindUSize:
		return "size_t", nil
	case types.KindF32:
		return "float", nil
	case types.KindF64:
		return "double", nil

	case types.KindI128, types.KindU128:
		return "", fmt.Errorf("128 位整数类型 '%s' 在严格 ISO C11 中缺乏可移植原生支持", tctx.Format(tid))

	case types.KindRef:
		if tid == tctx.StrView {
			return "const char*", nil
		}
		elemInfo := tctx.Info(info.Elem)
		if !IsC11ScalarOrChar(elemInfo.Kind) {
			return "", fmt.Errorf("引用类型 '%s' 的目标类型尚不支持降低到当前 C11 后端", tctx.Format(tid))
		}
		elemC, err := MapTypeToC(tctx, info.Elem, PosValue)
		if err != nil {
			return "", err
		}
		if info.IsMut {
			return elemC + "*", nil
		}
		return "const " + elemC + "*", nil

	case types.KindRawPtr:
		elemInfo := tctx.Info(info.Elem)
		if elemInfo.Kind == types.KindUnit {
			if info.IsMut {
				return "void*", nil
			}
			return "const void*", nil
		}
		if !IsC11ScalarOrChar(elemInfo.Kind) {
			return "", fmt.Errorf("原生指针 '%s' 指向的目标类型尚不支持当前 C11 后端", tctx.Format(tid))
		}
		elemC, err := MapTypeToC(tctx, info.Elem, PosValue)
		if err != nil {
			return "", err
		}
		if info.IsMut {
			return elemC + "*", nil
		}
		return "const " + elemC + "*", nil

	case types.KindArray:
		// 定长数组仅允许局部栈缓冲区，不能按值作为 C 参数或 C 返回值
		return "", fmt.Errorf("定长数组类型 '%s' 不能按值作为 C 参数或返回值", tctx.Format(tid))

	default:
		return "", fmt.Errorf("WON 类型 '%s' 尚未实现 ISO C11 后端类型映射", tctx.Format(tid))
	}
}

// IsC11ScalarOrChar 判断类型分类是否属于当前 C11 后端已支持直接值表示的标量/编码基元类型
func IsC11ScalarOrChar(k types.Kind) bool {
	switch k {
	case types.KindBool, types.KindChar,
		types.KindI8, types.KindI16, types.KindI32, types.KindI64, types.KindISize,
		types.KindU8, types.KindU16, types.KindU32, types.KindU64, types.KindUSize,
		types.KindF32, types.KindF64:
		return true
	default:
		return false
	}
}

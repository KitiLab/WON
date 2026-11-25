package types

import (
	"math"
	"math/big"
)

// TargetPointerBits 表示当前目标平台的指针字长 (Linux/x86_64 LP64 为 64 位)
const TargetPointerBits = 64

var (
	bigZero = big.NewInt(0)
	bigOne  = big.NewInt(1)

	minI8    = big.NewInt(math.MinInt8)
	maxI8    = big.NewInt(math.MaxInt8)
	minI16   = big.NewInt(math.MinInt16)
	maxI16   = big.NewInt(math.MaxInt16)
	minI32   = big.NewInt(math.MinInt32)
	maxI32   = big.NewInt(math.MaxInt32)
	minI64   = big.NewInt(math.MinInt64)
	maxI64   = big.NewInt(math.MaxInt64)
	minI128  = signedMinBound(128)
	maxI128  = signedMaxBound(128)
	minISize = signedMinBound(TargetPointerBits)
	maxISize = signedMaxBound(TargetPointerBits)

	maxU8    = big.NewInt(math.MaxUint8)
	maxU16   = big.NewInt(math.MaxUint16)
	maxU32   = big.NewInt(math.MaxUint32)
	maxU64   = unsignedMaxBound(64)
	maxU128  = unsignedMaxBound(128)
	maxUSize = unsignedMaxBound(TargetPointerBits)
)

func signedMinBound(bits uint) *big.Int {
	// -2^(bits-1)
	v := new(big.Int).Lsh(bigOne, bits-1)
	return v.Neg(v)
}

func signedMaxBound(bits uint) *big.Int {
	// 2^(bits-1) - 1
	v := new(big.Int).Lsh(bigOne, bits-1)
	return v.Sub(v, bigOne)
}

func unsignedMaxBound(bits uint) *big.Int {
	// 2^bits - 1
	v := new(big.Int).Lsh(bigOne, bits)
	return v.Sub(v, bigOne)
}

// Equal 通过唯一驻留 ID 执行 O(1) 类型严格相等判定（不依赖展示字符串）
func (c *Context) Equal(a, b TypeID) bool {
	return a == b
}

// IsError 检查类型是否为错恢复占位符 Invalid
func (c *Context) IsError(id TypeID) bool {
	return id == Invalid
}

// IsInteger 检查类型是否为有符号或无符号定长整数类型
func (c *Context) IsInteger(id TypeID) bool {
	k := c.Info(id).Kind
	return (k >= KindI8 && k <= KindISize) || (k >= KindU8 && k <= KindUSize)
}

// IsSigned 检查是否为有符号整数类型
func (c *Context) IsSigned(id TypeID) bool {
	k := c.Info(id).Kind
	return k >= KindI8 && k <= KindISize
}

// IsUnsigned 检查是否为无符号整数类型
func (c *Context) IsUnsigned(id TypeID) bool {
	k := c.Info(id).Kind
	return k >= KindU8 && k <= KindUSize
}

// IsFloat 检查是否为 IEEE 754 浮点类型
func (c *Context) IsFloat(id TypeID) bool {
	k := c.Info(id).Kind
	return k == KindF32 || k == KindF64
}

// IsNumeric 检查是否为整数或浮点数值类型
func (c *Context) IsNumeric(id TypeID) bool {
	return c.IsInteger(id) || c.IsFloat(id)
}

// IsSized 检查类型是否具有编译期已知固定内存尺寸（区分 str 与 [T] 等无尺寸 DST）
func (c *Context) IsSized(id TypeID) bool {
	info := c.Info(id)
	switch info.Kind {
	case KindStr, KindSlice:
		return false
	case KindTuple:
		for _, e := range info.Elems {
			if !c.IsSized(e) {
				return false
			}
		}
		return true
	case KindArray:
		return c.IsSized(info.Elem)
	default:
		return true
	}
}

// IsCopy 判断类型是否天然具备按位浅拷贝语义
func (c *Context) IsCopy(id TypeID) bool {
	info := c.Info(id)
	switch info.Kind {
	case KindUnit, KindBool, KindChar, KindRawPtr, KindFn, KindNever:
		return true
	case KindI8, KindI16, KindI32, KindI64, KindI128, KindISize,
		KindU8, KindU16, KindU32, KindU64, KindU128, KindUSize,
		KindF32, KindF64:
		return true
	case KindRef:
		// 仅不可变引用 &T 具备 Copy 语义，&mut T 具有独占语义不可 Copy
		return !info.IsMut
	case KindArray:
		return c.IsCopy(info.Elem)
	case KindTuple:
		for _, e := range info.Elems {
			if !c.IsCopy(e) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// FitsBigIntLiteral 使用精确任意精度边界判断整数字面量是否能安全表示在目标整数类型内
// 注意：这是规范上下文推断 (Contextual Literal Typing)，而非通用隐式数值转换。
func (c *Context) FitsBigIntLiteral(val *big.Int, target TypeID) bool {
	if val == nil {
		return false
	}
	inSigned := func(min, max *big.Int) bool {
		return val.Cmp(min) >= 0 && val.Cmp(max) <= 0
	}
	inUnsigned := func(max *big.Int) bool {
		return val.Cmp(bigZero) >= 0 && val.Cmp(max) <= 0
	}

	switch c.Info(target).Kind {
	case KindI8:
		return inSigned(minI8, maxI8)
	case KindI16:
		return inSigned(minI16, maxI16)
	case KindI32:
		return inSigned(minI32, maxI32)
	case KindI64:
		return inSigned(minI64, maxI64)
	case KindI128:
		return inSigned(minI128, maxI128)
	case KindISize:
		return inSigned(minISize, maxISize)
	case KindU8:
		return inUnsigned(maxU8)
	case KindU16:
		return inUnsigned(maxU16)
	case KindU32:
		return inUnsigned(maxU32)
	case KindU64:
		return inUnsigned(maxU64)
	case KindU128:
		return inUnsigned(maxU128)
	case KindUSize:
		return inUnsigned(maxUSize)
	default:
		return false
	}
}

// FitsIntegerLiteral 验证 int64 值是否落在目标整数类型的精确取值区间内
func (c *Context) FitsIntegerLiteral(val int64, target TypeID) bool {
	return c.FitsBigIntLiteral(big.NewInt(val), target)
}

// CanAssign 检查类型 from 是否可以直接赋值给期望类型 to
// 严格禁止整数宽度隐式扩展/截断及有符号与无符号之间的静默转换。
func (c *Context) CanAssign(from, to TypeID) bool {
	if from == Invalid || to == Invalid {
		return true // 错误恢复防级联传播
	}
	if from == to {
		return true
	}
	if c.Info(from).Kind == KindNever {
		return true
	}

	fi := c.Info(from)
	ti := c.Info(to)

	// 可变引用 &mut T 允许在只读上下文中隐式降级为 &T (前提是内部类型 T 完全一致)
	if fi.Kind == KindRef && ti.Kind == KindRef {
		if fi.IsMut && !ti.IsMut {
			return fi.Elem == ti.Elem
		}
		// 特殊规则：&mut String 可在借用参数处视为 &String，但 &mut str 与 &str 需显式一致
		return fi.IsMut == ti.IsMut && fi.Elem == ti.Elem
	}

	// 原生指针协变降级：*mut T 可传递给期望 *const T 的只读 C ABI 参数
	if fi.Kind == KindRawPtr && ti.Kind == KindRawPtr {
		if fi.IsMut && !ti.IsMut {
			return fi.Elem == ti.Elem
		}
		return fi.IsMut == ti.IsMut && fi.Elem == ti.Elem
	}

	// 元组逐元素递归校验
	if fi.Kind == KindTuple && ti.Kind == KindTuple && len(fi.Elems) == len(ti.Elems) {
		for i := range fi.Elems {
			if !c.CanAssign(fi.Elems[i], ti.Elems[i]) {
				return false
			}
		}
		return true
	}

	return false
}

// CanCast 判断是否允许通过显式 `expr as target` 执行类型转换
func (c *Context) CanCast(from, to TypeID) bool {
	if from == Invalid || to == Invalid {
		return true
	}
	if from == to {
		return true
	}
	// 数值类型间显式互转（如 i32 as i64, f64 as u64, usize as u64）
	if c.IsNumeric(from) && c.IsNumeric(to) {
		return true
	}
	// char 与整数之间互转（如 '0' as u32）
	if c.Info(from).Kind == KindChar && c.IsInteger(to) {
		return true
	}
	if c.IsInteger(from) && (c.Info(to).Kind == KindU8 || c.Info(to).Kind == KindChar) {
		return true
	}
	// bool 转整数
	if c.Info(from).Kind == KindBool && c.IsInteger(to) {
		return true
	}
	// 原生指针之间或原生指针与 usize 之间显式转换
	if c.Info(from).Kind == KindRawPtr && (c.Info(to).Kind == KindRawPtr || to == c.USize || to == c.ISize) {
		return true
	}
	if (from == c.USize || from == c.ISize) && c.Info(to).Kind == KindRawPtr {
		return true
	}
	// 引用转原生指针（在需要 FFI 互操作时）
	if c.Info(from).Kind == KindRef && c.Info(to).Kind == KindRawPtr && c.Info(from).Elem == c.Info(to).Elem {
		return !c.Info(to).IsMut || c.Info(from).IsMut
	}
	return false
}

// Substitute 对给定类型内部的所有泛型形参执行结构化类型替换（用于 Option[T] / Result[T, E] 与泛型结构体/枚举实例化）
func (c *Context) Substitute(id TypeID, subst map[uint64]TypeID) TypeID {
	if len(subst) == 0 || id == Invalid {
		return id
	}
	info := c.Info(id)
	switch info.Kind {
	case KindTypeParam:
		if rep, ok := subst[info.SymbolID]; ok {
			return rep
		}
		return id
	case KindRef:
		return c.Ref(info.IsMut, c.Substitute(info.Elem, subst))
	case KindRawPtr:
		return c.RawPtr(info.IsMut, c.Substitute(info.Elem, subst))
	case KindArray:
		return c.Array(c.Substitute(info.Elem, subst), info.Len)
	case KindSlice:
		return c.Slice(c.Substitute(info.Elem, subst))
	case KindTuple:
		elems := make([]TypeID, len(info.Elems))
		for i, e := range info.Elems {
			elems[i] = c.Substitute(e, subst)
		}
		return c.Tuple(elems)
	case KindFn:
		params := make([]TypeID, len(info.Params))
		for i, p := range info.Params {
			params[i] = c.Substitute(p, subst)
		}
		return c.Fn(params, c.Substitute(info.Return, subst))
	case KindStruct:
		if len(info.GenericArgs) == 0 {
			return id
		}
		args := make([]TypeID, len(info.GenericArgs))
		for i, a := range info.GenericArgs {
			args[i] = c.Substitute(a, subst)
		}
		return c.Struct(info.SymbolID, info.Name, args)
	case KindEnum:
		if len(info.GenericArgs) == 0 {
			return id
		}
		args := make([]TypeID, len(info.GenericArgs))
		for i, a := range info.GenericArgs {
			args[i] = c.Substitute(a, subst)
		}
		return c.Enum(info.SymbolID, info.Name, args)
	default:
		return id
	}
}

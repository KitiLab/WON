// Package types 实现了 WON 编译器的规范类型系统与结构化类型驻留池 (Type Interning)
// 每个唯一结构类型对应全局唯一的 TypeID，彻底摆脱对字符串拼接判等的依赖。
package types

import (
	"fmt"
	"strings"
)

// TypeID 表示编译器会话中类型的唯一整数标识
type TypeID uint32

const (
	Invalid TypeID = 0
)

// Kind 表示规范类型的结构分类
type Kind int

const (
	KindInvalid Kind = iota
	KindNever        // 发散控制流底类型 (return / break / continue)
	KindUnit         // () / unit
	KindBool
	KindChar
	KindI8
	KindI16
	KindI32
	KindI64
	KindI128
	KindISize
	KindU8
	KindU16
	KindU32
	KindU64
	KindU128
	KindUSize
	KindF32
	KindF64
	KindStr       // 无尺寸 UTF-8 字符串切片基元类型 (DST)
	KindString    // 堆分配所有权可变字符串 String
	KindRef       // &T 或 &mut T
	KindRawPtr    // *const T 或 *mut T
	KindArray     // [T; N]
	KindSlice     // [T] (DST)
	KindTuple     // (T1, T2, ...)
	KindFn        // fn(P1, ...) -> R
	KindStruct    // 具名结构体及其泛型实参
	KindEnum      // 具名枚举及其泛型实参（含 Option[T], Result[T, E]）
	KindTypeParam // 泛型形参占位类型
)

// TypeInfo 保存规范类型的不可变结构描述符
type TypeInfo struct {
	ID          TypeID
	Kind        Kind
	Name        string   // 具名类型名称
	SymbolID    uint64   // 关联的符号表唯一 ID（用于结构体、枚举、泛型形参）
	IsMut       bool     // Ref / RawPtr 是否可变
	Elem        TypeID   // Ref / RawPtr / Array / Slice 的内部元素类型
	Len         int64    // Array 的定长元素个数
	Elems       []TypeID // Tuple 的成员类型列表
	Params      []TypeID // Fn 的形参类型列表
	Return      TypeID   // Fn 的返回类型
	GenericArgs []TypeID // Struct / Enum 的泛型实参列表
}

// Context 管理类型驻留池并预注册内置原语与核心代数值类型
type Context struct {
	types       []TypeInfo
	internMap   map[string]TypeID
	derivedCopy map[uint64]bool // 已验证合法派生 @derive(Copy) 的具体结构体/枚举 SymbolID

	// 预置内置基本类型快捷 ID
	Never   TypeID
	Unit    TypeID
	Bool    TypeID
	Char    TypeID
	I8      TypeID
	I16     TypeID
	I32     TypeID
	I64     TypeID
	I128    TypeID
	ISize   TypeID
	U8      TypeID
	U16     TypeID
	U32     TypeID
	U64     TypeID
	U128    TypeID
	USize   TypeID
	F32     TypeID
	F64     TypeID
	Str     TypeID
	StringT TypeID
	StrView TypeID // &str

	// 内置泛型枚举预设符号 ID（由符号表或上下文协调注入）
	OptionSymID uint64
	ResultSymID uint64
}

// 内置预设 Option / Result 的保留 SymbolID，避免与普通声明冲突
const (
	BuiltinOptionSymbolID uint64 = 1
	BuiltinResultSymbolID uint64 = 2
	BuiltinVecSymbolID    uint64 = 3
)

// NewContext 初始化类型系统驻留上下文
func NewContext() *Context {
	ctx := &Context{
		types:       make([]TypeInfo, 0, 128),
		internMap:   make(map[string]TypeID, 128),
		derivedCopy: make(map[uint64]bool, 16),
		OptionSymID: BuiltinOptionSymbolID,
		ResultSymID: BuiltinResultSymbolID,
	}
	// ID 0 保留为 Invalid
	ctx.types = append(ctx.types, TypeInfo{ID: Invalid, Kind: KindInvalid, Name: "<invalid>"})

	ctx.Never = ctx.internPrimitive(KindNever, "!")
	ctx.Unit = ctx.internPrimitive(KindUnit, "unit")
	ctx.Bool = ctx.internPrimitive(KindBool, "bool")
	ctx.Char = ctx.internPrimitive(KindChar, "char")
	ctx.I8 = ctx.internPrimitive(KindI8, "i8")
	ctx.I16 = ctx.internPrimitive(KindI16, "i16")
	ctx.I32 = ctx.internPrimitive(KindI32, "i32")
	ctx.I64 = ctx.internPrimitive(KindI64, "i64")
	ctx.I128 = ctx.internPrimitive(KindI128, "i128")
	ctx.ISize = ctx.internPrimitive(KindISize, "isize")
	ctx.U8 = ctx.internPrimitive(KindU8, "u8")
	ctx.U16 = ctx.internPrimitive(KindU16, "u16")
	ctx.U32 = ctx.internPrimitive(KindU32, "u32")
	ctx.U64 = ctx.internPrimitive(KindU64, "u64")
	ctx.U128 = ctx.internPrimitive(KindU128, "u128")
	ctx.USize = ctx.internPrimitive(KindUSize, "usize")
	ctx.F32 = ctx.internPrimitive(KindF32, "f32")
	ctx.F64 = ctx.internPrimitive(KindF64, "f64")
	ctx.Str = ctx.internPrimitive(KindStr, "str")
	ctx.StringT = ctx.internPrimitive(KindString, "String")
	ctx.StrView = ctx.Ref(false, ctx.Str)

	return ctx
}

func (c *Context) internPrimitive(kind Kind, name string) TypeID {
	key := fmt.Sprintf("P:%d", kind)
	if id, ok := c.internMap[key]; ok {
		return id
	}
	id := TypeID(len(c.types))
	c.types = append(c.types, TypeInfo{ID: id, Kind: kind, Name: name})
	c.internMap[key] = id
	return id
}

func (c *Context) intern(key string, info TypeInfo) TypeID {
	if id, ok := c.internMap[key]; ok {
		return id
	}
	id := TypeID(len(c.types))
	info.ID = id
	c.types = append(c.types, info)
	c.internMap[key] = id
	return id
}

// Info 获取给定 TypeID 对应的不可变描述符
func (c *Context) Info(id TypeID) TypeInfo {
	if int(id) < 0 || int(id) >= len(c.types) {
		return c.types[Invalid]
	}
	return c.types[id]
}

// Ref 驻留并获取 &T 或 &mut T 引用类型
func (c *Context) Ref(isMut bool, elem TypeID) TypeID {
	key := fmt.Sprintf("R:%v:%d", isMut, elem)
	return c.intern(key, TypeInfo{Kind: KindRef, IsMut: isMut, Elem: elem})
}

// RawPtr 驻留并获取 *const T 或 *mut T 原生指针类型
func (c *Context) RawPtr(isMut bool, elem TypeID) TypeID {
	key := fmt.Sprintf("Ptr:%v:%d", isMut, elem)
	return c.intern(key, TypeInfo{Kind: KindRawPtr, IsMut: isMut, Elem: elem})
}

// Array 驻留并获取定长数组类型 [T; N]
func (c *Context) Array(elem TypeID, length int64) TypeID {
	key := fmt.Sprintf("Arr:%d:%d", elem, length)
	return c.intern(key, TypeInfo{Kind: KindArray, Elem: elem, Len: length})
}

// Slice 驻留并获取切片类型 [T]
func (c *Context) Slice(elem TypeID) TypeID {
	key := fmt.Sprintf("Slc:%d", elem)
	return c.intern(key, TypeInfo{Kind: KindSlice, Elem: elem})
}

// Tuple 驻留并获取元组类型；空元组自动规约为 KindUnit
func (c *Context) Tuple(elems []TypeID) TypeID {
	if len(elems) == 0 {
		return c.Unit
	}
	copied := make([]TypeID, len(elems))
	copy(copied, elems)
	var b strings.Builder
	b.WriteString("Tup:")
	for i, e := range copied {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d", e)
	}
	return c.intern(b.String(), TypeInfo{Kind: KindTuple, Elems: copied})
}

// Fn 驻留并获取函数签名类型 fn(Params) -> Return
func (c *Context) Fn(params []TypeID, ret TypeID) TypeID {
	copied := make([]TypeID, len(params))
	copy(copied, params)
	if ret == Invalid {
		ret = c.Unit
	}
	var b strings.Builder
	b.WriteString("Fn:")
	for i, p := range copied {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d", p)
	}
	fmt.Fprintf(&b, "->%d", ret)
	return c.intern(b.String(), TypeInfo{Kind: KindFn, Params: copied, Return: ret})
}

// Struct 驻留并获取具名结构体及其泛型实例化类型
func (c *Context) Struct(symID uint64, name string, genArgs []TypeID) TypeID {
	copied := make([]TypeID, len(genArgs))
	copy(copied, genArgs)
	var b strings.Builder
	fmt.Fprintf(&b, "Struct:%d", symID)
	if len(copied) > 0 {
		b.WriteByte('[')
		for i, a := range copied {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%d", a)
		}
		b.WriteByte(']')
	}
	return c.intern(b.String(), TypeInfo{
		Kind:        KindStruct,
		SymbolID:    symID,
		Name:        name,
		GenericArgs: copied,
	})
}

// Enum 驻留并获取具名枚举及其泛型实例化类型
func (c *Context) Enum(symID uint64, name string, genArgs []TypeID) TypeID {
	copied := make([]TypeID, len(genArgs))
	copy(copied, genArgs)
	var b strings.Builder
	fmt.Fprintf(&b, "Enum:%d", symID)
	if len(copied) > 0 {
		b.WriteByte('[')
		for i, a := range copied {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%d", a)
		}
		b.WriteByte(']')
	}
	return c.intern(b.String(), TypeInfo{
		Kind:        KindEnum,
		SymbolID:    symID,
		Name:        name,
		GenericArgs: copied,
	})
}

// TypeParam 驻留并获取泛型形参类型
func (c *Context) TypeParam(symID uint64, name string) TypeID {
	key := fmt.Sprintf("TP:%d", symID)
	return c.intern(key, TypeInfo{
		Kind:     KindTypeParam,
		SymbolID: symID,
		Name:     name,
	})
}

// Option 构造标准库内置 Option[T] 实例化类型
func (c *Context) Option(elem TypeID) TypeID {
	return c.Enum(c.OptionSymID, "Option", []TypeID{elem})
}

// Result 构造标准库内置 Result[T, E] 实例化类型
func (c *Context) Result(okType, errType TypeID) TypeID {
	return c.Enum(c.ResultSymID, "Result", []TypeID{okType, errType})
}

// IsOption 检查类型是否为 Option[T] 并返回其内部 T 类型
func (c *Context) IsOption(id TypeID) (TypeID, bool) {
	info := c.Info(id)
	if info.Kind == KindEnum && info.SymbolID == c.OptionSymID && len(info.GenericArgs) == 1 {
		return info.GenericArgs[0], true
	}
	return Invalid, false
}

// IsResult 检查类型是否为 Result[T, E] 并返回 (T, E, true)
func (c *Context) IsResult(id TypeID) (TypeID, TypeID, bool) {
	info := c.Info(id)
	if info.Kind == KindEnum && info.SymbolID == c.ResultSymID && len(info.GenericArgs) == 2 {
		return info.GenericArgs[0], info.GenericArgs[1], true
	}
	return Invalid, Invalid, false
}

// Format 生成面向开发者和结构化诊断的可读类型名称
func (c *Context) Format(id TypeID) string {
	info := c.Info(id)
	switch info.Kind {
	case KindInvalid:
		return "<unknown>"
	case KindNever:
		return "!"
	case KindUnit:
		return "unit"
	case KindBool, KindChar, KindI8, KindI16, KindI32, KindI64, KindI128, KindISize,
		KindU8, KindU16, KindU32, KindU64, KindU128, KindUSize, KindF32, KindF64,
		KindStr, KindString, KindTypeParam:
		return info.Name
	case KindRef:
		if info.IsMut {
			return "&mut " + c.Format(info.Elem)
		}
		return "&" + c.Format(info.Elem)
	case KindRawPtr:
		if info.IsMut {
			return "*mut " + c.Format(info.Elem)
		}
		return "*const " + c.Format(info.Elem)
	case KindArray:
		return fmt.Sprintf("[%s; %d]", c.Format(info.Elem), info.Len)
	case KindSlice:
		return fmt.Sprintf("[%s]", c.Format(info.Elem))
	case KindTuple:
		parts := make([]string, len(info.Elems))
		for i, e := range info.Elems {
			parts[i] = c.Format(e)
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case KindFn:
		parts := make([]string, len(info.Params))
		for i, p := range info.Params {
			parts[i] = c.Format(p)
		}
		return fmt.Sprintf("fn(%s) -> %s", strings.Join(parts, ", "), c.Format(info.Return))
	case KindStruct, KindEnum:
		if len(info.GenericArgs) == 0 {
			return info.Name
		}
		args := make([]string, len(info.GenericArgs))
		for i, a := range info.GenericArgs {
			args[i] = c.Format(a)
		}
		return fmt.Sprintf("%s[%s]", info.Name, strings.Join(args, ", "))
	default:
		return "<unknown>"
	}
}

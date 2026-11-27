// Package tir 定义 WON 编译器阶段 2C.0 的后端无关类型化中间表示 (TIR)。
// 该包严格依赖 Go 标准库，不引用任何 AST、符号表、前端类型上下文或 C 代码生成文本，从类型系统层面强制实现前后端隔离 (ADR-009)。
package tir

import (
	"fmt"
	"strings"
)

// TypeID 表示单个编译单元 TIR 类型表内的确定性规范类型编号 (0 表示无效类型)。
type TypeID uint32

// InvalidType 表示未解析或非法的 TIR 类型编号。
const InvalidType TypeID = 0

// TypeKind 定义后端无关的 TIR 类型结构分类。
type TypeKind uint8

const (
	TypeInvalid   TypeKind = iota
	TypeNever              // 发散底类型 (!)
	TypeUnit               // 单元类型 (unit)
	TypeBool               // 布尔类型 (bool)
	TypeChar               // Unicode 标量字符类型 (char)
	TypeInt                // 有符号或无符号整数类型 (含精确位宽或指针字长标志)
	TypeFloat              // IEEE 754 浮点类型 (f32 / f64)
	TypeStr                // UTF-8 字符串视图基元 (str, DST)
	TypeString             // 堆分配所有权字符串 (String)
	TypeRawPtr             // 非托管原生指针 (*const T / *mut T)
	TypeRef                // 受借用检查器保护的引用 (&T / &mut T)
	TypeArray              // 编译期定长数组 ([T; N])
	TypeSlice              // 动态切片视图 ([T])
	TypeTuple              // 定长异构元组 ((T1, T2, ...))
	TypeFn                 // 函数签名类型 (fn(P1, ...) -> R)
	TypeStruct             // 具名结构体或受管外部不透明资源句柄
	TypeEnum               // 具名代数数据类型枚举 (含 Option[T], Result[T, E])
	TypeTypeParam          // 泛型形参占位（仅在未单态化签名元数据中保留）
)

// String 返回 TypeKind 的确定性可读名称。
func (k TypeKind) String() string {
	switch k {
	case TypeNever:
		return "Never"
	case TypeBool:
		return "Bool"
	case TypeUnit:
		return "Unit"
	case TypeChar:
		return "Char"
	case TypeInt:
		return "Int"
	case TypeFloat:
		return "Float"
	case TypeStr:
		return "Str"
	case TypeString:
		return "String"
	case TypeRawPtr:
		return "RawPtr"
	case TypeRef:
		return "Ref"
	case TypeArray:
		return "Array"
	case TypeSlice:
		return "Slice"
	case TypeTuple:
		return "Tuple"
	case TypeFn:
		return "Fn"
	case TypeStruct:
		return "Struct"
	case TypeEnum:
		return "Enum"
	case TypeTypeParam:
		return "TypeParam"
	default:
		return "Invalid"
	}
}

// VariantPayloadKind 区分枚举变体在 TIR 中的载荷形式。
type VariantPayloadKind uint8

const (
	VariantUnit   VariantPayloadKind = iota // 无载荷单元变体 (如 None)
	VariantTuple                            // 有序元组载荷变体 (如 Some(T))
	VariantStruct                           // 具名字段载荷变体
)

// StructFieldType 描述 TIR 结构体或结构体枚举变体的有序字段签名。
type StructFieldType struct {
	Name  string
	Index int
	Type  TypeID
	IsVar bool
}

// EnumVariantType 描述 TIR 枚举类型的单个有序变体定义。
type EnumVariantType struct {
	Name         string
	Index        int
	Kind         VariantPayloadKind
	TupleElems   []TypeID
	StructFields []StructFieldType
}

// TypeInfo 描述规范化 TIR 类型的完整后端无关元数据。
type TypeInfo struct {
	ID               TypeID
	Kind             TypeKind
	Name             string
	CanonicalKey     string
	Signed           bool     // TypeInt 是否有符号
	Bits             uint8    // TypeInt (8/16/32/64/128; 0 表示指针字长) 与 TypeFloat (32/64)
	IsPtrSized       bool     // TypeInt 是否为 isize / usize
	IsMut            bool     // TypeRef / TypeRawPtr 可变性
	IsCopy           bool     // 是否具备按位 Copy 语义
	IsOpaqueForeign  bool     // 是否经由 FFI 契约登记为不透明外部资源句柄
	ForeignNamespace string   // 所属外部 FFI 命名空间（若 IsOpaqueForeign 为 true）
	Elem             TypeID   // TypeRef / TypeRawPtr / TypeArray / TypeSlice 元素类型
	ArrayLen         int64    // TypeArray 长度
	Elems            []TypeID // TypeTuple 成员类型列表
	Params           []TypeID // TypeFn 参数类型列表
	Return           TypeID   // TypeFn 返回类型
	Fields           []StructFieldType
	Variants         []EnumVariantType
	GenericArgs      []TypeID
}

// TypeTable 维护编译单元内确定性驻留的所有 TIR 类型定义，保证相同语义结构映射到同一 TypeID。
type TypeTable struct {
	entries   []TypeInfo
	internMap map[string]TypeID
}

// NewTypeTable 初始化空 TIR 类型表（保留 ID=0 为 InvalidType）。
func NewTypeTable() *TypeTable {
	t := &TypeTable{
		entries:   make([]TypeInfo, 1, 64),
		internMap: make(map[string]TypeID, 64),
	}
	t.entries[0] = TypeInfo{
		ID:           InvalidType,
		Kind:         TypeInvalid,
		Name:         "<invalid>",
		CanonicalKey: "<invalid>",
	}
	return t
}

func (t *TypeTable) intern(key string, info TypeInfo) TypeID {
	if id, exists := t.internMap[key]; exists {
		return id
	}
	id := TypeID(len(t.entries))
	info.ID = id
	info.CanonicalKey = key
	t.entries = append(t.entries, info)
	t.internMap[key] = id
	return id
}

// Len 返回已注册类型条目数（含 ID=0 保留项）。
func (t *TypeTable) Len() int {
	if t == nil {
		return 0
	}
	return len(t.entries)
}

// Valid 检查给定 TypeID 是否在当前类型表中有效定义。
func (t *TypeTable) Valid(id TypeID) bool {
	return t != nil && id != InvalidType && int(id) > 0 && int(id) < len(t.entries)
}

// Info 返回 TypeID 对应的不可变描述符；越界时返回 InvalidType 描述符。
func (t *TypeTable) Info(id TypeID) TypeInfo {
	if t == nil || int(id) < 0 || int(id) >= len(t.entries) {
		return TypeInfo{ID: InvalidType, Kind: TypeInvalid, Name: "<invalid>", CanonicalKey: "<invalid>"}
	}
	return t.entries[id]
}

// Unit 驻留并获取单元类型。
func (t *TypeTable) Unit() TypeID {
	return t.intern("P:unit", TypeInfo{Kind: TypeUnit, Name: "unit", IsCopy: true})
}

// Never 驻留并获取发散底类型。
func (t *TypeTable) Never() TypeID {
	return t.intern("P:never", TypeInfo{Kind: TypeNever, Name: "!", IsCopy: true})
}

// Bool 驻留并获取布尔类型。
func (t *TypeTable) Bool() TypeID {
	return t.intern("P:bool", TypeInfo{Kind: TypeBool, Name: "bool", IsCopy: true})
}

// Char 驻留并获取字符类型。
func (t *TypeTable) Char() TypeID {
	return t.intern("P:char", TypeInfo{Kind: TypeChar, Name: "char", IsCopy: true})
}

// Str 驻留并获取 str 切片视图基元类型。
func (t *TypeTable) Str() TypeID {
	return t.intern("P:str", TypeInfo{Kind: TypeStr, Name: "str", IsCopy: false})
}

// StringType 驻留并获取 String 所有权字符串类型。
func (t *TypeTable) StringType() TypeID {
	return t.intern("P:String", TypeInfo{Kind: TypeString, Name: "String", IsCopy: false})
}

// Int 驻留并获取定长或指针字长整数类型。
func (t *TypeTable) Int(signed bool, bits uint8, ptrSized bool, name string) TypeID {
	key := fmt.Sprintf("I:%v:%d:%v", signed, bits, ptrSized)
	if name == "" {
		prefix := "u"
		if signed {
			prefix = "i"
		}
		if ptrSized {
			name = prefix + "size"
		} else {
			name = fmt.Sprintf("%s%d", prefix, bits)
		}
	}
	return t.intern(key, TypeInfo{
		Kind:       TypeInt,
		Name:       name,
		Signed:     signed,
		Bits:       bits,
		IsPtrSized: ptrSized,
		IsCopy:     true,
	})
}

// Float 驻留并获取 IEEE 754 浮点类型。
func (t *TypeTable) Float(bits uint8) TypeID {
	name := fmt.Sprintf("f%d", bits)
	return t.intern("F:"+name, TypeInfo{
		Kind:   TypeFloat,
		Name:   name,
		Bits:   bits,
		IsCopy: true,
	})
}

// RawPtr 驻留并获取原生指针类型。
func (t *TypeTable) RawPtr(isMut bool, elem TypeID) TypeID {
	key := fmt.Sprintf("Ptr:%v:%d", isMut, elem)
	return t.intern(key, TypeInfo{Kind: TypeRawPtr, IsMut: isMut, Elem: elem, IsCopy: true})
}

// Ref 驻留并获取借用引用类型。
func (t *TypeTable) Ref(isMut bool, elem TypeID) TypeID {
	key := fmt.Sprintf("Ref:%v:%d", isMut, elem)
	return t.intern(key, TypeInfo{Kind: TypeRef, IsMut: isMut, Elem: elem, IsCopy: !isMut})
}

// Array 驻留并获取定长数组类型。
func (t *TypeTable) Array(elem TypeID, length int64) TypeID {
	key := fmt.Sprintf("Arr:%d:%d", elem, length)
	isCopy := t.Info(elem).IsCopy
	return t.intern(key, TypeInfo{Kind: TypeArray, Elem: elem, ArrayLen: length, IsCopy: isCopy})
}

// Slice 驻留并获取动态切片类型。
func (t *TypeTable) Slice(elem TypeID) TypeID {
	key := fmt.Sprintf("Slc:%d", elem)
	return t.intern(key, TypeInfo{Kind: TypeSlice, Elem: elem, IsCopy: false})
}

// Tuple 驻留并获取元组类型；空元组等价于 Unit。
func (t *TypeTable) Tuple(elems []TypeID) TypeID {
	if len(elems) == 0 {
		return t.Unit()
	}
	copied := append([]TypeID(nil), elems...)
	var sb strings.Builder
	sb.WriteString("Tup:")
	isCopy := true
	for i, e := range copied {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%d", e)
		if !t.Info(e).IsCopy {
			isCopy = false
		}
	}
	return t.intern(sb.String(), TypeInfo{Kind: TypeTuple, Elems: copied, IsCopy: isCopy})
}

// Fn 驻留并获取函数签名类型。
func (t *TypeTable) Fn(params []TypeID, ret TypeID) TypeID {
	copied := append([]TypeID(nil), params...)
	if ret == InvalidType {
		ret = t.Unit()
	}
	var sb strings.Builder
	sb.WriteString("Fn:")
	for i, p := range copied {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%d", p)
	}
	fmt.Fprintf(&sb, "->%d", ret)
	return t.intern(sb.String(), TypeInfo{Kind: TypeFn, Params: copied, Return: ret, IsCopy: true})
}

// ReserveStruct 预留具名结构体类型槽位（支持自引用/间接引用前向声明），后续通过 DefineStruct 填充字段。
func (t *TypeTable) ReserveStruct(canonicalKey, name string, genericArgs []TypeID, isCopy, isOpaqueForeign bool, foreignNS string) TypeID {
	if id, exists := t.internMap[canonicalKey]; exists {
		return id
	}
	return t.intern(canonicalKey, TypeInfo{
		Kind:             TypeStruct,
		Name:             name,
		IsCopy:           isCopy,
		IsOpaqueForeign:  isOpaqueForeign,
		ForeignNamespace: foreignNS,
		GenericArgs:      append([]TypeID(nil), genericArgs...),
	})
}

// DefineStruct 回填已预留结构体的有序字段定义。
func (t *TypeTable) DefineStruct(id TypeID, fields []StructFieldType) {
	if !t.Valid(id) || t.entries[id].Kind != TypeStruct {
		return
	}
	copied := make([]StructFieldType, len(fields))
	copy(copied, fields)
	t.entries[id].Fields = copied
}

// ReserveEnum 预留具名枚举类型槽位，后续通过 DefineEnum 填充变体布局。
func (t *TypeTable) ReserveEnum(canonicalKey, name string, genericArgs []TypeID, isCopy bool) TypeID {
	if id, exists := t.internMap[canonicalKey]; exists {
		return id
	}
	return t.intern(canonicalKey, TypeInfo{
		Kind:        TypeEnum,
		Name:        name,
		IsCopy:      isCopy,
		GenericArgs: append([]TypeID(nil), genericArgs...),
	})
}

// DefineEnum 回填已预留枚举的有序变体定义。
func (t *TypeTable) DefineEnum(id TypeID, variants []EnumVariantType) {
	if !t.Valid(id) || t.entries[id].Kind != TypeEnum {
		return
	}
	copied := make([]EnumVariantType, len(variants))
	for i, v := range variants {
		cv := v
		if len(v.TupleElems) > 0 {
			cv.TupleElems = append([]TypeID(nil), v.TupleElems...)
		}
		if len(v.StructFields) > 0 {
			cv.StructFields = append([]StructFieldType(nil), v.StructFields...)
		}
		copied[i] = cv
	}
	t.entries[id].Variants = copied
}

// TypeParam 驻留未实例化泛型形参占位类型。
func (t *TypeTable) TypeParam(canonicalKey, name string) TypeID {
	return t.intern(canonicalKey, TypeInfo{Kind: TypeTypeParam, Name: name})
}

// IsNumeric 判断类型是否为整数或浮点数值类型。
func (t *TypeTable) IsNumeric(id TypeID) bool {
	k := t.Info(id).Kind
	return k == TypeInt || k == TypeFloat
}

// isCompatibleArgOrReturn 验证后端无关参数传递与返回时的精确类型一致性（保留 &mut->& 与 *mut->*const 协变及泛型实参）。
func (t *TypeTable) isCompatibleArgOrReturn(from, to TypeID) bool {
	if from == to {
		return true
	}
	fi := t.Info(from)
	ti := t.Info(to)
	if fi.Kind == TypeNever || ti.Kind == TypeNever || fi.Kind == TypeTypeParam || ti.Kind == TypeTypeParam {
		return true
	}
	if ((fi.Kind == TypeRef && ti.Kind == TypeRef) || (fi.Kind == TypeRawPtr && ti.Kind == TypeRawPtr)) &&
		fi.IsMut && !ti.IsMut && fi.Elem == ti.Elem {
		return true
	}
	// 同名泛型枚举/结构体实例化参数差异
	if (fi.Kind == TypeStruct && ti.Kind == TypeStruct) || (fi.Kind == TypeEnum && ti.Kind == TypeEnum) {
		if fi.Name == ti.Name && (len(fi.GenericArgs) > 0 || len(ti.GenericArgs) > 0) {
			return true
		}
	}
	return false
}

// Format 返回确定性、与任何目标后端无关的可读 TIR 类型拼写。
func (t *TypeTable) Format(id TypeID) string {
	return t.formatVisited(id, make(map[TypeID]bool, 8))
}

func (t *TypeTable) formatVisited(id TypeID, seen map[TypeID]bool) string {
	info := t.Info(id)
	switch info.Kind {
	case TypeInvalid:
		return "<invalid>"
	case TypeNever:
		return "!"
	case TypeUnit:
		return "unit"
	case TypeBool, TypeChar, TypeInt, TypeFloat, TypeStr, TypeString, TypeTypeParam:
		return info.Name
	case TypeRawPtr:
		if info.IsMut {
			return "*mut " + t.formatVisited(info.Elem, seen)
		}
		return "*const " + t.formatVisited(info.Elem, seen)
	case TypeRef:
		if info.IsMut {
			return "&mut " + t.formatVisited(info.Elem, seen)
		}
		return "&" + t.formatVisited(info.Elem, seen)
	case TypeArray:
		return fmt.Sprintf("[%s; %d]", t.formatVisited(info.Elem, seen), info.ArrayLen)
	case TypeSlice:
		return fmt.Sprintf("[%s]", t.formatVisited(info.Elem, seen))
	case TypeTuple:
		parts := make([]string, len(info.Elems))
		for i, e := range info.Elems {
			parts[i] = t.formatVisited(e, seen)
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case TypeFn:
		parts := make([]string, len(info.Params))
		for i, p := range info.Params {
			parts[i] = t.formatVisited(p, seen)
		}
		return fmt.Sprintf("fn(%s) -> %s", strings.Join(parts, ", "), t.formatVisited(info.Return, seen))
	case TypeStruct, TypeEnum:
		if len(info.GenericArgs) == 0 || seen[id] {
			return info.Name
		}
		seen[id] = true
		args := make([]string, len(info.GenericArgs))
		for i, a := range info.GenericArgs {
			args[i] = t.formatVisited(a, seen)
		}
		delete(seen, id)
		return fmt.Sprintf("%s[%s]", info.Name, strings.Join(args, ", "))
	default:
		return "<invalid>"
	}
}

package symbols

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/ast"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// Table 管理整个编译单元的符号分配、前导内建符号及两阶段声明解析
type Table struct {
	Types       *types.Context
	GlobalScope *Scope
	All         []*Symbol
	ByID        map[SymbolID]*Symbol
	NodeSymbols map[ast.Node]*Symbol // AST 节点到已解析符号的绑定映射（供 TypedProgram 导出）
	nextID      uint64
}

// NewTable 初始化符号表并注册内置基本类型及标准前导符号 (Option, Result, String, 内置函数)
func NewTable(tctx *types.Context) *Table {
	t := &Table{
		Types:       tctx,
		GlobalScope: NewScope(ScopeModule, nil),
		All:         make([]*Symbol, 0, 64),
		ByID:        make(map[SymbolID]*Symbol, 64),
		NodeSymbols: make(map[ast.Node]*Symbol, 128),
		nextID:      100, // 1..99 保留给内置预置符号
	}
	t.registerPrelude()
	return t
}

func (t *Table) allocID() SymbolID {
	id := SymbolID(t.nextID)
	t.nextID++
	return id
}

// AllocSymbol 分配一个带唯一 SymbolID 的新符号并登记在全局列表中
func (t *Table) AllocSymbol(kind SymbolKind, name string, span diagnostic.Span) *Symbol {
	sym := &Symbol{
		ID:       t.allocID(),
		Kind:     kind,
		Name:     name,
		Span:     span,
		Methods:  make(map[string]*Symbol),
		FieldMap: make(map[string]*FieldInfo),
	}
	t.All = append(t.All, sym)
	t.ByID[sym.ID] = sym
	return sym
}

func (t *Table) registerBuiltinTypeSym(id SymbolID, kind SymbolKind, name string, typ types.TypeID) *Symbol {
	sym := &Symbol{
		ID:         id,
		Kind:       kind,
		Name:       name,
		Type:       typ,
		IsPub:      true,
		Defined:    true,
		Methods:    make(map[string]*Symbol),
		FieldMap:   make(map[string]*FieldInfo),
		VariantMap: make(map[string]*VariantInfo),
	}
	t.All = append(t.All, sym)
	t.ByID[sym.ID] = sym
	t.GlobalScope.Symbols[name] = sym
	return sym
}

func (t *Table) registerPrelude() {
	// 1. 注册内置基础函数
	for _, fnName := range []string{"println", "print", "panic", "assert"} {
		sym := t.AllocSymbol(SymBuiltin, fnName, diagnostic.Span{})
		sym.Type = t.Types.Unit
		sym.ReturnType = t.Types.Unit
		sym.Defined = true
		t.GlobalScope.Symbols[fnName] = sym
	}

	bytesEq := t.AllocSymbol(SymBuiltin, "bytes_equal", diagnostic.Span{})
	bytesEq.ParamTypes = []types.TypeID{
		t.Types.RawPtr(false, t.Types.U8),
		t.Types.RawPtr(false, t.Types.U8),
		t.Types.U64,
	}
	bytesEq.ReturnType = t.Types.Bool
	bytesEq.Type = t.Types.Fn(bytesEq.ParamTypes, t.Types.Bool)
	bytesEq.Defined = true
	t.GlobalScope.Symbols["bytes_equal"] = bytesEq

	// 2. 注册内置 String 类型及其固有方法 String::from
	stringSym := t.registerBuiltinTypeSym(t.allocID(), SymStruct, "String", t.Types.StringT)
	fromMethod := t.AllocSymbol(SymMethod, "from", diagnostic.Span{})
	fromMethod.OwnerSymID = stringSym.ID
	fromMethod.OwnerType = t.Types.StringT
	fromMethod.ParamNames = []string{"s"}
	fromMethod.ParamTypes = []types.TypeID{t.Types.StrView}
	fromMethod.ReturnType = t.Types.StringT
	fromMethod.Type = t.Types.Fn(fromMethod.ParamTypes, t.Types.StringT)
	stringSym.Methods["from"] = fromMethod

	// 3. 注册内置 Option[T] 泛型枚举与变体
	optSym := t.registerBuiltinTypeSym(SymbolID(types.BuiltinOptionSymbolID), SymEnum, "Option", types.Invalid)
	optParamID := t.allocID()
	optT := t.Types.TypeParam(uint64(optParamID), "T")
	optSym.GenericParamIDs = []SymbolID{optParamID}
	optSym.GenericParamNames = []string{"T"}
	optSym.Type = t.Types.Enum(uint64(optSym.ID), "Option", []types.TypeID{optT})

	someSym := t.AllocSymbol(SymEnumVariant, "Some", diagnostic.Span{})
	someSym.OwnerSymID = optSym.ID
	noneSym := t.AllocSymbol(SymEnumVariant, "None", diagnostic.Span{})
	noneSym.OwnerSymID = optSym.ID

	optSym.Variants = []VariantInfo{
		{SymbolID: someSym.ID, Name: "Some", Kind: VariantTuple, TupleTypes: []types.TypeID{optT}},
		{SymbolID: noneSym.ID, Name: "None", Kind: VariantUnit},
	}
	optSym.VariantMap["Some"] = &optSym.Variants[0]
	optSym.VariantMap["None"] = &optSym.Variants[1]

	// 4. 注册内置 Result[T, E] 泛型枚举与变体
	resSym := t.registerBuiltinTypeSym(SymbolID(types.BuiltinResultSymbolID), SymEnum, "Result", types.Invalid)
	resTID := t.allocID()
	resEID := t.allocID()
	resT := t.Types.TypeParam(uint64(resTID), "T")
	resE := t.Types.TypeParam(uint64(resEID), "E")
	resSym.GenericParamIDs = []SymbolID{resTID, resEID}
	resSym.GenericParamNames = []string{"T", "E"}
	resSym.Type = t.Types.Enum(uint64(resSym.ID), "Result", []types.TypeID{resT, resE})

	okSym := t.AllocSymbol(SymEnumVariant, "Ok", diagnostic.Span{})
	okSym.OwnerSymID = resSym.ID
	errSym := t.AllocSymbol(SymEnumVariant, "Err", diagnostic.Span{})
	errSym.OwnerSymID = resSym.ID

	resSym.Variants = []VariantInfo{
		{SymbolID: okSym.ID, Name: "Ok", Kind: VariantTuple, TupleTypes: []types.TypeID{resT}},
		{SymbolID: errSym.ID, Name: "Err", Kind: VariantTuple, TupleTypes: []types.TypeID{resE}},
	}
	resSym.VariantMap["Ok"] = &resSym.Variants[0]
	resSym.VariantMap["Err"] = &resSym.Variants[1]

	// 允许在源代码中不加 Option:: / Result:: 前缀直接调用内置构造器 Some / None / Ok / Err
	t.GlobalScope.Symbols["Some"] = someSym
	t.GlobalScope.Symbols["None"] = noneSym
	t.GlobalScope.Symbols["Ok"] = okSym
	t.GlobalScope.Symbols["Err"] = errSym
}

// ResolveTypeNode 将 AST 类型节点解析为规范内部类型 ID (types.TypeID)
func (t *Table) ResolveTypeNode(tn ast.TypeNode, scope *Scope, report *diagnostic.Report) types.TypeID {
	if tn == nil {
		return t.Types.Unit
	}

	switch node := tn.(type) {
	case *ast.RefType:
		elem := t.ResolveTypeNode(node.Elem, scope, report)
		if elem == types.Invalid {
			return types.Invalid
		}
		return t.Types.Ref(node.IsMut, elem)

	case *ast.RawPtrType:
		elem := t.ResolveTypeNode(node.Elem, scope, report)
		if elem == types.Invalid {
			return types.Invalid
		}
		return t.Types.RawPtr(node.IsMut, elem)

	case *ast.SliceType:
		elem := t.ResolveTypeNode(node.Elem, scope, report)
		if elem == types.Invalid {
			return types.Invalid
		}
		return t.Types.Slice(elem)

	case *ast.ArrayType:
		elem := t.ResolveTypeNode(node.Elem, scope, report)
		if elem == types.Invalid {
			return types.Invalid
		}
		sizeStr := strings.ReplaceAll(node.Size, "_", "")
		length, err := strconv.ParseInt(sizeStr, 10, 64)
		if err != nil || length < 0 {
			if report != nil {
				report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-TYP-004",
					Severity: "error",
					Message:  fmt.Sprintf("非法的定长数组尺寸常量: %q", node.Size),
					File:     node.Span.File,
					Span:     node.Span,
				})
			}
			return types.Invalid
		}
		return t.Types.Array(elem, length)

	case *ast.TupleType:
		elems := make([]types.TypeID, len(node.Elems))
		for i, e := range node.Elems {
			elems[i] = t.ResolveTypeNode(e, scope, report)
		}
		return t.Types.Tuple(elems)

	case *ast.FnType:
		params := make([]types.TypeID, len(node.Params))
		for i, p := range node.Params {
			params[i] = t.ResolveTypeNode(p, scope, report)
		}
		ret := t.Types.Unit
		if node.ReturnType != nil {
			ret = t.ResolveTypeNode(node.ReturnType, scope, report)
		}
		return t.Types.Fn(params, ret)

	case *ast.NamedType:
		if len(node.Path) == 0 {
			return types.Invalid
		}
		// 单段路径优先匹配内置基元类型和词汇别名
		if len(node.Path) == 1 && len(node.GenericArgs) == 0 {
			switch node.Path[0] {
			case "unit", "void":
				return t.Types.Unit
			case "bool":
				return t.Types.Bool
			case "char":
				return t.Types.Char
			case "str":
				return t.Types.Str
			case "Str":
				return t.Types.StrView
			case "String":
				return t.Types.StringT
			case "i8":
				return t.Types.I8
			case "i16":
				return t.Types.I16
			case "i32":
				return t.Types.I32
			case "i64", "Int":
				return t.Types.I64
			case "i128":
				return t.Types.I128
			case "isize":
				return t.Types.ISize
			case "u8", "Byte":
				return t.Types.U8
			case "u16":
				return t.Types.U16
			case "u32":
				return t.Types.U32
			case "u64", "UInt":
				return t.Types.U64
			case "u128":
				return t.Types.U128
			case "usize":
				return t.Types.USize
			case "f32":
				return t.Types.F32
			case "f64":
				return t.Types.F64
			}
		}

		// 查找用户自定义或前导声明类型（含 Self、泛型形参、Struct、Enum、TypeAlias）
		symName := node.Path[0]
		if len(node.Path) > 1 {
			symName = strings.Join(node.Path, "::")
		}
		sym := scope.Lookup(node.Path[0])
		if len(node.Path) > 1 {
			// 多段类型路径：先试图解析模块链，否则尝试精确全名查找
			sym = t.resolveQualifiedTypeSymbol(node.Path, scope)
		}

		if sym == nil {
			if report != nil {
				report.AddDiagnostic(diagnostic.Diagnostic{
					Code:        "WON-NAM-003",
					Severity:    "error",
					Message:     fmt.Sprintf("未声明的未知类型: %q", symName),
					File:        node.Span.File,
					Span:        node.Span,
					Explanation: fmt.Sprintf("当前作用域内未找到类型 '%s' 的定义，请检查拼写或确认是否遗漏定义。", symName),
				})
			}
			return types.Invalid
		}

		// 检查泛型实参个数匹配
		resolvedArgs := make([]types.TypeID, len(node.GenericArgs))
		for i, a := range node.GenericArgs {
			resolvedArgs[i] = t.ResolveTypeNode(a, scope, report)
		}

		switch sym.Kind {
		case SymTypeAlias:
			if len(resolvedArgs) > 0 && len(resolvedArgs) != len(sym.GenericParamIDs) {
				t.emitGenericArityErr(sym.Name, len(sym.GenericParamIDs), len(resolvedArgs), node.Span, report)
				return types.Invalid
			}
			if len(resolvedArgs) == 0 {
				return sym.Type
			}
			subst := make(map[uint64]types.TypeID, len(resolvedArgs))
			for i, pid := range sym.GenericParamIDs {
				subst[uint64(pid)] = resolvedArgs[i]
			}
			return t.Types.Substitute(sym.Type, subst)

		case SymStruct:
			if len(resolvedArgs) != len(sym.GenericParamIDs) {
				t.emitGenericArityErr(sym.Name, len(sym.GenericParamIDs), len(resolvedArgs), node.Span, report)
				return types.Invalid
			}
			return t.Types.Struct(uint64(sym.ID), sym.Name, resolvedArgs)

		case SymEnum:
			if len(resolvedArgs) != len(sym.GenericParamIDs) {
				t.emitGenericArityErr(sym.Name, len(sym.GenericParamIDs), len(resolvedArgs), node.Span, report)
				return types.Invalid
			}
			return t.Types.Enum(uint64(sym.ID), sym.Name, resolvedArgs)

		case SymTypeParam:
			if len(resolvedArgs) > 0 {
				t.emitGenericArityErr(sym.Name, 0, len(resolvedArgs), node.Span, report)
				return types.Invalid
			}
			return sym.Type

		default:
			if report != nil {
				report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-NAM-003",
					Severity: "error",
					Message:  fmt.Sprintf("符号 %q 不是有效类型名称", sym.Name),
					File:     node.Span.File,
					Span:     node.Span,
				})
			}
			return types.Invalid
		}
	}

	return types.Invalid
}

func (t *Table) resolveQualifiedTypeSymbol(path []string, scope *Scope) *Symbol {
	if len(path) == 0 {
		return nil
	}
	cur := scope.Lookup(path[0])
	if cur == nil || len(path) == 1 {
		return cur
	}
	return nil
}

func (t *Table) emitGenericArityErr(name string, want, got int, span diagnostic.Span, report *diagnostic.Report) {
	if report == nil {
		return
	}
	report.AddDiagnostic(diagnostic.Diagnostic{
		Code:        "WON-ADT-007",
		Severity:    "error",
		Message:     fmt.Sprintf("泛型类型 '%s' 期望 %d 个类型参数，实际传入 %d 个", name, want, got),
		File:        span.File,
		Span:        span,
		Explanation: "泛型类型实例化的实参数量必须与声明处的泛型形参数量严格一致。",
	})
}

// ResolveTypeString 兼容纯字符串形式（例如 Phase 0 函数形参返回串）到 canonical TypeID 的解析
func (t *Table) ResolveTypeString(raw string, scope *Scope) types.TypeID {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "", "void", "unit":
		return t.Types.Unit
	case "bool":
		return t.Types.Bool
	case "char":
		return t.Types.Char
	case "str":
		return t.Types.Str
	case "&str", "Str":
		return t.Types.StrView
	case "String":
		return t.Types.StringT
	case "i8":
		return t.Types.I8
	case "i16":
		return t.Types.I16
	case "i32":
		return t.Types.I32
	case "i64", "Int":
		return t.Types.I64
	case "i128":
		return t.Types.I128
	case "isize":
		return t.Types.ISize
	case "u8", "Byte":
		return t.Types.U8
	case "u16":
		return t.Types.U16
	case "u32":
		return t.Types.U32
	case "u64", "UInt":
		return t.Types.U64
	case "u128":
		return t.Types.U128
	case "usize":
		return t.Types.USize
	case "f32":
		return t.Types.F32
	case "f64":
		return t.Types.F64
	case "*const u8":
		return t.Types.RawPtr(false, t.Types.U8)
	case "*mut u8":
		return t.Types.RawPtr(true, t.Types.U8)
	}
	if scope != nil {
		if sym := scope.Lookup(raw); sym != nil {
			return sym.Type
		}
	}
	return types.Invalid
}

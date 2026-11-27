package lower

import (
	"fmt"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/tir"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

type typeConverter struct {
	src          *types.Context
	syms         *symbols.Table
	tt           *tir.TypeTable
	opaqueOwners map[string]string
	cache        map[types.TypeID]tir.TypeID
}

func newTypeConverter(src *types.Context, syms *symbols.Table, tt *tir.TypeTable, opaqueOwners map[string]string) *typeConverter {
	return &typeConverter{
		src:          src,
		syms:         syms,
		tt:           tt,
		opaqueOwners: opaqueOwners,
		cache:        make(map[types.TypeID]tir.TypeID, 64),
	}
}

func (tc *typeConverter) convert(id types.TypeID) tir.TypeID {
	if id == types.Invalid {
		return tc.tt.Unit()
	}
	if cached, ok := tc.cache[id]; ok {
		return cached
	}

	info := tc.src.Info(id)
	var out tir.TypeID
	switch info.Kind {
	case types.KindNever:
		out = tc.tt.Never()
	case types.KindUnit:
		out = tc.tt.Unit()
	case types.KindBool:
		out = tc.tt.Bool()
	case types.KindChar:
		out = tc.tt.Char()
	case types.KindI8:
		out = tc.tt.Int(true, 8, false, "i8")
	case types.KindI16:
		out = tc.tt.Int(true, 16, false, "i16")
	case types.KindI32:
		out = tc.tt.Int(true, 32, false, "i32")
	case types.KindI64:
		out = tc.tt.Int(true, 64, false, "i64")
	case types.KindI128:
		out = tc.tt.Int(true, 128, false, "i128")
	case types.KindISize:
		out = tc.tt.Int(true, 0, true, "isize")
	case types.KindU8:
		out = tc.tt.Int(false, 8, false, "u8")
	case types.KindU16:
		out = tc.tt.Int(false, 16, false, "u16")
	case types.KindU32:
		out = tc.tt.Int(false, 32, false, "u32")
	case types.KindU64:
		out = tc.tt.Int(false, 64, false, "u64")
	case types.KindU128:
		out = tc.tt.Int(false, 128, false, "u128")
	case types.KindUSize:
		out = tc.tt.Int(false, 0, true, "usize")
	case types.KindF32:
		out = tc.tt.Float(32)
	case types.KindF64:
		out = tc.tt.Float(64)
	case types.KindStr:
		out = tc.tt.Str()
	case types.KindString:
		out = tc.tt.StringType()
	case types.KindRawPtr:
		out = tc.tt.RawPtr(info.IsMut, tc.convert(info.Elem))
	case types.KindRef:
		out = tc.tt.Ref(info.IsMut, tc.convert(info.Elem))
	case types.KindArray:
		out = tc.tt.Array(tc.convert(info.Elem), info.Len)
	case types.KindSlice:
		out = tc.tt.Slice(tc.convert(info.Elem))
	case types.KindTuple:
		elems := make([]tir.TypeID, len(info.Elems))
		for i, e := range info.Elems {
			elems[i] = tc.convert(e)
		}
		out = tc.tt.Tuple(elems)
	case types.KindFn:
		params := make([]tir.TypeID, len(info.Params))
		for i, p := range info.Params {
			params[i] = tc.convert(p)
		}
		out = tc.tt.Fn(params, tc.convert(info.Return))
	case types.KindTypeParam:
		out = tc.tt.TypeParam(fmt.Sprintf("TP:%d", info.SymbolID), info.Name)
	case types.KindStruct:
		out = tc.convertStruct(id, info)
	case types.KindEnum:
		out = tc.convertEnum(id, info)
	default:
		out = tc.tt.Unit()
	}

	tc.cache[id] = out
	return out
}

func (tc *typeConverter) convertStruct(id types.TypeID, info types.TypeInfo) tir.TypeID {
	genArgs := make([]tir.TypeID, len(info.GenericArgs))
	for i, a := range info.GenericArgs {
		genArgs[i] = tc.convert(a)
	}
	key := canonicalSymKey("Struct", info.SymbolID, genArgs)
	foreignNS, isOpaque := tc.opaqueOwners[info.Name]
	reserved := tc.tt.ReserveStruct(key, info.Name, genArgs, tc.src.IsCopy(id), isOpaque, foreignNS)
	tc.cache[id] = reserved

	sym := tc.syms.ByID[symbols.SymbolID(info.SymbolID)]
	if sym == nil {
		return reserved
	}
	var subst map[uint64]types.TypeID
	if len(sym.GenericParamIDs) > 0 && len(sym.GenericParamIDs) == len(info.GenericArgs) {
		subst = make(map[uint64]types.TypeID, len(info.GenericArgs))
		for i, gid := range sym.GenericParamIDs {
			subst[uint64(gid)] = info.GenericArgs[i]
		}
	}
	fields := make([]tir.StructFieldType, len(sym.Fields))
	for i, f := range sym.Fields {
		ft := f.Type
		if subst != nil {
			ft = tc.src.Substitute(ft, subst)
		}
		fields[i] = tir.StructFieldType{
			Name:  f.Name,
			Index: i,
			Type:  tc.convert(ft),
			IsVar: f.IsVar,
		}
	}
	tc.tt.DefineStruct(reserved, fields)
	return reserved
}

func (tc *typeConverter) convertEnum(id types.TypeID, info types.TypeInfo) tir.TypeID {
	genArgs := make([]tir.TypeID, len(info.GenericArgs))
	for i, a := range info.GenericArgs {
		genArgs[i] = tc.convert(a)
	}
	key := canonicalSymKey("Enum", info.SymbolID, genArgs)
	reserved := tc.tt.ReserveEnum(key, info.Name, genArgs, tc.src.IsCopy(id))
	tc.cache[id] = reserved

	sym := tc.syms.ByID[symbols.SymbolID(info.SymbolID)]
	if sym == nil {
		return reserved
	}
	var subst map[uint64]types.TypeID
	if len(sym.GenericParamIDs) > 0 && len(sym.GenericParamIDs) == len(info.GenericArgs) {
		subst = make(map[uint64]types.TypeID, len(info.GenericArgs))
		for i, gid := range sym.GenericParamIDs {
			subst[uint64(gid)] = info.GenericArgs[i]
		}
	}
	variants := make([]tir.EnumVariantType, len(sym.Variants))
	for i, v := range sym.Variants {
		tv := tir.EnumVariantType{
			Name:  v.Name,
			Index: i,
		}
		switch v.Kind {
		case symbols.VariantUnit:
			tv.Kind = tir.VariantUnit
		case symbols.VariantTuple:
			tv.Kind = tir.VariantTuple
			tv.TupleElems = make([]tir.TypeID, len(v.TupleTypes))
			for j, pt := range v.TupleTypes {
				if subst != nil {
					pt = tc.src.Substitute(pt, subst)
				}
				tv.TupleElems[j] = tc.convert(pt)
			}
		case symbols.VariantStruct:
			tv.Kind = tir.VariantStruct
			tv.StructFields = make([]tir.StructFieldType, len(v.StructFields))
			for j, sf := range v.StructFields {
				st := sf.Type
				if subst != nil {
					st = tc.src.Substitute(st, subst)
				}
				tv.StructFields[j] = tir.StructFieldType{
					Name:  sf.Name,
					Index: j,
					Type:  tc.convert(st),
					IsVar: sf.IsVar,
				}
			}
		}
		variants[i] = tv
	}
	tc.tt.DefineEnum(reserved, variants)
	return reserved
}

func canonicalSymKey(prefix string, symID uint64, args []tir.TypeID) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s:%d", prefix, symID)
	if len(args) > 0 {
		sb.WriteByte('[')
		for i, a := range args {
			if i > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, "%d", a)
		}
		sb.WriteByte(']')
	}
	return sb.String()
}

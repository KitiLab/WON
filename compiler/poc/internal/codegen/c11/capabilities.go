package c11

import (
	"fmt"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/tir"
)

func newCapErr(code, msg string, span tir.SourceSpan, exp string) *CapabilityError {
	err := &CapabilityError{
		Code:        code,
		Message:     msg,
		Explanation: exp,
	}
	err.TIRSpan.File = span.File
	err.TIRSpan.StartLine = span.StartLine
	err.TIRSpan.StartCol = span.StartCol
	err.TIRSpan.StartByte = span.StartByte
	err.TIRSpan.EndLine = span.EndLine
	err.TIRSpan.EndCol = span.EndCol
	err.TIRSpan.EndByte = span.EndByte
	return err
}

// ValidateProgram 对经前端降低得到的 TIR 程序执行严格的纯 TIR C11 后端能力审核。
// 任意未验证 IR 或尚未实现的语言/内存布局特性均按 Fail-Closed 原则拒止于 C 源码发射之前 (WON-GEN-002 / WON-INT-001)。
func ValidateProgram(prog *tir.Program, target Target) error {
	if err := target.Validate(); err != nil {
		return err
	}
	if errs := tir.Verify(prog); len(errs) > 0 {
		return newCapErr(
			"WON-INT-001",
			fmt.Sprintf("TIR 结构或类型不变量校验失败: %v", tir.VerifyErrorSummary(errs)),
			tir.SourceSpan{},
			"C11 后端仅接受通过独立 tir.Verify 验证的规范中间表示。",
		)
	}

	tt := prog.Types

	// 记录已验证并携带头文件路径的 FFI 命名空间，确保不透明外部资源与释放在 C 中拥有合法声明来源
	validFFINS := make(map[string]bool)
	for _, cat := range prog.FFICatalogs {
		if cat.Namespace != "" && cat.HeaderPath != "" {
			validFFINS[cat.Namespace] = true
		}
	}
	for _, fc := range prog.FFIContracts {
		if fc.Namespace != "" && fc.HeaderPath != "" {
			validFFINS[fc.Namespace] = true
		}
	}

	// 1. 校验 DropGlue 目录：支持经过验证的 FFI 句柄以及按声明序递归清理的结构体/元组/定长数组/枚举胶水；
	// 严格拒止尚未开放的 String / CustomDrop / RegisteredResource 胶水。
	glueMap := make(map[tir.DropGlueID]tir.DropGlueDesc, len(prog.DropGlues))
	for _, g := range prog.DropGlues {
		glueMap[g.ID] = g
	}
	for _, g := range prog.DropGlues {
		if err := validateSupportedDropGlue(tt, g, glueMap, validFFINS, make(map[tir.DropGlueID]bool)); err != nil {
			return err
		}
	}

	// 2. 核验类型表中的不透明外部结构体均关联有效的头文件命名空间；其他结构体/枚举在函数触及时拒止
	for id := tir.TypeID(1); int(id) < tt.Len(); id++ {
		ti := tt.Info(id)
		if ti.Kind == tir.TypeStruct && ti.IsOpaqueForeign {
			if !validFFINS[ti.ForeignNamespace] || !isValidCIdentifier(ti.Name) {
				return newCapErr(
					"WON-GEN-002",
					fmt.Sprintf("外部不透明句柄类型 '%s' 缺少合法命名空间或有效 C 标识符名称", ti.Name),
					firstSpan(prog),
					"",
				)
			}
		}
	}

	// 3. 逐函数审校签名、局部变量、指令与终结器
	for _, fn := range prog.Functions {
		if fn == nil {
			continue
		}
		if err := validateFunctionCaps(prog, fn, glueMap, validFFINS); err != nil {
			return err
		}
	}
	return nil
}

func firstSpan(prog *tir.Program) tir.SourceSpan {
	if prog != nil {
		for _, fn := range prog.Functions {
			if fn != nil && fn.Span.StartLine > 0 {
				return fn.Span
			}
		}
	}
	return tir.SourceSpan{}
}

func splitForeignSymbol(sym string) (ns, fn string, ok bool) {
	idx := strings.Index(sym, "::")
	if idx <= 0 || idx+2 >= len(sym) {
		return "", "", false
	}
	return sym[:idx], sym[idx+2:], true
}

func isU8SliceRef(tt *tir.TypeTable, tid tir.TypeID) bool {
	ti := tt.Info(tid)
	if ti.Kind != tir.TypeRef || ti.IsMut {
		return false
	}
	si := tt.Info(ti.Elem)
	return si.Kind == tir.TypeSlice && tt.Info(si.Elem).Kind == tir.TypeInt && !tt.Info(si.Elem).Signed && tt.Info(si.Elem).Bits == 8
}

func validateFunctionCaps(prog *tir.Program, fn *tir.Function, glueMap map[tir.DropGlueID]tir.DropGlueDesc, validFFINS map[string]bool) error {
	tt := prog.Types

	// 校验函数名称必须为合法 C 标识符（拒止尚未支持代码生成的方法与关联函数，如 Point::new）
	if !isValidCIdentifier(fn.Name) {
		return newCapErr("WON-GEN-002", fmt.Sprintf("方法与关联函数 '%s' 尚未在当前 C11 后端实现代码生成", fn.Name), fn.Span, "")
	}

	// 检查泛型未单态化签名
	if _, err := MapType(tt, fn.ReturnType, PosReturn); err != nil {
		return newCapErr("WON-GEN-002", fmt.Sprintf("函数 '%s' 返回类型 %s 不支持 C11 生成: %v", fn.Name, tt.Format(fn.ReturnType), err), fn.Span, "")
	}
	for i, pt := range fn.ParamTypes {
		if _, err := MapType(tt, pt, PosParam); err != nil {
			return newCapErr("WON-GEN-002", fmt.Sprintf("函数 '%s' 形参 #%d 类型 %s 不支持 C11 生成: %v", fn.Name, i+1, tt.Format(pt), err), fn.Span, "")
		}
	}

	// ISO C11 §5.1.2.2.1 main ABI 守卫
	if fn.Name == "main" {
		if len(fn.ParamTypes) != 0 {
			return newCapErr("WON-GEN-002", "C11 后端要求程序入口 'main' 不得接收参数", fn.Span, "")
		}
		retInfo := tt.Info(fn.ReturnType)
		isI32 := retInfo.Kind == tir.TypeInt && retInfo.Signed && retInfo.Bits == 32 && !retInfo.IsPtrSized
		if !isI32 && retInfo.Kind != tir.TypeUnit {
			return newCapErr("WON-GEN-002", fmt.Sprintf("C11 宿主环境 'main' 入口仅支持返回 'i32' 或 'unit'，当前返回 '%s'", tt.Format(fn.ReturnType)), fn.Span, "")
		}
	}

	// 扫描局部作用域中的 Phase 0B .as_bytes() 只读字节切片视图别名，确认其仅被 as_bytes 初始化且仅被 as_ptr 消费
	sliceAliasLocals := collectSafeByteSliceAliases(tt, fn)

	for _, ld := range fn.Locals {
		if tt.Info(ld.Type).Kind == tir.TypeTypeParam {
			return newCapErr("WON-GEN-002", fmt.Sprintf("泛型函数 '%s' 尚未在 C11 后端实现单态化", fn.Name), ld.Span, "")
		}
		if ld.IsParam {
			if _, err := MapType(tt, ld.Type, PosParam); err != nil {
				return newCapErr("WON-GEN-002", fmt.Sprintf("函数 '%s' 参数 '%s' %v", fn.Name, ld.Name, err), ld.Span, "")
			}
			continue
		}
		if sliceAliasLocals[ld.ID] {
			continue
		}
		if isU8SliceRef(tt, ld.Type) {
			return newCapErr("WON-GEN-002", fmt.Sprintf("局部变量 '%s' (&[u8]) 在当前 C11 后端仅允许由 .as_bytes() 初始化并仅用于 .as_ptr()", ld.Name), ld.Span, "")
		}
		if _, err := MapType(tt, ld.Type, PosLocal); err != nil {
			return newCapErr("WON-GEN-002", fmt.Sprintf("局部变量 '%s' 类型 %s 不支持 C11 生成: %v", ld.Name, tt.Format(ld.Type), err), ld.Span, "")
		}
	}

	// 记录由 OpBorrow 派生的临时寄存器，辅助校验允许的 .as_ptr() / .as_mut_ptr() / .len() / &array 用法
	defInst := make(map[tir.ValueID]tir.Instruction)
	for _, b := range fn.Blocks {
		if b == nil {
			continue
		}
		for _, inst := range b.Instructions {
			if inst.Dest != tir.InvalidValue && inst.Kind != tir.OpValueAssign {
				defInst[inst.Dest] = inst
			}
		}
	}

	for _, b := range fn.Blocks {
		if b == nil {
			continue
		}
		for _, inst := range b.Instructions {
			if err := validateInstructionCaps(prog, fn, inst, defInst, glueMap, validFFINS, sliceAliasLocals); err != nil {
				return err
			}
		}
		if err := validateTerminatorCaps(tt, fn, b.Terminator); err != nil {
			return err
		}
	}
	return nil
}

// collectSafeByteSliceAliases 识别并验证符合 Phase 0B 契约的局部 &[u8] 绑定：
// 必须仅由 BuiltinAsBytes 初始化且在所有读取点仅通过 Borrow -> BuiltinAsPtr 消费，否则拒绝于 C11 后端之外。
func collectSafeByteSliceAliases(tt *tir.TypeTable, fn *tir.Function) map[tir.LocalID]bool {
	candidates := make(map[tir.LocalID]bool)
	for _, ld := range fn.Locals {
		if !ld.IsParam && isU8SliceRef(tt, ld.Type) {
			candidates[ld.ID] = true
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	// 建立 ValueID 定义和读取索引
	defKind := make(map[tir.ValueID]tir.Instruction)
	asPtrArgVals := make(map[tir.ValueID]bool)
	for _, b := range fn.Blocks {
		if b == nil {
			continue
		}
		for _, inst := range b.Instructions {
			if inst.Dest != tir.InvalidValue && inst.Kind != tir.OpValueAssign {
				defKind[inst.Dest] = inst
			}
			if inst.Kind == tir.OpBuiltinCall && inst.BuiltinKind == tir.BuiltinAsPtr && len(inst.Args) == 1 && inst.Args[0].Kind == tir.OperandValue {
				asPtrArgVals[inst.Args[0].Value] = true
			}
		}
	}

	validInit := make(map[tir.LocalID]bool)
	disqualified := make(map[tir.LocalID]bool)

	for _, b := range fn.Blocks {
		if b == nil {
			continue
		}
		for _, inst := range b.Instructions {
			switch inst.Kind {
			case tir.OpInit:
				if candidates[inst.Place.Local] && len(inst.Place.Projections) == 0 {
					if inst.Src.Kind == tir.OperandValue {
						if srcDef, ok := defKind[inst.Src.Value]; ok && srcDef.Kind == tir.OpBuiltinCall && srcDef.BuiltinKind == tir.BuiltinAsBytes {
							validInit[inst.Place.Local] = true
							continue
						}
					}
					disqualified[inst.Place.Local] = true
				}
			case tir.OpAssign:
				if candidates[inst.Place.Local] {
					disqualified[inst.Place.Local] = true
				}
			case tir.OpBorrow:
				if candidates[inst.Place.Local] {
					if asPtrArgVals[inst.Dest] {
						continue
					}
					disqualified[inst.Place.Local] = true
				}
			case tir.OpCopy, tir.OpMove, tir.OpComputeAddr:
				if candidates[inst.Place.Local] {
					disqualified[inst.Place.Local] = true
				}
			case tir.OpLoad:
				// lowerStatement 在 MethodCall 接收者前允许生成只读 OpRead -> OpLoad（若存在）仅当不绑定出去
				if candidates[inst.Place.Local] {
					if inst.Dest != tir.InvalidValue {
						disqualified[inst.Place.Local] = true
					}
				}
			}
		}
	}

	out := make(map[tir.LocalID]bool)
	for lid := range validInit {
		if !disqualified[lid] {
			out[lid] = true
		}
	}
	return out
}

func validateInstructionCaps(
	prog *tir.Program,
	fn *tir.Function,
	inst tir.Instruction,
	defInst map[tir.ValueID]tir.Instruction,
	glueMap map[tir.DropGlueID]tir.DropGlueDesc,
	validFFINS map[string]bool,
	sliceAliasLocals map[tir.LocalID]bool,
) error {
	tt := prog.Types

	checkNonReturnType := func(tid tir.TypeID, ctx string) error {
		if tt.Info(tid).Kind == tir.TypeUnit {
			return newCapErr("WON-GEN-002", fmt.Sprintf("%s 使用了 'unit' 非返回值对象类型", ctx), inst.Span, "")
		}
		if _, err := MapType(tt, tid, PosValue); err != nil {
			return newCapErr("WON-GEN-002", fmt.Sprintf("%s: %v", ctx, err), inst.Span, "")
		}
		return nil
	}

	switch inst.Kind {
	case tir.OpStorageLive, tir.OpStorageDead, tir.OpScopeEnter, tir.OpScopeExit,
		tir.OpDropFlagDecl, tir.OpDropFlagSet, tir.OpDropFlagCheck:
		return nil

	case tir.OpConst:
		if inst.Const.Kind == tir.ConstUnit {
			return newCapErr("WON-GEN-002", "不支持发射显式 unit '()' 常量值", inst.Span, "")
		}
		return checkNonReturnType(inst.Type, "常量表达式")

	case tir.OpStaticRef:
		return checkNonReturnType(inst.Type, "静态字符串引用")

	case tir.OpUnary:
		return checkNonReturnType(inst.Type, "一元运算")

	case tir.OpBinary:
		lt := tt.Info(inst.Left.Type)
		rt := tt.Info(inst.Right.Type)
		if inst.BinaryOp == tir.BinMod && (lt.Kind == tir.TypeFloat || rt.Kind == tir.TypeFloat) {
			return newCapErr("WON-GEN-002", "ISO C11 不支持对浮点类型执行 '%%' 取模运算", inst.Span, "")
		}
		if inst.BinaryOp == tir.BinEq || inst.BinaryOp == tir.BinNe {
			if (lt.Kind == tir.TypeRef && tt.Info(lt.Elem).Kind == tir.TypeStr) ||
				lt.Kind == tir.TypeUnit || lt.Kind == tir.TypeString || lt.Kind == tir.TypeEnum || lt.Kind == tir.TypeStruct || lt.Kind == tir.TypeTuple || lt.Kind == tir.TypeArray {
				if !(lt.Kind == tir.TypeStruct && lt.IsOpaqueForeign) {
					return newCapErr("WON-GEN-002", fmt.Sprintf("类型 '%s' 的相等比较 '%s' 尚未在当前 C11 后端实现", tt.Format(inst.Left.Type), inst.BinaryOp), inst.Span, "")
				}
			}
		}
		return checkNonReturnType(inst.Type, "二元运算")

	case tir.OpCast:
		return checkNonReturnType(inst.Type, "显式类型转换")

	case tir.OpValueAssign:
		if isU8SliceRef(tt, inst.Type) {
			return nil
		}
		if tt.Info(inst.Type).Kind != tir.TypeUnit {
			return checkNonReturnType(inst.Type, "汇合临时变量赋值")
		}
		return nil

	case tir.OpInit, tir.OpAssign:
		if err := validatePlaceProjectionsCaps(tt, inst.Place, inst.Span); err != nil {
			return err
		}
		if sliceAliasLocals[inst.Place.Local] {
			return nil
		}
		return checkNonReturnType(inst.Type, "变量初始化或赋值")

	case tir.OpLoad, tir.OpCopy, tir.OpMove:
		if err := validatePlaceProjectionsCaps(tt, inst.Place, inst.Span); err != nil {
			return err
		}
		if sliceAliasLocals[inst.Place.Local] && inst.Dest == tir.InvalidValue {
			return nil
		}
		return checkNonReturnType(inst.Type, "存储读取")

	case tir.OpStore:
		if err := validatePlaceProjectionsCaps(tt, inst.Place, inst.Span); err != nil {
			return err
		}
		return checkNonReturnType(inst.Type, "指针解引用存储")

	case tir.OpBorrow:
		if err := validatePlaceProjectionsCaps(tt, inst.Place, inst.Span); err != nil {
			return err
		}
		ti := tt.Info(inst.Type)
		if ti.Kind != tir.TypeRef {
			return newCapErr("WON-GEN-002", "借用目标必须为引用类型", inst.Span, "")
		}
		if isU8SliceRef(tt, inst.Type) {
			return nil
		}
		return checkNonReturnType(inst.Type, "引用借用")

	case tir.OpAggregateConstruct:
		switch inst.AggKind {
		case tir.AggStruct, tir.AggTuple, tir.AggArray, tir.AggEnumVariant:
			return checkNonReturnType(inst.Type, "聚合值构造")
		case tir.AggArrayRepeat:
			if inst.RepeatLen < 0 {
				return newCapErr("WON-GEN-002", fmt.Sprintf("定长数组重复构造长度 %d 非法", inst.RepeatLen), inst.Span, "")
			}
			return checkNonReturnType(inst.Type, "定长数组重复构造")
		case tir.AggInterpString:
			for _, it := range inst.AggItems {
				if err := checkNonReturnType(it.Value.Type, "插值字符串分量"); err != nil {
					return err
				}
			}
			return checkNonReturnType(inst.Type, "插值字符串构造")
		default:
			return newCapErr("WON-GEN-002", fmt.Sprintf("聚合值构造 (%s) 尚未在当前 C11 后端实现", inst.AggKind), inst.Span, "")
		}

	case tir.OpExtract:
		switch inst.ExtractProj.Kind {
		case tir.ProjDeref, tir.ProjDowncast, tir.ProjField, tir.ProjTupleField, tir.ProjConstIndex:
			return checkNonReturnType(inst.Type, "成员抽取")
		case tir.ProjDynIndex:
			if tt.Info(derefTypeIfRef(tt, inst.Src.Type)).Kind != tir.TypeArray || inst.ExtractProj.IndexValue == tir.InvalidValue {
				return newCapErr("WON-GEN-002", "仅支持在定长数组上进行整型动态下标抽取", inst.Span, "")
			}
			return checkNonReturnType(inst.Type, "数组动态下标抽取")
		default:
			return newCapErr("WON-GEN-002", fmt.Sprintf("成员抽取 (%v) 尚未在当前 C11 后端实现", inst.ExtractProj.Kind), inst.Span, "")
		}

	case tir.OpCall:
		if tt.Info(inst.Type).Kind != tir.TypeUnit {
			if _, err := MapType(tt, inst.Type, PosValue); err != nil {
				return newCapErr("WON-GEN-002", fmt.Sprintf("调用 '%s' 返回类型不支持 C11: %v", inst.CalleeName, err), inst.Span, "")
			}
		}
		return nil

	case tir.OpForeignCall:
		if tt.Info(inst.Type).Kind != tir.TypeUnit {
			if !isFFISafeType(tt, inst.Type, false) {
				return newCapErr("WON-GEN-002", fmt.Sprintf("FFI 调用 '%s::%s' 返回类型 '%s' 不允许跨 C ABI 边界传递", inst.CalleeNamespace, inst.CalleeName, tt.Format(inst.Type)), inst.Span, "ADR-012 禁止将 String、&str 或未注解原生 Struct/Enum/Tuple/Array 直接跨越 C FFI 边界。")
			}
			if _, err := MapType(tt, inst.Type, PosValue); err != nil {
				return newCapErr("WON-GEN-002", fmt.Sprintf("FFI 调用 '%s::%s' 返回类型不支持 C11: %v", inst.CalleeNamespace, inst.CalleeName, err), inst.Span, "")
			}
		}
		for i, a := range inst.Args {
			if !isFFISafeType(tt, a.Type, true) {
				return newCapErr("WON-GEN-002", fmt.Sprintf("FFI 调用 '%s::%s' 参数 #%d 类型 '%s' 禁止隐式跨越 C ABI 边界", inst.CalleeNamespace, inst.CalleeName, i+1, tt.Format(a.Type)), inst.Span, "通用 '&str' 不保证以 NUL 结尾；请显式使用 '.as_ptr()' (*const u8) 或经验证的 FFI 句柄/标量类型。")
			}
		}
		return nil

	case tir.OpBuiltinCall:
		switch inst.BuiltinKind {
		case tir.BuiltinPrintln, tir.BuiltinBytesEqual:
			return nil
		case tir.BuiltinStringFrom:
			if len(inst.Args) != 1 {
				return newCapErr("WON-GEN-002", "内置 String::from 期望 1 个实参", inst.Span, "")
			}
			argTi := tt.Info(inst.Args[0].Type)
			if (argTi.Kind == tir.TypeRef && tt.Info(argTi.Elem).Kind == tir.TypeStr) || argTi.Kind == tir.TypeStr {
				return checkNonReturnType(inst.Type, "String::from 结果")
			}
			return newCapErr("WON-GEN-002", fmt.Sprintf("内置 String::from 仅支持 &str 参数，实际为 '%s'", tt.Format(inst.Args[0].Type)), inst.Span, "")
		case tir.BuiltinPushStr:
			if len(inst.Args) != 2 {
				return newCapErr("WON-GEN-002", "内置 push_str 期望 2 个实参", inst.Span, "")
			}
			recTi := tt.Info(inst.Args[0].Type)
			if recTi.Kind != tir.TypeString && (recTi.Kind != tir.TypeRef || tt.Info(recTi.Elem).Kind != tir.TypeString) {
				return newCapErr("WON-GEN-002", fmt.Sprintf("内置 push_str 仅支持在 String / &mut String 接收者上调用，实际为 '%s'", tt.Format(inst.Args[0].Type)), inst.Span, "")
			}
			argTi := tt.Info(inst.Args[1].Type)
			if (argTi.Kind != tir.TypeRef || tt.Info(argTi.Elem).Kind != tir.TypeStr) && argTi.Kind != tir.TypeStr {
				return newCapErr("WON-GEN-002", fmt.Sprintf("内置 push_str 待追加实参必须为 &str，实际为 '%s'", tt.Format(inst.Args[1].Type)), inst.Span, "")
			}
			return nil
		case tir.BuiltinCapacity:
			if len(inst.Args) != 1 {
				return newCapErr("WON-GEN-002", "内置 .capacity() 期望 1 个实参", inst.Span, "")
			}
			argTi := tt.Info(inst.Args[0].Type)
			if argTi.Kind == tir.TypeString || (argTi.Kind == tir.TypeRef && tt.Info(argTi.Elem).Kind == tir.TypeString) {
				return nil
			}
			return newCapErr("WON-GEN-002", fmt.Sprintf("当前 C11 后端仅支持在 String 上调用 .capacity()，不支持 '%s'", tt.Format(inst.Args[0].Type)), inst.Span, "")
		case tir.BuiltinAsStr:
			if len(inst.Args) != 1 {
				return newCapErr("WON-GEN-002", "内置 .as_str() 期望 1 个实参", inst.Span, "")
			}
			argTi := tt.Info(inst.Args[0].Type)
			if argTi.Kind == tir.TypeString || (argTi.Kind == tir.TypeRef && tt.Info(argTi.Elem).Kind == tir.TypeString) {
				return nil
			}
			return newCapErr("WON-GEN-002", fmt.Sprintf("当前 C11 后端仅支持在 String 上调用 .as_str()，不支持 '%s'", tt.Format(inst.Args[0].Type)), inst.Span, "")
		case tir.BuiltinTryUnwrap:
			if len(inst.Args) != 1 || tt.Info(inst.Args[0].Type).Kind != tir.TypeEnum {
				return newCapErr("WON-GEN-002", "内置 '?' 解包操作符要求枚举 Result/Option 源操作数", inst.Span, "")
			}
			if tt.Info(inst.Type).Kind != tir.TypeUnit {
				return checkNonReturnType(inst.Type, "'?' 解包结果")
			}
			return nil
		case tir.BuiltinAsBytes:
			if len(inst.Args) != 1 {
				return newCapErr("WON-GEN-002", "内置 .as_bytes() 参数数量非法", inst.Span, "")
			}
			argTi := tt.Info(inst.Args[0].Type)
			if argTi.Kind != tir.TypeRef || tt.Info(argTi.Elem).Kind != tir.TypeStr {
				return newCapErr("WON-GEN-002", "目前仅支持在字符串视图 (&str) 上调用 .as_bytes()", inst.Span, "")
			}
			return nil
		case tir.BuiltinAsPtr:
			if len(inst.Args) != 1 {
				return newCapErr("WON-GEN-002", "内置 .as_ptr() 参数数量非法", inst.Span, "")
			}
			argTi := tt.Info(inst.Args[0].Type)
			if (argTi.Kind == tir.TypeRef && (tt.Info(argTi.Elem).Kind == tir.TypeStr || isSupportedArrayType(tt, argTi.Elem) || isU8SliceRef(tt, inst.Args[0].Type))) ||
				argTi.Kind == tir.TypeString || (argTi.Kind == tir.TypeRef && tt.Info(argTi.Elem).Kind == tir.TypeString) {
				return nil
			}
			return newCapErr("WON-GEN-002", fmt.Sprintf("当前 C11 后端暂不支持在 '%s' 上调用 .as_ptr()", tt.Format(inst.Args[0].Type)), inst.Span, "")
		case tir.BuiltinAsMutPtr:
			if len(inst.Args) != 1 {
				return newCapErr("WON-GEN-002", "内置 .as_mut_ptr() 参数数量非法", inst.Span, "")
			}
			argTi := tt.Info(inst.Args[0].Type)
			if (argTi.Kind == tir.TypeRef && argTi.IsMut && isSupportedArrayType(tt, argTi.Elem)) ||
				argTi.Kind == tir.TypeString || (argTi.Kind == tir.TypeRef && argTi.IsMut && tt.Info(argTi.Elem).Kind == tir.TypeString) {
				return nil
			}
			return newCapErr("WON-GEN-002", fmt.Sprintf("当前 C11 后端仅支持在可变定长数组或可变 String 上调用 .as_mut_ptr()，不支持 '%s'", tt.Format(inst.Args[0].Type)), inst.Span, "")
		case tir.BuiltinLen, tir.BuiltinIsEmpty:
			if len(inst.Args) != 1 {
				return newCapErr("WON-GEN-002", fmt.Sprintf("内置 .%s() 参数数量非法", inst.CalleeName), inst.Span, "")
			}
			argTi := tt.Info(inst.Args[0].Type)
			if (argTi.Kind == tir.TypeRef && ((tt.Info(argTi.Elem).Kind == tir.TypeStr && !argTi.IsMut) || isSupportedArrayType(tt, argTi.Elem))) ||
				argTi.Kind == tir.TypeString || (argTi.Kind == tir.TypeRef && tt.Info(argTi.Elem).Kind == tir.TypeString) {
				return nil
			}
			return newCapErr("WON-GEN-002", fmt.Sprintf("当前 C11 后端仅支持 '&str'、'String' 与定长数组 '[T; N]' 的 .%s()，不支持 '%s'", inst.CalleeName, tt.Format(inst.Args[0].Type)), inst.Span, "")
		default:
			return newCapErr("WON-GEN-002", fmt.Sprintf("内置函数或方法 '%s' 尚未在当前 C11 后端实现", inst.CalleeName), inst.Span, "")
		}

	case tir.OpOwnershipTransfer:
		return nil

	case tir.OpDrop, tir.OpCondDrop:
		g, ok := glueMap[inst.GlueID]
		if !ok {
			return newCapErr("WON-GEN-002", fmt.Sprintf("析构操作 g%d 未在程序胶水目录中注册", inst.GlueID), inst.Span, "")
		}
		return validateSupportedDropGlue(tt, g, glueMap, validFFINS, make(map[tir.DropGlueID]bool))

	default:
		return newCapErr("WON-GEN-002", fmt.Sprintf("TIR 指令 '%s' 尚未在当前 C11 后端实现", inst.Kind), inst.Span, "")
	}
}

func validateTerminatorCaps(tt *tir.TypeTable, fn *tir.Function, term tir.Terminator) error {
	switch term.Kind {
	case tir.TermGoto, tir.TermBranchIf, tir.TermUnreachable:
		return nil
	case tir.TermReturn:
		if tt.Info(fn.ReturnType).Kind != tir.TypeUnit {
			if term.ReturnVal == nil {
				return newCapErr("WON-INT-001", fmt.Sprintf("非 unit 函数 '%s' 缺少 ReturnVal 操作数", fn.Name), term.Span, "TIR 验证边界禁止为缺失返回值的非 unit 函数合成默认返回值。")
			}
			if _, err := MapType(tt, fn.ReturnType, PosReturn); err != nil {
				return newCapErr("WON-GEN-002", err.Error(), term.Span, "")
			}
		}
		return nil
	case tir.TermSwitch:
		if term.SwitchKind == tir.SwitchLoopIter {
			return newCapErr("WON-GEN-002", "循环迭代器 'for' 尚未在当前 C11 后端实现", term.Span, "")
		}
		condBase := derefTypeIfRef(tt, term.Cond.Type)
		ci := tt.Info(condBase)
		switch term.SwitchKind {
		case tir.SwitchTry:
			if ci.Kind != tir.TypeEnum || len(ci.Variants) < 2 {
				return newCapErr("WON-GEN-002", fmt.Sprintf("'?' 错误传播操作符要求至少 2 个变体的枚举，实际为 '%s'", tt.Format(term.Cond.Type)), term.Span, "")
			}
			return nil
		case tir.SwitchMatch, tir.SwitchGeneric:
			if ci.Kind != tir.TypeEnum && ci.Kind != tir.TypeInt && ci.Kind != tir.TypeBool && ci.Kind != tir.TypeChar && ci.Kind != tir.TypeStruct && ci.Kind != tir.TypeTuple {
				return newCapErr("WON-GEN-002", fmt.Sprintf("match 目标类型 '%s' 尚未在当前 C11 后端实现", tt.Format(term.Cond.Type)), term.Span, "")
			}
			return nil
		default:
			return newCapErr("WON-GEN-002", fmt.Sprintf("多路控制流 '%v' 尚未在当前 C11 后端实现", term.SwitchKind), term.Span, "")
		}
	default:
		return newCapErr("WON-GEN-002", fmt.Sprintf("未知控制流终结器 (TermKind=%d)", term.Kind), term.Span, "")
	}
}

func validateSupportedDropGlue(tt *tir.TypeTable, g tir.DropGlueDesc, glueMap map[tir.DropGlueID]tir.DropGlueDesc, validFFINS map[string]bool, visiting map[tir.DropGlueID]bool) error {
	if visiting[g.ID] {
		return nil
	}
	visiting[g.ID] = true
	defer delete(visiting, g.ID)

	if _, err := MapType(tt, g.Type, PosLocal); err != nil {
		return newCapErr("WON-GEN-002", fmt.Sprintf("析构胶水 '%s' 目标类型 %s 不支持降低至 ISO C11: %v", g.CanonicalName, tt.Format(g.Type), err), tir.SourceSpan{}, "")
	}

	switch g.Kind {
	case tir.GlueForeignResource:
		ns, cSym, ok := splitForeignSymbol(g.ReleaseSymbol)
		if !ok || !isValidCIdentifier(cSym) || !validFFINS[ns] {
			return newCapErr("WON-GEN-002", fmt.Sprintf("外部资源析构胶水 '%s' 缺少有效 C 释放符号或未绑定合法头文件 (%q)", g.CanonicalName, g.ReleaseSymbol), tir.SourceSpan{}, "受管外部资源必须通过已导入头文件与 FFI 契约目录明确提供可调用 C 释放符号。")
		}
		return nil
	case tir.GlueStruct:
		for _, f := range g.Fields {
			if f.GlueID == tir.InvalidDropGlue {
				continue
			}
			child, ok := glueMap[f.GlueID]
			if !ok {
				return newCapErr("WON-GEN-002", fmt.Sprintf("结构体析构胶水 '%s' 引用缺失字段胶水 g%d", g.CanonicalName, f.GlueID), tir.SourceSpan{}, "")
			}
			if err := validateSupportedDropGlue(tt, child, glueMap, validFFINS, visiting); err != nil {
				return err
			}
		}
		return nil
	case tir.GlueTuple:
		for _, el := range g.TupleElems {
			if el.GlueID == tir.InvalidDropGlue {
				continue
			}
			child, ok := glueMap[el.GlueID]
			if !ok {
				return newCapErr("WON-GEN-002", fmt.Sprintf("元组析构胶水 '%s' 引用缺失元素胶水 g%d", g.CanonicalName, el.GlueID), tir.SourceSpan{}, "")
			}
			if err := validateSupportedDropGlue(tt, child, glueMap, validFFINS, visiting); err != nil {
				return err
			}
		}
		return nil
	case tir.GlueArray:
		if g.ElemGlueID == tir.InvalidDropGlue {
			return nil
		}
		child, ok := glueMap[g.ElemGlueID]
		if !ok {
			return newCapErr("WON-GEN-002", fmt.Sprintf("数组析构胶水 '%s' 引用缺失元素胶水 g%d", g.CanonicalName, g.ElemGlueID), tir.SourceSpan{}, "")
		}
		return validateSupportedDropGlue(tt, child, glueMap, validFFINS, visiting)
	case tir.GlueEnum:
		for _, v := range g.Variants {
			for _, el := range v.TupleElems {
				if el.GlueID == tir.InvalidDropGlue {
					continue
				}
				child, ok := glueMap[el.GlueID]
				if !ok {
					return newCapErr("WON-GEN-002", fmt.Sprintf("枚举析构胶水 '%s' 变体 '%s' 引用缺失元素胶水 g%d", g.CanonicalName, v.Name, el.GlueID), tir.SourceSpan{}, "")
				}
				if err := validateSupportedDropGlue(tt, child, glueMap, validFFINS, visiting); err != nil {
					return err
				}
			}
			for _, f := range v.StructFields {
				if f.GlueID == tir.InvalidDropGlue {
					continue
				}
				child, ok := glueMap[f.GlueID]
				if !ok {
					return newCapErr("WON-GEN-002", fmt.Sprintf("枚举析构胶水 '%s' 变体 '%s' 引用缺失字段胶水 g%d", g.CanonicalName, v.Name, f.GlueID), tir.SourceSpan{}, "")
				}
				if err := validateSupportedDropGlue(tt, child, glueMap, validFFINS, visiting); err != nil {
					return err
				}
			}
		}
		return nil
	case tir.GlueString:
		return nil
	default:
		return newCapErr("WON-GEN-002", fmt.Sprintf("析构胶水 '%s' (%s, 类型 %s) 尚未在当前 C11 后端实现原生析构支持", g.CanonicalName, g.Kind, tt.Format(g.Type)), tir.SourceSpan{}, "自定义 impl Drop 析构留待后续阶段启用。")
	}
}

func validatePlaceProjectionsCaps(tt *tir.TypeTable, place tir.Place, span tir.SourceSpan) error {
	curType := place.RootType
	for _, p := range place.Projections {
		curBase := derefTypeIfRef(tt, curType)
		switch p.Kind {
		case tir.ProjDeref, tir.ProjField, tir.ProjTupleField, tir.ProjDowncast, tir.ProjConstIndex:
			// 受支持的位置投影链
		case tir.ProjDynIndex:
			if tt.Info(curBase).Kind != tir.TypeArray || (p.IndexValue == tir.InvalidValue && p.IndexLocal == tir.InvalidLocal) {
				return newCapErr("WON-GEN-002", "动态数组下标仅支持带有边界检查的定长数组 '[T; N][idx]'", span, "")
			}
		default:
			return newCapErr("WON-GEN-002", fmt.Sprintf("不支持的存储位置投影 (%v)", p.Kind), span, "")
		}
		if p.Kind != tir.ProjDowncast && p.ElemType != tir.InvalidType {
			curType = p.ElemType
		}
	}
	return nil
}

func isSupportedArrayType(tt *tir.TypeTable, tid tir.TypeID) bool {
	ti := tt.Info(tid)
	return ti.Kind == tir.TypeArray && ti.ArrayLen >= 0 && !hasUnsupportedNativeFeature(tt, tid, make(map[tir.TypeID]bool, 8))
}

func derefTypeIfRef(tt *tir.TypeTable, tid tir.TypeID) tir.TypeID {
	for i := 0; i < 8; i++ {
		ti := tt.Info(tid)
		if ti.Kind == tir.TypeRef && tt.Info(ti.Elem).Kind != tir.TypeStr && tt.Info(ti.Elem).Kind != tir.TypeSlice {
			tid = ti.Elem
			continue
		}
		break
	}
	return tid
}

func isFFISafeType(tt *tir.TypeTable, tid tir.TypeID, isArg bool) bool {
	ti := tt.Info(tid)
	if IsScalarOrChar(ti) {
		return true
	}
	switch ti.Kind {
	case tir.TypeRawPtr:
		return true
	case tir.TypeStruct:
		return ti.IsOpaqueForeign && ti.ForeignNamespace != ""
	case tir.TypeRef:
		ei := tt.Info(ti.Elem)
		if ei.Kind == tir.TypeStr {
			return false
		}
		if ei.Kind == tir.TypeSlice && isArg {
			return tt.Info(ei.Elem).Kind == tir.TypeInt && !tt.Info(ei.Elem).Signed && tt.Info(ei.Elem).Bits == 8
		}
		return IsScalarOrChar(ei) || (ei.Kind == tir.TypeStruct && ei.IsOpaqueForeign) || ei.Kind == tir.TypeUnit || isSupportedArrayType(tt, ti.Elem)
	default:
		return false
	}
}

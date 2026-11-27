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

	// 1. 校验 DropGlue 目录：目前仅开放已完成契约验证与 C 符号解析的 GlueForeignResource
	glueMap := make(map[tir.DropGlueID]tir.DropGlueDesc, len(prog.DropGlues))
	for _, g := range prog.DropGlues {
		glueMap[g.ID] = g
		if g.Kind != tir.GlueForeignResource {
			return newCapErr(
				"WON-GEN-002",
				fmt.Sprintf("析构胶水 '%s' (%s, 类型 %s) 尚未在当前 C11 后端实现原生析构支持", g.CanonicalName, g.Kind, tt.Format(g.Type)),
				firstSpan(prog),
				"Phase 2C.1 优先验证经过完整 FFI 契约认证的外部不透明资源释放；String 与常规 ADT 析构需要独立布局与分配器契约。",
			)
		}
		ns, cSym, ok := splitForeignSymbol(g.ReleaseSymbol)
		if !ok || !isValidCIdentifier(cSym) || !validFFINS[ns] {
			return newCapErr(
				"WON-GEN-002",
				fmt.Sprintf("外部资源析构胶水 '%s' 缺少有效 C 释放符号或未绑定合法头文件 (%q)", g.CanonicalName, g.ReleaseSymbol),
				firstSpan(prog),
				"受管外部资源必须通过已导入头文件与 FFI 契约目录明确提供可调用 C 释放符号。",
			)
		}
		if _, err := MapType(tt, g.Type, PosLocal); err != nil {
			return newCapErr(
				"WON-GEN-002",
				fmt.Sprintf("外部资源胶水 '%s' 目标类型 %s 暂不支持降低至 ISO C11: %v", g.CanonicalName, tt.Format(g.Type), err),
				firstSpan(prog),
				"",
			)
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

func isScalarArrayType(tt *tir.TypeTable, tid tir.TypeID) bool {
	ti := tt.Info(tid)
	return ti.Kind == tir.TypeArray && ti.ArrayLen >= 0 && IsScalarOrChar(tt.Info(ti.Elem))
}

func validateFunctionCaps(prog *tir.Program, fn *tir.Function, glueMap map[tir.DropGlueID]tir.DropGlueDesc, validFFINS map[string]bool) error {
	tt := prog.Types

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
		if isScalarArrayType(tt, ld.Type) {
			continue
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
				lt.Kind == tir.TypeUnit || lt.Kind == tir.TypeString || lt.Kind == tir.TypeEnum || lt.Kind == tir.TypeStruct {
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
		if tt.Info(inst.Type).Kind != tir.TypeUnit && !isScalarArrayType(tt, inst.Type) {
			return checkNonReturnType(inst.Type, "汇合临时变量赋值")
		}
		return nil

	case tir.OpInit, tir.OpAssign:
		if len(inst.Place.Projections) > 0 {
			for _, p := range inst.Place.Projections {
				if p.Kind != tir.ProjDeref {
					return newCapErr("WON-GEN-002", "成员字段或数组下标左值赋值尚未在 C11 后端实现", inst.Span, "")
				}
			}
		}
		if isScalarArrayType(tt, inst.Type) {
			if inst.Src.Kind != tir.OperandValue {
				return newCapErr("WON-GEN-002", "定长数组仅支持重复字面量初始化 [v; N]", inst.Span, "")
			}
			srcDef, ok := defInst[inst.Src.Value]
			if !ok || srcDef.Kind != tir.OpAggregateConstruct || srcDef.AggKind != tir.AggArrayRepeat {
				return newCapErr("WON-GEN-002", "定长数组仅支持重复字面量初始化 [v; N]", inst.Span, "")
			}
			return nil
		}
		if sliceAliasLocals[inst.Place.Local] {
			return nil
		}
		return checkNonReturnType(inst.Type, "变量初始化或赋值")

	case tir.OpLoad, tir.OpCopy, tir.OpMove:
		if len(inst.Place.Projections) > 0 {
			for _, p := range inst.Place.Projections {
				if p.Kind != tir.ProjDeref {
					return newCapErr("WON-GEN-002", "成员字段或数组下标读取尚未在 C11 后端实现", inst.Span, "")
				}
			}
		}
		if sliceAliasLocals[inst.Place.Local] && inst.Dest == tir.InvalidValue {
			return nil
		}
		if isScalarArrayType(tt, inst.Type) {
			return newCapErr("WON-GEN-002", fmt.Sprintf("定长数组类型 '%s' 不能按值复制或传递", tt.Format(inst.Type)), inst.Span, "")
		}
		return checkNonReturnType(inst.Type, "存储读取")

	case tir.OpStore:
		for _, p := range inst.Place.Projections {
			if p.Kind != tir.ProjDeref {
				return newCapErr("WON-GEN-002", "成员字段或数组下标写入尚未在 C11 后端实现", inst.Span, "")
			}
		}
		return checkNonReturnType(inst.Type, "指针解引用存储")

	case tir.OpBorrow:
		if len(inst.Place.Projections) > 0 {
			for _, p := range inst.Place.Projections {
				if p.Kind != tir.ProjDeref {
					return newCapErr("WON-GEN-002", "成员字段或数组元素引用借用尚未在 C11 后端实现", inst.Span, "")
				}
			}
		}
		ti := tt.Info(inst.Type)
		if ti.Kind != tir.TypeRef {
			return newCapErr("WON-GEN-002", "借用目标必须为引用类型", inst.Span, "")
		}
		elem := tt.Info(ti.Elem)
		if isScalarArrayType(tt, ti.Elem) || isU8SliceRef(tt, inst.Type) || (elem.Kind == tir.TypeStr && !ti.IsMut) || IsScalarOrChar(elem) || (elem.Kind == tir.TypeStruct && elem.IsOpaqueForeign) || elem.Kind == tir.TypeRawPtr {
			return nil
		}
		return newCapErr("WON-GEN-002", fmt.Sprintf("引用类型 '%s' 尚不支持当前 C11 后端", tt.Format(inst.Type)), inst.Span, "")

	case tir.OpAggregateConstruct:
		if inst.AggKind == tir.AggArrayRepeat && isScalarArrayType(tt, inst.Type) && inst.RepeatLen >= 0 {
			return nil
		}
		return newCapErr("WON-GEN-002", fmt.Sprintf("聚合值构造 (%s) 尚未在当前 C11 后端实现", inst.AggKind), inst.Span, "")

	case tir.OpExtract:
		if inst.ExtractProj.Kind == tir.ProjDeref {
			return checkNonReturnType(inst.Type, "指针解引用")
		}
		return newCapErr("WON-GEN-002", fmt.Sprintf("成员抽取 (%v) 尚未在当前 C11 后端实现", inst.ExtractProj.Kind), inst.Span, "")

	case tir.OpCall:
		if tt.Info(inst.Type).Kind != tir.TypeUnit {
			if _, err := MapType(tt, inst.Type, PosValue); err != nil {
				return newCapErr("WON-GEN-002", fmt.Sprintf("调用 '%s' 返回类型不支持 C11: %v", inst.CalleeName, err), inst.Span, "")
			}
		}
		return nil

	case tir.OpForeignCall:
		if tt.Info(inst.Type).Kind != tir.TypeUnit {
			if _, err := MapType(tt, inst.Type, PosValue); err != nil {
				return newCapErr("WON-GEN-002", fmt.Sprintf("FFI 调用 '%s::%s' 返回类型不支持 C11: %v", inst.CalleeNamespace, inst.CalleeName, err), inst.Span, "")
			}
		}
		return nil

	case tir.OpBuiltinCall:
		switch inst.BuiltinKind {
		case tir.BuiltinPrintln, tir.BuiltinBytesEqual:
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
			if argTi.Kind == tir.TypeRef && (tt.Info(argTi.Elem).Kind == tir.TypeStr || isScalarArrayType(tt, argTi.Elem) || isU8SliceRef(tt, inst.Args[0].Type)) {
				return nil
			}
			return newCapErr("WON-GEN-002", fmt.Sprintf("当前 C11 后端暂不支持在 '%s' 上调用 .as_ptr()", tt.Format(inst.Args[0].Type)), inst.Span, "")
		case tir.BuiltinAsMutPtr:
			if len(inst.Args) != 1 {
				return newCapErr("WON-GEN-002", "内置 .as_mut_ptr() 参数数量非法", inst.Span, "")
			}
			argTi := tt.Info(inst.Args[0].Type)
			if argTi.Kind == tir.TypeRef && argTi.IsMut && isScalarArrayType(tt, argTi.Elem) {
				return nil
			}
			return newCapErr("WON-GEN-002", fmt.Sprintf("当前 C11 后端仅支持在可变局部标量数组上调用 .as_mut_ptr()，不支持 '%s'", tt.Format(inst.Args[0].Type)), inst.Span, "")
		case tir.BuiltinLen:
			if len(inst.Args) != 1 {
				return newCapErr("WON-GEN-002", "内置 .len() 参数数量非法", inst.Span, "")
			}
			argTi := tt.Info(inst.Args[0].Type)
			if argTi.Kind == tir.TypeRef && isScalarArrayType(tt, argTi.Elem) {
				return nil
			}
			return newCapErr("WON-GEN-002", fmt.Sprintf("当前 C11 后端仅支持定长标量数组 [T; N].len()，不支持 '%s'", tt.Format(inst.Args[0].Type)), inst.Span, "")
		default:
			return newCapErr("WON-GEN-002", fmt.Sprintf("内置函数或方法 '%s' 尚未在当前 C11 后端实现", inst.CalleeName), inst.Span, "")
		}

	case tir.OpOwnershipTransfer:
		return nil

	case tir.OpDrop, tir.OpCondDrop:
		g, ok := glueMap[inst.GlueID]
		if !ok || g.Kind != tir.GlueForeignResource {
			return newCapErr("WON-GEN-002", fmt.Sprintf("析构操作 g%d 尚未在当前 C11 后端实现", inst.GlueID), inst.Span, "")
		}
		ns, cSym, ok := splitForeignSymbol(g.ReleaseSymbol)
		if !ok || !isValidCIdentifier(cSym) || !validFFINS[ns] {
			return newCapErr("WON-GEN-002", fmt.Sprintf("外部资源释放符号 '%s' 非法或缺少有效头文件绑定", g.ReleaseSymbol), inst.Span, "")
		}
		return nil

	default:
		return newCapErr("WON-GEN-002", fmt.Sprintf("TIR 指令 '%s' 尚未在当前 C11 后端实现", inst.Kind), inst.Span, "")
	}
}

func validateTerminatorCaps(tt *tir.TypeTable, fn *tir.Function, term tir.Terminator) error {
	switch term.Kind {
	case tir.TermGoto, tir.TermBranchIf, tir.TermUnreachable:
		return nil
	case tir.TermReturn:
		if term.ReturnVal != nil && term.ReturnVal.Kind == tir.OperandValue {
			// 显式 unit 字面量已由 OpConst(ConstUnit) 规则拦截；此处的 Value/ConstUnit 允许 void 函数尾调用及 unsafe void 块返回
		}
		if tt.Info(fn.ReturnType).Kind != tir.TypeUnit && term.ReturnVal != nil {
			if _, err := MapType(tt, fn.ReturnType, PosReturn); err != nil {
				return newCapErr("WON-GEN-002", err.Error(), term.Span, "")
			}
		}
		return nil
	case tir.TermSwitch:
		return newCapErr("WON-GEN-002", fmt.Sprintf("多路控制流 '%v' (match / for / ?) 尚未在当前 C11 后端实现", term.SwitchKind), term.Span, "")
	default:
		return newCapErr("WON-GEN-002", fmt.Sprintf("未知控制流终结器 (TermKind=%d)", term.Kind), term.Span, "")
	}
}

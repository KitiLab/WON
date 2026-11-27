package tir

import (
	"fmt"
	"strings"
)

// VerifyError 表示 TIR 独立结构与类型不变量验证器检出的一处非法 IR 缺陷。
type VerifyError struct {
	Function string
	Block    BlockID
	StmtIdx  int
	Message  string
}

func (e VerifyError) Error() string {
	if e.Function == "" {
		return "TIR 校验失败: " + e.Message
	}
	if e.Block != InvalidBlock && e.StmtIdx >= 0 {
		return fmt.Sprintf("TIR 校验失败 (函数 %s, bb%d#%d): %s", e.Function, e.Block, e.StmtIdx, e.Message)
	}
	if e.Block != InvalidBlock {
		return fmt.Sprintf("TIR 校验失败 (函数 %s, bb%d): %s", e.Function, e.Block, e.Message)
	}
	return fmt.Sprintf("TIR 校验失败 (函数 %s): %s", e.Function, e.Message)
}

// VerifyErrorSummary 将多条验证错误聚合为单一 error；全通过时返回 nil。
func VerifyErrorSummary(errs []VerifyError) error {
	if len(errs) == 0 {
		return nil
	}
	var sb strings.Builder
	for i, e := range errs {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(e.Error())
	}
	return fmt.Errorf("%s", sb.String())
}

// Verify 对生成或反序列化的 TIR 程序执行后端无关的结构、CFG、类型、所有权、Drop 与 FFI 契约完整性校验。
// 所有块（含可达与不可达块）均按 Fail-Closed 规则严格核验，绝不静默容忍畸形中间表示。
func Verify(prog *Program) []VerifyError {
	var errs []VerifyError
	addErr := func(fnName string, bb BlockID, idx int, format string, args ...any) {
		errs = append(errs, VerifyError{
			Function: fnName,
			Block:    bb,
			StmtIdx:  idx,
			Message:  fmt.Sprintf(format, args...),
		})
	}

	if prog == nil {
		addErr("", InvalidBlock, -1, "Program 为空")
		return errs
	}
	if !prog.OwnershipOK || !prog.DropOK {
		addErr("", InvalidBlock, -1, "程序尚未通过前置所有权或 Drop 验证 (OwnershipOK=%v, DropOK=%v)", prog.OwnershipOK, prog.DropOK)
	}
	if prog.Types == nil || prog.Types.Len() < 1 {
		addErr("", InvalidBlock, -1, "TIR TypeTable 缺失或未初始化")
		return errs
	}

	// 1. 校验 DropGlues 目录唯一性与引用完整性
	glueSet := make(map[DropGlueID]DropGlueDesc, len(prog.DropGlues))
	for _, g := range prog.DropGlues {
		if g.ID == InvalidDropGlue {
			addErr("", InvalidBlock, -1, "DropGlue 存在非法 ID=0 (%q)", g.CanonicalName)
			continue
		}
		if _, dup := glueSet[g.ID]; dup {
			addErr("", InvalidBlock, -1, "DropGlue ID g%d 重复定义", g.ID)
			continue
		}
		if g.Kind == GlueNone || !prog.Types.Valid(g.Type) {
			addErr("", InvalidBlock, -1, "DropGlue g%d (%s) 类型或种类非法 (kind=%s type=t%d)", g.ID, g.CanonicalName, g.Kind, g.Type)
		}
		glueSet[g.ID] = g
	}
	for _, g := range prog.DropGlues {
		if g.ElemGlueID != InvalidDropGlue {
			if _, ok := glueSet[g.ElemGlueID]; !ok {
				addErr("", InvalidBlock, -1, "数组 DropGlue g%d 引用未声明元素胶水 g%d", g.ID, g.ElemGlueID)
			}
		}
		for _, f := range g.Fields {
			if f.GlueID == InvalidDropGlue {
				continue
			}
			if _, ok := glueSet[f.GlueID]; !ok {
				addErr("", InvalidBlock, -1, "结构体 DropGlue g%d 字段 '%s' 引用未声明胶水 g%d", g.ID, f.Name, f.GlueID)
			}
		}
		for _, el := range g.TupleElems {
			if el.GlueID == InvalidDropGlue {
				continue
			}
			if _, ok := glueSet[el.GlueID]; !ok {
				addErr("", InvalidBlock, -1, "元组 DropGlue g%d 元素 #%d 引用未声明胶水 g%d", g.ID, el.Index, el.GlueID)
			}
		}
	}

	// 2. 校验 FFIContracts 与 FFICatalogs 一致性
	contractSet := make(map[ContractID]FFIContractDesc, len(prog.FFIContracts))
	for _, c := range prog.FFIContracts {
		if c.ID == InvalidContract {
			addErr("", InvalidBlock, -1, "FFIContract 存在非法 ID=0 (%q)", c.ContractKey)
			continue
		}
		if _, dup := contractSet[c.ID]; dup {
			addErr("", InvalidBlock, -1, "FFIContract ID c%d 重复定义", c.ID)
			continue
		}
		if c.ContractKey == "" || c.Namespace == "" || c.Function == "" || c.SchemaVersion == "" || c.HeaderPath == "" {
			addErr("", InvalidBlock, -1, "FFIContract c%d 缺少必填信封或函数标识元数据", c.ID)
		}
		contractSet[c.ID] = c
	}

	// 3. 校验全部函数签名及 ID 唯一性
	funcSet := make(map[FunctionID]*Function, len(prog.Functions))
	for _, fn := range prog.Functions {
		if fn == nil {
			addErr("", InvalidBlock, -1, "Functions 列表包含 nil 函数项")
			continue
		}
		if fn.ID == InvalidFunc {
			addErr(fn.Name, InvalidBlock, -1, "函数分配了非法 FunctionID=0")
			continue
		}
		if _, dup := funcSet[fn.ID]; dup {
			addErr(fn.Name, InvalidBlock, -1, "FunctionID @f%d 重复定义", fn.ID)
			continue
		}
		if !prog.Types.Valid(fn.ReturnType) {
			addErr(fn.Name, InvalidBlock, -1, "函数返回类型 t%d 无效", fn.ReturnType)
		}
		for pi, pt := range fn.ParamTypes {
			if !prog.Types.Valid(pt) {
				addErr(fn.Name, InvalidBlock, -1, "函数参数 #%d 类型 t%d 无效", pi, pt)
			}
		}
		funcSet[fn.ID] = fn
	}

	// 4. 逐函数核验局部变量、寄存器值、DropFlags、基本块 CFG、指令与终结器
	for _, fn := range prog.Functions {
		if fn != nil {
			verifyFunction(prog, fn, funcSet, glueSet, contractSet, addErr)
		}
	}
	return errs
}

func verifyFunction(
	prog *Program,
	fn *Function,
	funcSet map[FunctionID]*Function,
	glueSet map[DropGlueID]DropGlueDesc,
	contractSet map[ContractID]FFIContractDesc,
	addErr func(fnName string, bb BlockID, idx int, format string, args ...any),
) {
	t := prog.Types

	// 4.1 局部变量表唯一性
	localSet := make(map[LocalID]LocalDecl, len(fn.Locals))
	for _, l := range fn.Locals {
		if l.ID == InvalidLocal {
			addErr(fn.Name, InvalidBlock, -1, "局部变量 '%s' 分配了非法 LocalID=0", l.Name)
			continue
		}
		if _, dup := localSet[l.ID]; dup {
			addErr(fn.Name, InvalidBlock, -1, "LocalID _l%d 重复定义", l.ID)
			continue
		}
		if !t.Valid(l.Type) {
			addErr(fn.Name, InvalidBlock, -1, "局部变量 _l%d (%s) 引用无效类型 t%d", l.ID, l.Name, l.Type)
		}
		localSet[l.ID] = l
	}

	// 4.2 寄存器值定义声明唯一性
	valDeclSet := make(map[ValueID]ValueDecl, len(fn.Values))
	for _, v := range fn.Values {
		if v.ID == InvalidValue {
			addErr(fn.Name, InvalidBlock, -1, "ValueDecl 分配了非法 ValueID=0")
			continue
		}
		if _, dup := valDeclSet[v.ID]; dup {
			addErr(fn.Name, InvalidBlock, -1, "ValueID %%%d 在函数值表中重复定义", v.ID)
			continue
		}
		if !t.Valid(v.Type) {
			addErr(fn.Name, InvalidBlock, -1, "ValueID %%%d 引用无效类型 t%d", v.ID, v.Type)
		}
		valDeclSet[v.ID] = v
	}

	// 4.3 DropFlag 声明唯一性与胶水引用校验
	flagSet := make(map[DropFlagID]DropFlag, len(fn.DropFlags))
	for _, df := range fn.DropFlags {
		if df.ID == InvalidDropFlag {
			addErr(fn.Name, InvalidBlock, -1, "DropFlag 存在非法 ID=0")
			continue
		}
		if _, dup := flagSet[df.ID]; dup {
			addErr(fn.Name, InvalidBlock, -1, "DropFlag df%d 重复声明", df.ID)
			continue
		}
		if !t.Valid(df.Type) {
			addErr(fn.Name, InvalidBlock, -1, "DropFlag df%d 引用无效类型 t%d", df.ID, df.Type)
		}
		if _, ok := glueSet[df.GlueID]; !ok {
			addErr(fn.Name, InvalidBlock, -1, "DropFlag df%d 引用未声明 DropGlue g%d", df.ID, df.GlueID)
		}
		if df.OwnerLocal != InvalidLocal {
			if _, ok := localSet[df.OwnerLocal]; !ok {
				addErr(fn.Name, InvalidBlock, -1, "DropFlag df%d 引用未声明局部变量 _l%d", df.ID, df.OwnerLocal)
			}
		}
		flagSet[df.ID] = df
	}

	// 4.4 基本块索引、入口块与防悬挂目标校验
	if len(fn.Blocks) == 0 {
		addErr(fn.Name, InvalidBlock, -1, "函数缺少任何基本块")
		return
	}
	blockSet := make(map[BlockID]*BasicBlock, len(fn.Blocks))
	for _, b := range fn.Blocks {
		if b == nil {
			addErr(fn.Name, InvalidBlock, -1, "函数基本块表包含 nil 项")
			continue
		}
		if b.ID < 0 {
			addErr(fn.Name, b.ID, -1, "基本块 ID 不能为负 (%d)", b.ID)
			continue
		}
		if _, dup := blockSet[b.ID]; dup {
			addErr(fn.Name, b.ID, -1, "基本块 ID bb%d 重复", b.ID)
			continue
		}
		blockSet[b.ID] = b
	}
	if _, ok := blockSet[fn.EntryBlock]; !ok {
		addErr(fn.Name, fn.EntryBlock, -1, "函数入口块 bb%d 不存在", fn.EntryBlock)
	}

	verifyPlace := func(bb BlockID, idx int, p Place, label string) {
		if p.Local == InvalidLocal {
			addErr(fn.Name, bb, idx, "%s 缺少有效 LocalID", label)
			return
		}
		ld, ok := localSet[p.Local]
		if !ok {
			addErr(fn.Name, bb, idx, "%s 引用未声明局部变量 _l%d", label, p.Local)
			return
		}
		if p.RootType != ld.Type {
			addErr(fn.Name, bb, idx, "%s 根类型 t%d 与局部声明类型 t%d 不一致", label, p.RootType, ld.Type)
		}
		if !t.Valid(p.ElemType) {
			addErr(fn.Name, bb, idx, "%s 末级类型 t%d 无效", label, p.ElemType)
		}
		curType := p.RootType
		for pi, proj := range p.Projections {
			if !t.Valid(proj.ElemType) {
				addErr(fn.Name, bb, idx, "%s 第 #%d 级投影类型 t%d 无效", label, pi, proj.ElemType)
			}
			if proj.Kind == ProjDeref {
				ci := t.Info(curType)
				if ci.Kind != TypeRef && ci.Kind != TypeRawPtr {
					addErr(fn.Name, bb, idx, "%s 第 #%d 级解引用投影作用于非指针/非引用类型 %s", label, pi, t.Format(curType))
				}
			}
			curType = proj.ElemType
		}
	}

	verifyOperand := func(bb BlockID, idx int, op Operand, label string) {
		if op.Kind == OperandInvalid {
			addErr(fn.Name, bb, idx, "%s 为无效操作数", label)
			return
		}
		if !t.Valid(op.Type) {
			addErr(fn.Name, bb, idx, "%s 引用无效类型 t%d", label, op.Type)
		}
		switch op.Kind {
		case OperandValue:
			vd, ok := valDeclSet[op.Value]
			if !ok {
				addErr(fn.Name, bb, idx, "%s 引用未声明 ValueID %%%d", label, op.Value)
			} else if vd.Type != op.Type {
				addErr(fn.Name, bb, idx, "%s 操作数类型 t%d 与寄存器声明类型 t%d 不一致", label, op.Type, vd.Type)
			}
		case OperandPlace:
			verifyPlace(bb, idx, op.Place, label+".Place")
		case OperandFunc:
			if target, ok := funcSet[op.FuncID]; !ok {
				addErr(fn.Name, bb, idx, "%s 引用未声明 FunctionID @f%d", label, op.FuncID)
			} else if target.ReturnType != InvalidType && op.Type != InvalidType {
				ti := t.Info(op.Type)
				if ti.Kind != TypeFn {
					addErr(fn.Name, bb, idx, "%s 函数引用操作数必须具有 Fn 类型，实际为 %s", label, t.Format(op.Type))
				}
			}
		}
	}

	// 记录指令层面对非 JoinTemp 寄存器的单次静态赋值约束
	definedVals := make(map[ValueID]bool, len(fn.Values))
	recordDef := func(bb BlockID, idx int, dest ValueID, typ TypeID, allowMulti bool) {
		if dest == InvalidValue {
			return
		}
		vd, ok := valDeclSet[dest]
		if !ok {
			addErr(fn.Name, bb, idx, "指令结果写入未在函数值表声明的寄存器 %%%d", dest)
			return
		}
		if vd.Type != typ {
			addErr(fn.Name, bb, idx, "指令结果寄存器 %%%d 类型 t%d 与声明类型 t%d 不一致", dest, typ, vd.Type)
		}
		if definedVals[dest] && (!allowMulti || !vd.IsJoinTemp) {
			addErr(fn.Name, bb, idx, "ValueID %%%d 发生重复静态定义", dest)
		}
		definedVals[dest] = true
	}

	for _, b := range fn.Blocks {
		if b == nil {
			continue
		}
		for idx, inst := range b.Instructions {
			if inst.Kind != OpScopeEnter && inst.Kind != OpScopeExit && !(inst.Kind == OpStorageDead && len(inst.Locals) > 0) {
				if !t.Valid(inst.Type) {
					addErr(fn.Name, b.ID, idx, "指令 %s 缺少有效类型 (t%d)", inst.Kind, inst.Type)
				}
			}
			allowMultiDef := inst.Kind == OpValueAssign
			recordDef(b.ID, idx, inst.Dest, inst.Type, allowMultiDef)

			switch inst.Kind {
			case OpStorageLive:
				if _, ok := localSet[inst.Local]; !ok {
					addErr(fn.Name, b.ID, idx, "StorageLive 引用未声明局部变量 _l%d", inst.Local)
				}
			case OpStorageDead:
				if inst.Local != InvalidLocal {
					if _, ok := localSet[inst.Local]; !ok {
						addErr(fn.Name, b.ID, idx, "StorageDead 引用未声明局部变量 _l%d", inst.Local)
					}
				}
				for _, lid := range inst.Locals {
					if _, ok := localSet[lid]; !ok {
						addErr(fn.Name, b.ID, idx, "StorageDead 引用未声明作用域局部 _l%d", lid)
					}
				}
			case OpScopeEnter:
				// 作用域进入边界无需引用局部列表
			case OpScopeExit:
				for _, lid := range inst.Locals {
					if _, ok := localSet[lid]; !ok {
						addErr(fn.Name, b.ID, idx, "ScopeExit 引用未声明局部变量 _l%d", lid)
					}
				}
			case OpInit, OpAssign:
				verifyPlace(b.ID, idx, inst.Place, inst.Kind.String()+".Place")
				if inst.Src.Kind != OperandInvalid {
					verifyOperand(b.ID, idx, inst.Src, inst.Kind.String()+".Src")
				}
			case OpLoad:
				if inst.Dest == InvalidValue {
					addErr(fn.Name, b.ID, idx, "Load 缺少目的寄存器")
				}
				verifyPlace(b.ID, idx, inst.Place, "Load.Place")
			case OpStore:
				verifyPlace(b.ID, idx, inst.Place, "Store.Place")
				verifyOperand(b.ID, idx, inst.Src, "Store.Src")
			case OpComputeAddr:
				if inst.Dest == InvalidValue {
					addErr(fn.Name, b.ID, idx, "ComputeAddr 缺少目的寄存器")
				}
				verifyPlace(b.ID, idx, inst.TargetPlace, "ComputeAddr.Target")
				ti := t.Info(inst.Type)
				if ti.Kind != TypeRawPtr && ti.Kind != TypeRef {
					addErr(fn.Name, b.ID, idx, "ComputeAddr 结果类型必须为指针或引用，实际为 %s", t.Format(inst.Type))
				}
			case OpConst, OpStaticRef:
				if inst.Dest == InvalidValue {
					addErr(fn.Name, b.ID, idx, "%s 缺少目的寄存器", inst.Kind)
				}
			case OpUnary:
				if inst.Dest == InvalidValue {
					addErr(fn.Name, b.ID, idx, "Unary 缺少目的寄存器")
				}
				verifyOperand(b.ID, idx, inst.Src, "Unary.Src")
			case OpBinary:
				if inst.Dest == InvalidValue {
					addErr(fn.Name, b.ID, idx, "Binary 缺少目的寄存器")
				}
				verifyOperand(b.ID, idx, inst.Left, "Binary.Left")
				verifyOperand(b.ID, idx, inst.Right, "Binary.Right")
				if inst.BinaryOp.IsComparison() && t.Info(inst.Type).Kind != TypeBool {
					addErr(fn.Name, b.ID, idx, "比较运算 %s 结果类型必须为 bool，实际为 %s", inst.BinaryOp, t.Format(inst.Type))
				}
			case OpCast:
				if inst.Dest == InvalidValue {
					addErr(fn.Name, b.ID, idx, "Cast 缺少目的寄存器")
				}
				verifyOperand(b.ID, idx, inst.Src, "Cast.Src")
			case OpValueAssign:
				if inst.Dest == InvalidValue {
					addErr(fn.Name, b.ID, idx, "ValueAssign 缺少目的寄存器")
				}
				verifyOperand(b.ID, idx, inst.Src, "ValueAssign.Src")
				if vd, ok := valDeclSet[inst.Dest]; ok && !vd.IsJoinTemp {
					addErr(fn.Name, b.ID, idx, "ValueAssign 目标 %%%d 未标记为 JoinTemp", inst.Dest)
				}
			case OpAggregateConstruct:
				if inst.Dest == InvalidValue {
					addErr(fn.Name, b.ID, idx, "AggregateConstruct 缺少目的寄存器")
				}
				ti := t.Info(inst.Type)
				switch inst.AggKind {
				case AggStruct:
					if ti.Kind != TypeStruct {
						addErr(fn.Name, b.ID, idx, "Aggregate[Struct] 结果类型必须为 TypeStruct，实际为 %s", t.Format(inst.Type))
					} else if len(ti.Fields) > 0 {
						fieldTypeByName := make(map[string]TypeID, len(ti.Fields))
						for _, sf := range ti.Fields {
							fieldTypeByName[sf.Name] = sf.Type
						}
						for _, item := range inst.AggItems {
							verifyOperand(b.ID, idx, item.Value, "Aggregate.Field."+item.FieldName)
							if expectedT, exists := fieldTypeByName[item.FieldName]; !exists {
								addErr(fn.Name, b.ID, idx, "结构体 %s 构造包含未知字段 '%s'", ti.Name, item.FieldName)
							} else if item.Value.Type != expectedT && t.Info(expectedT).Kind != TypeTypeParam {
								addErr(fn.Name, b.ID, idx, "结构体字段 '%s.%s' 类型 t%d 与声明类型 t%d 不一致", ti.Name, item.FieldName, item.Value.Type, expectedT)
							}
						}
					}
				case AggTuple:
					if ti.Kind != TypeTuple && ti.Kind != TypeUnit {
						addErr(fn.Name, b.ID, idx, "Aggregate[Tuple] 结果类型必须为 TypeTuple/TypeUnit，实际为 %s", t.Format(inst.Type))
					} else if ti.Kind == TypeTuple {
						if len(inst.AggItems) != len(ti.Elems) {
							addErr(fn.Name, b.ID, idx, "元组构造元素数量 (%d) 与类型成员数量 (%d) 不一致", len(inst.AggItems), len(ti.Elems))
						}
						for ei, item := range inst.AggItems {
							verifyOperand(b.ID, idx, item.Value, fmt.Sprintf("Aggregate.Tuple.%d", ei))
							if ei < len(ti.Elems) && item.Value.Type != ti.Elems[ei] {
								addErr(fn.Name, b.ID, idx, "元组第 #%d 元素类型 t%d 与声明类型 t%d 不一致", ei, item.Value.Type, ti.Elems[ei])
							}
						}
					}
				case AggArray, AggArrayRepeat:
					if ti.Kind != TypeArray {
						addErr(fn.Name, b.ID, idx, "Aggregate[%s] 结果类型必须为 TypeArray，实际为 %s", inst.AggKind, t.Format(inst.Type))
					}
					for ei, item := range inst.AggItems {
						verifyOperand(b.ID, idx, item.Value, fmt.Sprintf("Aggregate.Array.%d", ei))
					}
				case AggEnumVariant:
					if ti.Kind != TypeEnum {
						addErr(fn.Name, b.ID, idx, "Aggregate[EnumVariant] 结果类型必须为 TypeEnum，实际为 %s", t.Format(inst.Type))
					}
					for ei, item := range inst.AggItems {
						verifyOperand(b.ID, idx, item.Value, fmt.Sprintf("Aggregate.VariantArg.%d", ei))
					}
				default:
					for ei, item := range inst.AggItems {
						verifyOperand(b.ID, idx, item.Value, fmt.Sprintf("Aggregate.Item.%d", ei))
					}
				}
			case OpExtract:
				if inst.Dest == InvalidValue {
					addErr(fn.Name, b.ID, idx, "Extract 缺少目的寄存器")
				}
				verifyOperand(b.ID, idx, inst.Src, "Extract.Src")
			case OpCopy, OpMove:
				verifyPlace(b.ID, idx, inst.Place, inst.Kind.String()+".Place")
				if inst.Place.ElemType != InvalidType && inst.Type != inst.Place.ElemType {
					addErr(fn.Name, b.ID, idx, "%s 指令类型 t%d 与源位置类型 t%d 不一致", inst.Kind, inst.Type, inst.Place.ElemType)
				}
			case OpBorrow:
				if inst.Dest == InvalidValue {
					addErr(fn.Name, b.ID, idx, "Borrow 缺少目的寄存器")
				}
				verifyPlace(b.ID, idx, inst.Place, "Borrow.Place")
				ti := t.Info(inst.Type)
				if ti.Kind != TypeRef && ti.Kind != TypeRawPtr {
					addErr(fn.Name, b.ID, idx, "Borrow 结果类型必须为引用或指针，实际为 %s", t.Format(inst.Type))
				}
				wantMut := inst.BorrowKind == BorrowMut || inst.BorrowKind == BorrowReborrowMut
				if ti.Kind == TypeRef && ti.IsMut != wantMut {
					addErr(fn.Name, b.ID, idx, "Borrow 可变性 (%s) 与结果类型 %s 不匹配", inst.BorrowKind, t.Format(inst.Type))
				}
				if (inst.BorrowKind == BorrowReborrowShared || inst.BorrowKind == BorrowReborrowMut) && inst.ParentPlace == nil {
					addErr(fn.Name, b.ID, idx, "Reborrow 缺少 ParentPlace 元数据")
				}
				if inst.ParentPlace != nil {
					verifyPlace(b.ID, idx, *inst.ParentPlace, "Borrow.ParentPlace")
				}
			case OpOwnershipTransfer:
				verifyPlace(b.ID, idx, inst.Place, "OwnershipTransfer.Place")
				if inst.ContractID == InvalidContract {
					addErr(fn.Name, b.ID, idx, "OwnershipTransfer 缺少有效 FFI ContractID")
				} else if _, ok := contractSet[inst.ContractID]; !ok {
					addErr(fn.Name, b.ID, idx, "OwnershipTransfer 引用未声明 ContractID c%d", inst.ContractID)
				}
				if inst.GlueID != InvalidDropGlue {
					if _, ok := glueSet[inst.GlueID]; !ok {
						addErr(fn.Name, b.ID, idx, "OwnershipTransfer 引用未声明 DropGlue g%d", inst.GlueID)
					}
				}
			case OpCall:
				targetFn, ok := funcSet[inst.CalleeFunc]
				if !ok {
					addErr(fn.Name, b.ID, idx, "Call 引用未声明 FunctionID @f%d (%q)", inst.CalleeFunc, inst.CalleeName)
				} else {
					if len(inst.Args) != len(targetFn.ParamTypes) {
						addErr(fn.Name, b.ID, idx, "Call 参数数量 (%d) 与函数 '%s' 签名 (%d) 不匹配", len(inst.Args), targetFn.Name, len(targetFn.ParamTypes))
					}
					for ai, arg := range inst.Args {
						verifyOperand(b.ID, idx, arg, fmt.Sprintf("Call.Arg#%d", ai))
						if ai < len(targetFn.ParamTypes) && !t.isCompatibleArgOrReturn(arg.Type, targetFn.ParamTypes[ai]) {
							addErr(fn.Name, b.ID, idx, "Call 参数 #%d 类型 %s 与形参类型 %s 不匹配", ai, t.Format(arg.Type), t.Format(targetFn.ParamTypes[ai]))
						}
					}
					if !t.isCompatibleArgOrReturn(targetFn.ReturnType, inst.Type) {
						addErr(fn.Name, b.ID, idx, "Call 返回类型 %s 与函数 '%s' 声明返回类型 %s 不匹配", t.Format(inst.Type), targetFn.Name, t.Format(targetFn.ReturnType))
					}
				}
			case OpBuiltinCall:
				for ai, arg := range inst.Args {
					verifyOperand(b.ID, idx, arg, fmt.Sprintf("BuiltinCall.Arg#%d", ai))
				}
			case OpForeignCall:
				for ai, arg := range inst.Args {
					verifyOperand(b.ID, idx, arg, fmt.Sprintf("ForeignCall.Arg#%d", ai))
				}
				if inst.HasContract {
					c, ok := contractSet[inst.ContractID]
					if !ok {
						addErr(fn.Name, b.ID, idx, "ForeignCall 引用未声明 FFI ContractID c%d", inst.ContractID)
					} else if len(inst.Args) != len(c.Params) {
						addErr(fn.Name, b.ID, idx, "ForeignCall 实参数量 (%d) 与 FFI 契约 '%s' 参数数量 (%d) 不匹配", len(inst.Args), c.ContractKey, len(c.Params))
					}
				}
			case OpDrop:
				verifyPlace(b.ID, idx, inst.Place, "Drop.Place")
				if inst.GlueID == InvalidDropGlue {
					addErr(fn.Name, b.ID, idx, "Drop 缺少有效 DropGlueID")
				} else if g, ok := glueSet[inst.GlueID]; !ok {
					addErr(fn.Name, b.ID, idx, "Drop 引用未声明 DropGlue g%d", inst.GlueID)
				} else if g.Type != inst.Type {
					addErr(fn.Name, b.ID, idx, "Drop 指令类型 t%d 与胶水 g%d 类型 t%d 不匹配", inst.Type, inst.GlueID, g.Type)
				}
			case OpCondDrop:
				verifyPlace(b.ID, idx, inst.Place, "CondDrop.Place")
				if inst.FlagID == InvalidDropFlag {
					addErr(fn.Name, b.ID, idx, "CondDrop 缺少有效 DropFlagID")
				} else if _, ok := flagSet[inst.FlagID]; !ok {
					addErr(fn.Name, b.ID, idx, "CondDrop 引用未声明 DropFlag df%d", inst.FlagID)
				}
				if inst.GlueID == InvalidDropGlue {
					addErr(fn.Name, b.ID, idx, "CondDrop 缺少有效 DropGlueID")
				} else if g, ok := glueSet[inst.GlueID]; !ok {
					addErr(fn.Name, b.ID, idx, "CondDrop 引用未声明 DropGlue g%d", inst.GlueID)
				} else if g.Type != inst.Type {
					addErr(fn.Name, b.ID, idx, "CondDrop 指令类型 t%d 与胶水 g%d 类型 t%d 不匹配", inst.Type, inst.GlueID, g.Type)
				}
			case OpDropFlagDecl, OpDropFlagSet:
				if _, ok := flagSet[inst.FlagID]; !ok {
					addErr(fn.Name, b.ID, idx, "%s 引用未声明 DropFlag df%d", inst.Kind, inst.FlagID)
				}
			case OpDropFlagCheck:
				if _, ok := flagSet[inst.FlagID]; !ok {
					addErr(fn.Name, b.ID, idx, "DropFlagCheck 引用未声明 DropFlag df%d", inst.FlagID)
				}
				if t.Info(inst.Type).Kind != TypeBool {
					addErr(fn.Name, b.ID, idx, "DropFlagCheck 结果类型必须为 bool，实际为 %s", t.Format(inst.Type))
				}
			default:
				addErr(fn.Name, b.ID, idx, "未知 TIR 指令操作码 %d", inst.Kind)
			}
		}

		// 4.5 终结器结构、目标与类型校验
		term := b.Terminator
		switch term.Kind {
		case TermGoto:
			if _, ok := blockSet[term.Target]; !ok {
				addErr(fn.Name, b.ID, -1, "Goto 引用悬挂基本块目标 bb%d", term.Target)
			}
		case TermBranchIf:
			verifyOperand(b.ID, len(b.Instructions), term.Cond, "BranchIf.Cond")
			if t.Info(term.Cond.Type).Kind != TypeBool {
				addErr(fn.Name, b.ID, -1, "BranchIf 条件类型必须为 bool，实际为 %s", t.Format(term.Cond.Type))
			}
			if _, ok := blockSet[term.ThenBlock]; !ok {
				addErr(fn.Name, b.ID, -1, "BranchIf ThenBlock bb%d 不存在", term.ThenBlock)
			}
			if _, ok := blockSet[term.ElseBlock]; !ok {
				addErr(fn.Name, b.ID, -1, "BranchIf ElseBlock bb%d 不存在", term.ElseBlock)
			}
		case TermSwitch:
			verifyOperand(b.ID, len(b.Instructions), term.Cond, "Switch.Discriminant")
			for ci, c := range term.Cases {
				if _, ok := blockSet[c.Target]; !ok {
					addErr(fn.Name, b.ID, -1, "Switch 分支 #%d 引用悬挂基本块 bb%d", ci, c.Target)
				}
			}
			if term.DefaultBlk != InvalidBlock {
				if _, ok := blockSet[term.DefaultBlk]; !ok {
					addErr(fn.Name, b.ID, -1, "Switch DefaultBlk bb%d 不存在", term.DefaultBlk)
				}
			} else if len(term.Cases) == 0 {
				addErr(fn.Name, b.ID, -1, "Switch 终结器缺少任何分支或默认目标")
			}
		case TermReturn:
			retInfo := t.Info(fn.ReturnType)
			if t.Valid(fn.ReturnType) && retInfo.Kind != TypeUnit && term.ReturnVal == nil {
				addErr(fn.Name, b.ID, -1, "WON-INT-001: 非 unit 函数返回类型 %s 缺少必需的 ReturnVal 操作数", t.Format(fn.ReturnType))
			} else if term.ReturnVal != nil {
				verifyOperand(b.ID, len(b.Instructions), *term.ReturnVal, "Return.Value")
				if !t.isCompatibleArgOrReturn(term.ReturnVal.Type, fn.ReturnType) {
					addErr(fn.Name, b.ID, -1, "Return 值类型 %s 与函数签名返回类型 %s 不匹配", t.Format(term.ReturnVal.Type), t.Format(fn.ReturnType))
				}
			}
		case TermUnreachable:
			// 合法终结器
		default:
			addErr(fn.Name, b.ID, -1, "基本块缺少合法终结器 (TermKind=%d)", term.Kind)
		}
	}
}

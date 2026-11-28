package tir

import (
	"fmt"
	"strings"
)

// Dump 生成完全确定性的规范 TIR 文本快照（严禁依赖无序 map 遍历）。
func (p *Program) Dump() string {
	if p == nil {
		return "<nil tir.Program>\n"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "tir.Program ownership_ok=%v drop_ok=%v\n", p.OwnershipOK, p.DropOK)

	if p.Types != nil {
		for id := TypeID(1); int(id) < p.Types.Len(); id++ {
			info := p.Types.Info(id)
			fmt.Fprintf(&sb, "  type t%d = %s", id, p.Types.Format(id))
			if info.IsOpaqueForeign {
				fmt.Fprintf(&sb, " [opaque_foreign ns=%s]", info.ForeignNamespace)
			}
			if info.Kind == TypeStruct && len(info.Fields) > 0 {
				sb.WriteString(" { ")
				for i, f := range info.Fields {
					if i > 0 {
						sb.WriteString(", ")
					}
					fmt.Fprintf(&sb, "%s: t%d(%s)", f.Name, f.Type, p.Types.Format(f.Type))
				}
				sb.WriteString(" }")
			} else if info.Kind == TypeEnum && len(info.Variants) > 0 {
				sb.WriteString(" { ")
				for i, v := range info.Variants {
					if i > 0 {
						sb.WriteString(" | ")
					}
					sb.WriteString(v.Name)
					if v.HasDiscriminant {
						fmt.Fprintf(&sb, " = %s", v.DiscriminantText)
					}
					if len(v.TupleElems) > 0 {
						sb.WriteByte('(')
						for j, te := range v.TupleElems {
							if j > 0 {
								sb.WriteString(", ")
							}
							fmt.Fprintf(&sb, "t%d", te)
						}
						sb.WriteByte(')')
					}
				}
				sb.WriteString(" }")
			}
			sb.WriteByte('\n')
		}
	}

	for _, g := range p.DropGlues {
		fmt.Fprintf(&sb, "  glue g%d name=%s kind=%s type=t%d(%s)", g.ID, g.CanonicalName, g.Kind, g.Type, p.fmtType(g.Type))
		if g.ReleaseSymbol != "" {
			fmt.Fprintf(&sb, " release=%s", g.ReleaseSymbol)
		}
		sb.WriteByte('\n')
	}

	for _, fh := range p.ForeignHeaders {
		fmt.Fprintf(&sb, "  foreign_header %s\n", fh.HeaderPath)
	}

	for _, cat := range p.FFICatalogs {
		fmt.Fprintf(&sb, "  ffi_catalog ns=%s schema=%s header=%q\n", cat.Namespace, cat.SchemaVersion, cat.HeaderPath)
	}

	for _, c := range p.FFIContracts {
		fmt.Fprintf(&sb, "  ffi_contract c%d key=%s fn=%s::%s cc=%s header=%q ret=%s borrows_param=%d\n",
			c.ID, c.ContractKey, c.Namespace, c.Function, c.CallingConv, c.HeaderPath, c.ReturnOwnership, c.BorrowsParam)
	}

	for _, fn := range p.Functions {
		if fn == nil {
			continue
		}
		sb.WriteString(fn.Dump(p))
	}
	return sb.String()
}

// Dump 生成单个 TIR 函数的确定性文本视图。
func (f *Function) Dump(prog *Program) string {
	if f == nil {
		return "<nil tir.Function>\n"
	}
	var sb strings.Builder
	params := make([]string, len(f.ParamTypes))
	for i, pt := range f.ParamTypes {
		params[i] = fmt.Sprintf("t%d(%s)", pt, prog.fmtType(pt))
	}
	fmt.Fprintf(&sb, "fn @f%d %s(%s) -> t%d(%s) [cc=%s entry=bb%d exit=bb%d]:\n",
		f.ID, f.Name, strings.Join(params, ", "), f.ReturnType, prog.fmtType(f.ReturnType), f.CallingConv, f.EntryBlock, f.ExitBlock)

	for _, l := range f.Locals {
		mutTag := "let"
		if l.IsMut {
			mutTag = "var"
		}
		paramTag := ""
		if l.IsParam {
			paramTag = fmt.Sprintf(" param#%d", l.ParamIdx)
		}
		fmt.Fprintf(&sb, "  local _l%d %s %s: t%d(%s)%s scope=%d\n",
			l.ID, mutTag, l.Name, l.Type, prog.fmtType(l.Type), paramTag, l.ScopeID)
	}

	for _, df := range f.DropFlags {
		fmt.Fprintf(&sb, "  flag df%d %s place=%s init=%v glue=g%d\n",
			df.ID, df.Name, df.Place.String(), df.Initial, df.GlueID)
	}

	for _, b := range f.Blocks {
		if b == nil {
			continue
		}
		fmt.Fprintf(&sb, "  bb%d (%s) preds=%v succs=%v:\n", b.ID, b.Label, b.Predecessors, b.Successors)
		for _, inst := range b.Instructions {
			sb.WriteString("    ")
			sb.WriteString(formatInst(prog, inst))
			sb.WriteByte('\n')
		}
		sb.WriteString("    -> ")
		sb.WriteString(formatTerm(prog, b.Terminator))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func (p *Program) fmtType(id TypeID) string {
	if p == nil || p.Types == nil {
		return fmt.Sprintf("t%d", id)
	}
	return p.Types.Format(id)
}

func formatOperand(prog *Program, op Operand) string {
	switch op.Kind {
	case OperandValue:
		return fmt.Sprintf("%%%d:t%d(%s)", op.Value, op.Type, prog.fmtType(op.Type))
	case OperandConst:
		return fmt.Sprintf("const %s:t%d(%s)", op.Const.String(), op.Type, prog.fmtType(op.Type))
	case OperandPlace:
		return fmt.Sprintf("place %s:t%d(%s)", op.Place.String(), op.Type, prog.fmtType(op.Type))
	case OperandFunc:
		fnName := fmt.Sprintf("@f%d", op.FuncID)
		if prog != nil {
			if fn, ok := prog.FunctionByID(op.FuncID); ok {
				fnName = fmt.Sprintf("@f%d(%s)", op.FuncID, fn.Name)
			}
		}
		return fmt.Sprintf("fn %s:t%d", fnName, op.Type)
	default:
		return "<invalid-op>"
	}
}

func formatInst(prog *Program, inst Instruction) string {
	typeSuffix := fmt.Sprintf("t%d(%s)", inst.Type, prog.fmtType(inst.Type))
	switch inst.Kind {
	case OpStorageLive:
		return fmt.Sprintf("StorageLive _l%d : %s", inst.Local, typeSuffix)
	case OpStorageDead:
		if len(inst.Locals) > 0 {
			return fmt.Sprintf("StorageDead scope=%d locals=%v", inst.ScopeID, inst.Locals)
		}
		return fmt.Sprintf("StorageDead _l%d : %s", inst.Local, typeSuffix)
	case OpScopeEnter:
		return fmt.Sprintf("ScopeEnter scope=%d", inst.ScopeID)
	case OpScopeExit:
		return fmt.Sprintf("ScopeExit scope=%d locals=%v", inst.ScopeID, inst.Locals)
	case OpInit:
		if inst.Src.Kind != OperandInvalid {
			return fmt.Sprintf("Init %s = %s : %s", inst.Place.String(), formatOperand(prog, inst.Src), typeSuffix)
		}
		return fmt.Sprintf("Init %s : %s", inst.Place.String(), typeSuffix)
	case OpLoad:
		return fmt.Sprintf("%%%d = Load %s : %s", inst.Dest, inst.Place.String(), typeSuffix)
	case OpStore:
		return fmt.Sprintf("Store %s = %s : %s", inst.Place.String(), formatOperand(prog, inst.Src), typeSuffix)
	case OpComputeAddr:
		return fmt.Sprintf("%%%d = ComputeAddr %s : %s", inst.Dest, inst.TargetPlace.String(), typeSuffix)
	case OpConst:
		return fmt.Sprintf("%%%d = Const %s : %s", inst.Dest, inst.Const.String(), typeSuffix)
	case OpUnary:
		return fmt.Sprintf("%%%d = Unary %s %s : %s", inst.Dest, inst.UnaryOp, formatOperand(prog, inst.Src), typeSuffix)
	case OpBinary:
		return fmt.Sprintf("%%%d = Binary %s %s, %s : %s", inst.Dest, inst.BinaryOp, formatOperand(prog, inst.Left), formatOperand(prog, inst.Right), typeSuffix)
	case OpCast:
		return fmt.Sprintf("%%%d = Cast[%s] %s -> %s", inst.Dest, inst.CastKind, formatOperand(prog, inst.Src), typeSuffix)
	case OpValueAssign:
		return fmt.Sprintf("%%%d = ValueAssign %s : %s", inst.Dest, formatOperand(prog, inst.Src), typeSuffix)
	case OpAggregateConstruct:
		items := make([]string, len(inst.AggItems))
		for i, it := range inst.AggItems {
			if it.FieldName != "" {
				items[i] = fmt.Sprintf("%s=%s", it.FieldName, formatOperand(prog, it.Value))
			} else {
				items[i] = formatOperand(prog, it.Value)
			}
		}
		extra := ""
		if inst.AggKind == AggEnumVariant {
			extra = fmt.Sprintf(" variant=%s#%d", inst.VariantName, inst.VariantIdx)
		} else if inst.AggKind == AggArrayRepeat {
			extra = fmt.Sprintf(" len=%d", inst.RepeatLen)
		}
		return fmt.Sprintf("%%%d = Aggregate[%s%s](%s) : %s", inst.Dest, inst.AggKind, extra, strings.Join(items, ", "), typeSuffix)
	case OpExtract:
		switch inst.ExtractProj.Kind {
		case ProjDowncast:
			return fmt.Sprintf("%%%d = Extract %s.@%s#%d : %s", inst.Dest, formatOperand(prog, inst.Src), inst.ExtractProj.VariantName, inst.ExtractProj.VariantIndex, typeSuffix)
		case ProjConstIndex:
			return fmt.Sprintf("%%%d = Extract %s[%d] : %s", inst.Dest, formatOperand(prog, inst.Src), inst.ExtractProj.ConstIndex, typeSuffix)
		default:
			return fmt.Sprintf("%%%d = Extract %s.%d(%s) : %s", inst.Dest, formatOperand(prog, inst.Src), inst.ExtractProj.FieldIndex, inst.ExtractProj.FieldName, typeSuffix)
		}
	case OpCopy:
		if inst.Dest != InvalidValue {
			return fmt.Sprintf("%%%d = Copy %s : %s", inst.Dest, inst.Place.String(), typeSuffix)
		}
		return fmt.Sprintf("Copy %s : %s", inst.Place.String(), typeSuffix)
	case OpMove:
		if inst.Dest != InvalidValue {
			return fmt.Sprintf("%%%d = Move %s : %s", inst.Dest, inst.Place.String(), typeSuffix)
		}
		return fmt.Sprintf("Move %s : %s", inst.Place.String(), typeSuffix)
	case OpAssign:
		opName := "Assign"
		if inst.IsReinit {
			opName = "Reinit"
		}
		if inst.Src.Kind != OperandInvalid {
			return fmt.Sprintf("%s %s = %s : %s", opName, inst.Place.String(), formatOperand(prog, inst.Src), typeSuffix)
		}
		return fmt.Sprintf("%s %s : %s", opName, inst.Place.String(), typeSuffix)
	case OpBorrow:
		parent := ""
		if inst.ParentPlace != nil {
			parent = " parent=" + inst.ParentPlace.String()
		}
		return fmt.Sprintf("%%%d = Borrow[%s loan=L%d%s] %s : %s", inst.Dest, inst.BorrowKind, inst.LoanID, parent, inst.Place.String(), typeSuffix)
	case OpStaticRef:
		return fmt.Sprintf("%%%d = StaticRef %s : %s", inst.Dest, inst.Const.String(), typeSuffix)
	case OpOwnershipTransfer:
		return fmt.Sprintf("OwnershipTransfer %s callee=%s contract=c%d glue=g%d : %s", inst.Place.String(), inst.CalleeName, inst.ContractID, inst.GlueID, typeSuffix)
	case OpCall, OpBuiltinCall:
		args := make([]string, len(inst.Args))
		for i, a := range inst.Args {
			args[i] = formatOperand(prog, a)
		}
		prefix := ""
		if inst.Dest != InvalidValue {
			prefix = fmt.Sprintf("%%%d = ", inst.Dest)
		}
		op := "Call"
		callee := fmt.Sprintf("@f%d(%s)", inst.CalleeFunc, inst.CalleeName)
		if inst.Kind == OpBuiltinCall {
			op = "BuiltinCall"
			callee = inst.CalleeName
		}
		return fmt.Sprintf("%s%s %s(%s) [cc=%s] : %s", prefix, op, callee, strings.Join(args, ", "), inst.CallingConv, typeSuffix)
	case OpForeignCall:
		args := make([]string, len(inst.Args))
		for i, a := range inst.Args {
			own := ""
			if i < len(inst.ArgOwnership) {
				own = "[" + inst.ArgOwnership[i].String() + "]"
			}
			args[i] = own + formatOperand(prog, a)
		}
		prefix := ""
		if inst.Dest != InvalidValue {
			prefix = fmt.Sprintf("%%%d = ", inst.Dest)
		}
		return fmt.Sprintf("%sForeignCall %s::%s(%s) [cc=%s unsafe=%v has_contract=%v contract=c%d ret=%s] : %s",
			prefix, inst.CalleeNamespace, inst.CalleeName, strings.Join(args, ", "), inst.CallingConv,
			inst.InUnsafe, inst.HasContract, inst.ContractID, inst.ReturnOwnership, typeSuffix)
	case OpDrop:
		return fmt.Sprintf("Drop %s glue=g%d : %s", inst.Place.String(), inst.GlueID, typeSuffix)
	case OpCondDrop:
		return fmt.Sprintf("CondDrop %s flag=df%d glue=g%d : %s", inst.Place.String(), inst.FlagID, inst.GlueID, typeSuffix)
	case OpDropFlagDecl:
		return fmt.Sprintf("DropFlagDecl df%d init=%v", inst.FlagID, inst.FlagBool)
	case OpDropFlagSet:
		return fmt.Sprintf("DropFlagSet df%d = %v", inst.FlagID, inst.FlagBool)
	case OpDropFlagCheck:
		return fmt.Sprintf("%%%d = DropFlagCheck df%d : %s", inst.Dest, inst.FlagID, typeSuffix)
	default:
		return fmt.Sprintf("<unknown-op %d>", inst.Kind)
	}
}

func formatTerm(prog *Program, t Terminator) string {
	switch t.Kind {
	case TermGoto:
		return fmt.Sprintf("Goto bb%d", t.Target)
	case TermBranchIf:
		return fmt.Sprintf("BranchIf %s then=bb%d else=bb%d", formatOperand(prog, t.Cond), t.ThenBlock, t.ElseBlock)
	case TermSwitch:
		cases := make([]string, len(t.Cases))
		for i, c := range t.Cases {
			cases[i] = fmt.Sprintf("%s->bb%d", c.Label, c.Target)
		}
		return fmt.Sprintf("Switch %s [%s] default=bb%d", formatOperand(prog, t.Cond), strings.Join(cases, ", "), t.DefaultBlk)
	case TermReturn:
		if t.ReturnVal != nil {
			return fmt.Sprintf("Return %s", formatOperand(prog, *t.ReturnVal))
		}
		return "Return"
	case TermUnreachable:
		if t.IsTrap {
			return fmt.Sprintf("Trap(%q)", t.TrapReason)
		}
		return "Unreachable"
	default:
		return "<invalid-terminator>"
	}
}

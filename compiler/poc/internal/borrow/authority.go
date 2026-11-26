package borrow

import (
	"fmt"
	"sort"

	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// AccessAuthority 显式区分引用/投影路径经证明后拥有的访问权限类别，严禁将元数据缺失或未知来源当作可变权限。
type AccessAuthority uint8

const (
	AuthorityMutable      AccessAuthority = iota // 已证明具备独占可变 (&mut T) 安全访问权限
	AuthorityShared                              // 已证明仅具备共享只读 (&T / *const T) 访问权限 -> 违反时报 WON-MEM-004
	AuthorityUnsafeRawPtr                        // 显式经由 *mut T 原生指针派生，允许 unsafe 写权限但别名须按 Unknown 保守处理
	AuthorityUnknown                             // 内部元数据缺失、下标越界、父借用不一致或投影类型冲突 -> Fail-Closed 报 WON-INT-001
)

// String 返回 AccessAuthority 的稳定可读标识，便于诊断信息与测试断言。
func (a AccessAuthority) String() string {
	switch a {
	case AuthorityMutable:
		return "AuthorityMutable"
	case AuthorityShared:
		return "AuthorityShared"
	case AuthorityUnsafeRawPtr:
		return "AuthorityUnsafeRawPtr"
	default:
		return "AuthorityUnknown"
	}
}

// PermitsMutableAccess 判断权限是否允许发起写操作或可变重借用（安全 &mut 或显式 unsafe *mut）。
func (a AccessAuthority) PermitsMutableAccess() bool {
	return a == AuthorityMutable || a == AuthorityUnsafeRawPtr
}

// authorityCheckOutcome 封装权限与不变量校验的结果，精确分离内部错误 (WON-INT-001) 与语言级借用权限错误 (WON-MEM-004)。
type authorityCheckOutcome struct {
	Authority AccessAuthority
	Internal  bool
	Detail    string
}

func okAuthority(a AccessAuthority) authorityCheckOutcome {
	return authorityCheckOutcome{Authority: a}
}

func sharedAuthority(detail string) authorityCheckOutcome {
	return authorityCheckOutcome{Authority: AuthorityShared, Detail: detail}
}

func internalAuthorityError(detail string) authorityCheckOutcome {
	return authorityCheckOutcome{Authority: AuthorityUnknown, Internal: true, Detail: detail}
}

// combineOriginAuthorities 在汇合多条来源（OriginSet 中的多个 Loan/Param/Flag）时取最严格下界：
// 任意内部损坏优先触发 AuthorityUnknown(Internal=true)，其次任一共享来源降级为 AuthorityShared，
// 仅当全部来源合法且含原生指针时降级为 AuthorityUnsafeRawPtr。
func combineOriginAuthorities(cur, next authorityCheckOutcome) authorityCheckOutcome {
	if cur.Internal {
		return cur
	}
	if next.Internal {
		return next
	}
	if cur.Authority == AuthorityUnknown {
		return cur
	}
	if next.Authority == AuthorityUnknown {
		return next
	}
	if cur.Authority == AuthorityShared {
		return cur
	}
	if next.Authority == AuthorityShared {
		return next
	}
	if cur.Authority == AuthorityUnsafeRawPtr || next.Authority == AuthorityUnsafeRawPtr {
		return okAuthority(AuthorityUnsafeRawPtr)
	}
	return okAuthority(AuthorityMutable)
}

func isValidBorrowKind(k cfg.BorrowKind) bool {
	switch k {
	case cfg.BorrowShared, cfg.BorrowMut, cfg.BorrowReborrowShared, cfg.BorrowReborrowMut:
		return true
	default:
		return false
	}
}

// validateParamDerefChainAuthority 沿着规范形参 Param(pIdx) 的解引用与成员投影链逐步验证类型与访问权限。
// 对函数符号缺失、参数越界、无效 TypeID、未知投影或投影类型失配返回 Internal=true (WON-INT-001)。
func validateParamDerefChainAuthority(
	fn *cfg.FuncCFG,
	pIdx int,
	place cfg.Place,
	tctx *types.Context,
	symTab *symbols.Table,
) authorityCheckOutcome {
	if fn == nil || fn.FuncSym == nil || tctx == nil {
		return internalAuthorityError("参数权限校验缺少 FuncSym 或 TypeContext")
	}
	paramCount := fn.FuncSym.EffectiveParamCount()
	if pIdx < 0 || pIdx >= paramCount {
		return internalAuthorityError(fmt.Sprintf("形参下标 %d 超出有效范围 [0, %d)", pIdx, paramCount))
	}
	curType := fn.FuncSym.EffectiveParamType(pIdx, tctx)
	if curType == types.Invalid {
		return internalAuthorityError(fmt.Sprintf("规范形参 #%d 缺少有效类型 (Invalid)", pIdx))
	}

	viaUnsafeRaw := false
	sawShared := false
	sawMutRef := false
	firstInfo := tctx.Info(curType)
	if firstInfo.Kind == types.KindRef {
		if !firstInfo.IsMut {
			sawShared = true
		} else {
			sawMutRef = true
		}
	} else if firstInfo.Kind == types.KindRawPtr {
		if !firstInfo.IsMut {
			sawShared = true
		} else {
			viaUnsafeRaw = true
		}
	}

	for projIdx, proj := range place.Projections {
		info := tctx.Info(curType)
		switch proj.Kind {
		case cfg.ProjDeref:
			if info.Kind != types.KindRef && info.Kind != types.KindRawPtr {
				return internalAuthorityError(fmt.Sprintf("投影 #%d (ProjDeref) 作用于非引用/指针类型 '%s'", projIdx, tctx.Format(curType)))
			}
			if !info.IsMut {
				sawShared = true
			} else if info.Kind == types.KindRef {
				sawMutRef = true
			} else if info.Kind == types.KindRawPtr {
				viaUnsafeRaw = true
			}
			nextType := info.Elem
			if nextType == types.Invalid {
				return internalAuthorityError(fmt.Sprintf("投影 #%d (ProjDeref) 解引用目标类型无效", projIdx))
			}
			if proj.ElemType != types.Invalid && proj.ElemType != nextType {
				return internalAuthorityError(fmt.Sprintf("投影 #%d (ProjDeref) 元数据类型 '%s' 与解引用元素类型 '%s' 不一致", projIdx, tctx.Format(proj.ElemType), tctx.Format(nextType)))
			}
			curType = nextType

		case cfg.ProjField:
			if info.Kind == types.KindRef || info.Kind == types.KindRawPtr {
				if !info.IsMut {
					sawShared = true
				} else if info.Kind == types.KindRef {
					sawMutRef = true
				} else if info.Kind == types.KindRawPtr {
					viaUnsafeRaw = true
				}
				if info.Elem == types.Invalid {
					return internalAuthorityError(fmt.Sprintf("投影 #%d (ProjField %s) 自动解引用根类型无效", projIdx, proj.FieldName))
				}
				curType = info.Elem
				info = tctx.Info(curType)
			}
			if info.Kind != types.KindStruct {
				return internalAuthorityError(fmt.Sprintf("投影 #%d (ProjField %s) 作用于非结构体类型 '%s'", projIdx, proj.FieldName, tctx.Format(curType)))
			}
			if symTab == nil {
				return internalAuthorityError(fmt.Sprintf("投影 #%d (ProjField %s) 缺少符号表上下文", projIdx, proj.FieldName))
			}
			sym := symTab.ByID[symbols.SymbolID(info.SymbolID)]
			if sym == nil || sym.FieldMap == nil {
				return internalAuthorityError(fmt.Sprintf("结构体类型 '%s' (sym=%d) 在符号表中缺失", tctx.Format(curType), info.SymbolID))
			}
			fi := sym.FieldMap[proj.FieldName]
			if fi == nil {
				return internalAuthorityError(fmt.Sprintf("结构体 '%s' 中不存在字段 '%s'", sym.Name, proj.FieldName))
			}
			fieldType := fi.Type
			if len(sym.GenericParamIDs) > 0 {
				if len(sym.GenericParamIDs) != len(info.GenericArgs) {
					return internalAuthorityError(fmt.Sprintf("泛型结构体 '%s' 字段 '%s' 替换参数个数不匹配 (%d vs %d)", sym.Name, proj.FieldName, len(sym.GenericParamIDs), len(info.GenericArgs)))
				}
				subst := make(map[uint64]types.TypeID, len(info.GenericArgs))
				for i, gid := range sym.GenericParamIDs {
					if info.GenericArgs[i] == types.Invalid {
						return internalAuthorityError(fmt.Sprintf("泛型结构体 '%s' 第 %d 个实参类型无效", sym.Name, i))
					}
					subst[uint64(gid)] = info.GenericArgs[i]
				}
				fieldType = tctx.Substitute(fieldType, subst)
			}
			if fieldType == types.Invalid {
				return internalAuthorityError(fmt.Sprintf("结构体 '%s' 字段 '%s' 类型无效", sym.Name, proj.FieldName))
			}
			if proj.ElemType != types.Invalid && proj.ElemType != fieldType && proj.ElemType != fi.Type {
				return internalAuthorityError(fmt.Sprintf("字段 '%s.%s' 投影类型 '%s' 与声明类型 '%s' 不一致", sym.Name, proj.FieldName, tctx.Format(proj.ElemType), tctx.Format(fieldType)))
			}
			curType = fieldType

		case cfg.ProjConstIdx, cfg.ProjIndex:
			if info.Kind == types.KindRef || info.Kind == types.KindRawPtr {
				if !info.IsMut {
					sawShared = true
				} else if info.Kind == types.KindRef {
					sawMutRef = true
				} else if info.Kind == types.KindRawPtr {
					viaUnsafeRaw = true
				}
				if info.Elem == types.Invalid {
					return internalAuthorityError(fmt.Sprintf("投影 #%d (Index) 自动解引用根类型无效", projIdx))
				}
				curType = info.Elem
				info = tctx.Info(curType)
			}
			if info.Kind != types.KindArray && info.Kind != types.KindSlice {
				return internalAuthorityError(fmt.Sprintf("投影 #%d (Index) 作用于非数组/切片类型 '%s'", projIdx, tctx.Format(curType)))
			}
			elemType := info.Elem
			if elemType == types.Invalid {
				return internalAuthorityError(fmt.Sprintf("投影 #%d (Index) 元素类型无效", projIdx))
			}
			if proj.Kind == cfg.ProjConstIdx && proj.ConstIndex < 0 {
				return internalAuthorityError(fmt.Sprintf("投影 #%d (ConstIndex) 存在负数下标 %d", projIdx, proj.ConstIndex))
			}
			if proj.ElemType != types.Invalid && proj.ElemType != elemType {
				return internalAuthorityError(fmt.Sprintf("投影 #%d (Index) 元素类型 '%s' 与实际 '%s' 不一致", projIdx, tctx.Format(proj.ElemType), tctx.Format(elemType)))
			}
			curType = elemType

		case cfg.ProjTupleField:
			if info.Kind == types.KindRef || info.Kind == types.KindRawPtr {
				if !info.IsMut {
					sawShared = true
				} else if info.Kind == types.KindRef {
					sawMutRef = true
				} else if info.Kind == types.KindRawPtr {
					viaUnsafeRaw = true
				}
				if info.Elem == types.Invalid {
					return internalAuthorityError(fmt.Sprintf("投影 #%d (TupleField %d) 自动解引用根类型无效", projIdx, proj.FieldIndex))
				}
				curType = info.Elem
				info = tctx.Info(curType)
			}
			if info.Kind != types.KindTuple {
				return internalAuthorityError(fmt.Sprintf("投影 #%d (TupleField) 作用于非元组类型 '%s'", projIdx, tctx.Format(curType)))
			}
			if proj.FieldIndex < 0 || proj.FieldIndex >= len(info.Elems) {
				return internalAuthorityError(fmt.Sprintf("元组下标 %d 越界 (len=%d)", proj.FieldIndex, len(info.Elems)))
			}
			elemType := info.Elems[proj.FieldIndex]
			if elemType == types.Invalid {
				return internalAuthorityError(fmt.Sprintf("元组第 %d 个成员类型无效", proj.FieldIndex))
			}
			if proj.ElemType != types.Invalid && proj.ElemType != elemType {
				return internalAuthorityError(fmt.Sprintf("元组第 %d 个成员投影类型 '%s' 与实际 '%s' 不一致", proj.FieldIndex, tctx.Format(proj.ElemType), tctx.Format(elemType)))
			}
			curType = elemType

		default:
			return internalAuthorityError(fmt.Sprintf("未知的 Place 投影种类 %d", proj.Kind))
		}
	}

	if sawShared {
		return sharedAuthority("解引用或投影链中包含只读共享引用 (&T / *const T)")
	}
	endInfo := tctx.Info(curType)
	if endInfo.Kind == types.KindRef {
		if !endInfo.IsMut {
			return sharedAuthority("参数投影目标类型为共享不可变引用 (&T)")
		}
		sawMutRef = true
	} else if endInfo.Kind == types.KindRawPtr {
		if !endInfo.IsMut {
			return sharedAuthority("参数投影目标类型为只读原生指针 (*const T)")
		}
		viaUnsafeRaw = true
	}

	if viaUnsafeRaw {
		return okAuthority(AuthorityUnsafeRawPtr)
	}
	if sawMutRef {
		return okAuthority(AuthorityMutable)
	}
	// 非引用标量或未精确定位至引用成员的值聚合参数绝不能默认授予可变借用权限
	return internalAuthorityError(fmt.Sprintf("形参 #%d 路径 '%s' (终端类型 '%s') 不携带可证的可变引用权限", pIdx, place.String(), tctx.Format(curType)))
}

// validateLoanRecordMetadata 强制验证单条 LoanInfo 的规范函数身份、引用/指针类型、ElemType、BorrowKind 与 FromRawPtr 一致性。
func validateLoanRecordMetadata(
	expectedFuncSymID symbols.SymbolID,
	loan *LoanInfo,
	tctx *types.Context,
	label string,
) authorityCheckOutcome {
	if expectedFuncSymID == 0 {
		return internalAuthorityError(fmt.Sprintf("%s校验缺少有效的函数符号身份 ID", label))
	}
	if loan == nil {
		return internalAuthorityError(fmt.Sprintf("%s记录为空 (nil)", label))
	}
	if loan.ID == cfg.InvalidLoan {
		return internalAuthorityError(fmt.Sprintf("%s LoanID 为零值 InvalidLoan (0)", label))
	}
	if loan.FuncSymID == 0 || loan.FuncSymID != expectedFuncSymID {
		return internalAuthorityError(fmt.Sprintf("%s L%d 函数身份不匹配 (期望 %d，实际 %d)", label, loan.ID, expectedFuncSymID, loan.FuncSymID))
	}
	if tctx == nil {
		return internalAuthorityError(fmt.Sprintf("%s L%d 校验缺少 TypeContext", label, loan.ID))
	}
	if !isValidBorrowKind(loan.Kind) {
		return internalAuthorityError(fmt.Sprintf("%s L%d BorrowKind (%d) 非法", label, loan.ID, loan.Kind))
	}
	if loan.IsReborrow && loan.Kind != cfg.BorrowReborrowShared && loan.Kind != cfg.BorrowReborrowMut {
		return internalAuthorityError(fmt.Sprintf("%s L%d 标记为重借用但 BorrowKind (%d) 非重借用类型", label, loan.ID, loan.Kind))
	}
	if loan.IsMut != (loan.Kind == cfg.BorrowMut || loan.Kind == cfg.BorrowReborrowMut) {
		return internalAuthorityError(fmt.Sprintf("%s L%d IsMut (%v) 与 BorrowKind (%d) 矛盾", label, loan.ID, loan.IsMut, loan.Kind))
	}
	if loan.Type == types.Invalid {
		return internalAuthorityError(fmt.Sprintf("%s L%d 缺少有效的 TypeID (Invalid)", label, loan.ID))
	}
	lInfo := tctx.Info(loan.Type)
	if lInfo.Kind != types.KindRef && lInfo.Kind != types.KindRawPtr {
		return internalAuthorityError(fmt.Sprintf("%s L%d 类型 '%s' 不是引用或指针类型", label, loan.ID, tctx.Format(loan.Type)))
	}
	if lInfo.Elem == types.Invalid {
		return internalAuthorityError(fmt.Sprintf("%s L%d 元素类型无效", label, loan.ID))
	}
	if lInfo.IsMut != loan.IsMut {
		return internalAuthorityError(fmt.Sprintf("%s L%d IsMut (%v) 与引用类型 '%s' 的可变性矛盾", label, loan.ID, loan.IsMut, tctx.Format(loan.Type)))
	}

	// 验证 FromRawPtr 与实际类型的物理来源一致性：普通安全引用绝不能仅凭布尔标记伪装成原生指针来源
	pref, hasDeref := loan.Place.PrefixBeforeLastDeref()
	derefsRawPtr := !loan.IsReborrow && hasDeref && pref.ElemType != types.Invalid && tctx.Info(pref.ElemType).Kind == types.KindRawPtr
	if loan.FromRawPtr {
		if lInfo.Kind != types.KindRawPtr && !derefsRawPtr {
			return internalAuthorityError(fmt.Sprintf("%s L%d 错误标记 FromRawPtr=true（既非 *T 类型也未直接解引用原生指针）", label, loan.ID))
		}
		if derefsRawPtr && loan.IsMut && !tctx.Info(pref.ElemType).IsMut {
			return sharedAuthority(fmt.Sprintf("%s L%d 从只读 *const 原生指针解引用不得获取可变借用", label, loan.ID))
		}
	} else {
		if lInfo.Kind == types.KindRawPtr || derefsRawPtr {
			return internalAuthorityError(fmt.Sprintf("%s L%d 为原生指针类型/解引用但缺少 FromRawPtr=true 标记", label, loan.ID))
		}
	}
	return okAuthority(AuthorityMutable)
}

// validateParentLoansIdentity 在排他检查前集中验证重借用及全部父借用标识、统一函数身份、强制类型一致、无环拓扑与前置可达顺序。
func validateParentLoansIdentity(
	fn *cfg.FuncCFG,
	flow *FuncLoanFlow,
	selfLoan *LoanInfo,
	reachable []bool,
	tctx *types.Context,
) authorityCheckOutcome {
	if fn == nil || fn.FuncSym == nil || fn.FuncSym.ID == 0 {
		return internalAuthorityError("重借用父关系校验缺少有效的 FuncCFG/FuncSym.ID")
	}
	expectedFuncID := fn.FuncSym.ID
	if flow == nil || flow.FuncSymID == 0 || flow.FuncSymID != expectedFuncID {
		return internalAuthorityError(fmt.Sprintf("重借用校验流 FuncSymID 缺失或与函数符号 %d 不一致", expectedFuncID))
	}
	if tctx == nil {
		return internalAuthorityError("重借用父关系校验缺少 TypeContext")
	}
	if selfLoan == nil || !selfLoan.IsReborrow {
		return internalAuthorityError("重借用父关系校验缺少有效重借用 LoanInfo")
	}
	if childCheck := validateLoanRecordMetadata(expectedFuncID, selfLoan, tctx, "子重借用"); childCheck.Internal {
		return childCheck
	}
	if !selfLoan.Place.HasDeref() && selfLoan.ParentPlace.RootSymID == 0 {
		return internalAuthorityError(fmt.Sprintf("重借用 L%d 缺少解引用投影与有效 ParentPlace", selfLoan.ID))
	}

	pids := make([]cfg.LoanID, 0, len(selfLoan.ParentLoans))
	for pid := range selfLoan.ParentLoans {
		pids = append(pids, pid)
	}
	sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })

	res := okAuthority(AuthorityMutable)
	for _, pid := range pids {
		if pid == cfg.InvalidLoan {
			return internalAuthorityError(fmt.Sprintf("重借用 L%d 包含零值无效父 LoanID (L0)", selfLoan.ID))
		}
		if pid == selfLoan.ID {
			return internalAuthorityError(fmt.Sprintf("重借用 L%d 存在自指父借用循环", selfLoan.ID))
		}
		pl := flow.LoanByID[pid]
		if pl == nil {
			return internalAuthorityError(fmt.Sprintf("重借用 L%d 引用了不存在的父借用 L%d", selfLoan.ID, pid))
		}
		parentCheck := validateLoanRecordMetadata(expectedFuncID, pl, tctx, fmt.Sprintf("重借用 L%d 的父借用", selfLoan.ID))
		if parentCheck.Internal {
			return parentCheck
		}
		// 一致性校验：若父占位与父借用均为简单局部变量载体且记录在 Carriers 中，父借用应确实绑定到该局部载体或其前驱
		if selfLoan.ParentPlace.RootSymID != 0 && !selfLoan.ParentPlace.HasDeref() {
			if fn.Locals != nil && fn.Locals[selfLoan.ParentPlace.RootSymID] == nil {
				return internalAuthorityError(fmt.Sprintf("重借用 L%d 的 ParentPlace '%s' 未在函数局部变量表登记", selfLoan.ID, selfLoan.ParentPlace.String()))
			}
			if len(pl.Carriers) > 0 && !pl.Carriers[selfLoan.ParentPlace.RootSymID] && len(selfLoan.ParentLoans) == 1 {
				return internalAuthorityError(fmt.Sprintf("重借用 L%d 的父位置 '%s' 与父借用 L%d 载体不匹配", selfLoan.ID, selfLoan.ParentPlace.String(), pid))
			}
		}
		// 检测 ParentLoans 祖先图中是否存在环
		if hasLoanAncestorCycle(flow, pid, map[cfg.LoanID]bool{selfLoan.ID: true}) {
			return internalAuthorityError(fmt.Sprintf("重借用 L%d 经父借用 L%d 检测到重借用环", selfLoan.ID, pid))
		}
		// 验证父借用创建点在当前可达控制流上先于子重借用（直达块前置或同块前置/可回边）
		if !loanPointPrecedesInReachableCFG(fn, pl.CreatedAt, selfLoan.CreatedAt, reachable) {
			return internalAuthorityError(fmt.Sprintf("父借用 L%d (bb%d:%d) 在控制流上未先于子重借用 L%d (bb%d:%d)", pid, pl.CreatedAt.Block, pl.CreatedAt.StmtIdx, selfLoan.ID, selfLoan.CreatedAt.Block, selfLoan.CreatedAt.StmtIdx))
		}
		if selfLoan.IsMut {
			if !pl.IsMut {
				res = combineOriginAuthorities(res, sharedAuthority("共享父借用不可派生可变重借用"))
			} else if pl.FromRawPtr {
				res = combineOriginAuthorities(res, okAuthority(AuthorityUnsafeRawPtr))
			}
		}
	}
	return res
}

func hasLoanAncestorCycle(flow *FuncLoanFlow, cur cfg.LoanID, visiting map[cfg.LoanID]bool) bool {
	if visiting[cur] {
		return true
	}
	li := flow.LoanByID[cur]
	if li == nil || !li.IsReborrow {
		return false
	}
	visiting[cur] = true
	for pid := range li.ParentLoans {
		if pid == cfg.InvalidLoan {
			continue
		}
		if hasLoanAncestorCycle(flow, pid, visiting) {
			return true
		}
	}
	delete(visiting, cur)
	return false
}

func loanPointPrecedesInReachableCFG(fn *cfg.FuncCFG, parent, child cfg.ProgramPoint, reachable []bool) bool {
	if fn == nil || parent.Block < 0 || int(parent.Block) >= len(fn.Blocks) || child.Block < 0 || int(child.Block) >= len(fn.Blocks) {
		return false
	}
	if len(reachable) == len(fn.Blocks) && (!reachable[parent.Block] || !reachable[child.Block]) {
		return false
	}
	if parent.Block == child.Block && parent.StmtIdx < child.StmtIdx {
		return true
	}
	// BFS 遍历从 parent.Block 出发经后续边可到达的全部基本块，若经非空路径可回到 child.Block（含循环回边），判定前置成立
	visited := make(map[cfg.BlockID]bool)
	queue := append([]cfg.BlockID(nil), fn.Blocks[parent.Block].Successors...)
	for len(queue) > 0 {
		b := queue[0]
		queue = queue[1:]
		if b == child.Block {
			return true
		}
		if visited[b] || int(b) < 0 || int(b) >= len(fn.Blocks) {
			continue
		}
		if len(reachable) == len(fn.Blocks) && !reachable[b] {
			continue
		}
		visited[b] = true
		queue = append(queue, fn.Blocks[b].Successors...)
	}
	return false
}

// validateCarrierOriginSet 严格校验引用载体 OriginSet 中的 Loan、Param、Static 与 Unknown 状态及可变访问权限。
// requireNonEmpty 为 true 时，空 OriginSet 触发 WON-INT-001，绝不静默放行无源借用。
func validateCarrierOriginSet(
	fn *cfg.FuncCFG,
	flow *FuncLoanFlow,
	orig OriginSet,
	place cfg.Place,
	requireNonEmpty bool,
	requireMutAccess bool,
	tctx *types.Context,
	symTab *symbols.Table,
) authorityCheckOutcome {
	if fn == nil || fn.FuncSym == nil || fn.FuncSym.ID == 0 || flow == nil || flow.FuncSymID == 0 || flow.FuncSymID != fn.FuncSym.ID {
		return internalAuthorityError("载体 OriginSet 校验缺少有效的 FuncSym/FuncLoanFlow 统一身份")
	}
	if tctx == nil {
		return internalAuthorityError("载体 OriginSet 校验缺少 TypeContext")
	}
	if orig.isEmpty() {
		if requireNonEmpty {
			return internalAuthorityError(fmt.Sprintf("位置 '%s' 需要父引用载体溯源，但 OriginSet 为空", place.String()))
		}
		return okAuthority(AuthorityMutable)
	}

	res := okAuthority(AuthorityMutable)
	rawBackedMut := false

	for _, lid := range orig.SortedLoans() {
		li := flow.LoanByID[lid]
		if chk := validateLoanRecordMetadata(fn.FuncSym.ID, li, tctx, "载体 OriginSet 借用"); chk.Internal {
			return chk
		} else if chk.Authority == AuthorityShared {
			res = combineOriginAuthorities(res, chk)
		}
		if requireMutAccess {
			if !li.IsMut {
				res = combineOriginAuthorities(res, sharedAuthority(fmt.Sprintf("借用 L%d 为只读共享借用", lid)))
			} else if li.FromRawPtr {
				rawBackedMut = true
				res = combineOriginAuthorities(res, okAuthority(AuthorityUnsafeRawPtr))
			}
		} else if li.FromRawPtr && li.IsMut {
			rawBackedMut = true
		}
	}

	for _, pIdx := range orig.SortedParams() {
		if pm, hasPaths := orig.ParamPaths[pIdx]; hasPaths && len(pm) > 0 {
			keys := make([]string, 0, len(pm))
			for k := range pm {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				srcPlace := pm[k]
				pOut := validateParamDerefChainAuthority(fn, pIdx, srcPlace, tctx, symTab)
				if pOut.Internal {
					return pOut
				}
				if pOut.Authority == AuthorityUnsafeRawPtr {
					rawBackedMut = true
				}
				res = combineOriginAuthorities(res, pOut)
			}
			continue
		}
		pOut := validateParamDerefChainAuthority(fn, pIdx, place, tctx, symTab)
		if pOut.Internal {
			return pOut
		}
		if pOut.Authority == AuthorityUnsafeRawPtr {
			rawBackedMut = true
		}
		res = combineOriginAuthorities(res, pOut)
	}

	if orig.Static {
		if requireMutAccess {
			res = combineOriginAuthorities(res, sharedAuthority("Static 字符串或静态来源不支持 &mut 可变权限"))
		} else if len(orig.Loans) == 0 && len(orig.Params) == 0 && orig.Unknown {
			return internalAuthorityError("OriginSet 同时声明 Static 与无背书 Unknown")
		}
	}

	if orig.Unknown {
		// 仅允许经类型一致性校验的 *mut T 原生指针来源在 unsafe 语境获得 AuthorityUnsafeRawPtr；
		// 孤立无有效 *mut 背书的 Unknown 绝不允许作为可变权限依据。
		if !rawBackedMut {
			if requireMutAccess {
				if len(orig.Loans) == 0 && len(orig.Params) == 0 {
					return internalAuthorityError("Unknown 溯源缺乏有效的 *mut 原生指针或引用证据，拒绝赋予可变权限")
				}
				return sharedAuthority("未知别名来源不具备可证的 &mut 访问权限")
			}
		} else {
			res = combineOriginAuthorities(res, okAuthority(AuthorityUnsafeRawPtr))
		}
	}

	return res
}

func emitInternalFlowDiag(fn *cfg.FuncCFG, report *diagnostic.Report, emitted map[diagDedupKey]bool, span diagnostic.Span, detail string) {
	fnName := "<unknown>"
	if fn != nil && fn.Name != "" {
		fnName = fn.Name
	}
	emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
		Code:        "WON-INT-001",
		Severity:    "error",
		Message:     fmt.Sprintf("所有权权限与溯源内部不变量违例 (函数 '%s'): %s", fnName, detail),
		File:        span.File,
		Span:        span,
		Explanation: "编译器在验证引用权限或借用溯源元数据时检测到缺失或不一致状态，按 Fail-Closed 原则拒绝认证。",
	})
}

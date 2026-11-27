package drop

import (
	"fmt"

	"github.com/KitiLab/WON/compiler/poc/internal/borrow"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

type absLifeBits uint8

const (
	lifeUninit absLifeBits = 1 << iota
	lifeInit
	lifeMoved
)

type absFlagBits uint8

const (
	flagFalse absFlagBits = 1 << iota
	flagTrue
)

// verifyElaboratedFunc 对已展开的 Drop CFG 执行独立的 Fail-Closed 不变量验证：
// 禁止双重析构、移动/未初始化后析构、OpStorageDead 后析构、非资源类型析构、缺失胶水/标志及逆声明序违例。
// VerifyElaboratedFuncForTest 暴露给同仓黑盒单元测试验证坏图注入时 Fail-Closed WON-INT-001 行为。
func VerifyElaboratedFuncForTest(fn *cfg.FuncCFG, df *DropElaboratedFunc, cc *ClassifyContext, view cfg.SemanticView, report *diagnostic.Report) bool {
	return verifyElaboratedFunc(fn, df, cc, view, report)
}

func verifyElaboratedFunc(fn *cfg.FuncCFG, df *DropElaboratedFunc, cc *ClassifyContext, view cfg.SemanticView, report *diagnostic.Report) bool {
	if fn == nil || df == nil || cc == nil || view == nil {
		return false
	}
	if !verifyStructuralInvariants(fn, df, cc, view, report) {
		return false
	}
	if !verifyAbstractLifecycle(fn, df, cc, view, report) {
		return false
	}
	return true
}

func verifyStructuralInvariants(fn *cfg.FuncCFG, df *DropElaboratedFunc, cc *ClassifyContext, view cfg.SemanticView, report *diagnostic.Report) bool {
	symTab := view.GetSymbols()
	tctx := view.GetTypes()
	ok := true

	// 1. 核验全部已声明标志拥有匹配的 FlagDecl 事件与合法胶水
	declFlags := make(map[DropFlagID]bool, len(df.FlagOrder))
	checkFlags := make(map[DropFlagID]bool, len(df.FlagOrder))
	for _, ev := range df.FlagEvents {
		switch ev.Op {
		case FlagDecl:
			declFlags[ev.Flag] = true
		case FlagCheck:
			checkFlags[ev.Flag] = true
		}
	}
	for _, fid := range df.FlagOrder {
		meta := df.Flags[fid]
		if meta == nil || meta.ID != fid || fid == InvalidDropFlag {
			emitVerifyErr(report, &ok, fn.Name, diagnostic.Span{}, fmt.Sprintf("DropFlag #%d 元数据不一致", fid))
			continue
		}
		if _, hasGlue := cc.glues[meta.GlueID]; meta.GlueID == "" || !hasGlue {
			emitVerifyErr(report, &ok, fn.Name, meta.Span, fmt.Sprintf("DropFlag df%d 缺少合法 DropGlue (%q)", fid, meta.GlueID))
		}
		if !declFlags[fid] {
			emitVerifyErr(report, &ok, fn.Name, meta.Span, fmt.Sprintf("DropFlag df%d 缺少入口 FlagDecl 事件", fid))
		}
		if !checkFlags[fid] {
			emitVerifyErr(report, &ok, fn.Name, meta.Span, fmt.Sprintf("DropFlag df%d 未绑定任何 OpCondDrop 检查事件", fid))
		}
	}

	// 2. 逐基本块校验声明顺序与存储终结前约束
	for _, blk := range fn.Blocks {
		if blk == nil {
			continue
		}
		deadSymSet := make(map[symbols.SymbolID]bool)
		var scopeDropRoots []symbols.SymbolID
		seenScopeExit := false
		seenStorageDead := false

		for _, st := range blk.Statements {
			switch st.Kind {
			case cfg.OpInitAssign, cfg.OpUninitDecl, cfg.OpStorageDecl:
				delete(deadSymSet, st.Place.RootSymID)

			case cfg.OpStorageDead:
				seenStorageDead = true
				for _, sid := range st.Locals {
					deadSymSet[sid] = true
				}

			case cfg.OpScopeExit:
				seenScopeExit = true
				if errMsg := verifyReverseDeclOrder(st.Locals, scopeDropRoots); errMsg != "" {
					emitVerifyErr(report, &ok, fn.Name, st.Span, errMsg)
				}
				scopeDropRoots = nil

			case cfg.OpDrop, cfg.OpCondDrop:
				if deadSymSet[st.Place.RootSymID] {
					emitVerifyErr(report, &ok, fn.Name, st.Span, fmt.Sprintf("检测到 OpStorageDead 之后的非法析构操作: %s", st.Place.String()))
				}
				if seenStorageDead && !seenScopeExit {
					// 同块连续退出区间内，禁止 OpDrop 越过先前 OpStorageDead
					if deadSymSet[st.Place.RootSymID] {
						emitVerifyErr(report, &ok, fn.Name, st.Span, fmt.Sprintf("析构操作出现在存储生命周期终结之后: %s", st.Place.String()))
					}
				}
				checkDropStmtMetadata(fn.Name, st, df, cc, symTab, tctx, report, &ok)
				if len(scopeDropRoots) == 0 || scopeDropRoots[len(scopeDropRoots)-1] != st.Place.RootSymID {
					scopeDropRoots = append(scopeDropRoots, st.Place.RootSymID)
				}
			}
		}
	}
	return ok
}

func checkDropStmtMetadata(fnName string, st cfg.Statement, df *DropElaboratedFunc, cc *ClassifyContext, symTab *symbols.Table, tctx *types.Context, report *diagnostic.Report, ok *bool) {
	if st.Place.RootSymID == 0 || symTab.ByID[st.Place.RootSymID] == nil {
		emitVerifyErr(report, ok, fnName, st.Span, fmt.Sprintf("%s 引用无效符号 '%s'", st.Kind, st.Place.String()))
		return
	}
	targetType := st.Type
	if targetType == types.Invalid {
		targetType = st.Place.ElemType
	}
	if !cc.NeedsDrop(targetType) {
		emitVerifyErr(report, ok, fnName, st.Span, fmt.Sprintf("禁止对无需 Drop 的类型 '%s' (%s) 生成析构事件", tctx.Format(targetType), st.Place.String()))
	}
	expectedGlue, need := cc.ResolveDropGlue(targetType)
	if !need || expectedGlue == nil || st.DropGlue != expectedGlue.ID {
		emitVerifyErr(report, ok, fnName, st.Span, fmt.Sprintf("%s 绑定胶水 '%s' 与类型 '%s' 不匹配", st.Kind, st.DropGlue, tctx.Format(targetType)))
	}
	if st.Kind == cfg.OpCondDrop {
		fid := DropFlagID(st.DropFlag)
		if fid == InvalidDropFlag || df.Flags[fid] == nil {
			emitVerifyErr(report, ok, fnName, st.Span, fmt.Sprintf("OpCondDrop '%s' 缺少有效 DropFlagID (%d)", st.Place.String(), st.DropFlag))
		}
	}
}

func verifyReverseDeclOrder(locals []symbols.SymbolID, observedDrops []symbols.SymbolID) string {
	if len(observedDrops) <= 1 || len(locals) <= 1 {
		return ""
	}
	localPos := make(map[symbols.SymbolID]int, len(locals))
	for idx, sid := range locals {
		localPos[sid] = idx
	}
	lastDeclPos := len(locals) + 1
	for _, root := range observedDrops {
		pos, inScope := localPos[root]
		if !inScope {
			continue
		}
		if pos > lastDeclPos {
			return fmt.Sprintf("作用域析构违反声明逆序 (LIFO) 契约：符号 #%d 晚于先声明符号 #%d 析构", root, locals[lastDeclPos])
		}
		lastDeclPos = pos
	}
	return ""
}

// verifyAbstractLifecycle 在最终 CFG 上执行独立于借用器内部细节的抽象生命期+标志双通道校验，
// 从结果图本身反向证明每条路径上绝不会发生重复释放、释放未初始化值或释放已移动值。
func verifyAbstractLifecycle(fn *cfg.FuncCFG, df *DropElaboratedFunc, cc *ClassifyContext, view cfg.SemanticView, report *diagnostic.Report) bool {
	if len(fn.Blocks) == 0 || fn.Entry < 0 {
		return true
	}
	n := len(fn.Blocks)
	reach := computeCFGReachable(fn)
	inLife := make([]map[string]absLifeBits, n)
	outLife := make([]map[string]absLifeBits, n)
	inFlags := make([]map[DropFlagID]absFlagBits, n)
	outFlags := make([]map[DropFlagID]absFlagBits, n)

	maxIters := n*(len(df.Flags)+len(fn.Locals)+16) + 128
	changed := true
	iters := 0
	ok := true

	for changed {
		iters++
		if iters > maxIters {
			emitVerifyErr(report, &ok, fn.Name, diagnostic.Span{}, fmt.Sprintf("Drop 验证不动点超过最大步数 (%d)", maxIters))
			return false
		}
		changed = false
		for bbIdx, blk := range fn.Blocks {
			bb := cfg.BlockID(bbIdx)
			if !reach[bb] || blk == nil {
				continue
			}

			var mLife map[string]absLifeBits
			var mFlag map[DropFlagID]absFlagBits
			if bb == fn.Entry {
				mLife = make(map[string]absLifeBits)
				mFlag = make(map[DropFlagID]absFlagBits, len(df.FlagOrder))
				for _, fid := range df.FlagOrder {
					mFlag[fid] = flagFalse
				}
			} else {
				mLife = make(map[string]absLifeBits)
				mFlag = make(map[DropFlagID]absFlagBits)
				allKeys := make(map[string]bool)
				for _, pred := range blk.Predecessors {
					if !reach[pred] || outLife[pred] == nil {
						continue
					}
					for k := range outLife[pred] {
						allKeys[k] = true
					}
				}
				for _, pred := range blk.Predecessors {
					if !reach[pred] || outLife[pred] == nil {
						continue
					}
					for k := range allKeys {
						if bits := resolveKeyBits(outLife[pred], k); bits != 0 {
							mLife[k] |= bits
						}
					}
					for fid, fv := range outFlags[pred] {
						mFlag[fid] |= fv
					}
				}
			}

			inLife[bb] = mLife
			inFlags[bb] = mFlag
			curLife := cloneLifeMap(mLife)
			curFlags := cloneFlagMap(mFlag)
			stepVerifyBlock(blk, fn, df, cc, curLife, curFlags, nil, nil)

			if !equalLifeMaps(outLife[bb], curLife) || !equalFlagMaps(outFlags[bb], curFlags) {
				outLife[bb] = curLife
				outFlags[bb] = curFlags
				changed = true
			}
		}
	}

	// 收敛后单遍校验 OpDrop / OpCondDrop 安全性
	for bbIdx, blk := range fn.Blocks {
		bb := cfg.BlockID(bbIdx)
		if !reach[bb] || blk == nil {
			continue
		}
		curLife := cloneLifeMap(inLife[bb])
		curFlags := cloneFlagMap(inFlags[bb])
		stepVerifyBlock(blk, fn, df, cc, curLife, curFlags, report, &ok)
	}
	return ok
}

func stepVerifyBlock(blk *cfg.BasicBlock, fn *cfg.FuncCFG, df *DropElaboratedFunc, cc *ClassifyContext, life map[string]absLifeBits, flags map[DropFlagID]absFlagBits, report *diagnostic.Report, ok *bool) {
	for _, st := range blk.Statements {
		pk := canonicalPlaceKey(st.Place)
		switch st.Kind {
		case cfg.OpUninitDecl:
			life[pk] = lifeUninit

		case cfg.OpInitAssign, cfg.OpStorageDecl, cfg.OpAssign, cfg.OpReinit:
			isNonOwningCopy := st.CopyPlace.RootSymID != 0 && cc != nil &&
				(borrow.IsTrackableRawCopy(st.Type, st.CopyPlace, cc.Types, cc.Symbols, nil) || cc.NeedsDrop(st.Type))
			if isNonOwningCopy {
				if len(st.Place.Projections) == 0 {
					setPlaceTreeLife(life, df, st.Place, lifeMoved)
				} else {
					life[pk] = lifeMoved
				}
				for _, fid := range df.FlagOrder {
					fm := df.Flags[fid]
					if placeCoversFlag(st.Place, fm.Place) {
						flags[fid] = flagFalse
					}
				}
				continue
			}
			if len(st.Place.Projections) == 0 {
				setPlaceTreeLife(life, df, st.Place, lifeInit)
			} else {
				life[pk] = lifeInit
			}
			for _, fid := range df.FlagOrder {
				fm := df.Flags[fid]
				if placeCoversFlag(st.Place, fm.Place) {
					flags[fid] = flagTrue
				}
			}

		case cfg.OpMove, cfg.OpFFITransfer:
			if st.Place.HasDeref() || st.Place.HasIndex() {
				continue
			}
			setPlaceTreeLife(life, df, st.Place, lifeMoved)
			for _, fid := range df.FlagOrder {
				fm := df.Flags[fid]
				if placeCoversFlag(st.Place, fm.Place) {
					flags[fid] = flagFalse
				}
			}

		case cfg.OpDrop:
			if st.Place.HasDeref() || st.Place.HasIndex() {
				continue
			}
			bits := resolveLifeBits(life, pk, st.Place)
			if bits != lifeInit {
				emitVerifyErr(report, ok, fn.Name, st.Span,
					fmt.Sprintf("禁止对非确定存活状态 (%s) 位置 '%s' 执行无条件 OpDrop", formatLifeBits(bits), st.Place.String()))
			}
			setPlaceTreeLife(life, df, st.Place, lifeMoved)
			for _, fid := range df.FlagOrder {
				fm := df.Flags[fid]
				if placeCoversFlag(st.Place, fm.Place) {
					flags[fid] = flagFalse
				}
			}

		case cfg.OpCondDrop:
			fid := DropFlagID(st.DropFlag)
			fbits := flags[fid]
			bits := resolveLifeBits(life, pk, st.Place)
			// 若标志可能为 true 而目标实际可能已处于 Uninit/Moved（且无法由标志区分），则存在双重析构/未初始化析构风险
			if (fbits&flagTrue) != 0 && (bits&lifeInit) == 0 {
				emitVerifyErr(report, ok, fn.Name, st.Span,
					fmt.Sprintf("OpCondDrop 标志 df%d 可能为 true，但位置 '%s' 处于不可释放状态 (%s)", fid, st.Place.String(), formatLifeBits(bits)))
			}
			flags[fid] = flagFalse
			setPlaceTreeLife(life, df, st.Place, lifeMoved)

		case cfg.OpStorageDead:
			for _, sid := range st.Locals {
				prefix := fmt.Sprintf("sym#%d", sid)
				for k := range life {
					if k == prefix || (len(k) > len(prefix) && k[:len(prefix)] == prefix && (k[len(prefix)] == '.' || k[len(prefix)] == '[')) {
						delete(life, k)
					}
				}
			}
		}
	}
}

func resolveLifeBits(life map[string]absLifeBits, pk string, place cfg.Place) absLifeBits {
	return resolveKeyBits(life, pk)
}

func resolveKeyBits(life map[string]absLifeBits, pk string) absLifeBits {
	if v, ok := life[pk]; ok && v != 0 {
		return v
	}
	// 沿着字段/元组祖先路径逐级回退查找未显式拆分的父聚合状态（与 Phase 2B.1 惰性展开格语义一致）
	for i := len(pk) - 1; i > 0; i-- {
		if pk[i] == '.' || pk[i] == '[' {
			if v, ok := life[pk[:i]]; ok && v != 0 {
				return v
			}
		}
	}
	return 0
}

func setPlaceTreeLife(life map[string]absLifeBits, df *DropElaboratedFunc, place cfg.Place, val absLifeBits) {
	pk := canonicalPlaceKey(place)
	life[pk] = val
	for k := range life {
		if k != pk && len(k) > len(pk) && k[:len(pk)] == pk && (k[len(pk)] == '.' || k[len(pk)] == '[') {
			life[k] = val
		}
	}
	for _, fid := range df.FlagOrder {
		fm := df.Flags[fid]
		if placeCoversFlag(place, fm.Place) {
			life[canonicalPlaceKey(fm.Place)] = val
		}
	}
}

func computeCFGReachable(fn *cfg.FuncCFG) []bool {
	reach := make([]bool, len(fn.Blocks))
	if fn.Entry < 0 || int(fn.Entry) >= len(fn.Blocks) {
		return reach
	}
	q := []cfg.BlockID{fn.Entry}
	reach[fn.Entry] = true
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		for _, s := range fn.Blocks[cur].Successors {
			if s >= 0 && int(s) < len(fn.Blocks) && !reach[s] {
				reach[s] = true
				q = append(q, s)
			}
		}
	}
	return reach
}

func emitVerifyErr(report *diagnostic.Report, ok *bool, fnName string, span diagnostic.Span, detail string) {
	if ok != nil {
		*ok = false
	}
	if report != nil {
		report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        "WON-INT-001",
			Severity:    "error",
			Message:     fmt.Sprintf("Drop 展开后内部不变量验证失败 (函数 '%s'): %s", fnName, detail),
			File:        span.File,
			Span:        span,
			Explanation: "编译器在 Drop Elaboration 后置验证中检测到潜在双重释放、未初始化释放、死区释放或声明逆序违例，按 Fail-Closed 策略拒绝认证。",
		})
	}
}

func formatLifeBits(b absLifeBits) string {
	switch b {
	case lifeUninit:
		return "Uninit"
	case lifeInit:
		return "Init"
	case lifeMoved:
		return "Moved"
	case 0:
		return "Untracked"
	default:
		return fmt.Sprintf("Mixed(0x%x)", uint8(b))
	}
}

func cloneLifeMap(m map[string]absLifeBits) map[string]absLifeBits {
	cp := make(map[string]absLifeBits, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func cloneFlagMap(m map[DropFlagID]absFlagBits) map[DropFlagID]absFlagBits {
	cp := make(map[DropFlagID]absFlagBits, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func equalLifeMaps(a, b map[string]absLifeBits) bool {
	if a == nil || b == nil || len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func equalFlagMaps(a, b map[DropFlagID]absFlagBits) bool {
	if a == nil || b == nil || len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

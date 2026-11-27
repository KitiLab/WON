package borrow

import (
	"fmt"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

type diagDedupKey struct {
	code string
	file string
	byte int
}

// AnalyzeProgram 对全程序所有权 CFG 运行完整的 Phase 2B.1 Move/Init、Stage A 借用流、Stage C 候选溯源以及 Stage B NLL/借用冲突校验。
// 遵循严格 Fail-Closed 原则：内部 CFG/状态/收敛不变量违例触发 WON-INT-001，绝不静默放行。
func AnalyzeProgram(progCFG *cfg.ProgramCFG, view cfg.SemanticView, report *diagnostic.Report) {
	if progCFG == nil || view == nil || report == nil {
		return
	}

	// Pass 1: Definite Initialization & Move Analysis (Phase 2B.1)
	for _, fn := range progCFG.Functions {
		maxIterations := len(fn.Blocks)*(len(fn.Locals)*8+64) + 256
		analyzeMoveInitWithBudget(fn, view, report, maxIterations)
	}
	if report.HasErrors() {
		return
	}

	// Pass 2 & 3: Stage A 借用载体流 + Stage C 全程序 Tarjan SCC 候选引用溯源推理与摘要精确化
	provRes := InferAndVerifyProvenance(progCFG, view, report)
	if provRes == nil || report.HasErrors() {
		return
	}

	// Pass 4: Stage B 基于精确化候选摘要的 NLL 向后活跃域、XOR 排他冲突与作用域生命周期校验
	for _, fn := range progCFG.Functions {
		if fn.FuncSym == nil {
			continue
		}
		if flow := provRes.Flows[fn.FuncSym.ID]; flow != nil {
			AnalyzeBorrowConflicts(fn, flow, view, report)
		}
	}
}

// MoveInitTrace 导出 Phase 2B.1 已收敛的可达基本块及每块入口精确初始化/移动/局部移动状态，
// 供 Phase 2B.3 Drop Elaboration 直接消费，禁止重复发明两套移动判定规则。
type MoveInitTrace struct {
	Reachable []bool
	InStates  []map[symbols.SymbolID]*PlaceState
}

// CloneInEnv 返回指定基本块入口状态环境的深拷贝。
func (t *MoveInitTrace) CloneInEnv(bb cfg.BlockID) map[symbols.SymbolID]*PlaceState {
	if t == nil || int(bb) < 0 || int(bb) >= len(t.InStates) || t.InStates[bb] == nil {
		return make(map[symbols.SymbolID]*PlaceState)
	}
	return cloneEnv(t.InStates[bb])
}

// StepStatement 对单条 CFG 语句执行 Phase 2B.1 状态转移（不采集重复诊断），
// 使 Drop Elaboration 能够在语句级别精确同步前/后状态。
func StepStatement(fnName string, env map[symbols.SymbolID]*PlaceState, st cfg.Statement, view cfg.SemanticView) {
	StepStatementWithFFI(fnName, env, st, view, nil)
}

// SolveMoveInitTrace 对单个函数求解并导出 Phase 2B.1 Move/Init 不动点收敛结果。
func SolveMoveInitTrace(fn *cfg.FuncCFG, view cfg.SemanticView, report *diagnostic.Report) (*MoveInitTrace, bool) {
	return SolveMoveInitTraceWithFFI(fn, view, nil, report)
}

func analyzeFunctionWithBudget(fn *cfg.FuncCFG, view cfg.SemanticView, report *diagnostic.Report, maxIterations int) {
	_, _ = solveMoveInitWithFFI(fn, view, nil, report, maxIterations, true)
}

func analyzeMoveInitWithBudget(fn *cfg.FuncCFG, view cfg.SemanticView, report *diagnostic.Report, maxIterations int) {
	_, _ = solveMoveInitWithFFI(fn, view, nil, report, maxIterations, true)
}

func solveMoveInit(fn *cfg.FuncCFG, view cfg.SemanticView, report *diagnostic.Report, maxIterations int, emitDiags bool) (*MoveInitTrace, bool) {
	return solveMoveInitWithFFI(fn, view, nil, report, maxIterations, emitDiags)
}

func solveMoveInitWithFFI(fn *cfg.FuncCFG, view cfg.SemanticView, opaqueTypes map[string]bindgen.FFIReturnContract, report *diagnostic.Report, maxIterations int, emitDiags bool) (*MoveInitTrace, bool) {
	if fn == nil || len(fn.Blocks) == 0 || fn.Entry < 0 {
		return nil, false
	}

	reachable := computeReachable(fn)
	inStates := make([]funcEnv, len(fn.Blocks))
	outStates := make([]funcEnv, len(fn.Blocks))

	// 使用基于可达工作表的单调有限格迭代直至全部可达基本块状态收敛
	changed := true
	iters := 0
	for changed {
		iters++
		if iters > maxIterations {
			var fnSpan diagnostic.Span
			if fn.Decl != nil {
				fnSpan = fn.Decl.GetSpan()
			}
			if report != nil {
				report.AddDiagnostic(diagnostic.Diagnostic{
					Code:        "WON-INT-001",
					Severity:    "error",
					Message:     fmt.Sprintf("所有权数据流不动点迭代在函数 '%s' 中超过安全上限 (%d 轮) 仍未收敛", fn.Name, maxIterations),
					File:        fnSpan.File,
					Span:        fnSpan,
					Explanation: "此错误表示编译器内部格传递函数或工作表在给定 CFG 上未按预期单调收敛；编译器按 Fail-Closed 原则拒绝认证该函数。",
				})
			}
			return nil, false
		}
		changed = false
		for bbIdx, blk := range fn.Blocks {
			bb := cfg.BlockID(bbIdx)
			if !reachable[bb] {
				continue
			}

			var merged funcEnv
			if bb == fn.Entry {
				merged = make(funcEnv)
			} else {
				predEnvs := make([]funcEnv, 0, len(blk.Predecessors))
				for _, pred := range blk.Predecessors {
					if reachable[pred] && outStates[pred] != nil {
						predEnvs = append(predEnvs, outStates[pred])
					}
				}
				merged = joinEnvs(predEnvs)
			}

			inStates[bb] = merged
			cur := cloneEnv(merged)
			transferBlockWithFFI(fn.Name, blk, cur, view, opaqueTypes, nil, nil)

			if outStates[bb] == nil || !equalEnvs(outStates[bb], cur) {
				outStates[bb] = cur
				changed = true
			}
		}
	}

	if emitDiags {
		// 收敛后按确定性基本块顺序对可达路径执行唯一一次诊断采集 Pass，避免工作表中间迭代产生重复报错
		emitted := make(map[diagDedupKey]bool)
		for bbIdx, blk := range fn.Blocks {
			bb := cfg.BlockID(bbIdx)
			if !reachable[bb] {
				continue
			}
			cur := cloneEnv(inStates[bb])
			transferBlockWithFFI(fn.Name, blk, cur, view, opaqueTypes, report, emitted)
		}
	}

	exported := make([]map[symbols.SymbolID]*PlaceState, len(inStates))
	for i, env := range inStates {
		if env != nil {
			exported[i] = cloneEnv(env)
		}
	}
	return &MoveInitTrace{
		Reachable: reachable,
		InStates:  exported,
	}, true
}

func computeReachable(fn *cfg.FuncCFG) []bool {
	reach := make([]bool, len(fn.Blocks))
	if fn.Entry < 0 || int(fn.Entry) >= len(fn.Blocks) {
		return reach
	}
	queue := []cfg.BlockID{fn.Entry}
	reach[fn.Entry] = true
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, succ := range fn.Blocks[cur].Successors {
			if succ >= 0 && int(succ) < len(fn.Blocks) && !reach[succ] {
				reach[succ] = true
				queue = append(queue, succ)
			}
		}
	}
	return reach
}

func emitInternalViolation(fnName string, span diagnostic.Span, place cfg.Place, detail string, report *diagnostic.Report, emitted map[diagDedupKey]bool) {
	if report == nil {
		return
	}
	emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
		Code:        "WON-INT-001",
		Severity:    "error",
		Message:     fmt.Sprintf("所有权 CFG 内部不变量违例 (函数 '%s', 位置 '%s'): %s", fnName, formatPlaceDisplay(place), detail),
		File:        span.File,
		Span:        span,
		Explanation: fmt.Sprintf("编译器未能为已解析符号 #%d (%s) 维护完整的 CFG 所有权状态，按 Fail-Closed 策略拒绝认证该程序。", place.RootSymID, place.RootName),
	})
}

func transferBlock(fnName string, blk *cfg.BasicBlock, env funcEnv, view cfg.SemanticView, report *diagnostic.Report, emitted map[diagDedupKey]bool) {
	transferBlockWithFFI(fnName, blk, env, view, nil, report, emitted)
}

func transferBlockWithFFI(fnName string, blk *cfg.BasicBlock, env funcEnv, view cfg.SemanticView, opaqueTypes map[string]bindgen.FFIReturnContract, report *diagnostic.Report, emitted map[diagDedupKey]bool) {
	symTab := view.GetSymbols()
	tctx := view.GetTypes()

	for _, st := range blk.Statements {
		stepBlockStmtWithFFI(fnName, st, env, symTab, tctx, opaqueTypes, report, emitted)
	}
}

func stepBlockStmt(fnName string, st cfg.Statement, env funcEnv, symTab *symbols.Table, tctx *types.Context, report *diagnostic.Report, emitted map[diagDedupKey]bool) {
	stepBlockStmtWithFFI(fnName, st, env, symTab, tctx, nil, report, emitted)
}

func stepBlockStmtWithFFI(fnName string, st cfg.Statement, env funcEnv, symTab *symbols.Table, tctx *types.Context, opaqueTypes map[string]bindgen.FFIReturnContract, report *diagnostic.Report, emitted map[diagDedupKey]bool) {
	switch st.Kind {
	case cfg.OpScopeEnter, cfg.OpNop, cfg.OpStaticRef, cfg.OpCall, cfg.OpAggregate, cfg.OpTempAssign,
		cfg.OpDrop, cfg.OpCondDrop:
		return

	case cfg.OpStorageDead:
		for _, sid := range st.Locals {
			delete(env, sid)
		}

	case cfg.OpScopeExit:
		for _, sid := range st.Locals {
			delete(env, sid)
		}

	case cfg.OpUninitDecl:
		if st.Place.RootSymID == 0 || symTab.ByID[st.Place.RootSymID] == nil {
			emitInternalViolation(fnName, st.Span, st.Place, "OpUninitDecl 缺少有效 SymbolID", report, emitted)
			return
		}
		env[st.Place.RootSymID] = newPlaceState(st.Type, st.Span, false)

	case cfg.OpInitAssign, cfg.OpStorageDecl:
		if st.Place.RootSymID == 0 || symTab.ByID[st.Place.RootSymID] == nil {
			emitInternalViolation(fnName, st.Span, st.Place, fmt.Sprintf("%s 缺少有效 SymbolID", st.Kind), report, emitted)
			return
		}
		ps := newPlaceState(st.Type, st.Span, true)
		if st.Kind == cfg.OpInitAssign && st.CopyPlace.RootSymID != 0 && isTrackableRawCopy(st.Type, st.CopyPlace, tctx, symTab, opaqueTypes) {
			ps.markNonOwningCopy(collectCopyAliasRoots(env, st.CopyPlace))
		}
		env[st.Place.RootSymID] = ps

	case cfg.OpRead, cfg.OpCopy:
		checkReadablePlace(fnName, env, st.Place, st.Span, symTab, tctx, report, emitted)

	case cfg.OpBorrowShared, cfg.OpBorrowMut, cfg.OpReborrow:
		// Phase 2B.1 保留事件：借用冲突检查留待 Phase 2B.2，但仍严格要求被借用根符号拥有合法 CFG 状态
		if st.Place.RootSymID == 0 || symTab.ByID[st.Place.RootSymID] == nil || env[st.Place.RootSymID] == nil {
			emitInternalViolation(fnName, st.Span, st.Place, fmt.Sprintf("%s 引用了未在 CFG 环境中追踪的位置", st.Kind), report, emitted)
		}

	case cfg.OpReturn:
		if st.Place.RootSymID != 0 {
			if symTab.ByID[st.Place.RootSymID] == nil || env[st.Place.RootSymID] == nil {
				emitInternalViolation(fnName, st.Span, st.Place, "OpReturn 引用了缺失状态的返回源位置", report, emitted)
			}
		}

	case cfg.OpMove:
		if st.Place.RootSymID == 0 || symTab.ByID[st.Place.RootSymID] == nil || env[st.Place.RootSymID] == nil {
			emitInternalViolation(fnName, st.Span, st.Place, "OpMove 缺少有效符号或未追踪的根存储状态", report, emitted)
			return
		}

		if st.Place.HasDeref() {
			checkReadablePlace(fnName, env, st.Place.RootPlace(), st.Span, symTab, tctx, report, emitted)
			if report != nil {
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-MEM-008",
					Severity:    "error",
					Message:     fmt.Sprintf("不能通过解引用 '%s' 移出非 Copy 类型 '%s' 的所有权", formatPlaceDisplay(st.Place), tctx.Format(st.Type)),
					File:        st.Span.File,
					Span:        st.Span,
					Explanation: "安全借用或原生指针解引用只允许借用访问或读取 Copy 类型成员，禁止在不替换新值的情况下移出所有权。",
				})
			}
			return
		}

		if st.Place.HasIndex() {
			checkReadablePlace(fnName, env, st.Place.RootPlace(), st.Span, symTab, tctx, report, emitted)
			if report != nil {
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-MEM-008",
					Severity:    "error",
					Message:     fmt.Sprintf("不能从数组或切片下标位置直接移出非 Copy 元素 ('%s')", formatPlaceDisplay(st.Place)),
					File:        st.Span.File,
					Span:        st.Span,
					Explanation: "Phase 2B.1 保守禁止从动态或定长索引位置直接按值移出非 Copy 元素；请改用借用访问。",
				})
			}
			return
		}

		ok := checkReadablePlace(fnName, env, st.Place, st.Span, symTab, tctx, report, emitted)
		if ok {
			applyMove(fnName, env, st.Place, st.Span, symTab, tctx, report, emitted)
		}

	case cfg.OpFFITransfer:
		if st.Place.RootSymID == 0 || symTab.ByID[st.Place.RootSymID] == nil || env[st.Place.RootSymID] == nil {
			emitInternalViolation(fnName, st.Span, st.Place, "OpFFITransfer 缺少有效符号或未追踪的根存储状态", report, emitted)
			return
		}
		if st.Place.HasDeref() || st.Place.HasIndex() {
			checkReadablePlace(fnName, env, st.Place.RootPlace(), st.Span, symTab, tctx, report, emitted)
			if report != nil {
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-MEM-008",
					Severity:    "error",
					Message:     fmt.Sprintf("不能直接通过解引用或下标位置 '%s' 向 FFI @owned 移交所有权", formatPlaceDisplay(st.Place)),
					File:        st.Span.File,
					Span:        st.Span,
					Explanation: "FFI @owned 所有权移交要求持有独立所有权的局部绑定或结构体字段路径。",
				})
			}
			return
		}
		if !checkReadablePlace(fnName, env, st.Place, st.Span, symTab, tctx, report, emitted) {
			return
		}
		fields, ok := st.Place.FieldPath()
		if !ok {
			emitInternalViolation(fnName, st.Span, st.Place, "OpFFITransfer 路径包含不受支持的投影类型", report, emitted)
			return
		}
		target, err := resolveFieldState(env[st.Place.RootSymID], fields, symTab, tctx)
		if err != nil {
			emitInternalViolation(fnName, st.Span, st.Place, err.Error(), report, emitted)
			return
		}
		_, _, _, _, canNonOwningCopy, _, _ := target.summaryOwnership()
		if canNonOwningCopy {
			if report != nil {
				emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
					Code:        "WON-MEM-001",
					Severity:    "error",
					Message:     fmt.Sprintf("不能通过复制得到的裸指针或句柄别名 '%s' 向 FFI 移交资源所有权", formatPlaceDisplay(st.Place)),
					File:        st.Span.File,
					Span:        st.Span,
					Explanation: "复制 Copy 裸指针值仅拷贝地址本身，不赋予副本向外部 C 函数移交或释放底层资源的权限。",
				})
			}
			return
		}
		applyMove(fnName, env, st.Place, st.Span, symTab, tctx, report, emitted)

	case cfg.OpAssign, cfg.OpReinit:
		applyAssignWithFFI(fnName, env, st, symTab, tctx, opaqueTypes, report, emitted)

	default:
		// 对任何尚未接管的可执行/未知 CFG 操作一律快速失败，绝不静默跳过
		emitInternalViolation(fnName, st.Span, st.Place, fmt.Sprintf("遇到未受支持的 CFG 操作 %s", st.Kind), report, emitted)
	}
}

func checkReadablePlace(fnName string, env funcEnv, place cfg.Place, useSpan diagnostic.Span, symTab *symbols.Table, tctx *types.Context, report *diagnostic.Report, emitted map[diagDedupKey]bool) bool {
	if place.RootSymID == 0 || symTab.ByID[place.RootSymID] == nil {
		emitInternalViolation(fnName, useSpan, place, "读取操作指向无效或缺失的符号标识", report, emitted)
		return false
	}
	rootState, exists := env[place.RootSymID]
	if !exists || rootState == nil {
		emitInternalViolation(fnName, useSpan, place, "读取操作对应的局部或参数绑定在 CFG 中未初始化声明", report, emitted)
		return false
	}

	var targetState *PlaceState
	exactPath := false
	disp := formatPlaceDisplay(place)

	if fields, isFieldPath := place.FieldPath(); isFieldPath {
		resolved, err := resolveFieldState(rootState, fields, symTab, tctx)
		if err != nil {
			emitInternalViolation(fnName, useSpan, place, err.Error(), report, emitted)
			return false
		}
		targetState = resolved
		exactPath = true
	} else if place.HasDeref() || place.HasIndex() {
		// 对 *ptr 或 arr[i] 读取需确保前缀根绑定本身已完成初始化且未被移动
		if fieldsBefore, hasPrefixFields := fieldPrefixBeforeDerefOrIndex(place); hasPrefixFields {
			resolved, err := resolveFieldState(rootState, fieldsBefore, symTab, tctx)
			if err != nil {
				emitInternalViolation(fnName, useSpan, place, err.Error(), report, emitted)
				return false
			}
			targetState = resolved
		} else {
			targetState = rootState
		}
	} else {
		// 遇到未支持的投影类型（如未实现的 ProjTupleField / ProjDowncast）立即 Fail-Closed
		emitInternalViolation(fnName, useSpan, place, "读取位置包含当前阶段尚未支持的投影种类", report, emitted)
		return false
	}

	canUninit, _, _, canMoved, canNonOwningCopy, aliasRoots, moveSpans := targetState.summaryOwnership()
	if !canUninit && !canMoved {
		if canNonOwningCopy {
			for _, ar := range aliasRoots {
				if srcState := env[ar]; srcState != nil {
					_, _, srcMoved, srcMoveSpans := srcState.summaryState()
					if srcMoved {
						if report != nil {
							hint := ""
							if len(srcMoveSpans) > 0 {
								hint = fmt.Sprintf("（原所有权已于第 %d 行第 %d 列移出）", srcMoveSpans[0].Start.Line, srcMoveSpans[0].Start.Column)
							}
							emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
								Code:        "WON-MEM-001",
								Severity:    "error",
								Message:     fmt.Sprintf("使用底层资源已被移出的裸指针别名 '%s'%s", disp, hint),
								File:        useSpan.File,
								Span:        useSpan,
								Explanation: "原持有所有权的指针/句柄已通过移动或 FFI @owned 移交失效，不能继续通过此前复制出的裸指针别名访问或移交该资源。",
							})
						}
						return false
					}
				}
			}
		}
		return true
	}

	if report != nil {
		if !exactPath && place.HasDeref() {
			disp = place.RootName
		}
		if canUninit && !canMoved {
			emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
				Code:        "WON-MEM-010",
				Severity:    "error",
				Message:     fmt.Sprintf("在所有可达执行路径上完成确定性初始化之前读取变量 '%s'", disp),
				File:        useSpan.File,
				Span:        useSpan,
				Explanation: fmt.Sprintf("绑定 '%s' 在声明于第 %d 行后存在未初始化即到达此处的控制流路径。", place.RootName, rootState.DeclSpan.Start.Line),
			})
		} else if canMoved && !canUninit {
			hint := ""
			if len(moveSpans) > 0 {
				hint = fmt.Sprintf("（此前所有权已于第 %d 行第 %d 列移出）", moveSpans[0].Start.Line, moveSpans[0].Start.Column)
			}
			emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
				Code:        "WON-MEM-001",
				Severity:    "error",
				Message:     fmt.Sprintf("使用已被移动或部分移动的值 '%s'%s", disp, hint),
				File:        useSpan.File,
				Span:        useSpan,
				Explanation: fmt.Sprintf("非 Copy 值 '%s' 所有权已发生转移，不能在未通过 'var' 重新赋值恢复之前再次读取、复制或移动。", disp),
			})
		} else {
			hint := ""
			if len(moveSpans) > 0 {
				hint = fmt.Sprintf("；此外值在另一条路径上曾于第 %d 行移出", moveSpans[0].Start.Line)
			}
			emitUniqueDiag(report, emitted, diagnostic.Diagnostic{
				Code:        "WON-MEM-010",
				Severity:    "error",
				Message:     fmt.Sprintf("读取可能未初始化或已在部分路径被移动的值 '%s'", disp),
				File:        useSpan.File,
				Span:        useSpan,
				Explanation: fmt.Sprintf("绑定 '%s' 在部分控制流路径上可能尚未初始化（声明于第 %d 行）%s。", disp, rootState.DeclSpan.Start.Line, hint),
			})
		}
	}
	return false
}

func applyMove(fnName string, env funcEnv, place cfg.Place, moveSpan diagnostic.Span, symTab *symbols.Table, tctx *types.Context, report *diagnostic.Report, emitted map[diagDedupKey]bool) {
	if place.RootSymID == 0 || symTab.ByID[place.RootSymID] == nil {
		emitInternalViolation(fnName, moveSpan, place, "Move 目标缺乏有效符号标识", report, emitted)
		return
	}
	rootState, exists := env[place.RootSymID]
	if !exists || rootState == nil {
		emitInternalViolation(fnName, moveSpan, place, "Move 目标在 CFG 中未拥有已追踪状态", report, emitted)
		return
	}
	fields, ok := place.FieldPath()
	if !ok {
		emitInternalViolation(fnName, moveSpan, place, "Move 路径包含不支持按字段局部移出的投影类型", report, emitted)
		return
	}
	target, err := resolveFieldState(rootState, fields, symTab, tctx)
	if err != nil {
		emitInternalViolation(fnName, moveSpan, place, err.Error(), report, emitted)
		return
	}
	target.markMoved(moveSpan)
}

func applyAssign(fnName string, env funcEnv, place cfg.Place, assignSpan diagnostic.Span, symTab *symbols.Table, tctx *types.Context, report *diagnostic.Report, emitted map[diagDedupKey]bool) {
	applyAssignWithFFI(fnName, env, cfg.Statement{Kind: cfg.OpAssign, Place: place, Span: assignSpan}, symTab, tctx, nil, report, emitted)
}

func applyAssignWithFFI(fnName string, env funcEnv, st cfg.Statement, symTab *symbols.Table, tctx *types.Context, opaqueTypes map[string]bindgen.FFIReturnContract, report *diagnostic.Report, emitted map[diagDedupKey]bool) {
	place := st.Place
	assignSpan := st.Span
	if place.RootSymID == 0 || symTab.ByID[place.RootSymID] == nil {
		emitInternalViolation(fnName, assignSpan, place, "赋值操作指向无效或缺失符号", report, emitted)
		return
	}
	sym := symTab.ByID[place.RootSymID]
	rootState, exists := env[place.RootSymID]
	if !exists || rootState == nil {
		emitInternalViolation(fnName, assignSpan, place, "赋值操作对应的根存储绑定在 CFG 中未预先声明", report, emitted)
		return
	}

	isCopyAlias := st.CopyPlace.RootSymID != 0 && isTrackableRawCopy(st.Type, st.CopyPlace, tctx, symTab, opaqueTypes)

	// 1. 简单整变量赋值 x = val
	if len(place.Projections) == 0 {
		if !sym.IsMut {
			canUninit, canInit, canMoved, _ := rootState.summaryState()
			// 未初始化的 let 绑定仅当所有到达路径均严格处于“确定未初始化”状态时，允许首次初始化赋值
			if !canUninit || canInit || canMoved {
				if report != nil {
					msg := fmt.Sprintf("不能对不可变绑定 '%s' 重复赋值", place.RootName)
					exp := "不可变 'let' 绑定一旦在某条路径上完成初始化或已发生移动，便禁止再次赋值或重新初始化；若需跨条件重赋值请改用 'var'。"
					if canMoved && !canInit {
						msg = fmt.Sprintf("不能在所有权移动后重新初始化不可变绑定 '%s'", place.RootName)
					} else if canUninit && (canInit || canMoved) {
						msg = fmt.Sprintf("不可变绑定 '%s' 在另一条路径上可能已初始化，拒绝重复赋值", place.RootName)
					}
					d := diagnostic.Diagnostic{
						Code:        "WON-MUT-001",
						Severity:    "error",
						Message:     msg,
						File:        assignSpan.File,
						Span:        assignSpan,
						Explanation: exp,
					}
					if sym.LetTokenSpan.End.Byte > sym.LetTokenSpan.Start.Byte {
						d.Fixes = []diagnostic.Fix{{
							Description: fmt.Sprintf("将绑定 '%s' 改为可变 ('var')", place.RootName),
							Edits: []diagnostic.Edit{{
								Start:       sym.LetTokenSpan.Start.Byte,
								End:         sym.LetTokenSpan.End.Byte,
								Replacement: "var",
							}},
						}}
					}
					emitUniqueDiag(report, emitted, d)
				}
			}
		}
		if isCopyAlias {
			rootState.markNonOwningCopy(collectCopyAliasRoots(env, st.CopyPlace))
		} else {
			rootState.markInit()
		}
		return
	}

	// 2. 通过 *ptr 赋值或 arr[i] 赋值：先确保根绑定已初始化可读，不覆盖整体所有权状态
	if place.HasDeref() || place.HasIndex() {
		checkReadablePlace(fnName, env, place.RootPlace(), assignSpan, symTab, tctx, report, emitted)
		return
	}

	// 3. 结构体嵌套字段部分恢复 s.f1.f2 = val（按需惰性展开任意深度的字段树）
	fields, ok := place.FieldPath()
	if !ok {
		emitInternalViolation(fnName, assignSpan, place, "赋值路径包含不受支持的投影种类", report, emitted)
		return
	}
	target, err := resolveFieldState(rootState, fields, symTab, tctx)
	if err != nil {
		emitInternalViolation(fnName, assignSpan, place, err.Error(), report, emitted)
		return
	}
	if isCopyAlias {
		target.markNonOwningCopy(collectCopyAliasRoots(env, st.CopyPlace))
	} else {
		target.markInit()
	}
}

func collectCopyAliasRoots(env funcEnv, copyPlace cfg.Place) map[symbols.SymbolID]bool {
	roots := make(map[symbols.SymbolID]bool)
	if copyPlace.RootSymID == 0 {
		return roots
	}
	if src := env[copyPlace.RootSymID]; src != nil && len(src.CopyAliasRoots) > 0 {
		for k := range src.CopyAliasRoots {
			roots[k] = true
		}
	}
	roots[copyPlace.RootSymID] = true
	return roots
}

func isTrackableRawCopy(tid types.TypeID, copyPlace cfg.Place, tctx *types.Context, symTab *symbols.Table, opaqueTypes map[string]bindgen.FFIReturnContract) bool {
	if tctx == nil {
		return false
	}
	if tid == types.Invalid {
		tid = copyPlace.ElemType
	}
	if tid == types.Invalid {
		return false
	}
	return typeContainsRawPtrOrOpaque(tid, tctx, symTab, opaqueTypes, make(map[types.TypeID]bool, 4))
}

func typeContainsRawPtrOrOpaque(tid types.TypeID, tctx *types.Context, symTab *symbols.Table, opaqueTypes map[string]bindgen.FFIReturnContract, visited map[types.TypeID]bool) bool {
	if tid == types.Invalid || tctx == nil || visited[tid] {
		return false
	}
	visited[tid] = true
	info := tctx.Info(tid)
	switch info.Kind {
	case types.KindRawPtr:
		return true
	case types.KindStruct:
		if symTab != nil {
			if sym := symTab.ByID[symbols.SymbolID(info.SymbolID)]; sym != nil {
				if len(opaqueTypes) > 0 {
					if ot, ok := opaqueTypes[sym.Name]; ok && (ot.Ownership == bindgen.FFIOpaqueHandle || ot.Ownership == bindgen.FFIOwnedReturn) {
						return true
					}
				}
				for _, f := range sym.Fields {
					if typeContainsRawPtrOrOpaque(f.Type, tctx, symTab, opaqueTypes, visited) {
						return true
					}
				}
			}
		}
		return false
	case types.KindArray:
		return typeContainsRawPtrOrOpaque(info.Elem, tctx, symTab, opaqueTypes, visited)
	case types.KindTuple:
		for _, el := range info.Elems {
			if typeContainsRawPtrOrOpaque(el, tctx, symTab, opaqueTypes, visited) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// resolveFieldState 基于惰性单层展开策略沿着字段路径向下定位精确 PlaceState，不存在任何硬编码深度限制。
func resolveFieldState(root *PlaceState, fields []string, symTab *symbols.Table, tctx *types.Context) (*PlaceState, error) {
	cur := root
	for _, fName := range fields {
		if cur == nil {
			return nil, fmt.Errorf("字段路径 '%s' 遇到空状态节点", fName)
		}
		if cur.Fields == nil {
			if err := cur.expandDirectFields(symTab, tctx); err != nil {
				return nil, err
			}
		}
		next, exists := cur.Fields[fName]
		if !exists || next == nil {
			return nil, fmt.Errorf("结构体状态中不存在字段 '%s'（类型 %s）", fName, tctx.Format(cur.Type))
		}
		cur = next
	}
	return cur, nil
}

func fieldPrefixBeforeDerefOrIndex(place cfg.Place) ([]string, bool) {
	if len(place.Projections) == 0 {
		return nil, false
	}
	var fields []string
	for _, p := range place.Projections {
		if p.Kind != cfg.ProjField {
			break
		}
		fields = append(fields, p.FieldName)
	}
	return fields, len(fields) > 0
}

func formatPlaceDisplay(p cfg.Place) string {
	cur := p.RootName
	if cur == "" && p.RootSymID != 0 {
		cur = fmt.Sprintf("#%d", p.RootSymID)
	}
	for _, proj := range p.Projections {
		switch proj.Kind {
		case cfg.ProjField:
			cur += "." + proj.FieldName
		case cfg.ProjDeref:
			cur = "*" + cur
		case cfg.ProjConstIdx:
			cur += fmt.Sprintf("[%d]", proj.ConstIndex)
		case cfg.ProjIndex:
			cur += "[...]"
		case cfg.ProjTupleField:
			cur += fmt.Sprintf(".%d", proj.FieldIndex)
		case cfg.ProjDowncast:
			cur += fmt.Sprintf("@v%d", proj.VariantIdx)
		}
	}
	return cur
}

func emitUniqueDiag(report *diagnostic.Report, emitted map[diagDedupKey]bool, d diagnostic.Diagnostic) {
	if report == nil {
		return
	}
	if emitted != nil {
		key := diagDedupKey{code: d.Code, file: d.Span.File, byte: d.Span.Start.Byte}
		if emitted[key] {
			return
		}
		emitted[key] = true
	}
	report.AddDiagnostic(d)
}

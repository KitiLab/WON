package borrow

import (
	"fmt"

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

// AnalyzeProgram 对全程序所有权 CFG 运行确定性初始化与移动数据流分析，向 report 写入 WON-MEM-* / WON-MUT-001 诊断。
func AnalyzeProgram(progCFG *cfg.ProgramCFG, view cfg.SemanticView, report *diagnostic.Report) {
	if progCFG == nil || view == nil || report == nil {
		return
	}
	for _, fn := range progCFG.Functions {
		analyzeFunction(fn, view, report)
	}
}

func analyzeFunction(fn *cfg.FuncCFG, view cfg.SemanticView, report *diagnostic.Report) {
	if fn == nil || len(fn.Blocks) == 0 || fn.Entry < 0 {
		return
	}

	reachable := computeReachable(fn)
	inStates := make([]funcEnv, len(fn.Blocks))
	outStates := make([]funcEnv, len(fn.Blocks))

	// 使用确定性固定点工作表迭代直至所有可达基本块入口/出口格状态收敛
	maxIterations := len(fn.Blocks)*(len(fn.Locals)*8+64) + 256
	changed := true
	iters := 0
	for changed {
		iters++
		if iters > maxIterations {
			report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-MEM-001",
				Severity: "error",
				Message:  fmt.Sprintf("函数 '%s' 所有权数据流不动点迭代未收敛，编译器拒绝静默放行", fn.Name),
				File:     fn.Decl.GetSpan().File,
				Span:     fn.Decl.GetSpan(),
			})
			return
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
			transferBlock(blk, cur, view, nil, nil)

			if outStates[bb] == nil || !equalEnvs(outStates[bb], cur) {
				outStates[bb] = cur
				changed = true
			}
		}
	}

	// 收敛后按确定性基本块顺序对可达路径执行唯一一次诊断采集 Pass，避免工作表中间迭代产生重复报错
	emitted := make(map[diagDedupKey]bool)
	for bbIdx, blk := range fn.Blocks {
		bb := cfg.BlockID(bbIdx)
		if !reachable[bb] {
			continue
		}
		cur := cloneEnv(inStates[bb])
		transferBlock(blk, cur, view, report, emitted)
	}
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

func transferBlock(blk *cfg.BasicBlock, env funcEnv, view cfg.SemanticView, report *diagnostic.Report, emitted map[diagDedupKey]bool) {
	symTab := view.GetSymbols()
	tctx := view.GetTypes()

	for _, st := range blk.Statements {
		switch st.Kind {
		case cfg.OpUninitDecl:
			if st.Place.RootSymID != 0 {
				env[st.Place.RootSymID] = newPlaceState(st.Type, st.Span, false, symTab, tctx)
			}

		case cfg.OpInitAssign, cfg.OpStorageDecl:
			if st.Place.RootSymID != 0 {
				env[st.Place.RootSymID] = newPlaceState(st.Type, st.Span, true, symTab, tctx)
			}

		case cfg.OpScopeExit:
			// 注意：局部绑定超出作用域时从当前流映射清除状态以减少状态空间
			for _, sid := range st.Locals {
				delete(env, sid)
			}

		case cfg.OpRead, cfg.OpCopy:
			checkReadablePlace(env, st.Place, st.Span, symTab, report, emitted)

		case cfg.OpMove:
			if st.Place.HasDeref() {
				// 允许内部先校验指针/引用变量本身是否合法初始化，同时禁止通过解引用直接移出非 Copy 目标
				if st.Place.RootSymID != 0 {
					checkReadablePlace(env, st.Place.RootPlace(), st.Span, symTab, report, emitted)
				}
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
				continue
			}

			if st.Place.HasIndex() {
				// 保守禁止直接从数组或切片下标位置移出非 Copy 元素
				if st.Place.RootSymID != 0 {
					checkReadablePlace(env, st.Place.RootPlace(), st.Span, symTab, report, emitted)
				}
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
				continue
			}

			ok := checkReadablePlace(env, st.Place, st.Span, symTab, report, emitted)
			if ok {
				applyMove(env, st.Place, st.Span)
			}

		case cfg.OpAssign, cfg.OpReinit:
			applyAssign(env, st.Place, st.Span, symTab, tctx, report, emitted)
		}
	}
}

func checkReadablePlace(env funcEnv, place cfg.Place, useSpan diagnostic.Span, symTab *symbols.Table, report *diagnostic.Report, emitted map[diagDedupKey]bool) bool {
	if place.RootSymID == 0 {
		return true
	}
	rootState := env[place.RootSymID]
	if rootState == nil {
		return true
	}

	// 若路径只读单个结构体字段，仅检查该字段（或其嵌套子结构）；若整块访问或经下标访问，聚合检查全字段
	targetState, exactPath := lookupTargetState(rootState, place)
	if !exactPath {
		// 对 *ptr 或 arr[i] 读取至少保证根绑定整体已完成初始化且未被移动
		targetState = rootState
	}

	canUninit, _, canMoved, moveSpans := targetState.summaryState()
	if !canUninit && !canMoved {
		return true
	}

	if report != nil {
		disp := formatPlaceDisplay(place)
		if !exactPath {
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

func applyMove(env funcEnv, place cfg.Place, moveSpan diagnostic.Span) {
	if place.RootSymID == 0 {
		return
	}
	rootState := env[place.RootSymID]
	if rootState == nil {
		return
	}
	target, exact := lookupTargetState(rootState, place)
	if !exact || target == nil {
		return
	}
	target.markMoved(moveSpan)
}

func applyAssign(env funcEnv, place cfg.Place, assignSpan diagnostic.Span, symTab *symbols.Table, tctx *types.Context, report *diagnostic.Report, emitted map[diagDedupKey]bool) {
	if place.RootSymID == 0 {
		return
	}
	sym := symTab.ByID[place.RootSymID]
	rootState := env[place.RootSymID]
	if rootState == nil {
		rootState = newPlaceState(place.RootType, assignSpan, false, symTab, tctx)
		env[place.RootSymID] = rootState
	}

	// 1. 简单整变量赋值 x = val
	if len(place.Projections) == 0 {
		if sym != nil && !sym.IsMut {
			canUninit, canInit, canMoved, _ := rootState.summaryState()
			// 未初始化的 let 绑定仅当所有到达路径均严格处于“确定未初始化”状态时，允许首次初始化赋值
			if !canUninit || canInit || canMoved {
				if report != nil {
					msg := fmt.Sprintf("不能对不可变绑定 '%s' 重复赋值", place.RootName)
					exp := fmt.Sprintf("不可变 'let' 绑定一旦在某条路径上完成初始化或已发生移动，便禁止再次赋值或重新初始化；若需跨条件重赋值请改用 'var'。")
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
		rootState.markInit()
		return
	}

	// 2. 通过 *ptr 赋值或 arr[i] 赋值：先确保根绑定已初始化可读，不修改整体所有权状态
	if place.HasDeref() || place.HasIndex() {
		checkReadablePlace(env, place.RootPlace(), assignSpan, symTab, report, emitted)
		return
	}

	// 3. 结构体字段部分恢复 s.field = val
	target, exact := lookupTargetState(rootState, place)
	if exact && target != nil {
		target.markInit()
		// 若顶层原处于局部 Uninit/Moved 状态而所有叶子现均 Init，清零顶层标志
		if _, _, _, _ = rootState.summaryState(); true {
			rootState.CanBeUninit = false
			rootState.CanBeMoved = false
			rootState.CanBeInit = true
		}
	}
}

func lookupTargetState(root *PlaceState, place cfg.Place) (*PlaceState, bool) {
	fields, ok := place.FieldPath()
	if !ok {
		return nil, false
	}
	cur := root
	for _, fName := range fields {
		if cur == nil || len(cur.Fields) == 0 {
			return nil, false
		}
		next, exists := cur.Fields[fName]
		if !exists {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func formatPlaceDisplay(p cfg.Place) string {
	cur := p.RootName
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

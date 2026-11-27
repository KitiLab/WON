package borrow

import (
	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// AnalyzeProgramWithFFI 在携带已校验的 FFI 所有权与借用契约下执行完整 Move/Init、溯源与 NLL 借用冲突分析。
func AnalyzeProgramWithFFI(
	progCFG *cfg.ProgramCFG,
	view cfg.SemanticView,
	contracts map[string]bindgen.FFIFuncContract,
	opaqueTypes map[string]bindgen.FFIReturnContract,
	report *diagnostic.Report,
) {
	if progCFG == nil || view == nil || report == nil {
		return
	}

	// Pass 1: Definite Initialization, Move, OpFFITransfer & Raw-Copy Alias Analysis
	for _, fn := range progCFG.Functions {
		maxIterations := len(fn.Blocks)*(len(fn.Locals)*8+64) + 256
		_, _ = solveMoveInitWithFFI(fn, view, opaqueTypes, report, maxIterations, true)
	}
	if report.HasErrors() {
		return
	}

	// Pass 2 & 3: Stage A 借用载体流 + Stage C 全程序 Tarjan SCC 候选引用溯源推理（含 FFI BorrowsParam）
	provRes := InferAndVerifyProvenanceWithFFI(progCFG, view, contracts, report)
	if provRes == nil || report.HasErrors() {
		return
	}

	// Pass 4: Stage B NLL 向后活跃域、XOR 排他冲突与作用域生命周期校验
	for _, fn := range progCFG.Functions {
		if fn.FuncSym == nil {
			continue
		}
		if flow := provRes.Flows[fn.FuncSym.ID]; flow != nil {
			AnalyzeBorrowConflicts(fn, flow, view, report)
		}
	}
}

// SolveMoveInitTraceWithFFI 在显式 FFI 不透明类型上下文中求解 Move/Init/NonOwningCopy 收敛追踪结果。
func SolveMoveInitTraceWithFFI(fn *cfg.FuncCFG, view cfg.SemanticView, opaqueTypes map[string]bindgen.FFIReturnContract, report *diagnostic.Report) (*MoveInitTrace, bool) {
	if fn == nil || view == nil || len(fn.Blocks) == 0 || fn.Entry < 0 {
		return nil, false
	}
	maxIterations := len(fn.Blocks)*(len(fn.Locals)*8+64) + 256
	return solveMoveInitWithFFI(fn, view, opaqueTypes, report, maxIterations, false)
}

// StepStatementWithFFI 在已知 FFI 不透明资源上下文中推进单条语句的状态转移，供 Drop Elaboration 精确同步状态。
func StepStatementWithFFI(fnName string, env map[symbols.SymbolID]*PlaceState, st cfg.Statement, view cfg.SemanticView, opaqueTypes map[string]bindgen.FFIReturnContract) {
	if view == nil {
		return
	}
	stepBlockStmtWithFFI(fnName, st, funcEnv(env), view.GetSymbols(), view.GetTypes(), opaqueTypes, nil, nil)
}

// IsTrackableRawCopy 判断类型是否包含裸指针或受管 FFI 不透明资源句柄（复制时不复制释放/移交权限）。
func IsTrackableRawCopy(tid types.TypeID, copyPlace cfg.Place, tctx *types.Context, symTab *symbols.Table, opaqueTypes map[string]bindgen.FFIReturnContract) bool {
	return isTrackableRawCopy(tid, copyPlace, tctx, symTab, opaqueTypes)
}

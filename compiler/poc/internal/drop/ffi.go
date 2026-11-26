package drop

import (
	"fmt"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/borrow"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

// RegisterFFIContracts 将经过 Validate() 校验的 FFI 所有权契约目录（OpaqueHandle / OwnedReturn）注册为显式受管外外部资源胶水。
// 未注册且无明确契约的 C 句柄和指针严格维持未受管 / Fail-Closed 边界，绝不猜测析构函数。
func RegisterFFIContracts(reg *ResourceRegistry, symTab *symbols.Table, tctx *types.Context, catalogs ...*bindgen.FFIContractCatalog) error {
	if reg == nil {
		return fmt.Errorf("ResourceRegistry 不能为空")
	}
	for _, cat := range catalogs {
		if cat == nil {
			continue
		}
		if err := cat.Validate(); err != nil {
			return err
		}
		for typeName, ot := range cat.OpaqueTypes {
			if ot.Ownership != bindgen.FFIOpaqueHandle && ot.Ownership != bindgen.FFIOwnedReturn {
				continue
			}
			ext := ResourceExtension{
				Kind:      GlueForeignResource,
				ReleaseFn: ot.ReleaseFn,
			}
			reg.byTypeName[typeName] = ext
			if symTab != nil && symTab.GlobalScope != nil {
				if sym := symTab.GlobalScope.Lookup(typeName); sym != nil && sym.Type != types.Invalid {
					reg.RegisterType(sym.Type, ext)
				}
			}
		}
		for _, fc := range cat.Functions {
			if (fc.Return.Ownership == bindgen.FFIOwnedReturn || fc.Return.Ownership == bindgen.FFIOpaqueHandle) && fc.Return.TypeName != "" {
				ext := ResourceExtension{
					Kind:      GlueForeignResource,
					ReleaseFn: fc.Return.ReleaseFn,
				}
				reg.byTypeName[fc.Return.TypeName] = ext
				if symTab != nil && symTab.GlobalScope != nil {
					if sym := symTab.GlobalScope.Lookup(fc.Return.TypeName); sym != nil && sym.Type != types.Invalid {
						reg.RegisterType(sym.Type, ext)
					}
				}
			}
		}
	}
	_ = tctx
	return nil
}

// ElaborateProgramWithFFI 在 ElaborateProgram 基础上整合显式 FFI 旁路所有权契约（@owned 转移 / @borrow / 外外部资源释放胶水），
// 同时保留原有 ProgramCFG 完全不可变且不猜测任何缺失契约的 C 调用。
func ElaborateProgramWithFFI(progCFG *cfg.ProgramCFG, view cfg.SemanticView, reg *ResourceRegistry, catalogs []*bindgen.FFIContractCatalog, report *diagnostic.Report) *DropElaboratedProgram {
	if progCFG == nil || view == nil || view.GetTypes() == nil || view.GetSymbols() == nil {
		return ElaborateProgram(progCFG, view, reg, report)
	}
	if reg == nil && len(catalogs) > 0 {
		reg = NewResourceRegistry()
	}
	if reg != nil && len(catalogs) > 0 {
		if err := RegisterFFIContracts(reg, view.GetSymbols(), view.GetTypes(), catalogs...); err != nil {
			if report != nil {
				report.AddDiagnostic(diagnostic.Diagnostic{
					Code:     "WON-INT-001",
					Severity: "error",
					Message:  fmt.Sprintf("FFI 所有权契约校验失败: %v", err),
				})
			}
			return &DropElaboratedProgram{DropElaborated: false}
		}
	}

	contractIndex := indexFFIContracts(catalogs)
	// 先在预拷贝 CFG 上应用 FFI 明确契约边界（OpFFITransfer / 参数借用冲突校验），
	// 再运行标准 Phase 2B.3 Drop 展开与不变量验证，使本地/FFI 转移统一在同一条已认证数据流中闭环。
	stagedCFG := CloneProgramCFG(progCFG)
	cc := NewClassifyContext(view.GetTypes(), view.GetSymbols(), reg)
	recordedContracts := make(map[symbols.SymbolID]map[cfg.ProgramPoint]string)

	ffiOK := true
	if len(contractIndex) > 0 {
		for _, fn := range stagedCFG.Functions {
			if fn == nil {
				continue
			}
			trace, ok := borrow.SolveMoveInitTrace(fn, view, report)
			if !ok || trace == nil {
				ffiOK = false
				continue
			}
			fnContracts := make(map[cfg.ProgramPoint]string)
			if !applyFFIContractsToFunc(fn, trace, contractIndex, cc, view, fnContracts, report) {
				ffiOK = false
			}
			if fn.FuncSym != nil && len(fnContracts) > 0 {
				recordedContracts[fn.FuncSym.ID] = fnContracts
			}
		}
	}

	if !ffiOK || (report != nil && report.HasErrors()) {
		return &DropElaboratedProgram{
			ElaboratedCFG:  stagedCFG,
			Functions:      make(map[symbols.SymbolID]*DropElaboratedFunc),
			GlueTable:      make(map[string]*DropGlue),
			DropElaborated: false,
		}
	}

	res := ElaborateProgram(stagedCFG, view, reg, report)
	if res != nil {
		for symID, fMap := range recordedContracts {
			if df := res.Functions[symID]; df != nil {
				for pt, cid := range fMap {
					df.FFIContracts[pt] = cid
				}
			}
		}
	}
	return res
}

func indexFFIContracts(catalogs []*bindgen.FFIContractCatalog) map[string]bindgen.FFIFuncContract {
	out := make(map[string]bindgen.FFIFuncContract)
	for _, cat := range catalogs {
		if cat == nil {
			continue
		}
		for fnName, fc := range cat.Functions {
			ns := fc.Namespace
			if ns == "" {
				ns = cat.Namespace
			}
			if fc.Function == "" {
				fc.Function = fnName
			}
			out[ns+"::"+fc.Function] = fc
			out[fc.Function] = fc
		}
	}
	return out
}

func applyFFIContractsToFunc(
	fn *cfg.FuncCFG,
	trace *borrow.MoveInitTrace,
	contracts map[string]bindgen.FFIFuncContract,
	cc *ClassifyContext,
	view cfg.SemanticView,
	fnContracts map[cfg.ProgramPoint]string,
	report *diagnostic.Report,
) bool {
	ok := true
	for bbIdx, blk := range fn.Blocks {
		bb := cfg.BlockID(bbIdx)
		if int(bbIdx) >= len(trace.Reachable) || !trace.Reachable[bbIdx] || blk == nil {
			continue
		}
		env := trace.CloneInEnv(bb)
		// 追踪本块中已消费为 FFI 参数/临时值的 ValTemp 对应源 Place（用于 OpCall ArgTemps 回溯）
		tempSource := make(map[cfg.ValueID]cfg.Place)
		ffiConsumed := make(map[string]bool)
		outStmts := make([]cfg.Statement, 0, len(blk.Statements)+2)

		for _, st := range blk.Statements {
			switch st.Kind {
			case cfg.OpInitAssign, cfg.OpStorageDecl, cfg.OpAssign, cfg.OpReinit:
				if st.Place.RootSymID != 0 {
					delete(ffiConsumed, canonicalPlaceKey(st.Place))
				}
				outStmts = append(outStmts, st)

			case cfg.OpMove, cfg.OpCopy, cfg.OpBorrowShared, cfg.OpBorrowMut, cfg.OpReborrow:
				if st.ValTemp != cfg.InvalidValue {
					tempSource[st.ValTemp] = st.Place
				}
				outStmts = append(outStmts, st)

			case cfg.OpCall:
				fc, hasContract := contracts[st.CalleeName]
				if !hasContract {
					// 无明确 FFI 所有权契约：Fail-Closed，绝不猜测或隐式转移所有权；原生安全性依赖 unsafe: 边界检查
					outStmts = append(outStmts, st)
					borrow.StepStatement(fn.Name, env, st, view)
					continue
				}

				for _, pc := range fc.Params {
					if pc.ParamIndex < 0 || (pc.ParamIndex >= len(st.ArgTemps) && pc.ParamIndex >= len(st.ArgPlaces)) {
						emitFFIErr(report, &ok, "WON-INT-001", st.Span,
							fmt.Sprintf("FFI 契约 '%s' 参数下标 %d 超出调用实参数量 %d", fc.ContractID, pc.ParamIndex, len(st.ArgTemps)), "")
						continue
					}
					var argPlace cfg.Place
					var hasSrc bool
					if pc.ParamIndex < len(st.ArgTemps) {
						argPlace, hasSrc = tempSource[st.ArgTemps[pc.ParamIndex]]
					}
					if (!hasSrc || argPlace.RootSymID == 0) && pc.ParamIndex < len(st.ArgPlaces) && st.ArgPlaces[pc.ParamIndex].RootSymID != 0 {
						argPlace = st.ArgPlaces[pc.ParamIndex]
						hasSrc = true
					}
					if !hasSrc || argPlace.RootSymID == 0 {
						continue
					}
					pk := canonicalPlaceKey(argPlace)

					switch pc.Ownership {
					case bindgen.FFIOwnedTransfer:
						// 显式所有权移交 C：拦截同一 FFI 句柄的重复移交（OpMove 已在前序检查过非 Copy 双移，此处同时守护 Copy 裸句柄契约）
						if ffiConsumed[pk] {
							emitFFIErr(report, &ok, "WON-MEM-001", st.Span,
								fmt.Sprintf("FFI 契约 '%s' 禁止重复移交已转移给 C 侧的资源 '%s'", fc.ContractID, argPlace.String()),
								"资源所有权此前已通过 @owned 契约移交 C 函数，禁止再次移交或释放。")
							continue
						}
						ffiConsumed[pk] = true
						glueID := ""
						if g, need := cc.ResolveDropGlue(argPlace.ElemType); need && g != nil {
							glueID = g.ID
						}
						outStmts = append(outStmts, cfg.Statement{
							Kind:       cfg.OpFFITransfer,
							Place:      argPlace,
							Type:       argPlace.ElemType,
							CalleeName: st.CalleeName,
							DropGlue:   glueID,
							Span:       st.Span,
						})
					case bindgen.FFIBorrowedShared, bindgen.FFIBorrowedMut:
						if ffiConsumed[pk] {
							emitFFIErr(report, &ok, "WON-MEM-001", st.Span,
								fmt.Sprintf("FFI 借用契约 '%s' 禁止借用已移交 C 侧的资源 '%s'", fc.ContractID, argPlace.String()),
								"资源所有权已被 FFI @owned 消费，不能再借出给外部 C 函数。")
							continue
						}
						// 显式借用契约：调用期间维持调用方所有权，不得移出或提前释放；必须在当前仍合法可读/初始
						if ps := env[argPlace.RootSymID]; ps != nil {
							canUninit, canInit, canMoved, _ := ps.SummaryState()
							if !canInit || canUninit || canMoved {
								emitFFIErr(report, &ok, "WON-MEM-001", st.Span,
									fmt.Sprintf("FFI 借用契约 '%s' 要求参数 '%s' 在调用点完整有效", fc.ContractID, argPlace.String()),
									"不能将可能未初始化或已移出的值借用给外部 C 函数。")
								continue
							}
						}
					case bindgen.FFIUnknown:
						emitFFIErr(report, &ok, "WON-SEC-001", st.Span,
							fmt.Sprintf("FFI 函数 '%s' 参数 #%d 缺乏所有权契约，按 Fail-Closed 策略禁止按受管所有权传入", st.CalleeName, pc.ParamIndex), "")
					}
				}

				callIdx := len(outStmts)
				outStmts = append(outStmts, st)
				if fc.ContractID != "" {
					fnContracts[cfg.ProgramPoint{Block: blk.ID, StmtIdx: callIdx}] = fc.ContractID
				}

			default:
				outStmts = append(outStmts, st)
			}

			borrow.StepStatement(fn.Name, env, st, view)
		}
		blk.Statements = outStmts
	}
	return ok
}

func emitFFIErr(report *diagnostic.Report, ok *bool, code string, span diagnostic.Span, msg, exp string) {
	if ok != nil {
		*ok = false
	}
	if report != nil {
		report.AddDiagnostic(diagnostic.Diagnostic{
			Code:        code,
			Severity:    "error",
			Message:     msg,
			File:        span.File,
			Span:        span,
			Explanation: exp,
		})
	}
}

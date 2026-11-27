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

// RegisterFFIContracts 将经过严格校验的 FFI 所有权契约目录（OpaqueHandle / OwnedReturn）注册为显式受管外部资源胶水。
// 未注册且无明确契约的 C 句柄和指针严格维持未受管 / Fail-Closed 边界，绝不猜测析构函数。
func RegisterFFIContracts(reg *ResourceRegistry, symTab *symbols.Table, tctx *types.Context, catalogs ...*bindgen.FFIContractCatalog) error {
	if reg == nil {
		return fmt.Errorf("ResourceRegistry 不能为空")
	}
	contractIndex, opaqueTypes, err := bindgen.IndexValidatedFFIContracts(catalogs)
	if err != nil {
		return err
	}
	for typeName, ot := range opaqueTypes {
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
	for _, fc := range contractIndex {
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
	_ = tctx
	return nil
}

// ElaborateProgramWithFFI 在 ElaborateProgram 基础上整合显式 FFI 旁路所有权契约（@owned 转移 / @borrow / @borrow(mut) / BorrowsParam / 外部资源释放胶水），
// 并在统一的全 CFG 借用与所有权数据流中完成验证，同时保持原有 ProgramCFG 完全不可变。
func ElaborateProgramWithFFI(progCFG *cfg.ProgramCFG, view cfg.SemanticView, reg *ResourceRegistry, catalogs []*bindgen.FFIContractCatalog, report *diagnostic.Report) *DropElaboratedProgram {
	// 1. Fail-Closed 优先：无论 CFG 或视图状态如何，必须在进入展开前完成全部 FFI 契约验证与矛盾检测
	contractIndex, opaqueTypes, err := bindgen.IndexValidatedFFIContracts(catalogs)
	if err != nil {
		if report != nil {
			report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-INT-001",
				Severity: "error",
				Message:  fmt.Sprintf("FFI 所有权契约校验失败: %v", err),
			})
		}
		return &DropElaboratedProgram{DropElaborated: false}
	}

	if len(contractIndex) == 0 && len(opaqueTypes) == 0 {
		return ElaborateProgram(progCFG, view, reg, report)
	}

	if progCFG == nil || view == nil || view.GetTypes() == nil || view.GetSymbols() == nil {
		if report != nil {
			report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-INT-001",
				Severity: "error",
				Message:  "FFI Drop Elaboration 缺乏有效的 CFG 或语义视图上下文，按 Fail-Closed 策略拒绝认证",
			})
		}
		return &DropElaboratedProgram{DropElaborated: false}
	}

	if reg == nil {
		reg = NewResourceRegistry()
	}
	if err := RegisterFFIContracts(reg, view.GetSymbols(), view.GetTypes(), catalogs...); err != nil {
		if report != nil {
			report.AddDiagnostic(diagnostic.Diagnostic{
				Code:     "WON-INT-001",
				Severity: "error",
				Message:  fmt.Sprintf("FFI 所有权契约注册失败: %v", err),
			})
		}
		return &DropElaboratedProgram{DropElaborated: false}
	}

	// 2. 在深拷贝的 stagedCFG 上执行结构化 FFI 契约降级（OpFFITransfer / OpBorrowShared / OpBorrowMut / 实参别名校验）
	stagedCFG := CloneProgramCFG(progCFG)
	cc := NewClassifyContext(view.GetTypes(), view.GetSymbols(), reg)
	recordedContracts := make(map[symbols.SymbolID]map[cfg.ProgramPoint]string)

	ffiOK := true
	for _, fn := range stagedCFG.Functions {
		if fn == nil {
			continue
		}
		fnContracts := make(map[cfg.ProgramPoint]string)
		if !lowerFFIContractsInFunc(fn, contractIndex, cc, view, fnContracts, report) {
			ffiOK = false
		}
		if fn.FuncSym != nil && len(fnContracts) > 0 {
			recordedContracts[fn.FuncSym.ID] = fnContracts
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

	// 3. 在同一份 FFI 降级后 CFG 上运行权威 Move/Init + BorrowsParam 溯源 + NLL 借用冲突检验
	borrow.AnalyzeProgramWithFFI(stagedCFG, view, contractIndex, opaqueTypes, report)
	if report != nil && report.HasErrors() {
		return &DropElaboratedProgram{
			ElaboratedCFG:  stagedCFG,
			Functions:      make(map[symbols.SymbolID]*DropElaboratedFunc),
			GlueTable:      make(map[string]*DropGlue),
			DropElaborated: false,
		}
	}

	// 4. 基于同一权威数据流在 stagedCFG 上执行 Drop 展开与独立不变量验证
	res := elaborateStagedProgramWithFFI(stagedCFG, view, reg, opaqueTypes, report)
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

func computeReachableCFG(fn *cfg.FuncCFG) []bool {
	reach := make([]bool, len(fn.Blocks))
	if fn == nil || fn.Entry < 0 || int(fn.Entry) >= len(fn.Blocks) {
		return reach
	}
	q := []cfg.BlockID{fn.Entry}
	reach[fn.Entry] = true
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		if fn.Blocks[cur] == nil {
			continue
		}
		for _, succ := range fn.Blocks[cur].Successors {
			if succ >= 0 && int(succ) < len(fn.Blocks) && !reach[succ] {
				reach[succ] = true
				q = append(q, succ)
			}
		}
	}
	return reach
}

func initTempAndLoanCounters(fn *cfg.FuncCFG) (cfg.ValueID, cfg.LoanID) {
	maxTemp := fn.NextTemp
	maxLoan := fn.NextLoan
	for _, blk := range fn.Blocks {
		if blk == nil {
			continue
		}
		for _, st := range blk.Statements {
			if st.ValTemp >= maxTemp {
				maxTemp = st.ValTemp + 1
			}
			if st.SrcTemp >= maxTemp {
				maxTemp = st.SrcTemp + 1
			}
			for _, at := range st.ArgTemps {
				if at >= maxTemp {
					maxTemp = at + 1
				}
			}
			if st.LoanID >= maxLoan {
				maxLoan = st.LoanID + 1
			}
		}
	}
	if maxTemp == 0 {
		maxTemp = 1
	}
	if maxLoan == 0 {
		maxLoan = 1
	}
	return maxTemp, maxLoan
}

func lowerFFIContractsInFunc(
	fn *cfg.FuncCFG,
	contracts map[string]bindgen.FFIFuncContract,
	cc *ClassifyContext,
	view cfg.SemanticView,
	fnContracts map[cfg.ProgramPoint]string,
	report *diagnostic.Report,
) bool {
	if fn == nil || len(fn.Blocks) == 0 {
		return true
	}
	tctx := view.GetTypes()
	symTab := view.GetSymbols()
	reach := computeReachableCFG(fn)
	nextTemp, nextLoan := initTempAndLoanCounters(fn)
	ok := true

	for bbIdx, blk := range fn.Blocks {
		if bbIdx >= len(reach) || !reach[bbIdx] || blk == nil {
			continue
		}
		tempSource := make(map[cfg.ValueID]cfg.Place)
		tempStmtIdx := make(map[cfg.ValueID]int)
		tempIsBorrow := make(map[cfg.ValueID]bool)
		tempBorrowKind := make(map[cfg.ValueID]cfg.BorrowKind)
		consumedArgStmts := make(map[int]bool)
		removeStmt := make(map[int]bool)
		outStmts := make([]cfg.Statement, 0, len(blk.Statements)+4)

		for _, st := range blk.Statements {
			switch st.Kind {
			case cfg.OpMove, cfg.OpCopy:
				idx := len(outStmts)
				outStmts = append(outStmts, st)
				if st.ValTemp != cfg.InvalidValue {
					tempSource[st.ValTemp] = st.Place
					tempStmtIdx[st.ValTemp] = idx
				}

			case cfg.OpBorrowShared, cfg.OpBorrowMut, cfg.OpReborrow:
				outStmts = append(outStmts, st)
				if st.ValTemp != cfg.InvalidValue {
					tempSource[st.ValTemp] = st.Place
					tempIsBorrow[st.ValTemp] = true
					tempBorrowKind[st.ValTemp] = st.BorrowKind
				}

			case cfg.OpCall:
				if st.Callee != nil {
					outStmts = append(outStmts, st)
					continue
				}
				fc, hasContract := contracts[st.CalleeName]
				if !hasContract {
					outStmts = append(outStmts, st)
					continue
				}

				if len(st.ArgTemps) != len(st.ArgPlaces) {
					emitFFIErr(report, &ok, "WON-INT-001", st.Span,
						fmt.Sprintf("FFI 调用 '%s' 实参元数据不一致 (ArgTemps=%d vs ArgPlaces=%d)", st.CalleeName, len(st.ArgTemps), len(st.ArgPlaces)), "")
					outStmts = append(outStmts, st)
					continue
				}
				if len(fc.Params) != len(st.ArgPlaces) {
					emitFFIErr(report, &ok, "WON-INT-001", st.Span,
						fmt.Sprintf("FFI 契约 '%s' 参数数量 (%d) 与调用 '%s' 实参数量 (%d) 不匹配", fc.ContractID, len(fc.Params), st.CalleeName, len(st.ArgPlaces)), "")
					outStmts = append(outStmts, st)
					continue
				}

				paramByIdx := make(map[int]bindgen.FFIParamContract, len(fc.Params))
				paramIndexErr := false
				for _, pc := range fc.Params {
					if pc.ParamIndex < 0 || pc.ParamIndex >= len(st.ArgPlaces) {
						emitFFIErr(report, &ok, "WON-INT-001", st.Span,
							fmt.Sprintf("FFI 契约 '%s' 参数下标 %d 超出调用实参数量 %d", fc.ContractID, pc.ParamIndex, len(st.ArgPlaces)), "")
						paramIndexErr = true
						continue
					}
					paramByIdx[pc.ParamIndex] = pc
				}
				if paramIndexErr || len(paramByIdx) != len(st.ArgPlaces) {
					if !paramIndexErr {
						emitFFIErr(report, &ok, "WON-INT-001", st.Span,
							fmt.Sprintf("FFI 契约 '%s' 参数契约不完整，无法覆盖全部 %d 个调用实参", fc.ContractID, len(st.ArgPlaces)), "")
					}
					outStmts = append(outStmts, st)
					continue
				}

				argCount := len(st.ArgPlaces)
				resolvedPlaces := make([]cfg.Place, argCount)
				isArgBorrow := make([]bool, argCount)
				argBorrowKind := make([]cfg.BorrowKind, argCount)
				for i := 0; i < argCount; i++ {
					if st.ArgPlaces[i].RootSymID != 0 {
						resolvedPlaces[i] = st.ArgPlaces[i]
					} else if st.ArgTemps[i] != cfg.InvalidValue {
						resolvedPlaces[i] = tempSource[st.ArgTemps[i]]
						isArgBorrow[i] = tempIsBorrow[st.ArgTemps[i]]
						argBorrowKind[i] = tempBorrowKind[st.ArgTemps[i]]
					}
				}

				// 校验同一次调用内多个实参之间的所有权/借用别名冲突
				for i := 0; i < argCount; i++ {
					pi := paramByIdx[i].Ownership
					for j := i + 1; j < argCount; j++ {
						if resolvedPlaces[i].RootSymID == 0 || resolvedPlaces[j].RootSymID == 0 || !resolvedPlaces[i].Overlaps(resolvedPlaces[j]) {
							continue
						}
						pj := paramByIdx[j].Ownership
						switch {
						case pi == bindgen.FFIOwnedTransfer && pj == bindgen.FFIOwnedTransfer:
							emitFFIErr(report, &ok, "WON-MEM-001", st.Span,
								fmt.Sprintf("FFI 契约 '%s' 禁止同一调用中将别名同一资源 '%s' 的多个实参重复按 @owned 移交", fc.ContractID, resolvedPlaces[i].String()),
								"同一资源所有权在一次 FFI 调用中只能移交一次。")
						case pi == bindgen.FFIOwnedTransfer && (pj == bindgen.FFIBorrowedShared || pj == bindgen.FFIBorrowedMut),
							pj == bindgen.FFIOwnedTransfer && (pi == bindgen.FFIBorrowedShared || pi == bindgen.FFIBorrowedMut):
							emitFFIErr(report, &ok, "WON-MEM-006", st.Span,
								fmt.Sprintf("FFI 契约 '%s' 禁止同一调用中对 '%s' 同时执行 @owned 移交与借用", fc.ContractID, resolvedPlaces[i].String()),
								"资源正在被借用期间不得同时将其所有权移交至外部 C 函数。")
						case pi == bindgen.FFIBorrowedMut && pj == bindgen.FFIBorrowedMut:
							emitFFIErr(report, &ok, "WON-MEM-005", st.Span,
								fmt.Sprintf("FFI 契约 '%s' 禁止同一调用中对 '%s' 创建多个活跃 @borrow(mut) 可变借用", fc.ContractID, resolvedPlaces[i].String()),
								"独占可变借用在同一调用中不能与其他可变借用别名同一存储位置。")
						case (pi == bindgen.FFIBorrowedMut && pj == bindgen.FFIBorrowedShared) || (pj == bindgen.FFIBorrowedMut && pi == bindgen.FFIBorrowedShared):
							emitFFIErr(report, &ok, "WON-MEM-004", st.Span,
								fmt.Sprintf("FFI 契约 '%s' 禁止同一调用中对 '%s' 同时传递 @borrow(mut) 与 @borrow", fc.ContractID, resolvedPlaces[i].String()),
								"共享借用与独占可变借用在同一次 FFI 调用期间互斥。")
						}
					}
				}

				// 从右到左将调用实参的 OpMove/OpCopy 降级语句匹配到对应实参槽位
				matchedStmt := make([]int, argCount)
				for i := range matchedStmt {
					matchedStmt[i] = -1
				}
				for i := argCount - 1; i >= 0; i-- {
					if isArgBorrow[i] {
						continue
					}
					if st.ArgTemps[i] != cfg.InvalidValue {
						if idx, found := tempStmtIdx[st.ArgTemps[i]]; found && !consumedArgStmts[idx] {
							matchedStmt[i] = idx
							consumedArgStmts[idx] = true
							continue
						}
					}
					if resolvedPlaces[i].RootSymID == 0 {
						continue
					}
					targetKey := resolvedPlaces[i].Key()
					for k := len(outStmts) - 1; k >= 0; k-- {
						if consumedArgStmts[k] {
							continue
						}
						prev := outStmts[k]
						switch prev.Kind {
						case cfg.OpInitAssign, cfg.OpAssign, cfg.OpReinit, cfg.OpUninitDecl, cfg.OpScopeExit, cfg.OpStorageDead, cfg.OpReturn:
							// 不跨过其他语句的绑定写入或作用域边界，避免误匹配先前声明语句的 Move/Copy
							k = -1
							continue
						}
						if (prev.Kind == cfg.OpMove || prev.Kind == cfg.OpCopy) && prev.Place.Key() == targetKey {
							matchedStmt[i] = k
							consumedArgStmts[k] = true
							break
						}
					}
				}

				newArgTemps := append([]cfg.ValueID(nil), st.ArgTemps...)
				for i := 0; i < argCount; i++ {
					pc := paramByIdx[i]
					argPlace := resolvedPlaces[i]

					switch pc.Ownership {
					case bindgen.FFIOwnedTransfer:
						if isArgBorrow[i] {
							emitFFIErr(report, &ok, "WON-MEM-008", st.Span,
								fmt.Sprintf("FFI 契约 '%s' 禁止通过借用引用向 @owned 参数 #%d 移交所有权", fc.ContractID, pc.ParamIndex),
								"请直接传入拥有所有权的绑定或句柄，而不要传入 & / &mut 临时借用。")
							continue
						}
						if argPlace.RootSymID == 0 {
							continue
						}
						glueID := ""
						if g, need := cc.ResolveDropGlue(argPlace.ElemType); need && g != nil {
							glueID = g.ID
						}
						if matchedStmt[i] >= 0 {
							mIdx := matchedStmt[i]
							outStmts[mIdx].Kind = cfg.OpFFITransfer
							outStmts[mIdx].CalleeName = st.CalleeName
							outStmts[mIdx].DropGlue = glueID
							outStmts[mIdx].Span = st.Span
						} else {
							outStmts = append(outStmts, cfg.Statement{
								Kind:       cfg.OpFFITransfer,
								Place:      argPlace,
								Type:       argPlace.ElemType,
								CalleeName: st.CalleeName,
								DropGlue:   glueID,
								Span:       st.Span,
							})
						}

					case bindgen.FFIBorrowedShared, bindgen.FFIBorrowedMut:
						wantMut := pc.Ownership == bindgen.FFIBorrowedMut
						if isArgBorrow[i] {
							if wantMut && (argBorrowKind[i] == cfg.BorrowShared || argBorrowKind[i] == cfg.BorrowReborrowShared) {
								emitFFIErr(report, &ok, "WON-MEM-004", st.Span,
									fmt.Sprintf("FFI 契约 '%s' @borrow(mut) 参数 #%d 不接受只读共享借用", fc.ContractID, pc.ParamIndex),
									"可变 FFI 借用要求传入具备独占 &mut 可变权限的借用或可变绑定。")
							}
							continue
						}
						if argPlace.RootSymID == 0 {
							continue
						}
						if wantMut {
							elemT := argPlace.ElemType
							if elemT != types.Invalid && tctx != nil {
								tInfo := tctx.Info(elemT)
								if tInfo.Kind == types.KindRef || tInfo.Kind == types.KindRawPtr {
									if !tInfo.IsMut {
										emitFFIErr(report, &ok, "WON-MEM-004", st.Span,
											fmt.Sprintf("FFI 契约 '%s' @borrow(mut) 参数 #%d 拒绝只读引用或 *const 指针 '%s'", fc.ContractID, pc.ParamIndex, argPlace.String()),
											"只有 &mut / *mut 或可变绑定可以授出可变 FFI 借用权限。")
										continue
									}
								} else {
									isMut := argPlace.RootMut
									if !isMut && symTab != nil {
										if sym := symTab.ByID[argPlace.RootSymID]; sym != nil {
											isMut = sym.IsMut
										}
									}
									if !isMut {
										emitFFIErr(report, &ok, "WON-MUT-001", st.Span,
											fmt.Sprintf("FFI 契约 '%s' @borrow(mut) 参数 #%d 不能借用不可变绑定 '%s'", fc.ContractID, pc.ParamIndex, argPlace.String()),
											"请将绑定声明为 'var' 以允许外部 C 函数在借用期内修改其内容。")
										continue
									}
								}
							}
						}

						var transferredPreComputes []cfg.ComputeOp
						if matchedStmt[i] >= 0 {
							removeStmt[matchedStmt[i]] = true
							if prevB := outStmts[matchedStmt[i]].Bridge; prevB != nil && len(prevB.PreComputes) > 0 {
								transferredPreComputes = append([]cfg.ComputeOp(nil), prevB.PreComputes...)
							}
						}
						opKind := cfg.OpBorrowShared
						bk := cfg.BorrowShared
						if wantMut {
							opKind = cfg.OpBorrowMut
							bk = cfg.BorrowMut
						}
						borrowType := argPlace.ElemType
						if borrowType != types.Invalid && tctx != nil {
							borrowType = tctx.Ref(wantMut, borrowType)
						}
						vTemp := nextTemp
						nextTemp++
						lID := nextLoan
						nextLoan++
						var readBridge *cfg.StatementBridge
						if len(transferredPreComputes) > 0 {
							readBridge = &cfg.StatementBridge{PreComputes: transferredPreComputes}
						}
						outStmts = append(outStmts,
							cfg.Statement{
								Kind:   cfg.OpRead,
								Place:  argPlace,
								Type:   argPlace.ElemType,
								Span:   st.Span,
								Bridge: readBridge,
							},
							cfg.Statement{
								Kind:       opKind,
								Place:      argPlace,
								Type:       borrowType,
								LoanID:     lID,
								BorrowKind: bk,
								ValTemp:    vTemp,
								Span:       st.Span,
							},
						)
						newArgTemps[i] = vTemp
						if st.Bridge != nil && i < len(st.Bridge.ArgVals) {
							st.Bridge.ArgVals[i] = cfg.InvalidBridgeVal
						}

					case bindgen.FFIUnknown:
						emitFFIErr(report, &ok, "WON-SEC-001", st.Span,
							fmt.Sprintf("FFI 函数 '%s' 参数 #%d 缺乏所有权契约，按 Fail-Closed 策略禁止按受管所有权传入", st.CalleeName, pc.ParamIndex), "")
					}
				}

				st.ArgTemps = newArgTemps
				outStmts = append(outStmts, st)

			default:
				outStmts = append(outStmts, st)
			}
		}

		// 移除被借用替换掉的旧 OpMove/OpCopy 占位语句，并计算准确的 OpCall 索引记录 FFIContracts
		filtered := make([]cfg.Statement, 0, len(outStmts))
		for idx, st := range outStmts {
			if removeStmt[idx] {
				continue
			}
			newIdx := len(filtered)
			filtered = append(filtered, st)
			if st.Kind == cfg.OpCall && st.Callee == nil {
				if fc, hasContract := contracts[st.CalleeName]; hasContract && fc.ContractID != "" {
					fnContracts[cfg.ProgramPoint{Block: blk.ID, StmtIdx: newIdx}] = fc.ContractID
				}
			}
		}
		blk.Statements = filtered
	}

	fn.NextTemp = nextTemp
	fn.NextLoan = nextLoan
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

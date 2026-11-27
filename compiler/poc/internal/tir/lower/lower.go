// Package lower 实现了从经过 Phase 2A/2B 验证的 TypedProgram 与 DropElaboratedProgram 到后端无关 TIR 的确定性降级通道。
// 该包按 Fail-Closed 原则拒绝未通过语义检查、所有权检验、FFI 契约验证或 Drop 展开的程序输入，并调用 tir.Verify 进行独立结构认证。
package lower

import (
	"fmt"
	"sort"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/bindgen"
	"github.com/KitiLab/WON/compiler/poc/internal/cfg"
	"github.com/KitiLab/WON/compiler/poc/internal/checker"
	"github.com/KitiLab/WON/compiler/poc/internal/diagnostic"
	"github.com/KitiLab/WON/compiler/poc/internal/drop"
	"github.com/KitiLab/WON/compiler/poc/internal/symbols"
	"github.com/KitiLab/WON/compiler/poc/internal/tir"
	"github.com/KitiLab/WON/compiler/poc/internal/types"
)

type ffiFuncEntry struct {
	id       tir.ContractID
	contract bindgen.FFIFuncContract
}

type lowerer struct {
	tp       *checker.TypedProgram
	bindings map[string]*bindgen.BindingMeta
	prog     *tir.Program

	tconv        *typeConverter
	glueIDMap    map[string]tir.DropGlueID
	fnIDMap      map[symbols.SymbolID]tir.FunctionID
	ffiByKey     map[string]ffiFuncEntry
	ffiByID      map[string]tir.ContractID
	opaqueOwners map[string]string // TypeName -> ForeignNamespace
}

// LowerProgram 将已认证的前端 TypedProgram 与 DropElaboratedProgram 确定性降级为经过 tir.Verify 验证的 *tir.Program。
func LowerProgram(tp *checker.TypedProgram, bindings map[string]*bindgen.BindingMeta) (*tir.Program, error) {
	if err := validateInputs(tp, bindings); err != nil {
		return nil, fmt.Errorf("WON-INT-001: %w", err)
	}

	l := &lowerer{
		tp:           tp,
		bindings:     bindings,
		glueIDMap:    make(map[string]tir.DropGlueID),
		fnIDMap:      make(map[symbols.SymbolID]tir.FunctionID),
		ffiByKey:     make(map[string]ffiFuncEntry),
		ffiByID:      make(map[string]tir.ContractID),
		opaqueOwners: make(map[string]string),
	}

	tt := tir.NewTypeTable()
	l.prog = &tir.Program{
		Types:       tt,
		OwnershipOK: tp.OwnershipChecked,
		DropOK:      tp.DropElaborated,
	}

	// 1. 解析并降级 FFI 目录及契约表（按命名空间与契约键确定性排序）
	if err := l.lowerFFIMetadata(); err != nil {
		return nil, fmt.Errorf("WON-INT-001: %w", err)
	}

	// 2. 初始化规范类型转换器并预注册所有程序函数签名 ID
	l.tconv = newTypeConverter(tp.Types, tp.Symbols, tt, l.opaqueOwners)
	l.registerAllFunctions()

	// 3. 确定性降级 DropGlue 目录
	l.lowerDropGlues()

	// 4. 逐函数确定性降级基本块、指令、所有权、DropFlag 与 FFI 事件
	elabFuncs := tp.DropPlan.ElaboratedCFG.Functions
	l.prog.Functions = make([]*tir.Function, 0, len(elabFuncs))
	fileSeen := make(map[string]bool)
	for _, fn := range elabFuncs {
		if fn == nil {
			continue
		}
		var df *drop.DropElaboratedFunc
		if fn.FuncSym != nil {
			df = tp.DropPlan.Functions[fn.FuncSym.ID]
		}
		file := ""
		if fn.Decl != nil && fn.Decl.Span.File != "" {
			file = fn.Decl.Span.File
		} else if fn.FuncSym != nil && fn.FuncSym.Span.File != "" {
			file = fn.FuncSym.Span.File
		}
		if file != "" && !fileSeen[file] {
			fileSeen[file] = true
			l.prog.SourceFiles = append(l.prog.SourceFiles, file)
		}
		tf, err := l.lowerFunction(fn, df)
		if err != nil {
			return nil, fmt.Errorf("WON-INT-001: 函数 '%s' TIR 降级失败: %w", fn.Name, err)
		}
		l.prog.Functions = append(l.prog.Functions, tf)
	}
	sort.Strings(l.prog.SourceFiles)

	// 5. 调用独立 TIR 结构与类型不变量验证器实行 Fail-Closed 自检
	if errs := tir.Verify(l.prog); len(errs) > 0 {
		return nil, fmt.Errorf("WON-INT-001: 生成的 TIR 未通过独立验证:\n%w", tir.VerifyErrorSummary(errs))
	}
	return l.prog, nil
}

func validateInputs(tp *checker.TypedProgram, bindings map[string]*bindgen.BindingMeta) error {
	if tp == nil {
		return fmt.Errorf("TypedProgram 为空")
	}
	if !tp.OwnershipChecked || !tp.DropElaborated {
		return fmt.Errorf("前端所有权或 Drop 展开尚未通过验证 (OwnershipChecked=%v, DropElaborated=%v)", tp.OwnershipChecked, tp.DropElaborated)
	}
	if tp.Types == nil || tp.Symbols == nil {
		return fmt.Errorf("TypedProgram 缺少规范 Types 或 Symbols 上下文")
	}
	if tp.CFG == nil || tp.DropPlan == nil || !tp.DropPlan.DropElaborated || tp.DropPlan.ElaboratedCFG == nil {
		return fmt.Errorf("TypedProgram 缺少有效的 CFG 或已验证 DropElaboratedProgram")
	}
	if len(tp.CFG.Functions) != len(tp.DropPlan.ElaboratedCFG.Functions) {
		return fmt.Errorf("原始 CFG 函数个数 (%d) 与 Drop 展开 CFG 函数个数 (%d) 不一致", len(tp.CFG.Functions), len(tp.DropPlan.ElaboratedCFG.Functions))
	}
	for i, baseFn := range tp.CFG.Functions {
		elabFn := tp.DropPlan.ElaboratedCFG.Functions[i]
		if baseFn == nil || elabFn == nil {
			continue
		}
		if baseFn.Name != elabFn.Name {
			return fmt.Errorf("函数 #%d 名称不一致 (%q vs %q)", i, baseFn.Name, elabFn.Name)
		}
		if baseFn.FuncSym != nil && elabFn.FuncSym != nil && baseFn.FuncSym.ID != elabFn.FuncSym.ID {
			return fmt.Errorf("函数 '%s' 符号 ID 不一致 (%d vs %d)", baseFn.Name, baseFn.FuncSym.ID, elabFn.FuncSym.ID)
		}
	}
	if err := bindgen.ValidateBindingContracts(bindings); err != nil {
		return fmt.Errorf("FFI 契约校验失败: %w", err)
	}
	return nil
}

func (l *lowerer) lowerFFIMetadata() error {
	if l.tp != nil && l.tp.AST != nil && len(l.tp.AST.Imports) > 0 {
		seenHdr := make(map[string]bool, len(l.tp.AST.Imports))
		for _, imp := range l.tp.AST.Imports {
			if imp == nil || imp.HeaderPath == "" || seenHdr[imp.HeaderPath] {
				continue
			}
			seenHdr[imp.HeaderPath] = true
			l.prog.ForeignHeaders = append(l.prog.ForeignHeaders, tir.ForeignHeaderDesc{
				HeaderPath: imp.HeaderPath,
			})
		}
	}
	if len(l.bindings) == 0 {
		return nil
	}
	nsList := make([]string, 0, len(l.bindings))
	for ns := range l.bindings {
		nsList = append(nsList, ns)
	}
	sort.Strings(nsList)

	var catalogs []*bindgen.FFIContractCatalog
	headerByNS := make(map[string]string)
	schemaByNS := make(map[string]string)

	for _, ns := range nsList {
		bm := l.bindings[ns]
		if bm == nil || bm.Contracts == nil {
			continue
		}
		cat := bm.Contracts
		canonHeader, err := bindgen.CanonicalizeFFIHeaderPath(cat.HeaderPath)
		if err != nil {
			return err
		}
		l.prog.FFICatalogs = append(l.prog.FFICatalogs, tir.FFICatalogDesc{
			Namespace:     cat.Namespace,
			SchemaVersion: cat.SchemaVersion,
			HeaderPath:    canonHeader,
		})
		headerByNS[cat.Namespace] = canonHeader
		schemaByNS[cat.Namespace] = cat.SchemaVersion
		catalogs = append(catalogs, cat)

		for typeName, ot := range cat.OpaqueTypes {
			if ot.Ownership == bindgen.FFIOpaqueHandle || ot.Ownership == bindgen.FFIOwnedReturn {
				l.opaqueOwners[typeName] = cat.Namespace
			}
		}
	}
	if len(catalogs) == 0 {
		return nil
	}

	fnContracts, opaqueTypes, err := bindgen.IndexValidatedFFIContracts(catalogs)
	if err != nil {
		return err
	}
	for typeName := range opaqueTypes {
		if _, exists := l.opaqueOwners[typeName]; !exists {
			l.opaqueOwners[typeName] = ""
		}
	}

	keys := make([]string, 0, len(fnContracts))
	for k := range fnContracts {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var nextCID tir.ContractID = 1
	for _, k := range keys {
		fc := fnContracts[k]
		if (fc.Return.Ownership == bindgen.FFIOwnedReturn || fc.Return.Ownership == bindgen.FFIOpaqueHandle) && fc.Return.TypeName != "" {
			l.opaqueOwners[fc.Return.TypeName] = fc.Namespace
		}
		var sig *bindgen.FunctionSig
		if bm := l.bindings[fc.Namespace]; bm != nil {
			if s, ok := bm.Functions[fc.Function]; ok {
				sig = &s
			}
		}
		params := make([]tir.FFIParamDesc, len(fc.Params))
		for i, pc := range fc.Params {
			cType := ""
			if sig != nil && pc.ParamIndex >= 0 && pc.ParamIndex < len(sig.ParamTypes) {
				cType = sig.ParamTypes[pc.ParamIndex]
			}
			params[i] = tir.FFIParamDesc{
				ParamIndex: pc.ParamIndex,
				ParamName:  pc.ParamName,
				CType:      cType,
				Ownership:  convertFFIOwnership(pc.Ownership),
			}
		}
		retCType := ""
		if sig != nil {
			retCType = sig.ReturnType
		}
		cid := nextCID
		nextCID++
		desc := tir.FFIContractDesc{
			ID:                  cid,
			ContractKey:         k,
			Namespace:           fc.Namespace,
			Function:            fc.Function,
			HeaderPath:          headerByNS[fc.Namespace],
			SchemaVersion:       schemaByNS[fc.Namespace],
			CallingConv:         "C",
			Params:              params,
			ReturnOwnership:     convertFFIOwnership(fc.Return.Ownership),
			ReturnCType:         retCType,
			ReturnTypeName:      fc.Return.TypeName,
			ReturnReleaseSymbol: fc.Return.ReleaseFn,
			BorrowsParam:        fc.Return.BorrowsParam,
		}
		if desc.ContractKey == "" {
			desc.ContractKey = fc.Namespace + "::" + fc.Function
		}
		if fc.ContractID != "" {
			desc.ContractKey = fc.ContractID
		}
		l.prog.FFIContracts = append(l.prog.FFIContracts, desc)
		l.ffiByKey[k] = ffiFuncEntry{id: cid, contract: fc}
		if fc.ContractID != "" {
			l.ffiByID[fc.ContractID] = cid
		}
	}
	return nil
}

func (l *lowerer) registerAllFunctions() {
	var nextFID tir.FunctionID = 1
	for _, fn := range l.tp.DropPlan.ElaboratedCFG.Functions {
		if fn == nil || fn.FuncSym == nil {
			continue
		}
		if _, exists := l.fnIDMap[fn.FuncSym.ID]; !exists {
			l.fnIDMap[fn.FuncSym.ID] = nextFID
			nextFID++
		}
	}
}

func (l *lowerer) lowerDropGlues() {
	ids := drop.SortedGlueIDs(l.tp.DropPlan.GlueTable)
	for i, name := range ids {
		l.glueIDMap[name] = tir.DropGlueID(i + 1)
	}
	for _, name := range ids {
		g := l.tp.DropPlan.GlueTable[name]
		if g == nil {
			continue
		}
		dg := tir.DropGlueDesc{
			ID:            l.glueIDMap[name],
			CanonicalName: g.ID,
			Kind:          convertGlueKind(g.Kind),
			Type:          l.tconv.convert(g.Type),
			TypeName:      g.TypeName,
			ElemType:      l.tconv.convert(g.ElemType),
			ArrayLen:      g.ArrayLen,
			ElemGlueID:    l.glueIDMap[g.ElemGlueID],
			ReleaseSymbol: g.ReleaseFn,
		}
		for _, f := range g.Fields {
			dg.Fields = append(dg.Fields, tir.DropGlueField{
				Name:   f.Name,
				Index:  f.Index,
				Type:   l.tconv.convert(f.Type),
				GlueID: l.glueIDMap[f.GlueID],
			})
		}
		for _, el := range g.TupleElems {
			dg.TupleElems = append(dg.TupleElems, tir.DropGlueElem{
				Index:  el.Index,
				Type:   l.tconv.convert(el.Type),
				GlueID: l.glueIDMap[el.GlueID],
			})
		}
		for _, v := range g.Variants {
			dv := tir.DropGlueVariant{Name: v.Name, Index: v.Index}
			for _, te := range v.TupleElems {
				dv.TupleElems = append(dv.TupleElems, tir.DropGlueElem{
					Index:  te.Index,
					Type:   l.tconv.convert(te.Type),
					GlueID: l.glueIDMap[te.GlueID],
				})
			}
			for _, sf := range v.StructFields {
				dv.StructFields = append(dv.StructFields, tir.DropGlueField{
					Name:   sf.Name,
					Index:  sf.Index,
					Type:   l.tconv.convert(sf.Type),
					GlueID: l.glueIDMap[sf.GlueID],
				})
			}
			dg.Variants = append(dg.Variants, dv)
		}
		l.prog.DropGlues = append(l.prog.DropGlues, dg)
	}
}

type fnLowerCtx struct {
	l          *lowerer
	srcFn      *cfg.FuncCFG
	df         *drop.DropElaboratedFunc
	outFn      *tir.Function
	localBySym map[symbols.SymbolID]tir.LocalDecl
	flagIDMap  map[drop.DropFlagID]tir.DropFlagID

	bridgeToVal map[cfg.BridgeValID]tir.ValueID
	cfgToVal    map[cfg.ValueID]tir.ValueID
	joinVals    map[tir.ValueID]bool
	funcRefs    map[tir.ValueID]tir.FunctionID
	nextVal     tir.ValueID

	pendingRetOp   map[tir.BlockID]*tir.Operand
	extraBlocks    []*tir.BasicBlock
	flagEventsAt   map[cfg.ProgramPoint][]drop.DropFlagEvent
	entryFlagDecls []drop.DropFlagEvent
}

func (fc *fnLowerCtx) allocVal(typ tir.TypeID, isJoin bool, span diagnostic.Span) tir.ValueID {
	if typ == tir.InvalidType {
		typ = fc.l.tconv.tt.Unit()
	}
	fc.nextVal++
	id := fc.nextVal
	fc.outFn.Values = append(fc.outFn.Values, tir.ValueDecl{
		ID:         id,
		Type:       typ,
		IsJoinTemp: isJoin,
		Span:       toTIRSpan(span),
	})
	if isJoin {
		fc.joinVals[id] = true
	}
	return id
}

func (fc *fnLowerCtx) ensureBridgeVal(bv cfg.BridgeValID, typ types.TypeID, isJoin bool, span diagnostic.Span) tir.ValueID {
	if bv == cfg.InvalidBridgeVal {
		return tir.InvalidValue
	}
	if v, ok := fc.bridgeToVal[bv]; ok {
		return v
	}
	tv := fc.allocVal(fc.l.tconv.convert(typ), isJoin, span)
	fc.bridgeToVal[bv] = tv
	return tv
}

func (fc *fnLowerCtx) ensureCFGTemp(vt cfg.ValueID, typ types.TypeID, isJoin bool, span diagnostic.Span) tir.ValueID {
	if vt == cfg.InvalidValue {
		return tir.InvalidValue
	}
	if v, ok := fc.cfgToVal[vt]; ok {
		return v
	}
	tv := fc.allocVal(fc.l.tconv.convert(typ), isJoin, span)
	fc.cfgToVal[vt] = tv
	return tv
}

func (fc *fnLowerCtx) resolveStmtDest(st cfg.Statement, isJoin bool) tir.ValueID {
	var bv cfg.BridgeValID
	if st.Bridge != nil {
		bv = st.Bridge.ResultVal
	}
	if bv != cfg.InvalidBridgeVal && st.ValTemp != cfg.InvalidValue {
		if existing, ok := fc.bridgeToVal[bv]; ok {
			fc.cfgToVal[st.ValTemp] = existing
			return existing
		}
		if existing, ok := fc.cfgToVal[st.ValTemp]; ok {
			fc.bridgeToVal[bv] = existing
			return existing
		}
		tv := fc.allocVal(fc.l.tconv.convert(st.Type), isJoin, st.Span)
		fc.bridgeToVal[bv] = tv
		fc.cfgToVal[st.ValTemp] = tv
		return tv
	}
	if bv != cfg.InvalidBridgeVal {
		return fc.ensureBridgeVal(bv, st.Type, isJoin, st.Span)
	}
	if st.ValTemp != cfg.InvalidValue {
		return fc.ensureCFGTemp(st.ValTemp, st.Type, isJoin, st.Span)
	}
	return tir.InvalidValue
}

func (fc *fnLowerCtx) resolveSrcOperand(bv cfg.BridgeValID, vt cfg.ValueID, fallbackType types.TypeID) tir.Operand {
	if bv != cfg.InvalidBridgeVal {
		if tv, ok := fc.bridgeToVal[bv]; ok {
			if targetFn, isFn := fc.funcRefs[tv]; isFn {
				return tir.FuncOperand(fc.typeOfVal(tv), targetFn)
			}
			return tir.ValueOperand(fc.typeOfVal(tv), tv)
		}
	}
	if vt != cfg.InvalidValue {
		if tv, ok := fc.cfgToVal[vt]; ok {
			return tir.ValueOperand(fc.typeOfVal(tv), tv)
		}
	}
	if fallbackType != types.Invalid {
		tid := fc.l.tconv.convert(fallbackType)
		if tid == fc.l.tconv.tt.Unit() {
			return tir.ConstOperand(tid, tir.Constant{Kind: tir.ConstUnit})
		}
	}
	return tir.Operand{}
}

func (fc *fnLowerCtx) typeOfVal(tv tir.ValueID) tir.TypeID {
	if vd, ok := fc.outFn.ValueByID(tv); ok {
		return vd.Type
	}
	return tir.InvalidType
}

func (l *lowerer) lowerFunction(fn *cfg.FuncCFG, df *drop.DropElaboratedFunc) (*tir.Function, error) {
	if len(fn.Blocks) == 0 {
		return nil, fmt.Errorf("函数 '%s' 没有任何基本块", fn.Name)
	}
	fid := tir.FunctionID(1)
	if fn.FuncSym != nil {
		if mapped, ok := l.fnIDMap[fn.FuncSym.ID]; ok {
			fid = mapped
		}
	}

	var paramTypes []tir.TypeID
	var fnSpan diagnostic.Span
	if fn.FuncSym != nil {
		for i := 0; i < fn.FuncSym.EffectiveParamCount(); i++ {
			paramTypes = append(paramTypes, l.tconv.convert(fn.FuncSym.EffectiveParamType(i, l.tp.Types)))
		}
		fnSpan = fn.FuncSym.Span
	} else {
		for _, sid := range fn.LocalOrder {
			if m := fn.Locals[sid]; m != nil && m.IsParam {
				paramTypes = append(paramTypes, l.tconv.convert(m.Type))
			}
		}
	}
	if fn.Decl != nil {
		fnSpan = fn.Decl.Span
	}

	outFn := &tir.Function{
		ID:          fid,
		Name:        fn.Name,
		CallingConv: "WON",
		ParamTypes:  paramTypes,
		ReturnType:  l.tconv.convert(fn.ReturnType),
		EntryBlock:  tir.BlockID(fn.Entry),
		ExitBlock:   tir.BlockID(fn.Exit),
		Span:        toTIRSpan(fnSpan),
	}

	fc := &fnLowerCtx{
		l:            l,
		srcFn:        fn,
		df:           df,
		outFn:        outFn,
		localBySym:   make(map[symbols.SymbolID]tir.LocalDecl, len(fn.Locals)),
		flagIDMap:    make(map[drop.DropFlagID]tir.DropFlagID),
		bridgeToVal:  make(map[cfg.BridgeValID]tir.ValueID, len(fn.BridgeVals)+8),
		cfgToVal:     make(map[cfg.ValueID]tir.ValueID, 16),
		joinVals:     make(map[tir.ValueID]bool),
		funcRefs:     make(map[tir.ValueID]tir.FunctionID),
		pendingRetOp: make(map[tir.BlockID]*tir.Operand),
		flagEventsAt: make(map[cfg.ProgramPoint][]drop.DropFlagEvent),
	}

	// 1. 按确定性 LocalOrder 转换局部存储槽位
	var nextLocal tir.LocalID = 1
	for _, sid := range fn.LocalOrder {
		meta := fn.Locals[sid]
		if meta == nil {
			continue
		}
		name := fmt.Sprintf("local_%d", sid)
		if meta.Sym != nil && meta.Sym.Name != "" {
			name = meta.Sym.Name
		}
		ld := tir.LocalDecl{
			ID:       nextLocal,
			Name:     name,
			Type:     l.tconv.convert(meta.Type),
			IsParam:  meta.IsParam,
			ParamIdx: meta.ParamIdx,
			IsMut:    meta.IsMut,
			HasInit:  meta.HasInit,
			ScopeID:  meta.ScopeID,
			Span:     toTIRSpan(meta.Span),
		}
		nextLocal++
		outFn.Locals = append(outFn.Locals, ld)
		fc.localBySym[sid] = ld
	}

	// 2. 预声明全部 BridgeValDecl（保持 JoinTemp 属性及确定性顺序）
	for _, bvd := range fn.BridgeVals {
		tv := fc.allocVal(l.tconv.convert(bvd.Type), bvd.IsJoinTemp, bvd.Span)
		fc.bridgeToVal[bvd.ID] = tv
	}

	// 3. 转换 DropFlag 元数据与事件索引
	if df != nil {
		for idx, fid := range df.FlagOrder {
			fm := df.Flags[fid]
			if fm == nil {
				continue
			}
			tirFID := tir.DropFlagID(idx + 1)
			fc.flagIDMap[fid] = tirFID
			var ownerLocal tir.LocalID
			if ld, ok := fc.localBySym[fm.OwnerSymID]; ok {
				ownerLocal = ld.ID
			}
			tPlace, err := fc.convertPlace(fm.Place, nil)
			if err != nil {
				return nil, err
			}
			outFn.DropFlags = append(outFn.DropFlags, tir.DropFlag{
				ID:         tirFID,
				Name:       fmt.Sprintf("df%d", tirFID),
				OwnerLocal: ownerLocal,
				Place:      tPlace,
				Type:       l.tconv.convert(fm.Type),
				GlueID:     l.glueIDMap[fm.GlueID],
				Initial:    fm.InitialVal,
				Span:       toTIRSpan(fm.Span),
			})
		}
		for _, ev := range df.FlagEvents {
			if ev.Op == drop.FlagDecl {
				fc.entryFlagDecls = append(fc.entryFlagDecls, ev)
			} else {
				fc.flagEventsAt[ev.Point] = append(fc.flagEventsAt[ev.Point], ev)
			}
		}
	}

	// 4. 逐基本块降低指令与终结器
	outFn.Blocks = make([]*tir.BasicBlock, len(fn.Blocks))
	for bi, srcBlk := range fn.Blocks {
		if srcBlk == nil {
			continue
		}
		tb, err := fc.lowerBasicBlock(srcBlk, bi == int(fn.Entry))
		if err != nil {
			return nil, err
		}
		outFn.Blocks[bi] = tb
	}
	if len(fc.extraBlocks) > 0 {
		outFn.Blocks = append(outFn.Blocks, fc.extraBlocks...)
	}

	// 为隐式尾随表达式返回补充 ExitBlock ReturnVal（若存在 normal fallthrough 传入）
	if fn.TailRetVal != cfg.InvalidBridgeVal {
		if exitBlk := outFn.Blocks[fn.Exit]; exitBlk != nil && exitBlk.Terminator.Kind == tir.TermReturn && exitBlk.Terminator.ReturnVal == nil {
			retOp := fc.resolveSrcOperand(fn.TailRetVal, cfg.InvalidValue, fn.ReturnType)
			if retOp.Kind != tir.OperandInvalid {
				exitBlk.Terminator.ReturnVal = &retOp
			}
		}
	}

	rebuildBlockEdges(outFn)
	// 当非 unit 函数的所有路径均已显式返回且没有任何前驱跳转到空 exit 块时，exit 块实质不可达，标记为 TermUnreachable。
	if outFn.ExitBlock >= 0 && int(outFn.ExitBlock) < len(outFn.Blocks) {
		if exitBlk := outFn.Blocks[outFn.ExitBlock]; exitBlk != nil &&
			outFn.ExitBlock != outFn.EntryBlock &&
			len(exitBlk.Predecessors) == 0 &&
			len(exitBlk.Instructions) == 0 &&
			exitBlk.Terminator.Kind == tir.TermReturn &&
			exitBlk.Terminator.ReturnVal == nil &&
			l.tconv.tt.Info(outFn.ReturnType).Kind != tir.TypeUnit {
			exitBlk.Terminator.Kind = tir.TermUnreachable
		}
	}
	return outFn, nil
}

func (fc *fnLowerCtx) lowerBasicBlock(blk *cfg.BasicBlock, isEntry bool) (*tir.BasicBlock, error) {
	tb := &tir.BasicBlock{
		ID:    tir.BlockID(blk.ID),
		Label: blk.Label,
	}

	if isEntry && len(fc.entryFlagDecls) > 0 {
		boolT := fc.l.tconv.tt.Bool()
		for _, ev := range fc.entryFlagDecls {
			if tfid, ok := fc.flagIDMap[ev.Flag]; ok {
				tb.Instructions = append(tb.Instructions, tir.Instruction{
					Kind:     tir.OpDropFlagDecl,
					Type:     boolT,
					FlagID:   tfid,
					FlagBool: false,
					Span:     toTIRSpan(ev.Span),
				})
			}
		}
	}

	for stIdx, st := range blk.Statements {
		if err := fc.lowerStatement(tb, blk.ID, stIdx, st); err != nil {
			return nil, err
		}
	}

	term, err := fc.lowerTerminator(tb, blk)
	if err != nil {
		return nil, err
	}
	tb.Terminator = term
	return tb, nil
}

func (fc *fnLowerCtx) lowerStatement(tb *tir.BasicBlock, blkID cfg.BlockID, stIdx int, st cfg.Statement) error {
	// 确定并记录当前语句之前的前置计算（如果该语句本身与最后一个 CompAggregate / CompConst 共用同一 Dest，则在下方语句中融合发射，避免重复定义）
	var mergeComp *cfg.ComputeOp
	if st.Bridge != nil && len(st.Bridge.PreComputes) > 0 {
		pre := st.Bridge.PreComputes
		if (st.Kind == cfg.OpStaticRef || st.Kind == cfg.OpAggregate) && st.Bridge.ResultVal != cfg.InvalidBridgeVal {
			last := pre[len(pre)-1]
			if last.Dest == st.Bridge.ResultVal {
				mergeComp = &last
				pre = pre[:len(pre)-1]
			}
		}
		if err := fc.emitComputeOps(&tb.Instructions, pre); err != nil {
			return err
		}
	}

	var idxVals []cfg.BridgeValID
	if st.Bridge != nil {
		idxVals = st.Bridge.IndexVals
	}

	stmtType := fc.l.tconv.convert(st.Type)

	switch st.Kind {
	case cfg.OpNop:
		// 空操作无需写入 TIR

	case cfg.OpScopeEnter:
		tb.Instructions = append(tb.Instructions, tir.Instruction{
			Kind:    tir.OpScopeEnter,
			ScopeID: st.ScopeID,
			Span:    toTIRSpan(st.Span),
		})

	case cfg.OpScopeExit:
		locals, err := fc.convertLocalList(st.Locals)
		if err != nil {
			return err
		}
		tb.Instructions = append(tb.Instructions, tir.Instruction{
			Kind:    tir.OpScopeExit,
			ScopeID: st.ScopeID,
			Locals:  locals,
			Span:    toTIRSpan(st.Span),
		})

	case cfg.OpStorageDead:
		locals, err := fc.convertLocalList(st.Locals)
		if err != nil {
			return err
		}
		tb.Instructions = append(tb.Instructions, tir.Instruction{
			Kind:    tir.OpStorageDead,
			ScopeID: st.ScopeID,
			Locals:  locals,
			Span:    toTIRSpan(st.Span),
		})

	case cfg.OpStorageDecl, cfg.OpUninitDecl:
		tp, err := fc.convertPlace(st.Place, idxVals)
		if err != nil {
			return err
		}
		tb.Instructions = append(tb.Instructions,
			tir.Instruction{Kind: tir.OpStorageLive, Type: tp.RootType, Local: tp.Local, Span: toTIRSpan(st.Span)},
			tir.Instruction{Kind: tir.OpInit, Type: stmtTypeOrPlace(stmtType, tp.ElemType), Place: tp, Span: toTIRSpan(st.Span)},
		)

	case cfg.OpInitAssign:
		tp, err := fc.convertPlace(st.Place, idxVals)
		if err != nil {
			return err
		}
		ld, ok := fc.localBySym[st.Place.RootSymID]
		if ok && !ld.IsParam && len(tp.Projections) == 0 {
			tb.Instructions = append(tb.Instructions, tir.Instruction{
				Kind:  tir.OpStorageLive,
				Type:  tp.RootType,
				Local: tp.Local,
				Span:  toTIRSpan(st.Span),
			})
		}
		var srcBV cfg.BridgeValID
		var pb *cfg.BridgePatternBind
		if st.Bridge != nil {
			srcBV = st.Bridge.SrcVal
			pb = st.Bridge.PatternBind
		}
		srcOp := fc.resolveSrcOperand(srcBV, st.SrcTemp, st.Type)
		elemT := stmtTypeOrPlace(stmtType, tp.ElemType)
		if pb != nil && pb.HasSubProj && srcOp.Kind != tir.OperandInvalid && srcOp.Type != elemT {
			extReg := fc.allocVal(elemT, false, st.Span)
			proj := tir.Projection{Kind: tir.ProjTupleField, FieldIndex: pb.TupleIndex, ElemType: elemT}
			if pb.FieldName != "" {
				proj = tir.Projection{Kind: tir.ProjField, FieldName: pb.FieldName, FieldIndex: -1, ElemType: elemT}
			}
			tb.Instructions = append(tb.Instructions, tir.Instruction{
				Kind:        tir.OpExtract,
				Type:        elemT,
				Dest:        extReg,
				Src:         srcOp,
				ExtractProj: proj,
				Span:        toTIRSpan(st.Span),
			})
			srcOp = tir.ValueOperand(elemT, extReg)
		}
		tb.Instructions = append(tb.Instructions, tir.Instruction{
			Kind:  tir.OpInit,
			Type:  elemT,
			Place: tp,
			Src:   srcOp,
			Span:  toTIRSpan(st.Span),
		})

	case cfg.OpAssign, cfg.OpReinit:
		tp, err := fc.convertPlace(st.Place, idxVals)
		if err != nil {
			return err
		}
		var srcBV cfg.BridgeValID
		assignOp := ""
		if st.Bridge != nil {
			srcBV = st.Bridge.SrcVal
			assignOp = st.Bridge.AssignOp
		}
		srcOp := fc.resolveSrcOperand(srcBV, st.SrcTemp, st.Type)
		elemT := stmtTypeOrPlace(stmtType, tp.ElemType)
		if binOp, isComp := compoundBinaryOp(assignOp); isComp && srcOp.Kind != tir.OperandInvalid {
			lhsReg := fc.allocVal(elemT, false, st.Span)
			resReg := fc.allocVal(elemT, false, st.Span)
			tb.Instructions = append(tb.Instructions,
				tir.Instruction{Kind: tir.OpLoad, Type: elemT, Dest: lhsReg, Place: tp.Clone(), Span: toTIRSpan(st.Span)},
				tir.Instruction{Kind: tir.OpBinary, Type: elemT, Dest: resReg, BinaryOp: binOp, Left: tir.ValueOperand(elemT, lhsReg), Right: srcOp, Span: toTIRSpan(st.Span)},
			)
			srcOp = tir.ValueOperand(elemT, resReg)
		}
		tb.Instructions = append(tb.Instructions, tir.Instruction{
			Kind:     tir.OpAssign,
			Type:     elemT,
			Place:    tp,
			Src:      srcOp,
			IsReinit: st.Kind == cfg.OpReinit,
			Span:     toTIRSpan(st.Span),
		})

	case cfg.OpRead:
		if st.Bridge != nil && st.Bridge.ResultVal != cfg.InvalidBridgeVal {
			dest := fc.resolveStmtDest(st, false)
			tp, err := fc.convertPlace(st.Place, idxVals)
			if err != nil {
				return err
			}
			tb.Instructions = append(tb.Instructions, tir.Instruction{
				Kind:  tir.OpLoad,
				Type:  stmtTypeOrPlace(stmtType, tp.ElemType),
				Dest:  dest,
				Place: tp,
				Span:  toTIRSpan(st.Span),
			})
		}

	case cfg.OpCopy, cfg.OpMove:
		tp, err := fc.convertPlace(st.Place, idxVals)
		if err != nil {
			return err
		}
		dest := fc.resolveStmtDest(st, false)
		op := tir.OpCopy
		if st.Kind == cfg.OpMove {
			op = tir.OpMove
		}
		tb.Instructions = append(tb.Instructions, tir.Instruction{
			Kind:  op,
			Type:  stmtTypeOrPlace(stmtType, tp.ElemType),
			Dest:  dest,
			Place: tp,
			Span:  toTIRSpan(st.Span),
		})

	case cfg.OpBorrowShared, cfg.OpBorrowMut, cfg.OpReborrow:
		tp, err := fc.convertPlace(st.Place, idxVals)
		if err != nil {
			return err
		}
		dest := fc.resolveStmtDest(st, false)
		if dest == tir.InvalidValue {
			dest = fc.allocVal(stmtType, false, st.Span)
		}
		var parentPtr *tir.Place
		if st.IsReborrow && st.ParentPlace.RootSymID != 0 {
			pp, err := fc.convertPlace(st.ParentPlace, nil)
			if err != nil {
				return err
			}
			parentPtr = &pp
		}
		tb.Instructions = append(tb.Instructions, tir.Instruction{
			Kind:        tir.OpBorrow,
			Type:        stmtType,
			Dest:        dest,
			Place:       tp,
			BorrowKind:  convertBorrowKind(st.BorrowKind),
			LoanID:      tir.LoanID(st.LoanID),
			ParentPlace: parentPtr,
			Span:        toTIRSpan(st.Span),
		})

	case cfg.OpStaticRef:
		dest := fc.resolveStmtDest(st, false)
		if dest == tir.InvalidValue {
			dest = fc.allocVal(stmtType, false, st.Span)
		}
		strVal := ""
		if mergeComp != nil {
			strVal = mergeComp.StrVal
		}
		tb.Instructions = append(tb.Instructions, tir.Instruction{
			Kind:  tir.OpStaticRef,
			Type:  stmtType,
			Dest:  dest,
			Const: tir.Constant{Kind: tir.ConstString, StrVal: strVal},
			Span:  toTIRSpan(st.Span),
		})

	case cfg.OpAggregate:
		dest := fc.resolveStmtDest(st, false)
		if dest == tir.InvalidValue {
			dest = fc.allocVal(stmtType, false, st.Span)
		}
		if mergeComp != nil {
			inst, err := fc.buildAggregateInst(*mergeComp, dest, stmtType)
			if err != nil {
				return err
			}
			tb.Instructions = append(tb.Instructions, inst)
		} else {
			items := make([]tir.AggregateItem, 0, len(st.AggTemps))
			for _, at := range st.AggTemps {
				op := fc.resolveSrcOperand(cfg.InvalidBridgeVal, at.Temp, types.Invalid)
				if op.Kind != tir.OperandInvalid {
					items = append(items, tir.AggregateItem{
						FieldName:  at.FieldName,
						FieldIndex: at.Index,
						Value:      op,
					})
				}
			}
			aggKind := inferAggregateKind(fc.l.tconv.tt.Info(stmtType).Kind)
			tb.Instructions = append(tb.Instructions, tir.Instruction{
				Kind:     tir.OpAggregateConstruct,
				Type:     stmtType,
				Dest:     dest,
				AggKind:  aggKind,
				AggItems: items,
				Span:     toTIRSpan(st.Span),
			})
		}

	case cfg.OpTempAssign:
		dest := fc.resolveStmtDest(st, true)
		var srcBV cfg.BridgeValID
		if st.Bridge != nil {
			srcBV = st.Bridge.SrcVal
		}
		srcOp := fc.resolveSrcOperand(srcBV, st.SrcTemp, st.Type)
		tb.Instructions = append(tb.Instructions, tir.Instruction{
			Kind: tir.OpValueAssign,
			Type: stmtType,
			Dest: dest,
			Src:  srcOp,
			Span: toTIRSpan(st.Span),
		})

	case cfg.OpCall:
		if err := fc.lowerCallStatement(tb, blkID, stIdx, st); err != nil {
			return err
		}

	case cfg.OpFFITransfer:
		tp, err := fc.convertPlace(st.Place, idxVals)
		if err != nil {
			return err
		}
		var cid tir.ContractID
		if entry, ok := fc.l.ffiByKey[st.CalleeName]; ok {
			cid = entry.id
		}
		dest := fc.resolveStmtDest(st, false)
		tb.Instructions = append(tb.Instructions, tir.Instruction{
			Kind:       tir.OpOwnershipTransfer,
			Type:       stmtTypeOrPlace(stmtType, tp.ElemType),
			Dest:       dest,
			Place:      tp,
			CalleeName: st.CalleeName,
			ContractID: cid,
			GlueID:     fc.l.glueIDMap[st.DropGlue],
			Span:       toTIRSpan(st.Span),
		})

	case cfg.OpDrop:
		tp, err := fc.convertPlace(st.Place, idxVals)
		if err != nil {
			return err
		}
		gid := fc.l.glueIDMap[st.DropGlue]
		if gid == tir.InvalidDropGlue {
			return fmt.Errorf("OpDrop 找不到胶水 '%s'", st.DropGlue)
		}
		tb.Instructions = append(tb.Instructions, tir.Instruction{
			Kind:   tir.OpDrop,
			Type:   stmtTypeOrPlace(stmtType, tp.ElemType),
			Place:  tp,
			GlueID: gid,
			Span:   toTIRSpan(st.Span),
		})

	case cfg.OpCondDrop:
		tp, err := fc.convertPlace(st.Place, idxVals)
		if err != nil {
			return err
		}
		gid := fc.l.glueIDMap[st.DropGlue]
		if gid == tir.InvalidDropGlue {
			return fmt.Errorf("OpCondDrop 找不到胶水 '%s'", st.DropGlue)
		}
		tfid := fc.flagIDMap[drop.DropFlagID(st.DropFlag)]
		if tfid == tir.InvalidDropFlag {
			return fmt.Errorf("OpCondDrop 找不到标志 df%d", st.DropFlag)
		}
		tb.Instructions = append(tb.Instructions, tir.Instruction{
			Kind:   tir.OpCondDrop,
			Type:   stmtTypeOrPlace(stmtType, tp.ElemType),
			Place:  tp,
			FlagID: tfid,
			GlueID: gid,
			Span:   toTIRSpan(st.Span),
		})

	case cfg.OpReturn:
		var srcBV cfg.BridgeValID
		if st.Bridge != nil {
			srcBV = st.Bridge.SrcVal
		}
		retOp := fc.resolveSrcOperand(srcBV, st.SrcTemp, st.Type)
		if retOp.Kind == tir.OperandInvalid && st.Place.RootSymID != 0 {
			tp, err := fc.convertPlace(st.Place, idxVals)
			if err != nil {
				return err
			}
			retOp = tir.PlaceOperand(tp)
		}
		if retOp.Kind != tir.OperandInvalid {
			fc.pendingRetOp[tir.BlockID(blkID)] = &retOp
		}
	}

	// 将 Drop 展开阶段在该语句处记录的 DropFlagSet 事件紧接 emit 为显式 TIR 指令
	if evs := fc.flagEventsAt[cfg.ProgramPoint{Block: blkID, StmtIdx: stIdx}]; len(evs) > 0 {
		boolT := fc.l.tconv.tt.Bool()
		for _, ev := range evs {
			if ev.Op != drop.FlagSetTrue && ev.Op != drop.FlagSetFalse {
				continue
			}
			if tfid, ok := fc.flagIDMap[ev.Flag]; ok {
				tb.Instructions = append(tb.Instructions, tir.Instruction{
					Kind:     tir.OpDropFlagSet,
					Type:     boolT,
					FlagID:   tfid,
					FlagBool: ev.Op == drop.FlagSetTrue,
					Span:     toTIRSpan(ev.Span),
				})
			}
		}
	}
	return nil
}

func (fc *fnLowerCtx) lowerCallStatement(tb *tir.BasicBlock, blkID cfg.BlockID, stIdx int, st cfg.Statement) error {
	retType := fc.l.tconv.convert(st.Type)
	var dest tir.ValueID
	if (st.Bridge != nil && st.Bridge.ResultVal != cfg.InvalidBridgeVal) || st.ValTemp != cfg.InvalidValue {
		dest = fc.resolveStmtDest(st, false)
	}

	argCount := len(st.ArgTemps)
	if st.Bridge != nil && len(st.Bridge.ArgVals) > argCount {
		argCount = len(st.Bridge.ArgVals)
	}
	if len(st.ArgPlaces) > argCount {
		argCount = len(st.ArgPlaces)
	}
	args := make([]tir.Operand, argCount)
	argPlaces := make([]tir.Place, len(st.ArgPlaces))
	for i := 0; i < argCount; i++ {
		var bv cfg.BridgeValID
		if st.Bridge != nil && i < len(st.Bridge.ArgVals) {
			bv = st.Bridge.ArgVals[i]
		}
		var vt cfg.ValueID
		if i < len(st.ArgTemps) {
			vt = st.ArgTemps[i]
		}
		args[i] = fc.resolveSrcOperand(bv, vt, types.Invalid)
		if i < len(st.ArgPlaces) && st.ArgPlaces[i].RootSymID != 0 {
			tp, err := fc.convertPlace(st.ArgPlaces[i], nil)
			if err != nil {
				return err
			}
			argPlaces[i] = tp
			if args[i].Kind == tir.OperandInvalid {
				args[i] = tir.PlaceOperand(tp)
			}
		}
		if args[i].Kind == tir.OperandInvalid {
			args[i] = tir.ConstOperand(fc.l.tconv.tt.Unit(), tir.Constant{Kind: tir.ConstUnit})
		}
	}

	cat := cfg.CallNormal
	foreignNS := ""
	if st.Bridge != nil {
		cat = st.Bridge.CallCat
		foreignNS = st.Bridge.ForeignNS
	}

	// 检查枚举元组构造器 (Some / Ok / Err / UserEnum::Variant)
	if cat == cfg.CallEnumVariant || (st.Callee != nil && st.Callee.Kind == symbols.SymEnumVariant) {
		vName := st.CalleeName
		if idx := strings.LastIndex(vName, "::"); idx >= 0 {
			vName = vName[idx+2:]
		}
		vIdx := 0
		ti := fc.l.tconv.tt.Info(retType)
		for idx, v := range ti.Variants {
			if v.Name == vName {
				vIdx = idx
				break
			}
		}
		items := make([]tir.AggregateItem, len(args))
		for i, a := range args {
			items[i] = tir.AggregateItem{FieldIndex: i, Value: a}
		}
		if dest == tir.InvalidValue {
			dest = fc.allocVal(retType, false, st.Span)
		}
		tb.Instructions = append(tb.Instructions, tir.Instruction{
			Kind:        tir.OpAggregateConstruct,
			Type:        retType,
			Dest:        dest,
			AggKind:     tir.AggEnumVariant,
			AggItems:    items,
			VariantIdx:  vIdx,
			VariantName: vName,
			Span:        toTIRSpan(st.Span),
		})
		return nil
	}

	// 区分外部 C FFI 调用：Callee == nil 且 CallCat == CallForeignFFI，或存在匹配 FFI 契约
	pt := cfg.ProgramPoint{Block: blkID, StmtIdx: stIdx}
	var cidKey string
	if fc.df != nil {
		cidKey = fc.df.FFIContracts[pt]
	}
	contractEntry, hasByName := fc.l.ffiByKey[st.CalleeName]
	isForeign := cat == cfg.CallForeignFFI || hasByName || cidKey != "" || (st.Callee == nil && strings.Contains(st.CalleeName, "::"))
	if isForeign && cat != cfg.CallMethodBuiltin && cat != cfg.CallBuiltin {
		ns := foreignNS
		fnName := st.CalleeName
		if idx := strings.Index(st.CalleeName, "::"); idx >= 0 {
			if ns == "" {
				ns = st.CalleeName[:idx]
			}
			fnName = st.CalleeName[idx+2:]
		}
		var cid tir.ContractID
		hasContract := false
		var argOwn []tir.FFIOwnership
		retOwn := tir.FFIRawUnmanaged
		if hasByName {
			cid = contractEntry.id
			hasContract = true
			retOwn = convertFFIOwnership(contractEntry.contract.Return.Ownership)
			argOwn = make([]tir.FFIOwnership, len(contractEntry.contract.Params))
			for _, pc := range contractEntry.contract.Params {
				if pc.ParamIndex >= 0 && pc.ParamIndex < len(argOwn) {
					argOwn[pc.ParamIndex] = convertFFIOwnership(pc.Ownership)
				}
			}
		} else if cidKey != "" {
			if mapped, ok := fc.l.ffiByID[cidKey]; ok {
				cid = mapped
				hasContract = true
			}
		}
		tb.Instructions = append(tb.Instructions, tir.Instruction{
			Kind:            tir.OpForeignCall,
			Type:            retType,
			Dest:            dest,
			CalleeNamespace: ns,
			CalleeName:      fnName,
			CallingConv:     "C",
			Args:            args,
			ArgPlaces:       argPlaces,
			ArgOwnership:    argOwn,
			ReturnOwnership: retOwn,
			ContractID:      cid,
			HasContract:     hasContract,
			InUnsafe:        true,
			Span:            toTIRSpan(st.Span),
		})
		return nil
	}

	// 内建函数与内建方法
	if cat == cfg.CallBuiltin || cat == cfg.CallMethodBuiltin || (st.Callee != nil && st.Callee.Kind == symbols.SymBuiltin) {
		tb.Instructions = append(tb.Instructions, tir.Instruction{
			Kind:        tir.OpBuiltinCall,
			Type:        retType,
			Dest:        dest,
			BuiltinKind: classifyBuiltin(st.CalleeName),
			CalleeName:  st.CalleeName,
			CallingConv: "WON",
			Args:        args,
			Span:        toTIRSpan(st.Span),
		})
		return nil
	}

	// 标准内部 WON 函数/方法调用
	var targetFID tir.FunctionID
	if st.Callee != nil {
		targetFID = fc.l.fnIDMap[st.Callee.ID]
	}
	if targetFID == tir.InvalidFunc {
		tb.Instructions = append(tb.Instructions, tir.Instruction{
			Kind:        tir.OpBuiltinCall,
			Type:        retType,
			Dest:        dest,
			BuiltinKind: classifyBuiltin(st.CalleeName),
			CalleeName:  st.CalleeName,
			CallingConv: "WON",
			Args:        args,
			Span:        toTIRSpan(st.Span),
		})
		return nil
	}
	tb.Instructions = append(tb.Instructions, tir.Instruction{
		Kind:        tir.OpCall,
		Type:        retType,
		Dest:        dest,
		CalleeFunc:  targetFID,
		CalleeName:  st.CalleeName,
		CallingConv: "WON",
		Args:        args,
		Span:        toTIRSpan(st.Span),
	})
	return nil
}

func (fc *fnLowerCtx) emitComputeOps(out *[]tir.Instruction, ops []cfg.ComputeOp) error {
	for _, op := range ops {
		dest := fc.ensureBridgeVal(op.Dest, op.Type, false, op.Span)
		typ := fc.l.tconv.convert(op.Type)
		switch op.Kind {
		case cfg.CompConst:
			*out = append(*out, tir.Instruction{
				Kind:  tir.OpConst,
				Type:  typ,
				Dest:  dest,
				Const: convertConstant(op),
				Span:  toTIRSpan(op.Span),
			})

		case cfg.CompUnary:
			src := fc.resolveSrcOperand(op.Src, cfg.InvalidValue, types.Invalid)
			if op.Op == "*" {
				if src.Kind == tir.OperandPlace {
					*out = append(*out, tir.Instruction{Kind: tir.OpLoad, Type: typ, Dest: dest, Place: src.Place, Span: toTIRSpan(op.Span)})
				} else {
					*out = append(*out, tir.Instruction{
						Kind:        tir.OpExtract,
						Type:        typ,
						Dest:        dest,
						Src:         src,
						ExtractProj: tir.Projection{Kind: tir.ProjDeref, FieldName: "*", ElemType: typ},
						Span:        toTIRSpan(op.Span),
					})
				}
				continue
			}
			*out = append(*out, tir.Instruction{
				Kind:    tir.OpUnary,
				Type:    typ,
				Dest:    dest,
				UnaryOp: convertUnaryOp(op.Op),
				Src:     src,
				Span:    toTIRSpan(op.Span),
			})

		case cfg.CompBinary:
			left := fc.resolveSrcOperand(op.Left, cfg.InvalidValue, types.Invalid)
			right := fc.resolveSrcOperand(op.Right, cfg.InvalidValue, types.Invalid)
			*out = append(*out, tir.Instruction{
				Kind:     tir.OpBinary,
				Type:     typ,
				Dest:     dest,
				BinaryOp: convertBinaryOp(op.Op),
				Left:     left,
				Right:    right,
				Span:     toTIRSpan(op.Span),
			})

		case cfg.CompCast:
			src := fc.resolveSrcOperand(op.Src, cfg.InvalidValue, types.Invalid)
			ck := classifyCastKind(fc.l.tconv.tt, src.Type, typ)
			*out = append(*out, tir.Instruction{
				Kind:     tir.OpCast,
				Type:     typ,
				Dest:     dest,
				CastKind: ck,
				Src:      src,
				Span:     toTIRSpan(op.Span),
			})

		case cfg.CompEnumUnit:
			vIdx := op.VariantIdx
			if vIdx < 0 {
				vIdx = 0
				ti := fc.l.tconv.tt.Info(typ)
				for idx, v := range ti.Variants {
					if v.Name == op.VariantName {
						vIdx = idx
						break
					}
				}
			}
			*out = append(*out, tir.Instruction{
				Kind:        tir.OpAggregateConstruct,
				Type:        typ,
				Dest:        dest,
				AggKind:     tir.AggEnumVariant,
				VariantIdx:  vIdx,
				VariantName: op.VariantName,
				Span:        toTIRSpan(op.Span),
			})

		case cfg.CompFuncRef:
			if op.FuncSym != nil {
				if fid, ok := fc.l.fnIDMap[op.FuncSym.ID]; ok {
					fc.funcRefs[dest] = fid
				}
			}

		case cfg.CompAggregate:
			inst, err := fc.buildAggregateInst(op, dest, typ)
			if err != nil {
				return err
			}
			*out = append(*out, inst)

		case cfg.CompExtractField:
			src := fc.resolveSrcOperand(op.Src, cfg.InvalidValue, types.Invalid)
			*out = append(*out, tir.Instruction{
				Kind:        tir.OpExtract,
				Type:        typ,
				Dest:        dest,
				Src:         src,
				ExtractProj: tir.Projection{Kind: tir.ProjField, FieldName: op.FieldName, FieldIndex: op.FieldIdx, ElemType: typ},
				Span:        toTIRSpan(op.Span),
			})

		case cfg.CompExtractIndex:
			src := fc.resolveSrcOperand(op.Src, cfg.InvalidValue, types.Invalid)
			proj := tir.Projection{Kind: tir.ProjDynIndex, ElemType: typ}
			if op.Left != cfg.InvalidBridgeVal {
				if iv, ok := fc.bridgeToVal[op.Left]; ok {
					proj.IndexValue = iv
				}
			}
			*out = append(*out, tir.Instruction{
				Kind:        tir.OpExtract,
				Type:        typ,
				Dest:        dest,
				Src:         src,
				ExtractProj: proj,
				Span:        toTIRSpan(op.Span),
			})

		case cfg.CompRange:
			var items []tir.AggregateItem
			if op.RangeStart != cfg.InvalidBridgeVal {
				items = append(items, tir.AggregateItem{FieldName: "start", FieldIndex: 0, Value: fc.resolveSrcOperand(op.RangeStart, cfg.InvalidValue, types.Invalid)})
			}
			if op.RangeEnd != cfg.InvalidBridgeVal {
				items = append(items, tir.AggregateItem{FieldName: "end", FieldIndex: 1, Value: fc.resolveSrcOperand(op.RangeEnd, cfg.InvalidValue, types.Invalid)})
			}
			*out = append(*out, tir.Instruction{
				Kind:        tir.OpAggregateConstruct,
				Type:        typ,
				Dest:        dest,
				AggKind:     tir.AggRange,
				AggItems:    items,
				IsInclusive: op.IsInclusive,
				Span:        toTIRSpan(op.Span),
			})

		case cfg.CompInterpString:
			items := make([]tir.AggregateItem, 0, len(op.InterpParts))
			for i, pt := range op.InterpParts {
				if pt.IsExpr {
					items = append(items, tir.AggregateItem{FieldIndex: i, Value: fc.resolveSrcOperand(pt.Value, cfg.InvalidValue, types.Invalid)})
				} else {
					strT := fc.l.tconv.convert(fc.l.tp.Types.StrView)
					items = append(items, tir.AggregateItem{FieldIndex: i, Value: tir.ConstOperand(strT, tir.Constant{Kind: tir.ConstString, StrVal: pt.Literal})})
				}
			}
			*out = append(*out, tir.Instruction{
				Kind:     tir.OpAggregateConstruct,
				Type:     typ,
				Dest:     dest,
				AggKind:  tir.AggInterpString,
				AggItems: items,
				Span:     toTIRSpan(op.Span),
			})

		case cfg.CompTryUnwrap:
			src := fc.resolveSrcOperand(op.Src, cfg.InvalidValue, types.Invalid)
			*out = append(*out, tir.Instruction{
				Kind:        tir.OpBuiltinCall,
				Type:        typ,
				Dest:        dest,
				BuiltinKind: tir.BuiltinTryUnwrap,
				CalleeName:  "__try_unwrap",
				CallingConv: "WON",
				Args:        []tir.Operand{src},
				Span:        toTIRSpan(op.Span),
			})
		}
	}
	return nil
}

func (fc *fnLowerCtx) buildAggregateInst(op cfg.ComputeOp, dest tir.ValueID, typ tir.TypeID) (tir.Instruction, error) {
	items := make([]tir.AggregateItem, 0, len(op.AggItems))
	for _, it := range op.AggItems {
		var valOp tir.Operand
		if it.Shorthand.RootSymID != 0 {
			tp, err := fc.convertPlace(it.Shorthand, nil)
			if err != nil {
				return tir.Instruction{}, err
			}
			valOp = tir.PlaceOperand(tp)
		} else {
			valOp = fc.resolveSrcOperand(it.Value, cfg.InvalidValue, types.Invalid)
		}
		if valOp.Kind != tir.OperandInvalid {
			items = append(items, tir.AggregateItem{
				FieldName:  it.FieldName,
				FieldIndex: it.FieldIndex,
				Value:      valOp,
			})
		}
	}
	kind := tir.AggTuple
	vIdx := op.VariantIdx
	vName := op.VariantName
	switch op.AggKind {
	case "struct":
		kind = tir.AggStruct
	case "tuple":
		kind = tir.AggTuple
	case "array":
		kind = tir.AggArray
	case "array_repeat":
		kind = tir.AggArrayRepeat
	case "enum_variant":
		kind = tir.AggEnumVariant
		if op.VariantInfo != nil {
			vName = op.VariantInfo.Name
			ti := fc.l.tconv.tt.Info(typ)
			for idx, v := range ti.Variants {
				if v.Name == vName {
					vIdx = idx
					break
				}
			}
		}
	}
	return tir.Instruction{
		Kind:        tir.OpAggregateConstruct,
		Type:        typ,
		Dest:        dest,
		AggKind:     kind,
		AggItems:    items,
		RepeatLen:   op.RepeatLen,
		VariantIdx:  vIdx,
		VariantName: vName,
		Span:        toTIRSpan(op.Span),
	}, nil
}

func (fc *fnLowerCtx) lowerTerminator(tb *tir.BasicBlock, blk *cfg.BasicBlock) (tir.Terminator, error) {
	srcTerm := blk.Terminator
	if srcTerm.Bridge != nil && len(srcTerm.Bridge.PreComputes) > 0 {
		if err := fc.emitComputeOps(&tb.Instructions, srcTerm.Bridge.PreComputes); err != nil {
			return tir.Terminator{}, err
		}
	}

	span := toTIRSpan(srcTerm.Span)
	switch srcTerm.Kind {
	case cfg.TermGoto:
		var tgt tir.BlockID = tir.InvalidBlock
		if len(srcTerm.Targets) > 0 {
			tgt = tir.BlockID(srcTerm.Targets[0])
		}
		if srcTerm.Bridge != nil && srcTerm.Bridge.YieldDest != cfg.InvalidBridgeVal {
			yieldDest := fc.ensureBridgeVal(srcTerm.Bridge.YieldDest, types.Invalid, true, srcTerm.Span)
			yieldSrc := fc.resolveSrcOperand(srcTerm.Bridge.YieldVal, cfg.InvalidValue, types.Invalid)
			if yieldSrc.Kind != tir.OperandInvalid {
				tb.Instructions = append(tb.Instructions, tir.Instruction{
					Kind: tir.OpValueAssign,
					Type: fc.typeOfVal(yieldDest),
					Dest: yieldDest,
					Src:  yieldSrc,
					Span: span,
				})
			}
		}
		return tir.Terminator{Kind: tir.TermGoto, Target: tgt, Span: span}, nil

	case cfg.TermSwitchInt:
		tbData := srcTerm.Bridge
		switchKind := cfg.BridgeSwitchNone
		var condOp tir.Operand
		if tbData != nil {
			switchKind = tbData.SwitchKind
			if tbData.CondVal != cfg.InvalidBridgeVal {
				condOp = fc.resolveSrcOperand(tbData.CondVal, cfg.InvalidValue, types.Invalid)
			}
		}

		// 短路逻辑与/或：构造短路值跳转蹦床基本块确保 JoinTemp 两条前驱路径均严格赋值
		if (switchKind == cfg.BridgeSwitchLogicAnd || switchKind == cfg.BridgeSwitchLogicOr) && len(srcTerm.Targets) == 2 {
			rhsBlk := tir.BlockID(srcTerm.Targets[0])
			mergeBlk := tir.BlockID(srcTerm.Targets[1])
			shortBlkID := tir.BlockID(len(fc.srcFn.Blocks) + len(fc.extraBlocks))
			boolT := fc.l.tconv.tt.Bool()
			var insts []tir.Instruction
			if tbData != nil && tbData.YieldDest != cfg.InvalidBridgeVal {
				jReg := fc.ensureBridgeVal(tbData.YieldDest, fc.l.tp.Types.Bool, true, srcTerm.Span)
				insts = append(insts, tir.Instruction{
					Kind: tir.OpValueAssign,
					Type: boolT,
					Dest: jReg,
					Src:  tir.ConstOperand(boolT, tir.Constant{Kind: tir.ConstBool, BoolVal: tbData.ShortCircuitVal}),
					Span: span,
				})
			}
			fc.extraBlocks = append(fc.extraBlocks, &tir.BasicBlock{
				ID:           shortBlkID,
				Label:        fmt.Sprintf("logic.short%d", shortBlkID),
				Instructions: insts,
				Terminator:   tir.Terminator{Kind: tir.TermGoto, Target: mergeBlk, Span: span},
			})
			if condOp.Kind == tir.OperandInvalid {
				condOp = tir.ConstOperand(boolT, tir.Constant{Kind: tir.ConstBool, BoolVal: true})
			}
			thenTgt, elseTgt := rhsBlk, shortBlkID
			if switchKind == cfg.BridgeSwitchLogicOr {
				thenTgt, elseTgt = shortBlkID, rhsBlk
			}
			return tir.Terminator{
				Kind:      tir.TermBranchIf,
				Cond:      condOp,
				ThenBlock: thenTgt,
				ElseBlock: elseTgt,
				Span:      span,
			}, nil
		}

		// 布尔二路条件分支 (if / while / match.guard)
		if (switchKind == cfg.BridgeSwitchIf || switchKind == cfg.BridgeSwitchWhile || switchKind == cfg.BridgeSwitchMatchGuard) && len(srcTerm.Targets) == 2 {
			boolT := fc.l.tconv.tt.Bool()
			if condOp.Kind == tir.OperandInvalid {
				condOp = tir.ConstOperand(boolT, tir.Constant{Kind: tir.ConstBool, BoolVal: true})
			}
			return tir.Terminator{
				Kind:      tir.TermBranchIf,
				Cond:      condOp,
				ThenBlock: tir.BlockID(srcTerm.Targets[0]),
				ElseBlock: tir.BlockID(srcTerm.Targets[1]),
				Span:      span,
			}, nil
		}

		// 多路 Switch（match / ? / for / 合成二路 Switch）
		if condOp.Kind == tir.OperandInvalid {
			// 对无 AST Bridge 的纯合成 CFG 兜底提供一个合法布尔或 i64 判别式
			i64T := fc.l.tconv.convert(fc.l.tp.Types.I64)
			condOp = tir.ConstOperand(i64T, tir.Constant{Kind: tir.ConstInt, IntText: "0"})
		}
		tirSwitchKind := tir.SwitchGeneric
		switch switchKind {
		case cfg.BridgeSwitchMatch:
			tirSwitchKind = tir.SwitchMatch
		case cfg.BridgeSwitchTry:
			tirSwitchKind = tir.SwitchTry
		case cfg.BridgeSwitchFor:
			tirSwitchKind = tir.SwitchLoopIter
		}
		cases := make([]tir.SwitchCase, len(srcTerm.Targets))
		for i, t := range srcTerm.Targets {
			sc := tir.SwitchCase{
				Kind:     tir.CaseInt,
				IntValue: int64(i),
				Label:    fmt.Sprintf("case_%d", i),
				Target:   tir.BlockID(t),
			}
			if tbData != nil && i < len(tbData.MatchCases) {
				mc := tbData.MatchCases[i]
				sc.Label = mc.Label
				sc.Guarded = mc.Guarded
				sc.VariantIndex = mc.VariantIndex
				if switchKind == cfg.BridgeSwitchTry {
					if i == 0 {
						sc.Kind = tir.CaseTryOk
					} else {
						sc.Kind = tir.CaseTryErr
					}
				} else if switchKind == cfg.BridgeSwitchMatch {
					if mc.IsIntLiteral {
						sc.Kind = tir.CaseInt
						sc.IntValue = mc.IntValue
					} else {
						sc.Kind = tir.CasePatternArm
					}
				}
			}
			cases[i] = sc
		}
		defaultBlk := tir.InvalidBlock
		if len(cases) > 0 {
			defaultBlk = cases[len(cases)-1].Target
		}
		return tir.Terminator{
			Kind:       tir.TermSwitch,
			Cond:       condOp,
			SwitchKind: tirSwitchKind,
			Cases:      cases,
			DefaultBlk: defaultBlk,
			Span:       span,
		}, nil

	case cfg.TermReturn:
		var retOpPtr *tir.Operand
		if srcTerm.Bridge != nil && srcTerm.Bridge.RetVal != cfg.InvalidBridgeVal {
			op := fc.resolveSrcOperand(srcTerm.Bridge.RetVal, cfg.InvalidValue, fc.srcFn.ReturnType)
			if op.Kind != tir.OperandInvalid {
				retOpPtr = &op
			}
		} else if pending := fc.pendingRetOp[tir.BlockID(blk.ID)]; pending != nil {
			cp := *pending
			retOpPtr = &cp
		} else if srcTerm.Bridge != nil && srcTerm.Bridge.TryErrSourceVal != cfg.InvalidBridgeVal {
			// '?' 错误传播提前返回：当前块直接沿用宿主函数返回类型构造传播返回占位
			retT := fc.outFn.ReturnType
			if retT != fc.l.tconv.tt.Unit() {
				srcErr := fc.resolveSrcOperand(srcTerm.Bridge.TryErrSourceVal, cfg.InvalidValue, types.Invalid)
				retReg := fc.allocVal(retT, false, srcTerm.Span)
				tb.Instructions = append(tb.Instructions, tir.Instruction{
					Kind:        tir.OpBuiltinCall,
					Type:        retT,
					Dest:        retReg,
					BuiltinKind: tir.BuiltinTryUnwrap,
					CalleeName:  "__try_propagate_err",
					CallingConv: "WON",
					Args:        []tir.Operand{srcErr},
					Span:        span,
				})
				op := tir.ValueOperand(retT, retReg)
				retOpPtr = &op
			}
		}
		return tir.Terminator{
			Kind:      tir.TermReturn,
			ReturnVal: retOpPtr,
			Span:      span,
		}, nil

	case cfg.TermUnreachable:
		isTrap := false
		reason := ""
		if srcTerm.Bridge != nil {
			isTrap = srcTerm.Bridge.IsTrap
			reason = srcTerm.Bridge.TrapReason
		}
		return tir.Terminator{
			Kind:       tir.TermUnreachable,
			IsTrap:     isTrap,
			TrapReason: reason,
			Span:       span,
		}, nil

	default:
		return tir.Terminator{Kind: tir.TermNone, Span: span}, nil
	}
}

func (fc *fnLowerCtx) convertLocalList(syms []symbols.SymbolID) ([]tir.LocalID, error) {
	if len(syms) == 0 {
		return nil, nil
	}
	out := make([]tir.LocalID, 0, len(syms))
	for _, sid := range syms {
		ld, ok := fc.localBySym[sid]
		if !ok {
			return nil, fmt.Errorf("引用未在函数 Locals 声明的符号 #%d", sid)
		}
		out = append(out, ld.ID)
	}
	return out, nil
}

func (fc *fnLowerCtx) convertPlace(p cfg.Place, idxVals []cfg.BridgeValID) (tir.Place, error) {
	ld, ok := fc.localBySym[p.RootSymID]
	if !ok {
		return tir.Place{}, fmt.Errorf("Place '%s' 引用未声明局部符号 #%d", p.String(), p.RootSymID)
	}
	tp := tir.Place{
		Local:     ld.ID,
		LocalName: ld.Name,
		RootType:  ld.Type,
		ElemType:  fc.l.tconv.convert(p.ElemType),
	}
	if tp.ElemType == tir.InvalidType {
		tp.ElemType = ld.Type
	}
	dynIdxCursor := 0
	for _, proj := range p.Projections {
		elemT := fc.l.tconv.convert(proj.ElemType)
		if elemT == tir.InvalidType {
			elemT = tp.RootType
		}
		tProj := tir.Projection{
			FieldName:    proj.FieldName,
			FieldIndex:   proj.FieldIndex,
			VariantIndex: proj.VariantIdx,
			ConstIndex:   proj.ConstIndex,
			ElemType:     elemT,
		}
		switch proj.Kind {
		case cfg.ProjField:
			tProj.Kind = tir.ProjField
		case cfg.ProjTupleField:
			tProj.Kind = tir.ProjTupleField
		case cfg.ProjConstIdx:
			tProj.Kind = tir.ProjConstIndex
		case cfg.ProjIndex:
			tProj.Kind = tir.ProjDynIndex
			if dynIdxCursor < len(idxVals) {
				tProj.IndexValue = fc.ensureBridgeVal(idxVals[dynIdxCursor], types.Invalid, false, p.Span)
				dynIdxCursor++
			}
			if proj.IndexSym != 0 {
				if ild, ok := fc.localBySym[proj.IndexSym]; ok {
					tProj.IndexLocal = ild.ID
				}
			}
		case cfg.ProjDeref:
			tProj.Kind = tir.ProjDeref
		case cfg.ProjDowncast:
			tProj.Kind = tir.ProjDowncast
		}
		tp.Projections = append(tp.Projections, tProj)
	}
	return tp, nil
}

func rebuildBlockEdges(fn *tir.Function) {
	for _, b := range fn.Blocks {
		if b != nil {
			b.Successors = nil
			b.Predecessors = nil
		}
	}
	idxByID := make(map[tir.BlockID]*tir.BasicBlock, len(fn.Blocks))
	for _, b := range fn.Blocks {
		if b != nil {
			idxByID[b.ID] = b
		}
	}
	for _, b := range fn.Blocks {
		if b == nil {
			continue
		}
		var tgts []tir.BlockID
		switch b.Terminator.Kind {
		case tir.TermGoto:
			tgts = []tir.BlockID{b.Terminator.Target}
		case tir.TermBranchIf:
			tgts = []tir.BlockID{b.Terminator.ThenBlock, b.Terminator.ElseBlock}
		case tir.TermSwitch:
			for _, c := range b.Terminator.Cases {
				tgts = append(tgts, c.Target)
			}
			if b.Terminator.DefaultBlk != tir.InvalidBlock {
				tgts = append(tgts, b.Terminator.DefaultBlk)
			}
		}
		seen := make(map[tir.BlockID]bool, len(tgts))
		for _, tgt := range tgts {
			if seen[tgt] || tgt == tir.InvalidBlock {
				continue
			}
			seen[tgt] = true
			if dst, ok := idxByID[tgt]; ok {
				b.Successors = append(b.Successors, tgt)
				dst.Predecessors = append(dst.Predecessors, b.ID)
			}
		}
	}
}

func toTIRSpan(sp diagnostic.Span) tir.SourceSpan {
	return tir.SourceSpan{
		File:      sp.File,
		StartLine: sp.Start.Line,
		StartCol:  sp.Start.Column,
		StartByte: sp.Start.Byte,
		EndLine:   sp.End.Line,
		EndCol:    sp.End.Column,
		EndByte:   sp.End.Byte,
	}
}

func stmtTypeOrPlace(a, b tir.TypeID) tir.TypeID {
	if a != tir.InvalidType {
		return a
	}
	return b
}

func convertConstant(op cfg.ComputeOp) tir.Constant {
	switch op.ConstKind {
	case "bool":
		return tir.Constant{Kind: tir.ConstBool, BoolVal: op.BoolVal}
	case "int":
		return tir.Constant{Kind: tir.ConstInt, IntText: op.IntText}
	case "float":
		return tir.Constant{Kind: tir.ConstFloat, FloatVal: op.FloatVal, FloatRaw: op.FloatRaw}
	case "char":
		return tir.Constant{Kind: tir.ConstChar, RuneVal: op.RuneVal}
	case "byte":
		return tir.Constant{Kind: tir.ConstByte, ByteVal: op.ByteVal}
	case "string":
		return tir.Constant{Kind: tir.ConstString, StrVal: op.StrVal}
	default:
		return tir.Constant{Kind: tir.ConstUnit}
	}
}

func convertUnaryOp(op string) tir.UnaryOp {
	switch op {
	case "-":
		return tir.UnaryNeg
	case "~":
		return tir.UnaryBitNot
	default:
		return tir.UnaryNot
	}
}

func convertBinaryOp(op string) tir.BinaryOp {
	switch op {
	case "+":
		return tir.BinAdd
	case "-":
		return tir.BinSub
	case "*":
		return tir.BinMul
	case "/":
		return tir.BinDiv
	case "%":
		return tir.BinMod
	case "&":
		return tir.BinBitAnd
	case "|":
		return tir.BinBitOr
	case "^":
		return tir.BinBitXor
	case "<<":
		return tir.BinShl
	case ">>":
		return tir.BinShr
	case "==":
		return tir.BinEq
	case "!=":
		return tir.BinNe
	case "<":
		return tir.BinLt
	case "<=":
		return tir.BinLe
	case ">":
		return tir.BinGt
	default:
		return tir.BinGe
	}
}

func compoundBinaryOp(assignOp string) (tir.BinaryOp, bool) {
	switch assignOp {
	case "+=":
		return tir.BinAdd, true
	case "-=":
		return tir.BinSub, true
	case "*=":
		return tir.BinMul, true
	case "/=":
		return tir.BinDiv, true
	case "%=":
		return tir.BinMod, true
	case "&=":
		return tir.BinBitAnd, true
	case "|=":
		return tir.BinBitOr, true
	case "^=":
		return tir.BinBitXor, true
	case "<<=":
		return tir.BinShl, true
	case ">>=":
		return tir.BinShr, true
	default:
		return 0, false
	}
}

func classifyCastKind(tt *tir.TypeTable, from, to tir.TypeID) tir.CastKind {
	fi := tt.Info(from)
	ti := tt.Info(to)
	switch {
	case fi.Kind == tir.TypeRef && ti.Kind == tir.TypeRawPtr:
		return tir.CastRefToRawPtr
	case fi.Kind == tir.TypeRawPtr && ti.Kind == tir.TypeRawPtr:
		return tir.CastPtrToPtr
	case fi.Kind == tir.TypeRawPtr && ti.Kind == tir.TypeInt:
		return tir.CastPtrToInt
	case fi.Kind == tir.TypeInt && ti.Kind == tir.TypeRawPtr:
		return tir.CastIntToPtr
	default:
		return tir.CastNumeric
	}
}

func classifyBuiltin(name string) tir.BuiltinKind {
	switch {
	case name == "print":
		return tir.BuiltinPrint
	case name == "println":
		return tir.BuiltinPrintln
	case name == "panic":
		return tir.BuiltinPanic
	case name == "assert":
		return tir.BuiltinAssert
	case name == "bytes_equal":
		return tir.BuiltinBytesEqual
	case name == "String::from" || name == "from":
		return tir.BuiltinStringFrom
	case name == "as_ptr":
		return tir.BuiltinAsPtr
	case name == "as_mut_ptr":
		return tir.BuiltinAsMutPtr
	case name == "as_bytes":
		return tir.BuiltinAsBytes
	case name == "len":
		return tir.BuiltinLen
	case name == "is_empty":
		return tir.BuiltinIsEmpty
	case name == "push_str":
		return tir.BuiltinPushStr
	default:
		return tir.BuiltinOther
	}
}

func convertBorrowKind(k cfg.BorrowKind) tir.BorrowKind {
	switch k {
	case cfg.BorrowMut:
		return tir.BorrowMut
	case cfg.BorrowReborrowShared:
		return tir.BorrowReborrowShared
	case cfg.BorrowReborrowMut:
		return tir.BorrowReborrowMut
	default:
		return tir.BorrowShared
	}
}

func convertGlueKind(k drop.DropGlueKind) tir.DropGlueKind {
	switch k {
	case drop.GlueString:
		return tir.GlueString
	case drop.GlueStruct:
		return tir.GlueStruct
	case drop.GlueEnum:
		return tir.GlueEnum
	case drop.GlueTuple:
		return tir.GlueTuple
	case drop.GlueArray:
		return tir.GlueArray
	case drop.GlueForeignResource:
		return tir.GlueForeignResource
	case drop.GlueRegisteredResource:
		return tir.GlueRegisteredResource
	case drop.GlueCustomDropReserved:
		return tir.GlueCustomDropReserved
	default:
		return tir.GlueNone
	}
}

func convertFFIOwnership(o bindgen.FFIOwnershipKind) tir.FFIOwnership {
	switch o {
	case bindgen.FFIOwnedTransfer:
		return tir.FFIOwnedTransfer
	case bindgen.FFIBorrowedShared:
		return tir.FFIBorrowShared
	case bindgen.FFIBorrowedMut:
		return tir.FFIBorrowMut
	case bindgen.FFIOwnedReturn:
		return tir.FFIOwnedReturn
	case bindgen.FFIOpaqueHandle:
		return tir.FFIOpaqueHandle
	case bindgen.FFINonOwningRaw:
		return tir.FFIRawUnmanaged
	default:
		return tir.FFIUnknown
	}
}

func inferAggregateKind(tk tir.TypeKind) tir.AggregateKind {
	switch tk {
	case tir.TypeStruct:
		return tir.AggStruct
	case tir.TypeArray:
		return tir.AggArray
	case tir.TypeEnum:
		return tir.AggEnumVariant
	default:
		return tir.AggTuple
	}
}

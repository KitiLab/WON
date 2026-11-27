package c11

import (
	"fmt"
	"math"
	"math/big"
	"path/filepath"
	"strings"

	"github.com/KitiLab/WON/compiler/poc/internal/tir"
)

// Emitter 将通过校验的后端无关 *tir.Program 翻译为严格遵循 ISO C11 (-pedantic-errors) 的原生 C 源码。
type Emitter struct {
	target Target
}

// NewEmitter 创建绑定指定目标配置的 TIR -> C11 代码生成器。
func NewEmitter(target Target) *Emitter {
	return &Emitter{target: target}
}

// Emit 对 *tir.Program 执行结构校验、纯 TIR 后端能力网关审核，并生成严格 ISO C11 源码。
func (e *Emitter) Emit(prog *tir.Program) (string, error) {
	if e == nil {
		return "", fmt.Errorf("WON-INT-001: C11 Emitter 接收者为空")
	}
	if err := ValidateProgram(prog, e.target); err != nil {
		return "", err
	}

	ctx := &emitCtx{
		prog: prog,
		tt:   prog.Types,
	}
	if err := ctx.emitProgram(); err != nil {
		return "", err
	}
	return ctx.sb.String(), nil
}

type emitCtx struct {
	prog   *tir.Program
	tt     *tir.TypeTable
	sb     strings.Builder
	indent int
}

func (c *emitCtx) writeLine(s string) {
	if s == "" {
		c.sb.WriteByte('\n')
		return
	}
	for i := 0; i < c.indent; i++ {
		c.sb.WriteString("    ")
	}
	c.sb.WriteString(s)
	c.sb.WriteByte('\n')
}

func (c *emitCtx) emitProgram() error {
	c.writeLine("/* 由 WON Phase 2C.1 TIR->C11 后端自动生成 - 严格遵循 ISO C11 标准 (Linux x86_64 LP64 ABI) */")
	c.writeLine("#define _POSIX_C_SOURCE 200809L")
	c.writeLine("#include <stdio.h>")
	c.writeLine("#include <stdlib.h>")
	c.writeLine("#include <stddef.h>")
	c.writeLine("#include <stdint.h>")
	c.writeLine("#include <stdbool.h>")
	c.writeLine("#include <inttypes.h>")
	c.writeLine("#include <string.h>")
	c.writeLine(`_Static_assert(sizeof(void*) == 8 && sizeof(long) == 8, "WON C11 backend targets 64-bit LP64 ABI");`)

	c.emitHeaderIncludes()
	c.writeLine("")

	// 按需生成内置辅助函数，避免 Clang -Werror=unused-function
	if c.programUsesBuiltin(tir.BuiltinBytesEqual) {
		c.writeLine("// --- WON 内置安全字节比较辅助函数 ---")
		c.writeLine("static inline bool won_bytes_equal(const uint8_t* a, const uint8_t* b, unsigned long len) {")
		c.writeLine("    return memcmp(a, b, len) == 0;")
		c.writeLine("}")
		c.writeLine("")
	}

	// 前向声明所有非 main 用户函数，支持任意互调与前向引用
	protoCount := 0
	for _, fn := range c.prog.Functions {
		if fn == nil || fn.Name == "main" {
			continue
		}
		sig, _, err := c.buildFuncSignature(fn)
		if err != nil {
			return err
		}
		c.writeLine(sig + ";")
		protoCount++
	}
	if protoCount > 0 {
		c.writeLine("")
	}

	for _, fn := range c.prog.Functions {
		if fn == nil {
			continue
		}
		if err := c.emitFunction(fn); err != nil {
			return err
		}
	}
	return nil
}

func (c *emitCtx) emitHeaderIncludes() {
	seen := make(map[string]bool)
	recordIncluded := func(raw string) {
		trimmed := strings.Trim(strings.TrimSpace(raw), `<>"`)
		if trimmed != "" {
			seen[trimmed] = true
			seen[filepath.Base(trimmed)] = true
		}
	}

	for _, fh := range c.prog.ForeignHeaders {
		spec := strings.TrimSpace(fh.HeaderPath)
		if spec == "" {
			continue
		}
		if strings.HasPrefix(spec, "<") && strings.HasSuffix(spec, ">") {
			c.writeLine(fmt.Sprintf("#include %s", spec))
		} else if strings.HasPrefix(spec, `"`) && strings.HasSuffix(spec, `"`) {
			c.writeLine(fmt.Sprintf("#include %s", spec))
		} else {
			c.writeLine(fmt.Sprintf("#include %q", spec))
		}
		recordIncluded(spec)
	}

	for _, cat := range c.prog.FFICatalogs {
		hdr := strings.TrimSpace(cat.HeaderPath)
		if hdr == "" {
			continue
		}
		if seen[hdr] || seen[filepath.Base(hdr)] {
			continue
		}
		c.writeLine(fmt.Sprintf("#include %q", hdr))
		recordIncluded(hdr)
	}
}

func (c *emitCtx) programUsesBuiltin(kind tir.BuiltinKind) bool {
	for _, fn := range c.prog.Functions {
		if fn == nil {
			continue
		}
		for _, b := range fn.Blocks {
			if b == nil {
				continue
			}
			for _, inst := range b.Instructions {
				if inst.Kind == tir.OpBuiltinCall && inst.BuiltinKind == kind {
					return true
				}
			}
		}
	}
	return false
}

func sanitizeCIdent(name string, fallback string) string {
	if name == "" {
		return fallback
	}
	var sb strings.Builder
	for i := 0; i < len(name); i++ {
		ch := name[i]
		isLetter := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
		isDigit := ch >= '0' && ch <= '9'
		if isLetter || (i > 0 && isDigit) {
			sb.WriteByte(ch)
		} else {
			sb.WriteByte('_')
		}
	}
	res := sb.String()
	if res == "" {
		return fallback
	}
	return res
}

func localCVarName(ld tir.LocalDecl) string {
	return fmt.Sprintf("_won_l%d_%s", ld.ID, sanitizeCIdent(ld.Name, "v"))
}

func valCTempName(vid tir.ValueID) string {
	return fmt.Sprintf("_won_t%d", vid)
}

func dropFlagCVarName(fid tir.DropFlagID) string {
	return fmt.Sprintf("_won_df%d", fid)
}

func (c *emitCtx) paramLocalsInOrder(fn *tir.Function) []string {
	params := make([]string, len(fn.ParamTypes))
	byIdx := make(map[int]string)
	for _, ld := range fn.Locals {
		if ld.IsParam {
			byIdx[ld.ParamIdx] = localCVarName(ld)
		}
	}
	for i := range fn.ParamTypes {
		if name, ok := byIdx[i]; ok {
			params[i] = name
		} else {
			params[i] = fmt.Sprintf("_won_p%d", i)
		}
	}
	return params
}

func (c *emitCtx) buildFuncSignature(fn *tir.Function) (sig string, isUnitMain bool, err error) {
	retC, err := MapType(c.tt, fn.ReturnType, PosReturn)
	if err != nil {
		return "", false, err
	}

	if fn.Name == "main" {
		if len(fn.ParamTypes) != 0 {
			return "", false, fmt.Errorf("入口函数 'main' 不允许携带参数")
		}
		ti := c.tt.Info(fn.ReturnType)
		if ti.Kind == tir.TypeInt && ti.Signed && ti.Bits == 32 && !ti.IsPtrSized {
			return "int main(void)", false, nil
		}
		if ti.Kind == tir.TypeUnit {
			return "static void won_user_main(void)", true, nil
		}
		return "", false, fmt.Errorf("不支持的 main 返回类型 '%s'", c.tt.Format(fn.ReturnType))
	}

	pNames := c.paramLocalsInOrder(fn)
	params := make([]string, 0, len(fn.ParamTypes))
	for i, pt := range fn.ParamTypes {
		pC, err := MapType(c.tt, pt, PosParam)
		if err != nil {
			return "", false, err
		}
		params = append(params, fmt.Sprintf("%s %s", pC, pNames[i]))
	}
	paramStr := "void"
	if len(params) > 0 {
		paramStr = strings.Join(params, ", ")
	}
	return fmt.Sprintf("%s %s(%s)", retC, fn.Name, paramStr), false, nil
}

type fnEmitState struct {
	ctx             *emitCtx
	fn              *tir.Function
	localMap        map[tir.LocalID]tir.LocalDecl
	localNames      map[tir.LocalID]string
	sliceAliases    map[tir.LocalID]bool
	glueMap         map[tir.DropGlueID]tir.DropGlueDesc
	defInst         map[tir.ValueID]tir.Instruction
	valUses         map[tir.ValueID]int
	foldNegConst    map[tir.ValueID]*big.Int // OpUnary(- constInt) -> 折叠后的精确常量值
	skipConstVal    map[tir.ValueID]bool     // 被折叠消费的独立 OpConst，无需发射
	arrayRepeatVal  map[tir.ValueID]tir.Instruction
	staticStrVal    map[tir.ValueID]string
	hasStaticStrVal map[tir.ValueID]bool
	declaredVal     map[tir.ValueID]bool
	usedLocal       map[tir.LocalID]bool
	localRead       map[tir.LocalID]bool
	usedFlag        map[tir.DropFlagID]bool
	reachable       map[tir.BlockID]bool
	labelNeeded     map[tir.BlockID]bool
}

func (c *emitCtx) emitFunction(fn *tir.Function) error {
	sig, isUnitMain, err := c.buildFuncSignature(fn)
	if err != nil {
		return err
	}

	fs := &fnEmitState{
		ctx:             c,
		fn:              fn,
		localMap:        make(map[tir.LocalID]tir.LocalDecl, len(fn.Locals)),
		localNames:      make(map[tir.LocalID]string, len(fn.Locals)),
		sliceAliases:    collectSafeByteSliceAliases(c.tt, fn),
		glueMap:         make(map[tir.DropGlueID]tir.DropGlueDesc, len(c.prog.DropGlues)),
		defInst:         make(map[tir.ValueID]tir.Instruction, len(fn.Values)),
		valUses:         make(map[tir.ValueID]int, len(fn.Values)),
		foldNegConst:    make(map[tir.ValueID]*big.Int),
		skipConstVal:    make(map[tir.ValueID]bool),
		arrayRepeatVal:  make(map[tir.ValueID]tir.Instruction),
		staticStrVal:    make(map[tir.ValueID]string),
		hasStaticStrVal: make(map[tir.ValueID]bool),
		declaredVal:     make(map[tir.ValueID]bool, len(fn.Values)),
		usedLocal:       make(map[tir.LocalID]bool, len(fn.Locals)),
		localRead:       make(map[tir.LocalID]bool, len(fn.Locals)),
		usedFlag:        make(map[tir.DropFlagID]bool, len(fn.DropFlags)),
		reachable:       make(map[tir.BlockID]bool, len(fn.Blocks)),
		labelNeeded:     make(map[tir.BlockID]bool, len(fn.Blocks)),
	}
	for _, g := range c.prog.DropGlues {
		fs.glueMap[g.ID] = g
	}
	for _, ld := range fn.Locals {
		fs.localMap[ld.ID] = ld
		fs.localNames[ld.ID] = localCVarName(ld)
	}

	fs.analyzeCFGAndUses()

	c.writeLine(sig + " {")
	c.indent++

	// 1. 消除未引用形参的 -Werror=unused-parameter 告警
	for _, ld := range fn.Locals {
		if ld.IsParam && !fs.usedLocal[ld.ID] {
			c.writeLine(fmt.Sprintf("(void)%s;", fs.localNames[ld.ID]))
		}
	}

	// 2. 在函数入口作用域声明全部被读取或析构的局部存储槽位（跨 CFG goto 跳转安全驻留）
	for _, ld := range fn.Locals {
		if ld.IsParam || !fs.usedLocal[ld.ID] || !fs.localRead[ld.ID] {
			continue
		}
		if err := fs.emitLocalDecl(ld); err != nil {
			return err
		}
	}

	// 3. 声明所有条件 DropFlag（初始化为 TIR 元数据给定的确定性 Initial 值）
	for _, df := range fn.DropFlags {
		if !fs.usedFlag[df.ID] {
			continue
		}
		initStr := "false"
		if df.Initial {
			initStr = "true"
		}
		c.writeLine(fmt.Sprintf("bool %s = %s;", dropFlagCVarName(df.ID), initStr))
	}

	// 4. 声明所有非 unit / 非阵列表达式占位临时寄存器变量
	for _, vd := range fn.Values {
		if fs.skipConstVal[vd.ID] {
			continue
		}
		if _, isArrRep := fs.arrayRepeatVal[vd.ID]; isArrRep {
			continue
		}
		if c.tt.Info(vd.Type).Kind == tir.TypeUnit || isScalarArrayType(c.tt, vd.Type) {
			continue
		}
		// 检查是否在可达块内被实际读取（含 OpValueAssign 目标，防止产生 -Werror=unused-but-set-variable）
		if fs.valUses[vd.ID] == 0 {
			continue
		}
		var cTyp string
		if isU8SliceRef(c.tt, vd.Type) {
			cTyp = "const uint8_t*"
		} else if c.tt.Info(vd.Type).Kind == tir.TypeRef && isScalarArrayType(c.tt, c.tt.Info(vd.Type).Elem) {
			arrTi := c.tt.Info(c.tt.Info(vd.Type).Elem)
			elemC, err := MapType(c.tt, arrTi.Elem, PosValue)
			if err != nil {
				return err
			}
			if c.tt.Info(vd.Type).IsMut {
				cTyp = elemC + "*"
			} else {
				cTyp = "const " + elemC + "*"
			}
		} else {
			var err error
			cTyp, err = MapType(c.tt, vd.Type, PosValue)
			if err != nil {
				return err
			}
		}
		zeroInit, err := c.zeroInitExpr(vd.Type)
		if err != nil {
			return err
		}
		c.writeLine(fmt.Sprintf("%s %s = %s;", cTyp, valCTempName(vd.ID), zeroInit))
		fs.declaredVal[vd.ID] = true
	}

	// 5. 按 EntryBlock 优先的确定性顺序发射所有可达基本块
	orderedBlocks := fs.orderedReachableBlocks()
	for _, b := range orderedBlocks {
		if fs.labelNeeded[b.ID] {
			c.indent--
			c.writeLine(fmt.Sprintf("won_bb_%d:;", b.ID))
			c.indent++
		}
		for _, inst := range b.Instructions {
			if err := fs.emitInstruction(inst); err != nil {
				return err
			}
		}
		if err := fs.emitTerminator(b.Terminator); err != nil {
			return err
		}
	}

	c.indent--
	c.writeLine("}")
	c.writeLine("")

	if isUnitMain {
		c.writeLine("int main(void) {")
		c.indent++
		c.writeLine("won_user_main();")
		c.writeLine("return 0;")
		c.indent--
		c.writeLine("}")
		c.writeLine("")
	}
	return nil
}

func (c *emitCtx) zeroInitExpr(tid tir.TypeID) (string, error) {
	ti := c.tt.Info(tid)
	switch ti.Kind {
	case tir.TypeBool:
		return "false", nil
	case tir.TypeChar, tir.TypeInt:
		return "0", nil
	case tir.TypeFloat:
		if ti.Bits == 32 {
			return "0.0f", nil
		}
		return "0.0", nil
	case tir.TypeRawPtr:
		return "NULL", nil
	case tir.TypeRef:
		return "NULL", nil
	case tir.TypeStruct:
		if ti.IsOpaqueForeign {
			return "{0}", nil
		}
	}
	return "{0}", nil
}

func (fs *fnEmitState) emitLocalDecl(ld tir.LocalDecl) error {
	c := fs.ctx
	name := fs.localNames[ld.ID]
	if fs.sliceAliases[ld.ID] {
		c.writeLine(fmt.Sprintf("const uint8_t* %s = NULL;", name))
		return nil
	}
	if isScalarArrayType(c.tt, ld.Type) {
		arrInfo := c.tt.Info(ld.Type)
		elemC, err := MapType(c.tt, arrInfo.Elem, PosValue)
		if err != nil {
			return err
		}
		c.writeLine(fmt.Sprintf("%s %s[%d] = {0};", elemC, name, arrInfo.ArrayLen))
		return nil
	}
	cTyp, err := MapType(c.tt, ld.Type, PosLocal)
	if err != nil {
		return err
	}
	zeroInit, err := c.zeroInitExpr(ld.Type)
	if err != nil {
		return err
	}
	c.writeLine(fmt.Sprintf("%s %s = %s;", cTyp, name, zeroInit))
	return nil
}

func (fs *fnEmitState) analyzeCFGAndUses() {
	// BFS 遍历确保仅处理从 EntryBlock 可达的基本块，避免向 dead exit 块发射无用标签或错误返回
	q := []tir.BlockID{fs.fn.EntryBlock}
	fs.reachable[fs.fn.EntryBlock] = true
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		b, ok := fs.fn.BlockByID(cur)
		if !ok || b == nil {
			continue
		}
		var targets []tir.BlockID
		switch b.Terminator.Kind {
		case tir.TermGoto:
			targets = []tir.BlockID{b.Terminator.Target}
		case tir.TermBranchIf:
			targets = []tir.BlockID{b.Terminator.ThenBlock, b.Terminator.ElseBlock}
		case tir.TermSwitch:
			for _, sc := range b.Terminator.Cases {
				targets = append(targets, sc.Target)
			}
			if b.Terminator.DefaultBlk != tir.InvalidBlock {
				targets = append(targets, b.Terminator.DefaultBlk)
			}
		}
		for _, tgt := range targets {
			if tgt == tir.InvalidBlock {
				continue
			}
			fs.labelNeeded[tgt] = true
			if !fs.reachable[tgt] {
				fs.reachable[tgt] = true
				q = append(q, tgt)
			}
		}
	}

	markOperand := func(op tir.Operand) {
		switch op.Kind {
		case tir.OperandValue:
			fs.valUses[op.Value]++
		case tir.OperandPlace:
			if op.Place.Local != tir.InvalidLocal {
				fs.usedLocal[op.Place.Local] = true
				fs.localRead[op.Place.Local] = true
			}
		}
	}

	// Pass 1: 收集可达块内全部单定义寄存器、静态串与重复数组描述
	for _, b := range fs.fn.Blocks {
		if b == nil || !fs.reachable[b.ID] {
			continue
		}
		for _, inst := range b.Instructions {
			if inst.Dest != tir.InvalidValue && inst.Kind != tir.OpValueAssign {
				fs.defInst[inst.Dest] = inst
			}
			if inst.Local != tir.InvalidLocal {
				fs.usedLocal[inst.Local] = true
			}
			if inst.Place.Local != tir.InvalidLocal {
				fs.usedLocal[inst.Place.Local] = true
			}
			if inst.FlagID != tir.InvalidDropFlag {
				fs.usedFlag[inst.FlagID] = true
			}
			switch inst.Kind {
			case tir.OpStaticRef:
				fs.staticStrVal[inst.Dest] = inst.Const.StrVal
				fs.hasStaticStrVal[inst.Dest] = true
			case tir.OpAggregateConstruct:
				if inst.AggKind == tir.AggArrayRepeat {
					fs.arrayRepeatVal[inst.Dest] = inst
				}
			}
		}
	}

	// Pass 2: 统计被真正读取的值与局部槽位（跳过内联 println 字面串及 memset 零数组常量元素）
	for _, b := range fs.fn.Blocks {
		if b == nil || !fs.reachable[b.ID] {
			continue
		}
		for _, inst := range b.Instructions {
			switch inst.Kind {
			case tir.OpCopy, tir.OpMove, tir.OpLoad, tir.OpBorrow, tir.OpDrop, tir.OpCondDrop, tir.OpOwnershipTransfer:
				if inst.Place.Local != tir.InvalidLocal {
					fs.localRead[inst.Place.Local] = true
				}
			case tir.OpUnary, tir.OpCast, tir.OpInit, tir.OpAssign, tir.OpStore, tir.OpExtract, tir.OpValueAssign:
				if len(inst.Place.Projections) > 0 && inst.Place.Local != tir.InvalidLocal {
					fs.localRead[inst.Place.Local] = true
				}
				markOperand(inst.Src)
			case tir.OpBinary:
				markOperand(inst.Left)
				markOperand(inst.Right)
			case tir.OpAggregateConstruct:
				if inst.AggKind == tir.AggArrayRepeat && len(inst.AggItems) == 1 && fs.isZeroConstantOperand(inst.AggItems[0].Value) {
					continue
				}
				for _, it := range inst.AggItems {
					markOperand(it.Value)
				}
			case tir.OpCall, tir.OpForeignCall:
				for _, a := range inst.Args {
					markOperand(a)
				}
				for _, ap := range inst.ArgPlaces {
					if ap.Local != tir.InvalidLocal {
						fs.usedLocal[ap.Local] = true
						fs.localRead[ap.Local] = true
					}
				}
			case tir.OpBuiltinCall:
				for _, a := range inst.Args {
					if inst.BuiltinKind == tir.BuiltinPrintln && a.Kind == tir.OperandValue && fs.hasStaticStrVal[a.Value] {
						continue
					}
					markOperand(a)
				}
				for _, ap := range inst.ArgPlaces {
					if ap.Local != tir.InvalidLocal {
						fs.usedLocal[ap.Local] = true
						fs.localRead[ap.Local] = true
					}
				}
			}
		}
		term := b.Terminator
		switch term.Kind {
		case tir.TermBranchIf:
			markOperand(term.Cond)
		case tir.TermSwitch:
			markOperand(term.Cond)
		case tir.TermReturn:
			if term.ReturnVal != nil {
				markOperand(*term.ReturnVal)
			}
		}
	}

	// 检测 `OpConst(int)` 紧跟单次使用 `OpUnary(UnaryNeg)` 的场景，直接折叠为符号极值安全常量 (如 INT64_MIN)
	for _, b := range fs.fn.Blocks {
		if b == nil || !fs.reachable[b.ID] {
			continue
		}
		for _, inst := range b.Instructions {
			if inst.Kind != tir.OpUnary || inst.UnaryOp != tir.UnaryNeg || inst.Src.Kind != tir.OperandValue {
				continue
			}
			srcID := inst.Src.Value
			if fs.valUses[srcID] != 1 {
				continue
			}
			srcDef, ok := fs.defInst[srcID]
			if !ok || srcDef.Kind != tir.OpConst || srcDef.Const.Kind != tir.ConstInt {
				continue
			}
			if val, okParse := new(big.Int).SetString(srcDef.Const.IntText, 10); okParse {
				negVal := new(big.Int).Neg(val)
				if _, fit := fs.formatIntLiteral(negVal, inst.Type); fit == nil {
					fs.foldNegConst[inst.Dest] = negVal
					fs.skipConstVal[srcID] = true
				}
			}
		}
	}
}

func (fs *fnEmitState) orderedReachableBlocks() []*tir.BasicBlock {
	out := make([]*tir.BasicBlock, 0, len(fs.fn.Blocks))
	if eb, ok := fs.fn.BlockByID(fs.fn.EntryBlock); ok && eb != nil && fs.reachable[eb.ID] {
		out = append(out, eb)
	}
	for _, b := range fs.fn.Blocks {
		if b == nil || b.ID == fs.fn.EntryBlock || !fs.reachable[b.ID] {
			continue
		}
		out = append(out, b)
	}
	return out
}

func (fs *fnEmitState) emitInstruction(inst tir.Instruction) error {
	c := fs.ctx

	switch inst.Kind {
	case tir.OpStorageLive, tir.OpStorageDead, tir.OpScopeEnter, tir.OpScopeExit:
		return nil

	case tir.OpDropFlagDecl:
		if fs.usedFlag[inst.FlagID] {
			val := "false"
			if inst.FlagBool {
				val = "true"
			}
			c.writeLine(fmt.Sprintf("%s = %s;", dropFlagCVarName(inst.FlagID), val))
		}
		return nil

	case tir.OpDropFlagSet:
		if fs.usedFlag[inst.FlagID] {
			val := "false"
			if inst.FlagBool {
				val = "true"
			}
			c.writeLine(fmt.Sprintf("%s = %s;", dropFlagCVarName(inst.FlagID), val))
		}
		return nil

	case tir.OpDropFlagCheck:
		if fs.declaredVal[inst.Dest] {
			c.writeLine(fmt.Sprintf("%s = %s;", valCTempName(inst.Dest), dropFlagCVarName(inst.FlagID)))
		}
		return nil

	case tir.OpConst:
		if fs.skipConstVal[inst.Dest] || !fs.declaredVal[inst.Dest] {
			return nil
		}
		expr, err := fs.constCExpr(inst.Const, inst.Type)
		if err != nil {
			return err
		}
		c.writeLine(fmt.Sprintf("%s = %s;", valCTempName(inst.Dest), expr))
		return nil

	case tir.OpStaticRef:
		if !fs.declaredVal[inst.Dest] {
			return nil
		}
		c.writeLine(fmt.Sprintf("%s = %s;", valCTempName(inst.Dest), quoteCString(inst.Const.StrVal)))
		return nil

	case tir.OpUnary:
		if !fs.declaredVal[inst.Dest] {
			return nil
		}
		if folded, ok := fs.foldNegConst[inst.Dest]; ok {
			expr, err := fs.formatIntLiteral(folded, inst.Type)
			if err != nil {
				return err
			}
			c.writeLine(fmt.Sprintf("%s = %s;", valCTempName(inst.Dest), expr))
			return nil
		}
		src, err := fs.operandCExpr(inst.Src)
		if err != nil {
			return err
		}
		op := "-"
		switch inst.UnaryOp {
		case tir.UnaryNot:
			op = "!"
		case tir.UnaryBitNot:
			op = "~"
		}
		c.writeLine(fmt.Sprintf("%s = (%s%s);", valCTempName(inst.Dest), op, src))
		return nil

	case tir.OpBinary:
		if !fs.declaredVal[inst.Dest] {
			return nil
		}
		left, err := fs.operandCExpr(inst.Left)
		if err != nil {
			return err
		}
		right, err := fs.operandCExpr(inst.Right)
		if err != nil {
			return err
		}
		c.writeLine(fmt.Sprintf("%s = (%s %s %s);", valCTempName(inst.Dest), left, inst.BinaryOp.String(), right))
		return nil

	case tir.OpCast:
		if !fs.declaredVal[inst.Dest] {
			return nil
		}
		src, err := fs.operandCExpr(inst.Src)
		if err != nil {
			return err
		}
		targetC, err := MapType(c.tt, inst.Type, PosValue)
		if err != nil {
			return err
		}
		c.writeLine(fmt.Sprintf("%s = ((%s)(%s));", valCTempName(inst.Dest), targetC, src))
		return nil

	case tir.OpValueAssign:
		if !fs.declaredVal[inst.Dest] {
			return nil
		}
		src, err := fs.operandCExpr(inst.Src)
		if err != nil {
			return err
		}
		c.writeLine(fmt.Sprintf("%s = %s;", valCTempName(inst.Dest), src))
		return nil

	case tir.OpAggregateConstruct:
		// AggArrayRepeat 由后续 OpInit / OpAssign 在目标栈数组槽位直接展开填充
		if inst.AggKind == tir.AggArrayRepeat {
			return nil
		}
		return newCapErr("WON-GEN-002", fmt.Sprintf("不支持的聚合构造 %s", inst.AggKind), inst.Span, "")

	case tir.OpCopy, tir.OpMove, tir.OpLoad:
		if inst.Dest == tir.InvalidValue || !fs.declaredVal[inst.Dest] {
			return nil
		}
		pExpr := fs.placeCExpr(inst.Place)
		c.writeLine(fmt.Sprintf("%s = %s;", valCTempName(inst.Dest), pExpr))
		return nil

	case tir.OpInit, tir.OpAssign, tir.OpStore:
		if isScalarArrayType(c.tt, inst.Type) {
			if len(inst.Place.Projections) == 0 && !fs.localRead[inst.Place.Local] {
				return nil
			}
			return fs.emitArrayInit(inst)
		}
		if c.tt.Info(inst.Type).Kind == tir.TypeUnit || inst.Src.Kind == tir.OperandInvalid {
			return nil
		}
		src, err := fs.operandCExpr(inst.Src)
		if err != nil {
			return err
		}
		if len(inst.Place.Projections) == 0 && !fs.localMap[inst.Place.Local].IsParam && !fs.localRead[inst.Place.Local] {
			c.writeLine(fmt.Sprintf("(void)(%s);", src))
			return nil
		}
		pExpr := fs.placeCExpr(inst.Place)
		c.writeLine(fmt.Sprintf("%s = %s;", pExpr, src))
		return nil

	case tir.OpBorrow:
		if !fs.declaredVal[inst.Dest] {
			return nil
		}
		p := inst.Place
		name := fs.localNames[p.Local]
		// &[u8] .as_bytes() 别名在 C 侧直接表示为 const uint8_t*；&str 重借用亦保持 const char*
		if len(p.Projections) == 1 && p.Projections[0].Kind == tir.ProjDeref &&
			(fs.sliceAliases[p.Local] || (c.tt.Info(p.RootType).Kind == tir.TypeRef && c.tt.Info(c.tt.Info(p.RootType).Elem).Kind == tir.TypeStr)) {
			c.writeLine(fmt.Sprintf("%s = %s;", valCTempName(inst.Dest), name))
			return nil
		}
		// 栈定长数组名在 C 中自然退化为首元素指针，严禁对数组名取 `&arr` 产生 `T (*)[N]` 类型不匹配
		if len(p.Projections) == 0 && isScalarArrayType(c.tt, p.ElemType) {
			c.writeLine(fmt.Sprintf("%s = %s;", valCTempName(inst.Dest), name))
			return nil
		}
		c.writeLine(fmt.Sprintf("%s = (&(%s));", valCTempName(inst.Dest), fs.placeCExpr(p)))
		return nil

	case tir.OpExtract:
		if !fs.declaredVal[inst.Dest] {
			return nil
		}
		src, err := fs.operandCExpr(inst.Src)
		if err != nil {
			return err
		}
		if inst.ExtractProj.Kind == tir.ProjDeref {
			c.writeLine(fmt.Sprintf("%s = (*(%s));", valCTempName(inst.Dest), src))
			return nil
		}
		return newCapErr("WON-GEN-002", fmt.Sprintf("成员抽取 (%v) 尚未实现", inst.ExtractProj.Kind), inst.Span, "")

	case tir.OpCall:
		return fs.emitCall(inst, inst.CalleeName)

	case tir.OpForeignCall:
		return fs.emitForeignCall(inst)

	case tir.OpBuiltinCall:
		return fs.emitBuiltinCall(inst)

	case tir.OpOwnershipTransfer:
		// 若 @owned 移交承载原本的表达式寄存器目标，搬运句柄值供紧随的 OpForeignCall 使用；标志清理由 Drop 展开显式保证
		if inst.Dest != tir.InvalidValue && fs.declaredVal[inst.Dest] {
			c.writeLine(fmt.Sprintf("%s = %s;", valCTempName(inst.Dest), fs.placeCExpr(inst.Place)))
		}
		return nil

	case tir.OpDrop:
		return fs.emitDrop(inst)

	case tir.OpCondDrop:
		return fs.emitCondDrop(inst)

	default:
		return newCapErr("WON-GEN-002", fmt.Sprintf("不支持的 TIR 指令 '%s'", inst.Kind), inst.Span, "")
	}
}

func (fs *fnEmitState) emitArrayInit(inst tir.Instruction) error {
	c := fs.ctx
	agg, ok := fs.arrayRepeatVal[inst.Src.Value]
	if !ok || len(agg.AggItems) != 1 {
		return newCapErr("WON-GEN-002", "定长数组初始化缺少重复元素操作数", inst.Span, "")
	}
	targetName := fs.placeCExpr(inst.Place)
	elemOp := agg.AggItems[0].Value
	if fs.isZeroConstantOperand(elemOp) {
		c.writeLine(fmt.Sprintf("memset(%s, 0, sizeof(%s));", targetName, targetName))
		return nil
	}
	elemExpr, err := fs.operandCExpr(elemOp)
	if err != nil {
		return err
	}
	c.writeLine(fmt.Sprintf("for (size_t _won_i = 0; _won_i < ((size_t)UINT64_C(%d)); ++_won_i) { %s[_won_i] = %s; }", agg.RepeatLen, targetName, elemExpr))
	return nil
}

func (fs *fnEmitState) isZeroConstantOperand(op tir.Operand) bool {
	if op.Kind == tir.OperandConst {
		return isZeroConstant(op.Const)
	}
	if op.Kind == tir.OperandValue {
		if def, ok := fs.defInst[op.Value]; ok && def.Kind == tir.OpConst {
			return isZeroConstant(def.Const)
		}
	}
	return false
}

func isZeroConstant(cnst tir.Constant) bool {
	switch cnst.Kind {
	case tir.ConstBool:
		return !cnst.BoolVal
	case tir.ConstInt:
		v, ok := new(big.Int).SetString(cnst.IntText, 10)
		return ok && v.Sign() == 0
	case tir.ConstChar:
		return cnst.RuneVal == 0
	case tir.ConstByte:
		return cnst.ByteVal == 0
	default:
		return false
	}
}

func (fs *fnEmitState) emitCall(inst tir.Instruction, fnName string) error {
	c := fs.ctx
	args := make([]string, len(inst.Args))
	for i, a := range inst.Args {
		s, err := fs.operandCExpr(a)
		if err != nil {
			return err
		}
		args[i] = s
	}
	callExpr := fmt.Sprintf("%s(%s)", fnName, strings.Join(args, ", "))
	if inst.Dest != tir.InvalidValue && fs.declaredVal[inst.Dest] && c.tt.Info(inst.Type).Kind != tir.TypeUnit {
		c.writeLine(fmt.Sprintf("%s = %s;", valCTempName(inst.Dest), callExpr))
	} else {
		c.writeLine(callExpr + ";")
	}
	return nil
}

func (fs *fnEmitState) emitForeignCall(inst tir.Instruction) error {
	c := fs.ctx
	var contract *tir.FFIContractDesc
	if inst.HasContract {
		if fc, ok := c.prog.ContractByID(inst.ContractID); ok {
			contract = &fc
		}
	}

	args := make([]string, len(inst.Args))
	for i, a := range inst.Args {
		s, err := fs.operandCExpr(a)
		if err != nil {
			return err
		}
		// 若 drop/ffi.go 为 @borrow 参数合成了 OpBorrow 间接引用层而 C 契约按句柄/指针原值接收，自动解引用还原 C ABI 实参类型
		if fs.shouldDerefSyntheticBorrow(inst, contract, i, a) {
			s = fmt.Sprintf("(*(%s))", s)
		}
		args[i] = s
	}
	callExpr := fmt.Sprintf("%s(%s)", inst.CalleeName, strings.Join(args, ", "))
	if inst.Dest != tir.InvalidValue && fs.declaredVal[inst.Dest] && c.tt.Info(inst.Type).Kind != tir.TypeUnit {
		c.writeLine(fmt.Sprintf("%s = %s;", valCTempName(inst.Dest), callExpr))
	} else {
		c.writeLine(callExpr + ";")
	}
	return nil
}

func (fs *fnEmitState) shouldDerefSyntheticBorrow(inst tir.Instruction, contract *tir.FFIContractDesc, idx int, arg tir.Operand) bool {
	if arg.Kind != tir.OperandValue || idx >= len(inst.ArgPlaces) || !inst.ArgPlaces[idx].IsValid() {
		return false
	}
	def, ok := fs.defInst[arg.Value]
	if !ok || def.Kind != tir.OpBorrow {
		return false
	}
	c := fs.ctx
	argTi := c.tt.Info(arg.Type)
	placeType := inst.ArgPlaces[idx].ElemType
	if argTi.Kind != tir.TypeRef || argTi.Elem != placeType {
		return false
	}
	own := tir.FFIUnknown
	if idx < len(inst.ArgOwnership) {
		own = inst.ArgOwnership[idx]
	}
	cType := ""
	if contract != nil {
		for _, p := range contract.Params {
			if p.ParamIndex == idx {
				cType = p.CType
				if own == tir.FFIUnknown {
					own = p.Ownership
				}
				break
			}
		}
	}
	if own != tir.FFIBorrowShared && own != tir.FFIBorrowMut {
		return false
	}
	placePtrDepth := tirPtrDepth(c.tt, placeType)
	cPtrDepth := countCPtrStars(cType)
	return cType != "" && cPtrDepth == placePtrDepth
}

func tirPtrDepth(tt *tir.TypeTable, tid tir.TypeID) int {
	depth := 0
	cur := tid
	for {
		ti := tt.Info(cur)
		if ti.Kind == tir.TypeRawPtr || ti.Kind == tir.TypeRef {
			depth++
			cur = ti.Elem
			continue
		}
		break
	}
	return depth
}

func countCPtrStars(cType string) int {
	n := 0
	for i := 0; i < len(cType); i++ {
		if cType[i] == '*' {
			n++
		}
	}
	return n
}

func (fs *fnEmitState) emitBuiltinCall(inst tir.Instruction) error {
	c := fs.ctx
	args := make([]string, len(inst.Args))
	for i, a := range inst.Args {
		s, err := fs.operandCExpr(a)
		if err != nil {
			return err
		}
		args[i] = s
	}

	var expr string
	switch inst.BuiltinKind {
	case tir.BuiltinPrintln:
		expr = fs.buildPrintlnExpr(inst.Args, args)
	case tir.BuiltinBytesEqual:
		if len(args) != 3 {
			return newCapErr("WON-GEN-002", fmt.Sprintf("内置 bytes_equal 期望 3 个实参，实际收到 %d 个", len(args)), inst.Span, "")
		}
		expr = fmt.Sprintf("won_bytes_equal(%s, %s, %s)", args[0], args[1], args[2])
	case tir.BuiltinAsBytes:
		expr = fmt.Sprintf("((const uint8_t*)(%s))", args[0])
	case tir.BuiltinAsPtr:
		argTi := c.tt.Info(inst.Args[0].Type)
		if argTi.Kind == tir.TypeRef && isScalarArrayType(c.tt, argTi.Elem) {
			elemC, err := MapType(c.tt, c.tt.Info(argTi.Elem).Elem, PosValue)
			if err != nil {
				return err
			}
			expr = fmt.Sprintf("((const %s*)(%s))", elemC, args[0])
		} else {
			expr = fmt.Sprintf("((const uint8_t*)(%s))", args[0])
		}
	case tir.BuiltinAsMutPtr:
		argTi := c.tt.Info(inst.Args[0].Type)
		elemC, err := MapType(c.tt, c.tt.Info(argTi.Elem).Elem, PosValue)
		if err != nil {
			return err
		}
		expr = fmt.Sprintf("((%s*)(%s))", elemC, args[0])
	case tir.BuiltinLen:
		argTi := c.tt.Info(inst.Args[0].Type)
		arrLen := c.tt.Info(argTi.Elem).ArrayLen
		expr = fmt.Sprintf("UINT64_C(%d)", arrLen)
	default:
		return newCapErr("WON-GEN-002", fmt.Sprintf("内置调用 '%s' 尚未在当前 C11 后端实现", inst.CalleeName), inst.Span, "")
	}

	if inst.Dest != tir.InvalidValue && fs.declaredVal[inst.Dest] && c.tt.Info(inst.Type).Kind != tir.TypeUnit {
		c.writeLine(fmt.Sprintf("%s = %s;", valCTempName(inst.Dest), expr))
	} else {
		c.writeLine(expr + ";")
	}
	return nil
}

func (fs *fnEmitState) buildPrintlnExpr(argOps []tir.Operand, args []string) string {
	if len(args) == 0 {
		return `printf("\n")`
	}
	c := fs.ctx
	var format strings.Builder
	passArgs := make([]string, 0, len(args))
	for i, argOp := range argOps {
		if argOp.Kind == tir.OperandConst && argOp.Const.Kind == tir.ConstString {
			format.WriteString(escapePrintfLiteral(argOp.Const.StrVal))
			continue
		}
		if argOp.Kind == tir.OperandValue && fs.hasStaticStrVal[argOp.Value] {
			format.WriteString(escapePrintfLiteral(fs.staticStrVal[argOp.Value]))
			continue
		}

		ti := c.tt.Info(argOp.Type)
		switch {
		case ti.Kind == tir.TypeRef && c.tt.Info(ti.Elem).Kind == tir.TypeStr:
			format.WriteString("%s")
			passArgs = append(passArgs, args[i])
		case ti.Kind == tir.TypeBool:
			format.WriteString("%s")
			passArgs = append(passArgs, fmt.Sprintf("((%s) ? \"true\" : \"false\")", args[i]))
		case ti.Kind == tir.TypeChar:
			format.WriteString("%u")
			passArgs = append(passArgs, fmt.Sprintf("(unsigned int)(%s)", args[i]))
		case ti.Kind == tir.TypeInt && !ti.Signed:
			format.WriteString("%llu")
			passArgs = append(passArgs, fmt.Sprintf("(unsigned long long)(%s)", args[i]))
		case ti.Kind == tir.TypeInt && ti.Signed:
			format.WriteString("%lld")
			passArgs = append(passArgs, fmt.Sprintf("(long long)(%s)", args[i]))
		case ti.Kind == tir.TypeFloat:
			format.WriteString("%g")
			passArgs = append(passArgs, fmt.Sprintf("(double)(%s)", args[i]))
		case ti.Kind == tir.TypeRawPtr || ti.Kind == tir.TypeRef:
			format.WriteString("%p")
			passArgs = append(passArgs, fmt.Sprintf("(const void*)(%s)", args[i]))
		default:
			format.WriteString("%lld")
			passArgs = append(passArgs, fmt.Sprintf("(long long)(%s)", args[i]))
		}
	}
	format.WriteString("\\n")
	if len(passArgs) == 0 {
		return fmt.Sprintf(`printf("%s")`, format.String())
	}
	return fmt.Sprintf(`printf("%s", %s)`, format.String(), strings.Join(passArgs, ", "))
}

func (fs *fnEmitState) emitDrop(inst tir.Instruction) error {
	g, ok := fs.glueMap[inst.GlueID]
	if !ok || g.Kind != tir.GlueForeignResource {
		return newCapErr("WON-GEN-002", fmt.Sprintf("Drop 指令引用未知或不支持胶水 g%d", inst.GlueID), inst.Span, "")
	}
	_, releaseC, okSym := splitForeignSymbol(g.ReleaseSymbol)
	if !okSym {
		return newCapErr("WON-GEN-002", fmt.Sprintf("Drop 释放符号 '%s' 非法", g.ReleaseSymbol), inst.Span, "")
	}
	pExpr := fs.placeCExpr(inst.Place)
	fs.ctx.writeLine(fmt.Sprintf("%s(%s);", releaseC, pExpr))
	return nil
}

func (fs *fnEmitState) emitCondDrop(inst tir.Instruction) error {
	g, ok := fs.glueMap[inst.GlueID]
	if !ok || g.Kind != tir.GlueForeignResource {
		return newCapErr("WON-GEN-002", fmt.Sprintf("CondDrop 指令引用未知或不支持胶水 g%d", inst.GlueID), inst.Span, "")
	}
	_, releaseC, okSym := splitForeignSymbol(g.ReleaseSymbol)
	if !okSym {
		return newCapErr("WON-GEN-002", fmt.Sprintf("CondDrop 释放符号 '%s' 非法", g.ReleaseSymbol), inst.Span, "")
	}
	dfName := dropFlagCVarName(inst.FlagID)
	pExpr := fs.placeCExpr(inst.Place)
	c := fs.ctx
	c.writeLine(fmt.Sprintf("if (%s) {", dfName))
	c.indent++
	c.writeLine(fmt.Sprintf("%s(%s);", releaseC, pExpr))
	c.writeLine(fmt.Sprintf("%s = false;", dfName))
	c.indent--
	c.writeLine("}")
	return nil
}

func (fs *fnEmitState) emitTerminator(term tir.Terminator) error {
	c := fs.ctx
	switch term.Kind {
	case tir.TermGoto:
		c.writeLine(fmt.Sprintf("goto won_bb_%d;", term.Target))
		return nil

	case tir.TermBranchIf:
		condStr, err := fs.operandCExpr(term.Cond)
		if err != nil {
			return err
		}
		c.writeLine(fmt.Sprintf("if (%s) goto won_bb_%d;", condStr, term.ThenBlock))
		c.writeLine(fmt.Sprintf("goto won_bb_%d;", term.ElseBlock))
		return nil

	case tir.TermReturn:
		if c.tt.Info(fs.fn.ReturnType).Kind == tir.TypeUnit {
			c.writeLine("return;")
			return nil
		}
		if term.ReturnVal == nil {
			return newCapErr(
				"WON-INT-001",
				fmt.Sprintf("内部不变量违例：非 unit 函数 '%s' 终结器缺少 ReturnVal 操作数，拒绝合成默认返回值", fs.fn.Name),
				term.Span,
				"C11 后端严格遵循 Fail-Closed 原则，绝不为缺失返回操作数的非 unit 函数伪造默认返回值。",
			)
		}
		retStr, err := fs.operandCExpr(*term.ReturnVal)
		if err != nil {
			return err
		}
		c.writeLine(fmt.Sprintf("return %s;", retStr))
		return nil

	case tir.TermUnreachable:
		c.writeLine("abort();")
		return nil

	default:
		return newCapErr("WON-GEN-002", fmt.Sprintf("不支持的基本块终结器 (%d)", term.Kind), term.Span, "")
	}
}

func (fs *fnEmitState) placeCExpr(p tir.Place) string {
	expr := fs.localNames[p.Local]
	for _, proj := range p.Projections {
		if proj.Kind == tir.ProjDeref {
			expr = fmt.Sprintf("(*(%s))", expr)
		}
	}
	return expr
}

func (fs *fnEmitState) operandCExpr(op tir.Operand) (string, error) {
	switch op.Kind {
	case tir.OperandValue:
		return valCTempName(op.Value), nil
	case tir.OperandConst:
		return fs.constCExpr(op.Const, op.Type)
	case tir.OperandPlace:
		return fs.placeCExpr(op.Place), nil
	default:
		return "", fmt.Errorf("不支持的 TIR 操作数类型 (%d)", op.Kind)
	}
}

func (fs *fnEmitState) constCExpr(cnst tir.Constant, tid tir.TypeID) (string, error) {
	c := fs.ctx
	switch cnst.Kind {
	case tir.ConstBool:
		if cnst.BoolVal {
			return "true", nil
		}
		return "false", nil
	case tir.ConstInt:
		val, ok := new(big.Int).SetString(cnst.IntText, 10)
		if !ok {
			return "", fmt.Errorf("非法的 TIR 整型常量 %q", cnst.IntText)
		}
		return fs.formatIntLiteral(val, tid)
	case tir.ConstFloat:
		raw := strings.ReplaceAll(cnst.FloatRaw, "_", "")
		if raw == "" {
			raw = fmt.Sprintf("%g", cnst.FloatVal)
		}
		raw = strings.TrimSuffix(strings.TrimSuffix(raw, "f32"), "f64")
		if c.tt.Info(tid).Kind == tir.TypeFloat && c.tt.Info(tid).Bits == 32 {
			return raw + "f", nil
		}
		return raw, nil
	case tir.ConstChar:
		return fmt.Sprintf("((uint32_t)UINT32_C(%d))", uint32(cnst.RuneVal)), nil
	case tir.ConstByte:
		return fmt.Sprintf("((uint8_t)UINT8_C(%d))", cnst.ByteVal), nil
	case tir.ConstString:
		return quoteCString(cnst.StrVal), nil
	default:
		return "", fmt.Errorf("不支持的 TIR 常量类型 %v", cnst.Kind)
	}
}

// formatIntLiteral 根据具体整数宽度与符号生成严格 ISO C11 无警告、零 UB 常量表达式。
func (fs *fnEmitState) formatIntLiteral(val *big.Int, tid tir.TypeID) (string, error) {
	if val == nil {
		return "", fmt.Errorf("整数字面量值为空")
	}
	ti := fs.ctx.tt.Info(tid)
	if ti.Kind != tir.TypeInt {
		return "", fmt.Errorf("整型常量目标类型 '%s' 非整数", fs.ctx.tt.Format(tid))
	}

	var minVal, maxVal *big.Int
	if ti.IsPtrSized {
		if ti.Signed {
			minVal = big.NewInt(math.MinInt64)
			maxVal = big.NewInt(math.MaxInt64)
		} else {
			minVal = big.NewInt(0)
			maxVal = new(big.Int).SetUint64(math.MaxUint64)
		}
	} else {
		switch ti.Bits {
		case 8:
			if ti.Signed {
				minVal = big.NewInt(math.MinInt8)
				maxVal = big.NewInt(math.MaxInt8)
			} else {
				minVal = big.NewInt(0)
				maxVal = big.NewInt(math.MaxUint8)
			}
		case 16:
			if ti.Signed {
				minVal = big.NewInt(math.MinInt16)
				maxVal = big.NewInt(math.MaxInt16)
			} else {
				minVal = big.NewInt(0)
				maxVal = big.NewInt(math.MaxUint16)
			}
		case 32:
			if ti.Signed {
				minVal = big.NewInt(math.MinInt32)
				maxVal = big.NewInt(math.MaxInt32)
			} else {
				minVal = big.NewInt(0)
				maxVal = big.NewInt(math.MaxUint32)
			}
		case 64:
			if ti.Signed {
				minVal = big.NewInt(math.MinInt64)
				maxVal = big.NewInt(math.MaxInt64)
			} else {
				minVal = big.NewInt(0)
				maxVal = new(big.Int).SetUint64(math.MaxUint64)
			}
		default:
			return "", fmt.Errorf("%d 位整型常量在严格 ISO C11 中不受支持", ti.Bits)
		}
	}

	if val.Cmp(minVal) < 0 || val.Cmp(maxVal) > 0 {
		return "", fmt.Errorf("整型常量 %s 超出类型 '%s' 取值范围", val.String(), fs.ctx.tt.Format(tid))
	}

	if ti.IsPtrSized {
		if ti.Signed {
			if val.Int64() == math.MinInt64 {
				return "PTRDIFF_MIN", nil
			}
			if val.Sign() < 0 {
				return fmt.Sprintf("((ptrdiff_t)(-INT64_C(%s)))", new(big.Int).Neg(val).String()), nil
			}
			return fmt.Sprintf("((ptrdiff_t)INT64_C(%s))", val.String()), nil
		}
		return fmt.Sprintf("((size_t)UINT64_C(%s))", val.String()), nil
	}

	switch ti.Bits {
	case 8:
		if ti.Signed {
			if val.Int64() == math.MinInt8 {
				return "INT8_MIN", nil
			}
			if val.Sign() < 0 {
				return fmt.Sprintf("((int8_t)(-INT8_C(%s)))", new(big.Int).Neg(val).String()), nil
			}
			return fmt.Sprintf("((int8_t)INT8_C(%s))", val.String()), nil
		}
		return fmt.Sprintf("((uint8_t)UINT8_C(%s))", val.String()), nil

	case 16:
		if ti.Signed {
			if val.Int64() == math.MinInt16 {
				return "INT16_MIN", nil
			}
			if val.Sign() < 0 {
				return fmt.Sprintf("((int16_t)(-INT16_C(%s)))", new(big.Int).Neg(val).String()), nil
			}
			return fmt.Sprintf("((int16_t)INT16_C(%s))", val.String()), nil
		}
		return fmt.Sprintf("((uint16_t)UINT16_C(%s))", val.String()), nil

	case 32:
		if ti.Signed {
			if val.Int64() == math.MinInt32 {
				return "INT32_MIN", nil
			}
			if val.Sign() < 0 {
				return fmt.Sprintf("(-INT32_C(%s))", new(big.Int).Neg(val).String()), nil
			}
			return fmt.Sprintf("INT32_C(%s)", val.String()), nil
		}
		return fmt.Sprintf("UINT32_C(%s)", val.String()), nil

	case 64:
		if ti.Signed {
			if val.Int64() == math.MinInt64 {
				return "INT64_MIN", nil
			}
			if val.Sign() < 0 {
				return fmt.Sprintf("(-INT64_C(%s))", new(big.Int).Neg(val).String()), nil
			}
			return fmt.Sprintf("INT64_C(%s)", val.String()), nil
		}
		return fmt.Sprintf("UINT64_C(%s)", val.String()), nil
	}
	return "", fmt.Errorf("未知整型位宽 %d", ti.Bits)
}

func quoteCString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	writeEscapedCBytes(&sb, s, false)
	sb.WriteByte('"')
	return sb.String()
}

func escapePrintfLiteral(s string) string {
	var sb strings.Builder
	writeEscapedCBytes(&sb, s, true)
	return sb.String()
}

// writeEscapedCBytes 使用严格 3 位八进制转义处理 NUL/控制字符，杜绝 ISO C11 `\x00a` 贪婪十六进制读取缺陷。
func writeEscapedCBytes(sb *strings.Builder, s string, escapePercent bool) {
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch b {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		case '%':
			if escapePercent {
				sb.WriteString("%%")
			} else {
				sb.WriteByte('%')
			}
		default:
			if b >= 0x20 && b <= 0x7e {
				sb.WriteByte(b)
			} else {
				fmt.Fprintf(sb, `\%03o`, b)
			}
		}
	}
}

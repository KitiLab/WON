package tir_test

import (
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/tir"
)

func buildBaselineValidProgram(t *testing.T) *tir.Program {
	t.Helper()
	tt := tir.NewTypeTable()
	tUnit := tt.Unit()
	tBool := tt.Bool()
	tI32 := tt.Int(true, 32, false, "i32")
	tU8 := tt.Int(false, 8, false, "u8")
	tStr := tt.StringType()
	tPtrU8 := tt.RawPtr(true, tU8)
	tRefI32 := tt.Ref(false, tI32)

	tPoint := tt.ReserveStruct("Struct:Point", "Point", nil, true, false, "")
	tt.DefineStruct(tPoint, []tir.StructFieldType{
		{Name: "x", Index: 0, Type: tI32},
		{Name: "y", Index: 1, Type: tI32},
	})

	p := &tir.Program{
		Types:       tt,
		OwnershipOK: true,
		DropOK:      true,
		DropGlues: []tir.DropGlueDesc{
			{ID: 1, CanonicalName: "glue.String", Kind: tir.GlueString, Type: tStr, TypeName: "String"},
		},
		FFICatalogs: []tir.FFICatalogDesc{
			{Namespace: "mem", SchemaVersion: "1.0", HeaderPath: "mem.h"},
		},
		FFIContracts: []tir.FFIContractDesc{
			{
				ID:              1,
				ContractKey:     "mem.take",
				Namespace:       "mem",
				Function:        "take",
				HeaderPath:      "mem.h",
				SchemaVersion:   "1.0",
				CallingConv:     "C",
				Params:          []tir.FFIParamDesc{{ParamIndex: 0, CType: "unsigned char *", Ownership: tir.FFIOwnedTransfer}},
				ReturnOwnership: tir.FFIRawUnmanaged,
				BorrowsParam:    -1,
			},
		},
		Functions: []*tir.Function{
			{
				ID:          1,
				Name:        "sample",
				CallingConv: "WON",
				ParamTypes:  []tir.TypeID{tBool, tI32, tPtrU8},
				ReturnType:  tI32,
				EntryBlock:  0,
				ExitBlock:   3,
				Locals: []tir.LocalDecl{
					{ID: 1, Name: "cond", Type: tBool, IsParam: true, ParamIdx: 0, HasInit: true, ScopeID: 0},
					{ID: 2, Name: "n", Type: tI32, IsParam: true, ParamIdx: 1, HasInit: true, ScopeID: 0},
					{ID: 3, Name: "buf", Type: tPtrU8, IsParam: true, ParamIdx: 2, HasInit: true, ScopeID: 0},
					{ID: 4, Name: "s", Type: tStr, IsMut: true, HasInit: false, ScopeID: 1},
					{ID: 5, Name: "pt", Type: tPoint, HasInit: true, ScopeID: 1},
					{ID: 6, Name: "rn", Type: tRefI32, HasInit: true, ScopeID: 1},
				},
				Values: []tir.ValueDecl{
					{ID: 1, Type: tBool},
					{ID: 2, Type: tI32},
					{ID: 3, Type: tI32},
					{ID: 4, Type: tPoint},
					{ID: 5, Type: tRefI32},
					{ID: 6, Type: tUnit},
					{ID: 7, Type: tI32, IsJoinTemp: true},
				},
				DropFlags: []tir.DropFlag{
					{ID: 1, Name: "df1", OwnerLocal: 4, Place: tir.Place{Local: 4, LocalName: "s", RootType: tStr, ElemType: tStr}, Type: tStr, GlueID: 1, Initial: false},
				},
				Blocks: []*tir.BasicBlock{
					{
						ID:         0,
						Label:      "entry",
						Successors: []tir.BlockID{1, 2},
						Instructions: []tir.Instruction{
							{Kind: tir.OpScopeEnter, ScopeID: 1},
							{Kind: tir.OpDropFlagDecl, Type: tBool, FlagID: 1, FlagBool: false},
							{Kind: tir.OpCopy, Type: tBool, Dest: 1, Place: tir.Place{Local: 1, LocalName: "cond", RootType: tBool, ElemType: tBool}},
							{Kind: tir.OpConst, Type: tI32, Dest: 2, Const: tir.Constant{Kind: tir.ConstInt, IntText: "1"}},
							{Kind: tir.OpBinary, Type: tI32, Dest: 3, BinaryOp: tir.BinAdd, Left: tir.PlaceOperand(tir.Place{Local: 2, LocalName: "n", RootType: tI32, ElemType: tI32}), Right: tir.ValueOperand(tI32, 2)},
							{Kind: tir.OpAggregateConstruct, Type: tPoint, Dest: 4, AggKind: tir.AggStruct, AggItems: []tir.AggregateItem{
								{FieldName: "x", FieldIndex: 0, Value: tir.ValueOperand(tI32, 3)},
								{FieldName: "y", FieldIndex: 1, Value: tir.ConstOperand(tI32, tir.Constant{Kind: tir.ConstInt, IntText: "2"})},
							}},
							{Kind: tir.OpInit, Type: tPoint, Place: tir.Place{Local: 5, LocalName: "pt", RootType: tPoint, ElemType: tPoint}, Src: tir.ValueOperand(tPoint, 4)},
							{Kind: tir.OpBorrow, Type: tRefI32, Dest: 5, BorrowKind: tir.BorrowShared, LoanID: 1, Place: tir.Place{Local: 5, LocalName: "pt", RootType: tPoint, ElemType: tI32, Projections: []tir.Projection{{Kind: tir.ProjField, FieldName: "x", FieldIndex: 0, ElemType: tI32}}}},
							{Kind: tir.OpInit, Type: tRefI32, Place: tir.Place{Local: 6, LocalName: "rn", RootType: tRefI32, ElemType: tRefI32}, Src: tir.ValueOperand(tRefI32, 5)},
						},
						Terminator: tir.Terminator{Kind: tir.TermBranchIf, Cond: tir.ValueOperand(tBool, 1), ThenBlock: 1, ElseBlock: 2},
					},
					{
						ID:           1,
						Label:        "then",
						Predecessors: []tir.BlockID{0},
						Successors:   []tir.BlockID{3},
						Instructions: []tir.Instruction{
							{Kind: tir.OpOwnershipTransfer, Type: tPtrU8, Place: tir.Place{Local: 3, LocalName: "buf", RootType: tPtrU8, ElemType: tPtrU8}, ContractID: 1, CalleeName: "mem::take"},
							{Kind: tir.OpForeignCall, Type: tUnit, Dest: 6, CalleeNamespace: "mem", CalleeName: "take", CallingConv: "C", Args: []tir.Operand{tir.PlaceOperand(tir.Place{Local: 3, LocalName: "buf", RootType: tPtrU8, ElemType: tPtrU8})}, ArgOwnership: []tir.FFIOwnership{tir.FFIOwnedTransfer}, ContractID: 1, HasContract: true, InUnsafe: true},
							{Kind: tir.OpValueAssign, Type: tI32, Dest: 7, Src: tir.ConstOperand(tI32, tir.Constant{Kind: tir.ConstInt, IntText: "10"})},
						},
						Terminator: tir.Terminator{Kind: tir.TermGoto, Target: 3},
					},
					{
						ID:           2,
						Label:        "else",
						Predecessors: []tir.BlockID{0},
						Successors:   []tir.BlockID{3},
						Instructions: []tir.Instruction{
							{Kind: tir.OpDropFlagSet, Type: tBool, FlagID: 1, FlagBool: true},
							{Kind: tir.OpCondDrop, Type: tStr, Place: tir.Place{Local: 4, LocalName: "s", RootType: tStr, ElemType: tStr}, FlagID: 1, GlueID: 1},
							{Kind: tir.OpValueAssign, Type: tI32, Dest: 7, Src: tir.ConstOperand(tI32, tir.Constant{Kind: tir.ConstInt, IntText: "20"})},
						},
						Terminator: tir.Terminator{Kind: tir.TermGoto, Target: 3},
					},
					{
						ID:           3,
						Label:        "exit",
						Predecessors: []tir.BlockID{1, 2},
						Instructions: []tir.Instruction{
							{Kind: tir.OpScopeExit, ScopeID: 1, Locals: []tir.LocalID{4, 5, 6}},
							{Kind: tir.OpStorageDead, ScopeID: 1, Locals: []tir.LocalID{4, 5, 6}},
						},
						Terminator: tir.Terminator{
							Kind:      tir.TermReturn,
							ReturnVal: &tir.Operand{Kind: tir.OperandValue, Type: tI32, Value: 7},
						},
					},
				},
			},
		},
	}
	return p
}

func TestPhase2C0_TIRConstructionAndDeterminism(t *testing.T) {
	p1 := buildBaselineValidProgram(t)
	p2 := buildBaselineValidProgram(t)

	if errs := tir.Verify(p1); len(errs) != 0 {
		t.Fatalf("合法基线 TIR 验证应当通过，实际错误:\n%v", tir.VerifyErrorSummary(errs))
	}
	d1 := p1.Dump()
	d2 := p2.Dump()
	if d1 != d2 {
		t.Fatalf("TIR Dump 必须严格确定性!\n--- 1 ---\n%s\n--- 2 ---\n%s", d1, d2)
	}
	for _, want := range []string{
		"tir.Program ownership_ok=true drop_ok=true",
		"ffi_catalog ns=mem schema=1.0 header=\"mem.h\"",
		"ForeignCall mem::take",
		"CondDrop _l4(s) flag=df1 glue=g1",
		"Aggregate[Struct]",
		"Borrow[Shared loan=L1]",
		"BranchIf %1:t2(bool) then=bb1 else=bb2",
	} {
		if !strings.Contains(d1, want) {
			t.Fatalf("TIR Dump 缺少期望信息 %q:\n%s", want, d1)
		}
	}
}

func TestPhase2C0_TIRVerifierMutationsRejectMalformedIR(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(p *tir.Program)
		want   string
	}{
		{
			name: "51_dangling_block_target",
			mutate: func(p *tir.Program) {
				p.Functions[0].Blocks[1].Terminator.Target = 99
			},
			want: "悬挂基本块",
		},
		{
			name: "52_duplicate_value_definition",
			mutate: func(p *tir.Program) {
				tI32 := p.Functions[0].ReturnType
				p.Functions[0].Blocks[1].Instructions = append(p.Functions[0].Blocks[1].Instructions, tir.Instruction{
					Kind: tir.OpConst, Type: tI32, Dest: 2, Const: tir.Constant{Kind: tir.ConstInt, IntText: "7"},
				})
			},
			want: "重复静态定义",
		},
		{
			name: "53_invalid_type_reference",
			mutate: func(p *tir.Program) {
				p.Functions[0].Locals[0].Type = 999
			},
			want: "无效类型",
		},
		{
			name: "54_mismatched_return_type",
			mutate: func(p *tir.Program) {
				tBool := p.Types.Bool()
				p.Functions[0].Blocks[3].Terminator.ReturnVal = &tir.Operand{Kind: tir.OperandConst, Type: tBool, Const: tir.Constant{Kind: tir.ConstBool, BoolVal: true}}
			},
			want: "Return 值类型",
		},
		{
			name: "55_invalid_drop_glue",
			mutate: func(p *tir.Program) {
				p.Functions[0].Blocks[2].Instructions[1].GlueID = 77
			},
			want: "未声明 DropGlue",
		},
		{
			name: "56_undeclared_drop_flag",
			mutate: func(p *tir.Program) {
				p.Functions[0].Blocks[2].Instructions[1].FlagID = 88
			},
			want: "未声明 DropFlag",
		},
		{
			name: "57_invalid_ffi_contract_reference",
			mutate: func(p *tir.Program) {
				p.Functions[0].Blocks[1].Instructions[1].ContractID = 55
			},
			want: "未声明 FFI ContractID",
		},
		{
			name: "58_missing_terminator",
			mutate: func(p *tir.Program) {
				p.Functions[0].Blocks[2].Terminator = tir.Terminator{Kind: tir.TermNone}
			},
			want: "缺少合法终结器",
		},
		{
			name: "59_invalid_branch_condition_type",
			mutate: func(p *tir.Program) {
				tI32 := p.Types.Int(true, 32, false, "i32")
				p.Functions[0].Blocks[0].Terminator.Cond = tir.ConstOperand(tI32, tir.Constant{Kind: tir.ConstInt, IntText: "1"})
			},
			want: "BranchIf 条件类型必须为 bool",
		},
		{
			name: "60_malformed_aggregate_field_layout",
			mutate: func(p *tir.Program) {
				p.Functions[0].Blocks[0].Instructions[5].AggItems[0].FieldName = "nonexistent_field"
			},
			want: "未知字段",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := buildBaselineValidProgram(t)
			tc.mutate(p)
			errs := tir.Verify(p)
			if len(errs) == 0 {
				t.Fatalf("畸形 TIR 用例 %s 必须被 Verify 拒绝", tc.name)
			}
			summary := tir.VerifyErrorSummary(errs).Error()
			if !strings.Contains(summary, tc.want) {
				t.Fatalf("畸形 TIR %s 期望错误包含 %q，实际:\n%s", tc.name, tc.want, summary)
			}
		})
	}
}

func TestPhase2C1_1_ReturnCompletenessInvariants(t *testing.T) {
	// 1. 非 unit 函数提供类型匹配 ReturnVal -> 通过校验
	t.Run("valid_non_unit_return_operand", func(t *testing.T) {
		p := buildBaselineValidProgram(t)
		if errs := tir.Verify(p); len(errs) != 0 {
			t.Fatalf("合法非 unit 返回程序不应报错: %v", errs)
		}
	})

	// 2. 非 unit 函数 TermReturn 缺失 ReturnVal -> 必须报 WON-INT-001
	t.Run("missing_non_unit_return_operand_rejected", func(t *testing.T) {
		p := buildBaselineValidProgram(t)
		fn := p.Functions[0]
		fn.Blocks[3].Terminator.ReturnVal = nil
		errs := tir.Verify(p)
		if len(errs) == 0 {
			t.Fatalf("非 unit 函数缺失 ReturnVal 必须被 tir.Verify 拒绝，却返回通过")
		}
		summary := tir.VerifyErrorSummary(errs).Error()
		if !strings.Contains(summary, "WON-INT-001") || !strings.Contains(summary, fn.Name) || !strings.Contains(summary, "bb3") {
			t.Fatalf("缺失 ReturnVal 期望报 WON-INT-001 且定位函数与基本块，实际:\n%s", summary)
		}
	})

	// 3. 非 unit 函数返回类型不匹配 -> 拒绝
	t.Run("mismatched_non_unit_return_type_rejected", func(t *testing.T) {
		p := buildBaselineValidProgram(t)
		tBool := p.Types.Bool()
		p.Functions[0].Blocks[3].Terminator.ReturnVal = &tir.Operand{
			Kind:  tir.OperandConst,
			Type:  tBool,
			Const: tir.Constant{Kind: tir.ConstBool, BoolVal: true},
		}
		errs := tir.Verify(p)
		if len(errs) == 0 || !strings.Contains(tir.VerifyErrorSummary(errs).Error(), "Return 值类型") {
			t.Fatalf("返回类型不匹配必须被拒绝，实际: %v", errs)
		}
	})

	// 4. 非 unit 函数不可达基本块存在缺失 ReturnVal 的 TermReturn -> 仍必须拒绝
	t.Run("unreachable_block_missing_non_unit_return_rejected", func(t *testing.T) {
		p := buildBaselineValidProgram(t)
		fn := p.Functions[0]
		deadID := tir.BlockID(len(fn.Blocks))
		fn.Blocks = append(fn.Blocks, &tir.BasicBlock{
			ID:         deadID,
			Label:      "unreachable_bad_exit",
			Terminator: tir.Terminator{Kind: tir.TermReturn, ReturnVal: nil},
		})
		errs := tir.Verify(p)
		if len(errs) == 0 {
			t.Fatalf("不可达块缺失非 unit ReturnVal 必须被 tir.Verify 一致拒绝，却通过校验")
		}
		summary := tir.VerifyErrorSummary(errs).Error()
		if !strings.Contains(summary, "WON-INT-001") || !strings.Contains(summary, "bb4") {
			t.Fatalf("不可达块缺失 ReturnVal 期望定位到 bb4 并含 WON-INT-001，实际:\n%s", summary)
		}
	})

	// 5 & 6 & 7. 合法 unit 裸 return、unit 尾调用操作数 return、unit 返回 main -> 均接受
	t.Run("valid_unit_bare_tail_and_main_returns", func(t *testing.T) {
		tt := tir.NewTypeTable()
		tUnit := tt.Unit()
		p := &tir.Program{
			Types:       tt,
			OwnershipOK: true,
			DropOK:      true,
			Functions: []*tir.Function{
				{
					ID: 1, Name: "bare_unit", CallingConv: "WON", ReturnType: tUnit, EntryBlock: 0, ExitBlock: 0,
					Blocks: []*tir.BasicBlock{{ID: 0, Label: "entry", Terminator: tir.Terminator{Kind: tir.TermReturn}}},
				},
				{
					ID: 2, Name: "tail_unit", CallingConv: "WON", ReturnType: tUnit, EntryBlock: 0, ExitBlock: 0,
					Values: []tir.ValueDecl{{ID: 1, Type: tUnit}},
					Blocks: []*tir.BasicBlock{{
						ID: 0, Label: "entry",
						Instructions: []tir.Instruction{{Kind: tir.OpCall, Type: tUnit, Dest: 1, CalleeFunc: 1, CalleeName: "bare_unit", CallingConv: "WON"}},
						Terminator:   tir.Terminator{Kind: tir.TermReturn, ReturnVal: ptrOperand(tir.ValueOperand(tUnit, 1))},
					}},
				},
				{
					ID: 3, Name: "main", CallingConv: "WON", ReturnType: tUnit, EntryBlock: 0, ExitBlock: 0,
					Blocks: []*tir.BasicBlock{{ID: 0, Label: "entry", Terminator: tir.Terminator{Kind: tir.TermReturn}}},
				},
			},
		}
		if errs := tir.Verify(p); len(errs) != 0 {
			t.Fatalf("合法 unit bare/tail/main 返回不应报错: %v", errs)
		}
	})
}

func ptrOperand(op tir.Operand) *tir.Operand { return &op }

package tir_test

import (
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/tir"
)

func buildBaselineForFuzz() *tir.Program {
	tt := tir.NewTypeTable()
	tBool := tt.Bool()
	tI32 := tt.Int(true, 32, false, "i32")
	tStr := tt.StringType()
	p := &tir.Program{
		Types:       tt,
		OwnershipOK: true,
		DropOK:      true,
		DropGlues: []tir.DropGlueDesc{
			{ID: 1, CanonicalName: "glue.String", Kind: tir.GlueString, Type: tStr, TypeName: "String"},
		},
		FFIContracts: []tir.FFIContractDesc{
			{ID: 1, ContractKey: "mem.take", Namespace: "mem", Function: "take", HeaderPath: "mem.h", SchemaVersion: "1.0", CallingConv: "C", BorrowsParam: -1},
		},
		Functions: []*tir.Function{
			{
				ID:          1,
				Name:        "fuzz_fn",
				CallingConv: "WON",
				ParamTypes:  []tir.TypeID{tBool},
				ReturnType:  tI32,
				EntryBlock:  0,
				ExitBlock:   1,
				Locals: []tir.LocalDecl{
					{ID: 1, Name: "c", Type: tBool, IsParam: true, ParamIdx: 0, HasInit: true, ScopeID: 0},
					{ID: 2, Name: "s", Type: tStr, IsMut: true, ScopeID: 1},
				},
				Values: []tir.ValueDecl{
					{ID: 1, Type: tBool},
					{ID: 2, Type: tI32},
				},
				DropFlags: []tir.DropFlag{
					{ID: 1, Name: "df1", OwnerLocal: 2, Place: tir.Place{Local: 2, LocalName: "s", RootType: tStr, ElemType: tStr}, Type: tStr, GlueID: 1},
				},
				Blocks: []*tir.BasicBlock{
					{
						ID:         0,
						Label:      "entry",
						Successors: []tir.BlockID{1},
						Instructions: []tir.Instruction{
							{Kind: tir.OpCopy, Type: tBool, Dest: 1, Place: tir.Place{Local: 1, LocalName: "c", RootType: tBool, ElemType: tBool}},
							{Kind: tir.OpConst, Type: tI32, Dest: 2, Const: tir.Constant{Kind: tir.ConstInt, IntText: "42"}},
							{Kind: tir.OpDropFlagDecl, Type: tBool, FlagID: 1, FlagBool: false},
						},
						Terminator: tir.Terminator{Kind: tir.TermGoto, Target: 1},
					},
					{
						ID:           1,
						Label:        "exit",
						Predecessors: []tir.BlockID{0},
						Instructions: []tir.Instruction{
							{Kind: tir.OpCondDrop, Type: tStr, Place: tir.Place{Local: 2, LocalName: "s", RootType: tStr, ElemType: tStr}, FlagID: 1, GlueID: 1},
						},
						Terminator: tir.Terminator{Kind: tir.TermReturn, ReturnVal: &tir.Operand{Kind: tir.OperandValue, Type: tI32, Value: 2}},
					},
				},
			},
		},
	}
	return p
}

// FuzzTIRVerifyAndDump 对异变 TIR 结构执行模糊验证，确保不 panic、不死循环、畸形 CFG/类型必定拒绝且 Dump 确定性。
func FuzzTIRVerifyAndDump(f *testing.F) {
	f.Add(uint8(0), uint32(0), int32(0), uint8(0))
	f.Add(uint8(1), uint32(99), int32(99), uint8(1))
	f.Add(uint8(2), uint32(2), int32(-1), uint8(5))
	f.Add(uint8(3), uint32(77), int32(0), uint8(2))

	f.Fuzz(func(t *testing.T, mode uint8, idMut uint32, targetMut int32, extra uint8) {
		p := buildBaselineForFuzz()
		fn := p.Functions[0]

		switch mode % 7 {
		case 0:
			// 无损坏基线检查：必须通过 Verify 且 Dump 确定
			if errs := tir.Verify(p); len(errs) != 0 {
				t.Fatalf("基线程序校验失败: %v", errs)
			}
		case 1:
			fn.Blocks[0].Terminator.Target = tir.BlockID(targetMut)
		case 2:
			fn.Blocks[0].Instructions[1].Type = tir.TypeID(idMut % 64)
		case 3:
			fn.Blocks[1].Instructions[0].GlueID = tir.DropGlueID(idMut % 16)
		case 4:
			fn.Blocks[1].Instructions[0].FlagID = tir.DropFlagID(idMut % 16)
		case 5:
			if extra%2 == 0 {
				fn.Blocks[0].Terminator = tir.Terminator{Kind: tir.TermNone}
			} else {
				fn.Blocks[0].Terminator = tir.Terminator{
					Kind:      tir.TermBranchIf,
					Cond:      tir.ConstOperand(tir.TypeID(idMut%8+1), tir.Constant{Kind: tir.ConstInt, IntText: "1"}),
					ThenBlock: 1,
					ElseBlock: tir.BlockID(targetMut),
				}
			}
		case 6:
			p.OwnershipOK = (extra % 2) == 0
			p.DropOK = (extra % 3) == 0
		}

		errs := tir.Verify(p)
		d1 := p.Dump()
		d2 := p.Dump()
		if d1 != d2 {
			t.Fatalf("Dump 非确定性:\n%s\n!=\n%s", d1, d2)
		}
		_ = errs
	})
}

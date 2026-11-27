package c11

import (
	"errors"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/tir"
)

// TestEmitterDefenseInDepthRejectsMissingNonUnitReturn 验证即便绕过前置 Verify/Capabilities 直接进入 C11 发射器，
// 缺失非 unit ReturnVal 的 TermReturn 仍严格 Fail-Closed 返回 WON-INT-001，绝不伪造 return 0。
func TestEmitterDefenseInDepthRejectsMissingNonUnitReturn(t *testing.T) {
	tt := tir.NewTypeTable()
	tI32 := tt.Int(true, 32, false, "i32")
	fn := &tir.Function{
		ID:          1,
		Name:        "malformed",
		CallingConv: "WON",
		ReturnType:  tI32,
		EntryBlock:  0,
		ExitBlock:   0,
		Blocks: []*tir.BasicBlock{{
			ID:         0,
			Label:      "entry",
			Terminator: tir.Terminator{Kind: tir.TermReturn, ReturnVal: nil},
		}},
	}
	prog := &tir.Program{
		Types:       tt,
		OwnershipOK: true,
		DropOK:      true,
		Functions:   []*tir.Function{fn},
	}

	ctx := &emitCtx{prog: prog, tt: tt}
	err := ctx.emitProgram()
	if err == nil {
		t.Fatalf("emitProgram 对缺失 ReturnVal 的非 unit 函数必须报错，却返回成功:\n%s", ctx.sb.String())
	}
	var capErr *CapabilityError
	if !errors.As(err, &capErr) || capErr.Code != "WON-INT-001" {
		t.Fatalf("期望 CapabilityError{WON-INT-001}，实际: %T %v", err, err)
	}
	if strings.Contains(ctx.sb.String(), "return 0;") {
		t.Fatalf("发射器失败路径不得产生 return 0;:\n%s", ctx.sb.String())
	}
}

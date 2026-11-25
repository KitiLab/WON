package types

import (
	"math/big"
	"testing"
)

func TestTypeInterningIdentity(t *testing.T) {
	ctx := NewContext()

	r1 := ctx.Ref(false, ctx.I32)
	r2 := ctx.Ref(false, ctx.I32)
	r3 := ctx.Ref(true, ctx.I32)

	if r1 != r2 {
		t.Fatalf("相同结构类型 &i32 应驻留为同一个 TypeID, 实际分别为 %d 与 %d", r1, r2)
	}
	if r1 == r3 {
		t.Fatalf("&i32 与 &mut i32 不应共享同一个 TypeID")
	}

	tup1 := ctx.Tuple([]TypeID{ctx.I32, ctx.Bool})
	tup2 := ctx.Tuple([]TypeID{ctx.I32, ctx.Bool})
	if tup1 != tup2 {
		t.Fatalf("相同元组类型 (i32, bool) 应返回同一 TypeID")
	}
	if empty := ctx.Tuple(nil); empty != ctx.Unit {
		t.Fatalf("空元组应规约为 Unit TypeID (%d), 实际为 %d", ctx.Unit, empty)
	}

	if ctx.Str == ctx.StrView || ctx.StrView == ctx.StringT || ctx.Str == ctx.StringT {
		t.Fatalf("str、&str 与 String 必须保持为 3 个互不相同的规范类型 ID")
	}
}

func TestIsSizedAndIsCopy(t *testing.T) {
	ctx := NewContext()

	if ctx.IsSized(ctx.Str) {
		t.Errorf("原语 str 为无尺寸 DST，IsSized 必须返回 false")
	}
	if !ctx.IsSized(ctx.StrView) || !ctx.IsSized(ctx.StringT) {
		t.Errorf("&str 与 String 均为定长类型，IsSized 必须返回 true")
	}

	sliceU8 := ctx.Slice(ctx.U8)
	if ctx.IsSized(sliceU8) {
		t.Errorf("[u8] 切片为无尺寸 DST，IsSized 必须返回 false")
	}
	if !ctx.IsSized(ctx.Ref(false, sliceU8)) {
		t.Errorf("&[u8] 胖指针应为定长类型")
	}

	if !ctx.IsCopy(ctx.I32) || !ctx.IsCopy(ctx.Bool) || !ctx.IsCopy(ctx.Ref(false, ctx.I32)) {
		t.Errorf("i32、bool 与 &i32 应具备 Copy 语义")
	}
	if ctx.IsCopy(ctx.Ref(true, ctx.I32)) {
		t.Errorf("可变引用 &mut i32 为独占所有权语义，不得具备 Copy 语义")
	}
	if ctx.IsCopy(ctx.StringT) {
		t.Errorf("堆分配所有权类型 String 不得具备 Copy 语义")
	}
}

func TestFitsIntegerLiteral(t *testing.T) {
	ctx := NewContext()

	if !ctx.FitsIntegerLiteral(127, ctx.I8) || ctx.FitsIntegerLiteral(128, ctx.I8) {
		t.Errorf("i8 字面量范围校验失败 (应接受 127，拒绝 128)")
	}
	if !ctx.FitsIntegerLiteral(-128, ctx.I8) || ctx.FitsIntegerLiteral(-129, ctx.I8) {
		t.Errorf("i8 负数范围校验失败")
	}
	if !ctx.FitsIntegerLiteral(255, ctx.U8) || ctx.FitsIntegerLiteral(256, ctx.U8) || ctx.FitsIntegerLiteral(-1, ctx.U8) {
		t.Errorf("u8 字面量范围校验失败")
	}
}

func TestCanAssignAndCanCastAndSubstitute(t *testing.T) {
	ctx := NewContext()

	// 严格禁止静默整数宽度转换或有/无符号互转
	if ctx.CanAssign(ctx.I32, ctx.I64) {
		t.Errorf("WON 禁止隐式数值扩张 i32 -> i64")
	}
	if ctx.CanAssign(ctx.I64, ctx.I32) {
		t.Errorf("WON 禁止隐式数值截断 i64 -> i32")
	}
	if ctx.CanAssign(ctx.U32, ctx.I32) {
		t.Errorf("WON 禁止无符号 u32 隐式赋值给有符号 i32")
	}

	// &mut T 可安全降级为 &T，反向禁止
	mutRef := ctx.Ref(true, ctx.U64)
	immRef := ctx.Ref(false, ctx.U64)
	if !ctx.CanAssign(mutRef, immRef) {
		t.Errorf("应允许 &mut u64 降级赋值给 &u64")
	}
	if ctx.CanAssign(immRef, mutRef) {
		t.Errorf("严禁不可变引用 &u64 升级赋值给 &mut u64")
	}

	// Never (!) 底类型可匹配任意目标类型
	if !ctx.CanAssign(ctx.Never, ctx.I32) || !ctx.CanAssign(ctx.Never, ctx.Bool) {
		t.Errorf("底类型 Never (!) 应能赋值给任意目标类型")
	}

	// 显式 as 转换检验
	if !ctx.CanCast(ctx.I32, ctx.I64) || !ctx.CanCast(ctx.USize, ctx.U64) {
		t.Errorf("应允许显式数值转型 i32 as i64 与 usize as u64")
	}
	if ctx.CanCast(ctx.Bool, ctx.StringT) || ctx.CanCast(ctx.I32, ctx.StrView) {
		t.Errorf("严禁标量向 String 或 &str 的非法显式转型")
	}

	// 泛型替换 Substitute 校验
	tpT := ctx.TypeParam(50, "T")
	tpE := ctx.TypeParam(51, "E")
	resGen := ctx.Enum(BuiltinResultSymbolID, "Result", []TypeID{tpT, tpE})
	resConcrete := ctx.Substitute(resGen, map[uint64]TypeID{
		50: ctx.I32,
		51: ctx.StrView,
	})
	expected := ctx.Enum(BuiltinResultSymbolID, "Result", []TypeID{ctx.I32, ctx.StrView})
	if resConcrete != expected {
		t.Fatalf("泛型替换失败: 期望 Result[i32, &str] (%d), 实际为 %s (%d)", expected, ctx.Format(resConcrete), resConcrete)
	}
}

func mustBig(s string) *big.Int {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("invalid test big.Int: " + s)
	}
	return v
}

func TestFitsBigIntLiteralExactBounds(t *testing.T) {
	ctx := NewContext()

	validPairs := []struct {
		val string
		tid TypeID
	}{
		{"18446744073709551615", ctx.U64},
		{"340282366920938463463374607431768211455", ctx.U128},
		{"170141183460469231731687303715884105727", ctx.I128},
		{"-170141183460469231731687303715884105728", ctx.I128},
		{"-9223372036854775808", ctx.ISize},
		{"18446744073709551615", ctx.USize},
	}
	for _, p := range validPairs {
		if !ctx.FitsBigIntLiteral(mustBig(p.val), p.tid) {
			t.Errorf("期望 %s 符合 %s 合法范围", p.val, ctx.Format(p.tid))
		}
	}

	invalidPairs := []struct {
		val string
		tid TypeID
	}{
		{"18446744073709551616", ctx.U64},
		{"340282366920938463463374607431768211456", ctx.U128},
		{"170141183460469231731687303715884105728", ctx.I128},
		{"-170141183460469231731687303715884105729", ctx.I128},
		{"-1", ctx.U128},
	}
	for _, p := range invalidPairs {
		if ctx.FitsBigIntLiteral(mustBig(p.val), p.tid) {
			t.Errorf("期望 %s 超出 %s 范围被拒绝", p.val, ctx.Format(p.tid))
		}
	}
}

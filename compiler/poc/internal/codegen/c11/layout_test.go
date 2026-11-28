package c11_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KitiLab/WON/compiler/poc/internal/codegen/c11"
	"github.com/KitiLab/WON/compiler/poc/internal/tir"
)

func TestLayoutL2StructAlignmentTieBreakingAndOffsets(t *testing.T) {
	tt := tir.NewTypeTable()
	tU8 := tt.Int(false, 8, false, "u8")
	tI32 := tt.Int(true, 32, false, "i32")
	tU64 := tt.Int(false, 64, false, "u64")
	tUnit := tt.Unit()

	// struct Mixed { a: u8 (0), b: u64 (1), c: unit (2), d: i32 (3), e: u8 (4), f: u64 (5) }
	// 期望 L2 物理序: b (align 8, idx 1, off 0), f (align 8, idx 5, off 8),
	//                 d (align 4, idx 3, off 16), a (align 1, idx 0, off 20), e (align 1, idx 4, off 21);
	//                 c (unit -> omitted), total size = 24, align = 8.
	sMixed := tt.ReserveStruct("Mixed", "Mixed", nil, false, false, "")
	tt.DefineStruct(sMixed, []tir.StructFieldType{
		{Name: "a", Index: 0, Type: tU8},
		{Name: "b", Index: 1, Type: tU64},
		{Name: "c", Index: 2, Type: tUnit},
		{Name: "d", Index: 3, Type: tI32},
		{Name: "e", Index: 4, Type: tU8},
		{Name: "f", Index: 5, Type: tU64},
	})

	tl, err := c11.BuildTargetLayout(c11.DefaultLinuxX86_64(), tt)
	if err != nil {
		t.Fatalf("BuildTargetLayout 失败: %v", err)
	}
	lay, err := tl.LayoutOf(sMixed)
	if err != nil {
		t.Fatalf("LayoutOf(Mixed) 失败: %v", err)
	}
	if lay.Size != 24 || lay.Align != 8 {
		t.Fatalf("Mixed size/align = (%d, %d), want (24, 8)", lay.Size, lay.Align)
	}
	wantOrder := []int{1, 5, 3, 0, 4}
	if len(lay.PhysicalFields) != len(wantOrder) {
		t.Fatalf("Mixed PhysicalFields=%v, want %v", lay.PhysicalFields, wantOrder)
	}
	for i, w := range wantOrder {
		if lay.PhysicalFields[i] != w {
			t.Fatalf("Mixed PhysicalFields[%d]=%d, want %d (%v)", i, lay.PhysicalFields[i], w, lay.PhysicalFields)
		}
	}
	wantOffsets := map[string]uint64{"b": 0, "f": 8, "d": 16, "a": 20, "e": 21}
	for _, f := range lay.Fields {
		if f.Name == "c" {
			if !f.IsZST || !f.Omitted || f.PhysicalIndex != -1 {
				t.Fatalf("unit 字段 'c' 必须标记 ZST 且被物理消去: %+v", f)
			}
			continue
		}
		if f.Offset != wantOffsets[f.Name] {
			t.Fatalf("字段 '%s' 偏移 = %d, want %d", f.Name, f.Offset, wantOffsets[f.Name])
		}
	}

	// 使用 GCC + Clang 实测 C11 _Static_assert 核验计算结果与真实 C 编译器布局字节一致
	cSrc := fmt.Sprintf(`
#include <stdint.h>
#include <stddef.h>
typedef struct {
    uint64_t b;
    uint64_t f;
    int32_t d;
    uint8_t a;
    uint8_t e;
} %s;
_Static_assert(sizeof(%s) == %d, "size mismatch");
_Static_assert(_Alignof(%s) == %d, "align mismatch");
_Static_assert(offsetof(%s, b) == %d, "b offset mismatch");
_Static_assert(offsetof(%s, f) == %d, "f offset mismatch");
_Static_assert(offsetof(%s, d) == %d, "d offset mismatch");
_Static_assert(offsetof(%s, a) == %d, "a offset mismatch");
_Static_assert(offsetof(%s, e) == %d, "e offset mismatch");
int main(void) { return 0; }
`, lay.CName, lay.CName, lay.Size, lay.CName, lay.Align,
		lay.CName, wantOffsets["b"],
		lay.CName, wantOffsets["f"],
		lay.CName, wantOffsets["d"],
		lay.CName, wantOffsets["a"],
		lay.CName, wantOffsets["e"])

	tmp := t.TempDir()
	cPath := filepath.Join(tmp, "layout_check.c")
	if err := os.WriteFile(cPath, []byte(cSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, cc := range []string{"gcc", "clang"} {
		bin := filepath.Join(tmp, "out_"+cc)
		cmd := exec.Command(cc, "-std=c11", "-pedantic-errors", "-Wall", "-Wextra", "-Werror", cPath, "-o", bin)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s 静态布局断言失败: %v\n%s", cc, err, out)
		}
	}
}

func TestLayoutZSTArraysTuplesAndStrings(t *testing.T) {
	tt := tir.NewTypeTable()
	tUnit := tt.Unit()
	tU8 := tt.Int(false, 8, false, "u8")
	tStrView := tt.Ref(false, tt.Str())
	tString := tt.StringType()
	tEmptyStruct := tt.ReserveStruct("Empty", "Empty", nil, false, false, "")
	tt.DefineStruct(tEmptyStruct, nil)
	tOnlyZST := tt.ReserveStruct("OnlyZST", "OnlyZST", nil, false, false, "")
	tt.DefineStruct(tOnlyZST, []tir.StructFieldType{
		{Name: "u", Index: 0, Type: tUnit},
		{Name: "e", Index: 1, Type: tEmptyStruct},
	})
	tZSTTuple := tt.Tuple([]tir.TypeID{tUnit, tEmptyStruct})
	tZeroArr := tt.Array(tU8, 0)
	tZSTArr := tt.Array(tOnlyZST, 8)
	tByteArr := tt.Array(tU8, 5)

	if !tt.IsZST(tUnit) || !tt.IsZST(tEmptyStruct) || !tt.IsZST(tOnlyZST) || !tt.IsZST(tZSTTuple) || !tt.IsZST(tZeroArr) || !tt.IsZST(tZSTArr) {
		t.Fatalf("IsZST 语义判定遗漏合法零尺寸类型")
	}
	if tt.IsZST(tU8) || tt.IsZST(tByteArr) || tt.IsZST(tStrView) || tt.IsZST(tString) {
		t.Fatalf("IsZST 误将非 ZST 类型判为零尺寸")
	}

	tl, err := c11.BuildTargetLayout(c11.DefaultLinuxX86_64(), tt)
	if err != nil {
		t.Fatalf("BuildTargetLayout 失败: %v", err)
	}
	for _, z := range []tir.TypeID{tEmptyStruct, tOnlyZST, tZSTTuple, tZeroArr, tZSTArr} {
		lay, err := tl.LayoutOf(z)
		if err != nil {
			t.Fatalf("LayoutOf(ZST %s) 失败: %v", tt.Format(z), err)
		}
		if !lay.IsZST || lay.Size != 1 || lay.Align != 1 {
			t.Fatalf("ZST %s 规范布局期望 (size=1, align=1, IsZST=true)，实际: %+v", tt.Format(z), lay)
		}
	}
	svLay, err := tl.LayoutOf(tStrView)
	if err != nil || svLay.CName != "_won_str_t" || svLay.Size != 16 || svLay.Align != 8 {
		t.Fatalf("&str 布局异常: %+v err=%v", svLay, err)
	}
	strLay, err := tl.LayoutOf(tString)
	if err != nil || strLay.CName != "_won_string_t" || strLay.Size != 24 || strLay.Align != 8 {
		t.Fatalf("String 布局异常: %+v err=%v", strLay, err)
	}
}

func TestLayoutEnumDiscriminantTagWidthsAndRejections(t *testing.T) {
	tt := tir.NewTypeTable()
	tI64 := tt.Int(true, 64, false, "i64")
	tUnit := tt.Unit()

	// 1. 单位枚举测试 8/16/32/64 位最小无符号标签规则 (ADR-012 D6)
	cases := []struct {
		name      string
		disc      string
		wantTag   string
		wantSize  uint64
		wantAlign uint64
	}{
		{"TagU8", "255", "uint8_t", 1, 1},
		{"TagU16", "256", "uint16_t", 2, 2},
		{"TagU32", "65536", "uint32_t", 4, 4},
		{"TagU64", "4294967296", "uint64_t", 8, 8},
		{"TagU64Max", "18446744073709551615", "uint64_t", 8, 8},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ltt := tir.NewTypeTable()
			e := ltt.ReserveEnum(tc.name, tc.name, nil, true)
			ltt.DefineEnum(e, []tir.EnumVariantType{
				{Name: "A", Index: 0, Kind: tir.VariantUnit},
				{Name: "B", Index: 1, Kind: tir.VariantUnit, HasDiscriminant: true, DiscriminantText: tc.disc},
			})
			tl, err := c11.BuildTargetLayout(c11.DefaultLinuxX86_64(), ltt)
			if err != nil {
				t.Fatalf("BuildTargetLayout 失败: %v", err)
			}
			lay, err := tl.LayoutOf(e)
			if err != nil {
				t.Fatalf("LayoutOf 失败: %v", err)
			}
			if !lay.IsUnitOnlyEnum || lay.TagCType != tc.wantTag || lay.Size != tc.wantSize || lay.Align != tc.wantAlign {
				t.Fatalf("%s layout=(unitOnly=%v tag=%s sz=%d al=%d), want (true %s %d %d)",
					tc.name, lay.IsUnitOnlyEnum, lay.TagCType, lay.Size, lay.Align, tc.wantTag, tc.wantSize, tc.wantAlign)
			}
		})
	}

	// 2. Option[i64] 与 Option[unit] 布局校验
	optI64 := tt.ReserveEnum("Option[i64]", "Option", []tir.TypeID{tI64}, true)
	tt.DefineEnum(optI64, []tir.EnumVariantType{
		{Name: "Some", Index: 0, Kind: tir.VariantTuple, TupleElems: []tir.TypeID{tI64}},
		{Name: "None", Index: 1, Kind: tir.VariantUnit},
	})
	optUnit := tt.ReserveEnum("Option[unit]", "Option", []tir.TypeID{tUnit}, true)
	tt.DefineEnum(optUnit, []tir.EnumVariantType{
		{Name: "Some", Index: 0, Kind: tir.VariantTuple, TupleElems: []tir.TypeID{tUnit}},
		{Name: "None", Index: 1, Kind: tir.VariantUnit},
	})
	tl, err := c11.BuildTargetLayout(c11.DefaultLinuxX86_64(), tt)
	if err != nil {
		t.Fatalf("BuildTargetLayout 失败: %v", err)
	}
	lOptI64, _ := tl.LayoutOf(optI64)
	if lOptI64.TagCType != "uint8_t" || lOptI64.PayloadOffset != 8 || lOptI64.Size != 16 || lOptI64.Align != 8 {
		t.Fatalf("Option[i64] 布局不符合预期: %+v", lOptI64)
	}
	lOptUnit, _ := tl.LayoutOf(optUnit)
	if !lOptUnit.IsUnitOnlyEnum || lOptUnit.Size != 1 || lOptUnit.Align != 1 {
		t.Fatalf("Option[unit] 应退化为 1 字节无符号标签: %+v", lOptUnit)
	}

	// 3. 拒绝非法/溢出/负数/重复判别式与值循环及数组尺寸溢出 (Fail-Closed)
	invalidCases := []struct {
		name  string
		setup func(*tir.TypeTable) tir.TypeID
	}{
		{
			name: "NegativeDiscriminant",
			setup: func(bad *tir.TypeTable) tir.TypeID {
				e := bad.ReserveEnum("Neg", "Neg", nil, true)
				bad.DefineEnum(e, []tir.EnumVariantType{
					{Name: "A", Index: 0, Kind: tir.VariantUnit, HasDiscriminant: true, DiscriminantText: "-1"},
				})
				return e
			},
		},
		{
			name: "U64OverflowDiscriminant",
			setup: func(bad *tir.TypeTable) tir.TypeID {
				e := bad.ReserveEnum("Over", "Over", nil, true)
				bad.DefineEnum(e, []tir.EnumVariantType{
					{Name: "A", Index: 0, Kind: tir.VariantUnit, HasDiscriminant: true, DiscriminantText: "18446744073709551616"},
				})
				return e
			},
		},
		{
			name: "DuplicateDiscriminant",
			setup: func(bad *tir.TypeTable) tir.TypeID {
				e := bad.ReserveEnum("Dup", "Dup", nil, true)
				bad.DefineEnum(e, []tir.EnumVariantType{
					{Name: "A", Index: 0, Kind: tir.VariantUnit, HasDiscriminant: true, DiscriminantText: "1"},
					{Name: "B", Index: 1, Kind: tir.VariantUnit, HasDiscriminant: true, DiscriminantText: "1"},
				})
				return e
			},
		},
		{
			name: "DirectRecursiveStructCycle",
			setup: func(bad *tir.TypeTable) tir.TypeID {
				a := bad.ReserveStruct("CycleA", "CycleA", nil, false, false, "")
				b := bad.ReserveStruct("CycleB", "CycleB", nil, false, false, "")
				bad.DefineStruct(a, []tir.StructFieldType{{Name: "b", Index: 0, Type: b}})
				bad.DefineStruct(b, []tir.StructFieldType{{Name: "a", Index: 0, Type: a}})
				return a
			},
		},
		{
			name: "ArrayByteSizeOverflow",
			setup: func(bad *tir.TypeTable) tir.TypeID {
				u64 := bad.Int(false, 64, false, "u64")
				return bad.Array(u64, 1<<60)
			},
		},
	}

	for _, ic := range invalidCases {
		t.Run(ic.name, func(t *testing.T) {
			badTT := tir.NewTypeTable()
			_ = ic.setup(badTT)
			if _, err := c11.BuildTargetLayout(c11.DefaultLinuxX86_64(), badTT); err == nil {
				t.Fatalf("非法布局 %s 必须 Fail-Closed 报错", ic.name)
			}
		})
	}
}

func FuzzTargetLayoutL2DeterminismAndConstraints(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7})
	f.Add([]byte{5, 3, 2, 1, 0, 4, 2, 0})
	f.Add([]byte{4, 4, 1, 3, 0, 2, 5, 1})

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) == 0 {
			return
		}
		tt := tir.NewTypeTable()
		pool := []tir.TypeID{
			tt.Unit(),
			tt.Bool(),
			tt.Int(false, 8, false, "u8"),
			tt.Int(true, 16, false, "i16"),
			tt.Int(true, 32, false, "i32"),
			tt.Int(false, 64, false, "u64"),
		}

		nFields := int(raw[0]%8) + 1
		fields := make([]tir.StructFieldType, nFields)
		for i := 0; i < nFields; i++ {
			b := raw[(i+1)%len(raw)]
			fields[i] = tir.StructFieldType{
				Name:  fmt.Sprintf("f_%d", i),
				Index: i,
				Type:  pool[int(b)%len(pool)],
			}
		}
		sid := tt.ReserveStruct("FuzzS", "FuzzS", nil, false, false, "")
		tt.DefineStruct(sid, fields)

		tl1, err := c11.BuildTargetLayout(c11.DefaultLinuxX86_64(), tt)
		if err != nil {
			t.Fatalf("BuildTargetLayout 意外失败: %v", err)
		}
		tl2, err := c11.BuildTargetLayout(c11.DefaultLinuxX86_64(), tt)
		if err != nil {
			t.Fatalf("BuildTargetLayout 第二次意外失败: %v", err)
		}
		l1, _ := tl1.LayoutOf(sid)
		l2, _ := tl2.LayoutOf(sid)

		if l1.Size != l2.Size || l1.Align != l2.Align || len(l1.PhysicalFields) != len(l2.PhysicalFields) {
			t.Fatalf("布局非确定性: l1=%+v l2=%+v", l1, l2)
		}
		if l1.Size == 0 || l1.Align == 0 || l1.Size%l1.Align != 0 {
			t.Fatalf("非法大小或对齐: size=%d align=%d", l1.Size, l1.Align)
		}
		// 校验物理字段非重叠且按 L2 降序对齐 / 声明序决胜
		var prevEnd uint64
		var prevAlign uint64 = ^uint64(0)
		var prevLog int = -1
		for _, pIdx := range l1.PhysicalFields {
			fl := l1.Fields[pIdx]
			if fl.Omitted || fl.IsZST {
				t.Fatalf("PhysicalFields 包含未消去的 ZST 字段: %+v", fl)
			}
			if fl.Offset < prevEnd || fl.Offset%fl.Align != 0 {
				t.Fatalf("字段偏移冲突或未对齐: %+v prevEnd=%d", fl, prevEnd)
			}
			if fl.Align > prevAlign || (fl.Align == prevAlign && fl.LogicalIndex <= prevLog) {
				t.Fatalf("违反 L2 降序对齐与声明序决胜规则: %+v prevAlign=%d prevLog=%d", fl, prevAlign, prevLog)
			}
			prevEnd = fl.Offset + fl.Size
			prevAlign = fl.Align
			prevLog = fl.LogicalIndex
		}
		_ = strings.Builder{}
	})
}

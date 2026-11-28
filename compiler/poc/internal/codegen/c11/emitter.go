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
	prog               *tir.Program
	tt                 *tir.TypeTable
	layout             *TargetLayout
	sb                 strings.Builder
	indent             int
	litDecls           []litDecl
	litByContent       map[string]string
	needEmpty          bool
	needZSTDummy       bool
	needBoundsU        bool
	needBoundsS        bool
	needWriteBytes     bool
	needStringRuntime  bool
	needDropString     bool
	needStringFrom     bool
	needStringPushStr  bool
	needStringPushChar bool
}

type litDecl struct {
	name string
	data string
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

func (c *emitCtx) layoutOf(tid tir.TypeID) (*TypeLayout, error) {
	return c.layout.LayoutOf(tid)
}

func (c *emitCtx) typeCName(tid tir.TypeID, pos TypePos) (string, error) {
	ti := c.tt.Info(tid)
	if pos == PosReturn && ti.Kind == tir.TypeUnit {
		return "void", nil
	}
	if isU8SliceRef(c.tt, tid) {
		return "const uint8_t*", nil
	}
	return MapType(c.tt, tid, pos)
}

func (c *emitCtx) registerStringLit(val string) string {
	if val == "" {
		c.needEmpty = true
		return "_won_empty_bytes"
	}
	if name, ok := c.litByContent[val]; ok {
		return name
	}
	name := fmt.Sprintf("_won_lit_%d", len(c.litDecls))
	c.litByContent[val] = name
	c.litDecls = append(c.litDecls, litDecl{name: name, data: val})
	return name
}

func (c *emitCtx) emitProgram() error {
	tl, err := BuildTargetLayout(DefaultLinuxX86_64(), c.tt)
	if err != nil {
		return err
	}
	c.layout = tl
	c.litByContent = make(map[string]string)

	// 预先为所有函数局部、寄存器与胶水类型计算布局，确保 typedef 顺序完整
	for _, g := range c.prog.DropGlues {
		if _, err := c.layoutOf(g.Type); err != nil {
			return err
		}
	}
	for _, fn := range c.prog.Functions {
		if fn == nil {
			continue
		}
		if c.tt.Info(fn.ReturnType).Kind != tir.TypeUnit {
			if _, err := c.layoutOf(fn.ReturnType); err != nil {
				return err
			}
		}
		for _, pt := range fn.ParamTypes {
			if _, err := c.layoutOf(pt); err != nil {
				return err
			}
		}
		for _, ld := range fn.Locals {
			if !isU8SliceRef(c.tt, ld.Type) {
				if _, err := c.layoutOf(ld.Type); err != nil {
					return err
				}
			}
		}
		for _, vd := range fn.Values {
			if c.tt.Info(vd.Type).Kind != tir.TypeUnit && !isU8SliceRef(c.tt, vd.Type) {
				if _, err := c.layoutOf(vd.Type); err != nil {
					return err
				}
			}
		}
	}

	c.writeLine("/* 由 WON Phase 2C.1 TIR->C11 (Phase 2C.2 扩展) 后端自动生成 - 严格遵循 ISO C11 标准 (Linux x86_64 LP64 ABI) */")
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

	// 基础运行时类型定义 (ADR-012 D2/D4)
	c.writeLine("typedef struct { uint8_t _zst; } _won_zst_t;")
	c.writeLine("typedef struct { const uint8_t* ptr; size_t len; } _won_str_t;")
	c.writeLine("typedef struct { uint8_t* ptr; size_t len; size_t cap; } _won_string_t;")
	c.writeLine(`_Static_assert(sizeof(_won_str_t) == 16 && _Alignof(_won_str_t) == 8, "_won_str_t ABI mismatch");`)
	c.writeLine(`_Static_assert(sizeof(_won_string_t) == 24 && _Alignof(_won_string_t) == 8, "_won_string_t ABI mismatch");`)
	c.writeLine("")

	// 发射确定性 L2 聚合与枚举 typedef 声明
	c.emitAggregateTypedefs()

	// 先将正文的辅助函数/原型/函数体写入子缓冲，按需在顶部插入字面量与辅助原语，防止 -Werror=unused-*
	bodyBuf := &emitCtx{
		prog:         c.prog,
		tt:           c.tt,
		layout:       c.layout,
		litByContent: c.litByContent,
	}

	if err := bodyBuf.emitDropGlues(); err != nil {
		return err
	}

	if bodyBuf.programUsesBuiltin(tir.BuiltinBytesEqual) {
		bodyBuf.writeLine("// --- WON 内置安全字节比较辅助函数 ---")
		bodyBuf.writeLine("static inline bool won_bytes_equal(const uint8_t* a, const uint8_t* b, unsigned long len) {")
		bodyBuf.writeLine("    return memcmp(a, b, len) == 0;")
		bodyBuf.writeLine("}")
		bodyBuf.writeLine("")
	}

	protoCount := 0
	for _, fn := range bodyBuf.prog.Functions {
		if fn == nil || fn.Name == "main" {
			continue
		}
		sig, _, err := bodyBuf.buildFuncSignature(fn)
		if err != nil {
			return err
		}
		bodyBuf.writeLine(sig + ";")
		protoCount++
	}
	if protoCount > 0 {
		bodyBuf.writeLine("")
	}

	for _, fn := range bodyBuf.prog.Functions {
		if fn == nil {
			continue
		}
		if err := bodyBuf.emitFunction(fn); err != nil {
			return err
		}
	}

	// 按需写入静态字节字面量、空串哨兵、ZST 哑元、运行时分配器与越界检查辅助函数
	if bodyBuf.needStringRuntime || bodyBuf.needDropString || bodyBuf.needStringFrom || bodyBuf.needStringPushStr || bodyBuf.needStringPushChar {
		bodyBuf.needStringRuntime = true
		bodyBuf.needEmpty = true
	}
	if bodyBuf.needStringPushChar {
		bodyBuf.needStringPushStr = true
	}
	if bodyBuf.needEmpty {
		c.writeLine("static const uint8_t _won_empty_bytes[1] = {0};")
	}
	needAlloc := bodyBuf.needStringFrom || bodyBuf.needStringPushStr
	needFree := bodyBuf.needDropString
	if bodyBuf.needStringRuntime {
		c.writeLine("#ifndef _WON_ALLOC_IMPL")
		c.writeLine("#define _WON_ALLOC_IMPL(bytes) malloc(bytes)")
		c.writeLine("#endif")
		c.writeLine("#ifndef _WON_REALLOC_IMPL")
		c.writeLine("#define _WON_REALLOC_IMPL(ptr, new_cap) realloc((ptr), (new_cap))")
		c.writeLine("#endif")
		c.writeLine("#ifndef _WON_FREE_IMPL")
		c.writeLine("#define _WON_FREE_IMPL(ptr, cap) free(ptr)")
		c.writeLine("#endif")
	}
	if needAlloc {
		c.writeLine("")
		c.writeLine("_Noreturn static inline void _won_panic_oom(size_t bytes) {")
		c.writeLine("    fprintf(stderr, \"WON runtime panic: out of memory allocating %zu bytes\\n\", bytes);")
		c.writeLine("    abort();")
		c.writeLine("}")
		c.writeLine("")
		c.writeLine("static inline void* _won_alloc(size_t bytes) {")
		c.writeLine("    if (bytes == 0) {")
		c.writeLine("        fprintf(stderr, \"WON runtime panic: _won_alloc called with 0 bytes\\n\");")
		c.writeLine("        abort();")
		c.writeLine("    }")
		c.writeLine("    void* p = _WON_ALLOC_IMPL(bytes);")
		c.writeLine("    if (p == NULL) {")
		c.writeLine("        _won_panic_oom(bytes);")
		c.writeLine("    }")
		c.writeLine("    return p;")
		c.writeLine("}")
	}
	if needFree {
		c.writeLine("")
		c.writeLine("static inline void _won_free(void* ptr, size_t cap) {")
		c.writeLine("    if (cap == 0 || ptr == NULL || ptr == (void*)_won_empty_bytes) {")
		c.writeLine("        return;")
		c.writeLine("    }")
		c.writeLine("    _WON_FREE_IMPL(ptr, cap);")
		c.writeLine("}")
	}
	if bodyBuf.needStringPushStr {
		c.writeLine("")
		c.writeLine("static inline size_t _won_calc_growth(size_t old_cap, size_t len, size_t add_len) {")
		c.writeLine("    if (add_len > SIZE_MAX - len) {")
		c.writeLine("        fprintf(stderr, \"WON runtime panic: string capacity overflow\\n\");")
		c.writeLine("        abort();")
		c.writeLine("    }")
		c.writeLine("    size_t required = len + add_len;")
		c.writeLine("    size_t new_cap = 16;")
		c.writeLine("    if (old_cap > 0) {")
		c.writeLine("        if (old_cap > SIZE_MAX / 2) {")
		c.writeLine("            new_cap = SIZE_MAX;")
		c.writeLine("        } else {")
		c.writeLine("            new_cap = old_cap * 2;")
		c.writeLine("        }")
		c.writeLine("    }")
		c.writeLine("    if (new_cap < required) {")
		c.writeLine("        new_cap = required;")
		c.writeLine("    }")
		c.writeLine("    if (new_cap < 16) {")
		c.writeLine("        new_cap = 16;")
		c.writeLine("    }")
		c.writeLine("    return new_cap;")
		c.writeLine("}")
		c.writeLine("")
		c.writeLine("static inline void* _won_realloc(void* ptr, size_t old_cap, size_t new_cap) {")
		c.writeLine("    if (new_cap == 0) {")
		c.writeLine("        fprintf(stderr, \"WON runtime panic: _won_realloc called with 0 new_cap\\n\");")
		c.writeLine("        abort();")
		c.writeLine("    }")
		c.writeLine("    if (old_cap == 0 || ptr == (void*)_won_empty_bytes) {")
		c.writeLine("        return _won_alloc(new_cap);")
		c.writeLine("    }")
		c.writeLine("    void* new_ptr = _WON_REALLOC_IMPL(ptr, new_cap);")
		c.writeLine("    if (new_ptr == NULL) {")
		c.writeLine("        _won_panic_oom(new_cap);")
		c.writeLine("    }")
		c.writeLine("    return new_ptr;")
		c.writeLine("}")
	}
	if bodyBuf.needDropString {
		c.writeLine("")
		c.writeLine("static inline void _won_drop_string(_won_string_t* s) {")
		c.writeLine("    if (s == NULL) return;")
		c.writeLine("    if (s->cap > 0) {")
		c.writeLine("        _won_free(s->ptr, s->cap);")
		c.writeLine("    }")
		c.writeLine("    s->ptr = (uint8_t*)_won_empty_bytes;")
		c.writeLine("    s->len = 0;")
		c.writeLine("    s->cap = 0;")
		c.writeLine("}")
	}
	if bodyBuf.needStringFrom {
		c.writeLine("")
		c.writeLine("static inline _won_string_t _won_string_from(_won_str_t view) {")
		c.writeLine("    if (view.len == 0) {")
		c.writeLine("        return (_won_string_t){")
		c.writeLine("            .ptr = (uint8_t*)_won_empty_bytes,")
		c.writeLine("            .len = 0,")
		c.writeLine("            .cap = 0,")
		c.writeLine("        };")
		c.writeLine("    }")
		c.writeLine("    size_t cap = view.len < 16 ? 16 : view.len;")
		c.writeLine("    uint8_t* buf = (uint8_t*)_won_alloc(cap);")
		c.writeLine("    memcpy(buf, view.ptr, view.len);")
		c.writeLine("    return (_won_string_t){")
		c.writeLine("        .ptr = buf,")
		c.writeLine("        .len = view.len,")
		c.writeLine("        .cap = cap,")
		c.writeLine("    };")
		c.writeLine("}")
	}
	if bodyBuf.needStringPushStr {
		c.writeLine("")
		c.writeLine("static inline void _won_string_push_str(_won_string_t* s, _won_str_t view) {")
		c.writeLine("    if (s == NULL || view.len == 0) {")
		c.writeLine("        return;")
		c.writeLine("    }")
		c.writeLine("    if (view.len > SIZE_MAX - s->len) {")
		c.writeLine("        fprintf(stderr, \"WON runtime panic: string capacity overflow\\n\");")
		c.writeLine("        abort();")
		c.writeLine("    }")
		c.writeLine("    size_t required = s->len + view.len;")
		c.writeLine("    if (required > s->cap) {")
		c.writeLine("        size_t new_cap = _won_calc_growth(s->cap, s->len, view.len);")
		c.writeLine("        s->ptr = (uint8_t*)_won_realloc(s->ptr, s->cap, new_cap);")
		c.writeLine("        s->cap = new_cap;")
		c.writeLine("    }")
		c.writeLine("    memcpy(s->ptr + s->len, view.ptr, view.len);")
		c.writeLine("    s->len = required;")
		c.writeLine("}")
	}
	if bodyBuf.needStringPushChar {
		c.writeLine("")
		c.writeLine("static inline void _won_string_push_char(_won_string_t* s, uint32_t cp) {")
		c.writeLine("    uint8_t buf[4];")
		c.writeLine("    size_t len = 0;")
		c.writeLine("    if (cp <= 0x7F) {")
		c.writeLine("        buf[0] = (uint8_t)cp;")
		c.writeLine("        len = 1;")
		c.writeLine("    } else if (cp <= 0x7FF) {")
		c.writeLine("        buf[0] = (uint8_t)(0xC0 | (cp >> 6));")
		c.writeLine("        buf[1] = (uint8_t)(0x80 | (cp & 0x3F));")
		c.writeLine("        len = 2;")
		c.writeLine("    } else if (cp <= 0xFFFF) {")
		c.writeLine("        buf[0] = (uint8_t)(0xE0 | (cp >> 12));")
		c.writeLine("        buf[1] = (uint8_t)(0x80 | ((cp >> 6) & 0x3F));")
		c.writeLine("        buf[2] = (uint8_t)(0x80 | (cp & 0x3F));")
		c.writeLine("        len = 3;")
		c.writeLine("    } else if (cp <= 0x10FFFF) {")
		c.writeLine("        buf[0] = (uint8_t)(0xF0 | (cp >> 18));")
		c.writeLine("        buf[1] = (uint8_t)(0x80 | ((cp >> 12) & 0x3F));")
		c.writeLine("        buf[2] = (uint8_t)(0x80 | ((cp >> 6) & 0x3F));")
		c.writeLine("        buf[3] = (uint8_t)(0x80 | (cp & 0x3F));")
		c.writeLine("        len = 4;")
		c.writeLine("    } else {")
		c.writeLine("        buf[0] = 0xEF; buf[1] = 0xBF; buf[2] = 0xBD;")
		c.writeLine("        len = 3;")
		c.writeLine("    }")
		c.writeLine("    _won_string_push_str(s, (_won_str_t){ .ptr = buf, .len = len });")
		c.writeLine("}")
	}
	if bodyBuf.needZSTDummy {
		c.writeLine("static _won_zst_t _won_zst_dummy = {0};")
	}
	for _, ld := range bodyBuf.litDecls {
		c.emitStaticByteLiteral(ld)
	}
	if bodyBuf.needBoundsU {
		c.writeLine("static inline size_t _won_chk_idx_u(uint64_t idx, size_t len) {")
		c.writeLine("    if (idx >= len) { fprintf(stderr, \"WON index out of bounds\\n\"); abort(); }")
		c.writeLine("    return (size_t)idx;")
		c.writeLine("}")
	}
	if bodyBuf.needBoundsS {
		c.writeLine("static inline size_t _won_chk_idx_s(int64_t idx, size_t len) {")
		c.writeLine("    if (idx < 0 || ((uint64_t)idx) >= ((uint64_t)len)) { fprintf(stderr, \"WON index out of bounds\\n\"); abort(); }")
		c.writeLine("    return (size_t)idx;")
		c.writeLine("}")
	}
	if bodyBuf.needWriteBytes {
		c.writeLine("#ifndef _WON_WRITE_BYTES_IMPL")
		c.writeLine("#define _WON_WRITE_BYTES_IMPL(ptr, len) (void)fwrite((ptr), 1, (len), stdout)")
		c.writeLine("#endif")
		c.writeLine("static inline void _won_write_bytes(const uint8_t *ptr, size_t len) {")
		c.writeLine("    if (len == 0) return;")
		c.writeLine("    _WON_WRITE_BYTES_IMPL(ptr, len);")
		c.writeLine("}")
	}
	if bodyBuf.needEmpty || len(bodyBuf.litDecls) > 0 || bodyBuf.needZSTDummy || bodyBuf.needBoundsU || bodyBuf.needBoundsS || bodyBuf.needWriteBytes || bodyBuf.needStringRuntime {
		c.writeLine("")
	}

	c.sb.WriteString(bodyBuf.sb.String())
	return nil
}

func (c *emitCtx) emitStaticByteLiteral(ld litDecl) {
	b := []byte(ld.data)
	parts := make([]string, len(b)+1)
	for i, ch := range b {
		parts[i] = fmt.Sprintf("0x%02x", ch)
	}
	parts[len(b)] = "0x00"
	c.writeLine(fmt.Sprintf("static const uint8_t %s[%d] = { %s };", ld.name, len(b)+1, strings.Join(parts, ", ")))
}

func (c *emitCtx) containsOpaqueForeign(tid tir.TypeID, seen map[tir.TypeID]bool) bool {
	if !c.tt.Valid(tid) || seen[tid] {
		return false
	}
	seen[tid] = true
	defer delete(seen, tid)
	ti := c.tt.Info(tid)
	switch ti.Kind {
	case tir.TypeStruct:
		if ti.IsOpaqueForeign {
			return true
		}
		for _, f := range ti.Fields {
			if c.containsOpaqueForeign(f.Type, seen) {
				return true
			}
		}
	case tir.TypeTuple:
		for _, el := range ti.Elems {
			if c.containsOpaqueForeign(el, seen) {
				return true
			}
		}
	case tir.TypeArray:
		return c.containsOpaqueForeign(ti.Elem, seen)
	case tir.TypeEnum:
		for _, v := range ti.Variants {
			for _, te := range v.TupleElems {
				if c.containsOpaqueForeign(te, seen) {
					return true
				}
			}
			for _, sf := range v.StructFields {
				if c.containsOpaqueForeign(sf.Type, seen) {
					return true
				}
			}
		}
	}
	return false
}

func (c *emitCtx) emitAggregateTypedefs() {
	// 前置标签声明支撑自引用指针结构体 (如 *mut Node)
	for _, tid := range c.layout.OrderedTypes {
		lay := c.layout.Layouts[tid]
		if lay == nil || !lay.NeedsTypedef || lay.IsZST || lay.IsStrView || lay.IsStringType || lay.IsUnitOnlyEnum {
			continue
		}
		if lay.Kind == tir.TypeStruct || lay.Kind == tir.TypeTuple {
			tag := strings.TrimSuffix(lay.CName, "_t")
			c.writeLine(fmt.Sprintf("typedef struct %s %s;", tag, lay.CName))
		}
	}

	emittedCount := 0
	for _, tid := range c.layout.OrderedTypes {
		lay := c.layout.Layouts[tid]
		if lay == nil || !lay.NeedsTypedef || lay.IsStrView || lay.IsStringType {
			continue
		}
		if lay.CName == "_won_zst_t" {
			continue
		}
		hasOpaque := c.containsOpaqueForeign(tid, make(map[tir.TypeID]bool, 8))
		switch lay.Kind {
		case tir.TypeStruct, tir.TypeTuple:
			if lay.IsZST {
				continue
			}
			tag := strings.TrimSuffix(lay.CName, "_t")
			c.writeLine(fmt.Sprintf("struct %s {", tag))
			c.indent++
			for _, pIdx := range lay.PhysicalFields {
				fl := lay.Fields[pIdx]
				fC, _ := c.typeCName(fl.Type, PosValue)
				c.writeLine(fmt.Sprintf("%s %s;", fC, fl.CName))
			}
			c.indent--
			c.writeLine("};")
			if !hasOpaque {
				c.writeLine(fmt.Sprintf(`_Static_assert(sizeof(%s) == %d && _Alignof(%s) == %d, "L2 aggregate layout mismatch");`,
					lay.CName, lay.Size, lay.CName, lay.Align))
			}
			emittedCount++

		case tir.TypeArray:
			if lay.IsZST || lay.ArrayLen == 0 {
				c.writeLine(fmt.Sprintf("typedef struct { uint8_t _zst; } %s;", lay.CName))
			} else {
				elemC, _ := c.typeCName(lay.ElemType, PosValue)
				c.writeLine(fmt.Sprintf("typedef struct { %s elems[%d]; } %s;", elemC, lay.ArrayLen, lay.CName))
				if !hasOpaque {
					c.writeLine(fmt.Sprintf(`_Static_assert(sizeof(%s) == %d && _Alignof(%s) == %d, "array layout mismatch");`,
						lay.CName, lay.Size, lay.CName, lay.Align))
				}
			}
			emittedCount++

		case tir.TypeEnum:
			if lay.IsUninhabited {
				c.writeLine(fmt.Sprintf("typedef struct { uint8_t _uninhabited; } %s;", lay.CName))
			} else if lay.IsUnitOnlyEnum {
				c.writeLine(fmt.Sprintf("typedef %s %s;", lay.TagCType, lay.CName))
				c.writeLine(fmt.Sprintf(`_Static_assert(sizeof(%s) == %d && _Alignof(%s) == %d, "unit enum layout mismatch");`,
					lay.CName, lay.Size, lay.CName, lay.Align))
			} else {
				c.writeLine("typedef struct {")
				c.indent++
				c.writeLine(fmt.Sprintf("%s tag;", lay.TagCType))
				c.writeLine("union {")
				c.indent++
				for _, vl := range lay.Variants {
					if !vl.HasPayload {
						continue
					}
					c.writeLine("struct {")
					c.indent++
					for _, pIdx := range vl.PhysicalFields {
						fl := vl.Fields[pIdx]
						fC, _ := c.typeCName(fl.Type, PosValue)
						c.writeLine(fmt.Sprintf("%s %s;", fC, fl.CName))
					}
					c.indent--
					c.writeLine(fmt.Sprintf("} %s;", vl.CFieldName))
				}
				c.indent--
				c.writeLine("} payload;")
				c.indent--
				c.writeLine(fmt.Sprintf("} %s;", lay.CName))
				if !hasOpaque {
					c.writeLine(fmt.Sprintf(`_Static_assert(sizeof(%s) == %d && _Alignof(%s) == %d, "enum layout mismatch");`,
						lay.CName, lay.Size, lay.CName, lay.Align))
				}
			}
			emittedCount++
		}
	}
	if emittedCount > 0 {
		c.writeLine("")
	}
}

func collectUsedDropGlues(prog *tir.Program) map[tir.DropGlueID]bool {
	used := make(map[tir.DropGlueID]bool)
	glueByID := make(map[tir.DropGlueID]tir.DropGlueDesc, len(prog.DropGlues))
	for _, g := range prog.DropGlues {
		glueByID[g.ID] = g
	}

	var mark func(id tir.DropGlueID)
	mark = func(id tir.DropGlueID) {
		if id == tir.InvalidDropGlue || used[id] {
			return
		}
		used[id] = true
		g, ok := glueByID[id]
		if !ok {
			return
		}
		for _, f := range g.Fields {
			mark(f.GlueID)
		}
		for _, e := range g.TupleElems {
			mark(e.GlueID)
		}
		if g.ElemGlueID != tir.InvalidDropGlue {
			mark(g.ElemGlueID)
		}
		for _, v := range g.Variants {
			for _, f := range v.StructFields {
				mark(f.GlueID)
			}
			for _, e := range v.TupleElems {
				mark(e.GlueID)
			}
		}
	}

	for _, fn := range prog.Functions {
		if fn == nil {
			continue
		}
		for _, bb := range fn.Blocks {
			for _, inst := range bb.Instructions {
				if (inst.Kind == tir.OpDrop || inst.Kind == tir.OpCondDrop) && inst.GlueID != tir.InvalidDropGlue {
					mark(inst.GlueID)
				}
			}
		}
	}
	return used
}

func (c *emitCtx) emitDropGlues() error {
	if len(c.prog.DropGlues) == 0 {
		return nil
	}
	usedGlues := collectUsedDropGlues(c.prog)
	if len(usedGlues) == 0 {
		return nil
	}
	glueMap := make(map[tir.DropGlueID]tir.DropGlueDesc, len(c.prog.DropGlues))
	for _, g := range c.prog.DropGlues {
		glueMap[g.ID] = g
	}
	for _, g := range c.prog.DropGlues {
		if !usedGlues[g.ID] {
			continue
		}
		cTyp, err := c.typeCName(g.Type, PosLocal)
		if err != nil {
			return err
		}
		c.writeLine(fmt.Sprintf("static inline void _won_drop_glue_%d(%s* v);", g.ID, cTyp))
	}
	c.writeLine("")

	for _, g := range c.prog.DropGlues {
		if !usedGlues[g.ID] {
			continue
		}
		cTyp, err := c.typeCName(g.Type, PosLocal)
		if err != nil {
			return err
		}
		lay, err := c.layoutOf(g.Type)
		if err != nil {
			return err
		}
		c.writeLine(fmt.Sprintf("static inline void _won_drop_glue_%d(%s* v) {", g.ID, cTyp))
		c.indent++
		c.writeLine("if (v == NULL) return;")
		switch g.Kind {
		case tir.GlueString:
			c.needDropString = true
			c.needStringRuntime = true
			c.needEmpty = true
			c.writeLine("_won_drop_string(v);")

		case tir.GlueForeignResource:
			_, releaseC, okSym := splitForeignSymbol(g.ReleaseSymbol)
			if !okSym {
				return newCapErr("WON-GEN-002", fmt.Sprintf("Drop 释放符号 '%s' 非法", g.ReleaseSymbol), tir.SourceSpan{}, "")
			}
			c.writeLine(fmt.Sprintf("%s(*v);", releaseC))

		case tir.GlueStruct:
			// 严格按照逻辑声明 Index 升序（0..N-1）逐字段析构，绝不依物理 L2 顺序
			fields := append([]tir.DropGlueField(nil), g.Fields...)
			sortDropFieldsByIndex(fields)
			for _, f := range fields {
				if f.GlueID == tir.InvalidDropGlue {
					continue
				}
				if f.Index < 0 || f.Index >= len(lay.Fields) || lay.Fields[f.Index].Omitted {
					continue
				}
				fl := lay.Fields[f.Index]
				c.writeLine(fmt.Sprintf("_won_drop_glue_%d(&(v->%s));", f.GlueID, fl.CName))
			}

		case tir.GlueTuple:
			elems := append([]tir.DropGlueElem(nil), g.TupleElems...)
			sortDropElemsByIndex(elems)
			for _, el := range elems {
				if el.GlueID == tir.InvalidDropGlue {
					continue
				}
				if el.Index < 0 || el.Index >= len(lay.Fields) || lay.Fields[el.Index].Omitted {
					continue
				}
				fl := lay.Fields[el.Index]
				c.writeLine(fmt.Sprintf("_won_drop_glue_%d(&(v->%s));", el.GlueID, fl.CName))
			}

		case tir.GlueArray:
			if g.ElemGlueID != tir.InvalidDropGlue && !lay.IsZST && lay.ArrayLen > 0 {
				c.writeLine(fmt.Sprintf("for (size_t _i = 0; _i < ((size_t)UINT64_C(%d)); ++_i) {", lay.ArrayLen))
				c.indent++
				c.writeLine(fmt.Sprintf("_won_drop_glue_%d(&(v->elems[_i]));", g.ElemGlueID))
				c.indent--
				c.writeLine("}")
			}

		case tir.GlueEnum:
			if !lay.IsUninhabited && !lay.IsUnitOnlyEnum {
				c.writeLine("switch (v->tag) {")
				for _, gv := range g.Variants {
					if gv.Index < 0 || gv.Index >= len(lay.Variants) {
						continue
					}
					vl := lay.Variants[gv.Index]
					if !vl.HasPayload {
						continue
					}
					c.writeLine(fmt.Sprintf("case ((%s)UINT64_C(%d)): {", lay.TagCType, vl.Discriminant))
					c.indent++
					if vl.IsStructVariant {
						fields := append([]tir.DropGlueField(nil), gv.StructFields...)
						sortDropFieldsByIndex(fields)
						for _, f := range fields {
							if f.GlueID == tir.InvalidDropGlue || f.Index < 0 || f.Index >= len(vl.Fields) || vl.Fields[f.Index].Omitted {
								continue
							}
							c.writeLine(fmt.Sprintf("_won_drop_glue_%d(&(v->payload.%s.%s));", f.GlueID, vl.CFieldName, vl.Fields[f.Index].CName))
						}
					} else {
						elems := append([]tir.DropGlueElem(nil), gv.TupleElems...)
						sortDropElemsByIndex(elems)
						for _, el := range elems {
							if el.GlueID == tir.InvalidDropGlue || el.Index < 0 || el.Index >= len(vl.Fields) || vl.Fields[el.Index].Omitted {
								continue
							}
							c.writeLine(fmt.Sprintf("_won_drop_glue_%d(&(v->payload.%s.%s));", el.GlueID, vl.CFieldName, vl.Fields[el.Index].CName))
						}
					}
					c.writeLine("break;")
					c.indent--
					c.writeLine("}")
				}
				c.writeLine("default: break;")
				c.writeLine("}")
			}
		}
		c.indent--
		c.writeLine("}")
		c.writeLine("")
	}
	return nil
}

func sortDropFieldsByIndex(fields []tir.DropGlueField) {
	for i := 1; i < len(fields); i++ {
		for j := i; j > 0 && fields[j].Index < fields[j-1].Index; j-- {
			fields[j], fields[j-1] = fields[j-1], fields[j]
		}
	}
}

func sortDropElemsByIndex(elems []tir.DropGlueElem) {
	for i := 1; i < len(elems); i++ {
		for j := i; j > 0 && elems[j].Index < elems[j-1].Index; j-- {
			elems[j], elems[j-1] = elems[j-1], elems[j]
		}
	}
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
	inPlaceArrRep   map[tir.ValueID]bool
	staticStrVal    map[tir.ValueID]string
	hasStaticStrVal map[tir.ValueID]bool
	downcastVariant map[tir.ValueID]int
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
		inPlaceArrRep:   make(map[tir.ValueID]bool),
		staticStrVal:    make(map[tir.ValueID]string),
		hasStaticStrVal: make(map[tir.ValueID]bool),
		downcastVariant: make(map[tir.ValueID]int),
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

	// 1. 消除未引用或 ZST 形参的 -Werror=unused-parameter 告警
	for _, ld := range fn.Locals {
		if !ld.IsParam {
			continue
		}
		isZST := false
		if lay, err := c.layoutOf(ld.Type); err == nil && (lay.IsZST || lay.LogicalZeroSized) {
			isZST = true
		}
		if !fs.usedLocal[ld.ID] || !fs.localRead[ld.ID] || isZST {
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

	// 4. 声明所有非 unit 表达式占位临时寄存器变量
	for _, vd := range fn.Values {
		if fs.skipConstVal[vd.ID] || fs.inPlaceArrRep[vd.ID] {
			continue
		}
		if c.tt.Info(vd.Type).Kind == tir.TypeUnit {
			continue
		}
		// 检查是否在可达块内被实际读取（防止产生 -Werror=unused-but-set-variable）
		if fs.valUses[vd.ID] == 0 {
			continue
		}
		cTyp, err := c.typeCName(vd.Type, PosValue)
		if err != nil {
			return err
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
		if c.tt.Info(ti.Elem).Kind == tir.TypeStr {
			return "{0}", nil
		}
		if isU8SliceRef(c.tt, tid) {
			return "NULL", nil
		}
		return "NULL", nil
	case tir.TypeEnum:
		if c.layout != nil {
			if lay := c.layout.Layouts[tid]; lay != nil && lay.IsUnitOnlyEnum {
				return "0", nil
			}
		}
		return "{0}", nil
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
	cTyp, err := c.typeCName(ld.Type, PosLocal)
	if err != nil {
		return err
	}
	zeroInit, err := c.zeroInitExpr(ld.Type)
	if err != nil {
		return err
	}
	c.writeLine(fmt.Sprintf("%s %s = %s;", cTyp, name, zeroInit))
	if lay, err := c.layoutOf(ld.Type); err == nil && (lay.IsZST || lay.LogicalZeroSized) {
		c.writeLine(fmt.Sprintf("(void)%s;", name))
	}
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

	markPlaceDynIndices := func(p tir.Place) {
		if p.Local != tir.InvalidLocal {
			fs.usedLocal[p.Local] = true
		}
		for _, pr := range p.Projections {
			if pr.Kind == tir.ProjDynIndex {
				if pr.IndexValue != tir.InvalidValue {
					fs.valUses[pr.IndexValue]++
				} else if pr.IndexLocal != tir.InvalidLocal {
					fs.usedLocal[pr.IndexLocal] = true
					fs.localRead[pr.IndexLocal] = true
				}
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
			markPlaceDynIndices(op.Place)
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

	// Pass 2: 统计被真正读取的值与局部槽位（跳过内联 println 字面串）
	arrRepDirectInit := make(map[tir.ValueID]int)
	for _, b := range fs.fn.Blocks {
		if b == nil || !fs.reachable[b.ID] {
			continue
		}
		for _, inst := range b.Instructions {
			switch inst.Kind {
			case tir.OpCopy, tir.OpMove, tir.OpLoad, tir.OpBorrow, tir.OpDrop, tir.OpCondDrop, tir.OpOwnershipTransfer:
				if inst.Place.Local != tir.InvalidLocal {
					fs.usedLocal[inst.Place.Local] = true
					fs.localRead[inst.Place.Local] = true
				}
				markPlaceDynIndices(inst.Place)
			case tir.OpUnary, tir.OpCast, tir.OpValueAssign:
				markOperand(inst.Src)
			case tir.OpInit, tir.OpAssign, tir.OpStore:
				if len(inst.Place.Projections) > 0 && inst.Place.Local != tir.InvalidLocal {
					fs.localRead[inst.Place.Local] = true
				}
				markPlaceDynIndices(inst.Place)
				markOperand(inst.Src)
				if inst.Kind == tir.OpInit && len(inst.Place.Projections) == 0 && inst.Src.Kind == tir.OperandValue {
					if _, isRep := fs.arrayRepeatVal[inst.Src.Value]; isRep {
						arrRepDirectInit[inst.Src.Value]++
					}
				}
			case tir.OpExtract:
				markOperand(inst.Src)
				if inst.ExtractProj.Kind == tir.ProjDynIndex && inst.ExtractProj.IndexValue != tir.InvalidValue {
					fs.valUses[inst.ExtractProj.IndexValue]++
				}
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
					markPlaceDynIndices(ap)
				}
			case tir.OpBuiltinCall:
				for _, a := range inst.Args {
					if inst.BuiltinKind == tir.BuiltinPrintln && a.Kind == tir.OperandValue && fs.hasStaticStrVal[a.Value] && !strings.ContainsRune(fs.staticStrVal[a.Value], 0) {
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

	// 单次直接初始化局部变量的 AggArrayRepeat 启用零拷贝原地展开（如 1MB 缓冲区 memset）
	for vid, cnt := range arrRepDirectInit {
		if cnt == 1 && fs.valUses[vid] == 1 {
			fs.inPlaceArrRep[vid] = true
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
		litSym := c.registerStringLit(inst.Const.StrVal)
		c.writeLine(fmt.Sprintf("%s = (_won_str_t){ .ptr = %s, .len = ((size_t)UINT64_C(%d)) };",
			valCTempName(inst.Dest), litSym, len(inst.Const.StrVal)))
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
		if inst.AggKind == tir.AggArrayRepeat && fs.inPlaceArrRep[inst.Dest] {
			return nil
		}
		return fs.emitAggregateConstruct(inst)

	case tir.OpCopy, tir.OpMove, tir.OpLoad:
		if inst.Dest == tir.InvalidValue || !fs.declaredVal[inst.Dest] {
			return nil
		}
		pExpr, isZST, isOmitted := fs.placeCExpr(inst.Place)
		if isZST || isOmitted {
			zTyp, err := c.typeCName(inst.Type, PosValue)
			if err != nil {
				return err
			}
			c.writeLine(fmt.Sprintf("%s = (%s){0};", valCTempName(inst.Dest), zTyp))
			return nil
		}
		c.writeLine(fmt.Sprintf("%s = %s;", valCTempName(inst.Dest), pExpr))
		return nil

	case tir.OpInit, tir.OpAssign, tir.OpStore:
		if inst.Src.Kind == tir.OperandValue && fs.inPlaceArrRep[inst.Src.Value] {
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
		pExpr, _, isOmitted := fs.placeCExpr(inst.Place)
		if isOmitted {
			c.writeLine(fmt.Sprintf("(void)(%s);", src))
			return nil
		}
		c.writeLine(fmt.Sprintf("%s = %s;", pExpr, src))
		return nil

	case tir.OpBorrow:
		if !fs.declaredVal[inst.Dest] {
			return nil
		}
		p := inst.Place
		// &[u8] .as_bytes() 别名与 &str 胖视图重借用：剥除尾部 ProjDeref 直接拷贝句柄/胖结构
		if len(p.Projections) >= 1 && p.Projections[len(p.Projections)-1].Kind == tir.ProjDeref {
			ti := c.tt.Info(inst.Type)
			if fs.sliceAliases[p.Local] || (ti.Kind == tir.TypeRef && c.tt.Info(ti.Elem).Kind == tir.TypeStr) {
				noDeref := p.Clone()
				noDeref.Projections = noDeref.Projections[:len(noDeref.Projections)-1]
				if len(noDeref.Projections) == 0 {
					c.writeLine(fmt.Sprintf("%s = %s;", valCTempName(inst.Dest), fs.localNames[p.Local]))
				} else {
					baseExpr, _, _ := fs.placeCExpr(noDeref)
					c.writeLine(fmt.Sprintf("%s = %s;", valCTempName(inst.Dest), baseExpr))
				}
				return nil
			}
		}
		pExpr, _, isOmitted := fs.placeCExpr(p)
		if isOmitted {
			c.needZSTDummy = true
			ptrC, err := c.typeCName(inst.Type, PosValue)
			if err != nil {
				return err
			}
			c.writeLine(fmt.Sprintf("%s = ((%s)(&(_won_zst_dummy)));", valCTempName(inst.Dest), ptrC))
			return nil
		}
		c.writeLine(fmt.Sprintf("%s = (&(%s));", valCTempName(inst.Dest), pExpr))
		return nil

	case tir.OpExtract:
		return fs.emitExtract(inst)

	case tir.OpCall:
		return fs.emitCall(inst, inst.CalleeName)

	case tir.OpForeignCall:
		return fs.emitForeignCall(inst)

	case tir.OpBuiltinCall:
		return fs.emitBuiltinCall(inst)

	case tir.OpOwnershipTransfer:
		// 若 @owned 移交承载原本的表达式寄存器目标，搬运句柄值供紧随的 OpForeignCall 使用；标志清理由 Drop 展开显式保证
		if inst.Dest != tir.InvalidValue && fs.declaredVal[inst.Dest] {
			pExpr, _, _ := fs.placeCExpr(inst.Place)
			c.writeLine(fmt.Sprintf("%s = %s;", valCTempName(inst.Dest), pExpr))
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

func (fs *fnEmitState) emitAggregateConstruct(inst tir.Instruction) error {
	c := fs.ctx
	lay, err := c.layoutOf(inst.Type)
	if err != nil {
		return err
	}
	evalUnusedItems := func() error {
		for _, it := range inst.AggItems {
			s, err := fs.operandCExpr(it.Value)
			if err != nil {
				return err
			}
			c.writeLine(fmt.Sprintf("(void)(%s);", s))
		}
		return nil
	}
	if inst.Dest == tir.InvalidValue || !fs.declaredVal[inst.Dest] {
		if lay.IsUninhabited {
			c.writeLine("abort();")
			return nil
		}
		return evalUnusedItems()
	}

	destVar := valCTempName(inst.Dest)
	switch inst.AggKind {
	case tir.AggStruct, tir.AggTuple:
		c.writeLine(fmt.Sprintf("%s = (%s){0};", destVar, lay.CName))
		for i, it := range inst.AggItems {
			valExpr, err := fs.operandCExpr(it.Value)
			if err != nil {
				return err
			}
			fIdx := it.FieldIndex
			if fIdx < 0 || fIdx >= len(lay.Fields) {
				if it.FieldName != "" {
					for fi, lf := range lay.Fields {
						if lf.Name == it.FieldName {
							fIdx = fi
							break
						}
					}
				} else if i < len(lay.Fields) {
					fIdx = i
				}
			}
			if fIdx < 0 || fIdx >= len(lay.Fields) || lay.Fields[fIdx].Omitted {
				c.writeLine(fmt.Sprintf("(void)(%s);", valExpr))
				continue
			}
			c.writeLine(fmt.Sprintf("%s.%s = %s;", destVar, lay.Fields[fIdx].CName, valExpr))
		}
		return nil

	case tir.AggArray:
		c.writeLine(fmt.Sprintf("%s = (%s){0};", destVar, lay.CName))
		for i, it := range inst.AggItems {
			valExpr, err := fs.operandCExpr(it.Value)
			if err != nil {
				return err
			}
			if lay.IsZST || i >= int(lay.ArrayLen) {
				c.writeLine(fmt.Sprintf("(void)(%s);", valExpr))
				continue
			}
			c.writeLine(fmt.Sprintf("%s.elems[%d] = %s;", destVar, i, valExpr))
		}
		return nil

	case tir.AggArrayRepeat:
		if len(inst.AggItems) != 1 {
			return newCapErr("WON-GEN-002", "定长数组初始化缺少重复元素操作数", inst.Span, "")
		}
		elemOp := inst.AggItems[0].Value
		if lay.IsZST || lay.ArrayLen == 0 {
			c.writeLine(fmt.Sprintf("%s = (%s){0};", destVar, lay.CName))
			if !fs.isZeroConstantOperand(elemOp) {
				elemExpr, err := fs.operandCExpr(elemOp)
				if err != nil {
					return err
				}
				c.writeLine(fmt.Sprintf("(void)(%s);", elemExpr))
			}
			return nil
		}
		if fs.isZeroConstantOperand(elemOp) {
			c.writeLine(fmt.Sprintf("memset(&(%s), 0, sizeof(%s));", destVar, destVar))
			return nil
		}
		elemExpr, err := fs.operandCExpr(elemOp)
		if err != nil {
			return err
		}
		elemC, err := c.typeCName(lay.ElemType, PosValue)
		if err != nil {
			return err
		}
		c.writeLine("{")
		c.indent++
		c.writeLine(fmt.Sprintf("%s _won_rep_val = %s;", elemC, elemExpr))
		c.writeLine(fmt.Sprintf("for (size_t _won_i = 0; _won_i < ((size_t)UINT64_C(%d)); ++_won_i) { %s.elems[_won_i] = _won_rep_val; }", lay.ArrayLen, destVar))
		c.indent--
		c.writeLine("}")
		return nil

	case tir.AggEnumVariant:
		if lay.IsUninhabited || inst.VariantIdx < 0 || inst.VariantIdx >= len(lay.Variants) {
			c.writeLine("abort();")
			return evalUnusedItems()
		}
		vl := lay.Variants[inst.VariantIdx]
		if lay.IsUnitOnlyEnum {
			c.writeLine(fmt.Sprintf("%s = ((%s)UINT64_C(%d));", destVar, lay.CName, vl.Discriminant))
			return evalUnusedItems()
		}
		c.writeLine(fmt.Sprintf("%s = (%s){0};", destVar, lay.CName))
		c.writeLine(fmt.Sprintf("%s.tag = ((%s)UINT64_C(%d));", destVar, lay.TagCType, vl.Discriminant))
		for i, it := range inst.AggItems {
			valExpr, err := fs.operandCExpr(it.Value)
			if err != nil {
				return err
			}
			fIdx := it.FieldIndex
			if fIdx < 0 || fIdx >= len(vl.Fields) {
				if it.FieldName != "" {
					for fi, lf := range vl.Fields {
						if lf.Name == it.FieldName {
							fIdx = fi
							break
						}
					}
				} else if i < len(vl.Fields) {
					fIdx = i
				}
			}
			if !vl.HasPayload || fIdx < 0 || fIdx >= len(vl.Fields) || vl.Fields[fIdx].Omitted {
				c.writeLine(fmt.Sprintf("(void)(%s);", valExpr))
				continue
			}
			c.writeLine(fmt.Sprintf("%s.payload.%s.%s = %s;", destVar, vl.CFieldName, vl.Fields[fIdx].CName, valExpr))
		}
		return nil

	case tir.AggInterpString:
		c.needStringRuntime = true
		c.needStringPushStr = true
		c.needEmpty = true
		c.writeLine(fmt.Sprintf("%s = (_won_string_t){ .ptr = (uint8_t*)_won_empty_bytes, .len = 0, .cap = 0 };", destVar))
		for _, it := range inst.AggItems {
			valExpr, err := fs.operandCExpr(it.Value)
			if err != nil {
				return err
			}
			ti := c.tt.Info(it.Value.Type)
			if it.Value.Kind == tir.OperandConst && it.Value.Const.Kind == tir.ConstString {
				litSym := c.registerStringLit(it.Value.Const.StrVal)
				c.writeLine(fmt.Sprintf("_won_string_push_str(&(%s), (_won_str_t){ .ptr = %s, .len = ((size_t)UINT64_C(%d)) });", destVar, litSym, len(it.Value.Const.StrVal)))
			} else if (ti.Kind == tir.TypeRef && c.tt.Info(ti.Elem).Kind == tir.TypeStr) || ti.Kind == tir.TypeStr {
				c.writeLine(fmt.Sprintf("_won_string_push_str(&(%s), %s);", destVar, valExpr))
			} else if ti.Kind == tir.TypeString {
				c.writeLine(fmt.Sprintf("_won_string_push_str(&(%s), (_won_str_t){ .ptr = ((%s).ptr), .len = ((%s).len) });", destVar, valExpr, valExpr))
			} else if ti.Kind == tir.TypeChar {
				c.needStringPushChar = true
				c.writeLine(fmt.Sprintf("_won_string_push_char(&(%s), (uint32_t)(%s));", destVar, valExpr))
			} else if ti.Kind == tir.TypeBool {
				c.writeLine(fmt.Sprintf("_won_string_push_str(&(%s), (%s) ? (_won_str_t){ .ptr = (const uint8_t*)\"true\", .len = 4 } : (_won_str_t){ .ptr = (const uint8_t*)\"false\", .len = 5 });", destVar, valExpr))
			} else if ti.Kind == tir.TypeInt {
				c.writeLine("{")
				c.indent++
				c.writeLine("char _tmp_buf[32];")
				if ti.Signed {
					c.writeLine(fmt.Sprintf("int _w = snprintf(_tmp_buf, sizeof(_tmp_buf), \"%%lld\", (long long)(%s));", valExpr))
				} else {
					c.writeLine(fmt.Sprintf("int _w = snprintf(_tmp_buf, sizeof(_tmp_buf), \"%%llu\", (unsigned long long)(%s));", valExpr))
				}
				c.writeLine("if (_w > 0) {")
				c.writeLine(fmt.Sprintf("    _won_string_push_str(&(%s), (_won_str_t){ .ptr = (const uint8_t*)_tmp_buf, .len = (size_t)_w });", destVar))
				c.writeLine("}")
				c.indent--
				c.writeLine("}")
			} else if ti.Kind == tir.TypeFloat {
				c.writeLine("{")
				c.indent++
				c.writeLine("char _tmp_buf[64];")
				c.writeLine(fmt.Sprintf("int _w = snprintf(_tmp_buf, sizeof(_tmp_buf), \"%%g\", (double)(%s));", valExpr))
				c.writeLine("if (_w > 0) {")
				c.writeLine(fmt.Sprintf("    _won_string_push_str(&(%s), (_won_str_t){ .ptr = (const uint8_t*)_tmp_buf, .len = (size_t)_w });", destVar))
				c.writeLine("}")
				c.indent--
				c.writeLine("}")
			} else {
				return newCapErr("WON-GEN-002", fmt.Sprintf("插值字符串不支持类型 '%s'", c.tt.Format(it.Value.Type)), inst.Span, "")
			}
		}
		return nil
	}
	return newCapErr("WON-GEN-002", fmt.Sprintf("不支持的聚合构造 %s", inst.AggKind), inst.Span, "")
}

func (fs *fnEmitState) emitExtract(inst tir.Instruction) error {
	c := fs.ctx
	if inst.ExtractProj.Kind == tir.ProjDowncast {
		fs.downcastVariant[inst.Dest] = inst.ExtractProj.VariantIndex
	}
	if !fs.declaredVal[inst.Dest] {
		return nil
	}
	srcExpr, err := fs.operandCExpr(inst.Src)
	if err != nil {
		return err
	}
	destVar := valCTempName(inst.Dest)
	srcBase := derefTypeIfRef(c.tt, inst.Src.Type)
	srcRefDeref := srcBase != inst.Src.Type
	srcValExpr := srcExpr
	if srcRefDeref {
		srcValExpr = fmt.Sprintf("(*(%s))", srcExpr)
	}

	switch inst.ExtractProj.Kind {
	case tir.ProjDeref:
		c.writeLine(fmt.Sprintf("%s = (*(%s));", destVar, srcExpr))
		return nil

	case tir.ProjDowncast:
		c.writeLine(fmt.Sprintf("%s = %s;", destVar, srcValExpr))
		return nil

	case tir.ProjField, tir.ProjTupleField:
		lay, err := c.layoutOf(srcBase)
		if err != nil {
			return err
		}
		var fl *FieldLayout
		vIdx, isDowncast := fs.downcastVariant[inst.Src.Value]
		if !isDowncast {
			vIdx = -1
		}
		var fieldPrefix string
		if lay.Kind == tir.TypeEnum && vIdx >= 0 && vIdx < len(lay.Variants) {
			vl := lay.Variants[vIdx]
			fl = findVariantFieldLayout(&vl, inst.ExtractProj)
			fieldPrefix = fmt.Sprintf(".payload.%s", vl.CFieldName)
		} else {
			fl = findStructOrTupleFieldLayout(lay, inst.ExtractProj)
		}
		if fl == nil || fl.Omitted {
			c.writeLine(fmt.Sprintf("(void)(%s);", srcValExpr))
			c.writeLine(fmt.Sprintf("%s = (_won_zst_t){0};", destVar))
			return nil
		}
		c.writeLine(fmt.Sprintf("%s = (%s)%s.%s;", destVar, srcValExpr, fieldPrefix, fl.CName))
		return nil

	case tir.ProjConstIndex:
		lay, err := c.layoutOf(srcBase)
		if err != nil {
			return err
		}
		if lay.IsZST {
			c.writeLine(fmt.Sprintf("(void)(%s);", srcValExpr))
			c.writeLine(fmt.Sprintf("%s = (_won_zst_t){0};", destVar))
			return nil
		}
		if inst.ExtractProj.ConstIndex < 0 || inst.ExtractProj.ConstIndex >= lay.ArrayLen {
			c.needBoundsS = true
			c.writeLine(fmt.Sprintf("%s = (%s).elems[_won_chk_idx_s(INT64_C(%d), ((size_t)UINT64_C(%d)))];",
				destVar, srcValExpr, inst.ExtractProj.ConstIndex, lay.ArrayLen))
			return nil
		}
		c.writeLine(fmt.Sprintf("%s = (%s).elems[UINT64_C(%d)];", destVar, srcValExpr, inst.ExtractProj.ConstIndex))
		return nil

	case tir.ProjDynIndex:
		lay, err := c.layoutOf(srcBase)
		if err != nil {
			return err
		}
		idxExpr, err := fs.dynIndexCExpr(inst.ExtractProj, lay.ArrayLen)
		if err != nil {
			return err
		}
		if lay.IsZST {
			c.writeLine(fmt.Sprintf("(void)(%s);", idxExpr))
			c.writeLine(fmt.Sprintf("(void)(%s);", srcValExpr))
			c.writeLine(fmt.Sprintf("%s = (_won_zst_t){0};", destVar))
			return nil
		}
		c.writeLine(fmt.Sprintf("%s = (%s).elems[%s];", destVar, srcValExpr, idxExpr))
		return nil

	default:
		return newCapErr("WON-GEN-002", fmt.Sprintf("成员抽取 (%v) 尚未实现", inst.ExtractProj.Kind), inst.Span, "")
	}
}

func (fs *fnEmitState) dynIndexCExpr(proj tir.Projection, arrLen int64) (string, error) {
	c := fs.ctx
	var rawExpr string
	var idxType tir.TypeID
	if proj.IndexValue != tir.InvalidValue {
		rawExpr = valCTempName(proj.IndexValue)
		for _, vd := range fs.fn.Values {
			if vd.ID == proj.IndexValue {
				idxType = vd.Type
				break
			}
		}
	} else if proj.IndexLocal != tir.InvalidLocal {
		rawExpr = fs.localNames[proj.IndexLocal]
		idxType = fs.localMap[proj.IndexLocal].Type
	} else {
		return "", newCapErr("WON-GEN-002", "动态数组下标缺少索引源操作数", tir.SourceSpan{}, "")
	}
	ti := c.tt.Info(idxType)
	if ti.Signed {
		c.needBoundsS = true
		return fmt.Sprintf("_won_chk_idx_s((int64_t)(%s), ((size_t)UINT64_C(%d)))", rawExpr, arrLen), nil
	}
	c.needBoundsU = true
	return fmt.Sprintf("_won_chk_idx_u((uint64_t)(%s), ((size_t)UINT64_C(%d)))", rawExpr, arrLen), nil
}

func findStructOrTupleFieldLayout(lay *TypeLayout, proj tir.Projection) *FieldLayout {
	if lay == nil {
		return nil
	}
	if proj.FieldName != "" {
		for i := range lay.Fields {
			if lay.Fields[i].Name == proj.FieldName {
				return &lay.Fields[i]
			}
		}
	}
	if proj.FieldIndex >= 0 && proj.FieldIndex < len(lay.Fields) {
		return &lay.Fields[proj.FieldIndex]
	}
	return nil
}

func findVariantFieldLayout(vl *VariantLayout, proj tir.Projection) *FieldLayout {
	if vl == nil {
		return nil
	}
	if proj.FieldName != "" {
		for i := range vl.Fields {
			if vl.Fields[i].Name == proj.FieldName {
				return &vl.Fields[i]
			}
		}
	}
	if proj.FieldIndex >= 0 && proj.FieldIndex < len(vl.Fields) {
		return &vl.Fields[proj.FieldIndex]
	}
	return nil
}

func (fs *fnEmitState) emitArrayInit(inst tir.Instruction) error {
	c := fs.ctx
	agg, ok := fs.arrayRepeatVal[inst.Src.Value]
	if !ok || len(agg.AggItems) != 1 {
		return newCapErr("WON-GEN-002", "定长数组初始化缺少重复元素操作数", inst.Span, "")
	}
	targetName, _, isOmitted := fs.placeCExpr(inst.Place)
	elemOp := agg.AggItems[0].Value
	if isOmitted {
		if !fs.isZeroConstantOperand(elemOp) {
			elemExpr, err := fs.operandCExpr(elemOp)
			if err != nil {
				return err
			}
			c.writeLine(fmt.Sprintf("(void)(%s);", elemExpr))
		}
		return nil
	}
	if fs.isZeroConstantOperand(elemOp) {
		c.writeLine(fmt.Sprintf("memset(&(%s), 0, sizeof(%s));", targetName, targetName))
		return nil
	}
	elemExpr, err := fs.operandCExpr(elemOp)
	if err != nil {
		return err
	}
	lay, err := c.layoutOf(inst.Type)
	if err != nil {
		return err
	}
	elemC, err := c.typeCName(lay.ElemType, PosValue)
	if err != nil {
		return err
	}
	c.writeLine("{")
	c.indent++
	c.writeLine(fmt.Sprintf("%s _won_rep_val = %s;", elemC, elemExpr))
	c.writeLine(fmt.Sprintf("for (size_t _won_i = 0; _won_i < ((size_t)UINT64_C(%d)); ++_won_i) { (%s).elems[_won_i] = _won_rep_val; }", agg.RepeatLen, targetName))
	c.indent--
	c.writeLine("}")
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
		return fs.emitPrintlnStmt(inst.Args, args)
	case tir.BuiltinBytesEqual:
		if len(args) != 3 {
			return newCapErr("WON-GEN-002", fmt.Sprintf("内置 bytes_equal 期望 3 个实参，实际收到 %d 个", len(args)), inst.Span, "")
		}
		expr = fmt.Sprintf("won_bytes_equal(%s, %s, %s)", args[0], args[1], args[2])
	case tir.BuiltinStringFrom:
		if len(args) != 1 {
			return newCapErr("WON-GEN-002", fmt.Sprintf("内置 String::from 期望 1 个实参，实际收到 %d 个", len(args)), inst.Span, "")
		}
		c.needStringFrom = true
		c.needStringRuntime = true
		c.needEmpty = true
		expr = fmt.Sprintf("_won_string_from(%s)", args[0])
	case tir.BuiltinPushStr:
		if len(args) != 2 {
			return newCapErr("WON-GEN-002", fmt.Sprintf("内置 push_str 期望 2 个实参，实际收到 %d 个", len(args)), inst.Span, "")
		}
		c.needStringPushStr = true
		c.needStringRuntime = true
		c.needEmpty = true
		recTi := c.tt.Info(inst.Args[0].Type)
		if recTi.Kind == tir.TypeRef && c.tt.Info(recTi.Elem).Kind == tir.TypeString {
			expr = fmt.Sprintf("_won_string_push_str(%s, %s)", args[0], args[1])
		} else {
			expr = fmt.Sprintf("_won_string_push_str(&(%s), %s)", args[0], args[1])
		}
	case tir.BuiltinCapacity:
		if len(args) != 1 {
			return newCapErr("WON-GEN-002", fmt.Sprintf("内置 capacity 期望 1 个实参，实际收到 %d 个", len(args)), inst.Span, "")
		}
		recTi := c.tt.Info(inst.Args[0].Type)
		if recTi.Kind == tir.TypeRef && c.tt.Info(recTi.Elem).Kind == tir.TypeString {
			expr = fmt.Sprintf("((size_t)((%s)->cap))", args[0])
		} else {
			expr = fmt.Sprintf("((size_t)((%s).cap))", args[0])
		}
	case tir.BuiltinAsStr:
		if len(args) != 1 {
			return newCapErr("WON-GEN-002", fmt.Sprintf("内置 as_str 期望 1 个实参，实际收到 %d 个", len(args)), inst.Span, "")
		}
		recTi := c.tt.Info(inst.Args[0].Type)
		if recTi.Kind == tir.TypeRef && c.tt.Info(recTi.Elem).Kind == tir.TypeString {
			expr = fmt.Sprintf("((_won_str_t){ .ptr = ((%s)->ptr), .len = ((%s)->len) })", args[0], args[0])
		} else {
			expr = fmt.Sprintf("((_won_str_t){ .ptr = ((%s).ptr), .len = ((%s).len) })", args[0], args[0])
		}
	case tir.BuiltinAsBytes:
		argTi := c.tt.Info(inst.Args[0].Type)
		if argTi.Kind == tir.TypeRef && c.tt.Info(argTi.Elem).Kind == tir.TypeStr {
			expr = fmt.Sprintf("((%s).ptr)", args[0])
		} else {
			expr = fmt.Sprintf("((const uint8_t*)(%s))", args[0])
		}
	case tir.BuiltinAsPtr:
		argTi := c.tt.Info(inst.Args[0].Type)
		if argTi.Kind == tir.TypeRef && c.tt.Info(argTi.Elem).Kind == tir.TypeStr {
			expr = fmt.Sprintf("((%s).ptr)", args[0])
		} else if argTi.Kind == tir.TypeString {
			expr = fmt.Sprintf("((const uint8_t*)((%s).ptr))", args[0])
		} else if argTi.Kind == tir.TypeRef && c.tt.Info(argTi.Elem).Kind == tir.TypeString {
			expr = fmt.Sprintf("((const uint8_t*)((%s)->ptr))", args[0])
		} else if argTi.Kind == tir.TypeRef && c.tt.Info(argTi.Elem).Kind == tir.TypeArray {
			arrLay, err := c.layoutOf(argTi.Elem)
			if err != nil {
				return err
			}
			elemC, err := c.typeCName(arrLay.ElemType, PosValue)
			if err != nil {
				return err
			}
			if arrLay.IsZST || arrLay.ArrayLen == 0 {
				c.needEmpty = true
				expr = fmt.Sprintf("(((void)(%s), ((const %s*)_won_empty_bytes)))", args[0], elemC)
			} else {
				expr = fmt.Sprintf("((const %s*)((%s)->elems))", elemC, args[0])
			}
		} else {
			expr = fmt.Sprintf("((const uint8_t*)(%s))", args[0])
		}
	case tir.BuiltinAsMutPtr:
		argTi := c.tt.Info(inst.Args[0].Type)
		if argTi.Kind == tir.TypeString {
			expr = fmt.Sprintf("((uint8_t*)((%s).ptr))", args[0])
		} else if argTi.Kind == tir.TypeRef && c.tt.Info(argTi.Elem).Kind == tir.TypeString {
			expr = fmt.Sprintf("((uint8_t*)((%s)->ptr))", args[0])
		} else if argTi.Kind == tir.TypeRef && c.tt.Info(argTi.Elem).Kind == tir.TypeArray {
			arrLay, err := c.layoutOf(argTi.Elem)
			if err != nil {
				return err
			}
			elemC, err := c.typeCName(arrLay.ElemType, PosValue)
			if err != nil {
				return err
			}
			if arrLay.IsZST || arrLay.ArrayLen == 0 {
				c.needZSTDummy = true
				expr = fmt.Sprintf("(((void)(%s), ((%s*)(&_won_zst_dummy))))", args[0], elemC)
			} else {
				expr = fmt.Sprintf("((%s*)((%s)->elems))", elemC, args[0])
			}
		} else {
			expr = fmt.Sprintf("((uint8_t*)(%s))", args[0])
		}
	case tir.BuiltinLen:
		argTi := c.tt.Info(inst.Args[0].Type)
		if argTi.Kind == tir.TypeRef && c.tt.Info(argTi.Elem).Kind == tir.TypeStr {
			expr = fmt.Sprintf("((%s).len)", args[0])
		} else if argTi.Kind == tir.TypeString {
			expr = fmt.Sprintf("((%s).len)", args[0])
		} else if argTi.Kind == tir.TypeRef && c.tt.Info(argTi.Elem).Kind == tir.TypeString {
			expr = fmt.Sprintf("((%s)->len)", args[0])
		} else {
			arrLen := c.tt.Info(argTi.Elem).ArrayLen
			expr = fmt.Sprintf("(((void)(%s), ((size_t)UINT64_C(%d))))", args[0], arrLen)
		}
	case tir.BuiltinIsEmpty:
		argTi := c.tt.Info(inst.Args[0].Type)
		if argTi.Kind == tir.TypeRef && c.tt.Info(argTi.Elem).Kind == tir.TypeStr {
			expr = fmt.Sprintf("(((%s).len) == 0)", args[0])
		} else if argTi.Kind == tir.TypeString {
			expr = fmt.Sprintf("(((%s).len) == 0)", args[0])
		} else if argTi.Kind == tir.TypeRef && c.tt.Info(argTi.Elem).Kind == tir.TypeString {
			expr = fmt.Sprintf("(((%s)->len) == 0)", args[0])
		} else {
			arrLen := c.tt.Info(argTi.Elem).ArrayLen
			isZero := "false"
			if arrLen == 0 {
				isZero = "true"
			}
			expr = fmt.Sprintf("(((void)(%s), (%s)))", args[0], isZero)
		}
	case tir.BuiltinTryUnwrap:
		return fs.emitTryUnwrapBuiltin(inst, args[0])
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

func (fs *fnEmitState) emitTryUnwrapBuiltin(inst tir.Instruction, srcExpr string) error {
	c := fs.ctx
	srcLay, err := c.layoutOf(inst.Args[0].Type)
	if err != nil {
		return err
	}
	if inst.Dest == tir.InvalidValue || !fs.declaredVal[inst.Dest] {
		c.writeLine(fmt.Sprintf("(void)(%s);", srcExpr))
		return nil
	}
	destVar := valCTempName(inst.Dest)

	if inst.CalleeName == "__try_propagate_err" {
		retLay, err := c.layoutOf(inst.Type)
		if err != nil {
			return err
		}
		if len(retLay.Variants) < 2 || len(srcLay.Variants) < 2 {
			return newCapErr("WON-GEN-002", "'?' 传播返回目标必须为 Option/Result 枚举", inst.Span, "")
		}
		retErr := retLay.Variants[1]
		srcErr := srcLay.Variants[1]
		if retLay.IsUnitOnlyEnum {
			c.writeLine(fmt.Sprintf("(void)(%s);", srcExpr))
			c.writeLine(fmt.Sprintf("%s = ((%s)UINT64_C(%d));", destVar, retLay.CName, retErr.Discriminant))
			return nil
		}
		c.writeLine(fmt.Sprintf("%s = (%s){0};", destVar, retLay.CName))
		c.writeLine(fmt.Sprintf("%s.tag = ((%s)UINT64_C(%d));", destVar, retLay.TagCType, retErr.Discriminant))
		if retErr.HasPayload && srcErr.HasPayload && len(retErr.Fields) > 0 && len(srcErr.Fields) > 0 && !retErr.Fields[0].Omitted && !srcErr.Fields[0].Omitted {
			c.writeLine(fmt.Sprintf("%s.payload.%s.%s = (%s).payload.%s.%s;",
				destVar, retErr.CFieldName, retErr.Fields[0].CName,
				srcExpr, srcErr.CFieldName, srcErr.Fields[0].CName))
		} else {
			c.writeLine(fmt.Sprintf("(void)(%s);", srcExpr))
		}
		return nil
	}

	// "__try_unwrap": 提取 Ok(T) / Some(T) 成功载荷 (VariantIdx = 0)
	if len(srcLay.Variants) < 1 {
		return newCapErr("WON-GEN-002", "'?' 解包操作数必须至少含 1 个变体", inst.Span, "")
	}
	okVar := srcLay.Variants[0]
	if !okVar.HasPayload || len(okVar.Fields) == 0 || okVar.Fields[0].Omitted {
		c.writeLine(fmt.Sprintf("(void)(%s);", srcExpr))
		c.writeLine(fmt.Sprintf("%s = (_won_zst_t){0};", destVar))
		return nil
	}
	c.writeLine(fmt.Sprintf("%s = (%s).payload.%s.%s;", destVar, srcExpr, okVar.CFieldName, okVar.Fields[0].CName))
	return nil
}

// emitPrintlnStmt 发射保持从左至右求值序与最终唯一换行的 println 语句。
// 对 &str / 字符串视图严格调用 _won_write_bytes(ptr, len) 进行定长字节流写入，
// 规避 printf("%.*s") 将 size_t 窄化为 int 产生的负精度及越界扫描 NUL 缺陷，完整支持嵌入 NUL 字节。
func (fs *fnEmitState) emitPrintlnStmt(argOps []tir.Operand, args []string) error {
	c := fs.ctx
	if len(args) == 0 {
		c.writeLine(`printf("\n");`)
		return nil
	}

	var curFmt strings.Builder
	var curArgs []string

	flushPrintf := func(appendNewline bool) {
		if curFmt.Len() == 0 && !appendNewline {
			return
		}
		if appendNewline {
			curFmt.WriteString("\\n")
		}
		fmtStr := curFmt.String()
		if len(curArgs) == 0 {
			c.writeLine(fmt.Sprintf(`printf("%s");`, fmtStr))
		} else {
			c.writeLine(fmt.Sprintf(`printf("%s", %s);`, fmtStr, strings.Join(curArgs, ", ")))
		}
		curFmt.Reset()
		curArgs = curArgs[:0]
	}

	for i, argOp := range argOps {
		// 无嵌入 NUL 字符的纯静态字符串字面量可直接合并至 C printf 格式串
		if argOp.Kind == tir.OperandConst && argOp.Const.Kind == tir.ConstString && !strings.ContainsRune(argOp.Const.StrVal, 0) {
			curFmt.WriteString(escapePrintfLiteral(argOp.Const.StrVal))
			continue
		}
		if argOp.Kind == tir.OperandValue && fs.hasStaticStrVal[argOp.Value] && !strings.ContainsRune(fs.staticStrVal[argOp.Value], 0) {
			curFmt.WriteString(escapePrintfLiteral(fs.staticStrVal[argOp.Value]))
			continue
		}

		ti := c.tt.Info(argOp.Type)
		if (ti.Kind == tir.TypeRef && c.tt.Info(ti.Elem).Kind == tir.TypeStr) || ti.Kind == tir.TypeStr || ti.Kind == tir.TypeString || (ti.Kind == tir.TypeRef && c.tt.Info(ti.Elem).Kind == tir.TypeString) {
			// 遇到字符串视图：先刷出此前累积的 printf 片段（不换行），随后使用定长 fwrite 发射字节流
			flushPrintf(false)

			var ptrExpr, lenExpr string
			if argOp.Kind == tir.OperandConst && argOp.Const.Kind == tir.ConstString {
				litSym := c.registerStringLit(argOp.Const.StrVal)
				ptrExpr = litSym
				lenExpr = fmt.Sprintf("((size_t)UINT64_C(%d))", len(argOp.Const.StrVal))
			} else if ti.Kind == tir.TypeRef && c.tt.Info(ti.Elem).Kind == tir.TypeString {
				ptrExpr = fmt.Sprintf("((%s)->ptr)", args[i])
				lenExpr = fmt.Sprintf("((%s)->len)", args[i])
			} else {
				ptrExpr = fmt.Sprintf("((%s).ptr)", args[i])
				lenExpr = fmt.Sprintf("((%s).len)", args[i])
			}
			c.writeLine(fmt.Sprintf("_won_write_bytes(%s, %s);", ptrExpr, lenExpr))
			c.needWriteBytes = true
			continue
		}

		// 标量及裸指针按 printf 安全类型说明符格式化
		switch {
		case ti.Kind == tir.TypeBool:
			curFmt.WriteString("%s")
			curArgs = append(curArgs, fmt.Sprintf("((%s) ? \"true\" : \"false\")", args[i]))
		case ti.Kind == tir.TypeChar:
			curFmt.WriteString("%u")
			curArgs = append(curArgs, fmt.Sprintf("(unsigned int)(%s)", args[i]))
		case ti.Kind == tir.TypeInt && !ti.Signed:
			curFmt.WriteString("%llu")
			curArgs = append(curArgs, fmt.Sprintf("(unsigned long long)(%s)", args[i]))
		case ti.Kind == tir.TypeInt && ti.Signed:
			curFmt.WriteString("%lld")
			curArgs = append(curArgs, fmt.Sprintf("(long long)(%s)", args[i]))
		case ti.Kind == tir.TypeFloat:
			curFmt.WriteString("%g")
			curArgs = append(curArgs, fmt.Sprintf("(double)(%s)", args[i]))
		case ti.Kind == tir.TypeRawPtr || ti.Kind == tir.TypeRef:
			curFmt.WriteString("%p")
			curArgs = append(curArgs, fmt.Sprintf("(const void*)(%s)", args[i]))
		default:
			curFmt.WriteString("%lld")
			curArgs = append(curArgs, fmt.Sprintf("(long long)(%s)", args[i]))
		}
	}

	// 最终刷出尾部格式串并附带整句唯一换行符
	flushPrintf(true)
	return nil
}

func (fs *fnEmitState) emitDrop(inst tir.Instruction) error {
	if _, ok := fs.glueMap[inst.GlueID]; !ok {
		return newCapErr("WON-GEN-002", fmt.Sprintf("Drop 指令引用未知胶水 g%d", inst.GlueID), inst.Span, "")
	}
	pExpr, _, isOmitted := fs.placeCExpr(inst.Place)
	if isOmitted {
		return nil
	}
	fs.ctx.writeLine(fmt.Sprintf("_won_drop_glue_%d(&(%s));", inst.GlueID, pExpr))
	return nil
}

func (fs *fnEmitState) emitCondDrop(inst tir.Instruction) error {
	if _, ok := fs.glueMap[inst.GlueID]; !ok {
		return newCapErr("WON-GEN-002", fmt.Sprintf("CondDrop 指令引用未知胶水 g%d", inst.GlueID), inst.Span, "")
	}
	dfName := dropFlagCVarName(inst.FlagID)
	pExpr, _, isOmitted := fs.placeCExpr(inst.Place)
	c := fs.ctx
	c.writeLine(fmt.Sprintf("if (%s) {", dfName))
	c.indent++
	if !isOmitted {
		c.writeLine(fmt.Sprintf("_won_drop_glue_%d(&(%s));", inst.GlueID, pExpr))
	}
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

	case tir.TermSwitch:
		return fs.emitSwitchTerminator(term)

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

func (fs *fnEmitState) emitSwitchTerminator(term tir.Terminator) error {
	c := fs.ctx
	condRaw, err := fs.operandCExpr(term.Cond)
	if err != nil {
		return err
	}
	condBaseType := derefTypeIfRef(c.tt, term.Cond.Type)
	condExpr := condRaw
	if condBaseType != term.Cond.Type {
		condExpr = fmt.Sprintf("(*(%s))", condRaw)
	}
	ci := c.tt.Info(condBaseType)

	if term.SwitchKind == tir.SwitchTry {
		lay, err := c.layoutOf(condBaseType)
		if err != nil || len(lay.Variants) < 2 {
			return newCapErr("WON-GEN-002", "'?' 错误传播分发缺少合法枚举布局", term.Span, "")
		}
		tagExpr := condExpr
		if !lay.IsUnitOnlyEnum {
			tagExpr = fmt.Sprintf("(%s).tag", condExpr)
		}
		var okTarget, errTarget tir.BlockID = tir.InvalidBlock, tir.InvalidBlock
		for _, sc := range term.Cases {
			if sc.Kind == tir.CaseTryOk {
				okTarget = sc.Target
			} else if sc.Kind == tir.CaseTryErr {
				errTarget = sc.Target
			}
		}
		if okTarget == tir.InvalidBlock {
			okTarget = term.DefaultBlk
		}
		if errTarget == tir.InvalidBlock {
			errTarget = term.DefaultBlk
		}
		c.writeLine(fmt.Sprintf("if ((%s) == ((%s)UINT64_C(%d))) goto won_bb_%d;",
			tagExpr, lay.TagCType, lay.Variants[0].Discriminant, okTarget))
		if errTarget != tir.InvalidBlock {
			c.writeLine(fmt.Sprintf("goto won_bb_%d;", errTarget))
		} else {
			c.writeLine("abort();")
		}
		return nil
	}

	var lay *TypeLayout
	if ci.Kind == tir.TypeEnum {
		lay, err = c.layoutOf(condBaseType)
		if err != nil {
			return err
		}
	}

	fallbackEmitted := false
	for _, sc := range term.Cases {
		switch sc.Kind {
		case tir.CaseEnumVariant:
			if lay == nil || sc.VariantIndex < 0 || sc.VariantIndex >= len(lay.Variants) {
				return newCapErr("WON-GEN-002", fmt.Sprintf("Switch 枚举变体索引 %d 非法", sc.VariantIndex), term.Span, "")
			}
			tagExpr := condExpr
			if !lay.IsUnitOnlyEnum {
				tagExpr = fmt.Sprintf("(%s).tag", condExpr)
			}
			c.writeLine(fmt.Sprintf("if ((%s) == ((%s)UINT64_C(%d))) goto won_bb_%d;",
				tagExpr, lay.TagCType, lay.Variants[sc.VariantIndex].Discriminant, sc.Target))

		case tir.CaseInt:
			var litStr string
			switch ci.Kind {
			case tir.TypeBool:
				if sc.IntValue != 0 {
					litStr = "true"
				} else {
					litStr = "false"
				}
			case tir.TypeChar:
				litStr = fmt.Sprintf("((uint32_t)UINT32_C(%d))", uint32(sc.IntValue))
			default:
				litStr, err = fs.formatIntLiteral(big.NewInt(sc.IntValue), condBaseType)
				if err != nil {
					return err
				}
			}
			c.writeLine(fmt.Sprintf("if ((%s) == (%s)) goto won_bb_%d;", condExpr, litStr, sc.Target))

		case tir.CasePatternArm:
			c.writeLine(fmt.Sprintf("(void)(%s);", condExpr))
			c.writeLine(fmt.Sprintf("goto won_bb_%d;", sc.Target))
			fallbackEmitted = true
		}
		if fallbackEmitted {
			break
		}
	}
	if !fallbackEmitted {
		if term.DefaultBlk != tir.InvalidBlock {
			c.writeLine(fmt.Sprintf("goto won_bb_%d;", term.DefaultBlk))
		} else {
			c.writeLine("abort();")
		}
	}
	return nil
}

func (fs *fnEmitState) placeCExpr(p tir.Place) (expr string, isZST bool, isOmitted bool) {
	c := fs.ctx
	expr = fs.localNames[p.Local]
	curType := p.RootType
	activeVariant := -1
	if fs.sliceAliases[p.Local] {
		return expr, false, false
	}

	for _, proj := range p.Projections {
		if proj.Kind != tir.ProjDeref && proj.Kind != tir.ProjDowncast {
			base := derefTypeIfRef(c.tt, curType)
			if base != curType {
				curType = base
				expr = fmt.Sprintf("(*(%s))", expr)
			}
		}
		switch proj.Kind {
		case tir.ProjDeref:
			ti := c.tt.Info(curType)
			if ti.Kind == tir.TypeRef || ti.Kind == tir.TypeRawPtr {
				curType = ti.Elem
			}
			expr = fmt.Sprintf("(*(%s))", expr)
			activeVariant = -1

		case tir.ProjDowncast:
			base := derefTypeIfRef(c.tt, curType)
			if base != curType {
				curType = base
				expr = fmt.Sprintf("(*(%s))", expr)
			}
			activeVariant = proj.VariantIndex

		case tir.ProjField, tir.ProjTupleField:
			lay, err := c.layoutOf(curType)
			if err != nil {
				return expr, false, false
			}
			var fl *FieldLayout
			fieldPrefix := ""
			if lay.Kind == tir.TypeEnum && activeVariant >= 0 && activeVariant < len(lay.Variants) {
				vl := lay.Variants[activeVariant]
				fl = findVariantFieldLayout(&vl, proj)
				fieldPrefix = fmt.Sprintf(".payload.%s", vl.CFieldName)
			} else {
				fl = findStructOrTupleFieldLayout(lay, proj)
			}
			activeVariant = -1
			if fl == nil || fl.Omitted {
				return expr, true, true
			}
			expr = fmt.Sprintf("((%s)%s.%s)", expr, fieldPrefix, fl.CName)
			curType = fl.Type

		case tir.ProjConstIndex:
			lay, err := c.layoutOf(curType)
			if err != nil {
				return expr, false, false
			}
			activeVariant = -1
			if lay.IsZST {
				return expr, true, true
			}
			if proj.ConstIndex < 0 || proj.ConstIndex >= lay.ArrayLen {
				c.needBoundsS = true
				expr = fmt.Sprintf("((%s).elems[_won_chk_idx_s(INT64_C(%d), ((size_t)UINT64_C(%d)))])", expr, proj.ConstIndex, lay.ArrayLen)
			} else {
				expr = fmt.Sprintf("((%s).elems[UINT64_C(%d)])", expr, proj.ConstIndex)
			}
			curType = lay.ElemType

		case tir.ProjDynIndex:
			lay, err := c.layoutOf(curType)
			if err != nil {
				return expr, false, false
			}
			activeVariant = -1
			idxExpr, err := fs.dynIndexCExpr(proj, lay.ArrayLen)
			if err != nil {
				return expr, false, false
			}
			if lay.IsZST {
				return expr, true, true
			}
			expr = fmt.Sprintf("((%s).elems[%s])", expr, idxExpr)
			curType = lay.ElemType
		}
	}

	if lay, err := c.layoutOf(curType); err == nil && (lay.IsZST || lay.LogicalZeroSized) {
		return expr, true, false
	}
	return expr, false, false
}

func (fs *fnEmitState) operandCExpr(op tir.Operand) (string, error) {
	switch op.Kind {
	case tir.OperandValue:
		return valCTempName(op.Value), nil
	case tir.OperandConst:
		return fs.constCExpr(op.Const, op.Type)
	case tir.OperandPlace:
		expr, isZST, isOmitted := fs.placeCExpr(op.Place)
		if isOmitted || isZST {
			cTyp, err := fs.ctx.typeCName(op.Type, PosValue)
			if err != nil {
				return "(_won_zst_t){0}", nil
			}
			return fmt.Sprintf("(%s){0}", cTyp), nil
		}
		return expr, nil
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

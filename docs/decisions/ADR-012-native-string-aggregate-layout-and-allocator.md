# ADR-012: Native String Representation, Aggregate Memory Layout & Allocator Policy

## Metadata
- **Decision ID**: ADR-012
- **Title**: Native `String` Representation, Fat `&str` Views, Aggregate Layout, Tagged Enums & Allocator Policy (Linux x86_64 LP64)
- **Status**: `PROPOSED` (Pending KitiLab Project Owner Approval — NOT yet ratified for code generation)
- **Date**: 2026-11-27
- **Author**: KitiLab Engineering
- **Builds Upon**: [ADR-001](ADR-001-c-codegen-backend.md), [ADR-003](ADR-003-ownership-lite-memory-model.md), [ADR-009](ADR-009-typed-intermediate-representation-architecture.md), [ADR-011](ADR-011-ownership-and-borrow-checking-semantics.md)
- **Technical Specification**: [Phase 2C.2 Native Layout & Resource Representation](../compiler/phase-2c.2-native-layout-and-resource-representation.md)

---

## Context

Following Phase 2C.1 (TIR-driven strict ISO C11 backend and native `GlueForeignResource` Drop execution), [`internal/codegen/c11`](../../compiler/poc/internal/codegen/c11/capabilities.go) still gates `String` (`GlueString`), general structs (`GlueStruct`), enums (`GlueEnum`), `Option`/`Result`, tuples (`GlueTuple`), resource-bearing arrays (`GlueArray`), and `&str.len()`/`==` with `WON-GEN-002`. Before removing those gates, KitiLab must lock down ABI, allocator, failure, ZST, and C-interop invariants in full conformance with [ADR-009](ADR-009-typed-intermediate-representation-architecture.md) and [ADR-011](ADR-011-ownership-and-borrow-checking-semantics.md).

---

## Considered Alternatives & Open Owner Decisions (`PROPOSED`)

### Decision Item D1: Heap Allocator & OOM Behavior for `String`
- **Alternative 1 (Inline raw `malloc`/`free`)**: Scatters libc dependencies through emitted functions.
- **Alternative 2 (Static C allocator shim `_won_alloc`/`_won_realloc`/`_won_free` wrapping libc `<stdlib.h>`, abort on OOM) — `PROPOSED`**: Keeps one allocator family, guarantees matched allocation/deallocation, avoids silent partial-string UB on OOM, and allows drop-in allocator replacement in one runtime translation unit later.
- *Requires Owner Decision*: Confirm OOM aborts (`stderr` + `abort()`) for infallible V1 `String` operations (`String::from`, `push_str`).

### Decision Item D2: Empty `String` Representation & Trailing NUL Policy
- **Alternative 1 (`ptr == NULL` when `cap == 0`, dynamic `cap + 1` trailing `\0`)**: Risks C11 UB if `NULL` flows into `memcpy(..., 0)` via `.as_ptr()`, and couples UTF-8 buffers to NUL sentinel allocation even though WON permits embedded `'\0'`.
- **Alternative 2 (`cap == 0` paired with static non-null empty sentinel `""`, no hidden `+1` NUL allocation) — `PROPOSED`**: Zero heap allocation on empty strings, non-null `.as_ptr()` invariant preserved, and single condition `if (s.cap > 0) _won_free(s.ptr, s.cap)` in `GlueString`. No SSO in V1 (avoids stack-pointer relocation hazards).

### Decision Item D3: `.len()` Return Type (`u64` vs `usize`)
- **Current Compiler Reality**: [`resolveBuiltinMethodSig`](../../compiler/poc/internal/checker/expressions.go#L1895) returns `u64`, whereas [`type-system.md`](../language-spec/type-system.md) models lengths/capacities as `usize`.
- **Proposed Resolution**: Align `.len()` / `.capacity()` to `usize` (`size_t` on LP64, matching `[T; N]` and `&[T]` indexing) once owner approves updating Phase 2A.2 / Phase 2C tests from `u64` to `usize` (or retain `u64` until cross-target work).

### Decision Item D4: Fat `&str` View Representation (`16B` vs `const char*`)
- **Current C11 Backend**: Maps `&str` to `const char*` (literally restricted).
- **Proposed Resolution**: Represent `&str` in C11 as `_won_str_t { const uint8_t* ptr; size_t len; }` (16B, align 8, `Copy`) while static string literals keep a static trailing `\0` in `.rodata` and C FFI `const char*` continues consuming `*const u8` (`.as_ptr()`) or explicit literal views inside `unsafe:`/`FFI`.

### Decision Item D5: Unannotated Struct Physical Layout Order (V1)
- **Invariant**: Source declaration order is **always** preserved for construction evaluation and `DropGlue` destruction order ([`ownership-and-memory.md`](../language-spec/ownership-and-memory.md), [`drop/classify.go`](../../compiler/poc/internal/drop/classify.go)).
- **Proposed Choice**: Allow C11 V1 backend to emit declaration-order fields initially for unannotated `struct` (`Option L1`) **or** alignment-descending reordering (`Option L2`), while keeping unannotated structs **non-C-ABI** across `OpForeignCall` boundaries in both cases ([`versioning-and-compatibility.md`](../language-spec/versioning-and-compatibility.md)). ISO C11 empty ZST structs/arrays elide ZST members cleanly or emit 1-byte dummy object placeholders so `-std=c11 -pedantic-errors` remains green.

### Decision Item D6: Tagged Union Enum Layout & Deferral of Niche Optimization
- **Proposed Resolution**: Concrete payload enums lower to deterministic `{ uint{8,16,32}_t tag; union { ... } payload; }` using `VariantIdx` (`Option`: `0=Some, 1=None`; `Result`: `0=Ok, 1=Err`); unit-only enums emit compact integer tags only. Dropping switches on `tag` and destroys **only** the active variant payload. Null-pointer/bool niche optimizations remain **explicitly deferred** beyond V1.

### Decision Item D7: `@repr(C)` & `@repr(packed)` Ratification Boundary
- Per [ADR-011](ADR-011-ownership-and-borrow-checking-semantics.md), `@repr(C)` and `@repr(packed)` remain rejected (`WON-SEM-010`) and are **not** activated by Phase 2C.2.0. Their syntax/FFI validation should follow in a scoped follow-up milestone (`Phase 2C.2.3`) after core WON structs and `String` pass full ASan/GCC/Clang verification.

---

## Proposed Decision Summary (Awaiting Approval)

**Adopt the simple, deterministic, leak-free V1 memory model specified in [`phase-2c.2-native-layout-and-resource-representation.md`](../compiler/phase-2c.2-native-layout-and-resource-representation.md): 24-byte `{ptr, len, cap}` `String` with matched C11 runtime allocator shims and static empty sentinel (`cap == 0`), 16-byte `{ptr, len}` fat `&str`, tagged-union enums without V1 niches, strict ZST elision for ISO C11 `-pedantic-errors`, and strict separation between internal WON layout and future `@repr(C)` FFI types.**

---

## Consequences
- **Positive**: Preserves target independence of `internal/tir` (ADR-009), keeps ADR-011 ownership/drop semantics intact without lifetime syntax, makes `GlueString` and `GlueEnum`/`GlueStruct`/`GlueTuple`/`GlueArray` lowering unambiguous and ASan-verifiable under strict `-std=c11 -pedantic-errors`.
- **Negative / Trade-Offs**: Deferring SSO and niche optimization leaves small stack overhead (24B `String`, `Option[&T]` 16B instead of 8B) in V1; transitioning `&str` from `const char*` to 16B fat struct updates C11 codegen mapping in Phase 2C.2.2.
- **Impact on Pillars**:
  - *Performance*: Zero GC, O(1) moves, exact heap allocation only when `cap > 0`.
  - *Memory Safety*: No drop of uninitialized/moved fields or inactive enum arms; ZSTs and empty buffers maintain non-null/C11 validity.
  - *C Interoperability*: Explicit FFI boundary prevents accidental ABI coupling to internal layouts.
  - *AI-Native Design*: Predictable, local value semantics without hidden allocator magic or niche-dependent aliasing traps.
  - *Simple Deployment*: Requires only ~60-100 lines of dependency-free C11 helper code linked directly into the emitted unit.

---

## Approval Gate

No code generation for `GlueString`, `GlueStruct`, `GlueEnum`, `GlueTuple`, `GlueArray`, or `@repr(...)` may be enabled in `compiler/poc/internal/codegen/c11` until KitiLab Project Owner explicitly ratifies Decisions **D1–D7** above.

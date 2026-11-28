# ADR-012: Native String Representation, Aggregate Memory Layout & Allocator Policy

## Metadata
- **Decision ID**: ADR-012
- **Title**: Native `String` Representation, Fat `&str` Views, Aggregate Layout, Tagged Enums & Allocator Policy (Linux x86_64 LP64)
- **Status**: `ACCEPTED` (KitiLab Owner Approval: November 28, 2026 — see [OWNER-APPROVAL-ADR-012-2026-11-28.md](OWNER-APPROVAL-ADR-012-2026-11-28.md))
- **Date**: 2026-11-28 (Proposed 2026-11-27 in Phase 2C.2.0; Accepted Nov 28, 2026)
- **Author**: KitiLab Engineering
- **Builds Upon**: [ADR-001](ADR-001-c-codegen-backend.md), [ADR-003](ADR-003-ownership-lite-memory-model.md), [ADR-009](ADR-009-typed-intermediate-representation-architecture.md), [ADR-011](ADR-011-ownership-and-borrow-checking-semantics.md)
- **Technical Specification**: [Phase 2C.2 Native Layout & Resource Representation](../compiler/phase-2c.2-native-layout-and-resource-representation.md)

---

## Context

Following Phase 2C.1 (TIR-driven strict ISO C11 backend and native `GlueForeignResource` Drop execution), [`internal/codegen/c11`](../../compiler/poc/internal/codegen/c11/capabilities.go) gated `String` (`GlueString`), general structs (`GlueStruct`), enums (`GlueEnum`), `Option`/`Result`, tuples (`GlueTuple`), resource-bearing arrays (`GlueArray`), and `&str.len()`/`==` with `WON-GEN-002`. Before enabling native representation and execution, WON must lock down target ABI, allocator, empty-buffer sentinel, ZST, enum discriminant, and C-interop invariants in strict compliance with [ADR-009](ADR-009-typed-intermediate-representation-architecture.md) and [ADR-011](ADR-011-ownership-and-borrow-checking-semantics.md).

---

## Considered Alternatives & Final Decisions (D1–D7)

### Decision D1: Libc-Backed Allocator Wrappers & OOM Policy — `APPROVED`
- **Alternative Considered**: Raw inline `malloc`/`free` scattered across functions, or custom allocator generic parameters (`String[A]`) / fallible runtime allocation returns in V1.
- **Approved Resolution**: Adopt the minimal single-family libc-backed runtime allocator interface:
  - `void* _won_alloc(size_t bytes)`: allocates `bytes` bytes from the WON allocator family.
  - `void* _won_realloc(void* ptr, size_t old_cap, size_t new_cap)`: resizes an existing WON heap buffer whose prior capacity is `old_cap` (or allocates fresh storage if transitioning from zero capacity `old_cap == 0`) to `new_cap` bytes. Parameter order MUST remain `(ptr, old_cap, new_cap)`.
  - `void _won_free(void* ptr, size_t cap)`: releases a WON heap allocation of capacity `cap` (no-op or strictly guarded never to receive `cap == 0`).
- **Mandatory Invariants**:
  - Use one allocator family for allocation, reallocation, and release.
  - Check all capacity and size arithmetic for unsigned `size_t` overflow before allocation.
  - Enforce minimum nonzero dynamic capacity of `16` bytes with deterministic geometric growth (initially `2x`).
  - Do NOT allocate hidden trailing NUL bytes (`cap + 1`) on dynamic `String` buffers.
  - On allocation failure in infallible V1 `String` operations, emit a fatal diagnostic and terminate via ISO C11 `abort()`. Fallible allocation APIs or custom allocator systems remain excluded from V1.

### Decision D2: Empty `String` Representation (`cap == 0` Non-Null Sentinel) — `APPROVED`
- **Alternative Considered**: Nullable `ptr == NULL` when `cap == 0`, or Small-String Optimization (SSO).
- **Approved Resolution**: An empty `String` uses a static non-null empty sentinel `_won_empty_bytes` with `len == 0` and `cap == 0` (zero heap allocation).
  - The sentinel is immutable read-only storage and MUST NEVER be written to or passed to `_won_free`.
  - Any mutating operation requiring heap storage when `cap == 0` MUST first allocate real writable owned memory via `_won_alloc` / `_won_realloc`.
  - Dynamic `String` does NOT promise trailing NUL termination (embedded `'\0'` is legal UTF-8).

### Decision D3: Length & Capacity Type (`usize`) — `APPROVED`
- **Alternative Considered**: Leaving `.len()` typed as `u64` in `internal/checker/expressions.go`.
- **Approved Resolution**: Both `.len()` and `.capacity()` MUST return `usize` (`size_t` on Linux x86_64 LP64), unifying the type checker, TIR mappings, C11 emission, and language specification ([`type-system.md`](../language-spec/type-system.md)). Byte-oriented UTF-8 indexing/counting semantics apply (`usize` counts bytes, not Unicode scalar values).

### Decision D4: 16-Byte Fat `&str` String Views & Explicit C Interop — `APPROVED`
- **Alternative Considered**: Retaining `const char*` as the general `&str` C11 representation.
- **Approved Resolution**: On the Linux x86_64 LP64 target, `&str` is represented as a 16-byte `Copy` two-word descriptor (`align == 8`):
  ```c
  struct {
      const uint8_t *ptr;
      size_t len;
  };
  ```
  - `&str` is NOT inherently a NUL-terminated C string.
  - Passing an arbitrary `&str` silently to an ordinary C `const char*` parameter MUST NOT be allowed; C interoperability remains explicit via raw pointers (`*const u8`) / `.as_ptr()` and validated FFI contracts.
  - Existing borrow provenance (`Prov(e)`, `Static` literals, NLL regions) remains strictly preserved.

### Decision D5: L2 Struct Physical Layout Order — `APPROVED (L2 Selected)`
- **Alternative Considered**: L1 (declaration-order physical fields for unannotated structs).
- **Approved Resolution**: Unannotated WON structs adopt **L2: descending-alignment physical field ordering**:
  1. Sort non-ZST fields by **descending target alignment**.
  2. Break equal-alignment ties deterministically by **ascending original declaration index** (stable sort, zero Go map iteration).
  3. Compute aligned byte offsets and final padded size using checked integer arithmetic.
- **Non-Negotiable Separation**: Language-level field declaration order, left-to-right literal construction evaluation order, logical field indices/access, and declaration-order struct field destruction (`DropGlue`) remain completely independent of L2 physical offsets. Backends MUST map logical `StructFieldType.Index` / `ProjField` to physical fields via the target layout table and MUST NEVER reorder semantic `Drop` events to match L2 physical order.
- **ZST Normalization**: Strict ISO C11 forbids empty structs and zero-length arrays; non-ZST composites elide ZST/`unit` fields physically, while standalone ZST composites lower to 1-byte `_won_zst_t` storage when addressable/materialized.

### Decision D6: Explicit Tagged Enum Representations (8/16/32/64-Bit Tags) & Optimization Deferrals — `APPROVED`
- **Approved Resolution**: Concrete enums use explicit deterministic tagged representations without V1 niche compression:
  - Supported unsigned discriminant integer types: `uint8_t`, `uint16_t`, `uint32_t`, and `uint64_t`.
  - Deterministically select the **smallest** valid unsigned width capable of representing the enum's maximum discriminant value (per language discriminant rules), failing closed if any discriminant cannot be represented in unsigned 64-bit.
  - Unit-only enums use the compact integer tag directly without an empty C `union`.
  - Payload-carrying enums emit `{ <tag_c_type> tag; union { ... } payload; }` incorporating computed variant payload alignments and layouts, accessing/dropping **only** the active variant.
  - Preserve `Option[T]` (`Some = 0`, `None = 1`) and `Result[T, E]` (`Ok = 0`, `Err = 1`).
- **Explicitly Deferred Beyond V1**: Small-string optimization (SSO), enum niche optimization, `Option[&T]` pointer niches, and `Option[String]` layout compression.

### Decision D7: `@repr(C)` & `@repr(packed)` Deferral — `APPROVED`
- Per [ADR-011](ADR-011-ownership-and-borrow-checking-semantics.md), both `@repr(C)` and `@repr(packed)` remain unsupported and fail-closed (`WON-SEM-010`).
- Unannotated WON structs MUST NOT be treated as C ABI-compatible in FFI signatures merely because the C11 backend lowers them to C structs. Enabling C-ABI attributes requires a separate future specification and approval milestone (`Phase 2C.2.3`).

---

## Decision Summary

**ADR-012 is `ACCEPTED` (November 28, 2026), ratifying D1–D7: libc-backed `_won_alloc`/`_won_realloc(ptr, old_cap, new_cap)`/`_won_free` wrappers aborting on OOM, non-null static empty `String` sentinel (`cap == 0`), `usize` `.len()`/`.capacity()`, 16-byte `{ptr, len}` fat `&str` with explicit C interop, L2 descending-alignment struct layout decoupled from declaration-order `Drop`, explicit 8/16/32/64-bit unsigned enum tags (with SSO and niches deferred), and continued deferral of `@repr(C)`/`@repr(packed)`.**

---

## Consequences
- **Positive**: Locks target ABI layout in the backend while keeping `internal/tir` target-independent (ADR-009), preserves ADR-011 ownership/destruction invariants cleanly, prevents C11 empty-struct/zero-array UB under `-pedantic-errors`, and unblocks staged implementation (`Phase 2C.2.1` aggregates + `&str`/`usize`; `Phase 2C.2.2` heap `String` allocator & `GlueString`).
- **Negative / Trade-Offs**: Deferring SSO and niche optimizations adds 1 tag word + padding to `Option[&T]` / `Option[String]` in V1; L2 physical reordering requires every C11 field/drop emission to translate logical indices through `TargetLayout`.
- **Approval vs Implementation Boundary**: Accepting ADR-012 authorizes staged `Phase 2C.2.1+` implementation; unimplemented constructs (such as heap `String` allocation and `GlueString` execution in `2C.2.1`) remain strictly gated behind `WON-GEN-002` until verified in code and CI.

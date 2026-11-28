# KitiLab Owner Architectural Approval Record — ADR-012 (2026-11-28)

> **Record ID**: OWNER-APPROVAL-ADR-012-2026-11-28  
> **Organization**: KitiLab  
> **Project**: WON Programming Language  
> **Approval Date**: November 28, 2026  
> **Scope**: Ratification of ADR-012 — Native `String` Representation, Aggregate Memory Layout & Allocator Policy (D1–D7), plus L2 Struct Ordering and 8/16/32/64-Bit Enum Tag Support  
> **Builds Upon**: [OWNER-APPROVAL-2026-11-24.md](OWNER-APPROVAL-2026-11-24.md) (`ADR-007`–`ADR-010`), [OWNER-APPROVAL-ADR-011-2026-11-25.md](OWNER-APPROVAL-ADR-011-2026-11-25.md) (`ADR-011`, preserved unchanged)

---

## 1. Executive Decision Summary

On **November 28, 2026**, the KitiLab project owner formally reviewed and approved **ADR-012 (`Native String Representation, Aggregate Memory Layout & Allocator Policy`)** and the Phase 2C.2.0 design specification, transitioning **ADR-012** from `PROPOSED` to **`ACCEPTED`** and authorizing **Phase 2C.2.1** (`Native Aggregate Layout & Execution Foundation`, followed by `Phase 2C.2.2` heap `String` allocation/destruction).

All seven open architectural decisions (**D1–D7**) were explicitly resolved by the owner, including **L2 descending-alignment struct layout** (with declaration-index tie-breaking) and **8/16/32/64-bit unsigned enum discriminant tag selection**.

---

## 2. Ratified Decisions (D1–D7)

### Decision D1 — Allocator Wrappers & OOM Policy
- **Status**: `APPROVED`
- Adopts libc-backed WON runtime wrappers `_won_alloc(size_t bytes)`, `_won_realloc(void* ptr, size_t old_cap, size_t new_cap)`, and `_won_free(void* ptr, size_t cap)` within a single allocator family.
- Requires overflow-checked allocation arithmetic, minimum nonzero capacity `16` bytes, initial `2x` geometric growth, zero hidden trailing NUL allocation on dynamic `String` buffers, and diagnostic + `abort()` on OOM for infallible V1 operations.

### Decision D2 — Empty `String` Representation
- **Status**: `APPROVED`
- Adopts static non-null empty sentinel `_won_empty_bytes` paired with `cap == 0` (zero heap allocation, never written to, never passed to `_won_free`). Mutating operations requiring storage first allocate writable owned memory.

### Decision D3 — Length & Capacity Semantics (`usize`)
- **Status**: `APPROVED`
- Both `.len()` and `.capacity()` MUST return `usize` (`size_t` on LP64), resolving the prior checker inconsistency (`u64`) and measuring UTF-8 byte lengths (not code points).

### Decision D4 — Fat String Views (`&str`) & C Interop
- **Status**: `APPROVED`
- Adopts the 16-byte `{ const uint8_t* ptr; size_t len; }` descriptor for `&str` on Linux x86_64 LP64. `&str` is not inherently NUL-terminated; arbitrary `&str` values MUST NOT silently pass to C `const char*` parameters.

### Decision D5 — L2 Struct Physical Layout
- **Status**: `APPROVED` (**L2 Selected**)
- Unannotated WON structs sort physical fields by **descending alignment**, breaking equal-alignment ties deterministically by **ascending original declaration index**.
- Language-level declaration order, construction evaluation order, logical field indices, and semantic field destruction (`Drop`) order remain independent of L2 physical offsets.

### Decision D6 — Tagged Enum Layout (8/16/32/64-Bit Tags) & Deferrals
- **Status**: `APPROVED` (Including **64-bit tag support**)
- Explicit tagged enum representations select the smallest valid unsigned integer tag width among `uint8_t`, `uint16_t`, `uint32_t`, and `uint64_t` able to represent the enum's maximum discriminant, failing closed if unrepresentable in unsigned 64-bit.
- Preserves `Option[T]` (`Some = 0`, `None = 1`) and `Result[T, E]` (`Ok = 0`, `Err = 1`).
- Explicitly defers Small-String Optimization (SSO), enum niche optimization, `Option[&T]` pointer niches, and `Option[String]` layout compression beyond V1.

### Decision D7 — `@repr(C)` and `@repr(packed)` Deferral
- **Status**: `APPROVED`
- Both `@repr(C)` and `@repr(packed)` remain unsupported and fail-closed (`WON-SEM-010`). Ordinary unannotated WON structs are NOT C ABI-compatible across FFI boundaries.

---

## 3. Approval vs. Implementation Verification Boundary

Per `AGENTS.md` and [OWNER-APPROVAL-ADR-011-2026-11-25.md](OWNER-APPROVAL-ADR-011-2026-11-25.md), accepting **ADR-012** ratifies the design decisions and authorizes staged implementation (**Phase 2C.2.1** native aggregate layout, execution, `&str`/`usize`, and supported aggregate `Drop`; followed by **Phase 2C.2.2** heap `String` allocation and `GlueString` native execution). Acceptance alone is NOT proof of implemented behavior; unimplemented paths remain fail-closed (`WON-GEN-002`) until coded and verified in CI.

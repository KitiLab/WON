# Phase 2C.2 — Native String & Aggregate Representation Specification

> **Document Status**: `SPECIFIED` (Phase 2C.2.0 Architectural & Memory-Representation Design; Pending KitiLab Owner Approval of [ADR-012](../decisions/ADR-012-native-string-aggregate-layout-and-allocator.md) Before Any Native Codegen Activation)  
> **Milestone**: Phase 2C.2.0 (Native `String` & Aggregate Layout, Drop Glue, and Target ABI Specification)  
> **Governing Baseline**: [ADR-001](../decisions/ADR-001-c-codegen-backend.md) (`ACCEPTED`), [ADR-003](../decisions/ADR-003-ownership-lite-memory-model.md) (`ACCEPTED`), [ADR-009](../decisions/ADR-009-typed-intermediate-representation-architecture.md) (`ACCEPTED`), [ADR-011](../decisions/ADR-011-ownership-and-borrow-checking-semantics.md) (`ACCEPTED`), [ADR-012 (Proposed)](../decisions/ADR-012-native-string-aggregate-layout-and-allocator.md) (`PROPOSED`)  
> **Baseline Codebase**: Commit `b6d45d8a3cde1b13aff7813df14c0013ca03cce4` (Phase 2C.1.1 — TIR Return Completeness & Fail-Closed C11 Emission)  
> **Invariant**: Native `GlueString`, `GlueStruct`, `GlueEnum`, `GlueTuple`, `GlueArray`, `@repr(...)`, and `TermSwitch` remain strictly gated by `WON-GEN-002` / `WON-SEM-010` until owner approval and staged implementation milestones complete.

---

## 1. Existing-State Audit

This audit records what is **currently established in code and accepted specifications** versus what remains **`PROPOSED`** or **`UNIMPLEMENTED`** across `compiler/poc`.

### 1.1 Compiler Pipeline & Capability Boundary
- Production `wonc build` executes: `lexer` -> `parser` -> `checker` (`TypedProgram`) -> `cfg` -> `borrow` -> `drop` -> `tir/lower` -> `tir.Verify` -> `internal/codegen/c11` -> GCC/Clang (`-std=c11 -pedantic-errors -Wall -Wextra -Werror`).
- [`internal/codegen/c11/target.go`](../../compiler/poc/internal/codegen/c11/target.go) certifies strictly `linux/x86_64` LP64 (`sizeof(void*) == 8 && sizeof(long) == 8`).
- [`internal/codegen/c11/capabilities.go`](../../compiler/poc/internal/codegen/c11/capabilities.go) enforces fail-closed `WON-GEN-002` gates rejecting:
  - `GlueString`, `GlueStruct`, `GlueEnum`, `GlueTuple`, `GlueArray` (`ValidateProgram`, `validateInstructionCaps`).
  - General structs, enums, tuples, `String`, `&mut str`, dynamic slices (`&[T]` except the narrow Phase 0B `let x = lit.as_bytes(); x.as_ptr()` pattern), non-`AggArrayRepeat` array/aggregate construction, non-`ProjDeref` projections in `OpExtract`/places, and `TermSwitch` (`match`, `for`, `?`).

### 1.2 Current Frontend, CFG, Drop, and TIR Inventory

| Construct | Frontend & Types (`types`, `symbols`, `checker`) | Ownership & Drop (`cfg`, `borrow`, `drop`) | Generic TIR (`internal/tir`) | Current C11 Backend (`internal/codegen/c11`) |
| :--- | :--- | :--- | :--- | :--- |
| **`String`** | `KindString`, `IsCopy=false`, `IsSized=true`; `String::from(&str)`, `InterpString`, `.len()->u64`, `.is_empty()->bool`, `.as_bytes()->&[u8]`, `.as_ptr()->*const u8`, `.as_mut_ptr()->*mut u8`, `.push_str(&str)->unit` ([`expressions.go`](../../compiler/poc/internal/checker/expressions.go)) | Tracked as Move-only owned resource (`NeedsDrop == true`, `GlueString`); move/reinit verified ([`classify.go`](../../compiler/poc/internal/drop/classify.go)) | `TypeString`, `BuiltinStringFrom`, `BuiltinPushStr`, `AggInterpString`, `GlueString` ([`types.go`](../../compiler/poc/internal/tir/types.go), [`ir.go`](../../compiler/poc/internal/tir/ir.go)) | Rejected with `WON-GEN-002` (no physical layout or heap runtime) |
| **`str` / `&str`** | `KindStr` DST (`WON-TYP-006` by value); `Ref(false, Str)` `IsCopy=true`, `Str` alias for `&str` ([`table.go`](../../compiler/poc/internal/symbols/table.go)) | String literals emit `OpStaticRef` (`Prov=Static`); `.as_bytes()` preserves origin ([`provenance.go`](../../compiler/poc/internal/borrow/provenance.go)) | `TypeStr`, `OpStaticRef`, `BuiltinAsBytes`, `BuiltinAsPtr` | **Mapped to `const char*`** ([`types.go`](../../compiler/poc/internal/codegen/c11/types.go)); `.len()`, `.is_empty()`, `==` gated (`WON-GEN-002`) |
| **Structs** | Ordered `Fields` (`IsVar` mutability, visibility); `@derive(Copy)` allowed on concrete all-`Copy` structs; `@repr(C)`/`@repr(packed)` rejected `WON-SEM-010` ([`declarations.go`](../../compiler/poc/internal/checker/declarations.go)) | Partial moves allowed (unless custom `Drop` -> `WON-MEM-007`); disjoint `&mut s.a` / `&mut s.b`; Drop executes in **declaration order** ([`elaborate.go`](../../compiler/poc/internal/drop/elaborate.go)) | `TypeStruct` + `Fields []StructFieldType` + `IsOpaqueForeign`; `OpAggregateConstruct(AggStruct)`, `ProjField`, `GlueStruct` | Opaque foreign FFI handles supported (`GlueForeignResource`); general `struct` rejected `WON-GEN-002` |
| **Tuples** | `KindTuple`; empty `()` collapses to `KindUnit`; `IsCopy` iff all elems `IsCopy` ([`relations.go`](../../compiler/poc/internal/types/relations.go)) | Indexed field moves & Drop flags supported in CFG/Drop; `GlueTuple` ordered `0..N-1` | `TypeTuple` (`Elems []TypeID`); `OpAggregateConstruct(AggTuple)`, `ProjTupleField`, `GlueTuple` | Rejected `WON-GEN-002` |
| **Fixed Arrays `[T; N]`** | `KindArray`, constant non-negative `Len`; `IsCopy` iff `Elem.IsCopy` | Disjoint constant index `[k]` vs conservative dynamic index `[i]` | `TypeArray` (`Elem`, `ArrayLen`); `AggArray`, `AggArrayRepeat`, `ProjConstIndex`/`DynIndex`, `GlueArray` | Local stack `T name[N]` via `[v; N]` for scalars only; param/return/indexing/resource arrays rejected `WON-GEN-002` |
| **Enums, `Option[T]`, `Result[T, E]`** | Ordered `Variants` (`VariantUnit`, `VariantTuple`, `VariantStruct`); `Option[T]` (`0=Some`, `1=None`); `Result[T, E]` (`0=Ok`, `1=Err`); match exhaustiveness ([`table.go`](../../compiler/poc/internal/symbols/table.go)) | Enum values tracked atomically (no field-split borrows/partial moves); `GlueEnum` per-variant cleanup | `TypeEnum` + `Variants []EnumVariantType`; `AggEnumVariant`, `ProjDowncast`, `TermSwitch`, `GlueEnum` | Rejected `WON-GEN-002` |
| **Aggregate `?` Partial Cleanup** | Typechecked in `checker` (`TryExpr` inside literals) | `lowerAggregateExprsWithCleanup` ([`builder.go`](../../compiler/poc/internal/cfg/builder.go)) hoists prior non-`Copy` fields before last `?` into temporary local scope for LIFO drop on early error return | Lowers as scoped `OpInit` / `OpMove` + `OpScopeExit` drops on `TermSwitch` error edge | Rejected `WON-GEN-002` |

### 1.3 Confirmed Spec-to-Code Discrepancies Requiring Reconciliation
1. **`.len()` Return Type (`u64` vs `usize`)**: [`type-system.md`](../language-spec/type-system.md) models `String`/slice lengths as `usize`, whereas [`resolveBuiltinMethodSig`](../../compiler/poc/internal/checker/expressions.go#L1895) currently returns `u64` (`UINT64_C(N)` in [`emitter.go`](../../compiler/poc/internal/codegen/c11/emitter.go#L1083)). Both are 64-bit unsigned on LP64, yet type identity differs (`u64 != usize`).
2. **`&str` Scalar `const char*` vs 16-Byte Fat Pointer**: [`type-system.md`](../language-spec/type-system.md) specifies `&str` as `(ptr: *const u8, len: usize)`, whereas current [`c11/types.go`](../../compiler/poc/internal/codegen/c11/types.go#L95) maps `&str` to `const char*`. Slices of `String` and interior `\0` views require `{ptr, len}`.
3. **Struct Field Order vs Destruction Order**: [`type-system.md`](../language-spec/type-system.md) and [`performance-and-execution.md`](../language-spec/performance-and-execution.md) permit alignment reordering for default structs, while [`ownership-and-memory.md`](../language-spec/ownership-and-memory.md) and [`drop/classify.go`](../../compiler/poc/internal/drop/classify.go#L317) mandate destruction in **declaration order**. Physical layout order MUST be decoupled from logical construction/destruction order.
4. **Escape Handling (`\xHH`) & Raw Mutability**: [`lexer.go`](../../compiler/poc/internal/lexer/lexer.go#L40) validates source UTF-8 (`WON-LEX-004`) and [`scanEscapeSeq`](../../compiler/poc/internal/lexer/lexer.go#L902) UTF-8-encodes `\xHH` via `WriteRune` (preserving valid UTF-8, including U+0000 `\0`/`\x00`). Meanwhile `var s: String; s.as_mut_ptr()` returns `*mut u8` outside `unsafe:` while dereference remains gated (`WON-SEC-002`).
5. **`@repr(C)` / `@repr(packed)`**: Rejected today with `WON-SEM-010` per [ADR-011](../decisions/ADR-011-ownership-and-borrow-checking-semantics.md).
6. **ISO C11 `-pedantic-errors` ZST Restrictions**: ISO C11 forbids empty structs (`struct {}`) and zero-length arrays (`T a[0]`).

---

## 2. `String` Logical Semantics

### 2.1 Established Semantics (`IMPLEMENTED` in Frontend / Ownership Passes)
- **Ownership & Copy**: `String` (`KindString` / `TypeString`) has `IsCopy == false`, `NeedsDrop == true`, and `IsSized == true` ([`types/relations.go`](../../compiler/poc/internal/types/relations.go), [`drop/classify.go`](../../compiler/poc/internal/drop/classify.go)). Assigning or passing `String` moves ownership (`OpMove`) and invalidates the source (`WON-MEM-001`).
- **Binding-Controlled Mutability**: `let s = String::from("a")` rejects `.push_str()` and `.as_mut_ptr()` (`WON-MUT-001`); `var s = ...` and `&mut String` permit mutation.
- **Role vs `str` / `&str`**: `str` is an unsized DST (`WON-TYP-006` by value); `&str` is an immutable `Copy` view (`NeedsDrop == false`) with borrow provenance; `String` owns heap storage (`GlueString`).
- **Existing Builtin Signature Surface**: `String::from(&str) -> String`, `InterpString -> String`, `.len() -> u64`, `.is_empty() -> bool`, `.as_bytes() -> &[u8]`, `.as_ptr() -> *const u8`, `.as_mut_ptr() -> *mut u8`, `.push_str(&str) -> unit`.

### 2.2 Proposed Logical Invariants (`PROPOSED` — Requires Spec Finalization)
1. **UTF-8 Byte Sequence**: Safe `String` and `&str` always store valid UTF-8 byte sequences; interior `'\0'` (`U+0000`) is legal UTF-8 data rather than a string terminator. Writing arbitrary non-UTF-8 bytes through `.as_mut_ptr()` in `unsafe:` breaks the UTF-8 contract at caller risk.
2. **Metric Unit**: `.len()` and future `.capacity()` count **bytes**, not Unicode scalar values or code units. `.is_empty()` is equivalent to `len == 0`. `.as_bytes()` returns `&[u8]` over the exact `[ptr, ptr + len)` byte slice.
3. **Raw Pointer Validity**:
   - `.as_ptr()` returns a **non-null** `*const u8` valid for reads of `len` bytes (`[ptr, ptr + len)`), including when `len == 0`.
   - `.as_mut_ptr()` returns a **non-null** `*mut u8` valid for reads of `len` bytes and writes within `[ptr, ptr + cap)` (no writes allowed when `cap == 0`).
   - Any mutating call (`push_str`), move, reassignment, or `Drop` of the owning `String` invalidates all previously extracted `*const u8` / `*mut u8` pointers (raw pointers are untracked `Copy` values).
4. **View Provenance During Move / Replace / Drop**: While any safe `&String`, `&str`, or `&[u8]` loan derived from `s` is live in `borrow`, moving, mutating, reassigning (`WON-MEM-006`), or dropping `s` (`WON-MEM-003`) is statically rejected. Safe views can never dangle.

---

## 3. `String` Physical Representation Proposal (`PROPOSED`)

### 3.1 Candidate Comparison

| Candidate | Layout (Linux x86_64 LP64) | Move & Borrow Impact | Drop Glue Complexity | C Interop & Safety | Verdict |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **A. Pointer + Length + Capacity (`{ptr, len, cap}`)** | 24 bytes, align 8 | Moves copy 24B bitwise without moving heap buffer; `&String -> &str` requires zero allocation | `if (cap > 0) _won_free(ptr, cap)` | Clean data pointer + explicit byte lengths | **RECOMMENDED V1** |
| **B. Pointer + Length + Out-of-Band Size** | 16B stack + 8B heap prefix (or `malloc_usable_size`) | Requires pointer arithmetic `ptr - 8` on realloc/free | Couples offset arithmetic to allocator internals | Non-standard in `-pedantic-errors`; error-prone | **REJECTED** |
| **C. Small-String Optimization (SSO)** | 24B tagged union (inline byte buffer + MSB/LSB tag) | Moving an SSO string invalidates inline pointers; double aliasing complexity for borrows & partial-init flags | Branchy inline-vs-heap checks in every method & destructor | Endianness-dependent bitpacking; high verification risk in V1 | **DEFERRED (Post-V1)** |

### 3.2 Recommended V1 Representation (`PROPOSED`)
```c
typedef struct {
    uint8_t* ptr;   /* offset 0,  size 8, align 8: always non-NULL */
    size_t   len;   /* offset 8,  size 8, align 8: active byte count (len <= cap) */
    size_t   cap;   /* offset 16, size 8, align 8: heap buffer capacity (0 => static sentinel, no heap free) */
} _won_string_t;
```
- **Total Size / Align**: 24 bytes, 8-byte aligned, zero internal or tail padding.
- **Empty Representation**: `.ptr = (uint8_t*)_won_empty_bytes` (static read-only 1-byte `"\0"` sentinel), `.len = 0`, `.cap = 0`. Zero heap allocation on empty `String`, non-null `.as_ptr()` guaranteed (avoiding C11 `memcpy(dst, NULL, 0)` UB), and `cap == 0` uniquely signals "not heap-allocated".

---

## 4. `String` Allocator, Minimal Runtime Interface & Drop Proposal (`PROPOSED`)

### 4.1 Allocator & Runtime Contract
- **Allocator Source & Family Matching**: V1 defines a tiny static/runtime allocator interface (`won_rt` / generated preamble ~60–100 LOC C11) wrapping ISO C11 `<stdlib.h>` `malloc`/`realloc`/`free`:
  - `void* _won_alloc(size_t bytes)`
  - `void* _won_realloc(void* ptr, size_t old_cap, size_t new_cap)`
  - `void _won_free(void* ptr, size_t cap)`
  - `_Noreturn void _won_panic_oom(size_t bytes)` / `_Noreturn void _won_panic_bounds(size_t idx, size_t len, const char* file, int line)`
- All `String` allocations and releases MUST belong strictly to this single allocator family. Mixing C `free()` with WON `String.ptr` or adopting external C `malloc` pointers directly as `String` without copying is **prohibited in V1**.
- **Alignment**: Guaranteed at least `_Alignof(max_align_t)` (16B on glibc x86_64 LP64), strictly satisfying `uint8_t` alignment (`1`).
- **Capacity Growth & Overflow**: Minimum non-zero allocation capacity `16` bytes; growth computes `new_cap = max(cap * 2, len + add_len)` with checked `size_t` overflow guards (`if (add_len > SIZE_MAX - len) abort()`).
- **Trailing NUL Policy**: Dynamic `String` does **NOT** allocate hidden `cap + 1` bytes for a trailing `\0` (interior `'\0'` is valid UTF-8, and `.len` tracks exact length).
- **Custom Allocators**: No per-instance allocator field is stored in `_won_string_t`; pluggable custom allocators remain a future global/link-time extension.
- **Allocation Failure Policy (`PROPOSED` Owner Decision)**: Default V1 policy prints a fatal OOM diagnostic to `stderr` and invokes ISO C11 `abort()`, aligning with `--panic=abort` in [`error-handling.md`](../language-spec/error-handling.md). Fallible `try_*` constructors may be added separately in future stdlib work.

### 4.2 `String` Drop Semantics (`GlueString`)
```c
static inline void _won_drop_string(_won_string_t* s) {
    if (s->cap > 0) {
        _won_free(s->ptr, s->cap);
        s->ptr = (uint8_t*)_won_empty_bytes;
        s->len = 0;
        s->cap = 0;
    }
}
```
- **Exact Pointer Freed**: Only `s->ptr` when `s->cap > 0`.
- **Moved Values**: Moved bindings lose Drop authority (`OpMove` suppresses `OpDrop` statically, or `OpDropFlagSet(..., false)` clears runtime `DropFlag` before `OpCondDrop`).
- **Reassignment (`s = expr`)**: Evaluates `expr` completely first; drops previous `s` via `OpDrop` / `OpCondDrop` immediately prior to `OpAssign`; writes new descriptor and sets `DropFlag = true` if flagged.
- **Nested & Partial Initialization**: Struct/tuple/array/enum containers holding `String` invoke `_won_drop_string(&field)` on active, initialized fields only.

---

## 5. String-View Semantics (`str` and `&str`)

- **Proposed V1 Representation (`PROPOSED`)**:
  ```c
  typedef struct {
      const uint8_t* ptr;  /* offset 0, size 8, align 8: non-NULL byte pointer */
      size_t         len;  /* offset 8, size 8, align 8: UTF-8 byte count */
  } _won_str_t;            /* sizeof == 16, alignof == 8 */
  ```
- **Literal Storage (`OpStaticRef`)**: Emits `static const uint8_t _won_lit_N[K+1] = { ..., 0 };` (including trailing static `0` for zero-copy C literal interop) and materializes `(_won_str_t){ .ptr = _won_lit_N, .len = K }` with `Static` provenance (no `'static` source syntax per [ADR-011](../decisions/ADR-011-ownership-and-borrow-checking-semantics.md)).
- **Borrowing from `String`**: Borrowing `String` as `&str` constructs `(_won_str_t){ .ptr = s->ptr, .len = s->len }` tied to `Loan(s)` in `borrow`; zero allocation and `NeedsDrop == false`.
- **Immutable vs Mutable Views**: `&str` is immutable and `Copy` (`sizeof == 16`), supporting `.len()`, `.is_empty()`, `.as_ptr()`, `.as_bytes()`, and content equality (`a.len == b.len && (a.len == 0 || memcmp(a.ptr, b.ptr, a.len) == 0)`). Mutable `&mut str` has no safe byte-mutation methods in V1 (`WON-GEN-002`).
- **Empty Views**: `(_won_str_t){ .ptr = _won_empty_bytes, .len = 0 }` — non-null sentinel, zero allocation.
- **Null Termination & C FFI**: General `&str` is **NOT** guaranteed NUL-terminated. Passing general `&str` implicitly to C `const char*` is **prohibited**; C FFI uses explicit `.as_ptr()` (`*const u8`) inside `unsafe:` / `.wonbind` `@borrow(param, length)` contracts, or literal-only `OpStaticRef` bridge rules.
- **Function Escapes & Aggregate Storage**: Governed identically by `borrow/provenance.go`: local `String` views cannot escape (`WON-MEM-003`); candidate input origins propagate via Strategy B; views stored in local structs track per-field `FieldProvs`.

---

## 6. Struct and Tuple Memory Layout

### 6.1 Three-Tier Layout Separation
1. **Language Guarantees**: Declaration order fixes initialization evaluation order, `StructFieldType.Index` / tuple index `0..N-1`, and field destruction order (`0..N-1`). No cross-version physical offset stability exists for unannotated `struct` or tuple types ([`versioning-and-compatibility.md`](../language-spec/versioning-and-compatibility.md)).
2. **Compiler-Internal V1 Layout (`PROPOSED`)**:
   - Realized per concrete monomorphized type as deterministic `typedef struct { ... } _won_s_<id>;` / `_won_tup_<id>;` inside `internal/codegen/c11` (`TargetLayout`).
   - Either declaration order (L1, simplest for initial `2C.2.1` bring-up) or descending-alignment reordering (L2, matching [`type-system.md`](../language-spec/type-system.md)) may be ratified in [ADR-012](../decisions/ADR-012-native-string-aggregate-layout-and-allocator.md); both MUST store logical `Index` separately so initialization and `Drop` always follow source declaration order.
3. **C ABI Compatibility Boundary**: An unannotated WON `struct` is **never** automatically C ABI-compatible even when emitted as a C11 `struct` and is rejected on by-value `OpForeignCall`. Explicit C layout attributes (`@repr(C)`, `@repr(packed)`) remain **UNRATIFIED (`WON-SEM-010`)** pending future approval ([ADR-011](../decisions/ADR-011-ownership-and-borrow-checking-semantics.md)).

### 6.2 Alignment, Padding, Offsets, ZSTs & Access
- **Alignment & Padding**: On LP64, non-ZST field `i` aligns at `align_up(cursor, align(f_i))`; struct alignment `A = max(1, max_i align(f_i))`; total size `align_up(end, A)`. Nested aggregates embed by value directly.
- **ZST / `unit` Policy for Strict ISO C11**:
  - `unit` / ZST fields inside a struct or tuple containing at least one non-ZST field are **omitted** from the physical C11 struct member list (expressions still evaluate for side effects).
  - Any standalone struct, tuple, or `[T; N]` containing **only ZSTs** lowers to a 1-byte dummy object `typedef struct { uint8_t _zst; } _won_zst_t;` when materialized as a local/parameter/addressable slot, preventing illegal empty C11 `struct {}` or `T[0]` under `-pedantic-errors` while preserving `unit -> void` for `PosReturn`.
- **Field & Tuple Access**: Lowers to member access `_won_lN.<c_field>` / `_won_lN._f<idx>` or `&(_won_lN.<c_field>)`. (Future `@repr(packed)` must reject `&packed.field` with `WON-TYP-005`.)

---

## 7. Enum and Tagged-Value Layout (`PROPOSED`)

Every concrete enum with `K` variants maps deterministically via `TargetLayout`:
1. **Uninhabited (`K == 0`)**: 1-byte placeholder C type if referenced; any construction emits `abort()`.
2. **Unit-Only Enum (all `K` variants have 0 non-ZST fields)**:
   - Physical type is solely the unsigned discriminant integer:
     - `K <= 256` -> `uint8_t` (size 1, align 1)
     - `256 < K <= 65536` -> `uint16_t` (size 2, align 2)
     - `K > 65536` -> `uint32_t` (size 4, align 4)
   - Avoids ISO C11 constraint errors from empty `union {}`.
   - Value equals declaration `VariantIdx` (`0..K-1`).
3. **Payload-Carrying Enum (at least one variant has >= 1 non-ZST field)**:
   ```c
   typedef struct {
       <tag_c_type> tag;
       union {
           struct { ... } v0;  /* omitted if variant 0 has 0 non-ZST fields */
           ...
       } payload;
   } _won_enum_<id>_t;
   ```
   - `VariantTuple` uses positional `_f0`, `_f1`, ...; `VariantStruct` uses named fields; ZST fields inside variants are elided cleanly.
   - Variant field declaration order within `struct { ... }` and alignment obey Section 6.
4. **Niche Optimization**: Explicitly **DEFERRED beyond V1** (`ADR-012` D6) to avoid premature aliasing, sentinel conflicts (empty `String` uses non-null sentinel), and partial-init tag ambiguity.

---

## 8. `Option[T]` and `Result[T, E]` Representation (`PROPOSED`)

Per [`symbols/table.go`](../../compiler/poc/internal/symbols/table.go#L121), variant indices are fixed:
- **`Option[T]`**:
  - `Some(T)`: `VariantIdx = 0` (`VariantTuple`, `[T]`)
  - `None`: `VariantIdx = 1` (`VariantUnit`)
  - Physical V1 C11 Layout: When `T` is non-ZST, `{ uint8_t tag; union { struct { C_T _f0; } v0; } payload; }` (e.g., `Option[i64]` -> 16 bytes, align 8; `Option[String]` -> 32 bytes, align 8). When `T` is ZST (`Option[unit]`), optimizes cleanly under Section 7 Rule 2 to `uint8_t tag;` (`1` byte) without any empty union.
- **`Result[T, E]`**:
  - `Ok(T)`: `VariantIdx = 0` (`VariantTuple`, `[T]`)
  - `Err(E)`: `VariantIdx = 1` (`VariantTuple`, `[E]`)
  - Physical V1 C11 Layout: When either `T` or `E` is non-ZST, `{ uint8_t tag; union { struct { C_T _f0; } v0; struct { C_E _f0; } v1; } payload; }` (omitting ZST variant structs). When both `T` and `E` are ZSTs, degrades cleanly to `uint8_t tag;`.

---

## 9. Aggregate Construction and `?` Failure Cleanup

1. **Left-to-Right Field Evaluation**: All aggregate constructors (`StructLiteral`, `TupleLiteral`, `ArrayLiteral`, `EnumVariant`) MUST evaluate component expressions left-to-right in source order.
2. **Scoped Partial-Construction Cleanup (`?` Propagation)**:
   - [`lowerAggregateExprsWithCleanup`](../../compiler/poc/internal/cfg/builder.go#L2778) already lowers partial aggregate construction failure across `?` by storing evaluated owned sub-expressions prior to the final `?` in temporary scoped locals (`__agg_tmp_k`) and releasing only initialized locals via `OpScopeExit` on the error return edge before building the aggregate on success.
   - **Required Hardening in `2C.2`**:
     - Replace `!IsCopy && !isRefCarryingType` heuristic with `drop.ClassifyContext.NeedsDrop` precision.
     - Extend cleanup hoisting to `CallEnumVariant` tuple calls (`Result::Ok`/`Enum::Tuple(a, b?)`), shorthand fields (`S { x, y: may_fail()? }`), and nested aggregate expressions `(S { a: str1 }, S { b: str2? })`.
     - Ensure target reassignment `x = Agg { ... may_fail()? ... }` evaluates and potentially aborts via `?` *before* dropping old `x` (so on `Err`/`None` propagation, old `x` drops normally at outer `OpScopeExit`, and constructed RHS prefixes drop via inner `tmpScope`).

---

## 10. Drop Glue Generation Requirements (`PROPOSED`)

`internal/codegen/c11` MUST generate deterministic static C helper functions from verified [`Program.DropGlues`](../../compiler/poc/internal/tir/ir.go#L445) (taking object pointers `_won_drop_glue_<id>(C_T* v)`) rather than inline macros:
- **`GlueString`**: Emits `_won_drop_string(v)`.
- **`GlueStruct`**: Iterates `g.Fields` **strictly in `Index` ascending order (`0..N-1`)**, invoking each child's `GlueID` helper on `&v-><field>`. Never iterates physical byte order.
- **`GlueTuple`**: Iterates `g.TupleElems` in `Index` order (`0..N-1`), invoking child `GlueID` on `&v->_f<idx>`.
- **`GlueArray`**: Loops `for (size_t _i = 0; _i < N; ++_i)` invoking element `GlueID` on `&v->elems[_i]` (or declaration/element index order matching [`drop/classify.go`](../../compiler/poc/internal/drop/classify.go#L22)).
- **`GlueEnum`**: `switch (v->tag)` dispatching **strictly on the active `Variant.Index`**:
  - Inactive union variants MUST NEVER have any destructor emitted or invoked.
  - Inside the active `case idx:`, drops `TupleElems` or `StructFields` in `Index` order (`0..M-1`).
- **`GlueForeignResource`**: Preserved untouched (calls `<release_fn>(*v)` per Phase 2C.1).
- **`OpDrop` / `OpCondDrop`**: Locals emit `_won_drop_glue_<id>(&place);`, nested conditionally moved places guard with `if (_won_dfN) { _won_drop_glue_<id>(&place); _won_dfN = false; }`.

---

## 11. Drop Flag and Partial-Move Behavior

- **Whole-Binding Conditional Lifetime (`MaybeInit`)**: `OpDropFlagDecl` declares stack `bool _won_dfK = false;`; `OpDropFlagSet` toggles on init (`true`), move/FFI transfer (`false`), reinit (`true`); `OpCondDrop` cleans up only when `_won_dfK == true` and resets it to `false` (loop-safe!).
- **Struct Partial Move (`s.a` Moved, `s.b` Live)**:
  - Non-custom-`Drop` structs: [`elaborate.go#L368`](../../compiler/poc/internal/drop/elaborate.go#L368) splits partial cleanup into per-field `OpDrop` / `OpCondDrop` along `Place.Projections = [ProjField]` in strict declaration order; moved fields (`_won_df_s_a == false`) are skipped cleanly.
  - Custom `impl Drop` structs: remain rejected (`WON-MEM-007`) for any partial field move.
  - Field Reinitialization (`s.a = new_str`): Drops previous `s.a` if `MaybeInit`, writes new field value, sets `_won_df_s_a = true`.
- **Enum Pattern Extraction (`match`)**: Enums do **not** allow partial field moves out while leaving the outer enum half-valid (`elaborate.go#L335`). Matching by move moves out payload fields (`OpMove` + `ProjDowncast`) and immediately clears the outer enum's Drop authority (`_won_df_enum = false`), preventing double-free on scope exit!
- **Variant-Active Partial Initialization**: Enum construction occurs atomically via temporaries/staging variables (`Section 9`); partially populated union variants never exist in live C memory.

---

## 12. C11 Target ABI Rules (`linux/x86_64` LP64)

- **Host Platform Scope**: Strictly Linux x86_64 LP64 (`Target.OS == "linux"`, `Arch == "x86_64"`, `PointerBits == 64`, `LongBits == 64`).
- **Primitive Mapping**: Already enforced in [`c11/types.go`](../../compiler/poc/internal/codegen/c11/types.go) (`bool`, `uint32_t` char, `<stdint.h>` exact-width ints, `ptrdiff_t`/`size_t`, `float`/`double`, excluding unportable `i128`/`u128` under ISO C11).
- **By-Value Array Wrapper**: Fixed arrays `[T; N]` containing resources or passed/returned/embedded inside structs & enums use `typedef struct { C_T elems[N]; } _won_arr_<id>;` (when `N > 0` and non-ZST) so arrays support C value assignment without pointer decay. (Existing scalar local buffer `[T; N]` optimization for `.as_ptr()`/`.as_mut_ptr()` accesses `&arr.elems[0]` or direct C array.)
- **Generated ABI Verification**: Emit `_Static_assert` checks validating pointer width (`8`), `long` width (`8`), `_won_string_t` (`sizeof == 24`, `_Alignof == 8`), `_won_str_t` (`sizeof == 16`, `_Alignof == 8`), and generated aggregate sizes/alignments where checked in compiler tests.

---

## 13. FFI Boundary Restrictions

- **Allowed Across `OpForeignCall` (`extern "C"`)**:
  - C11 scalars (`bool`, `i8..i64`, `u8..u64`, `isize`, `usize`, `f32`, `f64`).
  - Raw pointers (`*const T`, `*mut T`, `const void*`, `void*`) and validated scalar refs.
  - C string pointer `const char*` via explicit `*const u8` (`.as_ptr()`) or static literal `OpStaticRef` bridge (never arbitrary unsliced fat `&str`).
  - Validated `IsOpaqueForeign` handles with explicit `FFIContractCatalog` schema + header identity (`Phase 2B.3.2` + `2C.1`).
  - Future owner-ratified `@repr(C)` structs containing only FFI-safe fields.
- **Forbidden Across `OpForeignCall` (`WON-FFI-004` / `WON-GEN-002`)**:
  - `String` (managed by WON's `_won_alloc`/`_won_free` allocator family).
  - General fat `&str` (`_won_str_t`, not guaranteed NUL-terminated).
  - Unannotated `struct`, `enum`, `tuple`, `Option[T]`, `Result[T, E]`.
  - Uncontracted raw C calls outside `unsafe:` (`WON-SEC-001`).

---

## 14. Generic TIR Requirements (`internal/tir`)

Audit against [`internal/tir`](../../compiler/poc/internal/tir):
1. **Strict Backend Independence (`ADR-009`)**: Byte offsets, padding bytes, C type strings, and C headers MUST remain outside `internal/tir`; `TargetLayout` belongs strictly in `internal/codegen/c11` (or a dedicated backend target-layout package).
2. **ZST Classification Helper**: Add backend-neutral semantic `IsZST(id TypeID) bool` query helper on `*tir.TypeTable` (`unit`, empty tuples, empty structs, 0-length arrays, zero-field/all-ZST composites — guarded against cycles).
3. **Explicit `ProjDowncast` Payload Extraction**: Update `tir/lower` to emit `ProjDowncast(VariantIndex, VariantName)` systematically before `OpExtract` / `OpLoad` / `OpMove` inside `TermSwitch` (`match`) and `?` (`CaseTryOk`/`CaseTryErr`) arms so backends never infer active variant payload extracts from untagged outer values.
4. **Bounds-Checked Dynamic Indexing**: Formalize trap semantics on `ProjDynIndex` / dynamic slicing (`BuiltinBoundsCheck` or explicit runtime trap contract on `OpLoad`/`OpExtract`) preserving `TermUnreachable(IsTrap=true)`.
5. **Repr Constraint Metadata (Only If/When `@repr` Is Ratified)**: Semantic `ReprKind` enum (`ReprDefault`, `ReprC`, `ReprPacked`) on `TypeInfo` with zero target offsets/C syntax.

---

## 15. Unsupported Features (Remain Explicitly Gated)

After Phase 2C.2.0 and throughout V1 2C.2.x bring-up, the following remain rejected fail-closed (`WON-GEN-002` / `WON-SEM-010`):
- User-defined `impl Drop`, general `trait` definitions/bounds, generic `@derive(Copy)` (`T: Copy`).
- Custom per-container allocators (`String[A]`, `Arena[T]`), `Vec[T]`, `HashMap[K, V]`, `Shared[T]`, `Arc[T]` (unless specified cleanly in separate collection specs).
- Small-String Optimization (SSO) and enum niche optimization (`Option[&T]`).
- Mutable string view methods (`&mut str`).
- `@repr(C)` and `@repr(packed)` (until ratified).
- Structured concurrency (`scope` / `s.spawn`) & cross-target / LLP64 / ARM64 emission.

---

## 16. Alternatives Considered and Rejected

1. **Keep `&str` as Bare `const char*` Permanently**: Breaks zero-copy sub-slicing, O(1) `.len()`, and interior `\0` UTF-8 correctness.
2. **Use `cap + 1` Hidden `\0` Byte on `String`**: Still unsound on UTF-8 containing `'\0'`, complicates capacity accounting (`cap` vs allocated bytes), and obscures explicit C-string boundaries.
3. **Adopt SSO in V1**: Couples every move and borrow to inline-stack aliasing edge cases and endian bit-tagging before basic heap `String` emission works end-to-end.
4. **Enable Niche Optimization in V1**: Risks unsoundness across uninitialized union bytes, conditional drop flags, and non-null empty sentinels.
5. **Store Byte Sizes/Offsets Inside `internal/tir`**: Violates ADR-009 multi-backend decoupling (`C11` LP64 vs future targets/LLVM/QBE).

---

## 17. Performance and Memory Trade-Offs

- **`String` (24B stack, 0B heap if empty)**: `O(1)` 24-byte bitwise moves; zero allocation for `""`; `1` predictable branch on `Drop` (`cap > 0`). Slight memory overhead (16B more than single pointer) vs C `char*`, paid back by `O(1)` `.len()` and exponential amortized `push_str`.
- **`&str` (16B fat view, `Copy`)**: Registers pass `{ptr, len}` cleanly in two System V AMD64 registers (`%rdi, %rsi`); zero allocation and zero destructor overhead.
- **No V1 Niche (`Option[&T]` = 16B instead of 8B)**: Costs 8 bytes (1B tag + 7B align padding) in exchange for simple, auditable discriminant verification under ASan and GCC/Clang `-pedantic-errors`.
- **Inline Static Composites**: No boxing/heap headers on user structs, tuples, fixed arrays, or enums; stack locality preserved by default (`WON-PERF-001` still warns on >256B fixed-array copies).

---

## 18. Proposed Staged Implementation Roadmap

| Stage | Scope | Prerequisite Gate |
| :--- | :--- | :--- |
| **Phase 2C.2.0 (`Current`)** | Layout, representation, allocator, Drop, TIR, and C11 ABI specification + [ADR-012](../decisions/ADR-012-native-string-aggregate-layout-and-allocator.md) (`PROPOSED`) | Markdown & doc-integrity validation |
| **Phase 2C.2.1** | Backend `TargetLayout` (LP64 offsets/ZST rules), pure value structs, tuples, enums, `Option`, `Result`, `OpAggregateConstruct`, `OpExtract`, `TermSwitch` (`match`/`?`) | Owner approval of D5–D6 + `WON-GEN-002` preserved for `String`/resources |
| **Phase 2C.2.2** | Minimal `_won_alloc`/`_won_realloc`/`_won_free` C runtime helpers, 24B `String`, 16B `&str`, `GlueString`/`GlueStruct`/`GlueEnum`/`GlueTuple`/`GlueArray` Drop execution, hardened aggregate `?` cleanup | Owner approval of D1–D4 + ASan/LSan 0-leak verification under GCC & Clang |
| **Phase 2C.2.3** | `@repr(C)` / `@repr(packed)` parser + `WON-TYP-005` + C ABI struct FFI | Owner approval of D7 (separate FFI-struct milestone) |

---

## 19. Verification & Implementation Test Matrix (For Phase 2C.2.1–2C.2.2 Execution)

No codegen tests are invented in this spec-only milestone; future implementation stages MUST verify:
1. **`String`**: empty zero-alloc (`_won_empty_bytes` non-null `.as_ptr()`), growth across realloc boundaries, embedded `\0`, move without double-free, reassignment replacement, early-return/loop/conditional `DropFlag` under ASan/LSan.
2. **`&str`**: static literals, `&String -> &str`, `.len()`, `.is_empty()`, content `==`, `.as_bytes().as_ptr()`, FFI boundary safety.
3. **Structs & Tuples**: declaration-order Drop log verification, ZST elision & empty-struct `-pedantic-errors` checks, partial move + partial reinit, `WON-MEM-007` rejection for custom Drop.
4. **Enums / `Option` / `Result`**: unit-only enum (`uint8_t`), mixed payload enum, `Option[String]`, `Result[String, i32]`, inactive-variant non-drop proof, nested struct/enum `?` partial-initialization leak-freedom under ASan.
5. **Arrays**: `[String; N]`, `[unit; N]`, `[i32; 0]` under strict `-std=c11 -pedantic-errors -Wall -Wextra -Werror`, dynamic bounds trap.
6. **Fail-Closed Gates**: `WON-GEN-002` / `WON-INT-001` remain enforced for unratified targets, traits, custom `impl Drop`, and direct unannotated struct FFI calls.

---

## 20. Open Owner Decisions

Activation of native `String` and aggregate emission awaits explicit KitiLab Project Owner sign-off on **Decisions D1–D7** formalized in [ADR-012](../decisions/ADR-012-native-string-aggregate-layout-and-allocator.md):

1. **D1**: Approve libc-backed static C allocator shims (`_won_alloc` / `_won_realloc` / `_won_free`) with `stderr` + `abort()` on OOM for V1 infallible `String` operations.
2. **D2**: Approve `cap == 0` non-null static empty-string sentinel without hidden dynamic `cap + 1` trailing `\0` byte.
3. **D3**: Decide `.len()` return type alignment: keep `u64` or migrate to `usize` (`size_t`) per [`type-system.md`](../language-spec/type-system.md).
4. **D4**: Approve transitioning `&str` in C11 from `const char*` to 16-byte `{ptr, len}` while keeping explicit `*const u8` (`.as_ptr()`) / literal bridge at C FFI `const char*` sites.
5. **D5**: Choose V1 default unannotated struct physical layout policy: declaration order (`L1`) vs alignment-descending reordering (`L2`), preserving declaration-order `Drop` unconditionally.
6. **D6**: Ratify deferring SSO and enum niche optimization beyond V1 (`2C.2`).
7. **D7**: Confirm keeping `@repr(C)` and `@repr(packed)` gated (`WON-SEM-010`) until follow-up milestone `2C.2.3`.

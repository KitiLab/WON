# Phase 2C.1 — TIR-Driven C11 Backend & First Native Drop Execution

> **Document Status**: `IMPLEMENTED` (TIR-Driven ISO C11 Emitter `internal/codegen/c11`, TIR-Native `WON-GEN-002` Capability Validator, `wonc build` TIR Pipeline Migration, and Validated Opaque Foreign Resource Native Drop Execution)  
> **Milestone**: Phase 2C.1 (TIR-to-C11 Backend Transition & Initial Native RAII Execution)  
> **Governing Specification**: [ADR-001](../decisions/ADR-001-c-codegen-backend.md) (`ACCEPTED`), [ADR-009](../decisions/ADR-009-typed-intermediate-representation-architecture.md) (`ACCEPTED`), [ADR-011](../decisions/ADR-011-ownership-and-borrow-checking-semantics.md) (`ACCEPTED`), [compiler-and-ir-requirements.md](../language-spec/compiler-and-ir-requirements.md), [phase-2c.0-tir-foundation.md](phase-2c.0-tir-foundation.md), [phase-2a.2-backend-capability-matrix.md](phase-2a.2-backend-capability-matrix.md)  
> **Deferred Beyond Phase 2C.1**: Native C11 heap `String` representation/allocator contract, general user `struct`/`enum`/`match`/`?`/`for` layout & control-flow lowering (guarded fail-closed with `WON-GEN-002`), generic monomorphization instantiation, user `impl Drop` trait dispatch, and LLVM/QBE direct native backends.

---

## 1. Architectural Shift: Pure TIR Consumption

Phase 2C.1 retires the direct AST-to-C11 path (`internal/codegen`) from `wonc build` and replaces it with `compiler/poc/internal/codegen/c11`:

- **Zero Frontend Coupling**: `internal/codegen/c11` imports strictly Go standard-library packages and `internal/tir`. By Go package boundaries, `Emitter.Emit(*tir.Program)` cannot access AST nodes, symbol tables, parser structures, frontend `types.Context`, `cfg.FuncCFG`, `drop`, or legacy `codegen.Generator`.
- **Mandatory Structural Verification**: `ValidateProgram` runs `tir.Verify(prog)` before evaluating capabilities, failing closed with `WON-INT-001` on any malformed or uncertified IR, including any `TermReturn` in a non-`unit` function missing a typed `ReturnVal` operand (across both reachable and unreachable blocks). Unit returns preserve existing bare (`return;`) and pre-evaluated tail expression semantics.
- **No Zero-Return Synthesis & No Silent Fallback**: `Emitter` never synthesizes default `return 0;` / zero-initialized substitutes when a non-`unit` `TermReturn` lacks `ReturnVal` (defense-in-depth `WON-INT-001` failure before emitting partial C). `wonc build` and `wonc build --emit-c` always lower `TypedProgram` via `lower.LowerProgram` and emit C11 strictly through `c11.NewEmitter(c11.DefaultLinuxX86_64()).Emit(tirProg)`. Legacy `internal/codegen` is marked deprecated/reference-only for differential tests.

---

## 2. TIR-to-C11 Design (`internal/codegen/c11`)

### 2.1 Target ABI & Capability Validator (`target.go`, `types.go`, `capabilities.go`)
- **`Target` (`DefaultLinuxX86_64`)**: Validates `linux/x86_64` 64-bit LP64 configuration (`_Static_assert(sizeof(void*) == 8 && sizeof(long) == 8, ...)`). Non-LP64 targets reject fail-closed with `WON-GEN-002`.
- **`MapType` (Pure `*tir.TypeTable`)**: Maps C11 primitives (`bool`, `uint32_t` char, signed/unsigned `<stdint.h>` integers, `ptrdiff_t`/`size_t`, `float`/`double`), `unit` (strictly `PosReturn` -> `void`), C string view `&str` (`const char*`), scalar refs/raw pointers, `void*`/`const void*`, and contract-registered opaque foreign handles (`TypeInfo.IsOpaqueForeign == true` bound to a validated FFI namespace & header).
- **`ValidateProgram`**: Enforces all `WON-GEN-002` checks cleanly on verified TIR prior to emitting any C code, preserving `tir.SourceSpan` diagnostics in structured `CapabilityError`:
  - Rejects `i128`/`u128`, non-return `unit` or explicit `ConstUnit` literals (`return ()`), invalid `main` signatures, float `%`, unsupported `&str` equality/`.len()`, `String`, general structs/enums/tuples/dynamic slices, non-`AggArrayRepeat` aggregates, and multi-way `TermSwitch` (`match`, `for`, `?`).

### 2.2 Strict ISO C11 Emitter (`emitter.go`)
- **Headers**: Includes `<stdio.h>`, `<stdlib.h>`, `<stddef.h>`, `<stdint.h>`, `<stdbool.h>`, `<inttypes.h>`, `<string.h>`, plus deduplicated `ForeignHeaders` (`<zlib.h>`, `"demo.h"`) and uncovered `FFICatalogs` header paths.
- **Shadowing & Scope Safety**: Declares reachable locals as unique `_won_l<id>_<name>`, temporaries as `_won_t<id>`, drop flags as `bool _won_df<id>`, and reachable jump targets as `won_bb_<id>:;`. Unread value/local elimination prevents `-Werror=unused-but-set-variable` and `-Werror=unused-label` under GCC/Clang `-std=c11 -pedantic-errors -Wall -Wextra -Werror`.
- **Exact Integer Boundaries**: Folds unary `-` over single-use positive `OpConst` integers (e.g., `-9223372036854775808`) and emits `INT8_MIN`, `INT16_MIN`, `INT32_MIN`, `INT64_MIN`, `PTRDIFF_MIN`, and `INTN_C`/`UINTN_C` macros via `math/big.Int` range checks.
- **Control Flow & Builtins**: Emits `if (cond) goto won_bb_T; goto won_bb_E;`, `TermGoto`, `TermReturn`, and ISO C11 `abort();` for `TermUnreachable`. Lowers `println`, `won_bytes_equal` (on-demand), `.as_bytes()`, `.as_ptr()`, `.as_mut_ptr()`, array `.len()`, `[v; N]`, pointer deref reads/writes (`*p`), and explicit `as` casts.
- **Frontend Lowering Precision Fixes**:
  - `internal/cfg/builder.go`: Propagates `UnsafeExpr` block yields cleanly into `joinBV` for scalar results (fixing uninitialized `%5` register reads in `zlib_compression.won` TIR) and resolves LHS `*ptr` target element types in `extractPlace`.
  - `internal/tir/lower/lower.go`: Preserves `ForeignHeaders` in `tir.Program` and `Dest` operands on `OpOwnershipTransfer`.

---

## 3. First Native Drop Execution (`GlueForeignResource`)

Phase 2C.1 activates native C11 execution of deterministic Drop elaboration for **contract-validated opaque foreign resources** (`GlueForeignResource` with explicit `FFIContractCatalog` header identity and release function):

- **`OpDropFlagDecl` & `OpDropFlagSet`**: Allocates stack `bool _won_df<id>` initialized deterministically and updated on initialization, conditional moves, `@owned` transfers, and reinitializations.
- **`OpDrop`**: Emits direct `<release_c_symbol>(<place>);`.
- **`OpCondDrop`**: Emits:
  ```c
  if (_won_dfN) {
      <release_c_symbol>(<place>);
      _won_dfN = false;
  }
  ```
- **Contracted `@borrow` & `@owned` C ABI**:
  - `OpOwnershipTransfer` transfers ownership to the callee and clears drop flags without duplicate cleanup.
  - `@borrow` / `@borrow(mut)` foreign calls check `FFIContractDesc.Params[i].CType` pointer depth against synthesized `OpBorrow` wrappers so C prototypes expecting value handles vs pointers match strictly without C pointer-level mismatches.
- **Preserved Fail-Closed Scope**: `GlueString` (`String`) and general `GlueStruct`/`GlueEnum`/`GlueTuple`/`GlueArray` stay explicitly blocked with `WON-GEN-002` until an allocator/ABI contract ADR specifies their C runtime layout cleanly.

---

## 4. Verification Summary

- **`internal/codegen/c11`**: Unit, differential (vs reference AST emitter), strict GCC 16 & Clang 23 (`-std=c11 -pedantic-errors -Wall -Wextra -Werror`) compilation + binary execution (zlib byte-for-byte round-trip & real opaque resource counter tests proving normal exit, early return, conditional init, reassignment replacement, `@owned` transfer, `@borrow`, and loop Drop with zero leaks and zero double-frees), negative `WON-GEN-002` / `WON-INT-001` tests, and `FuzzC11Emitter`.
- **Full Compiler & CLI**: `gofmt`, `go vet ./...`, full `go test -count=1 ./...` across all 19 Go packages, CLI integration tests (`wonc parse`, `wonc check`, `wonc check --dump-tir`, `wonc build --emit-c`), Python unittest + Markdown link check.

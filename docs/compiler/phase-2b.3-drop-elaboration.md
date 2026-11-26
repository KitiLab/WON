# Phase 2B.3 — Deterministic Drop Elaboration, Resource Lifecycle & FFI Ownership Contracts

> **Document Status**: `IMPLEMENTED` (Stage A Resource Cleanup IR, Stage B Full CFG Drop Elaboration & Post-Elaboration Verification, Stage C Typed FFI Ownership Contract Integration)  
> **Milestone**: Phase 2B.3 (Ownership-Lite Destruction & Resource Lifecycle Completion)  
> **Governing Specification**: [ADR-003](../decisions/ADR-003-ownership-lite-memory-model.md) (`ACCEPTED`), [ADR-009](../decisions/ADR-009-typed-intermediate-representation-architecture.md) (`ACCEPTED`), [ADR-011](../decisions/ADR-011-ownership-and-borrow-checking-semantics.md) (`ACCEPTED`), [phase-2b.0-ownership-architecture.md](phase-2b.0-ownership-architecture.md), [ownership-and-borrowing.md](../language-spec/ownership-and-borrowing.md)  
> **Deferred Beyond Phase 2B.3**: Phase 2C ADR-009 TIR / expanded C11 destructor codegen (`WON-GEN-002` remains active for C11 subset), general `trait` coherence / user `impl Drop` dispatch, `.wonbind` source parser activation (`SPECIFIED`), and structured concurrency runtime (`scope`/`spawn`).

---

## 1. Purpose & Non-Negotiable Architecture

Phase 2B.3 converts the validated Phase 2B.1/2B.2 ownership, move, NLL borrow, and provenance results into an explicit, deterministic resource-cleanup plan (`drop.DropElaboratedProgram`) **without mutating the baseline `cfg.ProgramCFG`**.

### Key Separation: `!IsCopy` vs `NeedsDrop`
The compiler does **not** treat non-`Copy` types identically to resource-owning types:
- `&mut T` is non-`Copy`, yet `NeedsDrop(&mut T) == false` (exclusive references borrow memory and run no destructor).
- Fixed-size arrays/tuples/structs over pure `Copy` or non-dropping borrows yield `NeedsDrop == false`.
- `String`, structs/enums/tuples/arrays transitively containing `NeedsDrop` members, explicit `GlueForeignResource` FFI handles, registered extension types (`ResourceRegistry`), and reserved custom-Drop hooks yield `NeedsDrop == true`.

---

## 2. Three-Stage Implementation

### 2.1 Stage A — Resource Cleanup IR & Glue Taxonomy (`internal/cfg`, `internal/drop`)
- Added `OpDrop`, `OpCondDrop`, `OpFFITransfer`, `DropFlag`, and `DropGlue` statement fields in [cfg.go](../../compiler/poc/internal/cfg/cfg.go) (`ArgPlaces` added in Stage C for exact FFI argument tracking without altering Phase 2B.1/2B.2 snapshots).
- Implemented canonical resource classification & glue descriptors (`ClassifyContext.ResolveDropGlue`) in [classify.go](../../compiler/poc/internal/drop/classify.go):
  - `GlueString` (`String`)
  - `GlueStruct` (preserves strict **field declaration order** for structural teardown)
  - `GlueEnum` (active-variant payload teardown)
  - `GlueTuple` & `GlueArray`
  - `GlueForeignResource` (`ReleaseFn` bound via explicit FFI contract)
  - `GlueRegisteredResource` (extension hook for future standard-library containers without prematurely polluting Prelude with `Vec`/`Shared`/`Arc`)
  - `GlueCustomDropReserved` (extension hook enforcing `WON-MEM-007`)
- Added `DropFlagID`, `DropFlagOpKind` (`FlagDecl`, `FlagInit`, `FlagSetTrue`, `FlagSetFalse`, `FlagCheck`), `DropFlagMeta`, `DropFlagEvent`, `DropElaboratedProgram`, and immutable `CloneProgramCFG` in [ir.go](../../compiler/poc/internal/drop/ir.go).

### 2.2 Stage B — CFG Drop Elaboration, Partial Cleanup & Invariant Verification
Consumes Phase 2B.1's converged fixed-point states directly via `borrow.SolveMoveInitTrace` and `borrow.StepStatement` ([analyzer.go](../../compiler/poc/internal/borrow/analyzer.go)):
- **Lexical LIFO Exit (`elaborate.go`)**: Within every `OpScopeExit`, owned locals drop strictly in reverse declaration order *before* `OpStorageDead`. Owned by-value function parameters (`ScopeID == 0`) drop in reverse parameter order at function exit (`ScopeID == 1` exit or `TermReturn`).
- **Move & Return Exclusion**: Definitely `Uninit` or `Moved` places (including moved `return s` values) emit neither `OpDrop` nor redundant `DropFlagID`s.
- **Minimal Drop Flags**: Only `MaybeInit` places allocate `DropFlagID` (`OpCondDrop`), with deterministic `FlagDecl`, `FlagSetTrue`, `FlagSetFalse`, and `FlagCheck` events emitted for downstream ADR-009 TIR lowering.
- **Reassignment Replacement**: Reassigning an already-live non-`Copy` `var` binding emits `OpDrop` (when definitely `Init`) or `OpCondDrop` (when `MaybeInit`) *before* `OpAssign`. Reassigning after a move or uninitialized `var` emits no prior drop.
- **Partial Struct Move Cleanup**: When struct fields diverge via partial move, custom-Drop types reject with **`WON-MEM-007`**; non-custom-`Drop` structs emit per-field `OpDrop`/`OpCondDrop` in field declaration order without touching moved fields.
- **Partial Aggregate Construction (`?`)**: [builder.go](../../compiler/poc/internal/cfg/builder.go) tracks earlier resource fields in an internal scope when a trailing aggregate member contains `?`, ensuring `?` early returns drop already-evaluated fields while never touching trailing uninitialized fields.
- **Post-Elaboration Fail-Closed Verifier ([verify.go](../../compiler/poc/internal/drop/verify.go))**: Independently re-verifies structural and abstract lifecycle/flag invariants across the elaborated CFG (`WON-INT-001` on double drop, drop after move/uninit, drop after `OpStorageDead`, invalid glue/flag, or LIFO order violation).
- **Pipeline Gate**: [checker.go](../../compiler/poc/internal/checker/checker.go) sets `TypedProgram.DropElaborated` and gates `OwnershipChecked`, enforced in `wonc check` / `wonc build` ([main.go](../../compiler/poc/cmd/wonc/main.go)) and [codegen.go](../../compiler/poc/internal/codegen/codegen.go).

### 2.3 Stage C — Typed FFI Ownership Contract Integration (`internal/bindgen/contracts.go`, `internal/drop/ffi.go`)
- Defined `FFIContractCatalog`, `FFIFuncContract`, `FFIParamContract`, `FFIReturnContract`, and `FFIOwnershipKind` (`owned_transfer`, `borrow`, `borrow_mut`, `owned_return`, `opaque_handle`, `raw_unmanaged`, `unknown`) with strict fail-closed `Validate()` and JSON sidecar parser `ParseFFIContractJSON`.
- Integrated `ElaborateProgramWithFFI` into `checker.CheckProgram`:
  - `@owned` parameter transfers emit `OpFFITransfer` (suppressing local double drop, clearing conditional flags, and rejecting double FFI transfers with `WON-MEM-001`).
  - `@borrow` / `@borrow(mut)` calls verify live ownership without transferring caller responsibility.
  - `FFIOpaqueHandle` / `FFIOwnedReturn` bind explicit `GlueForeignResource(ReleaseFn)`.
  - Unknown / uncontracted C calls never guess `@owned` or destructors and remain strictly gated by **`WON-SEC-001`** outside `unsafe:`.
  - Textual `.wonbind` file parser syntax remains `SPECIFIED` / `Proposed` in [c-interop-and-unsafe.md](../language-spec/c-interop-and-unsafe.md).

---

## 3. Verification Summary

- Dedicated test suites: `internal/drop` unit/snapshot/invariant/FFI suites (`TestPhase2B3NeedsDropAndGlueClassification`, `TestPhase2B3DropIRCloningAndSnapshots`, `TestPhase2B3DeterministicDropElaborationCoreCases`, `TestPhase2B3CustomDropPartialMoveRejected`, `TestPhase2B3VerifierFailClosedInvariants`, `TestPhase2B3FFIContractSchemaValidation`, `TestPhase2B3FFIHandleLifecycleAndTransfer`, `TestPhase2B3UnknownFFIFailsClosedOutsideUnsafe`) and property fuzz target `FuzzDropElaboration` (`112,687+` execs, 0 failures).
- Regression preservation: All existing compiler packages (`bindgen`, `borrow`, `cfg`, `checker`, `codegen`, `lexer`, `parser`, `symbols`, `types`, `wonc`, `tests`), GCC/Clang `-std=c11 -pedantic-errors -Werror`, and Markdown link verification pass alongside `WON-GEN-002` backend gating.

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

### 2.3 Stage C & Phase 2B.3.1/2B.3.2 Closure — Typed FFI Ownership Contract Integration (`internal/bindgen/contracts.go`, `internal/borrow/ffi.go`, `internal/drop/ffi.go`)
- Defined `FFIContractCatalog`, `FFIFuncContract`, `FFIParamContract`, `FFIReturnContract`, and `FFIOwnershipKind` (`owned_transfer`, `borrow`, `borrow_mut`, `owned_return`, `opaque_handle`, `raw_unmanaged`, `unknown`) with strict fail-closed envelope and semantic validation (`ValidateEnvelope()`, `Validate()`), cross-catalog conflict detection (`IndexValidatedFFIContracts`), C-header arity & header-identity cross-checking (`ValidateBindingContracts`), and strict JSON sidecar parser `ParseFFIContractJSON` (`DisallowUnknownFields`, trailing-data rejection, explicit missing `param_index` rejection, and `borrows_param` defaulting to `-1` when omitted).
- **Schema Version & Header Identity Enforcement (Phase 2B.3.2)**:
  - Canonical schema constant `SupportedFFIContractSchemaVersion = "1.0"` is required identically across all catalogs; missing, empty, whitespace-padded, malformed, or unsupported schema versions fail closed in `ValidateEnvelope()` / `Validate()` before contract indexing or ownership/drop elaboration (`WON-INT-001`).
  - `CanonicalizeFFIHeaderPath` enforces purely lexical POSIX header-identifier validation and `path.Clean` normalization (no filesystem lookups or symlink resolution), rejecting empty/whitespace, padded, control/backslash/`<`/`>`/`"` characters, `..` traversal segments, trailing `/`, `.`, and `/`. Both `IndexValidatedFFIContracts` (across catalogs sharing a namespace) and `ValidateBindingContracts` (comparing `FFIContractCatalog.HeaderPath` against `BindingMeta.HeaderPath` cleanly, without collapsing relative paths onto absolute paths) fail closed on identity mismatches.
- Integrated FFI contracts directly into the authoritative CFG + Borrow + Drop dataflow (`borrow.AnalyzeProgramWithFFI` + `drop.ElaborateProgramWithFFI`):
  - `@owned` parameter transfers lower to `OpFFITransfer` on `stagedCFG` and are tracked across all reachable blocks, joins, and loop backedges in `internal/borrow` (`Move/Init`, `Liveness` `WON-MEM-006`, and `Drop` elaboration/verification) — preventing post-transfer use (`WON-MEM-001`), double transfer, and double caller-side drop while updating conditional `DropFlag`s cleanly.
  - `Copy` raw pointers and `FFIOpaqueHandle` copies (`CopyPlace`) are tracked as `CanBeNonOwningCopy` aliases in `PlaceState`, preserving the distinction between copying pointer bits and duplicating ownership (`@owned` transfer and `OpDrop` remain forbidden on copies, and copied aliases detect invalidation when the owner transfers).
  - `@borrow` / `@borrow(mut)` calls emit `OpRead` + `OpBorrowShared` / `OpBorrowMut` (without consuming caller ownership), enforcing exclusivity (`WON-MEM-004`/`005`), mutability (`WON-MUT-001`), and intra-call argument aliasing checks.
  - `BorrowsParam >= 0` integrates with `KindRef` (`&T` / `&mut T`) provenance in `internal/borrow/{loans,provenance}.go`, and explicitly rejects unsupported non-reference return-borrow relationships (`*const T`, scalars, opaque structs) with **`WON-SEM-010`**.
  - `FFIOpaqueHandle` / `FFIOwnedReturn` bind explicit `GlueForeignResource(ReleaseFn)`.
  - Unknown / uncontracted C calls never guess `@owned` or destructors and remain strictly gated by **`WON-SEC-001`** outside `unsafe:`.
  - Textual `.wonbind` file parser syntax remains `SPECIFIED` / `Proposed` in [c-interop-and-unsafe.md](../language-spec/c-interop-and-unsafe.md).

---

## 3. Verification Summary

- Dedicated test suites: `internal/bindgen` (`TestPhase2B3_2_SchemaVersionValidation`, `TestPhase2B3_2_HeaderIdentityValidation`), `internal/drop` unit/snapshot/invariant/FFI closure suites (`TestPhase2B3NeedsDropAndGlueClassification`, `TestPhase2B3DropIRCloningAndSnapshots`, `TestPhase2B3DeterministicDropElaborationCoreCases`, `TestPhase2B3CustomDropPartialMoveRejected`, `TestPhase2B3VerifierFailClosedInvariants`, `TestPhase2B3FFIContractSchemaValidation`, `TestPhase2B3FFIHandleLifecycleAndTransfer`, `TestPhase2B3UnknownFFIFailsClosedOutsideUnsafe`, `TestPhase2B3_1_OwnershipTransferAcrossBlocksJoinsLoops`, `TestPhase2B3_1_BorrowPreservationAndConflicts`, `TestPhase2B3_1_ContractValidationAndBorrowsParam`, `TestPhase2B3_2_PipelineEnvelopeAndHeaderFailClosed`) and property fuzz targets `FuzzDropElaboration`, `FuzzFFIContractJSON`, `FuzzFFIOwnership`, `FuzzOwnershipCFGAndDataflow`, and `FuzzBorrowNLLAndProvenance`.
- Regression preservation: All existing compiler packages (`bindgen`, `borrow`, `cfg`, `checker`, `codegen`, `lexer`, `parser`, `symbols`, `types`, `wonc`, `tests`), GCC/Clang `-std=c11 -pedantic-errors -Werror`, and Markdown link verification pass alongside `WON-GEN-002` backend gating.

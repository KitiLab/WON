# Phase 2B.2.2 — Borrow Authority, Parameter Provenance & Fail-Closed Invariant Closure

> **Document Status**: `IMPLEMENTED` (Verified in compiler unit, CLI, fuzz, and C11 suites; follows Phase 2B.2.1 baseline [`7d6b27b`](https://github.com/KitiLab/WON/commit/7d6b27ba0b67a1e762830685757b11365c696b31) CI run `45822885882`)  
> **Milestone**: Phase 2B.2.2 (Borrow Authority & Provenance Invariant Closure Prior to Phase 2B.3)  
> **Governing Specification**: [ADR-011](../decisions/ADR-011-ownership-and-borrow-checking-semantics.md) (`ACCEPTED`), [phase-2b.2-borrow-checker-nll-provenance.md](phase-2b.2-borrow-checker-nll-provenance.md), [phase-2b.2.1-mutability-and-provenance-closure.md](phase-2b.2.1-mutability-and-provenance-closure.md), [ownership-and-borrowing.md](../language-spec/ownership-and-borrowing.md)  
> **Scope Exclusions**: Phase 2B.3 (`Drop` elaboration, drop flags, `.wonbind` FFI resource cleanup), general `trait` / `impl Drop` / generic `Copy`, two-phase borrows, closures, and concurrency runtime remain deferred.

---

## 1. Core Safety Principle

> **Unknown or missing compiler metadata is never interpreted as permission to create an exclusive mutable reference (`&mut`), return a mutable reference, or certify mutable candidate disjointness.**

Phase 2B.2.2 closes six invariant gaps in `compiler/poc/internal/borrow` and `compiler/poc/cmd/wonc`:

### 1.1 Explicit Authority Model (`internal/borrow/authority.go`, `liveness.go`)

Replaced bare-`bool` parameter/carrier authority checks with the explicit `AccessAuthority` model and `authorityCheckOutcome` separating internal metadata corruption (`WON-INT-001`) from language-level borrow violations (`WON-MEM-004`):

- **`AuthorityMutable`**: Proven exclusive `&mut T` access authority across the root parameter and every projection step.
- **`AuthorityShared`**: Proven shared/read-only `&T` or `*const T` access anywhere on the path (`Static` sources also downgrade to `AuthorityShared` when `&mut` is requested) -> rejects with **`WON-MEM-004`**.
- **`AuthorityUnsafeRawPtr`**: Explicit `*mut T` raw-pointer-derived access inside permitted `unsafe:` boundaries; grants mutable dereference capability while keeping underlying alias/provenance sets conservatively `Unknown` (never treated as disjoint from other active/candidate places, and rejected with `WON-MEM-003` if returned as a safe reference). Note: **unsafe raw-pointer safety itself is not statically proven**.
- **`AuthorityUnknown`**: Missing `FuncSym`, nil `types.Context`, out-of-range effective parameter index, `types.Invalid` type, unknown `ProjectionKind`, missing struct/field/tuple/index metadata, or type mismatch -> fails closed with **`WON-INT-001`** and prevents `OwnershipChecked` from becoming `true`.

Every projection layer (`ProjDeref`, `ProjField`, `ProjConstIdx`, `ProjIndex`, `ProjTupleField`, including generic struct substitutions) is validated step by step against the current resolved `TypeID`.

### 1.2 Parent Loan Identity & Carrier Origin Validation (`loans.go`, `authority.go`, `liveness.go`)

- Added `LoanInfo.Type` (`types.TypeID`) recorded on every `OpBorrowShared`, `OpBorrowMut`, and `OpReborrow` statement (with `cfg/builder.go` `MethodCallExpr` fixed to emit `&T`/`&mut T` rather than bare referent `T`).
- Added `validateParentLoansIdentity` covering every reborrow (`OpReborrow`): rejects `InvalidLoan` (`0`), missing `LoanByID` entries, cross-function `FuncSymID` mismatches, invalid `BorrowKind` or non-ref/non-ptr parent `Type`, carrier inconsistency, cycles in `ParentLoans`, and non-preceding creation points in reachable CFG flow with **`WON-INT-001`**; rejects shared parent -> mutable child with **`WON-MEM-004`**.
- Added `validateCarrierOriginSet` validating `Loans`, `Params`, `Static`, `Unknown`, and non-empty carrier invariants on reborrows and dereferenced writes (`OpAssign`/`OpReinit` with `HasDeref()`, preserving explicit `*mut T` dereference writes within `unsafe:` while failing closed on empty/unbacked `Unknown` safe origins).

### 1.3 Mutable Return Provenance, Call-Site Disjointness & Opaque Boundaries (`provenance.go`, `wonc`)

- **`checkReturnOriginsValid`**: Fails closed with **`WON-INT-001`** whenever `FuncSym` or `tctx` is missing, `pIdx` is out of `EffectiveParamCount()` bounds, `EffectiveParamType` is `Invalid`, or referenced `LoanID`s are missing/foreign; rejects `&mut` return from `&T` or `Static` with **`WON-MEM-004`**, and rejects safe `&mut` return from raw pointers, locals, or `Unknown` with **`WON-MEM-003`** independently per mutable aggregate field.
- **`checkCallProvenanceAndDisjointness`**: Validates every summary candidate `pIdx` against `Callee.EffectiveParamCount()` and `ArgTemps`, rejects missing `ValueID`, empty caller `OriginSet`, or empty resolved targets with **`WON-INT-001`** (preventing `targetsOverlap(nil, nil) == false` from certifying safety), enforces `&mut` authority on mutable candidates, treats `Unknown` / `*mut`-derived aliases as conservatively overlapping (`WON-MEM-005`), and preserves cross-field `&` vs `&mut` overlap checks (`WON-MEM-004`/`005`).
- **`checkOpaqueSignatures`**: Emits **`WON-INT-001`** on bodyless/fn-ptr signatures with `Invalid` return or parameter types, `WON-SEM-010` on unprojectable ref aggregates, `WON-MEM-002` on 0-ref (including zero-arg bodyless/`fn()` returning `&str`) or `>=2`-ref opaque signatures, and `WON-MEM-004` on single shared input returning `&mut`.
- **`cmd/wonc`**: Both `wonc check` and `wonc build` enforce `enforceOwnershipCheckedGate` (`!OwnershipChecked -> WON-INT-001` before check success or C11 codegen, leaving zero `.won.c` artifacts on failure).

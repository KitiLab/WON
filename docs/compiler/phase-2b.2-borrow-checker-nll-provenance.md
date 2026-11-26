# Phase 2B.2 — Borrow Checker, Non-Lexical Lifetimes & Candidate-Origin Reference Provenance

> **Document Status**: `IMPLEMENTED` (Phase 2B.2 verified in CI [`9d2cda4`](https://github.com/KitiLab/WON/commit/9d2cda4811a81c4ed80f634597c73dd535a8954e), run `45814423072`; hardened in [Phase 2B.2.1](phase-2b.2.1-mutability-and-provenance-closure.md) and [Phase 2B.2.2](phase-2b.2.2-authority-and-invariant-closure.md))  
> **Milestone**: Phase 2B.2 (Ownership-Lite Borrow Enforcement & Reference Provenance)  
> **Governing Specification**: [ADR-011](../decisions/ADR-011-ownership-and-borrow-checking-semantics.md) (`ACCEPTED`), [phase-2b.0-ownership-architecture.md](phase-2b.0-ownership-architecture.md), [ownership-and-borrowing.md](../language-spec/ownership-and-borrowing.md)  
> **Scope Exclusions**: Phase 2B.3 (`Drop` elaboration, conditional drop flags, `.wonbind` FFI resource destruction), general `trait` coherence / `impl Drop` / generic `@derive(Copy)`, two-phase receiver borrows, and concurrency runtime (`Send`/`Sync`, `scope`/`spawn`) remain deferred to subsequent milestones.

---

## 1. Staged Implementation Architecture

1. **Stage A — Loan Identity & Reference-Value Carrier Flow (`internal/cfg`, `internal/borrow/loans.go`)**:
   - Assigned deterministic per-function `LoanID` (`L1, L2, ...`), `ValueID` (`v1, v2, ...`), `ProgramPoint`, `OpBorrowShared`, `OpBorrowMut`, `OpReborrow`, `OpStaticRef`, `OpCall`, `OpAggregate`, `OpTempAssign`, and paired `OpScopeExit` / `OpStorageDead`.
   - Linked every borrow creation event to its destination temporary/carrier binding (`ValTemp` -> `SrcTemp`), propagating `&T` copies (`&T` preserves all carriers), `&mut T` moves (transfers carrier cleanly), `Struct`/`Tuple`/`Aggregate` member origins, temporary call arguments, branch/match reference values, and match pattern `ref`/`ref mut` carriers (`ProjTupleField` disjoint tuple borrowing).
   - Explicitly recorded parent/child reborrow ancestry (`IsReborrow`, `ParentPlace`, `ParentLoans`) rather than inferring ancestry by variable name.

2. **Stage B — Non-Lexical Lifetimes (NLL) & Borrow Exclusivity (`internal/borrow/liveness.go`)**:
   - Solved backward NLL liveness (`NLLState{Temps, Places}`) over reachable CFG blocks (`computeReachable`) using finite monotone fixed-point iteration (`WON-INT-001` fail-closed on non-convergence).
   - Joined forward carrier flows with backward last-use sets to determine exact live loans (`PointLiveLoans`) at each CFG statement boundary — ending borrows at their final reachable use across straight-line code, branches, loops, reassignments, reborrow scopes, and `match` `guardFailBB` exits rather than block close.
   - Resolved underlying owner targets (`resolveLoanUnderlyingTargets`) through reborrows and dereferences (`Place.Overlaps`):
     - Distinct struct fields and distinct constant array indices remain disjoint; dynamic array indices and `unsafe:` raw-pointer dereferences (`FromRawPtr`/`Unknown`) remain conservatively overlapping.
     - Enforced XOR aliasing (`WON-MEM-004`, `WON-MEM-005`), mutation/move lockout (`WON-MEM-006`), parent-loan freezing while child reborrows are live, authorized access through an active carrier (`isAuthorizedThroughCarrier`), receiver-first left-to-right method calls (`vec.push(vec.len())` -> `WON-MEM-004`), and scope-exit owner lifetime enforcement (`WON-MEM-003`).

3. **Stage C — Candidate-Origin Reference Provenance (`internal/borrow/provenance.go`)**:
   - Implemented Strategy B candidate-origin return provenance `RetProv(f) ⊆ {Param(i), Static}` with **no `'a` or `'static` user-facing syntax**.
   - Built deterministic call graphs and Tarjan Strongly Connected Components (SCCs), iterating finite monotone fixed-point return summaries across direct and mutually recursive SCCs (`WON-INT-001` fail-closed on non-convergence).
   - Rejected local stack escapes (`return &local`, `return &local.field`, helper calls carrying local origins, any mixed reachable branch with local escape, or `unsafe` raw-pointer reference escapes) with **`WON-MEM-003`**.
   - Validated pairwise disjointness across mutable return candidates at call sites (**`WON-MEM-005`**) and enforced **`WON-MEM-002`** across opaque/bodyless or `KindFn` signatures returning references with `>= 2` input references or zero input references (failing closed without assuming `Static`).

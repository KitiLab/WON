# KitiLab Owner Architectural Approval Record — ADR-011 (2026-11-25)

> **Record ID**: OWNER-APPROVAL-ADR-011-2026-11-25  
> **Organization**: KitiLab  
> **Project**: WON Programming Language  
> **Approval Date**: November 25, 2026  
> **Scope**: Ratification of ADR-011 — Ownership and Borrow-Checking Semantics, plus Additional Owner Clarification on Explicitly Typed Uninitialized Bindings  
> **Builds Upon**: [OWNER-APPROVAL-2026-11-24.md](OWNER-APPROVAL-2026-11-24.md) (`ADR-007`–`ADR-010`, preserved unchanged)

---

## 1. Executive Decision Summary

On **November 25, 2026**, the KitiLab project owner formally reviewed and approved **ADR-011 (Ownership and Borrow-Checking Semantics)** and the closed Phase 2B.0 / 2B.0.1 ownership design, transitioning **ADR-011** from `Proposed` to **`ACCEPTED`** and authorizing **Phase 2B.1** implementation (`Ownership CFG`, `Place`/`Projection`, `Copy`/`@derive(Copy)`, explicitly typed uninitialized `let`/`var`, and definite-initialization/move dataflow).

---

## 2. Ratified Decisions & Owner Clarifications

### Decision 1 — Ownership CFG and Dataflow Architecture
- **Status**: `APPROVED`
- Ownership analysis uses a place-based Control-Flow Graph (`cfg`) representation rather than relying exclusively on left-to-right AST walks.
- The CFG integrates forward with ADR-009's language-independent typed CFG/TIR architecture.

### Decision 2 — Explicit `Copy` Derivation (`@derive(Copy)`)
- **Status**: `APPROVED`
- User-defined `struct` and `enum` types remain **Move-only by default**.
- The sole approved initial derivation syntax is **`@derive(Copy)`**, valid for concrete `struct` and `enum` declarations only when every field / variant payload satisfies `Copy`, the type has no destructor, and no unconstrained generic parameters are involved.
- Generic `@derive(Copy)` is deferred until the Trait Solver milestone can express trait bounds.
- Aliases such as `@copy` MUST NOT be added; unrelated attributes such as `@repr(C)` or `@repr(packed)` MUST NOT be activated during this phase.

### Decision 3 — No Visible Named Lifetime Annotations
- **Status**: `APPROVED`
- Ordinary WON reference syntax remains exclusively `&T` and `&mut T` (zero Rust-style `'static`, `'a`, or named lifetime tokens in source syntax).
- Static provenance (`Static`) is compiler-internal; string literals carry `Static` provenance, while module-level static variable storage is not assumed unless specified later.

### Decision 4 — Candidate-Origin Reference Returns (`Strategy B`)
- **Status**: `APPROVED` (Scheduled for **Phase 2B.2**, not Phase 2B.1)
- Analyzable reference-returning functions use conservative candidate-origin sets `Prov(e) = {Local(Place), Param(ParamIdx), Static}`.
- Returned local stack references are invalid (`WON-MEM-003`). Call sites conservatively keep all candidate parameter origins borrowed over the returned reference's live region (disjoint mutable candidates required; nested calls & SCC recursion fixed-point).
- Abstract / bodyless multi-reference functions preserve **`WON-MEM-002`**.

### Decision 5 — Deterministic Method Receiver Borrowing
- **Status**: `APPROVED` (Scheduled for **Phase 2B.2**)
- Method calls evaluate receiver-first, left-to-right: acquire receiver borrow immediately upon evaluating receiver, then evaluate arguments left-to-right while receiver loan is active.
- Conflicting nested calls such as `vec.push(vec.len())` are rejected deterministically (`WON-MEM-004`). Two-phase borrowing is excluded from the initial model.

### Decision 6 — NLL Borrowing and Deterministic Destruction
- **Status**: `APPROVED` (NLL in **Phase 2B.2**; Drop elaboration in **Phase 2B.3**)
- Borrowing uses flow-sensitive non-lexical region liveness. Destruction uses reverse declaration-order cleanup per lexical scope, conditional drop flags, cleanup across early returns/exits, and explicit `Drop` nodes in future CFG/TIR.

### Decision 7 — Phased Implementation Staging
- **Status**: `APPROVED`
- **Phase 2B.1 (Authorized Now)**: Ownership CFG, Place/Projection, `Copy`/`@derive(Copy)`, explicitly typed uninitialized `let`/`var`, definite initialization, move/reinitialization, and non-`Drop` partial-move analysis.
- **Phase 2B.2**: Borrow checking, NLL regions, reborrowing, and candidate-origin provenance.
- **Phase 2B.3**: Drop elaboration and resource lifecycle integration.
- **Separate Future Milestone**: General trait resolution, `impl Drop`, generic `Copy`, `Send`/`Sync`, and structured concurrency runtime.

### Additional Clarification — Explicitly Typed Uninitialized `let` and `var` Bindings
- **Status**: `APPROVED` (Included in **Phase 2B.1**)
- Simple identifier bindings MAY omit `=` when explicitly type-annotated (e.g., `let result: i32`, `var buffer: String`). Untyped uninitialized declarations (`let result`) and destructuring without initializers remain rejected.
- **Uninitialized `let`**: Starts uninitialized; first valid assignment is initialization (not mutation) and MAY occur at most once along each path; once initialized, it is immutable (`WON-MUT-001` on subsequent reassignment); cannot be reassigned after move or when possibly already initialized on another incoming path; reads before definite initialization are rejected (`WON-MEM-010`).
- **Uninitialized `var`**: Starts uninitialized; must be definitely initialized before reading (`WON-MEM-010`); may be reassigned or reinitialized after a move; replacing an already initialized owned value must be representable in CFG for future Phase 2B.3 drop insertion.

---

## 3. Approval vs. Implementation Verification Boundary

Per `AGENTS.md`, accepting **ADR-011** ratifies language design and authorizes **Phase 2B.1** implementation. Borrow checking (`2B.2`), drop elaboration (`2B.3`), and trait/concurrency enforcement remain unimplemented until their respective milestones are coded, tested, and verified in CI.

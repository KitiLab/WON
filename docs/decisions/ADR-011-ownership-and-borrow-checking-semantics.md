# ADR-011: Ownership and Borrow-Checking Semantics

## Metadata
- **Decision ID**: ADR-011
- **Title**: Ownership-Lite CFG Borrow Analysis, Non-Lexical Regions, Candidate Provenance & Copy/Drop Policy
- **Status**: Proposed (Awaiting Owner Approval)
- **Date**: 2026-11-25 (Closed in Phase 2B.0.1 Design Review)
- **Author**: KitiLab Engineering
- **Builds Upon**: [ADR-003 (Ownership-Lite Memory Model)](ADR-003-ownership-lite-memory-model.md), [ADR-008 (Structured Concurrency)](ADR-008-structured-concurrency-and-zero-gil.md), [ADR-009 (Typed Intermediate Representation Architecture)](ADR-009-typed-intermediate-representation-architecture.md)

---

## Context

ADR-003 established WON's **Ownership-Lite** foundation (single ownership, move semantics, borrowing without Rust-style named `'a` lifetime annotations, and `Shared[T]` for dynamic graphs), and ADR-009 adopted a CFG-based Typed Intermediate Representation (`TIR`).

Phase 2A–2A.2.1 implemented `TypedProgram`, interned `types.TypeID`, `IsCopy`, shallow `WON-MUT-001`, and `unsafe:` raw-pointer gates (`WON-SEC-001`/`002`), while leaving move/borrow checking unimplemented. Phases 2B.0 and 2B.0.1 closed six specific design ambiguities required before coding the borrow checker:

1. **Analysis Representation**: AST traversal vs explicit Ownership Control-Flow Graph (CFG).
2. **Region Liveness vs Drop Scope**: Strict AST-block locking vs non-lexical region liveness (NLL) paired with lexical `Drop`.
3. **User `Copy` Syntax & Rules**: Implicit structural `Copy` vs explicit `@derive(Copy)` (plus grammar gap & generics policy).
4. **Static Provenance Representation**: Eliminating any visible `'static` lifetime syntax while preserving `.rodata` literal safety.
5. **Multi-Input Reference-Return Provenance**: Strict single-origin rejection (Strategy A) vs conservative candidate-origin provenance sets (Strategy B).
6. **Method Evaluation & Trait/Drop Separation**: Strict left-to-right receiver borrowing vs two-phase borrows, and decoupling `Copy`/structural cleanup from a full trait solver or concurrency runtime.

---

## Considered Alternatives

### 1. Analysis IR: AST vs Ownership CFG
- **Alternative A — AST-Recursive Borrow Checker**: Walks `ast.Program` with lexical scopes.
  - *Rejected*: Fragile across early `return`, `?`, `break`/`continue`, conditional initialization, and match guards; throws away work when Phase 2C adds ADR-009 TIR CFGs.
- **Alternative B — Ownership CFG Upstream of TIR (Recommended)**: Lowers `TypedProgram` into basic blocks with `Place`, `Projection`, `StorageLive`/`StorageDead`, and `Loan` nodes feeding Phase 2C TIR 1:1.

### 2. Region Liveness vs Destruction Scope
- **Alternative A — Strict AST-Block Lexical Borrows**: Keep every `&x` / `&mut x` active until closing `DEDENT`.
  - *Rejected*: Triggers spurious conflicts (`let r = &x; use(r); x = 1`).
- **Alternative B — Flow-Sensitive NLL Regions + Strictly Lexical Drops (Recommended)**: Loans release after last CFG use while owned value destruction stays in deterministic reverse declaration order at scope exit, with zero `'a` lifetime syntax.

### 3. User Struct/Enum `Copy` Policy
- **Alternative A — Implicit Structural `Copy`**: Automatically make any all-`Copy` struct `Copy`.
  - *Rejected*: Adding a non-`Copy` field later silently breaks external callers; conflicts with Phase 2A's `IsCopy == false` default for structs/enums.
- **Alternative B — Explicit Single-Spelling `@derive(Copy)` (Recommended)**: Structs/enums remain Move-by-default unless `@derive(Copy)` is attached; valid only if all concrete fields/variants are `Copy` and no `Drop` destructor exists (`WON-MEM-011`). Generic `@derive(Copy)` is deferred (`WON-SEM-010`) until the Trait Solver milestone.

### 4. Static Provenance Syntax & Sources
- **Alternative A — Visible `'static` Annotations (`&'static str`)**: Introduces Rust lifetime token syntax into WON.
  - *Rejected*: Contradicts ADR-003 zero-named-lifetime goal and breaks `syntax-and-grammar.md`.
- **Alternative B — Internal `Static` Provenance Only (Recommended)**: User syntax has only `&T` and `&mut T`; compiler-internal `Prov(e)` uses category `Static` for string literals today (without assuming non-existent module `static` storage).

### 5. Multi-Input Reference-Return Provenance
- **Alternative A — Strict Single-Origin Rejection (`WON-MEM-002` whenever `>= 2` params)**:
  - *Pros*: Trivial single-origin propagation.
  - *Cons*: Rejects safe selection helpers (`fn choose(c: bool, a: &String, b: &String) -> &String`).
- **Alternative B — Conservative Candidate-Origin Provenance Sets (Recommended)**:
  - *Mechanism*: Checked function bodies compute union `RetProv(f) = {Param(i), ...} (U {Static})` across return paths (converging over finite SCCs). Call sites constrain **all** candidate argument loans $\rho_{\text{arg}_i} \supseteq \rho_{\text{ret}}$.
  - *Soundness*: Both shared returns (`&R` locking all candidate args immutably) and mutable returns (`&mut R` locking disjoint `&mut` args exclusively) prevent use-after-free and mutable aliasing regardless of runtime branch.
  - *Retained Boundary (`WON-MEM-002`)*: Abstract/bodyless signatures (`fn` pointers, future bodyless `trait` methods) with `>= 2` reference parameters still trigger `WON-MEM-002`.

### 6. Method Receiver Borrowing & Two-Phase Borrowing
- **Alternative A — Implicit Two-Phase Receiver Borrows**: Delays activating `&mut self` until arguments finish (`vec.push(vec.len())`).
  - *Rejected for Initial Phase 2B*: Adds non-trivial borrow activation state (`Reserved` vs `Active`) before core CFG/NLL is proven.
- **Alternative B — Deterministic Left-to-Right Receiver-First Borrowing (Recommended)**: Receiver loan starts immediately upon evaluating receiver before arguments (`vec.push(vec.len())` fails `WON-MEM-004`). Two-phase borrowing remains a documented future extension.

---

## Proposed Decision

**WON proposes adopting an Ownership-CFG static analyzer (`internal/cfg` + `internal/borrow`) featuring flow-sensitive NLL borrow regions, lexical reverse-order drops with conditional drop flags, explicit `@derive(Copy)` opt-in, internal `Static` provenance without `'a` or `'static` syntax, Strategy B candidate-origin return provenance, and deterministic left-to-right method borrowing (`WON-MEM-001`..`011`).**

*(Note: This ADR remains **Proposed / Awaiting Owner Approval**. No Phase 2B borrow-checker implementation has begun.)*

---

## Consequences

- **Positive Consequences**:
  - Eliminates throwaway AST passes: `cfg.FuncCFG` becomes the exact basic-block spine for ADR-009 TIR (`Phase 2C`).
  - Supports multi-input reference selection safely without introducing `'a` lifetime syntax.
  - Keeps `Copy` eligibility and trait resolution cleanly decoupled from core move/borrow verification.
  - Provides unambiguous error diagnostics (`WON-MEM-001`..`011`) integrated into JSON v1 schema.
- **Negative Consequences / Known Restrictions**:
  - Requires parser grammar extensions (`AT` token for `@derive(Copy)`, optional `=` on deferred-init `let`/`var`) prior to full test coverage in `wonc`.
  - Nested self-borrowing method calls (`vec.push(vec.len())`) require explicit temporary binding (`let n = vec.len(); vec.push(n)`) until any future two-phase extension.
  - Strategy B locks *all* candidate input arguments over the caller's returned reference region (intended conservative trade-off).
  - Bodyless multi-ref function pointers / future trait methods without body summaries remain restricted (`WON-MEM-002`).
  - Dynamic array-element borrows (`&mut arr[i]` and `&mut arr[j]`) conservatively conflict.

---

## Deferred Features

Explicitly excluded from Phase 2B and assigned to separate future milestones:
1. Full `trait` solver, coherence, conditional generic `@derive(Copy)` (`T: Copy`), user `impl Drop`, and `?` `From`/`Into` (`WON-ERR-002`).
2. `Send`/`Sync` auto-trait solver and structured-concurrency runtime enforcement (`scope`/`s.spawn`, `WON-CON-001`, Phase 3).
3. Two-phase method receiver borrowing.
4. Closure capture borrow inference (`Fn` / `FnMut` / `FnOnce`).
5. Dynamic trait objects (`&dyn Trait`), safe module `static` storage, and `.wonbind` C header sidecar enforcement (`.wonbind` hooks designed in Phase 2B.3).

---

## Explicit Owner Decisions Required Before Phase 2B.1

Before Phase 2B.1 implementation begins, owner confirmation is requested on:
1. Ratify **explicit `@derive(Copy)`** on `struct`/`enum` (concrete types in 2B; generics deferred).
2. Ratify **Strategy B (Conservative Candidate-Origin Sets)** for multi-input reference returns (retaining `WON-MEM-002` for abstract/bodyless callables).
3. Ratify **deterministic left-to-right method receiver borrowing** (deferring two-phase borrows).
4. Ratify **NLL borrow regions + lexical drops + stack drop flags**.
5. Ratify **proposed grammar extensions** (`@derive(...)` attributes + optional `=` in `let`/`var`) in [syntax-and-grammar.md](../language-spec/syntax-and-grammar.md).
6. Ratify **staged delivery (`2B.1 -> 2B.2 -> 2B.3`)** and **separation of Trait Solver / Concurrency** into a dedicated subsequent milestone.

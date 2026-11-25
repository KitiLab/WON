# ADR-011: Ownership and Borrow-Checking Semantics

## Metadata
- **Decision ID**: ADR-011
- **Title**: Ownership-Lite CFG Borrow Analysis, Non-Lexical Regions & Copy/Provenance Policy
- **Status**: Proposed (Awaiting Owner Approval)
- **Date**: 2026-11-25
- **Author**: KitiLab Engineering
- **Builds Upon**: [ADR-003 (Ownership-Lite Memory Model)](ADR-003-ownership-lite-memory-model.md), [ADR-008 (Structured Concurrency)](ADR-008-structured-concurrency-and-zero-gil.md), [ADR-009 (TIR Architecture)](ADR-009-typed-intermediate-representation-architecture.md)

---

## Context

ADR-003 established WON's **Ownership-Lite** paradigm (single ownership, move by default, lexical/local borrowing without `'a` lifetime syntax, `Shared[T]` for dynamic graphs), and ADR-009 committed WON to lowering checked programs into a CFG-based Typed Intermediate Representation (`TIR`).

Phase 2A–2A.2 implemented `TypedProgram`, interned `types.TypeID`, `IsCopy`, `WON-MUT-001`, and `WON-SEC-002`, leaving move/borrow checking and CFG lowering unimplemented (`docs/compiler/phase-2a-semantic-analysis.md`). To implement Phase 2B cleanly without rewriting AST passes or painting the compiler into a corner before Phase 2C TIR, four concrete semantic and architectural choices require explicit resolution:

1. Whether borrow and move analysis runs directly over AST recursion or over an explicit Ownership Control-Flow Graph (CFG).
2. Whether borrow lifetimes expire strictly at closing AST block boundaries (`DEDENT`) or at non-lexical last-use CFG points (NLL) while value destructors remain lexical.
3. Whether user-defined `struct`/`enum` types with all-`Copy` fields implicitly become `Copy` or remain Move-by-default unless explicitly annotated (`@derive(Copy)`).
4. How borrow splitting, provenance (`Prov(e)`), and raw-pointer escape hatches interact across `unsafe:` and C FFI boundaries.

---

## Considered Alternatives

### Choice 1: Analysis Representation
- **Alternative A — AST-Recursive Borrow Checker**: Walks `ast.Program` directly using lexical symbol maps.
  - *Rejected*: Fragile across early `return`, `?`, `break`/`continue`, conditional branch initialization, and match guards; would be discarded when Phase 2C introduces ADR-009 TIR CFGs.
- **Alternative B — Ownership CFG Upstream of TIR (Recommended)**: Lowers `TypedProgram` into basic blocks with `Place`, `Projection`, `StorageLive`/`StorageDead`, `Loan`, and `OpMove`/`OpCopy`, serving both Phase 2B borrow checking and Phase 2C TIR lowering.

### Choice 2: Reference Liveness vs Drop Order
- **Alternative A — Strict AST-Block Lexical Borrows**: Keep every `&x` / `&mut x` locked until end of enclosing indented block.
  - *Rejected*: Rejects natural imperative patterns (`let r = &x; use(r); x = 1`), creating friction for both human programmers and AI agents.
- **Alternative B — Flow-Sensitive Non-Lexical Region Liveness (NLL) + Strictly Lexical Value Drops (Recommended)**: Reference loans expire immediately after the last CFG read along reachable paths, while owned value `Drop` remains strictly in reverse declaration order at scope exit. Preserves zero `'a` lifetime annotations.

### Choice 3: User Struct/Enum `Copy` Policy
- **Alternative A — Implicit Structural `Copy`**: Any struct whose fields are all `Copy` silently becomes `Copy`.
  - *Rejected*: Adding one `String` or `Shared[T]` field silently breaks all callers relying on multiple value uses; also diverges from Phase 2A's `types.IsCopy == false` for structs/enums.
- **Alternative B — Explicit Opt-In `@derive(Copy)` (Recommended)**: User structs/enums remain Move-by-default (`!IsCopy`) unless explicitly marked `@derive(Copy)` (permitted only if all fields/variants are `Copy` and no `Drop` impl exists).

---

## Decision

**WON Phase 2B adopts an Ownership-CFG static analyzer using flow-sensitive non-lexical borrow regions, explicit `@derive(Copy)` opt-in for user composites, field-level disjoint borrow splitting, and single-origin `Prov(e)` return inference (`WON-MEM-001`..`010`).**

Specifically:
1. Compiler analysis lowers `TypedProgram` (`internal/checker`) into a basic-block `cfg.FuncCFG` (`internal/cfg`), analyzed by `internal/borrow`, and later lowered directly into Phase 2C TIR (`ADR-009`).
2. Definite initialization, move invalidation (`WON-MEM-001`), reinitialization, partial field moves (for non-`Drop` structs), and drop flags (`WON-MEM-007`) are tracked path-sensitively across CFG edges.
3. Disjoint struct fields (`s.a` vs `s.b`) and tuple slots split cleanly (`ProjField`), whereas dynamic array indexing (`arr[i]` vs `arr[j]`) conservatively conflicts.
4. Multi-input reference return ambiguity (`Prov(ret)` referencing `>= 2` input reference parameters) continues to be conservatively rejected (`WON-MEM-002`), avoiding named lifetime syntax per ADR-003.
5. Safe code cannot dereference `*const T` / `*mut T` (`WON-SEC-002`) or implicitly upgrade raw pointers to `&T`/`&mut T`.

---

## Consequences

- **Positive Consequences**:
  - Eliminates throwaway AST borrow-checker code: `cfg.FuncCFG` provides the exact control-flow and drop-elaborated backbone required by Phase 2C TIR (`ADR-009`).
  - Dramatically reduces spurious borrow conflicts compared to naive block-scoped borrows without introducing `'a` annotations.
  - Preserves existing Phase 2A `IsCopy` behavior for user structs/enums until `@derive(Copy)` lands.
  - Produces structured span provenance (use span + move/loan origin span) for AI JSON diagnostics.
- **Negative Consequences / Trade-Offs**:
  - Adds `internal/cfg` and `internal/borrow` compiler subsystems in Phase 2B.1–2B.2.
  - Multi-reference selection functions (`fn pick(c: bool, a: &T, b: &T) -> &T`) must use owned returns, `Shared[T]`, or `&mut out` parameters (`WON-MEM-002`).
  - Dynamic array-element disjointness requires slice-splitting helpers rather than raw `arr[i]`/`arr[j]` proofs.
- **Impact on Pillars**:
  - *Performance*: Zero runtime overhead for stack/scalar/borrow lifetimes; drop flags only synthesized for conditionally moved locals.
  - *Memory Safety*: Eliminates use-after-move, double-free, dangling stack references, and aliasing data races in safe code once Phase 2B.1–2B.2 ship.
  - *C Interoperability*: Clean `unsafe:` isolation (`WON-SEC-001`/`002`) with forward-compatible `.wonbind` `@owned`/`@borrow` loan hooks.
  - *AI-Native Design*: Clear diagnostics (`WON-MEM-001`..`010`) with origin spans and unambiguous fix edits.
  - *Simple Deployment*: Compile-time pass only; adds zero runtime library bloat.

---

## Open Questions & Future Milestones

1. **Owner Approval Gate**: Ratify `@derive(Copy)` opt-in vs structural `Copy`, NLL-within-scope vs strict block borrows, and strict argument evaluation order vs two-phase method receiver borrows (`v.push(v.len())`) prior to Phase 2B.1 coding.
2. **Closure Semantics (Future)**: Borrow-capture trait inference (`Fn`/`FnMut`/`FnOnce`) remains deferred until closures enter the parser/checker.
3. **Structured Concurrency Activation (Phase 3)**: Wire `Send`/`Sync` auto-trait checking and `TermScopeJoin` nursery borrow bounds into `scope s:` / `s.spawn:` once `WON-SEM-010` is lifted.

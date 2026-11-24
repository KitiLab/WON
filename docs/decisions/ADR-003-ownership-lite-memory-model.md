# ADR-003: Ownership-Lite Memory Model

## Metadata
- **Decision ID**: ADR-003
- **Title**: Ownership-Lite Memory Model
- **Status**: ACCEPTED
- **Date**: 2026-11-24
- **Author**: KitiLab Engineering

## Context
Automated and human systems programming requires memory safety without sacrificing performance or introducing garbage collector pauses. While Rust proves that static borrow checking can guarantee memory safety, its named lifetime annotations (`'a`) introduce substantial friction, complex error diagnostics, and steep barriers for AI coding agents.

WON requires a memory model that:
1. Eliminates use-after-free, double-free, and data races in safe code.
2. Incurs zero runtime overhead for scalar and stack allocations.
3. Completely avoids Rust-style named lifetime annotations (`'a`).
4. Remains intuitive for AI coding agents to reason about locally.

## Considered Alternatives
1. **Garbage Collection (Tracing or Generational)**:
   - *Rejected*: Incompatible with the non-negotiable performance pillar; introduces pause times, memory bloat, and complicates direct C interoperability.
2. **Full Rust-Style Lifetimes with Explicit Annotations**:
   - *Rejected*: Named lifetime annotations create brittle code, high cognitive load, and frequent compile failures for AI agents.
3. **Pure Reference Counting (Automatic ARC)**:
   - *Rejected*: Atomic reference count increment/decrement adds measurable performance tax to high-frequency inner loops and struggles with reference cycles.
4. **Ownership-Lite (Selected)**:
   - Move semantics by default for heap resources.
   - Lexical borrowing constrained to block and call scopes.
   - Implicit borrow propagation (no named lifetime annotations).
   - Explicit `Shared<T>` for dynamic graph topologies.

## Decision
**WON adopts the "Ownership-Lite" model as its core memory architecture.**

All values have a single owner. Moving transfers ownership. Borrowing is lexically scoped with compiler-enforced exclusivity (one mutable borrow XOR many immutable borrows). Functions cannot return references to stack variables. Dynamic structures requiring multi-owner lifetimes explicitly use `Shared<T>`.

## Consequences
- **Positive**: Compile-time safety guarantees without the cognitive or syntactic burden of lifetime parameters; highly structured and predictable for AI code generation.
- **Negative**: Certain complex cyclic or self-referential graph patterns require explicit `Shared<T>` or arena allocation rather than complex static lifetimes.
- **Impact on Pillars**:
  - *Performance*: Zero runtime GC overhead; deterministic destruction.
  - *Memory Safety*: Verified statically in compiler passes.
  - *AI Ergonomics*: Dramatically simpler for coding agents to generate correct code.

## Open Questions & Future Milestones
- Formalize drop-flag semantics and struct member borrow splitting in Phase 2.

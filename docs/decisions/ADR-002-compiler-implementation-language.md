# ADR-002: Compiler Implementation Language (Bootstrap Compiler)

## Metadata
- **Decision ID**: ADR-002
- **Title**: Compiler Implementation Language for Phase 0B Bootstrap
- **Status**: ACCEPTED (Scoped to Phase 0B Bootstrap Compiler)
- **Date**: 2026-11-24
- **Author**: KitiLab Engineering

## Context
Building a clean, rapid vertical slice requires selecting an implementation language for the bootstrap compiler (`compiler/poc/`) that provides fast compilation throughput, robust standard libraries, clean cross-platform execution, and zero external dependency friction.

Crucially:
> **The WON programming language remains strictly independent of its compiler implementation language.** The choice of Go for the bootstrap compiler does not constrain the syntax, runtime, or future self-hosting roadmap of WON. Executables compiled by WON have zero dependency on the Go runtime.

## Considered Candidates
1. **Go 1.22+ (Selected for Bootstrap)**:
   - *Strengths*: Instant sub-second compiler builds, clean standard library (`encoding/json`, `os/exec`, `testing`), simple memory management for AST structures, zero external dependencies.
   - *Scope*: Bootstrap compiler under `compiler/poc/`.
2. **Rust**:
   - *Strengths*: Pattern matching and algebraic data types.
   - *Considerations*: Slower iterative compile times during rapid prototype prototyping.
3. **Zig / OCaml**:
   - *Considerations*: Not pre-installed in the target environment.

## Decision
**Go is selected as the implementation language for the Phase 0B bootstrap compiler (`wonc`).**

This decision is strictly scoped to the Phase 0B prototype and incubation phase. It may be re-evaluated before entering Phase 1 or when pursuing self-hosting.

## Consequences
- **Positive**: Extremely fast build and test iteration loops (`<0.5s` test runs); zero third-party dependencies; straightforward AST/TIR manipulation.
- **Negative**: Lack of algebraic data types requires interface type-switches in Go.
- **Impact on Pillars**:
  - *Independence*: WON executables are native C binaries independent of Go.

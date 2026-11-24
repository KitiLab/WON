# ADR-002: Compiler Implementation Language

## Metadata
- **Decision ID**: ADR-002
- **Title**: Compiler Implementation Language Evaluation
- **Status**: PROPOSED / UNDER EVALUATION
- **Date**: 2026-11-24
- **Author**: KitiLab Engineering

## Context
The implementation language of the initial WON compiler (`wonc`) must balance rapid developer velocity, high compilation throughput, clean data structures for AST and TIR representation, excellent cross-platform portability, and suitability for AI-agent maintenance.

Crucially:
> **The WON programming language is strictly independent of its compiler implementation language.** The choice of language for the compiler does not constrain the semantics, syntax, or runtime of WON itself. Eventually, WON aims to self-host.

## Considered Candidates

### 1. Go
- **Pros**: Outstanding compilation speed, excellent standard library, simple concurrency model, trivial cross-compilation (`GOOS`/`GOARCH`), clean garbage-collected memory management for AST/TIR data structures (eliminating compiler-internal memory leaks during prototyping), highly structured tooling. Currently available on the target environment (`go version go1.27.4`).
- **Cons**: Minor runtime footprint; lack of algebraic data types (enums with data) requires interface-based visitor or type-switch patterns.

### 2. Rust
- **Pros**: Exceptional pattern matching and algebraic data types ideal for ASTs and compilers; zero runtime overhead; mature parser libraries. Available on target environment (`rustc 1.100.0`).
- **Cons**: Slower compilation times for the compiler itself; higher cognitive overhead for rapid iterative prototype changes during Phase 0B.

### 3. Zig
- **Pros**: First-class C integration; lightweight toolchain; excellent cross-compilation; modern systems language.
- **Cons**: Toolchain not currently installed in the host environment; rapid language evolution still introducing breaking changes.

### 4. C++ (Modern C++20/C++23)
- **Pros**: Universally available (`g++ 16.2`, `clang++ 23.1`); mature ecosystem; direct integration with LLVM if adopted later.
- **Cons**: High build complexity; unsafe defaults; verbose syntax; complex template diagnostics.

### 5. OCaml
- **Pros**: Traditional choice for compiler construction; exceptional pattern matching and type system.
- **Cons**: Not installed on host environment; smaller developer ecosystem.

## Current Recommendation
**Go is currently the preferred leading candidate** for the initial compiler bootstrap due to its exceptional compilation speed, rock-solid cross-platform support, and simplicity for AI-agent manipulation. Rust remains a strong second candidate.

## Decision
**NOT YET DECIDED.**
Final decision will be established through the Phase 0B proof-of-concept benchmark and prototype implementation.

# WON Development Roadmap

> **Document Status**: `SPECIFIED` (Phase 0B–2A `IMPLEMENTED`; Phase 2B+ Planned)  
> **Author**: KitiLab Engineering  
> **Last Updated**: Phase 2A

---

## Strategic Roadmap Overview

WON follows an empirical, milestone-driven development process divided into eight sequential phases (Phase 0 through Phase 7). Progress between phases requires meeting strict acceptance criteria validated by automated test suites, memory verifiers, and reproducible benchmarks.

```
Phase 0A ──► Phase 0B ──► Phase 1 ──► Phase 2 ──► Phase 3 ──► Phase 4 ──► Phase 5 ──► Phase 6 ──► Phase 7
  Repo       Vertical     Grammar     Core        Toolchain   Security    v1.0        Python      Ecosystem
Foundation    Slice        & AST    Compiler     & AI CLI    & Fuzzing   Launch      Embedded    Maturation
```

---

## Detailed Milestone Breakdown

### Phase 0: Proof of Concept
- **Sub-phase 0A (Current)**:
  - *Objective*: Official repository initialization under KitiLab, environment inspection, architectural specifications, ADR foundation, and community governance.
  - *Deliverables*: Official `KitiLab/WON` repository, complete `docs/` technical suite, initial ADRs, `AGENTS.md`.
  - *Acceptance Criteria*: Repository clean, public, verified; zero unverified performance claims; complete documentation alignment.
- **Sub-phase 0B**:
  - *Objective*: Implement a minimal end-to-end vertical slice proving parser feasibility, C header binding (`zlib`), static executable generation, and an automated AI error-repair loop.
  - *Deliverables*: Minimal prototype parser, zlib header binding generator, C codegen backend, benchmark harness vs C/Go, AI self-repair test.
  - *Acceptance Criteria*: Statically linked executable compiles and decompresses a buffer; identical zlib workload measured against GCC -O3 and Go; automated agent repairs a structured syntax error using JSON compiler diagnostics.

### Phase 1: Language Foundations & Formal Specification
- **Sub-phase 1A (Language Specification Initial Draft)**:
  - *Objective*: Transform the product vision into a formal, rigorous, internally consistent language specification suite ([docs/language-spec/](language-spec/README.md)).
  - *Deliverables*: Formal EBNF grammar, Type System, Ownership-Lite & Borrowing, Structured Concurrency, Error Handling, C Interoperability, Self-Hosting & Multi-Stage Bootstrap Pipeline, Typed Intermediate Representation (TIR) Architecture, Specification Test Fixtures, ADR-007 through ADR-010, and Reconciliation Audit.
  - *Status*: `SPECIFIED` (Under Architectural Review).
- **Sub-phase 1A.1 (Specification Integrity Review & Semantic Closure)**:
  - *Objective*: Remediate formal grammar gaps, establish mathematical soundness models for reference provenance ($\text{Prov}(e)$), formalize 3-tier string model, integer overflow semantics, declarative C sidecar contracts (`.wonbind`), and provide 100% EBNF traceability across all 20 canonical examples.
  - *Deliverables*: Comprehensive closure log ([docs/language-spec/phase-1a.1-semantic-closure.md](language-spec/phase-1a.1-semantic-closure.md)), updated specification files, and verified traceability matrix.
  - *Status*: `SPECIFIED` (Completed & Reconciled).
- **Sub-phase 1B (Canonical Lexer & Parser Implementation)**:
  - *Objective*: Implement the formal indentation-based lexer (`INDENT`/`DEDENT` off-side rule), recursive-descent/Pratt parser, and span-mapped AST data structures in the bootstrap compiler (`wonc0`).
  - *Deliverables*: Upgraded `wonc0` lexer/parser, canonical AST, migrated examples/benchmarks/repair tests, and automated syntax test suites verifying valid/invalid fixtures in `tests/language-spec/` and all 20 canonical examples.
  - *Status*: `IMPLEMENTED` (Canonical Frontend Verified; Semantic & TIR Passes Remain Phase 2).

---

## 3. Implementation Dependency Roadmap

The implementation roadmap adheres to strict technical dependency ordering across three sequential layers:

```
┌────────────────────────────────────────────────────────────────────────┐
│                      Layer 1: Foundational Core                         │
│ • Lexer with synthetic INDENT/DEDENT off-side rule                     │
│ • Recursive-descent parser and exact source-span AST                   │
│ • Symbol tables, multi-pass name resolution, module layout             │
│ • Bidirectional local type inference & trait constraint solver         │
│ • Ownership-lite borrow checking (move analysis, alias XOR mutability) │
│ • Typed Intermediate Representation (TIR) builder & basic blocks       │
│ • C11 code generation backend (C11Emitter)                             │
│ • Direct C header ingestion (#import) with composite fingerprint cache  │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ Unlocks
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│             Layer 2: Prerequisites for Self-Hosting (Stage 1)          │
│ • Dynamic resizable arrays (Vec[T]) and owned strings (String, &str)   │
│ • Associative symbol maps (HashMap[K, V])                              │
│ • Algebraic data types (structs, enums, match pattern destructuring)  │
│ • File I/O (reading source code, writing generated artifacts)          │
│ • Process spawning (invoking GCC/Clang/LD) and subprocess execution    │
│ • Structured JSON diagnostics protocol and error reporting             │
│ • Multi-file package compilation and module organization               │
│ • Self-hosted compiler implementation in WON (wonc1)                   │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ Unlocks
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│             Layer 3: Advanced Language & Ecosystem Capabilities        │
│ • Structured concurrency runtime (scope, spawn, work-stealing pool)    │
│ • OS process sandboxing (Linux Landlock/seccomp, macOS Seatbelt)       │
│ • Integrated LLVM / QBE direct machine code backends                   │
│ • Automated AI coding-agent repair and introspection APIs              │
│ • Cross-compilation toolchains and package distribution registry       │
└────────────────────────────────────────────────────────────────────────┘
```

### Phase 2: Core Compiler & Ownership-Lite
- **Sub-phase 2A (Core Semantic Analysis & Type System)**:
  - *Objective*: Implement canonical interned type representation, two-pass hierarchical symbol resolution, bidirectional local type inference, struct/enum/method semantics, `match` exhaustiveness checking, `Option`/`Result`/`?` propagation, UTF-8 validation (`WON-LEX-004`), and the backend-decoupled `TypedProgram` boundary (`WON-GEN-002`).
  - *Deliverables*: `internal/types`, `internal/symbols`, upgraded `internal/checker`, [phase-2a-typed-semantic-model.md](compiler/phase-2a-typed-semantic-model.md), [phase-2a-semantic-analysis.md](compiler/phase-2a-semantic-analysis.md), Go fuzz suite, and automated Phase 2A semantic fixture suite.
  - *Status*: `IMPLEMENTED` (Verified in CI).
- **Sub-phase 2B (Ownership-Lite Borrow Checker & Trait Solver — Planned)**:
  - *Objective*: Compile-time move tracking, lexical borrow & reference provenance (`WON-MEM-001`/`002`), trait coherence/impl checking, and `From`/`Into` conversion on `?`.
  - *Status*: `SPECIFIED` / `PLANNED`.
- **Sub-phase 2C (Typed Intermediate Representation & Expanded C11 Backend — Planned)**:
  - *Objective*: Lower `TypedProgram` to explicit basic-block TIR (ADR-009), monomorphize generics, and generate ISO C11 code for algebraic data types and pattern matching.
  - *Status*: `SPECIFIED` / `PLANNED`.
- **Acceptance Criteria**: Memory safety invariants enforced at compile time; zero use-after-free and double-free in safe test cases; C code compiles cleanly under GCC and Clang with zero warnings (`-Wall -Wextra -Werror`).

### Phase 3: Toolchain & AI Agent Integration
- **Objective**: Build the unified CLI driver (`won`) and machine-readable AI agent communication layer.
- **Deliverables**:
  - CLI commands: `won build`, `won run`, `won test`, `won check`, `won inspect`.
  - Machine-readable `--format=json` compiler diagnostics.
  - `--explain` and `--suggest-fix` agent interfaces.
  - Automated `AGENTS.md` synchronization tool reflecting current compiler capabilities.
- **Acceptance Criteria**: External AI agents can compile, inspect types, receive diagnostic payloads, and iteratively correct code without human intervention.

### Phase 4: Security, Fuzzing & Static Validation
- **Objective**: Harden the compiler, runtime, and standard library against memory safety regressions and security vulnerabilities.
- **Deliverables**:
  - Continuous libFuzzer and AFL++ integration for parser and compiler front-end.
  - Compile-time capability tracking (I/O, network, memory allocations).
  - Memory sanitizer (ASan, MSan, TSan) verification in CI.
- **Acceptance Criteria**: Zero unresolved crashes or memory leaks across 100M+ fuzzing iterations; 100% of safe-mode test cases pass AddressSanitizer with zero errors.

### Phase 5: Version 1.0 Public Release
- **Objective**: Finalize syntax stability, complete core standard library, package official distribution binaries, and publish public benchmark suite.
- **Deliverables**:
  - Stabilized v1.0 language specification.
  - Core standard library (`std::core`, `std::io`, `std::collections`, `std::sync`).
  - Single-binary distribution for Linux (x86_64, aarch64), macOS (Apple Silicon, Intel), and Windows.
  - Public reproducible benchmark repository and dashboard.
- **Acceptance Criteria**: Production language stability guarantee; benchmark results independently verifiable by third parties.

### Phase 6: Embedded Python Integration
- **Objective**: Integrate zero-cost native CPython C-API embedding directly into WON programs.
- **Deliverables**:
  - Direct compilation against CPython headers without inter-process communication.
  - Seamless data type conversion between WON primitives and Python PyObject pointers.
  - High-performance native bindings for AI/ML libraries (e.g. PyTorch, NumPy).
- **Acceptance Criteria**: WON programs can instantiate embedded Python runtimes, execute code, and exchange high-dimensional arrays with zero serialization copy overhead.

### Phase 7: Long-Term Ecosystem & Alternative Backends
- **Objective**: Expand compiler backend architecture to support dedicated code generation and grow language tooling.
- **Deliverables**:
  - Dedicated direct LLVM or QBE code generation backend options alongside the C backend.
  - Package manager and dependency resolution engine.
  - Official Language Server Protocol (LSP) server for editors.
- **Acceptance Criteria**: Native LLVM backend generates comparable or superior machine code to the C backend; full ecosystem tooling operational.

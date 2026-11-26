# WON Development Roadmap

> **Document Status**: `SPECIFIED` (Phase 0B–2B.2.2 `IMPLEMENTED`; Phase 2B.3+ Planned)  
> **Author**: KitiLab Engineering  
> **Last Updated**: Phase 2B.1 (Ownership CFG, Init & Move Analysis)

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
- **Sub-phase 2A.1 (Semantic Correctness Hardening)**:
  - *Objective*: Harden full-width `math/big.Int` integer literals (`i8..i128`, `u8..u128`, `isize`/`usize`), enforce `unsafe:` boundaries on raw-pointer dereferencing (`WON-SEC-002`), unify signature-first single-pass method checking, and replace bidirectional `CanAssign` hacks with strict `CommonType` type joins.
  - *Deliverables*: Arbitrary-precision literal AST/lexer/types validation, [phase-2a.1-correctness-hardening.md](compiler/phase-2a.1-correctness-hardening.md), and exhaustive regression & fuzz suites.
  - *Status*: `IMPLEMENTED` (Verified in CI).
- **Sub-phase 2A.2 / 2A.2.1 (C11 Backend Capability Contract, Numeric Lowering & Unit Return Closure)**:
  - *Objective*: Enforce fail-closed WON-to-C11 type mapping (`MapTypeToC`), complete pre-codegen `WON-GEN-002` capability gating, emit typed `<stdint.h>` integer constants (`INT64_MIN`, `UINT64_C`, etc.) from `TypedProgram`, support `return unit_call()`, and uphold strict ISO C11 hosted `main` ABI (`i32` and `unit` wrapper).
  - *Deliverables*: [phase-2a.2-backend-capability-matrix.md](compiler/phase-2a.2-backend-capability-matrix.md), `internal/checker/backend_caps.go`, hardened `internal/codegen`, and GCC/Clang `-pedantic-errors -Werror` validation suites.
  - *Status*: `IMPLEMENTED` (Verified in CI).
- **Sub-phase 2B (Ownership-Lite CFG & Borrow Checker)**:
  - **2B.0 / 2B.0.1 (Ownership Architecture & Design Closure)**: Closed design for Ownership-CFG (`Place`, `Loan`, NLL region liveness, `@derive(Copy)` policy, `Static` literal provenance without `'a`/`'static` syntax, Strategy B candidate-origin return provenance, deterministic left-to-right method receiver borrowing, `WON-MEM-001`..`011` matrix, and Drop elaboration contract).
    - *Deliverables*: [phase-2b.0-ownership-architecture.md](compiler/phase-2b.0-ownership-architecture.md), [ownership-and-borrowing.md](language-spec/ownership-and-borrowing.md), [ADR-011](decisions/ADR-011-ownership-and-borrow-checking-semantics.md) (`ACCEPTED`), [OWNER-APPROVAL-ADR-011-2026-11-25.md](decisions/OWNER-APPROVAL-ADR-011-2026-11-25.md).
    - *Status*: `IMPLEMENTED` (Governance & Design Ratified Nov 25, 2026).
  - **2B.1 / 2B.1.1 / 2B.1.2 (Ownership CFG, `@derive(Copy)`, Uninitialized `let`/`var`, Move/Init & Match Pattern Closure)**:
    - *Objective*: Implement `@derive(Copy)` & uninitialized simple `let`/`var` extensions, `internal/cfg` (`Place`, `Projection`, `BasicBlock`), definite-initialization, move, reinitialization, non-`Drop` partial-move, and loop fixed-point analysis (`WON-MEM-001`, `008`, `010`, `011`), Phase 2B.1.1 fail-closed hardening (`WON-INT-001`, unbounded lazy field tracking, `OwnershipChecked` codegen gate, AST-to-CFG exhaustiveness), and Phase 2B.1.2 match pattern ownership closure (borrow event preservation in mixed aggregates, `OpCopy` on `Copy` pattern bindings, and `WON-SEM-010` conservative guards on mixed opaque aggregates and guarded non-`Copy` consuming arms).
    - *Deliverables*: `internal/cfg`, `internal/borrow`, [phase-2b.1.1-cfg-soundness-closure.md](compiler/phase-2b.1.1-cfg-soundness-closure.md), [phase-2b.1.2-match-pattern-ownership-closure.md](compiler/phase-2b.1.2-match-pattern-ownership-closure.md), `@derive(Copy)` & uninit `let`/`var` in lexer/parser/types/checker/codegen, fuzz + CLI C11 tests.
    - *Status*: `IMPLEMENTED` (Verified in official CI run `45808397443`).
  - **2B.2 / 2B.2.1 / 2B.2.2 (Borrow Checking, NLL Regions, Provenance, Authority & Invariant Closure)**:
    - *Objective*: Enforce shared/exclusive XOR loan conflicts (`WON-MEM-004`/`005`/`006`), disjoint field/const-index splitting, reborrowing parent/child freezing & shared->mut prevention, NLL backward region liveness, deterministic left-to-right method borrowing, canonical `self` method indexing, Strategy B candidate-origin & per-field aggregate provenance via Tarjan SCC fixed point, local escape (`WON-MEM-003`), opaque boundary rejection (`WON-MEM-002`), and fail-closed `AccessAuthority` / parent-loan / carrier-origin / call-candidate metadata closure (`WON-INT-001`).
    - *Deliverables*: `internal/borrow/{loans,liveness,provenance,authority}.go`, `internal/symbols/symbol.go`, [phase-2b.2-borrow-checker-nll-provenance.md](compiler/phase-2b.2-borrow-checker-nll-provenance.md), [phase-2b.2.1-mutability-and-provenance-closure.md](compiler/phase-2b.2.1-mutability-and-provenance-closure.md), [phase-2b.2.2-authority-and-invariant-closure.md](compiler/phase-2b.2.2-authority-and-invariant-closure.md), unit, CLI, and fuzz suites.
    - *Status*: `IMPLEMENTED` (Phase 2B.2 verified at `9d2cda4` / `45814423072`; Phase 2B.2.1 verified at `7d6b27b` / `45822885882`; Phase 2B.2.2 closure implemented and verified).
  - **2B.3 (Drop Elaboration & Resource Lifecycle Integration — Proposed)**:
    - *Objective*: Lexical reverse-order cleanup, conditional drop flags, early-return / `?` cleanup edges, partial-move cleanup, and `.wonbind` ownership contract hooks (`WON-MEM-007` readiness).
    - *Status*: `PLANNED`.
- **Sub-phase 2C (Typed Intermediate Representation & Expanded C11 Backend — Planned)**:
  - *Objective*: Lower verified Ownership CFG to explicit basic-block TIR (ADR-009), emit `Drop` lifecycle calls, monomorphize generics, and generate ISO C11 code for algebraic data types and pattern matching.
  - *Status*: `SPECIFIED` / `PLANNED`.
- **Separate Future Milestone — General Trait Solver & Structured Concurrency Safety (`scope` / `Send` / `Sync`)**:
  - *Objective*: User `trait` coherence, generic `Copy` conditional bounds, user `impl Drop`, `?` `From`/`Into` conversion (`WON-ERR-002`), auto-traits `Send`/`Sync`, and structured-concurrency task capture enforcement (`WON-CON-001`, Phase 3).
  - *Status*: `PLANNED` (Explicitly separated from Phase 2B core borrow checker).
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

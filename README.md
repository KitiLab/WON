<div align="center">

# WON

**An independent, high-performance compiled programming language with ownership-lite memory safety, native execution, universal C interoperability and an AI-native compiler.**

*Developed by KitiLab*

---

[![Project Status](https://img.shields.io/badge/status-Phase%202B.0.1%20%28Ownership%20Design%20Closed%29-blue.svg)](#4-current-development-status)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Organization](https://img.shields.io/badge/org-KitiLab-black.svg)](https://github.com/KitiLab)

</div>

---

## 1. Project Identity & Positioning

> **"One tool. One file. Any C library. Built for AI agents."**

WON is an independent programming language created to unite systems-level performance with modern ergonomics, frictionless C library integration, and native support for automated AI coding agents.

WON is **not** a wrapper around an existing runtime. It is designed from first principles with its own grammar, compiler pipeline, type system, intermediate representation, runtime, standard library, and developer tooling.

---

## 2. The Five Engineering Pillars

### A. Performance (Non-Negotiable)
- **Target**: Competitive with optimized native C across standard production workloads.
- **Architecture**: Ahead-of-Time (AOT) compilation to native machine code via an optimized C generation pipeline and native toolchains (GCC / Clang).
- **Zero Overhead**: No garbage collection pauses, no Global Interpreter Lock (GIL), deterministic memory layouts, and predictable cache-friendly data structures.
- **Empirical Rigor**: Transparent, public, reproducible benchmarks run under identical hardware and compiler optimization levels.

### B. Ownership-Lite Memory Safety
- **Core Model**: Compile-time move and borrow semantics without Rust-style lifetime annotations (`'a`).
- **Safety Boundaries**: Thread safety enforced in safe code; explicit `unsafe:` blocks for raw pointer manipulation and low-level FFI.
- **Explicit Shared Ownership**: Ergonomic shared types for dynamic graph topologies without hidden runtime tracing.

### C. Universal C Interoperability
- **Header Parsing**: Direct compilation against existing C headers (`zlib.h`, `sqlite3.h`, `curl.h`, etc.).
- **Zero-Friction Bindings**: Automated type and function signature translation, eliminating manual binding boilerplate.
- **Future Integration**: Planned native embedding of CPython runtime via C API without inter-process overhead.

### D. AI-Native Toolchain
- **Machine-Readable Diagnostics**: First-class JSON output for errors, type queries, and symbol resolution.
- **Self-Repair Protocol**: Interactive `--explain` and `--suggest-fix` commands designed for automated coding agents.
- **Vendor Independence**: Provider-neutral JSON-RPC / CLI protocol compatible with any LLM or IDE orchestrator.
- **Embedded Agent Rules**: Automated `AGENTS.md` synchronization reflecting compiler versions and language rules.

### E. Single-File Static Deployment
- **Portability**: Native compilation to standalone, statically linked binaries.
- **Minimal Dependencies**: Zero external runtime requirements for deployed executables.

---

## 3. Planned Compiler Architecture

```
                 WON Source File (.won)
                           │
                           ▼
                 Lexer & Parsing Subsystem
                           │  AST (Abstract Syntax Tree)
                           ▼
               Type Checking & Inference Engine
                           │  TIR (Typed Intermediate Representation)
                           ▼
                  TIR Optimization Passes
                           │  Optimized TIR
                           ▼
                 C Code Generation Backend
                           │  Generated Standard C Source
                           ▼
                 Clang / GCC Native Compiler
                           │  Machine Code
                           ▼
               Statically Linked Native Binary
```

The initial backend generates standard C code, harnessing decades of mature optimization passes within GCC and Clang while establishing rapid validation of language semantics. Future phases will evaluate dedicated intermediate backends (such as LLVM or QBE).

---

## 4. Current Development Status

> [!NOTE]
> **WON is in Phase 2 (Core Compiler). Phase 0B/0B.2, Phase 1A/1A.1–1A.2, Phase 1B Canonical Frontend (Lexer & Parser), Phase 2A–2A.2.1 Core Semantic Analysis & C11 Subset Contract are `IMPLEMENTED`; Phase 2B.0 / 2B.0.1 Ownership-Lite Design Closure is `SPECIFIED` (`Proposed / Awaiting Owner Approval` for ADR-011 before Phase 2B.1 implementation begins).**
>
> The bootstrap compiler (`wonc0` under `compiler/poc/`) now implements strict UTF-8 validation (`WON-LEX-004`), the canonical Python-inspired indentation lexer (`INDENT`/`DEDENT`/`NEWLINE` off-side rule), recursive-descent/Pratt parser (Draft 0.1 EBNF), canonical interned type system (`internal/types`), hierarchical two-pass symbol table (`internal/symbols`), bidirectional type checker with struct/enum/`match` exhaustiveness/`Option`/`Result`/`?` checking and `TypedProgram` boundary (`internal/checker`), C header ingestion, strict ISO C11 code generation with `-Werror`, and autonomous AI coding-agent repair (`let` -> `var`).
>
> *Note on Scope Boundary*: `wonc check` validates the complete Phase 2A semantic surface. `wonc build` enforces `TypedProgram.CodegenSupported` and refuses high-level constructs not yet lowered by the Phase 0B/1B C11 backend (`WON-GEN-002`). Ownership-lite borrow analysis (Phases 2B.1–2B.3, designed in 2B.0 / 2B.0.1), full CFG TIR lowering (Phase 2C), trait constraint solving, and concurrency runtimes (Phase 3) remain `SPECIFIED`/`PLANNED`.
>
> *Note on Static Linking*: Standalone static binary generation remains classified as `BLOCKED (Environment)` on this host due to missing system development archives (`libc.a`, `libz.a`). Dynamic native builds link against `/lib64/libz.so.1` and pass 100% of functional gates.

### Working CLI Commands (`wonc`)

```bash
# Build the bootstrap compiler
cd compiler/poc && go build -o bin/wonc ./cmd/wonc

# Parse canonical WON syntax (lexical + AST only)
./bin/wonc parse ../../tests/language-spec/valid/types/struct_and_enum.won

# Check syntax and full Phase 2A semantics (with terminal or JSON output)
./bin/wonc check ../../examples/zlib_compression.won
./bin/wonc check ../../tests/language-spec/valid/types/struct_and_enum.won
./bin/wonc check --format=json ../../tests/language-spec/invalid/patterns/non_exhaustive_match.won

# Generate bindings directly from /usr/include/zlib.h
./bin/wonc bindings zlib

# Compile to native executable and execute (with optional --lto)
./bin/wonc build -o zlib_demo ../../examples/zlib_compression.won
./zlib_demo
```

### Feature & Component Status Summary

| Subsystem / Feature | Current Status | Specification Reference |
| :--- | :--- | :--- |
| Repository Architecture & Governance | `IMPLEMENTED` | [AGENTS.md](AGENTS.md), [GOVERNANCE.md](GOVERNANCE.md) |
| Architecture Decision Records (ADRs 001–010) | `ACCEPTED` | [docs/decisions/](docs/decisions/) |
| Ownership-Lite Architecture Decision (ADR-011) | `ACCEPTED` (Authorizes Phase 2B.1) | [ADR-011](docs/decisions/ADR-011-ownership-and-borrow-checking-semantics.md), [Approval Record](docs/decisions/OWNER-APPROVAL-ADR-011-2026-11-25.md) |
| Language Specification (Draft 0.1) | `SPECIFIED` | [docs/language-spec/](docs/language-spec/README.md) |
| Canonical Lexer (UTF-8 + Indentation EBNF) & Parser | `IMPLEMENTED` | [docs/language-spec/syntax-and-grammar.md](docs/language-spec/syntax-and-grammar.md) |
| Core Semantic Analysis & Type System (Phase 2A / 2A.1) | `IMPLEMENTED` | [docs/compiler/phase-2a-semantic-analysis.md](docs/compiler/phase-2a-semantic-analysis.md), [docs/compiler/phase-2a.1-correctness-hardening.md](docs/compiler/phase-2a.1-correctness-hardening.md) |
| Traits / Generic Monomorphization Solver | `SPECIFIED` (Phase 2B/2C) | [docs/language-spec/type-system.md](docs/language-spec/type-system.md) |
| Ownership-Lite Memory Model (Borrow Checker) | `SPECIFIED` (2B.0.1 Design Closed; Next: Phase 2B.1 Upon ADR-011 Approval) | [docs/language-spec/ownership-and-memory.md](docs/language-spec/ownership-and-memory.md), [Phase 2B.0.1 Architecture](docs/compiler/phase-2b.0-ownership-architecture.md), [Normative Proposal](docs/language-spec/ownership-and-borrowing.md), [ADR-011 (Proposed)](docs/decisions/ADR-011-ownership-and-borrow-checking-semantics.md) |
| Structured Concurrency & Zero-GIL | `SPECIFIED` (Runtime Phase 3) | [docs/language-spec/concurrency-model.md](docs/language-spec/concurrency-model.md) |
| Self-Hosting & Bootstrap Pipeline | `SPECIFIED` | [docs/language-spec/self-hosting-and-bootstrap.md](docs/language-spec/self-hosting-and-bootstrap.md) |
| Typed Intermediate Rep (TIR) | `SPECIFIED` (Phase 2C) | [docs/language-spec/compiler-and-ir-requirements.md](docs/language-spec/compiler-and-ir-requirements.md) |
| Initial C Codegen Strategy (ISO C11 Subset) | `IMPLEMENTED` (Strict Contract Verified) | [ADR-001](docs/decisions/ADR-001-c-codegen-backend.md), [Phase 2A.2 Capability Matrix](docs/compiler/phase-2a.2-backend-capability-matrix.md) |
| C Header Binding Engine (Clang AST) | `IMPLEMENTED` (zlib) | [ADR-006](docs/decisions/ADR-006-c-header-parsing-strategy.md) |
| Phase 0B Proof of Concept | `IMPLEMENTED` | [docs/phase-0-proof-of-concept.md](docs/phase-0-proof-of-concept.md) |
| AI Structured JSON Diagnostics | `IMPLEMENTED` | [docs/ai-native-design.md](docs/ai-native-design.md) |
| AI Agent Autonomous Self-Repair | `IMPLEMENTED` | [docs/phase-0b-ai-agent-experiment.md](docs/phase-0b-ai-agent-experiment.md) |
| Empirical Benchmark Suite (30 Runs) | `IMPLEMENTED` | [benchmarks/phase0/](benchmarks/phase0/) |
| Static Linking (-static) | `BLOCKED (Environment)` | [docs/phase-0-proof-of-concept.md#4-static-linking-evaluation--environment-status](docs/phase-0-proof-of-concept.md#4-static-linking-evaluation--environment-status) |
| Production Compiler (v1.0) | `PLANNED` | [docs/roadmap.md](docs/roadmap.md) |

---

## 5. Roadmap Overview

- **Phase 0 — Proof of Concept**: Repository foundation (0A) followed by minimal parser, zlib binding slice, static native build, and AI repair demonstration (0B).
- **Phase 1 — Language Foundations**: Formal grammar, AST definitions, lexical analysis, and basic TIR.
- **Phase 2 — Core Compiler**: Type inference engine, ownership-lite verification passes, and C codegen backend.
- **Phase 3 — Toolchain & AI Integration**: Command-line interface (`won build`, `won test`), JSON diagnostics, and agent query APIs.
- **Phase 4 — Security & Validation**: Formal safety verification, sandboxing, fuzzing, and stress test suites.
- **Phase 5 — Version 1.0 Public Release**: Stabilized core language, production packaging, and public reproducible benchmarks.
- **Phase 6 — Embedded Python Integration**: Zero-cost CPython C-API embedding.
- **Phase 7 — Long-Term Ecosystem**: Native LLVM/QBE backends, standard package distribution, and language maturation.

*For detailed phase deliverables and acceptance criteria, see [docs/roadmap.md](docs/roadmap.md).*

---

## 6. Architecture & Design Documentation

Comprehensive technical documentation is maintained under [`docs/`](docs/):

- **[Project Vision](docs/vision.md)**: Mission, user personas, and target use cases.
- **[Development Roadmap](docs/roadmap.md)**: Full milestone breakdown from Phase 0 to Phase 7.
- **[Language Design](docs/language-design.md)**: Syntax, type system, pattern matching, error handling, and traits.
- **[Compiler Architecture](docs/compiler-architecture.md)**: Multi-pass pipeline, TIR representation, and codegen.
- **[Performance Principles](docs/performance-principles.md)**: Performance engineering commitments and metrics.
- **[Memory Model](docs/memory-model.md)**: Detailed mechanics of the ownership-lite memory model.
- **[C Interoperability](docs/interoperability.md)**: Header parsing, FFI semantics, and memory boundaries.
- **[AI-Native Design](docs/ai-native-design.md)**: Machine-readable queries, diagnostics, and self-repair flows.
- **[Security Model](docs/security-model.md)**: Compile-time capabilities, sandboxing, and unsafe boundaries.
- **[Benchmarking Protocol](docs/benchmarking.md)**: Empirical methodology, competitor matrix, and reproducibility rules.
- **[Phase 0B Plan](docs/phase-0-proof-of-concept.md)**: Vertical slice specification for the upcoming proof of concept.

---

## 7. Community & Governance

- **[AGENTS.md](AGENTS.md)**: Mandatory rules and operational boundaries for AI coding agents.
- **[GOVERNANCE.md](GOVERNANCE.md)**: Project management, RFC process, and maintainer responsibilities.
- **[CONTRIBUTING.md](CONTRIBUTING.md)**: Contribution guidelines and workflow.
- **[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)**: Community standards and expectations.
- **[SECURITY.md](SECURITY.md)**: Vulnerability disclosure policy.
- **[LICENSE](LICENSE)**: Apache License, Version 2.0 (`SPDX-License-Identifier: Apache-2.0`).

---

## 8. License

This repository is licensed under the **Apache License, Version 2.0** (`SPDX-License-Identifier: Apache-2.0`). See [`LICENSE`](LICENSE) for the full, unmodified legal text.

- **Trademarks (Section 6)**: This license does not grant permission to use the trade names, trademarks, service marks, or product names of **KitiLab** or **WON**, except as required for reasonable and customary use in describing the origin of the Work.
- **Third-Party Libraries & Toolchains**: External libraries and compiler/system toolchains referenced or invoked externally by WON examples or host builds (such as `zlib`, `GCC`, and `Clang`) remain subject to their own respective upstream licenses and are not relicensed under Apache-2.0.

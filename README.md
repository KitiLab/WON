<div align="center">

# WON

**An independent, high-performance compiled programming language with ownership-lite memory safety, native execution, universal C interoperability and an AI-native compiler.**

*Developed by KitiLab*

---

[![Project Status](https://img.shields.io/badge/status-Phase%200B%20%28Proof%20of%20Concept%29-brightgreen.svg)](#current-development-status)
[![License](https://img.shields.io/badge/license-Pending%20Decision-orange.svg)](LICENSE_PENDING.md)
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
- **Safety Boundaries**: Thread safety enforced in safe code; explicit `unsafe { ... }` blocks for raw pointer manipulation and low-level FFI.
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
> **WON is in Phase 1 (Language Foundations). Phase 0B/0B.2 is completed and verified; Phase 1A formal specification is delivered.**
>
> A functioning bootstrap compiler (`wonc0` under `compiler/poc/`) validates the Phase 0B proof of concept (C header ingestion, strict ISO C11 code generation with `-Werror`, and autonomous AI coding-agent repair).
>
> **Phase 1A Formal Language Specification Suite** is published under [`docs/language-spec/`](docs/language-spec/README.md), establishing the formal EBNF grammar, type system, ownership-lite memory model, structured concurrency, error handling, Typed Intermediate Representation (TIR), and the 4-stage self-hosting roadmap.
>
> *Note on Static Linking*: Standalone static binary generation remains classified as `BLOCKED (Environment)` on this host due to missing system development archives (`libc.a`, `libz.a`). Dynamic native builds link against `/lib64/libz.so.1` and pass 100% of functional gates.

### Working CLI Commands (`wonc`)

```bash
# Build the bootstrap compiler
cd compiler/poc && go build -o bin/wonc ./cmd/wonc

# Check syntax and semantics (with terminal or JSON output)
./bin/wonc check examples/zlib_compression.won
./bin/wonc check --format=json examples/zlib_compression.won

# Generate bindings directly from /usr/include/zlib.h
./bin/wonc bindings zlib

# Compile to native executable and execute (with optional --lto)
./bin/wonc build -o zlib_demo examples/zlib_compression.won
./zlib_demo
```

### Feature & Component Status Summary

| Subsystem / Feature | Current Status | Specification Reference |
| :--- | :--- | :--- |
| Repository Architecture & Governance | `IMPLEMENTED` | [AGENTS.md](AGENTS.md), [GOVERNANCE.md](GOVERNANCE.md) |
| Architecture Decision Records (ADRs) | `IMPLEMENTED` | [docs/decisions/](docs/decisions/) |
| Language Specification (Draft 0.1) | `SPECIFIED` | [docs/language-spec/](docs/language-spec/README.md) |
| Syntax & Formal Grammar (EBNF) | `SPECIFIED` | [docs/language-spec/syntax-and-grammar.md](docs/language-spec/syntax-and-grammar.md) |
| Primitive & Composite Type System | `SPECIFIED` | [docs/language-spec/type-system.md](docs/language-spec/type-system.md) |
| Ownership-Lite Memory Model | `SPECIFIED` | [docs/language-spec/ownership-and-memory.md](docs/language-spec/ownership-and-memory.md) |
| Structured Concurrency & Zero-GIL | `SPECIFIED` | [docs/language-spec/concurrency-model.md](docs/language-spec/concurrency-model.md) |
| Self-Hosting & Bootstrap Pipeline | `SPECIFIED` | [docs/language-spec/self-hosting-and-bootstrap.md](docs/language-spec/self-hosting-and-bootstrap.md) |
| Typed Intermediate Rep (TIR) | `SPECIFIED` | [docs/language-spec/compiler-and-ir-requirements.md](docs/language-spec/compiler-and-ir-requirements.md) |
| Initial C Codegen Strategy (ISO C11) | `IMPLEMENTED` | [ADR-001](docs/decisions/ADR-001-c-codegen-backend.md) |
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
- **[LICENSE_PENDING.md](LICENSE_PENDING.md)**: Licensing deliberation and policy.

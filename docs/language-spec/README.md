# WON Language Specification

> **Specification Version**: Draft 0.1  
> **Status**: `SPECIFIED` (Under Architectural Review)  
> **Repository**: [https://github.com/KitiLab/WON](https://github.com/KitiLab/WON)  
> **Owner**: KitiLab  
> **Date**: November 2026

---

## 1. Executive Overview

WON is an independent, statically typed, compiled systems programming language engineered for high performance, readable syntax, and first-class automation. Its guiding architectural mission is:

> *"One tool. One file. Any C library. Built for AI agents."*

WON unites the performance and deterministic resource management of native systems languages with the readability and expressive simplicity of modern indentation-oriented syntax. WON is neither an interpreter, a JIT runtime, nor a wrapper around existing languages: it is designed from foundational principles with its own formal grammar, type system, ownership semantics, intermediate representation, and toolchain ecosystem.

---

## 2. Core Design Principles

1. **Near-C Native Performance (Non-Negotiable)**: Ahead-of-Time (AOT) compilation to native machine code with zero garbage collection pauses, zero Global Interpreter Lock (GIL), deterministic memory layouts, and aggressive link-time optimization.
2. **Readable & Familiar Syntax**: Python-inspired indentation-based block scoping paired with explicit type contracts at public boundaries, eliminating visual clutter without sacrificing static guarantees.
3. **Ownership-Lite Memory Safety**: Compile-time move and borrow semantics enforced without requiring complex, viral lifetime annotations (`'a`) in everyday application and systems code.
4. **Structured Concurrency**: Native, lightweight task spawning bounded by lexical scopes (`scope`), ensuring tasks cannot outlive their parents or leak background references.
5. **Universal C Interoperability**: Direct consumption of system C headers (`#import <header.h>`) with automatic ABI and type discovery, eliminating manual binding boilerplate.
6. **AI-Native Toolchain Integration**: Deterministic JSON-RPC diagnostics, precise byte-span error reporting, AST introspection, and structured patch proposals tailored for automated AI coding agents.
7. **Single-File Static Deployment**: Self-contained single-binary compilation with minimal external runtime dependencies.
8. **Self-Hosting Independence**: Architectural separation across language, compiler implementation, and backend, targeting an eventual self-hosted compiler written in WON.

---

## 3. Specification Structure

The WON language specification is organized into modular topic specifications:

| Document | Topic Covered | Status |
| :--- | :--- | :--- |
| **[syntax-and-grammar.md](syntax-and-grammar.md)** | Lexical structure, indentation tokenization, formal EBNF grammar, and canonical examples | `SPECIFIED` |
| **[type-system.md](type-system.md)** | Primitive types, composite types, local type inference, generics, traits, and dispatch | `SPECIFIED` |
| **[ownership-and-memory.md](ownership-and-memory.md)** | Value semantics, borrowing, lifetime-annotation-free safety, explicit sharing, and destruction | `SPECIFIED` |
| **[ownership-and-borrowing.md](ownership-and-borrowing.md)** | Phase 2B.0 normative ownership, CFG borrowing, NLL region & provenance rules, and test matrix | `SPECIFIED` (Proposed; Awaiting Owner Approval) |
| **[concurrency-model.md](concurrency-model.md)** | Structured concurrency, task scopes, channel/data transfer, thread safety, and synchronization | `SPECIFIED` |
| **[error-handling.md](error-handling.md)** | `Option[T]`, `Result[T, E]`, `?` operator, panic/trap semantics, and cleanup determinism | `SPECIFIED` |
| **[c-interop-and-unsafe.md](c-interop-and-unsafe.md)** | C header imports, ABI type mappings, pointer annotations, and unsafe code boundaries | `SPECIFIED` |
| **[modules-and-visibility.md](modules-and-visibility.md)** | Module layout, visibility (`pub`), imports, cyclic resolution, and compilation units | `SPECIFIED` |
| **[performance-and-execution.md](performance-and-execution.md)** | Native execution, memory layouts, calling conventions, and zero-cost abstractions | `SPECIFIED` |
| **[self-hosting-and-bootstrap.md](self-hosting-and-bootstrap.md)** | Multi-stage bootstrap architecture, self-hosting roadmap, and Go compiler retirement criteria | `SPECIFIED` |
| **[compiler-and-ir-requirements.md](compiler-and-ir-requirements.md)** | Pipeline architecture, Typed Intermediate Representation (TIR), and multi-backend design | `SPECIFIED` |
| **[safety-and-security-semantics.md](safety-and-security-semantics.md)** | Safe vs unsafe boundaries, compile-time capability analysis, and OS sandboxing (Landlock/Seatbelt) | `SPECIFIED` |
| **[versioning-and-compatibility.md](versioning-and-compatibility.md)** | Language editions, ABI/API stability, compiler release lifecycle, and deprecation protocols | `SPECIFIED` |
| **[reconciliation-and-glossary.md](reconciliation-and-glossary.md)** | Cross-document consistency review, architectural glossary, and design decision register | `SPECIFIED` |
| **[phase-1a.1-semantic-closure.md](phase-1a.1-semantic-closure.md)** | Semantic closure audit log, defect resolutions, and formal soundness models | `SPECIFIED` |

---

## 4. Normative Vocabulary & RFC 2119 Standards

The key words **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHALL NOT**, **SHOULD**, **SHOULD NOT**, **RECOMMENDED**, **NOT RECOMMENDED**, **MAY**, and **OPTIONAL** in this specification suite are to be interpreted as described in BCP 14 ([RFC 2119](https://tools.ietf.org/html/rfc2119), [RFC 8174](https://tools.ietf.org/html/rfc8174)):

- **MUST / SHALL / REQUIRED**: Absolute requirement of the specification. Conforming implementations that fail to satisfy this rule are non-compliant.
- **MUST NOT / SHALL NOT**: Absolute prohibition of the specification.
- **SHOULD / RECOMMENDED**: Valid reasons may exist in particular circumstances to ignore a particular item, but the full implications must be understood and carefully weighed before choosing a different course.
- **SHOULD NOT / NOT RECOMMENDED**: Valid reasons may exist when the particular behavior is acceptable or even useful, but the full implications should be understood and the case carefully weighed.
- **MAY / OPTIONAL**: The item is truly optional. One implementation may choose to include the item because a particular marketplace requires it or because it enhances the product, while another implementation may omit the same item.

---

## 5. Feature Status Classification

To ensure complete transparency and prevent confusion between design targets and working software, every language feature, compiler construct, and runtime capability is assigned an explicit status:

| Status Label | Formal Definition |
| :--- | :--- |
| `IMPLEMENTED` | Code exists in the repository, compiles cleanly, passes automated CI verification, and is delivered in active builds. |
| `EXPERIMENTAL` | Implemented behind a compiler feature flag or staging branch; semantics, syntax, and ABI are subject to breaking changes. |
| `SPECIFIED` | Formally defined in this specification suite and reviewed in architectural records; implementation has not yet been merged. |
| `PLANNED` | Accepted in high-level roadmap and design vision, but formal syntax and type-theoretic rules remain open for elaboration. |
| `UNDER INVESTIGATION` | Under active research, benchmarking, or formal verification; trade-offs are documented without final architectural commitment. |
| `NOT IMPLEMENTED` | Rejected, indefinitely deferred, or explicitly outside the scope of current language phases. |

> [!IMPORTANT]
> A feature marked as `SPECIFIED` is a formal specification commitment, **not an existing compiler feature**. The Phase 0 bootstrap compiler (`wonc0` under `compiler/poc/`) supports only the Phase 0 vertical slice subset documented in [`docs/phase-0-proof-of-concept.md`](../phase-0-proof-of-concept.md). Conformance to this Draft 0.1 specification will be delivered incrementally across Phase 1 through Phase 5.

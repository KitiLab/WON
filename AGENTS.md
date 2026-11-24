# AGENTS.md — Persistent Governance & Rules for AI Agents

> **Scope**: This document defines persistent operational rules, architectural constraints, and engineering governance standards for all AI coding agents (and human contributors collaborating with them) working on the WON programming language repository.

---

## 1. Core Operating Philosophy

WON is an independent, high-performance compiled programming language developed by **KitiLab**. Its mission is:

> *"One tool. One file. Any C library. Built for AI agents."*

All AI agents operating within this repository must adhere strictly to the following 20 core engineering rules:

1. **Read Architecture First**: Read the relevant architecture and design documentation (`docs/`) before implementing any feature or modifying existing subsystems.
2. **Strict Scope Control**: Do not implement unapproved features outside the explicitly assigned milestone or task scope.
3. **Preserve Language Semantics**: Never silently change established language semantics or syntax.
4. **No Feature Invention**: Never invent language features, keywords, or syntax on your own. All language design must stem from accepted specifications or ADRs.
5. **Mandatory Test Coverage**: Every implemented feature must be accompanied by comprehensive automated tests verifying positive and negative cases.
6. **Regression Protection**: Every bug fix must include a regression test reproducing the issue before applying the fix, where practical.
7. **Empirical Truth**: Never claim that a test passed unless it was actually executed and the exit status verified.
8. **Measured Performance Only**: Never claim performance improvements without concrete, reproducible measurements.
9. **Fair Benchmarks**: Benchmark strictly against equivalent, optimized implementations running on identical hardware under identical constraints.
10. **Modular Architecture**: Keep compiler architecture modular, maintaining clean boundaries between lexer, parser, type checker, TIR, code generator, and CLI.
11. **Minimalist Implementation**: Prefer simple, maintainable implementations over clever, deeply nested abstractions (YAGNI).
12. **Minimal Dependencies**: Avoid introducing unnecessary external dependencies. Prioritize standard libraries and lightweight, verifiable tools.
13. **Secret Hygiene**: Never commit, log, or expose access tokens, private keys, passwords, or credentials.
14. **Stable Public Interfaces**: Do not silently alter public APIs, compiler flags, or output formats without documenting the change.
15. **Document Architecture Decisions**: Record important architectural trade-offs as Architectural Decision Records (ADRs) in `docs/decisions/`.
16. **Documentation Synchronization**: Keep the `README.md` and documentation in `docs/` strictly synchronized with the actual implementation state.
17. **Explicit Status Classification**: Clearly distinguish implemented, experimental, planned, and unsupported features in all documentation and responses.
18. **Correctness Before Speed**: Never sacrifice correctness, memory safety, or stability to achieve superior benchmark numbers.
19. **AI Neutrality**: Preserve the compiler's independence from specific AI providers. Standard JSON interfaces, structured diagnostics, and CLI streams must remain vendor-agnostic.
20. **Surgical Modifications**: Never rewrite unrelated files or refactor code outside your task scope without an explicit engineering justification.

---

## 2. Feature Status Classification

When documenting or discussing any language construct, library module, or compiler subsystem, agents MUST classify its status using one of the following standardized labels:

| Status Label | Definition |
| :--- | :--- |
| `IMPLEMENTED` | Code exists, is tested in CI, and is part of the current buildable release. |
| `EXPERIMENTAL` | Implemented behind a feature flag or staging branch; semantics subject to change. |
| `SPECIFIED` | Formally specified in documentation and accepted in an ADR, but code is not yet written. |
| `PLANNED` | Included in the roadmap for future milestones; design details remain open. |
| `UNDER INVESTIGATION` | Currently under research, evaluation, or benchmarking; no final decision reached. |
| `NOT IMPLEMENTED` | Rejected, deferred indefinitely, or explicitly outside the scope of current phases. |

---

## 3. Communication & Code Quality Rules

- **Language of Communication**: All user interactions and technical reports must be delivered in English.
- **Source Code Comments**: In accordance with project source hygiene, all code docstrings, interface descriptions, and algorithmic comments within source files must be written in **Simplified Chinese** (`简体中文`).
- **Precision & Honesty**: If a command fails, a tool is missing, or an assumption is disproven, state the facts directly. Do not mask errors or fabricate results.

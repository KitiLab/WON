# WON Language Specification Test Fixtures

> **Specification Version**: Draft 0.1  
> **Status**: `SPECIFIED` (Specification Test Suite)  
> **Date**: November 2026

---

## 1. Purpose of Specification Fixtures

The files in this directory tree define the **normative behavioral contracts** of the WON programming language as specified in `docs/language-spec/`.

> [!IMPORTANT]
> **These files are specification fixtures, NOT tests for the Phase 0 bootstrap compiler.**  
> The Phase 0 bootstrap compiler (`wonc0` under `compiler/poc/`) supports only the provisional brace-delimited vertical slice described in `docs/phase-0-proof-of-concept.md`. These fixtures define the canonical Python-inspired indentation grammar and type semantics that the full compiler will implement across Phase 1 through Phase 3.

---

## 2. Directory Structure

```text
tests/language-spec/
├── README.md
├── valid/
│   ├── basics/hello.won
│   ├── types/struct_and_enum.won
│   ├── ownership/borrow_and_move.won
│   ├── errors/result_propagation.won
│   ├── concurrency/structured_scope.won
│   └── ffi/zlib_interop.won
└── invalid/
    ├── syntax/tab_indentation.won
    ├── types/implicit_coercion.won
    ├── ownership/use_after_move.won
    ├── concurrency/race_condition_capture.won
    └── ffi/foreign_call_outside_unsafe.won
```

---

## 3. Fixture Metadata Conventions

Every specification test fixture begins with a structured metadata header:

```won
## [SPEC-FIXTURE]
## Category: Valid / Invalid
## Feature: Language construct being tested
## Spec Reference: docs/language-spec/<module>.md
## Intended Semantics / Expected Diagnostic: Description
```

# WON Language Specification Test Fixtures

> **Specification Version**: Draft 0.1  
> **Status**: `IMPLEMENTED` (Canonical Lexer/Parser + Phase 2A Core Semantics Verified; Borrow Checker, Traits & Concurrency Deferred to Phase 2B/3)  
> **Date**: November 2026

---

## 1. Purpose of Specification Fixtures

The files in this directory tree define the **normative behavioral contracts** of the WON programming language as specified in [`docs/language-spec/`](../../docs/language-spec/README.md).

> [!NOTE]
> **Phase 2A Verification Scope**:  
> As of **Phase 2A**, `wonc` enforces strict UTF-8 lexing (`WON-LEX-004`), canonical Draft 0.1 AST parsing, two-pass hierarchical symbol resolution (`WON-NAM-001`..`006`), bidirectional type inference without implicit coercions (`WON-TYP-001`..`007`), struct/enum/method validation (`WON-ADT-001`..`007`), exhaustive pattern matching (`WON-MAT-001`..`004`), `Option`/`Result`/`?` checking (`WON-ERR-001`/`003`), and C FFI boundary enforcement (`WON-SEC-001`, `WON-FFI-*`) across `TestPhase2ASemanticValidFixtures` and `TestPhase2ASemanticInvalidFixtures`. Ownership-lite move/borrow (`WON-MEM-*`) and concurrency checks (`WON-CON-*`) remain deferred to Phase 2B/3.

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
    ├── syntax/
    │   ├── tab_indentation.won
    │   ├── brace_block_syntax.won
    │   ├── missing_block_body.won
    │   ├── unexpected_indentation.won
    │   ├── inconsistent_dedentation.won
    │   ├── missing_closing_delimiter.won
    │   ├── invalid_assignment_target.won
    │   ├── malformed_generic_args.won
    │   ├── invalid_interpolation_delimiter.won
    │   ├── invalid_type_annotation.won
    │   ├── malformed_range_expression.won
    │   └── malformed_function_declaration.won
    ├── types/implicit_coercion.won
    ├── ownership/use_after_move.won
    ├── concurrency/race_condition_capture.won
    └── ffi/foreign_call_outside_unsafe.won
```

---

## 3. Fixture Classification & Verification Manifest

| Fixture File | Lexical Result | Parse Result | Intended Semantic / Runtime Result | Phase 1B Status |
| :--- | :---: | :---: | :--- | :--- |
| `valid/basics/hello.won` | Valid | Valid AST | Compiles & executes cleanly | Frontend Verified (`PASS`) |
| `valid/types/struct_and_enum.won` | Valid | Valid AST | Typechecks structs/enums/match | Frontend Verified (`PASS`); Semantics Phase 2 |
| `valid/ownership/borrow_and_move.won` | Valid | Valid AST | Enforces single move & non-overlapping borrow | Frontend Verified (`PASS`); Semantics Phase 2 |
| `valid/errors/result_propagation.won` | Valid | Valid AST | Lowers `?` & `From::from` | Frontend Verified (`PASS`); Semantics Phase 2 |
| `valid/concurrency/structured_scope.won` | Valid | Valid AST | Executes bounded nursery tasks | Frontend Verified (`PASS`); Runtime Phase 3 |
| `valid/ffi/zlib_interop.won` | Valid | Valid AST | C FFI `Result` + `zlib` execution | Frontend Verified (`PASS`); Semantics Phase 2 |
| `invalid/syntax/tab_indentation.won` | **Reject (`WON-SYN-001`)** | Rejected | Forbidden Tab indentation | **VERIFIED (`PASS`)** |
| `invalid/syntax/brace_block_syntax.won` | Valid | **Reject (`WON-SYN-005`)** | Forbidden Phase 0 `{ ... }` block | **VERIFIED (`PASS`)** |
| `invalid/syntax/missing_block_body.won` | Valid | **Reject (`WON-SYN-004`)** | Missing indented block after `:` | **VERIFIED (`PASS`)** |
| `invalid/syntax/unexpected_indentation.won` | Valid (INDENT) | **Reject (`WON-SYN-002`)** | Unexpected indentation | **VERIFIED (`PASS`)** |
| `invalid/syntax/inconsistent_dedentation.won` | **Reject (`WON-SYN-003`)** | Rejected | Mismatched dedent column | **VERIFIED (`PASS`)** |
| `invalid/syntax/missing_closing_delimiter.won` | Valid | **Reject (`WON-SYN-007`)** | Unclosed `(` delimiter | **VERIFIED (`PASS`)** |
| `invalid/syntax/invalid_assignment_target.won` | Valid | **Reject (`WON-SYN-009`)** | Invalid rvalue target in assignment | **VERIFIED (`PASS`)** |
| `invalid/syntax/malformed_generic_args.won` | Valid | **Reject (`WON-SYN-005`)** | Empty generic arg list `[]` | **VERIFIED (`PASS`)** |
| `invalid/syntax/invalid_interpolation_delimiter.won` | **Reject (`WON-SYN-007`)** | Rejected | Unclosed `{` inside string literal | **VERIFIED (`PASS`)** |
| `invalid/syntax/invalid_type_annotation.won` | Valid | **Reject (`WON-SYN-005`)** | Missing type after `:` | **VERIFIED (`PASS`)** |
| `invalid/syntax/malformed_range_expression.won` | Valid | **Reject (`WON-SYN-009`)** | Chained range `0..10..20` | **VERIFIED (`PASS`)** |
| `invalid/syntax/malformed_function_declaration.won` | Valid | **Reject (`WON-SYN-005`)** | Invalid `fn` identifier token | **VERIFIED (`PASS`)** |
| `invalid/types/implicit_coercion.won` | Valid | Valid AST | Reject `WON-TYP-001` | Syntax Valid (`PASS`); Typecheck Phase 2 |
| `invalid/ownership/use_after_move.won` | Valid | Valid AST | Reject `WON-MEM-001` | Syntax Valid (`PASS`); Borrowcheck Phase 2 |
| `invalid/concurrency/race_condition_capture.won` | Valid | Valid AST | Reject `WON-CON-001` | Syntax Valid (`PASS`); Concurrency Phase 3 |
| `invalid/ffi/foreign_call_outside_unsafe.won` | Valid | Valid AST | **Reject (`WON-SEC-001`)** | **VERIFIED (`PASS`)** (Active FFI checker) |

---

## 4. Fixture Metadata Conventions

Every specification test fixture begins with a structured metadata header:

```won
## [SPEC-FIXTURE]
## Category: Valid / Invalid
## Feature: Language construct being tested
## Spec Reference: docs/language-spec/<module>.md
## Intended Semantics / Expected Diagnostic: Description
```

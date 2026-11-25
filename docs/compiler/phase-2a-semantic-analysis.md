# WON Phase 2A — Semantic Analysis & Type Checking Architecture

> **Document Status**: `IMPLEMENTED` (Hardened in [Phase 2A.1](phase-2a.1-correctness-hardening.md))  
> **Milestone**: Phase 2A / 2A.1 — Core Semantic Analysis & Type System Implementation  
> **Code Location**: `compiler/poc/internal/{lexer,types,symbols,checker}`  
> **Companion Documents**: [Typed Semantic Model (`TypedProgram`)](phase-2a-typed-semantic-model.md), [Phase 2A.1 Correctness Hardening](phase-2a.1-correctness-hardening.md)

---

## 1. Multi-Pass Semantic Analysis Pipeline

Phase 2A transforms the `wonc` frontend into a genuine static semantic compiler pipeline:

1. **Strict UTF-8 Lexing (`internal/lexer`)**:
   - Decodes source bytes via `utf8.DecodeRuneInString` into `{rune, byteLen, valid}` entries instead of lossy `[]rune(src)` replacement.
   - Rejects malformed UTF-8 byte sequences with `WON-LEX-004` at exact 1-based line, 1-based Unicode code-point column, and 0-based UTF-8 byte offset while allowing valid encoded `U+FFFD` (`0xEF 0xBF 0xBD`).
2. **Stage 1 — Top-Level Declaration Pre-Collection (`declarations.go`)**:
   - Registers all top-level `fn`, `struct`, `enum`, and `type` alias symbols in `GlobalScope` before checking any signatures or bodies, enabling forward references and mutually recursive types/functions.
   - Detects top-level duplicate names (`WON-NAM-002`) and resolves/flags imports (`WON-NAM-004`).
   - Explicitly classifies unimplemented future features (`trait`, `impl Trait for Type`, `scope`, `spawn`, `mod`) with `WON-SEM-010` rather than silently ignoring them.
3. **Stage 2 — Signature, Field, Variant, and Method Resolution (`declarations.go`)**:
   - Resolves parameter/return types, struct fields (rejecting bare DSTs with `WON-TYP-006` and duplicates with `WON-ADT-003`), enum variant payloads (unit, tuple, struct variants), generic type parameters, and inherent `impl` blocks.
4. **Stage 3 — Bidirectional Statement, Expression & Pattern Verification (`checker.go`, `statements.go`, `expressions.go`, `patterns.go`)**:
   - Checks every function and inherent method body using contextual expected-type propagation (`checkExpr(expr, expected, scope)`).

---

## 2. Implemented Semantic Guarantees

| Capability | Diagnostic Codes | Description |
| :--- | :--- | :--- |
| UTF-8 Source Validation | `WON-LEX-004` | Exact byte-offset tracking and rejection of invalid UTF-8 sequences without offset drift. |
| Scope & Name Resolution | `WON-NAM-001`..`006` | Undefined identifiers, same-scope duplicate declarations, unknown types, unresolved imports, invalid `break`/`continue`/`return`, unknown inherent methods. |
| Bidirectional Type Inference | `WON-TYP-001`..`007` | Contextual unsuffixed integer typing (including `-128` signed min boundaries), overflow checks (`WON-TYP-003`), strict no-implicit-coercion (`WON-TYP-001`), `bool`-only conditions (`WON-TYP-002`), explicit `as` validation (`WON-TYP-005`), DST by-value rejection (`WON-TYP-006`), function return checks (`WON-TYP-007`). |
| Struct & Enum Semantics | `WON-ADT-001`..`007` | Field existence, completeness, duplicate initialization, `let`/`var` field mutability (`WON-ADT-004`), enum variant lookup & payload arity (`WON-ADT-006`), generic substitution. |
| Pattern Matching & Exhaustiveness | `WON-MAT-001`..`004` | Exhaustiveness over `enum`, `Option[T]`, `Result[T, E]`, `bool`, and open domains (`WON-MAT-001`), pattern compatibility (`WON-MAT-002`), arm type agreement (`WON-MAT-003`), unreachable/duplicate arm rejection (`WON-MAT-004`), guard `if` handling (guarded arms do not count toward exhaustiveness). |
| `Option` / `Result` & `?` | `WON-ERR-001`, `WON-ERR-003` | Validates `?` inner value is `Result[T, E]` or `Option[T]`, enclosing function returns compatible `Result[_, E]` or `Option[_]`, and requires context for bare `None` (`WON-ERR-003`). |
| C FFI & Unsafe Preservation | `WON-FFI-001`..`006`, `WON-SEC-001`, `WON-MUT-001` | Preserves strict Clang-extracted C ABI validation for `#import <zlib.h>`, `unsafe:` boundary enforcement, and mutable pointer/reference safety with AI JSON auto-fix spans. |

> [!NOTE]
> **Real-World Bug Caught by Phase 2A Activation**: When enabling binary operator type checking (`==`), the semantic checker exposed a pre-existing latent C-style coercion in `examples/zlib_compression.won`, `benchmarks/phase0/won/bench.won`, and `correctness_test.go`, where `bytes_equal(...)` (returning `bool`) had been compared with integer literal `0` (`if is_equal == 0:`). The code was updated to `if !is_equal:` without altering runtime behavior.

---

## 3. Verification Matrix & Deferred Scope Boundary

- **Verified by**:
  - `compiler/poc/internal/types/types_test.go`
  - `compiler/poc/internal/symbols/symbols_test.go`
  - `compiler/poc/internal/checker/{checker_test,semantic_test,fuzz_test}.go` (including native Go fuzz runs `FuzzLexerParse` and `FuzzSemanticChecker`)
  - `compiler/poc/tests/{semantic_fixtures_test,spec_fixtures_test,protocol_test,correctness_test,integration_test}.go`
- **Explicitly Deferred Beyond Phase 2A**:
  - Ownership-lite move/borrow provenance checker (`WON-MEM-001`/`002`, Phase 2B)
  - Full trait solver & `From`/`Into` cross-error coercion on `?` (`WON-ERR-002`) and monomorphization
  - Structured concurrency `Send`/`Sync` & nursery runtime (`scope`/`spawn`, Phase 3)
  - Control-flow graph TIR lowering (ADR-009, Phase 2C) and C11 codegen for structs/enums/`match`/`?`

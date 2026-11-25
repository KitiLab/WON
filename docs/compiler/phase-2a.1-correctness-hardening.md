# WON Phase 2A.1 — Semantic Correctness Hardening

> **Document Status**: `IMPLEMENTED`  
> **Milestone**: Phase 2A.1 — Semantic Correctness Hardening  
> **Builds Upon**: [Phase 2A Semantic Analysis](phase-2a-semantic-analysis.md) & [Typed Semantic Model](phase-2a-typed-semantic-model.md)  
> **Normative References**: [Type System](../language-spec/type-system.md), [Ownership and Memory Model](../language-spec/ownership-and-memory.md)

---

## 1. Arbitrary-Precision Integer Representation and Parsing

Phase 2A stored `ast.IntegerLiteral.Value` as `int64` via `strconv.ParseInt(..., 64)`, truncating or overflowing 64-bit signed limits for `u64`, `usize`, `i128`, and `u128`. Phase 2A.1 replaces lossy 64-bit parsing across the entire frontend pipeline:

- **AST Representation ([ast.go](../../compiler/poc/internal/ast/ast.go))**: `IntegerLiteral` stores an authoritative `*big.Int` (`Value`), original source spelling (`Raw`), explicit type suffix (`Suffix`), and parse validity flag (`Valid bool`).
- **Lexer Arbitrary-Precision Parser ([lexer.go](../../compiler/poc/internal/lexer/lexer.go))**: `ParseBigIntLiteralWithSuffix` strips recognized suffixes longest-first (`isize`, `usize`, `i128`, `u128`, `i16`, `u16`, `i32`, `u32`, `i64`, `u64`, `i8`, `u8`), strips underscore separators (`_`), validates every digit against the parsed radix (base 2, 8, 10, 16), and constructs `*big.Int` cleanly via `SetString`.
- **No Silent Parse Fallback ([parser.go](../../compiler/poc/internal/parser/parser.go))**: Malformed numeric literals emit `WON-SYN-008` and produce `IntegerLiteral{Valid: false, Value: nil}` rather than defaulting to `0`. Invalid literals propagate `types.Invalid` in the checker without emitting spurious downstream `WON-TYP-003` errors.

---

## 2. Exact Supported Numeric Ranges (`FitsBigIntLiteral`)

Range verification in [relations.go](../../compiler/poc/internal/types/relations.go) compares parsed `math/big.Int` values against exact arbitrary-precision bounds (`TargetPointerBits = 64` for LP64 `linux/amd64`):

| Type | Exact Signed / Unsigned Bounds | Decimal Limit Examples |
| :--- | :--- | :--- |
| `i8` | $[-2^7, 2^7 - 1]$ | `-128 .. 127` |
| `i16` | $[-2^{15}, 2^{15} - 1]$ | `-32768 .. 32767` |
| `i32` | $[-2^{31}, 2^{31} - 1]$ | `-2147483648 .. 2147483647` |
| `i64`, `isize` | $[-2^{63}, 2^{63} - 1]$ | `-9223372036854775808 .. 9223372036854775807` |
| `i128` | $[-2^{127}, 2^{127} - 1]$ | `-170141183460469231731687303715884105728 .. 170141183460469231731687303715884105727` |
| `u8` | $[0, 2^8 - 1]$ | `0 .. 255` |
| `u16` | $[0, 2^{16} - 1]$ | `0 .. 65535` |
| `u32` | $[0, 2^{32} - 1]$ | `0 .. 4294967295` |
| `u64`, `usize` | $[0, 2^{64} - 1]$ | `0 .. 18446744073709551615` |
| `u128` | $[0, 2^{128} - 1]$ | `0 .. 340282366920938463463374607431768211455` |

---

## 3. Overflow, Unary Negation, and Codegen Boundaries

- **Unary Signed Minimum Boundaries**: Both unsuffixed (`let d: i128 = -170141183460469231731687303715884105728`) and suffixed (`-128i8`) minimum values parse positive magnitude into `*big.Int` first, negate once in `checkUnaryExpr("-")`, and check exact signed/unsigned bounds against the target type (`WON-TYP-003` for overflows or negative literals assigned to unsigned types).
- **Unsuffixed Overflow Check**: Unsuffixed integer literals without contextual expectations default to `i64` and reject values outside `i64` bounds (`WON-TYP-003`).
- **Explicit `i128`/`u128` C11 Backend Gate**: Semantic checking supports full 128-bit types (`wonc check`), while `markCodegenIfUnsupportedType` sets `TypedProgram.CodegenSupported = false` so `wonc build` explicitly refuses codegen with `WON-GEN-002`.

---

## 4. Raw-Pointer Dereference Safety (`WON-SEC-002`)

Per [Ownership and Memory Model](../language-spec/ownership-and-memory.md#7-raw-pointers-and-unsafe-operations):

- **Safe Construction**: Creating raw pointers (`&x as *const T`, `&mut x as *mut T`, `addr as *const T` for `usize`/`isize`) does **not** require `unsafe:`.
- **Unsafe Dereference Reads ([expressions.go](../../compiler/poc/internal/checker/expressions.go))**: Unary dereference `*ptr` on `*const T` or `*mut T` outside `unsafe:` emits **`WON-SEC-002`**, while safe `&T`/`&mut T` dereferencing remains allowed outside `unsafe:`.
- **Unsafe Dereference Writes ([statements.go](../../compiler/poc/internal/checker/statements.go))**: Assigning through `*ptr = value` where `ptr` is `*mut T` (or `*const T`) outside `unsafe:` also enforces **`WON-SEC-002`** (in addition to `WON-MUT-001` for immutable targets).

---

## 5. Explicit Unsafe Boundaries, Pointer Arithmetic & Cast Policy

- **Foreign Calls**: Imported C FFI calls require `unsafe:` (`WON-SEC-001`).
- **Pointer Arithmetic**: Binary `+` and `-` directly on `*const T` / `*mut T` are explicitly rejected (`WON-TYP-002`); address arithmetic must explicitly cast via `usize` where intended.
- **Pointer Cast Matrix (`CanCast`)**:
  - Allowed safely: `&T as *const T`, `&mut T as *mut T`, `&mut T as *const T`, `usize`/`isize` $\leftrightarrow$ `*const T`/`*mut T`, and `*const T` $\leftrightarrow$ `*mut U`.
  - Rejected (`WON-TYP-005`): `&T as *mut T` (mutability escalation), non-pointer-width integer to raw pointer (`i32 as *const T`), direct cross-element reference reinterpretation (`&u32 as *const u8`).

---

## 6. Built-In Method Signature Contracts

Method checking resolves receiver type and method signature **before** type-checking arguments so expected parameter types guide bidirectional checking and every argument is visited once:

| Receiver Type(s) | Method Signature | Mutability | Return Type | Status |
| :--- | :--- | :--- | :--- | :--- |
| `&str`, `String`, `[T; N]`, `[T]` | `as_ptr() -> *const Elem` (`u8` for strings) | Shared | `*const Elem` | Prototype Built-in (0 args enforced) |
| `String`, `[T; N]`, `[T]` | `as_mut_ptr() -> *mut Elem` | Mutable (`var` / `&mut`) | `*mut Elem` | Prototype Built-in (0 args enforced) |
| `&str`, `String` | `as_bytes() -> &[u8]` | Shared | `&[u8]` | Prototype Built-in (0 args enforced) |
| `&str`, `String`, `[T; N]`, `[T]` | `len() -> u64` | Shared | `u64` | Prototype Built-in (0 args enforced) |
| `&str`, `String`, `[T; N]`, `[T]` | `is_empty() -> bool` | Shared | `bool` | Prototype Built-in (0 args enforced) |
| `String` | `push_str(value: &str) -> unit` | Mutable (`var` / `&mut`) | `unit` | Checked; `WON-GEN-002` in codegen (1 `&str` arg enforced) |

Extra arguments, missing arguments, or mismatched argument types emit a single `WON-TYP-001` diagnostic.

---

## 7. User-Defined Inherent Method Validation

When calling `recv.method(args...)` on a user `struct`/`enum`:

1. Check receiver expression once and unwrap `&`/`&mut`.
2. Resolve owning `Symbol` and look up `methodSym` in `ownerSym.Methods` (user methods take precedence over built-in method names).
3. Reject static associated functions (`!methodSym.HasSelf`) with `WON-NAM-006`.
4. Enforce `SelfIsMut` (`&mut self`) against receiver mutability (`WON-MUT-001`, preserving JSON `LetTokenSpan` auto-fix edits).
5. Strip implicit `self` from `ParamTypes` (done during declaration resolution) and validate `e.Args` **exactly once** via `checkCallArgsAgainstSignature` with expected parameter types.
6. Record `TypedProgram.CallTargets[e] = methodSym`.

---

## 8. Match Result-Type Joins (`CommonType`)

`checkMatchExpr` replaces the previous bidirectional assignment check with `types.Context.CommonType(a, b)`:

- Identical types return `(a, true)`.
- `Never` (`!`) unifies with the other branch's type.
- `&mut T` + `&T` (same `T`) joins strictly to immutable **`&T`** (`*mut T` + `*const T` joins to **`*const T`**).
- Tuples join element-wise recursively.
- Incompatible pairs (`i32` vs `i64`, `&(&mut i32)` vs `&(&i32)`, distinct structs, incompatible tuple fields) fail with `WON-MAT-003`.
- `TypedProgram.ExprTypes[matchExpr]` stores the computed common `TypeID` (e.g. `&i32` when joining `&mut i32` and `&i32`).

---

## 9. Comparison Compatibility (`CommonType` & Comparability)

Comparison operators (`==`, `!=`, `<`, `<=`, `>`, `>=`) check operands via `CommonType(lt, rt)` after contextual literal propagation (including negated unsuffixed literals):

- Asymmetric pairs without a strict common type (`i32 == i64`, `&(&mut i32) == &(&i32)`) fail with `WON-TYP-001`.
- Ordering (`<`, `<=`, `>`, `>=`) requires `common` to be numeric or `char` (`WON-TYP-002` otherwise).
- Equality (`==`, `!=`) requires `isEqualityComparable(common)` (numeric, `bool`, `char`, string types, raw pointers, enums, or references to equality-comparable/slice types), rejecting unsupported types such as un-traited structs with `WON-TYP-002`.

---

## 10. Test Coverage Summary

- **Types Package**: [types_test.go](../../compiler/poc/internal/types/types_test.go) (`TestFitsBigIntLiteralExactBounds`, `TestCommonTypeJoin`)
- **Integer Literals**: [integer_literal_test.go](../../compiler/poc/internal/checker/integer_literal_test.go) (`i8..i128` & `u8..u128` & `isize`/`usize` bounds, +/-1 overflows, negative unsigned, multi-base, underscores, suffixes, malformed numeric parse errors)
- **Pointers, Methods, Match & Comparisons**: [hardening_test.go](../../compiler/poc/internal/checker/hardening_test.go) (`WON-SEC-002`, single-diagnostic method checks, `CommonType` / `ExprTypes` join assertions, comparison safety)
- **Fuzzing**: [fuzz_test.go](../../compiler/poc/internal/checker/fuzz_test.go) seeded with `u128`/`-2^127` bounds, `0x`, raw-ptr deref, bad method args, and reference joins
- **End-to-End Fixtures**: [semantic_fixtures_test.go](../../compiler/poc/tests/semantic_fixtures_test.go) covering `full_width_integers.won`, `int_overflow_u128.won`, `raw_ptr_deref_outside_unsafe.won`, `invalid_method_args.won`, `match_incompatible_join.won`, and `wonc build` `WON-GEN-002` checks

---

## 11. Remaining Limitations (Out of Scope)

- Ownership-lite move tracking and lexical borrow provenance (`WON-MEM-*`) — planned Phase 2B
- Full trait solver (`PartialEq`, `From`/`Into` conversion on `?`) & generic monomorphization — planned Phase 2B/2C
- Structured CFG TIR lowering (ADR-009) & expanded C11 backend for structs/enums/`match`/`i128` — planned Phase 2C
- Concurrency `Send`/`Sync` & nursery runtime — planned Phase 3
- Standalone `-static` native linking remains `BLOCKED (Environment)` on host lacking `libc.a`/`libz.a`

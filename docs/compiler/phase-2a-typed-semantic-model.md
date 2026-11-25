# WON Phase 2A — Typed Semantic Model (`TypedProgram`)

> **Document Status**: `IMPLEMENTED`  
> **Milestone**: Phase 2A — Core Semantic Analysis & Type System Implementation  
> **Implementation Packages**: `compiler/poc/internal/types`, `compiler/poc/internal/symbols`, `compiler/poc/internal/checker`  
> **Normative References**: [Type System](../language-spec/type-system.md), [Compiler & IR Requirements (ADR-009)](../language-spec/compiler-and-ir-requirements.md)

---

## 1. Architectural Role & Backend Decoupling

Phase 2A establishes the formal typed semantic boundary (`TypedProgram`) between AST parsing and downstream lowering:

```text
  AST (*ast.Program)
         │
         ▼   Two-Pass Collector + Bidirectional Checker
  TypedProgram
    ├── Types       (*types.Context)       Interned canonical TypeID table
    ├── Symbols     (*symbols.Table)       Hierarchical scopes, fields, variants, methods
    ├── ExprTypes   map[ast.Expr]TypeID    Every type-checked expression mapped to a TypeID
    ├── CallTargets map[ast.Expr]*Symbol   Resolved call/ctor/method targets
    ├── FieldRefs   map[ast.Node]*FieldInfo Struct/variant field resolution metadata
    ├── VariantRefs map[ast.Node]*VariantInfo Enum/Option/Result variant resolution metadata
    └── CodegenSupported (bool + Span)     Explicit Phase 0B/1B C11 subset gate (WON-GEN-002)
```

`TypedProgram` deliberately excludes C11 code-emission details. Downstream Phase 2B/2C borrow-checking and TIR lowering (ADR-009) consume `(Types, Symbols, ExprTypes, CallTargets, FieldRefs, VariantRefs)` without re-parsing AST strings.

---

## 2. Canonical Interned Type Representation (`internal/types`)

Every structural type in a compilation unit is normalized and interned into a dense 32-bit `TypeID` (`type.go`). Structural equality comparison `types.Equal(a, b)` is strict O(1) integer equality (`a == b`), eliminating fragile type-string matching.

| Category | `types.Kind` | Semantics Implemented |
| :--- | :--- | :--- |
| Error Recovery | `KindInvalid` (`0`) | Prevents cascading errors downstream of an already diagnosed expression/type failure. |
| Bottom Type | `KindNever` (`!`) | Assigned to diverging control flow (`return`, `break`, `continue`, `panic`); unifies with any expected type. |
| Unit | `KindUnit` | `unit` / `void` / `()`, default function return type when omitted. |
| Scalar Primitives | `KindBool`, `KindChar`, `i8..i128`, `isize`, `u8..u128`, `usize`, `f32`, `f64` | Distinct canonical IDs; zero implicit width widening/narrowing or signed/unsigned coercion (`CanAssign`). |
| 3-Tier Strings | `KindStr`, `KindRef(false, KindStr)` (`&str`), `KindString` (`String`) | `str` is dynamically sized (`IsSized == false`) and rejected by value (`WON-TYP-006`). |
| Compound Values | `KindArray`, `KindSlice`, `KindTuple`, `KindRef`, `KindRawPtr`, `KindFn` | Preserves mutability (`&T` vs `&mut T`, `*const T` vs `*mut T`); `[T]` is DST (`WON-TYP-006`). |
| Algebraic & Generic Types | `KindStruct`, `KindEnum`, `KindTypeParam` | Parameterized via `SymbolID` + concrete `GenericArgs`; supports structural instantiation via `Substitute`. |

### Type Relations (`relations.go`)
- **`IsSized`**: Returns `false` for bare `str`, `[T]`, and tuples containing unsized elements.
- **`IsCopy`**: True for scalars, `bool`, `char`, raw pointers, immutable references `&T`, `fn`, `!`, and arrays/tuples of `Copy` elements; `false` for `&mut T` and `String`.
- **`FitsIntegerLiteral`**: Validates unsuffixed literals (`i8`, `u8`, ..., `u64`/`i64` bounds including unary `-128` for `i8`) only within explicit or bidirectional contextual expectations (`WON-TYP-003`).
- **`CanAssign`**: Enforces identical `TypeID`, `Never` bottom coercion, safe `&mut T -> &T` / `*mut T -> *const T` covariant downgrade, and element-wise tuple compatibility. Implicit integer width coercion returns `false`.
- **`CanCast`**: Permits explicit `as` casts across numeric primitives, `char <-> integer`, `bool -> integer`, raw pointers <-> `usize`, and `&T -> *const T` / `&mut T -> *mut T`; rejects incompatible casts (`WON-TYP-005`).

---

## 3. Hierarchical Symbol Table & Prelude (`internal/symbols`)

- **`Symbol` (`symbol.go`)**: Tracks `SymbolID`, `SymbolKind` (`SymFunc`, `SymMethod`, `SymStruct`, `SymEnum`, `SymEnumVariant`, `SymVar`, `SymParam`, `SymGenericParam`, `SymBuiltin`, `SymAlias`), `TypeID`, `IsMut`, `IsPub`, `LetTokenSpan` (for AI auto-fix `let` -> `var`), `Fields`/`FieldMap`, `Variants`/`VariantMap`, `Methods`, and `GenericParamIDs`.
- **`Scope` (`scope.go`)**: Parent-linked lexical scopes (`ScopeModule`, `ScopeFunction`, `ScopeBlock`, `ScopeMatchArm`, `ScopeImpl`):
  - **Same-scope deduplication**: `Insert` rejects duplicate bindings in the same lexical scope (`WON-NAM-002`).
  - **Nested block shadowing**: Child blocks (`if`, `while`, `for`, inner blocks, `match` arms) may rebind an outer variable name cleanly without mutating the outer binding.
- **Prelude (`table.go`)**: Pre-registers `Option[T]` (`Some`, `None`), `Result[T, E]` (`Ok`, `Err`), `String` (`String::from`), `println`, `print`, `bytes_equal`, `panic`, `assert`.

---

## 4. Safe Subsetting Gate (`WON-GEN-002`)

Because `wonc`'s ISO C11 code generator currently lowers only the Phase 0B/1B C-interop scalar/array/pointer subset (`examples/zlib_compression.won`), `TypedProgram` records `CodegenSupported = false` whenever a validated program uses Phase 2A-only high-level constructs (user structs/enums, `match`, `?`, tuple/range expressions, or non-Phase-0 statements).

`wonc check` succeeds cleanly on valid Phase 2A programs; `wonc build` inspects `TypedProgram.CodegenSupported` and fails explicitly with diagnostic `WON-GEN-002` at the construct's exact `Span`, preventing silent wrong C11 generation.

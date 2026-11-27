# Phase 2C.0 — Backend-Neutral TIR Foundation & Verified CFG Lowering

> **Document Status**: `IMPLEMENTED` (TIR Core Data Model & Type Table, Verified Frontend/Drop CFG Lowering, Structural IR Verifier & CLI Validation Gate; TIR-Driven Codegen Emission Remains `PLANNED` for Phase 2C.1)  
> **Milestone**: Phase 2C.0 (Backend-Neutral Typed Intermediate Representation Foundation)  
> **Governing Specification**: [ADR-009](../decisions/ADR-009-typed-intermediate-representation-architecture.md) (`ACCEPTED`), [ADR-011](../decisions/ADR-011-ownership-and-borrow-checking-semantics.md) (`ACCEPTED`), [compiler-and-ir-requirements.md](../language-spec/compiler-and-ir-requirements.md) (`SPECIFIED`), [phase-2b.3-drop-elaboration.md](phase-2b.3-drop-elaboration.md) (`IMPLEMENTED`)  
> **Deferred Beyond Phase 2C.0**: Consuming TIR in `internal/codegen` (`2C.1`), native C11 RAII destructor execution / ADT layout lowering, general `trait` solver, generic monomorphization instantiation, LLVM/QBE emitters, and structured concurrency runtime.

---

## 1. Architectural Goals

Phase 2C.0 introduces WON's first real backend-neutral **Typed Intermediate Representation (`internal/tir`)** and its deterministic, fail-closed **lowering pass (`internal/tir/lower`)** in compliance with [ADR-009](../decisions/ADR-009-typed-intermediate-representation-architecture.md).

Core engineering invariants:
- **Zero Frontend Coupling in `tir`**: `compiler/poc/internal/tir` imports only Go standard-library packages. Structurally and by Go's package graph, `tir.Program` cannot retain `*ast.*`, `*symbols.Symbol`, `*types.Context`, `*cfg.FuncCFG`, `*checker.TypedProgram`, or C-specific syntax strings.
- **No Optimization / No Silent Drops**: Every operation accepted by the semantic checker and validated by Phase 2B ownership/borrow/FFI/Drop analysis lowers explicitly into typed TIR instructions and typed terminators.
- **Preserved C11 Baseline**: The reference AST-to-C11 emitter (`internal/codegen`) and pre-codegen `WON-GEN-002` capability gate remain active and unchanged in Phase 2C.0. Native codegen does not yet consume TIR.

---

## 2. TIR Data Model (`compiler/poc/internal/tir`)

A compiled `tir.Program` contains:
- `Types *tir.TypeTable`: canonical structurally interned types.
- `Functions []*tir.Function`: deterministic ordered function CFGs.
- `DropGlues []tir.DropGlueDesc`: explicit cleanup descriptors indexed by `DropGlueID`.
- `FFICatalogs []tir.FFICatalogDesc` & `FFIContracts []tir.FFIContractDesc`: validated Phase 2B.3.2 envelope and per-function FFI contract records.
- `SourceFiles []string`: sorted source paths.
- `OwnershipOK`, `DropOK bool`: propagated certification gates.

---

## 3. Canonical Type Representation (`tir/types.go`)

`tir.TypeTable` assigns sequential deterministic `TypeID`s (`InvalidType = 0`) using structural canonical keys with zero C spellings:
- **Primitives**: `TypeNever` (`!`), `TypeUnit` (`unit`), `TypeBool`, `TypeChar`, `TypeInt` (explicit `Signed`, `Bits` `8/16/32/64/128`, or `IsPtrSized` for `isize`/`usize`), `TypeFloat` (`32`/`64`), `TypeStr` (`str`), `TypeString` (`String`).
- **Indirections & Collections**: `TypeRawPtr` (`IsMut`, `Elem`), `TypeRef` (`IsMut`, `Elem`), `TypeArray` (`Elem`, `ArrayLen`), `TypeSlice` (`Elem`), `TypeTuple` (`Elems`), `TypeFn` (`Params`, `Return`).
- **Algebraic & Foreign Types**:
  - `TypeStruct`: preserves ordered `Fields []StructFieldType`, `IsCopy`, optional `GenericArgs`, and foreign opaque resource markers (`IsOpaqueForeign`, `ForeignNamespace`). Forward reservation (`ReserveStruct` / `DefineStruct`) handles recursive types cleanly.
  - `TypeEnum`: preserves ordered `Variants []EnumVariantType` (`VariantUnit`, `VariantTuple`, `VariantStruct`), `IsCopy`, and `GenericArgs` (covering `Option[T]`, `Result[T, E]`, and user enums).
  - `TypeTypeParam`: preserves uninstantiated type-parameter placeholders only where the frontend emits signature metadata; Phase 2C.0 does not claim monomorphization.

---

## 4. Value & Local Identity Rules

All IDs are allocated deterministically in program traversal order without Go map iteration or pointer addresses:
- `FunctionID` (`1..N`): order of functions in `DropElaboratedProgram.ElaboratedCFG.Functions`.
- `LocalID` (`1..M` per function): order of `cfg.FuncCFG.LocalOrder`.
- `ValueID` (`1..V` per function): declared in `Function.Values []ValueDecl` with exact `TypeID` and `IsJoinTemp` flag. Normal SSA-style values allow at most one static defining instruction; control-flow merge registers (`IsJoinTemp = true` for expression `if`, `match`, `unsafe`, and short-circuit `and`/`or`) may be written along each incoming branch via `OpValueAssign`.
- `DropFlagID` (`1..K` per function): order of `DropElaboratedFunc.FlagOrder`.
- `DropGlueID`, `ContractID`: sorted canonically by glue ID / namespace & contract key.

---

## 5. Place & Projection Model (`tir/place.go`)

`tir.Place` records `{ Local LocalID, LocalName, RootType, ElemType, Projections []Projection }`:
- `ProjField` (`.field#idx`)
- `ProjTupleField` (`.idx`)
- `ProjConstIndex` (`[k]`)
- `ProjDynIndex` (`[%idx_val]` / `[_l]`)
- `ProjDeref` (`.*`)
- `ProjDowncast` (`@variant#idx`)

Downstream backends inspect structural `Projection` slices directly rather than parsing string keys.

---

## 6. Instruction Taxonomy (`tir/ir.go`)

| Category | Instructions | Purpose |
| :--- | :--- | :--- |
| **Storage & Scope** | `OpStorageLive`, `OpStorageDead`, `OpScopeEnter`, `OpScopeExit`, `OpInit`, `OpLoad`, `OpStore`, `OpComputeAddr` | Stack storage lifetime, lexical scopes, direct loads/stores, address projection |
| **Value Compute** | `OpConst`, `OpUnary`, `OpBinary`, `OpCast`, `OpValueAssign`, `OpAggregateConstruct`, `OpExtract` | Typed constants (`big.Int` lossless text), operators, explicit casts, branch joins, struct/tuple/array/enum construction & extraction |
| **Ownership & Borrow** | `OpCopy`, `OpMove`, `OpAssign` (`IsReinit`), `OpBorrow` (`BorrowShared/Mut/Reborrow*` + `LoanID` + `ParentPlace`), `OpStaticRef`, `OpOwnershipTransfer` | Move vs Copy distinction, reinitialization, borrow/reborrow ancestry, static literal origin, FFI `@owned` transfer |
| **Calls** | `OpCall`, `OpBuiltinCall`, `OpForeignCall` | Internal WON calls (`cc=WON`), builtins (`println`, `panic`, `String::from`, `.len()`, `__try_unwrap`), and contracted/unsafe foreign calls (`cc=C`, `ContractID`, `ArgOwnership`, `ReturnOwnership`) |
| **Resource Lifecycle** | `OpDrop`, `OpCondDrop`, `OpDropFlagDecl`, `OpDropFlagSet`, `OpDropFlagCheck` | Unconditional/conditional cleanup linked directly to `DropGlueID` & `DropFlagID` |

---

## 7. Terminator Semantics

Every basic block ends with exactly one `tir.Terminator`:
- `TermGoto(Target)`
- `TermBranchIf(Cond: Operand<bool>, ThenBlock, ElseBlock)` (used by `if`, `while`, `match` guards, short-circuit `and`/`or`)
- `TermSwitch(Cond, SwitchKind, Cases, DefaultBlk)` (used by `match`, `for` iterators, `?` error propagation, and multi-way branches)
- `TermReturn(ReturnVal *Operand)`
- `TermUnreachable(IsTrap, TrapReason)`

Short-circuit `and`/`or` edges materialize an explicit short-circuit trampoline block writing the `bool` constant to the `IsJoinTemp` register before jumping to the merge block.

---

## 8. Source Mapping

Source regions are preserved as value-type `tir.SourceSpan` records (`File`, line/col/byte start & end) on `Function`, `LocalDecl`, `ValueDecl`, `DropFlag`, `Instruction`, and `Terminator`, decoupling diagnostics/debug metadata from AST nodes.

---

## 9. Frontend / CFG / Drop → TIR Mapping Table

During auditing of the existing Phase 2B compiler, we found that `cfg.FuncCFG` only allocated `ValueID`s for reference-carrying expressions and omitted scalar operators, literal constants, casts, branch condition operands, match case labels, compound assignment operators, and non-ref expression yields. To close this gap **without breaking Phase 2B CFG snapshots or ownership semantics**, `cfg/builder.go` populates non-dumped `StatementBridge`, `TerminatorBridge`, and `ComputeOp` records (`cfg/bridge.go`) preserved across `drop.CloneFuncCFG` (`drop/ir.go`) and FFI `@borrow` rewrites (`drop/ffi.go`).

| Frontend / CFG / Drop Source | Bridge / Metadata Preserved | Target TIR Representation |
| :--- | :--- | :--- |
| Scalar/float/bool/char/byte literal | `CompConst` | `OpConst` |
| `&str` literal (`OpStaticRef`) | `CompConst(string)` merged into `OpStaticRef` | `OpStaticRef` |
| Unary (`-`, `not`, `!`, `~`, `*`) | `CompUnary` | `OpUnary` / `OpLoad` / `OpExtract` |
| Binary (`+..-/%/&/\|/^/<</>>/==..>=`) | `CompBinary` | `OpBinary` |
| Logical `and` / `or` | `BridgeSwitchLogicAnd/Or`, short-circuit `YieldDest` | `TermBranchIf` + trampoline `OpValueAssign` -> merge `ValueID` |
| `expr as Type` | `CompCast` | `OpCast` (`CastNumeric`/`PtrToPtr`/`PtrToInt`/`IntToPtr`/`RefToRawPtr`) |
| `Struct` / `Tuple` / `Array` / `ArrayRepeat` / `Range` / `InterpString` | `CompAggregate` / `OpAggregate` | `OpAggregateConstruct` |
| `Enum::Unit` / `Some` / `Ok` / `Err` / `Enum::Tuple` / `Enum::Struct` | `CompEnumUnit` / `CallEnumVariant` / `VariantInfo` | `OpAggregateConstruct` (`AggEnumVariant`) |
| `a += b` compound assignment | `AssignOp` on `StatementBridge` | `OpLoad` + `OpBinary` + `OpAssign` |
| `if` / `else` / `while` | `BridgeSwitchIf/While`, `CondVal`, `YieldDest`/`YieldVal` | `TermBranchIf` + `OpValueAssign` on yield exits |
| `match` arms, guards & destructuring | `BridgeSwitchMatch/Guard`, `BridgeMatchCase`, `BridgePatternBind` | `TermSwitch` + `TermBranchIf` + `OpExtract` + `OpInit` |
| `expr?` (`TryExpr`) | `BridgeSwitchTry`, `CompTryUnwrap`, `TryErrSourceVal` | `TermSwitch` + `__try_unwrap` / `__try_propagate_err` + `TermReturn` |
| `OpBorrowShared/Mut/Reborrow` | `LoanID`, `BorrowKind`, `ParentPlace`, `ResultVal`/`ValTemp` | `OpBorrow` |
| `OpDrop` / `OpCondDrop` / `DropFlagEvent` | `DropElaboratedFunc.{Flags, FlagEvents}`, `DropGlue` | `OpDropFlagDecl`, `OpDropFlagSet`, `OpDrop`, `OpCondDrop` |
| `OpCall` (FFI) / `OpFFITransfer` | `FFIContracts`, `BindingMeta.HeaderPath`, `FFIContractCatalog.SchemaVersion` | `OpOwnershipTransfer`, `OpForeignCall` (`ContractID`, C ABI, header/schema metadata) |

---

## 10. Structural Verification Invariants (`tir/verify.go`)

`tir.Verify(prog *tir.Program) []VerifyError` enforces fail-closed invariants across both reachable and unreachable blocks:
- **Pipeline Gates**: requires `OwnershipOK == true`, `DropOK == true`, and initialized `TypeTable`.
- **Identity Integrity**: unique non-zero `FunctionID`, `LocalID`, `ValueID`, `DropFlagID`, `DropGlueID`, `ContractID`; unique non-negative `BlockID`; valid `EntryBlock`; non-dangling `Goto`/`BranchIf`/`Switch` targets; single valid `Terminator` on every block; no duplicate static writes to non-`JoinTemp` `ValueID`s.
- **Type Integrity**: valid `TypeID`s everywhere; `Bool` condition on `TermBranchIf`; compatible `TermReturn` / `OpCall` argument and return types; exact struct field names/types and tuple element arities in `OpAggregateConstruct`; valid dereference/projection chains on `Place`.
- **Ownership & Lifecycle Integrity**: valid `ParentPlace` on reborrows; `OpDrop` / `OpCondDrop` matching declared `DropGlueDesc.Type`; declared `DropFlagID` on `OpCondDrop` / `OpDropFlag*`; valid `ContractID` and arity alignment on `OpOwnershipTransfer` and contracted `OpForeignCall`.

---

## 11. Backend Independence Guarantees

- No C type names, header includes, `#define` macros, or GNU/Clang strings appear inside `internal/tir`.
- `internal/tir/dump.go` produces deterministic output exclusively from indexed slices (`Functions`, `DropGlues`, `FFIContracts`, `FFICatalogs`, `Types`) with zero unordered Go map iteration.

---

## 12. Explicit Scope & Unsupported Cases

- **Synthetic CFG Fallback**: Synthetic unit-test CFGs lacking an AST bridge lower all ownership/borrow/FFI/drop events directly, using conservative typed discriminants on synthetic switches.
- **Deferred Constructs**: Traits, generic monomorphization instantiation, user `impl Drop`, and concurrency `scope`/`spawn` remain upstream `WON-SEM-010` rejections and are not lowered into TIR.

---

## 13. Boundary Between Phase 2C.0 and Phase 2C.1

| Capability | Status After Phase 2C.0 |
| :--- | :--- |
| Backend-neutral TIR core (`internal/tir`) | `IMPLEMENTED` |
| Verified CFG + Drop + FFI lowering (`internal/tir/lower`) | `IMPLEMENTED` |
| Independent structural TIR verifier (`tir.Verify`) & `wonc check --dump-tir` | `IMPLEMENTED` |
| C11 backend consuming TIR (`C11Emitter`) & native Drop glue emission | `PLANNED` (Phase 2C.1; current C11 backend still lowers AST under `WON-GEN-002`) |

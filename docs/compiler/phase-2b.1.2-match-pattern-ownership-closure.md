# Phase 2B.1.2 — Match Pattern Ownership Closure & Guard Control-Flow Correctness

> **Document Status**: `IMPLEMENTED` (Verified in official GitHub Actions CI run `45808397443` at commit `91a6932f28f6d9c231958e4959ea29f5577c5f28`)  
> **Milestone**: Phase 2B.1.2 (Follow-up closure on Phase 2B.1.1 `92fabc0b602246e9d64d77e78aa8f6a3c86cc4d8`)  
> **Scope Boundary**: Preserves accepted ADR-011 semantics without inventing Phase 2B.2 borrow-conflict/NLL/provenance checks or Phase 2B.3 drop elaboration.

---

## 1. Closed Soundness Gaps

1. **Mixed Aggregate Pattern Event Preservation (`compiler/poc/internal/cfg/builder.go`)**:
   - Eliminated the `if needsMove ... else if len(borrowOps) > 0` branch inside `lowerOpaqueAggregatePattern` that could discard `OpBorrowShared` / `OpBorrowMut` events whenever a pattern mixed `ref` and by-value non-`Copy` bindings.
   - `IdentPattern` and direct `StructPattern` shorthand `Copy` bindings now emit explicit `OpCopy` source reads before `OpInitAssign`, ensuring zero false `OpMove`s while preserving the invariant that every bound local is initialized from an explicit source operation when matching a `Place`.
   - `Direct StructPattern` uses resolved field-level `ProjField` paths (`findStructFieldType` with generic substitution) so `ref`, `ref mut`, `Copy`, and non-`Copy` moved fields coexist independently on sibling fields.
   - Non-direct opaque aggregates (`EnumPattern`, `TuplePattern`, `SlicePattern`, and enum-variant `StructPattern`) preserve all `OpRead` + `OpBorrow*` + `OpCopy` events for `ref`/`ref mut`/`Copy` combinations, emit whole-subject `OpMove` for move-only aggregates, and conservatively reject mixed `(ref/ref mut + non-Copy move)` combinations with **`WON-SEM-010`** (`OwnershipChecked = false`) rather than silently dropping borrow events or misclassifying as `WON-MEM-001`.

2. **Guarded Match-Arm Control-Flow Order & Temporary Language Restriction**:
   - Investigated whether arbitrary non-`Copy` guarded bindings could be modeled via temporary guard borrows under accepted Phase 2B.1 semantics. Because `checker/patterns.go` currently types pattern locals directly as owned types (`T`, allowing arbitrary by-value moves/mutations inside guards) without a formal guard-borrow transformation, committing `OpMove` before evaluating `arm.Guard` is unsound (false move along `guardFailBB -> armTests[i+1]`), while deferring `OpMove` after a guard that can consume the binding creates dual-ownership aliasing.
   - **Conservative Rule**: Guarded consuming patterns (any `arm.Guard != nil` whose `patternConsumesNonCopy(arm.Pattern)` is true) are explicitly rejected with **`WON-SEM-010`** prior to emitting pattern ownership operations, preventing any premature `OpMove` on failed-guard paths.
   - **Temporary Language Restriction**: Guarded `match` arms currently allow `ref`, `ref mut`, wildcards, literals, unit enum variants, and by-value `Copy` bindings (cleaning up arm locals via `OpScopeExit` inside `guardFailBB` before jumping to the next candidate arm), while guarded non-`Copy` by-value moving patterns remain restricted (`WON-SEM-010`) pending a formally specified guard-ownership model.

---

## 2. Supported vs. Rejected Match Pattern Matrix

| Pattern Category | Example | Unguarded (`arm.Guard == nil`) | Guarded (`arm.Guard != nil`) | CFG / Diagnostic Contract |
| :--- | :--- | :--- | :--- | :--- |
| Wildcard / Literal / Unit Variant | `_`, `0`, `Opt::None` | **Supported** | **Supported** | `OpRead` discriminant check only; no moves or borrows. |
| Single `ref` / `ref mut` Binding | `ref r`, `ref mut m` | **Supported** | **Supported** | `OpRead` + `OpBorrowShared`/`OpBorrowMut` + `OpInitAssign`; `guardFailBB` emits `OpScopeExit`. |
| Single By-Value `Copy` Binding | `n: i32`, `flag: bool` | **Supported** | **Supported** | `OpCopy` + `OpInitAssign`; zero false `OpMove`. |
| Single By-Value Non-`Copy` Binding | `s: String` | **Supported** | **Rejected (`WON-SEM-010`)** | Unguarded emits `OpMove` (or `WON-MEM-008` through `&T`); guarded rejected pre-move. |
| Direct `StructPattern` (Pure Ref or Copy) | `S { x_i32, b: ref rb }` | **Supported** | **Supported** | Per-field `ProjField`: `OpCopy` / `OpRead`+`OpBorrow*` + `OpInitAssign`. |
| Direct `StructPattern` (With Non-`Copy` Move, incl. mixed Ref + Move) | `Pair { a, b: ref rb }` | **Supported** | **Rejected (`WON-SEM-010`)** | Unguarded uses per-field `ProjField` partial move (`Pair.a`) + `OpBorrow*` (`Pair.b`). |
| Opaque Aggregate (Ref-Only, Copy-Only, or Ref + Copy) | `Opt::Some(ref r)`, `(ref r, count_i32)`, `(a_i32, b_bool)` | **Supported** | **Supported** | Preserves all `OpRead` + `OpBorrow*` + `OpCopy` events; zero subject moves. |
| Opaque Aggregate (Non-`Copy` Move-Only, incl. Copy + Move) | `Dual::Pair(a, b)`, `(s_str, count_i32)` | **Supported** | **Rejected (`WON-SEM-010`)** | Unguarded emits whole-subject `OpMove` (or `WON-MEM-008` through `&T`). |
| Opaque Aggregate (Mixed `ref`/`ref mut` + Non-`Copy` Move) | `Dual::Pair(ref a, b)`, `(ref mut a, b)`, `Boxed::Item { ref l, right }` | **Rejected (`WON-SEM-010`)** | **Rejected (`WON-SEM-010`)** | Explicitly rejected without dropping borrows; `OwnershipChecked` stays `false`. |

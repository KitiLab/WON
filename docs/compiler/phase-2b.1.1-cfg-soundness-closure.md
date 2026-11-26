# Phase 2B.1.1 — Ownership CFG Soundness Closure & Fail-Closed Analysis

> **Document Status**: `IMPLEMENTED` (Verified in official GitHub Actions CI run `45806552788` at commit `92fabc0b602246e9d64d77e78aa8f6a3c86cc4d8`)  
> **Milestone**: Phase 2B.1.1 (Hardening closure over Phase 2B.1 `90b2061`)  
> **Baseline Design**: [phase-2b.0-ownership-architecture.md](phase-2b.0-ownership-architecture.md), [ADR-011](../decisions/ADR-011-ownership-and-borrow-checking-semantics.md) (`ACCEPTED`)  
> **Scope Boundary**: Preserves accepted ADR-011 semantics without modification; does **not** implement Phase 2B.2 (borrow conflicts / NLL regions / provenance) or Phase 2B.3 (drop elaboration / drop flags).

---

## 1. Hardening Summary (Fixes 1–5)

Phase 2B.1.1 closes five soundness and completeness gaps identified during public-source review of Phase 2B.1 (`compiler/poc/internal/cfg` and `compiler/poc/internal/borrow`):

1. **Unbounded Lazy Struct Field Tracking (`internal/borrow/state.go`)**:
   - Eliminated the fixed `depth > 8` truncation limit in `initStructFieldsIfAny`.
   - Replaced eager pre-expansion with single-level lazy `expandDirectFields(symTab, tctx)` + `uniformChild`, resolving nested struct projections (`resolveFieldState`) iteratively to arbitrary depths (`1, 8, 9, 16, 24+`).
   - Lattice `joinPlaceState` and `equalPlaceState` preserve uniform parent snapshots cleanly and synthesize lazy `uniformChild` nodes when joining or comparing expanded vs. unexpanded states.
   - Recursive pointer/reference/enum-indirected structs stop at reference/enum boundaries and only expand paths actually projected by source expressions, avoiding infinite recursion or stack overflow.
2. **Fail-Closed Internal State & Pipeline Gate (`WON-INT-001`)**:
   - Replaced silent returns and silent `newPlaceState(..., true)` fallbacks in `checkReadablePlace`, `applyMove`, `applyAssign`, `OpUninitDecl`, `OpInitAssign`, `OpBorrow*`, `OpReturn`, and `default:` statement handlers with **`WON-INT-001`** (`Ownership CFG invariant violation`).
   - Guarded missing `SymbolID`s, unregistered locals, invalid assign targets, and unimplemented `ProjectionKind` values during CFG construction (`internal/cfg/builder.go`).
   - Fixed `checkStructPatternFields` (`internal/checker/patterns.go`) and `Ast.FieldPattern.GetSpan()` (`internal/ast/ast.go`) so shorthand `Struct { a, b }` field bindings always record `NodeSymbols` entries with `DeclHasInit = true`.
   - Set `TypedProgram.OwnershipChecked = !c.report.HasErrors()` (`internal/checker/checker.go`) and enforced fail-closed gating in both `cmd/wonc` and `codegen.NewTyped(...).Generate()` so C11 emission is blocked whenever `OwnershipChecked == false`.
3. **Per-Arm `MatchExpr` Ownership Lowering (`internal/cfg/builder.go`)**:
   - Lowered `MatchExpr` discriminant reads (`OpRead`) without unconditionally moving the subject prior to arm selection.
   - Reference-only arms (`ref x` / `ref mut x`) emit `OpRead` + `OpBorrowShared`/`OpBorrowMut` (reserved for 2B.2) without invalidating the subject binding.
   - By-value non-`Copy` arms emit `OpMove` strictly inside the taking arm block (and reject non-`Copy` extraction through `&T` / `&mut T` / `*ptr` via `WON-MEM-008`).
   - Direct `StructPattern` arms emit per-field `ProjField` operations (`OpMove` on moved fields, `OpBorrow*` on `ref` fields), enabling partial moves across sibling struct fields.
   - Guarded arms emit `OpScopeExit` along `guardFailBB -> armTests[i+1]` before subsequent arm pattern evaluation, preventing failed guards from leaking bindings or false-committing moves to later arms.
4. **Exhaustive AST-to-CFG Coverage Guard (`internal/cfg/coverage_test.go`)**:
   - Added explicit `default -> WON-INT-001` guards in declaration, statement, expression, assignment-target, and pattern lowering switches.
   - Enforced structural CI verification (`TestASTToCFGExhaustiveInventory`) via Go AST inspection guaranteeing every `ast.Decl`, `ast.Stmt`, `ast.Expr`, and `ast.Pattern` defined in `compiler/poc/internal/ast/ast.go` is explicitly accounted for in `internal/cfg/builder.go`.
5. **Fixed-Point Non-Convergence Diagnostic (`WON-INT-001`)**:
   - Upgraded fixed-point iteration overflow from `WON-MEM-001` to **`WON-INT-001`**, computed strictly over reachable CFG blocks (`computeReachable`) using finite three-bit lattice monotonicity.

---

## 2. AST-to-CFG Ownership Lowering Coverage Matrix

Every node declared in `compiler/poc/internal/ast/ast.go` is classified below by its Phase 2B.1.1 handling inside `internal/cfg/builder.go`:

### 2.1 Top-Level Declarations (`ast.Decl`)

| AST Node | Classification | Ownership CFG Lowering Behavior |
| :--- | :--- | :--- |
| `*ast.FunctionDecl` | **Fully lowered** | Builds per-function `FuncCFG` with parameter `OpInitAssign` and body statements. |
| `*ast.ImplBlock` (inherent) | **Fully lowered** | Builds per-method `FuncCFG` (including `self`/`&self`/`&mut self`). |
| `*ast.ImportDecl`, `*ast.ModuleImportDecl`, `*ast.StructDecl`, `*ast.EnumDecl`, `*ast.TypeAliasDecl` | **Conservatively lowered** | Non-executable item definitions; no runtime function body CFG required. |
| `*ast.TraitDecl`, `*ast.ImplBlock` (`TraitType != nil`), `*ast.ModuleDecl` | **Explicitly unsupported** | Rejected earlier in semantic analysis (`WON-SEM-010`) before C codegen; `OwnershipChecked` remains `false`. |
| Any unknown future `ast.Decl` | **Fail-closed (`WON-INT-001`)** | Caught by `default:` in `BuildProgram`, blocking certification. |

### 2.2 Statements (`ast.Stmt`)

| AST Node | Classification | Ownership CFG Lowering Behavior |
| :--- | :--- | :--- |
| `*ast.BlockStmt` | **Fully lowered** | Pushes/pops lexical `ScopeFrame` (`OpScopeEnter`/`OpScopeExit`) and lowers inner statements. |
| `*ast.LetStmt` | **Fully lowered** | Distinguishes `Init == nil` (`OpUninitDecl`) vs initialized (`OpInitAssign`) and aggregate pattern bindings. |
| `*ast.AssignStmt` | **Fully lowered** | Evaluates RHS (Wait-free order), compound RHS reads, LHS sub-expressions, then `OpAssign` (full/field reinit) or `WON-INT-001` if non-place. |
| `*ast.IfStmt` | **Fully lowered** | Explicit `if.then` / `if.else` / `if.merge` branching; unreachable merge if all branches diverge. |
| `*ast.WhileStmt` | **Fully lowered** | Explicit `while.cond` / `while.body` / `while.exit` loop back-edges; fixed-point detects loop moves (`WON-MEM-001`). |
| `*ast.ForStmt` | **Fully lowered** | Evaluates iterable, binds pattern local per `for.body` iteration with loop back-edge to `for.header`. |
| `*ast.ReturnStmt` | **Fully lowered** | Evaluates return value (`OpMove`/`OpCopy`), emits `OpReturn`, closes live scopes, transitions to `exit`. |
| `*ast.BreakStmt`, `*ast.ContinueStmt` | **Fully lowered** | Emits exited intermediate `OpScopeExit` frames and jumps to enclosing loop `breakBB` / `continueBB`. |
| `*ast.ExprStmt` | **Fully lowered** | Lowers embedded expression via `lowerExprValue` (including `IfStmt`, `WhileStmt`, `ForStmt`, `MatchExpr` expressions). |
| `*ast.ScopeExpr`, `*ast.SpawnExpr` | **Explicitly unsupported** | Concurrency syntax emits `WON-SEM-010` in checker & CFG lowering (`OwnershipChecked = false`). |
| Any unknown future `ast.Stmt` | **Fail-closed (`WON-INT-001`)** | Caught by `default:` in `lowerStmt`. |

### 2.3 Expressions (`ast.Expr`)

| AST Node | Classification | Ownership CFG Lowering Behavior |
| :--- | :--- | :--- |
| `*ast.Identifier` | **Fully lowered** | Resolves `Place` (`OpCopy` for `Copy`, `OpMove` for non-`Copy`). |
| `*ast.FieldAccessExpr` | **Fully lowered** | Resolves `ProjField` / `ProjDeref`; moves/copies field or rejects deref non-`Copy` moves (`WON-MEM-008`). |
| `*ast.IndexExpr` | **Fully lowered** | Resolves base + index expression; rejects non-`Copy` move from indexed place (`WON-MEM-008`). |
| `*ast.UnaryExpr`, `*ast.BorrowExpr` | **Fully lowered** | Deref `*p` (`OpRead`/`WON-MEM-008`), `&`/`&mut` emits `OpRead` + `OpBorrowShared`/`OpBorrowMut` (borrow conflicts deferred to `2B.2`). |
| `*ast.BinaryExpr`, `*ast.CastExpr`, `*ast.RangeExpr`, `*ast.TryExpr`, `*ast.ArrayLiteral`, `*ast.ArrayRepeatLiteral`, `*ast.TupleLiteral`, `*ast.StructLiteral`, `*ast.InterpolatedStringExpr`, `*ast.UnsafeExpr`, `*ast.CallExpr`, `*ast.NamespacedCallExpr`, `*ast.MethodCallExpr` | **Fully lowered** | Lowers operands, receivers, arguments left-to-right per ownership value rules (`Move` for non-`Copy` args, `Borrow*` for `&`/`&mut self`). |
| Literal nodes (`*ast.IntegerLiteral`, `*ast.FloatLiteral`, `*ast.BoolLiteral`, `*ast.CharLiteral`, `*ast.ByteLiteral`, `*ast.StringLiteral`), `*ast.PathExpr` | **Fully lowered** | Pure value/constant path expressions; no linear resource move invalidation required. |
| `*ast.IfStmt`, `*ast.WhileStmt`, `*ast.ForStmt`, `*ast.MatchExpr` | **Fully lowered** | Control-flow expression lowering (`MatchExpr` lowered per-arm with independent ref/move/guard edges). |
| `*ast.ScopeExpr`, `*ast.SpawnExpr` | **Explicitly unsupported** | Emits `WON-SEM-010` (structured concurrency deferred to Phase 3). |
| Any unknown future `ast.Expr` | **Fail-closed (`WON-INT-001`)** | Caught by `default:` in `lowerExprValue` and `lowerPlaceAddressSubExprs`. |

### 2.4 Patterns (`ast.Pattern`)

| AST Node | Classification | Ownership CFG Lowering Behavior |
| :--- | :--- | :--- |
| `*ast.WildcardPattern`, `*ast.LiteralPattern` | **Fully lowered** | discriminant-only match; binds no owned variables and moves nothing out of subject. |
| `*ast.IdentPattern` | **Fully lowered** | Distinguishes unit enum variants (`VariantRef`), `ref`/`ref mut` (`OpRead`+`OpBorrow*`), `Copy` copy, and non-`Copy` `OpMove` (or `WON-MEM-008` through `&T`). |
| `*ast.StructPattern` (direct struct) | **Fully lowered** | Field-level `ProjField` partial move vs per-field `ref` borrow (`Struct { a, b: ref rb }` leaves `b` live). |
| `*ast.EnumPattern`, `*ast.StructPattern` (enum variant), `*ast.TuplePattern`, `*ast.SlicePattern` | **Conservatively lowered / Mixed-Mode Guarded (`2B.1.2`)** | Preserves all `OpBorrow*` + `OpCopy` events for ref/Copy/ref+Copy patterns; emits whole-subject `OpMove` for move-only; explicitly rejects mixed `ref` + non-`Copy` move aggregates and guarded consuming patterns with **`WON-SEM-010`** (see [phase-2b.1.2-match-pattern-ownership-closure.md](phase-2b.1.2-match-pattern-ownership-closure.md)); exact `ProjDowncast`/`ProjTupleField` sub-projections reserved for Phase 2B.2+. |
| Any unknown future `ast.Pattern` | **Fail-closed (`WON-INT-001`)** | Caught by `default:` in `applyArmPatternOwnership` and `lowerOpaqueAggregatePattern`. |

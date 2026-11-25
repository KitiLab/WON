# WON Phase 2B.0 — Ownership-Lite Architecture, Borrow Semantics & Control-Flow Analysis Design

> **Document Status**: `SPECIFIED` (Phase 2B.0 Architecture Design; Normative Language Rules Awaiting Owner Approval)  
> **Milestone**: Phase 2B.0 — Ownership Architecture & Formal Analysis Design  
> **Target Compiler Packages**: `compiler/poc/internal/cfg`, `compiler/poc/internal/borrow` (Planned Phase 2B.1+), bridging `compiler/poc/internal/checker` (`TypedProgram`) to ADR-009 TIR (`Phase 2C`)  
> **Companion Specifications**:
> - [Ownership and Borrowing Normative Proposal](../language-spec/ownership-and-borrowing.md) (`Proposed / Awaiting Owner Approval`)
> - [Ownership and Memory Specification](../language-spec/ownership-and-memory.md) (`SPECIFIED`)
> - [ADR-003: Ownership-Lite Memory Model](../decisions/ADR-003-ownership-lite-memory-model.md) (`ACCEPTED`)
> - [ADR-008: Structured Concurrency and Zero-GIL](../decisions/ADR-008-structured-concurrency-and-zero-gil.md) (`ACCEPTED`)
> - [ADR-009: Typed Intermediate Representation Architecture](../decisions/ADR-009-typed-intermediate-representation-architecture.md) (`ACCEPTED`)
> - [ADR-011: Ownership and Borrow-Checking Semantics](../decisions/ADR-011-ownership-and-borrow-checking-semantics.md) (`Proposed / Awaiting Owner Approval`)

---

## 1. Current Compiler Audit & Status Boundary

Before designing Phase 2B.1+ static memory-safety analysis, the compiler's **actual implemented state** at Phase 2A.2.1 (`compiler/poc/`) is strictly separated from designed future passes:

### 1.1 What Is Currently `IMPLEMENTED`
1. **Frontend & Canonical Type System (`internal/types`, `internal/symbols`, `internal/checker`)**:
   - `checker.Checker.CheckProgram` constructs `TypedProgram` containing `AST`, `Types (*types.Context)`, `Symbols (*symbols.Table)`, `ExprTypes (map[ast.Expr]types.TypeID)`, `CallTargets`, `FieldRefs`, `VariantRefs`, and `CodegenSupported` (`WON-GEN-002`).
   - `types.Context.IsCopy(TypeID)` classifies scalar primitives, `bool`, `char`, raw pointers (`*const T`, `*mut T`), shared references (`&T`), function types (`fn`), `!`, and arrays/tuples whose element types satisfy `IsCopy` as `Copy`. Mutable references (`&mut T`), `String`, `KindStruct`, `KindEnum`, and `KindTypeParam` currently evaluate to `!IsCopy` (`false`).
   - `symbols.Symbol` records `ID`, `Kind`, `Type`, `IsMut`, `LetTokenSpan` (used for AI JSON auto-fix `let` -> `var`), `FieldMap`, `VariantMap`, and `Methods`.
   - Shallow mutability checking (`WON-MUT-001`) rejects assignment, `&mut x`, `as_mut_ptr()`, and `&mut self` calls when the base binding lacks `var` or `&mut` provenance (`isLValueRootMutable`).
   - Safety boundary checks enforce `unsafe:` blocks around `#import` FFI calls (`WON-SEC-001`) and raw-pointer `*p` dereferencing / unsafe pointer arithmetic (`WON-SEC-002`).
   - Concurrency constructs (`scope`, `spawn`) and `trait` / `impl Trait for Type` / `mod` are explicitly rejected as unimplemented via `WON-SEM-010`.
2. **Backend Boundary (`internal/checker/backend_caps.go`, `internal/codegen`)**:
   - Only the scalar/array/C-FFI vertical slice compiles through `verifyBackendCapabilities` to ISO C11 (`WON-GEN-002` rejects structs, enums, references, tuples, `String`, `?`, and closures at `wonc build`).

### 1.2 What Does **Not** Exist Yet (`NOT IMPLEMENTED` / `PLANNED`)
- No Control-Flow Graph (CFG) IR exists in `compiler/poc/internal/`.
- No definite-initialization, move invalidation (`WON-MEM-001`), partial-move, or reinitialization analysis exists.
- No aliasing, exclusive-borrow (`WON-MEM-004`/`005`/`006`), non-lexical lifetime (NLL) region inference, or path-disjoint field borrow splitting exists.
- No return-reference provenance solver (`Prov(e)`, `WON-MEM-002`/`003`) exists.
- No RAII drop elaboration, drop flags, or deterministic `Drop` insertion exists.
- No `@derive(Copy)` opt-in trait resolution or `Send`/`Sync` auto-trait checker exists.

### 1.3 Status Classification Map

| Layer | Current Status | Notes |
| :--- | :--- | :--- |
| AST (`BorrowExpr`, `AssignStmt`, `MatchExpr`, `IdentPattern`, `UnsafeExpr`, `ScopeExpr`, `SpawnExpr`) | `IMPLEMENTED` | AST nodes & spans preserved. |
| Shallow `WON-MUT-001` & `WON-SEC-001`/`002` | `IMPLEMENTED` | Enforced in AST-walk type checker. |
| Normative Ownership-Lite Language Rules | `SPECIFIED` (Proposed Phase 2B.0) | Defined in [`ownership-and-borrowing.md`](../language-spec/ownership-and-borrowing.md); awaits owner approval. |
| Typed Ownership CFG + Borrow Analysis + Drop Elaboration | `SPECIFIED` (Design Only) | Architecture specified below; implementation starts in Phase 2B.1+. |
| ADR-009 TIR Multi-Backend Lowering & `Drop` Emission | `SPECIFIED` (Deferred to Phase 2C) | CFG designed here lowers directly into ADR-009 TIR. |
| `scope` / `s.spawn` Runtime & `Send`/`Sync` Enforcement | `SPECIFIED` (Deferred to Phase 3 runtime) | Trait checks designed structurally here cleanly. |

---

## 2. Core Design Goals & Non-Goals

### 2.1 Goals
1. **Zero Named Lifetime Syntax (`'a`)**: Preserve ADR-003 ergonomics while proving memory safety soundness in safe WON code.
2. **Sound Aliasing & Temporal Safety**: Guarantee single-owner RAII, no use-after-move, no double-free, no dangling references, and no simultaneous shared+exclusive aliasing (`&T` XOR `&mut T`) on safe paths.
3. **Flow-Sensitive Practicality (Non-Lexical Lifetimes within Scopes)**: Avoid frustrating lexical-block deadlocks when a reference's last use precedes subsequent mutation of the borrowed owner, while preserving deterministic lexical drop order for values.
4. **Structural Disjoint Borrow Splitting**: Allow simultaneous `&mut s.a` and `&mut s.b` on distinct struct fields and tuple indices without false conflicts.
5. **Direct Path to ADR-009 TIR**: Lower `TypedProgram` once into a basic-block Ownership CFG whose places, moves, borrows, and elaborated drops translate 1:1 into Phase 2C TIR.
6. **AI-Actionable Diagnostics**: Emit structured `diagnostic.Diagnostic` records (`WON-MEM-*`) with exact spans, borrow/move provenance spans, and valid `Edit` patches where unambiguous.

### 2.2 Explicit Non-Goals (Deferred / Out of Scope for Phase 2B)
1. Explicit lifetime generics on functions or user structs (e.g., `struct View<'a>` or `fn f<'a, 'b>`) — excluded by ADR-003.
2. Safe self-referential structs (`struct Node { data: String, slice: &str }`) — users employ `Shared[T]`, indices, or `Arena[T]`.
3. Disjoint array-index proofs with symbolic subscripts (`&mut arr[i]` and `&mut arr[j]` for dynamic `i != j`) — conservatively treated as whole-array borrows until slice-splitting APIs exist (`split_at_mut`).
4. Closure borrow-capture inference (`Fn` / `FnMut` / `FnOnce` environments) — deferred until closure AST/semantics are ratified.
5. Inter-procedural whole-program alias analysis across opaque FFI or dynamic dispatch — FFI safety relies on `unsafe:` + `.wonbind` contracts.

---

## 3. Architectural Position in the Compiler Pipeline

Phase 2B introduces an explicit static-analysis layer directly after `Checker.CheckProgram` (`TypedProgram`) and immediately upstream of Phase 2C TIR lowering and code generation:

```text
  Source (.won)
       │
       ▼
  [1. Lexer & Off-Side Parser]      --> *ast.Program
       │
       ▼
  [2. Type Checker (`checker`)]     --> *TypedProgram (Types, Symbols, ExprTypes, CallTargets, FieldRefs, VariantRefs)
       │
       ▼
  [3. Ownership CFG Builder]        --> *cfg.Program (BasicBlocks, Places, Rvalues, Drops, StorageLive/Dead)
       │
       ├──► [4. Move & Init Dataflow]      (Definite Init, Move/Reinit, Partial Move, Drop Flags)
       ├──► [5. Borrow & Region Solver]    (NLL Liveness, Constraints, Conflicts, Prov(e) Return Check)
       ├──► [6. Drop Elaboration]          (Explicit StorageDead / Drop / ConditionalDrop edges)
       └──► [7. Concurrency Cap Check]     (Future Phase 3 hook: Send/Sync across SpawnExpr)
       │
       ▼
  [8. Verified Program / Gate]      --> Semantic Diagnostics (WON-MEM-*) or Verified CFG -> TIR (Phase 2C) / C11 Gate
```

**Why Borrow Checking Must Run on CFG Instead of AST Recursion**:
- AST-recursive checks cannot accurately track path-sensitive state across `if`/`else`, early `return`, `break`/`continue` in `while`/`for`, `match` arm divergence (`!`), or `?` error-propagation short-circuits.
- Without a CFG, non-lexical last-use release (`let r = &x; use(r); x = 1`) and conditional initialization (`let x: String; if cond: x = a else: x = b; use(x)`) either require fragile ad-hoc hacks or reject valid idiomatic WON code.
- Building the Ownership CFG once creates the exact basic-block skeleton needed for Phase 2C TIR (`ADR-009`), avoiding throwaway AST passes.

---

## 4. Ownership-Lite Domain Model & Core Abstractions

The Ownership CFG (`internal/cfg`) represents every function and inherent method body in canonical machine-checkable primitives:

### 4.1 Places (`Place` & `Projection`)
A **Place** identifies a memory location rooted at a stack local, parameter, temporary, static global, or dereferenced reference/pointer:

```go
type LocalID uint32 // 0 = _0 (return slot), 1..k = params, k+1.. = user locals & temps

type Place struct {
    Local       LocalID
    Projections []Projection
}

type ProjectionKind uint8
const (
    ProjField    ProjectionKind = iota // .field_name (struct or tuple index)
    ProjDeref                          // *place (deref of &T, &mut T, *const T, *mut T)
    ProjDowncast                       // enum variant payload projection after tag match
    ProjIndex                          // [local_idx] (dynamic array/slice element -> coarse parent conflict)
    ProjConstIdx                       // [k] constant tuple/array literal index
)

type Projection struct {
    Kind       ProjectionKind
    FieldIndex int    // Struct FieldMap order or tuple slot
    VariantIdx int    // Enum VariantInfo index
    ConstIndex int64
    IndexLocal LocalID
    ElemType   types.TypeID
}
```

### 4.2 Place Path Equivalence & Conflict Algebra
Given two places $p_1 = (L_1, \pi_1)$ and $p_2 = (L_2, \pi_2)$:
- If $L_1 \neq L_2$ (distinct non-aliased locals): **Disjoint**.
- Compare projections prefix-by-prefix up to $\min(|\pi_1|, |\pi_2|)$:
  1. Different `ProjField` indices on the same struct/tuple -> **Disjoint** (enables split field borrows `&mut s.x` and `&mut s.y`).
  2. Different `ProjDowncast` variants on the same enum -> **Disjoint**.
  3. Any `ProjIndex` on the same array/slice vs another `ProjIndex` or `ProjConstIdx` on that array/slice -> **May Alias / Conflicting** (conservative).
  4. Any `ProjDeref` on a reference/pointer steps through borrowed region provenance rather than purely local stack projections; dereferencing `&mut T` acts as a reborrow of the target provenance, while dereferencing `&T` can never produce a `&mut` place.
  5. If $\pi_1$ is a prefix of $\pi_2$ (or vice versa), or equal -> **Overlapping / Conflicting**.

### 4.3 Ownership Value Semantics: `Copy` vs `Move` Policy
Phase 2B reconciles `types/relations.go` (`IsCopy`) with `ownership-and-memory.md`:

1. **Always `Copy` (Bitwise Copyable, Trivial No-Drop)**:
   - Integer primitives (`i8..i128`, `u8..u128`, `isize`, `usize`), floats (`f32`, `f64`), `bool`, `char`, `unit`, `!`.
   - Shared references (`&T` for any `T`), function pointers (`fn(...) -> R`), raw pointers (`*const T`, `*mut T`).
   - Arrays `[T; N]` and tuples `(T1, ..., Tn)` if and only if every element type is `Copy`.
2. **Never `Copy` (Always Move / Exclusive Resource)**:
   - Mutable references (`&mut T` — uniqueness invariant forbids copying).
   - `String`, `Vec[T]`, file/OS handles, synchronization guards, `Shared[T]`, `Arc[T]` (cloning `Shared`/`Arc` is explicit `.clone()` so reference-count traffic is visible).
   - Any type implementing `Drop`.
3. **User `struct` and `enum` `Copy` Policy (Normative Recommendation)**:
   - **Rule**: User-defined `struct` and `enum` types are **non-`Copy` (Move-by-default)** unless explicitly annotated `@derive(Copy)` (or `@copy`), which succeeds only if **every field / variant payload is `Copy`** and the type does not implement `Drop`.
   - **Rationale**: Implicit structural `Copy` silently turns refactoring (adding a `String` field to `Point` or `User`) into a breaking change at every call site; explicit opt-in matches Rust/systems ergonomics and preserves AI predictability while keeping Phase 2A's conservative `IsCopy == false` for user structs/enums backward-compatible!
   - *(Recorded as an explicit Owner Decision item in ADR-011 Section "Open Questions".)*

### 4.4 Region Variables & Provenance (`RegionID` and `Prov(e)`)
- Every borrow statement (`r = &place`, `r = &mut place`) introduces a fresh inference **Region** $\rho_i \in \text{RegionID}$, typed internally as `&'ρ_i T` or `&mut 'ρ_i T` (never written in user syntax).
- Function signatures with reference parameters assign symbolic **External Boundary Regions** $\rho_{\text{in},0}, \rho_{\text{in},1}, \dots$ for reference inputs and a **Static Region** $\rho_{\text{static}}$ for string literals `&'static str` and immutable statics.
- Return provenance $\text{Prov}(e)$ computes the finite subset of regions `{Local(place), Param(i), Static}` from which any reference returned by expression $e$ may derive.

---

## 5. Control-Flow Graph (CFG) IR Specification

Every checked `FunctionDecl` and `ImplBlock` method lowers from `(AST, TypedProgram)` to a `cfg.FuncCFG`:

```go
type BlockID uint32

type BasicBlock struct {
    ID          BlockID
    Statements  []Statement
    Terminator  Terminator
    Predecessors []BlockID
    Successors   []BlockID
}

type StmtKind uint8
const (
    StmtAssign       StmtKind = iota // Place = Rvalue
    StmtStorageLive                  // Lexical scope entry for LocalID
    StmtStorageDead                  // Lexical scope exit (uninit / stack reclaim)
    StmtDrop                         // Explicit drop slot (filled during Drop Elaboration)
    StmtDeinit                       // Explicit move/reinit invalidate marker
    StmtNop
)

type OperandKind uint8
const (
    OpCopy  OperandKind = iota // Place type satisfies IsCopy
    OpMove                     // Place type does not satisfy IsCopy (consumes Place)
    OpConst                    // Literal / static constant
)

type RvalueKind uint8
const (
    RvUse          RvalueKind = iota // Direct Copy/Move/Const operand
    RvRef                            // &place or &mut place (creates Loan(RegionID, Mutability, Place))
    RvAggregate                      // StructLiteral / ArrayLiteral / TupleLiteral / EnumCtor
    RvUnary                          // Unary op (excluding *deref which is Place Projection)
    RvBinary                         // Binary arithmetic / comparison / logic
    RvCast                           // Explicit `as` cast
    RvAddressOf                      // &raw const place / &raw mut place (if lowered)
)

type TermKind uint8
const (
    TermGoto        TermKind = iota // Unconditional edge to Target
    TermSwitchInt                   // Conditional branch (if / while / match discriminant)
    TermCall                        // Call(fn, args, dest: Place, next: BlockID, unwind/panic?)
    TermReturn                      // Return from _0
    TermUnreachable                 // Diverging `!` tail
    TermErrorPropagate              // `?` early-return edge on Err / None
    TermScopeJoin                   // Structured concurrency `scope` join barrier (Phase 3+)
)
```

### 5.1 Canonical Lowering Rules for Key WON Constructs
1. **Compound Assignment (`x += y`, `a.b = c`)**: Evaluates RHS operands left-to-right into temporaries before mutating `LHS` place, so `x = consume(x)` evaluates `consume(x)` first, checks move-conflict before reinitializing `x`.
2. **Short-Circuit Boolean Ops (`a and b`, `a or b`)**: Lowered to `SwitchInt` basic blocks so conditional initialization or conditional moves in `b` do not unconditionally apply along the short-circuited edge.
3. **`if` / `elif` / `else`**: Branches rejoin at a merge block; `StorageLive`/`StorageDead` mark block-scoped locals (`ScopeBlock`) cleanly so declarations inside `then` leave scope prior to merge.
4. **`while` & `for`**: Loop header dominates body and exit; `break` jumps to loop exit after executing enclosing loop-body block `StorageDead` / `Drop` cleanup; `continue` jumps to header after cleanup.
5. **`match` & `IdentPattern` (`[ref] [mut] ident`)**:
   - Discriminant read checks shared access without moving unless an arm binds a non-`Copy` payload by value (`Move`) instead of `ref` / `ref mut` (`Ref`).
   - Guarded match arms (`if guard:`) branch back to next arm on false without permanently invalidating scrutinee on the failure edge (two-phase / arm-scoped binding move!).
6. **`?` (`TryExpr`)**: Lowers to enum tag switch: `Ok(v)` / `Some(v)` binds `v` and continues to `next_block`; `Err(e)` / `None` writes return value into `_0`, triggers active local drops in LIFO order along the early-return path, and terminates with `TermReturn`.
7. **`unsafe:` (`UnsafeExpr`)**: Preserved as an attribute bit (`InUnsafe bool`) on statements/calls/derefs in the CFG. Ownership move/borrow checks for safe local `&`/`&mut` bindings **still operate across `unsafe:` boundaries**, while raw-pointer (`*const T`, `*mut T`) dereferencing and arithmetic are permitted only where `InUnsafe == true` (`WON-SEC-002`).

---

## 6. Definite Initialization & Move Semantics

Move analysis runs as a forward bit-vector / lattice dataflow pass over `FuncCFG`:

### 6.1 Lattice State per Tracked Place Path (`InitState`)
Every non-`Copy` local variable and its initialized field paths carry a 3-state lattice element at each program point `(bb, stmt_idx)`:

$$\text{InitState}(p) \in \{\bot\text{ (Uninit)}, \top_{\text{moved}}\text{ (Moved)}, \text{Init}\text{ (Initialized)}, \text{MaybeInit}\text{ (Path-Split / Conditional)}\}$$

More precisely, for composite structs without `Drop`, state tracks **per-field initialization** $\text{Init}(L.f_i) \in \{0, 1\}$, and aggregates over incoming CFG edges via join:
- `MaybeInit` means initialized along at least one predecessor path and uninitialized/moved along at least one other path.

### 6.2 Transfer Rules
1. **Declaration**:
   - `let x: T = init_expr`: Evaluates `init_expr` -> `Init(x) = Init`.
   - `let x: T` (deferred init, if permitted by grammar): `Init(x) = Uninit`.
2. **Use (`OpMove(p)`)**:
   - Requires `p` (and all sub-fields of `p` if aggregate) to be definitely `Init`.
   - If `p` is `Moved` or `MaybeInit` (moved on some predecessor edge): emit **`WON-MEM-001`** (`Use of moved value`, reporting both the use span and the prior move span).
   - If `p` is `Uninit`: emit **`WON-MEM-010`** (`Use of uninitialized variable`).
   - After move succeeds: mark `p` (and all descendant fields of `p`) as `Moved` (record `MoveOriginSpan`).
3. **Partial Move**:
   - Moving `s.field` marks `s.field` as `Moved`, leaving `s.other_field` `Init`.
   - Using `s.other_field` remains legal!
   - Using whole `s` (moving or borrowing `&s` / `&mut s`) while any field of `s` is `Moved` fails with **`WON-MEM-001`** (partially moved struct).
   - **Critical Invariant (`Drop` Guard, `WON-MEM-007`)**: Partial moving a field out of a value whose root struct implements `Drop` (or contains a `Drop` destructor on the parent container) is **always rejected** with **`WON-MEM-007`**, because the parent destructor requires the whole invariant struct at drop time.
4. **Reinitialization (`AssignStmt` to moved/uninit variable)**:
   - Reassigning to a `var` variable or field (`x = new_val` or `s.field = new_val`) restores `Init(p) = Init`!
   - If all moved fields of a struct are reinitialized, whole `s` returns to `Init`.
   - Reassigning an already-`Init` non-`Copy` `var` variable emits an implicit `Drop` of the prior value **before** storing `new_val`.
   - Assigning into an immutable `let` binding triggers `WON-MUT-001` (already enforced by AST checker; CFG asserts mutability invariant defensively).
5. **Loop Edges & Repeated Move Protection**:
   - Moving a non-`Copy` outer variable inside a `while`/`for` loop body makes `x` `Moved` on the back-edge into the loop header unless `x` is unconditionally reinitialized prior to loop continuation -> caught cleanly as `MaybeInit`/`Moved` on loop iteration 2 (`WON-MEM-001`)!

---

## 7. Borrow Semantics & Aliasing Rules

Borrow checking enforces **XOR Aliasing** (Multiple Shared Readers `&T` **OR** Single Exclusive Writer `&mut T`) at every CFG point `(bb, stmt_idx)`.

### 7.1 Loan Representation
Every `RvRef` creates a **Loan**:

```go
type BorrowKind uint8
const (
    BorrowShared BorrowKind = iota // &T (or implicit reborrow/match ref)
    BorrowMut                      // &mut T
)

type Loan struct {
    ID         uint32
    Kind       BorrowKind
    Borrowed   Place       // Exact place path being borrowed
    Region     RegionID    // Inference lifetime region of resulting ref
    OriginSpan diagnostic.Span
    DropScope  ScopeID     // Lexical scope anchor for Drop interaction
}
```

### 7.2 Exclusivity & Access Compatibility Matrix
When a program point performs an **Access** `(AccessKind, TargetPlace)` while a `Loan(Kind, Borrowed)` is **Active** (live at that CFG point) and `TargetPlace` conflicts with `Loan.Borrowed` (per Section 4.2):

| Active Loan Kind | Attempted Shallow/Deep Read (`Copy` / `&T`) | Attempted Write (`=`) / `&mut T` / `Move` | Attempted `StorageDead` / `Drop` |
| :--- | :--- | :--- | :--- |
| **`BorrowShared` (`&T`)** | **ALLOWED** (Multiple shared readers) | **REJECT `WON-MEM-005` / `WON-MEM-006`** | **REJECT `WON-MEM-003`** (Borrowed value dropped too early) |
| **`BorrowMut` (`&mut T`)** | **REJECT `WON-MEM-004` / `WON-MEM-001`** | **REJECT `WON-MEM-005` / `WON-MEM-006`** | **REJECT `WON-MEM-003`** (Mut-borrowed value dropped too early) |

Note on **Path Prefix vs Suffix Conflicts**:
- **Deep Conflict**: If `&mut s` is active, reading or borrowing `&s.a` or `&mut s.a` conflicts because `s` is a prefix of `s.a`.
- **Supporting Prefix Freeze**: While `&s.a` is active, `s` (ancestor prefix) cannot be moved or written as a whole (`WON-MEM-006`), and `&mut s` cannot be taken (`WON-MEM-005`), **but** `&mut s.b` (disjoint sibling) **is allowed**!
- **Shallow Read Exception for Deref**: Reading `*r` where `r: &mut T` or taking a copy of `r`'s target when `r` itself is not aliased accesses through `r`, extending `r`'s region.

### 7.3 Auto-Reborrowing (`&mut T -> &mut T` / `&mut T -> &T`)
- Passing `var r: &mut Point` to `fn inspect(p: &Point)` or `fn mutate(p: &mut Point)` does **not** necessarily consume `r` permanently if auto-reborrowed (`&*r` / `&mut *r`):
  - If expected type is `&U` or `&mut U` and source expression is a place of type `&mut T`: lower as reborrow loan of `*r`. While the reborrow's region is live, `r` is temporarily frozen; once the reborrow ends (e.g., after the function call returns), `r` becomes usable again!
  - Moving `r` into a binding `let r2 = r` without reborrowing transfers `r` directly (`OpMove` since `&mut T` is non-`Copy`).

### 7.4 Method Call Receivers (`&self`, `&mut self`, `self`)
- Method call lowering checks parameter 0 (`Symbol.SelfIsRef`, `Symbol.SelfIsMut`):
  - `self` -> `OpMove` (or `OpCopy` if `IsCopy`).
  - `&self` -> `RvRef(BorrowShared, receiver_place)`.
  - `&mut self` -> `RvRef(BorrowMut, receiver_place)`.
- **Nested Two-Phase / Receiver Order**: In `vec.push(vec.len())`, arguments evaluate cleanly cleanly: if receiver borrows `&mut vec` only at call entry after computing `args`, `vec.len()` (`&vec`) completes before the `&mut vec` activation if two-phase borrows are enabled — OR conservative evaluation order rejects aliasing unless explicit temporary `let n = vec.len(); vec.push(n)` is used. **Phase 2B.1 Choice**: Start with **strict left-to-right standard evaluation (no implicit two-phase magic except documented in Open Questions)** so aliasing semantics are 100% transparent to both humans and AI agents!

---

## 8. Non-Lexical Borrow Regions, Control-Flow & Provenance Analysis

To combine Rust NLL usability with WON's **zero named lifetime syntax**, Phase 2B separates **Value Drop Lifetime** (strictly lexical reverse-declaration order at scope exit) from **Borrow Active Region** (flow-sensitive dataflow liveness within function CFG):

### 8.1 Why Flow-Sensitive Regions (NLL) Without `'a` Annotations?
Under naive AST-lexical lifetimes:
```won
var x = 10
let r = &x
println(r)
x = 20       # Would fail needlessly if `r` lived until `}` even though `r` is dead!
```
With **CFG Region Liveness**:
1. Each reference variable/temp carries an inferred Region $\rho \subseteq \text{ProgramPoints(FuncCFG)}$.
2. **Region Liveness (Backward DFA)**: A region $\rho$ is **live** at CFG point $P$ if any variable whose type mentions $\rho$ is live at $P$ (i.e., will be read along some path from $P$ before being overwritten or reaching `StorageDead`, OR its destructor requires running at drop time — note: references `&T`/`&mut T` have trivial no-op drop!).
3. **Outlives / Subset Constraints ($\rho_a : \rho_b$ at point $P$)**: Generated at assignments (`r2 = r1` -> $\rho_1 \supseteq \rho_2$ live where $r_2$ is live), function calls binding output provenance to input reference regions, and reborrows (`r_sub = &*r_parent` -> $\rho_{\text{parent}} \supseteq \rho_{\text{sub}}$).
4. **Fixed-Point Propagation**: Solve subset equations until every $\rho_i$ holds the minimal set of CFG points where loans originating $\rho_i$ must remain active.
5. **Drop Liveness Subtlety**: If a variable `guard: RefStruct` has a non-trivial `Drop` (impossible in safe code for borrows until/unless lifetime-carrying structs exist, which are currently excluded), its drop point keeps references live; because Phase 2B forbids safe structs holding `&T`/`&mut T`, references only live in locals/params/temps/tuples!

### 8.2 Function Boundary Provenance (`Prov(e)`)
Reconciling `ownership-and-memory.md` Section 5 & `phase-1a.1-semantic-closure.md`:

For any function signature `fn f(p0: T0, ..., pk: Tk) -> Ret`:
- **Case A (`Ret` contains no reference)**: No return provenance constraint. Loans created for arguments end as soon as call finishes (unless `Ret` borrows, see Case B).
- **Case B (`Ret` is `&R` or `&mut R`)**: Compute $\text{Prov}(e_{\text{ret}})$ for every `return e` and tail expression `e` inside `f`'s CFG:
  1. **Local Escape Check (`WON-MEM-003`)**:
     $$\text{Prov}(e_{\text{ret}}) \cap \{\text{Local}(v) \mid v \text{ is local stack slot of } f\} \neq \emptyset \implies \textbf{REJECT }(\text{WON-MEM-003})$$
  2. **0 Reference Parameters & Not Static (`WON-MEM-003`)**: No valid caller provenance exists for a non-static returned reference.
  3. **Unique Origin Provenance (Accepted)**:
     - All return paths derive exclusively from $\text{Param}(i)$ (plus optional `Static`) for **one unique input reference parameter $i$** (for methods, commonly `&self` or `&mut self`, OR a single explicit `&arg` parameter — **not** hardcoding `&self` per `DEF-011`!), **OR** strictly from `Static` (`&'static str`).
     - The function's internal `BorrowSignature` records `ReturnProvenance = Param(i)` (or `Static`).
     - At every call site `let out = f(arg0, ..., argk)`, the caller binds $\text{Region}(out) \subseteq \text{Region}(arg_i)$ automatically without lifetime syntax!
  4. **Ambiguous Multi-Input Return (`WON-MEM-002`)**:
     - If $\text{Prov}(e_{\text{ret}})$ contains $\{ \text{Param}(i), \text{Param}(j) \}$ with $i \neq j$ (e.g., `if cond: return a else: return b`), reject deterministically at definition of `f` with **`WON-MEM-002`**!
     - Provide structured remediation hints: return an owned `T`, return `Shared[T]`, write through `&mut Out`, or refactor to single reference provenance.

> [!NOTE]
> **Soundness Note**: What about recursion (`fn walk(n: &Node) -> &Node`)? Intra-procedural provenance on `fn` signature determines whether `Ret` derives from unique parameter `0` (`self`/`n`). Checking the body assumes recursive call preserves unique parameter `i` -> check reaches fixed point trivially because signature is fixed once declared or checked with optimistic single-param unification!

---

## 9. Scope Exits, Destruction Order & Drop Elaboration

Every owning variable obeys **Deterministic Lexical RAII Drop Semantics**:

### 9.1 Destruction Ordering Rules
1. **Reverse Declaration Order**: Within a lexical block (`BlockStmt`), variables declared later are dropped **before** variables declared earlier (`StorageDead` order mirrors reverse declaration).
2. **Scope-Exit Equivalence**: Every control-flow exit from a scope — normal fallthrough, `return`, `break`, `continue`, or `?` error-return — runs the drops for **all initialized bindings belonging to the exited scopes** in strict reverse declaration order.
3. **Moved / Uninitialized Suppression**: A binding that was moved away (`InitState == Moved`) or never initialized (`InitState == Uninit`) MUST NOT be dropped when leaving scope.
4. **Partial Initialization / Partial Move (Drop Flags)**:
   - If along merged paths a binding `x` is `MaybeInit` (e.g., moved inside `if cond:` only), Phase 2B/2C lowers a stack-allocated boolean **Drop Flag** (`_drop_flag_x: bool = false`, set `true` on init, set `false` on move) and emits `if _drop_flag_x: Drop(x)`.
   - If a non-`Drop` struct `s` had field `s.a` moved out (`WON-MEM-007` inapplicable because `s` itself has no `Drop`, only field `s.b` has `Drop`), Drop Elaboration drops `s.b` individually without touching moved `s.a`!
5. **Borrow-vs-Drop Ordering Check**:
   - Dropping a local variable `x` acts like an **exclusive write (`Move` / `StorageDead`)** on `x`.
   - If any loan on `x` is still live at `x`'s `Drop`/`StorageDead` point (for instance, returning `&x` or holding `&x` in an outer-scope variable `outer_ref` declared *before* `x`), emit **`WON-MEM-003`**!
   - *Declaration Order Subtlety*:
     ```won
     var r = &fallback    # r declared first -> dropped SECOND (last)
     let local = String::from("hi")
     r = &local           # r outlives local because r drops AFTER local! -> REJECT WON-MEM-003
     ```
     Wait: if `r` is never used *after* `local` exits scope and has trivial drop (`&String` has no destructor!), NLL ends `r`'s loan after `r`'s last use **before** `local` drops, so `r = &local; println(r)` inside the block **succeeds**, whereas using `r` after `local` exits scope triggers **`WON-MEM-003`**!

---

## 10. Mutability, Binding Modifiers & Field Borrowing

WON enforces clear, layered mutability rules (extending implemented `WON-MUT-001`):

1. **Binding Mutability (`let` vs `var`)**:
   - `let x: T = v`: `x` cannot be reassigned, its fields cannot be mutated directly, and `&mut x` cannot be created (`WON-MUT-001`).
   - `var x: T = v`: Allows rebinding `x = v2`, mutating `var` fields, and creating `&mut x`.
   - *Note on `var` + move*: Moving out of an immutable `let x = s` is **allowed** (`let s2 = s`) because moving consumes `x` rather than mutating its in-place value!
2. **Parameter Mutability (`x: T` vs `var x: T` vs `x: &mut T`)**:
   - Calling `f(x)` with `x: &mut T` requires `x` to be a `var` binding (or already an exclusive `&mut` chain); parameter passing by value moves or copies regardless of callee `var`.
3. **Struct Field Mutability (`struct S: var a: i32; b: i32`)**:
   - Current Phase 2A (`WON-ADT-004`) already distinguishes immutable fields (`b`) vs `var` fields (`a`).
   - **Phase 2B Interaction**:
     - Mutating `s.b = 1` requires `s` to be `var`/exclusive **AND** `b` to be `var` (`WON-ADT-004` + `WON-MUT-001`).
     - Taking `&mut s` grants exclusive access to the whole `s` (for passing to `fn f(p: &mut S)` or whole replacement `s = S { ... }` when `s` is `var`), while field assignment through `s.b = 1` continues honoring field `var` encapsulation (`WON-ADT-004`).
     - Borrow splitting: `&mut s.a` and `&mut s.b` (or `&s.a` and `&mut s.b`) never conflict because `ProjField(a) != ProjField(b)`.
4. **Through-Reference Dereferencing**:
   - Given `r: &T`: `*r` is read-only. Forming `&mut *r` is rejected (`WON-MUT-001`). Moving non-`Copy` `*r` out of `&T` or `&mut T` without replacement is rejected (`WON-MEM-008`: cannot move out of borrowed content).
   - Given `r: &mut T`: `*r = new_val` is allowed (drops old `*r`, writes `new_val`), and reborrowing `&mut *r` is allowed.

---

## 11. Safe vs Unsafe Boundaries, Raw Pointers & C Interop

Phase 2B cleanly reconciles strict safety with WON's C-interop core mission:

### 11.1 Raw Pointers (`*const T`, `*mut T`)
- `*const T` and `*mut T` are **`Copy`** (`types/relations.go`) and carry **no compiler-verified lifetime or aliasing provenance**.
- Creating a raw pointer from a reference (`r as *const T`, `r as *mut T`) is safe in Phase 2A (`CanCast` in `relations.go`) and **ends at the borrow boundary** unless tied by pointer provenance — wait: converting `&x as *const T` does **not** keep `&x` live indefinitely, so dangling raw pointer dereference inside `unsafe:` is the programmer's responsibility.
- Dereferencing `*p` (read or write) or calling `p.offset(...)` is **forbidden in safe code** (`WON-SEC-002` — already `IMPLEMENTED` in Phase 2A.1!).
- **Forbidden Implicit Escalation**: Raw pointers (`*const T`, `*mut T`) **MUST NOT** implicitly coerce to safe references (`&T`, `&mut T`). Converting `*const T -> &T` or `*mut T -> &mut T` (if exposed via `unsafe` intrinsic in future) requires an explicit `unsafe:` block, and the borrow checker assigns the resulting reference a local temporary borrow whose soundness rests on the `unsafe:` caller invariant.

### 11.2 Direct C FFI (`#import <header.h>`) & `.wonbind` Contracts
- Direct C calls (`zlib::compress(...)`) require `unsafe:` unless backed by a safe `.wonbind` wrapper (`WON-SEC-001`).
- **Safe Args Passing into FFI**: Passing `&buf[0]` / `s.as_ptr()` / `var` buffer pointer to C:
  - Within safe caller CFG, the borrow created to obtain the pointer (e.g., `buf.as_mut_ptr()`) creates a call-scoped loan on `buf` (`BorrowShared` for `as_ptr`, `BorrowMut` for `as_mut_ptr`) for the duration of that expression/pointer usage, preventing concurrent safe mutation during the call!
- **`.wonbind` Ownership Contracts (`c-interop-and-unsafe.md`)**:
  - `@borrow p: *const u8`: Caller guarantees loan lives through the foreign call; foreign code MUST NOT retain `p` past return.
  - `@owned p`: Transfers ownership out of WON into C (acts like `OpMove` / suppresses WON `Drop` to prevent double-free!).
  - `@out_buffer` / `@retained`: Marks newly C-owned buffer returned to WON wrapper for RAII wrapping.
  *(Note: `.wonbind` parser integration is `SPECIFIED` / deferred past Phase 2B.1 core borrow checker.)*

---

## 12. Interior Mutability & Explicit Shared Ownership (`Shared[T]`, `Arc[T]`, `Mutex[T]`)

When static single-owner trees cannot express graphs, caches, or shared concurrent state:

| Construct | Thread Safety | Aliasing Strategy | Safety Guarantee | Status |
| :--- | :--- | :--- | :--- | :--- |
| `Cell[T]` (`T: Copy`) | `!Sync` | Copy-in / Copy-out without handing out `&mut T` | Compile-time `!Sync`, zero runtime cost | `SPECIFIED` |
| `RefCell[T]` | `!Sync` | Dynamic runtime borrow counter (`borrow()`, `borrow_mut()`) | Panic on runtime aliasing violation; `!Send`/`!Sync` prevents cross-thread race | `SPECIFIED` |
| `Shared[T]` / `Weak[T]` | `!Send`, `!Sync` | Non-atomic single-thread refcount; `.clone()` explicit; immutable payload unless paired with `RefCell`/`Cell` | Deterministic free at count `0`; never moves across threads | `SPECIFIED` |
| `Arc[T]` | `Send + Sync` (if `T: Send + Sync`) | Atomic refcount; `.clone()` explicit; requires `Mutex[T]`/`RwLock[T]`/`Atomic*` for mutation | Thread-safe refcount without data races | `SPECIFIED` |
| `Mutex[T]` / `RwLock[T]` | `Send + Sync` (if `T: Send`) | OS/futex lock returning RAII guard (`Guard[T]`) | Cannot access inner `T` without active guard; lock released deterministically on `Drop` | `SPECIFIED` |

*(Interaction with Phase 2B Borrow Checker)*: Because `RefCell::borrow_mut()` returns a `RefMut[T]` guard whose lifetime ties to `&self` (unique input param provenance!), the borrow checker statically ensures the `RefMut[T]` guard cannot outlive the `RefCell` or `Mutex`, seamlessly bridging runtime dynamic borrowing with compile-time lifetime safety!

---

## 13. Concurrency Interactions (`scope` / `s.spawn`, `Send` / `Sync`)

Aligning with ADR-008 (`scope` nurseries) and Phase 3 roadmap:

1. **Auto-Trait Classification (Compiler-Computed Structural Traits)**:
   - `Send`: Safe to transfer ownership to another thread/task.
     - Primitives, `String`, `Vec[T]` (`T: Send`), `Arc[T]` (`T: Send + Sync`), `Mutex[T]` (`T: Send`), structs/enums where **all fields are `Send`**.
     - **NOT `Send`**: `Shared[T]`, `Weak[T]`, `*const T`, `*mut T`, `&T` (only `Send` when `T: Sync`!), `&mut T` (only `Send` when `T: Send`).
   - `Sync`: Safe to share `&T` simultaneously across threads ($T: \text{Sync} \iff \&T: \text{Send}$).
     - Primitives, `String`, `Vec[T]` (`T: Sync`), `Mutex[T]` (`T: Send`), `Atomic*`, structs/enums where **all fields are `Sync`**.
     - **NOT `Sync`**: `Cell[T]`, `RefCell[T]`, `Shared[T]`, `*const T`, `*mut T`.
2. **Structured Concurrency Capture Contract (`scope s: ... s.spawn:`)**:
   - Every captured variable inside `s.spawn:` is analyzed by the CFG borrow checker just like a synchronous closure/block, **plus** boundary region & trait constraints:
     - **Move capture (`v`)**: Requires `T: Send`. `v` becomes `Moved` in parent scope immediately at `s.spawn:`!
     - **Shared borrow capture (`&v`)**: Permitted **only if `T: Sync`** AND the active loan on `v` spans **the entire enclosing `scope s:` block** (because child tasks may run anywhere until the `TermScopeJoin` barrier at the end of `scope s:`!).
     - **Mutable borrow capture (`&mut v`)**: **FORBIDDEN across `s.spawn:` boundaries (`WON-CON-001`)** because two sibling spawned tasks inside `scope s:` could alias or violate exclusive cross-task access! Mutating shared state across tasks requires `Mutex[T]` / `RwLock[T]` / `Atomic*` / `Channel[T]`.
   - At `TermScopeJoin` (end of `scope s:`), all child-task shared borrows release, restoring exclusive/mutable access in the parent scope!
3. **Phase Scope Note**: Since `scope`/`spawn` currently trigger `WON-SEM-010`, Phase 2B prepares the `TermScopeJoin` & `Send`/`Sync` interface metadata cleanly without enabling runtime threading prior to Phase 3.

---

## 14. Diagnostic System Integration (`WON-MEM-*` Schema & AI Auto-Fixes)

All Phase 2B diagnostics use `compiler/poc/internal/diagnostic.Diagnostic` (`Version: 1`), preserving strict AI-agent neutrality (ADR-004).

### 14.1 Standardized Error Code Catalogue

Notice existing specification IDs `WON-MEM-001`..`007` in [`ownership-and-memory.md`](../language-spec/ownership-and-memory.md) are preserved verbatim with zero renumbering; Phase 2B adds `WON-MEM-008`..`010` for completeness:

| Code | Severity | Meaning | Key Primary/Secondary Spans & Explanation |
| :--- | :--- | :--- | :--- |
| **`WON-MEM-001`** | `error` | **Use of moved value (or partially moved value)** | Primary span: use site. Explanation cites line/column where value was moved (`MoveOriginSpan`) and suggests borrowing (`&x`) or explicit `.clone()` if available. |
| **`WON-MEM-002`** | `error` | **Ambiguous return reference provenance** | Primary span: function return type or conflicting `return` expr. Lists candidate parameter names (`a`, `b`), and advises returning owned `T`, `Shared[T]`, or `&mut out`. |
| **`WON-MEM-003`** | `error` | **Dangling reference / local variable escape / drop while borrowed** | Primary span: returned `&local` or outliving `Drop` / `StorageDead`. Explanation shows referenced binding scope vs return/borrow outlive target. |
| **`WON-MEM-004`** | `error` | **Cannot borrow mutably while immutably borrowed (or read while mutably borrowed)** | Primary span: conflicting borrow/read. Explanation cites active immutable loan origin span & last use point. |
| **`WON-MEM-005`** | `error` | **Cannot borrow mutably more than once at a time (or while borrowed)** | Primary span: second `&mut x` / call. Explanation cites first active `&mut` loan span. |
| **`WON-MEM-006`** | `error` | **Cannot assign to or move borrowed value while active loan exists** | Primary span: assignment / move site. Explanation cites active borrow span blocking mutation. |
| **`WON-MEM-007`** | `error` | **Cannot partially move field out of type that implements `Drop`** | Primary span: field move expr. Suggests borrowing `&s.field`, swapping (`mem::replace`), or moving whole `s`. |
| **`WON-MEM-008`** | `error` | **Cannot move non-`Copy` value out of borrowed reference (`&T` / `&mut T`)** | Primary span: deref/field move expr. Suggests borrowing `&*r` or `.clone()` instead of taking ownership through a reference. |
| **`WON-MEM-009`** | `warning` | **Unnecessary `var` binding (never mutated or mutably borrowed)** | Optional hygiene warning; emits AI auto-fix replacing `var` token span with `let`. *(Disabled or info in strict tests if configured).* |
| **`WON-MEM-010`** | `error` | **Use of possibly uninitialized binding** | Primary span: read/borrow/move of variable before definite initialization along all control-flow paths. |

*(Preserved existing codes in other subsystems)*:
- `WON-MUT-001` (`internal/checker/expressions.go`): Immutable binding mutation / `&mut` borrow on `let` (includes `LetTokenSpan` auto-fix `let` -> `var`).
- `WON-SEC-001` / `WON-SEC-002`: FFI & raw-pointer deref outside `unsafe:`.
- `WON-CON-001` (`concurrency-model.md`): `&mut` capture across `s.spawn:` (Phase 3).

### 14.2 Representative Machine-Readable JSON Output Example (`wonc check --json`)

```json
{
  "version": 1,
  "status": "error",
  "diagnostics": [
    {
      "code": "WON-MEM-001",
      "severity": "error",
      "message": "Use of moved value 's1'",
      "file": "examples/move_err.won",
      "span": {
        "file": "examples/move_err.won",
        "start": { "line": 4, "column": 13, "byte": 68 },
        "end": { "line": 4, "column": 15, "byte": 70 }
      },
      "explanation": "Value 's1' of non-Copy type 'String' was moved at line 3 column 14 ('let s2 = s1'), invalidating 's1'. Borrow immutably with '&s1' at line 3 if shared access was intended, or clone explicitly.",
      "fixes": [
        {
          "description": "Borrow 's1' instead of moving ownership (only valid if 's2' can accept '&String')",
          "edits": [
            { "start": 50, "end": 52, "replacement": "&s1" }
          ]
        }
      ]
    }
  ]
}
```

---

## 15. Internal Compiler Package & Data Structure Design (`compiler/poc/internal/`)

Concrete interfaces planned for Phase 2B.1+ (zero Go code added in Phase 2B.0):

1. **`internal/cfg/`**:
   - `cfg.go`: `ProgramCFG`, `FuncCFG`, `BasicBlock`, `LocalDecl`, `Place`, `Projection`, `Statement`, `Terminator`, `Rvalue`, `Operand`, `Loan`.
   - `builder.go`: `BuildFuncCFG(fn *ast.FunctionDecl, sym *symbols.Symbol, tp *checker.TypedProgram) *FuncCFG`.
   - `dump.go`: Deterministic textual CFG printer (`WON_DUMP_CFG=1` or test helper) for snapshot unit tests.
2. **`internal/borrow/`**:
   - `place_map.go`: Canonical path interning + `Conflicts(a, b Place) bool` + `IsStrictPrefix(a, b Place) bool`.
   - `init_move.go`: Forward worklist DFA computing per-block `InitBitSet` / `MovedOriginMap`, emitting `WON-MEM-001`, `WON-MEM-007`, `WON-MEM-008`, `WON-MEM-010`.
   - `liveness.go`: Backward liveness DFA over `LocalID` and inferred `RegionID`s.
   - `loans.go`: Forward loan propagation + conflict check against active live loans, emitting `WON-MEM-003`, `WON-MEM-004`, `WON-MEM-005`, `WON-MEM-006`.
   - `provenance.go`: Intra-procedural $\text{Prov}(e)$ evaluator verifying return references against parameters & locals (`WON-MEM-002`, `WON-MEM-003`).
   - `drop_elab.go`: Computes drop flags and inserts explicit `StmtDrop` / `DropScope` cleanup edges into `FuncCFG`.
   - `analyzer.go`: `AnalyzeProgram(tp *checker.TypedProgram, rep *diagnostic.Report) *ProgramCFG` entry point called from `Checker` or driver between Phase 2A `TypedProgram` and Phase 2C TIR lowering.

**Extension to `TypedProgram`**:
In Phase 2B.1, `checker.TypedProgram` gains an optional `CFG *cfg.ProgramCFG` field (populated after `CheckProgram` succeeds without type errors), keeping Phase 2A's existing fields unchanged!

---

## 16. Interaction with Backend & Future ADR-009 TIR (Phase 2C)

1. **Preserving Phase 2A.2 C11 Subset Gate (`WON-GEN-002`)**:
   - In Phase 2B.1, `verifyBackendCapabilities` continues enforcing `WON-GEN-002` at `wonc build` for constructs not yet lowered to C11 (structs, enums, refs, `String`, etc.).
   - However, **all Phase 2B positive and negative ownership programs run cleanly through `wonc check`** (`Lexer -> Parser -> Checker -> CFG -> BorrowChecker`) and emit exact `WON-MEM-*` diagnostics!
2. **Phase 2C TIR Bridge**:
   - Each `cfg.FuncCFG` maps 1:1 to `TIRFunction` basic blocks (`ADR-009`).
   - `OpMove(p)` -> TIR `Move(dest, src)`.
   - `StmtDrop(p)` / elaborated conditional drop flags -> TIR `Drop(target)`.
   - Zero duplicate CFG construction needed when implementing Phase 2C TIR optimization & expanded C11 codegen!

---

## 17. End-to-End Worked Analysis Walkthroughs

### Walkthrough A — Non-Lexical Region Release + Disjoint Field Borrow Split (PASS)
```won
struct Pair:
    var left: i32
    var right: i32

fn update_pair(var p: Pair):
    let r = &p.left       # Loan L1: BorrowShared on p.left, Region R0
    let snap = *r         # Last use of r (R0 live here, dead immediately after!)
    let mut_l = &mut p.left   # R0 is DEAD -> L1 inactive! Loan L2 (&mut p.left) ALLOWED
    let mut_r = &mut p.right  # L2 is on p.left; p.right is DISJOINT -> Loan L3 ALLOWED
    *mut_l = snap + 1
    *mut_r = 2
```
- **CFG Trace**: Backward region liveness computes `R0` live only between `let r = &p.left` and `let snap = *r`. At `let mut_l = &mut p.left`, `L1` is no longer active -> no `WON-MEM-005` conflict. At `let mut_r = &mut p.right`, `Conflicts(L2.Borrowed=p.0, L3.Borrowed=p.1)` is `false` -> accepted cleanly!

### Walkthrough B — Partial Move, Reinitialization & Conditional Drop Flag (PASS)
```won
struct Names:
    first: String
    last: String

fn swap_first(var n: Names, replacement: String) -> String:
    let old = n.first      # Partial move: n.first = Moved, n.last = Init (Names has no custom Drop)
    n.first = replacement  # Reinitializes n.first -> whole n returns to Init
    return old             # Caller drops n safely (all fields Init) and returns old
```
- **CFG Trace**: Neither `Names` nor custom `Drop` trait forbids moving `n.first` when `n` is wholly restored before `n` is used as a unit or dropped. Had `replacement` not been stored and `return` ran early, Drop Elaboration drops only `n.last` (since `n.first` is statically `Moved`).

### Walkthrough C — Conditional Move in Loop Caught on Back-Edge (`WON-MEM-001`)
```won
fn loop_move(items: [bool; 2]):
    let s = String::from("payload")
    for flag in items:
        if flag:
            consume(s)     # MoveOriginSpan on s
```
- **CFG Trace**:
  - Path 1 (`flag == false`): `s` stays `Init`.
  - Path 2 (`flag == true`): `s` becomes `Moved`.
  - Loop back-edge joins Path 1 and Path 2 at loop header -> `InitState(s) = MaybeInit`!
  - Fixed-point iteration revisits `consume(s)` with incoming `MaybeInit` -> emits **`WON-MEM-001`** (`Use of possibly moved value 's'`, pointing to `consume(s)`).

---

## 18. Verification & Test Suite Strategy for Phase 2B.1+

When implementation begins in Phase 2B.1, verification MUST cover six complementary test tiers:

1. **CFG Lowering Snapshot Tests (`internal/cfg/cfg_test.go`)**: Verify `if`, `while`, `for`, `match`, `?`, `break`/`continue`, short-circuit `and`/`or`, and `StorageLive`/`StorageDead` nesting produce exact expected basic-block predecessors/successors and reverse drop order.
2. **Place Algebra Unit Tests (`internal/borrow/place_map_test.go`)**: Exhaustive matrix testing prefix/disjoint/enum-downcast/array-index conflict symmetry.
3. **10 Positive + 12 Negative Normative Ownership Fixtures (`internal/borrow/borrow_test.go`)**: Automated fixture suite validating every canonical case specified in [`ownership-and-borrowing.md`](../language-spec/ownership-and-borrowing.md) (`PASS` or exact `WON-MEM-*` code + exact primary span line/col).
4. **AI JSON Diagnostic Contract Tests (`tests/protocol_test.go`)**: Assert `wonc check --json` outputs valid schema v1 diagnostics for `WON-MEM-001`..`010` with non-empty explanations and byte-valid edits.
5. **Differential / Fuzz Stability Tests (`internal/borrow/fuzz_test.go`)**: Extend `go test -fuzz` coverage across arbitrary control-flow + `&`/`&mut`/move ASTs to guarantee zero panics and bounded fixed-point convergence.
6. **Non-Regression of Existing Suite**: All existing `lexer`, `parser`, `types`, `symbols`, `checker`, `codegen`, and C11 GCC/Clang `-pedantic-errors -Werror` integration suites stay 100% green.

---

## 19. Phased Implementation Plan

| Sub-Phase | Scope | Deliverables | Exit Criteria |
| :--- | :--- | :--- | :--- |
| **Phase 2B.0 (Current)** | Architecture & Normative Proposal | `phase-2b.0-ownership-architecture.md`, `ownership-and-borrowing.md`, `ADR-011`, doc/CI updates | Owner review / CI documentation gates green; **zero Go code modifications**. |
| **Phase 2B.1** | Core Ownership CFG + Definite Init + Move/Reinit + Drop Flags + `@derive(Copy)` | `internal/cfg`, `internal/borrow/{place_map,init_move}.go`, `WON-MEM-001/007/008/010` | CFG unit tests + move/init/partial-move/loop fixtures pass under `wonc check` & CI. |
| **Phase 2B.2** | Full Non-Lexical Borrow Checker + Provenance Solver (`Prov(e)`) + AI Fix Edits | `internal/borrow/{liveness,loans,provenance,analyzer}.go`, `WON-MEM-002..006` | All 10 positive + 12 negative spec fixtures, protocol tests, and fuzz suite pass. |
| **Phase 2C** | TIR Lowering + Drop Elaboration + Expanded C11 Backend (Structs, Enums, `String`, `Drop`) | `internal/tir`, `drop_elab.go`, C11 emitter upgrade, lift `WON-GEN-002` for ownership types | End-to-end compiled binaries execute without leaks/double-frees (verified under ASan/UBSan). |
| **Phase 3+** | Structured Concurrency & Cross-Thread Safety | `Send`/`Sync` auto-trait solver, `scope s:` / `s.spawn:` `TermScopeJoin` enforcement (`WON-CON-001`) | Multi-threaded nursery tests compile & pass TSan cleanly. |

---

## 20. Open Design Decisions Requiring Owner Confirmation

Before locking Phase 2B.1 implementation, three deliberate policy questions are surfaced in [`ADR-011`](../decisions/ADR-011-ownership-and-borrow-checking-semantics.md) for owner confirmation:

1. **Implicit Structural `Copy` vs Explicit `@derive(Copy)`**:
   - Proposal favors explicit `@derive(Copy)` on user `struct`/`enum` (even when all fields are `Copy`), preventing silent API semver breaks and preserving Phase 2A behavior.
2. **Non-Lexical Region Liveness (NLL within scope) vs Strict Block-Lexical Scoping**:
   - Proposal adopts flow-sensitive NLL region release while keeping value `Drop` strictly at lexical block exit — dramatically eliminating false positives while keeping zero `'a` lifetime syntax.
3. **Strict Evaluation Order vs Two-Phase Method Receiver Borrows (`v.push(v.len())`)**:
   - Phase 2B.1 recommends strict evaluation (requiring explicit temporary `let n = v.len(); v.push(n)`) for predictability; two-phase method receiver exception can be added cleanly in Phase 2B.2 if ergonomic telemetry warrants it.

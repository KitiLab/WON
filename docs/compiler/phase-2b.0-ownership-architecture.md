# WON Phase 2B.0 / 2B.0.1 — Ownership-Lite Architecture, Borrow Semantics & Design Closure

> **Document Status**: `ACCEPTED` (Architectural Design Closed in 2B.0.1 & Ratified in [ADR-011](../decisions/ADR-011-ownership-and-borrow-checking-semantics.md); Phase 2B.1–2B.2 `IMPLEMENTED`)  
> **Milestone**: Phase 2B.0 / 2B.0.1 — Ownership Architecture & Semantic Design Closure  
> **Target Compiler Packages**: `compiler/poc/internal/cfg`, `compiler/poc/internal/borrow` (`2B.1–2B.2` Implemented; `2B.3` Planned), bridging `compiler/poc/internal/checker` (`TypedProgram`) to ADR-009 TIR (`Phase 2C`)  
> **Companion Specifications**:
> - [Ownership and Borrowing Specification](../language-spec/ownership-and-borrowing.md) (`ACCEPTED`; `2B.1–2B.2` Implemented)
> - [Phase 2B.2 Borrow Checker, NLL & Provenance](phase-2b.2-borrow-checker-nll-provenance.md) (`IMPLEMENTED`)
> - [Ownership and Memory Overview](../language-spec/ownership-and-memory.md) (`SPECIFIED`)
> - [Syntax and Formal Grammar](../language-spec/syntax-and-grammar.md) (`SPECIFIED` + `2B.1` Extensions Implemented)
> - [ADR-003: Ownership-Lite Memory Model](../decisions/ADR-003-ownership-lite-memory-model.md) (`ACCEPTED`)
> - [ADR-008: Structured Concurrency and Zero-GIL](../decisions/ADR-008-structured-concurrency-and-zero-gil.md) (`ACCEPTED`)
> - [ADR-009: Typed Intermediate Representation Architecture](../decisions/ADR-009-typed-intermediate-representation-architecture.md) (`ACCEPTED`)
> - [ADR-011: Ownership and Borrow-Checking Semantics](../decisions/ADR-011-ownership-and-borrow-checking-semantics.md) (`ACCEPTED` — Nov 25, 2026)

---

## 1. Compiler Audit & Status Boundary

> [!NOTE]
> **Implementation Status Reconciliation (Post-Phase 2B.2.1)**: Following owner approval ([OWNER-APPROVAL-ADR-011-2026-11-25.md](../decisions/OWNER-APPROVAL-ADR-011-2026-11-25.md)), Phases **2B.1–2B.1.2** (Ownership CFG, `@derive(Copy)`, uninitialized `let`/`var`, move/init & match-pattern closure) and Phases **2B.2–2B.2.1** (Borrow Checker, NLL Regions, Reborrow & Reference Mutability Closure, Canonical `self` Method Indexing, Per-Field Aggregate & Candidate Provenance `WON-MEM-002..006`) are **`IMPLEMENTED`**. **Phase 2B.3** (Drop Elaboration) and trait/concurrency milestones remain `SPECIFIED`/`PLANNED`.

### 1.1 Historical Baseline Context & Currently `IMPLEMENTED` Capabilities
1. **Lexer & Parser (`internal/token`, `internal/lexer`, `internal/parser`)**:
   - Supports canonical 4-space indentation blocks (`INDENT`, `DEDENT`, `NEWLINE`), `let`, `var`, `@derive(Copy)` (`token.AT` added in `2B.1`), optional uninitialized simple `let x: T` / `var x: T` (`2B.1`), `fn`, `struct`, `enum`, `impl`, `trait`, `match`, `?`, `unsafe:`, `scope`, `spawn`, `ref`/`mut` pattern modifiers, `&x` / `&mut x`, and `*p`.
   - `parsePostfix` (`parser.go:1715`) parses `.ident` field/method access, not numeric tuple indexing `t.0` (tuple destructuring `let (a, b) = t` is supported).
2. **Canonical Type System & Semantic Checker (`internal/types`, `internal/symbols`, `internal/checker`)**:
   - `Checker.CheckProgram` constructs `TypedProgram` containing `AST`, `Types (*types.Context)`, `Symbols (*symbols.Table)`, `ExprTypes (map[ast.Expr]types.TypeID)`, `CallTargets`, `FieldRefs`, `VariantRefs`, and `CodegenSupported` (`WON-GEN-002`).
   - `types.Context.IsCopy(TypeID)` classifies scalars (`i8..i128`, `u8..u128`, `isize`, `usize`, `f32`, `f64`), `bool`, `char`, `unit`, `!`, raw pointers (`*const T`, `*mut T`), shared references (`&T`), function types (`fn`), and arrays/tuples of `Copy` elements as `Copy`. Mutable references (`&mut T`), `String`, `KindStruct`, `KindEnum`, and `KindTypeParam` evaluate to `!IsCopy` (`false`).
   - Built-in prelude (`internal/symbols/table.go`, `internal/checker/expressions.go`): `String` (`String::from(&str) -> String`, `.len() -> u64`, `.is_empty() -> bool`, `.as_ptr()`, `.as_mut_ptr()`, `.as_bytes()`, `.push_str(&str)`), `Option[T]`, `Result[T, E]`, `println`, `print`, `bytes_equal`, `panic`, `assert`. `Vec[T]`, `Shared[T]`, `Arc[T]`, `Mutex[T]`, and `RwLock[T]` do **not** exist in the prelude yet.
   - Shallow mutability (`WON-MUT-001`) rejects assignment, `&mut x`, `as_mut_ptr()`, and `&mut self` calls when the base binding lacks `var` or `&mut` provenance (`isLValueRootMutable`), providing a machine-applicable `let` -> `var` edit via `Symbol.LetTokenSpan`.
   - Unsafe boundary enforcement rejects `#import` FFI calls outside `unsafe:` (`WON-SEC-001`) and raw-pointer `*p` dereferencing / unsafe pointer arithmetic outside `unsafe:` (`WON-SEC-002`).
   - Unimplemented features (`trait`, `impl Trait for Type`, `scope`, `spawn`, `mod`) are rejected explicitly via `WON-SEM-010`.
3. **Backend Boundary (`internal/checker/backend_caps.go`, `internal/codegen`)**:
   - Only the scalar/array/C-FFI vertical slice compiles through `verifyBackendCapabilities` to ISO C11 (`WON-GEN-002` rejects structs, enums, references, tuples, `String`, `?`, and closures at `wonc build`).

### 1.2 Deferred Subsystems (`SPECIFIED` / `PLANNED` / In Progress)
- Ownership CFG (`internal/cfg`) and definite-initialization / move / partial-move / reinitialization validation (`internal/borrow`, `WON-MEM-001/008/010/011`, `WON-INT-001`) are **`IMPLEMENTED`** (Phases `2B.1–2B.1.2`).
- XOR aliasing / borrow checker (`WON-MEM-004`/`005`/`006`), NLL region solver, reborrow mutability enforcement, canonical `self` indexing, and `Prov(e)` per-field / candidate provenance (`WON-MEM-002`/`003`) are **`IMPLEMENTED`** (Phases **2B.2–2B.2.1**).
- RAII destructor runtime, drop elaboration, drop flags, and `WON-MEM-007` enforcement remain **`PLANNED`** (Phase **2B.3** / **2C**).
- General `trait` coherence, conditional generic `@derive(Copy)`, `impl Drop`, and `Send`/`Sync` auto-traits remain **`PLANNED`** in separate post-2B milestones.

### 1.3 Status Classification Map

| Layer | Current Status | Notes |
| :--- | :--- | :--- |
| Canonical AST (`BorrowExpr`, `AssignStmt`, `MatchExpr`, `IdentPattern`, `UnsafeExpr`, `ScopeExpr`, `SpawnExpr`) | `IMPLEMENTED` | Syntax parsed and spans retained. |
| Attribute (`@derive(Copy)`) & Uninit `let x: T` Grammar | `IMPLEMENTED` (`2B.1`) | Tokenized, parsed, and checked in `wonc`. |
| Shallow `WON-MUT-001` & `WON-SEC-001`/`002` | `IMPLEMENTED` | Enforced in AST-walk type checker. |
| Ownership-Lite Architecture (`ADR-011`, `ownership-and-borrowing.md`) | `ACCEPTED` | Approved Nov 25, 2026 (`OWNER-APPROVAL-ADR-011-2026-11-25.md`). |
| Ownership CFG & Move/Init Analysis (`internal/cfg`, `internal/borrow` — `2B.1–2B.1.2`) | `IMPLEMENTED` | Verified in CI (`91a6932`); fail-closed `WON-INT-001` & `OwnershipChecked` gate. |
| Borrow Checker, NLL & Candidate Provenance (`2B.2`) | `IMPLEMENTED` | `loans.go`, `liveness.go`, `provenance.go` (`WON-MEM-002..006`). |
| Drop Elaboration & Drop Flags (`2B.3`) | `SPECIFIED` / `PLANNED` | Deferred to Phase 2B.3 / Phase 2C. |
| General Traits (`trait`, `impl Drop`, generic `Copy`), `Send`/`Sync`, `scope`/`spawn` | `PLANNED` (Separate Future Milestone) | Rejected with `WON-SEM-010` until post-2B trait & concurrency milestones. |
| ADR-009 TIR Lowering & Multi-Type C11 Codegen (`Drop` Emission) | `SPECIFIED` (Phase 2C) | Ownership CFG designed to lower 1:1 to TIR. |

---

## 2. Core Design Goals & Non-Goals

### 2.1 Goals
1. **Zero Named Lifetime Syntax in Ordinary WON**: Preserve ADR-003 ergonomics with **no** lifetime parameters or lifetime annotations (`&T` and `&mut T` only) in user-facing syntax.
2. **Sound Aliasing & Temporal Safety**: Guarantee single-owner semantics, no use-after-move, no read-before-init, no double-free, no dangling references, and XOR aliasing (`&T` XOR `&mut T`) on safe paths.
3. **Flow-Sensitive NLL Regions + Deterministic Lexical Drops**: End borrows at their last live CFG use (preventing false lexical deadlocks) while keeping value cleanup strictly at lexical scope exit in reverse declaration order.
4. **Structural Disjoint Borrow Splitting**: Permit simultaneous `&mut s.a` and `&mut s.b` on distinct struct fields (and future distinct tuple slots).
5. **Deterministic Method Borrowing & Sound Multi-Origin Provenance**: Support common selection patterns safely via conservative candidate-origin provenance sets (`Prov(e)`), while keeping method-call argument/receiver borrowing simple and predictable without two-phase borrowing.
6. **Direct Bridge to ADR-009 TIR & AI Diagnostics**: Feed Phase 2C TIR cleanly and emit unambiguous `WON-MEM-*` structured diagnostics (`diagnostic.Diagnostic`).

### 2.2 Explicit Non-Goals (Out of Scope for Phase 2B)
1. Explicit lifetime parameters on functions or user structs (excluded by ADR-003).
2. Safe self-referential structs (`Shared[T]`, `Arc[T]`, arenas, or integer indices are used instead).
3. Symbolic array index disjointness (`&mut arr[i]` and `&mut arr[j]` conservatively conflict).
4. General trait solver, user `impl Drop`, generic `@derive(Copy)` constraints, closures, and multi-threaded `scope`/`spawn` enforcement (isolated to separate future milestones).
5. Two-phase method receiver borrowing (`v.push(v.len())` stays rejected in Phase 2B; documented as future extension).

---

## 3. Architectural Position in the Compiler Pipeline

Phase 2B inserts cleanly between `Checker.CheckProgram` (`TypedProgram`) and Phase 2C TIR lowering:

```text
  Source (.won)
       │
       ▼
  [1. Lexer & Off-Side Parser]         --> *ast.Program
       │
       ▼
  [2. Type Checker (`internal/checker`)] --> *TypedProgram (Types, Symbols, ExprTypes, CallTargets, FieldRefs, VariantRefs)
       │
       ▼
  [3. Ownership CFG Builder (`2B.1`)]    --> *cfg.ProgramCFG (BasicBlocks, Places, Rvalues, StorageLive/Dead)
       │
       ├──► [4. Init & Move Dataflow (`2B.1`)]   (Definite Init, Move/Reinit, Partial Move, Loops -> WON-MEM-001/007/008/010)
       ├──► [5. Borrow, NLL & Prov (`2B.2`)]     (Regions, Loans, XOR Conflicts, Candidate Prov -> WON-MEM-002..006)
       └──► [6. Drop Elaboration (`2B.3`)]       (Lexical Cleanup, Early-Return/Uninit Drop Flags, FFI Hooks)
       │
       ▼
  [7. Backend Gate / Future TIR (`2C`)]  --> WON-GEN-002 C11 Subset Gate (now) / ADR-009 TIR Lowering (Phase 2C)
```

---

## 4. Ownership-Lite Domain Model & Core Abstractions

### 4.1 Places (`Place` & `Projection`)
```go
type LocalID uint32 // 0 = _0 (return slot), 1..k = params, k+1.. = locals & temps

type Place struct {
    Local       LocalID
    Projections []Projection
}

type ProjectionKind uint8
const (
    ProjField    ProjectionKind = iota // .field_name (struct field or tuple slot index)
    ProjDeref                          // *place (deref of &T, &mut T, *const T, *mut T)
    ProjDowncast                       // enum variant payload projection after tag match
    ProjIndex                          // [local_idx] (dynamic array/slice index -> coarse parent alias)
    ProjConstIdx                       // [k] fixed array index
)

type Projection struct {
    Kind       ProjectionKind
    FieldIndex int
    VariantIdx int
    ConstIndex int64
    IndexLocal LocalID
    ElemType   types.TypeID
}
```

### 4.2 Place Path Conflict Algebra
Given two places $p_1 = (L_1, \pi_1)$ and $p_2 = (L_2, \pi_2)$:
- If $L_1 \neq L_2$: **Disjoint** (except when stepping through `ProjDeref` whose underlying reference provenances may alias — handled via active loans on the dereferenced provenance!).
- Along matching local root $L_1 = L_2$, compare projections left-to-right up to $\min(|\pi_1|, |\pi_2|)$:
  1. Distinct `ProjField` indices on same struct/tuple -> **Disjoint** (`&mut s.a` and `&mut s.b` coexist).
  2. Distinct `ProjDowncast` variant indices on same enum -> **Disjoint**.
  3. Any `ProjIndex` vs any `ProjIndex`/`ProjConstIdx` on the same array/slice -> **May Alias / Conflicting** (conservative).
  4. If $\pi_1$ is a prefix of $\pi_2$ (or vice versa), or equal -> **Conflicting**.

### 4.3 User `Copy` Semantics & `@derive(Copy)` Policy (Design Closure)
1. **Builtin `Copy` Types**:
   - Scalars (`i8..i128`, `u8..u128`, `isize`, `usize`, `f32`, `f64`), `bool`, `char`, `unit`, `!`.
   - Shared references (`&T`), function types (`fn(...) -> R`), raw pointers (`*const T`, `*mut T`).
   - Arrays `[T; N]` and tuples `(T1, ..., Tn)` if and only if all elements satisfy `IsCopy`.
2. **Builtin Non-`Copy` Types**:
   - Mutable references (`&mut T`), `String`, future `Vec[T]`, `Shared[T]`, `Arc[T]`, synchronization/file handles, and any type with custom or transitive cleanup.
3. **User Composites (`struct` and `enum`) — Proposed `@derive(Copy)` Policy**:
   - **Move-by-default**: User-defined `struct` and `enum` definitions always default to non-`Copy` (`IsCopy == false`).
   - **Single Exact Spelling**: Opt-in uses `@derive(Copy)` immediately preceding `[pub] struct Name:` or `[pub] enum Name:`. Alternative spellings (such as `@copy`) are **forbidden** (`WON-SYN-*` / syntax error once attributes are added).
   - **Deterministic Eligibility Rule (Phase 2B Intrinsic, No Full Trait Solver)**:
     - Checked **before** CFG move classification using a dedicated structural check over the type's fields / enum variant payloads:
     - Every field (for `struct`) and every variant payload (for `enum`) MUST satisfy `IsCopy == true`.
     - The type MUST NOT define or acquire a custom `Drop` destructor (and no field may carry cleanup semantics).
     - Violation emits **`WON-MEM-011`**.
   - **Generic Composites**: `@derive(Copy)` on a `struct` or `enum` containing an unconstrained generic `TypeParam` (e.g., `struct Boxed[T]: val: T`) is **deferred / rejected (`WON-SEM-010`)** in Phase 2B rather than inventing ad-hoc conditional constraints. Full conditional `Copy` derivation (`T: Copy`) belongs to the future Trait Solver milestone.
   - **Grammar Impact**: Resolved in **Phase 2B.1** (`token.AT`, lexer/parser attribute support for `@derive(Copy)` implemented).

### 4.4 Compiler-Internal Region & Provenance Model (`Static` Without Lifetime Syntax)
WON enforces a strict **three-layer separation** so static provenance never leaks into user syntax:

| Layer | Representation | What Users / Compilers See |
| :--- | :--- | :--- |
| **1. Source-Level Reference Type** | `&T` and `&mut T` only | Ordinary WON signatures (`fn get(s: &String) -> &str`). Zero apostrophe lifetime tokens (`'a` or `'static`) exist in source syntax. |
| **2. Compiler-Internal Provenance Category** | `ProvKind`: `Local(Place)`, `Param(ParamIdx)`, `Static` | Represented in `internal/borrow` data structures as tagged Go structs (`ProvOrigin`), never rendered as Rust lifetime syntax in user code. |
| **3. Proven Valid Static Sources** | String literals (`&str` backed by `.rodata`) | Today **only string literals** have `Static` provenance. Module-level `static` variable declarations do **not** exist in WON's current grammar/spec and are **not assumed** by Phase 2B. |

Internal borrow regions $\rho_i \in \text{RegionID}$ represent abstract sets of CFG program points (`PointSet`), created for each `RvRef` expression and solved purely inside `internal/borrow`.

---

## 5. Ownership Control-Flow Graph (CFG) IR Specification

Every checked function and inherent method lowers `(AST, TypedProgram)` to a `cfg.FuncCFG`:

```go
type BlockID uint32

type BasicBlock struct {
    ID           BlockID
    Statements   []Statement
    Terminator   Terminator
    Predecessors []BlockID
    Successors   []BlockID
}

type StmtKind uint8
const (
    StmtAssign       StmtKind = iota // Place = Rvalue
    StmtStorageLive                  // Lexical binding enters scope
    StmtStorageDead                  // Lexical binding exits scope (triggers unborrowed check)
    StmtDrop                         // Unconditional destructor call (Phase 2B.3 / 2C)
    StmtCondDrop                     // Drop-flag guarded destructor call (Phase 2B.3 / 2C)
    StmtDeinit                       // Marks Place Moved
    StmtNop
)
```

Key lowering invariants:
- `if` / `elif` / `else`, `while`, `for`, `match` arms (with guard-failure fallthrough before arm move commits), `break`, `continue`, `return`, `?` (`TermErrorPropagate` running exited-scope cleanup edges), and short-circuit `and`/`or` lower explicitly into basic blocks.
- `UnsafeExpr` annotates lowered CFG statements with `InUnsafe = true`. Safe reference move/borrow tracking **continues inside `unsafe:`**, while raw-pointer (`*const T`, `*mut T`) dereference/arithmetic gates strictly on `InUnsafe == true` (`WON-SEC-002`).
- Deferred uninitialized `let x: T` / `var x: T` declarations require the proposed optional `[ "=" , Expr ]` grammar extension before CFG uninit testing runs in `wonc` parses.

---

## 6. Definite Initialization, Moves & Partial Moves (Phase 2B.1)

Forward lattice dataflow over `(bb, stmt_idx)`:

$$\text{InitState}(p) \in \{\text{Uninit}, \text{Moved}, \text{Init}, \text{MaybeInit}\}$$

1. **Read-Before-Init (`WON-MEM-010`)**: Reading, borrowing, or moving an `Uninit` (or `MaybeInit` never-initialized path) local emits `WON-MEM-010`.
2. **Use-After-Move (`WON-MEM-001`)**: `OpMove(p)` transitions `p` (and sub-fields) to `Moved` with `MoveOriginSpan`. Any subsequent read, move, or borrow of `Moved` or `MaybeInit` (e.g., moved on one branch or loop body back-edge without reinit) emits `WON-MEM-001`.
3. **Reinitialization**: Assigning `x = val` on a `var` binding restores `x` to `Init`. Assigning to an already-`Init` non-`Copy` binding emits a drop of the prior value prior to storing `val` (`Phase 2B.3`). Assigning to `let` remains `WON-MUT-001`.
4. **Partial Moves & `Drop` Guard (`WON-MEM-007`)**:
   - Moving a non-`Copy` field `s.a` out of a composite without custom `Drop` marks only `s.a` as `Moved`; `s.b` stays `Init` and usable; whole `s` becomes unreadable/unborrowable (`WON-MEM-001`) until all moved fields are restored.
   - Moving any field out of a type with a custom root `Drop` destructor **MUST fail** with **`WON-MEM-007`**.

---

## 7. Borrow Semantics, Exclusivity & Method Evaluation (Phase 2B.2)

### 7.1 XOR Aliasing Matrix
At any CFG point, a `Loan(Kind, BorrowedPlace, RegionID, OriginSpan)` is **active** if the current CFG point lies inside `RegionID`:

| Active Loan Kind | Attempted Read (`Copy` / `&T`) | Attempted Write / `&mut T` / `Move` | Scope Exit (`StorageDead` / `Drop`) |
| :--- | :--- | :--- | :--- |
| `BorrowShared` (`&T`) | **ALLOWED** | **REJECT `WON-MEM-004` / `WON-MEM-006`** | **REJECT `WON-MEM-003`** |
| `BorrowMut` (`&mut T`) | **REJECT `WON-MEM-004`** | **REJECT `WON-MEM-005` / `WON-MEM-006`** | **REJECT `WON-MEM-003`** |

Moving a non-`Copy` value out of `&T` or `&mut T` deref without replacement is rejected via **`WON-MEM-008`**.

### 7.2 Method Receiver Borrowing & Argument Evaluation Policy (Design Closure)
Phase 2B adopts **strict left-to-right deterministic evaluation** (no two-phase borrowing):

1. Evaluate the receiver place `recv` and immediately acquire the receiver loan (`&recv` for `&self`, `&mut recv` for `&mut self`, or `OpMove`/`OpCopy` for `self`).
2. Evaluate argument expressions `arg0, arg1, ...` strictly left-to-right **while the receiver loan is active**.
3. Invoke the callee with the active loans covering the call duration.

**Exact Consequences**:
- **Nested receiver/arg conflict (`vec.push(vec.len())` or `s.mutate_with(s.read_ref())`)**: `s.mutate_with(...)` acquires `&mut s` in step 1 before evaluating arguments in step 2; step 2 attempts `&s` (`s.read_ref()`) while `&mut s` is live -> rejected deterministically with **`WON-MEM-004`**. Remedy: hoist argument into a binding (`let n = vec.len(); vec.push(n)`).
- **Sequential mutable calls (`s.bump(); s.bump()`)**: **Valid** — first `&mut s` loan expires when call 1's temporary region dies before call 2 begins.
- **Read method followed by mutation (`let n = s.len(); s.push_str("x")`)**: **Valid** — `s.len()` returns `u64` (`Copy`), `&s` expires immediately after `.len()`, so `&mut s` succeeds.
- **Reborrow while parent `&mut` ref is live (`let r = &mut pt.x; bump(r); *r += 1`)**: **Valid** — passing `r` to `bump(&mut i32)` creates a call-scoped reborrow `&mut *r`, freezing `r` only across `bump(r)` and restoring `r` afterward. Passing two conflicting arguments using `r` simultaneously (`pair(r, *r)`) fails left-to-right (`WON-MEM-004`).
- **Two-Phase Borrowing**: Explicitly documented as a `PLANNED` future ergonomics evaluation — **not** part of Phase 2B.

---

## 8. Non-Lexical Regions & Multi-Origin Reference Provenance (`Prov(e)`) (Design Closure)

### 8.1 Non-Lexical Region Liveness (NLL Within Function CFG)
Each inferred reference carries an internal `RegionID`. Backward liveness DFA computes the exact set of CFG points at which any variable/temporary mentioning `RegionID` may still be read. A loan expires as soon as its region is no longer live along outgoing paths — eliminating false lexical-block conflicts without introducing lifetime syntax.

### 8.2 Multi-Origin Return Provenance: Strategy Comparison & Proof of Soundness

Consider returning a reference across multiple input parameters (using **no** named lifetime syntax):
```won
fn choose(condition: bool, a: &String, b: &String) -> &String:
    if condition:
        return a
    return b
```

| Dimension | Strategy A: Strict Single-Origin Rejection | Strategy B: Conservative Candidate-Origin Sets (**Recommended**) |
| :--- | :--- | :--- |
| **Rule** | Reject `choose` at definition (`WON-MEM-002`) whenever $\lvert \text{Prov}(ret) \cap \text{Params} \rvert > 1$. | Compute $\text{Prov}(ret) = S \cup (\{\text{Static}\} \text{ if any})$ over CFG; bind caller returned region $\rho_{ret}$ to **every** candidate arg $i \in S$. |
| **Ergonomics** | Breaks common selection helpers (`choose`, `max_ref`, `fallback_ref`). | Supports single-module selection & propagation naturally with zero `'a` annotations. |
| **Implementation Complexity** | Minimal (`<= 1` origin bit). | Low-Moderate: fixed bitset `CandidateMask uint64` per parameter + intra-unit call summary / finite-lattice SCC iteration. |
| **Role of `WON-MEM-002`** | Rejects all multi-param conditional returns. | Rejects **abstract / bodyless multi-reference boundaries** (e.g., `fn(&T, &T) -> &T` function pointers, future bodyless `trait` methods, unannotated cross-crate boundaries). |

#### Formal Soundness Justification of Strategy B
Strategy B **distinguishes** a *candidate-origin union* $S = \{p_1, p_2\}$ (meaning "dynamically points to one of $p_1$ or $p_2$, so hold **all** $i \in S$ borrowed while the result is used") from claiming either source can be mutated independently:

1. **Shared Return (`-> &R`)**: If $r = \text{choose}(c, \&x, \&y)$ has summary $S = \{1, 2\}$, caller emits subset constraints $\rho_{\text{arg1}} \supseteq \rho_r$ and $\rho_{\text{arg2}} \supseteq \rho_r$. While $r$ is live, **both** $x$ and $y$ remain immutably borrowed (`BorrowShared`) and cannot be moved, mutated (`WON-MEM-006`), or dropped (`WON-MEM-003`). Whatever branch executed at runtime, the dynamic referent of $r$ is guaranteed alive and immutable.
2. **Mutable Return (`-> &mut R`)**: A function body may return a mutable reference derived from param $i$ only if param $i$ itself has `&mut` provenance (`WON-MUT-001` forbids returning `&mut` derived from `&`). Therefore every $i \in S$ has `&mut` type in the callee signature. At any call site, XOR exclusivity (`WON-MEM-005`) already proves all `&mut` arguments in $S$ refer to **disjoint** exclusive places. Extending $\rho_{\text{arg}_i} \supseteq \rho_r$ for every $i \in S$ locks **all** candidate `&mut` arguments exclusively while $r$ is live — guaranteeing zero aliasing and zero dangling.
3. **Branches**: Conditional merges form $\text{Prov}_{\text{out}} = \text{Prov}_1 \cup \text{Prov}_2$, strictly over-approximating dynamic execution paths.
4. **Local Escape Invariant (`WON-MEM-003`)**: Whenever $\text{Local}(v) \in \text{Prov}(ret)$ on any reachable return path, compile fails with **`WON-MEM-003`**. Strategy B never permits returning stack locals.
5. **Reborrowed References**: Callee reborrows `&*p` / `&mut *p` inherit $\text{Prov}(p)$ cleanly (only `&local_slot` creates `Local`).
6. **Nested Calls & Propagation**: Given `outer(flag, p, q)` calling `choose(flag, p, q)`, substituting caller argument provenances computes $\bigcup_{k \in S_{\text{choose}}} \text{Prov}(\text{arg}_k)$, staying exact across arbitrary call nesting.
7. **Parameter Aliasing at Call Site**: Passing two shared refs to the same owner (`choose(c, &x, &x)`) creates two `BorrowShared` loans on `x`, both extended to $\rho_r$ — sound (`&T` + `&T` allowed; writes blocked). Passing aliased `&mut` fails `WON-MEM-005`.
8. **Call-Site Locals**: Assigning `let r = choose(...)` propagates $\rho_r$ constraints through `r`'s NLL liveness and drop-order checks (`WON-MEM-003`).
9. **Recursive Calls**: Because $\mathcal{P}(\{0..k-1\} \cup \{\text{Static}\})$ is a finite powerset lattice of height $k + 1$, monotonic summary iteration over intra-unit call-graph SCCs terminates deterministically in at most $k+1$ iterations per cycle!
10. **Opaque Boundaries (`WON-MEM-002`)**: Where a callee has **no analyzable body** (e.g., `fn(&T, &T) -> &T` function pointers or future bodyless `trait` method declarations) and returns a reference with $\ge 2$ input references, candidate provenance cannot be proven statically without violating cross-boundary modularity, so **`WON-MEM-002`** rejects cleanly (and users return owned `T`, `Shared[T]`, or `&mut out`).

---

## 9. Scope Exits, Drop Semantics & Destructor Boundary (Phase 2B.3 & Future Trait Milestone)

1. **Current Implementation (`Phase 2A.2`)**: **No** runtime destructor (`Drop`) or `String` freeing runs in `wonc` today (`String` triggers `WON-GEN-002`; `impl Drop` triggers `WON-SEM-010`).
2. **Compiler-Managed Cleanup (`Phase 2B.3` CFG/Drop Elaboration + `Phase 2C` C11/TIR)**:
   - Built-in resource types (`String`, and future `Vec[T]`, `Shared[T]`, `Arc[T]`, handles) and user structs/enums transitively containing them receive compiler-managed structural cleanup.
   - Custom user `impl Drop for Type:` syntax/trait resolution is **designed (`SPECIFIED`)** and deferred to the **Separate Trait Milestone** (`WON-SEM-010` until then).
3. **Destruction Ordering & Control Flow**:
   - Within every lexical block (`BlockStmt`), initialized non-`Copy` cleanup-bearing bindings drop in **reverse declaration order** (LIFO).
   - Normal exit, `return`, `break`, `continue`, and `?` early-return edges execute drops for every exited lexical scope.
   - If a value was moved away or uninitialized on all incoming edges, **no drop** runs.
   - If moved along some paths (`MaybeInit`), Phase 2B.3 synthesizes a boolean stack **Drop Flag** (`_drop_flag_x: bool`) and lowers `StmtCondDrop(x, _drop_flag_x)` into ADR-009 TIR `Drop`.
   - **Partial-Move Composite Cleanup**: Moving field `s.a` out of a non-custom-`Drop` struct `s` containing `String` fields causes Drop Elaboration to drop only still-initialized field `s.b`, never touching moved `s.a`. Custom `impl Drop for T` blocks partial moves entirely (`WON-MEM-007`).
4. **Destructor Panic Rule**: Destructors MUST NOT unwind past an already-active panic; double-panic aborts deterministically (`ownership-and-memory.md` §6.4).

---

## 10. Trait-System Separation & Deferred Future Concurrency

To prevent Phase 2B from silently turning into a trait solver + runtime concurrency project:

| Concern | Phase 2B Scope (Minimal Intrinsic) | Future Separate Milestone |
| :--- | :--- | :--- |
| `Copy` | Concrete `@derive(Copy)` field/variant check + builtin `IsCopy` | Generic `T: Copy` conditional derives & `trait Copy` resolution |
| `Drop` | Structural cleanup for `String`/containers + `WON-MEM-007` contract | User `impl Drop for Type` trait method dispatch & coherence |
| Error Coercion (`?`) | Exact error-type compatibility (`WON-ERR-001`) | `From`/`Into` trait coercion (`WON-ERR-002`) |
| Concurrency | CFG hook `TermScopeJoin` reserved | `Send`/`Sync` auto-traits + `scope s:` / `s.spawn:` runtime (`WON-CON-001`) |

Raw pointers (`*const T`, `*mut T`) remain `Copy`, cannot coerce to `&T`/`&mut T`, require `unsafe:` on deref (`WON-SEC-002`) and unchecked FFI (`WON-SEC-001`), and future `.wonbind` `@owned`/`@borrow` contracts integrate at Phase 2B.3 FFI call edges without guessing safety.

---

## 11. Canonical Ownership & Safety Diagnostic Matrix

All Phase 2B diagnostics map cleanly to `diagnostic.Diagnostic` (`Version: 1`). Existing `WON-MUT-001`, `WON-SEC-001`, `WON-SEC-002`, `WON-SEM-010`, and `WON-GEN-002` keep their exact implemented meanings. **No machine auto-fix `Edits` are invented where programmer intent is ambiguous** (e.g., borrowing vs cloning vs changing return types); those diagnostics supply rich `Explanation` provenance lines (`line:col` of prior move/loan). *(Optional non-breaking `Related []RelatedSpan` extension to `diagnostic.Diagnostic` may be added in Phase 2B.1 alongside `Explanation`.)*

| Code | Status | Short Stable Message | Exact Violated Rule | Primary Span | Secondary Span / Label (in `Explanation` / optional `Related`) | Safe Repair Suggestion |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **`WON-MEM-001`** | `IMPLEMENTED` (2B.1) | `Use of moved value` | Read, move, or borrow of a `Moved` / partially moved / conditionally moved place. | Use / move site | Prior `MoveOriginSpan` ("value moved here"). | Advise borrowing (`&x`), `.clone()` (when available), or reinitializing `var` binding (no blind auto-edit). |
| **`WON-MEM-002`** | `IMPLEMENTED` (2B.2) | `Ambiguous multi-reference return provenance` | Reference-returning abstract/bodyless signature (`fn` pointer or bodyless trait) with `>= 2` input refs lacks uniquely provable parameter origin. | Function/method signature return type or call | Conflicting candidate reference parameters listed. | Return owned `T`, `Shared[T]`, or accept explicit `&mut Out` parameter. |
| **`WON-MEM-003`** | `IMPLEMENTED` (2B.2) | `Borrowed value does not live long enough / local reference escape` | Return provenance contains `Local(v)` OR owner drops/`StorageDead` occurs while loan region remains live. | Escaping `return &local` or `let r = &x` outliving drop | Owner declaration / drop scope exit span. | Return owned value or restructure owner scope to outlive last reference use. |
| **`WON-MEM-004`** | `IMPLEMENTED` (2B.2) | `Borrow exclusivity conflict (&T vs &mut T)` | Taking `&mut p` while `&p` is live, OR reading / `&p` while `&mut p` is live (incl. `v.push(v.len())`). | Conflicting second access/borrow | Active prior loan `OriginSpan` & blocking last use. | Reorder last read before `&mut` borrow, or hoist sub-expression into temporary. |
| **`WON-MEM-005`** | `IMPLEMENTED` (2B.2) | `Multiple active mutable borrows` | Creating second overlapping `&mut p` while another `&mut p` loan is live. | Second `&mut` expression/call | First active `&mut` loan `OriginSpan`. | Split disjoint struct fields (`&mut s.a`, `&mut s.b`) or finish first borrow's uses first. |
| **`WON-MEM-006`** | `IMPLEMENTED` (2B.2) | `Cannot mutate or move borrowed value` | Assigning, modifying, or moving owner place `p` while `&p` or `&mut p` is live. | Mutation or move site | Active loan `OriginSpan` blocking write. | Complete all uses of the borrow before mutating/moving owner. |
| **`WON-MEM-007`** | `SPECIFIED` (2B.3 / Future Drop) | `Cannot partially move out of Drop type` | Moving field/payload out of type defining root `Drop` destructor. | Field move expression | `Drop` impl definition span on parent type. | Borrow `&s.field` instead, or move whole struct. |
| **`WON-MEM-008`** | `IMPLEMENTED` (2B.1) | `Cannot move out of borrowed reference` | Moving non-`Copy` value through `*r` or `r.field` when `r` is `&T` or `&mut T`. | Deref/field move expression | Parameter/borrow binding declaration of `r`. | Borrow (`&*r`), clone explicitly, or take parameter by value if ownership transfer is intended. |
| **`WON-MEM-009`** | `SPECIFIED` (Optional Lint) | `Unnecessary var binding` | `var x` never reassigned, field-mutated, or mutably borrowed. | `var` binding name | `var` token span (`LetTokenSpan`). | Syntactic replacement edit `var` -> `let` (safe, unambiguous lint). |
| **`WON-MEM-010`** | `IMPLEMENTED` (2B.1) | `Use of possibly uninitialized binding` | Read, borrow, or move of binding whose `InitState` is `Uninit` / `MaybeInit` never-initialized. | Use site | Declaration span (`let x: T` once optional uninit syntax exists). | Ensure every control-flow path initializes variable before use. |
| **`WON-MEM-011`** | `IMPLEMENTED` (2B.1) | `Invalid @derive(Copy) attribute` | `@derive(Copy)` placed on type with non-`Copy` field/variant, custom `Drop`, or disallowed item kind. | `@derive(Copy)` attribute span | Offending non-`Copy` field/variant or `Drop` span. | Remove `@derive(Copy)` or replace non-`Copy` fields with `Copy` types (no blind removal if semantic intent unclear). |
| **`WON-MUT-001`** | `IMPLEMENTED` | `Cannot mutate immutable binding` | Assignment, `&mut`, `as_mut_ptr`, or `&mut self` on `let` / `&T`. | Mutation / `&mut` site | Binding `let` token span (`LetTokenSpan`). | Deterministic AI edit `let` -> `var` when root is local `let` (no edit on `&T` deref). |
| **`WON-SEC-001` / `002`** | `IMPLEMENTED` | `Unsafe boundary required` | Direct `#import` FFI call (`001`) or raw pointer `*p` deref (`002`) outside `unsafe:`. | Call / `*p` site | — | Wrap required minimal operation in explicit `unsafe:` block after auditing safety invariants. |
| **`WON-CON-001`** | `SPECIFIED` (Phase 3) | `Mutable reference cannot cross task boundary` | Capturing `&mut` across `s.spawn:` nursery boundary. | Capture / `s.spawn:` expression | `scope s:` span. | Share synchronized `Mutex[T]` / `Atomic*` or pass owned `Send` channel message. |

---

## 12. Refined Phase 2B Implementation Sequence & Acceptance Gates

*(Recommended sequence awaiting Owner Approval of ADR-011; zero implementation occurs in Phase 2B.0.1.)*

1. **Phase 2B.1 — Ownership CFG, `@derive(Copy)` & Move/Init Analysis**:
   - Extend lexer/parser for `@derive(Copy)` (`AT` token) and optional `[ "=" Expr ]` on `let`/`var` if approved; add `internal/cfg` (`Place`, `Projection`, `BasicBlock`, CFG lowering over `TypedProgram`).
   - Implement `internal/borrow` Move/Definite-Init forward DFA (`WON-MEM-001`, `WON-MEM-008`, `WON-MEM-010`, `WON-MEM-011`).
   - **Exit Gate**: CFG snapshot tests + move/reinit/partial-move/loop fixed-point tests + `WON-MEM-001/008/010/011` diagnostics green under CI.
2. **Phase 2B.2 — Borrow Checking, NLL Regions & Candidate Provenance**:
   - Implement `liveness.go`, `loans.go` (XOR exclusivity + field splitting + deterministic left-to-right method calls), and `provenance.go` (Strategy B candidate-origin sets + SCC recursion fixed point + `WON-MEM-002..006`).
   - **Exit Gate**: All positive/negative borrow & provenance acceptance tests + AI JSON protocol tests + Go fuzz stability (`go test -fuzz`) green under CI.
3. **Phase 2B.3 — Drop Elaboration & Resource Lifecycle**:
   - Implement reverse-declaration lexical drops, early-return / `?` cleanup edges, stack drop flags for `MaybeInit`, partial-move field cleanup (`WON-MEM-007` readiness), and `.wonbind` `@owned`/`@borrow` hooks.
   - Connects directly into **Phase 2C** ADR-009 TIR & expanded C11 emission (verified with ASan/UBSan).
4. **Separate Future Milestone — General Traits & Structured Concurrency Safety**:
   - User `trait` coherence, generic `@derive(Copy)` bounds, user `impl Drop`, `?` `From`/`Into` (`WON-ERR-002`), `Send`/`Sync` auto-traits, and `scope`/`s.spawn` nursery checking (`WON-CON-001`).

---

## 13. Open Design Decisions Awaiting Owner Ratification (ADR-011)

1. Confirm **explicit `@derive(Copy)`** on `struct`/`enum` (concrete fields only in 2B; generics deferred).
2. Confirm **Strategy B (Conservative Candidate-Origin Sets)** over Strategy A for intra-unit multi-input reference returns (retaining `WON-MEM-002` for abstract/bodyless multi-ref signatures).
3. Confirm **strict left-to-right method receiver borrowing** in Phase 2B (rejecting `vec.push(vec.len())` with `WON-MEM-004`; deferring two-phase borrowing).
4. Confirm **flow-sensitive NLL borrow regions** paired with **strictly lexical reverse-declaration value drops**.
5. Confirm **optional uninitialized declaration grammar** (`let x: T`) and **`@derive(Copy)` attribute syntax** extensions for Phase 2B.1 parser work.
6. Confirm **phased execution breakdown (`2B.1 -> 2B.2 -> 2B.3`)** and **separation of Trait Solver / `Drop` / `Send`+`Sync`** into a distinct post-2B milestone.

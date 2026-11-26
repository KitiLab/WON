# WON Language Specification: Ownership and Borrowing (Normative Proposal)

> **Specification Version**: Draft 0.2.1 (Phase 2B.0.1 Design Closure; ADR-011 Approved)  
> **Status**: `ACCEPTED` (Architectural Direction **ACCEPTED** via [ADR-011](../decisions/ADR-011-ownership-and-borrow-checking-semantics.md) & [Owner Approval](../decisions/OWNER-APPROVAL-ADR-011-2026-11-25.md))  
> **Compiler Implementation Status**: Phase 2B.1–2B.2 (`Ownership CFG`, `@derive(Copy)`, Uninitialized `let`/`var`, Move/Init, Borrow Exclusivity, NLL Regions, Reborrowing & Candidate-Origin Provenance `WON-MEM-001`..`006`, `008`, `010`, `011`, `WON-INT-001`) `IMPLEMENTED`; Phase 2B.3 (`Drop`) Remains `SPECIFIED` / Unimplemented  
> **Architecture & Closure Details**: [phase-2b.0-ownership-architecture.md](../compiler/phase-2b.0-ownership-architecture.md), [phase-2b.1.1-cfg-soundness-closure.md](../compiler/phase-2b.1.1-cfg-soundness-closure.md), [phase-2b.1.2-match-pattern-ownership-closure.md](../compiler/phase-2b.1.2-match-pattern-ownership-closure.md), [phase-2b.2-borrow-checker-nll-provenance.md](../compiler/phase-2b.2-borrow-checker-nll-provenance.md)
> **Governing Architecture**: [Phase 2B.0 / 2B.0.1 Ownership Architecture](../compiler/phase-2b.0-ownership-architecture.md)  
> **Related Core Spec**: [Ownership and Memory Overview](ownership-and-memory.md), [Syntax & Grammar](syntax-and-grammar.md), [ADR-003](../decisions/ADR-003-ownership-lite-memory-model.md)

---

## 1. Purpose & Normative Authority

This document specifies the formal language-level ownership, value-copy, initialization, move, mutability, borrowing, reference-provenance, destruction, and boundary rules for **WON Ownership-Lite**.

Normative keywords **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHALL NOT**, **SHOULD**, and **MAY** follow [RFC 2119](https://tools.ietf.org/html/rfc2119). Every static error rule binds to a canonical diagnostic code (`WON-MEM-*`, `WON-MUT-*`, `WON-SEC-*`).

> [!IMPORTANT]
> **Implementation Boundary**: ADR-011 is `ACCEPTED`. Phase 2B.1–2B.2 (`WON-MEM-001`–`006`, `008`, `010`, `011`, `@derive(Copy)`, uninitialized `let`/`var`, NLL borrow checking & candidate provenance, `WON-INT-001`) are `IMPLEMENTED` in `wonc`. Phase 2B.3 (`Drop`, `WON-MEM-007`) and general trait/concurrency rules remain `SPECIFIED`/`PLANNED`.

---

## 2. Single Ownership & Value Semantics (`Copy` vs `Move`)

1. **Single Owner Invariant**: Every runtime value in safe WON has a single owning place (local binding, struct field, enum payload, or array/tuple element) at any point in execution.
2. **Builtin `Copy` Types (`IsCopy == true`)**:
   - The following types **SHALL** implement `Copy`:
     - All scalar integer (`i8..i128`, `u8..u128`, `isize`, `usize`), float (`f32`, `f64`), `bool`, `char`, `unit`, and `!` types.
     - Immutable shared references (`&T` for any `T`).
     - Raw pointers (`*const T`, `*mut T`) and function types (`fn(...) -> R`).
     - Fixed-size arrays `[T; N]` and tuples `(T1, ..., Tn)` if and only if every element type implements `Copy`.
   - Copying duplicates the representation bitwise without invalidating the source place.
3. **Non-`Copy` Types (`IsCopy == false`)**:
   - Mutable references (`&mut T`), owned heap types (`String`, future `Vec[T]`), `Shared[T]`, `Arc[T]`, synchronization/file handles, and any type defining or transitively containing a destructor **MUST NOT** be `Copy`.
   - User-defined `struct` and `enum` types default to **Move-by-default (`IsCopy == false`)**.
4. **Explicit `@derive(Copy)` for Structs & Enums (Proposed Grammar Extension)**:
   - A `struct` or `enum` declaration **MAY** opt into `Copy` solely using the exact attribute syntax `@derive(Copy)` placed immediately above the declaration (`@copy` and alternative spellings **MUST NOT** be accepted).
   - **Eligibility Requirements**: `@derive(Copy)` **SHALL** succeed if and only if:
     1. Every field (for `struct`) or every variant payload (for `enum`) satisfies `IsCopy == true`.
     2. The type neither implements `Drop` nor acquires a destructor.
     3. *(Phase 2B Restriction)* All fields/variants are concrete (unconstrained generic type parameters `T` in `@derive(Copy)` are **rejected / deferred (`WON-SEM-010`)** until the general Trait Solver milestone introduces conditional bounds).
   - **Violation**: Placing `@derive(Copy)` on a type with a non-`Copy` field/variant or a `Drop` impl **MUST** fail with **`WON-MEM-011`**.
5. **Move Semantics (`WON-MEM-001`)**:
   - Assigning, passing by value, or returning a non-`Copy` place transfers ownership to the destination and marks the source place `Moved`.
   - Any subsequent read, move, or borrow of a `Moved` or conditionally moved (`MaybeInit`) place **MUST** fail with **`WON-MEM-001`**.

---

## 3. Definite Initialization & Reinitialization

1. **Read-Before-Init (`WON-MEM-010`)**: Every binding **MUST** be definitely initialized along all control-flow paths reaching a read, borrow, or move of that binding (`WON-MEM-010`).
   - *(Grammar Note)* Deferred declaration without `=` (`let x: T` / `var x: T`) requires the proposed optional initializer extension in [syntax-and-grammar.md](syntax-and-grammar.md); current `wonc` parser requires `=` at declaration (`WON-SYN-009`).
2. **Branch-Convergent Initialization**: Assigning an uninitialized binding along *every* non-diverging incoming branch of an `if`/`elif`/`else` or exhaustive `match` transitions the binding to `Init` at the merge block.
3. **Reinitialization After Move**: Assigning a new value (`x = new_val`) to a moved `var` binding `x` restores `x` to `Init`. Reassigning an already-`Init` non-`Copy` `var` binding destroys the previous value before storing `new_val`. Assigning to `let` remains **`WON-MUT-001`** (`IMPLEMENTED`).

---

## 4. Partial Moves & Structural Integrity

1. **Non-`Drop` Field-Level Partial Moves**: Moving a non-`Copy` field `s.field` out of a composite `struct` (without custom root `Drop`) marks only `s.field` as `Moved`. Disjoint sibling fields remain `Init` and usable.
2. **Whole-Composite Lockout (`WON-MEM-001`)**: While any field of `s` is `Moved`, reading, moving, or borrowing whole `s` **MUST** fail with **`WON-MEM-001`**. Reinitializing every moved field restores whole `s` to `Init`.
3. **Custom `Drop` Prohibition (`WON-MEM-007`)**: Moving any field out of a `struct` defining a custom root `Drop` destructor **MUST** fail with **`WON-MEM-007`**.

---

## 5. Mutability Rules (`let`, `var`, `&mut`)

1. **`let` vs `var` (`WON-MUT-001` — `IMPLEMENTED`)**:
   - `let` prohibits reassignment, direct field mutation, and taking `&mut x`. Consuming `let x = owned` by **moving ownership** (`let y = x`) IS allowed.
   - `var` permits reassignment, `var` field mutation (`WON-ADT-004`), and taking `&mut x`.
2. **Dereference Boundaries (`WON-MEM-008`)**:
   - Through `&T`: read-only; mutation or `&mut *r` emits `WON-MUT-001`.
   - Through `&mut T`: `*r = new_val` is permitted; moving a non-`Copy` value *out* of `*r` or `r.field` without replacement **MUST** fail with **`WON-MEM-008`**.

---

## 6. Borrowing, Aliasing Exclusivity & Method Evaluation

At every CFG point:

1. **Shared (`&T`) & Exclusive (`&mut T`) Rules**:
   - Multiple `&T` loans on overlapping places MAY coexist.
   - At most one active `&mut p` loan MAY exist, and it conflicts with any overlapping `&p` or `&mut p`.
   - Overlapping `&mut p` while `&p` live (or read/`&p` while `&mut p` live) -> **`WON-MEM-004`**.
   - Overlapping `&mut p` while `&mut p` live -> **`WON-MEM-005`**.
   - Assigning/mutating/moving `p` while `&p` or `&mut p` live -> **`WON-MEM-006`**.
2. **Disjoint Struct Field Splitting vs Conservative Array Indexing**:
   - Distinct struct fields (`&mut s.a` and `&mut s.b`) are disjoint and **SHALL** be permitted concurrently.
   - Dynamic array/slice indexing (`&mut arr[i]` and `&mut arr[j]`) conservatively treats indices as conflicting (`WON-MEM-005`).
3. **Non-Lexical Active Regions (Within Scopes)**:
   - A borrow loan terminates immediately after the **last CFG point** at which any reference carrying its region may still be read.
   - Reborrowing (`&*r`, `&mut *r`) freezes parent `r` only over the child reborrow's live region.
4. **Deterministic Method Receiver Borrowing (No Two-Phase Borrowing)**:
   - For `recv.method(arg0, arg1, ...)`:
     1. Receiver borrow (`&recv` for `&self`, `&mut recv` for `&mut self`, or `Move`/`Copy` for `self`) starts **immediately upon evaluating `recv`**.
     2. Arguments `arg0, arg1, ...` evaluate strictly left-to-right **while the receiver borrow is active**.
   - Therefore, nested self-borrowing patterns such as `vec.push(vec.len())` or `s.mutate_with(s.peek())` **MUST** fail deterministically with **`WON-MEM-004`**; callers bind the argument first (`let n = vec.len(); vec.push(n)`). Two-phase borrowing is explicitly deferred (`NOT in Phase 2B`).

---

## 7. Zero-Syntax Static & Candidate-Origin Reference Provenance (`Prov(e)`)

Source-level WON uses **only** `&T` and `&mut T`. Named lifetime tokens (`'a`, `'static`) **MUST NOT** appear in WON source syntax or signatures. Internally, the compiler models provenance categories:

$$\text{Prov}(e) \subseteq \{\text{Local}(p), \text{Param}(i), \text{Static}\}$$

1. **Valid `Static` Provenance**:
   - Today, **only string literals** carry compiler-internal `Static` provenance (`&str` backed by read-only binary data).
   - Module-level `static` variable declarations do **not** exist in WON's canonical grammar (`syntax-and-grammar.md`) and are **not** assumed.
2. **No Local Stack Escape (`WON-MEM-003`)**:
   - Any return path where $\text{Local}(p) \in \text{Prov}(e_{\text{ret}})$ **MUST** fail with **`WON-MEM-003`**.
   - Dropping or exiting scope of owner $x$ while a borrow loan on $x$ remains live **MUST** fail with **`WON-MEM-003`**.
3. **Candidate-Origin Provenance Tracking (Strategy B)**:
   - For checked functions with bodies whose return type is `&R` or `&mut R`, the compiler computes the union set of candidate input parameters $\text{RetProv}(f) = S \cup (\{\text{Static}\} \text{ if applicable})$ across all return paths (converging over finite recursion SCCs).
   - **Single-Origin ($|S| \le 1$)**: Caller ties returned region $\rho_{\text{ret}}$ to that single parameter (e.g., `&self` or another single `&arg`), or none if purely `Static`.
   - **Multi-Input Return ($|S| \ge 2$)**: If all paths return valid parameter/static origins (never `Local`), the function signature **is accepted**, and every call site `let r = f(args...)` binds $\rho_{\text{ret}}$ to **ALL** candidate argument loans $i \in S$ simultaneously ($\rho_{\text{arg}_i} \supseteq \rho_{\text{ret}}$):
     - Shared returns (`-> &R`) keep all candidate arguments immutably borrowed across `r`'s live region.
     - Mutable returns (`-> &mut R`) lock all candidate `&mut` arguments (proven disjoint at call entry by `WON-MEM-005`) exclusively across `r`'s live region.
   - **Opaque / Abstract Boundary Rejection (`WON-MEM-002`)**: Where a callable has **no analyzable body** (such as a `fn(&T, &T) -> &T` function pointer type or future bodyless `trait` method) and accepts $\ge 2$ input references while returning a reference, candidate origins cannot be proven modularly, so the compiler **MUST** reject with **`WON-MEM-002`**.

---

## 8. Deterministic Destruction (`Drop`) & Safe Boundaries

1. **Compiler-Managed Cleanup vs `impl Drop`**:
   - Built-in resource types (`String`, future `Vec[T]`, `Shared[T]`, `Arc[T]`) and composites transitively containing them drop automatically at lexical scope exit in **exact reverse declaration order** (along normal fallthrough, `return`, `break`, `continue`, and `?`).
   - User-defined `impl Drop for T:` is specified conceptually but deferred to the future Trait Solver Milestone (`WON-SEM-010` today).
2. **No Double-Free & Conditional Drop Flags**:
   - Moved bindings never drop. Conditionally moved bindings (`MaybeInit`) use compiler-synthesized stack drop flags (`Phase 2B.3`).
3. **Raw Pointers & FFI Boundaries (`WON-SEC-001`, `WON-SEC-002` — `IMPLEMENTED`)**:
   - Raw pointers `*const T`, `*mut T` are `Copy`, cannot coerce implicitly to `&T` or `&mut T`, and require `unsafe:` on dereference (`WON-SEC-002`) or raw C FFI calls (`WON-SEC-001`). Unknown FFI ownership cannot silently become safe.
4. **Concurrency Captures (`scope` / `s.spawn` — Future Phase 3, currently `WON-SEM-010`)**:
   - Owned moves into `s.spawn:` will require `T: Send`.
   - Shared stack borrows `&v` into `s.spawn:` will require `T: Sync` and must remain bounded within the enclosing `scope s:` nursery. Mutable captures `&mut v` across tasks are forbidden (`WON-CON-001`).

---

## 9. Canonical Diagnostics Assigned to Ownership Rules

*(Implemented in `wonc`: `WON-MUT-001`, `WON-SEC-001`, `WON-SEC-002`, `WON-SEM-010`, and Phase 2B.1–2B.1.2 `WON-MEM-001`, `008`, `010`, `011`. Phase 2B.2 active: `WON-MEM-002`–`006`; Future `2B.3` / Phase 3: `WON-MEM-007`, `WON-MEM-009`, `WON-CON-001`.)*

- **`WON-MEM-001`**: Use of moved or partially moved value (`2B.1`).
- **`WON-MEM-002`**: Ambiguous multi-reference provenance across opaque/bodyless signature (`2B.2`).
- **`WON-MEM-003`**: Escape of local stack reference / borrowed owner dropped before last use (`2B.2`).
- **`WON-MEM-004`**: Borrow exclusivity conflict (`&T` vs `&mut T`, including `v.push(v.len())`) (`2B.2`).
- **`WON-MEM-005`**: Multiple simultaneous `&mut` borrows (`2B.2`).
- **`WON-MEM-006`**: Mutating or moving a value while an active borrow exists (`2B.2`).
- **`WON-MEM-007`**: Partial move out of a type implementing custom `Drop` (`2B.1` / Future `Drop`).
- **`WON-MEM-008`**: Moving a non-`Copy` value out of a dereferenced borrow `&T` / `&mut T` (`2B.1`).
- **`WON-MEM-009`**: Optional lint warning — unnecessary `var` binding (`2B.1`).
- **`WON-MEM-010`**: Use of possibly uninitialized binding (`2B.1`).
- **`WON-MEM-011`**: Invalid `@derive(Copy)` attribute (`2B.1`).

---

## 10. Normative Acceptance Examples & Implementation-Status Matrix

Every case below explicitly states its **Proposed Normative Rule** alongside exact status against today's `wonc` compiler (`parser`, `checker`, `C11 backend`).

> [!NOTE]
> `String.len()` returns `u64` in `wonc` Phase 2A.2 (`expressions.go:1802`). Examples below use `u64` cleanly where testing current checker behavior.

### 10.1 Copy Derivation (`A1`–`A4`)

#### A1. `@derive(Copy)` on Struct Containing Only `Copy` Fields (Proposed `PASS` -> `2B.1`)
```won
@derive(Copy)
struct Point2D:
    x: f64
    y: f64

fn main() -> i32:
    let p1 = Point2D { x: 1.0, y: 2.0 }
    let p2 = p1
    if p1.x == p2.x:
        return 0
    return 1
```
- **Proposed Behavior**: Valid (`PASS`). `f64` is `Copy`; `p1` remains readable after `let p2 = p1`.
- **Current Compiler**: Parser rejects `@` (no `AT` token yet).

#### A2. `@derive(Copy)` on Struct Containing `String` (Proposed `REJECT: WON-MEM-011` -> `2B.1`)
```won
@derive(Copy)
struct NamedId:
    label: String
    id: i32
```
- **Proposed Behavior**: Rejected with **`WON-MEM-011`** (`label: String` is non-`Copy`).
- **Current Compiler**: Parser rejects `@`.

#### A3. `@derive(Copy)` on Type Implementing `Drop` (Proposed `REJECT: WON-MEM-011` -> Future Trait Milestone)
```won
@derive(Copy)
struct Handle:
    fd: i32

impl Drop for Handle:
    fn drop(&mut self):
        println(self.fd)
```
- **Proposed Behavior**: Rejected with **`WON-MEM-011`** (types with destructors cannot derive `Copy`).
- **Current Compiler**: Parser rejects `@`; checker rejects `impl Drop` with `WON-SEM-010`.

#### A4. `@derive(Copy)` on Generic Struct with Unconstrained `T` (Proposed `REJECT / DEFERRED: WON-SEM-010` -> Trait Milestone)
```won
@derive(Copy)
struct Wrapper[T]:
    value: T
```
- **Proposed Behavior**: Deferred in Phase 2B (`WON-SEM-010` / rejected until conditional `T: Copy` trait bounds exist in Trait Milestone).
- **Current Compiler**: Parser rejects `@`.

### 10.2 Static Provenance & Escapes (`A5`–`A7`)

#### A5. Returning a String Literal Reference Without `'static` Syntax (Proposed `PASS` -> `2B.2`)
```won
fn greeting(formal: bool) -> &str:
    if formal:
        return "Good morning"
    return "Hello"
```
- **Proposed Behavior**: Valid (`PASS`). Literals carry compiler-internal provenance `Static`, outliving all frames; source type is `&str` with zero lifetime syntax.
- **Current Compiler**: Parses, typechecks cleanly (`Won` Phase 2A.2), rejected by `wonc build` with `WON-GEN-002` (`&str` return).

#### A6. Returning a Reference to a Local Stack Variable (Proposed `REJECT: WON-MEM-003` -> `2B.2`)
```won
fn dangling() -> &String:
    let local = String::from("stack")
    return &local
```
- **Proposed Behavior**: Rejected with **`WON-MEM-003`** ($\text{Local}(local) \in \text{Prov}(\&local)$).
- **Current Compiler**: Parses and typechecks today without borrow checking; rejected at `wonc build` (`WON-GEN-002`).

#### A7. Reference to Module-Level Storage (`NOT IMPLEMENTED / UNSPECIFIED`)
- **Rule**: Module-level `static` items do **not** exist in WON's canonical grammar (`token.go` / `syntax-and-grammar.md`). Static provenance applies **only to string literals** until a future ADR specifies global `static` storage rules.

### 10.3 Multi-Input Return Provenance & Nested Propagation (`A8`–`A10`)

#### A8. Returning a Reference from a Unique Input Parameter (Proposed `PASS` -> `2B.2`)
```won
fn pick_second(tag: i32, text: &String) -> &String:
    return text

fn test_unique() -> u64:
    let msg = String::from("won")
    let view = pick_second(1, &msg)
    return view.len()
```
- **Proposed Behavior**: Valid (`PASS`). `RetProv(pick_second) = {Param(1)}` uniquely.
- **Current Compiler**: Parses and typechecks today (`u64`); `WON-GEN-002` on `wonc build`.

#### A9. Returning a Reference from Either of Two Input Parameters (Proposed `PASS` Under Strategy B; Locks Both Caller Args -> `2B.2`)
```won
fn choose(cond: bool, a: &String, b: &String) -> &String:
    if cond:
        return a
    return b

fn test_choose_ok(cond: bool) -> u64:
    let s1 = String::from("alpha")
    let s2 = String::from("beta")
    let picked = choose(cond, &s1, &s2)
    return picked.len()

fn test_choose_conflict(cond: bool) -> u64:
    var s1 = String::from("alpha")
    var s2 = String::from("beta")
    let picked = choose(cond, &s1, &s2)
    s2.push_str("!")       # ERROR: s2 still candidate-borrowed while picked is live!
    return picked.len()
```
- **Proposed Behavior (Strategy B)**:
  - `choose` itself **compiles cleanly** with `RetProv(choose) = {Param(1), Param(2)}`.
  - `test_choose_ok` is **valid (`PASS`)** because neither `s1` nor `s2` is mutated/dropped while `picked` is live.
  - `test_choose_conflict` **MUST fail (`WON-MEM-006` / `WON-MEM-004`)** on `s2.push_str("!")` because conservative candidate binding keeps both `s1` and `s2` immutably borrowed across `picked`'s NLL region!
  - *(Note: Calling an opaque function pointer `fn(&String, &String) -> &String` with `>= 2` input refs without analyzable body fails with **`WON-MEM-002`**.)*

#### A10. Nested Propagation of Candidate Provenance (Proposed `PASS` + Constraint Propagation -> `2B.2`)
```won
fn outer_choose(c1: bool, c2: bool, x: &String, y: &String, z: &String) -> &String:
    if c1:
        return choose(c2, x, y)
    return z
```
- **Proposed Behavior (Strategy B)**: $\text{RetProv}(outer\_choose) = \{Param(2), Param(3)\} \cup \{Param(4)\} = \{Param(2), Param(3), Param(4)\}$. Caller holds `x`, `y`, `z` borrowed for the lifetime region of the returned reference; returning a local alongside `choose` triggers `WON-MEM-003`.

### 10.4 Method Borrowing, Reborrows & Evaluation Order (`A11`–`A14`)

#### A11. Sequential Mutable Method Calls (Proposed `PASS` -> `2B.2`)
```won
fn test_seq_mut():
    var s = String::from("start")
    s.push_str("-mid")
    s.push_str("-end")
```
- **Proposed Behavior**: Valid (`PASS`). First `&mut s` borrow expires at end of first call before second begins.
- **Current Compiler**: Parses and typechecks cleanly today (`WON-GEN-002` at `wonc build`).

#### A12. Read-Only Method Call Followed by Mutation When Borrow Is Dead (Proposed `PASS` -> `2B.2`)
```won
fn test_read_then_mut() -> u64:
    var s = String::from("data")
    let view = &s
    let before = view.len()
    s.push_str("-updated")
    return before
```
- **Proposed Behavior**: Valid (`PASS`). `view`'s last use is `view.len()` (producing `Copy` `u64`); NLL releases `view` before `s.push_str(...)`.
- **Current Compiler**: Parses and typechecks today (`WON-GEN-002` at `wonc build`).

#### A13. Nested Receiver / Argument Borrowing (`s.push_str(...)` / `vec.push(vec.len())`) (Proposed `REJECT: WON-MEM-004` -> `2B.2`)
```won
fn borrow_len(s: &String) -> &str:
    return "x"

fn test_nested_borrow():
    var s = String::from("abc")
    s.push_str(borrow_len(&s))
```
*(Identical in rule to `vec.push(vec.len())` once `Vec[T]` exists in prelude/stdlib.)*
- **Proposed Behavior**: Rejected with **`WON-MEM-004`** under deterministic left-to-right evaluation (`s.push_str` acquires `&mut s` before `borrow_len(&s)` attempts `&s`). Fix: bind `let suffix = borrow_len(&s)` (or `let n = vec.len()`) beforehand. Two-phase borrowing is `NOT in Phase 2B`.

#### A14. Reborrow While Parent `&mut` Reference Is Live (Proposed `PASS` -> `2B.2`)
```won
fn bump(x: &mut i32):
    *x += 1

fn test_reborrow() -> i32:
    var n: i32 = 10
    let r = &mut n
    bump(r)        # auto-reborrows `&mut *r` cleanly for duration of call
    *r += 5        # parent `r` usable again after reborrow ends
    return n
```
- **Proposed Behavior**: Valid (`PASS`). `bump(r)` reborrows `&mut *r` (freezing `r` over call duration only); `*r += 5` succeeds; `n` reads after `r`'s last use (`NLL`).

### 10.5 Destruction, Conditional Cleanup & Partial Moves (`A15`–`A19`)

#### A15. Moved Value Does Not Drop Twice (Proposed `PASS` -> `2B.3`)
```won
fn consume(s: String):
    println(s)

fn test_no_double_drop():
    let s1 = String::from("once")
    let s2 = s1
    consume(s2)
```
- **Proposed Behavior**: `s1` marked `Moved` at `let s2 = s1`; `s2` moved into `consume`; `test_no_double_drop` drops zero times at scope exit (owned inside `consume` once).

#### A16. Initialized Value Drops on Normal Scope Exit (Proposed `PASS` -> `2B.3`)
```won
fn test_normal_drop() -> u64:
    let a = String::from("first")
    let b = String::from("second")
    return a.len() + b.len()
```
- **Proposed Behavior**: Evaluates return `u64`, then drops `b` before `a` (strict reverse declaration order).

#### A17. Initialized Value Drops on Early Return (Proposed `PASS` -> `2B.3`)
```won
fn test_early_return(abort: bool) -> i32:
    let buf = String::from("owned")
    if abort:
        return 1       # Drops `buf` on early-return edge
    consume(buf)       # Moves `buf` on fallthrough path
    return 0           # No double-free here
```
- **Proposed Behavior**: Early-return edge runs `Drop(buf)`; fallthrough edge skips `Drop(buf)`.

#### A18. Partial-Move Aggregate Cleanup (Non-`Drop` Root Struct) (Proposed `PASS` -> `2B.1` / `2B.3`)
```won
struct TwoStrings:
    first: String
    second: String

fn test_partial_cleanup() -> u64:
    let p = TwoStrings { first: String::from("a"), second: String::from("b") }
    let moved = p.first
    return moved.len() + p.second.len()
```
- **Proposed Behavior**: `p.first` moved to `moved`; at scope exit, `moved` drops and `p.second` drops individually (no drop on moved `p.first`, no `WON-MEM-007` since `TwoStrings` has no custom `impl Drop`). Reading whole `p` after `p.first` moved fails with `WON-MEM-001`.

#### A19. Branch-Dependent Destruction (Drop Flag) (Proposed `PASS` -> `2B.3`)
```won
fn test_cond_drop(flag: bool):
    let s = String::from("maybe")
    if flag:
        consume(s)     # sets _drop_flag_s = false
    # at scope exit: if _drop_flag_s: Drop(s)
```
- **Proposed Behavior**: Synthesizes stack drop flag `_drop_flag_s`; prevents leak when `!flag` and prevents double-free when `flag`.

### 10.6 Cross-Task & FFI Boundaries (`A20`–`A22`)

#### A20. Owned Transfer into Child Task Subject to Future `Send` (Proposed `SPECIFIED` -> Future Concurrency Milestone)
```won
fn spawn_move_example():
    let msg = String::from("task-payload")
    scope s:
        s.spawn:
            println(msg)
```
- **Proposed Behavior**: Valid when `String: Send`; `msg` is `Moved` at `s.spawn:`. Moving `!Send` (`Shared[T]`, `*const T`) into `s.spawn:` is rejected.
- **Current Compiler**: Parsed by `parser.go`; rejected by `checker` with **`WON-SEM-010`** (`scope`/`spawn` not implemented yet).

#### A21. Borrowed Child-Task Data Cannot Outlive Parent Scope (Proposed `SPECIFIED` -> Future Concurrency Milestone)
```won
fn spawn_borrow_example():
    let data = String::from("shared-read")
    scope s:
        s.spawn:
            println(data.len())
    # All child tasks join at end of `scope s:` before `data` drops!
```
- **Proposed Behavior**: Immutable `&data` allowed inside `s.spawn:` because `String: Sync` and `TermScopeJoin` at `scope` exit outlives child tasks; mutable `&mut data` inside `s.spawn:` rejected with `WON-CON-001`.
- **Current Compiler**: Rejected with **`WON-SEM-010`**.

#### A22. Unknown FFI Ownership Cannot Silently Become Safe (`IMPLEMENTED` Gate `WON-SEC-001`/`002` + Future `.wonbind` `2B.3`)
```won
#import <stdlib.h>

fn unsafe_ffi_example(sz: u64):
    let p = stdlib::malloc(sz)    # Rejects WON-SEC-001 outside `unsafe:`
```
- **Proposed & Current Behavior**: Calling uncontracted `#import` C functions or dereferencing `*const T` / `*mut T` outside `unsafe:` **MUST** fail (**`WON-SEC-001`** / **`WON-SEC-002`** — **`IMPLEMENTED`** today in `wonc`!). Safe wrappers require explicit `.wonbind` ownership annotations (`SPECIFIED`, Phase 2B.3+) and `unsafe:` auditing.

### 10.7 Additional Canonical Negative Checks (`N1`–`N8`)

- **N1 (`WON-MEM-001` — Loop Back-Edge Move)**: Moving outer `s: String` inside `for i in 0..2: consume(s)` fails (`MaybeInit` at header). *(Parses & typechecks in 2A.2; caught in **Phase 2B.1**.)*
- **N2 (`WON-MEM-001` — Whole Struct Read After Partial Move)**: Moving `p.a` then passing `p` fails at `p`. *(Parses & typechecks in 2A.2; caught in **Phase 2B.1**.)*
- **N3 (`WON-MEM-007` — Partial Move Out of `Drop`)**: Moving `g.label` out of `impl Drop for Guarded` fails. *(`impl Drop` rejected `WON-SEM-010` today; checked in **Future Trait Milestone**.)*
- **N4 (`WON-MEM-003` — Owner Drops Before Reference Last Use)**: Storing `&inner` from inner block into outer `var r = &fallback` and using `r` after inner block ends fails. *(Parses & typechecks in 2A.2; caught in **Phase 2B.2**.)*
- **N5 (`WON-MEM-004` — `&mut` While `&` Live)**: `let r1 = &x; let r2 = &mut x; println(*r1); *r2 = 2` fails at `&mut x`. *(Parses & typechecks in 2A.2; caught in **Phase 2B.2**.)*
- **N6 (`WON-MEM-005` — Double `&mut`)**: `let m1 = &mut x; let m2 = &mut x; *m1 += 1; *m2 += 1` fails at second `&mut x`. *(Parses & typechecks in 2A.2; caught in **Phase 2B.2**.)*
- **N7 (`WON-MEM-008` — Move Out of Borrowed Ref)**: `fn f(r: &String) -> String: return *r` fails at `*r`. *(Parses & typechecks in 2A.2; caught in **Phase 2B.1**.)*
- **N8 (`WON-MUT-001` + `WON-SEC-002` — Already Enforced Today)**: Mutating/taking `&mut` of `let x`, or `*p` outside `unsafe:` fails **`IMPLEMENTED`** checks in `wonc` today!
- **N9 (`WON-MEM-010` — Use Before Init, Uninit Extension)**: `let x: i32; return x` fails (`WON-MEM-010`). *(Requires proposed optional `=` extension in **Phase 2B.1**.)*

---

## 11. Implementation-Status Summary Across All Examples

| Group / IDs | Normative Outcome | Current Parser (`wonc`) | Current Semantic + Ownership (`2B.1.1`) | Current C11 Backend | Target Enforcement Milestone |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `A1`, `A2`, `A4` (`@derive(Copy)`) | `PASS` (`A1`), `REJECT` (`A2` `011`, `A4` `010`) | `IMPLEMENTED` | **`IMPLEMENTED` (`WON-MEM-011`)** | `WON-GEN-002` (non-scalar) | `IMPLEMENTED` (Phase 2B.1) |
| `A3`, `N3` (`impl Drop`) | `REJECT` (`WON-MEM-011` / `WON-MEM-007`) | Parses `impl Drop` | `WON-SEM-010` (`impl Trait`) | `WON-GEN-002` | Trait Milestone |
| `A5`, `A6`, `A11`–`A14`, `N1`, `N2`, `N7` | `PASS` / Exact `WON-MEM-*` | `IMPLEMENTED` | **`IMPLEMENTED` (`WON-MEM-001` / `008`)** | `WON-GEN-002` (`String`/refs/`struct`) | `IMPLEMENTED` (Phase 2B.1 / 2B.1.1) |
| `A8`–`A10`, `N4`–`N6` (Borrow/NLL/Provenance) | `PASS` / Exact `WON-MEM-*` | `IMPLEMENTED` | **`IMPLEMENTED` (`WON-MEM-002`–`006`)** | `WON-GEN-002` | `IMPLEMENTED` (Phase 2B.2) |
| `A15`–`A19` (Drop / Drop Flags) | `PASS` (exact cleanup order) | `IMPLEMENTED` | Typechecks cleanly | `WON-GEN-002` | Phase 2B.3 & 2C |
| `N9` (Uninit `let x: T`) | `REJECT: WON-MEM-010` | `IMPLEMENTED` | **`IMPLEMENTED` (`WON-MEM-010`)** | Active for supported scalars (`WON-GEN-002` otherwise) | `IMPLEMENTED` (Phase 2B.1) |
| `A20`, `A21` (`scope` / `s.spawn`) | `PASS` (guarded by `Send`/`Sync`) | `IMPLEMENTED` | `WON-SEM-010` | `WON-GEN-002` | Separate Concurrency Milestone (Phase 3) |
| `A22`, `N8` (`WON-MUT-001`, `WON-SEC-001/002`) | `REJECT` | `IMPLEMENTED` | **`IMPLEMENTED` (Active Today)** | Active Gate | Active Today (`2A.2.1`) |

# WON Language Specification: Ownership and Borrowing (Normative Proposal)

> **Specification Version**: Draft 0.2 (Phase 2B.0 Normative Elaboration)  
> **Status**: `SPECIFIED` (**Proposed / Awaiting Owner Approval** — see [ADR-011](../decisions/ADR-011-ownership-and-borrow-checking-semantics.md))  
> **Compiler Implementation Status**: `NOT IMPLEMENTED` (CFG & Borrow Checker scheduled for Phase 2B.1–2B.2; only shallow `WON-MUT-001` and `unsafe:` raw pointer `WON-SEC-002` are currently active in Phase 2A.2)  
> **Governing Architecture**: [Phase 2B.0 Ownership Architecture](../compiler/phase-2b.0-ownership-architecture.md)  
> **Related Core Spec**: [Ownership and Memory Overview](ownership-and-memory.md), [ADR-003](../decisions/ADR-003-ownership-lite-memory-model.md)

---

## 1. Purpose & Normative Authority

This document specifies the formal language-level ownership, move, mutability, borrowing, provenance, destruction, and concurrency-boundary rules for **WON Ownership-Lite**.

Normative keywords **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHALL NOT**, **SHOULD**, and **MAY** follow [RFC 2119](https://tools.ietf.org/html/rfc2119). Every compile-time error rule carries its canonical diagnostic code (`WON-MEM-*`, `WON-MUT-*`, `WON-SEC-*`).

> [!IMPORTANT]
> **Status Boundary**: Nothing in this document claims these checks are already active in the `wonc` Phase 2A.2 Go compiler unless explicitly marked `IMPLEMENTED`. Activation occurs incrementally in Phase 2B.1–2B.2.

---

## 2. Single Ownership & Value Semantics (`Copy` vs `Move`)

1. **Single Owner Invariant**: Every runtime value in safe WON has exactly one owning place (binding, struct field, enum payload, array/tuple slot) at any dynamic execution point.
2. **`Copy` Types (`IsCopy == true`)**:
   - The following types **SHALL** implement `Copy`:
     - All scalar integer (`i8..i128`, `u8..u128`, `isize`, `usize`), float (`f32`, `f64`), `bool`, `char`, `unit`, and `!` types.
     - Immutable shared references (`&T` for any `T`).
     - Raw pointers (`*const T`, `*mut T`) and function pointers (`fn(...) -> R`).
     - Fixed-size arrays `[T; N]` and tuples `(T1, ..., Tn)` if and only if all element types are `Copy`.
   - Assigning, passing by value, or returning a `Copy` place copies its representation bitwise and leaves the source place initialized and readable.
3. **Non-`Copy` Types (`IsCopy == false`)**:
   - Mutable references (`&mut T`), owned heap containers (`String`, `Vec[T]`), RAII resource handles, `Shared[T]`, `Arc[T]`, and any type implementing `Drop` **MUST NOT** be `Copy`.
   - User-defined `struct` and `enum` types default to **non-`Copy` (Move semantics)** unless explicitly opted in via `@derive(Copy)` *(proposed — requires all fields/variants to be `Copy` and no `Drop` impl)*.
4. **Move Semantics (`WON-MEM-001`)**:
   - Assigning or passing a non-`Copy` place transfers ownership to the destination and marks the source place `Moved`.
   - Any subsequent read, move, or borrow of a `Moved` (or conditionally `MaybeInit`) place **MUST** fail compilation with **`WON-MEM-001`**.

---

## 3. Definite Initialization & Reinitialization

1. **Read-Before-Init (`WON-MEM-010`)**: A variable binding **MUST** be definitely initialized along every CFG path reaching a read, move, or borrow of that binding (`WON-MEM-010`).
2. **Branch-Convergent Initialization**: If an uninitialized binding `let x: T` (or `var x: T`) is assigned along *every* reachable non-diverging branch of an `if`/`elif`/`else` or exhaustive `match`, `x` becomes definitely initialized after the merge point.
3. **Reinitialization After Move**: If a `var` binding `x` of non-`Copy` type has been moved, assigning a new value (`x = new_val`) restores `x` to initialized state, after which `x` MAY be moved or borrowed again.
   - Assigning to an immutable `let` binding that is already initialized is rejected (`WON-MUT-001`).

---

## 4. Partial Moves & Structural Integrity

1. **Field-Level Partial Moves**: Moving a non-`Copy` field `s.field` out of a composite `struct` or tuple `s` marks only `s.field` as `Moved`. Unmoved sibling fields (`s.other`) remain accessible and borrowable.
2. **Whole-Struct Lockout**: While any field of `s` is `Moved`, any read, move, or borrow of whole `s` **MUST** fail with **`WON-MEM-001`**. Reinitializing all moved fields restores `s` to whole initialized state.
3. **`Drop` Container Restriction (`WON-MEM-007`)**: Moving any field out of a `struct` whose root type implements `Drop` **MUST** fail with **`WON-MEM-007`**, because `Drop::drop(&mut self)` requires every field to remain valid at destruction time.

---

## 5. Mutability Rules (`let` vs `var` vs `&mut`)

1. **`let` vs `var` Bindings (`WON-MUT-001` — `IMPLEMENTED`)**:
   - `let x = ...` forbids reassignment, field mutation through `x`, and taking `&mut x`.
   - `var x = ...` permits reassignment, field mutation (where permitted by field visibility/mutability `var` in `WON-ADT-004`), and taking `&mut x`.
   - Consuming `let x = owned_val` by **moving** (`let y = x`) is **ALLOWED**, because moving transfers ownership rather than mutating the original place.
2. **Dereference Mutability**:
   - Through `&T`: read-only access only. Writing `*r = v`, mutating fields `r.f = v`, or taking `&mut *r` **MUST** fail with `WON-MUT-001`.
   - Through `&mut T`: reading and overwriting `*r = v` are permitted; moving a non-`Copy` field/target *out* of `*r` without immediate replacement **MUST** fail with **`WON-MEM-008`** (cannot move out of borrowed reference).

---

## 6. Borrowing, Aliasing Exclusivity & Borrow Splitting

At every point in a function's control flow:

1. **Shared Borrows (`&T`)**: Multiple active immutable loans to the same place MAY coexist simultaneously.
2. **Exclusive Borrows (`&mut T`)**: At most one active mutable loan to a place MAY exist at any program point, and **NO** shared loan to any overlapping place may be active concurrently.
3. **Conflict Rules**:
   - Creating `&mut p` while `&p` is active -> **`WON-MEM-004`** (immutable borrow conflicts with mutable borrow).
   - Reading `p` or creating `&p` while `&mut p` is active -> **`WON-MEM-004`**.
   - Creating `&mut p` while another `&mut p` is active -> **`WON-MEM-005`** (multiple mutable borrows).
   - Mutating `p = ...`, moving `p`, or dropping `p` while any `&p` or `&mut p` loan is active -> **`WON-MEM-006`** (mutation/move while borrowed) or **`WON-MEM-003`** (drop while borrowed).
4. **Disjoint Struct & Tuple Borrow Splitting**:
   - Borrows on distinct fields `&mut s.a` and `&mut s.b` (or tuple slots `&mut t.0` and `&t.1`) target disjoint memory places and **SHALL** be permitted simultaneously.
   - Simultaneous mutable indexing `&mut arr[i]` and `&mut arr[j]` conservatively conflicts (`WON-MEM-005`) because `i != j` is generally undecidable at compile time; slice-splitting standard library helpers SHALL be used when distinct slices are needed.
5. **Non-Lexical Active Regions (NLL within Lexical Scope)**:
   - A loan ends immediately after the **last CFG point where any reference carrying that loan's region is live (used)**.
   - Reborrowing `&*r` or `&mut *r` freezes `r` only while the child reborrow's region remains live.

---

## 7. Zero-Annotation Return Provenance (`Prov(e)`)

WON intentionally omits named lifetime parameters (`'a`). Functions returning references obey strict provenance inference:

1. **No Stack Escapes (`WON-MEM-003`)**: A function **MUST NOT** return a reference (`&T`, `&mut T`, `&str`) whose provenance set $\text{Prov}(e_{\text{ret}})$ includes any local variable, local parameter value moved into the frame, or temporary allocated on that function's stack (`WON-MEM-003`).
2. **Static Constants**: Returning `&'static str` string literals (or immutable module-static data) has provenance `Static`, which outlives every call frame and is unconditionally valid.
3. **Single Provenance Propagation**:
   - If a function's return reference is proven to derive exclusively from a **single** input reference parameter (`&self`, `&mut self`, or parameter `arg_i: &U` / `&mut U`), the caller implicitly ties the returned reference's region to `arg_i`.
4. **Ambiguous Multi-Input Rejection (`WON-MEM-002`)**:
   - If a function takes multiple reference parameters and can return a reference derived from more than one of them across control-flow paths (e.g., `if cond: return a else: return b`), the compiler **MUST** conservatively reject the function signature/body with **`WON-MEM-002`**.

---

## 8. Deterministic Destruction (`Drop`)

1. **Lexical Scope Destruction**: Every initialized non-`Copy` binding whose type has a destructor (`Drop` or transitive `Drop` field) is destroyed deterministically when control leaves its enclosing lexical block (via normal completion, `return`, `break`, `continue`, or `?`), in **exact reverse declaration order**.
2. **Value vs Borrow Lifetime Separation**: A borrowed reference ends at its last use (Section 6.5), whereas owned values drop at lexical block exit (Section 8.1). If a variable holding a reference is dropped *after* the referenced owner would be dropped while still live at its drop/use point, the compiler **MUST** emit **`WON-MEM-003`**.
3. **No Double-Free**: Moved bindings and uninitialized paths never execute destructors; path-conditional moves employ compiler-synthesized stack drop flags.

---

## 9. Unsafe Boundaries, Raw Pointers & Structured Concurrency

1. **Raw Pointers (`*const T`, `*mut T`)**:
   - Raw pointers are `Copy` and untracked for aliasing/lifetime provenance.
   - Dereferencing `*p` (`WON-SEC-002`) or calling direct C FFI without safe wrapper (`WON-SEC-001`) **MUST** occur inside `unsafe:`.
   - Raw pointers **MUST NOT** implicitly coerce to `&T` or `&mut T`.
2. **Explicit Sharing & Interior Mutability**:
   - Graphs/shared ownership use `Shared[T]` / `Weak[T]` (single-thread) or `Arc[T]` (multi-thread).
   - Interior mutation uses `Cell[T]`, `RefCell[T]`, `Mutex[T]`, or `RwLock[T]`.
3. **Structured Concurrency (`scope s:` / `s.spawn:` — Phase 3)**:
   - Values moved into `s.spawn:` MUST satisfy `T: Send`.
   - Immutable stack borrows `&x` captured by `s.spawn:` MUST satisfy `T: Sync`, with loan held across the entire enclosing `scope s:`.
   - Capturing `&mut x` directly into `s.spawn:` is prohibited (`WON-CON-001`).

---

## 10. Canonical Normative Test Suite (Positive & Negative Specification Cases)

Every conforming Phase 2B.1–2B.2 borrow checker **MUST** satisfy all 22 normative test scenarios below.

### 10.1 Positive Cases (MUST Compile Cleanly in `wonc check`)

#### P1. `Copy` Primitive Reuse Across Assignments & Calls
```won
fn add_one(n: i32) -> i32:
    return n + 1

fn p1_copy_reuse() -> i32:
    let a: i32 = 41
    let b = a
    return add_one(a) + b
```
- **Expected**: `PASS` (no diagnostics). `i32` is `Copy`; `a` remains valid after `b = a` and `add_one(a)`.

#### P2. Rebinding Non-`Copy` Ownership Chain (`Move` + Drop Once)
```won
fn p2_move_chain() -> usize:
    let s1 = String::from("won")
    let s2 = s1
    return s2.len()
```
- **Expected**: `PASS`. Ownership moves from `s1` to `s2`; `s2` is dropped once at return.

#### P3. Multiple Simultaneous Shared Borrows (`&T`)
```won
fn measure(a: &String, b: &String) -> usize:
    return a.len() + b.len()

fn p3_shared_borrows() -> usize:
    let msg = String::from("hello")
    let r1 = &msg
    let r2 = &msg
    return measure(r1, r2)
```
- **Expected**: `PASS`. Any number of `&String` references may read concurrently.

#### P4. Non-Lexical Borrow End Before Mutation
```won
fn p4_nll_release() -> i32:
    var count: i32 = 10
    let view = &count
    let snapshot = *view
    # Last use of `view` occurred on previous line; loan ends here.
    count = snapshot + 5
    return count
```
- **Expected**: `PASS`. `view` is dead before `count = snapshot + 5`, avoiding false lexical deadlock.

#### P5. Sequential Exclusive Borrows & Reborrows
```won
fn bump(x: &mut i32):
    *x += 1

fn p5_seq_mut_borrows() -> i32:
    var v: i32 = 0
    bump(&mut v)
    bump(&mut v)
    return v
```
- **Expected**: `PASS`. Call 1's `&mut v` loan expires upon return before call 2 begins.

#### P6. Disjoint Struct Field Borrow Splitting
```won
struct Point:
    var x: i32
    var y: i32

fn p6_split_fields() -> i32:
    var pt = Point { x: 1, y: 2 }
    let rx = &mut pt.x
    let ry = &mut pt.y
    *rx += 10
    *ry += 20
    return pt.x + pt.y
```
- **Expected**: `PASS`. `pt.x` and `pt.y` are disjoint projections (`FieldIndex 0 != 1`).

#### P7. Reinitialization After Move (`var` Binding)
```won
fn take_str(s: String):
    println(s)

fn p7_reinit_after_move() -> usize:
    var name = String::from("first")
    take_str(name)
    name = String::from("second")
    return name.len()
```
- **Expected**: `PASS`. `name` becomes `Moved` at `take_str(name)` and returns to `Init` at `name = ...`.

#### P8. Exhaustive Branch Initialization Before Read
```won
fn p8_branch_init(flag: bool) -> String:
    let msg: String
    if flag:
        msg = String::from("yes")
    else:
        msg = String::from("no")
    return msg
```
- **Expected**: `PASS`. Both arms initialize `msg`, so `msg` is definitely `Init` at `return`.

#### P9. Unique-Parameter Return Provenance Propagation (Including Non-`self` Input)
```won
fn pick_str(unused: i32, text: &String) -> &String:
    return text

fn p9_return_prov() -> usize:
    let s = String::from("provenance")
    let ref_s = pick_str(7, &s)
    return ref_s.len()
```
- **Expected**: `PASS`. Compiler proves `Prov(ret) = {Param(1)}` uniquely and ties `ref_s` to `s`.

#### P10. Conditional `return` / `?` Cleanup Without Double-Free
```won
fn p10_early_return(abort: bool) -> i32:
    let buf = String::from("resource")
    if abort:
        return 1
    let moved = buf
    return moved.len() as i32
```
- **Expected**: `PASS`. If `abort` is true, `buf` is dropped cleanly before returning `1`; on fallthrough, `buf` moves to `moved` so only `moved` drops.

---

### 10.2 Negative Cases (MUST Reject with Exact Diagnostic Code)

#### N1. Use-After-Move (`WON-MEM-001`)
```won
fn n1_use_after_move() -> usize:
    let s1 = String::from("owned")
    let s2 = s1
    return s1.len()
```
- **Diagnostic**: `WON-MEM-001` at `s1.len()` (moved at `let s2 = s1`).

#### N2. Move Inside Loop Back-Edge Without Reinit (`WON-MEM-001`)
```won
fn consume(s: String):
    println(s)

fn n2_loop_move():
    let s = String::from("once")
    for i in 0..2:
        consume(s)
```
- **Diagnostic**: `WON-MEM-001` at `consume(s)` (second iteration may read already-moved `s`).

#### N3. Read of Whole Struct While Partially Moved (`WON-MEM-001`)
```won
struct PairStr:
    a: String
    b: String

fn sink(p: PairStr):
    let _x = p

fn n3_partial_move_whole():
    let p = PairStr { a: String::from("x"), b: String::from("y") }
    let extracted = p.a
    sink(p)
```
- **Diagnostic**: `WON-MEM-001` at `sink(p)` (`p.a` moved at `let extracted = p.a`).

#### N4. Partial Move Out of `Drop` Type (`WON-MEM-007`)
```won
struct Guarded:
    label: String
    fd: i32

impl Drop for Guarded:
    fn drop(&mut self):
        println(self.label)

fn n4_partial_move_drop():
    let g = Guarded { label: String::from("log"), fd: 3 }
    let s = g.label
```
- **Diagnostic**: `WON-MEM-007` at `g.label` (cannot move field out of `Drop` type `Guarded`). *(Note: uses `WON-MEM-007` once `Drop` trait resolution is active).*

#### N5. Returning Reference to Local Variable (`WON-MEM-003`)
```won
fn n5_return_local() -> &String:
    let local = String::from("dangling")
    return &local
```
- **Diagnostic**: `WON-MEM-003` at `return &local` ($\text{Prov} \cap \{\text{Local}\} \neq \emptyset$).

#### N6. Dropping Borrowed Owner Before Last Reference Use (`WON-MEM-003`)
```won
fn n6_outlive_scope() -> usize:
    var r: &String
    let fallback = String::from("ok")
    r = &fallback
    if true:
        let inner = String::from("temp")
        r = &inner
    return r.len()
```
- **Diagnostic**: `WON-MEM-003` (`inner` drops at end of `if` block while `r` remains live up to `return r.len()`).

#### N7. Ambiguous Multi-Input Return Reference (`WON-MEM-002`)
```won
fn n7_ambiguous_pick(cond: bool, a: &String, b: &String) -> &String:
    if cond:
        return a
    return b
```
- **Diagnostic**: `WON-MEM-002` at `n7_ambiguous_pick` ($\text{Prov} = \{\text{Param}(1), \text{Param}(2)\}$ cannot be expressed without named lifetimes).

#### N8. Mutable Borrow While Shared Borrow Active (`WON-MEM-004`)
```won
fn n8_mut_while_shared():
    var x: i32 = 1
    let r1 = &x
    let r2 = &mut x
    println(*r1)
    *r2 = 2
```
- **Diagnostic**: `WON-MEM-004` at `&mut x` (`r1` is still live at `println(*r1)`).

#### N9. Double Mutable Borrow (`WON-MEM-005`)
```won
fn n9_double_mut():
    var x: i32 = 1
    let m1 = &mut x
    let m2 = &mut x
    *m1 += 1
    *m2 += 1
```
- **Diagnostic**: `WON-MEM-005` at `&mut x` (`m1` remains active across `*m2 += 1`).

#### N10. Mutate or Move Owner While Borrowed (`WON-MEM-006`)
```won
fn n10_mutate_while_borrowed():
    var s = String::from("abc")
    let view = &s
    s = String::from("xyz")
    println(view.len())
```
- **Diagnostic**: `WON-MEM-006` at `s = String::from("xyz")` (`view` still active until `println(view.len())`).

#### N11. Move Out of Dereferenced Borrow (`WON-MEM-008`)
```won
fn n11_move_out_of_ref(r: &String) -> String:
    return *r
```
- **Diagnostic**: `WON-MEM-008` at `*r` (cannot move non-`Copy` `String` out of shared borrow `&String`).

#### N12. Mutable Borrow / Mutation of Immutable `let` Binding (`WON-MUT-001` + `WON-SEC-002` Boundary)
```won
fn n12_immutable_mut_and_unsafe(p: *const i32):
    let x: i32 = 5
    let m = &mut x
    let v = *p
```
- **Diagnostics**: `WON-MUT-001` at `&mut x` (`x` declared with `let`, not `var` — `IMPLEMENTED`) and `WON-SEC-002` at `*p` (raw pointer deref outside `unsafe:` — `IMPLEMENTED`).

---

## 11. Deferred Items (Beyond Phase 2B Core)
- **Closures & Capture Regions**: Defer `Fn` / `FnMut` / `FnOnce` capture inference until lambda syntax & closure traits are specified.
- **Explicit Lifetime Parameters on User Structs**: Remain out of language design per ADR-003 unless future ADR revises policy.
- **`.wonbind` Ownership Contract Checking**: Automated validation of `@owned` / `@borrow` C binding metadata deferred to FFI hardening sub-milestone.
- **Structured Concurrency `scope` / `s.spawn` Activation**: Runtime and borrow-checker enforcement deferred to Phase 3 (`WON-SEM-010` remains active today).

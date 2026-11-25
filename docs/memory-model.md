# WON Memory Model & Ownership-Lite

> **Document Status**: `SPECIFIED`  
> **Author**: KitiLab Engineering  
> **Last Updated**: Phase 1A  
> **Normative Specification**: [docs/language-spec/ownership-and-memory.md](language-spec/ownership-and-memory.md)

---

## 1. Introduction: The "Ownership-Lite" Vision

A primary design goal of WON is to deliver compile-time memory safety without the cognitive friction and syntactic complexity of Rust-style named lifetime annotations (`'a`, `'b`).

> [!NOTE]
> The formal normative rules for ownership-lite value semantics, borrowing, single-reference elision, explicit sharing (`Shared[T]`, `Arc[T]`), and raw pointer safety are specified in **[docs/language-spec/ownership-and-memory.md](language-spec/ownership-and-memory.md)**.

We term our approach **"Ownership-Lite"**:
- It retains the core safety benefits of ownership, move semantics, and lexical borrowing.
- It enforces strict safety invariants within function bodies and at function call boundaries.
- It eliminates explicit lifetime parameters by constraining borrow scopes to lexical lifetimes and caller-callee scopes.

> [!WARNING]
> **Safety Claims Disclosure**: While the ownership-lite model is fully specified conceptually, formal safety guarantees must not be claimed until the static analysis passes are implemented, tested, and validated in Phase 2.

---

## 2. Core Mechanics

### 2.1 Value Ownership & Move Semantics
Every value in WON has a single owning variable at any given point in execution.
- **Types with the `Copy` Capability**: Simple scalar types (`i8`..`i128`, `u8`..`u128`, `isize`, `usize`, `f32`, `f64`, `bool`, `char`, `&T`, `*const T`, `*mut T`, fixed-size arrays/tuples of `Copy` types) and `@derive(Copy)` composites (`Proposed / Awaiting Owner Approval` in Phase 2B.0.1) are copied bitwise upon assignment or parameter passing.
- **Non-Copy Types**: Heap-allocated structures (such as `String`, future `Vec[T]`, file handles) and user composites without `@derive(Copy)` move ownership upon assignment or passing by value. Accessing a moved variable generates a compile-time error (`WON-MEM-001` once Phase 2B.1 is implemented):

```won
let s1: String = String::from("Hello WON")
let s2 = s1 # Ownership moves to s2
# println(s1) # COMPILE ERROR: Use of moved value 's1' (WON-MEM-001)
```

### 2.2 Lexical Borrowing Rules
Instead of transferring ownership, code may borrow access to a value:
- **Shared Reference (`&T`)**: Multiple immutable references may coexist simultaneously within a lexical scope.
- **Exclusive Reference (`&mut T`)**: Only one mutable reference may exist at a time within a lexical scope.
- **Exclusivity Invariant**: A value cannot be borrowed mutably while immutable borrows are active.

```won
var numbers: Vec[i32] = Vec::new()
numbers.push(1)

# Lexical immutable borrow
block:
    let view: &Vec[i32] = &numbers
    println(view.len())
    # numbers.push(2) # COMPILE ERROR: Cannot borrow 'numbers' mutably while borrowed immutably

numbers.push(2) # OK: Exclusive access restored
```

### 2.3 Elimination of Named Lifetime Annotations
In Rust, returning a reference from a struct or linking the lifetime of a parameter to a return type requires explicit lifetime annotations: `fn select<'a>(x: &'a T, y: &'a T) -> &'a T`.

WON avoids named lifetime parameters through principled defaults and compile-time Reference Provenance ($\text{Prov}(e)$):
1. **No Reference Escape**: Safe functions cannot return references to locally allocated stack frames (`WON-MEM-003`).
2. **Candidate-Origin Borrow Propagation (`Proposed` in ADR-011)**: If a checked function returns a reference originating from one or more input reference parameters (e.g., single input parameter or Strategy B candidate union across branches), the caller's borrow checker automatically constrains the corresponding input borrow regions over the returned reference's NLL liveness span without lifetime syntax.
3. **Opaque Multi-Borrow Boundaries (`WON-MEM-002`)**: Where a bodyless callable (`fn(...)` pointer or future bodyless trait method) takes multiple references and returns a reference without uniquely provable origin, the compiler rejects with `WON-MEM-002`, requiring owned returns, `Shared[T]`, or explicit `&mut Out` parameters.

### 2.4 Explicit Shared Ownership (`Shared[T]`)
For dynamic data structures where ownership cannot be resolved statically at compile time (such as complex graph structures or shared caches), WON provides explicit reference-counted wrappers (`SPECIFIED`):
- `Shared[T]`: Deterministic non-atomic reference counted wrapper for single-threaded usage.
- `Arc[T]`: Atomic reference counted wrapper for multi-threaded usage (`Send + Sync`).
- Releases memory immediately when the reference count drops to zero.

### 2.5 Unsafe Escape Hatches
When interacting with C libraries, operating system drivers, or ultra-low-level memory allocators, developers can use `unsafe:` blocks to perform raw pointer manipulation (`*const T`, `*mut T`). Safe code cannot perform pointer arithmetic or raw pointer dereferencing (`WON-SEC-002` — `IMPLEMENTED` in Phase 2A.1).

---

## 3. Research & Resolved Design Topics

1. **Cycle Detection for `Shared[T]`**: Provided via weak references (`Weak[T]`); optional cycle collector remains `UNDER INVESTIGATION`.
2. **Borrow Splitting on Struct Fields**: Closed in [Phase 2B.0.1 Ownership Architecture](compiler/phase-2b.0-ownership-architecture.md) via `Place`/`ProjField` disjointness (`&mut a.field1` and `&mut a.field2` permitted concurrently once Phase 2B.2 ships).
3. **Cross-Thread Asynchronous Borrows**: Guarded by `Send` / `Sync` auto-traits and `scope s:` nursery bounds (`SPECIFIED` in `ADR-008` / [concurrency-model.md](language-spec/concurrency-model.md), deferred to Phase 3).

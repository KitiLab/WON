# WON Memory Model & Ownership-Lite

> **Document Status**: `SPECIFIED` / `UNDER INVESTIGATION`  
> **Author**: KitiLab Engineering  
> **Last Updated**: Phase 0A

---

## 1. Introduction: The "Ownership-Lite" Vision

A primary design goal of WON is to deliver compile-time memory safety without the cognitive friction and syntactic complexity of Rust-style named lifetime annotations (`'a`, `'b`).

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
- **Types with the `Copy` Trait**: Simple scalar types (`i8`..`i64`, `u8`..`u64`, `f32`, `f64`, `bool`, fixed-size arrays of Copy types) are copied bitwise upon assignment or parameter passing.
- **Non-Copy Types**: Heap-allocated structures (such as `Vec<T>`, `String`, file handles) move ownership upon assignment or passing by value. Accessing a moved variable generates a compile-time error:

```won
let s1: String = String::from("Hello WON");
let s2 = s1; // Ownership moves to s2
// println(s1); // COMPILE ERROR: Use of moved value 's1'
```

### 2.2 Lexical Borrowing Rules
Instead of transferring ownership, code may borrow access to a value:
- **Shared Reference (`&T`)**: Multiple immutable references may coexist simultaneously within a lexical scope.
- **Exclusive Reference (`&mut T`)**: Only one mutable reference may exist at a time within a lexical scope.
- **Exclusivity Invariant**: A value cannot be borrowed mutably while immutable borrows are active.

```won
let mut numbers: Vec<i32> = Vec::new();
numbers.push(1);

// Lexical immutable borrow
{
    let view: &Vec<i32> = &numbers;
    println(view.len());
    // numbers.push(2); // COMPILE ERROR: Cannot borrow 'numbers' mutably while borrowed immutably
} // 'view' goes out of scope here

numbers.push(2); // OK: Exclusive access restored
```

### 2.3 Elimination of Named Lifetime Annotations
In Rust, returning a reference from a struct or linking the lifetime of a parameter to a return type requires explicit lifetime annotations: `fn select<'a>(x: &'a T, y: &'a T) -> &'a T`.

WON avoids named lifetime parameters through principled defaults:
1. **No Reference Escape**: Safe functions cannot return references to locally allocated stack frames.
2. **Borrow Propagation**: If a function takes a single reference parameter and returns a reference, the returned reference is implicitly bound to the lifetime of the input borrow.
3. **Multi-Borrow Projections**: When multiple references are supplied to a function returning a reference, the function must either:
   - Return an owned value, or
   - Return a `Shared<T>` pointer, or
   - Accept an explicit output buffer parameter passed by mutable reference (`&mut Out`).

### 2.4 Explicit Shared Ownership (`Shared<T>`)
For dynamic data structures where ownership cannot be resolved statically at compile time (such as complex graph structures or shared caches), WON provides explicit reference-counted wrappers:
- `Shared<T>`: Deterministic atomic/non-atomic reference counted wrapper.
- Releases memory immediately when the reference count drops to zero.

### 2.5 Unsafe Escape Hatches
When interacting with C libraries, operating system drivers, or ultra-low-level memory allocators, developers can use `unsafe { ... }` blocks to perform raw pointer manipulation (`*const T`, `*mut T`). Safe code cannot perform pointer arithmetic or raw pointer dereferencing.

---

## 3. Unresolved Questions & Research Areas

The following topics remain `UNDER INVESTIGATION`:
1. **Cycle Detection for `Shared<T>`**: Determining whether weak reference types (`Weak<T>`) are sufficient, or if an optional cycle-breaking collector should be provided.
2. **Borrow Splitting on Struct Fields**: Designing precise data-flow rules that allow simultaneously borrowing distinct fields of a struct mutably (`&mut a.field1` and `&mut a.field2`) without triggering false borrow checker rejections.
3. **Cross-Thread Asynchronous Borrows**: Rules governing references passed to async task executors or thread pools.

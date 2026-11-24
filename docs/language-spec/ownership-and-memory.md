# WON Language Specification: Ownership and Memory Model

> **Specification Version**: Draft 0.1  
> **Status**: `SPECIFIED` (Under Architectural Review)  
> **Module**: Ownership-Lite & Memory Safety  
> **Date**: November 2026

---

## 1. Architectural Philosophy: Ownership-Lite

Systems languages face a stark historical dichotomy:
- **Tracing Garbage Collection (Go, Java)**: Eliminates memory bugs at the cost of non-deterministic GC pauses, hidden runtime bloat, and poor low-level C FFI ergonomics.
- **Explicit Lifetime Annotations (Rust)**: Achieves deterministic safety, but infects everyday APIs with complex lifetime syntax (`'a`, `'b: 'a`), steepening the learning curve and creating frictional hurdles for automated AI coding agents.

WON establishes a third path: **Ownership-Lite**.
> **Ownership-Lite Invariant**: Complete compile-time memory safety and deterministic destruction with zero garbage collection, **without requiring developers or AI agents to annotate named lifetime parameters (`'a`) in application source code.**

---

## 2. Value Semantics: Copy vs. Move

Every value in WON has a single unique owner at any point during program execution. When a variable goes out of scope, its resources are immediately and deterministically destroyed.

```
                         Value Assignment (b = a)
                                    │
                  ┌─────────────────┴─────────────────┐
                  ▼                                   ▼
          Implements Copy?                    Does Not Implement Copy
                  │                                   │
                  ▼                                   ▼
             Bitwise Copy                     Ownership Transfer (Move)
          (Both valid afterwards)             (Source variable invalidated)
```

### 2.1 The `Copy` Capability
Types that implement the `Copy` trait have cheap, bitwise-reproducible representations that reside entirely on the stack and own no external heap or OS resources:
- All primitive integer types (`i8`..`i128`, `u8`..`u128`, `isize`, `usize`).
- Floating-point types (`f32`, `f64`), booleans (`bool`), and characters (`char`).
- Raw pointers (`*const T`, `*mut T`).
- Fixed-size arrays `[T; N]` and tuples where `T: Copy`.
- Structs composed exclusively of fields implementing `Copy`.

For `Copy` types, assignment and parameter passing duplicate the bits without invalidating the source variable.

### 2.2 Move Semantics (Default for Non-Copy Types)
All types that manage heap allocations, OS handles, sockets, or file descriptors (e.g., `String`, `Vec[T]`, `File`, `Arc[T]`) do **NOT** implement `Copy`.
- Passing or assigning an owned non-Copy value transfers ownership (**Move**).
- After a move, the source variable is statically marked as uninitialized. Any subsequent read or use of the source variable results in a compile-time diagnostic (`WON-MEM-001: Use of moved value`).
- At the machine level, moves are simple bitwise copies of the small stack-allocated descriptor (pointer, length, capacity).

```won
let s1 = String::from("WON Systems")
let s2 = s1    # Ownership transferred to s2
# println(s1)  <- Compile Error: Use of moved value 's1'
```

---

## 3. Borrowing and Reference Semantics

Rather than transferring ownership, values can be temporarily accessed via references (**Borrows**).

### 3.1 Immutable Borrows (`&T`)
- Multiple immutable references to the same value **MAY** coexist simultaneously.
- The underlying value cannot be modified or moved while active immutable references exist.

### 3.2 Mutable Borrows (`&mut T`)
- A mutable reference grants exclusive read and write access to the target value.
- **The Core Aliasing Invariant**:
  $$\text{At any point in time, a value may have either: } (\text{Any number of } \&T) \;\text{ XOR }\; (\text{Exactly one } \&\text{mut } T)$$
- This rule mathematically guarantees the elimination of data races, iterator invalidation, and dangling pointer aliasing at compile time.

---

## 4. The Lifetime-Free Enforcement Engine

Rust achieves soundness by requiring developers to explicitly name and link lifetime parameters across function signatures:
```rust
// Rust: Explicit lifetime parameters required
fn longest<'a>(x: &'a str, y: &'a str) -> &'a str { ... }
```

WON eliminates `'a` annotations using **Scope-Bounded Region Inference** and a strict set of **Reference Return Rules**:

### 4.1 Lexical Scope Bounding
References in local variables are tracked via lexical intervals. A reference `&x` is bounded strictly by the scope of the owner `x`. A reference **MUST NOT** escape the scope of the referenced value.

### 4.2 Function Call Boundary Invariants
At function call boundaries, the compiler enforces three deterministic rules without requiring lifetime syntax:

1. **Rule 1: References Cannot Escape into Stored Fields Without Explicit Sharing**  
   Structs containing borrowed references (`&T`) cannot be returned from the function where the referenced value was created. To store references across long-lived boundaries, developers **MUST** use explicit owned sharing (`Shared[T]` or `Arc[T]`).

2. **Rule 2: Single-Reference Elision**  
   If a function takes exactly one reference parameter and returns a reference, the output reference is automatically tied to the input reference's lifetime:
   ```won
   # Inferred: The returned &str is borrowed from `input`
   fn trim_whitespace(input: &str) -> &str:
       ...
   ```

3. **Rule 3: Multi-Reference Disambiguation Rule**  
   If a function takes multiple reference parameters and returns a reference, one of two disambiguation rules applies:
   - **Method Receiver Precedence**: If the function is a method taking `&self` or `&mut self`, the returned reference is tied exclusively to `self`.
   - **Explicit Disambiguation Requirement**: If a free function takes multiple reference arguments (such as `fn select(a: &str, b: &str) -> &str`), the compiler rejects the ambiguous signature (`WON-MEM-002`) and requires the developer to either:
     - Return an owned value (`String`).
     - Or pass an explicit output destination reference (`fn select(a: &str, b: &str, out: &mut &str)`).

By forbidding ambiguous multi-input-to-single-output reference returns in free functions, WON guarantees 100% sound lifetime verification without a single lifetime annotation in the language grammar.

---

## 5. Explicit Sharing and Memory Arenas

Complex data topologies (graphs, ASTs, observers) require non-exclusive or shared ownership. WON provides explicit, auditable abstractions:

### 5.1 `Shared[T]` (Single-Threaded Reference Counting)
- Provides shared ownership within a single thread.
- Tracks strong and weak reference counts.
- Non-atomic reference counting operations ensure near-zero CPU overhead.
- Does not implement `Send` or `Sync`; rejected by the compiler if passed across thread boundaries.

### 5.2 `Arc[T]` (Atomic Thread-Safe Reference Counting)
- Provides multi-threaded shared ownership.
- Reference counts are updated using atomic CPU instructions (`lock xadd`).
- Safe to transfer across task and thread boundaries (`Send + Sync` when `T: Send + Sync`).

### 5.3 Interior Mutability: `Mutex[T]` and `RwLock[T]`
Shared references (`&Shared[T]` or `&Arc[T]`) are immutable. To mutate data behind a shared pointer, explicit synchronization primitives are required:
```won
let shared_state = Arc::new(Mutex::new(Counter { count: 0 }))
```

### 5.4 `Arena[T]` (Region-Based Memory Management)
For compilers, parsers, and game engines where thousands of small objects share a common lifecycle:
- An `Arena[T]` allocates memory in contiguous blocks (chunks).
- Individual object deallocations are no-ops.
- When the entire `Arena` goes out of scope, all allocated memory is reclaimed in a single, high-speed OS deallocation.
- Objects allocated within an arena share the arena's lifetime, eliminating per-node reference counting overhead.

---

## 6. Deterministic Destruction (RAII)

WON guarantees deterministic destruction of all resources:
1. **Drop Order**: Variables in a scope are destroyed in reverse order of declaration (LIFO). Fields within a struct are destroyed in order of declaration.
2. **Deterministic Unwinding**: On early `return` or error propagation (`?`), all local variables constructed up to that point are dropped before the stack frame is released.
3. **The `Drop` Trait**: Custom clean-up logic is defined via the `Drop` trait:
   ```won
   trait Drop:
       fn drop(&mut self)
   ```

---

## 7. Raw Pointers and Unsafe Operations

For low-level systems programming, operating system interfaces, and C library interop:
- **Raw Pointers**: `*const T` (immutable raw pointer) and `*mut T` (mutable raw pointer).
- **Properties**: Raw pointers may be null, unaligned, or dangling. The compiler does not track their lifetimes or aliasing.
- **Unsafe Boundary**: Creating raw pointers is safe (`let p = &val as *const i32`), but **dereferencing a raw pointer, pointer arithmetic, or calling foreign C functions MUST take place inside an explicit `unsafe:` block.**

```won
fn read_word(address: usize) -> u32:
    let ptr = address as *const u32
    unsafe:
        *ptr    # Dereference allowed only inside unsafe block
```

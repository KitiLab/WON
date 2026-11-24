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

> **Status**: `PROPOSED` / `UNDER INVESTIGATION` (Formal Soundness Model)

Rust achieves soundness by requiring developers to explicitly name and link lifetime parameters across function signatures (`fn longest<'a>(x: &'a str, y: &'a str) -> &'a str`).

WON eliminates explicit `'a` lifetime annotations from application and systems APIs by employing **Reference Provenance Tracking**, **Scope-Bounded Region Inference**, and **Strict Multi-Reference Rejection**:

### 4.1 Lexical Scope Bounding
References in local variables are tracked via lexical intervals. A reference `&x` is bounded strictly by the scope of the owner `x`. A reference **MUST NOT** escape the scope of the referenced value.

### 4.2 Reference Provenance Tracking ($\text{Prov}(e)$)
The compiler statically computes a **Provenance Set** $\text{Prov}(e)$ for every reference expression $e$:
$$\text{Prov}(e) \subseteq \text{Params} \cup \text{Globals} \cup \text{Locals}$$

- **Parameter Provenance**: If $e$ is derived from an input reference parameter $p$, then $p \in \text{Prov}(e)$.
- **Global Provenance**: If $e$ points to static/constant global memory, $\text{Prov}(e) = \{\text{static}\}$.
- **Local Provenance**: If $e$ borrows a local variable $l$ within the current stack frame, then $l \in \text{Prov}(e)$.
- **Conditional Paths**: For branches (`if cond: r1 else: r2`), the resulting provenance is the union: $\text{Prov} = \text{Prov}(r_1) \cup \text{Prov}(r_2)$.

> **The Non-Escape Invariant**:
> If a function returns a reference $r_{\text{ret}}$, the compiler asserts:
> $$\text{Prov}(r_{\text{ret}}) \cap \text{Locals} = \emptyset$$
> If any execution path allows a reference derived from a local stack variable to reach the return expression, compilation is rejected with diagnostic `WON-MEM-003: Escape of local reference`.

### 4.3 Function Boundary Invariants

1. **Rule 1: References Cannot Escape into Stored Fields Without Explicit Sharing**  
   Structs containing borrowed references (`&T`) cannot be returned from the function where the referenced value was created. To store references across long-lived boundaries, developers **MUST** use explicit owned sharing (`Shared[T]` or `Arc[T]`).

2. **Rule 2: Provenance-Validated Single-Reference Elision**  
   If a function takes exactly one reference parameter $p$ and returns a reference:
   - The returned reference is valid **if and only if** its proven provenance satisfies:
     $$\text{Prov}(r_{\text{ret}}) \subseteq \{p\} \cup \{\text{static}\}$$
   - The return reference's lifetime is automatically bound to the caller's argument lifetime corresponding to $p$.
   - If $\text{Prov}(r_{\text{ret}})$ contains any local stack variable, compilation is rejected (`WON-MEM-003`).

3. **Rule 3: Provenance-Driven Method Receiver Rule**  
   For methods with a receiver (`&self` or `&mut self`) and additional reference parameters:
   - The compiler computes $\text{Prov}(r_{\text{ret}})$.
   - If $\text{Prov}(r_{\text{ret}}) \subseteq \{\text{self}\}$, the returned reference is tied to `self`.
   - If $\text{Prov}(r_{\text{ret}}) \subseteq \{\text{other}\}$, the returned reference is tied to `other`, **NOT** `self`. For example:
     ```won
     fn choose(&self, other: &str) -> &str:
         other    # Prov = {other}; caller binds lifetime to `other`
     ```
   - If the return provenance conditionally merges `self` and another reference parameter ($\text{Prov} = \{\text{self}, \text{other}\}$), the signature is ambiguous and **MUST** be rejected under Rule 4.

4. **Rule 4: Conservative Multi-Reference Rejection**  
   If a free function (or method) takes multiple reference parameters and its return expression conditionally originates from more than one input reference parameter ($\text{Prov}(r_{\text{ret}}) = \{p_1, p_2, \dots\}$), WON **conservatively rejects the signature at compile time** (`WON-MEM-002: Ambiguous multi-reference provenance`).
   
   To resolve the ambiguity without named lifetime annotations, developers **MUST** choose one of three safe designs:
   - **Alternative A (Return Owned Value)**: Return an owned `String` or `Vec[T]`.
   - **Alternative B (Return Selection Index)**: Return a numeric index (`usize`) or enum discriminator into the slice/collection.
   - **Alternative C (Caller-Allocated Destination)**: Pass an explicit caller-allocated destination reference (`fn select(a: &str, b: &str, out: &mut &str)`), binding the output lifetime unambiguously at the caller's scope.

### 4.4 Formal Borrow Analysis Algorithm

The borrow checker operates on the Control Flow Graph (CFG) in six sequential phases:

```text
Algorithm: Scope-Bounded Borrow and Provenance Checker
Input: CFG of Function F = (BasicBlocks, Edges)
Output: Valid CFG or Compile Diagnostic (WON-MEM-xxx)

1. [Initialization Tracking]
   For each local variable v:
     State(v) in { Uninitialized, Initialized, Moved }
   Track Moves: When non-Copy variable v is moved, mark State(v) = Moved.
   If read(v) occurs while State(v) == Moved:
     Emit WON-MEM-001 (Use of moved value).

2. [Provenance Analysis]
   For each reference expression r = &x or r = &mut x:
     Prov(r) = { x } if x in (Params U Locals U Globals)
     If r = deref(p): Prov(r) = Prov(p)
   At merge points: Prov(r_out) = Union(Prov(r_in1), Prov(r_in2))

3. [Live Borrow Sets]
   Maintain at each program point:
     ActiveShared: Set of (target, borrow_id, scope_end)
     ActiveMutable: Set of (target, borrow_id, scope_end)
   
   On `b = &x`:
     If x in ActiveMutable:
       Emit WON-MEM-004 (Cannot borrow x immutably while mutably borrowed).
     Add (x, b, Scope(b)) to ActiveShared.

   On `b = &mut x`:
     If x in ActiveShared or x in ActiveMutable:
       Emit WON-MEM-005 (Cannot borrow x mutably while borrowed).
     Add (x, b, Scope(b)) to ActiveMutable.

4. [Mutation Invalidation]
   On write/assignment to x:
     If x in ActiveShared or x in ActiveMutable:
       Emit WON-MEM-006 (Cannot mutate x while borrowed).

5. [Function Return Verification]
   For return statement `return expr`:
     If Type(expr) is reference `&T` or `&mut T`:
       If Prov(expr) INTERSECT Locals != Empty:
         Emit WON-MEM-003 (Escape of local stack reference).
       If |Prov(expr) INTERSECT Params| > 1:
         Emit WON-MEM-002 (Ambiguous multi-reference provenance).
```

---

## 5. Explicit Sharing and Memory Arenas

Complex data topologies (graphs, ASTs, observers) require non-exclusive or shared ownership. WON provides explicit, auditable abstractions:

### 5.1 `Shared[T]` (Single-Threaded Reference Counting)
- Provides shared ownership within a single thread.
- Tracks strong and weak reference counts (`Weak[T]` via `Shared::downgrade()`) to break cyclic graphs.
- Non-atomic reference counting operations ensure near-zero CPU overhead.
- Does not implement `Send` or `Sync`; rejected by the compiler if passed across thread boundaries.

### 5.2 `Arc[T]` (Atomic Thread-Safe Reference Counting)
- Provides multi-threaded shared ownership with atomic reference counting.
- Safe to transfer across task and thread boundaries (`Send + Sync` when `T: Send + Sync`).
- Provides `Weak[T]` (`Arc::downgrade()`) for thread-safe cycle breaking.

### 5.3 Interior Mutability: `Mutex[T]` and `RwLock[T]`
Shared references (`&Shared[T]` or `&Arc[T]`) are immutable. To mutate data behind a shared pointer, explicit synchronization primitives are required:
```won
let shared_state = Arc::new(Mutex::new(Counter { count: 0i64 }))
```

### 5.4 `Arena[T]` (Region-Based Memory Management)
For compilers, parsers, and game engines where thousands of small objects share a common lifecycle:
- An `Arena[T]` allocates memory in contiguous blocks (chunks).
- Individual object deallocations are no-ops.
- When the entire `Arena` goes out of scope, all allocated chunk memory is reclaimed in a single, high-speed OS deallocation.
- Objects allocated within an arena do **NOT** invoke individual `Drop` destructors by default unless explicitly allocated via `arena.alloc_with_drop(val)`.

---

## 6. Advanced Memory & Destruction Semantics

### 6.1 Copy Behavior for Large Fixed-Size Arrays
Types that implement `Copy` (including `[T; N]` where `T: Copy`) duplicate bits upon assignment. However, to prevent stack overflow from copying large fixed-size arrays:
- Arrays where `size_of([T; N]) > 256` bytes trigger a compiler performance warning (`WON-PERF-001`) advising the developer to pass the array by immutable reference `&[T; N]` instead of copying by value.

### 6.2 Partial Moves from Structs
- If a struct implements the `Drop` trait, partial moves of its fields are **FORBIDDEN** (`WON-MEM-007: Cannot partially move from struct implementing Drop`).
- If a struct does not implement `Drop`, individual non-`Copy` fields may be moved out. Once any field is moved, the remaining fields may still be read, but the parent struct cannot be moved or passed as a whole.

### 6.3 Partial Initialization and Destruction
If an error or panic occurs while constructing a struct or tuple, all fields that have already been evaluated and initialized are destroyed in reverse order of evaluation; uninitialized fields are untouched.

### 6.4 Destructor Panic Safety (Double-Panic Abort)
Destructors (`Drop::drop`) must be panic-free. If a destructor panics while the thread is already unwinding from an active panic or task failure, the runtime immediately terminates the process via `abort()` to prevent undefined behavior and cascading heap corruption.

### 6.5 Foreign C ABI Panic Boundary
A WON panic **MUST NOT** unwind across an `extern "C"` boundary. If a safe WON callback passed to a C library panics, the panic is caught at the foreign boundary and converted to a process abort or designated C error code.

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

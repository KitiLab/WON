# WON Language Specification: Performance and Native Execution Semantics

> **Specification Version**: Draft 0.1  
> **Status**: `SPECIFIED` (Linux x86_64 LP64 C11 Subset Verified in [Phase 2A.2 Backend Matrix](../compiler/phase-2a.2-backend-capability-matrix.md))  
> **Module**: Execution Engine, Memory Layouts & ABI  
> **Date**: November 2026

---

## 1. Non-Negotiable Performance Mandate

The primary performance objective of WON is **native parity with optimized C**:
- **Ahead-of-Time Compilation**: Source files compile directly to native machine code via optimized backends.
- **Zero GC Pauses**: Memory reclamation is 100% deterministic, executed via RAII stack drops and explicit deallocations without runtime pauses.
- **Zero Global Interpreter Lock (Zero-GIL)**: The runtime contains no global interpreter lock, permitting true parallel execution of independent tasks across native OS threads.
- **Predictable Mechanical Sympathy**: Cache-conscious memory layouts, contiguous array allocations, and zero hidden boxing.

---

## 2. Memory Allocation Semantics: Stack vs. Heap

WON distinguishes stack allocation from heap allocation with complete transparency:

```
                          Memory Allocation Rules
                                    │
          ┌─────────────────────────┴─────────────────────────┐
          ▼                                                   ▼
   Stack Allocation                                    Heap Allocation
• Primitives (i8..i128, f64)                       • Dynamic Vectors (Vec[T])
• Fixed-size Arrays ([T; N])                       • Owned Strings (String)
• Structs by value                                 • Heap Boxes (Box[T])
• Zero runtime allocator cost                      • Explicit Shared (Shared[T], Arc[T])
• Lifetime bounded by local scope                  • String interpolation & closures
```

1. **Stack by Default**: Variables, tuples, structs, and fixed-size arrays (`[u8; 1024]`) are allocated directly on the current thread stack. Stack allocation incurs zero heap allocation overhead, maximizes L1/L2 cache locality, and reclaims memory instantaneously upon stack frame unwinding.
2. **Heap Allocation Points**:
   - Heap memory is allocated strictly through explicit collection types (`Vec[T]`, `HashMap[K, V]`), heap boxes (`Box::new(val)`), or shared pointers (`Shared::new(val)`, `Arc::new(val)`).
   - In expressions, dynamic allocations occur specifically during:
     - String formatting and interpolation (`"Hello, {name}"` produces an owned `String`).
     - Growing dynamic collections beyond their current capacity.
     - Heap-allocated closures capturing non-`Copy` environments.
3. **Pluggable Allocators**: Systems and embedded targets can replace the standard heap allocator (`jemalloc` or system `malloc`) with custom arena, slab, or embedded bare-metal allocators.

---

## 3. Data Layout, Alignment, and Representation

### 3.1 Default Struct Layout
The compiler is free to reorder fields within a standard `struct` to minimize padding and maximize hardware cache alignment:
- Fields are sorted by descending alignment requirements (e.g., 64-bit integers and pointers first, followed by 32-bit integers, 16-bit integers, and 8-bit bytes).
- Struct size is always an integer multiple of its largest field alignment.

### 3.2 C-Compatible Layout (`@repr(C)`)
For structs passed across the foreign function interface to C libraries:
```won
@repr(C)
struct ZStream:
    next_in: *const u8
    avail_in: u32
    total_in: u64
    next_out: *mut u8
    avail_out: u32
    total_out: u64
```
- Fields **MUST** be laid out in the exact order declared.
- Alignment and padding **MUST** conform strictly to the host C compiler's ABI rules (e.g., standard System V AMD64 or Microsoft x64).

### 3.3 Packed Structs (`@repr(packed)`)
For binary wire protocols and hardware device registers:
- Struct fields have an alignment of 1 byte with zero inter-field padding.
- **Safety Invariant**: Because borrowing an unaligned field can trigger hardware misalignment traps on modern architectures, **taking a reference to an unaligned field (`&packed.field`) is strictly FORBIDDEN in safe code** (`WON-TYP-005: Cannot borrow unaligned packed field by reference`). Access **MUST** occur by value copy or via raw pointer in an `unsafe:` block.

---

## 4. Bounds Checking and Safety Invariants

1. **Array and Slice Indexing**:
   - Accessing an array or slice element (`arr[i]`) performs an automated bounds check ($0 \le i < \text{length}$) in safe code.
   - If an index is out of bounds, the program triggers an immediate panic, preventing memory corruption or out-of-bounds reads.
2. **Optimizer Bounds Elimination**:
   - The compiler's optimization pipeline eliminates bounds checks inside induction loops (e.g., `for item in slice:`) where bounds safety is mathematically proven.
3. **Unchecked Indexing in Unsafe Blocks**:
   - In performance-critical inner loops where bounds checks have been verified externally, developers **MAY** use `.get_unchecked(i)` inside an `unsafe:` block to completely bypass the check.

---

## 5. Calling Conventions and Machine Interop

- **Internal Function Calls**: Internal function calling conventions are target-specific backend responsibilities (e.g., register-based passing on x86_64 and AArch64). The compiler is free to optimize register assignments across module boundaries during whole-program Link-Time Optimization (LTO).
- **Foreign C Calls**: Functions declared with `extern "C"` adhere strictly to the target platform C calling convention:
  - Linux, macOS, BSD: System V AMD64 ABI.
  - Windows: Microsoft x64 ABI.
- **Inlining Strategy**: Small functions and generic method specializations are aggressively inlined across module boundaries during LTO.

---

## 6. Runtime Startup and Execution Footprint

- **Sub-Millisecond Startup Target**: Because WON executables do not initialize a garbage collection daemon, JIT engine, or complex reflection tables, sub-millisecond process startup is an empirical design target on modern systems.
- **Minimal Binary Size**: Dead code elimination and LTO strip unreferenced functions, allowing minimal CLI utilities to compile down to tens of kilobytes.

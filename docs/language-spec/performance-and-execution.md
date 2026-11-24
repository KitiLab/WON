# WON Language Specification: Performance and Native Execution Semantics

> **Specification Version**: Draft 0.1  
> **Status**: `SPECIFIED` (Under Architectural Review)  
> **Module**: Execution Engine, Memory Layouts & ABI  
> **Date**: November 2026

---

## 1. Non-Negotiable Performance Mandate

The primary performance objective of WON is **native parity with optimized C**:
- **Ahead-of-Time Compilation**: Source files compile directly to native machine code via optimized backends.
- **Zero GC Pauses**: Memory reclamation is 100% deterministic, executed via RAII stack drops and explicit deallocations without runtime pauses.
- **Zero Global Interpreter Lock (GIL)**: Concurrent tasks execute across all available CPU cores with native OS threading.
- **Predictable Mechanical Sympathy**: Cache-conscious memory layouts, contiguous array allocations, and zero hidden boxing.

---

## 2. Memory Allocation Semantics: Stack vs. Heap

WON distinguishes stack allocation from heap allocation with absolute transparency:

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
• Lifetime bounded by local scope                  • Bounded by RAII drop owner
```

1. **Stack by Default**: Variables, tuples, structs, and fixed-size arrays (`[u8; 1024]`) are allocated directly on the current thread stack. Stack allocation incurs zero heap allocation overhead, maximizes L1/L2 cache locality, and reclaims memory instantaneously upon stack frame unwinding.
2. **Explicit Heap Allocation**: Heap memory is never allocated implicitly by the compiler. It is allocated strictly when constructing heap-backed collection types (`Vec`, `String`, `HashMap`) or explicit heap boxes (`Box::new(val)`).
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
- Accessing unaligned fields generates safe byte-by-byte copies to prevent hardware unaligned memory faults.

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

- **Internal Function Calls**: The compiler uses a fast register-based calling convention: arguments are passed in hardware registers (`RAX`, `RDI`, `RSI`, `RDX`, `RCX`, `R8`, `R9` on x86_64), with small structs returned directly in registers.
- **Foreign C Calls**: Functions declared with `extern "C"` adhere strictly to the target platform C calling convention:
  - Linux, macOS, BSD: System V AMD64 ABI.
  - Windows: Microsoft x64 ABI.
- **Inlining Strategy**: Small functions and generic method specializations are aggressively inlined across module boundaries during Link-Time Optimization (LTO).

---

## 6. Runtime Startup and Execution Footprint

- **Sub-Millisecond Startup**: Because WON executables do not initialize a garbage collection daemon, JIT engine, or complex reflection tables, startup latency is under 1 millisecond.
- **Minimal Binary Size**: Dead code elimination and LTO strip unreferenced functions, allowing minimal CLI utilities to compile down to tens of kilobytes.

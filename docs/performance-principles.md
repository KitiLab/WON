# WON Performance Engineering Principles

> **Document Status**: `SPECIFIED`  
> **Author**: KitiLab Engineering  
> **Last Updated**: Phase 0A

---

## 1. Performance as a Non-Negotiable Constraint

In WON, execution performance, compilation speed, and memory efficiency are non-negotiable architectural constraints, not post-hoc optimizations.

Our objective is:
- **Match optimized native C** across representative compute, memory, and I/O workloads.
- **Outperform C** on specific workloads where compile-time ownership tracking enables superior memory reuse, arena allocations, and aggressive vectorization.
- **Maintain sub-millisecond startup times** and minimal memory overhead.
- **Fast incremental compilation** to preserve human and agent development velocity.

> [!IMPORTANT]
> **Zero Fabrication Policy**: We will never publish fabricated, unverified, or misleading performance claims. All reported benchmark figures must be derived from automated, public, and reproducible test suites run on disclosed hardware.

---

## 2. Core Architectural Levers for Performance

### 2.1 Ahead-of-Time (AOT) Native Compilation
WON compiles directly to native machine instructions through optimized C (GCC / Clang `-O3`) with Link-Time Optimization (LTO). There is no bytecode interpretation, no intermediate VM startup cost, and no JIT warm-up latency.

### 2.2 Predictable Memory Layouts & Cache Efficiency
- **Flat Memory Layouts**: Structs and tuples are contiguous in memory by default. No hidden pointer chasing or object boxing.
- **Zero-Cost Abstractions**: Generics are monomorphized at compile time. Traits and interfaces resolve to direct function calls without dynamic dispatch overhead unless explicitly requested via dynamic trait objects.
- **No Garbage Collection**: Memory is reclaimed deterministically at compile-time determined scope boundaries, eliminating runtime stop-the-world pauses and GC metadata overhead.

### 2.3 Concurrency Without Global Locks
- **No Global Interpreter Lock (GIL)**: WON provides true native multi-threading.
- **Thread Safety Invariants**: The ownership-lite system verifies at compile time that data shared between threads is either immutable or safely synchronized, eliminating data races without runtime lock bloat.

### 2.4 SIMD Vectorization & Optimization Passes
The compiler pipeline is designed to emit C constructs that mature native compilers easily vectorize (auto-vectorization via GCC/Clang with `#pragma omp simd` or explicit SIMD intrinsics).

---

## 3. Empirical Benchmarking Methodology

To ensure integrity and scientific rigor, WON adopts the following public benchmarking rules (formalized in [ADR-005](decisions/ADR-005-public-benchmark-methodology.md)):

1. **Equivalent Implementations**: Competing benchmark implementations in C, C++, Go, and Rust must implement identical algorithms, use identical data structures, and use equivalent buffering/allocation strategies.
2. **Release Optimizations**: All implementations must be compiled with their respective standard production flags (e.g., `gcc -O3 -flto`, `clang -O3 -flto`, `go build`, `cargo build --release`).
3. **Disclosed Hardware & Environment**: Benchmarks must record and publish the exact CPU model, core count, RAM, OS kernel version, compiler versions, and temperature/throttling state.
4. **Statistical Rigor**: Measurements must include warm-up iterations, multiple sample runs (minimum 30 iterations), and report median, interquartile range (IQR), and 99th percentile latency rather than single best-run figures.
5. **Correctness Verification**: Every benchmark run must verify the computational correctness of its output before recording timing data. Fast incorrect programs are recorded as failures.
6. **Public Raw Data**: All benchmark source code, compilation commands, and raw JSON logs must be published in the public `benchmarks/` directory.

# WON Public Benchmarking Protocol & Integrity Rules

> **Document Status**: `SPECIFIED`  
> **Author**: KitiLab Engineering  
> **Last Updated**: Phase 0A

---

## 1. Benchmarking Integrity Mandate

Performance benchmarks in the programming language ecosystem are frequently criticized for cherry-picked workloads, unoptimized competitor baselines, and misleading claims.

WON adheres to an uncompromising integrity standard:

> **We will never publish unverified, synthetic, or fabricated benchmark results. Every reported number must be reproducible by independent third parties using public code, standard hardware, and disclosed configurations.**

---

## 2. Competitor Matrix

All official benchmarks must compare WON against equivalent, idiomatic implementations in established systems languages:

| Language | Compiler / Toolchain | Optimization Flags |
| :--- | :--- | :--- |
| **C** | GCC 16+ / Clang 23+ | `-O3 -flto -march=native` |
| **C++** | G++ 16+ / Clang++ 23+ | `-O3 -flto -march=native` |
| **Rust** | rustc (stable) | `--release` (opt-level=3, LTO=fat, codegen-units=1) |
| **Go** | go build | Standard release build |
| **Zig** | zig build-exe | `-O ReleaseFast` |

---

## 3. Workload Categories

Benchmarks are organized into five distinct workload domains:

1. **CPU & Algorithmic Compute**:
   - Cryptographic hashing (SHA-256, BLAKE3).
   - Compression & decompression (zlib / deflate).
   - Matrix multiplication and numerical kernels.
2. **Memory Allocation & Access Patterns**:
   - Dense array transformations and cache-line stride access.
   - Arena allocation vs dynamic heap allocation.
   - High-throughput binary tree traversal.
3. **I/O & Network Throughput**:
   - High-volume buffered file I/O.
   - Socket ping-pong and HTTP parsing throughput.
4. **Startup & Binary Footprint**:
   - Time to execute a minimal "Hello World" executable (`sub-millisecond` target).
   - Final stripped binary size of statically linked executables.
   - Peak RSS memory consumption.
5. **Compilation Throughput**:
   - Lines of code compiled per second (cold vs incremental builds).

---

## 4. Execution Protocol & Statistical Rigor

All benchmark runs must comply with the following testing protocol:

1. **Workload Equivalence**: Algorithms and data structures must be mathematically identical across all languages.
2. **Correctness Verification**: The benchmark runner must assert the correct checksum or result before recording timing metrics. Failed assertions invalidate the run.
3. **Warm-Up Phase**: A minimum of 5 warm-up iterations must execute before recording metrics to ensure CPU cache and branch predictors stabilize.
4. **Sample Count**: A minimum of 30 independent measurement iterations must be collected per workload.
5. **Reported Statistics**:
   - **Median**: Primary reported central tendency metric (resists outliers).
   - **Interquartile Range (IQR)**: Measure of statistical dispersion.
   - **99th Percentile (p99)**: Tail latency indicator.
   - Mean and standard deviation recorded as secondary metrics.
6. **Hardware Disclosure**: Every benchmark artifact must record:
   - CPU model, architecture, frequency, and cache sizes.
   - System memory capacity and speed.
   - Operating system kernel version and CPU governor settings (e.g. `performance`).
   - Compiler versions and exact compilation command lines.

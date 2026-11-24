# WON Vision & Technical Mission

> **Document Status**: `SPECIFIED`  
> **Author**: KitiLab Engineering  
> **Last Updated**: Phase 0A

---

## 1. Executive Summary

**WON** is an independent, high-performance compiled programming language designed to unite the speed, low overhead, and direct machine access of C with modern memory safety, frictionless C library integration, and native ergonomics for artificial intelligence coding agents.

The project is governed by **KitiLab** under the defining philosophy:

> **"One tool. One file. Any C library. Built for AI agents."**

WON is neither a transpile-only dialect nor a virtual machine script. It is an independent language with its own grammar, intermediate representation, type checker, ownership-lite semantics, runtime, and native code generation pipeline.

---

## 2. Why WON Exists: The Systems Programming Dilemma

Modern systems software faces a compounding set of tensions across four axes:

1. **The Legacy vs Safety Trade-Off**:
   - **C / C++** offer raw execution performance, unmatched hardware compatibility, and trillions of lines of mature libraries, but suffer from endemic spatial and temporal memory safety vulnerabilities.
   - **Rust** provides formidable compile-time memory safety guarantees, but introduces steep cognitive friction around lifetime annotations (`'a`), high compilation latency, complex syntax, and difficult agent autocompletion.
2. **The FFI Tax & Wrapper Fatigue**:
   - Modern languages claim "C interoperability," yet in practice require hand-written binding layers, complex C FFI headers, build-system bridges (CMake/Cargo/Meson), or foreign runtime marshaling overhead.
3. **The Software Agent Revolution**:
   - Today's programming languages were engineered exclusively for human visual terminals. Modern AI coding agents struggle with human-oriented compiler errors, non-deterministic build cascades, opaque macro expansions, and scattered build metadata.
4. **Toolchain Bloat & Deployment Complexity**:
   - Developers frequently navigate gigabytes of SDK dependencies, dynamic linker path mismatches (`LD_LIBRARY_PATH`), complex containerization, and slow cold-start container spin-ups for what should be a single native binary.

WON was conceived to resolve these trade-offs by providing an uncompromising native language engineered from day one for modern compilers, direct C library consumption, and programmatic AI manipulation.

---

## 3. Target Audience & Core Use Cases

WON is optimized for developers and automated systems operating in high-demand environments:

- **Systems & Infrastructure Engineers**: Building ultra-low latency network services, storage engines, database kernels, and CLI tools requiring deterministic latency, zero garbage collection pauses, and minimal memory footprints.
- **AI Agent Orchestrators**: Automated workflows where language models autonomously write, compile, diagnose, and repair high-performance native code through structured machine-readable compiler outputs.
- **Embedded & Edge Systems Developers**: Engineers who require direct access to native C drivers, micro-controllers, or platform SDKs without carrying runtime VMs or heavy runtimes.
- **High-Performance Microservices**: Organizations replacing memory-heavy interpreted or JIT-compiled microservices with lightweight, single-file statically linked executables that boot in sub-millisecond times.

---

## 4. Long-Term Architectural Goals

Over its multi-phase evolution, WON targets the following milestones:

1. **Predictable Native Performance**: Match optimized C (-O3) across standard benchmarks, outperforming C in select memory-allocation and concurrency workloads.
2. **Accessible Memory Safety ("Ownership-Lite")**: Provide compile-time elimination of use-after-free, double-free, and data races without forcing developers or agents to construct complex lifetime annotation graphs.
3. **Universal Header-First C Interoperability**: Enable developers to `#import <zlib.h>` or `#import <sqlite3.h>` directly, generating type-safe, zero-cost native call sites without writing intermediate C stubs.
4. **Provider-Agnostic AI Integration**: The compiler serves as an interactive semantic server, exposing JSON diagnostics, typed AST inspection, and deterministic fix suggestions over standard streams.
5. **Single-File Static Distribution**: Produce fully self-contained native binaries with zero external dynamic library dependencies for any target POSIX or Windows architecture.

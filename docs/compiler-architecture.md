# WON Compiler Architecture

> **Document Status**: `SPECIFIED`  
> **Author**: KitiLab Engineering  
> **Last Updated**: Phase 0A

---

## 1. Architectural Overview & Pipeline

The WON compiler (`wonc`) employs a multi-stage, modular architecture designed for high compilation throughput, actionable error diagnostics, and native code generation:

```
┌─────────────────────────────────────────────────────────────┐
│                    WON Source Code (.won)                   │
└──────────────────────────────┬──────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                   Stage 1: Lexical Analysis                 │
│         - Token stream with precise SourceSpans             │
└──────────────────────────────┬──────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                Stage 2: Parsing & AST Construction          │
│         - Abstract Syntax Tree with recovery mechanisms     │
└──────────────────────────────┬──────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────┐
│           Stage 3: Type Checking & Type Inference           │
│         - Bidirectional local inference                     │
│         - Trait resolution & generic constraint validation  │
└──────────────────────────────┬──────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────┐
│     Stage 4: Typed Intermediate Representation (TIR)        │
│         - Control Flow Graph (CFG)                          │
│         - Ownership-lite borrow verification                │
│         - Move semantics & drop-flag insertion              │
└──────────────────────────────┬──────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                Stage 5: High-Level Optimizations            │
│         - Dead code elimination                             │
│         - Constant folding & monomorphization               │
│         - Inline expansion heuristics                       │
└──────────────────────────────┬──────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────┐
│               Stage 6: C Code Generation (AOT)              │
│         - Standard ISO C99/C11 generation                   │
│         - Clean mapping to C functions, structs, and types  │
└──────────────────────────────┬──────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────┐
│              Stage 7: Native Toolchain Invocation           │
│         - Clang / GCC invocation with -O3 and -flto         │
│         - Static linking against musl / glibc               │
└──────────────────────────────┬──────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────┐
│              Statically Linked Native Executable            │
└─────────────────────────────────────────────────────────────┘
```

---

## 2. Typed Intermediate Representation (TIR)

TIR is the central semantic foundation of the WON compiler. It serves three vital roles:

1. **Explicit Memory Semantics**: TIR represents every allocation, variable move, borrow initiation, and resource drop as explicit low-level primitives.
2. **Control Flow Graph (CFG)**: Basic blocks with explicit branching, phi nodes, or scoped variables enable deterministic data-flow analysis.
3. **Target Independence**: TIR isolates the frontend syntax and type system from the final code generation target.

---

## 3. Rationale for the Initial C Code Generation Backend

WON initially adopts a standard C code generation backend rather than generating LLVM IR or machine code directly. This is an intentional architectural strategy (formalized in [ADR-001](decisions/ADR-001-c-codegen-backend.md)):

- **Access to Decades of Optimizer Engineering**: GCC and Clang contain over three decades of sophisticated vectorization, loop unrolling, register allocation, and target-specific optimizations.
- **Universal Portability**: Standard C runs on every CPU architecture (x86_64, aarch64, RISC-V, WebAssembly, and embedded microcontrollers).
- **Direct C Interoperability Validation**: Emitting C makes validating C header interop, struct alignments, and FFI boundaries transparent and debuggable.
- **Rapid Semantic Iteration**: Enables the team to refine the language syntax, type checker, and ownership model without getting bogged down in low-level ABI calling conventions or LLVM API breaking changes.

---

## 4. Future Backend Evaluation

While the C backend serves as the permanent baseline and portable reference implementation, the compiler architecture is strictly decoupled to permit alternative backends in later phases:

- **Dedicated LLVM Backend**: Direct LLVM IR generation for tighter compiler control, faster single-pass compilation, and advanced JIT or debug symbol integration.
- **QBE Backend**: A lightweight, fast C compiler backend offering near-instant compilation speeds for development workflows and fast test loops.
- **Cranelift Backend**: Fast native code generator with sub-second compilation latency, suitable for rapid edit-compile-test cycles.

---

## 5. Library-First Architecture & Tooling

The compiler is organized as a reusable library (`libwonc`) with a thin CLI wrapper (`won`):
- All compiler phases can be invoked programmatically.
- Diagnostics, AST queries, and symbol resolution are exposed via standard programmatic APIs and JSON streams for IDEs and AI coding agents.

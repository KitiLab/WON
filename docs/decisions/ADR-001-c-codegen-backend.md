# ADR-001: Initial C Code-Generation Backend

## Metadata
- **Decision ID**: ADR-001
- **Title**: Initial C Code-Generation Backend
- **Status**: ACCEPTED
- **Date**: 2026-11-24
- **Author**: KitiLab Engineering

## Context
When bootstrapping a new compiled systems programming language, deciding on the initial code generation backend is one of the most critical architectural decisions. The options range from generating native machine code directly, targeting LLVM IR, generating C source code, or targeting lightweight backends like QBE or Cranelift.

The key requirements for WON in Phase 0 and Phase 1 are:
1. Rapidly validate language syntax, typing rules, and ownership semantics.
2. Access world-class optimization pipelines (vectorization, register allocation, inlining).
3. Validate seamless C header interoperability directly against native platform ABIs.
4. Minimize initial toolchain dependency bloat.

## Considered Alternatives
1. **Direct LLVM IR Generation**:
   - *Pros*: Direct access to LLVM optimization passes; fine-grained control over debug info (DWARF).
   - *Cons*: Heavy compilation dependency; massive C++ API surface prone to breaking changes; significant development overhead before achieving a runnable "Hello World".
2. **Direct Machine Code Emission (x86_64/AArch64)**:
   - *Pros*: Fast compilation; zero external dependencies.
   - *Cons*: Extremely high engineering burden; no advanced optimization passes; non-portable across architectures.
3. **QBE / Cranelift Backend**:
   - *Pros*: Fast compilation speeds; clean intermediate representations.
   - *Cons*: Smaller ecosystem; less mature optimization passes than GCC/Clang; fewer target architectures.
4. **Standard C Code Generation (Selected)**:
   - *Pros*: Immediately leverages mature GCC and Clang optimizers (`-O3`, `-flto`); completely portable to any architecture with a C compiler; makes C header interop transparent and inspectable; human-readable target output eases compiler debugging.
   - *Cons*: Dependent on an external C compiler toolchain; compilation latency includes C compiler pass.

## Decision
**WON will utilize standard ISO C99/C11 code generation as its initial backend.**

The compiler will translate the Typed Intermediate Representation (TIR) into clean, standard C source code, which is then compiled to native binaries using the host's existing GCC or Clang toolchain.

## Consequences
- **Positive**: Enables rapid bootstrapping and early validation of language semantics; provides instant `-O3` performance matching C; simplifies cross-compilation.
- **Negative**: Compilation speed is bounded by GCC/Clang invocation times; debugging requires mapping line numbers between WON source and generated C.
- **Impact on Pillars**:
  - *Performance*: Directly achieves parity with native C via GCC/Clang `-O3`.
  - *Memory Safety*: Verified in TIR before C emission; C output uses safe primitive mappings.
  - *C Interoperability*: Native C ABI compatibility is intrinsic.
  - *Simple Deployment*: Produces standalone statically linked native executables.

## Open Questions & Future Milestones
- Investigation of dedicated LLVM and QBE backends in Phase 7 to accelerate development compilation cycles.

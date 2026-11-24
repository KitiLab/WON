# ADR-009: Typed Intermediate Representation (TIR) Architecture

## Metadata
- **Decision ID**: ADR-009
- **Title**: Typed Intermediate Representation (TIR) Architecture
- **Status**: ACCEPTED (KitiLab Owner Approval: November 24, 2026 — see [OWNER-APPROVAL-2026-11-24.md](OWNER-APPROVAL-2026-11-24.md))
- **Date**: 2026-11-24
- **Author**: KitiLab Engineering

## Context
Phase 0 lowered the AST directly to C source code. While effective for a proof of concept, direct AST-to-C translation couples the language frontend to C code generation, inhibits inter-procedural optimization, prevents monomorphization deduplication, and locks the compiler into a single backend. Phase 1 must establish a language-independent intermediate representation.

## Considered Alternatives
1. **Direct AST to C Codegen (Phase 0 Approach)**:
   - *Rejected for Production*: Cannot support multiple backends (LLVM/QBE); makes lifetime checking and monomorphization fragile.
2. **Standard LLVM IR as Primary Intermediate Representation**:
   - *Rejected for Core Frontend*: LLVM IR is too low-level; drops high-level type semantics, ownership moves, and borrow regions.
3. **Dedicated High-Level Typed Intermediate Representation (TIR) (Selected / Recommended)**:
   - Formulates code as a Control Flow Graph (CFG) of basic blocks with explicit types, values, and memory operations.
   - Explicitly models ownership transfers (`Move`), borrow regions, and destructor invocations (`Drop`).
   - Serves as the universal input for all code generation backends (`C11Emitter`, `LlvmEmitter`, `QbeEmitter`).

## Decision
**WON adopts a dedicated, language-independent Typed Intermediate Representation (TIR) structured as basic-block CFGs with explicit type and resource lifecycle semantics.**

## Consequences
- **Positive**: Clean separation between language frontend and code generation backends; enables rigorous optimization passes (DCE, bounds check elimination, inlining).
- **Negative**: Adds a translation pass between AST and codegen.
- **Owner Approval Clarifications (2026-11-24)**:
  - The C11 backend remains the initial code-generation target; LLVM and QBE remain future backend evaluations.
  - TIR details may evolve during implementation; no claim of completed TIR implementation is permitted prior to Phase 2 verification.
  - TIR implementation is out of scope for Phase 1B.

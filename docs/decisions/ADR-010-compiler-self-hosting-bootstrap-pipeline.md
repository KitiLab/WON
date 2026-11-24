# ADR-010: Compiler Self-Hosting and Multi-Stage Bootstrap Pipeline

## Metadata
- **Decision ID**: ADR-010
- **Title**: Compiler Self-Hosting and Multi-Stage Bootstrap Pipeline
- **Status**: PROPOSED (Requires KitiLab Owner Approval)
- **Date**: 2026-11-24
- **Author**: KitiLab Engineering

## Context
Per ADR-002, Go was selected as the temporary bootstrap language for Phase 0. However, the permanent architectural vision requires WON to be completely self-hosting. We must define the formal stages and objective criteria for implementing the compiler in WON and retiring Go.

## Considered Alternatives
1. **Permanent Go Implementation**:
   - *Rejected*: Violates core project identity; keeps Go as a mandatory external dependency.
2. **Immediate Rewrite in C/Rust**:
   - *Rejected*: Wastes engineering effort rewriting the compiler in another third-party language rather than WON.
3. **Staged Self-Hosting via Four Verified Bootstrap Stages (Selected / Recommended)**:
   - Stage 0 (`wonc0`): Existing Go bootstrap compiler.
   - Stage 1 (`wonc1`): New compiler written in WON, compiled by `wonc0`.
   - Stage 2 (`wonc2`): Self-compilation (`wonc1` compiles compiler source).
   - Stage 3: Convergence and reproducible output verification.

## Decision
**WON adopts a four-stage self-hosting roadmap, retaining the Go bootstrap compiler until 10 objective retirement criteria are satisfied.** The self-hosted compiler will initially continue emitting C, separating Go retirement from GCC/Clang retirement.

## Consequences
- **Positive**: Delivers true language autonomy; dogfoods WON for large-scale systems software; removes Go from the final toolchain.
- **Negative**: Requires maintaining the Go bootstrap compiler until self-hosting convergence is proven.
- **Owner Approval**: REQUIRED before commencing Phase 2 compiler implementation.

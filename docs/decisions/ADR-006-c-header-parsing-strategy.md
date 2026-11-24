# ADR-006: C Header Parsing Strategy

## Metadata
- **Decision ID**: ADR-006
- **Title**: C Header Parsing Strategy Evaluation
- **Status**: PROPOSED / UNDER EVALUATION
- **Date**: 2026-11-24
- **Author**: KitiLab Engineering

## Context
WON's capability to import C libraries directly (`#import <zlib.h>`) requires parsing C header files to extract macro definitions, struct layouts, typedefs, and function signatures. C headers feature complex macro expansions, platform-specific compiler builtins (`__attribute__`), and preprocessor directives.

Selecting the right parsing approach determines compiler dependencies, compilation latency, and parsing accuracy.

## Considered Candidates

### 1. Embedded libclang (LLVM)
- **Pros**: 100% standard C/C++ compliance; flawlessly handles complex macros, GCC extensions, and architecture-specific type sizes.
- **Cons**: Substantial dynamic library dependency (`libclang.so`); slower cold-start parsing times; cross-compilation complexity.

### 2. Lightweight Custom C Header Parser
- **Pros**: Zero external dependencies; extremely fast parsing speeds; easily embedded directly in the WON compiler binary; straightforward for AI agents to audit.
- **Cons**: Difficult to achieve 100% GCC/Clang header macro compatibility across arbitrary glibc system headers.

### 3. Tree-Sitter C Grammar
- **Pros**: Fast, robust incremental parsing; well-tested C grammar.
- **Cons**: Does not perform macro preprocessor expansion or semantic type resolution natively; requires a separate preprocessor pass.

### 4. Native Preprocessed Header Analysis (Hybrid)
- **Pros**: Invokes host C compiler in preprocessor-only mode (`clang -E` or `gcc -E`) to expand macros, then parses the clean preprocessed tokens using a focused C parser.
- **Cons**: Relies on host C preprocessor.

## Current Recommendation
For the **Phase 0B proof-of-concept**, we will evaluate a targeted, focused header extraction for `zlib.h` (which has a clean, standard header). The full evaluation between libclang and the hybrid preprocessor strategy will be completed before Phase 1.

## Decision
**NOT YET DECIDED.**
Final architectural decision will be made following Phase 0B empirical validation.

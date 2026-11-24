# ADR-006: C Header Parsing Strategy

## Metadata
- **Decision ID**: ADR-006
- **Title**: C Header Parsing Strategy for Phase 0B
- **Status**: ACCEPTED (Phase 0B Implementation)
- **Date**: 2026-11-24
- **Author**: KitiLab Engineering

## Context
WON requires automatic discovery of C declarations directly from system headers (`#import <zlib.h>`). The parser must handle complex preprocessor macros, type definitions (`Bytef`, `uLong`), and target platform ABIs without fragile regular expressions or hardcoded mock signatures.

## Considered Alternatives
1. **Regular Expression / Heuristic Text Parsing**:
   - *Rejected*: Inaccurate, fails on multiline declarations, cannot resolve typedefs, breaks on macro attributes.
2. **Custom Complete C Preprocessor and Parser**:
   - *Rejected for Phase 0B*: Prohibitive complexity for a vertical slice; high risk of non-standard behavior on glibc/system headers.
3. **Clang Semantic JSON AST Dump (`clang -Xclang -ast-dump=json`) (Selected)**:
   - Leverages the host's existing, verified Clang installation (`clang version 23.1.5`).
   - Produces 100% compliant, fully expanded semantic ASTs containing exact return types, parameter types, qualifiers (`const`), and canonical typedef resolutions.
   - Outputs machine-readable JSON directly inspectable by Go.

## Decision
**WON adopts the Clang Semantic AST strategy for Phase 0B C header binding generation.**

The generator invokes `clang -Xclang -ast-dump=json -fsyntax-only <header.h>`, extracts matching `FunctionDecl` and `TypedefDecl` nodes, and emits verified binding metadata with header SHA-256 and target triple provenance.

## Consequences
- **Positive**: Absolute ABI precision; zero manual binding boilerplate; automatic typedef resolution (`Bytef` -> `unsigned char`, `uLong` -> `unsigned long`).
- **Negative**: Requires Clang installed on the compilation host.
- **Impact on Pillars**:
  - *C Interoperability*: Delivers true, automated "Any C library" header-first integration.

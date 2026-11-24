# ADR-006: C Header Parsing Strategy

## Metadata
- **Decision ID**: ADR-006
- **Title**: C Header Parsing Strategy for Phase 0B & Phase 0B.1
- **Status**: ACCEPTED (Phase 0B.1 Verified)
- **Date**: 2026-11-24
- **Author**: KitiLab Engineering

## Context
WON requires automatic discovery of C declarations directly from system headers (`#import <zlib.h>`). The parser must handle complex preprocessor macros, type definitions (`Bytef`, `uLong`), and target platform ABIs without fragile regular expressions or hardcoded mock signatures.

Furthermore, in Phase 0B.1, the binding metadata must participate directly in the semantic checker and code generator pipelines, while avoiding redundant Clang AST executions when headers have not changed.

## Considered Alternatives
1. **Regular Expression / Heuristic Text Parsing**:
   - *Rejected*: Inaccurate, fails on multiline declarations, cannot resolve typedefs, breaks on macro attributes.
2. **Custom Complete C Preprocessor and Parser**:
   - *Rejected for Phase 0B*: Prohibitive complexity for a vertical slice; high risk of non-standard behavior on glibc/system headers.
3. **Clang Semantic JSON AST Dump (`clang -Xclang -ast-dump=json`) with Fingerprint Caching (Selected)**:
   - Leverages the host's existing, verified Clang installation (`clang version 23.1.5`).
   - Produces 100% compliant, fully expanded semantic ASTs containing exact return types, parameter types, qualifiers (`const`), and canonical typedef resolutions.
   - Outputs machine-readable JSON directly inspectable by Go.
   - Caches parsed metadata keyed by the SHA-256 fingerprint of the header file, ensuring fast incremental builds and automatic invalidation when headers change.

## Decision
**WON adopts the Clang Semantic AST strategy with SHA-256 header fingerprint caching for C header binding generation.**

The generator computes the SHA-256 hash of `/usr/include/zlib.h`, checks for a valid matching cache file in `bindings/zlib/generated/zlib_binding.json`, or invokes `clang -Xclang -ast-dump=json -fsyntax-only <header.h>`, extracts matching `FunctionDecl` and `TypedefDecl` nodes, and emits verified binding metadata with target triple provenance.

This metadata is injected directly into `checker.New(report, bindings)` to enforce symbol resolution (`WON-FFI-002`) and argument counts (`WON-FFI-003`), as well as `codegen.New(prog, bindings)` to infer accurate C return types.

## Consequences
- **Positive**: Absolute ABI precision; zero manual binding boilerplate; automatic typedef resolution (`Bytef` -> `unsigned char`, `uLong` -> `unsigned long`); fast incremental builds via SHA-256 cache verification.
- **Negative**: Requires Clang installed on the compilation host.
- **Impact on Pillars**:
  - *C Interoperability*: Delivers true, automated "Any C library" header-first integration directly verified in compiler semantic analysis.

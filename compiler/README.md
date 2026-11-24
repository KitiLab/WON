# Compiler Subsystem

## Overview
This directory contains the core WON compiler pipeline.

- **Current Status**: `PLANNED` / `UNDER INVESTIGATION` (Implementation begins in Phase 0B proof-of-concept and Phase 1 formalization).
- **Architecture**: Multi-pass compiler pipeline emitting C99/C11 code for native compilation via GCC/Clang.
- **Reference**: See [`../docs/compiler-architecture.md`](../docs/compiler-architecture.md).

## Submodules (Planned)
- `lexer/`: Tokenization and source span tracking.
- `parser/`: AST construction and grammar rules.
- `types/`: Type checker and local bidirectional type inference.
- `tir/`: Typed Intermediate Representation with explicit ownership semantics.
- `codegen/`: C code generator targeting ISO C.
- `diagnostics/`: Structured error emission and AI-agent JSON reporting.

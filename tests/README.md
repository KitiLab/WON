# Test Suites

## Overview
This directory organizes test suites for compiler phases, language semantics, and integration checks.

- **Structure**:
  - `syntax/`: Lexer and parser conformance tests.
  - `types/`: Type checker and inference tests.
  - `ownership/`: Borrow checker and move semantics positive/negative tests.
  - `codegen/`: C generation and execution verification.
  - `ffi/`: C library linking and foreign function call tests.
  - `agent/`: AI agent JSON diagnostics and automated repair validation tests.

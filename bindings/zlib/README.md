# Zlib C Bindings Subsystem

> **Status**: `IMPLEMENTED` (Phase 0B & Phase 0B.1 Verified)  
> **Source Header**: `/usr/include/zlib.h`  
> **Target ABI**: `x86_64-redhat-linux-gnu`

---

## Overview

WON avoids manual binding stubs or fragile regular expression parsers. Instead, the compiler invokes Clang's semantic JSON AST dump (`clang -Xclang -ast-dump=json -fsyntax-only`) to inspect the real host C header and extract verified declarations.

## Pipeline Integration (Phase 0B.1)

In Phase 0B.1, the binding subsystem is directly integrated into the compiler pipeline:
1. When a source file declares `#import <zlib.h>`, `wonc` invokes `GetOrExtractZlibBindings`.
2. The current SHA-256 fingerprint of the header file is computed and compared against cached metadata.
3. If the cache is missing, corrupted, or possesses a mismatched SHA-256 hash, Clang is automatically re-invoked to regenerate the binding.
4. The extracted symbol table and type signatures are injected into the semantic checker (`checker.New`) and code generator (`codegen.New`).
5. Calls to foreign functions (`zlib::compress`) are verified against the extracted parameter count, types, and return values, rejecting nonexistent symbols (`WON-FFI-002`) or mismatched arity (`WON-FFI-003`).

## Generated Artifacts

- `generated/zlib_binding.json`: Deterministic JSON metadata detailing extracted C function prototypes, parameter types, return types, typedef mappings, and provenance.

### Extracted Declarations
1. `compress`: `int (unsigned char *, unsigned long *, const unsigned char *, unsigned long)`
2. `uncompress`: `int (unsigned char *, unsigned long *, const unsigned char *, unsigned long)`
3. `compressBound`: `unsigned long (unsigned long)`
4. `zlibVersion`: `const char *(void)`

### Extracted Typedef Mappings
- `Byte` ➔ `unsigned char`
- `Bytef` ➔ `Byte` (`unsigned char`)
- `uInt` ➔ `unsigned int`
- `uLong` ➔ `unsigned long`
- `uLongf` ➔ `uLong` (`unsigned long`)

## Provenance
- **Header SHA-256**: `8bc935aeb8909d05fe58f9286a0fe2c674a41638c374786ee888485ab5ff4409`
- **Compiler**: `clang version 23.1.5 (Fedora 23.1.5-2.fc45)`

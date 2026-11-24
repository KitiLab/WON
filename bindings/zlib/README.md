# Zlib C Bindings Subsystem

> **Status**: `IMPLEMENTED` (Phase 0B Proof of Concept)  
> **Source Header**: `/usr/include/zlib.h`  
> **Target ABI**: `x86_64-redhat-linux-gnu`

---

## Overview

WON avoids manual binding stubs or fragile regular expression parsers. Instead, the compiler invokes Clang's semantic JSON AST dump (`clang -Xclang -ast-dump=json -fsyntax-only`) to inspect the real host C header and extract verified declarations.

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

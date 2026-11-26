# WON Language Specification: C Interoperability and Unsafe Boundaries

> **Specification Version**: Draft 0.1  
> **Status**: `SPECIFIED` (`#import` / `WON-SEC-001` & Typed Sidecar Contract Model `FFIContractCatalog` `IMPLEMENTED` in Phase 2B.3; Textual `.wonbind` Parser Syntax Remains `SPECIFIED` / Proposed)  
> **Module**: Native C FFI, Header Processing & Unsafe Model  
> **Date**: November 2026

---

## 1. Architectural Mission

The founding mandate of WON is:
> *"One tool. One file. Any C library. Built for AI agents."*

Unlike languages that require intricate manual binding definitions (Cgo, Rust `bindgen`, Python `ctypes`), WON treats the C language and system C headers as first-class input artifacts. Developers and automated AI coding agents can import an existing C header directly in source code and immediately invoke its functions with full compile-time semantic checking.

---

## 2. The Header Import Model (`#import`)

System C headers are consumed using the `#import` directive:

```won
#import <zlib.h>
#import "sqlite3.h"
```

### 2.1 Resolution and Ingestion Pipeline
1. **Search Paths**: The compiler searches standard system include directories (`/usr/include`, `/usr/local/include`) or directories specified via `-I <path>` compiler flags.
2. **Clang Semantic AST Extraction**: The compiler extracts C declarations via Clang's semantic AST dump (`clang -Xclang -ast-dump=json -fsyntax-only`). This extracts complete function prototypes, canonical typedef resolutions (`Bytef` -> `unsigned char`), struct layouts, enum constants, and parameter qualifiers (`const`).
3. **Composite Dependency Fingerprinting (ADR-006)**:
   To ensure instantaneous compilation without redundant header re-parsing, the compiler tracks a composite SHA-256 fingerprint:
   $$\text{Fingerprint} = \text{SHA256}\Big(\text{Hash}(H_{\text{primary}}) \parallel \sum_{i} \text{Hash}(H_{\text{transitive}, i}) \parallel \text{TargetTriple} \parallel \text{ClangVer}\Big)$$
   Transitive dependencies are resolved via `clang -MM`. If any transitive header (such as `zconf.h`) or compiler target changes, the cache is invalidated and regenerated automatically.

---

## 3. Target-ABI-Aware C Type Mapping

> **ABI Provenance Requirement**: C type mappings and data alignments are **NOT hardcoded assumptions**. They are dynamically extracted from the active Clang target triple (`clang -dumpmachine`) and the semantic AST data layout string for the active compilation target.

The table below summarizes standard mappings for standard x86_64 target ABIs (LP64 for Linux/macOS vs. LLP64 for Windows):

| C Data Type | Linux / macOS x86_64 (LP64) | Windows x64 (LLP64) | Notes / Invariants |
| :--- | :--- | :--- | :--- |
| `bool`, `_Bool` | `bool` | `bool` | 1 byte |
| `char` | `i8` or `u8` | `i8` or `u8` | Target architecture signedness dependent |
| `signed char` | `i8` | `i8` | Exactly 8-bit signed |
| `unsigned char` | `u8` | `u8` | Exactly 8-bit unsigned |
| `short` | `i16` | `i16` | 16-bit signed |
| `unsigned short` | `u16` | `u16` | 16-bit unsigned |
| `int` | `i32` | `i32` | 32-bit signed |
| `unsigned int` | `u32` | `u32` | 32-bit unsigned |
| `long` | **`i64`** | **`i32`** | **Target ABI Sensitive (LP64 vs LLP64)** |
| `unsigned long` | **`u64`** | **`u32`** | **Target ABI Sensitive (LP64 vs LLP64)** |
| `long long` | `i64` | `i64` | 64-bit signed |
| `unsigned long long`| `u64` | `u64` | 64-bit unsigned |
| `size_t` | `usize` | `usize` | Pointer-sized unsigned word |
| `ssize_t`, `ptrdiff_t`| `isize` | `isize` | Pointer-sized signed word |
| `float` | `f32` | `f32` | IEEE 754 single precision |
| `double` | `f64` | `f64` | IEEE 754 double precision |
| `void*` | `*mut unit` | `*mut unit` | Raw untyped mutable pointer |
| `const void*` | `*const unit`| `*const unit`| Raw untyped immutable pointer |
| `T*` | `*mut T` | `*mut T` | Mutable raw pointer |
| `const T*` | `*const T` | `*const T` | Immutable raw pointer |
| `const char*` | `*const u8` | `*const u8` | C null-terminated byte sequence |

---

## 4. Ownership Semantics and Sidecar Binding Contracts

C headers describe data types and function prototypes, but **do not encode memory ownership semantics**. A raw C signature cannot distinguish whether a returned pointer must be freed with `free()`, whether it borrows static memory, or whether an input pointer is retained inside an internal data structure.

### 4.1 Default Safety Rule: Raw Pointers are Unmanaged
By default, all pointers passed to or returned from imported C functions are treated as unmanaged raw pointers (`*const T` or `*mut T`). The compiler does not track their lifetimes, cannot verify nullability, and does not invoke destructors upon them.

### 4.2 Extensible Safe Binding Contracts (`.wonbind`)
To generate safe, idiomatic WON wrappers around raw C libraries without modifying external C headers, WON specifies an extensible **Sidecar Binding Contract** format (`.wonbind` files or manifest contract directives):

```won
# Sidecar binding contract for zlib compression: bindings/zlib/zlib.wonbind
@bind(header: "zlib.h", package: "zlib")
contract:
    fn compressBound(sourceLen: uLong) -> uLong:
        @pure

    fn compress(
        dest: *mut Bytef,
        destLen: *mut uLongf,
        source: *const Bytef,
        sourceLen: uLong
    ) -> i32:
        @out_buffer(target: dest, length_in_out: destLen, capacity_assert: compressBound(sourceLen))
        @borrow(param: source, length: sourceLen)

    fn deflateInit_(strm: *mut z_stream, level: i32, version: *const u8, stream_size: i32) -> i32:
        @state_initializer(strm, destructor: "deflateEnd")
```

#### Supported Contract Declarations:
1. `@owned(destructor = "c_free")`: Returned pointer ownership is transferred to the caller; WON generates an RAII wrapper invoking the specified release function upon drop.
2. `@borrow(param = N)`: Returned pointer borrows from input parameter $N$.
3. `@out_buffer(target, length_param, capacity_assert)`: Enforces buffer capacity verification prior to call execution.
4. `@nullability(param = N, state = "non_null" | "nullable")`: Enforces compile-time null checks or maps to `Option[*mut T]`.
5. `@retained`: Alerts the ownership checker that C retains a pointer beyond the call, preventing local stack deallocation.
6. `@callback_lifetime("call" | "persistent")`: Binds callback function pointer lifetimes to the containing call or to an explicit deregistration handle.

### 4.3 Division of Responsibilities
- `@repr(C)`: Governs **data layout and alignment** matching the host C compiler.
- `extern "C"`: Governs **calling conventions and symbol mangling**.
- `unsafe:`: Mandatory boundary for all raw FFI invocations that lack certified safe contracts. Certified wrappers generated via `.wonbind` contracts expose safe WON functions.

---

## 5. Unsafe Boundary Classification

Foreign C code is outside the reach of WON's type safety and ownership invariants. Consequently:

1. **Mandatory `unsafe:` Enforcement (`WON-SEC-001`)**:
   Calling any function declared in an imported C header **MUST** occur inside an explicit `unsafe:` block. The compiler strictly rejects foreign calls in safe code:
   ```won
   # Compile Error: Call to foreign C function 'zlib::compress' outside unsafe block
   let status = zlib::compress(...) 
   
   # Valid: Explicit safety boundary
   let status = unsafe:
       zlib::compress(...)
   ```

2. **Foreign Function Pointers & Callbacks**:
   When passing WON functions as callbacks to C libraries, the function must be declared with C calling conventions:
   ```won
   extern "C" fn my_comparator(a: *const unit, b: *const unit) -> i32:
       # Callback implementation
       0
   ```

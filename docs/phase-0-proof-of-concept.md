# WON Phase 0B Implementation Specification & Acceptance Report

> **Milestone**: Phase 0B & Phase 0B.1 (Proof of Concept Vertical Slice & Acceptance Remediation)  
> **Status**: `IMPLEMENTED` / All Phase 0B & 0B.1 Acceptance Remediation Gates Passed  
> **Author**: KitiLab Engineering  
> **Repository**: `https://github.com/KitiLab/WON`

---

## 1. Executive Summary & Verification of Core Claims

Phase 0B and Phase 0B.1 have successfully established and proven the first working vertical slice of the WON programming language:

1. **Claim A (Parsing)**: Verified. Real `.won` source files are parsed into a structured AST with accurate source span tracking, lexical position preservation, and error recovery.
2. **Claim B (C Interoperability & Pipeline Integration)**: Verified. Real C function prototypes and typedefs (`compress`, `uncompress`, `compressBound`, `Bytef`, `uLong`, `uLongf`) are extracted from `/usr/include/zlib.h` using Clang's semantic JSON AST. Binding metadata participates directly in semantic checking (`WON-FFI-001`, `WON-FFI-002`, `WON-FFI-003`) and type-driven code generation without hardcoded function lists.
3. **Claim C (Strict ISO C11 Codegen & Native Execution)**: Verified. The compiler emits clean ISO C11 (completely eliminating GNU statement expressions `({ ... })` in favor of standard block scoping), which compiles with zero warnings and zero errors under both `gcc -std=c11 -pedantic-errors -Wall -Wextra` and `clang -std=c11 -pedantic-errors -Wall -Wextra`.
4. **Claim D (Correctness & Safety)**: Verified. The vertical slice proves byte-for-byte correctness (`won_bytes_equal`, `memcmp`) across empty inputs, normal text, and edge cases. Buffer safety is enforced via `compressBound` capacity checks before compression.
5. **Claim E (Empirical Benchmarking)**: Verified. 30 independent measured runs of a 1 MiB payload across 50 iterations demonstrated execution parity with optimized native C under identical compiler flags (`gcc -O3`: 85.8 ms for WON vs 89.9 ms for C; `gcc -O3 -flto`: 87.6 ms for WON vs 90.3 ms for C). True isolated peak RSS is measured at 5.25 MB via `/usr/bin/time -v`.
6. **Claim F (Autonomous AI Coding-Agent Repair)**: Verified. Deliberate mutability defects (`WON-MUT-001`) produce structured JSON diagnostics with byte spans and replacement edits. An actual AI agent autonomously read the JSON, diagnosed the defect, dispatched `edit_file` to apply the surgical patch, and verified that the repaired binary compiled and ran cleanly (documented in [`docs/phase-0b-ai-agent-experiment.md`](phase-0b-ai-agent-experiment.md)).

---

## 2. Supported Language Subset in Phase 0B

The implemented bootstrap compiler (`wonc`) supports the following verified language constructs:

```won
#import <zlib.h>

fn main() -> i32 {
    let message = "WON Phase 0B Proof of Concept: Native C Interop with zlib";
    let input_bytes = message.as_bytes();
    let input_len: u64 = 57;

    // 1. 调用 compressBound 获取充足的压缩缓冲区上限容量并做边界防护
    let max_bound = unsafe {
        zlib::compressBound(input_len)
    };

    if max_bound > 256 {
        println("Buffer capacity too small for max_bound: ", max_bound);
        return 1;
    }

    let mut compressed_buf: [u8; 256] = [0; 256];
    let mut compressed_len: u64 = 256;

    // 2. 调用 zlib::compress 进行真实数据压缩
    let compress_status = unsafe {
        zlib::compress(
            compressed_buf.as_mut_ptr(),
            &mut compressed_len,
            input_bytes.as_ptr(),
            input_len
        )
    };

    if compress_status != 0 {
        return 1;
    }

    // 3. 分配解压缓冲区并调用 zlib::uncompress 解压
    let mut decompressed_buf: [u8; 256] = [0; 256];
    let mut decompressed_len: u64 = 256;

    let uncompress_status = unsafe {
        zlib::uncompress(
            decompressed_buf.as_mut_ptr(),
            &mut decompressed_len,
            compressed_buf.as_ptr(),
            compressed_len
        )
    };

    if uncompress_status != 0 {
        return 2;
    }

    // 4. 验证解压长度与逐字节严格相等
    if decompressed_len != input_len {
        return 3;
    }

    let is_equal = unsafe {
        bytes_equal(input_bytes.as_ptr(), decompressed_buf.as_ptr(), input_len)
    };
    if is_equal == 0 {
        return 4;
    }

    println("Byte-for-byte verification: PASS");
    println("Zlib round-trip verification: PASS");
    return 0;
}
```

### Syntax Specifications:
- **Header Imports**: `#import <header.h>` or `#import "header.h"`
- **Function Declarations**: `fn name(params) -> RetType { ... }`
- **Variables**: `let` (immutable) and `let mut` (mutable), with optional `: Type`
- **Control Flow**: `if / else`, `while (cond) { ... }`, `return`
- **Primitives**: `i32`, `u64`, `u8`, `&str`, fixed-size arrays `[u8; N]`
- **Unsafe Boundaries**: Mandatory `unsafe { ... }` blocks for all foreign C library calls (`WON-SEC-001`)
- **Pointers & Borrows**: `&var`, `&mut var`, `.as_ptr()`, `.as_mut_ptr()`, `.len()`
- **Built-in Functions**: `println(...)`, `bytes_equal(ptr1, ptr2, len)`

---

## 3. C ABI Correctness & Header Parsing Strategy

- **Header Inspected**: `/usr/include/zlib.h` (SHA-256: `8bc935aeb8909d05fe58f9286a0fe2c674a41638c374786ee888485ab5ff4409`)
- **Extraction Engine**: Clang semantic JSON AST (`clang -Xclang -ast-dump=json -fsyntax-only`)
- **Target ABI Triple**: `x86_64-redhat-linux-gnu`
- **Fingerprint Caching**: Header SHA-256 is tracked. Stale or tampered cache files are automatically invalidated and re-extracted.
- **Typedef Preservation**:
  - `Bytef` mapped to `unsigned char`
  - `uLong` and `uLongf` mapped to `unsigned long` (preserving 64-bit width on Linux x86_64 while maintaining C type compatibility)
- **C Header Inclusion**: Generated C explicitly emits `#include <zlib.h>`, allowing the native compiler to verify all C calling conventions.

---

## 4. Static Linking Evaluation & Environment Status

- **Status**: `BLOCKED (Environment)`
- **Analysis**: Running `./compiler/poc/bin/wonc build --static` passes `-static` to the linker. On this Fedora Linux 45 host, the linker reports:
  ```
  /usr/bin/ld: cannot find -lz: No such file or directory
  /usr/bin/ld: cannot find -lc: No such file or directory
  ```
- **Finding**: Neither `glibc-static` (`libc.a`) nor `zlib-static` (`libz.a`) is installed on the host. In compliance with governance rules, package installations or system alterations requiring sudo were not performed.
- **Diagnostics**: The `wonc` compiler explicitly diagnoses missing static archives and informs the user of the exact system packages required (`glibc-static`, `zlib-static`).
- **Dynamic Build**: The default dynamic linking build links against `/lib64/libz.so.1` and passes 100% of integration and benchmark tests.

---

## 5. Acceptance Remediation (Phase 0B.1) Verification Matrix

| Area | Prior Issue in Phase 0B | Remediation Implemented in Phase 0B.1 | Verification Status |
| :--- | :--- | :--- | :--- |
| **Pipeline Integration** | Bindings extracted to disk but not used in `runBuild` | Bindings dynamically parsed from header/cache, passed to checker (`WON-FFI-001/002/003`) and codegen | `VERIFIED` (`go test ./...`) |
| **ISO C11 Compliance** | Used GNU statement expressions `({ ... })` | Replaced with standard C11 block scopes & pre-declared return variables | `VERIFIED` (`gcc/clang -std=c11 -pedantic-errors`) |
| **Correctness & Safety** | Only verified length match; no capacity check | Added `compressBound` capacity assertion and `won_bytes_equal` / `memcmp` check | `VERIFIED` (`examples/zlib_compression.won`) |
| **Benchmark Fairness** | Mismatched compiler flags; `RUSAGE_CHILDREN` multi-process memory leak | Matched `-O3` and `-O3 -flto` flags; isolated per-process RSS via `/usr/bin/time -v` | `VERIFIED` (`run_bench.py` 30 samples) |
| **AI Agent Repair** | In-process test runner string manipulation | Live autonomous repair experiment with `wonc check --format=json` and `edit_file` | `VERIFIED` ([docs/phase-0b-ai-agent-experiment.md](phase-0b-ai-agent-experiment.md)) |
| **Static Linking** | Unclear error reporting on missing static libc | Added specific diagnostic distinguishing missing static archives | `VERIFIED` (`wonc build --static`) |

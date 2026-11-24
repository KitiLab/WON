# WON Phase 0B Implementation Specification: The Proof of Concept

> **Milestone**: Phase 0B (Proof of Concept Vertical Slice)  
> **Status**: `SPECIFIED` / Ready for Execution  
> **Author**: KitiLab Engineering  
> **Scope**: Pre-Alpha Verification Pipeline

---

## 1. Executive Mission & Five Validation Goals

The objective of Phase 0B is **not** to implement the entire WON compiler or runtime. Rather, it is to build a laser-focused, fully operational vertical slice that empirically proves five core assertions:

1. **Claim A (Parsing)**: A minimal subset of WON source code can be parsed into a clean AST.
2. **Claim B (C Interoperability)**: WON code can call a real, installed system C library through an automated binding generated from its official C header file.
3. **Claim C (Native Execution & Static Linking)**: The compiler pipeline can emit standard C and produce a standalone, statically linked native executable.
4. **Claim D (Fair Empirical Benchmarking)**: The resulting executable can execute a real-world workload in a fair, automated benchmark against equivalent optimized C (`gcc -O3`) and Go implementations.
5. **Claim E (AI Self-Repair Loop)**: An autonomous AI coding agent can receive structured JSON compiler diagnostics from a deliberately introduced defect, interpret the error span and suggestions, and autonomously repair the source code to achieve a clean compilation.

---

## 2. First C Library Evaluation & Selection

### Evaluated Candidates: SQLite vs zlib

| Criterion | SQLite (`sqlite3.h`) | zlib (`zlib.h`) |
| :--- | :--- | :--- |
| **Header Complexity** | Over 10,000 lines; extensive C preprocessor macros; dynamic function pointer tables; OS VFS abstractions. | ~1,800 lines; standard ISO C signatures; clean struct layouts (`z_stream`); explicit integer error codes. |
| **System Availability** | Runtime present, but development headers (`sqlite-devel`) not universally installed. | Headers and development libraries (`/usr/include/zlib.h`, `-lz`) verified present in the host environment. |
| **Verification Veracity** | Relies on query execution and disk I/O, which introduces filesystem caching variables into benchmarks. | Pure algorithmic byte-buffer compression/decompression; exact mathematical output verification (checksums). |
| **FFI Surface Demonstrated** | Complex pointer handles (`sqlite3*`, `sqlite3_stmt*`), variadic functions, callbacks. | Direct pointer + buffer length passing, mutable pointers, struct passing, return code checking. |
| **Benchmark Suitability** | High I/O overhead obscures language/compiler baseline performance. | CPU- and memory-intensive; cleanly isolates compiler codegen efficiency and ABI calling overhead. |

### Decision: Selection of `zlib` for Phase 0B
**`zlib` is selected as the primary library for Phase 0B.**
- It is an authentic, battle-tested native C library installed directly in the operating system.
- It tests genuine native C linking (`-lz`), raw buffer handling, pointer mutability, and error handling without requiring mock or fake stubs.
- SQLite remains scheduled as the primary database driver target in Phase 1/2 once the full macro and typedef engine matures.

---

## 3. Minimal Language Syntax (Phase 0B Subset)

To prevent premature complexity, the Phase 0B compiler frontend will accept only the minimum syntax required to achieve the vertical slice:

```won
// Phase 0B Minimal Syntax Specification

#import <zlib.h>

fn main() -> i32 {
    let message = "WON Phase 0B Proof of Concept: Native C Interop with zlib";
    let input_bytes = message.as_bytes();
    let input_len: u64 = input_bytes.len();

    let mut compressed_buffer: [u8; 256] = [0; 256];
    let mut compressed_len: u64 = 256;

    let status = unsafe {
        zlib::compress(
            compressed_buffer.as_mut_ptr(),
            &mut compressed_len,
            input_bytes.as_ptr(),
            input_len
        )
    };

    if status != 0 {
        return 1;
    }

    println("Compression successful. Compressed bytes: ", compressed_len);
    return 0;
}
```

### Supported Syntax Subset in Phase 0B:
- Functions: `fn main() -> i32 { ... }`
- Variables: `let` (immutable) and `let mut` (mutable)
- Types: `i32`, `u64`, `u8`, fixed-size byte buffers `[u8; N]`, string literals
- Unsafe Blocks: `unsafe { ... }` for foreign C call invocations
- External Calls: Namespaced C library function calls (`zlib::compress`)
- Basic Control Flow: `if / else`, `return`

---

## 4. Minimal Compiler Architecture & Pipeline

```
                input.won
                    │
                    ▼
          ┌──────────────────┐
          │  Minimal Lexer   │  Token stream with line/column coordinates
          └─────────┬────────┘
                    │
                    ▼
          ┌──────────────────┐
          │  Minimal Parser  │  Basic AST nodes (Functions, Calls, Binds)
          └─────────┬────────┘
                    │
                    ▼
          ┌──────────────────┐
          │  Binding Parser  │  Extracts `compress` & `uncompress` from zlib.h
          └─────────┬────────┘
                    │
                    ▼
          ┌──────────────────┐
          │  C99 Generator   │  Emits clean C code linking zlib.h
          └─────────┬────────┘
                    │
                    ▼
          ┌──────────────────┐
          │  Native Linker   │  gcc/clang -O3 -static -lz -o won_poc
          └─────────┬────────┘
                    │
                    ▼
             won_poc (binary)
```

### Static Linking Verification:
The output executable must be tested with `ldd` and `file`:
- `file won_poc`: Confirms ELF 64-bit LSB executable, statically linked.
- `ldd won_poc`: Outputs "not a dynamic executable" (or links exclusively against verified static libc/musl).

---

## 5. Automated AI Self-Repair Experiment (Claim E)

To empirically prove the AI-native capability:

1. **Inject Error**: A deliberate defect will be introduced into `broken.won`:
   - e.g., Omitting `mut` on `compressed_len` passed to `&mut compressed_len`, or passing a moved value.
2. **Compiler Diagnostic**: The Phase 0B compiler runs with `--format=json`:
   - Emits structured JSON identifying the exact error code, source line, column, explanation, and `suggestions` array with a replacement diff.
3. **Agent Action**: An AI script reads the JSON error payload, extracts the suggestion, applies the patch to the source file, and re-compiles.
4. **Verification**: The compiler re-runs and successfully produces a valid binary, proving zero-human-intervention repair.

---

## 6. Empirical Benchmark Harness (Claim D)

The benchmark harness will compare:
1. **WON Binary**: Compiled via WON C codegen + `gcc -O3`.
2. **C Baseline**: Idiomatic C program doing identical compression iterations compiled with `gcc -O3 -flto`.
3. **Go Baseline**: Idiomatic Go program utilizing standard `compress/zlib` compiled with `go build`.

### Benchmark Workload:
- Compress and decompress a standard 1MB text payload 1,000 times in memory.
- Collect 30 iterations per implementation after 5 warm-up iterations.
- Record:
  - Median wall-clock execution time.
  - Interquartile Range (IQR).
  - Peak RSS memory footprint.
  - Executable binary size.

---

## 7. Acceptance Criteria for Phase 0B

Phase 0B will be formally accepted only when all of the following conditions are satisfied:

- [ ] Minimal parser compiles valid Phase 0B `.won` code without panic.
- [ ] Automated binding generator successfully extracts functions and constants from `/usr/include/zlib.h`.
- [ ] End-to-end compilation generates an executable `won_poc` that successfully compresses and decompresses test payloads with zero data corruption.
- [ ] Static linking is verified via `file` or `ldd`.
- [ ] Automated AI repair loop successfully resolves a broken `.won` file using structured JSON compiler output.
- [ ] Benchmark harness executes across all three implementations (WON, C, Go), recording raw results in `benchmarks/phase0/`.
- [ ] All code and benchmark raw outputs are committed to the official repository.

---

## 8. Expected Artifacts

At the conclusion of Phase 0B, the repository will contain:
- `compiler/poc/`: Minimal bootstrap compiler prototype.
- `bindings/zlib/`: Generated zlib binding and binding extraction utility.
- `examples/zlib_compression.won`: Working sample program.
- `benchmarks/phase0/`: Benchmark runner, C baseline, Go baseline, and raw JSON benchmark data.
- `tests/ai_repair/`: Automated AI repair test script with broken and repaired fixtures.

# WON C Interoperability Architecture

> **Document Status**: `SPECIFIED`  
> **Author**: KitiLab Engineering  
> **Last Updated**: Phase 0A

---

## 1. The Core Promise: Frictionless C Integration

One of the central pillars of WON is:

> **"Any C library. One tool. One file."**

Rather than treating foreign function interfaces (FFI) as an awkward, secondary bridge requiring manual bindings or separate wrapper libraries, WON treats C interoperability as a first-class language capability.

---

## 2. Header-First Import Model

In WON, developers can import standard C header files directly into their source files:

```won
#import <zlib.h>
#import <sqlite3.h>

fn compress_data(input: &[u8]) -> Result<Vec<u8>, i32> {
    let mut dest_len: uLongf = (input.len() * 2) as uLongf;
    let mut output: Vec<u8> = Vec::with_capacity(dest_len as usize);

    unsafe {
        let ret = zlib::compress(
            output.as_mut_ptr(),
            &mut dest_len,
            input.as_ptr(),
            input.len() as uLong
        );
        if ret != zlib::Z_OK {
            return Err(ret);
        }
    }

    output.set_len(dest_len as usize);
    return Ok(output);
}
```

---

## 3. C Binding Generation Strategy

To achieve direct header importing, the compiler utilizes an automated C header analysis pass:

1. **Header Parsing**: The compiler parses target C headers (`.h`), extracting macro definitions, struct declarations, typedefs, unions, enums, and function signatures.
2. **Type Mapping**: Standard C types are mapped to deterministic WON ABI types:
   - `int` ➔ `c_int` (`i32`)
   - `unsigned long` ➔ `c_ulong` (`u64` on 64-bit systems)
   - `char*` ➔ `*mut c_char`
   - `const char*` ➔ `*const c_char`
   - Structs with C alignment (`#[repr(C)]`).
3. **Safety Isolation**: All foreign C functions are marked `unsafe`, requiring developers to wrap raw C calls in safe, idiomatic WON abstractions.

*(The choice between a libclang-based parser and a lightweight native C header parser is currently evaluated under [ADR-006](decisions/ADR-006-c-header-parsing-strategy.md).)*

---

## 4. FFI Ownership & Memory Boundaries

Crossing the boundary between safe WON code and C libraries requires clear ownership contracts:

| Responsibility | Safe WON Code | Foreign C Library |
| :--- | :--- | :--- |
| **Memory Allocation** | Allocated via WON allocator; deallocated at scope exit. | Allocated via `malloc()` or library custom allocator. |
| **Ownership Transfer** | Explicitly passed via raw pointers. | Must provide a documented free function (e.g., `sqlite3_free`, `free`). |
| **Buffer Slicing** | Contiguous slices (`&[u8]`) passed as pointer + length. | Must respect provided length; bounds checking not performed by C. |

---

## 5. Roadmap for C Interoperability Targets

- **Phase 0B Target**: `zlib` (Header: `zlib.h`, Lib: `-lz`). Compact, standard, zero external dependencies, immediate compression/decompression verification.
- **Phase 1/2 Target**: `sqlite3` (Header: `sqlite3.h`, Lib: `-lsqlite3`). Relational database engine exercising complex structs, callbacks, and pointer handles.
- **Phase 3 Target**: `libcurl` (Header: `curl/curl.h`, Lib: `-lcurl`). Network client exercising socket descriptors, multi-handles, and asynchronous callbacks.
- **Phase 6 Target**: `Python.h` (CPython C-API). Native embedded Python runtime allowing zero-overhead memory exchange with PyTorch, NumPy, and Python data science packages.

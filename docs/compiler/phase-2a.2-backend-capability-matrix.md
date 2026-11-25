# WON Phase 2A.2 — C11 Backend Capability Contract & Matrix

> **Document Status**: `IMPLEMENTED`  
> **Milestone**: Phase 2A.2 — Backend Capability Correctness  
> **Target ABI**: `x86_64-redhat-linux-gnu` (Linux x86_64 LP64, ISO C11 `-std=c11 -pedantic-errors -Wall -Wextra -Werror`)  
> **Scope Boundary**: Semantic validation (`wonc check`) covers the broader Phase 2A/2A.1 language surface; native compilation (`wonc build`) enforces this capability contract and rejects unsupported constructs prior to C emission with `WON-GEN-002`.

---

## 1. Master C11 Backend Capability Matrix

Every WON type and language construct accepted by the Phase 2A/2A.1 semantic checker falls into one of two deterministic categories:
- **Mapped**: Explicitly lowered via a fail-closed WON-to-C11 type mapper (`MapTypeToC`) and checked against strict GCC 16 & Clang 23 `-std=c11 -pedantic-errors -Wall -Wextra -Werror`.
- **Gated (`WON-GEN-002`)**: Valid in `wonc check`, but explicitly rejected by `TypedProgram` capability analysis before any `.won.c` file is written in `wonc build`.

| WON Type or Feature | Semantic Support | Current C11 Support (`linux/x86_64` LP64) | Action / C11 Representation |
|---|---|---|---|
| `i8` | Full | Full (`<stdint.h>` `int8_t`) | **Map** -> `int8_t`; literals `INT8_MIN` / `((int8_t)INT8_C(x))` |
| `i16` | Full | Full (`<stdint.h>` `int16_t`) | **Map** -> `int16_t`; literals `INT16_MIN` / `((int16_t)INT16_C(x))` |
| `i32` | Full | Full (`<stdint.h>` `int32_t`, `int` ABI match on LP64) | **Map** -> `int32_t`; literals `INT32_MIN` / `INT32_C(x)` |
| `i64` | Full | Full (`<stdint.h>` `int64_t`) | **Map** -> `int64_t`; literals `INT64_MIN` / `INT64_C(x)` |
| `u8` | Full | Full (`<stdint.h>` `uint8_t`, compatible with `unsigned char`) | **Map** -> `uint8_t`; literals `((uint8_t)UINT8_C(x))` |
| `u16` | Full | Full (`<stdint.h>` `uint16_t`) | **Map** -> `uint16_t`; literals `((uint16_t)UINT16_C(x))` |
| `u32` | Full | Full (`<stdint.h>` `uint32_t`) | **Map** -> `uint32_t`; literals `UINT32_C(x)` |
| `u64` | Full | Full (`<stdint.h>` `uint64_t`, matches LP64 `unsigned long` / `uLong`) | **Map** -> `uint64_t`; literals `UINT64_C(x)` |
| `i128`, `u128` | Full (`big.Int`) | Not portable in strict ISO C11 (`__int128` is GNU extension) | **Gate (`WON-GEN-002`)** in locals, params, returns, casts, literals |
| `isize` | Full | Full on LP64 (`<stddef.h>` `ptrdiff_t`, 64-bit signed) | **Map** -> `ptrdiff_t`; literals `PTRDIFF_MIN` / `((ptrdiff_t)INT64_C(x))` |
| `usize` | Full | Full on LP64 (`<stddef.h>` `size_t`, 64-bit unsigned) | **Map** -> `size_t`; literals `((size_t)UINT64_C(x))` |
| `f32` | Full | Arithmetic (`+`, `-`, `*`, `/`) & comparisons; modulo `%` invalid in C11 | **Map** -> `float` (`f`-suffixed literal); **Gate `%` (`WON-GEN-002`)** |
| `f64` | Full | Arithmetic (`+`, `-`, `*`, `/`) & comparisons; modulo `%` invalid in C11 | **Map** -> `double`; **Gate `%` (`WON-GEN-002`)** |
| `bool` | Full | Full (`<stdbool.h>` `bool`, `true`/`false`, `and`->`&&`, `or`->`\|\|`, `not`->`!`) | **Map** -> `bool` |
| `char` | Full | Full (`<stdint.h>` `uint32_t` UTF-32 code point) | **Map** -> `uint32_t`; literals `((uint32_t)UINT32_C(rune))` |
| `unit` | Full | **Return position only** (`void`); invalid as C object type | **Map** `void` in fn returns & wrap `fn main() -> unit` via `int main(void)`; **Gate (`WON-GEN-002`)** in params/locals/exprs |
| `str` (DST) | Rejected by value (`WON-TYP-006`) | Unsupported | **Gate** (`WON-TYP-006` / `WON-GEN-002`) |
| `&str` | Full | Subset: C string literal view (`const char*`), FFI, `println`; content `==` / `.len()` / `.is_empty()` unlowered | **Map** `const char*` in C-string view & FFI; **Gate (`WON-GEN-002`)** `.len()`, `.is_empty()`, `&str == &str` |
| `String` | Partial (`String::from`, `push_str`) | Deferred (no heap string runtime lowering) | **Gate (`WON-GEN-002`)** |
| Fixed arrays `[T; N]` | Full | Subset: stack local repeat-initialized buffers `[T; N]` where `T` is scalar, `.as_ptr()`, `.as_mut_ptr()`, `.len()` | **Map** stack C array + constant `UINT64_C(N)` `.len()`; **Gate (`WON-GEN-002`)** array params, array returns, `[elem1, elem2]` list literals, indexing `arr[i]`, slicing |
| References `&T`, `&mut T` | Full | Subset: scalar `const <C_T>*` / `<C_T>*`, plus `let x = s.as_bytes()` solely for `x.as_ptr()` (`const uint8_t*`) | **Map** scalar refs; **Gate (`WON-GEN-002`)** `&[T]` params/returns, general slices, unsupported pointees |
| Raw pointers `*const T`, `*mut T` | Full | Subset: C11-supported pointee `const <C_T>*` / `<C_T>*` or `unit` (`const void*`/`void*`) | **Map** scalar/`void` raw pointers; **Gate (`WON-GEN-002`)** pointers to unsupported types |
| Tuples `(T1, ...)` | Full | Deferred | **Gate (`WON-GEN-002`)** |
| User-defined `struct` / `enum` | Full | Deferred (Phase 2C TIR lowering) | **Gate (`WON-GEN-002`)** |
| Inherent `impl` & trait methods | Inherent supported; traits deferred | Deferred | **Gate (`WON-GEN-002`)** |
| Interpolated strings (`InterpString`) | Full | Deferred | **Gate (`WON-GEN-002`)** |
| `match`, `Option`, `Result`, `?` | Full | Deferred | **Gate (`WON-GEN-002`)** |
| Generic instantiation | Partial (`Option`/`Result`/type aliases) | Deferred (monomorphization Phase 2C) | **Gate (`WON-GEN-002`)** |
| Casts (`as`), `for`, `panic`, `assert` | Semantically checked | Deferred in C11 subset | **Gate (`WON-GEN-002`)** |
| Ownership (`Move`/`Borrow` pass) & Concurrency (`scope`/`spawn`) | Deferred (`Phase 2B`/`Phase 3`) | Deferred | **Gate (`WON-SEM-010` / `WON-GEN-002`)** |

---

## 2. Fail-Closed C Type Mapping Rules (`MapTypeToC`)

The C11 generator never falls back to raw WON type names or `void` defaults:

1. **Explicit Positional Context (`TypePos`)**:
   - `PosReturn`: Permits `unit` (`void`) and any C11-supported value/pointer/scalar-ref return type; rejects fixed arrays `[T; N]` (invalid C return type) and unsupported types.
   - `PosParam`: Rejects `unit`, fixed arrays `[T; N]`, `&[T]` fat slices, and unsupported types.
   - `PosLocal`: Permits C11 scalars, scalar pointers/references, `&str`, and local fixed arrays `[T; N]` with scalar elements (or Phase 0B `.as_bytes()` alias); rejects `unit`, `String`, `&[T]` (except `.as_bytes().as_ptr()`), structs, enums, tuples.
2. **Error Propagation**: Unmappable types are caught before C emission during backend capability verification (`WON-GEN-002`) and also return explicit `error` values inside `codegen` (`WON-GEN-001` defense-in-depth).

---

## 3. Main Entry-Point ABI Policy

Per ISO C11 §5.1.2.2.1 (hosted environments), `void main` and arbitrary return types like `uint64_t main` are ill-formed or non-conforming under `-std=c11 -pedantic-errors`:

- **`fn main() -> i32:`**: Emits canonical `int main(void) { ... }`.
- **`fn main():` or `fn main() -> unit:`**: Emits `static void won_user_main(void) { ... }` followed by `int main(void) { won_user_main(); return 0; }`, preserving clean process exit code `0`.
- **Any other `main` signature** (parameters `fn main(x: i32)`, or return types other than `i32`/`unit`, e.g., `bool`, `u64`, `i64`): Rejected prior to codegen with `WON-GEN-002`.

---

## 4. Typed Integer Constant Emission Strategy

Every integer literal is emitted using its resolved semantic `TypeID` from `TypedProgram.ExprTypes` (plus unary `-` detection for signed minimums):

- **Exact Signed Minimum Constants**:
  - `i8` `-128` -> `INT8_MIN`
  - `i16` `-32768` -> `INT16_MIN`
  - `i32` `-2147483648` -> `INT32_MIN`
  - `i64` `-9223372036854775808` -> `INT64_MIN`
  - `isize` `-9223372036854775808` -> `PTRDIFF_MIN`
- **Other Signed/Unsigned Values** (using `<stdint.h>` constant macros to guarantee width and signedness without C implicit overflow or `-pedantic-errors` decimal-constant errors):
  - `i8`: `((int8_t)INT8_C(val))` (or `-((int8_t)INT8_C(pos))` via `((int8_t)(-INT8_C(absVal)))`)
  - `i16`: `((int16_t)INT16_C(val))`
  - `i32`: `INT32_C(val)` / `(-INT32_C(absVal))`
  - `i64`: `INT64_C(val)` / `(-INT64_C(absVal))`
  - `isize`: `((ptrdiff_t)INT64_C(val))` / `((ptrdiff_t)(-INT64_C(absVal)))`
  - `u8`: `((uint8_t)UINT8_C(val))`
  - `u16`: `((uint16_t)UINT16_C(val))`
  - `u32`: `UINT32_C(val)`
  - `u64`: `UINT64_C(val)` (handles `18446744073709551615u64` cleanly under `-pedantic-errors`)
  - `usize`: `((size_t)UINT64_C(val))`

---

## 5. Target ABI Consistency (`x86_64` Linux LP64)

- Generated C includes `<stddef.h>`, `<stdint.h>`, `<stdbool.h>`, and `<inttypes.h>`, plus `_Static_assert(sizeof(void*) == 8 && sizeof(long) == 8, "WON Phase 2A.2 C11 backend targets Linux x86_64 LP64 ABI");`.
- `u64` (`uint64_t`) and `usize` (`size_t`) align natively with LP64 `unsigned long` (`uLong` / `uLongf` in `<zlib.h>`); `i32` (`int32_t`) aligns natively with `int`.
- Cross-platform Windows LLP64 (`long` = 32-bit) and non-64-bit targets remain outside Phase 2A.2 scope.

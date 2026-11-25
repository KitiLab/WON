# WON Language Specification: Type System

> **Specification Version**: Draft 0.1  
> **Status**: `IMPLEMENTED` (Core Type System, Bidirectional Inference, Structs/Enums & Pattern Exhaustiveness in Phase 2A; Trait Solver & Monomorphization `SPECIFIED` for Phase 2B/2C)  
> **Module**: Core Type System & Trait Architecture  
> **Phase 2A Implementation**: [Phase 2A Typed Semantic Model](../compiler/phase-2a-typed-semantic-model.md)  
> **Date**: November 2026

---

## 1. Foundations of the Type System

WON is a statically typed language designed with three fundamental type-theoretic principles:
1. **Zero Runtime Type Overhead**: Concrete types, method dispatch, and generic abstractions are resolved ahead-of-time (AOT) at compile time via monomorphization and static resolution.
2. **Local Inference, Explicit Interfaces**: Within function bodies, variable types are inferred through bidirectional local type inference. At module and public function boundaries, all parameter and return types **MUST** be explicitly declared.
3. **Sound Memory Invariants**: The type system cooperates directly with the ownership and borrowing engine, distinguishing owned values from immutable references (`&T`) and mutable references (`&mut T`).

---

## 2. Primitive Types

WON provides fixed-width numeric types, logical booleans, Unicode characters, strings, and the zero-sized unit type.

```
                            Primitive Types
                                   │
      ┌──────────────┬─────────────┴────────────┬─────────────┐
      ▼              ▼                          ▼             ▼
   Integers       Floats                     Textual       Special
  i8   .. i128    f32 (IEEE 754 single)      bool          unit ()
  u8   .. u128    f64 (IEEE 754 double)      char (UTF-32)
  isize, usize                               str (UTF-8)
```

### 2.1 Integer Types and Arithmetic Semantics
- **Signed Fixed-Width**: `i8`, `i16`, `i32`, `i64`, `i128`. Representation: Two's complement.
- **Unsigned Fixed-Width**: `u8`, `u16`, `u32`, `u64`, `u128`.
- **Pointer-Sized**: `isize` and `usize`. Guaranteed to match the native word size of the target platform (32-bit on x86, 64-bit on x86_64 and AArch64). Required for array indexing, pointer offsets, and memory sizing.
- **Vocabulary Aliases**:
  - `Int`: Normative alias for `i64`. Default type for unannotated integer literals.
  - `UInt`: Normative alias for `u64`.
  - `Byte`: Normative alias for `u8`.
- **Arithmetic and Overflow Semantics**:
  - **Trade-off Evaluation**: Discrepancies between debug (panic) and release (wrapping) builds can create difficult "heisenbugs" where release builds silently calculate wrong numbers that crashed in debug.
  - **Normative Policy**:
    1. **Default Wrapping Arithmetic**: Standard binary operations (`+`, `-`, `*`) default to two's complement wrapping across all optimization profiles to guarantee deterministic, reproducible semantics.
    2. **Debug Instrumentation Flag**: The compiler provides an opt-in flag `-C overflow-checks=on` (enabled by default in debug/test profiles) that inserts runtime trap checks.
    3. **Division by Zero**: Dividing an integer by zero (`x / 0` or `x % 0`) triggers an immediate hardware exception / runtime panic across all build profiles.
    4. **Minimum Integer Negation**: Evaluating `INT_MIN / -1` or `INT_MIN % -1` triggers an immediate overflow panic across all build profiles (two's complement cannot represent `+2^(N-1)`).
    5. **Shift Count Masking**: Bitwise shifts (`<<`, `>>`) with shift count $S \ge W$ (where $W$ is bit width) mask the shift count to $S \pmod W$, matching native CPU instruction behavior and eliminating undefined behavior.
    6. **Constant Expression Overflow**: Arithmetic overflow in compile-time constant expressions is strictly rejected at build time (`WON-TYP-004: Compile-time arithmetic overflow`).
    7. **Standard Explicit Intrinsics**:
       - `.checked_add()`, `.checked_sub()`, `.checked_mul()`, `.checked_div()` -> `Option[T]`
       - `.saturating_add()`, `.saturating_sub()`, `.saturating_mul()` -> `T`
       - `.wrapping_add()`, `.wrapping_sub()`, `.wrapping_mul()` -> `T`

### 2.2 Floating-Point Types
- `f32`: Standard IEEE 754-2008 single-precision (32-bit).
- `f64`: Standard IEEE 754-2008 double-precision (64-bit). Default type for unannotated floating-point literals.

### 2.3 Textual, Logical and String Types
- `bool`: Logical boolean (`true` or `false`). Stored in memory as 1 byte (`0x00` = false, `0x01` = true). Any other byte representation is invalid.
- `char`: A 32-bit Unicode Scalar Value (any Unicode code point excluding high and low surrogate code points, `0x0000` to `0xD7FF` and `0xE000` to `0x10FFFF`). Size: 4 bytes.
- **The Three-Tier String Model**:
  1. `str`: A **Dynamically Sized Type (DST)** representing an unsized, immutable sequence of valid UTF-8 bytes. Because its size is not known at compile time, `str` **CANNOT** exist as an unadorned local stack variable or be passed by value.
  2. `&str`: An immutable **borrowed string view**. Represented at runtime as a 16-byte fat pointer `(pointer: *const u8, length: usize)` on 64-bit systems. Cheaply copyable (`Copy`).
  3. `String`: An **owned, heap-allocated, resizable UTF-8 string buffer**. Represented at runtime as a 24-byte descriptor `(pointer: *mut u8, length: usize, capacity: usize)` on 64-bit systems. Moved by default (non-`Copy`). Automatically reclaims its heap memory when dropped.
  4. `Str`: A high-level type alias for `&str` when used as an immutable parameter in public API declarations (`fn greet(name: Str)` -> `fn greet(name: &str)`).
- `unit`: The zero-sized type `()`. Occupies 0 bytes in memory. Replaces C `void`.

---

## 3. Composite Types and Memory Representations

### 3.1 Structs and Layout Optimization
Structs define nominal record types containing named fields.

```won
struct ConnectionConfig:
    pub host: String
    pub port: u16
    pub timeout_ms: u32
    pub var retries: u8    # Mutable field within mutable struct instance
```
- **Field Mutability (`var field: Type`)**: Struct fields are immutable by default when accessed through an immutable binding (`let c = ...`). A field marked `var` indicates that the field is mutable whenever accessed through a mutable binding (`var c = ...`) or a mutable reference (`&mut self`), while non-`var` fields remain read-only if desired.
- **Default Memory Layout**: To maximize hardware cache efficiency and eliminate wasted alignment holes, the compiler sorts fields by descending alignment:
  - 64-bit pointers and integers (8-byte alignment) first.
  - 32-bit integers and floats (4-byte alignment) second.
  - 16-bit integers (2-byte alignment) third.
  - 8-bit bytes and booleans (1-byte alignment) last.
- **C-Compatible Layout (`@repr(C)`)**: Fields **MUST** be placed in the exact order declared, with standard C ABI padding and alignment.
- **Packed Structs (`@repr(packed)`)**: Fields are aligned to 1 byte with zero inter-field padding.
  > **Safety Invariant**: Because taking a reference to an unaligned field can trigger CPU hardware misalignment traps, **borrowing an unaligned field by reference (`&packed.field`) is strictly FORBIDDEN in safe code** (`WON-TYP-005: Cannot borrow unaligned packed field by reference`). Access **MUST** occur by value copy or via raw pointer in `unsafe:`.

### 3.2 Enums (Algebraic Data Types)
Enums model disjoint sum types:
- **Representation**: Stored as a tagged union consisting of:
  $$\text{Enum Memory} = \text{Discriminant Tag } (1, 2, 4, \text{ or } 8 \text{ bytes}) \;+\; \text{Aligned Payload Buffer (sized to largest variant)}$$
- **Discriminant Optimization**: For nullable pointer types (`Option[&T]`, `Option[Box[T]]`, `Option[fn()]`), the compiler applies null-pointer optimization: the `None` variant is represented as null pointer `0x0`, reducing `Option[&T]` to exactly the size of a single pointer (8 bytes) with zero tag overhead.

```won
enum NetState:
    Disconnected
    Connecting(u32)             # Tuple-style payload
    Connected:                  # Struct-style payload
        socket_fd: i32
        peer_ip: str
    Failed(str)
```
- **Representation**: Enums are compiled as tagged unions consisting of an integer discriminant tag plus a payload buffer sized to the largest variant.
- **Safety**: Unchecked field access is forbidden; variant payloads **MUST** be accessed exclusively through exhaustive pattern matching (`match`).

### 3.3 Tuples
Anonymous product types grouping heterogeneous values:
```won
let pair: (i32, str) = (42, "Answer")
let (code, msg) = pair    # Pattern destructuring
```

### 3.4 Arrays and Slices
- **Fixed-Size Arrays (`[T; N]`)**: Statically sized contiguous sequences allocated on the stack or inline within containing structs. Size $N$ **MUST** be a compile-time constant integer:
  ```won
  let buffer: [u8; 1024] = [0u8; 1024]
  ```
- **Slices (`[T]` and `&[T]`)**: Dynamically sized views into a contiguous sequence of elements. Represented at runtime as a fat pointer containing `(pointer: *const T, length: usize)`.
- **Dynamic Vectors (`Vec[T]`)**: Standard library heap-allocated, resizable array with capacity and length tracking.

### 3.5 Option and Result Types
Built directly into the core prelude:
```won
enum Option[T]:
    Some(T)
    None

enum Result[T, E]:
    Ok(T)
    Err(E)
```

---

## 4. Local Type Inference

WON employs a bidirectional local type inference algorithm within function scopes:
1. **Bottom-Up Synthesis**: Expressions synthesize their type from sub-expressions and literals (e.g., `10 + 20` synthesizes `i64`).
2. **Top-Down Propagation**: Expected types from variable declarations, return types, or parameter signatures propagate downward into expressions:
   ```won
   let x: u8 = 10    # 10 is inferred as u8 based on annotation
   ```
3. **No Implicit Lossy Conversions**: WON strictly prohibits implicit widening or narrowing numeric coercions. Coercions **MUST** be explicit using the `as` operator or standard conversion traits (`Into[T]`):
   ```won
   let a: i32 = 100
   # let b: i64 = a        <- Compile Error: Type mismatch (i32 vs i64)
   let b: i64 = a as i64   # OK: Explicit conversion
   ```
4. **Boundary Clarity**: Top-level functions, methods, struct fields, and trait definitions **MUST** specify explicit type annotations for all parameters and return values.

---

## 5. Generics and Monomorphization

WON supports parameterized polymorphism across functions, structs, enums, and traits.

### 5.1 Type Parameters and Trait Bounds
```won
fn find_maximum[T: Comparable + Copy](items: &[T]) -> Option[T]:
    if items.len() == 0:
        return Option::None
    var max_val = items[0]
    for item in items:
        if item > max_val:
            max_val = item
    Option::Some(max_val)
```

### 5.2 Monomorphization Pipeline
- When a generic function or data structure is instantiated with concrete type arguments (e.g., `find_maximum[i32]`), the compiler generates a specialized, concrete instance at the Typed Intermediate Representation (TIR) level.
- **Zero Runtime Dispatch**: Calls to generic functions compile down to direct, non-virtual native function calls.
- **Code Bloat Mitigation**: The TIR optimizer deduplicates identical monomorphized specializations for pointer-compatible types (`&T` and raw pointers).

---

## 6. Traits and Abstraction

WON avoids class inheritance and virtual table bloat in favor of trait-based compositional polymorphism.

### 6.1 Trait Declaration and Implementation
```won
trait Serializer:
    fn serialize(&self) -> [u8]
    fn format_name() -> str    # Static associated function

struct JsonPayload:
    content: str

impl Serializer for JsonPayload:
    fn serialize(&self) -> [u8]:
        self.content.as_bytes()

    fn format_name() -> str:
        "JSON"
```

### 6.2 Trait Resolution and Dispatch
1. **Static Dispatch & Monomorphization (Default)**:
   - When functions use generic trait bounds (`fn export[T: Serializer](item: &T)`), the compiler generates specialized machine code for each concrete type $T$.
   - **Performance**: Zero runtime dispatch overhead; enables direct inlining, constant propagation, and vectorization.
   - **Code Size Trade-off**: Multiple instantiations can increase binary size. The TIR optimizer deduplicates monomorphized instances for ABI-compatible types (e.g. sharing identical code for all `&T` where $T$ has pointer width).
2. **Dynamic Dispatch (`dyn Trait`)**:
   - When heterogeneous collections or runtime polymorphism are required, types can be manipulated via explicit trait objects:
     ```won
     let obj: &dyn Serializer = &my_payload
     ```
   - **Representation**: A `dyn Trait` value is represented as a 16-byte fat pointer on 64-bit systems:
     $$\text{Fat Pointer} = (\text{Data Pointer: } *\text{const unit}, \; \text{VTable Pointer: } *\text{const VTable})$$
   - **Performance Overhead**: Unlike generic static dispatch, invoking methods through `&dyn Trait` incurs an indirect function call through the vtable pointer, which carries CPU branch prediction overhead and prevents inlining. Dynamic dispatch is explicitly opt-in.
3. **Object Safety Rules**: A trait is object-safe (usable with `dyn Trait`) only if:
   - All methods take an explicit receiver (`&self` or `&mut self`).
   - No methods return `Self`.
   - The trait has no generic type parameters on individual methods.

# WON Language Specification: Type System

> **Specification Version**: Draft 0.1  
> **Status**: `SPECIFIED` (Under Architectural Review)  
> **Module**: Core Type System & Trait Architecture  
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

### 2.1 Integer Types
- **Signed Fixed-Width**: `i8`, `i16`, `i32`, `i64`, `i128`. Representation: Two's complement.
- **Unsigned Fixed-Width**: `u8`, `u16`, `u32`, `u64`, `u128`.
- **Pointer-Sized**: `isize` and `usize`. Guaranteed to match the native word size of the target platform (32-bit on x86, 64-bit on x86_64 and AArch64). Required for array indexing, pointer offsets, and memory sizing.
- **Legacy Vocabulary Aliases**:
  - `Int`: Normative alias for `i64`. Default type for unannotated integer literals.
  - `UInt`: Normative alias for `u64`.
  - `Byte`: Normative alias for `u8`.
- **Integer Overflow Semantics**:
  - In debug builds, integer overflow **MUST** trigger a controlled runtime panic.
  - In release builds, integer arithmetic defaults to two's complement wrapping.
  - Explicit standard library intrinsics are provided for checked, saturating, and wrapping arithmetic: `.checked_add()`, `.saturating_add()`, `.wrapping_add()`.

### 2.2 Floating-Point Types
- `f32`: Standard IEEE 754-2008 single-precision (32-bit).
- `f64`: Standard IEEE 754-2008 double-precision (64-bit). Default type for unannotated floating-point literals.

### 2.3 Textual and Logical Types
- `bool`: Logical boolean (`true` or `false`). Stored as a single byte (8-bit).
- `char`: A Unicode Scalar Value (any Unicode code point excluding high and low surrogate code points, `0x0000` to `0xD7FF` and `0xE000` to `0x10FFFF`). Size: 32 bits (4 bytes).
- `str`: An immutable, dynamically sized UTF-8 string slice (`[u8]`). Typically manipulated via borrowed string view reference `&str` or owned heap-allocated `String`.
- `unit`: The empty tuple `()` indicating the absence of a meaningful value. Replaces C `void`. Functions without an explicit `-> ReturnType` implicitly return `unit`.

---

## 3. Composite Types

### 3.1 Structs
Structs define nominal record types containing named fields.

```won
struct ConnectionConfig:
    pub host: str
    pub port: u16
    pub timeout_ms: u32
    var retries: u8    # Mutable field within mutable struct instance
```
- **Layout**: Default struct layout is optimized for cache alignment and minimal padding.
- **C-Compatible Layout**: Structs annotated with `@repr(C)` guarantee standard C ABI field ordering and alignment.

### 3.2 Enums (Algebraic Data Types)
Enums model disjoint union types (sum types) with optional type payloads:

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
1. **Static Dispatch (Default)**: Using trait bounds (`fn export[T: Serializer](item: &T)`), the compiler resolves calls statically at compile time with inlining opportunities.
2. **Dynamic Dispatch (`dyn Trait`)**: When heterogeneous collections or runtime polymorphism are required, types can be manipulated via explicit trait objects:
   ```won
   let obj: &dyn Serializer = &my_payload
   ```
   A `dyn Trait` value is represented as a fat pointer:
   $$\text{Fat Pointer} = (\text{Data Pointer: } *\text{const unit}, \; \text{VTable Pointer: } *\text{const VTable})$$
3. **Object Safety Rules**: A trait is object-safe (usable with `dyn Trait`) only if:
   - All methods take an explicit receiver (`&self` or `&mut self`).
   - No methods return `Self`.
   - The trait has no generic methods.

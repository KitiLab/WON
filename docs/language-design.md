# WON Language Design Specification

> **Document Status**: `SPECIFIED` (Phase 1B–2A Frontend & Core Type Checker `IMPLEMENTED`)  
> **Author**: KitiLab Engineering  
> **Last Updated**: Phase 2A  
> **Note**: Phase 0A exploratory brace examples below are historical; normative indentation syntax is specified in [`docs/language-spec/syntax-and-grammar.md`](language-spec/syntax-and-grammar.md) and implemented semantics are documented in [`docs/compiler/phase-2a-semantic-analysis.md`](compiler/phase-2a-semantic-analysis.md).

---

## 1. Design Philosophy

WON's syntax and type system are engineered to provide systems-level control with minimal visual noise and unambiguous parseability for both humans and AI agents.

### Core Principles
- **Explicit over Implicit**: Type conversions, mutability, and unsafe operations must be explicit.
- **Predictable Syntax**: Context-free grammar rules that minimize parser backtracking and simplify AST manipulation.
- **Zero-Cost Abstractions**: High-level constructs (traits, pattern matching, generics) compile directly to efficient native structures without mandatory pointer indirection.

---

> [!NOTE]
> **Formal Specification Reference**: The complete, normative language specification is documented under [`docs/language-spec/`](language-spec/README.md). See:
> - [`syntax-and-grammar.md`](language-spec/syntax-and-grammar.md) for formal EBNF and indentation tokenization.
> - [`type-system.md`](language-spec/type-system.md) for primitive and composite type semantics.
> - [`ownership-and-memory.md`](language-spec/ownership-and-memory.md) for ownership-lite memory safety rules.

---

## 2. Decided Design Principles vs Unresolved Questions

| Area | Decided Principles (`SPECIFIED`) | Unresolved Questions (`UNDER INVESTIGATION`) |
| :--- | :--- | :--- |
| **Mutability** | Immutability via `let`; mutability via `var` (ADR-007). | Exact syntax for deep vs shallow immutability in nested structs. |
| **Syntax Style** | Indentation-based blocks with `:` (ADR-007). | Phase 1B lexer transition from Phase 0 brace prototype. |
| **Functions** | `fn name(param: Type) -> RetType`; explicit public signatures. | Default parameter values vs explicit function overloading. |
| **Error Handling** | Sum types: `Result[T, E]` and `Option[T]`; `?` operator. | Error chaining trait standards (`std::error::Error`). |
| **Pointers & FFI** | Distinct safe references (`&T`, `&mut T`) vs raw pointers (`*const T`, `*mut T`). | Lifetime inference rules across complex struct member projections. |
| **Memory Model** | Move by default for non-`Copy` types; ownership-lite lexical borrowing. | Automatic reference counting promotion for circular graphs. |
| **Generics** | Compile-time monomorphization with trait constraints. | Syntax for associated types vs generic type parameters on traits. |

---

## 3. Proposed Language Constructs

### 3.1 Variables and Binding
Variables are declared using `let` (immutable) or `var` (mutable):

```won
let count: i32 = 42
var buffer: Vec[u8] = Vec::new()
buffer.push(0xFF)
```

Type inference is supported for local variables where the type can be deduced unambiguously:

```won
let name = "WON"          # Inferred as &str
let ratio = 3.14159       # Inferred as f64
```

### 3.2 Functions
Functions are declared with `fn`:

```won
fn calculate_hash(data: &[u8], seed: u64) -> u64:
    var hash = seed
    for byte in data:
        hash = (hash ^ (*byte as u64)) * 1099511628211
    hash
```

### 3.3 Data Structures: Structs
Structs represent contiguous memory layouts with zero hidden overhead:

```won
struct Point3D {
    x: f32,
    y: f32,
    z: f32,
}

impl Point3D {
    fn origin() -> Point3D {
        return Point3D { x: 0.0, y: 0.0, z: 0.0 };
    }

    fn distance_squared(&self) -> f32 {
        return self.x * self.x + self.y * self.y + self.z * self.z;
    }
}
```

### 3.4 Traits (Interfaces)
Traits define behavioral contracts without requiring vtables unless dynamic dispatch is explicitly requested:

```won
trait Serializable {
    fn serialize(&self) -> Vec<u8>;
}

impl Serializable for Point3D {
    fn serialize(&self) -> Vec<u8> {
        let mut out = Vec::with_capacity(12);
        out.extend_from_slice(&self.x.to_le_bytes());
        out.extend_from_slice(&self.y.to_le_bytes());
        out.extend_from_slice(&self.z.to_le_bytes());
        return out;
    }
}
```

### 3.5 Error Handling: Option & Result
Null pointers do not exist in safe WON code. Optional values are modeled via `Option<T>` and fallible operations via `Result<T, E>`:

```won
enum Option<T> {
    Some(T),
    None,
}

enum Result<T, E> {
    Ok(T),
    Err(E),
}

fn open_config(path: &str) -> Result<Config, IoError> {
    let file = File::open(path)?;
    let content = file.read_all()?;
    return Ok(parse_config(&content)?);
}
```

### 3.6 Pattern Matching
Exhaustive pattern matching is enforced at compile time:

```won
fn handle_result(res: Result<u32, ErrorCode>) -> i32 {
    match res {
        Ok(value) => {
            return (value as i32);
        }
        Err(ErrorCode::NotFound) => {
            return -1;
        }
        Err(ErrorCode::PermissionDenied) => {
            return -2;
        }
        Err(_) => {
            return -99;
        }
    }
}
```

### 3.7 Concurrency Model
Safe concurrency is achieved by enforcing Send/Sync semantics at compile time:
- Types that do not share state or are immutable can be safely transferred across thread boundaries.
- Shared mutable state requires synchronization wrappers (`Mutex<T>`, `RwLock<T>`) or channels (`Sender<T>`, `Receiver<T>`).

### 3.8 Unsafe Boundaries
Low-level hardware access, raw pointer dereferencing, and direct C library calls require explicit `unsafe` blocks:

```won
fn direct_memory_copy(dest: *mut u8, src: *const u8, length: usize) {
    unsafe {
        // Direct pointer arithmetic and raw C memcpy
        c_memcpy(dest, src, length);
    }
}
```
All unsafe operations are strictly scoped, allowing audits to isolate non-verified code paths.

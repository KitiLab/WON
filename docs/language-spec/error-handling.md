# WON Language Specification: Error Handling

> **Specification Version**: Draft 0.1  
> **Status**: `SPECIFIED` (Under Architectural Review)  
> **Module**: Error Types, Propagation & Failure Semantics  
> **Date**: November 2026

---

## 1. Error Handling Philosophy

WON explicitly rejects the unchecked, exception-based error model (e.g., C++ exceptions, Java runtime exceptions, Python `try/except`). Unchecked exceptions introduce invisible control-flow edges, defeat compiler optimizations, make resource cleanup unpredictable, and create severe reasoning hurdles for automated AI coding agents.

Instead, WON enforces:
1. **Explicit Error Values**: Errors are regular first-class values represented as algebraic types (`Result[T, E]` and `Option[T]`).
2. **Predictable Control Flow**: Functions that can fail **MUST** advertise possible failure modes in their type signatures.
3. **Ergonomic Propagation (`?`)**: The `?` operator provides concise error propagation without verbose boilerplate.
4. **Deterministic RAII Cleanup**: When an error is propagated, all local stack-allocated variables are dropped in strict reverse declaration order before the function returns.
5. **Clear Separation of Failures**: Recoverable operational errors (`Result`) are strictly distinguished from unrecoverable programmer defects (`panic`).

---

## 2. Core Failure Types: `Option[T]` and `Result[T, E]`

The core prelude defines `Option[T]` and `Result[T, E]`:

```won
enum Option[T]:
    Some(T)
    None

enum Result[T, E]:
    Ok(T)
    Err(E)
```

### 2.1 Representing Optional Values (`Option[T]`)
Used when a function may legitimately find or return nothing:
```won
fn find_user(id: u64) -> Option[User]:
    if id == 0:
        Option::None
    else:
        Option::Some(User { id: id, name: "Alice" })
```

### 2.2 Representing Recoverable Operations (`Result[T, E]`)
Used when an operation can fail due to external conditions (I/O, network, bad input):
```won
struct IoError:
    code: i32
    message: str

fn read_config(path: str) -> Result[Config, IoError]:
    if not file_exists(path):
        return Result::Err(IoError { code: 404, message: "File not found" })
    # Read file and parse...
    Result::Ok(Config::default())
```

---

## 3. The Error Propagation Operator (`?`)

The postfix `?` operator unwraps a successful result or performs an immediate early return from the enclosing function.

### 3.1 Mechanics on `Result[T, E]`
Given an expression `expr?` evaluated inside a function returning `Result[U, TargetError]`:
1. `expr` is evaluated to a value $V$ of type `Result[T, SourceError]`.
2. If $V$ is `Result::Ok(val)`, the expression yields `val` and execution continues.
3. If $V$ is `Result::Err(err)`:
   - `err` is converted to `TargetError` via the `From[SourceError]` conversion trait.
   - All stack variables currently live in the enclosing function are deterministically dropped in reverse declaration order.
   - The function immediately returns `Result::Err(converted_err)`.

```won
fn parse_and_validate(raw_text: str) -> Result[Settings, AppError]:
    # If read_tokens() returns Err, it drops local variables and returns early.
    let tokens = lex_tokens(raw_text)?
    let ast = parse_ast(&tokens)?
    let settings = validate_settings(ast)?
    Result::Ok(settings)
```

### 3.2 Mechanics on `Option[T]`
The `?` operator can also be used inside functions that return `Option[U]`:
- If the evaluated expression is `Option::Some(val)`, it unwraps `val`.
- If the evaluated expression is `Option::None`, local resources are dropped, and the function immediately returns `Option::None`.

---

## 4. Error Conversion and Composition

In large software systems, lower-level subsystem errors (e.g., `IoError`, `ParseError`, `ZlibError`) must seamlessly convert into unified application-level domain errors.

### 4.1 The `From` and `Into` Traits
```won
trait From[Source]:
    fn from(source: Source) -> Self
```

When `?` encounters an error type that does not match the enclosing function's return error type, it automatically invokes `TargetError::from(source_err)`.

```won
enum AppError:
    Io(IoError)
    Parse(str)
    Compression(i32)

impl From[IoError] for AppError:
    fn from(err: IoError) -> AppError:
        AppError::Io(err)

impl From[str] for AppError:
    fn from(msg: str) -> AppError:
        AppError::Parse(msg)

# Both IoError and str can now be propagated with `?` directly into AppError!
fn execute_workflow(filepath: str) -> Result[Data, AppError]:
    let file = open_file(filepath)?        # IoError automatically converted
    let parsed = parse_contents(&file)?   # str error automatically converted
    Result::Ok(parsed)
```

---

## 5. Unrecoverable Failures: Panics and Traps

Not all errors are recoverable. A program **MUST NOT** attempt to recover from internal invariant violations, arithmetic corruption, or out-of-bounds memory indexing.

### 5.1 Panic Semantics
- A panic is triggered via `panic("reason")` or runtime assertions (`assert(condition)`).
- When a panic occurs:
  1. An informative error message with source file, line number, column, and stack backtrace is printed to `stderr`.
  2. If the panic occurred inside an isolated task within a concurrency `scope`, the panic is caught at the nursery boundary, cancelling sibling tasks and returning an unhandled failure to the parent.
  3. If unhandled at the root task, the process immediately terminates with non-zero exit code (default: 101).

### 5.2 Panic Strategy Configuration
The compiler provides two build strategies:
- `--panic=abort` (Default for performance-critical systems): Panics immediately terminate the process via a hardware trap or `abort()`, eliminating stack-unwinding metadata.
- `--panic=unwind`: Panics walk the stack and invoke `Drop::drop` on live stack objects before terminating or catching at a nursery boundary.

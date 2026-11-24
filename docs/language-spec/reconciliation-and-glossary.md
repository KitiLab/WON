# WON Language Specification: Reconciliation, Audit and Architectural Glossary

> **Specification Version**: Draft 0.1  
> **Status**: `SPECIFIED` (Under Architectural Review)  
> **Module**: Cross-Document Consistency Review & Decision Registry  
> **Date**: November 2026

---

## 1. Cross-Document Internal Consistency Audit

To ensure the language specification suite is completely cohesive, rigorous, and free from conflicting semantics, an exhaustive cross-document consistency audit was conducted across all 14 critical language dimensions:

### 1. `let` versus `var`
- **Audit Finding**: In Phase 0, the bootstrap compiler used `let mut` (Rust-style), whereas earlier vision documents proposed `var` for mutable variables.
- **Resolution**: Canonical WON formally adopts `let` for immutable bindings and `var` for mutable bindings. This eliminates redundant keywords, improves readability, and aligns with the Python-inspired ergonomic goals. Phase 0 `let mut` is classified as provisional bootstrap syntax.

### 2. Indentation versus Brace-Delimited Blocks
- **Audit Finding**: Phase 0 implementation used curly braces `{ ... }`, while the Master Blueprint proposed indentation blocks (`:`).
- **Resolution**: Indentation-based blocks with `:` are formally specified as canonical WON syntax. Strict whitespace rules (spaces only, 4 spaces per level, tabs forbidden via `WON-SYN-001`) and synthetic `INDENT`/`DEDENT` tokenization guarantee 100% deterministic parsing. Provisional brace parsing in `wonc0` remains in place until Phase 1B parser upgrade.

### 3. `Int` versus Fixed-Width Integer Types
- **Audit Finding**: Earlier sketches used `Int` and `UInt`, while systems code requires explicit fixed-width integers (`i8`..`i128`, `u8`..`u128`).
- **Resolution**: Fixed-width types (`i8`, `i16`, `i32`, `i64`, `i128`, `u8`, `u16`, `u32`, `u64`, `u128`, `isize`, `usize`) are the canonical primitive types. `Int` is formally defined as an alias for `i64` (default integer literal type), and `UInt` as an alias for `u64`.

### 4. `Str` versus Owned Strings and String Views
- **Audit Finding**: Conceptual confusion existed between string views, static literals, and heap-allocated resizable strings.
- **Resolution**: Formally partitioned into three distinct types:
  - `&str`: Borrowed view into UTF-8 bytes (fat pointer: pointer + length).
  - `String`: Owned, heap-allocated, resizable UTF-8 string buffer (pointer + length + capacity).
  - `Str`: Convenient type alias for `&str` when used as an immutable parameter type.

### 5. Copy versus Move Behavior
- **Audit Finding**: Potential ambiguity regarding whether structs with mixed fields are copied or moved.
- **Resolution**: Types implementing `Copy` (primitives, raw pointers, structs where all fields are `Copy`) are bitwise copied. All types managing external heap, file handles, or OS resources do **NOT** implement `Copy` and are moved by default. Reads from moved variables are rejected with `WON-MEM-001`.

### 6. `Shared[T]` versus Thread-Safe `Arc[T]`
- **Audit Finding**: In early notes, `Shared` and `Arc` were occasionally used interchangeably.
- **Resolution**: Formally separated:
  - `Shared[T]`: Single-threaded reference counting (non-atomic). Zero multi-threading overhead. Does not implement `Send` or `Sync`.
  - `Arc[T]`: Multi-threaded atomic reference counting. Thread-safe (`Send + Sync` when `T: Send + Sync`).

### 7. Borrowing versus Returned References (No Lifetime Annotations)
- **Audit Finding**: Claiming memory safety without Rust-style `'a` annotations could lead to unsound dangling references if not formally constrained.
- **Resolution**: Formally bounded: Borrowed references cannot be stored in returned structs without explicit ownership (`Shared`/`Arc`). Single input references automatically propagate to return references. Ambiguous multi-input reference returns in free functions are rejected at compile time (`WON-MEM-002`) requiring explicit output destination parameters.

### 8. Ownership versus C Memory Management
- **Audit Finding**: C libraries do not communicate ownership, risking premature freeing or memory leaks.
- **Resolution**: All imported C pointers are treated as unmanaged raw pointers (`*const T` or `*mut T`) by default. Passing WON buffers to C requires explicit `.as_ptr()` or `.as_mut_ptr()` views with mandatory capacity assertions before foreign invocations.

### 9. Async Operations versus Structured Concurrency
- **Audit Finding**: Traditional async/await often permits detached, orphaned tasks that outlive parent scopes.
- **Resolution**: Structured Concurrency via nursery scopes (`scope s:`) is the normative concurrency model. All spawned tasks (`s.spawn:`) must complete or be cancelled before the scope exits, completely eliminating dangling background threads.

### 10. Error Propagation (`?`) versus Destruction (RAII)
- **Audit Finding**: Error returns could bypass destructor drops if stack unwinding is not strictly defined.
- **Resolution**: The `?` operator is defined as an immediate early return accompanied by deterministic LIFO destruction of all live stack variables in the enclosing scope before the error value is returned.

### 11. Generics versus Monomorphization
- **Audit Finding**: Risk of runtime dispatch overhead or virtual table bloat with generic functions.
- **Resolution**: Generics are 100% monomorphized at compile time into concrete, specialized functions and structs at the TIR level, guaranteeing zero virtual dispatch penalty.

### 12. Unsafe Boundaries versus Generated FFI Wrappers
- **Audit Finding**: Question whether generated C bindings should automatically mark everything safe.
- **Resolution**: All foreign C functions remain classified as `unsafe` by default (`WON-SEC-001`). Safe ergonomic wrappers can be written in WON or generated with explicit buffer safety invariants.

### 13. Self-Hosting versus Minimum Standard-Library Requirements
- **Audit Finding**: Self-hosting could prematurely stall if it required the entire finished standard library.
- **Resolution**: Pragmatic bootstrap matrix: Only 14 core compiler prerequisites (strings, vectors, maps, AST structs/enums, recursion, basic I/O, error propagation, process execution) are required for Stage 1 self-hosting. Advanced concurrency and rich ecosystem libraries are deferred to Phase 3.

### 14. C Backend versus Future Native Backend
- **Audit Finding**: Risk of coupling the language definition to C generation quirks.
- **Resolution**: Architectural independence: Typed Intermediate Representation (TIR) represents pure WON semantics. The C11 backend is merely one code generation target (`C11Emitter`), preserving an identical interface for future LLVM and QBE backends.

---

## 2. Architectural Glossary

| Term | Formal Definition in WON Language Specification |
| :--- | :--- |
| **AOT Compilation** | Ahead-of-Time compilation directly to native machine code prior to execution. |
| **Borrowing** | Temporarily accessing a value via an immutable (`&T`) or mutable (`&mut T`) reference without taking ownership. |
| **Copy Semantics** | Implicit duplication of a value's bits during assignment without invalidating the source variable. Restricted to simple, stack-only types. |
| **DEDENT** | A synthetic token emitted by the lexer when indentation level decreases to match a previous scope on the indentation stack. |
| **Fat Pointer** | A two-word runtime structure consisting of a data pointer paired with auxiliary metadata (e.g., length for `&[T]`, or vtable pointer for `dyn Trait`). |
| **INDENT** | A synthetic token emitted by the lexer when indentation level increases after a block introducer (`:`). |
| **Monomorphization**| The compile-time process of generating distinct, specialized machine code instances for each concrete type used with a generic function or struct. |
| **Move Semantics** | The transfer of ownership of a value from one variable or scope to another, immediately invalidating the source variable. |
| **Nursery Scope** | A lexical block (`scope s:`) that bounds the lifetime of all concurrently spawned child tasks. |
| **Ownership-Lite** | Compile-time memory safety enforcing single ownership and borrowing invariants without requiring user-annotated lifetime parameters (`'a`). |
| **RAII** | Resource Acquisition Is Initialization: deterministic destruction of resources when the owning variable goes out of scope. |
| **Structured Concurrency**| Concurrency paradigm where concurrent tasks are strictly nested inside lexical scopes, ensuring no task outlives its parent. |
| **TIR** | Typed Intermediate Representation: a language-independent, control-flow-graph-based intermediate language used for optimization and code generation. |
| **Zero-GIL** | Multi-threaded execution environment operating without a Global Interpreter Lock, fully utilizing all available CPU hardware cores. |

---

## 3. Design Decision Register & Owner Approval Matrix

The table below summarizes the formal architectural decisions proposed in Phase 1A. All items marked as requiring owner approval await review from KitiLab leadership prior to Phase 1B implementation:

| Topic / Decision | Recommendation | Architectural Rationale | Status | Owner Approval Required? |
| :--- | :--- | :--- | :--- | :---: |
| **Syntax & Block Scoping** (ADR-007) | Option A: Indentation-based blocks with `:`, `let`/`var` | Maximizes visual clarity, delivers Python-like ergonomics, eliminates delimiter noise. | `PROPOSED` | **YES** |
| **Whitespace Rules** (ADR-007) | Strictly 4 spaces; tabs forbidden (`WON-SYN-001`) | Eliminates ambiguity; ensures 100% deterministic indentation tokenization. | `PROPOSED` | **YES** |
| **Mutability Vocabulary** | `let` (immutable) and `var` (mutable) | Replaces verbose `let mut`; clear cognitive distinction for developers and AI agents. | `PROPOSED` | **YES** |
| **Integer Types** | Fixed-width `i8`..`i128`, `u8`..`u128`, `Int = i64` | Guarantees predictable machine representation and eliminates platform width bugs. | `PROPOSED` | **YES** |
| **Ownership-Lite Engine** (ADR-003) | Scope-bounded region inference, elision rules, no `'a` | Complete memory safety without polluting APIs with viral lifetime annotations. | `PROPOSED` | **YES** |
| **Concurrency Model** (ADR-008) | Structured concurrency (`scope`/`spawn`), Zero-GIL, `Send`/`Sync` | Eliminates thread leaks; enables stack borrowing into child tasks; safe multi-core execution. | `PROPOSED` | **YES** |
| **Error Handling** | `Result[T, E]`, `Option[T]`, `?` operator | Predictable control flow; zero hidden exceptions; deterministic RAII destruction. | `PROPOSED` | **YES** |
| **C FFI Ingestion** (ADR-006) | Direct `#import <header.h>`, composite dependency caching | True "Any C library" zero-friction interop with instant incremental compilation. | `ACCEPTED` (0B.2) | Done |
| **FFI Safety Boundary** | Mandatory `unsafe:` block for foreign C calls | Explicit, auditable safety boundary; prevents foreign memory corruption in safe code. | `ACCEPTED` (0B.2) | Done |
| **Intermediate Rep** (ADR-009) | Basic-block CFG Typed Intermediate Rep (TIR) | Decouples language frontend from backends; enables LLVM/QBE addition and optimization. | `PROPOSED` | **YES** |
| **Self-Hosting Roadmap** (ADR-010) | 4-stage bootstrap; retain Go until 10 criteria met | Dogfoods WON for systems development; achieves permanent toolchain independence. | `PROPOSED` | **YES** |
| **C Codegen Backend** (ADR-001) | Initial C11 emission; retain GCC/Clang during Stage 1 | Allows Go retirement before GCC/Clang retirement; harnesses mature native optimizers. | `ACCEPTED` (0A) | Done |
| **Process Sandboxing** | Linux Landlock/seccomp, macOS Seatbelt | Defense-in-depth: protects host system against untrusted or vulnerable native C code. | `PROPOSED` | **YES** |
| **Language Editions** | 3-year edition cycles (`2026`, `2029`) in `won.toml` | Permits syntax evolution without breaking ecosystem backwards compatibility. | `PROPOSED` | **YES** |

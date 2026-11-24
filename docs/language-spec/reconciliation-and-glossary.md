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
- **Resolution**: Formally partitioned into a rigorous 3-tier string architecture:
  - `str`: Dynamically Sized Type (DST) representing an unsized sequence of valid UTF-8 code units in memory. Never instantiated directly as a value.
  - `&str`: Immutable borrowed view into a UTF-8 sequence (16-byte fat pointer: 8-byte pointer + 8-byte byte length).
  - `String`: Owned, heap-allocated, resizable UTF-8 buffer (24 bytes: 8-byte pointer, 8-byte length, 8-byte capacity).
  - `Str`: Convenient type alias for `&str` when used as an immutable function parameter type.

### 5. Copy versus Move Behavior
- **Audit Finding**: Potential ambiguity regarding whether structs with mixed fields are copied or moved.
- **Resolution**: Types implementing `Copy` (primitives, raw pointers, structs where all fields are `Copy`) are bitwise copied. All types managing external heap, file handles, or OS resources do **NOT** implement `Copy` and are moved by default. Reads from moved variables are rejected with `WON-MEM-001`.

### 6. `Shared[T]` versus Thread-Safe `Arc[T]`
- **Audit Finding**: In early notes, `Shared` and `Arc` were occasionally used interchangeably.
- **Resolution**: Formally separated:
  - `Shared[T]`: Single-threaded reference counting (non-atomic). Zero multi-threading overhead. Does not implement `Send` or `Sync`.
  - `Arc[T]`: Multi-threaded atomic reference counting. Thread-safe (`Send + Sync` when `T: Send + Sync`).

### 7. Borrowing versus Returned References (Provenance Tracking Without Lifetime Annotations)
- **Audit Finding**: Claiming memory safety without Rust-style `'a` annotations risked unsoundness if multiple input references could escape or if receiver provenance was assumed without proof.
- **Resolution**: Formally grounded via Reference Provenance ($\text{Prov}(e)$):
  - Provenance analysis tracks origins at compile time without surface syntax annotations.
  - Single-reference input elision is verified against return expression provenance: $\text{Prov}(r_{\text{ret}}) \subseteq \{p\} \cup \{\text{static}\}$.
  - Provenance-driven receiver rules: `&self` does not unconditionally bind return references if the return provenance actually points to another argument.
  - Functions returning references with multiple input reference parameters without provable origin are conservatively rejected at compile time (`WON-MEM-002`), requiring callers to supply an explicit output reference or use owned types.

### 8. Ownership versus C Memory Management & Sidecar Contracts
- **Audit Finding**: C libraries do not communicate memory ownership, nullability, or buffer sizes, risking premature freeing or memory corruption.
- **Resolution**: Raw C imports treat all pointers as unmanaged `*const T` or `*mut T` by default (`WON-SEC-001`). To enable safe, ergonomic interop without manual wrappers, WON specifies declarative sidecar safe binding contracts (`.wonbind`) that attach `@owned`, `@borrow`, `@out_buffer`, and `@nullability` contracts to foreign C declarations.

### 9. Async Operations versus Structured Concurrency
- **Audit Finding**: Traditional async/await often permits detached, orphaned tasks that outlive parent scopes, and early drafts claimed unverified "100% CPU utilization" guarantees.
- **Resolution**: Structured Concurrency via nursery scopes (`scope s:`) is the normative concurrency model. Tasks are spawned via `s.spawn: Block`, returning typed `TaskHandle[T]` for cooperative cancellation and join semantics. Capture of stack references requires `T: Sync`. Blocking C system calls are offloaded to dedicated OS worker threads. Unhandled task failures aggregate into `AggregateTaskError`.

### 10. Error Propagation (`?`) versus Destruction (RAII)
- **Audit Finding**: Error returns could bypass destructor drops, and type conversion behavior was under-specified.
- **Resolution**: The `?` operator performs type conversion via `From`/`Into` traits, conservatively rejecting ambiguous conversions (`WON-ERR-002`). Unwinding executes deterministic LIFO destruction of all initialized local stack bindings. Double panic during destruction triggers an immediate unrecoverable abort. Panics across C ABI boundaries are strictly trapped and contained.

### 11. Generics versus Monomorphization
- **Audit Finding**: Risk of runtime dispatch overhead or virtual table bloat with generic functions.
- **Resolution**: Generics are monomorphized 100% at compile time into specialized TIR functions. Dynamic trait dispatch is strictly opt-in via explicit `dyn Trait` fat pointers.

### 12. Unsafe Boundaries versus Generated FFI Wrappers
- **Audit Finding**: Question whether generated C bindings should automatically mark everything safe.
- **Resolution**: All foreign C functions remain classified as `unsafe` by default (`WON-SEC-001`). Safe ergonomic wrappers can be written in WON or generated via `.wonbind` contracts with explicit buffer safety invariants.

### 13. Self-Hosting versus Minimum Standard-Library Requirements
- **Audit Finding**: Self-hosting could prematurely stall if it required the entire finished standard library.
- **Resolution**: Pragmatic bootstrap matrix: Only 14 core compiler prerequisites (strings, vectors, maps, AST structs/enums, recursion, basic I/O, error propagation, process execution) are required for Stage 1 self-hosting. Convergence is mathematically defined as $\text{Emit}(\text{wonc1}, \text{Source}) \equiv \text{Emit}(\text{wonc2}, \text{Source})$.

### 14. C Backend versus Future Native Backend
- **Audit Finding**: Risk of coupling the language definition to C generation quirks or LLVM-specific leaky abstractions (e.g. `getelementptr`).
- **Resolution**: Architectural independence: Typed Intermediate Representation (TIR) represents pure WON semantics using neutral operations like `ComputeAddress(dest, base, path)`. The C11 backend is merely one code generation target (`C11Emitter`), preserving an identical interface for future LLVM and QBE backends.

---

## 2. Architectural Glossary

| Term | Formal Definition in WON Language Specification |
| :--- | :--- |
| **AOT Compilation** | Ahead-of-Time compilation directly to native machine code prior to execution. |
| **Borrowing** | Temporarily accessing a value via an immutable (`&T`) or mutable (`&mut T`) reference without taking ownership. |
| **ComputeAddress** | Abstract typed memory address calculation in TIR representing aggregate field offsets or array indexing without performing a dereference. |
| **Cooperative Cancellation** | Task termination mechanism where tasks observe cancellation signals via tokens or yield points, permitting orderly local resource cleanup. |
| **Copy Semantics** | Implicit duplication of a value's bits during assignment without invalidating the source variable. Restricted to simple, stack-only types. |
| **DEDENT** | A synthetic token emitted by the lexer when indentation level decreases to match a previous scope on the indentation stack. |
| **Fat Pointer** | A two-word runtime structure consisting of a data pointer paired with auxiliary metadata (e.g., length for `&[T]`, or vtable pointer for `dyn Trait`). |
| **INDENT** | A synthetic token emitted by the lexer when indentation level increases after a block introducer (`:`). |
| **Monomorphization**| The compile-time process of generating distinct, specialized machine code instances for each concrete type used with a generic function or struct. |
| **Move Semantics** | The transfer of ownership of a value from one variable or scope to another, immediately invalidating the source variable. |
| **Nursery Scope** | A lexical block (`scope s:`) that bounds the lifetime of all concurrently spawned child tasks. |
| **Ownership-Lite** | Compile-time memory safety enforcing single ownership and borrowing invariants without requiring user-annotated lifetime parameters (`'a`). |
| **Provenance** | The static set of origin parameters or memory regions that can yield a given reference value ($\text{Prov}(e)$). |
| **RAII** | Resource Acquisition Is Initialization: deterministic destruction of resources when the owning variable goes out of scope. |
| **Sidecar Contract** | Declarative metadata (`.wonbind`) specifying memory ownership, capacity relationships, and nullability contracts for C header declarations. |
| **Structured Concurrency**| Concurrency paradigm where concurrent tasks are strictly nested inside lexical scopes, ensuring no task outlives its parent. |
| **TIR** | Typed Intermediate Representation: a language-independent, control-flow-graph-based intermediate language used for optimization and code generation. |
| **Unsized Type (DST)**| A Dynamically Sized Type whose byte size is known only at runtime through accompanying metadata (e.g., `str`, `[T]`). |
| **Zero-GIL** | Multi-threaded execution environment operating without a Global Interpreter Lock, permitting parallel execution of independent tasks. (Actual CPU utilization depends on workload characteristics, OS scheduling, hardware availability, and program architecture). |

---

## 3. Design Decision Register & Owner Approval Matrix

Governance in the WON project strictly distinguishes between three distinct architectural tiers:
1. **Accepted High-Level Architectural Foundations (ADR-001 through ADR-006)**: Foundational principles formally approved by KitiLab project leadership during Phase 0.
2. **Phase 1 Detailed Specifications**: Elaborations (such as reference provenance analysis $\text{Prov}(e)$, the 3-tier string model, and sidecar `.wonbind` contracts) that flesh out the accepted foundations. These are `SPECIFIED` / `UNDER INVESTIGATION` until implemented and verified in the compiler.
3. **Proposed Architectural Decision Records (ADR-007 through ADR-010)**: Major architectural proposals introduced in Phase 1A that await formal KitiLab owner review and approval prior to Phase 1B implementation.

### Canonical Architectural Decision Records (ADR-001 through ADR-010)

| ADR ID | Canonical Title | Scope | Recommendation / Summary | Existing Status | Owner Approval Required? |
| :--- | :--- | :--- | :--- | :--- | :---: |
| **ADR-001** | C Code-Generation Backend | Backend | Emit ISO C11 initially; evaluate LLVM/QBE in Phase 5 | `ACCEPTED` (Phase 0A) | Done |
| **ADR-002** | Compiler Implementation Language — Go Bootstrap | Frontend | Go 1.22+ for initial bootstrap prototype (`wonc0`) | `ACCEPTED` (Scoped to Phase 0B) | Done |
| **ADR-003** | Ownership-Lite Memory Model | Type System | Memory safety without explicit lifetime parameters (`'a`) | `ACCEPTED` (Core Direction) | Detailed borrowing passes: `PROPOSED` |
| **ADR-004** | AI-Independent Compiler Query Interface | Diagnostics | Neutral JSON-RPC and CLI diagnostic stream without vendor lock-in | `ACCEPTED` (Phase 0A) | Done |
| **ADR-005** | Public Benchmark Methodology and Integrity Rules | Governance | Fair, transparent, reproducible benchmarks against native baselines | `ACCEPTED` (Phase 0A) | Done |
| **ADR-006** | C Header Parsing Strategy | Toolchain | Direct `#import <header.h>`, libclang AST discovery and caching | `ACCEPTED` (Phase 0B.2) | Done |
| **ADR-007** | Formal Syntax and Indentation-Based Grammar | Grammar | Python-inspired indentation blocks (`:`), 4-space indent, `let`/`var` | `PROPOSED` | **YES** |
| **ADR-008** | Structured Concurrency and Zero-GIL | Runtime | Lexical nursery scopes (`scope`/`spawn`), work-stealing, parallel tasks | `PROPOSED` | **YES** |
| **ADR-009** | Typed Intermediate Representation (TIR) Architecture | Compiler | Basic-block CFG TIR with `ComputeAddress`, multi-backend abstraction | `PROPOSED` | **YES** |
| **ADR-010** | Compiler Self-Hosting and Bootstrap Pipeline | Toolchain | 4-stage bootstrap pipeline, Go retirement criteria | `PROPOSED` | **YES** |

> [!IMPORTANT]
> **ADR-003 Note**: ADR-003 accepts the ownership-lite architecture as an overarching project direction. It does **not** signify that every detailed borrowing pass or provenance algorithm proposed during Phase 1 has already been approved, implemented, or proven sound. Specific borrow-checker passes remain `SPECIFIED` / `UNDER INVESTIGATION` pending implementation in Phase 2.
>
> **Proposals Gate**: ADR-007 through ADR-010 remain strictly **`PROPOSED`** until KitiLab owner review. Including a feature in a specification document does not constitute architectural acceptance.

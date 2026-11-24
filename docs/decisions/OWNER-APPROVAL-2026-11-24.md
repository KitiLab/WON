# KitiLab Owner Architectural Approval Record (2026-11-24)

> **Record ID**: OWNER-APPROVAL-2026-11-24  
> **Organization**: KitiLab  
> **Project**: WON Programming Language  
> **Approval Date**: November 24, 2026  
> **Scope**: Ratification of ADR-007 through ADR-010  
> **Authoritative Statement**: *"Approve the four architectural directions, subject to the technical clarifications described above."*

---

## 1. Executive Decision Summary

On **November 24, 2026**, the KitiLab project owner formally reviewed and approved the four foundational architectural decisions proposed in Phase 1A (**ADR-007**, **ADR-008**, **ADR-009**, and **ADR-010**), transitioning their governance status from `PROPOSED` to `ACCEPTED`.

This record establishes the binding architectural direction for subsequent compiler implementation phases while maintaining strict engineering clarity regarding what is authorized, what technical conditions apply, and which features remain unimplemented.

---

## 2. Ratified Decisions & Technical Clarifications

### 2.1 ADR-007: Formal Syntax and Indentation-Based Grammar Strategy
- **Status**: `ACCEPTED` (Owner Approval: November 24, 2026)
- **Approved Scope**:
  - Python-inspired indentation-based statement blocks.
  - Colon (`:`) as the canonical block body introducer.
  - Strict four-space indentation levels (tabs forbidden in leading indentation via `WON-SYN-001`).
  - Synthetic `INDENT`, `DEDENT`, and `NEWLINE` lexer tokens produced via the off-side rule.
  - `let` declares immutable bindings; `var` declares mutable bindings.
  - **No interchangeable brace-based block syntax**: Brace-delimited blocks (`{ ... }`) from the Phase 0 prototype are provisional and MUST be retired and migrated to canonical indentation syntax.
  - Braces remain permitted strictly where defined in the formal grammar for expressions (e.g., struct literals, string interpolation, set/map literals).
- **Authorized Phases**: Immediately authorizes **Phase 1B** (Canonical Lexer & Recursive-Descent/Pratt Parser implementation in `wonc0`).
- **Currently Unimplemented**: Indentation lexer, canonical AST/parser, complete type system, semantic passes for all parsed constructs.

---

### 2.2 ADR-008: Structured Concurrency and Zero-GIL Concurrency Model
- **Status**: `ACCEPTED` (Owner Approval: November 24, 2026)
- **Approved Scope**:
  - Lexically scoped task nurseries (`scope s:`).
  - Structured task lifetimes ensuring child tasks cannot outlive their parent scope.
  - Compile-time data-race safety model based on `Send` and `Sync` traits.
  - No Global Interpreter Lock (Zero-GIL), allowing independent tasks to execute in parallel.
  - Safe cross-task stack borrowing, strictly subject to static lifetime, provenance, and `Sync` verification.
- **Technical Conditions & Clarifications**:
  - An M:N scheduler and work-stealing runtime are implementation strategies, **not** mandatory permanent language guarantees.
  - WON does **not** guarantee "100% CPU utilization" (actual throughput depends on workload, OS scheduler, hardware, and program design).
  - Child tasks may borrow stack data from enclosing parent scopes only when the compiler can statically verify valid lifetime bounds and `Sync` requirements.
  - Task cancellation, joining, and error aggregation semantics must adhere strictly to the formal specification ([concurrency-model.md](../language-spec/concurrency-model.md)).
- **Authorized Phases**: Authorizes future runtime and semantic design in **Phase 3**; runtime implementation is explicitly excluded from **Phase 1B**.
- **Currently Unimplemented**: `Send`/`Sync` trait checker, nursery runtime scheduler, cooperative cancellation runtime, and cross-task borrow verification.

---

### 2.3 ADR-009: Typed Intermediate Representation (TIR) Architecture
- **Status**: `ACCEPTED` (Owner Approval: November 24, 2026)
- **Approved Scope**:
  - Dedicated, language-independent Typed Intermediate Representation (TIR).
  - Control-Flow Graph (CFG) / basic-block intermediate structure.
  - Explicit IR representation of concrete types, ownership transfers (`Move`), borrow bounds, memory address calculations (`ComputeAddress`), and deterministic destruction (`Drop`).
  - Backend-neutral compiler frontend and mid-end architecture.
- **Technical Conditions & Clarifications**:
  - The ISO C11 code-generation backend (`C11Emitter`) remains the initial implementation target.
  - LLVM and QBE remain future backend evaluations, not immediate commitments.
  - TIR instruction formats and internal structures may evolve as implementation experience reveals better engineering trade-offs.
  - No claim of completed TIR implementation is permitted until it actually exists and passes verification tests.
- **Authorized Phases**: Authorizes TIR pipeline construction in **Phase 2**; TIR implementation is excluded from **Phase 1B**.
- **Currently Unimplemented**: TIR builder pass, basic-block CFG lowering, mid-level optimizer, LLVM/QBE backends.

---

### 2.4 ADR-010: Compiler Self-Hosting and Multi-Stage Bootstrap Pipeline
- **Status**: `ACCEPTED` (Owner Approval: November 24, 2026)
- **Approved Scope**:
  - The existing Go compiler (`compiler/poc/`, `wonc0`) remains the temporary bootstrap compiler.
  - The production WON compiler will be implemented in WON (`compiler/wonc/`).
  - `wonc0` (Go) compiles the first WON-written compiler (`wonc1`).
  - `wonc1` compiles itself to produce `wonc2`.
  - Convergence and reproducibility across compiler generations ($\text{Emit}(\text{wonc1}, \text{Source}) \equiv \text{Emit}(\text{wonc2}, \text{Source})$) determines self-hosting verification.
- **Technical Conditions & Clarifications**:
  - Go must eventually cease to be a mandatory dependency of normal WON development and application build workflows.
  - The Go bootstrap compiler must remain preserved until all ten objective retirement criteria specified in [self-hosting-and-bootstrap.md](../language-spec/self-hosting-and-bootstrap.md) are empirically met.
  - The first self-hosted WON compiler may continue emitting ISO C11.
  - Retiring Go and retiring external C compilers (GCC/Clang) are separate engineering milestones.
  - Native machine-code backend development remains a future milestone.
- **Authorized Phases**: Governs multi-stage transition across **Phase 2 through Phase 5**; Go removal is strictly prohibited in **Phase 1B**.
- **Currently Unimplemented**: Self-hosted WON compiler (`wonc1`), `wonc2`, convergence CI verification.

---

## 3. Architecture Approval vs. Verified Engineering Guarantees

In accordance with `AGENTS.md` governance rules, **an `ACCEPTED` ADR authorizes design direction and implementation effort; it is NOT proof of implemented functionality or verified safety guarantees.**

| Domain | Governance Status | Engineering Implementation Status | Verification Boundary |
| :--- | :--- | :--- | :--- |
| **Canonical Indentation Syntax (ADR-007)** | `ACCEPTED` | Implementing in Phase 1B | Parser AST construction validated in Phase 1B; full semantic validation deferred to Phase 2. |
| **Ownership-Lite & Borrowing (ADR-003)** | `ACCEPTED` (Core Direction) | `NOT IMPLEMENTED` (Pass) | Borrow-checker and provenance algorithms remain `SPECIFIED` / `UNDER INVESTIGATION`. |
| **Structured Concurrency (ADR-008)** | `ACCEPTED` | `NOT IMPLEMENTED` | Only parsing of syntax (`scope`/`spawn`) is covered in Phase 1B; runtime is Phase 3. |
| **Typed IR (TIR) (ADR-009)** | `ACCEPTED` | `NOT IMPLEMENTED` | Current PoC lowers AST directly to C11; TIR pipeline is Phase 2. |
| **Self-Hosting (ADR-010)** | `ACCEPTED` | `NOT IMPLEMENTED` | Bootstrap compiler remains Go (`wonc0`). |
| **Phase 0 C11 Backend & Zlib FFI (ADR-001, ADR-006)** | `ACCEPTED` | `IMPLEMENTED` | Verified via strict C11 `-Werror` compilation and byte-for-byte runtime execution. |

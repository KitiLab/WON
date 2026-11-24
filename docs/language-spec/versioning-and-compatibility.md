# WON Language Specification: Versioning and Compatibility

> **Specification Version**: Draft 0.1  
> **Status**: `SPECIFIED` (Under Architectural Review)  
> **Module**: Evolutive Governance, Stability & Editions  
> **Date**: November 2026

---

## 1. Multi-Tier Versioning Model

To ensure language evolution without breaking existing production software or disrupting automated AI coding pipelines, WON formalizes three distinct versioning tiers:

```
                            Versioning Taxonomy
                                     │
         ┌───────────────────────────┼───────────────────────────┐
         ▼                           ▼                           ▼
1. Specification Version    2. Compiler Release Version    3. Language Editions
   • Draft 0.1 (Current)       • SemVer 2.0.0 (e.g. 0.2.0)    • Proposed: 3-Year Epochs
   • Formal normative rules    • Implementation binary        • Opt-in syntactic evolution
   • Project Architect scope   • Toolchain release cycle      • Subject to Owner Approval
```

1. **Specification Version (`Draft X.Y`)**: Tracks the formal normative rules defined in this specification suite.
2. **Compiler Release Version (`Major.Minor.Patch`)**: Adheres strictly to [Semantic Versioning 2.0.0](https://semver.org/):
   - `Patch`: Bug fixes, optimization enhancements, diagnostic improvements (100% backwards-compatible).
   - `Minor`: New language features, new standard library modules, new toolchain flags (backwards-compatible within an edition).
   - `Major`: Breaking API changes or fundamental architectural transitions.
3. **Language Editions (`edition = "YYYY"`)**: Cohesive language epochs that allow opt-in syntax cleanups and deprecation removals without breaking interoperability with packages written in older editions.
   - *Governance Status*: A 3-year epoch cadence (e.g. `2026`, `2029`) is currently **`PROPOSED`** and subject to formal project owner review and acceptance before implementation in Phase 2.

---

## 2. Source and Binary Compatibility Rules

### 2.1 Public API Stability vs Internal Binary ABI
- **API Stability (Source Level)**: Standard library modules and language constructs marked `pub` commit to non-breaking API stability within a major compiler version. Deprecated functions are tagged with `@deprecated(message, since_version, removal_version)` and produce compiler warnings for at least one full minor release cycle prior to removal.
- **ABI Boundary Distinction**:
  - **Foreign C ABI Boundary (`@repr(C)`)**: Structs, functions, and callbacks annotated with `@repr(C)` maintain 100% frozen, target-platform ABI stability matching the host C compiler and platform calling convention. Foreign C libraries and external callers can rely indefinitely on `@repr(C)` interfaces.
  - **Internal WON Binary ABI**: The internal binary ABI of pure WON functions (name mangling, register calling conventions, unannotated struct field layout and padding, fat-pointer representations) is **explicitly unstable** across compiler versions. Two compilation units built with different minor compiler versions or different flags MUST NOT link together via internal calling conventions; they must communicate across `@repr(C)` interfaces or be recompiled together from source or stable TIR bytecode. This preserves the compiler's freedom to optimize calling conventions, register assignments, and data packing without breaking long-term ecosystem stability.

---

## 3. The Edition Mechanism

Every package declares its language edition in its `won.toml` manifest:

```toml
[package]
name = "my-service"
edition = "2026"
```

- **Edition Boundaries**: Editions group evolutionary syntactic changes (e.g., keyword reservations, syntax refinements).
- **Interoperability Invariant**: Packages written in `edition = "2026"` can seamlessly import and link against packages written in `edition = "2029"` within the same binary. The compiler lowers all editions into a common, unified Typed Intermediate Representation (TIR).

---

## 4. C Header Binding Regeneration Policy

Because system C headers evolve across operating system updates (e.g., glibc or zlib version bumps):
- C bindings generated via `#import` are cached with composite dependency fingerprints.
- If a system header package is upgraded, the composite fingerprint changes, triggering an automatic, transparent background re-extraction without manual developer intervention.

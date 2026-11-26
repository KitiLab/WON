# Phase 2B.2.1 — Reference Mutability, Reborrow & Provenance Soundness Closure

> **Document Status**: `IMPLEMENTED` (Verified in compiler unit, CLI, and fuzz suites at [`7d6b27b`](https://github.com/KitiLab/WON/commit/7d6b27ba0b67a1e762830685757b11365c696b31), CI run `45822885882`; further hardened in [Phase 2B.2.2](phase-2b.2.2-authority-and-invariant-closure.md))  
> **Milestone**: Phase 2B.2.1 (Compiler Correctness Hardening Prior to Phase 2B.3)  
> **Governing Specification**: [ADR-011](../decisions/ADR-011-ownership-and-borrow-checking-semantics.md) (`ACCEPTED`), [phase-2b.2-borrow-checker-nll-provenance.md](phase-2b.2-borrow-checker-nll-provenance.md), [ownership-and-borrowing.md](../language-spec/ownership-and-borrowing.md)  
> **Scope Exclusions**: Phase 2B.3 (`Drop` elaboration, drop flags, `.wonbind` cleanup), general `trait` / `impl Drop` / generic `Copy`, two-phase borrows, closures, and concurrency runtime remain deferred.

---

## 1. Closed Soundness Gaps

Phase 2B.2.1 closes four correctness gaps discovered during public-source review before beginning Phase 2B.3:

### 1.1 Reference Mutability & Reborrow Authority (`Critical Issue A`)

1. **Binding vs Dereference Authority (`checker/expressions.go`, `checker/statements.go`)**:
   - Separated **slot mutability** (`sym.IsMut`: whether `let`/`var` allows rebinding or direct owned `&mut x`) from **dereferenced reference mutability** (`info.IsMut`: whether `&mut T` vs `&T` permits mutating or mutably reborrowing the target).
   - Replaced shallow `*ast.Identifier`-only checks in `checkBorrowExpr` and buggy `isLValueRootMutable` with unified recursive helpers: `analyzePlaceMutability`, `analyzeDerefTargetMutability`, and `analyzeRefCarrierChain` — covering `*p`, `p.field`, `(*p).field`, `a[i]`, nested dereferences `**pp` (`&mut &T` and `&(&mut T)`), copied shared refs, and `var` bindings holding `&T`.
   - Fixed `checkAssignStmt` for auto-deref `FieldAccessExpr` / `IndexExpr` (`var r = &h; r.value = 10` -> `WON-ADT-004`) and nested deref LHS (`*(*pp) = 42` -> `WON-MUT-001`).
2. **CFG / Borrow Checker Defense-in-Depth (`borrow/loans.go`, `borrow/liveness.go`)**:
   - Removed the premature `if len(activeAfter) == 0 { return }` bypass in `checkStmtBorrowConflicts` so mutable reborrows (`OpReborrow`) and dereferenced writes (`OpAssign`/`OpReinit` with `HasDeref()`) validate parent origin authority even when no local loan is active (for example `fn bad(p: &i32): let q = &mut *p`).
   - Added `hasMutableReborrowAuthority` / `paramDerefChainMutable` and multi-deref carrier lookup `lookupReborrowParentOrigins` (allowing valid `let p = &mut x; let pp = &mut p; let q = &mut **pp` without false `WON-INT-001`).

#### Canonical Diagnostic Matrix (Mutability & Reborrowing)

| Program Pattern | Enforced Layer | Diagnostic Code |
| :--- | :--- | :--- |
| Mutable reborrow through shared ref (`&mut *p`, `var r = &x; &mut *r`, `&mut h.val` on `&Holder`, `&mut **pp` on `&(&mut T)` / `&mut &T`) | `checker` + `borrow/liveness` | **`WON-MEM-004`** |
| Mutable borrow/mutation of owned immutable `let` binding (`let x = 1; &mut x`, `s.push_str("!")` on `var r = &s`, `*(*pp) = 42`) | `checker` | **`WON-MUT-001`** |
| Writing struct field via `var r = &h; r.field = v` or non-`var` struct field | `checker` (`checkAssignStmt`) | **`WON-ADT-004`** |
| Reborrowing mutable `&mut *p` through `let p = &mut x` or `h: &mut Holder` | `checker` + `borrow` | **ALLOWED** |

---

### 1.2 Canonical `self` Parameter Indexing (`Critical Issue B`)

- **Root Cause Eliminated**: `SymMethod` stores explicit parameters in `ParamTypes` while `self` metadata resides in `HasSelf`/`SelfIsRef`/`SelfIsMut`/`OwnerType`, whereas `cfg/builder.go` registers `self` at `ParamIdx = 0` and prepends `&self`/`&mut self` at `ArgTemps[0]`.
- **Canonical Resolution (`symbols/symbol.go`)**: Added `EffectiveParamCount()`, `EffectiveParamType(idx, tctx)`, and `EffectiveParamName(idx)`. Updated `checkReturnOriginsValid`, `checkOpaqueSignatures`, SCC convergence budgets, and parameter chain checks to use these canonical helpers — eliminating the off-by-one shift where `ParamIdx = 0` inspected explicit arg 0 and explicit last arg went out of bounds.

---

### 1.3 Per-Field Aggregate Return Provenance & Fail-Closed Container Gate (`Critical Issue C`)

- **Structured Projection Summaries (`borrow/provenance.go`, `borrow/loans.go`)**:
  - Extended `FuncSummary` with `FieldProvs map[string]*FieldProvenance` keyed by canonical projection path (`""` for direct `&T`/`&mut T`, `"left"` / `"[0]"` / nested `"box.left"` for `Struct` and `Tuple` aggregates), tracking `IsMut`, `ParamIndices`, `HasStatic`, and `Unknown` independently per leaf.
  - Propagated nested `OpAggregate`, `OpCopy`/`OpMove` of local aggregates, parameter initialization (`OpInitAssign`), and `OpCall` field instantiation into `carrierEnv.aggFields` and `places`, preserving distinct NLL lifetimes per returned field (e.g. `(&i32, &mut i32)`).
  - Enforced per-field `&mut` origin authority (`WON-MEM-004`), local escape rejection (`WON-MEM-003`), per-mutable-field candidate disjointness (`WON-MEM-005`), and cross-field alias disjointness across distinct parameters (`WON-MEM-005` / `WON-MEM-004`).
- **Fail-Closed Gate on Unprojectable Reference Aggregates**:
  - Returns containing references inside **arrays (`[&T; N]`)**, **slices**, or **enums other than pure immutable `&str` (`Result[T, &str]`)** (e.g., `Option[&mut T]`, custom `enum RefBag { A(&String), B(&mut String) }`) are rejected cleanly with **`WON-SEM-010`** rather than flattening provenance or losing mutability.

---

### 1.4 Opaque & Function-Pointer Boundary Hardening (`Section 5`)

- Removed the overly permissive `ReturnType != StrView` exemption in `checkOpaqueSignatures`: bodyless declarations and `fn(...)` function pointers with **0** reference inputs returning `&str` (as well as any `&T`/`&mut T`) are now rejected with **`WON-MEM-002`**, preventing unverified non-literal `&str` sources from assuming `Static`.
- Extended `checkOpaqueSignatures` to inspect `KindFn` types stored inside `Struct` fields and `Enum` variant payloads, bodyless `SymMethod` receivers via `EffectiveParamCount()`, and single-shared-ref->mut opaque signatures (**`WON-MEM-004`**).
- Hardened call-site opaque validation (`checkCallProvenanceAndDisjointness`) to reject opaque calls returning references with either `0` or `>= 2` reference arguments (**`WON-MEM-002`**), while preserving builtin/variant constructors (`as_bytes`, `Result::Ok/Err`, `Option::Some/None`).

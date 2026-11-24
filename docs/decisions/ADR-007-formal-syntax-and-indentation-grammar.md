# ADR-007: Formal Syntax and Indentation-Based Grammar Strategy

## Metadata
- **Decision ID**: ADR-007
- **Title**: Formal Syntax and Indentation-Based Grammar Strategy
- **Status**: PROPOSED (Requires KitiLab Owner Approval)
- **Date**: 2026-11-24
- **Author**: KitiLab Engineering

## Context
During Phase 0, a bootstrap compiler was rapidly developed using C/Rust-style brace syntax (`{ ... }`) and `let mut` for convenience. However, the original WON Master Blueprint specifies a readable, expressive Python-inspired syntax using indentation-based blocks (`:` followed by an indented block) and `let`/`var` for immutability and mutability. We must formally resolve this syntactic divergence.

## Considered Alternatives
1. **Option A: Indentation-Based Blocks (Python-Inspired) — RECOMMENDED**
   - Syntax: Blocks introduced by `:` and indented 4 spaces. Synthetic `INDENT`/`DEDENT` tokens emitted via off-side rule.
   - Bindings: `let` for immutable, `var` for mutable.
   - Pros: Maximum visual clarity, clean ergonomics, aligns with founding vision, familiar to AI coding agents.
   - Cons: Lexer requires indentation tracking; tab characters must be strictly forbidden.
2. **Option B: Retain Phase 0 Brace Delimiters (`{ ... }`)**
   - Syntax: Braces enclose blocks, semicolons terminate statements, `let mut` indicates mutability.
   - Pros: Simpler lexer; standard C-family tradition.
   - Cons: Visually noisy; contradicts the core product differentiation of WON.
3. **Option C: Dual / Permissive Mode**
   - Syntax: Accept both braces and indentation interchangeably.
   - Cons: Severe style fragmentation across codebases; highly complex parser recovery.

## Decision
**Recommend Option A (Indentation-Based Blocks with `let`/`var`) as the canonical syntax of the WON language.**  
The Phase 0 bootstrap compiler retains its brace parser as a provisional tool, while Phase 1B will upgrade the lexer with an off-side `INDENT`/`DEDENT` engine.

## Consequences
- **Positive**: Achieves the expressive, clean aesthetic envisioned in the master product specification.
- **Negative**: Requires upgrading the bootstrap parser in Phase 1B.
- **Owner Approval**: REQUIRED before merging Phase 1B compiler implementation.

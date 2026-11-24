# ADR-008: Structured Concurrency and Zero-GIL Concurrency Model

## Metadata
- **Decision ID**: ADR-008
- **Title**: Structured Concurrency and Zero-GIL Concurrency Model
- **Status**: PROPOSED (Requires KitiLab Owner Approval)
- **Date**: 2026-11-24
- **Author**: KitiLab Engineering

## Context
Concurrent programming in systems software is notoriously prone to data races, deadlocks, and orphaned background threads that outlive their callers. WON requires an ergonomic, highly scalable concurrency model that fully utilizes modern multi-core hardware without a Global Interpreter Lock (GIL) or dangerous detached threads.

## Considered Alternatives
1. **Unstructured OS Threads (C / C++ `std::thread`, POSIX threads)**:
   - *Rejected*: Threads can outlive their stack frames, creating use-after-free bugs and thread leaks.
2. **Unstructured Green Threads / Coroutines (Go `go func()`)**:
   - *Rejected*: Lacks lexical nursery bounds; unhandled goroutine leaks and silent panics are common anti-patterns.
3. **Structured Concurrency with Nurseries (`scope s:`) (Selected / Recommended)**:
   - Tasks are bound to lexical scopes (`scope s:`).
   - Parent scopes suspend until all child tasks finish or are cancelled.
   - Child tasks can safely borrow stack data from parent scopes without heap cloning.
   - Compile-time data-race freedom guaranteed via `Send` and `Sync` traits.

## Decision
**WON adopts Structured Concurrency via nursery scopes (`scope`) and compile-time data race freedom (`Send`/`Sync`) without a Global Interpreter Lock (GIL).**

## Consequences
- **Positive**: Eliminates background thread leaks; enables zero-cost borrowing of stack variables into child tasks; provides clean cooperative cancellation.
- **Negative**: Requires an M:N work-stealing runtime scheduler when concurrency features are linked.
- **Owner Approval**: REQUIRED before implementation in Phase 3.

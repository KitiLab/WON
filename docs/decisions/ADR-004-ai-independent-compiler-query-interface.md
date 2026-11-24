# ADR-004: AI-Independent Compiler Query Interface

## Metadata
- **Decision ID**: ADR-004
- **Title**: AI-Independent Compiler Query Interface
- **Status**: ACCEPTED
- **Date**: 2026-11-24
- **Author**: KitiLab Engineering

## Context
WON is positioned as an "AI-native" programming language. However, tying the compiler to a specific proprietary AI cloud provider (e.g. OpenAI, Anthropic, or Google) would create vendor lock-in, require private API credentials during builds, introduce external network dependencies, and compromise user privacy.

The compiler requires a structured, machine-readable interface that empowers any AI coding agent, local language model, or developer tool without sacrificing autonomy or neutrality.

## Considered Alternatives
1. **Proprietary Vendor API Integration**:
   - *Rejected*: Violates privacy, introduces external network calls into compiler runs, creates commercial dependency.
2. **Standard Human Terminal Text Only**:
   - *Rejected*: Forces AI agents to use fragile regular expressions to parse multiline compiler errors, leading to parsing errors and poor agent ergonomics.
3. **Open Machine-Readable JSON Protocol (Selected)**:
   - Provide `--format=json` across all compiler commands.
   - Emit rich diagnostics including error codes, exact byte/line/column source spans, contextual reasons, and machine-applicable suggested replacement diffs.
   - Provide `won inspect` for symbol, type, and AST queries.

## Decision
**WON adopts a fully open, vendor-agnostic, machine-readable JSON query protocol as its official AI interface.**

All compiler operations can output structured JSON streams over standard I/O (`stdout`). The compiler remains 100% functional offline with zero third-party AI dependencies.

## Consequences
- **Positive**: Complete provider independence; deterministic error parsing for any AI agent; privacy and offline reliability preserved.
- **Negative**: Requires maintaining JSON serialization schemas alongside human-readable error formatting.
- **Impact on Pillars**:
  - *AI-Native Design*: Provides first-class, structured inspection and self-repair capabilities.
  - *Performance*: Zero network overhead during compilation.

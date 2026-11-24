# ADR-005: Public Benchmark Methodology & Integrity Rules

## Metadata
- **Decision ID**: ADR-005
- **Title**: Public Benchmark Methodology & Integrity Rules
- **Status**: ACCEPTED
- **Date**: 2026-11-24
- **Author**: KitiLab Engineering

## Context
Performance claims in the systems programming language landscape frequently suffer from methodological flaws:
- Unoptimized or naive competitor baselines.
- Single-run measurements ignoring OS jitter, frequency scaling, and thermal throttling.
- Cherry-picked micro-benchmarks that do not reflect production realities.
- Unreleased benchmark source code preventing third-party verification.

As performance is a non-negotiable pillar for WON, an empirical, transparent, and unyielding benchmark methodology is required.

## Considered Alternatives
1. **Informal Ad-Hoc Benchmarks**:
   - *Rejected*: Destroys engineering credibility; cannot be reproduced.
2. **Closed Proprietary Benchmark Suites**:
   - *Rejected*: Incompatible with open-source transparency.
3. **Standardized Open Empirical Protocol (Selected)**:
   - Mandatory comparison against optimized production C (GCC/Clang -O3 -flto) and equivalent systems languages.
   - Enforced correctness checks before recording timing metrics.
   - Minimum 30 iterations with median, IQR, and p99 statistics.
   - Comprehensive hardware and environmental disclosures.
   - Public open-source benchmark repository with raw JSON logs.

## Decision
**WON officially accepts the Standardized Open Empirical Benchmark Protocol as defined in `docs/benchmarking.md`.**

All published performance numbers must originate from this protocol. No member of the team or AI agent may publish unverified performance claims.

## Consequences
- **Positive**: Guarantees public credibility and prevents self-deception; ensures compiler optimizations deliver authentic real-world speedups.
- **Negative**: Increases the engineering overhead of writing and validating benchmark suites.
- **Impact on Pillars**:
  - *Performance*: Sets an empirical standard ensuring WON matches or exceeds C where intended.

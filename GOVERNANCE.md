# WON Project Governance

This document outlines the governance model, decision-making processes, and project leadership structure for the WON programming language project under **KitiLab**.

---

## 1. Project Stewardship

WON is stewarded by **KitiLab** as an open-source systems programming initiative. The core team at KitiLab guides the language design, engineering roadmap, security requirements, and official releases.

## 2. Decision Making Process

Technical and architectural decisions follow an evidence-driven, transparent review model:

- **Architectural Decision Records (ADRs)**: Any substantial change to language syntax, compiler intermediate representations, memory model rules, or public APIs must be proposed via an ADR in `docs/decisions/`.
- **Consensus & Review**: Changes require review and sign-off by KitiLab maintainers before adoption.
- **Empirical Validation**: Claims regarding runtime performance, compilation speed, or safety invariants must be backed by reproducible benchmark harnesses and test suites.

## 3. Contributor Roles

- **Maintainers**: Responsible for architectural direction, branch protection, releases, and approving pull requests.
- **Contributors**: Propose improvements, file issues, submit documentation updates, implement features, and report bugs.
- **Automated AI Agents**: Operate under the guidelines in `AGENTS.md`, submitting scoped pull requests subject to maintainer validation and automated testing.

## 4. RFC Lifecycle for Language Evolution

1. **Idea & Discussion**: Open an issue labeled `rfc-discussion` describing the problem and rationale.
2. **Draft ADR**: Submit a PR adding a draft ADR with status `PROPOSED`.
3. **Evaluation**: Benchmark prototype implementations, verify ergonomic impact, and test agent inspectability.
4. **Resolution**: Maintainers transition the ADR to `ACCEPTED` or `REJECTED`.

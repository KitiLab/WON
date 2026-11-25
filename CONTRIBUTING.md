# Contributing to WON

Thank you for your interest in contributing to the WON programming language. We welcome contributions from engineers, researchers, and AI agents adhering to our engineering standards.

---

## Getting Started

1. **Review Architecture**: Before submitting code, read the core design documents in `docs/`:
   - `docs/vision.md`
   - `docs/roadmap.md`
   - `docs/compiler-architecture.md`
   - `docs/memory-model.md`
   - `docs/performance-principles.md`
2. **Review Agent Guidelines**: All automated contributions and human-AI pairings must comply with `AGENTS.md`.
3. **Check Issue Tracker**: Look for open issues or RFCs before beginning major work.

## Development Workflow

1. Fork or branch from `main`.
2. Ensure changes are focused and atomic. Avoid combining unrelated refactorings with feature work.
3. Every new feature or bug fix must include corresponding tests.
4. Write clear commit messages summarizing technical intent.
5. Submit a Pull Request targeting `main`.

## Coding Standards

- **Identifiers**: Standard English for all symbols, types, functions, and file names.
- **Source Code Comments**: Simplified Chinese (`简体中文`) for internal docstrings, type documentation, and algorithm explanations.
- **Dependencies**: Keep external dependencies minimal. Prioritize standard libraries and zero-dependency designs.
- **Formatting**: Adhere to `.editorconfig` settings (UTF-8, LF line endings, 4-space indent for code, 2-space for configuration).

## Licensing of Contributions

All contributions submitted to the WON repository are provided under the terms of the [Apache License, Version 2.0](LICENSE) (`Apache-2.0`), in accordance with Section 5 (Submission of Contributions) of that license. Unless explicitly stated otherwise in a separate written notice, external third-party code under incompatible or undisclosed licenses must not be copied directly into this repository.

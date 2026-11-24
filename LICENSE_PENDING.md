# Software License Pending Decision

The open-source licensing for the **WON** programming language and its official compiler toolchain is currently under active deliberation by the project maintainers at **KitiLab**.

---

## Current Status

- **Status**: `UNDER INVESTIGATION`
- **Owner**: KitiLab
- **Reference**: Architectural Decision Record tracking licensing evaluation will be documented under `docs/decisions/`.

## Evaluated License Models

The team is evaluating several standard open-source licenses to determine the best alignment with WON's strategic objectives:

1. **Apache License 2.0**:
   - *Strengths*: Explicit patent grant, strong enterprise adoption, clear contributor license terms.
   - *Considerations*: Requirements on NOTICE files and state changes.

2. **MIT License**:
   - *Strengths*: Maximum permissiveness, universal compatibility, minimal friction for adoption and embedding.
   - *Considerations*: No explicit patent grant.

3. **Dual License (Apache-2.0 OR MIT)**:
   - *Strengths*: Standard practice among modern systems programming languages (e.g., Rust ecosystem), providing maximum flexibility for users.

4. **Mozilla Public License 2.0 (MPL-2.0)**:
   - *Strengths*: File-level copyleft ensures improvements to the core compiler and runtime remain open, while allowing proprietary integration and proprietary applications built with WON.

## Policy During Incubation (Phase 0)

Until an official license file (`LICENSE`) is formally adopted and committed by KitiLab:
- All source files, specifications, and documentation are provided for evaluation, development, and community review within the official repository.
- No commercial re-distribution under proprietary terms is authorized until the permanent open-source license is enacted.

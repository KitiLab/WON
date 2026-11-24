# Security Policy for WON

The WON team takes language runtime safety, compiler integrity, and supply chain security seriously.

---

## Supported Versions

| Version | Status | Security Support |
| :--- | :--- | :--- |
| `0.x` (Phase 0 Proof of Concept) | Experimental / Pre-alpha | Best-effort review; non-production |
| `1.0+` (Future) | Planned Public Release | Active LTS security support |

## Reporting a Vulnerability

If you identify a security issue, memory corruption vulnerability, or compiler code-execution flaw:

1. **Do not disclose publicly** in an open GitHub issue or public forum.
2. Email the maintainers directly at `security@kitilab.com` with:
   - Description of the vulnerability.
   - Minimal reproduction code (`.won` source or compiler command line).
   - Expected vs actual behavior.
   - Affected platforms or architectures.
3. You will receive an initial response within 48 hours.
4. Once verified, a fix will be prepared in private and published with an appropriate advisory.

# AI Agent Structured Diagnostic & Automated Self-Repair Test

This directory contains test fixtures and verification scripts demonstrating WON's machine-readable diagnostic interface and automated self-repair loop (Claim E).

## Contents
- `broken.won`: Deliberately broken WON source file triggering `WON-MUT-001` (immutable binding mutably borrowed).
- `repair_test.go`: Automated test verifying JSON schema conformance, edit application, and post-repair compilation.

## Verification
Run:
```bash
go test -v ./tests/ai_repair/...
```

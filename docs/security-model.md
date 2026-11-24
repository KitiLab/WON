# WON Security & Sandboxing Model

> **Document Status**: `SPECIFIED`  
> **Author**: KitiLab Engineering  
> **Last Updated**: Phase 0A

---

## 1. Security Architecture Overview

The security model of WON addresses threats at three levels:
1. **Memory Corruption Vulnerabilities**: Mitigated by the compile-time ownership-lite safety system.
2. **Supply Chain & Foreign Code Risks**: Mitigated by explicit capability summaries and foreign C library audits.
3. **Runtime Privilege Exploits**: Mitigated by planned OS-level sandboxing.

---

## 2. Compile-Time Capability Summaries

Modern software architectures suffer from opaque dependencies: importing a minor utility package may secretly open network sockets or read local credentials.

WON addresses this through **Compile-Time Capability Tracking**:
- **Capability Tags**: The compiler analyzes the call graph to deduce the external capabilities invoked by each module:
  - `cap::fs_read`, `cap::fs_write`
  - `cap::net_connect`, `cap::net_listen`
  - `cap::env_read`
  - `cap::exec`
  - `cap::unsafe_ffi`
- **Capability Manifest**: A module's public contract can declare permitted capabilities. If a dependency attempts an undeclared action (e.g. a string parser attempting network I/O), compilation fails.

---

## 3. Unsafe Isolation Boundaries

The `unsafe` keyword in WON is an explicit trust boundary:
- Safe code is statically proven free of data races, use-after-free, double-free, and buffer overflows.
- All raw pointer arithmetic, arbitrary type casting (`transmute`), and foreign C calls must reside inside `unsafe { ... }` blocks.
- **Audit Reports**: The compiler command `won audit` lists all `unsafe` blocks, foreign C symbols, and raw pointer dereferences across the entire dependency graph in JSON format.

---

## 4. Foreign C Library Defense

Because WON enables direct C library inclusion (`#import <header.h>`), external C code presents the largest surface for memory vulnerabilities.

Security principles for C interoperability:
1. **Header Sandboxing**: Header parsing is performed in a restricted parser pass that rejects arbitrary preprocessor code execution.
2. **Safe Wrapper Enforcement**: Direct C calls remain `unsafe`. Standard library bindings encapsulate foreign handles inside safe types that enforce correct initialization and deallocation.
3. **Hardened Compiler Flags**: Generated C code is compiled with modern defensive flags:
   - Stack smashing protection (`-fstack-protector-strong`)
   - Control-flow integrity (`-fcf-protection`)
   - Fortified source functions (`-D_FORTIFY_SOURCE=3`)
   - Position-Independent Executables (`-fPIE -pie`)

---

## 5. OS Sandboxing Integration (Planned)

In Phase 4, the WON runtime will provide optional declarative sandboxing primitives utilizing kernel-level isolation:
- **Linux**: Integration with Linux Landlock LSM and seccomp-bpf to restrict filesystem and syscall access.
- **OpenBSD**: Native support for `pledge(2)` and `unveil(2)`.
- **macOS / Windows**: Support for sandbox profiles and AppContainers.

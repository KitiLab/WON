# WON Language Specification: Safety and Security Semantics

> **Specification Version**: Draft 0.1  
> **Status**: `SPECIFIED` (Under Architectural Review)  
> **Module**: Safety Boundaries, Capabilities & OS Sandboxing  
> **Date**: November 2026

---

## 1. The Tripartite Security Architecture

Security in WON is designed as a defense-in-depth model spanning three distinct conceptual layers:

```
                      Layer 1: Safe WON Code
          (Compile-Time Memory Safety, Data-Race Freedom)
                                │
                                ▼
                     Layer 2: Unsafe WON Code
        (Explicit Raw Pointer Manipulation, C FFI Boundaries)
                                │
                                ▼
                   Layer 3: OS Process Sandbox
          (Kernel-Enforced System Call & Filesystem Isolation)
```

1. **Layer 1: Safe WON Code**: The default, verified language subset. Provably immune to spatial and temporal memory safety bugs (buffer overflows, use-after-free, double-free) and multithreaded data races.
2. **Layer 2: Unsafe WON Code**: An explicit escape hatch for low-level systems programming, operating system interfaces, and C library interop. The developer explicitly certifies memory safety invariants.
3. **Layer 3: OS Process Sandboxing**: Kernel-level runtime containment (Linux Landlock/seccomp, macOS Seatbelt). Provides boundary enforcement against malicious or buggy native C libraries that bypass compile-time guarantees.

---

## 2. Compile-Time Invariants in Safe WON

Safe code guarantees the following invariants at compile time:
- **Spatial Memory Safety**: All array and slice accesses are bounds-checked. Pointer arithmetic is forbidden.
- **Temporal Memory Safety**: Dangling references are mathematically impossible through scope-bounded region analysis. Use-after-free is prevented by move semantics.
- **Type Safety**: Unchecked type casts (type punning) are prohibited. Downcasting and union access require exhaustive pattern matching.
- **Data-Race Freedom**: Simultaneous mutable access and uncoordinated shared access across threads are rejected by the `Send` and `Sync` type system.
- **Deterministic Initialization**: Reading uninitialized variables or memory is rejected at compile time.

---

## 3. The `unsafe:` Escape Boundary

The `unsafe:` block establishes an auditable, isolated region where the compiler relaxes specific static checks:

```won
unsafe:
    # Relaxed operations permitted here
```

### 3.1 Operations Permitted Only Inside `unsafe:`
1. Dereferencing raw pointers (`*ptr`).
2. Performing raw pointer arithmetic (`ptr.offset(count)`).
3. Invoking foreign C library functions imported via `#import`.
4. Calling functions explicitly declared with `unsafe fn`.
5. Accessing mutable static global variables.
6. Accessing fields of untagged C unions.

### 3.2 What `unsafe:` CANNOT Bypass
- `unsafe:` does **NOT** disable the type system or relax syntax rules.
- `unsafe:` does **NOT** permit accessing variables outside their lexical scopes.
- `unsafe:` does **NOT** alter the binary ABI or calling conventions of the compiler.

---

## 4. Kernel-Enforced Process Sandboxing

> [!IMPORTANT]
> **No compiler type system can constrain arbitrary third-party native C libraries.** If an imported C library contains an internal buffer overflow or malicious payload, it can subvert the host process.

To address this fundamental reality, WON integrates kernel-level process sandboxing into its toolchain:

### 4.1 Linux Sandboxing: Landlock & Seccomp-BPF
- **Filesystem Confinement (Landlock LSM)**:
  Programs or agent tasks can restrict their filesystem access to explicit directories:
  ```won
  import std::security::sandbox

  fn secure_execution():
      sandbox::restrict_filesystem(
          read_only: ["/usr/lib", "/etc/ssl"],
          read_write: ["/tmp/app_scratch"]
      )
  ```
- **System Call Filtering (seccomp-bpf)**:
  Disables dangerous system calls (`fork`, `execve`, `ptrace`, raw socket creation) after application initialization, blocking shell escapes and privilege escalations.

### 4.2 macOS Sandboxing: Seatbelt
- Configures mandatory access control profiles via `sandbox_init()` to restrict network, IPC, and filesystem access.

### 4.3 Windows Sandboxing (Under Investigation)
- Evaluates restricted security tokens, Job Objects, and Windows AppContainer boundaries for native process isolation.

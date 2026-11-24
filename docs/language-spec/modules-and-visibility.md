# WON Language Specification: Modules and Visibility

> **Specification Version**: Draft 0.1  
> **Status**: `SPECIFIED` (Under Architectural Review)  
> **Module**: Code Organization, Scopes & Packaging  
> **Date**: November 2026

---

## 1. Modular Hierarchy

WON code is structured into an intuitive, scalable hierarchy designed for both single-file scripts and enterprise-scale systems:

```
                            Package (won.toml)
                                    │
                        Root Module (main.won / lib.won)
                                    │
                    ┌───────────────┴───────────────┐
                    ▼                               ▼
             Submodule (file)              Submodule (directory)
               parser.won                       codegen/
                                              ├── mod.won
                                              ├── c11.won
                                              └── llvm.won
```

1. **Compilation Unit**: A single file or a directory tree containing a `won.toml` manifest.
2. **Module**: A distinct namespace containing declarations (functions, structs, traits, enums, submodules).
3. **Package**: A distributable unit of compilation consisting of a library (`lib.won`) and/or one or more executables (`main.won` or `cmd/<name>/main.won`).

---

## 2. Source File to Module Mapping

WON avoids redundant boilerplate by deriving module hierarchies directly from the filesystem structure:
- **File Modules**: A file named `parser.won` located in the same directory as `main.won` defines the module `parser`.
- **Directory Modules**: A directory named `codegen/` containing `mod.won` defines the module `codegen`. Sibling files in that directory (e.g., `codegen/c11.won`) become nested submodules (`codegen::c11`).
- **Inline Modules**: Modules can also be declared inline within any source file using the `mod` keyword:
  ```won
  mod internal_crypto:
      pub fn hash(data: &[u8]) -> [u8; 32]:
          ...
  ```

---

## 3. Visibility and Access Control

In WON, **all declarations are private to their containing module by default.** Encapsulation is preserved unless explicitly relaxed using visibility modifiers:

| Visibility Modifier | Accessible Scope | Notes |
| :--- | :--- | :--- |
| *(None / Default)* | Current module only | Private to the lexical file or inline module |
| `pub` | Entire world / public API | Exported to downstream consumers and importers |
| `pub(crate)` | Current package only | Accessible anywhere within the package, but hidden from external consumers |
| `pub(super)` | Parent module only | Accessible to the immediate containing parent module |

```won
pub struct Token:
    pub kind: TokenKind       # Publicly readable field
    span: SourceSpan          # Private to module (internal detail)
```

---

## 4. Import Semantics and Name Resolution

Declarations from other modules or external packages are brought into scope using `import` or `from`:

### 4.1 Direct Path Import
```won
import std::fs
import compiler::lexer

fn run():
    let file = fs::read_to_string("main.won")
```

### 4.2 Selective Symbol Import
```won
from std::collections import HashMap, Vec
from compiler::ast import Expr, Statement

fn parse():
    let map = HashMap::new()
```

### 4.3 Renaming and Aliasing
```won
import std::sync::Mutex as SyncLock
from std::io import eprintln as err_print
```

### 4.4 Wildcard Imports
```won
from compiler::ast import *
```
*Note: Wildcard imports (`*`) are permitted within test modules, but are discouraged in production libraries to maintain AI agent readability and unambiguous symbol resolution.*

---

## 5. Circular Dependency Resolution

At the package boundary, cyclic dependencies between independent packages are strictly forbidden and rejected at build time.

Within a single package, **mutually recursive types and functions across modules are permitted and resolved soundly**:
- The compiler employs a multi-pass symbol resolution pipeline:
  1. **Pass 1 (Symbol Registration)**: Scans all files in the package and registers nominal type names, module paths, and function signatures.
  2. **Pass 2 (Type Checking & Sizing)**: Resolves struct field types, generic constraints, and method signatures. Mutually recursive structs must be broken by an indirection layer (`Shared[T]`, `Arc[T]`, or `Box[T]`) to ensure finite struct sizing.
  3. **Pass 3 (Body Checking)**: Type-checks function bodies and expressions.

---

## 6. Package Manifest Requirements (`won.toml`)

Packages are configured using a declarative TOML manifest:

```toml
[package]
name = "won-crypto"
version = "0.1.0"
authors = ["KitiLab Engineering <dev@kitilab.org>"]
edition = "2026"
license = "MIT"

[dependencies]
zlib = { system = "zlib.h", link = "z" }
sqlite = { system = "sqlite3.h", link = "sqlite3", optional = true }

[binaries]
won-hash = "cmd/won-hash/main.won"

[lib]
path = "src/lib.won"
```

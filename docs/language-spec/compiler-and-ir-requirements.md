# WON Language Specification: Compiler Architecture and Intermediate Representation

> **Specification Version**: Draft 0.1  
> **Status**: `SPECIFIED` (Phase 2C.0 Backend-Neutral TIR & Verifier, Phase 2C.1 TIR-Driven C11 Backend, and Phase 2C.2.0–2C.2.2 Native Aggregate Layout & Owned String Runtime `IMPLEMENTED` in [phase-2c.0-tir-foundation.md](../compiler/phase-2c.0-tir-foundation.md), [phase-2c.1-tir-c11-backend.md](../compiler/phase-2c.1-tir-c11-backend.md), & [phase-2c.2-native-layout-and-resource-representation.md](../compiler/phase-2c.2-native-layout-and-resource-representation.md); [ADR-012](../decisions/ADR-012-native-string-aggregate-layout-and-allocator.md) `ACCEPTED`)  
> **Module**: Compiler Passes & Typed Intermediate Representation (TIR)  
> **Date**: November 2026

---

## 1. High-Level Multi-Pass Architecture

The permanent WON compiler is engineered as a modular, staged pipeline with clean architectural seams between lexical analysis, AST construction, semantic validation, intermediate optimization, and code generation:

```
                            WON Source Code (.won)
                                      │
                                      ▼
                           1. Lexical Analysis
                        (Off-side INDENT/DEDENT)
                                      │  Token Stream
                                      ▼
                            2. Syntax Parsing
                                      │  Untyped AST (with Source Spans)
                                      ▼
                           3. Name Resolution
                                      │  Symbol Tables & Scopes
                                      ▼
                    4. Type Checking & Local Inference
                                      │  Typed AST
                                      ▼
                    5. Ownership & Borrow Analysis
                        (Move checking, Aliasing)
                                      │  Verified AST
                                      ▼
                           6. TIR Construction
                                      │  Typed Intermediate Representation
                                      ▼
                         7. Mid-Level Optimization
                        (Inlining, Monomorphization)
                                      │  Optimized TIR
                                      ▼
                         8. Code Generation Backend
                        (C11, Future LLVM, Future QBE)
                                      │  Target Representation
                                      ▼
                         9. Assembler & Linker Driver
                                      │
                                      ▼
                           Native Machine Binary
```

---

## 2. Compiler Pipeline Stages

1. **Lexical Analysis (`lexer`)**:
   - Consumes UTF-8 bytes and emits a flat token stream.
   - Maintains an indentation stack to emit synthetic `INDENT`, `DEDENT`, and `NEWLINE` tokens according to the off-side rule.
   - Strips comments while attaching doc comments (`##` / `///`) to following tokens.
2. **Parsing (`parser`)**:
   - Recursive-descent parser with Pratt parsing for binary and unary expressions.
   - Constructs a concrete Abstract Syntax Tree (AST) where every node preserves its exact source span `(start_byte, end_byte, line, col)`.
3. **Name Resolution (`resolver`)**:
   - Resolves lexical scopes, imports, module paths, and symbol declarations across packages.
   - Resolves mutually recursive definitions across files in the same package.
4. **Type Checking & Inference (`typechecker`)**:
   - Performs bidirectional local type inference within function bodies.
   - Enforces trait bounds and monomorphizes generic functions and data types.
5. **Ownership and Borrow Checking (`borrowchecker`)**:
   - Enforces single-ownership move semantics and invalidates moved variables.
   - Verifies the core borrowing invariant (multiple readers XOR single writer) via scope-bounded region analysis.
6. **TIR Construction (`tir_builder`)**:
   - Lowers the verified typed AST into a high-level, language-independent Typed Intermediate Representation (TIR).
7. **Optimization Passes (`optimizer`)**:
   - Monomorphization instantiation and deduplication.
   - Constant folding, dead code elimination, and loop-invariant code motion.
   - Redundant bounds check elimination.
8. **Backend Code Generation (`backend`)**:
   - **C11 Backend**: Translates TIR into strictly compliant, portable ISO C11.
   - **Future LLVM Backend**: Translates TIR into LLVM IR for direct machine code generation.
   - **Future QBE Backend**: Translates TIR into QBE IL for ultra-fast compilation cycles.
9. **Assembler & Linker Driver (`driver`)**:
   - Invokes system compilers (GCC, Clang, or LLD) to produce native standalone executables.

---

## 3. Typed Intermediate Representation (TIR) Specification

The Typed Intermediate Representation (TIR) is the central semantic abstraction of the WON compiler. **TIR is completely decoupled from Go data structures, Go runtime behavior, and C-specific syntax.**

### 3.1 Structural Characteristics of TIR
- **Control Flow Graph (CFG)**: Functions are represented as directed graphs of Basic Blocks. Each basic block contains a linear sequence of instructions ending in a single terminal instruction (`Branch`, `BranchIf`, `Return`, `Panic`).
- **Explicit Types**: Every value, temporary register, and instruction operand in TIR possesses a known, concrete, monomorphized type.
- **Explicit Memory Semantics**: Memory allocations, loads, stores, and address calculations are explicit instructions.
- **Explicit Destruction**: Ownership drops (`Drop`) are represented as explicit instructions at basic block exits, ensuring deterministic RAII ordering.

### 3.2 Canonical TIR Instruction Set

```
TIR Program
 ├── Types: Map[TypeID, ConcreteType]
 ├── Globals: List[GlobalVar]
 └── Functions: List[TIRFunction]
      ├── Signature: (params: [TIRType], return: TIRType)
      ├── Locals: Map[LocalID, TIRType]
      └── Blocks: List[BasicBlock]
           ├── ID: BlockID
           ├── Instructions: List[TIRInstruction]
           └── Terminator: TIRTerminator
```

#### Core TIR Instructions:
- **Memory Operations**:
  - `StackAlloc(dest: LocalID, type: TIRType)`: Reserves stack space for a local variable.
  - `Load(dest: RegID, ptr: RegID)`: Dereferences a pointer and loads a value.
  - `Store(ptr: RegID, val: RegID)`: Writes a value into a memory location.
  - `ComputeAddress(dest: RegID, base: RegID, path: [PathElement])`: Computes the effective memory address of an aggregate field (`Field(ident)`) or indexed element (`Index(Operand)`) without performing a memory dereference. Unlike backend-leaky constructs (such as LLVM's `getelementptr`), `ComputeAddress` operates strictly on high-level, strongly typed TIR data structures and is backend-neutral across C11, LLVM, and QBE generators.
- **Value & Compute Operations**:
  - `Unary(dest: RegID, op: UnaryOp, src: Operand)`
  - `Binary(dest: RegID, op: BinaryOp, left: Operand, right: Operand)`
  - `Cast(dest: RegID, kind: CastKind, src: Operand, target_type: TIRType)`
- **Ownership & Resource Operations**:
  - `Move(dest: RegID, src: RegID)`: Transfers ownership and marks `src` dead.
  - `Drop(target: RegID)`: Emits destructor invocation if type implements `Drop`.
- **Function & Foreign Calls**:
  - `Call(dest: RegID, callee: FunctionID, args: [Operand])`: Standard internal function call.
  - `ForeignCall(dest: RegID, symbol: str, args: [Operand], abi: CAbi)`: Direct C ABI invocation.
- **Terminators**:
  - `Branch(target: BlockID)`: Unconditional jump.
  - `BranchIf(cond: Operand, then_block: BlockID, else_block: BlockID)`: Conditional jump.
  - `Return(val: Optional[Operand])`: Return from function.
  - `Trap(reason: str)`: Unrecoverable panic/abort.

---

## 4. Multi-Backend Interface Design

To guarantee long-term compiler flexibility, the backend architecture is defined via an abstract code emission trait:

```won
trait BackendEmitter:
    fn emit_function(&mut self, func: &TIRFunction) -> Result[unit, CodegenError]
    fn emit_global(&mut self, global: &GlobalVar) -> Result[unit, CodegenError]
    fn finalize(&mut self, output_path: &str) -> Result[unit, CodegenError]
```

- **Phase 0 & Phase 1**: The primary emitter is `C11Emitter`, which maps TIR basic blocks to standard C labels, gotos, and control structures.
- **Current PoC Reconciliation (Phase 2C.1–2C.2.0)**: `compiler/poc/internal/tir` and `internal/tir/lower` implement the backend-neutral TIR data model, verified `TypedProgram`/`DropElaboratedProgram` lowering, `DropFlag`/`CondDrop` lifecycle preservation, FFI contracts, multi-way `TermSwitch`/`TermBranchIf`, deterministic `Dump()`, and structural `tir.Verify()` (including non-unit `TermReturn` completeness verification). `compiler/poc/internal/codegen/c11` implements the active TIR-driven ISO C11 emitter and `WON-GEN-002` capability validator consumed by `wonc build`, executing native `GlueForeignResource` Drop/DropFlag cleanup while keeping `String` and user ADT layout lowering explicitly gated (`WON-GEN-002`). Target-independent ZST/downcast TIR extensions and Linux x86_64 LP64 `String`/aggregate C11 layout rules are specified in [phase-2c.2-native-layout-and-resource-representation.md](../compiler/phase-2c.2-native-layout-and-resource-representation.md) (`SPECIFIED`; [ADR-012](../decisions/ADR-012-native-string-aggregate-layout-and-allocator.md) `ACCEPTED` Nov 28, 2026), keeping byte offsets and C spellings strictly outside generic TIR. Legacy AST `internal/codegen` remains solely as a reference baseline for differential testing.
- **Future Phase 5**: An `LlvmEmitter` will lower TIR instructions directly into LLVM IR modules, unlocking native cross-compilation and advanced target-specific optimizations without external toolchain friction.

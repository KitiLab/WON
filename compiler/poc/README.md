# WON Phase 0B Bootstrap Compiler (`wonc`)

This directory houses the initial **Phase 0B Proof of Concept** compiler prototype for the WON programming language.

> **Status**: `IMPLEMENTED` (Phase 0B Vertical Slice)  
> **Notice**: This is a minimal bootstrap compiler built to empirically validate language parsing, C header binding generation, C code generation, and AI-native structured diagnostics. It is not the production compiler.

---

## Architecture & Directory Layout

```
compiler/poc/
├── cmd/
│   └── wonc/                  # CLI 驱动入口 (main.go)
├── internal/
│   ├── ast/                   # 抽象语法树节点定义
│   ├── token/                 # 词法记号定义
│   ├── lexer/                 # 源码位置精确保留的词法分析器
│   ├── parser/                # 递归下降语法解析器
│   ├── checker/               # 极简语义分析（可变性检查与 unsafe 隔离）
│   ├── bindgen/               # 基于 Clang 语义 AST 的 C 头文件绑定提取器
│   ├── codegen/               # 标准 ISO C99/C11 源码生成器
│   ├── diagnostic/            # 人机通用的标准 JSON 结构化诊断系统
│   └── toolchain/             # 底层 GCC / Clang 原生编译器调用封装
├── tests/                     # 端到端集成与 AI 自动化修复测试
├── go.mod                     # Go 模块配置 (标准库零三方依赖)
└── README.md
```

---

## Building the Compiler

Requirements: Go 1.22+ and GCC or Clang.

```bash
# 进入目录并构建 wonc 命令行工具
cd compiler/poc
go build -o bin/wonc ./cmd/wonc

# 查看版本与目标架构信息
./bin/wonc version
```

---

## Running the Automated Test Suite

```bash
cd compiler/poc
go test -v ./...
```
Tests cover:
- Lexer tokenization, string escape sequences, and illegal character diagnostics.
- Parser AST construction, control flow, loops, and error reporting.
- Semantic checker mutability enforcement (`WON-MUT-001`) and unsafe isolation (`WON-SEC-001`).
- Clang AST header inspection on `/usr/include/zlib.h`.
- C code generation and native executable linking.
- Machine-readable JSON diagnostic parsing and automated AI repair validation.

---

## CLI Usage & Operational Commands

### 1. Syntax & Semantic Checking
```bash
# 终端纯文本格式检查
./bin/wonc check examples/zlib_compression.won

# 机器可读 JSON 诊断格式 (供 AI Agent / IDE 使用)
./bin/wonc check --format=json examples/zlib_compression.won
```

### 2. Extract C Header Bindings
```bash
# 从系统 /usr/include/zlib.h 自动分析生成绑定元数据
./bin/wonc bindings zlib
```

### 3. Compile to Native Executable
```bash
# 编译生成原生机器码可执行文件
./bin/wonc build -o zlib_demo examples/zlib_compression.won

# 运行验证
./zlib_demo
```

### 4. Static Linking Evaluation
```bash
# 尝试静态链接 (-static)
./bin/wonc build --static examples/zlib_compression.won
```
*注：在当前 Fedora Linux 45 主机环境中，由于系统缺少 `glibc-static` (`libc.a`) 和 `zlib-static` (`libz.a`) 归档，编译器将明确提示环境依赖缺失。*

---

## Supported Phase 0B Language Subset

- Functions: `fn name(params) -> RetType { ... }`
- Variables: `let` (只读) 与 `let mut` (可变)
- Basic Types: `i32`, `u64`, `u8`, `&str`, 定长数组 `[u8; N]`
- Control Flow: `if / else`, `while (cond) { ... }`, `return`
- Borrowing: `&var`, `&mut var`
- Unsafe Blocks: `unsafe { ... }` 隔离底层 C FFI 调用
- Foreign Calls: `zlib::compress(...)`, `zlib::uncompress(...)`, `zlib::compressBound(...)`
- Builtins: `.as_bytes()`, `.as_ptr()`, `.as_mut_ptr()`, `.len()`, `println(...)`

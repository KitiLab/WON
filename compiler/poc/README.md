# WON Phase 0B Bootstrap Compiler (`wonc`)

This directory houses the **Phase 0B & Phase 0B.1 Proof of Concept** compiler prototype for the WON programming language.

> **Status**: `IMPLEMENTED` (Phase 0B & Phase 0B.1 Acceptance Remediation Verified)  
> **Notice**: This is a minimal bootstrap compiler built to empirically validate language parsing, C header binding generation, strict ISO C11 code generation, zero-overhead C FFI execution, and AI-native structured diagnostics. It is not the production compiler.

---

## Architecture & Directory Layout

```
compiler/poc/
├── cmd/
│   └── wonc/                  # CLI 驱动入口 (main.go - check, bindings, build)
├── internal/
│   ├── ast/                   # 抽象语法树节点模型
│   ├── token/                 # 词法记号定义
│   ├── lexer/                 # 源码位置精确保留的词法分析器
│   ├── parser/                # 递归下降语法解析器
│   ├── checker/               # 语义分析（变量作用域、可变性借用、C FFI 签名校验与 unsafe 边界）
│   ├── bindgen/               # 基于 Clang 语义 AST 的真实 C 头文件绑定提取与 SHA-256 指纹缓存器
│   ├── codegen/               # 严格标准 ISO C11 源码生成器 (杜绝非标 GNU 扩展)
│   ├── diagnostic/            # 人机通用的标准 JSON 结构化诊断系统
│   └── toolchain/             # 底层 GCC / Clang 原生编译器与链接器调用封装
├── tests/                     # 端到端集成测试、边界正确性测试、绑定指纹测试与 JSON 诊断协议测试
│   ├── binding_test.go        # 头文件 SHA-256 指纹防伪与自动失效重提测试
│   ├── correctness_test.go    # 空输入、损坏数据安全拒绝与边界防护测试
│   ├── integration_test.go    # 端到端构建与逐字节一致性测试
│   └── protocol_test.go       # JSON 诊断格式与自动化代码修复切片协议测试
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
- Semantic checker mutability enforcement (`WON-MUT-001`), unsafe isolation (`WON-SEC-001`), and FFI signature matching (`WON-FFI-001/002/003`).
- Clang AST header inspection and SHA-256 fingerprint validation on `/usr/include/zlib.h`.
- Strict ISO C11 code generation validated with `-pedantic-errors -Wall -Wextra` against both GCC 16 and Clang 23.
- End-to-end zlib compression and byte-for-byte correctness verification.
- Machine-readable JSON diagnostic serialization and automated AI repair protocol.

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

# 启用链接时优化 (--lto) 编译
./bin/wonc build --lto -o zlib_demo_lto examples/zlib_compression.won

# 运行验证
./zlib_demo
```

### 4. Static Linking Evaluation
```bash
# 尝试静态链接 (-static)
./bin/wonc build --static examples/zlib_compression.won
```
*注：若系统环境缺少 `glibc-static` (`libc.a`) 或 `zlib-static` (`libz.a`)，编译器将输出精确的依赖缺失诊断。*

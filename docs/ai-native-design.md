# WON AI-Native Compiler Architecture

> **Document Status**: `SPECIFIED`  
> **Author**: KitiLab Engineering  
> **Last Updated**: Phase 0A

---

## 1. Vision: Built for AI Coding Agents

Traditional compilers were built exclusively for human eyes reading terminal emulators. They output ANSI-colored text, truncated stack traces, and unstructured warning messages that require complex regex parsing by external tools.

WON is designed from the ground up as an **AI-Native Language Toolchain**:

> The compiler is not just an executable builder; it is an interactive semantic query engine exposing structured, machine-readable interfaces for automated coding agents.

Importantly, this capability is **completely vendor-agnostic**. The compiler does not call external LLM APIs; rather, it exposes structured protocols that any autonomous agent, IDE, or human can consume.

---

## 2. Core AI-Native Subsystems

### 2.1 Machine-Readable JSON Diagnostics
Every compilation error, warning, and lint emitted by the compiler can be output as structured JSON via the `--format=json` flag:

```json
{
  "version": "1.0",
  "status": "error",
  "errors": [
    {
      "code": "WON-E0402",
      "severity": "error",
      "message": "Use of moved value 'buffer'",
      "span": {
        "file": "src/main.won",
        "start": { "line": 42, "column": 9, "byte": 1204 },
        "end": { "line": 42, "column": 15, "byte": 1210 }
      },
      "context": {
        "moved_at": {
          "file": "src/main.won",
          "start": { "line": 38, "column": 18, "byte": 1092 },
          "end": { "line": 38, "column": 24, "byte": 1098 }
        },
        "reason": "Value moved into function 'process_data(buffer)'"
      },
      "suggestions": [
        {
          "description": "Pass by immutable borrow instead of moving ownership",
          "replacement_span": {
            "file": "src/main.won",
            "start": { "line": 38, "column": 18, "byte": 1092 },
            "end": { "line": 38, "column": 24, "byte": 1098 }
          },
          "new_text": "&buffer"
        }
      ]
    }
  ]
}
```

### 2.2 Interactive Semantic Query Interface (`won inspect`)
Agents can query the compiler's internal symbol table, type definitions, and module contracts without generating native executables:

- `won inspect --type <expression>`: Returns the resolved type, trait implementations, and memory layout (size, alignment, field offsets).
- `won inspect --symbols <file>`: Returns all public functions, structs, traits, and documentation strings in JSON format.
- `won inspect --call-graph`: Outputs the static function invocation graph.

### 2.3 Automated Fix & Explanation Workflows
The CLI driver provides targeted developer and agent assistants:
- `won explain <ERROR_CODE>`: Emits formal documentation, root cause explanations, and working examples of how to resolve the error.
- `won fix --dry-run`: Applies compiler-generated suggested fixes directly as git patches or JSON diffs.

### 2.4 Version-Specific Agent Rules Generation (`won agent-rules`)
To eliminate hallucinated syntax in AI agents, the compiler can generate an authoritative, version-locked `AGENTS.md` file tailored to the exact installed compiler version:
- Lists all active keywords, types, and compiler flags.
- Distinguishes implemented features from planned features.
- Provides tested code patterns directly extracted from the compiler test suite.

---

## 3. Provider Independence & Open Protocol

WON strictly maintains independence from all AI cloud providers (OpenAI, Anthropic, Google, etc.):
- No proprietary API keys are embedded or required.
- All agent interfaces operate via standard input/output streams (`stdin`/`stdout`), Unix domain sockets, or standard JSON-RPC.
- The protocol conforms to open standards, ensuring compatibility with any local or cloud LLM.

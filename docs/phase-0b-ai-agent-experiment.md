# Phase 0B.1 — AI Coding-Agent Autonomous Repair Experiment

> **Status**: Verified & Documented  
> **Date**: 2026-11-24  
> **Subject**: Autonomous closed-loop diagnosis and surgical patch application via structured compiler diagnostics.

---

## 1. Executive Summary

A core architectural tenet of the WON programming language is its AI-native compiler design:
> *"Structured JSON diagnostics, deterministic error codes, and machine-actionable byte-level fixes designed for AI agent consumption."*

This document records the empirical results of a genuine autonomous AI coding-agent repair experiment conducted during **Phase 0B.1**. Unlike synthetic test harnesses that perform string replacement in-process, this experiment was conducted live by an AI agent interacting with the `wonc` compiler CLI and file system tools.

---

## 2. Experimental Setup

- **Compiler Binary**: `compiler/poc/bin/wonc` (Git revision: `3e83078` + Phase 0B.1 remediation patches)
- **Target Fixture**: `tests/ai_repair/live_experiment.won`
- **Compiler Command**: `wonc check --format=json tests/ai_repair/live_experiment.won`
- **Agent Operating Mode**: Headless tool invocation (`run_command`, `edit_file`)

### 2.1 Initial Defective Source File

```won
#import <zlib.h>

fn main() -> i32 {
    let message = "WON Live AI Agent Autonomous Repair Experiment";
    let input_bytes = message.as_bytes();
    let input_len: u64 = 47;

    // Defect: compressed_buf is declared immutable (let), but passed to as_mut_ptr()
    let compressed_buf: [u8; 256] = [0; 256];
    let mut compressed_len: u64 = 256;

    let compress_status = unsafe {
        zlib::compress(
            compressed_buf.as_mut_ptr(),
            &mut compressed_len,
            input_bytes.as_ptr(),
            input_len
        )
    };

    if compress_status != 0 {
        return 1;
    }

    println("Autonomous agent repair experiment: SUCCESS");
    return 0;
}
```

---

## 3. Step 1: Compiler Diagnostic Emission

Running `wonc check --format=json` against the fixture produced the following machine-readable diagnostic output (exit code `1`):

```json
{
  "version": 1,
  "status": "error",
  "diagnostics": [
    {
      "code": "WON-MUT-001",
      "severity": "error",
      "message": "无法对不可变变量 'compressed_buf' 调用可变指针方法 (as_mut_ptr)",
      "file": "tests/ai_repair/live_experiment.won",
      "span": {
        "file": "tests/ai_repair/live_experiment.won",
        "start": {
          "line": 14,
          "column": 13,
          "byte": 420
        },
        "end": {
          "line": 14,
          "column": 40,
          "byte": 447
        }
      },
      "explanation": "变量 'compressed_buf' 使用 'let' 声明为不可变。获取可变原生指针 (*mut) 要求其定义带有 'mut' 标记。",
      "fixes": [
        {
          "description": "将变量 'compressed_buf' 声明修改为可变 ('let mut')",
          "edits": [
            {
              "start": 267,
              "end": 270,
              "replacement": "let mut"
            }
          ]
        }
      ]
    }
  ]
}
```

---

## 4. Step 2: Agent Reasoning & Diagnosis

Upon parsing the JSON output, the agent established the following diagnostic deductions:
1. **Error Classification**: Error code `WON-MUT-001` represents a mutability borrow violation.
2. **Defect Location**: At line 14, column 13 (byte offset 420-447), method `compressed_buf.as_mut_ptr()` is called.
3. **Root Cause**: The variable `compressed_buf` was declared at line 9 with immutable qualifier `let`, but `as_mut_ptr()` requires mutable storage (`*mut u8`).
4. **Actionable Remediation**: The compiler's `fixes[0].edits[0]` specifies replacing byte offset `[267:270]` (`let`) with `let mut`.
5. **Tool Action**: Apply surgical edit to line 9:
   - Target: `let compressed_buf: [u8; 256] = [0; 256];`
   - Replacement: `let mut compressed_buf: [u8; 256] = [0; 256];`

---

## 5. Step 3: Surgical Tool Application

The agent dispatched the `edit_file` tool call to update `tests/ai_repair/live_experiment.won`:

```diff
--- a/tests/ai_repair/live_experiment.won
+++ b/tests/ai_repair/live_experiment.won
@@ -9,1 +9,1 @@
-    let compressed_buf: [u8; 256] = [0; 256];
+    let mut compressed_buf: [u8; 256] = [0; 256];
```

---

## 6. Step 4: Verification & Binary Execution

The agent re-checked the modified file and built the native binary:

```bash
$ compiler/poc/bin/wonc check --format=json tests/ai_repair/live_experiment.won
{
  "version": 1,
  "status": "ok",
  "diagnostics": []
}

$ compiler/poc/bin/wonc build -o tests/ai_repair/live_experiment_bin tests/ai_repair/live_experiment.won
[wonc] 成功编译生成原生机器码可执行文件: tests/ai_repair/live_experiment_bin

$ ./tests/ai_repair/live_experiment_bin
Autonomous agent repair experiment: SUCCESS
```

Exit code: `0`.

---

## 7. Conclusions

1. **Deterministic Agent Feedback**: The structured JSON format (`version`, `status`, `diagnostics` with `code`, `span`, `explanation`, and `fixes`) enabled the AI agent to pinpoint the defect, understand the root cause without ambiguous natural-language parsing, and apply the exact patch.
2. **Zero Secondary Regression**: The edit was strictly local to the declaration without modifying any surrounding program logic.
3. **Protocol Test Distinction**: The repository maintains `compiler/poc/tests/protocol_test.go` as an automated regression test for the diagnostic serialization schema and edit-splicing protocol in CI, while this document records the live interactive agent verification.

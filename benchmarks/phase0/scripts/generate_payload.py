#!/usr/bin/env python3
"""
生成 Phase 0B 基准测试所需的 1MB 确定性测试数据载荷
记录精确的 SHA-256 指纹，确保各语言基准测试在完全相同的负载与压缩率下运行。
"""

import hashlib
import os

PAYLOAD_SIZE = 1048576  # 1 MB (1024 * 1024)
OUTPUT_PATH = os.path.abspath(
    os.path.join(os.path.dirname(__file__), "..", "payloads", "payload_1mb.txt")
)

PATTERN = (
    "WON: An independent, high-performance compiled programming language.\n"
    "One tool. One file. Any C library. Built for AI agents.\n"
    "Designed and engineered by KitiLab. Performance without compromises.\n"
    "Memory-safety with ownership-lite semantics, zero GC pauses, no GIL.\n"
)

def generate_payload():
    os.makedirs(os.path.dirname(OUTPUT_PATH), exist_ok=True)
    pattern_bytes = PATTERN.encode("utf-8")
    period = len(pattern_bytes)
    
    repeats = (PAYLOAD_SIZE // period) + 1
    full_data = (pattern_bytes * repeats)[:PAYLOAD_SIZE]

    with open(OUTPUT_PATH, "wb") as f:
        f.write(full_data)

    sha256_hash = hashlib.sha256(full_data).hexdigest()
    print(f"成功生成 1MB 确定性基准测试载荷: {OUTPUT_PATH}")
    print(f"字节长度: {len(full_data)} 字节")
    print(f"SHA-256: {sha256_hash}")
    return sha256_hash

if __name__ == "__main__":
    generate_payload()

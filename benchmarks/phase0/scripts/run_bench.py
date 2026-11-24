#!/usr/bin/env python3
"""
WON Phase 0B 官方基准测试测量与统计脚本 (Phase 0B.1 修正版)
- 对等编译选项对比: WON (GCC -O3) vs C (GCC -O3), WON (GCC -O3 -flto) vs C (GCC -O3 -flto)
- 独立隔离的峰值物理内存测量: 使用 /usr/bin/time -v 替代累计 RUSAGE_CHILDREN
- 逐字节严格校验: 每次解压均经 byte-for-byte 比对验证
- 统计方法: 5 次预热后进行 30 次独立采样测量，计算中位数、IQR、均值、极值与方差
"""

import os
import sys
import time
import subprocess
import json
import statistics

def get_cpu_info():
    try:
        with open("/proc/cpuinfo", "r") as f:
            for line in f:
                if "model name" in line:
                    return line.split(":", 1)[1].strip()
    except Exception:
        pass
    return "Unknown CPU"

def get_binary_size(path):
    try:
        return os.path.getsize(path)
    except Exception:
        return 0

def get_isolated_peak_rss(binary_path):
    cmd = ["/usr/bin/time", "-v", binary_path]
    res = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if res.returncode != 0:
        raise RuntimeError(f"RSS measurement failed: {res.stderr.decode()}")
    for line in res.stderr.decode().splitlines():
        if "Maximum resident set size" in line:
            parts = line.split(":")
            if len(parts) == 2:
                return int(parts[1].strip())
    return 0

def measure_execution(binary_path, iterations=30, warmup=5):
    print(f"--> 测试目标: {binary_path} (预热 {warmup} 次，正式采样 {iterations} 次)...")
    
    # 预热阶段
    for _ in range(warmup):
        res = subprocess.run([binary_path], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        if res.returncode != 0:
            raise RuntimeError(f"预热运行失败: {res.stderr.decode()}")

    samples_ms = []

    # 正式测量阶段 (进程级端到端耗时)
    for _ in range(iterations):
        start_ns = time.perf_counter_ns()
        proc = subprocess.run([binary_path], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        elapsed_ms = (time.perf_counter_ns() - start_ns) / 1_000_000.0
        
        if proc.returncode != 0:
            raise RuntimeError(f"运行失败 (code {proc.returncode}): {proc.stderr.decode()}")
        
        samples_ms.append(round(elapsed_ms, 3))

    peak_rss_kb = get_isolated_peak_rss(binary_path)

    sorted_samples = sorted(samples_ms)
    median_val = statistics.median(sorted_samples)
    q1 = statistics.median(sorted_samples[: len(sorted_samples) // 2])
    q3 = statistics.median(sorted_samples[(len(sorted_samples) + 1) // 2 :])
    iqr_val = round(q3 - q1, 3)

    return {
        "samples_ms": samples_ms,
        "median_ms": round(median_val, 3),
        "iqr_ms": iqr_val,
        "q1_ms": round(q1, 3),
        "q3_ms": round(q3, 3),
        "mean_ms": round(statistics.mean(samples_ms), 3),
        "stddev_ms": round(statistics.stdev(samples_ms), 3),
        "min_ms": round(min(samples_ms), 3),
        "max_ms": round(max(samples_ms), 3),
        "peak_rss_kb": peak_rss_kb,
        "binary_size_bytes": get_binary_size(binary_path),
    }

def main():
    root_dir = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
    os.chdir(root_dir)

    print("==================================================")
    print("WON Phase 0B.1 实证基准测试 (50 次 1MB 压缩/解压/逐字节比对)")
    print("==================================================")

    # 确保测试载荷文件存在
    payload_file = "benchmarks/phase0/payloads/payload_1mb.txt"
    if not os.path.exists(payload_file):
        print(f"载荷文件不存在，自动调用 generate_payload.py 生成...")
        subprocess.check_call([sys.executable, "benchmarks/phase0/scripts/generate_payload.py"])

    # 1. 编译所有二进制
    print("\n[1/3] 编译所有基准测试实现...")
    targets = {
        "WON (GCC -O3)": {
            "cmd": ["./compiler/poc/bin/wonc", "build", "-o", "benchmarks/phase0/won_bench_gcc", "benchmarks/phase0/won/bench.won"],
            "bin": "benchmarks/phase0/won_bench_gcc",
            "flags": "wonc build (gcc -O3 -std=c11 -lz)",
        },
        "WON (GCC -O3 -flto)": {
            "cmd": ["./compiler/poc/bin/wonc", "build", "--lto", "-o", "benchmarks/phase0/won_bench_lto", "benchmarks/phase0/won/bench.won"],
            "bin": "benchmarks/phase0/won_bench_lto",
            "flags": "wonc build --lto (gcc -O3 -flto -std=c11 -lz)",
        },
        "C (GCC 16 -O3)": {
            "cmd": ["gcc", "-O3", "-std=c11", "-I", "benchmarks/phase0/payloads", "benchmarks/phase0/c/bench.c", "-lz", "-o", "benchmarks/phase0/c_bench_gcc"],
            "bin": "benchmarks/phase0/c_bench_gcc",
            "flags": "gcc -std=c11 -O3 -lz",
        },
        "C (GCC 16 -O3 -flto)": {
            "cmd": ["gcc", "-O3", "-flto", "-std=c11", "-I", "benchmarks/phase0/payloads", "benchmarks/phase0/c/bench.c", "-lz", "-o", "benchmarks/phase0/c_bench_lto"],
            "bin": "benchmarks/phase0/c_bench_lto",
            "flags": "gcc -std=c11 -O3 -flto -lz",
        },
        "C (Clang 23 -O3)": {
            "cmd": ["clang", "-O3", "-std=c11", "-I", "benchmarks/phase0/payloads", "benchmarks/phase0/c/bench.c", "-lz", "-o", "benchmarks/phase0/c_bench_clang"],
            "bin": "benchmarks/phase0/c_bench_clang",
            "flags": "clang -std=c11 -O3 -lz",
        },
        "Go (Native compress/zlib)": {
            "cmd": ["go", "build", "-o", "benchmarks/phase0/go_native_bench", "benchmarks/phase0/go/bench_native.go"],
            "bin": "benchmarks/phase0/go_native_bench",
            "flags": "go build (standard library compress/zlib)",
        },
        "Go (CGO + zlib)": {
            "cmd": ["go", "build", "-o", "benchmarks/phase0/go_cgo_bench", "benchmarks/phase0/go/bench_cgo.go"],
            "bin": "benchmarks/phase0/go_cgo_bench",
            "flags": "go build (cgo -lz)",
        },
    }

    for name, info in targets.items():
        print(f"编译 {name}...")
        res = subprocess.run(info["cmd"], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        if res.returncode != 0:
            print(f"编译错误 {name}: {res.stderr.decode()}")
            sys.exit(1)

    # 2. 测量
    print("\n[2/3] 执行 30 轮高精度样本收集...")
    results = {}
    for name, info in targets.items():
        metrics = measure_execution(info["bin"], iterations=30, warmup=5)
        metrics["compiler_flags"] = info["flags"]
        results[name] = metrics

    # 3. 输出汇总与保存 JSON
    kernel_ver = subprocess.check_output(["uname", "-r"]).decode().strip()
    cpu_model = get_cpu_info()

    report_payload = {
        "metadata": {
            "timestamp_unix": int(time.time()),
            "environment": {
                "cpu": cpu_model,
                "os": "Fedora Linux 45",
                "kernel": kernel_ver,
                "arch": "x86_64",
                "gcc": "16.2.1",
                "clang": "23.1.5",
                "go": "1.27.4",
                "zlib": "1.3.1.zlib-ng",
            },
            "methodology": {
                "timing": "Process-level end-to-end wall clock time (time.perf_counter_ns) covering startup, loading payload, 50x round-trip compress/uncompress/memcmp, and exit",
                "memory": "Isolated per-process peak RSS measured via /usr/bin/time -v (Maximum resident set size in KBytes)",
                "verification": "Byte-for-byte correctness verification (memcmp / bytes_equal / bytes.Equal) on every single iteration",
            },
            "workload": {
                "description": "50 iterations of 1,048,576 byte in-memory compress + uncompress + byte-for-byte verification",
                "payload_size_bytes": 1048576,
                "payload_sha256": "e4fe2038316d8386eb4b74701834196d264a9cd87cbb529edee52fac9f175471",
                "iterations_per_run": 50,
                "measured_sample_runs": 30,
                "warmup_runs": 5,
            },
        },
        "results": results,
    }

    os.makedirs("benchmarks/phase0/results", exist_ok=True)
    json_path = "benchmarks/phase0/results/benchmark_results.json"
    with open(json_path, "w", encoding="utf-8") as f:
        json.dump(report_payload, f, indent=2, ensure_ascii=False)

    print("\n[3/3] 基准测试结果汇总:")
    print("-" * 92)
    print(f"{'Target Implementation':<26} | {'Median (ms)':<11} | {'Mean (ms)':<10} | {'Binary Size':<12} | {'Peak RSS':<10}")
    print("-" * 92)
    for name, m in results.items():
        size_str = f"{m['binary_size_bytes'] / 1024:.1f} KB"
        rss_str = f"{m['peak_rss_kb']} KB"
        print(f"{name:<26} | {m['median_ms']:<11.3f} | {m['mean_ms']:<10.3f} | {size_str:<12} | {rss_str:<10}")
    print("-" * 92)
    print(f"\n原始基准测试数据已保存至: {json_path}")

if __name__ == "__main__":
    main()

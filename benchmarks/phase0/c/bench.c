// C 语言 Phase 0B 基准测试对比基线 (GCC/Clang -O3 -flto)
// 内存分配策略说明 (Allocation Disclosure):
// 当前 C 基准实现采用堆分配 (malloc/free)，而 bench.won 采用函数局部数组 ([u8; N])。
// 缓冲区分配均在 50 次压缩/解压循环之外完成，对循环内部执行吞吐无直接影响。
// 函数局部数组的零初始化与物理页预映射使 WON 进程峰值驻留集 (Peak RSS) 略高于 C 堆分配 (~5.3MB vs ~4.3MB)。
#define _POSIX_C_SOURCE 200809L
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <zlib.h>
#include "payload_loader.h"

#define PAYLOAD_SIZE 1048576UL
#define MAX_ITER 50

int main(void) {
    unsigned char* input_buf = (unsigned char*)malloc(PAYLOAD_SIZE);
    if (!input_buf) {
        fprintf(stderr, "Failed to allocate input buffer\n");
        return 1;
    }

    load_benchmark_payload("benchmarks/phase0/payloads/payload_1mb.txt", input_buf, PAYLOAD_SIZE);

    unsigned long comp_bound = compressBound(PAYLOAD_SIZE);
    unsigned char* comp_buf = (unsigned char*)malloc(comp_bound);
    unsigned char* decomp_buf = (unsigned char*)malloc(PAYLOAD_SIZE);

    if (!comp_buf || !decomp_buf) {
        fprintf(stderr, "Failed to allocate compression buffers\n");
        return 1;
    }

    for (int iter = 0; iter < MAX_ITER; iter++) {
        unsigned long comp_len = comp_bound;
        int c_status = compress(comp_buf, &comp_len, input_buf, PAYLOAD_SIZE);
        if (c_status != Z_OK) {
            fprintf(stderr, "C compress failed: %d\n", c_status);
            return 2;
        }

        unsigned long decomp_len = PAYLOAD_SIZE;
        int d_status = uncompress(decomp_buf, &decomp_len, comp_buf, comp_len);
        if (d_status != Z_OK) {
            fprintf(stderr, "C uncompress failed: %d\n", d_status);
            return 3;
        }

        if (decomp_len != PAYLOAD_SIZE) {
            fprintf(stderr, "C decompressed length mismatch\n");
            return 4;
        }

        if (memcmp(input_buf, decomp_buf, PAYLOAD_SIZE) != 0) {
            fprintf(stderr, "C decompressed bytes mismatch\n");
            return 5;
        }
    }

    printf("C benchmark success! Iterations: %d\n", MAX_ITER);

    free(input_buf);
    free(comp_buf);
    free(decomp_buf);
    return 0;
}

// C 语言 Phase 0B 基准测试对比基线 (GCC/Clang -O3 -flto)
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
    }

    printf("C benchmark success! Iterations: %d\n", MAX_ITER);

    free(input_buf);
    free(comp_buf);
    free(decomp_buf);
    return 0;
}

#ifndef PAYLOAD_LOADER_H
#define PAYLOAD_LOADER_H

#include <stdio.h>
#include <stdlib.h>

static inline void load_benchmark_payload(const char* path, unsigned char* dest, unsigned long size) {
    FILE* f = fopen(path, "rb");
    if (!f) {
        fprintf(stderr, "[BENCHMARK ERROR] 无法打开测试数据载荷文件: %s\n", path);
        exit(1);
    }
    size_t n = fread(dest, 1, size, f);
    if (n != size) {
        fprintf(stderr, "[BENCHMARK ERROR] 数据载荷读取不完整: 读取 %zu / 期望 %lu 字节\n", n, size);
        exit(1);
    }
    fclose(f);
}

#endif // PAYLOAD_LOADER_H

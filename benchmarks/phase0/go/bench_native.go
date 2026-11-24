// Go 标准库 compress/zlib 基线测试
package main

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"os"
)

const (
	payloadSize = 1048576
	maxIter     = 50
)

func main() {
	inputBuf, err := os.ReadFile("benchmarks/phase0/payloads/payload_1mb.txt")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read payload: %v\n", err)
		os.Exit(1)
	}

	if len(inputBuf) != payloadSize {
		fmt.Fprintf(os.Stderr, "Payload size mismatch: %d != %d\n", len(inputBuf), payloadSize)
		os.Exit(1)
	}

	var compBuf bytes.Buffer
	decompBuf := make([]byte, payloadSize)

	for iter := 0; iter < maxIter; iter++ {
		compBuf.Reset()

		// 压缩
		w := zlib.NewWriter(&compBuf)
		if _, err := w.Write(inputBuf); err != nil {
			fmt.Fprintf(os.Stderr, "Go compress write failed: %v\n", err)
			os.Exit(2)
		}
		if err := w.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Go compress close failed: %v\n", err)
			os.Exit(2)
		}

		// 解压缩
		r, err := zlib.NewReader(&compBuf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Go uncompress reader init failed: %v\n", err)
			os.Exit(3)
		}
		n, err := io.ReadFull(r, decompBuf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Go uncompress read failed: %v\n", err)
			os.Exit(3)
		}
		if err := r.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Go uncompress close failed: %v\n", err)
			os.Exit(3)
		}

		if n != payloadSize {
			fmt.Fprintf(os.Stderr, "Go decompressed length mismatch: %d\n", n)
			os.Exit(4)
		}
	}

	fmt.Printf("Go Native benchmark success! Iterations: %d\n", maxIter)
}

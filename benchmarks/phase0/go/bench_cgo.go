// Go + CGO 直接调用系统底层 zlib 原生库对比基线
package main

/*
#cgo LDFLAGS: -lz
#include <zlib.h>
#include <stdlib.h>
*/
import "C"
import (
	"bytes"
	"fmt"
	"os"
	"unsafe"
)

const (
	payloadSize = 1048576
	maxIter     = 50
)

func main() {
	inputData, err := os.ReadFile("benchmarks/phase0/payloads/payload_1mb.txt")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read payload: %v\n", err)
		os.Exit(1)
	}

	inputPtr := (*C.Bytef)(unsafe.Pointer(&inputData[0]))
	inputLen := C.uLong(len(inputData))

	compBound := C.compressBound(inputLen)
	compBuf := make([]byte, compBound)
	compPtr := (*C.Bytef)(unsafe.Pointer(&compBuf[0]))

	decompBuf := make([]byte, payloadSize)
	decompPtr := (*C.Bytef)(unsafe.Pointer(&decompBuf[0]))

	for iter := 0; iter < maxIter; iter++ {
		compLen := compBound
		cStatus := C.compress(compPtr, &compLen, inputPtr, inputLen)
		if cStatus != C.Z_OK {
			fmt.Fprintf(os.Stderr, "CGO compress failed: %d\n", cStatus)
			os.Exit(2)
		}

		decompLen := C.uLong(payloadSize)
		dStatus := C.uncompress(decompPtr, &decompLen, compPtr, compLen)
		if dStatus != C.Z_OK {
			fmt.Fprintf(os.Stderr, "CGO uncompress failed: %d\n", dStatus)
			os.Exit(3)
		}

		if int(decompLen) != payloadSize {
			fmt.Fprintf(os.Stderr, "CGO decompressed length mismatch\n")
			os.Exit(4)
		}

		if !bytes.Equal(inputData, decompBuf) {
			fmt.Fprintf(os.Stderr, "CGO byte mismatch\n")
			os.Exit(5)
		}
	}

	fmt.Printf("Go CGO benchmark success! Iterations: %d\n", maxIter)
}

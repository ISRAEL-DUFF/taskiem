// Package pywasm carries CPython compiled to WebAssembly (WASI) for Python
// code steps; see PROVENANCE.md.
package pywasm

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
)

//go:embed python.wasm.gz
var compressed []byte

// SHA256 is the expected hash of the uncompressed module.
const SHA256 = "e5dc5a398b07b54ea8fdb503bf68fb583d533f10ec3f930963e02b9505f7a763"

// Version is the CPython version of the module.
const Version = "3.12.0"

// Module returns the uncompressed module, checked against SHA256.
func Module() ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	wasm, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(wasm)
	if got := hex.EncodeToString(sum[:]); got != SHA256 {
		return nil, fmt.Errorf("pywasm: module hash %s, want %s", got, SHA256)
	}
	return wasm, nil
}

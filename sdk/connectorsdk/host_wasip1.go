//go:build wasip1

package connectorsdk

import "unsafe"

//go:wasmimport taskiem input_read
func inputRead(ptr unsafe.Pointer)

//go:wasmimport taskiem http_request
func httpRequest(ptr unsafe.Pointer, n uint32) uint32

//go:wasmimport taskiem http_response_read
func httpResponseRead(ptr unsafe.Pointer)

//go:wasmimport taskiem log
func logLine(ptr unsafe.Pointer, n uint32)

// result stays reachable until the next call, while the engine reads it.
var result []byte

//go:wasmexport taskiem_execute_v1
func execute(n uint32) uint64 {
	in := make([]byte, n)
	if n > 0 {
		inputRead(unsafe.Pointer(&in[0]))
	}
	result = Run(in)
	return uint64(uintptr(unsafe.Pointer(&result[0])))<<32 | uint64(len(result))
}

func hostHTTP(req []byte) []byte {
	n := httpRequest(unsafe.Pointer(&req[0]), uint32(len(req)))
	out := make([]byte, n)
	if n > 0 {
		httpResponseRead(unsafe.Pointer(&out[0]))
	}
	return out
}

func hostLog(s string) {
	if s == "" {
		return
	}
	b := []byte(s)
	logLine(unsafe.Pointer(&b[0]), uint32(len(b)))
}

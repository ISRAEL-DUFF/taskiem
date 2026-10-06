// Package wasmtest builds the example WebAssembly connector for tests.
package wasmtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var (
	once             sync.Once
	module, manifest []byte
	buildErr         error
)

// root is the repository root, from this file's location.
func root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// Example returns the example connector's manifest and module
// (examples/wasm-connector), built once per test binary.
func Example(t testing.TB) (manifestYAML, wasm []byte) {
	t.Helper()
	once.Do(func() {
		dir, err := os.MkdirTemp("", "wasmtest")
		if err != nil {
			buildErr = err
			return
		}
		defer func() { _ = os.RemoveAll(dir) }()
		src := filepath.Join(root(), "examples", "wasm-connector")
		out := filepath.Join(dir, "connector.wasm")
		cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, ".") //nolint:gosec // fixed arguments
		cmd.Dir = src
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		if b, err := cmd.CombinedOutput(); err != nil {
			buildErr = &buildError{err: err, out: string(b)}
			return
		}
		if module, buildErr = os.ReadFile(out); buildErr != nil { //nolint:gosec // our temp file
			return
		}
		manifest, buildErr = os.ReadFile(filepath.Join(src, "manifest.yaml")) //nolint:gosec // repository file
	})
	if buildErr != nil {
		t.Fatalf("building the example connector: %v", buildErr)
	}
	return manifest, module
}

type buildError struct {
	err error
	out string
}

func (e *buildError) Error() string { return e.err.Error() + "\n" + e.out }

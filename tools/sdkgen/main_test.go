package main

import (
	"bytes"
	"os"
	"testing"
)

// The committed helpers match the manifests built into the binary.
func TestGeneratedHelpersAreCurrent(t *testing.T) {
	want, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../sdk/src/connectors.gen.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("sdk/src/connectors.gen.ts is stale: run `go generate ./sdk`")
	}
}

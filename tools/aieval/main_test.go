package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedSuiteRunsOffline(t *testing.T) {
	out := filepath.Join(t.TempDir(), "report.json")
	var buf strings.Builder
	if err := run([]string{"-suite", "../../evals/builder", "-tags", "dogfood", "-out", out}, &buf); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var rep Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Cases != 4 || rep.Errors != 0 || rep.Provider != "fake" || len(rep.Results) != 4 {
		t.Fatalf("report: %+v", rep)
	}
	if !strings.Contains(buf.String(), "valid on first try") {
		t.Errorf("summary: %s", buf.String())
	}
	// The whole seed suite parses and has unique ids.
	cases, err := load("../../evals/builder")
	if err != nil || len(cases) < 25 {
		t.Fatalf("seed suite: %d cases, %v", len(cases), err)
	}
}

func TestGate(t *testing.T) {
	var buf strings.Builder
	err := run([]string{"-suite", "../../evals/builder", "-limit", "1", "-min-first-try", "1.01"}, &buf)
	if err == nil || !strings.Contains(err.Error(), "below") {
		t.Fatalf("gate: %v", err)
	}
}

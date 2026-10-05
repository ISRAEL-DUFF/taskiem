package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRepoContracts(t *testing.T) {
	files, _ := filepath.Glob("../../flows/*/*.wd.json")
	manifests, _ := filepath.Glob("../../connectors/*/manifest.yaml")
	files = append(files, manifests...)
	var out bytes.Buffer
	if err := run(append([]string{"validate"}, files...), &out, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got := strings.Count(out.String(), "ok    "); got != len(files) {
		t.Errorf("%d ok lines for %d files:\n%s", got, len(files), out.String())
	}
}

func TestValidateReportsProblems(t *testing.T) {
	f := filepath.Join(t.TempDir(), "bad.wd.json")
	_ = os.WriteFile(f, []byte(`{"schema":"wd/v1","id":"wf_x","version":1,"name":"x","trigger":{"type":"manual"},"steps":[{"id":"a","type":"wait","needs":["a"],"config":{"duration":"1s"}}]}`), 0o644)
	var out bytes.Buffer
	err := run([]string{"validate", f}, &out, &out)
	if err == nil || !strings.Contains(out.String(), "needs itself") {
		t.Errorf("err=%v out=%s", err, out.String())
	}
}

func TestServeRejectsUnknownRole(t *testing.T) {
	if err := run([]string{"serve", "--role", "nope"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "unknown role") {
		t.Errorf("got %v", err)
	}
}

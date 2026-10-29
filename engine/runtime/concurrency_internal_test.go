package runtime

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The lease-expiry runbook tells operators how many steps a worker runs
// at once, and that no setting changes it; it must say what the worker
// does, and must not send them to a setting that does not exist.
func TestRunbookStatesWorkerConcurrency(t *testing.T) {
	for queue, want := range map[string]int{"connector": 16, "ai": 16, "sandbox": 4, "container": 4} {
		w := &Worker{Queue: queue}
		w.defaults()
		if w.Concurrency != want {
			t.Errorf("%s worker concurrency %d, want %d (and update docs/reliability.md)", queue, w.Concurrency, want)
		}
	}
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "reliability.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "runs up to 16 steps at once on each queue it serves (4 on `sandbox` and `container`), and no setting changes that") {
		t.Error("docs/reliability.md does not state the worker's concurrency")
	}
	if m := regexp.MustCompile("`TASKIEM_[A-Z_]+` concurrency").FindString(string(doc)); m != "" {
		t.Errorf("docs/reliability.md names %q, but worker concurrency has no setting", m)
	}
}

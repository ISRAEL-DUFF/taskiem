package ai_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// The hard safety rule (spec 12, gate G3): the AI layer can never publish,
// approve, read secrets, or execute a write against a real provider. It is
// enforced structurally: nothing the AI layer links can do any of those
// things. This test fails the build if engine/ai or anything under it
// comes to depend, even transitively, on code that can.
var forbidden = []struct{ pkg, why string }{
	{"github.com/israel-duff/taskiem/engine/secrets", "the vault: secrets and connection credentials"},
	{"github.com/israel-duff/taskiem/engine/runtime", "workers, publishing state and approval decisions"},
	{"github.com/israel-duff/taskiem/engine/db", "database access (saving, publishing, approving)"},
	{"github.com/israel-duff/taskiem/engine/audit", "the audit chain (the API records AI actions, not the AI)"},
	{"github.com/israel-duff/taskiem/engine/ingest", "triggers and run starts"},
	{"github.com/israel-duff/taskiem/engine/wasmconn", "executing tenants' connectors"},
	{"github.com/israel-duff/taskiem/engine/sandbox", "executing code steps"},
	{"github.com/israel-duff/taskiem/engine/wdcheck", "pulls in ingest and the runtime; the API injects the check"},
	{"github.com/israel-duff/taskiem/api", "the API and its principals"},
	{"github.com/israel-duff/taskiem/connectors/", "connector handlers that call real providers"},
	{"github.com/israel-duff/taskiem/engine/shadow", "the shadow sandbox holds a failed run's opened recording; repair gets only its sanitised verdict"},
	{"github.com/israel-duff/taskiem/engine/drift", "drift records are read by the repair service and passed in as plain findings"},
}

// The packages the rule must cover: a package moved out of engine/ai
// would silently escape it.
var covered = []string{
	"github.com/israel-duff/taskiem/engine/ai",
	"github.com/israel-duff/taskiem/engine/ai/builder",
	"github.com/israel-duff/taskiem/engine/ai/repair",
}

func TestImportGraphExcludesPrivilegedCode(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not available")
	}
	out, err := exec.Command(goTool, "list", "-deps", "github.com/israel-duff/taskiem/engine/ai/...").CombinedOutput() //nolint:gosec // fixed arguments
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	deps := strings.Fields(string(out))
	if len(deps) < 10 {
		t.Fatalf("suspiciously few dependencies: %v", deps)
	}
	for _, c := range covered {
		if !slices.Contains(deps, c) {
			t.Errorf("%s is not under the import rule", c)
		}
	}
	for _, d := range deps {
		for _, f := range forbidden {
			if d == f.pkg || strings.HasPrefix(d, f.pkg+"/") || (strings.HasSuffix(f.pkg, "/") && strings.HasPrefix(d, f.pkg)) {
				t.Errorf("engine/ai depends on %s (%s)", d, f.why)
			}
		}
	}
}

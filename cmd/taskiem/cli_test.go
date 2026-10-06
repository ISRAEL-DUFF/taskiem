package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDeployDiffAndTail drives the remote commands against a real server:
// deploy creates and publishes, diff reports nothing then a step change,
// deploy publishes the change as a new version, and runs tail follows a run
// to its end.
func TestDeployDiffAndTail(t *testing.T) {
	srv := startServer(t)
	login := srv.call(t, "POST", "/v1/auth/login", "", map[string]any{"email": "admin@smoke.test", "password": "correct horse battery"})
	tok := login["token"].(string)
	key := srv.call(t, "POST", "/v1/api-keys", tok, map[string]any{"name": "ci",
		"permissions": []string{"workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start"}})["key"].(string)
	t.Setenv("TASKIEM_URL", srv.base)
	t.Setenv("TASKIEM_API_KEY", key)

	dir := t.TempDir()
	flow := filepath.Join(dir, "double.wd.json")
	write := func(factor string) {
		doc := `{"schema":"wd/v1","id":"wf_double","version":1,"name":"Double","trigger":{"type":"manual"},
		  "steps":[{"id":"double","type":"transform","config":{"output":"=trigger.body.n * ` + factor + `"}},
		           {"id":"wait","type":"wait","needs":["double"],"config":{"duration":"1s"}}]}`
		if err := os.WriteFile(flow, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cli := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := run(args, &out, &out); err != nil {
			t.Fatalf("taskiem %s: %v\n%s", strings.Join(args, " "), err, out.String())
		}
		return out.String()
	}

	write("2")
	if out := cli("diff", dir); !strings.Contains(out, `+ wf_double  new workflow "Double"`) {
		t.Errorf("diff before deploy:\n%s", out)
	}
	if out := cli("deploy", dir); !strings.Contains(out, "published version 1") {
		t.Errorf("deploy:\n%s", out)
	}
	if out := cli("diff", dir); !strings.Contains(out, "= wf_double  unchanged (version 1 is published)") {
		t.Errorf("diff after deploy:\n%s", out)
	}
	write("3")
	if out := cli("deploy", "--dry-run", dir); !strings.Contains(out, "~ wf_double  version 2 replaces published version 1") || !strings.Contains(out, "~ step double") || strings.Contains(out, "published version 2") || strings.Contains(out, "step wait") {
		t.Errorf("dry run:\n%s", out)
	}
	if out := cli("deploy", dir); !strings.Contains(out, "published version 2") {
		t.Errorf("deploy v2:\n%s", out)
	}

	list := srv.call(t, "GET", "/v1/workflows", tok, nil)
	wf := list["workflows"].([]any)[0].(map[string]any)
	if wf["key"] != "wf_double" || wf["active_version"] != float64(2) {
		t.Fatalf("workflow: %v", wf)
	}
	runID := srv.call(t, "POST", "/v1/workflows/"+wf["id"].(string)+"/runs", tok, map[string]any{"input": map[string]any{"n": 7}})["run_id"].(string)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out bytes.Buffer
	if err := runsCmd(ctx, []string{"tail", "--interval", "100ms", runID}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"RunStarted", "StepCompleted double", "StepScheduled wait", "RunCompleted", "run " + runID + " completed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("tail lacks %q:\n%s", want, out.String())
		}
	}
}

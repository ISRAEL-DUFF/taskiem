package runtime_test

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

const doubleIt = `{"schema":"wd/v1","id":"wf_double","version":1,"name":"Double it","trigger":{"type":"subflow"},
  "inputs":{"schema":{"type":"object","required":["amount_kobo"],"properties":{"amount_kobo":{"type":"integer"}}}},
  "steps":[{"id":"total","type":"transform","config":{"output":{"kobo":"=trigger.body.amount_kobo * 2"}}}],
  "outputs":[{"name":"kobo","type":"integer","title":"Total","description":"Twice","value":"=steps.total.output.kobo"}]}`

func stepOutput(t *testing.T, e *rt.Env, ref runtime.RunRef, step string) map[string]any {
	t.Helper()
	for _, ev := range events(t, e, ref) {
		if ev.Type == history.StepCompleted && ev.StepID == step {
			return payloadOf(t, ev)
		}
	}
	t.Fatalf("no %s completed: %s", step, types(events(t, e, ref)))
	return nil
}

// A subflow step (decision 0049) runs another workflow as a child run in
// the same transaction, with the parent's seed; its report is the step's
// output; a failed child fails the step; a parent that ends cancels the
// children it waits for.
func TestSubflows(t *testing.T) {
	e := rt.New(t)
	child := e.Publish(t, doubleIt)
	parent := e.Publish(t, wfDoc(`{"id":"sub","type":"subflow","config":{"workflow":"`+child.String()+`"},"input":{"amount_kobo":"=trigger.amount"}},
	  {"id":"after","type":"transform","needs":["sub"],"config":{"output":"=steps.sub.output.outputs.kobo"}}`, ""))
	ref := e.Start(t, parent, map[string]any{"amount": 21})
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("parent: %s %s", st, types(events(t, e, ref)))
	}
	if out := stepOutput(t, e, ref, "after")["output"]; out != float64(42) {
		t.Errorf("after: %v", out)
	}
	sub := stepOutput(t, e, ref, "sub")["output"].(map[string]any)
	links, err := e.Store.Forks(ctx, ref)
	if err != nil || len(links.Subflows) != 1 || links.Subflows[0].String() != sub["run_id"] || sub["status"] != "completed" {
		t.Fatalf("lineage %+v, step output %v: %v", links, sub, err)
	}
	childRef := runtime.RunRef{ID: links.Subflows[0], TenantID: e.Tenant}
	back, err := e.Store.Forks(ctx, childRef)
	if err != nil || back.Parent == nil || *back.Parent != ref.ID || back.ParentStep == nil || *back.ParentStep != "sub" || len(back.Children) != 0 {
		t.Errorf("the child's lineage: %+v %v", back, err)
	}
	var seed, by string
	if err := e.DB.Admin.QueryRow(ctx, `SELECT idempotency_seed, started_by FROM runs WHERE id = $1`, childRef.ID).Scan(&seed, &by); err != nil || seed != ref.ID.String()+"/sub" || by != "subflow:"+ref.ID.String()+"/sub" {
		t.Errorf("the child's seed %q, started by %q: %v", seed, by, err)
	}
	if trig := payloadOf(t, events(t, e, childRef)[0])["trigger"].(map[string]any); trig["type"] != "subflow" || trig["parent"].(map[string]any)["step"] != "sub" || trig["body"].(map[string]any)["amount_kobo"] != float64(21) {
		t.Errorf("the child's trigger: %v", trig)
	}

	// A child that fails fails the step.
	broken := e.Publish(t, `{"schema":"wd/v1","id":"wf_b","version":1,"name":"Broken","trigger":{"type":"subflow"},
	  "steps":[{"id":"boom","type":"transform","config":{"output":"=1 / trigger.body.zero"}}]}`)
	failing := e.Publish(t, wfDoc(`{"id":"sub","type":"subflow","config":{"workflow":"`+broken.String()+`"},"input":{"zero":0}}`, ""))
	ref2 := e.Start(t, failing, map[string]any{})
	e.Drain(t)
	if st := e.Status(t, ref2); st != "failed" {
		t.Fatalf("parent of a failing child: %s %s", st, types(events(t, e, ref2)))
	}
	for _, ev := range events(t, e, ref2) {
		if ev.Type == history.StepFailed && ev.StepID == "sub" {
			if e := payloadOf(t, ev)["error"].(map[string]any); e["kind"] != "subflow" || !strings.Contains(e["message"].(string), "the workflow it ran failed: boom") {
				t.Errorf("the step's failure: %v", e)
			}
		}
	}

	// A step that does not wait completes once the child has started.
	nowait := e.Publish(t, wfDoc(`{"id":"sub","type":"subflow","config":{"workflow":"`+child.String()+`","wait":false},"input":{"amount_kobo":1}}`, ""))
	ref3 := e.Start(t, nowait, map[string]any{})
	e.Drain(t)
	if st := e.Status(t, ref3); st != "completed" || stepOutput(t, e, ref3, "sub")["output"].(map[string]any)["status"] != "started" {
		t.Errorf("no wait: %s %v", st, stepOutput(t, e, ref3, "sub"))
	}

	// A parent that is cancelled cancels the child it waits for.
	slow := e.Publish(t, `{"schema":"wd/v1","id":"wf_s","version":1,"name":"Slow","trigger":{"type":"subflow"},
	  "steps":[{"id":"w","type":"signal","config":{"event":"never","correlation":"=run.id"}}]}`)
	waiting := e.Publish(t, wfDoc(`{"id":"sub","type":"subflow","config":{"workflow":"`+slow.String()+`"}}`, ""))
	ref4 := e.Start(t, waiting, map[string]any{})
	links, _ = e.Store.Forks(ctx, ref4)
	if len(links.Subflows) != 1 || e.Status(t, ref4) != "running" && e.Status(t, ref4) != "waiting" {
		t.Fatalf("waiting parent: %+v %s", links, e.Status(t, ref4))
	}
	if err := e.Store.CancelRun(ctx, ref4, "u", true); err != nil {
		t.Fatal(err)
	}
	if st := e.Status(t, runtime.RunRef{ID: links.Subflows[0], TenantID: e.Tenant}); st != "cancelled" {
		t.Errorf("the child after the parent's cancel: %s", st)
	}

	// A child that is cancelled fails the step.
	ref5 := e.Start(t, waiting, map[string]any{})
	links, _ = e.Store.Forks(ctx, ref5)
	if err := e.Store.CancelRun(ctx, runtime.RunRef{ID: links.Subflows[0], TenantID: e.Tenant}, "u", true); err != nil {
		t.Fatal(err)
	}
	if st := e.Status(t, ref5); st != "failed" {
		t.Errorf("parent of a cancelled child: %s %s", st, types(events(t, e, ref5)))
	}

	// A workflow with no published version, or none at all, fails the step
	// rather than the run's decision.
	draft := uuid.Must(uuid.NewV7())
	if _, err := e.DB.Admin.Exec(ctx, `INSERT INTO workflows (id, tenant_id, name, created_by) VALUES ($1, $2, 'Draft', $2)`, draft, e.Tenant); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"unpublished": draft.String(), "missing": uuid.Must(uuid.NewV7()).String()} {
		wf := e.Publish(t, wfDoc(`{"id":"sub","type":"subflow","config":{"workflow":"`+id+`"}}`, ""))
		r := e.Start(t, wf, map[string]any{})
		e.Drain(t)
		if st := e.Status(t, r); st != "failed" || !strings.Contains(types(events(t, e, r)), "StepFailed(sub)") {
			t.Errorf("%s: %s %s", name, st, types(events(t, e, r)))
		}
	}

	// A workflow that runs itself stops at the depth limit.
	selfID := uuid.Must(uuid.NewV7())
	loop := `{"schema":"wd/v1","id":"wf_loop","version":1,"name":"Loop","trigger":{"type":"subflow"},
	  "steps":[{"id":"again","type":"subflow","config":{"workflow":"` + selfID.String() + `"}}]}`
	if _, err := e.DB.Admin.Exec(ctx, `INSERT INTO workflows (id, tenant_id, name, created_by, active_version) VALUES ($1, $2, 'Loop', $2, 1)`, selfID, e.Tenant); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(loop))
	if _, err := e.DB.Admin.Exec(ctx, `INSERT INTO workflow_versions (workflow_id, version, tenant_id, definition, digest, state) VALUES ($1, 1, $2, $3, $4, 'published')`, selfID, e.Tenant, loop, sum[:]); err != nil {
		t.Fatal(err)
	}
	root := e.Start(t, selfID, map[string]any{})
	e.Drain(t)
	var runs int
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM runs WHERE workflow_id = $1`, selfID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if st := e.Status(t, root); st != "failed" || runs != 6 {
		t.Errorf("a loop: root %s, %d runs", st, runs)
	}
}

func payloadOf(t *testing.T, ev history.Event) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(ev.Payload, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

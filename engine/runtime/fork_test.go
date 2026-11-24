package runtime_test

import (
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// addVersion stores version v of a workflow, published.
func addVersion(t *testing.T, e *rt.Env, wf uuid.UUID, v int, doc string) {
	t.Helper()
	sum := sha256.Sum256([]byte(doc))
	err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO workflow_versions (workflow_id, version, tenant_id, definition, digest, state) VALUES ($1, $2, $3, $4, $5, 'published')`,
			wf, v, e.Tenant, doc, sum[:])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func fork(t *testing.T, e *rt.Env, wf uuid.UUID, v int, parent runtime.RunRef) (runtime.RunRef, error) {
	t.Helper()
	st, err := e.Store.Start(ctx, runtime.StartRequest{TenantID: e.Tenant, WorkflowID: wf, Version: v, Environment: "prod", StartedBy: "tester",
		Fork: &runtime.Fork{Parent: parent.ID}})
	return st.Ref, err
}

// The failing step reads trigger.customer.name, which the trigger lacks.
const (
	forkPay    = `{"id":"pay","type":"connector","connector":"fakepay@1","action":"transfer","input":{"amount":"=trigger.amount","logical_id":"=trigger.id"}}`
	forkCheck  = `{"id":"check","type":"connector","needs":["pay"],"connector":"fakepay@1","action":"verify","input":{"reference":"=steps.pay.output.reference"}}`
	forkNotify = `{"id":"notify","type":"connector","needs":["check"],"connector":"fakepay@1","action":"notify","input":{"logical_id":"=trigger.id + '-' + trigger.customer.name"},"retry":{"max":0}}`
	forkFixed  = `{"id":"notify","type":"connector","needs":["check"],"connector":"fakepay@1","action":"notify","input":{"logical_id":"=trigger.id + '-' + (has(trigger.customer) ? trigger.customer.name : 'unknown')"},"retry":{"max":0}}`
)

func TestForkResumesWithoutRepeatingWrites(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(forkPay+","+forkCheck+","+forkNotify, ""))
	parent := e.Start(t, wf, map[string]any{"amount": 500, "id": "order-1"})
	e.Drain(t)
	if st := e.Status(t, parent); st != "failed" {
		t.Fatalf("parent: %s\n%s", st, types(events(t, e, parent)))
	}
	if n := e.Provider.Executions("order-1"); n != 1 {
		t.Fatalf("transfer executed %d times", n)
	}
	addVersion(t, e, wf, 2, wfDoc(forkPay+","+forkCheck+","+forkFixed, ""))

	child, err := fork(t, e, wf, 2, parent)
	if err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	h := events(t, e, child)
	if st := e.Status(t, child); st != "completed" {
		t.Fatalf("fork: %s\n%s", st, types(h))
	}
	// The transfer and the read were replayed, never sent: no task, no intent.
	if n := e.Provider.Executions("order-1"); n != 1 {
		t.Errorf("the completed write was sent again: %d executions", n)
	}
	if count(h, history.EffectIntent, "pay") != 0 || count(h, history.StepStarted, "check") != 0 {
		t.Errorf("replayed steps were executed:\n%s", types(h))
	}
	if n := e.Provider.Executions("order-1-unknown"); n != 1 {
		t.Errorf("the failed step ran %d times on the new version", n)
	}
	info, err := e.Store.Forks(ctx, child)
	if err != nil || info.Parent == nil || *info.Parent != parent.ID || info.ResumedStep == nil || *info.ResumedStep != "notify" {
		t.Errorf("fork link: %+v %v", info, err)
	}
	// The fork keeps the parent's seed: the notify key is the parent's.
	for _, ev := range h {
		if ev.Type == history.EffectIntent && ev.StepID == "notify" && !strings.Contains(string(ev.Payload), parent.ID.String()) {
			t.Errorf("the fork's key is not seeded by the parent: %s", ev.Payload)
		}
	}
	// Only failed runs are resumed.
	if _, err := fork(t, e, wf, 2, child); err == nil {
		t.Errorf("a completed run was resumed")
	} else if _, ok := runtime.IsNotResumable(err); !ok {
		t.Errorf("error: %v", err)
	}
}

// A write whose input the new version changes is parked for a person,
// neither replayed nor sent.
func TestForkParksAChangedWrite(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(forkPay+","+forkCheck+","+forkNotify, ""))
	parent := e.Start(t, wf, map[string]any{"amount": 500, "id": "order-2"})
	e.Drain(t)
	changed := `{"id":"pay","type":"connector","connector":"fakepay@1","action":"transfer","input":{"amount":"=trigger.amount + 1","logical_id":"=trigger.id"}}`
	addVersion(t, e, wf, 2, wfDoc(changed+","+forkCheck+","+forkFixed, ""))
	child, err := fork(t, e, wf, 2, parent)
	if err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	if st := e.Status(t, child); st != "needs_reconciliation" {
		t.Fatalf("fork: %s\n%s", st, types(events(t, e, child)))
	}
	if n := e.Provider.Executions("order-2"); n != 1 {
		t.Errorf("transfer executed %d times", n)
	}
}

// A run whose write may have happened cannot be resumed.
func TestForkRefusesUncertainRuns(t *testing.T) {
	e := rt.New(t)
	e.Provider.Faults = func(action string) rt.Fault {
		if action == "notify" {
			return rt.FailAfter
		}
		return rt.NoFault
	}
	notify := `{"id":"notify","type":"connector","connector":"fakepay@1","action":"notify","input":{"logical_id":"=trigger.id"}}`
	wf := e.Publish(t, wfDoc(notify, ""))
	parent := e.Start(t, wf, map[string]any{"id": "n-1"})
	e.Drain(t)
	if st := e.Status(t, parent); st != "needs_reconciliation" {
		t.Fatalf("parent: %s", st)
	}
	if _, err := fork(t, e, wf, 1, parent); err == nil {
		t.Fatal("an uncertain run was resumed")
	} else if _, ok := runtime.IsNotResumable(err); !ok {
		t.Fatalf("error: %v", err)
	}
}

// A failed run is resumed once: a second resume (another repair proposal
// for the same run) is refused, naming the run that resumed it, since both
// would send the steps after the failure. The same request again is the
// same resume, and a resume that failed in its turn is resumed itself.
func TestForkOnlyOnce(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(forkPay+","+forkCheck+","+forkNotify, ""))
	parent := e.Start(t, wf, map[string]any{"amount": 100, "id": "ord-1"})
	e.Drain(t)
	resume := func(from runtime.RunRef, v int, proposal string) (runtime.Started, error) {
		return e.Store.Start(ctx, runtime.StartRequest{TenantID: e.Tenant, WorkflowID: wf, Version: v, Environment: "prod", StartedBy: "tester",
			TriggerID: "repair/" + proposal, DedupKey: "resume", Fork: &runtime.Fork{Parent: from.ID}})
	}
	first, err := resume(parent, 1, "a")
	if err != nil || !first.Created {
		t.Fatalf("first resume: %+v %v", first, err)
	}
	e.Drain(t)
	if st := e.Status(t, first.Ref); st != "failed" {
		t.Fatalf("first resume on the same version: %s", st)
	}
	if again, err := resume(parent, 1, "a"); err != nil || again.Created || again.Ref.ID != first.Ref.ID {
		t.Errorf("the same resume again: %+v %v", again, err)
	}
	if _, err := resume(parent, 1, "b"); err == nil {
		t.Fatal("a run already resumed was resumed again")
	} else if nr, ok := runtime.IsNotResumable(err); !ok || !strings.Contains(nr.Reason, first.Ref.ID.String()) {
		t.Errorf("second resume: %v", err)
	}
	addVersion(t, e, wf, 2, wfDoc(forkPay+","+forkCheck+","+forkFixed, ""))
	second, err := resume(first.Ref, 2, "c")
	if err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	if st := e.Status(t, second.Ref); st != "completed" {
		t.Errorf("resume of the failed resume: %s", st)
	}
	if n := e.Provider.Executions("ord-1"); n != 1 {
		t.Errorf("the transfer ran %d times", n)
	}
	if n := e.Provider.Executions("ord-1-unknown"); n != 1 {
		t.Errorf("the notify after the failure ran %d times", n)
	}
}

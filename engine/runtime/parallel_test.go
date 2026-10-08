package runtime_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/history"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// The losing branch of a join "any" parallel: its open signal wait is
// removed, its completed payment refunded, and the run completes.
func TestParallelJoinAnyCancelsLosingBranch(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(`{"id":"race","type":"parallel","config":{"join":"any","branches":[
	  {"name":"auto","steps":[
	    {"id":"pay","type":"connector","connector":"fakepay@1","action":"transfer",
	     "input":{"amount":1,"logical_id":"=run.id + ':pay'"},"compensate":{"action":"refund","input":{"logical_id":"=run.id + ':refund'"}}},
	    {"id":"settle","type":"signal","needs":["pay"],"config":{"event":"fakepay@1:transfer","correlation":"=run.id","timeout":"1h"}}]},
	  {"name":"manual","steps":[{"id":"ok","type":"approval","config":{"role":"ops"}}]}]}},
	  {"id":"done","type":"transform","needs":["race"],"config":{"output":"=has(steps.race.output.manual)"}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	if n := waits(t, e, ref.ID.String()); n != 1 {
		t.Fatalf("expected the signal wait, got %d", n)
	}
	if err := e.Store.DecideApproval(ctx, ref, "ok", "approved", "checker", "web"); err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	h := events(t, e, ref)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("status %s: %s", st, types(h))
	}
	if count(h, history.StepCancelled, "settle") != 1 || waits(t, e, ref.ID.String()) != 0 {
		t.Errorf("signal wait not cancelled: %s", types(h))
	}
	if e.Provider.Executions(ref.ID.String()+":pay") != 1 || e.Provider.Executions(ref.ID.String()+":refund") != 1 {
		t.Errorf("pay %d refund %d", e.Provider.Executions(ref.ID.String()+":pay"), e.Provider.Executions(ref.ID.String()+":refund"))
	}
}

// A losing write whose task no worker has claimed yet never runs; one a
// worker holds without having started is dropped when it gets to it.
func TestParallelCancelledWriteNeverStarts(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(`{"id":"race","type":"parallel","config":{"join":"any","branches":[
	  {"name":"a","steps":[{"id":"pay_a","type":"connector","connector":"fakepay@1","action":"transfer","input":{"amount":1,"logical_id":"=run.id + ':a'"}}]},
	  {"name":"b","steps":[{"id":"pay_b","type":"connector","connector":"fakepay@1","action":"transfer","input":{"amount":1,"logical_id":"=run.id + ':b'"}}]},
	  {"name":"manual","steps":[{"id":"ok","type":"approval","config":{"role":"ops"}}]}]}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	// A worker has claimed pay_b but not yet prepared it.
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE tasks SET lease_owner = 'w-slow', lease_until = now() + interval '1 minute' WHERE run_id = $1 AND step_id = 'pay_b'`, ref.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.DecideApproval(ctx, ref, "ok", "approved", "checker", "web"); err != nil {
		t.Fatal(err)
	}
	var left int
	_ = e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE run_id = $1`, ref.ID).Scan(&left)
	if left != 1 {
		t.Errorf("the unclaimed task should be gone and the held one kept, %d left", left)
	}
	// The slow worker's lease lapses; whoever picks the task up drops it.
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE tasks SET lease_owner = NULL, lease_until = NULL WHERE run_id = $1`, ref.ID); err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	h := events(t, e, ref)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("status %s: %s", st, types(h))
	}
	if e.Provider.Executions(ref.ID.String()+":a")+e.Provider.Executions(ref.ID.String()+":b") != 0 || count(h, history.EffectIntent, "") != 0 {
		t.Errorf("a cancelled write ran: %s", types(h))
	}
	_ = e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE run_id = $1`, ref.ID).Scan(&left)
	if left != 0 {
		t.Errorf("%d tasks left", left)
	}
}

func waits(t *testing.T, e *rt.Env, run string) int {
	t.Helper()
	var n int
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM signal_waits WHERE run_id = $1`, run).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

package runtime_test

import (
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// A subflow step in the losing branch of a join "any" parallel is
// withdrawn, and the child run it started is cancelled with what it did
// reversed (decision 0054): it does not run on beside a parent that has
// moved on, and its reversing is not cut short when the parent ends.
func TestLosingBranchCancelsItsSubflowChild(t *testing.T) {
	e := rt.New(t)
	paying := e.Publish(t, `{"schema":"wd/v1","id":"wf_p","version":1,"name":"Paying","trigger":{"type":"subflow"},
	  "steps":[{"id":"pay","type":"connector","connector":"fakepay@1","action":"transfer",
	     "input":{"amount":1,"logical_id":"=run.id + ':pay'"},"compensate":{"action":"refund","input":{"logical_id":"=run.id + ':refund'"}}},
	    {"id":"w","type":"signal","needs":["pay"],"config":{"event":"never","correlation":"=run.id"}}]}`)
	race := `{"id":"race","type":"parallel","config":{"join":"any","branches":[
	  {"name":"auto","steps":[{"id":"sub","type":"subflow","config":{"workflow":"` + paying.String() + `"}}]},
	  {"name":"manual","steps":[{"id":"ok","type":"approval","config":{"role":"ops"}}]}]}}`

	for name, steps := range map[string]string{
		"the parent goes on": race + `,{"id":"later","type":"approval","needs":["race"],"config":{"role":"ops"}}`,
		"the parent ends":    race,
	} {
		ref := e.Start(t, e.Publish(t, wfDoc(steps, "")), map[string]any{})
		e.Drain(t)
		links, err := e.Store.Forks(ctx, ref)
		if err != nil || len(links.Subflows) != 1 {
			t.Fatalf("%s: the child run: %+v %v", name, links, err)
		}
		kid := runtime.RunRef{ID: links.Subflows[0], TenantID: e.Tenant}
		if e.Provider.Executions(kid.ID.String()+":pay") != 1 {
			t.Fatalf("%s: the child has not paid: %s", name, types(events(t, e, kid)))
		}
		if err := e.Store.DecideApproval(ctx, ref, "ok", "approved", "checker", "web"); err != nil {
			t.Fatal(err)
		}
		rt.WaitFor(t, 10*time.Second, name+": the child to end", func() bool {
			e.Drain(t)
			st := e.Status(t, kid)
			return st != "running" && st != "waiting"
		})
		h := events(t, e, ref)
		if count(h, history.StepCancelled, "sub") != 1 {
			t.Fatalf("%s: the subflow step was not withdrawn: %s", name, types(h))
		}
		if st := e.Status(t, kid); st != "cancelled" || e.Provider.Executions(kid.ID.String()+":refund") != 1 {
			t.Errorf("%s: child %s, refunds %d: %s", name, st, e.Provider.Executions(kid.ID.String()+":refund"), types(events(t, e, kid)))
		}
		if name == "the parent goes on" {
			if st := e.Status(t, ref); st == "completed" || st == "failed" || st == "cancelled" {
				t.Fatalf("%s: parent %s: %s", name, st, types(h))
			}
			if err := e.Store.DecideApproval(ctx, ref, "later", "approved", "checker", "web"); err != nil {
				t.Fatal(err)
			}
			e.Drain(t)
		}
		if st := e.Status(t, ref); st != "completed" {
			t.Errorf("%s: parent %s: %s", name, st, types(events(t, e, ref)))
		}
	}
}

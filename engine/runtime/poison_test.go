package runtime_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/container"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// noise is n bytes of text that does not shrink when stored.
func noise(n int) string {
	var b strings.Builder
	x := uint32(2463534242)
	for b.Len() < n {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b.WriteByte("abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ-_"[x&63])
	}
	return b.String()
}

// A code step that returns a NUL character, which the history's jsonb
// column refuses, is recorded all the same, with the character replaced.
// Before, the outcome could never be recorded and the task was claimed
// and run again for ever.
func TestOutputWithANulCharacterIsRecorded(t *testing.T) {
	e := rt.New(t)
	src, _ := json.Marshal(`export default () => { console.log("log\u0000line"); return { text: "a\u0000b", "k\u0000ey": 1 }; }`)
	wf := e.Publish(t, wfDoc(`{"id":"calc","type":"code","retry":{"max":0},"config":{"language":"javascript","source":`+string(src)+`}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	sb := e.Worker("w-sandbox")
	sb.Queue = "sandbox"
	if n, err := sb.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("the step's outcome was not recorded: %d tasks, %v", n, err)
	}
	h := events(t, e, ref)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("status %s: %s", st, types(h))
	}
	out, _ := completedOutput(t, h, "calc")
	if out["text"] != "a�b" || out["k�ey"] != float64(1) {
		t.Errorf("output %q", out)
	}
	var left int
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE run_id = $1`, ref.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("%d tasks left: %v", left, err)
	}
}

// A container step may return up to its limits.output_bytes, 1 MB at
// most (spec 4.9), and an http step a 256 KB body: the history keeps
// both. The column refused anything over 256 KB as stored, which a 256 KB
// body already is.
func TestOutputsWithinTheirStepsLimitsAreKept(t *testing.T) {
	e := rt.New(t)
	enableContainers(t, e, 10)
	big := noise(700 << 10)
	e.Containers = &container.Fake{Handle: func(context.Context, container.Spec) (container.Result, error) {
		out, _ := json.Marshal(map[string]any{"pdf": big})
		return container.Result{Output: out}, nil
	}}
	wf := e.Publish(t, wfDoc(containerStep(`,"class":"read","limits":{"output_bytes":1048576}`), ""))
	ref := e.Start(t, wf, map[string]any{"html": "x"})
	e.Drain(t)
	h := events(t, e, ref)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("container: status %s: %s %+v", st, types(h), containerFailure(h, "render"))
	}
	if out, _ := completedOutput(t, h, "render"); out["pdf"] != big {
		t.Errorf("container: the output was not kept whole")
	}

	body := "x" + noise(256<<10-1) // not a JSON value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
	defer srv.Close()
	if err := e.Store.AllowEgress(ctx, e.Tenant, "prod", "127.0.0.1", "admin"); err != nil {
		t.Fatal(err)
	}
	wf = e.Publish(t, wfDoc(`{"id":"get","type":"http","retry":{"max":0},"config":{"method":"GET","url":"`+srv.URL+`"}}`, ""))
	ref = e.Start(t, wf, map[string]any{})
	e.Drain(t)
	h = events(t, e, ref)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("http: status %s: %s %+v", st, types(h), containerFailure(h, "get"))
	}
	if out, _ := completedOutput(t, h, "get"); out["body"] != body {
		t.Errorf("http: the body was not kept whole")
	}
}

// An output the history cannot hold at all is not tried for ever: the
// step fails saying so, and a write, which has happened, parks for an
// operator instead.
func TestOutputTheHistoryCannotHoldFailsOrParks(t *testing.T) {
	e := rt.New(t)
	enableContainers(t, e, 10)
	huge := noise(5 << 20)
	fake := &container.Fake{Handle: func(context.Context, container.Spec) (container.Result, error) {
		out, _ := json.Marshal(map[string]any{"pdf": huge})
		return container.Result{Output: out}, nil
	}}
	e.Containers = fake
	for class, want := range map[string]string{"read": "failed", "idempotent_write": "needs_reconciliation"} {
		wf := e.Publish(t, wfDoc(containerStep(`,"class":"`+class+`"`), ""))
		ref := e.Start(t, wf, map[string]any{"html": "x"})
		before := len(fake.Specs())
		e.Drain(t)
		h := events(t, e, ref)
		f := containerFailure(h, "render")
		if st := e.Status(t, ref); st != want || f.Kind != "output_too_large" || f.MaybeApplied != (class != "read") {
			t.Errorf("%s: status %s, failure %+v: %s", class, st, f, types(h))
		}
		if n := len(fake.Specs()) - before; n != 1 {
			t.Errorf("%s: the program ran %d times", class, n)
		}
	}
}

// refuseOutcome makes the database refuse a step's StepCompleted, as a
// payload it cannot store would be refused.
func refuseOutcome(t *testing.T, e *rt.Env, step string) {
	t.Helper()
	for _, q := range []string{
		`CREATE FUNCTION test_refuse_outcome() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		   IF NEW.type = 'StepCompleted' AND NEW.step_id = '` + step + `' THEN
		     RAISE EXCEPTION 'this outcome cannot be stored' USING ERRCODE = '22P05';
		   END IF;
		   RETURN NEW; END $$`,
		`CREATE TRIGGER test_refuse_outcome BEFORE INSERT ON run_events FOR EACH ROW EXECUTE FUNCTION test_refuse_outcome()`,
	} {
		if _, err := e.DB.Admin.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
}

// A task whose outcome the database refuses every time is not delivered
// for ever. After a few tries the step is settled with a failure that can
// be recorded: a write that was sent parks for an operator, a read fails.
func TestTaskWhoseOutcomeCannotBeRecordedIsSettled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) }))
	defer srv.Close()
	for step, want := range map[string]string{
		`{"id":"poison","type":"connector","connector":"fakepay@1","action":"transfer","input":{"amount":1,"logical_id":"=run.id + ':n'"},"retry":{"max":0}}`: "needs_reconciliation",
		`{"id":"poison","type":"http","retry":{"max":0},"config":{"method":"GET","url":"` + srv.URL + `"}}`:                                                   "failed",
	} {
		e := rt.New(t)
		refuseOutcome(t, e, "poison")
		if err := e.Store.AllowEgress(ctx, e.Tenant, "prod", "127.0.0.1", "admin"); err != nil {
			t.Fatal(err)
		}
		ref := e.Start(t, e.Publish(t, wfDoc(step, "")), map[string]any{})
		w := e.Worker("w-poison")
		tries := 0
		for ; tries < 10 && e.Status(t, ref) == "running"; tries++ {
			// Each delivery fails to record; the lease then expires and the
			// scheduler hands the task back.
			_, _ = w.RunOnce(ctx)
			if _, err := e.DB.Admin.Exec(ctx, `UPDATE tasks SET lease_until = now() - interval '1 second' WHERE run_id = $1 AND lease_owner IS NOT NULL`, ref.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Scheduler().Tick(ctx); err != nil {
				t.Fatal(err)
			}
		}
		h := events(t, e, ref)
		f := containerFailure(h, "poison")
		write := want == "needs_reconciliation"
		if st := e.Status(t, ref); st != want || tries != 3 || f.Kind != "unrecordable" || f.MaybeApplied != write {
			t.Errorf("status %s (want %s) after %d deliveries, failure %+v: %s", st, want, tries, f, types(h))
		}
		if n := e.Provider.Executions(ref.ID.String() + ":n"); write && n != 1 {
			t.Errorf("the transfer took effect %d times", n)
		}
	}
}

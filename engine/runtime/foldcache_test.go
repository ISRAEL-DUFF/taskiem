package runtime_test

import (
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/decide"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/wd"
)

// A transaction that appends an event, decides and then fails to commit
// (here a deferred check; under a database crash, the lost COMMIT) must
// not leave the uncommitted event in the decide cache. If it did, the
// next decision would extend a history with an event that never happened
// from the seq after it, skipping the real event written at that seq, and
// write decisions replay cannot reproduce (TestDatabaseCrashSuite).
func TestFailedCommitDoesNotPoisonDecideCache(t *testing.T) {
	e := rt.New(t)
	doc := wfDoc(payOne+`,{"id":"done","type":"transform","needs":["pay"],"config":{"output":"=steps.pay.output.status"}}`, "")
	wf := e.Publish(t, doc)
	// The first completion recorded fails at COMMIT, after the worker has
	// appended it and decided on it in the same transaction (a sequence,
	// which a rollback does not undo, makes it fail once).
	for _, q := range []string{
		`CREATE SEQUENCE test_fail_once`,
		`CREATE FUNCTION test_fail_first_completion() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path = public AS $$
		 BEGIN
		   IF NEW.type = 'StepCompleted' AND nextval('test_fail_once') = 1 THEN RAISE EXCEPTION 'injected commit failure'; END IF;
		   RETURN NULL;
		 END $$`,
		`CREATE CONSTRAINT TRIGGER test_fail_first_completion AFTER INSERT ON run_events
		 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION test_fail_first_completion()`,
	} {
		if _, err := e.DB.Admin.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	ref := e.Start(t, wf, map[string]any{"amount": 100})
	w := e.Worker("w1")
	w.Lease = 300 * time.Millisecond
	_, _ = w.RunOnce(ctx) // the completion does not commit
	time.Sleep(400 * time.Millisecond)
	rt.WaitFor(t, 20*time.Second, "run to settle", func() bool {
		e.Drain(t)
		return e.Status(t, ref) != "running"
	})
	h := events(t, e, ref)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("run %s: %s", st, types(h))
	}
	def, err := wd.Load([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if err := decide.Verify(def, h); err != nil {
		t.Fatalf("replay: %v\n%s", err, types(h))
	}
}

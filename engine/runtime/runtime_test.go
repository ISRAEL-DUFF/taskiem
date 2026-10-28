package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/wd"
)

var ctx = context.Background()

func wfDoc(steps, settings string) string {
	if settings == "" {
		settings = "{}"
	}
	return `{"schema":"wd/v1","id":"wf_t","version":1,"name":"t","trigger":{"type":"manual"},"steps":[` + steps + `],"settings":` + settings + `}`
}

const payOne = `{"id":"pay","type":"connector","connector":"fakepay@1","action":"transfer",
  "input":{"amount":"=trigger.amount","logical_id":"=run.id + ':pay'"},"retry":{"max":5,"initial":"10ms","backoff":"fixed"}}`

func events(t *testing.T, e *rt.Env, ref runtime.RunRef) []history.Event {
	t.Helper()
	h, err := e.Store.RunHistory(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func count(h []history.Event, typ, step string) int {
	n := 0
	for _, ev := range h {
		if ev.Type == typ && (step == "" || ev.StepID == step) {
			n++
		}
	}
	return n
}

func types(h []history.Event) string {
	var b strings.Builder
	for _, ev := range h {
		b.WriteString(ev.Type + "(" + ev.StepID + ") ")
	}
	return b.String()
}

func TestPaymentRunEndToEnd(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(payOne+`,{"id":"done","type":"transform","needs":["pay"],"config":{"output":"=steps.pay.output.status"}}`, ""))
	ref := e.Start(t, wf, map[string]any{"amount": 150000})
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("status %s: %s", st, types(events(t, e, ref)))
	}
	h := events(t, e, ref)
	if count(h, history.EffectIntent, "pay") != 1 || e.Provider.Executions(ref.ID.String()+":pay") != 1 {
		t.Errorf("want one intent and one execution: %s", types(h))
	}
	var intent history.IntentPayload
	for _, ev := range h {
		if ev.Type == history.EffectIntent {
			_ = json.Unmarshal(ev.Payload, &intent)
		}
		if ev.Origin == "" {
			t.Errorf("event %d has no origin", ev.Seq)
		}
	}
	if !strings.HasPrefix(intent.Key, "tsk_") || len(intent.Key) != 36 {
		t.Errorf("idempotency key %q", intent.Key)
	}
}

func TestDuplicateTriggerReturnsSameRun(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(payOne, ""))
	req := runtime.StartRequest{TenantID: e.Tenant, WorkflowID: wf, Version: 1, Environment: "prod", Trigger: map[string]any{"amount": 1}, TriggerID: "hook", DedupKey: "evt_1"}
	a, created1, err := e.Store.StartRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	b, created2, err := e.Store.StartRun(ctx, req)
	if err != nil || !created1 || created2 || a.ID != b.ID {
		t.Errorf("dedup: %v %v %v %v %v", a.ID, b.ID, created1, created2, err)
	}
}

func TestRetryableErrorThenSuccess(t *testing.T) {
	e := rt.New(t)
	var calls atomic.Int32
	e.Provider.Faults = func(string) rt.Fault {
		if calls.Add(1) <= 2 {
			return rt.FailBefore
		}
		return rt.NoFault
	}
	wf := e.Publish(t, wfDoc(payOne, ""))
	ref := e.Start(t, wf, map[string]any{"amount": 1})
	rt.WaitFor(t, 10*time.Second, "completion", func() bool { e.Drain(t); return e.Status(t, ref) == "completed" })
	h := events(t, e, ref)
	if count(h, history.RetryScheduled, "pay") != 2 || e.Provider.Executions(ref.ID.String()+":pay") != 1 {
		t.Errorf("want 2 retries, 1 execution: %s", types(h))
	}
}

func TestUnknownOutcomeOnIdempotentWriteRetriesSameKey(t *testing.T) {
	e := rt.New(t)
	var calls atomic.Int32
	e.Provider.Faults = func(string) rt.Fault {
		if calls.Add(1) == 1 {
			return rt.FailAfter // executed, but the worker does not know
		}
		return rt.NoFault
	}
	wf := e.Publish(t, wfDoc(payOne, ""))
	ref := e.Start(t, wf, map[string]any{"amount": 1})
	rt.WaitFor(t, 10*time.Second, "completion", func() bool { e.Drain(t); return e.Status(t, ref) == "completed" })
	h := events(t, e, ref)
	keys := map[string]bool{}
	for _, ev := range h {
		if ev.Type == history.EffectIntent {
			var p history.IntentPayload
			_ = json.Unmarshal(ev.Payload, &p)
			keys[p.Key] = true
		}
	}
	if len(keys) != 1 || e.Provider.Executions(ref.ID.String()+":pay") != 1 {
		t.Errorf("want one key reused and one execution, got keys=%v executions=%d", keys, e.Provider.Executions(ref.ID.String()+":pay"))
	}
}

func TestReconcilableWriteReconcilesInsteadOfResending(t *testing.T) {
	e := rt.New(t)
	var calls atomic.Int32
	e.Provider.Faults = func(a string) rt.Fault {
		if a == "transfer" && calls.Add(1) == 1 {
			return rt.FailAfter
		}
		return rt.NoFault
	}
	wf := e.Publish(t, wfDoc(`{"id":"pay","type":"connector","connector":"fakepay@1","action":"bank_transfer",
	  "input":{"amount":1,"logical_id":"=run.id"},"retry":{"max":3,"initial":"10ms","backoff":"fixed"}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	rt.WaitFor(t, 10*time.Second, "completion", func() bool { e.Drain(t); return e.Status(t, ref) == "completed" })
	if n := e.Provider.Executions(ref.ID.String()); n != 1 {
		t.Errorf("provider without dedup executed %d times; reconcile should have prevented a resend", n)
	}
	h := events(t, e, ref)
	var p history.CompletedPayload
	for _, ev := range h {
		if ev.Type == history.StepCompleted && ev.StepID == "pay" {
			_ = json.Unmarshal(ev.Payload, &p)
		}
	}
	if !p.Reconciled {
		t.Errorf("completion should be marked reconciled: %s", types(h))
	}
}

func TestUnsafeWriteUnknownOutcomeParks(t *testing.T) {
	e := rt.New(t)
	e.Provider.Faults = func(string) rt.Fault { return rt.FailAfter }
	wf := e.Publish(t, wfDoc(`{"id":"sms","type":"connector","connector":"fakepay@1","action":"notify","input":{"logical_id":"=run.id"}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	if st := e.Status(t, ref); st != "needs_reconciliation" {
		t.Errorf("status %s", st)
	}
	if n := e.Provider.Executions(ref.ID.String()); n != 1 {
		t.Errorf("unsafe write executed %d times; must never be retried after an unknown outcome", n)
	}
}

func TestCrashAfterIntentIsRecoveredWithoutDuplicate(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(payOne, ""))
	ref := e.Start(t, wf, map[string]any{"amount": 1})
	crashing := e.Worker("crashy")
	crashing.Lease = 300 * time.Millisecond
	crashing.Hooks = &runtime.Hooks{AfterCall: func(string, int) error { return errors.New("kill -9") }}
	if _, err := crashing.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if e.Provider.Executions(ref.ID.String()+":pay") != 1 || e.Status(t, ref) != "running" {
		t.Fatal("setup: the effect should have happened without being recorded")
	}
	time.Sleep(400 * time.Millisecond) // lease expires
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("status %s: %s", st, types(events(t, e, ref)))
	}
	if n := e.Provider.Executions(ref.ID.String() + ":pay"); n != 1 {
		t.Errorf("executed %d times after crash recovery", n)
	}
}

func TestSignalsBufferedAndLive(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(`{"id":"settle","type":"signal","config":{"event":"fakepay@1:transfer","correlation":"=trigger.ref","timeout":"1h"}},
	  {"id":"out","type":"transform","needs":["settle"],"config":{"output":"=steps.settle.output.status"}}`, ""))

	// Live: the run waits first.
	live := e.Start(t, wf, map[string]any{"ref": "R1"})
	woke, err := e.Store.DeliverSignal(ctx, e.Tenant, "prod", "fakepay@1:transfer", "R1", map[string]any{"status": "success"})
	if err != nil || len(woke) != 1 {
		t.Fatalf("deliver: %v %v", woke, err)
	}
	if st := e.Status(t, live); st != "completed" {
		t.Errorf("live signal: %s %s", st, types(events(t, e, live)))
	}

	// Buffered: the signal arrives before the run exists.
	if woke, err := e.Store.DeliverSignal(ctx, e.Tenant, "prod", "fakepay@1:transfer", "R2", map[string]any{"status": "reversed"}); err != nil || len(woke) != 0 {
		t.Fatalf("buffer: %v %v", woke, err)
	}
	early := e.Start(t, wf, map[string]any{"ref": "R2"})
	if st := e.Status(t, early); st != "completed" {
		t.Errorf("buffered signal not consumed: %s %s", st, types(events(t, e, early)))
	}
}

func TestApprovalDecision(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(`{"id":"ok","type":"approval","config":{"role":"officer","timeout":"24h"}},`+
		strings.Replace(payOne, `"id":"pay",`, `"id":"pay","needs":["ok"],"when":"=steps.ok.output.decision == 'approved'",`, 1), ""))
	ref := e.Start(t, wf, map[string]any{"amount": 1})
	if err := e.Store.DecideApproval(ctx, ref, "nope", "approved", "u", "web"); err == nil {
		t.Error("deciding a non-existent approval should fail")
	}
	if err := e.Store.DecideApproval(ctx, ref, "ok", "approved", "checker", "web"); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.DecideApproval(ctx, ref, "ok", "rejected", "checker", "web"); err == nil {
		t.Error("an approval can be decided only once")
	}
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" || e.Provider.Executions(ref.ID.String()+":pay") != 1 {
		t.Errorf("status %s: %s", st, types(events(t, e, ref)))
	}
}

// A run shows waiting while all it has left is waiting on someone or
// something (an approval here), and running while a worker has its step
// (engine findings 5f).
func TestRunWaitingStatus(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(`{"id":"ok","type":"approval","config":{"role":"officer","timeout":"24h"}},`+
		strings.Replace(payOne, `"id":"pay",`, `"id":"pay","needs":["ok"],`, 1), ""))
	ref := e.Start(t, wf, map[string]any{"amount": 1})
	if st := e.Status(t, ref); st != "waiting" {
		t.Fatalf("waiting on the approval: %s", st)
	}
	if err := e.Store.DecideApproval(ctx, ref, "ok", "approved", "checker", "web"); err != nil {
		t.Fatal(err)
	}
	if st := e.Status(t, ref); st != "running" {
		t.Fatalf("with the payment queued for a worker: %s", st)
	}
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("at the end: %s %s", st, types(events(t, e, ref)))
	}

	// A waiting run still counts against the tenant's running-runs limit.
	slow := e.Publish(t, wfDoc(`{"id":"w","type":"wait","config":{"duration":"1h"}}`, ""))
	w := e.Start(t, slow, map[string]any{})
	if st := e.Status(t, w); st != "waiting" {
		t.Fatalf("on a wait step: %s", st)
	}
	l, err := e.Store.ViewLimits(ctx, e.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	if l.Usage.RunningRuns != 1 {
		t.Errorf("running runs counted: %d, want 1", l.Usage.RunningRuns)
	}
}

func TestWaitTimerAndRunTimeout(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(`{"id":"w","type":"wait","config":{"duration":"1s"}},{"id":"x","type":"transform","needs":["w"],"config":{"output":1}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	rt.WaitFor(t, 5*time.Second, "wait step", func() bool { e.Drain(t); return e.Status(t, ref) == "completed" })

	slow := e.Publish(t, wfDoc(`{"id":"w","type":"wait","config":{"duration":"1h"}}`, `{"timeout":"1s"}`))
	ref2 := e.Start(t, slow, map[string]any{})
	rt.WaitFor(t, 5*time.Second, "run timeout", func() bool { e.Drain(t); return e.Status(t, ref2) == "failed" })
}

func TestCancelStopsRun(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(`{"id":"w","type":"wait","config":{"duration":"1h"}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	if err := e.Store.CancelRun(ctx, ref, "u1", true); err != nil {
		t.Fatal(err)
	}
	if st := e.Status(t, ref); st != "cancelled" {
		t.Errorf("status %s", st)
	}
	var timers int
	_ = e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM timers WHERE run_id = $1 AND fired_at IS NULL`, ref.ID).Scan(&timers)
	if timers != 0 {
		t.Errorf("%d timers left after cancel", timers)
	}
}

// Cancelling a half-done disbursement reverses the payments already made,
// newest first, before the run ends; cancelling without reversing leaves
// them (decision 0054).
func TestCancelReversesCompletedSteps(t *testing.T) {
	e := rt.New(t)
	doc := wfDoc(`{"id":"pay1","type":"connector","connector":"fakepay@1","action":"transfer",
	   "input":{"amount":1,"logical_id":"=run.id + ':pay1'"},"compensate":{"action":"refund","input":{"logical_id":"=run.id + ':refund1'"}}},
	  {"id":"pay2","type":"connector","connector":"fakepay@1","action":"transfer","needs":["pay1"],
	   "input":{"amount":2,"logical_id":"=run.id + ':pay2'"},"compensate":{"action":"refund","input":{"logical_id":"=run.id + ':refund2'"}}},
	  {"id":"ok","type":"approval","needs":["pay2"],"config":{"role":"officer"}},
	  {"id":"pay3","type":"connector","connector":"fakepay@1","action":"transfer","needs":["ok"],"input":{"amount":3,"logical_id":"=run.id + ':pay3'"}}`, "")
	wf := e.Publish(t, doc)
	reversed, kept := e.Start(t, wf, map[string]any{}), e.Start(t, wf, map[string]any{})
	e.Drain(t)
	for _, ref := range []runtime.RunRef{reversed, kept} {
		if h := events(t, e, ref); count(h, history.ApprovalRequested, "ok") != 1 {
			t.Fatalf("setup: %s", types(h))
		}
	}

	if err := e.Store.CancelRun(ctx, reversed, "u1", true); err != nil {
		t.Fatal(err)
	}
	if st := e.Status(t, reversed); st != "running" {
		t.Errorf("while its compensations run: %s", st)
	}
	rt.WaitFor(t, 10*time.Second, "reversed cancel", func() bool { e.Drain(t); return e.Status(t, reversed) != "running" })
	h := events(t, e, reversed)
	if st := e.Status(t, reversed); st != "cancelled" || count(h, history.CompensationCompleted, "") != 1 {
		t.Fatalf("reversed: %s %s", st, types(h))
	}
	var order []string
	for _, ev := range h {
		if ev.Type == history.StepCompleted && strings.HasPrefix(ev.StepID, history.CompensationPrefix) {
			order = append(order, ev.StepID)
		}
	}
	if strings.Join(order, ",") != "compensate:pay2,compensate:pay1" {
		t.Errorf("compensation order %v", order)
	}
	if def, err := wd.Load([]byte(doc)); err != nil || decide.Verify(def, h) != nil {
		t.Errorf("replay: %v %v", err, decide.Verify(def, h))
	}
	if e.Provider.Executions(reversed.ID.String()+":refund1") != 1 || e.Provider.Executions(reversed.ID.String()+":refund2") != 1 || e.Provider.Executions(reversed.ID.String()+":pay3") != 0 {
		t.Errorf("provider: %v", e.Provider.Snapshot())
	}
	if count(h, history.StepCancelled, "ok") != 1 || h[len(h)-1].Type != history.RunCancelled || !strings.Contains(string(h[len(h)-1].Payload), `"reversed": ["pay2", "pay1"]`) {
		t.Errorf("end: %s %s", types(h), h[len(h)-1].Payload)
	}
	var open int
	_ = e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM approvals WHERE run_id = $1 AND status = 'open'`, reversed.ID).Scan(&open)
	if open != 0 {
		t.Errorf("%d approvals still open", open)
	}
	// A second cancel changes nothing.
	if err := e.Store.CancelRun(ctx, reversed, "u2", true); err != nil || count(events(t, e, reversed), history.CancelRequested, "") != 1 {
		t.Errorf("second cancel: %v", err)
	}

	if err := e.Store.CancelRun(ctx, kept, "u1", false); err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	if st, h := e.Status(t, kept), events(t, e, kept); st != "cancelled" || count(h, history.CompensationStarted, "") != 0 || e.Provider.Executions(kept.ID.String()+":refund1") != 0 {
		t.Errorf("without reversing: %s %s", st, types(h))
	}
}

func TestCompensationRunsOnFailure(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(`{"id":"pay","type":"connector","connector":"fakepay@1","action":"transfer",
	   "input":{"amount":1,"logical_id":"=run.id + ':pay'"},"compensate":{"action":"refund","input":{"logical_id":"=run.id + ':refund'"}}},
	  {"id":"boom","type":"http","needs":["pay"],"config":{"method":"GET","url":"http://127.0.0.1:1/unreachable"},"retry":{"max":0}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	rt.WaitFor(t, 10*time.Second, "failure", func() bool { e.Drain(t); return e.Status(t, ref) == "failed" })
	h := events(t, e, ref)
	if count(h, history.CompensationCompleted, "") != 1 || e.Provider.Executions(ref.ID.String()+":refund") != 1 {
		t.Errorf("compensation: %s", types(h))
	}
}

func TestHTTPStepThroughEgressAllowList(t *testing.T) {
	e := rt.New(t)
	var gotKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotAuth = r.Header.Get("Idempotency-Key"), r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"amount":1500}`))
	}))
	defer srv.Close()
	e.Secrets["api_token"] = "tok_123"
	if err := e.Store.SetVariable(ctx, e.Tenant, "prod", "partner_url", srv.URL); err != nil {
		t.Fatal(err)
	}
	wf := e.Publish(t, wfDoc(`{"id":"post","type":"http","retry":{"max":0},"config":{"method":"POST","url":"=env.partner_url + '/credit'",
	  "headers":{"Authorization":"='Bearer ' + secrets.api_token"},"body":{"n":1},"class":"idempotent_write","idempotency_header":"Idempotency-Key"}},
	  {"id":"out","type":"transform","needs":["post"],"config":{"output":"=steps.post.output.body.amount"}}`, ""))

	// Not on the allow-list: denied, fatal.
	denied, _, err := e.Store.StartRun(ctx, runtime.StartRequest{TenantID: e.Tenant, WorkflowID: wf, Version: 1, Environment: "prod", Trigger: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	if st := e.Status(t, denied); st != "failed" || gotKey != "" {
		t.Fatalf("unlisted host should be refused before any request: %s %q", st, gotKey)
	}

	if err := e.Store.AllowEgress(ctx, e.Tenant, "prod", "127.0.0.1", "admin"); err != nil {
		t.Fatal(err)
	}
	ok, _, err := e.Store.StartRun(ctx, runtime.StartRequest{TenantID: e.Tenant, WorkflowID: wf, Version: 1, Environment: "prod", Trigger: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	if st := e.Status(t, ok); st != "completed" {
		t.Fatalf("status %s: %s", st, types(events(t, e, ok)))
	}
	if !strings.HasPrefix(gotKey, "tsk_") || gotAuth != "Bearer tok_123" {
		t.Errorf("headers: key %q auth %q", gotKey, gotAuth)
	}
	h := events(t, e, ok)
	for _, ev := range h {
		if strings.Contains(string(ev.Payload), "tok_123") {
			t.Errorf("secret value leaked into %s", ev.Type)
		}
	}
}

func TestConnectorCredentialsFromConnections(t *testing.T) {
	e := rt.New(t)
	var seen string
	conn := e.Provider.Connector()
	conn.Manifest.ID = "authpay"
	conn.Manifest.Auth.Type = "api_key"
	conn.Manifest.Auth.Fields = []connector.AuthField{{Key: "secret_key"}}
	verify := conn.Actions["verify"]
	conn.Actions["verify"] = connector.ActionFunc(func(c context.Context, r connector.Request) (connector.Response, error) {
		seen = r.Credentials["secret_key"]
		return verify.Execute(c, r)
	})
	if err := e.Registry.Register(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Vault.CreateConnection(ctx, e.Tenant, "prod", "authpay", "main", "api_key", map[string]string{"secret_key": "sk_test_9"}, "admin"); err != nil {
		t.Fatal(err)
	}
	wf := e.Publish(t, wfDoc(`{"id":"pay","type":"connector","connector":"authpay@1","action":"transfer","input":{"amount":1,"logical_id":"x"}},
	  {"id":"check","type":"connector","connector":"authpay@1","action":"verify","needs":["pay"],"input":{"reference":"=steps.pay.output.reference"}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" || seen != "sk_test_9" {
		t.Errorf("status %s, credential %q: %s", st, seen, types(events(t, e, ref)))
	}
}

// A step output over 256 KB is not kept: a read fails saying so, and a
// write, which has already happened, parks for an operator.
func TestLargeOutputsAreRefused(t *testing.T) {
	e := rt.New(t)
	list := func(kb int) map[string]any {
		return map[string]any{"rows": strings.Repeat("x", kb<<10)}
	}
	conn := e.Provider.Connector()
	conn.Manifest.ID = "bigpay"
	conn.Actions["verify"] = connector.ActionFunc(func(_ context.Context, r connector.Request) (connector.Response, error) {
		kb, _ := r.Input["reference"].(string)
		n, _ := strconv.Atoi(kb)
		return connector.Response{Output: list(n)}, nil
	})
	conn.Actions["transfer"] = connector.ActionFunc(func(context.Context, connector.Request) (connector.Response, error) {
		return connector.Response{Output: list(300)}, nil
	})
	if err := e.Registry.Register(conn); err != nil {
		t.Fatal(err)
	}
	read := e.Publish(t, wfDoc(`{"id":"list","type":"connector","connector":"bigpay@1","action":"verify","retry":{"max":0},"input":{"reference":"=trigger.kb"}}`, ""))
	small := e.Start(t, read, map[string]any{"kb": "100"})
	big := e.Start(t, read, map[string]any{"kb": "300"})
	write := e.Start(t, e.Publish(t, wfDoc(`{"id":"pay","type":"connector","connector":"bigpay@1","action":"transfer","input":{"amount":1,"logical_id":"x"}}`, "")), map[string]any{})
	e.Drain(t)
	if st := e.Status(t, small); st != "completed" {
		t.Errorf("100 KB output: %s", st)
	}
	if st, msg := e.Status(t, big), failure(events(t, e, big), "list"); st != "failed" || !strings.Contains(msg, "step output is 301 KB, over the 256 KB limit") {
		t.Errorf("300 KB read: %s %q", st, msg)
	}
	if st, msg := e.Status(t, write), failure(events(t, e, write), "pay"); st != "needs_reconciliation" || !strings.Contains(msg, "record the step as completed") {
		t.Fatalf("300 KB write: %s %q", st, msg)
	}
	if err := e.Store.ResolveStep(ctx, write, "pay", runtime.ResolveCompleted, map[string]any{"reference": "r1"}, "provider shows the transfer", "ops"); err != nil {
		t.Fatal(err)
	}
	if st := e.Status(t, write); st != "completed" {
		t.Errorf("resolved write: %s", st)
	}
}

func TestCodeStepInRun(t *testing.T) {
	e := rt.New(t)
	fx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ngn_per_usd": 1500}`))
	}))
	defer fx.Close()
	e.Secrets["fx_key"] = "k-123"
	if err := e.Store.AllowEgress(ctx, e.Tenant, "prod", "127.0.0.1", "admin"); err != nil {
		t.Fatal(err)
	}
	src, _ := json.Marshal(`
	  type Line = { usd: number }
	  export default async function (input: { lines: Line[]; fx: string }, host: any) {
	    const rate = (await host.fetch(input.fx)).json().ngn_per_usd;
	    console.log("rate", rate, "key", host.secret("fx_key").length);
	    return { total_kobo: input.lines.reduce((s, l) => s + l.usd * rate * 100, 0), at: host.now() };
	  }`)
	wf := e.Publish(t, wfDoc(`{"id":"calc","type":"code","input":{"lines":"=trigger.lines","fx":"=trigger.fx"},
	  "config":{"language":"typescript","secrets":["fx_key"],"source":`+string(src)+`}},
	  {"id":"out","type":"transform","needs":["calc"],"config":{"output":"=steps.calc.output.total_kobo"}}`, ""))
	ref := e.Start(t, wf, map[string]any{"lines": []any{map[string]any{"usd": 2}, map[string]any{"usd": 3}}, "fx": fx.URL})
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		for _, ev := range events(t, e, ref) {
			t.Logf("%s(%s) %s", ev.Type, ev.StepID, ev.Payload)
		}
		t.Fatalf("status %s", st)
	}
	h := events(t, e, ref)
	var calc, out history.CompletedPayload
	for _, ev := range h {
		if ev.Type == history.StepCompleted && ev.StepID == "calc" {
			_ = json.Unmarshal(ev.Payload, &calc)
		}
		if ev.Type == history.StepCompleted && ev.StepID == "out" {
			_ = json.Unmarshal(ev.Payload, &out)
		}
	}
	if out.Output != float64(750000) || len(calc.Logs) != 1 || calc.Logs[0] != "rate 1500 key 5" {
		t.Errorf("out %v logs %q", out.Output, calc.Logs)
	}

	bad := e.Publish(t, wfDoc(`{"id":"calc","type":"code","config":{"language":"javascript","source":"export default () => { throw new Error('no rate') }"}}`, ""))
	ref2 := e.Start(t, bad, map[string]any{})
	e.Drain(t)
	if st := e.Status(t, ref2); st != "failed" {
		t.Errorf("throwing script: %s", st)
	}
}

func TestPythonCodeStepInRun(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles CPython")
	}
	e := rt.New(t)
	fx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ngn_per_usd": "1500.50"}`))
	}))
	defer fx.Close()
	e.Secrets["fx_key"] = "k-123"
	if err := e.Store.AllowEgress(ctx, e.Tenant, "prod", "127.0.0.1", "admin"); err != nil {
		t.Fatal(err)
	}
	src, _ := json.Marshal(`from decimal import Decimal

def main(input, host):
    rate = Decimal(host.fetch(input["fx"]).json()["ngn_per_usd"])
    print("rate", rate, "key", len(host.secret("fx_key")))
    return {"total_kobo": int(sum(Decimal(l["usd"]) for l in input["lines"]) * rate * 100)}
`)
	wf := e.Publish(t, wfDoc(`{"id":"calc","type":"code","input":{"lines":"=trigger.lines","fx":"=trigger.fx"},
	  "config":{"language":"python","secrets":["fx_key"],"source":`+string(src)+`}},
	  {"id":"out","type":"transform","needs":["calc"],"config":{"output":"=steps.calc.output.total_kobo"}}`, ""))
	ref := e.Start(t, wf, map[string]any{"lines": []any{map[string]any{"usd": 2}, map[string]any{"usd": 3}}, "fx": fx.URL})
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		for _, ev := range events(t, e, ref) {
			t.Logf("%s(%s) %s", ev.Type, ev.StepID, ev.Payload)
		}
		t.Fatalf("status %s", st)
	}
	var calc, out history.CompletedPayload
	for _, ev := range events(t, e, ref) {
		if ev.Type == history.StepCompleted && ev.StepID == "calc" {
			_ = json.Unmarshal(ev.Payload, &calc)
		}
		if ev.Type == history.StepCompleted && ev.StepID == "out" {
			_ = json.Unmarshal(ev.Payload, &out)
		}
	}
	if out.Output != float64(750250) || len(calc.Logs) != 1 || calc.Logs[0] != "rate 1500.50 key 5" {
		t.Errorf("out %v logs %q", out.Output, calc.Logs)
	}

	// Writes are refused before they leave the sandbox.
	post, _ := json.Marshal(`def main(input, host):
    host.fetch(input["fx"], method="POST", body={"amount": 1})
`)
	wf = e.Publish(t, wfDoc(`{"id":"calc","type":"code","retry":{"max":0},"input":{"fx":"=trigger.fx"},"config":{"language":"python","source":`+string(post)+`}}`, ""))
	ref = e.Start(t, wf, map[string]any{"fx": fx.URL})
	e.Drain(t)
	if st, msg := e.Status(t, ref), failure(events(t, e, ref), "calc"); st != "failed" || !strings.Contains(msg, "POST: host.fetch allows only GET and HEAD") {
		t.Errorf("python POST: status %s: %q", st, msg)
	}
}

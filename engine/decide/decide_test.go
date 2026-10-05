package decide

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/history"
)

func TestLinearConnectorFlow(t *testing.T) {
	s := newSim(t, wdDoc(`
	  {"id":"verify","type":"connector","connector":"smileid@2","action":"verify_bvn","input":{"bvn":"=trigger.body.bvn"}},
	  {"id":"pay","type":"connector","connector":"paystack@1","action":"transfer","needs":["verify"],
	   "input":{"amount":"=trigger.body.amount","recipient":"=steps.verify.output.recipient","auth":"=secrets.key"},
	   "effect":{"idempotency_seed":"=trigger.body.loan_id"}}`, ""),
		map[string]any{"body": map[string]any{"bvn": "222", "amount": 5000000, "loan_id": "L9"}})

	if got := s.pendingTasks(); !reflect.DeepEqual(got, []string{"verify"}) {
		t.Fatalf("pending = %v", got)
	}
	if in := s.payload(history.StepScheduled, "verify")["input"]; !reflect.DeepEqual(in, map[string]any{"bvn": "222"}) {
		t.Errorf("verify input = %v", in)
	}
	s.complete("verify", map[string]any{"recipient": "RCP_1"})
	p := s.payload(history.StepScheduled, "pay")
	want := map[string]any{"amount": int64(5000000), "recipient": "RCP_1", "auth": "=secrets.key"}
	if !reflect.DeepEqual(p["input"], want) {
		t.Errorf("pay input = %v, want %v (secret left unresolved)", p["input"], want)
	}
	if p["seed"] != "L9" || p["key_step"] != "pay" {
		t.Errorf("seed/key_step = %v/%v", p["seed"], p["key_step"])
	}
	s.complete("pay", map[string]any{"status": "success"})
	if s.last().Type != history.RunCompleted {
		t.Errorf("run not completed: %s", s.types())
	}
	if out := s.decide(); len(out) != 0 {
		t.Errorf("decide after completion emitted %v", out)
	}
}

func TestWhenFalseSkipsDependents(t *testing.T) {
	s := newSim(t, wdDoc(`
	  {"id":"approve","type":"approval","config":{"role":"credit_officer","timeout":"24h"}},
	  {"id":"pay","type":"connector","connector":"paystack@1","action":"transfer","needs":["approve"],"when":"=steps.approve.output.decision == 'approved'"},
	  {"id":"notify","type":"transform","needs":["pay"],"config":{"output":"sent"}}`, ""), map[string]any{})
	if !s.has(history.ApprovalRequested, "approve") {
		t.Fatalf("no approval requested: %s", s.types())
	}
	s.external(history.ApprovalDecided, "approve", 0, history.ApprovalDecidedPayload{Decision: "rejected", DecidedBy: "u2"}, history.OriginAPI)
	if !s.has(history.StepSkipped, "pay") || !s.has(history.StepSkipped, "notify") {
		t.Errorf("pay and notify should be skipped: %s", s.types())
	}
	if s.last().Type != history.RunCompleted {
		t.Errorf("run should complete: %s", s.types())
	}
}

func TestRetriesWithDeterministicBackoffThenFail(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"pay","type":"connector","connector":"paystack@1","action":"transfer",
	  "retry":{"max":2,"backoff":"exponential","initial":"2s"}}`, ""), map[string]any{})
	s.failStep("pay", "retryable", "retry")
	if s.lastAttempt("pay") != 2 || !s.has(history.RetryScheduled, "pay") {
		t.Fatalf("no retry scheduled: %s", s.types())
	}
	at1, _ := history.ParseTime(s.payload(history.RetryScheduled, "pay")["at"].(string))
	if d := at1.Sub(s.now); d < 2*time.Second || d > 2400*time.Millisecond {
		t.Errorf("first backoff %s outside [2s, 2.4s]", d)
	}
	s.failStep("pay", "unknown_outcome", "retry")
	at2, _ := history.ParseTime(s.payload(history.RetryScheduled, "pay")["at"].(string))
	if d := at2.Sub(s.now); d < 4*time.Second || d > 4800*time.Millisecond {
		t.Errorf("second backoff %s outside [4s, 4.8s]", d)
	}
	s.failStep("pay", "retryable", "retry")
	if s.lastAttempt("pay") != 3 || s.last().Type != history.RunFailed {
		t.Errorf("want run failed after 3 attempts: %s", s.types())
	}
}

func TestParkedStepHoldsRun(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"sms","type":"connector","connector":"termii@1","action":"send_sms"},
	  {"id":"other","type":"transform","config":{"output":1}}`, ""), map[string]any{})
	s.failStep("sms", "unknown_outcome", "park")
	if s.last().Type == history.RunCompleted || s.last().Type == history.RunFailed {
		t.Errorf("parked run ended: %s", s.types())
	}
	if !s.has(history.StepCompleted, "other") {
		t.Errorf("independent step should still run: %s", s.types())
	}
}

func TestOnErrorHandlesFailure(t *testing.T) {
	s := newSim(t, wdDoc(`
	  {"id":"pay","type":"connector","connector":"paystack@1","action":"transfer","retry":{"max":0},
	   "on_error":{"steps":[{"id":"alert","type":"transform","config":{"output":"=steps.pay.error.kind"}}]}},
	  {"id":"after","type":"transform","needs":["pay"],"config":{"output":1}},
	  {"id":"independent","type":"transform","config":{"output":2}}`, ""), map[string]any{})
	s.failStep("pay", "fatal", "fail")
	if got := s.payload(history.StepCompleted, "alert")["output"]; got != "fatal" {
		t.Errorf("on_error saw error kind %v", got)
	}
	if !s.has(history.StepSkipped, "after") {
		t.Errorf("dependent of a handled failure should be skipped: %s", s.types())
	}
	if s.last().Type != history.RunCompleted {
		t.Errorf("handled failure should not fail the run: %s", s.types())
	}
}

func TestBranch(t *testing.T) {
	doc := wdDoc(`
	  {"id":"verify","type":"connector","connector":"paystack@1","action":"verify_charge"},
	  {"id":"route","type":"branch","needs":["verify"],"config":{"paths":[
	    {"name":"paid","when":"=steps.verify.output.status == 'success'","steps":[
	      {"id":"credit","type":"transform","config":{"output":"credited"}}]},
	    {"name":"failed","when":"=steps.verify.output.status == 'failed'","steps":[
	      {"id":"mark_failed","type":"transform","config":{"output":"failed"}}]}],
	   "default":{"steps":[{"id":"wait_more","type":"transform","config":{"output":"later"}}]}}},
	  {"id":"done","type":"transform","needs":["route"],"config":{"output":"=steps.route.output.path"}}`, "")
	for status, path := range map[string]string{"success": "paid", "failed": "failed", "pending": "default"} {
		s := newSim(t, doc, map[string]any{})
		s.complete("verify", map[string]any{"status": status})
		if got := s.payload(history.StepCompleted, "done")["output"]; got != path {
			t.Errorf("status %s: path %v, want %s (%s)", status, got, path, s.types())
		}
		if s.has(history.StepCompleted, "credit") != (path == "paid") {
			t.Errorf("status %s: credit ran = %v", status, path != "paid")
		}
	}
}

func TestForeachConcurrencyAndOutputs(t *testing.T) {
	s := newSim(t, wdDoc(`
	  {"id":"pay_all","type":"foreach","config":{"items":"=trigger.body.employees","max_concurrency":2,"steps":[
	    {"id":"pay","type":"connector","connector":"paystack@1","action":"transfer","input":{"amount":"=item.amount","n":"=index"},
	     "effect":{"idempotency_seed":"='P1:' + item.id"}},
	    {"id":"note","type":"transform","needs":["pay"],"config":{"output":"=item.id + ':' + steps.pay.output.ref"}}]}},
	  {"id":"report","type":"transform","needs":["pay_all"],"config":{"output":"=size(steps.pay_all.output)"}}`, ""),
		map[string]any{"body": map[string]any{"employees": []any{
			map[string]any{"id": "e1", "amount": 100}, map[string]any{"id": "e2", "amount": 200}, map[string]any{"id": "e3", "amount": 300},
		}}})
	if got := s.pendingTasks(); !reflect.DeepEqual(got, []string{"pay_all[0].pay", "pay_all[1].pay"}) {
		t.Fatalf("max_concurrency 2: pending %v", got)
	}
	p := s.payload(history.StepScheduled, "pay_all[1].pay")
	if !reflect.DeepEqual(p["input"], map[string]any{"amount": int64(200), "n": int64(1)}) || p["seed"] != "P1:e2" {
		t.Errorf("iteration 1 payload %v", p)
	}
	s.complete("pay_all[1].pay", map[string]any{"ref": "r2"})
	if got := s.pendingTasks(); !reflect.DeepEqual(got, []string{"pay_all[0].pay", "pay_all[2].pay"}) {
		t.Fatalf("after one finished: pending %v", got)
	}
	s.complete("pay_all[0].pay", map[string]any{"ref": "r1"})
	s.complete("pay_all[2].pay", map[string]any{"ref": "r3"})
	out := s.payload(history.StepCompleted, "pay_all")["output"].([]any)
	if len(out) != 3 || out[2].(map[string]any)["note"] != "e3:r3" {
		t.Errorf("foreach output %v", out)
	}
	if s.payload(history.StepCompleted, "report")["output"] != int64(3) || s.last().Type != history.RunCompleted {
		t.Errorf("report/run: %s", s.types())
	}
}

func TestForeachFailureStopsNewIterations(t *testing.T) {
	s := newSim(t, wdDoc(`
	  {"id":"f","type":"foreach","config":{"items":"=[1,2,3,4]","max_concurrency":2,"steps":[
	    {"id":"x","type":"connector","connector":"paystack@1","action":"transfer","retry":{"max":0}}]}}`, ""), map[string]any{})
	s.failStep("f[0].x", "fatal", "fail")
	if got := s.pendingTasks(); !reflect.DeepEqual(got, []string{"f[1].x"}) {
		t.Fatalf("no new iteration after failure: pending %v", got)
	}
	s.complete("f[1].x", nil)
	if !s.has(history.StepFailed, "f") || s.last().Type != history.RunFailed {
		t.Errorf("foreach and run should fail: %s", s.types())
	}
}

func TestWaitSignalAndTimeouts(t *testing.T) {
	s := newSim(t, wdDoc(`
	  {"id":"pause","type":"wait","config":{"duration":"1h"}},
	  {"id":"settle","type":"signal","needs":["pause"],"config":{"event":"paystack@1:transfer_event","correlation":"=trigger.body.ref","timeout":"24h"}},
	  {"id":"out","type":"transform","needs":["settle"],"config":{"output":"=steps.settle.output.status"}}`, ""),
		map[string]any{"body": map[string]any{"ref": "tsk_abc"}})
	if fire := s.payload(history.StepScheduled, "pause")["fire_at"]; fire != "2026-10-05T10:00:00Z" {
		t.Errorf("fire_at %v", fire)
	}
	s.fire("pause", "wait")
	p := s.payload(history.StepScheduled, "settle")
	if p["kind"] != "signal" || p["correlation"] != "tsk_abc" || p["timeout_at"] == nil {
		t.Errorf("signal payload %v", p)
	}
	s.external(history.SignalReceived, "settle", 0, history.SignalPayload{Event: "transfer.success", Payload: map[string]any{"status": "success"}}, history.OriginSignal)
	if s.payload(history.StepCompleted, "out")["output"] != "success" || s.last().Type != history.RunCompleted {
		t.Errorf("signal flow: %s", s.types())
	}

	s2 := newSim(t, wdDoc(`{"id":"settle","type":"signal","config":{"event":"e","correlation":"='x'","timeout":"1h"}}`, ""), map[string]any{})
	s2.fire("settle", "signal_timeout")
	if s2.last().Type != history.RunFailed {
		t.Errorf("signal timeout should fail the run: %s", s2.types())
	}
}

func TestApprovalTimeoutEscalatesThenRejects(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"ok","type":"approval","config":{"role":"officer","timeout":"1h","on_timeout":"escalate:head"}}`, ""), map[string]any{})
	s.fire("ok", "approval_timeout")
	if p := s.payload(history.ApprovalRequested, "ok"); p["role"] != "head" || p["escalated"] != true {
		t.Fatalf("escalation %v", p)
	}
	s.fire("ok", "approval_timeout")
	if got := s.payload(history.StepCompleted, "ok")["output"].(map[string]any)["decision"]; got != "rejected" {
		t.Errorf("decision after second timeout %v", got)
	}
}

func TestCompensationInReverseOrder(t *testing.T) {
	s := newSim(t, wdDoc(`
	  {"id":"debit","type":"connector","connector":"bank@1","action":"debit","compensate":{"action":"refund","input":{"ref":"=steps.debit.output.ref"}}},
	  {"id":"credit","type":"connector","connector":"bank@1","action":"credit","needs":["debit"],"compensate":{"action":"reverse","input":{"ref":"=steps.credit.output.ref"}}},
	  {"id":"notify","type":"connector","connector":"termii@1","action":"send_sms","needs":["credit"],"retry":{"max":0}}`, ""), map[string]any{})
	s.complete("debit", map[string]any{"ref": "D1"})
	s.complete("credit", map[string]any{"ref": "C1"})
	s.failStep("notify", "fatal", "fail")
	if !s.has(history.CompensationStarted, "") {
		t.Fatalf("no compensation: %s", s.types())
	}
	if got := s.pendingTasks(); !reflect.DeepEqual(got, []string{"compensate:credit"}) {
		t.Fatalf("credit must be compensated first: %v", got)
	}
	if p := s.payload(history.StepScheduled, "compensate:credit"); p["action"] != "reverse" || !reflect.DeepEqual(p["input"], map[string]any{"ref": "C1"}) {
		t.Errorf("compensation payload %v", p)
	}
	s.complete("compensate:credit", nil)
	if got := s.pendingTasks(); !reflect.DeepEqual(got, []string{"compensate:debit"}) {
		t.Fatalf("then debit: %v", got)
	}
	s.complete("compensate:debit", nil)
	if !s.has(history.CompensationCompleted, "") || s.last().Type != history.RunFailed {
		t.Errorf("want compensation completed then run failed: %s", s.types())
	}
	if msg := s.payload(history.RunFailed, "")["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "notify") {
		t.Errorf("run failure should name notify: %s", msg)
	}
}

func TestRunWaitsForInFlightBeforeFailing(t *testing.T) {
	s := newSim(t, wdDoc(`
	  {"id":"a","type":"connector","connector":"x@1","action":"y","retry":{"max":0}},
	  {"id":"b","type":"connector","connector":"x@1","action":"y"}`, ""), map[string]any{})
	s.failStep("a", "fatal", "fail")
	if s.last().Type == history.RunFailed {
		t.Fatal("run failed while b is in flight")
	}
	s.complete("b", nil)
	if s.last().Type != history.RunFailed {
		t.Errorf("run should fail once b settles: %s", s.types())
	}
}

func TestRunTimeout(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"w","type":"wait","config":{"duration":"10d"}}`, `{"timeout":"1h"}`), map[string]any{})
	s.external(history.TimerFired, "", 0, history.TimerPayload{Kind: "run_timeout"}, history.OriginScheduler)
	if s.last().Type != history.RunFailed {
		t.Errorf("run timeout: %s", s.types())
	}
}

func TestExpressionErrorFailsStep(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"x","type":"transform","config":{"output":"=trigger.body.missing"}}`, ""), map[string]any{"body": map[string]any{}})
	if !s.has(history.StepFailed, "x") || s.last().Type != history.RunFailed {
		t.Errorf("expression error: %s", s.types())
	}
	if k := s.payload(history.StepFailed, "x")["error"].(map[string]any)["kind"]; k != "expression" {
		t.Errorf("kind %v", k)
	}
}

func TestUnsupportedStepType(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"p","type":"parallel","config":{"branches":[
	  {"name":"a","steps":[{"id":"a1","type":"transform","config":{"output":1}}]},
	  {"name":"b","steps":[{"id":"b1","type":"transform","config":{"output":2}}]}]}}`, ""), map[string]any{})
	if k := s.payload(history.StepFailed, "p")["error"].(map[string]any)["kind"]; k != "unsupported" {
		t.Errorf("parallel should be unsupported in Phase 1: %v", k)
	}
}

func TestCancelledRunDecidesNothing(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"a","type":"connector","connector":"x@1","action":"y"}`, ""), map[string]any{})
	if out := s.external(history.RunCancelled, "", 0, map[string]any{"by": "u1"}, history.OriginAPI); len(out) != 0 {
		t.Errorf("decided after cancel: %v", out)
	}
}

func TestBackoffIsDeterministicAndBounded(t *testing.T) {
	r := retry{max: 10, exponential: true, initial: time.Second, maxDelay: 10 * time.Second}
	for n := 1; n <= 8; n++ {
		a, b := backoff(r, n, "run", "pay"), backoff(r, n, "run", "pay")
		if a != b {
			t.Fatalf("attempt %d: %s vs %s", n, a, b)
		}
		if a > 12*time.Second {
			t.Errorf("attempt %d: %s exceeds max_delay plus jitter", n, a)
		}
	}
	if backoff(r, 1, "run-a", "pay") == backoff(r, 1, "run-b", "pay") && backoff(r, 2, "run-a", "pay") == backoff(r, 2, "run-b", "pay") {
		t.Error("jitter does not vary between runs")
	}
}

func TestPayrollaDogfoodEndToEnd(t *testing.T) {
	doc, err := os.ReadFile("../../flows/dogfood/payrolla-salary-disbursement.wd.json")
	if err != nil {
		t.Fatal(err)
	}
	s := newSim(t, string(doc), map[string]any{"body": map[string]any{
		"payroll_id": "PR-10", "period": "Oct 2026", "funding_wallet_id": "w_company", "total_kobo": 300000,
		"employees": []any{
			map[string]any{"employee_id": "e1", "net_pay_kobo": 100000, "wallet_id": "w_e1"},
			map[string]any{"employee_id": "e2", "net_pay_kobo": 200000, "bank_code": "044", "account_number": "0690000031", "account_name": "Ada Obi"},
			map[string]any{"employee_id": "e3", "net_pay_kobo": 900000, "wallet_id": "w_e3"},
		},
	}})
	if !s.has(history.StepCompleted, "summarise") {
		t.Fatalf("summarise: %s", s.types())
	}
	if in := s.payload(history.StepScheduled, "check_balance")["input"].(map[string]any); in["wallet_id"] != "w_company" {
		t.Fatalf("balance input %v", in)
	}
	s.complete("check_balance", map[string]any{"balances": []any{map[string]any{"currency": "NGN", "available": 9000000, "total": 9100000}}})
	subj := s.payload(history.ApprovalRequested, "approve")["subject"].(map[string]any)
	if subj["available_kobo"] != int64(9000000) || subj["employees"] != int64(3) || subj["paid_to_bank"] != int64(1) {
		t.Fatalf("approval subject %v", subj)
	}
	s.external(history.ApprovalDecided, "approve", 0, history.ApprovalDecidedPayload{Decision: "approved", DecidedBy: "checker"}, history.OriginAPI)

	// e1 has a wallet: an instant transfer. e2 does not: a bank payout.
	w := s.payload(history.StepScheduled, "pay_all[0].to_wallet")
	if w["seed"] != "PR-10:e1" || w["input"].(map[string]any)["to_wallet_id"] != "w_e1" {
		t.Fatalf("transfer payload %v", w)
	}
	if !s.has(history.StepSkipped, "pay_all[0].to_bank") || !s.has(history.StepSkipped, "pay_all[1].to_wallet") {
		t.Fatalf("each employee takes one rail: %s", s.types())
	}
	b := s.payload(history.StepScheduled, "pay_all[1].to_bank")
	if b["seed"] != "PR-10:e2" || b["input"].(map[string]any)["bank_code"] != "044" {
		t.Fatalf("payout payload %v", b)
	}
	s.complete("pay_all[0].to_wallet", map[string]any{"status": "completed", "txn_id": "txn_1"})
	// iswallet refuses e3's transfer; the payroll carries on.
	s.external(history.StepFailed, "pay_all[2].to_wallet", 1, history.FailedPayload{Error: history.Error{Kind: "fatal", Message: "iswallet 422 EXCEEDS_SINGLE_TXN_LIMIT", Next: "fail"}}, history.OriginWorker)
	if !s.has(history.StepCompleted, "pay_all[2].wallet_failed") {
		t.Fatalf("refused payment not recorded: %s", s.types())
	}
	if !s.has(history.StepSkipped, "pay_all[0].settle") {
		t.Errorf("a wallet transfer has nothing to wait for: %s", s.types())
	}
	s.complete("pay_all[1].to_bank", map[string]any{"status": "pending", "outflow_id": "out_2"})
	if c := s.payload(history.StepScheduled, "pay_all[1].settle")["correlation"]; c != "out_2" {
		t.Errorf("settle correlation %v", c)
	}
	s.external(history.SignalReceived, "pay_all[1].settle", 0, history.SignalPayload{Event: "iswallet@1:outflow_event", Payload: map[string]any{"event": "wallet.outflow.confirmed"}}, history.OriginSignal)
	if s.has(history.StepFailed, "report") {
		t.Fatalf("report failed: %v", s.payload(history.StepFailed, "report"))
	}
	rp := s.payload(history.StepScheduled, "report")
	if in := rp["input"].(map[string]any); in["headers"].(map[string]any)["Authorization"] != "='Bearer ' + secrets.payrolla_api_token" ||
		in["url"] != "https://payrolla.example/payrolls/PR-10/disbursement-report" || len(in["body"].(map[string]any)["results"].([]any)) != 3 {
		t.Errorf("report input %v", in)
	}
	body := rp["input"].(map[string]any)["body"].(map[string]any)
	if body["paid"] != int64(2) || body["failed"] != int64(1) {
		t.Errorf("report counts %v", body)
	}
	failure := body["results"].([]any)[2].(map[string]any)["wallet_failed"].(map[string]any)
	if failure["employee_id"] != "e3" || !strings.Contains(failure["error"].(map[string]any)["message"].(string), "EXCEEDS_SINGLE_TXN_LIMIT") {
		t.Errorf("failure record %v", failure)
	}
	s.complete("report", map[string]any{"status": 200})
	if s.last().Type != history.RunCompleted {
		t.Errorf("payroll run should complete: %s", s.types())
	}
}

func TestWriteThatMayHaveAppliedParksWhenRetriesRunOut(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"pay","type":"connector","connector":"iswallet@1","action":"payout","retry":{"max":1,"initial":"1s"}}`, ""), map[string]any{})
	maybe := history.FailedPayload{Error: history.Error{Kind: "unknown_outcome", Message: "500", Next: "retry", MaybeApplied: true}}
	s.external(history.StepFailed, "pay", 1, maybe, history.OriginWorker)
	if s.lastAttempt("pay") != 2 {
		t.Fatalf("no retry: %s", s.types())
	}
	// The retry fails harmlessly, but the first attempt may still have paid.
	s.failStep("pay", "retryable", "retry")
	last := s.last()
	if last.Type != history.StepFailed || s.payload(history.StepFailed, "pay")["error"].(map[string]any)["next"] != "park" {
		t.Fatalf("want the step parked, got %s", s.types())
	}
	if out := s.decide(); len(out) != 0 {
		t.Errorf("parked step decided again: %v", out)
	}
}

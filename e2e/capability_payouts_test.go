package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// payoutsWD pays a batch another system posts, after approval, by any
// rail that can pay bank accounts, and follows each payout to its end.
const payoutsWD = `{"schema":"wd/v1","id":"wf_batchPayouts","version":1,"name":"Pay a batch","trigger":{"type":"webhook","config":{"path":"/payouts/approved","auth":"hmac","dedup":"=trigger.body.batch_id"}},
 "steps":[
  {"id":"approve","type":"approval","config":{"role":"approver","count":1,"timeout":"24h","subject":{"batch":"=trigger.body.batch_id","people":"=size(trigger.body.people)"}}},
  {"id":"pay_all","type":"foreach","needs":["approve"],"when":"=steps.approve.output.decision == 'approved'","config":{"items":"=trigger.body.people","max_concurrency":5,"steps":[
    {"id":"pay","type":"capability","uses":"pay_bank","connection":"main",
     "input":{"amount_kobo":"=item.kobo","bank_code":"=item.bank_code","account_number":"=item.account_number","account_name":"=item.account_name","narration":"='Batch ' + trigger.body.batch_id","source":"=trigger.body.wallet"},
     "effect":{"idempotency_seed":"=trigger.body.batch_id + ':' + item.id"},"retry":{"max":5,"backoff":"fixed","initial":"10ms"},
     "on_error":{"steps":[{"id":"pay_failed","type":"transform","config":{"output":{"reason":"=steps.pay.error.message"}}}]}},
    {"id":"settle","type":"capability","uses":"await_settlement","needs":["pay"],"timeout":"72h","when":"=steps.pay.output.status == 'pending'","input":{"payout_id":"=steps.pay.output.payout_id"}},
    {"id":"back","type":"capability","uses":"await_reversal","needs":["settle"],"timeout":"35d","when":"=steps.settle.output.status == 'settled'","input":{"payout_id":"=steps.pay.output.payout_id"}}]}}]}`

// Capability payouts on the real stack with iswallet (decision 0044): a
// signed webhook starts the run once however often it is delivered;
// nothing moves before approval, and not by the workflow's author; each
// bank payout is made once, its lost answer replayed under the same key;
// iswallet's signed events end each wait through ingest (a forged one is
// refused, one for a payout the run did not make wakes nothing); a
// reversal is noticed without paying again; personal data stays sealed,
// and the run replays over its pinned expansion.
func TestCapabilityPayoutsOnIswalletEndToEnd(t *testing.T) {
	isw := newFakeIswallet()
	isw.balances["w_company"] = 900_000_000
	isw.limit = 40_000_000 // ₦400,000 per transaction: Dayo's ₦450,000 is refused
	iswallet := httptest.NewServer(isw)
	defer iswallet.Close()

	s := newStack(t, builtin.Options{IswalletURL: iswallet.URL})
	s.CallTimeout = 5 * time.Second // a whole batch, under the suite's load
	anon := ""
	tenant := s.must(t, 201, anon, "POST", "/v1/signup", map[string]any{"tenant": "Ada Logistics", "email": "ops@ada.test", "password": "correct horse battery"})["tenant_id"].(string)
	owner := s.login(t, "ops@ada.test")
	s.must(t, 201, owner, "POST", "/v1/members", map[string]any{"email": "cfo@ada.test", "password": "correct horse battery", "roles": []string{"approver"}})
	cfo := s.login(t, "cfo@ada.test")
	s.must(t, 201, owner, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "iswallet@1", "name": "main",
		"credentials": map[string]string{"api_key": "isw_test_key", "webhook_secret": "whsec_test"}})
	s.must(t, 204, owner, "POST", "/v1/egress", map[string]any{"environment": "prod", "host": "127.0.0.1"})
	s.secret(t, owner, "webhook_wf_batchPayouts", "batch-hook-key")
	wf := s.must(t, 201, owner, "POST", "/v1/workflows", map[string]any{"name": "Pay a batch", "definition": json.RawMessage(payoutsWD)})["id"].(string)
	s.must(t, 200, owner, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	var hook string
	for _, tr := range s.must(t, 200, owner, "GET", "/v1/workflows/"+wf+"/triggers", nil)["triggers"].([]any) {
		if m := tr.(map[string]any); m["environment"] == "prod" {
			hook, _ = m["url"].(string)
		}
	}

	batch := []byte(`{"batch_id":"B-2026-10","wallet":"w_company","people":[
	  {"id":"E1","kobo":30000000,"bank_code":"044","account_number":"0690000031","account_name":"Ada Obi"},
	  {"id":"E2","kobo":35000000,"bank_code":"058","account_number":"0123456789","account_name":"Bola Ade"},
	  {"id":"E3","kobo":45000000,"bank_code":"058","account_number":"0222222222","account_name":"Dayo Ojo"}]}`)
	sig := "sha256=" + mac(sha256.New, "batch-hook-key", batch)
	first := s.must(t, 202, anon, "POST", hook, batch, "X-Taskiem-Signature", sig)
	again := s.must(t, 202, anon, "POST", hook, batch, "X-Taskiem-Signature", sig)
	if again["run_id"] != first["run_id"] || again["duplicate"] != true {
		t.Fatalf("a repeated delivery started another run: %v %v", first, again)
	}
	if st, _ := s.call(t, anon, "POST", hook, batch, "X-Taskiem-Signature", "sha256=00"); st != 401 {
		t.Errorf("a bad signature: %d", st)
	}
	run := first["run_id"].(string)
	ref := runtime.RunRef{ID: uuid.MustParse(run), TenantID: uuid.MustParse(tenant)}

	s.Drain(t)
	if len(isw.moves) != 0 {
		t.Fatal("money moved before approval")
	}
	s.must(t, 403, owner, "POST", "/v1/approvals/"+run+"/approve", map[string]any{"decision": "approved"}) // wrote the workflow
	isw.mu.Lock()
	isw.failPayout = true // Ada's payout moves money, but its answer is lost
	isw.mu.Unlock()
	s.must(t, 200, cfo, "POST", "/v1/approvals/"+run+"/approve", map[string]any{"decision": "approved"})

	rt.WaitFor(t, 20*time.Second, "the payouts", func() bool {
		s.Drain(t)
		isw.mu.Lock()
		defer isw.mu.Unlock()
		return len(isw.moves) == 2
	})
	isw.mu.Lock()
	for k, n := range isw.moves {
		if n != 1 {
			t.Errorf("key %s moved money %d times", k, n)
		}
	}
	isw.mu.Unlock()
	ada, bola := isw.outflowFor("Ada Obi"), isw.outflowFor("Bola Ade")
	if ada == nil || bola == nil || isw.outflowFor("Dayo Ojo") != nil || ada["narration"] != "Batch B-2026-10" {
		t.Fatalf("payouts: ada %v bola %v", ada, bola)
	}

	events := "/hooks/" + tenant + "/connectors/iswallet@1/outflow_event?connection=main"
	deliver := func(o map[string]any, kind, secret string, want int) map[string]any {
		t.Helper()
		body, hdr := isw.event(o, kind, secret)
		st, out := s.call(t, anon, "POST", events, body, hdr...)
		if st != want {
			t.Fatalf("%s: %d %v", kind, st, out)
		}
		return out
	}
	// A forged event is refused; a real one for each payout ends its wait.
	deliver(ada, "wallet.outflow.confirmed", "not_the_secret", 401)
	if out := deliver(ada, "wallet.outflow.confirmed", "whsec_test", 202); out["normalised"] != float64(1) {
		t.Fatalf("ada's confirmation: %v", out)
	}
	if out := deliver(bola, "wallet.outflow.failed", "whsec_test", 202); out["normalised"] != float64(1) {
		t.Fatalf("bola's failure: %v", out)
	}
	// A reversal of a payout this run did not make wakes nothing.
	stray := map[string]any{"outflow_id": "out_999", "operation_id": "out_999", "wallet_id": "w_company", "amount": int64(100000), "fee": 5350,
		"currency": "NGN", "idempotency_key": "tsk_other", "provider_reference": "tsk_other"}
	if out := deliver(stray, "wallet.outflow.reversed", "whsec_test", 202); out["normalised"] != nil {
		t.Errorf("a stray reversal woke a run: %v", out)
	}
	s.Drain(t)
	if st := s.Status(t, ref); st == "completed" || st == "failed" {
		t.Fatalf("the run ended before Ada's reversal watch: %s", st)
	}
	// Ada's payout comes back: the watch notices it and pays nobody again.
	if out := deliver(ada, "wallet.outflow.reversed", "whsec_test", 202); out["normalised"] != float64(1) {
		t.Fatalf("ada's reversal: %v", out)
	}
	rt.WaitFor(t, 20*time.Second, "the run to end", func() bool { s.Drain(t); return s.Status(t, ref) == "completed" })
	isw.mu.Lock()
	if len(isw.moves) != 2 {
		t.Errorf("the watch moved money: %v", isw.moves)
	}
	isw.mu.Unlock()

	got := toJSON(s.must(t, 200, owner, "GET", "/v1/runs/"+run, nil))
	for _, want := range []string{`"status": "settled"`, `"status": "reversed"`, "EXCEEDS_SINGLE_TXN_LIMIT"} {
		if !strings.Contains(strings.ReplaceAll(got, `":"`, `": "`), want) {
			t.Errorf("the run does not show %s", want)
		}
	}
	if strings.Contains(got, "0690000031") {
		t.Error("an account number is in the run's history in plain text")
	}
	opened, err := s.Store.OpenedHistory(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	def, err := s.Store.RunDefinition(context.Background(), ref.TenantID, ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := decide.Verify(def, opened); err != nil {
		t.Errorf("replay over the pinned expansion: %v", err)
	}
}

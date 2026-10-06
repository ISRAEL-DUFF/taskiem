package e2e

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/builtin"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// lose drops the connection after the provider has acted, as a timeout or
// a broken network would: the caller cannot know what happened.
func lose(w http.ResponseWriter) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err == nil {
		_ = conn.Close()
	}
}

// runTo starts a manual run through the API and drives it until it leaves
// "running" or deadline passes, returning the run as the API shows it.
func runTo(t *testing.T, s *stack, owner, wf string, input map[string]any, stop func(map[string]any) bool) (string, map[string]any) {
	t.Helper()
	run := s.must(t, 201, owner, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": input})["run_id"].(string)
	var got map[string]any
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		s.Drain(t)
		if got = s.must(t, 200, owner, "GET", "/v1/runs/"+run, nil); stop(got) {
			return run, got
		}
	}
	t.Fatalf("run %s did not get there: %s\n%s", run, runStatus(got), toJSON(got["events"]))
	return run, got
}

// scheduled reports whether a run has scheduled a step (a signal step is
// then waiting).
func scheduled(r map[string]any, step string) bool {
	events, _ := r["events"].([]any)
	for _, e := range events {
		m, _ := e.(map[string]any)
		if m["type"] == "StepScheduled" && m["step_id"] == step {
			return true
		}
	}
	return false
}

func runStatus(r map[string]any) string { return r["run"].(map[string]any)["status"].(string) }

const lencoFlow = `{"schema":"wd/v1","id":"wf_lencoPayout","version":1,"name":"Lenco payout","trigger":{"type":"manual"},
  "steps":[
    {"id":"pay","type":"connector","connector":"lenco@1","action":"transfer","connection":"main",
     "input":{"amount":"=trigger.body.amount","account_number":"=trigger.body.account_number","bank_code":"=trigger.body.bank_code","narration":"Payout"},
     "retry":{"max":5,"backoff":"exponential","initial":"200ms"}},
    {"id":"settle","type":"signal","needs":["pay"],"config":{"event":"lenco@1:event","correlation":"=steps.pay.output.reference","timeout":"1h"}},
    {"id":"done","type":"transform","needs":["settle"],"config":{"output":{"event":"=steps.settle.output.event","reference":"=steps.pay.output.reference"}}}]}`

// TestLencoPayoutSurvivesALostResponse: Lenco accepts the transfer but the
// answer is lost. The engine resends under the same reference, Lenco refuses
// the duplicate, the connector reports the transfer already made, and the
// run settles on Lenco's signed webhook. One transfer, never two.
func TestLencoPayoutSurvivesALostResponse(t *testing.T) {
	var mu sync.Mutex
	posts := map[string]int{}
	lenco := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer lenco-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/transfer":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			ref := b["reference"].(string)
			if b["amount"] != "2000.00" {
				t.Errorf("amount sent as %v, want naira \"2000.00\"", b["amount"])
			}
			posts[ref]++
			if posts[ref] == 1 {
				lose(w) // the transfer is made; the answer never arrives
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"status":false,"message":"Duplicate client reference","data":[],"errorCode":"04"}`))
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/transfer/by-reference/"):
			ref := strings.TrimPrefix(r.URL.Path, "/transfer/by-reference/")
			if posts[ref] == 0 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"status":false,"message":"Transaction was not found","data":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":true,"message":"","data":{"request":{"reference":"` + ref + `","status":"created"},
			  "transaction":{"id":"txn-1","amount":"2000.00","fee":"10.75","status":"pending","clientReference":"` + ref + `","details":{"accountName":"ADA OBI"}}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer lenco.Close()

	s := newStack(t, builtin.Options{LencoURL: lenco.URL})
	tenant := s.must(t, 201, "", "POST", "/v1/signup", map[string]any{"tenant": "Acme", "email": "ops@acme.test", "password": "correct horse battery"})["tenant_id"].(string)
	owner := s.login(t, "ops@acme.test")
	s.must(t, 201, owner, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "lenco@1", "name": "main",
		"credentials": map[string]string{"api_token": "lenco-token", "account_id": "056ffebf-812a-433a-83a0-9c67cd8c089c"}})
	wf := s.must(t, 201, owner, "POST", "/v1/workflows", map[string]any{"name": "pay", "definition": json.RawMessage(lencoFlow)})["id"].(string)
	s.must(t, 200, owner, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)

	run, got := runTo(t, s, owner, wf, map[string]any{"amount": 200000, "account_number": "8144374977", "bank_code": "000014"},
		func(r map[string]any) bool { return scheduled(r, "settle") || runStatus(r) != "running" })
	if runStatus(got) != "running" {
		t.Fatalf("run %s ended %s before the webhook:\n%s", run, runStatus(got), toJSON(got["events"]))
	}
	mu.Lock()
	var ref string
	for r, n := range posts {
		ref = r
		if n != 2 {
			t.Errorf("reference %s sent %d times, want 2 (the lost one and the refused resend)", r, n)
		}
	}
	if len(posts) != 1 {
		t.Errorf("%d references used, want 1: %v", len(posts), posts)
	}
	mu.Unlock()

	// Lenco's webhook: HMAC-SHA512 keyed with the hex SHA-256 of the token.
	body := []byte(`{"event":"transaction.successful","data":{"id":"txn-1","status":"successful","clientReference":"` + ref + `"}}`)
	key := sha256.Sum256([]byte("lenco-token"))
	hook := "/hooks/" + tenant + "/connectors/lenco@1/event?connection=main"
	if st, _ := s.call(t, "", "POST", hook, body, "X-Lenco-Signature", mac(sha512.New, "forged", body)); st != http.StatusUnauthorized {
		t.Errorf("forged webhook: %d", st)
	}
	out := s.must(t, 202, "", "POST", hook, body, "X-Lenco-Signature", mac(sha512.New, hex.EncodeToString(key[:]), body))
	if out["signalled"] != float64(1) {
		t.Fatalf("webhook: %v", out)
	}
	rt.WaitFor(t, 30*time.Second, "settled run", func() bool {
		s.Drain(t)
		got = s.must(t, 200, owner, "GET", "/v1/runs/"+run, nil)
		return runStatus(got) != "running"
	})
	if runStatus(got) != "completed" || !strings.Contains(toJSON(got), "transaction.successful") {
		t.Fatalf("run %s: %s\n%s", run, runStatus(got), toJSON(got["events"]))
	}
}

const breetFlow = `{"schema":"wd/v1","id":"wf_breetWithdraw","version":1,"name":"Breet withdrawal","trigger":{"type":"manual"},
  "steps":[{"id":"withdraw","type":"connector","connector":"breet@1","action":"withdraw_crypto","connection":"main",
    "input":{"amount_usd":"=trigger.body.cents","token":"USDT","network":"TRC20","wallet_address":"=trigger.body.address"},
    "retry":{"max":5,"backoff":"exponential","initial":"200ms"}}]}`

// TestBreetWithdrawalReconcilesInsteadOfResending: Breet takes the
// withdrawal but the answer is lost. Breet has no idempotency key, so the
// engine must not send again: it asks Breet for the withdrawal by our
// externalId and records what it finds.
func TestBreetWithdrawalReconcilesInsteadOfResending(t *testing.T) {
	var mu sync.Mutex
	var sent []string
	breet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("x-app-secret") != "sec" || r.Header.Get("X-Breet-Env") != "production" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/payments/withdraw/address":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			sent = append(sent, b["externalId"].(string))
			if b["amount"] != 25.0 || b["pin"] != "4321" {
				t.Errorf("withdrawal body %v", b)
			}
			lose(w)
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/payments/withdrawal/"):
			id := strings.TrimPrefix(r.URL.Path, "/payments/withdrawal/")
			if len(sent) == 0 || id != sent[0] {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"success":false,"message":"withdrawal not found"}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"message":"data fetched","data":{"id":"w-1","externalId":"` + id + `","status":"processing","amount":25,"currency":"usd","fee":1,"reason":""}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer breet.Close()

	s := newStack(t, builtin.Options{BreetURL: breet.URL})
	s.must(t, 201, "", "POST", "/v1/signup", map[string]any{"tenant": "Acme", "email": "ops@acme.test", "password": "correct horse battery"})
	owner := s.login(t, "ops@acme.test")
	s.must(t, 201, owner, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "breet@1", "name": "main",
		"credentials": map[string]string{"app_id": "app", "app_secret": "sec", "pin": "4321"}})
	wf := s.must(t, 201, owner, "POST", "/v1/workflows", map[string]any{"name": "withdraw", "definition": json.RawMessage(breetFlow)})["id"].(string)
	s.must(t, 200, owner, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)

	run, got := runTo(t, s, owner, wf, map[string]any{"cents": 2500, "address": "TRecipient"}, func(r map[string]any) bool { return runStatus(r) != "running" })
	if runStatus(got) != "completed" || !strings.Contains(toJSON(got), `"withdrawal_id":"w-1"`) {
		t.Fatalf("run %s: %s\n%s", run, runStatus(got), toJSON(got["events"]))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 {
		t.Errorf("withdrawal sent %d times, want once: %v", len(sent), sent)
	}
}

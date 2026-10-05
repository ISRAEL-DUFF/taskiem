package e2e

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/wd"
)

// stack is the API and the edge on one test server, over the harness engine.
type stack struct {
	*rt.Env
	url string
}

func newStack(t *testing.T, opts builtin.Options) *stack {
	e := rt.New(t)
	if err := builtin.Register(e.Registry, opts); err != nil {
		t.Fatal(err)
	}
	hooks := &ingest.Handler{Store: e.Store, Secrets: e.Vault, Connections: e.Vault, Registry: e.Registry}
	srv := httptest.NewServer((&api.Server{Store: e.Store, Vault: e.Vault, Registry: e.Registry, AllowSignup: true, Ingest: hooks}).Handler())
	t.Cleanup(srv.Close)
	return &stack{Env: e, url: srv.URL}
}

// call makes an API or webhook request and decodes the JSON reply.
func (s *stack) call(t *testing.T, token, method, path string, body any, hdr ...string) (int, map[string]any) {
	t.Helper()
	var raw []byte
	switch b := body.(type) {
	case nil:
	case []byte:
		raw = b
	default:
		raw, _ = json.Marshal(b)
	}
	req, _ := http.NewRequest(method, s.url+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func (s *stack) must(t *testing.T, want int, token, method, path string, body any, hdr ...string) map[string]any {
	t.Helper()
	got, out := s.call(t, token, method, path, body, hdr...)
	if got != want {
		t.Fatalf("%s %s: %d, want %d: %v", method, path, got, want, out)
	}
	return out
}

func (s *stack) login(t *testing.T, email string) string {
	return s.must(t, 200, "", "POST", "/v1/auth/login", map[string]any{"email": email, "password": "correct horse battery"})["token"].(string)
}

// publishFile creates and publishes a dogfood workflow through the API and
// returns its id and its trigger URL in prod.
func (s *stack) publishFile(t *testing.T, owner, file string) (string, string) {
	t.Helper()
	doc, err := os.ReadFile("../flows/dogfood/" + file)
	if err != nil {
		t.Fatal(err)
	}
	wf := s.must(t, 201, owner, "POST", "/v1/workflows", map[string]any{"name": file, "definition": json.RawMessage(doc)})["id"].(string)
	s.must(t, 200, owner, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	for _, tr := range s.must(t, 200, owner, "GET", "/v1/workflows/"+wf+"/triggers", nil)["triggers"].([]any) {
		m := tr.(map[string]any)
		if m["environment"] == "prod" {
			u, _ := m["url"].(string)
			return wf, u
		}
	}
	return wf, ""
}

func mac(h func() hash.Hash, key string, body []byte) string {
	m := hmac.New(h, []byte(key))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

func TestPayrollaDisbursementEndToEnd(t *testing.T) {
	isw := newFakeIswallet()
	isw.balances["w_company"] = 900_000_000
	// Ada's transfer reaches iswallet and moves money, but its response is
	// lost as a 500: the retry must be answered from the replay cache.
	iswallet := httptest.NewServer(isw)
	defer iswallet.Close()
	var mu sync.Mutex
	var reports []map[string]any
	payrolla := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		reports = append(reports, map[string]any{"path": r.URL.Path, "auth": r.Header.Get("Authorization"), "key": r.Header.Get("Idempotency-Key"), "body": in})
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer payrolla.Close()

	s := newStack(t, builtin.Options{IswalletURL: iswallet.URL})
	anon := ""
	tenant := s.must(t, 201, anon, "POST", "/v1/signup", map[string]any{"tenant": "Payrolla", "email": "ops@payrolla.test", "password": "correct horse battery"})["tenant_id"].(string)
	owner := s.login(t, "ops@payrolla.test")
	s.must(t, 201, owner, "POST", "/v1/members", map[string]any{"email": "cfo@payrolla.test", "password": "correct horse battery", "roles": []string{"approver", "payroll_approver"}})
	cfo := s.login(t, "cfo@payrolla.test")
	s.must(t, 201, owner, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "iswallet@1", "name": "payrolla",
		"credentials": map[string]string{"api_key": "isw_payrolla_key", "webhook_secret": "whsec_payrolla"}})
	s.must(t, 204, owner, "PUT", "/v1/secrets/prod/webhook_wf_payrollaSalaryDisbursement", map[string]any{"value": "payrolla-hook-key"})
	s.must(t, 204, owner, "PUT", "/v1/variables/prod/payrolla_api_url", map[string]any{"value": payrolla.URL})
	s.must(t, 204, owner, "POST", "/v1/egress", map[string]any{"environment": "prod", "host": "127.0.0.1"})
	s.Secrets["payrolla_api_token"] = "pr_token"

	_, hook := s.publishFile(t, owner, "payrolla-salary-disbursement.wd.json")
	if !strings.HasPrefix(hook, "/hooks/"+tenant+"/payrolla/payroll-approved") {
		t.Fatalf("webhook url %q", hook)
	}

	payroll := []byte(`{"payroll_id":"PR-2026-09","period":"September 2026","funding_wallet_id":"w_company","total_kobo":90000000,"employees":[
	  {"employee_id":"E1","net_pay_kobo":30000000,"wallet_id":"w_ada"},
	  {"employee_id":"E2","net_pay_kobo":35000000,"bank_code":"044","account_number":"0690000031","account_name":"Bola Ade"},
	  {"employee_id":"E3","net_pay_kobo":25000000,"wallet_id":"w_chi"}]}`)
	sig := "sha256=" + mac(sha256.New, "payrolla-hook-key", payroll)
	first := s.must(t, 202, anon, "POST", hook, payroll, "X-Taskiem-Signature", sig)
	again := s.must(t, 202, anon, "POST", hook, payroll, "X-Taskiem-Signature", sig)
	if again["run_id"] != first["run_id"] || again["duplicate"] != true {
		t.Fatalf("Payrolla retry started another run: %v %v", first, again)
	}
	run := first["run_id"].(string)
	ref := runtime.RunRef{ID: uuid.MustParse(run), TenantID: uuid.MustParse(tenant)}

	// Balance check, then the approval waits; nothing moves before approval.
	s.Drain(t)
	if len(isw.moves) != 0 {
		t.Fatal("money moved before approval")
	}
	inbox := s.must(t, 200, cfo, "GET", "/v1/approvals", nil)["approvals"].([]any)
	if len(inbox) != 1 || !strings.Contains(toJSON(inbox), `"total_kobo":90000000`) || !strings.Contains(toJSON(inbox), `"available_kobo":900000000`) || !strings.Contains(toJSON(inbox), `"paid_to_bank":1`) {
		t.Fatalf("approver inbox: %s", toJSON(inbox))
	}
	s.must(t, 403, owner, "POST", "/v1/approvals/"+run+"/approve", map[string]any{"decision": "approved"}) // wrote the workflow
	isw.mu.Lock()
	isw.failNextTransferTo("w_ada")
	isw.mu.Unlock()
	s.must(t, 200, cfo, "POST", "/v1/approvals/"+run+"/approve", map[string]any{"decision": "approved"})

	// Two wallet transfers settle at once; Bola's bank payout waits for iswallet.
	rt.WaitFor(t, 15*time.Second, "transfers and payout", func() bool {
		s.Drain(t)
		isw.mu.Lock()
		defer isw.mu.Unlock()
		return len(isw.moves) == 3
	})
	if st := s.Status(t, ref); st != "running" {
		t.Fatalf("after transfers: %s", st)
	}
	o := isw.outflowFor("Bola Ade")
	if o == nil {
		t.Fatal("no payout for Bola")
	}
	body, hdr := isw.event(o, "wallet.outflow.confirmed", "whsec_payrolla")
	out := s.must(t, 202, anon, "POST", "/hooks/"+tenant+"/connectors/iswallet@1/outflow_event?connection=payrolla", body, hdr...)
	if out["signalled"] != float64(1) {
		t.Fatalf("settlement: %v", out)
	}
	// A forged delivery is refused.
	forged, fh := isw.event(o, "wallet.outflow.confirmed", "not_the_secret")
	if st, _ := s.call(t, anon, "POST", "/hooks/"+tenant+"/connectors/iswallet@1/outflow_event?connection=payrolla", forged, fh...); st != 401 {
		t.Errorf("forged webhook: %d", st)
	}
	// Ada's transfer retries with the same key and is answered from the replay cache.
	rt.WaitFor(t, 30*time.Second, "payroll run", func() bool { s.Drain(t); return s.Status(t, ref) != "running" })
	if st := s.Status(t, ref); st != "completed" {
		h, _ := s.Store.RunHistory(ctx, ref)
		var b strings.Builder
		for _, ev := range h {
			b.WriteString(ev.Type + "(" + ev.StepID + ") " + string(ev.Payload) + "\n")
		}
		t.Fatalf("status %s:\n%s", st, b.String())
	}
	isw.mu.Lock()
	for k, n := range isw.moves {
		if n != 1 {
			t.Errorf("key %s moved money %d times", k, n)
		}
	}
	if len(isw.moves) != 3 || isw.balances["w_ada"] != 30000000 || isw.balances["w_chi"] != 25000000 || isw.balances["w_company"] != 900000000-90000000-5350 {
		t.Errorf("moves %v, balances %v", isw.moves, isw.balances)
	}
	isw.mu.Unlock()
	mu.Lock()
	if len(reports) != 1 || reports[0]["path"] != "/payrolls/PR-2026-09/disbursement-report" || reports[0]["auth"] != "Bearer pr_token" {
		t.Errorf("reports: %v", reports)
	}
	mu.Unlock()

	// Personal data stays sealed at rest; replay holds over the opened history.
	sealed := s.must(t, 200, owner, "GET", "/v1/runs/"+run, nil)
	if strings.Contains(toJSON(sealed), "0690000031") {
		t.Error("account number stored in plaintext")
	}
	opened, err := s.Store.OpenedHistory(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := os.ReadFile("../flows/dogfood/payrolla-salary-disbursement.wd.json")
	def, _ := wd.Load(doc)
	if err := decide.Verify(def, opened); err != nil {
		t.Errorf("replay: %v", err)
	}
}

func TestOpsPayoutFailureAlertEndToEnd(t *testing.T) {
	var mu sync.Mutex
	var sms, slack []string
	termii := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		sms = append(sms, in["to"].(string)+": "+in["sms"].(string))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"message_id":"m1","message":"Successfully Sent","balance":100}`))
	}))
	defer termii.Close()
	slackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		slack = append(slack, in["text"].(string))
		mu.Unlock()
		_, _ = w.Write([]byte("ok"))
	}))
	defer slackSrv.Close()

	s := newStack(t, builtin.Options{TermiiURL: termii.URL})
	tenant := s.must(t, 201, "", "POST", "/v1/signup", map[string]any{"tenant": "Holdco Ops", "email": "oncall@holdco.test", "password": "correct horse battery"})["tenant_id"].(string)
	owner := s.login(t, "oncall@holdco.test")
	s.must(t, 201, owner, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "iswallet@1", "credentials": map[string]string{"api_key": "isw_ops", "webhook_secret": "whsec_ops"}})
	s.must(t, 201, owner, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "termii@1", "credentials": map[string]string{"api_key": "tm_key", "sender_id": "Holdco"}})
	s.must(t, 204, owner, "PUT", "/v1/variables/prod/ops_on_call_phone", map[string]any{"value": "2348000000001"})
	s.must(t, 204, owner, "POST", "/v1/egress", map[string]any{"environment": "prod", "host": "127.0.0.1"})
	s.Secrets["ops_slack_webhook_url"] = slackSrv.URL + "/services/T/B/X"
	_, hook := s.publishFile(t, owner, "ops-payout-failure-alert.wd.json")
	if hook != "/hooks/"+tenant+"/connectors/iswallet@1/outflow_event?env=prod" {
		t.Fatalf("hook %q", hook)
	}

	isw := newFakeIswallet()
	send := func(o map[string]any, eventType string) map[string]any {
		body, hdr := isw.event(o, eventType, "whsec_ops")
		return s.must(t, 202, "", "POST", hook, body, hdr...)
	}
	payout := map[string]any{"outflow_id": "out_9", "operation_id": "out_9", "wallet_id": "w_company", "amount": 250000, "fee": 5350, "currency": "NGN", "idempotency_key": "tsk_x", "provider_reference": "tsk_x"}
	if n := len(send(payout, "wallet.outflow.failed")["runs"].([]any)); n != 1 {
		t.Fatalf("failed payout started %d runs", n)
	}
	send(payout, "wallet.outflow.failed") // iswallet redelivers
	if n := len(send(payout, "wallet.outflow.confirmed")["runs"].([]any)); n != 0 {
		t.Errorf("confirmed payout started %d alert runs", n)
	}
	rt.WaitFor(t, 10*time.Second, "alerts", func() bool {
		s.Drain(t)
		mu.Lock()
		defer mu.Unlock()
		return len(sms) >= 1 && len(slack) >= 1
	})
	mu.Lock()
	defer mu.Unlock()
	want := "iswallet payout failed: 2500 NGN, outflow out_9, wallet w_company (beneficiary account closed)"
	if len(sms) != 1 || sms[0] != "2348000000001: "+want || len(slack) != 1 || slack[0] != want {
		t.Errorf("sms %v, slack %v", sms, slack)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

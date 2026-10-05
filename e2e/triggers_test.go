package e2e

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
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

// fakePaystack records transfers by reference; listed recipients come back
// pending (settled later by webhook), the rest succeed at once.
type fakePaystack struct {
	mu        sync.Mutex
	transfers map[string]int // reference -> times created
	recipient map[string]string
	pending   map[string]bool
}

func (f *fakePaystack) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/balance":
		_, _ = w.Write([]byte(`{"status":true,"data":[{"currency":"NGN","balance":900000000}]}`))
	case "/transfer":
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		ref, rcp := in["reference"].(string), in["recipient"].(string)
		f.transfers[ref]++
		f.recipient[ref] = rcp
		status := "success"
		if f.pending[rcp] {
			status = "pending"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": true, "data": map[string]any{"reference": ref, "status": status, "amount": in["amount"], "currency": "NGN", "transfer_code": "TRF_" + ref[:8]}})
	default:
		http.NotFound(w, r)
	}
}

func TestPayrollaDisbursementEndToEnd(t *testing.T) {
	ps := &fakePaystack{transfers: map[string]int{}, recipient: map[string]string{}, pending: map[string]bool{"RCP_bola": true}}
	paystack := httptest.NewServer(ps)
	defer paystack.Close()
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

	s := newStack(t, builtin.Options{PaystackURL: paystack.URL})
	anon := ""
	tenant := s.must(t, 201, anon, "POST", "/v1/signup", map[string]any{"tenant": "Payrolla", "email": "ops@payrolla.test", "password": "correct horse battery"})["tenant_id"].(string)
	owner := s.login(t, "ops@payrolla.test")
	s.must(t, 201, owner, "POST", "/v1/members", map[string]any{"email": "cfo@payrolla.test", "password": "correct horse battery", "roles": []string{"approver", "payroll_approver"}})
	cfo := s.login(t, "cfo@payrolla.test")
	s.must(t, 201, owner, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "credentials": map[string]string{"secret_key": "sk_test_pr"}})
	s.must(t, 204, owner, "PUT", "/v1/secrets/prod/webhook_wf_payrollaSalaryDisbursement", map[string]any{"value": "payrolla-hook-key"})
	s.must(t, 204, owner, "PUT", "/v1/variables/prod/payrolla_api_url", map[string]any{"value": payrolla.URL})
	s.must(t, 204, owner, "POST", "/v1/egress", map[string]any{"environment": "prod", "host": "127.0.0.1"})
	s.Secrets["payrolla_api_token"] = "pr_token"

	wf, hook := s.publishFile(t, owner, "payrolla-salary-disbursement.wd.json")
	if !strings.HasPrefix(hook, "/hooks/"+tenant+"/payrolla/payroll-approved") {
		t.Fatalf("webhook url %q", hook)
	}

	payroll := []byte(`{"payroll_id":"PR-2026-09","period":"September 2026","total_kobo":90000000,"employees":[
	  {"employee_id":"E1","net_pay_kobo":30000000,"recipient_code":"RCP_ada"},
	  {"employee_id":"E2","net_pay_kobo":35000000,"recipient_code":"RCP_bola"},
	  {"employee_id":"E3","net_pay_kobo":25000000,"recipient_code":"RCP_chi"}]}`)
	sig := "sha256=" + mac(sha256.New, "payrolla-hook-key", payroll)
	first := s.must(t, 202, anon, "POST", hook, payroll, "X-Taskiem-Signature", sig)
	again := s.must(t, 202, anon, "POST", hook, payroll, "X-Taskiem-Signature", sig)
	if again["run_id"] != first["run_id"] || again["duplicate"] != true {
		t.Fatalf("Payrolla retry started another run: %v %v", first, again)
	}
	run := first["run_id"].(string)
	ref := runtime.RunRef{ID: uuid.MustParse(run), TenantID: uuid.MustParse(tenant)}

	// Balance check, then the approval waits; no transfer before approval.
	s.Drain(t)
	if len(ps.transfers) != 0 {
		t.Fatal("transferred before approval")
	}
	inbox := s.must(t, 200, cfo, "GET", "/v1/approvals", nil)["approvals"].([]any)
	if len(inbox) != 1 || !strings.Contains(toJSON(inbox), `"total_kobo":90000000`) || !strings.Contains(toJSON(inbox), `"ngn_balance_kobo":900000000`) {
		t.Fatalf("approver inbox: %s", toJSON(inbox))
	}
	s.must(t, 403, owner, "POST", "/v1/approvals/"+run+"/approve", map[string]any{"decision": "approved"}) // wrote the workflow
	s.must(t, 200, cfo, "POST", "/v1/approvals/"+run+"/approve", map[string]any{"decision": "approved"})

	// Three transfers; Bola's is pending until Paystack's webhook.
	s.Drain(t)
	if st := s.Status(t, ref); st != "running" {
		t.Fatalf("after transfers: %s", st)
	}
	var bolaRef string
	for r, rcp := range ps.transfers {
		if rcp != 1 {
			t.Errorf("transfer %s created %d times", r, rcp)
		}
		if ps.recipient[r] == "RCP_bola" {
			bolaRef = r
		}
	}
	if len(ps.transfers) != 3 || bolaRef == "" {
		t.Fatalf("transfers: %v", ps.transfers)
	}
	settle := []byte(`{"event":"transfer.success","data":{"reference":"` + bolaRef + `","amount":35000000,"status":"success"}}`)
	out := s.must(t, 202, anon, "POST", "/hooks/"+tenant+"/connectors/paystack@1/transfer_event", settle, "x-paystack-signature", mac(sha512.New, "sk_test_pr", settle))
	if out["signalled"] != float64(1) {
		t.Fatalf("settlement: %v", out)
	}
	s.Drain(t)
	if st := s.Status(t, ref); st != "completed" {
		h, _ := s.Store.RunHistory(ctx, ref)
		var b strings.Builder
		for _, ev := range h {
			b.WriteString(ev.Type + "(" + ev.StepID + ") " + string(ev.Payload) + "\n")
		}
		t.Fatalf("status %s:\n%s", st, b.String())
	}
	mu.Lock()
	if len(reports) != 1 || reports[0]["path"] != "/payrolls/PR-2026-09/disbursement-report" || reports[0]["auth"] != "Bearer pr_token" {
		t.Errorf("reports: %v", reports)
	}
	mu.Unlock()

	// Personal data stays sealed at rest; replay holds over the opened history.
	sealed := s.must(t, 200, owner, "GET", "/v1/runs/"+run, nil)
	if strings.Contains(toJSON(sealed), "RCP_ada") {
		t.Error("recipient codes stored in plaintext")
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
	_ = wf
}

func TestOpsTransferFailureAlertEndToEnd(t *testing.T) {
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
	s.must(t, 201, owner, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "credentials": map[string]string{"secret_key": "sk_ops"}})
	s.must(t, 201, owner, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "termii@1", "credentials": map[string]string{"api_key": "tm_key", "sender_id": "Holdco"}})
	s.must(t, 204, owner, "PUT", "/v1/variables/prod/ops_on_call_phone", map[string]any{"value": "2348000000001"})
	s.must(t, 204, owner, "POST", "/v1/egress", map[string]any{"environment": "prod", "host": "127.0.0.1"})
	s.Secrets["ops_slack_webhook_url"] = slackSrv.URL + "/services/T/B/X"
	_, hook := s.publishFile(t, owner, "ops-transfer-failure-alert.wd.json")
	if hook != "/hooks/"+tenant+"/connectors/paystack@1/transfer_event?env=prod" {
		t.Fatalf("hook %q", hook)
	}

	send := func(body string) map[string]any {
		b := []byte(body)
		return s.must(t, 202, "", "POST", hook, b, "x-paystack-signature", mac(sha512.New, "sk_ops", b))
	}
	failed := `{"event":"transfer.failed","data":{"reference":"TRF-9","amount":250000,"reason":"Account closed"}}`
	if n := len(send(failed)["runs"].([]any)); n != 1 {
		t.Fatalf("failed transfer started %d runs", n)
	}
	send(failed) // Paystack retry
	if n := len(send(`{"event":"transfer.success","data":{"reference":"TRF-10","amount":1}}`)["runs"].([]any)); n != 0 {
		t.Errorf("success started %d alert runs", n)
	}
	rt.WaitFor(t, 10*time.Second, "alerts", func() bool {
		s.Drain(t)
		mu.Lock()
		defer mu.Unlock()
		return len(sms) >= 1 && len(slack) >= 1
	})
	mu.Lock()
	defer mu.Unlock()
	want := "Paystack transfer.failed: 2500 NGN, ref TRF-9 (Account closed)"
	if len(sms) != 1 || sms[0] != "2348000000001: "+want || len(slack) != 1 || slack[0] != want {
		t.Errorf("sms %v, slack %v", sms, slack)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

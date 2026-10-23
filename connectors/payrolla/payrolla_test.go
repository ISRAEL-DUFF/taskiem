package payrolla

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/conntest"
	"github.com/israel-duff/taskiem/engine/expr"
)

// Every action passes the conformance kit on fixtures shaped after
// Payrolla's published examples (docs/integrations/payrolla-reference).
func TestConformance(t *testing.T) {
	s, err := conntest.LoadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	rep := conntest.Run(context.Background(), New(Options{}), s, conntest.Options{})
	for _, r := range rep.Results {
		for _, p := range r.Problems {
			t.Errorf("%s: %s", r.Case, p)
		}
	}
	if len(rep.Uncovered)+len(rep.KeyNotSent)+len(rep.ReadWrites) > 0 {
		t.Errorf("uncovered %v, key not sent %v, reads that write %v", rep.Uncovered, rep.KeyNotSent, rep.ReadWrites)
	}
}

func TestRegisters(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, _ := reg.Get("payrolla@1")
	if c.Manifest.KindOf() != "product" || len(c.Manifest.Capabilities) != 0 || c.Manifest.Hosts()[0] != "synledger.name.ng" {
		t.Errorf("kind %s, capabilities %v, hosts %v", c.Manifest.KindOf(), c.Manifest.Capabilities, c.Manifest.Hosts())
	}
	for name := range c.Manifest.Triggers {
		if c.Registrars[name] == nil {
			t.Errorf("%s: no registrar", name)
		}
	}
}

// A revoked key says what to do about it.
func TestRevokedKeyNamesTheFix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"API_KEY_INVALID","message":"revoked"}}`)
	}))
	defer srv.Close()
	c := New(Options{BaseURL: srv.URL})
	_, err := c.Actions["get_company"].Execute(context.Background(), connector.Request{HTTP: srv.Client(), Credentials: map[string]string{"api_key": "pyk_live_x"}})
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != "API_KEY_INVALID" || !strings.Contains(err.Error(), "reconnect") || !errors.Is(err, connector.ErrCredentialsRejected) {
		t.Fatalf("err %v", err)
	}
}

// Payrolla's test vector (api/webhooks.md) verifies; a changed body, a
// wrong secret and a stale timestamp do not.
func TestSignatureVector(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["run_event"]
	defer func(now func() time.Time) { connector.Now = now }(connector.Now)
	connector.Now = func() time.Time { return time.Unix(1700000000+60, 0) }
	h := http.Header{}
	h.Set("Payrolla-Signature", "t=1700000000,v1=33ff6664879612e191c4e01184f3da32597a3c9b33ca2655fac317d01c5abc3f")
	body := []byte(`{"id":"evt_1","type":"ping"}`)
	if err := connector.VerifyWebhook(spec.Verify, "whsec_test", h, body); err != nil {
		t.Fatalf("the vector: %v", err)
	}
	if connector.VerifyWebhook(spec.Verify, "whsec_test", h, []byte(`{"id":"evt_2","type":"ping"}`)) == nil {
		t.Error("a changed body verified")
	}
	if connector.VerifyWebhook(spec.Verify, "whsec_other", h, body) == nil {
		t.Error("a wrong secret verified")
	}
	connector.Now = func() time.Time { return time.Unix(1700000000+6*60, 0) }
	if connector.VerifyWebhook(spec.Verify, "whsec_test", h, body) == nil {
		t.Error("a delivery over 5 minutes old verified")
	}
}

// Each trigger reads Payrolla's documented events: the type, the event id
// to deduplicate on, and the id a waiting step correlates on.
func TestTriggerExpressions(t *testing.T) {
	triggers := New(Options{}).Manifest.Triggers
	e := expr.MustNewTriggerEngine("body", "headers", "query", "item")
	for _, c := range []struct{ trigger, body, typ, corr string }{
		{"run_event", `{"id":"01J9ZA","type":"run.computed","api_version":"2026-11-01","created_at":"2026-10-25T09:12:03.120Z","company_id":"co",
		  "data":{"run":{"run_id":"run-1","period_start":"2026-10-01","period_end":"2026-10-31","pay_date":"2026-10-28","run_type":"REGULAR","status":"COMPUTED",
		  "employee_count":48,"total_gross_kobo":1240000000,"total_net_kobo":920000000},"approval_url":"https://payroller.surge.sh/runs/run-1/review"}}`, "run.computed", "run-1"},
		{"payout_event", `{"id":"01J9ZB","type":"payout.failed","api_version":"2026-11-01","created_at":"2026-10-28T10:01:12Z","company_id":"co",
		  "data":{"run_id":"run-1","payslip_id":"ps-1","employee_id":"emp-1","amount_kobo":18500000,"method":"BANK","reference":"NIP-1","reason":"beneficiary account is dormant"}}`, "payout.failed", "run-1"},
		{"employee_event", `{"id":"01J9ZC","type":"employee.terminated","api_version":"2026-11-01","created_at":"2026-10-28T10:01:12Z","company_id":"co",
		  "data":{"employee":{"id":"emp-1","first_name":"Ngozi","last_name":"Eze","email":"n@acme.ng","phone":"+234","department":"Sales","status":"TERMINATED",
		  "employment_type":"SALARIED","start_date":"2024-02-01","pay_method":"BANK"},"changed_fields":["status"]}}`, "employee.terminated", "emp-1"},
		{"payslip_event", `{"id":"01J9ZD","type":"payslip.issued","api_version":"2026-11-01","created_at":"2026-10-28T10:01:12Z","company_id":"co",
		  "data":{"payslip_id":"ps-1","employee_id":"emp-1","run_id":"run-1","period":"October 2026"}}`, "payslip.issued", "run-1"},
		{"run_event", `{"id":"01J9ZE","type":"ping","api_version":"2026-11-01","created_at":"2026-10-28T10:01:12Z","company_id":"co","data":{"message":"hello","subscription_id":"s"}}`, "ping", ""},
	} {
		spec := triggers[c.trigger]
		body, err := expr.DecodeJSON([]byte(c.body))
		if err != nil {
			t.Fatal(err)
		}
		act := map[string]any{"body": body, "headers": map[string]any{}, "query": map[string]any{}, "item": nil}
		for src, want := range map[string]any{spec.EventType: c.typ, spec.Dedup: body.(map[string]any)["id"], spec.Correlation: c.corr, spec.TestEvent: c.typ == "ping"} {
			if got, err := e.Eval(src, act); err != nil || got != want {
				t.Errorf("%s %s: %s = %v (%v), want %v", c.trigger, c.typ, src, got, err, want)
			}
		}
	}
}

// fakeWebhooks is Payrolla's /connect/webhooks, enough for the registrar.
type fakeWebhooks struct {
	mu      sync.Mutex
	hooks   []webhook
	created int
	deleted []string
}

func (f *fakeWebhooks) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer pyk_live_x" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/connect/webhooks":
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"webhooks": f.hooks}})
	case r.Method == http.MethodPost && r.URL.Path == "/connect/webhooks":
		var in struct {
			URL        string   `json:"url"`
			EventTypes []string `json:"event_types"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.created++
		h := webhook{ID: "wh-" + string(rune('0'+f.created)), URL: in.URL, EventTypes: in.EventTypes, Status: "ACTIVE"}
		f.hooks = append(f.hooks, h)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"webhook": h, "signing_secret": "whsec_" + h.ID}})
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/connect/webhooks/"):
		id := strings.TrimPrefix(r.URL.Path, "/connect/webhooks/")
		for i, h := range f.hooks {
			if h.ID == id {
				f.hooks = append(f.hooks[:i], f.hooks[i+1:]...)
				f.deleted = append(f.deleted, id)
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"deleted": true}})
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":"NOT_FOUND","message":"no such subscription"}}`)
	default:
		w.WriteHeader(http.StatusTeapot)
	}
}

// The registrar creates a subscription for the workflow's events (never
// ping), keeps one that matches, replaces one that does not (Payrolla
// cannot change a subscription), maps a switched-off one to paused,
// deletes an orphan Find recognises by its URL rather than adopt a
// subscription whose secret was never kept, and deletes idempotently.
func TestRegistrar(t *testing.T) {
	f := &fakeWebhooks{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := New(Options{BaseURL: srv.URL})
	r := c.Registrars["payout_event"]
	call := connector.RemoteCall{Credentials: map[string]string{"api_key": "pyk_live_x"}, HTTP: srv.Client()}
	ctx := context.Background()
	url := "https://hooks.taskiem.test/t1/connectors/payrolla%401/payout_event?env=prod&subscription=sub-1"
	spec := connector.RemoteSpec{Name: "taskiem-sub-1", URL: url, Events: []string{"payout.failed", "ping"}}

	st, err := r.Create(ctx, call, spec)
	if err != nil || st.ID != "wh-1" || st.Secret != "whsec_wh-1" || st.Status != connector.RemoteHealthy {
		t.Fatalf("create %+v %v", st, err)
	}
	if got := f.hooks[0].EventTypes; len(got) != 1 || got[0] != "payout.failed" {
		t.Errorf("event types %v", got)
	}
	if st, err := r.Update(ctx, call, "wh-1", spec); err != nil || st.ID != "wh-1" || f.created != 1 {
		t.Errorf("an unchanged update: %+v %v (created %d)", st, err, f.created)
	}
	all := connector.RemoteSpec{Name: spec.Name, URL: url} // no events: every payout event
	if _, err := r.Update(ctx, call, "wh-1", all); !errors.Is(err, connector.ErrNotFound) || len(f.hooks) != 0 {
		t.Fatalf("a changed update is not replaced: %v %v", err, f.hooks)
	}
	if st, err = r.Create(ctx, call, all); err != nil || len(f.hooks[0].EventTypes) != 3 {
		t.Fatalf("recreate %+v %v %v", st, err, f.hooks)
	}

	f.hooks[0].Status, f.hooks[0].DisabledReason = "DISABLED", "failed for 3 days"
	if st, err := r.Get(ctx, call, st.ID); err != nil || st.Status != connector.RemotePaused || st.Reason != "failed for 3 days" {
		t.Errorf("get %+v %v", st, err)
	}
	if _, err := r.Get(ctx, call, "wh-9"); !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("get missing: %v", err)
	}

	// An orphan (created, id never recorded) is deleted, not adopted.
	if _, ok, err := r.Find(ctx, call, "taskiem-sub-1"); ok || err != nil || len(f.hooks) != 0 {
		t.Errorf("find %v %v %v", ok, err, f.hooks)
	}
	if _, ok, err := r.Find(ctx, call, "taskiem-sub-2"); ok || err != nil {
		t.Errorf("find none %v %v", ok, err)
	}
	if err := r.Delete(ctx, call, "wh-1"); err != nil {
		t.Errorf("deleting a gone subscription: %v", err)
	}
	if _, err := r.Create(ctx, call, connector.RemoteSpec{Name: "taskiem-sub-3", URL: url, Events: []string{"ping"}}); err == nil {
		t.Error("a subscription to ping alone was created")
	}
}

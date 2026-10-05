// Package e2e runs the dogfood workflows end to end: real engine, real
// Postgres, first-party connectors against fake provider servers.
package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/wd"
)

var ctx = context.Background()

func TestTopupReconciliationExample(t *testing.T) {
	e := rt.New(t)
	paystack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ref := strings.TrimPrefix(r.URL.Path, "/transaction/verify/")
		status, amount := map[string]string{"PSK_paid": "success", "PSK_short": "success", "PSK_failed": "failed", "PSK_wait": "pending"}[ref], 50000
		if ref == "PSK_short" {
			amount = 40000
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": true, "data": map[string]any{"reference": ref, "status": status, "amount": amount, "currency": "NGN"}})
	}))
	defer paystack.Close()
	var mu sync.Mutex
	calls := map[string][]string{}
	ispend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.URL.Path] = append(calls[r.URL.Path], r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ispend.Close()
	if err := builtin.Register(e.Registry, builtin.Options{PaystackURL: paystack.URL}); err != nil {
		t.Fatal(err)
	}

	// iSpend's database, reached through the postgres connector.
	if _, err := e.DB.Admin.Exec(ctx, `CREATE TABLE topups (id text PRIMARY KEY, paystack_reference text, amount_kobo bigint, wallet_id text, status text, created_at timestamptz);
		INSERT INTO topups VALUES
		 ('t1', 'PSK_paid',   50000, 'w1', 'pending', now() - interval '1 hour'),
		 ('t2', 'PSK_short',  50000, 'w2', 'pending', now() - interval '1 hour'),
		 ('t3', 'PSK_failed', 50000, 'w3', 'pending', now() - interval '1 hour'),
		 ('t4', 'PSK_wait',   50000, 'w4', 'pending', now() - interval '1 hour'),
		 ('t5', 'PSK_new',    50000, 'w5', 'pending', now())`); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(e.DB.DSN)
	pw, _ := u.User.Password()
	if _, err := e.Vault.CreateConnection(ctx, e.Tenant, "prod", "postgres", "ispend_readonly", "custom", map[string]string{
		"host": u.Hostname(), "port": u.Port(), "database": strings.TrimPrefix(u.Path, "/"), "user": u.User.Username(), "password": pw, "sslmode": "disable"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Vault.CreateConnection(ctx, e.Tenant, "prod", "paystack", "main", "api_key", map[string]string{"secret_key": "sk_test_x"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.SetVariable(ctx, e.Tenant, "prod", "ispend_api_url", ispend.URL); err != nil {
		t.Fatal(err)
	}
	e.Secrets["ispend_api_token"] = "tok"
	if err := e.Store.AllowEgress(ctx, e.Tenant, "prod", "127.0.0.1", "admin"); err != nil {
		t.Fatal(err)
	}

	doc, err := os.ReadFile("../flows/examples/topup-reconciliation-postgres-paystack.wd.json")
	if err != nil {
		t.Fatal(err)
	}
	wf := e.Publish(t, string(doc))
	ref, _, err := e.Store.StartRun(ctx, runtime.StartRequest{TenantID: e.Tenant, WorkflowID: wf, Version: 1, Environment: "prod", Trigger: map[string]any{"scheduled_for": time.Now().UTC().Format(time.RFC3339)}})
	if err != nil {
		t.Fatal(err)
	}
	rt.WaitFor(t, 30*time.Second, "reconciliation run", func() bool { e.Drain(t); return e.Status(t, ref) != "running" })
	h, _ := e.Store.RunHistory(ctx, ref)
	if st := e.Status(t, ref); st != "completed" {
		var b strings.Builder
		for _, ev := range h {
			b.WriteString(ev.Type + "(" + ev.StepID + ") " + string(ev.Payload) + "\n")
		}
		t.Fatalf("status %s:\n%s", st, b.String())
	}
	mu.Lock()
	defer mu.Unlock()
	want := map[string]int{"/internal/topups/t1/complete": 1, "/internal/topups/t2/flag": 1, "/internal/topups/t3/fail": 1}
	for path, n := range want {
		if len(calls[path]) != n || !strings.HasPrefix(calls[path][0], "tsk_") {
			t.Errorf("%s called %v", path, calls[path])
		}
	}
	if len(calls) != len(want) {
		t.Errorf("unexpected iSpend calls: %v (t4 is still pending, t5 is too new)", calls)
	}
	def, _ := wd.Load(doc)
	if err := decide.Verify(def, h); err != nil {
		t.Errorf("replay: %v", err)
	}
}

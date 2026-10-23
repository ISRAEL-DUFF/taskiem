package remote_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/remote"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/wd"
)

// fakePayrolla is Payrolla's /connect/webhooks: it keeps each
// subscription's URL, events and signing secret, and can deliver an event
// to a subscription signed as Payrolla signs.
type fakePayrolla struct {
	*httptest.Server
	mu    sync.Mutex
	n     int
	hooks map[string]map[string]any // id -> {url, event_types, secret}
}

func newFakePayrolla(t *testing.T) *fakePayrolla {
	f := &fakePayrolla{hooks: map[string]map[string]any{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer pyk_live_acme" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"code":"API_KEY_INVALID","message":"revoked"}}`)
			return
		}
		view := func(id string) map[string]any {
			h := f.hooks[id]
			return map[string]any{"id": id, "url": h["url"], "event_types": h["event_types"], "status": "ACTIVE", "created_at": "2026-10-23T09:00:00Z"}
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/connect/webhooks":
			var list []map[string]any
			for id := range f.hooks {
				list = append(list, view(id))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"webhooks": list}})
		case r.Method == http.MethodPost && r.URL.Path == "/connect/webhooks":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.n++
			id := "7c1d0000-0000-4000-8000-00000000000" + strconv.Itoa(f.n)
			secret := "whsec_" + strconv.Itoa(f.n)
			f.hooks[id] = map[string]any{"url": in["url"], "event_types": in["event_types"], "secret": secret}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"webhook": view(id), "signing_secret": secret}})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/connect/webhooks/"):
			id := strings.TrimPrefix(r.URL.Path, "/connect/webhooks/")
			if _, ok := f.hooks[id]; !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"error":{"code":"NOT_FOUND","message":"no such subscription"}}`)
				return
			}
			delete(f.hooks, id)
			_, _ = io.WriteString(w, `{"data":{"deleted":true}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// only is the one subscription Payrolla holds.
func (f *fakePayrolla) only(t *testing.T) (url string, events []any, secret string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.hooks) != 1 {
		t.Fatalf("payrolla holds %d subscriptions", len(f.hooks))
	}
	for _, h := range f.hooks {
		ev, _ := h["event_types"].([]any)
		return h["url"].(string), ev, h["secret"].(string)
	}
	return "", nil, ""
}

// deliver posts an event to url signed with secret, as Payrolla does.
func deliver(t *testing.T, url, secret string, body []byte) int {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "."))
	m.Write(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Payrolla-Signature", "t="+ts+",v1="+hex.EncodeToString(m.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// A workflow on Payrolla's payout events (decision 0044 R5): deploying it
// subscribes Payrolla to exactly its events with the company's key; a
// signed payout.failed starts one run however often Payrolla repeats it;
// a forged one is refused; and undeploying deletes the subscription.
func TestPayrollaPayoutFailedStartsOnce(t *testing.T) {
	e := rt.New(t)
	f := newFakePayrolla(t)
	if err := e.Registry.Register(payrolla.New(payrolla.Options{BaseURL: f.URL})); err != nil {
		t.Fatal(err)
	}
	err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO environments (tenant_id, name) VALUES ($1, 'prod')`, e.Tenant)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Vault.CreateConnection(ctx, e.Tenant, "prod", "payrolla", "acme", "api_key", map[string]string{"api_key": "pyk_live_acme"}, "test"); err != nil {
		t.Fatal(err)
	}
	h := &ingest.Handler{Store: e.Store, Secrets: e.Vault, Connections: e.Vault, Registry: e.Registry, Egress: e.Egress}
	mux := http.NewServeMux()
	mux.Handle("/hooks/", http.StripPrefix("/hooks", h))
	edge := httptest.NewServer(mux)
	defer edge.Close()
	rec := &remote.Reconciler{Pool: e.DB.App, Vault: e.Vault, Registry: e.Registry, Egress: e.Egress, HooksURL: edge.URL + "/hooks"}

	doc := `{"schema":"wd/v1","id":"wf_payoutFailed","version":1,"name":"payout failed",
	  "trigger":{"type":"connector_event","config":{"connector":"payrolla@1","trigger":"payout_event","connection":"acme","events":["payout.failed"]}},
	  "steps":[{"id":"say","type":"transform","config":{"output":"='Not paid: ' + trigger.body.data.reason"}}]}`
	wf := e.Publish(t, doc)
	def, err := wd.Load([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	deploy := func() {
		t.Helper()
		if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
			return ingest.SyncEnvironments(ctx, tx, e.Tenant, wf, []string{"prod"}, 1, def, e.Registry, time.Now())
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := rec.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	deploy()
	url, events, secret := f.only(t)
	if len(events) != 1 || events[0] != "payout.failed" || !strings.Contains(url, "/connectors/payrolla@1/payout_event?") {
		t.Fatalf("subscription %s %v", url, events)
	}

	body := []byte(`{"id":"01J9ZB00000000000000000001","type":"payout.failed","api_version":"2026-11-01","created_at":"2026-10-28T10:01:12Z","company_id":"co",
	  "data":{"run_id":"run-1","payslip_id":"ps-1","employee_id":"emp-1","amount_kobo":18500000,"method":"BANK","reference":"NIP-1","reason":"beneficiary account is dormant"}}`)
	for range 2 {
		if st := deliver(t, url, secret, body); st/100 != 2 {
			t.Fatalf("delivery: %d", st)
		}
	}
	if st := deliver(t, url, "whsec_forged", body); st != http.StatusUnauthorized {
		t.Errorf("a forged delivery: %d", st)
	}
	var n int
	if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM runs WHERE workflow_id = $1`, wf).Scan(&n)
	}); err != nil || n != 1 {
		t.Fatalf("runs %d %v", n, err)
	}

	// Undeployed: the subscription is deleted at Payrolla.
	if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM triggers WHERE workflow_id = $1`, wf); err != nil {
			return err
		}
		return remote.Release(ctx, tx, wf, []string{"prod"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rec.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	left := len(f.hooks)
	f.mu.Unlock()
	if left != 0 {
		t.Errorf("%d subscriptions left at Payrolla", left)
	}
}

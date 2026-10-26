package remote_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/remote"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/wd"
)

// A provider that holds a subscription only for a while (Zoho CRM's
// watches) and is asked again before it lapses.
const watchManifest = `manifest: connector/v1
id: watch
version: 1.0.0
name: Watch
category: productivity
auth:
  type: api_key
  fields: [{ key: token, label: Token, secret: true }]
base_url: https://watch.example
actions:
  ping: { title: Ping, class: read, input: { type: object } }
triggers:
  changed:
    type: webhook
    registration: remote
    verify: { scheme: hmac_sha256, header: X-Watch-Signature }
`

// watches is the provider's side: each subscription lasts two hours from
// when it was created or last renewed.
type watches struct {
	mu      sync.Mutex
	now     func() time.Time
	expires map[string]time.Time
	renewed int
}

func (p *watches) Create(_ context.Context, _ connector.RemoteCall, spec connector.RemoteSpec) (connector.RemoteState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expires["w1"] = p.now().Add(2 * time.Hour)
	return connector.RemoteState{ID: "w1", Name: spec.Name, Secret: "sig", Status: connector.RemoteHealthy, ExpiresAt: p.expires["w1"]}, nil
}

func (p *watches) Update(_ context.Context, _ connector.RemoteCall, id string, spec connector.RemoteSpec) (connector.RemoteState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.renewed++
	p.expires[id] = p.now().Add(2 * time.Hour)
	return connector.RemoteState{ID: id, Name: spec.Name, Status: connector.RemoteHealthy, ExpiresAt: p.expires[id]}, nil
}

// Get reads no expiry, as some providers' reads do not.
func (p *watches) Get(context.Context, connector.RemoteCall, string) (connector.RemoteState, error) {
	return connector.RemoteState{ID: "w1", Status: connector.RemoteHealthy}, nil
}

func (p *watches) Find(context.Context, connector.RemoteCall, string) (connector.RemoteState, bool, error) {
	return connector.RemoteState{}, false, nil
}

func (p *watches) RotateSecret(context.Context, connector.RemoteCall, string) (string, error) {
	return "sig2", nil
}

func (p *watches) Delete(context.Context, connector.RemoteCall, string) error { return nil }

func TestExpiringSubscriptionsAreRenewed(t *testing.T) {
	e := rt.New(t)
	p := &watches{now: time.Now, expires: map[string]time.Time{}}
	m := connector.MustParse([]byte(watchManifest))
	ping := connector.ActionFunc(func(context.Context, connector.Request) (connector.Response, error) { return connector.Response{}, nil })
	if err := e.Registry.Register(&connector.Connector{Manifest: m, Actions: map[string]connector.Action{"ping": ping}, Registrars: map[string]connector.Registrar{"changed": p}}); err != nil {
		t.Fatal(err)
	}
	if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO environments (tenant_id, name) VALUES ($1, 'prod')`, e.Tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Vault.CreateConnection(ctx, e.Tenant, "prod", "watch", "main", "api_key", map[string]string{"token": "t"}, "test"); err != nil {
		t.Fatal(err)
	}
	doc := `{"schema":"wd/v1","id":"wf_watch","version":1,"name":"watch","trigger":{"type":"connector_event","config":{"connector":"watch@1","trigger":"changed"}},
	  "steps":[{"id":"a","type":"transform","config":{"output":1}}]}`
	wf := e.Publish(t, doc)
	def, err := wd.Load([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		return ingest.SyncEnvironments(ctx, tx, e.Tenant, wf, []string{"prod"}, 1, def, e.Registry, time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	rec := &remote.Reconciler{Pool: e.DB.App, Vault: e.Vault, Registry: e.Registry, Egress: e.Egress, HooksURL: "https://hooks.example/hooks"}
	tick := func() {
		t.Helper()
		if _, err := rec.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	read := func() (expires, next *time.Time) {
		t.Helper()
		if err := e.DB.Admin.QueryRow(ctx, `SELECT expires_at, next_attempt_at FROM remote_subscriptions WHERE workflow_id = $1`, wf).Scan(&expires, &next); err != nil {
			t.Fatal(err)
		}
		return expires, next
	}

	tick() // created: lasts two hours, due again in one
	expires, next := read()
	if expires == nil || next == nil || next.Before(time.Now().Add(55*time.Minute)) || next.After(time.Now().Add(65*time.Minute)) {
		t.Fatalf("after create: expires %v, next %v", expires, next)
	}
	// A health check that reads no expiry keeps the renewal planned.
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE remote_subscriptions SET checked_at = now() - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	tick()
	if exp2, next2 := read(); exp2 == nil || next2 == nil || !exp2.Equal(*expires) {
		t.Fatalf("after a health check: expires %v, next %v", exp2, next2)
	}
	if p.renewed != 0 {
		t.Fatalf("renewed %d times before it was due", p.renewed)
	}
	// When it comes due, the subscription is applied again, which renews it.
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE remote_subscriptions SET next_attempt_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	p.now = func() time.Time { return time.Now().Add(time.Hour) }
	tick()
	if p.renewed != 1 {
		t.Fatalf("renewed %d times, want 1", p.renewed)
	}
	if exp3, next3 := read(); exp3 == nil || !exp3.After(*expires) || next3 == nil {
		t.Errorf("after renewal: expires %v (was %v), next %v", exp3, expires, next3)
	}
}

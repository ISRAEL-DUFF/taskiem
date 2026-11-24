package remote_test

import (
	"context"
	"fmt"
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

// slowCreates is a provider whose first Create commits at once and then
// answers late (held until release), and which keeps each subscription's
// current signing secret.
type slowCreates struct {
	mu       sync.Mutex
	n        int
	creates  int
	rotates  int
	subs     map[string]string // id: secret
	names    map[string]string // name: id
	creating chan struct{}
	release  chan struct{}
}

func (p *slowCreates) Create(_ context.Context, _ connector.RemoteCall, spec connector.RemoteSpec) (connector.RemoteState, error) {
	p.mu.Lock()
	p.n++
	p.creates++
	id := fmt.Sprintf("w%d", p.n)
	secret := "S_create_" + id
	p.subs[id], p.names[spec.Name] = secret, id
	first := p.creates == 1
	p.mu.Unlock()
	if first {
		close(p.creating)
		<-p.release
	}
	return connector.RemoteState{ID: id, Name: spec.Name, Secret: secret, Status: connector.RemoteHealthy}, nil
}

func (p *slowCreates) Update(_ context.Context, _ connector.RemoteCall, id string, spec connector.RemoteSpec) (connector.RemoteState, error) {
	return connector.RemoteState{ID: id, Name: spec.Name, Status: connector.RemoteHealthy}, nil
}

func (p *slowCreates) Get(_ context.Context, _ connector.RemoteCall, id string) (connector.RemoteState, error) {
	return connector.RemoteState{ID: id, Status: connector.RemoteHealthy}, nil
}

func (p *slowCreates) Find(_ context.Context, _ connector.RemoteCall, name string) (connector.RemoteState, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	id, ok := p.names[name]
	return connector.RemoteState{ID: id, Name: name}, ok, nil
}

func (p *slowCreates) RotateSecret(_ context.Context, _ connector.RemoteCall, id string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rotates++
	s := fmt.Sprintf("S_rotated_%s_%d", id, p.rotates)
	p.subs[id] = s
	return s, nil
}

func (p *slowCreates) Delete(context.Context, connector.RemoteCall, string) error { return nil }

// TestConcurrentPassesKeepTheProvidersSecret: a pass whose lease ran out
// while a provider call was slow, and that another pass took over, records
// nothing, so the secret kept is the one the provider signs with (F20).
func TestConcurrentPassesKeepTheProvidersSecret(t *testing.T) {
	e := rt.New(t)
	p := &slowCreates{subs: map[string]string{}, names: map[string]string{}, creating: make(chan struct{}), release: make(chan struct{})}
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

	// Pass 1 (a tick) is inside a slow Create when its lease runs out.
	done := make(chan error, 1)
	go func() { _, err := rec.Tick(ctx); done <- err }()
	<-p.creating
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE remote_subscriptions SET lease_until = now() - interval '1 second' WHERE workflow_id = $1`, wf); err != nil {
		t.Fatal(err)
	}
	// Pass 2 (a connection check in the API) takes the row over.
	if err := rec.ReconcileTenant(ctx, e.Tenant); err != nil {
		t.Fatal(err)
	}
	close(p.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	var subID uuid.UUID
	var remoteID *string
	if err := e.DB.Admin.QueryRow(ctx, `SELECT id, remote_id FROM remote_subscriptions WHERE workflow_id = $1`, wf).Scan(&subID, &remoteID); err != nil {
		t.Fatal(err)
	}
	if remoteID == nil {
		t.Fatal("no subscription recorded")
	}
	stored, err := e.Vault.Get(ctx, e.Tenant, remote.SecretEnv, remote.SecretName(subID))
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	signs := p.subs[*remoteID]
	p.mu.Unlock()
	if stored != signs {
		t.Errorf("kept secret %q, but the provider signs %s with %q", stored, *remoteID, signs)
	}
	if p.creates != 1 {
		t.Errorf("%d subscriptions created, want 1", p.creates)
	}
}

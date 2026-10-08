package runtimetest

import (
	"context"
	"crypto/sha256"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/container"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/wasmconn"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Env is an engine on a throwaway database with the fakepay connector.
type Env struct {
	DB       *dbtest.DB
	Store    *runtime.Store
	Registry *connector.Registry
	Provider *Provider
	Tenant   uuid.UUID
	Secrets  runtime.MapSecrets
	// VaultSecrets makes workers read workflow secrets from Vault (which
	// records each read) instead of Secrets.
	VaultSecrets bool
	Vault        *secrets.Vault
	// Connectors loads the tenant's own WebAssembly connectors.
	Connectors *wasmconn.Source
	// Egress allows loopback so tests can reach httptest servers; every
	// other non-public address is still refused.
	Egress *egress.Guard
	// Containers runs container steps for Drain's container worker (nil:
	// they fail as not enabled); Proxy and ProxyAddr give them egress.
	Containers container.Runner
	Proxy      *egress.Proxy
	ProxyAddr  string
}

// New creates an engine environment; it skips without a test database.
func New(t testing.TB) *Env {
	t.Helper()
	d := dbtest.New(t)
	reg := connector.NewRegistry()
	prov := NewProvider()
	if err := reg.Register(prov.Connector()); err != nil {
		t.Fatal(err)
	}
	tn := d.SeedTenant(t, nil)
	kms, err := secrets.NewLocalKMS(map[string][]byte{"root": make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	wrt, err := wasmconn.New(context.Background(), wasmconn.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wrt.Close(context.Background()) })
	src := &wasmconn.Source{Pool: d.AppPool(t, 2), Runtime: wrt}
	reg.SetTenantSource(src.Connectors)
	guard := &egress.Guard{Blocked: func(a netip.Addr) bool { return !a.IsLoopback() && egress.BlockedAddr(a) }}
	vault := &secrets.Vault{Pool: d.App, KMS: kms, RootKey: "root"}
	return &Env{DB: d, Store: &runtime.Store{Pool: d.App, Registry: reg, PII: vault}, Registry: reg, Provider: prov, Tenant: tn.ID,
		Secrets: runtime.MapSecrets{}, Vault: vault, Egress: guard, Connectors: src}
}

// Publish stores a published workflow version and returns its workflow id.
func (e *Env) Publish(t testing.TB, wdJSON string) uuid.UUID {
	t.Helper()
	return e.PublishIn(t, e.Tenant, wdJSON)
}

// AddTenant creates another tenant (no workflows).
func (e *Env) AddTenant(t testing.TB) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	ctx := context.Background()
	err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{id}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO tenants (id, name, plan_id) VALUES ($1, 'other', $1)`, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// PublishIn is Publish in another tenant.
func (e *Env) PublishIn(t testing.TB, tenant uuid.UUID, wdJSON string) uuid.UUID {
	t.Helper()
	if _, err := wd.Load([]byte(wdJSON)); err != nil {
		t.Fatal(err)
	}
	id := uuid.Must(uuid.NewV7())
	sum := sha256.Sum256([]byte(wdJSON))
	ctx := context.Background()
	err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO workflows (id, tenant_id, name, created_by, active_version) VALUES ($1, $2, 'wf', $2, 1)`, id, tenant); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO workflow_versions (workflow_id, version, tenant_id, definition, digest, state) VALUES ($1, 1, $2, $3, $4, 'published')`,
			id, tenant, wdJSON, sum[:])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Start starts a run of version 1 of a workflow.
func (e *Env) Start(t testing.TB, wf uuid.UUID, trigger any) runtime.RunRef {
	t.Helper()
	ref, _, err := e.Store.StartRun(context.Background(), runtime.StartRequest{
		TenantID: e.Tenant, WorkflowID: wf, Version: 1, Environment: "prod", Trigger: trigger,
		Env: map[string]any{"api": "https://api.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// Worker returns a worker for the connector queue.
func (e *Env) Worker(id string) *runtime.Worker {
	var s runtime.Secrets = e.Secrets
	if e.VaultSecrets {
		s = e.Vault
	}
	return &runtime.Worker{Store: e.Store, Registry: e.Registry, Secrets: s, Connections: e.Vault, Egress: e.Egress,
		ID: id, Queue: "connector", Lease: 30 * time.Second, CallTimeout: 200 * time.Millisecond}
}

// ContainerWorker returns a worker for the container queue.
func (e *Env) ContainerWorker(id string) *runtime.Worker {
	w := e.Worker(id)
	w.Queue, w.Containers, w.Proxy, w.ProxyAddr = "container", e.Containers, e.Proxy, e.ProxyAddr
	return w
}

// Scheduler returns a scheduler.
func (e *Env) Scheduler() *runtime.Scheduler {
	return &runtime.Scheduler{Store: e.Store, ID: "sched", Interval: 50 * time.Millisecond}
}

// Drain runs workers and the scheduler synchronously until nothing is left
// to do right now.
func (e *Env) Drain(t testing.TB) {
	t.Helper()
	ctx := context.Background()
	w, s := e.Worker("drain"), e.Scheduler()
	sb := e.Worker("drain-sandbox")
	sb.Queue = "sandbox"
	cw := e.ContainerWorker("drain-container")
	for i := 0; i < 1000; i++ {
		n, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		m, err := sb.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		n += m
		if m, err = cw.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		n += m
		st, err := s.Tick(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 && st.TimersFired == 0 && st.RunsSwept == 0 && st.LeasesRecovered == 0 && st.RunsAdmitted == 0 {
			return
		}
	}
	t.Fatal("drain did not settle")
}

// Status returns a run's status.
func (e *Env) Status(t testing.TB, ref runtime.RunRef) string {
	t.Helper()
	st, err := e.Store.RunStatus(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// WaitFor polls until cond holds or the timeout passes.
func WaitFor(t testing.TB, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

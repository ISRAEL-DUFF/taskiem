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
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
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
	Vault    *secrets.Vault
	// Egress allows loopback so tests can reach httptest servers; every
	// other non-public address is still refused.
	Egress *egress.Guard
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
	guard := &egress.Guard{Blocked: func(a netip.Addr) bool { return !a.IsLoopback() && egress.BlockedAddr(a) }}
	return &Env{DB: d, Store: &runtime.Store{Pool: d.App, Registry: reg}, Registry: reg, Provider: prov, Tenant: tn.ID,
		Secrets: runtime.MapSecrets{}, Vault: &secrets.Vault{Pool: d.App, KMS: kms, RootKey: "root"}, Egress: guard}
}

// Publish stores a published workflow version and returns its workflow id.
func (e *Env) Publish(t testing.TB, wdJSON string) uuid.UUID {
	t.Helper()
	if _, err := wd.Load([]byte(wdJSON)); err != nil {
		t.Fatal(err)
	}
	id := uuid.Must(uuid.NewV7())
	sum := sha256.Sum256([]byte(wdJSON))
	ctx := context.Background()
	err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO workflows (id, tenant_id, name, created_by, active_version) VALUES ($1, $2, 'wf', $2, 1)`, id, e.Tenant); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO workflow_versions (workflow_id, version, tenant_id, definition, digest, state) VALUES ($1, 1, $2, $3, $4, 'published')`,
			id, e.Tenant, wdJSON, sum[:])
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
	return &runtime.Worker{Store: e.Store, Registry: e.Registry, Secrets: e.Secrets, Connections: e.Vault, Egress: e.Egress,
		ID: id, Queue: "connector", Lease: 30 * time.Second, CallTimeout: 200 * time.Millisecond}
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
	for i := 0; i < 1000; i++ {
		n, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		st, err := s.Tick(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 && st.TimersFired == 0 && st.RunsSwept == 0 && st.LeasesRecovered == 0 {
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

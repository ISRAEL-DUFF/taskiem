package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// TestCodeShareHoldsAcrossReplicas: a tenant's share of tenant-code slots
// is shared by every API replica (tenant_code_leases), not granted again
// by each. A slot is freed on release, and a dead replica's lease expires.
func TestCodeShareHoldsAcrossReplicas(t *testing.T) {
	d := dbtest.New(t)
	a, b := d.SeedTenant(t, nil), d.SeedTenant(t, nil)
	replica := func() *codeGate {
		s := &Server{Store: &runtime.Store{Pool: d.App}, CodeLimits: CodeLimits{Concurrency: 4, PerTenant: 2, Wait: 300 * time.Millisecond}}
		s.initCodeLeases()
		g := s.gate()
		g.leases.poll = 20 * time.Millisecond
		return g
	}
	r1, r2 := replica(), replica()
	ctx := context.Background()

	one, err := r1.enter(ctx, a.ID, r1.wait)
	if err != nil {
		t.Fatal(err)
	}
	two, err := r2.enter(ctx, a.ID, r2.wait)
	if err != nil {
		t.Fatal(err)
	}
	// Each replica has room, but the tenant has used its share of 2.
	if _, err := r1.enter(ctx, a.ID, r1.wait); !errors.Is(err, errCodeTenantBusy) {
		t.Fatalf("a third slot on replica 1: %v", err)
	}
	if _, err := r2.enter(ctx, a.ID, r2.wait); !errors.Is(err, errCodeTenantBusy) {
		t.Fatalf("a third slot on replica 2: %v", err)
	}
	// Another tenant is unaffected.
	other, err := r2.enter(ctx, b.ID, r2.wait)
	if err != nil {
		t.Fatalf("another tenant: %v", err)
	}
	other()

	// A slot released on one replica is taken by a request waiting on the other.
	go func() { time.Sleep(50 * time.Millisecond); one() }()
	three, err := r2.enter(ctx, a.ID, time.Second)
	if err != nil {
		t.Fatalf("waiting for a slot freed elsewhere: %v", err)
	}
	two()
	three()
	var held int
	_ = d.Admin.QueryRow(ctx, `SELECT count(*) FROM tenant_code_leases WHERE expires_at > now()`).Scan(&held)
	if held != 0 {
		t.Errorf("%d leases left after every release", held)
	}

	// A replica that died holding both slots: its leases expire.
	for slot := range 2 {
		if _, err := d.Admin.Exec(ctx, `INSERT INTO tenant_code_leases (tenant_id, slot, lease_id, expires_at) VALUES ($1, $2, $3, now() + interval '1 hour')
			ON CONFLICT (tenant_id, slot) DO UPDATE SET lease_id = EXCLUDED.lease_id, expires_at = EXCLUDED.expires_at`, a.ID, slot, uuid.New()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r1.enter(ctx, a.ID, r1.wait); !errors.Is(err, errCodeTenantBusy) {
		t.Fatalf("slots held by a dead replica: %v", err)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE tenant_code_leases SET expires_at = now() - interval '1 second' WHERE tenant_id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	four, err := r1.enter(ctx, a.ID, r1.wait)
	if err != nil {
		t.Fatalf("an expired lease was not taken over: %v", err)
	}
	four()
}

// TestCodeShareRenewsLongWork: a lease is renewed while the work runs, so
// long work does not lose its slot to another replica.
func TestCodeShareRenewsLongWork(t *testing.T) {
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	s := &Server{Store: &runtime.Store{Pool: d.App}, CodeLimits: CodeLimits{Concurrency: 2, PerTenant: 1, Wait: 100 * time.Millisecond}}
	s.initCodeLeases()
	g := s.gate()
	g.leases.ttl = 400 * time.Millisecond
	release, err := g.enter(context.Background(), a.ID, g.wait)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	time.Sleep(time.Second) // more than twice the lease
	other := &Server{Store: &runtime.Store{Pool: d.App}, CodeLimits: CodeLimits{Concurrency: 2, PerTenant: 1, Wait: 100 * time.Millisecond}}
	other.initCodeLeases()
	if _, err := other.gate().enter(context.Background(), a.ID, 100*time.Millisecond); !errors.Is(err, errCodeTenantBusy) {
		t.Errorf("a renewed lease was taken by another replica: %v", err)
	}
}

// TestCodeSharePerReplica: TASKIEM_TENANT_CODE_SHARE=replica keeps the
// share in memory.
func TestCodeSharePerReplica(t *testing.T) {
	s := &Server{Store: &runtime.Store{}, CodeLimits: CodeLimits{PerReplica: true}}
	s.initCodeLeases()
	if s.gate().leases != nil {
		t.Error("per-replica share uses leases")
	}
}

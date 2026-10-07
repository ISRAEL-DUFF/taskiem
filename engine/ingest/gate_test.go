package ingest

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

// The limiter map stays bounded however many tenant ids are tried.
func TestTenantGateEvicts(t *testing.T) {
	g := &tenantGate{entries: map[uuid.UUID]*gateEntry{}}
	now := time.Now()
	known := uuid.New()
	g.entries[known] = &gateEntry{lim: rate.NewLimiter(1, 1), checked: now, seen: now}
	idle := uuid.New()
	g.entries[idle] = &gateEntry{lim: rate.NewLimiter(1, 1), checked: now, seen: now.Add(-time.Hour)}
	for len(g.entries) < gateMax {
		g.entries[uuid.New()] = &gateEntry{checked: now, seen: now} // made-up ids
	}
	g.evict(now)
	if len(g.entries) >= gateMax {
		t.Fatalf("%d entries after eviction", len(g.entries))
	}
	if _, ok := g.entries[idle]; ok {
		t.Error("idle tenant kept")
	}
	if _, ok := g.entries[known]; !ok {
		t.Error("an active tenant was evicted before unknown ids")
	}
}

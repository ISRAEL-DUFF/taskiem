package ingest

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// The limiter map stays bounded however many tenant ids are tried.
func TestTenantGateEvicts(t *testing.T) {
	g := &tenantGate{entries: map[uuid.UUID]*gateEntry{}}
	now := time.Now()
	known := uuid.New()
	g.entries[known] = &gateEntry{hard: rate.NewLimiter(1, 1), checked: now, seen: now}
	idle := uuid.New()
	g.entries[idle] = &gateEntry{hard: rate.NewLimiter(1, 1), checked: now, seen: now.Add(-time.Hour)}
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

// Made-up tenant ids cannot spend a known tenant's re-check: once the
// shared lookup budget is gone, a tenant the gate already knows is still
// let through, refreshed from its own budget or on its last answer, and
// only unknown ids are told to retry (F29).
func TestGateKnownTenantSurvivesFlood(t *testing.T) {
	e := runtimetest.New(t)
	h := &Handler{Store: e.Store, Secrets: e.Vault, Connections: e.Vault, Registry: e.Registry}
	post := func(tenant uuid.UUID) int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/"+tenant.String()+"/x", strings.NewReader(`{}`)))
		return w.Code
	}
	if c := post(e.Tenant); c == http.StatusTooManyRequests {
		t.Fatalf("first delivery %d", c)
	}
	h.tenants.mu.Lock()
	h.tenants.entries[e.Tenant].checked = time.Now().Add(-2 * gateTTL)
	h.tenants.mu.Unlock()
	codes := map[int]int{}
	for range 400 {
		codes[post(uuid.New())]++
	}
	if codes[http.StatusTooManyRequests] == 0 {
		t.Fatalf("made-up ids were never turned away: %v", codes)
	}
	if c := post(e.Tenant); c == http.StatusTooManyRequests {
		t.Errorf("a known tenant's delivery during the flood got %d", c)
	}
	// With every budget spent, the known tenant keeps its last answer
	// without a lookup; an unknown id is still told to retry.
	h.tenants.mu.Lock()
	h.tenants.entries[e.Tenant].checked = time.Now().Add(-2 * gateTTL)
	h.tenants.lookups = rate.NewLimiter(gateLookup, 0)
	if h.tenants.refresh != nil {
		h.tenants.refresh = rate.NewLimiter(gateLookup, 0)
	}
	h.tenants.mu.Unlock()
	h.Store = nil // a lookup would panic
	if g, err := h.gate(t.Context(), e.Tenant); err != nil || g.hard == nil {
		t.Errorf("known tenant with spent budgets: %v %v", g.hard, err)
	}
	if _, err := h.gate(t.Context(), uuid.New()); !errors.Is(err, errBusy) {
		t.Errorf("unknown id with spent budgets: %v", err)
	}
}

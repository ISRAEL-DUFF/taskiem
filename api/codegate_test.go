package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Tenant code in the API process is bounded: per tenant first, so one
// tenant cannot hold every slot, then for the process (self-review S34).
func TestCodeGateBoundsTenantsAndProcess(t *testing.T) {
	s := &Server{CodeLimits: CodeLimits{Concurrency: 3, PerTenant: 2, Wait: 30 * time.Millisecond}}
	g := s.gate()
	ctx := context.Background()
	a, b, c := uuid.New(), uuid.New(), uuid.New()

	r1, err := g.enter(ctx, a, g.wait)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := g.enter(ctx, a, g.wait)
	if err != nil {
		t.Fatal(err)
	}
	// Tenant a holds its share: a third is refused as the tenant's.
	if _, err := g.enter(ctx, a, g.wait); !errors.Is(err, errCodeTenantBusy) {
		t.Fatalf("a third slot for one tenant: %v", err)
	}
	// Another tenant still gets the slot left.
	r3, err := g.enter(ctx, b, g.wait)
	if err != nil {
		t.Fatalf("another tenant was starved: %v", err)
	}
	// Now the process is full.
	if _, err := g.enter(ctx, c, g.wait); !errors.Is(err, errCodeBusy) {
		t.Fatalf("a full process admitted more: %v", err)
	}
	// A slot freed while waiting is taken.
	go func() { time.Sleep(5 * time.Millisecond); r1() }()
	r4, err := g.enter(ctx, c, time.Second)
	if err != nil {
		t.Fatalf("a freed slot was not taken: %v", err)
	}
	r1() // releasing twice is harmless
	for _, r := range []func(){r2, r3, r4} {
		r()
	}
	if len(g.slots) != 0 || len(g.tenants) != 0 {
		t.Errorf("slots leaked: %d in use, tenants %v", len(g.slots), g.tenants)
	}
}

func TestTenantCodeAnswers429And503(t *testing.T) {
	s := &Server{CodeLimits: CodeLimits{Concurrency: 2, PerTenant: 1, Wait: 20 * time.Millisecond}}
	inside := make(chan struct{})
	done := make(chan struct{})
	h := s.tenantCode(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !holdsCodeSlot(r.Context()) {
			t.Error("the handler's context does not hold the slot")
		}
		inside <- struct{}{}
		<-done
		w.WriteHeader(http.StatusOK)
	}))
	call := func(tenant uuid.UUID) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/v1/code/compile", nil)
		h.ServeHTTP(rec, req.WithContext(context.WithValue(req.Context(), ctxKey{}, &Principal{TenantID: tenant})))
		return rec
	}
	a, b := uuid.New(), uuid.New()
	go call(a)
	<-inside
	if rec := call(a); rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("same tenant over its share: %d %s", rec.Code, rec.Body)
	}
	go call(b)
	<-inside
	if rec := call(uuid.New()); rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Errorf("full process: %d %s", rec.Code, rec.Body)
	}
	close(done)
}

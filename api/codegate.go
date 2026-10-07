package api

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/telemetry"
)

// Tenant code in the API process (self-review S34): compiling flow code,
// checking code steps (a Python check runs the interpreter), generating
// code, and checking catalogue submissions and uploaded connectors. Each
// is bounded on its own (sandbox memory and time limits); the gate bounds
// how many run at once in a process, and how many of those one tenant
// holds, so one tenant cannot take every slot.

// codeGate admits tenant code: at most total at once in the process, at
// most per for one tenant.
type codeGate struct {
	once  sync.Once
	total int
	per   int
	wait  time.Duration
	slots chan struct{}

	mu      sync.Mutex
	tenants map[uuid.UUID]int
	freed   chan struct{} // closed and replaced whenever a tenant's slot frees
}

var (
	// errCodeTenantBusy: the tenant already runs its share; 429.
	errCodeTenantBusy = errors.New("your organisation is already compiling or checking as much code as it may at once; try again in a moment")
	// errCodeBusy: the process runs as much tenant code as it may; 503.
	errCodeBusy = errors.New("code checks are busy; try again in a moment")
)

// CodeLimits are the gate's settings (TASKIEM_TENANT_CODE_CONCURRENCY,
// TASKIEM_TENANT_CODE_PER_TENANT); zero values take the defaults.
type CodeLimits struct {
	Concurrency int           // at once in the process; default max(2, CPUs)
	PerTenant   int           // at once for one tenant; default max(1, Concurrency/2)
	Wait        time.Duration // how long a request waits for a slot; default 5s
}

func (g *codeGate) init(l CodeLimits) {
	g.once.Do(func() {
		g.total = l.Concurrency
		if g.total <= 0 {
			g.total = max(2, runtime.NumCPU())
		}
		g.per = l.PerTenant
		if g.per <= 0 {
			g.per = max(1, g.total/2)
		}
		g.per = min(g.per, g.total)
		g.wait = l.Wait
		if g.wait <= 0 {
			g.wait = 5 * time.Second
		}
		g.slots = make(chan struct{}, g.total)
		g.tenants = map[uuid.UUID]int{}
		g.freed = make(chan struct{})
	})
}

// enter takes a slot for tenant, waiting up to wait (and while ctx lives).
// It returns the release, or errCodeTenantBusy or errCodeBusy.
func (g *codeGate) enter(ctx context.Context, tenant uuid.UUID, wait time.Duration) (func(), error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	// The tenant's share first, so a tenant waiting on its own share holds
	// no process slot.
	for {
		g.mu.Lock()
		if g.tenants[tenant] < g.per {
			g.tenants[tenant]++
			g.mu.Unlock()
			break
		}
		freed := g.freed
		g.mu.Unlock()
		select {
		case <-freed:
		case <-timer.C:
			return nil, errCodeTenantBusy
		case <-ctx.Done():
			return nil, errCodeTenantBusy
		}
	}
	leaveTenant := func() {
		g.mu.Lock()
		if g.tenants[tenant]--; g.tenants[tenant] <= 0 {
			delete(g.tenants, tenant)
		}
		close(g.freed)
		g.freed = make(chan struct{})
		g.mu.Unlock()
	}
	select {
	case g.slots <- struct{}{}:
	case <-timer.C:
		leaveTenant()
		return nil, errCodeBusy
	case <-ctx.Done():
		leaveTenant()
		return nil, errCodeBusy
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-g.slots
			leaveTenant()
		})
	}, nil
}

type codeSlotKey struct{}

// holdsCodeSlot reports whether ctx's request already holds a slot.
func holdsCodeSlot(ctx context.Context) bool { return ctx.Value(codeSlotKey{}) != nil }

func (s *Server) gate() *codeGate {
	s.code.init(s.CodeLimits)
	return &s.code
}

// tenantCode admits a request that runs tenant code, answering 429 when
// its tenant already runs its share and 503 when the process is full,
// both with Retry-After.
func (s *Server) tenantCode(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g := s.gate()
		release, err := g.enter(r.Context(), principalFrom(r.Context()).TenantID, g.wait)
		if err != nil {
			s.codeRefused(w, err)
			return
		}
		defer release()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), codeSlotKey{}, true)))
	})
}

func (s *Server) codeRefused(w http.ResponseWriter, err error) {
	w.Header().Set("Retry-After", strconv.Itoa(2))
	if errors.Is(err, errCodeTenantBusy) {
		telemetry.TenantCodeRefused.WithLabelValues("tenant").Inc()
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error(), "code": "tenant_code_busy"})
		return
	}
	telemetry.TenantCodeRefused.WithLabelValues("process").Inc()
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error(), "code": "code_checks_busy"})
}

// withCodeSlot runs fn holding a slot, unless ctx's request already holds
// one. Work outside a request (publishing from Git, AI drafts, repairs,
// WhatsApp building) waits longer, up to a minute.
func (s *Server) withCodeSlot(ctx context.Context, tenant uuid.UUID, fn func()) error {
	if holdsCodeSlot(ctx) {
		fn()
		return nil
	}
	release, err := s.gate().enter(ctx, tenant, time.Minute)
	if err != nil {
		return err
	}
	defer release()
	fn()
	return nil
}

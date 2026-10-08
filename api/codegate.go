package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
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

	// leases makes the per-tenant share hold across replicas; nil keeps it
	// per process (no database, unit tests).
	leases *codeLeases
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
	// PerReplica keeps the per-tenant share in each replica's memory
	// instead of across replicas (TASKIEM_TENANT_CODE_SHARE=replica).
	PerReplica bool
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
	// Then the tenant's share across replicas.
	var lease *heldLease
	if g.leases != nil && tenant != uuid.Nil {
		var err error
		lease, err = g.leases.wait(ctx, tenant, g.per, timer.C)
		if errors.Is(err, errCodeTenantBusy) {
			leaveTenant()
			return nil, err
		}
		if err != nil {
			// The database is unreachable: the per-process share still
			// holds, and the request would fail on its own reads anyway.
			g.leases.logger().Warn("tenant code lease unavailable; per-replica share only", "tenant", tenant, "err", err)
		}
	}
	select {
	case g.slots <- struct{}{}:
	case <-timer.C:
		lease.release()
		leaveTenant()
		return nil, errCodeBusy
	case <-ctx.Done():
		lease.release()
		leaveTenant()
		return nil, errCodeBusy
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-g.slots
			lease.release()
			leaveTenant()
		})
	}, nil
}

// codeLeases holds a tenant's share of tenant-code slots across API
// replicas in tenant_code_leases (migration 00141): slot numbers 0 to
// per-1, each taken for ttl and renewed while the work runs. A replica
// that dies leaves its leases to expire.
type codeLeases struct {
	pool *pgxpool.Pool
	ttl  time.Duration // default 2 minutes
	poll time.Duration // how often a waiting request looks again; default 100ms
	log  *slog.Logger
}

func (l *codeLeases) logger() *slog.Logger {
	if l.log == nil {
		return slog.Default()
	}
	return l.log
}

func (l *codeLeases) lifetime() time.Duration {
	if l.ttl <= 0 {
		return 2 * time.Minute
	}
	return l.ttl
}

// take claims a free (or expired) slot of the tenant's, if there is one.
func (l *codeLeases) take(ctx context.Context, tenant uuid.UUID, per int) (uuid.UUID, bool, error) {
	id := uuid.New()
	got := false
	err := db.InTenantTx(ctx, l.pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var slot int
		err := tx.QueryRow(ctx, `
			WITH free AS (
			  SELECT s FROM generate_series(0, $2 - 1) s
			   WHERE NOT EXISTS (SELECT 1 FROM tenant_code_leases l WHERE l.tenant_id = $1 AND l.slot = s AND l.expires_at > now())
			   ORDER BY s LIMIT 1)
			INSERT INTO tenant_code_leases (tenant_id, slot, lease_id, expires_at)
			SELECT $1, s, $3, now() + $4 * interval '1 millisecond' FROM free
			ON CONFLICT (tenant_id, slot) DO UPDATE SET lease_id = EXCLUDED.lease_id, expires_at = EXCLUDED.expires_at
			 WHERE tenant_code_leases.expires_at <= now()
			RETURNING slot`, tenant, min(per, 1024), id, l.lifetime().Milliseconds()).Scan(&slot)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		got = err == nil
		return err
	})
	return id, got, err
}

// wait takes a slot, looking again every poll until expired fires
// (errCodeTenantBusy) or the database fails.
func (l *codeLeases) wait(ctx context.Context, tenant uuid.UUID, per int, expired <-chan time.Time) (*heldLease, error) {
	poll := l.poll
	if poll <= 0 {
		poll = 100 * time.Millisecond
	}
	raced := false
	for {
		id, ok, err := l.take(ctx, tenant, per)
		if err != nil {
			if ctx.Err() != nil {
				return nil, errCodeTenantBusy
			}
			return nil, err
		}
		if ok {
			return l.hold(tenant, id), nil
		}
		if !raced { // another replica may have taken the slot this one saw free
			raced = true
			continue
		}
		raced = false
		t := time.NewTimer(poll)
		select {
		case <-t.C:
		case <-expired:
			t.Stop()
			return nil, errCodeTenantBusy
		case <-ctx.Done():
			t.Stop()
			return nil, errCodeTenantBusy
		}
	}
}

// heldLease is a taken slot, renewed until released.
type heldLease struct {
	l      *codeLeases
	tenant uuid.UUID
	id     uuid.UUID
	stop   chan struct{}
	once   sync.Once
}

func (l *codeLeases) hold(tenant, id uuid.UUID) *heldLease {
	h := &heldLease{l: l, tenant: tenant, id: id, stop: make(chan struct{})}
	go func() {
		tick := time.NewTicker(l.lifetime() / 4)
		defer tick.Stop()
		for {
			select {
			case <-h.stop:
				return
			case <-tick.C:
				h.exec(`UPDATE tenant_code_leases SET expires_at = now() + $3 * interval '1 millisecond' WHERE tenant_id = $1 AND lease_id = $2`, l.lifetime().Milliseconds())
			}
		}
	}()
	return h
}

func (h *heldLease) exec(q string, args ...any) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := db.InTenantTx(ctx, h.l.pool, []uuid.UUID{h.tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, q, append([]any{h.tenant, h.id}, args...)...)
		return err
	})
	if err != nil {
		h.l.logger().Warn("tenant code lease", "tenant", h.tenant, "err", err)
	}
}

// release frees the slot; nil-safe.
func (h *heldLease) release() {
	if h == nil {
		return
	}
	h.once.Do(func() {
		close(h.stop)
		h.exec(`DELETE FROM tenant_code_leases WHERE tenant_id = $1 AND lease_id = $2`)
	})
}

type codeSlotKey struct{}

// holdsCodeSlot reports whether ctx's request already holds a slot.
func holdsCodeSlot(ctx context.Context) bool { return ctx.Value(codeSlotKey{}) != nil }

func (s *Server) gate() *codeGate {
	s.code.init(s.CodeLimits)
	return &s.code
}

// initCodeLeases makes the per-tenant share cluster-wide when the server
// has a database. Called once from Handler, before requests.
func (s *Server) initCodeLeases() {
	g := s.gate()
	if s.Store != nil && s.Store.Pool != nil && g.leases == nil && !s.CodeLimits.PerReplica {
		g.leases = &codeLeases{pool: s.Store.Pool, log: s.Logger}
	}
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

package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/telemetry"
)

// Scheduler fires timers, recovers expired leases, and sweeps runs left
// with undecided events. Running several is safe: every claim uses SKIP
// LOCKED and every firing is guarded by fired_at, so there is no leader
// election (spec 2.3).
type Scheduler struct {
	Store    *Store
	ID       string
	Interval time.Duration
	Logger   *slog.Logger
	// Archiver receives runs past retention before they are purged; nil
	// disables purging.
	Archiver Archiver
	// SweepOnly limits the scheduler to deciding swept runs: the
	// "orchestrator" role, when it runs apart from the "scheduler" role.
	SweepOnly bool
	// PurgeBatch and PurgeBudget pace the retention purge: runs claimed
	// per batch (default 25), and how long a tick may spend purging
	// before the rest waits for the next tick (default a second; the
	// batch under way when it runs out is finished).
	PurgeBatch  int
	PurgeBudget time.Duration

	lastPartitions, lastPurge, lastReceipts, lastSessions, lastPurgeReport time.Time
	// purgeBehind: the last purge pass ran out of budget with runs left.
	purgeBehind bool
}

// TickStats reports one tick's work.
type TickStats struct {
	TimersFired, LeasesRecovered, RunsSwept int
	// RunsAdmitted counts runs held back by tenant limits and let through.
	RunsAdmitted int
}

func (s *Scheduler) defaults() {
	if s.ID == "" {
		s.ID = "scheduler-" + uuid.NewString()[:8]
	}
	if s.Interval <= 0 {
		s.Interval = 250 * time.Millisecond
	}
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
}

// Run ticks until ctx ends.
func (s *Scheduler) Run(ctx context.Context) error {
	s.defaults()
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	defer s.releaseLeases()
	mode := "scheduler"
	if s.SweepOnly {
		mode = "sweep"
	}
	for {
		_, err := s.Tick(ctx)
		switch {
		case err == nil || onlyItemErrors(err):
			// A timer or run that could not be decided was logged and
			// counted on its own (taskiem_scheduler_item_failures_total);
			// the scheduler itself is keeping up.
			telemetry.SchedulerTick.WithLabelValues(mode).SetToCurrentTime()
		case ctx.Err() == nil:
			s.Logger.Error("scheduler tick failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// releaseLeases hands back the timer and orchestration leases a tick cut
// short by shutdown left behind, so another scheduler picks them up at once.
func (s *Scheduler) releaseLeases() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n int
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_release_leases($1)`, s.ID).Scan(&n); err != nil {
		s.Logger.Warn("could not release leases at shutdown; they expire instead", "scheduler", s.ID, "err", err)
		return
	}
	telemetry.LeasesReleased.Add(float64(n))
}

// Tick does one round of scheduler work.
func (s *Scheduler) Tick(ctx context.Context) (TickStats, error) {
	s.defaults()
	var st TickStats
	var errs []error
	if s.SweepOnly {
		n, err := s.sweep(ctx)
		st.RunsSwept = n
		telemetry.RunsSwept.Add(float64(n))
		return st, err
	}
	n, err := s.fireTimers(ctx)
	st.TimersFired = n
	errs = append(errs, err)
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_recover_expired_leases()`).Scan(&st.LeasesRecovered); err != nil {
		errs = append(errs, err)
	}
	n, err = s.sweep(ctx)
	st.RunsSwept = n
	errs = append(errs, err)
	n, err = s.Store.AdmitQueued(ctx)
	st.RunsAdmitted = n
	errs = append(errs, err)
	if s.purgeBehind || time.Since(s.lastPurge) > purgeEvery { // retention.go
		done, err := s.purgeRetention(ctx)
		s.purgeBehind = err == nil && !done
		if err != nil {
			errs = append(errs, err)
		} else if done {
			s.lastPurge = time.Now()
		}
	}
	if time.Since(s.lastReceipts) > time.Hour {
		if err := s.purgeReceipts(ctx); err != nil {
			errs = append(errs, err)
		} else {
			s.lastReceipts = time.Now()
		}
	}
	if time.Since(s.lastSessions) > time.Hour {
		if err := s.purgeSessions(ctx); err != nil { // retention.go
			errs = append(errs, err)
		} else {
			s.lastSessions = time.Now()
		}
	}
	if time.Since(s.lastPartitions) > time.Hour {
		if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_ensure_run_event_partitions(now(), 3)`); err != nil {
			errs = append(errs, err)
		} else {
			s.lastPartitions = time.Now()
		}
	}
	telemetry.TimersFired.Add(float64(st.TimersFired))
	telemetry.LeasesRecovered.Add(float64(st.LeasesRecovered))
	telemetry.RunsSwept.Add(float64(st.RunsSwept))
	return st, errors.Join(errs...)
}

func (s *Scheduler) fireTimers(ctx context.Context) (int, error) {
	rows, err := s.Store.Pool.Query(ctx, `SELECT timer_id, tenant_id, run_id, COALESCE(step_id, ''), kind,
		GREATEST(extract(epoch FROM clock_timestamp() - fire_at), 0)::float8 FROM taskiem_claim_due_timers($1, 100)`, s.ID)
	if err != nil {
		return 0, err
	}
	type due struct {
		id, tenant, run uuid.UUID
		step, kind      string
		late            float64
	}
	var timers []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.tenant, &d.run, &d.step, &d.kind, &d.late); err != nil {
			rows.Close()
			return 0, err
		}
		timers = append(timers, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	fired := 0
	var errs []error
	for _, d := range timers {
		done := false
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{d.tenant}, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `UPDATE timers SET fired_at = now() WHERE id = $1 AND fired_at IS NULL`, d.id)
			if err != nil || tag.RowsAffected() == 0 {
				return err
			}
			run, err := lockRun(ctx, tx, d.run)
			if err != nil {
				return err
			}
			switch run.status {
			case "completed", "failed", "cancelled":
				return nil
			}
			if _, err := appendEvent(ctx, tx, d.run, history.TimerFired, d.step, 0, history.TimerPayload{Kind: d.kind, TimerID: d.id.String()}, history.OriginScheduler); err != nil {
				return err
			}
			if err := s.Store.decideInline(ctx, tx, run.ref); err != nil {
				return err
			}
			done = true
			return nil
		})
		if err != nil {
			if ctx.Err() != nil {
				return fired, errors.Join(append(errs, err)...)
			}
			// One run that cannot be decided must not hold up the timers
			// claimed with it (other tenants' among them): note it, put it
			// back for later, and go on.
			errs = append(errs, s.failedItem(ctx, "timer", d.tenant, `UPDATE timers SET lease_until = now() + $3::interval WHERE id = $1 AND lease_owner = $2 AND fired_at IS NULL`, d.id,
				fmt.Errorf("timer %s of run %s: %w", d.id, d.run, err)))
			continue
		}
		if done {
			fired++
			telemetry.SchedulerLateness.WithLabelValues("timer").Observe(d.late)
		}
	}
	return fired, errors.Join(errs...)
}

// itemError is one timer or run a tick could not decide, while the rest of
// its batch went on.
type itemError struct{ err error }

func (e *itemError) Error() string { return e.err.Error() }
func (e *itemError) Unwrap() error { return e.err }

// onlyItemErrors reports whether every error joined in err is an itemError.
func onlyItemErrors(err error) bool {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range j.Unwrap() {
			if !onlyItemErrors(e) {
				return false
			}
		}
		return true
	}
	var item *itemError
	return errors.As(err, &item)
}

// failedItemBackoff is how long a timer or run whose decision failed is
// left before it is claimed again: long enough that it stops heading every
// claim (they take the oldest first), short enough that a passing fault
// (a key briefly unavailable) costs little.
const failedItemBackoff = time.Minute

// failedItem logs and counts a timer or run the scheduler could not
// handle, and pushes its lease out by failedItemBackoff with backoff (a
// constant UPDATE taking the item's id, this scheduler's id and the
// interval), so the claim's oldest-first order does not hand it back
// first after every lease. It returns err, for Tick's joined error.
func (s *Scheduler) failedItem(ctx context.Context, kind string, tenant uuid.UUID, backoff string, id uuid.UUID, err error) error {
	telemetry.SchedulerItemFailures.WithLabelValues(kind).Inc()
	s.Logger.Error("scheduler: could not handle "+kind+"; others go on", "id", id, "tenant", tenant, "err", err)
	if berr := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, backoff, id, s.ID, failedItemBackoff)
		return err
	}); berr != nil {
		s.Logger.Warn("scheduler: could not back off "+kind+"; its lease expires as usual", "id", id, "err", berr)
	}
	return &itemError{err}
}

// sweep decides runs whose events were appended without an inline decision.
func (s *Scheduler) sweep(ctx context.Context) (int, error) {
	rows, err := s.Store.Pool.Query(ctx, `SELECT run_id, tenant_id FROM taskiem_claim_runs_to_orchestrate($1, 50)`, s.ID)
	if err != nil {
		return 0, err
	}
	var refs []RunRef
	for rows.Next() {
		var r RunRef
		if err := rows.Scan(&r.ID, &r.TenantID); err != nil {
			rows.Close()
			return 0, err
		}
		refs = append(refs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	swept := 0
	var errs []error
	for _, r := range refs {
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{r.TenantID}, func(tx pgx.Tx) error {
			return s.Store.decideInline(ctx, tx, r)
		}); err != nil {
			if ctx.Err() != nil {
				return swept, errors.Join(append(errs, err)...)
			}
			errs = append(errs, s.failedItem(ctx, "run", r.TenantID, `UPDATE runs SET orch_lease_until = now() + $3::interval WHERE id = $1 AND orch_lease_owner = $2`, r.ID,
				fmt.Errorf("run %s: %w", r.ID, err)))
			continue
		}
		swept++
	}
	return swept, errors.Join(errs...)
}

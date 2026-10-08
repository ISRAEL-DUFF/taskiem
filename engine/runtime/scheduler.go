package runtime

import (
	"context"
	"errors"
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
// LOCKED and every firing is guarded by fired_at, so leader election (spec
// 2.3) only saves duplicate work.
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

	lastPartitions, lastPurge time.Time
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
		if _, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.Logger.Error("scheduler tick failed", "err", err)
		} else if err == nil {
			telemetry.SchedulerTick.WithLabelValues(mode).SetToCurrentTime()
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
	if time.Since(s.lastPurge) > 10*time.Minute {
		if _, err := s.PurgeExpired(ctx, 500); err != nil {
			errs = append(errs, err)
		} else {
			s.lastPurge = time.Now()
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
	for _, d := range timers {
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
			fired++
			telemetry.SchedulerLateness.WithLabelValues("timer").Observe(d.late)
			return s.Store.decideInline(ctx, tx, run.ref)
		})
		if err != nil {
			return fired, err
		}
	}
	return fired, nil
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
	for _, r := range refs {
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{r.TenantID}, func(tx pgx.Tx) error {
			return s.Store.decideInline(ctx, tx, r)
		}); err != nil {
			return 0, err
		}
	}
	return len(refs), rows.Err()
}

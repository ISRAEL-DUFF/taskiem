package db

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/telemetry"
)

// Replica is an optional read replica (TASKIEM_DATABASE_READ_URL, decision
// 0024) for staleness-tolerant reads: run lists and dashboards. It is never
// used for anything that decides, leases or writes; ReadTx is the only way
// in, and its transactions are READ ONLY wherever they run.
//
// The replica pool connects with the same role switching as the primary
// (SET ROLE taskiem_app), and ReadTx sets the tenant scope the same way, so
// row-level security applies on the replica exactly as on the primary.
//
// Run measures the lag every Interval; while it is beyond MaxLag, or the
// replica cannot be reached, reads go to the primary.
type Replica struct {
	Pool *pgxpool.Pool
	// Primary is where the heartbeat is written.
	Primary *pgxpool.Pool
	// MaxLag is the staleness reads tolerate; default 10s.
	MaxLag time.Duration
	// Interval between lag checks; default 2s.
	Interval time.Duration
	Logger   *slog.Logger
	// Beat writes the heartbeat on the primary; nil writes db_heartbeat
	// through Primary. Tests replace it to hold the heartbeat still.
	Beat func(context.Context) error

	inUse atomic.Bool
	lag   atomic.Uint64 // float64 bits, seconds; -1 unreachable
}

func (r *Replica) maxLag() time.Duration {
	if r.MaxLag <= 0 {
		return 10 * time.Second
	}
	return r.MaxLag
}

func (r *Replica) logger() *slog.Logger {
	if r.Logger == nil {
		return slog.Default()
	}
	return r.Logger
}

// InUse reports whether reads currently go to the replica.
func (r *Replica) InUse() bool { return r != nil && r.inUse.Load() }

// Lag is the last measured lag in seconds; -1 when the replica could not
// be reached.
func (r *Replica) Lag() float64 { return math.Float64frombits(r.lag.Load()) }

func (r *Replica) set(lag float64, use bool) {
	r.lag.Store(math.Float64bits(lag))
	telemetry.DBReplicaLag.Set(lag)
	if was := r.inUse.Swap(use); was != use {
		if use {
			r.logger().Info("read replica in use", "lag_seconds", lag)
		} else {
			r.logger().Warn("read replica set aside: reads go to the primary", "lag_seconds", lag, "max_lag", r.maxLag().String())
		}
	}
	if use {
		telemetry.DBReplicaInUse.Set(1)
	} else {
		telemetry.DBReplicaInUse.Set(0)
	}
}

// Check writes a heartbeat on the primary, measures the replica against it
// and decides whether reads use the replica.
func (r *Replica) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	beat := r.Beat
	if beat == nil {
		beat = func(ctx context.Context) error {
			_, err := r.Primary.Exec(ctx, `UPDATE db_heartbeat SET beat_at = GREATEST(beat_at, now()) WHERE id = 1`)
			return err
		}
	}
	if err := beat(ctx); err != nil {
		// The primary is unwell: what the replica holds cannot be judged,
		// so keep the last decision.
		return err
	}
	var lag float64
	err := r.Pool.QueryRow(ctx, `SELECT GREATEST(EXTRACT(EPOCH FROM now() - beat_at), 0)::float8 FROM db_heartbeat WHERE id = 1`).Scan(&lag)
	if err != nil {
		r.set(-1, false)
		return err
	}
	r.set(lag, lag <= r.maxLag().Seconds())
	return nil
}

// Run checks the replica until ctx ends.
func (r *Replica) Run(ctx context.Context) error {
	interval := r.Interval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := r.Check(ctx); err != nil && ctx.Err() == nil {
			r.logger().Debug("read replica check failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// ReadTx runs fn in a READ ONLY transaction scoped to the tenants, on the
// replica when it is in use and on the primary otherwise. If the replica
// fails underneath it (the connection drops, or a recovery conflict
// cancels the query), the replica is set aside until the next good check
// and fn runs again on the primary, so fn must be safe to run twice and
// must reset anything it collects. A write inside fn fails on either.
func ReadTx(ctx context.Context, primary *pgxpool.Pool, r *Replica, tenants []uuid.UUID, fn func(pgx.Tx) error) error {
	if len(tenants) == 0 {
		return errors.New("db: empty tenant scope")
	}
	run := func(pool *pgxpool.Pool) error {
		return pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_scope', $1, true)", Scope(tenants...)); err != nil {
				return err
			}
			return fn(tx)
		})
	}
	if r == nil {
		telemetry.DBReads.WithLabelValues("no_replica").Inc()
		return run(primary)
	}
	if r.InUse() {
		err := run(r.Pool)
		// read_only_sql_transaction is fn trying to write: its own bug,
		// not the replica's, and it would fail on the primary too.
		var pe *pgconn.PgError
		if err == nil || !Transient(err) || ctx.Err() != nil || (errors.As(err, &pe) && pe.Code == "25006") {
			telemetry.DBReads.WithLabelValues("replica").Inc()
			return err
		}
		r.set(-1, false)
		r.logger().Warn("read replica failed a query: falling back to the primary", "err", err)
	}
	telemetry.DBReads.WithLabelValues("fallback").Inc()
	return run(primary)
}

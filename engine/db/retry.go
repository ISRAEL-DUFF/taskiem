package db

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/telemetry"
)

// RetryWindow bounds how long InTenantTxRetry keeps trying through a
// database outage: long enough for a managed primary failover (typically
// 10–30 seconds), short of a worker's lease.
var RetryWindow = 30 * time.Second

// Transient reports whether err is the database going away rather than the
// statement failing: a dropped or refused connection, the server shutting
// down or starting up, or a connection that landed on a read-only server
// (a standby, or a demoted primary) during a failover.
func Transient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "25006", // read_only_sql_transaction: connected to a standby
			"57P01", "57P02", "57P03", // admin_shutdown, crash_shutdown, cannot_connect_now
			"40001": // serialisation failure: also a standby's recovery conflict
			return true
		}
		return len(pe.Code) == 5 && pe.Code[:2] == "08" // connection_exception
	}
	if pgconn.SafeToRetry(err) {
		return true
	}
	var ce *pgconn.ConnectError
	var ne net.Error
	return errors.As(err, &ce) || errors.As(err, &ne) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed)
}

// serverMoved reports whether err means every pooled connection may point
// at the wrong server: the connection is gone, or the server is now a
// standby. A serialisation failure alone does not.
func serverMoved(err error) bool {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code != "40001"
	}
	return true
}

// ErrCommitUnknown marks an error from COMMIT itself: the transaction may
// or may not have committed.
var ErrCommitUnknown = errors.New("commit outcome unknown")

type commitError struct{ err error }

func (e commitError) Error() string   { return ErrCommitUnknown.Error() + ": " + e.err.Error() }
func (e commitError) Unwrap() []error { return []error{ErrCommitUnknown, e.err} }

// inTenantTx is InTenantTx with commit errors marked.
func inTenantTx(ctx context.Context, pool *pgxpool.Pool, tenants []uuid.UUID, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_scope', $1, true)", Scope(tenants...)); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		// Any loss of the connection here leaves the outcome unknown,
		// even when pgx calls the error safe to retry: a test cutting
		// the connection just after the COMMIT reached the server sees
		// such an error while the transaction committed.
		if Transient(err) {
			return commitError{err}
		}
		return err
	}
	return nil
}

// InTenantTxRetry is InTenantTx that rides out a failover: when the
// database goes away it retries for up to RetryWindow, with backoff,
// resetting the pool so new connections find the new primary
// (target_session_attrs=read-write in the connection string).
//
// A transaction that failed before COMMIT was sent rolled back, so it is
// always retried. One that failed during COMMIT may have committed; it is
// retried only when idempotent is true, which the caller asserts when
// running fn twice is harmless (a fenced write that finds the fence moved
// the second time, an upsert). fn must not have effects outside the
// transaction, and must reset anything it collects, since it can run more
// than once.
func InTenantTxRetry(ctx context.Context, pool *pgxpool.Pool, tenants []uuid.UUID, idempotent bool, fn func(pgx.Tx) error) error {
	if len(tenants) == 0 {
		return InTenantTx(ctx, pool, tenants, fn)
	}
	deadline := time.Now().Add(RetryWindow)
	backoff := 100 * time.Millisecond
	for attempt := 0; ; attempt++ {
		err := inTenantTx(ctx, pool, tenants, fn)
		if err == nil {
			if attempt > 0 {
				telemetry.DBRetries.WithLabelValues("recovered").Inc()
			}
			return nil
		}
		if !Transient(err) || (errors.Is(err, ErrCommitUnknown) && !idempotent) {
			return err
		}
		if ctx.Err() != nil || time.Now().Add(backoff).After(deadline) {
			telemetry.DBRetries.WithLabelValues("gave_up").Inc()
			return err
		}
		telemetry.DBRetries.WithLabelValues("retried").Inc()
		if serverMoved(err) {
			// Connections to the old primary are dead or read-only now.
			pool.Reset()
		}
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return err
		case <-t.C:
		}
		backoff = min(2*backoff, 2*time.Second)
	}
}

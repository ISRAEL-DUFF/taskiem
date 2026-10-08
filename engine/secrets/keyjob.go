package secrets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// KeyJob is the scheduler's key work (docs/byok.md): it health-checks
// customer keys, re-wraps after rotations, and resumes steps parked while a
// key was unavailable once it works again.
type KeyJob struct {
	Vault *Vault
	// Interval between passes (default 1 minute). With the cache TTL, it
	// bounds how long a revoked key keeps working and how long parked steps
	// wait after the key returns.
	Interval time.Duration
	// Batches is how many Rewrap batches one pass may run per tenant
	// (default 10).
	Batches int
	// Resume resumes a tenant's steps parked for its key
	// (runtime.Store.ResumeKeyParked); nil leaves them for people.
	Resume func(ctx context.Context, tenant uuid.UUID) (int, error)
	// Unavailable, when set, is told about each tenant whose key does not
	// work (metrics, logs).
	Unavailable func(tenant uuid.UUID, h Health)
	Logger      *slog.Logger
}

func (j *KeyJob) log() *slog.Logger {
	if j.Logger == nil {
		return slog.Default()
	}
	return j.Logger
}

// Run passes on a loop until ctx ends.
func (j *KeyJob) Run(ctx context.Context) error {
	every := j.Interval
	if every <= 0 {
		every = time.Minute
	}
	for {
		if err := j.Tick(ctx); err != nil && ctx.Err() == nil {
			j.log().Error("tenant keys", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(every):
		}
	}
}

// Tick makes one pass over the tenants with key work.
func (j *KeyJob) Tick(ctx context.Context) error {
	rows, err := j.Vault.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_key_tenants(now())`)
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tenants {
		if err := j.tenant(ctx, t); err != nil && ctx.Err() == nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", t, err))
		}
	}
	return errors.Join(errs...)
}

func (j *KeyJob) tenant(ctx context.Context, t uuid.UUID) error {
	h, err := j.Vault.CheckKey(ctx, t, "system")
	if err != nil {
		return err
	}
	if !h.OK {
		if h.Changed || h.Status == "unavailable" {
			j.log().Warn("tenant key unavailable: steps that need it park until it returns", "tenant", t, "customer_key", h.Customer, "status", h.Status, "error", h.Error)
		}
		if j.Unavailable != nil {
			j.Unavailable(t, h)
		}
		return nil
	}
	if h.Recovered {
		j.log().Info("customer key available again", "tenant", t)
	}
	batches := j.Batches
	if batches <= 0 {
		batches = 10
	}
	for i := 0; i < batches; i++ {
		var due bool
		if err := j.dueNow(ctx, t, &due); err != nil {
			return err
		}
		if !due {
			break
		}
		r, err := j.Vault.Rewrap(ctx, t, DefaultRewrapBatch)
		if err != nil {
			return fmt.Errorf("rewrap: %w", err)
		}
		if r.Done || r.Busy {
			break
		}
	}
	if j.Resume != nil {
		n, err := j.Resume(ctx, t)
		if err != nil {
			return fmt.Errorf("resume parked steps: %w", err)
		}
		if n > 0 {
			j.log().Info("resumed steps parked while the tenant key was unavailable", "tenant", t, "steps", n)
		}
	}
	return nil
}

func (j *KeyJob) dueNow(ctx context.Context, t uuid.UUID, due *bool) error {
	return db.InTenantTx(ctx, j.Vault.Pool, []uuid.UUID{t}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM key_rewrap_due WHERE tenant_id = $1 AND not_before <= now())`, t).Scan(due)
	})
}

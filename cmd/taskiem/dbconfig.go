package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// cloudConfig is the production-cloud settings (decision 0024): the worker
// pool this process serves, the read replica, and how long transactions
// retry through a failover.
type cloudConfig struct {
	// WorkerPool (TASKIEM_WORKER_POOL, default "shared"): the pool the
	// worker role claims for.
	WorkerPool string
	// ReadDSN (TASKIEM_DATABASE_READ_URL): a read replica for run lists
	// and dashboards; empty reads from the primary.
	ReadDSN string
	// ReadMaxLag (TASKIEM_DATABASE_READ_MAX_LAG, default 10s): beyond it,
	// reads go to the primary.
	ReadMaxLag time.Duration
	// RetryWindow (TASKIEM_DATABASE_RETRY_WINDOW, default 30s): how long a
	// worker keeps an outcome through a database failover.
	RetryWindow time.Duration
}

func cloudConfigFromEnv() (cloudConfig, error) {
	c := cloudConfig{
		WorkerPool:  strings.TrimSpace(env("TASKIEM_WORKER_POOL", runtime.SharedPool)),
		ReadDSN:     os.Getenv("TASKIEM_DATABASE_READ_URL"),
		ReadMaxLag:  10 * time.Second,
		RetryWindow: db.RetryWindow,
	}
	if !runtime.ValidPool(c.WorkerPool) {
		return c, fmt.Errorf("TASKIEM_WORKER_POOL: %q is not a pool name (lowercase letters, digits and dashes, up to 32)", c.WorkerPool)
	}
	for _, d := range []struct {
		key string
		dst *time.Duration
	}{{"TASKIEM_DATABASE_READ_MAX_LAG", &c.ReadMaxLag}, {"TASKIEM_DATABASE_RETRY_WINDOW", &c.RetryWindow}} {
		if v := os.Getenv(d.key); v != "" {
			x, err := time.ParseDuration(v)
			if err != nil || x <= 0 {
				return c, fmt.Errorf("%s: %q is not a positive duration (10s, 1m)", d.key, v)
			}
			*d.dst = x
		}
	}
	return c, nil
}

// dsnWarnings points out connection strings that will not survive a
// failover: several hosts without target_session_attrs=read-write may
// connect to a standby and fail every write.
func dsnWarnings(name, dsn string) []string {
	pc, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil
	}
	// Fallbacks also hold TLS and plain attempts at one host (sslmode=prefer).
	hosts := map[string]bool{fmt.Sprintf("%s:%d", pc.ConnConfig.Host, pc.ConnConfig.Port): true}
	for _, f := range pc.ConnConfig.Fallbacks {
		hosts[fmt.Sprintf("%s:%d", f.Host, f.Port)] = true
	}
	if len(hosts) > 1 && pc.ConnConfig.ValidateConnect == nil && name == "TASKIEM_DATABASE_URL" {
		return []string{name + " names several hosts without target_session_attrs=read-write: a connection may land on a standby (docs/operations.md#high-availability-postgres)"}
	}
	return nil
}

// openReplica opens the read replica with the same role switching as the
// primary, so row-level security applies there too.
func openReplica(ctx context.Context, c config, primary *pgxpool.Pool, log *slog.Logger) (*db.Replica, error) {
	rc := c
	rc.DSN = c.Cloud.ReadDSN
	pool, pingErr := openPool(ctx, rc)
	if pingErr != nil {
		// A replica that is down at start is not fatal: reads use the
		// primary until it answers. pgxpool connects lazily, so only a bad
		// connection string ends up here without a pool.
		pc, perr := pgxpool.ParseConfig(rc.DSN)
		if perr != nil {
			return nil, fmt.Errorf("TASKIEM_DATABASE_READ_URL: %w", perr)
		}
		pc.MaxConns = rc.PoolSize
		pc.AfterConnect = afterConnect(rc.DBRole)
		var err error
		if pool, err = pgxpool.NewWithConfig(ctx, pc); err != nil {
			return nil, fmt.Errorf("TASKIEM_DATABASE_READ_URL: %w", err)
		}
		log.Warn("read replica unreachable at start: reads use the primary until it answers", "err", pingErr)
	}
	return &db.Replica{Pool: pool, Primary: primary, MaxLag: c.Cloud.ReadMaxLag, Logger: log}, nil
}

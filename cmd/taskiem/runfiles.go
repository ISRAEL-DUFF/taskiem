package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/runfiles"
)

// runFilesSweep is how often expired run files are deleted.
const runFilesSweep = 10 * time.Minute

// runFilesService keeps the files runs fetch or make (docs/files.md), in
// each organisation's own storage. What a person sent is scanned by the
// clamd at TASKIEM_CLAMD_ADDR; without one such files are refused, as
// hosted forms' files are. A file is kept 30 days, or less when the plan
// keeps run history for less (max_retention_days).
func runFilesService(e *engine, log *slog.Logger) *runfiles.Service {
	m := &media.Service{Credentials: e.tables().Credentials, Guard: &egress.Guard{Logger: log}, Logger: log}
	if addr := strings.TrimSpace(os.Getenv("TASKIEM_CLAMD_ADDR")); addr != "" {
		m.Scanner = &forms.ClamdScanner{Addr: addr}
	}
	return &runfiles.Service{Pool: e.pool, Store: m, Logger: log.With("component", "runfiles"),
		Retention: func(ctx context.Context, tenant uuid.UUID) (time.Duration, error) {
			lim, err := e.store.LimitsFor(ctx, tenant)
			if err != nil {
				return 0, err
			}
			keep := runfiles.DefaultRetention
			if c := time.Duration(lim.MaxRetentionDays) * 24 * time.Hour; c > 0 && c < keep {
				keep = c
			}
			return keep, nil
		}}
}

// sweepRunFiles deletes expired run files until ctx ends.
func sweepRunFiles(ctx context.Context, s *runfiles.Service, log *slog.Logger) {
	t := time.NewTicker(runFilesSweep)
	defer t.Stop()
	for {
		for {
			n, err := s.Sweep(ctx)
			if err != nil {
				log.Warn("run files: sweep failed", "error", err)
			}
			if n < 500 || err != nil || ctx.Err() != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

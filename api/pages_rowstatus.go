package api

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/pages"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// Pages H5, g-pages-rowstatus (docs/spec/pages.md §4, Status): a list's
// or a table's "status" puts on each row where a run of its workflow is:
// the run a column names (run), the run that waits, or last waited, under
// what the row names (to: its correlation, as a page's signal reaches a
// delivery's run), or the run the page's address names. The rules are the
// status part's: only a run of that workflow, deployed in prod, in this
// business; its state in words for everyone the page opens to, the run
// itself (its id, times, who it waits for) only with run.read.

// rowRun is a run as a row shows it.
type rowRun struct {
	id             uuid.UUID
	status         string
	started, ended *time.Time
}

// markRowRuns sets each shown row's run_status.
func (s *Server) markRowRuns(ctx context.Context, pc *pageCtx, b pages.Block, out map[string]any) {
	rs := b.RowStatus
	rows, ok := out["rows"].([]pages.Shown)
	if rs == nil || !ok || len(rows) == 0 {
		return
	}
	p := principalFrom(ctx)
	// What each row names, as the viewer is shown it: a masked value
	// names nothing (it would be a run found by what is hidden).
	byID := map[uuid.UUID][]int{}
	byTo := map[string][]int{}
	for i, r := range rows {
		switch {
		case rs.Run != "":
			if id, err := uuid.Parse(strings.TrimSpace(r.RunID)); err == nil {
				byID[id] = append(byID[id], i)
			}
		case rs.To != "":
			if to := r.RunTo; to != "" && !strings.Contains(to, "•") {
				byTo[to] = append(byTo[to], i)
			}
		case pc.run != uuid.Nil:
			byID[pc.run] = append(byID[pc.run], i)
		}
	}
	if len(byID) == 0 && len(byTo) == 0 {
		return
	}
	// Runs keep what they wait under as its keyed hash, never the value
	// (runtime.CorrelationCategory).
	digests := map[string]string{}
	if len(byTo) > 0 && s.Vault != nil {
		for to := range byTo {
			d, err := s.Vault.SubjectFor(ctx, p.TenantID, runtime.CorrelationCategory, to)
			if err != nil {
				s.Logger.Warn("pages: a row's run", "page", pc.page.Name, "part", b.ID, "err", err)
				return
			}
			digests[d] = to
		}
	}
	found := map[int]rowRun{}
	declared := true
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		wf, _, _, n, err := pageWorkflow(ctx, s, tx, rs.Workflow)
		if err != nil {
			return err
		}
		if n != 1 {
			declared = false
			return nil
		}
		scan := func(q string, args []any, at func(rowRun, string) []int) error {
			res, err := tx.Query(ctx, q, args...)
			if err != nil {
				return err
			}
			defer res.Close()
			for res.Next() {
				var x rowRun
				var key string
				if err := res.Scan(&x.id, &x.status, &x.started, &x.ended, &key); err != nil {
					return err
				}
				for _, i := range at(x, key) {
					found[i] = x
				}
			}
			return res.Err()
		}
		if len(byID) > 0 {
			ids := make([]uuid.UUID, 0, len(byID))
			for id := range byID {
				ids = append(ids, id)
			}
			if err := scan(`SELECT id, status, started_at, ended_at, '' FROM runs
				WHERE tenant_id = $4 AND workflow_id = $1 AND environment = $2 AND id = ANY($3)`, []any{wf, pageToolEnv, ids, p.TenantID},
				func(x rowRun, _ string) []int { return byID[x.id] }); err != nil {
				return err
			}
		}
		if len(digests) > 0 {
			keys := make([]string, 0, len(digests))
			for d := range digests {
				keys = append(keys, d)
			}
			// The newest run of the workflow under each.
			if err := scan(`SELECT DISTINCT ON (correlation_key) id, status, started_at, ended_at, correlation_key FROM runs
				WHERE tenant_id = $4 AND correlation_key = ANY($3) AND workflow_id = $1 AND environment = $2
				ORDER BY correlation_key, started_at DESC`, []any{wf, pageToolEnv, keys, p.TenantID},
				func(_ rowRun, key string) []int { return byTo[digests[key]] }); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.Logger.Warn("pages: a row's run", "page", pc.page.Name, "part", b.ID, "err", err)
		return
	}
	if !declared {
		return // the workflow is gone or named twice: Check says so to its editors
	}
	reads := p.Can(PermRunRead)
	viewer := uuid.Nil
	if p.EndUser == nil {
		viewer = p.UserID
	}
	named := map[int]bool{}
	for _, is := range byID {
		for _, i := range is {
			named[i] = true
		}
	}
	for _, is := range byTo {
		for _, i := range is {
			named[i] = true
		}
	}
	for i := range rows {
		if !named[i] {
			continue
		}
		x, ok := found[i]
		if !ok {
			rows[i].RunStatus = map[string]any{"words": "Not started", "tone": "plain"}
			continue
		}
		words, tone := pages.StatusWords(x.status)
		st := map[string]any{"status": x.status, "words": words, "tone": tone}
		if reads {
			st["run"] = x.id
			if x.started != nil {
				st["started_at"] = x.started.UTC().Format(time.RFC3339)
			}
			if x.ended != nil {
				st["ended_at"] = x.ended.UTC().Format(time.RFC3339)
			}
			if stillGoing(x.status) {
				if waits, err := s.waitingFor(ctx, p.TenantID, x.id, viewer); err == nil && len(waits) > 0 {
					st["waiting_for"] = waits
				}
			}
		}
		rows[i].RunStatus = st
	}
}

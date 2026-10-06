package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Dashboards (spec 15.1): success rate, durations, failures by step and
// connector, runs needing reconciliation and approvals pending, over runs
// started in a window, derived from run history.

type dayCount struct {
	Day       string `json:"day"` // YYYY-MM-DD in the requested time zone
	Completed int    `json:"completed"`
	Failed    int    `json:"failed"`
	Other     int    `json:"other"` // running, waiting, cancelled, needs reconciliation
}

type failureCount struct {
	Workflow  string `json:"workflow,omitempty"`
	Step      string `json:"step,omitempty"`
	Connector string `json:"connector,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Count     int    `json:"count"`
}

type workflowStats struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Runs        int       `json:"runs"`
	Completed   int       `json:"completed"`
	Failed      int       `json:"failed"`
	SuccessRate *float64  `json:"success_rate"`
	P95Seconds  *float64  `json:"p95_seconds"`
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days := 7
	if v, err := strconv.Atoi(q.Get("days")); err == nil && v >= 1 && v <= 90 {
		days = v
	}
	tz := q.Get("tz")
	if tz == "" {
		tz = "Africa/Lagos"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		s.fail(w, r, fmt.Errorf("%w: unknown time zone %q", errBadRequest, tz))
		return
	}
	p := principalFrom(r.Context())
	env := q.Get("environment")
	if p.Environment != "" {
		env = p.Environment
	}
	var wf *uuid.UUID
	if v := q.Get("workflow"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			s.fail(w, r, fmt.Errorf("%w: bad workflow id", errBadRequest))
			return
		}
		wf = &id
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	// $1 since, $2 environment ('' for all), $3 workflow (NULL for all)
	runFilter := `r.started_at >= $1 AND ($2 = '' OR r.environment = $2) AND ($3::uuid IS NULL OR r.workflow_id = $3)`
	args := []any{since, env, wf}

	out := map[string]any{"days": days, "environment": env, "time_zone": tz}
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		var total, completed, failed, cancelled, active int
		var p50, p95 *float64
		if err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status = 'completed'), count(*) FILTER (WHERE status = 'failed'),
			count(*) FILTER (WHERE status = 'cancelled'), count(*) FILTER (WHERE status IN ('queued', 'running', 'waiting')),
			percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM ended_at - started_at)) FILTER (WHERE status = 'completed'),
			percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM ended_at - started_at)) FILTER (WHERE status = 'completed')
			FROM runs r WHERE `+runFilter, args...).Scan(&total, &completed, &failed, &cancelled, &active, &p50, &p95); err != nil {
			return err
		}
		out["runs"] = map[string]any{"total": total, "completed": completed, "failed": failed, "cancelled": cancelled, "active": active}
		out["success_rate"] = successRate(completed, failed)
		out["duration_seconds"] = map[string]any{"p50": p50, "p95": p95}

		// Right now, whatever the window.
		var recon, approvals int
		var oldest *time.Time
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM runs r WHERE r.status = 'needs_reconciliation' AND ($1 = '' OR r.environment = $1) AND ($2::uuid IS NULL OR r.workflow_id = $2)`, env, wf).Scan(&recon); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*), min(a.requested_at) FROM approvals a JOIN runs r ON r.id = a.run_id
			WHERE a.status = 'open' AND ($1 = '' OR r.environment = $1) AND ($2::uuid IS NULL OR r.workflow_id = $2)`, env, wf).Scan(&approvals, &oldest); err != nil {
			return err
		}
		out["needs_reconciliation"] = recon
		out["approvals_pending"] = map[string]any{"count": approvals, "oldest": oldest}

		rows, err := tx.Query(ctx, `SELECT to_char(d, 'YYYY-MM-DD'),
			count(r.id) FILTER (WHERE r.status = 'completed'), count(r.id) FILTER (WHERE r.status = 'failed'),
			count(r.id) FILTER (WHERE r.status NOT IN ('completed', 'failed'))
			FROM generate_series(date_trunc('day', $1 AT TIME ZONE $4), date_trunc('day', now() AT TIME ZONE $4), interval '1 day') d
			LEFT JOIN runs r ON date_trunc('day', r.started_at AT TIME ZONE $4) = d AND `+runFilter+`
			GROUP BY d ORDER BY d`, since, env, wf, tz)
		if err != nil {
			return err
		}
		series, err := pgx.CollectRows(rows, pgx.RowToStructByPos[dayCount])
		if err != nil {
			return err
		}
		out["daily"] = series

		// A step failure is a failed attempt; retried ones count too, since
		// they show a provider or step that is struggling.
		failures := func(cols, where, group string) ([]failureCount, error) {
			rows, err := tx.Query(ctx, `SELECT `+cols+`, count(*) FROM run_events f JOIN runs r ON r.id = f.run_id AND f.run_started_at = r.started_at
				JOIN workflows w ON w.id = r.workflow_id
				LEFT JOIN run_events s ON s.run_id = f.run_id AND s.run_started_at = f.run_started_at AND s.type = 'StepScheduled' AND s.step_id = f.step_id AND s.attempt = f.attempt
				WHERE f.type = 'StepFailed' AND `+where+runFilter+` GROUP BY `+group+` ORDER BY count(*) DESC, 1 LIMIT 10`, args...)
			if err != nil {
				return nil, err
			}
			return pgx.CollectRows(rows, pgx.RowToStructByName[failureCount])
		}
		bySteps, err := failures(`w.name AS workflow, f.step_id AS step, COALESCE(s.payload->>'connector', '') AS connector, '' AS kind`, ``, `1, 2, 3`)
		if err != nil {
			return err
		}
		byConnector, err := failures(`'' AS workflow, '' AS step, s.payload->>'connector' AS connector, COALESCE(f.payload->'error'->>'kind', '') AS kind`, `s.payload->>'connector' IS NOT NULL AND `, `3, 4`)
		if err != nil {
			return err
		}
		out["failures_by_step"] = nonNil(bySteps)
		out["failures_by_connector"] = nonNil(byConnector)

		rows, err = tx.Query(ctx, `SELECT w.id, w.name, count(*), count(*) FILTER (WHERE r.status = 'completed'), count(*) FILTER (WHERE r.status = 'failed'),
			percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM r.ended_at - r.started_at)) FILTER (WHERE r.status = 'completed')
			FROM runs r JOIN workflows w ON w.id = r.workflow_id WHERE `+runFilter+` GROUP BY w.id, w.name ORDER BY count(*) DESC LIMIT 50`, args...)
		if err != nil {
			return err
		}
		perWorkflow, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (workflowStats, error) {
			var ws workflowStats
			err := row.Scan(&ws.ID, &ws.Name, &ws.Runs, &ws.Completed, &ws.Failed, &ws.P95Seconds)
			ws.SuccessRate = successRate(ws.Completed, ws.Failed)
			return ws, err
		})
		out["workflows"] = nonNil(perWorkflow)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// successRate is completed / (completed + failed), or nil before anything ended.
func successRate(completed, failed int) *float64 {
	if completed+failed == 0 {
		return nil
	}
	v := float64(completed) / float64(completed+failed)
	return &v
}

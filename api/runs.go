package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/wd"
)

type startReq struct {
	Input       json.RawMessage `json:"input"`
	Version     int             `json:"version,omitempty"`
	Environment string          `json:"environment,omitempty"`
}

// environment picks the environment for a request: an environment-scoped
// API key fixes it; otherwise the request names it, defaulting to prod.
func environment(p *Principal, requested string) (string, error) {
	switch {
	case p.Environment != "" && requested != "" && requested != p.Environment:
		return "", fmt.Errorf("%w: this key is limited to %s", errForbidden, p.Environment)
	case p.Environment != "":
		return p.Environment, nil
	case requested != "":
		return requested, nil
	}
	return "prod", nil
}

// startRun starts a run of a workflow's published version (the one deployed
// in the environment unless the request pins another). An Idempotency-Key header makes the
// request safe to retry: the same key returns the same run.
func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	wf, err := uuid.Parse(chi.URLParam(r, "wf"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var req startReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	env, err := environment(p, req.Environment)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 255 {
		s.fail(w, r, fmt.Errorf("%w: Idempotency-Key is longer than 255 characters", errBadRequest))
		return
	}
	if len(req.Input) == 0 {
		req.Input = json.RawMessage("{}")
	}
	version := req.Version
	var def []byte
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM environments WHERE name = $1)`, env).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: no environment %q", errBadRequest, env)
		}
		if version == 0 {
			if err := tx.QueryRow(ctx, `SELECT 1 FROM workflows WHERE id = $1`, wf).Scan(new(int)); err != nil {
				return err
			}
			deployed, err := deployedVersion(ctx, tx, wf, env)
			if err != nil {
				return err
			}
			if deployed == 0 {
				return fmt.Errorf("%w: workflow has no version deployed in %s", errConflict, env)
			}
			version = deployed
		} else {
			// A gated environment runs only what was promoted to it.
			var gated bool
			if err := tx.QueryRow(ctx, `SELECT promotion_from IS NOT NULL FROM environments WHERE name = $1`, env).Scan(&gated); err != nil {
				return err
			}
			if deployed, err := deployedVersion(ctx, tx, wf, env); err != nil {
				return err
			} else if gated && deployed != version {
				return fmt.Errorf("%w: %s runs only the version promoted to it (%d)", errConflict, env, deployed)
			} else if deployed != version && env != "dev" && !p.Can(PermWorkflowPublish) {
				// Pinning another version outside dev is a deploy of sorts.
				return fmt.Errorf("%w: %s runs version %d; running another takes %s", errForbidden, env, deployed, PermWorkflowPublish)
			}
		}
		var state string
		if err := tx.QueryRow(ctx, `SELECT state, definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, version).Scan(&state, &def); err != nil {
			return err
		}
		if state != "published" && state != "deprecated" {
			return fmt.Errorf("%w: version %d is %s", errConflict, version, state)
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if msgs := s.inputProblems(wf, version, def, req.Input); len(msgs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "input does not match the workflow's inputs schema", "problems": msgs})
		return
	}
	var input any
	if err := json.Unmarshal(req.Input, &input); err != nil {
		s.fail(w, r, errors.Join(errBadRequest, err))
		return
	}
	ref, created, err := s.Store.StartRun(r.Context(), runtime.StartRequest{
		TenantID: p.TenantID, WorkflowID: wf, Version: version, Environment: env,
		Trigger:   map[string]any{"type": "manual", "body": input},
		StartedBy: p.Actor(), TriggerID: "api/" + wf.String(), DedupKey: key,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	st, _ := s.Store.RunStatus(r.Context(), ref)
	writeJSON(w, status, map[string]any{"run_id": ref.ID, "workflow_id": wf, "version": version, "environment": env, "status": st, "created": created})
}

// inputProblems validates a run's input against the version's inputs schema.
func (s *Server) inputProblems(wf uuid.UUID, version int, def, input []byte) []string {
	d, err := s.definitionFor(wf, version, def)
	if err != nil {
		return []string{err.Error()}
	}
	return d.ValidateInputs(input)
}

// definitionFor parses a version's definition once; versions are immutable.
func (s *Server) definitionFor(wf uuid.UUID, version int, doc []byte) (*wd.Definition, error) {
	key := wf.String() + "/" + strconv.Itoa(version)
	if d, ok := s.defs.Load(key); ok {
		return d.(*wd.Definition), nil
	}
	d, err := wd.Load(doc)
	if err != nil {
		return nil, err
	}
	s.defs.Store(key, d)
	return d, nil
}

type runSummary struct {
	ID          uuid.UUID  `json:"id"`
	WorkflowID  uuid.UUID  `json:"workflow_id"`
	Workflow    string     `json:"workflow"`
	Version     int        `json:"version"`
	Environment string     `json:"environment"`
	Status      string     `json:"status"`
	StartedBy   *string    `json:"started_by"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at"`
}

const runColumns = `r.id, r.workflow_id, w.name, r.version, r.environment, r.status, r.started_by, r.started_at, r.ended_at`

// listRuns lists runs, newest first. Filters: workflow, status, environment;
// paging with before (a started_at timestamp) and limit.
func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	q := r.URL.Query()
	env, err := environment(p, q.Get("environment"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if p.Environment == "" && q.Get("environment") == "" {
		env = ""
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	before := time.Now().Add(time.Hour)
	if b := q.Get("before"); b != "" {
		if before, err = time.Parse(time.RFC3339Nano, b); err != nil {
			s.fail(w, r, fmt.Errorf("%w: before must be an RFC 3339 time", errBadRequest))
			return
		}
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
	var out []runSummary
	err = s.readTx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+runColumns+` FROM runs r JOIN workflows w ON w.id = r.workflow_id
			WHERE r.started_at < $1 AND ($2::uuid IS NULL OR r.workflow_id = $2) AND ($3 = '' OR r.status = $3) AND ($4 = '' OR r.environment = $4)
			ORDER BY r.started_at DESC LIMIT $5`, before, wf, q.Get("status"), env, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[runSummary])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": nonNil(out)})
}

func (s *Server) runRef(r *http.Request) (runtime.RunRef, runSummary, error) {
	p := principalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "run"))
	if err != nil {
		return runtime.RunRef{}, runSummary{}, pgx.ErrNoRows
	}
	var sum runSummary
	err = s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+runColumns+` FROM runs r JOIN workflows w ON w.id = r.workflow_id WHERE r.id = $1`, id)
		if err != nil {
			return err
		}
		sum, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[runSummary])
		return err
	})
	if err == nil && p.Environment != "" && sum.Environment != p.Environment {
		err = pgx.ErrNoRows
	}
	return runtime.RunRef{ID: id, TenantID: p.TenantID}, sum, err
}

// getRun returns a run and its history. Personal data stays sealed unless
// the caller holds pii.reveal and asks with ?reveal=true; revealing is
// audited (spec 9.4).
func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	ref, sum, err := s.runRef(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	reveal := r.URL.Query().Get("reveal") == "true"
	var events []history.Event
	if reveal {
		if !principalFrom(r.Context()).Can(PermPIIReveal) {
			writeErr(w, http.StatusForbidden, "requires "+PermPIIReveal)
			return
		}
		if err := s.tx(r, func(tx pgx.Tx) error { return auditTx(r, tx, "pii.reveal", "run:"+ref.ID.String(), nil) }); err != nil {
			s.fail(w, r, err)
			return
		}
		events, err = s.Store.OpenedHistory(r.Context(), ref)
	} else {
		events, err = s.Store.RunHistory(r.Context(), ref)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// What this run resumed (a fork from a failed run's step) and the runs
	// that resumed it (docs/ai.md#repairing-failed-runs).
	forks, err := s.Store.Forks(r.Context(), ref)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	forks.Children = nonNil(forks.Children)
	writeJSON(w, http.StatusOK, map[string]any{"run": sum, "events": nonNil(events), "revealed": reveal, "forks": forks})
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	ref, _, err := s.runRef(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Store.CancelRun(r.Context(), ref, principalFrom(r.Context()).Actor()); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error { return auditTx(r, tx, "run.cancel", "run:"+ref.ID.String(), nil) }); err != nil {
		s.fail(w, r, err)
		return
	}
	st, _ := s.Store.RunStatus(r.Context(), ref)
	writeJSON(w, http.StatusOK, map[string]any{"run_id": ref.ID, "status": st})
}

type resolveReq struct {
	Resolution string `json:"resolution"` // completed | failed | retry
	Output     any    `json:"output,omitempty"`
	Note       string `json:"note"`
}

// resolveStep settles a step parked in needs_reconciliation after the
// operator has checked with the provider (spec 4.4). A note is required.
func (s *Server) resolveStep(w http.ResponseWriter, r *http.Request) {
	ref, _, err := s.runRef(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req resolveReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	switch req.Resolution {
	case runtime.ResolveCompleted, runtime.ResolveFailed, runtime.ResolveRetry:
	default:
		s.fail(w, r, fmt.Errorf("%w: resolution must be completed, failed or retry", errBadRequest))
		return
	}
	if req.Note == "" {
		s.fail(w, r, fmt.Errorf("%w: a note recording what the provider shows is required", errBadRequest))
		return
	}
	step := chi.URLParam(r, "step")
	if err := s.Store.ResolveStep(r.Context(), ref, step, req.Resolution, req.Output, req.Note, principalFrom(r.Context()).Actor()); err != nil {
		s.fail(w, r, err)
		return
	}
	st, _ := s.Store.RunStatus(r.Context(), ref)
	writeJSON(w, http.StatusOK, map[string]any{"run_id": ref.ID, "step": step, "status": st})
}

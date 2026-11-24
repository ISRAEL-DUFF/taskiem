package api

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Each run's use of a business's saved thing (build plan H6, g-saved-wf;
// saved_uses, 00641): recorded when a run starts and reads it, shown on
// the saved thing's page (the runs that read it) and on the run's (the
// things it read), to those who may read runs. Only a business's saved
// things are read by runs, so only they have uses.

// savedUse is one run that read a saved thing.
type savedUse struct {
	RunID      uuid.UUID `json:"run_id"`
	WorkflowID uuid.UUID `json:"workflow_id"`
	Workflow   string    `json:"workflow"`
	Version    int       `json:"version"`
	// Name is the saved thing's name as the run read it, and Steps the
	// steps that read it.
	Name   string    `json:"name"`
	Steps  []string  `json:"steps"`
	ReadAt time.Time `json:"read_at"`
	// Status is how the run stands now; empty once it is no longer kept.
	Status string `json:"status,omitempty"`
}

// savedUsesMax is how many of the latest runs a saved thing's page lists.
const savedUsesMax = 50

// savedRuns is GET /v1/saved/{id}/runs: the latest runs that read one of
// the business's saved things, newest first.
func (s *Server) savedRuns(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	out := []savedUse{}
	err = s.tx(r, func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM saved_objects WHERE id = $1 AND person_id IS NULL)`, id).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return pgx.ErrNoRows
		}
		rows, err := tx.Query(r.Context(), `SELECT u.run_id, u.workflow_id, COALESCE(w.name, ''), u.version, u.name, u.steps, u.read_at, COALESCE(r.status, '')
			FROM saved_uses u LEFT JOIN workflows w ON w.id = u.workflow_id LEFT JOIN runs r ON r.id = u.run_id
			WHERE u.object_id = $1 ORDER BY u.read_at DESC LIMIT $2`, id, savedUsesMax)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[savedUse])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": nonNil(out)})
}

// runSavedRead is one saved thing a run read.
type runSavedRead struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Title  string    `json:"title"`
	Kind   string    `json:"kind"`
	Steps  []string  `json:"steps"`
	ReadAt time.Time `json:"read_at"`
}

// runSavedReads are the saved things run read at its start, by name.
func runSavedReads(ctx context.Context, tx pgx.Tx, run uuid.UUID) ([]runSavedRead, error) {
	rows, err := tx.Query(ctx, `SELECT o.id, u.name, o.title, o.kind, u.steps, u.read_at
		FROM saved_uses u JOIN saved_objects o ON o.id = u.object_id WHERE u.run_id = $1 ORDER BY u.name`, run)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[runSavedRead])
	return nonNil(out), err
}

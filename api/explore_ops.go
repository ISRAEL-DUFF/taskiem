package api

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ops"
)

// Explore's Featured row, as Taskiem's operators choose it (docs/spec/
// mobile-app-explore-and-offline.md §2.2, migration 00427): a business is
// featured until a date, and a pick can be ended early. Each write needs a
// passkey step-up and is recorded in the platform chain. A business that
// stops being findable, or whose page is suspended, falls out of the row at
// once whatever its pick says.

func (s *Server) opsFeaturedRoutes(r chi.Router) {
	r.Get("/explore/featured", s.opsFeatured)
	r.Post("/explore/featured", s.opsFeature)
	r.Post("/explore/featured/{id}/end", s.opsFeatureEnd)
}

type featuredPick struct {
	ID        uuid.UUID  `json:"id"`
	Business  uuid.UUID  `json:"business"`
	Name      string     `json:"name"`
	StartsAt  time.Time  `json:"starts_at"`
	Until     time.Time  `json:"until"`
	Note      string     `json:"note"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	EndedAt   *time.Time `json:"ended_at"`
	EndedBy   *string    `json:"ended_by"`
	// Shown says it is in the row now (live, findable, not suspended).
	Shown bool `json:"shown"`
}

func (s *Server) opsFeatured(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT id, business, name, starts_at, until, note, created_by, created_at, ended_at, ended_by, shown FROM taskiem_ops_featured()`)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[featuredPick])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"picks": nonNil(out)})
}

func (s *Server) opsFeature(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Business uuid.UUID  `json:"business"`
		Until    time.Time  `json:"until"`
		Note     string     `json:"note"`
		StepUp   *opsStepUp `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	req.Note = strings.TrimSpace(req.Note)
	if req.Business == uuid.Nil || req.Until.IsZero() || utf8.RuneCountInString(req.Note) > 500 {
		writeErr(w, http.StatusUnprocessableEntity, "a pick names a business and when it ends, and may have a note (up to 500 characters)")
		return
	}
	if !s.checkOpsStepUp(w, r, req.StepUp, "ops.explore.feature", req.Business.String()) {
		return
	}
	o := operatorFrom(r.Context())
	id := uuid.Must(uuid.NewV7())
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		if _, err := tx.Exec(r.Context(), `SELECT taskiem_ops_feature_add($1, $2, $3, $4, $5)`, id, req.Business, req.Until, req.Note, o.Actor()); err != nil {
			return err
		}
		return ops.AuditTx(r.Context(), tx, o.Actor(), "ops.explore.feature", req.Business.String(),
			map[string]any{"pick": id.String(), "until": req.Until.UTC().Format(time.RFC3339), "note": req.Note})
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) opsFeatureEnd(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such pick")
		return
	}
	var req struct {
		StepUp *opsStepUp `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.checkOpsStepUp(w, r, req.StepUp, "ops.explore.unfeature", id.String()) {
		return
	}
	o := operatorFrom(r.Context())
	err = s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		var biz uuid.UUID
		if err := tx.QueryRow(r.Context(), `SELECT taskiem_ops_feature_end($1, $2)`, id, o.Actor()).Scan(&biz); err != nil {
			return err
		}
		return ops.AuditTx(r.Context(), tx, o.Actor(), "ops.explore.unfeature", biz.String(), map[string]any{"pick": id.String()})
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

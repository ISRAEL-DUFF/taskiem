package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ingest"
)

// startWebhookListen makes a test address for the webhook trigger an
// editor is building ("Listen for a test call"): the trigger's config as
// it stands in the editor, saved or not, and the environment whose secret
// checks the call. The first call within two minutes is kept for the
// editor to show and starts nothing (docs/contracts/wd-v1.md#webhook-triggers).
func (s *Server) startWebhookListen(w http.ResponseWriter, r *http.Request) {
	wf, err := uuid.Parse(chi.URLParam(r, "wf"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var req struct {
		Environment string         `json:"environment"`
		Config      map[string]any `json:"config"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if req.Environment == "" {
		req.Environment = "prod"
	}
	if !envNameRe.MatchString(req.Environment) {
		s.fail(w, r, fmt.Errorf("%w: environment is not a name", errBadRequest))
		return
	}
	if p.Environment != "" && p.Environment != req.Environment {
		s.fail(w, r, fmt.Errorf("%w: this key is limited to %s", errForbidden, p.Environment))
		return
	}
	if req.Config == nil {
		s.fail(w, r, fmt.Errorf("%w: config is the webhook trigger's config", errBadRequest))
		return
	}
	var l ingest.Listen
	err = s.tx(r, func(tx pgx.Tx) error {
		// The secret is the workflow's own, whatever the editor sends.
		var wdID string
		err := tx.QueryRow(r.Context(), `SELECT definition->>'id' FROM workflow_versions WHERE workflow_id = $1 ORDER BY version DESC LIMIT 1`, wf).Scan(&wdID)
		if err != nil {
			return err // no version: no such workflow (404)
		}
		if l, err = ingest.StartListen(r.Context(), tx, p.TenantID, wf, wdID, req.Environment, p.Actor(), req.Config, time.Now()); err != nil {
			if errors.Is(err, ingest.ErrListenConfig) {
				return fmt.Errorf("%w: %w", errBadRequest, err)
			}
			return err
		}
		return auditTx(r, tx, "webhook.listen", wf.String(), map[string]any{"environment": req.Environment, "listen": l.ID})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": l.ID, "url": s.hooksBase() + l.Path, "expires_at": l.ExpiresAt})
}

// webhookListen is a test address's state, with its call once it came.
func (s *Server) webhookListen(w http.ResponseWriter, r *http.Request) {
	wf, err1 := uuid.Parse(chi.URLParam(r, "wf"))
	id, err2 := uuid.Parse(chi.URLParam(r, "listen"))
	if err1 != nil || err2 != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	p := principalFrom(r.Context())
	var st ingest.ListenState
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		st, err = ingest.ReadListen(r.Context(), tx, s.Store.PII, p.TenantID, wf, id, time.Now())
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// newSigningSecret makes a new random signing secret for a workflow's
// webhook in one environment, stores it in the vault under the name the
// trigger checks (webhook_<WD id>), and shows it once: the Endpoints page's
// "New signing secret". It replaces the old one at once, so the sender must
// switch to it. This is how a sub-tenant's people get the secret for a
// workflow their partner installed, without the partner ever holding it
// (decision 0030).
func (s *Server) newSigningSecret(w http.ResponseWriter, r *http.Request) {
	wf, err := uuid.Parse(chi.URLParam(r, "wf"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var req struct {
		Environment string `json:"environment"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if !envNameRe.MatchString(req.Environment) {
		s.fail(w, r, fmt.Errorf("%w: environment is required", errBadRequest))
		return
	}
	if p.Environment != "" && p.Environment != req.Environment {
		s.fail(w, r, fmt.Errorf("%w: this key is limited to %s", errForbidden, p.Environment))
		return
	}
	var name, auth string
	err = s.tx(r, func(tx pgx.Tx) error {
		err := tx.QueryRow(r.Context(), `SELECT COALESCE(secret_name, ''), COALESCE(auth, '') FROM triggers WHERE workflow_id = $1 AND environment = $2 AND type = 'webhook'`,
			wf, req.Environment).Scan(&name, &auth)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: the workflow has no webhook deployed in %s: publish it first", errConflict, req.Environment)
		}
		if err != nil {
			return err
		}
		if name == "" || auth == "" || auth == "none" {
			return fmt.Errorf("%w: the webhook in %s takes no secret (auth %q)", errConflict, req.Environment, auth)
		}
		var exists bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM secrets WHERE environment = $1 AND name = $2)`, req.Environment, name).Scan(&exists); err != nil || exists {
			return err
		}
		return s.checkCount(r.Context(), tx, p.TenantID, "max_secrets")
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		s.fail(w, r, err)
		return
	}
	secret := "whsec_" + hex.EncodeToString(raw)
	if _, err := s.Vault.Put(r.Context(), p.TenantID, req.Environment, name, []byte(secret), p.Actor()); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"environment": req.Environment, "secret_name": name, "auth": auth, "secret": secret})
}

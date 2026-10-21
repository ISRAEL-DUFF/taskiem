package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/inmail"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Inbound email (docs/email.md, decision 0038): a workflow's addresses,
// what arrived at them, a new address, a sample message, and attachments.

// emailDeployable refuses deploying an email trigger this deployment
// cannot receive mail for, or whose attachments it cannot keep.
func (s *Server) emailDeployable(d *wd.Definition) error {
	if d.Trigger.Type != "email" {
		return nil
	}
	if s.Email == nil || s.MailDomain == "" {
		return fmt.Errorf("%w: this Taskiem cannot receive email yet (TASKIEM_MAIL_DOMAIN and TASKIEM_PUBLIC_KEY are unset)", errConflict)
	}
	if inmail.TriggerConfig(d).Attachments == "keep" && s.Email.Scanner == nil {
		return fmt.Errorf("%w: keeping attachments needs a malware scanner (TASKIEM_CLAMD_ADDR) on this deployment", errConflict)
	}
	return nil
}

type emailMessageOut struct {
	ID           uuid.UUID  `json:"id"`
	SenderDomain *string    `json:"sender_domain"`
	Size         int        `json:"size"`
	Attachments  int        `json:"attachments"`
	Status       string     `json:"status"`
	Note         *string    `json:"note"`
	RunID        *uuid.UUID `json:"run_id"`
	ReceivedAt   time.Time  `json:"received_at"`
}

type emailEndpointOut struct {
	ID          uuid.UUID         `json:"id"`
	Environment string            `json:"environment"`
	Address     string            `json:"address"`
	Status      string            `json:"status"`
	Version     int               `json:"version"`
	Confirm     json.RawMessage   `json:"confirmation"`
	Messages    []emailMessageOut `json:"messages"`
	Senders     []string          `json:"senders"`
	Attachments string            `json:"attachments"`
}

// getWorkflowEmail is a workflow's addresses, per environment, with the
// latest messages each got and the latest forwarding confirmation.
func (s *Server) getWorkflowEmail(w http.ResponseWriter, r *http.Request) {
	wf, err := uuid.Parse(chi.URLParam(r, "wf"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var out []emailEndpointOut
	err = s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT e.id, e.environment, e.public_id, e.status, e.version, e.confirmation, v.definition->'trigger'->'config'
			FROM email_endpoints e JOIN workflow_versions v ON v.workflow_id = e.workflow_id AND v.version = e.version
			WHERE e.workflow_id = $1 ORDER BY e.environment`, wf)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (emailEndpointOut, error) {
			var o emailEndpointOut
			var public string
			var cfg []byte
			err := row.Scan(&o.ID, &o.Environment, &public, &o.Status, &o.Version, &o.Confirm, &cfg)
			if s.MailDomain != "" {
				o.Address = public + "@" + s.MailDomain
			}
			var c inmail.Config
			_ = json.Unmarshal(cfg, &c)
			o.Senders, o.Attachments = nonNil(c.Senders), c.Attachments
			if o.Attachments == "" {
				o.Attachments = "ignore"
			}
			return o, err
		})
		if err != nil {
			return err
		}
		for i := range out {
			rows, err := tx.Query(r.Context(), `SELECT id, sender_domain, size, attachments, status, note, run_id, received_at FROM email_messages
				WHERE endpoint_id = $1 ORDER BY received_at DESC LIMIT 10`, out[i].ID)
			if err != nil {
				return err
			}
			out[i].Messages, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (emailMessageOut, error) {
				var m emailMessageOut
				return m, row.Scan(&m.ID, &m.SenderDomain, &m.Size, &m.Attachments, &m.Status, &m.Note, &m.RunID, &m.ReceivedAt)
			})
			if err != nil {
				return err
			}
			out[i].Messages = nonNil(out[i].Messages)
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"endpoints": nonNil(out), "receiving": s.Email != nil && s.MailDomain != ""})
}

// newEmailAddress gives a workflow's environment a new address; mail to
// the old one is refused from then on (an address that gets spam).
func (s *Server) newEmailAddress(w http.ResponseWriter, r *http.Request) {
	wf, err := uuid.Parse(chi.URLParam(r, "wf"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	env := chi.URLParam(r, "env")
	var public string
	err = s.tx(r, func(tx pgx.Tx) error {
		err := tx.QueryRow(r.Context(), `UPDATE email_endpoints SET public_id = $3, confirmation = NULL, updated_at = now()
			WHERE workflow_id = $1 AND environment = $2 AND status <> 'suspended' RETURNING public_id`, wf, env, ingest.NewFormID()).Scan(&public)
		if err != nil {
			return err
		}
		return auditTx(r, tx, "email.address_changed", "workflow:"+wf.String(), map[string]any{"environment": env})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	addr := ""
	if s.MailDomain != "" {
		addr = public + "@" + s.MailDomain
	}
	writeJSON(w, http.StatusOK, map[string]any{"address": addr})
}

// sendTestEmail runs the workflow with a sample message, as if one had
// arrived at the address.
func (s *Server) sendTestEmail(w http.ResponseWriter, r *http.Request) {
	if s.Email == nil {
		writeErr(w, http.StatusConflict, "this Taskiem cannot receive email yet")
		return
	}
	wf, err := uuid.Parse(chi.URLParam(r, "wf"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	env := chi.URLParam(r, "env")
	p := principalFrom(r.Context())
	var ep uuid.UUID
	var name string
	if err := s.tx(r, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT e.id, w.name FROM email_endpoints e JOIN workflows w ON w.id = e.workflow_id
			WHERE e.workflow_id = $1 AND e.environment = $2 AND e.status = 'live'`, wf, env).Scan(&ep, &name)
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusConflict, "publish the workflow first: its address takes mail once it is live")
			return
		}
		s.fail(w, r, err)
		return
	}
	run, note, err := s.Email.Sample(r.Context(), p.TenantID, ep, name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run_id": run, "note": note})
}

// emailFileURL is a short-lived link to an attachment, for members who
// may see personal data.
func (s *Server) emailFileURL(w http.ResponseWriter, r *http.Request) {
	if s.Email == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	p := principalFrom(r.Context())
	out, err := s.Email.FileURL(r.Context(), p.TenantID, chi.URLParam(r, "id"), 5*time.Minute)
	if errors.Is(err, forms.ErrNoRequest) {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "email.file_download", "email_file:"+chi.URLParam(r, "id"), nil)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

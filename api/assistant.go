package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/assistant"
	"github.com/israel-duff/taskiem/engine/ops"
)

// The business's customer assistant (decision 0062 V6,
// docs/assistant-guard.md): the owner's pause for every customer, the
// customers handed to a person, and Taskiem's own switches for the
// business, which only operators change.

func (s *Server) assistantRoutes(r chi.Router) {
	r.Get("/", s.getAssistant)
	r.Post("/pause", s.pauseAssistant)
	r.Post("/handovers/resume", s.resumeHandover)
}

// handoverView is a customer waiting for a person, as members see them:
// the channel, when, until when, and a hint of who (never the number).
type handoverView struct {
	Channel  string    `json:"channel"`
	Customer string    `json:"customer"` // their subject: pseudonymous, what resume names
	Where    string    `json:"where"`
	Hint     string    `json:"hint"`
	At       time.Time `json:"at"`
	Until    time.Time `json:"until"`
}

func (s *Server) getAssistant(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	out := map[string]any{"paused": false, "paused_at": nil, "paused_by": nil, "handovers": []handoverView{}}
	mode := s.AssistantMode
	if mode == "" {
		mode = assistant.ModeOn
	}
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		var at *time.Time
		var by *string
		if err := tx.QueryRow(ctx, `SELECT assistant_paused_at, assistant_paused_by FROM tenant_channel_settings WHERE tenant_id = $1`, p.TenantID).Scan(&at, &by); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		out["paused"], out["paused_at"], out["paused_by"] = at != nil, at, by
		var beta bool
		var stopped *time.Time
		var reason *string
		if err := tx.QueryRow(ctx, `SELECT beta, stopped_at, stopped_reason FROM assistant_controls WHERE tenant_id = $1`, p.TenantID).Scan(&beta, &stopped, &reason); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		// What Taskiem allows: on, or beta and this business is in it, and
		// not stopped for it.
		on := mode == assistant.ModeOn || mode == assistant.ModeBeta && beta
		out["taskiem"] = map[string]any{"on": on && stopped == nil, "mode": mode, "beta": beta, "stopped": stopped != nil, "stopped_reason": reason}
		rows, err := tx.Query(ctx, `SELECT channel, subject, sender, handover_at, handover_until FROM assistant_contacts
			WHERE tenant_id = $1 AND handover_until > now() ORDER BY handover_at DESC LIMIT 200`, p.TenantID)
		if err != nil {
			return err
		}
		type row struct {
			h      handoverView
			sealed map[string]any
		}
		var list []row
		for rows.Next() {
			var x row
			if err := rows.Scan(&x.h.Channel, &x.h.Customer, &x.sealed, &x.h.At, &x.h.Until); err != nil {
				rows.Close()
				return err
			}
			list = append(list, x)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		views := make([]handoverView, 0, len(list))
		for _, x := range list {
			x.h.Where = channelWhere(x.h.Channel)
			// A Telegram chat has no number to show: a short mark of the
			// customer tells two of them apart (never their chat id).
			x.h.Hint = x.h.Where + " customer " + shortMark(x.h.Customer)
			if s.Vault != nil && strings.HasPrefix(x.h.Channel, "whatsapp@") {
				if v, err := s.Vault.OpenTx(ctx, tx, p.TenantID, x.sealed); err == nil {
					if n, ok := v.(string); ok && len(n) > 4 {
						x.h.Hint = "Number ending " + n[len(n)-4:]
					}
				}
			}
			views = append(views, x.h)
		}
		out["handovers"] = views
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func channelWhere(channel string) string {
	switch {
	case strings.HasPrefix(channel, "whatsapp@"):
		return "WhatsApp"
	case strings.HasPrefix(channel, "telegram@"):
		return "Telegram"
	}
	return "Chat"
}

// pauseAssistant pauses (or resumes) the business's assistant for every
// customer (L11): their messages are kept, nothing is answered.
func (s *Server) pauseAssistant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Paused bool `json:"paused"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	action := "assistant.resume"
	if req.Paused {
		action = "assistant.pause"
	}
	err := s.tx(r, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `INSERT INTO tenant_channel_settings (tenant_id, assistant_paused_at, assistant_paused_by, updated_by)
			VALUES ($1, CASE WHEN $2 THEN now() END, CASE WHEN $2 THEN $3 END, $3)
			ON CONFLICT (tenant_id) DO UPDATE SET assistant_paused_at = CASE WHEN $2 THEN COALESCE(tenant_channel_settings.assistant_paused_at, now()) END,
			  assistant_paused_by = CASE WHEN $2 THEN COALESCE(tenant_channel_settings.assistant_paused_by, $3) END, updated_by = $3, updated_at = now()`,
			p.TenantID, req.Paused, p.Actor())
		if err != nil {
			return err
		}
		return auditTx(r, tx, action, p.TenantID.String(), nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.getAssistant(w, r)
}

// resumeHandover answers a customer with the assistant again before their
// hand-over ends.
func (s *Server) resumeHandover(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channel  string `json:"channel"`
		Customer string `json:"customer"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	err := s.tx(r, func(tx pgx.Tx) error {
		if err := assistant.Resume(r.Context(), tx, p.TenantID, req.Channel, req.Customer); err != nil {
			return err
		}
		return auditTx(r, tx, "assistant.handover.resume", req.Customer, map[string]any{"channel": req.Channel})
	})
	if errors.Is(err, assistant.ErrNoContact) {
		writeErr(w, http.StatusNotFound, "no such customer on this channel")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.getAssistant(w, r)
}

// opsAssistantAction is Taskiem's switches for one business's assistants
// (E4, L11): assistants-stop and assistants-start (with a reason), and
// assistants-beta-on and -off (TASKIEM_ASSISTANTS=beta lets only flagged
// businesses' assistants answer). Recorded in both chains.
func (s *Server) opsAssistantAction(w http.ResponseWriter, r *http.Request, tenant uuid.UUID, action string) {
	var req struct {
		Reason string     `json:"reason"`
		StepUp *opsStepUp `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" || utf8.RuneCountInString(req.Reason) > 500 {
		writeErr(w, http.StatusUnprocessableEntity, "give a reason (up to 500 characters): it is recorded in the platform's and the organisation's audit")
		return
	}
	if !s.checkOpsStepUp(w, r, req.StepUp, "ops.tenant."+action, tenant.String()) {
		return
	}
	set := map[string]string{
		"assistants-stop":     `stopped_at = now(), stopped_reason = $3`,
		"assistants-start":    `stopped_at = NULL, stopped_reason = NULL`,
		"assistants-beta-on":  `beta = true`,
		"assistants-beta-off": `beta = false`,
	}[action]
	o := operatorFrom(r.Context())
	err := s.opsTx(r, func(tx pgx.Tx, widen func(uuid.UUID) error) error {
		ctx := r.Context()
		if err := widen(tenant); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tenants WHERE id = $1)`, tenant).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return errNotFoundTenant
		}
		if _, err := tx.Exec(ctx, `INSERT INTO assistant_controls (tenant_id, updated_by) VALUES ($1, $2) ON CONFLICT (tenant_id) DO NOTHING`, tenant, o.Actor()); err != nil {
			return err
		}
		args := []any{tenant, o.Actor()}
		if action == "assistants-stop" {
			args = append(args, req.Reason)
		}
		if _, err := tx.Exec(ctx, `UPDATE assistant_controls SET `+set+`, updated_by = $2, updated_at = now() WHERE tenant_id = $1`, args...); err != nil {
			return err
		}
		detail := map[string]any{"reason": req.Reason}
		if err := tenantAudit(r, tx, tenant, o.Actor(), "tenant."+action, tenant.String(), detail); err != nil {
			return err
		}
		return ops.AuditTx(ctx, tx, o.Actor(), "ops.tenant."+action, tenant.String(), detail)
	})
	switch {
	case errors.Is(err, errNotFoundTenant):
		writeErr(w, http.StatusNotFound, "no such organisation")
	case err != nil:
		s.fail(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"id": tenant, "action": action})
	}
}

// shortMark is a few characters of a customer's pseudonymous subject, to
// tell customers apart without showing who they are.
func shortMark(subject string) string {
	if i := strings.LastIndex(subject, ":"); i >= 0 {
		subject = subject[i+1:]
	}
	if len(subject) > 4 {
		subject = subject[:4]
	}
	return strings.ToUpper(subject)
}

// assistantsOn reports whether Taskiem lets this business's assistants
// answer customers: TASKIEM_ASSISTANTS on, or beta with the business in
// it, and not stopped for it by an operator.
func (s *Server) assistantsOn(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (bool, error) {
	mode := s.AssistantMode
	if mode == "" {
		mode = assistant.ModeOn
	}
	var beta bool
	var stopped *time.Time
	if err := tx.QueryRow(ctx, `SELECT beta, stopped_at FROM assistant_controls WHERE tenant_id = $1`, tenant).Scan(&beta, &stopped); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	return (mode == assistant.ModeOn || mode == assistant.ModeBeta && beta) && stopped == nil, nil
}

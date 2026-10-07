package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The WhatsApp outbox's messages, for people watching deliveries
// (docs/whatsapp.md#delivery-and-retries): dead letters by default, or
// those with ?status=pending|sent|dropped. Who each went to is named by
// email; numbers and message text are not stored.

type outboxView struct {
	ID          uuid.UUID  `json:"id"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	UserID      uuid.UUID  `json:"user_id"`
	Email       string     `json:"email"`
	RunID       *uuid.UUID `json:"run_id,omitempty"`
	StepID      *string    `json:"step_id,omitempty"`
	AlertID     *uuid.UUID `json:"alert_id,omitempty"`
	Attempts    int        `json:"attempts"`
	LastError   *string    `json:"last_error"`
	NextAttempt *time.Time `json:"next_attempt_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	FinishedAt  *time.Time `json:"finished_at"`
}

func (s *Server) listWhatsAppOutbox(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	switch status {
	case "":
		status = "dead"
	case "dead", "pending", "sent", "dropped":
	default:
		writeErr(w, http.StatusBadRequest, "status is dead, pending, sent or dropped")
		return
	}
	var out []outboxView
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT o.id, o.kind, o.status, o.user_id, COALESCE(u.email, ''), o.run_id, o.step_id, o.alert_id, o.attempts, o.last_error,
			CASE WHEN o.status = 'pending' THEN o.next_attempt_at END, o.created_at, o.finished_at
			FROM whatsapp_outbox o LEFT JOIN users u ON u.id = o.user_id WHERE o.status = $1 ORDER BY o.created_at DESC LIMIT 200`, status)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[outboxView])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": nonNil(out)})
}

// retryWhatsAppOutbox sends a dead letter again from a fresh set of attempts.
func (s *Server) retryWhatsAppOutbox(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE whatsapp_outbox SET status = 'pending', attempts = 0, next_attempt_at = now(), finished_at = NULL
			WHERE id = $1 AND status = 'dead'`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return auditTx(r, tx, "whatsapp.outbox.retry", id.String(), nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

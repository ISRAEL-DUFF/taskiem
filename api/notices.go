package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// sendNoticeItem sends a queued Tell me message (notify@1) to its person
// on their chat channel. The text is the step's input as the run's history
// recorded it, opened now: the outbox holds none.
func (s *Server) sendNoticeItem(ctx context.Context, it whatsapp.OutboxItem) error {
	if it.Run == nil || it.Step == nil {
		return fmt.Errorf("%w: incomplete", whatsapp.ErrNothingToSend)
	}
	var business, title, message string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, it.Tenant).Scan(&business); err != nil {
			return err
		}
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT payload FROM run_events WHERE run_id = $1 AND step_id = $2 AND type = 'StepScheduled' ORDER BY seq DESC LIMIT 1`, *it.Run, *it.Step).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: the step's input is gone", whatsapp.ErrNothingToSend)
		}
		if err != nil {
			return err
		}
		var payload struct {
			Input any `json:"input"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return err
		}
		opened, err := pii.Open(ctx, s.Store.PII, tx, it.Tenant, payload.Input, pii.Taint{})
		if err != nil {
			return err
		}
		in, _ := opened.(map[string]any)
		title, _ = in["title"].(string)
		message, _ = in["message"].(string)
		return nil
	})
	if err != nil {
		return err
	}
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("%w: no message", whatsapp.ErrNothingToSend)
	}
	ch, addr, err := s.chatAddrOf(ctx, it.Channel, it.Tenant, it.User)
	if err != nil {
		return err
	}
	// Outside WhatsApp's window the alert template carries it, in the one
	// line the template has: the title and the message, as far as fits.
	head := message
	if title != "" {
		head = title + ": " + message
	}
	if r := []rune(head); len(r) > 200 {
		head = string(r[:200]) + "…"
	}
	body := message
	if title == "" {
		body = ""
	}
	m := whatsapp.AlertMessage(business, "notice", head, body, "", nil)
	m.Text = strings.TrimRight(m.Text, "\n")
	m.Tenant = it.Tenant
	if ch.OwnTenant() != uuid.Nil {
		m.Text = strings.TrimPrefix(m.Text, "["+business+"] ")
	}
	if _, err := ch.Send(ctx, addr, m); err != nil {
		return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(addr), err)
	}
	return nil
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// openStepInput is a step's input as the run's history recorded it when
// it was scheduled, its personal data opened.
func (s *Server) openStepInput(ctx context.Context, tx pgx.Tx, tenant, run uuid.UUID, step string) (map[string]any, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT payload FROM run_events WHERE run_id = $1 AND step_id = $2 AND type = 'StepScheduled' ORDER BY seq DESC LIMIT 1`, run, step).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: the step's input is gone", whatsapp.ErrNothingToSend)
	}
	if err != nil {
		return nil, err
	}
	var payload struct {
		Input any `json:"input"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	opened, err := pii.Open(ctx, s.Store.PII, tx, tenant, payload.Input, pii.Taint{})
	if err != nil {
		return nil, err
	}
	in, _ := opened.(map[string]any)
	return in, nil
}

// sendNoticeItem sends a queued Tell me message (notify@1) to its person
// on their chat channel. The text is the step's input as the run's history
// recorded it, opened now: the outbox holds none.
func (s *Server) sendNoticeItem(ctx context.Context, it whatsapp.OutboxItem) error {
	business, in, err := s.stepInput(ctx, it)
	if err != nil {
		return err
	}
	title, _ := in["title"].(string)
	message, _ := in["message"].(string)
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
	// Its buttons, each filling a tool's form (tools_buttons.go).
	if bs, _ := in["buttons"].([]any); len(bs) > 0 {
		buttons, err := s.noticeButtons(ctx, it, bs)
		if err != nil {
			return err
		}
		m.Buttons = buttons
	}
	if ch.OwnTenant() != uuid.Nil {
		m.Text = strings.TrimPrefix(m.Text, "["+business+"] ")
	}
	// A step that names a template of its own (a school's daily close)
	// goes as that template, its variables from the step's input, and as
	// the alert template until Meta approves it.
	if name, _ := in["template"].(string); name != "" {
		tpl, ok := whatsapp.TellTemplate(name)
		if !ok {
			return fmt.Errorf("%w: the template %q is not one a Tell me message can carry", whatsapp.ErrNothingToSend, name)
		}
		vars := map[string]string{"tenant": business}
		if given, _ := in["template_vars"].(map[string]any); given != nil {
			for k, v := range given {
				vars[k] = strings.Join(strings.Fields(fmt.Sprint(v)), " ")
			}
		}
		err := sendOwnTemplate(ctx, ch, addr, m.Text, it.Tenant, tpl, vars, m.Vars)
		if err != nil && !errors.Is(err, whatsapp.ErrFellBack) {
			return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(addr), err)
		}
		return err
	}
	if _, err := ch.Send(ctx, addr, m); err != nil {
		return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(addr), err)
	}
	return nil
}

// noticeButtons mints a Tell me message's buttons (a label, a tool's
// command, values, once) for the run that sent it, in that run's
// environment, as the chat's reply buttons; one whose tool is not there
// is left out.
func (s *Server) noticeButtons(ctx context.Context, it whatsapp.OutboxItem, bs []any) ([]whatsapp.Button, error) {
	var drawn []answerBtn
	err := db0(ctx, s, it.Tenant, func(tx pgx.Tx) error {
		var env string
		if err := tx.QueryRow(ctx, `SELECT environment FROM runs WHERE id = $1`, *it.Run).Scan(&env); err != nil {
			return err
		}
		for i, raw := range bs {
			b, _ := raw.(map[string]any)
			label, _ := b["label"].(string)
			tool, _ := b["tool"].(string)
			once, _ := b["once"].(bool)
			inputs, _ := b["inputs"].(map[string]any)
			for k, v := range inputs {
				if str, ok := v.(string); ok {
					inputs[k] = maskWords(str)
				}
			}
			ab := answerBtn{Label: label, Tool: tool, Inputs: inputs, Once: once}
			id, err := mintButton(ctx, tx, it.Tenant, env, *it.Run, "notice/"+*it.Step+"/"+strconv.Itoa(i), ab)
			if err != nil {
				return err
			}
			if id != uuid.Nil {
				ab.Token = id.String()
				drawn = append(drawn, ab)
			}
		}
		return nil
	})
	return chatButtons(drawn), err
}

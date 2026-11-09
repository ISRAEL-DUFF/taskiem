package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/assistant"
)

// The conversation's record from a run (decision 0062 V7): a step that
// sent something to the run's customer (an action declaring turn, rule 24),
// heard their voice note (turn.transcript), or a reply the check held back
// (assistant@1 check_reply answering ok false) is recorded with the
// conversation the run belongs to, after the step's result is written. A
// failure to record is logged, never the step's.

// recordTurn records what a completed step said to (or heard from) the
// run's customer, when the run belongs to a conversation.
func (w *Worker) recordTurn(ctx context.Context, outer pgx.Tx, p *plan, input map[string]any, output any) {
	if p == nil || p.conn == nil {
		return
	}
	act, ok := p.conn.Manifest.Actions[p.action]
	held := p.conn.Manifest.ID == "assistant" && p.action == "check_reply"
	if !ok || act.Turn == nil && !held {
		return
	}
	sealer, ok := w.Store.PII.(assistant.Sealer)
	if !ok {
		return
	}
	// In the step's own transaction, under a savepoint: a failure to
	// record is logged, never the step's.
	tx, err := outer.Begin(ctx)
	if err != nil {
		w.logTurn(p, err)
		return
	}
	err = func() error {
		channel, subject, root, ok, err := assistant.RunConversation(ctx, tx, p.c.tenant, p.c.run)
		if err != nil || !ok {
			return err
		}
		// The turn belongs to the run the customer's message started; the
		// step's own run (a child of it) is kept apart.
		run, stepRun := root, p.c.run
		out, _ := output.(map[string]any)
		switch {
		case held:
			if ok, _ := out["ok"].(bool); ok {
				return nil
			}
			text, _ := input["text"].(string)
			var kinds []string
			if probs, _ := out["problems"].([]any); probs != nil {
				for _, x := range probs {
					if m, _ := x.(map[string]any); m != nil {
						k := fmt.Sprint(m["kind"])
						kinds = append(kinds, k)
						if k == assistant.ProblemSecret || k == assistant.ProblemInternal {
							// Held because it carried a secret or a value
							// the business keeps from customers: the words
							// are not kept for anyone to read.
							text = ""
						}
					}
				}
			}
			return recordSent(ctx, tx, sealer, p, channel, subject, assistant.Sent{Role: assistant.RoleHeld, Text: text,
				Reason: strings.Join(kinds, ", "), Run: &run, StepRun: &stepRun, Step: p.c.step})
		case act.Turn.Transcript != "":
			text, _ := lookup(out, act.Turn.Transcript).(string)
			if text == "" {
				return nil
			}
			return assistant.SetTranscript(ctx, tx, sealer, p.c.tenant, p.c.run, text)
		case act.Turn.To != "":
			to := fmt.Sprint(lookup(input, act.Turn.To))
			if to == "" || to == "<nil>" {
				return nil
			}
			if assistant.SenderCategory(channel) == "phone" {
				to = phoneAsSender(to)
			}
			// Only what was sent to this conversation's customer.
			them, err := sealer.LiveSubjectTx(ctx, tx, p.c.tenant, assistant.SenderCategory(channel), to)
			if err != nil {
				return err
			}
			if baseSubject(them) != baseSubject(subject) {
				return nil
			}
			text, _ := lookup(input, act.Turn.Text).(string)
			kind := act.Turn.Kind
			if act.Turn.KindField != "" {
				if k, _ := lookup(input, act.Turn.KindField).(string); k != "" {
					kind = k
				}
			}
			if v, _ := input["voice"].(bool); v {
				kind = "voice"
			}
			var file any
			if act.Turn.File != "" {
				file = lookup(input, act.Turn.File)
			}
			return recordSent(ctx, tx, sealer, p, channel, subject, assistant.Sent{Role: assistant.RoleAssistant, Kind: kind, Text: text, File: file,
				Run: &run, StepRun: &stepRun, Step: p.c.step})
		}
		return nil
	}()
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		w.logTurn(p, err)
	}
}

func (w *Worker) logTurn(p *plan, err error) {
	if w.Logger != nil {
		w.Logger.Warn("conversation: a step's message was not recorded", "tenant", p.c.tenant, "run", p.c.run, "step", p.c.step, "err", err)
	}
}

func recordSent(ctx context.Context, tx pgx.Tx, sealer assistant.Sealer, p *plan, channel, subject string, s assistant.Sent) error {
	err := assistant.RecordSent(ctx, tx, sealer, p.c.tenant, channel, subject, s)
	if err == assistant.ErrDuplicate {
		return nil // a step's result recorded again (a retried write of the result)
	}
	return err
}

// lookup reads a dotted path (interactive.body.text) in a value.
func lookup(v any, path string) any {
	if path == "" {
		return nil
	}
	for _, k := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

// baseSubject is a subject without its generation (<subject>~2: a person
// erased who came back).
func baseSubject(s string) string {
	if i := strings.LastIndex(s, "~"); i > 0 {
		return s[:i]
	}
	return s
}

// phoneAsSender is a number as WhatsApp gives a sender (digits, with the
// country code: 2348031234567), however a workflow wrote it (+234 803…,
// 0803…).
func phoneAsSender(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	if strings.HasPrefix(d, "0") && len(d) == 11 {
		return "234" + d[1:]
	}
	return d
}

package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/capability"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/pii"
)

// recordPayout writes, in the transaction that records a payment step's
// completion, the money log entry for a payout the rail answered settled
// at once (pay_bank, pay_wallet, pay_mobile_money answering status
// settled), in prod as the log hears every rail event (docs/money-log.md).
// The entry is keyed as the rail's payout.settled event would be, so a
// later event for the same payout is the same entry. A pending answer
// writes nothing: its event, or nothing, follows.
func (w *Worker) recordPayout(ctx context.Context, tx pgx.Tx, p *plan, result history.Event) error {
	if w.Store.MoneyLog == nil || p.env != "prod" || p.conn == nil || result.Type != history.StepCompleted || !projected(p) {
		return nil
	}
	spec, ok := capability.Get(p.step.Capability.Uses)
	if !ok || !spec.IsPayout() {
		return nil
	}
	v, err := expr.DecodeJSON(result.Payload)
	if err != nil {
		return err // finish fails on it next anyway
	}
	done, _ := v.(map[string]any)
	out, _ := done["output"].(map[string]any)
	in, _ := p.sched.Capability.Input.(map[string]any)
	if out == nil {
		return nil
	}
	channel, err := w.Store.MoneyLog.Channel(ctx, tx, p.c.tenant, p.env, p.conn.Manifest.ID, p.sched.Connection)
	if err != nil {
		return err
	}
	e, ok := moneylog.FromPayout(channel, in, out)
	if !ok {
		return nil
	}
	if _, _, err = w.Store.MoneyLog.RecordTx(ctx, tx, p.c.tenant, e); errors.Is(err, moneylog.ErrInvalid) {
		return nil // never keeps the step's own outcome from being recorded
	}
	return err
}

// rememberPaidAccounts records, in the transaction that records a payment
// step's completion, the bank accounts it paid (pay_bank, and pay_many's
// items) in prod, for the approvals warning "First payment to this
// account" (docs/money-log.md#accounts-paid). A payout the rail refused
// (status failed) paid nobody.
func (w *Worker) rememberPaidAccounts(ctx context.Context, tx pgx.Tx, p *plan, result history.Event) error {
	subjects, ok := w.Store.PII.(moneylog.Subjects)
	if !ok || p.env != "prod" || result.Type != history.StepCompleted || !projected(p) {
		return nil
	}
	uses := p.step.Capability.Uses
	if uses != "pay_bank" && uses != "pay_many" {
		return nil
	}
	v, err := expr.DecodeJSON(result.Payload)
	if err != nil {
		return err
	}
	done, _ := v.(map[string]any)
	out, _ := done["output"].(map[string]any)
	in, _ := p.sched.Capability.Input.(map[string]any)
	payees := paidPayees(uses, in, out)
	for _, m := range payees {
		account, err := w.plainField(ctx, tx, p, m["account_number"])
		if err != nil || account == "" {
			continue // an account that cannot be read is not remembered, never a failed step
		}
		bank, _ := m["bank_code"].(string)
		if err := moneylog.RememberAccount(ctx, tx, subjects, p.c.tenant, account, bank, time.Now()); err != nil {
			return err
		}
	}
	return nil
}

// plainField is a field of a scheduled input as text, opening it when it
// is still sealed.
func (w *Worker) plainField(ctx context.Context, tx pgx.Tx, p *plan, v any) (string, error) {
	if pii.IsEnvelope(v) {
		opened, err := w.Store.PII.OpenTx(ctx, tx, p.c.tenant, v.(map[string]any))
		if err != nil {
			return "", err
		}
		v = opened
	}
	s, _ := v.(string)
	return s, nil
}

// paidPayees are the payees of a payout step's input that money went to:
// pay_bank's one unless it failed, and each item of a pay_many batch whose
// own result, in the batch's order, did not fail. A batch result that does
// not line up with the batch gives none: a first-payment warning too many
// is safer than one too few.
func paidPayees(uses string, in, out map[string]any) []map[string]any {
	if in == nil || out == nil {
		return nil
	}
	if uses == "pay_bank" {
		if out["status"] == "failed" {
			return nil
		}
		return []map[string]any{in}
	}
	items, _ := in["items"].([]any)
	results, _ := out["items"].([]any)
	if len(results) != len(items) {
		return nil
	}
	var paid []map[string]any
	for i, it := range items {
		r, _ := results[i].(map[string]any)
		if m, ok := it.(map[string]any); ok && r != nil && r["status"] != "failed" {
			paid = append(paid, m)
		}
	}
	return paid
}

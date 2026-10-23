package runtime

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/capability"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/moneylog"
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

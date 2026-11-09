package alerts

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AllowanceKind is the alert at 80 % and 100 % of a monthly allowance
// (decision 0062 L10): the owners hear before an assistant stops
// answering, not only when it has.
const AllowanceKind = "allowance"

// Allowances an assistant spends, by what the alert calls them.
var allowanceNames = map[string]struct{ name, unit string }{
	"ai_steps": {"AI steps", "tokens"},
	"speech":   {"voice notes (hearing and speaking)", "seconds"},
}

// Allowance tells the owners once a month when what is used of a monthly
// allowance passes 80 % and again at 100 %: the highest mark passed.
// allowed 0 (none, or no limit) tells nothing.
func Allowance(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, what string, used, allowed int64, now time.Time) error {
	if allowed <= 0 || used <= 0 {
		return nil
	}
	n, ok := allowanceNames[what]
	if !ok {
		n.name, n.unit = what, "units"
	}
	month := now.UTC().Format("2006-01")
	pct := used * 100 / allowed
	for _, mark := range []int64{100, 80} {
		if pct < mark {
			continue
		}
		title := fmt.Sprintf("%d%% of this month's allowance for %s is used", mark, n.name)
		body := fmt.Sprintf("%d of %d %s are used this month. ", used, allowed, n.unit)
		if mark == 100 {
			title = fmt.Sprintf("This month's allowance for %s is used up", n.name)
			body += "Your assistant answers with its fixed replies until the allowance renews next month, or until you change plan."
		} else {
			body += "When it is all used, your assistant answers with its fixed replies until it renews next month. Change plan under Billing to raise it."
		}
		_, err := Notify(ctx, tx, tenant, Alert{Kind: AllowanceKind, Dedup: what + ":" + month + ":" + fmt.Sprint(mark),
			Title: title, Body: body, Link: "/billing", Detail: map[string]any{"allowance": what, "used": used, "allowed": allowed, "mark": mark}})
		return err
	}
	return nil
}

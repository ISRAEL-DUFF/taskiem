package schools

import (
	"context"
	"fmt"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/schools"
)

// The daily close (docs/spec/schools.md 13.2, B9; docs/schools.md
// #the-daily-close): a day's report, taken once per school and day and
// never recomputed, with its text ready to send the proprietor. It names
// no student: admission numbers, invoice and receipt numbers only.

func (a *actions) dailyClose(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	day, _ := req.Input["day"].(string)
	if day != "" {
		if _, perr := time.Parse(time.DateOnly, day); perr != nil {
			return connector.Response{}, fmt.Errorf("day is a date, YYYY-MM-DD: %w", effects.ErrFatal)
		}
	}
	link, _ := req.Input["link"].(string)
	x, err := a.ops.CloseFor(ctx, c.Tenant, day, "workflow:"+c.Run.String())
	if err != nil {
		return connector.Response{}, classify(err)
	}
	sum := func(s schools.CloseSum) map[string]any { return map[string]any{"count": s.Count, "kobo": s.Kobo} }
	items := func(list []schools.CloseItem) []any {
		out := make([]any, 0, len(list))
		for _, it := range list {
			x := map[string]any{"kind": it.Kind, "message": it.Message, "path": it.Path}
			if it.AmountKobo != 0 {
				x["amount_kobo"] = it.AmountKobo
			}
			if it.Ref != "" {
				x["ref"] = it.Ref
			}
			out = append(out, x)
		}
		return out
	}
	out := map[string]any{"id": x.ID.String(), "day": x.Day, "ran_at": x.RanAt.UTC().Format(time.RFC3339), "reconciles": x.Reconciles, "problems": x.Problems,
		"money_in": sum(x.MoneyIn), "into_accounts": sum(x.IntoAccounts), "at_the_desk": sum(x.AtTheDesk), "matched_auto": sum(x.MatchedAuto), "matched_person": sum(x.MatchedPerson),
		"unmatched": sum(x.Unmatched), "lookback_found": sum(x.LookbackFound), "pending": len(x.Pending), "claims": len(x.Claims),
		"problem_list": items(x.ProblemList), "pending_list": items(x.Pending), "claim_list": items(x.Claims), "unmatched_list": items(x.UnmatchedList),
		"text": schools.CloseText(x, link)}
	if x.Term != "" {
		out["term"] = x.Term
	}
	return connector.Response{Output: out}, nil
}

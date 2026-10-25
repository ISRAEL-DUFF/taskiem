package schools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/schools"
)

// Chasing (docs/spec/schools.md 11, B7; docs/schools.md#debtors): the
// debtors with their aging, promises and plan standing; reminders sent
// under the step's key; plan decisions and telling a family of one. None
// answers a name.

func (a *actions) listDebtors(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	limit := 200
	if v, ok := intIn(req.Input["limit"]); ok {
		limit = v
	}
	if limit < 1 || limit > schools.MaxListed {
		return connector.Response{}, fmt.Errorf("limit is 1 to %d: %w", schools.MaxListed, effects.ErrFatal)
	}
	class, err := a.class(ctx, c, req)
	if err != nil {
		return connector.Response{}, err
	}
	f := schools.DebtorFilter{ClassID: class}
	f.Bucket, _ = req.Input["bucket"].(string)
	f.Has, _ = req.Input["has"].(string)
	d, err := a.ops.ListDebtors(ctx, c.Tenant, f)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	out := make([]any, 0, min(len(d.Debtors), limit))
	for _, x := range d.Debtors[:min(len(d.Debtors), limit)] {
		row := map[string]any{"student_id": x.StudentID.String(), "admission_number": x.AdmissionNumber, "class": x.Class, "class_id": x.ClassID.String(),
			"balance_kobo": x.BalanceKobo, "due_on": x.DueOn, "days_past_due": x.DaysPastDue, "bucket": x.Bucket, "last_rung": 0, "promise": "none", "plan": "none"}
		if x.FamilyID != nil {
			row["family_id"] = x.FamilyID.String()
		}
		if x.LastReminder != nil {
			row["last_rung"], row["last_reminded_at"] = x.LastReminder.Rung, x.LastReminder.At.UTC().Format(time.RFC3339)
		}
		if x.Promise != nil {
			row["promise"], row["promised_on"] = x.Promise.Status, x.Promise.PromisedOn
		}
		if x.Plan != nil {
			row["plan"] = x.Plan.State
			if x.Plan.NextDueOn != "" {
				row["plan_next_due_on"] = x.Plan.NextDueOn
			}
		}
		out = append(out, row)
	}
	totals := map[string]any{}
	for b, t := range d.Totals {
		totals[b] = map[string]any{"students": t.Students, "balance_kobo": t.BalanceKobo}
	}
	return connector.Response{Output: map[string]any{"debtors": out, "count": len(out), "total": len(d.Debtors), "balance_kobo": d.BalanceKobo, "totals": totals}}, nil
}

// sendReminders sends the chosen students (or the debtors past due) a
// reminder, once for the step.
func (a *actions) sendReminders(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	if req.IdempotencyKey == "" {
		return connector.Response{}, fmt.Errorf("send_reminders runs as a workflow step, with its key: %w", effects.ErrFatal)
	}
	in := schools.RemindIn{Key: "step:" + req.IdempotencyKey}
	switch r, _ := req.Input["rung"].(string); r {
	case "", "auto":
	case "1", "2", "3":
		in.Rung = int(r[0] - '0')
	default:
		if n, ok := intIn(req.Input["rung"]); ok && n >= 1 && n <= 3 {
			in.Rung = n
		} else {
			return connector.Response{}, fmt.Errorf("rung is 1, 2, 3 or auto: %w", effects.ErrFatal)
		}
	}
	if v, ok := intIn(req.Input["limit"]); ok {
		if v < 1 || v > schools.MaxRemind {
			return connector.Response{}, fmt.Errorf("limit is 1 to %d: %w", schools.MaxRemind, effects.ErrFatal)
		}
		in.Limit = v
	}
	if list, ok := req.Input["students"].([]any); ok && len(list) > 0 {
		if len(list) > schools.MaxRemind {
			return connector.Response{}, fmt.Errorf("at most %d students per send: %d were given: %w", schools.MaxRemind, len(list), effects.ErrFatal)
		}
		refs := make([]string, 0, len(list))
		for _, v := range list {
			s, _ := v.(string)
			if s = strings.TrimSpace(s); s != "" {
				refs = append(refs, s)
			}
		}
		if in.Students, err = a.ops.FindStudents(ctx, c.Tenant, refs); err != nil {
			if errors.Is(err, schools.ErrNotFound) {
				return connector.Response{}, fmt.Errorf("%s: %w", schools.Plain(err), effects.ErrFatal)
			}
			return connector.Response{}, classify(err)
		}
	} else if in.ClassID, err = a.class(ctx, c, req); err != nil {
		return connector.Response{}, err
	}
	r, err := a.ops.SendRemindersOnce(ctx, c.Tenant, in, "workflow:"+c.Run.String())
	if err != nil {
		return connector.Response{}, classify(err)
	}
	results := make([]any, 0, len(r.Results))
	for _, x := range r.Results {
		row := map[string]any{"student_id": x.StudentID.String(), "admission_number": x.AdmissionNumber, "rung": x.Rung, "status": x.Status, "balance_kobo": x.BalanceKobo, "nobody": x.Nobody}
		if x.Reason != "" {
			row["reason"] = x.Reason
		}
		results = append(results, row)
	}
	byRung := map[string]any{}
	for k, v := range r.ByRung {
		byRung[k] = v
	}
	return connector.Response{Output: map[string]any{"sent": r.Sent, "skipped": r.Skipped, "by_rung": byRung, "results": results}}, nil
}

func (a *actions) planDecisions(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	f := schools.PlanDecisionFilter{Untold: true}
	f.Status, _ = req.Input["status"].(string)
	if v, ok := req.Input["untold"].(bool); ok {
		f.Untold = v
	}
	if v, ok := intIn(req.Input["since_days"]); ok {
		f.SinceDays = v
	}
	if v, ok := intIn(req.Input["limit"]); ok {
		f.Limit = v
	}
	list, err := a.ops.ListPlanDecisions(ctx, c.Tenant, f)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	out := make([]any, 0, len(list))
	for _, p := range list {
		out = append(out, planOut(p))
	}
	return connector.Response{Output: map[string]any{"decisions": out, "count": len(out)}}, nil
}

func planOut(p schools.PlanRequest) map[string]any {
	ins := p.DecidedInstalments
	if p.Status != "approved" {
		ins = p.Instalments
	}
	list := make([]any, 0, len(ins))
	for _, in := range ins {
		list = append(list, map[string]any{"amount_kobo": in.AmountKobo, "due_on": in.DueOn})
	}
	out := map[string]any{"id": p.ID.String(), "student_id": p.StudentID.String(), "admission_number": p.AdmissionNumber, "class": p.Class, "status": p.Status, "instalments": list, "told": p.ToldAt != nil}
	if p.DecidedAt != nil {
		out["decided_at"] = p.DecidedAt.UTC().Format(time.RFC3339)
	}
	if p.DecisionNote != "" {
		out["note"] = p.DecisionNote
	}
	return out
}

// tellPlanDecision tells a family of a decision, once per decision.
func (a *actions) tellPlanDecision(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	if req.IdempotencyKey == "" {
		return connector.Response{}, fmt.Errorf("tell_plan_decision runs as a workflow step, with its key: %w", effects.ErrFatal)
	}
	plan, _ := req.Input["plan"].(string)
	if strings.TrimSpace(plan) == "" {
		return connector.Response{}, fmt.Errorf("the plan request's id is required: %w", effects.ErrFatal)
	}
	t, err := a.ops.TellPlan(ctx, c.Tenant, strings.TrimSpace(plan))
	if err != nil {
		return connector.Response{}, classify(err)
	}
	channels := map[string]any{}
	for k, v := range t.Sent.Channels {
		channels[k] = v
	}
	return connector.Response{Output: map[string]any{"plan_id": t.Plan.ID.String(), "student_id": t.Plan.StudentID.String(), "admission_number": t.Plan.AdmissionNumber,
		"status": t.Plan.Status, "told_before": t.Told, "channels": channels, "nobody": !t.Told && t.Sent.Nobody}}, nil
}

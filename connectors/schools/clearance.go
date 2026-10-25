package schools

import (
	"context"
	"fmt"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/schools"
)

// Clearance (docs/spec/schools.md 12, B8; docs/schools.md#clearance):
// one student's clearance, or a class's with the balances, for workflows
// that may see the school's money; and clearance_list, the teachers'
// lists with no amounts, one summary per class ready to send.

func clearanceOut(r schools.ClearanceRow, amounts bool) map[string]any {
	out := map[string]any{"student_id": r.StudentID.String(), "admission_number": r.AdmissionNumber, "name": r.Name, "class": r.Class, "class_id": r.ClassID.String(),
		"student_status": r.StudentStatus, "status": r.Status, "reason": r.Reason, "invoiced": r.Invoiced}
	if amounts {
		out["total_kobo"], out["paid_kobo"], out["balance_kobo"] = r.TotalKobo, r.PaidKobo, r.BalanceKobo
	}
	if r.Override != nil {
		out["override_ends_on"] = r.Override.EndsOn
	}
	if r.Plan != nil {
		out["plan"] = r.Plan.State
	}
	return out
}

// clearance answers one student's clearance (student), or every student
// of a class's (class), for a term (the current one by default), with
// the balances.
func (a *actions) clearance(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	t, err := a.term(ctx, c, req)
	if err != nil {
		return connector.Response{}, err
	}
	student, _ := req.Input["student"].(string)
	if student != "" {
		r, err := a.ops.ClearanceOf(ctx, c.Tenant, student, t.ID)
		if err != nil {
			return connector.Response{}, classify(err)
		}
		out := clearanceOut(r, true)
		out["term_id"], out["term"] = t.ID.String(), t.Name+" "+t.Session
		return connector.Response{Output: out}, nil
	}
	class, err := a.class(ctx, c, req)
	if err != nil {
		return connector.Response{}, err
	}
	if class == nil {
		return connector.Response{}, fmt.Errorf("give a student (id or admission number) or a class: %w", effects.ErrFatal)
	}
	l, err := a.ops.ListClearance(ctx, c.Tenant, t.ID, class)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	rows := make([]any, 0, len(l.Rows))
	for _, r := range l.Rows {
		rows = append(rows, clearanceOut(r, true))
	}
	return connector.Response{Output: map[string]any{"term_id": t.ID.String(), "term": l.Term, "students": rows, "count": len(rows),
		"cleared": l.Totals.Cleared, "partly_paid": l.Totals.PartlyPaid, "not_paid": l.Totals.NotPaid}}, nil
}

// clearanceList is the teachers' lists: per class (one, or every class
// of the term), who is cleared and who is not, with a message ready to
// send and no amounts.
func (a *actions) clearanceList(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	t, err := a.term(ctx, c, req)
	if err != nil {
		return connector.Response{}, err
	}
	class, err := a.class(ctx, c, req)
	if err != nil {
		return connector.Response{}, err
	}
	l, err := a.ops.ListClearance(ctx, c.Tenant, t.ID, class)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	var classes []any
	for _, s := range schools.Summarise(l) {
		classes = append(classes, map[string]any{"class_id": s.ClassID.String(), "class": s.Class, "students": s.Students, "cleared": s.Cleared,
			"partly_paid": s.PartlyPaid, "not_paid": s.NotPaid, "not_cleared": toAny(s.NotCleared), "text": s.Text})
	}
	if classes == nil {
		classes = []any{}
	}
	return connector.Response{Output: map[string]any{"term_id": t.ID.String(), "term": l.Term, "classes": classes, "count": len(classes),
		"students": l.Totals.Students, "cleared": l.Totals.Cleared, "partly_paid": l.Totals.PartlyPaid, "not_paid": l.Totals.NotPaid}}, nil
}

func toAny(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

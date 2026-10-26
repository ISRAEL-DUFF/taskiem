// Package schools is the schools platform connector (docs/schools.md,
// decision 0046): workflows read the business's school record (students
// and their guardians, classes, the current term). Like invoices@1 it acts
// on Taskiem's own records, for the tenant of the run calling it
// (connector.CallerFrom), never one named in its input, and its personal
// data (names, phones, emails) is declared so a run's history seals it.
// It has no action that changes the record.
package schools

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/schools"
)

//go:embed manifest.yaml
var manifest []byte

// Ops is what the actions call: schools.Service.
type Ops interface {
	FindClass(ctx context.Context, tenant uuid.UUID, idOrLabel string) (schools.Class, error)
	ListStudents(ctx context.Context, tenant uuid.UUID, f schools.Filter) ([]schools.Student, int, error)
	GetStudent(ctx context.Context, tenant uuid.UUID, idOrNumber string) (schools.StudentDetail, error)
	ListClasses(ctx context.Context, tenant uuid.UUID) ([]schools.Class, error)
	Current(ctx context.Context, tenant uuid.UUID) (schools.Term, bool, error)
	ResolveTerm(ctx context.Context, tenant uuid.UUID, id string) (schools.Term, error)
	ListFees(ctx context.Context, tenant, term uuid.UUID, class *uuid.UUID) ([]schools.ClassFees, error)
	ListTermInvoices(ctx context.Context, tenant, term uuid.UUID, class *uuid.UUID, status string) ([]schools.TermInvoice, error)
	InvoiceTermOnce(ctx context.Context, tenant, term uuid.UUID, key string, send bool, by string) (schools.TermInvoiced, error)
	IssueAccount(ctx context.Context, tenant uuid.UUID, student string, in schools.IssueIn, by string) (schools.StudentAccount, error)
	FamilyLinkFor(ctx context.Context, tenant uuid.UUID, family, student string) (schools.FamilyLinkMinted, error)
	ListDebtors(ctx context.Context, tenant uuid.UUID, f schools.DebtorFilter) (schools.Debtors, error)
	FindStudents(ctx context.Context, tenant uuid.UUID, refs []string) ([]uuid.UUID, error)
	SendRemindersOnce(ctx context.Context, tenant uuid.UUID, in schools.RemindIn, by string) (schools.Reminded, error)
	ListPlanDecisions(ctx context.Context, tenant uuid.UUID, f schools.PlanDecisionFilter) ([]schools.PlanRequest, error)
	TellPlan(ctx context.Context, tenant uuid.UUID, plan string) (schools.PlanTold, error)
	SendReceiptOnce(ctx context.Context, tenant uuid.UUID, idOrNumber, by string) (schools.ReceiptSent, error)
	SendAccountNumberOnce(ctx context.Context, tenant uuid.UUID, idOrNumber, key, by string) (schools.AccountSent, error)
	ClearanceOf(ctx context.Context, tenant uuid.UUID, student string, term uuid.UUID) (schools.ClearanceRow, error)
	ListClearance(ctx context.Context, tenant, term uuid.UUID, class *uuid.UUID) (schools.ClearanceList, error)
	CloseFor(ctx context.Context, tenant uuid.UUID, day, by string) (schools.Close, error)
}

var _ Ops = schools.Service{}

// New returns the connector; ops nil makes every action fail plainly.
func New(ops Ops) *connector.Connector {
	m := connector.MustParse(manifest)
	a := &actions{ops: ops}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"list_students":       connector.ActionFunc(a.listStudents),
		"get_student":         connector.ActionFunc(a.getStudent),
		"list_classes":        connector.ActionFunc(a.listClasses),
		"current_term":        connector.ActionFunc(a.currentTerm),
		"list_fee_items":      connector.ActionFunc(a.listFeeItems),
		"term_invoices":       connector.ActionFunc(a.termInvoices),
		"invoice_term":        connector.ActionFunc(a.invoiceTerm),
		"issue_account":       connector.ActionFunc(a.issueAccount),
		"family_link":         connector.ActionFunc(a.familyLink),
		"list_debtors":        connector.ActionFunc(a.listDebtors),
		"send_reminders":      connector.ActionFunc(a.sendReminders),
		"plan_decisions":      connector.ActionFunc(a.planDecisions),
		"tell_plan_decision":  connector.ActionFunc(a.tellPlanDecision),
		"send_receipt":        connector.ActionFunc(a.sendReceipt),
		"send_account_number": connector.ActionFunc(a.sendAccountNumber),
		"clearance":           connector.ActionFunc(a.clearance),
		"clearance_list":      connector.ActionFunc(a.clearanceList),
		"daily_close":         connector.ActionFunc(a.dailyClose),
	}}
}

// term is the input's term, else the current one.
func (a *actions) term(ctx context.Context, c connector.Caller, req connector.Request) (schools.Term, error) {
	id, _ := req.Input["term"].(string)
	t, err := a.ops.ResolveTerm(ctx, c.Tenant, id)
	if errors.Is(err, schools.ErrNotFound) {
		return t, fmt.Errorf("the school has no term %q: %w", id, effects.ErrFatal)
	}
	return t, classify(err)
}

// class is the input's class, nil for none.
func (a *actions) class(ctx context.Context, c connector.Caller, req connector.Request) (*uuid.UUID, error) {
	name, _ := req.Input["class"].(string)
	if name == "" {
		return nil, nil
	}
	cl, err := a.ops.FindClass(ctx, c.Tenant, name)
	if errors.Is(err, schools.ErrNotFound) {
		return nil, fmt.Errorf("the school has no class %q: %w", name, effects.ErrFatal)
	}
	if err != nil {
		return nil, classify(err)
	}
	return &cl.ID, nil
}

func (a *actions) listFeeItems(ctx context.Context, req connector.Request) (connector.Response, error) {
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
	list, err := a.ops.ListFees(ctx, c.Tenant, t.ID, class)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	out := make([]any, 0, len(list))
	for _, f := range list {
		items := make([]any, 0, len(f.Items))
		for _, it := range f.Items {
			x := map[string]any{"id": it.ID.String(), "name": it.Name, "amount_kobo": it.AmountKobo, "kind": it.Kind}
			if it.PassThroughFor != "" {
				x["pass_through_for"] = it.PassThroughFor
			}
			items = append(items, x)
		}
		out = append(out, map[string]any{"class_id": f.ClassID.String(), "class": f.Class, "required_kobo": f.RequiredKobo, "pass_through_kobo": f.PassThroughKobo, "items": items})
	}
	return connector.Response{Output: map[string]any{"term_id": t.ID.String(), "term": t.Name + " " + t.Session, "classes": out}}, nil
}

func (a *actions) termInvoices(ctx context.Context, req connector.Request) (connector.Response, error) {
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
	t, err := a.term(ctx, c, req)
	if err != nil {
		return connector.Response{}, err
	}
	class, err := a.class(ctx, c, req)
	if err != nil {
		return connector.Response{}, err
	}
	status, _ := req.Input["status"].(string)
	list, err := a.ops.ListTermInvoices(ctx, c.Tenant, t.ID, class, status)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	out := make([]any, 0, min(len(list), limit))
	for _, x := range list[:min(len(list), limit)] {
		out = append(out, map[string]any{"number": x.Number, "status": x.Status, "student_id": x.StudentID.String(), "admission_number": x.AdmissionNumber,
			"class": x.Class, "class_id": x.ClassID.String(), "total_kobo": x.TotalKobo, "paid_kobo": x.PaidKobo, "due_kobo": x.DueKobo})
	}
	return connector.Response{Output: map[string]any{"invoices": out, "count": len(out), "total": len(list)}}, nil
}

func (a *actions) invoiceTerm(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	if req.IdempotencyKey == "" {
		return connector.Response{}, fmt.Errorf("invoice_term runs as a workflow step, with its key: %w", effects.ErrFatal)
	}
	t, err := a.term(ctx, c, req)
	if err != nil {
		return connector.Response{}, err
	}
	send, _ := req.Input["send"].(bool)
	r, err := a.ops.InvoiceTermOnce(ctx, c.Tenant, t.ID, req.IdempotencyKey, send, "workflow:"+c.Run.String())
	if err != nil {
		return connector.Response{}, classify(err)
	}
	return connector.Response{Output: map[string]any{"term_id": t.ID.String(), "term": t.Name + " " + t.Session, "enrolled": r.Enrolled, "to_invoice": r.ToInvoice,
		"created": r.Created, "already_invoiced": r.AlreadyInvoiced, "total_kobo": r.TotalKobo, "pass_through_kobo": r.PassThroughKobo,
		"no_contact": len(r.NoContact), "no_fees": len(r.NoFees), "nothing_to_pay": len(r.NothingToPay), "needs_approval": len(r.NeedsApproval), "requested": r.Requested,
		"sent": r.Sent, "failed": r.Failed}}, nil
}

// issueAccount issues a student's account number on the school's issuing
// connection: exactly once per student and connection, so a retry of the
// step (or a student who has one) answers the same account.
func (a *actions) issueAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	if req.IdempotencyKey == "" {
		return connector.Response{}, fmt.Errorf("issue_account runs as a workflow step, with its key: %w", effects.ErrFatal)
	}
	student, _ := req.Input["student"].(string)
	if student == "" {
		return connector.Response{}, fmt.Errorf("a student's id or admission number is required: %w", effects.ErrFatal)
	}
	in := schools.IssueIn{}
	in.BVN, _ = req.Input["bvn"].(string)
	in.NIN, _ = req.Input["nin"].(string)
	x, err := a.ops.IssueAccount(ctx, c.Tenant, student, in, "workflow:"+c.Run.String())
	switch {
	case schools.ErrIssue(err):
		return connector.Response{}, fmt.Errorf("%s: %w", schools.Plain(err), effects.ErrFatal)
	case errors.Is(err, schools.ErrState) && strings.Contains(err.Error(), "being issued now"):
		return connector.Response{}, fmt.Errorf("%s: %w", schools.Plain(err), effects.ErrRetryable)
	case err != nil:
		return connector.Response{}, classify(err)
	}
	out := map[string]any{"student_id": x.StudentID.String(), "connection": x.Connection, "status": x.Status, "account_number": x.AccountNumber,
		"bank_name": x.BankName, "named_by_rail": x.NamedByRail, "reference": x.Reference}
	if x.AccountName != "" {
		out["account_name"] = x.AccountName
	}
	return connector.Response{Output: out}, nil
}

type actions struct{ ops Ops }

func (a *actions) caller(ctx context.Context) (connector.Caller, error) {
	c, ok := connector.CallerFrom(ctx)
	if !ok {
		return c, fmt.Errorf("school actions run only in a workflow run: %w", effects.ErrFatal)
	}
	if a.ops == nil {
		return c, fmt.Errorf("schools are not available on this deployment: %w", effects.ErrFatal)
	}
	return c, nil
}

func (a *actions) listStudents(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	status, _ := req.Input["status"].(string)
	switch status {
	case "":
		status = schools.Active
	case "any":
		status = ""
	}
	limit := 200
	if v, ok := intIn(req.Input["limit"]); ok {
		limit = v
	}
	if limit < 1 || limit > schools.MaxListed {
		return connector.Response{}, fmt.Errorf("limit is 1 to %d: %w", schools.MaxListed, effects.ErrFatal)
	}
	var class *uuid.UUID
	if name, _ := req.Input["class"].(string); name != "" {
		cl, err := a.ops.FindClass(ctx, c.Tenant, name)
		if errors.Is(err, schools.ErrNotFound) {
			return connector.Response{}, fmt.Errorf("the school has no class %q: %w", name, effects.ErrFatal)
		}
		if err != nil {
			return connector.Response{}, classify(err)
		}
		class = &cl.ID
	}
	without, _ := req.Input["without_account"].(bool)
	list, total, err := a.ops.ListStudents(ctx, c.Tenant, schools.Filter{ClassID: class, Status: status, Limit: limit, WithoutAccount: without})
	if err != nil {
		return connector.Response{}, classify(err)
	}
	out := make([]any, 0, len(list))
	for _, st := range list {
		out = append(out, studentOut(st))
	}
	return connector.Response{Output: map[string]any{"students": out, "count": len(out), "total": total}}, nil
}

func studentOut(st schools.Student) map[string]any {
	x := map[string]any{"id": st.ID.String(), "admission_number": st.AdmissionNumber, "name": st.Name, "class": st.Class,
		"class_id": st.ClassID.String(), "status": st.Status}
	if st.FamilyID != nil {
		x["family_id"] = st.FamilyID.String()
	}
	return x
}

func (a *actions) getStudent(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	id, _ := req.Input["student"].(string)
	if id == "" {
		return connector.Response{}, fmt.Errorf("a student's id or admission number is required: %w", effects.ErrFatal)
	}
	d, err := a.ops.GetStudent(ctx, c.Tenant, id)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	out := studentOut(d.Student)
	if d.DateOfBirth != "" {
		out["date_of_birth"] = d.DateOfBirth
	}
	guardians := []any{}
	if d.Family != nil {
		for _, g := range d.Family.Guardians {
			x := map[string]any{"name": g.Name, "phone": g.Phone}
			if g.Email != "" {
				x["email"] = g.Email
			}
			if g.Relationship != "" {
				x["relationship"] = g.Relationship
			}
			guardians = append(guardians, x)
		}
	}
	out["guardians"] = guardians
	return connector.Response{Output: out}, nil
}

func (a *actions) listClasses(ctx context.Context, _ connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	list, err := a.ops.ListClasses(ctx, c.Tenant)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	out := make([]any, 0, len(list))
	for _, cl := range list {
		out = append(out, map[string]any{"id": cl.ID.String(), "name": cl.Name, "arm": cl.Arm, "label": cl.Label, "order": cl.Order, "students": cl.Students})
	}
	return connector.Response{Output: map[string]any{"classes": out, "count": len(out)}}, nil
}

func (a *actions) currentTerm(ctx context.Context, _ connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	t, ok, err := a.ops.Current(ctx, c.Tenant)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	if !ok {
		return connector.Response{Output: map[string]any{"found": false}}, nil
	}
	return connector.Response{Output: map[string]any{"found": true, "id": t.ID.String(), "name": t.Name, "session": t.Session,
		"starts_on": t.StartsOn, "ends_on": t.EndsOn, "fees_due_on": t.FeesDueOn, "enrolled": t.Enrolled}}, nil
}

// familyLink mints a fresh link to a family's page, for a message or a
// template: by family id, or by a student's id or admission number.
func (a *actions) familyLink(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	family, _ := req.Input["family"].(string)
	student, _ := req.Input["student"].(string)
	if strings.TrimSpace(family) == "" && strings.TrimSpace(student) == "" {
		return connector.Response{}, fmt.Errorf("family_link needs a family or a student: %w", effects.ErrFatal)
	}
	l, err := a.ops.FamilyLinkFor(ctx, c.Tenant, strings.TrimSpace(family), strings.TrimSpace(student))
	if err != nil {
		return connector.Response{}, classify(err)
	}
	return connector.Response{Output: map[string]any{"link": l.URL, "expires_at": l.ExpiresAt.UTC().Format(time.RFC3339)}}, nil
}

// intIn reads a whole number from a step's input (JSON numbers arrive as
// float64).
func intIn(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	}
	return 0, false
}

func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, schools.ErrNotFound), errors.Is(err, schools.ErrInvalid), errors.Is(err, schools.ErrState), errors.Is(err, schools.ErrOff):
		return fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	return err
}

// Late is Ops set after the connector is registered: the registry is built
// before the vault and the database the record needs.
type Late struct {
	mu  sync.RWMutex
	ops Ops
}

// Set binds the operations.
func (l *Late) Set(ops Ops) { l.mu.Lock(); l.ops = ops; l.mu.Unlock() }

func (l *Late) get() (Ops, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.ops == nil {
		return nil, fmt.Errorf("schools are not available on this deployment: %w", effects.ErrFatal)
	}
	return l.ops, nil
}

// IssueAccount implements Ops.
func (l *Late) IssueAccount(ctx context.Context, t uuid.UUID, student string, in schools.IssueIn, by string) (schools.StudentAccount, error) {
	o, err := l.get()
	if err != nil {
		return schools.StudentAccount{}, err
	}
	return o.IssueAccount(ctx, t, student, in, by)
}

// FamilyLinkFor implements Ops.
func (l *Late) FamilyLinkFor(ctx context.Context, t uuid.UUID, family, student string) (schools.FamilyLinkMinted, error) {
	o, err := l.get()
	if err != nil {
		return schools.FamilyLinkMinted{}, err
	}
	return o.FamilyLinkFor(ctx, t, family, student)
}

// FindClass implements Ops.
func (l *Late) FindClass(ctx context.Context, t uuid.UUID, idOrLabel string) (schools.Class, error) {
	o, err := l.get()
	if err != nil {
		return schools.Class{}, err
	}
	return o.FindClass(ctx, t, idOrLabel)
}

// ListStudents implements Ops.
func (l *Late) ListStudents(ctx context.Context, t uuid.UUID, f schools.Filter) ([]schools.Student, int, error) {
	o, err := l.get()
	if err != nil {
		return nil, 0, err
	}
	return o.ListStudents(ctx, t, f)
}

// GetStudent implements Ops.
func (l *Late) GetStudent(ctx context.Context, t uuid.UUID, idOrNumber string) (schools.StudentDetail, error) {
	o, err := l.get()
	if err != nil {
		return schools.StudentDetail{}, err
	}
	return o.GetStudent(ctx, t, idOrNumber)
}

// ListClasses implements Ops.
func (l *Late) ListClasses(ctx context.Context, t uuid.UUID) ([]schools.Class, error) {
	o, err := l.get()
	if err != nil {
		return nil, err
	}
	return o.ListClasses(ctx, t)
}

// Current implements Ops.
func (l *Late) Current(ctx context.Context, t uuid.UUID) (schools.Term, bool, error) {
	o, err := l.get()
	if err != nil {
		return schools.Term{}, false, err
	}
	return o.Current(ctx, t)
}

// ResolveTerm implements Ops.
func (l *Late) ResolveTerm(ctx context.Context, t uuid.UUID, id string) (schools.Term, error) {
	o, err := l.get()
	if err != nil {
		return schools.Term{}, err
	}
	return o.ResolveTerm(ctx, t, id)
}

// ListFees implements Ops.
func (l *Late) ListFees(ctx context.Context, t, term uuid.UUID, class *uuid.UUID) ([]schools.ClassFees, error) {
	o, err := l.get()
	if err != nil {
		return nil, err
	}
	return o.ListFees(ctx, t, term, class)
}

// ListTermInvoices implements Ops.
func (l *Late) ListTermInvoices(ctx context.Context, t, term uuid.UUID, class *uuid.UUID, status string) ([]schools.TermInvoice, error) {
	o, err := l.get()
	if err != nil {
		return nil, err
	}
	return o.ListTermInvoices(ctx, t, term, class, status)
}

// ListDebtors implements Ops.
func (l *Late) ListDebtors(ctx context.Context, t uuid.UUID, f schools.DebtorFilter) (schools.Debtors, error) {
	o, err := l.get()
	if err != nil {
		return schools.Debtors{}, err
	}
	return o.ListDebtors(ctx, t, f)
}

// FindStudents implements Ops.
func (l *Late) FindStudents(ctx context.Context, t uuid.UUID, refs []string) ([]uuid.UUID, error) {
	o, err := l.get()
	if err != nil {
		return nil, err
	}
	return o.FindStudents(ctx, t, refs)
}

// SendRemindersOnce implements Ops.
func (l *Late) SendRemindersOnce(ctx context.Context, t uuid.UUID, in schools.RemindIn, by string) (schools.Reminded, error) {
	o, err := l.get()
	if err != nil {
		return schools.Reminded{}, err
	}
	return o.SendRemindersOnce(ctx, t, in, by)
}

// ListPlanDecisions implements Ops.
func (l *Late) ListPlanDecisions(ctx context.Context, t uuid.UUID, f schools.PlanDecisionFilter) ([]schools.PlanRequest, error) {
	o, err := l.get()
	if err != nil {
		return nil, err
	}
	return o.ListPlanDecisions(ctx, t, f)
}

// TellPlan implements Ops.
func (l *Late) TellPlan(ctx context.Context, t uuid.UUID, plan string) (schools.PlanTold, error) {
	o, err := l.get()
	if err != nil {
		return schools.PlanTold{}, err
	}
	return o.TellPlan(ctx, t, plan)
}

// SendReceiptOnce implements Ops.
func (l *Late) SendReceiptOnce(ctx context.Context, t uuid.UUID, idOrNumber, by string) (schools.ReceiptSent, error) {
	o, err := l.get()
	if err != nil {
		return schools.ReceiptSent{}, err
	}
	return o.SendReceiptOnce(ctx, t, idOrNumber, by)
}

// SendAccountNumberOnce implements Ops.
func (l *Late) SendAccountNumberOnce(ctx context.Context, t uuid.UUID, idOrNumber, key, by string) (schools.AccountSent, error) {
	o, err := l.get()
	if err != nil {
		return schools.AccountSent{}, err
	}
	return o.SendAccountNumberOnce(ctx, t, idOrNumber, key, by)
}

// InvoiceTermOnce implements Ops.
func (l *Late) InvoiceTermOnce(ctx context.Context, t, term uuid.UUID, key string, send bool, by string) (schools.TermInvoiced, error) {
	o, err := l.get()
	if err != nil {
		return schools.TermInvoiced{}, err
	}
	return o.InvoiceTermOnce(ctx, t, term, key, send, by)
}

// ClearanceOf implements Ops.
func (l *Late) ClearanceOf(ctx context.Context, t uuid.UUID, student string, term uuid.UUID) (schools.ClearanceRow, error) {
	o, err := l.get()
	if err != nil {
		return schools.ClearanceRow{}, err
	}
	return o.ClearanceOf(ctx, t, student, term)
}

// ListClearance implements Ops.
func (l *Late) ListClearance(ctx context.Context, t, term uuid.UUID, class *uuid.UUID) (schools.ClearanceList, error) {
	o, err := l.get()
	if err != nil {
		return schools.ClearanceList{}, err
	}
	return o.ListClearance(ctx, t, term, class)
}

// CloseFor implements Ops.
func (l *Late) CloseFor(ctx context.Context, t uuid.UUID, day, by string) (schools.Close, error) {
	o, err := l.get()
	if err != nil {
		return schools.Close{}, err
	}
	return o.CloseFor(ctx, t, day, by)
}

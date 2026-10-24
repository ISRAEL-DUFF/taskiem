package schools_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	conn "github.com/israel-duff/taskiem/connectors/schools"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/schools"
)

type ops struct {
	class  *uuid.UUID
	status string
}

var jss1 = schools.Class{ID: uuid.New(), Name: "JSS 1", Arm: "A", Label: "JSS 1 A", Order: 7, Students: 1}

func (o *ops) FindClass(_ context.Context, _ uuid.UUID, s string) (schools.Class, error) {
	if s == "JSS 1 A" || s == jss1.ID.String() {
		return jss1, nil
	}
	return schools.Class{}, schools.ErrNotFound
}
func (o *ops) ListStudents(_ context.Context, _ uuid.UUID, class *uuid.UUID, status string, _ int) ([]schools.Student, int, error) {
	o.class, o.status = class, status
	return []schools.Student{{ID: uuid.New(), AdmissionNumber: "GCA/001", Name: "Ebube Eze", ClassID: jss1.ID, Class: "JSS 1 A", Status: "active"}}, 1, nil
}
func (o *ops) GetStudent(_ context.Context, _ uuid.UUID, id string) (schools.StudentDetail, error) {
	if id != "GCA/001" {
		return schools.StudentDetail{}, schools.ErrNotFound
	}
	fam := uuid.New()
	return schools.StudentDetail{Student: schools.Student{ID: uuid.New(), AdmissionNumber: "GCA/001", Name: "Ebube Eze", Class: "JSS 1 A", Status: "active", FamilyID: &fam},
		Family: &schools.Family{ID: fam, Guardians: []schools.Guardian{{Name: "Ngozi Eze", Phone: "+2348031234567", Relationship: "Mother"}}}}, nil
}
func (o *ops) ListClasses(context.Context, uuid.UUID) ([]schools.Class, error) {
	return []schools.Class{jss1}, nil
}
func (o *ops) Current(context.Context, uuid.UUID) (schools.Term, bool, error) {
	return schools.Term{}, false, schools.ErrOff
}

var first = schools.Term{ID: uuid.New(), Name: "First term", Session: "2026/2027"}

func (o *ops) ResolveTerm(_ context.Context, _ uuid.UUID, id string) (schools.Term, error) {
	if id == "" || id == first.ID.String() {
		return first, nil
	}
	return schools.Term{}, schools.ErrNotFound
}
func (o *ops) ListFees(_ context.Context, _ uuid.UUID, _ uuid.UUID, class *uuid.UUID) ([]schools.ClassFees, error) {
	o.class = class
	return []schools.ClassFees{{ClassID: jss1.ID, Class: "JSS 1 A", RequiredKobo: 100, Items: []schools.FeeItem{{ID: uuid.New(), Name: "WAEC", AmountKobo: 100, Kind: "required", PassThroughFor: "WAEC"}}}}, nil
}
func (o *ops) ListTermInvoices(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ *uuid.UUID, status string) ([]schools.TermInvoice, error) {
	o.status = status
	return []schools.TermInvoice{{Number: "INV-0001", Status: "sent", AdmissionNumber: "GCA/001", Name: "Ebube Eze", TotalKobo: 100, DueKobo: 100}}, nil
}

type invoiced struct {
	key  string
	send bool
}

var lastInvoiced invoiced

func (o *ops) InvoiceTermOnce(_ context.Context, _ uuid.UUID, _ uuid.UUID, key string, send bool, _ string) (schools.TermInvoiced, error) {
	lastInvoiced = invoiced{key, send}
	return schools.TermInvoiced{TermPlan: schools.TermPlan{Enrolled: 3, ToInvoice: 2, NoContact: []schools.PlanStudent{{}}}, Created: 2, Sent: 2}, nil
}

var issued []string

func (o *ops) IssueAccount(_ context.Context, _ uuid.UUID, student string, in schools.IssueIn, _ string) (schools.StudentAccount, error) {
	issued = append(issued, student+":"+in.BVN)
	switch student {
	case "GCA/002":
		return schools.StudentAccount{}, errors.Join(schools.ErrNotFound)
	case "GCA/003":
		return schools.StudentAccount{}, schools.IssueRefusal("iswallet KYC_REQUIRED: the director's BVN is missing")
	}
	return schools.StudentAccount{StudentID: uuid.New(), Connection: "iswallet/main", Status: "issued", AccountNumber: "9912345678", BankName: "Wema Bank",
		AccountName: "GREENFIELD / EBUBE EZE", NamedByRail: true, Reference: "tsk_abc"}, nil
}

// issue_account: under the step's key; the account answered; the rail's
// refusal fails the step with its reason, never retried.
func TestIssueAccount(t *testing.T) {
	c := conn.New(&ops{})
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: uuid.New(), Run: uuid.New(), Step: "s"})
	if _, err := c.Actions["issue_account"].Execute(ctx, connector.Request{Input: map[string]any{"student": "GCA/001"}}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("no key: %v", err)
	}
	r, err := c.Actions["issue_account"].Execute(ctx, connector.Request{Input: map[string]any{"student": "GCA/001", "bvn": "12345678901"}, IdempotencyKey: "tsk_step"})
	if err != nil {
		t.Fatal(err)
	}
	if out := r.Output.(map[string]any); out["account_number"] != "9912345678" || out["connection"] != "iswallet/main" || out["named_by_rail"] != true || issued[0] != "GCA/001:12345678901" {
		t.Fatalf("issued %v %v", out, issued)
	}
	_, err = c.Actions["issue_account"].Execute(ctx, connector.Request{Input: map[string]any{"student": "GCA/003"}, IdempotencyKey: "tsk_step2"})
	if !errors.Is(err, effects.ErrFatal) || err.Error() != "iswallet KYC_REQUIRED: the director's BVN is missing: fatal" {
		t.Errorf("refused: %v", err)
	}
	if _, err := c.Actions["issue_account"].Execute(ctx, connector.Request{Input: map[string]any{"student": "GCA/002"}, IdempotencyKey: "tsk_step3"}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("an unknown student: %v", err)
	}
}

// The fee and term invoice actions: the current term by default, no
// names in a term's invoices, invoice_term under the step's key.
func TestFeeActions(t *testing.T) {
	o := &ops{}
	c := conn.New(o)
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: uuid.New(), Run: uuid.New(), Step: "s"})
	r, err := c.Actions["list_fee_items"].Execute(ctx, connector.Request{Input: map[string]any{"class": "JSS 1 A"}})
	if err != nil {
		t.Fatal(err)
	}
	cls := r.Output.(map[string]any)["classes"].([]any)
	if len(cls) != 1 || o.class == nil || cls[0].(map[string]any)["items"].([]any)[0].(map[string]any)["pass_through_for"] != "WAEC" {
		t.Fatalf("fees %v", r.Output)
	}
	if _, err := c.Actions["list_fee_items"].Execute(ctx, connector.Request{Input: map[string]any{"term": "nope"}}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("an unknown term: %v", err)
	}
	r, err = c.Actions["term_invoices"].Execute(ctx, connector.Request{Input: map[string]any{"status": "sent"}})
	if err != nil {
		t.Fatal(err)
	}
	inv := r.Output.(map[string]any)["invoices"].([]any)[0].(map[string]any)
	if _, named := inv["name"]; named || inv["number"] != "INV-0001" || o.status != "sent" {
		t.Fatalf("term invoices %v", inv)
	}
	if _, err := c.Actions["invoice_term"].Execute(ctx, connector.Request{Input: map[string]any{}}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("no key: %v", err)
	}
	r, err = c.Actions["invoice_term"].Execute(ctx, connector.Request{Input: map[string]any{"send": true}, IdempotencyKey: "tsk_step"})
	if err != nil {
		t.Fatal(err)
	}
	if out := r.Output.(map[string]any); out["created"] != 2 || out["no_contact"] != 1 || lastInvoiced != (invoiced{"tsk_step", true}) {
		t.Fatalf("invoiced %v %+v", out, lastInvoiced)
	}
}

func TestActions(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(conn.New(nil)); err != nil {
		t.Fatal(err)
	}
	o := &ops{}
	c := conn.New(o)
	// Outside a run, nothing.
	if _, err := c.Actions["list_classes"].Execute(context.Background(), connector.Request{}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("outside a run: %v", err)
	}
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: uuid.New(), Run: uuid.New(), Step: "s"})
	r, err := c.Actions["list_students"].Execute(ctx, connector.Request{Input: map[string]any{"class": "JSS 1 A"}})
	if err != nil {
		t.Fatal(err)
	}
	out := r.Output.(map[string]any)
	if out["count"] != 1 || o.class == nil || *o.class != jss1.ID || o.status != "active" {
		t.Fatalf("list %v %v %q", out, o.class, o.status)
	}
	if _, err := c.Actions["list_students"].Execute(ctx, connector.Request{Input: map[string]any{"status": "any"}}); err != nil || o.status != "" || o.class != nil {
		t.Fatalf("any: %v %q", err, o.status)
	}
	if _, err := c.Actions["list_students"].Execute(ctx, connector.Request{Input: map[string]any{"class": "JSS 9"}}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("an unknown class: %v", err)
	}
	r, err = c.Actions["get_student"].Execute(ctx, connector.Request{Input: map[string]any{"student": "GCA/001"}})
	if err != nil {
		t.Fatal(err)
	}
	if g := r.Output.(map[string]any)["guardians"].([]any)[0].(map[string]any); g["phone"] != "+2348031234567" || g["relationship"] != "Mother" {
		t.Fatalf("guardian %v", g)
	}
	if _, err := c.Actions["get_student"].Execute(ctx, connector.Request{Input: map[string]any{"student": "X"}}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("missing student: %v", err)
	}
	// The area turned off fails the step.
	if _, err := c.Actions["current_term"].Execute(ctx, connector.Request{}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("off: %v", err)
	}
	// The personal data is declared, so a run's history seals it.
	m := c.Manifest
	if len(m.Actions["get_student"].PII) < 3 || len(m.Actions["list_students"].PII) != 1 {
		t.Errorf("pii %v %v", m.Actions["get_student"].PII, m.Actions["list_students"].PII)
	}
	late := &conn.Late{}
	if _, err := conn.New(late).Actions["list_classes"].Execute(ctx, connector.Request{}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("unbound: %v", err)
	}
}

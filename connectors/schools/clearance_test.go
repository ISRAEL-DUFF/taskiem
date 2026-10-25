package schools_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	conn "github.com/israel-duff/taskiem/connectors/schools"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/schools"
)

var chidi = uuid.New()

func (o *ops) ClearanceOf(_ context.Context, _ uuid.UUID, student string, term uuid.UUID) (schools.ClearanceRow, error) {
	if student != "GCA/001" && student != ebube.String() {
		return schools.ClearanceRow{}, schools.ErrNotFound
	}
	return schools.ClearanceRow{Clearance: schools.Clearance{StudentID: ebube, TermID: term, Status: schools.PartlyPaid, Reason: schools.ReasonBalance, Invoiced: true, TotalKobo: 4_000_000, PaidKobo: 1_000_000, BalanceKobo: 3_000_000},
		AdmissionNumber: "GCA/001", Name: "Ebube Eze", StudentStatus: "active", ClassID: jss1.ID, Class: "JSS 1 A"}, nil
}

func (o *ops) ListClearance(_ context.Context, _ uuid.UUID, term uuid.UUID, class *uuid.UUID) (schools.ClearanceList, error) {
	o.class = class
	rows := []schools.ClearanceRow{
		{Clearance: schools.Clearance{StudentID: ebube, TermID: term, Status: schools.PartlyPaid, Reason: schools.ReasonBalance, Invoiced: true, TotalKobo: 4_000_000, PaidKobo: 1_000_000, BalanceKobo: 3_000_000}, AdmissionNumber: "GCA/001", Name: "Ebube Eze", StudentStatus: "active", ClassID: jss1.ID, Class: "JSS 1 A"},
		{Clearance: schools.Clearance{StudentID: chidi, TermID: term, Status: schools.Cleared, Reason: schools.ReasonPaid, Invoiced: true, TotalKobo: 4_000_000, PaidKobo: 4_000_000}, AdmissionNumber: "GCA/002", Name: "Chidi Eze", StudentStatus: "active", ClassID: jss1.ID, Class: "JSS 1 A"},
	}
	return schools.ClearanceList{TermID: term, Term: "First term 2026/2027", Rows: rows, Totals: schools.ClearanceTotals{Students: 2, Cleared: 1, PartlyPaid: 1}}, nil
}

// clearance: one student with the balances, or a class; a student or a
// class is required.
func TestClearance(t *testing.T) {
	o := &ops{}
	c := conn.New(o)
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: uuid.New(), Run: uuid.New(), Step: "s"})
	r, err := c.Actions["clearance"].Execute(ctx, connector.Request{Input: map[string]any{"student": "GCA/001"}})
	if err != nil {
		t.Fatal(err)
	}
	out := r.Output.(map[string]any)
	if out["status"] != "partly_paid" || out["reason"] != "balance" || out["balance_kobo"] != int64(3_000_000) || out["name"] != "Ebube Eze" || out["term"] != "First term 2026/2027" {
		t.Fatalf("one %v", out)
	}
	if _, err := c.Actions["clearance"].Execute(ctx, connector.Request{Input: map[string]any{"student": "GCA/009"}}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("unknown student: %v", err)
	}
	if _, err := c.Actions["clearance"].Execute(ctx, connector.Request{Input: map[string]any{}}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("neither: %v", err)
	}
	r, err = c.Actions["clearance"].Execute(ctx, connector.Request{Input: map[string]any{"class": "JSS 1 A"}})
	if err != nil {
		t.Fatal(err)
	}
	out = r.Output.(map[string]any)
	if out["count"] != 2 || out["cleared"] != 1 || out["partly_paid"] != 1 || o.class == nil || *o.class != jss1.ID {
		t.Fatalf("class %v", out)
	}
	if row := out["students"].([]any)[1].(map[string]any); row["status"] != "cleared" || row["balance_kobo"] != int64(0) {
		t.Fatalf("row %v", row)
	}
}

// clearance_list: a summary per class with the names of those not
// cleared and a message ready to send, and no amount anywhere.
func TestClearanceList(t *testing.T) {
	o := &ops{}
	c := conn.New(o)
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: uuid.New(), Run: uuid.New(), Step: "s"})
	r, err := c.Actions["clearance_list"].Execute(ctx, connector.Request{Input: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	out := r.Output.(map[string]any)
	if out["count"] != 1 || out["students"] != 2 || out["cleared"] != 1 || o.class != nil {
		t.Fatalf("list %v", out)
	}
	cl := out["classes"].([]any)[0].(map[string]any)
	if cl["class"] != "JSS 1 A" || cl["cleared"] != 1 || cl["partly_paid"] != 1 || cl["not_cleared"].([]any)[0] != "Ebube Eze (GCA/001): partly paid" {
		t.Fatalf("class %v", cl)
	}
	text := cl["text"].(string)
	if !strings.HasPrefix(text, "Clearance list, JSS 1 A, First term 2026/2027: 1 of 2 cleared. See the bursar:\n- Ebube Eze (GCA/001): partly paid") || strings.Contains(text, "₦") || strings.Contains(text, "000") {
		t.Fatalf("text %q", text)
	}
	for k := range cl {
		if strings.Contains(k, "kobo") || strings.Contains(k, "balance") {
			t.Errorf("an amount in the teachers' list: %s", k)
		}
	}
	if _, err := c.Actions["clearance_list"].Execute(ctx, connector.Request{Input: map[string]any{"class": "JSS 9"}}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("unknown class: %v", err)
	}
}

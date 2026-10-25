package schools_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	conn "github.com/israel-duff/taskiem/connectors/schools"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/schools"
)

var (
	ebube      = uuid.New()
	lastRemind schools.RemindIn
	told       []string
)

func (o *ops) ListDebtors(_ context.Context, _ uuid.UUID, f schools.DebtorFilter) (schools.Debtors, error) {
	o.class = f.ClassID
	next := "2026-11-15"
	return schools.Debtors{Students: 1, BalanceKobo: 4_000_000, Totals: map[string]schools.BucketTotal{schools.Bucket7to14: {Students: 1, BalanceKobo: 4_000_000}},
		Debtors: []schools.Debtor{{StudentID: ebube, AdmissionNumber: "GCA/001", Name: "Ebube Eze", Class: "JSS 1 A", ClassID: jss1.ID, BalanceKobo: 4_000_000, DueOn: "2026-10-14", DaysPastDue: 10, Bucket: schools.Bucket7to14,
			LastReminder: &schools.ReminderBrief{Rung: 1, At: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)},
			Promise:      &schools.Promise{Status: schools.PromiseMissed, PromisedOn: "2026-10-20"},
			Plan:         &schools.PlanStanding{State: schools.PlanGood, NextDueOn: next}}}}, nil
}

func (o *ops) FindStudents(_ context.Context, _ uuid.UUID, refs []string) ([]uuid.UUID, error) {
	out := []uuid.UUID{}
	for _, r := range refs {
		if r != "GCA/001" && r != ebube.String() {
			return nil, errors.Join(schools.ErrNotFound)
		}
		out = append(out, ebube)
	}
	return out, nil
}

func (o *ops) SendRemindersOnce(_ context.Context, _ uuid.UUID, in schools.RemindIn, _ string) (schools.Reminded, error) {
	lastRemind = in
	return schools.Reminded{Key: in.Key, Sent: 1, Skipped: 1, ByRung: map[string]int{"1": 0, "2": 1, "3": 0}, Results: []schools.RemindOutcome{
		{StudentID: ebube, AdmissionNumber: "GCA/001", Rung: 2, Status: schools.RemindSent, BalanceKobo: 4_000_000, Channels: map[string]int{"sms": 1}},
		{StudentID: uuid.New(), AdmissionNumber: "GCA/002", Rung: 2, Status: schools.RemindSkipped, Reason: "a promise to pay by 25 Oct 2026"}}}, nil
}

func (o *ops) ListPlanDecisions(_ context.Context, _ uuid.UUID, f schools.PlanDecisionFilter) ([]schools.PlanRequest, error) {
	if !f.Untold {
		return []schools.PlanRequest{}, nil
	}
	at := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	return []schools.PlanRequest{{ID: uuid.MustParse("0192e2e0-0000-7000-8000-0000000000aa"), StudentID: ebube, AdmissionNumber: "GCA/001", Name: "Ebube Eze", Class: "JSS 1 A", Status: "approved", DecidedAt: &at,
		DecisionNote: "Half now", Instalments: []schools.Instalment{{AmountKobo: 1, DueOn: "2026-11-01"}}, DecidedInstalments: []schools.Instalment{{AmountKobo: 2_000_000, DueOn: "2026-11-01"}, {AmountKobo: 2_000_000, DueOn: "2026-12-01"}}}}, nil
}

func (o *ops) TellPlan(_ context.Context, _ uuid.UUID, plan string) (schools.PlanTold, error) {
	told = append(told, plan)
	switch plan {
	case "0192e2e0-0000-7000-8000-0000000000aa":
		return schools.PlanTold{Plan: schools.PlanRequest{ID: uuid.MustParse(plan), StudentID: ebube, AdmissionNumber: "GCA/001", Status: "approved"}, Sent: schools.FamilySent{Channels: map[string]int{"whatsapp": 1}}}, nil
	case "0192e2e0-0000-7000-8000-0000000000bb":
		return schools.PlanTold{Plan: schools.PlanRequest{ID: uuid.MustParse(plan), StudentID: ebube, AdmissionNumber: "GCA/001", Status: "declined"}, Told: true}, nil
	}
	return schools.PlanTold{}, schools.ErrNotFound
}

// list_debtors: no names; the aging, the last reminder, the promise and
// the plan's state in words a workflow can branch on.
func TestListDebtors(t *testing.T) {
	o := &ops{}
	c := conn.New(o)
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: uuid.New(), Run: uuid.New(), Step: "s"})
	r, err := c.Actions["list_debtors"].Execute(ctx, connector.Request{Input: map[string]any{"class": "JSS 1 A", "bucket": "7_14"}})
	if err != nil {
		t.Fatal(err)
	}
	out := r.Output.(map[string]any)
	d := out["debtors"].([]any)[0].(map[string]any)
	if _, named := d["name"]; named || o.class == nil || out["count"] != 1 || d["bucket"] != "7_14" || d["last_rung"] != 1 || d["promise"] != "missed" || d["plan"] != "good" || d["plan_next_due_on"] != "2026-11-15" || d["days_past_due"] != 10 {
		t.Fatalf("debtors %v", out)
	}
	if tot := out["totals"].(map[string]any)["7_14"].(map[string]any); tot["balance_kobo"] != int64(4_000_000) {
		t.Fatalf("totals %v", out["totals"])
	}
	if _, err := c.Actions["list_debtors"].Execute(ctx, connector.Request{Input: map[string]any{"limit": 0}}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("limit 0: %v", err)
	}
}

// send_reminders: under the step's key; students by admission number or
// the debtors past due of a class; the rung as given or by days; at most
// fifty.
func TestSendReminders(t *testing.T) {
	c := conn.New(&ops{})
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: uuid.New(), Run: uuid.New(), Step: "s"})
	if _, err := c.Actions["send_reminders"].Execute(ctx, connector.Request{Input: map[string]any{"rung": "1"}}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("no key: %v", err)
	}
	r, err := c.Actions["send_reminders"].Execute(ctx, connector.Request{Input: map[string]any{"students": []any{"GCA/001"}, "rung": "2"}, IdempotencyKey: "tsk_step"})
	if err != nil {
		t.Fatal(err)
	}
	out := r.Output.(map[string]any)
	if out["sent"] != 1 || out["skipped"] != 1 || lastRemind.Key != "step:tsk_step" || lastRemind.Rung != 2 || len(lastRemind.Students) != 1 || lastRemind.Students[0] != ebube {
		t.Fatalf("sent %v %+v", out, lastRemind)
	}
	if res := out["results"].([]any)[1].(map[string]any); res["status"] != "skipped" || res["reason"] != "a promise to pay by 25 Oct 2026" {
		t.Fatalf("results %v", out["results"])
	}
	if _, err := c.Actions["send_reminders"].Execute(ctx, connector.Request{Input: map[string]any{"class": "JSS 1 A", "limit": 20}, IdempotencyKey: "tsk_step2"}); err != nil {
		t.Fatal(err)
	}
	if lastRemind.ClassID == nil || *lastRemind.ClassID != jss1.ID || lastRemind.Rung != 0 || lastRemind.Limit != 20 || len(lastRemind.Students) != 0 {
		t.Fatalf("auto %+v", lastRemind)
	}
	many := make([]any, 51)
	for i := range many {
		many[i] = "GCA/001"
	}
	if _, err := c.Actions["send_reminders"].Execute(ctx, connector.Request{Input: map[string]any{"students": many}, IdempotencyKey: "tsk_step3"}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("51 students: %v", err)
	}
	if _, err := c.Actions["send_reminders"].Execute(ctx, connector.Request{Input: map[string]any{"students": []any{"GCA/009"}}, IdempotencyKey: "tsk_step4"}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("an unknown student: %v", err)
	}
	if _, err := c.Actions["send_reminders"].Execute(ctx, connector.Request{Input: map[string]any{"rung": "9"}, IdempotencyKey: "tsk_step5"}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("rung 9: %v", err)
	}
}

// plan_decisions and tell_plan_decision: the untold decisions with the
// instalments as decided, told once each.
func TestPlanDecisions(t *testing.T) {
	c := conn.New(&ops{})
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: uuid.New(), Run: uuid.New(), Step: "s"})
	r, err := c.Actions["plan_decisions"].Execute(ctx, connector.Request{Input: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	list := r.Output.(map[string]any)["decisions"].([]any)
	d := list[0].(map[string]any)
	if _, named := d["name"]; named || len(list) != 1 || d["status"] != "approved" || d["told"] != false || len(d["instalments"].([]any)) != 2 || d["note"] != "Half now" || d["decided_at"] != "2026-10-23T09:00:00Z" {
		t.Fatalf("decisions %v", list)
	}
	if r, err := c.Actions["plan_decisions"].Execute(ctx, connector.Request{Input: map[string]any{"untold": false}}); err != nil || r.Output.(map[string]any)["count"] != 0 {
		t.Fatalf("told too: %v %v", r.Output, err)
	}
	if _, err := c.Actions["tell_plan_decision"].Execute(ctx, connector.Request{Input: map[string]any{"plan": d["id"]}}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("no key: %v", err)
	}
	r, err = c.Actions["tell_plan_decision"].Execute(ctx, connector.Request{Input: map[string]any{"plan": d["id"]}, IdempotencyKey: "tsk_step"})
	if err != nil {
		t.Fatal(err)
	}
	if out := r.Output.(map[string]any); out["told_before"] != false || out["channels"].(map[string]any)["whatsapp"] != 1 || out["status"] != "approved" || told[len(told)-1] != d["id"] {
		t.Fatalf("told %v", out)
	}
	r, err = c.Actions["tell_plan_decision"].Execute(ctx, connector.Request{Input: map[string]any{"plan": "0192e2e0-0000-7000-8000-0000000000bb"}, IdempotencyKey: "tsk_step2"})
	if err != nil {
		t.Fatal(err)
	}
	if out := r.Output.(map[string]any); out["told_before"] != true || out["nobody"] != false {
		t.Fatalf("told before %v", out)
	}
	if _, err := c.Actions["tell_plan_decision"].Execute(ctx, connector.Request{Input: map[string]any{"plan": "nope"}, IdempotencyKey: "tsk_step3"}); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("unknown plan: %v", err)
	}
}

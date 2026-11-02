package storefront_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/templates"
)

// Hire (docs/spec/fronts.md §6, F5): a hire submitted with its claim is
// recorded with its item and dates, keeps its claim, and starts the hire
// workflow; Returned ends the claim and adds a fee for each late day, and
// asked again gives the same answer.
func TestHireRecordedAndReturned(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	tpl, ok := templates.Default().Get("storefront-hire")
	if !ok {
		t.Fatal("no storefront-hire template")
	}
	def, err := tpl.Instantiate(map[string]any{}, "Hire")
	if err != nil {
		t.Fatal(err)
	}
	wf := w.env.Publish(t, string(def))
	front, block, action := uuid.New(), uuid.New(), uuid.New()
	canopy, _ := json.Marshal(storefront.Item{ID: "1", Name: "Canopy", PriceKobo: kobo(500000), Available: true, Capacity: 3})
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO storefront_fronts (id, tenant_id, storefront_id, slug, kind, name, enabled) VALUES ($1, $2, $3, 'tools', 'hire', 'Tools', true)`, []any{front, w.env.Tenant, w.sf.ID}},
		{`INSERT INTO storefront_blocks (id, tenant_id, front_id, kind, config) VALUES ($1, $2, $3, 'list', '{"table_name": "hire_items"}')`, []any{block, w.env.Tenant, front}},
		{`INSERT INTO storefront_actions (id, storefront_id, kind, tenant_id, enabled, label, payment, front_id, slug, builtin, options)
			VALUES ($1, $2, 'hire', $3, true, 'Hire now', 'after_accept', $4, 'hire', false, '{"deposit_percent": 50, "late_fee_kobo": 200000}')`, []any{action, w.sf.ID, w.env.Tenant, front}},
		{`INSERT INTO storefront_catalogue (storefront_id, table_name, row_id, tenant_id, position, item) VALUES ($1, 'hire_items', '1', $2, 0, $3)`, []any{w.sf.ID, w.env.Tenant, canopy}},
		{`INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, storefront_action, storefront_action_id) VALUES ($1, $2, $3, 1, 'prod', 'storefront', 'hire', $4)`,
			[]any{uuid.New(), w.env.Tenant, wf, action}},
	} {
		if _, err := w.env.DB.Admin.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}

	today := time.Now().In(storefront.Lagos)
	from := time.Date(today.Year(), today.Month(), today.Day()+3, 0, 0, 0, 0, storefront.Lagos)
	h := storefront.Hired{ItemID: "1", Item: "Canopy", Qty: 2, From: from, To: from.AddDate(0, 0, 1), Until: from.AddDate(0, 0, 2), Days: 2}
	ref := storefront.NewReference("ada-kitchen")
	key, hash := storefront.NewStatusKey()
	sub := storefront.Submission{Kind: "hire", Customer: storefront.Customer{Name: "Bola", Phone: "+2348031234567"}, Hire: &h,
		Basket: []storefront.Line{{ID: "1", Name: "Canopy", Quantity: 4, PriceKobo: 500000, LineKobo: 2000000}}, TotalKobo: 2000000, DepositKobo: 1000000,
		Fulfilment: "pickup", Payment: "after_accept", Channel: "direct", Submitted: time.Now(), StatusKey: key}
	ct, err := storefront.SealSubmission(w.key, w.sf.ID, ref, sub)
	if err != nil {
		t.Fatal(err)
	}
	idem := make([]byte, 32)
	copy(idem, ref)
	var got *string
	if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT taskiem_public_claim($1, $2, $3, $4, $5, '1', 2, $6, $7)`, action, ref, idem, hash, ct, h.From, h.Until).
		Scan(&got); err != nil || got == nil || *got != ref {
		t.Fatalf("claim: %v %v", got, err)
	}
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	o := w.order(ref)
	if o.Kind != "hire" || o.ServiceID != "1" || o.ServiceName != "Canopy" || o.StartsAt == nil || !o.StartsAt.Equal(h.From) || !o.EndsAt.Equal(h.Until) ||
		o.DepositKobo == nil || *o.DepositKobo != 1000000 || o.RunID == nil {
		t.Fatalf("recorded: %+v", o)
	}
	var status, summary string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM runs WHERE id = $1`, *o.RunID).Scan(&status); err != nil || (status != "running" && status != "waiting") {
		t.Fatalf("the hire's run: %s %v", status, err)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT payload->'trigger'->'body'->>'summary' FROM run_events WHERE run_id = $1 AND type = 'RunStarted'`, *o.RunID).Scan(&summary); err != nil ||
		summary != "2 × Canopy, "+storefront.Day(h.From)+" to "+storefront.Day(h.To) {
		t.Fatalf("the hire's summary: %q %v", summary, err)
	}
	var expires *time.Time
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT expires_at FROM storefront_claims WHERE reference = $1`, ref).Scan(&expires); err != nil || expires != nil || o.ExpiresAt != nil {
		t.Fatalf("the claim lives as long as the hire: %v %v %v", expires, o.ExpiresAt, err)
	}

	// Returned is for hires.
	plain := w.submit("order", "after_accept", 1000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := w.orders.MarkReturned(ctx, w.env.Tenant, plain, "user:ada"); !errors.Is(err, storefront.ErrDecision) {
		t.Fatalf("an order is not a hire: %v", err)
	}

	// Back a day and a bit late: two days' fee on top of what it costs.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET status = 'accepted', ends_at = now() - interval '26 hours' WHERE reference = $1`, ref); err != nil {
		t.Fatal(err)
	}
	r, err := w.orders.MarkReturned(ctx, w.env.Tenant, ref, "user:ada")
	if err != nil || r.LateDays != 2 || r.LateFeeKobo != 400000 || r.Order.ReturnedAt == nil || r.Order.DueKobo() != 2400000 {
		t.Fatalf("returned: %+v %v", r, err)
	}
	var claims int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM storefront_claims WHERE reference = $1`, ref).Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("claims after return: %d %v", claims, err)
	}
	// Asked again (a retried step): the same answer, no second fee.
	again, err := w.orders.MarkReturned(ctx, w.env.Tenant, ref, "user:ada")
	if err != nil || again.LateDays != 2 || again.LateFeeKobo != 400000 || again.Order.DueKobo() != 2400000 {
		t.Fatalf("again: %+v %v", again, err)
	}
}

// A space booked by the hour is recorded with its times and starts the
// space workflow, which waits on the owner's answer.
func TestSpaceRecorded(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	tpl, ok := templates.Default().Get("storefront-space")
	if !ok {
		t.Fatal("no storefront-space template")
	}
	def, err := tpl.Instantiate(map[string]any{}, "Halls")
	if err != nil {
		t.Fatal(err)
	}
	wf := w.env.Publish(t, string(def))
	front, block, action := uuid.New(), uuid.New(), uuid.New()
	hall, _ := json.Marshal(storefront.Item{ID: "1", Name: "Main hall", PriceKobo: kobo(2000000), Available: true})
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO storefront_fronts (id, tenant_id, storefront_id, slug, kind, name, enabled) VALUES ($1, $2, $3, 'halls', 'spaces', 'Halls', true)`, []any{front, w.env.Tenant, w.sf.ID}},
		{`INSERT INTO storefront_blocks (id, tenant_id, front_id, kind, config) VALUES ($1, $2, $3, 'list', '{"table_name": "spaces", "calendar": {"grain": "hours"}}')`, []any{block, w.env.Tenant, front}},
		{`INSERT INTO storefront_actions (id, storefront_id, kind, tenant_id, enabled, label, payment, front_id, slug, builtin, options)
			VALUES ($1, $2, 'space', $3, true, 'Book the space', 'after_accept', $4, 'space', false, '{"deposit_percent": 50}')`, []any{action, w.sf.ID, w.env.Tenant, front}},
		{`INSERT INTO storefront_catalogue (storefront_id, table_name, row_id, tenant_id, position, item) VALUES ($1, 'spaces', '1', $2, 0, $3)`, []any{w.sf.ID, w.env.Tenant, hall}},
		{`INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, storefront_action, storefront_action_id) VALUES ($1, $2, $3, 1, 'prod', 'storefront', 'space', $4)`,
			[]any{uuid.New(), w.env.Tenant, wf, action}},
	} {
		if _, err := w.env.DB.Admin.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	today := time.Now().In(storefront.Lagos)
	start := time.Date(today.Year(), today.Month(), today.Day()+3, 10, 0, 0, 0, storefront.Lagos)
	h := storefront.Hired{ItemID: "1", Item: "Main hall", Qty: 1, From: start, To: start, Until: start.Add(4 * time.Hour), Hours: 4}
	ref := storefront.NewReference("ada-kitchen")
	key, hash := storefront.NewStatusKey()
	sub := storefront.Submission{Kind: "space", Customer: storefront.Customer{Name: "Bola", Phone: "+2348031234567"}, Hire: &h,
		Basket: []storefront.Line{{ID: "1", Name: "Main hall", Quantity: 4, PriceKobo: 2000000, LineKobo: 8000000, Unit: "hour"}}, TotalKobo: 8000000, DepositKobo: 4000000,
		Payment: "after_accept", Channel: "direct", Submitted: time.Now(), StatusKey: key}
	ct, err := storefront.SealSubmission(w.key, w.sf.ID, ref, sub)
	if err != nil {
		t.Fatal(err)
	}
	idem := make([]byte, 32)
	copy(idem, ref)
	var got *string
	if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT taskiem_public_claim($1, $2, $3, $4, $5, '1', 1, $6, $7)`, action, ref, idem, hash, ct, h.From, h.Until).
		Scan(&got); err != nil || got == nil || *got != ref {
		t.Fatalf("claim: %v %v", got, err)
	}
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	o := w.order(ref)
	if o.Kind != "space" || o.ServiceName != "Main hall" || !o.StartsAt.Equal(start) || !o.EndsAt.Equal(h.Until) || o.RunID == nil || o.Basket[0].Unit != "hour" {
		t.Fatalf("recorded: %+v", o)
	}
	var status string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM runs WHERE id = $1`, *o.RunID).Scan(&status); err != nil || (status != "running" && status != "waiting") {
		var why string
		_ = w.env.DB.Admin.QueryRow(ctx, `SELECT string_agg(type || ' ' || COALESCE(step_id, '') || ' ' || COALESCE(payload::text, ''), E'\n' ORDER BY seq) FROM run_events WHERE run_id = $1`, *o.RunID).Scan(&why)
		t.Fatalf("the run: %s %v\n%s", status, err, why)
	}
	// Declined, the time is free again.
	if _, err := w.orders.Decide(ctx, w.env.Tenant, ref, "decline", "", "user:ada"); err != nil {
		t.Fatal(err)
	}
	var claims int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM storefront_claims WHERE reference = $1`, ref).Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("claims after declining: %d %v", claims, err)
	}
}

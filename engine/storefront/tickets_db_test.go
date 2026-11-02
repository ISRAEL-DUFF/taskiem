package storefront_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/templates"
)

// countFront adds a front of a counted kind (Tickets or Classes) to the
// world's storefront: its list of one item, its action with its workflow
// deployed, and returns the action.
func (w *world) countFront(kind, frontKind, table, template, opts string, item storefront.Item) uuid.UUID {
	w.t.Helper()
	ctx := context.Background()
	tpl, ok := templates.Default().Get(template)
	if !ok {
		w.t.Fatalf("no %s template", template)
	}
	def, err := tpl.Instantiate(map[string]any{}, frontKind)
	if err != nil {
		w.t.Fatal(err)
	}
	wf := w.env.Publish(w.t, string(def))
	front, block, action := uuid.New(), uuid.New(), uuid.New()
	raw, _ := json.Marshal(item)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO storefront_fronts (id, tenant_id, storefront_id, slug, kind, name, enabled) VALUES ($1, $2, $3, $4, $4, $4, true)`, []any{front, w.env.Tenant, w.sf.ID, frontKind}},
		{`INSERT INTO storefront_blocks (id, tenant_id, front_id, kind, config) VALUES ($1, $2, $3, 'list', jsonb_build_object('table_name', $4::text))`, []any{block, w.env.Tenant, front, table}},
		{`INSERT INTO storefront_actions (id, storefront_id, kind, tenant_id, enabled, label, payment, front_id, slug, builtin, options)
			VALUES ($1, $2, $3, $4, true, 'Take', 'at_order', $5, $3, false, $6)`, []any{action, w.sf.ID, kind, w.env.Tenant, front, opts}},
		{`INSERT INTO storefront_catalogue (storefront_id, table_name, row_id, tenant_id, position, item) VALUES ($1, $2, $3, $4, 0, $5)`, []any{w.sf.ID, table, item.ID, w.env.Tenant, raw}},
		{`INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, storefront_action, storefront_action_id) VALUES ($1, $2, $3, 1, 'prod', 'storefront', $4, $5)`,
			[]any{uuid.New(), w.env.Tenant, wf, kind, action}},
	} {
		if _, err := w.env.DB.Admin.Exec(ctx, q.sql, q.args...); err != nil {
			w.t.Fatal(err)
		}
	}
	return action
}

// take submits qty of an item to a counted action as the public host does:
// through taskiem_public_claim, or with no claim for a waiting list.
func (w *world) take(action uuid.UUID, kind string, item storefront.Item, qty int, mod func(*storefront.Submission)) (string, error) {
	w.t.Helper()
	ctx := context.Background()
	ref := storefront.NewReference("ada-kitchen")
	key, hash := storefront.NewStatusKey()
	w.keys[ref] = key
	h := storefront.Hired{ItemID: item.ID, Item: item.Name, Qty: qty}
	if item.Starts != nil {
		h.From = *item.Starts
	}
	total := *item.PriceKobo * int64(qty)
	sub := storefront.Submission{Kind: kind, Customer: storefront.Customer{Name: "Bola", Phone: "+2348031234567"}, Hire: &h,
		Basket: []storefront.Line{{ID: item.ID, Name: item.Name, Quantity: qty, PriceKobo: *item.PriceKobo, LineKobo: total}}, TotalKobo: total,
		Payment: "at_order", Channel: "direct", Submitted: time.Now(), StatusKey: key}
	if mod != nil {
		mod(&sub)
	}
	ct, err := storefront.SealSubmission(w.key, w.sf.ID, ref, sub)
	if err != nil {
		w.t.Fatal(err)
	}
	idem := make([]byte, 32)
	copy(idem, ref)
	pool := w.env.DB.PublicPool(w.t, 1)
	if sub.Waitlist {
		_, err = pool.Exec(ctx, `SELECT taskiem_public_submit_action($1, $2, $3, $4, $5)`, action, ref, idem, hash, ct)
	} else {
		_, err = pool.Exec(ctx, `SELECT taskiem_public_claim($1, $2, $3, $4, $5, $6, $7, $8, $9)`, action, ref, idem, hash, ct, item.ID, qty, h.From, h.Until)
	}
	if err != nil {
		return "", err
	}
	return ref, w.orders.Drain(ctx)
}

// Tickets (docs/spec/fronts.md §6, F6): sold against the event's count,
// paid at order; once paid, a code per seat is issued (asked again, the
// same codes), each checks in once, and a refunded order's codes do not.
func TestTicketsSoldIssuedAndCheckedIn(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	starts := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)
	concert := storefront.Item{ID: "1", Name: "Concert", PriceKobo: kobo(500000), Available: true, Capacity: 3, Starts: &starts}
	action := w.countFront("tickets", "tickets", "events", "storefront-tickets", `{"max_per_order": 10}`, concert)

	ref, err := w.take(action, "tickets", concert, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	o := w.order(ref)
	if o.Kind != "tickets" || o.Status != "awaiting_payment" || o.ServiceID != "1" || o.StartsAt == nil || !o.StartsAt.Equal(starts) || o.EndsAt != nil {
		t.Fatalf("recorded: %+v", o)
	}
	// Two of three taken: two more are refused, one is not.
	if _, err := w.take(action, "tickets", concert, 2, nil); err == nil {
		t.Fatal("more tickets than the event has")
	}
	if _, err := w.take(action, "tickets", concert, 1, nil); err != nil {
		t.Fatalf("the last ticket: %v", err)
	}
	if _, _, err := w.orders.IssueTickets(ctx, w.env.Tenant, ref); !errors.Is(err, storefront.ErrDecision) {
		t.Fatalf("tickets before payment: %v", err)
	}

	// Paid: the run starts, and the codes are issued once.
	w.pay.pay(storefront.PaymentKey(o.ID))
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	if o = w.order(ref); o.PaidAt == nil || o.RunID == nil {
		t.Fatalf("paid: %+v", o)
	}
	var status string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM runs WHERE id = $1`, *o.RunID).Scan(&status); err != nil || (status != "running" && status != "waiting") {
		t.Fatalf("the run: %s %v", status, err)
	}
	list, _, err := w.orders.IssueTickets(ctx, w.env.Tenant, ref)
	if err != nil || len(list) != 2 || list[0].Code != ref+"-1" || list[1].Code != ref+"-2" || list[1].Of != 2 {
		t.Fatalf("issued: %+v %v", list, err)
	}
	again, _, err := w.orders.IssueTickets(ctx, w.env.Tenant, ref)
	if err != nil || len(again) != 2 || again[0].Code != list[0].Code {
		t.Fatalf("again: %+v %v", again, err)
	}

	// At the door: in once, typed any way.
	tk, already, err := w.orders.CheckIn(ctx, w.env.Tenant, " "+strings.ToLower(ref)+"2 ", "user:ada")
	if err != nil || already || tk.Code != ref+"-2" || tk.Seat != 2 || tk.Event != "Concert" || tk.CheckedInAt == nil {
		t.Fatalf("checked in: %+v %v %v", tk, already, err)
	}
	if tk, already, err = w.orders.CheckIn(ctx, w.env.Tenant, ref+"-2", "user:ada"); err != nil || !already {
		t.Fatalf("twice: %+v %v %v", tk, already, err)
	}
	if _, _, err := w.orders.CheckIn(ctx, w.env.Tenant, ref+"-9", "user:ada"); !errors.Is(err, storefront.ErrNoTicket) {
		t.Fatalf("no such seat: %v", err)
	}
	// Counted for the owner: three taken, one in.
	var left []storefront.EventCount
	w.in(func(tx pgx.Tx) error {
		var err error
		left, err = storefront.TicketsLeft(ctx, tx, w.env.Tenant, "tickets")
		return err
	})
	if len(left) != 1 || left[0].Tickets != 3 || left[0].Taken != 3 || left[0].CheckedIn != 1 {
		t.Fatalf("left: %+v", left)
	}
	// Refunded, its codes go in no more and its tickets are free again.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET status = 'refund_due' WHERE reference = $1`, ref); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.orders.CheckIn(ctx, w.env.Tenant, ref+"-1", "user:ada"); !errors.Is(err, storefront.ErrDecision) {
		t.Fatalf("a refunded ticket: %v", err)
	}
}

// Classes (§6, F6): a fee in instalments takes the first at order and
// tells the run the rest; a full class takes a name for its waiting list,
// holding no place, and offering it a place is refused until one frees.
func TestClassesInstalmentsAndWaitingList(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	starts := time.Now().Add(10 * 24 * time.Hour).UTC().Truncate(time.Second)
	course := storefront.Item{ID: "7", Name: "Baking class", PriceKobo: kobo(3000000), Available: true, Capacity: 1, Starts: &starts}
	action := w.countFront("class", "classes", "classes", "storefront-classes", `{"max_per_order": 5, "instalments": 3, "instalment_days": 14, "waiting_list": true}`, course)

	parts := storefront.InstalmentAmounts(3000000, 3)
	if parts[0] != 1000000 || parts[1] != 1000000 || parts[2] != 1000000 {
		t.Fatalf("parts: %v", parts)
	}
	if p := storefront.InstalmentAmounts(1000050, 3); p[0]+p[1]+p[2] != 1000050 || p[0] != 333400 {
		t.Fatalf("uneven parts: %v", p)
	}
	ref, err := w.take(action, "class", course, 1, func(s *storefront.Submission) { s.Instalments, s.DepositKobo = 3, parts[0] })
	if err != nil {
		t.Fatal(err)
	}
	o := w.order(ref)
	if o.Instalments != 3 || o.DepositKobo == nil || *o.DepositKobo != 1000000 || o.Status != "awaiting_payment" {
		t.Fatalf("enrolled: %+v", o)
	}
	// The class is full now: a name for the waiting list holds no place.
	if _, err := w.take(action, "class", course, 1, nil); err == nil {
		t.Fatal("a place beyond the class's")
	}
	wait, err := w.take(action, "class", course, 1, func(s *storefront.Submission) { s.Waitlist = true })
	if err != nil {
		t.Fatal(err)
	}
	wo := w.order(wait)
	if !wo.Waitlist || wo.Payment != "none" || wo.Status != "received" || wo.RunID == nil {
		t.Fatalf("waiting: %+v", wo)
	}
	if _, err := w.orders.OfferPlace(ctx, w.env.Tenant, wait, "user:ada"); !errors.Is(err, storefront.ErrDecision) {
		t.Fatalf("offered while full: %v", err)
	}
	if _, err := w.orders.OfferPlace(ctx, w.env.Tenant, ref, "user:ada"); !errors.Is(err, storefront.ErrDecision) {
		t.Fatalf("offered to an enrolled learner: %v", err)
	}
	// The first learner cancels: the place is offered and taken.
	for _, q := range []string{`UPDATE storefront_orders SET status = 'cancelled' WHERE reference = $1`, `DELETE FROM storefront_claims WHERE reference = $1`} {
		if _, err := w.env.DB.Admin.Exec(ctx, q, ref); err != nil {
			t.Fatal(err)
		}
	}
	got, err := w.orders.OfferPlace(ctx, w.env.Tenant, wait, "user:ada")
	if err != nil || got.Waitlist || got.Status != "accepted" || got.Payment != "after_accept" {
		t.Fatalf("offered: %+v %v", got, err)
	}
	var claims int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM storefront_claims WHERE reference = $1 AND expires_at IS NULL`, wait).Scan(&claims); err != nil || claims != 1 {
		t.Fatalf("the place: %d %v", claims, err)
	}
	if link, err := w.orders.PaymentLink(ctx, w.env.Tenant, wait, 0); err != nil || link["url"] == "" {
		t.Fatalf("its payment link: %v %v", link, err)
	}
}

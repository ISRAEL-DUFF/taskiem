package api_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Stage 3's review of Orders on You ("As built: stage 3, Orders and
// account review fixes" in docs/spec/taskiem-for-everyone.md).

// rowOf is the row of reference in GET /v1/me/orders.
func rowOf(t *testing.T, c *client, ref string) map[string]any {
	t.Helper()
	for _, o := range c.must(200, "GET", "/v1/me/orders", nil)["orders"].([]any) {
		if m := o.(map[string]any); m["reference"] == ref {
			return m
		}
	}
	t.Fatalf("%s is not on Orders", ref)
	return nil
}

// One rule for Pay (finding 1): an order that is cancelled, declined,
// expired or due a refund shows no Pay, and its pay link, from Orders or
// from the chat, is refused with "This order is closed."
func TestOrdersPayRuleClosedOrders(t *testing.T) {
	ctx := context.Background()
	w := ordersWorld(t)
	const number = "+2348031140101"
	tolu, _ := w.personIn(t, number)
	toluID := personID(t, tolu)
	chat := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	o := w.placeByPhone(t, w.biz, number, storefront.ChatPayTransfer, &toluID)
	base := "/v1/me/orders/" + w.biz.String() + "/" + o.Reference
	if r := rowOf(t, tolu, o.Reference); r["pay"] != true {
		t.Fatalf("due and payable: %v", r)
	}
	tolu.must(200, "GET", base+"/pay", nil)
	tolu.must(200, "GET", "/v1/me/chats/"+chat+"/orders/"+o.Reference+"/pay", nil)
	for _, st := range []string{"cancelled", "declined", "expired", "refund_due", "completed"} {
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET status = $2 WHERE id = $1`, o.ID, st); err != nil {
			t.Fatal(err)
		}
		if r := rowOf(t, tolu, o.Reference); r["pay"] != false || r["due_kobo"] != float64(60000) {
			t.Fatalf("%s: Pay on the list: %v", st, r)
		}
		if e := tolu.must(409, "GET", base+"/pay", nil); e["error"] != "This order is closed." {
			t.Fatalf("%s: the pay link from Orders: %v", st, e)
		}
		if e := tolu.must(409, "GET", "/v1/me/chats/"+chat+"/orders/"+o.Reference+"/pay", nil); e["error"] != "This order is closed." {
			t.Fatalf("%s: the pay link from the chat: %v", st, e)
		}
	}
	// Waiting for a new price: not closed, but not payable either.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET status = 'price_changed' WHERE id = $1`, o.ID); err != nil {
		t.Fatal(err)
	}
	if e := tolu.must(409, "GET", base+"/pay", nil); !strings.Contains(e["error"].(string), "new price") || rowOf(t, tolu, o.Reference)["pay"] != false {
		t.Fatalf("price_changed: %v", e)
	}
}

// Decision A: an order made with the number before it was verified on
// this account is shown short only, even after "These are mine": no
// summary, nothing due, no Pay; its detail is the row alone (no door, no
// audit); its pay link is refused; and the door itself refuses it.
func TestOrdersEarlierOnTheNumber(t *testing.T) {
	ctx := context.Background()
	w := ordersWorld(t)
	const number = "+2348031140201"
	early := w.placeByPhone(t, w.biz, number, storefront.ChatPayTransfer, nil)
	time.Sleep(10 * time.Millisecond)
	tolu, _ := w.personIn(t, number)
	toluID := personID(t, tolu)
	later := w.placeByPhone(t, w.biz, number, storefront.ChatPayTransfer, nil)
	if got := tolu.must(200, "GET", "/v1/me/orders", nil); got["waiting_count"] != float64(1) {
		t.Fatalf("waiting: %v", got)
	}
	tolu.must(200, "POST", "/v1/me/storefront-orders/confirm", nil)
	r := rowOf(t, tolu, early.Reference)
	if r["earlier"] != true || r["summary"] != "" || r["due_kobo"] != float64(0) || r["pay"] != false || r["total_kobo"] != float64(60000) ||
		r["business"] != "Mama's Kitchen" || r["state"] != "awaiting_payment" {
		t.Fatalf("the earlier order's row: %v", r)
	}
	if l := rowOf(t, tolu, later.Reference); l["earlier"] != nil || l["summary"] != "3 × Puff-puff" || l["pay"] != true {
		t.Fatalf("an order made since: %v", l)
	}
	base := "/v1/me/orders/" + w.biz.String() + "/"
	d := tolu.must(200, "GET", base+early.Reference, nil)
	if d["earlier"] != true || len(d["lines"].([]any)) != 0 || d["address"] != nil || d["fulfilment"] != nil || d["summary"] != "" {
		t.Fatalf("the earlier order's detail: %v", d)
	}
	if w.audits(t, "customer.order_detail", toluID) != 0 {
		t.Fatal("a door was opened for an earlier order")
	}
	if e := tolu.must(409, "GET", base+early.Reference+"/pay", nil); e["earlier"] != true {
		t.Fatalf("paying an earlier order: %v", e)
	}
	tolu.must(200, "GET", base+later.Reference+"/pay", nil)
	err := db.InPersonTx(ctx, w.env.DB.App, toluID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_person_order_enter($1, $2, $3, $4, 'customer.order_detail')`, toluID, w.srv.PhoneIndex.IndexKey(), w.biz, early.Reference)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "before it was on this account") {
		t.Fatalf("the door for an earlier order: %v", err)
	}
	// "Chat about this" still opens: the door's phone orders include it.
	tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "order"})
}

// waiting_count counts what "These are mine" then shows (finding 2):
// enquiries, quotes, trade orders and orders with a workflows workspace
// are not counted, as the list does not show them.
func TestOrdersWaitingCountMatchesTheList(t *testing.T) {
	ctx := context.Background()
	w := ordersWorld(t)
	const number = "+2348031140301"
	shown := w.placeByPhone(t, w.biz, number, storefront.ChatPayLater, nil)
	var hidden []storefront.Order
	for range 3 {
		hidden = append(hidden, w.placeByPhone(t, w.biz, number, storefront.ChatPayLater, nil))
	}
	for i, kind := range []string{"enquiry", "quote", "trade"} {
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET kind = $2 WHERE id = $1`, hidden[i].ID, kind); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(10 * time.Millisecond)
	tolu, _ := w.personIn(t, number)
	if got := tolu.must(200, "GET", "/v1/me/orders", nil); got["waiting_count"] != float64(1) {
		t.Fatalf("waiting_count %v, want 1", got["waiting_count"])
	}
	tolu.must(200, "POST", "/v1/me/storefront-orders/confirm", nil)
	if got := tolu.must(200, "GET", "/v1/me/orders", nil)["orders"].([]any); fmt.Sprint(refs(got)) != fmt.Sprint([]string{shown.Reference}) {
		t.Fatalf("after These are mine: %v", refs(got))
	}
}

// Hire, spaces, classes and tickets are bookings on Orders, named by what
// was booked (finding 3).
func TestOrdersBookingKinds(t *testing.T) {
	ctx := context.Background()
	w := ordersWorld(t)
	tolu, _ := w.personIn(t, "+2348031140401")
	toluID := personID(t, tolu)
	start := time.Now().Add(48 * time.Hour).Truncate(time.Hour)
	cases := []struct{ kind, service, want string }{
		{"hire", "Canopy", "Canopy, 3 days"},
		{"tickets", "Afrobeats Night", "3 tickets, Afrobeats Night"},
		{"class", "Pastry class", "3 places, Pastry class"},
		{"space", "Hall A", "Hall A"},
		{"booking", "Haircut", "Haircut"},
	}
	for _, c := range cases {
		o := w.placeByPhone(t, w.biz, "+2348031140401", storefront.ChatPayLater, &toluID)
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET kind = $2, service_name = $3, starts_at = $4, ends_at = $5 WHERE id = $1`,
			o.ID, c.kind, c.service, start, start.Add(72*time.Hour)); err != nil {
			t.Fatal(err)
		}
		r := rowOf(t, tolu, o.Reference)
		want := c.kind
		if want == "booking" {
			want = "service"
		}
		if r["kind"] != "booking" || r["booking_type"] != want || r["summary"] != c.want {
			t.Errorf("%s: %v", c.kind, r)
		}
	}
	// An order is an order, with no booking_type.
	o := w.placeByPhone(t, w.biz, "+2348031140401", storefront.ChatPayLater, &toluID)
	if r := rowOf(t, tolu, o.Reference); r["kind"] != "order" || r["booking_type"] != nil {
		t.Fatalf("an order: %v", r)
	}
}

// One order's detail and its pay link write the business's audit chain,
// so each person has a limit (finding 4).
func TestOrdersLookLimited(t *testing.T) {
	w := ordersWorld(t)
	const number = "+2348031140501"
	tolu, _ := w.personIn(t, number)
	toluID := personID(t, tolu)
	o := w.placeByPhone(t, w.biz, number, storefront.ChatPayTransfer, &toluID)
	path := "/v1/me/orders/" + w.biz.String() + "/" + o.Reference
	limited := false
	for i := range 40 {
		p := path
		if i%2 == 1 {
			p += "/pay"
		}
		if st, _ := tolu.do("GET", p, nil); st == 429 {
			limited = true
			break
		} else if st != 200 {
			t.Fatalf("look %d: %d", i, st)
		}
	}
	if !limited {
		t.Fatal("40 looks at once were not limited")
	}
	// Another person is not held by Tolu's limit.
	ade, _ := w.personIn(t, "+2348031140502")
	ade.must(404, "GET", path, nil)
}

// The business's photo on Orders never shows a page logo the moderators
// rejected (finding 5).
func TestOrdersPhotoLeavesOutARejectedLogo(t *testing.T) {
	ctx := context.Background()
	w := ordersWorld(t)
	tolu, _ := w.personIn(t, "+2348031140601")
	toluID := personID(t, tolu)
	o := w.placeByPhone(t, w.biz, "+2348031140601", storefront.ChatPayLater, &toluID)
	media := uuid.New()
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO storefront_media (id, tenant_id, hash, width, height, type, urls, paths, moderation, created_by)
		VALUES ($1, $2, 'h', 10, 10, 'image/png', '{"320": "https://x.test/logo-320.png"}', '{}', 'approved', 'test')`, media, w.biz); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_snapshots snap SET model = jsonb_set(jsonb_set(snap.model, '{logo}', '{"320": "https://x.test/logo-320.png"}'),
		'{card,logo}', to_jsonb($2::text)) FROM storefronts s WHERE s.id = snap.storefront_id AND snap.version = s.published_version AND s.tenant_id = $1`, w.biz, media.String()); err != nil {
		t.Fatal(err)
	}
	if r := rowOf(t, tolu, o.Reference); r["photo"] == nil {
		t.Fatalf("the page's logo: %v", r)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_media SET moderation = 'rejected' WHERE id = $1`, media); err != nil {
		t.Fatal(err)
	}
	if r := rowOf(t, tolu, o.Reference); r["photo"] != nil {
		t.Fatalf("a rejected logo shown: %v", r)
	}
}

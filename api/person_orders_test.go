package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Stage 3, Orders and addresses ("As built: stage 3, Orders and
// addresses" in docs/spec/taskiem-for-everyone.md).

// ordersWorld is the door's shop world with the phone index written by
// every order (app orders too, as the inbox's worker takes them in).
func ordersWorld(t *testing.T) *doorShopWorld {
	t.Helper()
	w := newDoorShopWorld(t)
	withChatLinks(w.world)
	w.srv.Orders.PhoneIndex = w.srv.PhoneIndex
	return w
}

// placeByPhone places a storefront order (a chat order the business
// confirmed) made with phone, as the business's own staff would.
func (w *doorShopWorld) placeByPhone(t *testing.T, tenant uuid.UUID, phone, pay string, person *uuid.UUID) storefront.Order {
	t.Helper()
	var o storefront.Order
	if err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		o, err = w.srv.Orders.PlaceChatTx(context.Background(), tx, tenant, storefront.ChatOrder{Customer: storefront.Customer{Name: "Tolu", Phone: phone},
			Lines: []storefront.Line{{ID: "1", Name: "Puff-puff", Quantity: 3, PriceKobo: 20000}}, Fulfilment: "pickup", Pay: pay, By: "user:x", PersonID: person})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	return o
}

// refs are the references of a list of orders, in its order.
func refs(list []any) []string {
	out := make([]string, len(list))
	for i, o := range list {
		out[i] = o.(map[string]any)["reference"].(string)
	}
	return out
}

// Orders on You (§13): app orders and the phone's confirmed storefront
// orders merged, each once, newest first, paged; an order made with the
// phone before it was on the account only counted until "These are mine";
// someone else's orders (another phone, or another person's app order on
// the same phone) never listed; each row only its own fields, with "Chat
// about this" where a chat exists; Pay only while something is due and
// the business takes it; one order's detail through the narrow door,
// audited; isolation from another person and between businesses.
func TestOrdersOnYou(t *testing.T) {
	ctx := context.Background()
	w := ordersWorld(t)
	const number = "+2348031130101"
	ada := w.tenant(t, "Ada Stores", "ada@ada.test")
	adaID := tenantUUID(t, ada)

	// Before the account: an order made with the number (waiting), and
	// someone else's order in Ada Stores.
	early := w.placeByPhone(t, adaID, number, storefront.ChatPayLater, nil)
	w.placeByPhone(t, adaID, "+2348031130199", storefront.ChatPayLater, nil)
	time.Sleep(10 * time.Millisecond)
	tolu, _ := w.personIn(t, number)
	toluID := personID(t, tolu)

	// In the app: a chat with Mama's Kitchen and an order there (taken in
	// by the inbox's worker, so both person_id and phone-indexed).
	chat := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	ids := map[string]string{}
	for _, it := range tolu.must(200, "GET", "/v1/me/chats/"+chat+"/shop", nil)["items"].([]any) {
		m := it.(map[string]any)
		ids[m["name"].(string)] = m["id"].(string)
	}
	app := tolu.must(201, "POST", "/v1/me/chats/"+chat+"/orders", map[string]any{"lines": []any{map[string]any{"item_id": ids["Jollof rice"], "quantity": 2},
		map[string]any{"item_id": ids["Chapman"], "quantity": 1}}, "fulfilment": "pickup"}, "Idempotency-Key", "a1")["order"].(map[string]any)["reference"].(string)
	if err := w.srv.Orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	var indexed int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM customer_phone_index i JOIN storefront_orders o ON o.id = i.order_id WHERE o.reference = $1`, app).Scan(&indexed); err != nil || indexed != 1 {
		t.Fatalf("the app order is not in the phone index: %d %v", indexed, err)
	}
	time.Sleep(5 * time.Millisecond)
	// After the account: by phone in Mama's Kitchen, paid by transfer
	// (due), and later in Ada Stores (no chat with it).
	transfer := w.placeByPhone(t, w.biz, number, storefront.ChatPayTransfer, nil)
	later := w.placeByPhone(t, adaID, number, storefront.ChatPayLater, nil)
	// Another person's app order made with the same number: never Tolu's.
	ade, _ := w.personIn(t, "+2348031130102")
	adeID := personID(t, ade)
	w.placeByPhone(t, adaID, number, storefront.ChatPayLater, &adeID)

	got := tolu.must(200, "GET", "/v1/me/orders", nil)
	list := got["orders"].([]any)
	want := []string{later.Reference, transfer.Reference, app}
	if fmt.Sprint(refs(list)) != fmt.Sprint(want) || got["waiting_count"] != float64(1) || got["next"] != nil {
		t.Fatalf("orders: %v want %v (%v)", refs(list), want, got)
	}
	if b, _ := json.Marshal(got); strings.Contains(string(b), early.Reference) || strings.Contains(string(b), "+234") {
		t.Fatalf("a waiting order, or a number, shown: %s", b)
	}
	// Each row: only its fields; the business; what; Pay only when due.
	keys := []string{}
	for k := range list[2].(map[string]any) {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if fmt.Sprint(keys) != "[business business_id chat_id due_kobo kind made_at pay reference state summary total_kobo]" {
		t.Fatalf("a row's fields: %v", keys)
	}
	a := list[2].(map[string]any)
	if a["business"] != "Mama's Kitchen" || a["business_id"] != w.biz.String() || a["chat_id"] != chat || a["kind"] != "order" ||
		a["summary"] != "2 × Jollof rice, 1 × Chapman" || a["total_kobo"] != float64(1050000) || a["pay"] != false {
		t.Fatalf("the app order: %v", a)
	}
	tr := list[1].(map[string]any)
	if tr["state"] != "awaiting_payment" || tr["pay"] != true || tr["due_kobo"] != float64(60000) || tr["chat_id"] != chat {
		t.Fatalf("the transfer order: %v", tr)
	}
	if l := list[0].(map[string]any); l["business"] != "Ada Stores" || l["chat_id"] != nil || l["pay"] != false || l["summary"] != "3 × Puff-puff" {
		t.Fatalf("the later order: %v", l)
	}

	// Paging: two, then one; the pages together are the list.
	p1 := tolu.must(200, "GET", "/v1/me/orders?limit=2", nil)
	next, _ := p1["next"].(string)
	if len(p1["orders"].([]any)) != 2 || next == "" {
		t.Fatalf("page 1: %v", p1)
	}
	p2 := tolu.must(200, "GET", "/v1/me/orders?limit=2&before="+url.QueryEscape(next), nil)
	if fmt.Sprint(append(refs(p1["orders"].([]any)), refs(p2["orders"].([]any))...)) != fmt.Sprint(want) || p2["next"] != nil {
		t.Fatalf("pages: %v %v", p1, p2)
	}
	tolu.must(400, "GET", "/v1/me/orders?before=nope", nil)
	tolu.must(400, "GET", "/v1/me/orders?limit=500", nil)

	// "These are mine": the early order joins, in its place.
	tolu.must(200, "POST", "/v1/me/storefront-orders/confirm", nil)
	got = tolu.must(200, "GET", "/v1/me/orders", nil)
	if fmt.Sprint(refs(got["orders"].([]any))) != fmt.Sprint(append(want, early.Reference)) || got["waiting_count"] != float64(0) {
		t.Fatalf("after These are mine: %v", got)
	}

	// Pay: the transfer order's link through the door; nothing due, 409.
	base := "/v1/me/orders/" + w.biz.String() + "/"
	if p := tolu.must(200, "GET", base+transfer.Reference+"/pay", nil); p["url"] == "" || p["due_kobo"] != float64(60000) {
		t.Fatalf("pay: %v", p)
	}
	tolu.must(409, "GET", base+app+"/pay", nil)
	if w.audits(t, "customer.pay", toluID) != 1 { // the refused one is rolled back with its entry
		t.Fatalf("paying is not audited in the business's chain")
	}

	// One order: its lines, through the narrow door, audited.
	d := tolu.must(200, "GET", base+app, nil)
	if d["reference"] != app || d["fulfilment"] != "pickup" || d["address"] != nil || len(d["lines"].([]any)) != 2 ||
		d["lines"].([]any)[0].(map[string]any)["name"] != "Jollof rice" || d["chat_id"] != chat {
		t.Fatalf("one order: %v", d)
	}
	if w.audits(t, "customer.order_detail", toluID) != 1 {
		t.Fatalf("one order's look is not audited")
	}
	// Ada Stores, with no chat: the detail is reached all the same.
	if d := tolu.must(200, "GET", "/v1/me/orders/"+adaID.String()+"/"+later.Reference, nil); d["business"] != "Ada Stores" || d["chat_id"] != nil {
		t.Fatalf("an order with no chat: %v", d)
	}

	// Isolation: another person sees none of it and reaches none of it; a
	// reference under another business is no order; the other person's
	// app order made with Tolu's number is theirs alone.
	if got := ade.must(200, "GET", "/v1/me/orders", nil); len(got["orders"].([]any)) != 1 || got["waiting_count"] != float64(0) {
		t.Fatalf("another person's list: %v", got)
	}
	ade.must(404, "GET", base+app, nil)
	ade.must(404, "GET", base+transfer.Reference+"/pay", nil)
	tolu.must(404, "GET", "/v1/me/orders/"+adaID.String()+"/"+app, nil)
	tolu.must(404, "GET", "/v1/me/orders/"+adaID.String()+"/"+app+"/pay", nil)
	tolu.must(404, "GET", "/v1/me/orders/not-a-business/"+app, nil)
	// The function itself, for another person than the one in scope.
	err := db.InPersonTx(ctx, w.env.DB.App, adeID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT * FROM taskiem_person_orders($1, NULL, NULL, NULL, NULL, NULL, 10)`, toluID)
		return err
	})
	if err == nil {
		t.Fatal("another person's orders were read")
	}
	err = db.InPersonTx(ctx, w.env.DB.App, adeID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_person_order_enter($1, NULL, $2, $3, 'customer.order_detail')`, toluID, w.biz, app)
		return err
	})
	if err == nil {
		t.Fatal("the order door opened for another person")
	}
	// Through the order door, only that business's rows.
	err = db.InPersonTx(ctx, w.env.DB.App, toluID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT taskiem_person_order_enter($1, NULL, $2, $3, 'customer.order_detail')`, toluID, w.biz, app); err != nil {
			return err
		}
		var other int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM storefront_orders WHERE tenant_id = $1`, adaID).Scan(&other); err != nil {
			return err
		}
		if other != 0 {
			return fmt.Errorf("another business's orders through the door: %d", other)
		}
		// A second door in the same transaction is refused.
		_, err := tx.Exec(ctx, `SELECT taskiem_person_order_enter($1, NULL, $2, $3, 'customer.order_detail')`, toluID, adaID, later.Reference)
		if err == nil {
			return fmt.Errorf("a second door opened")
		}
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "already open") {
		t.Fatalf("a second door in one transaction: %v", err)
	}

	// The business blocks Tolu: no Pay on the list, and its door shut.
	w.owner.must(204, "POST", "/v1/customer-chats/"+w.threadOf(t, toluID)+"/block", nil)
	for _, o := range tolu.must(200, "GET", "/v1/me/orders", nil)["orders"].([]any) {
		if m := o.(map[string]any); m["reference"] == transfer.Reference && m["pay"] != false {
			t.Fatalf("pay while blocked: %v", m)
		}
	}
	if e := tolu.must(403, "GET", base+transfer.Reference+"/pay", nil); e["blocked"] != true {
		t.Fatalf("pay while blocked: %v", e)
	}
	w.owner.must(204, "DELETE", "/v1/customer-chats/"+w.threadOf(t, toluID)+"/block", nil)

	// The Orders area off: no Pay, and the door says so.
	w.owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]any{"storefront": false, "orders": false}, "confirm": true})
	if e := tolu.must(409, "GET", base+transfer.Reference, nil); e["area_off"] != true {
		t.Fatalf("orders off: %v", e)
	}
	for _, o := range tolu.must(200, "GET", "/v1/me/orders", nil)["orders"].([]any) {
		if m := o.(map[string]any); m["pay"] != false {
			t.Fatalf("pay with Orders off: %v", m)
		}
	}
	w.owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]any{"storefront": true, "orders": true}})

	// The business's order is unchanged by any of it.
	if so := w.owner.must(200, "GET", "/v1/storefront/orders/"+transfer.Reference, nil)["order"].(map[string]any); so["status"] != "awaiting_payment" {
		t.Fatalf("the business's order: %v", so)
	}
}

// Saved addresses (§13): the person's own; one default; added, edited and
// deleted; offered when ordering, where the chosen one is copied into the
// order and the business sees only that copy, never the list (not even
// through the door); gone with the account, while the business keeps its
// orders and the phone index its rows (§14).
func TestSavedAddresses(t *testing.T) {
	ctx := context.Background()
	w := ordersWorld(t)
	tolu, _ := w.personIn(t, "+2348031130201")
	toluID := personID(t, tolu)

	if got := tolu.must(200, "GET", "/v1/me/addresses", nil); len(got["addresses"].([]any)) != 0 {
		t.Fatalf("none yet: %v", got)
	}
	home := tolu.must(201, "POST", "/v1/me/addresses", map[string]any{"label": " Home ", "line": "12 Herbert Macaulay Way", "area": "Yaba",
		"landmark": "Opposite the church", "phone": "0803 113 0299"})
	if home["default"] != true || home["label"] != "Home" || home["phone"] != "+2348031130299" {
		t.Fatalf("the first is the default: %v", home)
	}
	work := tolu.must(201, "POST", "/v1/me/addresses", map[string]any{"label": "Work", "line": "3 Broad Street", "area": "Lagos Island"})
	if work["default"] != false || work["phone"] != nil {
		t.Fatalf("a second: %v", work)
	}
	if e := tolu.must(400, "POST", "/v1/me/addresses", map[string]any{"label": "Mum's", "line": "x", "phone": "12"}); e["phone_invalid"] != true {
		t.Fatalf("a phone that is not one: %v", e)
	}
	tolu.must(400, "POST", "/v1/me/addresses", map[string]any{"label": "", "line": "x"})
	tolu.must(400, "POST", "/v1/me/addresses", map[string]any{"label": "Mum's"})
	tolu.must(400, "POST", "/v1/me/addresses", map[string]any{"label": "Mum's", "line": strings.Repeat("x", 201)})

	// Work becomes the default; edited fields only change.
	got := tolu.must(200, "PUT", "/v1/me/addresses/"+work["id"].(string), map[string]any{"default": true, "landmark": "By the bank"})
	if got["default"] != true || got["line"] != "3 Broad Street" || got["landmark"] != "By the bank" {
		t.Fatalf("edited: %v", got)
	}
	list := tolu.must(200, "GET", "/v1/me/addresses", nil)["addresses"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["id"] != work["id"] || list[1].(map[string]any)["default"] != false {
		t.Fatalf("the default first, one default: %v", list)
	}
	tolu.must(200, "PUT", "/v1/me/addresses/"+home["id"].(string), map[string]any{"phone": ""})
	// Deleting the default makes the one left the default.
	tolu.must(204, "DELETE", "/v1/me/addresses/"+work["id"].(string), nil)
	tolu.must(404, "DELETE", "/v1/me/addresses/"+work["id"].(string), nil)
	list = tolu.must(200, "GET", "/v1/me/addresses", nil)["addresses"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["default"] != true || list[0].(map[string]any)["phone"] != nil {
		t.Fatalf("after deleting the default: %v", list)
	}
	tolu.must(200, "PUT", "/v1/me/addresses/"+home["id"].(string), map[string]any{"phone": "+2348031130299"})

	// Another person: none of Tolu's, and cannot touch them.
	ade, _ := w.personIn(t, "+2348031130202")
	if got := ade.must(200, "GET", "/v1/me/addresses", nil); len(got["addresses"].([]any)) != 0 {
		t.Fatalf("another person's addresses: %v", got)
	}
	ade.must(404, "PUT", "/v1/me/addresses/"+home["id"].(string), map[string]any{"label": "Mine"})
	ade.must(404, "DELETE", "/v1/me/addresses/"+home["id"].(string), nil)

	// Up to 20.
	for i := range 19 {
		ade.must(201, "POST", "/v1/me/addresses", map[string]any{"label": fmt.Sprintf("Place %d", i), "line": "1 Road"})
	}
	ade.must(201, "POST", "/v1/me/addresses", map[string]any{"label": "Last", "line": "1 Road"})
	ade.must(409, "POST", "/v1/me/addresses", map[string]any{"label": "One more", "line": "1 Road"})

	// Ordering with a saved address: copied into the order.
	chat := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	var jollof string
	for _, it := range tolu.must(200, "GET", "/v1/me/chats/"+chat+"/shop", nil)["items"].([]any) {
		if m := it.(map[string]any); m["name"] == "Jollof rice" {
			jollof = m["id"].(string)
		}
	}
	order := map[string]any{"lines": []any{map[string]any{"item_id": jollof, "quantity": 1}}, "fulfilment": "delivery", "zone": "Yaba", "address_id": home["id"]}
	adeAddress := ade.must(200, "GET", "/v1/me/addresses", nil)["addresses"].([]any)[0].(map[string]any)["id"]
	tolu.must(404, "POST", "/v1/me/chats/"+chat+"/orders", map[string]any{"lines": order["lines"], "fulfilment": "delivery", "zone": "Yaba", "address_id": adeAddress}, "Idempotency-Key", "s0")
	ref := tolu.must(201, "POST", "/v1/me/chats/"+chat+"/orders", order, "Idempotency-Key", "s1")["order"].(map[string]any)["reference"].(string)
	if err := w.srv.Orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	so := w.owner.must(200, "GET", "/v1/storefront/orders/"+ref+"?reveal=true", nil)["order"].(map[string]any)
	c := so["customer"].(map[string]any)
	if c["address"] != "12 Herbert Macaulay Way, Yaba" || c["landmark"] != "Opposite the church. Phone for delivery: +2348031130299" {
		t.Fatalf("the address on the business's order: %v", c)
	}
	d := tolu.must(200, "GET", "/v1/me/orders/"+w.biz.String()+"/"+ref, nil)
	if a, _ := d["address"].(map[string]any); a == nil || a["address"] != "12 Herbert Macaulay Way, Yaba" || d["zone"] != "Yaba" {
		t.Fatalf("the address on the person's order: %v", d)
	}
	// The saved list stays the person's: a business's own scope reads
	// none, and neither does the person's transaction once a door is open.
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.biz}, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM saved_objects WHERE person_id IS NOT NULL`).Scan(&n); err != nil || n != 0 {
			return fmt.Errorf("the business read %d saved addresses (%w)", n, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.InPersonTx(ctx, w.env.DB.App, toluID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT taskiem_customer_enter($1, $2, 'customer.chat', '{}')`, toluID, w.biz); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM saved_objects WHERE person_id IS NOT NULL`).Scan(&n); err != nil || n != 0 {
			return fmt.Errorf("saved addresses through the door: %d (%w)", n, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(w.owner.must(200, "GET", "/v1/storefront/orders/"+ref, nil)); strings.Contains(string(b), "Home") {
		t.Fatalf("the saved address's name reached the business: %s", b)
	}

	// The users row deleted by hand (its sign-ins first; the route,
	// POST /v1/me/delete, empties the row instead): its addresses and its
	// list go; the business's order stays, and the index's row goes, the
	// order marked as never claimable by phone (decision B, 00467).
	for _, q := range []string{`DELETE FROM sessions WHERE user_id = $1`, `DELETE FROM user_phones WHERE user_id = $1`, `DELETE FROM users WHERE id = $1`} {
		if _, err := w.env.DB.Admin.Exec(ctx, q, toluID); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var addresses, kept, indexed int
	var person *uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM saved_objects WHERE person_id = $1 AND kind = 'place'),
		(SELECT count(*) FROM storefront_orders WHERE reference = $2), (SELECT person_id FROM storefront_orders WHERE reference = $2),
		(SELECT count(*) FROM customer_phone_index i JOIN storefront_orders o ON o.id = i.order_id WHERE o.reference = $2)`, toluID, ref).
		Scan(&addresses, &kept, &person, &indexed); err != nil {
		t.Fatal(err)
	}
	if addresses != 0 || kept != 1 || person != nil || indexed != 0 {
		t.Fatalf("after deleting the account: addresses %d, order %d (person %v), index %d", addresses, kept, person, indexed)
	}
	if so := w.owner.must(200, "GET", "/v1/storefront/orders/"+ref+"?reveal=true", nil)["order"].(map[string]any); so["customer"].(map[string]any)["address"] != "12 Herbert Macaulay Way, Yaba" {
		t.Fatalf("the business's record after the account went: %v", so)
	}
}

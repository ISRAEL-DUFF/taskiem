package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneylog"
)

// Ordering together, the review's fixes ("As built: stage 5b, ordering
// together", "Review fixes").

// "Each pays" (fixes 1, 2, 4): the order is placed paid in shares, with a
// day to pay and no page of its own for the whole (its own pay link is
// refused, so no one pays it all on top of the shares); the placer with no
// items of their own has a share carrying the kobo left over, and the
// basket names who reads it (you_id) to compare with placed_by.
func TestBasketsReviewEachPays(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	pay := newBasketPay()
	w.srv.Orders.Payments = pay
	w.srv.Orders.MoneyLog = &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	ada, adaID := w.many(t, "+2348031163001", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031163002", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031163003", "Chidi Eze")
	for _, c := range []*client{bola, chidi} {
		adds(t, ada, c)
		adds(t, c, ada)
	}
	gid, _ := newGroup(t, ada, "Lunch", bolaID, chidiID)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_actions SET payment = 'at_order' WHERE tenant_id = $1 AND kind = 'order' AND builtin`, w.biz); err != nil {
		t.Fatal(err)
	}
	id := ada.must(201, "POST", "/v1/me/baskets", map[string]any{"business_id": w.biz, "group_id": gid})["basket"].(map[string]any)["id"].(string)
	chapman := shopIDs(t, bola, id)["Chapman"]
	mine(t, bola, id, line(chapman, 1))
	mine(t, chidi, id, line(chapman, 1))
	// Delivered to Yaba (₦1,000): ₦500 each for the two with items.
	ada.must(200, "PUT", "/v1/me/baskets/"+id, map[string]any{"fulfilment": "delivery", "zone": "Yaba"})
	ada.must(200, "POST", "/v1/me/baskets/"+id+"/close", nil)
	placed := ada.must(200, "POST", "/v1/me/baskets/"+id+"/place", map[string]any{"payment": "each", "address": "12 Herbert Macaulay Way"})
	ref := placed["order"].(map[string]any)["reference"].(string)
	if err := w.srv.Orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	v := ada.must(200, "GET", "/v1/me/baskets/"+id, nil)
	if v["you_id"] != adaID.String() || v["placed_by"] != adaID.String() {
		t.Fatalf("who reads it: %v", v)
	}
	me := basketPerson(v, adaID)
	if me == nil || me["share"] == nil || me["share"].(map[string]any)["amount_kobo"] != float64(0) {
		t.Fatalf("the placer with no items has no share: %v", v["people"])
	}
	if s := basketPerson(v, bolaID)["share"].(map[string]any); s["delivery_kobo"] != float64(50000) || s["refund_due"] != false {
		t.Fatalf("Bola's share: %v", s)
	}
	o := v["order"].(map[string]any)
	payBy, _ := time.Parse(time.RFC3339, o["pay_by"].(string))
	if o["status"] != "awaiting_payment" || time.Until(payBy) < 23*time.Hour {
		t.Fatalf("a day to pay the shares: %v", o)
	}
	var paidInShares bool
	var url *string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT paid_in_shares, payment_url FROM storefront_orders WHERE reference = $1`, ref).Scan(&paidInShares, &url); err != nil || !paidInShares || url != nil {
		t.Fatalf("placed paid in shares: %v %v %v", paidInShares, url, err)
	}
	// The order's own pay link (You's orders) would take it all again.
	ada.must(409, "GET", "/v1/me/orders/"+w.biz.String()+"/"+ref+"/pay", nil)
	// Each pays their own share; the placer can pay what is left for one.
	bp := bola.must(200, "GET", "/v1/me/baskets/"+id+"/pay", nil)
	if bp["amount_kobo"] != float64(200000) {
		t.Fatalf("Bola's share link: %v", bp)
	}
	cp := ada.must(200, "GET", "/v1/me/baskets/"+id+"/pay?for="+chidiID.String(), nil)
	pay.pay(bp["url"].(string))
	pay.pay(cp["url"].(string))
	if err := w.srv.Orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	if v = chidi.must(200, "GET", "/v1/me/baskets/"+id, nil); v["order"].(map[string]any)["payment_state"] != "paid" {
		t.Fatalf("paid by its shares: %v", v["order"])
	}
	if so := w.owner.must(200, "GET", "/v1/storefront/orders/"+ref, nil)["order"].(map[string]any); so["paid_kobo"] != float64(400000) {
		t.Fatalf("the business's order, not overpaid: %v", so)
	}
}

// Holding and placing (fixes 5, 6, 10): a member with no items is handed
// the basket and sees the members; a second place while the first is at
// work is refused; a retry of a basket whose order exists finishes it with
// that order, nothing checked again; a basket stuck placing whose holder
// left is taken over and closed; by SQL, a member who doesn't hold it
// changes nothing of the holder's, adds no line once closed or placed, and
// writes no share.
func TestBasketsReviewHoldingAndPlacing(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, adaID := w.many(t, "+2348031164001", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031164002", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031164003", "Chidi Eze")
	for _, c := range []*client{bola, chidi} {
		adds(t, ada, c)
		adds(t, c, ada)
	}
	gid, _ := newGroup(t, ada, "Lunch", bolaID, chidiID)
	id := ada.must(201, "POST", "/v1/me/baskets", map[string]any{"business_id": w.biz, "group_id": gid})["basket"].(map[string]any)["id"].(string)
	chapman := shopIDs(t, ada, id)["Chapman"]
	mine(t, chidi, id, line(chapman, 1))
	ada.must(200, "PUT", "/v1/me/baskets/"+id, map[string]any{"fulfilment": "pickup"})

	// Members (with or without items) and who reads it.
	v := bola.must(200, "GET", "/v1/me/baskets/"+id, nil)
	if v["you_id"] != bolaID.String() || len(v["members"].([]any)) != 3 {
		t.Fatalf("members: %v", v)
	}
	// Handed to Bola, who has no items, and back.
	if v = ada.must(200, "PUT", "/v1/me/baskets/"+id+"/holder", map[string]any{"person_id": bolaID}); v["holder_id"] != bolaID.String() {
		t.Fatalf("handed to Bola: %v", v)
	}
	bola.must(200, "PUT", "/v1/me/baskets/"+id+"/holder", map[string]any{"person_id": adaID})
	ada.must(200, "POST", "/v1/me/baskets/"+id+"/close", nil)

	// By SQL, Bola (not holding it) changes nothing of the holder's, and
	// adds no line to a closed basket.
	for _, q := range []string{`UPDATE people_baskets SET state = 'open' WHERE id = $1`, `UPDATE people_baskets SET holder_id = '` + bolaID.String() + `' WHERE id = $1`,
		`UPDATE people_baskets SET payment = 'all' WHERE id = $1`,
		`INSERT INTO people_basket_lines (id, basket_id, person_id, item_id, quantity, name, price_kobo) VALUES (gen_random_uuid(), $1, '` + bolaID.String() + `', 'x', 1, 'x', 1)`,
		`INSERT INTO people_basket_shares (basket_id, person_id, key, items_kobo, delivery_kobo, amount_kobo) VALUES ($1, '` + bolaID.String() + `', gen_random_uuid(), 0, 0, 0)`} {
		err := db.InPersonTx(ctx, w.env.DB.App, bolaID, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, q, id)
			if err == nil && tag.RowsAffected() == 0 {
				return pgx.ErrNoRows
			}
			return err
		})
		if err == nil {
			t.Fatalf("Bola by SQL: %s", q)
		}
	}

	// Another place at work: refused.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_baskets SET state = 'placing' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	ada.must(409, "POST", "/v1/me/baskets/"+id+"/place", map[string]any{"payment": "all"})
	// Stuck a while (the first lost before it placed anything): placed now.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_baskets SET placing_at = now() - interval '5 minutes' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	placed := ada.must(200, "POST", "/v1/me/baskets/"+id+"/place", map[string]any{"payment": "all"})
	ref := placed["order"].(map[string]any)["reference"].(string)

	// The order exists, but the basket was left placing (its last step cut
	// off): a retry finds the order and finishes, the shop not read again
	// (a price changed meanwhile does not stop it, nor place another).
	tx, err := w.env.DB.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`SET LOCAL session_replication_role = replica`,
		`UPDATE people_baskets SET state = 'placing', placing_at = now(), order_reference = NULL, placed_by = NULL, placed_at = NULL, placed_kobo = NULL WHERE id = $1`,
		`DELETE FROM people_basket_shares WHERE basket_id = $1`} {
		var args []any
		if q != `SET LOCAL session_replication_role = replica` {
			args = []any{id}
		}
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_catalogue SET item = jsonb_set(item, '{price_kobo}', '999900') WHERE item->>'id' = $1`, chapman); err != nil {
		t.Fatal(err)
	}
	again := ada.must(200, "POST", "/v1/me/baskets/"+id+"/place", map[string]any{"payment": "all", "total_kobo": 1})
	if again["order"].(map[string]any)["reference"] != ref || again["basket"].(map[string]any)["state"] != "placed" {
		t.Fatalf("the retry: %v", again)
	}
	// By SQL, no line is added to a placed basket, even by its placer.
	err = db.InPersonTx(ctx, w.env.DB.App, adaID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO people_basket_lines (id, basket_id, person_id, item_id, quantity, name, price_kobo) VALUES (gen_random_uuid(), $1, $2, 'x', 1, 'x', 1)`, id, adaID)
		return err
	})
	if err == nil {
		t.Fatal("a line added to a placed basket")
	}

	// A basket stuck placing whose holder left the group: taken over and
	// closed again, not stuck for ever.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_catalogue SET item = jsonb_set(item, '{price_kobo}', '150000') WHERE item->>'id' = $1`, chapman); err != nil {
		t.Fatal(err)
	}
	b2 := bola.must(201, "POST", "/v1/me/baskets", map[string]any{"business_id": w.biz, "group_id": gid})["basket"].(map[string]any)["id"].(string)
	mine(t, chidi, b2, line(chapman, 1))
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_baskets SET state = 'placing' WHERE id = $1`, b2); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_baskets SET placing_at = now() - interval '1 hour' WHERE id = $1`, b2); err != nil {
		t.Fatal(err)
	}
	bola.must(200, "POST", "/v1/me/groups/"+gid+"/leave", nil)
	if v = chidi.must(200, "PUT", "/v1/me/baskets/"+b2+"/holder", map[string]any{"person_id": chidiID}); v["holder_id"] != chidiID.String() || v["state"] != "closed" {
		t.Fatalf("taken over when stuck: %v", v)
	}
}

// Seeing a basket (fixes 7, 9): looking at it writes nothing in the
// business's audit chain (adding items does); a business that blocked the
// person shows them the basket without its shop, not a refusal.
func TestBasketsReviewLookAndBlocked(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348031165001", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031165002", "Bola Ade")
	adds(t, ada, bola)
	adds(t, bola, ada)
	gid, _ := newGroup(t, ada, "Lunch", bolaID)
	id := ada.must(201, "POST", "/v1/me/baskets", map[string]any{"business_id": w.biz, "group_id": gid})["basket"].(map[string]any)["id"].(string)
	chapman := shopIDs(t, bola, id)["Chapman"]
	before := w.audits(t, "customer.basket", bolaID)
	for range 3 {
		bola.must(200, "GET", "/v1/me/baskets/"+id, nil)
	}
	shopIDs(t, bola, id)
	if n := w.audits(t, "customer.basket", bolaID); n != before {
		t.Fatalf("looking audited: %d → %d", before, n)
	}
	mine(t, bola, id, line(chapman, 1))
	if n := w.audits(t, "customer.basket", bolaID); n != before+1 {
		t.Fatalf("adding items not audited: %d → %d", before, n)
	}
	// The business blocks Bola: the basket without its shop.
	bola.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_conversations SET blocked_at = now() WHERE tenant_id = $1 AND person_id = $2`, w.biz, bolaID); err != nil {
		t.Fatal(err)
	}
	v := bola.must(200, "GET", "/v1/me/baskets/"+id, nil)
	if v["shop"] != nil || v["id"] != id {
		t.Fatalf("blocked: %v", v)
	}
	bola.must(403, "PUT", "/v1/me/baskets/"+id+"/mine", map[string]any{"lines": []any{line(chapman, 2)}})
}

package api_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Ordering together (people-chatting §10.3, §11 A and C, §17 Q8; 5b C with
// the founder's C1, C2, C3; "As built: stage 5b, ordering together").

// basketPay is a payment provider for baskets: each page made, paid when
// told.
type basketPay struct {
	mu      sync.Mutex
	created map[string]storefront.PaymentRequest
	paid    map[string]bool
}

func newBasketPay() *basketPay {
	return &basketPay{created: map[string]storefront.PaymentRequest{}, paid: map[string]bool{}}
}

func (p *basketPay) Create(_ context.Context, _ uuid.UUID, _ string, r storefront.PaymentRequest) (string, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.created[r.Reference] = r
	return "https://checkout.paystack.test/" + r.Reference, r.Reference, nil
}

func (p *basketPay) Verify(_ context.Context, _ uuid.UUID, _ string, ref string) (string, int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.paid[ref] {
		return "paid", p.created[ref].AmountKobo, nil
	}
	return "pending", 0, nil
}

func (p *basketPay) Refund(context.Context, uuid.UUID, string, string, string) (string, error) {
	return "pending", nil
}

func (p *basketPay) pay(url string) {
	p.mu.Lock()
	p.paid[strings.TrimPrefix(url, "https://checkout.paystack.test/")] = true
	p.mu.Unlock()
}

// shopIDs are the basket's shop's items by name.
func shopIDs(t *testing.T, c *client, basket string) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for _, x := range c.must(200, "GET", "/v1/me/baskets/"+basket+"/shop", nil)["items"].([]any) {
		ids[x.(map[string]any)["name"].(string)] = x.(map[string]any)["id"].(string)
	}
	return ids
}

func mine(t *testing.T, c *client, basket string, lines ...any) map[string]any {
	t.Helper()
	return c.must(200, "PUT", "/v1/me/baskets/"+basket+"/mine", map[string]any{"lines": lines})
}

func line(item string, n int) map[string]any { return map[string]any{"item_id": item, "quantity": n} }

// personOf is the basket's person with this id.
func basketPerson(b map[string]any, id uuid.UUID) map[string]any {
	for _, p := range b["people"].([]any) {
		if p.(map[string]any)["person_id"] == id.String() {
			return p.(map[string]any)
		}
	}
	return nil
}

// In a group (§10.3, C1, C2 "I'll pay all"): the card posted; each member
// adds their own lines from the shop; who wants what; the holder closes,
// reopens, hands it on; places it as their own order with a note of whose
// lines are whose (never the group); the card follows the order; shares
// with an equal part of the delivery; "Your share" cards in one step; a
// member who left before closing has their lines taken out; an outsider
// reaches nothing; a basket card is not forwarded.
func TestBasketsInAGroup(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, adaID := w.many(t, "+2348031160001", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031160002", "Bola Ade")
	chidi, chidiID := w.many(t, "+2348031160003", "Chidi Eze")
	dayo, dayoID := w.many(t, "+2348031160004", "Dayo Bello")
	outsider, _ := w.many(t, "+2348031160005", "Out Sider")
	for _, c := range []*client{bola, chidi, dayo} {
		adds(t, ada, c)
		adds(t, c, ada)
	}
	gid, added := newGroup(t, ada, "Family lunch", bolaID, chidiID, dayoID)
	if added[bolaID] != "added" || added[chidiID] != "added" || added[dayoID] != "added" {
		t.Fatalf("added: %v", added)
	}

	// Start: the card in the group, the basket open, Ada holding it.
	ada.must(400, "POST", "/v1/me/baskets", map[string]any{"business_id": w.biz})
	ada.must(404, "POST", "/v1/me/baskets", map[string]any{"business_id": w.biz, "group_id": uuid.NewString()})
	outsider.must(404, "POST", "/v1/me/baskets", map[string]any{"business_id": w.biz, "group_id": gid})
	started := ada.must(201, "POST", "/v1/me/baskets", map[string]any{"business_id": w.biz, "group_id": gid})
	b := started["basket"].(map[string]any)
	id := b["id"].(string)
	if b["state"] != "open" || b["holder_id"] != adaID.String() || b["you_hold"] != true || b["business"] != "Mama's Kitchen" || b["where"] != "group" || b["group_id"] != gid {
		t.Fatalf("started: %v", b)
	}
	if sh := b["shop"].(map[string]any); sh["open"] != true || sh["each_offered"] != false || sh["payment"] != "none" {
		t.Fatalf("the shop: %v", sh)
	}
	msg := started["message"].(map[string]any)
	if c := msg["card"].(map[string]any); c["type"] != "basket" || c["basket_id"] != id || c["title"] != "Order together from Mama's Kitchen" {
		t.Fatalf("the card: %v", msg)
	}
	seen := gmessage(t, bola, gid, msg["body"].(string))
	if seen["card"].(map[string]any)["basket_id"] != id {
		t.Fatalf("Bola's card: %v", seen)
	}
	// Opening the card reads the basket live.
	if live := bola.must(200, "GET", "/v1/me/groups/"+gid+"/messages/"+seen["id"].(string)+"/card", nil)["live"].(map[string]any); live["basket"].(map[string]any)["id"] != id {
		t.Fatalf("the card opened: %v", live)
	}
	// Not forwarded.
	bola.must(409, "POST", "/v1/me/forward", map[string]any{"group_id": gid, "message_id": seen["id"], "to_groups": []string{gid}})

	// Each adds their own; the card shows who wants what.
	ids := shopIDs(t, bola, id)
	jollof, chapman := ids["Jollof rice"], ids["Chapman"]
	bola.must(422, "PUT", "/v1/me/baskets/"+id+"/mine", map[string]any{"lines": []any{line("nope", 1)}})
	bola.must(400, "PUT", "/v1/me/baskets/"+id+"/mine", map[string]any{"lines": []any{line(jollof, 0)}})
	mine(t, bola, id, line(jollof, 2))
	mine(t, chidi, id, line(chapman, 1))
	mine(t, dayo, id, line(chapman, 2))
	got := mine(t, ada, id, line(jollof, 1), line(chapman, 1))
	if p := basketPerson(got, bolaID); p == nil || p["name"] != "Bola Ade" || p["items_kobo"] != float64(900000) || len(p["lines"].([]any)) != 1 {
		t.Fatalf("Bola's lines as Ada sees them: %v", got)
	}
	if p := basketPerson(got, adaID); p["name"] != "You" || p["you"] != true || p["items_kobo"] != float64(600000) {
		t.Fatalf("Ada's own: %v", p)
	}
	if got["items_kobo"] != float64(1950000) || len(got["people"].([]any)) != 4 {
		t.Fatalf("the whole basket: %v", got)
	}
	outsider.must(404, "GET", "/v1/me/baskets/"+id, nil)
	outsider.must(404, "PUT", "/v1/me/baskets/"+id+"/mine", map[string]any{"lines": []any{line(jollof, 1)}})

	// Pick-up or delivery and the area: the holder's.
	bola.must(403, "PUT", "/v1/me/baskets/"+id, map[string]any{"fulfilment": "pickup"})
	got = ada.must(200, "PUT", "/v1/me/baskets/"+id, map[string]any{"fulfilment": "delivery", "zone": "Ikeja"})
	if !strings.Contains(strings.Join(anyStrings(got["problems"]), " "), "areas they deliver to") {
		t.Fatalf("an area not delivered to: %v", got["problems"])
	}
	got = ada.must(200, "PUT", "/v1/me/baskets/"+id, map[string]any{"zone": "Yaba"})
	if len(got["problems"].([]any)) != 0 || got["delivery_kobo"] != float64(100000) || got["total_kobo"] != float64(2050000) {
		t.Fatalf("delivered to Yaba: %v", got)
	}

	// C1: only the holder closes; closed, no one changes lines; reopened
	// until placed; handed on and back.
	bola.must(403, "POST", "/v1/me/baskets/"+id+"/close", nil)
	ada.must(409, "POST", "/v1/me/baskets/"+id+"/place", map[string]any{"payment": "all", "address": "12 Herbert Macaulay Way"})
	if got = ada.must(200, "POST", "/v1/me/baskets/"+id+"/close", nil); got["state"] != "closed" {
		t.Fatalf("closed: %v", got)
	}
	bola.must(409, "PUT", "/v1/me/baskets/"+id+"/mine", map[string]any{"lines": []any{line(jollof, 3)}})
	ada.must(200, "POST", "/v1/me/baskets/"+id+"/reopen", nil)
	mine(t, bola, id, line(jollof, 1))
	ada.must(409, "PUT", "/v1/me/baskets/"+id+"/holder", map[string]any{"person_id": outsiderID(t, outsider)})
	if got = ada.must(200, "PUT", "/v1/me/baskets/"+id+"/holder", map[string]any{"person_id": bolaID}); got["holder_id"] != bolaID.String() || got["you_hold"] != false {
		t.Fatalf("handed on: %v", got)
	}
	ada.must(403, "POST", "/v1/me/baskets/"+id+"/close", nil)
	bola.must(200, "PUT", "/v1/me/baskets/"+id+"/holder", map[string]any{"person_id": adaID})

	// Dayo leaves before it is closed: their lines are taken out.
	dayo.must(200, "POST", "/v1/me/groups/"+gid+"/leave", nil)
	dayo.must(404, "GET", "/v1/me/baskets/"+id, nil)
	got = ada.must(200, "POST", "/v1/me/baskets/"+id+"/close", nil)
	if basketPerson(got, dayoID) != nil || got["items_kobo"] != float64(1200000) {
		t.Fatalf("Dayo's lines after leaving: %v", got)
	}

	// Placing: the total as seen, payment as offered (no payment in the
	// app: Ada pays all).
	bola.must(403, "POST", "/v1/me/baskets/"+id+"/place", map[string]any{"payment": "all", "address": "x"})
	ada.must(400, "POST", "/v1/me/baskets/"+id+"/place", map[string]any{"payment": "half"})
	if e := ada.must(409, "POST", "/v1/me/baskets/"+id+"/place", map[string]any{"payment": "each", "address": "12 Herbert Macaulay Way"}); e["code"] != "pay_all_only" {
		t.Fatalf("each without payment in the app: %v", e)
	}
	if e := ada.must(409, "POST", "/v1/me/baskets/"+id+"/place", map[string]any{"payment": "all", "address": "12 Herbert Macaulay Way", "total_kobo": 100}); e["code"] != "changed" {
		t.Fatalf("a total not as now: %v", e)
	}
	placed := ada.must(200, "POST", "/v1/me/baskets/"+id+"/place", map[string]any{"payment": "all", "address": "12 Herbert Macaulay Way", "note": "Ring twice", "total_kobo": 1300000})
	ref := placed["order"].(map[string]any)["reference"].(string)
	if err := w.srv.Orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	// Again: the same order, nothing more.
	ada.must(409, "POST", "/v1/me/baskets/"+id+"/place", map[string]any{"payment": "all", "address": "12 Herbert Macaulay Way"})

	// The business: one order from Ada, a note of whose lines are whose,
	// nothing of the group.
	so := w.owner.must(200, "GET", "/v1/storefront/orders/"+ref+"?reveal=true", nil)["order"].(map[string]any)
	note, _ := so["customer"].(map[string]any)["message"].(string)
	if so["kind"] != "app" || so["total_kobo"] != float64(1300000) || so["customer"].(map[string]any)["name"] != "Ada Obi" ||
		!strings.Contains(note, "Ordered together in Taskiem by 3 people.") || !strings.Contains(note, "Bola Ade: 1 × Jollof rice.") ||
		!strings.Contains(note, "Chidi Eze: 1 × Chapman.") || !strings.Contains(note, "Ring twice") || strings.Contains(note, "Family") {
		t.Fatalf("the business's order: %v", so)
	}
	var person uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT person_id FROM storefront_orders WHERE reference = $1`, ref).Scan(&person); err != nil || person != adaID {
		t.Fatalf("the order is Ada's own: %v %v", person, err)
	}
	var mentions int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND detail::text LIKE '%'||$2||'%'`, w.biz, gid).Scan(&mentions); err != nil || mentions != 0 {
		t.Fatalf("the group in the business's chain: %d %v", mentions, err)
	}

	// The card follows the order; each sees their share (₦1,000 delivery
	// in three: ₦333.33 each, the kobo over Ada's).
	view := bola.must(200, "GET", "/v1/me/baskets/"+id, nil)
	o := view["order"].(map[string]any)
	if view["state"] != "placed" || view["payment"] != "all" || o["reference"] != ref || o["status"] == "" || o["total_kobo"] != float64(1300000) || o["paid_kobo"] != float64(0) {
		t.Fatalf("placed, as Bola sees it: %v", view)
	}
	bs := basketPerson(view, bolaID)["share"].(map[string]any)
	as := basketPerson(view, adaID)["share"].(map[string]any)
	if bs["items_kobo"] != float64(450000) || bs["delivery_kobo"] != float64(33333) || bs["amount_kobo"] != float64(483333) || as["delivery_kobo"] != float64(33334) {
		t.Fatalf("shares: %v %v", bs, as)
	}
	bola.must(409, "GET", "/v1/me/baskets/"+id+"/pay", nil)
	bola.must(409, "PUT", "/v1/me/baskets/"+id+"/mine", map[string]any{"lines": []any{}})

	// "Your share" cards in one step, from Ada's account; once each.
	bola.must(403, "POST", "/v1/me/baskets/"+id+"/share-cards", map[string]any{})
	ada.must(400, "POST", "/v1/me/baskets/"+id+"/share-cards", map[string]any{})
	acct := map[string]any{"bank": "GTBank", "account_number": "0123456789", "account_name": "Ada Obi"}
	sc := ada.must(200, "POST", "/v1/me/baskets/"+id+"/share-cards", map[string]any{"pay_to": acct})
	if len(sc["sent"].([]any)) != 2 {
		t.Fatalf("share cards: %v", sc)
	}
	if again := ada.must(200, "POST", "/v1/me/baskets/"+id+"/share-cards", map[string]any{"pay_to": acct}); len(again["sent"].([]any)) != 0 {
		t.Fatalf("sent twice: %v", again)
	}
	var share map[string]any
	for _, m := range gmessages(t, bola, gid) {
		if c, _ := m["card"].(map[string]any); c != nil && c["type"] == "split" && c["for_id"] == bolaID.String() {
			share = c
		}
	}
	if share == nil || share["amount_kobo"] != float64(483333) || share["for"] != "Bola Ade" || share["basket_id"] != id || !strings.Contains(share["what"].(string), ref) {
		t.Fatalf("Bola's share card: %v", share)
	}
	if v := ada.must(200, "GET", "/v1/me/baskets/"+id, nil); basketPerson(v, bolaID)["share"].(map[string]any)["card_sent"] != true {
		t.Fatalf("card sent: %v", v)
	}

	// Chidi leaves after placing: still sees the basket, and his share.
	chidi.must(200, "POST", "/v1/me/groups/"+gid+"/leave", nil)
	if v := chidi.must(200, "GET", "/v1/me/baskets/"+id, nil); basketPerson(v, chidiID)["share"] == nil {
		t.Fatalf("Chidi after leaving: %v", v)
	}
	// The business moves the order: the basket follows.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET status = 'accepted' WHERE reference = $1`, ref); err != nil {
		t.Fatal(err)
	}
	if v := bola.must(200, "GET", "/v1/me/baskets/"+id, nil); v["order"].(map[string]any)["status"] == o["status"] {
		t.Fatalf("not followed: %v", v["order"])
	}

	// A second basket, cancelled by its holder; a closed group basket
	// takes nothing.
	b2 := ada.must(201, "POST", "/v1/me/baskets", map[string]any{"business_id": w.biz, "group_id": gid})["basket"].(map[string]any)["id"].(string)
	bola.must(403, "POST", "/v1/me/baskets/"+b2+"/cancel", nil)
	if c := ada.must(200, "POST", "/v1/me/baskets/"+b2+"/cancel", nil); c["state"] != "cancelled" {
		t.Fatalf("cancelled: %v", c)
	}
	bola.must(409, "PUT", "/v1/me/baskets/"+b2+"/mine", map[string]any{"lines": []any{line(jollof, 1)}})
}

func anyStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func outsiderID(t *testing.T, c *client) uuid.UUID {
	t.Helper()
	return uuid.MustParse(c.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)["id"].(string))
}

// One-to-one (C3) and "Each pays their share" (C2): the same basket in a
// chat between two people; stock running out and a price changed before
// placing are shown and refused at placing until dealt with; each pays
// their own share by its own link, the placer what is left for the other;
// the order is paid once the shares are; the holder who is gone is taken
// over; deleting an account takes its lines out of a basket not placed.
func TestBasketsOneToOneEachPays(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	pay := newBasketPay()
	w.srv.Orders.Payments = pay
	w.srv.Orders.MoneyLog = &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	ada, adaID := w.many(t, "+2348031161001", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031161002", "Bola Ade")
	adaChat := chatWith(t, ada, bolaID)
	send(t, ada, adaChat, "Lunch?")
	bolaChat := requestOf(t, bola)

	// Not while it is a request.
	bola.must(409, "POST", "/v1/me/baskets", map[string]any{"business_id": w.biz, "chat_id": bolaChat})
	bola.must(200, "POST", "/v1/me/people/"+bolaChat+"/accept", nil)

	// Payment in the app, by card: each may pay their own share.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_actions SET payment = 'at_order' WHERE tenant_id = $1 AND kind = 'order' AND builtin`, w.biz); err != nil {
		t.Fatal(err)
	}
	started := bola.must(201, "POST", "/v1/me/baskets", map[string]any{"business_id": w.biz, "chat_id": bolaChat})
	b := started["basket"].(map[string]any)
	id := b["id"].(string)
	if b["where"] != "person" || b["chat_id"] != bolaChat || b["shop"].(map[string]any)["each_offered"] != true {
		t.Fatalf("started one to one: %v", b)
	}
	if v := ada.must(200, "GET", "/v1/me/baskets/"+id, nil); v["chat_id"] != adaChat || v["holder"] != "Bola Ade" {
		t.Fatalf("as Ada sees it: %v", v)
	}
	var card map[string]any
	for _, m := range messagesOf(t, ada, adaChat) {
		if c, _ := m["card"].(map[string]any); c != nil && c["type"] == "basket" {
			card = m
		}
	}
	if card == nil {
		t.Fatal("no basket card in Ada's copy")
	}
	ada.must(409, "POST", "/v1/me/people/forward", map[string]any{"chat_id": adaChat, "message_id": card["id"], "to": []string{adaChat}})

	ids := shopIDs(t, ada, id)
	jollof, chapman := ids["Jollof rice"], ids["Chapman"]
	mine(t, ada, id, line(jollof, 2))
	mine(t, bola, id, line(chapman, 2))
	bola.must(200, "PUT", "/v1/me/baskets/"+id, map[string]any{"fulfilment": "pickup"})

	// Stock runs out and a price changes before placing.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_catalogue SET item = jsonb_set(item, '{available}', 'false') WHERE item->>'id' = $1`, jollof); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_catalogue SET item = jsonb_set(item, '{price_kobo}', '200000') WHERE item->>'id' = $1`, chapman); err != nil {
		t.Fatal(err)
	}
	v := bola.must(200, "GET", "/v1/me/baskets/"+id, nil)
	al := basketPerson(v, adaID)["lines"].([]any)[0].(map[string]any)
	bl := basketPerson(v, bolaID)["lines"].([]any)[0].(map[string]any)
	if al["available"] != false || bl["price_changed"] != true || bl["price_kobo"] != float64(200000) || bl["added_price_kobo"] != float64(150000) ||
		!strings.Contains(strings.Join(anyStrings(v["problems"]), " "), "Jollof rice is sold out now.") {
		t.Fatalf("sold out and a new price: %v", v)
	}
	ada.must(422, "PUT", "/v1/me/baskets/"+id+"/mine", map[string]any{"lines": []any{line(jollof, 1)}})
	bola.must(200, "POST", "/v1/me/baskets/"+id+"/close", nil)
	if e := bola.must(409, "POST", "/v1/me/baskets/"+id+"/place", map[string]any{"payment": "each"}); e["code"] != "basket_problems" {
		t.Fatalf("placed with a line sold out: %v", e)
	}
	// The holder takes out the sold-out line; Ada a member can't take out Bola's.
	ada.must(403, "DELETE", "/v1/me/baskets/"+id+"/lines/"+bl["id"].(string), nil)
	bola.must(200, "DELETE", "/v1/me/baskets/"+id+"/lines/"+al["id"].(string), nil)
	bola.must(200, "POST", "/v1/me/baskets/"+id+"/reopen", nil)
	mine(t, ada, id, line(chapman, 1))
	bola.must(200, "POST", "/v1/me/baskets/"+id+"/close", nil)
	placed := bola.must(200, "POST", "/v1/me/baskets/"+id+"/place", map[string]any{"payment": "each", "total_kobo": 600000})
	ref := placed["order"].(map[string]any)["reference"].(string)
	if err := w.srv.Orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	v = ada.must(200, "GET", "/v1/me/baskets/"+id, nil)
	if v["payment"] != "each" || v["order"].(map[string]any)["status"] != "awaiting_payment" || v["order"].(map[string]any)["payable_now"] != true {
		t.Fatalf("placed, each pays: %v", v)
	}

	// Each pays their own share by their own link; Ada asks only for hers.
	ada.must(403, "GET", "/v1/me/baskets/"+id+"/pay?for="+bolaID.String(), nil)
	ap := ada.must(200, "GET", "/v1/me/baskets/"+id+"/pay", nil)
	if ap["amount_kobo"] != float64(200000) || ap["url"] == "" {
		t.Fatalf("Ada's share link: %v", ap)
	}
	pay.pay(ap["url"].(string))
	if err := w.srv.Orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	v = bola.must(200, "GET", "/v1/me/baskets/"+id, nil)
	if s := basketPerson(v, adaID)["share"].(map[string]any); s["paid"] != true || v["order"].(map[string]any)["paid_kobo"] != float64(200000) ||
		v["order"].(map[string]any)["payment_state"] != "part_paid" {
		t.Fatalf("Ada's share paid: %v", v)
	}
	ada.must(409, "GET", "/v1/me/baskets/"+id+"/pay", nil)
	// The placer pays what is left for no one else here: their own.
	bp := bola.must(200, "GET", "/v1/me/baskets/"+id+"/pay?for="+bolaID.String(), nil)
	pay.pay(bp["url"].(string))
	if err := w.srv.Orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	v = ada.must(200, "GET", "/v1/me/baskets/"+id, nil)
	if v["order"].(map[string]any)["payment_state"] != "paid" {
		t.Fatalf("the order paid by its shares: %v", v["order"])
	}
	so := w.owner.must(200, "GET", "/v1/storefront/orders/"+ref, nil)["order"].(map[string]any)
	if so["paid_kobo"] != float64(600000) {
		t.Fatalf("the business's order: %v", so)
	}
	// The business's audit chain shows the basket door, not the chat.
	if w.audits(t, "customer.basket", adaID) == 0 {
		t.Fatal("the basket door is not audited")
	}

	// A holder whose account is gone: the other takes it over; the gone
	// account's lines leave a basket not placed.
	b2 := ada.must(201, "POST", "/v1/me/baskets", map[string]any{"business_id": w.biz, "chat_id": adaChat})["basket"].(map[string]any)["id"].(string)
	mine(t, ada, b2, line(chapman, 1))
	mine(t, bola, b2, line(chapman, 1))
	bola.must(403, "PUT", "/v1/me/baskets/"+b2+"/holder", map[string]any{"person_id": bolaID})
	ada.must(202, "POST", "/v1/me/delete/start", map[string]any{"via": "sms"})
	ada.must(200, "POST", "/v1/me/delete", map[string]any{"confirm": true, "code": w.sms.code(t, "+2348031161001")})
	v = bola.must(200, "GET", "/v1/me/baskets/"+b2, nil)
	if basketPerson(v, adaID) != nil || v["holder_gone"] != true {
		t.Fatalf("after Ada's account went: %v", v)
	}
	if v = bola.must(200, "PUT", "/v1/me/baskets/"+b2+"/holder", map[string]any{"person_id": bolaID}); v["you_hold"] != true {
		t.Fatalf("taken over: %v", v)
	}
	// The placed basket keeps Ada's lines as the order's record.
	if v = bola.must(200, "GET", "/v1/me/baskets/"+id, nil); basketPerson(v, adaID)["name"] != "Deleted account" {
		t.Fatalf("a placed basket after Ada's account went: %v", v)
	}
}

// Row-level security does the seeing (00486): an outsider's transaction
// finds no basket, line or share, nor writes a line into one; a member's
// finds them; a business's door open finds nothing. The live connection
// carries a basket to its members and to no one else.
func TestBasketsIsolationAndLive(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348031162001", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031162002", "Bola Ade")
	outsider, outsiderID := w.many(t, "+2348031162003", "Out Sider")
	adds(t, ada, bola)
	adds(t, bola, ada)
	gid, _ := newGroup(t, ada, "Live lunch", bolaID)
	bEvents, _, closeB := liveStream(t, bola, "")
	defer closeB()
	oEvents, _, closeO := liveStream(t, outsider, "")
	defer closeO()

	id := ada.must(201, "POST", "/v1/me/baskets", map[string]any{"business_id": w.biz, "group_id": gid})["basket"].(map[string]any)["id"].(string)
	jollof := shopIDs(t, ada, id)["Jollof rice"]
	mine(t, ada, id, line(jollof, 3))
	got := await(bEvents, 8*time.Second, func(ev streamEvent) bool {
		b, _ := ev.Data["basket"].(map[string]any)
		return ev.Event == "basket" && b["id"] == id && b["items_kobo"] == float64(1350000)
	})
	if got[0] == nil {
		t.Fatal("Bola's connection did not carry the basket")
	}
	for _, ev := range drain(oEvents, time.Second) {
		if ev.Event == "basket" {
			t.Fatalf("the outsider got %v", ev)
		}
	}

	count := func(person uuid.UUID) (n int) {
		t.Helper()
		if err := db.InPersonTx(ctx, w.env.DB.App, person, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM people_baskets WHERE id = $1) + (SELECT count(*) FROM people_basket_lines WHERE basket_id = $1)`, id).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(bolaID) != 2 || count(outsiderID) != 0 {
		t.Fatalf("by row-level security: bola %d, outsider %d", count(bolaID), count(outsiderID))
	}
	err := db.InPersonTx(ctx, w.env.DB.App, outsiderID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO people_basket_lines (id, basket_id, person_id, item_id, quantity, name, price_kobo) VALUES (gen_random_uuid(), $1, $2, 'x', 1, 'x', 1)`, id, outsiderID)
		return err
	})
	if err == nil {
		t.Fatal("an outsider wrote a line by SQL")
	}
	// A business's door open: nothing of the basket.
	if err := db.InPersonTx(ctx, w.env.DB.App, bolaID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT taskiem_basket_enter($1, 'shop')`, id); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM people_baskets`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("baskets with the door open: %d %v", n, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	outsider.must(404, "GET", "/v1/me/baskets/"+id+"/shop", nil)
}

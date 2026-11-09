package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Staff use chat as their app (docs/spec/chat-tools.md §7), and the owner
// gets the 8am card. A cashier works a shift from chat alone: sells on
// WhatsApp and with the Sell tool, is told of a new order and accepts it,
// sees their tools and no more; a shift manager hands the shift over, and
// the owner is told; the owner's briefing is ready at 8am Lagos, and "how
// did we do this week?" is answered with a chart.
func TestCashierShift(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, _ := linkedPair(t, w.waWorld)
	const ownerOn, cashOn = "+2348010000911", "+2348010000912"
	w.bind(t, seller, ownerOn)
	w.say(t, ownerOn, "links")
	seller.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	seller.must(200, "POST", "/v1/stock/setup", nil)
	rice := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 40, "unit": "bag", "price_kobo": 7800000, "reorder_level": 10},
		"Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	cashier := addMember(t, w.world, seller, "cash", "cashier")
	w.bind(t, cashier, cashOn)

	// A sale on WhatsApp, and one from the Sell tool, each a diary line.
	if r := w.say(t, cashOn, "sold 2 bags of rice 156,000 cash"); !strings.Contains(r, "Sold 2 bags Rice for ₦156,000, in cash?") {
		t.Fatalf("a sale on WhatsApp: %q", r)
	}
	if r := w.yes(t, cashOn); !strings.Contains(r, "Recorded S-") {
		t.Fatalf("recorded: %q", r)
	}
	cashier.must(201, "POST", "/v1/stock/sales", map[string]any{"lines": []any{map[string]any{"item_id": rice, "quantity": 1}}, "paid_how": "pos"},
		"Idempotency-Key", "sell-1")
	if d := diaryOf(t, cashier); !hasLine(d, "Sold 1 Rice: ₦78,000 (POS)") {
		t.Fatalf("the sale's diary: %q", d)
	}
	// The same sale again (a retry): recorded once, one diary line.
	cashier.must(201, "POST", "/v1/stock/sales", map[string]any{"lines": []any{map[string]any{"item_id": rice, "quantity": 1}}, "paid_how": "pos"},
		"Idempotency-Key", "sell-1")
	var sold int
	for _, l := range diaryOf(t, cashier) {
		if strings.Contains(l, "Sold 1 Rice: ₦78,000 (POS)") {
			sold++
		}
	}
	if sold != 1 {
		t.Fatalf("a retried sale wrote %d diary lines", sold)
	}
	// Their tools, and not the storefront's.
	if r := w.say(t, cashOn, "tools"); !strings.Contains(r, "Sell") || !strings.Contains(r, "Orders") || !strings.Contains(r, "Stock") ||
		strings.Contains(r, "Price lists") || strings.Contains(r, "Handover") {
		t.Fatalf("a cashier's tools: %q", r)
	}
	cashier.must(403, "GET", "/v1/stock/suppliers", nil)
	cashier.must(403, "GET", "/v1/price-lists", nil)
	cashier.must(403, "POST", "/v1/storefront/orders/X-1/refunded", nil)
	cashier.must(403, "POST", "/v1/chat-handover", map[string]any{"cash_counted_kobo": 0})
	cashier.must(403, "GET", "/v1/chat-briefing", nil)

	// A linked business orders: the cashier is told, sees the card, and
	// accepts it.
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	bRice := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 1, "unit": "bag"}, "Idempotency-Key", "br")["item"].(map[string]any)["id"].(string)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote", "phone": "0803 123 4991"})["supplier"].(map[string]any)["id"].(string)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})
	po := buyer.must(201, "POST", "/v1/stock/purchases", map[string]any{"supplier_id": sup, "lines": []any{map[string]any{"item_id": bRice, "quantity": 5}}},
		"Idempotency-Key", "po")["purchase"].(map[string]any)["id"].(string)
	before := len(w.graph.Sent(cashOn))
	ref := buyer.must(200, "POST", "/v1/stock/purchases/"+po+"/send", nil)["purchase"].(map[string]any)["reference"].(string)
	drainLinked(t, w.world)
	if told := w.lastTo(t, cashOn, before); len(told) == 0 || !strings.Contains(told[0].Text, ref) {
		t.Fatalf("the cashier told of the order: %+v", told)
	}
	var seen bool
	for _, x := range cashier.must(200, "GET", "/v1/chat-alerts", nil)["alerts"].([]any) {
		seen = seen || x.(map[string]any)["kind"] == "new_order" && x.(map[string]any)["reference"] == ref
	}
	if !seen {
		t.Fatal("no new order card for the cashier")
	}
	if r := w.say(t, cashOn, "orders"); strings.Contains(r, "not allowed") || strings.Contains(r, "can't") {
		t.Fatalf("the cashier's orders: %q", r)
	}
	// A cashier works the order's steps but does not change it.
	cashier.must(403, "POST", "/v1/trade-orders/"+ref+"/change", map[string]any{"lines": []any{}})
	cashier.must(200, "POST", "/v1/trade-orders/"+ref+"/accept", nil)
	if d := diaryOf(t, cashier); !hasLine(d, "Accepted order "+ref+" from Ada's Kitchen") {
		t.Fatalf("the accept's diary: %q", d)
	}

	// The shift handed over: the owner told, with the cash counted.
	manager := addMember(t, w.world, seller, "sam", "shift_manager")
	const samOn = "+2348010000913"
	w.bind(t, manager, samOn)
	h := manager.must(200, "GET", "/v1/chat-handover", nil)
	sales := h["sales"].(map[string]any)
	if sales["total_kobo"] != float64(23400000) || sales["by_how"].(map[string]any)["cash"] != float64(15600000) || h["orders_open"] != float64(1) {
		t.Fatalf("the shift so far: %v", h)
	}
	before = len(w.graph.Sent(ownerOn))
	samBefore := len(w.graph.Sent(samOn))
	out := manager.must(200, "POST", "/v1/chat-handover", map[string]any{"cash_counted_kobo": 15000000, "note": "Till drawer sticks"})
	words := out["words"].(string)
	if !strings.Contains(words, "Sold ₦234,000 in 2 sales") || !strings.Contains(words, "Cash counted: ₦150,000, ₦6,000 short of the ₦156,000 sold") ||
		!strings.Contains(words, "Note: Till drawer sticks") {
		t.Fatalf("the handover: %q", words)
	}
	w.srv.WaitBackground()
	if told := w.lastTo(t, ownerOn, before); len(told) == 0 || !strings.Contains(told[0].Text, "sam handed the shift over") {
		t.Fatalf("the owner told: %+v", told)
	}
	if told := w.lastTo(t, samOn, samBefore); len(told) != 0 {
		t.Fatalf("the one handing over was told of their own handover: %+v", told)
	}
	manager.must(400, "POST", "/v1/chat-handover", map[string]any{"cash_counted_kobo": -1})

	// The owner's briefing: from 8am Lagos, not for a cashier.
	lagos, _ := time.LoadLocation("Africa/Lagos")
	at := func(hour int) func() time.Time {
		return func() time.Time {
			n := time.Now().In(lagos)
			return time.Date(n.Year(), n.Month(), n.Day(), hour, 5, 0, 0, lagos)
		}
	}
	w.srv.BriefingClock = at(7)
	if b := seller.must(200, "GET", "/v1/chat-briefing", nil); b["ready"] != false {
		t.Fatalf("before 8am: %v", b)
	}
	w.srv.BriefingClock = at(9)
	b := seller.must(200, "GET", "/v1/chat-briefing", nil)
	if b["ready"] != true || b["put_off"] != false || len(b["sales"].(map[string]any)["week"].([]any)) != 7 || b["owed_kobo"] == nil || b["low"] != float64(0) {
		t.Fatalf("the briefing: %v", b)
	}
	cashier.must(403, "GET", "/v1/chat-briefing", nil)
	seller.must(200, "POST", "/v1/chat-alerts/later", map[string]any{"key": b["key"], "hours": 24})
	if b := seller.must(200, "GET", "/v1/chat-briefing", nil); b["put_off"] != true {
		t.Fatalf("Got it: %v", b)
	}
	// The greeting follows the hour, and the time is the time it was read.
	if b["part"] != "morning" || b["now"] != "09:05" {
		t.Fatalf("the briefing's hour: %v", b)
	}
	w.srv.BriefingClock = at(15)
	if b := seller.must(200, "GET", "/v1/chat-briefing", nil); b["part"] != "afternoon" || b["now"] != "15:05" || b["put_off"] != true {
		t.Fatalf("in the afternoon: %v", b)
	}
	// Asked for again (the tools menu, or "briefing"): back, put away or not,
	// and at any hour.
	if b := seller.must(200, "GET", "/v1/chat-briefing?show=1", nil); b["ready"] != true || b["put_off"] != false || b["part"] != "afternoon" {
		t.Fatalf("shown again: %v", b)
	}
	if b := seller.must(200, "GET", "/v1/chat-briefing", nil); b["put_off"] != false {
		t.Fatalf("after showing it again: %v", b)
	}
	w.srv.BriefingClock = at(6)
	if b := seller.must(200, "GET", "/v1/chat-briefing?show=1", nil); b["ready"] != true {
		t.Fatalf("asked for before 8am: %v", b)
	}
	// Put away today, it is back tomorrow from 8am.
	w.srv.BriefingClock = at(9)
	seller.must(200, "POST", "/v1/chat-alerts/later", map[string]any{"key": b["key"], "hours": 24})
	w.srv.BriefingClock = func() time.Time { return at(9)().Add(24 * time.Hour) }
	if b := seller.must(200, "GET", "/v1/chat-briefing", nil); b["ready"] != true || b["put_off"] != false {
		t.Fatalf("the next day: %v", b)
	}
	w.srv.BriefingClock = at(9)

	// An invoice sent is a sale; one still a draft is not.
	inv := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Bisi Okafor", "phone": "+2348030000555"}, "amount_kobo": 4250000}, "Idempotency-Key", "inv-1")
	seller.must(200, "POST", "/v1/invoices/"+inv["id"].(string)+"/send", nil, "Idempotency-Key", "inv-1s")
	seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Bisi Okafor", "phone": "+2348030000555"}, "amount_kobo": 100000}, "Idempotency-Key", "inv-2")

	// "How did we do this week?": the words, and a chart card. Sales are the
	// counter's ₦234,000, the trade order accepted above (₦390,000) and the
	// invoice sent (₦42,500); the trade order's own invoice is not counted
	// again.
	said := seller.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "how did we do this week?"})["messages"].([]any)
	var reply string
	for _, x := range said {
		if m := x.(map[string]any); m["inbound"] == false {
			reply += m["text"].(string)
		}
	}
	if !strings.Contains(reply, "Sales, the last 7 days: ₦666,500") {
		t.Fatalf("how did we do: %q", reply)
	}
	var chart map[string]any
	for _, x := range seller.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any) {
		if m := x.(map[string]any); m["kind"] == "chart" {
			_ = json.Unmarshal([]byte(m["text"].(string)), &chart)
		}
	}
	if chart == nil || chart["total_kobo"] != float64(66650000) || len(chart["bars"].([]any)) != 7 || chart["highlight"] != float64(-1) {
		t.Fatalf("the chart: %v", chart)
	}
	if r := w.say(t, ownerOn, "how did we do today"); !strings.Contains(r, "Sales today: ₦666,500") {
		t.Fatalf("on WhatsApp: %q", r)
	}
}

// A rider works a shift from chat alone: asks what they have, takes it,
// picks it up and delivers it, the next word to send told each time.
func TestRiderShift(t *testing.T) {
	w, ada := newDispatchWorld(t, "Ada Stores")
	path := strings.TrimPrefix(ada.must(200, "GET", "/v1/riders", nil)["tap_in_url"].(string), "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
	const musa = "+2348031110081"
	w.whatsapp(t, musa, loc.Query().Get("text"))
	ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": musa, "zones": []string{"Lekki"}, "fee_kobo": 100000})
	ref := w.order(t, "none")
	ada.must(200, "POST", "/v1/storefront/orders/"+ref+"/offer", map[string]any{})
	w.tick(t)
	jobs := func(want string) {
		t.Helper()
		w.whatsapp(t, musa, "my deliveries")
		if m := w.graph.Last(t, musa); !strings.Contains(m.Text, "Your deliveries now:") || !strings.Contains(m.Text, ref) || !strings.Contains(m.Text, want) {
			t.Fatalf("my deliveries (%s): %q", want, m.Text)
		}
	}
	jobs("Send ACCEPT or CAN'T")
	w.whatsapp(t, musa, "accept")
	jobs("Send PICKED UP")
	if m := w.graph.Last(t, musa); !strings.Contains(m.Text, "12 Admiralty Way") {
		t.Fatalf("the address once it is theirs: %q", m.Text)
	}
	w.whatsapp(t, musa, "picked up")
	jobs("Send ARRIVED at the door, or DELIVERED")
	w.whatsapp(t, musa, "delivered")
	w.tick(t)
	// Done is not open: no longer among their deliveries.
	w.whatsapp(t, musa, "my deliveries")
	if m := w.graph.Last(t, musa); strings.Contains(m.Text, "Your deliveries now:") {
		t.Fatalf("a delivered one still listed: %q", m.Text)
	}
	for _, x := range ada.must(200, "GET", "/v1/deliveries", nil)["deliveries"].([]any) {
		if d := x.(map[string]any); d["reference"] == ref && d["status"] != "delivered" {
			t.Fatalf("delivered from chat: %v", d)
		}
	}
}

// A cashier sees the riders without their numbers, fees or banks, and
// offers a delivery without setting a fee.
func TestCashierSeesRidersMasked(t *testing.T) {
	w, ada := newDispatchWorld(t, "Ada Stores")
	ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": "+2348031110082", "zones": []string{"Lekki"}, "fee_kobo": 100000})
	cashier := addMember(t, w.world, ada, "cash", "cashier")
	list := cashier.must(200, "GET", "/v1/riders", nil)
	r := list["riders"].([]any)[0].(map[string]any)
	if strings.Contains(toJSON(r), "31110082") || !strings.Contains(toJSON(r), "0082") || (r["fee_kobo"] != nil && r["fee_kobo"] != float64(0)) || list["tap_in_url"] != nil && list["tap_in_url"] != "" {
		t.Fatalf("a cashier's riders: %v", list)
	}
	ref := w.order(t, "none")
	cashier.must(403, "POST", "/v1/storefront/orders/"+ref+"/offer", map[string]any{"fee_kobo": 1})
	cashier.must(200, "POST", "/v1/storefront/orders/"+ref+"/offer", map[string]any{})
}

// A business's own role named like a new built-in keeps its own
// permissions.
func TestOwnRoleNamedCashierWins(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Ada Stores", "ada@ada.test")
	// Made before cashier was built in (a new one of that name is refused).
	if _, err := w.env.DB.Admin.Exec(context.Background(), `INSERT INTO roles (tenant_id, name, description, permissions, created_by) SELECT $1, 'cashier', 'Ours', ARRAY['table.read'], user_id FROM memberships WHERE tenant_id = $1 LIMIT 1`,
		tenantOf(t, owner)); err != nil {
		t.Fatal(err)
	}
	c := addMember(t, w, owner, "cash", "cashier")
	c.must(403, "GET", "/v1/trade-orders", nil)
	c.must(403, "GET", "/v1/storefront/orders", nil)
	var n int
	for _, x := range owner.must(200, "GET", "/v1/roles", nil)["roles"].([]any) {
		if x.(map[string]any)["name"] == "cashier" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("cashier listed %d times", n)
	}
}

// Anyone with order.handle decides a customer order's own steps (accept or
// decline, done), as the owner would; not another workflow's approvals.
func TestCashierDecidesCustomerOrders(t *testing.T) {
	w := newWAWorld(t)
	ctx := context.Background()
	key, _ := storefront.InboxKey(bytes.Repeat([]byte{3}, 32))
	w.srv.PublicHosts = []string{shopDomain}
	w.srv.Orders = &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault, InboxKey: key, PreviewDomain: shopDomain,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)},
		Start:    w.env.Store.Start, Signal: w.env.Store.DeliverSignalOnce, Cancel: w.env.Store.CancelRun}
	if err := w.env.Registry.Register(sfconn.New(w.srv.Orders)); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	verifyPhone(t, w.world, owner, "phone", "business")
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": map[string]any{"name": "Ada's Kitchen"}})
	wf := owner.must(201, "POST", "/v1/templates/storefront-order/instantiate", map[string]any{"params": map[string]any{}, "name": "Orders"})["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	owner.must(200, "PUT", "/v1/storefront/actions/order", map[string]any{"enabled": true, "payment": "none"})
	sfID := uuid.MustParse(owner.must(200, "POST", "/v1/storefront/publish", nil)["id"].(string))
	cashier := addMember(t, w.world, owner, "cash", "cashier")
	viewer := addMember(t, w.world, owner, "vic", "viewer")

	ref := storefront.NewReference("ada-kitchen")
	_, hash := storefront.NewStatusKey()
	ct, _ := storefront.SealSubmission(key, sfID, ref, storefront.Submission{Kind: "order", Payment: "none", Channel: "direct", Submitted: time.Now(),
		Basket: []storefront.Line{{ID: "1", Name: "Jollof rice", Quantity: 1, PriceKobo: 150000, LineKobo: 150000}}, TotalKobo: 150000, Fulfilment: "pickup",
		Customer: storefront.Customer{Name: "Bola Ade", Phone: "+2348031234567"}})
	var got *string
	if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT taskiem_public_submit($1, 'order', $2, $3, $4, $5)`,
		sfID, ref, bytes.Repeat([]byte{7}, 32), hash, ct).Scan(&got); err != nil || got == nil {
		t.Fatalf("submit: %v %v", got, err)
	}
	if err := w.srv.Orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	var run string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT run_id::text FROM storefront_orders WHERE reference = $1`, ref).Scan(&run); err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, a := range cashier.must(200, "GET", "/v1/approvals", nil)["approvals"].([]any) {
		seen = seen || a.(map[string]any)["run_id"] == run
	}
	if !seen {
		t.Fatal("the order's approval is not in the cashier's inbox")
	}
	viewer.must(403, "POST", "/v1/approvals/"+run+"/decide", map[string]any{"decision": "approved"})
	if res := cashier.must(200, "POST", "/v1/approvals/"+run+"/decide", map[string]any{"decision": "approved"}); res["status"] != "approved" {
		t.Fatalf("the cashier accepts: %v", res)
	}
	w.env.Drain(t)
	var status string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM storefront_orders WHERE reference = $1`, ref).Scan(&status); err != nil || (status != "accepted" && status != "paid") {
		t.Fatalf("after the cashier accepts: %s %v", status, err)
	}
}

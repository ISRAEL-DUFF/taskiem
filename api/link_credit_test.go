package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/moneydigest"
	"github.com/israel-duff/taskiem/engine/moneyreport"
	"github.com/israel-duff/taskiem/engine/sender"
)

// Bills due reach a business that only buys through links (§10; the
// review): it has no money entries and no sent invoices, so the morning
// summary used to pass it by; now it is looked at, and a quiet day with a
// bill due still gets the summary, with the bills due in it.
func TestBillsDueReachBuyerOnly(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.AccountRails = &linkRails{}
	seller, buyer, _, buyerID := linkedPair(t, w)
	const buyerOn = "+2348010000371"
	w.bind(t, buyer, buyerOn)
	w.say(t, buyerOn, "help") // the conversation window is open
	x := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Ada's Kitchen", "key": "link:" + buyerID},
		"amount_kobo": 1000000}, "Idempotency-Key", "b-1")
	seller.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "s-1")
	if _, err := w.srv.LinkMirrorTick(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE bills SET due_on = current_date - 1 WHERE tenant_id = $1`, buyerID); err != nil {
		t.Fatal(err)
	}
	var money int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM money_entries WHERE tenant_id = $1`, buyerID).Scan(&money); err != nil || money != 0 {
		t.Fatalf("a buyer with money of its own: %d %v", money, err)
	}
	today, _ := moneyreport.Day(time.Now())
	tomorrow := today.AddDate(0, 0, 1)
	d := &moneydigest.Digester{Pool: w.env.DB.App, Channels: []string{"whatsapp"}, PublicURL: "https://taskiem.test",
		Now: func() time.Time {
			return time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 8, 5, 0, 0, moneyreport.Lagos)
		}}
	for range 2 {
		if _, err := d.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		w.tick(t)
	}
	var got []string
	for _, s := range w.graph.Sent(buyerOn) {
		if strings.Contains(s.Text, "Yesterday: ") {
			got = append(got, s.Text)
		}
	}
	if len(got) != 1 || !strings.Contains(got[0], "Bills due: 1, ₦10,000, 1 overdue") {
		t.Fatalf("the buyer's summary: %q", got)
	}
}

// Statements, credit terms and limits, and reminders between linked
// businesses (L5, docs/spec/business-links.md §10): the seller's terms set
// an invoice's due date; an order that takes the buyer past its limit
// reaches the seller flagged, not accepted by itself, and the buyer is
// told why; the seller may accept it anyway; paid down (here, the limit
// raised), it is accepted by itself; the statement on each side adds up
// to the same number; a reminder reaches the buyer as a notice on the
// bill, not a text message.
func TestLinkCredit(t *testing.T) {
	ctx := context.Background()
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, sellerID, buyerID := linkedPair(t, w.waWorld)
	const buyerOn = "+2348010000361"
	w.bind(t, buyer, buyerOn)
	w.say(t, buyerOn, "links") // the window is open: notices reach the buyer's chat
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	// The buyer's cost is the seller's price: an order at another price is
	// the seller's change, not accepted by itself (§16.3).
	rice := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 3, "unit": "bag", "cost_kobo": 4500000},
		"Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	seller.must(200, "POST", "/v1/stock/setup", nil)
	seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "rice", "quantity": 500, "unit": "bag", "price_kobo": 4500000}, "Idempotency-Key", "sr")
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote", "phone": "0803 123 4701"})["supplier"].(map[string]any)["id"].(string)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})

	// The seller's terms: 14 days, ₦500,000, accepted by themselves.
	seller.must(400, "PUT", "/v1/links/"+sellerLink+"/trade", map[string]any{"terms_days": 400})
	tr := seller.must(200, "PUT", "/v1/links/"+sellerLink+"/trade", map[string]any{"terms_days": 14, "credit_limit_kobo": 50000000, "auto_accept": true})
	if tr["terms_days"] != float64(14) || tr["credit_limit_kobo"] != float64(50000000) {
		t.Fatalf("terms: %v", tr)
	}
	if st := buyer.must(200, "GET", "/v1/links/"+link+"/statement", nil); st["we_owe"].(map[string]any)["credit_limit_kobo"] != float64(50000000) ||
		st["we_owe"].(map[string]any)["terms_days"] != float64(14) {
		t.Fatalf("the buyer sees its terms: %v", st)
	}
	order := func(key string, n int) string {
		t.Helper()
		id := buyer.must(201, "POST", "/v1/stock/purchases", map[string]any{"supplier_id": sup, "lines": []any{map[string]any{"item_id": rice, "quantity": n}}},
			"Idempotency-Key", key)["purchase"].(map[string]any)["id"].(string)
		ref := buyer.must(200, "POST", "/v1/stock/purchases/"+id+"/send", nil)["purchase"].(map[string]any)["reference"].(string)
		drainLinked(t, w.world)
		return ref
	}
	trade := func(ref string) map[string]any {
		t.Helper()
		return seller.must(200, "GET", "/v1/trade-orders/"+ref, nil)["order"].(map[string]any)
	}

	// Within the limit: accepted by itself (₦450,000 owed now).
	first := order("p1", 10)
	if o := trade(first); o["status"] != "accepted" || o["over_limit_at"] != nil {
		t.Fatalf("within the limit: %v", o)
	}
	// Past it: flagged, waiting, and the buyer told why.
	second := order("p2", 5)
	o := trade(second)
	if o["status"] != "received" || o["over_limit_at"] == nil {
		t.Fatalf("over the limit: %v", o)
	}
	w.srv.WaitBackground()
	var waiting map[string]any
	for _, x := range buyer.must(200, "GET", "/v1/stock/purchases", nil)["purchases"].([]any) {
		if x.(map[string]any)["reference"] == second {
			waiting = x.(map[string]any)
		}
	}
	if !strings.Contains(waiting["seller_note"].(string), "takes you past the ₦500,000 credit") {
		t.Fatalf("the buyer's order says why: %v", waiting)
	}
	told := false
	for _, n := range buyer.must(200, "GET", "/v1/notifications", nil)["notifications"].([]any) {
		told = told || strings.Contains(n.(map[string]any)["title"].(string), "over your credit limit")
	}
	if !told {
		t.Error("the buyer's bell")
	}
	st := seller.must(200, "GET", "/v1/links/"+sellerLink+"/statement", nil)["they_owe"].(map[string]any)
	if st["available_kobo"] != float64(50000000-45000000) {
		t.Errorf("available: %v", st)
	}
	// The buyer's statement says the same: its accepted order, not yet
	// invoiced, counts against the limit as the seller counts it.
	if mine := buyer.must(200, "GET", "/v1/links/"+link+"/statement", nil)["we_owe"].(map[string]any); mine["available_kobo"] != st["available_kobo"] {
		t.Errorf("available on both sides: %v / %v", mine["available_kobo"], st["available_kobo"])
	}

	// Raised, it is checked again and accepted by itself.
	seller.must(200, "PUT", "/v1/links/"+sellerLink+"/trade", map[string]any{"credit_limit_kobo": 100000000})
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE link_orders_due SET due_at = now() WHERE kind = 'credit'`); err != nil {
		t.Fatal(err)
	}
	drainLinked(t, w.world)
	if o := trade(second); o["status"] != "accepted" || o["over_limit_at"] != nil {
		t.Fatalf("within the limit again: %v", o)
	}
	// Past it again; the seller accepts it anyway.
	seller.must(200, "PUT", "/v1/links/"+sellerLink+"/trade", map[string]any{"credit_limit_kobo": 70000000})
	third := order("p3", 2)
	if o := trade(third); o["over_limit_at"] == nil {
		t.Fatalf("over again: %v", o)
	}
	if o := seller.must(200, "POST", "/v1/trade-orders/"+third+"/accept", map[string]any{})["order"].(map[string]any); o["status"] != "accepted" || o["over_limit_at"] != nil {
		t.Fatalf("accepted anyway: %v", o)
	}

	// The terms set an invoice's due date; the statement adds up both sides.
	x := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Ada's Kitchen", "key": "link:" + buyerID},
		"lines": []map[string]any{{"description": "Sacks", "quantity": 1, "unit_kobo": 1000000}}}, "Idempotency-Key", "i-1")
	want := time.Now().In(lagosTZ(t)).AddDate(0, 0, 14).Format(time.DateOnly)
	if x["due_on"] != want {
		t.Fatalf("due in 14 days: %v (want %s)", x["due_on"], want)
	}
	seller.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "s-1")
	if _, err := w.srv.LinkMirrorTick(ctx); err != nil {
		t.Fatal(err)
	}
	theirs := seller.must(200, "GET", "/v1/links/"+sellerLink+"/statement", nil)["they_owe"].(map[string]any)
	ours := buyer.must(200, "GET", "/v1/links/"+link+"/statement", nil)["we_owe"].(map[string]any)
	if theirs["owed_kobo"] != ours["owed_kobo"] || theirs["owed_kobo"] != float64(1000000) || len(ours["documents"].([]any)) != 1 ||
		ours["documents"].([]any)[0].(map[string]any)["reference"] != x["number"] {
		t.Fatalf("one number both sides: %v / %v", theirs, ours)
	}
	// What is left to use too: the limit less the invoice and the three
	// accepted orders not yet invoiced (₦450,000 + ₦225,000 + ₦90,000).
	if theirs["available_kobo"] != ours["available_kobo"] || theirs["available_kobo"] != float64(70000000-1000000-76500000) {
		t.Fatalf("what is left on both sides: %v / %v", theirs["available_kobo"], ours["available_kobo"])
	}

	// The morning summary: due in 14 days is not due yet; overdue is.
	if line := w.srv.BillsDueLine(ctx, uuid.MustParse(buyerID)); line != "" {
		t.Errorf("not due yet: %q", line)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE bills SET due_on = current_date - 2 WHERE tenant_id = $1`, buyerID); err != nil {
		t.Fatal(err)
	}
	if line := w.srv.BillsDueLine(ctx, uuid.MustParse(buyerID)); line != "Bills due: 1, ₦10,000, 1 overdue. Send *bills due* to see them." {
		t.Errorf("bills due: %q", line)
	}

	// A reminder: a notice on the bill, in the buyer's own business.
	out, err := w.srv.Invoices.NotifyCustomer(ctx, uuid.MustParse(sellerID), x["number"].(string),
		"A reminder that invoice {number} is overdue: {due} is still due. See it and pay: {link}", "", "rem-1")
	if err != nil || out["queued"].([]string)[0] != "link" {
		t.Fatalf("reminded: %v %v", out, err)
	}
	w.srv.WaitBackground()
	reminded := false
	for _, m := range buyer.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any) {
		if c, ok := m.(map[string]any)["card"].(map[string]any); ok && c["event"] == "reminder" && c["reference"] == x["number"] {
			reminded = true
		}
	}
	if !reminded {
		t.Error("a reminder card on the bill")
	}
	bell := false
	for _, n := range buyer.must(200, "GET", "/v1/notifications", nil)["notifications"].([]any) {
		bell = bell || strings.Contains(n.(map[string]any)["title"].(string), "a reminder about "+x["number"].(string))
	}
	if !bell {
		t.Error("the reminder in the bell")
	}
	var sms int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM platform_messages WHERE tenant_id = $1 AND dedup_key LIKE 'notice:rem-1%'`, sellerID).Scan(&sms); err != nil || sms != 0 {
		t.Errorf("no text message to a linked business: %d", sms)
	}
	// The reminder in chat carries Pay (§10).
	payButton := false
	for _, m := range w.waWorld.graph.Sent(buyerOn) {
		if strings.Contains(m.Text, "a reminder about "+x["number"].(string)) {
			for _, b := range m.Buttons {
				payButton = payButton || (b.Title == "Pay" && b.ID == "lk:pay:"+x["number"].(string))
			}
		}
	}
	if !payButton {
		t.Error("the reminder in chat with Pay")
	}

	// A queried bill is not reminded, and the overdue lists leave its
	// invoice out, until the query is settled (§8).
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_invoices SET due_on = current_date - 2 WHERE tenant_id = $1`, sellerID); err != nil {
		t.Fatal(err)
	}
	svc := invoicing.Service{Invoices: w.srv.Invoices}
	if got, err := svc.ListCustomersOverdue(ctx, uuid.MustParse(sellerID), 0); err != nil || len(got) != 1 {
		t.Fatalf("overdue before the query: %v %+v", err, got)
	}
	buyer.must(200, "POST", "/v1/bills/"+x["number"].(string)+"/query", map[string]any{"reason": "We got 4 cartons, not 5"})
	if _, err := w.srv.LinkMirrorTick(ctx); err != nil {
		t.Fatal(err)
	}
	out, err = w.srv.Invoices.NotifyCustomer(ctx, uuid.MustParse(sellerID), x["number"].(string), "Still overdue: {number}", "", "rem-2")
	if err != nil || len(out["queued"].([]string)) != 0 || out["reason"] != "disputed" {
		t.Fatalf("a queried bill reminded: %v %v", out, err)
	}
	if got, err := svc.ListCustomersOverdue(ctx, uuid.MustParse(sellerID), 0); err != nil || len(got) != 0 {
		t.Errorf("the queried invoice in the overdue customers: %v %+v", err, got)
	}
	if got, err := svc.ListFor(ctx, uuid.MustParse(sellerID), "overdue", 0, 0); err != nil || len(got) != 0 {
		t.Errorf("the queried invoice in the overdue list: %v %+v", err, got)
	}
	seller.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/settle-query", map[string]any{"note": "As it stands"})
	if got, err := svc.ListCustomersOverdue(ctx, uuid.MustParse(sellerID), 0); err != nil || len(got) != 1 {
		t.Errorf("settled, listed again: %v %+v", err, got)
	}
}

func lagosTZ(t *testing.T) *time.Location {
	t.Helper()
	l, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

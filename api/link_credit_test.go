package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

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
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	rice := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 3, "unit": "bag", "cost_kobo": 4000000},
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
}

func lagosTZ(t *testing.T) *time.Location {
	t.Helper()
	l, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

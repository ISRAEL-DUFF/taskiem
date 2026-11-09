package api_test

import (
	"context"
	"strings"
	"testing"
)

// The chat's Invoices and Money tools (docs/spec/chat-five-more-flows.md,
// phase 2 additions): an invoice made, sent, paid in cash and cancelled
// from the chat, the money log listed with what each entry counts towards,
// money matched to an invoice by hand, and each change told as a diary line.
func TestChatInvoicesAndMoney(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withLinkedOrders(w.world)
	seller, _, _, _ := linkedPair(t, w.waWorld)
	seller.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	cust := map[string]any{"name": "Bisi Okafor", "phone": "+2348030000556"}

	// A new invoice, sent.
	a := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": cust, "description": "Catering for 50", "amount_kobo": 4500000}, "Idempotency-Key", "m-a")
	seller.must(200, "POST", "/v1/invoices/"+a["id"].(string)+"/send", nil, "Idempotency-Key", "m-as")
	num := a["number"].(string)

	// Paid in cash: the entry, then matched to the invoice.
	e := seller.must(201, "POST", "/v1/money/entries", map[string]any{"kind": "cash", "direction": "in", "amount_kobo": 2000000, "description": "Payment for " + num, "channel": "cash"}, "Idempotency-Key", "m-pay")
	entry := e["entry_id"].(string)
	seller.must(200, "POST", "/v1/money/entries/"+entry+"/match", map[string]any{"invoice": num})
	inv := seller.must(200, "GET", "/v1/invoices/"+a["id"].(string), nil)["invoice"].(map[string]any)
	if inv["status"] != "part_paid" || inv["due_kobo"] != float64(2500000) {
		t.Fatalf("paid in part: %v", inv)
	}

	// An expense, and cash left to match.
	seller.must(201, "POST", "/v1/money/entries", map[string]any{"kind": "expense", "amount_kobo": 300000, "description": "Diesel", "vendor": "Total"}, "Idempotency-Key", "m-ex")
	loose := seller.must(201, "POST", "/v1/money/entries", map[string]any{"kind": "cash", "direction": "in", "amount_kobo": 2500000, "description": "Cash from Bisi"}, "Idempotency-Key", "m-loose")["entry_id"].(string)

	list := seller.must(200, "GET", "/v1/money/entries", nil)["entries"].([]any)
	if len(list) != 3 {
		t.Fatalf("the money log: %v", list)
	}
	byID := map[string]map[string]any{}
	for _, x := range list {
		m := x.(map[string]any)
		byID[m["id"].(string)] = m
	}
	if p := byID[entry]; p["towards"] != "invoice "+num || p["direction"] != "in" || p["amount_kobo"] != float64(2000000) || p["left_kobo"] != float64(0) {
		t.Fatalf("the payment: %v", p)
	}
	if first := list[0].(map[string]any); first["id"] != loose {
		t.Fatalf("newest first: %v", list)
	}
	for _, x := range list {
		if m := x.(map[string]any); m["kind"] == "expense" && (m["direction"] != "out" || m["vendor"] != "Total" || m["left_kobo"] != nil) {
			t.Fatalf("the expense: %v", m)
		}
	}
	// Paging: before the newest, the rest.
	if older := seller.must(200, "GET", "/v1/money/entries?limit=1&before="+list[0].(map[string]any)["when"].(string), nil)["entries"].([]any); len(older) != 1 {
		t.Fatalf("a page: %v", older)
	}
	seller.must(400, "GET", "/v1/money/entries?before=yesterday", nil)

	// The loose cash matched by hand: the invoice paid.
	seller.must(200, "POST", "/v1/money/entries/"+loose+"/match", map[string]any{"invoice": num})
	if inv := seller.must(200, "GET", "/v1/invoices/"+a["id"].(string), nil)["invoice"].(map[string]any); inv["status"] != "paid" {
		t.Fatalf("paid: %v", inv)
	}

	// Another invoice, cancelled.
	b := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": cust, "description": "Chairs", "amount_kobo": 100000}, "Idempotency-Key", "m-b")
	seller.must(200, "POST", "/v1/invoices/"+b["id"].(string)+"/void", map[string]any{"reason": "Made by mistake"})

	d := diaryOf(t, seller)
	for _, want := range []string{
		"Made invoice " + num + " for ₦45,000",
		"Sent invoice " + num,
		"Matched a payment to invoice " + num,
		"Added an expense of ₦3,000 to the money log",
		"Cancelled invoice " + b["number"].(string),
	} {
		found := false
		for _, l := range d {
			if strings.Contains(l, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("diary %q missing from %q", want, d)
		}
	}

	// Only those who may see invoices see the money log.
	builder := addMember(t, w.world, seller, "build", "builder")
	builder.must(403, "GET", "/v1/money/entries", nil)
}

// "/invoice" and "/order" in a business's conversation (phase 2
// additions): what is between the two businesses only, named in a
// message with its total and status; the other side's copy carries no
// id of ours; someone else's invoice is refused.
func TestLinkChatRefs(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, buyerID := linkedPair(t, w.waWorld)
	x := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Ada's Kitchen", "key": "link:" + buyerID},
		"amount_kobo": 4250000}, "Idempotency-Key", "r-1")
	seller.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "r-1s")
	// Another customer's invoice: never offered here.
	other := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Bisi", "phone": "+2348030000557"}, "amount_kobo": 100000}, "Idempotency-Key", "r-2")
	seller.must(200, "POST", "/v1/invoices/"+other["id"].(string)+"/send", nil, "Idempotency-Key", "r-2s")
	if _, err := w.srv.LinkMirrorTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	link := seller.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	refs := seller.must(200, "GET", "/v1/link-chats/"+link+"/refs", nil)["refs"].([]any)
	if len(refs) != 1 {
		t.Fatalf("the seller's refs: %v", refs)
	}
	r := refs[0].(map[string]any)
	if r["kind"] != "invoice" || r["name"] != x["number"] || r["side"] != "ours" || r["total_kobo"] != float64(4250000) || r["status"] != "sent" {
		t.Fatalf("our invoice: %v", r)
	}
	bl := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	theirs := buyer.must(200, "GET", "/v1/link-chats/"+bl+"/refs", nil)["refs"].([]any)
	if len(theirs) != 1 || theirs[0].(map[string]any)["side"] != "theirs" || theirs[0].(map[string]any)["name"] != x["number"] {
		t.Fatalf("the buyer's refs: %v", theirs)
	}

	num := x["number"].(string)
	msg := seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{
		"text": "#" + num + " is due on Friday", "mentions": []any{map[string]any{"kind": "invoice", "ref": x["id"]}}})["message"].(map[string]any)
	ms, _ := msg["mentions"].([]any)
	if len(ms) != 1 {
		t.Fatalf("mentions: %v", msg)
	}
	if m := ms[0].(map[string]any); m["kind"] != "invoice" || m["name"] != num || m["price_kobo"] != float64(4250000) || m["status"] != "sent" || m["ref"] != x["id"] {
		t.Fatalf("the invoice named: %v", m)
	}
	w.srv.WaitBackground()
	got := buyer.must(200, "GET", "/v1/link-chats/"+bl+"/messages", nil)["messages"].([]any)
	if m := got[len(got)-1].(map[string]any)["mentions"].([]any)[0].(map[string]any); m["kind"] != "invoice" || m["name"] != num || m["ref"] != nil {
		t.Fatalf("their copy: %v", m)
	}
	// Someone else's invoice, and a made-up one.
	seller.must(400, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{
		"text": "#" + other["number"].(string), "mentions": []any{map[string]any{"kind": "invoice", "ref": other["id"]}}})
	seller.must(400, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{
		"text": "#INV-9999", "mentions": []any{map[string]any{"kind": "order", "ref": "nope"}}})
	// Without invoice.read, no invoices are offered.
	builder := addMember(t, w.world, seller, "build2", "builder")
	if refs := builder.must(200, "GET", "/v1/link-chats/"+link+"/refs", nil)["refs"].([]any); len(refs) != 0 {
		t.Fatalf("a builder's refs: %v", refs)
	}
}

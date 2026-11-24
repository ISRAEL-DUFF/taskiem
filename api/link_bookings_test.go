package api_test

import (
	"context"
	"strings"
	"testing"

	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/templates"
)

// Book in a business's conversation (docs/spec/business-chat-tools.md §3,
// §6 item 3): a linked business sees the other's services and their free
// times, as their storefront's booking offers them (the storefront never
// published, §7.1), and books one; the seller has it in their bookings
// like any other, a message in the conversation says so to both sides, a
// repeat of the request is the same booking, the same time again is
// refused when the service takes one at a time, and a business that is
// not linked can neither look nor book.
func TestLinkBookings(t *testing.T) {
	ctx := context.Background()
	w := shopWorld(t)
	withLinkedOrders(w.world)
	if err := w.env.Registry.Register(sfconn.New(w.srv.Orders)); err != nil {
		t.Fatal(err)
	}
	seller, buyer, _, _ := linkedPair(t, w.waWorld)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)

	// No storefront yet: nothing to book.
	if got := buyer.must(200, "GET", "/v1/link-chats/"+link+"/services", nil); len(got["services"].([]any)) != 0 || got["open"] != false {
		t.Fatalf("services before any: %v", got)
	}

	// The seller's services: a haircut, one at a time, every day 09:00 to
	// 17:00; bookings taken online; the storefront is not published.
	tbl := seller.must(201, "POST", "/v1/tables", map[string]any{"name": "services", "columns": []map[string]any{
		{"name": "name", "type": "text", "required": true}, {"name": "price", "type": "number"}, {"name": "minutes", "type": "integer"}}})["id"].(string)
	seller.must(201, "POST", "/v1/tables/"+tbl+"/rows", map[string]any{"row": map[string]any{"name": "Haircut", "price": "3000", "minutes": 30}}, "Idempotency-Key", "s1")
	seller.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "dangote-salon", "draft": map[string]any{"name": "Dangote Stores"}})
	every := []any{}
	for range 7 {
		every = append(every, []any{map[string]any{"open": "09:00", "close": "17:00"}})
	}
	seller.must(200, "PUT", "/v1/storefront/bookings", map[string]any{"services_table": tbl,
		"services_columns": map[string]any{"name": "name", "price": "price", "duration": "minutes"}, "availability": map[string]any{"days": every}})

	// Shown, but they take no bookings online yet.
	got := buyer.must(200, "GET", "/v1/link-chats/"+link+"/services", nil)
	if len(got["services"].([]any)) != 1 || got["open"] != false || !strings.Contains(got["closed"].(string), "Dangote Stores") {
		t.Fatalf("services, bookings not taken: %v", got)
	}
	tpl, _ := templates.Default().Get("storefront-booking")
	def, err := tpl.Instantiate(map[string]any{}, "Bookings")
	if err != nil {
		t.Fatal(err)
	}
	publishFlow(t, seller, string(def))
	seller.must(200, "PUT", "/v1/storefront/actions/booking", map[string]any{"enabled": true, "payment": "none"})

	got = buyer.must(200, "GET", "/v1/link-chats/"+link+"/services", nil)
	svcs := got["services"].([]any)
	if len(svcs) != 1 || got["open"] != true {
		t.Fatalf("services: %v", got)
	}
	haircut := svcs[0].(map[string]any)
	if haircut["name"] != "Haircut" || haircut["minutes"] != float64(30) || haircut["price_kobo"] != float64(300000) {
		t.Fatalf("the haircut as the buyer sees it: %v", haircut)
	}
	id := haircut["id"].(string)
	times := buyer.must(200, "GET", "/v1/link-chats/"+link+"/services/"+id+"/times", nil)
	if len(times["days"].([]any)) == 0 || len(times["times"].([]any)) == 0 || times["day"] != times["days"].([]any)[0] {
		t.Fatalf("times: %v", times)
	}
	// Another day's times.
	second := times["days"].([]any)[1].(string)
	other := buyer.must(200, "GET", "/v1/link-chats/"+link+"/services/"+id+"/times?day="+second, nil)
	if other["day"] != second || len(other["times"].([]any)) == 0 {
		t.Fatalf("the second day's times: %v", other)
	}
	buyer.must(400, "GET", "/v1/link-chats/"+link+"/services/"+id+"/times?day=tomorrow", nil)
	buyer.must(404, "GET", "/v1/link-chats/"+link+"/services/nope/times", nil)
	at := other["times"].([]any)[0].(string)

	// Booking: a phone is needed (the buyer has no storefront to give one);
	// a time not offered is refused.
	book := map[string]any{"service_id": id, "start": at, "note": "Two of us"}
	buyer.must(400, "POST", "/v1/link-chats/"+link+"/bookings", book)
	if e := buyer.must(422, "POST", "/v1/link-chats/"+link+"/bookings", book, "Idempotency-Key", "b0"); e["code"] != "phone_needed" {
		t.Fatalf("without a phone: %v", e)
	}
	book["phone"] = "0803 123 4567"
	odd := map[string]any{"service_id": id, "start": strings.Replace(at, ":00:00Z", ":07:00Z", 1), "phone": "0803 123 4567"}
	if strings.Contains(at, ":00:00Z") {
		if e := buyer.must(422, "POST", "/v1/link-chats/"+link+"/bookings", odd, "Idempotency-Key", "b-odd"); e["code"] != "pick_time" {
			t.Fatalf("a time not offered: %v", e)
		}
	}
	b := buyer.must(201, "POST", "/v1/link-chats/"+link+"/bookings", book, "Idempotency-Key", "b1")["booking"].(map[string]any)
	ref := b["reference"].(string)
	if b["service_name"] != "Haircut" || b["price_kobo"] != float64(300000) || ref == "" {
		t.Fatalf("booked: %v", b)
	}
	// The same request again: the same booking.
	again := buyer.must(200, "POST", "/v1/link-chats/"+link+"/bookings", book, "Idempotency-Key", "b1")["booking"].(map[string]any)
	if again["reference"] != ref {
		t.Fatalf("a repeat booked again: %v", again)
	}
	// The same time, once more: taken (one at a time).
	if e := buyer.must(409, "POST", "/v1/link-chats/"+link+"/bookings", book, "Idempotency-Key", "b2"); e["code"] != "slot_taken" {
		t.Fatalf("the same time again: %v", e)
	}
	// Nor is it offered any more.
	for _, x := range buyer.must(200, "GET", "/v1/link-chats/"+link+"/services/"+id+"/times?day="+second, nil)["times"].([]any) {
		if x == at {
			t.Fatalf("a booked time still offered")
		}
	}

	// The seller has it in their bookings, the buyer its customer.
	if err := w.srv.Orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	var found map[string]any
	for _, x := range seller.must(200, "GET", "/v1/storefront/bookings", nil)["bookings"].([]any) {
		if o := x.(map[string]any); o["reference"] == ref {
			found = o
		}
	}
	if found == nil || found["service_name"] != "Haircut" || found["kind"] != "booking" {
		t.Fatalf("the seller's bookings: %v", found)
	}
	// Both sides see it said in the conversation, once.
	for _, side := range []struct {
		c    *client
		link string
	}{{buyer, link}, {seller, sellerLink}} {
		n := 0
		for _, m := range side.c.must(200, "GET", "/v1/link-chats/"+side.link+"/messages", nil)["messages"].([]any) {
			if body, _ := m.(map[string]any)["body"].(string); strings.Contains(body, "Booked: Haircut") && strings.Contains(body, ref) {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("the booking said %d times in the conversation", n)
		}
	}

	// A business not linked with the seller can neither look nor book.
	stranger := w.tenant(t, "Bola Bakes", "bola@book.test")
	stranger.must(404, "GET", "/v1/link-chats/"+link+"/services", nil)
	stranger.must(404, "GET", "/v1/link-chats/"+link+"/services/"+id+"/times", nil)
	stranger.must(404, "POST", "/v1/link-chats/"+link+"/bookings", book, "Idempotency-Key", "s1")
}

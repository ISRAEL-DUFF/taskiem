package api_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/templates"
)

// Book through the door (taskiem-for-everyone §8.3, §8.4): the business's
// services and free times; a booking in the person's name, linked to
// them, recorded by the business's booking workflow, with its card in the
// chat; a repeat is the same booking. And the person's limits (§15): a new
// account writes fewer messages a minute, and starts fewer chats an hour,
// in its first week.
func TestCustomerDoorBookingAndLimits(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	owner := w.owner
	tbl := owner.must(201, "POST", "/v1/tables", map[string]any{"name": "services", "columns": []map[string]any{
		{"name": "name", "type": "text", "required": true}, {"name": "price", "type": "number"}, {"name": "minutes", "type": "integer"}}})["id"].(string)
	owner.must(201, "POST", "/v1/tables/"+tbl+"/rows", map[string]any{"row": map[string]any{"name": "Hair braiding", "price": "8000", "minutes": 60}}, "Idempotency-Key", "s1")
	every := []any{}
	for range 7 {
		every = append(every, []any{map[string]any{"open": "08:00", "close": "18:00"}})
	}
	owner.must(200, "PUT", "/v1/storefront/bookings", map[string]any{"services_table": tbl,
		"services_columns": map[string]any{"name": "name", "price": "price", "duration": "minutes"}, "availability": map[string]any{"days": every}})
	tpl, _ := templates.Default().Get("storefront-booking")
	def, err := tpl.Instantiate(map[string]any{}, "Bookings")
	if err != nil {
		t.Fatal(err)
	}
	publishFlow(t, owner, string(def))
	owner.must(200, "PUT", "/v1/storefront/actions/booking", map[string]any{"enabled": true, "payment": "none"})

	tolu, _ := w.personIn(t, "+2348031110009")
	toluID := personID(t, tolu)
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	got := tolu.must(200, "GET", "/v1/me/chats/"+id+"/services", nil)
	svcs := got["services"].([]any)
	if got["open"] != true || len(svcs) != 1 || svcs[0].(map[string]any)["name"] != "Hair braiding" {
		t.Fatalf("services: %v", got)
	}
	svc := svcs[0].(map[string]any)["id"].(string)
	times := tolu.must(200, "GET", "/v1/me/chats/"+id+"/services/"+svc+"/times", nil)
	second := times["days"].([]any)[1].(string)
	at := tolu.must(200, "GET", "/v1/me/chats/"+id+"/services/"+svc+"/times?day="+second, nil)["times"].([]any)[0].(string)
	tolu.must(404, "GET", "/v1/me/chats/"+id+"/services/nope/times", nil)

	book := map[string]any{"service_id": svc, "start": at, "note": "Long braids"}
	// The same booking sent four times at once: one booking, and each
	// answer is it (never "that time was just taken").
	var wg sync.WaitGroup
	answers := make([]map[string]any, 4)
	codes := make([]int, 4)
	for i := range answers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], answers[i] = tolu.do("POST", "/v1/me/chats/"+id+"/bookings", book, "Idempotency-Key", "b1")
		}()
	}
	wg.Wait()
	var b map[string]any
	created := 0
	for i, c := range codes {
		if c == 201 {
			created++
			b = answers[i]["booking"].(map[string]any)
		} else if c != 200 {
			t.Fatalf("a booking sent at once: %v %v", codes, answers)
		}
	}
	if created != 1 {
		t.Fatalf("booked %d times: %v", created, codes)
	}
	ref := b["reference"].(string)
	for _, a := range answers {
		if a["booking"].(map[string]any)["reference"] != ref {
			t.Fatalf("answers: %v", answers)
		}
	}
	if b["service_name"] != "Hair braiding" || b["price_kobo"] != float64(800000) {
		t.Fatalf("booked: %v", b)
	}
	if again := tolu.must(200, "POST", "/v1/me/chats/"+id+"/bookings", book, "Idempotency-Key", "b1")["booking"].(map[string]any); again["reference"] != ref {
		t.Fatalf("a repeat booked again: %v", again)
	}
	if e := tolu.must(409, "POST", "/v1/me/chats/"+id+"/bookings", book, "Idempotency-Key", "b2"); e["code"] != "slot_taken" {
		t.Fatalf("the same time again: %v", e)
	}
	if err := w.srv.Orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	var person *uuid.UUID
	var kind string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT kind, person_id FROM storefront_orders WHERE reference = $1`, ref).Scan(&kind, &person); err != nil ||
		kind != "booking" || person == nil || *person != toluID {
		t.Fatalf("the business's booking: %s %v %v", kind, person, err)
	}
	mine := tolu.must(200, "GET", "/v1/me/chats/"+id+"/orders", nil)["orders"].([]any)
	if len(mine) != 1 || mine[0].(map[string]any)["kind"] != "booking" || mine[0].(map[string]any)["summary"] != "Hair braiding" {
		t.Fatalf("your bookings: %v", mine)
	}
	pm := tolu.must(200, "GET", "/v1/me/chats/"+id+"/messages", nil)["messages"].([]any)
	if len(pm) != 1 || pm[0].(map[string]any)["card"].(map[string]any)["type"] != "booking" {
		t.Fatalf("the booking's card: %v", pm)
	}
	// A booking made in the app is told in the conversation, not by SMS.
	told, err := w.srv.Orders.NotifyCustomer(ctx, w.biz, ref, "Your braids are confirmed", "", "told:"+ref)
	if err != nil {
		t.Fatal(err)
	}
	if q := told["queued"].([]string); len(q) != 1 || q[0] != "app" {
		t.Fatalf("told by: %v", told)
	}
	pm = tolu.must(200, "GET", "/v1/me/chats/"+id+"/messages", nil)["messages"].([]any)
	if last := pm[len(pm)-1].(map[string]any); last["body"] != "Your braids are confirmed" || last["mine"] != false {
		t.Fatalf("told in the chat: %v", pm)
	}
	var sms int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM platform_messages WHERE tenant_id = $1 AND channel = 'sms' AND text LIKE '%braids%'`, w.biz).Scan(&sms); err == nil && sms != 0 {
		t.Fatalf("told by SMS too: %d", sms)
	}
	// The business sees it was made in the app.
	if so := w.owner.must(200, "GET", "/v1/storefront/orders/"+ref, nil)["order"].(map[string]any); so["in_app"] != true {
		t.Fatalf("the booking as the business sees it: %v", so)
	}
	// A booking offer from the business: the service, as a card.
	var thread string
	for _, c := range owner.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any) {
		thread = c.(map[string]any)["id"].(string)
	}
	c := owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"card": map[string]any{"type": "booking", "service_id": svc}})
	if c["card"].(map[string]any)["title"] != "Book Hair braiding" || c["card"].(map[string]any)["minutes"] != float64(60) {
		t.Fatalf("a booking offer: %v", c)
	}

	// In its first week, a person writes at most 10 messages a minute in a
	// chat (the booking's card was one).
	for i := range 9 {
		tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": fmt.Sprintf("Message %d", i)})
	}
	tolu.must(429, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "One too many"})
	// And starts at most 5 chats an hour; chats a business opened by
	// writing first are not the person's.
	for i := range 6 {
		if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO person_conversations (id, person_id, tenant_id, by_business) VALUES (gen_random_uuid(), $1, $2, $3)`,
			toluID, w.env.DB.SeedTenant(t, nil).ID, i%2 == 1); err != nil {
			t.Fatal(err)
		}
	}
	shop := func(name, slug string) uuid.UUID {
		other := w.tenant(t, name, "owner@"+slug+".test")
		verifyPhone(t, w.world, other, "phone", "business")
		other.must(200, "PUT", "/v1/storefront", map[string]any{"slug": slug, "draft": map[string]any{"name": name}})
		other.must(200, "POST", "/v1/storefront/publish", nil)
		return tenantIDNamed(t, w.world, name)
	}
	tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": shop("Ade Foods", "ade-foods")})
	if e := tolu.must(429, "POST", "/v1/me/chats", map[string]any{"business_id": shop("Bola Foods", "bola-foods")}); !strings.Contains(e["error"].(string), "many new chats") {
		t.Fatalf("the sixth chat: %v", e)
	}
}

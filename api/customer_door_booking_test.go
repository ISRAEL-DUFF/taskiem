package api_test

import (
	"context"
	"fmt"
	"strings"
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
	b := tolu.must(201, "POST", "/v1/me/chats/"+id+"/bookings", book, "Idempotency-Key", "b1")["booking"].(map[string]any)
	ref := b["reference"].(string)
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
	// And starts at most 5 chats an hour.
	for range 4 {
		if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO person_conversations (id, person_id, tenant_id) VALUES (gen_random_uuid(), $1, $2)`,
			toluID, w.env.DB.SeedTenant(t, nil).ID); err != nil {
			t.Fatal(err)
		}
	}
	other := w.tenant(t, "Ade Foods", "owner@ade.test")
	verifyPhone(t, w.world, other, "phone", "business")
	other.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ade-foods", "draft": map[string]any{"name": "Ade Foods"}})
	other.must(200, "POST", "/v1/storefront/publish", nil)
	if e := tolu.must(429, "POST", "/v1/me/chats", map[string]any{"business_id": tenantIDNamed(t, w.world, "Ade Foods")}); !strings.Contains(e["error"].(string), "many new chats") {
		t.Fatalf("the sixth chat: %v", e)
	}
}

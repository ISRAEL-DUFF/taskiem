package api_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/templates"
)

func TestStorefrontBookings(t *testing.T) {
	w := newTablesWorld(t)
	w.srv.PublicHosts = []string{"shops.taskiem.test"}
	key, _ := storefront.InboxKey(bytes.Repeat([]byte{3}, 32))
	w.srv.Orders = &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault, InboxKey: key, PreviewDomain: "shops.taskiem.test",
		Start: w.env.Store.Start, Signal: w.env.Store.DeliverSignalOnce, Cancel: w.env.Store.CancelRun}
	if err := w.env.Registry.Register(sfconn.New(w.srv.Orders)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	owner := w.tenant(t, "Ada's Salon", "ada@ada.test")
	tbl := owner.must(201, "POST", "/v1/tables", map[string]any{"name": "services", "columns": []map[string]any{
		{"name": "name", "type": "text", "required": true}, {"name": "price", "type": "number"}, {"name": "minutes", "type": "integer"},
		{"name": "seats", "type": "integer"}, {"name": "stylist_phone", "type": "phone"}}})
	table := tbl["id"].(string)
	owner.must(201, "POST", "/v1/tables/"+table+"/rows", map[string]any{"row": map[string]any{"name": "Haircut", "price": "3000", "minutes": 30}}, "Idempotency-Key", "s1")
	owner.must(201, "POST", "/v1/tables/"+table+"/rows", map[string]any{"row": map[string]any{"name": "Braiding class", "price": "5000", "minutes": 60, "seats": 2}}, "Idempotency-Key", "s2")
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-salon", "draft": map[string]any{"name": "Ada's Salon"}})

	cols := map[string]any{"name": "name", "price": "price", "duration": "minutes", "capacity": "seats"}
	every := []any{}
	for range 7 {
		every = append(every, []any{map[string]any{"open": "09:00", "close": "17:00"}})
	}
	for _, bad := range []map[string]any{
		{"services_table": table, "services_columns": map[string]any{"name": "name"}},
		{"services_table": table, "services_columns": map[string]any{"name": "name", "duration": "stylist_phone"}},
		{"services_table": table, "services_columns": cols, "availability": map[string]any{"days": []any{[]any{map[string]any{"open": "18:00", "close": "09:00"}}, nil, nil, nil, nil, nil, nil}}},
		{"services_table": table, "services_columns": cols, "availability": map[string]any{"off": []any{"next week"}}},
	} {
		owner.must(400, "PUT", "/v1/storefront/bookings", bad)
	}
	sf := owner.must(200, "PUT", "/v1/storefront/bookings", map[string]any{"services_table": table, "services_columns": cols,
		"availability": map[string]any{"days": every, "off": []any{"2026-12-25", "2026-12-25"}, "cutoff_minutes": 60}})
	if sf["services_table"] != table || sf["availability"].(map[string]any)["cutoff_minutes"] != float64(60) || len(sf["availability"].(map[string]any)["off"].([]any)) != 1 {
		t.Fatalf("%v", sf)
	}
	svc := owner.must(200, "GET", "/v1/storefront/services", nil)
	items := svc["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["duration_minutes"] != float64(30) || items[1].(map[string]any)["capacity"] != float64(2) ||
		svc["availability"].(map[string]any)["ahead_days"] != float64(storefront.DefaultAheadDays) {
		t.Fatalf("services: %v", svc)
	}
	haircut := items[0].(map[string]any)

	// The booking form needs its workflow; then it takes a deposit.
	owner.must(409, "PUT", "/v1/storefront/actions/booking", map[string]any{"enabled": true})
	tpl, _ := templates.Default().Get("storefront-booking")
	def, err := tpl.Instantiate(map[string]any{}, "Bookings")
	if err != nil {
		t.Fatal(err)
	}
	publishFlow(t, owner, string(def))
	owner.must(400, "PUT", "/v1/storefront/actions/booking", map[string]any{"enabled": true, "payment": "at_order", "options": map[string]any{"deposit_percent": 150}})
	a := owner.must(200, "PUT", "/v1/storefront/actions/booking", map[string]any{"enabled": true, "payment": "at_order", "options": map[string]any{"deposit_percent": 50}})
	if a["payment"] != "at_order" || a["options"].(map[string]any)["deposit_percent"] != float64(50) || a["label"] != "Book" {
		t.Fatalf("%v", a)
	}
	owner.must(200, "PUT", "/v1/storefront/actions/booking", map[string]any{"enabled": true, "payment": "none"})
	pub := owner.must(200, "POST", "/v1/storefront/publish", nil)
	sfID := uuid.MustParse(pub["id"].(string))

	book := func(start time.Time) string {
		t.Helper()
		ref := storefront.NewReference("ada-salon")
		_, hash := storefront.NewStatusKey()
		b := &storefront.Booked{ServiceID: haircut["id"].(string), ServiceName: "Haircut", Start: start, End: start.Add(30 * time.Minute), Until: start.Add(30 * time.Minute), Capacity: 1}
		ct, _ := storefront.SealSubmission(key, sfID, ref, storefront.Submission{Kind: "booking", Payment: "none", Channel: "direct", Submitted: time.Now(), TotalKobo: 300000,
			Booking: b, Customer: storefront.Customer{Name: "Bola Ade", Phone: "+2348031234567"}})
		idem := make([]byte, 32)
		copy(idem, ref)
		var got *string
		if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT taskiem_public_book($1, $2, $3, $4, $5, $6, $7, $8, 1)`, sfID, ref, idem, hash, ct, b.ServiceID, b.Start, b.Until).Scan(&got); err != nil || got == nil {
			t.Fatalf("book: %v %v", got, err)
		}
		return ref
	}
	now := time.Now().In(storefront.Lagos)
	day := time.Date(now.Year(), now.Month(), now.Day()+3, 0, 0, 0, 0, storefront.Lagos)
	first, second := book(day.Add(10*time.Hour)), book(day.Add(11*time.Hour))
	if err := w.srv.Orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	list := owner.must(200, "GET", "/v1/storefront/bookings", nil)["bookings"].([]any)
	if len(list) != 2 {
		t.Fatalf("%v", list)
	}
	b := list[0].(map[string]any)
	if b["reference"] != first || b["service_name"] != "Haircut" || b["kind"] != "booking" || b["customer"].(map[string]any)["name"] != "B•••" {
		t.Fatalf("listed: %v", b)
	}
	owner.must(400, "GET", "/v1/storefront/bookings?from=tomorrow", nil)
	if l := owner.must(200, "GET", "/v1/storefront/orders?kind=booking", nil)["orders"].([]any); len(l) != 2 {
		t.Fatalf("kind=booking: %v", l)
	}
	if l := owner.must(200, "GET", "/v1/storefront/orders?kind=order&status=open", nil)["orders"].([]any); len(l) != 0 {
		t.Fatalf("kind=order: %v", l)
	}

	// The owner moves and cancels from the web app as from chat.
	owner.must(409, "POST", "/v1/storefront/bookings/"+first+"/move", map[string]any{"starts_at": day.Add(11 * time.Hour)})
	moved := owner.must(200, "POST", "/v1/storefront/bookings/"+first+"/move", map[string]any{"starts_at": day.Add(14 * time.Hour)})["booking"].(map[string]any)
	if moved["version"] != float64(1) {
		t.Fatalf("moved: %v", moved)
	}
	owner.must(200, "POST", "/v1/storefront/bookings/"+second+"/cancel", nil)
	owner.must(409, "POST", "/v1/storefront/bookings/"+second+"/cancel", nil)
	owner.must(404, "POST", "/v1/storefront/bookings/AD-NOPE00/cancel", nil)
	if err := w.srv.Orders.ProcessChanges(ctx); err != nil {
		t.Fatal(err)
	}
	if l := owner.must(200, "GET", "/v1/storefront/bookings", nil)["bookings"].([]any); len(l) != 1 {
		t.Fatalf("after cancelling: %v", l)
	}

	// Another organisation sees none of it.
	other := w.tenant(t, "Bola Bakes", "bola@bola.test")
	other.must(404, "GET", "/v1/storefront/services", nil)
	other.must(404, "POST", "/v1/storefront/bookings/"+first+"/cancel", nil)
}

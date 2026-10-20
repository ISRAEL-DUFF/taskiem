package publicweb

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// TestBookingThroughTheDatabase books, cancels, moves and confirms new
// prices through the public host's pages over the real functions of
// 00181, as taskiem_public: two customers after one time, one gets it.
func TestBookingThroughTheDatabase(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	tn := d.SeedTenant(t, nil)
	sf := uuid.New()
	var days [7][]storefront.Span
	for i := range days {
		days[i] = []storefront.Span{{Open: "09:00", Close: "17:00"}}
	}
	avail, _ := json.Marshal(storefront.Availability{Days: days})
	if _, err := d.Admin.Exec(ctx, `INSERT INTO storefronts (id, tenant_id, slug, status, created_by, availability) VALUES ($1, $2, 'ada-kitchen', 'published', 'user:ada', $3)`,
		sf, tn.ID, avail); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `INSERT INTO storefront_actions (storefront_id, kind, tenant_id, enabled, label, payment) VALUES ($1, 'booking', $2, true, 'Book', 'none')`, sf, tn.ID); err != nil {
		t.Fatal(err)
	}
	for i, it := range []storefront.Item{haircut, class} {
		raw, _ := json.Marshal(it)
		if _, err := d.Admin.Exec(ctx, `INSERT INTO storefront_services (storefront_id, row_id, tenant_id, position, item) VALUES ($1, $2, $3, $4, $5)`, sf, it.ID, tn.ID, i, raw); err != nil {
			t.Fatal(err)
		}
	}
	pool := d.PublicPool(t, 4)
	p := samplePage("classic", 0)
	p.Model.HasServices = true
	p.Model.Actions = []storefront.PublicAction{{Kind: "booking", Label: "Book", Payment: "none"}}
	s := &Server{Lookup: func(context.Context, string) (Site, bool, error) {
		return Site{Tenant: tn.ID, Kind: "storefront", ID: sf}, true, nil
	},
		Storefronts: func(context.Context, uuid.UUID) (StorefrontPage, bool, error) { return p, true, nil },
		Submit:      DBSubmit(pool), OrderStatus: DBOrderStatus(pool), Services: DBServices(pool), Busy: DBBusy(pool), Book: DBBook(pool),
		Booking: DBBookingStatus(pool), Change: DBChange(pool), PriceChange: DBPriceChange(pool),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Views: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour),
		Submissions: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour)}
	var err error
	if s.FormKey, err = storefront.FormKey(testMaster); err != nil {
		t.Fatal(err)
	}
	if s.InboxKey, err = storefront.InboxKey(testMaster); err != nil {
		t.Fatal(err)
	}
	// The form's clock runs ahead of the database's by what a person takes
	// to fill a form; the database's is the real one.
	ahead := time.Duration(0)
	s.now = func() time.Time { return time.Now().Add(ahead) }
	e := &formEnv{s: s, h: s.Handler(), sf: sf}
	wait := func() { ahead += 5 * time.Second }

	today := time.Now().In(storefront.Lagos)
	day := func(n int) string { return today.AddDate(0, 0, n).Format(time.DateOnly) }
	slot := func(n, hh, mm int) time.Time {
		return time.Date(today.Year(), today.Month(), today.Day()+n, hh, mm, 0, 0, storefront.Lagos)
	}
	start := slot(3, 10, 0)

	_, tok1 := e.form(t, "/a/booking?service=7&day="+day(3))
	_, tok2 := e.form(t, "/a/booking?service=7&day="+day(3))
	wait()
	w := e.post("/a/booking", goodBooking(tok1, start))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("first: %d %s", w.Code, w.Body.String())
	}
	first := w.Header().Get("Location")
	w = e.post("/a/booking", goodBooking(tok2, start))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), msgSlotTaken) || strings.Contains(w.Body.String(), `value="`+unix(start)+`"`) {
		t.Fatalf("second: %d %s", w.Code, w.Body.String())
	}
	if body := get(e.h, "GET", sfHost, first).Body.String(); !strings.Contains(body, "We have your booking") {
		t.Fatalf("in the inbox: %s", body)
	}
	// The second takes 10:30 instead.
	tok := tokenRe.FindStringSubmatch(w.Body.String())[1]
	wait()
	w = e.post("/a/booking", goodBooking(tok, start.Add(30*time.Minute)))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("second, again: %d %s", w.Code, w.Body.String())
	}
	second := w.Header().Get("Location")

	// What the api role makes of them: orders holding their times.
	record := func(loc, status string) {
		t.Helper()
		ref := refOf(loc)
		if _, err := d.Admin.Exec(ctx, `INSERT INTO storefront_orders (id, tenant_id, storefront_id, kind, reference, status_hash, status, customer, subject_id,
			total_kobo, payment, service_id, service_name, starts_at, ends_at)
			SELECT gen_random_uuid(), i.tenant_id, i.storefront_id, 'booking', i.reference, i.status_hash, $2, '{}', 'subject', 300000, 'none', h.service_id, 'Haircut',
			  h.starts_at, h.until_at FROM storefront_inbox i JOIN storefront_holds h ON h.reference = i.reference WHERE i.reference = $1`, ref, status); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Admin.Exec(ctx, `DELETE FROM storefront_inbox WHERE reference = $1`, ref); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Admin.Exec(ctx, `UPDATE storefront_holds SET expires_at = NULL WHERE reference = $1`, ref); err != nil {
			t.Fatal(err)
		}
	}
	record(first, "received")
	record(second, "accepted")
	body := get(e.h, "GET", sfHost, first).Body.String()
	if !strings.Contains(body, "Waiting for") || !strings.Contains(body, "Haircut, "+storefront.When(start)) || !strings.Contains(body, "Move to another time") {
		t.Fatalf("received: %s", body)
	}

	// The first cancels.
	ref1, key1 := refOf(first), strings.Split(first, "k=")[1]
	_, tok = e.form(t, "/o/"+ref1+"/cancel?k="+key1)
	wait()
	if w := e.post("/o/"+ref1+"/cancel?k="+key1, url.Values{"token": {tok}}); w.Code != 303 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	var status string
	var holds, changes int
	if err := d.Admin.QueryRow(ctx, `SELECT status, (SELECT count(*) FROM storefront_holds WHERE reference = $1), (SELECT count(*) FROM storefront_changes)
		FROM storefront_orders WHERE reference = $1`, ref1).Scan(&status, &holds, &changes); err != nil || status != "cancelled" || holds != 0 || changes != 1 {
		t.Fatalf("after cancelling: %s, %d holds, %d changes, %v", status, holds, changes, err)
	}

	// The second moves into the time the first gave up.
	ref2, key2 := refOf(second), strings.Split(second, "k=")[1]
	body, tok = e.form(t, "/o/"+ref2+"/move?k="+key2+"&day="+day(3))
	if !strings.Contains(body, `value="`+unix(start)+`"`) || strings.Contains(body, `value="`+unix(start.Add(30*time.Minute))+`"`) {
		t.Fatalf("move page: %s", body)
	}
	wait()
	if w := e.post("/o/"+ref2+"/move?k="+key2, url.Values{"token": {tok}, "start": {unix(start)}}); w.Code != 303 {
		t.Fatalf("move: %d %s", w.Code, w.Body.String())
	}
	var moved time.Time
	if err := d.Admin.QueryRow(ctx, `SELECT h.starts_at FROM storefront_holds h WHERE h.reference = $1`, ref2).Scan(&moved); err != nil || !moved.Equal(start) {
		t.Fatalf("hold at %v %v", moved, err)
	}
	if body := get(e.h, "GET", sfHost, second).Body.String(); !strings.Contains(body, "Haircut, "+storefront.When(start)) || !strings.Contains(body, "Your booking is confirmed") {
		t.Fatalf("after moving: %s", body)
	}

	// New prices, waiting on the second customer.
	if _, err := d.Admin.Exec(ctx, `UPDATE storefront_orders SET status = 'price_changed', price_change = '{"total_kobo": 360000, "deposit_kobo": 100000}' WHERE reference = $1`, ref2); err != nil {
		t.Fatal(err)
	}
	body = get(e.h, "GET", sfHost, second).Body.String()
	if !strings.Contains(body, "Prices have changed") || !strings.Contains(body, "The new total is ₦3,600.") || !strings.Contains(body, "You pay ₦1,000 now.") {
		t.Fatalf("price changed: %s", body)
	}
	wait()
	if w := e.post("/o/"+ref2+"/prices?k="+key2, url.Values{"token": {tokenRe.FindStringSubmatch(body)[1]}, "do": {"accept"}}); w.Code != 303 {
		t.Fatalf("accept: %d %s", w.Code, w.Body.String())
	}
	if err := d.Admin.QueryRow(ctx, `SELECT status FROM storefront_orders WHERE reference = $1`, ref2).Scan(&status); err != nil || status != "received" {
		t.Fatalf("after accepting: %s %v", status, err)
	}

	// A wrong key is no page at all.
	r := httptest.NewRequest("GET", "/o/"+ref2+"/cancel?k=wrong", nil)
	r.Host = sfHost
	rw := httptest.NewRecorder()
	e.h.ServeHTTP(rw, r)
	if rw.Code != 404 {
		t.Fatalf("a wrong key: %d", rw.Code)
	}
}

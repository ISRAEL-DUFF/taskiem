package publicweb

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// TestHireThroughTheDatabase hires canopies through the public host's hire
// form over taskiem_public_claim, as taskiem_public: the count is what the
// owner has, claims on overlapping days add up, and one beyond the count
// is refused while other days are free.
func TestHireThroughTheDatabase(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	tn := d.SeedTenant(t, nil)
	sf, front, block, action := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO storefronts (id, tenant_id, slug, status, created_by) VALUES ($1, $2, 'ada-hire', 'published', 'user:ada')`, []any{sf, tn.ID}},
		{`INSERT INTO storefront_fronts (id, tenant_id, storefront_id, slug, kind, name, enabled) VALUES ($1, $2, $3, 'tools', 'hire', 'Tools', true)`, []any{front, tn.ID, sf}},
		{`INSERT INTO storefront_blocks (id, tenant_id, front_id, kind, config) VALUES ($1, $2, $3, 'list', '{"table_name": "hire_items"}')`, []any{block, tn.ID, front}},
		{`INSERT INTO storefront_actions (id, storefront_id, kind, tenant_id, enabled, label, payment, front_id, slug, builtin) VALUES ($1, $2, 'hire', $3, true, 'Hire now', 'after_accept', $4, 'hire', false)`,
			[]any{action, sf, tn.ID, front}},
	} {
		if _, err := d.Admin.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	canopy := storefront.Item{ID: "1", Name: "Canopy", PriceKobo: kobo(500000), Available: true, Capacity: 3}
	chairs := storefront.Item{ID: "2", Name: "Chairs", PriceKobo: kobo(20000), Available: false, Capacity: 100}
	for i, it := range []storefront.Item{canopy, chairs} {
		raw, _ := json.Marshal(it)
		if _, err := d.Admin.Exec(ctx, `INSERT INTO storefront_catalogue (storefront_id, table_name, row_id, tenant_id, position, item) VALUES ($1, 'hire_items', $2, $3, $4, $5)`,
			sf, it.ID, tn.ID, i, raw); err != nil {
			t.Fatal(err)
		}
	}
	pool := d.PublicPool(t, 4)
	p := samplePage("classic", 0)
	tools := storefront.PublicFront{Slug: "tools", Kind: "hire", Name: "Tools",
		Blocks:  []storefront.PublicBlock{{Kind: "list", Config: json.RawMessage(`{"table_name": "hire_items"}`)}},
		Actions: []storefront.PublicAction{{ID: &action, Slug: "hire", Kind: "hire", Label: "Hire now", Payment: "after_accept", Pickup: true, DepositPercent: 50, Form: storefront.BuiltinForm("hire")}}}
	p.Model.Fronts = []storefront.PublicFront{{Slug: "", Kind: "shop", Name: "Ada"}, tools}
	// The page's copy says the chairs are out; the hidden check below is the database's.
	chairs.Available = true
	p.Lists = map[string][]storefront.Item{"hire_items": {canopy, chairs}}
	s := &Server{Lookup: func(context.Context, string) (Site, bool, error) {
		return Site{Tenant: tn.ID, Kind: "storefront", ID: sf}, true, nil
	},
		Storefronts: func(context.Context, uuid.UUID) (StorefrontPage, bool, error) { return p, true, nil },
		Submit:      DBSubmit(pool), OrderStatus: DBOrderStatus(pool), Claim: DBClaim(pool),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Views: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour),
		Submissions: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour)}
	var err error
	if s.FormKey, err = storefront.FormKey(testMaster); err != nil {
		t.Fatal(err)
	}
	if s.InboxKey, err = storefront.InboxKey(testMaster); err != nil {
		t.Fatal(err)
	}
	ahead := time.Duration(0)
	s.now = func() time.Time { return time.Now().Add(ahead) }
	e := &formEnv{s: s, h: s.Handler(), sf: sf}

	today := time.Now().In(storefront.Lagos)
	day := func(n int) string { return today.AddDate(0, 0, n).Format(time.DateOnly) }
	hire := func(item string, from, to, qty int) *httptest.ResponseRecorder {
		t.Helper()
		_, tok := e.form(t, "/tools/a/hire")
		ahead += 5 * time.Second
		return e.post("/tools/a/hire", url.Values{"token": {tok}, "website": {""}, "name": {"Bola"}, "phone": {"0803 123 4567"}, "fulfilment": {"pickup"},
			"hire_item": {item}, "hire_from": {day(from)}, "hire_to": {day(to)}, "hire_qty": {strconv.Itoa(qty)}})
	}

	body, _ := e.form(t, "/tools/a/hire")
	if !strings.Contains(body, `name="hire_from"`) || !strings.Contains(body, "Canopy, ₦5,000 a day") || !strings.Contains(body, "50% of the price is paid to confirm") {
		t.Fatalf("the hire form: %s", body)
	}

	// Two canopies for three days: ₦30,000, half of it to confirm.
	r := hire("1", 3, 5, 2)
	if r.Code != http.StatusSeeOther {
		t.Fatalf("first: %d", r.Code)
	}
	ref := refOf(r.Header().Get("Location"))
	var payload []byte
	var qty int
	var starts, until time.Time
	if err := d.Admin.QueryRow(ctx, `SELECT i.payload, h.qty, h.starts_at, h.until_at FROM storefront_inbox i JOIN storefront_claims h ON h.reference = i.reference
		WHERE i.reference = $1 AND i.kind = 'hire' AND i.action_id = $2 AND h.block_id = $3 AND h.item_id = '1'`, ref, action, block).Scan(&payload, &qty, &starts, &until); err != nil {
		t.Fatal(err)
	}
	sub, err := storefront.OpenSubmission(s.InboxKey, sf, ref, payload)
	from := time.Date(today.Year(), today.Month(), today.Day()+3, 0, 0, 0, 0, storefront.Lagos)
	if err != nil || qty != 2 || !starts.Equal(from) || !until.Equal(from.AddDate(0, 0, 3)) || sub.Hire == nil || sub.Hire.Days != 3 ||
		sub.TotalKobo != 3000000 || sub.DepositKobo != 1500000 {
		t.Fatalf("claimed %d from %v to %v: %+v %v", qty, starts, until, sub, err)
	}

	// Two more overlapping those days are more than the third left.
	if r := hire("1", 5, 6, 2); r.Code != http.StatusConflict {
		t.Fatalf("beyond the count: %d", r.Code)
	}
	if r := hire("1", 4, 6, 1); r.Code != http.StatusSeeOther {
		t.Fatalf("the third: %d", r.Code)
	}
	// Days after the first hire: two free again.
	if r := hire("1", 6, 7, 2); r.Code != http.StatusSeeOther {
		t.Fatalf("after: %d", r.Code)
	}
	if r := hire("1", 6, 6, 1); r.Code != http.StatusConflict {
		t.Fatalf("day 6 is full: %d", r.Code)
	}
	// A claim that lapsed unread takes nothing.
	if _, err := d.Admin.Exec(ctx, `UPDATE storefront_claims SET expires_at = now() - interval '1 minute' WHERE reference = $1`, ref); err != nil {
		t.Fatal(err)
	}
	if r := hire("1", 3, 3, 3); r.Code != http.StatusSeeOther {
		t.Fatalf("after the lapse: %d", r.Code)
	}
	// An item the database's copy hides is refused, whatever the page says.
	if r := hire("2", 3, 4, 1); r.Code != http.StatusConflict {
		t.Fatalf("hidden: %d", r.Code)
	}
	// What is taken, for the page: by item and time, no references.
	var taken int
	if err := pool.QueryRow(ctx, `SELECT COALESCE(sum(qty), 0) FROM taskiem_public_taken($1, $2, $3)`, action, from, from.AddDate(0, 0, 10)).Scan(&taken); err != nil || taken != 1+2+3 {
		t.Fatalf("taken: %d %v", taken, err)
	}
}

// TestSpaceThroughTheDatabase books a hall by the hour through the public
// host over taskiem_public_claim: one party at a time, an hour of setup
// kept clear between bookings, and a booking by the day that runs from
// opening to closing.
func TestSpaceThroughTheDatabase(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	tn := d.SeedTenant(t, nil)
	sf, front, block, action := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	cfg := `{"table_name": "spaces", "calendar": {"grain": "hours", "open": "08:00", "close": "22:00", "min_hours": 2, "setup_minutes": 60}}`
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO storefronts (id, tenant_id, slug, status, created_by) VALUES ($1, $2, 'ada-halls', 'published', 'user:ada')`, []any{sf, tn.ID}},
		{`INSERT INTO storefront_fronts (id, tenant_id, storefront_id, slug, kind, name, enabled) VALUES ($1, $2, $3, 'halls', 'spaces', 'Halls', true)`, []any{front, tn.ID, sf}},
		{`INSERT INTO storefront_blocks (id, tenant_id, front_id, kind, config) VALUES ($1, $2, $3, 'list', $4)`, []any{block, tn.ID, front, cfg}},
		{`INSERT INTO storefront_actions (id, storefront_id, kind, tenant_id, enabled, label, payment, front_id, slug, builtin) VALUES ($1, $2, 'space', $3, true, 'Book the space', 'after_accept', $4, 'space', false)`,
			[]any{action, sf, tn.ID, front}},
	} {
		if _, err := d.Admin.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	hall := storefront.Item{ID: "1", Name: "Main hall", PriceKobo: kobo(2000000), Available: true}
	raw, _ := json.Marshal(hall)
	if _, err := d.Admin.Exec(ctx, `INSERT INTO storefront_catalogue (storefront_id, table_name, row_id, tenant_id, position, item) VALUES ($1, 'spaces', '1', $2, 0, $3)`, sf, tn.ID, raw); err != nil {
		t.Fatal(err)
	}
	pool := d.PublicPool(t, 4)
	p := samplePage("classic", 0)
	halls := storefront.PublicFront{Slug: "halls", Kind: "spaces", Name: "Halls",
		Blocks:  []storefront.PublicBlock{{Kind: "list", Config: json.RawMessage(cfg)}},
		Actions: []storefront.PublicAction{{ID: &action, Slug: "space", Kind: "space", Label: "Book the space", Payment: "after_accept", DepositPercent: 50, Form: storefront.BuiltinForm("space")}}}
	p.Model.Fronts = []storefront.PublicFront{{Slug: "", Kind: "shop", Name: "Ada"}, halls}
	p.Lists = map[string][]storefront.Item{"spaces": {hall}}
	s := &Server{Lookup: func(context.Context, string) (Site, bool, error) {
		return Site{Tenant: tn.ID, Kind: "storefront", ID: sf}, true, nil
	},
		Storefronts: func(context.Context, uuid.UUID) (StorefrontPage, bool, error) { return p, true, nil },
		Submit:      DBSubmit(pool), OrderStatus: DBOrderStatus(pool), Claim: DBClaim(pool),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Views: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour),
		Submissions: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour)}
	var err error
	if s.FormKey, err = storefront.FormKey(testMaster); err != nil {
		t.Fatal(err)
	}
	if s.InboxKey, err = storefront.InboxKey(testMaster); err != nil {
		t.Fatal(err)
	}
	ahead := time.Duration(0)
	s.now = func() time.Time { return time.Now().Add(ahead) }
	e := &formEnv{s: s, h: s.Handler(), sf: sf}
	today := time.Now().In(storefront.Lagos)
	day := today.AddDate(0, 0, 3).Format(time.DateOnly)
	book := func(from, to int) *httptest.ResponseRecorder {
		t.Helper()
		_, tok := e.form(t, "/halls/a/space")
		ahead += 5 * time.Second
		return e.post("/halls/a/space", url.Values{"token": {tok}, "website": {""}, "name": {"Bola"}, "phone": {"0803 123 4567"},
			"hire_item": {"1"}, "hire_day": {day}, "hire_start": {strconv.Itoa(from)}, "hire_end": {strconv.Itoa(to)}})
	}

	body, _ := e.form(t, "/halls/a/space")
	if !strings.Contains(body, `name="hire_start"`) || !strings.Contains(body, "Main hall, ₦20,000 an hour") || !strings.Contains(body, "Open 08:00 to 22:00. At least 2 hours.") ||
		strings.Contains(body, `name="hire_qty"`) {
		t.Fatalf("the space form: %s", body)
	}
	// 10:00 to 14:00: four hours, ₦80,000, half to confirm.
	r := book(10, 14)
	if r.Code != http.StatusSeeOther {
		t.Fatalf("first: %d %s", r.Code, r.Body.String())
	}
	ref := refOf(r.Header().Get("Location"))
	var payload []byte
	if err := d.Admin.QueryRow(ctx, `SELECT payload FROM storefront_inbox WHERE reference = $1 AND kind = 'space'`, ref).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	sub, err := storefront.OpenSubmission(s.InboxKey, sf, ref, payload)
	if err != nil || sub.TotalKobo != 8000000 || sub.DepositKobo != 4000000 || sub.Hire == nil || sub.Hire.Hours != 4 || sub.Basket[0].Unit != "hour" || sub.Hire.From.In(storefront.Lagos).Hour() != 10 {
		t.Fatalf("booked: %+v %v", sub, err)
	}
	// One hour is too short; 14:00 is inside the setup hour; 15:00 is clear.
	if r := book(16, 17); r.Code != http.StatusUnprocessableEntity || !strings.Contains(r.Body.String(), "Book at least 2 hours.") {
		t.Fatalf("too short: %d", r.Code)
	}
	if r := book(14, 16); r.Code != http.StatusConflict || !strings.Contains(r.Body.String(), "That space is booked for some of that time.") {
		t.Fatalf("in the setup time: %d", r.Code)
	}
	if r := book(15, 18); r.Code != http.StatusSeeOther {
		t.Fatalf("after setup: %d %s", r.Code, r.Body.String())
	}
	if r := book(12, 13); r.Code == http.StatusSeeOther {
		t.Fatal("an overlap was booked")
	}
}

// TestCountsThroughTheDatabase sells tickets and class places through the
// public host's count field over taskiem_public_claim: the event's count
// holds, a class's fee may be paid in instalments (the first now), and a
// full class offers its waiting list, which holds no place.
func TestCountsThroughTheDatabase(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	tn := d.SeedTenant(t, nil)
	sf := uuid.New()
	if _, err := d.Admin.Exec(ctx, `INSERT INTO storefronts (id, tenant_id, slug, status, created_by) VALUES ($1, $2, 'ada-events', 'published', 'user:ada')`, sf, tn.ID); err != nil {
		t.Fatal(err)
	}
	starts := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)
	concert := storefront.Item{ID: "1", Name: "Concert", PriceKobo: kobo(500000), Available: true, Capacity: 3, Starts: &starts, Venue: "Muson Centre"}
	course := storefront.Item{ID: "2", Name: "Baking class", PriceKobo: kobo(3000000), Available: true, Capacity: 1, Starts: &starts}
	p := samplePage("classic", 0)
	p.Model.Fronts = []storefront.PublicFront{{Slug: "", Kind: "shop", Name: "Ada"}}
	p.Lists = map[string][]storefront.Item{}
	actions := map[string]uuid.UUID{}
	for _, f := range []struct {
		slug, kind, action, table string
		item                      storefront.Item
		opts                      string
		pub                       storefront.PublicAction
	}{
		{"concerts", "tickets", "tickets", "events", concert, `{"max_per_order": 2}`, storefront.PublicAction{MaxPerOrder: 2}},
		{"courses", "classes", "class", "classes", course, `{"instalments": 3, "waiting_list": true}`, storefront.PublicAction{Instalments: 3, WaitingList: true}},
	} {
		front, action := uuid.New(), uuid.New()
		raw, _ := json.Marshal(f.item)
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO storefront_fronts (id, tenant_id, storefront_id, slug, kind, name, enabled) VALUES ($1, $2, $3, $4, $5, $4, true)`, []any{front, tn.ID, sf, f.slug, f.kind}},
			{`INSERT INTO storefront_blocks (id, tenant_id, front_id, kind, config) VALUES ($1, $2, $3, 'list', jsonb_build_object('table_name', $4::text))`, []any{uuid.New(), tn.ID, front, f.table}},
			{`INSERT INTO storefront_actions (id, storefront_id, kind, tenant_id, enabled, label, payment, front_id, slug, builtin, options) VALUES ($1, $2, $3, $4, true, 'Take', 'at_order', $5, $3, false, $6)`,
				[]any{action, sf, f.action, tn.ID, front, f.opts}},
			{`INSERT INTO storefront_catalogue (storefront_id, table_name, row_id, tenant_id, position, item) VALUES ($1, $2, $3, $4, 0, $5)`, []any{sf, f.table, f.item.ID, tn.ID, raw}},
		} {
			if _, err := d.Admin.Exec(ctx, q.sql, q.args...); err != nil {
				t.Fatal(err)
			}
		}
		actions[f.action] = action
		pub := f.pub
		pub.ID, pub.Slug, pub.Kind, pub.Label, pub.Payment, pub.Form = &action, f.action, f.action, "Take", "at_order", storefront.BuiltinForm(f.action)
		p.Model.Fronts = append(p.Model.Fronts, storefront.PublicFront{Slug: f.slug, Kind: f.kind, Name: f.slug,
			Blocks:  []storefront.PublicBlock{{Kind: "list", Config: json.RawMessage(`{"table_name": "` + f.table + `"}`)}},
			Actions: []storefront.PublicAction{pub}})
		p.Lists[f.table] = []storefront.Item{f.item}
	}
	pool := d.PublicPool(t, 4)
	s := &Server{Lookup: func(context.Context, string) (Site, bool, error) {
		return Site{Tenant: tn.ID, Kind: "storefront", ID: sf}, true, nil
	},
		Storefronts: func(context.Context, uuid.UUID) (StorefrontPage, bool, error) { return p, true, nil },
		Submit:      DBSubmit(pool), OrderStatus: DBOrderStatus(pool), Claim: DBClaim(pool),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Views: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour),
		Submissions: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour)}
	var err error
	if s.FormKey, err = storefront.FormKey(testMaster); err != nil {
		t.Fatal(err)
	}
	if s.InboxKey, err = storefront.InboxKey(testMaster); err != nil {
		t.Fatal(err)
	}
	ahead := time.Duration(0)
	s.now = func() time.Time { return time.Now().Add(ahead) }
	e := &formEnv{s: s, h: s.Handler(), sf: sf}
	take := func(path string, extra url.Values) *httptest.ResponseRecorder {
		t.Helper()
		_, tok := e.form(t, path)
		ahead += 5 * time.Second
		v := url.Values{"token": {tok}, "website": {""}, "name": {"Bola"}, "phone": {"0803 123 4567"}}
		for k, x := range extra {
			v[k] = x
		}
		return e.post(path, v)
	}
	sealed := func(loc string) storefront.Submission {
		t.Helper()
		ref := refOf(loc)
		var payload []byte
		if err := d.Admin.QueryRow(ctx, `SELECT payload FROM storefront_inbox WHERE reference = $1`, ref).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		sub, err := storefront.OpenSubmission(s.InboxKey, sf, ref, payload)
		if err != nil {
			t.Fatal(err)
		}
		return sub
	}

	body, _ := e.form(t, "/concerts/a/tickets")
	if !strings.Contains(body, "Concert, "+storefront.When(starts)+", Muson Centre, ₦5,000") || !strings.Contains(body, `max="2"`) || strings.Contains(body, `name="plan"`) {
		t.Fatalf("the ticket form: %s", body)
	}
	r := take("/concerts/a/tickets", url.Values{"hire_item": {"1"}, "hire_qty": {"2"}})
	if r.Code != http.StatusSeeOther {
		t.Fatalf("two tickets: %d %s", r.Code, r.Body.String())
	}
	if sub := sealed(r.Header().Get("Location")); sub.Kind != "tickets" || sub.TotalKobo != 1000000 || sub.Hire == nil || !sub.Hire.From.Equal(starts) {
		t.Fatalf("sent: %+v", sub)
	}
	if r := take("/concerts/a/tickets", url.Values{"hire_item": {"1"}, "hire_qty": {"3"}}); r.Code != http.StatusUnprocessableEntity || !strings.Contains(r.Body.String(), "At most 2 in one order.") {
		t.Fatalf("over the order's limit: %d", r.Code)
	}
	if r := take("/concerts/a/tickets", url.Values{"hire_item": {"1"}, "hire_qty": {"2"}}); r.Code != http.StatusConflict || !strings.Contains(r.Body.String(), "Not that many tickets are left.") {
		t.Fatalf("beyond the event's: %d", r.Code)
	}

	// A class in three instalments: the first now.
	body, _ = e.form(t, "/courses/a/class")
	if !strings.Contains(body, `name="plan" value="3"`) || !strings.Contains(body, "Pay in 3 instalments, the first now") {
		t.Fatalf("the class form: %s", body)
	}
	r = take("/courses/a/class", url.Values{"hire_item": {"2"}, "hire_qty": {"1"}, "plan": {"3"}})
	if r.Code != http.StatusSeeOther {
		t.Fatalf("enrol: %d %s", r.Code, r.Body.String())
	}
	if sub := sealed(r.Header().Get("Location")); sub.Kind != "class" || sub.Instalments != 3 || sub.DepositKobo != 1000000 || sub.TotalKobo != 3000000 {
		t.Fatalf("enrolled: %+v", sub)
	}
	// Full: the waiting list is offered, and sending again joins it.
	r = take("/courses/a/class", url.Values{"hire_item": {"2"}, "hire_qty": {"1"}})
	if r.Code != http.StatusConflict || !strings.Contains(r.Body.String(), "That class is full.") || !strings.Contains(r.Body.String(), `name="waitlist" value="1"`) {
		t.Fatalf("full: %d %s", r.Code, r.Body.String())
	}
	r = take("/courses/a/class", url.Values{"hire_item": {"2"}, "hire_qty": {"1"}, "waitlist": {"1"}})
	if r.Code != http.StatusSeeOther {
		t.Fatalf("waiting list: %d %s", r.Code, r.Body.String())
	}
	if sub := sealed(r.Header().Get("Location")); !sub.Waitlist || sub.DepositKobo != 0 {
		t.Fatalf("waiting: %+v", sub)
	}
	var claims int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM storefront_claims WHERE reference = $1`, refOf(r.Header().Get("Location"))).Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("a waiting name holds a place: %d %v", claims, err)
	}
	// An event that has started sells no more.
	if _, err := d.Admin.Exec(ctx, `UPDATE storefront_catalogue SET item = jsonb_set(item, '{starts}', to_jsonb(now() - interval '1 minute')) WHERE row_id = '1'`); err != nil {
		t.Fatal(err)
	}
	if r := take("/concerts/a/tickets", url.Values{"hire_item": {"1"}, "hire_qty": {"1"}}); r.Code != http.StatusConflict {
		t.Fatalf("after it started: %d", r.Code)
	}
}

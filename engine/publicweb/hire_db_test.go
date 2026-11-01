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

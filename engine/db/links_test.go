package db_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// TestBusinessLinks (decision 0061; docs/spec/business-links.md §5): no
// business reads business_links; a search lists only businesses that let
// themselves be found; a requester never tells a decline or a block from
// a request still waiting, and a blind request from no business at all;
// taskiem_link_enter is the one door, audited in both chains, narrowing
// the scope to the other side.
func TestBusinessLinks(t *testing.T) {
	d := dbtest.New(t)
	ada, dangote, cherry, hidden := d.SeedTenant(t, nil), d.SeedTenant(t, nil), d.SeedTenant(t, nil), d.SeedTenant(t, nil)
	name := func(tn uuid.UUID, n string, findable *bool) {
		t.Helper()
		if err := scoped(t, d, []uuid.UUID{tn}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE tenants SET name = $2 WHERE id = $1`, tn, n); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO link_settings (tenant_id, findable, updated_by) VALUES ($1, $2, 'test')`, tn, findable)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	yes, no := true, false
	name(ada.ID, "Ada's Kitchen", &yes)
	name(dangote.ID, "Dangote Stores", &yes)
	name(cherry.ID, "Cherry Blocks", &yes)
	name(hidden.ID, "Dangote Hidden", &no)

	as := func(tn uuid.UUID, fn func(tx pgx.Tx) error) error { return scoped(t, d, []uuid.UUID{tn}, fn) }
	search := func(tn uuid.UUID, q string) map[string]string {
		t.Helper()
		out := map[string]string{}
		if err := as(tn, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT name, state FROM taskiem_link_search($1, 10)`, q)
			if err != nil {
				return err
			}
			for rows.Next() {
				var n, s string
				if err := rows.Scan(&n, &s); err != nil {
					return err
				}
				out[n] = s
			}
			return rows.Err()
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	request := func(from, to uuid.UUID) (*uuid.UUID, error) {
		var told *uuid.UUID
		err := as(from, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT taskiem_link_request($1, NULL, NULL, NULL, 'supplier', 'user', 'u1')`, to).Scan(&told)
		})
		return told, err
	}
	type view struct {
		id    uuid.UUID
		state string
		role  string
	}
	links := func(tn uuid.UUID) map[uuid.UUID]view {
		t.Helper()
		out := map[uuid.UUID]view{}
		if err := as(tn, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT id, business, state, role FROM taskiem_links_of()`)
			if err != nil {
				return err
			}
			for rows.Next() {
				var v view
				var other uuid.UUID
				if err := rows.Scan(&v.id, &other, &v.state, &v.role); err != nil {
					return err
				}
				out[other] = v
			}
			return rows.Err()
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	answer := func(tn, link uuid.UUID, a string) error {
		return as(tn, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `SELECT taskiem_link_answer($1, $2, 'user', 'u2')`, link, a)
			return err
		})
	}

	// Search: by name, never a business that chose not to be found, never
	// the searcher itself, never by a phone number.
	if got := search(ada.ID, "dangote"); len(got) != 1 || got["Dangote Stores"] != "none" {
		t.Errorf("search: %v", got)
	}
	if got := search(ada.ID, "kitchen"); len(got) != 0 {
		t.Errorf("found itself: %v", got)
	}
	if got := search(ada.ID, "0803 123 4567"); len(got) != 0 {
		t.Errorf("a phone number searched: %v", got)
	}
	if _, err := request(ada.ID, hidden.ID); sqlState(err) != "P0002" {
		t.Errorf("a request to a business that is not found: %v", err)
	}

	// No business reads the table itself.
	var n int
	if err := as(ada.ID, func(tx pgx.Tx) error { return tx.QueryRow(ctx, `SELECT count(*) FROM business_links`).Scan(&n) }); sqlState(err) != "42501" {
		t.Errorf("business_links read directly: %d %v", n, err)
	}

	// Ada asks Dangote; Dangote is told; each sees its side.
	if told, err := request(ada.ID, dangote.ID); err != nil || told == nil || *told != dangote.ID {
		t.Fatalf("request: %v %v", told, err)
	}
	if told, err := request(ada.ID, dangote.ID); err != nil || told != nil {
		t.Errorf("asked twice: told %v %v", told, err)
	}
	l := links(ada.ID)[dangote.ID]
	if l.state != "requested" || l.role != "supplier" {
		t.Errorf("Ada's view: %+v", l)
	}
	if v := links(dangote.ID)[ada.ID]; v.state != "asked_us" || v.role != "customer" {
		t.Errorf("Dangote's view: %+v", v)
	}
	if got := search(ada.ID, "dangote"); got["Dangote Stores"] != "requested" {
		t.Errorf("search after asking: %v", got)
	}
	// Ada cannot accept her own request.
	if err := answer(ada.ID, l.id, "accept"); sqlState(err) != "55000" {
		t.Errorf("requester accepted: %v", err)
	}
	// A third business cannot answer it, or even find it.
	if err := answer(cherry.ID, l.id, "accept"); sqlState(err) != "P0002" {
		t.Errorf("stranger answered: %v", err)
	}

	// Declined: Ada still sees it waiting, as she would a block.
	if err := answer(dangote.ID, l.id, "decline"); err != nil {
		t.Fatal(err)
	}
	if v := links(ada.ID)[dangote.ID]; v.state != "requested" {
		t.Errorf("declined, Ada sees %+v", v)
	}
	if _, ok := links(dangote.ID)[ada.ID]; ok {
		t.Error("Dangote still sees the declined request")
	}

	// Cherry asks Dangote, who blocks: the same silence.
	if _, err := request(cherry.ID, dangote.ID); err != nil {
		t.Fatal(err)
	}
	cl := links(cherry.ID)[dangote.ID]
	if err := answer(dangote.ID, cl.id, "block"); err != nil {
		t.Fatal(err)
	}
	if v := links(cherry.ID)[dangote.ID]; v.state != "requested" {
		t.Errorf("blocked, Cherry sees %+v", v)
	}
	if v := links(dangote.ID)[cherry.ID]; v.state != "blocked" {
		t.Errorf("Dangote sees %+v", v)
	}
	// Asking again after a block tells no one.
	if told, err := request(cherry.ID, dangote.ID); err != nil || told != nil {
		t.Errorf("a blocked request told %v %v", told, err)
	}
	// After 30 days a declined and a blocked request read the same.
	if _, err := d.Admin.Exec(ctx, `UPDATE business_links SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if a, c := links(ada.ID)[dangote.ID], links(cherry.ID)[dangote.ID]; a.state != "not_accepted" || c.state != "not_accepted" {
		t.Errorf("after 30 days: declined %+v, blocked %+v", a, c)
	}

	// Asked again, Dangote accepts: active on both sides, audited in both.
	if told, err := request(ada.ID, dangote.ID); err != nil || told == nil {
		t.Fatalf("asked again: %v %v", told, err)
	}
	if err := answer(dangote.ID, l.id, "accept"); err != nil {
		t.Fatal(err)
	}
	if a, b := links(ada.ID)[dangote.ID], links(dangote.ID)[ada.ID]; a.state != "active" || b.state != "active" {
		t.Errorf("accepted: %+v %+v", a, b)
	}
	for _, tn := range []uuid.UUID{ada.ID, dangote.ID} {
		if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'link.accept' AND target = $2`, tn, l.id.String()).Scan(&n); err != nil || n != 1 {
			t.Errorf("accept audited in %v: %d %v", tn, n, err)
		}
	}
	if got := search(ada.ID, "dangote"); got["Dangote Stores"] != "active" {
		t.Errorf("search when linked: %v", got)
	}

	// The door: Ada enters Dangote's side and sees only Dangote's rows.
	var seen []uuid.UUID
	if err := as(ada.ID, func(tx pgx.Tx) error {
		var other uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT taskiem_link_enter($1, 'user', 'u1', 'link.test', '{}')`, l.id).Scan(&other); err != nil {
			return err
		}
		if other != dangote.ID {
			t.Errorf("entered %v", other)
		}
		rows, err := tx.Query(ctx, `SELECT DISTINCT tenant_id FROM workflows`)
		if err != nil {
			return err
		}
		seen, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != dangote.ID {
		t.Errorf("through the link, saw workflows of %v", seen)
	}
	var actor string
	if err := d.Admin.QueryRow(ctx, `SELECT actor_id FROM audit_log WHERE tenant_id = $1 AND action = 'link.test'`, dangote.ID).Scan(&actor); err != nil ||
		actor != "link:"+ada.ID.String()+"/u1" {
		t.Errorf("Dangote's audit actor = %q %v", actor, err)
	}
	enter := func(scope []uuid.UUID, link uuid.UUID, action string) error {
		return scoped(t, d, scope, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `SELECT taskiem_link_enter($1, 'user', 'u1', $2, '{}')`, link, action)
			return err
		})
	}
	for name, c := range map[string]struct {
		scope  []uuid.UUID
		link   uuid.UUID
		action string
		code   string
	}{
		"a stranger":         {[]uuid.UUID{cherry.ID}, l.id, "link.test", "P0002"},
		"a link not active":  {[]uuid.UUID{cherry.ID}, cl.id, "link.test", "P0002"},
		"from a wider scope": {[]uuid.UUID{ada.ID, cherry.ID}, l.id, "link.test", "42501"},
		"an action not link": {[]uuid.UUID{ada.ID}, l.id, "invoice.void", "22023"},
	} {
		if err := enter(c.scope, c.link, c.action); sqlState(err) != c.code {
			t.Errorf("%s: %v, want %s", name, err, c.code)
		}
	}

	// Ended by either side: the door shuts.
	if err := answer(ada.ID, l.id, "end"); err != nil {
		t.Fatal(err)
	}
	if err := enter([]uuid.UUID{dangote.ID}, l.id, "link.test"); sqlState(err) != "P0002" {
		t.Errorf("entered an ended link: %v", err)
	}
	if v := links(dangote.ID)[ada.ID]; v.state != "ended" {
		t.Errorf("Dangote sees %+v", v)
	}
}

// TestBlindLinkRequest: a request by phone or address tells the caller
// nothing; it reaches the business only when the number is its owner's
// linked number or its own WhatsApp number, and the requester sees it only
// once accepted.
func TestBlindLinkRequest(t *testing.T) {
	d := dbtest.New(t)
	ada, dangote := d.SeedTenant(t, nil), d.SeedTenant(t, nil)
	owner := uuid.Must(uuid.NewV7())
	const phone = "+2348031234567"
	if err := scoped(t, d, []uuid.UUID{dangote.ID}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, email) VALUES ($1, 'd@dangote.test')`, owner); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'owner')`, dangote.ID, owner)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `INSERT INTO whatsapp_bindings (user_id, number) VALUES ($1, $2)`, owner, phone); err != nil {
		t.Fatal(err)
	}
	blind := func(p string) (*uuid.UUID, error) {
		var told *uuid.UUID
		err := scoped(t, d, []uuid.UUID{ada.ID}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT taskiem_link_request(NULL, NULL, $1, NULL, 'supplier', 'user', 'u1')`, p).Scan(&told)
		})
		return told, err
	}
	if told, err := blind("+2348039999999"); err != nil || told != nil {
		t.Errorf("no one behind the number: %v %v", told, err)
	}
	told, err := blind(phone)
	if err != nil || told == nil || *told != dangote.ID {
		t.Fatalf("the owner's number: %v %v", told, err)
	}
	if n := count(t, d, []uuid.UUID{ada.ID}, `SELECT count(*) FROM taskiem_links_of()`); n != 0 {
		t.Errorf("a blind request shows to its requester: %d", n)
	}
	var link uuid.UUID
	var state string
	if err := scoped(t, d, []uuid.UUID{dangote.ID}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, state FROM taskiem_links_of()`).Scan(&link, &state)
	}); err != nil || state != "asked_us" {
		t.Fatalf("Dangote: %v %v", state, err)
	}
	if err := scoped(t, d, []uuid.UUID{dangote.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_link_answer($1, 'accept', 'user', 'u2')`, link)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, d, []uuid.UUID{ada.ID}, `SELECT count(*) FROM taskiem_links_of() WHERE state = 'active'`); n != 1 {
		t.Errorf("accepted, Ada sees %d active", n)
	}
}

// TestLinkSearchByWhatIsSold: a published storefront is findable by
// default, by its card's area and by its published items, with their
// prices; every word must match; turning findability off hides it.
func TestLinkSearchByWhatIsSold(t *testing.T) {
	d := dbtest.New(t)
	ada, cherry := d.SeedTenant(t, nil), d.SeedTenant(t, nil)
	sf := uuid.Must(uuid.NewV7())
	if _, err := d.Admin.Exec(ctx, `UPDATE tenants SET name = 'Cherry Blocks Ltd' WHERE id = $1`, cherry.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `INSERT INTO storefronts (id, tenant_id, slug, status, published_version, published_slug, published_at, created_by)
		VALUES ($1, $2, 'cherry-blocks', 'published', 1, 'cherry-blocks', now(), 'test')`, sf, cherry.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `INSERT INTO storefront_snapshots (storefront_id, version, tenant_id, model, hash, created_by)
		VALUES ($1, 1, $2, '{"card": {"name": "Cherry Blocks", "area": "Ikeja", "tagline": "Blocks and sand"}}', sha256('m'), 'test')`, sf, cherry.ID); err != nil {
		t.Fatal(err)
	}
	for i, item := range []string{`{"name": "9 inch blocks", "price_kobo": 65000, "category": "Building", "available": true}`,
		`{"name": "Sharp sand", "price_kobo": 9000000, "available": false}`} {
		if _, err := d.Admin.Exec(ctx, `INSERT INTO storefront_catalogue (storefront_id, row_id, tenant_id, position, item) VALUES ($1, $2, $3, $4, $5)`,
			sf, uuid.NewString(), cherry.ID, i, item); err != nil {
			t.Fatal(err)
		}
	}
	type hit struct {
		name, area, slug string
		items            []map[string]any
	}
	search := func(q string) []hit {
		t.Helper()
		var out []hit
		if err := scoped(t, d, []uuid.UUID{ada.ID}, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT name, COALESCE(area, ''), COALESCE(slug, ''), items FROM taskiem_link_search($1, 10)`, q)
			if err != nil {
				return err
			}
			for rows.Next() {
				var h hit
				if err := rows.Scan(&h.name, &h.area, &h.slug, &h.items); err != nil {
					return err
				}
				out = append(out, h)
			}
			return rows.Err()
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	got := search("blocks in ikeja")
	if len(got) != 1 || got[0].name != "Cherry Blocks" || got[0].area != "Ikeja" || got[0].slug != "cherry-blocks" {
		t.Fatalf("blocks in Ikeja: %+v", got)
	}
	if len(got[0].items) != 1 || got[0].items[0]["name"] != "9 inch blocks" || got[0].items[0]["price_kobo"] != float64(65000) {
		t.Errorf("items: %+v", got[0].items)
	}
	if got := search("building"); len(got) != 1 {
		t.Errorf("by category: %+v", got)
	}
	// An item no longer available finds the business by name alone, not
	// by the item.
	if got := search("sharp sand"); len(got) != 0 {
		t.Errorf("an unavailable item: %+v", got)
	}
	if got := search("blocks lekki"); len(got) != 0 {
		t.Errorf("every word must match: %+v", got)
	}
	if err := scoped(t, d, []uuid.UUID{cherry.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO link_settings (tenant_id, findable, updated_by) VALUES ($1, false, 'test')`, cherry.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := search("blocks"); len(got) != 0 {
		t.Errorf("found after turning it off: %+v", got)
	}
}

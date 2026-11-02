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

// TestLinkRequestPrivacy (docs/spec/business-links.md §5.1, §13; migration
// 00287): what a request may not reveal. A blind request to a business the
// caller already sees leaves its row alone; a request by search after a
// blind one shows as sent whether or not the blind one reached anyone;
// the daily quota counts no blind request; the side blocked cannot lift
// the block by blocking back or unblocking; and asking again a business
// that blocked us leaves our clock as asking one that declined does.
func TestLinkRequestPrivacy(t *testing.T) {
	d := dbtest.New(t)
	ada, dangote, cherry, bisi := d.SeedTenant(t, nil), d.SeedTenant(t, nil), d.SeedTenant(t, nil), d.SeedTenant(t, nil)
	phones := map[uuid.UUID]string{dangote.ID: "+2348031110001", cherry.ID: "+2348031110002", bisi.ID: "+2348031110003"}
	if err := scoped(t, d, []uuid.UUID{ada.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO link_settings (tenant_id, findable, updated_by) VALUES ($1, true, 'test')`, ada.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for tn, phone := range phones {
		owner := uuid.Must(uuid.NewV7())
		if err := scoped(t, d, []uuid.UUID{tn}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, email) VALUES ($1, $2)`, owner, owner.String()+"@links.test"); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'owner')`, tn, owner); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO link_settings (tenant_id, findable, updated_by) VALUES ($1, true, 'test')`, tn)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Admin.Exec(ctx, `INSERT INTO whatsapp_bindings (user_id, number) VALUES ($1, $2)`, owner, phone); err != nil {
			t.Fatal(err)
		}
	}
	as := func(tn uuid.UUID, fn func(tx pgx.Tx) error) error { return scoped(t, d, []uuid.UUID{tn}, fn) }
	request := func(from, to uuid.UUID) *uuid.UUID {
		t.Helper()
		var told *uuid.UUID
		if err := as(from, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT taskiem_link_request($1, NULL, NULL, NULL, 'supplier', 'user', 'u1')`, to).Scan(&told)
		}); err != nil {
			t.Fatal(err)
		}
		return told
	}
	blind := func(from uuid.UUID, phone string) *uuid.UUID {
		t.Helper()
		var told *uuid.UUID
		if err := as(from, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT taskiem_link_request(NULL, NULL, $1, NULL, 'supplier', 'user', 'u1')`, phone).Scan(&told)
		}); err != nil {
			t.Fatal(err)
		}
		return told
	}
	view := func(me, other uuid.UUID) string {
		t.Helper()
		state := "(none)"
		if err := as(me, func(tx pgx.Tx) error {
			err := tx.QueryRow(ctx, `SELECT state FROM taskiem_links_of() WHERE business = $1`, other).Scan(&state)
			if err == pgx.ErrNoRows {
				return nil
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return state
	}
	linkID := func(me, other uuid.UUID) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := as(me, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT id FROM taskiem_links_of() WHERE business = $1`, other).Scan(&id)
		}); err != nil {
			t.Fatalf("the link of %v with %v: %v", me, other, err)
		}
		return id
	}
	answer := func(tn, link uuid.UUID, a string) error {
		return as(tn, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `SELECT taskiem_link_answer($1, $2, 'user', 'u2')`, link, a)
			return err
		})
	}
	// row is the stored row's privacy-bearing fields, read as the operator.
	type row struct {
		how, status          string
		requestedAt, expires string
		hidden               bool
	}
	rowOf := func(a, b uuid.UUID) row {
		t.Helper()
		var r row
		if err := d.Admin.QueryRow(ctx, `SELECT how, status, requested_at::text, expires_at::text, requester_hidden FROM business_links
			WHERE a_tenant = LEAST($1::uuid, $2::uuid) AND b_tenant = GREATEST($1::uuid, $2::uuid)`, a, b).Scan(&r.how, &r.status, &r.requestedAt, &r.expires, &r.hidden); err != nil {
			t.Fatal(err)
		}
		return r
	}
	toldOf := func(tn uuid.UUID) int {
		t.Helper()
		var n int
		if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'link.requested'`, tn).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// 1. A row the requester sees is never touched by a blind request:
	// while it waits, and once linked.
	if told := request(ada.ID, dangote.ID); told == nil {
		t.Fatal("Dangote not told")
	}
	before := rowOf(ada.ID, dangote.ID)
	if told := blind(ada.ID, phones[dangote.ID]); told != nil {
		t.Errorf("a blind request to a business already asked told %v", told)
	}
	if after := rowOf(ada.ID, dangote.ID); after != before || view(ada.ID, dangote.ID) != "requested" {
		t.Errorf("a blind request moved a visible row: %+v -> %+v, Ada sees %s", before, after, view(ada.ID, dangote.ID))
	}
	if err := answer(dangote.ID, linkID(dangote.ID, ada.ID), "accept"); err != nil {
		t.Fatal(err)
	}
	before = rowOf(ada.ID, dangote.ID)
	if told := blind(ada.ID, phones[dangote.ID]); told != nil || rowOf(ada.ID, dangote.ID) != before || view(ada.ID, dangote.ID) != "active" {
		t.Errorf("a blind request to a linked business: told %v, %+v -> %+v", told, before, rowOf(ada.ID, dangote.ID))
	}
	if n := toldOf(dangote.ID); n != 1 {
		t.Errorf("Dangote told %d times", n)
	}

	// 2. A request by search where a blind one stands shows as sent, as it
	// does where none did, and tells the business once.
	if told := blind(ada.ID, phones[cherry.ID]); told == nil || *told != cherry.ID {
		t.Fatalf("Cherry's number: told %v", told)
	}
	if v := view(ada.ID, cherry.ID); v != "(none)" {
		t.Fatalf("a blind request shows to its requester as %s", v)
	}
	if told := request(ada.ID, cherry.ID); told != nil {
		t.Errorf("Cherry told twice: %v", told)
	}
	if told := request(ada.ID, bisi.ID); told == nil {
		t.Errorf("Bisi not told")
	}
	if c, b := view(ada.ID, cherry.ID), view(ada.ID, bisi.ID); c != "requested" || b != "requested" {
		t.Errorf("after a blind request (Cherry) and none (Bisi), the search requests read %s and %s", c, b)
	}
	if r := rowOf(ada.ID, cherry.ID); r.how != "search" || r.status != "requested" {
		t.Errorf("the blind row became %+v", r)
	}
	if n := toldOf(cherry.ID); n != 1 {
		t.Errorf("Cherry told %d times", n)
	}
	// The same where the blind request was blocked.
	if told := blind(bisi.ID, phones[cherry.ID]); told == nil {
		t.Fatal("Cherry not told of Bisi's blind request")
	}
	if err := answer(cherry.ID, linkID(cherry.ID, bisi.ID), "block"); err != nil {
		t.Fatal(err)
	}
	if told := request(bisi.ID, cherry.ID); told != nil {
		t.Errorf("Cherry told of a blocked requester: %v", told)
	}
	if v := view(bisi.ID, cherry.ID); v != "requested" {
		t.Errorf("blocked after a blind request, Bisi's search request reads %s", v)
	}
	if v := view(cherry.ID, bisi.ID); v != "blocked" {
		t.Errorf("Cherry's block became %s", v)
	}

	// 3. The quota counts no blind request: twenty blind rows leave a
	// request by search free; twenty by search refuse the next.
	var partners []uuid.UUID
	for range 20 {
		p := d.SeedTenant(t, nil)
		partners = append(partners, p.ID)
		if _, err := d.Admin.Exec(ctx, `INSERT INTO business_links (id, a_tenant, b_tenant, status, requested_by, requester_actor, how, role, expires_at)
			VALUES (gen_random_uuid(), LEAST($1::uuid, $2::uuid), GREATEST($1::uuid, $2::uuid), 'requested', $1, 'user:u1', 'blind', 'both', now() + interval '30 days')`, cherry.ID, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	if told := request(cherry.ID, dangote.ID); told == nil {
		t.Error("twenty blind requests counted against a request by search")
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE business_links SET how = 'search' WHERE requested_by = $1 AND (a_tenant = ANY($2) OR b_tenant = ANY($2))`, cherry.ID, partners); err != nil {
		t.Fatal(err)
	}
	if err := as(cherry.ID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_link_request($1, NULL, NULL, NULL, 'supplier', 'user', 'u1')`, ada.ID)
		return err
	}); sqlState(err) != "53400" {
		t.Errorf("the twenty-first request by search: %v", err)
	}
	if _, err := d.Admin.Exec(ctx, `DELETE FROM business_links WHERE a_tenant = ANY($1) OR b_tenant = ANY($1)`, partners); err != nil {
		t.Fatal(err)
	}

	// 4. A block is each side's own. Dangote blocks the link it made with
	// Ada; Ada reads it as ended and may block too, which neither lifts
	// Dangote's block nor tells Dangote; her unblock lifts only hers; her
	// later request is dropped.
	ld := linkID(dangote.ID, ada.ID)
	if err := answer(dangote.ID, ld, "block"); err != nil {
		t.Fatal(err)
	}
	if v := view(ada.ID, dangote.ID); v != "ended" {
		t.Fatalf("blocked by Dangote, Ada sees %s", v)
	}
	if err := answer(ada.ID, ld, "block"); err != nil {
		t.Fatalf("Ada's block: %v", err)
	}
	if a, dg := view(ada.ID, dangote.ID), view(dangote.ID, ada.ID); a != "blocked" || dg != "blocked" {
		t.Errorf("both blocked: Ada sees %s, Dangote %s", a, dg)
	}
	if err := answer(ada.ID, ld, "unblock"); err != nil {
		t.Fatalf("Ada's unblock: %v", err)
	}
	if a, dg := view(ada.ID, dangote.ID), view(dangote.ID, ada.ID); a != "ended" || dg != "blocked" {
		t.Errorf("Ada unblocked: Ada sees %s, Dangote %s", a, dg)
	}
	if err := answer(ada.ID, ld, "unblock"); sqlState(err) != "55000" {
		t.Errorf("Ada unblocked again: %v", err)
	}
	told := toldOf(dangote.ID)
	if got := request(ada.ID, dangote.ID); got != nil || toldOf(dangote.ID) != told {
		t.Errorf("a request from a blocked business reached Dangote: %v", got)
	}
	if a, dg := view(ada.ID, dangote.ID), view(dangote.ID, ada.ID); a != "requested" || dg != "blocked" {
		t.Errorf("asked a business that blocked us: Ada sees %s, Dangote %s", a, dg)
	}
	// With both sides blocking, the side that unblocks first still reads
	// the other's block as a link that ended; the other's stands.
	if err := answer(ada.ID, ld, "block"); err == nil {
		t.Error("Ada blocked a request of her own")
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE business_links SET a_blocked = true, b_blocked = true WHERE id = $1`, ld); err != nil {
		t.Fatal(err)
	}
	if err := answer(dangote.ID, ld, "unblock"); err != nil {
		t.Fatal(err)
	}
	if a, dg := view(ada.ID, dangote.ID), view(dangote.ID, ada.ID); a != "blocked" || dg != "(none)" {
		t.Errorf("Dangote unblocked while Ada blocks: Ada sees %s, Dangote %s", a, dg)
	}
	if got := request(dangote.ID, ada.ID); got != nil {
		t.Errorf("a request from Dangote reached Ada, who blocks it: %v", got)
	}

	// 5. Asked again within the 30 days, a business that declined and one
	// that blocked keep the same clock; after them, both start again.
	if err := answer(bisi.ID, linkID(bisi.ID, ada.ID), "decline"); err != nil {
		t.Fatal(err)
	}
	if err := answer(cherry.ID, linkID(cherry.ID, ada.ID), "block"); err != nil {
		t.Fatal(err)
	}
	declined, blocked := rowOf(ada.ID, bisi.ID), rowOf(ada.ID, cherry.ID)
	request(ada.ID, bisi.ID)
	request(ada.ID, cherry.ID)
	if d2, b2 := rowOf(ada.ID, bisi.ID), rowOf(ada.ID, cherry.ID); d2.expires != declined.expires || b2.expires != blocked.expires {
		t.Errorf("asked again within 30 days: declined %s -> %s, blocked %s -> %s", declined.expires, d2.expires, blocked.expires, b2.expires)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE business_links SET expires_at = now() - interval '1 minute' WHERE requested_by = $1`, ada.ID); err != nil {
		t.Fatal(err)
	}
	if b, c := view(ada.ID, bisi.ID), view(ada.ID, cherry.ID); b != "not_accepted" || c != "not_accepted" {
		t.Errorf("after 30 days: declined %s, blocked %s", b, c)
	}
	request(ada.ID, bisi.ID)
	request(ada.ID, cherry.ID)
	d2, b2 := rowOf(ada.ID, bisi.ID), rowOf(ada.ID, cherry.ID)
	if d2.expires == declined.expires || b2.expires == blocked.expires || view(ada.ID, bisi.ID) != "requested" || view(ada.ID, cherry.ID) != "requested" {
		t.Errorf("asked again after 30 days: declined %+v, blocked %+v", d2, b2)
	}
	if c := view(cherry.ID, ada.ID); c != "blocked" {
		t.Errorf("Cherry's block after Ada asked again: %s", c)
	}
}

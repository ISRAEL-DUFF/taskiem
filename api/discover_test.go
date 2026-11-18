package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/israel-duff/taskiem/engine/webauthn"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/discover"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// discoverNames runs a search as c and answers the names found, in order.
func discoverNames(t *testing.T, c *client, body map[string]any) []string {
	t.Helper()
	out := []string{}
	for _, r := range c.must(200, "POST", "/v1/discover/search", body)["results"].([]any) {
		out = append(out, r.(map[string]any)["name"].(string))
	}
	return out
}

func discoverResults(t *testing.T, c *client, body map[string]any) []map[string]any {
	t.Helper()
	out := []map[string]any{}
	for _, r := range c.must(200, "POST", "/v1/discover/search", body)["results"].([]any) {
		out = append(out, r.(map[string]any))
	}
	return out
}

func sameNames(got []string, want ...string) bool {
	return fmt.Sprint(got) == fmt.Sprint(want)
}

// tick runs Discover's sweep (the index and the one-off notice) as the
// server's job does every minute.
func (w *doorShopWorld) discoverTick(t *testing.T) {
	t.Helper()
	if err := w.srv.DiscoverTick(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Discover (taskiem-for-everyone §10): the setting and its defaults, a
// listing that lacks a category, an area, a photo or hours is not shown
// (and the settings say what is missing), a workflows workspace is never
// listed; search by name, by what a business sells and by area, the
// nearest first when the person gives their place (kept nowhere); a
// business with chats off is listed without Chat; Chat from Discover
// opens a business with no shop or link through the customer door.
func TestDiscover(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	withChatLinks(w.world)
	mamas := w.owner

	// A business with a published storefront is listed by default, but
	// its listing lacks a category, an area and a photo: not shown.
	st := mamas.must(200, "GET", "/v1/settings/discover", nil)
	if st["listed"] != true || st["default"] != true || st["chosen"] != nil || st["shown"] != false {
		t.Fatalf("a published storefront's default: %v", st)
	}
	if fmt.Sprint(st["missing"]) != "[category area photo]" ||
		st["missing_line"] != "Customers can't find you in Discover until you add a category, an area and a photo." {
		t.Fatalf("what is missing: %v %v", st["missing"], st["missing_line"])
	}
	tolu, _ := w.personIn(t, "+2348031110031")
	if got := discoverNames(t, tolu, map[string]any{"q": "Mama"}); len(got) != 0 {
		t.Fatalf("an incomplete listing was found: %v", got)
	}

	// The profile gives the area and the category, the logo the photo;
	// the hours are the published page's. Now it is shown.
	mamas.must(200, "PUT", "/v1/business-profile", map[string]any{"area": "Yaba", "categories": []string{"food"}})
	if s, out := mamas.rawPost(t, "/v1/business-profile/logo", "image/jpeg", photoN(t, 0)); s != 200 {
		t.Fatalf("logo: %d %v", s, out)
	}
	w.discoverTick(t)
	st = mamas.must(200, "GET", "/v1/settings/discover", nil)
	if st["shown"] != true || len(st["missing"].([]any)) != 0 {
		t.Fatalf("a complete listing: %v", st)
	}

	// Another business with no storefront: off until it turns it on, and
	// it gives its own category, area and hours.
	bola := w.tenant(t, "Bola Bakes", "bola@bola.test")
	bolaID := tenantUUID(t, bola)
	bola.must(200, "PUT", "/v1/business-profile", map[string]any{"tagline": "Birthday cake, party dress and bus hire"})
	st = bola.must(200, "GET", "/v1/settings/discover", nil)
	if st["listed"] != false || st["default"] != false {
		t.Fatalf("no storefront: %v", st)
	}
	bola.must(400, "PUT", "/v1/settings/discover", map[string]any{"category": "cakes"})
	st = bola.must(200, "PUT", "/v1/settings/discover", map[string]any{"listed": true, "category": "food", "area": "Ikeja GRA", "hours": hoursOpen(true)})
	if st["listed"] != true || fmt.Sprint(st["missing"]) != "[photo]" || st["shown"] != false {
		t.Fatalf("Bola's listing without a photo: %v", st)
	}
	if s, out := bola.rawPost(t, "/v1/business-profile/logo", "image/jpeg", photoN(t, 1)); s != 200 {
		t.Fatalf("logo: %d %v", s, out)
	}
	w.discoverTick(t)
	if st = bola.must(200, "GET", "/v1/settings/discover", nil); st["shown"] != true {
		t.Fatalf("Bola's listing: %v", st)
	}
	var audited int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'discover.settings'`, bolaID).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("settings audited: %d %v", audited, err)
	}

	// A third business with a published page that turned Discover off is
	// never found, by name or by what it sells.
	ada := w.tenant(t, "Ada Rice Mill", "ada@ada.test")
	verifyPhone(t, w.world, ada, "phone", "business")
	ada.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-rice", "draft": map[string]any{"name": "Ada Rice Mill", "area": "Yaba", "hours": hoursOpen(true)}})
	ada.must(200, "POST", "/v1/storefront/publish", nil)
	ada.must(200, "PUT", "/v1/business-profile", map[string]any{"categories": []string{"food"}})
	ada.rawPost(t, "/v1/business-profile/logo", "image/jpeg", photoN(t, 2))
	ada.must(200, "PUT", "/v1/settings/discover", map[string]any{"listed": false})
	w.discoverTick(t)

	// Workflows workspaces are never listed.
	again := w.login(t, "bola@bola.test", "correct horse battery")
	started := again.must(201, "POST", "/v1/me/start", map[string]any{"kind": "workflows", "name": "Bola's flows"})
	ws := &client{t: t, base: w.base, token: started["token"].(string)}
	ws.must(409, "PUT", "/v1/settings/discover", map[string]any{"listed": true})
	if st = ws.must(200, "GET", "/v1/settings/discover", nil); st["listed"] != false || st["workspace"] != true {
		t.Fatalf("a workspace: %v", st)
	}

	// Search: by name, by what it sells (an item, a plural), by area, by
	// category; a phone number is never searched.
	if got := discoverNames(t, tolu, map[string]any{"q": "mama"}); !sameNames(got, "Mama's Kitchen") {
		t.Fatalf("by name: %v", got)
	}
	if got := discoverNames(t, tolu, map[string]any{"q": "rice"}); !sameNames(got, "Mama's Kitchen") {
		t.Fatalf("by what it sells (Ada is off): %v", got)
	}
	if got := discoverNames(t, tolu, map[string]any{"q": "jollof rices"}); !sameNames(got, "Mama's Kitchen") {
		t.Fatalf("a plural: %v", got)
	}
	if got := discoverNames(t, tolu, map[string]any{"area": "ikeja"}); !sameNames(got, "Bola Bakes") {
		t.Fatalf("by area: %v", got)
	}
	if got := discoverNames(t, tolu, map[string]any{"category": "food"}); !sameNames(got, "Bola Bakes", "Mama's Kitchen") {
		t.Fatalf("by category: %v", got)
	}
	if got := discoverNames(t, tolu, map[string]any{"q": "0803 111 0031"}); len(got) != 0 {
		t.Fatalf("a phone was searched: %v", got)
	}
	// Plurals: "es" after s, x, z, ch or sh goes too, an s alone, and the
	// word as typed is always tried.
	for _, q := range []string{"dresses", "buses", "cakes", "cake", "birthday cakes", "dress"} {
		if got := discoverNames(t, tolu, map[string]any{"q": q}); !sameNames(got, "Bola Bakes") {
			t.Fatalf("%q: %v", q, got)
		}
	}
	// Words that are all left out find nothing, not everything; "shop"
	// and "buy" are searched like any word.
	for _, q := range []string{"the", "a to the", "near me", "shop", "buy"} {
		if got := discoverNames(t, tolu, map[string]any{"q": q}); len(got) != 0 {
			t.Fatalf("%q found %v", q, got)
		}
	}
	if got := discoverNames(t, tolu, map[string]any{"q": "", "category": "food"}); len(got) != 2 {
		t.Fatalf("no words: %v", got)
	}
	// Paging stops at 200.
	tolu.must(400, "POST", "/v1/discover/search", map[string]any{"offset": 220})
	tolu.must(400, "POST", "/v1/discover/search", map[string]any{"offset": -1})
	if out := tolu.must(200, "POST", "/v1/discover/search", map[string]any{"offset": 200}); out["more"] != false {
		t.Fatalf("the last page: %v", out)
	}
	tolu.must(400, "POST", "/v1/discover/search", map[string]any{"category": "weapons"})
	res := discoverResults(t, tolu, map[string]any{"q": "mama"})[0]
	if res["category_name"] != "Food and drinks" || res["area"] != "Yaba" || res["open_now"] != true || res["can_chat"] != true ||
		res["photo"] == nil || !strings.HasPrefix(res["line"].(string), "Open now") || res["km"] != nil {
		t.Fatalf("a result: %v", res)
	}

	// Near first: each business gives its place; the person's place comes
	// with the search and is kept nowhere.
	mamas.must(200, "PUT", "/v1/settings/discover", map[string]any{"location": map[string]any{"lat": 6.5095, "lng": 3.3711}})
	bola.must(200, "PUT", "/v1/settings/discover", map[string]any{"location": map[string]any{"lat": 6.5833, "lng": 3.3500}})
	near := discoverResults(t, tolu, map[string]any{"near": map[string]any{"lat": 6.58791, "lng": 3.35237}})
	if len(near) != 2 || near[0]["name"] != "Bola Bakes" || near[1]["name"] != "Mama's Kitchen" || near[0]["km"].(float64) > near[1]["km"].(float64) {
		t.Fatalf("near Ikeja: %v", near)
	}
	near = discoverResults(t, tolu, map[string]any{"near": map[string]any{"lat": 6.50812, "lng": 3.37233}})
	if near[0]["name"] != "Mama's Kitchen" {
		t.Fatalf("near Yaba: %v", near)
	}
	for _, q := range []string{
		`SELECT count(*) FROM audit_log WHERE detail::text LIKE '%6.587%' OR detail::text LIKE '%6.508%'`,
		`SELECT count(*) FROM discover_index WHERE lat::text LIKE '6.587%' OR lat::text LIKE '6.508%'`,
		`SELECT count(*) FROM discover_settings WHERE lat::text LIKE '6.587%' OR lat::text LIKE '6.508%'`,
		// The people chats' own index (people_search, 00479) keeps no
		// searches, only keyed tokens of a person's own messages.
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name LIKE '%search%' AND table_name NOT LIKE 'people\_search%'`,
	} {
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, q).Scan(&n); err != nil || n != 0 {
			t.Fatalf("the person's place or the search was kept (%s): %d %v", q, n, err)
		}
	}
	tolu.must(400, "POST", "/v1/discover/search", map[string]any{"near": map[string]any{"lat": 91, "lng": 3}})

	// A business's profile: photos, hours, address, what it offers; and
	// the person's own chat with it once they have one.
	prof := tolu.must(200, "GET", "/v1/discover/"+w.biz.String(), nil)
	if prof["name"] != "Mama's Kitchen" || prof["has_shop"] != true || prof["can_chat"] != true || len(prof["photos"].([]any)) != 1 ||
		prof["hours"] == nil || prof["shop"] == "" || prof["chat"] != nil {
		t.Fatalf("Mama's profile: %v", prof)
	}
	if out := tolu.must(404, "GET", "/v1/discover/"+tenantUUID(t, ada).String(), nil); out["error"] != "This business is not in Discover any more." {
		t.Fatalf("a listing that is gone: %v", out)
	}
	tolu.must(404, "GET", "/v1/discover/"+uuid.NewString(), nil)

	// Chat from Discover: Bola has no shop, no chat link and no order of
	// the person's; its listing opens the door.
	ade, _ := w.personIn(t, "+2348031110032")
	bprof := ade.must(200, "GET", "/v1/discover/"+bolaID.String(), nil)
	if bprof["has_shop"] != false || bprof["has_book"] != false || bprof["can_chat"] != true || bprof["shop"] != nil {
		t.Fatalf("Bola's profile: %v", bprof)
	}
	chat := ade.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": bolaID, "from": "discover"})
	if chat["name"] != "Bola Bakes" {
		t.Fatalf("a chat from Discover: %v", chat)
	}
	if prof = ade.must(200, "GET", "/v1/discover/"+bolaID.String(), nil); prof["chat"] != chat["id"] {
		t.Fatalf("the profile's chat: %v", prof)
	}
	var via int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'customer.start' AND detail->>'via' = 'discover'`, bolaID).
		Scan(&via); err != nil || via != 1 {
		t.Fatalf("the door's entry: %d %v", via, err)
	}

	// Off: not found, and the door no longer opens from the listing.
	bola.must(200, "PUT", "/v1/settings/discover", map[string]any{"listed": false})
	if got := discoverNames(t, tolu, map[string]any{"q": "bola"}); len(got) != 0 {
		t.Fatalf("turned off, still found: %v", got)
	}
	tolu.must(404, "POST", "/v1/me/chats", map[string]any{"business_id": bolaID, "from": "discover"})
	bola.must(200, "PUT", "/v1/settings/discover", map[string]any{"listed": true})

	// A listing that loses its area is hidden, and its settings say why.
	st = bola.must(200, "PUT", "/v1/settings/discover", map[string]any{"area": ""})
	if st["shown"] != false || fmt.Sprint(st["missing"]) != "[area]" {
		t.Fatalf("no area: %v", st)
	}
	if got := discoverNames(t, tolu, map[string]any{"q": "bola"}); len(got) != 0 {
		t.Fatalf("no area, still found: %v", got)
	}
	bola.must(200, "PUT", "/v1/settings/discover", map[string]any{"area": "Ikeja GRA"})

	// Chats off (the Orders area off): listed without Chat, through the
	// sweep that follows the area's switch.
	bola.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"orders": false, "storefront": false}, "confirm": true})
	w.discoverTick(t)
	if r := discoverResults(t, tolu, map[string]any{"q": "bola"}); len(r) != 1 || r[0]["can_chat"] != false {
		t.Fatalf("orders off: %v", r)
	}
	if st = bola.must(200, "GET", "/v1/settings/discover", nil); st["chat_off"] != "orders_off" {
		t.Fatalf("why no chat: %v", st)
	}
	tolu.must(404, "POST", "/v1/me/chats", map[string]any{"business_id": bolaID, "from": "discover"})

	// An API key is not a person.
	key := mamas.must(201, "POST", "/v1/api-keys", map[string]any{"name": "k", "permissions": []string{"order.handle"}})["key"].(string)
	keyed := &client{t: t, base: w.base, token: key}
	keyed.must(403, "POST", "/v1/discover/search", map[string]any{"q": "mama"})
}

// The narrow definers: they answer listing columns only, and only listed
// businesses; the index cannot be read across businesses any other way.
func TestDiscoverNarrowFunctions(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	w.owner.must(200, "PUT", "/v1/business-profile", map[string]any{"area": "Yaba", "categories": []string{"food"}})
	w.owner.rawPost(t, "/v1/business-profile/logo", "image/jpeg", photoN(t, 0))
	w.discoverTick(t)
	other := w.tenant(t, "Other Shop", "other@other.test")
	other.must(200, "PUT", "/v1/settings/discover", map[string]any{"listed": false})

	for fn, want := range map[string]string{
		"taskiem_discover_search(text,text,text,double precision,double precision,integer,integer)": "TABLE(business uuid, name text, category text, area text, photo jsonb, hours jsonb, can_chat boolean, km double precision)",
		"taskiem_discover_profile(uuid)": "TABLE(business uuid, name text, tagline text, category text, area text, address text, map_link text, photo jsonb, photos jsonb, " +
			"hours jsonb, shop text, can_chat boolean, has_shop boolean, has_book boolean)",
	} {
		var got string
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT pg_get_function_result($1::regprocedure)`, fn).Scan(&got); err != nil || got != want {
			t.Fatalf("%s answers %q (%v), want %q", fn, got, err, want)
		}
	}
	// As the application, in another business's scope: the search finds
	// only the listed business; the index's rows are not readable.
	tx, err := w.env.DB.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_scope', $1, true)`, "{"+tenantOf(t, other)+"}"); err != nil {
		t.Fatal(err)
	}
	results, _, err := discover.Search(ctx, tx, discover.Query{}, time.Now())
	if err != nil || len(results) != 1 || results[0].Business != w.biz {
		t.Fatalf("the search from another business: %+v %v", results, err)
	}
	var rows int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM discover_index`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("another business read the index: %d %v", rows, err)
	}
	if _, err := discover.ProfileOf(ctx, tx, tenantUUID(t, other), time.Now()); err != discover.ErrNotFound {
		t.Fatalf("an unlisted business's profile: %v", err)
	}
}

// shipNow records that Discover shipped now: the businesses whose page is
// published by then are due the one-off notice (§10).
func (w *doorShopWorld) shipNow(t *testing.T) {
	t.Helper()
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE discover_launch SET shipped_at = clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
}

// aged makes a person's account eight days old, so their reports count.
func (w *doorShopWorld) aged(t *testing.T, c *client) {
	t.Helper()
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE users SET created_at = now() - interval '8 days' WHERE id = $1`, personID(t, c)); err != nil {
		t.Fatal(err)
	}
}

// Reports (§10, §15, the founder's rule): a report counts only from an
// account at least 7 days old whose earlier Discover reports were not
// dismissed twice with none acted on; three counting reports put the
// listing first in the moderators' queue, and hide it only when one says
// scam. The reports' own buttons refuse a Discover report; a moderator
// takes a listing out (the owners told) and puts it back. Discover reports
// leave Explore alone. The one-off notice goes once to a business listed
// by default, shown, whose page was published when Discover shipped;
// never to one that chose, nor to one published later.
func TestDiscoverReportsAndNotice(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	w.srv.WebAuthn = webauthn.Config{RPID: rpID, RPName: "Taskiem", Origins: []string{origin}}
	w.srv.Ops = &api.OpsSettings{}
	op, a := enrolOperator(t, w.world, "moderator@taskiem.test")
	biz := w.biz.String()
	told := func(tenant uuid.UUID) int {
		t.Helper()
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE tenant_id = $1 AND kind = $2`, tenant, discover.AlertListed).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Mama's page was published when Discover shipped; while its listing
	// is not shown (no category, area or photo), it is not told.
	w.shipNow(t)
	w.discoverTick(t)
	if n := told(w.biz); n != 0 {
		t.Fatalf("told while not shown: %d", n)
	}
	// Shown: told once, however often the sweep runs.
	w.owner.must(200, "PUT", "/v1/business-profile", map[string]any{"area": "Yaba", "categories": []string{"food"}})
	w.owner.rawPost(t, "/v1/business-profile/logo", "image/jpeg", photoN(t, 0))
	w.discoverTick(t)
	w.discoverTick(t)
	if told(w.biz) != 1 {
		t.Fatalf("told %d times", told(w.biz))
	}
	var body string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT body FROM alerts WHERE tenant_id = $1 AND kind = $2`, w.biz, discover.AlertListed).Scan(&body); err != nil ||
		body != "Your shop is now listed in Taskiem's Discover, where customers find businesses. Turn it off here." {
		t.Fatalf("the words: %q %v", body, err)
	}
	// A business that chose is never told, though its page was published
	// before Discover shipped; one published after is listed, not told.
	publish := func(name, email, slug string, n int) *client {
		c := w.tenant(t, name, email)
		verifyPhone(t, w.world, c, "phone", "business")
		c.must(200, "PUT", "/v1/storefront", map[string]any{"slug": slug, "draft": map[string]any{"name": name, "area": "Yaba", "hours": hoursOpen(true)}})
		c.must(200, "POST", "/v1/storefront/publish", nil)
		c.must(200, "PUT", "/v1/business-profile", map[string]any{"categories": []string{"food"}})
		c.rawPost(t, "/v1/business-profile/logo", "image/jpeg", photoN(t, n))
		return c
	}
	chose := publish("Chose Shop", "chose@chose.test", "chose-shop", 3)
	choseID := tenantUUID(t, chose)
	w.shipNow(t)
	chose.must(200, "PUT", "/v1/settings/discover", map[string]any{"listed": true})
	late := publish("Late Shop", "late@late.test", "late-shop", 4)
	w.discoverTick(t)
	if st := late.must(200, "GET", "/v1/settings/discover", nil); st["shown"] != true || st["chosen"] != nil {
		t.Fatalf("a page published after Discover shipped is listed by default: %v", st)
	}
	if n, m := told(choseID), told(tenantUUID(t, late)); n != 0 || m != 0 {
		t.Fatalf("told a business that chose (%d), or one published after Discover shipped (%d)", n, m)
	}

	// Explore: Mama's is findable there.
	w.owner.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	inExplore := func() bool {
		t.Helper()
		var open bool
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT taskiem_link_explore_open($1, $2)`, choseID, w.biz).Scan(&open); err != nil {
			t.Fatal(err)
		}
		return open
	}
	if !inExplore() {
		t.Fatal("Mama's is not in Explore to begin with")
	}

	person := func(n int, old bool) *client {
		p, _ := w.personIn(t, fmt.Sprintf("+2348031110%03d", 100+n))
		if old {
			w.aged(t, p)
		}
		return p
	}
	report := func(c *client, reason string) {
		t.Helper()
		if got := c.must(http.StatusAccepted, "POST", "/v1/discover/"+biz+"/report", map[string]any{"reason": reason}); got["reported"] != true {
			t.Fatalf("a report: %v", got)
		}
	}
	var looker *client
	found := func() bool {
		t.Helper()
		return len(discoverNames(t, looker, map[string]any{"q": "mama"})) == 1
	}
	queue := func() []map[string]any {
		t.Helper()
		out := []map[string]any{}
		for _, r := range op.must(200, "GET", "/v1/ops/reports", nil)["reports"].([]any) {
			out = append(out, r.(map[string]any))
		}
		return out
	}

	// Reports from new accounts reach the moderators but do not count:
	// three scam reports from accounts made today leave it shown.
	newbie := person(0, false)
	looker = newbie
	newbie.must(400, "POST", "/v1/discover/"+biz+"/report", map[string]any{"reason": "boring"})
	if out := newbie.must(404, "POST", "/v1/discover/"+uuid.NewString()+"/report", map[string]any{"reason": "scam"}); out["error"] != "This business is not in Discover any more." {
		t.Fatalf("no such listing: %v", out)
	}
	newbie.must(http.StatusAccepted, "POST", "/v1/discover/"+biz+"/report", map[string]any{"reason": "scam", "note": "Asked me to pay into a personal account"})
	report(person(1, false), "scam")
	report(person(2, false), "scam")
	if !found() {
		t.Fatal("reports from new accounts hid it")
	}
	seen := false
	for _, r := range queue() {
		seen = seen || r["kind"] == "discover" && r["tenant_id"] == biz && r["message"] == "Asked me to pay into a personal account"
	}
	if !seen {
		t.Fatal("the moderators do not see the report")
	}
	// The same person three times a day for one listing: the fourth is not
	// taken.
	newbie.must(http.StatusAccepted, "POST", "/v1/discover/"+biz+"/report", map[string]any{"reason": "scam"})
	newbie.must(http.StatusAccepted, "POST", "/v1/discover/"+biz+"/report", map[string]any{"reason": "scam"})
	newbie.must(http.StatusTooManyRequests, "POST", "/v1/discover/"+biz+"/report", map[string]any{"reason": "scam"})
	// A Discover report does not take a business out of Explore.
	if !inExplore() {
		t.Fatal("a Discover report took Mama's out of Explore")
	}

	// A newer report on a page comes first while no listing has three
	// counting reports.
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO page_reports (id, tenant_id, kind, site_id, reason, ip_hash, created_at)
		VALUES (gen_random_uuid(), $1, 'storefront', gen_random_uuid(), 'other', decode(repeat('ab', 32), 'hex'), now() + interval '1 minute')`, choseID); err != nil {
		t.Fatal(err)
	}
	if q := queue(); q[0]["kind"] != "storefront" {
		t.Fatalf("the newest first: %v", q[0])
	}

	// A reporter whose two earlier Discover reports were dismissed (and
	// none acted on) does not count: they reported Chose Shop twice, and a
	// moderator found nothing (restore dismisses a listing's reports).
	crier := person(3, true)
	for range 2 {
		crier.must(http.StatusAccepted, "POST", "/v1/discover/"+choseID.String()+"/report", map[string]any{"reason": "scam"})
	}
	op.must(200, "POST", "/v1/ops/discover/"+choseID.String()+"/restore", map[string]any{"step_up": op.stepUp(a, "ops.discover.restore", choseID.String())})
	var choseTold int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE tenant_id = $1 AND title = 'Your listing is back in Discover'`, choseID).Scan(&choseTold); err != nil || choseTold != 0 {
		t.Fatalf("a listing never taken out was told it is back: %d %v", choseTold, err)
	}
	report(crier, "scam")

	// Three counting reports, none of them scam: first in the queue, still
	// shown.
	busy := person(4, true)
	report(busy, "offensive")
	report(person(5, true), "other")
	if !found() {
		t.Fatal("two counting reports and one that does not hid it")
	}
	report(person(6, true), "impersonation")
	if !found() {
		t.Fatal("three counting reports with no scam hid it")
	}
	q := queue()
	if q[0]["kind"] != "discover" || q[0]["tenant_id"] != biz {
		t.Fatalf("a listing with three counting reports is not first in the queue: %v", q[0])
	}
	// The reports' own buttons refuse a Discover report: the listing is
	// decided on with remove or restore.
	out := op.must(http.StatusConflict, "POST", "/v1/ops/reports/"+q[0]["id"].(string)+"/dismiss", map[string]any{})
	if !strings.Contains(out["error"].(string), "/v1/ops/discover/"+biz+"/restore") {
		t.Fatalf("the refusal does not point to the listing: %v", out)
	}
	op.must(http.StatusConflict, "POST", "/v1/ops/reports/"+q[0]["id"].(string)+"/action", map[string]any{})

	// A fourth counting report says scam: hidden until a moderator looks.
	report(person(7, true), "scam")
	if found() {
		t.Fatal("three counting reports with a scam did not hide it")
	}
	if out := looker.must(404, "GET", "/v1/discover/"+biz, nil); out["error"] != "This business is not in Discover any more." {
		t.Fatalf("a hidden listing: %v", out)
	}
	if st := w.owner.must(200, "GET", "/v1/settings/discover", nil); st["hidden"] != "reported" || st["shown"] != false {
		t.Fatalf("hidden for reports: %v", st)
	}
	// Reports are still taken on a listing hidden for them.
	report(person(1, false), "other")

	// A moderator takes it out (a reason is needed), then puts it back:
	// the reports are acted on, then shown again.
	op.must(422, "POST", "/v1/ops/discover/"+biz+"/remove", map[string]any{"step_up": op.stepUp(a, "ops.discover.remove", biz)})
	op.must(200, "POST", "/v1/ops/discover/"+biz+"/remove", map[string]any{"reason": "Selling something not allowed", "step_up": op.stepUp(a, "ops.discover.remove", biz)})
	st := w.owner.must(200, "GET", "/v1/settings/discover", nil)
	if st["hidden"] != "removed" || st["removed_note"] != "Selling something not allowed" || st["shown"] != false {
		t.Fatalf("removed: %v", st)
	}
	var open int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM page_reports WHERE site_id = $1 AND status = 'open'`, w.biz).Scan(&open); err != nil || open != 0 {
		t.Fatalf("open reports after removal: %d %v", open, err)
	}
	// The owner cannot put it back by turning it on.
	w.owner.must(200, "PUT", "/v1/settings/discover", map[string]any{"listed": true})
	w.discoverTick(t)
	if found() {
		t.Fatal("a removed listing came back")
	}
	op.must(200, "POST", "/v1/ops/discover/"+biz+"/restore", map[string]any{"step_up": op.stepUp(a, "ops.discover.restore", biz)})
	if !found() {
		t.Fatal("restored, not found")
	}
	for _, action := range []string{"discover.remove", "discover.restore"} {
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = $2`, w.biz, action).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s audited %d times (%v)", action, n, err)
		}
	}

	// Twelve Discover reports in two hours per person, counted in the
	// database (not in a server's memory).
	// The one who reported "offensive" made one; eleven more as if made
	// from another server.
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO page_reports (id, tenant_id, kind, site_id, reason, ip_hash, seasoned)
		SELECT gen_random_uuid(), $1, 'discover', gen_random_uuid(), 'other', r.ip_hash, true FROM page_reports r, generate_series(1, 11)
		 WHERE r.site_id = $2 AND r.reason = 'offensive' AND r.kind = 'discover'`, choseID, w.biz); err != nil {
		t.Fatal(err)
	}
	if out := busy.must(http.StatusTooManyRequests, "POST", "/v1/discover/"+choseID.String()+"/report", map[string]any{"reason": "scam"}); out["error"] != "You have sent many reports. Try again later." {
		t.Fatalf("the twelfth report: %v", out)
	}

	// A report on Mama's page still takes it out of Explore.
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO page_reports (id, tenant_id, kind, site_id, reason, ip_hash)
		SELECT gen_random_uuid(), $1, 'storefront', s.id, 'other', decode(repeat('cd', 32), 'hex') FROM storefronts s WHERE s.tenant_id = $1`, w.biz); err != nil {
		t.Fatal(err)
	}
	if inExplore() {
		t.Fatal("a report on a page no longer takes it out of Explore")
	}
}

// "Chat with {business} about this order" (§13, asked for by the Orders
// stream): the door opens a business with no published shop for a person
// with a confirmed storefront order there, found through the phone index
// by their own verified phone; an order waiting for "These are mine"
// does not, nor does anyone else's; the index key is not audited.
func TestCustomerDoorOpensForPhoneIndexOrders(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	withChatLinks(w.world)
	bola := tenantUUID(t, w.tenant(t, "Bola Bakes", "bola@bola.test"))
	orders := &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault, PhoneIndex: w.srv.PhoneIndex}
	const number = "+2348031110051"
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{bola}, func(tx pgx.Tx) error {
		_, err := orders.PlaceChatTx(ctx, tx, bola, storefront.ChatOrder{Customer: storefront.Customer{Name: "Tolu", Phone: number},
			Lines: []storefront.Line{{ID: "1", Name: "Chocolate cake", Quantity: 1, PriceKobo: 1500000}}, Fulfilment: "pickup", Pay: storefront.ChatPayLater, By: "user:x"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	tolu, _ := w.personIn(t, number)
	other, _ := w.personIn(t, "+2348031110052")

	// Made before the phone was on the account: not theirs until they say so.
	tolu.must(404, "POST", "/v1/me/chats", map[string]any{"business_id": bola, "from": "order"})
	tolu.must(200, "POST", "/v1/me/storefront-orders/confirm", nil)
	if chat := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": bola, "from": "order"}); chat["name"] != "Bola Bakes" {
		t.Fatalf("the chat about an order: %v", chat)
	}
	other.must(404, "POST", "/v1/me/chats", map[string]any{"business_id": bola, "from": "order"})
	// A wrong key, straight to the door, finds nothing.
	if err := doorStartWith(ctx, w, personID(t, other), bola, map[string]any{"via": "order", "index_key": strings.Repeat("ab", 32)}); err == nil {
		t.Fatal("the door opened for a made-up key")
	}
	var keys int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND detail::text LIKE '%index_key%'`, bola).Scan(&keys); err != nil || keys != 0 {
		t.Fatalf("the index key in the audit: %d %v", keys, err)
	}
}

// Every source marks the sweep: a storefront's catalogue (its refresh,
// and a stock change written straight to its rows) once per statement.
// The one-off notice takes first the businesses it has not tried, then
// the longest since it tried, so one that keeps failing does not hold up
// the others.
func TestDiscoverSweepSources(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	marked := func() bool {
		t.Helper()
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM discover_dirty WHERE tenant_id = $1`, w.biz).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	unmark := func() {
		t.Helper()
		if _, err := w.env.DB.Admin.Exec(ctx, `DELETE FROM discover_dirty`); err != nil {
			t.Fatal(err)
		}
	}
	unmark()
	w.owner.must(200, "POST", "/v1/storefront/catalogue/refresh", nil)
	if !marked() {
		t.Fatal("the catalogue's refresh did not mark the sweep")
	}
	unmark()
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_catalogue SET item = item || '{"available": false}' WHERE tenant_id = $1`, w.biz); err != nil {
		t.Fatal(err)
	}
	if !marked() {
		t.Fatal("a stock change did not mark the sweep")
	}
	unmark()
	if _, err := w.env.DB.Admin.Exec(ctx, `DELETE FROM storefront_catalogue WHERE tenant_id = $1`, w.biz); err != nil {
		t.Fatal(err)
	}
	if !marked() {
		t.Fatal("emptying the catalogue did not mark the sweep")
	}

	// Two businesses due the notice: the one tried and failed goes last.
	w.owner.must(200, "PUT", "/v1/business-profile", map[string]any{"area": "Yaba", "categories": []string{"food"}})
	w.owner.rawPost(t, "/v1/business-profile/logo", "image/jpeg", photoN(t, 0))
	other := w.tenant(t, "Other Kitchen", "other@other.test")
	verifyPhone(t, w.world, other, "phone", "business")
	other.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "other-kitchen", "draft": map[string]any{"name": "Other Kitchen", "area": "Yaba", "hours": hoursOpen(true)}})
	other.must(200, "POST", "/v1/storefront/publish", nil)
	other.must(200, "PUT", "/v1/business-profile", map[string]any{"categories": []string{"food"}})
	other.rawPost(t, "/v1/business-profile/logo", "image/jpeg", photoN(t, 1))
	w.shipNow(t)
	if err := w.srv.DiscoverTick(ctx); err != nil {
		t.Fatal(err)
	}
	// Told both; un-tell them, and say the notice failed for Mama's.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE discover_settings SET told_at = NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE discover_index SET notice_tried_at = now() WHERE tenant_id = $1`, w.biz); err != nil {
		t.Fatal(err)
	}
	var first uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT * FROM taskiem_discover_notice_due(1)`).Scan(&first); err != nil || first != tenantUUID(t, other) {
		t.Fatalf("the notice's first: %v %v", first, err)
	}
}

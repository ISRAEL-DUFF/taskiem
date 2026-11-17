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
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name LIKE '%search%'`,
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
	tolu.must(404, "GET", "/v1/discover/"+tenantUUID(t, ada).String(), nil)
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

// Reports (§10, §15): a report on a listing reaches the moderators beside
// the pages' reports; three different people hide it until a moderator
// looks; a moderator takes a listing out (the owners told) and puts it
// back. The one-off notice goes once to a business listed by default,
// never to one that chose.
func TestDiscoverReportsAndNotice(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	w.srv.WebAuthn = webauthn.Config{RPID: rpID, RPName: "Taskiem", Origins: []string{origin}}
	w.srv.Ops = &api.OpsSettings{}
	op, a := enrolOperator(t, w.world, "moderator@taskiem.test")
	w.owner.must(200, "PUT", "/v1/business-profile", map[string]any{"area": "Yaba", "categories": []string{"food"}})
	w.owner.rawPost(t, "/v1/business-profile/logo", "image/jpeg", photoN(t, 0))
	biz := w.biz.String()

	// The notice: once, however often the sweep runs.
	w.discoverTick(t)
	w.discoverTick(t)
	told := func(tenant uuid.UUID) int {
		t.Helper()
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE tenant_id = $1 AND kind = $2`, tenant, discover.AlertListed).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if told(w.biz) != 1 {
		t.Fatalf("told %d times", told(w.biz))
	}
	var body string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT body FROM alerts WHERE tenant_id = $1 AND kind = $2`, w.biz, discover.AlertListed).Scan(&body); err != nil ||
		body != "Your shop is now listed in Taskiem's Discover, where customers find businesses. Turn it off here." {
		t.Fatalf("the words: %q %v", body, err)
	}
	// A business that chose is never told; nor one with no storefront.
	chose := w.tenant(t, "Chose Shop", "chose@chose.test")
	verifyPhone(t, w.world, chose, "phone", "business")
	chose.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "chose-shop", "draft": map[string]any{"name": "Chose Shop", "area": "Yaba", "hours": hoursOpen(true)}})
	chose.must(200, "POST", "/v1/storefront/publish", nil)
	chose.must(200, "PUT", "/v1/business-profile", map[string]any{"categories": []string{"food"}})
	chose.rawPost(t, "/v1/business-profile/logo", "image/jpeg", photoN(t, 3))
	chose.must(200, "PUT", "/v1/settings/discover", map[string]any{"listed": true})
	w.discoverTick(t)
	if n := told(tenantUUID(t, chose)); n != 0 {
		t.Fatalf("a business that chose was told: %d", n)
	}

	// Reports.
	people := []*client{}
	for i := range 3 {
		p, _ := w.personIn(t, fmt.Sprintf("+23480311100%d", 41+i))
		people = append(people, p)
	}
	people[0].must(400, "POST", "/v1/discover/"+biz+"/report", map[string]any{"reason": "boring"})
	people[0].must(404, "POST", "/v1/discover/"+uuid.NewString()+"/report", map[string]any{"reason": "scam"})
	got := people[0].must(http.StatusAccepted, "POST", "/v1/discover/"+biz+"/report", map[string]any{"reason": "scam", "note": "Asked me to pay into a personal account"})
	if got["reported"] != true {
		t.Fatalf("a report: %v", got)
	}
	var found bool
	for _, r := range op.must(200, "GET", "/v1/ops/reports", nil)["reports"].([]any) {
		r := r.(map[string]any)
		if r["kind"] == "discover" && r["tenant_id"] == biz && r["message"] == "Asked me to pay into a personal account" {
			found = true
		}
	}
	if !found {
		t.Fatal("the moderators do not see the report")
	}
	// The same person three more times a day: the fourth is not taken.
	people[0].must(http.StatusAccepted, "POST", "/v1/discover/"+biz+"/report", map[string]any{"reason": "scam"})
	people[0].must(http.StatusAccepted, "POST", "/v1/discover/"+biz+"/report", map[string]any{"reason": "scam"})
	people[0].must(http.StatusTooManyRequests, "POST", "/v1/discover/"+biz+"/report", map[string]any{"reason": "scam"})
	if got := discoverNames(t, people[1], map[string]any{"q": "mama"}); len(got) != 1 {
		t.Fatalf("one person's reports hid it: %v", got)
	}
	people[1].must(http.StatusAccepted, "POST", "/v1/discover/"+biz+"/report", map[string]any{"reason": "offensive"})
	people[2].must(http.StatusAccepted, "POST", "/v1/discover/"+biz+"/report", map[string]any{"reason": "other"})
	if got := discoverNames(t, people[1], map[string]any{"q": "mama"}); len(got) != 0 {
		t.Fatalf("three people's reports did not hide it: %v", got)
	}
	people[1].must(404, "GET", "/v1/discover/"+biz, nil)
	if st := w.owner.must(200, "GET", "/v1/settings/discover", nil); st["hidden"] != "reported" || st["shown"] != false {
		t.Fatalf("hidden for reports: %v", st)
	}

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
	if got := discoverNames(t, people[1], map[string]any{"q": "mama"}); len(got) != 0 {
		t.Fatalf("a removed listing came back: %v", got)
	}
	op.must(200, "POST", "/v1/ops/discover/"+biz+"/restore", map[string]any{"step_up": op.stepUp(a, "ops.discover.restore", biz)})
	if got := discoverNames(t, people[1], map[string]any{"q": "mama"}); len(got) != 1 {
		t.Fatalf("restored, not found: %v", got)
	}
	for _, action := range []string{"discover.remove", "discover.restore"} {
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = $2`, w.biz, action).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s audited %d times (%v)", action, n, err)
		}
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

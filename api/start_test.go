package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Starting something (taskiem-for-everyone §18): a person signed in starts
// workflows or a business as its owner, the same account, and this
// session moves into it as switching does.
func TestStartSomething(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	ada := w.tenant(t, "Ada Shop", "ada@start.test")
	var user, shop string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT u.id, m.tenant_id FROM users u JOIN memberships m ON m.user_id = u.id WHERE u.email = 'ada@start.test'`).Scan(&user, &shop); err != nil {
		t.Fatal(err)
	}
	if me := ada.must(200, "GET", "/v1/me", nil); me["tenant_kind"] != "business" {
		t.Fatalf("a signed-up business: %v", me["tenant_kind"])
	}

	// What is refused, with nothing made.
	for _, bad := range []map[string]any{
		{"kind": "shop"},
		{},
		{"kind": "business"},
		{"kind": "business", "name": "A"},
		{"kind": "business", "name": "Visit www.evil.example"},
		{"kind": "business", "name": "Ada Salon", "kinds": []string{}},
		{"kind": "business", "name": "Ada Salon", "kinds": []string{"bakery"}},
		{"kind": "workflows", "kinds": []string{"shop"}},
		{"kind": "workflows", "name": "x"},
	} {
		if st, out := ada.do("POST", "/v1/me/start", bad); st != 400 {
			t.Errorf("%v: %d %v", bad, st, out)
		}
	}
	users, tenants := startCount(t, w, `SELECT count(*) FROM users`), startCount(t, w, `SELECT count(*) FROM tenants`)

	// Workflows: no name needed, every area off, named after its owner.
	out := ada.must(201, "POST", "/v1/me/start", map[string]any{"kind": "workflows"})
	if out["kind"] != "workflows" || out["name"] != "Owner's workflows" || out["token"] == nil || out["user_id"] != user || out["tenant_id"] == shop {
		t.Fatalf("workflows: %v", out)
	}
	wf := out["tenant_id"].(string)
	if !slices.Contains(out["tenants"].([]any), any(wf)) || !slices.Contains(out["tenants"].([]any), any(shop)) {
		t.Errorf("tenants: %v", out["tenants"])
	}
	// The session moved: the old one ended, the new one is in the workspace.
	if st, _ := ada.do("GET", "/v1/me", nil); st != 401 {
		t.Fatalf("the session started from still works: %d", st)
	}
	inWf := &client{t: t, base: w.base, token: out["token"].(string)}
	me := inWf.must(200, "GET", "/v1/me", nil)
	if me["tenant_id"] != wf || me["tenant_kind"] != "workflows" || me["tenant_name"] != "Owner's workflows" || !strings.Contains(fmt.Sprint(me["roles"]), "owner") {
		t.Fatalf("me in workflows: %v", me)
	}
	for a, on := range me["areas"].(map[string]any) {
		if on != false {
			t.Errorf("%s on in workflows", a)
		}
	}
	// The same person, no one new; one tenant more.
	if n := startCount(t, w, `SELECT count(*) FROM users`); n != users {
		t.Errorf("users: %d, before %d", n, users)
	}
	if n := startCount(t, w, `SELECT count(*) FROM tenants`); n != tenants+1 {
		t.Errorf("tenants: %d, before %d", n, tenants)
	}
	if v := scalarText(t, w, `SELECT o.source || '|' || m.role || '|' || (SELECT string_agg(action, ' ' ORDER BY chain_seq) FROM audit_log a WHERE a.tenant_id = o.tenant_id)
		FROM tenant_onboarding o JOIN memberships m ON m.tenant_id = o.tenant_id AND m.user_id = o.signed_up_by WHERE o.tenant_id = $1`, wf); v != "app|owner|tenant.create auth.switch" {
		t.Errorf("onboarding, membership and audit: %s", v)
	}
	if n := startCount(t, w, `SELECT count(*) FROM workspaces WHERE tenant_id = $1`, wf) + startCount(t, w, `SELECT count(*) FROM environments WHERE tenant_id = $1`, wf); n != 3 {
		t.Errorf("workspace and environments: %d", n)
	}
	if n := startCount(t, w, `SELECT count(*) FROM business_areas WHERE tenant_id = $1 AND NOT enabled`, wf); n != 5 {
		t.Errorf("areas off as rows: %d", n)
	}
	// The switcher shows both, with their kinds.
	kinds := map[string]string{}
	for _, b := range inWf.must(200, "GET", "/v1/me/businesses", nil)["businesses"].([]any) {
		kinds[b.(map[string]any)["name"].(string)] = b.(map[string]any)["kind"].(string)
	}
	if fmt.Sprint(kinds) != "map[Ada Shop:business Owner's workflows:workflows]" {
		t.Errorf("businesses: %v", kinds)
	}

	// A business from the workspace: its name, the areas of what it does.
	out = inWf.must(201, "POST", "/v1/me/start", map[string]any{"kind": "business", "name": "  Ada   Salon ", "kinds": []string{"service"}})
	if out["kind"] != "business" || out["name"] != "Ada Salon" {
		t.Fatalf("business: %v", out)
	}
	inSalon := &client{t: t, base: w.base, token: out["token"].(string)}
	me = inSalon.must(200, "GET", "/v1/me", nil)
	if a := me["areas"].(map[string]any); me["tenant_kind"] != "business" || a["money"] != true || a["links"] != true || a["storefront"] != false || a["stock"] != false {
		t.Errorf("the salon: %v %v", me["tenant_kind"], a)
	}
	// Without kinds, a business has every part, as the API's always had.
	out = inSalon.must(201, "POST", "/v1/me/start", map[string]any{"kind": "business", "name": "Ada Trading"})
	inTrading := &client{t: t, base: w.base, token: out["token"].(string)}
	if a := inTrading.must(200, "GET", "/v1/me", nil)["areas"].(map[string]any); a["storefront"] != true || a["stock"] != true || a["schools"] != false {
		t.Errorf("without kinds: %v", a)
	}
	// A named workspace keeps its name.
	out = inTrading.must(201, "POST", "/v1/me/start", map[string]any{"kind": "workflows", "name": "Team chores"})
	if out["name"] != "Team chores" {
		t.Errorf("a named workspace: %v", out)
	}
	if n := startCount(t, w, `SELECT count(*) FROM memberships WHERE user_id = $1 AND role = 'owner'`, user); n != 5 {
		t.Errorf("Ada owns %d", n)
	}

	// Five a day: the sixth is refused, and nothing is made or moved.
	last := &client{t: t, base: w.base, token: out["token"].(string)}
	last.must(201, "POST", "/v1/me/start", map[string]any{"kind": "workflows"})
	if st, out := (&client{t: t, base: w.base, token: last.token}).do("POST", "/v1/me/start", map[string]any{"kind": "workflows"}); st != 401 {
		t.Fatalf("the moved session: %d %v", st, out)
	}
	again := w.login(t, "ada@start.test", "correct horse battery")
	before := startCount(t, w, `SELECT count(*) FROM tenants`)
	if st, out := again.do("POST", "/v1/me/start", map[string]any{"kind": "workflows"}); st != 429 {
		t.Fatalf("the sixth today: %d %v", st, out)
	}
	if n := startCount(t, w, `SELECT count(*) FROM tenants`); n != before {
		t.Errorf("made over the limit: %d", n)
	}
	again.must(200, "GET", "/v1/me", nil)

	// An API key is not a person.
	key := again.must(201, "POST", "/v1/api-keys", map[string]any{"name": "ci", "permissions": []string{"run.read"}})
	if st, _ := (&client{t: t, base: w.base, token: key["key"].(string)}).do("POST", "/v1/me/start", map[string]any{"kind": "workflows"}); st != 403 {
		t.Errorf("an API key: %d", st)
	}
}

// A web session (a cookie) starting something gets its new session as a
// cookie, not in the answer, and the workspace's onboarding is the web's.
func TestStartSomethingCookie(t *testing.T) {
	w := newWorld(t)
	ada := w.tenant(t, "Ada Shop", "ada@cookie.test")
	body := strings.NewReader(`{"kind":"workflows"}`)
	req, _ := http.NewRequest("POST", w.base+"/v1/me/start", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Taskiem-Request", "1")
	req.AddCookie(&http.Cookie{Name: "taskiem_session", Value: ada.token})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var next string
	for _, c := range resp.Cookies() {
		if c.Name == "taskiem_session" {
			next = c.Value
		}
	}
	if next == "" || next == ada.token {
		t.Fatalf("no new cookie: %v", resp.Cookies())
	}
	me := (&client{t: t, base: w.base, token: next}).must(200, "GET", "/v1/me", nil)
	if me["tenant_kind"] != "workflows" {
		t.Errorf("me: %v", me)
	}
	if v := scalarText(t, w, `SELECT source FROM tenant_onboarding WHERE tenant_id = $1`, me["tenant_id"]); v != "signup" {
		t.Errorf("source: %s", v)
	}
}

// What a tenant is follows its areas (§18.2): turning one on in a
// workflows workspace makes it a business; turning every one off makes a
// business workflows again. Each change is audited.
func TestAreasFlipKind(t *testing.T) {
	w := newWorld(t)
	ada := w.tenant(t, "Ada Shop", "ada@flip.test")
	out := ada.must(201, "POST", "/v1/me/start", map[string]any{"kind": "workflows"})
	wf := &client{t: t, base: w.base, token: out["token"].(string)}
	tenant := out["tenant_id"].(string)
	kind := func() string { return wf.must(200, "GET", "/v1/me", nil)["tenant_kind"].(string) }

	wf.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"money": true}})
	if k := kind(); k != "business" {
		t.Fatalf("an area on: %s", k)
	}
	wf.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"money": false}})
	if k := kind(); k != "workflows" {
		t.Fatalf("every area off: %s", k)
	}
	// Schools' own switch is an area too.
	wf.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true})
	if k := kind(); k != "business" {
		t.Fatalf("schools on: %s", k)
	}
	if v := scalarText(t, w, `SELECT string_agg(detail->>'to', ' ' ORDER BY chain_seq) FROM audit_log WHERE tenant_id = $1 AND action = 'tenant.kind'`, tenant); v != "business workflows business" {
		t.Errorf("audited: %q", v)
	}
	// A business that turns everything off becomes workflows.
	ada2 := w.login(t, "ada@flip.test", "correct horse battery")
	ada2.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"storefront": false, "orders": false, "stock": false, "money": false, "links": false}, "confirm": true})
	if k := ada2.must(200, "GET", "/v1/me", nil)["tenant_kind"]; k != "workflows" {
		t.Errorf("a business with nothing on: %v", k)
	}
}

// Workflows are never found: not in Explore, not in the links search,
// even with being found turned on; a business that becomes workflows
// drops out.
func TestExploreLeavesOutWorkflows(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	ada := w.tenant(t, "Ada Kitchen", "ada@explore-kind.test")
	ada.must(200, "PUT", "/v1/business-profile", map[string]any{"name": "", "tagline": "", "area": "Ikeja", "about": ""})
	bisi := w.tenant(t, "Bisi Stores", "bisi@explore-kind.test")
	bisi.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	bisi.must(200, "PUT", "/v1/business-profile", map[string]any{"name": "", "tagline": "", "area": "Ikeja", "about": ""})
	var bisiID string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM tenants WHERE name = 'Bisi Stores'`).Scan(&bisiID); err != nil {
		t.Fatal(err)
	}
	listed := func() bool {
		t.Helper()
		ex := ada.must(200, "GET", "/v1/explore", nil)
		found := false
		for _, sec := range []string{"near", "fresh"} {
			for _, c := range ex[sec].([]any) {
				found = found || c.(map[string]any)["business"] == bisiID
			}
		}
		search := ada.must(200, "GET", "/v1/links/search?q="+url.QueryEscape("Bisi"), nil)
		inSearch := strings.Contains(fmt.Sprint(search), bisiID)
		if found != inSearch {
			t.Errorf("explore %v, search %v", found, inSearch)
		}
		return found
	}
	if !listed() {
		t.Fatal("a findable business is not listed")
	}
	// Made workflows by hand, its links still on: it drops out.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE tenants SET kind = 'workflows' WHERE id = $1`, bisiID); err != nil {
		t.Fatal(err)
	}
	if listed() {
		t.Fatal("a workflows workspace is listed")
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE tenants SET kind = 'business' WHERE id = $1`, bisiID); err != nil {
		t.Fatal(err)
	}
	if !listed() {
		t.Fatal("back to a business, not listed")
	}
	// Every area off: workflows, and gone.
	bisi.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"storefront": false, "orders": false, "stock": false, "money": false, "links": false}, "confirm": true})
	if listed() {
		t.Fatal("a business turned workflows is listed")
	}
}

// The web form: "just workflows" (kinds: []) is a workflows workspace;
// any kind, or none said, a business.
func TestSignupKind(t *testing.T) {
	w := newWorld(t)
	anon := &client{t: t, base: w.base}
	for _, c := range []struct {
		kinds any
		want  string
	}{{[]string{}, "workflows"}, {[]string{"shop"}, "business"}, {nil, "business"}} {
		email := uuid.NewString()[:8] + "@signup-kind.test"
		body := map[string]any{"tenant": "Kind Test", "email": email, "name": "Owner", "password": "correct horse battery", "accept_terms": true}
		if c.kinds != nil {
			body["kinds"] = c.kinds
		}
		out := anon.must(201, "POST", "/v1/signup", body)
		if v := scalarText(t, w, `SELECT kind FROM tenants WHERE id = $1`, out["tenant_id"]); v != c.want {
			t.Errorf("kinds %v: %s, want %s", c.kinds, v, c.want)
		}
	}
}

func startCount(t *testing.T, w *world, q string, args ...any) int {
	t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func scalarText(t *testing.T, w *world, q string, args ...any) string {
	t.Helper()
	var s *string
	if err := w.env.DB.Admin.QueryRow(context.Background(), q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if s == nil {
		return ""
	}
	return *s
}

// Start something from a person's own side (taskiem-for-everyone §18):
// a person made with a phone, in no business, starts workflows and then a
// business; each time their session moves into what they started, and
// the new tenant's start is recorded as the app's (they have no email to
// confirm). Back on their own side, they see both in the switcher.
func TestStartSomethingFromPersonal(t *testing.T) {
	ctx := context.Background()
	w, sms := phoneWorld(t)
	you, made := phoneIn(t, w, sms, "+2348031230299", nil)
	if made["personal"] != true {
		t.Fatalf("not personal: %v", made)
	}
	out := you.must(201, "POST", "/v1/me/start", map[string]any{"kind": "workflows"})
	if out["kind"] != "workflows" || out["name"] != "Tolu Ade's workflows" || out["token"] == nil {
		t.Fatalf("start workflows: %v", out)
	}
	ws := &client{t: t, base: w.base, token: out["token"].(string)}
	me := ws.must(200, "GET", "/v1/me", nil)
	if me["personal"] != false || me["tenant_id"] != out["tenant_id"] || me["tenant_kind"] != "workflows" {
		t.Fatalf("me in the workspace: %v", me)
	}
	if st, _ := you.do("GET", "/v1/me", nil); st != 401 {
		t.Fatalf("the personal session should have moved: %d", st)
	}
	var source string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT source FROM tenant_onboarding WHERE tenant_id = $1`, out["tenant_id"]).Scan(&source); err != nil || source != "app" {
		t.Fatalf("source %q: %v", source, err)
	}
	// From the workspace: a business, then back to their own side.
	biz := ws.must(201, "POST", "/v1/me/start", map[string]any{"kind": "business", "name": "Tolu Cakes", "kinds": []string{"shop"}})
	shop := &client{t: t, base: w.base, token: biz["token"].(string)}
	back := shop.must(200, "POST", "/v1/me/switch", map[string]any{"personal": true})
	own := &client{t: t, base: w.base, token: back["token"].(string)}
	list := own.must(200, "GET", "/v1/me/businesses", nil)["businesses"].([]any)
	kinds := map[string]string{}
	for _, b := range list {
		m := b.(map[string]any)
		kinds[m["name"].(string)], _ = m["kind"].(string)
	}
	if len(list) != 2 || kinds["Tolu Ade's workflows"] != "workflows" || kinds["Tolu Cakes"] != "business" {
		t.Fatalf("businesses: %v", list)
	}
}

package api_test

import (
	"bytes"
	"context"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	stockconn "github.com/israel-duff/taskiem/connectors/stock"
	"github.com/israel-duff/taskiem/engine/areas"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
)

// areaState reads GET /v1/settings/areas as area name to on or off.
func areaState(t *testing.T, c *client) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, x := range c.must(200, "GET", "/v1/settings/areas", nil)["areas"].([]any) {
		row := x.(map[string]any)
		out[row["area"].(string)] = row["enabled"].(bool)
	}
	return out
}

// TestBusinessAreas: a business has every area but Schools on until it
// chooses; an area turned off is refused on its routes (404 with area_off)
// and comes back, with its data, when turned on; areas that need each other
// move together; and only an owner changes them.
func TestBusinessAreas(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")

	st := areaState(t, owner)
	for _, a := range []string{"storefront", "orders", "stock", "money", "links"} {
		if !st[a] {
			t.Errorf("%s is off for a business that has not chosen", a)
		}
	}
	if st["schools"] {
		t.Error("schools is on for a business that has not chosen")
	}
	me := owner.must(200, "GET", "/v1/me", nil)["areas"].(map[string]any)
	if me["stock"] != true || me["schools"] != false {
		t.Errorf("/v1/me areas: %v", me)
	}

	// Stock off: its routes are refused, the others still answer.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"stock": false}})
	if st, out := owner.do("GET", "/v1/stock", nil); st != 404 || out["area_off"] != "stock" {
		t.Errorf("stock off: GET /v1/stock = %d %v", st, out)
	}
	if st, out := owner.do("GET", "/v1/price-lists", nil); st != 404 || out["area_off"] != "stock" {
		t.Errorf("stock off: GET /v1/price-lists = %d %v", st, out)
	}
	if st, out := owner.do("GET", "/v1/invoices", nil); st == 404 && out["area_off"] != nil {
		t.Errorf("stock off refused invoices: %v", out)
	}
	if areaState(t, owner)["stock"] {
		t.Error("stock still on")
	}
	// And back on: the route answers again.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"stock": true}})
	if st, out := owner.do("GET", "/v1/stock", nil); st == 404 && out["area_off"] != nil {
		t.Errorf("stock back on: %d %v", st, out)
	}

	// Orders cannot go off while the shop is on; both off together is fine.
	owner.must(409, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"orders": false}})
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"orders": false, "storefront": false}})
	if st, out := owner.do("GET", "/v1/storefront/orders", nil); st != 404 || out["area_off"] != "orders" {
		t.Errorf("orders off: %d %v", st, out)
	}
	if st, out := owner.do("GET", "/v1/storefront", nil); st != 404 || out["area_off"] != "storefront" {
		t.Errorf("storefront off: %d %v", st, out)
	}
	// Turning the shop on turns orders on.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"storefront": true}})
	if st := areaState(t, owner); !st["storefront"] || !st["orders"] {
		t.Errorf("shop on: %v", st)
	}

	// Schools follow money, and use the Schools switch.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"schools": true}})
	if got := owner.must(200, "GET", "/v1/settings/schools", nil); got["enabled"] != true {
		t.Errorf("schools settings after the areas switch: %v", got)
	}
	owner.must(409, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"money": false}})

	// The Schools switch's own endpoint keeps the rule too: with money off,
	// turning schools on there turns money on.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"schools": false, "money": false}})
	if st := areaState(t, owner); st["money"] || st["schools"] {
		t.Errorf("money and schools off: %v", st)
	}
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true})
	if st := areaState(t, owner); !st["money"] || !st["schools"] {
		t.Errorf("schools on through its own endpoint: %v", st)
	}

	// Only an owner changes areas; any member reads them.
	viewer := addMember(t, w, owner, "vic", "viewer")
	if st := areaState(t, viewer); !st["stock"] {
		t.Errorf("a viewer's view: %v", st)
	}
	viewer.must(403, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"stock": false}})
	if !areaState(t, owner)["stock"] {
		t.Error("a viewer turned stock off")
	}

	// Bad requests.
	owner.must(400, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"billing": false}})
	owner.must(400, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{}})

	// One business's choice is not another's.
	other := w.tenant(t, "Beta", "owner@beta.test")
	if st, out := other.do("GET", "/v1/storefront", nil); out["area_off"] != nil {
		t.Errorf("another business is refused its shop: %d %v", st, out)
	}
	if st := areaState(t, other); !st["stock"] || !st["storefront"] {
		t.Errorf("another business: %v", st)
	}
}

const stockFlow = `{"schema":"wd/v1","id":"wf_lowstock","version":1,"name":"Low stock","trigger":{"type":"manual"},
  "steps":[{"id":"low","type":"connector","name":"Find the items that are low","connector":"stock@1","action":"low_items","input":{}}]}`

// TestAreasWarnBeforeTurningOffWhatWorkflowsUse: turning an area off while
// a deployed workflow uses it is refused with the list, until confirmed;
// and once it is off, a run of that workflow stops at the step, in plain
// words, and runs again when the area is back.
func TestAreasWarnBeforeTurningOffWhatWorkflowsUse(t *testing.T) {
	w := shopWorld(t)
	if err := w.env.Registry.Register(stockconn.New(withStock(w.world))); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	wf := publishFlow(t, owner, stockFlow)

	body := map[string]any{"areas": map[string]bool{"stock": false}}
	st, out := owner.do("PUT", "/v1/settings/areas", body)
	if st != 409 {
		t.Fatalf("stock off with a workflow using it: %d %v", st, out)
	}
	used, _ := out["affected"].([]any)
	if len(used) == 0 || used[0].(map[string]any)["workflow"] != "loan" || used[0].(map[string]any)["step"] != "Find the items that are low" {
		t.Errorf("affected: %v", out["affected"])
	}
	if !areaState(t, owner)["stock"] {
		t.Error("stock went off without confirmation")
	}
	// Another part, which nothing uses, is not held up.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"links": false}})

	runOnce := func() map[string]any {
		run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
		w.env.Drain(t)
		return owner.must(200, "GET", "/v1/runs/"+run, nil)
	}
	const off = "Stock is not turned on for this business"
	if got := runOnce(); strings.Contains(toJSON(got["events"]), off) {
		t.Fatalf("with stock on: %v", toJSON(got["events"]))
	}

	// Confirmed, it goes off, and the step stops in plain words.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"stock": false}, "confirm": true})
	got := runOnce()
	if got["run"].(map[string]any)["status"] != "failed" || !strings.Contains(toJSON(got["events"]), off) {
		t.Fatalf("with stock off: %v %v", got["run"], toJSON(got["events"]))
	}
	// And on again, it runs.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"stock": true}})
	if got := runOnce(); strings.Contains(toJSON(got["events"]), off) {
		t.Fatalf("with stock back on: %v", toJSON(got["events"]))
	}
}

// TestAreasOtherDoors: with the shop off, its public page closes and its
// chat words are not understood; back on, both return (a page the owner
// closed stays closed).
func TestAreasOtherDoors(t *testing.T) {
	w := shopWorld(t)
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	verifyPhone(t, w.world, owner, "phone", "business")
	const on = "+2348010000049"
	w.bind(t, owner, on)
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": map[string]any{"name": "Ada's Kitchen"}})
	owner.must(200, "POST", "/v1/storefront/publish", nil)
	page := func() (string, string) {
		var status, msg string
		if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT status, COALESCE(closed_message, '') FROM storefronts WHERE slug = 'ada-kitchen'`).Scan(&status, &msg); err != nil {
			t.Fatal(err)
		}
		return status, msg
	}
	if r := w.say(t, on, "fronts"); !strings.Contains(r, "Your fronts:") {
		t.Fatalf("fronts with the shop on: %q", r)
	}

	// The shop off: the page closes, and "fronts" is no longer a command.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"storefront": false, "orders": false}})
	if st, msg := page(); st != "closed" || !strings.Contains(msg, "not taking orders") {
		t.Errorf("page with the shop off: %s %q", st, msg)
	}
	if r := w.say(t, on, "fronts"); strings.Contains(r, "Your fronts:") || strings.Contains(r, "no page yet") {
		t.Errorf("fronts with the shop off: %q", r)
	}

	// Back on: the page opens again, and the words work.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"storefront": true}})
	if st, _ := page(); st != "published" {
		t.Errorf("page with the shop back on: %s", st)
	}
	if r := w.say(t, on, "fronts"); !strings.Contains(r, "Your fronts:") {
		t.Errorf("fronts with the shop back on: %q", r)
	}

	// A page its owner closed stays closed through off and on.
	owner.must(200, "POST", "/v1/storefront/close", map[string]any{"message": "Back Monday"})
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"storefront": false, "orders": false}})
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"storefront": true}})
	if st, msg := page(); st != "closed" || msg != "Back Monday" {
		t.Errorf("page after the owner's own close and an off-on: %s %q", st, msg)
	}
}

// TestAreasOnlyForJobs: the jobs that message owners on their own skip a
// business that has the area off, and keep one that has not chosen.
func TestAreasOnlyForJobs(t *testing.T) {
	w := newWorld(t)
	a := w.tenant(t, "Acme", "owner@acme.test")
	b := w.tenant(t, "Beta", "owner@beta.test")
	ida, idb := uuid.MustParse(tenantOf(t, a)), uuid.MustParse(tenantOf(t, b))
	a.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"money": false}})
	got, err := areas.Only(context.Background(), w.env.DB.App, []uuid.UUID{ida, idb}, areas.Money)
	if err != nil || len(got) != 1 || got[0] != idb {
		t.Fatalf("money: %v %v", got, err)
	}
	// Stock is still on for both.
	got, err = areas.Only(context.Background(), w.env.DB.App, []uuid.UUID{ida, idb}, areas.Stock)
	if err != nil || len(got) != 2 {
		t.Fatalf("stock: %v %v", got, err)
	}
}

// TestAreasAcrossALink: a document does not cross a link into a business
// that has turned its part off; it waits, and crosses when it is back on.
func TestAreasAcrossALink(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.AccountRails = &linkRails{}
	seller, buyer, _, buyerID := linkedPair(t, w)
	tick := func(want int) {
		t.Helper()
		n, err := w.srv.LinkMirrorTick(ctx)
		if err != nil || n != want {
			t.Fatalf("mirrored %d, want %d: %v", n, want, err)
		}
		w.srv.WaitBackground()
	}
	buyer.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"money": false}})
	draft := map[string]any{"customer": map[string]any{"name": "Ada's Kitchen", "key": "link:" + buyerID},
		"lines": []map[string]any{{"description": "Rice, 50kg", "quantity": 5, "unit_kobo": 8500000}}, "due_on": "2099-01-31"}
	x := seller.must(201, "POST", "/v1/invoices", draft, "Idempotency-Key", "b-1")
	seller.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "s-1")
	tick(0) // the buyer does not use Money: nothing crosses
	if st, out := buyer.do("GET", "/v1/bills", nil); st != 404 || out["area_off"] != "money" {
		t.Errorf("the buyer's bills with Money off: %d %v", st, out)
	}
	buyer.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"money": true}})
	// It was waiting, with a longer wait after each try; the wait is over.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE link_mirrors_due SET due_at = now()`); err != nil {
		t.Fatal(err)
	}
	tick(1)
	if bills := buyer.must(200, "GET", "/v1/bills", nil)["bills"].([]any); len(bills) != 1 {
		t.Fatalf("the bill after Money is back on: %v", bills)
	}
}

// TestSignupKinds: what a business says it does at signup decides which
// parts it starts with; left out, it has every part; unknown words are
// refused.
func TestSignupKinds(t *testing.T) {
	w := newWorld(t)
	signUp := func(name, email string, kinds any) *client {
		t.Helper()
		body := map[string]any{"tenant": name, "email": email, "name": "Owner", "password": "correct horse battery", "accept_terms": true, "bearer": true}
		if kinds != nil {
			body["kinds"] = kinds
		}
		anon := &client{t: t, base: w.base}
		out := anon.must(201, "POST", "/v1/signup", body)
		return &client{t: t, base: w.base, token: out["token"].(string)}
	}
	state := func(c *client) string {
		on := []string{}
		for a, v := range areaState(t, c) {
			if v {
				on = append(on, a)
			}
		}
		sort.Strings(on)
		return strings.Join(on, ",")
	}

	if got := state(signUp("Plain Ltd", "a@signup.test", nil)); got != "links,money,orders,stock,storefront" {
		t.Errorf("no kinds: %s", got)
	}
	if got := state(signUp("Workflows Ltd", "b@signup.test", []string{})); got != "" {
		t.Errorf("just workflows: %q", got)
	}
	if got := state(signUp("Shop Ltd", "c@signup.test", []string{"shop"})); got != "links,money,orders,stock,storefront" {
		t.Errorf("shop: %s", got)
	}
	if got := state(signUp("ISP Ltd", "d@signup.test", []string{"service"})); got != "links,money" {
		t.Errorf("service: %s", got)
	}
	school := signUp("School Ltd", "e@signup.test", []string{"school"})
	if got := state(school); got != "money,schools" {
		t.Errorf("school: %s", got)
	}
	if got := school.must(200, "GET", "/v1/settings/schools", nil); got["enabled"] != true {
		t.Errorf("school settings: %v", got)
	}
	if got := state(signUp("Both Ltd", "f@signup.test", []string{"service", "school"})); got != "links,money,schools" {
		t.Errorf("service and school: %s", got)
	}

	// A business with nothing on is refused the parts, and can turn one on.
	bare := signUp("Bare Ltd", "g@signup.test", []string{})
	if st, out := bare.do("GET", "/v1/invoices", nil); st != 404 || out["area_off"] != "money" {
		t.Errorf("a bare business's invoices: %d %v", st, out)
	}
	bare.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"money": true}})
	if st, out := bare.do("GET", "/v1/invoices", nil); st == 404 && out["area_off"] != nil {
		t.Errorf("after turning Money on: %d %v", st, out)
	}

	anon := &client{t: t, base: w.base}
	anon.must(400, "POST", "/v1/signup", map[string]any{"tenant": "Odd Ltd", "email": "h@signup.test", "password": "correct horse battery", "accept_terms": true, "kinds": []string{"bank"}})
}

// TestAreasReviewFixes: a business with Linked businesses off is not found
// by search and is not written to across a link; and money words in chat
// stop being commands when Money is off.
func TestAreasReviewFixes(t *testing.T) {
	w := shopWorld(t)
	seller := w.tenant(t, "Dangote Depot", "d@review.test")
	buyer := w.tenant(t, "Ada Kitchen", "a@review.test")
	sellerID := tenantOf(t, seller)
	seller.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	find := func() int {
		out := buyer.must(200, "GET", "/v1/links/search?q="+url.QueryEscape("dangote depot"), nil)
		res, _ := out["results"].([]any)
		return len(res)
	}
	if find() != 1 {
		t.Fatalf("a findable business is not found")
	}
	buyer.must(201, "POST", "/v1/links", map[string]any{"business": sellerID, "role": "supplier"})
	l := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)
	seller.must(200, "POST", "/v1/links/"+l["id"].(string)+"/accept", nil)
	w.srv.WaitBackground()
	linkID := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)

	// The seller turns Linked businesses off: not found by others, and a
	// message to it says so, plainly, and writes nothing.
	seller.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"links": false}})
	if n := find(); n != 0 {
		t.Errorf("a business with Linked businesses off is found: %d", n)
	}
	st, out := buyer.do("POST", "/v1/link-chats/"+linkID+"/messages", map[string]any{"text": "Is rice in stock?"})
	if st != 409 || !strings.Contains(toJSON(out), "does not use Linked businesses") {
		t.Errorf("a message to a business with Linked businesses off: %d %v", st, out)
	}
	// Back on: found, and the message goes.
	seller.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"links": true}})
	if find() != 1 {
		t.Error("not found again after turning Linked businesses back on")
	}
	buyer.must(201, "POST", "/v1/link-chats/"+linkID+"/messages", map[string]any{"text": "Is rice in stock?"})

	// Chat: "cash in 20,000 till" is a money-log command while Money is on,
	// and not one when it is off.
	verifyPhone(t, w.world, buyer, "phone", "business")
	const num = "+2348010000077"
	w.bind(t, buyer, num)
	on := w.say(t, num, "cash in 20,000 till")
	w.say(t, num, "no") // leave it: the next message starts fresh
	buyer.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"money": false}})
	off := w.say(t, num, "cash in 20,000 till")
	if strings.Contains(on, "did not understand") || !strings.Contains(off, "did not understand") {
		t.Errorf("cash in, Money on: %q, off: %q", on, off)
	}
}

// TestTemplatesFollowTheSwitches: a template of a part that is off is not
// offered, and comes back with it.
func TestTemplatesFollowTheSwitches(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	has := func(id string) bool {
		for _, x := range owner.must(200, "GET", "/v1/templates", nil)["templates"].([]any) {
			if x.(map[string]any)["id"] == id {
				return true
			}
		}
		return false
	}
	for _, id := range []string{"subscription-monthly-invoices", "subscription-reconnect-on-payment", "subscription-outage-notice"} {
		if !has(id) {
			t.Errorf("%s is not offered with every part on", id)
		}
	}
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"money": false}})
	if has("subscription-monthly-invoices") || has("subscription-reconnect-on-payment") || has("chase-overdue-invoices") {
		t.Error("invoice templates are offered with Money off")
	}
	if !has("subscription-outage-notice") {
		t.Error("a template that needs no part that is off disappeared")
	}
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"money": true}})
	if !has("subscription-monthly-invoices") {
		t.Error("not offered again with Money back on")
	}
}

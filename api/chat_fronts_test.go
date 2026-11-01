package api_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Fronts from chat (docs/spec/fronts.md F4): "add front" asks the kind,
// or takes it named; the front is made off with its table; dishes and
// items go to its own list, by "to <front>" or the one Food front; price,
// sold out and the rest find an item on any list, naming the front when
// a name is on more than one; "front … on" publishes the page again;
// "turn on orders for …" makes and publishes the front's own order
// workflow; "orders …" lists one front's orders. Messages are spread over
// several numbers, as each number gets twenty, then one every three
// seconds.
func TestChatFronts(t *testing.T) {
	w := shopWorld(t)
	ctx := context.Background()
	w.srv.Orders = &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault}
	if err := w.env.Registry.Register(sfconn.New(w.srv.Orders)); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	verifyPhone(t, w.world, owner, "phone", "business")
	const on, vn = "+2348010000049", "+2348010000045"
	w.bind(t, owner, on)
	w.bind(t, w.member(t, owner, "vic@ada.test", "viewer"), vn)
	admins := []string{"+2348010000041", "+2348010000042", "+2348010000043", "+2348010000044"}
	for i, n := range admins {
		w.bind(t, w.member(t, owner, fmt.Sprintf("adm%d@ada.test", i), "admin"), n)
	}
	an, bn, cn, dn := admins[0], admins[1], admins[2], admins[3]

	if r := w.say(t, vn, "fronts"); !strings.Contains(r, "storefront.manage") {
		t.Errorf("viewer: %q", r)
	}
	if r := w.say(t, on, "fronts"); !strings.Contains(r, "no page yet") {
		t.Errorf("no page: %q", r)
	}
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": map[string]any{"name": "Ada's Kitchen"}})
	owner.must(200, "POST", "/v1/storefront/publish", nil)
	if r := w.say(t, on, "fronts"); !strings.Contains(r, "Your fronts:") || !strings.Contains(r, "*Ada's Kitchen* · Shop · on · home page https://ada-kitchen."+shopDomain) {
		t.Fatalf("fronts: %q", r)
	}

	// The kind asked as a list, then shown and made on yes.
	r := w.say(t, on, "add front Snacks")
	if !strings.Contains(r, "What is *Snacks* for?") || !strings.Contains(r, "1. *Shop*") || !strings.Contains(r, "2. *Food*") || !strings.Contains(r, "3. *Hire*") || !strings.Contains(r, "4. *Spaces*") || !strings.Contains(r, "7. *Custom*") {
		t.Fatalf("kinds: %q", r)
	}
	if r := w.say(t, on, "rockets"); !strings.Contains(r, "What is *Snacks* for?") {
		t.Errorf("not a kind: %q", r)
	}
	r = w.say(t, on, "2")
	if !strings.Contains(r, "Add *Snacks*, a Food front?") || !strings.Contains(r, "its own list, Menu") || !strings.Contains(r, "*Order now*") || !strings.Contains(r, "starts off") {
		t.Fatalf("confirm: %q", r)
	}
	r = w.yes(t, on)
	if !strings.Contains(r, "Made *Snacks*, a Food front. It is off for now.") || !strings.Contains(r, "*add dish Jollof rice 2500 to Snacks*") ||
		!strings.Contains(r, "*turn on orders for Snacks*") || !strings.Contains(r, "*front Snacks on*") {
		t.Fatalf("made: %q", r)
	}
	var slug string
	var enabled bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT slug, enabled FROM storefront_fronts WHERE name = 'Snacks'`).Scan(&slug, &enabled); err != nil || slug != "snacks" || enabled {
		t.Fatalf("front: %v %q %v", err, slug, enabled)
	}
	if r := w.say(t, on, "add front snacks"); !strings.Contains(r, "a front called *Snacks* already") {
		t.Errorf("twice: %q", r)
	}
	// The kind named; a front with no list.
	if r := w.say(t, on, "add questions front Ask us"); !strings.Contains(r, "Add *Ask us*, a Questions front?") || strings.Contains(r, "its own list") {
		t.Fatalf("questions: %q", r)
	}
	if r := w.yes(t, on); !strings.Contains(r, "Made *Ask us*") || !strings.Contains(r, "*turn on questions for Ask us*") || strings.Contains(r, "add item") {
		t.Fatalf("made questions: %q", r)
	}
	// "add product Front door mat" is a product, not a front.
	if r := w.say(t, an, "add product Front door mat 3000"); !strings.Contains(r, "Add this product?") {
		t.Errorf("a product: %q", r)
	}
	w.say(t, an, "no")

	// A dish goes to the one Food front; an item names its front.
	r = w.say(t, an, "add dish Puff puff 500")
	if !strings.Contains(r, "Add this to *Snacks*?") || !strings.Contains(r, "*Puff puff* ₦500") {
		t.Fatalf("dish: %q", r)
	}
	if r := w.yes(t, an); !strings.Contains(r, "Added *Puff puff* at ₦500 to *Snacks*.") || !strings.Contains(r, "*front Snacks on*") {
		t.Fatalf("dish added: %q", r)
	}
	if r := w.say(t, an, "add item Chin chin 300 to snacks"); !strings.Contains(r, "Add this to *Snacks*?") || !strings.Contains(r, "*Chin chin* ₦300") {
		t.Fatalf("item: %q", r)
	}
	w.yes(t, an)
	rows := w.rows(t, "menu")
	if len(rows) != 2 || rows[0]["name"] != "Puff puff" || fmt.Sprint(rows[0]["price"]) != "500" || rows[0]["available"] != true {
		t.Fatalf("menu: %v", rows)
	}
	if r := w.say(t, an, "add dish Puff puff 600"); !strings.Contains(r, "*Puff puff* is on Snacks already, at ₦500") {
		t.Errorf("twice: %q", r)
	}
	if r := w.say(t, an, "add item Zobo 300 to Ask us"); !strings.Contains(r, "*Ask us* has no list of items") {
		t.Errorf("no list: %q", r)
	}
	if r := w.say(t, an, "add item Zobo 300"); !strings.Contains(r, "Say which front") || !strings.Contains(r, "*Snacks*") {
		t.Errorf("which front: %q", r)
	}
	if r := w.say(t, vn, "add dish Akara 200"); !strings.Contains(r, "storefront.manage") {
		t.Errorf("viewer dish: %q", r)
	}

	// Changes find the item on its front; sold out hides it.
	if r := w.say(t, bn, "price chin chin 350"); !strings.Contains(r, "*Chin chin*") || !strings.Contains(r, "₦300") || !strings.Contains(r, "₦350") {
		t.Fatalf("price: %q", r)
	}
	if r := w.yes(t, bn); !strings.Contains(r, "Done: *Chin chin* on Snacks is updated.") {
		t.Fatalf("priced: %q", r)
	}
	if r := w.say(t, bn, "sold out puff puff"); !strings.Contains(r, "*Puff puff*") {
		t.Fatalf("sold out: %q", r)
	}
	w.yes(t, bn)
	rows = w.rows(t, "menu")
	if fmt.Sprint(rows[1]["price"]) != "350" || rows[0]["available"] != false {
		t.Fatalf("changed: %v", rows)
	}
	// A name on two lists: the pick names each one's front.
	w.say(t, cn, "add product Puff puff 450")
	w.yes(t, cn)
	r = w.say(t, cn, "show puff puff")
	if !strings.Contains(r, "Puff puff (home page)") || !strings.Contains(r, "Puff puff (Snacks)") {
		t.Fatalf("pick: %q", r)
	}
	n := "2"
	if strings.Index(r, "Puff puff (Snacks)") < strings.Index(r, "Puff puff (home page)") {
		n = "1"
	}
	if r := w.say(t, cn, n); !strings.Contains(r, "*Puff puff*") {
		t.Fatalf("picked: %q", r)
	}
	if r := w.yes(t, cn); !strings.Contains(r, "on Snacks is updated") {
		t.Fatalf("shown: %q", r)
	}
	if w.rows(t, "menu")[0]["available"] != true {
		t.Fatal("the Snacks one is shown again")
	}
	if r := w.say(t, cn, "stock chin chin 5"); !strings.Contains(r, "Only your home page's products keep stock") {
		t.Errorf("stock: %q", r)
	}

	// On and off: the page published again.
	if r := w.say(t, dn, "front Nowhere on"); !strings.Contains(r, "No front is called Nowhere") || !strings.Contains(r, "*Snacks*") {
		t.Errorf("no front: %q", r)
	}
	if r := w.say(t, dn, "front Ada's Kitchen off"); !strings.Contains(r, "always on") {
		t.Errorf("home: %q", r)
	}
	if r := w.say(t, dn, "front snacks on"); !strings.Contains(r, "Turn on *Snacks*?") {
		t.Fatalf("on asked: %q", r)
	}
	r = w.yes(t, dn)
	if !strings.Contains(r, "*Snacks* is on. https://ada-kitchen."+shopDomain+"/snacks") || !strings.Contains(r, "*turn on orders for Snacks*") {
		t.Fatalf("on: %q", r)
	}
	if r := w.say(t, dn, "front Snacks on"); !strings.Contains(r, "on already") {
		t.Errorf("again: %q", r)
	}
	var snapshot string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT n.model::text FROM storefront_snapshots n JOIN storefronts s ON s.id = n.storefront_id AND n.version = s.published_version WHERE s.slug = 'ada-kitchen'`).Scan(&snapshot); err != nil || !strings.Contains(snapshot, `"snacks"`) {
		t.Fatalf("published: %v %s", err, snapshot)
	}

	// The front's own order form: its workflow made, naming it, published.
	if r := w.say(t, on, "turn on orders for Ask us"); !strings.Contains(r, "*Ask us* has no form for orders") {
		t.Errorf("no form: %q", r)
	}
	if r := w.say(t, on, "turn on orders for snacks"); !strings.Contains(r, "Turn on *Order now* on *Snacks*?") {
		t.Fatalf("turn on asked: %q", r)
	}
	r = w.yes(t, on)
	if !strings.Contains(r, "*Snacks* takes orders now") {
		t.Fatalf("turned on: %q", r)
	}
	var action string
	var actionOn bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT a.id::text, a.enabled FROM storefront_actions a JOIN storefront_fronts f ON f.id = a.front_id
		JOIN triggers t ON t.storefront_action_id = a.id AND t.environment = 'prod' WHERE f.name = 'Snacks' AND a.kind = 'order'`).Scan(&action, &actionOn); err != nil || !actionOn {
		t.Fatalf("the action's workflow: %v %v", err, actionOn)
	}
	if r := w.say(t, on, "turn on orders for Snacks"); !strings.Contains(r, "on already") {
		t.Errorf("again: %q", r)
	}
	if r := w.say(t, on, "turn on deliveries for Snacks"); !strings.Contains(r, "not available here") {
		t.Errorf("deliveries without riders: %q", r)
	}

	// Orders by front.
	if r := w.say(t, on, "orders Snacks"); !strings.Contains(r, "No open orders on *Snacks*.") {
		t.Errorf("orders: %q", r)
	}
	if r := w.say(t, on, "orders Nowhere"); !strings.Contains(r, "No front is called Nowhere") {
		t.Errorf("orders, no front: %q", r)
	}
	r = w.say(t, an, "fronts")
	if !strings.Contains(r, "*Snacks* · Food · on · https://ada-kitchen."+shopDomain+"/snacks · 2 items · 1 forms on") || !strings.Contains(r, "*Ask us* · Questions · off") {
		t.Errorf("fronts: %q", r)
	}
	if r := w.say(t, an, "front Snacks off"); !strings.Contains(r, "Turn off *Snacks*?") {
		t.Fatalf("off asked: %q", r)
	}
	if r := w.yes(t, an); !strings.Contains(r, "*Snacks* is off.") {
		t.Fatalf("off: %q", r)
	}
	if r := w.say(t, an, "help"); !strings.Contains(r, "*add front Snacks*") {
		t.Errorf("help: %q", r)
	}
}

// Fronts in Pidgin (F4): the list, adding one, switching one and its
// orders by the Pidgin words, answered in Pidgin.
func TestChatFrontsInPidgin(t *testing.T) {
	w := shopWorld(t)
	w.srv.Orders = &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault}
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN, lang.PCM}}
	owner := w.tenant(t, "Mama Put", "ada@mama.test")
	verifyPhone(t, w.world, owner, "phone", "business")
	const on = "+2348010000059"
	w.bind(t, owner, on)
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "mama-put", "draft": map[string]any{"name": "Mama Put"}})
	w.say(t, on, "language pidgin")
	if r := w.say(t, on, "make new front Snacks"); !strings.Contains(r, "Wetin *Snacks* dey for?") || !strings.Contains(r, "2. *Food*") {
		t.Fatalf("add front: %q", r)
	}
	if r := w.say(t, on, "food"); !strings.Contains(r, "Make I add *Snacks*, Food front?") {
		t.Fatalf("confirm: %q", r)
	}
	if r := w.yes(t, on); !strings.Contains(r, "We don make *Snacks*, Food front.") {
		t.Fatalf("made: %q", r)
	}
	if r := w.say(t, on, "my fronts"); !strings.Contains(r, "Your fronts:") || !strings.Contains(r, "*Snacks* · Food · off") {
		t.Fatalf("fronts: %q", r)
	}
	if r := w.say(t, on, "on front Snacks"); !strings.Contains(r, "Make I on *Snacks*?") {
		t.Fatalf("on: %q", r)
	}
	if r := w.yes(t, on); !strings.Contains(r, "*Snacks* don on.") || !strings.Contains(r, "*publish*") {
		t.Fatalf("on, a draft page: %q", r)
	}
	if r := w.say(t, on, "orders for Snacks"); !strings.Contains(r, "No open orders for *Snacks*.") {
		t.Fatalf("orders: %q", r)
	}
}

// Hire and Spaces fronts from chat (fronts F5): made with their tables and
// calendars, an item added with a count to start from, and their forms
// turned on with their workflows.
func TestChatHireAndSpaceFronts(t *testing.T) {
	w := shopWorld(t)
	ctx := context.Background()
	w.srv.Orders = &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault}
	if err := w.env.Registry.Register(sfconn.New(w.srv.Orders)); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada's Events", "ada@ada.test")
	verifyPhone(t, w.world, owner, "phone", "business")
	const on = "+2348010000049"
	w.bind(t, owner, on)
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-events", "draft": map[string]any{"name": "Ada's Events"}})
	owner.must(200, "POST", "/v1/storefront/publish", nil)

	for _, c := range []struct{ make, title, item, added, turnOn, kind string }{
		{"add hire front Tools", "a Hire front", "add item Canopy 5000 to Tools", "Added *Canopy* at ₦5,000 to *Tools*.", "turn on hire for Tools", "hire"},
		{"add spaces front Halls", "a Spaces front", "add item Main hall 150000 to Halls", "Added *Main hall* at ₦150,000 to *Halls*.", "turn on bookings for Halls", "space"},
	} {
		if r := w.say(t, on, c.make); !strings.Contains(r, c.title) {
			t.Fatalf("%s: %q", c.make, r)
		}
		if r := w.yes(t, on); !strings.Contains(r, "Made *") {
			t.Fatalf("%s, made: %q", c.make, r)
		}
		if r := w.say(t, on, c.item); !strings.Contains(r, "Add this to *") {
			t.Fatalf("%s: %q", c.item, r)
		}
		if r := w.yes(t, on); !strings.Contains(r, c.added) {
			t.Fatalf("%s, added: %q", c.item, r)
		}
		if r := w.say(t, on, c.turnOn); !strings.Contains(r, "Turn on *") {
			t.Fatalf("%s: %q", c.turnOn, r)
		}
		r := w.yes(t, on)
		var enabled bool
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT a.enabled FROM storefront_actions a JOIN triggers t ON t.storefront_action_id = a.id AND t.environment = 'prod'
			WHERE a.kind = $1 AND t.storefront_action = $1`, c.kind).Scan(&enabled); err != nil || !enabled {
			t.Fatalf("%s: %v %v %q", c.turnOn, err, enabled, r)
		}
	}
	var grain string
	var setup int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT b.config->'calendar'->>'grain', (b.config->'calendar'->>'setup_minutes')::int FROM storefront_blocks b
		JOIN storefront_fronts f ON f.id = b.front_id WHERE f.name = 'Halls'`).Scan(&grain, &setup); err != nil || grain != "days" || setup != 60 {
		t.Fatalf("the halls' calendar: %q %d %v", grain, setup, err)
	}
}

package api_test

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// treatFixture is a buyer linked with a seller whose price list it orders
// from in chat (what-a-business-buys §6, In chat; U2b).
type treatFixture struct {
	seller, buyer *client
	labels        map[string]string // the buyer's labels' ids, by name
	lace          string            // the buyer's material's stock item
}

// treatSetup gives the seller a price list shared with the buyer (a
// sewing machine, a generator, nylon bags, paper cups, lace fabric, a
// toolbox), and the buyer stock (the material Lace, the product Indomie),
// the seller as a supplier, and three labels.
func treatSetup(t *testing.T, w *world, seller, buyer *client) treatFixture {
	t.Helper()
	f := treatFixture{seller: seller, buyer: buyer, labels: map[string]string{}}
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Lace", "quantity": 10, "unit": "yard", "material": true}, "Idempotency-Key", "b-lace")
	buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Indomie", "quantity": 4, "unit": "carton"}, "Idempotency-Key", "b-indomie")
	f.lace = itemsByName(t, buyer.must(200, "GET", "/v1/stock", nil))["Lace"]["stock_item_id"].(string)
	for _, n := range []string{"Machines", "Packaging", "Staff welfare"} {
		f.labels[n] = buyer.must(201, "POST", "/v1/money/labels", map[string]any{"name": n})["id"].(string)
	}
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote Stores", "phone": "0803 123 4702"})["supplier"].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})

	seller.must(200, "POST", "/v1/stock/setup", nil)
	var items []any
	for i, it := range []map[string]any{
		{"name": "Singer heavy-duty sewing machine", "price_kobo": 25000000},
		{"name": "Generator", "price_kobo": 40000000},
		{"name": "Nylon bags", "price_kobo": 5000},
		{"name": "Paper cups", "price_kobo": 2000},
		{"name": "Lace fabric", "unit": "yard", "price_kobo": 150000},
		{"name": "Toolbox", "price_kobo": 1200000},
		{"name": "Plastic spoons", "price_kobo": 1000},
		{"name": "Rice", "unit": "bag", "price_kobo": 7500000},
	} {
		it["quantity"] = 1000
		id := seller.must(201, "POST", "/v1/stock/items", it, "Idempotency-Key", "s-"+string(rune('a'+i)))["item"].(map[string]any)["id"].(string)
		items = append(items, map[string]any{"item_id": id, "price_kobo": it["price_kobo"]})
	}
	list := seller.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade"})["list"].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": items})
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{sellerLink}})
	return f
}

// lineOf is the buyer's latest order line of an item.
func (f treatFixture) lineOf(t *testing.T, item string) (map[string]any, map[string]any) {
	t.Helper()
	for _, x := range f.buyer.must(200, "GET", "/v1/stock/purchases", nil)["purchases"].([]any) {
		po := x.(map[string]any)
		for _, l := range po["lines"].([]any) {
			if l := l.(map[string]any); l["item"] == item {
				return po, l
			}
		}
	}
	t.Fatalf("no line of %s", item)
	return nil, nil
}

// treated checks how a line is treated now: confirmed, with the label
// (none for "").
func (f treatFixture) treated(t *testing.T, item, treatment, label string) map[string]any {
	t.Helper()
	po, l := f.lineOf(t, item)
	want := any(nil)
	if label != "" {
		want = f.labels[label]
	}
	if l["treatment"] != treatment || l["label_id"] != want || l["unconfirmed"] != nil {
		t.Fatalf("%s: %v, want %s %s", item, l, treatment, label)
	}
	return po
}

// choiceID is the id of the choice titled so, among a message's reply
// buttons or its list's rows.
func choiceID(t *testing.T, m whatsapptest.Sent, title string) string {
	t.Helper()
	for _, b := range m.Buttons {
		if b.Title == title {
			return b.ID
		}
	}
	for _, r := range m.List {
		if r.Title == title {
			return r.ID
		}
	}
	t.Fatalf("no %q in %+v", title, m)
	return ""
}

func one(t *testing.T, got []whatsapptest.Sent) whatsapptest.Sent {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("%d answers: %+v", len(got), got)
	}
	return got[0]
}

// The question on WhatsApp: a new item is asked, the treatment as three
// reply buttons and the label as a list (three labels, Another, No
// label); the order is then shown for Yes and sent with the line as
// answered, and the seller sees nothing of it; the same item again asks
// nothing. A typed answer with its label in one; Add to stock picking a
// material, with Stock bought offered first; Another typed; 60 to keep
// track of asks first, and 300 cannot be.
func TestPurchaseQuestionOnWhatsApp(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, _ := linkedPair(t, w.waWorld)
	// A number gets twenty messages, then one every three seconds: the
	// steps are spread over two people of the business.
	on := "+2348010000701"
	const bolaOn = "+2348010000702"
	w.bind(t, buyer, on)
	w.bind(t, w.member(t, buyer, "bola@treat.test", "owner"), bolaOn)
	w.say(t, on, "links")
	f := treatSetup(t, w.world, seller, buyer)
	send := func(text string) whatsapptest.Sent { t.Helper(); return one(t, w.send(t, on, whatsapptest.Text(text))) }
	press := func(m whatsapptest.Sent, title string) whatsapptest.Sent {
		t.Helper()
		id := choiceID(t, m, title)
		if len(m.List) > 0 {
			return one(t, w.send(t, on, whatsapptest.ListReply(id, title)))
		}
		return one(t, w.send(t, on, whatsapptest.ButtonReply(id, title)))
	}

	// A new item: how to treat it, three buttons.
	q := send("order 1 sewing machine from dangote")
	if !strings.Contains(q.Text, "*Singer heavy-duty sewing machine* (1) from *Dangote Stores*: how should we treat it?") || len(q.Buttons) != 3 ||
		q.Buttons[0].Title != "Just record it" || q.Buttons[1].Title != "Add to stock" || q.Buttons[2].Title != "Keep track of it" {
		t.Fatalf("asked: %+v", q)
	}
	// The label: the three most used, Another and No label, as a list.
	l := press(q, "Keep track of it")
	if !strings.Contains(l.Text, "Label for *Singer heavy-duty sewing machine*?") || len(l.List) != 5 || l.ListButton != "Choose" ||
		l.List[0].Title != "Machines" || l.List[3].Title != "Another" || l.List[4].Title != "No label" {
		t.Fatalf("the label: %+v", l)
	}
	c := press(l, "Machines")
	if !strings.Contains(c.Text, "*Singer heavy-duty sewing machine*: Keep track of it · Machines.") || !strings.Contains(c.Text, "Send it to them?") {
		t.Fatalf("then the order: %q", c.Text)
	}
	f.treated(t, "Singer heavy-duty sewing machine", "track", "Machines")
	if r := w.yes(t, on); !strings.Contains(r, "Sent P-") {
		t.Fatalf("sent: %q", r)
	}
	po, _ := f.lineOf(t, "Singer heavy-duty sewing machine")
	if po["status"] != "sent" {
		t.Fatalf("the order: %v", po)
	}
	// Remembered (the first answer is the confirmation), and the seller
	// sees the order as ever: nothing of the treatment or the label.
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	if ls := buyer.must(200, "GET", "/v1/item-links/"+link, nil)["item_links"].([]any); len(ls) != 1 || ls[0].(map[string]any)["treatment"] != "track" {
		t.Fatalf("remembered: %v", ls)
	}
	drainLinked(t, w.world)
	o := seller.must(200, "GET", "/v1/trade-orders/"+po["reference"].(string), nil)["order"]
	raw, _ := json.Marshal(o)
	if s := string(raw); !strings.Contains(s, "Singer heavy-duty sewing machine") || strings.Contains(s, "Machines") || strings.Contains(s, "track") ||
		strings.Contains(s, "label") || strings.Contains(s, "treatment") {
		t.Fatalf("the seller's order: %s", s)
	}
	// The same item again asks nothing.
	if r := w.say(t, on, "order 1 sewing machine from dangote"); !strings.Contains(r, "Send it to them?") || strings.Contains(r, "how should we treat it?") {
		t.Fatalf("remembered: %q", r)
	}
	w.say(t, on, "no")

	// Typed, with its label, in one.
	if q := send("order 1 generator from dangote"); !strings.Contains(q.Text, "*Generator* (1) from *Dangote Stores*: how should we treat it?") {
		t.Fatalf("asked: %q", q.Text)
	}
	if r := w.say(t, on, "keep track of it, machines"); !strings.Contains(r, "*Generator*: Keep track of it · Machines.") || !strings.Contains(r, "Send it to them?") {
		t.Fatalf("typed: %q", r)
	}
	f.treated(t, "Generator", "track", "Machines")
	w.say(t, on, "no")

	// Add to stock: which stock item, materials included; then the label,
	// Stock bought first.
	q = send("order 30 yards of lace fabric from dangote")
	which := press(q, "Add to stock")
	if !strings.Contains(which.Text, "Which stock item does *Lace fabric* go into?") || choiceID(t, which, "A new one") != "pt:item:new" {
		t.Fatalf("which stock item: %+v", which)
	}
	l = press(which, "Lace")
	if !strings.Contains(l.Text, "Label for *Lace fabric*?") || l.List[0].Title != "Stock bought" {
		t.Fatalf("the label: %+v", l)
	}
	if c := press(l, "Stock bought"); !strings.Contains(c.Text, "*Lace fabric*: Add to stock (Lace) · Stock bought.") {
		t.Fatalf("saved: %q", c.Text)
	}
	if _, line := f.lineOf(t, "Lace fabric"); line["treatment"] != "stock" || line["stock_item_id"] != f.lace || line["label_id"] == nil {
		t.Fatalf("into the material: %v", line)
	}
	if r := w.yes(t, on); !strings.Contains(r, "Sent P-") {
		t.Fatalf("sent: %q", r)
	}

	// Another: the label typed, made.
	on = bolaOn
	q = send("order 2 toolbox from dangote")
	l = press(q, "Just record it")
	typed := press(l, "Another")
	if !strings.Contains(typed.Text, "Type the label for *Toolbox*") || len(typed.Buttons) != 0 {
		t.Fatalf("type it: %+v", typed)
	}
	if r := w.say(t, on, "tools"); !strings.Contains(r, "*Toolbox*: Just record it · Tools.") {
		t.Fatalf("typed: %q", r)
	}
	labels := buyer.must(200, "GET", "/v1/money/labels", nil)["labels"].([]any)
	for _, x := range labels {
		if x.(map[string]any)["name"] == "Tools" {
			f.labels["Tools"] = x.(map[string]any)["id"].(string)
		}
	}
	f.treated(t, "Toolbox", "record", "Tools")
	w.say(t, on, "no")

	// 60 to keep track of asks first; Just record them.
	q = send("order 60 nylon bags from dangote")
	many := press(q, "Keep track of it")
	if !strings.Contains(many.Text, "Put 60 Nylon bags on Things we have, one row each?") || len(many.Buttons) != 2 ||
		many.Buttons[0].Title != "Yes, one row each" || many.Buttons[1].Title != "Just record them" {
		t.Fatalf("asked first: %+v", many)
	}
	l = press(many, "Just record them")
	if c := press(l, "No label"); !strings.Contains(c.Text, "*Nylon bags*: Just record it · Not labelled.") {
		t.Fatalf("recorded: %q", c.Text)
	}
	f.treated(t, "Nylon bags", "record", "")
	w.say(t, on, "no")
	// Yes, one row each: kept track of, 60 rows to come.
	q = send("order 60 paper cups from dangote")
	many = press(q, "Keep track of it")
	l = press(many, "Yes, one row each")
	press(l, "Tools")
	f.treated(t, "Paper cups", "track", "Tools")
	w.say(t, on, "no")
	// 300 cannot be kept track of one by one: said, and recorded.
	send("order 300 plastic spoons from dangote")
	if r := w.say(t, on, "track"); !strings.Contains(r, "300 Plastic spoons are too many to keep track of one by one (at most 200), so it is just recorded.") ||
		!strings.Contains(r, "Label for *Plastic spoons*?") {
		t.Fatalf("too many: %q", r)
	}
	if r := w.say(t, on, "no label"); !strings.Contains(r, "*Plastic spoons*: Just record it · Not labelled.") {
		t.Fatalf("recorded: %q", r)
	}
	f.treated(t, "Plastic spoons", "record", "")
	w.say(t, on, "no")
}

// The Taskiem chat asks with buttons, and takes presses and typing: a new
// item (treatment, then label), A new one asking Do you sell it? (No: a
// material, the line going into it), Another typed, and the remembered
// item asking nothing; in Pidgin, its own words.
func TestPurchaseQuestionInTheTaskiemChat(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, _ := linkedPair(t, w.waWorld)
	f := treatSetup(t, w.world, seller, buyer)
	buttons := func(m map[string]any) map[string]string {
		out := map[string]string{}
		bs, _ := m["buttons"].([]any)
		for _, b := range bs {
			out[b.(map[string]any)["title"].(string)] = b.(map[string]any)["id"].(string)
		}
		return out
	}
	last := func(msgs []map[string]any) map[string]any {
		t.Helper()
		if len(msgs) == 0 {
			t.Fatal("no answer")
		}
		return msgs[len(msgs)-1]
	}
	press := func(m map[string]any, title string) map[string]any {
		t.Helper()
		id := buttons(m)[title]
		if id == "" {
			t.Fatalf("no %q: %v", title, m)
		}
		return last(taskiemAll(t, buyer, map[string]any{"text": title, "reply": id}))
	}

	q := last(taskiemAll(t, buyer, map[string]any{"text": "order 2 generator from dangote"}))
	if b := buttons(q); !strings.Contains(q["text"].(string), "*Generator* (2) from *Dangote Stores*: how should we treat it?") || len(b) != 3 {
		t.Fatalf("asked: %v", q)
	}
	l := press(q, "Keep track of it")
	if b := buttons(l); len(b) != 5 || b["Machines"] == "" || b["Another"] == "" || b["No label"] == "" {
		t.Fatalf("the label, as buttons: %v", l)
	}
	if c := press(l, "Machines"); !strings.Contains(c["text"].(string), "*Generator*: Keep track of it · Machines.") || !strings.Contains(c["text"].(string), "Send it to them?") {
		t.Fatalf("then the order: %v", c)
	}
	f.treated(t, "Generator", "track", "Machines")
	if r := taskiemSays(t, buyer, "yes"); !strings.Contains(r, "Sent P-") {
		t.Fatalf("sent: %q", r)
	}
	// Remembered: nothing asked.
	if r := taskiemSays(t, buyer, "order 1 generator from dangote"); !strings.Contains(r, "Send it to them?") {
		t.Fatalf("remembered: %q", r)
	}
	taskiemSays(t, buyer, "no")

	// A new stock item: Do you sell it? No makes a material, and the line
	// goes into it; Another, typed.
	q = last(taskiemAll(t, buyer, map[string]any{"text": "order 5 paper cups from dangote"}))
	which := press(q, "Add to stock")
	sell := press(which, "A new one")
	if !strings.Contains(sell["text"].(string), "*Paper cups* will be a new stock item. Do you sell it?") {
		t.Fatalf("do you sell it: %v", sell)
	}
	l = last(taskiemAll(t, buyer, map[string]any{"text": "No, I don't", "reply": "no"}))
	if !strings.Contains(l["text"].(string), "Label for *Paper cups*?") {
		t.Fatalf("the label: %v", l)
	}
	cups := itemsByName(t, buyer.must(200, "GET", "/v1/stock", nil))["Paper cups"]
	if cups == nil || cups["material"] != true {
		t.Fatalf("a material: %v", cups)
	}
	typed := press(l, "Another")
	if !strings.Contains(typed["text"].(string), "Type the label for *Paper cups*") {
		t.Fatalf("type it: %v", typed)
	}
	if r := taskiemSays(t, buyer, "Kitchen things"); !strings.Contains(r, "*Paper cups*: Add to stock (Paper cups) · Kitchen things.") {
		t.Fatalf("saved: %q", r)
	}
	if _, line := f.lineOf(t, "Paper cups"); line["treatment"] != "stock" || line["stock_item_id"] != cups["stock_item_id"] {
		t.Fatalf("into the new material: %v", line)
	}
	taskiemSays(t, buyer, "no")

	// The same item twice on one order is refused as it is drafted, so the
	// question never has a second line of an item the first answer makes
	// known (it would be skipped, keeping the suggestion).
	if r := taskiemSays(t, buyer, "Buy 2 toolbox and 3 toolbox from @Dangote Stores"); !strings.Contains(r, "Toolbox is on the order twice") {
		t.Fatalf("twice: %q", r)
	}

	// In Pidgin, the answer in its own words (and its label after them).
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN, lang.PCM}}
	taskiemSays(t, buyer, "language pidgin")
	if r := taskiemSays(t, buyer, "order 1 toolbox from dangote"); !strings.Contains(r, "*Toolbox* (1)") {
		t.Fatalf("asked: %q", r)
	}
	if r := taskiemSays(t, buyer, "dey track am, machines"); !strings.Contains(r, "*Toolbox*: Keep track of it · Machines.") {
		t.Fatalf("in Pidgin: %q", r)
	}
	f.treated(t, "Toolbox", "track", "Machines")
}

// On Telegram the question's answers are buttons under the message, the
// label's five among them; Another asks for it typed.
func TestPurchaseQuestionOnTelegram(t *testing.T) {
	w := newTGWorld(t)
	pg := xenibasetest.New(t)
	pg.AddToken(xenibasetest.Token{Secret: tablesPlatformToken, Name: "platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: time.Now().Add(5 * 365 * 24 * time.Hour)})
	if err := w.env.Registry.Register(xenibase.New(xenibase.Options{BaseURL: pg.URL})); err != nil {
		t.Fatal(err)
	}
	w.srv.Tables = &datatables.Service{Pool: w.env.DB.App, Vault: w.env.Vault, Egress: w.env.Egress, Logger: w.srv.Logger,
		Platform: datatables.Platform{URL: pg.URL, Token: tablesPlatformToken, Org: pg.OrgID}, ConnectorURL: pg.URL, PollInterval: time.Millisecond}
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, _ := linkedPair(t, &waWorld{world: w.world})
	f := treatSetup(t, w.world, seller, buyer)
	const ada = int64(5601)
	w.link(t, buyer, ada, "ada")
	data := func(c telegramtest.Call, title string) string {
		t.Helper()
		for _, row := range c.Keyboard {
			for _, b := range row {
				if b.Text == title {
					return b.Data
				}
			}
		}
		t.Fatalf("no %q: %+v", title, c.Keyboard)
		return ""
	}
	count := func(c telegramtest.Call) int {
		n := 0
		for _, row := range c.Keyboard {
			n += len(row)
		}
		return n
	}
	press := func(c telegramtest.Call, title string) telegramtest.Call {
		t.Helper()
		d := data(c, title)
		var out []telegramtest.Call
		for _, x := range w.update(t, ada, func(id int64) []byte { return telegramtest.Callback(id, ada, c.MessageID, "q", d) }) {
			if x.Method == "sendMessage" {
				out = append(out, x)
			}
		}
		if len(out) != 1 {
			t.Fatalf("%q: %+v", title, out)
		}
		return out[0]
	}

	q := w.say(t, ada, "order 1 sewing machine from dangote")
	if !strings.Contains(q.Text, "how should we treat it?") || count(q) != 3 {
		t.Fatalf("asked: %+v", q)
	}
	l := press(q, "Keep track of it")
	if !strings.Contains(l.Text, "Label for Singer heavy-duty sewing machine?") || count(l) != 5 {
		t.Fatalf("the label: %+v", l)
	}
	typed := press(l, "Another")
	if !strings.Contains(typed.Text, "Type the label") {
		t.Fatalf("type it: %+v", typed)
	}
	c := w.say(t, ada, "Machines")
	if !strings.Contains(c.Text, "Keep track of it · Machines.") || !strings.Contains(c.Text, "Send it to them?") {
		t.Fatalf("then the order: %+v", c)
	}
	if r := press(c, "Yes"); !strings.Contains(r.Text, "Sent P-") {
		t.Fatalf("sent: %+v", r)
	}
	if po := f.treated(t, "Singer heavy-duty sewing machine", "track", "Machines"); po["status"] != "sent" {
		t.Fatalf("the order: %v", po)
	}
}

// In a Slack DM the question's answers are buttons in the message.
func TestPurchaseQuestionOnSlack(t *testing.T) {
	w := newSlackWorld(t)
	pg := xenibasetest.New(t)
	pg.AddToken(xenibasetest.Token{Secret: tablesPlatformToken, Name: "platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: time.Now().Add(5 * 365 * 24 * time.Hour)})
	if err := w.env.Registry.Register(xenibase.New(xenibase.Options{BaseURL: pg.URL})); err != nil {
		t.Fatal(err)
	}
	w.srv.Tables = &datatables.Service{Pool: w.env.DB.App, Vault: w.env.Vault, Egress: w.env.Egress, Logger: w.srv.Logger,
		Platform: datatables.Platform{URL: pg.URL, Token: tablesPlatformToken, Org: pg.OrgID}, ConnectorURL: pg.URL, PollInterval: time.Millisecond}
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, _ := linkedPair(t, &waWorld{world: w.world})
	f := treatSetup(t, w.world, seller, buyer)
	buyer.must(201, "POST", "/v1/connections", map[string]any{"connector": "slack@1", "environment": "prod", "credentials": map[string]string{"bot_token": "xoxb-acme"}})
	w.fake.users["ada@bills.test"] = "U0ADA"
	w.link(t, buyer, "U0ADA")
	n := 0
	say := func(text string) map[string]any {
		t.Helper()
		n++
		before := len(w.fake.to("U0ADA"))
		if st, _ := w.event(t, dm("EvT"+strconv.Itoa(n), "U0ADA", text), slackSecret); st != 200 {
			t.Fatalf("dm: %d", st)
		}
		got := w.fake.to("U0ADA")[before:]
		if len(got) != 1 {
			t.Fatalf("%q: %v", text, got)
		}
		return got[0]
	}
	click := func(value string) map[string]any {
		t.Helper()
		before := len(w.fake.to("U0ADA"))
		if st := w.click(t, "U0ADA", "taskiem.reply.0", value, slackSecret); st != 200 {
			t.Fatalf("click: %d", st)
		}
		got := w.fake.to("U0ADA")[before:]
		if len(got) != 1 {
			t.Fatalf("%q: %v", value, got)
		}
		return got[0]
	}

	q := say("order 1 generator from dangote")
	if !strings.Contains(q["text"].(string), "how should we treat it?") || !slices.Equal(buttons(q), []string{"pt:record", "pt:stock", "pt:track"}) {
		t.Fatalf("asked: %v", q)
	}
	l := click("pt:track")
	if bs := buttons(l); !strings.Contains(l["text"].(string), "Label for") || len(bs) != 5 || bs[3] != "pt:label:another" || bs[4] != "pt:label:none" {
		t.Fatalf("the label: %v", l)
	}
	c := click("pt:label:1")
	if !strings.Contains(c["text"].(string), "Keep track of it · Machines.") || !strings.Contains(c["text"].(string), "Send it to them?") {
		t.Fatalf("then the order: %v", c)
	}
	f.treated(t, "Generator", "track", "Machines")
	if r := click(buttons(c)[0]); !strings.Contains(r["text"].(string), "Sent P-") {
		t.Fatalf("sent: %v", r)
	}
}

// g-uses-ask (build-plan-gaps H2): Add to stock into a stock item counted
// in another unit asks how many of it one of the line is, once: rice
// bought in bags, counted in grams, asks how many grams one bag is, and
// the line goes in with it; the next order of it asks nothing, the uses
// remembered with the item. A reply in words of its own ("add 10 bags of
// rice") is read by the model, given the question, as its answer.
func TestPurchaseQuestionAsksUses(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, _ := linkedPair(t, w.waWorld)
	asked := []string{}
	fake := &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		p := ai.PromptText(req)
		asked = append(asked, p)
		msg := p[strings.Index(p, "<message>\n")+len("<message>\n") : strings.Index(p, "\n</message>")]
		switch msg {
		case "add 10 bags of rice":
			return &ai.Response{Text: `{"kind":"command","id":"","pick":"","values":[],"command":"add to stock, rice","reply":""}`}, nil
		case "fifty kilos":
			return &ai.Response{Text: `{"kind":"command","id":"","pick":"","values":[],"command":"50000","reply":""}`}, nil
		}
		return &ai.Response{Text: `{"kind":"cannot","id":"","pick":"","values":[],"command":"","reply":"Not an answer."}`}, nil
	}}
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN}, Model: fake}
	const on = "+2348010000711"
	w.bind(t, buyer, on)
	w.say(t, on, "links")
	f := treatSetup(t, w.world, seller, buyer)
	buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 0, "unit": "g", "material": true}, "Idempotency-Key", "b-rice")
	rice := itemsByName(t, buyer.must(200, "GET", "/v1/stock", nil))["Rice"]["stock_item_id"].(string)

	if r := w.say(t, on, "order 10 bags of rice from dangote"); !strings.Contains(r, "*Rice* (10 bags) from *Dangote Stores*: how should we treat it?") {
		t.Fatalf("asked: %q", r)
	}
	// Its own words: the model is given the question, and its answer is
	// Add to stock into Rice; then how many grams one bag is.
	r := w.say(t, on, "add 10 bags of rice")
	if len(asked) != 1 || !strings.Contains(asked[0], "how should we treat it?") || !strings.Contains(asked[0], "add to stock, <the stock item's name>") {
		t.Fatalf("the model was not given the question: %v", asked)
	}
	if !strings.Contains(r, "*Rice* is counted in g. How many g is one bag?") {
		t.Fatalf("uses asked: %q", r)
	}
	// Not a number: said again; in words, read by the model.
	if r := w.say(t, on, "a lot"); !strings.Contains(r, "Say how many g one bag is, as a number") {
		t.Fatalf("not a number: %q", r)
	}
	if r := w.say(t, on, "fifty kilos"); !strings.Contains(r, "Label for *Rice*?") {
		t.Fatalf("in words: %q", r)
	}
	if r := w.say(t, on, "1"); !strings.Contains(r, "*Rice*: Add to stock (Rice) · Stock bought.") {
		t.Fatalf("saved: %q", r)
	}
	if _, line := f.lineOf(t, "Rice"); line["treatment"] != "stock" || line["stock_item_id"] != rice || line["uses"] != float64(50000) || line["label"] != "Stock bought" {
		t.Fatalf("the line: %v", line)
	}
	w.say(t, on, "no")
	// Remembered with the item: the next order asks nothing, and goes in
	// at 50,000 g a bag.
	n := len(asked)
	if r := w.say(t, on, "order 2 bags of rice from dangote"); strings.Contains(r, "how should we treat it?") || strings.Contains(r, "How many g") ||
		!strings.Contains(r, "Send it to them?") {
		t.Fatalf("remembered: %q", r)
	}
	po, line := f.lineOf(t, "Rice")
	if line["uses"] != float64(50000) || line["quantity"] != float64(2) || len(asked) != n {
		t.Fatalf("the next order: %v %v", po["reference"], line)
	}
	w.say(t, on, "no")
	// The same unit asks nothing: lace bought in yards into Lace, counted
	// in yards.
	w.say(t, on, "order 3 yards of lace fabric from dangote")
	if r := w.say(t, on, "add to stock, lace"); strings.Contains(r, "How many") || !strings.Contains(r, "Label for *Lace fabric*?") {
		t.Fatalf("the same unit: %q", r)
	}
}

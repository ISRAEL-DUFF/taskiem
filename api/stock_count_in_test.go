package api_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/riders"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// Counting in by words (what-a-business-buys §4.5, S1b): the words that
// start with add are told apart. A quantity first counts something in: a
// thing the business has is topped up, with no question; something new
// asks Do you sell it?. add product, add item with a price and add rider
// keep their meaning.
func TestCountingInWordsToldApart(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	book := &reach.Book{Pool: w.env.Store.Pool, Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}
	w.srv.Riders = &riders.Riders{Pool: w.env.DB.App, Vault: w.env.Vault, Book: book, PageBase: "https://" + reachFormHost}
	owner := w.tenant(t, "Ada Fabrics", "ada@fabrics.test")
	owner.must(200, "POST", "/v1/stock/setup", nil)
	owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Indomie", "quantity": 10, "unit": "carton"}, "Idempotency-Key", "i1")
	owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Thread", "quantity": 4, "unit": "roll", "material": true}, "Idempotency-Key", "i2")
	// A number gets twenty messages, then one every three seconds: the
	// cases are spread over several.
	numbers := []string{"+2348010000201", "+2348010000202"}
	w.bind(t, owner, numbers[0])
	w.bind(t, w.member(t, owner, "bola@fabrics.test", "owner"), numbers[1])

	for i, c := range []struct {
		words string
		want  string // in the answer
		asks  bool   // Do you sell it?
	}{
		{"add product Jollof rice 2500", "Add this product?\n*Jollof rice* ₦2,500", false},
		{"add item Chin chin 500", "None of your fronts has a list to add to.", false},
		{"add 20 yards of ankara fabric", "*ankara fabric* is new: 20 yards.", true},
		{"add 20 yards of ankara fabric at 1,500", "*ankara fabric* is new: 20 yards at ₦1,500 each.", true},
		{"stock in 3 rolls of lace", "*lace* is new: 3 rolls.", true},
		{"received 4 packs of buttons", "*buttons* is new: 4 packs.", true},
		{"add 5 cartons of indomie", "Add 5 cartons to *Indomie* (10 cartons → 15 cartons)?", false},
		{"stock in 5 cartons of indomie", "Add 5 cartons to *Indomie* (10 cartons → 15 cartons)?", false},
		{"received 5 cartons of indomie", "Add 5 cartons to *Indomie* (10 cartons → 15 cartons)?", false},
		{"add 2 rolls of thread", "Add 2 rolls to *Thread* (4 rolls → 6 rolls)?", false},
		{"add rider Musa 0803 123 4567 Ikeja", "Musa is now your rider.", false},
	} {
		on := numbers[i%len(numbers)]
		got := w.send(t, on, whatsapptest.Text(c.words))
		if len(got) != 1 {
			t.Fatalf("%q: %d answers: %+v", c.words, len(got), got)
		}
		r := got[0].Text
		if asked := strings.Contains(r, "Do you sell it?"); asked != c.asks || !strings.Contains(r, c.want) {
			t.Errorf("%q: %q", c.words, r)
		}
		if c.asks && (len(got[0].Buttons) != 3 || got[0].Buttons[0].Title != "Yes, I sell it" || got[0].Buttons[1].ID != "no") {
			t.Errorf("%q: buttons %+v", c.words, got[0].Buttons)
		}
		w.say(t, on, "cancel")
	}
	// Nothing was added by any of them.
	if items := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil)); len(items) != 2 {
		t.Fatalf("items: %v", items)
	}
}

// Something new counted in by words, on WhatsApp: No adds a material, a
// stock item with no product, counted in by a movement; it is on stock,
// how many, adjust and the low list at its low level. Yes adds a product
// counted by a new stock item. Another answer asks again.
func TestCountingInByWordsOnWhatsApp(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Ada Fabrics", "ada@fabrics.test")
	const on, vn = "+2348010000211", "+2348010000212"
	w.bind(t, owner, on)
	w.bind(t, w.member(t, owner, "vic@fabrics.test", "viewer"), vn)

	if r := w.say(t, vn, "add 20 yards of ankara fabric"); !strings.Contains(r, "table.edit") {
		t.Errorf("viewer: %q", r)
	}
	// The first: it starts keeping stock, and asks the same.
	if r := w.say(t, on, "add 20 yards of ankara fabric"); !strings.Contains(r, "Start keeping stock with *ankara fabric*: 20 yards. Do you sell it?") ||
		!strings.Contains(r, "*No* only keeps count of it") {
		t.Fatalf("first: %q", r)
	}
	if r := w.say(t, on, "maybe"); !strings.Contains(r, "Do you sell *ankara fabric*? Reply *yes* or *no*") {
		t.Fatalf("again: %q", r)
	}
	got := w.send(t, on, whatsapptest.ButtonReply("no", "No, I don't"))
	if len(got) != 1 || !strings.Contains(got[0].Text, "Added *ankara fabric*: 20 yards. It is counted only") {
		t.Fatalf("no: %+v", got)
	}
	stock := owner.must(200, "GET", "/v1/stock", nil)
	fabric := itemsByName(t, stock)["ankara fabric"]
	if fabric == nil || fabric["material"] != true || fabric["stock"] != float64(20) || fabric["unit"] != "yard" {
		t.Fatalf("a material: %v", stock)
	}
	if rows := w.rows(t, "products"); len(rows) != 0 {
		t.Fatalf("nothing in the catalogue: %v", rows)
	}
	list := owner.must(200, "GET", "/v1/stock/movements?item="+fabric["id"].(string), nil)["movements"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["reason"] != "received" {
		t.Fatalf("counted in: %v", list)
	}

	// Since S1a, stock words read materials: how many, adjust, and the low
	// list at its low level.
	if r := w.say(t, on, "how many yards of ankara fabric do I have?"); !strings.Contains(r, "*ankara fabric*: 20 yards.") {
		t.Fatalf("how many: %q", r)
	}
	if r := w.say(t, on, "adjust ankara fabric -2 cut wrong"); !strings.Contains(r, "(20 yards → 18 yards)") {
		t.Fatalf("adjust: %q", r)
	}
	if r := w.yes(t, on); !strings.HasSuffix(r, "Done: *ankara fabric* is 18 yards now.") {
		t.Fatalf("adjusted: %q", r)
	}
	owner.must(200, "PATCH", "/v1/stock/items/"+fabric["id"].(string), map[string]any{"reorder_level": 25})
	if r := w.say(t, on, "stock"); !strings.Contains(r, "• ankara fabric: 18 yards (reorder at 25)") {
		t.Fatalf("low: %q", r)
	}
	if low := owner.must(200, "GET", "/v1/stock", nil)["low"]; low != float64(1) {
		t.Fatalf("low on the page: %v", low)
	}
	// Named again: counted in, no question.
	if r := w.say(t, on, "add 5 yards of ankara fabric"); strings.Contains(r, "Do you sell it?") || !strings.Contains(r, "(18 yards → 23 yards)") {
		t.Fatalf("again: %q", r)
	}
	w.say(t, on, "cancel")

	// Yes: a product, counted by a new stock item.
	if r := w.say(t, on, "add 12 bottles of zobo"); !strings.Contains(r, "*zobo* is new: 12 bottles. Do you sell it?") {
		t.Fatalf("zobo: %q", r)
	}
	if r := w.yes(t, on); !strings.Contains(r, "Added *zobo*: 12 bottles.") || !strings.Contains(r, "send *price zobo 9500*") {
		t.Fatalf("yes: %q", r)
	}
	zobo := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))["zobo"]
	if zobo == nil || zobo["material"] == true || zobo["stock_item_id"] == nil || zobo["stock"] != float64(12) {
		t.Fatalf("a product: %v", zobo)
	}
	if rows := w.rows(t, "products"); len(rows) != 1 || rows[0]["name"] != "zobo" {
		t.Fatalf("the catalogue: %v", rows)
	}
	// cancel leaves it.
	w.say(t, on, "add 3 rolls of lace")
	w.say(t, on, "cancel")
	if items := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil)); len(items) != 2 {
		t.Fatalf("cancelled: %v", items)
	}
}

// The Taskiem chat asks Do you sell it? as buttons, and takes a press.
func TestCountingInByWordsInTheTaskiemChat(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Ada Fabrics", "ada@fabrics.test")
	owner.must(200, "POST", "/v1/stock/setup", nil)

	msgs := taskiemAll(t, owner, map[string]any{"text": "add 6 rolls of lace"})
	if len(msgs) != 1 || !strings.Contains(msgs[0]["text"].(string), "*lace* is new: 6 rolls. Do you sell it?") {
		t.Fatalf("asked: %v", msgs)
	}
	var ids []string
	for _, b := range msgs[0]["buttons"].([]any) {
		ids = append(ids, b.(map[string]any)["id"].(string))
	}
	if len(ids) != 3 || !strings.HasPrefix(ids[0], "yes") || ids[1] != "no" || ids[2] != "cancel" {
		t.Fatalf("buttons: %v", ids)
	}
	if r := textOf(taskiemAll(t, owner, map[string]any{"text": "No, I don't", "reply": "no"})); !strings.Contains(r, "Added *lace*: 6 rolls. It is counted only") {
		t.Fatalf("no: %q", r)
	}
	if lace := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))["lace"]; lace == nil || lace["material"] != true || lace["stock"] != float64(6) {
		t.Fatalf("lace: %v", lace)
	}
	// Yes, pressed under the question.
	msgs = taskiemAll(t, owner, map[string]any{"text": "add 4 packs of buttons"})
	yes := msgs[0]["buttons"].([]any)[0].(map[string]any)["id"].(string)
	if r := textOf(taskiemAll(t, owner, map[string]any{"text": "Yes, I sell it", "reply": yes})); !strings.Contains(r, "Added *buttons*: 4 packs.") {
		t.Fatalf("yes: %q", r)
	}
	if b := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))["buttons"]; b == nil || b["material"] == true {
		t.Fatalf("buttons: %v", b)
	}
}

// On Telegram the question's answers are buttons under the message.
func TestCountingInByWordsOnTelegram(t *testing.T) {
	w := newTGWorld(t)
	pg := xenibasetest.New(t)
	pg.AddToken(xenibasetest.Token{Secret: tablesPlatformToken, Name: "platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: time.Now().Add(5 * 365 * 24 * time.Hour)})
	if err := w.env.Registry.Register(xenibase.New(xenibase.Options{BaseURL: pg.URL})); err != nil {
		t.Fatal(err)
	}
	w.srv.Tables = &datatables.Service{Pool: w.env.DB.App, Vault: w.env.Vault, Egress: w.env.Egress, Logger: w.srv.Logger,
		Platform: datatables.Platform{URL: pg.URL, Token: tablesPlatformToken, Org: pg.OrgID}, ConnectorURL: pg.URL, PollInterval: time.Millisecond}
	withStock(w.world)
	owner := w.tenant(t, "Ada Fabrics", "ada@fabrics.test")
	owner.must(200, "POST", "/v1/stock/setup", nil)
	const ada = int64(5501)
	w.link(t, owner, ada, "ada")

	call := w.say(t, ada, "add 20 yards of ankara fabric")
	if !strings.Contains(call.Text, "Do you sell it?") || len(call.Keyboard) == 0 {
		t.Fatalf("asked: %+v", call)
	}
	var no string
	for _, row := range call.Keyboard {
		for _, b := range row {
			if b.Text == "No, I don't" {
				no = b.Data
			}
		}
	}
	if no == "" {
		t.Fatalf("no No: %+v", call.Keyboard)
	}
	got := w.update(t, ada, func(id int64) []byte { return telegramtest.Callback(id, ada, call.MessageID, "q1", no) })
	if len(got) == 0 || !strings.Contains(got[len(got)-1].Text, "It is counted only") {
		t.Fatalf("no: %+v", got)
	}
	if f := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil))["ankara fabric"]; f == nil || f["material"] != true || f["stock"] != float64(20) {
		t.Fatalf("fabric: %v", f)
	}
}

// Words no command reads go to the model, which knows Add something to
// count: its action, with what it understood, asks Do you sell it?; its
// row, pressed, asks what step by step first.
func TestCountingInThroughTheCatalogue(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	fake := &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		if strings.Contains(ai.PromptText(req), "I bought ankara for sewing") {
			return &ai.Response{Text: `{"kind":"action","id":"stock.new","pick":"","values":[{"field":"what","value":"20 yards of ankara fabric"}],"command":"","reply":""}`}, nil
		}
		return &ai.Response{Text: "not json"}, nil
	}}
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN}, Model: fake}
	owner := w.tenant(t, "Ada Fabrics", "ada@fabrics.test")
	owner.must(200, "POST", "/v1/stock/setup", nil)

	if r := taskiemSays(t, owner, "I bought ankara for sewing, 20 yards"); !strings.Contains(r, "*ankara fabric* is new: 20 yards. Do you sell it?") {
		t.Fatalf("the model's action: %q", r)
	}
	if p := ai.PromptText(fake.Requests()[0]); !strings.Contains(p, "- stock.new: Add something to count") {
		t.Fatalf("the catalogue was not given: %s", p)
	}
	if r := taskiemSays(t, owner, "no"); !strings.Contains(r, "It is counted only") {
		t.Fatalf("no: %q", r)
	}
	// The row: what, asked; then the question.
	if r := textOf(taskiemAll(t, owner, map[string]any{"text": "Add something to count", "reply": "act:stock.new"})); !strings.Contains(r, "What is it, and how many?") {
		t.Fatalf("the row: %q", r)
	}
	if r := taskiemSays(t, owner, "4 packs of buttons"); !strings.Contains(r, "*buttons* is new: 4 packs. Do you sell it?") {
		t.Fatalf("asked: %q", r)
	}
	if r := taskiemSays(t, owner, "yes"); !strings.Contains(r, "Added *buttons*: 4 packs.") {
		t.Fatalf("yes: %q", r)
	}
}

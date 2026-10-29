package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/moneydigest"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Sales from the web (docs/spec/stock.md K2): a cash sale writes its
// takings; a transfer sale waits for its money and is matched to it; a
// terminal's sale with no items is listed and given them; the day adds up.
func TestStockSalesByAPI(t *testing.T) {
	ctx := context.Background()
	w := newTablesWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Mama Put Stores", "ada@mama.test")
	viewer := addMember(t, w.world, owner, "vic", "viewer")
	tenant := uuid.MustParse(tenantOf(t, owner))
	owner.must(200, "POST", "/v1/stock/setup", nil)
	rice := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 10, "unit": "bag", "price_kobo": 4500000}, "Idempotency-Key", "r")["item"].(map[string]any)["id"]
	indomie := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Indomie", "quantity": 2, "unit": "carton"}, "Idempotency-Key", "i")["item"].(map[string]any)["id"]

	// No price and no amount: refused; so is a viewer.
	owner.must(400, "POST", "/v1/stock/sales", map[string]any{"lines": []any{map[string]any{"item_id": indomie, "quantity": 1}}, "paid_how": "cash"}, "Idempotency-Key", "s0")
	viewer.must(403, "POST", "/v1/stock/sales", map[string]any{"lines": []any{map[string]any{"item_id": rice, "quantity": 1}}, "paid_how": "cash"}, "Idempotency-Key", "s0")

	cash := owner.must(201, "POST", "/v1/stock/sales", map[string]any{"lines": []any{
		map[string]any{"item_id": rice, "quantity": 2}, map[string]any{"item_id": indomie, "quantity": 3, "price_kobo": 950000}},
		"paid_how": "cash"}, "Idempotency-Key", "s1")["sale"].(map[string]any)
	if cash["status"] != "recorded" || cash["amount_kobo"] != float64(9000000+2850000) || toJSON(cash["short"]) != `["Indomie"]` {
		t.Fatalf("cash sale: %v", cash)
	}
	transfer := owner.must(201, "POST", "/v1/stock/sales", map[string]any{"lines": []any{map[string]any{"item_id": rice, "quantity": 1}},
		"amount_kobo": 4400000, "paid_how": "transfer"}, "Idempotency-Key", "s2")["sale"].(map[string]any)
	if transfer["status"] != "waiting" {
		t.Fatalf("transfer: %v", transfer)
	}
	// The transfer comes; the terminal reports a sale nobody recorded.
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	now := time.Now()
	amount, terminal := int64(4400000), int64(450000)
	if _, _, err := log.Record(ctx, tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: "paystack/main", Reference: "PSK-9 " + transfer["reference"].(string),
		AmountKobo: &amount, OccurredAt: &now, Source: "event", Event: "payment.received"}); err != nil {
		t.Fatal(err)
	}
	pos, _, err := log.Record(ctx, tenant, moneylog.Entry{Direction: "in", Kind: "pos_sale", Status: "settled", Channel: "moniepointpos/shop", Reference: "TRM-1",
		AmountKobo: &terminal, OccurredAt: &now, Source: "event", Event: "pos.sale"})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := w.srv.Stock.MatchSales(ctx, tenant); err != nil || n != 1 {
		t.Fatalf("matched %d, %v", n, err)
	}
	day := viewer.must(200, "GET", "/v1/stock/sales", nil)
	if day["count"] != float64(2) || day["waiting"] != float64(0) || day["by_how"].(map[string]any)["transfer"] != float64(4400000) ||
		len(day["unsold"].([]any)) != 1 || day["unsold"].([]any)[0].(map[string]any)["entry_id"] != pos.String() {
		t.Fatalf("day: %v", day)
	}
	// Say what was sold: the terminal's sale gets its items.
	given := owner.must(201, "POST", "/v1/stock/sales", map[string]any{"lines": []any{map[string]any{"item_id": rice, "quantity": 1, "price_kobo": 450000}},
		"paid_how": "pos", "entry_id": pos}, "Idempotency-Key", "s3")["sale"].(map[string]any)
	if given["status"] != "matched" || given["matched_how"] != "owner" {
		t.Fatalf("given: %v", given)
	}
	owner.must(409, "POST", "/v1/stock/sales", map[string]any{"lines": []any{map[string]any{"item_id": rice, "quantity": 1, "price_kobo": 450000}},
		"paid_how": "pos", "entry_id": pos}, "Idempotency-Key", "s4")
	if day := viewer.must(200, "GET", "/v1/stock/sales", nil); len(day["unsold"].([]any)) != 0 || day["count"] != float64(3) {
		t.Fatalf("after: %v", day)
	}
	viewer.must(400, "GET", "/v1/stock/sales?day=yesterday", nil)
	if items := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil)); items["Rice"]["stock"] != float64(6) || items["Indomie"]["stock"] != float64(0) {
		t.Fatalf("stock: %v", items)
	}
}

// Sales by chat: sold in English and in Pidgin, the evening total at
// closing time, once.
func TestStockSalesByChat(t *testing.T) {
	ctx := context.Background()
	w := shopWorld(t)
	withStock(w.world)
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN, lang.PCM}}
	owner := w.tenant(t, "Mama Put Stores", "ada@mama.test")
	const on, pn = "+2348010000081", "+2348010000082"
	w.bind(t, owner, on)
	w.bind(t, w.member(t, owner, "bisi@mama.test", "admin"), pn)
	tenant := uuid.MustParse(tenantOf(t, owner))
	owner.must(200, "POST", "/v1/stock/setup", nil)
	owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Indomie", "quantity": 10, "unit": "carton", "price_kobo": 1000000}, "Idempotency-Key", "i")
	owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 5, "unit": "bag", "reorder_level": 5}, "Idempotency-Key", "r")

	if r := w.say(t, on, "help"); !strings.Contains(r, "*sold 3 bags of rice 45,000 cash*") {
		t.Errorf("help: %q", r)
	}
	if r := w.say(t, on, "sold 3 cartons of indomie 30,000 cash"); !strings.Contains(r, "Sold 3 cartons Indomie for ₦30,000, in cash?") {
		t.Fatalf("confirm: %q", r)
	}
	r := w.yes(t, on)
	if !strings.Contains(r, ": 3 cartons Indomie, ₦30,000 in cash.") || !strings.Contains(r, "Recorded S-") {
		t.Fatalf("recorded: %q", r)
	}
	if r := w.say(t, on, "sold 2 rice"); !strings.Contains(r, "*Rice* has no price") {
		t.Fatalf("no price: %q", r)
	}
	if r := w.say(t, on, "sold 6 bags rice 270,000 transfer"); !strings.Contains(r, "Sold 6 bags Rice for ₦270,000, by transfer?") || !strings.Contains(r, "more Rice than Taskiem counts") {
		t.Fatalf("beyond: %q", r)
	}
	if r := w.yes(t, on); !strings.Contains(r, "Waiting for the transfer") || !strings.Contains(r, "You sold more Rice than Taskiem counted") {
		t.Fatalf("waiting: %q", r)
	}
	if r := w.say(t, on, "sold bread 500"); !strings.Contains(r, "Nothing in your catalogue is called bread") {
		t.Fatalf("unknown: %q", r)
	}
	// In Pidgin, after its sale word.
	w.say(t, pn, "language pidgin")
	pcm := func(id string, kv ...string) string { return lang.Default().Text(lang.PCM, id, kv...) }
	if r := w.say(t, pn, "I don sell 1 carton indomie 10,000 transfer"); !strings.Contains(r, pcm("wa.sale.confirm", "items", "1 carton Indomie", "amount", "₦10,000", "how", pcm("sales.how_transfer"))) {
		t.Fatalf("pidgin: %q", r)
	}
	if r := w.say(t, pn, "yes"); !strings.Contains(r, pcm("wa.sale.waiting_transfer")) {
		t.Fatalf("pidgin recorded: %q", r)
	}

	// The evening total, once, after closing.
	now := time.Now().In(storefront.Lagos)
	at := time.Date(now.Year(), now.Month(), now.Day(), 21, 30, 0, 0, storefront.Lagos)
	if now.After(at) {
		at = now
	}
	evening := &stock.Evening{Pool: w.env.DB.App, Channels: []string{"whatsapp"}, Now: func() time.Time { return at }}
	if sent, err := evening.Tenant(ctx, tenant); err != nil || !sent {
		t.Fatalf("evening: %v %v", sent, err)
	}
	if sent, _ := evening.Tenant(ctx, tenant); sent {
		t.Fatal("the evening total twice")
	}
	before := len(w.graph.Sent(on))
	w.tick(t)
	got := w.lastTo(t, on, before)
	if len(got) != 1 || !strings.Contains(got[0].Text, "*Today's sales*: ₦310,000 from 3 sales.") || !strings.Contains(got[0].Text, "Cash ₦30,000 · Transfer ₦280,000") ||
		!strings.Contains(got[0].Text, "2 sales by transfer or on the terminal are waiting for their money") || !strings.Contains(got[0].Text, "Sold most:") {
		t.Fatalf("evening total: %+v", got)
	}
	// The next morning's money summary says the day's sales.
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 8, 5, 0, 0, storefront.Lagos)
	digest := &moneydigest.Digester{Pool: w.env.DB.App, Channels: []string{"whatsapp"}, Now: func() time.Time { return tomorrow }}
	if _, err := digest.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	before = len(w.graph.Sent(on))
	w.tick(t)
	if got := w.lastTo(t, on, before); len(got) != 1 || !strings.Contains(got[0].Text, "Sales you recorded: ₦310,000 from 3 sales (Cash ₦30,000 · Transfer ₦280,000).") ||
		!strings.Contains(got[0].Text, "Low on stock: Rice (0 bags, order 10 bags).") {
		t.Fatalf("morning summary: %+v", got)
	}
	// Before closing, nothing.
	early := &stock.Evening{Pool: w.env.DB.App, Channels: []string{"whatsapp"}, Now: func() time.Time { return at.AddDate(0, 0, 1).Add(-12 * time.Hour) }}
	if sent, err := early.Tenant(ctx, tenant); err != nil || sent {
		t.Fatalf("before closing: %v %v", sent, err)
	}
}

func TestEveningText(t *testing.T) {
	d := stock.Day{TotalKobo: 4500000, Count: 2, ByHow: map[string]int64{"cash": 4000000, "pos": 500000}, Top: []stock.TopItem{{Item: "Rice", Unit: "bag", Quantity: 2}},
		Unsold: []stock.Unsold{{AmountKobo: 210000}, {AmountKobo: 5000}}}
	two := int64(2)
	five := int64(5)
	text := api.EveningText(lang.EN, d, []stock.Item{{Name: "Garri", Unit: "bag", Stock: &two, Reorder: &five}})
	for _, want := range []string{"*Today's sales*: ₦45,000 from 2 sales.", "Cash ₦40,000 · Terminal ₦5,000", "Sold most: 2 bags Rice.",
		"2 terminal sales, ₦2,150, have no items", "Low now: Garri (2 bags)."} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
}

package stock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	conn "github.com/israel-duff/taskiem/connectors/stock"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/stock"
)

func n(v int64) *int64 { return &v }

type ops struct {
	tenant  uuid.UUID
	moved   stock.Move
	filter  stock.Filter
	sold    []stock.SellLine
	soldKey string
}

var items = []stock.Item{
	{ID: "1", Name: "Indomie", Stock: n(4), Unit: "carton", Reorder: n(10), Target: n(50), Supplier: "Dangote Depot", CostKobo: n(850000)},
	{ID: "2", Name: "Rice", Stock: n(30), Unit: "bag", Reorder: n(5), Supplier: "Mama Rice"},
}

func (o *ops) Items(_ context.Context, tenant uuid.UUID) ([]stock.Item, error) {
	o.tenant = tenant
	return items, nil
}

func (o *ops) Item(_ context.Context, _ uuid.UUID, id string) (stock.Item, error) {
	for _, it := range items {
		if it.ID == id {
			return it, nil
		}
	}
	return stock.Item{}, stock.ErrNoItem
}

func (o *ops) Move(_ context.Context, _ uuid.UUID, mv stock.Move) (stock.Movement, error) {
	o.moved = mv
	return stock.Movement{ID: uuid.New(), ItemID: mv.ItemID, Item: mv.Item, Reason: mv.Reason, Status: "done", Before: n(4), After: n(9), Change: n(5),
		CreatedAt: time.Now()}, nil
}

func (o *ops) Movements(_ context.Context, _ uuid.UUID, f stock.Filter) ([]stock.Movement, error) {
	o.filter = f
	return []stock.Movement{{ID: uuid.New(), ItemID: "1", Item: "Indomie", Reason: stock.Sale, Status: "done", Change: n(-2), CreatedAt: time.Now()}}, nil
}

func (o *ops) Sell(_ context.Context, _ uuid.UUID, lines []stock.SellLine, amount *int64, paidHow, note, actor, key string) (stock.CounterSale, error) {
	o.sold, o.soldKey = lines, key
	return stock.CounterSale{ID: uuid.New(), Reference: "S-7QX4KD", AmountKobo: *amount, PaidHow: paidHow, Status: "recorded", Actor: actor}, nil
}

// The purchase operations (a Buyer).
type buyer struct {
	ops
	ask  stock.DraftAsk
	sent string
	got  map[string]int64
}

var po = stock.Purchase{ID: uuid.New(), Reference: "P-K2M9PA", Supplier: "Dangote Depot", Status: stock.Drafted, TotalKobo: 39100000,
	Lines: []stock.PurchaseLine{{ItemID: "1", Item: "Indomie", Unit: "carton", Quantity: 46, CostKobo: 850000}}}

func (b *buyer) DraftPurchase(_ context.Context, _ uuid.UUID, a stock.DraftAsk) ([]stock.Purchase, []string, error) {
	b.ask = a
	return []stock.Purchase{po}, []string{"Garri"}, nil
}

func (b *buyer) SendPurchase(_ context.Context, _ uuid.UUID, ref, _ string) (stock.Purchase, error) {
	b.sent = ref
	x := po
	x.Status = stock.Sent
	return x, nil
}

func (b *buyer) ReceivePurchase(_ context.Context, _ uuid.UUID, _ string, got map[string]int64, _, _ string) (stock.Purchase, error) {
	b.got = got
	x := po
	x.Status = stock.PartReceived
	return x, nil
}

func (b *buyer) ListPurchases(context.Context, uuid.UUID, stock.PurchaseFilter) ([]stock.Purchase, error) {
	return []stock.Purchase{po}, nil
}

func TestPurchaseActions(t *testing.T) {
	b := &buyer{}
	c := conn.New(b)
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: uuid.New(), Run: uuid.New()})
	if out, err := call(t, c, ctx, "low_items", map[string]any{"supplier": "dangote depot"}, ""); err != nil || out["count"] != 1 {
		t.Fatalf("low: %v %v", out, err)
	}
	out, err := call(t, c, ctx, "draft_purchase", map[string]any{"supplier": "Dangote"}, "tsk_abcdefghijklmnop")
	if err != nil || out["count"] != 1 || b.ask.Supplier != "Dangote" || b.ask.Key != "wf-tsk_abcdefghijklmnop" || len(b.ask.Lines) != 0 ||
		out["purchases"].([]any)[0].(map[string]any)["items"] != "46 cartons Indomie" || out["no_supplier"].([]string)[0] != "Garri" {
		t.Fatalf("draft: %v %v %+v", out, err, b.ask)
	}
	if _, err := call(t, c, ctx, "draft_purchase", map[string]any{"supplier": "Dangote", "lines": []any{map[string]any{"id": "1", "quantity": float64(5)}}},
		"tsk_abcdefghijklmnoq"); err != nil || len(b.ask.Lines) != 1 || b.ask.Lines[0].Quantity != 5 {
		t.Fatalf("draft lines: %v %+v", err, b.ask)
	}
	if out, err := call(t, c, ctx, "send_purchase", map[string]any{"reference": "P-K2M9PA"}, "tsk_abcdefghijklmnor"); err != nil || out["status"] != stock.Sent || b.sent != "P-K2M9PA" {
		t.Fatalf("send: %v %v", out, err)
	}
	if out, err := call(t, c, ctx, "receive", map[string]any{"id": "1", "quantity": float64(20), "purchase": "P-K2M9PA"}, "tsk_abcdefghijklmnos"); err != nil ||
		b.got["1"] != 20 || out["purchase"].(map[string]any)["status"] != stock.PartReceived {
		t.Fatalf("receive: %v %v", out, err)
	}
	if out, err := call(t, c, ctx, "list_purchases", map[string]any{"supplier": "dangote depot"}, ""); err != nil || out["count"] != 1 {
		t.Fatalf("list: %v %v", out, err)
	}
	// Without the purchase operations: refused plainly.
	if _, err := call(t, conn.New(&ops{}), ctx, "send_purchase", map[string]any{"reference": "P-K2M9PA"}, "tsk_abcdefghijklmnot"); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("no buyer: %v", err)
	}
	if _, err := call(t, conn.New(&conn.Late{}), ctx, "list_purchases", nil, ""); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("unbound: %v", err)
	}
}

func (o *ops) SalesDay(_ context.Context, _ uuid.UUID, day time.Time) (stock.Day, error) {
	return stock.Day{Day: day.Format("2006-01-02"), TotalKobo: 4500000, Count: 2, ByHow: map[string]int64{stock.Cash: 4000000, stock.POS: 500000},
		Top: []stock.TopItem{{Item: "Rice", Unit: "bag", Quantity: 2}}}, nil
}

func TestSalesDay(t *testing.T) {
	c := conn.New(&ops{})
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: uuid.New(), Run: uuid.New()})
	out, err := call(t, c, ctx, "sales_day", map[string]any{"day": "2026-10-29"}, "")
	if err != nil || out["day"] != "2026-10-29" || out["total_kobo"] != int64(4500000) || out["cash_kobo"] != int64(4000000) || out["transfer_kobo"] != int64(0) ||
		out["top"] != "2 bags Rice" {
		t.Fatalf("day: %v %v", out, err)
	}
	if _, err := call(t, c, ctx, "sales_day", map[string]any{"day": "yesterday"}, ""); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("a bad day: %v", err)
	}
}

func call(t *testing.T, c *connector.Connector, ctx context.Context, action string, in map[string]any, key string) (map[string]any, error) {
	t.Helper()
	resp, err := c.Actions[action].Execute(ctx, connector.Request{Input: in, IdempotencyKey: key})
	if err != nil {
		return nil, err
	}
	return resp.Output.(map[string]any), nil
}

func TestStockConnector(t *testing.T) {
	o := &ops{}
	c := conn.New(o)
	tenant, run := uuid.New(), uuid.New()
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: tenant, Run: run})

	out, err := call(t, c, ctx, "list_items", map[string]any{"low": true}, "")
	if err != nil || out["count"] != 1 || o.tenant != tenant {
		t.Fatalf("low: %v %v", out, err)
	}
	if it := out["items"].([]any)[0].(map[string]any); it["name"] != "Indomie" || it["suggest"] != int64(46) || it["low"] != true || it["cost_kobo"] != int64(850000) {
		t.Fatalf("item: %v", it)
	}
	if out, _ := call(t, c, ctx, "list_items", map[string]any{"supplier": "mama rice"}, ""); out["count"] != 1 {
		t.Fatalf("by supplier: %v", out)
	}
	if out, _ := call(t, c, ctx, "list_items", map[string]any{"name": "ric"}, ""); out["count"] != 1 {
		t.Fatalf("by name: %v", out)
	}
	if _, err := call(t, c, ctx, "get_item", map[string]any{"id": "9"}, ""); !errors.Is(err, effects.ErrFatal) {
		t.Fatalf("no item: %v", err)
	}

	m, err := call(t, c, ctx, "receive", map[string]any{"id": "1", "quantity": float64(5), "reference": "PO-12"}, "tsk_abcdefghijklmnop")
	if err != nil || m["after"] != int64(9) || o.moved.Reason != stock.Received || *o.moved.Delta != 5 || o.moved.Reference != "PO-12" ||
		o.moved.Key != "wf-tsk_abcdefghijklmnop" || o.moved.Actor != "workflow:"+run.String() || o.moved.Item != "Indomie" {
		t.Fatalf("receive: %v %v %+v", m, err, o.moved)
	}
	if _, err := call(t, c, ctx, "adjust", map[string]any{"id": "1", "change": float64(-2), "note": "spoilt"}, "tsk_abcdefghijklmnoq"); err != nil ||
		o.moved.Reason != stock.Adjustment || *o.moved.Delta != -2 || o.moved.Note != "spoilt" {
		t.Fatalf("adjust: %v %+v", err, o.moved)
	}
	if _, err := call(t, c, ctx, "count", map[string]any{"id": "2", "count": float64(28)}, "tsk_abcdefghijklmnor"); err != nil || *o.moved.SetTo != 28 {
		t.Fatalf("count: %v %+v", err, o.moved)
	}
	for _, bad := range []struct {
		action string
		in     map[string]any
	}{{"adjust", map[string]any{"id": "1", "change": float64(0)}}, {"receive", map[string]any{"id": "1", "quantity": float64(-1)}},
		{"count", map[string]any{"id": "1", "count": 2.5}}} {
		if _, err := call(t, c, ctx, bad.action, bad.in, "tsk_abcdefghijklmnos"); !errors.Is(err, effects.ErrFatal) {
			t.Errorf("%s %v: %v", bad.action, bad.in, err)
		}
	}
	if _, err := call(t, c, ctx, "adjust", map[string]any{"id": "1", "change": float64(1)}, ""); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("no key: %v", err)
	}
	list, err := call(t, c, ctx, "movements", map[string]any{"id": "1", "since": "2026-10-01T00:00:00Z", "limit": float64(10)}, "")
	if err != nil || list["count"] != 1 || o.filter.ItemID != "1" || o.filter.Limit != 10 || o.filter.Since == nil {
		t.Fatalf("movements: %v %v %+v", list, err, o.filter)
	}

	sale, err := call(t, c, ctx, "record_sale", map[string]any{"lines": []any{map[string]any{"id": "1", "quantity": float64(2), "price_kobo": float64(950000)}},
		"amount_kobo": float64(1900000), "paid_how": "cash"}, "tsk_abcdefghijklmnot")
	if err != nil || sale["reference"] != "S-7QX4KD" || len(o.sold) != 1 || o.sold[0].Quantity != 2 || *o.sold[0].PriceKobo != 950000 || o.soldKey != "wf-tsk_abcdefghijklmnot" {
		t.Fatalf("sale: %v %v %+v", sale, err, o.sold)
	}
	if _, err := call(t, c, ctx, "record_sale", map[string]any{"lines": []any{map[string]any{"id": "1"}}, "paid_how": "cash"}, "tsk_abcdefghijklmnou"); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("a line with no quantity: %v", err)
	}

	// Outside a run, or unbound: refused plainly.
	if _, err := call(t, c, context.Background(), "list_items", nil, ""); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("outside a run: %v", err)
	}
	if _, err := call(t, conn.New(&conn.Late{}), ctx, "list_items", nil, ""); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("unbound: %v", err)
	}
}

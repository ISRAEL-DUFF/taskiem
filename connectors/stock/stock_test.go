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
	tenant uuid.UUID
	moved  stock.Move
	filter stock.Filter
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

	// Outside a run, or unbound: refused plainly.
	if _, err := call(t, c, context.Background(), "list_items", nil, ""); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("outside a run: %v", err)
	}
	if _, err := call(t, conn.New(&conn.Late{}), ctx, "list_items", nil, ""); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("unbound: %v", err)
	}
}

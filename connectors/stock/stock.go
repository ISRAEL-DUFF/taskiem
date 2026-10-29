// Package stock is the stock platform connector (docs/stock.md, decision
// 0057): what workflows read and do with a shop's stock (its items, their
// counts, adjustments, goods received, counts, and the movements that
// explain them). Like riders@1 it acts on Taskiem's own records, for the
// tenant of the run calling it (connector.CallerFrom), never one named in
// its input.
package stock

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/stock"
)

//go:embed manifest.yaml
var manifest []byte

// Ops is what the actions call: *stock.Stock.
type Ops interface {
	Items(ctx context.Context, tenant uuid.UUID) ([]stock.Item, error)
	Item(ctx context.Context, tenant uuid.UUID, id string) (stock.Item, error)
	Move(ctx context.Context, tenant uuid.UUID, mv stock.Move) (stock.Movement, error)
	Movements(ctx context.Context, tenant uuid.UUID, f stock.Filter) ([]stock.Movement, error)
	Sell(ctx context.Context, tenant uuid.UUID, lines []stock.SellLine, amount *int64, paidHow, note, actor, key string) (stock.CounterSale, error)
}

// Buyer is the purchase actions' operations: *stock.Purchases. nil, or a
// Late with none set, makes them fail plainly.
type Buyer interface {
	DraftPurchase(ctx context.Context, tenant uuid.UUID, a stock.DraftAsk) ([]stock.Purchase, []string, error)
	SendPurchase(ctx context.Context, tenant uuid.UUID, ref, actor string) (stock.Purchase, error)
	ReceivePurchase(ctx context.Context, tenant uuid.UUID, ref string, got map[string]int64, actor, key string) (stock.Purchase, error)
	ListPurchases(ctx context.Context, tenant uuid.UUID, f stock.PurchaseFilter) ([]stock.Purchase, error)
}

// New returns the connector; ops nil makes every action fail plainly. The
// purchase actions use ops when it is also a Buyer (Late is).
func New(ops Ops) *connector.Connector {
	m := connector.MustParse(manifest)
	a := &actions{ops: ops}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"list_items":     connector.ActionFunc(a.list),
		"get_item":       connector.ActionFunc(a.get),
		"adjust":         connector.ActionFunc(a.adjust),
		"receive":        connector.ActionFunc(a.receive),
		"count":          connector.ActionFunc(a.count),
		"movements":      connector.ActionFunc(a.movements),
		"record_sale":    connector.ActionFunc(a.sell),
		"low_items":      connector.ActionFunc(a.low),
		"draft_purchase": connector.ActionFunc(a.draft),
		"send_purchase":  connector.ActionFunc(a.send),
		"list_purchases": connector.ActionFunc(a.purchases),
	}}
}

type actions struct{ ops Ops }

func (a *actions) caller(ctx context.Context) (connector.Caller, error) {
	c, ok := connector.CallerFrom(ctx)
	if !ok {
		return c, fmt.Errorf("stock actions run only in a workflow run: %w", effects.ErrFatal)
	}
	if a.ops == nil {
		return c, fmt.Errorf("stock is not available on this deployment: %w", effects.ErrFatal)
	}
	if l, ok := a.ops.(*Late); ok && !l.set() {
		return c, fmt.Errorf("stock is not available on this deployment: %w", effects.ErrFatal)
	}
	return c, nil
}

func (a *actions) list(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	items, err := a.ops.Items(ctx, c.Tenant)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	low, _ := req.Input["low"].(bool)
	supplier, _ := req.Input["supplier"].(string)
	name, _ := req.Input["name"].(string)
	if low {
		items = stock.Low(items)
	}
	out := []any{}
	for _, it := range items {
		if supplier != "" && !strings.EqualFold(strings.TrimSpace(it.Supplier), strings.TrimSpace(supplier)) {
			continue
		}
		if name != "" && !strings.Contains(strings.ToLower(it.Name), strings.ToLower(strings.TrimSpace(name))) {
			continue
		}
		out = append(out, ItemOut(it))
	}
	return connector.Response{Output: map[string]any{"items": out, "count": len(out)}}, nil
}

func (a *actions) get(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	id, _ := req.Input["id"].(string)
	it, err := a.ops.Item(ctx, c.Tenant, id)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	return connector.Response{Output: ItemOut(it)}, nil
}

func (a *actions) move(ctx context.Context, req connector.Request, reason string, delta, setTo *int64) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	if req.IdempotencyKey == "" {
		return connector.Response{}, fmt.Errorf("a stock change needs its idempotency key: %w", effects.ErrFatal)
	}
	id, _ := req.Input["id"].(string)
	it, err := a.ops.Item(ctx, c.Tenant, id)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	note, _ := req.Input["note"].(string)
	ref, _ := req.Input["reference"].(string)
	m, err := a.ops.Move(ctx, c.Tenant, stock.Move{ItemID: it.ID, Item: it.Name, Reason: reason, Delta: delta, SetTo: setTo, Note: note,
		Reference: ref, Actor: "workflow:" + c.Run.String(), Key: "wf-" + req.IdempotencyKey})
	if err != nil {
		return connector.Response{}, classify(err)
	}
	return connector.Response{Output: MovementOut(m)}, nil
}

func (a *actions) adjust(ctx context.Context, req connector.Request) (connector.Response, error) {
	n, ok := number(req.Input["change"])
	if !ok || n == 0 {
		return connector.Response{}, fmt.Errorf("change is a whole number, not 0: %w", effects.ErrFatal)
	}
	return a.move(ctx, req, stock.Adjustment, &n, nil)
}

func (a *actions) receive(ctx context.Context, req connector.Request) (connector.Response, error) {
	n, ok := number(req.Input["quantity"])
	if !ok || n <= 0 {
		return connector.Response{}, fmt.Errorf("quantity is a whole number above 0: %w", effects.ErrFatal)
	}
	// Against a purchase order: received on it, naming it.
	if ref, _ := req.Input["purchase"].(string); ref != "" {
		c, b, err := a.buyer(ctx)
		if err != nil {
			return connector.Response{}, err
		}
		if req.IdempotencyKey == "" {
			return connector.Response{}, fmt.Errorf("a stock change needs its idempotency key: %w", effects.ErrFatal)
		}
		id, _ := req.Input["id"].(string)
		x, err := b.ReceivePurchase(ctx, c.Tenant, ref, map[string]int64{id: n}, "workflow:"+c.Run.String(), "wf-"+req.IdempotencyKey)
		if err != nil {
			return connector.Response{}, classify(err)
		}
		return connector.Response{Output: map[string]any{"item_id": id, "reason": stock.Received, "status": "done", "change": n, "reference": x.Reference,
			"purchase": PurchaseOut(x)}}, nil
	}
	return a.move(ctx, req, stock.Received, &n, nil)
}

// buyer is the purchase operations, or why there are none.
func (a *actions) buyer(ctx context.Context) (connector.Caller, Buyer, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return c, nil, err
	}
	b, ok := a.ops.(Buyer)
	if l, isLate := a.ops.(*Late); isLate && !l.buying() {
		ok = false
	}
	if !ok {
		return c, nil, fmt.Errorf("purchase orders are not available on this deployment: %w", effects.ErrFatal)
	}
	return c, b, nil
}

func (a *actions) low(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	items, err := a.ops.Items(ctx, c.Tenant)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	supplier, _ := req.Input["supplier"].(string)
	out := []any{}
	for _, it := range stock.Low(items) {
		if supplier != "" && !strings.EqualFold(strings.TrimSpace(it.Supplier), strings.TrimSpace(supplier)) {
			continue
		}
		out = append(out, ItemOut(it))
	}
	return connector.Response{Output: map[string]any{"items": out, "count": len(out)}}, nil
}

func (a *actions) draft(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, b, err := a.buyer(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	if req.IdempotencyKey == "" {
		return connector.Response{}, fmt.Errorf("drafting an order needs its idempotency key: %w", effects.ErrFatal)
	}
	ask := stock.DraftAsk{Actor: "workflow:" + c.Run.String(), Key: "wf-" + req.IdempotencyKey}
	ask.Supplier, _ = req.Input["supplier"].(string)
	ask.Note, _ = req.Input["note"].(string)
	list, _ := req.Input["lines"].([]any)
	for _, x := range list {
		m, _ := x.(map[string]any)
		id, _ := m["id"].(string)
		q, ok := number(m["quantity"])
		if id == "" || !ok || q <= 0 {
			return connector.Response{}, fmt.Errorf("each line is an item's id and how many: %w", effects.ErrFatal)
		}
		l := stock.PurchaseLine{ItemID: id, Quantity: q}
		if p, ok := number(m["cost_kobo"]); ok {
			l.CostKobo = p
		}
		ask.Lines = append(ask.Lines, l)
	}
	drafted, skipped, err := b.DraftPurchase(ctx, c.Tenant, ask)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	out := make([]any, len(drafted))
	for i, x := range drafted {
		out[i] = PurchaseOut(x)
	}
	if skipped == nil {
		skipped = []string{}
	}
	return connector.Response{Output: map[string]any{"purchases": out, "count": len(out), "no_supplier": skipped}}, nil
}

func (a *actions) send(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, b, err := a.buyer(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	ref, _ := req.Input["reference"].(string)
	x, err := b.SendPurchase(ctx, c.Tenant, ref, "workflow:"+c.Run.String())
	if err != nil {
		return connector.Response{}, classify(err)
	}
	return connector.Response{Output: PurchaseOut(x)}, nil
}

func (a *actions) purchases(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, b, err := a.buyer(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	f := stock.PurchaseFilter{}
	f.Open, _ = req.Input["open"].(bool)
	if n, ok := number(req.Input["limit"]); ok {
		f.Limit = int(n)
	}
	list, err := b.ListPurchases(ctx, c.Tenant, f)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	supplier, _ := req.Input["supplier"].(string)
	out := []any{}
	for _, x := range list {
		if supplier != "" && !strings.EqualFold(x.Supplier, strings.TrimSpace(supplier)) {
			continue
		}
		out = append(out, PurchaseOut(x))
	}
	return connector.Response{Output: map[string]any{"purchases": out, "count": len(out)}}, nil
}

// PurchaseOut is a purchase order as workflows read it.
func PurchaseOut(x stock.Purchase) map[string]any {
	lines := make([]any, len(x.Lines))
	for i, l := range x.Lines {
		lines[i] = map[string]any{"id": l.ItemID, "item": l.Item, "unit": l.Unit, "quantity": l.Quantity, "cost_kobo": l.CostKobo, "received": l.Received}
	}
	return map[string]any{"id": x.ID.String(), "reference": x.Reference, "supplier": x.Supplier, "status": x.Status, "lines": lines,
		"total_kobo": x.TotalKobo, "received_kobo": x.ReceivedKobo, "paid_kobo": x.PaidKobo, "owed_kobo": x.OwedKobo, "items": x.Words()}
}

func (a *actions) count(ctx context.Context, req connector.Request) (connector.Response, error) {
	n, ok := number(req.Input["count"])
	if !ok || n < 0 {
		return connector.Response{}, fmt.Errorf("count is a whole number, 0 or more: %w", effects.ErrFatal)
	}
	return a.move(ctx, req, stock.Count, nil, &n)
}

func (a *actions) movements(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	f := stock.Filter{}
	f.ItemID, _ = req.Input["id"].(string)
	f.Reason, _ = req.Input["reason"].(string)
	if n, ok := number(req.Input["limit"]); ok {
		f.Limit = int(n)
	}
	if s, _ := req.Input["since"].(string); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return connector.Response{}, fmt.Errorf("since is a time like 2026-10-29T00:00:00Z: %w", effects.ErrFatal)
		}
		f.Since = &t
	}
	list, err := a.ops.Movements(ctx, c.Tenant, f)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	out := make([]any, len(list))
	for i, m := range list {
		out[i] = MovementOut(m)
	}
	return connector.Response{Output: map[string]any{"movements": out, "count": len(out)}}, nil
}

func (a *actions) sell(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	if req.IdempotencyKey == "" {
		return connector.Response{}, fmt.Errorf("a sale needs its idempotency key: %w", effects.ErrFatal)
	}
	list, _ := req.Input["lines"].([]any)
	var lines []stock.SellLine
	for _, x := range list {
		m, _ := x.(map[string]any)
		id, _ := m["id"].(string)
		q, ok := number(m["quantity"])
		if id == "" || !ok || q <= 0 {
			return connector.Response{}, fmt.Errorf("each line is an item's id and how many: %w", effects.ErrFatal)
		}
		l := stock.SellLine{ItemID: id, Quantity: q}
		if p, ok := number(m["price_kobo"]); ok {
			l.PriceKobo = &p
		}
		lines = append(lines, l)
	}
	var amount *int64
	if n, ok := number(req.Input["amount_kobo"]); ok {
		amount = &n
	}
	how, _ := req.Input["paid_how"].(string)
	note, _ := req.Input["note"].(string)
	x, err := a.ops.Sell(ctx, c.Tenant, lines, amount, how, note, "workflow:"+c.Run.String(), "wf-"+req.IdempotencyKey)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	short := x.Short
	if short == nil {
		short = []string{}
	}
	return connector.Response{Output: map[string]any{"id": x.ID.String(), "reference": x.Reference, "amount_kobo": x.AmountKobo, "paid_how": x.PaidHow,
		"status": x.Status, "short": short}}, nil
}

// ItemOut is an item as workflows read it.
func ItemOut(it stock.Item) map[string]any {
	out := map[string]any{"id": it.ID, "name": it.Name, "low": it.Low()}
	for k, v := range map[string]*int64{"stock": it.Stock, "price_kobo": it.PriceKobo, "cost_kobo": it.CostKobo, "reorder_level": it.Reorder,
		"target_level": it.Target} {
		if v != nil {
			out[k] = *v
		}
	}
	if it.Unit != "" {
		out["unit"] = it.Unit
	}
	if it.Supplier != "" {
		out["supplier"] = it.Supplier
	}
	if it.Low() {
		out["suggest"] = it.Suggest()
	}
	return out
}

// MovementOut is a movement as workflows read it.
func MovementOut(m stock.Movement) map[string]any {
	out := map[string]any{"id": m.ID.String(), "item_id": m.ItemID, "item": m.Item, "reason": m.Reason, "status": m.Status}
	for k, v := range map[string]*int64{"change": m.Change, "before": m.Before, "after": m.After} {
		if v != nil {
			out[k] = *v
		}
	}
	if m.Short > 0 {
		out["short"] = m.Short
	}
	if m.Note != "" {
		out["note"] = m.Note
	}
	if m.Reference != "" {
		out["reference"] = m.Reference
	}
	at := m.CreatedAt
	if m.DoneAt != nil {
		at = *m.DoneAt
	}
	out["at"] = at.UTC().Format(time.RFC3339)
	return out
}

func number(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), n == float64(int64(n))
	case int64:
		return n, true
	case int:
		return int64(n), true
	}
	return 0, false
}

func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, stock.ErrInvalid), errors.Is(err, stock.ErrNoItem), errors.Is(err, stock.ErrNotTracked), errors.Is(err, stock.ErrSaleInvalid),
		errors.Is(err, stock.ErrEntryTaken), errors.Is(err, stock.ErrPurchase), errors.Is(err, stock.ErrNoPurchase), errors.Is(err, stock.ErrNoSupplier),
		errors.Is(err, stock.ErrSecondApproval):
		return fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	return err
}

// Late is Ops set after the connector is registered: the registry is built
// before the vault and the database stock needs.
type Late struct {
	mu    sync.RWMutex
	ops   Ops
	buyer Buyer
}

// SetBuyer binds the purchase operations.
func (l *Late) SetBuyer(b Buyer) { l.mu.Lock(); l.buyer = b; l.mu.Unlock() }

func (l *Late) buying() bool { l.mu.RLock(); defer l.mu.RUnlock(); return l.buyer != nil }

func (l *Late) getBuyer() Buyer { l.mu.RLock(); defer l.mu.RUnlock(); return l.buyer }

func (l *Late) DraftPurchase(ctx context.Context, tenant uuid.UUID, a stock.DraftAsk) ([]stock.Purchase, []string, error) {
	return l.getBuyer().DraftPurchase(ctx, tenant, a)
}

func (l *Late) SendPurchase(ctx context.Context, tenant uuid.UUID, ref, actor string) (stock.Purchase, error) {
	return l.getBuyer().SendPurchase(ctx, tenant, ref, actor)
}

func (l *Late) ReceivePurchase(ctx context.Context, tenant uuid.UUID, ref string, got map[string]int64, actor, key string) (stock.Purchase, error) {
	return l.getBuyer().ReceivePurchase(ctx, tenant, ref, got, actor, key)
}

func (l *Late) ListPurchases(ctx context.Context, tenant uuid.UUID, f stock.PurchaseFilter) ([]stock.Purchase, error) {
	return l.getBuyer().ListPurchases(ctx, tenant, f)
}

// Set binds the operations.
func (l *Late) Set(ops Ops) { l.mu.Lock(); l.ops = ops; l.mu.Unlock() }

func (l *Late) set() bool { l.mu.RLock(); defer l.mu.RUnlock(); return l.ops != nil }

func (l *Late) get() Ops { l.mu.RLock(); defer l.mu.RUnlock(); return l.ops }

func (l *Late) Items(ctx context.Context, tenant uuid.UUID) ([]stock.Item, error) {
	return l.get().Items(ctx, tenant)
}

func (l *Late) Item(ctx context.Context, tenant uuid.UUID, id string) (stock.Item, error) {
	return l.get().Item(ctx, tenant, id)
}

func (l *Late) Move(ctx context.Context, tenant uuid.UUID, mv stock.Move) (stock.Movement, error) {
	return l.get().Move(ctx, tenant, mv)
}

func (l *Late) Movements(ctx context.Context, tenant uuid.UUID, f stock.Filter) ([]stock.Movement, error) {
	return l.get().Movements(ctx, tenant, f)
}

func (l *Late) Sell(ctx context.Context, tenant uuid.UUID, lines []stock.SellLine, amount *int64, paidHow, note, actor, key string) (stock.CounterSale, error) {
	return l.get().Sell(ctx, tenant, lines, amount, paidHow, note, actor, key)
}

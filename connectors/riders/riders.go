// Package riders is the riders platform connector (docs/dispatch.md,
// decision 0055): what workflows read and do with the business's own
// riders (list, read, add, retire). Unlike provider connectors it acts on
// Taskiem's own records, for the tenant of the run calling it
// (connector.CallerFrom), never one named in its input.
package riders

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/riders"
	"github.com/israel-duff/taskiem/engine/storefront"
)

//go:embed manifest.yaml
var manifest []byte

// Ops is what the actions call: *riders.Riders.
type Ops interface {
	List(ctx context.Context, tenant uuid.UUID, f riders.Filter) ([]riders.Rider, error)
	Get(ctx context.Context, tenant, id uuid.UUID) (riders.Rider, error)
	Add(ctx context.Context, tenant uuid.UUID, in riders.In, actor string) (riders.Rider, error)
	Retire(ctx context.Context, tenant, id uuid.UUID, actor string) (riders.Rider, error)
}

// Dispatcher is what the dispatch actions call: *dispatch.Dispatch.
type Dispatcher interface {
	Offer(ctx context.Context, tenant uuid.UUID, reference string, in dispatch.OfferIn) (dispatch.OfferOut, error)
	Release(ctx context.Context, tenant uuid.UUID, reference, actor string) (dispatch.Delivery, error)
	PrepareBatch(ctx context.Context, tenant, batch uuid.UUID) (dispatch.Batch, error)
	FinishBatch(ctx context.Context, tenant, batch uuid.UUID, results []dispatch.Result, cancel bool) (int, int, error)
}

// New returns the connector; ops nil makes every action fail plainly. ops
// that is also a Dispatcher serves offer and release.
func New(ops Ops) *connector.Connector {
	m := connector.MustParse(manifest)
	a := &actions{ops: ops}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"list_riders":     connector.ActionFunc(a.list),
		"get_rider":       connector.ActionFunc(a.get),
		"add_rider":       connector.ActionFunc(a.add),
		"retire_rider":    connector.ActionFunc(a.retire),
		"offer":           connector.ActionFunc(a.offer),
		"release":         connector.ActionFunc(a.release),
		"prepare_payouts": connector.ActionFunc(a.prepare),
		"finish_payouts":  connector.ActionFunc(a.finish),
	}}
}

type actions struct{ ops Ops }

func (a *actions) dispatcher(ctx context.Context) (connector.Caller, Dispatcher, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return c, nil, err
	}
	d, ok := a.ops.(Dispatcher)
	if !ok {
		return c, nil, fmt.Errorf("deliveries with riders are not available on this deployment: %w", effects.ErrFatal)
	}
	if l, ok := d.(*Late); ok && !l.dispatching() {
		return c, nil, fmt.Errorf("deliveries with riders are not available on this deployment: %w", effects.ErrFatal)
	}
	return c, d, nil
}

func (a *actions) offer(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, d, err := a.dispatcher(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	ref, _ := req.Input["reference"].(string)
	in := dispatch.OfferIn{Ask: true, By: "workflow:" + c.Run.String()}
	if s, ok := req.Input["rider_id"].(string); ok && s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			return connector.Response{}, fmt.Errorf("rider_id is a rider's id: %w", effects.ErrFatal)
		}
		in.Rider = &id
	}
	if n, ok := number(req.Input["fee_kobo"]); ok {
		in.FeeKobo = &n
	}
	out, err := d.Offer(ctx, c.Tenant, ref, in)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	res := map[string]any{"reference": ref, "status": out.Status, "rider": out.Rider}
	if out.Delivery != nil {
		res["delivery_id"] = out.Delivery.ID.String()
	}
	return connector.Response{Output: res}, nil
}

// prepare makes the payout batch (the one named, or this run's) and
// answers who is paid what, with their bank accounts.
func (a *actions) prepare(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, d, err := a.dispatcher(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	batch := c.Run
	if s, ok := req.Input["batch_id"].(string); ok && s != "" {
		if batch, err = uuid.Parse(s); err != nil {
			return connector.Response{}, fmt.Errorf("batch_id is a payout batch's id: %w", effects.ErrFatal)
		}
	}
	b, err := d.PrepareBatch(ctx, c.Tenant, batch)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	payouts := make([]any, len(b.Payouts))
	for i, p := range b.Payouts {
		payouts[i] = map[string]any{"payout_id": p.ID.String(), "rider": p.Rider, "amount_kobo": p.AmountKobo, "fees_kobo": p.FeesKobo, "owed_kobo": p.OwedKobo,
			"deliveries": p.Deliveries, "bank_code": p.BankCode, "account_number": p.AccountNumber, "account_name": p.AccountName}
	}
	notPaid := make([]any, len(b.NotPaid))
	for i, n := range b.NotPaid {
		notPaid[i] = n
	}
	return connector.Response{Output: map[string]any{"batch_id": b.ID.String(), "payouts": payouts, "not_paid": notPaid, "total_kobo": b.TotalKobo,
		"count": len(payouts)}}, nil
}

// finish records how each payout went, or that the batch was not approved.
func (a *actions) finish(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, d, err := a.dispatcher(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	s, _ := req.Input["batch_id"].(string)
	batch, err := uuid.Parse(s)
	if err != nil {
		return connector.Response{}, fmt.Errorf("batch_id is a payout batch's id: %w", effects.ErrFatal)
	}
	cancel, _ := req.Input["cancel"].(bool)
	var results []dispatch.Result
	list, _ := req.Input["results"].([]any)
	for _, x := range list {
		m, _ := x.(map[string]any)
		id, err := uuid.Parse(fmt.Sprint(m["payout_id"]))
		if err != nil {
			continue
		}
		status, _ := m["status"].(string)
		name, _ := m["account_name"].(string)
		results = append(results, dispatch.Result{PayoutID: id, Status: status, AccountName: name})
	}
	paid, failed, err := d.FinishBatch(ctx, c.Tenant, batch, results, cancel)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	return connector.Response{Output: map[string]any{"batch_id": batch.String(), "paid": paid, "failed": failed}}, nil
}

func (a *actions) release(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, d, err := a.dispatcher(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	ref, _ := req.Input["reference"].(string)
	x, err := d.Release(ctx, c.Tenant, ref, "workflow:"+c.Run.String())
	if err != nil {
		return connector.Response{}, classify(err)
	}
	return connector.Response{Output: map[string]any{"reference": ref, "status": x.Status, "rider": x.Rider}}, nil
}

func (a *actions) caller(ctx context.Context) (connector.Caller, error) {
	c, ok := connector.CallerFrom(ctx)
	if !ok {
		return c, fmt.Errorf("riders actions run only in a workflow run: %w", effects.ErrFatal)
	}
	if a.ops == nil {
		return c, fmt.Errorf("riders are not available on this deployment: %w", effects.ErrFatal)
	}
	return c, nil
}

// Out is a rider as workflows read them.
func Out(x riders.Rider) map[string]any {
	out := map[string]any{"id": x.ID.String(), "name": x.Name, "phone": x.Phone, "zones": x.Zones, "fee_kobo": x.FeeKobo,
		"fee": storefront.FormatNaira(x.FeeKobo), "active": x.Active, "retired": x.RetiredAt != nil, "reached": x.Reached, "state": x.Presence.State}
	if x.Presence.State == "busy" && x.Presence.Until != nil {
		out["busy_until"] = x.Presence.Until.Format(time.RFC3339)
	}
	return out
}

func (a *actions) list(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	zone, _ := req.Input["zone"].(string)
	all, _ := req.Input["include_inactive"].(bool)
	list, err := a.ops.List(ctx, c.Tenant, riders.Filter{Zone: zone, ActiveOnly: !all})
	if err != nil {
		return connector.Response{}, classify(err)
	}
	out := make([]any, len(list))
	for i, x := range list {
		out[i] = Out(x)
	}
	return connector.Response{Output: map[string]any{"riders": out, "count": len(out)}}, nil
}

func riderID(req connector.Request) (uuid.UUID, error) {
	s, _ := req.Input["id"].(string)
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("a rider's id is required: %w", effects.ErrFatal)
	}
	return id, nil
}

func (a *actions) get(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	id, err := riderID(req)
	if err != nil {
		return connector.Response{}, err
	}
	x, err := a.ops.Get(ctx, c.Tenant, id)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	return connector.Response{Output: Out(x)}, nil
}

func (a *actions) add(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	var in riders.In
	if s, ok := req.Input["name"].(string); ok {
		in.Name = &s
	}
	if s, ok := req.Input["phone"].(string); ok {
		in.Phone = &s
	}
	if list, ok := req.Input["zones"].([]any); ok {
		zones := make([]string, 0, len(list))
		for _, z := range list {
			if s, ok := z.(string); ok {
				zones = append(zones, s)
			}
		}
		in.Zones = &zones
	}
	if n, ok := number(req.Input["fee_kobo"]); ok {
		in.FeeKobo = &n
	}
	x, err := a.ops.Add(ctx, c.Tenant, in, "workflow:"+c.Run.String())
	already := errors.Is(err, riders.ErrExists)
	if err != nil && !already {
		return connector.Response{}, classify(err)
	}
	out := Out(x)
	out["already"] = already
	return connector.Response{Output: out}, nil
}

func (a *actions) retire(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	id, err := riderID(req)
	if err != nil {
		return connector.Response{}, err
	}
	x, err := a.ops.Retire(ctx, c.Tenant, id, "workflow:"+c.Run.String())
	if err != nil {
		return connector.Response{}, classify(err)
	}
	return connector.Response{Output: Out(x)}, nil
}

// number reads a JSON number (float64 from decoding, or an int).
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
	case errors.Is(err, riders.ErrInvalid), errors.Is(err, riders.ErrNoRider), errors.Is(err, dispatch.ErrNotReady), errors.Is(err, dispatch.ErrInvalid),
		errors.Is(err, dispatch.ErrNoDelivery), errors.Is(err, dispatch.ErrMove), errors.Is(err, storefront.ErrNoOrder):
		return fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	return err
}

// Late is Ops set after the connector is registered: the registry is built
// before the vault and the database riders need.
type Late struct {
	mu  sync.RWMutex
	ops Ops
	dis Dispatcher
}

// Set binds the operations.
func (l *Late) Set(ops Ops) { l.mu.Lock(); l.ops = ops; l.mu.Unlock() }

// SetDispatch binds the dispatch actions.
func (l *Late) SetDispatch(d Dispatcher) { l.mu.Lock(); l.dis = d; l.mu.Unlock() }

func (l *Late) dispatching() bool { l.mu.RLock(); defer l.mu.RUnlock(); return l.dis != nil }

func (l *Late) Offer(ctx context.Context, tenant uuid.UUID, reference string, in dispatch.OfferIn) (dispatch.OfferOut, error) {
	l.mu.RLock()
	d := l.dis
	l.mu.RUnlock()
	if d == nil {
		return dispatch.OfferOut{}, fmt.Errorf("deliveries with riders are not available on this deployment: %w", effects.ErrFatal)
	}
	return d.Offer(ctx, tenant, reference, in)
}

func (l *Late) PrepareBatch(ctx context.Context, tenant, batch uuid.UUID) (dispatch.Batch, error) {
	l.mu.RLock()
	d := l.dis
	l.mu.RUnlock()
	if d == nil {
		return dispatch.Batch{}, fmt.Errorf("deliveries with riders are not available on this deployment: %w", effects.ErrFatal)
	}
	return d.PrepareBatch(ctx, tenant, batch)
}

func (l *Late) FinishBatch(ctx context.Context, tenant, batch uuid.UUID, results []dispatch.Result, cancel bool) (int, int, error) {
	l.mu.RLock()
	d := l.dis
	l.mu.RUnlock()
	if d == nil {
		return 0, 0, fmt.Errorf("deliveries with riders are not available on this deployment: %w", effects.ErrFatal)
	}
	return d.FinishBatch(ctx, tenant, batch, results, cancel)
}

func (l *Late) Release(ctx context.Context, tenant uuid.UUID, reference, actor string) (dispatch.Delivery, error) {
	l.mu.RLock()
	d := l.dis
	l.mu.RUnlock()
	if d == nil {
		return dispatch.Delivery{}, fmt.Errorf("deliveries with riders are not available on this deployment: %w", effects.ErrFatal)
	}
	return d.Release(ctx, tenant, reference, actor)
}

func (l *Late) get() (Ops, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.ops == nil {
		return nil, fmt.Errorf("riders are not available on this deployment: %w", effects.ErrFatal)
	}
	return l.ops, nil
}

func (l *Late) List(ctx context.Context, tenant uuid.UUID, f riders.Filter) ([]riders.Rider, error) {
	ops, err := l.get()
	if err != nil {
		return nil, err
	}
	return ops.List(ctx, tenant, f)
}

func (l *Late) Get(ctx context.Context, tenant, id uuid.UUID) (riders.Rider, error) {
	ops, err := l.get()
	if err != nil {
		return riders.Rider{}, err
	}
	return ops.Get(ctx, tenant, id)
}

func (l *Late) Add(ctx context.Context, tenant uuid.UUID, in riders.In, actor string) (riders.Rider, error) {
	ops, err := l.get()
	if err != nil {
		return riders.Rider{}, err
	}
	return ops.Add(ctx, tenant, in, actor)
}

func (l *Late) Retire(ctx context.Context, tenant, id uuid.UUID, actor string) (riders.Rider, error) {
	ops, err := l.get()
	if err != nil {
		return riders.Rider{}, err
	}
	return ops.Retire(ctx, tenant, id, actor)
}

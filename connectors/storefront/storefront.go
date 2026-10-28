// Package storefront is the storefront platform connector: the actions
// workflows take on the organisation's storefront orders and bookings
// (accept, decline, complete, a payment link, a message to the customer,
// reading a booking). Unlike provider
// connectors it acts on Taskiem's own records, for the tenant of the run
// calling it (connector.CallerFrom), never one named in its input.
package storefront

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/sender"
	sf "github.com/israel-duff/taskiem/engine/storefront"
)

//go:embed manifest.yaml
var manifest []byte

// Ops is what the actions call: *storefront.Orders.
type Ops interface {
	Decide(ctx context.Context, tenant uuid.UUID, reference, decision, note, actor string) (map[string]any, error)
	PaymentLink(ctx context.Context, tenant uuid.UUID, reference string) (map[string]any, error)
	NotifyCustomer(ctx context.Context, tenant uuid.UUID, reference, text, subject, key string) (map[string]any, error)
	GetBooking(ctx context.Context, tenant uuid.UUID, reference string) (map[string]any, error)
	Refund(ctx context.Context, tenant uuid.UUID, reference, actor string) (sf.Order, error)
	// CustomerLanguage is the business's language for its customers
	// (its default chat language), English when it has none.
	CustomerLanguage(ctx context.Context, tenant uuid.UUID) lang.Tag
}

// New returns the connector; ops nil makes every action fail plainly.
func New(ops Ops) *connector.Connector {
	m := connector.MustParse(manifest)
	a := &actions{ops: ops}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"decide":          connector.ActionFunc(a.decide),
		"payment_link":    connector.ActionFunc(a.paymentLink),
		"notify_customer": connector.ActionFunc(a.notify),
		"get_booking":     connector.ActionFunc(a.getBooking),
		"refund":          connector.ActionFunc(a.refund),
	}}
}

type actions struct{ ops Ops }

func (a *actions) caller(ctx context.Context, req connector.Request) (connector.Caller, string, error) {
	c, ok := connector.CallerFrom(ctx)
	if !ok {
		return c, "", fmt.Errorf("storefront actions run only in a workflow run: %w", effects.ErrFatal)
	}
	if a.ops == nil {
		return c, "", fmt.Errorf("storefront orders are not available on this deployment: %w", effects.ErrFatal)
	}
	ref, _ := req.Input["reference"].(string)
	if ref == "" {
		return c, "", fmt.Errorf("a reference is required: %w", effects.ErrFatal)
	}
	return c, ref, nil
}

func (a *actions) decide(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, ref, err := a.caller(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	decision, _ := req.Input["decision"].(string)
	note, _ := req.Input["note"].(string)
	out, err := a.ops.Decide(ctx, c.Tenant, ref, decision, note, "workflow:"+c.Run.String())
	return connector.Response{Output: out}, classify(err)
}

func (a *actions) paymentLink(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, ref, err := a.caller(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	out, err := a.ops.PaymentLink(ctx, c.Tenant, ref)
	return connector.Response{Output: out}, classify(err)
}

func (a *actions) notify(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, ref, err := a.caller(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	text, _ := req.Input["text"].(string)
	subject, _ := req.Input["subject"].(string)
	// A message by name, in the business's language (no-technical-steps
	// N6): customer.<say> in the catalogue, filled from vars.
	if say, _ := req.Input["say"].(string); say != "" {
		id := "customer." + say
		l := a.ops.CustomerLanguage(ctx, c.Tenant)
		if !lang.Default().Has(id) {
			return connector.Response{}, fmt.Errorf("no customer message %q: %w", say, effects.ErrFatal)
		}
		var kv []string
		if vars, ok := req.Input["vars"].(map[string]any); ok {
			for k, v := range vars {
				kv = append(kv, k, fmt.Sprint(v))
			}
		}
		text = lang.Default().Text(l, id, kv...)
	}
	key := req.IdempotencyKey
	if key == "" {
		key = c.Run.String() + ":" + c.Step
	}
	out, err := a.ops.NotifyCustomer(ctx, c.Tenant, ref, text, subject, key)
	return connector.Response{Output: out}, classify(err)
}

func (a *actions) getBooking(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, ref, err := a.caller(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	out, err := a.ops.GetBooking(ctx, c.Tenant, ref)
	return connector.Response{Output: out}, classify(err)
}

func (a *actions) refund(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, ref, err := a.caller(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	o, err := a.ops.Refund(ctx, c.Tenant, ref, "workflow:"+c.Run.String())
	if errors.Is(err, sf.ErrRefundByHand) {
		// Paid by bank transfer: the owner sends it back from their bank
		// and marks it refunded; the order says so (N3).
		return connector.Response{Output: map[string]any{"reference": ref, "status": o.Status, "total": sf.FormatNaira(o.Payable()), "by_hand": true,
			"note": strings.TrimSpace(strings.TrimPrefix(err.Error(), sf.ErrRefundByHand.Error()+":"))}}, nil
	}
	if err != nil {
		return connector.Response{}, classify(err)
	}
	return connector.Response{Output: map[string]any{"reference": ref, "status": o.Status, "total": sf.FormatNaira(o.Payable())}}, nil
}

// classify makes refusals fatal: retrying an order the tenant does not
// have, or a decision its status does not allow, cannot help.
func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sf.ErrNoOrder), errors.Is(err, sf.ErrDecision), errors.Is(err, sf.ErrInvalid),
		errors.Is(err, sender.ErrInvalid), errors.Is(err, sender.ErrRecipientLimit), errors.Is(err, sf.ErrRefund):
		return fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	return err
}

// Late is Ops set after the connector is registered: the registry is built
// before the vault and the database the orders need.
type Late struct {
	mu  sync.RWMutex
	ops Ops
}

// Set binds the operations.
func (l *Late) Set(ops Ops) { l.mu.Lock(); l.ops = ops; l.mu.Unlock() }

func (l *Late) get() (Ops, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.ops == nil {
		return nil, fmt.Errorf("storefront orders are not available on this deployment: %w", effects.ErrFatal)
	}
	return l.ops, nil
}

func (l *Late) Decide(ctx context.Context, tenant uuid.UUID, reference, decision, note, actor string) (map[string]any, error) {
	ops, err := l.get()
	if err != nil {
		return nil, err
	}
	return ops.Decide(ctx, tenant, reference, decision, note, actor)
}

func (l *Late) PaymentLink(ctx context.Context, tenant uuid.UUID, reference string) (map[string]any, error) {
	ops, err := l.get()
	if err != nil {
		return nil, err
	}
	return ops.PaymentLink(ctx, tenant, reference)
}

func (l *Late) NotifyCustomer(ctx context.Context, tenant uuid.UUID, reference, text, subject, key string) (map[string]any, error) {
	ops, err := l.get()
	if err != nil {
		return nil, err
	}
	return ops.NotifyCustomer(ctx, tenant, reference, text, subject, key)
}

func (l *Late) CustomerLanguage(ctx context.Context, tenant uuid.UUID) lang.Tag {
	ops, err := l.get()
	if err != nil {
		return lang.EN
	}
	return ops.CustomerLanguage(ctx, tenant)
}

func (l *Late) GetBooking(ctx context.Context, tenant uuid.UUID, reference string) (map[string]any, error) {
	ops, err := l.get()
	if err != nil {
		return nil, err
	}
	return ops.GetBooking(ctx, tenant, reference)
}

func (l *Late) Refund(ctx context.Context, tenant uuid.UUID, reference, actor string) (sf.Order, error) {
	ops, err := l.get()
	if err != nil {
		return sf.Order{}, err
	}
	return ops.Refund(ctx, tenant, reference, actor)
}

// Package payments is the payments platform connector (P1;
// docs/spec/chat-pay-people.md): a workflow prepares a payment to a saved
// account for a person to confirm, and the pay-person workflow records
// how one ended. Like the bills and riders connectors it acts on Taskiem's
// own records, for the tenant of the run calling it (connector.CallerFrom),
// never one named in its input.
package payments

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// ErrRefused is a payment that cannot be prepared or finished as asked:
// its words say why. It is not retried.
var ErrRefused = errors.New("payments: refused")

// PrepareIn is a payment a workflow prepares.
type PrepareIn struct {
	Payee       string
	AmountKobo  int64
	Purpose     string
	PurposeNote string
	Note        string
}

// Ops is what the actions call.
type Ops interface {
	Prepare(ctx context.Context, tenant, run uuid.UUID, in PrepareIn) (map[string]any, error)
	Finish(ctx context.Context, tenant, payment uuid.UUID, status, accountName, payoutID, reason string, feeKobo *int64) (map[string]any, error)
}

// New returns the connector; ops nil makes every action fail plainly.
func New(ops Ops) *connector.Connector {
	m := connector.MustParse(manifest)
	a := &actions{ops: ops}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"prepare": connector.ActionFunc(a.prepare),
		"finish":  connector.ActionFunc(a.finish),
	}}
}

type actions struct{ ops Ops }

func (a *actions) caller(ctx context.Context) (connector.Caller, error) {
	c, ok := connector.CallerFrom(ctx)
	if !ok {
		return c, fmt.Errorf("payments actions run only in a workflow run: %w", effects.ErrFatal)
	}
	if a.ops == nil {
		return c, fmt.Errorf("payments are not available on this deployment: %w", effects.ErrFatal)
	}
	return c, nil
}

func fatal(err error) error {
	if errors.Is(err, ErrRefused) {
		return fmt.Errorf("%s: %w", err.Error(), effects.ErrFatal)
	}
	return err
}

func (a *actions) prepare(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	in := PrepareIn{}
	in.Payee, _ = req.Input["payee"].(string)
	in.Purpose, _ = req.Input["purpose"].(string)
	in.PurposeNote, _ = req.Input["purpose_note"].(string)
	in.Note, _ = req.Input["note"].(string)
	switch v := req.Input["amount_kobo"].(type) {
	case float64:
		in.AmountKobo = int64(v)
	case int64:
		in.AmountKobo = v
	case int:
		in.AmountKobo = int64(v)
	}
	out, err := a.ops.Prepare(ctx, c.Tenant, c.Run, in)
	if err != nil {
		return connector.Response{}, fatal(err)
	}
	return connector.Response{Output: out}, nil
}

func (a *actions) finish(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	id, err := uuid.Parse(fmt.Sprint(req.Input["payment_id"]))
	if err != nil {
		return connector.Response{}, fmt.Errorf("payment_id is a payment's id: %w", effects.ErrFatal)
	}
	status, _ := req.Input["status"].(string)
	name, _ := req.Input["account_name"].(string)
	payout, _ := req.Input["payout_id"].(string)
	reason, _ := req.Input["reason"].(string)
	var fee *int64
	switch v := req.Input["fee_kobo"].(type) {
	case float64:
		f := int64(v)
		fee = &f
	case int64:
		fee = &v
	}
	out, err := a.ops.Finish(ctx, c.Tenant, id, status, name, payout, reason, fee)
	if err != nil {
		return connector.Response{}, fatal(err)
	}
	return connector.Response{Output: out}, nil
}

// Late binds the operations once the server that does them is made.
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
		return nil, fmt.Errorf("payments are not available on this deployment: %w", effects.ErrFatal)
	}
	return l.ops, nil
}

// Prepare calls the bound operations.
func (l *Late) Prepare(ctx context.Context, tenant, run uuid.UUID, in PrepareIn) (map[string]any, error) {
	ops, err := l.get()
	if err != nil {
		return nil, err
	}
	return ops.Prepare(ctx, tenant, run, in)
}

// Finish calls the bound operations.
func (l *Late) Finish(ctx context.Context, tenant, payment uuid.UUID, status, accountName, payoutID, reason string, feeKobo *int64) (map[string]any, error) {
	ops, err := l.get()
	if err != nil {
		return nil, err
	}
	return ops.Finish(ctx, tenant, payment, status, accountName, payoutID, reason, feeKobo)
}

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
	// Check holds the pay-person run to its payment before any money
	// moves: the payment claimed for this run, the run's input the
	// payment's own, approved by whom it must be.
	Check(ctx context.Context, tenant, run, payment uuid.UUID, in CheckIn) (map[string]any, error)
	// Finish records how the payment of the run ended: only the run the
	// payment was sent with may.
	Finish(ctx context.Context, tenant, run, payment uuid.UUID, status, accountName, payoutID, reason string, feeKobo *int64, route string) (map[string]any, error)
}

// CheckIn is what the pay-person run is about to pay, as its input says.
type CheckIn struct {
	AmountKobo    int64
	BankCode      string
	AccountNumber string
	NeedsOK       bool
	// Route and WalletID: how the run pays it (bank, or wallet to the
	// wallet named), as the payment says (paying people H7).
	Route    string
	WalletID string
}

// New returns the connector; ops nil makes every action fail plainly.
func New(ops Ops) *connector.Connector {
	m := connector.MustParse(manifest)
	a := &actions{ops: ops}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"prepare": connector.ActionFunc(a.prepare),
		"finish":  connector.ActionFunc(a.finish),
		"check":   connector.ActionFunc(a.check),
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
	in.AmountKobo = kobo(req.Input["amount_kobo"])
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
	route, _ := req.Input["route"].(string)
	out, err := a.ops.Finish(ctx, c.Tenant, c.Run, id, status, name, payout, reason, fee, route)
	if err != nil {
		return connector.Response{}, fatal(err)
	}
	return connector.Response{Output: out}, nil
}

func kobo(v any) int64 {
	switch v := v.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

func (a *actions) check(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	id, err := uuid.Parse(fmt.Sprint(req.Input["payment_id"]))
	if err != nil {
		return connector.Response{}, fmt.Errorf("payment_id is a payment's id: %w", effects.ErrFatal)
	}
	in := CheckIn{AmountKobo: kobo(req.Input["amount_kobo"])}
	in.BankCode, _ = req.Input["bank_code"].(string)
	in.AccountNumber, _ = req.Input["account_number"].(string)
	in.NeedsOK, _ = req.Input["needs_ok"].(bool)
	in.Route, _ = req.Input["route"].(string)
	in.WalletID, _ = req.Input["wallet_id"].(string)
	out, err := a.ops.Check(ctx, c.Tenant, c.Run, id, in)
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
func (l *Late) Finish(ctx context.Context, tenant, run, payment uuid.UUID, status, accountName, payoutID, reason string, feeKobo *int64, route string) (map[string]any, error) {
	ops, err := l.get()
	if err != nil {
		return nil, err
	}
	return ops.Finish(ctx, tenant, run, payment, status, accountName, payoutID, reason, feeKobo, route)
}

// Check calls the bound operations.
func (l *Late) Check(ctx context.Context, tenant, run, payment uuid.UUID, in CheckIn) (map[string]any, error) {
	ops, err := l.get()
	if err != nil {
		return nil, err
	}
	return ops.Check(ctx, tenant, run, payment, in)
}

// Package invoices is the invoices platform connector (docs/invoices.md,
// decision 0045): create, send, read and void the business's invoices to
// its customers, list the overdue and lately paid ones, message their
// customers, and give a customer their own account number. Like the storefront connector it acts on Taskiem's own
// records, for the tenant of the run calling it (connector.CallerFrom),
// never one named in its input. It has no action that marks an invoice
// paid.
package invoices

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
)

//go:embed manifest.yaml
var manifest []byte

// Ops is what the actions call: invoicing.Service.
type Ops interface {
	CreateOnce(ctx context.Context, tenant uuid.UUID, key string, d invoicing.Draft, by string) (invoicing.Invoice, error)
	Send(ctx context.Context, tenant uuid.UUID, number, by, key string) (invoicing.Sent, error)
	Read(ctx context.Context, tenant uuid.UUID, number string) (invoicing.Invoice, error)
	Cancel(ctx context.Context, tenant uuid.UUID, number, reason, by string) (invoicing.Invoice, error)
	NotifyCustomer(ctx context.Context, tenant uuid.UUID, number, text, subject, key string) (map[string]any, error)
	ListFor(ctx context.Context, tenant uuid.UUID, status string, within time.Duration, limit int) ([]invoicing.Listed, error)
	GiveAccount(ctx context.Context, tenant uuid.UUID, in invoicing.AccountRequest, by string) (invoicing.CustomerAccount, error)
}

// New returns the connector; ops nil makes every action fail plainly.
func New(ops Ops) *connector.Connector {
	m := connector.MustParse(manifest)
	a := &actions{ops: ops}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"create":           connector.ActionFunc(a.create),
		"send":             connector.ActionFunc(a.send),
		"get":              connector.ActionFunc(a.get),
		"void":             connector.ActionFunc(a.void),
		"notify_customer":  connector.ActionFunc(a.notify),
		"list":             connector.ActionFunc(a.list),
		"register_account": connector.ActionFunc(a.registerAccount),
	}}
}

type actions struct{ ops Ops }

func (a *actions) caller(ctx context.Context) (connector.Caller, error) {
	c, ok := connector.CallerFrom(ctx)
	if !ok {
		return c, fmt.Errorf("invoice actions run only in a workflow run: %w", effects.ErrFatal)
	}
	if a.ops == nil {
		return c, fmt.Errorf("invoices are not available on this deployment: %w", effects.ErrFatal)
	}
	return c, nil
}

func by(c connector.Caller) string { return "workflow:" + c.Run.String() }

func number(req connector.Request) (string, error) {
	n, _ := req.Input["number"].(string)
	if n == "" {
		return "", fmt.Errorf("an invoice number is required: %w", effects.ErrFatal)
	}
	return n, nil
}

func summary(x invoicing.Invoice) map[string]any {
	return map[string]any{"number": x.Number, "status": x.Status, "total_kobo": x.TotalKobo, "total": invoicing.Naira(x.TotalKobo)}
}

// create is made idempotent by the step's key: a retry finds the invoice
// the first attempt made rather than taking another number.
func (a *actions) create(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	in := map[string]any{}
	for k, v := range req.Input {
		if k != "idempotency_key" {
			in[k] = v
		}
	}
	raw, _ := json.Marshal(in)
	var d invoicing.Draft
	if err := json.Unmarshal(raw, &d); err != nil {
		return connector.Response{}, fmt.Errorf("invoice: %w: %w", err, effects.ErrFatal)
	}
	x, err := a.ops.CreateOnce(ctx, c.Tenant, req.IdempotencyKey, d, by(c))
	if err != nil {
		return connector.Response{}, classify(err)
	}
	return connector.Response{Output: summary(x)}, nil
}

func (a *actions) send(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	n, err := number(req)
	if err != nil {
		return connector.Response{}, err
	}
	key := req.IdempotencyKey
	if key == "" {
		key = c.Run.String() + ":" + c.Step
	}
	s, err := a.ops.Send(ctx, c.Tenant, n, by(c), key)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	out := summary(s.Invoice)
	out["queued"] = s.Queued
	return connector.Response{Output: out}, nil
}

func (a *actions) get(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	n, err := number(req)
	if err != nil {
		return connector.Response{}, err
	}
	x, err := a.ops.Read(ctx, c.Tenant, n)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	out := summary(x)
	out["paid_kobo"], out["due_kobo"], out["due"], out["customer_name"] = x.PaidKobo, x.DueKobo, invoicing.Naira(x.DueKobo), x.Customer.Name
	if x.DueOn != "" {
		out["due_on"] = x.DueOn
	}
	return connector.Response{Output: out}, nil
}

func (a *actions) void(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	n, err := number(req)
	if err != nil {
		return connector.Response{}, err
	}
	reason, _ := req.Input["reason"].(string)
	x, err := a.ops.Cancel(ctx, c.Tenant, n, reason, by(c))
	if err != nil {
		return connector.Response{}, classify(err)
	}
	return connector.Response{Output: map[string]any{"number": x.Number, "status": x.Status}}, nil
}

func (a *actions) notify(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	n, err := number(req)
	if err != nil {
		return connector.Response{}, err
	}
	text, _ := req.Input["text"].(string)
	subject, _ := req.Input["subject"].(string)
	key := req.IdempotencyKey
	if key == "" {
		key = c.Run.String() + ":" + c.Step
	}
	out, err := a.ops.NotifyCustomer(ctx, c.Tenant, n, text, subject, key)
	return connector.Response{Output: out}, classify(err)
}

// list reads overdue invoices, or those paid lately, with no customer
// details: a run reaches the customer through notify_customer.
func (a *actions) list(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	status, _ := req.Input["status"].(string)
	hours, limit := 24, 50
	if v, ok := intIn(req.Input["paid_within_hours"]); ok {
		hours = v
	}
	if v, ok := intIn(req.Input["limit"]); ok {
		limit = v
	}
	if limit < 1 || limit > invoicing.MaxListed {
		return connector.Response{}, fmt.Errorf("limit is 1 to %d: %w", invoicing.MaxListed, effects.ErrFatal)
	}
	got, err := a.ops.ListFor(ctx, c.Tenant, status, time.Duration(hours)*time.Hour, limit)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	list := make([]any, 0, len(got))
	for _, l := range got {
		x := map[string]any{"number": l.Number, "status": l.Status, "total_kobo": l.TotalKobo, "total": invoicing.Naira(l.TotalKobo),
			"due_kobo": l.DueKobo, "due": invoicing.Naira(l.DueKobo), "days_overdue": l.DaysOverdue}
		if l.DueOn != "" {
			x["due_on"] = l.DueOn
		}
		if l.PaidAt != nil {
			x["paid_at"] = l.PaidAt.UTC().Format(time.RFC3339)
		}
		list = append(list, x)
	}
	return connector.Response{Output: map[string]any{"invoices": list, "count": len(list)}}, nil
}

// registerAccount gives a customer a permanent account number: issued on
// the connection, or the one the business already has for them. Issuing
// is once per customer and connection (the engine answers the account
// already issued), so a retry of the step answers the same account.
func (a *actions) registerAccount(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	in := map[string]any{}
	for k, v := range req.Input {
		if k != "idempotency_key" {
			in[k] = v
		}
	}
	raw, _ := json.Marshal(in)
	var ar invoicing.AccountRequest
	if err := json.Unmarshal(raw, &ar); err != nil {
		return connector.Response{}, fmt.Errorf("register_account: %w: %w", err, effects.ErrFatal)
	}
	if !ar.Issue && (ar.AccountNumber == "" || ar.Reference == "") {
		return connector.Response{}, fmt.Errorf("give the account_number and reference the business has for the customer, or issue true to issue one: %w", effects.ErrFatal)
	}
	x, err := a.ops.GiveAccount(ctx, c.Tenant, ar, by(c))
	if err != nil {
		return connector.Response{}, classify(err)
	}
	out := map[string]any{"account_number": x.AccountNumber, "reference": x.Reference, "connection": x.Channel, "customer_name": x.Customer.Name}
	if x.BankName != "" {
		out["bank_name"] = x.BankName
	}
	return connector.Response{Output: out}, nil
}

// intIn reads a whole number from a step's input (JSON numbers arrive as
// float64).
func intIn(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	}
	return 0, false
}

func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, invoicing.ErrNotFound), errors.Is(err, invoicing.ErrInvalid), errors.Is(err, invoicing.ErrState), errors.Is(err, invoicing.ErrNoRail),
		errors.Is(err, sender.ErrInvalid), errors.Is(err, sender.ErrRecipientLimit):
		return fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	return err
}

// Late is Ops set after the connector is registered: the registry is built
// before the vault and the database the invoices need.
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
		return nil, fmt.Errorf("invoices are not available on this deployment: %w", effects.ErrFatal)
	}
	return l.ops, nil
}

// CreateOnce implements Ops.
func (l *Late) CreateOnce(ctx context.Context, t uuid.UUID, key string, d invoicing.Draft, by string) (invoicing.Invoice, error) {
	o, err := l.get()
	if err != nil {
		return invoicing.Invoice{}, err
	}
	return o.CreateOnce(ctx, t, key, d, by)
}

// Send implements Ops.
func (l *Late) Send(ctx context.Context, t uuid.UUID, number, by, key string) (invoicing.Sent, error) {
	o, err := l.get()
	if err != nil {
		return invoicing.Sent{}, err
	}
	return o.Send(ctx, t, number, by, key)
}

// Read implements Ops.
func (l *Late) Read(ctx context.Context, t uuid.UUID, number string) (invoicing.Invoice, error) {
	o, err := l.get()
	if err != nil {
		return invoicing.Invoice{}, err
	}
	return o.Read(ctx, t, number)
}

// Cancel implements Ops.
func (l *Late) Cancel(ctx context.Context, t uuid.UUID, number, reason, by string) (invoicing.Invoice, error) {
	o, err := l.get()
	if err != nil {
		return invoicing.Invoice{}, err
	}
	return o.Cancel(ctx, t, number, reason, by)
}

// ListFor implements Ops.
func (l *Late) ListFor(ctx context.Context, t uuid.UUID, status string, within time.Duration, limit int) ([]invoicing.Listed, error) {
	o, err := l.get()
	if err != nil {
		return nil, err
	}
	return o.ListFor(ctx, t, status, within, limit)
}

// NotifyCustomer implements Ops.
func (l *Late) NotifyCustomer(ctx context.Context, t uuid.UUID, number, text, subject, key string) (map[string]any, error) {
	o, err := l.get()
	if err != nil {
		return nil, err
	}
	return o.NotifyCustomer(ctx, t, number, text, subject, key)
}

// GiveAccount implements Ops.
func (l *Late) GiveAccount(ctx context.Context, t uuid.UUID, in invoicing.AccountRequest, by string) (invoicing.CustomerAccount, error) {
	o, err := l.get()
	if err != nil {
		return invoicing.CustomerAccount{}, err
	}
	return o.GiveAccount(ctx, t, in, by)
}

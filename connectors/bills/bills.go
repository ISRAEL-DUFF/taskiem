// Package bills is the bills platform connector (docs/spec/business-links.md
// §6.2): what the pay-linked-bill workflow records about a bill's payment.
// Like the riders connector it acts on Taskiem's own records, for the
// tenant of the run calling it (connector.CallerFrom), never one named in
// its input.
package bills

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/links"
)

//go:embed manifest.yaml
var manifest []byte

// Ops is what the action calls.
type Ops interface {
	FinishBillPayment(ctx context.Context, tenant, payment uuid.UUID, status, accountName string) (links.BillPayment, error)
}

// New returns the connector; ops nil makes the action fail plainly.
func New(ops Ops) *connector.Connector {
	m := connector.MustParse(manifest)
	a := &actions{ops: ops}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"finish_bill_payment": connector.ActionFunc(a.finish),
	}}
}

type actions struct{ ops Ops }

func (a *actions) finish(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, ok := connector.CallerFrom(ctx)
	if !ok {
		return connector.Response{}, fmt.Errorf("bills actions run only in a workflow run: %w", effects.ErrFatal)
	}
	if a.ops == nil {
		return connector.Response{}, fmt.Errorf("bills are not available on this deployment: %w", effects.ErrFatal)
	}
	id, err := uuid.Parse(fmt.Sprint(req.Input["payment_id"]))
	if err != nil {
		return connector.Response{}, fmt.Errorf("payment_id is a bill payment's id: %w", effects.ErrFatal)
	}
	status, _ := req.Input["status"].(string)
	name, _ := req.Input["account_name"].(string)
	p, err := a.ops.FinishBillPayment(ctx, c.Tenant, id, status, name)
	switch {
	case errors.Is(err, links.ErrNotFound):
		return connector.Response{}, fmt.Errorf("no such payment of a bill: %w", effects.ErrFatal)
	case errors.Is(err, links.ErrInvalid):
		return connector.Response{}, fmt.Errorf("%s: %w", err.Error(), effects.ErrFatal)
	case err != nil:
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"payment_id": p.ID.String(), "status": p.Status, "bill": p.Reference}}, nil
}

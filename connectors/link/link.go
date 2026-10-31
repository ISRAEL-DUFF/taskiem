// Package link is the link platform connector (link@1): what workflows do
// between businesses linked on Taskiem (docs/spec/business-links.md §11).
// The API server does the work (api/link_connector.go); this package
// holds the manifest and hands each action to it with the run's caller.
package link

import (
	_ "embed"

	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Call is one action called by a run's step.
type Call struct {
	Tenant uuid.UUID
	Run    uuid.UUID
	Step   string
	Action string
	Input  map[string]any
	// Key is the step's idempotency key (writes): the same key twice is
	// the same answer.
	Key string
}

// Ops does the actions. A refusal the run cannot change by retrying is
// wrapped with effects.ErrFatal.
type Ops interface {
	LinkAction(ctx context.Context, c Call) (map[string]any, error)
}

// Actions are the connector's actions.
var Actions = []string{"get_bill", "list_bills", "pay_bill", "get_order", "order_step", "price_of", "low_orders", "place_order", "reply"}

// New returns the connector; ops nil makes every action fail plainly.
func New(ops Ops) *connector.Connector {
	m := connector.MustParse(manifest)
	acts := map[string]connector.Action{}
	for _, name := range Actions {
		acts[name] = connector.ActionFunc(func(ctx context.Context, req connector.Request) (connector.Response, error) {
			return call(ctx, ops, name, req)
		})
	}
	return &connector.Connector{Manifest: m, Actions: acts}
}

func call(ctx context.Context, ops Ops, action string, req connector.Request) (connector.Response, error) {
	c, ok := connector.CallerFrom(ctx)
	if !ok {
		return connector.Response{}, fmt.Errorf("link actions run only in a workflow run: %w", effects.ErrFatal)
	}
	if ops == nil {
		return connector.Response{}, fmt.Errorf("linked businesses are not available on this deployment: %w", effects.ErrFatal)
	}
	in := map[string]any{}
	for k, v := range req.Input {
		if k != "idempotency_key" {
			in[k] = v
		}
	}
	key := req.IdempotencyKey
	if key == "" {
		key = c.Run.String() + ":" + c.Step
	}
	out, err := ops.LinkAction(ctx, Call{Tenant: c.Tenant, Run: c.Run, Step: c.Step, Action: action, Input: in, Key: key})
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: out}, nil
}

// Late is Ops set after the connector is registered: the registry is
// built before the server that does the work.
type Late struct {
	mu  sync.RWMutex
	ops Ops
}

// Set binds the operations.
func (l *Late) Set(ops Ops) { l.mu.Lock(); l.ops = ops; l.mu.Unlock() }

// LinkAction hands the call to the bound operations.
func (l *Late) LinkAction(ctx context.Context, c Call) (map[string]any, error) {
	l.mu.RLock()
	ops := l.ops
	l.mu.RUnlock()
	if ops == nil {
		return nil, fmt.Errorf("linked businesses are not available on this deployment: %w", effects.ErrFatal)
	}
	return ops.LinkAction(ctx, c)
}

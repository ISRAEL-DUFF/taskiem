// Package knowledge is the knowledge@1 platform connector (docs/knowledge.md,
// decision 0062 D12): it searches what a business's assistant knows, for
// the tenant of the run calling it (connector.CallerFrom), never one named
// in its input.
package knowledge

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
	"github.com/israel-duff/taskiem/engine/knowledge"
)

//go:embed manifest.yaml
var manifest []byte

// Ops is what the actions call: *knowledge.Service.
type Ops interface {
	Search(ctx context.Context, tenant uuid.UUID, question string, k, maxChars int) ([]knowledge.Hit, error)
	List(ctx context.Context, tenant uuid.UUID) ([]knowledge.Source, error)
	RequestRefresh(ctx context.Context, tenant, id uuid.UUID) error
}

// New returns the connector; ops nil makes every action fail plainly.
func New(ops Ops) *connector.Connector {
	a := &actions{ops: ops}
	return &connector.Connector{Manifest: connector.MustParse(manifest), Actions: map[string]connector.Action{
		"search":  connector.ActionFunc(a.search),
		"refresh": connector.ActionFunc(a.refresh),
	}}
}

type actions struct{ ops Ops }

func (a *actions) caller(ctx context.Context) (uuid.UUID, error) {
	c, ok := connector.CallerFrom(ctx)
	if !ok {
		return uuid.Nil, fmt.Errorf("knowledge actions run only in a workflow run: %w", effects.ErrFatal)
	}
	if a.ops == nil {
		return uuid.Nil, fmt.Errorf("what the assistant knows is not available on this deployment: %w", effects.ErrFatal)
	}
	return c.Tenant, nil
}

func intIn(in map[string]any, k string) int {
	switch v := in[k].(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func (a *actions) search(ctx context.Context, req connector.Request) (connector.Response, error) {
	tenant, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	q, _ := req.Input["question"].(string)
	hits, err := a.ops.Search(ctx, tenant, q, intIn(req.Input, "limit"), intIn(req.Input, "max_chars"))
	if err != nil {
		return connector.Response{}, fmt.Errorf("knowledge: %w: %w", err, effects.ErrRetryable)
	}
	passages := make([]any, len(hits))
	for i, h := range hits {
		passages[i] = map[string]any{"source": h.Source, "kind": h.Kind, "title": h.Title, "text": h.Text, "link": h.Link, "read_at": h.ReadAt.UTC().Format(time.RFC3339)}
	}
	return connector.Response{Output: map[string]any{"found": len(hits) > 0, "material": knowledge.Material(hits), "passages": passages}}, nil
}

func (a *actions) refresh(ctx context.Context, req connector.Request) (connector.Response, error) {
	tenant, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	name := strings.TrimSpace(fmt.Sprint(req.Input["source"]))
	sources, err := a.ops.List(ctx, tenant)
	if err != nil {
		return connector.Response{}, fmt.Errorf("knowledge: %w: %w", err, effects.ErrRetryable)
	}
	var id uuid.UUID
	for _, s := range sources {
		if s.ID.String() == name || strings.EqualFold(s.Title, name) {
			id = s.ID
			break
		}
	}
	if id == uuid.Nil {
		return connector.Response{}, fmt.Errorf("knowledge: no source called %q: %w", name, effects.ErrFatal)
	}
	err = a.ops.RequestRefresh(ctx, tenant, id)
	switch {
	case errors.Is(err, knowledge.ErrBadInput), errors.Is(err, knowledge.ErrNotFound):
		return connector.Response{}, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	case err != nil:
		return connector.Response{}, fmt.Errorf("%w: %w", err, effects.ErrRetryable)
	}
	return connector.Response{Output: map[string]any{"requested": true}}, nil
}

// Late is Ops set after the connector is registered.
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
		return nil, errors.New("what the assistant knows is not available yet")
	}
	return l.ops, nil
}

func (l *Late) Search(ctx context.Context, tenant uuid.UUID, q string, k, maxChars int) ([]knowledge.Hit, error) {
	o, err := l.get()
	if err != nil {
		return nil, err
	}
	return o.Search(ctx, tenant, q, k, maxChars)
}

func (l *Late) List(ctx context.Context, tenant uuid.UUID) ([]knowledge.Source, error) {
	o, err := l.get()
	if err != nil {
		return nil, err
	}
	return o.List(ctx, tenant)
}

func (l *Late) RequestRefresh(ctx context.Context, tenant, id uuid.UUID) error {
	o, err := l.get()
	if err != nil {
		return err
	}
	return o.RequestRefresh(ctx, tenant, id)
}

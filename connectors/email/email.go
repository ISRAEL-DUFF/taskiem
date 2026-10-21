// Package email is the inbound email platform connector: a run started by
// an email reads the files that came with it through short-lived links.
// It acts for the tenant of the run calling it (connector.CallerFrom),
// never one named in its input.
package email

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/forms"
)

//go:embed manifest.yaml
var manifest []byte

// Ops is what the actions call: *inmail.Service.
type Ops interface {
	FileURL(ctx context.Context, tenant uuid.UUID, file string, ttl time.Duration) (map[string]any, error)
}

// New returns the connector; ops nil makes every action fail plainly.
func New(ops Ops) *connector.Connector {
	a := &actions{ops: ops}
	return &connector.Connector{Manifest: connector.MustParse(manifest), Actions: map[string]connector.Action{
		"file_url": connector.ActionFunc(a.fileURL),
	}}
}

type actions struct{ ops Ops }

func (a *actions) fileURL(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, ok := connector.CallerFrom(ctx)
	if !ok {
		return connector.Response{}, fmt.Errorf("email actions run only in a workflow run: %w", effects.ErrFatal)
	}
	if a.ops == nil {
		return connector.Response{}, fmt.Errorf("inbound email is not available on this deployment: %w", effects.ErrFatal)
	}
	file, _ := req.Input["file"].(string)
	ttl := 15 * time.Minute
	switch n := req.Input["ttl_minutes"].(type) {
	case int64:
		ttl = time.Duration(n) * time.Minute
	case float64:
		ttl = time.Duration(n) * time.Minute
	}
	out, err := a.ops.FileURL(ctx, c.Tenant, file, ttl)
	if err != nil && errors.Is(err, forms.ErrNoRequest) {
		// A file the tenant does not have cannot be helped by retrying.
		err = fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	return connector.Response{Output: out}, err
}

// Late is Ops set after the connector is registered (the registry is
// built before the vault and the email service).
type Late struct {
	mu  sync.RWMutex
	ops Ops
}

// Set binds the operations.
func (l *Late) Set(ops Ops) { l.mu.Lock(); l.ops = ops; l.mu.Unlock() }

func (l *Late) FileURL(ctx context.Context, tenant uuid.UUID, file string, ttl time.Duration) (map[string]any, error) {
	l.mu.RLock()
	ops := l.ops
	l.mu.RUnlock()
	if ops == nil {
		return nil, fmt.Errorf("inbound email is not available on this deployment: %w", effects.ErrFatal)
	}
	return ops.FileURL(ctx, tenant, file, ttl)
}

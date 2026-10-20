// Package form is the forms platform connector: a run tells the person
// who filled in its form how things stand, and reads the files they sent
// through short-lived links. It acts for the tenant of the run calling it
// (connector.CallerFrom), never one named in its input.
package form

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

// Ops is what the actions call: *forms.Service.
type Ops interface {
	Respond(ctx context.Context, tenant, run uuid.UUID, message string) (map[string]any, error)
	FileURL(ctx context.Context, tenant uuid.UUID, file string, ttl time.Duration) (map[string]any, error)
}

// New returns the connector; ops nil makes every action fail plainly.
func New(ops Ops) *connector.Connector {
	a := &actions{ops: ops}
	return &connector.Connector{Manifest: connector.MustParse(manifest), Actions: map[string]connector.Action{
		"respond":  connector.ActionFunc(a.respond),
		"file_url": connector.ActionFunc(a.fileURL),
	}}
}

type actions struct{ ops Ops }

func (a *actions) caller(ctx context.Context) (connector.Caller, error) {
	c, ok := connector.CallerFrom(ctx)
	if !ok {
		return c, fmt.Errorf("form actions run only in a workflow run: %w", effects.ErrFatal)
	}
	if a.ops == nil {
		return c, fmt.Errorf("forms are not available on this deployment: %w", effects.ErrFatal)
	}
	return c, nil
}

func (a *actions) respond(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	msg, _ := req.Input["message"].(string)
	out, err := a.ops.Respond(ctx, c.Tenant, c.Run, msg)
	return connector.Response{Output: out}, classify(err)
}

func (a *actions) fileURL(ctx context.Context, req connector.Request) (connector.Response, error) {
	c, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
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
	return connector.Response{Output: out}, classify(err)
}

// classify makes refusals fatal: a run no form started, or a file the
// tenant does not have, cannot be helped by retrying.
func classify(err error) error {
	if err != nil && (errors.Is(err, forms.ErrNoRequest) || errors.Is(err, forms.ErrRefused)) {
		return fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	return err
}

// Late is Ops set after the connector is registered (the registry is
// built before the vault and the forms service).
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
		return nil, fmt.Errorf("forms are not available on this deployment: %w", effects.ErrFatal)
	}
	return l.ops, nil
}

func (l *Late) Respond(ctx context.Context, tenant, run uuid.UUID, message string) (map[string]any, error) {
	ops, err := l.get()
	if err != nil {
		return nil, err
	}
	return ops.Respond(ctx, tenant, run, message)
}

func (l *Late) FileURL(ctx context.Context, tenant uuid.UUID, file string, ttl time.Duration) (map[string]any, error) {
	ops, err := l.get()
	if err != nil {
		return nil, err
	}
	return ops.FileURL(ctx, tenant, file, ttl)
}

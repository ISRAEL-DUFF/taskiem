// Package files is the platform connector for the files a workflow run
// keeps (engine/runfiles, docs/files.md): a short-lived link to one, or
// deleting one early. It acts for the tenant of the run calling it
// (connector.CallerFrom), never one named in its input.
package files

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
	"github.com/israel-duff/taskiem/engine/runfiles"
)

//go:embed manifest.yaml
var manifest []byte

// Ops is what the actions call: *runfiles.Service.
type Ops interface {
	Link(ctx context.Context, tenant uuid.UUID, id string, ttl time.Duration) (string, time.Time, runfiles.Ref, error)
	Delete(ctx context.Context, tenant uuid.UUID, id string) error
}

// New returns the connector; ops nil makes every action fail plainly.
func New(ops Ops) *connector.Connector {
	a := &actions{ops: ops}
	return &connector.Connector{Manifest: connector.MustParse(manifest), Actions: map[string]connector.Action{
		"link":   connector.ActionFunc(a.link),
		"delete": connector.ActionFunc(a.delete),
	}}
}

type actions struct{ ops Ops }

func (a *actions) caller(ctx context.Context) (uuid.UUID, error) {
	c, ok := connector.CallerFrom(ctx)
	if !ok {
		return uuid.Nil, fmt.Errorf("file actions run only in a workflow run: %w", effects.ErrFatal)
	}
	if a.ops == nil {
		return uuid.Nil, fmt.Errorf("files are not available on this deployment: %w", effects.ErrFatal)
	}
	return c.Tenant, nil
}

func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, runfiles.ErrNotFound), errors.Is(err, runfiles.ErrUnavailable):
		return fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	return fmt.Errorf("%w: %w", err, effects.ErrRetryable)
}

func (a *actions) link(ctx context.Context, req connector.Request) (connector.Response, error) {
	tenant, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	ttl := 15 * time.Minute
	switch n := req.Input["ttl_minutes"].(type) {
	case int64:
		ttl = time.Duration(n) * time.Minute
	case float64:
		ttl = time.Duration(n) * time.Minute
	}
	url, exp, ref, err := a.ops.Link(ctx, tenant, connector.FileID(req.Input["file"]), ttl)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	return connector.Response{Output: map[string]any{"url": url, "expires_at": exp.UTC().Format(time.RFC3339), "file": ref.Map()}}, nil
}

func (a *actions) delete(ctx context.Context, req connector.Request) (connector.Response, error) {
	tenant, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	if err := a.ops.Delete(ctx, tenant, connector.FileID(req.Input["file"])); err != nil {
		return connector.Response{}, classify(err)
	}
	return connector.Response{Output: map[string]any{"deleted": true}}, nil
}

// Late is Ops set after the connector is registered (the registry is
// built before the storage service).
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
		return nil, fmt.Errorf("%w: %w", runfiles.ErrUnavailable, effects.ErrFatal)
	}
	return l.ops, nil
}

func (l *Late) Link(ctx context.Context, tenant uuid.UUID, id string, ttl time.Duration) (string, time.Time, runfiles.Ref, error) {
	o, err := l.get()
	if err != nil {
		return "", time.Time{}, runfiles.Ref{}, err
	}
	return o.Link(ctx, tenant, id, ttl)
}

func (l *Late) Delete(ctx context.Context, tenant uuid.UUID, id string) error {
	o, err := l.get()
	if err != nil {
		return err
	}
	return o.Delete(ctx, tenant, id)
}

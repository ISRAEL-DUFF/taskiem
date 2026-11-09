// Package assistant is the assistant@1 platform connector
// (docs/assistant-guard.md, decision 0062 V6): checking a reply before it
// reaches a customer, handing a customer to a person, and WhatsApp's
// window, for the tenant of the run calling it (connector.CallerFrom).
package assistant

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/assistant"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// Ops is what the actions call: *assistant.Service.
type Ops interface {
	HandOver(ctx context.Context, tenant uuid.UUID, channel, sender string, hours int) (time.Time, error)
	ResumeSender(ctx context.Context, tenant uuid.UUID, channel, sender string) error
	Window(ctx context.Context, tenant uuid.UUID, channel, sender string) (bool, *time.Time, error)
}

// New returns the connector; ops nil makes every action but check_reply
// fail plainly.
func New(ops Ops) *connector.Connector {
	a := &actions{ops: ops}
	return &connector.Connector{Manifest: connector.MustParse(manifest), Actions: map[string]connector.Action{
		"check_reply": connector.ActionFunc(a.checkReply),
		"hand_over":   connector.ActionFunc(a.handOver),
		"resume":      connector.ActionFunc(a.resume),
		"window":      connector.ActionFunc(a.window),
	}}
}

type actions struct{ ops Ops }

func (a *actions) caller(ctx context.Context) (uuid.UUID, error) {
	c, ok := connector.CallerFrom(ctx)
	if !ok {
		return uuid.Nil, fmt.Errorf("assistant actions run only in a workflow run: %w", effects.ErrFatal)
	}
	if a.ops == nil {
		return uuid.Nil, fmt.Errorf("the customer assistant is not available on this deployment: %w", effects.ErrFatal)
	}
	return c.Tenant, nil
}

func str(in map[string]any, k string) string {
	s, _ := in[k].(string)
	return strings.TrimSpace(s)
}

func strs(in map[string]any, k string) []string {
	var out []string
	if list, ok := in[k].([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func num(in map[string]any, k string) int {
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

func (a *actions) checkReply(_ context.Context, req connector.Request) (connector.Response, error) {
	text, _ := req.Input["text"].(string)
	r := assistant.CheckReply(assistant.CheckInput{Text: text, Material: str(req.Input, "material"), Customer: str(req.Input, "customer"),
		Links: strs(req.Input, "links"), Never: strs(req.Input, "never"), Max: num(req.Input, "max"), Language: str(req.Input, "language")})
	probs := make([]any, len(r.Problems))
	for i, p := range r.Problems {
		probs[i] = map[string]any{"kind": p.Kind, "detail": p.Detail}
	}
	return connector.Response{Output: map[string]any{"ok": r.OK, "text": r.Text, "problems": probs}}, nil
}

func (a *actions) who(req connector.Request) (string, string, error) {
	channel, sender := str(req.Input, "channel"), str(req.Input, "sender")
	if channel == "" || sender == "" {
		return "", "", fmt.Errorf("channel and sender are the guarded trigger's (trigger.guard.channel, trigger.guard.sender): %w", effects.ErrFatal)
	}
	return channel, sender, nil
}

func classify(err error) error {
	if errors.Is(err, assistant.ErrNoContact) {
		return fmt.Errorf("this customer has not written on this channel: %w", effects.ErrFatal)
	}
	return fmt.Errorf("assistant: %w: %w", err, effects.ErrRetryable)
}

func (a *actions) handOver(ctx context.Context, req connector.Request) (connector.Response, error) {
	tenant, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	channel, sender, err := a.who(req)
	if err != nil {
		return connector.Response{}, err
	}
	until, err := a.ops.HandOver(ctx, tenant, channel, sender, num(req.Input, "hours"))
	if err != nil {
		return connector.Response{}, classify(err)
	}
	return connector.Response{Output: map[string]any{"until": until.UTC().Format(time.RFC3339)}}, nil
}

func (a *actions) resume(ctx context.Context, req connector.Request) (connector.Response, error) {
	tenant, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	channel, sender, err := a.who(req)
	if err != nil {
		return connector.Response{}, err
	}
	if err := a.ops.ResumeSender(ctx, tenant, channel, sender); err != nil {
		return connector.Response{}, classify(err)
	}
	return connector.Response{Output: map[string]any{"resumed": true}}, nil
}

func (a *actions) window(ctx context.Context, req connector.Request) (connector.Response, error) {
	tenant, err := a.caller(ctx)
	if err != nil {
		return connector.Response{}, err
	}
	channel, sender, err := a.who(req)
	if err != nil {
		return connector.Response{}, err
	}
	open, last, err := a.ops.Window(ctx, tenant, channel, sender)
	if err != nil {
		return connector.Response{}, classify(err)
	}
	out := map[string]any{"open": open, "last_inbound_at": "", "closes_at": ""}
	if last != nil {
		out["last_inbound_at"] = last.UTC().Format(time.RFC3339)
		out["closes_at"] = last.Add(assistant.Window).UTC().Format(time.RFC3339)
	}
	return connector.Response{Output: out}, nil
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
		return nil, errors.New("the customer assistant is not available yet")
	}
	return l.ops, nil
}

func (l *Late) HandOver(ctx context.Context, tenant uuid.UUID, channel, sender string, hours int) (time.Time, error) {
	o, err := l.get()
	if err != nil {
		return time.Time{}, err
	}
	return o.HandOver(ctx, tenant, channel, sender, hours)
}

func (l *Late) ResumeSender(ctx context.Context, tenant uuid.UUID, channel, sender string) error {
	o, err := l.get()
	if err != nil {
		return err
	}
	return o.ResumeSender(ctx, tenant, channel, sender)
}

func (l *Late) Window(ctx context.Context, tenant uuid.UUID, channel, sender string) (bool, *time.Time, error) {
	o, err := l.get()
	if err != nil {
		return false, nil, err
	}
	return o.Window(ctx, tenant, channel, sender)
}

// Package speech is the speech@1 platform connector (docs/speech.md,
// decision 0062 D5, plan V3): it hears a customer's voice note and speaks
// a reply through engine/speech, for the tenant of the run calling it
// (connector.CallerFrom), never one named in its input. Audio comes from
// and goes to the run's file store.
package speech

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/runfiles"
	"github.com/israel-duff/taskiem/engine/speech"
)

//go:embed manifest.yaml
var manifest []byte

// Ops is what the actions call: *speech.Service.
type Ops interface {
	Transcribe(ctx context.Context, tenant uuid.UUID, data []byte, mime, language string) (speech.Heard, error)
	Speak(ctx context.Context, tenant uuid.UUID, text, voice, format, instructions string) (speech.Spoken, error)
	CannotHear(reason, language string) string
}

// New returns the connector; ops nil makes hearing and speaking fail
// plainly (cannot_hear still answers).
func New(ops Ops) *connector.Connector {
	a := &actions{ops: ops}
	return &connector.Connector{Manifest: connector.MustParse(manifest), Actions: map[string]connector.Action{
		"transcribe":  connector.ActionFunc(a.transcribe),
		"speak":       connector.ActionFunc(a.speak),
		"cannot_hear": connector.ActionFunc(a.cannotHear),
	}}
}

type actions struct{ ops Ops }

func (a *actions) caller(ctx context.Context, req connector.Request) (uuid.UUID, error) {
	c, ok := connector.CallerFrom(ctx)
	if !ok {
		return uuid.Nil, fmt.Errorf("speech actions run only in a workflow run: %w", effects.ErrFatal)
	}
	if a.ops == nil {
		return uuid.Nil, fmt.Errorf("%w: %w", speech.ErrUnavailable, effects.ErrFatal)
	}
	if req.Files == nil {
		return uuid.Nil, fmt.Errorf("speech: this run cannot keep files (no file store on this deployment): %w", effects.ErrFatal)
	}
	return c.Tenant, nil
}

// classify: the service failing may mend; everything else needs a person.
func classify(err error) error {
	if errors.Is(err, speech.ErrService) {
		return fmt.Errorf("%w: %w", err, effects.ErrRetryable)
	}
	return fmt.Errorf("%w: %w", err, effects.ErrFatal)
}

func str(in map[string]any, k string) string {
	s, _ := in[k].(string)
	return strings.TrimSpace(s)
}

func (a *actions) transcribe(ctx context.Context, req connector.Request) (connector.Response, error) {
	tenant, err := a.caller(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	data, ref, err := req.Files.Read(ctx, req.Input["file"])
	if err != nil {
		return connector.Response{}, runfiles.ActionError(err)
	}
	mime, _ := ref["type"].(string)
	h, err := a.ops.Transcribe(ctx, tenant, data, mime, str(req.Input, "language"))
	if err != nil {
		return connector.Response{}, classify(err)
	}
	out := map[string]any{"heard": h.OK, "seconds": h.Seconds}
	if h.OK {
		out["text"], out["language"] = h.Text, h.Language
	} else {
		out["reason"], out["reply"] = h.Reason, h.Reply
	}
	return connector.Response{Output: out}, nil
}

func (a *actions) speak(ctx context.Context, req connector.Request) (connector.Response, error) {
	tenant, err := a.caller(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	sp, err := a.ops.Speak(ctx, tenant, str(req.Input, "text"), str(req.Input, "voice"), str(req.Input, "format"), str(req.Input, "how"))
	if err != nil {
		return connector.Response{}, classify(err)
	}
	name := "reply.ogg"
	if sp.Type == "audio/mpeg" {
		name = "reply.mp3"
	}
	// Made by the step, not sent by a person: kept, not scanned.
	ref, err := req.Files.Put(ctx, name, sp.Data, false)
	if err != nil {
		return connector.Response{}, runfiles.ActionError(err)
	}
	return connector.Response{Output: map[string]any{"file": ref, "seconds": sp.Seconds, "characters": sp.Characters, "plays_inline": sp.PlaysInline()}}, nil
}

func (a *actions) cannotHear(_ context.Context, req connector.Request) (connector.Response, error) {
	reason, language := str(req.Input, "reason"), str(req.Input, "language")
	var reply string
	if a.ops != nil {
		reply = a.ops.CannotHear(reason, language)
	} else {
		reply = (*speech.Service)(nil).CannotHear(reason, language)
	}
	return connector.Response{Output: map[string]any{"reply": reply}}, nil
}

// Late is Ops set after the connector is registered (the registry is
// built before the speech service).
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
		return nil, speech.ErrUnavailable
	}
	return l.ops, nil
}

func (l *Late) Transcribe(ctx context.Context, tenant uuid.UUID, data []byte, mime, language string) (speech.Heard, error) {
	o, err := l.get()
	if err != nil {
		return speech.Heard{}, err
	}
	return o.Transcribe(ctx, tenant, data, mime, language)
}

func (l *Late) Speak(ctx context.Context, tenant uuid.UUID, text, voice, format, instructions string) (speech.Spoken, error) {
	o, err := l.get()
	if err != nil {
		return speech.Spoken{}, err
	}
	return o.Speak(ctx, tenant, text, voice, format, instructions)
}

func (l *Late) CannotHear(reason, language string) string {
	o, err := l.get()
	if err != nil {
		return (*speech.Service)(nil).CannotHear(reason, language)
	}
	return o.CannotHear(reason, language)
}

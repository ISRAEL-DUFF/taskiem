package brevo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

// webhooks manages a Brevo webhook behind a trigger (transactional or
// marketing; Brevo's /webhooks, decision 0021).
//
//   - The webhook's description is Taskiem's subscription name, so one
//     made just before a crash is found again.
//   - Its bearer token is Taskiem's: made when the webhook is, kept in the
//     vault, and sent by Brevo with every delivery.
//   - Taskiem never reads the token back, so a webhook that no longer
//     matches (or one found after a crash) is deleted and made again with a
//     new one, rather than changed.
type webhooks struct {
	c      *client
	kind   string // transactional, marketing
	events []string
}

type webhook struct {
	ID          json.Number `json:"id"`
	URL         string      `json:"url"`
	Description string      `json:"description"`
	Events      []string    `json:"events"`
	Type        string      `json:"type"`
}

func (r *webhooks) eventsOf(spec connector.RemoteSpec) []string {
	evs := spec.Events
	if len(evs) == 0 {
		evs = r.events
	}
	out := slices.Clone(evs)
	slices.Sort(out)
	return out
}

func (r *webhooks) call(ctx context.Context, c connector.RemoteCall, op, method, path string, body, out any) error {
	return r.c.call(ctx, c.Credentials, c.HTTP, op, method, path, body, out)
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Create makes the webhook with a token of Taskiem's.
func (r *webhooks) Create(ctx context.Context, c connector.RemoteCall, spec connector.RemoteSpec) (connector.RemoteState, error) {
	tok, err := newToken()
	if err != nil {
		return connector.RemoteState{}, err
	}
	body := map[string]any{"url": spec.URL, "description": spec.Name, "events": r.eventsOf(spec), "type": r.kind, "auth": map[string]string{"type": "bearer", "token": tok}}
	var resp struct {
		ID json.Number `json:"id"`
	}
	if err := r.call(ctx, c, "creating the webhook", http.MethodPost, "/webhooks", body, &resp); err != nil {
		return connector.RemoteState{}, err
	}
	if resp.ID == "" {
		return connector.RemoteState{}, fmt.Errorf("brevo: the webhook came back without its id: %w", effects.ErrUnknownOutcome)
	}
	return connector.RemoteState{ID: resp.ID.String(), Name: spec.Name, Secret: tok, Status: connector.RemoteHealthy}, nil
}

func (r *webhooks) get(ctx context.Context, c connector.RemoteCall, id string) (webhook, error) {
	var w webhook
	err := r.call(ctx, c, "reading the webhook", http.MethodGet, "/webhooks/"+id, nil, &w)
	return w, err
}

// Update keeps a webhook whose URL and events already match; any other is
// deleted and reported missing, so it is made again with a new token.
func (r *webhooks) Update(ctx context.Context, c connector.RemoteCall, id string, spec connector.RemoteSpec) (connector.RemoteState, error) {
	w, err := r.get(ctx, c, id)
	if err != nil {
		return connector.RemoteState{}, err
	}
	have := slices.Clone(w.Events)
	slices.Sort(have)
	if w.URL == spec.URL && slices.Equal(have, r.eventsOf(spec)) {
		return connector.RemoteState{ID: id, Name: spec.Name, Status: connector.RemoteHealthy}, nil
	}
	if err := r.Delete(ctx, c, id); err != nil {
		return connector.RemoteState{}, err
	}
	return connector.RemoteState{}, fmt.Errorf("brevo: the webhook no longer matches: %w", connector.ErrNotFound)
}

// Get reads the webhook.
func (r *webhooks) Get(ctx context.Context, c connector.RemoteCall, id string) (connector.RemoteState, error) {
	if _, err := r.get(ctx, c, id); err != nil {
		return connector.RemoteState{}, err
	}
	return connector.RemoteState{ID: id, Status: connector.RemoteHealthy}, nil
}

// Find looks for a webhook described by the subscription's name. One
// found was made before a crash, with a token Taskiem never kept: it is
// deleted and reported missing, so the reconciler makes it again.
func (r *webhooks) Find(ctx context.Context, c connector.RemoteCall, name string) (connector.RemoteState, bool, error) {
	var resp struct {
		Webhooks []webhook `json:"webhooks"`
	}
	if err := r.call(ctx, c, "listing webhooks", http.MethodGet, "/webhooks?type="+r.kind, nil, &resp); err != nil {
		return connector.RemoteState{}, false, err
	}
	for _, w := range resp.Webhooks {
		if w.Description == name {
			if err := r.Delete(ctx, c, w.ID.String()); err != nil {
				return connector.RemoteState{}, false, err
			}
		}
	}
	return connector.RemoteState{}, false, nil
}

// RotateSecret is not reached: Find never reports a webhook whose token
// Taskiem lacks.
func (r *webhooks) RotateSecret(context.Context, connector.RemoteCall, string) (string, error) {
	return "", fmt.Errorf("brevo: a webhook's token is not changed, the webhook is made again: %w", effects.ErrFatal)
}

// Delete removes the webhook; one already gone is not an error.
func (r *webhooks) Delete(ctx context.Context, c connector.RemoteCall, id string) error {
	err := r.call(ctx, c, "deleting the webhook", http.MethodDelete, "/webhooks/"+id, nil, nil)
	if errors.Is(err, connector.ErrNotFound) {
		return nil
	}
	return err
}

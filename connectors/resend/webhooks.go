package resend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

// webhooks manages the Resend webhook behind the email_events trigger
// (Resend's /webhooks; decision 0021).
//
// Resend keeps no name for a webhook and returns its signing secret only
// when it is made, so, as for Payrolla:
//   - a webhook is Taskiem's when its endpoint is the subscription's URL,
//     which carries Taskiem's subscription id;
//   - one found that way after a crash has a secret Taskiem never kept, so
//     Find deletes it and reports none, and the reconciler makes it again.
type webhooks struct {
	c      *client
	events []string
}

type webhook struct {
	ID       string   `json:"id"`
	Endpoint string   `json:"endpoint"`
	Events   []string `json:"events"`
	Status   string   `json:"status"`
	Secret   string   `json:"signing_secret"`
}

func (w webhook) state(name string) connector.RemoteState {
	st := connector.RemoteState{ID: w.ID, Name: name, Status: connector.RemoteHealthy}
	if w.Status == "disabled" {
		st.Status, st.Reason = connector.RemotePaused, "turned off in Resend (Webhooks)"
	}
	return st
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
	return r.c.call(ctx, c.Credentials, c.HTTP, op, method, path, nil, body, out)
}

// Create makes the webhook; Resend returns its signing secret now only.
func (r *webhooks) Create(ctx context.Context, c connector.RemoteCall, spec connector.RemoteSpec) (connector.RemoteState, error) {
	var w webhook
	if err := r.call(ctx, c, "creating the webhook", http.MethodPost, "/webhooks", map[string]any{"endpoint": spec.URL, "events": r.eventsOf(spec)}, &w); err != nil {
		return connector.RemoteState{}, err
	}
	if w.ID == "" || w.Secret == "" {
		return connector.RemoteState{}, fmt.Errorf("resend: the webhook came back without its id or signing secret: %w", effects.ErrUnknownOutcome)
	}
	st := w.state(spec.Name)
	st.Secret = w.Secret
	return st, nil
}

// Update makes the webhook's endpoint and events the spec's and turns it
// back on; the signing secret stays.
func (r *webhooks) Update(ctx context.Context, c connector.RemoteCall, id string, spec connector.RemoteSpec) (connector.RemoteState, error) {
	var w webhook
	err := r.call(ctx, c, "updating the webhook", http.MethodPatch, "/webhooks/"+url.PathEscape(id), map[string]any{"endpoint": spec.URL, "events": r.eventsOf(spec), "status": "enabled"}, &w)
	if err != nil {
		return connector.RemoteState{}, err
	}
	if w.ID == "" {
		w.ID = id
	}
	return w.state(spec.Name), nil
}

// Get reads the webhook.
func (r *webhooks) Get(ctx context.Context, c connector.RemoteCall, id string) (connector.RemoteState, error) {
	var w webhook
	if err := r.call(ctx, c, "reading the webhook", http.MethodGet, "/webhooks/"+url.PathEscape(id), nil, &w); err != nil {
		return connector.RemoteState{}, err
	}
	return w.state(""), nil
}

// Find looks for a webhook delivering to the subscription's URL. One found
// was made before a crash, with a secret Taskiem never kept: it is deleted
// and reported missing, so the reconciler makes it again.
func (r *webhooks) Find(ctx context.Context, c connector.RemoteCall, name string) (connector.RemoteState, bool, error) {
	var resp struct {
		Data []webhook `json:"data"`
	}
	if err := r.call(ctx, c, "listing webhooks", http.MethodGet, "/webhooks", nil, &resp); err != nil {
		return connector.RemoteState{}, false, err
	}
	for _, w := range resp.Data {
		if u, err := url.Parse(w.Endpoint); err == nil && u.Query().Get("subscription") != "" && u.Query().Get("subscription") == subscriptionOf(name) {
			if err := r.Delete(ctx, c, w.ID); err != nil {
				return connector.RemoteState{}, false, err
			}
		}
	}
	return connector.RemoteState{}, false, nil
}

// subscriptionOf is the subscription id a subscription's name carries
// (remote.Name: "taskiem-<id>").
func subscriptionOf(name string) string {
	const prefix = "taskiem-"
	if len(name) > len(prefix) && name[:len(prefix)] == prefix {
		return name[len(prefix):]
	}
	return name
}

// RotateSecret is not reached: Find never reports a webhook whose secret
// Taskiem lacks.
func (r *webhooks) RotateSecret(context.Context, connector.RemoteCall, string) (string, error) {
	return "", fmt.Errorf("resend: a webhook's secret is not changed, the webhook is made again: %w", effects.ErrFatal)
}

// Delete removes the webhook; one already gone is not an error.
func (r *webhooks) Delete(ctx context.Context, c connector.RemoteCall, id string) error {
	err := r.call(ctx, c, "deleting the webhook", http.MethodDelete, "/webhooks/"+url.PathEscape(id), nil, nil)
	if errors.Is(err, connector.ErrNotFound) {
		return nil
	}
	return err
}

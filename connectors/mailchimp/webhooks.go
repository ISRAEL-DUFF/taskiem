package mailchimp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

// webhooks manages the webhook on an audience behind the audience_events
// trigger (Mailchimp's /lists/{id}/webhooks; decision 0021).
//
// Mailchimp returns the signing secret only when it makes the webhook, and
// keeps no name for one, so, as for Payrolla:
//   - a webhook is Taskiem's when its URL is the subscription's, which
//     carries Taskiem's subscription id;
//   - one found that way after a crash, or one whose events no longer
//     match, is deleted and made again with a secret Taskiem keeps.
//
// The webhook's id is "<audience>/<webhook>", since every call names both.
type webhooks struct{ c *client }

// all are the events a webhook can send, and sources who may cause them.
var all = []string{"subscribe", "unsubscribe", "profile", "upemail", "cleaned", "campaign"}

type webhook struct {
	ID      string          `json:"id"`
	URL     string          `json:"url"`
	Events  map[string]bool `json:"events"`
	Secret  string          `json:"signing_secret"`
	Signing bool            `json:"signing_enabled"`
}

func eventsOf(spec connector.RemoteSpec) map[string]bool {
	want := spec.Events
	if len(want) == 0 {
		want = all
	}
	out := map[string]bool{}
	for _, e := range all {
		out[e] = false
	}
	for _, e := range want {
		out[e] = true
	}
	return out
}

func audienceOf(spec connector.RemoteSpec) (string, error) {
	a, _ := spec.Options["audience_id"].(string)
	if strings.TrimSpace(a) == "" {
		return "", fmt.Errorf("mailchimp: the trigger's audience_id is required: %w", effects.ErrFatal)
	}
	return a, nil
}

func split(id string) (string, string, error) {
	a, w, ok := strings.Cut(id, "/")
	if !ok || a == "" || w == "" {
		return "", "", fmt.Errorf("mailchimp: %q is no webhook of Taskiem's: %w", id, connector.ErrNotFound)
	}
	return a, w, nil
}

func (r *webhooks) call(ctx context.Context, c connector.RemoteCall, op, method, path string, body, out any) error {
	return r.c.call(ctx, c.Credentials, c.HTTP, op, method, path, body, out)
}

// Create makes the webhook on the audience, signed; Mailchimp returns the
// secret now only.
func (r *webhooks) Create(ctx context.Context, c connector.RemoteCall, spec connector.RemoteSpec) (connector.RemoteState, error) {
	aud, err := audienceOf(spec)
	if err != nil {
		return connector.RemoteState{}, err
	}
	body := map[string]any{"url": spec.URL, "events": eventsOf(spec), "sources": map[string]bool{"user": true, "admin": true, "api": true}, "signing_enabled": true}
	var w webhook
	if err := r.call(ctx, c, "creating the webhook", http.MethodPost, "/lists/"+url.PathEscape(aud)+"/webhooks", body, &w); err != nil {
		return connector.RemoteState{}, err
	}
	if w.ID == "" || w.Secret == "" {
		return connector.RemoteState{}, fmt.Errorf("mailchimp: the webhook came back without its id or signing secret: %w", effects.ErrUnknownOutcome)
	}
	return connector.RemoteState{ID: aud + "/" + w.ID, Name: spec.Name, Secret: w.Secret, Status: connector.RemoteHealthy}, nil
}

// Update keeps a webhook whose URL and events still match; any other is
// deleted and reported missing, so it is made again.
func (r *webhooks) Update(ctx context.Context, c connector.RemoteCall, id string, spec connector.RemoteSpec) (connector.RemoteState, error) {
	aud, wid, err := split(id)
	if err != nil {
		return connector.RemoteState{}, err
	}
	var w webhook
	if err := r.call(ctx, c, "reading the webhook", http.MethodGet, "/lists/"+url.PathEscape(aud)+"/webhooks/"+url.PathEscape(wid), nil, &w); err != nil {
		return connector.RemoteState{}, err
	}
	want, _ := audienceOf(spec)
	same := w.URL == spec.URL && want == aud
	for e, on := range eventsOf(spec) {
		same = same && w.Events[e] == on
	}
	if same {
		return connector.RemoteState{ID: id, Name: spec.Name, Status: connector.RemoteHealthy}, nil
	}
	if err := r.Delete(ctx, c, id); err != nil {
		return connector.RemoteState{}, err
	}
	return connector.RemoteState{}, fmt.Errorf("mailchimp: the webhook no longer matches: %w", connector.ErrNotFound)
}

// Get reads the webhook.
func (r *webhooks) Get(ctx context.Context, c connector.RemoteCall, id string) (connector.RemoteState, error) {
	aud, wid, err := split(id)
	if err != nil {
		return connector.RemoteState{}, err
	}
	if err := r.call(ctx, c, "reading the webhook", http.MethodGet, "/lists/"+url.PathEscape(aud)+"/webhooks/"+url.PathEscape(wid), nil, nil); err != nil {
		return connector.RemoteState{}, err
	}
	return connector.RemoteState{ID: id, Status: connector.RemoteHealthy}, nil
}

// Find looks in every audience for a webhook delivering to the
// subscription's URL; one found was made before a crash, with a secret
// Taskiem never kept: it is deleted and reported missing.
func (r *webhooks) Find(ctx context.Context, c connector.RemoteCall, name string) (connector.RemoteState, bool, error) {
	sub := strings.TrimPrefix(name, "taskiem-")
	var lists struct {
		Lists []struct {
			ID string `json:"id"`
		} `json:"lists"`
	}
	if err := r.call(ctx, c, "listing audiences", http.MethodGet, "/lists?count=1000&fields=lists.id", nil, &lists); err != nil {
		return connector.RemoteState{}, false, err
	}
	for _, l := range lists.Lists {
		var hooks struct {
			Webhooks []webhook `json:"webhooks"`
		}
		if err := r.call(ctx, c, "listing webhooks", http.MethodGet, "/lists/"+url.PathEscape(l.ID)+"/webhooks", nil, &hooks); err != nil {
			return connector.RemoteState{}, false, err
		}
		for _, w := range hooks.Webhooks {
			if u, err := url.Parse(w.URL); err == nil && u.Query().Get("subscription") == sub && sub != "" {
				if err := r.Delete(ctx, c, l.ID+"/"+w.ID); err != nil {
					return connector.RemoteState{}, false, err
				}
			}
		}
	}
	return connector.RemoteState{}, false, nil
}

// RotateSecret is not reached: Find never reports a webhook whose secret
// Taskiem lacks.
func (r *webhooks) RotateSecret(context.Context, connector.RemoteCall, string) (string, error) {
	return "", fmt.Errorf("mailchimp: a webhook's secret is not changed, the webhook is made again: %w", effects.ErrFatal)
}

// Delete removes the webhook; one already gone is not an error.
func (r *webhooks) Delete(ctx context.Context, c connector.RemoteCall, id string) error {
	aud, wid, err := split(id)
	if err != nil {
		return nil //nolint:nilerr // an id that names no webhook of Taskiem's: nothing to delete
	}
	err = r.call(ctx, c, "deleting the webhook", http.MethodDelete, "/lists/"+url.PathEscape(aud)+"/webhooks/"+url.PathEscape(wid), nil, nil)
	if errors.Is(err, connector.ErrNotFound) {
		return nil
	}
	return err
}

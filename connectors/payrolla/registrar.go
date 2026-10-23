package payrolla

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

// registrar manages the Payrolla webhook subscription behind a trigger
// (Payrolla's /connect/webhooks: list, create, delete; decision 0021).
//
// Payrolla keeps no name for a subscription and can neither change one
// nor rotate its secret, so:
//   - a subscription is Taskiem's when its URL carries Taskiem's
//     subscription id (the ingest URL's subscription parameter), which is
//     how Find recognises one created just before a crash;
//   - one found that way has a secret Taskiem never kept, so Find deletes
//     it and reports none, and the reconciler creates it again;
//   - Update keeps a subscription whose URL and events already match, and
//     otherwise deletes it and answers ErrNotFound, so it is created again
//     with a new secret.
type registrar struct {
	c *client
	// events are the trigger's own events: the subscription's when the
	// workflow names none.
	events []string
}

// webhook is Payrolla's WebhookSubscription (the fields Taskiem reads).
type webhook struct {
	ID             string   `json:"id"`
	URL            string   `json:"url"`
	EventTypes     []string `json:"event_types"`
	Status         string   `json:"status"`
	DisabledReason string   `json:"disabled_reason"`
	FailingSince   string   `json:"failing_since"`
}

func (w webhook) state(name string) connector.RemoteState {
	st := connector.RemoteState{ID: w.ID, Name: name}
	switch {
	case w.Status == "DISABLED":
		// After a 410, three days of failures, or the owner pausing it;
		// only the owner can turn it back on (Payrolla has no resume API).
		st.Status, st.Reason = connector.RemotePaused, w.DisabledReason
		if st.Reason == "" {
			st.Reason = "switched off in Payrolla; the owner can turn it back on in Settings → Connected apps"
		}
	case w.FailingSince != "":
		st.Status, st.Reason = connector.RemoteFailing, "deliveries failing since "+w.FailingSince
	case w.Status == "ACTIVE":
		st.Status = connector.RemoteHealthy
	}
	return st
}

// subscriptionOf is the Taskiem subscription id a URL carries.
func subscriptionOf(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return p.Query().Get("subscription")
}

// eventTypes are what the subscription should receive: the workflow's
// events, or the trigger's, never Payrolla's ping (sent only on request).
func (r registrar) eventTypes(spec connector.RemoteSpec) []string {
	from := spec.Events
	if len(from) == 0 {
		from = r.events
	}
	var out []string
	for _, e := range from {
		if e != "ping" && slices.Contains(r.events, e) && !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return nil
	}
	slices.Sort(out)
	return out
}

func (r registrar) list(ctx context.Context, c connector.RemoteCall) ([]webhook, error) {
	var out struct {
		Webhooks []webhook `json:"webhooks"`
	}
	if err := r.c.do(ctx, c.Credentials, c.HTTP, http.MethodGet, "/connect/webhooks", nil, &out); err != nil {
		return nil, err
	}
	return out.Webhooks, nil
}

func (r registrar) Create(ctx context.Context, c connector.RemoteCall, spec connector.RemoteSpec) (connector.RemoteState, error) {
	types := r.eventTypes(spec)
	if len(types) == 0 {
		return connector.RemoteState{}, fmt.Errorf("payrolla: the workflow names no event this trigger sends: %w", effects.ErrFatal)
	}
	var out struct {
		Webhook webhook `json:"webhook"`
		Secret  string  `json:"signing_secret"`
	}
	if err := r.c.do(ctx, c.Credentials, c.HTTP, http.MethodPost, "/connect/webhooks", map[string]any{"url": spec.URL, "event_types": types}, &out); err != nil {
		return connector.RemoteState{}, err
	}
	if out.Webhook.ID == "" || out.Secret == "" {
		return connector.RemoteState{}, fmt.Errorf("payrolla: the subscription came back without its id or signing secret: %w", effects.ErrUnknownOutcome)
	}
	st := out.Webhook.state(spec.Name)
	st.Secret = out.Secret
	return st, nil
}

func (r registrar) Update(ctx context.Context, c connector.RemoteCall, id string, spec connector.RemoteSpec) (connector.RemoteState, error) {
	hooks, err := r.list(ctx, c)
	if err != nil {
		return connector.RemoteState{}, err
	}
	i := slices.IndexFunc(hooks, func(w webhook) bool { return w.ID == id })
	if i < 0 {
		return connector.RemoteState{}, fmt.Errorf("payrolla: subscription %s: %w", id, connector.ErrNotFound)
	}
	w := hooks[i]
	have := slices.Clone(w.EventTypes)
	slices.Sort(have)
	if w.URL == spec.URL && slices.Equal(have, r.eventTypes(spec)) {
		return w.state(spec.Name), nil
	}
	// Payrolla cannot change a subscription: replace it.
	if err := r.Delete(ctx, c, id); err != nil {
		return connector.RemoteState{}, err
	}
	return connector.RemoteState{}, fmt.Errorf("payrolla: subscription %s replaced: %w", id, connector.ErrNotFound)
}

func (r registrar) Get(ctx context.Context, c connector.RemoteCall, id string) (connector.RemoteState, error) {
	hooks, err := r.list(ctx, c)
	if err != nil {
		return connector.RemoteState{}, err
	}
	for _, w := range hooks {
		if w.ID == id {
			return w.state(""), nil
		}
	}
	return connector.RemoteState{}, fmt.Errorf("payrolla: subscription %s: %w", id, connector.ErrNotFound)
}

// Find looks for the subscription by the id in its URL. One found is
// deleted rather than adopted: its signing secret was shown once and
// never kept, and Payrolla cannot rotate it.
func (r registrar) Find(ctx context.Context, c connector.RemoteCall, name string) (connector.RemoteState, bool, error) {
	hooks, err := r.list(ctx, c)
	if err != nil {
		return connector.RemoteState{}, false, err
	}
	sub := strings.TrimPrefix(name, "taskiem-")
	for _, w := range hooks {
		if sub != "" && subscriptionOf(w.URL) == sub {
			if err := r.Delete(ctx, c, w.ID); err != nil {
				return connector.RemoteState{}, false, err
			}
		}
	}
	return connector.RemoteState{}, false, nil
}

// RotateSecret is never reached (Find adopts nothing); Payrolla cannot
// rotate a subscription's secret.
func (r registrar) RotateSecret(context.Context, connector.RemoteCall, string) (string, error) {
	return "", fmt.Errorf("payrolla: a subscription's signing secret cannot be rotated; delete it and create it again: %w", effects.ErrFatal)
}

func (r registrar) Delete(ctx context.Context, c connector.RemoteCall, id string) error {
	err := r.c.do(ctx, c.Credentials, c.HTTP, http.MethodDelete, "/connect/webhooks/"+url.PathEscape(id), nil, nil)
	if errors.Is(err, connector.ErrNotFound) {
		return nil // already gone
	}
	return err
}

package iswallet

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

// registrar holds a connection's webhook subscription for one trigger at
// iswallet (registration: connection; docs/spec/iswallet-account-numbers.md
// I7). iswallet has no page where a business pastes a URL: the
// subscription is made through its API (POST /v1/webhooks/subscriptions
// with endpoint_url and the event types, exactly, no wildcards), changed
// with PATCH, and its signing secret is returned once, at creation (27
// October 2026). The endpoint must be https and public; iswallet retries a
// delivery 10 times and then dead-letters it, and lists each attempt with
// our answer (GET …/deliveries), which the health check reads.
type registrar struct {
	c       *client
	trigger string
}

// subscription is iswallet's webhook subscription (the fields Taskiem reads).
type subscription struct {
	ID             string   `json:"id"`
	SubscriptionID string   `json:"subscription_id"`
	EndpointURL    string   `json:"endpoint_url"`
	EventTypes     []string `json:"event_types"`
	Status         string   `json:"status"`
	SigningSecret  string   `json:"signing_secret"`
}

func (s subscription) id() string {
	if s.ID != "" {
		return s.ID
	}
	return s.SubscriptionID
}

func (s subscription) state() connector.RemoteState {
	st := connector.RemoteState{ID: s.id(), Secret: s.SigningSecret}
	switch strings.ToLower(s.Status) {
	case "", "active", "enabled", "healthy":
		st.Status = connector.RemoteHealthy
	case "paused", "disabled", "inactive":
		st.Status, st.Reason = connector.RemotePaused, "iswallet says this subscription is "+s.Status
	default:
		st.Status, st.Reason = connector.RemoteBroken, "iswallet says this subscription is "+s.Status
	}
	return st
}

const subscriptionsPath = "/v1/webhooks/subscriptions"

// registrarRequest is the request a registrar call sends: the connection's key, and
// the subscription's name as the Idempotency-Key a create needs, so a
// create resent after a lost answer makes no second subscription.
func registrarRequest(c connector.RemoteCall, name string) connector.Request {
	req := c.Request()
	req.IdempotencyKey = name
	return req
}

// eventTypes are the events to subscribe to: those asked for, else every
// event the trigger sends (iswallet takes exact names only).
func (r registrar) eventTypes(spec connector.RemoteSpec) []string {
	ev := spec.Events
	if len(ev) == 0 {
		ev = r.c.m.Triggers[r.trigger].Events
	}
	out := append([]string(nil), ev...)
	sort.Strings(out)
	return out
}

func (r registrar) Create(ctx context.Context, c connector.RemoteCall, spec connector.RemoteSpec) (connector.RemoteState, error) {
	if err := allowWallet(c.Request(), "wallet_id", ""); errors.Is(err, ErrAllowListMissing) {
		return connector.RemoteState{}, err
	}
	var s subscription
	body := map[string]any{"endpoint_url": spec.URL, "event_types": r.eventTypes(spec)}
	if _, err := r.c.doID(ctx, registrarRequest(c, spec.Name), http.MethodPost, subscriptionsPath, body, &s); err != nil {
		return connector.RemoteState{}, err
	}
	if s.id() == "" || s.SigningSecret == "" {
		return connector.RemoteState{}, fmt.Errorf("iswallet: the subscription came back without its id or signing secret: %w", effects.ErrUnknownOutcome)
	}
	return s.state(), nil
}

func (r registrar) Update(ctx context.Context, c connector.RemoteCall, id string, spec connector.RemoteSpec) (connector.RemoteState, error) {
	var s subscription
	body := map[string]any{"endpoint_url": spec.URL, "event_types": r.eventTypes(spec)}
	if _, err := r.c.doID(ctx, c.Request(), http.MethodPatch, subscriptionsPath+"/"+url.PathEscape(id), body, &s); err != nil {
		return connector.RemoteState{}, err
	}
	if s.id() == "" {
		s.ID = id
	}
	st := s.state()
	st.Secret = "" // a PATCH never returns the secret; the one kept stays
	return st, nil
}

// recentDeliveries is how many of a subscription's last attempts the
// health check reads.
const recentDeliveries = 20

// delivery is one attempt iswallet made, with what our endpoint answered.
type delivery struct {
	EventType      string `json:"event_type"`
	Status         string `json:"status"`
	StatusCode     int    `json:"status_code"`
	ResponseStatus int    `json:"response_status"`
	ResponseBody   string `json:"response_body"`
	AttemptedAt    string `json:"attempted_at"`
	DeliveredAt    string `json:"delivered_at"`
}

func (d delivery) code() int {
	if d.StatusCode != 0 {
		return d.StatusCode
	}
	return d.ResponseStatus
}

// Get reads the subscription and its last deliveries: one whose latest
// attempt got anything but a 2xx from Taskiem is failing, with iswallet's
// record of our answer as the reason, so "we never got the webhook" is
// answered without asking iswallet.
func (r registrar) Get(ctx context.Context, c connector.RemoteCall, id string) (connector.RemoteState, error) {
	var s subscription
	if _, err := r.c.doID(ctx, c.Request(), http.MethodGet, subscriptionsPath+"/"+url.PathEscape(id), nil, &s); err != nil {
		return connector.RemoteState{}, err
	}
	if s.id() == "" {
		s.ID = id
	}
	st := s.state()
	st.Secret = ""
	if st.Status != connector.RemoteHealthy {
		return st, nil
	}
	var d struct {
		Deliveries []delivery `json:"deliveries"`
		Data       []delivery `json:"data"`
	}
	q := url.Values{"limit": {fmt.Sprint(recentDeliveries)}}
	if _, err := r.c.doID(ctx, c.Request(), http.MethodGet, subscriptionsPath+"/"+url.PathEscape(id)+"/deliveries?"+q.Encode(), nil, &d); err != nil {
		return connector.RemoteState{}, err
	}
	list := d.Deliveries
	if list == nil {
		list = d.Data
	}
	if last, ok := latest(list); ok && (last.code() < 200 || last.code() > 299) {
		st.Status = connector.RemoteFailing
		st.Reason = failedAttempt(last)
	}
	return st, nil
}

// latest is the most recent attempt (RFC 3339 times sort as text).
func latest(list []delivery) (delivery, bool) {
	var out delivery
	when := ""
	for _, d := range list {
		t := d.AttemptedAt
		if t == "" {
			t = d.DeliveredAt
		}
		if t >= when {
			out, when = d, t
		}
	}
	return out, len(list) > 0
}

// failedAttempt says in plain words what iswallet got from Taskiem.
func failedAttempt(d delivery) string {
	body := strings.TrimSpace(d.ResponseBody)
	if len(body) > 200 {
		body = body[:200]
	}
	got := "no answer"
	if d.code() != 0 {
		got = fmt.Sprintf("HTTP %d", d.code())
	}
	msg := fmt.Sprintf("iswallet's last delivery (%s) got %s from Taskiem", d.EventType, got)
	if body != "" {
		msg += ": " + body
	}
	return msg
}

// Find looks for a subscription made for this row before a crash: iswallet
// subscriptions carry no name, so it is the one whose endpoint_url names the
// row (subscription=<id>). Its secret was never kept and iswallet cannot
// rotate one, so it is deleted and reported absent: the reconciler then
// creates it again, secret and all.
func (r registrar) Find(ctx context.Context, c connector.RemoteCall, name string) (connector.RemoteState, bool, error) {
	row := strings.TrimPrefix(name, "taskiem-")
	var out struct {
		Subscriptions []subscription `json:"subscriptions"`
		Data          []subscription `json:"data"`
	}
	_, err := r.c.doID(ctx, c.Request(), http.MethodGet, subscriptionsPath, nil, &out)
	if errors.Is(err, connector.ErrNotFound) {
		return connector.RemoteState{}, false, nil // none yet
	}
	if err != nil {
		return connector.RemoteState{}, false, err
	}
	list := out.Subscriptions
	if list == nil {
		list = out.Data
	}
	for _, s := range list {
		u, err := url.Parse(s.EndpointURL)
		if err != nil || u.Query().Get("subscription") != row {
			continue
		}
		if err := r.Delete(ctx, c, s.id()); err != nil {
			return connector.RemoteState{}, false, err
		}
	}
	return connector.RemoteState{}, false, nil
}

// RotateSecret: iswallet documents no rotation. Find never adopts a
// subscription, so this is not called.
func (r registrar) RotateSecret(context.Context, connector.RemoteCall, string) (string, error) {
	return "", fmt.Errorf("iswallet cannot rotate a subscription's secret; it is made again instead: %w", effects.ErrFatal)
}

func (r registrar) Delete(ctx context.Context, c connector.RemoteCall, id string) error {
	_, err := r.c.doID(ctx, c.Request(), http.MethodDelete, subscriptionsPath+"/"+url.PathEscape(id), nil, nil)
	if errors.Is(err, connector.ErrNotFound) {
		return nil // already gone
	}
	return err
}

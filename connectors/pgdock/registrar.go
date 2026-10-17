package pgdock

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

// registrar manages the Xenibase webhook behind a row_changed trigger
// through Xenibase's webhook API (POST/GET/PATCH/DELETE
// /projects/{id}/webhooks, rotate-secret; docs/webhooks.md, "Managing
// webhooks from another tool"). Taskiem's webhooks are named after their
// subscription (connector.RemoteSpec.Name) and tagged with it in metadata
// (taskiem_subscription), which is how a webhook created just before a
// crash is found again, by the tag first, since a person may rename it.
type registrar struct{ c *client }

// metaSubscription is the metadata key that tags a webhook as a Taskiem
// subscription's; its value is the subscription id.
const metaSubscription = "taskiem_subscription"

// subscriptionOf is the subscription id a webhook name carries
// (taskiem-<id>).
func subscriptionOf(name string) string { return strings.TrimPrefix(name, "taskiem-") }

// webhook is Xenibase's Webhook (the fields Taskiem reads).
type webhook struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Metadata     map[string]string `json:"metadata"`
	Tables       []string          `json:"tables"`
	Events       []string          `json:"events"`
	Columns      []string          `json:"columns"`
	URL          string            `json:"url"`
	Enabled      bool              `json:"enabled"`
	Status       string            `json:"status"`
	StatusReason *string           `json:"status_reason"`
	Backlog      int64             `json:"backlog"`
}

func (w webhook) state() connector.RemoteState {
	st := connector.RemoteState{ID: w.ID, Name: w.Name}
	switch w.Status {
	case "healthy", "failing", "paused", "broken":
		st.Status = w.Status
	}
	if !w.Enabled && st.Status != connector.RemoteBroken {
		st.Status = connector.RemotePaused
	}
	if w.StatusReason != nil {
		st.Reason = *w.StatusReason
	}
	return st
}

// pgEvents are the change events Xenibase sends; TEST is sent on request
// and needs no subscription.
var pgEvents = []string{"INSERT", "UPDATE", "DELETE"}

func strs(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	if l, ok := v.([]string); ok {
		out = append(out, l...)
	}
	return out
}

// request is the webhook Xenibase should hold for spec: its tables,
// events and URL, a description, and the subscription tag in metadata.
func request(spec connector.RemoteSpec, update bool) (map[string]any, error) {
	tables := strs(spec.Options["tables"])
	if len(tables) == 0 {
		return nil, fmt.Errorf("pgdock: the trigger names no tables: %w", effects.ErrFatal)
	}
	var events []string
	for _, e := range spec.Events {
		if slices.Contains(pgEvents, e) && !slices.Contains(events, e) {
			events = append(events, e)
		}
	}
	if len(events) == 0 {
		events = pgEvents // no filter, or only TEST: every change
	}
	body := map[string]any{"name": spec.Name, "tables": tables, "events": events, "url": spec.URL, "enabled": true,
		"description": "Managed by Taskiem", "metadata": map[string]string{metaSubscription: subscriptionOf(spec.Name)}}
	if cols := strs(spec.Options["columns"]); len(cols) > 0 {
		body["columns"] = cols
	} else if update {
		body["columns"] = nil // clear a filter set before
	}
	return body, nil
}

func webhooksPath(creds map[string]string) (string, error) {
	p, err := project(creds)
	if err != nil {
		return "", err
	}
	return "/api/v1/projects/" + p + "/webhooks", nil
}

func (r registrar) Create(ctx context.Context, c connector.RemoteCall, spec connector.RemoteSpec) (connector.RemoteState, error) {
	body, err := request(spec, false)
	if err != nil {
		return connector.RemoteState{}, err
	}
	path, err := webhooksPath(c.Credentials)
	if err != nil {
		return connector.RemoteState{}, err
	}
	var out struct {
		Webhook webhook `json:"webhook"`
		Secret  string  `json:"secret"`
	}
	if err := r.c.do(ctx, c.Credentials, c.HTTP, http.MethodPost, path, body, &out); err != nil {
		return connector.RemoteState{}, err
	}
	if out.Webhook.ID == "" || out.Secret == "" {
		return connector.RemoteState{}, fmt.Errorf("pgdock: the created webhook came back without its id or secret: %w", effects.ErrUnknownOutcome)
	}
	st := out.Webhook.state()
	st.Secret = out.Secret
	return st, nil
}

func (r registrar) Update(ctx context.Context, c connector.RemoteCall, id string, spec connector.RemoteSpec) (connector.RemoteState, error) {
	body, err := request(spec, true)
	if err != nil {
		return connector.RemoteState{}, err
	}
	path, err := webhooksPath(c.Credentials)
	if err != nil {
		return connector.RemoteState{}, err
	}
	var out webhook
	if err := r.c.do(ctx, c.Credentials, c.HTTP, http.MethodPatch, path+"/"+url.PathEscape(id), body, &out); err != nil {
		return connector.RemoteState{}, err
	}
	return out.state(), nil
}

func (r registrar) Get(ctx context.Context, c connector.RemoteCall, id string) (connector.RemoteState, error) {
	path, err := webhooksPath(c.Credentials)
	if err != nil {
		return connector.RemoteState{}, err
	}
	var out webhook
	if err := r.c.do(ctx, c.Credentials, c.HTTP, http.MethodGet, path+"/"+url.PathEscape(id), nil, &out); err != nil {
		return connector.RemoteState{}, err
	}
	return out.state(), nil
}

func (r registrar) Find(ctx context.Context, c connector.RemoteCall, name string) (connector.RemoteState, bool, error) {
	path, err := webhooksPath(c.Credentials)
	if err != nil {
		return connector.RemoteState{}, false, err
	}
	var out struct {
		Items []webhook `json:"items"`
	}
	if err := r.c.do(ctx, c.Credentials, c.HTTP, http.MethodGet, path, nil, &out); err != nil {
		return connector.RemoteState{}, false, err
	}
	// By the subscription tag first (a person may have renamed the
	// webhook), then by name (a webhook created before tags were sent).
	for _, w := range out.Items {
		if w.Metadata[metaSubscription] == subscriptionOf(name) {
			return w.state(), true, nil
		}
	}
	for _, w := range out.Items {
		if w.Name == name {
			return w.state(), true, nil
		}
	}
	return connector.RemoteState{}, false, nil
}

func (r registrar) RotateSecret(ctx context.Context, c connector.RemoteCall, id string) (string, error) {
	path, err := webhooksPath(c.Credentials)
	if err != nil {
		return "", err
	}
	var out struct {
		Secret string `json:"secret"`
	}
	if err := r.c.do(ctx, c.Credentials, c.HTTP, http.MethodPost, path+"/"+url.PathEscape(id)+"/rotate-secret", nil, &out); err != nil {
		return "", err
	}
	if out.Secret == "" {
		return "", fmt.Errorf("pgdock: the rotated secret came back empty: %w", effects.ErrUnknownOutcome)
	}
	return out.Secret, nil
}

func (r registrar) Delete(ctx context.Context, c connector.RemoteCall, id string) error {
	path, err := webhooksPath(c.Credentials)
	if err != nil {
		return err
	}
	err = r.c.do(ctx, c.Credentials, c.HTTP, http.MethodDelete, path+"/"+url.PathEscape(id), nil, nil)
	if errors.Is(err, connector.ErrNotFound) {
		return nil // already gone
	}
	return err
}

// --- truncated events ---

// enrich completes a truncated row-changed event (over 256 KB, sent
// without its records) when the workflow asked for it
// (options.refetch_truncated): the row is fetched by its primary_key,
// through the data API (GET /data/v1/{table}/{key}, a single-column key)
// when the connection has its secret key and URL, else through the
// management rows endpoint, so it may have changed since the event. The
// event stays marked truncated, with refetched true and record set, or
// refetched false and refetch_error saying why (the row is gone, the key
// is not one PGDock sent as an object). A refusal worth retrying (429,
// 503, a timeout) refuses the delivery, and PGDock sends it again.
func (c *client) enrich(ctx context.Context, call connector.EventCall, body any) (any, error) {
	ev, ok := body.(map[string]any)
	if !ok || ev["truncated"] != true {
		return body, nil
	}
	if want, _ := call.Options["refetch_truncated"].(bool); !want {
		return body, nil
	}
	out := make(map[string]any, len(ev)+2)
	for k, v := range ev {
		out[k] = v
	}
	fail := func(why string) (any, error) {
		out["refetched"], out["refetch_error"] = false, why
		return out, nil
	}
	if t, _ := ev["type"].(string); t == "DELETE" {
		return fail("a deleted row cannot be fetched")
	}
	key, ok := ev["primary_key"].(map[string]any)
	if !ok || len(key) == 0 {
		return fail("the event's primary_key is not an object of key columns")
	}
	table, _ := ev["table"].(string)
	if table == "" {
		return fail("the event names no table")
	}
	schema, name := splitTable(table)
	creds, hc, err := call.Connect()
	if err != nil {
		return nil, err
	}
	row, found, err := c.rowByKey(ctx, creds, hc, schema, name, key)
	switch {
	case err != nil && effects.Classify(err) == effects.KindFatal:
		return fail(err.Error())
	case err != nil:
		return nil, err
	case !found:
		return fail("the row no longer exists")
	}
	out["record"], out["refetched"] = row, true
	return out, nil
}

// rowByKey fetches one row by its primary key: through the data API when
// the connection has its credentials and the key is one column, else
// through the management rows endpoint.
func (c *client) rowByKey(ctx context.Context, creds map[string]string, hc *http.Client, schema, table string, key map[string]any) (map[string]any, bool, error) {
	if _, _, err := dataAPI(creds); err != nil || len(key) != 1 {
		return c.row(ctx, creds, hc, schema, table, key)
	}
	var col string
	for k := range key {
		col = k
	}
	text, err := valueText(key[col])
	if err != nil || text == "" {
		return c.row(ctx, creds, hc, schema, table, key)
	}
	name := table
	if schema != "" && schema != "public" {
		name = schema + "." + table
	}
	if !identRe.MatchString(name) {
		return nil, false, fmt.Errorf("pgdock: %q is not a table: %w", name, effects.ErrFatal)
	}
	var out struct {
		Data map[string]any `json:"data"`
	}
	_, err = c.doData(ctx, connector.Request{Credentials: creds, HTTP: hc, Attempt: 1}, http.MethodGet, "/data/v1/"+url.PathEscape(name)+"/"+url.PathEscape(text), nil, nil, &out)
	switch {
	case errors.Is(err, connector.ErrNotFound):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	return out.Data, out.Data != nil, nil
}

package zohocrm

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/israel-duff/taskiem/connectors/internal/zoho"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

// watches manages the CRM notification watch behind the record_changes
// trigger (CRM's /actions/watch; decision 0021).
//
//   - A watch's channel id is Taskiem's subscription name hashed into a
//     number, so a watch made just before a crash is found again by name,
//     and stopped (its token was never kept): the reconciler makes it
//     again.
//   - Its token is Taskiem's: a random secret CRM sends back in every
//     notification (body_token), kept in the vault.
//   - A watch lasts a week at most; Taskiem asks for six days and renews
//     it (Update, which sets a new expiry) before it lapses
//     (RemoteState.ExpiresAt).
type watches struct{ c *client }

// lifetime is how long a watch is asked for: under CRM's week, so a
// renewal a day early never asks for more than CRM allows.
const lifetime = 6 * 24 * time.Hour

// channelID is a subscription name as a CRM channel id: a positive number.
func channelID(name string) string {
	sum := sha256.Sum256([]byte(name))
	return strconv.FormatUint(binary.BigEndian.Uint64(sum[:8])>>12, 10) // within JavaScript's safe integers
}

// events are the CRM events a workflow's watch asks for: the module's
// create, edit and delete, or all of them.
func events(spec connector.RemoteSpec) ([]string, error) {
	mod, _ := spec.Options["module"].(string)
	if mod == "" {
		return nil, fmt.Errorf("zoho crm: the trigger's module is required: %w", effects.ErrFatal)
	}
	ops := map[string]string{"insert": "create", "update": "edit", "delete": "delete"}
	var out []string
	for _, e := range spec.Events {
		if op, ok := ops[e]; ok {
			out = append(out, mod+"."+op)
		}
	}
	if len(out) == 0 || len(out) == len(ops) {
		return []string{mod + ".all"}, nil
	}
	slices.Sort(out)
	return out, nil
}

func newToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil // 48 characters, under CRM's 50
}

func (w *watches) session(c connector.RemoteCall) *zoho.Session { return w.c.zoho.Open(c.Request()) }

func (w *watches) send(ctx context.Context, c connector.RemoteCall, op, method string, watch map[string]any) (time.Time, error) {
	var resp struct {
		Watch []struct {
			Code    string `json:"code"`
			Status  string `json:"status"`
			Message string `json:"message"`
			Details struct {
				Events []struct {
					Expiry string `json:"channel_expiry"`
				} `json:"events"`
			} `json:"details"`
		} `json:"watch"`
	}
	if err := w.c.do(ctx, w.session(c), op, method, "/actions/watch", nil, map[string]any{"watch": []any{watch}}, &resp); err != nil {
		return time.Time{}, err
	}
	if len(resp.Watch) == 0 {
		return time.Time{}, fmt.Errorf("zoho crm %s: the answer has no watch: %w", op, effects.ErrUnknownOutcome)
	}
	r := resp.Watch[0]
	if r.Status == "error" {
		f := fault{Code: r.Code, Message: r.Message}
		return time.Time{}, f.err(op, http.StatusBadRequest)
	}
	var exp time.Time
	for _, e := range r.Details.Events {
		if t, ok := zoho.ParseTime(e.Expiry); ok && (exp.IsZero() || t.Before(exp)) {
			exp = t
		}
	}
	return exp, nil
}

func (w *watches) watch(spec connector.RemoteSpec, id string) (map[string]any, error) {
	evs, err := events(spec)
	if err != nil {
		return nil, err
	}
	expiry := w.c.now().Add(lifetime)
	return map[string]any{"channel_id": id, "events": evs, "notify_url": spec.URL, "channel_expiry": expiry.UTC().Format(time.RFC3339), "notify_on_related_action": false}, nil
}

// Create asks CRM to watch the module, with a token of Taskiem's.
func (w *watches) Create(ctx context.Context, c connector.RemoteCall, spec connector.RemoteSpec) (connector.RemoteState, error) {
	id := channelID(spec.Name)
	body, err := w.watch(spec, id)
	if err != nil {
		return connector.RemoteState{}, err
	}
	tok, err := newToken()
	if err != nil {
		return connector.RemoteState{}, err
	}
	body["token"] = tok
	exp, err := w.send(ctx, c, "watching the module", http.MethodPost, body)
	if err != nil {
		return connector.RemoteState{}, err
	}
	if exp.IsZero() {
		exp = w.c.now().Add(lifetime)
	}
	return connector.RemoteState{ID: id, Name: spec.Name, Secret: tok, Status: connector.RemoteHealthy, ExpiresAt: exp}, nil
}

// Update makes the watch match spec and renews it for another six days;
// a watch CRM no longer has is ErrNotFound, so it is created again.
func (w *watches) Update(ctx context.Context, c connector.RemoteCall, id string, spec connector.RemoteSpec) (connector.RemoteState, error) {
	if _, err := w.Get(ctx, c, id); err != nil {
		return connector.RemoteState{}, err
	}
	body, err := w.watch(spec, id)
	if err != nil {
		return connector.RemoteState{}, err
	}
	exp, err := w.send(ctx, c, "renewing the watch", http.MethodPatch, body)
	if err != nil {
		return connector.RemoteState{}, err
	}
	if exp.IsZero() {
		exp = w.c.now().Add(lifetime)
	}
	return connector.RemoteState{ID: id, Name: spec.Name, Status: connector.RemoteHealthy, ExpiresAt: exp}, nil
}

// Get reads the watch; one CRM no longer has, or that has lapsed, is
// ErrNotFound.
func (w *watches) Get(ctx context.Context, c connector.RemoteCall, id string) (connector.RemoteState, error) {
	var resp struct {
		Watch []struct {
			ChannelID zoho.ID `json:"channel_id"`
			Events    []struct {
				Expiry string `json:"channel_expiry"`
			} `json:"events"`
		} `json:"watch"`
	}
	err := w.c.do(ctx, w.session(c), "reading the watch", http.MethodGet, "/actions/watch", nil, nil, &resp)
	if err != nil {
		return connector.RemoteState{}, err
	}
	for _, x := range resp.Watch {
		if string(x.ChannelID) != id {
			continue
		}
		st := connector.RemoteState{ID: id, Status: connector.RemoteHealthy}
		for _, e := range x.Events {
			if t, ok := zoho.ParseTime(e.Expiry); ok && (st.ExpiresAt.IsZero() || t.Before(st.ExpiresAt)) {
				st.ExpiresAt = t
			}
		}
		if !st.ExpiresAt.IsZero() && st.ExpiresAt.Before(w.c.now()) {
			break // lapsed: as good as gone
		}
		return st, nil
	}
	return connector.RemoteState{}, fmt.Errorf("zoho crm: no watch %s: %w", id, connector.ErrNotFound)
}

// Find looks for the watch a name makes. One found was made before a
// crash, with a token Taskiem never kept, and CRM cannot be asked to change
// only the token: it is stopped and reported missing, so the reconciler
// makes a new one.
func (w *watches) Find(ctx context.Context, c connector.RemoteCall, name string) (connector.RemoteState, bool, error) {
	id := channelID(name)
	_, err := w.Get(ctx, c, id)
	if errors.Is(err, connector.ErrNotFound) {
		return connector.RemoteState{}, false, nil
	}
	if err != nil {
		return connector.RemoteState{}, false, err
	}
	return connector.RemoteState{}, false, w.Delete(ctx, c, id)
}

// RotateSecret is not reached: Find never reports a watch whose token
// Taskiem lacks.
func (w *watches) RotateSecret(context.Context, connector.RemoteCall, string) (string, error) {
	return "", fmt.Errorf("zoho crm: a watch's token is not changed, the watch is made again: %w", effects.ErrFatal)
}

// Delete stops the watch; one already gone is not an error.
func (w *watches) Delete(ctx context.Context, c connector.RemoteCall, id string) error {
	err := w.c.do(ctx, w.session(c), "stopping the watch", http.MethodDelete, "/actions/watch", url.Values{"channel_ids": {id}}, nil, nil)
	if errors.Is(err, connector.ErrNotFound) {
		return nil
	}
	return err
}

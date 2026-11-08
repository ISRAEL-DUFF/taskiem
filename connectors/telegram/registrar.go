package telegram

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

// webhook registers a bot's one webhook (decision 0021; decision 0062 C7),
// so the owner never calls setWebhook by hand. A bot has one webhook,
// which is why it is the connection's, whichever workflows use the bot;
// and setting it stops whatever else read the bot's updates (another
// webhook, getUpdates), which is why it is set only while a published
// workflow starts on the bot's updates (registration:
// connection_when_used), never for a bot used only to send.
//
// Telegram keeps no name for a webhook and never shows its secret token,
// and Taskiem keeps the secret only when the subscription is created or
// adopted. So the token is derived from the bot token and the
// subscription's name: the same on every setWebhook, and unguessable
// without the bot token, which can change the webhook anyway.
type webhook struct{ c *client }

func secretFor(c connector.RemoteCall, name string) (string, error) {
	tok := strings.TrimSpace(c.Credentials["bot_token"])
	if tok == "" {
		return "", fmt.Errorf("telegram: the connection has no bot_token: %w", effects.ErrFatal)
	}
	m := hmac.New(sha256.New, []byte(tok))
	m.Write([]byte("taskiem-webhook:" + name))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)), nil
}

func (w webhook) set(ctx context.Context, c connector.RemoteCall, spec connector.RemoteSpec) (connector.RemoteState, error) {
	if !strings.HasPrefix(spec.URL, "https://") && !strings.HasPrefix(spec.URL, "http://127.0.0.1") {
		return connector.RemoteState{}, fmt.Errorf("telegram: a webhook must be https: %w", effects.ErrFatal)
	}
	secret, err := secretFor(c, spec.Name)
	if err != nil {
		return connector.RemoteState{}, err
	}
	// One bot connected in two environments, each with a published
	// workflow on it: the second would take the webhook from the first,
	// and the first's repair would take it back. It is refused instead,
	// in words the Connections page shows.
	if c.Claimed != nil {
		info, err := w.info(ctx, c)
		if err != nil {
			return connector.RemoteState{}, err
		}
		if cur := str(info, "url"); cur != "" && !ours(info, spec.Name) {
			env, err := c.Claimed(ctx, cur)
			if err != nil {
				return connector.RemoteState{}, err
			}
			if env != "" {
				return connector.RemoteState{}, fmt.Errorf("telegram: this bot already receives messages for %s; use one bot per environment: %w", env, effects.ErrFatal)
			}
		}
	}
	// Telegram's own default for allowed_updates (every kind but chat
	// member and reaction updates) is kept; the workflows' events filter.
	if err := w.c.call(ctx, c.Request(), "setWebhook", map[string]any{"url": spec.URL, "secret_token": secret}, nil); err != nil {
		return connector.RemoteState{}, err
	}
	return connector.RemoteState{ID: spec.Name, Name: spec.Name, Secret: secret, Status: connector.RemoteHealthy}, nil
}

func (w webhook) info(ctx context.Context, c connector.RemoteCall) (map[string]any, error) {
	var r any
	if err := w.c.call(ctx, c.Request(), "getWebhookInfo", nil, &r); err != nil {
		return nil, err
	}
	m, _ := r.(map[string]any)
	return m, nil
}

// ours reports whether the bot's webhook is the subscription named name:
// its URL carries the subscription's id.
func ours(info map[string]any, name string) bool {
	u, err := url.Parse(str(info, "url"))
	if err != nil || u.Host == "" {
		return false
	}
	id := strings.TrimPrefix(name, "taskiem-")
	return id != "" && (u.Query().Get("subscription") == id || strings.Contains(u.Path, id))
}

func (w webhook) Create(ctx context.Context, c connector.RemoteCall, spec connector.RemoteSpec) (connector.RemoteState, error) {
	return w.set(ctx, c, spec)
}

func (w webhook) Update(ctx context.Context, c connector.RemoteCall, id string, spec connector.RemoteSpec) (connector.RemoteState, error) {
	st, err := w.set(ctx, c, spec)
	// The secret was kept when it was created; it has not changed.
	st.Secret = ""
	return st, err
}

func (w webhook) Get(ctx context.Context, c connector.RemoteCall, id string) (connector.RemoteState, error) {
	info, err := w.info(ctx, c)
	if err != nil {
		return connector.RemoteState{}, err
	}
	if !ours(info, id) {
		// Someone set another webhook on the bot (or removed it).
		return connector.RemoteState{}, connector.ErrNotFound
	}
	return health(info, id, now()), nil
}

// now is the clock health reads; tests replace it.
var now = time.Now

// recentError is how long a delivery error Telegram reports counts.
const recentError = time.Hour

// health reads getWebhookInfo: failing while updates wait behind an error,
// or when the last error is under an hour old (Telegram retries, so a
// webhook that fails now and then has nothing pending between failures);
// healthy otherwise.
func health(info map[string]any, id string, at time.Time) connector.RemoteState {
	st := connector.RemoteState{ID: id, Name: id, Status: connector.RemoteHealthy}
	msg := str(info, "last_error_message")
	pending, _ := intOf(info["pending_update_count"])
	when, _ := intOf(info["last_error_date"])
	recent := when > 0 && at.Sub(time.Unix(when, 0)) < recentError
	if msg != "" && pending > 0 || recent {
		if msg == "" {
			msg = "no reason given"
		}
		st.Status, st.Reason = connector.RemoteFailing, "Telegram could not deliver updates: "+msg
	}
	return st
}

func (w webhook) Find(ctx context.Context, c connector.RemoteCall, name string) (connector.RemoteState, bool, error) {
	info, err := w.info(ctx, c)
	if err != nil {
		return connector.RemoteState{}, false, err
	}
	if !ours(info, name) {
		return connector.RemoteState{}, false, nil
	}
	return connector.RemoteState{ID: name, Name: name, Status: connector.RemoteHealthy}, true, nil
}

func (w webhook) RotateSecret(_ context.Context, c connector.RemoteCall, id string) (string, error) {
	// The secret is derived, so "rotating" answers the one in use.
	return secretFor(c, id)
}

func (w webhook) Delete(ctx context.Context, c connector.RemoteCall, id string) error {
	info, err := w.info(ctx, c)
	if err != nil {
		return err
	}
	if !ours(info, id) {
		return nil // already gone, or another webhook: not Taskiem's to remove
	}
	return w.c.call(ctx, c.Request(), "deleteWebhook", map[string]any{}, nil)
}

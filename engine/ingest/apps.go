package ingest

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// App is Taskiem's registration with a provider whose app sends every
// business's events to one URL (decision 0051; connector/v1 rule 18): the
// secret it signs deliveries with, and the token it sends to check the
// endpoint (Meta's hub.verify_token). Process configuration
// (TASKIEM_OAUTH_<CLIENT>_WEBHOOK_SECRET, _VERIFY_TOKEN), never a tenant's.
type App struct {
	WebhookSecret string
	VerifyToken   string
}

// appMaxBody bounds an app delivery before any tenant is known.
const appMaxBody = 1 << 20

// appRate bounds each app's deliveries before they are routed; each
// tenant they reach then applies its own limits.
const appRate, appBurst = 200, 400

type appGate struct {
	mu sync.Mutex
	by map[string]*rate.Limiter
}

func (g *appGate) allow(client string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.by == nil {
		g.by = map[string]*rate.Limiter{}
	}
	l, ok := g.by[client]
	if !ok {
		l = rate.NewLimiter(appRate, appBurst)
		g.by[client] = l
	}
	return l.Allow()
}

// appTrigger finds an app-delivered trigger and its app's registration.
func (h *Handler) appTrigger(r *http.Request) (*connector.Connector, connector.TriggerSpec, App, bool) {
	ref, name := chi.URLParam(r, "connector"), chi.URLParam(r, "trigger")
	conn, ok := h.Registry.Get(ref)
	if !ok {
		return nil, connector.TriggerSpec{}, App{}, false
	}
	spec, ok := conn.Manifest.Triggers[name]
	if !ok || !spec.AppDelivered() {
		return nil, connector.TriggerSpec{}, App{}, false
	}
	app, ok := h.Apps[conn.Manifest.ClientName()]
	return conn, spec, app, ok && app.WebhookSecret != ""
}

// appEvent takes a delivery to Taskiem's platform app for a provider
// (POST /hooks/apps/{connector}/{trigger}): verified with the app's
// secret, each event goes to every active connection that holds the
// account it names (taskiem_app_route_event, the one step that crosses
// tenants), as if it had arrived at that tenant's own URL: the same
// signal, dedup key, subscribers, limits and suspension. An event for an
// account no connection holds is acknowledged and counted, never stored.
// An event the trigger's revokes marks expires those connections first.
func (h *Handler) appEvent(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()
	conn, spec, app, ok := h.appTrigger(r)
	if !ok {
		replyErr(w, http.StatusNotFound, "no such app trigger")
		return
	}
	ref, name := conn.Ref(), chi.URLParam(r, "trigger")
	if !h.apps.allow(conn.Manifest.ClientName()) {
		w.Header().Set("Retry-After", "1")
		replyErr(w, http.StatusTooManyRequests, "ingest rate exceeded")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, appMaxBody))
	if err != nil {
		replyErr(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	verify := func() error { return connector.VerifyWebhook(spec.Verify, app.WebhookSecret, r.Header, body) }
	if spec.Verify.Scheme == "connector" {
		verify = func() error {
			v := conn.Verifiers[name]
			if v == nil {
				return connector.ErrBadSignature
			}
			return v(app.WebhookSecret, r.Header, body)
		}
	}
	if err := verify(); err != nil {
		replyErr(w, http.StatusUnauthorized, "signature invalid")
		return
	}
	p := parse(r, body, strings.ToLower(spec.Verify.Header))
	act := p.activation()
	if hs := spec.Handshake; hs != nil && hs.Method == "POST" {
		v, err := h.exprs.Eval(hs.When, act)
		if b, _ := v.(bool); err == nil && b {
			answer, err := evalString(h.exprs, hs.Respond, act)
			if err != nil {
				replyErr(w, http.StatusBadRequest, "unexpected handshake for "+ref+" "+name)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(answer))
			return
		}
	}
	test := false
	if spec.TestEvent != "" {
		v, err := h.exprs.Eval(spec.TestEvent, act)
		b, _ := v.(bool)
		test = b && err == nil
	}
	items, err := h.itemsOf(spec, name, act, body)
	if err != nil {
		replyErr(w, http.StatusBadRequest, "unexpected payload for "+ref+" "+name)
		return
	}
	results := []map[string]any{}
	for _, it := range items {
		if it.ignored {
			results = append(results, map[string]any{"ignored": it.event})
			continue
		}
		account, err := evalString(h.exprs, spec.AccountKey, it.act)
		if err != nil {
			account = ""
		}
		targets, err := h.appTargets(ctx, conn.Manifest.ID, account)
		if err != nil {
			h.unavailable(w, r, err)
			return
		}
		if len(targets) == 0 {
			if _, err := h.Store.Pool.Exec(ctx, `SELECT taskiem_app_event_dropped($1, $2)`, ref, name); err != nil {
				h.Logger.Warn("ingest: counting an app event for no connection", "connector", ref, "err", err)
			}
			results = append(results, map[string]any{"event": it.event, "routed": 0})
			continue
		}
		if spec.Revokes != "" {
			if v, err := h.exprs.Eval(spec.Revokes, it.act); err == nil && v == true {
				for _, t := range targets {
					if err := h.expireConnection(ctx, t, conn.Manifest.ID); err != nil {
						h.unavailable(w, r, err)
						return
					}
				}
			}
		}
		n := 0
		for _, t := range targets {
			throttled, exists, ok := h.admit(ctx, w, r, t.tenant)
			if !ok {
				return
			}
			if !exists {
				continue
			}
			var subs []subscriber
			if err := db.InTenantTx(ctx, h.Store.Pool, []uuid.UUID{t.tenant}, func(tx pgx.Tx) error {
				var err error
				subs, err = loadSubscribers(ctx, tx, t.env, ref, name, uuid.Nil)
				return err
			}); err != nil {
				h.unavailable(w, r, err)
				return
			}
			ev := eventItem{conn: conn, ref: ref, name: name, env: t.env, event: it.event, dedup: it.dedup, correlation: it.correlation,
				body: p.body, item: it.item, split: spec.Split != "", test: test}
			if _, ok := h.deliverInto(ctx, w, r, target{tenant: t.tenant, connection: t.connection, throttled: throttled, subs: subs}, ev); !ok {
				return
			}
			n++
		}
		results = append(results, map[string]any{"event": it.event, "routed": n})
	}
	if a := spec.Ack; a != nil {
		status := a.Status
		if status == 0 {
			status = http.StatusOK
		}
		ct := a.ContentType
		if ct == "" {
			ct = "text/plain; charset=utf-8"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(a.Body))
		return
	}
	// 200, not 202: an app's provider (Slack within 3 s) wants plain success.
	if spec.Split != "" {
		reply(w, http.StatusOK, map[string]any{"events": results})
		return
	}
	reply(w, http.StatusOK, results[0])
}

// appHandshake answers a provider's GET endpoint check for its app
// (Meta's hub.challenge): the token it sends must equal the app's.
func (h *Handler) appHandshake(w http.ResponseWriter, r *http.Request) {
	conn, spec, app, ok := h.appTrigger(r)
	hs := spec.Handshake
	if !ok || hs == nil || hs.Method != "GET" {
		replyErr(w, http.StatusNotFound, "no such app trigger")
		return
	}
	got := r.URL.Query().Get(hs.TokenQuery)
	if app.VerifyToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(app.VerifyToken)) != 1 {
		replyErr(w, http.StatusForbidden, "verification token does not match")
		return
	}
	answer, err := evalString(h.exprs, hs.Respond, parse(r, nil).activation())
	if err != nil {
		replyErr(w, http.StatusBadRequest, "unexpected handshake for "+conn.Ref())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(answer))
}

// appTarget is a connection holding the account an app event is for.
type appTarget struct {
	tenant     uuid.UUID
	env        string
	connection string
}

func (h *Handler) appTargets(ctx context.Context, connectorID, account string) ([]appTarget, error) {
	if account == "" {
		return nil, nil
	}
	rows, err := h.Store.Pool.Query(ctx, `SELECT tenant_id, environment, connection FROM taskiem_app_route_event($1, $2)`, connectorID, account)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (appTarget, error) {
		var t appTarget
		return t, row.Scan(&t.tenant, &t.env, &t.connection)
	})
}

// expireConnection marks a connection expired when the business took the
// app away (connector/v1 rule 18, revokes): runs stop using it and the
// Connections page says to connect again.
func (h *Handler) expireConnection(ctx context.Context, t appTarget, connectorID string) error {
	ex, ok := h.Connections.(interface {
		ExpireConnection(ctx context.Context, tenant uuid.UUID, env, connector, name string) error
	})
	if !ok {
		return nil
	}
	err := ex.ExpireConnection(ctx, t.tenant, t.env, connectorID, t.connection)
	if errors.Is(err, secrets.ErrNotFound) {
		return nil
	}
	return err
}

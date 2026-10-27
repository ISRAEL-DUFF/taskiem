package ingest

import (
	"context"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
)

// Platform is Taskiem's own client at a provider (decision 0052): the
// secret each mode's subscription signs its deliveries with. Process
// configuration (TASKIEM_ISWALLET_WEBHOOK_SECRET, _TEST_WEBHOOK_SECRET),
// never a tenant's.
type Platform struct {
	WebhookSecrets map[string]string // by mode: live, test
}

// platformEvent takes a delivery to the subscription Taskiem's own client
// holds at a provider (POST /hooks/platform/{connector}?mode=live): every
// business's wallet events, under the one client. It is verified with the
// mode's secret; each event goes to the trigger that sends its type, and
// is delivered into every connection Taskiem made under its own client in
// that mode that holds the account the event names (the trigger's route
// key: iswallet's wallet_id), as if it had come to that tenant's own URL.
// An event for an account no such connection holds is acknowledged (the
// provider would retry it for days), counted and logged for the operators,
// never stored.
func (h *Handler) platformEvent(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()
	ref := chi.URLParam(r, "connector")
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "live"
	}
	conn, ok := h.Registry.Get(ref)
	secret := ""
	if ok {
		secret = h.Platform[conn.Manifest.ID].WebhookSecrets[mode]
	}
	// The triggers whose events name an account (route), in a fixed order.
	var names []string
	if ok {
		for _, n := range slices.Sorted(maps.Keys(conn.Manifest.Triggers)) {
			if t := conn.Manifest.Triggers[n]; t.Route != nil && t.Verify != nil {
				names = append(names, n)
			}
		}
	}
	if secret == "" || len(names) == 0 {
		replyErr(w, http.StatusNotFound, "no such platform client")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, appMaxBody))
	if err != nil {
		replyErr(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	// The trigger that sends this event; the first when none does (it is
	// then acknowledged as ignored, once verified).
	name, spec := names[0], conn.Manifest.Triggers[names[0]]
	var items []deliveryItem
	for _, n := range names {
		s := conn.Manifest.Triggers[n]
		p := parse(r, body, strings.ToLower(s.Verify.Header))
		its, err := h.itemsOf(s, n, p.activation(), body)
		if err != nil || !slices.ContainsFunc(its, func(it deliveryItem) bool { return !it.ignored }) {
			continue
		}
		name, spec, items = n, s, its
		break
	}
	if err := connector.VerifyWebhook(spec.Verify, secret, r.Header, body); err != nil {
		replyErr(w, http.StatusUnauthorized, "signature invalid")
		return
	}
	// Counted once verified: unsigned requests cannot spend the rate the
	// provider's own deliveries need.
	if !h.apps.allow("platform:" + ref) {
		w.Header().Set("Retry-After", "1")
		replyErr(w, http.StatusTooManyRequests, "ingest rate exceeded")
		return
	}
	p := parse(r, body, strings.ToLower(spec.Verify.Header))
	if items == nil {
		if items, err = h.itemsOf(spec, name, p.activation(), body); err != nil {
			replyErr(w, http.StatusBadRequest, "unexpected payload for "+ref)
			return
		}
	}
	results := []map[string]any{}
	for _, it := range items {
		if it.ignored {
			results = append(results, map[string]any{"ignored": it.event})
			continue
		}
		account, err := evalString(h.exprs, spec.Route.Key, it.act)
		if err != nil {
			account = ""
		}
		targets, err := h.platformTargets(ctx, conn.Manifest.ID, mode, account)
		if err != nil {
			h.unavailable(w, r, err)
			return
		}
		if len(targets) == 0 {
			if _, err := h.Store.Pool.Exec(ctx, `SELECT taskiem_app_event_dropped($1, $2)`, ref, name); err != nil {
				h.Logger.Warn("ingest: counting a platform event for no connection", "connector", ref, "err", err)
			}
			h.Logger.Error("ingest: a platform client's event names an account no business holds", "connector", ref, "mode", mode,
				"trigger", name, "event", it.event, "account", account, "dedup", it.dedup)
			results = append(results, map[string]any{"event": it.event, "routed": 0})
			continue
		}
		ev := eventItem{conn: conn, ref: ref, name: name, event: it.event, dedup: it.dedup, correlation: it.correlation,
			body: p.body, item: it.item, split: spec.Split != ""}
		n, ok := h.deliverToConnections(ctx, w, r, targets, ev)
		if !ok {
			return
		}
		results = append(results, map[string]any{"event": it.event, "routed": n})
	}
	if spec.Split != "" {
		reply(w, http.StatusOK, map[string]any{"events": results})
		return
	}
	reply(w, http.StatusOK, results[0])
}

// platformTargets are the connections Taskiem made under its own client
// in a mode that hold an account.
func (h *Handler) platformTargets(ctx context.Context, connectorID, mode, account string) ([]appTarget, error) {
	if account == "" {
		return nil, nil
	}
	rows, err := h.Store.Pool.Query(ctx, `SELECT tenant_id, environment, connection FROM taskiem_platform_route_event($1, $2, $3)`, connectorID, mode, account)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (appTarget, error) {
		var t appTarget
		return t, row.Scan(&t.tenant, &t.env, &t.connection)
	})
}

// deliverToConnections delivers one event into each connection that holds
// the account it names, in whichever tenant, as a delivery to that
// tenant's own URL would be: its limits, its suspension, its subscribed
// workflows and its money log. It returns how many it reached; on failure
// it has answered the delivery.
func (h *Handler) deliverToConnections(ctx context.Context, w http.ResponseWriter, r *http.Request, targets []appTarget, ev eventItem) (int, bool) {
	n := 0
	for _, t := range targets {
		throttled, exists, ok := h.admit(ctx, w, r, t.tenant)
		if !ok {
			return n, false
		}
		if !exists {
			continue
		}
		var subs []subscriber
		if err := db.InTenantTx(ctx, h.Store.Pool, []uuid.UUID{t.tenant}, func(tx pgx.Tx) error {
			var err error
			subs, err = loadSubscribers(ctx, tx, t.env, ev.ref, ev.name, uuid.Nil)
			return err
		}); err != nil {
			h.unavailable(w, r, err)
			return n, false
		}
		ev.env = t.env
		if _, ok := h.deliverInto(ctx, w, r, target{tenant: t.tenant, connection: t.connection, throttled: throttled, subs: subs}, ev); !ok {
			return n, false
		}
		n++
	}
	return n, true
}

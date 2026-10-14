package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// subscriber is a workflow subscribed to a connector trigger.
type subscriber struct {
	workflow   uuid.UUID
	version    int
	events     []string
	connection *string
	options    map[string]any
}

// loadSubscribers reads the workflows subscribed to a connector trigger in
// the tenant tx is scoped to; owner, when set, keeps only that workflow (a
// remotely registered trigger).
func loadSubscribers(ctx context.Context, tx pgx.Tx, env, ref, name string, owner uuid.UUID) ([]subscriber, error) {
	rows, err := tx.Query(ctx, `SELECT workflow_id, version, COALESCE(events, '{}'), connection, options FROM triggers
		WHERE type = 'connector_event' AND environment = $1 AND connector = $2 AND trigger_name = $3 AND ($4::uuid IS NULL OR workflow_id = $4)`,
		env, ref, name, nullUUID(owner))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var subs []subscriber
	for rows.Next() {
		var s subscriber
		var opts []byte
		if err := rows.Scan(&s.workflow, &s.version, &s.events, &s.connection, &opts); err != nil {
			return nil, err
		}
		if len(opts) > 0 {
			_ = json.Unmarshal(opts, &s.options)
		}
		subs = append(subs, s)
	}
	return subs, rows.Err()
}

// target is a tenant an event is delivered into: the one whose URL it
// came to, or a sub-tenant it was routed to.
type target struct {
	tenant     uuid.UUID
	connection string // the connection subscribers' filters compare with
	throttled  bool
	subs       []subscriber
}

// eventItem is one verified event of a delivery.
type eventItem struct {
	conn                      *connector.Connector
	ref, name, env            string
	event, dedup, correlation string
	body, item                any
	split, test               bool
}

// deliverInto delivers an event into one tenant: the signal to runs
// waiting on its correlation, then a run of every subscribed workflow, each
// deduplicated on the event's dedup key. On failure it has answered the
// delivery (503 so the provider retries, 429, 423) and returns false.
func (h *Handler) deliverInto(ctx context.Context, w http.ResponseWriter, r *http.Request, t target, ev eventItem) (map[string]any, bool) {
	out := map[string]any{"event": ev.event}
	payload := map[string]any{"event": ev.event, "body": ev.body}
	trig := map[string]any{"type": "connector_event", "connector": ev.ref, "trigger": ev.name, "event": ev.event, "body": ev.body}
	if ev.split {
		payload["item"], trig["item"] = ev.item, ev.item
	}
	if ev.test {
		out["test"], trig["test"] = true, true
	}
	if ev.correlation != "" {
		woke, fresh, err := h.Store.DeliverSignalOnce(ctx, t.tenant, ev.env, ev.ref+":"+ev.name, ev.correlation, ev.dedup, payload)
		if err != nil {
			h.unavailable(w, r, err)
			return nil, false
		}
		out["signalled"], out["duplicate"] = len(woke), !fresh
	}
	runs := []uuid.UUID{}
	for _, s := range t.subs {
		if (len(s.events) > 0 || ev.test) && !slices.Contains(s.events, ev.event) {
			continue
		}
		if s.connection != nil && *s.connection != t.connection {
			continue
		}
		runTrig := trig
		if enrich := ev.conn.Enrichers[ev.name]; enrich != nil {
			body, err := h.enrich(ctx, enrich, t.tenant, ev.env, ev.conn, t.connection, s.options, ev.body)
			if err != nil {
				h.unavailable(w, r, fmt.Errorf("completing the event: %w", err))
				return nil, false
			}
			runTrig = maps.Clone(trig)
			runTrig["body"] = body
		}
		// Signals above were delivered whatever the tenant's limits; a
		// refused start fails the delivery so the provider retries, and
		// the retry's signal is recognised as a duplicate.
		run, _, err := h.Store.StartRun(ctx, runtime.StartRequest{
			TenantID: t.tenant, WorkflowID: s.workflow, Version: s.version, Environment: ev.env,
			Trigger: runTrig, StartedBy: "connector:" + ev.ref, TriggerID: "event/" + s.workflow.String() + "/" + ev.env, DedupKey: ev.dedup,
			Throttled: t.throttled,
		})
		if le, ok := runtime.IsLimit(err); ok {
			limited(w, le)
			return nil, false
		}
		if errors.Is(err, runtime.ErrTenantSuspended) {
			h.suspended(w, r, t.tenant)
			return nil, false
		}
		if err != nil {
			h.unavailable(w, r, err)
			return nil, false
		}
		runs = append(runs, run.ID)
	}
	out["runs"] = runs
	return out, true
}

// routeEvent delivers an event a partner received into each of its
// sub-tenants whose connection lists the event's key (decision 0031), as
// if it had arrived at that sub-tenant's own URL: same signal, same dedup
// key, the sub-tenant's own subscribers, limits and suspension. Which
// sub-tenants hold the key, the record of where the event went and its
// audit in both chains come from taskiem_partner_route_event, the only
// step that crosses tenants; it routes nothing for a tenant that is not a
// partner. An event that matches no sub-tenant is recorded for the partner
// and alerted once a day per key. It returns how many sub-tenants the
// event reached; false when it has answered the delivery with an error.
func (h *Handler) routeEvent(ctx context.Context, w http.ResponseWriter, r *http.Request, partner uuid.UUID, field, key string, ev eventItem) (int, bool) {
	type routed struct {
		sub        *uuid.UUID
		connection *string
		fresh      bool
	}
	var targets []routed
	err := db.InTenantTx(ctx, h.Store.Pool, []uuid.UUID{partner}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT sub_tenant, connection, fresh FROM taskiem_partner_route_event($1, $2, $3, $4, $5, $6, $7)`,
			ev.env, ev.ref, ev.name, field, key, ev.event, ev.dedup)
		if err != nil {
			return err
		}
		targets, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (routed, error) {
			var t routed
			return t, row.Scan(&t.sub, &t.connection, &t.fresh)
		})
		if err != nil {
			return err
		}
		for _, t := range targets {
			if t.sub != nil || !t.fresh {
				continue
			}
			day := time.Now().UTC().Format(time.DateOnly)
			_, err := alerts.Notify(ctx, tx, partner, alerts.Alert{
				Kind:  alerts.EventUnrouted,
				Dedup: ev.ref + ":" + ev.name + ":" + ev.env + ":" + key + ":" + day,
				Title: fmt.Sprintf("A %s event matched none of your sub-tenants", ev.ref),
				Body: fmt.Sprintf("A verified %s %s event (%s) in %s names %s %q, which no sub-tenant's %s connection lists, nor one of your own. "+
					"It was delivered to your organisation only. If it belongs to a customer, add the value to that sub-tenant's connection "+
					"(PUT /v1/partner/sub-tenants/{sub}/connections/{id}/credentials); GET /v1/partner/event-routes?status=unrouted lists such events. "+
					"Alerted once a day per value.",
					ev.ref, ev.name, ev.event, ev.env, field, key, ev.conn.Manifest.ID),
				Detail: map[string]any{"connector": ev.ref, "trigger": ev.name, "event": ev.event, "environment": ev.env, "field": field, "route_key": key, "dedup": ev.dedup},
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		h.unavailable(w, r, fmt.Errorf("routing to sub-tenants: %w", err))
		return 0, false
	}
	n := 0
	for _, t := range targets {
		if t.sub == nil {
			continue
		}
		// The sub-tenant's own gate, as at its own URL.
		g, err := h.gate(ctx, *t.sub)
		switch {
		case errors.Is(err, errBusy):
			w.Header().Set("Retry-After", "1")
			replyErr(w, http.StatusTooManyRequests, "ingest rate exceeded")
			return 0, false
		case err != nil:
			h.unavailable(w, r, err)
			return 0, false
		case g.hard == nil:
			continue // deleted since it was routed
		case g.suspended:
			h.suspended(w, r, *t.sub)
			return 0, false
		}
		if !g.hard.Allow() {
			h.Store.LimitHit(ctx, *t.sub, "ingest_ceiling")
			w.Header().Set("Retry-After", "1")
			reply(w, http.StatusTooManyRequests, map[string]string{"error": "ingest rate exceeded", "code": "rate_limited"})
			return 0, false
		}
		var subs []subscriber
		if err := db.InTenantTx(ctx, h.Store.Pool, []uuid.UUID{*t.sub}, func(tx pgx.Tx) error {
			var err error
			subs, err = loadSubscribers(ctx, tx, ev.env, ev.ref, ev.name, uuid.Nil)
			return err
		}); err != nil {
			h.unavailable(w, r, err)
			return 0, false
		}
		conn := ""
		if t.connection != nil {
			conn = *t.connection
		}
		if _, ok := h.deliverInto(ctx, w, r, target{tenant: *t.sub, connection: conn, throttled: !g.soft.Allow(), subs: subs}, ev); !ok {
			return 0, false
		}
		n++
	}
	return n, true
}

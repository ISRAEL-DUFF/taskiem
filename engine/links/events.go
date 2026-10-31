package links

// What happens between linked businesses, for workflows (L6;
// docs/spec/business-links.md §11; migration 00282): the "link" trigger.
// An event is queued in the business it happens to, inside the
// transaction that makes it happen, once per workflow that starts on it;
// the link worker starts the runs. A body carries the link, the business's
// name, the document's reference and the amounts, never the other side's
// contact details.

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TriggerType is the wd/v1 trigger that starts a workflow on a link event:
// {"type": "link", "config": {"event": "order.received"}}.
const TriggerType = "link"

// The link trigger's events.
const (
	EventBillReceived   = "bill.received"   // a linked supplier's invoice became our bill
	EventBillPaid       = "bill.paid"       // one of our bills is paid in full
	EventOrderReceived  = "order.received"  // a linked buyer's order reached us
	EventOrderDelivered = "order.delivered" // a linked supplier delivered our order
	EventThreadMessage  = "thread.message"  // a linked business wrote to us
	EventLinkAccepted   = "link.accepted"   // a link became active
)

// Events are the link trigger's events.
var Events = []string{EventBillReceived, EventBillPaid, EventOrderReceived, EventOrderDelivered, EventThreadMessage, EventLinkAccepted}

// QueueEventTx queues an event in the business in scope (tenant): a run to
// start for each of its prod workflows whose trigger is this link event,
// once per key whatever happens after.
func QueueEventTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, event, key string, body map[string]any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `INSERT INTO link_starts (id, tenant_id, workflow_id, version, event, dedup_key, body)
		SELECT gen_random_uuid(), $1, t.workflow_id, t.version, $2, $3, $4 FROM triggers t
		 WHERE t.tenant_id = $1 AND t.type = 'link' AND t.environment = 'prod' AND $2 = ANY (t.events)
		ON CONFLICT (workflow_id, dedup_key) DO NOTHING
		RETURNING id`, tenant, event, event+":"+key, raw)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := dueTx(ctx, tx, tenant, "start", id, 0); err != nil {
			return err
		}
	}
	return nil
}

// Start is a queued run to start.
type Start struct {
	ID       uuid.UUID
	Tenant   uuid.UUID
	Workflow uuid.UUID
	Version  int
	Event    string
	Key      string
	Body     map[string]any
}

// StartTx is a queued start still to make, in the business in scope.
func StartTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Start, bool, error) {
	s := Start{ID: id, Tenant: tenant}
	var raw []byte
	var done bool
	err := tx.QueryRow(ctx, `SELECT workflow_id, version, event, dedup_key, body, done_at IS NOT NULL FROM link_starts WHERE tenant_id = $1 AND id = $2`,
		tenant, id).Scan(&s.Workflow, &s.Version, &s.Event, &s.Key, &raw, &done)
	if errors.Is(err, pgx.ErrNoRows) || done {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	if err := json.Unmarshal(raw, &s.Body); err != nil {
		return s, false, err
	}
	return s, true, nil
}

// StartDoneTx records what came of a start: its run, or why none.
func StartDoneTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, run *uuid.UUID, note string) error {
	if len(note) > 300 {
		note = note[:300]
	}
	if _, err := tx.Exec(ctx, `UPDATE link_starts SET run_id = $3, note = NULLIF($4, ''), done_at = now() WHERE tenant_id = $1 AND id = $2`,
		tenant, id, run, note); err != nil {
		return err
	}
	return Undue(ctx, tx, tenant, "start", id)
}

// Trigger is a link event as trigger.body sees it, with its event.
func (s Start) Trigger() map[string]any {
	body := map[string]any{}
	for k, v := range s.Body {
		body[k] = v
	}
	body["event"] = s.Event
	return map[string]any{"type": TriggerType, "event": s.Event, "body": body}
}

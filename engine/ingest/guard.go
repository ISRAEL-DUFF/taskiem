package ingest

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/assistant"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runfiles"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// The customer guard at the door (decision 0062 V6, docs/assistant-guard.md):
// for a workflow whose chat trigger has a guard, each customer's message
// is checked before a run can start or wait in a queue. Only new messages
// in private chats start one; Taskiem's switch, the business's beta flag,
// STOP, the per-customer limits, the owner's pause, a hand-over to a
// person, the kinds of message and the workflow's queue decide whether it
// does and what it is asked to do (trigger.guard). A voice note is
// fetched here, while the provider's link still works.

// fetchTimeout bounds fetching a customer's voice note, inside the
// delivery's budget.
const fetchTimeout = 3 * time.Second

// guarded is what the guard decided for one subscriber.
type guarded struct {
	start    bool
	trigger  map[string]any // trigger.guard
	customer *runtime.Customer
	pii      []pii.Path
	dropped  string
	outcome  string
}

// guardEvent runs the guard for a subscriber with one.
func (h *Handler) guardEvent(ctx context.Context, t target, ev eventItem, s subscriber) (guarded, error) {
	if ev.conv == nil || ev.test {
		return guarded{dropped: assistant.DropNotCustomer}, nil
	}
	vault := h.Vault
	if vault == nil {
		vault, _ = h.Secrets.(assistant.Sealer)
	}
	if vault == nil {
		return guarded{}, errors.New("no vault: a customer guard cannot key its customers")
	}
	var d assistant.Decision
	var msg assistant.Message
	err := db.InTenantTx(ctx, h.Store.Pool, []uuid.UUID{t.tenant}, func(tx pgx.Tx) error {
		// A customer is keyed on the connection the delivery is for, by
		// name, and its environment: not on how the webhook address was
		// written (with or without ?connection=).
		name, err := connectionName(ctx, tx, t.tenant, ev.env, ev.conn.Manifest.ID, t.connection)
		if err != nil {
			return err
		}
		msg = assistant.Message{Channel: ev.ref + "/" + name + "/" + ev.env, Dedup: ev.dedup, Sender: ev.conv.sender, SenderCategory: ev.conv.category,
			Private: ev.conv.private, Kind: ev.conv.kind, Text: ev.conv.text}
		if d, err = h.Guard.Decide(ctx, tx, vault, t.tenant, *s.guard, msg); err != nil {
			return err
		}
		// The workflow's queue holds back what it would answer, never a
		// fixed answer the guard owes the customer (a STOP's, a hand-over's).
		if d.Start && d.Outcome != assistant.OutcomeReply {
			full, err := assistant.QueueFull(ctx, tx, t.tenant, s.workflow, *s.guard, time.Now())
			if err != nil {
				return err
			}
			if full {
				d = assistant.Decision{Dropped: assistant.DropBusy}
			}
		}
		return nil
	})
	if err != nil {
		return guarded{}, err
	}
	if !d.Start {
		return guarded{dropped: d.Dropped}, nil
	}
	g := d.Map(msg, ev.conv.kind)
	if d.Outcome == assistant.OutcomeAnswer && ev.conv.media != "" {
		file := d.File
		if file == nil {
			var ferr error
			if file, ferr = h.fetchMedia(ctx, t, ev, s); ferr != nil {
				h.Logger.Warn("guard: a voice note could not be fetched before the run", "tenant", t.tenant, "workflow", s.workflow, "err", ferr)
			} else {
				// Kept with the decision: a retry of this delivery uses it,
				// and does not fetch the note again.
				d.File = file
				_ = db.InTenantTx(context.WithoutCancel(ctx), h.Store.Pool, []uuid.UUID{t.tenant}, func(tx pgx.Tx) error {
					return assistant.Keep(ctx, tx, t.tenant, msg.Channel, msg.Dedup, d)
				})
			}
		}
		if file != nil {
			g["file"] = file
		} else {
			g["file_error"] = "the voice note could not be fetched when it arrived: fetch it in the run"
		}
	}
	cat := ev.conv.category
	if cat == "" {
		cat = "other"
	}
	return guarded{start: true, trigger: g, outcome: d.Outcome,
		customer: &runtime.Customer{Category: cat, Value: ev.conv.sender},
		pii: []pii.Path{{Segments: []string{"guard", "sender"}, Category: cat}, {Segments: []string{"guard", "text"}, Category: "other"},
			{Segments: []string{"guard", "file", "name"}, Category: "other"}}}, nil
}

// fetchMedia downloads a customer's voice note or audio with the trigger's
// fetch action (get_media, get_file) into a run file tied to the customer,
// before the run can wait in a queue (C21).
func (h *Handler) fetchMedia(ctx context.Context, t target, ev eventItem, s subscriber) (any, error) {
	spec := ev.conn.Manifest.Triggers[ev.name].Conversation
	act, ok := ev.conn.Actions[spec.Fetch]
	if !ok || spec.FetchInput == "" {
		return nil, errors.New("the trigger names no fetch action")
	}
	if h.RunFiles == nil || h.Egress == nil {
		return nil, errors.New("no run file store or egress guard on this edge")
	}
	// Within what is left of the delivery's time, keeping a second for the
	// run to start.
	budget := fetchTimeout
	if dl, ok := ctx.Deadline(); ok {
		budget = min(budget, time.Until(dl)-time.Second)
	}
	if budget < 500*time.Millisecond {
		return nil, errors.New("no time left in the delivery to fetch the voice note")
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: secrets.PurposeIngestEnrich})
	creds, err := h.Connections.Credentials(use, t.tenant, ev.env, ev.conn.Manifest.ID, t.connection)
	if err != nil {
		return nil, err
	}
	pol := egress.Policy{Tenant: t.tenant.String(), Hosts: ev.conn.Manifest.HostsFor(creds), Purpose: "connector:" + ev.conn.Manifest.ID + ":ingest"}
	wf := s.workflow
	resp, err := act.Execute(ctx, connector.Request{
		Input:       map[string]any{spec.FetchInput: ev.conv.media, "sender": ev.conv.sender},
		Credentials: creds, HTTP: h.Egress.Client(pol, fetchTimeout), Logger: h.Logger, Attempt: 1,
		Files: h.RunFiles.For(runfiles.Owner{Tenant: t.tenant, Environment: ev.env, Workflow: &wf}),
	})
	if err != nil {
		return nil, err
	}
	out, _ := resp.Output.(map[string]any)
	file, ok := out["file"]
	if !ok {
		return nil, errors.New("the fetch action answered no file")
	}
	return file, nil
}

// guardSummary is what a delivery's answer says of the guard, without
// anything personal.
func guardSummary(g guarded) map[string]any {
	if g.start {
		return map[string]any{"outcome": g.outcome}
	}
	return map[string]any{"dropped": g.dropped}
}

// connectionName is the connection a delivery is for: the one it names, or
// the business's only active connection to the connector in the
// environment.
func connectionName(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, env, connectorID, named string) (string, error) {
	if named != "" {
		return named, nil
	}
	rows, err := tx.Query(ctx, `SELECT name FROM connections WHERE tenant_id = $1 AND environment = $2 AND connector = $3 AND status = 'active' LIMIT 2`, tenant, env, connectorID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return "", err
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(names) == 1 {
		return names[0], nil
	}
	return "", nil
}

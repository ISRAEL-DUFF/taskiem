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
	msg := assistant.Message{Channel: ev.ref + "/" + t.connection, Sender: ev.conv.sender, SenderCategory: ev.conv.category,
		Private: ev.conv.private, Kind: ev.conv.kind, Text: ev.conv.text}
	var d assistant.Decision
	err := db.InTenantTx(ctx, h.Store.Pool, []uuid.UUID{t.tenant}, func(tx pgx.Tx) error {
		var err error
		d, err = h.Guard.Decide(ctx, tx, vault, t.tenant, s.workflow, *s.guard, msg)
		return err
	})
	if err != nil {
		return guarded{}, err
	}
	if !d.Start {
		return guarded{dropped: d.Dropped}, nil
	}
	g := d.Map(msg, ev.conv.kind)
	if d.Outcome == assistant.OutcomeAnswer && ev.conv.media != "" {
		if file, err := h.fetchMedia(ctx, t, ev, s); err != nil {
			h.Logger.Warn("guard: a voice note could not be fetched before the run", "tenant", t.tenant, "workflow", s.workflow, "err", err)
			g["file_error"] = "the voice note could not be fetched when it arrived: fetch it in the run"
		} else {
			g["file"] = file
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
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
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

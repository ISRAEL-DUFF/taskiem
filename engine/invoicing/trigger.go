package invoicing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// TriggerType is the wd/v1 trigger that starts a workflow on an invoice
// of the business: {"type": "invoice", "config": {"event": "paid"}}
// (docs/contracts/wd-v1.md, docs/invoices.md).
const TriggerType = "invoice"

// TriggerEvents are the events an invoice trigger starts on: paid (in
// full, once per invoice) and payment (a part counted, once per part;
// trigger_payment.go).
var TriggerEvents = []string{"paid", "payment"}

// noLongerPaid is the note on a start not made because the payment was
// undone before the run could start; paying the invoice again reopens it.
const noLongerPaid = "Not started: the invoice was no longer paid."

// maxStartAttempts is how often a start that fails for another reason is
// tried (a minute apart) before it is given up, with the reason.
const maxStartAttempts = 30

// queuePaidStarts records, in the transaction of the payment that made
// the invoice paid, a run to start for each prod workflow of the tenant
// whose trigger is an invoice being paid: once per invoice and workflow,
// whatever happens after (a replay, a restart, the invoice paid again
// after an undo). A start skipped because the invoice was no longer paid
// is the only one taken up again.
func queuePaidStarts(ctx context.Context, tx pgx.Tx, tenant, invoice uuid.UUID) error {
	_, err := tx.Exec(ctx, `INSERT INTO invoice_paid_starts (invoice_id, workflow_id, tenant_id, version)
		SELECT $2, t.workflow_id, $1, t.version FROM triggers t
		 WHERE t.tenant_id = $1 AND t.type = 'invoice' AND t.environment = 'prod' AND 'paid' = ANY (t.events)
		ON CONFLICT (invoice_id, workflow_id) DO UPDATE SET done_at = NULL, note = NULL, claimed_until = NULL, attempts = 0, version = EXCLUDED.version
		 WHERE invoice_paid_starts.run_id IS NULL AND invoice_paid_starts.note = $3`, tenant, invoice, noLongerPaid)
	return err
}

// StartPaid starts the runs that invoices being paid queued: each
// workflow's run once, with the invoice as trigger.body. Run it every
// second. A start the tenant's plan or state refuses is not tried again;
// the row keeps why.
func (s *Invoices) StartPaid(ctx context.Context) error {
	if s.Start == nil {
		return nil
	}
	type claim struct {
		tenant, invoice, workflow uuid.UUID
		version, attempts         int
	}
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id, invoice_id, workflow_id, version, attempts FROM taskiem_invoice_paid_starts_claim(20)`)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (claim, error) {
		var c claim
		err := r.Scan(&c.tenant, &c.invoice, &c.workflow, &c.version, &c.attempts)
		return c, err
	})
	if err != nil {
		return err
	}
	for _, c := range list {
		run, note, err := s.startPaid(ctx, c.tenant, c.invoice, c.workflow, c.version)
		if err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // stopping; the claim runs out and the start is taken again
			}
			s.log().Warn("invoice paid: start", "tenant", c.tenant, "invoice", c.invoice, "workflow", c.workflow, "attempt", c.attempts, "err", err)
			if c.attempts < maxStartAttempts {
				continue // taken again once the claim runs out
			}
			note = "Not started: " + err.Error()
		}
		var runID *uuid.UUID
		if run != uuid.Nil {
			runID = &run
		}
		if _, err := s.Pool.Exec(ctx, `SELECT taskiem_invoice_paid_starts_done($1, $2, $3, NULLIF($4, ''))`, c.invoice, c.workflow, runID, note); err != nil {
			return err
		}
	}
	return nil
}

// startPaid starts one workflow's run for a paid invoice: the run, or
// why none was started (a note), or an error to try again.
func (s *Invoices) startPaid(ctx context.Context, tenant, invoice, workflow uuid.UUID, version int) (uuid.UUID, string, error) {
	var x Invoice
	var link string
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if x, err = s.Get(ctx, tx, tenant, invoice.String()); err != nil {
			return err
		}
		if x.Status != "paid" {
			return nil
		}
		link, err = s.PageLink(ctx, tx, tenant, x)
		return err
	})
	if err != nil {
		return uuid.Nil, "", err
	}
	if x.Status != "paid" {
		return uuid.Nil, noLongerPaid, nil
	}
	st, err := s.Start(ctx, runtime.StartRequest{TenantID: tenant, WorkflowID: workflow, Version: version, Environment: "prod",
		Trigger: PaidTrigger(x, link), StartedBy: "invoice:" + x.Number, TriggerID: "invoice/" + workflow.String(), DedupKey: x.ID.String()})
	if le, ok := runtime.IsLimit(err); ok {
		return uuid.Nil, "Not started: the plan's limit " + le.Limit + " was reached.", nil
	}
	var ce *runtime.CapabilityError
	switch {
	case errors.Is(err, runtime.ErrTenantSuspended):
		return uuid.Nil, "Not started: the organisation is suspended.", nil
	case errors.Is(err, runtime.ErrNotFound):
		return uuid.Nil, "Not started: the workflow's version is gone.", nil
	case errors.As(err, &ce):
		return uuid.Nil, "Not started: " + ce.Error(), nil
	case err != nil:
		return uuid.Nil, "", err
	}
	return st.Ref.ID, "", nil
}

// PaidTrigger is the trigger of a run an invoice being paid starts:
// trigger.body is the invoice as a receipt needs it, with the bank's fee
// and what reached the business when the rail said (fee_kobo,
// received_kobo). The customer's
// name, phone and email, and the page link, are sealed in the run's
// history (engine/runtime: invoice trigger paths).
func PaidTrigger(x Invoice, link string) map[string]any {
	customer := map[string]any{"name": x.Customer.Name}
	if x.Customer.Phone != "" {
		customer["phone"] = x.Customer.Phone
	}
	if x.Customer.Email != "" {
		customer["email"] = x.Customer.Email
	}
	body := map[string]any{
		"event": "paid", "id": x.ID.String(), "number": x.Number, "status": x.Status,
		"amount_kobo": x.TotalKobo, "amount": Naira(x.TotalKobo), "paid_kobo": x.PaidKobo, "currency": x.Currency,
		"customer": customer, "link": link,
	}
	if x.PaidAt != nil {
		body["paid_at"] = x.PaidAt.UTC().Format(time.RFC3339)
	}
	// The bank's fee on the transfers that paid it, when it is known
	// (Invoice.Fee): what reached the business is what was sent less it.
	if sent, fee, ok := x.Fee(); ok {
		body["fee_kobo"], body["fee"] = fee, Naira(fee)
		body["received_kobo"], body["received"] = sent-fee, Naira(sent-fee)
	}
	return map[string]any{"type": TriggerType, "event": "paid", "body": body}
}

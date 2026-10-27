package invoicing

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// The invoice trigger's payment event (docs/spec/schools.md 9, B5;
// docs/invoices.md): a workflow whose trigger is {"type": "invoice",
// "config": {"event": "payment"}} starts once for every payment part
// counted towards one of the business's invoices, paid in full or not,
// with the invoice, the part and, for a school's invoice, its receipt as
// trigger.body. It is how a receipt goes to the family the moment the
// money is counted. The outbox is invoice_payment_starts (00214), kept
// as invoice_paid_starts is: a row per part and workflow, written in the
// transaction that counts the part.

// noLongerCounted is the note on a start not made because the part was
// undone before the run could start.
const noLongerCounted = "Not started: the payment was undone."

// queuePaymentStarts records, in the transaction that counted a part
// towards an invoice, a run to start for each prod workflow of the tenant
// whose invoice trigger starts on a payment: once per part and workflow,
// whatever happens after (the part undone, a replay, a restart).
func queuePaymentStarts(ctx context.Context, tx pgx.Tx, tenant, invoice, part uuid.UUID) error {
	_, err := tx.Exec(ctx, `INSERT INTO invoice_payment_starts (part_id, workflow_id, tenant_id, invoice_id, version)
		SELECT $3, t.workflow_id, $1, $2, t.version FROM triggers t
		 WHERE t.tenant_id = $1 AND t.type = 'invoice' AND t.environment = 'prod' AND 'payment' = ANY (t.events)
		ON CONFLICT (part_id, workflow_id) DO NOTHING`, tenant, invoice, part)
	return err
}

// StartPayments starts the runs that payments counted queued: each
// workflow's run once per part, with the invoice, the payment and its
// receipt as trigger.body. Run it every second, beside StartPaid.
func (s *Invoices) StartPayments(ctx context.Context) error {
	if s.Start == nil {
		return nil
	}
	type claim struct {
		tenant, part, invoice, workflow uuid.UUID
		version, attempts               int
	}
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id, part_id, invoice_id, workflow_id, version, attempts FROM taskiem_invoice_payment_starts_claim(20)`)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (claim, error) {
		var c claim
		err := r.Scan(&c.tenant, &c.part, &c.invoice, &c.workflow, &c.version, &c.attempts)
		return c, err
	})
	if err != nil {
		return err
	}
	for _, c := range list {
		run, note, err := s.startPayment(ctx, c.tenant, c.part, c.invoice, c.workflow, c.version)
		if err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // stopping; the claim runs out and the start is taken again
			}
			s.log().Warn("invoice payment: start", "tenant", c.tenant, "part", c.part, "workflow", c.workflow, "attempt", c.attempts, "err", err)
			if c.attempts < maxStartAttempts {
				continue
			}
			note = "Not started: " + err.Error()
		}
		var runID *uuid.UUID
		if run != uuid.Nil {
			runID = &run
		}
		if _, err := s.Pool.Exec(ctx, `SELECT taskiem_invoice_payment_starts_done($1, $2, $3, NULLIF($4, ''))`, c.part, c.workflow, runID, note); err != nil {
			return err
		}
	}
	return nil
}

// Receipt is a payment part's receipt as the trigger carries it (a
// school's; docs/schools.md#receipts): its id, number and verification
// link.
type Receipt struct {
	ID     uuid.UUID `json:"id"`
	Number string    `json:"number"`
	Link   string    `json:"link,omitempty"`
}

// ReceiptOf is the standing receipt of a payment part, if a school
// issued one; ok is false otherwise. The link is the public verification
// page, on the host that serves invoice pages.
func (s *Invoices) ReceiptOf(ctx context.Context, tx pgx.Tx, tenant, part uuid.UUID) (Receipt, bool, error) {
	var r Receipt
	var env map[string]any
	err := tx.QueryRow(ctx, `SELECT id, number, token FROM school_receipts WHERE tenant_id = $1 AND part_id = $2 AND voided_at IS NULL`, tenant, part).Scan(&r.ID, &r.Number, &env)
	if errors.Is(err, pgx.ErrNoRows) {
		return Receipt{}, false, nil
	}
	if err != nil {
		return Receipt{}, false, err
	}
	if s.PageBase != "" && s.Vault != nil {
		v, err := s.Vault.OpenTx(ctx, tx, tenant, env)
		if err != nil {
			return Receipt{}, false, err
		}
		if t, ok := v.(string); ok && t != "" {
			r.Link = ReceiptLink(s.PageBase, t)
		}
	}
	return r, true, nil
}

// ReceiptLink is a receipt's public verification page: /r/<token> on the
// host that serves invoice pages.
func ReceiptLink(base, token string) string {
	return strings.TrimRight(base, "/") + "/r/" + token
}

// ReceiptTokenHash is what finds a receipt from its verification link's
// token (school_receipts.token_hash; the public host looks it up).
func ReceiptTokenHash(token string) []byte {
	h := sha256.Sum256([]byte("taskiem receipt " + token))
	return h[:]
}

// startPayment starts one workflow's run for a counted part: the run, or
// why none was started (a note), or an error to try again.
func (s *Invoices) startPayment(ctx context.Context, tenant, part, invoice, workflow uuid.UUID, version int) (uuid.UUID, string, error) {
	var x Invoice
	var link, familyLink string
	var p *Payment
	var receipt *Receipt
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if x, err = s.Get(ctx, tx, tenant, invoice.String()); err != nil {
			return err
		}
		for i := range x.Payments {
			if x.Payments[i].ID == part && x.Payments[i].Undoes == nil && x.Payments[i].UndoneBy == nil {
				p = &x.Payments[i]
			}
		}
		if p == nil {
			return nil
		}
		if r, ok, err := s.ReceiptOf(ctx, tx, tenant, part); err != nil {
			return err
		} else if ok {
			receipt = &r
		}
		if s.FamilyLink != nil {
			if familyLink, err = s.FamilyLink(ctx, tx, tenant, x.ID, "receipt"); err != nil {
				return err
			}
		}
		link, err = s.PageLink(ctx, tx, tenant, x)
		return err
	})
	if err != nil {
		return uuid.Nil, "", err
	}
	if p == nil {
		return uuid.Nil, noLongerCounted, nil
	}
	st, err := s.Start(ctx, runtime.StartRequest{TenantID: tenant, WorkflowID: workflow, Version: version, Environment: "prod",
		Trigger: PaymentTrigger(x, *p, receipt, link, familyLink), StartedBy: "invoice:" + x.Number, TriggerID: "invoice/" + workflow.String(), DedupKey: "payment:" + part.String()})
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

// PaymentTrigger is the trigger of a run a payment counted starts:
// trigger.body is the invoice as PaidTrigger gives it (event payment,
// status as the invoice is now, due_kobo what is still due), with the
// payment (its part's id, amount, how it was matched and when, and the
// bank's fee and what reached the business when the rail said) and, for
// a school's invoice, its receipt (id, number and the verification link,
// which a template sends the family) and family_link, a fresh link to the
// family's page. The receipt and family links are sealed in the run's
// history as the invoice link is (engine/runtime: invoice trigger paths).
func PaymentTrigger(x Invoice, p Payment, r *Receipt, link, familyLink string) map[string]any {
	t := PaidTrigger(x, link)
	body := t["body"].(map[string]any)
	body["event"] = "payment"
	body["due_kobo"] = x.DueKobo
	pay := map[string]any{"id": p.ID.String(), "amount_kobo": p.AmountKobo, "amount": Naira(p.AmountKobo), "how": p.How,
		"paid_at": p.CreatedAt.UTC().Format(time.RFC3339)}
	if p.FeeKobo != nil {
		pay["fee_kobo"], pay["fee"] = *p.FeeKobo, Naira(*p.FeeKobo)
		pay["received_kobo"], pay["received"] = p.AmountKobo-*p.FeeKobo, Naira(p.AmountKobo-*p.FeeKobo)
	}
	body["payment"] = pay
	// The invoice-wide figures are the payment's, not PaidTrigger's.
	delete(body, "fee_kobo")
	delete(body, "fee")
	delete(body, "received_kobo")
	delete(body, "received")
	if r != nil {
		rec := map[string]any{"id": r.ID.String(), "number": r.Number}
		if r.Link != "" {
			rec["link"] = r.Link
		}
		body["receipt"] = rec
	}
	if familyLink != "" {
		body["family_link"] = familyLink
	}
	t["event"] = "payment"
	return t
}

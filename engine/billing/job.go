package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/alerts"
)

// Run is the billing job (scheduler role): it moves subscriptions along
// their periods, verifies payments still pending with the provider,
// enrols tenants without a subscription in a trial, and snapshots usage.
// Off, it only snapshots usage (useful for capacity planning anyway).
func (s *Service) Run(ctx context.Context) error {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	var lastSnap time.Time
	for {
		if s.On() {
			if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
				s.log().Error("billing tick", "err", err)
			}
			if err := s.Reconcile(ctx, 2*time.Minute); err != nil && ctx.Err() == nil {
				s.log().Error("billing reconcile", "err", err)
			}
		}
		if now := s.now(); now.Sub(lastSnap) >= time.Hour {
			if err := s.SnapshotAll(ctx); err != nil && ctx.Err() == nil {
				s.log().Error("usage snapshots", "err", err)
			} else {
				lastSnap = now
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

// Tick enrols unsubscribed tenants and acts on every subscription due.
func (s *Service) Tick(ctx context.Context) error {
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_billing_tenants() WHERE parent_id IS NULL AND NOT subscribed`)
	if err != nil {
		return err
	}
	fresh, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range fresh {
		errs = append(errs, s.Ensure(ctx, t, "billing"))
	}
	for range 10 { // bounded passes: one action per tenant per pass
		rows, err := s.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_billing_due($1, 200)`, s.now())
		if err != nil {
			return err
		}
		due, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		if len(due) == 0 {
			break
		}
		progressed := false
		for _, t := range due {
			acted, err := s.Advance(ctx, t)
			errs = append(errs, err)
			progressed = progressed || acted
		}
		if !progressed {
			break
		}
	}
	return errors.Join(errs...)
}

// Advance performs the one action a tenant's subscription is due for (see
// Subscription.Due) and reports whether it did anything.
func (s *Service) Advance(ctx context.Context, tenant uuid.UUID) (bool, error) {
	var act, chargeRef string
	var note *notice
	err := s.tx(ctx, tenant, func(tx pgx.Tx) error {
		sub, err := loadSub(ctx, tx, tenant, true)
		if err != nil || sub == nil {
			return err
		}
		now := s.now()
		act = sub.Due(now, s.Config)
		from := sub.Status
		switch act {
		case ActNone:
			// Rescheduled (e.g. the config's grace changed).
			sub.Schedule(s.Config)
			return saveSub(ctx, tx, sub, "billing")
		case ActCancel:
			sub.Status = StatusCancelled
			sub.CancelAtPeriodEnd = false
			note = &notice{kind: "cancelled", ref: sub.PeriodEnd.Format("2006-01-02"), subject: "Your Taskiem subscription has ended",
				body: "Your subscription ended at the end of its period, as you asked. Your workflows and history are kept; new runs are refused until you choose a plan on the Billing page."}
		case ActDegrade:
			sub.Status = StatusDegraded
			note = &notice{kind: "degraded", ref: fmt.Sprint(sub.PastDueSince.Unix()), subject: "Taskiem: new runs paused for an unpaid invoice",
				body: "Your invoice is still unpaid after the grace period, so new runs are now refused. Runs already running, approvals and reconciliation continue: no payment in flight is stranded. Pay the open invoice on the Billing page to resume at once."}
		case ActDun:
			ref, err := s.openInvoiceNumber(ctx, tx, tenant)
			if err != nil {
				return err
			}
			if sub.AuthorizationCode != "" && ref != "" {
				in, err := s.openInvoice(ctx, tx, tenant)
				if err != nil {
					return err
				}
				pay, err := s.newPayment(ctx, tx, tenant, in, sub.Provider, "authorization", "billing")
				if err != nil {
					return err
				}
				chargeRef = pay.Reference
			}
			note = &notice{kind: "past_due", ref: fmt.Sprintf("%s/%d", ref, sub.DunningAttempts), subject: "Taskiem: payment due for invoice " + ref,
				body: fmt.Sprintf("We could not collect payment for invoice %s. Pay it on the Billing page by %s to keep starting new runs; after that, new runs are paused until it is paid (running work always continues).",
					ref, sub.graceEnd(s.Config).Format("2 Jan 2006"))}
			sub.DunningAttempts++
		case ActRenew:
			if chargeRef, err = s.renew(ctx, tx, sub, now); err != nil {
				return err
			}
		}
		sub.Schedule(s.Config)
		if err := saveSub(ctx, tx, sub, "billing"); err != nil {
			return err
		}
		s.Store.ForgetLimits(tenant)
		return audit(ctx, tx, tenant, "system", "billing", "billing."+act, sub.PlanID, map[string]any{"from": from, "status": sub.Status})
	})
	if err != nil || act == ActNone {
		return false, err
	}
	if chargeRef != "" {
		if err := s.chargeSaved(ctx, tenant, chargeRef); err != nil {
			s.log().Warn("billing: saved card charge", "tenant", tenant, "err", err)
		}
	}
	if note != nil {
		s.notify(ctx, tenant, note.kind, note.ref, note.subject, note.body)
	}
	return true, nil
}

// renew invoices the next period: the plan (a scheduled downgrade takes
// effect now), the overage of ended months and any credit. Nothing owed
// renews at once; otherwise the subscription is past due until paid, and
// a saved card is charged (the returned reference) after commit.
func (s *Service) renew(ctx context.Context, tx pgx.Tx, sub *Subscription, now time.Time) (string, error) {
	start := sub.PeriodEnd
	kind := "renewal"
	switch sub.Status {
	case StatusTrial:
		start, kind = *sub.TrialEnd, "subscription"
	case StatusComped:
		start, kind = *sub.CompUntil, "subscription"
	}
	if start.Before(now.AddDate(0, 0, -1)) {
		start = now // the job was down: do not bill time already gone
	}
	planID, interval := sub.PlanID, sub.Interval
	if sub.PendingPlanID != "" {
		planID, interval = sub.PendingPlanID, sub.PendingInterval
	}
	if interval == "" {
		interval = IntervalMonthly
	}
	p, err := PlanByID(ctx, tx, planID)
	if err != nil {
		return "", err
	}
	end := PeriodEnd(start, interval)
	lines := []Line{planLine(p, interval, start, end)}
	over, err := s.overageLines(ctx, tx, sub, p, now)
	if err != nil {
		return "", err
	}
	lines = append(lines, over...)
	if sub.CreditKobo > 0 {
		lines = append(lines, Line{Kind: "credit", Description: "Credit from earlier changes", Quantity: 1, UnitKobo: -sub.CreditKobo, AmountKobo: -sub.CreditKobo})
	}
	in, left, err := s.issue(ctx, tx, sub, kind, p, interval, start, end, lines, nil)
	if err != nil {
		return "", err
	}
	sub.CreditKobo = left
	sub.PlanID, sub.Interval, sub.PendingPlanID, sub.PendingInterval = p.ID, interval, "", ""
	sub.PeriodStart, sub.PeriodEnd, sub.CompUntil = start, end, nil
	if sub.Status == StatusTrial {
		sub.TrialEnd = nil
	}
	if in.Status == "paid" {
		sub.Activate(start, end, interval, now)
		return "", nil
	}
	sub.Status = StatusActive // MarkPastDue starts from a good standing
	sub.MarkPastDue(now)
	if sub.AuthorizationCode == "" {
		return "", nil
	}
	pay, err := s.newPayment(ctx, tx, sub.TenantID, in, sub.Provider, "authorization", "billing")
	if err != nil {
		return "", err
	}
	// The first reminder waits for this charge's outcome.
	sub.DunningAttempts = 0
	return pay.Reference, nil
}

func (s *Service) openInvoice(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Invoice, error) {
	rows, err := tx.Query(ctx, `SELECT `+invCols+` FROM invoices WHERE tenant_id = $1 AND status IN ('open', 'uncollectible') AND kind <> 'upgrade'
		ORDER BY issued_at DESC LIMIT 1`, tenant)
	if err != nil {
		return Invoice{}, err
	}
	return pgx.CollectExactlyOneRow(rows, scanInvoice)
}

func (s *Service) openInvoiceNumber(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (string, error) {
	in, err := s.openInvoice(ctx, tx, tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return in.Number, err
}

// Reconcile asks the provider about payments pending longer than age
// (a missed webhook, a closed browser) and settles them. Pending more than
// a day, they are abandoned.
func (s *Service) Reconcile(ctx context.Context, age time.Duration) error {
	if !s.On() {
		return nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id, reference FROM taskiem_billing_pending_payments($1, 200)`, s.now().Add(-age))
	if err != nil {
		return err
	}
	type pending struct {
		tenant uuid.UUID
		ref    string
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pending, error) {
		var p pending
		return p, r.Scan(&p.tenant, &p.ref)
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range list {
		errs = append(errs, s.reconcileOne(ctx, p.tenant, p.ref))
	}
	return errors.Join(errs...)
}

func (s *Service) reconcileOne(ctx context.Context, tenant uuid.UUID, ref string) error {
	var pay Payment
	err := s.tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		pay, err = scanPayment(tx.QueryRow(ctx, `SELECT `+payCols+` FROM billing_payments WHERE tenant_id = $1 AND reference = $2`, tenant, ref))
		return err
	})
	if err != nil {
		return err
	}
	prov, err := s.provider(pay.Provider)
	if err != nil {
		return err
	}
	t, err := prov.Verify(ctx, ref)
	if err != nil {
		return err
	}
	if t.Status == TxPending && s.now().Sub(pay.CreatedAt) > 24*time.Hour {
		t = Transaction{Reference: ref, Status: TxAbandoned, Message: "not completed within a day"}
	}
	var recovered bool
	err = s.tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		recovered, err = s.settle(ctx, tx, tenant, ref, t, "billing:reconcile")
		return err
	})
	if errors.Is(err, ErrAmountMismatch) {
		s.log().Error("billing: payment amount mismatch", "tenant", tenant, "reference", ref)
		return s.tx(ctx, tenant, func(tx pgx.Tx) error {
			_, err := s.settle(ctx, tx, tenant, ref, t, "billing:reconcile")
			if errors.Is(err, ErrAmountMismatch) {
				return nil
			}
			return err
		})
	}
	if err == nil && recovered {
		s.notify(ctx, tenant, "recovered", ref, "Payment received: service restored",
			"We received your payment and your Taskiem subscription is in good standing again. New runs are accepted as usual.")
	}
	return err
}

type notice struct{ kind, ref, subject, body string }

// notify emails the tenant's owners (and billing email) once per kind and
// ref. Without a mailer it records nothing: the in-app banner remains.
func (s *Service) notify(ctx context.Context, tenant uuid.UUID, kind, ref, subject, body string) {
	if s.Mailer == nil || s.From == "" {
		return
	}
	var to []string
	err := s.tx(ctx, tenant, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO billing_notices (tenant_id, kind, ref) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, tenant, kind, ref)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		if to, err = ownerEmails(ctx, tx, tenant); err != nil {
			return err
		}
		sub, err := loadSub(ctx, tx, tenant, false)
		if err != nil {
			return err
		}
		if sub != nil && sub.BillingEmail != "" {
			to = append(to, sub.BillingEmail)
		}
		if len(to) == 0 {
			return nil
		}
		link := ""
		if s.PublicURL != "" {
			link = "\n\nBilling: " + s.PublicURL + "/settings/billing"
		}
		msg := alerts.BuildEmail(s.From, to, "[Taskiem] "+subject, body+link, kind+"-"+ref)
		// Sent inside the transaction: a failure rolls the notice back so
		// the next tick retries it.
		return s.Mailer.Send(ctx, s.From, to, msg)
	})
	if err != nil {
		s.log().Warn("billing: email", "tenant", tenant, "kind", kind, "err", err)
	}
}

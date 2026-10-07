package billing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/runtime"
)

// CheckoutRequest pays for a plan (from a trial, or after cancelling) or
// an open invoice.
type CheckoutRequest struct {
	Plan     string `json:"plan,omitempty"`
	Interval string `json:"interval,omitempty"`
	// Invoice pays this open invoice instead (past due).
	Invoice  *uuid.UUID `json:"invoice,omitempty"`
	Provider string     `json:"provider,omitempty"`
	// Email receives receipts and invoices (default: the first owner's).
	Email string `json:"email,omitempty"`
	// Channels offered at checkout: card, bank_transfer (default both).
	Channels []string `json:"channels,omitempty"`
}

// CheckoutResult is where to pay, or that nothing was owed.
type CheckoutResult struct {
	Invoice     Invoice `json:"invoice"`
	Reference   string  `json:"reference,omitempty"`
	CheckoutURL string  `json:"checkout_url,omitempty"`
	Paid        bool    `json:"paid"`
}

func validInterval(i string) (string, error) {
	switch i {
	case "", IntervalMonthly:
		return IntervalMonthly, nil
	case IntervalAnnual:
		return IntervalAnnual, nil
	}
	return "", fmt.Errorf("%w: interval is monthly or annual", ErrInvalid)
}

// offered reads a plan a tenant may choose.
func offered(ctx context.Context, tx pgx.Tx, id string) (Plan, error) {
	p, err := PlanByID(ctx, tx, id)
	if errors.Is(err, ErrNotFound) || (err == nil && (!p.Public || !p.Active)) {
		return Plan{}, fmt.Errorf("%w: plan %q is not offered", ErrInvalid, id)
	}
	return p, err
}

// Checkout issues (or reuses) the invoice to pay and starts a hosted
// payment at the provider.
func (s *Service) Checkout(ctx context.Context, tenant uuid.UUID, req CheckoutRequest, by string) (CheckoutResult, error) {
	if !s.On() {
		return CheckoutResult{}, fmt.Errorf("%w: billing is off on this deployment", ErrConflict)
	}
	prov, err := s.provider(req.Provider)
	if err != nil {
		return CheckoutResult{}, err
	}
	for _, c := range req.Channels {
		if c != "card" && c != "bank_transfer" {
			return CheckoutResult{}, fmt.Errorf("%w: channels are card and bank_transfer", ErrInvalid)
		}
	}
	if req.Email != "" && (!strings.Contains(req.Email, "@") || len(req.Email) > 254) {
		return CheckoutResult{}, fmt.Errorf("%w: email is not an email address", ErrInvalid)
	}
	var out CheckoutResult
	var pay Payment
	var sub *Subscription
	err = s.tx(ctx, tenant, func(tx pgx.Tx) error {
		if err := s.ensureTx(ctx, tx, tenant, by); err != nil {
			return err
		}
		var err error
		if sub, err = loadSub(ctx, tx, tenant, true); err != nil {
			return err
		}
		if sub == nil {
			return fmt.Errorf("%w: a sub-tenant runs on its partner's plan", ErrConflict)
		}
		if req.Email != "" {
			sub.BillingEmail = req.Email
		}
		now := s.now()
		var in Invoice
		switch {
		case req.Invoice != nil:
			if in, err = InvoiceByID(ctx, tx, tenant, *req.Invoice); err != nil {
				return err
			}
			if in.Status != "open" && in.Status != "uncollectible" {
				return fmt.Errorf("%w: invoice %s is %s", ErrConflict, in.Number, in.Status)
			}
		case sub.Status == StatusPastDue || sub.Status == StatusDegraded:
			// Pay what is owed first.
			rows, err := tx.Query(ctx, `SELECT `+invCols+` FROM invoices WHERE tenant_id = $1 AND status IN ('open', 'uncollectible') AND kind <> 'upgrade'
				ORDER BY issued_at DESC LIMIT 1`, tenant)
			if err != nil {
				return err
			}
			if in, err = pgx.CollectExactlyOneRow(rows, scanInvoice); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("%w: nothing is owed", ErrConflict)
				}
				return err
			}
		case sub.Status == StatusTrial || sub.Status == StatusCancelled:
			interval, err := validInterval(req.Interval)
			if err != nil {
				return err
			}
			planID := req.Plan
			if planID == "" {
				planID = sub.PlanID
			}
			p, err := offered(ctx, tx, planID)
			if err != nil {
				return err
			}
			if blockers, err := s.blockers(ctx, tx, tenant, p); err != nil {
				return err
			} else if len(blockers) > 0 {
				return &DowngradeError{Plan: p.Name, Blockers: blockers}
			}
			// The period starts at payment; superseded checkouts are voided.
			if _, err := tx.Exec(ctx, `UPDATE invoices SET status = 'void', closed_reason = 'superseded by a new checkout'
				WHERE tenant_id = $1 AND status = 'open' AND kind = 'subscription'`, tenant); err != nil {
				return err
			}
			end := PeriodEnd(now, interval)
			lines := []Line{planLine(p, interval, now, end)}
			if sub.CreditKobo > 0 {
				lines = append(lines, Line{Kind: "credit", Description: "Credit from earlier changes", Quantity: 1, UnitKobo: -sub.CreditKobo, AmountKobo: -sub.CreditKobo})
			}
			t := Compute(lines, s.Config.VATBasisPoints())
			if in, _, err = s.issue(ctx, tx, sub, "subscription", p, interval, now, end, lines, map[string]any{"credit_left_kobo": t.CreditLeft}); err != nil {
				return err
			}
			if in.Status == "paid" {
				if err := s.apply(ctx, tx, sub, in, now); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("%w: the subscription is %s; change plans with POST /v1/billing/plan", ErrConflict, sub.Status)
		}
		out.Invoice = in
		sub.Schedule(s.Config)
		if err := saveSub(ctx, tx, sub, by); err != nil {
			return err
		}
		if in.Status == "paid" {
			out.Paid = true
			s.Store.ForgetLimits(tenant)
			return nil
		}
		pay, err = s.newPayment(ctx, tx, tenant, in, prov.Name(), "checkout", by)
		if err != nil {
			return err
		}
		at, actor := actorOf(by)
		return audit(ctx, tx, tenant, at, actor, "billing.checkout", in.Number, map[string]any{"reference": pay.Reference, "provider": prov.Name(), "total_kobo": in.TotalKobo})
	})
	if err != nil || out.Paid {
		return out, err
	}
	email, err := s.payerEmail(ctx, tenant, sub)
	if err != nil {
		return out, err
	}
	callback := ""
	if s.PublicURL != "" {
		callback = s.PublicURL + "/settings/billing?reference=" + pay.Reference
	}
	sess, err := prov.StartCheckout(ctx, Checkout{Reference: pay.Reference, AmountKobo: pay.AmountKobo, Email: email, CallbackURL: callback,
		Channels: req.Channels, Metadata: map[string]string{"tenant": tenant.String(), "invoice": out.Invoice.Number}})
	if err != nil {
		return out, err
	}
	err = s.tx(ctx, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE billing_payments SET checkout_url = $2 WHERE id = $1`, pay.ID, sess.URL)
		return err
	})
	out.Reference, out.CheckoutURL = pay.Reference, sess.URL
	return out, err
}

// Blocker is something a tenant uses that a smaller plan does not allow:
// what it must remove before downgrading.
type Blocker struct {
	Limit   string `json:"limit"`
	Used    int64  `json:"used"`
	Allowed int64  `json:"allowed"`
	Remove  string `json:"remove"`
}

// DowngradeError refuses a plan the tenant's current usage exceeds.
type DowngradeError struct {
	Plan     string
	Blockers []Blocker
}

func (e *DowngradeError) Error() string {
	parts := make([]string, len(e.Blockers))
	for i, b := range e.Blockers {
		parts[i] = b.Remove
	}
	return fmt.Sprintf("the %s plan is smaller than what this tenant uses: %s", e.Plan, strings.Join(parts, "; "))
}

// blockers lists what the tenant uses beyond plan p: counted limits and
// features in use.
func (s *Service) blockers(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, p Plan) ([]Blocker, error) {
	target, err := p.RuntimeLimits(s.Store.PlatformLimits())
	if err != nil {
		return nil, err
	}
	// Operator overrides stay with the tenant whatever the plan.
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT jsonb_strip_nulls(to_jsonb(l) - 'tenant_id' - 'updated_by' - 'updated_at') FROM tenant_limits l WHERE tenant_id = $1`, tenant).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if len(raw) > 0 {
		over := map[string]any{}
		if err := jsonUnmarshal(raw, &over); err != nil {
			return nil, err
		}
		if target, err = target.With(over); err != nil {
			return nil, err
		}
	}
	var out []Blocker
	count := func(limit string, allowed int64, q, noun string) error {
		if allowed <= 0 {
			return nil
		}
		var n int64
		if err := tx.QueryRow(ctx, q, tenant).Scan(&n); err != nil {
			return err
		}
		if n > allowed {
			out = append(out, Blocker{Limit: limit, Used: n, Allowed: allowed, Remove: fmt.Sprintf("remove %d %s (%d of %d allowed)", n-allowed, noun, n, allowed)})
		}
		return nil
	}
	checks := []struct {
		limit   string
		allowed int64
		q, noun string
	}{
		{"max_workflows", int64(target.MaxWorkflows), `SELECT count(*) FROM workflows WHERE tenant_id = $1`, "workflows"},
		{"max_secrets", int64(target.MaxSecrets), `SELECT count(*) FROM secrets WHERE tenant_id = $1 AND name IS NOT NULL`, "secrets"},
		{"max_connections", int64(target.MaxConnections), `SELECT count(*) FROM connections WHERE tenant_id = $1 AND status = 'active'`, "connections"},
		{"max_subtenants", int64(p.Partner.MaxSubtenants), `SELECT count(*) FROM taskiem_partner_usage($1)`, "sub-tenants"},
	}
	for _, c := range checks {
		if err := count(c.limit, c.allowed, c.q, c.noun); err != nil {
			return nil, err
		}
	}
	features := []struct {
		feature, q, remove string
	}{
		{FeatureSSO, `SELECT count(*) FROM sso_connections WHERE tenant_id = $1`, "delete the single sign-on connections"},
		{FeatureSCIM, `SELECT count(*) FROM scim_config WHERE tenant_id = $1`, "turn off SCIM provisioning"},
		{FeatureCustomRoles, `SELECT count(*) FROM roles WHERE tenant_id = $1`, "delete the custom roles"},
		{FeatureGit, `SELECT count(*) FROM git_connections WHERE tenant_id = $1`, "disconnect Git from every environment"},
		{FeatureEmbedded, `SELECT count(*) FROM embed_apps WHERE tenant_id = $1`, "delete the embed apps"},
		{FeatureWhiteLabel, `SELECT count(*) FROM embed_app_domains WHERE tenant_id = $1`, "remove the embed apps' custom domains"},
		{FeatureBYOK, `SELECT count(*) FROM tenant_byok_keys WHERE tenant_id = $1 AND status <> 'retired'`, "return to the platform key (Settings > Encryption keys)"},
	}
	for _, f := range features {
		if p.Has(f.feature) {
			continue
		}
		var n int64
		if err := tx.QueryRow(ctx, f.q, tenant).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			out = append(out, Blocker{Limit: "feature:" + f.feature, Used: n, Allowed: 0, Remove: f.remove + " (the plan has no " + featureNames[f.feature] + ")"})
		}
	}
	return out, nil
}

// ChangeResult is the outcome of a plan change.
type ChangeResult struct {
	// Effective is "now" (upgrade, paid or nothing owed), "on_payment"
	// (upgrade awaiting payment at CheckoutURL) or "period_end" (downgrade).
	Effective    string        `json:"effective"`
	Subscription *Subscription `json:"subscription"`
	Invoice      *Invoice      `json:"invoice,omitempty"`
	Reference    string        `json:"reference,omitempty"`
	CheckoutURL  string        `json:"checkout_url,omitempty"`
}

// isUpgrade compares plans: a higher tier, or a dearer plan of the same
// tier, or monthly to annual on the same plan.
func isUpgrade(cur Plan, curInterval string, to Plan, toInterval string) bool {
	if cur.ID == to.ID {
		return curInterval == IntervalMonthly && toInterval == IntervalAnnual
	}
	if TierRank(to.Tier) != TierRank(cur.Tier) {
		return TierRank(to.Tier) > TierRank(cur.Tier)
	}
	return to.MonthlyKobo > cur.MonthlyKobo
}

// ChangePlan moves a tenant to another plan or interval. Upgrades take
// effect when paid (at once with a saved card), credited for the unused
// part of the current period; downgrades take effect at period end, and
// are refused while the tenant uses more than the smaller plan allows.
func (s *Service) ChangePlan(ctx context.Context, tenant uuid.UUID, planID, interval, by string) (ChangeResult, error) {
	if !s.On() {
		return ChangeResult{}, fmt.Errorf("%w: billing is off on this deployment", ErrConflict)
	}
	interval, err := validInterval(interval)
	if err != nil {
		return ChangeResult{}, err
	}
	var out ChangeResult
	var charge, checkout bool
	var pay Payment
	var sub *Subscription
	err = s.tx(ctx, tenant, func(tx pgx.Tx) error {
		if err := s.ensureTx(ctx, tx, tenant, by); err != nil {
			return err
		}
		var err error
		if sub, err = loadSub(ctx, tx, tenant, true); err != nil {
			return err
		}
		if sub == nil {
			return fmt.Errorf("%w: a sub-tenant runs on its partner's plan", ErrConflict)
		}
		to, err := offered(ctx, tx, planID)
		if err != nil {
			return err
		}
		cur, err := PlanByID(ctx, tx, sub.PlanID)
		if err != nil {
			return err
		}
		at, actor := actorOf(by)
		now := s.now()
		switch sub.Status {
		case StatusComped:
			return fmt.Errorf("%w: this plan was granted by the operator; ask them to change it", ErrConflict)
		case StatusPastDue, StatusDegraded:
			return fmt.Errorf("%w: pay the open invoice before changing plans", ErrConflict)
		case StatusCancelled:
			return fmt.Errorf("%w: the subscription is cancelled; start a new one with POST /v1/billing/checkout", ErrConflict)
		}
		if to.ID == sub.PlanID && interval == sub.Interval {
			// Same plan: undo a scheduled downgrade.
			sub.PendingPlanID, sub.PendingInterval = "", ""
			out.Effective = "now"
			out.Subscription = sub
			return saveSub(ctx, tx, sub, by)
		}
		blockers, err := s.blockers(ctx, tx, tenant, to)
		if err != nil {
			return err
		}
		if len(blockers) > 0 {
			return &DowngradeError{Plan: to.Name, Blockers: blockers}
		}
		if sub.Status == StatusTrial {
			// During the trial a plan change is just the plan tried.
			sub.PlanID, sub.Interval, sub.PendingPlanID, sub.PendingInterval = to.ID, interval, "", ""
			out.Effective, out.Subscription = "now", sub
			s.Store.ForgetLimits(tenant)
			if err := saveSub(ctx, tx, sub, by); err != nil {
				return err
			}
			return audit(ctx, tx, tenant, at, actor, "billing.plan.change", to.ID, map[string]any{"from": cur.ID, "to": to.ID, "interval": interval, "effective": "now"})
		}
		if !isUpgrade(cur, sub.Interval, to, interval) {
			sub.PendingPlanID, sub.PendingInterval = to.ID, interval
			out.Effective, out.Subscription = "period_end", sub
			if err := saveSub(ctx, tx, sub, by); err != nil {
				return err
			}
			return audit(ctx, tx, tenant, at, actor, "billing.plan.change", to.ID,
				map[string]any{"from": cur.ID, "to": to.ID, "interval": interval, "effective": "period_end", "at": sub.PeriodEnd})
		}
		// Upgrade: a new period from now at the new price, less what is
		// left of the current one.
		if _, err := tx.Exec(ctx, `UPDATE invoices SET status = 'void', closed_reason = 'superseded by a new plan change'
			WHERE tenant_id = $1 AND status = 'open' AND kind = 'upgrade'`, tenant); err != nil {
			return err
		}
		end := PeriodEnd(now, interval)
		lines := []Line{planLine(to, interval, now, end)}
		if credit := Unused(cur.Price(sub.Interval), sub.PeriodStart, sub.PeriodEnd, now); credit > 0 {
			lines = append(lines, Line{Kind: "proration_credit", Quantity: 1, UnitKobo: -credit, AmountKobo: -credit,
				Description: fmt.Sprintf("Unused time on the %s plan (%s) to %s", cur.Name, sub.Interval, sub.PeriodEnd.Format("2 Jan 2006"))})
		}
		if sub.CreditKobo > 0 {
			lines = append(lines, Line{Kind: "credit", Description: "Credit from earlier changes", Quantity: 1, UnitKobo: -sub.CreditKobo, AmountKobo: -sub.CreditKobo})
		}
		t := Compute(lines, s.Config.VATBasisPoints())
		in, left, err := s.issue(ctx, tx, sub, "upgrade", to, interval, now, end, lines, map[string]any{"from_plan": cur.ID, "credit_left_kobo": t.CreditLeft})
		if err != nil {
			return err
		}
		_ = left // carried on the invoice (credit_left_kobo): the credit is replaced when it is paid
		out.Invoice = &in
		if err := audit(ctx, tx, tenant, at, actor, "billing.plan.change", to.ID,
			map[string]any{"from": cur.ID, "to": to.ID, "interval": interval, "effective": "on_payment", "invoice": in.Number}); err != nil {
			return err
		}
		if in.Status == "paid" {
			if err := s.apply(ctx, tx, sub, in, now); err != nil {
				return err
			}
			out.Effective = "now"
			s.Store.ForgetLimits(tenant)
		} else {
			out.Effective = "on_payment"
			method := "checkout"
			if sub.AuthorizationCode != "" {
				method, charge = "authorization", true
			} else {
				checkout = true
			}
			prov, err := s.provider(sub.Provider)
			if err != nil {
				return err
			}
			if pay, err = s.newPayment(ctx, tx, tenant, in, prov.Name(), method, by); err != nil {
				return err
			}
			out.Reference = pay.Reference
		}
		out.Subscription = sub
		return saveSub(ctx, tx, sub, by)
	})
	if err != nil {
		return out, err
	}
	switch {
	case charge:
		if err := s.chargeSaved(ctx, tenant, pay.Reference); err != nil {
			return out, err
		}
		err = s.tx(ctx, tenant, func(tx pgx.Tx) error {
			var err error
			if out.Subscription, err = loadSub(ctx, tx, tenant, false); err != nil {
				return err
			}
			in, err := InvoiceByID(ctx, tx, tenant, out.Invoice.ID)
			if err == nil && in.Status == "paid" {
				out.Effective, out.Invoice = "now", &in
			}
			return err
		})
	case checkout:
		email, err := s.payerEmail(ctx, tenant, sub)
		if err != nil {
			return out, err
		}
		prov, err := s.provider(pay.Provider)
		if err != nil {
			return out, err
		}
		callback := ""
		if s.PublicURL != "" {
			callback = s.PublicURL + "/settings/billing?reference=" + pay.Reference
		}
		sess, err := prov.StartCheckout(ctx, Checkout{Reference: pay.Reference, AmountKobo: pay.AmountKobo, Email: email, CallbackURL: callback,
			Metadata: map[string]string{"tenant": tenant.String(), "invoice": out.Invoice.Number}})
		if err != nil {
			return out, err
		}
		out.CheckoutURL = sess.URL
		return out, s.tx(ctx, tenant, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE billing_payments SET checkout_url = $2 WHERE id = $1`, pay.ID, sess.URL)
			return err
		})
	}
	return out, err
}

// Cancel cancels at the end of the current period (or trial); resume
// undoes it before then.
func (s *Service) Cancel(ctx context.Context, tenant uuid.UUID, resume bool, by string) (*Subscription, error) {
	if !s.On() {
		return nil, fmt.Errorf("%w: billing is off on this deployment", ErrConflict)
	}
	var sub *Subscription
	err := s.tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if sub, err = loadSub(ctx, tx, tenant, true); err != nil {
			return err
		}
		if sub == nil {
			return fmt.Errorf("%w: no subscription", ErrConflict)
		}
		switch sub.Status {
		case StatusActive, StatusTrial, StatusPastDue:
		case StatusComped:
			return fmt.Errorf("%w: this plan was granted by the operator; ask them to end it", ErrConflict)
		default:
			return fmt.Errorf("%w: the subscription is %s", ErrConflict, sub.Status)
		}
		if sub.Status == StatusPastDue && !resume {
			// Unpaid: cancelling now ends it (the open invoice is voided).
			sub.Status = StatusCancelled
			if _, err := tx.Exec(ctx, `UPDATE invoices SET status = 'void', closed_reason = 'subscription cancelled' WHERE tenant_id = $1 AND status = 'open'`, tenant); err != nil {
				return err
			}
		}
		sub.CancelAtPeriodEnd = !resume
		sub.Schedule(s.Config)
		if err := saveSub(ctx, tx, sub, by); err != nil {
			return err
		}
		s.Store.ForgetLimits(tenant)
		at, actor := actorOf(by)
		action := "billing.cancel"
		if resume {
			action = "billing.resume"
		}
		return audit(ctx, tx, tenant, at, actor, action, sub.PlanID, map[string]any{"period_end": sub.PeriodEnd, "status": sub.Status})
	})
	return sub, err
}

// HandleWebhook processes a provider's delivery: signature checked, the
// payment re-verified with the provider (a webhook body is never trusted
// for amounts), then settled once.
func (s *Service) HandleWebhook(ctx context.Context, provider string, h http.Header, body []byte) error {
	if !s.On() {
		return fmt.Errorf("%w: billing is off", ErrNotFound)
	}
	prov, ok := s.Providers[provider]
	if !ok {
		return fmt.Errorf("%w: unknown provider", ErrNotFound)
	}
	ev, err := prov.ParseWebhook(h, body)
	if err != nil {
		return err
	}
	if !ev.Payment {
		return nil
	}
	tenant, ok := TenantOfReference(ev.Reference)
	if !ok {
		return nil // not one of ours (the merchant account may take other payments)
	}
	var seen bool
	err = s.tx(ctx, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM billing_webhook_receipts WHERE provider = $1 AND dedup_key = $2)`, provider, ev.DedupKey).Scan(&seen)
	})
	if err != nil || seen {
		return err
	}
	t, err := prov.Verify(ctx, ev.Reference)
	if err != nil {
		return err // the provider redelivers; reconciliation catches up meanwhile
	}
	var recovered bool
	err = s.tx(ctx, tenant, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO billing_webhook_receipts (provider, dedup_key, tenant_id, outcome) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
			provider, ev.DedupKey, tenant, t.Status)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		recovered, err = s.settle(ctx, tx, tenant, ev.Reference, t, "billing:webhook")
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	})
	if errors.Is(err, ErrAmountMismatch) {
		// Recorded (the mismatch commits with its receipt in a separate
		// transaction below); acknowledged so the provider stops resending.
		s.log().Error("billing: payment amount mismatch", "tenant", tenant, "reference", ev.Reference, "paid_kobo", t.AmountKobo, "currency", t.Currency)
		return s.recordMismatch(ctx, tenant, provider, ev, t)
	}
	if err == nil && recovered {
		s.notify(ctx, tenant, "recovered", ev.Reference, "Payment received: service restored",
			"We received your payment and your Taskiem subscription is in good standing again. New runs are accepted as usual.")
	}
	return err
}

// recordMismatch commits a mismatched payment (settle's transaction was
// rolled back by its error) so it shows to operators and is not retried.
func (s *Service) recordMismatch(ctx context.Context, tenant uuid.UUID, provider string, ev WebhookEvent, t Transaction) error {
	return s.tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO billing_webhook_receipts (provider, dedup_key, tenant_id, outcome) VALUES ($1, $2, $3, 'mismatch') ON CONFLICT DO NOTHING`,
			provider, ev.DedupKey, tenant); err != nil {
			return err
		}
		_, err := s.settle(ctx, tx, tenant, ev.Reference, t, "billing:webhook")
		if errors.Is(err, ErrAmountMismatch) {
			return nil
		}
		return err
	})
}

// ---- overview ----

// Overview is everything the billing page shows a tenant.
type Overview struct {
	Enabled           bool               `json:"enabled"`
	PlaceholderPrices bool               `json:"placeholder_prices"`
	Plan              Plan               `json:"plan"`
	Subscription      *Subscription      `json:"subscription"`
	Inherited         bool               `json:"inherited"` // a sub-tenant on its partner's plan
	Plans             []Plan             `json:"plans"`
	Limits            runtime.Limits     `json:"limits"`
	Usage             runtime.Usage      `json:"usage"`
	History           []Snapshot         `json:"history"`
	SubTenants        []PartnerUsage     `json:"subtenant_usage,omitempty"`
	Invoices          []Invoice          `json:"invoices"`
	OpenInvoice       *Invoice           `json:"open_invoice,omitempty"`
	PendingPlan       *Plan              `json:"pending_plan,omitempty"`
	Banner            *Banner            `json:"banner,omitempty"`
	VATPercent        float64            `json:"vat_percent"`
	Providers         []string           `json:"providers"`
	PaymentMethod     *map[string]string `json:"payment_method,omitempty"`
}

// Banner is the in-app notice for a subscription needing attention.
type Banner struct {
	Level   string `json:"level"` // warning, error
	Message string `json:"message"`
}

// Overview reads the tenant's billing page.
func (s *Service) Overview(ctx context.Context, tenant uuid.UUID) (Overview, error) {
	out := Overview{Enabled: s.On(), Invoices: []Invoice{}, Plans: []Plan{}, History: []Snapshot{}}
	view, err := s.Store.ViewLimits(ctx, tenant)
	if err != nil {
		return out, err
	}
	out.Limits, out.Usage = view.Limits, view.Usage
	if !s.On() {
		out.Plan = InternalPlan()
		return out, nil
	}
	if err := s.Ensure(ctx, tenant, "billing"); err != nil {
		return out, err
	}
	out.PlaceholderPrices, out.VATPercent = s.Config.PlaceholderPrices, s.Config.VATPercent
	for name := range s.Providers {
		out.Providers = append(out.Providers, name)
	}
	err = s.tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if out.Plan, out.Subscription, err = s.PlanOf(ctx, tx, tenant); err != nil {
			return err
		}
		var parent *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT parent_id FROM tenants WHERE id = $1`, tenant).Scan(&parent); err != nil {
			return err
		}
		out.Inherited = parent != nil
		if out.Plans, err = Catalogue(ctx, tx); err != nil {
			return err
		}
		if out.Invoices, err = Invoices(ctx, tx, tenant, 24); err != nil {
			return err
		}
		for i := range out.Invoices {
			if in := out.Invoices[i]; (in.Status == "open" || in.Status == "uncollectible") && out.OpenInvoice == nil {
				out.OpenInvoice = &out.Invoices[i]
			}
		}
		if out.History, err = Snapshots(ctx, tx, tenant, 31); err != nil {
			return err
		}
		if out.SubTenants, err = PartnerAggregate(ctx, tx, tenant, s.now().AddDate(0, 0, -30), s.now()); err != nil {
			return err
		}
		if sub := out.Subscription; sub != nil {
			if sub.PendingPlanID != "" {
				p, err := PlanByID(ctx, tx, sub.PendingPlanID)
				if err != nil {
					return err
				}
				out.PendingPlan = &p
			}
			if sub.CardLast4 != "" {
				out.PaymentMethod = &map[string]string{"provider": sub.Provider, "brand": sub.CardBrand, "last4": sub.CardLast4, "exp": sub.CardExp}
			}
		}
		return nil
	})
	out.Banner = s.Banner(out.Subscription)
	return out, err
}

// Banner is the in-app notice for a subscription (nil: none).
func (s *Service) Banner(sub *Subscription) *Banner {
	if sub == nil {
		return nil
	}
	day := func(t time.Time) string { return t.Format("2 Jan 2006") }
	switch sub.Status {
	case StatusPastDue:
		msg := "Your payment is overdue."
		if sub.PastDueSince != nil {
			msg += " Pay by " + day(sub.graceEnd(s.Config)) + " to keep starting new runs."
		}
		return &Banner{Level: "warning", Message: msg}
	case StatusDegraded:
		return &Banner{Level: "error", Message: "Your subscription is unpaid: new runs are refused. Running runs, approvals and reconciliation continue. Pay the open invoice to restore service."}
	case StatusCancelled:
		return &Banner{Level: "error", Message: "Your subscription is cancelled: new runs are refused. Choose a plan to resume."}
	case StatusTrial:
		if sub.TrialEnd != nil && sub.TrialEnd.Sub(s.now()) < 3*24*time.Hour {
			return &Banner{Level: "warning", Message: "Your trial ends on " + day(*sub.TrialEnd) + ". Choose a plan to keep going."}
		}
	case StatusActive:
		if sub.CancelAtPeriodEnd {
			return &Banner{Level: "warning", Message: "Your subscription ends on " + day(sub.PeriodEnd) + "."}
		}
	}
	return nil
}

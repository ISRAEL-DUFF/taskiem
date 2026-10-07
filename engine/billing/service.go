package billing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// Service runs billing: subscriptions, invoices, payments and the job
// that moves them along. A nil *Service, or one not Enabled, is billing
// off: every tenant on the internal plan, every feature on.
type Service struct {
	Pool    *pgxpool.Pool
	Store   *runtime.Store // effective limits; its cache is dropped on plan changes
	Config  *Config
	Enabled bool
	// Providers by name; Default is used for new checkouts.
	Providers map[string]Provider
	Default   string
	// Mailer and From send dunning and lifecycle emails; nil skips them
	// (in-app banners still show).
	Mailer alerts.Mailer
	From   string
	// PublicURL builds links back to the billing page and checkout returns.
	PublicURL string
	Logger    *slog.Logger
	// Clock is the billing clock (tests use a fake one); nil is time.Now.
	Clock func() time.Time
}

// On reports whether billing is on.
func (s *Service) On() bool { return s != nil && s.Enabled && s.Config != nil }

func (s *Service) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) tx(ctx context.Context, tenant uuid.UUID, fn func(pgx.Tx) error) error {
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, fn)
}

func (s *Service) provider(name string) (Provider, error) {
	if name == "" {
		name = s.Default
	}
	p, ok := s.Providers[name]
	if !ok || p == nil {
		return nil, fmt.Errorf("%w: payment provider %q is not configured on this deployment", ErrInvalid, name)
	}
	return p, nil
}

// Errors the API maps to responses.
var (
	ErrInvalid  = errors.New("invalid")  // 400
	ErrConflict = errors.New("conflict") // 409
	// ErrAmountMismatch: a provider reported a payment of another amount
	// or currency than invoiced; the invoice stays unpaid.
	ErrAmountMismatch = errors.New("billing: paid amount does not match the invoice")
)

func audit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, actorType, actor, action, target string, detail map[string]any) error {
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, $2, $3, $4, $5, $6)`, tenant, actorType, actor, action, target, raw)
	return err
}

// actorOf splits "user:<id>" style actors; plain names are system actors.
func actorOf(by string) (string, string) {
	if t, a, ok := strings.Cut(by, ":"); ok && (t == "user" || t == "api_key" || t == "system") {
		return t, a
	}
	return "system", by
}

// ---- plans and features ----

// PlanOf is a tenant's plan (a sub-tenant's partner's) and its
// subscription (nil for a sub-tenant or without one). Without billing, or
// without a subscription, it is the internal plan.
func (s *Service) PlanOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Plan, *Subscription, error) {
	if !s.On() {
		return InternalPlan(), nil, nil
	}
	var planID string
	err := tx.QueryRow(ctx, `SELECT plan_id FROM taskiem_tenant_plan($1)`, tenant).Scan(&planID)
	if errors.Is(err, pgx.ErrNoRows) {
		return InternalPlan(), nil, nil
	}
	if err != nil {
		return Plan{}, nil, err
	}
	p, err := PlanByID(ctx, tx, planID)
	if err != nil {
		return p, nil, err
	}
	sub, err := loadSub(ctx, tx, tenant, false)
	return p, sub, err
}

// FeatureError refuses a feature the tenant's plan does not include.
type FeatureError struct {
	Feature, Plan string
}

func (e *FeatureError) Error() string {
	return fmt.Sprintf("the %s plan does not include %s; upgrade on the Billing page", e.Plan, featureNames[e.Feature])
}

var featureNames = map[string]string{
	FeatureSSO: "single sign-on", FeatureSCIM: "SCIM provisioning", FeatureCustomRoles: "custom roles",
	FeatureWhiteLabel: "white-label custom domains", FeatureBYOK: "bring-your-own-key encryption", FeatureGit: "Git integration",
	FeatureAI: "AI building", FeatureEmbedded: "embedding and the partner API",
}

// Require returns a *FeatureError unless the tenant's plan has feature.
// Billing off allows everything.
func (s *Service) Require(ctx context.Context, tenant uuid.UUID, feature string) error {
	if !s.On() {
		return nil
	}
	var p Plan
	err := s.tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		p, _, err = s.PlanOf(ctx, tx, tenant)
		return err
	})
	if err != nil {
		return err
	}
	if !p.Has(feature) {
		return &FeatureError{Feature: feature, Plan: p.Name}
	}
	return nil
}

// AIOverage is the tenant's plan's AI overage rate (0: the AI budget is a
// hard cap).
func (s *Service) AIOverage(ctx context.Context, tenant uuid.UUID) (int64, error) {
	if !s.On() {
		return 0, nil
	}
	var rate int64
	err := s.tx(ctx, tenant, func(tx pgx.Tx) error {
		p, _, err := s.PlanOf(ctx, tx, tenant)
		rate = p.Overage.AIKoboPerMillionTokens
		return err
	})
	return rate, err
}

// ---- subscriptions ----

// Ensure starts a trial for a top-level tenant without a subscription
// (billing on). Sub-tenants never subscribe.
func (s *Service) Ensure(ctx context.Context, tenant uuid.UUID, by string) error {
	if !s.On() {
		return nil
	}
	return s.tx(ctx, tenant, func(tx pgx.Tx) error { return s.ensureTx(ctx, tx, tenant, by) })
}

func (s *Service) ensureTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, by string) error {
	var parent *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT parent_id FROM tenants WHERE id = $1`, tenant).Scan(&parent); err != nil {
		return err
	}
	if parent != nil {
		return nil
	}
	sub, err := loadSub(ctx, tx, tenant, true)
	if err != nil || sub != nil {
		return err
	}
	planID := s.Config.TrialPlan
	if planID == "" {
		planID = s.Config.Plans[0].ID
	}
	now := s.now()
	end := now.Add(time.Duration(s.Config.TrialDays) * 24 * time.Hour)
	if s.Config.TrialDays == 0 {
		end = now.Add(time.Minute) // no trial: invoiced at once
	}
	sub = &Subscription{TenantID: tenant, PlanID: planID, Interval: IntervalMonthly, Status: StatusTrial, PeriodStart: now, PeriodEnd: end, TrialEnd: &end}
	sub.Schedule(s.Config)
	if err := saveSub(ctx, tx, sub, by); err != nil {
		return err
	}
	at, actor := actorOf(by)
	s.Store.ForgetLimits(tenant)
	return audit(ctx, tx, tenant, at, actor, "billing.trial.start", planID, map[string]any{"plan": planID, "trial_end": end})
}

// Grant puts a tenant on a plan without payment (design partners, comps),
// until a time or indefinitely. Operators only (taskiem billing grant).
func (s *Service) Grant(ctx context.Context, tenant uuid.UUID, planID string, until *time.Time, by string) error {
	if s.Config == nil {
		return errors.New("billing: no plans config loaded")
	}
	return s.tx(ctx, tenant, func(tx pgx.Tx) error {
		var parent *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT parent_id FROM tenants WHERE id = $1`, tenant).Scan(&parent); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("tenant %s: %w", tenant, ErrNotFound)
			}
			return err
		}
		if parent != nil {
			return fmt.Errorf("%w: a sub-tenant runs on its partner's plan", ErrInvalid)
		}
		p, err := PlanByID(ctx, tx, planID)
		if err != nil {
			return err
		}
		now := s.now()
		if until != nil && !until.After(now) {
			return fmt.Errorf("%w: --until must be in the future", ErrInvalid)
		}
		sub, err := loadSub(ctx, tx, tenant, true)
		if err != nil {
			return err
		}
		if sub == nil {
			sub = &Subscription{TenantID: tenant, Interval: IntervalMonthly}
		}
		end := PeriodEnd(now, IntervalMonthly)
		if until != nil {
			end = *until
		}
		sub.PlanID, sub.Status, sub.PeriodStart, sub.PeriodEnd, sub.CompUntil = p.ID, StatusComped, now, end, until
		sub.PastDueSince, sub.DunningAttempts, sub.CancelAtPeriodEnd, sub.PendingPlanID, sub.PendingInterval = nil, 0, false, "", ""
		sub.Schedule(s.Config)
		if err := saveSub(ctx, tx, sub, by); err != nil {
			return err
		}
		// Open invoices are forgiven: the operator decided.
		if _, err := tx.Exec(ctx, `UPDATE invoices SET status = 'void', closed_reason = 'comped by the operator' WHERE tenant_id = $1 AND status = 'open'`, tenant); err != nil {
			return err
		}
		s.Store.ForgetLimits(tenant)
		detail := map[string]any{"plan": p.ID}
		if until != nil {
			detail["until"] = *until
		}
		return audit(ctx, tx, tenant, "system", by, "billing.grant", p.ID, detail)
	})
}

// ---- invoices ----

// Invoice is an issued invoice.
type Invoice struct {
	ID            uuid.UUID      `json:"id"`
	Number        string         `json:"number"`
	Kind          string         `json:"kind"`
	PlanID        string         `json:"plan"`
	Interval      string         `json:"interval"`
	PeriodStart   time.Time      `json:"period_start"`
	PeriodEnd     time.Time      `json:"period_end"`
	Currency      string         `json:"currency"`
	Lines         []Line         `json:"lines"`
	SubtotalKobo  int64          `json:"subtotal_kobo"`
	VATRateBP     int            `json:"vat_rate_bp"`
	VATKobo       int64          `json:"vat_kobo"`
	TotalKobo     int64          `json:"total_kobo"`
	BillTo        map[string]any `json:"bill_to"`
	Meta          map[string]any `json:"meta,omitempty"`
	Status        string         `json:"status"`
	IssuedAt      time.Time      `json:"issued_at"`
	DueAt         time.Time      `json:"due_at"`
	PaidAt        *time.Time     `json:"paid_at,omitempty"`
	PaidReference string         `json:"paid_reference,omitempty"`
	ClosedReason  string         `json:"closed_reason,omitempty"`
}

const invCols = `id, number, kind, plan_id, billing_interval, period_start, period_end, currency, lines, subtotal_kobo, vat_rate_bp, vat_kobo,
	total_kobo, bill_to, meta, status, issued_at, due_at, paid_at, COALESCE(paid_reference, ''), COALESCE(closed_reason, '')`

func scanInvoice(r pgx.CollectableRow) (Invoice, error) {
	var in Invoice
	var lines, billTo, meta []byte
	if err := r.Scan(&in.ID, &in.Number, &in.Kind, &in.PlanID, &in.Interval, &in.PeriodStart, &in.PeriodEnd, &in.Currency, &lines, &in.SubtotalKobo,
		&in.VATRateBP, &in.VATKobo, &in.TotalKobo, &billTo, &meta, &in.Status, &in.IssuedAt, &in.DueAt, &in.PaidAt, &in.PaidReference, &in.ClosedReason); err != nil {
		return in, err
	}
	if err := json.Unmarshal(lines, &in.Lines); err != nil {
		return in, err
	}
	if err := json.Unmarshal(billTo, &in.BillTo); err != nil {
		return in, err
	}
	return in, json.Unmarshal(meta, &in.Meta)
}

// Invoices lists a tenant's invoices, newest first.
func Invoices(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit int) ([]Invoice, error) {
	rows, err := tx.Query(ctx, `SELECT `+invCols+` FROM invoices WHERE tenant_id = $1 ORDER BY issued_at DESC, number DESC LIMIT $2`, tenant, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, scanInvoice)
	if out == nil {
		out = []Invoice{}
	}
	return out, err
}

// InvoiceByID reads one of a tenant's invoices.
func InvoiceByID(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Invoice, error) {
	rows, err := tx.Query(ctx, `SELECT `+invCols+` FROM invoices WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err != nil {
		return Invoice{}, err
	}
	in, err := pgx.CollectExactlyOneRow(rows, scanInvoice)
	if errors.Is(err, pgx.ErrNoRows) {
		return in, fmt.Errorf("invoice: %w", ErrNotFound)
	}
	return in, err
}

// issue numbers and writes an invoice. A zero total is paid on issue.
// It returns the invoice and the credit its lines left over.
func (s *Service) issue(ctx context.Context, tx pgx.Tx, sub *Subscription, kind string, p Plan, interval string, start, end time.Time,
	lines []Line, meta map[string]any) (Invoice, int64, error) {
	now := s.now()
	t := Compute(lines, s.Config.VATBasisPoints())
	var number, name string
	if err := tx.QueryRow(ctx, `SELECT taskiem_next_invoice_number($1, $2)`, s.Config.InvoicePrefix, now).Scan(&number); err != nil {
		return Invoice{}, 0, err
	}
	if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, sub.TenantID).Scan(&name); err != nil {
		return Invoice{}, 0, err
	}
	if meta == nil {
		meta = map[string]any{}
	}
	in := Invoice{ID: uuid.Must(uuid.NewV7()), Number: number, Kind: kind, PlanID: p.ID, Interval: interval, PeriodStart: start, PeriodEnd: end,
		Currency: "NGN", Lines: lines, SubtotalKobo: t.Subtotal, VATRateBP: t.VATBasisPoints, VATKobo: t.VAT, TotalKobo: t.Total,
		BillTo: map[string]any{"tenant_id": sub.TenantID, "name": name, "email": sub.BillingEmail}, Meta: meta, Status: "open",
		IssuedAt: now, DueAt: now.Add(time.Duration(s.Config.DueDays) * 24 * time.Hour)}
	if in.TotalKobo == 0 {
		in.Status, in.PaidAt = "paid", &now
	}
	linesRaw, _ := json.Marshal(lines)
	billTo, _ := json.Marshal(in.BillTo)
	metaRaw, _ := json.Marshal(meta)
	_, err := tx.Exec(ctx, `INSERT INTO invoices (id, tenant_id, number, kind, plan_id, billing_interval, period_start, period_end, lines, subtotal_kobo,
		vat_rate_bp, vat_kobo, total_kobo, bill_to, meta, status, issued_at, due_at, paid_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`,
		in.ID, sub.TenantID, in.Number, in.Kind, in.PlanID, in.Interval, in.PeriodStart, in.PeriodEnd, linesRaw, in.SubtotalKobo,
		in.VATRateBP, in.VATKobo, in.TotalKobo, billTo, metaRaw, in.Status, in.IssuedAt, in.DueAt, in.PaidAt)
	if err != nil {
		return in, 0, err
	}
	err = audit(ctx, tx, sub.TenantID, "system", "billing", "billing.invoice.issue", in.Number,
		map[string]any{"kind": kind, "plan": p.ID, "total_kobo": in.TotalKobo, "vat_kobo": in.VATKobo})
	return in, t.CreditLeft, err
}

func planLine(p Plan, interval string, start, end time.Time) Line {
	price := p.Price(interval)
	return Line{Kind: "plan", Description: fmt.Sprintf("%s plan (%s), %s to %s", p.Name, interval, start.Format("2 Jan 2006"), end.Format("2 Jan 2006")),
		Quantity: 1, UnitKobo: price, AmountKobo: price}
}

// overageLines invoices pass-through use in UTC months that have ended
// since the last invoiced one: WhatsApp templates beyond the allowance at
// Meta's cost, and AI tokens beyond the budget where the plan allows it.
// It advances sub.OverageBilledThrough.
func (s *Service) overageLines(ctx context.Context, tx pgx.Tx, sub *Subscription, p Plan, now time.Time) ([]Line, error) {
	thisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	from := time.Date(sub.CreatedAt.Year(), sub.CreatedAt.Month(), 1, 0, 0, 0, 0, time.UTC)
	if sub.OverageBilledThrough != nil {
		from = sub.OverageBilledThrough.AddDate(0, 1, 0)
	}
	if !from.Before(thisMonth) {
		return nil, nil
	}
	var lines []Line
	rows, err := tx.Query(ctx, `SELECT month, category, over_allowance FROM whatsapp_template_usage
		WHERE tenant_id = $1 AND month >= $2 AND month < $3 AND over_allowance > 0 ORDER BY month, category`, sub.TenantID, from, thisMonth)
	if err != nil {
		return nil, err
	}
	type wa struct {
		month    time.Time
		category string
		n        int64
	}
	was, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (wa, error) {
		var w wa
		return w, r.Scan(&w.month, &w.category, &w.n)
	})
	if err != nil {
		return nil, err
	}
	for _, w := range was {
		unit := s.Config.WhatsAppTemplateKobo[w.category]
		lines = append(lines, Line{Kind: "whatsapp_overage", Quantity: w.n, UnitKobo: unit, AmountKobo: unit * w.n,
			Description: fmt.Sprintf("WhatsApp %s templates beyond the allowance, %s (at Meta's cost)", w.category, w.month.Format("Jan 2006"))})
	}
	if rate := p.Overage.AIKoboPerMillionTokens; rate > 0 {
		lim, err := s.Store.LimitsFor(ctx, sub.TenantID)
		if err != nil {
			return nil, err
		}
		for m := from; m.Before(thisMonth); m = m.AddDate(0, 1, 0) {
			var used int64
			if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(total_tokens), 0)::bigint FROM ai_interactions WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3`,
				sub.TenantID, m, m.AddDate(0, 1, 0)).Scan(&used); err != nil {
				return nil, err
			}
			if over := used - lim.AIMonthlyTokens; lim.AIMonthlyTokens > 0 && over > 0 {
				amount := (over*rate + 999_999) / 1_000_000
				lines = append(lines, Line{Kind: "ai_overage", Quantity: over, UnitKobo: rate, AmountKobo: amount,
					Description: fmt.Sprintf("AI tokens beyond the budget, %s (per million tokens)", m.Format("Jan 2006"))})
			}
		}
	}
	last := thisMonth.AddDate(0, -1, 0)
	sub.OverageBilledThrough = &last
	return lines, nil
}

// ---- payments ----

// Payment is an attempt to pay an invoice.
type Payment struct {
	ID          uuid.UUID  `json:"id"`
	InvoiceID   uuid.UUID  `json:"invoice_id"`
	Provider    string     `json:"provider"`
	Reference   string     `json:"reference"`
	Method      string     `json:"method"`
	AmountKobo  int64      `json:"amount_kobo"`
	Status      string     `json:"status"`
	CheckoutURL string     `json:"checkout_url,omitempty"`
	Channel     string     `json:"channel,omitempty"`
	Failure     string     `json:"failure,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	VerifiedAt  *time.Time `json:"verified_at,omitempty"`
}

const payCols = `id, invoice_id, provider, reference, method, amount_kobo, status, COALESCE(checkout_url, ''), COALESCE(channel, ''), COALESCE(failure, ''), created_at, verified_at`

func scanPayment(r pgx.Row) (Payment, error) {
	var p Payment
	err := r.Scan(&p.ID, &p.InvoiceID, &p.Provider, &p.Reference, &p.Method, &p.AmountKobo, &p.Status, &p.CheckoutURL, &p.Channel, &p.Failure, &p.CreatedAt, &p.VerifiedAt)
	return p, err
}

// NewReference is a payment reference carrying its tenant: tkm-<tenant>-<random>
// (Paystack allows letters, digits, '-', '.' and '=').
func NewReference(tenant uuid.UUID) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "tkm-" + tenant.String() + "-" + hex.EncodeToString(b)
}

// TenantOfReference reads the tenant out of a reference.
func TenantOfReference(ref string) (uuid.UUID, bool) {
	if !strings.HasPrefix(ref, "tkm-") || len(ref) < 4+36+2 || ref[40] != '-' {
		return uuid.Nil, false
	}
	t, err := uuid.Parse(ref[4:40])
	return t, err == nil
}

func (s *Service) newPayment(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in Invoice, provider, method, by string) (Payment, error) {
	p := Payment{ID: uuid.Must(uuid.NewV7()), InvoiceID: in.ID, Provider: provider, Reference: NewReference(tenant), Method: method,
		AmountKobo: in.TotalKobo, Status: "pending", CreatedAt: s.now()}
	_, err := tx.Exec(ctx, `INSERT INTO billing_payments (id, tenant_id, invoice_id, provider, reference, method, amount_kobo, created_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, p.ID, tenant, p.InvoiceID, p.Provider, p.Reference, p.Method, p.AmountKobo, p.CreatedAt, by)
	return p, err
}

// settle records a provider's verdict on a payment (verified with the
// provider, never taken from a webhook body alone). A success of the
// invoiced amount pays the invoice and applies it; any other amount is
// refused (ErrAmountMismatch). Settling twice is a no-op.
func (s *Service) settle(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ref string, t Transaction, by string) (bool, error) {
	pay, err := scanPayment(tx.QueryRow(ctx, `SELECT `+payCols+` FROM billing_payments WHERE tenant_id = $1 AND reference = $2 FOR UPDATE`, tenant, ref))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("payment %s: %w", ref, ErrNotFound)
	}
	if err != nil {
		return false, err
	}
	if pay.Status == "success" || pay.Status == "mismatch" {
		return false, nil
	}
	now := s.now()
	switch t.Status {
	case TxPending:
		return false, nil
	case TxFailed, TxAbandoned:
		if _, err := tx.Exec(ctx, `UPDATE billing_payments SET status = $2, failure = NULLIF($3, ''), verified_at = $4 WHERE id = $1`, pay.ID, t.Status, t.Message, now); err != nil {
			return false, err
		}
		return false, audit(ctx, tx, tenant, "system", by, "billing.payment."+t.Status, ref, map[string]any{"message": t.Message})
	}
	if t.AmountKobo != pay.AmountKobo || (t.Currency != "" && t.Currency != "NGN") {
		if _, err := tx.Exec(ctx, `UPDATE billing_payments SET status = 'mismatch', failure = $2, verified_at = $3, provider_txn = NULLIF($4, '') WHERE id = $1`,
			pay.ID, fmt.Sprintf("paid %d %s, invoiced %d NGN", t.AmountKobo, t.Currency, pay.AmountKobo), now, t.ProviderID); err != nil {
			return false, err
		}
		if err := audit(ctx, tx, tenant, "system", by, "billing.payment.mismatch", ref,
			map[string]any{"paid_kobo": t.AmountKobo, "currency": t.Currency, "invoiced_kobo": pay.AmountKobo}); err != nil {
			return false, err
		}
		return false, ErrAmountMismatch
	}
	if _, err := tx.Exec(ctx, `UPDATE billing_payments SET status = 'success', channel = NULLIF($2, ''), provider_txn = NULLIF($3, ''), verified_at = $4 WHERE id = $1`,
		pay.ID, t.Channel, t.ProviderID, now); err != nil {
		return false, err
	}
	in, err := InvoiceByID(ctx, tx, tenant, pay.InvoiceID)
	if err != nil {
		return false, err
	}
	sub, err := loadSub(ctx, tx, tenant, true)
	if err != nil {
		return false, err
	}
	if in.Status == "void" || sub == nil {
		// Paid after it was voided (comped, superseded): record it for a
		// refund by the operator; nothing else changes.
		return false, audit(ctx, tx, tenant, "system", by, "billing.payment.unapplied", ref, map[string]any{"invoice": in.Number, "amount_kobo": t.AmountKobo})
	}
	if _, err := tx.Exec(ctx, `UPDATE invoices SET status = 'paid', paid_at = $2, paid_reference = $3 WHERE id = $1 AND status IN ('open', 'uncollectible')`, in.ID, now, ref); err != nil {
		return false, err
	}
	if a := t.Authorization; a != nil && a.Reusable {
		sub.Provider, sub.AuthorizationCode, sub.CardBrand, sub.CardLast4, sub.CardExp = pay.Provider, a.Code, a.Brand, a.Last4, a.Exp
	}
	was := sub.Status
	if err := s.apply(ctx, tx, sub, in, now); err != nil {
		return false, err
	}
	if err := saveSub(ctx, tx, sub, by); err != nil {
		return false, err
	}
	s.Store.ForgetLimits(tenant)
	if err := audit(ctx, tx, tenant, "system", by, "billing.invoice.paid", in.Number,
		map[string]any{"reference": ref, "total_kobo": in.TotalKobo, "channel": t.Channel, "from_status": was, "status": sub.Status}); err != nil {
		return false, err
	}
	return was == StatusPastDue || was == StatusDegraded, nil
}

// apply puts a paid invoice into effect on its subscription.
func (s *Service) apply(_ context.Context, _ pgx.Tx, sub *Subscription, in Invoice, now time.Time) error {
	if in.Kind == "upgrade" || in.Kind == "subscription" {
		// The invoice used the credit the subscription had; what its lines
		// left over is the credit now.
		sub.PlanID = in.PlanID
		sub.PendingPlanID, sub.PendingInterval = "", ""
		sub.CancelAtPeriodEnd = false
		sub.TrialEnd = nil
		sub.CreditKobo = metaInt(in.Meta, "credit_left_kobo")
	}
	// A renewal's plan is already the subscription's.
	sub.Activate(in.PeriodStart, in.PeriodEnd, in.Interval, now)
	sub.Schedule(s.Config)
	return nil
}

// payOrCheckout pays an invoice with the saved card when there is one
// (outside any transaction: providers are slow), else returns a hosted
// checkout. Settling happens in its own transaction.
func (s *Service) chargeSaved(ctx context.Context, tenant uuid.UUID, ref string) error {
	var sub *Subscription
	var pay Payment
	err := s.tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if sub, err = loadSub(ctx, tx, tenant, false); err != nil {
			return err
		}
		pay, err = scanPayment(tx.QueryRow(ctx, `SELECT `+payCols+` FROM billing_payments WHERE tenant_id = $1 AND reference = $2`, tenant, ref))
		return err
	})
	if err != nil {
		return err
	}
	if sub == nil || sub.AuthorizationCode == "" {
		return nil
	}
	prov, err := s.provider(pay.Provider)
	if err != nil {
		return err
	}
	email, err := s.payerEmail(ctx, tenant, sub)
	if err != nil {
		return err
	}
	t, err := prov.ChargeSaved(ctx, Charge{Reference: ref, AmountKobo: pay.AmountKobo, Email: email, Authorization: sub.AuthorizationCode})
	if err != nil {
		s.log().Warn("billing: card charge failed", "tenant", tenant, "reference", ref, "err", err)
		t = Transaction{Reference: ref, Status: TxFailed, Message: err.Error()}
	}
	var recovered bool
	err = s.tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		recovered, err = s.settle(ctx, tx, tenant, ref, t, "billing")
		return err
	})
	if err == nil && recovered {
		s.notify(ctx, tenant, "recovered", ref, "Payment received: service restored",
			"We received your payment and your Taskiem subscription is in good standing again. New runs are accepted as usual.")
	}
	return err
}

// payerEmail is the billing email, or the first owner's.
func (s *Service) payerEmail(ctx context.Context, tenant uuid.UUID, sub *Subscription) (string, error) {
	if sub != nil && sub.BillingEmail != "" {
		return sub.BillingEmail, nil
	}
	var to []string
	err := s.tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		to, err = ownerEmails(ctx, tx, tenant)
		return err
	})
	if err != nil {
		return "", err
	}
	if len(to) == 0 {
		return "", fmt.Errorf("%w: the tenant has no owner email to bill", ErrInvalid)
	}
	return to[0], nil
}

func ownerEmails(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT u.email FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.tenant_id = $1 AND m.role = 'owner' ORDER BY u.email`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// metaInt reads a whole number from invoice metadata, as issued (int64) or
// as read back from JSON (float64).
func metaInt(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

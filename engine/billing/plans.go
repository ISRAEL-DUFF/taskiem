// Package billing is Taskiem's plans, subscriptions, invoices and naira
// payments (spec 16, build plan Phase 4; decision 0017).
//
// Tenants pay a flat fee per plan and never per execution. A plan's limits
// are the base of a tenant's effective limits (runtime.Store with Billing
// on), its features gate the API, and pass-through costs (WhatsApp
// templates beyond the allowance, AI beyond the budget where a plan allows
// it) are invoiced at cost. With billing off (the default, and every
// self-hosted deployment) every tenant is on the internal plan: the
// platform defaults and every feature.
package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"sigs.k8s.io/yaml"

	"github.com/israel-duff/taskiem/engine/runtime"
)

// Features a plan unlocks. The API refuses the matching configuration
// (402, code plan_feature_required) on a plan without it.
const (
	FeatureSSO         = "sso"          // single sign-on (OIDC, SAML)
	FeatureSCIM        = "scim"         // SCIM provisioning
	FeatureCustomRoles = "custom_roles" // tenant-defined roles
	FeatureWhiteLabel  = "white_label"  // custom domains for embed apps
	FeatureBYOK        = "byok"         // bring your own key (Phase 4 enterprise)
	FeatureGit         = "git"          // Git-connected environments
	FeatureAI          = "ai"           // AI building and repair
	FeatureEmbedded    = "embedded"     // partner API: sub-tenants, embed apps
)

// AllFeatures in display order.
var AllFeatures = []string{FeatureSSO, FeatureSCIM, FeatureCustomRoles, FeatureWhiteLabel, FeatureBYOK, FeatureGit, FeatureAI, FeatureEmbedded}

// Tiers, cheapest first (spec 16.1). internal is the self-hosted plan.
var Tiers = []string{"internal", "starter", "growth", "business", "enterprise"}

// InternalPlanID is the plan of every tenant without a subscription: on a
// self-hosted deployment (billing off) that is every tenant.
const InternalPlanID = "self_hosted"

// Plan is one entry of the catalogue.
type Plan struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Tier        string          `json:"tier"`
	MonthlyKobo int64           `json:"monthly_kobo"`
	AnnualKobo  int64           `json:"annual_kobo"`
	Limits      map[string]any  `json:"limits"`
	Features    map[string]bool `json:"features"`
	// Partner caps for embedded tiers (spec 13.1): sub-tenants' shared run
	// quotas when an operator set none on the partner, and how many
	// sub-tenants the plan is sized for.
	Partner PartnerCaps `json:"partner"`
	// Overage rates for pass-through costs.
	Overage Overage `json:"overage"`
	Public  bool    `json:"public"` // offered self-serve
	Active  bool    `json:"active"` // false: retired
	Sort    int     `json:"sort"`
}

// PartnerCaps are an embedded plan's caps on a partner's sub-tenants.
type PartnerCaps struct {
	MaxSubtenants         int   `json:"max_subtenants,omitempty"`
	SubtenantRunsPerDay   int64 `json:"subtenant_runs_per_day,omitempty"`
	SubtenantRunsPerMonth int64 `json:"subtenant_runs_per_month,omitempty"`
}

// Overage prices pass-through use beyond a plan's allowance.
type Overage struct {
	// AIKoboPerMillionTokens: AI tokens beyond the monthly budget, billed
	// per million; 0 (the default) keeps the budget a hard cap.
	AIKoboPerMillionTokens int64 `json:"ai_kobo_per_million_tokens,omitempty"`
}

// Has reports whether the plan unlocks a feature.
func (p Plan) Has(feature string) bool { return p.Features[feature] }

// Price is the plan's price for an interval.
func (p Plan) Price(interval string) int64 {
	if interval == IntervalAnnual {
		return p.AnnualKobo
	}
	return p.MonthlyKobo
}

// TierRank orders tiers, cheapest first; unknown tiers rank last.
func TierRank(tier string) int {
	for i, t := range Tiers {
		if t == tier {
			return i
		}
	}
	return len(Tiers)
}

// RuntimeLimits are the plan's limits over the platform defaults.
func (p Plan) RuntimeLimits(defaults runtime.Limits) (runtime.Limits, error) {
	return defaults.With(p.Limits)
}

// InternalPlan is the self-hosted plan: the platform's default limits,
// every feature, no price. It is built in, never configured.
func InternalPlan() Plan {
	f := map[string]bool{}
	for _, k := range AllFeatures {
		f[k] = true
	}
	return Plan{ID: InternalPlanID, Name: "Self-hosted", Tier: "internal", Limits: map[string]any{}, Features: f, Active: true}
}

// Billing intervals.
const (
	IntervalMonthly = "monthly"
	IntervalAnnual  = "annual"
)

// Config is the billing configuration file (deploy/plans.yaml): the plan
// catalogue and the commercial terms around it.
type Config struct {
	// PlaceholderPrices marks the prices as not yet decided (decision B1,
	// docs/needs-people.md): the CLI warns and the billing page says so.
	PlaceholderPrices bool   `json:"placeholder_prices"`
	Currency          string `json:"currency"`
	// VATPercent is shown separately on every invoice (Nigerian VAT: 7.5).
	VATPercent float64 `json:"vat_percent"`
	// InvoicePrefix starts every invoice number (TKM-2026-000001).
	InvoicePrefix string `json:"invoice_prefix"`
	TrialDays     int    `json:"trial_days"`
	// TrialPlan is the plan a new tenant tries.
	TrialPlan string `json:"trial_plan"`
	// GraceDays a past-due subscription keeps working before it is
	// degraded (new runs refused).
	GraceDays int `json:"grace_days"`
	// DunningDays after falling past due when the card is retried and the
	// owners are reminded (0: at once).
	DunningDays []int `json:"dunning_days"`
	// DueDays an invoice is payable within.
	DueDays int `json:"due_days"`
	// WhatsAppTemplateKobo is Meta's charge per template message beyond the
	// allowance, by category, passed through at cost.
	WhatsAppTemplateKobo map[string]int64 `json:"whatsapp_template_kobo"`
	Plans                []Plan           `json:"plans"`
}

var (
	planIDRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)
	prefixRe = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)
)

// LoadConfig reads and validates a billing config file.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the operator's config file
	if err != nil {
		return nil, err
	}
	return ParseConfig(raw)
}

// ParseConfig parses and validates a billing config (YAML or JSON).
func ParseConfig(raw []byte) (*Config, error) {
	c := &Config{}
	if err := yaml.UnmarshalStrict(raw, c); err != nil {
		return nil, fmt.Errorf("billing config: %w", err)
	}
	c.defaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) defaults() {
	if c.Currency == "" {
		c.Currency = "NGN"
	}
	if c.InvoicePrefix == "" {
		c.InvoicePrefix = "TKM"
	}
	if c.DueDays == 0 {
		c.DueDays = 7
	}
	if c.DunningDays == nil {
		c.DunningDays = []int{0, 3, 5}
	}
	for i := range c.Plans {
		if c.Plans[i].Limits == nil {
			c.Plans[i].Limits = map[string]any{}
		}
		if c.Plans[i].Features == nil {
			c.Plans[i].Features = map[string]bool{}
		}
	}
}

// Validate checks the config: every plan's id, tier, prices, limit keys
// (each a runtime.LimitKeys key with a valid value) and feature names.
func (c *Config) Validate() error {
	var errs []error
	if c.Currency != "NGN" {
		errs = append(errs, fmt.Errorf("currency: only NGN is supported, not %q", c.Currency))
	}
	if c.VATPercent < 0 || c.VATPercent > 100 {
		errs = append(errs, fmt.Errorf("vat_percent: %v is not between 0 and 100", c.VATPercent))
	}
	if !prefixRe.MatchString(c.InvoicePrefix) {
		errs = append(errs, fmt.Errorf("invoice_prefix: %q is not 2-10 capital letters or digits", c.InvoicePrefix))
	}
	if c.TrialDays < 0 || c.GraceDays < 0 || c.DueDays < 0 {
		errs = append(errs, errors.New("trial_days, grace_days and due_days cannot be negative"))
	}
	for _, d := range c.DunningDays {
		if d < 0 {
			errs = append(errs, errors.New("dunning_days cannot be negative"))
		}
	}
	for k, v := range c.WhatsAppTemplateKobo {
		if k != "marketing" && k != "utility" && k != "authentication" {
			errs = append(errs, fmt.Errorf("whatsapp_template_kobo: unknown category %q (marketing, utility, authentication)", k))
		}
		if v < 0 {
			errs = append(errs, fmt.Errorf("whatsapp_template_kobo.%s: negative", k))
		}
	}
	seen := map[string]bool{}
	for _, p := range c.Plans {
		where := "plan " + p.ID
		if !planIDRe.MatchString(p.ID) {
			errs = append(errs, fmt.Errorf("plan id %q: lowercase letters, digits and _ (2-40)", p.ID))
		}
		if p.ID == InternalPlanID {
			errs = append(errs, fmt.Errorf("%s: the internal plan is built in", where))
		}
		if seen[p.ID] {
			errs = append(errs, fmt.Errorf("%s: listed twice", where))
		}
		seen[p.ID] = true
		if strings.TrimSpace(p.Name) == "" {
			errs = append(errs, fmt.Errorf("%s: needs a name", where))
		}
		if TierRank(p.Tier) == 0 || TierRank(p.Tier) >= len(Tiers) {
			errs = append(errs, fmt.Errorf("%s: tier %q is not one of starter, growth, business, enterprise", where, p.Tier))
		}
		if p.MonthlyKobo < 0 || p.AnnualKobo < 0 {
			errs = append(errs, fmt.Errorf("%s: prices cannot be negative", where))
		}
		for k, v := range p.Limits {
			if _, err := runtime.ParseLimit(k, limitString(v)); err != nil {
				errs = append(errs, fmt.Errorf("%s: limits: %w", where, err))
			}
		}
		for f := range p.Features {
			known := false
			for _, k := range AllFeatures {
				known = known || k == f
			}
			if !known {
				errs = append(errs, fmt.Errorf("%s: unknown feature %q (one of %s)", where, f, strings.Join(AllFeatures, ", ")))
			}
		}
		if p.Partner.MaxSubtenants < 0 || p.Partner.SubtenantRunsPerDay < 0 || p.Partner.SubtenantRunsPerMonth < 0 || p.Overage.AIKoboPerMillionTokens < 0 {
			errs = append(errs, fmt.Errorf("%s: partner caps and overage rates cannot be negative", where))
		}
	}
	if c.TrialPlan != "" && !seen[c.TrialPlan] {
		errs = append(errs, fmt.Errorf("trial_plan: %q is not a plan in this file", c.TrialPlan))
	}
	if len(c.Plans) == 0 {
		errs = append(errs, errors.New("plans: none"))
	}
	return errors.Join(errs...)
}

// Plan finds a plan of the config (or the internal plan) by id.
func (c *Config) Plan(id string) (Plan, bool) {
	if id == InternalPlanID {
		return InternalPlan(), true
	}
	for _, p := range c.Plans {
		if p.ID == id {
			return p, true
		}
	}
	return Plan{}, false
}

// VATBasisPoints is the VAT rate in basis points (7.5% = 750).
func (c *Config) VATBasisPoints() int { return int(c.VATPercent*100 + 0.5) }

// limitString renders a limit's value as an operator writes it (YAML
// numbers arrive as float64: no exponent).
func limitString(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

// normalizedLimits renders a plan's limits with each value parsed the way
// tenant_limits stores it.
func normalizedLimits(in map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range in {
		pv, err := runtime.ParseLimit(k, limitString(v))
		if err != nil {
			return nil, err
		}
		if pv != nil {
			out[k] = pv
		}
	}
	return out, nil
}

// Store writes the catalogue (and the internal plan) to the database, as
// `taskiem billing plans --load` does. Plans missing from the file are
// retired (active false), never deleted: subscribers keep them.
func Store(ctx context.Context, pool *pgxpool.Pool, c *Config, by string) error {
	plans := append([]Plan{InternalPlan()}, c.Plans...)
	plans[0].Public = false
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		ids := make([]string, 0, len(plans))
		for _, p := range plans {
			lim, err := normalizedLimits(p.Limits)
			if err != nil {
				return fmt.Errorf("plan %s: %w", p.ID, err)
			}
			doc := map[string]any{"id": p.ID, "name": p.Name, "tier": p.Tier, "monthly_kobo": p.MonthlyKobo, "annual_kobo": p.AnnualKobo,
				"limits": lim, "features": p.Features, "partner": p.Partner, "overage": p.Overage, "public": p.Public, "active": true, "sort": p.Sort}
			raw, _ := json.Marshal(doc)
			if _, err := tx.Exec(ctx, `SELECT taskiem_put_plan($1, $2)`, raw, by); err != nil {
				return fmt.Errorf("plan %s: %w", p.ID, err)
			}
			ids = append(ids, p.ID)
		}
		rows, err := tx.Query(ctx, `SELECT id, name, tier, monthly_kobo, annual_kobo, limits, features, partner, overage, public, sort FROM plans WHERE active AND NOT (id = ANY ($1))`, ids)
		if err != nil {
			return err
		}
		retired, err := pgx.CollectRows(rows, scanPlan)
		if err != nil {
			return err
		}
		for _, p := range retired {
			doc := map[string]any{"id": p.ID, "name": p.Name, "tier": p.Tier, "monthly_kobo": p.MonthlyKobo, "annual_kobo": p.AnnualKobo,
				"limits": p.Limits, "features": p.Features, "partner": p.Partner, "overage": p.Overage, "public": false, "active": false, "sort": p.Sort}
			raw, _ := json.Marshal(doc)
			if _, err := tx.Exec(ctx, `SELECT taskiem_put_plan($1, $2)`, raw, by); err != nil {
				return err
			}
		}
		return nil
	})
}

func scanPlan(r pgx.CollectableRow) (Plan, error) {
	var p Plan
	var limits, features, partner, overage []byte
	if err := r.Scan(&p.ID, &p.Name, &p.Tier, &p.MonthlyKobo, &p.AnnualKobo, &limits, &features, &partner, &overage, &p.Public, &p.Sort); err != nil {
		return p, err
	}
	p.Active = true
	for _, x := range []struct {
		raw []byte
		v   any
	}{{limits, &p.Limits}, {features, &p.Features}, {partner, &p.Partner}, {overage, &p.Overage}} {
		if err := json.Unmarshal(x.raw, x.v); err != nil {
			return p, err
		}
	}
	return p, nil
}

const planCols = `id, name, tier, monthly_kobo, annual_kobo, limits, features, partner, overage, public, sort`

// Catalogue reads the plans tenants may choose (public, active), cheapest
// first.
func Catalogue(ctx context.Context, tx pgx.Tx) ([]Plan, error) {
	rows, err := tx.Query(ctx, `SELECT `+planCols+` FROM plans WHERE public AND active`)
	if err != nil {
		return nil, err
	}
	plans, err := pgx.CollectRows(rows, scanPlan)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(plans, func(i, j int) bool {
		if plans[i].Sort != plans[j].Sort {
			return plans[i].Sort < plans[j].Sort
		}
		return plans[i].MonthlyKobo < plans[j].MonthlyKobo
	})
	return plans, nil
}

// PlanByID reads one plan, retired or not.
func PlanByID(ctx context.Context, tx pgx.Tx, id string) (Plan, error) {
	if id == InternalPlanID {
		return InternalPlan(), nil
	}
	rows, err := tx.Query(ctx, `SELECT `+planCols+`, active FROM plans WHERE id = $1`, id)
	if err != nil {
		return Plan{}, err
	}
	p, err := pgx.CollectExactlyOneRow(rows, func(r pgx.CollectableRow) (Plan, error) {
		var p Plan
		var limits, features, partner, overage []byte
		if err := r.Scan(&p.ID, &p.Name, &p.Tier, &p.MonthlyKobo, &p.AnnualKobo, &limits, &features, &partner, &overage, &p.Public, &p.Sort, &p.Active); err != nil {
			return p, err
		}
		for _, x := range []struct {
			raw []byte
			v   any
		}{{limits, &p.Limits}, {features, &p.Features}, {partner, &p.Partner}, {overage, &p.Overage}} {
			if err := json.Unmarshal(x.raw, x.v); err != nil {
				return p, err
			}
		}
		return p, nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Plan{}, fmt.Errorf("plan %q: %w", id, ErrNotFound)
	}
	return p, err
}

// ErrNotFound: no such plan, subscription, invoice or payment.
var ErrNotFound = errors.New("not found")

package storefront

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/publicsuffix"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/embedsites"
)

// A business's own domain (Phase 5, S6; docs/storefronts.md#your-own-domain,
// spec 6.6). The owner adds a domain it already has and proves it with a
// DNS TXT record at _taskiem-verify.<domain>, as embed sites are proven
// (embedsites.Prover). Once proven the domain is a public_hosts row
// serving the storefront; the storefront's other hosts redirect to the
// primary one. Domains are proven again every few hours (DomainChecker).
//
// Any number of organisations may add the same domain; the first to prove
// it serves it, and another can take it over only once it is unverified
// or removed.

// MaxDomains is how many domains a storefront may have.
const MaxDomains = 5

var (
	// ErrDomainExists refuses a domain the storefront already has.
	ErrDomainExists = errors.New("storefront: the domain is already added")
	// ErrDomainTaken refuses a domain another business's page serves.
	ErrDomainTaken = errors.New("storefront: the domain is in use by another page")
	// ErrDomainLimit refuses a domain past MaxDomains.
	ErrDomainLimit = errors.New("storefront: too many domains")
	// ErrDomainNotFound is a domain that does not exist (in the caller's scope).
	ErrDomainNotFound = errors.New("storefront: no such domain")
	// ErrNotProven says the TXT record does not hold the token yet.
	ErrNotProven = errors.New("storefront: the domain is not proven yet")
)

// Domain is one of the storefront's own domains.
type Domain struct {
	ID   uuid.UUID `json:"id"`
	Host string    `json:"domain"`
	// Twin is the www. twin of a registrable domain (or the domain of a
	// www. host), served too and redirecting to the primary.
	Twin           *string    `json:"twin"`
	Token          string     `json:"token"`
	Primary        bool       `json:"primary"`
	VerifiedAt     *time.Time `json:"verified_at"`
	CheckedAt      *time.Time `json:"checked_at,omitempty"`
	CheckFailures  int        `json:"check_failures,omitempty"`
	LastCheckError *string    `json:"last_check_error,omitempty"`
	UnverifiedAt   *time.Time `json:"unverified_at,omitempty"`
	CreatedBy      string     `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
}

// Hosts are the names the domain serves: the domain and its twin.
func (d Domain) Hosts() []string {
	if d.Twin != nil {
		return []string{d.Host, *d.Twin}
	}
	return []string{d.Host}
}

// ProofHost is where the TXT record goes (_taskiem-verify.<ProofHost>):
// the registrable domain of a www. pair, whose proof covers both, or the
// host itself.
func (d Domain) ProofHost() string {
	if d.Twin != nil && strings.HasPrefix(d.Host, "www.") {
		return *d.Twin
	}
	return d.Host
}

// Site is the domain as an embed site, so its proof is checked by the
// same DNS lookup (embedsites.Prover).
func (d Domain) Site() embedsites.Site {
	return embedsites.Site{ID: d.ID, Origin: "https://" + d.ProofHost(), Method: embedsites.DNS, Token: d.Token}
}

// DomainTarget is where owners point their domains (the operator's:
// TASKIEM_PUBLIC_CNAME and TASKIEM_PUBLIC_ADDRESSES).
type DomainTarget struct {
	// CNAME is the name a subdomain (shop.example.com, www.example.com)
	// points at.
	CNAME string `json:"cname,omitempty"`
	// Addresses are the public host's IPv4 and IPv6 addresses, for a
	// registrable domain (example.com), which cannot be a CNAME.
	Addresses []string `json:"addresses,omitempty"`
}

// Record is one DNS record the owner creates.
type Record struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
	// Purpose: "proof" (the TXT record that proves the domain, kept for
	// as long as it is used) or "point" (sends visitors to the page).
	Purpose string `json:"purpose"`
	Note    string `json:"note,omitempty"`
	// Short is the name as registrars ask for it: relative to the
	// registered domain, "@" for the domain itself (no-technical-steps N9).
	Short string `json:"short"`
}

// Records are what the owner creates for the domain: the proof, and the
// records that point each of its hosts at the public host.
func (d Domain) Records(t DomainTarget) []Record {
	out := d.records(t)
	for i := range out {
		out[i].Short = shortName(out[i].Name)
	}
	return out
}

// shortName is a record's name relative to its registered domain.
func shortName(name string) string {
	reg, err := publicsuffix.EffectiveTLDPlusOne(strings.TrimPrefix(name, "_taskiem-verify."))
	if err != nil || reg == "" {
		return name
	}
	if name == reg {
		return "@"
	}
	return strings.TrimSuffix(name, "."+reg)
}

func (d Domain) records(t DomainTarget) []Record {
	out := []Record{{Type: "TXT", Name: "_taskiem-verify." + d.ProofHost(), Value: d.Token, Purpose: "proof"}}
	for _, h := range d.Hosts() {
		if apex(h) {
			for _, a := range t.Addresses {
				typ := "A"
				if ip := net.ParseIP(a); ip != nil && ip.To4() == nil {
					typ = "AAAA"
				}
				out = append(out, Record{Type: typ, Name: h, Value: a, Purpose: "point"})
			}
			if len(t.Addresses) == 0 && t.CNAME != "" {
				out = append(out, Record{Type: "ALIAS", Name: h, Value: t.CNAME, Purpose: "point",
					Note: "A domain without a subdomain cannot be a CNAME: use your DNS provider's ALIAS, ANAME or CNAME flattening"})
			}
			continue
		}
		if t.CNAME != "" {
			out = append(out, Record{Type: "CNAME", Name: h, Value: t.CNAME, Purpose: "point"})
		}
	}
	return out
}

// apex reports whether host is a registrable domain (example.com,
// example.com.ng), as opposed to a subdomain of one.
func apex(host string) bool {
	r, err := publicsuffix.EffectiveTLDPlusOne(host)
	return err == nil && r == host
}

// NormaliseDomain turns what the owner typed (AdaKitchen.ng,
// https://www.adakitchen.ng/) into a host name and its twin: www.<domain>
// for a registrable domain, the domain for www.<domain>, none otherwise.
// Refused as for embed sites (embedsites.NormaliseOrigin): addresses,
// punycode, protected and government names and the platform's own hosts,
// and a public suffix itself (com.ng).
func NormaliseDomain(raw string, platformHosts []string) (host, twin string, err error) {
	origin, err := embedsites.NormaliseOrigin(raw, platformHosts)
	if err != nil {
		return "", "", fmt.Errorf("%w: %s", ErrInvalid, strings.TrimPrefix(strings.TrimPrefix(err.Error(), "embedsites: invalid site: "), "publicnames: "))
	}
	host = strings.TrimPrefix(origin, "https://")
	reg, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return "", "", fmt.Errorf("%w: %s is not a domain a business can own", ErrInvalid, host)
	}
	switch {
	case reg == host:
		twin = "www." + host
	case host == "www."+reg:
		twin = reg
	}
	for _, p := range platformHosts {
		p = strings.ToLower(strings.TrimSpace(p))
		if twin != "" && p != "" && (twin == p || strings.HasSuffix(twin, "."+p)) {
			return "", "", fmt.Errorf("%w: %s is the platform's own", ErrInvalid, twin)
		}
	}
	return host, twin, nil
}

// NewDomainToken is a domain's verification value.
func NewDomainToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "taskiem-domain-verify=" + base64.RawURLEncoding.EncodeToString(b)
}

const domainCols = `id, host, twin, token, is_primary, verified_at, checked_at, check_failures, last_check_error, unverified_at, created_by, created_at`

func scanDomain(r pgx.Row) (Domain, error) {
	var d Domain
	err := r.Scan(&d.ID, &d.Host, &d.Twin, &d.Token, &d.Primary, &d.VerifiedAt, &d.CheckedAt, &d.CheckFailures, &d.LastCheckError,
		&d.UnverifiedAt, &d.CreatedBy, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Domain{}, ErrDomainNotFound
	}
	return d, err
}

// AddDomain adds an unproven domain to the tenant's storefront in tx.
func AddDomain(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, host, twin, actor string) (Domain, error) {
	var sf uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM storefronts WHERE tenant_id = $1`, tenant).Scan(&sf); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Domain{}, ErrNotFound
		}
		return Domain{}, err
	}
	var n int
	var clash bool
	if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(bool_or(host = ANY ($2) OR twin = ANY ($2)), false) FROM storefront_domains WHERE storefront_id = $1`,
		sf, []string{host, twin}).Scan(&n, &clash); err != nil {
		return Domain{}, err
	}
	if clash {
		return Domain{}, fmt.Errorf("%w: %s", ErrDomainExists, host)
	}
	if n >= MaxDomains {
		return Domain{}, fmt.Errorf("%w: at most %d", ErrDomainLimit, MaxDomains)
	}
	var tw *string
	if twin != "" {
		tw = &twin
	}
	return scanDomain(tx.QueryRow(ctx, `INSERT INTO storefront_domains (id, tenant_id, storefront_id, host, twin, token, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+domainCols, uuid.Must(uuid.NewV7()), tenant, sf, host, tw, NewDomainToken(), actor))
}

// ListDomains returns the tenant's storefront's domains.
func ListDomains(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]Domain, error) {
	rows, err := tx.Query(ctx, `SELECT `+domainCols+` FROM storefront_domains WHERE tenant_id = $1 ORDER BY is_primary DESC, created_at`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Domain, error) { return scanDomain(r) })
}

// GetDomain returns one domain.
func GetDomain(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Domain, error) {
	return scanDomain(tx.QueryRow(ctx, `SELECT `+domainCols+` FROM storefront_domains WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

// VerifyDomain serves a proven domain in tx: its hosts become the
// storefront's (ErrDomainTaken when another page serves it), and it is
// the primary domain when the storefront has no other verified one.
func VerifyDomain(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Domain, error) {
	d, err := GetDomain(ctx, tx, tenant, id)
	if err != nil {
		return Domain{}, err
	}
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT taskiem_storefront_claim_domain($1)`, id).Scan(&ok); err != nil {
		return Domain{}, err
	}
	if !ok {
		return Domain{}, fmt.Errorf("%w: %s", ErrDomainTaken, d.Host)
	}
	if _, err := tx.Exec(ctx, `UPDATE storefront_domains SET verified_at = now(), checked_at = now(), check_failures = 0, failing_since = NULL,
		last_check_error = NULL, unverified_at = NULL WHERE tenant_id = $1 AND id = $2`, tenant, id); err != nil {
		return Domain{}, err
	}
	var hasPrimary bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storefront_domains o JOIN storefront_domains d ON d.storefront_id = o.storefront_id
		WHERE d.id = $1 AND o.id <> $1 AND o.is_primary AND o.verified_at IS NOT NULL)`, id).Scan(&hasPrimary); err != nil {
		return Domain{}, err
	}
	if !hasPrimary {
		if err := setPrimary(ctx, tx, tenant, id); err != nil {
			return Domain{}, err
		}
	}
	return GetDomain(ctx, tx, tenant, id)
}

func setPrimary(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) error {
	if _, err := tx.Exec(ctx, `UPDATE storefront_domains SET is_primary = false WHERE tenant_id = $1 AND id <> $2 AND is_primary`, tenant, id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE storefront_domains SET is_primary = true WHERE tenant_id = $1 AND id = $2`, tenant, id)
	return err
}

// SetPrimaryDomain makes a verified domain the one the storefront's other
// hosts redirect to.
func SetPrimaryDomain(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Domain, error) {
	d, err := GetDomain(ctx, tx, tenant, id)
	if err != nil {
		return Domain{}, err
	}
	if d.VerifiedAt == nil {
		return Domain{}, fmt.Errorf("%w: %s is not verified yet", ErrNotProven, d.Host)
	}
	if err := setPrimary(ctx, tx, tenant, id); err != nil {
		return Domain{}, err
	}
	d.Primary = true
	return d, nil
}

// RemoveDomain deletes a domain in tx and stops serving it.
func RemoveDomain(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Domain, error) {
	var sf uuid.UUID
	var d Domain
	err := tx.QueryRow(ctx, `DELETE FROM storefront_domains WHERE tenant_id = $1 AND id = $2 RETURNING storefront_id, host, twin`, tenant, id).
		Scan(&sf, &d.Host, &d.Twin)
	if errors.Is(err, pgx.ErrNoRows) {
		return Domain{}, ErrDomainNotFound
	}
	if err != nil {
		return Domain{}, err
	}
	d.ID = id
	_, err = tx.Exec(ctx, `SELECT taskiem_storefront_release_domain($1, $2)`, sf, d.Hosts())
	return d, err
}

// DomainChecker proves verified domains again (the scheduler role runs
// it), as embedsites.Checker does websites: every Every (6h); Failures
// failed checks in a row (4) over at least Grace (24h) unverify a domain,
// which stops serving at once; a check that says nothing is not counted.
type DomainChecker struct {
	Pool     *pgxpool.Pool
	Prover   embedsites.Proofs
	Every    time.Duration
	Failures int
	Grace    time.Duration
	// Interval is how often the checker looks for due domains (10m).
	Interval time.Duration
	// PublicURL is the web app's address, for the link in the owner's
	// alert.
	PublicURL string
	Logger    *slog.Logger
	Now       func() time.Time
}

func (c *DomainChecker) settings() (time.Duration, int, time.Duration) {
	every, failures, grace := c.Every, c.Failures, c.Grace
	if every <= 0 {
		every = 6 * time.Hour
	}
	if failures <= 0 {
		failures = 4
	}
	if grace <= 0 {
		grace = 24 * time.Hour
	}
	return every, failures, grace
}

func (c *DomainChecker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *DomainChecker) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

// Run checks until ctx ends.
func (c *DomainChecker) Run(ctx context.Context) error {
	every := c.Interval
	if every <= 0 {
		every = 10 * time.Minute
	}
	for {
		if err := c.Tick(ctx); err != nil && ctx.Err() == nil {
			c.log().Error("storefront domain checks", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(every):
		}
	}
}

// Tick checks every tenant's due domains.
func (c *DomainChecker) Tick(ctx context.Context) error {
	every, _, _ := c.settings()
	rows, err := c.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_storefront_domain_tenants($1)`, c.now().Add(-every))
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tenants {
		if err := c.Check(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", t, err))
		}
	}
	return errors.Join(errs...)
}

// Check proves a tenant's due domains again.
func (c *DomainChecker) Check(ctx context.Context, tenant uuid.UUID) error {
	every, failures, grace := c.settings()
	now := c.now()
	var due []Domain
	if err := db.InTenantTx(ctx, c.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+domainCols+` FROM storefront_domains WHERE tenant_id = $1 AND verified_at IS NOT NULL
			AND COALESCE(checked_at, verified_at) <= $2 ORDER BY checked_at NULLS FIRST LIMIT 100`, tenant, now.Add(-every))
		if err != nil {
			return err
		}
		due, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Domain, error) { return scanDomain(r) })
		return err
	}); err != nil {
		return err
	}
	prover := c.Prover
	if prover == nil {
		prover = &embedsites.Prover{}
	}
	var errs []error
	for _, d := range due {
		ok, inconclusive, perr := prover.Prove(ctx, d.Site())
		if inconclusive {
			c.log().Warn("storefront domain check inconclusive", "tenant", tenant, "domain", d.Host, "err", perr)
			continue
		}
		err := db.InTenantTx(ctx, c.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			if ok {
				_, err := tx.Exec(ctx, `UPDATE storefront_domains SET checked_at = $3, check_failures = 0, failing_since = NULL, last_check_error = NULL
					WHERE tenant_id = $1 AND id = $2 AND verified_at IS NOT NULL`, tenant, d.ID, now)
				return err
			}
			reason := clipErr(perr)
			var n int
			var since time.Time
			var sf uuid.UUID
			err := tx.QueryRow(ctx, `UPDATE storefront_domains SET checked_at = $3, check_failures = check_failures + 1,
				failing_since = COALESCE(failing_since, $3), last_check_error = $4
				WHERE tenant_id = $1 AND id = $2 AND verified_at IS NOT NULL RETURNING check_failures, failing_since, storefront_id`,
				tenant, d.ID, now, reason).Scan(&n, &since, &sf)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil // removed or unverified meanwhile
			}
			if err != nil || n < failures || now.Sub(since) < grace {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE storefront_domains SET verified_at = NULL, unverified_at = $3 WHERE tenant_id = $1 AND id = $2`,
				tenant, d.ID, now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `SELECT taskiem_storefront_release_domain($1, $2)`, sf, d.Hosts()); err != nil {
				return err
			}
			c.log().Warn("storefront domain unverified", "tenant", tenant, "domain", d.Host, "failures", n, "reason", reason)
			detail, _ := json.Marshal(map[string]any{"failures": n, "failing_since": since.UTC(), "reason": reason})
			if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'storefront-domain-checker', 'storefront_domain.unverified', $2, $3)`,
				tenant, d.Host, detail); err != nil {
				return err
			}
			// The owner hears of it in chat, with what to check
			// (no-technical-steps N9).
			_, err = alerts.TellOwners(ctx, tx, tenant, alerts.Alert{Kind: alerts.DomainUnverified, Dedup: d.Host + ":" + now.UTC().Format(time.RFC3339),
				Title: "Your page stopped showing on " + d.Host,
				Body: "Taskiem could not find the TXT record _taskiem-verify." + d.ProofHost() + " for over a day, so " + d.Host +
					" no longer shows your page. Check the record is still there at the company you bought the domain from, then press Check now on your page's settings. Your page still works at its Taskiem address.",
				Link: link(c.PublicURL, "/storefront"), Detail: map[string]any{"domain": d.Host}})
			return err
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func clipErr(err error) string {
	if err == nil {
		return "not proven"
	}
	s := err.Error()
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// link is a web app path as a full address, or "" without one.
func link(publicURL, path string) string {
	if publicURL == "" {
		return ""
	}
	return strings.TrimRight(publicURL, "/") + path
}

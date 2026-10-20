// Package embedsites keeps the websites an organisation has proven it
// controls, which may embed its storefront and forms (Phase 5, S1;
// docs/embed-sites.md). The public host puts their origins in its /embed/
// pages' frame-ancestors; every other public page may not be framed.
//
// A site is proven by a DNS TXT record at _taskiem-verify.<host> or by a
// file at https://<host>/.well-known/taskiem-site.txt holding its token,
// and proven again every few hours. A site that fails Failures checks in a
// row over at least Grace is unverified, so a domain that lapses or
// changes hands stops framing the business's pages. A check that says
// nothing (a DNS timeout, a site that cannot be reached for a moment) is
// not counted.
package embedsites

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/publicnames"
)

// Methods of proof.
const (
	DNS  = "dns"
	File = "file"
)

// WellKnownPath is the file a site serves for the file method.
const WellKnownPath = "/.well-known/taskiem-site.txt"

// MaxSites is how many sites an organisation may add by default; the
// plan's embed_sites sets it (runtime.Limits.EmbedSites).
const MaxSites = 10

var (
	// ErrInvalid wraps a refused origin or method.
	ErrInvalid = errors.New("embedsites: invalid site")
	// ErrNotProven says the record or file does not hold the token yet.
	ErrNotProven = errors.New("embedsites: not proven yet")
	// ErrLimit refuses a site past MaxSites.
	ErrLimit = errors.New("embedsites: too many sites")
	// ErrExists refuses a site already added.
	ErrExists = errors.New("embedsites: the site is already added")
	// ErrNotFound is a site that does not exist (in the caller's scope).
	ErrNotFound = errors.New("embedsites: no such site")
)

// Site is one website.
type Site struct {
	ID             uuid.UUID  `json:"id"`
	Origin         string     `json:"origin"`
	Method         string     `json:"method"`
	Token          string     `json:"token"`
	VerifiedAt     *time.Time `json:"verified_at"`
	CheckedAt      *time.Time `json:"checked_at,omitempty"`
	CheckFailures  int        `json:"check_failures,omitempty"`
	LastCheckError *string    `json:"last_check_error,omitempty"`
	UnverifiedAt   *time.Time `json:"unverified_at,omitempty"`
	CreatedBy      string     `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
}

// Host is the site's host name.
func (s Site) Host() string { return strings.TrimPrefix(s.Origin, "https://") }

// Instructions say what to publish to prove the site.
func (s Site) Instructions() map[string]string {
	if s.Method == DNS {
		return map[string]string{"record_type": "TXT", "record_name": "_taskiem-verify." + s.Host(), "record_value": s.Token}
	}
	return map[string]string{"file_url": s.Origin + WellKnownPath, "file_contents": s.Token}
}

// NormaliseOrigin turns what someone typed (adakitchen.ng,
// https://www.adakitchen.ng/shop) into an origin: https, a host name and
// no path or port. Hosts the platform owns are refused, as are addresses,
// punycode names and protected names (publicnames.CheckDomain).
func NormaliseOrigin(raw string, platformHosts []string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("%w: %q is not a website address", ErrInvalid, raw)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("%w: the site must use https", ErrInvalid)
	}
	if u.Port() != "" && u.Port() != "443" {
		return "", fmt.Errorf("%w: a site on port %s cannot embed pages", ErrInvalid, u.Port())
	}
	host, err := publicnames.CheckDomain(u.Hostname())
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	for _, p := range platformHosts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" && (host == p || strings.HasSuffix(host, "."+p)) {
			return "", fmt.Errorf("%w: %s is the platform's own", ErrInvalid, host)
		}
	}
	return "https://" + host, nil
}

// Prover checks a site's proof.
type Prover struct {
	// LookupTXT reads TXT records; nil uses the system resolver.
	LookupTXT func(ctx context.Context, name string) ([]string, error)
	// Guard fetches the file through the egress guard: public addresses
	// only, the site's own host only.
	Guard *egress.Guard
	// Client, when set, fetches the file instead (tests).
	Client *http.Client
}

// Proofs checks sites' proofs; *Prover is the real one.
type Proofs interface {
	Prove(ctx context.Context, s Site) (ok, inconclusive bool, err error)
}

// Prove reports whether the site's record or file holds its token. An
// error with inconclusive true says nothing about the proof.
func (p *Prover) Prove(ctx context.Context, s Site) (ok, inconclusive bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if s.Method == DNS {
		lookup := p.LookupTXT
		if lookup == nil {
			lookup = net.DefaultResolver.LookupTXT
		}
		records, err := lookup(ctx, "_taskiem-verify."+s.Host())
		if err != nil {
			var de *net.DNSError
			if errors.As(err, &de) && de.IsNotFound {
				return false, false, fmt.Errorf("no TXT record _taskiem-verify.%s", s.Host())
			}
			return false, true, err
		}
		if slices.Contains(records, s.Token) {
			return true, false, nil
		}
		return false, false, fmt.Errorf("the TXT record _taskiem-verify.%s does not hold %s", s.Host(), s.Token)
	}
	c := p.Client
	if c == nil {
		u, err := url.Parse(s.Origin)
		if err != nil {
			return false, false, err
		}
		g := p.Guard
		if g == nil {
			g = &egress.Guard{}
		}
		c = g.Client(egress.Policy{Tenant: "embed_site", Hosts: []string{u.Hostname()}, Purpose: "embed_site:verify"}, 10*time.Second)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Origin+WellKnownPath, nil)
	if err != nil {
		return false, false, err
	}
	resp, err := c.Do(req) //nolint:gosec // the site's own host, through the egress guard (public addresses only)
	if err != nil {
		return false, !errors.Is(err, egress.ErrDenied), fmt.Errorf("%s could not be fetched: %w", s.Origin+WellKnownPath, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return false, true, fmt.Errorf("%s answered %d", s.Origin+WellKnownPath, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return false, false, fmt.Errorf("%s answered %d", s.Origin+WellKnownPath, resp.StatusCode)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == s.Token {
			return true, false, nil
		}
	}
	return false, false, fmt.Errorf("%s does not hold %s", s.Origin+WellKnownPath, s.Token)
}

// NewToken is a site's verification value.
func NewToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "taskiem-site-verify=" + base64.RawURLEncoding.EncodeToString(b)
}

// Add adds an unverified site in tx (the tenant's scope); max is how many
// the organisation may have (0: no limit).
func Add(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, origin, method, actor string, max int) (Site, error) {
	if method != DNS && method != File {
		return Site{}, fmt.Errorf("%w: method must be dns or file", ErrInvalid)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM embed_sites WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		return Site{}, err
	}
	if max > 0 && n >= max {
		return Site{}, fmt.Errorf("%w: your plan allows %d", ErrLimit, max)
	}
	s := Site{ID: uuid.Must(uuid.NewV7()), Origin: origin, Method: method, Token: NewToken(), CreatedBy: actor}
	err := tx.QueryRow(ctx, `INSERT INTO embed_sites (id, tenant_id, origin, method, token, created_by) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, origin) DO NOTHING RETURNING created_at`, s.ID, tenant, origin, method, s.Token, actor).Scan(&s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Site{}, fmt.Errorf("%w: %s", ErrExists, origin)
	}
	return s, err
}

const columns = `id, origin, method, token, verified_at, checked_at, check_failures, last_check_error, unverified_at, created_by, created_at`

func scan(r pgx.Row) (Site, error) {
	var s Site
	err := r.Scan(&s.ID, &s.Origin, &s.Method, &s.Token, &s.VerifiedAt, &s.CheckedAt, &s.CheckFailures, &s.LastCheckError, &s.UnverifiedAt, &s.CreatedBy, &s.CreatedAt)
	return s, err
}

// List returns the tenant's sites.
func List(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]Site, error) {
	rows, err := tx.Query(ctx, `SELECT `+columns+` FROM embed_sites WHERE tenant_id = $1 ORDER BY origin`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Site, error) { return scan(r) })
}

// Get returns one site.
func Get(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Site, error) {
	s, err := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM embed_sites WHERE tenant_id = $1 AND id = $2`, tenant, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Site{}, ErrNotFound
	}
	return s, err
}

// MarkVerified records a successful proof in tx.
func MarkVerified(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `UPDATE embed_sites SET verified_at = now(), checked_at = now(), check_failures = 0, failing_since = NULL,
		last_check_error = NULL, unverified_at = NULL WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// Remove deletes a site in tx and returns it.
func Remove(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Site, error) {
	s, err := scan(tx.QueryRow(ctx, `DELETE FROM embed_sites WHERE tenant_id = $1 AND id = $2 RETURNING `+columns, tenant, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Site{}, ErrNotFound
	}
	return s, err
}

// Checker proves verified sites again (the scheduler role runs it).
type Checker struct {
	Pool   *pgxpool.Pool
	Prover Proofs
	// Every is how often each site is checked (default 6h); Failures
	// checks in a row (default 4) over at least Grace (default 24h)
	// unverify it.
	Every    time.Duration
	Failures int
	Grace    time.Duration
	// Interval is how often the checker looks for due sites (default 10m).
	Interval time.Duration
	Logger   *slog.Logger
	Now      func() time.Time
}

func (c *Checker) settings() (time.Duration, int, time.Duration) {
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

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Checker) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

// Run checks until ctx ends.
func (c *Checker) Run(ctx context.Context) error {
	every := c.Interval
	if every <= 0 {
		every = 10 * time.Minute
	}
	for {
		if err := c.Tick(ctx); err != nil && ctx.Err() == nil {
			c.log().Error("embed site checks", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(every):
		}
	}
}

// Tick checks every tenant's due sites.
func (c *Checker) Tick(ctx context.Context) error {
	every, _, _ := c.settings()
	rows, err := c.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_embed_site_tenants($1)`, c.now().Add(-every))
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

// Check proves a tenant's due sites again.
func (c *Checker) Check(ctx context.Context, tenant uuid.UUID) error {
	every, failures, grace := c.settings()
	now := c.now()
	var due []Site
	if err := db.InTenantTx(ctx, c.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+columns+` FROM embed_sites WHERE tenant_id = $1 AND verified_at IS NOT NULL
			AND COALESCE(checked_at, verified_at) <= $2 ORDER BY checked_at NULLS FIRST LIMIT 100`, tenant, now.Add(-every))
		if err != nil {
			return err
		}
		due, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Site, error) { return scan(r) })
		return err
	}); err != nil {
		return err
	}
	var prover = c.Prover
	if prover == nil {
		prover = &Prover{}
	}
	var errs []error
	for _, s := range due {
		ok, inconclusive, perr := prover.Prove(ctx, s)
		if inconclusive {
			c.log().Warn("embed site check inconclusive", "tenant", tenant, "origin", s.Origin, "err", perr)
			continue
		}
		err := db.InTenantTx(ctx, c.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			if ok {
				_, err := tx.Exec(ctx, `UPDATE embed_sites SET checked_at = $3, check_failures = 0, failing_since = NULL, last_check_error = NULL
					WHERE tenant_id = $1 AND id = $2 AND verified_at IS NOT NULL`, tenant, s.ID, now)
				return err
			}
			reason := clip(perr)
			var n int
			var since time.Time
			err := tx.QueryRow(ctx, `UPDATE embed_sites SET checked_at = $3, check_failures = check_failures + 1,
				failing_since = COALESCE(failing_since, $3), last_check_error = $4
				WHERE tenant_id = $1 AND id = $2 AND verified_at IS NOT NULL RETURNING check_failures, failing_since`,
				tenant, s.ID, now, reason).Scan(&n, &since)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil // removed or unverified meanwhile
			}
			if err != nil || n < failures || now.Sub(since) < grace {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE embed_sites SET verified_at = NULL, unverified_at = $3 WHERE tenant_id = $1 AND id = $2`,
				tenant, s.ID, now); err != nil {
				return err
			}
			c.log().Warn("embed site unverified", "tenant", tenant, "origin", s.Origin, "failures", n, "reason", reason)
			detail, _ := json.Marshal(map[string]any{"failures": n, "failing_since": since.UTC(), "reason": reason})
			_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'embed-site-checker', 'embed_site.unverified', $2, $3)`,
				tenant, s.Origin, detail)
			return err
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func clip(err error) string {
	if err == nil {
		return "not proven"
	}
	s := err.Error()
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

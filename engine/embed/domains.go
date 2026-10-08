package embed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
)

// EventDomainUnverified is sent to the app that owned a custom domain when
// re-verification gives up on it.
const EventDomainUnverified = "domain.unverified"

// DomainChecker re-verifies verified custom domains (the scheduler role
// runs it; docs/embedding.md#custom-domains). A domain whose
// _taskiem-verify TXT record no longer holds its token, in Failures
// checks in a row spanning at least Grace, is unverified: it stops
// routing to the app (replicas' host caches within 30 seconds), the app's
// webhook gets domain.unverified, and the partner's audit chain records
// it. A failed lookup that says nothing about the record (a timeout, a
// resolver that cannot be reached) is not counted.
type DomainChecker struct {
	Pool *pgxpool.Pool
	// Lookup reads TXT records; nil uses the system resolver.
	Lookup func(ctx context.Context, name string) ([]string, error)
	// Every is how often each domain is checked again (default 6h).
	Every time.Duration
	// Failures is how many failed checks in a row unverify (default 4).
	Failures int
	// Grace is how long a domain must have been failing (default 24h).
	Grace time.Duration
	// Interval is how often the checker looks for due domains (default 10m).
	Interval time.Duration
	Logger   *slog.Logger
	// Now is the clock (tests).
	Now func() time.Time
	// Unverified, when set, is told each domain unverified (so a replica
	// can drop it from its host cache at once).
	Unverified func(domain string)
}

func (c *DomainChecker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *DomainChecker) log() *slog.Logger {
	if c.Logger == nil {
		return slog.Default()
	}
	return c.Logger
}

func (c *DomainChecker) settings() (every time.Duration, failures int, grace time.Duration) {
	every, failures, grace = c.Every, c.Failures, c.Grace
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

// Run checks until ctx ends.
func (c *DomainChecker) Run(ctx context.Context) error {
	every := c.Interval
	if every <= 0 {
		every = 10 * time.Minute
	}
	for {
		if err := c.Tick(ctx); err != nil && ctx.Err() == nil {
			c.log().Error("custom domain checks", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(every):
		}
	}
}

// Tick checks every partner's due domains.
func (c *DomainChecker) Tick(ctx context.Context) error {
	rows, err := c.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_partner_tenants()`)
	if err != nil {
		return err
	}
	partners, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range partners {
		if err := c.Check(ctx, p); err != nil {
			errs = append(errs, fmt.Errorf("partner %s: %w", p, err))
		}
	}
	return errors.Join(errs...)
}

type dueDomain struct {
	domain, token string
	app           uuid.UUID
}

// Check re-verifies a partner's verified domains that are due.
func (c *DomainChecker) Check(ctx context.Context, partner uuid.UUID) error {
	every, _, _ := c.settings()
	now := c.now()
	var due []dueDomain
	err := db.InTenantTx(ctx, c.Pool, []uuid.UUID{partner}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT domain, token, app_id FROM embed_app_domains
			WHERE tenant_id = $1 AND verified_at IS NOT NULL AND COALESCE(checked_at, verified_at) <= $2 ORDER BY checked_at NULLS FIRST LIMIT 100`,
			partner, now.Add(-every))
		if err != nil {
			return err
		}
		due, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (dueDomain, error) {
			var d dueDomain
			return d, r.Scan(&d.domain, &d.token, &d.app)
		})
		return err
	})
	if err != nil {
		return err
	}
	lookup := c.Lookup
	if lookup == nil {
		lookup = net.DefaultResolver.LookupTXT
	}
	var errs []error
	for _, d := range due {
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		records, lerr := lookup(lctx, "_taskiem-verify."+d.domain)
		cancel()
		if lerr != nil && inconclusive(lerr) {
			c.log().Warn("custom domain check inconclusive", "partner", partner, "domain", d.domain, "err", lerr)
			continue
		}
		if err := c.record(ctx, partner, d, lerr == nil && slices.Contains(records, d.token), lerr); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// inconclusive reports a lookup error that says nothing about the record.
func inconclusive(err error) bool {
	var de *net.DNSError
	if errors.As(err, &de) {
		return !de.IsNotFound && (de.IsTimeout || de.IsTemporary)
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// record stores a check's outcome and unverifies a domain that has failed
// long enough.
func (c *DomainChecker) record(ctx context.Context, partner uuid.UUID, d dueDomain, ok bool, lerr error) error {
	_, failures, grace := c.settings()
	now := c.now()
	unverified := false
	err := db.InTenantTx(ctx, c.Pool, []uuid.UUID{partner}, func(tx pgx.Tx) error {
		if ok {
			_, err := tx.Exec(ctx, `UPDATE embed_app_domains SET checked_at = $3, check_failures = 0, failing_since = NULL, last_check_error = NULL
				WHERE tenant_id = $1 AND domain = $2 AND verified_at IS NOT NULL`, partner, d.domain, now)
			return err
		}
		reason := "the TXT record _taskiem-verify." + d.domain + " no longer holds the verification value"
		if lerr != nil {
			reason = "the TXT record _taskiem-verify." + d.domain + " was not found"
		}
		var n int
		var since time.Time
		if err := tx.QueryRow(ctx, `UPDATE embed_app_domains SET checked_at = $3, check_failures = check_failures + 1,
			failing_since = COALESCE(failing_since, $3), last_check_error = $4
			WHERE tenant_id = $1 AND domain = $2 AND verified_at IS NOT NULL RETURNING check_failures, failing_since`,
			partner, d.domain, now, reason).Scan(&n, &since); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil // removed or unverified meanwhile
			}
			return err
		}
		if n < failures || now.Sub(since) < grace {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE embed_app_domains SET verified_at = NULL, unverified_at = $3 WHERE tenant_id = $1 AND domain = $2`,
			partner, d.domain, now); err != nil {
			return err
		}
		detail, _ := json.Marshal(map[string]any{"app": d.app, "failures": n, "failing_since": since.UTC(), "reason": reason})
		if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'domain-checker', 'embed_app.domain_unverified', $2, $3)`,
			partner, d.domain, detail); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"domain": d.domain, "app_id": d.app, "failures": n, "failing_since": since.UTC(), "reason": reason})
		if _, err := tx.Exec(ctx, `INSERT INTO partner_webhook_deliveries (id, tenant_id, app_id, event, dedup_key, payload)
			SELECT gen_random_uuid(), a.tenant_id, a.id, $3, $4, $5 FROM embed_apps a
			 WHERE a.tenant_id = $1 AND a.id = $2 AND a.webhook_url IS NOT NULL
			ON CONFLICT (app_id, dedup_key) DO NOTHING`, partner, d.app, EventDomainUnverified,
			EventDomainUnverified+":"+d.domain+":"+now.UTC().Format(time.RFC3339), payload); err != nil {
			return err
		}
		unverified = true
		return nil
	})
	if err == nil && unverified {
		c.log().Warn("custom domain unverified", "partner", partner, "domain", d.domain)
		if c.Unverified != nil {
			c.Unverified(d.domain)
		}
	}
	return err
}

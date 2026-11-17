package discover

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
)

// Sweeper keeps the index in step with what the businesses change, and
// tells each business listed by default, once, that it is.
type Sweeper struct {
	Pool *pgxpool.Pool
	// PreviewDomain makes a storefront's address (storefront.PreviewURL).
	PreviewDomain string
	// PublicURL is the web app's address, for the link to the switch.
	PublicURL string
}

// Sweep writes again the listings of up to limit businesses the database
// marked (their storefront, catalogue, profile, photos, chat settings,
// areas, settings or tenant row changed), each in its own scope. Each
// mark is claimed with FOR UPDATE SKIP LOCKED, and the business's Discover
// lock taken without waiting, so two replicas never write the same one
// and a business its owner is changing waits for the next sweep; a mark
// made while it was being written stays for the next sweep. A business
// whose listing fails to write goes to the back of the queue (its mark's
// time moves on), so it does not hold up the others. It also works out
// again the listings whose reports leave the 30 days. It answers how many
// were written.
func (s *Sweeper) Sweep(ctx context.Context, limit int) (int, error) {
	var errs []error
	if _, err := s.Pool.Exec(ctx, `SELECT taskiem_discover_recount($1)`, limit); err != nil {
		errs = append(errs, err)
	}
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_discover_dirty($1)`, limit)
	if err != nil {
		return 0, errors.Join(append(errs, err)...)
	}
	marks, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, errors.Join(append(errs, err)...)
	}
	n := 0
	for _, tenant := range marks {
		if ctx.Err() != nil {
			break
		}
		wrote := false
		err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			var at time.Time
			err := tx.QueryRow(ctx, `SELECT at FROM discover_dirty WHERE tenant_id = $1 FOR UPDATE SKIP LOCKED`, tenant).Scan(&at)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil // another replica has it, or it is done
			}
			if err != nil {
				return err
			}
			if ok, err := tryLockTx(ctx, tx, tenant); err != nil || !ok {
				return err // being written by its owner: the next sweep
			}
			if _, err := RefreshTx(ctx, tx, tenant, s.PreviewDomain); err != nil {
				return err
			}
			// Held since it was read: a mark made since waits for this
			// transaction, and comes back after it.
			if _, err := tx.Exec(ctx, `DELETE FROM discover_dirty WHERE tenant_id = $1`, tenant); err != nil {
				return err
			}
			wrote = true
			return nil
		})
		if err != nil {
			errs = append(errs, err)
			s.later(ctx, tenant, `UPDATE discover_dirty SET at = clock_timestamp() WHERE tenant_id = $1`)
			continue
		}
		if wrote {
			n++
		}
	}
	return n, errors.Join(errs...)
}

// later moves a business that failed to the back of its queue, so the
// next run tries the others first.
func (s *Sweeper) later(ctx context.Context, tenant uuid.UUID, sql string) {
	_ = db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, sql, tenant)
		return err
	})
}

// The one-off notice (§10): a business listed by default is told once, in
// its owners' Taskiem chat, with the way to the switch.
const (
	// AlertListed is the notice's alert kind (one per business: its dedup
	// key is the kind).
	AlertListed = "discover_listed"
	// ListedTitle and ListedWords are what the owners are told.
	ListedTitle = "Your shop is in Discover"
	ListedWords = "Your shop is now listed in Taskiem's Discover, where customers find businesses. Turn it off here."
	// SettingsPath is where the switch is on the web.
	SettingsPath = "/settings/discover"
)

// Tell tells up to limit businesses listed by default (shown in
// Discover, no choice of their own, their storefront published when
// Discover shipped) that they are, once each: the business's settings
// record when (told_at, claimed through taskiem_discover_claim_notice
// under the business's Discover lock), and the owners hear of it as an
// owners' alert (the bell, and their chat on WhatsApp or Telegram), with a
// link to the switch. Running it twice, or on two replicas at once, tells
// no one twice. A business whose notice fails goes to the back of the
// queue. It answers how many were told.
func (s *Sweeper) Tell(ctx context.Context, limit int) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT * FROM taskiem_discover_notice_due($1)`, limit)
	if err != nil {
		return 0, err
	}
	due, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	var errs []error
	n := 0
	for _, tenant := range due {
		if ctx.Err() != nil {
			break
		}
		told := false
		err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			if err := lockTx(ctx, tx, tenant); err != nil {
				return err
			}
			// Claim it: only while it is due (shown, nobody told them,
			// they chose nothing).
			var claimed bool
			if err := tx.QueryRow(ctx, `SELECT taskiem_discover_claim_notice($1)`, tenant).Scan(&claimed); err != nil || !claimed {
				return err
			}
			if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'discover', 'discover.told', $2, '{}'::jsonb)`, tenant, tenant.String()); err != nil {
				return err
			}
			if _, err := alerts.TellOwners(ctx, tx, tenant, alerts.Alert{Kind: AlertListed, Dedup: AlertListed,
				Title: ListedTitle, Body: ListedWords, Link: s.link(SettingsPath)}); err != nil {
				return err
			}
			told = true
			return nil
		})
		if err != nil {
			errs = append(errs, err)
			s.later(ctx, tenant, `UPDATE discover_index SET notice_tried_at = now() WHERE tenant_id = $1`)
			continue
		}
		if told {
			n++
		}
	}
	return n, errors.Join(errs...)
}

func (s *Sweeper) link(path string) string {
	if s.PublicURL == "" {
		return ""
	}
	return strings.TrimRight(s.PublicURL, "/") + path
}

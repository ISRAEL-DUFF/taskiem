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
// marked (their storefront, profile, photos, chat settings, areas or
// tenant row changed), each in its own scope; a mark made while it was
// being written stays for the next sweep. It answers how many were
// written; one business failing does not stop the others.
func (s *Sweeper) Sweep(ctx context.Context, limit int) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id, at FROM taskiem_discover_dirty($1)`, limit)
	if err != nil {
		return 0, err
	}
	type mark struct {
		tenant uuid.UUID
		at     time.Time
	}
	marks, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (mark, error) {
		var m mark
		return m, r.Scan(&m.tenant, &m.at)
	})
	if err != nil {
		return 0, err
	}
	var errs []error
	n := 0
	for _, m := range marks {
		if ctx.Err() != nil {
			break
		}
		err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{m.tenant}, func(tx pgx.Tx) error {
			if _, err := RefreshTx(ctx, tx, m.tenant, s.PreviewDomain); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `DELETE FROM discover_dirty WHERE tenant_id = $1 AND at = $2`, m.tenant, m.at)
			return err
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
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

// Tell tells up to limit businesses listed by default (a published
// storefront, shown in Discover, no choice of their own) that they are,
// once each: the business's settings record when (told_at), and the
// owners hear of it as an owners' alert (the bell, and their chat on
// WhatsApp or Telegram), with a link to the switch. Running it twice, or
// on two replicas at once, tells no one twice. It answers how many were
// told.
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
			var shown bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM discover_index WHERE tenant_id = $1)`, tenant).Scan(&shown); err != nil || !shown {
				return err
			}
			// Claim it: only while nobody told them and they chose nothing.
			var claimed bool
			err := tx.QueryRow(ctx, `INSERT INTO discover_settings (tenant_id, told_at, updated_by) VALUES ($1, now(), 'system:discover')
				ON CONFLICT (tenant_id) DO UPDATE SET told_at = now()
				  WHERE discover_settings.told_at IS NULL AND discover_settings.listed IS NULL
				RETURNING true`, tenant).Scan(&claimed)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
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

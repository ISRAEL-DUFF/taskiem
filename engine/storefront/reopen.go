package storefront

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
)

// Lagos is where "tomorrow morning" is.
var lagos = func() *time.Location {
	l, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		return time.FixedZone("WAT", 3600)
	}
	return l
}()

// ReopenHour is when a page closed for the day opens again, Lagos time.
const ReopenHour = 6

// TomorrowMorning is the next ReopenHour in Lagos after now.
func TomorrowMorning(now time.Time) time.Time {
	l := now.In(lagos)
	t := time.Date(l.Year(), l.Month(), l.Day(), ReopenHour, 0, 0, 0, lagos)
	if !t.After(l) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// Reopener opens again pages closed until a time (scheduler role).
type Reopener struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
}

// Run reopens due pages every minute until ctx ends.
func (r *Reopener) Run(ctx context.Context) error {
	for {
		if _, err := r.Tick(ctx); err != nil && ctx.Err() == nil && r.Logger != nil {
			r.Logger.Error("storefront reopener", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Minute):
		}
	}
}

// Tick reopens every page whose time has come, each in its own tenant's
// scope and audited; it returns how many.
func (r *Reopener) Tick(ctx context.Context) (int, error) {
	rows, err := r.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_storefronts_to_reopen()`)
	if err != nil {
		return 0, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range tenants {
		err := db.InTenantTx(ctx, r.Pool, []uuid.UUID{t}, func(tx pgx.Tx) error {
			sf, err := Reopen(ctx, tx, t)
			if err != nil {
				return err
			}
			detail, _ := json.Marshal(map[string]any{"by_itself": true})
			_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'storefront', 'storefront.reopen', $2, $3)`, t, sf.Slug, detail)
			return err
		})
		if errors.Is(err, ErrNotPublished) {
			continue
		}
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

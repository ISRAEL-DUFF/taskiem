package catalogue

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
)

// NotifyRevoked alerts every tenant that installed a revoked version, in
// its own scope, on every enabled alert channel, and writes the revocation
// in its audit chain. It returns how many tenants it alerted. Running it
// twice alerts no one twice.
func NotifyRevoked(ctx context.Context, pool *pgxpool.Pool, connectorID, version, reason, by, publicURL string) (int, error) {
	rows, err := pool.Query(ctx, `SELECT * FROM taskiem_catalogue_installers($1, $2)`, connectorID, version)
	if err != nil {
		return 0, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	major, _, _ := strings.Cut(version, ".")
	link := ""
	if publicURL != "" {
		link = strings.TrimRight(publicURL, "/") + "/connections"
	}
	n := 0
	var errs []error
	for _, t := range tenants {
		err := db.InTenantTx(ctx, pool, []uuid.UUID{t}, func(tx pgx.Tx) error {
			sent, err := alerts.Notify(ctx, tx, t, alerts.Alert{
				Kind:  alerts.ConnectorRevoked,
				Dedup: connectorID + "@" + version,
				Title: fmt.Sprintf("Connector %s %s was revoked", connectorID, version),
				Body: fmt.Sprintf("The catalogue connector %s %s, which this organisation installed, was revoked: %s. "+
					"Steps that use %s@%s now fail instead of running it. Upgrade to a version that is not revoked, or uninstall it and change the workflows that use it.",
					connectorID, version, reason, connectorID, major),
				Link:   link,
				Detail: map[string]any{"connector": connectorID, "version": version, "reason": reason},
			})
			if err != nil || !sent {
				return err
			}
			n++
			_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', $2, 'catalogue.revoked', $3, jsonb_build_object('reason', $4::text))`,
				t, by, connectorID+"@"+version, reason)
			return err
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", t, err))
		}
	}
	return n, errors.Join(errs...)
}

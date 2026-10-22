package datatables

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// Token renewal. Xenibase tokens expire (a year at most), so the
// scheduler role runs RunRenewals: every RenewEvery, every
// platform-provisioned project whose connection token expires within
// RenewBefore gets a new one minted with the platform token, written
// into the data_tables connection in place of the old. An organisation's
// own project is its own to keep. A failure is logged at error and
// raised to the tenant as an alert once a day (its workflows on the
// table will stop when the token does).

// TokenAlert is the kind of the alert a tenant gets when its data_tables
// token could not be renewed.
const TokenAlert = "data_tables_token" //nolint:gosec // an alert kind, not a token

// RunRenewals renews tokens until ctx ends.
func (s *Service) RunRenewals(ctx context.Context) error {
	if !s.Available() {
		s.log().Info("data tables: no platform Xenibase configured; token renewal is off")
		<-ctx.Done()
		return nil
	}
	for {
		if err := s.RenewTick(ctx); err != nil && ctx.Err() == nil {
			s.log().Error("data tables: token renewal", "err", pii.Redact(err.Error()))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(RenewEvery):
		}
	}
}

// RenewTick renews every token due now and returns how many it renewed.
func (s *Service) RenewTick(ctx context.Context) error {
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_data_table_renewals($1)`, s.now().Add(RenewBefore))
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tenants {
		if err := s.Renew(ctx, t); err != nil && ctx.Err() == nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", t, err))
			s.log().Error("data tables: the data_tables token could not be renewed", "tenant", t, "err", pii.Redact(err.Error()))
			s.alertTenant(ctx, t, err)
		}
	}
	return errors.Join(errs...)
}

// Renew mints a new token for a tenant's platform-provisioned project
// and replaces the connection's.
func (s *Service) Renew(ctx context.Context, tenant uuid.UUID) error {
	var p Project
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		p, err = loadProject(ctx, tx, tenant)
		return err
	})
	if err != nil {
		return err
	}
	if !p.Ready() || p.Own || p.ProjectID == "" {
		return nil
	}
	c, err := s.platform(tenant.String())
	if err != nil {
		return err
	}
	creds, err := s.Credentials(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: "tables.renew"}), tenant)
	if err != nil {
		return err
	}
	days, err := c.tokenDays(ctx, s.now())
	if err != nil {
		return err
	}
	// A new token cannot outlive the platform's: when that one is what
	// runs out, renewing gains nothing, and the operator must replace it.
	if until := s.now().Add(time.Duration(days) * 24 * time.Hour); p.TokenExpiresAt != nil && !until.After(p.TokenExpiresAt.Add(24*time.Hour)) {
		return fmt.Errorf("%w: the platform's Xenibase token (TASKIEM_XENIBASE_TOKEN) expires before %s: replace it, and this one is renewed", ErrProjectFailed, until.Format("2 January 2006"))
	}
	tok, err := c.createToken(ctx, s.Platform.Org, TokenName, p.ProjectID, []string{"read", "write", "admin"}, days)
	if err != nil {
		return err
	}
	next := map[string]string{}
	for k, v := range creds {
		next[k] = v
	}
	next["token"] = tok.Secret
	expires := tok.Token.ExpiresAt
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := s.Vault.ReplaceCredentialsTx(ctx, tx, tenant, p.ConnectionID, next, "system"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE data_table_projects SET token_expires_at = $2, updated_at = now() WHERE tenant_id = $1`, tenant, expires); err != nil {
			return err
		}
		return audit(ctx, tx, tenant, "table.project.token_renewed", p.ProjectID, map[string]any{"expires_at": expires})
	})
}

// alertTenant raises the renewal failure to the tenant, once a day.
func (s *Service) alertTenant(ctx context.Context, tenant uuid.UUID, cause error) {
	day := s.now().UTC().Format("2006-01-02")
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := alerts.Notify(ctx, tx, tenant, alerts.Alert{Kind: TokenAlert, Dedup: day, Title: "Data tables: the connection's token could not be renewed",
			Body: "Taskiem could not renew the token of the data_tables connection. Workflows and the Tables page keep working until it expires; the platform operator has been told. " + pii.Redact(cause.Error()),
			Link: "/tables", Detail: map[string]any{"day": day}})
		return err
	})
	if err != nil {
		s.log().Warn("data tables: could not record the renewal alert", "tenant", tenant, "err", err)
	}
}

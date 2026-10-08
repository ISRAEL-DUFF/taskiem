package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/ussd"
)

// The USSD notifier (role api): starts confirmed sessions whose hand-off
// did not happen (the edge died after answering), sends outcomes by SMS
// where the menu asks for them, and purges old sessions.

// ussdSMSConnector is the connector outcomes are sent with.
const ussdSMSConnector = "africastalking@1"

// RunUSSD works the USSD queue until ctx ends.
func (s *Server) RunUSSD(ctx context.Context) error {
	for {
		if err := s.USSDTick(ctx); err != nil && ctx.Err() == nil {
			s.Logger.Error("ussd notifier", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}

// USSDTick does what is due now for every tenant with USSD sessions.
func (s *Server) USSDTick(ctx context.Context) error {
	rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_ussd_tenants()`)
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tenants {
		if err := s.ussdTenantTick(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", t, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Server) ussdTenantTick(ctx context.Context, tenant uuid.UUID) error {
	type key struct{ provider, sid string }
	var stuck []key
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		// Keep a day of expired sessions for the per-number limits, then
		// forget them; sessions still owing an outcome stay.
		if _, err := tx.Exec(ctx, `DELETE FROM ussd_sessions WHERE tenant_id = $1 AND expires_at < now() - interval '1 day'
			AND state <> 'confirmed' AND NOT (notify AND notified_at IS NULL AND state IN ('started', 'refused') AND data IS NOT NULL)`, tenant); err != nil {
			return err
		}
		// Owed outcomes older than a week are dropped with their data.
		if _, err := tx.Exec(ctx, `UPDATE ussd_sessions SET data = NULL, notice = 'skipped', notified_at = now()
			WHERE tenant_id = $1 AND notify AND notified_at IS NULL AND confirmed_at < now() - interval '7 days'`, tenant); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT provider, session_id FROM ussd_sessions WHERE tenant_id = $1 AND state = 'confirmed'
			AND confirmed_at < now() - interval '5 seconds' ORDER BY confirmed_at LIMIT 100`, tenant)
		if err != nil {
			return err
		}
		stuck, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (key, error) {
			var k key
			return k, r.Scan(&k.provider, &k.sid)
		})
		return err
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, k := range stuck {
		if err := s.ussdHandoff(ctx, tenant, k.provider, k.sid); err != nil {
			errs = append(errs, err)
		}
	}
	if err := s.ussdNotify(ctx, tenant); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

type ussdOutcome struct {
	provider, sid, ref, env, state, run string
	wf                                  uuid.UUID
	version                             int
	data                                []byte
}

// ussdNotify sends what is owed. Each outcome is claimed (notified_at)
// before it is sent: an SMS has no idempotency key, so one whose sending
// fails is recorded as failed, never sent twice.
func (s *Server) ussdNotify(ctx context.Context, tenant uuid.UUID) error {
	var due []ussdOutcome
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `WITH due AS (
			  SELECT s.provider, s.session_id, COALESCE(r.status, '') AS run_status FROM ussd_sessions s LEFT JOIN runs r ON r.id = s.run_id
			   WHERE s.tenant_id = $1 AND s.notify AND s.notified_at IS NULL AND s.data IS NOT NULL
			     AND (s.state = 'refused' OR (s.state = 'started' AND r.status IN ('completed', 'failed', 'cancelled', 'needs_reconciliation')))
			   ORDER BY s.confirmed_at LIMIT 50 FOR UPDATE OF s SKIP LOCKED)
			UPDATE ussd_sessions s SET notified_at = now(), updated_at = now() FROM due
			 WHERE s.tenant_id = $1 AND s.provider = due.provider AND s.session_id = due.session_id
			RETURNING s.provider, s.session_id, s.reference, s.environment, s.state, due.run_status, s.workflow_id, s.version, s.data`, tenant)
		if err != nil {
			return err
		}
		due, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ussdOutcome, error) {
			var o ussdOutcome
			return o, r.Scan(&o.provider, &o.sid, &o.ref, &o.env, &o.state, &o.run, &o.wf, &o.version, &o.data)
		})
		return err
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, o := range due {
		notice := "sent"
		if err := s.ussdSendOutcome(ctx, tenant, o); err != nil {
			notice = "failed"
			s.Logger.Warn("ussd: outcome SMS not sent", "tenant", tenant, "ref", o.ref, "err", pii.Redact(err.Error()))
		}
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE ussd_sessions SET notice = $4, data = NULL, updated_at = now()
				WHERE tenant_id = $1 AND provider = $2 AND session_id = $3`, tenant, o.provider, o.sid, notice)
			return err
		}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ussdSendOutcome renders the menu's message for the outcome and sends it
// to the caller through the tenant's Africa's Talking connection.
func (s *Server) ussdSendOutcome(ctx context.Context, tenant uuid.UUID, o ussdOutcome) error {
	m, err := s.ussdMenuFor(ctx, tenant, o.wf, o.version)
	if err != nil {
		return err
	}
	if !m.menu.SMS() {
		return nil
	}
	var phone string
	var inputs map[string]any
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		v, err := expr.DecodeJSON(o.data)
		if err != nil {
			return err
		}
		opened, err := pii.Open(ctx, s.Store.PII, tx, tenant, v, pii.Taint{})
		if err != nil {
			return err
		}
		dm, _ := opened.(map[string]any)
		phone, _ = dm["caller"].(string)
		inputs, _ = dm["inputs"].(map[string]any)
		return nil
	}); err != nil {
		return err
	}
	if phone == "" {
		return errors.New("no number to send to")
	}
	n := m.menu.Notify
	tmpl := ussd.DefaultFailed
	if n.Failed != "" {
		tmpl = n.Failed
	}
	switch {
	case o.state == "started" && o.run == "completed":
		tmpl = ussd.DefaultCompleted
		if n.Completed != "" {
			tmpl = n.Completed
		}
	case o.state == "started" && o.run == "needs_reconciliation":
		// Taskiem's own text, in the caller's language when it is on
		// (a menu's own texts are the tenant's, in its words).
		tmpl = lang.ASCII(tr(s.chooseLang(ctx, tenant, phone, "").Tag, "sms.reconcile", "reference", "{{reference}}"))
	}
	return s.ussdSMS(ctx, tenant, o.env, n.Connection, phone, m.menu.Render(tmpl, inputs, o.ref))
}

// ussdSMS sends one SMS with the tenant's Africa's Talking connection,
// through the egress guard, reading the credentials with a recorded use.
func (s *Server) ussdSMS(ctx context.Context, tenant uuid.UUID, env, connection, to, text string) error {
	if s.Registry == nil || s.Vault == nil {
		return errors.New("no connector registry or vault")
	}
	reg, err := s.Registry.For(ctx, tenant.String())
	if err != nil {
		return err
	}
	conn, ok := reg.Get(ussdSMSConnector)
	if !ok || conn.Actions["send_sms"] == nil {
		return errors.New("the Africa's Talking connector is not installed")
	}
	creds, err := s.Vault.Credentials(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: "ussd.notify"}), tenant, env, conn.Manifest.ID, connection)
	if err != nil {
		return err
	}
	g := s.Egress
	if g == nil {
		g = &egress.Guard{Logger: s.Logger}
	}
	hc := g.Client(egress.Policy{Tenant: tenant.String(), Hosts: conn.Manifest.Hosts(), Purpose: "connector:" + conn.Manifest.ID}, 20*time.Second)
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err = conn.Actions["send_sms"].Execute(sctx, connector.Request{
		Input: map[string]any{"to": []any{to}, "message": text}, Credentials: creds, HTTP: hc, Logger: s.Logger,
	})
	return err
}

package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// What Payrolla's employee events do in Taskiem (P4b-2), heard on the
// connection's own subscription:
//
//   - employee.created, .updated and .terminated: screens read Payrolla
//     when they open, so nothing is kept to refresh; a name that changed
//     (changed_fields) is the name kept with a linked staff member.
//   - employee.bank_change_requested: the linked approvers other than
//     whoever asked are told it is waiting, with the bank's name for the
//     new account and its last four digits (read from Payrolla, not the
//     event, which carries no account).
//   - employee.bank_change_approved and _rejected: whoever asked in
//     Taskiem is told, and a linked employee gets a line in their own
//     chat (Payrolla texts and emails them itself).

// HeardAll is each of fns told every event, in order.
func HeardAll(fns ...func(context.Context, ingest.HeardEvent)) func(context.Context, ingest.HeardEvent) {
	return func(ctx context.Context, e ingest.HeardEvent) {
		for _, fn := range fns {
			if fn != nil {
				fn(ctx, e)
			}
		}
	}
}

// PayrollaTeamHeard reacts to Payrolla's employee events. It reads
// Payrolla on the connection the event came on (reg, vault and guard as a
// connector's call uses them).
func PayrollaTeamHeard(pool *pgxpool.Pool, vault *secrets.Vault, reg *connector.Registry, guard *egress.Guard, log *slog.Logger) func(context.Context, ingest.HeardEvent) {
	h := &teamHeard{pool: pool, vault: vault, reg: reg, guard: guard, log: log}
	return h.heard
}

type teamHeard struct {
	pool  *pgxpool.Pool
	vault *secrets.Vault
	reg   *connector.Registry
	guard *egress.Guard
	log   *slog.Logger
}

func (h *teamHeard) heard(ctx context.Context, e ingest.HeardEvent) {
	if e.Connector != payrollaID {
		return
	}
	ev, ok := payrolla.ReadEmployeeEvent(e.Body)
	if !ok {
		return
	}
	var err error
	switch ev.Type {
	case "employee.updated":
		if slices.Contains(ev.ChangedFields, "first_name") || slices.Contains(ev.ChangedFields, "last_name") {
			err = db.InTenantTx(ctx, h.pool, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE staff_profiles SET payrolla_name = $3 WHERE tenant_id = $1 AND payrolla_employee_id = $2`,
					e.Tenant, ev.Employee.ID, clipRunes(ev.Employee.Name(), payrollaNameMax))
				return err
			})
		}
	case "employee.bank_change_requested":
		err = h.requested(ctx, e, ev)
	case "employee.bank_change_approved", "employee.bank_change_rejected":
		err = h.decided(ctx, e, ev)
	}
	if err != nil {
		h.log.Warn("payrolla: an employee event", "type", ev.Type, "err", err)
	}
}

// client reads Payrolla on the event's connection, as Taskiem.
func (h *teamHeard) client(ctx context.Context, e ingest.HeardEvent) (*payrolla.Client, error) {
	lookup, err := h.reg.For(ctx, e.Tenant.String())
	if err != nil {
		return nil, err
	}
	c, ok := connectorByID(lookup, payrollaID)
	if !ok {
		return nil, errors.New("payrolla: not available")
	}
	use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: "payrolla.employee_event"})
	creds, err := h.vault.Credentials(use, e.Tenant, e.Environment, payrollaID, e.Connection)
	if err != nil {
		return nil, err
	}
	hc := h.guard.Client(egress.Policy{Tenant: e.Tenant.String(), Hosts: c.Manifest.HostsFor(creds), Purpose: "connector:" + payrollaID}, 20*time.Second)
	tenant, env, name := e.Tenant, e.Environment, e.Connection
	return payrolla.NewClient(c, creds, hc, func(ctx context.Context, fn connector.RotateFunc) (map[string]string, error) {
		return h.vault.RotateCredentials(use, tenant, env, payrollaID, name, fn)
	}), nil
}

// linkedStaffTx is the active staff member linked to an employee, or Nil.
func linkedStaffTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, employee string) (uuid.UUID, error) {
	var u uuid.UUID
	err := tx.QueryRow(ctx, `SELECT sp.user_id FROM staff_profiles sp JOIN memberships m ON m.tenant_id = sp.tenant_id AND m.user_id = sp.user_id
		WHERE sp.tenant_id = $1 AND sp.payrolla_employee_id = $2 LIMIT 1`, tenant, employee).Scan(&u)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	return u, err
}

func (h *teamHeard) requested(ctx context.Context, e ingest.HeardEvent, ev payrolla.EmployeeEvent) error {
	cl, err := h.client(ctx, e)
	if err != nil {
		return err
	}
	changes, err := cl.BankChanges(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(changes, func(c payrolla.BankChange) bool { return c.ID == ev.ChangeID })
	if i < 0 {
		return nil // decided already
	}
	c := changes[i]
	approvers, err := cl.Approvers(ctx)
	if err != nil {
		return err
	}
	return db.InTenantTx(ctx, h.pool, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		asked := map[string]payrollStaffRef{}
		if err := askedByTx(ctx, tx, e.Tenant, []string{bankChangeTarget(c.ID)}, asked); err != nil {
			return err
		}
		asker, inTaskiem := asked[c.ID]
		employee, err := linkedStaffTx(ctx, tx, e.Tenant, c.EmployeeID)
		if err != nil {
			return err
		}
		var to []uuid.UUID
		for _, a := range approvers {
			if u := a.LinkedUser(); u != uuid.Nil && a.CanApprove && u != asker.UserID && u != employee {
				to = append(to, u)
			}
		}
		by := "Asked in Payrolla."
		switch {
		case ev.RequestedVia == "employee":
			by = "Asked by " + firstName(ev.Employee.Name()) + " themselves."
		case inTaskiem:
			by = "Asked by " + firstName(asker.Name) + " in Taskiem."
		case ev.RequestedVia == "connect":
			by = "Asked in Taskiem."
		}
		body := fmt.Sprintf("%s's salary account change is waiting: %s, %s ••%s. %s", ev.Employee.Name(), c.AccountName, bankName(c.BankCode), c.Last4, by)
		_, _, err = alerts.TellPeople(ctx, tx, e.Tenant, to, alerts.Alert{Kind: "payroll_bank_change", Dedup: c.ID, Title: "Account change waiting",
			Body: body, Link: "/payroll/account-changes"})
		return err
	})
}

func firstName(n string) string {
	if f := strings.Fields(n); len(f) > 0 {
		return f[0]
	}
	return n
}

func (h *teamHeard) decided(ctx context.Context, e ingest.HeardEvent, ev payrolla.EmployeeEvent) error {
	approved := ev.Type == "employee.bank_change_approved"
	return db.InTenantTx(ctx, h.pool, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		asked := map[string]payrollStaffRef{}
		if err := askedByTx(ctx, tx, e.Tenant, []string{bankChangeTarget(ev.ChangeID)}, asked); err != nil {
			return err
		}
		employee, err := linkedStaffTx(ctx, tx, e.Tenant, ev.Employee.ID)
		if err != nil {
			return err
		}
		name := ev.Employee.Name()
		decision, title := "approved", "Account change approved"
		if !approved {
			decision, title = "rejected", "Account change rejected"
		}
		if a, ok := asked[ev.ChangeID]; ok && a.UserID != employee {
			body := fmt.Sprintf("The change to %s's salary account was approved. Payrolla pays them into the new account from now on.", name)
			if !approved {
				body = fmt.Sprintf("The change to %s's salary account was rejected. They're still paid into their current account.", name)
			}
			if _, _, err := alerts.TellPeople(ctx, tx, e.Tenant, []uuid.UUID{a.UserID}, alerts.Alert{Kind: "payroll_bank_change_" + decision, Dedup: ev.ChangeID,
				Title: title, Body: body, Link: "/payroll/team"}); err != nil {
				return err
			}
		}
		if employee == uuid.Nil {
			return nil
		}
		body := "Your new salary account was approved. You're paid into it from now on."
		if !approved {
			body = "Your salary account change was not approved. You're still paid into your current account. Ask your employer why."
		}
		_, _, err = alerts.TellPeople(ctx, tx, e.Tenant, []uuid.UUID{employee}, alerts.Alert{Kind: "payroll_my_bank_change_" + decision, Dedup: ev.ChangeID,
			Title: title, Body: body, Link: "/payroll/me"})
		return err
	})
}

// askEndLeaverTx asks the owners whether a staff member who just left the
// business, and was linked to a Payrolla employee, should leave Payrolla
// too: Taskiem never ends someone's employment by itself.
func askEndLeaverTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, name string) error {
	var emp *string
	err := tx.QueryRow(ctx, `SELECT payrolla_employee_id FROM staff_profiles WHERE tenant_id = $1 AND user_id = $2`, tenant, user).Scan(&emp)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && emp == nil {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = alerts.TellOwners(ctx, tx, tenant, alerts.Alert{Kind: "payrolla_leaver", Dedup: user.String() + ":" + *emp, Title: "Still in Payrolla",
		Body: name + " left the business in Taskiem. End their employment in Payrolla too?", Link: "/payroll/team?employee=" + *emp})
	return err
}

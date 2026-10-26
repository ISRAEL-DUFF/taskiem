//go:build e2e

package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/schools/demo"
	"github.com/israel-duff/taskiem/engine/sender"
)

const demoUsage = `usage: taskiem demo school (--create-tenant [--name NAME] | --tenant TENANT_ID) [--rail CONNECTION [--wallet ID]]

Seeds the demonstration school the browser tests walk (web/e2e/pilot.spec.ts):
forty students in twenty-five families, the term invoiced, account numbers,
and a realistic mix of payments and exceptions, through the engine's own
services, in a test build only (-tags e2e). --create-tenant makes a business of its own (default name "Demo
School") with a proprietor, a bursar, an approver and a gate user, whose
passwords are printed once; --tenant seeds an existing business that has
no students yet, with its first owner doing everything. --rail names the
connection that issues the students' account numbers (connector/name);
without it the first connection that can issue them is used, else the
demo rail, which needs no service. Seeding the same tenant again does
nothing; money log rows are append-only, so a demo is never removed`

// The demo school is a test fixture: it is in the binary only when built
// with `-tags e2e` (web/e2e/serve.sh and the Go tests), never in the image
// or a release, so no deployed Taskiem can seed one.
func init() {
	testCommands["demo"] = demoCmd
}

// demoCmd is `taskiem demo school`, in an e2e build only.
func demoCmd(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "school" {
		return errors.New(demoUsage)
	}
	fs := flag.NewFlagSet("demo school", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	create := fs.Bool("create-tenant", false, "make a business of its own, with its people")
	name := fs.String("name", "Demo School", "the business's name with --create-tenant")
	tenantFlag := fs.String("tenant", "", "an existing business's id")
	rail := fs.String("rail", "", "the connection that issues account numbers (connector/name), or fake")
	wallet := fs.String("wallet", "", "the wallet the numbers pay into, on rails that need one")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("demo school: %w\n%s", err, demoUsage)
	}
	if fs.NArg() > 0 || *create == (*tenantFlag != "") {
		return errors.New(demoUsage)
	}
	*name = strings.Join(strings.Fields(*name), " ")
	if *name == "" {
		return errors.New("demo school: --name needs a name")
	}
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("demo school: %w", err)
	}
	if err := configureEgress(nil); err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e, err := newEngine(ctx, cfg, log)
	if err != nil {
		return fmt.Errorf("demo school: %w", err)
	}
	defer e.pool.Close()
	defer e.connectors.Pool.Close()

	// The people: made, or the tenant's first owner.
	type person struct{ role, email, password string }
	var people []person
	var tenant, owner, bursar, approver uuid.UUID
	if *create {
		slug := strings.Trim(strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
				return r
			case r >= 'A' && r <= 'Z':
				return r + ('a' - 'A')
			}
			return '-'
		}, *name), "-")
		if slug == "" {
			slug = "demo-school"
		}
		for _, role := range []string{"proprietor", "bursar", "approver", "gate"} {
			pw := make([]byte, 15)
			if _, err := rand.Read(pw); err != nil {
				return err
			}
			people = append(people, person{role, role + "@" + slug + ".invalid", base64.RawURLEncoding.EncodeToString(pw)})
		}
		if tenant, owner, err = api.CreateTenant(ctx, e.pool, *name, people[0].email, "Proprietor", people[0].password); err != nil {
			return fmt.Errorf("demo school: %w (pass another --name to make a second demo)", err)
		}
		if err := db.InTenantTx(ctx, e.pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, err := api.EnsureBursarRole(ctx, tx, tenant, owner.String())
			return err
		}); err != nil {
			return fmt.Errorf("demo school: %w", err)
		}
		for i, roles := range [][]string{{api.BursarRoleName}, {"owner"}, {"gate"}} {
			p := people[i+1]
			id, err := api.CreateMember(ctx, e.pool, tenant, p.email, strings.ToUpper(p.role[:1])+p.role[1:], p.password, roles)
			if err != nil {
				return fmt.Errorf("demo school: %s: %w", p.role, err)
			}
			switch p.role {
			case "bursar":
				bursar = id
			case "approver":
				approver = id
			}
		}
	} else {
		if tenant, err = uuid.Parse(*tenantFlag); err != nil {
			return fmt.Errorf("demo school: %q is not a tenant id", *tenantFlag)
		}
		if err := db.InTenantTx(ctx, e.pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT m.user_id FROM memberships m JOIN users u ON u.id = m.user_id WHERE m.tenant_id = $1 AND m.role = 'owner' ORDER BY u.created_at, u.id LIMIT 1`, tenant).Scan(&owner)
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("demo school: tenant %s has no owner", tenant)
			}
			return fmt.Errorf("demo school: %w", err)
		}
		bursar, approver = owner, owner
	}

	// The services as the server wires them, with the demo rail beside
	// the business's own, and a message queue to hold the guardians on
	// when the server has none.
	sch := e.schools()
	rails := demo.Rails{Real: sch.Rails}
	sch.Rails = rails
	if *rail == "" {
		list, err := rails.Capable(ctx, tenant, "virtual_account")
		if err != nil {
			return fmt.Errorf("demo school: %w", err)
		}
		*rail = list[0].Channel() // the demo rail is last
		for _, r := range list {
			if r.Connector != "demo" && (!r.Needs("virtual_account", "account") || *wallet != "") {
				*rail = r.Channel()
				break
			}
		}
	}
	if e.invoices.Messages == nil {
		key := sha256.Sum256([]byte("taskiem demo school " + tenant.String()))
		e.invoices.Messages = &sender.Queue{Vault: e.vault, HashKey: key[:]}
	}
	m := &matching.Matcher{Invoices: e.invoices, PII: e.vault, Reversed: sch.ReceiptsReversed, Logger: log}
	seeder := &demo.Seeder{Schools: sch, MoneyLog: e.moneyLog(), Matcher: m, Messages: e.invoices.Messages}
	res, err := seeder.Seed(ctx, demo.Options{Tenant: tenant, Rail: *rail, Wallet: *wallet, Owner: owner.String(), Bursar: bursar.String(), Approver: approver.String()})
	if err != nil {
		return fmt.Errorf("demo school: %w", err)
	}
	if !res.Seeded {
		fmt.Fprintf(stdout, "tenant %s has the demo school already: nothing changed\n", tenant)
		return nil
	}
	fmt.Fprintf(stdout, "tenant %s\n", tenant)
	fmt.Fprintf(stdout, "seeded %s: %d classes, %d families, %d students, %d invoices for %s, %d account numbers on %s\n",
		*name, res.Classes, res.Families, res.Students, res.Invoiced, res.Term, res.AccountsIssued, railWords(res.Rail))
	fmt.Fprintf(stdout, "closed %s (reconciles: %t)\n", res.CloseDay, res.Reconciles)
	if len(people) > 0 {
		fmt.Fprintln(stdout, "\nSign in as (the passwords are shown once):")
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		for _, p := range people {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", p.role, p.email, p.password)
		}
		_ = tw.Flush()
	}
	return nil
}

func railWords(channel string) string {
	if channel == demo.Channel {
		return "the demo rail (no payment service: the numbers take no real money)"
	}
	return channel
}

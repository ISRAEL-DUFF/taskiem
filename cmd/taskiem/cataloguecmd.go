package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/catalogue"
	"github.com/israel-duff/taskiem/engine/db"
)

const catalogueUsage = `usage:
  taskiem catalogue reviewers [add|remove EMAIL]
      list, add or remove the people who review catalogue submissions
  taskiem catalogue publishers [verify|suspend|reinstate SLUG] [--note TEXT]
      list publishers, or verify a namespace (it may then submit), suspend one (its versions stop loading) or reinstate it
  taskiem catalogue queue [--all]
      submissions waiting for review (--all: every state)
  taskiem catalogue show ID
      a submission's manifest summary, automated checks and history
  taskiem catalogue review ID --as EMAIL (--approve --confirm KEY,...|all | --reject) --note TEXT
      decide a submission; the reviewer must be on the list and outside the publisher (four eyes);
      approving needs every checklist item confirmed
  taskiem catalogue revoke CONNECTOR VERSION --as EMAIL --reason TEXT
      the kill switch: new steps stop using the version and every installing tenant is alerted
Every decision is audited in the publisher's chain (and revocations in each installing tenant's)`

// catalogueCmd is the operator's and reviewer's side of the public
// connector catalogue (docs/connector-submissions.md).
func catalogueCmd(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(catalogueUsage)
	}
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("catalogue: %w", err)
	}
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return fmt.Errorf("catalogue: %w", err)
	}
	defer pool.Close()
	by := "cli:" + env("USER", "operator")
	switch args[0] {
	case "reviewers":
		return catalogueReviewers(ctx, pool, args[1:], by, stdout)
	case "publishers":
		return cataloguePublishers(ctx, pool, args[1:], by, stdout)
	case "queue":
		return catalogueQueue(ctx, pool, args[1:], stdout)
	case "show":
		return catalogueShow(ctx, pool, args[1:], stdout)
	case "review":
		return catalogueReview(ctx, pool, args[1:], stdout)
	case "revoke":
		return catalogueRevoke(ctx, pool, args[1:], cfg.PublicURL, stdout)
	}
	return errors.New(catalogueUsage)
}

func catalogueReviewers(ctx context.Context, pool *pgxpool.Pool, args []string, by string, stdout io.Writer) error {
	switch len(args) {
	case 0:
	case 2:
		verb, email := args[0], args[1] //nolint:gosec // two arguments in this case
		if verb != "add" && verb != "remove" {
			return errors.New(catalogueUsage)
		}
		if !strings.Contains(email, "@") {
			return fmt.Errorf("catalogue reviewers: %q is not an email address", email)
		}
		if _, err := pool.Exec(ctx, `SELECT taskiem_catalogue_set_reviewer($1, $2, $3)`, email, verb == "add", by); err != nil {
			return fmt.Errorf("catalogue reviewers: %w", err)
		}
	default:
		return errors.New(catalogueUsage)
	}
	rows, err := pool.Query(ctx, `SELECT email, added_by, added_at FROM taskiem_catalogue_reviewers()`)
	if err != nil {
		return err
	}
	defer rows.Close()
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "REVIEWER\tADDED BY\tADDED")
	for rows.Next() {
		var email, addedBy string
		var at time.Time
		if err := rows.Scan(&email, &addedBy, &at); err != nil {
			return err
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", email, addedBy, at.Format(time.DateOnly))
	}
	_ = tw.Flush()
	return rows.Err()
}

// auditIn appends an operator's entry to a tenant's chain.
func auditIn(ctx context.Context, pool *pgxpool.Pool, tenant uuid.UUID, by, action, target string, detail map[string]any) error {
	raw, _ := json.Marshal(detail)
	return db.InTenantTx(ctx, pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', $2, $3, $4, $5)`, tenant, by, action, target, raw)
		return err
	})
}

func cataloguePublishers(ctx context.Context, pool *pgxpool.Pool, args []string, by string, stdout io.Writer) error {
	if len(args) >= 2 {
		status := map[string]string{"verify": "verified", "suspend": "suspended", "reinstate": "verified"}[args[0]]
		if status == "" {
			return errors.New(catalogueUsage)
		}
		fs := flag.NewFlagSet("catalogue publishers", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		note := fs.String("note", "", "why (shown to the publisher)")
		if err := fs.Parse(args[2:]); err != nil || fs.NArg() > 0 {
			return errors.New(catalogueUsage)
		}
		if args[0] == "suspend" && *note == "" {
			return errors.New("catalogue publishers suspend: --note is required: the publisher is told why")
		}
		var tenant uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT taskiem_catalogue_set_publisher($1, $2, $3, $4)`, args[1], status, by, *note).Scan(&tenant); err != nil {
			return fmt.Errorf("catalogue publishers: %w", err)
		}
		if err := auditIn(ctx, pool, tenant, by, "catalogue.publisher."+args[0], args[1], map[string]any{"note": *note}); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "publisher %s is %s\n", args[1], status)
		if args[0] == "suspend" {
			fmt.Fprintln(stdout, "its versions stop loading for new steps within a minute, and leave the catalogue")
		}
		return nil
	}
	if len(args) != 0 {
		return errors.New(catalogueUsage)
	}
	rows, err := pool.Query(ctx, `SELECT tenant_id, slug, name, key_id, status, requested_at FROM taskiem_catalogue_publishers()`)
	if err != nil {
		return err
	}
	defer rows.Close()
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SLUG\tNAME\tSTATUS\tKEY\tTENANT\tREQUESTED")
	for rows.Next() {
		var tenant uuid.UUID
		var slug, name, key, status string
		var at time.Time
		if err := rows.Scan(&tenant, &slug, &name, &key, &status, &at); err != nil {
			return err
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", slug, name, status, key, tenant, at.Format(time.DateOnly))
	}
	_ = tw.Flush()
	return rows.Err()
}

func catalogueQueue(ctx context.Context, pool *pgxpool.Pool, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("catalogue queue", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	all := fs.Bool("all", false, "every state")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return errors.New(catalogueUsage)
	}
	states := []string{"in_review"}
	if *all {
		states = []string{"checks_failed", "in_review", "approved", "rejected", "published", "withdrawn", "revoked"}
	}
	rows, err := pool.Query(ctx, `SELECT id, publisher, connector_id, version, state, licence, submitted_at, passed, COALESCE(reviewed_by, '') FROM taskiem_catalogue_queue($1)`, states)
	if err != nil {
		return err
	}
	defer rows.Close()
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPUBLISHER\tCONNECTOR\tVERSION\tSTATE\tCHECKS\tLICENCE\tSUBMITTED\tREVIEWER")
	for rows.Next() {
		var id uuid.UUID
		var pub, conn, ver, state, licence, reviewer string
		var at time.Time
		var passed bool
		if err := rows.Scan(&id, &pub, &conn, &ver, &state, &licence, &at, &passed, &reviewer); err != nil {
			return err
		}
		checks := "passed"
		if !passed {
			checks = "failed"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", id, pub, conn, ver, state, checks, licence, at.Format(time.DateTime), reviewer)
	}
	_ = tw.Flush()
	return rows.Err()
}

func catalogueShow(ctx context.Context, pool *pgxpool.Pool, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New(catalogueUsage)
	}
	id, err := uuid.Parse(args[0])
	if err != nil {
		return fmt.Errorf("catalogue show: %q is not a submission id", args[0])
	}
	var (
		tenant                                                       uuid.UUID
		pub, conn, ver, state, manifest, modDigest, pkgDigest, keyID string
		licence, submittedBy                                         string
		source, reviewedBy, note                                     *string
		att, checks                                                  []byte
		at                                                           time.Time
	)
	err = pool.QueryRow(ctx, `SELECT publisher_tenant, publisher, connector_id, version, state, manifest, module_digest, package_digest, key_id, licence, source_url,
		attestation, checks, submitted_by, submitted_at, reviewed_by, review_note FROM taskiem_catalogue_submission($1)`, id).
		Scan(&tenant, &pub, &conn, &ver, &state, &manifest, &modDigest, &pkgDigest, &keyID, &licence, &source, &att, &checks, &submittedBy, &at, &reviewedBy, &note)
	if err != nil {
		return fmt.Errorf("catalogue show: %w", err)
	}
	fmt.Fprintf(stdout, "%s %s by %s (tenant %s): %s\n  submitted by %s at %s\n  package digest %s, signed with key %s\n  module sha256 %s\n  licence %s",
		conn, ver, pub, tenant, state, submittedBy, at.Format(time.DateTime), pkgDigest, keyID, modDigest, licence)
	if source != nil {
		fmt.Fprintf(stdout, ", source %s", *source)
	}
	fmt.Fprintf(stdout, "\n  attestation %s\n", att)
	if reviewedBy != nil {
		fmt.Fprintf(stdout, "  reviewed by %s: %s\n", *reviewedBy, deref(note))
	}
	m, findings := catalogue.Lint([]byte(manifest), catalogue.LintOptions{Publisher: pub})
	if m != nil {
		s := catalogue.Summarise(m)
		fmt.Fprintf(stdout, "\n%s: %s\n  hosts: %s\n", s.Name, s.Description, strings.Join(s.Hosts, ", "))
		for name, class := range s.Actions {
			fmt.Fprintf(stdout, "  action %s: %s\n", name, class)
		}
		fmt.Fprintf(stdout, "  personal data: %s\n", strings.Join(s.PII, ", "))
	}
	for _, f := range findings {
		fmt.Fprintf(stdout, "  lint %s\n", f)
	}
	var rep catalogue.Report
	_ = json.Unmarshal(checks, &rep)
	fmt.Fprintln(stdout, "\nautomated checks:")
	for _, c := range rep.Checks {
		mark := "pass"
		if !c.Pass {
			mark = "FAIL"
		}
		fmt.Fprintf(stdout, "  %s %s: %s\n", mark, c.Name, c.Detail)
	}
	if rep.Conformance != nil {
		for _, r := range rep.Conformance.Results {
			fmt.Fprintf(stdout, "  case %s (%s): pass=%v %s\n", r.Case, r.Action, r.Pass, strings.Join(r.Problems, "; "))
		}
		if len(rep.Conformance.ReadWrites) > 0 {
			fmt.Fprintf(stdout, "  CHECK: read action(s) that sent other than GET/HEAD: %s\n", strings.Join(rep.Conformance.ReadWrites, ", "))
		}
	}
	fmt.Fprintln(stdout, "\nreview checklist (confirm each to approve):")
	for _, c := range catalogue.Checklist {
		fmt.Fprintf(stdout, "  %-12s %s\n", c.Key, c.Text)
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func catalogueReview(ctx context.Context, pool *pgxpool.Pool, args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return errors.New(catalogueUsage)
	}
	id, err := uuid.Parse(args[0])
	if err != nil {
		return fmt.Errorf("catalogue review: %q is not a submission id", args[0])
	}
	fs := flag.NewFlagSet("catalogue review", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	as := fs.String("as", "", "the reviewer's email (on the reviewer list)")
	approve := fs.Bool("approve", false, "approve")
	reject := fs.Bool("reject", false, "reject")
	confirm := fs.String("confirm", "", "checklist items confirmed: comma-separated keys, or all")
	note := fs.String("note", "", "what the publisher is told")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 || *as == "" || *approve == *reject || strings.TrimSpace(*note) == "" {
		return errors.New(catalogueUsage)
	}
	checklist := map[string]bool{}
	if *approve {
		confirmed := strings.Split(*confirm, ",")
		if *confirm == "all" {
			confirmed = catalogue.ChecklistKeys()
		}
		var missing []string
		for _, k := range catalogue.ChecklistKeys() {
			if slices.Contains(confirmed, k) {
				checklist[k] = true
			} else {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("catalogue review: to approve, confirm every checklist item (--confirm all); not confirmed: %s", strings.Join(missing, ", "))
		}
	}
	raw, _ := json.Marshal(checklist)
	var tenant uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT taskiem_catalogue_review($1, $2, $3, $4, $5)`, id, *as, *approve, *note, raw).Scan(&tenant); err != nil {
		return fmt.Errorf("catalogue review: %w", err)
	}
	decision, action := "rejected", "catalogue.reject"
	if *approve {
		decision, action = "approved", "catalogue.approve"
	}
	if err := auditIn(ctx, pool, tenant, "reviewer:"+strings.ToLower(*as), action, id.String(), map[string]any{"note": *note, "checklist": checklist}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "submission %s %s\n", id, decision)
	if *approve {
		fmt.Fprintln(stdout, "the publisher can now publish it")
	}
	return nil
}

func catalogueRevoke(ctx context.Context, pool *pgxpool.Pool, args []string, publicURL string, stdout io.Writer) error {
	if len(args) < 2 {
		return errors.New(catalogueUsage)
	}
	conn, ver := args[0], args[1]
	fs := flag.NewFlagSet("catalogue revoke", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	as := fs.String("as", "", "the reviewer's email")
	reason := fs.String("reason", "", "why; installing tenants are told")
	if err := fs.Parse(args[2:]); err != nil || fs.NArg() > 0 || *as == "" || strings.TrimSpace(*reason) == "" {
		return errors.New(catalogueUsage)
	}
	var tenant uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT taskiem_catalogue_revoke($1, $2, $3, $4)`, conn, ver, *as, *reason).Scan(&tenant); err != nil {
		return fmt.Errorf("catalogue revoke: %w", err)
	}
	by := "reviewer:" + strings.ToLower(*as)
	if err := auditIn(ctx, pool, tenant, by, "catalogue.revoke", conn+"@"+ver, map[string]any{"reason": *reason}); err != nil {
		return err
	}
	n, err := catalogue.NotifyRevoked(ctx, pool, conn, ver, *reason, by, publicURL)
	fmt.Fprintf(stdout, "revoked %s %s; alerted %d installing tenant(s); running engines stop using it within a minute\n", conn, ver, n)
	return err
}

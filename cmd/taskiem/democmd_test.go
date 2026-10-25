//go:build e2e

package main

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// `taskiem demo school --create-tenant` makes a business with its four
// people and seeds the school; seeding the same tenant again changes
// nothing; the usage is refused without a tenant or with both.
func TestDemoSchoolCLI(t *testing.T) {
	d := dbtest.New(t)
	t.Setenv("TASKIEM_DATABASE_URL", d.DSN)
	t.Setenv("TASKIEM_LOCAL_KMS_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	for _, bad := range [][]string{{"demo"}, {"demo", "school"}, {"demo", "school", "--create-tenant", "--tenant", "x"}, {"demo", "school", "--tenant", "nope"}} {
		if err := run(bad, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Errorf("%v: no error", bad)
		}
	}
	var out bytes.Buffer
	if err := run([]string{"demo", "school", "--create-tenant", "--name", "Greenfield Demo Academy"}, &out, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	text := out.String()
	m := regexp.MustCompile(`(?m)^tenant ([0-9a-f-]{36})$`).FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no tenant line:\n%s", text)
	}
	for _, want := range []string{"6 classes, 25 families, 40 students, 40 invoices for First term", "40 account numbers on the demo rail", "reconciles: true",
		"proprietor@greenfield-demo-academy.invalid", "bursar@greenfield-demo-academy.invalid", "approver@greenfield-demo-academy.invalid", "gate@greenfield-demo-academy.invalid"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	ctx := context.Background()
	var people, students int
	if err := d.Admin.QueryRow(ctx, `SELECT (SELECT count(DISTINCT user_id) FROM memberships WHERE tenant_id = $1), (SELECT count(*) FROM school_students WHERE tenant_id = $1)`, m[1]).Scan(&people, &students); err != nil {
		t.Fatal(err)
	}
	if people != 4 || students != 40 {
		t.Errorf("%d people, %d students", people, students)
	}
	var roles []string
	if err := d.Admin.QueryRow(ctx, `SELECT array_agg(role ORDER BY role) FROM memberships WHERE tenant_id = $1`, m[1]).Scan(&roles); err != nil {
		t.Fatal(err)
	}
	if strings.Join(roles, ",") != "bursar,gate,owner,owner" {
		t.Errorf("roles %v", roles)
	}
	out.Reset()
	if err := run([]string{"demo", "school", "--tenant", m[1]}, &out, &out); err != nil || !strings.Contains(out.String(), "has the demo school already: nothing changed") {
		t.Fatalf("again: %v\n%s", err, out.String())
	}
	// The same name again would reuse the people's emails: refused, nothing made.
	out.Reset()
	if err := run([]string{"demo", "school", "--create-tenant", "--name", "Greenfield Demo Academy"}, &out, &out); err == nil || !strings.Contains(err.Error(), "another --name") {
		t.Errorf("the same name twice: %v", err)
	}
}

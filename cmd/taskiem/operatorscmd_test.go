package main

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// Operator accounts are made from the CLI only: add prints a one-time
// enrolment link, reset and disable take effect at once, and the list
// shows each account's state.
func TestOperatorsCLI(t *testing.T) {
	d := dbtest.New(t)
	t.Setenv("TASKIEM_DATABASE_URL", d.DSN)
	t.Setenv("TASKIEM_PUBLIC_URL", "https://app.taskiem.test")
	t.Setenv("USER", "ops")

	out, err := cli("operators", "add", "Ada@Taskiem.test", "--name", "Ada")
	if err != nil || !strings.Contains(out, "operator ada@taskiem.test is active") || !strings.Contains(out, "https://app.taskiem.test/ops/enrol#") {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if out, err := cli("operators", "add", "not-an-email"); err == nil {
		t.Errorf("add without an email: %s", out)
	}
	if out, err := cli("operators", "enrol", "ada@taskiem.test", "--ttl", "720h"); err == nil {
		t.Errorf("a month-long link: %s", out)
	}
	if out, err := cli("operators", "enrol", "nobody@taskiem.test"); err == nil || !strings.Contains(err.Error(), "no such operator") {
		t.Errorf("enrol a stranger: %v %s", err, out)
	}
	out, err = cli("operators")
	if err != nil || !strings.Contains(out, "ada@taskiem.test") || !strings.Contains(out, "active") || !strings.Contains(out, "never") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if out, err := cli("operators", "reset", "ada@taskiem.test"); err != nil || !strings.Contains(out, "removed 0 passkey") {
		t.Errorf("reset: %v %s", err, out)
	}
	if out, err := cli("operators", "disable", "ada@taskiem.test"); err != nil || !strings.Contains(out, "disabled") {
		t.Errorf("disable: %v %s", err, out)
	}
	if out, err := cli("operators", "enrol", "ada@taskiem.test"); err == nil {
		t.Errorf("a link for a disabled operator: %s", out)
	}
	if out, _ := cli("operators"); !strings.Contains(out, "disabled") {
		t.Errorf("list after disable:\n%s", out)
	}
	// Adding again reactivates.
	if out, err := cli("operators", "add", "ada@taskiem.test"); err != nil || !strings.Contains(out, "/ops/enrol#") {
		t.Errorf("re-add: %v %s", err, out)
	}
	var n int
	if err := d.Admin.QueryRow(t.Context(), `SELECT count(*) FROM audit_log WHERE tenant_id = 'ffffffff-ffff-ffff-ffff-ffffffffffff' AND actor_id = 'cli:ops'`).Scan(&n); err != nil || n != 6 {
		t.Errorf("platform chain entries by the CLI: %d %v", n, err)
	}
}

package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/secrets"
)

func TestSecretReadsAPIAndReport(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	owner := w.tenant(t, "Acme", "owner@acme.test")
	globex := w.tenant(t, "Globex", "owner@globex.test")
	owner.must(204, "PUT", "/v1/secrets/prod/token", map[string]any{"value": "tok-prod-81"})
	owner.must(204, "PUT", "/v1/secrets/dev/token", map[string]any{"value": "tok-dev-82"})
	owner.must(204, "PUT", "/v1/secrets/prod/idle", map[string]any{"value": "never-read"})
	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM tenants WHERE name = 'Acme'`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	conn, err := w.env.Vault.CreateConnection(ctx, tenant, "prod", "fakepay", "keyed", "api_key", map[string]string{"k": "conn-secret-83"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.Must(uuid.NewV7())
	use := secrets.WithUse(ctx, secrets.Use{Purpose: "step.http", RunID: run, StepID: "post", Attempt: 1})
	for _, env := range []string{"prod", "dev"} {
		if _, err := w.env.Vault.Get(use, tenant, env, "token"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.env.Vault.Credentials(secrets.WithUse(ctx, secrets.Use{Purpose: "step.connector", RunID: run, StepID: "pay", Attempt: 1}), tenant, "prod", "fakepay", "keyed"); err != nil {
		t.Fatal(err)
	}

	count := func(c *client, q string) int {
		t.Helper()
		return len(c.must(200, "GET", "/v1/secrets/reads"+q, nil)["reads"].([]any))
	}
	for q, want := range map[string]int{
		"":                                 3,
		"?name=token":                      2,
		"?name=token&env=dev":              1,
		"?run=" + run.String():             3,
		"?connection=" + conn.String():     1,
		"?kind=connection":                 1,
		"?since=2999-01-01":                0,
		"?until=2000-01-01T00:00:00Z":      0,
		"?limit=1":                         1,
		"?run=" + uuid.NewString():         0,
		"?name=idle":                       0,
		"?env=prod&kind=secret&name=token": 1,
	} {
		if got := count(owner, q); got != want {
			t.Errorf("reads%s: %d, want %d", q, got, want)
		}
	}
	owner.must(400, "GET", "/v1/secrets/reads?run=nope", nil)
	owner.must(400, "GET", "/v1/secrets/reads?since=yesterday", nil)
	if s := toJSON(owner.must(200, "GET", "/v1/secrets/reads", nil)); strings.Contains(s, "tok-") || strings.Contains(s, "conn-secret") {
		t.Errorf("a value in the reads: %s", s)
	}
	if n := count(globex, ""); n != 0 {
		t.Errorf("another tenant sees %d reads", n)
	}

	// A key limited to dev sees dev's reads only; without audit.read, none.
	dev := keyFor(t, owner, "dev", "audit.read")
	if got := dev.must(200, "GET", "/v1/secrets/reads", nil)["reads"].([]any); len(got) != 1 || got[0].(map[string]any)["environment"] != "dev" {
		t.Errorf("dev key: %v", got)
	}
	dev.must(403, "GET", "/v1/secrets/reads?env=prod", nil)
	keyFor(t, owner, "", "secret.manage").must(403, "GET", "/v1/secrets/reads", nil)

	// Last use on the Secrets and Connections lists.
	for _, s := range owner.must(200, "GET", "/v1/secrets", nil)["secrets"].([]any) {
		m := s.(map[string]any)
		if used := m["last_used_at"] != nil; used != (m["name"] == "token") {
			t.Errorf("secret %s/%s last_used_at %v", m["environment"], m["name"], m["last_used_at"])
		}
	}
	for _, c := range owner.must(200, "GET", "/v1/connections", nil)["connections"].([]any) {
		if m := c.(map[string]any); m["id"] == conn.String() && m["last_used_at"] == nil {
			t.Errorf("connection without last use: %v", m)
		}
	}

	// The report: counts per secret per day, unused secrets, and the hourly
	// digests checked against the reads.
	x := &secrets.ReadDigester{Pool: w.env.DB.App, Now: func() time.Time { return time.Now().Add(2 * time.Hour) }}
	if n, err := x.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("digest: %d %v", n, err)
	}
	rep := owner.must(200, "GET", "/v1/reports/secret-use", nil)
	if rows := rep["rows"].([]any); len(rows) != 3 {
		t.Errorf("report rows: %v", rows)
	}
	sum := rep["summary"].(map[string]any)
	if sum["reads"].(float64) != 3 || sum["digests_intact"] != true || sum["digests"].(map[string]any)["ok"].(float64) != 1 {
		t.Errorf("summary: %v", sum)
	}
	unused := toJSON(sum["unused"])
	if !strings.Contains(unused, `"name":"idle"`) || strings.Contains(unused, `"name":"token"`) {
		t.Errorf("unused: %s", unused)
	}
	if csv := owner.raw(t, "GET", "/v1/reports/secret-use?format=csv"); !strings.HasPrefix(csv, "day,kind,environment,name") {
		t.Errorf("csv: %.80s", csv)
	}
}

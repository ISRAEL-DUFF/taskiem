package postgres

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
)

func setup(t *testing.T) (connector.Request, *pgx.Conn) {
	t.Helper()
	dsn := os.Getenv("TASKIEM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TASKIEM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(ctx) })
	_, _ = admin.Exec(ctx, `DROP TABLE IF EXISTS pgconn_topups`)
	if _, err := admin.Exec(ctx, `CREATE TABLE pgconn_topups (id int PRIMARY KEY, ref text, amount_kobo bigint, fee numeric(12,2), at timestamptz DEFAULT '2026-10-05T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, `DROP TABLE IF EXISTS pgconn_topups`) })
	u, _ := url.Parse(dsn)
	pw, _ := u.User.Password()
	guard := &egress.Guard{Blocked: func(a netip.Addr) bool { return !a.IsLoopback() && egress.BlockedAddr(a) }}
	pol := egress.Policy{Hosts: []string{u.Hostname()}}
	req := connector.Request{
		Credentials: map[string]string{"host": u.Hostname(), "port": u.Port(), "database": u.Path[1:], "user": u.User.Username(), "password": pw, "sslmode": "disable"},
		Dial:        func(ctx context.Context, n, a string) (net.Conn, error) { return guard.DialContext(ctx, pol, n, a) },
	}
	return req, admin
}

func TestExecuteThenQuery(t *testing.T) {
	req, _ := setup(t)
	ctx := context.Background()
	c := New()
	if err := connector.NewRegistry().Register(c); err != nil {
		t.Fatal(err)
	}
	req.Input = map[string]any{"sql": `INSERT INTO pgconn_topups (id, ref, amount_kobo, fee) VALUES ($1, $2, $3, 12.5), (2, 'b', 300, 0)`, "params": []any{int64(1), "a", int64(5000000)}}
	r, err := c.Actions["execute"].Execute(ctx, req)
	if err != nil || r.Output.(map[string]any)["rows_affected"] != int64(2) {
		t.Fatalf("execute: %v %v", r.Output, err)
	}
	req.Input = map[string]any{"sql": `SELECT id, ref, amount_kobo, fee, at FROM pgconn_topups ORDER BY id`, "max_rows": int64(1)}
	r, err = c.Actions["query"].Execute(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	out := r.Output.(map[string]any)
	row := out["rows"].([]any)[0].(map[string]any)
	if row["id"] != int64(1) || row["amount_kobo"] != int64(5000000) || row["fee"] != 12.5 || row["at"] != "2026-10-05T10:00:00Z" || out["truncated"] != true {
		t.Errorf("query output %v", out)
	}
}

func TestQueryCannotWrite(t *testing.T) {
	req, _ := setup(t)
	req.Input = map[string]any{"sql": `DELETE FROM pgconn_topups RETURNING id`}
	_, err := New().Actions["query"].Execute(context.Background(), req)
	if effects.Classify(err) != effects.KindFatal {
		t.Errorf("a write through query must be refused: %v", err)
	}
}

func TestBadSQLIsFatalAndEgressIsEnforced(t *testing.T) {
	req, _ := setup(t)
	req.Input = map[string]any{"sql": `SELEC 1`}
	if _, err := New().Actions["query"].Execute(context.Background(), req); effects.Classify(err) != effects.KindFatal {
		t.Errorf("syntax error should be fatal: %v", err)
	}
	strict := &egress.Guard{}
	req.Dial = func(ctx context.Context, n, a string) (net.Conn, error) {
		return strict.DialContext(ctx, egress.Policy{Hosts: []string{req.Credentials["host"]}}, n, a)
	}
	req.Input = map[string]any{"sql": `SELECT 1`}
	if _, err := New().Actions["query"].Execute(context.Background(), req); effects.Classify(err) != effects.KindFatal {
		t.Errorf("loopback database must be refused by the default egress guard: %v", err)
	}
}

package mysql

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/mysql/internal/wire"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
)

// TASKIEM_TEST_MYSQL_DSN is a URL such as
// mysql://root:pw@127.0.0.1:3306/taskiem_test?sslmode=disable
// (sslmode as for the connection; default require).
func setupReal(t *testing.T) connector.Request {
	t.Helper()
	dsn := os.Getenv("TASKIEM_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TASKIEM_TEST_MYSQL_DSN not set")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := u.User.Password()
	guard := &egress.Guard{Blocked: func(a netip.Addr) bool { return !a.IsLoopback() && egress.BlockedAddr(a) }}
	pol := egress.Policy{Hosts: []string{u.Hostname()}}
	req := connector.Request{
		Credentials: map[string]string{"host": u.Hostname(), "port": u.Port(), "database": strings.TrimPrefix(u.Path, "/"), "user": u.User.Username(), "password": pw, "sslmode": u.Query().Get("sslmode")},
		Dial:        func(ctx context.Context, n, a string) (net.Conn, error) { return guard.DialContext(ctx, pol, n, a) },
	}
	ctx := context.Background()
	admin, err := connect(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(c *wire.Conn, sql string) {
		if _, err := c.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(admin, "DROP TABLE IF EXISTS myconn_topups")
	exec(admin, "CREATE TABLE myconn_topups (id INT PRIMARY KEY, ref VARCHAR(20), amount_kobo BIGINT, fee DECIMAL(12,2), at TIMESTAMP(3) NULL DEFAULT '2026-10-05 10:00:00', raw VARBINARY(4), doc JSON, flag BIT(1))")
	_ = admin.Close()
	t.Cleanup(func() {
		if c, err := connect(ctx, req); err == nil {
			_, _ = c.Exec(ctx, "DROP TABLE IF EXISTS myconn_topups")
			_ = c.Close()
		}
	})
	return req
}

func TestRealExecuteThenQuery(t *testing.T) {
	req := setupReal(t)
	ctx := context.Background()
	c := New()
	req.Input = map[string]any{"sql": "INSERT INTO myconn_topups (id, ref, amount_kobo, fee, raw, doc, flag) VALUES (?, ?, ?, 12.5, x'00ff', ?, b'1'), (2, 'b', 300, 0, NULL, NULL, b'0')", "params": []any{int64(1), "a'; --", int64(5000000), map[string]any{"k": "v"}}}
	r, err := c.Actions["execute"].Execute(ctx, req)
	if err != nil || r.Output.(map[string]any)["rows_affected"] != int64(2) {
		t.Fatalf("execute: %v %v", r.Output, err)
	}
	req.Input = map[string]any{"sql": "UPDATE myconn_topups SET ref = ref WHERE id = ?", "params": []any{int64(1)}}
	r, err = c.Actions["execute"].Execute(ctx, req)
	if err != nil || r.Output.(map[string]any)["rows_affected"] != int64(1) {
		t.Errorf("an unchanged matched row counts, as in postgres: %v %v", r.Output, err)
	}
	req.Input = map[string]any{"sql": "SELECT id, ref, amount_kobo, fee, at, raw, doc, flag FROM myconn_topups ORDER BY id", "max_rows": int64(1)}
	r, err = c.Actions["query"].Execute(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	out := r.Output.(map[string]any)
	row := out["rows"].([]any)[0].(map[string]any)
	if row["id"] != int64(1) || row["ref"] != "a'; --" || row["amount_kobo"] != int64(5000000) || row["fee"] != "12.50" ||
		row["at"] != "2026-10-05T10:00:00Z" || row["raw"] != "AP8=" || row["flag"] != int64(1) || out["truncated"] != true {
		t.Errorf("query output %v", out)
	}
	if doc, _ := row["doc"].(map[string]any); doc["k"] != "v" {
		t.Errorf("json %v", row["doc"])
	}
}

func TestRealQueryCannotWrite(t *testing.T) {
	req := setupReal(t)
	for _, sql := range []string{"DELETE FROM myconn_topups", "CREATE TABLE myconn_other (id INT)", "INSERT INTO myconn_topups (id) VALUES (9)"} {
		req.Input = map[string]any{"sql": sql}
		_, err := New().Actions["query"].Execute(context.Background(), req)
		if effects.Classify(err) != effects.KindFatal {
			t.Errorf("%s through query must be refused: %v", sql, err)
		}
	}
}

func TestRealBadSQLIsFatal(t *testing.T) {
	req := setupReal(t)
	req.Input = map[string]any{"sql": "SELEC 1"}
	if _, err := New().Actions["query"].Execute(context.Background(), req); effects.Classify(err) != effects.KindFatal {
		t.Errorf("syntax error should be fatal: %v", err)
	}
	req.Input = map[string]any{"sql": "INSERT INTO myconn_topups (id) VALUES (1), (1)"}
	if _, err := New().Actions["execute"].Execute(context.Background(), req); effects.Classify(err) != effects.KindFatal {
		t.Errorf("duplicate key should be fatal: %v", err)
	}
}

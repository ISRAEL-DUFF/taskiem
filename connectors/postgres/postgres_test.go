package postgres

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"
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
	if row["id"] != int64(1) || row["amount_kobo"] != int64(5000000) || row["fee"] != "12.50" || row["at"] != "2026-10-05T10:00:00Z" || out["truncated"] != true {
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

func TestDefaultSSLModeVerifiesTheServer(t *testing.T) {
	creds := map[string]string{"host": "db.example.test", "database": "app", "user": "taskiem", "password": "pw"}
	cfg, err := config(creds)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLSConfig == nil || cfg.TLSConfig.InsecureSkipVerify || cfg.TLSConfig.ServerName != "db.example.test" || len(cfg.Fallbacks) != 0 {
		t.Errorf("default TLS does not verify the server: %+v, %d fallbacks", cfg.TLSConfig, len(cfg.Fallbacks))
	}
	// An explicit mode is kept: require encrypts without verifying.
	creds["sslmode"] = "require"
	if cfg, err = config(creds); err != nil || cfg.TLSConfig == nil || !cfg.TLSConfig.InsecureSkipVerify {
		t.Errorf("require: %v %+v", err, cfg)
	}
	// A bad setting never echoes the password.
	creds["sslmode"], creds["password"] = "sometimes", "pw-not-to-be-seen"
	if _, err := config(creds); err == nil || strings.Contains(err.Error(), "pw-not-to-be-seen") {
		t.Errorf("bad sslmode: %v", err)
	}
}

// max_rows as the worker delivers it: a step's input is decoded with
// encoding/json, so a number is a float64. It was read as int64 only, so
// the cap was always the default 1000.
func TestMaxRowsAsTheWorkerDeliversIt(t *testing.T) {
	req, admin := setup(t)
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `INSERT INTO pgconn_topups (id, ref, amount_kobo, fee) SELECT g, 'r', g, 0 FROM generate_series(1, 5) g`); err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(`{"sql": "SELECT id FROM pgconn_topups ORDER BY id", "max_rows": 2}`), &input); err != nil {
		t.Fatal(err)
	}
	req.Input = input
	r, err := New().Actions["query"].Execute(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if out := r.Output.(map[string]any); out["row_count"] != 2 || out["truncated"] != true {
		t.Errorf("max_rows 2 returned %v rows, truncated %v", out["row_count"], out["truncated"])
	}
	for in, want := range map[any]int{float64(7): 7, int64(7): 7, 7: 7, json.Number("7"): 7, float64(0): 1000, float64(2.5): 1000, float64(1e9): 10000, nil: 1000, "7": 1000} {
		if got := maxRows(map[string]any{"max_rows": in}); got != want {
			t.Errorf("maxRows(%T %v) = %d, want %d", in, in, got, want)
		}
	}
}

// A NUMERIC with a fraction is returned as its exact decimal text, never
// through a float64: money columns keep every digit.
func TestNumericIsExact(t *testing.T) {
	req, _ := setup(t)
	req.Input = map[string]any{"sql": `SELECT 0.1::numeric + 0.2::numeric AS sum, 12345678901234567890.123456789::numeric AS big, 12.50::numeric(12,2) AS fee,
		42::numeric AS whole, 1e20::numeric AS huge, 'NaN'::numeric AS nan, NULL::numeric AS none, -0.05::numeric AS neg`}
	r, err := New().Actions["query"].Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	row := r.Output.(map[string]any)["rows"].([]any)[0].(map[string]any)
	want := map[string]any{"sum": "0.3", "big": "12345678901234567890.123456789", "fee": "12.50", "whole": int64(42), "huge": "100000000000000000000", "nan": "NaN", "none": nil, "neg": "-0.05"}
	for k, w := range want {
		if row[k] != w {
			t.Errorf("%s = %#v, want %#v", k, row[k], w)
		}
	}
}

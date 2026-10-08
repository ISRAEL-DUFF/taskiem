package mysql

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	fs "github.com/israel-duff/taskiem/connectors/mysql/internal/fakeserver"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
)

const host = "db.example.test"

// fakeRequest points a request at s: the dialler checks it was asked for
// ${connection.host}:port and connects to the fake instead.
func fakeRequest(t *testing.T, s *fs.Server, creds map[string]string) connector.Request {
	t.Helper()
	if s.User == "" {
		s.User, s.Password, s.Database = "app", "pw", "shop"
	}
	addr := s.Start(t)
	c := map[string]string{"host": host, "database": "shop", "user": "app", "password": "pw", "sslmode": "disable"}
	for k, v := range creds {
		c[k] = v
	}
	return connector.Request{
		Credentials: c,
		Dial: func(ctx context.Context, network, a string) (net.Conn, error) {
			if network != "tcp" || a != net.JoinHostPort(host, "3306") {
				return nil, fmt.Errorf("dialled %s %s: %w", network, a, effects.ErrFatal)
			}
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		},
	}
}

func run(t *testing.T, action string, req connector.Request, in map[string]any) (map[string]any, error) {
	t.Helper()
	req.Input = in
	r, err := New().Actions[action].Execute(context.Background(), req)
	if err != nil {
		return nil, err
	}
	return r.Output.(map[string]any), nil
}

func TestManifestRegisters(t *testing.T) {
	c := New()
	if err := connector.NewRegistry().Register(c); err != nil {
		t.Fatal(err)
	}
	if c.Manifest.Actions["query"].Class != effects.Read || c.Manifest.Actions["execute"].Class != effects.UnsafeWrite {
		t.Errorf("classes must match the postgres connector")
	}
}

func TestQueryDecodesEveryCommonType(t *testing.T) {
	sql := "SELECT * FROM everything WHERE id = ?"
	s := &fs.Server{Statements: map[string]*fs.Result{sql: {
		Params: 1,
		Columns: []fs.Column{
			{Name: "tiny", Type: fs.TTiny}, {Name: "small_u", Type: fs.TShort, Unsigned: true},
			{Name: "int", Type: fs.TLong}, {Name: "big", Type: fs.TLongLong},
			{Name: "big_u", Type: fs.TLongLong, Unsigned: true}, {Name: "f", Type: fs.TFloat},
			{Name: "d", Type: fs.TDouble}, {Name: "dec", Type: fs.TNewDecimal},
			{Name: "date", Type: fs.TDate}, {Name: "dt", Type: fs.TDateTime}, {Name: "ts", Type: fs.TTimestamp},
			{Name: "zero_dt", Type: fs.TDateTime}, {Name: "tm", Type: fs.TTime},
			{Name: "name", Type: fs.TVarString}, {Name: "bin", Type: fs.TBlob, Binary: true},
			{Name: "doc", Type: fs.TJSON}, {Name: "flag", Type: fs.TBit}, {Name: "yr", Type: fs.TYear},
			{Name: "state", Type: fs.TString}, {Name: "nothing", Type: fs.TVarString},
		},
		Rows: [][]any{{
			int64(-5), uint64(65535), int64(-2147483648), int64(5000000),
			uint64(18446744073709551615), float32(0.1),
			2.5, "12.50",
			fs.DT{Y: 2026, Mo: 10, D: 5}, fs.DT{Y: 2026, Mo: 10, D: 5, H: 10, Mi: 1, S: 2, Us: 300000}, fs.DT{Y: 2026, Mo: 10, D: 5, H: 10},
			fs.DT{}, fs.TM{Neg: true, Days: 1, H: 2, M: 3, S: 4},
			"Adé", []byte{0, 1, 2},
			`{"a":1,"b":[1.5,"x"],"big":12345678901234}`, []byte{0x01, 0x02}, uint64(2026),
			"active", nil,
		}},
		Check: func(a []any) error {
			if a[0] != int64(7) {
				return fmt.Errorf("id %#v", a[0])
			}
			return nil
		},
	}}}
	req := fakeRequest(t, s, nil)
	out, err := run(t, "query", req, map[string]any{"sql": sql, "params": []any{int64(7)}})
	if err != nil {
		t.Fatal(err)
	}
	row := out["rows"].([]any)[0].(map[string]any)
	want := map[string]any{
		"tiny": int64(-5), "small_u": int64(65535), "int": int64(-2147483648), "big": int64(5000000),
		"big_u": "18446744073709551615", "f": 0.1, "d": 2.5, "dec": "12.50",
		"date": "2026-10-05", "dt": "2026-10-05T10:01:02.3Z", "ts": "2026-10-05T10:00:00Z",
		"zero_dt": "0000-00-00 00:00:00", "tm": "-26:03:04",
		"name": "Adé", "bin": "AAEC",
		"doc":  map[string]any{"a": int64(1), "b": []any{1.5, "x"}, "big": int64(12345678901234)},
		"flag": int64(258), "yr": int64(2026), "state": "active", "nothing": nil,
	}
	for k, v := range want {
		if !reflect.DeepEqual(row[k], v) {
			t.Errorf("%s = %#v, want %#v", k, row[k], v)
		}
	}
	if len(row) != len(want) || out["row_count"] != 1 || out["truncated"] != false {
		t.Errorf("output %v", out)
	}
}

func TestQueryRunsReadOnlyWithTimeouts(t *testing.T) {
	sql := "DELETE FROM topups"
	s := &fs.Server{Statements: map[string]*fs.Result{sql: {Write: true, AffectedRows: 3}}}
	req := fakeRequest(t, s, nil)
	_, err := run(t, "query", req, map[string]any{"sql": sql})
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "1792") {
		t.Errorf("a write through query must be refused: %v", err)
	}
	log := s.Log()
	want := []string{
		"query: SET SESSION time_zone = '+00:00', max_execution_time = 30000, lock_wait_timeout = 30, innodb_lock_wait_timeout = 30",
		"query: SET SESSION TRANSACTION READ ONLY",
		"query: START TRANSACTION READ ONLY",
		"prepare: DELETE FROM topups",
		"execute: DELETE FROM topups []",
	}
	if len(log) < len(want)+1 || !reflect.DeepEqual(log[1:len(want)+1], want) {
		t.Errorf("server saw:\n%s", strings.Join(log, "\n"))
	}
}

func TestQueryOnMariaDBUsesMaxStatementTime(t *testing.T) {
	s := &fs.Server{Version: "5.5.5-10.11.6-MariaDB", Statements: map[string]*fs.Result{"SELECT 1": {Columns: []fs.Column{{Name: "1", Type: fs.TLongLong}}, Rows: [][]any{{int64(1)}}}}}
	req := fakeRequest(t, s, nil)
	if _, err := run(t, "query", req, map[string]any{"sql": "SELECT 1"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(s.Log(), "\n"), "max_statement_time = 30,") {
		t.Errorf("log %v", s.Log())
	}
}

func TestQueryTruncatesAtMaxRows(t *testing.T) {
	var rows [][]any
	for i := range 50 {
		rows = append(rows, []any{int64(i)})
	}
	s := &fs.Server{Statements: map[string]*fs.Result{"SELECT id FROM t": {Columns: []fs.Column{{Name: "id", Type: fs.TLongLong}}, Rows: rows}}}
	req := fakeRequest(t, s, nil)
	out, err := run(t, "query", req, map[string]any{"sql": "SELECT id FROM t", "max_rows": int64(2)})
	if err != nil {
		t.Fatal(err)
	}
	if out["row_count"] != 2 || out["truncated"] != true || out["rows"].([]any)[1].(map[string]any)["id"] != int64(1) {
		t.Errorf("output %v", out)
	}
	out, err = run(t, "query", req, map[string]any{"sql": "SELECT id FROM t", "max_rows": int64(50)})
	if err != nil || out["row_count"] != 50 || out["truncated"] != false {
		t.Errorf("exactly max_rows is not truncated: %v %v", out["row_count"], err)
	}
}

func TestParamsAreBoundNeverInterpolated(t *testing.T) {
	sql := "INSERT INTO topups (id, ref, amount, fee, meta, ok, gone) VALUES (?, ?, ?, ?, ?, ?, ?)"
	evil := "x'); DROP TABLE topups; --"
	s := &fs.Server{Statements: map[string]*fs.Result{sql: {
		Params: 7, AffectedRows: 1, LastInsertID: 41,
		Check: func(a []any) error {
			want := []any{int64(1), evil, int64(5000000), 12.5, `{"k":"v"}`, int64(1), nil}
			if !reflect.DeepEqual(a, want) {
				return fmt.Errorf("got %#v", a)
			}
			return nil
		},
	}}}
	req := fakeRequest(t, s, nil)
	out, err := run(t, "execute", req, map[string]any{"sql": sql, "params": []any{int64(1), evil, int64(5000000), 12.5, map[string]any{"k": "v"}, true, nil}})
	if err != nil {
		t.Fatal(err)
	}
	if out["rows_affected"] != int64(1) || out["last_insert_id"] != int64(41) {
		t.Errorf("output %v", out)
	}
	for _, l := range s.Log() {
		if strings.HasPrefix(l, "query:") && strings.Contains(l, "DROP") {
			t.Errorf("a parameter reached the text protocol: %s", l)
		}
	}
	if _, err := run(t, "execute", req, map[string]any{"sql": sql, "params": []any{struct{}{}}}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("unsupported param type: %v", err)
	}
	if _, err := run(t, "execute", req, map[string]any{"sql": sql, "params": []any{int64(1)}}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("wrong parameter count: %v", err)
	}
}

func TestErrorClassification(t *testing.T) {
	s := &fs.Server{Statements: map[string]*fs.Result{
		"INSERT dup":         {Err: &fs.Error{Code: 1062, State: "23000", Msg: "Duplicate entry '1' for key 'PRIMARY'"}},
		"UPDATE deadlock":    {Err: &fs.Error{Code: 1213, State: "40001", Msg: "Deadlock found when trying to get lock"}},
		"UPDATE lost":        {DropOnExecute: true},
		"UPDATE early":       {DropOnPrepare: true},
		"SELECT lost":        {DropOnExecute: true},
		"SELECT unsupported": {PrepareErr: &fs.Error{Code: 1295, State: "HY000", Msg: "This command is not supported in the prepared statement protocol yet"}},
	}}
	req := fakeRequest(t, s, nil)
	for _, tc := range []struct {
		action, sql string
		want        effects.ErrorKind
		text        string
	}{
		{"query", "SELEC 1", effects.KindFatal, "1064 (42000)"},
		{"execute", "INSERT dup", effects.KindFatal, "Duplicate entry"},
		{"execute", "UPDATE deadlock", effects.KindRetryable, "1213"},
		{"execute", "UPDATE lost", effects.KindUnknownOutcome, ""},
		{"execute", "UPDATE early", effects.KindNotSent, ""},
		{"query", "SELECT lost", effects.KindRetryable, ""},
		{"query", "SELECT unsupported", effects.KindFatal, "1295"},
	} {
		_, err := run(t, tc.action, req, map[string]any{"sql": tc.sql})
		if got := effects.Classify(err); got != tc.want || !strings.Contains(fmt.Sprint(err), tc.text) {
			t.Errorf("%s %q: %v (%v), want %v", tc.action, tc.sql, got, err, tc.want)
		}
	}
}

func TestLoginErrors(t *testing.T) {
	s := &fs.Server{User: "app", Password: "other", Database: "shop"}
	req := fakeRequest(t, s, nil)
	if _, err := run(t, "query", req, map[string]any{"sql": "SELECT 1"}); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "1045") {
		t.Errorf("bad password: %v", err)
	}
	req.Credentials = map[string]string{"host": host, "user": "app"}
	if _, err := run(t, "query", req, map[string]any{"sql": "SELECT 1"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("missing database: %v", err)
	}
	req.Credentials = map[string]string{"host": host, "user": "app", "database": "shop", "sslmode": "prefer"}
	if _, err := run(t, "query", req, map[string]any{"sql": "SELECT 1"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("unknown sslmode: %v", err)
	}
	// Nothing listening: refused before anything was sent.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	req = connector.Request{
		Credentials: map[string]string{"host": host, "user": "app", "database": "shop", "sslmode": "disable"},
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			c, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", err, effects.ErrNotSent)
			}
			return c, nil
		},
	}
	if _, err := run(t, "execute", req, map[string]any{"sql": "UPDATE t SET a = 1"}); effects.Classify(err) != effects.KindNotSent {
		t.Errorf("connection refused: %v", err)
	}
}

func TestTLSModes(t *testing.T) {
	srvTLS, caPEM, err := fs.TLSConfig(host)
	if err != nil {
		t.Fatal(err)
	}
	ok := map[string]*fs.Result{"SELECT 1": {Columns: []fs.Column{{Name: "1", Type: fs.TLongLong}}, Rows: [][]any{{int64(1)}}}}
	for _, tc := range []struct {
		name  string
		creds map[string]string
		full  bool
		want  effects.ErrorKind // -1: success
	}{
		{"default is require", map[string]string{"sslmode": ""}, true, -1},
		{"verify-full with CA", map[string]string{"sslmode": "verify-full", "ssl_ca": string(caPEM)}, true, -1},
		{"verify-ca with CA", map[string]string{"sslmode": "verify-ca", "ssl_ca": string(caPEM)}, false, -1},
		{"require with CA verifies", map[string]string{"sslmode": "require", "ssl_ca": string(caPEM)}, false, -1},
		{"verify-full, unknown CA", map[string]string{"sslmode": "verify-full"}, false, effects.KindFatal},
		{"bad ssl_ca", map[string]string{"sslmode": "verify-full", "ssl_ca": "nope"}, false, effects.KindFatal},
		{"disable against require_secure_transport", map[string]string{"sslmode": "disable"}, false, effects.KindFatal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &fs.Server{TLS: srvTLS.Clone(), RequireTLS: true, FullAuth: tc.full, Statements: ok}
			req := fakeRequest(t, s, tc.creds)
			out, err := run(t, "query", req, map[string]any{"sql": "SELECT 1"})
			if tc.want == -1 {
				if err != nil || out["row_count"] != 1 {
					t.Fatalf("%v %v", out, err)
				}
				if !strings.Contains(s.Log()[0], "tls=true") {
					t.Errorf("not encrypted: %v", s.Log())
				}
				return
			}
			if effects.Classify(err) != tc.want {
				t.Errorf("%v", err)
			}
		})
	}
}

func TestTLSConfigShapes(t *testing.T) {
	c, err := tlsConfig(map[string]string{"host": host, "sslmode": "verify-full"})
	if err != nil || c.InsecureSkipVerify || c.ServerName != host || c.MinVersion != tls.VersionTLS12 {
		t.Errorf("verify-full %+v %v", c, err)
	}
	if c, _ := tlsConfig(map[string]string{"sslmode": "disable"}); c != nil {
		t.Error("disable must not use TLS")
	}
}

func TestStatementTimeoutGivesUnknownOutcomeForWrites(t *testing.T) {
	s := &fs.Server{Statements: map[string]*fs.Result{"UPDATE slow": {Hang: true}, "SELECT slow": {Hang: true}}}
	req := fakeRequest(t, s, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req.Input = map[string]any{"sql": "UPDATE slow"}
	_, err := New().Actions["execute"].Execute(ctx, req)
	if effects.Classify(err) != effects.KindUnknownOutcome || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("write timeout: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req.Input = map[string]any{"sql": "SELECT slow"}
	_, err = New().Actions["query"].Execute(ctx, req)
	if effects.Classify(err) != effects.KindRetryable {
		t.Errorf("read timeout: %v", err)
	}
}

func TestExecuteCountsResultRows(t *testing.T) {
	s := &fs.Server{NoDeprecateEOF: true, Statements: map[string]*fs.Result{
		"SELECT id FROM t FOR UPDATE": {Columns: []fs.Column{{Name: "id", Type: fs.TLong}}, Rows: [][]any{{int64(1)}, {int64(2)}}},
	}}
	req := fakeRequest(t, s, nil)
	out, err := run(t, "execute", req, map[string]any{"sql": "SELECT id FROM t FOR UPDATE"})
	if err != nil || out["rows_affected"] != int64(2) {
		t.Errorf("%v %v", out, err)
	}
}

func TestEgressIsEnforced(t *testing.T) {
	s := &fs.Server{}
	addr := s.Start(t)
	h, p, _ := net.SplitHostPort(addr)
	strict := &egress.Guard{}
	req := connector.Request{
		Credentials: map[string]string{"host": h, "port": p, "database": "shop", "user": "app", "sslmode": "disable"},
		Dial: func(ctx context.Context, n, a string) (net.Conn, error) {
			return strict.DialContext(ctx, egress.Policy{Hosts: []string{h}}, n, a)
		},
	}
	if _, err := run(t, "query", req, map[string]any{"sql": "SELECT 1"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("a loopback database must be refused by the default egress guard: %v", err)
	}
	if len(s.Log()) != 0 {
		t.Errorf("the server was reached: %v", s.Log())
	}
}

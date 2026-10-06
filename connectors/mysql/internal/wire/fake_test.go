package wire_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	fs "github.com/israel-duff/taskiem/connectors/mysql/internal/fakeserver"
	"github.com/israel-duff/taskiem/connectors/mysql/internal/wire"
)

func dial(t *testing.T, s *fs.Server, cfg wire.Config) (*wire.Conn, error) {
	t.Helper()
	addr := s.Start(t)
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return wire.Connect(ctx, nc, cfg)
}

func tlsPair(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	srv, caPEM, err := fs.TLSConfig("db.example.test")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	return srv, &tls.Config{RootCAs: pool, ServerName: "db.example.test", MinVersion: tls.VersionTLS12}
}

func TestLoginMatrix(t *testing.T) {
	cases := []struct {
		name   string
		server fs.Server
		tls    bool
		log    string
	}{
		{name: "native", server: fs.Server{DefaultPlugin: wire.PluginNative}},
		{name: "caching_sha2 fast", server: fs.Server{}},
		{name: "caching_sha2 full over TLS", server: fs.Server{FullAuth: true}, tls: true},
		{name: "caching_sha2 full with RSA", server: fs.Server{FullAuth: true}, log: "rsa password exchange"},
		{name: "switch native to sha2", server: fs.Server{DefaultPlugin: wire.PluginNative, AccountPlugin: wire.PluginCachingSHA2}, log: "auth switch to caching_sha2_password"},
		{name: "switch sha2 to native", server: fs.Server{AccountPlugin: wire.PluginNative}, log: "auth switch to mysql_native_password"},
		{name: "clear over TLS", server: fs.Server{AccountPlugin: wire.PluginClear}, tls: true},
		{name: "unknown default plugin", server: fs.Server{DefaultPlugin: "client_ed25519", AccountPlugin: wire.PluginNative}},
		{name: "legacy EOF", server: fs.Server{NoDeprecateEOF: true}},
	}
	for _, tc := range cases {
		for _, pw := range []string{"s3cret!", ""} {
			t.Run(fmt.Sprintf("%s/pw=%q", tc.name, pw), func(t *testing.T) {
				s := tc.server
				s.User, s.Password, s.Database = "app", pw, "shop"
				cfg := wire.Config{User: "app", Password: pw, Database: "shop", Attrs: map[string]string{"program_name": "taskiem"}}
				if tc.tls {
					s.TLS, cfg.TLS = tlsPair(t)
				}
				c, err := dial(t, &s, cfg)
				if err != nil {
					t.Fatal(err)
				}
				if c.TLS() != tc.tls {
					t.Errorf("TLS %v", c.TLS())
				}
				if err := c.Ping(context.Background()); err != nil {
					t.Fatal(err)
				}
				_ = c.Close()
				waitLog(t, &s, "quit")
				log := strings.Join(s.Log(), "\n")
				if !strings.Contains(log, "attrs=map[program_name:taskiem]") || (tc.log != "" && !strings.Contains(log, tc.log)) {
					t.Errorf("log:\n%s", log)
				}
			})
		}
	}
}

func waitLog(t *testing.T, s *fs.Server, line string) {
	t.Helper()
	for range 200 {
		if slices.Contains(s.Log(), line) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("server never logged %q: %v", line, s.Log())
}

func TestLoginFailures(t *testing.T) {
	var se *wire.ServerError
	s := &fs.Server{User: "app", Password: "right"}
	_, err := dial(t, s, wire.Config{User: "app", Password: "wrong"})
	if !errors.As(err, &se) || se.Code != 1045 || se.SQLState != "28000" {
		t.Errorf("wrong password: %v", err)
	}
	s = &fs.Server{User: "app", Password: "right", FullAuth: true}
	_, err = dial(t, s, wire.Config{User: "app", Password: "wrong"})
	if !errors.As(err, &se) || se.Code != 1045 {
		t.Errorf("wrong password, RSA path: %v", err)
	}
	srvTLS, _ := tlsPair(t)
	s = &fs.Server{User: "app", TLS: srvTLS, RequireTLS: true}
	_, err = dial(t, s, wire.Config{User: "app"})
	if !errors.As(err, &se) || se.Code != 3159 {
		t.Errorf("insecure transport: %v", err)
	}
	_, cliTLS := tlsPair(t)
	s = &fs.Server{User: "app"}
	if _, err = dial(t, s, wire.Config{User: "app", TLS: cliTLS}); !errors.Is(err, wire.ErrConfig) {
		t.Errorf("TLS wanted, server has none: %v", err)
	}
	s = &fs.Server{User: "app", TLS: srvTLS}
	if _, err = dial(t, s, wire.Config{User: "app", TLS: cliTLS}); !errors.Is(err, wire.ErrConfig) {
		t.Errorf("certificate from an unknown CA: %v", err)
	}
	s = &fs.Server{User: "app", Password: "pw", AccountPlugin: wire.PluginClear}
	if _, err = dial(t, s, wire.Config{User: "app", Password: "pw"}); !errors.Is(err, wire.ErrConfig) {
		t.Errorf("cleartext without TLS must be refused: %v", err)
	}
	if strings.Contains(strings.Join(s.Log(), " "), "pw") {
		t.Error("password leaked")
	}
	s = &fs.Server{User: "app", AccountPlugin: "auth_gssapi_client"}
	if _, err = dial(t, s, wire.Config{User: "app"}); !errors.Is(err, wire.ErrConfig) {
		t.Errorf("unknown plugin: %v", err)
	}
}

func TestNotAMySQLServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_, _ = io.WriteString(c, "HTTP/1.1 400 Bad Request\r\n\r\n")
			_ = c.Close()
		}
	}()
	nc, _ := net.Dial("tcp", ln.Addr().String())
	_, err = wire.Connect(context.Background(), nc, wire.Config{User: "x"})
	if err == nil {
		t.Fatal("connected to an HTTP server")
	}
}

func TestPreparedStatementRoundTrip(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacyEOF=%v", legacy), func(t *testing.T) {
			s := fs.Server{User: "app", NoDeprecateEOF: legacy, Statements: map[string]*fs.Result{
				"SELECT id, name, at, d, dur FROM t WHERE id > ? AND name <> ?": {
					Params: 2,
					Columns: []fs.Column{
						{Name: "id", Type: fs.TLongLong}, {Name: "name", Type: fs.TVarString},
						{Name: "at", Type: fs.TDateTime}, {Name: "d", Type: fs.TNewDecimal}, {Name: "dur", Type: fs.TTime},
					},
					Rows: [][]any{
						{int64(1), "a", fs.DT{Y: 2026, Mo: 10, D: 5, H: 10}, "12.50", fs.TM{H: 1, M: 2, S: 3}},
						{int64(2), nil, fs.DT{}, nil, fs.TM{Neg: true, Days: 1, Us: 5}},
					},
					Check: func(a []any) error {
						if a[0] != int64(0) || a[1] != "x' OR '1'='1" {
							return fmt.Errorf("args %#v", a)
						}
						return nil
					},
				},
				"CALL p()": {AffectedRows: 3},
			}}
			c, err := dial(t, &s, wire.Config{User: "app"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close() }()
			ctx := context.Background()
			st, err := c.Prepare(ctx, "SELECT id, name, at, d, dur FROM t WHERE id > ? AND name <> ?")
			if err != nil {
				t.Fatal(err)
			}
			if st.NumParams() != 2 || len(st.Columns) != 5 {
				t.Fatalf("metadata %d %d", st.NumParams(), len(st.Columns))
			}
			rows, err := st.Execute(ctx, []any{int64(0), "x' OR '1'='1"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for {
				r, err := rows.Next()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, fmt.Sprintf("%v", r))
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			want := []string{
				"[1 [97] 2026-10-05 10:00:00 12.50 01:02:03]",
				"[2 <nil> 0000-00-00 00:00:00 <nil> -24:00:00.000005]",
			}
			if !slices.Equal(got, want) {
				t.Errorf("rows\n%v\nwant\n%v", got, want)
			}
			if err := st.Close(ctx); err != nil {
				t.Fatal(err)
			}
			res, err := c.Exec(ctx, "START TRANSACTION")
			if err != nil || c.Status()&wire.StatusInTrans == 0 {
				t.Errorf("status after START TRANSACTION: %+v %v", res, err)
			}
			st, err = c.Prepare(ctx, "CALL p()")
			if err != nil {
				t.Fatal(err)
			}
			rows, err = st.Execute(ctx, nil, nil)
			if err != nil || rows.Close() != nil || rows.Result().AffectedRows != 3 {
				t.Errorf("OK result: %+v %v", rows.Result(), err)
			}
			_, err = c.Prepare(ctx, "SELEC 1")
			var se *wire.ServerError
			if !errors.As(err, &se) || se.Code != 1064 {
				t.Errorf("syntax error: %v", err)
			}
			if err := c.Ping(ctx); err != nil {
				t.Errorf("a server error leaves the connection usable: %v", err)
			}
		})
	}
}

func TestTextResultSet(t *testing.T) {
	s := fs.Server{User: "app", Statements: map[string]*fs.Result{
		"SELECT @@version, NULL": {Columns: []fs.Column{{Name: "v", Type: fs.TVarString}, {Name: "n", Type: fs.TNull}}, Rows: [][]any{{"8.0.36", nil}}},
	}}
	c, err := dial(t, &s, wire.Config{User: "app"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := c.Query(context.Background(), "SELECT @@version, NULL")
	if err != nil {
		t.Fatal(err)
	}
	r, err := rows.Next()
	if err != nil || string(r[0].([]byte)) != "8.0.36" || r[1] != nil {
		t.Fatalf("%v %v", r, err)
	}
	if _, err := rows.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("end: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

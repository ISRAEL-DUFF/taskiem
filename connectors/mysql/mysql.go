// Package mysql is the first-party MySQL / MariaDB connector. It mirrors
// the PostgreSQL connector: a read-only query action and an unsafe_write
// execute action. The protocol client is a clean-room implementation in
// internal/wire (the common Go driver is MPL-2.0, which the licence policy
// does not allow in linked code).
package mysql

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"time"

	"github.com/israel-duff/taskiem/connectors/mysql/internal/wire"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// StatementTimeout bounds every statement. The server enforces it for
// SELECTs (max_execution_time, or max_statement_time on MariaDB); the
// client gives up on any statement a little later.
const StatementTimeout = 30 * time.Second

// ConnectTimeout bounds dialling, TLS and login.
const ConnectTimeout = 10 * time.Second

// clientGrace is how much longer than StatementTimeout the client waits,
// so the server's own timeout normally fires first and reports cleanly.
const clientGrace = 5 * time.Second

func New() *connector.Connector {
	return &connector.Connector{Manifest: connector.MustParse(manifest), Actions: map[string]connector.Action{
		"query":   connector.ActionFunc(query),
		"execute": connector.ActionFunc(execute),
	}}
}

func tlsConfig(c map[string]string) (*tls.Config, error) {
	mode := c["sslmode"]
	if mode == "" {
		mode = "require"
	}
	if mode == "disable" {
		return nil, nil
	}
	var roots *x509.CertPool // nil: the system pool
	if ca := c["ssl_ca"]; ca != "" {
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(ca)) {
			return nil, fmt.Errorf("ssl_ca holds no PEM certificate: %w", effects.ErrFatal)
		}
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: c["host"], RootCAs: roots}
	switch mode {
	case "verify-full":
		return cfg, nil
	case "require":
		if roots == nil {
			// As libpq and the postgres connector do: encrypt, but do not
			// verify the certificate unless a CA is given.
			cfg.InsecureSkipVerify = true // #nosec G402 -- sslmode=require semantics; verify-full verifies
			return cfg, nil
		}
		fallthrough
	case "verify-ca":
		// Verify the chain but not the host name.
		cfg.InsecureSkipVerify = true // #nosec G402 -- chain verified in VerifyConnection below
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("server sent no certificate")
			}
			opts := x509.VerifyOptions{Roots: roots, Intermediates: x509.NewCertPool()}
			for _, ic := range cs.PeerCertificates[1:] {
				opts.Intermediates.AddCert(ic)
			}
			_, err := cs.PeerCertificates[0].Verify(opts)
			return err
		}
		return cfg, nil
	}
	return nil, fmt.Errorf("unsupported sslmode %q (use require, verify-ca, verify-full or disable): %w", mode, effects.ErrFatal)
}

// setupSQL sets the session: UTC for TIMESTAMP values, the statement
// timeout, and bounded lock waits.
func setupSQL(mariaDB bool) string {
	secs := int(StatementTimeout / time.Second)
	if mariaDB {
		return fmt.Sprintf("SET SESSION time_zone = '+00:00', max_statement_time = %d, lock_wait_timeout = %d, innodb_lock_wait_timeout = %d", secs, secs, secs)
	}
	return fmt.Sprintf("SET SESSION time_zone = '+00:00', max_execution_time = %d, lock_wait_timeout = %d, innodb_lock_wait_timeout = %d", StatementTimeout.Milliseconds(), secs, secs)
}

func connect(ctx context.Context, req connector.Request) (*wire.Conn, error) {
	c := req.Credentials
	if c["host"] == "" || c["database"] == "" || c["user"] == "" {
		return nil, fmt.Errorf("connection needs host, database and user: %w", effects.ErrFatal)
	}
	if req.Dial == nil {
		return nil, fmt.Errorf("no egress dialer: %w", effects.ErrFatal)
	}
	port := c["port"]
	if port == "" {
		port = "3306"
	}
	tc, err := tlsConfig(c)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, ConnectTimeout)
	defer cancel()
	// The egress guard resolves, vets and pins the host itself.
	nc, err := req.Dial(cctx, "tcp", net.JoinHostPort(c["host"], port))
	if err != nil {
		return nil, classifyConnect(err)
	}
	conn, err := wire.Connect(cctx, nc, wire.Config{
		User: c["user"], Password: c["password"], Database: c["database"], TLS: tc,
		Attrs: map[string]string{"program_name": "taskiem"},
		// Report matched rows for UPDATE, as PostgreSQL does.
		FoundRows: true,
	})
	if err != nil {
		return nil, classifyConnect(err)
	}
	if _, err := conn.Exec(cctx, setupSQL(conn.MariaDB())); err != nil {
		_ = conn.Abort()
		return nil, classifyConnect(err)
	}
	return conn, nil
}

func params(in map[string]any) ([]any, error) {
	raw, _ := in["params"].([]any)
	out := make([]any, len(raw))
	for i, v := range raw {
		switch t := v.(type) {
		case nil, bool, int64, float64, string:
			out[i] = t
		case int:
			out[i] = int64(t)
		case json.Number:
			if n, err := t.Int64(); err == nil {
				out[i] = n
			} else {
				out[i] = wire.Decimal(t.String())
			}
		case map[string]any, []any:
			b, err := json.Marshal(t)
			if err != nil {
				return nil, fmt.Errorf("params[%d]: %w: %w", i, err, effects.ErrFatal)
			}
			out[i] = string(b)
		default:
			return nil, fmt.Errorf("params[%d]: unsupported type %T: %w", i, v, effects.ErrFatal)
		}
	}
	return out, nil
}

func query(ctx context.Context, req connector.Request) (connector.Response, error) {
	sql, _ := req.Input["sql"].(string)
	maxRows := 1000
	if m, ok := req.Input["max_rows"].(int64); ok && m > 0 {
		maxRows = int(m)
	}
	args, err := params(req.Input)
	if err != nil {
		return connector.Response{}, err
	}
	conn, err := connect(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(ctx, StatementTimeout+clientGrace)
	defer cancel()

	// Both: the session default covers anything the statement does after
	// an implicit commit; the transaction is what the statement runs in.
	for _, s := range []string{"SET SESSION TRANSACTION READ ONLY", "START TRANSACTION READ ONLY"} {
		if _, err := conn.Exec(ctx, s); err != nil {
			return connector.Response{}, classify(err, true)
		}
	}
	st, err := conn.Prepare(ctx, sql)
	if err != nil {
		return connector.Response{}, classify(err, true)
	}
	rows, err := st.Execute(ctx, args, nil)
	if err != nil {
		return connector.Response{}, classify(err, true)
	}
	cols := rows.Columns()
	out := []any{}
	truncated := false
	for {
		vals, err := rows.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return connector.Response{}, classify(err, true)
		}
		if len(out) == maxRows {
			// Do not stream the rest: dropping the connection ends the
			// statement and rolls the transaction back.
			truncated = true
			_ = conn.Abort()
			break
		}
		row := make(map[string]any, len(vals))
		for i, v := range vals {
			row[cols[i].Name] = jsonValue(cols[i], v)
		}
		out = append(out, row)
	}
	if !truncated {
		if err := rows.Close(); err != nil {
			return connector.Response{}, classify(err, true)
		}
		// Nothing to keep: the rows are read. Errors here do not matter
		// because the connection is closed next.
		_ = st.Close(ctx)
		_, _ = conn.Exec(ctx, "ROLLBACK")
	}
	return connector.Response{Output: map[string]any{"rows": out, "row_count": len(out), "truncated": truncated}}, nil
}

func execute(ctx context.Context, req connector.Request) (connector.Response, error) {
	sql, _ := req.Input["sql"].(string)
	args, err := params(req.Input)
	if err != nil {
		return connector.Response{}, err
	}
	conn, err := connect(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(ctx, StatementTimeout+clientGrace)
	defer cancel()

	st, err := conn.Prepare(ctx, sql)
	if err != nil {
		return connector.Response{}, classifyUnsent(err)
	}
	sent := false
	rows, err := st.Execute(ctx, args, func() { sent = true })
	if err != nil {
		if !sent {
			return connector.Response{}, classifyUnsent(err)
		}
		return connector.Response{}, classify(err, false)
	}
	var n int64
	counting := len(rows.Columns()) > 0
	for counting {
		_, err := rows.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return connector.Response{}, classify(err, false)
		}
		n++
	}
	if err := rows.Close(); err != nil {
		return connector.Response{}, classify(err, false)
	}
	res := rows.Result()
	if !counting {
		n = clampInt64(res.AffectedRows)
	}
	_ = st.Close(ctx)
	return connector.Response{Output: map[string]any{"rows_affected": n, "last_insert_id": clampInt64(res.LastInsertID)}}, nil
}

func clampInt64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

// retryableCode lists server errors that mean "try again later" without
// the statement having taken effect.
func retryableCode(code uint16) bool {
	switch code {
	case 1213, // ER_LOCK_DEADLOCK: the transaction was rolled back
		1205, // ER_LOCK_WAIT_TIMEOUT: the statement was rolled back
		1040, // ER_CON_COUNT_ERROR: too many connections
		1203, // ER_TOO_MANY_USER_CONNECTIONS
		1053: // ER_SERVER_SHUTDOWN
		return true
	}
	return false
}

func serverError(err error) error {
	var se *wire.ServerError
	if !errors.As(err, &se) {
		return nil
	}
	if retryableCode(se.Code) {
		return fmt.Errorf("%w: %w", err, effects.ErrRetryable)
	}
	return fmt.Errorf("%w: %w", err, effects.ErrFatal)
}

// classifyConnect maps a failure before any user statement was sent.
// Protocol and configuration errors at login (a non-MySQL port, TLS
// refused, a bad certificate) will not fix themselves.
func classifyConnect(err error) error {
	if se := serverError(err); se != nil {
		return se
	}
	if errors.Is(err, effects.ErrFatal) {
		return err
	}
	if errors.Is(err, wire.ErrConfig) || errors.Is(err, wire.ErrProtocol) {
		return fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if errors.Is(err, effects.ErrNotSent) {
		return err
	}
	return fmt.Errorf("%w: %w", err, effects.ErrNotSent)
}

// classifyUnsent maps a failure before COM_STMT_EXECUTE left the process:
// nothing can have been written.
func classifyUnsent(err error) error {
	if se := serverError(err); se != nil {
		return se
	}
	if errors.Is(err, effects.ErrFatal) {
		return err
	}
	if errors.Is(err, wire.ErrConfig) {
		return fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	return fmt.Errorf("%w: %w", err, effects.ErrNotSent)
}

// classify maps database errors for the engine, as the postgres connector
// does. Server-reported errors mean the statement was rejected (fatal, or
// retryable for deadlocks, lock wait timeouts and overload); a connection
// lost mid-statement is an unknown outcome unless the statement could not
// have written.
func classify(err error, readOnly bool) error {
	if se := serverError(err); se != nil {
		return se
	}
	if errors.Is(err, effects.ErrFatal) {
		return err
	}
	if errors.Is(err, wire.ErrConfig) {
		return fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	if readOnly {
		return fmt.Errorf("%w: %w", err, effects.ErrRetryable)
	}
	return fmt.Errorf("%w: %w", err, effects.ErrUnknownOutcome)
}

// jsonValue converts a decoded column value into a JSON-compatible value.
func jsonValue(col wire.Column, v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case int64:
		return t
	case uint64:
		if t <= math.MaxInt64 {
			return int64(t)
		}
		return strconv.FormatUint(t, 10)
	case float32:
		// Keep FLOAT's shortest decimal form: 0.1, not 0.10000000149.
		f, _ := strconv.ParseFloat(strconv.FormatFloat(float64(t), 'g', -1, 32), 64)
		return f
	case float64:
		return t
	case wire.Decimal:
		// Exact: a string, never a float.
		return string(t)
	case wire.DateTime:
		if col.Type == wire.TypeDate || col.Type == wire.TypeNewDate {
			return t.DateString()
		}
		if tm, ok := t.Time(); ok {
			return tm.Format(time.RFC3339Nano)
		}
		return t.String() // zero dates such as 0000-00-00 00:00:00
	case wire.Duration:
		return t.String()
	case []byte:
		switch col.Type {
		case wire.TypeJSON:
			return decodeJSON(t)
		case wire.TypeBit:
			if len(t) <= 8 {
				var b [8]byte
				copy(b[8-len(t):], t)
				return jsonValue(col, binary.BigEndian.Uint64(b[:]))
			}
		}
		if col.Charset == wire.CharsetBinary {
			return base64.StdEncoding.EncodeToString(t)
		}
		return string(t)
	}
	return fmt.Sprint(v)
}

func decodeJSON(b []byte) any {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return string(b)
	}
	return normalise(v)
}

func normalise(v any) any {
	switch t := v.(type) {
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n
		}
		f, _ := t.Float64()
		return f
	case map[string]any:
		for k, x := range t {
			t[k] = normalise(x)
		}
	case []any:
		for i, x := range t {
			t[i] = normalise(x)
		}
	}
	return v
}

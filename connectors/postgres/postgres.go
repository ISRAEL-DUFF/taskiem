// Package postgres is the first-party PostgreSQL connector (spec 6.5).
package postgres

import (
	"context"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

// StatementTimeout bounds every statement.
const StatementTimeout = 30 * time.Second

func New() *connector.Connector {
	return &connector.Connector{Manifest: connector.MustParse(manifest), Actions: map[string]connector.Action{
		"query":   connector.ActionFunc(query),
		"execute": connector.ActionFunc(execute),
	}}
}

func connect(ctx context.Context, req connector.Request) (*pgx.Conn, error) {
	c := req.Credentials
	if c["host"] == "" || c["database"] == "" || c["user"] == "" {
		return nil, fmt.Errorf("connection needs host, database and user: %w", effects.ErrFatal)
	}
	if req.Dial == nil {
		return nil, fmt.Errorf("no egress dialer: %w", effects.ErrFatal)
	}
	port := c["port"]
	if port == "" {
		port = "5432"
	}
	ssl := c["sslmode"]
	if ssl == "" {
		ssl = "require"
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword(c["user"], c["password"]), Host: net.JoinHostPort(c["host"], port), Path: "/" + c["database"]}
	u.RawQuery = url.Values{"sslmode": {ssl}, "connect_timeout": {"10"}}.Encode()
	cfg, err := pgx.ParseConfig(u.String())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	// Hand the host name to the egress guard unresolved, so it resolves,
	// vets, and pins the address itself.
	cfg.LookupFunc = func(_ context.Context, host string) ([]string, error) { return []string{host}, nil }
	cfg.DialFunc = req.Dial
	cfg.RuntimeParams["statement_timeout"] = fmt.Sprint(StatementTimeout.Milliseconds())
	cfg.RuntimeParams["application_name"] = "taskiem"
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, classify(err, true)
	}
	return conn, nil
}

func params(in map[string]any) []any {
	p, _ := in["params"].([]any)
	return p
}

func query(ctx context.Context, req connector.Request) (connector.Response, error) {
	sql, _ := req.Input["sql"].(string)
	max := 1000
	if m, ok := req.Input["max_rows"].(int64); ok && m > 0 {
		max = int(m)
	}
	conn, err := connect(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var out []any
	truncated := false
	err = pgx.BeginTxFunc(ctx, conn, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, sql, params(req.Input)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		fields := rows.FieldDescriptions()
		for rows.Next() {
			if len(out) == max {
				truncated = true
				break
			}
			vals, err := rows.Values()
			if err != nil {
				return err
			}
			row := make(map[string]any, len(vals))
			for i, v := range vals {
				row[fields[i].Name] = jsonValue(v)
			}
			out = append(out, row)
		}
		return rows.Err()
	})
	if err != nil {
		return connector.Response{}, classify(err, true)
	}
	if out == nil {
		out = []any{}
	}
	return connector.Response{Output: map[string]any{"rows": out, "row_count": len(out), "truncated": truncated}}, nil
}

func execute(ctx context.Context, req connector.Request) (connector.Response, error) {
	sql, _ := req.Input["sql"].(string)
	conn, err := connect(ctx, req)
	if err != nil {
		return connector.Response{}, err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	tag, err := conn.Exec(ctx, sql, params(req.Input)...)
	if err != nil {
		return connector.Response{}, classify(err, false)
	}
	return connector.Response{Output: map[string]any{"rows_affected": tag.RowsAffected()}}, nil
}

// classify maps database errors for the engine. Server-reported errors mean
// the statement was rejected (fatal, or retryable for serialisation failures and
// deadlock); a connection lost mid-statement is an unknown outcome unless
// the statement could not have written.
func classify(err error, readOnly bool) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "40001", "40P01", "57P01", "53300":
			return fmt.Errorf("%w: %w", err, effects.ErrRetryable)
		}
		return fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	var ce *pgconn.ConnectError
	if errors.As(err, &ce) || errors.Is(err, effects.ErrNotSent) {
		return fmt.Errorf("%w: %w", err, effects.ErrNotSent)
	}
	if errors.Is(err, effects.ErrFatal) {
		return err
	}
	if readOnly {
		return fmt.Errorf("%w: %w", err, effects.ErrRetryable)
	}
	return fmt.Errorf("%w: %w", err, effects.ErrUnknownOutcome)
}

// jsonValue converts database values into JSON-compatible Go values.
func jsonValue(v any) any {
	switch t := v.(type) {
	case nil, bool, string, int64, float64:
		return t
	case int16:
		return int64(t)
	case int32:
		return int64(t)
	case int:
		return int64(t)
	case float32:
		return float64(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	case []byte:
		return base64.StdEncoding.EncodeToString(t)
	case [16]byte:
		return fmt.Sprintf("%x-%x-%x-%x-%x", t[0:4], t[4:6], t[6:8], t[8:10], t[10:16])
	case pgtype.Numeric:
		if !t.Valid {
			return nil
		}
		if t.Exp >= 0 && t.Int != nil {
			n := new(big.Int).Mul(t.Int, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(t.Exp)), nil))
			if n.IsInt64() {
				return n.Int64()
			}
			return n.String()
		}
		f, _ := t.Float64Value()
		return f.Float64
	case map[string]any:
		for k, x := range t {
			t[k] = jsonValue(x)
		}
		return t
	case []any:
		for i, x := range t {
			t[i] = jsonValue(x)
		}
		return t
	}
	return fmt.Sprint(v)
}

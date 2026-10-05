package runtime

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/expr"
)

// EgressHosts returns the hosts http steps and sandbox fetch may reach in a
// tenant's environment (spec 14.2).
func (s *Store) EgressHosts(ctx context.Context, tenant uuid.UUID, env string) ([]string, error) {
	var hosts []string
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT host FROM egress_rules WHERE tenant_id = $1 AND environment = $2 ORDER BY host`, tenant, env)
		if err != nil {
			return err
		}
		hosts, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	return hosts, err
}

// AllowEgress adds a host (or "*.domain") to a tenant environment's allow-list.
func (s *Store) AllowEgress(ctx context.Context, tenant uuid.UUID, env, host, by string) error {
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO egress_rules (tenant_id, environment, host, created_by) VALUES ($1, $2, lower($3), $4) ON CONFLICT DO NOTHING`, tenant, env, host, by); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'egress.allow', $3, '{}')`, tenant, by, env+"/"+host)
		return err
	})
}

// SetVariable sets a tenant variable (the `env` root of expressions).
func (s *Store) SetVariable(ctx context.Context, tenant uuid.UUID, env, name string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO variables (tenant_id, environment, name, value) VALUES ($1, $2, $3, $4)
			ON CONFLICT (tenant_id, environment, name) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, tenant, env, name, raw)
		return err
	})
}

// variables loads an environment's variables; RunStarted snapshots them so
// replays see the values the run started with.
func variables(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, env string) (map[string]any, error) {
	rows, err := tx.Query(ctx, `SELECT name, value FROM variables WHERE tenant_id = $1 AND environment = $2`, tenant, env)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]any{}
	for rows.Next() {
		var name string
		var raw []byte
		if err := rows.Scan(&name, &raw); err != nil {
			return nil, err
		}
		v, err := expr.DecodeJSON(raw)
		if err != nil {
			return nil, err
		}
		out[name] = v
	}
	return out, rows.Err()
}

package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// Connection is a tenant's configured account with a connector.
type Connection struct {
	ID          uuid.UUID
	Environment string
	Connector   string
	Name        string
	AuthType    string
	Status      string
}

// CreateConnection stores credentials encrypted and records the connection.
func (v *Vault) CreateConnection(ctx context.Context, tenant uuid.UUID, env, connector, name, authType string, creds map[string]string, by string) (uuid.UUID, error) {
	var id uuid.UUID
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		id, err = v.CreateConnectionTx(ctx, tx, tenant, env, connector, name, authType, creds, by, "")
		return err
	})
	return id, err
}

// CreateConnectionTx is CreateConnection inside a transaction whose scope
// is tenant (the partner connector bridge enters a sub-tenant first, so
// the access is audited in both chains). provisionedBy, when set, records
// who outside the tenant provisioned it. The credentials are encrypted
// under tenant's own key and can never be read back through the API.
func (v *Vault) CreateConnectionTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, env, connector, name, authType string, creds map[string]string, by, provisionedBy string) (uuid.UUID, error) {
	id := uuid.Must(uuid.NewV7())
	var secretRef *uuid.UUID
	if len(creds) > 0 {
		sid := uuid.Must(uuid.NewV7())
		raw, _ := json.Marshal(creds)
		ct, wdek, ver, err := v.encrypt(ctx, tx, tenant, sid, raw)
		if err != nil {
			return id, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO secrets (id, tenant_id, environment, ciphertext, wrapped_key, kek_version, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			sid, tenant, env, ct, wdek, ver, by); err != nil {
			return id, err
		}
		secretRef = &sid
	}
	if _, err := tx.Exec(ctx, `INSERT INTO connections (id, tenant_id, environment, connector, name, auth_type, secret_ref, provisioned_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''))`, id, tenant, env, connector, name, authType, secretRef, provisionedBy); err != nil {
		return id, err
	}
	return id, audit(ctx, tx, tenant, by, "connection.create", connector+"/"+name, map[string]any{"connection_id": id, "environment": env})
}

// ReplaceCredentialsTx replaces a connection's credentials (a rotation) in
// a transaction scoped to tenant, and reactivates it. It returns
// ErrNotFound when the connection is not tenant's.
func (v *Vault) ReplaceCredentialsTx(ctx context.Context, tx pgx.Tx, tenant, conn uuid.UUID, creds map[string]string, by string) error {
	var connector, name, env string
	var ref *uuid.UUID
	err := tx.QueryRow(ctx, `SELECT connector, name, environment, secret_ref FROM connections WHERE id = $1 AND tenant_id = $2 FOR UPDATE`, conn, tenant).
		Scan(&connector, &name, &env, &ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(creds)
	sid := uuid.Must(uuid.NewV7())
	if ref != nil {
		sid = *ref
	}
	ct, wdek, ver, err := v.encrypt(ctx, tx, tenant, sid, raw)
	if err != nil {
		return err
	}
	if ref != nil {
		_, err = tx.Exec(ctx, `UPDATE secrets SET ciphertext = $2, wrapped_key = $3, kek_version = $4, updated_at = now() WHERE id = $1`, sid, ct, wdek, ver)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO secrets (id, tenant_id, environment, ciphertext, wrapped_key, kek_version, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			sid, tenant, env, ct, wdek, ver, by)
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE connections SET secret_ref = $2, status = 'active' WHERE id = $1`, conn, sid); err != nil {
		return err
	}
	return audit(ctx, tx, tenant, by, "connection.credentials_replace", connector+"/"+name, map[string]any{"connection_id": conn, "environment": env})
}

// Credentials returns a connection's decrypted credential fields. An empty
// name selects the only active connection for the connector. The decryption
// is recorded in secret_reads with the Use attached to ctx; a connection
// without credentials decrypts nothing and records nothing.
func (v *Vault) Credentials(ctx context.Context, tenant uuid.UUID, env, connector, name string) (map[string]string, error) {
	out := map[string]string{}
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		q := `SELECT c.id, c.name, c.secret_ref FROM connections c WHERE c.tenant_id = $1 AND c.environment = $2 AND c.connector = $3 AND c.status = 'active'`
		args := []any{tenant, env, connector}
		if name != "" {
			q += ` AND c.name = $4`
			args = append(args, name)
		}
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		type match struct {
			id   uuid.UUID
			name string
			ref  *uuid.UUID
		}
		var ms []match
		for rows.Next() {
			var m match
			if err := rows.Scan(&m.id, &m.name, &m.ref); err != nil {
				rows.Close()
				return err
			}
			ms = append(ms, m)
		}
		rows.Close()
		switch {
		case len(ms) == 0:
			return fmt.Errorf("no active %s connection in %s: %w", connector, env, ErrNotFound)
		case len(ms) > 1:
			return fmt.Errorf("%d active %s connections in %s; name one with the step's connection field: %w", len(ms), connector, env, ErrAmbiguous)
		}
		if ms[0].ref == nil {
			return nil
		}
		var ct, wdek []byte
		var ver int
		if err := tx.QueryRow(ctx, `SELECT ciphertext, wrapped_key, kek_version FROM secrets WHERE id = $1`, *ms[0].ref).Scan(&ct, &wdek, &ver); err != nil {
			return err
		}
		raw, err := v.decrypt(ctx, tx, tenant, *ms[0].ref, ct, wdek, ver)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return err
		}
		return record(ctx, tx, tenant, useFrom(ctx, KindConnection), env, ms[0].name, &ms[0].id, connector)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return out, err
}

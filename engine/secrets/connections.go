package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

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
	return v.createConnectionTx(ctx, tx, tenant, env, connector, name, authType, creds, by, provisionedBy, nil, nil)
}

// CreateOAuthConnectionTx is CreateConnectionTx for a connection linked by
// OAuth (decision 0034): auth_type oauth2, expiring when the token does
// (nil: the provider named no expiry), and audited with via: oauth.
func (v *Vault) CreateOAuthConnectionTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, env, connector, name string, creds map[string]string, expiresAt *time.Time, by string) (uuid.UUID, error) {
	return v.createConnectionTx(ctx, tx, tenant, env, connector, name, "oauth2", creds, by, "", expiresAt, map[string]any{"via": "oauth"})
}

func (v *Vault) createConnectionTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, env, connector, name, authType string, creds map[string]string, by, provisionedBy string, expiresAt *time.Time, detail map[string]any) (uuid.UUID, error) {
	id := uuid.Must(uuid.NewV7())
	var secretRef *uuid.UUID
	creds = stored(creds)
	if len(creds) > 0 {
		sid := uuid.Must(uuid.NewV7())
		raw, _ := json.Marshal(creds)
		ct, wdek, ver, err := v.encrypt(ctx, tx, tenant, sid, env, connectionBinding(id), raw)
		if err != nil {
			return id, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO secrets (id, tenant_id, environment, ciphertext, wrapped_key, kek_version, aad_version, created_by) VALUES ($1, $2, $3, $4, $5, $6, 2, $7)`,
			sid, tenant, env, ct, wdek, ver, by); err != nil {
			return id, err
		}
		secretRef = &sid
	}
	if _, err := tx.Exec(ctx, `INSERT INTO connections (id, tenant_id, environment, connector, name, auth_type, secret_ref, provisioned_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9)`, id, tenant, env, connector, name, authType, secretRef, provisionedBy, expiresAt); err != nil {
		return id, err
	}
	d := map[string]any{"connection_id": id, "environment": env}
	for k, x := range detail {
		d[k] = x
	}
	return id, audit(ctx, tx, tenant, by, "connection.create", connector+"/"+name, d)
}

// ConnectionCredentialsTx returns a connection's decrypted credential
// fields by id, in a transaction scoped to tenant, for the platform's own
// use of them (adding fields, revoking the token at the provider): never
// for an answer. The read is recorded with the Use attached to ctx. A
// connection without credentials gives an empty map; one that is not
// tenant's gives ErrNotFound.
func (v *Vault) ConnectionCredentialsTx(ctx context.Context, tx pgx.Tx, tenant, conn uuid.UUID) (map[string]string, error) {
	out := map[string]string{}
	var connector, name, env string
	var ref *uuid.UUID
	var mode, platform *string
	err := tx.QueryRow(ctx, `SELECT connector, name, environment, secret_ref, mode, platform FROM connections WHERE id = $1 AND tenant_id = $2`, conn, tenant).
		Scan(&connector, &name, &env, &ref, &mode, &platform)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if ref != nil {
		var ct, wdek []byte
		var ver, aad int
		if err := tx.QueryRow(ctx, `SELECT ciphertext, wrapped_key, kek_version, aad_version FROM secrets WHERE id = $1`, *ref).Scan(&ct, &wdek, &ver, &aad); err != nil {
			return nil, err
		}
		raw, err := v.decrypt(ctx, tx, tenant, *ref, env, connectionBinding(conn), aad, ct, wdek, ver)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		if err := record(ctx, tx, tenant, useFrom(ctx, KindConnection), env, name, &conn, connector); err != nil {
			return nil, err
		}
	}
	return out, v.handOut(out, connector, mode, platform)
}

// DeleteConnectionTx removes a connection and its credentials, in a
// transaction scoped to tenant, audited as connection.delete. Its routing
// keys go with it (ON DELETE CASCADE); its recorded secret reads stay, as
// history. ErrNotFound when the connection is not tenant's.
func (v *Vault) DeleteConnectionTx(ctx context.Context, tx pgx.Tx, tenant, conn uuid.UUID, by string, detail map[string]any) error {
	var connector, name, env string
	var ref *uuid.UUID
	err := tx.QueryRow(ctx, `DELETE FROM connections WHERE id = $1 AND tenant_id = $2 RETURNING connector, name, environment, secret_ref`, conn, tenant).Scan(&connector, &name, &env, &ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if ref != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM secrets WHERE id = $1`, *ref); err != nil {
			return err
		}
	}
	d := map[string]any{"connection_id": conn, "environment": env}
	for k, x := range detail {
		d[k] = x
	}
	return audit(ctx, tx, tenant, by, "connection.delete", connector+"/"+name, d)
}

// ExpireConnection marks the active OAuth connection(s) a step used as
// expired, when the provider answered that their token is revoked or
// expired (decision 0034): runs stop picking them, the Connections page
// shows it, and the person connects again. An empty name means the only
// active connection for the connector in env, as Credentials selects it.
// Pasted credentials (auth_type api_key) are left alone: the person
// replaces them. Audited as connection.expire, by the system.
func (v *Vault) ExpireConnection(ctx context.Context, tenant uuid.UUID, env, connector, name string) error {
	return db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE connections SET status = 'expired' WHERE tenant_id = $1 AND environment = $2 AND connector = $3
			AND auth_type = 'oauth2' AND status = 'active' AND ($4 = '' OR name = $4) RETURNING id, name`, tenant, env, connector, name)
		if err != nil {
			return err
		}
		type hit struct {
			id   uuid.UUID
			name string
		}
		hits, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (hit, error) {
			var h hit
			return h, r.Scan(&h.id, &h.name)
		})
		if err != nil {
			return err
		}
		for _, h := range hits {
			if err := audit(ctx, tx, tenant, "system", "connection.expire", connector+"/"+h.name, map[string]any{"connection_id": h.id, "environment": env, "reason": "the provider refused the token as revoked or expired"}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReplaceCredentialsTx replaces a connection's credentials (a rotation) in
// a transaction scoped to tenant, and reactivates it. It returns
// ErrNotFound when the connection is not tenant's.
func (v *Vault) ReplaceCredentialsTx(ctx context.Context, tx pgx.Tx, tenant, conn uuid.UUID, creds map[string]string, by string) error {
	var connector, name, env string
	var ref *uuid.UUID
	var platform *string
	err := tx.QueryRow(ctx, `SELECT connector, name, environment, secret_ref, platform FROM connections WHERE id = $1 AND tenant_id = $2 FOR UPDATE`, conn, tenant).
		Scan(&connector, &name, &env, &ref, &platform)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if platform != nil {
		return fmt.Errorf("%s/%s is Taskiem's own account for this business: its details are Taskiem's to change: %w", connector, name, ErrPlatformConnection)
	}
	raw, _ := json.Marshal(stored(creds))
	sid := uuid.Must(uuid.NewV7())
	if ref != nil {
		sid = *ref
	}
	ct, wdek, ver, err := v.encrypt(ctx, tx, tenant, sid, env, connectionBinding(conn), raw)
	if err != nil {
		return err
	}
	if ref != nil {
		_, err = tx.Exec(ctx, `UPDATE secrets SET ciphertext = $2, wrapped_key = $3, kek_version = $4, aad_version = 2, updated_at = now() WHERE id = $1`, sid, ct, wdek, ver)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO secrets (id, tenant_id, environment, ciphertext, wrapped_key, kek_version, aad_version, created_by) VALUES ($1, $2, $3, $4, $5, $6, 2, $7)`,
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

// SetRouteKeysTx replaces the routing keys indexed for a connection (the
// wallets an iswallet connection lists, by field), in a transaction whose
// scope is tenant (decision 0031). Keys are not secrets: they are stored in
// the clear so a partner's events can be routed to the sub-tenant holding
// them. Called whenever a connection's credentials are written.
func SetRouteKeysTx(ctx context.Context, tx pgx.Tx, tenant, conn uuid.UUID, keys map[string][]string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM connection_route_keys WHERE tenant_id = $1 AND connection_id = $2`, tenant, conn); err != nil {
		return err
	}
	for field, vs := range keys {
		for _, k := range vs {
			if _, err := tx.Exec(ctx, `INSERT INTO connection_route_keys (tenant_id, connection_id, environment, connector, connection, field, route_key)
				SELECT tenant_id, id, environment, connector, name, $3, $4 FROM connections WHERE tenant_id = $1 AND id = $2
				ON CONFLICT DO NOTHING`, tenant, conn, field, k); err != nil {
				return err
			}
		}
	}
	return nil
}

// Credentials returns a connection's decrypted credential fields. An empty
// name selects the only active connection for the connector. The decryption
// is recorded in secret_reads with the Use attached to ctx; a connection
// without credentials decrypts nothing and records nothing.
func (v *Vault) Credentials(ctx context.Context, tenant uuid.UUID, env, connector, name string) (map[string]string, error) {
	out := map[string]string{}
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		q := `SELECT c.id, c.name, c.secret_ref, c.mode, c.platform FROM connections c WHERE c.tenant_id = $1 AND c.environment = $2 AND c.connector = $3 AND c.status = 'active'`
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
			id             uuid.UUID
			name           string
			ref            *uuid.UUID
			mode, platform *string
		}
		var ms []match
		for rows.Next() {
			var m match
			if err := rows.Scan(&m.id, &m.name, &m.ref, &m.mode, &m.platform); err != nil {
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
		m := ms[0]
		if m.ref != nil {
			var ct, wdek []byte
			var ver, aad int
			if err := tx.QueryRow(ctx, `SELECT ciphertext, wrapped_key, kek_version, aad_version FROM secrets WHERE id = $1`, *m.ref).Scan(&ct, &wdek, &ver, &aad); err != nil {
				return err
			}
			raw, err := v.decrypt(ctx, tx, tenant, *m.ref, env, connectionBinding(m.id), aad, ct, wdek, ver)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				return err
			}
			if err := record(ctx, tx, tenant, useFrom(ctx, KindConnection), env, m.name, &m.id, connector); err != nil {
				return err
			}
		}
		return v.handOut(out, connector, m.mode, m.platform)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return out, err
}

// modeKey is connector.ModeKey: a connection's mode (live, test) handed
// to its connector with the credentials, never stored with them.
const modeKey = "$mode"

// withMode adds a connection's mode to the credentials handed out.
func withMode(creds map[string]string, mode *string) {
	if mode != nil && *mode != "" && creds != nil {
		creds[modeKey] = *mode
	}
}

// PlatformClients are the credentials of Taskiem's own provider clients
// (decision 0052), by connector id and then mode (live, test): what a
// connection Taskiem made under its own client (connections.platform) is
// handed in place of a key of its own. Process configuration
// (TASKIEM_ISWALLET_CLIENT_KEY, …), never stored.
type PlatformClients map[string]map[string]map[string]string

// For is the platform's credentials for a connector in a mode.
func (p PlatformClients) For(connector, mode string) (map[string]string, bool) {
	c, ok := p[connector][mode]
	return c, ok && len(c) > 0
}

// PlatformName is the one platform client a connection may be made under,
// and the name of the connection Taskiem makes under it.
const PlatformName = "taskiem"

// PlatformProvisioner is what made a connection under Taskiem's own
// client (connections.provisioned_by): kept by Taskiem, so nobody deletes
// it while its wallet may hold money.
const PlatformProvisioner = "taskiem:receive_money"

// handOut adds what a connection is handed but never stores: its mode,
// and for a connection under Taskiem's own client, that client's
// credentials for the mode (which win over anything stored).
func (v *Vault) handOut(creds map[string]string, connector string, mode, platform *string) error {
	withMode(creds, mode)
	if platform == nil {
		return nil
	}
	m := ""
	if mode != nil {
		m = *mode
	}
	keys, ok := v.Platform.For(connector, m)
	if !ok {
		return fmt.Errorf("Taskiem's own %s client is not set up on this Taskiem for %s: %w", connector, m, ErrPlatformClient)
	}
	maps.Copy(creds, keys)
	return nil
}

// CreatePlatformConnectionTx records a connection under Taskiem's own
// client (decision 0052) in a transaction scoped to tenant: its mode, the
// non-secret fields it keeps (the wallet it is for), and the provider
// account its events are routed by (connection_app_accounts, client
// taskiem). Its connector is handed the platform's key for the mode.
func (v *Vault) CreatePlatformConnectionTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, env, connector, name, mode, account string, fields map[string]string, by string) (uuid.UUID, error) {
	if _, ok := v.Platform.For(connector, mode); !ok {
		return uuid.Nil, fmt.Errorf("Taskiem's own %s client is not set up on this Taskiem for %s: %w", connector, mode, ErrPlatformClient)
	}
	id, err := v.createConnectionTx(ctx, tx, tenant, env, connector, name, "api_key", fields, by, PlatformProvisioner, nil, map[string]any{"via": "platform", "mode": mode})
	if err != nil {
		return id, err
	}
	if _, err := tx.Exec(ctx, `UPDATE connections SET platform = $2, mode = $3 WHERE id = $1`, id, PlatformName, mode); err != nil {
		return id, err
	}
	return id, SetAppAccountTx(ctx, tx, tenant, id, PlatformName, account)
}

// stored is the credentials as they are encrypted: without the keys only
// handed out ($mode), so a read and a write back never store them.
func stored(creds map[string]string) map[string]string {
	if _, ok := creds[modeKey]; !ok {
		return creds
	}
	out := make(map[string]string, len(creds))
	for k, v := range creds {
		if k != modeKey {
			out[k] = v
		}
	}
	return out
}

// RotateCredentials refreshes a connection's credentials once across every
// worker and process (connector.Request.Rotate; decision 0051): the
// connection's row is locked, its credentials read as they are stored now,
// fn given them, and what fn returns stored before the lock goes, in one
// transaction. A worker that waited sees the credentials the first one
// stored, so a provider that rotates refresh tokens (QuickBooks) never
// receives a spent one. Returning the same map stores nothing. The write
// is audited as connection.refresh, with no values. An empty name selects
// the only active connection for the connector.
func (v *Vault) RotateCredentials(ctx context.Context, tenant uuid.UUID, env, connector, name string, fn func(map[string]string) (map[string]string, error)) (map[string]string, error) {
	var out map[string]string
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		q := `SELECT id, name, secret_ref, mode, platform FROM connections WHERE tenant_id = $1 AND environment = $2 AND connector = $3 AND status = 'active'`
		args := []any{tenant, env, connector}
		if name != "" {
			q += ` AND name = $4`
			args = append(args, name)
		}
		rows, err := tx.Query(ctx, q+` FOR UPDATE`, args...)
		if err != nil {
			return err
		}
		type match struct {
			id             uuid.UUID
			name           string
			ref            *uuid.UUID
			mode, platform *string
		}
		ms, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (match, error) {
			var m match
			return m, row.Scan(&m.id, &m.name, &m.ref, &m.mode, &m.platform)
		})
		if err != nil {
			return err
		}
		switch {
		case len(ms) == 0:
			return fmt.Errorf("no active %s connection in %s: %w", connector, env, ErrNotFound)
		case len(ms) > 1:
			return fmt.Errorf("%d active %s connections in %s; name one with the step's connection field: %w", len(ms), connector, env, ErrAmbiguous)
		}
		m := ms[0]
		current := map[string]string{}
		if m.ref != nil {
			var ct, wdek []byte
			var ver, aad int
			if err := tx.QueryRow(ctx, `SELECT ciphertext, wrapped_key, kek_version, aad_version FROM secrets WHERE id = $1`, *m.ref).Scan(&ct, &wdek, &ver, &aad); err != nil {
				return err
			}
			raw, err := v.decrypt(ctx, tx, tenant, *m.ref, env, connectionBinding(m.id), aad, ct, wdek, ver)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &current); err != nil {
				return err
			}
			if err := record(ctx, tx, tenant, useFrom(ctx, KindConnection), env, m.name, &m.id, connector); err != nil {
				return err
			}
		}
		if m.platform != nil {
			// Taskiem's own client: nothing of it is the connection's to
			// refresh or store.
			out = current
			return v.handOut(out, connector, m.mode, m.platform)
		}
		withMode(current, m.mode)
		next, err := fn(maps.Clone(current))
		if err != nil {
			return err
		}
		if maps.Equal(stored(next), stored(current)) {
			out = current
			return nil
		}
		raw, _ := json.Marshal(stored(next))
		sid := uuid.Must(uuid.NewV7())
		if m.ref != nil {
			sid = *m.ref
		}
		ct, wdek, ver, err := v.encrypt(ctx, tx, tenant, sid, env, connectionBinding(m.id), raw)
		if err != nil {
			return err
		}
		if m.ref != nil {
			_, err = tx.Exec(ctx, `UPDATE secrets SET ciphertext = $2, wrapped_key = $3, kek_version = $4, aad_version = 2, updated_at = now() WHERE id = $1`, sid, ct, wdek, ver)
		} else {
			_, err = tx.Exec(ctx, `INSERT INTO secrets (id, tenant_id, environment, ciphertext, wrapped_key, kek_version, aad_version, created_by) VALUES ($1, $2, $3, $4, $5, $6, 2, 'system')`,
				sid, tenant, env, ct, wdek, ver)
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE connections SET secret_ref = $2 WHERE id = $1`, m.id, sid)
			}
		}
		if err != nil {
			return err
		}
		out = maps.Clone(next)
		withMode(out, m.mode)
		return audit(ctx, tx, tenant, "system", "connection.refresh", connector+"/"+m.name, map[string]any{"connection_id": m.id, "environment": env})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return out, err
}

// SetAppAccountTx records the provider account a connection made through
// Taskiem's platform app is for (Slack's team id; decision 0051), in a
// transaction scoped to tenant, so the app's events for that account reach
// it. An empty account removes the record.
func SetAppAccountTx(ctx context.Context, tx pgx.Tx, tenant, conn uuid.UUID, client, account string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM connection_app_accounts WHERE tenant_id = $1 AND connection_id = $2`, tenant, conn); err != nil || account == "" {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO connection_app_accounts (connection_id, tenant_id, environment, connector, connection, client, account_key)
		SELECT id, tenant_id, environment, connector, name, $3, $4 FROM connections WHERE tenant_id = $1 AND id = $2`, tenant, conn, client, account)
	return err
}

package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/byok"
	"github.com/israel-duff/taskiem/engine/db"
)

// ErrNotFound means no such secret or connection is visible.
var ErrNotFound = errors.New("not found")

// ErrAmbiguous means several connections match and none was named.
var ErrAmbiguous = errors.New("ambiguous connection")

// Vault stores secrets and connection credentials.
type Vault struct {
	Pool    *pgxpool.Pool
	KMS     KMS
	RootKey string // root key name in the KMS

	// BYOK builds providers for customer keys (docs/byok.md); nil uses a
	// default factory (public addresses only).
	BYOK *byok.Factory
	// BYOKCacheTTL is how long a tenant key unwrapped with a customer key
	// is kept in memory (default DefaultBYOKCacheTTL): revoking the key
	// stops every process within this bound. Keys wrapped only by the
	// platform's KMS are kept until the process ends, as before.
	BYOKCacheTTL time.Duration
	// DestroyAfter is how long a retired tenant key version is kept once
	// nothing uses it (default DefaultDestroyAfter).
	DestroyAfter time.Duration
	// Now is the clock (tests).
	Now func() time.Time

	keks      sync.Map // "tenant/version" -> cachedKey
	pseudo    sync.Map // tenant -> cachedKey
	providers sync.Map // BYOK key id -> cachedProvider
	failing   sync.Map // tenant -> time.Time: unwrapping failed; fail fast until then
}

func audit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, actor, action, target string, detail map[string]any) error {
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, $2, $3, $4, $5, $6)`, tenant, actorType(actor), actor, action, target, raw)
	return err
}

func actorType(actor string) string {
	switch {
	case actor == "system":
		return "system"
	case strings.HasPrefix(actor, "cli:"):
		return "platform_admin" // the operator CLI (taskiem tenants keys)
	case strings.HasPrefix(actor, "key:"), strings.HasPrefix(actor, "partner:"):
		return "api_key"
	}
	return "user"
}

// Put creates or replaces a named secret in an environment.
func (v *Vault) Put(ctx context.Context, tenant uuid.UUID, env, name string, value []byte, by string) (uuid.UUID, error) {
	var id uuid.UUID
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id FROM secrets WHERE tenant_id = $1 AND environment = $2 AND name = $3`, tenant, env, name).Scan(&id)
		isNew := errors.Is(err, pgx.ErrNoRows)
		if err != nil && !isNew {
			return err
		}
		if isNew {
			id = uuid.Must(uuid.NewV7())
		}
		ct, wdek, ver, err := v.encrypt(ctx, tx, tenant, id, env, name, value)
		if err != nil {
			return err
		}
		if isNew {
			_, err = tx.Exec(ctx, `INSERT INTO secrets (id, tenant_id, environment, name, ciphertext, wrapped_key, kek_version, aad_version, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, 2, $8)`,
				id, tenant, env, name, ct, wdek, ver, by)
		} else {
			_, err = tx.Exec(ctx, `UPDATE secrets SET ciphertext = $2, wrapped_key = $3, kek_version = $4, aad_version = 2, updated_at = now() WHERE id = $1`, id, ct, wdek, ver)
		}
		if err != nil {
			return err
		}
		return audit(ctx, tx, tenant, by, "secret.write", env+"/"+name, map[string]any{"secret_id": id})
	})
	return id, err
}

// Get returns a named secret's value; it implements runtime.Secrets. The
// decryption is recorded in secret_reads with the Use attached to ctx.
func (v *Vault) Get(ctx context.Context, tenant uuid.UUID, env, name string) (string, error) {
	var out []byte
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var id uuid.UUID
		var ct, wdek []byte
		var ver, aad int
		err := tx.QueryRow(ctx, `SELECT id, ciphertext, wrapped_key, kek_version, aad_version FROM secrets WHERE tenant_id = $1 AND environment = $2 AND name = $3`,
			tenant, env, name).Scan(&id, &ct, &wdek, &ver, &aad)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("secret %q in %s: %w", name, env, ErrNotFound)
		}
		if err != nil {
			return err
		}
		if out, err = v.decrypt(ctx, tx, tenant, id, env, name, aad, ct, wdek, ver); err != nil {
			return err
		}
		return record(ctx, tx, tenant, useFrom(ctx, KindSecret), env, name, nil, "")
	})
	return string(out), err
}

// Delete removes a named secret.
func (v *Vault) Delete(ctx context.Context, tenant uuid.UUID, env, name, by string) error {
	return db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM secrets WHERE tenant_id = $1 AND environment = $2 AND name = $3`, tenant, env, name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit(ctx, tx, tenant, by, "secret.delete", env+"/"+name, map[string]any{})
	})
}

// Rotate creates a new tenant key version, wrapped by the tenant's
// customer key if one is in use, and queues the re-wrapping of every data
// key, subject key and the pseudonym key under it (the key job does it in
// batches; Rewrap does it at once). Values are not re-encrypted (spec
// 14.1). Older versions are retired once nothing uses them. It returns the
// new version.
func (v *Vault) Rotate(ctx context.Context, tenant uuid.UUID, by string) (int, error) {
	var newVersion int
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		newVersion, err = v.rotateTx(ctx, tx, tenant, nil, "rotation")
		if err != nil {
			return err
		}
		return audit(ctx, tx, tenant, by, "secret.rotate_key", "tenant_key", map[string]any{"version": newVersion})
	})
	return newVersion, err
}

// rotateTx adds a tenant key version (wrapped by p, or the tenant's key in
// use when p is nil) and marks the tenant for re-wrapping.
func (v *Vault) rotateTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, p *inUseKey, reason string) (int, error) {
	var cur int
	// Lock the tenant's key rows so two rotations cannot pick one version.
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version), 0) FROM (SELECT version FROM tenant_keys WHERE tenant_id = $1 FOR UPDATE) k`, tenant).Scan(&cur); err != nil {
		return 0, err
	}
	ver, _, err := v.newKEK(ctx, tx, tenant, cur+1, p)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO key_rewrap_due (tenant_id, reason) VALUES ($1, $2)
		ON CONFLICT (tenant_id) DO UPDATE SET reason = EXCLUDED.reason, not_before = now(), last_error = NULL`, tenant, reason); err != nil {
		return 0, err
	}
	return ver, nil
}

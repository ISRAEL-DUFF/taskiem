package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
)

// ErrNotFound means no such secret or connection is visible.
var ErrNotFound = errors.New("not found")

// Vault stores secrets and connection credentials.
type Vault struct {
	Pool    *pgxpool.Pool
	KMS     KMS
	RootKey string // root key name in the KMS

	keks sync.Map // "tenant/version" -> []byte; KEKs are immutable per version
}

func (v *Vault) kekCacheKey(t uuid.UUID, version int) string { return fmt.Sprintf("%s/%d", t, version) }

// currentKEK returns the tenant's newest KEK, creating version 1 if needed.
func (v *Vault) currentKEK(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (int, []byte, error) {
	var version int
	var wrapped, kmsKey string
	err := tx.QueryRow(ctx, `SELECT version, wrapped_kek, kms_key FROM tenant_keys WHERE tenant_id = $1 AND retired_at IS NULL ORDER BY version DESC LIMIT 1`, tenant).
		Scan(&version, &wrapped, &kmsKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return v.newKEK(ctx, tx, tenant, 1)
	}
	if err != nil {
		return 0, nil, err
	}
	kek, err := v.unwrapKEK(ctx, tenant, version, kmsKey, wrapped)
	return version, kek, err
}

func (v *Vault) newKEK(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, version int) (int, []byte, error) {
	kek, err := newKey()
	if err != nil {
		return 0, nil, err
	}
	wrapped, err := v.KMS.Encrypt(ctx, v.RootKey, kek)
	if err != nil {
		return 0, nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tenant_keys (tenant_id, version, wrapped_kek, kms_key) VALUES ($1, $2, $3, $4)`, tenant, version, wrapped, v.RootKey); err != nil {
		return 0, nil, err
	}
	v.keks.Store(v.kekCacheKey(tenant, version), kek)
	return version, kek, nil
}

func (v *Vault) kek(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, version int) ([]byte, error) {
	if k, ok := v.keks.Load(v.kekCacheKey(tenant, version)); ok {
		return k.([]byte), nil
	}
	var wrapped, kmsKey string
	if err := tx.QueryRow(ctx, `SELECT wrapped_kek, kms_key FROM tenant_keys WHERE tenant_id = $1 AND version = $2`, tenant, version).Scan(&wrapped, &kmsKey); err != nil {
		return nil, err
	}
	return v.unwrapKEK(ctx, tenant, version, kmsKey, wrapped)
}

func (v *Vault) unwrapKEK(ctx context.Context, tenant uuid.UUID, version int, kmsKey, wrapped string) ([]byte, error) {
	if k, ok := v.keks.Load(v.kekCacheKey(tenant, version)); ok {
		return k.([]byte), nil
	}
	kek, err := v.KMS.Decrypt(ctx, kmsKey, wrapped)
	if err != nil {
		return nil, fmt.Errorf("unwrap tenant key: %w", err)
	}
	v.keks.Store(v.kekCacheKey(tenant, version), kek)
	return kek, nil
}

// encrypt seals value under a fresh data key bound to the secret's id.
func (v *Vault) encrypt(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, value []byte) (ct, wrappedDEK []byte, version int, err error) {
	version, kek, err := v.currentKEK(ctx, tx, tenant)
	if err != nil {
		return nil, nil, 0, err
	}
	dek, err := newKey()
	if err != nil {
		return nil, nil, 0, err
	}
	if ct, err = seal(dek, value, id[:]); err != nil {
		return nil, nil, 0, err
	}
	if wrappedDEK, err = seal(kek, dek, tenant[:]); err != nil {
		return nil, nil, 0, err
	}
	return ct, wrappedDEK, version, nil
}

func (v *Vault) decrypt(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, ct, wrappedDEK []byte, version int) ([]byte, error) {
	kek, err := v.kek(ctx, tx, tenant, version)
	if err != nil {
		return nil, err
	}
	dek, err := open(kek, wrappedDEK, tenant[:])
	if err != nil {
		return nil, fmt.Errorf("unwrap data key: %w", err)
	}
	return open(dek, ct, id[:])
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
	case strings.HasPrefix(actor, "key:"):
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
		ct, wdek, ver, err := v.encrypt(ctx, tx, tenant, id, value)
		if err != nil {
			return err
		}
		if isNew {
			_, err = tx.Exec(ctx, `INSERT INTO secrets (id, tenant_id, environment, name, ciphertext, wrapped_key, kek_version, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				id, tenant, env, name, ct, wdek, ver, by)
		} else {
			_, err = tx.Exec(ctx, `UPDATE secrets SET ciphertext = $2, wrapped_key = $3, kek_version = $4, updated_at = now() WHERE id = $1`, id, ct, wdek, ver)
		}
		if err != nil {
			return err
		}
		return audit(ctx, tx, tenant, by, "secret.write", env+"/"+name, map[string]any{"secret_id": id})
	})
	return id, err
}

// Get returns a named secret's value; it implements runtime.Secrets.
func (v *Vault) Get(ctx context.Context, tenant uuid.UUID, env, name string) (string, error) {
	var out []byte
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var id uuid.UUID
		var ct, wdek []byte
		var ver int
		err := tx.QueryRow(ctx, `SELECT id, ciphertext, wrapped_key, kek_version FROM secrets WHERE tenant_id = $1 AND environment = $2 AND name = $3`,
			tenant, env, name).Scan(&id, &ct, &wdek, &ver)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("secret %q in %s: %w", name, env, ErrNotFound)
		}
		if err != nil {
			return err
		}
		out, err = v.decrypt(ctx, tx, tenant, id, ct, wdek, ver)
		return err
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

// Rotate creates a new KEK version and re-wraps every data key under it.
// Values are not re-encrypted (spec 14.1). It returns the new version.
func (v *Vault) Rotate(ctx context.Context, tenant uuid.UUID, by string) (int, error) {
	var newVersion int
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var cur int
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version), 0) FROM tenant_keys WHERE tenant_id = $1`, tenant).Scan(&cur); err != nil {
			return err
		}
		ver, kek, err := v.newKEK(ctx, tx, tenant, cur+1)
		if err != nil {
			return err
		}
		newVersion = ver
		rows, err := tx.Query(ctx, `SELECT id, wrapped_key, kek_version FROM secrets WHERE tenant_id = $1 AND kek_version <> $2`, tenant, ver)
		if err != nil {
			return err
		}
		type item struct {
			id   uuid.UUID
			wdek []byte
			ver  int
		}
		var items []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.id, &it.wdek, &it.ver); err != nil {
				rows.Close()
				return err
			}
			items = append(items, it)
		}
		rows.Close()
		for _, it := range items {
			old, err := v.kek(ctx, tx, tenant, it.ver)
			if err != nil {
				return err
			}
			dek, err := open(old, it.wdek, tenant[:])
			if err != nil {
				return err
			}
			rewrapped, err := seal(kek, dek, tenant[:])
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE secrets SET wrapped_key = $2, kek_version = $3 WHERE id = $1`, it.id, rewrapped, ver); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE tenant_keys SET retired_at = now() WHERE tenant_id = $1 AND version < $2 AND retired_at IS NULL`, tenant, ver); err != nil {
			return err
		}
		return audit(ctx, tx, tenant, by, "secret.rotate_key", "tenant_key", map[string]any{"version": ver, "rewrapped": len(items)})
	})
	return newVersion, err
}

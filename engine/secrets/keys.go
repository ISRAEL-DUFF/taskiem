package secrets

// The tenant key hierarchy (spec 14.1; decision 0019):
//
//	value ── data key (AES-256-GCM, one per value)
//	           └─ tenant key (KEK), version n
//	                └─ platform KMS root key            (every tenant)
//	                     └─ customer key (BYOK)          (when one is in use)
//
// A tenant key wrapped by a customer key is stored as
// customer.Wrap(platformKMS.Encrypt(root, kek)): unwrapping it needs both
// the customer's KMS and the platform's, and the customer's KMS never sees
// the tenant key itself.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/byok"
)

// DefaultBYOKCacheTTL bounds how long an unwrapped tenant key that depends
// on a customer key stays in a process's memory.
const DefaultBYOKCacheTTL = 5 * time.Minute

// DefaultDestroyAfter is how long a retired, unused tenant key version is
// kept before its wrapped material is destroyed.
const DefaultDestroyAfter = 24 * time.Hour

// failFast is how long a failed unwrap is remembered, so a revoked or
// unreachable key fails each read at once instead of each waiting for the
// provider's timeout. The key job's health check clears it on recovery.
const failFast = 15 * time.Second

// ErrKeyUnavailable means the tenant's key could not be unwrapped: its
// customer key is revoked, disabled or unreachable, or the platform KMS is
// down. Nothing is decrypted or encrypted for the tenant until it returns;
// the worker parks steps that need it and resumes them afterwards.
var ErrKeyUnavailable = errors.New("tenant key unavailable")

// ErrKeyDestroyed means a retired tenant key version's material was
// destroyed (nothing should still reference it).
var ErrKeyDestroyed = errors.New("tenant key version destroyed")

// cachedKey is an unwrapped key with the stored material it came from. A
// cache hit needs the row to hold the same material, so an entry made in a
// transaction that rolled back (or a row changed since) is never used.
type cachedKey struct {
	key     []byte
	wrapped string
	exp     time.Time // zero: until the process ends
}

func (c cachedKey) valid(now time.Time, wrapped string) bool {
	return c.wrapped == wrapped && (c.exp.IsZero() || now.Before(c.exp))
}

type cachedProvider struct {
	credentials string // the sealed credentials it was built from
	p           byok.Provider
}

func (v *Vault) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v *Vault) ttl() time.Duration {
	if v.BYOKCacheTTL > 0 {
		return v.BYOKCacheTTL
	}
	return DefaultBYOKCacheTTL
}

func (v *Vault) factory() *byok.Factory {
	if v.BYOK != nil {
		return v.BYOK
	}
	return &byok.Factory{}
}

func (v *Vault) kekCacheKey(t uuid.UUID, version int) string { return fmt.Sprintf("%s/%d", t, version) }

// forget drops everything cached for a tenant in this process (a customer
// key found unavailable, or replaced).
func (v *Vault) forget(tenant uuid.UUID) {
	prefix := tenant.String() + "/"
	v.keks.Range(func(k, _ any) bool {
		if strings.HasPrefix(k.(string), prefix) {
			v.keks.Delete(k)
		}
		return true
	})
	v.pseudo.Delete(tenant)
}

func (v *Vault) unavailable(tenant uuid.UUID, err error) error {
	v.failing.Store(tenant, v.now().Add(failFast))
	return fmt.Errorf("%w: %w", ErrKeyUnavailable, err)
}

// currentKEK returns the tenant's newest KEK, creating version 1 if needed.
func (v *Vault) currentKEK(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (int, []byte, error) {
	var version int
	err := tx.QueryRow(ctx, `SELECT version FROM tenant_keys WHERE tenant_id = $1 AND retired_at IS NULL ORDER BY version DESC LIMIT 1`, tenant).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return v.newKEK(ctx, tx, tenant, 1, nil)
	}
	if err != nil {
		return 0, nil, err
	}
	kek, err := v.kek(ctx, tx, tenant, version)
	return version, kek, err
}

// newKEK creates a tenant key version, wrapped by the platform key and, if
// the tenant has a customer key in use, by that key too. p, when set, is
// that key's provider (onboarding passes the one it just verified).
func (v *Vault) newKEK(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, version int, p *inUseKey) (int, []byte, error) {
	if p == nil {
		var err error
		if p, err = v.inUseKey(ctx, tx, tenant); err != nil {
			return 0, nil, err
		}
	}
	kek, err := newKey()
	if err != nil {
		return 0, nil, err
	}
	wrapped, err := v.KMS.Encrypt(ctx, v.RootKey, kek)
	if err != nil {
		return 0, nil, v.unavailable(tenant, fmt.Errorf("platform KMS: %w", err))
	}
	var byokID *uuid.UUID
	exp := time.Time{}
	if p != nil {
		if wrapped, err = p.provider.Wrap(ctx, []byte(wrapped)); err != nil {
			return 0, nil, v.unavailable(tenant, err)
		}
		byokID, exp = &p.id, v.now().Add(v.ttl())
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tenant_keys (tenant_id, version, wrapped_kek, kms_key, byok_key_id) VALUES ($1, $2, $3, $4, $5)`,
		tenant, version, wrapped, v.RootKey, byokID); err != nil {
		return 0, nil, err
	}
	v.keks.Store(v.kekCacheKey(tenant, version), cachedKey{key: kek, wrapped: wrapped, exp: exp})
	return version, kek, nil
}

// kek returns tenant key version, from the cache or unwrapped. The row is
// read either way (one indexed query), so a destroyed or changed version is
// never served from memory.
func (v *Vault) kek(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, version int) ([]byte, error) {
	c, err := v.kekEntry(ctx, tx, tenant, version)
	return c.key, err
}

func (v *Vault) kekEntry(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, version int) (cachedKey, error) {
	now := v.now()
	var wrapped, kmsKey string
	var byokID *uuid.UUID
	var destroyed *time.Time
	if err := tx.QueryRow(ctx, `SELECT wrapped_kek, kms_key, byok_key_id, destroyed_at FROM tenant_keys WHERE tenant_id = $1 AND version = $2`, tenant, version).
		Scan(&wrapped, &kmsKey, &byokID, &destroyed); err != nil {
		return cachedKey{}, err
	}
	if destroyed != nil {
		return cachedKey{}, fmt.Errorf("version %d: %w", version, ErrKeyDestroyed)
	}
	if k, ok := v.keks.Load(v.kekCacheKey(tenant, version)); ok && k.(cachedKey).valid(now, wrapped) {
		return k.(cachedKey), nil
	}
	if until, ok := v.failing.Load(tenant); ok && now.Before(until.(time.Time)) {
		return cachedKey{}, fmt.Errorf("%w: unwrapping failed moments ago; retrying shortly", ErrKeyUnavailable)
	}
	c := cachedKey{wrapped: wrapped}
	if byokID != nil {
		p, err := v.provider(ctx, tx, tenant, *byokID)
		if err != nil {
			return cachedKey{}, err
		}
		inner, err := p.Unwrap(ctx, wrapped)
		if err != nil {
			return cachedKey{}, v.unavailable(tenant, err)
		}
		c.exp = now.Add(v.ttl())
		wrapped = string(inner)
	}
	kek, err := v.KMS.Decrypt(ctx, kmsKey, wrapped)
	if err != nil {
		return cachedKey{}, v.unavailable(tenant, fmt.Errorf("unwrap tenant key: %w", err))
	}
	c.key = kek
	v.keks.Store(v.kekCacheKey(tenant, version), c)
	return c, nil
}

// inUseKey is the customer key that wraps new tenant key versions.
type inUseKey struct {
	id       uuid.UUID
	provider byok.Provider
}

// inUseKey returns the tenant's customer key, or nil when it has none.
func (v *Vault) inUseKey(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (*inUseKey, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM tenant_byok_keys WHERE tenant_id = $1 AND status <> 'retired'`, tenant).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p, err := v.provider(ctx, tx, tenant, id)
	if err != nil {
		return nil, err
	}
	return &inUseKey{id: id, provider: p}, nil
}

// credentialsContext binds sealed BYOK credentials to their tenant and key
// row, so they cannot be moved to another (the platform KMS authenticates
// the whole plaintext).
func credentialsContext(tenant, id uuid.UUID) string {
	return "taskiem/byok-credentials/v1\x00" + tenant.String() + "\x00" + id.String() + "\x00"
}

func (v *Vault) sealCredentials(ctx context.Context, tenant, id uuid.UUID, creds map[string]string) (string, error) {
	raw, _ := json.Marshal(creds)
	ct, err := v.KMS.Encrypt(ctx, v.RootKey, append([]byte(credentialsContext(tenant, id)), raw...))
	if err != nil {
		return "", fmt.Errorf("platform KMS: %w", err)
	}
	return ct, nil
}

func (v *Vault) openCredentials(ctx context.Context, tenant, id uuid.UUID, kmsKey, sealed string) (map[string]string, error) {
	if sealed == "" {
		return nil, fmt.Errorf("%w: the customer key's credentials were removed", ErrKeyUnavailable)
	}
	pt, err := v.KMS.Decrypt(ctx, kmsKey, sealed)
	if err != nil {
		return nil, v.unavailable(tenant, fmt.Errorf("platform KMS: %w", err))
	}
	raw, ok := strings.CutPrefix(string(pt), credentialsContext(tenant, id))
	if !ok {
		return nil, fmt.Errorf("customer key credentials are bound to another key")
	}
	var creds map[string]string
	if err := json.Unmarshal([]byte(raw), &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

// provider builds (or reuses) the provider for a customer key row.
func (v *Vault) provider(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (byok.Provider, error) {
	var cfgRaw []byte
	var sealed, kmsKey string
	if err := tx.QueryRow(ctx, `SELECT config, credentials, kms_key FROM tenant_byok_keys WHERE tenant_id = $1 AND id = $2`, tenant, id).
		Scan(&cfgRaw, &sealed, &kmsKey); err != nil {
		return nil, fmt.Errorf("customer key %s: %w", id, err)
	}
	if c, ok := v.providers.Load(id); ok && c.(cachedProvider).credentials == sealed {
		return c.(cachedProvider).p, nil
	}
	creds, err := v.openCredentials(ctx, tenant, id, kmsKey, sealed)
	if err != nil {
		return nil, err
	}
	var cfg byok.Config
	if err := json.Unmarshal(cfgRaw, &cfg); err != nil {
		return nil, err
	}
	p, err := v.factory().New(cfg, creds, byok.Options{Tenant: tenant.String()})
	if err != nil {
		return nil, v.unavailable(tenant, err)
	}
	v.providers.Store(id, cachedProvider{credentials: sealed, p: p})
	return p, nil
}

// secretAAD is the associated data binding a secret's ciphertext to where
// it belongs. Version 1 (before migration 00100) bound only the id; version
// 2 binds tenant, environment, name (or owning connection) and id, so a row
// moved to another name or environment no longer decrypts (S33).
func secretAAD(aadVersion int, tenant uuid.UUID, env, binding string, id uuid.UUID) []byte {
	if aadVersion < 2 {
		return id[:]
	}
	return []byte("taskiem/secret/v2\x00" + tenant.String() + "\x00" + env + "\x00" + binding + "\x00" + id.String())
}

// connectionBinding is what a connection's credentials are bound to.
func connectionBinding(conn uuid.UUID) string { return "connection:" + conn.String() }

// encrypt seals value under a fresh data key bound to the secret's place.
func (v *Vault) encrypt(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, env, binding string, value []byte) (ct, wrappedDEK []byte, version int, err error) {
	version, kek, err := v.currentKEK(ctx, tx, tenant)
	if err != nil {
		return nil, nil, 0, err
	}
	dek, err := newKey()
	if err != nil {
		return nil, nil, 0, err
	}
	if ct, err = seal(dek, value, secretAAD(2, tenant, env, binding, id)); err != nil {
		return nil, nil, 0, err
	}
	if wrappedDEK, err = seal(kek, dek, tenant[:]); err != nil {
		return nil, nil, 0, err
	}
	return ct, wrappedDEK, version, nil
}

func (v *Vault) decrypt(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, env, binding string, aadVersion int, ct, wrappedDEK []byte, version int) ([]byte, error) {
	kek, err := v.kek(ctx, tx, tenant, version)
	if err != nil {
		return nil, err
	}
	dek, err := open(kek, wrappedDEK, tenant[:])
	if err != nil {
		return nil, fmt.Errorf("unwrap data key: %w", err)
	}
	return open(dek, ct, secretAAD(aadVersion, tenant, env, binding, id))
}

// pseudonymAAD binds the sealed pseudonymisation key to its tenant.
func pseudonymAAD(tenant uuid.UUID) []byte {
	return []byte("taskiem/pseudonym/v1\x00" + tenant.String())
}

// pseudonymKey returns the tenant's pseudonymisation key (subject ids, spec
// 5.2). The first call for a tenant creates it from tenant key version 1's
// material, which is what subject ids were keyed with before migration
// 00100, and seals it under the current tenant key.
func (v *Vault) pseudonymKey(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]byte, error) {
	var wrapped []byte
	var ver int
	err := tx.QueryRow(ctx, `SELECT wrapped_key, kek_version FROM tenant_pseudonym_keys WHERE tenant_id = $1`, tenant).Scan(&wrapped, &ver)
	stored := fmt.Sprintf("%d:%x", ver, wrapped)
	if c, ok := v.pseudo.Load(tenant); ok && err == nil && c.(cachedKey).valid(v.now(), stored) {
		return c.(cachedKey).key, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		cur, kek, err := v.currentKEK(ctx, tx, tenant) // creates version 1 for a new tenant
		if err != nil {
			return nil, err
		}
		k1, err := v.kek(ctx, tx, tenant, 1)
		if err != nil {
			return nil, err
		}
		w, err := seal(kek, k1, pseudonymAAD(tenant))
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tenant_pseudonym_keys (tenant_id, wrapped_key, kek_version) VALUES ($1, $2, $3) ON CONFLICT (tenant_id) DO NOTHING`, tenant, w, cur); err != nil {
			return nil, err
		}
		return v.pseudonymKey(ctx, tx, tenant)
	}
	if err != nil {
		return nil, err
	}
	c, err := v.kekEntry(ctx, tx, tenant, ver)
	if err != nil {
		return nil, err
	}
	key, err := open(c.key, wrapped, pseudonymAAD(tenant))
	if err != nil {
		return nil, fmt.Errorf("unwrap pseudonym key: %w", err)
	}
	v.pseudo.Store(tenant, cachedKey{key: key, wrapped: stored, exp: c.exp})
	return key, nil
}

func digest(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}

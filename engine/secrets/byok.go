package secrets

// Customer keys (BYOK): onboarding with a verified round trip, credential
// replacement, health checks, returning to the platform key, and the
// status pages and the CLI read. docs/byok.md; decision 0019.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/byok"
	"github.com/israel-duff/taskiem/engine/db"
)

// ErrVerify means a customer key failed the onboarding or credential
// round trip; nothing was changed.
var ErrVerify = errors.New("customer key verification failed")

// ErrNoBYOK means the tenant has no customer key in use.
var ErrNoBYOK = errors.New("no customer key in use")

// unavailableAfter is how many consecutive failed checks mark a customer
// key unavailable (and raise the key_health alert). Steps park at the
// first failed unwrap regardless.
const unavailableAfter = 2

// BYOKKey is a customer key as pages and the CLI show it: no credentials.
type BYOKKey struct {
	ID                uuid.UUID   `json:"id"`
	Provider          string      `json:"provider"`
	Description       string      `json:"description"`
	Config            byok.Config `json:"config"`
	CredentialsDigest string      `json:"credentials_digest,omitempty"`
	Status            string      `json:"status"` // active, unavailable, retired
	CheckedAt         *time.Time  `json:"checked_at,omitempty"`
	LastOKAt          *time.Time  `json:"last_ok_at,omitempty"`
	FailingSince      *time.Time  `json:"failing_since,omitempty"`
	CheckFailures     int         `json:"check_failures"`
	LastError         string      `json:"last_error,omitempty"`
	RecoveredAt       *time.Time  `json:"recovered_at,omitempty"`
	CreatedBy         string      `json:"created_by"`
	CreatedAt         time.Time   `json:"created_at"`
	RetiredAt         *time.Time  `json:"retired_at,omitempty"`
}

// KeyVersion is one tenant key version.
type KeyVersion struct {
	Version     int        `json:"version"`
	WrappedBy   string     `json:"wrapped_by"` // "platform" or "customer"
	BYOKKeyID   *uuid.UUID `json:"byok_key_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	RetiredAt   *time.Time `json:"retired_at,omitempty"`
	DestroyedAt *time.Time `json:"destroyed_at,omitempty"`
	Secrets     int        `json:"secrets"`
	SubjectKeys int        `json:"subject_keys"`
}

// KeyStatus is the tenant's key hierarchy as an owner sees it.
type KeyStatus struct {
	Mode            string       `json:"mode"` // "platform" or "customer"
	CurrentVersion  int          `json:"current_version"`
	Versions        []KeyVersion `json:"versions"`
	BYOK            *BYOKKey     `json:"byok,omitempty"`
	RetiredBYOK     []BYOKKey    `json:"retired_byok,omitempty"`
	Rewrap          *RewrapDue   `json:"rewrap,omitempty"`
	ParkedSteps     int          `json:"parked_steps"`
	CacheTTLSeconds int          `json:"cache_ttl_seconds"`
}

// RewrapDue says that re-wrapping is under way.
type RewrapDue struct {
	Reason    string    `json:"reason"`
	Since     time.Time `json:"since"`
	NotBefore time.Time `json:"not_before"`
	Secrets   int       `json:"secrets"`      // still under an older version or context
	Subjects  int       `json:"subject_keys"` // still under an older version
	LastError string    `json:"last_error,omitempty"`
}

const byokColumns = `id, provider, description, config, credentials_digest, status, checked_at, last_ok_at, failing_since, check_failures,
	COALESCE(last_error, ''), recovered_at, created_by, created_at, retired_at`

func scanBYOK(row pgx.Row) (BYOKKey, error) {
	var k BYOKKey
	var cfg []byte
	err := row.Scan(&k.ID, &k.Provider, &k.Description, &cfg, &k.CredentialsDigest, &k.Status, &k.CheckedAt, &k.LastOKAt, &k.FailingSince,
		&k.CheckFailures, &k.LastError, &k.RecoveredAt, &k.CreatedBy, &k.CreatedAt, &k.RetiredAt)
	if err == nil {
		_ = json.Unmarshal(cfg, &k.Config)
	}
	return k, err
}

// KeyStatus reports the tenant's tenant key versions, customer key and
// re-wrapping progress. It decrypts nothing.
func (v *Vault) KeyStatus(ctx context.Context, tenant uuid.UUID) (KeyStatus, error) {
	st := KeyStatus{Mode: "platform", CacheTTLSeconds: int(v.ttl() / time.Second), Versions: []KeyVersion{}}
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT k.version, k.byok_key_id, k.created_at, k.retired_at, k.destroyed_at,
			(SELECT count(*) FROM secrets s WHERE s.tenant_id = k.tenant_id AND s.kek_version = k.version),
			(SELECT count(*) FROM subject_keys sk WHERE sk.tenant_id = k.tenant_id AND sk.kek_version = k.version AND sk.wrapped_key IS NOT NULL)
			FROM tenant_keys k WHERE k.tenant_id = $1 ORDER BY k.version DESC`, tenant)
		if err != nil {
			return err
		}
		for rows.Next() {
			var kv KeyVersion
			if err := rows.Scan(&kv.Version, &kv.BYOKKeyID, &kv.CreatedAt, &kv.RetiredAt, &kv.DestroyedAt, &kv.Secrets, &kv.SubjectKeys); err != nil {
				rows.Close()
				return err
			}
			kv.WrappedBy = "platform"
			if kv.BYOKKeyID != nil {
				kv.WrappedBy = "customer"
			}
			if st.CurrentVersion == 0 && kv.RetiredAt == nil {
				st.CurrentVersion = kv.Version
			}
			st.Versions = append(st.Versions, kv)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT `+byokColumns+` FROM tenant_byok_keys WHERE tenant_id = $1 ORDER BY created_at DESC`, tenant)
		if err != nil {
			return err
		}
		for rows.Next() {
			k, err := scanBYOK(rows)
			if err != nil {
				rows.Close()
				return err
			}
			if k.Status == "retired" {
				st.RetiredBYOK = append(st.RetiredBYOK, k)
			} else {
				st.BYOK, st.Mode = &k, "customer"
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		var d RewrapDue
		err = tx.QueryRow(ctx, `SELECT reason, since, not_before, COALESCE(last_error, '') FROM key_rewrap_due WHERE tenant_id = $1`, tenant).
			Scan(&d.Reason, &d.Since, &d.NotBefore, &d.LastError)
		if err == nil {
			if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM secrets WHERE tenant_id = $1 AND (kek_version <> $2 OR aad_version < 2)),
				(SELECT count(*) FROM subject_keys WHERE tenant_id = $1 AND kek_version <> $2 AND wrapped_key IS NOT NULL)`, tenant, st.CurrentVersion).
				Scan(&d.Secrets, &d.Subjects); err != nil {
				return err
			}
			st.Rewrap = &d
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM key_parked_steps WHERE tenant_id = $1`, tenant).Scan(&st.ParkedSteps)
	})
	return st, err
}

// verify wraps a fresh canary with p and unwraps it again.
func verify(ctx context.Context, p byok.Provider) (ct string, canary []byte, err error) {
	canary = make([]byte, 32)
	if _, err := rand.Read(canary); err != nil {
		return "", nil, err
	}
	if ct, err = p.Wrap(ctx, canary); err != nil {
		return "", nil, fmt.Errorf("%w: wrapping with the key: %w", ErrVerify, err)
	}
	back, err := p.Unwrap(ctx, ct)
	if err != nil {
		return "", nil, fmt.Errorf("%w: unwrapping with the key: %w", ErrVerify, err)
	}
	if !bytes.Equal(back, canary) {
		return "", nil, fmt.Errorf("%w: the key returned different bytes than it was given", ErrVerify)
	}
	return ct, canary, nil
}

// EnableBYOK onboards a customer key. The key must wrap and unwrap a fresh
// canary before anything is stored. Then, in one transaction, the key and
// its sealed credentials are recorded, a new tenant key version wrapped by
// it becomes current, and re-wrapping of everything else is queued (the key
// job moves the data keys over; until it has, the older versions are still
// wrapped by the platform key only). A customer key already in use is
// replaced: it is retired once nothing depends on it.
func (v *Vault) EnableBYOK(ctx context.Context, tenant uuid.UUID, cfg byok.Config, creds map[string]string, by string) (BYOKKey, int, error) {
	cfg = cfg.Normalise()
	if err := byok.Validate(cfg, creds); err != nil {
		return BYOKKey{}, 0, err
	}
	p, err := v.factory().New(cfg, creds, byok.Options{Tenant: tenant.String()})
	if err != nil {
		return BYOKKey{}, 0, err
	}
	check, canary, err := verify(ctx, p)
	if err != nil {
		return BYOKKey{}, 0, err
	}
	id := uuid.Must(uuid.NewV7())
	sealed, err := v.sealCredentials(ctx, tenant, id, creds)
	if err != nil {
		return BYOKKey{}, 0, err
	}
	cfgRaw, _ := json.Marshal(cfg)
	var out BYOKKey
	var version int
	err = db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var replaced *uuid.UUID
		if err := tx.QueryRow(ctx, `UPDATE tenant_byok_keys SET status = 'retired', retired_at = now(), updated_at = now()
			WHERE tenant_id = $1 AND status <> 'retired' RETURNING id`, tenant).Scan(&replaced); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tenant_byok_keys (id, tenant_id, provider, config, description, credentials, credentials_digest, kms_key,
			check_ciphertext, check_digest, status, checked_at, last_ok_at, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'active', now(), now(), $11)`,
			id, tenant, cfg.Provider, cfgRaw, cfg.Describe(), sealed, byok.CredentialDigest(creds), v.RootKey, check, digest(canary), by); err != nil {
			return err
		}
		v.providers.Store(id, cachedProvider{credentials: sealed, p: p})
		var err error
		if version, err = v.rotateTx(ctx, tx, tenant, &inUseKey{id: id, provider: p}, "byok"); err != nil {
			return err
		}
		detail := map[string]any{"byok_key_id": id, "provider": cfg.Provider, "key": cfg.Describe(), "version": version, "credentials_digest": byok.CredentialDigest(creds)}
		if replaced != nil {
			detail["replaced"] = *replaced
		}
		if err := audit(ctx, tx, tenant, by, "key.byok_enabled", cfg.Describe(), detail); err != nil {
			return err
		}
		out, err = scanBYOK(tx.QueryRow(ctx, `SELECT `+byokColumns+` FROM tenant_byok_keys WHERE id = $1`, id))
		return err
	})
	if err != nil {
		v.providers.Delete(id)
	}
	v.failing.Delete(tenant)
	return out, version, err
}

// ReplaceBYOKCredentials stores new credentials for the customer key in use
// (the customer rotated its token or access key). They must unwrap the
// canary wrapped at onboarding, which proves they reach the same key.
func (v *Vault) ReplaceBYOKCredentials(ctx context.Context, tenant uuid.UUID, creds map[string]string, by string) (BYOKKey, error) {
	var id uuid.UUID
	var cfgRaw []byte
	var check string
	var want []byte
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id, config, check_ciphertext, check_digest FROM tenant_byok_keys WHERE tenant_id = $1 AND status <> 'retired'`, tenant).
			Scan(&id, &cfgRaw, &check, &want)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoBYOK
		}
		return err
	})
	if err != nil {
		return BYOKKey{}, err
	}
	var cfg byok.Config
	_ = json.Unmarshal(cfgRaw, &cfg)
	if err := byok.Validate(cfg, creds); err != nil {
		return BYOKKey{}, err
	}
	p, err := v.factory().New(cfg, creds, byok.Options{Tenant: tenant.String()})
	if err != nil {
		return BYOKKey{}, err
	}
	pt, err := p.Unwrap(ctx, check)
	if err != nil {
		return BYOKKey{}, fmt.Errorf("%w: the new credentials cannot unwrap with the key: %w", ErrVerify, err)
	}
	if !bytes.Equal(digest(pt), want) {
		return BYOKKey{}, fmt.Errorf("%w: the new credentials reach a different key", ErrVerify)
	}
	sealed, err := v.sealCredentials(ctx, tenant, id, creds)
	if err != nil {
		return BYOKKey{}, err
	}
	var out BYOKKey
	err = db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE tenant_byok_keys SET credentials = $2, credentials_digest = $3, updated_at = now() WHERE id = $1 AND status <> 'retired'`,
			id, sealed, byok.CredentialDigest(creds))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNoBYOK
		}
		if err := audit(ctx, tx, tenant, by, "key.byok_credentials_replaced", cfg.Describe(), map[string]any{"byok_key_id": id, "credentials_digest": byok.CredentialDigest(creds)}); err != nil {
			return err
		}
		out, err = scanBYOK(tx.QueryRow(ctx, `SELECT `+byokColumns+` FROM tenant_byok_keys WHERE id = $1`, id))
		return err
	})
	if err == nil {
		v.providers.Store(id, cachedProvider{credentials: sealed, p: p})
		v.failing.Delete(tenant)
	}
	return out, err
}

// DisableBYOK returns the tenant to the platform key: the customer key is
// retired and a new tenant key version wrapped only by the platform key
// becomes current. Re-wrapping data keys onto it still needs the customer
// key to unwrap the versions it wrapped; once done, the customer key is no
// longer used and its credentials are forgotten.
func (v *Vault) DisableBYOK(ctx context.Context, tenant uuid.UUID, by string) (int, error) {
	var version int
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var id uuid.UUID
		var desc string
		err := tx.QueryRow(ctx, `UPDATE tenant_byok_keys SET status = 'retired', retired_at = now(), updated_at = now()
			WHERE tenant_id = $1 AND status <> 'retired' RETURNING id, description`, tenant).Scan(&id, &desc)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoBYOK
		}
		if err != nil {
			return err
		}
		if version, err = v.rotateTx(ctx, tx, tenant, nil, "byok_disabled"); err != nil {
			return err
		}
		return audit(ctx, tx, tenant, by, "key.byok_disabled", desc, map[string]any{"byok_key_id": id, "version": version})
	})
	return version, err
}

// Health is the result of checking a tenant's keys.
type Health struct {
	Customer  bool   `json:"customer"`         // a customer key is in use
	OK        bool   `json:"ok"`               // the tenant key unwraps
	Status    string `json:"status,omitempty"` // the customer key's status after the check
	Error     string `json:"error,omitempty"`
	Changed   bool   `json:"changed"`   // the status changed with this check
	Recovered bool   `json:"recovered"` // it was unavailable and works again
}

// CheckKey checks that the tenant's keys work: with a customer key, it
// unwraps the onboarding canary with it (never from cache) and records the
// result, marking the key unavailable after consecutive failures and
// available again on success (both audited); then it unwraps the current
// tenant key. by is "system" for the key job; other callers' checks are
// audited too.
func (v *Vault) CheckKey(ctx context.Context, tenant uuid.UUID, by string) (Health, error) {
	var h Health
	var id uuid.UUID
	var check string
	var want []byte
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id, check_ciphertext, check_digest FROM tenant_byok_keys WHERE tenant_id = $1 AND status <> 'retired'`, tenant).
			Scan(&id, &check, &want)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		h.Customer = true
		return err
	})
	if err != nil {
		return h, err
	}
	var cause error
	if h.Customer {
		err = db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			// A failure here is the check's result, not the transaction's.
			cause = v.checkCanary(ctx, tx, tenant, id, check, want)
			return nil
		})
		if err != nil {
			return h, err
		}
		if err := v.recordCheck(ctx, tenant, id, cause, by, &h); err != nil {
			return h, err
		}
	}
	if cause == nil {
		// The current tenant key itself (also the platform KMS).
		err = db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, _, err := v.currentKEK(ctx, tx, tenant)
			return err
		})
		if err != nil && !errors.Is(err, ErrKeyUnavailable) {
			return h, err
		}
		cause = err
	}
	h.OK = cause == nil
	if cause != nil {
		h.Error = clipError(cause)
	}
	return h, nil
}

// checkCanary unwraps the onboarding canary with the customer key.
func (v *Vault) checkCanary(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, check string, want []byte) error {
	p, err := v.provider(ctx, tx, tenant, id)
	if err != nil {
		return err
	}
	pt, err := p.Unwrap(ctx, check)
	if err != nil {
		return err
	}
	if !bytes.Equal(digest(pt), want) {
		return errors.New("the key unwrapped the check value to different bytes")
	}
	return nil
}

// recordCheck stores a customer key check's result and audits changes.
func (v *Vault) recordCheck(ctx context.Context, tenant, id uuid.UUID, cause error, by string, h *Health) error {
	return db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var status, desc string
		var failingSince *time.Time
		var failures int
		if err := tx.QueryRow(ctx, `SELECT status, description, failing_since, check_failures FROM tenant_byok_keys WHERE id = $1 FOR UPDATE`, id).
			Scan(&status, &desc, &failingSince, &failures); err != nil {
			return err
		}
		if status == "retired" { // replaced while we checked
			h.Status = status
			return nil
		}
		if cause == nil {
			if _, err := tx.Exec(ctx, `UPDATE tenant_byok_keys SET status = 'active', checked_at = now(), last_ok_at = now(), check_failures = 0,
				failing_since = NULL, last_error = NULL, recovered_at = CASE WHEN status = 'unavailable' THEN now() ELSE recovered_at END WHERE id = $1`, id); err != nil {
				return err
			}
			v.failing.Delete(tenant)
			h.Status = "active"
			if status == "unavailable" {
				h.Changed, h.Recovered = true, true
				detail := map[string]any{"byok_key_id": id}
				if failingSince != nil {
					detail["unavailable_since"] = failingSince.UTC().Format(time.RFC3339)
					detail["down_seconds"] = int(v.now().Sub(*failingSince).Seconds())
				}
				if err := audit(ctx, tx, tenant, "system", "key.byok_recovered", desc, detail); err != nil {
					return err
				}
			}
		} else {
			failures++
			next := status
			if failures >= unavailableAfter {
				next = "unavailable"
			}
			if _, err := tx.Exec(ctx, `UPDATE tenant_byok_keys SET status = $2, checked_at = now(), check_failures = $3,
				failing_since = COALESCE(failing_since, now()), last_error = $4 WHERE id = $1`, id, next, failures, clipError(cause)); err != nil {
				return err
			}
			v.forget(tenant)
			v.failing.Store(tenant, v.now().Add(failFast))
			h.Status = next
			if next == "unavailable" && status != "unavailable" {
				h.Changed = true
				if err := audit(ctx, tx, tenant, "system", "key.byok_unavailable", desc, map[string]any{"byok_key_id": id, "error": clipError(cause)}); err != nil {
					return err
				}
			}
		}
		if by != "system" {
			return audit(ctx, tx, tenant, by, "key.byok_checked", desc, map[string]any{"byok_key_id": id, "ok": cause == nil})
		}
		return nil
	})
}

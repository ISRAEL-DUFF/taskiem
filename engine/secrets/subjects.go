package secrets

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/pii"
)

// subjectID derives a stable pseudonymous id for a data subject from the
// value, keyed by the tenant's pseudonymisation key (spec 5.2: hmac(tenant
// key, bvn)). That key started as tenant key version 1's material and is
// kept apart from it since migration 00100, so ids never change while
// tenant keys rotate.
func (v *Vault) subjectID(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (string, error) {
	key, err := v.pseudonymKey(ctx, tx, tenant)
	if err != nil {
		return "", err
	}
	return subjectIDWith(key, category, value), nil
}

func subjectIDWith(key []byte, category string, value any) string {
	raw, _ := json.Marshal(value)
	m := hmac.New(sha256.New, append([]byte("taskiem/subject/v1\x00"), key...))
	m.Write([]byte(category + "\x00"))
	m.Write(raw)
	return category + ":" + hex.EncodeToString(m.Sum(nil))[:32]
}

// subjectKey returns the subject's data key, creating it if needed.
func (v *Vault) subjectKey(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject, category string, create bool) ([]byte, error) {
	var wrapped []byte
	var ver *int
	err := tx.QueryRow(ctx, `SELECT wrapped_key, kek_version FROM subject_keys WHERE tenant_id = $1 AND subject_id = $2`, tenant, subject).Scan(&wrapped, &ver)
	switch {
	case errors.Is(err, pgx.ErrNoRows) && create:
		version, kek, err := v.currentKEK(ctx, tx, tenant)
		if err != nil {
			return nil, err
		}
		dek, err := newKey()
		if err != nil {
			return nil, err
		}
		w, err := seal(kek, dek, []byte(tenant.String()+"\x00"+subject))
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO subject_keys (tenant_id, subject_id, wrapped_key, kek_version, category) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (tenant_id, subject_id) DO NOTHING`, tenant, subject, w, version, category); err != nil {
			return nil, err
		}
		return v.subjectKey(ctx, tx, tenant, subject, category, false)
	case errors.Is(err, pgx.ErrNoRows):
		return nil, pii.ErrErased // no key: never sealed here, or purged
	case err != nil:
		return nil, err
	case wrapped == nil:
		return nil, pii.ErrErased
	}
	kek, err := v.kek(ctx, tx, tenant, *ver)
	if err != nil {
		return nil, err
	}
	return open(kek, wrapped, []byte(tenant.String()+"\x00"+subject))
}

// SealTx encrypts one personal value under its subject's key.
func (v *Vault) SealTx(ctx context.Context, tx pgx.Tx, t uuid.UUID, category string, value any) (map[string]any, error) {
	subject, err := v.subjectID(ctx, tx, t, category, value)
	if err != nil {
		return nil, err
	}
	dek, err := v.subjectKey(ctx, tx, t, subject, category, true)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(value)
	ct, err := seal(dek, raw, []byte(subject))
	if err != nil {
		return nil, err
	}
	return map[string]any{"$pii": category, "subject": subject, "ct": base64.StdEncoding.EncodeToString(ct)}, nil
}

// OpenTx decrypts an envelope; it returns pii.ErrErased once the subject's
// key is destroyed.
func (v *Vault) OpenTx(ctx context.Context, tx pgx.Tx, t uuid.UUID, env map[string]any) (any, error) {
	subject, _ := env["subject"].(string)
	ctB64, _ := env["ct"].(string)
	ct, err := base64.StdEncoding.DecodeString(ctB64)
	if err != nil || subject == "" {
		return nil, fmt.Errorf("malformed pii envelope")
	}
	dek, err := v.subjectKey(ctx, tx, t, subject, "", false)
	if err != nil {
		return nil, err
	}
	raw, err := open(dek, ct, []byte(subject))
	if err != nil {
		return nil, err
	}
	out, err := expr.DecodeJSON(raw)
	return out, err
}

// SubjectFor returns the subject id a value would be sealed under.
func (v *Vault) SubjectFor(ctx context.Context, tenant uuid.UUID, category string, value any) (string, error) {
	var s string
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		s, err = v.subjectID(ctx, tx, tenant, category, value)
		return err
	})
	return s, err
}

// Erase destroys a subject's key (NDPA erasure, spec 9.4). It is one-way.
func (v *Vault) Erase(ctx context.Context, tenant uuid.UUID, subject, by string) error {
	return db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE subject_keys SET wrapped_key = NULL, shredded_at = now() WHERE tenant_id = $1 AND subject_id = $2 AND shredded_at IS NULL`, tenant, subject)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit(ctx, tx, tenant, by, "pii.erase", subject, map[string]any{})
	})
}

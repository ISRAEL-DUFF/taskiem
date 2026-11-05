package secrets

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// PutLegacy stores a named secret as code before migration 00100 did:
// associated data is the id only (aad_version 1).
func (v *Vault) PutLegacy(ctx context.Context, tenant uuid.UUID, env, name string, value []byte) error {
	return db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		id := uuid.Must(uuid.NewV7())
		ver, kek, err := v.currentKEK(ctx, tx, tenant)
		if err != nil {
			return err
		}
		dek, _ := newKey()
		ct, _ := seal(dek, value, secretAAD(1, tenant, env, name, id))
		wdek, _ := seal(kek, dek, tenant[:])
		_, err = tx.Exec(ctx, `INSERT INTO secrets (id, tenant_id, environment, name, ciphertext, wrapped_key, kek_version, aad_version, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 1, 'legacy')`, id, tenant, env, name, ct, wdek, ver)
		return err
	})
}

// RecordLegacyBindings is the statement migration 00140 runs (as the
// schema owner) to record where each first-context secret is.
const RecordLegacyBindings = `INSERT INTO secret_legacy_bindings (secret_id, tenant_id, environment, binding)
  SELECT s.id, s.tenant_id, s.environment,
         COALESCE(s.name, 'connection:' || (SELECT c.id FROM connections c WHERE c.secret_ref = s.id LIMIT 1)::text, '')
    FROM secrets s
   WHERE s.aad_version < 2
  ON CONFLICT (secret_id) DO NOTHING`

// LegacySubjectID is a subject id as code before migration 00100 derived
// it: keyed by tenant key version 1 directly.
func (v *Vault) LegacySubjectID(ctx context.Context, tenant uuid.UUID, category string, value any) (string, error) {
	var s string
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		k1, err := v.kek(ctx, tx, tenant, 1)
		if err != nil {
			return err
		}
		s = subjectIDWith(k1, category, value)
		return nil
	})
	return s, err
}

// SealRawTx seals a value as code before canonical subject ids did: under
// the subject of the value exactly as given.
func (v *Vault) SealRawTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (map[string]any, error) {
	key, err := v.pseudonymKey(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	subject := rawSubjectIDWith(key, category, value)
	dek, err := v.subjectKey(ctx, tx, tenant, subject, category, true)
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

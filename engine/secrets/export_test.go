package secrets

import (
	"context"

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

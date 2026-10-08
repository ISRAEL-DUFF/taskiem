package secrets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// RewrapResult is what one Rewrap pass did.
type RewrapResult struct {
	Version     int   `json:"version"`      // the tenant key version everything moves to
	Secrets     int   `json:"secrets"`      // data keys re-wrapped (and contexts upgraded)
	SubjectKeys int   `json:"subject_keys"` // personal-data subject keys re-wrapped
	Pseudonym   bool  `json:"pseudonym"`    // the pseudonymisation key re-sealed
	Retired     []int `json:"retired,omitempty"`
	Destroyed   []int `json:"destroyed,omitempty"`
	Done        bool  `json:"done"` // nothing is left under an older version
	Busy        bool  `json:"busy"` // another process is re-wrapping this tenant
}

// DefaultRewrapBatch is how many rows one Rewrap pass handles.
const DefaultRewrapBatch = 500

func (v *Vault) destroyAfter() time.Duration {
	if v.DestroyAfter > 0 {
		return v.DestroyAfter
	}
	return DefaultDestroyAfter
}

// Rewrap moves up to batch data keys and batch subject keys of a tenant
// onto its current tenant key version, upgrades secrets to the second
// encryption context (S33), and seals the pseudonymisation key under the
// current version (S23). Once nothing is left under an older version, it
// retires the older versions; versions retired for DestroyAfter that
// nothing uses have their wrapped material destroyed, and a retired
// customer key nothing depends on any more has its credentials forgotten.
// Every step is audited. It needs every tenant key version involved to be
// unwrappable: with a customer key unavailable it returns ErrKeyUnavailable
// and changes nothing.
func (v *Vault) Rewrap(ctx context.Context, tenant uuid.UUID, batch int) (RewrapResult, error) {
	if batch <= 0 {
		batch = DefaultRewrapBatch
	}
	var res RewrapResult
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		res = RewrapResult{}
		// One re-wrapper per tenant at a time (several scheduler replicas).
		var due bool
		err := tx.QueryRow(ctx, `SELECT true FROM key_rewrap_due WHERE tenant_id = $1 FOR UPDATE SKIP LOCKED`, tenant).Scan(&due)
		if errors.Is(err, pgx.ErrNoRows) {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM key_rewrap_due WHERE tenant_id = $1)`, tenant).Scan(&exists); err != nil {
				return err
			}
			res.Busy, res.Done = exists, !exists
			return nil
		}
		if err != nil {
			return err
		}
		cur, kek, err := v.currentKEK(ctx, tx, tenant)
		if err != nil {
			return err
		}
		res.Version = cur

		// The pseudonymisation key first: it is what keeps version 1 alive.
		var pver int
		err = tx.QueryRow(ctx, `SELECT kek_version FROM tenant_pseudonym_keys WHERE tenant_id = $1`, tenant).Scan(&pver)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if _, err := v.pseudonymKey(ctx, tx, tenant); err != nil {
				return err
			}
			res.Pseudonym = true
		case err != nil:
			return err
		case pver != cur:
			key, err := v.pseudonymKey(ctx, tx, tenant)
			if err != nil {
				return err
			}
			w, err := seal(kek, key, pseudonymAAD(tenant))
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE tenant_pseudonym_keys SET wrapped_key = $2, kek_version = $3 WHERE tenant_id = $1`, tenant, w, cur); err != nil {
				return err
			}
			res.Pseudonym = true
		}

		if res.Secrets, err = v.rewrapSecrets(ctx, tx, tenant, cur, kek, batch); err != nil {
			return err
		}
		if res.SubjectKeys, err = v.rewrapSubjects(ctx, tx, tenant, cur, kek, batch); err != nil {
			return err
		}
		var left bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM secrets WHERE tenant_id = $1 AND (kek_version <> $2 OR aad_version < 2))
			OR EXISTS (SELECT 1 FROM subject_keys WHERE tenant_id = $1 AND kek_version <> $2 AND wrapped_key IS NOT NULL)`, tenant, cur).Scan(&left); err != nil {
			return err
		}
		if res.Secrets+res.SubjectKeys > 0 || res.Pseudonym {
			if err := audit(ctx, tx, tenant, "system", "key.rewrap", "tenant_key", map[string]any{
				"version": cur, "secrets": res.Secrets, "subject_keys": res.SubjectKeys, "pseudonym_key": res.Pseudonym, "remaining": left}); err != nil {
				return err
			}
		}
		if left {
			return nil
		}
		return v.finishRewrap(ctx, tx, tenant, cur, &res)
	})
	if err != nil {
		v.noteRewrapError(ctx, tenant, err)
	}
	return res, err
}

func (v *Vault) rewrapSecrets(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, cur int, kek []byte, batch int) (int, error) {
	rows, err := tx.Query(ctx, `SELECT s.id, s.environment, s.name, (SELECT c.id FROM connections c WHERE c.secret_ref = s.id LIMIT 1),
		s.ciphertext, s.wrapped_key, s.kek_version, s.aad_version
		FROM secrets s WHERE s.tenant_id = $1 AND (s.kek_version <> $2 OR s.aad_version < 2)
		ORDER BY s.id LIMIT $3 FOR UPDATE OF s SKIP LOCKED`, tenant, cur, batch)
	if err != nil {
		return 0, err
	}
	type item struct {
		id       uuid.UUID
		env      string
		name     *string
		conn     *uuid.UUID
		ct, wdek []byte
		ver, aad int
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.env, &it.name, &it.conn, &it.ct, &it.wdek, &it.ver, &it.aad); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, it := range items {
		old, err := v.kek(ctx, tx, tenant, it.ver)
		if err != nil {
			return 0, err
		}
		dek, err := open(old, it.wdek, tenant[:])
		if err != nil {
			return 0, fmt.Errorf("secret %s: unwrap data key: %w", it.id, err)
		}
		binding := ""
		switch {
		case it.name != nil:
			binding = *it.name
		case it.conn != nil:
			binding = connectionBinding(*it.conn)
		}
		ct := it.ct
		if it.aad < 2 {
			// Upgrade only where the secret was recorded (K6): a renamed or
			// moved row stops the job, with the reason, for an operator.
			if err := checkLegacyBinding(ctx, tx, it.id, it.env, binding); err != nil {
				return 0, err
			}
			value, err := open(dek, it.ct, secretAAD(it.aad, tenant, it.env, binding, it.id))
			if err != nil {
				return 0, fmt.Errorf("secret %s: %w", it.id, err)
			}
			if ct, err = seal(dek, value, secretAAD(2, tenant, it.env, binding, it.id)); err != nil {
				return 0, err
			}
		}
		wdek, err := seal(kek, dek, tenant[:])
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE secrets SET ciphertext = $2, wrapped_key = $3, kek_version = $4, aad_version = 2 WHERE id = $1`, it.id, ct, wdek, cur); err != nil {
			return 0, err
		}
		if it.aad < 2 {
			if _, err := tx.Exec(ctx, `DELETE FROM secret_legacy_bindings WHERE secret_id = $1`, it.id); err != nil {
				return 0, err
			}
		}
	}
	return len(items), nil
}

func (v *Vault) rewrapSubjects(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, cur int, kek []byte, batch int) (int, error) {
	rows, err := tx.Query(ctx, `SELECT subject_id, wrapped_key, kek_version FROM subject_keys
		WHERE tenant_id = $1 AND kek_version <> $2 AND wrapped_key IS NOT NULL ORDER BY subject_id LIMIT $3 FOR UPDATE SKIP LOCKED`, tenant, cur, batch)
	if err != nil {
		return 0, err
	}
	type item struct {
		subject string
		w       []byte
		ver     int
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.subject, &it.w, &it.ver); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, it := range items {
		old, err := v.kek(ctx, tx, tenant, it.ver)
		if err != nil {
			return 0, err
		}
		aad := []byte(tenant.String() + "\x00" + it.subject)
		dek, err := open(old, it.w, aad)
		if err != nil {
			return 0, fmt.Errorf("subject key: %w", err)
		}
		w, err := seal(kek, dek, aad)
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE subject_keys SET wrapped_key = $3, kek_version = $4 WHERE tenant_id = $1 AND subject_id = $2`, tenant, it.subject, w, cur); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

// finishRewrap retires versions older than cur, destroys retired versions
// nothing uses once DestroyAfter has passed, forgets retired customer keys
// nothing depends on, and settles the tenant's place in key_rewrap_due.
func (v *Vault) finishRewrap(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, cur int, res *RewrapResult) error {
	rows, err := tx.Query(ctx, `UPDATE tenant_keys SET retired_at = now() WHERE tenant_id = $1 AND version < $2 AND retired_at IS NULL RETURNING version`, tenant, cur)
	if err != nil {
		return err
	}
	if res.Retired, err = pgx.CollectRows(rows, pgx.RowTo[int]); err != nil {
		return err
	}
	if len(res.Retired) > 0 {
		if err := audit(ctx, tx, tenant, "system", "key.rewrap_completed", "tenant_key", map[string]any{"version": cur, "retired": res.Retired}); err != nil {
			return err
		}
	}
	rows, err = tx.Query(ctx, `UPDATE tenant_keys k SET wrapped_kek = '', destroyed_at = now()
		WHERE k.tenant_id = $1 AND k.version <> $2 AND k.destroyed_at IS NULL AND k.retired_at IS NOT NULL
		  AND k.retired_at <= now() - $3::interval
		  AND NOT EXISTS (SELECT 1 FROM secrets s WHERE s.tenant_id = $1 AND s.kek_version = k.version)
		  AND NOT EXISTS (SELECT 1 FROM subject_keys sk WHERE sk.tenant_id = $1 AND sk.kek_version = k.version AND sk.wrapped_key IS NOT NULL)
		  AND NOT EXISTS (SELECT 1 FROM tenant_pseudonym_keys p WHERE p.tenant_id = $1 AND p.kek_version = k.version)
		RETURNING k.version`, tenant, cur, pgInterval(v.destroyAfter()))
	if err != nil {
		return err
	}
	if res.Destroyed, err = pgx.CollectRows(rows, pgx.RowTo[int]); err != nil {
		return err
	}
	for _, ver := range res.Destroyed {
		v.keks.Delete(v.kekCacheKey(tenant, ver))
	}
	if len(res.Destroyed) > 0 {
		if err := audit(ctx, tx, tenant, "system", "key.versions_destroyed", "tenant_key", map[string]any{"versions": res.Destroyed}); err != nil {
			return err
		}
	}
	rows, err = tx.Query(ctx, `UPDATE tenant_byok_keys b SET credentials = '', updated_at = now()
		WHERE b.tenant_id = $1 AND b.status = 'retired' AND b.credentials <> ''
		  AND NOT EXISTS (SELECT 1 FROM tenant_keys k WHERE k.tenant_id = $1 AND k.byok_key_id = b.id AND k.destroyed_at IS NULL)
		RETURNING b.id`, tenant)
	if err != nil {
		return err
	}
	forgotten, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, id := range forgotten {
		v.providers.Delete(id)
		if err := audit(ctx, tx, tenant, "system", "key.byok_forgotten", id.String(), map[string]any{"byok_key_id": id}); err != nil {
			return err
		}
	}
	var next *time.Time
	if err := tx.QueryRow(ctx, `SELECT min(retired_at) + $3::interval FROM tenant_keys WHERE tenant_id = $1 AND version <> $2 AND destroyed_at IS NULL`,
		tenant, cur, pgInterval(v.destroyAfter())).Scan(&next); err != nil {
		return err
	}
	if next == nil {
		res.Done = true
		_, err = tx.Exec(ctx, `DELETE FROM key_rewrap_due WHERE tenant_id = $1`, tenant)
		return err
	}
	res.Done = true // nothing is left to re-wrap; destruction waits
	_, err = tx.Exec(ctx, `UPDATE key_rewrap_due SET not_before = $2, last_error = NULL WHERE tenant_id = $1`, tenant, *next)
	return err
}

// noteRewrapError records why re-wrapping stopped and backs off a minute.
func (v *Vault) noteRewrapError(ctx context.Context, tenant uuid.UUID, cause error) {
	_ = db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE key_rewrap_due SET last_error = $2, not_before = now() + interval '1 minute' WHERE tenant_id = $1`, tenant, clipError(cause))
		return err
	})
}

// pgInterval renders d as a PostgreSQL interval.
func pgInterval(d time.Duration) string { return fmt.Sprintf("%d microseconds", d.Microseconds()) }

func clipError(err error) string {
	s := err.Error()
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	return s
}

// RewrapAll runs Rewrap until nothing is left to re-wrap (the CLI's
// --wait, and tests).
func (v *Vault) RewrapAll(ctx context.Context, tenant uuid.UUID) (RewrapResult, error) {
	var total RewrapResult
	for {
		r, err := v.Rewrap(ctx, tenant, DefaultRewrapBatch)
		if err != nil {
			return total, err
		}
		total.Version, total.Secrets, total.SubjectKeys = r.Version, total.Secrets+r.Secrets, total.SubjectKeys+r.SubjectKeys
		total.Pseudonym = total.Pseudonym || r.Pseudonym
		total.Retired = append(total.Retired, r.Retired...)
		total.Destroyed = append(total.Destroyed, r.Destroyed...)
		if r.Busy {
			return total, fmt.Errorf("another process is re-wrapping this tenant's keys; try again shortly")
		}
		if r.Done {
			total.Done = true
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}

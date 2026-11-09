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
	"regexp"
	"strconv"

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
// tenant keys rotate. A category is lower-case letters and underscores, so
// no id is another's generation (<subject>~2) or contains its prefix.
func (v *Vault) subjectID(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (string, error) {
	if !validCategory(category) {
		return "", fmt.Errorf("personal data category %q: want lower-case letters and underscores", category)
	}
	key, err := v.pseudonymKey(ctx, tx, tenant)
	if err != nil {
		return "", err
	}
	return subjectIDWith(key, category, value), nil
}

var categoryRE = regexp.MustCompile(`^[a-z_]+$`)

func validCategory(c string) bool { return categoryRE.MatchString(c) }

// generationsSQL matches a subject's later generations (<base>~2, ~3...)
// by a range that subject_keys_generations (migration 00321, a
// text_pattern_ops index) serves: every id that starts with "<base>~" and
// nothing else, since '~' is the last printable ASCII character before
// U+007F and ids are ASCII. $2 is the base.
const generationsSQL = `subject_id ~>=~ ($2 || '~') AND subject_id ~<~ ($2 || chr(127))`

// generations returns a subject's base key state and its later
// generations: whether the base exists and is shredded, and, only when it
// is shredded, each generation's shredded state. The base is looked up by
// primary key; generations only exist once a base was shredded.
func generations(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, base string) (exists, shredded bool, gens map[string]bool, err error) {
	err = tx.QueryRow(ctx, `SELECT shredded_at IS NOT NULL FROM subject_keys WHERE tenant_id = $1 AND subject_id = $2`, tenant, base).Scan(&shredded)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil, nil
	}
	if err != nil || !shredded {
		return err == nil, false, nil, err
	}
	rows, err := tx.Query(ctx, `SELECT subject_id, shredded_at IS NOT NULL FROM subject_keys WHERE tenant_id = $1 AND `+generationsSQL, tenant, base)
	if err != nil {
		return true, true, nil, err
	}
	defer rows.Close()
	gens = map[string]bool{}
	for rows.Next() {
		var id string
		var gone bool
		if err := rows.Scan(&id, &gone); err != nil {
			return true, true, nil, err
		}
		gens[id] = gone
	}
	return true, true, gens, rows.Err()
}

// liveGeneration is the generation of base sealed under now: base itself
// while it is not shredded; once it is, the first later generation not
// shredded (<base>~2, ~3...), or, when none is (renew), the next one, or
// "" (not renew).
func liveGeneration(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, base string, renew bool) (string, error) {
	_, shredded, gens, err := generations(ctx, tx, tenant, base)
	if err != nil || !shredded {
		return base, err
	}
	for g := 2; ; g++ {
		id := base + "~" + strconv.Itoa(g)
		gone, ok := gens[id]
		switch {
		case ok && !gone:
			return id, nil
		case !ok && renew:
			return id, nil
		case !ok:
			return "", nil
		}
	}
}

// RenewSubjectTx is the subject a person is sealed under now, making its
// key: their own, or, once that was erased and they came back (a customer
// who writes again), the next generation of it, <subject>~2, ~3...
// Erasure is one way (a shredded key is never restored); a person who
// returns gets a new key, and erasing them again (Erase, by their
// subject) shreds every generation. Only what names the person afresh
// calls it: the customer guard for a sender, and a run started by their
// message (runtime setCustomer). SealTx never moves to a new generation,
// so plaintext read before an erasure is never sealed readable again.
func (v *Vault) RenewSubjectTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (string, error) {
	base, err := v.subjectID(ctx, tx, tenant, category, value)
	if err != nil {
		return "", err
	}
	subject, err := liveGeneration(ctx, tx, tenant, base, true)
	if err != nil {
		return "", err
	}
	if _, err := v.subjectKey(ctx, tx, tenant, subject, category, true); err != nil {
		return "", err
	}
	return subject, nil
}

// LiveSubjectTx is the subject a person is sealed under now, without
// making anything: their own, or the generation RenewSubjectTx made when
// they came back after an erasure. A person erased who has not come back
// is their own (erased) subject. Lookups of what is kept by a person's
// current subject (the customer guard's contacts) use it.
func (v *Vault) LiveSubjectTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (string, error) {
	base, err := v.subjectID(ctx, tx, tenant, category, value)
	if err != nil {
		return "", err
	}
	subject, err := liveGeneration(ctx, tx, tenant, base, false)
	if err != nil || subject == "" {
		return base, err
	}
	return subject, nil
}

// SealCurrentTx is SealTx for a value just received from outside (a
// buffered signal): under the person's generation RenewSubjectTx made when
// they came back after an erasure, if there is one; it never makes a
// generation. A person erased who has not come back is pii.ErrErased.
func (v *Vault) SealCurrentTx(ctx context.Context, tx pgx.Tx, t uuid.UUID, category string, value any) (map[string]any, error) {
	base, err := v.subjectID(ctx, tx, t, category, value)
	if err != nil {
		return nil, err
	}
	subject, err := liveGeneration(ctx, tx, t, base, false)
	if err != nil {
		return nil, err
	}
	if subject == "" {
		return nil, pii.ErrErased
	}
	if subject == base {
		return v.SealTx(ctx, tx, t, category, value)
	}
	return v.SealUnderTx(ctx, tx, t, subject, category, value)
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

// SealTx encrypts one personal value under its subject's key. A subject
// erased stays erased (pii.ErrErased): a person who comes back is sealed
// under a new generation only once something names them afresh
// (RenewSubjectTx).
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

// SealUnderTx encrypts a value under an existing subject's key, so it is
// erased with that subject: a message queued for a person, sealed under
// the subject of their phone number or email.
func (v *Vault) SealUnderTx(ctx context.Context, tx pgx.Tx, t uuid.UUID, subject, category string, value any) (map[string]any, error) {
	dek, err := v.subjectKey(ctx, tx, t, subject, category, false)
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
	if len(ct) == 0 {
		return nil, pii.ErrErased // a tombstone: sealed when its subject was erased already
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

// SubjectForTx is the subject a value is sealed under by SealTx, in the
// caller's transaction, without sealing anything. It is the value's own
// subject, erased or not; LiveSubjectTx and RenewSubjectTx follow a
// person who came back after an erasure.
func (v *Vault) SubjectForTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (string, error) {
	return v.subjectID(ctx, tx, tenant, category, value)
}

// Erase destroys a subject's key (NDPA erasure, spec 9.4), and the keys of
// its later generations (a person erased who came back). It is one-way.
// A generation named (<subject>~2) erases the person: its base and every
// generation.
func (v *Vault) Erase(ctx context.Context, tenant uuid.UUID, subject, by string) error {
	subject = pii.BaseSubject(subject)
	return db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE subject_keys SET wrapped_key = NULL, shredded_at = now()
			WHERE tenant_id = $1 AND subject_id = $2 AND shredded_at IS NULL`, tenant, subject)
		if err != nil {
			return err
		}
		n := tag.RowsAffected()
		// Generations exist only once the base was shredded (this call's
		// or an earlier one's), so they are looked for only then.
		var shredded bool
		err = tx.QueryRow(ctx, `SELECT shredded_at IS NOT NULL FROM subject_keys WHERE tenant_id = $1 AND subject_id = $2`, tenant, subject).Scan(&shredded)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if shredded {
			tag, err := tx.Exec(ctx, `UPDATE subject_keys SET wrapped_key = NULL, shredded_at = now()
				WHERE tenant_id = $1 AND `+generationsSQL+` AND shredded_at IS NULL`, tenant, subject)
			if err != nil {
				return err
			}
			n += tag.RowsAffected()
		}
		if n == 0 {
			return ErrNotFound
		}
		return audit(ctx, tx, tenant, by, "pii.erase", subject, map[string]any{})
	})
}

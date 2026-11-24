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

// baseSubjectID derives a stable pseudonymous id for a data subject from
// the value, keyed by the tenant's pseudonymisation key (spec 5.2:
// hmac(tenant key, bvn)). That key started as tenant key version 1's
// material and is kept apart from it since migration 00100, so ids never
// change while tenant keys rotate.
func (v *Vault) baseSubjectID(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (string, error) {
	if !validCategory(category) {
		return "", fmt.Errorf("personal data category %q: want lower-case letters, digits and underscores", category)
	}
	key, err := v.pseudonymKey(ctx, tx, tenant)
	if err != nil {
		return "", err
	}
	if category == "other" {
		// Free text is no one's: one subject a tenant, not one a text
		// (pii.Unattributed). Envelopes sealed under a text's own subject
		// before keep it.
		value = pii.Unattributed
	}
	return subjectIDWith(key, category, value), nil
}

// maxGenerations bounds how often one value's subject is erased and
// started again.
const maxGenerations = 1000

// generation is a value's subject after it was erased g times: an erased
// person who comes back is a new subject (base~1, base~2, ...), so what
// was sealed for them before stays erased and they can be sealed again.
func generation(base string, g int) string {
	if g == 0 {
		return base
	}
	return base + "~" + strconv.Itoa(g)
}

// subjectID is the subject a value is sealed under now: the first
// generation of its base id whose key is not shredded (or does not exist
// yet).
func (v *Vault) subjectID(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (string, error) {
	base, err := v.baseSubjectID(ctx, tx, tenant, category, value)
	if err != nil {
		return "", err
	}
	for g := range maxGenerations {
		subject := generation(base, g)
		var shredded bool
		err := tx.QueryRow(ctx, `SELECT shredded_at IS NOT NULL FROM subject_keys WHERE tenant_id = $1 AND subject_id = $2`, tenant, subject).Scan(&shredded)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !shredded) {
			return subject, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", pii.ErrErased
}

// A category is lower-case letters, digits and underscores, so no id is
// another's generation (<subject>~1) or contains its prefix.
var categoryRE = regexp.MustCompile(`^[a-z0-9_]+$`)

func validCategory(c string) bool { return categoryRE.MatchString(c) }

// generationsSQL matches a subject's later generations (<base>~1, ~2...)
// by a range that subject_keys_generations (a text_pattern_ops index)
// serves: every id that starts with "<base>~" and nothing else, since '~'
// is the last printable ASCII character before U+007F and ids are ASCII.
// $2 is the base.
const generationsSQL = `subject_id ~>=~ ($2 || '~') AND subject_id ~<~ ($2 || chr(127))`

// RenewSubjectTx is the subject a person is sealed under now, making its
// key: their own, or, once that was erased and they came back (a customer
// who writes again), the next generation of it. The customer guard calls
// it for a sender, and a run started by their message (runtime
// setCustomer), so the run's key exists before anything is sealed.
func (v *Vault) RenewSubjectTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (string, error) {
	subject, err := v.subjectID(ctx, tx, tenant, category, value)
	if err != nil {
		return "", err
	}
	if _, err := v.subjectKey(ctx, tx, tenant, subject, category, true); err != nil {
		return "", err
	}
	return subject, nil
}

// LiveSubjectTx is the subject a person is sealed under now, without
// making anything. Lookups of what is kept by a person's current subject
// (the customer guard's contacts, a customer's invoices) use it: a person
// erased who has not come back has a subject nothing is kept under yet.
func (v *Vault) LiveSubjectTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (string, error) {
	return v.subjectID(ctx, tx, tenant, category, value)
}

// SealCurrentTx is SealTx for a value just received from outside (a
// buffered signal): under the person's current generation.
func (v *Vault) SealCurrentTx(ctx context.Context, tx pgx.Tx, t uuid.UUID, category string, value any) (map[string]any, error) {
	return v.SealTx(ctx, tx, t, category, value)
}

// subjectIDWith keys the subject by the value's canonical form
// (pii.Canonical), so the same person written another way ('0803 123
// 4567', '+2348031234567') is one subject. Values already canonical keep
// the ids they had.
func subjectIDWith(key []byte, category string, value any) string {
	return rawSubjectIDWith(key, category, pii.Canonical(category, value))
}

// rawSubjectIDWith is the subject of the value exactly as given, as ids
// were keyed before they were canonical.
func rawSubjectIDWith(key []byte, category string, value any) string {
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
// A value whose subject was erased is sealed under its next generation.
func (v *Vault) SealTx(ctx context.Context, tx pgx.Tx, t uuid.UUID, category string, value any) (map[string]any, error) {
	base, err := v.baseSubjectID(ctx, tx, t, category, value)
	if err != nil {
		return nil, err
	}
	var subject string
	var dek []byte
	for g := 0; ; g++ {
		subject = generation(base, g)
		dek, err = v.subjectKey(ctx, tx, t, subject, category, true)
		if !errors.Is(err, pii.ErrErased) || g+1 >= maxGenerations {
			break
		}
		// Shredded: the person was erased and came back.
	}
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

// SubjectFor returns the subject id a value would be sealed under: its
// current generation, so erasing by value erases the person as they are
// now (earlier generations were erased already).
func (v *Vault) SubjectFor(ctx context.Context, tenant uuid.UUID, category string, value any) (string, error) {
	var s string
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		s, err = v.subjectID(ctx, tx, tenant, category, value)
		return err
	})
	return s, err
}

// SubjectsFor lists every subject a value's person may be sealed under:
// the one it is sealed under now (SubjectFor), then those of the ways it
// was commonly written (pii.Formats) that still hold a key, from before
// subject ids were canonical. Erasing a person by value erases them all.
func (v *Vault) SubjectsFor(ctx context.Context, tenant uuid.UUID, category string, value any) ([]string, error) {
	var out []string
	err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		cur, err := v.subjectID(ctx, tx, tenant, category, value)
		if err != nil {
			return err
		}
		out = append(out, cur)
		key, err := v.pseudonymKey(ctx, tx, tenant)
		if err != nil {
			return err
		}
		seen := map[string]bool{subjectIDWith(key, category, value): true}
		for _, f := range pii.Formats(category, value) {
			raw := rawSubjectIDWith(key, category, f)
			if seen[raw] {
				continue
			}
			seen[raw] = true
			var live bool
			err := tx.QueryRow(ctx, `SELECT true FROM subject_keys WHERE tenant_id = $1 AND subject_id = $2 AND shredded_at IS NULL`, tenant, raw).Scan(&live)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			out = append(out, raw)
		}
		return nil
	})
	return out, err
}

// SubjectForTx is SubjectFor in the caller's transaction: the subject a
// value would be sealed under, without sealing anything.
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

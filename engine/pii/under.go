package pii

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// UnderCategories are the categories a run started by a customer's
// message seals under that customer (Under): what they wrote and said,
// names, and what was made from them. Identifiers (phone, email, BVN...)
// keep their own subject, so erasing whoever they name still reaches them.
var UnderCategories = map[string]bool{"other": true, "name": true}

// BaseSubject is a subject id without its generation (<subject>~2 is the
// second key of a person erased who came back): the person erasure names.
func BaseSubject(subject string) string {
	i := strings.LastIndexByte(subject, '~')
	if i < 0 || i == len(subject)-1 {
		return subject
	}
	for _, r := range subject[i+1:] {
		if r < '0' || r > '9' {
			return subject
		}
	}
	return subject[:i]
}

// Tombstone is the envelope a value gets when the person it would be
// sealed under is erased already: it holds nothing (no ciphertext), names
// the erased subject, and opens as Erased. A run that meets an erased
// person's data carries on without keeping it.
func Tombstone(category, subject string) map[string]any {
	return map[string]any{"$pii": category, "subject": subject, "ct": ""}
}

// IsTombstone reports whether an envelope is a Tombstone.
func IsTombstone(env map[string]any) bool {
	ct, ok := env["ct"].(string)
	return ok && ct == ""
}

// Under is a cipher that seals free text and names under one person's
// subject as well as their own (decision 0062 P2): in a run started by a
// customer's message, their words, the transcript of their voice note and
// the replies made from them are all erased with that customer, wherever
// they were copied in the run's history or archive. Each such value is an
// envelope under its own subject inside one under the customer, so
// erasing either erases it: another person's name or chat id a run of
// this customer holds is still erased with that person. The customer's own
// identifier is sealed under subject (the generation they are on now).
//
// A value whose person, or the customer, is erased already is sealed as a
// Tombstone instead of failing: a run still open when its customer was
// erased (a message they send later wakes it, its timers fire) goes on
// without keeping what it is given. c must be able to seal under a
// subject; otherwise it is returned as it is.
func Under(c Cipher, subject string) Cipher {
	ss, ok := c.(SubjectSealer)
	if !ok || subject == "" {
		return c
	}
	return &under{c: c, ss: ss, subject: subject, base: BaseSubject(subject)}
}

type under struct {
	c       Cipher
	ss      SubjectSealer
	subject string // the customer's subject, a generation of base
	base    string
}

func (u *under) SealTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (map[string]any, error) {
	own, err := u.ss.SubjectForTx(ctx, tx, tenant, category, value)
	if err != nil {
		return nil, err
	}
	if own == u.base {
		// The customer themselves: under the key they hold now.
		return u.sealUnder(ctx, tx, tenant, u.subject, category, value)
	}
	inner, err := u.c.SealTx(ctx, tx, tenant, category, value)
	if errors.Is(err, ErrErased) {
		return Tombstone(category, own), nil
	}
	if err != nil || !UnderCategories[category] {
		return inner, err
	}
	return u.sealUnder(ctx, tx, tenant, u.subject, category, inner)
}

// sealUnder seals under a subject, or makes a Tombstone when it is erased.
func (u *under) sealUnder(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject, category string, value any) (map[string]any, error) {
	env, err := u.ss.SealUnderTx(ctx, tx, tenant, subject, category, value)
	if errors.Is(err, ErrErased) {
		return Tombstone(category, subject), nil
	}
	return env, err
}

func (u *under) OpenTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, envelope map[string]any) (any, error) {
	return u.c.OpenTx(ctx, tx, tenant, envelope)
}

// SubjectForTx is a value's own subject; the customer's own identifier is
// the generation they are on.
func (u *under) SubjectForTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (string, error) {
	own, err := u.ss.SubjectForTx(ctx, tx, tenant, category, value)
	if err == nil && own == u.base {
		return u.subject, nil
	}
	return own, err
}

func (u *under) SealUnderTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject, category string, value any) (map[string]any, error) {
	return u.ss.SealUnderTx(ctx, tx, tenant, subject, category, value)
}

// CurrentSealer seals a value just received from outside under its
// person's key now: the generation they were given when they came back
// after an erasure, if any (secrets.Vault.SealCurrentTx).
type CurrentSealer interface {
	SealCurrentTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (map[string]any, error)
}

// Received is the cipher for personal data just received from outside
// and kept for no run yet (a buffered signal), or for a run that only
// receives it (a chat message's run with no customer guard): each value
// is sealed under its person's current key (CurrentSealer), and the value
// of a person erased who has not come back is a Tombstone, so an inbound
// message from them is accepted without being kept.
func Received(c Cipher) Cipher {
	if c == nil {
		return nil
	}
	return &received{c: c, tolerant: true}
}

// Current is Received refusing (ErrErased) the value of a person erased
// who has not come back, rather than keeping it as erased: for input a
// person gave a run (an API start), which must not act on an erased value.
func Current(c Cipher) Cipher {
	if c == nil {
		return nil
	}
	return &received{c: c}
}

type received struct {
	c        Cipher
	tolerant bool
}

func (r *received) SealTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (map[string]any, error) {
	var env map[string]any
	var err error
	if cs, ok := r.c.(CurrentSealer); ok {
		env, err = cs.SealCurrentTx(ctx, tx, tenant, category, value)
	} else {
		env, err = r.c.SealTx(ctx, tx, tenant, category, value)
	}
	if !r.tolerant || !errors.Is(err, ErrErased) {
		return env, err
	}
	ss, ok := r.c.(SubjectSealer)
	if !ok {
		return nil, err
	}
	subject, serr := ss.SubjectForTx(ctx, tx, tenant, category, value)
	if serr != nil {
		return nil, serr
	}
	return Tombstone(category, subject), nil
}

func (r *received) OpenTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, envelope map[string]any) (any, error) {
	return r.c.OpenTx(ctx, tx, tenant, envelope)
}

// CustomerOf is the customer a run's cipher seals under (Under), or "".
func CustomerOf(c Cipher) string {
	if u, ok := c.(*under); ok {
		return u.subject
	}
	return ""
}

// SealUnderCustomer seals a record made in a customer's run (an AI step's
// call, already redacted of the values the run knows) under that customer
// alone, so it is erased with them; a Tombstone once they are. ok is false
// when c is not a customer's (Under).
func SealUnderCustomer(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, category string, value any) (env map[string]any, ok bool, err error) {
	u, isUnder := c.(*under)
	if !isUnder {
		return nil, false, nil
	}
	env, err = u.sealUnder(ctx, tx, tenant, u.subject, category, value)
	return env, true, err
}

package pii

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// UnderCategories are the categories a run started by a customer's
// message seals under that customer (Under): what they wrote and said,
// names, and what was made from them. Identifiers (phone, email, BVN...)
// keep their own subject, so erasing whoever they name still reaches them.
var UnderCategories = map[string]bool{"other": true, "name": true}

// Under is a cipher that seals free text and names under one person's
// subject (decision 0062 P2): in a run started by a customer's message,
// their words, the transcript of their voice note and the replies made
// from them are all erased with that customer, wherever they were copied
// in the run's history or archive. c must be able to seal under a subject;
// otherwise it is returned as it is.
func Under(c Cipher, subject string) Cipher {
	ss, ok := c.(SubjectSealer)
	if !ok || subject == "" {
		return c
	}
	return &under{c: c, ss: ss, subject: subject}
}

type under struct {
	c       Cipher
	ss      SubjectSealer
	subject string
}

func (u *under) SealTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (map[string]any, error) {
	if UnderCategories[category] {
		return u.ss.SealUnderTx(ctx, tx, tenant, u.subject, category, value)
	}
	return u.c.SealTx(ctx, tx, tenant, category, value)
}

func (u *under) OpenTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, envelope map[string]any) (any, error) {
	return u.c.OpenTx(ctx, tx, tenant, envelope)
}

func (u *under) SubjectForTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (string, error) {
	if UnderCategories[category] {
		return u.subject, nil
	}
	return u.ss.SubjectForTx(ctx, tx, tenant, category, value)
}

func (u *under) SealUnderTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject, category string, value any) (map[string]any, error) {
	return u.ss.SealUnderTx(ctx, tx, tenant, subject, category, value)
}

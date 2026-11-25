package publicweb

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/forms"
)

// Forms left half-filled (docs/spec/forms-that-sell.md §6, §10): the
// public host writes a keep to its inbox, sealed for the api role, and
// claims a follow-up's restore link once by its token's hash, or stops
// the follow-ups from it. It reads nothing of the records themselves.

// Keeps writes keeps and claims restore links.
type Keeps interface {
	// Keep writes a sealed keep for an order form; false when the form
	// does not follow up, or the page's order (sent, its status hash) is
	// sent already.
	Keep(ctx context.Context, action uuid.UUID, payload, sent []byte) (bool, error)
	// RestoreOK reports whether a restore link still restores, using
	// nothing up: what its page offers.
	RestoreOK(ctx context.Context, tokenHash []byte) (bool, error)
	// Restore claims a restore link once: its form or action and the
	// choice; ok false when it is unknown, used or past its time.
	Restore(ctx context.Context, tokenHash []byte) (Restored, bool, error)
	// Stop stops the follow-ups to the link's contact from its business;
	// false when the link is unknown or past its time.
	Stop(ctx context.Context, tokenHash []byte) (bool, error)
}

// Restored is what a restore link gives back: never the contact.
type Restored struct {
	Endpoint *uuid.UUID   `json:"endpoint_id"`
	Action   *uuid.UUID   `json:"action_id"`
	Choice   forms.Choice `json:"choice"`
}

// DBKeeps writes and claims through taskiem_public_form_keep,
// taskiem_public_form_restore_ok, taskiem_public_form_restore and
// taskiem_public_form_stop, on a pool connected as taskiem_public.
func DBKeeps(pool *pgxpool.Pool) Keeps { return dbKeeps{pool} }

type dbKeeps struct{ pool *pgxpool.Pool }

func (d dbKeeps) Keep(ctx context.Context, action uuid.UUID, payload, sent []byte) (bool, error) {
	var ok bool
	err := d.pool.QueryRow(ctx, `SELECT taskiem_public_form_keep($1, $2, $3)`, action, payload, sent).Scan(&ok)
	return ok, err
}

func (d dbKeeps) RestoreOK(ctx context.Context, tokenHash []byte) (bool, error) {
	var ok bool
	err := d.pool.QueryRow(ctx, `SELECT taskiem_public_form_restore_ok($1)`, tokenHash).Scan(&ok)
	return ok, err
}

func (d dbKeeps) Restore(ctx context.Context, tokenHash []byte) (Restored, bool, error) {
	var raw []byte
	err := d.pool.QueryRow(ctx, `SELECT taskiem_public_form_restore($1)`, tokenHash).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && raw == nil) {
		return Restored{}, false, nil
	}
	if err != nil {
		return Restored{}, false, err
	}
	var r Restored
	if err := json.Unmarshal(raw, &r); err != nil {
		return Restored{}, false, err
	}
	return r, true, nil
}

func (d dbKeeps) Stop(ctx context.Context, tokenHash []byte) (bool, error) {
	var ok bool
	err := d.pool.QueryRow(ctx, `SELECT taskiem_public_form_stop($1)`, tokenHash).Scan(&ok)
	return ok, err
}

// SealKeep seals a keep for the api role, bound to its form or action.
func SealKeep(keys *forms.Keys, scope uuid.UUID, k forms.Keep) ([]byte, error) {
	if err := k.Check(); err != nil {
		return nil, err
	}
	return keys.Seal(forms.KeepAAD(scope), k)
}

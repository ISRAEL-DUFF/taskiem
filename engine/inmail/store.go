package inmail

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBStore is Store over the mail role's own pool, connected as
// taskiem_public.
type DBStore struct{ Pool *pgxpool.Pool }

func (d DBStore) Recipient(ctx context.Context, publicID string) (uuid.UUID, bool, error) {
	var id *uuid.UUID
	if err := d.Pool.QueryRow(ctx, `SELECT taskiem_mail_recipient($1)`, publicID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	if id == nil {
		return uuid.Nil, false, nil
	}
	return *id, true, nil
}

func (d DBStore) Receive(ctx context.Context, id, endpoint uuid.UUID, idem, meta, raw []byte) (bool, error) {
	var ok bool
	err := d.Pool.QueryRow(ctx, `SELECT taskiem_mail_receive($1, $2, $3, $4, $5)`, id, endpoint, idem, meta, raw).Scan(&ok)
	return ok, err
}

func sealJSON(s Sealer, what string, v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return s.SealBytes(what, raw)
}

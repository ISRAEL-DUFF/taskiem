package audit

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	"github.com/jackc/pgx/v5"
)

// Export writes a tenant's chain as JSON lines, entries then the head, from
// a transaction scoped to that tenant. Only entries up to the head read
// first are written: appends are serialised on the head row, so they are
// all committed.
func Export(ctx context.Context, tx pgx.Tx, tenant string, w io.Writer) error {
	enc := json.NewEncoder(w)
	head := Head{TenantID: tenant, Hash: hex.EncodeToString(make([]byte, 32))}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT chain_seq, head_hash FROM audit_chain_heads WHERE tenant_id = $1`, tenant).Scan(&head.Seq, &raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if raw != nil {
		head.Hash = hex.EncodeToString(raw)
	}
	rows, err := tx.Query(ctx, `SELECT tenant_id::text, chain_seq, actor_type, actor_id, action, target, detail::text,
			to_char(at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'), encode(prev_hash, 'hex'), encode(hash, 'hex')
		FROM audit_log WHERE tenant_id = $1 AND chain_seq <= $2 ORDER BY chain_seq`, tenant, head.Seq)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.TenantID, &e.Seq, &e.ActorType, &e.ActorID, &e.Action, &e.Target, &e.Detail, &e.At, &e.PrevHash, &e.Hash); err != nil {
			return err
		}
		if err := enc.Encode(Line{Entry: &e}); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return enc.Encode(Line{Head: &head})
}

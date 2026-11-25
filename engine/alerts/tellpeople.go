package alerts

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/notices"
)

// TellPeople tells some of a business's people, not its owners as such:
// the alert is recorded once per kind and dedup key (as Notify does),
// given to each of users who is an active member in the app's bell, and
// queued to each on the chat they linked, WhatsApp before Telegram, as
// their settings for setup problems say. Alert channels are not told. It
// answers who it reached on a chat; nothing (and false) when the alert was
// told already. tx must have the tenant in scope.
func TellPeople(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, users []uuid.UUID, al Alert) (map[uuid.UUID]bool, bool, error) {
	ids := make([]string, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.String())
	}
	members, err := membersTx(ctx, tx, tenant, ids)
	if err != nil || len(members) == 0 {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('alerts.notify ' || $1::text || ' ' || $2 || ' ' || $3, 0))`,
		tenant, al.Kind, al.Dedup); err != nil {
		return nil, false, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM alerts WHERE tenant_id = $1 AND rule_id IS NULL AND kind = $2 AND dedup_key = $3)`,
		tenant, al.Kind, al.Dedup).Scan(&exists); err != nil || exists {
		return nil, false, err
	}
	id := uuid.Must(uuid.NewV7())
	if err := insertAlert(ctx, tx, tenant, id, al); err != nil {
		return nil, false, err
	}
	if err := notices.AddTx(ctx, tx, tenant, members, setupNotice(al, id)); err != nil {
		return nil, false, err
	}
	chat, _, err := notices.Wants(ctx, tx, members, notices.GroupSetup)
	if err != nil {
		return nil, false, err
	}
	told, err := queueChat(ctx, tx, tenant, id, chat)
	return told, true, err
}

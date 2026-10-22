package alerts

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/notices"
)

// DomainUnverified is the kind of the alert a business gets when its own
// domain or a website that embeds it stops proving it is theirs and
// Taskiem stops serving it (no-technical-steps N9).
const DomainUnverified = "domain_unverified"

// TellOwners records an alert as Notify does (once per kind and dedup key,
// to every enabled alert channel, and in each owner's notifications) and
// also queues it to each owner on the chat they linked, WhatsApp before
// Telegram, unless they turned chat off for setup problems (Settings >
// Notifications). tx must have the tenant in scope.
func TellOwners(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, al Alert) (bool, error) {
	if ok, err := Notify(ctx, tx, tenant, al); err != nil || !ok {
		return ok, err
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM alerts WHERE tenant_id = $1 AND rule_id IS NULL AND kind = $2 AND dedup_key = $3`, tenant, al.Kind, al.Dedup).Scan(&id); err != nil {
		return true, err
	}
	owners, err := ownersTx(ctx, tx, tenant)
	if err != nil {
		return true, err
	}
	chat, _, err := notices.Wants(ctx, tx, owners, notices.GroupSetup)
	if err != nil {
		return true, err
	}
	_, err = queueChat(ctx, tx, tenant, id, chat)
	return true, err
}

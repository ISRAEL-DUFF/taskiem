package alerts

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// DomainUnverified is the kind of the alert a business gets when its own
// domain or a website that embeds it stops proving it is theirs and
// Taskiem stops serving it (no-technical-steps N9).
const DomainUnverified = "domain_unverified"

// TellOwners records an alert as Notify does (once per kind and dedup key,
// to every enabled alert channel) and also queues it to each of the
// business's owners on the chat they linked, WhatsApp before Telegram, as
// the alert itself is rendered when sent. tx must have the tenant in scope.
func TellOwners(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, al Alert) (bool, error) {
	if ok, err := Notify(ctx, tx, tenant, al); err != nil || !ok {
		return ok, err
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM alerts WHERE tenant_id = $1 AND rule_id IS NULL AND kind = $2 AND dedup_key = $3`, tenant, al.Kind, al.Dedup).Scan(&id); err != nil {
		return true, err
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT ms.user_id FROM memberships ms JOIN users u ON u.id = ms.user_id
		WHERE ms.tenant_id = $1 AND ms.role = 'owner' AND u.status = 'active'`, tenant)
	if err != nil {
		return true, err
	}
	owners, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return true, err
	}
	told := map[uuid.UUID]bool{}
	for _, ch := range []struct{ name, q string }{
		{"whatsapp", `SELECT user_id FROM taskiem_wa_numbers($1)`},
		{"telegram", `SELECT user_id FROM taskiem_tg_accounts($1)`},
	} {
		rows, err := tx.Query(ctx, ch.q, owners)
		if err != nil {
			return true, err
		}
		linked, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return true, err
		}
		for _, u := range linked {
			if told[u] {
				continue
			}
			told[u] = true
			it := whatsapp.OutboxItem{Tenant: tenant, Channel: ch.name, Kind: whatsapp.OutboxAlert, User: u, Alert: &id}
			key := "alert:" + id.String() + ":" + u.String()
			if ch.name != "whatsapp" {
				key = ch.name + ":" + key
			}
			if _, err := whatsapp.Enqueue(ctx, tx, it, key); err != nil {
				return true, err
			}
		}
	}
	return true, nil
}

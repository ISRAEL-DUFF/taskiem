package riders

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/pii"
)

// A rider that is a business on Taskiem (docs/spec/chat-riders.md, phase
// 2): the business that adds them by phone is linked with their business,
// and a rider whose phone is a business we are linked with is named as
// that business, so their deliveries go through the link (phase 1).

// PhoneIsBusinessTx says whether a phone number is another active business
// on Taskiem (its owner's WhatsApp, or its own WhatsApp number). Which one
// is not told: a link request to the number says, when it answers.
func PhoneIsBusinessTx(ctx context.Context, tx pgx.Tx, phone string) (bool, error) {
	var is bool
	err := tx.QueryRow(ctx, `SELECT taskiem_link_phone_is_business($1)`, phone).Scan(&is)
	return is, err
}

// ClaimLinkedTx names, on each current rider not yet named as a business,
// the active link with the business their phone belongs to, when there is
// one: a rider added before their business was on Taskiem, or linked with
// after being added. It answers how many riders it named.
func (r *Riders) ClaimLinkedTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (int, error) {
	list, err := r.ListTx(ctx, tx, tenant, Filter{})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, x := range list {
		if x.LinkID != nil || x.Phone == pii.Erased || x.Phone == "" {
			continue
		}
		var link *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT taskiem_link_phone_linked($1)`, x.Phone).Scan(&link); err != nil {
			return n, err
		}
		if link == nil {
			continue
		}
		// Not when another current rider is already that business (a
		// failed statement would end the transaction).
		tag, err := tx.Exec(ctx, `UPDATE riders SET link_id = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2 AND link_id IS NULL AND retired_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM riders o WHERE o.tenant_id = $1 AND o.link_id = $3 AND o.retired_at IS NULL)`, tenant, x.ID, *link)
		if err != nil {
			return n, err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := audit(ctx, tx, tenant, "rider.linked", x.ID, map[string]any{"link": *link, "by": "system:links"}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

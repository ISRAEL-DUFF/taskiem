package storefront

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// Classes (docs/spec/fronts.md §6, F6): a name on a full class's waiting
// list holds no place. When one frees (an order cancelled or refunded,
// a payment never made), the owner offers it: OfferPlace takes the place
// under the list's lock, as the public host's claim does, and the order
// becomes an accepted one paid after acceptance, so the workflow can send
// its payment link.

// OfferPlace gives a waiting-list order its places, if they are free.
func (o *Orders) OfferPlace(ctx context.Context, tenant uuid.UUID, reference, actor string) (Order, error) {
	var ord Order
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if ord, err = scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND reference = $2 FOR UPDATE`, tenant, reference)); err != nil {
			return err
		}
		if ord.Kind != "class" || !ord.Waitlist {
			return fmt.Errorf("%w: %s is not on a waiting list", ErrDecision, reference)
		}
		if ord.Status != "received" {
			return fmt.Errorf("%w: %s is %s", ErrDecision, reference, ord.Status)
		}
		qty := 0
		for _, l := range ord.Basket {
			qty += l.Quantity
		}
		var blk uuid.UUID
		var have, used int
		if err := tx.QueryRow(ctx, `SELECT b.id FROM storefront_blocks b WHERE b.front_id = $1 AND b.kind = 'list' ORDER BY b.position LIMIT 1`, ord.FrontID).Scan(&blk); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('storefront-claims:' || $1::text, 0))`, blk); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE((c.item->>'capacity')::int, 0) FROM storefront_catalogue c JOIN storefront_blocks b ON b.id = $3
			WHERE c.storefront_id = $1 AND c.table_name = b.config->>'table_name' AND c.row_id = $2`, ord.storefront, ord.ServiceID, blk).Scan(&have); err != nil {
			return fmt.Errorf("%w: %s is no longer on the list", ErrDecision, ord.ServiceName)
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(qty), 0)::int FROM storefront_claims WHERE block_id = $1 AND item_id = $2
			AND (expires_at IS NULL OR expires_at > now())`, blk, ord.ServiceID).Scan(&used); err != nil {
			return err
		}
		if have-used < qty {
			return fmt.Errorf("%w: %s is still full (%d of %d places taken)", ErrDecision, ord.ServiceName, used, have)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO storefront_claims (reference, storefront_id, tenant_id, item_id, qty, block_id, starts_at, until_at)
			VALUES ($1, $2, $3, $4, $5, $6, '-infinity', 'infinity')`, reference, ord.storefront, tenant, ord.ServiceID, qty, blk); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET waitlist = false, payment = 'after_accept', status = 'accepted', decided_by = $3, decided_at = now(),
			updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, ord.ID, actor); err != nil {
			return err
		}
		if err := audit(ctx, tx, tenant, "storefront.place_offered", reference, map[string]any{"by": actor, "places": qty}); err != nil {
			return err
		}
		ord, err = o.GetOrder(ctx, tx, tenant, reference)
		return err
	})
	return ord, err
}

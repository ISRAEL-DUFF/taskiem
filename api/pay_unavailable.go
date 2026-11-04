package api

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
)

// InvoicePayUnavailable tells a business's people who send invoices that a
// customer asked to pay online and no payments connection could do it, so
// the customer was asked to pay them directly (invoicing.Invoices.
// PayUnavailable). Once for each invoice, order or customer page and way of
// paying: what they must do is connect a payments service.
func (s *Server) InvoicePayUnavailable(ctx context.Context, q invoicing.Request, cause error) {
	s.background(ctx, "payment unavailable", func(ctx context.Context) {
		what, group := "what they owe you", "pay-unavailable:link:"+q.Kind
		if q.Link != nil {
			group += ":" + q.Link.String()
		}
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{q.Tenant}, func(tx pgx.Tx) error {
			switch {
			case q.Order != nil:
				var ref string
				if err := tx.QueryRow(ctx, `SELECT reference FROM storefront_orders WHERE tenant_id = $1 AND id = $2`, q.Tenant, *q.Order).Scan(&ref); err != nil {
					return err
				}
				what, group = "order "+ref, "pay-unavailable:order:"+ref+":"+q.Kind
			case q.Link == nil:
				var number string
				if err := tx.QueryRow(ctx, `SELECT number FROM customer_invoices WHERE tenant_id = $1 AND id = $2`, q.Tenant, q.Invoice).Scan(&number); err != nil {
					return err
				}
				what, group = "invoice "+number, "pay-unavailable:invoice:"+number+":"+q.Kind
			}
			return nil
		})
		if err != nil {
			s.Logger.Warn("payment unavailable: reading what was asked for", "tenant", q.Tenant, "err", err)
			return
		}
		how := map[string]string{"transfer": "bank transfer", "link": "card"}[q.Kind]
		s.tellHolders(ctx, q.Tenant, []string{PermInvoiceManage}, group, "/connections", func(l lang.Tag) (string, string) {
			return tr(l, "invoice.pay_unavailable_title"), tr(l, "invoice.pay_unavailable", "what", what, "how", how)
		})
	})
}

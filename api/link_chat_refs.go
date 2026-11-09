package api

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/links"
)

// References in a business's conversation (docs/spec/chat-five-more-flows.md,
// phase 2 additions): "/invoice" and "/order" in the composer offer what is
// between the two businesses only, never another customer's. Invoices are
// ours to them and the bills they sent us; orders are those they placed
// with us and those we placed with them. A message's references are checked
// against the same list (checkMentions).

// linkRef is one thing a message in the conversation may name.
type linkRef struct {
	Kind      string `json:"kind"` // invoice or order
	ID        string `json:"id"`
	Name      string `json:"name"` // its number or reference
	TotalKobo int64  `json:"total_kobo"`
	Status    string `json:"status"`
	// Side: ours (we sent it, or they ordered from us) or theirs.
	Side string `json:"side"`
}

// linkRefsTx is what the conversation with link may name, newest first,
// of what the person may see: invoices and bills with invoice.read, orders
// placed with us with orders, our own orders with table.read.
func linkRefsTx(ctx context.Context, tx pgx.Tx, p *Principal, l links.Link) ([]linkRef, error) {
	tenant := p.TenantID
	q := func(kind, side, sql string, args ...any) ([]linkRef, error) {
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (linkRef, error) {
			x := linkRef{Kind: kind, Side: side}
			var id uuid.UUID
			err := r.Scan(&id, &x.Name, &x.TotalKobo, &x.Status)
			x.ID = id.String()
			return x, err
		})
	}
	var out []linkRef
	for _, s := range []struct {
		kind, side, sql string
		args            []any
		may             bool
	}{
		{"invoice", "ours", `SELECT id, number, total_kobo, status FROM customer_invoices
			WHERE tenant_id = $1 AND customer_key = $2 AND kind = 'invoice' AND status <> 'draft'
			ORDER BY created_at DESC LIMIT 100`, []any{tenant, links.KeyPrefix + l.Business.String()}, p.Can(PermInvoiceRead)},
		{"invoice", "theirs", `SELECT id, reference, amount_kobo, status FROM bills
			WHERE tenant_id = $1 AND link_id = $2 ORDER BY created_at DESC LIMIT 100`, []any{tenant, l.ID}, p.Can(PermInvoiceRead)},
		{"order", "ours", `SELECT o.id, o.reference, o.total_kobo, o.status FROM storefront_orders o JOIN link_orders x ON x.order_id = o.id
			WHERE x.tenant_id = $1 AND x.link_id = $2 ORDER BY o.created_at DESC LIMIT 100`, []any{tenant, l.ID}, p.Can(PermOrders)},
		{"order", "theirs", `SELECT o.id, o.reference, o.total_kobo, o.status FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id
			WHERE o.tenant_id = $1 AND s.link_id = $2 AND o.status <> 'drafted' ORDER BY o.created_at DESC LIMIT 100`, []any{tenant, l.ID}, p.Can(PermTableRead)},
	} {
		if !s.may {
			continue
		}
		got, err := q(s.kind, s.side, s.sql, s.args...)
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	if out == nil {
		out = []linkRef{}
	}
	return out, nil
}

// linkChatRefs is GET /v1/link-chats/{link}/refs.
func (s *Server) linkChatRefs(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out []linkRef
	if err := s.tx(r, func(tx pgx.Tx) error {
		l, err := links.GetTx(r.Context(), tx, link)
		if err != nil {
			return err
		}
		out, err = linkRefsTx(r.Context(), tx, p, l)
		return err
	}); err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"refs": out})
}

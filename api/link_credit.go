package api

// Statements, credit terms and limits, and reminders between linked
// businesses (business links L5, §10; engine/links/credit.go).

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// InvoiceDueFor is the due date an invoice to a linked business takes
// from the seller's terms for the link, when it names none
// (invoicing.Invoices.DueFor).
func (s *Server) InvoiceDueFor(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, key string) (string, error) {
	id, ok := strings.CutPrefix(key, links.KeyPrefix)
	if !ok {
		return "", nil
	}
	buyer, err := uuid.Parse(id)
	if err != nil {
		return "", nil //nolint:nilerr // not a business's key: no terms
	}
	return links.TermsDueOn(ctx, tx, tenant, buyer)
}

// InvoiceReminded tells a linked buyer a message its supplier sent about
// an invoice (a reminder): a card on the bill in both conversations, and
// the buyer's people who pay bills told in chat and the bell, with the
// bill to pay (invoicing.Invoices.Reminded).
func (s *Server) InvoiceReminded(ctx context.Context, seller uuid.UUID, x invoicing.Invoice, text string) {
	id, ok := strings.CutPrefix(x.Customer.Key, links.KeyPrefix)
	if !ok {
		return
	}
	buyer, err := uuid.Parse(id)
	if err != nil {
		return
	}
	s.background(ctx, "link reminder", func(ctx context.Context) {
		var link links.Link
		var from string
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{seller}, func(tx pgx.Tx) error {
			var err error
			if link, err = links.LinkWith(ctx, tx, buyer); err != nil {
				return err
			}
			return tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, seller).Scan(&from)
		}); err != nil {
			s.Logger.Warn("links: a reminder to a linked business", "tenant", seller, "err", err)
			return
		}
		due := x.DueKobo
		card := s.billCards(ctx, link.ID, seller, buyer, x.Number, "reminder", true, &due, text)
		s.tellHoldersSent(ctx, buyer, []string{PermInvoiceManage}, "reminder:"+x.Number, "/bills/"+x.Number, func(l lang.Tag) (string, string) {
			return tr(l, "bill.reminder_title", "from", from, "reference", x.Number),
				tr(l, "bill.reminder", "from", whatsapp.SafeText(from), "reference", x.Number, "amount", invoicing.Naira(due), "note", whatsapp.SafeText(text))
		}, nil, s.keepSent(ctx, buyer, link.ID, card))
	})
}

// creditCheck checks a new order from a linked buyer against the limit
// the seller set for the link: over it, the order is flagged (and not
// accepted by itself), the buyer's purchase order says why, and the
// buyer's people are told, with the bills to pay. It reports whether the
// order is over.
func (s *Server) creditCheck(ctx context.Context, seller uuid.UUID, ref string) (links.Credit, *links.Order) {
	var o links.Order
	var c links.Credit
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{seller}, func(tx pgx.Tx) error {
		var err error
		o, c, err = links.CreditCheckTx(ctx, tx, seller, ref)
		return err
	}); err != nil {
		s.Logger.Warn("links: checking an order's credit", "tenant", seller, "err", err)
		return links.Credit{}, nil
	}
	if !c.Changed {
		return c, &o
	}
	s.creditTold(ctx, seller, o, c)
	return c, &o
}

// creditTold tells both sides that an order went over the limit, or is
// within it again.
func (s *Server) creditTold(ctx context.Context, seller uuid.UUID, o links.Order, c links.Credit) {
	var name string
	_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{seller}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, seller).Scan(&name)
	})
	l := lang.EN
	note := ""
	if c.Over {
		note = tr(l, "order.link.over_limit_note", "business", name, "limit", invoicing.Naira(c.LimitKobo), "owed", invoicing.Naira(c.BalanceKobo))
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{seller}, func(tx pgx.Tx) error {
		return links.HeldNoteTx(ctx, tx, o, note)
	}); err != nil {
		s.Logger.Warn("links: noting an order's credit", "tenant", seller, "err", err)
	}
	key := "credit:" + o.ID.String() + ":" + map[bool]string{true: "over", false: "within"}[c.Over]
	s.tellHoldersSent(ctx, o.Buyer, []string{PermInvoiceManage}, key, "/bills", func(l lang.Tag) (string, string) {
		if c.Over {
			return tr(l, "order.link.over_limit_title", "reference", o.Reference),
				tr(l, "order.link.over_limit", "reference", o.Reference, "business", whatsapp.SafeText(name), "limit", invoicing.Naira(c.LimitKobo),
					"owed", invoicing.Naira(c.BalanceKobo))
		}
		return tr(l, "order.link.within_limit_title", "reference", o.Reference),
			tr(l, "order.link.within_limit", "reference", o.Reference, "business", whatsapp.SafeText(name))
	}, nil, nil)
	if !c.Over {
		return
	}
	s.tellHoldersSent(ctx, seller, []string{PermStorefrontManage}, key, "/trade-orders/"+o.OrderReference, func(l lang.Tag) (string, string) {
		return tr(l, "order.link.over_limit_seller_title", "reference", o.Reference, "buyer", o.BuyerName),
			tr(l, "order.link.over_limit_seller", "reference", o.Reference, "buyer", whatsapp.SafeText(o.BuyerName), "limit", invoicing.Naira(c.LimitKobo),
				"owed", invoicing.Naira(c.BalanceKobo))
	}, nil, nil)
}

// creditAgain checks an order over the limit again (the worker, hourly):
// within it now, the flag clears, both sides are told, and an order the
// seller accepts by itself is accepted.
func (s *Server) creditAgain(ctx context.Context, seller, id uuid.UUID) error {
	c, o := s.creditCheck(ctx, seller, id.String())
	if o == nil || c.Over || !c.Changed {
		return nil
	}
	if o.AutoAccept && o.Status == links.OrderReceived && allPriced(o.Lines) {
		if _, _, err := s.sellerStep(ctx, seller, o.ID.String(), links.StepAccept, links.StepIn{}, links.Actor{Type: "system", ID: "link-orders"},
			"system:link-orders", nil); err != nil {
			s.Logger.Warn("links: accepting an order within the limit", "tenant", seller, "err", err)
		}
	}
	return nil
}

// linkStatement is GET /v1/links/{id}/statement: the link's statement for
// the business: what they owe us (our invoices to them) and what we owe
// them (their bills), each with the terms and limit and the open
// documents, oldest due first. Both sides add up to the same numbers.
func (s *Server) linkStatement(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out links.Statement
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = links.StatementTx(r.Context(), tx, p.TenantID, id)
		return err
	}); err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// billsDueDailyLine is the morning summary's line on the bills linked
// suppliers sent: those due within three days or overdue.
func (s *Server) billsDueDailyLine(ctx context.Context, l lang.Tag, tenant uuid.UUID) string {
	var n, overdue int
	var kobo int64
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*)::int, COALESCE(sum(amount_kobo - paid_kobo), 0)::bigint,
			count(*) FILTER (WHERE due_on < (now() AT TIME ZONE 'Africa/Lagos')::date)::int
			FROM bills WHERE tenant_id = $1 AND status NOT IN ('paid', 'void') AND NOT voided AND due_on IS NOT NULL
			AND due_on <= (now() AT TIME ZONE 'Africa/Lagos')::date + 3`, tenant).Scan(&n, &kobo, &overdue)
	})
	if err != nil || n == 0 {
		return ""
	}
	if overdue > 0 {
		return tr(l, "money.daily.bills_due_overdue", "n", strconv.Itoa(n), "amount", invoicing.Naira(kobo), "overdue", strconv.Itoa(overdue))
	}
	return tr(l, "money.daily.bills_due", "n", strconv.Itoa(n), "amount", invoicing.Naira(kobo))
}

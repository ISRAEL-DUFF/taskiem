package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/access"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/notices"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/templates"
)

// Bills, business links L1 (docs/spec/business-links.md §6): a linked
// supplier's invoice, mirrored into the buyer's business. The seller's
// transaction only notes the invoice as due (migration 00292); the
// mirror worker here writes the bill in a transaction of its own, entering
// the buyer's side through taskiem_link_enter, and tells the buyer's
// people of a new bill in their own scope.

func (s *Server) billRoutes(r chi.Router) {
	read := s.need(PermInvoiceRead)
	manage := s.need(PermInvoiceManage)
	r.With(read).Get("/bills", s.listBills)
	r.With(manage).Get("/bills/pay", s.getBillPay)
	r.With(manage, s.tenantWide).Put("/bills/pay/setup", s.setupBillPay)
	r.With(read).Get("/bills/{id}", s.getBill)
	r.With(read).Get("/bills/{id}/payments", s.listBillPayments)
	r.With(manage, s.tenantWide).Post("/bills/{id}/pay", s.payBill)
	r.With(manage, s.tenantWide).Post("/bills/{id}/payments/{payment}/confirm-pair", s.confirmBillPair)
	r.With(manage, s.tenantWide).Post("/bills/{id}/declare", s.declareBillPaid)
	r.With(manage, s.tenantWide).Post("/bills/{id}/query", s.queryBill)
	r.With(manage, s.tenantWide).Post("/invoices/{id}/settle-query", s.settleInvoiceQuery)
}

type billsOut struct {
	Bills []links.Bill `json:"bills"`
}

func (s *Server) listBills(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && !slices.Contains(links.BillStatuses, status) {
		s.fail(w, r, fmt.Errorf("%w: status is open, paid, void or all", errBadRequest))
		return
	}
	var out billsOut
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out.Bills, err = links.ListBillsTx(r.Context(), tx, status, 200)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getBill(w http.ResponseWriter, r *http.Request) {
	var b links.Bill
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		b, err = links.GetBillTx(r.Context(), tx, chi.URLParam(r, "id"))
		return err
	}); err != nil {
		s.fail(w, r, billErr(err))
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// listBillPayments is GET /v1/bills/{id}/payments: the bill's payments
// from Taskiem, newest first, each with whether the bank confirmed it and
// whether the supplier's matching paired it.
func (s *Server) listBillPayments(w http.ResponseWriter, r *http.Request) {
	var out []links.BillPayment
	if err := s.tx(r, func(tx pgx.Tx) error {
		b, err := links.GetBillTx(r.Context(), tx, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		out, err = links.PaymentsOfBillTx(r.Context(), tx, b.ID)
		return err
	}); err != nil {
		s.fail(w, r, billErr(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"payments": out})
}

// billErr answers a bill's refusals with their reasons.
func billErr(err error) error {
	switch {
	case errors.Is(err, links.ErrAmbiguous):
		return fmt.Errorf("%w: %s", errConflict, strings.TrimPrefix(err.Error(), links.ErrAmbiguous.Error()+": "))
	case errors.Is(err, links.ErrState):
		return fmt.Errorf("%w: %s", errConflict, strings.TrimPrefix(err.Error(), links.ErrState.Error()+": "))
	case errors.Is(err, links.ErrInvalid):
		return fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), links.ErrInvalid.Error()+": "))
	case errors.Is(err, links.ErrNotFound):
		return pgx.ErrNoRows
	}
	return err
}

// --- the mirror worker ---

// RunLinkMirrors mirrors linked invoices into their buyers' bills, and
// does the linked orders' work (link_orders.go), every few seconds, while
// ctx lives.
func (s *Server) runLinkMirrors(ctx context.Context) {
	tick, payments, hourly := time.NewTicker(3*time.Second), time.NewTicker(2*time.Minute), time.NewTicker(time.Hour)
	defer tick.Stop()
	defer payments.Stop()
	defer hourly.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-payments.C:
			if _, err := s.BillPaymentsTick(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("links: settling bill payments", "err", err)
			}
		case <-tick.C:
			if _, err := s.LinkMirrorTick(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("links: mirroring bills", "err", err)
			}
			if _, err := s.LinkOrdersTick(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("links: linked orders", "err", err)
			}
		case <-hourly.C:
			if _, err := s.LinkReconcile(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("links: reconciling bills", "err", err)
			}
			// The link trigger's starts past their retention (link_connector.go).
			if err := s.purgeLinkStarts(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("links: purging queued starts", "err", err)
			}
		}
	}
}

// LinkReconcile compares every active link's invoices with their bills
// and puts each that differs back on its seller's mirror list, for the
// worker to write again from the invoice; it reports how many, and logs
// a warning when there are any (a mirror that went wrong, or was changed
// by hand).
func (s *Server) LinkReconcile(ctx context.Context) (int, error) {
	var n int
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_link_reconcile()`).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		s.Logger.Warn("links: bills differed from their invoices and are mirrored again", "count", n)
	}
	return n, nil
}

// BillPaymentsTick settles, in every business with a bill payment to look
// at, the payments whose workflow ended without recording them (failed,
// cancelled, or never started), and tells the business's owners of a
// payment whose outcome is unknown (the money went, the workflow ended
// without saying how; the bill stays paying) and of a payout the bank
// confirmed three days ago that the supplier's matching never paired
// (§5.3): the money reached the supplier, who may have counted it against
// an older invoice; a button confirms the pair from the bill. It reports
// how many businesses it looked at.
func (s *Server) BillPaymentsTick(ctx context.Context) (int, error) {
	rows, err := s.Store.Pool.Query(ctx, `SELECT taskiem_bill_payments_tenants()`)
	if err != nil {
		return 0, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	for _, tenant := range tenants {
		var unknown, unpaired []links.BillPayment
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			if _, err := links.SettleStalePaymentsTx(ctx, tx); err != nil {
				return err
			}
			var err error
			if unknown, err = links.UnknownPaymentsTx(ctx, tx); err != nil {
				return err
			}
			unpaired, err = links.UnpairedPayoutsTx(ctx, tx)
			return err
		}); err != nil {
			s.Logger.Warn("links: settling a business's bill payments", "tenant", tenant, "err", err)
			continue
		}
		for _, p := range unknown {
			s.tellHoldersWith(ctx, tenant, []string{PermInvoiceManage, PermLinkManage}, "bill-payment:"+p.ID.String(), "/bills/"+p.BillID.String(),
				func(l lang.Tag) (string, string) {
					return tr(l, "bill.unknown_title", "reference", p.Reference), tr(l, "bill.unknown", "supplier", whatsapp.SafeText(p.SupplierName),
						"reference", p.Reference, "amount", invoicing.Naira(p.AmountKobo))
				}, nil)
		}
		for _, p := range unpaired {
			s.tellHoldersWith(ctx, tenant, []string{PermInvoiceManage, PermLinkManage}, "bill-payment:"+p.ID.String(), "/bills/"+p.BillID.String(),
				func(l lang.Tag) (string, string) {
					return tr(l, "bill.unpaired_title", "reference", p.Reference), tr(l, "bill.unpaired", "supplier", whatsapp.SafeText(p.SupplierName),
						"reference", p.Reference, "amount", invoicing.Naira(p.AmountKobo), "token", p.Token)
				}, func(l lang.Tag) []whatsapp.Button {
					return []whatsapp.Button{{ID: billPairPrefix + p.ID.String(), Title: tr(l, "bill.button_confirm_pair")}}
				})
		}
	}
	return len(tenants), nil
}

// billPairPrefix starts the chat button that confirms a payout paid its
// bill: bp:<payment>.
const billPairPrefix = "bp:"

// waBillPairPress takes Confirm on an unpaired payout's message, for
// whichever of the person's businesses made the payment, if they may pay
// bills there.
func (s *Server) waBillPairPress(ctx context.Context, c *chat, reply string) error {
	id, err := uuid.Parse(strings.TrimPrefix(reply, billPairPrefix))
	if err != nil {
		return c.say(ctx, s, c.t("bill.pair_gone"))
	}
	tenants := append([]tenantRef{c.tenant}, slices.DeleteFunc(slices.Clone(c.tenants), func(t tenantRef) bool { return t.ID == c.tenant.ID })...)
	for _, t := range tenants {
		var bill uuid.UUID
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{t.ID}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT bill_id FROM bill_payments WHERE id = $1`, id).Scan(&bill)
		}); errors.Is(err, pgx.ErrNoRows) {
			continue
		} else if err != nil {
			return err
		}
		p, err := s.principalOf(ctx, t.ID, c.p.UserID)
		if err != nil || !p.Can(PermInvoiceManage) {
			return c.say(ctx, s, c.t("bill.denied", "org", t.Name))
		}
		pay, n, seller, err := s.confirmPair(ctx, p, bill.String(), id.String())
		switch {
		case errors.Is(err, links.ErrState):
			return c.say(ctx, s, c.t("bill.pair_gone"))
		case err != nil:
			return err
		}
		s.tellNotice(ctx, seller, t.ID, n, "/invoices/"+pay.Reference)
		return c.say(ctx, s, c.t("bill.pair_confirmed", "reference", pay.Reference, "supplier", whatsapp.SafeText(pay.SupplierName)))
	}
	return c.say(ctx, s, c.t("bill.pair_gone"))
}

// confirmPair marks a payout paired with its bill, as p's business, and
// tells the supplier which invoice the money was for.
func (s *Server) confirmPair(ctx context.Context, p *Principal, bill, payment string) (links.BillPayment, links.Notice, uuid.UUID, error) {
	var pay links.BillPayment
	var n links.Notice
	var seller uuid.UUID
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		detail, _ := json.Marshal(map[string]any{"payment": payment})
		if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, $2, $3, 'bill.pair', $4, $5)`, p.TenantID, p.ActorType(), p.Actor(), bill, detail); err != nil {
			return err
		}
		var err error
		pay, n, seller, err = links.ConfirmPairTx(ctx, tx, p.TenantID, bill, payment, links.Actor{Type: p.ActorType(), ID: p.Actor()})
		return err
	})
	return pay, n, seller, err
}

// confirmBillPair is POST /v1/bills/{id}/payments/{payment}/confirm-pair
// (§5.3): the business's own word that a payout the bank confirmed, which
// the supplier's matching did not pair in three days, paid this bill. The
// payment is marked paired and the supplier is told which invoice the
// money was for.
func (s *Server) confirmBillPair(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	pay, n, seller, err := s.confirmPair(r.Context(), p, chi.URLParam(r, "id"), chi.URLParam(r, "payment"))
	if err != nil {
		s.fail(w, r, billErr(err))
		return
	}
	s.tellNotice(r.Context(), seller, p.TenantID, n, "/invoices/"+pay.Reference)
	writeJSON(w, http.StatusOK, pay)
}

// LinkMirrorTick mirrors the invoices due, and reports how many it
// mirrored. One that cannot be mirrored yet stays due, tried again later.
func (s *Server) LinkMirrorTick(ctx context.Context) (int, error) {
	if s.Invoices == nil {
		return 0, nil
	}
	rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id, invoice_id FROM taskiem_link_mirrors_claim(50)`)
	if err != nil {
		return 0, err
	}
	type due struct{ seller, invoice uuid.UUID }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
		var d due
		return d, r.Scan(&d.seller, &d.invoice)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range list {
		done, err := s.mirrorInvoice(ctx, d.seller, d.invoice)
		if err != nil {
			s.Logger.Warn("links: mirroring an invoice", "tenant", d.seller, "invoice", d.invoice, "err", err)
			continue
		}
		if done {
			n++
		}
	}
	return n, nil
}

// mirrorInvoice writes one invoice's bill: in the seller's scope it reads
// the invoice and the link and takes the invoice off the due list, then
// enters the buyer's side and writes the bill, in one transaction. An
// invoice with no active link to its customer is taken off and mirrors
// nothing.
func (s *Server) mirrorInvoice(ctx context.Context, seller, invoice uuid.UUID) (bool, error) {
	var buyer, link uuid.UUID
	var created bool
	var bill links.Mirror
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{seller}, func(tx pgx.Tx) error {
		clear := func() error {
			_, err := tx.Exec(ctx, `DELETE FROM link_mirrors_due WHERE tenant_id = $1 AND invoice_id = $2`, seller, invoice)
			return err
		}
		x, err := s.Invoices.Get(ctx, tx, seller, invoice.String())
		if errors.Is(err, invoicing.ErrNotFound) {
			return clear()
		}
		if err != nil {
			return err
		}
		var ok bool
		if buyer, ok = links.BuyerOf(x.Customer.Key); !ok || x.SentAt == nil {
			return clear()
		}
		list, _, err := links.ListTx(ctx, tx)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(list, func(l links.Link) bool { return l.Business == buyer && l.State == links.Active })
		if i < 0 {
			return clear()
		}
		link = list[i].ID
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, seller).Scan(&name); err != nil {
			return err
		}
		lines, err := json.Marshal(x.Lines)
		if err != nil {
			return err
		}
		bill = links.Mirror{Link: link, Supplier: seller, SupplierName: name, Source: x.ID, Reference: x.Number, Lines: lines,
			AmountKobo: x.TotalKobo, PaidKobo: x.PaidKobo, DueOn: x.DueOn, Voided: x.VoidedAt != nil, AllowParts: x.AllowPartPayments}
		// The way to pay: the account number issued to this buyer (the
		// first, when there are several), and the invoice's page.
		accounts, err := s.Invoices.AccountsOf(ctx, tx, seller, x.CustomerID())
		if err != nil {
			return err
		}
		if len(accounts) > 0 {
			bill.PayAccountNumber, bill.PayBankName = accounts[0].AccountNumber, accounts[0].BankName
		}
		if x.VoidedAt == nil {
			if bill.PayLink, err = s.Invoices.PageLink(ctx, tx, seller, x); err != nil {
				return err
			}
		}
		if err := clear(); err != nil {
			return err
		}
		// The seller's rows are written; from here the scope is the
		// buyer's alone.
		detail, _ := json.Marshal(map[string]any{"invoice": x.Number})
		if _, err := links.EnterTx(ctx, tx, link, "link.bill", detail, links.Actor{Type: "system", ID: "link-mirror"}); err != nil {
			return err
		}
		created, err = links.MirrorBillTx(ctx, tx, buyer, bill)
		return err
	})
	if errors.As(err, new(*links.OtherOffError)) {
		// The buyer has turned Money off: nothing crosses, and the invoice
		// stays due to be mirrored, which it is when they turn it back on.
		return false, nil
	}
	if err != nil || link == uuid.Nil {
		return false, err
	}
	if created {
		s.background(ctx, "bill arrived", func(ctx context.Context) {
			amount := bill.AmountKobo
			card := s.billCards(ctx, link, seller, buyer, bill.Reference, "bill", true, &amount, "")
			s.tellBill(ctx, buyer, bill, s.keepSent(ctx, buyer, link, card))
		})
	}
	return true, nil
}

// tellBill tells the buyer's people who may pay of a new bill, in the bell
// and in chat, in the buyer's own scope.
func (s *Server) tellBill(ctx context.Context, buyer uuid.UUID, b links.Mirror, sent func(channel, addr, id string)) {
	s.tellHoldersSent(ctx, buyer, []string{PermInvoiceManage, PermLinkManage}, "bill:"+b.Source.String(), "/bills",
		func(l lang.Tag) (string, string) { return billWords(l, b) }, nil, sent)
}

// tellHolders tells the people of a business whose role holds every one of
// perms, in the bell and in chat, in that business's own scope.
func (s *Server) tellHolders(ctx context.Context, tenant uuid.UUID, perms []string, group, link string, words func(lang.Tag) (string, string)) {
	s.tellHoldersWith(ctx, tenant, perms, group, link, words, nil)
}

// tellHoldersWith is tellHolders, the chat message with buttons in the
// reader's language.
func (s *Server) tellHoldersWith(ctx context.Context, tenant uuid.UUID, perms []string, group, link string, words func(lang.Tag) (string, string),
	buttons func(lang.Tag) []whatsapp.Button) {
	s.tellHoldersSent(ctx, tenant, perms, group, link, words, buttons, nil)
}

// tellHoldersSent is tellHoldersWith, hearing of each chat message sent:
// its channel, the address and the chat app's id for it.
func (s *Server) tellHoldersSent(ctx context.Context, tenant uuid.UUID, perms []string, group, link string, words func(lang.Tag) (string, string),
	buttons func(lang.Tag) []whatsapp.Button, sent func(channel, addr, id string)) {
	var to []linkTold
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT user_id FROM memberships WHERE role = ANY ($1)`, holdingAll(perms...))
		if err != nil {
			return err
		}
		users, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		title, body := words(lang.EN)
		if err := notices.AddTx(ctx, tx, tenant, users, notices.Notice{Kind: notices.Setup, Title: title, Body: body, Link: link, Group: group}); err != nil {
			return err
		}
		for _, channel := range []string{"whatsapp", "telegram", "slack"} {
			addrs, err := chatAddrsTx(ctx, tx, channel, users)
			if err != nil {
				return err
			}
			for _, u := range users {
				if a, ok := addrs[u]; ok && !slices.ContainsFunc(to, func(t linkTold) bool { return t.addr == a }) {
					to = append(to, linkTold{channel: channel, addr: a, user: u})
				}
			}
		}
		return nil
	})
	if err != nil {
		s.Logger.Warn("links: telling a business", "tenant", tenant, "err", err)
		return
	}
	for _, t := range to {
		ch, err := s.chatChannelFor(ctx, t.channel, tenant)
		if err != nil {
			continue
		}
		l := s.chooseLang(ctx, tenant, t.addr, "").Tag
		title, body := words(l)
		var bs []whatsapp.Button
		if buttons != nil {
			bs = buttons(l)
		}
		id, err := ch.Send(ctx, t.addr, linkPhoneMessage(tenant, ch.Prefix(""), title, body, bs))
		if err != nil {
			s.Logger.Warn("links: a chat message", "tenant", tenant, "err", err)
			continue
		}
		if sent != nil && id != "" {
			sent(t.channel, t.addr, id)
		}
	}
}

// tellNotice tells the other side's people what a notice says, and puts
// it in both sides' conversation as a card on the bill (L3); from is the
// business that said it.
func (s *Server) tellNotice(ctx context.Context, tenant, from uuid.UUID, n links.Notice, link string) {
	s.background(ctx, "link notice", func(ctx context.Context) {
		seller, buyer := tenant, from // the buyer declared or queried
		if n.Kind == "resolved" {
			seller, buyer = from, tenant
		}
		card := s.billCards(ctx, n.Link, seller, buyer, n.Reference, n.Kind, n.Kind == "resolved", n.AmountKobo, n.Note)
		s.tellHoldersSent(ctx, tenant, []string{PermInvoiceManage, PermLinkManage}, "notice:"+n.ID.String(), link, func(l lang.Tag) (string, string) {
			amount := ""
			if n.AmountKobo != nil {
				amount = invoicing.Naira(*n.AmountKobo)
			}
			return tr(l, "bill.notice."+n.Kind+"_title", "from", n.From, "reference", n.Reference),
				strings.TrimSpace(tr(l, "bill.notice."+n.Kind, "from", whatsapp.SafeText(n.From), "reference", n.Reference, "amount", amount,
					"how", tr(l, "bill.how."+n.How), "note", whatsapp.SafeText(n.Note)))
		}, nil, s.keepSent(ctx, tenant, n.Link, card))
	})
}

// holdingAll are the built-in roles holding every one of perms.
func holdingAll(perms ...string) []string {
	var out []string
	for _, r := range access.Holding(perms[0]) {
		ok := true
		for _, p := range perms[1:] {
			ok = ok && slices.Contains(access.Roles[r], p)
		}
		if ok {
			out = append(out, r)
		}
	}
	return out
}

func billWords(l lang.Tag, b links.Mirror) (string, string) {
	due := ""
	if b.DueOn != "" {
		if d, err := time.Parse("2006-01-02", b.DueOn); err == nil {
			due = tr(l, "bill.due_on", "date", d.Format("2 Jan 2006"))
		}
	}
	return tr(l, "bill.arrived_title", "supplier", b.SupplierName),
		strings.TrimSpace(tr(l, "bill.arrived_body", "supplier", whatsapp.SafeText(b.SupplierName), "reference", b.Reference,
			"amount", invoicing.Naira(b.AmountKobo-b.PaidKobo)) + " " + due)
}

// --- chat ---

// waBillCommand answers the buyer's bill words: "bills" (also "my bills",
// "bills due"), the bills still to pay; "bill INV-0042", one bill, with
// the supplier's name before the number when two suppliers used it ("bill
// Dangote INV-0042"). False when text is neither ("bill Ada 5,000" makes
// an invoice, as before: the last word is not an invoice number).
func (s *Server) waBillCommand(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	f := strings.Fields(text)
	one := len(f) >= 2 && strings.EqualFold(f[0], "bill") && (len(f) == 2 || billNumberRe.MatchString(f[len(f)-1]))
	switch {
	case cmd == "bills" || cmd == "my bills" || cmd == "bills due":
	case one:
	default:
		return false, nil
	}
	if !c.p.Can(PermInvoiceRead) {
		return true, c.say(ctx, s, c.t("bill.denied", "org", c.tenant.Name))
	}
	if one {
		var b links.Bill
		err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			var err error
			b, err = links.GetBillTx(ctx, tx, strings.Join(f[1:], " "))
			return err
		})
		if errors.Is(err, links.ErrNotFound) {
			return true, c.say(ctx, s, c.t("bill.not_found", "reference", whatsapp.SafeText(strings.Join(f[1:], " "))))
		}
		if errors.Is(err, links.ErrAmbiguous) {
			// Two suppliers used the number: "bill Dangote INV-0042".
			return true, c.say(ctx, s, whatsapp.SafeText(strings.TrimPrefix(err.Error(), links.ErrAmbiguous.Error()+": ")))
		}
		if err != nil {
			return true, err
		}
		return true, c.say(ctx, s, billDetail(c.lang.Tag, b))
	}
	var list []links.Bill
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		list, err = links.ListBillsTx(ctx, tx, "open", 20)
		return err
	}); err != nil {
		return true, err
	}
	if len(list) == 0 {
		return true, c.say(ctx, s, c.t("bill.none"))
	}
	lines := []string{c.t("bill.list_title")}
	var total int64
	for _, b := range list {
		lines = append(lines, c.t("bill.list_line", "reference", b.Reference, "supplier", whatsapp.SafeText(b.SupplierName), "amount", invoicing.Naira(b.DueKobo))+billDue(c.lang.Tag, b))
		total += b.DueKobo
	}
	lines = append(lines, c.t("bill.list_total", "amount", invoicing.Naira(total)))
	return true, c.say(ctx, s, strings.Join(lines, "\n"))
}

// billNumberRe is an invoice number as suppliers' Taskiem numbers them
// (INV-0042), the last word of "bill Dangote INV-0042".
var billNumberRe = regexp.MustCompile(`^[A-Za-z]{1,6}-?[0-9]{1,12}$`)

func billDue(l lang.Tag, b links.Bill) string {
	if b.DueOn == "" {
		return ""
	}
	d, err := time.Parse("2006-01-02", b.DueOn)
	if err != nil {
		return ""
	}
	return " " + tr(l, "bill.due_on", "date", d.Format("2 Jan 2006"))
}

// billDetail is one bill in chat: who sent it, its lines, what is paid and
// what is left.
func billDetail(l lang.Tag, b links.Bill) string {
	var lines []invoicing.Line
	_ = json.Unmarshal(b.Lines, &lines)
	out := []string{tr(l, "bill.detail_head", "reference", b.Reference, "supplier", whatsapp.SafeText(b.SupplierName), "status", tr(l, "bill.status."+b.Status))}
	for _, x := range lines {
		out = append(out, "• "+whatsapp.SafeText(x.Description)+": "+invoicing.Naira(x.AmountKobo()))
	}
	out = append(out, tr(l, "bill.detail_sums", "total", invoicing.Naira(b.AmountKobo), "paid", invoicing.Naira(b.PaidKobo), "due", invoicing.Naira(b.DueKobo))+billDue(l, b))
	if b.DueKobo > 0 && b.PayAccountNumber != "" {
		out = append(out, tr(l, "bill.pay_into", "account", b.PayAccountNumber, "bank", b.PayBankName, "reference", b.Reference))
	}
	if b.DueKobo > 0 && b.PayLink != "" {
		out = append(out, tr(l, "bill.pay_page", "link", b.PayLink))
	}
	return strings.Join(out, "\n")
}

// --- paying a bill from Taskiem (§6.2) ---

// payLinkedBillTemplate is the managed workflow a business's bills are
// paid with: the name on the account found, one approval, the payout
// carrying the bill's token, the bank's confirmation, the outcome recorded.
const payLinkedBillTemplate = "pay-linked-bill"

type billPaySettings struct {
	WorkflowID     *uuid.UUID `json:"workflow_id"`
	Connection     string     `json:"connection"`
	FundingAccount string     `json:"funding_account"`
	ApproverRole   string     `json:"approver_role"`
	// Waiting: the workflow waits for a second person to publish it.
	Waiting bool `json:"waiting,omitempty"`
}

func billPaySettingsTx(ctx context.Context, tx pgx.Tx) (billPaySettings, error) {
	var st billPaySettings
	err := tx.QueryRow(ctx, `SELECT workflow_id, connection, funding_account, approver_role FROM bill_pay_settings`).
		Scan(&st.WorkflowID, &st.Connection, &st.FundingAccount, &st.ApproverRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return billPaySettings{ApproverRole: "owner"}, nil
	}
	return st, err
}

func (s *Server) getBillPay(w http.ResponseWriter, r *http.Request) {
	var st billPaySettings
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		st, err = billPaySettingsTx(r.Context(), tx)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

type billPaySetup struct {
	RailConnection string `json:"rail_connection"`
	FundingAccount string `json:"funding_account"`
	ApproverRole   string `json:"approver_role"`
}

// setupBillPay is PUT /v1/bills/pay/setup: the payments account bills are
// paid from and who approves, written into the managed workflow made from
// pay-linked-bill (a new version after the first), under the business's
// governance as any payout workflow.
func (s *Server) setupBillPay(w http.ResponseWriter, r *http.Request) {
	var in billPaySetup
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.ApproverRole == "" {
		in.ApproverRole = "owner"
	}
	tpl, ok := templates.Default().Get(payLinkedBillTemplate)
	if !ok {
		s.fail(w, r, fmt.Errorf("%w: paying bills is not available here", errConflict))
		return
	}
	p := principalFrom(r.Context())
	var out billPaySettings
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		// With no payments account chosen, bills are paid from the
		// business's Taskiem wallet.
		if in.RailConnection == "" || in.RailConnection == secrets.PlatformName {
			var wallet *string
			err := tx.QueryRow(ctx, `SELECT wallet_id FROM receive_money WHERE tenant_id = $1`, p.TenantID).Scan(&wallet)
			if errors.Is(err, pgx.ErrNoRows) || err == nil && (wallet == nil || *wallet == "") {
				return fmt.Errorf("%w: choose the payments account bills are paid from, or turn on receiving money with Taskiem", errConflict)
			}
			if err != nil {
				return err
			}
			in.RailConnection, in.FundingAccount = secrets.PlatformName, *wallet
		}
		doc, err := tpl.Instantiate(map[string]any{"rail_connection": in.RailConnection, "funding_account": in.FundingAccount, "approver_role": in.ApproverRole},
			"Pay bills from linked suppliers")
		if err != nil {
			return fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		have, err := billPaySettingsTx(ctx, tx)
		if err != nil {
			return err
		}
		g, err := governanceTx(ctx, tx)
		if err != nil {
			return err
		}
		if out.Waiting, err = publishNeedsApproval(ctx, tx, g, nil); err != nil {
			return err
		}
		wf, _, err := s.managedWorkflowTx(r, tx, managedWorkflow{Name: "Pay bills from linked suppliers", Template: payLinkedBillTemplate, What: "how bills are paid"},
			doc, have.WorkflowID, out.Waiting)
		if err != nil {
			return err
		}
		out.WorkflowID, out.Connection, out.FundingAccount, out.ApproverRole = &wf, in.RailConnection, in.FundingAccount, in.ApproverRole
		_, err = tx.Exec(ctx, `INSERT INTO bill_pay_settings (tenant_id, workflow_id, connection, funding_account, approver_role, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (tenant_id) DO UPDATE SET workflow_id = $2, connection = $3, funding_account = $4,
			approver_role = $5, updated_by = $6, updated_at = now()`, p.TenantID, wf, in.RailConnection, in.FundingAccount, in.ApproverRole, p.Actor())
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type billPayIn struct {
	// AmountKobo is a part payment, where the supplier takes parts; empty
	// pays what is left.
	AmountKobo int64 `json:"amount_kobo"`
}

type billPayOut struct {
	Payment links.BillPayment `json:"payment"`
	RunID   uuid.UUID         `json:"run_id"`
}

// payBill is POST /v1/bills/{id}/pay: a payout of the bill from the
// business's own payments account, through the pay-linked-bill workflow
// (the approver approves it in Approvals or chat). The supplier's bank is
// found by name among the banks that account pays (docs/spec/business-
// links.md §16.2): with no single match it is refused, plainly.
func (s *Server) payBill(w http.ResponseWriter, r *http.Request) {
	var in billPayIn
	if r.ContentLength != 0 {
		if err := decodeBody(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	p := principalFrom(r.Context())
	var bill links.Bill
	pay, err := s.startBillPay(r.Context(), p.TenantID, chi.URLParam(r, "id"), in.AmountKobo, p.ActorType()+":"+p.Actor(), p.Actor(),
		func(tx pgx.Tx, b links.Bill, pay links.BillPayment) error {
			bill = b
			return auditTx(r, tx, "bill.pay", b.Reference, map[string]any{"amount_kobo": pay.AmountKobo, "payment": pay.ID})
		})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.diaryChange(r.Context(), p, buyerStepWords("pay", bill.Reference, bill.SupplierName, pay.AmountKobo))
	writeJSON(w, http.StatusAccepted, billPayOut{Payment: pay, RunID: *pay.RunID})
}

// startBillPay starts a payment of a bill (by id or reference) from the
// business's own payments account, through its pay-linked-bill workflow,
// whose approver approves it: for Pay, and for a workflow's link@1
// pay_bill. amount 0 pays what is left. by is who started it.
func (s *Server) startBillPay(ctx context.Context, tenant uuid.UUID, bill string, amount int64, by, startedBy string,
	audit func(tx pgx.Tx, b links.Bill, pay links.BillPayment) error) (links.BillPayment, error) {
	var st billPaySettings
	var b links.Bill
	var version int
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		// A payment whose workflow died is settled first; one the money
		// went for stays, and the tick tells the owner.
		if _, err := links.SettleStalePaymentsTx(ctx, tx); err != nil {
			return err
		}
		var err error
		if st, err = billPaySettingsTx(ctx, tx); err != nil {
			return err
		}
		if st.WorkflowID == nil {
			return fmt.Errorf("%w: set up paying bills first: choose the payments account they are paid from", errConflict)
		}
		version, err = deployedVersion(ctx, tx, *st.WorkflowID, "prod")
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: paying bills waits to be published", errConflict)
		}
		if err != nil {
			return err
		}
		b, err = links.GetBillTx(ctx, tx, bill)
		return err
	})
	if err != nil {
		return links.BillPayment{}, billErr(err)
	}
	if b.PayAccountNumber == "" || b.PayBankName == "" {
		return links.BillPayment{}, fmt.Errorf("%w: %s has not given your business an account number to pay into; pay on the invoice's page instead", errConflict, b.SupplierName)
	}
	code, err := s.billBankCode(ctx, tenant, st.Connection, b.PayBankName)
	if err != nil {
		return links.BillPayment{}, err
	}
	if amount == 0 {
		amount = b.DueKobo
	}
	var pay links.BillPayment
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if pay, err = links.StartBillPaymentTx(ctx, tx, tenant, links.PayIn{Bill: b.ID, AmountKobo: amount, Connection: st.Connection,
			BankCode: code, AccountNumber: b.PayAccountNumber, By: by}); err != nil {
			return err
		}
		return audit(tx, b, pay)
	}); err != nil {
		return pay, billErr(err)
	}
	ref, _, err := s.Store.StartRun(ctx, runtime.StartRequest{TenantID: tenant, WorkflowID: *st.WorkflowID, Version: version, Environment: "prod",
		Trigger: map[string]any{"type": "manual", "body": map[string]any{"payment_id": pay.ID.String(), "supplier": b.SupplierName, "reference": b.Reference,
			"token": pay.Token, "bank_code": code, "account_number": b.PayAccountNumber, "amount_kobo": pay.AmountKobo}},
		StartedBy: startedBy, TriggerID: "bills/pay", DedupKey: "bill-payment:" + pay.ID.String()})
	if err != nil {
		// Nothing will pay it: the bill is not left paying.
		_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, ferr := links.FinishBillPaymentTx(ctx, tx, pay.ID, "failed", "")
			return ferr
		})
		return pay, err
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error { return links.SetPaymentRunTx(ctx, tx, pay.ID, ref.ID) }); err != nil {
		return pay, err
	}
	pay.RunID = &ref.ID
	return pay, nil
}

// billBankCode is the code of the bank named, among the banks the
// business's payments connection pays: by name, with "Bank", "PLC" and
// "Limited" and punctuation set aside, exactly one match. BillBanks, when
// set, lists them instead of the connection.
func (s *Server) billBankCode(ctx context.Context, tenant uuid.UUID, connection, bank string) (string, error) {
	var banks []map[string]any
	var err error
	if s.BillBanks != nil {
		banks, err = s.BillBanks(ctx, tenant, connection)
	} else {
		banks, err = s.connectionBanks(ctx, tenant, connection)
	}
	if err != nil {
		return "", err
	}
	if code, ok := links.BankCode(banks, bank); ok {
		return code, nil
	}
	return "", fmt.Errorf("%w: your payments account does not list %s as one bank; pay into the account from your bank app, or on the invoice's page", errConflict, bank)
}

// connectionBanks asks a payments connection (by name, or Taskiem's
// own wallet) for the banks it pays.
func (s *Server) connectionBanks(ctx context.Context, tenant uuid.UUID, connection string) ([]map[string]any, error) {
	// The connection is named as a workflow's capability step names it:
	// the connector is the one of that name that can pay bank accounts.
	id, name := receiveConnector, secrets.PlatformName
	if connection != secrets.PlatformName {
		rails := s.accountRails()
		if rails == nil {
			return nil, fmt.Errorf("%w: this server cannot pay bank accounts", errConflict)
		}
		capable, err := rails.Capable(ctx, tenant, "pay_bank")
		if err != nil {
			return nil, err
		}
		i := slices.IndexFunc(capable, func(r invoicing.Rail) bool { return r.Connection == connection })
		if i < 0 {
			return nil, fmt.Errorf("%w: the payments account %q cannot pay bank accounts in prod", errConflict, connection)
		}
		id, name = capable[i].Connector, connection
	}
	reg, err := s.Registry.For(ctx, tenant.String())
	if err != nil {
		return nil, err
	}
	c, found := connectorByID(reg, id)
	if !found {
		return nil, fmt.Errorf("%w: the payments account %q is not available here", errConflict, connection)
	}
	use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: "bills.banks"})
	creds, err := s.Vault.Credentials(use, tenant, "prod", id, name)
	if err != nil {
		return nil, err
	}
	out, err := s.runAction(ctx, tenant, c, creds, "list_banks", "bills.banks", map[string]any{}, "")
	if err != nil {
		return nil, fmt.Errorf("%w: your payments account did not list its banks just now; try again: %s", errConflict, plainConnectorError(err))
	}
	list, _ := out["banks"].([]any)
	banks := make([]map[string]any, 0, len(list))
	for _, x := range list {
		if m, ok := x.(map[string]any); ok {
			banks = append(banks, m)
		}
	}
	return banks, nil
}

// --- what each side says: paid outside Taskiem, a query, its settling ---

type billDeclareIn struct {
	AmountKobo int64  `json:"amount_kobo"`
	How        string `json:"how"`
	Note       string `json:"note"`
}

// declareBillPaid is POST /v1/bills/{id}/declare: the business's own word
// that it paid outside Taskiem (§6.2). It is its own figure beside what
// the supplier counted; the supplier is told.
func (s *Server) declareBillPaid(w http.ResponseWriter, r *http.Request) {
	var in billDeclareIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var b links.Bill
	var n links.Notice
	var seller uuid.UUID
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := auditTx(r, tx, "bill.declare", chi.URLParam(r, "id"), map[string]any{"amount_kobo": in.AmountKobo, "how": in.How}); err != nil {
			return err
		}
		var err error
		b, n, seller, err = links.DeclarePaidTx(r.Context(), tx, p.TenantID, chi.URLParam(r, "id"), in.AmountKobo, in.How, strings.TrimSpace(in.Note),
			links.Actor{Type: p.ActorType(), ID: p.Actor()})
		return err
	}); err != nil {
		s.fail(w, r, billErr(err))
		return
	}
	s.tellNotice(r.Context(), seller, p.TenantID, n, "/invoices/"+b.Reference)
	s.diaryChange(r.Context(), p, buyerStepWords("declare", b.Reference, b.SupplierName, in.AmountKobo))
	writeJSON(w, http.StatusOK, b)
}

type billQueryIn struct {
	Reason string `json:"reason"`
}

// queryBill is POST /v1/bills/{id}/query (§8): the bill is queried, not
// paid from Taskiem until the query is settled, and the supplier's invoice
// is passed over by its oldest-first matching; the supplier is told.
func (s *Server) queryBill(w http.ResponseWriter, r *http.Request) {
	var in billQueryIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var b links.Bill
	var n links.Notice
	var seller uuid.UUID
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := auditTx(r, tx, "bill.query", chi.URLParam(r, "id"), nil); err != nil {
			return err
		}
		var err error
		b, n, seller, err = links.QueryBillTx(r.Context(), tx, p.TenantID, chi.URLParam(r, "id"), in.Reason, links.Actor{Type: p.ActorType(), ID: p.Actor()})
		return err
	}); err != nil {
		s.fail(w, r, billErr(err))
		return
	}
	s.tellNotice(r.Context(), seller, p.TenantID, n, "/invoices/"+b.Reference)
	s.diaryChange(r.Context(), p, buyerStepWords("query", b.Reference, b.SupplierName, 0))
	writeJSON(w, http.StatusOK, b)
}

type settleQueryIn struct {
	// How the query is settled (§8): as_it_stands (the invoice stays, Note
	// says why), or discount (AmountKobo off the invoice, an amendment
	// with Note as its reason). Empty is as_it_stands.
	How        string `json:"how"`
	AmountKobo int64  `json:"amount_kobo"`
	Note       string `json:"note"`
}

// settleInvoiceQuery is POST /v1/invoices/{id}/settle-query, the seller's
// answer to a linked buyer's query (§8): the invoice is matched
// oldest-first again and the buyer's bill is no longer queried; the buyer
// is told.
func (s *Server) settleInvoiceQuery(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	var in settleQueryIn
	if r.ContentLength != 0 {
		if err := decodeBody(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	in.Note = strings.TrimSpace(in.Note)
	switch in.How {
	case "", "as_it_stands":
		in.How = "as_it_stands"
	case "discount":
		if in.AmountKobo <= 0 || in.Note == "" {
			s.fail(w, r, fmt.Errorf("%w: taking an amount off needs the amount and the reason", errBadRequest))
			return
		}
	default:
		s.fail(w, r, fmt.Errorf("%w: how is as_it_stands or discount", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	var n links.Notice
	var buyer uuid.UUID
	var number string
	var amended invoicing.Amended
	if err := s.tx(r, func(tx pgx.Tx) error {
		x, err := s.Invoices.Get(r.Context(), tx, p.TenantID, chi.URLParam(r, "id"))
		if err != nil {
			return invoiceErr(err)
		}
		number = x.Number
		if err := auditTx(r, tx, "invoice.settle_query", x.Number, map[string]any{"how": in.How, "amount_kobo": in.AmountKobo}); err != nil {
			return err
		}
		note := in.Note
		if in.How == "discount" {
			// The amount off is the invoice's own amendment, made before the
			// buyer's side is written.
			if !x.Disputed {
				return fmt.Errorf("%w: the invoice is not queried", links.ErrState)
			}
			if amended, err = s.Invoices.Amend(r.Context(), tx, p.TenantID, x.ID.String(), invoicing.AmendmentIn{Kind: invoicing.AmendDiscount,
				Description: "Settled query", AmountKobo: in.AmountKobo, Reason: in.Note}, p.Actor()); err != nil {
				return invoiceErr(err)
			}
			note = "Took " + invoicing.Naira(in.AmountKobo) + " off: " + in.Note
		}
		n, buyer, err = links.ResolveQueryTx(r.Context(), tx, p.TenantID, x.ID, x.Number, x.Customer.Key, note, links.Actor{Type: p.ActorType(), ID: p.Actor()})
		return err
	}); err != nil {
		s.fail(w, r, billErr(err))
		return
	}
	if amended.Became {
		if err := s.Invoices.Paid(r.Context(), p.TenantID, amended.Invoice); err != nil {
			s.Logger.Warn("links: an invoice paid by settling a query", "err", err)
		}
	}
	if n.ID != uuid.Nil {
		s.tellNotice(r.Context(), buyer, p.TenantID, n, "/bills")
	}
	writeJSON(w, http.StatusOK, map[string]any{"number": number, "settled": true})
}

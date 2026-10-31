package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Bills, business links L1 (docs/spec/business-links.md §6): a linked
// supplier's invoice, mirrored into the buyer's business. The seller's
// transaction only notes the invoice as due (migration 00276); the
// mirror worker here writes the bill in a transaction of its own, entering
// the buyer's side through taskiem_link_enter, and tells the buyer's
// people of a new bill in their own scope.

func (s *Server) billRoutes(r chi.Router) {
	read := s.need(PermInvoiceRead)
	r.With(read).Get("/bills", s.listBills)
	r.With(read).Get("/bills/{id}", s.getBill)
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
		s.fail(w, r, linkErr(err))
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// --- the mirror worker ---

// RunLinkMirrors mirrors linked invoices into their buyers' bills every
// few seconds, while ctx lives.
func (s *Server) runLinkMirrors(ctx context.Context) {
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if _, err := s.LinkMirrorTick(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("links: mirroring bills", "err", err)
			}
		}
	}
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
	if err != nil || link == uuid.Nil {
		return false, err
	}
	if created {
		s.background(ctx, "bill arrived", func(ctx context.Context) { s.tellBill(ctx, buyer, bill) })
	}
	return true, nil
}

// tellBill tells the buyer's people who may pay of a new bill, in the bell
// and in chat, in the buyer's own scope.
func (s *Server) tellBill(ctx context.Context, buyer uuid.UUID, b links.Mirror) {
	var to []linkTold
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{buyer}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT user_id FROM memberships WHERE role = ANY ($1)`, holdingAll(PermInvoiceManage, PermLinkManage))
		if err != nil {
			return err
		}
		users, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		title, body := billWords(lang.EN, b)
		if err := notices.AddTx(ctx, tx, buyer, users, notices.Notice{Kind: notices.Setup, Title: title, Body: body, Link: "/bills", Group: "bill:" + b.Source.String()}); err != nil {
			return err
		}
		for _, channel := range []string{"whatsapp", "telegram", "slack"} {
			addrs, err := chatAddrsTx(ctx, tx, channel, users)
			if err != nil {
				return err
			}
			for _, u := range users {
				if a, ok := addrs[u]; ok && !slices.ContainsFunc(to, func(t linkTold) bool { return t.addr == a }) {
					to = append(to, linkTold{channel, a})
				}
			}
		}
		return nil
	})
	if err != nil {
		s.Logger.Warn("links: telling of a bill", "tenant", buyer, "err", err)
		return
	}
	for _, t := range to {
		ch, err := s.chatChannelFor(ctx, t.channel, buyer)
		if err != nil {
			continue
		}
		title, body := billWords(s.chooseLang(ctx, buyer, t.addr, "").Tag, b)
		if err := s.waSay(ctx, ch, t.addr, "", title+"\n"+body); err != nil {
			s.Logger.Warn("links: a bill's chat message", "tenant", buyer, "err", err)
		}
	}
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
// "bills due"), the bills still to pay; "bill INV-0042", one bill. False
// when text is neither ("bill Ada 5,000" makes an invoice, as before).
func (s *Server) waBillCommand(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	f := strings.Fields(text)
	switch {
	case cmd == "bills" || cmd == "my bills" || cmd == "bills due":
	case len(f) == 2 && strings.EqualFold(f[0], "bill"):
	default:
		return false, nil
	}
	if !c.p.Can(PermInvoiceRead) {
		return true, c.say(ctx, s, c.t("bill.denied", "org", c.tenant.Name))
	}
	if len(f) == 2 && strings.EqualFold(f[0], "bill") {
		var b links.Bill
		err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			var err error
			b, err = links.GetBillTx(ctx, tx, f[1])
			return err
		})
		if errors.Is(err, links.ErrNotFound) {
			return true, c.say(ctx, s, c.t("bill.not_found", "reference", whatsapp.SafeText(f[1])))
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
		out = append(out, tr(l, "bill.pay_into", "account", b.PayAccountNumber, "bank", b.PayBankName))
	}
	if b.DueKobo > 0 && b.PayLink != "" {
		out = append(out, tr(l, "bill.pay_page", "link", b.PayLink))
	}
	return strings.Join(out, "\n")
}

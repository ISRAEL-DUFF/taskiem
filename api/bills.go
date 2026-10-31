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
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/templates"
)

// Bills, business links L1 (docs/spec/business-links.md §6): a linked
// supplier's invoice, mirrored into the buyer's business. The seller's
// transaction only notes the invoice as due (migration 00276); the
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
	r.With(manage, s.tenantWide).Post("/bills/{id}/pay", s.payBill)
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

// billErr answers a bill's refusals with their reasons.
func billErr(err error) error {
	switch {
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
	var st billPaySettings
	var b links.Bill
	var version int
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		if err := links.SettleStalePaymentsTx(ctx, tx); err != nil {
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
		b, err = links.GetBillTx(ctx, tx, chi.URLParam(r, "id"))
		return err
	})
	if err != nil {
		s.fail(w, r, billErr(err))
		return
	}
	if b.PayAccountNumber == "" || b.PayBankName == "" {
		s.fail(w, r, fmt.Errorf("%w: %s has not given your business an account number to pay into; pay on the invoice's page instead", errConflict, b.SupplierName))
		return
	}
	code, err := s.billBankCode(r.Context(), p.TenantID, st.Connection, b.PayBankName)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if in.AmountKobo == 0 {
		in.AmountKobo = b.DueKobo
	}
	var pay links.BillPayment
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if pay, err = links.StartBillPaymentTx(r.Context(), tx, p.TenantID, links.PayIn{Bill: b.ID, AmountKobo: in.AmountKobo, Connection: st.Connection,
			BankCode: code, AccountNumber: b.PayAccountNumber, By: p.ActorType() + ":" + p.Actor()}); err != nil {
			return err
		}
		return auditTx(r, tx, "bill.pay", b.Reference, map[string]any{"amount_kobo": in.AmountKobo, "payment": pay.ID})
	}); err != nil {
		s.fail(w, r, billErr(err))
		return
	}
	ref, _, err := s.Store.StartRun(r.Context(), runtime.StartRequest{TenantID: p.TenantID, WorkflowID: *st.WorkflowID, Version: version, Environment: "prod",
		Trigger: map[string]any{"type": "manual", "body": map[string]any{"payment_id": pay.ID.String(), "supplier": b.SupplierName, "reference": b.Reference,
			"token": pay.Token, "bank_code": code, "account_number": b.PayAccountNumber, "amount_kobo": pay.AmountKobo}},
		StartedBy: p.Actor(), TriggerID: "bills/pay", DedupKey: "bill-payment:" + pay.ID.String()})
	if err != nil {
		// Nothing will pay it: the bill is not left paying.
		_ = s.tx(r, func(tx pgx.Tx) error {
			_, ferr := links.FinishBillPaymentTx(r.Context(), tx, pay.ID, "failed", "")
			return ferr
		})
		s.fail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error { return links.SetPaymentRunTx(r.Context(), tx, pay.ID, ref.ID) }); err != nil {
		s.fail(w, r, err)
		return
	}
	pay.RunID = &ref.ID
	writeJSON(w, http.StatusAccepted, billPayOut{Payment: pay, RunID: ref.ID})
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

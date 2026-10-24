package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/access"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/leaks"
	"github.com/israel-duff/taskiem/engine/sender"
)

// Invoices a business sends its customers (decision 0045, T3;
// docs/invoices.md). Not the subscription's invoices (billing.go).

const (
	// PermInvoiceRead lists and reads the business's invoices.
	PermInvoiceRead = access.InvoiceRead
	// PermInvoiceManage creates, sends and voids them, changes their
	// settings and undoes a wrong payment.
	PermInvoiceManage = access.InvoiceManage
)

// invoiceRoutes mounts /v1/invoices and /v1/invoice-settings inside the
// authenticated group.
func (s *Server) invoiceRoutes(r chi.Router) {
	read, manage := s.need(PermInvoiceRead), s.need(PermInvoiceManage)
	r.With(read).Get("/invoices", s.listInvoices)
	r.With(manage, s.tenantWide).Post("/invoices", s.createInvoice)
	r.With(read).Get("/invoices/{id}", s.getCustomerInvoice)
	r.With(manage, s.tenantWide).Put("/invoices/{id}", s.updateInvoice)
	r.With(manage, s.tenantWide).Post("/invoices/{id}/send", s.sendInvoice)
	r.With(manage, s.tenantWide).Post("/invoices/{id}/void", s.voidInvoice)
	r.With(manage, s.tenantWide).Post("/invoices/{id}/amendments", s.amendInvoice) // schools_fees.go
	r.With(manage, s.tenantWide).Post("/invoices/{id}/payments/{payment}/undo", s.undoInvoicePayment)
	r.With(read).Get("/invoice-settings", s.getInvoiceSettings)
	r.With(manage, s.tenantWide).Put("/invoice-settings", s.putInvoiceSettings)
	s.matchingRoutes(r)
	s.moneyRoutes(r)
	s.moneyReportRoutes(r)
}

func invoiceErr(err error) error {
	switch {
	case errors.Is(err, invoicing.ErrInvalid), errors.Is(err, sender.ErrInvalid):
		return fmt.Errorf("%w: %s", errBadRequest, plainInvoiceError(err))
	case errors.Is(err, invoicing.ErrState):
		return fmt.Errorf("%w: %s", errConflict, plainInvoiceError(err))
	case errors.Is(err, invoicing.ErrNotFound):
		return pgx.ErrNoRows
	}
	return err
}

func plainInvoiceError(err error) string {
	msg := err.Error()
	for _, p := range []string{"invoicing: invalid: ", "invoicing: not allowed in the invoice's status: ", "invoicing: ", "sender: invalid message: "} {
		msg = strings.ReplaceAll(msg, p, "")
	}
	return msg
}

func (s *Server) invoicesOn(w http.ResponseWriter, r *http.Request) bool {
	if s.Invoices == nil {
		s.fail(w, r, fmt.Errorf("%w: invoices are not available on this server", errConflict))
		return false
	}
	return true
}

func (s *Server) listInvoices(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	status := r.URL.Query().Get("status")
	switch status {
	case "", "draft", "sent", "part_paid", "paid", "overdue", "void":
	default:
		s.fail(w, r, fmt.Errorf("%w: unknown status %q", errBadRequest, status))
		return
	}
	var list []invoicing.Invoice
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = s.Invoices.List(r.Context(), tx, principalFrom(r.Context()).TenantID, status, limit)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	for i := range list {
		maskInvoice(&list[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"invoices": list})
}

// getCustomerInvoice is one invoice. The customer's details are masked
// unless the caller holds pii.reveal and asks with ?reveal=true, which is
// audited, as for orders and runs (spec 9.4).
func (s *Server) getCustomerInvoice(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	reveal := r.URL.Query().Get("reveal") == "true"
	if reveal && !principalFrom(r.Context()).Can(PermPIIReveal) {
		writeErr(w, http.StatusForbidden, "requires "+PermPIIReveal)
		return
	}
	var x invoicing.Invoice
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, err = s.Invoices.Get(r.Context(), tx, principalFrom(r.Context()).TenantID, chi.URLParam(r, "id"))
		if err != nil || !reveal {
			return invoiceErr(err)
		}
		return auditTx(r, tx, "pii.reveal", "customer_invoice:"+x.Number, nil)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !reveal {
		maskInvoice(&x)
	}
	writeJSON(w, http.StatusOK, map[string]any{"invoice": x, "revealed": reveal})
}

// maskInvoice leaves enough to recognise the customer, as for orders.
func maskInvoice(x *invoicing.Invoice) {
	if r := []rune(x.Customer.Name); len(r) > 0 {
		x.Customer.Name = string(r[0]) + "•••"
	}
	if len(x.Customer.Phone) > 4 {
		x.Customer.Phone = "•••" + x.Customer.Phone[len(x.Customer.Phone)-4:]
	}
	if at := strings.IndexByte(x.Customer.Email, '@'); at > 0 {
		x.Customer.Email = x.Customer.Email[:1] + "•••" + x.Customer.Email[at:]
	}
}

func (s *Server) createInvoice(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	var d invoicing.Draft
	if err := decodeBody(r, &d); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var x invoicing.Invoice
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, err = s.Invoices.CreateOnce(r.Context(), tx, p.TenantID, r.Header.Get("Idempotency-Key"), d, p.Actor())
		return invoiceErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	maskInvoice(&x)
	writeJSON(w, http.StatusCreated, x)
}

func (s *Server) updateInvoice(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	var d invoicing.Draft
	if err := decodeBody(r, &d); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var x invoicing.Invoice
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, err = s.Invoices.Update(r.Context(), tx, p.TenantID, chi.URLParam(r, "id"), d, p.Actor())
		return invoiceErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	maskInvoice(&x)
	writeJSON(w, http.StatusOK, x)
}

// sendInvoice sends (or sends again) an invoice by SMS and email. An
// Idempotency-Key makes a repeated request send nothing more.
func (s *Server) sendInvoice(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	p := principalFrom(r.Context())
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = "api:" + uuid.NewString()
	}
	out, err := s.Invoices.Send(r.Context(), p.TenantID, chi.URLParam(r, "id"), p.Actor(), key)
	if err != nil {
		s.fail(w, r, invoiceErr(err))
		return
	}
	maskInvoice(&out.Invoice)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) voidInvoice(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var x invoicing.Invoice
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, err = s.Invoices.Void(r.Context(), tx, p.TenantID, chi.URLParam(r, "id"), req.Reason, p.Actor())
		return invoiceErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	maskInvoice(&x)
	writeJSON(w, http.StatusOK, x)
}

// undoInvoicePayment takes a payment wrongly counted towards an invoice
// back out of it; both rows stay in its history.
func (s *Server) undoInvoicePayment(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	pid, err := uuid.Parse(chi.URLParam(r, "payment"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	p := principalFrom(r.Context())
	var x invoicing.Invoice
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, err = s.Invoices.UndoPayment(r.Context(), tx, p.TenantID, chi.URLParam(r, "id"), pid, p.Actor())
		return invoiceErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	maskInvoice(&x)
	writeJSON(w, http.StatusOK, x)
}

func (s *Server) getInvoiceSettings(w http.ResponseWriter, r *http.Request) {
	var st invoicing.Settings
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		st, err = invoicing.GetSettings(r.Context(), tx, principalFrom(r.Context()).TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) putInvoiceSettings(w http.ResponseWriter, r *http.Request) {
	var req invoicing.Settings
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	var st invoicing.Settings
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		st, err = invoicing.SaveSettings(r.Context(), tx, principalFrom(r.Context()).TenantID, req)
		if err != nil {
			return invoiceErr(err)
		}
		return auditTx(r, tx, "invoice.settings", "invoices", map[string]any{"prefix": st.Prefix, "connection": st.Connection,
			"fee_paid_by": st.FeePaidBy, "allow_part_payments": st.AllowPartPayments, "auto_match": st.AutoMatch})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// RunInvoices moves invoices past their due date to overdue, hourly (the
// check is cheap and the date turns at midnight in Lagos).
func (s *Server) RunInvoices(ctx context.Context) error {
	if s.Invoices == nil {
		<-ctx.Done()
		return nil
	}
	go s.runInvoicePayments(ctx)
	go s.runSchoolAccounts(ctx)
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		if _, err := s.Invoices.Overdue(ctx); err != nil && ctx.Err() == nil {
			s.Logger.Error("invoices: overdue", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

// runInvoicePayments issues the accounts and links invoice pages and
// chat ask for and starts the runs invoices being paid queued (every
// second), looks their payments up (every 30
// seconds), matches the money that came in (every 10 seconds) and looks
// for leaks (every 15 minutes).
func (s *Server) runInvoicePayments(ctx context.Context) {
	drain, check, match, leak := time.NewTicker(time.Second), time.NewTicker(30*time.Second), time.NewTicker(10*time.Second), time.NewTicker(15*time.Minute)
	defer drain.Stop()
	defer check.Stop()
	defer match.Stop()
	defer leak.Stop()
	detector := &leaks.Detector{Pool: s.Invoices.Pool, Logger: s.Logger}
	for {
		select {
		case <-ctx.Done():
			return
		case <-drain.C:
			if err := s.Invoices.Drain(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("invoices: requests", "err", err)
			}
			if err := s.Invoices.StartPaid(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("invoices: runs on paid", "err", err)
			}
		case <-check.C:
			if err := s.Invoices.CheckPayments(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("invoices: payments", "err", err)
			}
		case <-match.C:
			if err := s.matcher().Tick(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("invoices: matching", "err", err)
			}
		case <-leak.C:
			if err := detector.Tick(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("invoices: leaks", "err", err)
			}
		}
	}
}

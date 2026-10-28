package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/schools"
)

// Fees and term invoices (decision 0046, B3; docs/schools.md#fees): a
// class's fee structure for a term, a student's optional items and
// standing discounts, invoicing the term (a plan first, then exactly once
// per student), the term's invoices, and the discount threshold. A
// discount at or above the threshold answers 409 needs_approval until
// the approvals of B5 replace the refusal with a request.

// schoolFeeRoutes mounts the fee routes inside the Schools group.
func (s *Server) schoolFeeRoutes(r chi.Router) {
	read, manage := s.need(PermSchoolRead), s.need(PermSchoolManage)
	r.With(read).Get("/schools/terms/{id}/fees", s.listSchoolFees)
	r.With(manage, s.tenantWide).Put("/schools/terms/{id}/fees/{class}", s.setSchoolFees)
	r.With(manage, s.tenantWide).Post("/schools/terms/{id}/fees/{class}/copy", s.copySchoolFees)
	r.With(manage, s.tenantWide).Post("/schools/terms/{id}/invoice", s.invoiceSchoolTerm)
	r.With(read).Get("/schools/terms/{id}/invoices", s.listSchoolTermInvoices)
	r.With(read).Get("/schools/students/{id}/fees", s.getSchoolStudentFees)
	r.With(manage, s.tenantWide).Put("/schools/students/{id}/options", s.setSchoolStudentOptions)
	r.With(read).Get("/schools/students/{id}/discounts", s.listSchoolDiscounts)
	r.With(manage, s.tenantWide).Post("/schools/students/{id}/discounts", s.addSchoolDiscount)
	r.With(manage, s.tenantWide).Delete("/schools/students/{id}/discounts/{discount}", s.endSchoolDiscount)
}

// schoolFail answers a discount recorded for a second person as 202
// needs_approval with the request (schools_cash.go), and anything else as
// fail does.
func (s *Server) schoolFail(w http.ResponseWriter, r *http.Request, err error) {
	if approvalNeeded(w, err) {
		return
	}
	s.fail(w, r, schoolErr(err))
}

func (s *Server) listSchoolFees(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var list []schools.ClassFees
	var prev *schools.Term
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		tn := principalFrom(r.Context()).TenantID
		if list, err = s.schoolsSvc().TermFees(r.Context(), tx, tn, id); err != nil {
			return schoolErr(err)
		}
		p, ok, err := s.schoolsSvc().PreviousTerm(r.Context(), tx, tn, id)
		if ok {
			prev = &p
		}
		return schoolErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"classes": list, "previous_term": prev})
}

func feesAudit(f schools.ClassFees) []map[string]any {
	out := []map[string]any{}
	for _, it := range f.Items {
		out = append(out, map[string]any{"name": it.Name, "amount_kobo": it.AmountKobo, "kind": it.Kind, "pass_through_for": it.PassThroughFor})
	}
	return out
}

func (s *Server) setSchoolFees(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	class, err := pathID(r, "class")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req struct {
		Items []schools.FeeItemIn `json:"items"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out schools.FeesSet
	if err := s.tx(r, func(tx pgx.Tx) error {
		before, err := s.schoolsSvc().Fees(r.Context(), tx, p.TenantID, id, class)
		if err != nil {
			return schoolErr(err)
		}
		if out, err = s.schoolsSvc().SetFees(r.Context(), tx, p.TenantID, id, class, req.Items, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.fees.set", id.String()+":"+class.String(), map[string]any{"class": out.Fees.Class,
			"from": feesAudit(before), "to": feesAudit(out.Fees), "drafts_updated": out.DraftsUpdated})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) copySchoolFees(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	class, err := pathID(r, "class")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req struct {
		FromTerm  *uuid.UUID `json:"from_term"`
		FromClass *uuid.UUID `json:"from_class"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out schools.FeesSet
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out, err = s.schoolsSvc().CopyFees(r.Context(), tx, p.TenantID, id, class, req.FromTerm, req.FromClass, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.fees.copy", id.String()+":"+class.String(), map[string]any{"class": out.Fees.Class, "from_term": req.FromTerm,
			"from_class": req.FromClass, "to": feesAudit(out.Fees), "drafts_updated": out.DraftsUpdated})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func maskPlan(p *schools.TermPlan) {
	for _, list := range [][]schools.PlanStudent{p.NoContact, p.NoFees, p.NothingToPay, p.NeedsApproval} {
		for i := range list {
			list[i].Name = schoolName(list[i].Name)
		}
	}
}

// invoiceSchoolTerm serves POST /v1/schools/terms/{id}/invoice: {} answers
// the plan; {confirm: <the plan's hash>, send} invoices exactly that plan,
// one invoice per student once, as drafts or sent at once.
func (s *Server) invoiceSchoolTerm(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req struct {
		Confirm string `json:"confirm"`
		Send    bool   `json:"send"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Confirm != "" && s.Invoices == nil {
		s.fail(w, r, errors.Join(errConflict, errors.New("invoices are not available on this server")))
		return
	}
	p := principalFrom(r.Context())
	if req.Confirm == "" {
		var plan schools.TermPlan
		if err := s.tx(r, func(tx pgx.Tx) error {
			var err error
			plan, err = s.schoolsSvc().PlanTerm(r.Context(), tx, p.TenantID, id)
			return schoolErr(err)
		}); err != nil {
			s.fail(w, r, err)
			return
		}
		maskPlan(&plan)
		writeJSON(w, http.StatusOK, map[string]any{"plan": plan})
		return
	}
	var out schools.TermInvoiced
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out, err = s.schoolsSvc().InvoiceTerm(r.Context(), tx, p.TenantID, id, req.Confirm, req.Send, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.term.invoice", id.String(), map[string]any{"created": out.Created, "total_kobo": out.TotalKobo,
			"to_invoice": out.ToInvoice, "already_invoiced": out.AlreadyInvoiced, "send": req.Send, "plan": out.Hash})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Send {
		sent, failed, err := s.schoolsSvc().SendTerm(r.Context(), p.TenantID, id, p.Actor())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out.Sent, out.Failed = sent, len(failed)
	}
	maskPlan(&out.TermPlan)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listSchoolTermInvoices(w http.ResponseWriter, r *http.Request) {
	rev, ok := revealing(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var class *uuid.UUID
	if c := r.URL.Query().Get("class"); c != "" {
		cid, err := uuid.Parse(c)
		if err != nil {
			s.fail(w, r, errors.Join(errBadRequest, errors.New("class is a class's id")))
			return
		}
		class = &cid
	}
	var list []schools.TermInvoice
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if list, err = s.schoolsSvc().TermInvoices(r.Context(), tx, principalFrom(r.Context()).TenantID, id, class, r.URL.Query().Get("status")); err != nil {
			return schoolErr(err)
		}
		if rev {
			return auditTx(r, tx, "pii.reveal", "school_term_invoices:"+id.String(), map[string]any{"invoices": len(list)})
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !rev {
		for i := range list {
			list[i].Name = schoolName(list[i].Name)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"invoices": list, "revealed": rev})
}

func (s *Server) getSchoolStudentFees(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out schools.StudentFees
	if err := s.tx(r, func(tx pgx.Tx) error {
		term, err := s.termParam(r, tx)
		if err != nil {
			return err
		}
		out, err = s.schoolsSvc().StudentTermFees(r.Context(), tx, p.TenantID, id, term)
		return schoolErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if out.Invoice != nil {
		out.Invoice.Name = schoolName(out.Invoice.Name)
	}
	writeJSON(w, http.StatusOK, out)
}

// termParam is ?term=<id>, else the current term.
func (s *Server) termParam(r *http.Request, tx pgx.Tx) (uuid.UUID, error) {
	if t := r.URL.Query().Get("term"); t != "" {
		id, err := uuid.Parse(t)
		if err != nil {
			return uuid.Nil, errors.Join(errBadRequest, errors.New("term is a term's id"))
		}
		return id, nil
	}
	cur, ok, err := s.schoolsSvc().CurrentTerm(r.Context(), tx, principalFrom(r.Context()).TenantID)
	if err != nil {
		return uuid.Nil, schoolErr(err)
	}
	if !ok {
		return uuid.Nil, errors.Join(errConflict, errors.New("no term is current: name one with ?term="))
	}
	return cur.ID, nil
}

func (s *Server) setSchoolStudentOptions(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req struct {
		Term  uuid.UUID   `json:"term"`
		Items []uuid.UUID `json:"items"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Items == nil {
		req.Items = []uuid.UUID{}
	}
	p := principalFrom(r.Context())
	var out schools.StudentFees
	if err := s.tx(r, func(tx pgx.Tx) error {
		var n int
		var err error
		if out, n, err = s.schoolsSvc().SetOptions(r.Context(), tx, p.TenantID, id, req.Term, req.Items, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.student.options", id.String(), map[string]any{"term": req.Term, "items": req.Items, "drafts_updated": n})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if out.Invoice != nil {
		out.Invoice.Name = schoolName(out.Invoice.Name)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listSchoolDiscounts(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var list []schools.Discount
	var threshold int64
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		tn := principalFrom(r.Context()).TenantID
		if list, err = s.schoolsSvc().Discounts(r.Context(), tx, tn, id); err != nil {
			return schoolErr(err)
		}
		threshold, err = schools.DiscountThreshold(r.Context(), tx, tn)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"discounts": list, "discount_threshold_kobo": threshold})
}

func (s *Server) addSchoolDiscount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in schools.DiscountIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var d schools.Discount
	var n int
	var needs *schools.ApprovalNeeded
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		d, n, err = s.schoolsSvc().AddDiscount(r.Context(), tx, p.TenantID, id, in, p.Actor())
		if errors.As(err, &needs) {
			// The request is recorded in this transaction: it commits.
			return auditTx(r, tx, "school.approval.request", needs.Request.ID.String(), map[string]any{"kind": needs.Request.Kind, "amount_kobo": needs.Request.AmountKobo, "student": id, "reason": in.Reason})
		}
		if err != nil {
			return err
		}
		return auditTx(r, tx, "school.discount.add", id.String(), map[string]any{"discount": d.ID, "kind": d.Kind, "percent": d.Percent,
			"amount_kobo": d.AmountKobo, "reason": d.Reason, "drafts_updated": n})
	}); err != nil {
		s.schoolFail(w, r, err)
		return
	}
	if needs != nil {
		approvalNeeded(w, needs)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"discount": d, "drafts_updated": n})
}

func (s *Server) endSchoolDiscount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	did, err := pathID(r, "discount")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var d schools.Discount
	var n int
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if d, n, err = s.schoolsSvc().EndDiscount(r.Context(), tx, p.TenantID, id, did, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.discount.end", id.String(), map[string]any{"discount": d.ID, "drafts_updated": n})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"discount": d, "drafts_updated": n})
}

// amendInvoice serves POST /v1/invoices/{id}/amendments, for every
// business: a recorded change to a sent invoice with a reason. Where the
// Schools area is on, a discount or a removal at or above the school's
// discount threshold answers 202 needs_approval with the request recorded
// for a second person.
func (s *Server) amendInvoice(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	var in invoicing.AmendmentIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out invoicing.Amended
	var needs *schools.ApprovalNeeded
	err := s.tx(r, func(tx pgx.Tx) error {
		if s.schoolsOn(r.Context(), tx, p.TenantID) {
			// A discount or a line taken out at or above the school's
			// discount threshold (with what the invoice was reduced by
			// already without a second person) is recorded for a second
			// person instead (schools_cash.go); the request commits with
			// this transaction.
			err := s.schoolsSvc().RequestDiscountAmendment(r.Context(), tx, p.TenantID, chi.URLParam(r, "id"), in, p.Actor())
			if errors.As(err, &needs) {
				return auditTx(r, tx, "school.approval.request", needs.Request.ID.String(), map[string]any{"kind": needs.Request.Kind, "amount_kobo": needs.Request.AmountKobo,
					"invoice": needs.Request.InvoiceNumber, "reason": in.Reason})
			}
			if err != nil {
				return invoiceErr(schoolErr(err))
			}
		}
		var err error
		out, err = s.Invoices.Amend(r.Context(), tx, p.TenantID, chi.URLParam(r, "id"), in, p.Actor())
		return invoiceErr(err)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if needs != nil {
		approvalNeeded(w, needs)
		return
	}
	if out.Became {
		if err := s.Invoices.Paid(r.Context(), p.TenantID, out.Invoice); err != nil {
			s.Logger.Warn("invoices: paid signal after an amendment", "invoice", out.Invoice.Number, "err", err)
		}
	}
	maskInvoice(&out.Invoice)
	writeJSON(w, http.StatusCreated, out)
}

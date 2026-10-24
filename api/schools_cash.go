package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/schools"
)

// Cash at the desk, the second person and receipts (docs/spec/schools.md
// 8 and 9, B5; docs/schools.md#cash-and-transfers, #receipts):
// POST /v1/schools/payments posts money a family paid at the desk, or
// records it as pending at or above the cash threshold; the requests
// waiting for a second person are listed at /v1/schools/approvals and
// decided there by people with school.approve who did not ask;
// /v1/schools/receipts lists, finds and renders receipts. A discount at or
// above the discount threshold (schools_fees.go, the amendment route)
// answers 202 needs_approval with the request recorded.

// schoolCashRoutes mounts the routes inside the Schools group.
func (s *Server) schoolCashRoutes(r chi.Router) {
	read, manage, approve := s.need(PermSchoolRead), s.need(PermSchoolManage), s.need(PermSchoolApprove)
	r.With(manage, s.tenantWide).Post("/schools/payments", s.postSchoolPayment)
	r.With(read).Get("/schools/payments", s.listSchoolPayments)
	r.With(approve).Get("/schools/approvals", s.listSchoolApprovals)
	r.With(approve, s.tenantWide).Post("/schools/approvals/{id}", s.decideSchoolApproval)
	r.With(read).Get("/schools/receipts", s.listSchoolReceipts)
	r.With(read).Get("/schools/receipts/{id}", s.getSchoolReceipt)
	r.With(read).Get("/schools/receipts/{id}/pdf", s.getSchoolReceiptPDF)
}

// approvalNeeded answers a request recorded for a second person: 202
// with code needs_approval and the request. ok is false for other errors.
func approvalNeeded(w http.ResponseWriter, err error) bool {
	var an *schools.ApprovalNeeded
	if !errors.As(err, &an) {
		return false
	}
	req := an.Request
	maskPosting(&req)
	writeJSON(w, http.StatusAccepted, map[string]any{"error": schools.Plain(err), "code": "needs_approval", "request": req})
	return true
}

func maskPosting(p *schools.Posting) { p.Name = schoolName(p.Name) }

func maskReceipt(r *schools.Receipt) { r.Name = schoolName(r.Name) }

// postSchoolPayment serves POST /v1/schools/payments.
func (s *Server) postSchoolPayment(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	var in schools.PostingIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	in.Key = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(in.Key) > 200 {
		s.fail(w, r, fmt.Errorf("%w: Idempotency-Key is up to 200 characters", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	posting, parts, err := s.schoolsSvc().PostPayment(r.Context(), p.TenantID, in, p.Actor())
	var an *schools.ApprovalNeeded
	if errors.As(err, &an) {
		if err := s.tx(r, func(tx pgx.Tx) error {
			return auditTx(r, tx, "school.approval.request", an.Request.ID.String(), map[string]any{"kind": an.Request.Kind, "amount_kobo": an.Request.AmountKobo, "student": an.Request.StudentID, "how": in.How})
		}); err != nil {
			s.fail(w, r, err)
			return
		}
		approvalNeeded(w, err)
		return
	}
	if err != nil {
		s.fail(w, r, schoolErr(err))
		return
	}
	if err := s.schoolsSvc().PostedAfter(r.Context(), p.TenantID, parts); err != nil {
		s.Logger.Warn("schools: paid signal after a posting", "posting", posting.ID, "err", err)
	}
	if posting.Created {
		if err := s.tx(r, func(tx pgx.Tx) error {
			return auditTx(r, tx, "school.payment.post", posting.ID.String(), map[string]any{"amount_kobo": posting.AmountKobo, "student": posting.StudentID, "how": in.How, "parts": len(parts)})
		}); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	maskPosting(&posting)
	status := http.StatusCreated
	if !posting.Created {
		status = http.StatusOK // the same key again: the posting as it was
	}
	writeJSON(w, status, map[string]any{"posting": posting})
}

// listSchoolPayments serves GET /v1/schools/payments: cash postings (and
// the other requests), newest first, filtered by status, kind and
// student.
func (s *Server) listSchoolPayments(w http.ResponseWriter, r *http.Request) {
	rev, ok := revealing(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := schools.PostingFilter{Status: q.Get("status"), Kind: q.Get("kind")}
	if v := q.Get("student"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			s.fail(w, r, fmt.Errorf("%w: student is an id", errBadRequest))
			return
		}
		f.Student = &id
	}
	if v := q.Get("limit"); v != "" {
		f.Limit, _ = strconv.Atoi(v)
	}
	p := principalFrom(r.Context())
	var list []schools.Posting
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if list, err = s.schoolsSvc().Postings(r.Context(), tx, p.TenantID, f); err != nil {
			return schoolErr(err)
		}
		if rev {
			return auditTx(r, tx, "pii.reveal", "school_payments", map[string]any{"postings": len(list)})
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	for i := range list {
		list[i].Yours = list[i].RequestedUser != nil && *list[i].RequestedUser == p.Human()
		if !rev {
			maskPosting(&list[i])
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"postings": list, "revealed": rev})
}

// listSchoolApprovals serves GET /v1/schools/approvals: the requests
// waiting for a second person, each saying whether the caller asked for
// it (and so cannot decide it).
func (s *Server) listSchoolApprovals(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var list []schools.Posting
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = s.schoolsSvc().Postings(r.Context(), tx, p.TenantID, schools.PostingFilter{Status: schools.StatePending, Limit: 200})
		if errors.Is(err, schools.ErrOff) {
			list = []schools.Posting{}
			return nil
		}
		return schoolErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	for i := range list {
		list[i].Yours = list[i].RequestedUser != nil && *list[i].RequestedUser == p.Human() || list[i].RequestedBy == p.Actor()
		maskPosting(&list[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": list})
}

// decideSchoolApproval serves POST /v1/schools/approvals/{id}: a person
// with school.approve, not the requester, approves or declines.
func (s *Server) decideSchoolApproval(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "requests are decided by people, not API keys")
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req struct {
		Decision string `json:"decision"` // approved | declined
		Reason   string `json:"reason,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Decision != "approved" && req.Decision != "declined" {
		s.fail(w, r, fmt.Errorf("%w: decision is approved or declined", errBadRequest))
		return
	}
	var d schools.Decision
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if d, err = s.schoolsSvc().Decide(r.Context(), tx, p.TenantID, id, req.Decision == "approved", req.Reason, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.approval.decide", id.String(), map[string]any{"decision": req.Decision, "kind": d.Request.Kind, "amount_kobo": d.Request.AmountKobo,
			"reason": req.Reason, "requested_by": d.Request.RequestedBy, "via": "web"})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.schoolsSvc().DecidedAfter(r.Context(), p.TenantID, d); err != nil {
		s.Logger.Warn("schools: after a decision", "request", id, "err", err)
	}
	req2 := d.Request
	maskPosting(&req2)
	writeJSON(w, http.StatusOK, map[string]any{"request": req2})
}

// listSchoolReceipts serves GET /v1/schools/receipts: by student, term,
// invoice or number, newest first.
func (s *Server) listSchoolReceipts(w http.ResponseWriter, r *http.Request) {
	rev, ok := revealing(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := schools.ReceiptFilter{Number: q.Get("number")}
	for name, into := range map[string]**uuid.UUID{"student": &f.Student, "term": &f.Term, "invoice": &f.Invoice} {
		if v := q.Get(name); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				s.fail(w, r, fmt.Errorf("%w: %s is an id", errBadRequest, name))
				return
			}
			*into = &id
		}
	}
	if v := q.Get("limit"); v != "" {
		f.Limit, _ = strconv.Atoi(v)
	}
	p := principalFrom(r.Context())
	var list []schools.Receipt
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if list, err = s.schoolsSvc().Receipts(r.Context(), tx, p.TenantID, f); err != nil {
			return schoolErr(err)
		}
		if rev {
			return auditTx(r, tx, "pii.reveal", "school_receipts", map[string]any{"receipts": len(list)})
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !rev {
		for i := range list {
			maskReceipt(&list[i])
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"receipts": list, "revealed": rev})
}

// getSchoolReceipt serves GET /v1/schools/receipts/{id}, by id or number.
func (s *Server) getSchoolReceipt(w http.ResponseWriter, r *http.Request) {
	rev, ok := revealing(w, r)
	if !ok {
		return
	}
	p := principalFrom(r.Context())
	var rc schools.Receipt
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if rc, err = s.schoolsSvc().ReceiptByID(r.Context(), tx, p.TenantID, chi.URLParam(r, "id")); err != nil {
			return schoolErr(err)
		}
		if rev {
			return auditTx(r, tx, "pii.reveal", "school_receipt:"+rc.Number, nil)
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !rev {
		maskReceipt(&rc)
	}
	writeJSON(w, http.StatusOK, map[string]any{"receipt": rc, "revealed": rev})
}

// getSchoolReceiptPDF serves GET /v1/schools/receipts/{id}/pdf: the
// receipt as the family gets it, with the student's name, so each
// rendering is in the audit log.
func (s *Server) getSchoolReceiptPDF(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var out []byte
	var rc schools.Receipt
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out, rc, err = s.schoolsSvc().ReceiptPDF(r.Context(), tx, p.TenantID, chi.URLParam(r, "id")); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.receipt.pdf", rc.Number, map[string]any{"student": rc.StudentID, "voided": rc.Voided})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="`+rc.Number+`.pdf"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out) //nolint:gosec // a PDF made here from the school's own record, sent as application/pdf with nosniff
}

// schoolMatcher is the matcher with the receipts hook: money that went
// back voids the receipts of the parts it undoes.
func (s *Server) schoolMatcher(m *matching.Matcher) *matching.Matcher {
	if m.Reversed == nil {
		m.Reversed = s.schoolsSvc().ReceiptsReversed
	}
	if m.Claims == nil {
		m.Claims = s.schoolClaims()
	}
	return m
}

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/saved"
)

// Paying several people (paying people, phase 3): a batch is a list of
// payments, each prepared, checked and sent exactly as one payment is
// (paymentsTx, its own pay-person run, its own key, the run's check), so
// nothing about one person's payment is weaker in a list. A row that
// fails a check is held out, with why, before anything moves; the batch is
// confirmed with one passkey for the batch, and the rows that wait for the
// owner's OK are approved with one. Each row has its own result; the
// failed ones can be paid again as a new batch.

const (
	payOpBatch  = "money.pay.batch"
	maxBatchLen = 100
)

type batchRow struct {
	payIn
}

// batchHeld is a row held out of a batch: what was asked, and why.
type batchHeld struct {
	Row     int    `json:"row"`
	Payee   string `json:"payee"`
	Code    string `json:"code"`
	Problem string `json:"problem"`
}

// batchResult is what became of a row when the batch was sent.
type batchResult struct {
	Payment uuid.UUID `json:"payment"`
	Code    string    `json:"code,omitempty"`
	Problem string    `json:"problem,omitempty"`
}

type batchView struct {
	ID          uuid.UUID     `json:"id"`
	Title       string        `json:"title"`
	Kind        string        `json:"kind"`
	Status      string        `json:"status"`
	CreatedBy   string        `json:"created_by"`
	CreatedAt   time.Time     `json:"created_at"`
	ConfirmedAt *time.Time    `json:"confirmed_at"`
	Payments    []payment     `json:"payments"`
	TotalKobo   int64         `json:"total_kobo"`
	Count       int           `json:"count"`
	Held        []batchHeld   `json:"held,omitempty"`
	Results     []batchResult `json:"results,omitempty"`
	// Paid, waiting, failed: how the rows stand, for the diary line.
	PaidKobo int64 `json:"paid_kobo"`
	Waiting  int   `json:"waiting"`
	Failed   int   `json:"failed"`
}

func (s *Server) paymentBatchRoutes(r chi.Router) {
	pay := s.need(PermMoneyPay)
	r.With(pay).Get("/payment-batches", s.listBatches)
	r.With(pay, s.tenantWide).Post("/payment-batches", s.createBatch)
	r.With(pay).Post("/payment-batches/read", s.readBatchList)
	r.With(pay).Get("/payment-batches/{id}", s.getBatch)
	r.With(pay, s.tenantWide).Post("/payment-batches/{id}/confirm", s.confirmBatch)
	r.With(pay, s.tenantWide).Post("/payment-batches/{id}/approve", s.approveBatch)
	r.With(pay, s.tenantWide).Post("/payment-batches/{id}/cancel", s.cancelBatch)
	r.With(pay, s.tenantWide).Post("/payment-batches/{id}/again", s.batchAgain)
}

// batchTx reads a batch with its payments.
func batchTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, owner bool) (batchView, error) {
	var b batchView
	if err := tx.QueryRow(ctx, `SELECT id, title, kind, status, created_by, created_at, confirmed_at, held FROM payment_batches WHERE id = $1`, id).
		Scan(&b.ID, &b.Title, &b.Kind, &b.Status, &b.CreatedBy, &b.CreatedAt, &b.ConfirmedAt, &b.Held); err != nil {
		return b, err
	}
	rows, err := tx.Query(ctx, `SELECT `+paymentCols+` FROM payments WHERE batch_id = $1 AND (NOT personal OR $2) ORDER BY created_at, id`, id, owner)
	if err != nil {
		return b, err
	}
	if b.Payments, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (payment, error) { return scanPayment(r) }); err != nil {
		return b, err
	}
	if b.Payments == nil {
		b.Payments = []payment{}
	}
	for _, x := range b.Payments {
		if x.Status == "cancelled" || x.Status == "rejected" {
			continue
		}
		b.TotalKobo += x.AmountKobo
		b.Count++
		switch x.Status {
		case "paid":
			b.PaidKobo += x.AmountKobo
		case "waiting_ok":
			b.Waiting++
		case "failed":
			b.Failed++
		}
	}
	return b, nil
}

func (s *Server) batchID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return uuid.Nil, false
	}
	return id, true
}

func isOwnerOf(p *Principal) bool { return slices.Contains(p.Roles, "owner") }

// createBatch is POST /v1/payment-batches {title?, kind?, rows | copy_of,
// from?}: each row prepared as one payment is (up to 100), those failing
// a check held out with why. Nothing is sent.
func (s *Server) createBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Title  string     `json:"title"`
		Kind   string     `json:"kind"`
		Rows   []batchRow `json:"rows"`
		CopyOf *uuid.UUID `json:"copy_of"`
		From   string     `json:"from"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(ctx)
	owner := isOwnerOf(p)
	if in.Kind == "" {
		in.Kind = "list"
	}
	if !slices.Contains([]string{"list", "riders", "messages"}, in.Kind) {
		s.payFail(w, r, refusePay(http.StatusBadRequest, "kind", "A batch is a list, riders, or from messages."))
		return
	}
	if len([]rune(in.Title)) > 80 {
		s.payFail(w, r, refusePay(http.StatusBadRequest, "title", "The title is too long."))
		return
	}
	if in.CopyOf != nil {
		// Last time's again: its rows, as they were asked for.
		// Only the rows that went: not those rejected, cancelled or failed
		// (Pay the failed ones again is its own).
		rows, title, kind, err := s.batchRowsOf(ctx, p.TenantID, *in.CopyOf, owner, []string{"paid", "sending", "unknown", "waiting_ok"})
		if err != nil {
			s.payFail(w, r, err)
			return
		}
		in.Rows, in.Kind = rows, kind
		if in.Title == "" {
			in.Title = title
		}
	}
	if len(in.Rows) == 0 {
		s.payFail(w, r, refusePay(http.StatusBadRequest, "rows", "Add someone to pay."))
		return
	}
	if len(in.Rows) > maxBatchLen {
		s.payFail(w, r, refusePay(http.StatusBadRequest, "rows", fmt.Sprintf("Up to %d people a batch.", maxBatchLen)))
		return
	}
	from := in.From
	if from == "" {
		from = "tools"
	}
	s.prepareBatch(w, r, in.Rows, strings.TrimSpace(in.Title), in.Kind, in.CopyOf, from)
}

// rowLabel is how a held row is named back: its saved name, or the
// account masked.
func rowLabel(in payIn) string {
	switch {
	case in.Payee != "":
		return "@" + strings.TrimPrefix(in.Payee, "@")
	case in.AccountNumber != "":
		return strings.TrimSpace(in.BankName + " " + saved.Masked(in.AccountNumber))
	case in.RiderID != nil:
		return "a rider"
	case in.StaffID != nil:
		return "a staff member"
	case in.Mine:
		return "your own account"
	}
	return "a saved account"
}

// batchRowsOf are a batch's rows as they were asked for (only those whose
// status is in keep, when given): a saved account or a rider by its id,
// an owner's own, or an account number (opened from the payment's seal).
func (s *Server) batchRowsOf(ctx context.Context, tenant, id uuid.UUID, owner bool, keep []string) ([]batchRow, string, string, error) {
	var rows []batchRow
	var title, kind string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT title, kind FROM payment_batches WHERE id = $1`, id).Scan(&title, &kind); err != nil {
			return err
		}
		list, err := tx.Query(ctx, `SELECT saved_id, rider_id, staff_id, personal, bank_name, account_sealed, amount_kobo, purpose, purpose_note, note, status
			FROM payments WHERE batch_id = $1 AND (NOT personal OR $2) ORDER BY created_at, id`, id, owner)
		if err != nil {
			return err
		}
		type raw struct {
			saved, rider, staff *uuid.UUID
			personal            bool
			bank                string
			sealed              map[string]any
			amount              int64
			purpose, pn, note   string
			status              string
		}
		got, err := pgx.CollectRows(list, func(r pgx.CollectableRow) (raw, error) {
			var x raw
			return x, r.Scan(&x.saved, &x.rider, &x.staff, &x.personal, &x.bank, &x.sealed, &x.amount, &x.purpose, &x.pn, &x.note, &x.status)
		})
		if err != nil {
			return err
		}
		for _, x := range got {
			if keep != nil && !slices.Contains(keep, x.status) {
				continue
			}
			in := payIn{SavedID: x.saved, RiderID: x.rider, StaffID: x.staff, Mine: x.personal, AmountKobo: x.amount, Purpose: x.purpose, PurposeNote: x.pn, Note: x.note}
			if x.staff != nil {
				// Paid again to whichever account they are linked to now.
				in.SavedID = nil
			}
			if x.saved == nil && x.rider == nil && x.staff == nil && !x.personal {
				v, err := s.Store.PII.OpenTx(ctx, tx, tenant, x.sealed)
				if err != nil {
					return err
				}
				in.AccountNumber, in.BankName = fmt.Sprint(v), x.bank
			}
			rows = append(rows, batchRow{in})
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", "", refusePay(http.StatusNotFound, "no_batch", "There is no such batch.")
	}
	return rows, title, kind, err
}

// confirmBatch is POST /v1/payment-batches/{id}/confirm {passkey, again?}:
// one passkey for the batch; each row still prepared is then claimed and
// sent as one payment is (within the payer's limit at once; over it, or to
// a changed account or one staff never paid, waiting for the owner's OK).
// A row refused now (the same payment again without again) stays prepared,
// said in the results; one whose account (or the business's payments
// account) changed since it was prepared is cancelled, said
// account_changed (provider_changed), to be prepared again.
func (s *Server) confirmBatch(w http.ResponseWriter, r *http.Request) {
	id, ok := s.batchID(w, r)
	if !ok {
		return
	}
	var in batchConfirmIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	owner := isOwnerOf(p)
	var list []uuid.UUID
	if err := s.tx(r, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM payment_batches WHERE id = $1`, id).Scan(&status); err != nil {
			return err
		}
		if status != "prepared" {
			return refusePay(http.StatusConflict, "not_prepared", "This batch is "+status+".")
		}
		rows, err := tx.Query(ctx, `SELECT id FROM payments WHERE batch_id = $1 AND status = 'prepared' AND (NOT personal OR $2) ORDER BY created_at, id`, id, owner)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	}); err != nil {
		s.payFail(w, r, err)
		return
	}
	if len(list) == 0 {
		s.payFail(w, r, refusePay(http.StatusConflict, "nothing_to_send", "Nothing in this batch is left to send."))
		return
	}
	// The rows the person saw, exactly: none added meanwhile.
	if !sameIDs(list, in.Payments) {
		s.payFail(w, r, refusePay(http.StatusConflict, "changed", "The batch changed since it was shown: look at it again."))
		return
	}
	method, ok := s.payPasskey(w, r, stepUpProof{TOTP: in.TOTP, Passkey: in.Passkey}, payOpBatch, "batch:"+id.String(), "Payments", "paying")
	if !ok {
		return
	}
	r = r.WithContext(context.WithoutCancel(ctx))
	ctx = r.Context()
	var results []batchResult
	for _, pid := range list {
		var x payment
		var needs string
		err := s.tx(r, func(tx pgx.Tx) error {
			var err error
			x, needs, err = confirmCheckTx(ctx, tx, p, owner, pid, slices.Contains(in.Again, pid), true)
			return err
		})
		var pr payRefused
		switch {
		case errors.As(err, &pr):
			results = append(results, batchResult{Payment: pid, Code: pr.code, Problem: pr.msg})
			continue
		case err != nil:
			s.Logger.Warn("payments: checking a batch's row", "batch", id, "payment", pid, "err", err)
			results = append(results, batchResult{Payment: pid, Code: "not_sent", Problem: "It could not be sent just now: send the batch again."})
			continue
		}
		run, err := s.sendPayment(r, x, needs, p, method)
		switch {
		case errors.As(err, &pr):
			results = append(results, batchResult{Payment: pid, Code: pr.code, Problem: pr.msg})
		case err != nil && run == uuid.Nil:
			s.Logger.Warn("payments: sending a batch's row", "batch", id, "payment", pid, "err", err)
			results = append(results, batchResult{Payment: pid, Code: "not_sent", Problem: "It could not be sent just now: send the batch again."})
		case err != nil:
			// The run started and the payer's own OK (tried twice) did not
			// reach it: nobody else can give it, so it is said as it is;
			// the payer confirms the payment again on its own to give it.
			s.Logger.Warn("payments: approving a batch's row", "batch", id, "payment", pid, "err", err)
			results = append(results, batchResult{Payment: pid, Code: "not_approved",
				Problem: "Nothing was paid to them yet: your OK did not reach this payment. Confirm it again on its own in a moment."})
		default:
			results = append(results, batchResult{Payment: pid})
		}
	}
	var out batchView
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE payment_batches SET status = 'sent', confirmed_by = $2, confirmed_at = now() WHERE id = $1 AND status = 'prepared'
			AND NOT EXISTS (SELECT 1 FROM payments WHERE batch_id = $1 AND status = 'prepared')`, id, p.Actor()); err != nil {
			return err
		}
		var err error
		if out, err = batchTx(ctx, tx, id, owner); err != nil {
			return err
		}
		return auditTx(r, tx, "payment.batch_confirm", id.String(), map[string]any{"rows": len(list), "total_kobo": out.TotalKobo, "waiting": out.Waiting})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	out.Results = results
	writeJSON(w, http.StatusAccepted, map[string]any{"batch": out})
}

// batchConfirmIn is a batch confirmed or approved: the passkey, the rows
// the person saw (acted on exactly), and those they said pay again to.
type batchConfirmIn struct {
	Passkey  *credentialJSON `json:"passkey"`
	TOTP     string          `json:"totp"`
	Payments []uuid.UUID     `json:"payments"`
	Again    []uuid.UUID     `json:"again"`
}

// sameIDs reports whether a and b hold the same ids.
func sameIDs(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

// approveBatch is POST /v1/payment-batches/{id}/approve {passkey}: the
// owner's OK for every row of the batch waiting for it, with one passkey.
// Each row's run checks as one payment's does that it was approved by an
// owner who is not its payer.
func (s *Server) approveBatch(w http.ResponseWriter, r *http.Request) {
	id, ok := s.batchID(w, r)
	if !ok {
		return
	}
	var in batchConfirmIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	if !isOwnerOf(p) {
		s.payFail(w, r, refusePay(http.StatusForbidden, "owner_only", "Only an owner gives the OK."))
		return
	}
	type waiting struct {
		ID          uuid.UUID
		Run         uuid.UUID
		ConfirmedBy string
		NeedsOK     string
	}
	var list []waiting
	if err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, run_id, COALESCE(confirmed_by, ''), needs_ok FROM payments WHERE batch_id = $1 AND status = 'waiting_ok' AND run_id IS NOT NULL ORDER BY created_at, id`, id)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, pgx.RowToStructByPos[waiting])
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(list) == 0 {
		s.payFail(w, r, refusePay(http.StatusConflict, "nothing_waiting", "Nothing in this batch waits for your OK."))
		return
	}
	ids := make([]uuid.UUID, len(list))
	for i, x := range list {
		ids[i] = x.ID
	}
	if !sameIDs(ids, in.Payments) {
		s.payFail(w, r, refusePay(http.StatusConflict, "changed", "What waits changed since it was shown: look at it again."))
		return
	}
	method, ok := s.payPasskey(w, r, stepUpProof{TOTP: in.TOTP, Passkey: in.Passkey}, payOpApprove, "batch:"+id.String(), "Approvals", "approving payments")
	if !ok {
		return
	}
	r = r.WithContext(context.WithoutCancel(ctx))
	ctx = r.Context()
	var results []batchResult
	for _, x := range list {
		if strings.Contains(x.NeedsOK, "changed") {
			// A changed account is the owner's to look at alone.
			results = append(results, batchResult{Payment: x.ID, Code: "changed", Problem: "The account changed since it was last paid: call them on a number you know, then approve it in Approvals."})
			continue
		}
		if x.ConfirmedBy == p.Actor() {
			results = append(results, batchResult{Payment: x.ID, Code: "own", Problem: "You confirmed this one: another owner gives its OK."})
			continue
		}
		run := x.Run
		vote := runtime.Vote{UserID: p.UserID, Roles: p.Roles, Decision: "approved", Channel: "web", IP: clientIP(r), StepUp: method,
			Audit: func(tx pgx.Tx, res runtime.VoteResult) error {
				return auditTx(r, tx, "approval.decide", run.String()+"/approve", map[string]any{"decision": "approved", "result": res.Status, "via": "payment.batch_approve"})
			}}
		if _, err := s.Store.VoteApproval(ctx, runtime.RunRef{ID: run, TenantID: p.TenantID}, "approve", vote); err != nil {
			results = append(results, batchResult{Payment: x.ID, Code: "not_approved", Problem: "It could not be approved just now: approve it in Approvals."})
			s.Logger.Warn("payments: approving a batch's row", "batch", id, "payment", x.ID, "err", err)
			continue
		}
		results = append(results, batchResult{Payment: x.ID})
	}
	var out batchView
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = batchTx(ctx, tx, id, true)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	out.Results = results
	writeJSON(w, http.StatusOK, map[string]any{"batch": out})
}

// cancelBatch is POST /v1/payment-batches/{id}/cancel: the rows not sent
// yet are let go.
func (s *Server) cancelBatch(w http.ResponseWriter, r *http.Request) {
	id, ok := s.batchID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var out batchView
	if err := s.tx(r, func(tx pgx.Tx) error {
		// After any confirm claiming a row: a row claimed but not sent is
		// then cancelled with the batch, not given back (sendPayment).
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "payments:"+principalFrom(ctx).TenantID.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE payments SET status = 'cancelled', finished_at = now() WHERE batch_id = $1 AND status = 'prepared' AND (NOT personal OR $2)`,
			id, isOwnerOf(principalFrom(ctx))); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE payment_batches SET status = 'cancelled' WHERE id = $1 AND status = 'prepared'
			AND NOT EXISTS (SELECT 1 FROM payments WHERE batch_id = $1 AND status = 'prepared')`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			if out, err = batchTx(ctx, tx, id, isOwnerOf(principalFrom(ctx))); err != nil {
				return err
			}
			return refusePay(http.StatusConflict, "not_prepared", "This batch was "+out.Status+" already.")
		}
		if out, err = batchTx(ctx, tx, id, isOwnerOf(principalFrom(ctx))); err != nil {
			return err
		}
		return auditTx(r, tx, "payment.batch_cancel", id.String(), nil)
	}); err != nil {
		s.payFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"batch": out})
}

// batchAgain is POST /v1/payment-batches/{id}/again: the rows that failed
// (or the bank did not answer for), prepared again as a new batch.
func (s *Server) batchAgain(w http.ResponseWriter, r *http.Request) {
	id, ok := s.batchID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	rows, title, kind, err := s.batchRowsOf(ctx, p.TenantID, id, isOwnerOf(p), []string{"failed"})
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	if len(rows) == 0 {
		s.payFail(w, r, refusePay(http.StatusConflict, "nothing_failed", "Nothing in this batch failed."))
		return
	}
	s.prepareBatch(w, r, rows, strings.TrimSpace(title+" (again)"), kind, &id, "tools")
}

// prepareBatch prepares rows as a new batch and answers it (201). The
// batch is "preparing" until every row is in, so it cannot be confirmed
// half made; it goes on if the person goes away meanwhile.
func (s *Server) prepareBatch(w http.ResponseWriter, r *http.Request, rows []batchRow, title, kind string, copyOf *uuid.UUID, from string) {
	out, _, err := s.prepareBatchView(r, rows, title, kind, copyOf, from)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"batch": out})
}

// prepareBatchView prepares a batch's rows (each as one payment is, those
// failing a check held out) and answers the batch, with the payment each
// row became (uuid.Nil for a row held out).
func (s *Server) prepareBatchView(r *http.Request, rows []batchRow, title, kind string, copyOf *uuid.UUID, from string) (batchView, []uuid.UUID, error) {
	r = r.WithContext(context.WithoutCancel(r.Context()))
	ctx := r.Context()
	p := principalFrom(ctx)
	owner := isOwnerOf(p)
	if len([]rune(title)) > 80 {
		title = string([]rune(title)[:80])
	}
	id := uuid.Must(uuid.NewV7())
	if err := s.tx(r, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO payment_batches (id, tenant_id, title, kind, copy_of, status, created_by) VALUES ($1, $2, $3, $4, $5, 'preparing', $6)`,
			id, p.TenantID, title, kind, copyOf, p.Actor())
		return err
	}); err != nil {
		return batchView{}, nil, err
	}
	var held []batchHeld
	made := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		row.From = from
		x, err := s.preparePayment(ctx, p.TenantID, p.UserID, owner, row.payIn, p.Actor())
		var pr payRefused
		if errors.As(err, &pr) {
			held = append(held, batchHeld{Row: i, Payee: rowLabel(row.payIn), Code: pr.code, Problem: pr.msg})
			continue
		}
		if err != nil {
			s.Logger.Warn("payments: preparing a batch's row", "batch", id, "row", i, "err", err)
			held = append(held, batchHeld{Row: i, Payee: rowLabel(row.payIn), Code: "not_prepared", Problem: "It could not be checked just now: add it again."})
			continue
		}
		if err := s.tx(r, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE payments SET batch_id = $2 WHERE id = $1`, x.ID, id)
			return err
		}); err != nil {
			return batchView{}, nil, err
		}
		made[i] = x.ID
	}
	var out batchView
	if err := s.tx(r, func(tx pgx.Tx) error {
		// Every row held out: nothing to send, so the batch is let go.
		if held == nil {
			held = []batchHeld{}
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_batches SET status = CASE WHEN EXISTS (SELECT 1 FROM payments WHERE batch_id = $1) THEN 'prepared' ELSE 'cancelled' END,
			held = $2 WHERE id = $1 AND status = 'preparing'`, id, held); err != nil {
			return err
		}
		var err error
		if out, err = batchTx(ctx, tx, id, owner); err != nil {
			return err
		}
		return auditTx(r, tx, "payment.batch_prepare", id.String(), map[string]any{"rows": len(rows), "held": len(held), "total_kobo": out.TotalKobo})
	}); err != nil {
		return batchView{}, nil, err
	}
	return out, made, nil
}

// listBatches is GET /v1/payment-batches: the newest first.
func (s *Server) listBatches(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	owner := isOwnerOf(principalFrom(ctx))
	var out []batchView
	if err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT b.id, b.title, b.kind, b.status, b.created_by, b.created_at, b.confirmed_at,
			COALESCE(sum(p.amount_kobo) FILTER (WHERE p.status NOT IN ('cancelled', 'rejected')), 0),
			count(p.id) FILTER (WHERE p.status NOT IN ('cancelled', 'rejected')),
			COALESCE(sum(p.amount_kobo) FILTER (WHERE p.status = 'paid'), 0),
			count(p.id) FILTER (WHERE p.status = 'waiting_ok'), count(p.id) FILTER (WHERE p.status = 'failed')
			FROM payment_batches b LEFT JOIN payments p ON p.batch_id = b.id AND (NOT p.personal OR $1)
			GROUP BY b.id HAVING $1 OR count(p.id) > 0 OR NOT EXISTS (SELECT 1 FROM payments x WHERE x.batch_id = b.id)
			ORDER BY b.created_at DESC LIMIT 50`, owner)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (batchView, error) {
			b := batchView{Payments: []payment{}}
			return b, r.Scan(&b.ID, &b.Title, &b.Kind, &b.Status, &b.CreatedBy, &b.CreatedAt, &b.ConfirmedAt, &b.TotalKobo, &b.Count, &b.PaidKobo, &b.Waiting, &b.Failed)
		})
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if out == nil {
		out = []batchView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"batches": out})
}

// getBatch is GET /v1/payment-batches/{id}: the batch with each row.
func (s *Server) getBatch(w http.ResponseWriter, r *http.Request) {
	id, ok := s.batchID(w, r)
	if !ok {
		return
	}
	var out batchView
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = batchTx(r.Context(), tx, id, isOwnerOf(principalFrom(r.Context())))
		return err
	}); err != nil {
		s.payFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"batch": out})
}

// batchLine is one line of a pasted list, read for a payment.
type batchLine struct {
	Line          int    `json:"line"`
	Text          string `json:"text"`
	AccountNumber string `json:"account_number,omitempty"`
	BankName      string `json:"bank_name,omitempty"`
	AmountKobo    int64  `json:"amount_kobo,omitempty"`
	Name          string `json:"name,omitempty"`
	// Payee: the business's saved account with that number, by name.
	Payee   string `json:"payee,omitempty"`
	Problem string `json:"problem,omitempty"`
}

// readBatchList is POST /v1/payment-batches/read {text}: a pasted list,
// one person a line ("Chidi 0123456789 GTBank 45k"), each line read for a
// row to check; a line that cannot be read is given back as it was, with
// why. Nothing is prepared.
func (s *Server) readBatchList(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
		// From is "photo" for text the phone read from a photo: letters
		// read in place of digits are put right first.
		From string `json:"from"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.From == "photo" {
		in.Text = saved.PhotoText(in.Text)
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	if !s.limiter("pay-read:"+p.Actor(), time.Minute/30, 30).Allow() {
		s.payFail(w, r, refusePay(http.StatusTooManyRequests, "too_many", "Too many lists read just now: wait a minute."))
		return
	}
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(in.Text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	if len(lines) > maxBatchLen {
		s.payFail(w, r, refusePay(http.StatusBadRequest, "rows", fmt.Sprintf("Up to %d people a batch.", maxBatchLen)))
		return
	}
	out := make([]batchLine, 0, len(lines))
	for i, l := range lines {
		bl := batchLine{Line: i + 1, Text: l}
		rd := saved.Read(l)
		number := rd.Details["account_number"]
		if number == "" {
			// In a list to pay, ten digits on a line are its account.
			for _, run := range append(strings.Fields(l), payDigitRun.FindAllString(l, -1)...) {
				if n, err := saved.AccountNumber(run); err == nil {
					number = n
					if b := saved.BankNamed(l); b != "" {
						rd.Details["bank_name"] = b
					}
					break
				}
			}
		}
		if number == "" {
			bl.Problem = "No account number of 10 digits on this line."
			out = append(out, bl)
			continue
		}
		bl.AccountNumber, bl.BankName, bl.Name = number, rd.Details["bank_name"], rd.Title
		bl.AmountKobo = readPayAmount(l, number)
		if bl.Name == "" {
			bl.Name = lineName(l, number)
		}
		switch {
		case bl.BankName == "":
			bl.Problem = "Which bank? None is named on this line."
		case bl.AmountKobo == 0:
			bl.Problem = "How much? No amount on this line (like 45k)."
		}
		out = append(out, bl)
	}
	// Saved already: each line's account by its saved name.
	o := savedOwner{tenant: p.TenantID, by: p.UserID}
	if err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		where, who := ownerWhere(o)
		rows, err := tx.Query(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE `+where+` AND kind = $2`, who, saved.KindBankAccount)
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (savedRow, error) { return scanSaved(r) })
		if err != nil {
			return err
		}
		byLast4 := map[string][]savedRow{}
		for _, x := range list {
			if l4, _ := x.Details["last4"].(string); l4 != "" {
				byLast4[l4] = append(byLast4[l4], x)
			}
		}
		for i := range out {
			if out[i].AccountNumber == "" || !s.mayMatchSaved(p) {
				continue
			}
			for _, x := range byLast4[saved.Last4(out[i].AccountNumber)] {
				n, err := s.openSaved(ctx, tx, o, x.ID, x.Sealed)
				if err != nil {
					return err
				}
				if n == out[i].AccountNumber {
					out[i].Payee = x.Name
					break
				}
			}
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": out})
}

// lineName is the words of a list's line that are not its number, bank or
// amount: the person's name ("Chidi" in "Chidi 0123456789 GTBank 45k").
func lineName(line, number string) string {
	var words []string
	for _, w := range strings.Fields(line) {
		clean := strings.Trim(w, ",.;:-–")
		if clean == "" || strings.ContainsAny(clean, "0123456789₦") || saved.BankNamed(clean) != "" {
			continue
		}
		lower := strings.ToLower(clean)
		if slices.Contains([]string{"bank", "plc", "naira", "for", "to", "pay", "send", "acct", "account", "no", "number"}, lower) {
			continue
		}
		words = append(words, clean)
	}
	name := strings.Join(words, " ")
	if len([]rune(name)) > 80 {
		name = string([]rune(name)[:80])
	}
	return name
}

// mayMatchSaved spends one of the few matches an hour a person who may not
// see saved numbers gets: a number pasted is told back by its saved name
// only so often, so saved numbers cannot be found by guessing.
func (s *Server) mayMatchSaved(p *Principal) bool {
	if isOwnerOf(p) || p.Can(PermSavedManage) {
		return true
	}
	return s.limiter("pay-match:"+p.Actor(), time.Hour/100, 100).Allow()
}

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/leaks"
	"github.com/israel-duff/taskiem/engine/moneylog"
)

// Leaks, statements, cash and expenses (decision 0045, T6; docs/leaks.md,
// docs/money-log.md): the leaks Taskiem found and closing them, the fees
// agreed with each connection, uploading a statement, and entering cash
// and expenses. The same permissions as invoices: invoice.read to see,
// invoice.manage to act.

func (s *Server) moneyRoutes(r chi.Router) {
	read, manage := s.need(PermInvoiceRead), s.need(PermInvoiceManage)
	r.With(read).Get("/money/leaks", s.listLeaks)
	r.With(manage, s.tenantWide).Post("/money/leaks/{id}/assign", s.assignLeak)
	r.With(manage, s.tenantWide).Post("/money/leaks/{id}/close", s.closeLeak)
	r.With(read).Get("/money/fee-rates", s.listFeeRates)
	r.With(manage, s.tenantWide).Put("/money/fee-rates", s.putFeeRate)
	r.With(read).Get("/money/statement-format", s.getStatementFormat)
	r.With(manage, s.tenantWide).Post("/money/statements", s.uploadStatement)
	r.With(manage, s.tenantWide).Post("/money/entries", s.enterMoney)
}

func (s *Server) moneyLog() *moneylog.Log {
	if s.Invoices != nil && s.Invoices.MoneyLog != nil {
		return s.Invoices.MoneyLog
	}
	return &moneylog.Log{Pool: s.Store.Pool, PII: s.Store.PII}
}

func leakErr(err error) error {
	switch {
	case errors.Is(err, leaks.ErrInvalid), errors.Is(err, moneylog.ErrInvalid):
		msg := err.Error()
		for _, p := range []string{"leaks: invalid: ", "moneylog: invalid entry: "} {
			msg = strings.TrimPrefix(msg, p)
		}
		return fmt.Errorf("%w: %s", errBadRequest, msg)
	case errors.Is(err, leaks.ErrState):
		return fmt.Errorf("%w: %s", errConflict, strings.TrimPrefix(err.Error(), "leaks: "))
	case errors.Is(err, leaks.ErrNotFound):
		return pgx.ErrNoRows
	}
	return err
}

func (s *Server) listLeaks(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && status != "open" && status != "closed" && status != "all" {
		s.fail(w, r, fmt.Errorf("%w: status is open, closed or all", errBadRequest))
		return
	}
	var list []leaks.Leak
	var tot leaks.Totals
	tenant := principalFrom(r.Context()).TenantID
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if list, err = leaks.List(r.Context(), tx, tenant, status, 200); err != nil {
			return err
		}
		tot, err = leaks.Sum(r.Context(), tx, tenant)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"leaks": list, "totals": tot})
}

func (s *Server) assignLeak(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var req struct {
		Member *uuid.UUID `json:"member"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	member := uuid.Nil
	if req.Member != nil {
		member = *req.Member
	}
	var l leaks.Leak
	if err := s.tx(r, func(tx pgx.Tx) error {
		if member != uuid.Nil {
			var ok bool
			if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM memberships WHERE tenant_id = $1 AND user_id = $2)`,
				principalFrom(r.Context()).TenantID, member).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: member is not a member of this organisation", errBadRequest)
			}
		}
		var err error
		l, err = leaks.Assign(r.Context(), tx, principalFrom(r.Context()).TenantID, id, member)
		if err != nil {
			return leakErr(err)
		}
		return auditTx(r, tx, "money.leak_assigned", id.String(), map[string]any{"member": req.Member})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) closeLeak(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var req struct {
		Status        string `json:"status"`
		RecoveredKobo *int64 `json:"recovered_kobo"`
		Note          string `json:"note"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var l leaks.Leak
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		l, err = leaks.Close(r.Context(), tx, p.TenantID, id, req.Status, req.RecoveredKobo, strings.TrimSpace(req.Note), p.Actor(), time.Now())
		if err != nil {
			return leakErr(err)
		}
		return auditTx(r, tx, "money.leak_closed", id.String(), map[string]any{"status": req.Status, "recovered_kobo": req.RecoveredKobo})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) listFeeRates(w http.ResponseWriter, r *http.Request) {
	var list []leaks.FeeRate
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = leaks.FeeRates(r.Context(), tx, principalFrom(r.Context()).TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fee_rates": list})
}

func (s *Server) putFeeRate(w http.ResponseWriter, r *http.Request) {
	var req leaks.FeeRate
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := leaks.SetFeeRate(r.Context(), tx, principalFrom(r.Context()).TenantID, req); err != nil {
			return leakErr(err)
		}
		return auditTx(r, tx, "money.fee_rate", req.Channel, map[string]any{"percent_bp": req.PercentBP, "flat_kobo": req.FlatKobo, "cap_kobo": req.CapKobo})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) getStatementFormat(w http.ResponseWriter, r *http.Request) {
	channel := r.URL.Query().Get("channel")
	var cols moneylog.StatementFormat
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		cols, err = moneylog.StatementColumns(r.Context(), tx, principalFrom(r.Context()).TenantID, channel)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channel": channel, "columns": cols})
}

// uploadStatement serves POST /v1/money/statements: {channel, csv,
// columns?}. Without columns, the channel's last ones (or the common
// format's) are used. A file Taskiem cannot read whole adds nothing and
// answers 422 with what is wrong.
func (s *Server) uploadStatement(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channel string                    `json:"channel"`
		CSV     string                    `json:"csv"`
		Columns *moneylog.StatementFormat `json:"columns"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, moneylog.MaxStatementBytes+64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.fail(w, r, errors.Join(errBadRequest, err))
		return
	}
	p := principalFrom(r.Context())
	cols := moneylog.CommonColumns
	if req.Columns != nil {
		cols = *req.Columns
	} else if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		cols, err = moneylog.StatementColumns(r.Context(), tx, p.TenantID, req.Channel)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	lines, err := moneylog.ParseStatement(strings.NewReader(req.CSV), cols)
	var se *moneylog.StatementError
	if errors.As(err, &se) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": se.Message, "code": "statement_unreadable", "missing": se.Missing, "problems": se.Problems})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	got, err := s.moneyLog().ImportStatement(r.Context(), p.TenantID, req.Channel, p.Actor(), cols, lines)
	if err != nil {
		s.fail(w, r, leakErr(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channel": req.Channel, "added": got.Added, "skipped": got.Skipped, "columns": cols})
}

// enterMoney serves POST /v1/money/entries: cash or an expense, once per
// Idempotency-Key.
func (s *Server) enterMoney(w http.ResponseWriter, r *http.Request) {
	var req struct {
		moneylog.Entered
		OccurredAt string `json:"occurred_at"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.OccurredAt != "" {
		t, err := time.Parse(time.RFC3339, req.OccurredAt)
		if err != nil {
			if t, err = time.Parse("2006-01-02", req.OccurredAt); err != nil {
				s.fail(w, r, fmt.Errorf("%w: occurred_at is a date (2026-10-23) or a time (RFC 3339)", errBadRequest))
				return
			}
		}
		req.When = t
	}
	p := principalFrom(r.Context())
	req.Key = r.Header.Get("Idempotency-Key")
	id, fresh, err := s.moneyLog().Enter(r.Context(), p.TenantID, p.Actor(), req.Entered)
	if err != nil {
		s.fail(w, r, leakErr(err))
		return
	}
	status := http.StatusCreated
	if !fresh {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"entry_id": id, "created": fresh})
}

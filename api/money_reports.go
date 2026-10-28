package api

import (
	"bytes"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/moneyreport"
)

// The Money page's summary and the exports (decision 0045, T7;
// docs/money-reports.md): a period's money, and its money log, invoices
// and accounting journal as CSV.

func (s *Server) moneyReportRoutes(r chi.Router) {
	read := s.need(PermInvoiceRead)
	r.With(read).Get("/money/summary", s.moneySummary)
	r.With(read).Get("/money/exports/{kind}", s.moneyExport)
}

// period reads ?from= and ?to= (Lagos days, to inclusive), or ?month=
// (2026-10); with none, today. At most 400 days.
func period(r *http.Request) (time.Time, time.Time, error) {
	q := r.URL.Query()
	if m := q.Get("month"); m != "" {
		t, err := time.ParseInLocation("2006-01", m, moneyreport.Lagos)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: month is like 2026-10", errBadRequest)
		}
		from, to := moneyreport.Month(t)
		return from, to, nil
	}
	from, to := moneyreport.Day(time.Now())
	if f := q.Get("from"); f != "" {
		t, err := moneyreport.ParseDay(f)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: from is a date like 2026-10-01", errBadRequest)
		}
		from, to = t, t.AddDate(0, 0, 1)
	}
	if v := q.Get("to"); v != "" {
		t, err := moneyreport.ParseDay(v)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: to is a date like 2026-10-31", errBadRequest)
		}
		to = t.AddDate(0, 0, 1)
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: to is on or after from", errBadRequest)
	}
	if to.Sub(from) > 400*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: a period is at most 400 days", errBadRequest)
	}
	return from, to, nil
}

func (s *Server) moneySummary(w http.ResponseWriter, r *http.Request) {
	from, to, err := period(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Riders' days (dispatch D4): deliveries, cash at the door, what each
	// owes; left out with no riders' activity.
	var out struct {
		moneyreport.Summary
		Riders []dispatch.RiderDay `json:"riders,omitempty"`
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		tenant := principalFrom(r.Context()).TenantID
		var err error
		if out.Summary, err = moneyreport.Summarise(r.Context(), tx, tenant, from, to); err != nil {
			return err
		}
		if s.Dispatch != nil {
			out.Riders, err = s.Dispatch.RiderDaysTx(r.Context(), tx, tenant, from, to)
		}
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// moneyExport serves GET /v1/money/exports/{log|invoices|journal} as a
// CSV download. The invoices export names customers only for people with
// pii.reveal who ask with ?reveal=true, which is audited.
func (s *Server) moneyExport(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	switch kind {
	case "log", "invoices", "journal":
	default:
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	from, to, err := period(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	reveal := kind == "invoices" && r.URL.Query().Get("reveal") == "true"
	if reveal && !p.Can(PermPIIReveal) {
		writeErr(w, http.StatusForbidden, "requires "+PermPIIReveal)
		return
	}
	var buf bytes.Buffer
	if err := s.tx(r, func(tx pgx.Tx) error {
		switch kind {
		case "log":
			err = moneyreport.ExportLog(r.Context(), tx, p.TenantID, from, to, &buf)
		case "journal":
			err = moneyreport.ExportJournal(r.Context(), tx, p.TenantID, from, to, &buf)
		default:
			var name func(uuid.UUID) string
			if reveal && s.Invoices != nil {
				name = func(id uuid.UUID) string {
					x, err := s.Invoices.Get(r.Context(), tx, p.TenantID, id.String())
					if err != nil {
						return ""
					}
					return x.Customer.Name
				}
			}
			err = moneyreport.ExportInvoices(r.Context(), tx, p.TenantID, from, to, name, &buf)
		}
		if err != nil {
			return err
		}
		return auditTx(r, tx, "money.export", kind, map[string]any{"from": from.Format("2006-01-02"), "to": to.AddDate(0, 0, -1).Format("2006-01-02"), "reveal": reveal})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	name := fmt.Sprintf("%s-%s-to-%s.csv", kind, from.Format("2006-01-02"), to.AddDate(0, 0, -1).Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

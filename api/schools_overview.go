package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/schools"
)

// The proprietor (docs/spec/schools.md 13, B9; docs/schools.md#overview,
// #the-daily-close): Schools > Overview for a term, and the daily close,
// taken once per school and day by the invoice tick (RunInvoices) or on
// demand, listed and read back. The overview names the top debtors, so
// their names are masked unless the caller holds pii.reveal and asks,
// which is audited as the Debtors page is; a close names no one.

// schoolOverviewRoutes mounts the routes inside the Schools group.
func (s *Server) schoolOverviewRoutes(r chi.Router) {
	read, manage := s.need(PermSchoolRead), s.need(PermSchoolManage)
	r.With(read).Get("/schools/overview", s.getSchoolOverview)
	r.With(manage, s.tenantWide).Post("/schools/close", s.closeSchoolDay)
	r.With(read).Get("/schools/closes", s.listSchoolCloses)
	r.With(read).Get("/schools/closes/{day}", s.getSchoolClose)
}

// getSchoolOverview serves GET /v1/schools/overview: the term's money as
// it stands (?term=, the current term by default).
func (s *Server) getSchoolOverview(w http.ResponseWriter, r *http.Request) {
	rev, ok := revealing(w, r)
	if !ok {
		return
	}
	p := principalFrom(r.Context())
	var ov schools.Overview
	if err := s.tx(r, func(tx pgx.Tx) error {
		term, err := s.termParam(r, tx)
		if err != nil {
			return err
		}
		if ov, err = s.schoolsSvc().Overview(r.Context(), tx, p.TenantID, term); err != nil {
			return schoolErr(err)
		}
		if rev {
			return auditTx(r, tx, "pii.reveal", "school_overview", map[string]any{"debtors": len(ov.TopDebtors)})
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !rev {
		for i := range ov.TopDebtors {
			maskDebtor(&ov.TopDebtors[i])
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"overview": ov, "revealed": rev})
}

// closeSchoolDay serves POST /v1/schools/close: the close of a day
// ({day}, yesterday by default), taken now when it was not yet; the
// stored close otherwise. Audited when this call took it.
func (s *Server) closeSchoolDay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Day string `json:"day"`
	}
	if r.ContentLength != 0 {
		if err := decodeBody(r, &req); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	p := principalFrom(r.Context())
	day := s.schoolsSvc().Yesterday()
	if req.Day != "" {
		var err error
		if day, err = schools.Day(req.Day); err != nil {
			s.fail(w, r, schoolErr(err))
			return
		}
	}
	var c schools.Close
	var fresh bool
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if c, fresh, err = s.schoolsSvc().CloseDay(r.Context(), tx, p.TenantID, day, p.Actor()); err != nil {
			return schoolErr(err)
		}
		if fresh {
			return auditTx(r, tx, "school.close.run", c.Day, map[string]any{"reconciles": c.Reconciles, "problems": c.Problems})
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"close": c, "taken_now": fresh})
}

// listSchoolCloses serves GET /v1/schools/closes?from&to: the closes
// taken, newest first (the last 31 by default).
func (s *Server) listSchoolCloses(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	q := r.URL.Query()
	var list []schools.CloseBrief
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = s.schoolsSvc().Closes(r.Context(), tx, p.TenantID, q.Get("from"), q.Get("to"), 0)
		return schoolErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"closes": list})
}

// getSchoolClose serves GET /v1/schools/closes/{day}: a day's close, 404
// when the day was not closed.
func (s *Server) getSchoolClose(w http.ResponseWriter, r *http.Request) {
	day, err := schools.Day(chi.URLParam(r, "day"))
	if err != nil {
		s.fail(w, r, schoolErr(err))
		return
	}
	p := principalFrom(r.Context())
	var c schools.Close
	if err := s.tx(r, func(tx pgx.Tx) error {
		x, ok, err := s.schoolsSvc().CloseOf(r.Context(), tx, p.TenantID, day)
		if err != nil {
			return schoolErr(err)
		}
		if !ok {
			return pgx.ErrNoRows
		}
		c = x
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"close": c})
}

// runSchoolCloses closes yesterday for every school once a day has
// turned, from the invoice tick: a day is closed once, so a look that
// finds it closed does nothing.
func (s *Server) runSchoolCloses(ctx context.Context) {
	r, err := s.schoolsSvc().CloseYesterday(ctx)
	switch {
	case err != nil && ctx.Err() == nil && !errors.Is(err, context.Canceled):
		s.Logger.Error("schools: daily close", "err", err)
	case r.Taken > 0:
		s.Logger.Info("schools: daily close", "schools", r.Schools, "taken", r.Taken, "not_reconciled", r.NotReconciled, "day", s.schoolsSvc().Yesterday().Format(time.DateOnly))
	}
}

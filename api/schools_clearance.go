package api

import (
	"bytes"
	"encoding/csv"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/schools"
)

// Clearance and the gate (decision 0046, points 4 and 10; docs/spec/
// schools.md 12, B8; docs/schools.md#clearance): the clearance list per
// class (and its CSV), a student's clearance, overrides recorded and
// withdrawn by people with school.approve (audited), and the gate's two
// routes, which answer a name search and cleared or see the bursar with
// no amount in any field. The clearance routes need school.read, so
// their amounts are seen by people who may see the school's money; the
// gate routes are what a gate session (school.gate and nothing else) may
// call besides who it is and signing out, and are open to school.read
// too so a bursar can use the gate screen.

// schoolClearanceRoutes mounts the clearance routes inside the Schools
// group.
func (s *Server) schoolClearanceRoutes(r chi.Router) {
	read, approve := s.need(PermSchoolRead), s.need(PermSchoolApprove)
	r.With(read).Get("/schools/clearance", s.listSchoolClearance)
	r.With(read).Get("/schools/clearance-overrides", s.listSchoolOverrides)
	r.With(approve, s.tenantWide).Post("/schools/clearance-overrides/{id}/withdraw", s.withdrawSchoolOverride)
	r.With(read).Get("/schools/students/{id}/clearance", s.getSchoolStudentClearance)
	r.With(approve, s.tenantWide).Post("/schools/students/{id}/clearance-override", s.addSchoolOverride)
}

// gateRoutes mounts /v1/gate inside the authenticated group: the gate
// role's two routes (authenticate lets a gate session through to them).
func (s *Server) gateScreenRoutes(r chi.Router) {
	r.With(s.gateOrRead).Get("/gate/search", s.gateSearch)
	r.With(s.gateOrRead).Get("/gate/students/{id}", s.gateCheck)
}

// gateOrRead admits the gate role and anyone who may read the school.
func (s *Server) gateOrRead(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r.Context())
		if !p.Can(PermSchoolGate) && !p.Can(PermSchoolRead) {
			writeErr(w, http.StatusForbidden, "requires "+PermSchoolGate+" or "+PermSchoolRead)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func maskClearanceRow(x *schools.ClearanceRow) { x.Name = schoolName(x.Name) }

// listSchoolClearance serves GET /v1/schools/clearance: every student
// enrolled in the term (?term=, the current one by default), of one
// class with ?class=, with their clearance and balance; ?format=csv for
// the file, masked the same way.
func (s *Server) listSchoolClearance(w http.ResponseWriter, r *http.Request) {
	rev, ok := revealing(w, r)
	if !ok {
		return
	}
	class, err := optionalID(r, "class")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	csvOut := r.URL.Query().Get("format") == "csv"
	p := principalFrom(r.Context())
	var l schools.ClearanceList
	if err := s.tx(r, func(tx pgx.Tx) error {
		term, err := s.termParam(r, tx)
		if err != nil {
			return err
		}
		if l, err = s.schoolsSvc().TermClearance(r.Context(), tx, p.TenantID, term, class); err != nil {
			return schoolErr(err)
		}
		if rev {
			return auditTx(r, tx, "pii.reveal", "school_clearance", map[string]any{"students": len(l.Rows), "csv": csvOut})
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !rev {
		for i := range l.Rows {
			maskClearanceRow(&l.Rows[i])
		}
	}
	if csvOut {
		var buf bytes.Buffer
		cw := csv.NewWriter(&buf)
		_ = cw.Write([]string{"Class", "Admission number", "Name", "Clearance", "Why", "Balance", "Override ends"})
		for _, x := range l.Rows {
			row := []string{x.Class, x.AdmissionNumber, x.Name, clearanceWords[x.Status], schools.ReasonWords[x.Reason], invoicing.Naira(x.BalanceKobo), ""}
			if x.Override != nil {
				row[6] = x.Override.EndsOn
			}
			_ = cw.Write(row)
		}
		cw.Flush()
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="clearance-`+l.Today+`.csv"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(buf.Bytes())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"term_id": l.TermID, "term": l.Term, "students": l.Rows, "totals": l.Totals, "today": l.Today, "revealed": rev})
}

var clearanceWords = map[string]string{schools.Cleared: "Cleared", schools.PartlyPaid: "Partly paid", schools.NotPaid: "Not paid"}

// getSchoolStudentClearance serves GET /v1/schools/students/{id}/clearance
// (?term=, the current term by default): the student's clearance and the
// overrides recorded for them.
func (s *Server) getSchoolStudentClearance(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	idOrNumber, err := studentRef(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var c schools.Clearance
	var overrides []schools.ClearanceOverride
	if err := s.tx(r, func(tx pgx.Tx) error {
		term, err := s.termParam(r, tx)
		if err != nil {
			return err
		}
		st, err := s.schoolsSvc().Student(r.Context(), tx, p.TenantID, idOrNumber)
		if err != nil {
			return schoolErr(err)
		}
		if c, err = s.schoolsSvc().Clearance(r.Context(), tx, p.TenantID, st.ID, term); err != nil {
			return schoolErr(err)
		}
		overrides, err = s.schoolsSvc().Overrides(r.Context(), tx, p.TenantID, schools.OverrideFilter{Student: &st.ID})
		return schoolErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clearance": c, "overrides": overrides})
}

// addSchoolOverride serves POST /v1/schools/students/{id}/clearance-override.
func (s *Server) addSchoolOverride(w http.ResponseWriter, r *http.Request) {
	var in schools.OverrideIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	idOrNumber, err := studentRef(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var out schools.ClearanceOverride
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out, err = s.schoolsSvc().AddOverride(r.Context(), tx, p.TenantID, idOrNumber, in, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.clearance.override", out.ID.String(), map[string]any{"student": out.StudentID, "term": out.TermID, "ends_on": out.EndsOn})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"override": out})
}

// listSchoolOverrides serves GET /v1/schools/clearance-overrides: by
// student and term, ?in_force=true for those counting today.
func (s *Server) listSchoolOverrides(w http.ResponseWriter, r *http.Request) {
	f := schools.OverrideFilter{InForce: r.URL.Query().Get("in_force") == "true"}
	var err error
	if f.Student, err = optionalID(r, "student"); err != nil {
		s.fail(w, r, err)
		return
	}
	if f.Term, err = optionalID(r, "term"); err != nil {
		s.fail(w, r, err)
		return
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		f.Limit, _ = strconv.Atoi(v)
	}
	p := principalFrom(r.Context())
	var list []schools.ClearanceOverride
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = s.schoolsSvc().Overrides(r.Context(), tx, p.TenantID, f)
		return schoolErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"overrides": list})
}

// withdrawSchoolOverride serves POST /v1/schools/clearance-overrides/{id}/withdraw.
func (s *Server) withdrawSchoolOverride(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out schools.ClearanceOverride
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out, err = s.schoolsSvc().WithdrawOverride(r.Context(), tx, p.TenantID, id, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.clearance.withdraw", id.String(), map[string]any{"student": out.StudentID, "term": out.TermID})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"override": out})
}

// --- the gate ---

// Gate searches per session: one every gateSearchEvery, up to
// gateSearchBurst at once (a person typing, not a script walking the
// roll).
const (
	gateSearchEvery = 400 * time.Millisecond
	gateSearchBurst = 30
)

// gateSearch serves GET /v1/gate/search?q=: active students whose name
// holds the letters (three or more), at most ten: id, name and class.
func (s *Server) gateSearch(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	who := p.UserID
	if who == uuid.Nil {
		who = p.KeyID
	}
	if !s.limiter("gate-search:"+who.String(), gateSearchEvery, gateSearchBurst).Allow() {
		w.Header().Set("Retry-After", "5")
		writeErr(w, http.StatusTooManyRequests, "too many searches at once; wait a moment")
		return
	}
	var hits []schools.GateHit
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		hits, err = s.schoolsSvc().GateSearch(r.Context(), tx, p.TenantID, r.URL.Query().Get("q"))
		return schoolErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if hits == nil {
		hits = []schools.GateHit{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"students": hits})
}

// gateCheck serves GET /v1/gate/students/{id}: cleared or see_bursar for
// the current term, with the name and class and nothing else; each check
// is in the audit log.
func (s *Server) gateCheck(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out schools.GateAnswer
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out, err = s.schoolsSvc().GateCheck(r.Context(), tx, p.TenantID, id); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.gate.check", id.String(), map[string]any{"status": out.Status})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

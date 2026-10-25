package api

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/schools"
)

// Chasing (docs/spec/schools.md 11, B7; docs/schools.md#debtors):
// Debtors with aging (and its CSV), the reminder ladder, promises to pay,
// plan standing on the student's page, and the chasing check run by hand.
// Names are masked unless the caller holds pii.reveal and asks, which is
// audited, as everywhere in the area.

// schoolChasingRoutes mounts the routes inside the Schools group.
func (s *Server) schoolChasingRoutes(r chi.Router) {
	read, manage := s.need(PermSchoolRead), s.need(PermSchoolManage)
	r.With(read).Get("/schools/debtors", s.listSchoolDebtors)
	r.With(manage, s.tenantWide).Post("/schools/reminders", s.sendSchoolReminders)
	r.With(read).Get("/schools/students/{id}/chasing", s.getSchoolStudentChasing)
	r.With(manage, s.tenantWide).Post("/schools/students/{id}/promise", s.recordSchoolPromise)
	r.With(read).Get("/schools/promises", s.listSchoolPromises)
	r.With(manage, s.tenantWide).Post("/schools/promises/{id}/withdraw", s.withdrawSchoolPromise)
	r.With(manage, s.tenantWide).Post("/schools/chasing/check", s.checkSchoolChasing)
}

func maskDebtor(d *schools.Debtor) { d.Name = schoolName(d.Name) }

// studentRef is the {id} of a student route: an id, or an admission
// number, which may hold a slash sent escaped (GCA%2F001).
func studentRef(r *http.Request) (string, error) {
	ref, err := url.PathUnescape(chi.URLParam(r, "id"))
	if err != nil {
		return "", pgx.ErrNoRows
	}
	return ref, nil
}

// listSchoolDebtors serves GET /v1/schools/debtors: students with a
// balance, aged, by class; ?format=csv for the file, with the same
// masking rule.
func (s *Server) listSchoolDebtors(w http.ResponseWriter, r *http.Request) {
	rev, ok := revealing(w, r)
	if !ok {
		return
	}
	f := schools.DebtorFilter{Bucket: r.URL.Query().Get("bucket"), Has: r.URL.Query().Get("has")}
	var err error
	if f.ClassID, err = optionalID(r, "class"); err != nil {
		s.fail(w, r, err)
		return
	}
	csvOut := r.URL.Query().Get("format") == "csv"
	p := principalFrom(r.Context())
	var d schools.Debtors
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if d, err = s.schoolsSvc().Debtors(r.Context(), tx, p.TenantID, f); err != nil {
			return schoolErr(err)
		}
		if rev {
			return auditTx(r, tx, "pii.reveal", "school_debtors", map[string]any{"debtors": len(d.Debtors), "csv": csvOut})
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !rev {
		for i := range d.Debtors {
			maskDebtor(&d.Debtors[i])
		}
	}
	if csvOut {
		var buf bytes.Buffer
		cw := csv.NewWriter(&buf)
		_ = cw.Write([]string{"Class", "Admission number", "Name", "Balance", "Due on", "Days past due", "Aging", "Last reminder", "Reminded on", "Promise", "Promised by", "Plan", "Next instalment"})
		for _, x := range d.Debtors {
			row := []string{x.Class, x.AdmissionNumber, x.Name, invoicing.Naira(x.BalanceKobo), x.DueOn, strconv.Itoa(x.DaysPastDue), schools.BucketWords[x.Bucket], "", "", "", "", "", ""}
			if x.LastReminder != nil {
				row[7], row[8] = strconv.Itoa(x.LastReminder.Rung), x.LastReminder.At.Format("2006-01-02")
			}
			if x.Promise != nil {
				row[9], row[10] = x.Promise.Status, x.Promise.PromisedOn
			}
			if x.Plan != nil {
				row[11], row[12] = x.Plan.State, x.Plan.NextDueOn
			}
			_ = cw.Write(row)
		}
		cw.Flush()
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="debtors-`+d.Today+`.csv"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(buf.Bytes())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"debtors": d.Debtors, "totals": d.Totals, "students": d.Students, "balance_kobo": d.BalanceKobo, "today": d.Today, "revealed": rev})
}

// sendSchoolReminders serves POST /v1/schools/reminders: at most 50
// students and a rung, once per Idempotency-Key.
func (s *Server) sendSchoolReminders(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	var req struct {
		Students []uuid.UUID `json:"students"`
		Rung     int         `json:"rung"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		s.fail(w, r, fmt.Errorf("%w: an Idempotency-Key of up to 200 characters is required, so a retried send messages nobody twice", errBadRequest))
		return
	}
	if len(req.Students) == 0 {
		s.fail(w, r, fmt.Errorf("%w: choose at least one student", errBadRequest))
		return
	}
	if req.Rung < 1 || req.Rung > 3 {
		s.fail(w, r, fmt.Errorf("%w: rung is 1, 2 or 3", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	out, err := s.schoolsSvc().SendReminders(r.Context(), p.TenantID, schools.RemindIn{Students: req.Students, Rung: req.Rung, Key: "api:" + key}, p.Actor())
	if err != nil {
		s.fail(w, r, schoolErr(err))
		return
	}
	if !out.Replayed {
		if err := s.tx(r, func(tx pgx.Tx) error {
			return auditTx(r, tx, "school.reminders.send", key, map[string]any{"rung": req.Rung, "chosen": len(req.Students), "sent": out.Sent, "skipped": out.Skipped})
		}); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// getSchoolStudentChasing serves GET /v1/schools/students/{id}/chasing:
// the student's balance and aging, promises, reminders and plan standing.
func (s *Server) getSchoolStudentChasing(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	idOrNumber, err := studentRef(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var out schools.StudentChasing
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = s.schoolsSvc().StudentChasing(r.Context(), tx, p.TenantID, idOrNumber)
		return schoolErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// recordSchoolPromise serves POST /v1/schools/students/{id}/promise.
func (s *Server) recordSchoolPromise(w http.ResponseWriter, r *http.Request) {
	var in schools.PromiseIn
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
	var out schools.Promise
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out, err = s.schoolsSvc().RecordPromise(r.Context(), tx, p.TenantID, idOrNumber, in, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.promise.record", out.ID.String(), map[string]any{"student": out.StudentID, "promised_on": out.PromisedOn, "balance_kobo": out.BalanceKobo})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"promise": out})
}

// listSchoolPromises serves GET /v1/schools/promises: by student and
// status (every status by default), newest first.
func (s *Server) listSchoolPromises(w http.ResponseWriter, r *http.Request) {
	f := schools.PromiseFilter{Status: r.URL.Query().Get("status")}
	switch f.Status {
	case "", schools.PromiseOpen, schools.PromiseKept, schools.PromiseMissed, schools.PromiseWithdrawn:
	default:
		s.fail(w, r, fmt.Errorf("%w: status is open, kept, missed or withdrawn", errBadRequest))
		return
	}
	var err error
	if f.Student, err = optionalID(r, "student"); err != nil {
		s.fail(w, r, err)
		return
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		f.Limit, _ = strconv.Atoi(v)
	}
	p := principalFrom(r.Context())
	var list []schools.Promise
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = s.schoolsSvc().Promises(r.Context(), tx, p.TenantID, f)
		return schoolErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"promises": list})
}

// withdrawSchoolPromise serves POST /v1/schools/promises/{id}/withdraw.
func (s *Server) withdrawSchoolPromise(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out schools.Promise
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out, err = s.schoolsSvc().WithdrawPromise(r.Context(), tx, p.TenantID, id, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.promise.withdraw", id.String(), map[string]any{"student": out.StudentID})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"promise": out})
}

// checkSchoolChasing serves POST /v1/schools/chasing/check: runs the
// school's chasing check now (it runs every hour by itself); each
// promise and instalment is decided once, so running it again changes
// nothing.
func (s *Server) checkSchoolChasing(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	out, err := s.schoolsSvc().CheckChasing(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, r, schoolErr(err))
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "school.chasing.check", p.TenantID.String(), map[string]any{"promises_kept": out.PromisesKept, "promises_missed": out.PromisesMissed, "arrears": out.Arrears})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

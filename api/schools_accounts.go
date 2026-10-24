package api

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/lookback"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/schools"
)

// Students' account numbers (docs/spec/schools.md 6, B4;
// docs/schools.md#account-numbers): a student's accounts, issuing one, and
// issuing in batches (a plan, then its confirmation, then the batch's
// progress). The issuing connection is the owner's setting
// (/v1/settings/schools). An account's name has the child's name, so it is
// masked unless the caller holds pii.reveal and asks; the account number
// is the school's own collection account, meant to be given to parents.

func (s *Server) schoolAccountRoutes(r chi.Router) {
	read, manage := s.need(PermSchoolRead), s.need(PermSchoolManage)
	r.With(read).Get("/schools/students/{id}/accounts", s.listStudentAccounts)
	r.With(manage, s.tenantWide).Post("/schools/students/{id}/account", s.issueStudentAccount)
	r.With(manage, s.tenantWide).Post("/schools/accounts/issue", s.issueSchoolAccounts)
	r.With(read).Get("/schools/accounts/jobs", s.listSchoolAccountJobs)
	r.With(read).Get("/schools/accounts/jobs/{id}", s.getSchoolAccountJob)
}

// studentParam is the {id} of a student route: an id, or an admission
// number (sent escaped when it holds a slash).
func studentParam(r *http.Request) (string, error) {
	v, err := url.PathUnescape(chi.URLParam(r, "id"))
	if err != nil {
		return "", pgx.ErrNoRows
	}
	return v, nil
}

func maskAccounts(list []schools.StudentAccount) {
	for i := range list {
		list[i].AccountName = schoolName(list[i].AccountName)
	}
}

func (s *Server) listStudentAccounts(w http.ResponseWriter, r *http.Request) {
	rev, ok := revealing(w, r)
	if !ok {
		return
	}
	idOrNumber, err := studentParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var list []schools.StudentAccount
	var st schools.Issuing
	if err := s.tx(r, func(tx pgx.Tx) error {
		d, err := s.schoolsSvc().Student(r.Context(), tx, p.TenantID, idOrNumber)
		if err != nil {
			return schoolErr(err)
		}
		if list, err = s.schoolsSvc().StudentAccounts(r.Context(), tx, p.TenantID, d.ID); err != nil {
			return err
		}
		if st, err = schools.GetIssuing(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		if rev {
			return auditTx(r, tx, "pii.reveal", "school_student_accounts:"+d.AdmissionNumber, nil)
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !rev {
		maskAccounts(list)
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": list, "issuing_connection": st.Channel, "revealed": rev})
}

// schoolIssueFail answers a rail's refusal of one student as 422
// rail_refused with the rail's reason (it is recorded on the student), and
// anything else as fail does.
func (s *Server) schoolIssueFail(w http.ResponseWriter, r *http.Request, err error) {
	if schools.ErrIssue(err) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": schools.Plain(err), "code": "rail_refused"})
		return
	}
	s.fail(w, r, schoolErr(err))
}

// issueStudentAccount serves POST /v1/schools/students/{id}/account:
// issues the student's account number on the school's issuing connection,
// once (a student who has one is answered it, 200). It takes a parent's
// bvn or nin for rails that need one (sent to the rail, never kept), and
// retry after a lost answer was checked with the rail.
func (s *Server) issueStudentAccount(w http.ResponseWriter, r *http.Request) {
	idOrNumber, err := studentParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in schools.IssueIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	a, err := s.schoolsSvc().IssueOne(r.Context(), p.TenantID, idOrNumber, in, p.Actor())
	if err != nil {
		s.schoolIssueFail(w, r, err)
		return
	}
	list := []schools.StudentAccount{a}
	maskAccounts(list)
	writeJSON(w, http.StatusOK, list[0])
}

// issueSchoolAccounts serves POST /v1/schools/accounts/issue: {} answers
// the plan (who is issued an account, who cannot be and why); {confirm:
// <the plan's hash>} starts its batch (202), which issues each student
// once, in the background.
func (s *Server) issueSchoolAccounts(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if req.Confirm == "" {
		var plan schools.IssuePlan
		if err := s.tx(r, func(tx pgx.Tx) error {
			var err error
			plan, err = s.schoolsSvc().PlanAccounts(r.Context(), tx, p.TenantID)
			return schoolErr(err)
		}); err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"plan": plan})
		return
	}
	var job schools.Job
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if job, err = s.schoolsSvc().StartBatch(r.Context(), tx, p.TenantID, req.Confirm, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.accounts.batch", job.ID.String(), map[string]any{"connection": job.Connection, "total": job.Total, "plan": req.Confirm})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) listSchoolAccountJobs(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var list []schools.Job
	if err := s.tx(r, func(tx pgx.Tx) error {
		if !s.schoolsOn(r.Context(), tx, p.TenantID) {
			return schoolErr(schools.ErrOff)
		}
		var err error
		list, err = s.schoolsSvc().Jobs(r.Context(), tx, p.TenantID, 10)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": list})
}

func (s *Server) getSchoolAccountJob(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var j schools.Job
	if err := s.tx(r, func(tx pgx.Tx) error {
		if !s.schoolsOn(r.Context(), tx, p.TenantID) {
			return schoolErr(schools.ErrOff)
		}
		var err error
		j, err = s.schoolsSvc().Job(r.Context(), tx, p.TenantID, id)
		return schoolErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

// runSchoolAccounts works through the batches issuing students' accounts
// (every 2 seconds) and reads customers' credits back from the rails that
// issued their accounts (the look-back: each connection every 15
// minutes; checked every minute).
func (s *Server) runSchoolAccounts(ctx context.Context) {
	rails := s.accountRails()
	if rails == nil {
		return
	}
	log := s.Invoices.MoneyLog
	if log == nil {
		log = &moneylog.Log{Pool: s.Store.Pool, PII: s.Store.PII}
	}
	looker := &lookback.Looker{Pool: s.Store.Pool, Rails: rails, Log: log, Logger: s.Logger}
	jobs, look := time.NewTicker(2*time.Second), time.NewTicker(time.Minute)
	defer jobs.Stop()
	defer look.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-jobs.C:
			if err := s.schoolsSvc().RunJobs(ctx, 20); err != nil && ctx.Err() == nil {
				s.Logger.Error("schools: issuing accounts", "err", err)
			}
		case <-look.C:
			if err := looker.Tick(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("money: look-back", "err", err)
			}
		}
	}
}

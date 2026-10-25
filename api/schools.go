package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/access"
	"github.com/israel-duff/taskiem/engine/billing"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/schools"
)

// The Schools area (decision 0046, B2; docs/schools.md): turning it on,
// and the school's record: classes, sessions and terms, students,
// families, enrollments, the import and promotion. Students' and
// guardians' details are masked unless the caller holds pii.reveal and
// asks with ?reveal=true, which is audited, as for invoices. Every change
// is audited.

const (
	PermSchoolRead    = access.SchoolRead
	PermSchoolManage  = access.SchoolManage
	PermSchoolApprove = access.SchoolApprove
	PermSchoolGate    = access.SchoolGate
)

// gateRoutes are what a gate session (the gate role's school.gate and
// nothing else) may call: who it is, signing out, and the gate screen's
// own routes under /v1/gate/ (schools_clearance.go). Everything else is
// refused, centrally in authenticate, not only hidden (docs/spec/
// schools.md 12.3).
var gateRoutes = map[string]bool{"GET /v1/me": true, "POST /v1/auth/logout": true}

// gatePrefix is the gate screen's routes: a name search and one
// student's answer, nothing else (TestGateResponsesCarryNoAmount walks
// them).
const gatePrefix = "/v1/gate/"

func gateAllowed(method, path string) bool {
	return gateRoutes[method+" "+strings.TrimSuffix(path, "/")] || (method == http.MethodGet && strings.HasPrefix(path, gatePrefix))
}

// bursarRole is the custom role offered when the area is turned on.
var bursarRole = struct {
	Name, Description string
	Permissions       []string
}{"bursar", "Runs the fees office: students, families, classes and terms, and invoices", []string{PermInvoiceManage, PermInvoiceRead, PermSchoolManage, PermSchoolRead}}

// schoolRoutes mounts /v1/schools inside the authenticated group.
func (s *Server) schoolRoutes(r chi.Router) {
	read, manage := s.need(PermSchoolRead), s.need(PermSchoolManage)
	r.With(read).Get("/schools/classes", s.listSchoolClasses)
	r.With(manage, s.tenantWide).Post("/schools/classes", s.addSchoolClass)
	r.With(manage, s.tenantWide).Put("/schools/classes/{id}", s.changeSchoolClass)
	r.With(manage, s.tenantWide).Delete("/schools/classes/{id}", s.removeSchoolClass)
	r.With(read).Get("/schools/sessions", s.listSchoolSessions)
	r.With(manage, s.tenantWide).Post("/schools/sessions", s.addSchoolSession)
	r.With(manage, s.tenantWide).Post("/schools/sessions/{id}/terms", s.addSchoolTerm)
	r.With(read).Get("/schools/sessions/{id}/promotion", s.getSchoolPromotion)
	r.With(manage, s.tenantWide).Post("/schools/sessions/{id}/promotion", s.promoteSchoolSession)
	r.With(manage, s.tenantWide).Put("/schools/terms/{id}", s.changeSchoolTerm)
	r.With(manage, s.tenantWide).Delete("/schools/terms/{id}", s.removeSchoolTerm)
	r.With(manage, s.tenantWide).Post("/schools/terms/{id}/start", s.startSchoolTerm)
	r.With(read).Get("/schools/terms/{id}/enrollments", s.listSchoolEnrollments)
	r.With(read).Get("/schools/students", s.listSchoolStudents)
	r.With(manage, s.tenantWide).Post("/schools/students", s.addSchoolStudent)
	r.With(manage, s.tenantWide).Post("/schools/students/import", s.importSchoolStudents)
	r.With(read).Get("/schools/students/{id}", s.getSchoolStudent)
	r.With(manage, s.tenantWide).Patch("/schools/students/{id}", s.changeSchoolStudent)
	r.With(read).Get("/schools/families", s.listSchoolFamilies)
	r.With(manage, s.tenantWide).Post("/schools/families", s.addSchoolFamily)
	r.With(read).Get("/schools/families/{id}", s.getSchoolFamily)
	r.With(manage, s.tenantWide).Post("/schools/families/{id}/guardians", s.addSchoolGuardian)
	r.With(manage, s.tenantWide).Put("/schools/families/{id}/guardians/{guardian}", s.changeSchoolGuardian)
	s.schoolFeeRoutes(r)       // fees and term invoices (schools_fees.go)
	s.schoolAccountRoutes(r)   // students' account numbers (schools_accounts.go)
	s.schoolCashRoutes(r)      // cash at the desk, approvals, receipts (schools_cash.go)
	s.schoolFamilyRoutes(r)    // the family page's claims, plans and links (schools_family.go)
	s.schoolChasingRoutes(r)   // debtors, reminders, promises, plan standing (schools_chasing.go)
	s.schoolClearanceRoutes(r) // clearance, overrides (schools_clearance.go)
	s.schoolOverviewRoutes(r)  // the overview and the daily close (schools_overview.go)
	s.gateScreenRoutes(r)      // the gate's two routes, under /v1/gate (schools_clearance.go)
	r.With(manage, s.tenantWide).Delete("/schools/families/{id}/guardians/{guardian}", s.removeSchoolGuardian)
}

func (s *Server) schoolsSvc() *schools.Schools {
	return &schools.Schools{Pool: s.Store.Pool, Vault: s.Vault, Invoices: s.Invoices, Rails: s.accountRails(), InboxKey: s.SchoolInboxKey, Logger: s.Logger}
}

// accountRails are AccountRails, else the invoices' rails when they can
// issue accounts; nil without either.
func (s *Server) accountRails() invoicing.CapabilityRails {
	if s.AccountRails != nil {
		return s.AccountRails
	}
	if s.Invoices != nil {
		if r, ok := s.Invoices.Rails.(invoicing.CapabilityRails); ok {
			return r
		}
	}
	return nil
}

func schoolErr(err error) error {
	switch {
	case errors.Is(err, schools.ErrInvalid):
		return fmt.Errorf("%w: %s", errBadRequest, schools.Plain(err))
	case errors.Is(err, schools.ErrState), errors.Is(err, schools.ErrOff):
		return fmt.Errorf("%w: %s", errConflict, schools.Plain(err))
	case errors.Is(err, schools.ErrNotFound):
		return pgx.ErrNoRows
	}
	return err
}

func pathID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, pgx.ErrNoRows
	}
	return id, nil
}

// --- the setting ---

func (s *Server) schoolsOn(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) bool {
	on, err := schools.Enabled(ctx, tx, tenant)
	return err == nil && on
}

// getSchoolSettings answers GET /v1/settings/schools.
func (s *Server) getSchoolSettings(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var on, bursar bool
	var threshold int64
	var cash *int64
	var linkDays, grace int
	var issuing schools.Issuing
	var wording map[int]string
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if on, err = schools.Enabled(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		if linkDays, err = schools.FamilyLinkDays(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		if grace, err = schools.PlanGraceDays(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		if wording, err = schools.CustomWording(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		if threshold, err = schools.DiscountThreshold(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		if cash, err = schools.CashThreshold(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		if issuing, err = schools.GetIssuing(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		return tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM roles WHERE name = $1)`, bursarRole.Name).Scan(&bursar)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	issuers, err := s.schoolsSvc().Issuers(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": on, "bursar_role": bursar, "discount_threshold_naira": threshold / 100, "cash_threshold_naira": nairaOrNil(cash),
		"family_link_days": linkDays, "issuing": issuing, "issuers": issuers, "plan_grace_days": grace, "reminder_wording": wordingOut(wording), "reminder_defaults": wordingOut(schools.DefaultWording),
		"reminder_placeholders": schools.ReminderPlaceholders})
}

// wordingOut is a rung-keyed wording as JSON ({"1": ...}).
func wordingOut(w map[int]string) map[string]string {
	out := map[string]string{}
	for r, v := range w {
		out[strconv.Itoa(r)] = v
	}
	return out
}

// nairaOrNil is a nullable threshold in whole naira.
func nairaOrNil(kobo *int64) *int64 {
	if kobo == nil {
		return nil
	}
	n := *kobo / 100
	return &n
}

// putSchoolSettings answers PUT /v1/settings/schools: only an owner turns
// the area on or off. Turning it on can also add the Bursar custom role
// (bursar_role), when the plan has custom roles and the name is free.
func (s *Server) putSchoolSettings(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !slices.Contains(p.Roles, "owner") {
		writeErr(w, http.StatusForbidden, "only an owner turns the Schools area on or off")
		return
	}
	var req struct {
		Enabled    bool `json:"enabled"`
		BursarRole bool `json:"bursar_role"`
		// DiscountThresholdNaira, when given, sets the school's discount
		// threshold in whole naira (0: none). Discounts at or above it
		// need a second person (B5; refused until then).
		DiscountThresholdNaira *int64 `json:"discount_threshold_naira"`
		// CashThresholdNaira, when given, sets the school's cash threshold
		// in whole naira: null for none, 0 for every posting (B5).
		CashThresholdNaira json.RawMessage `json:"cash_threshold_naira"`
		// Issuing, when given, sets the connection that issues students'
		// account numbers (and its wallet), and whether siblings share
		// family credit.
		Issuing *schools.Issuing `json:"issuing"`
		// FamilyLinkDays, when given, is how long a family page link works
		// (1 to 30 days; 3 by default, B6).
		FamilyLinkDays *int `json:"family_link_days"`
		// ReminderWording, when given, sets the rungs named ("1", "2",
		// "3"; "" takes a rung back to the default) (B7).
		ReminderWording map[string]string `json:"reminder_wording"`
		// PlanGraceDays, when given, is how many days past an instalment's
		// date a payment plan stays in good standing (0 to 60) (B7).
		PlanGraceDays *int `json:"plan_grace_days"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Issuing != nil && req.Enabled {
		checked, err := s.schoolsSvc().CheckIssuing(r.Context(), p.TenantID, *req.Issuing)
		if err != nil {
			s.fail(w, r, schoolErr(err))
			return
		}
		req.Issuing = &checked
	}
	addBursar := req.Enabled && req.BursarRole
	if addBursar && s.Billing.Require(r.Context(), p.TenantID, billing.FeatureCustomRoles) != nil {
		addBursar = false // the plan has no custom roles: the area still turns on
	}
	bursar, threshold := false, int64(0)
	var cash *int64
	var linkDays, grace int
	var issuing schools.Issuing
	var wording map[int]string
	if err := s.tx(r, func(tx pgx.Tx) error {
		changed, err := schools.SetEnabled(r.Context(), tx, p.TenantID, req.Enabled, p.Actor())
		if err != nil {
			return err
		}
		if changed {
			if err := auditTx(r, tx, "settings.schools", p.TenantID.String(), map[string]any{"enabled": req.Enabled}); err != nil {
				return err
			}
		}
		if addBursar {
			var taken bool
			if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM roles WHERE name = $1) OR EXISTS (SELECT 1 FROM memberships WHERE role = $1)`, bursarRole.Name).Scan(&taken); err != nil {
				return err
			}
			if !taken {
				if _, err := tx.Exec(r.Context(), `INSERT INTO roles (tenant_id, name, description, permissions, created_by) VALUES ($1, $2, $3, $4, $5)`,
					p.TenantID, bursarRole.Name, bursarRole.Description, bursarRole.Permissions, p.Actor()); err != nil {
					return err
				}
				if err := auditTx(r, tx, "role.put", bursarRole.Name, map[string]any{"permissions": bursarRole.Permissions, "before": nil, "template": "schools"}); err != nil {
					return err
				}
			}
		}
		if req.DiscountThresholdNaira != nil && req.Enabled {
			if *req.DiscountThresholdNaira < 0 || *req.DiscountThresholdNaira > 1_000_000_000 {
				return errors.Join(errBadRequest, errors.New("discount_threshold_naira is 0 (none) to 1,000,000,000"))
			}
			was, err := schools.SetDiscountThreshold(r.Context(), tx, p.TenantID, *req.DiscountThresholdNaira*100, p.Actor())
			if err != nil {
				return schoolErr(err)
			}
			if was != *req.DiscountThresholdNaira*100 {
				if err := auditTx(r, tx, "settings.schools.discount_threshold", p.TenantID.String(), map[string]any{"from_kobo": was, "to_kobo": *req.DiscountThresholdNaira * 100}); err != nil {
					return err
				}
			}
		}
		if len(req.CashThresholdNaira) > 0 && req.Enabled {
			var kobo *int64
			if string(req.CashThresholdNaira) != "null" {
				var n int64
				if err := json.Unmarshal(req.CashThresholdNaira, &n); err != nil || n < 0 || n > 1_000_000_000 {
					return errors.Join(errBadRequest, errors.New("cash_threshold_naira is null (none), 0 (every posting) or up to 1,000,000,000"))
				}
				n *= 100
				kobo = &n
			}
			was, err := schools.SetCashThreshold(r.Context(), tx, p.TenantID, kobo, p.Actor())
			if err != nil {
				return schoolErr(err)
			}
			if (was == nil) != (kobo == nil) || was != nil && *was != *kobo {
				if err := auditTx(r, tx, "settings.schools.cash_threshold", p.TenantID.String(), map[string]any{"from_kobo": was, "to_kobo": kobo}); err != nil {
					return err
				}
			}
		}
		if req.FamilyLinkDays != nil && req.Enabled {
			was, err := schools.SetFamilyLinkDays(r.Context(), tx, p.TenantID, *req.FamilyLinkDays, p.Actor())
			if err != nil {
				return schoolErr(err)
			}
			if was != *req.FamilyLinkDays {
				if err := auditTx(r, tx, "settings.schools.family_link_days", p.TenantID.String(), map[string]any{"from": was, "to": *req.FamilyLinkDays}); err != nil {
					return err
				}
			}
		}
		if len(req.ReminderWording) > 0 && req.Enabled {
			in := map[int]string{}
			for k, v := range req.ReminderWording {
				n, err := strconv.Atoi(k)
				if err != nil {
					return errors.Join(errBadRequest, errors.New("reminder_wording is keyed by rung: 1, 2 or 3"))
				}
				in[n] = v
			}
			was, err := schools.SetReminderWording(r.Context(), tx, p.TenantID, in, p.Actor())
			if err != nil {
				return schoolErr(err)
			}
			if err := auditTx(r, tx, "settings.schools.reminder_wording", p.TenantID.String(), map[string]any{"rungs": slices.Sorted(maps.Keys(req.ReminderWording)), "before": wordingOut(was)}); err != nil {
				return err
			}
		}
		if req.PlanGraceDays != nil && req.Enabled {
			was, err := schools.SetPlanGraceDays(r.Context(), tx, p.TenantID, *req.PlanGraceDays, p.Actor())
			if err != nil {
				return schoolErr(err)
			}
			if was != *req.PlanGraceDays {
				if err := auditTx(r, tx, "settings.schools.plan_grace_days", p.TenantID.String(), map[string]any{"from": was, "to": *req.PlanGraceDays}); err != nil {
					return err
				}
			}
		}
		if req.Issuing != nil && req.Enabled {
			was, err := schools.SetIssuing(r.Context(), tx, p.TenantID, *req.Issuing, p.Actor())
			if err != nil {
				return err
			}
			if was != *req.Issuing {
				if err := auditTx(r, tx, "settings.schools.issuing", p.TenantID.String(), map[string]any{"from": was, "to": *req.Issuing}); err != nil {
					return err
				}
			}
		}
		if threshold, err = schools.DiscountThreshold(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		if cash, err = schools.CashThreshold(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		if linkDays, err = schools.FamilyLinkDays(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		if grace, err = schools.PlanGraceDays(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		if wording, err = schools.CustomWording(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		if issuing, err = schools.GetIssuing(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		return tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM roles WHERE name = $1)`, bursarRole.Name).Scan(&bursar)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	issuers, err := s.schoolsSvc().Issuers(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": req.Enabled, "bursar_role": bursar, "discount_threshold_naira": threshold / 100, "cash_threshold_naira": nairaOrNil(cash),
		"family_link_days": linkDays, "issuing": issuing, "issuers": issuers, "plan_grace_days": grace, "reminder_wording": wordingOut(wording), "reminder_defaults": wordingOut(schools.DefaultWording),
		"reminder_placeholders": schools.ReminderPlaceholders})
}

// --- masking ---

// schoolName masks a student's or guardian's name as invoices' customers
// are, an erased one left as it is.
func schoolName(n string) string {
	if n == schools.Erased {
		return n
	}
	return maskName(n)
}

func maskStudent(st *schools.Student) {
	st.Name, st.DateOfBirth = schoolName(st.Name), ""
}

func maskFamily(f *schools.Family) {
	for i := range f.Guardians {
		g := &f.Guardians[i]
		g.Name = schoolName(g.Name)
		if len(g.Phone) > 4 {
			g.Phone = "•••" + g.Phone[len(g.Phone)-4:]
		}
		if at := strings.IndexByte(g.Email, '@'); at > 0 {
			g.Email = g.Email[:1] + "•••" + g.Email[at:]
		}
	}
	for i := range f.Students {
		f.Students[i].Name = schoolName(f.Students[i].Name)
	}
}

func maskDetail(d *schools.StudentDetail) {
	maskStudent(&d.Student)
	if d.Family != nil {
		maskFamily(d.Family)
	}
}

// --- classes ---

func (s *Server) listSchoolClasses(w http.ResponseWriter, r *http.Request) {
	var list []schools.Class
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = s.schoolsSvc().Classes(r.Context(), tx, principalFrom(r.Context()).TenantID)
		return schoolErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"classes": list})
}

func (s *Server) addSchoolClass(w http.ResponseWriter, r *http.Request) {
	var in schools.ClassIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var c schools.Class
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if c, err = s.schoolsSvc().AddClass(r.Context(), tx, p.TenantID, in, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.class.add", c.ID.String(), map[string]any{"class": c.Label, "order": c.Order})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) changeSchoolClass(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in schools.ClassIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var c schools.Class
	if err := s.tx(r, func(tx pgx.Tx) error {
		before, err := s.schoolsSvc().Class(r.Context(), tx, p.TenantID, id)
		if err != nil {
			return schoolErr(err)
		}
		if c, err = s.schoolsSvc().ChangeClass(r.Context(), tx, p.TenantID, id, in); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.class.change", id.String(), map[string]any{"from": map[string]any{"class": before.Label, "order": before.Order},
			"to": map[string]any{"class": c.Label, "order": c.Order}})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) removeSchoolClass(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		c, err := s.schoolsSvc().RemoveClass(r.Context(), tx, p.TenantID, id)
		if err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.class.remove", id.String(), map[string]any{"class": c.Label})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- sessions and terms ---

func (s *Server) listSchoolSessions(w http.ResponseWriter, r *http.Request) {
	var list []schools.Session
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = s.schoolsSvc().Sessions(r.Context(), tx, principalFrom(r.Context()).TenantID)
		return schoolErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": list})
}

func (s *Server) addSchoolSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Label string `json:"label"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var x schools.Session
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if x, err = s.schoolsSvc().AddSession(r.Context(), tx, p.TenantID, req.Label, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.session.add", x.ID.String(), map[string]any{"session": x.Label})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, x)
}

func termAudit(t schools.Term) map[string]any {
	return map[string]any{"term": t.Name, "session": t.Session, "starts_on": t.StartsOn, "ends_on": t.EndsOn, "fees_due_on": t.FeesDueOn}
}

func (s *Server) addSchoolTerm(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in schools.TermIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var t schools.Term
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if t, err = s.schoolsSvc().AddTerm(r.Context(), tx, p.TenantID, id, in, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.term.add", t.ID.String(), termAudit(t))
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) changeSchoolTerm(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in schools.TermIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var t schools.Term
	if err := s.tx(r, func(tx pgx.Tx) error {
		before, after, err := s.schoolsSvc().ChangeTerm(r.Context(), tx, p.TenantID, id, in)
		if err != nil {
			return schoolErr(err)
		}
		t = after
		return auditTx(r, tx, "school.term.change", id.String(), map[string]any{"from": termAudit(before), "to": termAudit(after)})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) removeSchoolTerm(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		t, err := s.schoolsSvc().RemoveTerm(r.Context(), tx, p.TenantID, id)
		if err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.term.remove", id.String(), termAudit(t))
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// startSchoolTerm makes a term current and enrols the students, each
// once.
func (s *Server) startSchoolTerm(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out schools.Started
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out, err = s.schoolsSvc().StartTerm(r.Context(), tx, p.TenantID, id, p.Actor()); err != nil {
			return schoolErr(err)
		}
		detail := termAudit(out.Term)
		detail["enrolled"] = out.Enrolled
		if out.Previous != nil {
			detail["previous"] = out.Previous.ID
		}
		return auditTx(r, tx, "school.term.start", id.String(), detail)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listSchoolEnrollments(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var list []schools.Enrollment
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = s.schoolsSvc().Enrollments(r.Context(), tx, principalFrom(r.Context()).TenantID, id)
		return schoolErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enrollments": list})
}

// --- promotion ---

func (s *Server) getSchoolPromotion(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var out schools.Promotion
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = s.schoolsSvc().Promotion(r.Context(), tx, principalFrom(r.Context()).TenantID, id, nil)
		return schoolErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	for i := range out.Students {
		out.Students[i].Name = schoolName(out.Students[i].Name)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) promoteSchoolSession(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req struct {
		Exceptions map[uuid.UUID]string `json:"exceptions"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out schools.Promotion
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out, err = s.schoolsSvc().Promote(r.Context(), tx, p.TenantID, id, req.Exceptions, p.Actor()); err != nil {
			return schoolErr(err)
		}
		ex := map[string]string{}
		for st, a := range req.Exceptions {
			ex[st.String()] = a
		}
		return auditTx(r, tx, "school.promote", id.String(), map[string]any{"session": out.Session, "counts": out.Counts, "exceptions": ex})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	for i := range out.Students {
		out.Students[i].Name = schoolName(out.Students[i].Name)
	}
	writeJSON(w, http.StatusOK, out)
}

// --- students ---

func (s *Server) listSchoolStudents(w http.ResponseWriter, r *http.Request) {
	rev, ok := revealing(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := schools.Filter{Status: q.Get("status"), Query: q.Get("q"), Names: rev}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	if c := q.Get("class"); c != "" {
		id, err := uuid.Parse(c)
		if err != nil {
			s.fail(w, r, fmt.Errorf("%w: class is a class's id", errBadRequest))
			return
		}
		f.ClassID = &id
	}
	p := principalFrom(r.Context())
	var list []schools.Student
	var total int
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if list, total, err = s.schoolsSvc().Students(r.Context(), tx, p.TenantID, f); err != nil {
			return schoolErr(err)
		}
		if rev {
			return auditTx(r, tx, "pii.reveal", "school_students", map[string]any{"students": len(list)})
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !rev {
		for i := range list {
			maskStudent(&list[i])
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"students": list, "total": total, "revealed": rev})
}

func (s *Server) getSchoolStudent(w http.ResponseWriter, r *http.Request) {
	rev, ok := revealing(w, r)
	if !ok {
		return
	}
	// An admission number may hold a slash, sent escaped (GCA%2F001).
	idOrNumber, err := url.PathUnescape(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	p := principalFrom(r.Context())
	var d schools.StudentDetail
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if d, err = s.schoolsSvc().Student(r.Context(), tx, p.TenantID, idOrNumber); err != nil {
			return schoolErr(err)
		}
		if rev {
			return auditTx(r, tx, "pii.reveal", "school_student:"+d.AdmissionNumber, nil)
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !rev {
		maskDetail(&d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"student": d, "revealed": rev})
}

func (s *Server) addSchoolStudent(w http.ResponseWriter, r *http.Request) {
	var in schools.StudentIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var d schools.StudentDetail
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if d, err = s.schoolsSvc().AddStudent(r.Context(), tx, p.TenantID, in, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.student.add", d.ID.String(), map[string]any{"admission_number": d.AdmissionNumber, "class_id": d.ClassID,
			"family_id": d.FamilyID, "enrolled": len(d.Enrollments) > 0})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	maskDetail(&d)
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) changeSchoolStudent(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var c schools.StudentChange
	if err := decodeBody(r, &c); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var d schools.StudentDetail
	if err := s.tx(r, func(tx pgx.Tx) error {
		var changed schools.Changed
		var err error
		if d, changed, err = s.schoolsSvc().ChangeStudent(r.Context(), tx, p.TenantID, id, c, p.Actor()); err != nil {
			return schoolErr(err)
		}
		if len(changed) == 0 {
			return nil
		}
		changed["admission_number_now"] = d.AdmissionNumber
		return auditTx(r, tx, "school.student.change", id.String(), changed)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	maskDetail(&d)
	writeJSON(w, http.StatusOK, d)
}

// importSchoolStudents serves POST /v1/schools/students/import: {csv,
// confirm?}. Without confirm it answers the plan; with the plan's hash it
// saves it. A file with any line that cannot be taken saves nothing and
// answers 422 with each line and why.
func (s *Server) importSchoolStudents(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CSV     string `json:"csv"`
		Confirm string `json:"confirm"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, schools.MaxImportBytes+64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.fail(w, r, errors.Join(errBadRequest, err))
		return
	}
	p := principalFrom(r.Context())
	var plan schools.ImportPlan
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if plan, err = s.schoolsSvc().Import(r.Context(), tx, p.TenantID, req.CSV, req.Confirm, p.Actor()); err != nil {
			return err
		}
		if !plan.Saved {
			return nil
		}
		return auditTx(r, tx, "school.students.import", plan.Hash, map[string]any{"added": plan.Added, "updated": plan.Updated, "families": len(plan.Families)})
	})
	var ie *schools.ImportError
	if errors.As(err, &ie) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": ie.Message, "code": "import_unreadable", "missing": ie.Missing, "problems": ie.Problems, "total": ie.Total})
		return
	}
	if err != nil {
		s.fail(w, r, schoolErr(err))
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// --- families ---

func (s *Server) listSchoolFamilies(w http.ResponseWriter, r *http.Request) {
	rev, ok := revealing(w, r)
	if !ok {
		return
	}
	p := principalFrom(r.Context())
	var list []schools.Family
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if list, err = s.schoolsSvc().Families(r.Context(), tx, p.TenantID); err != nil {
			return schoolErr(err)
		}
		if rev {
			return auditTx(r, tx, "pii.reveal", "school_families", map[string]any{"families": len(list)})
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !rev {
		for i := range list {
			maskFamily(&list[i])
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"families": list, "revealed": rev})
}

func (s *Server) getSchoolFamily(w http.ResponseWriter, r *http.Request) {
	rev, ok := revealing(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var f schools.Family
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if f, err = s.schoolsSvc().Family(r.Context(), tx, p.TenantID, id); err != nil {
			return schoolErr(err)
		}
		if rev {
			return auditTx(r, tx, "pii.reveal", "school_family:"+id.String(), nil)
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !rev {
		maskFamily(&f)
	}
	writeJSON(w, http.StatusOK, map[string]any{"family": f, "revealed": rev})
}

func (s *Server) addSchoolFamily(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Guardians []schools.GuardianIn `json:"guardians"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var f schools.Family
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if f, err = s.schoolsSvc().AddFamily(r.Context(), tx, p.TenantID, req.Guardians, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.family.add", f.ID.String(), map[string]any{"guardians": len(f.Guardians)})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	maskFamily(&f)
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) addSchoolGuardian(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var g schools.GuardianIn
	if err := decodeBody(r, &g); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var f schools.Family
	if err := s.tx(r, func(tx pgx.Tx) error {
		var gid uuid.UUID
		var err error
		if f, gid, err = s.schoolsSvc().AddGuardian(r.Context(), tx, p.TenantID, id, g, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.guardian.add", id.String(), map[string]any{"guardian": gid})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	maskFamily(&f)
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) changeSchoolGuardian(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	gid, err := pathID(r, "guardian")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var g schools.GuardianIn
	if err := decodeBody(r, &g); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var f schools.Family
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if f, err = s.schoolsSvc().ChangeGuardian(r.Context(), tx, p.TenantID, id, gid, g, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.guardian.change", id.String(), map[string]any{"guardian": gid})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	maskFamily(&f)
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) removeSchoolGuardian(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	gid, err := pathID(r, "guardian")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var f schools.Family
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if f, err = s.schoolsSvc().RemoveGuardian(r.Context(), tx, p.TenantID, id, gid); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.guardian.remove", id.String(), map[string]any{"guardian": gid})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	maskFamily(&f)
	writeJSON(w, http.StatusOK, f)
}

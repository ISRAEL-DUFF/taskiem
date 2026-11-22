package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/engine/connector"
	phonenum "github.com/israel-duff/taskiem/engine/phone"
)

// Payroll is Payrolla's (P4a; docs/spec/build-plan-saved-tools-payments.md
// P4): Payrolla calculates, approves and pays it, and Taskiem shows it in
// its own screens through Payrolla's Connect API, on the business's
// Payrolla connection. Nothing here moves money, approves a payroll or
// changes an employee. What people earn is shown only to those holding
// payroll.read (the owner's alone unless given to a role), whatever the
// key's scopes.

const payrollaID = "payrolla"

// payrollRun is a run as the Payroll page lists it: Payrolla's run summary.
type payrollRun struct {
	RunID          string `json:"run_id"`
	PeriodStart    string `json:"period_start"`
	PeriodEnd      string `json:"period_end"`
	PayDate        string `json:"pay_date"`
	RunType        string `json:"run_type"`
	Status         string `json:"status"`
	EmployeeCount  int64  `json:"employee_count"`
	TotalGrossKobo int64  `json:"total_gross_kobo"`
	TotalNetKobo   int64  `json:"total_net_kobo"`
	// Waiting: approval is wanted (COMPUTED), in Payrolla until its approve
	// API exists.
	Waiting bool `json:"awaiting_approval"`
}

func runOf(m map[string]any) payrollRun {
	r := payrollRun{RunID: strOf(m["run_id"]), PeriodStart: strOf(m["period_start"]), PeriodEnd: strOf(m["period_end"]), PayDate: strOf(m["pay_date"]),
		RunType: strOf(m["run_type"]), Status: strOf(m["status"]), EmployeeCount: intOf(m["employee_count"]),
		TotalGrossKobo: intOf(m["total_gross_kobo"]), TotalNetKobo: intOf(m["total_net_kobo"])}
	r.Waiting = r.Status == "COMPUTED"
	return r
}

func strOf(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func intOf(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

// payrollaConnected is the business's active Payrolla connection's name
// (the named one, or the first), or "" when it has none.
func (s *Server) payrollaConnected(ctx context.Context, tenant uuid.UUID, connection string) (name string, mode *string, err error) {
	err = s.tenantTx(ctx, tenant, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `SELECT name, mode FROM connections WHERE environment = 'prod' AND connector = $1 AND status = 'active' AND ($2 = '' OR name = $2)
			ORDER BY created_at LIMIT 1`, payrollaID, connection).Scan(&name, &mode)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		return e
	})
	return name, mode, err
}

// payrolla runs one Payrolla read on the connection named.
func (s *Server) payrolla(ctx context.Context, connection, action string, in map[string]any) (map[string]any, error) {
	out, err := s.connectorAction(ctx, principalFrom(ctx), payrollaID, "Payrolla", connection, action, "payroll."+action, in)
	if err != nil {
		return nil, payrollaRefusal(err)
	}
	return out, nil
}

// payrollaRefusal says what a Payrolla refusal means for the business, as
// a refusal the screens show: a key without the permission, a key revoked,
// a run that is not there, Payrolla not answering.
func payrollaRefusal(err error) error {
	var pe *payrolla.Error
	switch {
	case errors.As(err, &pe) && pe.Code == "SCOPE_REQUIRED":
		return refusePay(http.StatusConflict, "scope_required", "Your Payrolla key cannot read this: in Payrolla, Settings → Connected apps, make a key with the Read payroll and Read employees permissions and reconnect it in Taskiem.")
	case errors.Is(err, connector.ErrCredentialsRejected) || errors.As(err, &pe) && strings.HasPrefix(pe.Code, "API_KEY"):
		return refusePay(http.StatusConflict, "reconnect", "Payrolla no longer accepts the key: make a new one in Payrolla, Settings → Connected apps, and reconnect it in Taskiem.")
	case errors.Is(err, connector.ErrNotFound):
		return pgx.ErrNoRows
	case isLookup(err):
		return refusePay(http.StatusBadGateway, "payrolla_down", "Payrolla did not answer just now; try again in a minute.")
	}
	return err
}

// getPayroll is GET /v1/payroll {cursor?, connection?}: the company in
// Payrolla and its runs, newest first ({connected: false} without a
// Payrolla connection).
func (s *Server) getPayroll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	q := r.URL.Query()
	name, mode, err := s.payrollaConnected(ctx, p.TenantID, q.Get("connection"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if name == "" {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false, "runs": []payrollRun{}})
		return
	}
	co, err := s.payrolla(ctx, name, "get_company", map[string]any{})
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	in := map[string]any{"limit": "20"}
	if c := strings.TrimSpace(q.Get("cursor")); c != "" {
		in["cursor"] = c
	}
	list, err := s.payrolla(ctx, name, "list_runs", in)
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	runs := []payrollRun{}
	for _, x := range anyList(list["runs"]) {
		if m, ok := x.(map[string]any); ok {
			runs = append(runs, runOf(m))
		}
	}
	company, _ := co["company"].(map[string]any)
	out := map[string]any{"connected": true, "connection": name, "test": mode != nil && *mode == "test",
		"company": map[string]any{"name": strOf(company["name"]), "pay_frequency": strOf(company["pay_frequency"])}, "runs": runs}
	if c := strOf(list["next_cursor"]); c != "" {
		out["next_cursor"] = c
	}
	writeJSON(w, http.StatusOK, out)
}

// payrollLine is one person's pay in a run: Payrolla's line, with whether
// it arrived in plain terms and the staff member it is in Taskiem.
type payrollLine struct {
	PayslipID  string `json:"payslip_id"`
	EmployeeID string `json:"employee_id"`
	Name       string `json:"name"`
	NetKobo    int64  `json:"net_kobo"`
	// Status is Payrolla's; State is paid, waiting, failed or returned.
	Status    string           `json:"status"`
	State     string           `json:"state"`
	Reference string           `json:"reference,omitempty"`
	Reason    string           `json:"reason,omitempty"`
	Staff     *payrollStaffRef `json:"staff"`
}

type payrollStaffRef struct {
	UserID uuid.UUID `json:"user_id"`
	Name   string    `json:"name"`
}

// lineState is a payout status in plain terms.
func lineState(status string) string {
	switch status {
	case "CONFIRMED":
		return "paid"
	case "FAILED", "PERMANENTLY_FAILED":
		return "failed"
	case "REVERSED":
		return "returned"
	}
	return "waiting"
}

// getPayrollRun is GET /v1/payroll/runs/{run} {connection?}: one run, its
// timeline, each person's pay and whether it arrived, who was not paid and
// why, and Payrolla's approval link while it waits for approval.
func (s *Server) getPayrollRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	run := strings.TrimSpace(chi.URLParam(r, "run"))
	if _, err := uuid.Parse(run); err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	name, _, err := s.payrollaConnected(ctx, p.TenantID, r.URL.Query().Get("connection"))
	if err == nil && name == "" {
		err = refusePay(http.StatusConflict, "not_connected", "Connect Payrolla first.")
	}
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	got, err := s.payrolla(ctx, name, "get_run", map[string]any{"run_id": run})
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	detail, _ := got["run"].(map[string]any)
	if detail == nil {
		s.payFail(w, r, pgx.ErrNoRows)
		return
	}
	ls, err := s.payrolla(ctx, name, "get_run_lines", map[string]any{"run_id": run})
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	linked, err := s.payrollaLinks(ctx, p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	lines := []payrollLine{}
	counts := map[string]int{"paid": 0, "waiting": 0, "failed": 0, "returned": 0}
	for _, x := range anyList(ls["lines"]) {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		l := payrollLine{PayslipID: strOf(m["payslip_id"]), EmployeeID: strOf(m["employee_id"]),
			Name:    strings.TrimSpace(strOf(m["first_name"]) + " " + strOf(m["last_name"])),
			NetKobo: intOf(m["net_kobo"]), Status: strOf(m["disbursement_status"]), Reference: strOf(m["reference"]), Reason: strOf(m["reason"])}
		l.State = lineState(l.Status)
		counts[l.State]++
		if st, ok := linked[l.EmployeeID]; ok {
			l.Staff = &st
		}
		lines = append(lines, l)
	}
	out := map[string]any{"run": map[string]any{"summary": runOf(detail), "created_at": detail["created_at"], "updated_at": detail["updated_at"],
		"approved_at": detail["approved_at"], "locked_at": detail["locked_at"]}, "lines": lines, "counts": counts, "connection": name}
	if u := strOf(detail["approval_url"]); u != "" && strings.HasPrefix(u, "https://") {
		out["approval_url"] = u
	}
	writeJSON(w, http.StatusOK, out)
}

// payrollaLinks are the business's staff linked to Payrolla employees, by
// employee id.
func (s *Server) payrollaLinks(ctx context.Context, tenant uuid.UUID) (map[string]payrollStaffRef, error) {
	out := map[string]payrollStaffRef{}
	err := s.tenantTx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT sp.payrolla_employee_id, u.id, u.name FROM staff_profiles sp JOIN users u ON u.id = sp.user_id
			JOIN memberships m ON m.tenant_id = sp.tenant_id AND m.user_id = sp.user_id
			WHERE sp.tenant_id = $1 AND sp.payrolla_employee_id IS NOT NULL GROUP BY 1, 2, 3`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var emp string
			var ref payrollStaffRef
			if err := rows.Scan(&emp, &ref.UserID, &ref.Name); err != nil {
				return err
			}
			out[emp] = ref
		}
		return rows.Err()
	})
	return out, err
}

// payrollaEmployee is an employee in Payrolla as the linking screen shows
// it: their name, and the email and phone they are matched by.
type payrollaEmployee struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Department string `json:"department,omitempty"`
	Status     string `json:"status"`
	email      string
	phone      string
	// LinkedTo is the staff member already linked to them.
	LinkedTo *uuid.UUID `json:"linked_to"`
}

func employeeOf(m map[string]any) payrollaEmployee {
	e := payrollaEmployee{ID: strOf(m["id"]), Name: strings.TrimSpace(strOf(m["first_name"]) + " " + strOf(m["last_name"])),
		Department: strOf(m["department"]), Status: strOf(m["status"]), email: strings.ToLower(strOf(m["email"]))}
	if n, err := phonenum.Normalise(strOf(m["phone"])); err == nil {
		e.phone = n
	}
	return e
}

// payrollaEmployees are the company's employees in Payrolla, up to 500,
// those who left last.
func (s *Server) payrollaEmployees(ctx context.Context, connection string) ([]payrollaEmployee, error) {
	var out []payrollaEmployee
	cursor := ""
	for range 5 {
		in := map[string]any{"limit": "100"}
		if cursor != "" {
			in["cursor"] = cursor
		}
		got, err := s.payrolla(ctx, connection, "list_employees", in)
		if err != nil {
			return nil, err
		}
		for _, x := range anyList(got["employees"]) {
			if m, ok := x.(map[string]any); ok {
				out = append(out, employeeOf(m))
			}
		}
		if cursor = strOf(got["next_cursor"]); cursor == "" {
			break
		}
	}
	return out, nil
}

// payrollStaff is a staff member on the linking screen: who they are in
// Payrolla, or who they look to be (the same email or phone) for the owner
// to confirm.
type payrollStaff struct {
	UserID    uuid.UUID          `json:"user_id"`
	Name      string             `json:"name"`
	Payrolla  *staffPayrolla     `json:"payrolla"`
	Suggested *payrollSuggestion `json:"suggested"`
}

type payrollSuggestion struct {
	EmployeeID string `json:"employee_id"`
	Name       string `json:"name"`
	// By is what matched: email or phone.
	By string `json:"by"`
}

// staffPayrolla is who a staff member is in Payrolla.
type staffPayrolla struct {
	EmployeeID   string     `json:"employee_id"`
	Name         string     `json:"name"`
	LinkedByName string     `json:"linked_by_name,omitempty"`
	LinkedAt     *time.Time `json:"linked_at,omitempty"`
}

// staffPayrollaTx is who the staff member is in Payrolla, or nil.
func staffPayrollaTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID) (*staffPayrolla, error) {
	var emp, name, by *string
	var at *time.Time
	err := tx.QueryRow(ctx, `SELECT payrolla_employee_id, payrolla_name, payrolla_linked_by, payrolla_linked_at FROM staff_profiles WHERE tenant_id = $1 AND user_id = $2`,
		tenant, user).Scan(&emp, &name, &by, &at)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && emp == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := &staffPayrolla{EmployeeID: *emp, LinkedAt: at}
	if name != nil {
		out.Name = *name
	}
	if by != nil {
		_ = tx.QueryRow(ctx, `SELECT COALESCE((SELECT name FROM users WHERE id::text = taskiem_actor_human($1)), '')`, strings.TrimPrefix(*by, "user:")).Scan(&out.LinkedByName)
	}
	return out, nil
}

// getPayrollStaff is GET /v1/payroll/staff {connection?}: the business's
// staff, each with who they are in Payrolla or who they look to be (the
// same sign-in email or chat number), and Payrolla's employees for
// choosing by hand.
func (s *Server) getPayrollStaff(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	name, _, err := s.payrollaConnected(ctx, p.TenantID, r.URL.Query().Get("connection"))
	if err == nil && name == "" {
		err = refusePay(http.StatusConflict, "not_connected", "Connect Payrolla first.")
	}
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	emps, err := s.payrollaEmployees(ctx, name)
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	var people []staffMember
	staff := []payrollStaff{}
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if people, err = staffPeopleTx(ctx, tx, p, uuid.Nil, func(n string) string { return n }); err != nil {
			return err
		}
		for _, m := range people {
			ps := payrollStaff{UserID: m.UserID, Name: m.Name}
			if ps.Payrolla, err = staffPayrollaTx(ctx, tx, p.TenantID, m.UserID); err != nil {
				return err
			}
			staff = append(staff, ps)
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	byEmp := map[string]int{}
	for i := range emps {
		byEmp[emps[i].ID] = i
	}
	for i, m := range people {
		if l := staff[i].Payrolla; l != nil {
			if j, ok := byEmp[l.EmployeeID]; ok {
				id := m.UserID
				emps[j].LinkedTo = &id
			}
		}
	}
	// A suggestion: the one employee, not yet linked and still employed,
	// with the staff member's email or chat number.
	for i, m := range people {
		if staff[i].Payrolla != nil {
			continue
		}
		email := strings.ToLower(strings.TrimSpace(m.Email))
		phone, _ := phonenum.Normalise(m.Phone)
		var found []payrollSuggestion
		for _, e := range emps {
			if e.LinkedTo != nil || e.Status == "TERMINATED" {
				continue
			}
			switch {
			case email != "" && e.email == email:
				found = append(found, payrollSuggestion{EmployeeID: e.ID, Name: e.Name, By: "email"})
			case phone != "" && e.phone == phone:
				found = append(found, payrollSuggestion{EmployeeID: e.ID, Name: e.Name, By: "phone"})
			}
		}
		if len(found) == 1 {
			staff[i].Suggested = &found[0]
		}
	}
	if emps == nil {
		emps = []payrollaEmployee{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"staff": staff, "employees": emps, "connection": name})
}

// putStaffPayrolla is PUT /v1/staff/{user}/payrolla {employee_id?,
// connection?}: an owner confirms who a staff member is in Payrolla (left
// out, the link is removed). Payroll messages about that employee then
// reach the staff member's own chat.
func (s *Server) putStaffPayrolla(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	user, err := uuid.Parse(chi.URLParam(r, "user"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var in struct {
		EmployeeID *string `json:"employee_id"`
		Connection string  `json:"connection"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if !isOwnerOf(p) {
		s.payFail(w, r, refusePay(http.StatusForbidden, "owner_only", "Only an owner says who a staff member is in Payrolla."))
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		var staff bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memberships WHERE tenant_id = $1 AND user_id = $2)`, p.TenantID, user).Scan(&staff); err != nil {
			return err
		}
		if !staff {
			return pgx.ErrNoRows
		}
		return nil
	}); err != nil {
		s.payFail(w, r, err)
		return
	}
	var emp, empName *string
	if in.EmployeeID != nil && strings.TrimSpace(*in.EmployeeID) != "" {
		id := strings.TrimSpace(*in.EmployeeID)
		if _, err := uuid.Parse(id); err != nil {
			s.payFail(w, r, refusePay(http.StatusBadRequest, "no_employee", "That is not an employee in Payrolla."))
			return
		}
		name, _, err := s.payrollaConnected(ctx, p.TenantID, in.Connection)
		if err == nil && name == "" {
			err = refusePay(http.StatusConflict, "not_connected", "Connect Payrolla first.")
		}
		if err != nil {
			s.payFail(w, r, err)
			return
		}
		got, err := s.payrolla(ctx, name, "get_employee", map[string]any{"employee_id": id})
		if errors.Is(err, pgx.ErrNoRows) {
			err = refusePay(http.StatusNotFound, "no_employee", "There is no such employee in the business's Payrolla.")
		}
		if err != nil {
			s.payFail(w, r, err)
			return
		}
		m, _ := got["employee"].(map[string]any)
		e := employeeOf(m)
		if e.ID != id {
			s.payFail(w, r, refusePay(http.StatusNotFound, "no_employee", "There is no such employee in the business's Payrolla."))
			return
		}
		emp, empName = &id, &e.Name
	}
	var out *staffPayrolla
	err = s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO staff_profiles (tenant_id, user_id, payrolla_employee_id, payrolla_name, payrolla_linked_by, payrolla_linked_at, updated_by)
			VALUES ($1, $2, $3, $4, $5, now(), $5)
			ON CONFLICT (tenant_id, user_id) DO UPDATE SET payrolla_employee_id = EXCLUDED.payrolla_employee_id, payrolla_name = EXCLUDED.payrolla_name,
				payrolla_linked_by = EXCLUDED.payrolla_linked_by, payrolla_linked_at = EXCLUDED.payrolla_linked_at`,
			p.TenantID, user, emp, empName, p.Actor()); err != nil {
			var pg *pgconn.PgError
			if errors.As(err, &pg) && pg.Code == "23505" {
				return refusePay(http.StatusConflict, "taken", "Another staff member is already that employee in Payrolla.")
			}
			return err
		}
		var err error
		if out, err = staffPayrollaTx(ctx, tx, p.TenantID, user); err != nil {
			return err
		}
		detail := map[string]any{"user_id": user}
		if emp != nil {
			detail["employee_id"] = *emp
		}
		return auditTx(r, tx, "staff.payrolla", user.String(), detail)
	})
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"payrolla": out})
}

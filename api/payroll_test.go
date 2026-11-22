package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/engine/egress"
)

const (
	runID   = "11111111-1111-4111-8111-111111111111"
	empMusa = "22222222-2222-4222-8222-222222222222"
	empAda  = "33333333-3333-4333-8333-333333333333"
	empLong = "77777777-7777-4777-8777-777777777777"
)

// fakeConnect is Payrolla's Connect API read side: the company, a run
// waiting for approval with three lines, and two employees.
func fakeConnect(t *testing.T, scopes ...string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pyk_test_good" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"code":"API_KEY_INVALID","message":"revoked"}}`))
			return
		}
		var data any
		switch strings.TrimPrefix(r.URL.Path, "/api/v1") {
		case "/connect/company":
			data = map[string]any{"company": map[string]any{"id": "c1", "name": "Ada Foods Ltd", "pay_frequency": "MONTHLY"},
				"key": map[string]any{"id": "k1", "name": "Taskiem", "kind": "CONNECTED_APP", "scopes": scopes}}
		case "/connect/runs":
			data = map[string]any{"runs": []any{map[string]any{"run_id": runID, "period_start": "2026-11-01", "period_end": "2026-11-30", "pay_date": "2026-11-28",
				"run_type": "REGULAR", "status": "COMPUTED", "employee_count": 3, "total_gross_kobo": 90000000, "total_net_kobo": 75000000}}, "next_cursor": ""}
		case "/connect/runs/" + runID:
			data = map[string]any{"run": map[string]any{"run_id": runID, "period_start": "2026-11-01", "period_end": "2026-11-30", "pay_date": "2026-11-28",
				"run_type": "REGULAR", "status": "COMPUTED", "employee_count": 3, "total_gross_kobo": 90000000, "total_net_kobo": 75000000,
				"created_at": "2026-11-20T09:00:00Z", "updated_at": "2026-11-21T09:00:00Z", "approval_url": "https://payrolla.test/approve/" + runID}}
		case "/connect/runs/" + runID + "/lines":
			data = map[string]any{"lines": []any{
				map[string]any{"payslip_id": "p1", "employee_id": empMusa, "first_name": "Musa", "last_name": "Bello", "net_kobo": 25000000, "disbursement_status": "CONFIRMED", "reference": "TRF1"},
				map[string]any{"payslip_id": "p2", "employee_id": empAda, "first_name": "Ada", "last_name": "Obi", "net_kobo": 25000000, "disbursement_status": "FAILED", "reason": "Account closed"},
				map[string]any{"payslip_id": "p3", "employee_id": "44444444-4444-4444-8444-444444444444", "first_name": "Femi", "last_name": "Ade", "net_kobo": 25000000, "disbursement_status": "PENDING"},
			}}
		case "/connect/employees":
			data = map[string]any{"employees": []any{
				map[string]any{"id": empMusa, "first_name": "Musa", "last_name": "Bello", "email": "Musa@Payroll.test", "phone": "08031234567", "department": "Kitchen", "status": "ACTIVE"},
				map[string]any{"id": empAda, "first_name": "Ada", "last_name": "Obi", "email": "ada.obi@elsewhere.test", "phone": "", "department": "Front", "status": "ACTIVE"},
			}, "next_cursor": ""}
		case "/connect/employees/" + empLong:
			data = map[string]any{"employee": map[string]any{"id": empLong, "first_name": strings.Repeat("Ọ", 150), "last_name": strings.Repeat("b", 30), "status": "ACTIVE"}}
		case "/connect/employees/" + empMusa:
			data = map[string]any{"employee": map[string]any{"id": empMusa, "first_name": "Musa", "last_name": "Bello", "email": "musa@payroll.test", "status": "ACTIVE"}}
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"no such thing"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Payroll in Taskiem, read from Payrolla: the page's runs, a run's lines
// with who was not paid and why and its approval link; staff suggested by
// the same email and linked by an owner; what people earn only for those
// holding payroll.read.
func TestPayroll(t *testing.T) {
	w := newWAWorld(t)
	w.srv.Egress = &egress.Guard{Blocked: func(netip.Addr) bool { return false }}
	fake := fakeConnect(t, "payroll:read", "employees:read")
	if err := w.env.Registry.Register(payrolla.New(payrolla.Options{BaseURL: fake.URL + "/api/v1", LiveURL: fake.URL + "/api/v1", TestURL: fake.URL + "/api/v1"})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada Foods", "ada@payroll.test")

	// Not connected: the page says so.
	if got := owner.must(200, "GET", "/v1/payroll", nil); got["connected"] != false {
		t.Fatalf("not connected: %v", got)
	}
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "payrolla@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"api_key": "pyk_test_good"}})

	page := owner.must(200, "GET", "/v1/payroll", nil)
	runs := page["runs"].([]any)
	if page["connected"] != true || page["company"].(map[string]any)["name"] != "Ada Foods Ltd" || len(runs) != 1 || runs[0].(map[string]any)["awaiting_approval"] != true {
		t.Fatalf("the page: %v", page)
	}

	// Staff: Musa is suggested by his email; the owner confirms.
	musa := w.member(t, owner, "musa@payroll.test", "admin")
	musaID := idOf(t, musa)
	var sugg map[string]any
	for _, x := range owner.must(200, "GET", "/v1/payroll/staff", nil)["staff"].([]any) {
		if m := x.(map[string]any); m["user_id"] == musaID {
			sugg, _ = m["suggested"].(map[string]any)
		}
	}
	if sugg == nil || sugg["employee_id"] != empMusa || sugg["by"] != "email" {
		t.Fatalf("suggested: %v", sugg)
	}
	path := "/v1/staff/" + musaID + "/payrolla"
	musa.must(403, "PUT", path, map[string]any{"employee_id": empMusa})
	owner.must(404, "PUT", path, map[string]any{"employee_id": "55555555-5555-4555-8555-555555555555"})
	if got := owner.must(200, "PUT", path, map[string]any{"employee_id": empMusa})["payrolla"].(map[string]any); got["name"] != "Musa Bello" {
		t.Fatalf("linked: %v", got)
	}
	if got := owner.must(200, "GET", "/v1/staff/"+musaID, nil)["payrolla"].(map[string]any); got["employee_id"] != empMusa || got["linked_by_name"] == "" {
		t.Fatalf("on their page: %v", got)
	}
	// One employee is one staff member.
	other := w.member(t, owner, "femi@payroll.test", "cashier")
	if e := owner.must(409, "PUT", "/v1/staff/"+idOf(t, other)+"/payrolla", map[string]any{"employee_id": empMusa}); e["code"] != "taken" {
		t.Fatalf("taken: %v", e)
	}

	// The run: each line in plain terms, Musa named as staff, the
	// approval link while it waits.
	got := owner.must(200, "GET", "/v1/payroll/runs/"+runID, nil)
	lines := got["lines"].([]any)
	if len(lines) != 3 || got["approval_url"] != "https://payrolla.test/approve/"+runID {
		t.Fatalf("the run: %v", got)
	}
	first, second := lines[0].(map[string]any), lines[1].(map[string]any)
	if first["state"] != "paid" || first["staff"].(map[string]any)["user_id"] != musaID || second["state"] != "failed" || second["reason"] != "Account closed" || second["staff"] != nil {
		t.Fatalf("lines: %v", lines)
	}
	if c := got["counts"].(map[string]any); c["paid"] != float64(1) || c["failed"] != float64(1) || c["waiting"] != float64(1) {
		t.Fatalf("counts: %v", c)
	}
	owner.must(404, "GET", "/v1/payroll/runs/66666666-6666-4666-8666-666666666666", nil)

	// What people earn: not for an admin unless given.
	musa.must(403, "GET", "/v1/payroll", nil)
	musa.must(403, "GET", "/v1/payroll/runs/"+runID, nil)

	// Unlinked: no one is left as having linked them.
	if got := owner.must(200, "PUT", path, map[string]any{}); got["payrolla"] != nil {
		t.Fatalf("unlinked: %v", got)
	}
	var linked bool
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT payrolla_linked_by IS NOT NULL OR payrolla_linked_at IS NOT NULL FROM staff_profiles WHERE user_id = $1`, musaID).Scan(&linked); err != nil || linked {
		t.Fatalf("still says who linked: %v %v", linked, err)
	}

	// A name longer than kept is cut, not refused.
	if got := owner.must(200, "PUT", path, map[string]any{"employee_id": empLong})["payrolla"].(map[string]any); len([]rune(got["name"].(string))) != 160 {
		t.Fatalf("long name: %d", len([]rune(got["name"].(string))))
	}
}

// Someone who leaves the business, however they leave, takes their
// Payrolla link and saved pay account with them: the employee is free for
// the staff member who replaces them, and links left by removals before
// this are cleared (00513) or ignored.
func TestPayrollLeavers(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.Egress = &egress.Guard{Blocked: func(netip.Addr) bool { return false }}
	fake := fakeConnect(t, "payroll:read", "employees:read")
	if err := w.env.Registry.Register(payrolla.New(payrolla.Options{BaseURL: fake.URL + "/api/v1", LiveURL: fake.URL + "/api/v1", TestURL: fake.URL + "/api/v1"})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada Foods", "ada@leavers.test")
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "payrolla@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"api_key": "pyk_test_good"}})
	links := func(user string) (emp *string, saved bool) {
		t.Helper()
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT payrolla_employee_id, payrolla_name IS NOT NULL OR payrolla_linked_by IS NOT NULL OR payrolla_linked_at IS NOT NULL
			OR saved_linked_by IS NOT NULL OR saved_linked_at IS NOT NULL FROM staff_profiles WHERE user_id = $1`, user).Scan(&emp, &saved); err != nil {
			t.Fatal(err)
		}
		return emp, saved
	}
	link := func(user string) {
		t.Helper()
		owner.must(200, "PUT", "/v1/staff/"+user+"/payrolla", map[string]any{"employee_id": empMusa})
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE staff_profiles SET saved_linked_by = 'user:test', saved_linked_at = now() WHERE user_id = $1`, user); err != nil {
			t.Fatal(err)
		}
	}

	// Removed from the business.
	musa := idOf(t, w.member(t, owner, "musa@leavers.test", "cashier"))
	link(musa)
	owner.must(204, "POST", "/v1/staff/"+musa+"/remove", nil)
	if emp, left := links(musa); emp != nil || left {
		t.Fatalf("removed, still linked: %v %v", emp, left)
	}
	owner.must(404, "PUT", "/v1/staff/"+musa+"/payrolla", map[string]any{"employee_id": empMusa})

	// Their last role taken away; another role kept leaves the link.
	bola := idOf(t, w.member(t, owner, "bola@leavers.test", "cashier", "packer"))
	link(bola)
	owner.must(204, "DELETE", "/v1/members/"+bola+"/roles/packer", nil)
	if emp, _ := links(bola); emp == nil {
		t.Fatal("a role left, unlinked")
	}
	owner.must(204, "DELETE", "/v1/members/"+bola+"/roles/cashier", nil)
	if emp, left := links(bola); emp != nil || left {
		t.Fatalf("last role taken, still linked: %v %v", emp, left)
	}

	// Deprovisioned by the business's SCIM provider.
	key := owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "SCIM", "permissions": []string{"scim.provision"}})["key"].(string)
	scim := &client{t: t, base: w.base, token: key}
	ct := []string{"Content-Type", "application/scim+json"}
	kemi := idOf(t, w.member(t, owner, "kemi@leavers.test", "cashier"))
	link(kemi)
	kemiSCIM := scim.must(201, "POST", "/scim/v2/Users", map[string]any{"userName": "kemi@leavers.test"}, ct...)["id"].(string)
	scim.must(200, "PATCH", "/scim/v2/Users/"+kemiSCIM, map[string]any{"Operations": []any{map[string]any{"op": "replace", "path": "active", "value": false}}}, ct...)
	if emp, left := links(kemi); emp != nil || left {
		t.Fatalf("deprovisioned, still linked: %v %v", emp, left)
	}

	// A link left from before: the employee is not taken by them, and
	// the backfill clears it.
	femi := idOf(t, w.member(t, owner, "femi@leavers.test", "cashier"))
	link(femi)
	if _, err := w.env.DB.Admin.Exec(ctx, `DELETE FROM memberships WHERE user_id = $1`, femi); err != nil {
		t.Fatal(err)
	}
	tunde := idOf(t, w.member(t, owner, "tunde@leavers.test", "cashier"))
	owner.must(200, "PUT", "/v1/staff/"+tunde+"/payrolla", map[string]any{"employee_id": empMusa})
	if emp, _ := links(femi); emp != nil {
		t.Fatalf("the leaver kept the employee: %v", *emp)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE staff_profiles SET payrolla_employee_id = $2, payrolla_name = 'X', payrolla_linked_by = 'user:test', payrolla_linked_at = now(),
		saved_linked_by = 'user:test', saved_linked_at = now() WHERE user_id = $1`, femi, empAda); err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../engine/db/migrations/00513_staff_links_leavers.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.SplitN(strings.SplitN(string(up), "-- +goose Up", 2)[1], "-- +goose Down", 2)[0]
	if _, err := w.env.DB.Admin.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
	if emp, left := links(femi); emp != nil || left {
		t.Fatalf("backfill: %v %v", emp, left)
	}
	if emp, _ := links(tunde); emp == nil || *emp != empMusa {
		t.Fatal("backfill unlinked a member")
	}
}

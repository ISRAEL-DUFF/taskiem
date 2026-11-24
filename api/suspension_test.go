package api_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// Repair jobs of a suspended tenant wait until it is resumed.
func TestSuspendedTenantRepairsWait(t *testing.T) {
	rw := newRepairWorld(t, patcher{class: "data", replace: [2]string{missingCustomer, defaulted}}.respond)
	ctx := context.Background()
	rw.owner.must(204, "PUT", "/v1/secrets/prod/charge_memo", map[string]any{"value": "m"})
	wf := rw.publish(t, stepCharge+","+stepVerify+","+stepNotify)
	tenant := uuid.MustParse(rw.owner.must(200, "GET", "/v1/me", nil)["tenant_id"].(string))
	run := rw.owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"amount": 500, "id": "o-s"}})["run_id"].(string)
	rw.env.Drain(t)
	if _, err := rw.env.DB.Admin.Exec(ctx, `UPDATE tenants SET status = 'suspended' WHERE id = $1`, tenant); err != nil {
		t.Fatal(err)
	}
	if n, err := rw.srv.RepairOnce(ctx); err != nil || n != 0 {
		t.Fatalf("repairs while suspended: %d %v", n, err)
	}
	if _, err := rw.env.DB.Admin.Exec(ctx, `UPDATE tenants SET status = 'active' WHERE id = $1`, tenant); err != nil {
		t.Fatal(err)
	}
	if n, err := rw.srv.RepairOnce(ctx); err != nil || n != 1 {
		t.Fatalf("repairs after resume: %d %v", n, err)
	}
	if p := rw.only(t, run); p["status"] != "proposed" {
		t.Errorf("proposal: %v", p)
	}
}

// Suspending a sub-tenant through the partner API records when (its work
// is held: engine/ingest TestSuspendedTenantDoesNoNewWork); resuming
// records when, and its schedules continue from then.
func TestSubTenantSuspensionHoldsWork(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	p := w.partner(t, "Wagely", "owner@wagely.test")
	sub := p.sub(t, "Customer", nil)
	app, _ := p.app(t, map[string]any{"name": "api", "headless": true, "allowed_connectors": []string{},
		"end_user_permissions": []string{"workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start"}})
	u := p.endUser(t, w, app, sub, "u", "", "workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start")
	wf := u.call(201, "POST", "/workflows", map[string]any{"name": "a", "definition": json.RawMessage(transformFlow)})["id"].(string)
	u.call(200, "POST", "/workflows/"+wf+"/versions/1/publish", nil)
	before := time.Now().Add(-time.Second)
	p.key.must(200, "POST", "/v1/partner/sub-tenants/"+sub+"/suspend", nil)
	var suspendedAt, resumedAt *time.Time
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT suspended_at, resumed_at FROM tenants WHERE id = $1`, sub).Scan(&suspendedAt, &resumedAt); err != nil ||
		suspendedAt == nil || suspendedAt.Before(before) || resumedAt != nil {
		t.Fatalf("suspended: %v %v %v", suspendedAt, resumedAt, err)
	}
	p.key.must(409, "POST", "/v1/partner/sub-tenants/"+sub+"/suspend", nil)
	// Its end users are out.
	u.call(401, "GET", "/workflows", nil)
	p.key.must(200, "POST", "/v1/partner/sub-tenants/"+sub+"/resume", nil)
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT resumed_at FROM tenants WHERE id = $1`, sub).Scan(&resumedAt); err != nil || resumedAt == nil || resumedAt.Before(*suspendedAt) {
		t.Fatalf("resumed: %v %v", resumedAt, err)
	}
	u2 := p.endUser(t, w, app, sub, "u", "", "run.start", "run.read")
	u2.call(201, "POST", "/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"name": "x"}})
}

// A sub-tenant a Taskiem operator suspended stays suspended until an
// operator resumes it (F40): its partner can still read it and revoke its
// end users' tokens, but cannot resume it or write into it. An operator can
// also take over a suspension the partner made. Once the operator resumes
// it, the partner suspends and resumes it as before.
func TestOperatorSuspensionHoldsAgainstPartner(t *testing.T) {
	ctx := context.Background()
	w := opsWorld(t)
	p := w.partner(t, "Wagely", "owner@wagely.test")
	sub := p.sub(t, "Customer", nil)
	c, a := enrolOperator(t, w, "reviewer@taskiem.test")
	status := func() (st string, by *string) {
		t.Helper()
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status, suspended_by FROM tenants WHERE id = $1`, sub).Scan(&st, &by); err != nil {
			t.Fatal(err)
		}
		return st, by
	}
	base := "/v1/partner/sub-tenants/" + sub

	// The partner suspends it; an operator then holds it as Taskiem's.
	p.key.must(200, "POST", base+"/suspend", nil)
	c.must(200, "POST", "/v1/ops/tenants/"+sub+"/suspend", map[string]any{"reason": "fraud report", "step_up": c.stepUp(a, "ops.tenant.suspend", sub)})
	if st, by := status(); st != "suspended" || by == nil || *by != "operator" {
		t.Fatalf("after the operator's suspension: %s %v", st, by)
	}

	p.key.must(403, "POST", base+"/resume", nil)
	p.key.must(403, "POST", base+"/invitations", map[string]any{"email": "x@customer.test", "roles": []string{"viewer"}})
	p.key.must(403, "PUT", base+"/roles/clerk", map[string]any{"permissions": []string{"run.read"}})
	p.key.must(403, "PUT", base+"/limits", map[string]any{"runs_per_day": 10})
	if st, _ := status(); st != "suspended" {
		t.Fatalf("the partner resumed an operator's suspension: %s", st)
	}
	// Reading it still works.
	p.key.must(200, "GET", base+"/runs", nil)
	p.key.must(200, "GET", base+"/invitations", nil)
	p.key.must(200, "GET", base+"/connections", nil)
	p.key.must(200, "GET", "/v1/partner/sub-tenants", nil)

	// The application role cannot lift the hold by writing the row itself.
	err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{uuid.MustParse(sub)}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tenants SET status = 'active' WHERE id = $1`, sub)
		return err
	})
	if err == nil {
		t.Fatal("an application transaction lifted an operator's suspension")
	}
	err = db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{uuid.MustParse(sub)}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tenants SET suspended_by = 'partner' WHERE id = $1`, sub)
		return err
	})
	if err == nil {
		t.Fatal("an application transaction changed who holds a suspension")
	}

	c.must(200, "POST", "/v1/ops/tenants/"+sub+"/resume", map[string]any{"reason": "cleared", "step_up": c.stepUp(a, "ops.tenant.resume", sub)})
	if st, by := status(); st != "active" || by != nil {
		t.Fatalf("after the operator's resume: %s %v", st, by)
	}
	p.key.must(200, "POST", base+"/suspend", nil)
	if st, by := status(); st != "suspended" || by == nil || *by != "partner" {
		t.Fatalf("after the partner's suspension: %s %v", st, by)
	}
	p.key.must(201, "POST", base+"/invitations", map[string]any{"email": "y@customer.test", "roles": []string{"viewer"}})
	p.key.must(200, "POST", base+"/resume", nil)
}

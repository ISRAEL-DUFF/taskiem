package api_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
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
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
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

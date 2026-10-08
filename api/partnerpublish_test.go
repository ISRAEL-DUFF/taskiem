package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// With four-eyes publishing on in a sub-tenant, an end user's publish is a
// request a person decides: the partner (as its key's owner) or a member
// of the sub-tenant with workflow.publish. Nothing is deployed before.
func TestEndUserFourEyesPublish(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	sub := p.sub(t, "Customer", nil)
	other := p.sub(t, "Other", nil)
	app, _ := p.app(t, map[string]any{"name": "api", "headless": true, "allowed_connectors": []string{},
		"end_user_permissions": []string{"workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start"}})
	u := p.endUser(t, w, app, sub, "u", "", "workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start")
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO governance_settings (tenant_id, four_eyes_publish, updated_by) VALUES ($1, true, 'operator')`, sub); err != nil {
		t.Fatal(err)
	}
	wf := u.call(201, "POST", "/workflows", map[string]any{"name": "a", "definition": json.RawMessage(transformFlow)})["id"].(string)
	got := u.call(202, "POST", "/workflows/"+wf+"/versions/1/publish", nil)
	if got["state"] != "pending_approval" {
		t.Fatalf("publish: %v", got)
	}
	if a := u.call(200, "GET", "/workflows/"+wf, nil)["workflow"].(map[string]any)["active_version"]; a != nil {
		t.Fatalf("deployed before approval: %v", a)
	}
	u.call(409, "POST", "/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"name": "x"}})

	// The partner sees the request and approves it as its key's owner.
	reqs := p.key.must(200, "GET", "/v1/partner/sub-tenants/"+sub+"/publish-requests", nil)["requests"].([]any)
	if len(reqs) != 1 || reqs[0].(map[string]any)["workflow_id"] != wf {
		t.Fatalf("requests: %v", reqs)
	}
	endUserID := reqs[0].(map[string]any)["requested_by"].(string)
	if n := len(p.key.must(200, "GET", "/v1/partner/sub-tenants/"+other+"/publish-requests", nil)["requests"].([]any)); n != 0 {
		t.Errorf("another sub-tenant lists %d", n)
	}
	p.key.must(404, "POST", "/v1/partner/sub-tenants/"+other+"/publish-requests/"+wf+"/1/approve", nil)
	if got := p.key.must(200, "POST", "/v1/partner/sub-tenants/"+sub+"/publish-requests/"+wf+"/1/approve", map[string]any{"comment": "ok"}); got["state"] != "published" {
		t.Fatalf("approve: %v", got)
	}
	p.key.must(409, "POST", "/v1/partner/sub-tenants/"+sub+"/publish-requests/"+wf+"/1/approve", nil)
	if a := u.call(200, "GET", "/workflows/"+wf, nil)["workflow"].(map[string]any)["active_version"]; a != float64(1) {
		t.Fatalf("not deployed: %v", a)
	}
	u.call(201, "POST", "/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"name": "x"}})
	var publishedBy, decidedBy string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT v.published_by::text, q.decided_by::text FROM workflow_versions v JOIN publish_requests q USING (workflow_id, version)
		WHERE v.workflow_id = $1 AND v.version = 1`, uuid.MustParse(wf)).Scan(&publishedBy, &decidedBy); err != nil {
		t.Fatal(err)
	}
	ownerID := p.owner.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)["id"].(string)
	if publishedBy != ownerID || decidedBy != ownerID || decidedBy == endUserID {
		t.Errorf("published by %s, decided by %s; the key's owner is %s", publishedBy, decidedBy, ownerID)
	}
	for _, a := range []string{"publish_request.create", "publish_request.approve", "workflow.publish"} {
		if n := auditCount(t, w, sub, a); n != 1 {
			t.Errorf("%s audited %d times in the sub-tenant", a, n)
		}
	}
	if n := auditCount(t, w, p.id.String(), "partner.subtenant.publish.approve"); n != 1 {
		t.Errorf("the partner's chain has %d approvals", n)
	}

	// Version 2: a member of the sub-tenant with workflow.publish approves.
	v2 := strings.Replace(transformFlow, "hello ", "hi ", 1)
	u.call(201, "POST", "/workflows/"+wf+"/versions", map[string]any{"definition": json.RawMessage(v2)})
	u.call(202, "POST", "/workflows/"+wf+"/versions/2/publish", nil)
	ops := w.tenant(t, "Ops", "ops@ops.test")
	opsID := ops.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)["id"].(string)
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'admin')`, sub, uuid.MustParse(opsID)); err != nil {
		t.Fatal(err)
	}
	anon := &client{t: t, base: w.base}
	member := &client{t: t, base: w.base, token: anon.must(200, "POST", "/v1/auth/login", map[string]any{"email": "ops@ops.test", "password": testPassword, "tenant_id": sub, "bearer": true})["token"].(string)}
	pending := member.must(200, "GET", "/v1/publish-requests", nil)["requests"].([]any)
	if len(pending) != 1 || pending[0].(map[string]any)["requested_by"] != endUserID {
		t.Fatalf("member's list: %v", pending)
	}
	member.must(200, "POST", "/v1/workflows/"+wf+"/versions/2/publish/approve", map[string]any{})
	if a := u.call(200, "GET", "/workflows/"+wf, nil)["workflow"].(map[string]any)["active_version"]; a != float64(2) {
		t.Fatalf("version 2 not deployed: %v", a)
	}

	// Version 3: the partner rejects it; it stays a draft.
	u.call(201, "POST", "/workflows/"+wf+"/versions", map[string]any{"definition": json.RawMessage(transformFlow)})
	u.call(202, "POST", "/workflows/"+wf+"/versions/3/publish", nil)
	if got := p.key.must(200, "POST", "/v1/partner/sub-tenants/"+sub+"/publish-requests/"+wf+"/3/reject", map[string]any{"comment": "no"}); got["state"] != "draft" {
		t.Errorf("reject: %v", got)
	}
	if a := u.call(200, "GET", "/workflows/"+wf, nil)["workflow"].(map[string]any)["active_version"]; a != float64(2) {
		t.Errorf("after rejecting: %v", a)
	}
	// A partner key with read only cannot decide.
	ro := p.owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "ro", "permissions": []string{"partner.read"}})["key"].(string)
	u.call(201, "POST", "/workflows/"+wf+"/versions", map[string]any{"definition": json.RawMessage(v2)})
	u.call(202, "POST", "/workflows/"+wf+"/versions/4/publish", nil)
	(&client{t: t, base: w.base, token: ro}).must(403, "POST", "/v1/partner/sub-tenants/"+sub+"/publish-requests/"+wf+"/4/approve", nil)
}

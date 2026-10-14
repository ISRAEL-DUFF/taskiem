package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/runtime"
)

const payrollPackage = `{"schema":"wd/v1","id":"wf_payroll","version":1,"name":"payroll",
  "trigger":{"type":"webhook","config":{"path":"/payrolla/payroll-approved","auth":"hmac"}},
  "steps":[
    {"id":"approve","type":"approval","config":{"role":"payroll_approver","timeout":"24h","subject":{"payroll":"=trigger.body.payroll_id"}}},
    {"id":"report","type":"http","needs":["approve"],"config":{"method":"POST","url":"=env.payrolla_api_url + '/reports'",
      "headers":{"Authorization":"='Bearer ' + secrets.payrolla_api_token"},"body":{"status":"=steps.approve.output.decision"},"class":"idempotent_write","idempotency_header":"Idempotency-Key"}}]}`

// secretProbe hands a secret to a connector, so a run shows whose secret
// it read.
const secretProbe = `{"schema":"wd/v1","id":"wf_probe","version":1,"name":"probe","trigger":{"type":"manual"},
  "steps":[{"id":"use","type":"connector","connector":"fakepay@1","action":"notify","retry":{"max":0},"input":{"logical_id":"=secrets.partner_only"}}]}`

// sharePackage creates a workflow in the partner's own tenant and shares it.
func sharePackage(t *testing.T, p *partner, doc string) string {
	t.Helper()
	out := p.owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Payroll", "definition": json.RawMessage(doc)})
	if probs := out["problems"].([]any); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	wf := out["id"].(string)
	p.key.must(200, "PUT", "/v1/partner/workflows/"+wf+"/share", nil)
	return wf
}

// A partner installs a shared workflow into a sub-tenant: the copy is the
// sub-tenant's, published there, with its variables and roles; the answer
// carries the webhook URLs and which secrets are missing, never a value.
// A newer version arrives as a draft. Other partners and other sub-tenants
// are out of reach.
func TestWorkflowPackages(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	sub := p.sub(t, "Company 1042", nil)
	other := p.sub(t, "Company 7", nil)
	rival := w.partner(t, "Rival", "owner@rival.test")
	rivalSub := rival.sub(t, "Rival customer", nil)
	p.owner.must(204, "PUT", "/v1/secrets/prod/payrolla_api_token", map[string]any{"value": "PARTNER-OWN-TOKEN"})

	pkg := sharePackage(t, p, payrollPackage)
	p.key.must(404, "PUT", "/v1/partner/workflows/"+uuid.NewString()+"/share", nil)
	shared := p.key.must(200, "GET", "/v1/partner/workflows", nil)["workflows"].([]any)
	if len(shared) != 1 || shared[0].(map[string]any)["id"] != pkg || shared[0].(map[string]any)["latest_version"] != float64(1) {
		t.Fatalf("shared: %v", shared)
	}
	if n := len(rival.key.must(200, "GET", "/v1/partner/workflows", nil)["workflows"].([]any)); n != 0 {
		t.Errorf("the rival sees %d packages", n)
	}

	install := map[string]any{"package": pkg, "environments": []string{"dev", "prod"},
		"variables": map[string]any{"prod": map[string]any{"payrolla_api_url": "https://api.payrolla.example"}},
		"roles":     []map[string]any{{"name": "payroll_approver", "permissions": []string{"approval.decide", "run.read", "workflow.read"}}}}
	// Not the rival's package, nor its sub-tenant; not a variable the workflow does not read.
	rival.key.must(400, "POST", "/v1/partner/sub-tenants/"+rivalSub+"/workflows", install)
	rival.key.must(404, "POST", "/v1/partner/sub-tenants/"+sub+"/workflows", install)
	p.key.must(404, "POST", "/v1/partner/sub-tenants/"+rivalSub+"/workflows", install)
	p.key.must(400, "POST", "/v1/partner/sub-tenants/"+sub+"/workflows", map[string]any{"package": pkg, "variables": map[string]any{"prod": map[string]any{"funding_wallet_id": "w1"}}})
	p.key.must(400, "POST", "/v1/partner/sub-tenants/"+sub+"/workflows", map[string]any{"package": pkg, "environments": []string{"staging"}})

	got := p.key.must(201, "POST", "/v1/partner/sub-tenants/"+sub+"/workflows", install)
	if got["state"] != "published" || got["version"] != float64(1) || got["package_version"] != float64(1) || toJSON(got["roles_created"]) != `["payroll_approver"]` {
		t.Fatalf("install: %v", got)
	}
	wf := got["workflow_id"].(string)
	eps := got["endpoints"].([]any)
	if len(eps) != 2 {
		t.Fatalf("endpoints: %v", eps)
	}
	prod := eps[1].(map[string]any)
	if prod["environment"] != "prod" || prod["url"] != "/hooks/"+sub+"/payrolla/payroll-approved?env=prod" || prod["auth"] != "hmac" ||
		prod["secret_name"] != "webhook_wf_payroll" || prod["deployed_version"] != float64(1) {
		t.Errorf("prod endpoint: %v", prod)
	}
	if toJSON(got["variables_missing"]) != `[{"environment":"dev","name":"payrolla_api_url"}]` {
		t.Errorf("variables missing: %v", got["variables_missing"])
	}
	secrets := toJSON(got["secrets"])
	if !strings.Contains(secrets, `{"environment":"prod","name":"payrolla_api_token","set":false}`) || !strings.Contains(secrets, `{"environment":"prod","name":"webhook_wf_payroll","set":false}`) {
		t.Errorf("secrets: %s", secrets)
	}
	if strings.Contains(toJSON(got), "PARTNER-OWN-TOKEN") {
		t.Fatal("a secret in the answer")
	}

	// The copy is the sub-tenant's; nothing of it is in the partner's or the other sub-tenant's.
	var owner uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT tenant_id FROM workflows WHERE id = $1`, uuid.MustParse(wf)).Scan(&owner); err != nil || owner.String() != sub {
		t.Fatalf("installed into %s (%v)", owner, err)
	}
	var vars, roles int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM variables WHERE tenant_id = $1 AND name = 'payrolla_api_url'), (SELECT count(*) FROM roles WHERE tenant_id = $1)`,
		uuid.MustParse(other)).Scan(&vars, &roles); err != nil || vars != 0 || roles != 0 {
		t.Errorf("the other sub-tenant got %d variables, %d roles (%v)", vars, roles, err)
	}
	if n := len(p.key.must(200, "GET", "/v1/partner/sub-tenants/"+other+"/installs", nil)["installs"].([]any)); n != 0 {
		t.Errorf("the other sub-tenant has %d installs", n)
	}
	rival.key.must(404, "GET", "/v1/partner/sub-tenants/"+sub+"/installs", nil)

	// A newer version is a draft: production keeps version 1.
	v2 := strings.Replace(payrollPackage, `"timeout":"24h"`, `"timeout":"48h"`, 1)
	p.owner.must(201, "POST", "/v1/workflows/"+pkg+"/versions", map[string]any{"definition": json.RawMessage(v2)})
	up := p.key.must(200, "POST", "/v1/partner/sub-tenants/"+sub+"/workflows", map[string]any{"package": pkg})
	if up["state"] != "draft" || up["version"] != float64(2) || up["package_version"] != float64(2) || up["workflow_id"] != wf {
		t.Fatalf("upgrade: %v", up)
	}
	if again := p.key.must(200, "POST", "/v1/partner/sub-tenants/"+sub+"/workflows", map[string]any{"package": pkg, "version": 2}); again["state"] != "unchanged" || again["version"] != float64(2) {
		t.Errorf("same version again: %v", again)
	}
	installs := p.key.must(200, "GET", "/v1/partner/sub-tenants/"+sub+"/installs", nil)["installs"].([]any)
	if len(installs) != 1 {
		t.Fatalf("installs: %v", installs)
	}
	in := installs[0].(map[string]any)
	if in["active_version"] != float64(1) || in["latest_version"] != float64(2) || in["package_version"] != float64(2) || len(in["endpoints"].([]any)) != 2 {
		t.Errorf("install: %v", in)
	}
	for tenant, want := range map[string]map[string]int{
		p.id.String(): {"partner.workflow.share": 1, "partner.subtenant.workflow.install": 3},
		sub:           {"partner.subtenant.workflow.install": 3, "workflow.create": 1, "workflow.publish": 1, "workflow.version.create": 1, "variable.write": 1, "role.put": 1},
	} {
		for action, n := range want {
			if got := auditCount(t, w, tenant, action); got != n {
				t.Errorf("%s in %s: %d, want %d", action, tenant, got, n)
			}
		}
	}

	// The sub-tenant's own people make the signing secret on the Endpoints page.
	adaID := uuid.Must(uuid.NewV7())
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO users (id, email, name, password_hash) SELECT $1, 'ada@company.test', 'Ada', password_hash FROM users WHERE email = 'owner@payrolla.test'`, adaID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'admin')`, uuid.MustParse(sub), adaID); err != nil {
		t.Fatal(err)
	}
	ada := w.login(t, "ada@company.test", testPassword)
	ada.must(409, "POST", "/v1/workflows/"+wf+"/signing-secret", map[string]any{"environment": "staging"})
	sec := ada.must(201, "POST", "/v1/workflows/"+wf+"/signing-secret", map[string]any{"environment": "prod"})
	if sec["secret_name"] != "webhook_wf_payroll" || !strings.HasPrefix(sec["secret"].(string), "whsec_") {
		t.Fatalf("signing secret: %v", sec)
	}
	if v, err := w.env.Vault.Get(ctx, uuid.MustParse(sub), "prod", "webhook_wf_payroll"); err != nil || v != sec["secret"] {
		t.Errorf("vault: %q %v", v, err)
	}
	if _, err := w.env.Vault.Get(ctx, p.id, "prod", "webhook_wf_payroll"); err == nil {
		t.Error("the partner's tenant has the sub-tenant's signing secret")
	}
	// The partner's key reaches no tenant route of the sub-tenant.
	if st, _ := p.key.do("POST", "/v1/workflows/"+wf+"/signing-secret", map[string]any{"environment": "prod"}); st != 404 && st != 403 {
		t.Errorf("the partner made a signing secret: %d", st)
	}
	again := p.key.must(200, "POST", "/v1/partner/sub-tenants/"+sub+"/workflows", map[string]any{"package": pkg})
	if !strings.Contains(toJSON(again["secrets"]), `{"environment":"prod","name":"webhook_wf_payroll","set":true}`) || strings.Contains(toJSON(again), sec["secret"].(string)) {
		t.Errorf("after the secret: %v", again["secrets"])
	}

	// A read-only key installs nothing; unsharing stops new installs.
	ro := &client{t: t, base: w.base, token: p.owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "ro", "permissions": []string{"partner.read"}})["key"].(string)}
	ro.must(403, "POST", "/v1/partner/sub-tenants/"+other+"/workflows", map[string]any{"package": pkg})
	p.key.must(200, "DELETE", "/v1/partner/workflows/"+pkg+"/share", nil)
	p.key.must(404, "DELETE", "/v1/partner/workflows/"+pkg+"/share", nil)
	p.key.must(400, "POST", "/v1/partner/sub-tenants/"+other+"/workflows", map[string]any{"package": pkg})
}

// Under four-eyes in the sub-tenant, installing opens a publish request
// the partner's own key cannot approve.
func TestPackageFourEyes(t *testing.T) {
	w := newWorld(t)
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	sub := p.sub(t, "Company", nil)
	if _, err := w.env.DB.Admin.Exec(context.Background(), `INSERT INTO governance_settings (tenant_id, four_eyes_publish, updated_by) VALUES ($1, true, 'operator')`, sub); err != nil {
		t.Fatal(err)
	}
	pkg := sharePackage(t, p, payrollPackage)
	got := p.key.must(201, "POST", "/v1/partner/sub-tenants/"+sub+"/workflows", map[string]any{"package": pkg,
		"roles": []map[string]any{{"name": "payroll_approver", "permissions": []string{"approval.decide"}}}})
	if got["state"] != "pending_approval" || got["endpoints"].([]any)[0].(map[string]any)["deployed_version"] != nil {
		t.Fatalf("install: %v", got)
	}
	wf := got["workflow_id"].(string)
	reqs := p.key.must(200, "GET", "/v1/partner/sub-tenants/"+sub+"/publish-requests", nil)["requests"].([]any)
	if len(reqs) != 1 || reqs[0].(map[string]any)["workflow_id"] != wf {
		t.Fatalf("requests: %v", reqs)
	}
	p.key.must(403, "POST", "/v1/partner/sub-tenants/"+sub+"/publish-requests/"+wf+"/1/approve", nil)
}

// An installed workflow runs with the sub-tenant's secrets, never the
// partner's: the same workflow reads the partner's secret in the partner's
// tenant and finds nothing in the sub-tenant.
func TestInstalledWorkflowCannotReadPartnerSecrets(t *testing.T) {
	w := newWorld(t)
	w.env.VaultSecrets = true
	ctx := context.Background()
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	sub := p.sub(t, "Company", nil)
	p.owner.must(204, "PUT", "/v1/secrets/prod/partner_only", map[string]any{"value": "PARTNER-SECRET-VALUE"})
	pkg := sharePackage(t, p, secretProbe)
	p.owner.must(200, "POST", "/v1/workflows/"+pkg+"/versions/1/publish", nil)
	got := p.key.must(201, "POST", "/v1/partner/sub-tenants/"+sub+"/workflows", map[string]any{"package": pkg, "environments": []string{"prod"}})
	if got["state"] != "published" || toJSON(got["secrets"]) != `[{"environment":"prod","name":"partner_only","set":false}]` {
		t.Fatalf("install: %v", got)
	}
	start := func(tenant uuid.UUID, wf string) runtime.RunRef {
		ref, _, err := w.env.Store.StartRun(ctx, runtime.StartRequest{TenantID: tenant, WorkflowID: uuid.MustParse(wf), Version: 1, Environment: "prod", Trigger: map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	inSub := start(uuid.MustParse(sub), got["workflow_id"].(string))
	w.env.Drain(t)
	if st := w.env.Status(t, inSub); st != "failed" {
		t.Errorf("the sub-tenant's run: %s", st)
	}
	if n := w.env.Provider.Executions("PARTNER-SECRET-VALUE"); n != 0 {
		t.Fatalf("the sub-tenant's run used the partner's secret %d times", n)
	}
	inPartner := start(p.id, pkg)
	w.env.Drain(t)
	if st := w.env.Status(t, inPartner); st != "completed" || w.env.Provider.Executions("PARTNER-SECRET-VALUE") != 1 {
		t.Errorf("the partner's own run: %s, %d", st, w.env.Provider.Executions("PARTNER-SECRET-VALUE"))
	}
}

// A partner sets a sub-tenant's max_payload_bytes within its own, which an
// operator raises (a 5,000-employee payroll is close to 1 MB).
func TestSubTenantPayloadLimit(t *testing.T) {
	w := newWorld(t)
	p := w.partner(t, "Payrolla", "owner@payrolla.test")
	sub := p.sub(t, "Company", nil)
	p.key.must(400, "PUT", "/v1/partner/sub-tenants/"+sub+"/limits", map[string]any{"max_payload_bytes": 2 << 20})
	if err := w.env.Store.SetLimits(context.Background(), p.id, map[string]any{"max_payload_bytes": 4 << 20}, "test"); err != nil {
		t.Fatal(err)
	}
	w.env.Store.ForgetLimits(p.id)
	got := p.key.must(200, "PUT", "/v1/partner/sub-tenants/"+sub+"/limits", map[string]any{"max_payload_bytes": 2 << 20})["limits"].(map[string]any)
	if got["max_payload_bytes"] != float64(2<<20) {
		t.Errorf("limits: %v", got)
	}
	p.key.must(400, "PUT", "/v1/partner/sub-tenants/"+sub+"/limits", map[string]any{"max_payload_bytes": 8 << 20})
	p.key.must(400, "PUT", "/v1/partner/sub-tenants/"+sub+"/limits", map[string]any{"max_payload_bytes": 11 << 20})
}

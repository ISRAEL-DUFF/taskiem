package api_test

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

const pw = "correct horse battery"

// addMember adds a member with roles and signs them in.
func addMember(t *testing.T, w *world, owner *client, name string, roles ...string) *client {
	t.Helper()
	owner.must(201, "POST", "/v1/members", map[string]any{"email": name + "@acme.test", "password": pw, "roles": roles})
	return w.login(t, name+"@acme.test", pw)
}

// keyFor issues an API key as c and returns a client using it.
func keyFor(t *testing.T, c *client, env string, perms ...string) *client {
	t.Helper()
	out := c.must(201, "POST", "/v1/api-keys", map[string]any{"name": "k", "permissions": perms, "environment": env})
	return &client{t: t, base: c.base, token: out["key"].(string)}
}

const approvalFlow = `{"schema":"wd/v1","id":"wf_pay","version":1,"name":"pay","trigger":{"type":"manual"},
  "steps":[{"id":"ok","type":"approval","config":{"role":"credit_officer"}}]}`

// An API key is its owner for four-eyes: an owner cannot write a change
// with their key and approve it themselves, and keys approve nothing.
func TestAPIKeyCountsAsItsOwnerForFourEyes(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	bob := addMember(t, w, owner, "bob", "admin")
	owner.must(200, "PUT", "/v1/governance", map[string]any{"four_eyes_publish": true, "four_eyes_policies": true})
	key := keyFor(t, owner, "", "policy.manage", "workflow.edit", "workflow.publish", "workflow.read")

	// Policies.
	if out := key.must(201, "PUT", "/v1/policies/payouts", map[string]any{"document": json.RawMessage(`{"rules":[{"levels":[{"role":"approver"}]}]}`)}); out["state"] != "pending" {
		t.Fatalf("policy: %v", out)
	}
	if st, body := owner.do("POST", "/v1/policies/payouts/versions/1/approve", nil); st != 403 || !strings.Contains(body["error"].(string), "author") {
		t.Errorf("owner approved what their key wrote: %d %v", st, body)
	}
	if st, _ := key.do("POST", "/v1/policies/payouts/versions/1/approve", nil); st != 403 {
		t.Errorf("a key approved a policy: %d", st)
	}
	bob.must(200, "POST", "/v1/policies/payouts/versions/1/approve", nil)

	// Publishing: the version written with the owner's key is the owner's.
	wf := key.must(201, "POST", "/v1/workflows", map[string]any{"name": "pay", "definition": json.RawMessage(approvalFlow)})["id"].(string)
	bob.must(202, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	if st, body := owner.do("POST", "/v1/workflows/"+wf+"/versions/1/publish/approve", nil); st != 403 {
		t.Errorf("owner approved publishing what their key wrote: %d %v", st, body)
	}
}

// A key acts within its owner's current permissions, and stops when they
// leave; a key limited to an environment makes only keys limited to it.
func TestAPIKeyFollowsItsOwner(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	bob := addMember(t, w, owner, "bob", "admin", "viewer")
	key := keyFor(t, bob, "", "workflow.read", "member.manage")
	key.must(200, "GET", "/v1/api-keys", nil)

	bobID := memberID(t, owner, "bob@acme.test")
	owner.must(204, "DELETE", "/v1/members/"+bobID+"/roles/admin", nil)
	key.must(200, "GET", "/v1/workflows", nil) // viewer still reads
	key.must(403, "GET", "/v1/api-keys", nil)  // but no longer manages members
	owner.must(204, "DELETE", "/v1/members/"+bobID+"/roles/viewer", nil)
	key.must(401, "GET", "/v1/workflows", nil)

	// Environment limits carry over to keys made with a limited key.
	dev := keyFor(t, owner, "dev", "member.manage", "secret.manage")
	dev.must(403, "POST", "/v1/api-keys", map[string]any{"name": "wider", "permissions": []string{"secret.manage"}, "environment": "prod"})
	child := keyFor(t, dev, "", "secret.manage")
	child.must(403, "PUT", "/v1/secrets/prod/token", map[string]any{"value": "x"})
	child.must(204, "PUT", "/v1/secrets/dev/token", map[string]any{"value": "x"})
}

// Keys limited to one environment cannot act on all of them, and see only
// their own environment's settings.
func TestEnvironmentLimitedKeysStayInTheirEnvironment(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(204, "PUT", "/v1/secrets/prod/token", map[string]any{"value": "p"})
	owner.must(204, "PUT", "/v1/secrets/dev/token", map[string]any{"value": "d"})
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "pay", "definition": json.RawMessage(approvalFlow)})["id"].(string)
	dev := keyFor(t, owner, "dev", "workflow.publish", "secret.manage", "audit.read", "git.manage", "connection.manage", "workflow.read")

	dev.must(403, "POST", "/v1/environments", map[string]any{"name": "staging"})
	dev.must(403, "PUT", "/v1/environments/prod", map[string]any{"promotion_from": "dev"})
	dev.must(403, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	dev.must(403, "GET", "/v1/audit/export", nil)
	dev.must(403, "GET", "/v1/reports/approvals", nil)
	dev.must(403, "PUT", "/v1/git/dev", map[string]any{"provider": "github", "repo": "a/b", "branch": "main", "mode": "git_led"})
	dev.must(403, "POST", "/v1/git/dev/sync", nil)
	dev.must(200, "GET", "/v1/connections", nil)
	secrets := dev.must(200, "GET", "/v1/secrets", nil)["secrets"].([]any)
	if len(secrets) != 1 || secrets[0].(map[string]any)["environment"] != "dev" {
		t.Errorf("a dev key sees other environments' secrets: %v", secrets)
	}

	// Gates: adding one takes workflow.publish; lifting one takes an owner.
	bob := addMember(t, w, owner, "bob", "admin")
	bob.must(200, "PUT", "/v1/environments/prod", map[string]any{"promotion_from": "dev"})
	bob.must(403, "PUT", "/v1/environments/prod", map[string]any{"promotion_from": ""})
	owner.must(200, "PUT", "/v1/environments/prod", map[string]any{"promotion_from": ""})
}

// The secrets API cannot reach the platform's reserved environments.
func TestSecretsAPIRefusesReservedEnvironments(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	for _, env := range []string{"_identity", "_alerts", "_git"} {
		owner.must(400, "PUT", "/v1/secrets/"+env+"/anything", map[string]any{"value": "x"})
		owner.must(400, "DELETE", "/v1/secrets/"+env+"/anything", nil)
	}
	owner.must(400, "DELETE", "/v1/secrets/prod/not-a-name", nil)
	// Webhook trigger keys stay the tenant's to set.
	owner.must(204, "PUT", "/v1/secrets/prod/webhook_wf_orders", map[string]any{"value": "x"})
}

// Business roles decide who approves money: granting one takes
// approval.decide, and members holding a name cannot have it turned into a
// custom role by someone who could not grant it.
func TestBusinessRolesNeedApprovalDecide(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	adm := addMember(t, w, owner, "adm", "admin")
	adm.must(403, "POST", "/v1/members", map[string]any{"email": "c@acme.test", "password": pw, "roles": []string{"credit_officer"}})
	adm.must(201, "POST", "/v1/members", map[string]any{"email": "b@acme.test", "password": pw, "roles": []string{"builder"}})
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "c@acme.test", "password": pw, "roles": []string{"credit_officer"}})

	// A custom role no approval names is granted on its permissions alone;
	// once an approval names it, it is a business role too.
	owner.must(201, "PUT", "/v1/roles/payout_lead", map[string]any{"permissions": []string{"workflow.read"}})
	adm.must(201, "POST", "/v1/members", map[string]any{"email": "d@acme.test", "password": pw, "roles": []string{"payout_lead"}})
	owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "lead", "definition": json.RawMessage(strings.Replace(approvalFlow, "credit_officer", "payout_lead", 1))})
	adm.must(403, "POST", "/v1/members", map[string]any{"email": "e@acme.test", "password": pw, "roles": []string{"payout_lead"}})

	// credit_officer is held: only someone who could grant it may give it
	// permissions.
	adm.must(403, "PUT", "/v1/roles/credit_officer", map[string]any{"permissions": []string{"workflow.read"}})
	owner.must(201, "PUT", "/v1/roles/credit_officer", map[string]any{"permissions": []string{"workflow.read"}})
}

// Two owners revoking each other at once leave one owner.
func TestLastOwnerSurvivesConcurrentRevokes(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	other := addMember(t, w, owner, "second", "owner")
	ids := []string{memberID(t, owner, "owner@acme.test"), memberID(t, owner, "second@acme.test")}
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i, c := range []*client{owner, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i], _ = c.do("DELETE", "/v1/members/"+ids[1-i]+"/roles/owner", nil)
		}()
	}
	wg.Wait()
	ok := 0
	for _, st := range statuses {
		if st == 204 {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("revokes: %v", statuses)
	}
}

// A delegation ends with the delegator's role, and stops counting at once.
func TestDelegationEndsWithTheRole(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	carol := addMember(t, w, owner, "carol", "approver", "credit_officer")
	dave := addMember(t, w, owner, "dave", "approver")
	carol.must(201, "POST", "/v1/delegations", map[string]any{"to": "dave@acme.test", "roles": []string{"credit_officer"},
		"ends_at": time.Now().Add(24 * time.Hour), "reason": "leave"})
	wf := publishFlow(t, owner, approvalFlow)
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})
	if inbox := dave.must(200, "GET", "/v1/approvals", nil)["approvals"].([]any); len(inbox) != 1 {
		t.Fatalf("dave's inbox under the delegation: %v", inbox)
	}
	owner.must(204, "DELETE", "/v1/members/"+memberID(t, owner, "carol@acme.test")+"/roles/credit_officer", nil)
	if inbox := dave.must(200, "GET", "/v1/approvals", nil)["approvals"].([]any); len(inbox) != 0 {
		t.Errorf("the delegation outlived carol's role: %v", inbox)
	}
	d := owner.must(200, "GET", "/v1/delegations", nil)["delegations"].([]any)[0].(map[string]any)
	if d["revoked_at"] == nil {
		t.Errorf("delegation not revoked: %v", d)
	}
}

// Outside dev, only someone who may publish runs a version other than the
// deployed one.
func TestPinningAnOldVersionNeedsPublish(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	op := addMember(t, w, owner, "op", "operator")
	flow := `{"schema":"wd/v1","id":"wf_n","version":1,"name":"n","trigger":{"type":"manual"},"steps":[{"id":"x","type":"transform","config":{"output":1}}]}`
	wf := publishFlow(t, owner, flow)
	owner.must(201, "POST", "/v1/workflows/"+wf+"/versions", map[string]any{"definition": json.RawMessage(strings.Replace(flow, `"output":1`, `"output":2`, 1))})
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/2/publish", nil)

	op.must(403, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"version": 1, "environment": "prod"})
	op.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"version": 1, "environment": "dev"})
	op.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"version": 2, "environment": "prod"})
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"version": 1, "environment": "prod"})
}

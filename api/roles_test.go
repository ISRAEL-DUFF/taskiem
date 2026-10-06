package api_test

import (
	"strings"
	"testing"
)

func TestCustomRoles(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "adm@acme.test", "password": "correct horse battery", "roles": []string{"admin"}})
	adm := w.login(t, "adm@acme.test", "correct horse battery")

	// A role with what treasury ops need, and nothing else.
	out := owner.must(201, "PUT", "/v1/roles/treasury_ops", map[string]any{"description": "Runs payouts", "permissions": []string{"run.read", "run.start", "workflow.read", "run.resolve"}})
	if strings.Join(toStrings(out["permissions"]), ",") != "run.read,run.resolve,run.start,workflow.read" {
		t.Errorf("permissions %v", out["permissions"])
	}
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "tre@acme.test", "password": "correct horse battery", "roles": []string{"treasury_ops"}})
	tre := w.login(t, "tre@acme.test", "correct horse battery")
	perms := strings.Join(toStrings(tre.must(200, "GET", "/v1/me", nil)["permissions"]), ",")
	if perms != "run.read,run.resolve,run.start,workflow.read" {
		t.Fatalf("member's permissions: %s", perms)
	}
	tre.must(200, "GET", "/v1/runs", nil)
	tre.must(403, "GET", "/v1/members", nil)

	// No escalation: admins hold neither pii.reveal nor pii.erase.
	adm.must(403, "PUT", "/v1/roles/snoop", map[string]any{"permissions": []string{"pii.reveal"}})
	owner.must(201, "PUT", "/v1/roles/privacy", map[string]any{"permissions": []string{"pii.reveal"}})
	adm.must(403, "POST", "/v1/members", map[string]any{"email": "x@acme.test", "password": "correct horse battery", "roles": []string{"privacy"}})
	adm.must(403, "DELETE", "/v1/members/"+memberID(t, owner, "owner@acme.test")+"/roles/owner", nil)
	// Widening a role is held to the same rule.
	adm.must(403, "PUT", "/v1/roles/treasury_ops", map[string]any{"permissions": []string{"run.read", "pii.erase"}})

	owner.must(400, "PUT", "/v1/roles/admin", map[string]any{"permissions": []string{"run.read"}})
	owner.must(400, "PUT", "/v1/roles/x", map[string]any{"permissions": []string{"run.read"}})
	owner.must(400, "PUT", "/v1/roles/ops2", map[string]any{"permissions": []string{"run.everything"}})

	// Narrowed, the change applies at the member's next request.
	owner.must(200, "PUT", "/v1/roles/treasury_ops", map[string]any{"permissions": []string{"run.read"}})
	tre.must(403, "POST", "/v1/runs/00000000-0000-0000-0000-000000000000/cancel", nil)

	// A held role cannot be deleted; revoked, it can. The last owner stays.
	owner.must(409, "DELETE", "/v1/roles/treasury_ops", nil)
	owner.must(204, "DELETE", "/v1/members/"+memberID(t, owner, "tre@acme.test")+"/roles/treasury_ops", nil)
	owner.must(204, "DELETE", "/v1/roles/treasury_ops", nil)
	owner.must(409, "DELETE", "/v1/members/"+memberID(t, owner, "owner@acme.test")+"/roles/owner", nil)
	if st, _ := tre.do("GET", "/v1/me", nil); st != 401 {
		t.Errorf("a member with no roles left still signed in: %d", st)
	}

	roles := owner.must(200, "GET", "/v1/roles", nil)["roles"].([]any)
	if !strings.Contains(toJSON(roles), `"privacy"`) || !strings.Contains(toJSON(roles), `"built_in":true`) {
		t.Errorf("roles %v", roles)
	}
	if n := len(owner.must(200, "GET", "/v1/permissions", nil)["permissions"].([]any)); n < 18 {
		t.Errorf("%d permissions", n)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func memberID(t *testing.T, c *client, email string) string {
	t.Helper()
	for _, m := range c.must(200, "GET", "/v1/members", nil)["members"].([]any) {
		if mm := m.(map[string]any); mm["email"] == email {
			return mm["user_id"].(string)
		}
	}
	t.Fatalf("no member %s", email)
	return ""
}

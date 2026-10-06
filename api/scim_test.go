package api_test

import (
	"strings"
	"testing"
)

// rolesOf returns a member's roles as the member list shows them, or "" if
// they are not a member.
func rolesOf(t *testing.T, c *client, email string) string {
	t.Helper()
	for _, m := range c.must(200, "GET", "/v1/members", nil)["members"].([]any) {
		m := m.(map[string]any)
		if m["email"] == email {
			return strings.Join(toStrings(m["roles"]), ",")
		}
	}
	return ""
}

func TestSCIMProvisioning(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Bank", "owner@bank.test")
	key := owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "Okta SCIM", "permissions": []string{"scim.provision"}})["key"].(string)
	scim := &client{t: t, base: w.base, token: key}
	ct := []string{"Content-Type", "application/scim+json"}

	// Keys only, and only with scim.provision.
	owner.must(401, "GET", "/scim/v2/Users", nil)
	other := owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "runs", "permissions": []string{"run.read"}})["key"].(string)
	if out := (&client{t: t, base: w.base, token: other}).must(403, "GET", "/scim/v2/Users", nil); out["schemas"] == nil {
		t.Errorf("not a SCIM error: %v", out)
	}
	scim.must(200, "GET", "/scim/v2/ServiceProviderConfig", nil)

	owner.must(200, "PUT", "/v1/scim", map[string]any{"default_roles": []string{"viewer"}, "group_roles": map[string]any{"Treasury": []string{"operator"}}})
	owner.must(400, "PUT", "/v1/scim", map[string]any{"default_roles": []string{"owner"}})

	// Create, with the default role.
	ada := scim.must(201, "POST", "/scim/v2/Users", map[string]any{
		"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:User"}, "userName": "ada@bank.test", "externalId": "00u1",
		"name": map[string]any{"givenName": "Ada", "familyName": "Lovelace"}, "emails": []any{map[string]any{"value": "ada@bank.test", "primary": true}},
		"urn:ietf:params:scim:schemas:extension:enterprise:2.0:User": map[string]any{"department": "Treasury"}}, ct...)
	adaID := ada["id"].(string)
	if ada["active"] != true || rolesOf(t, owner, "ada@bank.test") != "viewer" {
		t.Fatalf("created: %v, roles %q", ada, rolesOf(t, owner, "ada@bank.test"))
	}
	if out := scim.must(409, "POST", "/scim/v2/Users", map[string]any{"userName": "ADA@bank.test"}, ct...); out["scimType"] != "uniqueness" {
		t.Errorf("duplicate: %v", out)
	}
	found := scim.must(200, "GET", `/scim/v2/Users?filter=userName+eq+%22Ada%40Bank.test%22`, nil)
	if found["totalResults"] != float64(1) {
		t.Errorf("filter: %v", found)
	}
	if out := scim.must(200, "GET", `/scim/v2/Users?filter=userName+eq+%22nobody%40bank.test%22`, nil); out["totalResults"] != float64(0) {
		t.Errorf("filter for nobody: %v", out)
	}
	scim.must(400, "GET", `/scim/v2/Users?filter=userName+sw+%22a%22`, nil)

	// Groups carry the roles an administrator mapped to them.
	grp := scim.must(201, "POST", "/scim/v2/Groups", map[string]any{"displayName": "Treasury", "members": []any{map[string]any{"value": adaID}}}, ct...)
	gid := grp["id"].(string)
	if got := rolesOf(t, owner, "ada@bank.test"); got != "operator,viewer" {
		t.Fatalf("after joining Treasury: %s", got)
	}
	scim.must(200, "PATCH", "/scim/v2/Groups/"+gid, map[string]any{"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []any{map[string]any{"op": "Remove", "path": `members[value eq "` + adaID + `"]`}}}, ct...)
	if got := rolesOf(t, owner, "ada@bank.test"); got != "viewer" {
		t.Errorf("after leaving Treasury: %s", got)
	}
	scim.must(200, "PATCH", "/scim/v2/Groups/"+gid, map[string]any{"Operations": []any{map[string]any{"op": "add", "path": "members", "value": []any{map[string]any{"value": adaID}}}}}, ct...)
	if got := rolesOf(t, owner, "ada@bank.test"); got != "operator,viewer" {
		t.Errorf("after rejoining: %s", got)
	}
	// Renaming the group to one with no roles takes them away.
	scim.must(200, "PATCH", "/scim/v2/Groups/"+gid, map[string]any{"Operations": []any{map[string]any{"op": "replace", "value": map[string]any{"displayName": "Ops"}}}}, ct...)
	if got := rolesOf(t, owner, "ada@bank.test"); got != "viewer" {
		t.Errorf("after the rename: %s", got)
	}
	// An administrator maps it, and everyone provisioned follows.
	owner.must(200, "PUT", "/v1/scim", map[string]any{"default_roles": []string{"viewer"}, "group_roles": map[string]any{"ops": []string{"operator"}}})
	if got := rolesOf(t, owner, "ada@bank.test"); got != "operator,viewer" {
		t.Errorf("after mapping Ops: %s", got)
	}
	cfg := owner.must(200, "GET", "/v1/scim", nil)
	if !strings.Contains(toJSON(cfg["groups"]), `"members":1`) || toJSON(cfg["users"]) != `{"active":1,"inactive":0}` {
		t.Errorf("config: %v", cfg)
	}

	// An existing member is linked; deactivating them takes everything and
	// signs them out. Reactivated, they get only what SCIM grants.
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "bob@bank.test", "password": "correct horse battery", "roles": []string{"approver"}})
	bob := w.login(t, "bob@bank.test", "correct horse battery")
	bobID := scim.must(201, "POST", "/scim/v2/Users", map[string]any{"userName": "bob@bank.test", "displayName": "Bob"}, ct...)["id"].(string)
	if got := rolesOf(t, owner, "bob@bank.test"); got != "approver,viewer" {
		t.Errorf("linked: %s", got)
	}
	out := scim.must(200, "PATCH", "/scim/v2/Users/"+bobID, map[string]any{"Operations": []any{map[string]any{"op": "Replace", "path": "active", "value": "False"}}}, ct...)
	if out["active"] != false {
		t.Errorf("patched: %v", out)
	}
	if st, _ := bob.do("GET", "/v1/me", nil); st != 401 {
		t.Errorf("deactivated member still signed in: %d", st)
	}
	if got := rolesOf(t, owner, "bob@bank.test"); got != "" {
		t.Errorf("deactivated member keeps %s", got)
	}
	if out := scim.must(200, "GET", "/scim/v2/Users/"+bobID, nil); out["active"] != false || out["userName"] != "bob@bank.test" {
		t.Errorf("deactivated user reads %v", out)
	}
	scim.must(200, "PATCH", "/scim/v2/Users/"+bobID, map[string]any{"Operations": []any{map[string]any{"op": "replace", "value": map[string]any{"active": true, "name.givenName": "Robert"}}}}, ct...)
	if got := rolesOf(t, owner, "bob@bank.test"); got != "viewer" {
		t.Errorf("reactivated: %s", got)
	}

	// Owners are removed by owners, never by provisioning.
	ownerID := scim.must(201, "POST", "/scim/v2/Users", map[string]any{"userName": "owner@bank.test"}, ct...)["id"].(string)
	scim.must(200, "PATCH", "/scim/v2/Users/"+ownerID, map[string]any{"Operations": []any{map[string]any{"op": "replace", "path": "active", "value": false}}}, ct...)
	owner.must(200, "GET", "/v1/me", nil)

	// Deleted: gone from SCIM and from the tenant.
	scim.must(204, "DELETE", "/scim/v2/Users/"+adaID, nil)
	scim.must(404, "GET", "/scim/v2/Users/"+adaID, nil)
	if got := rolesOf(t, owner, "ada@bank.test"); got != "" {
		t.Errorf("deleted member keeps %s", got)
	}
	if out := scim.must(200, "GET", "/scim/v2/Groups/"+gid, nil); toJSON(out["members"]) != "[]" {
		t.Errorf("group after delete: %v", out)
	}
	scim.must(204, "DELETE", "/scim/v2/Groups/"+gid, nil)

	// Another tenant's provider sees none of this.
	owner2 := w.tenant(t, "Other", "owner@other.test")
	key2 := owner2.must(201, "POST", "/v1/api-keys", map[string]any{"name": "SCIM", "permissions": []string{"scim.provision"}})["key"].(string)
	scim2 := &client{t: t, base: w.base, token: key2}
	scim2.must(404, "GET", "/scim/v2/Users/"+bobID, nil)
	if out := scim2.must(200, "GET", "/scim/v2/Users", nil); out["totalResults"] != float64(0) {
		t.Errorf("other tenant sees %v", out)
	}
}

func TestSCIMMappingCannotEscalate(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Bank", "owner@bank.test")
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "adm@bank.test", "password": "correct horse battery", "roles": []string{"admin"}})
	adm := w.login(t, "adm@bank.test", "correct horse battery")
	owner.must(201, "PUT", "/v1/roles/privacy", map[string]any{"permissions": []string{"pii.reveal"}})

	adm.must(403, "PUT", "/v1/scim", map[string]any{"default_roles": []string{"viewer"}, "group_roles": map[string]any{"dpo": []string{"privacy"}}})
	adm.must(200, "PUT", "/v1/scim", map[string]any{"default_roles": []string{"viewer"}, "group_roles": map[string]any{"ops": []string{"operator"}}})
}

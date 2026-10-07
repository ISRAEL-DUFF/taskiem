package api_test

import (
	"context"
	"strings"
	"testing"
)

// Admins list and cancel pending invitations; the invitee declines one.
func TestInvitationsListCancelDecline(t *testing.T) {
	w := newWorld(t)
	acme := w.tenant(t, "Acme", "owner@acme.test")
	beta := w.tenant(t, "Beta", "owner@beta.test")
	gamma := w.tenant(t, "Gamma", "owner@gamma.test")
	viewer := addMember(t, w, beta, "viewer", "viewer")
	betaID, gammaID := tenantOf(t, beta), tenantOf(t, gamma)

	beta.must(201, "POST", "/v1/members", map[string]any{"email": "owner@acme.test", "roles": []string{"operator"}})
	gamma.must(201, "POST", "/v1/members", map[string]any{"email": "owner@acme.test", "roles": []string{"viewer"}})
	list := beta.must(200, "GET", "/v1/invitations", nil)["invitations"].([]any)
	if len(list) != 1 {
		t.Fatalf("invitations: %v", list)
	}
	inv := list[0].(map[string]any)
	if inv["email"] != "owner@acme.test" || toJSON(inv["roles"]) != `["operator"]` || inv["source"] != "manual" || inv["invited_at"] == nil {
		t.Errorf("invitation: %v", inv)
	}
	// Not for members without member.manage, nor across tenants.
	viewer.must(403, "GET", "/v1/invitations", nil)
	if n := len(acme.must(200, "GET", "/v1/invitations", nil)["invitations"].([]any)); n != 0 {
		t.Errorf("acme sees %d invitations", n)
	}
	acme.must(404, "DELETE", "/v1/invitations/"+inv["user_id"].(string), nil)

	// Beta cancels: the invitee no longer sees it and cannot accept it.
	beta.must(204, "DELETE", "/v1/invitations/"+inv["user_id"].(string), nil)
	beta.must(404, "DELETE", "/v1/invitations/"+inv["user_id"].(string), nil)
	mine := acme.must(200, "GET", "/v1/me/invitations", nil)["invitations"].([]any)
	if len(mine) != 1 || mine[0].(map[string]any)["tenant"] != "Gamma" {
		t.Fatalf("after cancel: %v", mine)
	}
	acme.must(404, "POST", "/v1/me/invitations/"+betaID+"/accept", nil)

	// The invitee declines Gamma's.
	acme.must(404, "POST", "/v1/me/invitations/"+betaID+"/decline", nil)
	acme.must(204, "POST", "/v1/me/invitations/"+gammaID+"/decline", nil)
	if n := len(acme.must(200, "GET", "/v1/me/invitations", nil)["invitations"].([]any)); n != 0 {
		t.Errorf("%d invitations left", n)
	}
	if n := len(gamma.must(200, "GET", "/v1/invitations", nil)["invitations"].([]any)); n != 0 {
		t.Errorf("gamma still lists %d", n)
	}
	acme.must(404, "POST", "/v1/me/invitations/"+gammaID+"/accept", nil)
	if n := auditCount(t, w, betaID, "member.invitation.cancel"); n != 1 {
		t.Errorf("cancel audited %d times", n)
	}
	if n := auditCount(t, w, gammaID, "member.invitation.decline"); n != 1 {
		t.Errorf("decline audited %d times", n)
	}
}

// Someone with an account but no membership anywhere signs in to an
// invitee session: it reaches their invitations and nothing else. Accepting
// one makes it a session in that tenant.
func TestInviteeSession(t *testing.T) {
	w := newWorld(t)
	acme := w.tenant(t, "Acme", "owner@acme.test")
	beta := w.tenant(t, "Beta", "owner@beta.test")
	gamma := w.tenant(t, "Gamma", "owner@gamma.test")
	betaID, gammaID := tenantOf(t, beta), tenantOf(t, gamma)
	// Bob had an account at Acme, then left it.
	bob := acme.must(201, "POST", "/v1/members", map[string]any{"email": "bob@acme.test", "password": testPassword, "roles": []string{"viewer"}})["user_id"].(string)
	acme.must(204, "DELETE", "/v1/members/"+bob+"/roles/viewer", nil)
	anon := &client{t: t, base: w.base}
	login := map[string]any{"email": "bob@acme.test", "password": testPassword}

	// With no membership and no invitation, sign-in is refused as before.
	if st, out := anon.do("POST", "/v1/auth/login", login); st != 401 || out["invitations_only"] != nil {
		t.Fatalf("no invitation: %d %v", st, out)
	}
	beta.must(201, "POST", "/v1/members", map[string]any{"email": "bob@acme.test", "roles": []string{"builder"}})
	gamma.must(201, "POST", "/v1/members", map[string]any{"email": "bob@acme.test", "roles": []string{"viewer"}})
	out := anon.must(200, "POST", "/v1/auth/login", login)
	if out["invitations_only"] != true || out["token"] == nil || len(out["tenants"].([]any)) != 0 {
		t.Fatalf("invitee sign-in: %v", out)
	}
	// A wrong password is still wrong.
	if st, _ := anon.do("POST", "/v1/auth/login", map[string]any{"email": "bob@acme.test", "password": "not the password"}); st != 401 {
		t.Errorf("wrong password: %d", st)
	}
	inv := &client{t: t, base: w.base, token: out["token"].(string)}
	me := inv.must(200, "GET", "/v1/me", nil)
	if me["invitations_only"] != true || me["tenant_id"] != nil || len(me["permissions"].([]any)) != 0 || me["user"].(map[string]any)["email"] != "bob@acme.test" {
		t.Fatalf("me: %v", me)
	}
	// No tenant data, no settings, nothing else of the API.
	for _, path := range []string{"/v1/workflows", "/v1/runs", "/v1/members", "/v1/secrets", "/v1/audit", "/v1/limits", "/v1/governance",
		"/v1/approvals", "/v1/me/passkeys", "/v1/me/whatsapp", "/v1/templates", "/v1/connectors", "/v1/permissions", "/v1/invitations"} {
		if st, body := inv.do("GET", path, nil); st != 403 || body["invitations_only"] != true {
			t.Errorf("GET %s: %d %v", path, st, body)
		}
	}
	for _, c := range []struct{ method, path string }{{"POST", "/v1/workflows"}, {"POST", "/v1/me/totp"}, {"POST", "/v1/me/passkeys/options"},
		{"POST", "/v1/api-keys"}, {"POST", "/v1/me/password"}, {"POST", "/v1/members"}} {
		if st, _ := inv.do(c.method, c.path, map[string]any{}); st != 403 {
			t.Errorf("%s %s: %d", c.method, c.path, st)
		}
	}
	if n := len(inv.must(200, "GET", "/v1/me/invitations", nil)["invitations"].([]any)); n != 2 {
		t.Fatalf("invitations: %d", n)
	}
	// It declines one and accepts the other: then it is a session in that
	// tenant, with the roles offered.
	inv.must(204, "POST", "/v1/me/invitations/"+gammaID+"/decline", nil)
	acc := inv.must(200, "POST", "/v1/me/invitations/"+betaID+"/accept", nil)
	if acc["token"] == nil || toJSON(acc["roles"]) != `["builder"]` {
		t.Fatalf("accept: %v", acc)
	}
	if st, _ := inv.do("GET", "/v1/me", nil); st != 401 {
		t.Errorf("the invitee session outlived the accept: %d", st)
	}
	member := &client{t: t, base: w.base, token: acc["token"].(string)}
	if got := member.must(200, "GET", "/v1/me", nil); got["tenant_id"] != betaID || toJSON(got["roles"]) != `["builder"]` {
		t.Errorf("after accepting: %v", got)
	}
	member.must(200, "GET", "/v1/workflows", nil)
	// Signing in now is an ordinary sign-in.
	if again := anon.must(200, "POST", "/v1/auth/login", login); again["invitations_only"] != nil || again["tenant_id"] != betaID {
		t.Errorf("sign-in after accepting: %v", again)
	}
}

// Signing out ends an invitee session; a suspended inviter's invitation
// does not open one.
func TestInviteeSessionEnds(t *testing.T) {
	w := newWorld(t)
	acme := w.tenant(t, "Acme", "owner@acme.test")
	beta := w.tenant(t, "Beta", "owner@beta.test")
	bob := acme.must(201, "POST", "/v1/members", map[string]any{"email": "bob@acme.test", "password": testPassword, "roles": []string{"viewer"}})["user_id"].(string)
	acme.must(204, "DELETE", "/v1/members/"+bob+"/roles/viewer", nil)
	beta.must(201, "POST", "/v1/members", map[string]any{"email": "bob@acme.test", "roles": []string{"viewer"}})
	anon := &client{t: t, base: w.base}
	login := map[string]any{"email": "bob@acme.test", "password": testPassword}
	inv := &client{t: t, base: w.base, token: anon.must(200, "POST", "/v1/auth/login", login)["token"].(string)}
	inv.must(204, "POST", "/v1/auth/logout", nil)
	if st, _ := inv.do("GET", "/v1/me/invitations", nil); st != 401 {
		t.Errorf("after sign-out: %d", st)
	}
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE tenants SET status = 'suspended' WHERE id = $1`, tenantOf(t, beta)); err != nil {
		t.Fatal(err)
	}
	if st, out := anon.do("POST", "/v1/auth/login", login); st != 401 || strings.Contains(toJSON(out), "invitations_only") {
		t.Errorf("invited only by a suspended tenant: %d %v", st, out)
	}
}

package api_test

import (
	"context"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/webauthn"
	"github.com/israel-duff/taskiem/engine/webauthn/webauthntest"
)

var invitationLink = regexp.MustCompile(`https://app\.taskiem\.test/accept-invitation#token=([A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)`)

// onboardingWorld is a world with passkeys and email, where partners
// invite people into their sub-tenants.
func onboardingWorld(t *testing.T) (*world, *fakeMail) {
	t.Helper()
	w := newWorld(t)
	w.srv.WebAuthn = webauthn.Config{RPID: rpID, RPName: "Taskiem", Origins: []string{origin}}
	mail := mailWorld(t, w)
	return w, mail
}

// sentTo returns the emails to address sent since before.
func sentTo(t *testing.T, w *world, mail *fakeMail, before int, address string) []string {
	t.Helper()
	w.srv.WaitBackground()
	var out []string
	for _, m := range mail.all()[before:] {
		if strings.Contains(m, "To: "+address) {
			out = append(out, m)
		}
	}
	return out
}

// acceptLink follows an invitation link: creates a passkey and accepts.
func acceptLink(t *testing.T, w *world, token string, want int) (*client, *webauthntest.Authenticator, map[string]any) {
	t.Helper()
	anon := &client{t: t, base: w.base}
	opts := anon.must(200, "POST", "/v1/auth/invitation/options", map[string]any{"token": token})
	a := webauthntest.New(rpID, origin)
	a.UserHandle, _ = base64.RawURLEncoding.DecodeString(opts["publicKey"].(map[string]any)["user"].(map[string]any)["id"].(string))
	out := anon.must(want, "POST", "/v1/auth/invitation/accept", map[string]any{"token": token, "name": "Ada Obi", "passkey_name": "Laptop",
		"credential": a.Register(challengeFrom(t, opts)).JSON(), "bearer": true})
	if want != 200 {
		return nil, a, out
	}
	return &client{t: t, base: w.base, token: out["token"].(string)}, a, out
}

// A partner invites people into its sub-tenant; someone new follows the
// emailed link, creates a passkey and is a member of that sub-tenant only.
// The partner never sees the link or a session; another partner cannot
// reach the sub-tenant's invitations.
func TestPartnerInvitesIntoSubTenant(t *testing.T) {
	w, mail := onboardingWorld(t)
	p := w.partner(t, "Wagely", "owner@wagely.test")
	sub := p.sub(t, "Company 1042", nil)
	other := p.sub(t, "Company 7", nil)
	rival := w.partner(t, "Rival", "owner@rival.test")
	rivalSub := rival.sub(t, "Rival customer", nil)

	before := len(mail.all())
	inv := p.key.must(201, "POST", "/v1/partner/sub-tenants/"+sub+"/invitations", map[string]any{"email": "ada@company.test", "name": "Ada", "roles": []string{"owner"}})
	if inv["status"] != "pending" || inv["delivery"] != "email" || toJSON(inv["roles"]) != `["owner"]` || inv["token"] != nil {
		t.Fatalf("invite: %v", inv)
	}
	emails := sentTo(t, w, mail, before, "ada@company.test")
	if len(emails) != 1 || !strings.Contains(emails[0], "Company 1042") || !strings.Contains(emails[0], "through Wagely") {
		t.Fatalf("emails: %v", emails)
	}
	m := invitationLink.FindStringSubmatch(emails[0])
	if m == nil {
		t.Fatalf("no link in:\n%s", emails[0])
	}
	token := m[1]
	if strings.Contains(toJSON(inv), token) || strings.Contains(toJSON(inv), strings.Split(token, ".")[1]) {
		t.Fatal("the partner got the link")
	}

	// One owner only, from the partner; roles must exist in the sub-tenant.
	p.key.must(409, "POST", "/v1/partner/sub-tenants/"+sub+"/invitations", map[string]any{"email": "bola@company.test", "roles": []string{"owner"}})
	p.key.must(400, "POST", "/v1/partner/sub-tenants/"+sub+"/invitations", map[string]any{"email": "bola@company.test", "roles": []string{"payroll_approver"}})
	p.key.must(400, "POST", "/v1/partner/sub-tenants/"+sub+"/invitations", map[string]any{"email": "bola@company.test", "roles": []string{}})
	p.key.must(400, "PUT", "/v1/partner/sub-tenants/"+sub+"/roles/payroll_approver", map[string]any{"permissions": []string{"partner.manage"}})
	p.key.must(400, "PUT", "/v1/partner/sub-tenants/"+sub+"/roles/admin", map[string]any{"permissions": []string{"run.read"}})
	role := p.key.must(201, "PUT", "/v1/partner/sub-tenants/"+sub+"/roles/payroll_approver",
		map[string]any{"description": "Releases payrolls", "permissions": []string{"approval.decide", "run.read", "workflow.read"}})
	if toJSON(role["permissions"]) != `["approval.decide","run.read","workflow.read"]` {
		t.Errorf("role: %v", role)
	}
	p.key.must(200, "PUT", "/v1/partner/sub-tenants/"+sub+"/roles/payroll_approver", map[string]any{"permissions": []string{"approval.decide", "run.read", "workflow.read"}})
	roles := toJSON(p.key.must(200, "GET", "/v1/partner/sub-tenants/"+sub+"/roles", nil)["roles"])
	if !strings.Contains(roles, `"name":"payroll_approver"`) || strings.Contains(toJSON(p.key.must(200, "GET", "/v1/partner/sub-tenants/"+other+"/roles", nil)["roles"]), "payroll_approver") {
		t.Errorf("roles: %s", roles)
	}
	bola := p.key.must(201, "POST", "/v1/partner/sub-tenants/"+sub+"/invitations", map[string]any{"email": "bola@company.test", "roles": []string{"payroll_approver"}})
	bolaToken := invitationLink.FindStringSubmatch(sentTo(t, w, mail, before, "bola@company.test")[0])[1]

	// The partner lists its own invitations, in that sub-tenant only.
	if list := p.key.must(200, "GET", "/v1/partner/sub-tenants/"+sub+"/invitations", nil)["invitations"].([]any); len(list) != 2 {
		t.Fatalf("invitations: %v", list)
	}
	if n := len(p.key.must(200, "GET", "/v1/partner/sub-tenants/"+other+"/invitations", nil)["invitations"].([]any)); n != 0 {
		t.Errorf("the other sub-tenant lists %d", n)
	}
	// Another partner reaches none of it.
	rival.key.must(404, "GET", "/v1/partner/sub-tenants/"+sub+"/invitations", nil)
	rival.key.must(404, "POST", "/v1/partner/sub-tenants/"+sub+"/invitations", map[string]any{"email": "eve@rival.test", "roles": []string{"admin"}})
	rival.key.must(404, "DELETE", "/v1/partner/sub-tenants/"+sub+"/invitations/"+bola["user_id"].(string), nil)
	rival.key.must(404, "PUT", "/v1/partner/sub-tenants/"+sub+"/roles/spy", map[string]any{"permissions": []string{"run.read"}})
	p.key.must(404, "GET", "/v1/partner/sub-tenants/"+rivalSub+"/invitations", nil)
	// A read-only key cannot invite.
	ro := &client{t: t, base: w.base, token: p.owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "ro", "permissions": []string{"partner.read"}})["key"].(string)}
	ro.must(403, "POST", "/v1/partner/sub-tenants/"+sub+"/invitations", map[string]any{"email": "x@company.test", "roles": []string{"viewer"}})

	// Ada follows the link: a passkey, a member, signed in.
	anon := &client{t: t, base: w.base}
	opts := anon.must(200, "POST", "/v1/auth/invitation/options", map[string]any{"token": token})
	if opts["tenant"] != "Company 1042" || toJSON(opts["roles"]) != `["owner"]` || opts["email"] != "ada@company.test" {
		t.Errorf("options: %v", opts)
	}
	ada, key, out := acceptLink(t, w, token, 200)
	if out["tenant_id"] != sub || toJSON(out["roles"]) != `["owner"]` {
		t.Fatalf("accept: %v", out)
	}
	me := ada.must(200, "GET", "/v1/me", nil)
	if me["tenant_id"] != sub || toJSON(me["roles"]) != `["owner"]` || me["auth_method"] != "passkey" {
		t.Fatalf("me: %v", me)
	}
	ada.must(200, "GET", "/v1/workflows", nil)
	// Only that sub-tenant: signing in again reaches it alone.
	ch := challengeFrom(t, anon.must(200, "POST", "/v1/auth/passkey/options", nil))
	again := anon.must(200, "POST", "/v1/auth/passkey", map[string]any{"credential": key.Assert(ch).JSON(), "bearer": true})
	if again["tenant_id"] != sub || toJSON(again["tenants"]) != `["`+sub+`"]` {
		t.Errorf("sign-in: %v", again)
	}
	if st, _ := anon.do("POST", "/v1/auth/login", map[string]any{"email": "ada@company.test", "password": "anything at all", "tenant_id": other}); st != 401 {
		t.Errorf("into the other sub-tenant: %d", st)
	}
	// The link works once.
	anon.must(400, "POST", "/v1/auth/invitation/options", map[string]any{"token": token})
	anon.must(400, "POST", "/v1/auth/invitation/options", map[string]any{"token": "nope"})

	// Ada, the sub-tenant's owner, sees the partner's pending invitation.
	pending := ada.must(200, "GET", "/v1/invitations", nil)["invitations"].([]any)
	if len(pending) != 1 || pending[0].(map[string]any)["source"] != "partner" || pending[0].(map[string]any)["email"] != "bola@company.test" {
		t.Errorf("sub-tenant's invitations: %v", pending)
	}
	// The partner withdraws Bola's; the link stops working.
	p.key.must(204, "DELETE", "/v1/partner/sub-tenants/"+sub+"/invitations/"+bola["user_id"].(string), nil)
	p.key.must(404, "DELETE", "/v1/partner/sub-tenants/"+sub+"/invitations/"+bola["user_id"].(string), nil)
	anon.must(400, "POST", "/v1/auth/invitation/options", map[string]any{"token": bolaToken})

	// Someone with an account is asked to sign in and accept: no link.
	w.tenant(t, "Elsewhere", "chidi@elsewhere.test")
	before = len(mail.all())
	p.key.must(201, "POST", "/v1/partner/sub-tenants/"+sub+"/invitations", map[string]any{"email": "chidi@elsewhere.test", "roles": []string{"payroll_approver"}})
	notes := sentTo(t, w, mail, before, "chidi@elsewhere.test")
	if len(notes) != 1 || invitationLink.MatchString(notes[0]) || !strings.Contains(notes[0], "https://app.taskiem.test/login") {
		t.Fatalf("existing account's email: %v", notes)
	}
	chidi := w.login(t, "chidi@elsewhere.test", testPassword)
	chidi.must(200, "POST", "/v1/me/invitations/"+sub+"/accept", nil)
	// Held now: the partner cannot widen it behind its members.
	p.key.must(409, "PUT", "/v1/partner/sub-tenants/"+sub+"/roles/payroll_approver", map[string]any{"permissions": []string{"approval.decide", "secret.manage"}})

	ctx := context.Background()
	var members int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(DISTINCT user_id) FROM memberships WHERE tenant_id = $1`, uuid.MustParse(sub)).Scan(&members); err != nil || members != 2 {
		t.Errorf("members of the sub-tenant: %d %v", members, err)
	}
	for tenant, want := range map[string]map[string]int{
		p.id.String(): {"partner.subtenant.invitation.create": 3, "partner.subtenant.invitation.cancel": 1, "partner.subtenant.role.put": 2},
		sub:           {"partner.subtenant.invitation.create": 3, "member.invite": 3, "member.invitation.accept": 2, "member.invitation.cancel": 1, "role.put": 2, "passkey.add": 1},
		other:         {"member.invite": 0},
	} {
		for action, n := range want {
			if got := auditCount(t, w, tenant, action); got != n {
				t.Errorf("%s in %s: %d, want %d", action, tenant, got, n)
			}
		}
	}
}

// An invitation link is single-use, works only for the account it was made
// for while nobody can sign in as it, and a passkey for another link's
// challenge does not pass.
func TestInvitationLinkGuards(t *testing.T) {
	w, mail := onboardingWorld(t)
	p := w.partner(t, "Wagely", "owner@wagely.test")
	sub := p.sub(t, "Company", nil)
	before := len(mail.all())
	p.key.must(201, "POST", "/v1/partner/sub-tenants/"+sub+"/invitations", map[string]any{"email": "a@company.test", "roles": []string{"viewer"}})
	p.key.must(201, "POST", "/v1/partner/sub-tenants/"+sub+"/invitations", map[string]any{"email": "b@company.test", "roles": []string{"viewer"}})
	tokA := invitationLink.FindStringSubmatch(sentTo(t, w, mail, before, "a@company.test")[0])[1]
	tokB := invitationLink.FindStringSubmatch(sentTo(t, w, mail, before, "b@company.test")[0])[1]

	// A's challenge answered under B's link.
	anon := &client{t: t, base: w.base}
	optsA := anon.must(200, "POST", "/v1/auth/invitation/options", map[string]any{"token": tokA})
	a := webauthntest.New(rpID, origin)
	if st, _ := anon.do("POST", "/v1/auth/invitation/accept", map[string]any{"token": tokB, "credential": a.Register(challengeFrom(t, optsA)).JSON(), "bearer": true}); st != 401 {
		t.Errorf("cross-link accept: %d", st)
	}
	// A wrong secret for a real selector counts; five lock the link.
	sel := strings.Split(tokA, ".")[0]
	wrong := sel + "." + base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for range 5 {
		anon.must(400, "POST", "/v1/auth/invitation/options", map[string]any{"token": wrong})
	}
	anon.must(400, "POST", "/v1/auth/invitation/options", map[string]any{"token": tokA})

	// Re-inviting replaces the link; the old one stops.
	before = len(mail.all())
	p.key.must(201, "POST", "/v1/partner/sub-tenants/"+sub+"/invitations", map[string]any{"email": "b@company.test", "roles": []string{"operator"}})
	tokB2 := invitationLink.FindStringSubmatch(sentTo(t, w, mail, before, "b@company.test")[0])[1]
	anon.must(400, "POST", "/v1/auth/invitation/options", map[string]any{"token": tokB})
	_, _, out := acceptLink(t, w, tokB2, 200)
	if toJSON(out["roles"]) != `["operator","viewer"]` {
		t.Errorf("roles: %v", out["roles"])
	}
	// A name that reads as a link stays out of Taskiem's email; an address
	// gets five invitations, then waits.
	phish := p.sub(t, "Verify your salary at evil.example", nil)
	before = len(mail.all())
	for range 5 {
		p.key.must(201, "POST", "/v1/partner/sub-tenants/"+phish+"/invitations", map[string]any{"email": "d@company.test", "roles": []string{"viewer"}})
	}
	p.key.must(429, "POST", "/v1/partner/sub-tenants/"+phish+"/invitations", map[string]any{"email": "d@company.test", "roles": []string{"viewer"}})
	if sent := sentTo(t, w, mail, before, "d@company.test"); len(sent) != 5 || strings.Contains(sent[0], "evil.example") || !strings.Contains(sent[0], "invited to an organisation") {
		t.Errorf("emails: %d %v", len(sent), sent)
	}
	// A suspended sub-tenant's link does not work.
	p.key.must(201, "POST", "/v1/partner/sub-tenants/"+sub+"/invitations", map[string]any{"email": "c@company.test", "roles": []string{"viewer"}})
	tokC := invitationLink.FindStringSubmatch(sentTo(t, w, mail, before, "c@company.test")[0])[1]
	p.key.must(200, "POST", "/v1/partner/sub-tenants/"+sub+"/suspend", nil)
	anon.must(400, "POST", "/v1/auth/invitation/options", map[string]any{"token": tokC})
}

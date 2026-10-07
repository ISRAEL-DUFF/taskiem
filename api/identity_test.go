package api_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/oidc/oidctest"
	"github.com/israel-duff/taskiem/engine/totp"
	"github.com/israel-duff/taskiem/engine/webauthn/webauthntest"
)

// stepUp is a passkey assertion for a fresh step-up challenge.
func stepUp(t *testing.T, c *client, key *webauthntest.Authenticator) map[string]any {
	t.Helper()
	return key.Assert(challengeFrom(t, c.must(200, "POST", "/v1/me/step-up/options", nil))).JSON()
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// tenantOf is the tenant a client is signed in to.
func tenantOf(t *testing.T, c *client) string {
	t.Helper()
	return c.must(200, "GET", "/v1/me", nil)["tenant_id"].(string)
}

// wrongCode is a six-digit code no nearby time step accepts.
func wrongCode(secret string) string {
	now := totp.Step(time.Now())
	for i := 0; ; i++ {
		c := fmt.Sprintf("%06d", i)
		ok := true
		for s := now - 3; s <= now+3; s++ {
			if want, _ := totp.Code(secret, s); want == c {
				ok = false
			}
		}
		if ok {
			return c
		}
	}
}

// A session alone cannot add or remove a factor: a thief with a session
// could otherwise enrol their own and pass step-up.
func TestFactorChangesNeedProof(t *testing.T) {
	w := passkeyWorld(t)
	w.srv.RequireAdminPasskeys = false
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "ada@acme.test", "password": testPassword, "roles": []string{"approver"}})
	ada := w.login(t, "ada@acme.test", testPassword)
	if f := ada.must(200, "GET", "/v1/me", nil)["factors"].(map[string]any); f["password"] != true || f["passkey"] != false || f["totp"] != false {
		t.Fatalf("factors: %v", f)
	}

	// No passkey or authenticator yet: the password.
	if st, body := ada.do("POST", "/v1/me/passkeys/options", nil); st != 403 || toJSON(body["reauth"]) != `["password"]` {
		t.Fatalf("passkey options without proof: %d %v", st, body)
	}
	ada.must(403, "POST", "/v1/me/passkeys/options", map[string]any{"password": "not the password"})
	ada.must(403, "POST", "/v1/me/totp", nil)
	key := addPasskey(t, ada, "Phone")

	// With a passkey, the password is no longer enough.
	if st, body := ada.do("POST", "/v1/me/totp", map[string]any{"password": testPassword}); st != 403 || toJSON(body["reauth"]) != `["passkey"]` {
		t.Fatalf("authenticator with only the password: %d %v", st, body)
	}
	ada.must(403, "POST", "/v1/me/passkeys/options", map[string]any{"password": testPassword})
	secret := ada.must(200, "POST", "/v1/me/totp", map[string]any{"passkey": stepUp(t, ada, key)})["secret"].(string)
	code, _ := totp.Code(secret, totp.Step(time.Now()))
	ada.must(204, "POST", "/v1/me/totp/confirm", map[string]any{"code": code})

	// An enrolled authenticator is not replaced, even with proof: it is
	// removed first, with a current code.
	ada.must(409, "POST", "/v1/me/totp", map[string]any{"passkey": stepUp(t, ada, key)})
	ada.must(409, "POST", "/v1/me/totp/confirm", map[string]any{"code": codeAfter(secret, 1)})

	// Removing a passkey needs proof too: the authenticator's code serves.
	ada.must(403, "DELETE", "/v1/me/passkeys/"+b64url(key.CredID), nil)
	ada.must(403, "DELETE", "/v1/me/passkeys/"+b64url(key.CredID), map[string]any{"password": testPassword})
	ada.must(204, "DELETE", "/v1/me/passkeys/"+b64url(key.CredID), map[string]any{"totp": codeAfter(secret, 1)})
	if f := ada.must(200, "GET", "/v1/me", nil)["factors"].(map[string]any); f["passkey"] != false || f["totp"] != true {
		t.Errorf("factors after: %v", f)
	}
}

// The admin passkey rule holds whichever tenant a sign-in asks for.
func TestAdminPasskeyRuleAcrossTenants(t *testing.T) {
	w := passkeyWorld(t)
	acme := w.tenant(t, "Acme", "owner@acme.test")
	key := addPasskey(t, acme, "Laptop")
	pk := w.passkeyLogin(t, key, 200)
	beta := w.passkeyLogin(t, addPasskey(t, w.tenant(t, "Beta", "owner@beta.test"), "Phone"), 200)
	beta.must(201, "POST", "/v1/members", map[string]any{"email": "owner@acme.test", "roles": []string{"viewer"}})
	pk.must(200, "POST", "/v1/me/invitations/"+tenantOf(t, beta)+"/accept", nil)

	anon := &client{t: t, base: w.base}
	for _, tenant := range []string{"", tenantOf(t, beta)} {
		body := map[string]any{"email": "owner@acme.test", "password": testPassword}
		if tenant != "" {
			body["tenant_id"] = tenant
		}
		if st, out := anon.do("POST", "/v1/auth/login", body); st != 401 || out["passkey_required"] != true {
			t.Errorf("password sign-in of an owner with a passkey, tenant %q: %d %v", tenant, st, out)
		}
	}
}

// Someone with an account is invited, not attached; the answer does not
// tell whether they had one.
func TestExistingPeopleAreInvited(t *testing.T) {
	w := ssoWorld(t)
	acme := w.tenant(t, "Acme", "owner@acme.test")
	acme.must(201, "POST", "/v1/members", map[string]any{"email": "ops@acme.test", "password": testPassword, "roles": []string{"operator"}})
	beta := w.tenant(t, "Beta", "owner@beta.test")
	betaID := tenantOf(t, beta)

	existing := beta.must(201, "POST", "/v1/members", map[string]any{"email": "owner@acme.test", "roles": []string{"viewer"}})
	fresh := beta.must(201, "POST", "/v1/members", map[string]any{"email": "new@beta.test", "roles": []string{"viewer"}})
	if len(existing) != len(fresh) || existing["user_id"] == nil || fresh["user_id"] == nil {
		t.Errorf("answers differ: %v / %v", existing, fresh)
	}
	if strings.Contains(toJSON(beta.must(200, "GET", "/v1/members", nil)), "owner@acme.test") {
		t.Error("an invited person is listed as a member before accepting")
	}
	anon := &client{t: t, base: w.base}
	if st, _ := anon.do("POST", "/v1/auth/login", map[string]any{"email": "owner@acme.test", "password": testPassword, "tenant_id": betaID}); st != 401 {
		t.Errorf("signed in to a tenant before accepting its invitation: %d", st)
	}

	// The person sees the invitation and accepts it.
	inv := acme.must(200, "GET", "/v1/me/invitations", nil)["invitations"].([]any)
	if len(inv) != 1 || inv[0].(map[string]any)["tenant"] != "Beta" || toJSON(inv[0].(map[string]any)["roles"]) != `["viewer"]` {
		t.Fatalf("invitations: %v", inv)
	}
	acme.must(404, "POST", "/v1/me/invitations/"+tenantOf(t, acme)+"/accept", nil)
	acme.must(200, "POST", "/v1/me/invitations/"+betaID+"/accept", nil)
	in := anon.must(200, "POST", "/v1/auth/login", map[string]any{"email": "owner@acme.test", "password": testPassword, "tenant_id": betaID})
	me := (&client{t: t, base: w.base, token: in["token"].(string)}).must(200, "GET", "/v1/me", nil)
	if toJSON(me["roles"]) != `["viewer"]` {
		t.Errorf("roles after accepting: %v", me["roles"])
	}
	if n := len(acme.must(200, "GET", "/v1/me/invitations", nil)["invitations"].([]any)); n != 0 {
		t.Errorf("%d invitations left", n)
	}

	// On one of the tenant's verified SSO domains, the tenant speaks for the
	// address: no invitation.
	idp := oidctest.New()
	defer idp.Close()
	conn := beta.must(201, "POST", "/v1/sso", map[string]any{"protocol": "oidc", "name": "x",
		"oidc": map[string]any{"issuer": idp.URL, "client_id": idp.ClientID, "client_secret": idp.ClientSecret}})["id"].(string)
	dom := beta.must(201, "POST", "/v1/sso/"+conn+"/domains", map[string]any{"domain": "acme.test"})
	w.txt["_taskiem-verify.acme.test"] = []string{dom["txt_value"].(string)}
	beta.must(204, "POST", "/v1/sso/domains/acme.test/verify", nil)
	beta.must(201, "POST", "/v1/members", map[string]any{"email": "ops@acme.test", "roles": []string{"viewer"}})
	anon.must(200, "POST", "/v1/auth/login", map[string]any{"email": "ops@acme.test", "password": testPassword, "tenant_id": betaID})
}

// SCIM provisions someone with an account elsewhere as an invitation.
func TestSCIMInvitesExistingPeople(t *testing.T) {
	w := newWorld(t)
	acme := w.tenant(t, "Acme", "owner@acme.test")
	bank := w.tenant(t, "Bank", "owner@bank.test")
	key := &client{t: t, base: w.base, token: bank.must(201, "POST", "/v1/api-keys", map[string]any{"name": "idp", "permissions": []string{"scim.provision"}})["key"].(string)}
	created := key.must(201, "POST", "/scim/v2/Users", map[string]any{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"userName": "owner@acme.test", "emails": []map[string]any{{"value": "owner@acme.test", "primary": true}}})
	if strings.Contains(toJSON(bank.must(200, "GET", "/v1/members", nil)), "owner@acme.test") {
		t.Fatal("SCIM attached someone with an account elsewhere")
	}
	// Deactivating and reactivating does not slip past the invitation.
	id := created["id"].(string)
	patch := func(active bool) {
		key.must(200, "PATCH", "/scim/v2/Users/"+id, map[string]any{"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
			"Operations": []map[string]any{{"op": "replace", "path": "active", "value": active}}})
	}
	patch(false)
	patch(true)
	if strings.Contains(toJSON(bank.must(200, "GET", "/v1/members", nil)), "owner@acme.test") {
		t.Fatal("SCIM reactivation attached someone who had not accepted")
	}
	acme.must(200, "POST", "/v1/me/invitations/"+tenantOf(t, bank)+"/accept", nil)
	if !strings.Contains(toJSON(bank.must(200, "GET", "/v1/members", nil)), "owner@acme.test") {
		t.Error("accepting did not apply SCIM's roles")
	}
}

// A tenant resets only the passkeys of people who belong to it alone.
func TestPasskeyResetStaysInTenant(t *testing.T) {
	w := passkeyWorld(t)
	w.srv.RequireAdminPasskeys = false
	acme := w.tenant(t, "Acme", "owner@acme.test")
	acme.must(201, "POST", "/v1/members", map[string]any{"email": "ada@acme.test", "password": testPassword, "roles": []string{"approver"}})
	ada := w.login(t, "ada@acme.test", testPassword)
	addPasskey(t, ada, "Phone")
	acme.must(204, "DELETE", "/v1/members/"+memberID(t, acme, "ada@acme.test")+"/passkeys", nil) // hers alone
	key := addPasskey(t, w.login(t, "ada@acme.test", testPassword), "Phone")

	evil := w.tenant(t, "Evil", "owner@evil.test")
	evil.must(201, "POST", "/v1/members", map[string]any{"email": "ada@acme.test", "roles": []string{"viewer"}})
	pk := w.passkeyLogin(t, key, 200)
	pk.must(200, "POST", "/v1/me/invitations/"+tenantOf(t, evil)+"/accept", nil)
	if st, body := evil.do("DELETE", "/v1/members/"+memberID(t, evil, "ada@acme.test")+"/passkeys", nil); st != 403 || !strings.Contains(toJSON(body), "another organisation") {
		t.Fatalf("reset of someone who also belongs elsewhere: %d %v", st, body)
	}
	w.passkeyLogin(t, key, 200)
}

// Wrong authenticator codes are counted; at five in fifteen minutes codes
// are refused, right or wrong, and both are audited.
func TestTOTPLockout(t *testing.T) {
	w := passkeyWorld(t)
	w.srv.RequireAdminPasskeys = false
	owner := w.tenant(t, "Acme", "owner@acme.test")
	secret := enrol(t, owner)
	bad := wrongCode(secret)
	for range 4 {
		owner.must(403, "DELETE", "/v1/me/totp", map[string]any{"code": bad})
	}
	// A right code resets the count (here as proof for a passkey, which
	// changes nothing until one is registered).
	owner.must(200, "POST", "/v1/me/passkeys/options", map[string]any{"totp": codeAfter(secret, 1)})
	for range 4 {
		owner.must(403, "DELETE", "/v1/me/totp", map[string]any{"code": bad})
	}
	if st, _ := owner.do("DELETE", "/v1/me/totp", map[string]any{"code": bad}); st != 403 {
		t.Fatalf("fifth wrong code: %d", st)
	}
	if st, body := owner.do("DELETE", "/v1/me/totp", map[string]any{"code": codeAfter(secret, 2)}); st != 429 {
		t.Fatalf("a right code while locked: %d %v", st, body)
	}
	audit := toJSON(owner.must(200, "GET", "/v1/audit", nil))
	if !strings.Contains(audit, "mfa.totp.fail") || !strings.Contains(audit, "mfa.totp.locked") {
		t.Error("audit lacks the failures or the lock")
	}
	// The lock ends.
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE member_mfa SET totp_locked_until = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	// Codes are looked at again: a wrong one is wrong, not locked out.
	owner.must(403, "DELETE", "/v1/me/totp", map[string]any{"code": bad})
}

// Sign-in endpoints take only JSON, so another site cannot post a form to
// them.
func TestSignInTakesOnlyJSON(t *testing.T) {
	w := passkeyWorld(t)
	w.tenant(t, "Acme", "owner@acme.test")
	for _, path := range []string{"/v1/auth/login", "/v1/auth/passkey"} {
		for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"} {
			req, _ := http.NewRequest("POST", w.base+path, strings.NewReader(`{"email":"owner@acme.test","password":"correct horse battery"}`))
			if ct != "" {
				req.Header.Set("Content-Type", ct)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusUnsupportedMediaType {
				t.Errorf("%s as %q: %d", path, ct, resp.StatusCode)
			}
		}
	}
	w.login(t, "owner@acme.test", testPassword) // application/json
}

// Suspended tenants' sessions and keys, and disabled users' sessions, stop.
func TestSuspendedTenantsAndDisabledUsers(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	key := &client{t: t, base: w.base, token: owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "k", "permissions": []string{"run.read"}})["key"].(string)}
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "ada@acme.test", "password": testPassword, "roles": []string{"viewer"}})
	ada := w.login(t, "ada@acme.test", testPassword)
	key.must(200, "GET", "/v1/runs", nil)
	ctx := context.Background()
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET status = 'disabled' WHERE email = 'ada@acme.test'`); err != nil {
		t.Fatal(err)
	}
	ada.must(401, "GET", "/v1/me", nil)
	owner.must(200, "GET", "/v1/me", nil)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE tenants SET status = 'suspended' WHERE name = 'Acme'`); err != nil {
		t.Fatal(err)
	}
	owner.must(401, "GET", "/v1/me", nil)
	key.must(401, "GET", "/v1/runs", nil)
}

// Sign-in attempts are limited per account as well as per address, and
// SSO discovery per address.
func TestSignInLimits(t *testing.T) {
	w := ssoWorld(t)
	w.srv.LoginBurst = 3
	w.srv.TrustProxy = true
	w.tenant(t, "Acme", "owner@acme.test")
	from := func(ip string) []string { return []string{"X-Forwarded-For", ip} }
	anon := &client{t: t, base: w.base}
	var got []int
	for i := range 5 {
		st, _ := anon.do("POST", "/v1/auth/login", map[string]any{"email": "owner@acme.test", "password": "wrong password!!"}, from(fmt.Sprintf("203.0.113.%d", i+1))...)
		got = append(got, st)
	}
	if !slices.Equal(got, []int{401, 401, 429, 429, 429}) { // signup's sign-in used one
		t.Errorf("one account from many addresses: %v", got)
	}
	if st, _ := anon.do("POST", "/v1/auth/login", map[string]any{"email": "other@acme.test", "password": "wrong password!!"}, from("203.0.113.50")...); st != 401 {
		t.Errorf("another account: %d", st)
	}
	limited := false
	for range 40 {
		if st, _ := anon.do("POST", "/v1/auth/sso/discover", map[string]any{"email": "x@acme.test"}, from("203.0.113.99")...); st == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("SSO discovery is not limited")
	}
}

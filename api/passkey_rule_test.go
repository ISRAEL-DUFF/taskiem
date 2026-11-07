package api_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/webauthn/webauthntest"
)

// reauth is a passkey assertion for an account.reauth step-up on target.
func reauth(t *testing.T, c *client, key *webauthntest.Authenticator, target string) map[string]any {
	t.Helper()
	ch := challengeFrom(t, c.must(200, "POST", "/v1/me/step-up/options", map[string]any{"operation": "account.reauth", "target": target}))
	return key.Assert(ch).JSON()
}

// Passkeys are optional (docs/spec/optional-passkeys.md): with the
// deployment's switch off, an owner works with a password, with or
// without a passkey. "Only sign in with my passkey" holds them to it: on
// and off each need the passkey (not the password); then a password
// sign-in, and a password session already open, are refused and the
// passkey signs in; removing the last passkey turns it off.
func TestPasskeysOptional(t *testing.T) {
	w := passkeyWorld(t)
	w.srv.RequireAdminPasskeys = false
	owner := w.tenant(t, "Acme", "owner@acme.test")
	if me := owner.must(200, "GET", "/v1/me", nil); me["enrol_passkey"] != false {
		t.Fatalf("an owner is not made to add a passkey: %v", me)
	}
	owner.must(200, "GET", "/v1/workflows", nil)
	key := addPasskey(t, owner, "Phone")
	w.login(t, "owner@acme.test", testPassword) // a password still signs in

	// On: the passkey, not the password.
	if st, out := owner.do("PUT", "/v1/me/passkey-only", map[string]any{"on": true}); st != 403 || out["reauth"] == nil {
		t.Fatalf("on without the passkey: %d %v", st, out)
	}
	owner.must(200, "PUT", "/v1/me/passkey-only", map[string]any{"on": true, "passkey": reauth(t, owner, key, "passkey_only.on")})
	anon := &client{t: t, base: w.base}
	if st, out := anon.do("POST", "/v1/auth/login", map[string]any{"email": "owner@acme.test", "password": testPassword}); st != 401 || out["passkey_required"] != true {
		t.Fatalf("a password when held: %d %v", st, out)
	}
	if st, out := owner.do("GET", "/v1/workflows", nil); st != 401 || out["passkey_required"] != true {
		t.Fatalf("an open password session when held: %d %v", st, out)
	}
	pk := w.passkeyLogin(t, key, 200)
	if me := pk.must(200, "GET", "/v1/me", nil); me["user"].(map[string]any)["passkey_only"] != true {
		t.Fatalf("me: %v", me)
	}

	// Off: the passkey again; a password is not enough.
	if st, out := pk.do("PUT", "/v1/me/passkey-only", map[string]any{"on": false}); st != 403 || out["reauth"] == nil {
		t.Fatalf("off without the passkey: %d %v", st, out)
	}
	pk.must(200, "PUT", "/v1/me/passkey-only", map[string]any{"on": false, "passkey": reauth(t, pk, key, "passkey_only.off")})
	w.login(t, "owner@acme.test", testPassword)

	// The last passkey removed: the setting goes with it.
	pk.must(200, "PUT", "/v1/me/passkey-only", map[string]any{"on": true, "passkey": reauth(t, pk, key, "passkey_only.on")})
	id := pk.must(200, "GET", "/v1/me/passkeys", nil)["passkeys"].([]any)[0].(map[string]any)["id"].(string)
	pk.must(204, "DELETE", "/v1/me/passkeys/"+id, map[string]any{"passkey": reauth(t, pk, key, "passkey.remove/"+id)})
	w.login(t, "owner@acme.test", testPassword)
	if st, _ := pk.do("PUT", "/v1/me/passkey-only", map[string]any{"on": true}); st != 409 {
		t.Fatalf("on without a passkey: %d", st)
	}
}

// "Require passkeys for administrators" holds a business's administrators
// to passkeys there: only an owner changes it, with a passkey of their own
// and proof; an administrator without a passkey can then only add one,
// and says why; off frees them.
func TestBusinessRequiresPasskeys(t *testing.T) {
	w := passkeyWorld(t)
	w.srv.RequireAdminPasskeys = false
	owner := w.tenant(t, "Acme", "owner@acme.test")
	ade := addMember(t, w, owner, "ade", "admin")
	sec := owner.must(200, "GET", "/v1/settings/security", nil)
	if sec["require_admin_passkeys"] != false || sec["admins_without_passkey"] != float64(2) || sec["can_change"] != true {
		t.Fatalf("security: %v", sec)
	}
	if st, _ := ade.do("PUT", "/v1/settings/security", map[string]any{"require_admin_passkeys": true, "password": pw}); st != 403 {
		t.Fatalf("not an owner: %d", st)
	}
	if st, _ := owner.do("PUT", "/v1/settings/security", map[string]any{"require_admin_passkeys": true, "password": testPassword}); st != 409 {
		t.Fatalf("on without a passkey of one's own: %d", st)
	}
	key := addPasskey(t, owner, "Phone")
	pk := w.passkeyLogin(t, key, 200)
	if st, _ := pk.do("PUT", "/v1/settings/security", map[string]any{"require_admin_passkeys": true, "password": testPassword}); st != 403 {
		t.Fatalf("on with a password, having a passkey: %d", st)
	}
	pk.must(200, "PUT", "/v1/settings/security", map[string]any{"require_admin_passkeys": true, "passkey": reauth(t, pk, key, "business.require_passkeys")})

	// Ade, an administrator with none: adding one only, and why.
	if me := ade.must(200, "GET", "/v1/me", nil); me["enrol_passkey"] != true || me["passkey_reason"] != "business" {
		t.Fatalf("ade: %v", me)
	}
	if st, out := ade.do("GET", "/v1/workflows", nil); st != 403 || out["enrol_passkey"] != true {
		t.Fatalf("ade reached workflows: %d %v", st, out)
	}
	// The owner, who has one, signs in with it, not the password.
	if st, out := owner.do("GET", "/v1/workflows", nil); st != 401 || out["passkey_required"] != true {
		t.Fatalf("the owner's password session: %d %v", st, out)
	}
	// A viewer is not an administrator: not held.
	viewer := addMember(t, w, pk, "vic", "viewer")
	viewer.must(200, "GET", "/v1/workflows", nil)
	if got := pk.must(200, "GET", "/v1/settings/security", nil); got["admins_without_passkey"] != float64(1) {
		t.Fatalf("one left without: %v", got)
	}

	// Off frees them.
	pk.must(200, "PUT", "/v1/settings/security", map[string]any{"require_admin_passkeys": false, "passkey": reauth(t, pk, key, "business.require_passkeys")})
	ade.must(200, "GET", "/v1/workflows", nil)
}

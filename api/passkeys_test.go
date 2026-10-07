package api_test

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/webauthn"
	"github.com/israel-duff/taskiem/engine/webauthn/webauthntest"
)

const rpID, origin = "taskiem.test", "https://app.taskiem.test"

// testPassword is every test member's password.
const testPassword = "correct horse battery"

func passkeyWorld(t *testing.T) *world {
	w := newWorld(t)
	w.srv.WebAuthn = webauthn.Config{RPID: rpID, RPName: "Taskiem", Origins: []string{origin}}
	w.srv.RequireAdminPasskeys = true
	w.srv.LoginBurst = 100 // these tests sign in many times from one address
	return w
}

func challengeFrom(t *testing.T, opts map[string]any) []byte {
	t.Helper()
	ch, err := base64.RawURLEncoding.DecodeString(opts["publicKey"].(map[string]any)["challenge"].(string))
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

// addPasskey registers a software passkey for the signed-in member.
func addPasskey(t *testing.T, c *client, name string) *webauthntest.Authenticator {
	t.Helper()
	a := webauthntest.New(rpID, origin)
	opts := c.must(200, "POST", "/v1/me/passkeys/options", map[string]any{"password": testPassword})
	user := opts["publicKey"].(map[string]any)["user"].(map[string]any)
	a.UserHandle, _ = base64.RawURLEncoding.DecodeString(user["id"].(string))
	c.must(201, "POST", "/v1/me/passkeys", map[string]any{"name": name, "credential": a.Register(challengeFrom(t, opts)).JSON()})
	return a
}

// passkeyLogin signs in with a and returns the session's client.
func (w *world) passkeyLogin(t *testing.T, a *webauthntest.Authenticator, want int) *client {
	t.Helper()
	anon := &client{t: t, base: w.base}
	ch := challengeFrom(t, anon.must(200, "POST", "/v1/auth/passkey/options", nil))
	out := anon.must(want, "POST", "/v1/auth/passkey", map[string]any{"credential": a.Assert(ch).JSON(), "bearer": true})
	if want != 200 {
		return nil
	}
	return &client{t: t, base: w.base, token: out["token"].(string)}
}

func TestAdminsAreHeldToPasskeys(t *testing.T) {
	w := passkeyWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")

	// A password session of an administrator can only enrol a passkey.
	if me := owner.must(200, "GET", "/v1/me", nil); me["enrol_passkey"] != true || me["auth_method"] != "password" {
		t.Fatalf("me: %v", me)
	}
	if st, body := owner.do("GET", "/v1/workflows", nil); st != 403 || body["enrol_passkey"] != true {
		t.Fatalf("enrol-only session reached workflows: %d %v", st, body)
	}
	key := addPasskey(t, owner, "Laptop")

	// Now the password no longer signs the owner in; the passkey does.
	anon := &client{t: t, base: w.base}
	if st, body := anon.do("POST", "/v1/auth/login", map[string]any{"email": "owner@acme.test", "password": "correct horse battery"}); st != 401 || body["passkey_required"] != true {
		t.Fatalf("password login of an admin with a passkey: %d %v", st, body)
	}
	pk := w.passkeyLogin(t, key, 200)
	if me := pk.must(200, "GET", "/v1/me", nil); me["auth_method"] != "passkey" || me["enrol_passkey"] != false {
		t.Fatalf("passkey session: %v", me)
	}
	pk.must(200, "GET", "/v1/workflows", nil)

	// Members without administrative permissions keep their passwords.
	pk.must(201, "POST", "/v1/members", map[string]any{"email": "op@acme.test", "password": "correct horse battery", "roles": []string{"operator"}})
	op := w.login(t, "op@acme.test", "correct horse battery")
	op.must(200, "GET", "/v1/runs", nil)

	// A challenge works once, and an unknown passkey does not sign in.
	ch := challengeFrom(t, anon.must(200, "POST", "/v1/auth/passkey/options", nil))
	assertion := key.Assert(ch).JSON()
	anon.must(200, "POST", "/v1/auth/passkey", map[string]any{"credential": assertion})
	anon.must(401, "POST", "/v1/auth/passkey", map[string]any{"credential": assertion})
	w.passkeyLogin(t, webauthntest.New(rpID, origin), 401)
	// A passkey made for another site is refused.
	phish := webauthntest.New(rpID, "https://taskiem-login.test")
	phish.CredID = key.CredID
	phish.Key = key.Key
	phish.Count = key.Count + 10
	w.passkeyLogin(t, phish, 401)

	// Lost passkeys: another owner resets them and the member enrols again.
	if n := len(pk.must(200, "GET", "/v1/me/passkeys", nil)["passkeys"].([]any)); n != 1 {
		t.Fatalf("%d passkeys", n)
	}
	pk.must(201, "POST", "/v1/members", map[string]any{"email": "own2@acme.test", "password": "correct horse battery", "roles": []string{"owner"}})
	own2 := w.passkeyLogin(t, addPasskey(t, w.login(t, "own2@acme.test", "correct horse battery"), "Phone"), 200)
	own2.must(204, "DELETE", "/v1/members/"+memberID(t, own2, "owner@acme.test")+"/passkeys", nil)
	w.passkeyLogin(t, key, 401)
	if st, _ := pk.do("GET", "/v1/me", nil); st != 401 {
		t.Errorf("the reset member's sessions survived: %d", st)
	}
	again := w.login(t, "owner@acme.test", "correct horse battery")
	if me := again.must(200, "GET", "/v1/me", nil); me["enrol_passkey"] != true {
		t.Errorf("after reset: %v", me)
	}
}

func TestPasskeyStepUp(t *testing.T) {
	w := passkeyWorld(t)
	w.srv.RequireAdminPasskeys = false
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "ada@acme.test", "password": "correct horse battery", "roles": []string{"approver", "credit_officer"}})
	ada := w.login(t, "ada@acme.test", "correct horse battery")
	key := addPasskey(t, ada, "Phone")

	policy := `{"rules":[{"levels":[{"role":"credit_officer"}],"step_up":"passkey"}]}`
	owner.must(201, "PUT", "/v1/policies/high_value", map[string]any{"document": json.RawMessage(policy)})
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "disburse", "definition": json.RawMessage(policyFlow)})["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"amount": 5000000}})["run_id"].(string)
	path := "/v1/approvals/" + run + "/ok"

	if st, body := ada.do("POST", path, map[string]any{"decision": "approved"}); st != 403 || body["step_up"] != "passkey" {
		t.Fatalf("without step-up: %d %v", st, body)
	}
	// A sign-in challenge is not a step-up challenge.
	anon := &client{t: t, base: w.base}
	login := challengeFrom(t, anon.must(200, "POST", "/v1/auth/passkey/options", nil))
	if st, _ := ada.do("POST", path, map[string]any{"decision": "approved", "passkey": key.Assert(login).JSON()}); st != 403 {
		t.Errorf("a login challenge passed step-up: %d", st)
	}
	ch := challengeFrom(t, ada.must(200, "POST", "/v1/me/step-up/options", map[string]any{"operation": "approval.decide", "target": run + "/ok/approved"}))
	res := ada.must(200, "POST", path, map[string]any{"decision": "approved", "passkey": key.Assert(ch).JSON()})
	if res["status"] != "approved" {
		t.Fatalf("after passkey step-up: %v", res)
	}
	audit := toJSON(owner.must(200, "GET", "/v1/audit", nil))
	if !strings.Contains(audit, `"step_up":"passkey"`) || !strings.Contains(audit, "passkey.add") {
		t.Errorf("audit lacks the step-up or the enrolment")
	}
}

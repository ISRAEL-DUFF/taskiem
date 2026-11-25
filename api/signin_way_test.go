package api_test

import (
	"context"
	"testing"

	"github.com/israel-duff/taskiem/engine/alerts"
)

// A person in a business with no way to prove a new device (no passkey,
// email or password: the PIN is none) is let in from a new device and
// told so on /v1/me (needs_sign_in_way; sign_in_way_step for an account
// new to the apps). Their first password, set from a session made by a
// phone code, is a way: it proves the device it was set on, and asks for
// itself on the next new device, though their email is not confirmed
// (they chose it). Founder, 25 November 2026.
func TestSignInWayFirst(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.LoginBurst = 100
	w.srv.Alerts = &alerts.Alerter{Mailer: &fakeMail{}, From: "no-reply@taskiem.test"}
	const ada = "+2348031230213"
	signUpOnWhatsApp(t, w, ada, "Ada Foods", "Ada Obi")
	var adaID, foods string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT b.user_id, m.tenant_id FROM whatsapp_bindings b JOIN memberships m ON m.user_id = b.user_id WHERE b.number = $1`, ada).
		Scan(&adaID, &foods); err != nil {
		t.Fatal(err)
	}
	anon := &client{t: t, base: w.base}
	signIn := func(device string) *client {
		t.Helper()
		anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": ada})
		out := anon.must(200, "POST", "/v1/auth/phone/verify", map[string]any{"phone": ada, "code": codeRe.FindString(w.graph.Last(t, ada).Text), "bearer": true, "device": appDevice(device)})
		return &client{t: t, base: w.base, token: out["token"].(string)}
	}
	const d1, d2 = "0192f000-0000-7000-8000-00000000f001", "0192f000-0000-7000-8000-00000000f002"

	one := signIn(d1)
	sw := one.must(200, "POST", "/v1/me/switch", map[string]any{"tenant_id": foods})
	in := &client{t: t, base: w.base, token: sw["token"].(string)}
	u := in.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)
	if u["needs_sign_in_way"] != true || u["sign_in_way_step"] != true {
		t.Fatalf("a new account with no way: %v", u)
	}
	// An account in use before: reminded, not stepped.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET sign_in_way_due = false WHERE id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	u = in.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)
	if u["needs_sign_in_way"] != true || u["sign_in_way_step"] != false {
		t.Fatalf("an account in use with no way: %v", u)
	}

	// The first password, from the code session, with nothing more.
	if st, _ := in.do("POST", "/v1/me/password", map[string]any{"new_password": "short"}); st != 400 {
		t.Fatalf("a short password: %d", st)
	}
	in.must(204, "POST", "/v1/me/password", map[string]any{"new_password": "a long new password 1"})
	u = in.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)
	if u["needs_sign_in_way"] != false {
		t.Fatalf("a chosen password is a way: %v", u)
	}
	var how string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT proved_by FROM user_devices WHERE user_id = $1 AND proved_at IS NOT NULL`, adaID).Scan(&how); err != nil || how != "password" {
		t.Fatalf("the device it was set on is proved: %q %v", how, err)
	}
	// Still in, and the settings still open from here.
	in.must(200, "GET", "/v1/workflows", nil)

	// A new device asks for it, and it proves the device.
	two := signIn(d2)
	me := two.must(200, "GET", "/v1/me", nil)
	proof := me["second_proof"].(map[string]any)
	if proof["required"] != true || proof["ways"].(map[string]any)["password"] != true || proof["ways"].(map[string]any)["email"] != false {
		t.Fatalf("a new device after the password: %v", proof)
	}
	if st, out := two.do("POST", "/v1/auth/second-proof/password", map[string]any{"password": "not it at all 12"}); st != 401 || out["password_wrong"] != true {
		t.Fatalf("a wrong password (the session goes on): %d %v", st, out)
	}
	two.must(200, "POST", "/v1/auth/second-proof/password", map[string]any{"password": "a long new password 1"})
	two.must(200, "POST", "/v1/me/switch", map[string]any{"tenant_id": foods})

	// A password someone else chose for them (not marked chosen), on an
	// email not confirmed, is no way.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET password_chosen_at = NULL WHERE id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	if me := signIn(d1).must(200, "GET", "/v1/me", nil); me["user"].(map[string]any)["needs_sign_in_way"] != true {
		t.Fatalf("a password not chosen by them: %v", me["user"])
	}
}

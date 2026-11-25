package api_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/israel-duff/taskiem/engine/webauthn"
	"github.com/israel-duff/taskiem/engine/webauthn/webauthntest"
)

// The review of stage 1 (docs/spec/taskiem-for-everyone.md, "Review fixes:
// server") found three ways past a second proof (§5.4). Each is refused
// here: the chat-code door, accepting an invitation, and Start something
// then adding a passkey. The chat PIN cannot be guessed in parallel past
// its lock.

// The chat-code door on a new device gives a personal session to a person
// in a business who could give a second proof (C4); the PIN's lock holds
// against guesses sent side by side, and a right PIN while locked is
// refused.
func TestChatSigninNeedsSecondProof(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.LoginBurst = 100
	const ada = "+2348031230303"
	signUpOnWhatsApp(t, w, ada, "Ada Foods", "Ada Obi")
	var adaID, foods string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT b.user_id, m.tenant_id FROM whatsapp_bindings b JOIN memberships m ON m.user_id = b.user_id WHERE b.number = $1`, ada).
		Scan(&adaID, &foods); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET email = 'ada@foods.test', email_verified_at = now() WHERE id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `SELECT taskiem_wa_pin_set($1, $2)`, adaID, pinHash("4829")); err != nil {
		t.Fatal(err)
	}
	anon := &client{t: t, base: w.base}
	anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": ada})
	out := anon.must(200, "POST", "/v1/auth/phone/verify", map[string]any{"phone": ada, "code": codeRe.FindString(w.graph.Last(t, ada).Text), "bearer": true,
		"device": appDevice("0192f000-0000-7000-8000-00000000e001")})
	ph := &client{t: t, base: w.base, token: out["token"].(string)}
	if st, o := ph.do("POST", "/v1/me/switch", map[string]any{"tenant_id": foods}); st != 401 || o["second_proof_required"] != true {
		t.Fatalf("phone door should be locked: %d %v", st, o)
	}

	// The chat door, same number, a new device: the person's own side.
	anon.must(202, "POST", "/v1/auth/chat/start", map[string]any{"phone": ada})
	w.srv.WaitBackground()
	out = anon.must(200, "POST", "/v1/auth/chat/verify", map[string]any{"phone": ada, "code": codeRe.FindString(w.graph.Last(t, ada).Text), "bearer": true,
		"device": appDevice("0192f000-0000-7000-8000-00000000e002")})
	if out["personal"] != true || out["tenant_id"] != nil || out["second_proof_required"] != true {
		t.Fatalf("chat sign-in on a new device: %v", out)
	}
	chat := &client{t: t, base: w.base, token: out["token"].(string)}
	if st, body := chat.do("GET", "/v1/workflows", nil); st != 403 || body["personal_only"] != true {
		t.Fatalf("the chat-code session reached a business route: %d %v", st, body)
	}
	if st, o := chat.do("POST", "/v1/me/switch", map[string]any{"tenant_id": foods}); st != 401 || o["second_proof_required"] != true {
		t.Fatalf("the chat-code session switched in: %d %v", st, o)
	}

	// PIN guesses side by side: at most four are weighed before the lock
	// (the fifth locks it), the rest are refused as locked.
	var evaluated, locked int64
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st, _ := ph.do("POST", "/v1/auth/second-proof/pin", map[string]any{"pin": fmt.Sprintf("1%03d", i)})
			switch st {
			case 401:
				atomic.AddInt64(&evaluated, 1)
			case 429:
				atomic.AddInt64(&locked, 1)
			}
		}(i)
	}
	wg.Wait()
	if evaluated > 4 || evaluated+locked != 60 {
		t.Fatalf("PIN: %d wrong guesses weighed (at most 4), %d refused as locked", evaluated, locked)
	}
	if st, o := ph.do("POST", "/v1/auth/second-proof/pin", map[string]any{"pin": "4829"}); st != 429 || o["locked"] != true {
		t.Fatalf("the right PIN while locked: %d %v", st, o)
	}
}

// Accepting an invitation from a session that needs a second proof joins
// the business but does not enter it (C3).
func TestInvitationAcceptNeedsSecondProof(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.LoginBurst = 100
	const ada = "+2348031230304"
	signUpOnWhatsApp(t, w, ada, "Ada Foods", "Ada Obi")
	var adaID string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT user_id FROM whatsapp_bindings WHERE number = $1`, ada).Scan(&adaID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET email = 'ada2@foods.test', email_verified_at = now() WHERE id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	acme := tenantOf(t, owner)
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO member_invitations (tenant_id, user_id, roles, invited_by) VALUES ($1, $2, '{admin}', 'test')`, acme, adaID); err != nil {
		t.Fatal(err)
	}
	anon := &client{t: t, base: w.base}
	anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": ada})
	out := anon.must(200, "POST", "/v1/auth/phone/verify", map[string]any{"phone": ada, "code": codeRe.FindString(w.graph.Last(t, ada).Text), "bearer": true,
		"device": appDevice("0192f000-0000-7000-8000-00000000e011")})
	ph := &client{t: t, base: w.base, token: out["token"].(string)}
	acc := ph.must(200, "POST", "/v1/me/invitations/"+acme+"/accept", nil)
	ways, _ := acc["ways"].(map[string]any)
	if acc["token"] != nil || acc["sign_in"] != "second_proof_required" || ways["email"] != true || acc["tenant_id"] != acme {
		t.Fatalf("accepted: %v", acc)
	}
	if st, body := ph.do("GET", "/v1/workflows", nil); st != 403 || body["personal_only"] != true {
		t.Fatalf("the session moved into Acme: %d %v", st, body)
	}
	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM memberships WHERE user_id = $1 AND tenant_id = $2`, adaID, acme).Scan(&n); err != nil || n != 1 {
		t.Fatalf("not a member: %v %v", n, err)
	}
}

// Start something from an unproved phone session, then adding a sign-in
// factor in the new workspace, is refused: the session's method and device
// carry over, and it stays unproved (fix 1a).
func TestStartThenAddFactorNeedsSecondProof(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.LoginBurst = 100
	w.srv.WebAuthn = webauthn.Config{RPID: rpID, RPName: "Taskiem", Origins: []string{origin}}
	const ada = "+2348031230305"
	signUpOnWhatsApp(t, w, ada, "Ada Foods", "Ada Obi")
	var adaID, foods string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT b.user_id, m.tenant_id FROM whatsapp_bindings b JOIN memberships m ON m.user_id = b.user_id WHERE b.number = $1`, ada).Scan(&adaID, &foods); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET email = 'ada3@foods.test', email_verified_at = now() WHERE id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	anon := &client{t: t, base: w.base}
	anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": ada})
	out := anon.must(200, "POST", "/v1/auth/phone/verify", map[string]any{"phone": ada, "code": codeRe.FindString(w.graph.Last(t, ada).Text), "bearer": true})
	ph := &client{t: t, base: w.base, token: out["token"].(string)}
	if st, _ := ph.do("POST", "/v1/me/switch", map[string]any{"tenant_id": foods}); st != 401 {
		t.Fatalf("expected locked: %d", st)
	}
	st := ph.must(201, "POST", "/v1/me/start", map[string]any{"kind": "workflows"})
	mine := &client{t: t, base: w.base, token: st["token"].(string)}
	if me := mine.must(200, "GET", "/v1/me", nil); me["auth_method"] != "phone" {
		t.Fatalf("the moved session's method: %v", me["auth_method"])
	}
	for _, c := range []struct {
		method, path string
		body         map[string]any
	}{
		{"POST", "/v1/me/passkeys/options", map[string]any{}}, {"POST", "/v1/me/totp", map[string]any{}},
		{"POST", "/v1/me/email", map[string]any{"email": "eve@evil.test"}},
	} {
		if code, body := mine.do(c.method, c.path, c.body); code != 401 || body["second_proof_required"] != true {
			t.Fatalf("%s %s from an unproved session: %d %v", c.method, c.path, code, body)
		}
	}
	if code, _ := mine.do("POST", "/v1/me/switch", map[string]any{"tenant_id": foods}); code != 401 {
		t.Fatalf("switched from the new workspace into Foods: %d", code)
	}
}

// The person's own passkey proves a new device without changing the
// session (C2); the password does too (fix 3), and both are ways.
func TestSecondProofByPasskeyAndPassword(t *testing.T) {
	ctx := context.Background()
	w := passkeyWorld(t)
	w.srv.RequireAdminPasskeys = false
	w.srv.LoginBurst = 100
	sms := &fakeSMS{}
	w.srv.CodeSMS = sms
	owner := w.tenant(t, "Acme", "owner@acme.test")
	acme := tenantOf(t, owner)
	key := addPasskey(t, owner, "Phone")
	const num = "+2348031230311"
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO user_phones (user_id, number) SELECT id, $1 FROM users WHERE email = 'owner@acme.test'`, num); err != nil {
		t.Fatal(err)
	}
	you, _ := phoneIn(t, w, sms, num, appDevice("0192f000-0000-7000-8000-00000000e021"))
	me := you.must(200, "GET", "/v1/me", nil)
	ways := me["second_proof"].(map[string]any)["ways"].(map[string]any)
	if ways["passkey"] != true || ways["password"] != true {
		t.Fatalf("ways: %v", ways)
	}
	if st, o := you.do("POST", "/v1/me/switch", map[string]any{"tenant_id": acme}); st != 401 || o["second_proof_required"] != true {
		t.Fatalf("locked: %d %v", st, o)
	}
	// Another's passkey proves nothing.
	opts := you.must(200, "POST", "/v1/auth/second-proof/passkey/options", nil)
	if allow := opts["publicKey"].(map[string]any)["allowCredentials"].([]any); len(allow) != 1 {
		t.Fatalf("allowCredentials: %v", allow)
	}
	stranger := webauthntest.New(rpID, origin)
	if st, _ := you.do("POST", "/v1/auth/second-proof/passkey", map[string]any{"credential": stranger.Assert(challengeFrom(t, opts)).JSON()}); st != 401 {
		t.Fatalf("a stranger's passkey: %d", st)
	}
	opts = you.must(200, "POST", "/v1/auth/second-proof/passkey/options", nil)
	if out := you.must(200, "POST", "/v1/auth/second-proof/passkey", map[string]any{"credential": key.Assert(challengeFrom(t, opts)).JSON()}); out["device_proved"] != true {
		t.Fatalf("proved: %v", out)
	}
	if me := you.must(200, "GET", "/v1/me", nil); me["personal"] != true || me["second_proof"].(map[string]any)["device_proved"] != true {
		t.Fatalf("after the passkey: %v", me)
	}
	you.must(200, "POST", "/v1/me/switch", map[string]any{"tenant_id": acme})

	// The password, on another device.
	two, _ := phoneIn(t, w, sms, num, appDevice("0192f000-0000-7000-8000-00000000e022"))
	if st, o := two.do("POST", "/v1/auth/second-proof/password", map[string]any{"password": "not the password"}); st != 401 {
		t.Fatalf("a wrong password: %d %v", st, o)
	}
	two.must(200, "POST", "/v1/auth/second-proof/password", map[string]any{"password": testPassword})
	two.must(200, "POST", "/v1/me/switch", map[string]any{"tenant_id": acme})
}

// A proved app phone's device id is not a key (review of the review fixes,
// taskiem-for-everyone §5.4): the app chooses that id, so whoever holds a
// reissued number and learns it must not inherit the phone's proof. Your
// phones lists a stand-in, never the id; and a sign-in by code starts
// unproved even on a device that was proved.
func TestDeviceIdIsNotASecondProof(t *testing.T) {
	ctx := context.Background()
	w, sms := phoneWorld(t)
	owner := w.tenant(t, "Acme", "owner@replay.test")
	acme := tenantOf(t, owner)
	const num = "+2348031230377"
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO user_phones (user_id, number) SELECT id, $1 FROM users WHERE email = 'owner@replay.test'`, num); err != nil {
		t.Fatal(err)
	}
	// The owner's own phone, proved with their password.
	mine := appDevice("0192f000-0000-7000-8000-00000000f001")
	theirs, _ := phoneIn(t, w, sms, num, mine)
	theirs.must(200, "POST", "/v1/auth/second-proof/password", map[string]any{"password": testPassword})
	in := theirs.must(200, "POST", "/v1/me/switch", map[string]any{"tenant_id": acme})
	owned := &client{t: t, base: w.base, token: in["token"].(string)}
	for _, d := range owned.must(200, "GET", "/v1/me/devices", nil)["devices"].([]any) {
		if d.(map[string]any)["id"] == mine["id"] {
			t.Fatalf("Your phones shows the device id: %v", d)
		}
	}
	// Whoever holds the number presents that same device id: no proof.
	thief, _ := phoneIn(t, w, sms, num, mine)
	if st, o := thief.do("POST", "/v1/me/switch", map[string]any{"tenant_id": acme}); st != 401 || o["second_proof_required"] != true {
		t.Fatalf("a replayed device id entered the business: %d %v", st, o)
	}
}

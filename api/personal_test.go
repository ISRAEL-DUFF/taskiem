package api_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"

	"github.com/israel-duff/taskiem/engine/alerts"
)

// fakeSMS is the SMS provider a person's own codes go through.
type fakeSMS struct {
	mu   sync.Mutex
	sent map[string][]string
}

func (f *fakeSMS) SendCode(_ context.Context, number, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sent == nil {
		f.sent = map[string][]string{}
	}
	f.sent[number] = append(f.sent[number], text)
	return nil
}

// code is the last code texted to number.
func (f *fakeSMS) code(t *testing.T, number string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	got := f.sent[number]
	if len(got) == 0 {
		t.Fatalf("no SMS to %s", number)
	}
	return codeRe.FindString(got[len(got)-1])
}

func (f *fakeSMS) count(number string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent[number])
}

var _ interface {
	SendCode(context.Context, string, string) error
} = (*fakeSMS)(nil)

// phoneWorld is a world whose codes go by SMS.
func phoneWorld(t *testing.T) (*world, *fakeSMS) {
	w := newWorld(t)
	sms := &fakeSMS{}
	w.srv.CodeSMS = sms
	w.srv.LoginBurst = 100 // many sign-ins from one address
	return w, sms
}

func appDevice(id string) map[string]any {
	return map[string]any{"id": id, "name": "Tolu's Tecno", "platform": "android"}
}

// phoneIn signs in or makes an account by phone and returns the session,
// and the answer of the last step.
func phoneIn(t *testing.T, w *world, sms *fakeSMS, number string, device map[string]any) (*client, map[string]any) {
	t.Helper()
	anon := &client{t: t, base: w.base}
	anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": number})
	body := map[string]any{"phone": number, "code": sms.code(t, number), "bearer": true}
	if device != nil {
		body["device"] = device
	}
	out := anon.must(200, "POST", "/v1/auth/phone/verify", body)
	if out["status"] == "new" {
		create := map[string]any{"ticket": out["ticket"], "name": "Tolu Ade", "adult": true, "terms": true, "bearer": true}
		if device != nil {
			create["device"] = device
		}
		out = anon.must(201, "POST", "/v1/auth/phone/create", create)
	}
	return &client{t: t, base: w.base, token: out["token"].(string)}, out
}

// The phone way in (docs/spec/taskiem-for-everyone.md §5.1): a code by SMS
// where WhatsApp is not set up; a new number makes a personal account with
// a name, 18 or over and the terms agreed; the code signs in to it after;
// the session is the person's own side and reaches no business route; the
// limits and the lock.
func TestPhoneWayIn(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.srv.LoginBurst = 100
	anon := &client{t: t, base: w.base}
	const tolu = "+2348031230201"

	// Nothing to send with: said so.
	if st, out := anon.do("POST", "/v1/auth/phone/start", map[string]any{"phone": tolu}); st != 503 {
		t.Fatalf("no way to send: %d %v", st, out)
	}
	if st, out := anon.do("POST", "/v1/auth/phone/start", map[string]any{"phone": tolu, "via": "sms"}); st != 503 || out["sms_unavailable"] != true {
		t.Fatalf("no SMS: %d %v", st, out)
	}
	sms := &fakeSMS{}
	w.srv.CodeSMS = sms

	out := anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": "0803 123 0201"})
	if out["sent_via"] != "sms" || out["expires_in"] != float64(600) {
		t.Fatalf("start: %v", out)
	}
	code := sms.code(t, tolu)
	if st, _ := anon.do("POST", "/v1/auth/phone/verify", map[string]any{"phone": tolu, "code": "000000"}); st != 401 {
		t.Fatalf("a wrong code: %d", st)
	}
	out = anon.must(200, "POST", "/v1/auth/phone/verify", map[string]any{"phone": tolu, "code": code, "bearer": true})
	ticket, _ := out["ticket"].(string)
	if out["status"] != "new" || ticket == "" || out["token"] != nil {
		t.Fatalf("a new number: %v", out)
	}
	// Name, 18 or over and the terms: each required.
	for _, bad := range []map[string]any{
		{"ticket": ticket, "name": "", "adult": true, "terms": true},
		{"ticket": ticket, "name": "Tolu Ade", "adult": false, "terms": true},
		{"ticket": ticket, "name": "Tolu Ade", "adult": true, "terms": false},
	} {
		if st, out := anon.do("POST", "/v1/auth/phone/create", bad); st != 400 {
			t.Fatalf("refused %v: %d %v", bad, st, out)
		}
	}
	made := anon.must(201, "POST", "/v1/auth/phone/create", map[string]any{"ticket": ticket, "name": "Tolu Ade", "adult": true, "terms": true, "bearer": true})
	if made["status"] != "created" || made["personal"] != true || made["tenant_id"] != nil || made["token"] == nil {
		t.Fatalf("made: %v", made)
	}
	if st, _ := anon.do("POST", "/v1/auth/phone/create", map[string]any{"ticket": ticket, "name": "Again", "adult": true, "terms": true}); st != 401 {
		t.Fatalf("a ticket twice: %d", st)
	}
	you := &client{t: t, base: w.base, token: made["token"].(string)}
	me := you.must(200, "GET", "/v1/me", nil)
	user := me["user"].(map[string]any)
	if me["personal"] != true || me["tenant_id"] != nil || me["auth_method"] != "phone" || len(me["roles"].([]any)) != 0 ||
		user["phone"] != tolu || user["name"] != "Tolu Ade" || user["email_placeholder"] != true || len(me["businesses"].([]any)) != 0 {
		t.Fatalf("me: %v", me)
	}
	var adult bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT adult_confirmed_at IS NOT NULL FROM users WHERE id = $1`, made["user_id"]).Scan(&adult); err != nil || !adult {
		t.Fatalf("18 or over not recorded: %v", err)
	}

	// Their own side only: no business route.
	for _, path := range []string{"/v1/workflows", "/v1/members", "/v1/audit", "/v1/me/passkeys", "/v1/me/devices"} {
		if st, body := you.do("GET", path, nil); st != 403 || body["personal_only"] != true {
			t.Errorf("GET %s from a personal session: %d %v", path, st, body)
		}
	}
	if got := you.must(200, "GET", "/v1/me/businesses", nil); len(got["businesses"].([]any)) != 0 || got["personal"] != true {
		t.Fatalf("businesses: %v", got)
	}
	you.must(409, "POST", "/v1/me/switch", map[string]any{"tenant_id": nil})
	you.must(404, "POST", "/v1/me/switch", map[string]any{"tenant_id": uuid.NewString()})
	you.must(200, "GET", "/v1/me/invitations", nil)

	// The number signs in to the account now.
	again, out := phoneIn(t, w, sms, tolu, nil)
	if out["status"] != "signed_in" || out["user_id"] != made["user_id"] {
		t.Fatalf("signed in again: %v", out)
	}
	again.must(204, "POST", "/v1/auth/logout", nil)
	if st, _ := again.do("GET", "/v1/me", nil); st != 401 {
		t.Fatalf("after signing out: %d", st)
	}

	// Five codes an hour per number (two sent so far).
	for range 3 {
		anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": tolu})
	}
	if st, _ := anon.do("POST", "/v1/auth/phone/start", map[string]any{"phone": tolu}); st != 429 {
		t.Fatalf("a sixth code in the hour: %d", st)
	}

	// Five wrong codes lock the number for an hour, the right one included.
	const bola = "+2348031230202"
	anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": bola})
	right := sms.code(t, bola)
	for i := range 4 {
		if st, _ := anon.do("POST", "/v1/auth/phone/verify", map[string]any{"phone": bola, "code": fmt.Sprintf("000%d", i)}); st != 401 {
			t.Fatalf("wrong try %d: %d", i, st)
		}
	}
	if st, out := anon.do("POST", "/v1/auth/phone/verify", map[string]any{"phone": bola, "code": "0009"}); st != 429 || out["locked"] != true {
		t.Fatalf("the fifth wrong: %d %v", st, out)
	}
	if st, _ := anon.do("POST", "/v1/auth/phone/verify", map[string]any{"phone": bola, "code": right}); st != 429 {
		t.Fatalf("the right code while locked: %d", st)
	}
	if st, out := anon.do("POST", "/v1/auth/phone/start", map[string]any{"phone": bola}); st != 429 || out["locked"] != true {
		t.Fatalf("a code while locked: %d %v", st, out)
	}
	if n := sms.count(bola); n != 1 {
		t.Fatalf("codes sent to a locked number: %d", n)
	}
}

// Personal sessions and switching (§5.2, §5.4, §6): a business session
// switches to the person's own side and back; a personal session reaches
// no business route; a password user in no business signs in to their own
// side; a business that requires single sign-on is locked and refused.
func TestPersonalSwitching(t *testing.T) {
	ctx := context.Background()
	w, _ := phoneWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	acme := tenantOf(t, owner)

	me := owner.must(200, "GET", "/v1/me", nil)
	bs := me["businesses"].([]any)
	if me["personal"] != false || len(bs) != 1 || bs[0].(map[string]any)["current"] != true || bs[0].(map[string]any)["lock"] != nil {
		t.Fatalf("a business session's me: %v", me)
	}

	sw := owner.must(200, "POST", "/v1/me/switch", map[string]any{"tenant_id": nil})
	if sw["personal"] != true || sw["tenant_id"] != nil {
		t.Fatalf("to You: %v", sw)
	}
	you := &client{t: t, base: w.base, token: sw["token"].(string)}
	if st, _ := owner.do("GET", "/v1/me", nil); st != 401 {
		t.Fatalf("the business session outlived the switch: %d", st)
	}
	if st, body := you.do("GET", "/v1/workflows", nil); st != 403 || body["personal_only"] != true {
		t.Fatalf("a business route from You: %d %v", st, body)
	}
	me = you.must(200, "GET", "/v1/me", nil)
	if me["personal"] != true || me["auth_method"] != "password" || me["businesses"].([]any)[0].(map[string]any)["locked"] != false {
		t.Fatalf("You's me: %v", me)
	}
	sw = you.must(200, "POST", "/v1/me/switch", map[string]any{"tenant_id": acme})
	biz := &client{t: t, base: w.base, token: sw["token"].(string)}
	biz.must(200, "GET", "/v1/workflows", nil)
	if st, _ := you.do("GET", "/v1/me", nil); st != 401 {
		t.Fatalf("You outlived the switch: %d", st)
	}
	if n := auditCount(t, w, acme, "auth.switch"); n != 2 {
		t.Fatalf("switches audited: %d", n)
	}
	if st, _ := biz.do("POST", "/v1/me/switch", map[string]any{}); st != 400 {
		t.Fatalf("no tenant_id: %d", st)
	}

	// Single sign-on required: locked, and refused on switching.
	ada := w.tenant(t, "Ada Foods", "ada@bank.test")
	bank := tenantOf(t, w.tenant(t, "Bank", "owner@bankco.test"))
	var adaID string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM users WHERE email = 'ada@bank.test'`).Scan(&adaID); err != nil {
		t.Fatal(err)
	}
	conn := uuid.New()
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'viewer')`, bank, adaID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO sso_connections (id, tenant_id, protocol, name, config, enforce, created_by) VALUES ($2, $1, 'oidc', 'Bank', '{}', true, 'test')`, bank, conn); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO sso_domains (domain, connection_id, tenant_id, token, verified_at) VALUES ('bank.test', $2, $1, 'x', now())`, bank, conn); err != nil {
		t.Fatal(err)
	}
	for _, b := range ada.must(200, "GET", "/v1/me/businesses", nil)["businesses"].([]any) {
		m := b.(map[string]any)
		if (m["id"] == bank) != (m["lock"] == "sso_required") {
			t.Fatalf("locks: %v", m)
		}
	}
	if st, out := ada.do("POST", "/v1/me/switch", map[string]any{"tenant_id": bank}); st != 401 || out["sso_required"] != true {
		t.Fatalf("into a single sign-on business: %d %v", st, out)
	}
	sw = ada.must(200, "POST", "/v1/me/switch", map[string]any{"personal": true}) // as tenant_id null
	adaYou := &client{t: t, base: w.base, token: sw["token"].(string)}
	if st, out := adaYou.do("POST", "/v1/me/switch", map[string]any{"tenant_id": bank}); st != 401 || out["sso_required"] != true {
		t.Fatalf("from You into a single sign-on business: %d %v", st, out)
	}

	// A password user in no business: their own side.
	bob := biz.must(201, "POST", "/v1/members", map[string]any{"email": "bob@acme.test", "password": testPassword, "roles": []string{"viewer"}})
	biz.must(204, "DELETE", "/v1/members/"+bob["user_id"].(string)+"/roles/viewer", nil)
	out := (&client{t: t, base: w.base}).must(200, "POST", "/v1/auth/login", map[string]any{"email": "bob@acme.test", "password": testPassword, "bearer": true})
	if out["personal"] != true || out["tenant_id"] != nil {
		t.Fatalf("a password user in no business: %v", out)
	}
}

// pinHash is a chat PIN as the database keeps it (argon2id).
func pinHash(pin string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key := argon2.IDKey([]byte(pin), salt, 1, 8*1024, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", 8*1024, 1, 1, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

var proofLinkRe = regexp.MustCompile(`/second-proof#token=([A-Za-z0-9_-]+)`)

// An owner who signed up on WhatsApp signs in by phone (§5.3): their bound
// number finds them, and becomes their phone. With no second proof to give
// they still enter their business, and it is recorded. Once they have one
// (a confirmed email, a chat PIN), a new device needs it first: the email
// link proves the device that asked and no other; the PIN proves another;
// 90 days unused, a device is new again. Then their phone changes (§5.5).
func TestSecondProofs(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.LoginBurst = 100
	mail := &fakeMail{}
	const ada = "+2348031230203"
	signUpOnWhatsApp(t, w, ada, "Ada Foods", "Ada Obi")
	var adaID, foods string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT b.user_id, m.tenant_id FROM whatsapp_bindings b JOIN memberships m ON m.user_id = b.user_id WHERE b.number = $1`, ada).
		Scan(&adaID, &foods); err != nil {
		t.Fatal(err)
	}
	anon := &client{t: t, base: w.base}
	signIn := func(device string) (*client, map[string]any) {
		t.Helper()
		out := anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": ada})
		if out["sent_via"] != "whatsapp" {
			t.Fatalf("sent: %v", out)
		}
		last := w.graph.Last(t, ada)
		if last.Template != "taskiem_otp" {
			t.Fatalf("the code: %+v", last)
		}
		out = anon.must(200, "POST", "/v1/auth/phone/verify", map[string]any{"phone": ada, "code": codeRe.FindString(last.Text), "bearer": true, "device": appDevice(device)})
		if out["status"] != "signed_in" || out["user_id"] != adaID || out["personal"] != true {
			t.Fatalf("signed in: %v", out)
		}
		return &client{t: t, base: w.base, token: out["token"].(string)}, out
	}
	const d1, d2, d3 = "0192f000-0000-7000-8000-00000000d001", "0192f000-0000-7000-8000-00000000d002", "0192f000-0000-7000-8000-00000000d003"

	// Nothing to prove with: let in, and recorded.
	one, _ := signIn(d1)
	me := one.must(200, "GET", "/v1/me", nil)
	if me["user"].(map[string]any)["phone"] != ada || me["second_proof"].(map[string]any)["required"] != false ||
		me["businesses"].([]any)[0].(map[string]any)["lock"] != nil {
		t.Fatalf("me: %v", me)
	}
	sw := one.must(200, "POST", "/v1/me/switch", map[string]any{"tenant_id": foods})
	(&client{t: t, base: w.base, token: sw["token"].(string)}).must(200, "GET", "/v1/workflows", nil)
	var unproved int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'auth.switch' AND detail->>'second_proof' = 'none_possible'`, foods).
		Scan(&unproved); err != nil || unproved != 1 {
		t.Fatalf("an unproved switch recorded: %d %v", unproved, err)
	}

	// A confirmed email: a new device needs it first.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET email = 'ada@foods.test', email_verified_at = now() WHERE id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	two, _ := signIn(d2)
	me = two.must(200, "GET", "/v1/me", nil)
	proof := me["second_proof"].(map[string]any)
	if proof["required"] != true || proof["device_proved"] != false || proof["ways"].(map[string]any)["email"] != true ||
		me["businesses"].([]any)[0].(map[string]any)["lock"] != "second_proof_required" {
		t.Fatalf("a new device's me: %v", me)
	}
	if st, out := two.do("POST", "/v1/me/switch", map[string]any{"tenant_id": foods}); st != 401 || out["second_proof_required"] != true {
		t.Fatalf("switched without a second proof: %d %v", st, out)
	}
	if st, _ := two.do("POST", "/v1/auth/second-proof/email/start", nil); st != 503 {
		t.Fatalf("no mail: %d", st)
	}
	w.srv.Alerts = &alerts.Alerter{Mailer: mail, From: "no-reply@taskiem.test"}
	sent := two.must(202, "POST", "/v1/auth/second-proof/email/start", nil)
	if sent["sent_to"] != "a•••@foods.test" {
		t.Fatalf("sent: %v", sent)
	}
	var token string
	for _, m := range mail.all() {
		if g := proofLinkRe.FindStringSubmatch(strings.ReplaceAll(m, "=\r\n", "")); g != nil {
			token = g[1]
		}
	}
	if token == "" {
		t.Fatalf("no link in %v", mail.all())
	}
	// Opened on another device: no good.
	three, _ := signIn(d3)
	if st, _ := three.do("POST", "/v1/auth/second-proof/email/verify", map[string]any{"token": token}); st != 400 {
		t.Fatalf("the link on another device: %d", st)
	}
	two.must(200, "POST", "/v1/auth/second-proof/email/verify", map[string]any{"token": token})
	if st, _ := two.do("POST", "/v1/auth/second-proof/email/verify", map[string]any{"token": token}); st != 400 {
		t.Fatalf("a link twice: %d", st)
	}
	if me := two.must(200, "GET", "/v1/me", nil); me["second_proof"].(map[string]any)["device_proved"] != true || me["businesses"].([]any)[0].(map[string]any)["lock"] != nil {
		t.Fatalf("proved: %v", me)
	}
	sw = two.must(200, "POST", "/v1/me/switch", map[string]any{"tenant_id": foods})
	inFoods := &client{t: t, base: w.base, token: sw["token"].(string)}
	inFoods.must(200, "GET", "/v1/workflows", nil)

	// The chat PIN proves the third device.
	if st, _ := three.do("POST", "/v1/auth/second-proof/pin", map[string]any{"pin": "4829"}); st != 409 {
		t.Fatalf("no PIN set: %d", st)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `SELECT taskiem_wa_pin_set($1, $2)`, adaID, pinHash("4829")); err != nil {
		t.Fatal(err)
	}
	if st, out := three.do("POST", "/v1/auth/second-proof/pin", map[string]any{"pin": "1111"}); st != 401 || out["tries_left"] != float64(4) {
		t.Fatalf("a wrong PIN: %d %v", st, out)
	}
	three.must(200, "POST", "/v1/auth/second-proof/pin", map[string]any{"pin": "4829"})
	sw = three.must(200, "POST", "/v1/me/switch", map[string]any{"tenant_id": foods})
	inFoods = &client{t: t, base: w.base, token: sw["token"].(string)}

	// 90 days without signing in on it: the device is new again.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE user_devices SET last_signin_at = now() - interval '91 days' WHERE user_id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	two, _ = signIn(d2)
	if me := two.must(200, "GET", "/v1/me", nil); me["second_proof"].(map[string]any)["required"] != true {
		t.Fatalf("a device unused for 90 days: %v", me)
	}

	// Changing the phone from it: a code to the old number too.
	const newer = "+2348031230204"
	out := two.must(202, "POST", "/v1/me/phone/start", map[string]any{"phone": newer})
	if out["needs_old_code"] != true {
		t.Fatalf("change: %v", out)
	}
	newCode := codeRe.FindString(w.graph.Last(t, newer).Text)
	if st, out := two.do("POST", "/v1/me/phone/verify", map[string]any{"code": newCode}); st != 403 || out["old_code_required"] != true {
		t.Fatalf("without the old number's code: %d %v", st, out)
	}
	two.must(202, "POST", "/v1/me/phone/start", map[string]any{"phone": newer, "confirm_old": true})
	newCode = codeRe.FindString(w.graph.Last(t, newer).Text)
	oldCode := codeRe.FindString(w.graph.Last(t, ada).Text)
	if st, _ := two.do("POST", "/v1/me/phone/verify", map[string]any{"code": newCode, "old_code": "000000"}); st != 401 {
		t.Fatalf("a wrong old code: %d", st)
	}
	two.must(200, "POST", "/v1/me/phone/verify", map[string]any{"code": newCode, "old_code": oldCode})
	if me := two.must(200, "GET", "/v1/me", nil); me["user"].(map[string]any)["phone"] != newer {
		t.Fatalf("the phone: %v", me["user"])
	}
	// The change ended every other session (the PIN-proved device's
	// business session among them) and forgot other devices' proofs.
	if st, _ := inFoods.do("GET", "/v1/me", nil); st != 401 {
		t.Fatalf("another session outlived the change: %d", st)
	}
	var proved int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM user_devices WHERE user_id = $1 AND proved_at IS NOT NULL`, adaID).Scan(&proved); err != nil || proved != 0 {
		t.Fatalf("devices still proved: %d %v", proved, err)
	}
	if st, _ := two.do("POST", "/v1/me/phone/start", map[string]any{"phone": newer}); st != 409 {
		t.Fatalf("its own number again: %d", st)
	}
	// Another account's number: a code goes to it all the same (whose it
	// is is not told), and the change is refused once it is entered.
	const bola = "+2348031230205"
	signUpOnWhatsApp(t, w, bola, "Bola Stores", "Bola")
	two.must(202, "POST", "/v1/me/phone/start", map[string]any{"phone": bola, "confirm_old": true})
	if st, out := two.do("POST", "/v1/me/phone/verify", map[string]any{"code": codeRe.FindString(w.graph.Last(t, bola).Text),
		"old_code": codeRe.FindString(w.graph.Last(t, newer).Text)}); st != 409 {
		t.Fatalf("another account's number: %d %v", st, out)
	}
	// The old number signs in no one now: the WhatsApp binding stays, but
	// the account's phone is the new one.
	anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": ada})
	if out := anon.must(200, "POST", "/v1/auth/phone/verify", map[string]any{"phone": ada, "code": codeRe.FindString(w.graph.Last(t, ada).Text)}); out["status"] != "new" {
		t.Fatalf("the old number: %v", out)
	}
	var changes int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'auth.phone_change'`, foods).Scan(&changes); err != nil || changes != 1 {
		t.Fatalf("the change audited: %d %v", changes, err)
	}
}

// Passkeys on switching (§5.4): someone who chose "Only sign in with my
// passkey" signs in by phone to their own side, sees their business locked
// for a passkey, and is refused; a passkey sign-in enters, and from its
// session's own side they switch back in.
func TestSwitchHeldToPasskeys(t *testing.T) {
	ctx := context.Background()
	w := passkeyWorld(t)
	w.srv.RequireAdminPasskeys = false
	sms := &fakeSMS{}
	w.srv.CodeSMS = sms
	owner := w.tenant(t, "Acme", "owner@acme.test")
	acme := tenantOf(t, owner)
	key := addPasskey(t, owner, "Phone")
	const num = "+2348031230206"
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET passkey_only = true WHERE email = 'owner@acme.test'`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO user_phones (user_id, number) SELECT id, $1 FROM users WHERE email = 'owner@acme.test'`, num); err != nil {
		t.Fatal(err)
	}
	you, _ := phoneIn(t, w, sms, num, appDevice("0192f000-0000-7000-8000-00000000e001"))
	if b := you.must(200, "GET", "/v1/me/businesses", nil)["businesses"].([]any)[0].(map[string]any); b["lock"] != "passkey_required" {
		t.Fatalf("lock: %v", b)
	}
	if st, out := you.do("POST", "/v1/me/switch", map[string]any{"tenant_id": acme}); st != 401 || out["passkey_required"] != true {
		t.Fatalf("switched without the passkey: %d %v", st, out)
	}
	pk := w.passkeyLogin(t, key, 200)
	sw := pk.must(200, "POST", "/v1/me/switch", map[string]any{"tenant_id": nil})
	pkYou := &client{t: t, base: w.base, token: sw["token"].(string)}
	sw = pkYou.must(200, "POST", "/v1/me/switch", map[string]any{"tenant_id": acme})
	(&client{t: t, base: w.base, token: sw["token"].(string)}).must(200, "GET", "/v1/workflows", nil)
}

// A personal session accepts an invitation and is moved into the business.
func TestPersonalAcceptsInvitation(t *testing.T) {
	w, sms := phoneWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	acme := tenantOf(t, owner)
	you, made := phoneIn(t, w, sms, "+2348031230207", nil)
	ctx := context.Background()
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO member_invitations (tenant_id, user_id, roles, invited_by) VALUES ($1, $2, '{viewer}', 'test')`, acme, made["user_id"]); err != nil {
		t.Fatal(err)
	}
	if me := you.must(200, "GET", "/v1/me", nil); me["invitations_only"] != true {
		t.Fatalf("me: %v", me)
	}
	acc := you.must(200, "POST", "/v1/me/invitations/"+acme+"/accept", nil)
	if acc["token"] == nil || acc["personal"] != false {
		t.Fatalf("accepted: %v", acc)
	}
	in := &client{t: t, base: w.base, token: acc["token"].(string)}
	if me := in.must(200, "GET", "/v1/me", nil); me["tenant_id"] != acme {
		t.Fatalf("after accepting: %v", me)
	}
	if st, _ := you.do("GET", "/v1/me", nil); st != 401 {
		t.Fatalf("the personal session outlived the accept: %d", st)
	}
}

// WhatsApp first, "Send by SMS instead" when asked; a browser signing in
// by phone gets its session and device cookies.
func TestPhoneCodeBySMSInsteadAndInABrowser(t *testing.T) {
	w := newWAWorld(t)
	sms := &fakeSMS{}
	w.srv.CodeSMS = sms
	anon := &client{t: t, base: w.base}
	const kemi = "+2348031230208"
	if out := anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": kemi}); out["sent_via"] != "whatsapp" || w.graph.Last(t, kemi).Template != "taskiem_otp" {
		t.Fatalf("WhatsApp first: %v", out)
	}
	if out := anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": kemi, "via": "sms"}); out["sent_via"] != "sms" || sms.count(kemi) != 1 {
		t.Fatalf("by SMS instead: %v", out)
	}
	// The last code sent is the good one.
	raw, _ := json.Marshal(map[string]any{"phone": kemi, "code": sms.code(t, kemi)})
	resp, err := http.Post(w.base+"/v1/auth/phone/verify", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || out["status"] != "new" {
		t.Fatalf("verify: %d %v", resp.StatusCode, out)
	}
	raw, _ = json.Marshal(map[string]any{"ticket": out["ticket"], "name": "Kemi", "adult": true, "terms": true})
	resp, err = http.Post(w.base+"/v1/auth/phone/create", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	var session, device string
	for _, c := range resp.Cookies() {
		switch c.Name {
		case "taskiem_session":
			session = c.Value
		case "taskiem_device":
			device = c.Value
		}
	}
	if resp.StatusCode != 201 || session == "" || device == "" {
		t.Fatalf("a browser's sign-up: %d session %q device %q", resp.StatusCode, session, device)
	}
	req, _ := http.NewRequest("GET", w.base+"/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: "taskiem_session", Value: session})
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out = nil
	_ = json.NewDecoder(resp.Body).Decode(&out)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || out["personal"] != true {
		t.Fatalf("me in the browser: %d %v", resp.StatusCode, out)
	}
}

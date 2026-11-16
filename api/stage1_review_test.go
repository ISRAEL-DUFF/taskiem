package api_test

import (
	"context"
	"crypto/sha256"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/israel-duff/taskiem/engine/alerts"
)

// The review fixes of stage 1 (docs/spec/taskiem-for-everyone.md, "Review
// fixes: server"), beyond the second-proof bypasses
// (second_proof_bypass_test.go).

// Start something is one transaction under a lock on the person: twelve
// starts at once from one session make one tenant, and starts from many
// sessions at once stay within the day's five.
func TestStartSomethingIsAtomic(t *testing.T) {
	w, sms := phoneWorld(t)
	you, made := phoneIn(t, w, sms, "+2348031230310", nil)
	user := made["user_id"].(string)
	var ok, other int64
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if st, _ := you.do("POST", "/v1/me/start", map[string]any{"kind": "workflows"}); st == 201 {
				atomic.AddInt64(&ok, 1)
			} else if st != 401 {
				atomic.AddInt64(&other, 1)
			}
		}()
	}
	wg.Wait()
	if n := startCount(t, w, `SELECT count(*) FROM tenant_onboarding WHERE signed_up_by = $1`, user); ok != 1 || other != 0 || n != 1 {
		t.Fatalf("one session, twelve starts: %d made (%d answered otherwise), %d tenants", ok, other, n)
	}

	// Eight sessions of one person at once: five a day.
	w.tenant(t, "Limits", "limits@start.test")
	sessions := make([]*client, 8)
	for i := range sessions {
		sessions[i] = w.login(t, "limits@start.test", testPassword)
	}
	var made5, refused int64
	for _, c := range sessions {
		wg.Add(1)
		go func(c *client) {
			defer wg.Done()
			switch st, _ := c.do("POST", "/v1/me/start", map[string]any{"kind": "workflows"}); st {
			case 201:
				atomic.AddInt64(&made5, 1)
			case 429:
				atomic.AddInt64(&refused, 1)
			}
		}(c)
	}
	wg.Wait()
	n := startCount(t, w, `SELECT count(*) - 1 FROM memberships m JOIN users u ON u.id = m.user_id WHERE u.email = 'limits@start.test' AND m.role = 'owner'`)
	if made5 != 5 || refused != 3 || n != 5 {
		t.Fatalf("eight starts at once: %d made, %d refused, %d tenants", made5, refused, n)
	}
}

// The onboarding source follows the person's email, not the door: a phone
// person's start is the app's, and the app's tenants wait for a confirmed
// email before sending email for the business, as chat sign-ups do.
func TestStartedByPhoneWaitsForEmail(t *testing.T) {
	w, sms := phoneWorld(t)
	you, _ := phoneIn(t, w, sms, "+2348031230312", nil)
	out := you.must(201, "POST", "/v1/me/start", map[string]any{"kind": "business", "name": "Tolu Cakes", "kinds": []string{"shop"}})
	if v := scalarText(t, w, `SELECT source FROM tenant_onboarding WHERE tenant_id = $1`, out["tenant_id"]); v != "app" {
		t.Fatalf("source: %s", v)
	}
	shop := &client{t: t, base: w.base, token: out["token"].(string)}
	w.srv.Alerts = &alerts.Alerter{Mailer: &fakeMail{}, From: "no-reply@taskiem.test"}
	if w.srv.PublicURL == "" {
		w.srv.PublicURL = "https://taskiem.test"
	}
	if st, body := shop.do("POST", "/v1/members", map[string]any{"email": "bisi@cakes.test", "roles": []string{"viewer"}}); st != 403 {
		t.Fatalf("an email invitation from an app business with no confirmed email: %d %v", st, body)
	}
}

// People's own numbers are read by one rule (engine/phone, C5): a number
// that is not one is 400 phone_invalid; wrong codes and spent tickets say
// so (C1).
func TestPhoneNumbersAndCodeAnswers(t *testing.T) {
	w, sms := phoneWorld(t)
	anon := &client{t: t, base: w.base}
	const tolu = "+2348031230313"
	anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": "+234 0803 123 0313"})
	if sms.count(tolu) != 1 {
		t.Fatalf("+234 0803… is +234 803…")
	}
	anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": "803 123 0313"})
	if sms.count(tolu) != 2 {
		t.Fatalf("a bare 803… is Nigerian")
	}
	for _, bad := range []string{"12345", "2348031230313", "+2346031230313", "hello"} {
		if st, out := anon.do("POST", "/v1/auth/phone/start", map[string]any{"phone": bad}); st != 400 || out["phone_invalid"] != true {
			t.Errorf("%q: %d %v", bad, st, out)
		}
	}
	if st, out := anon.do("POST", "/v1/auth/chat/start", map[string]any{"phone": "12345"}); st != 400 || out["phone_invalid"] != true {
		t.Errorf("chat start: %d %v", st, out)
	}
	if st, out := anon.do("POST", "/v1/auth/phone/verify", map[string]any{"phone": tolu, "code": "000000"}); st != 401 || out["code_wrong"] != true {
		t.Fatalf("a wrong code: %d %v", st, out)
	}
	if st, out := anon.do("POST", "/v1/auth/phone/create", map[string]any{"ticket": "spent", "name": "Tolu", "adult": true, "terms": true}); st != 401 || out["ticket_expired"] != true {
		t.Fatalf("a spent ticket: %d %v", st, out)
	}
	you, _ := phoneIn(t, w, sms, tolu, nil)
	you.must(202, "POST", "/v1/me/phone/start", map[string]any{"phone": "08031230314", "confirm_old": true})
	if st, out := you.do("POST", "/v1/me/phone/verify", map[string]any{"code": "000000", "old_code": sms.code(t, tolu)}); st != 401 || out["code_wrong"] != true {
		t.Fatalf("a wrong change code: %d %v", st, out)
	}
}

// A change code another person asked for the same number, still pending,
// is not replaced: theirs still works.
func TestChangeCodeIsNotOverwritten(t *testing.T) {
	w, sms := phoneWorld(t)
	const a, b, x = "+2348031230315", "+2348031230316", "+2348031230317"
	ac, _ := phoneIn(t, w, sms, a, nil)
	bc, _ := phoneIn(t, w, sms, b, nil)
	ac.must(202, "POST", "/v1/me/phone/start", map[string]any{"phone": x, "confirm_old": true})
	code := sms.code(t, x)
	if st, out := bc.do("POST", "/v1/me/phone/start", map[string]any{"phone": x}); st != 429 {
		t.Fatalf("another's pending code replaced: %d %v", st, out)
	}
	if out := ac.must(200, "POST", "/v1/me/phone/verify", map[string]any{"code": code, "old_code": sms.code(t, a)}); out["phone"] != x {
		t.Fatalf("changed: %v", out)
	}
}

// A WhatsApp number changed away from signs no one in by chat: no code is
// sent to it, and a link from it is refused.
func TestChatSigninRefusesAChangedNumber(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.LoginBurst = 100
	const ada = "+2348031230318"
	signUpOnWhatsApp(t, w, ada, "Ada Foods", "Ada Obi")
	var adaID string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT user_id FROM whatsapp_bindings WHERE number = $1`, ada).Scan(&adaID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO user_phones (user_id, number) VALUES ($1, '+2348031230319')
		ON CONFLICT (user_id) DO UPDATE SET number = EXCLUDED.number`, adaID); err != nil {
		t.Fatal(err)
	}
	before := len(w.graph.Sent(ada))
	anon := &client{t: t, base: w.base}
	anon.must(202, "POST", "/v1/auth/chat/start", map[string]any{"phone": ada})
	w.srv.WaitBackground()
	if n := len(w.graph.Sent(ada)); n != before {
		t.Fatalf("a code went to the old number: %d messages, before %d", n, before)
	}
	tok := "a-chat-link-token-from-before-the-change"
	h := sha256.Sum256([]byte(tok))
	if _, err := w.env.DB.Admin.Exec(ctx, `SELECT taskiem_auth_chat_link_issue($1, $2, 'whatsapp', '10 minutes'::interval, 5)`, adaID, h[:]); err != nil {
		t.Fatal(err)
	}
	if st, out := anon.do("POST", "/v1/auth/chat/link", map[string]any{"token": tok, "bearer": true}); st != 401 {
		t.Fatalf("a link from the old number: %d %v", st, out)
	}
}

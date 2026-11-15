package api_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// Signing up in the phone app as on WhatsApp (docs/spec/mobile-app-m4.md
// N7): off unless listed; a code on WhatsApp proves the number; the
// WhatsApp sign-up's questions answered in the app; the business made with
// its owner bound to the number, the terms recorded, and the phone signed in
// with a device session. A number on an account signs in instead.
func TestAppSignup(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	anon := &client{t: t, base: w.base}
	const ada = "+2348031239901"

	// Off: not listed in TASKIEM_CHAT_SIGNUP; the app is told so.
	chatSignupOn(w.world, "whatsapp")
	if s, _ := anon.do("POST", "/v1/signup/app/code", map[string]any{"phone": ada}); s != 404 {
		t.Fatalf("off: %d", s)
	}
	if c := anon.must(200, "GET", "/v1/app/config", nil); c["chat_signup"] != false {
		t.Errorf("config off: %v", c)
	}
	chatSignupOn(w.world, "whatsapp", "app")
	if c := anon.must(200, "GET", "/v1/app/config", nil); c["chat_signup"] != true {
		t.Errorf("config on: %v", c)
	}

	// Another country is not open.
	if s, out := anon.do("POST", "/v1/signup/app/code", map[string]any{"phone": "+447700900123"}); s != 403 {
		t.Errorf("another country: %d %v", s, out)
	}

	// The code, on WhatsApp; a wrong one, then the right one.
	anon.must(202, "POST", "/v1/signup/app/code", map[string]any{"phone": "0803 123 9901"})
	last := w.graph.Last(t, ada)
	if last.Template != "taskiem_otp" {
		t.Fatalf("the code: %+v", last)
	}
	code := codeRe.FindString(last.Text)
	if s, _ := anon.do("POST", "/v1/signup/app/verify", map[string]any{"phone": ada, "code": "000000"}); s != 401 {
		t.Fatalf("a wrong code: %d", s)
	}
	v := anon.must(200, "POST", "/v1/signup/app/verify", map[string]any{"phone": ada, "code": code})
	token := v["token"].(string)
	first := v["messages"].([]any)[0].(map[string]any)
	if !strings.Contains(first["text"].(string), testTerms.URL) || len(first["buttons"].([]any)) != 2 {
		t.Fatalf("the terms: %v", first)
	}
	// The code works once.
	if s, _ := anon.do("POST", "/v1/signup/app/verify", map[string]any{"phone": ada, "code": code}); s != 401 {
		t.Errorf("a code twice: %d", s)
	}

	device := map[string]any{"id": uuid.NewString(), "name": "Ada’s Tecno", "platform": "android"}
	turn := func(text, reply string) map[string]any {
		t.Helper()
		return anon.must(200, "POST", "/v1/signup/app/turn", map[string]any{"phone": ada, "token": token, "text": text, "reply": reply, "bearer": true, "device": device})
	}
	// Someone without the token is nobody's sign-up.
	if s, out := anon.do("POST", "/v1/signup/app/turn", map[string]any{"phone": ada, "token": "nope", "reply": "su:yes"}); s != 404 || out["ended"] != true {
		t.Errorf("a wrong token: %d %v", s, out)
	}
	turn("", "su:yes")
	turn("Ada Kitchen", "")
	turn("Ada Obi", "")
	turn("food", "")
	done := turn("cash, transfer", "")
	if done["done"] != true || done["token"] == nil || done["tenant_id"] == nil {
		t.Fatalf("the end: %v", done)
	}
	if msgs := done["messages"].([]any); !strings.Contains(msgs[len(msgs)-1].(map[string]any)["text"].(string), "Ada Kitchen") {
		t.Errorf("the last message: %v", msgs)
	}

	// Signed in on this phone, in the new business, as its owner.
	owner := &client{t: t, base: w.base, token: done["token"].(string)}
	me := owner.must(200, "GET", "/v1/me", nil)
	if me["tenant_name"] != "Ada Kitchen" || !strings.Contains(fmt.Sprint(me["roles"]), "owner") {
		t.Errorf("me: %v", me)
	}
	if ds := owner.must(200, "GET", "/v1/me/devices", nil)["devices"].([]any); len(ds) != 1 || ds[0].(map[string]any)["name"] != "Ada’s Tecno" {
		t.Errorf("the phone: %v", ds)
	}
	user := uuid.MustParse(done["user_id"].(string))
	_, source, terms := onboardingOf(t, w.world, user)
	if source != "app" || terms != 2 {
		t.Errorf("onboarding: %s, %d terms", source, terms)
	}
	// Bound to the number: signing in with WhatsApp works, and signing up
	// again with it is refused.
	var bound int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM taskiem_auth_chat_user($1)`, ada).Scan(&bound); err != nil || bound != 1 {
		t.Errorf("bound: %d %v", bound, err)
	}
	anon.must(202, "POST", "/v1/signup/app/code", map[string]any{"phone": ada})
	again := codeRe.FindString(w.graph.Last(t, ada).Text)
	if s, out := anon.do("POST", "/v1/signup/app/verify", map[string]any{"phone": ada, "code": again}); s != 409 || out["sign_in"] != true {
		t.Errorf("a number on an account: %d %v", s, out)
	}
}

// Cancelling ends the app's sign-up, and nothing is made.
func TestAppSignupCancelled(t *testing.T) {
	w := newWAWorld(t)
	chatSignupOn(w.world, "app", "whatsapp")
	anon := &client{t: t, base: w.base}
	const bayo = "+2348031239902"
	anon.must(202, "POST", "/v1/signup/app/code", map[string]any{"phone": bayo})
	v := anon.must(200, "POST", "/v1/signup/app/verify", map[string]any{"phone": bayo, "code": codeRe.FindString(w.graph.Last(t, bayo).Text)})
	out := anon.must(200, "POST", "/v1/signup/app/turn", map[string]any{"phone": bayo, "token": v["token"], "reply": "su:no"})
	if out["ended"] != true || out["done"] != nil {
		t.Errorf("declined: %v", out)
	}
	if s, _ := anon.do("POST", "/v1/signup/app/turn", map[string]any{"phone": bayo, "token": v["token"], "reply": "su:yes"}); s != 404 {
		t.Errorf("after: %d", s)
	}
}

// The sign-up's limits: five guesses at a code, counted even when sent all
// at once; a new code does not give five more; three codes a number while
// one lives; ten an hour from one address; the binding needs the proof the
// right code left, and the proof's expiry ends the sign-up without one.
func TestAppSignupLimits(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.LoginBurst = 1000
	chatSignupOn(w.world, "whatsapp", "app")
	anon := &client{t: t, base: w.base}
	code := func(n string) string {
		t.Helper()
		anon.must(202, "POST", "/v1/signup/app/code", map[string]any{"phone": n})
		return codeRe.FindString(w.graph.Last(t, n).Text)
	}
	wrong := func(right string) string {
		if right == "000000" {
			return "000001"
		}
		return "000000"
	}

	// Twenty guesses at once: five are counted, then the right code is no good.
	const a = "+2348031230001"
	right := code(a)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			anon.do("POST", "/v1/signup/app/verify", map[string]any{"phone": a, "code": wrong(right)})
		}()
	}
	wg.Wait()
	if s, _ := anon.do("POST", "/v1/signup/app/verify", map[string]any{"phone": a, "code": right}); s != 401 {
		t.Errorf("the right code after twenty guesses at once: %d", s)
	}
	// A new code keeps the guesses made: still locked.
	again := code(a)
	if s, _ := anon.do("POST", "/v1/signup/app/verify", map[string]any{"phone": a, "code": again}); s != 401 {
		t.Errorf("a new code after five wrong: %d", s)
	}
	// Three codes a number while one lives.
	code(a)
	if s, _ := anon.do("POST", "/v1/signup/app/code", map[string]any{"phone": a}); s != 429 {
		t.Errorf("a fourth code: %d", s)
	}

	// The binding needs the proof: without one it is refused.
	var outcome string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT taskiem_chat_bind_whatsapp_proven($1, '+2348031230009')`, uuid.New()).Scan(&outcome); err != nil || outcome != "unproven" {
		t.Errorf("a binding with no proof: %q %v", outcome, err)
	}

	// The proof expiring before the last answer: nothing is made.
	const b = "+2348031230002"
	v := anon.must(200, "POST", "/v1/signup/app/verify", map[string]any{"phone": b, "code": code(b)})
	turn := func(text, reply string) map[string]any {
		t.Helper()
		return anon.must(200, "POST", "/v1/signup/app/turn", map[string]any{"phone": b, "token": v["token"], "text": text, "reply": reply, "bearer": true,
			"device": map[string]any{"id": uuid.NewString(), "name": "Tecno", "platform": "android"}})
	}
	turn("", "su:yes")
	turn("Bayo Rice", "")
	turn("Bayo Ade", "")
	turn("food", "")
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE chat_signups SET expires_at = now() - interval '1 second'
		WHERE address_hash = sha256(convert_to('chat-signup:app-proven:' || $1::text, 'UTF8'))`, b); err != nil {
		t.Fatal(err)
	}
	if out := turn("cash", ""); out["done"] == true || out["token"] != nil {
		t.Errorf("made with the proof expired: %v", out)
	}
	var made int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM taskiem_auth_chat_user($1)`, b).Scan(&made); err != nil || made != 0 {
		t.Errorf("bound with the proof expired: %d %v", made, err)
	}

	// Ten codes an hour from one address, whatever the numbers.
	limited := false
	for i := range 12 {
		if s, _ := anon.do("POST", "/v1/signup/app/code", map[string]any{"phone": fmt.Sprintf("+23480312301%02d", 10+i)}); s == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("no limit on codes from one address")
	}
}

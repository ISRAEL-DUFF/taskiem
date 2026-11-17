package api_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/oidc/oidctest"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// Four ways a person's standing was got round, each now closed: an
// emailed chat link that bound a stranger's chat with no proof; removing
// staff past the rule for taking roles away, and past the last-owner
// lock; an administrator's save that turned a single sign-on connection
// back on; and a password link from a chat whose sign-in was turned off.

func accessUserID(t *testing.T, w *world, email string) string {
	t.Helper()
	var id uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT id FROM users WHERE lower(email) = lower($1)`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id.String()
}

func accessAudited(t *testing.T, w *world, action, where string) int {
	t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = $1 AND `+where, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A stranger's chat asks to link with someone else's email. The emailed
// link alone binds nothing: the page reads which chat it is without
// using the link, linking takes the account's factor, and "This is not
// mine" spends the link. A chat linked this way opens nothing on the web
// for a day, and the account is told.
func TestAccessChatLinkNeedsFactor(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	chatSignupOn(w.world, "whatsapp")
	mail := mailWorld(t, w.world)
	victim := w.tenant(t, "Ada Foods", "ada@foods.test")
	victimID := accessUserID(t, w.world, "ada@foods.test")
	other := w.tenant(t, "Bob Shop", "bob@shop.test")
	const stranger = "+2348031239771"
	anon := &client{t: t, base: w.base}

	ask := func() string {
		t.Helper()
		w.send(t, stranger, whatsapptest.Text("hello"))
		w.press(t, stranger, "su:have")
		w.say(t, stranger, "ada@foods.test")
		w.srv.WaitBackground()
		msg, _ := mailedTo(mail, "ada@foods.test")
		m := chatLinkRe.FindStringSubmatch(msg)
		if m == nil {
			t.Fatalf("no link email: %s", msg)
		}
		if !strings.Contains(msg, "lets that chat act as your account") || !strings.Contains(msg, "Never continue unless you asked for this yourself") {
			t.Fatalf("the email does not warn: %s", msg)
		}
		return m[1]
	}
	bound := func() string {
		t.Helper()
		u, _ := w.wa.UserOf(ctx, stranger)
		return u.String()
	}

	// "This is not mine": the link is spent, nothing is bound, it is audited.
	token := ask()
	victim.must(204, "POST", "/v1/me/chat-link/refuse", map[string]any{"token": token})
	if st, _ := victim.do("POST", "/v1/me/chat-link", map[string]any{"token": token, "password": testPassword}); st != 400 {
		t.Fatalf("a refused link still links: %d", st)
	}
	if bound() == victimID || accessAudited(t, w.world, "auth.link_refused", `detail->>'via' = 'email_link'`) == 0 {
		t.Fatalf("refusing: bound to %s, or not audited", bound())
	}

	// The page's look: the channel and the whole number, the link unused;
	// nothing for another account.
	token = ask()
	if st, _ := other.do("POST", "/v1/me/chat-link/preview", map[string]any{"token": token}); st != 400 {
		t.Fatalf("another account previews the link: %d", st)
	}
	for i := 0; i < 2; i++ {
		if out := victim.must(200, "POST", "/v1/me/chat-link/preview", map[string]any{"token": token}); out["channel"] != "whatsapp" || out["address"] != stranger {
			t.Fatalf("preview: %v", out)
		}
	}
	// The link alone, or with a wrong password: 403 as the number's own
	// form answers, nothing bound, the link not spent.
	for _, body := range []map[string]any{{"token": token}, {"token": token, "password": "not the password 1"}} {
		st, out := victim.do("POST", "/v1/me/chat-link", body)
		if st != 403 || toJSON(out["reauth"]) != `["password"]` {
			t.Fatalf("no factor: %d %v", st, out)
		}
		if bound() == victimID {
			t.Fatal("the stranger's chat is bound without the account's factor")
		}
	}
	// A session alone is not a factor for an account that has one, however
	// lately it signed in.
	fresh := w.login(t, "ada@foods.test", testPassword)
	if st, _ := fresh.do("POST", "/v1/me/chat-link", map[string]any{"token": token}); st != 403 {
		t.Fatalf("a fresh session alone links: %d", st)
	}

	// The person says so, with their password: linked, told everywhere.
	if out := victim.must(200, "POST", "/v1/me/chat-link", map[string]any{"token": token, "password": testPassword}); out["linked"] != true {
		t.Fatalf("link: %v", out)
	}
	w.srv.WaitBackground()
	if bound() != victimID {
		t.Fatalf("bound to %s", bound())
	}
	if msg, n := mailedTo(mail, "ada@foods.test"); n == 0 || !strings.Contains(msg, "A chat was just linked to your Taskiem account") {
		t.Fatalf("the account's email was not told: %s", msg)
	}

	// For a day the new chat opens nothing on the web: no sign-in link, no
	// password link, no code from the login page.
	for _, word := range []string{"sign in", "password"} {
		r := w.say(t, stranger, word)
		if signinLinkRe.MatchString(r) || passwordLinkRe.MatchString(r) || !strings.Contains(r, "less than 24 hours ago") || !strings.Contains(r, "/login") {
			t.Fatalf("%q from a chat linked a moment ago: %q", word, r)
		}
	}
	anon.must(202, "POST", "/v1/auth/chat/start", map[string]any{"phone": stranger})
	w.srv.WaitBackground()
	var codes int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM chat_signin_codes WHERE user_id = $1`, victimID).Scan(&codes); err != nil || codes != 0 {
		t.Fatalf("a sign-in code was issued to a chat linked a moment ago: %d %v", codes, err)
	}
	if n := accessAudited(t, w.world, "auth.chat_signin_refused", `detail->>'reason' = 'newly_linked'`); n < 3 {
		t.Fatalf("refusals audited: %d", n)
	}

	// A day on, it signs in as any linked chat does.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE chat_link_requests SET linked_at = now() - interval '25 hours' WHERE user_id = $1`, victimID); err != nil {
		t.Fatal(err)
	}
	if r := w.say(t, stranger, "sign in"); !signinLinkRe.MatchString(r) {
		t.Fatalf("sign in after the day: %q", r)
	}
}

// An administrator (member.manage, no approval.decide, not an owner) may
// not take the approver role from someone, by the roles route or by
// removing them from Staff; an owner may.
func TestAccessStaffRemoveNeedsStanding(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "adm@acme.test", "password": testPassword, "roles": []string{"admin"}})
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "appr@acme.test", "password": testPassword, "roles": []string{"approver"}})
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "adm2@acme.test", "password": testPassword, "roles": []string{"admin", "approver"}})
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "view@acme.test", "password": testPassword, "roles": []string{"viewer"}})
	adm := w.login(t, "adm@acme.test", testPassword)
	roles := func(user string) []string {
		var r []string
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT COALESCE(array_agg(role ORDER BY role), '{}') FROM memberships WHERE user_id = $1`, user).Scan(&r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, email := range []string{"appr@acme.test", "adm2@acme.test"} {
		id := accessUserID(t, w, email)
		before := roles(id)
		if st, _ := adm.do("DELETE", "/v1/members/"+id+"/roles/approver", nil); st != 403 {
			t.Fatalf("%s: the roles route: %d", email, st)
		}
		st, out := adm.do("POST", "/v1/staff/"+id+"/remove", nil)
		if st != 403 || !strings.Contains(fmt.Sprint(out["error"]), "approver") {
			t.Fatalf("%s: an administrator removed an approver: %d %v", email, st, out)
		}
		if after := roles(id); toJSON(after) != toJSON(before) {
			t.Fatalf("%s: roles went from %v to %v", email, before, after)
		}
	}
	// Roles the administrator could take one by one go together.
	view := accessUserID(t, w, "view@acme.test")
	adm.must(204, "POST", "/v1/staff/"+view+"/remove", nil)
	// An owner has the standing.
	appr := accessUserID(t, w, "appr@acme.test")
	owner.must(204, "POST", "/v1/staff/"+appr+"/remove", nil)
	if left := roles(appr); len(left) != 0 {
		t.Fatalf("the owner's removal left %v", left)
	}
}

// Two owners remove each other at the same moment, forty times: the
// business has an owner after every round.
func TestAccessOwnersRemovingEachOtherLeaveOne(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.srv.LoginBurst = 1000
	a := w.tenant(t, "Acme", "a@acme.test")
	a.must(201, "POST", "/v1/members", map[string]any{"email": "b@acme.test", "password": testPassword, "roles": []string{"owner"}})
	b := w.login(t, "b@acme.test", testPassword)
	aID, bID := accessUserID(t, w, "a@acme.test"), accessUserID(t, w, "b@acme.test")
	tenant := tenantOf(t, a)
	for i := 0; i < 40; i++ {
		if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'owner'), ($1, $3, 'owner') ON CONFLICT DO NOTHING`, tenant, aID, bID); err != nil {
			t.Fatal(err)
		}
		if st, _ := a.do("GET", "/v1/me", nil); st != 200 {
			a = w.login(t, "a@acme.test", testPassword)
		}
		if st, _ := b.do("GET", "/v1/me", nil); st != 200 {
			b = w.login(t, "b@acme.test", testPassword)
		}
		var wg sync.WaitGroup
		var sa, sb int
		start := make(chan struct{})
		wg.Add(2)
		go func() { defer wg.Done(); <-start; sa, _ = a.do("POST", "/v1/staff/"+bID+"/remove", nil) }()
		go func() { defer wg.Done(); <-start; sb, _ = b.do("POST", "/v1/staff/"+aID+"/remove", nil) }()
		close(start)
		wg.Wait()
		w.srv.WaitBackground()
		var owners int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(DISTINCT user_id) FROM memberships WHERE tenant_id = $1 AND role = 'owner'`, tenant).Scan(&owners); err != nil {
			t.Fatal(err)
		}
		if owners == 0 || (sa == 204 && sb == 204) {
			t.Fatalf("round %d: A removes B -> %d, B removes A -> %d, owners left %d", i, sa, sb, owners)
		}
	}
	// And by the roles route against the staff route.
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'owner'), ($1, $3, 'owner') ON CONFLICT DO NOTHING`, tenant, aID, bID); err != nil {
		t.Fatal(err)
	}
	a, b = w.login(t, "a@acme.test", testPassword), w.login(t, "b@acme.test", testPassword)
	a.must(204, "DELETE", "/v1/members/"+bID+"/roles/owner", nil)
	if st, out := b.do("POST", "/v1/staff/"+aID+"/remove", nil); st == 204 {
		t.Fatalf("the last owner was removed: %v", out)
	}
	if st, out := a.do("DELETE", "/v1/members/"+aID+"/roles/owner", nil); st != 409 {
		t.Fatalf("the last owner gave up owner: %d %v", st, out)
	}
}

// An administrator's save of a single sign-on connection leaves what it
// does not name as it is, and cannot turn the connection on or off,
// require it, or let new people in: those are an owner's.
func TestAccessSSOUpdateKeepsOwnersSettings(t *testing.T) {
	ctx := context.Background()
	w := ssoWorld(t)
	idp := oidctest.New()
	defer idp.Close()
	owner := w.tenant(t, "Bank", "owner@bank.test")
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "adm@bank.test", "password": testPassword, "roles": []string{"admin"}})
	adm := w.login(t, "adm@bank.test", testPassword)
	cfg := map[string]any{"issuer": idp.URL, "client_id": idp.ClientID, "client_secret": idp.ClientSecret}
	id := owner.must(201, "POST", "/v1/sso", map[string]any{"protocol": "oidc", "name": "Okta", "oidc": cfg, "default_roles": []string{"viewer"}, "enforce": true})["id"].(string)
	state := func() string {
		t.Helper()
		var enforce, jit, disabled, approved bool
		var defaults []string
		var groups string
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT enforce, jit, disabled_at IS NOT NULL, owner_approved_at IS NOT NULL, default_roles, group_roles::text FROM sso_connections WHERE id = $1`, id).
			Scan(&enforce, &jit, &disabled, &approved, &defaults, &groups); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("enforce=%v jit=%v disabled=%v approved=%v defaults=%v groups=%s", enforce, jit, disabled, approved, defaults, groups)
	}
	owner.must(204, "PUT", "/v1/sso/"+id, map[string]any{"default_roles": []string{"viewer"}, "enforce": true, "jit": false, "disabled": true})
	const off = "enforce=true jit=false disabled=true approved=true defaults=[viewer] groups={}"
	if got := state(); got != off {
		t.Fatalf("the owner's settings: %s", got)
	}
	// Nothing named, nothing changed.
	adm.must(204, "PUT", "/v1/sso/"+id, map[string]any{})
	if got := state(); got != off {
		t.Fatalf("an empty save changed it: %s", got)
	}
	// The mapping is the administrator's, with roles they can grant; the
	// rest stays.
	adm.must(204, "PUT", "/v1/sso/"+id, map[string]any{"default_roles": []string{"viewer"}, "group_roles": map[string]any{"ops": []string{"operator"}}})
	const mapped = `enforce=true jit=false disabled=true approved=true defaults=[viewer] groups={"ops": ["operator"]}`
	if got := state(); got != mapped {
		t.Fatalf("an administrator's mapping: %s", got)
	}
	// The web's form sends every field: unchanged ones pass.
	adm.must(204, "PUT", "/v1/sso/"+id, map[string]any{"default_roles": []string{"viewer"}, "group_roles": map[string]any{"ops": []string{"operator"}}, "enforce": true, "jit": false, "disabled": true})
	// On or off, required, new people: refused, each way.
	for _, body := range []map[string]any{{"disabled": false}, {"enforce": false}, {"jit": true}, {"default_roles": []string{"viewer"}, "disabled": false, "enforce": true, "jit": false}} {
		if st, out := adm.do("PUT", "/v1/sso/"+id, body); st != 403 || !strings.Contains(fmt.Sprint(out["error"]), "only an owner") {
			t.Fatalf("an administrator saved %s: %d %v", toJSON(body), st, out)
		}
		if got := state(); got != mapped {
			t.Fatalf("after a refused %s: %s", toJSON(body), got)
		}
	}
	// A role the administrator cannot grant, given or taken away.
	if st, _ := adm.do("PUT", "/v1/sso/"+id, map[string]any{"group_roles": map[string]any{"ops": []string{"operator"}, "risk": []string{"approver"}}}); st != 403 {
		t.Fatalf("an administrator mapped approver: %d", st)
	}
	owner.must(204, "PUT", "/v1/sso/"+id, map[string]any{"group_roles": map[string]any{"ops": []string{"operator"}, "risk": []string{"approver"}}})
	if st, _ := adm.do("PUT", "/v1/sso/"+id, map[string]any{"group_roles": map[string]any{"ops": []string{"operator"}}}); st != 403 {
		t.Fatalf("an administrator unmapped approver: %d", st)
	}
	// The owner turns it on again; enforcement was never dropped.
	owner.must(204, "PUT", "/v1/sso/"+id, map[string]any{"disabled": false})
	if got := state(); !strings.HasPrefix(got, "enforce=true jit=false disabled=false approved=true") {
		t.Fatalf("the owner's enable: %s", got)
	}
}

// Chat sign-in turned off closes both of the chat's doors: "sign in" and
// "password". An email no one confirmed gets no password link, and a
// link sent in chat confirms no email.
func TestAccessChatPasswordFollowsChatSignin(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	mailWorld(t, w.world)
	const bisi = "+2348031230772"
	signUpOnWhatsApp(t, w, bisi, "Bisi Salon", "Bisi Ade")
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users u SET email = 'bisi@salon.test', email_verified_at = now()
		FROM whatsapp_bindings b WHERE b.user_id = u.id AND b.number = $1`, bisi); err != nil {
		t.Fatal(err)
	}
	anon := &client{t: t, base: w.base}
	m := signinLinkRe.FindStringSubmatch(w.say(t, bisi, "sign in"))
	if m == nil {
		t.Fatal("no sign-in link")
	}
	out := anon.must(200, "POST", "/v1/auth/chat/link", map[string]any{"token": m[1], "bearer": true})
	web := &client{t: t, base: w.base, token: out["token"].(string)}
	w.srv.WaitBackground()
	web.must(200, "PUT", "/v1/me/chat-signin", map[string]any{"on": false})

	if r := w.say(t, bisi, "sign in"); signinLinkRe.MatchString(r) || !strings.Contains(r, "is off for you") || strings.Contains(r, "*password*") {
		t.Fatalf("sign in when off: %q", r)
	}
	if r := w.say(t, bisi, "password"); passwordLinkRe.MatchString(r) || !strings.Contains(r, "is off for you") || !strings.Contains(r, "Reset your password from the sign-in page, with your email") {
		t.Fatalf("password when chat sign-in is off: %q", r)
	}
	if n := accessAudited(t, w.world, "auth.chat_signin_refused", `detail->>'reason' = 'off'`); n != 2 {
		t.Fatalf("refusals audited: %d", n)
	}

	// On again, the email not confirmed: no link, and how to confirm it.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET chat_signin = true, email_verified_at = NULL WHERE email = 'bisi@salon.test'`); err != nil {
		t.Fatal(err)
	}
	if r := w.say(t, bisi, "password"); passwordLinkRe.MatchString(r) || !strings.Contains(r, "not confirmed yet") || !strings.Contains(r, "*email you@example.com*") {
		t.Fatalf("password with an unconfirmed email: %q", r)
	}

	// A link sent in chat proves the chat, not the mailbox: used on an
	// account whose email is (by now) unconfirmed, it confirms nothing.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET email_verified_at = now() WHERE email = 'bisi@salon.test'`); err != nil {
		t.Fatal(err)
	}
	pm := passwordLinkRe.FindStringSubmatch(w.say(t, bisi, "password"))
	if pm == nil {
		t.Fatal("no password link with chat sign-in on and the email confirmed")
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET email_verified_at = NULL WHERE email = 'bisi@salon.test'`); err != nil {
		t.Fatal(err)
	}
	anon.must(200, "POST", "/v1/auth/password/reset", map[string]any{"token": pm[1], "password": "from the chat door 1"})
	var verified bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT email_verified_at IS NOT NULL FROM users WHERE email = 'bisi@salon.test'`).Scan(&verified); err != nil || verified {
		t.Fatalf("a chat link confirmed the email: %v %v", verified, err)
	}
}

// Wrong codes are counted across codes: asking for a new code does not
// start the count again, and each lock is longer than the last.
func TestAccessChatCodeGuessesLockAcrossCodes(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	const ada = "+2348031230773"
	signUpOnWhatsApp(t, w, ada, "Ada Foods", "Ada Obi")
	var user uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT user_id FROM whatsapp_bindings WHERE number = $1`, ada).Scan(&user); err != nil {
		t.Fatal(err)
	}
	issue := func() string {
		t.Helper()
		var out string
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT taskiem_auth_chat_code_issue($1, $2, 'whatsapp', '123456', '10 minutes', 100)`, user, ada).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	check := func(code string) string {
		t.Helper()
		var out string
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT outcome FROM taskiem_auth_chat_code_check($1, $2, 5)`, ada, code).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	lock := func() float64 {
		t.Helper()
		var minutes *float64
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT extract(epoch FROM locked_until - now()) / 60 FROM chat_signin_codes WHERE user_id = $1`, user).Scan(&minutes); err != nil {
			t.Fatal(err)
		}
		if minutes == nil {
			return 0
		}
		return *minutes
	}
	// Three wrong on one code, a new code, two more: locked, though no
	// one code had five.
	if issue() != "ok" || check("000001") != "wrong" || check("000002") != "wrong" || check("000003") != "wrong" {
		t.Fatal("the first code")
	}
	if issue() != "ok" || check("000004") != "wrong" || check("000005") != "wrong" {
		t.Fatal("the second code")
	}
	if m := lock(); m < 14 || m > 15 {
		t.Fatalf("after five wrong across two codes: locked %.1f minutes", m)
	}
	if got := check("123456"); got != "locked" {
		t.Fatalf("the right code while locked: %s", got)
	}
	if got := issue(); got != "locked" {
		t.Fatalf("a new code while locked: %s", got)
	}
	// The lock over, five more: twice as long.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE chat_signin_codes SET locked_until = now() - interval '1 second' WHERE user_id = $1`, user); err != nil {
		t.Fatal(err)
	}
	if issue() != "ok" {
		t.Fatal("a code after the lock")
	}
	for i := 0; i < 5; i++ {
		check(fmt.Sprintf("00001%d", i))
	}
	if m := lock(); m < 29 || m > 30 {
		t.Fatalf("the second lock: %.1f minutes", m)
	}
	// A right code clears the count.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE chat_signin_codes SET locked_until = NULL WHERE user_id = $1`, user); err != nil {
		t.Fatal(err)
	}
	if issue() != "ok" || check("123456") != "ok" {
		t.Fatal("the right code")
	}
	var strikes int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT strikes FROM chat_signin_codes WHERE user_id = $1`, user).Scan(&strikes); err != nil || strikes != 0 {
		t.Fatalf("strikes after a right code: %d %v", strikes, err)
	}

	// From the login page, a wrong code is audited for the person.
	anon := &client{t: t, base: w.base}
	anon.must(202, "POST", "/v1/auth/chat/start", map[string]any{"phone": ada})
	w.srv.WaitBackground()
	if st, _ := anon.do("POST", "/v1/auth/chat/verify", map[string]any{"phone": ada, "code": "000000"}); st != 401 {
		t.Fatalf("a wrong code: %d", st)
	}
	if n := accessAudited(t, w.world, "auth.chat_signin_refused", `detail->>'reason' = 'wrong' AND detail->>'how' = 'code'`); n == 0 {
		t.Fatal("a wrong code was not audited")
	}
}

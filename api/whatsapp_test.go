package api_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/db"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

const (
	waPNID      = "106540352242922"
	waToken     = "EAAplatform"
	waSecret    = "app-secret-platform"
	waVerify    = "vt-platform"
	waPublicURL = "https://taskiem.test"
)

var waKey = bytes.Repeat([]byte{42}, 32)

// waFlowKey is the Flows endpoint's key pair, made once per test binary.
var waFlowKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
})

type waWorld struct {
	*world
	graph *whatsapptest.Graph
	wa    *whatsapp.Platform
	hook  string
	seq   atomic.Int64
}

func newWAWorld(t *testing.T) *waWorld {
	e := rt.New(t)
	g := whatsapptest.New(t, waPNID, waToken)
	wa := whatsapp.New(e.Store.Pool, whatsapp.Config{PhoneNumberID: waPNID, AccessToken: waToken, AppSecret: waSecret, VerifyToken: waVerify,
		TokenKey: waKey, GraphURL: g.URL, DisplayNumber: "+15550001111"}, e.Egress, slog.New(slog.DiscardHandler))
	wa.Secrets = e.Vault
	wa.Meter = &whatsapp.UsageMeter{Pool: e.Store.Pool, Allowance: func(ctx context.Context, t uuid.UUID) (int64, error) {
		l, err := e.Store.LimitsFor(ctx, t)
		return l.WhatsAppTemplatesMonthly, err
	}}
	srv := &api.Server{Store: e.Store, Vault: e.Vault, Registry: e.Registry, Connectors: e.Connectors, AllowSignup: true, Egress: e.Egress,
		Logger: slog.New(slog.DiscardHandler), WhatsApp: wa, PublicURL: waPublicURL}
	mux := http.NewServeMux()
	hooks := http.StripPrefix("/channels/whatsapp", srv.WhatsAppHooks())
	mux.Handle("/channels/whatsapp", hooks)
	mux.Handle("/channels/whatsapp/", hooks)
	mux.Handle("/", srv.Handler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &waWorld{world: &world{env: e, base: ts.URL, srv: srv}, graph: g, wa: wa, hook: ts.URL + "/channels/whatsapp"}
}

// deliver posts a signed webhook delivery; it returns the status.
func (w *waWorld) deliver(t *testing.T, body []byte, sig string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", w.hook, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// send delivers one message from a number and returns what the platform
// sent back to it.
func (w *waWorld) send(t *testing.T, from string, msg map[string]any) []whatsapptest.Sent {
	t.Helper()
	before := len(w.graph.Sent(from))
	body := whatsapptest.Delivery(waPNID, from, fmt.Sprintf("wamid.in%d", w.seq.Add(1)), msg)
	if st := w.deliver(t, body, whatsapp.Sign(waSecret, body)); st != 200 {
		t.Fatalf("delivery: %d", st)
	}
	return w.graph.Sent(from)[before:]
}

// say sends text and returns the single reply's text.
func (w *waWorld) say(t *testing.T, from, text string) string {
	t.Helper()
	got := w.send(t, from, whatsapptest.Text(text))
	if len(got) != 1 {
		t.Fatalf("%q from %s: %d replies: %+v", text, from, len(got), got)
	}
	return got[0].Text
}

var codeRe = regexp.MustCompile(`\b\d{6}\b`)

// bind links a number to the client's person through the web app.
func (w *waWorld) bind(t *testing.T, c *client, number string) {
	t.Helper()
	c.must(202, "POST", "/v1/me/whatsapp", map[string]any{"number": number, "password": testPassword})
	last := w.graph.Last(t, number)
	if last.Template != "taskiem_otp" {
		t.Fatalf("code message: %+v", last)
	}
	c.must(200, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": codeRe.FindString(last.Text)})
}

func (w *waWorld) member(t *testing.T, owner *client, email string, roles ...string) *client {
	t.Helper()
	owner.must(201, "POST", "/v1/members", map[string]any{"email": email, "password": testPassword, "roles": roles})
	return w.login(t, email, testPassword)
}

func (w *waWorld) tick(t *testing.T) {
	t.Helper()
	if err := w.srv.WhatsAppTick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
}

func TestWhatsAppBinding(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	got := owner.must(200, "GET", "/v1/me/whatsapp", nil)
	if got["enabled"] != true || got["number"] != nil || got["platform_number"] != "+15550001111" {
		t.Fatalf("before: %v", got)
	}
	// Binding needs the person to prove it is them.
	st, body := owner.do("POST", "/v1/me/whatsapp", map[string]any{"number": "08012345678"})
	if st != 403 || body["reauth"] == nil {
		t.Fatalf("without proof: %d %v", st, body)
	}
	owner.must(400, "POST", "/v1/me/whatsapp", map[string]any{"number": "not a number", "password": testPassword})

	// A national number takes +234; the code arrives as the OTP template.
	out := owner.must(202, "POST", "/v1/me/whatsapp", map[string]any{"number": "0801 234 5678", "password": testPassword})
	if out["number"] != "+2348012345678" {
		t.Fatalf("number: %v", out)
	}
	code := codeRe.FindString(w.graph.Last(t, "+2348012345678").Text)
	wrong := fmt.Sprintf("%06d", (atoi(code)+1)%1_000_000)
	owner.must(400, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": wrong})
	owner.must(400, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": "12ab"})
	if p := owner.must(200, "GET", "/v1/me/whatsapp", nil)["pending"]; p == nil {
		t.Error("no pending code shown")
	}
	owner.must(200, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": code})
	got = owner.must(200, "GET", "/v1/me/whatsapp", nil)
	if got["number"] != "+2348012345678" || got["verified_at"] == nil || got["pending"] != nil {
		t.Fatalf("after: %v", got)
	}
	// A spent code does not work twice.
	owner.must(400, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": code})

	// The number is someone's: another person cannot bind it.
	bob := w.member(t, owner, "bob@acme.test", "viewer")
	bob.must(409, "POST", "/v1/me/whatsapp", map[string]any{"number": "+2348012345678", "password": testPassword})

	// Five wrong codes spend a code.
	bob.must(202, "POST", "/v1/me/whatsapp", map[string]any{"number": "+2348099990000", "password": testPassword})
	bobCode := codeRe.FindString(w.graph.Last(t, "+2348099990000").Text)
	bobWrong := fmt.Sprintf("%06d", (atoi(bobCode)+7)%1_000_000)
	for range 5 {
		bob.must(400, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": bobWrong})
	}
	bob.must(429, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": bobCode})

	// An expired code is refused.
	bob.must(202, "POST", "/v1/me/whatsapp", map[string]any{"number": "+2348099990000", "password": testPassword})
	bobCode = codeRe.FindString(w.graph.Last(t, "+2348099990000").Text)
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE whatsapp_otps SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	bob.must(400, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": bobCode})
	// Codes are paced: a few every few minutes (the refusal above counted).
	bob.must(429, "POST", "/v1/me/whatsapp", map[string]any{"number": "+2348099990000", "password": testPassword})

	// Or the code is sent back from the number itself.
	carol := w.member(t, owner, "carol@acme.test", "viewer")
	carol.must(202, "POST", "/v1/me/whatsapp", map[string]any{"number": "+2348077770000", "password": testPassword})
	if reply := w.say(t, "+2348077770000", "000000"); !strings.Contains(reply, "not linked") {
		t.Errorf("a wrong code from the number: %q", reply)
	}
	if reply := w.say(t, "+2348077770000", codeRe.FindString(w.graph.Sent("+2348077770000")[0].Text)); !strings.Contains(reply, "now linked") {
		t.Fatalf("reply with the code: %q", reply)
	}
	if carol.must(200, "GET", "/v1/me/whatsapp", nil)["number"] != "+2348077770000" {
		t.Error("binding by reply")
	}

	// Unbinding; the number can then be bound by someone else.
	owner.must(204, "DELETE", "/v1/me/whatsapp", nil)
	owner.must(404, "DELETE", "/v1/me/whatsapp", nil)
	if owner.must(200, "GET", "/v1/me/whatsapp", nil)["number"] != nil {
		t.Error("still bound")
	}
	audit := toJSON(owner.must(200, "GET", "/v1/audit", nil))
	for _, a := range []string{"whatsapp.code_sent", "whatsapp.bind", "whatsapp.unbind"} {
		if !strings.Contains(audit, a) {
			t.Errorf("audit lacks %s", a)
		}
	}
	if strings.Contains(audit, "8012345678") {
		t.Error("audit shows the full number")
	}
	// API keys have no number.
	key := owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "k", "permissions": []string{"run.read"}})["key"].(string)
	(&client{t: t, base: w.base, token: key}).must(403, "POST", "/v1/me/whatsapp", map[string]any{"number": "+2348011112222"})
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

func TestWhatsAppInbound(t *testing.T) {
	w := newWAWorld(t)
	// The subscription handshake.
	resp, err := http.Get(w.hook + "?hub.mode=subscribe&hub.verify_token=" + waVerify + "&hub.challenge=1158201444")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || string(b) != "1158201444" {
		t.Fatalf("handshake: %d %s", resp.StatusCode, b)
	}
	resp, _ = http.Get(w.hook + "?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=1")
	_ = resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("wrong verify token: %d", resp.StatusCode)
	}

	from := "+2348011112222"
	body := whatsapptest.Delivery(waPNID, from, "wamid.dup", whatsapptest.Text("help"))
	if st := w.deliver(t, body, ""); st != 401 {
		t.Errorf("unsigned: %d", st)
	}
	if st := w.deliver(t, body, whatsapp.Sign("not the secret", body)); st != 401 {
		t.Errorf("wrong secret: %d", st)
	}
	if n := len(w.graph.Sent("")); n != 0 {
		t.Fatalf("refused deliveries were answered: %d", n)
	}
	// An unbound number is told how to link it, and nothing else.
	if st := w.deliver(t, body, whatsapp.Sign(waSecret, body)); st != 200 {
		t.Fatal(st)
	}
	sent := w.graph.Sent(from)
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "not linked") || !strings.Contains(sent[0].Text, waPublicURL+"/account") {
		t.Fatalf("unbound reply: %+v", sent)
	}
	// Meta's retry of the same message is not handled again.
	if st := w.deliver(t, body, whatsapp.Sign(waSecret, body)); st != 200 {
		t.Fatal(st)
	}
	if n := len(w.graph.Sent(from)); n != 1 {
		t.Errorf("a retried delivery was handled again: %d replies", n)
	}
	// Messages to another number on the same app are ignored.
	other := whatsapptest.Delivery("999", from, "wamid.other", whatsapptest.Text("help"))
	w.deliver(t, other, whatsapp.Sign(waSecret, other))
	if n := len(w.graph.Sent(from)); n != 1 {
		t.Errorf("another number's message was handled: %d", n)
	}
}

func TestWhatsAppTenantsAndRateLimit(t *testing.T) {
	w := newWAWorld(t)
	acme := w.tenant(t, "Acme", "owner@acme.test")
	beta := w.tenant(t, "Beta", "owner@beta.test")
	beta.must(201, "POST", "/v1/members", map[string]any{"email": "owner@acme.test", "roles": []string{"viewer"}})
	inv := acme.must(200, "GET", "/v1/me/invitations", nil)["invitations"].([]any)
	acme.must(200, "POST", "/v1/me/invitations/"+inv[0].(map[string]any)["tenant_id"].(string)+"/accept", nil)
	n := "+2348033334444"
	w.bind(t, acme, n)

	if r := w.say(t, n, "status"); !strings.HasPrefix(r, "[Acme] ") || !strings.Contains(r, "No runs") {
		t.Errorf("status in Acme: %q", r)
	}
	if r := w.say(t, n, "help"); !strings.Contains(r, "switch") {
		t.Errorf("help for two tenants: %q", r)
	}
	if r := w.say(t, n, "switch"); !strings.Contains(r, "Beta") {
		t.Errorf("list: %q", r)
	}
	if r := w.say(t, n, "switch nowhere"); !strings.Contains(r, "Which organisation") {
		t.Errorf("unknown: %q", r)
	}
	if r := w.say(t, n, "Switch beta"); r != "[Beta] You are now working in Beta." {
		t.Errorf("switch: %q", r)
	}
	if r := w.say(t, n, "run anything"); !strings.HasPrefix(r, "[Beta] ") || !strings.Contains(r, "run.start") {
		t.Errorf("a viewer in Beta: %q", r)
	}
	if r := w.say(t, n, "What failed today?"); !strings.HasPrefix(r, "[Beta] ") {
		t.Errorf("status in Beta: %q", r)
	}

	// Per number: a flood is cut off, with one warning.
	replies := 0
	warned := 0
	for range 30 {
		for _, s := range w.send(t, n, whatsapptest.Text("hello")) {
			replies++
			if strings.Contains(s.Text, "too quickly") {
				warned++
			}
		}
	}
	if warned != 1 || replies >= 30 {
		t.Errorf("flood: %d replies, %d warnings", replies, warned)
	}
}

const waLoanFlow = `{"schema":"wd/v1","id":"wf_loan","version":1,"name":"loan","trigger":{"type":"manual"},
  "inputs":{"schema":{"type":"object","required":["bvn","amount","account_number"],"properties":{
    "bvn":{"type":"string","x-pii":"bvn"},"amount":{"type":"integer","minimum":1},"account_number":{"type":"string","x-pii":"account_number"}}}},
  "steps":[
    {"id":"ok","type":"approval","config":{"role":"credit_officer","count":2,"subject":{"bvn":"=trigger.body.bvn","amount":"=trigger.body.amount","account_number":"=trigger.body.account_number"}}},
    {"id":"done","type":"transform","needs":["ok"],"config":{"output":{"decision":"=steps.ok.output.decision"}}}]}`

func TestWhatsAppApprovals(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "owner@acme.test", "roles": []string{"credit_officer", "approver"}})
	alice := w.member(t, owner, "alice@acme.test", "approver", "credit_officer")
	bob := w.member(t, owner, "bob@acme.test", "approver", "credit_officer")
	carol := w.member(t, owner, "carol@acme.test", "approver", "credit_officer")
	const an, bn, cn, on = "+2348010000001", "+2348010000002", "+2348010000003", "+2348010000009"
	w.bind(t, alice, an)
	w.bind(t, bob, bn)
	w.bind(t, carol, cn)
	w.bind(t, owner, on)
	w.say(t, an, "hi") // Alice's window is open; Bob's is not.

	wf := publishFlow(t, owner, waLoanFlow)
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"bvn": "22212345678", "amount": 5000, "account_number": "0123456789"}})["run_id"].(string)
	w.tick(t)

	// Alice: interactive buttons with signed tokens; Bob: the template with
	// the tokens as quick-reply payloads. Personal data masked.
	am := w.graph.Last(t, an)
	if am.Type != "interactive" || len(am.Buttons) != 2 || !strings.HasPrefix(am.Buttons[0].ID, "tk1.") || !strings.HasPrefix(am.Text, "[Acme] Approval needed: loan · ok") {
		t.Fatalf("alice's request: %+v", am)
	}
	bm := w.graph.Last(t, bn)
	if bm.Type != "template" || bm.Template != "taskiem_approval_request" || len(bm.Payloads) != 2 {
		t.Fatalf("bob's request: %+v", bm)
	}
	for _, m := range []whatsapptest.Sent{am, bm} {
		if strings.Contains(m.Text, "22212345678") || strings.Contains(m.Text, "0123456789") || !strings.Contains(m.Text, "••••6789") || !strings.Contains(m.Text, "[bvn]") {
			t.Errorf("request shows personal data, or not the last four: %q", m.Text)
		}
	}
	// The maker (who also holds the role) is not asked.
	for _, s := range w.graph.Sent(on) {
		if strings.Contains(s.Text, "Approval needed") {
			t.Errorf("the maker was asked: %q", s.Text)
		}
	}
	// Once per approver and level.
	before := len(w.graph.Sent(""))
	w.tick(t)
	if len(w.graph.Sent("")) != before {
		t.Error("a second tick sent again")
	}

	tap := func(from, tok string) string {
		t.Helper()
		got := w.send(t, from, whatsapptest.ButtonReply(tok, "Approve"))
		if len(got) != 1 {
			t.Fatalf("tap: %+v", got)
		}
		return got[0].Text
	}
	approveA, rejectA := am.Buttons[0].ID, am.Buttons[1].ID

	// Forged: altered, or signed with another key.
	forged := approveA[:len(approveA)-3] + "AAA"
	if r := tap(an, forged); !strings.Contains(r, "not valid") {
		t.Errorf("forged: %q", r)
	}
	h, _, _ := whatsapp.ParseToken(approveA)
	other := whatsapp.Signer{Key: bytes.Repeat([]byte{1}, 32)}.Sign(h)
	if r := tap(an, other); !strings.Contains(r, "not valid") {
		t.Errorf("other key: %q", r)
	}
	// Wrong sender: Alice's button from Bob's number.
	if r := tap(bn, approveA); !strings.Contains(r, "someone else") {
		t.Errorf("wrong sender: %q", r)
	}
	// Alice approves.
	if r := tap(an, approveA); !strings.Contains(r, "Recorded: you approved ok") || !strings.Contains(r, "more approvals") {
		t.Fatalf("approve: %q", r)
	}
	// Replay, and the pair's other button, are spent.
	if r := tap(an, approveA); !strings.Contains(r, "already answered") {
		t.Errorf("replay: %q", r)
	}
	if r := tap(an, rejectA); !strings.Contains(r, "already answered") {
		t.Errorf("pair: %q", r)
	}
	// Self-approval is still refused, even with a valid button: the owner
	// asks for what is waiting and taps Approve.
	got := w.send(t, on, whatsapptest.Text("approvals"))
	if len(got) != 1 || len(got[0].Buttons) != 2 {
		t.Fatalf("owner's approvals: %+v", got)
	}
	if r := tap(on, got[0].Buttons[0].ID); !strings.Contains(r, "maker") {
		t.Errorf("self-approval: %q", r)
	}
	// Bob approves from the template: the run continues to completion.
	got = w.send(t, bn, whatsapptest.QuickReply(bm.Payloads[0], "Approve"))
	if len(got) != 1 || !strings.Contains(got[0].Text, "request is approved") {
		t.Fatalf("bob: %+v", got)
	}
	w.env.Drain(t)
	res := owner.must(200, "GET", "/v1/runs/"+run, nil)
	if st := res["run"].(map[string]any)["status"]; st != "completed" {
		t.Fatalf("run: %v", st)
	}
	if !strings.Contains(toJSON(res["events"]), `"channel":"whatsapp"`) {
		t.Error("the decision is not recorded with channel whatsapp")
	}
	audit := toJSON(owner.must(200, "GET", "/v1/audit", nil))
	for _, a := range []string{`"approval.decide"`, `"approval.decide.refused"`, `"approval.token.refused"`} {
		if !strings.Contains(audit, a) {
			t.Errorf("audit lacks %s", a)
		}
	}
	// Carol's request is now closed.
	cm := w.graph.Last(t, cn)
	if r := tap(cn, cm.Payloads[0]); !strings.Contains(r, "already approved") {
		t.Errorf("closed: %q", r)
	}

	// A second run, rejected from WhatsApp.
	run2 := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"bvn": "22212345678", "amount": 7, "account_number": "0123456789"}})["run_id"].(string)
	w.tick(t)
	am = w.graph.Last(t, an)
	if r := tap(an, am.Buttons[1].ID); !strings.Contains(r, "you rejected ok") || !strings.Contains(r, "request is rejected") {
		t.Fatalf("reject: %q", r)
	}
	w.env.Drain(t)
	res = owner.must(200, "GET", "/v1/runs/"+run2, nil)
	if !strings.Contains(toJSON(res["events"]), `"decision":"rejected"`) {
		t.Errorf("rejection not in history: %s", toJSON(res["events"]))
	}

	// An expired token, correctly signed, is refused.
	run3 := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"bvn": "22212345678", "amount": 9, "account_number": "0123456789"}})["run_id"].(string)
	tenant := uuid.MustParse(owner.must(200, "GET", "/v1/me", nil)["tenant_id"].(string))
	aliceID := uuid.MustParse(alice.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)["id"].(string))
	c := whatsapp.Claims{Tenant: tenant, Nonce: whatsapp.NewNonce(), Purpose: whatsapp.PurposeDecide, Decision: "approved",
		Expires: time.Unix(time.Now().Add(-time.Minute).Unix(), 0), Run: uuid.MustParse(run3), Step: "ok", Level: 0, User: aliceID}
	if err := db.InTenantTx(context.Background(), w.env.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO whatsapp_tokens (nonce, tenant_id, purpose, run_id, step_id, level, user_id, decision, notice, expires_at)
			VALUES ($1, $2, 'decide', $3, 'ok', 0, $4, 'approved', $5, $6)`, c.Nonce[:], tenant, c.Run, aliceID, uuid.New(), c.Expires)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if r := tap(an, whatsapp.Signer{Key: waKey}.Sign(c)); !strings.Contains(r, "not valid") {
		t.Errorf("expired: %q", r)
	}
	_ = bob
	_ = carol
}

func TestWhatsAppStepUpHandoff(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	alice := w.member(t, owner, "alice@acme.test", "approver", "credit_officer")
	const an = "+2348010000001"
	w.bind(t, alice, an)
	w.say(t, an, "hi")
	secret := enrol(t, alice)
	owner.must(201, "PUT", "/v1/policies/high_value", map[string]any{"document": json.RawMessage(
		`{"rules":[{"when":"=subject.amount >= 0","levels":[{"role":"credit_officer"}],"step_up":"totp"}]}`)})
	created := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "disburse", "definition": json.RawMessage(policyFlow)})
	wf := created["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"amount": 2400000}})["run_id"].(string)
	w.tick(t)
	am := w.graph.Last(t, an)
	got := w.send(t, an, whatsapptest.ButtonReply(am.Buttons[0].ID, "Approve"))
	if len(got) != 1 || !strings.Contains(got[0].Text, waPublicURL+"/handoff#tk1.") {
		t.Fatalf("hand-off: %+v", got)
	}
	tok := got[0].Text[strings.Index(got[0].Text, "#")+1:]
	if r := w.say(t, an, "status"); !strings.Contains(r, "Last 24 hours") {
		t.Errorf("commands still work while a hand-off waits: %q", r)
	}
	// Only Alice, in the web app, can use it; with step-up.
	owner.must(403, "GET", "/v1/whatsapp/handoff/"+tok, nil)
	view := alice.must(200, "GET", "/v1/whatsapp/handoff/"+tok, nil)
	if view["decision"] != "approved" || view["workflow"] != "disburse" || view["step_up"] != "totp" {
		t.Fatalf("view: %v", view)
	}
	st, body := alice.do("POST", "/v1/whatsapp/handoff/"+tok, map[string]any{})
	if st != 403 || body["step_up"] == nil {
		t.Errorf("without step-up: %d %v", st, body)
	}
	alice.must(403, "POST", "/v1/whatsapp/handoff/"+tok, map[string]any{"totp": "000000"})
	res := alice.must(200, "POST", "/v1/whatsapp/handoff/"+tok, map[string]any{"totp": codeAfter(secret, 1)})
	if res["status"] != "approved" {
		t.Fatalf("decided: %v", res)
	}
	if last := w.graph.Last(t, an); !strings.Contains(last.Text, "Recorded with step-up") {
		t.Errorf("confirmation: %+v", last)
	}
	alice.must(409, "POST", "/v1/whatsapp/handoff/"+tok, map[string]any{"totp": codeAfter(secret, 2)})
	alice.must(400, "GET", "/v1/whatsapp/handoff/tk1.garbage", nil)
	w.env.Drain(t)
	r := owner.must(200, "GET", "/v1/runs/"+run, nil)
	if r["run"].(map[string]any)["status"] != "completed" {
		t.Errorf("run: %v", r["run"])
	}
	if !strings.Contains(toJSON(owner.must(200, "GET", "/v1/audit", nil)), `"via":"handoff"`) {
		t.Error("audit lacks the hand-off decision")
	}
}

const waPayFlow = `{"schema":"wd/v1","id":"wf_pay","version":1,"name":"Pay supplier","trigger":{"type":"manual"},
  "inputs":{"schema":{"type":"object","required":["amount","account_number","currency"],"properties":{
    "amount":{"type":"integer","minimum":1,"title":"Amount"},"account_number":{"type":"string","pattern":"^[0-9]{10}$","x-pii":"account_number"},
    "currency":{"type":"string","enum":["NGN","USD"]}}}},
  "steps":[{"id":"done","type":"transform","config":{"output":{"amount":"=trigger.body.amount"}}}]}`

func TestWhatsAppTriggerAndStatus(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on = "+2348010000009"
	w.bind(t, owner, on)
	out := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Pay supplier", "definition": json.RawMessage(waPayFlow)})
	wf := out["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)

	if r := w.say(t, on, "run payroll"); !strings.Contains(r, "No workflow") {
		t.Errorf("no match: %q", r)
	}
	if r := w.say(t, on, "run pay"); !strings.Contains(r, "needs 3 input") || !strings.Contains(r, "Enter Amount") {
		t.Fatalf("start: %q", r)
	}
	if r := w.say(t, on, "lots"); !strings.Contains(r, "does not fit") {
		t.Errorf("bad amount: %q", r)
	}
	if r := w.say(t, on, "5000"); !strings.Contains(r, "account_number") {
		t.Errorf("next field: %q", r)
	}
	if r := w.say(t, on, "123"); !strings.Contains(r, "does not fit") {
		t.Errorf("bad account: %q", r)
	}
	w.say(t, on, "0123456789")
	confirm := w.send(t, on, whatsapptest.Text("ngn"))
	if len(confirm) != 1 || len(confirm[0].Buttons) != 2 {
		t.Fatalf("confirmation: %+v", confirm)
	}
	ct := confirm[0].Text
	if !strings.Contains(ct, "Start Pay supplier (version 1) in prod with:") || !strings.Contains(ct, "amount: 5000") || !strings.Contains(ct, "account_number: ••••6789") ||
		!strings.Contains(ct, "currency: NGN") || strings.Contains(ct, "0123456789") {
		t.Errorf("confirmation text: %q", ct)
	}
	// The account number waits sealed.
	var raw string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT data::text FROM chat_sessions`).Scan(&raw); err != nil || strings.Contains(raw, "0123456789") || raw == "" {
		t.Errorf("session data: %v %s", err, raw)
	}
	if runs := owner.must(200, "GET", "/v1/runs", nil)["runs"].([]any); len(runs) != 0 {
		t.Fatal("started before confirmation")
	}
	got := w.send(t, on, whatsapptest.ButtonReply("yes", "Yes, run it"))
	if len(got) != 1 || !strings.Contains(got[0].Text, "Started Pay supplier") {
		t.Fatalf("started: %+v", got)
	}
	runs := owner.must(200, "GET", "/v1/runs", nil)["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("runs: %v", runs)
	}
	id := runs[0].(map[string]any)["id"].(string)
	if !strings.Contains(toJSON(owner.must(200, "GET", "/v1/runs/"+id+"?reveal=true", nil)), "0123456789") {
		t.Error("the run's input lacks the account number")
	}
	// A "yes" now starts nothing more.
	if r := w.say(t, on, "yes"); !strings.Contains(r, "did not understand") {
		t.Errorf("second yes: %q", r)
	}
	w.env.Drain(t)
	w.tick(t)
	if last := w.graph.Last(t, on); !strings.Contains(last.Text, "Pay supplier completed in prod") || last.Type != "text" {
		t.Errorf("outcome: %+v", last)
	}

	// Cancel drops a pending command.
	w.say(t, on, "run pay supplier")
	if r := w.say(t, on, "cancel"); !strings.Contains(r, "Cancelled") {
		t.Errorf("cancel: %q", r)
	}

	// Status: counts and failures, nothing personal.
	r := w.say(t, on, "status")
	if !strings.Contains(r, "Last 24 hours: 1 run (1 completed)") || strings.Contains(r, "0123456789") {
		t.Errorf("status: %q", r)
	}

	// A viewer cannot trigger.
	v := w.member(t, owner, "vic@acme.test", "viewer")
	w.bind(t, v, "+2348010000005")
	if r := w.say(t, "+2348010000005", "run pay"); !strings.Contains(r, "run.start") {
		t.Errorf("viewer: %q", r)
	}
	// Nor can an auditor-less member without run.read see status.
	bare := w.member(t, owner, "bare@acme.test", "credit_officer")
	w.bind(t, bare, "+2348010000006")
	if r := w.say(t, "+2348010000006", "status"); !strings.Contains(r, "run.read") {
		t.Errorf("no run.read: %q", r)
	}
}

func TestWhatsAppWindowAndAlerts(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on = "+2348010000009"
	w.bind(t, owner, on)
	tenant := uuid.MustParse(owner.must(200, "GET", "/v1/me", nil)["tenant_id"].(string))
	ownerID := uuid.MustParse(owner.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)["id"].(string))
	ctx := context.Background()
	detail := map[string]any{"workflow": "payroll", "environment": "prod", "run_id": "r1"}

	// Outside the window: the run_failed template.
	if err := w.wa.Notify(ctx, tenant, []uuid.UUID{ownerID}, "run_failed", "payroll failed in prod", "Run r1 failed. Contact ade@example.com", waPublicURL+"/runs/r1", detail); err != nil {
		t.Fatal(err)
	}
	last := w.graph.Last(t, on)
	if last.Type != "template" || last.Template != "taskiem_run_failed" || last.Text != "[Acme] payroll failed in prod. Details: "+waPublicURL+"/runs/r1" {
		t.Errorf("outside: %+v", last)
	}
	// Inside it: text, redacted.
	w.say(t, on, "hi")
	if err := w.wa.Notify(ctx, tenant, []uuid.UUID{ownerID}, "run_failed", "payroll failed in prod", "Run r1 failed. Contact ade@example.com", "", detail); err != nil {
		t.Fatal(err)
	}
	last = w.graph.Last(t, on)
	if last.Type != "text" || strings.Contains(last.Text, "ade@example.com") || !strings.HasPrefix(last.Text, "[Acme] payroll failed") {
		t.Errorf("inside: %+v", last)
	}
	// If Meta says the window has closed after all, the template goes.
	w.graph.SetFail(131047)
	if err := w.wa.Notify(ctx, tenant, []uuid.UUID{ownerID}, "needs_reconciliation", "x", "y", "", detail); err != nil {
		t.Fatal(err)
	}
	w.graph.SetFail(0)
	if last = w.graph.Last(t, on); last.Template != "taskiem_needs_reconciliation" {
		t.Errorf("fallback: %+v", last)
	}
	// Members of another tenant are not reached through this one.
	other := w.tenant(t, "Other", "owner@other.test")
	w.bind(t, other, "+2348010000008")
	otherID := uuid.MustParse(other.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)["id"].(string))
	if err := w.wa.Notify(ctx, tenant, []uuid.UUID{otherID}, "run_failed", "t", "b", "", detail); err == nil {
		t.Error("reached another tenant's member")
	}

	// The alert channel: members by email.
	owner.must(400, "POST", "/v1/alerts/channels", map[string]any{"kind": "whatsapp", "name": "ops", "to": []string{"nobody@acme.test"}})
	ch := owner.must(201, "POST", "/v1/alerts/channels", map[string]any{"kind": "whatsapp", "name": "ops", "to": []string{"OWNER@acme.test"}})
	if !strings.Contains(toJSON(ch["config"]), ownerID.String()) {
		t.Errorf("channel: %v", ch)
	}
}

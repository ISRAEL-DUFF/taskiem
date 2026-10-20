package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/webauthn"
)

var agentResetLink = regexp.MustCompile(`/reset-password#token=([A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)`)

// agentDo sends a request as the agent, in a session when one is named.
func agentDo(t *testing.T, c *client, session, method, path string, body any) (int, map[string]any) {
	t.Helper()
	if session == "" {
		return c.do(method, path, body)
	}
	return c.do(method, path, body, "X-Taskiem-Agent-Session", session)
}

func agentRaw(t *testing.T, c *client, path string, body []byte) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "image/jpeg")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestFieldAgents(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.WebAuthn = webauthn.Config{RPID: rpID, RPName: "Taskiem", Origins: []string{origin}}
	w.srv.Ops = &api.OpsSettings{}
	w.srv.PublicHosts = []string{"shops.taskiem.test"}
	mail := mailWorld(t, w.world)
	store := fakeStorage(t)
	w.srv.Media = &media.Service{
		Credentials: func(context.Context, uuid.UUID) (map[string]string, error) {
			return map[string]string{"token": "xb_tok", "project_id": "prj_1", "base_url": store.URL, "api_url": store.URL}, nil
		},
		Guard: &egress.Guard{Blocked: func(netip.Addr) bool { return false }},
	}
	op, a := enrolOperator(t, w.world, "ops@taskiem.test")

	// Registering an agent: checked in person, and confirmed with a passkey.
	reg := map[string]any{"email": "tunde@agents.test", "name": "Tunde Bello", "phone": "0802 000 1111", "region": "Lagos: Balogun market",
		"checks": map[string]any{"id_type": "nin", "id_checked": true, "referee_name": "Mrs Ade", "referee_phone": "08030000000"}}
	op.must(403, "POST", "/v1/ops/agents", reg)
	unchecked := map[string]any{}
	for k, v := range reg {
		unchecked[k] = v
	}
	unchecked["checks"] = map[string]any{"id_type": "nin"}
	op.must(422, "POST", "/v1/ops/agents", unchecked)
	reg["step_up"] = op.stepUp(a, "ops.agent.add", "tunde@agents.test")
	added := op.must(201, "POST", "/v1/ops/agents", reg)
	ag := added["agent"].(map[string]any)
	code, agentID := ag["code"].(string), ag["id"].(string)
	if !regexp.MustCompile(`^AG-[A-Z2-9]{5}$`).MatchString(code) || added["setup_email_sent"] != true || ag["phone"] != "+2348020001111" {
		t.Fatalf("registered: %v", added)
	}
	w.srv.WaitBackground()
	var welcome string
	for _, m := range mail.all() {
		if strings.Contains(m, "Welcome, Taskiem field agent") {
			welcome = m
		}
	}
	tok := agentResetLink.FindStringSubmatch(welcome)
	if tok == nil || !strings.Contains(welcome, code) {
		t.Fatalf("welcome: %q", welcome)
	}
	anon := &client{t: t, base: w.base}
	anon.must(200, "POST", "/v1/auth/password/reset", map[string]any{"token": tok[1], "password": testPassword})
	agent := w.login(t, "tunde@agents.test", testPassword)
	if me := agent.must(200, "GET", "/v1/agent/me", nil)["agent"].(map[string]any); me["code"] != code || me["status"] != "active" {
		t.Fatalf("me: %v", me)
	}
	// Anyone else is not an agent.
	acme := w.tenant(t, "Acme", "owner@acme.test")
	acme.must(403, "GET", "/v1/agent/me", nil)
	acme.must(403, "POST", "/v1/agent/businesses", map[string]any{"name": "x", "owner_name": "y", "owner_phone": "08031234567"})

	// Signing a business up: made at once, waiting for the owner.
	agent.must(400, "POST", "/v1/agent/businesses", map[string]any{"name": "Mama Bisi Foods", "owner_name": "Bisi", "owner_phone": "12"})
	ob := agent.must(201, "POST", "/v1/agent/businesses", map[string]any{"name": "Mama Bisi Foods", "owner_name": "Bisi Ade", "owner_phone": "0803 123 4567"})
	sess, consent := ob["session"].(map[string]any), ob["consent"].(map[string]any)
	sid, business := sess["id"].(string), sess["tenant_id"].(string)
	if sess["status"] != "pending" || !strings.HasPrefix(consent["link"].(string), "https://wa.me/15550001111?text=AGREE+") || consent["text"] != "AGREE "+consent["code"].(string) ||
		!strings.HasPrefix(consent["qr"].(string), "data:image/png;base64,") {
		t.Fatalf("onboarding: %v", ob)
	}
	// Until the owner agrees, the agent cannot act in it.
	if st, out := agentDo(t, agent, sid, "GET", "/v1/storefront", nil); st != 403 || out["agent_session_ended"] != true {
		t.Fatalf("pending: %d %v", st, out)
	}
	// Someone else's number cannot agree; a wrong code does not.
	const owner = "+2348031234567"
	if reply := w.say(t, "+2348099999999", "AGREE "+consent["code"].(string)); !strings.Contains(reply, "No business is waiting") {
		t.Fatalf("stranger: %q", reply)
	}
	if reply := w.say(t, owner, "agree 000000"); !strings.Contains(reply, "not right") {
		t.Fatalf("wrong code: %q", reply)
	}
	// A new code replaces the first.
	consent = agent.must(200, "POST", "/v1/agent/sessions/"+sid+"/code", nil)["consent"].(map[string]any)
	if reply := w.say(t, owner, "  Agree   "+consent["code"].(string)); !strings.Contains(reply, "Tunde Bello ("+code+")") || !strings.Contains(reply, "Mama Bisi Foods") {
		t.Fatalf("consent: %q", reply)
	}
	if got := agent.must(200, "GET", "/v1/agent/sessions/"+sid, nil)["session"].(map[string]any); got["status"] != "active" {
		t.Fatalf("after consent: %v", got)
	}
	agent.must(409, "POST", "/v1/agent/sessions/"+sid+"/code", nil)

	// In the session: the page and its tables, nothing else.
	in := func(want int, method, path string, body any) map[string]any {
		t.Helper()
		st, out := agentDo(t, agent, sid, method, path, body)
		if st != want {
			t.Fatalf("as the agent, %s %s: %d, want %d: %v", method, path, st, want, out)
		}
		return out
	}
	in(404, "GET", "/v1/storefront", nil)
	for _, p := range []string{"/v1/storefront/orders", "/v1/storefront/bookings", "/v1/members", "/v1/audit", "/v1/connections", "/v1/me", "/v1/runs", "/v1/tables/project"} {
		in(403, "GET", p, nil)
	}
	in(403, "POST", "/v1/storefront/orders/SO-1/refund", nil)
	in(200, "GET", "/v1/tables", nil)
	card := map[string]any{"name": "Mama Bisi Foods", "tagline": "Amala and ewedu", "phone": "0803 123 4567", "whatsapp": "08031234567", "template": "bold", "accent": "orange"}
	in(200, "PUT", "/v1/storefront", map[string]any{"slug": "mama-bisi", "draft": card})

	// The visit: a photo and the place.
	if st, out := agentRaw(t, agent, "/v1/agent/sessions/"+sid+"/attest?lat=6.4541&lng=3.3947&note=Stall+14", photo(t)); st != 201 {
		t.Fatalf("attest: %d %v", st, out)
	}
	if st, _ := agentRaw(t, agent, "/v1/agent/sessions/"+sid+"/attest?lat=0&lng=0", photo(t)); st != 400 {
		t.Fatalf("attest at 0,0: %d", st)
	}

	// Publishing hands the business over: the session ends, the owner hears.
	pub := in(200, "POST", "/v1/storefront/publish", nil)
	if pub["preview_url"] != "https://mama-bisi.shops.taskiem.test" {
		t.Fatalf("published: %v", pub)
	}
	if last := w.graph.Last(t, owner); !strings.Contains(last.Text, "https://mama-bisi.shops.taskiem.test") || !strings.Contains(last.Text, "MY PAGE") {
		t.Fatalf("owner told: %+v", last)
	}
	in(403, "GET", "/v1/storefront", nil)
	if got := agent.must(200, "GET", "/v1/agent/sessions", nil)["sessions"].([]any)[0].(map[string]any); got["status"] != "ended" || got["end_reason"] != "published" {
		t.Fatalf("sessions: %v", got)
	}

	// The business records its agent, the owner's number and the visit;
	// what the agent did is in its chain under the agent's code.
	var agentOf *uuid.UUID
	var levels []string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT agent_id, ARRAY(SELECT level FROM tenant_verifications v WHERE v.tenant_id = t.id ORDER BY level)
		FROM tenants t WHERE id = $1`, business).Scan(&agentOf, &levels); err != nil {
		t.Fatal(err)
	}
	if agentOf == nil || agentOf.String() != agentID || strings.Join(levels, ",") != "agent_onboarded,phone" {
		t.Fatalf("business: %v %v", agentOf, levels)
	}
	var actions []string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT array_agg(action ORDER BY chain_seq) FROM audit_log WHERE tenant_id = $1 AND actor_id = $2`, business, "agent:"+code).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tenant.create", "storefront.save", "tenant.verified", "storefront.publish", "agent.session_end"} {
		if !strings.Contains(strings.Join(actions, " "), want) {
			t.Errorf("no %s by the agent in %v", want, actions)
		}
	}
	// The owner has an account, linked to their number, and owns it.
	if reply := w.say(t, owner, "status"); strings.Contains(reply, "not linked") {
		t.Fatalf("the owner's number: %q", reply)
	}

	// A support visit: an owner invites the agent back for a few hours.
	acme.must(400, "POST", "/v1/agent-visits", map[string]any{"agent_code": "AG-ZZZZZ"})
	acme.must(400, "POST", "/v1/agent-visits", map[string]any{"agent_code": code, "hours": 9})
	visit := acme.must(201, "POST", "/v1/agent-visits", map[string]any{"agent_code": strings.ToLower(code), "hours": 2})["visit"].(map[string]any)
	vid := visit["id"].(string)
	if visit["status"] != "active" || visit["kind"] != "support" || visit["agent_name"] != "Tunde Bello" {
		t.Fatalf("visit: %v", visit)
	}
	if st, _ := agentDo(t, agent, vid, "GET", "/v1/storefront", nil); st != 404 {
		t.Fatalf("visiting: %d", st)
	}
	// An approver of Acme cannot invite agents.
	approver := w.member(t, acme, "approver@acme.test", "approver")
	approver.must(403, "POST", "/v1/agent-visits", map[string]any{"agent_code": code})
	// STOP AGENT on WhatsApp ends it.
	w.bind(t, acme, "+2348055550000")
	if reply := w.say(t, "+2348055550000", "STOP AGENT"); !strings.Contains(reply, "Tunde Bello can no longer work") {
		t.Fatalf("stop: %q", reply)
	}
	if st, _ := agentDo(t, agent, vid, "GET", "/v1/storefront", nil); st != 403 {
		t.Fatalf("after stop: %d", st)
	}
	if reply := w.say(t, "+2348055550000", "stop agent"); !strings.Contains(reply, "No agent is working") {
		t.Fatalf("stop again: %q", reply)
	}
	visits := acme.must(200, "GET", "/v1/agent-visits", nil)["visits"].([]any)
	if len(visits) != 1 || visits[0].(map[string]any)["end_reason"] != "owner_ended" {
		t.Fatalf("visits: %v", visits)
	}
	// Ending from the app, and a session of another business.
	v2 := acme.must(201, "POST", "/v1/agent-visits", map[string]any{"agent_code": code})["visit"].(map[string]any)["id"].(string)
	acme.must(404, "POST", "/v1/agent-visits/"+sid+"/end", nil)
	acme.must(200, "POST", "/v1/agent-visits/"+v2+"/end", nil)
	acme.must(404, "POST", "/v1/agent-visits/"+v2+"/end", nil)

	// Operators see what agents did, and suspend them.
	list := op.must(200, "GET", "/v1/ops/agents", nil)["agents"].([]any)
	if x := list[0].(map[string]any); x["businesses"] != float64(1) || x["published"] != float64(1) || x["verified"] != float64(1) {
		t.Fatalf("ops agents: %v", x)
	}
	biz := op.must(200, "GET", "/v1/ops/agents/"+agentID+"/businesses", nil)["businesses"].([]any)
	if len(biz) != 1 || biz[0].(map[string]any)["name"] != "Mama Bisi Foods" {
		t.Fatalf("businesses: %v", biz)
	}
	v3 := acme.must(201, "POST", "/v1/agent-visits", map[string]any{"agent_code": code})["visit"].(map[string]any)["id"].(string)
	op.must(422, "POST", "/v1/ops/agents/"+agentID+"/suspend", map[string]any{"step_up": op.stepUp(a, "ops.agent.suspend", agentID)})
	op.must(200, "POST", "/v1/ops/agents/"+agentID+"/suspend", map[string]any{"note": "Complaint from a trader", "step_up": op.stepUp(a, "ops.agent.suspend", agentID)})
	agent.must(403, "GET", "/v1/agent/me", nil)
	if st, _ := agentDo(t, agent, v3, "GET", "/v1/storefront", nil); st != 403 {
		t.Fatalf("suspended agent in a visit: %d", st)
	}
	acme.must(400, "POST", "/v1/agent-visits", map[string]any{"agent_code": code})
	op.must(200, "POST", "/v1/ops/agents/"+agentID+"/retire", map[string]any{"note": "Left", "step_up": op.stepUp(a, "ops.agent.retire", agentID)})
	op.must(409, "POST", "/v1/ops/agents/"+agentID+"/reactivate", map[string]any{"step_up": op.stepUp(a, "ops.agent.reactivate", agentID)})
	audit := toJSON(op.must(200, "GET", "/v1/ops/audit", nil))
	for _, x := range []string{"ops.agent.add", "ops.agent.suspend", "ops.agent.retire"} {
		if !strings.Contains(audit, x) {
			t.Errorf("no %s in the platform chain", x)
		}
	}
}

// TestFieldAgentsOnTelegram: with no WhatsApp on the deployment, the owner
// agrees from Telegram by sharing their own number.
func TestFieldAgentsOnTelegram(t *testing.T) {
	ctx := context.Background()
	w := newTGWorld(t)
	w.srv.PublicHosts = []string{"shops.taskiem.test"}
	store := fakeStorage(t)
	w.srv.Media = &media.Service{
		Credentials: func(context.Context, uuid.UUID) (map[string]string, error) {
			return map[string]string{"token": "xb_tok", "project_id": "prj_1", "base_url": store.URL, "api_url": store.URL}, nil
		},
		Guard: &egress.Guard{Blocked: func(netip.Addr) bool { return false }},
	}
	agent := w.tenant(t, "Tunde's workspace", "tunde@agents.test")
	if _, err := w.env.DB.Admin.Exec(ctx, `SELECT taskiem_ops_agent_add(gen_random_uuid(), u.id, m.tenant_id, 'AG-TGTST', 'Tunde Bello', '+2348020001111', 'Lagos', '{}', 'test')
		FROM users u JOIN memberships m ON m.user_id = u.id WHERE u.email = 'tunde@agents.test'`); err != nil {
		t.Fatal(err)
	}
	ob := agent.must(201, "POST", "/v1/agent/businesses", map[string]any{"name": "Mama Bisi Foods", "owner_name": "Bisi Ade", "owner_phone": "0803 123 4567"})
	sid, consent := ob["session"].(map[string]any)["id"].(string), ob["consent"].(map[string]any)
	code := consent["code"].(string)
	if consent["telegram_link"] != "https://t.me/"+tgBot+"?start=agree_"+code || consent["link"] != nil ||
		!strings.HasPrefix(consent["telegram_qr"].(string), "data:image/png;base64,") {
		t.Fatalf("consent: %v", consent)
	}
	const owner = int64(7001)
	contact := func(phone string, of int64) []telegramtest.Call {
		return w.update(t, owner, func(id int64) []byte { return telegramtest.ContactMessage(id, owner, phone, of) })
	}
	// The link asks for the owner's own number; a code is never enough.
	got := w.update(t, owner, func(id int64) []byte { return telegramtest.Start(id, owner, "", "agree_"+code) })
	if len(got) != 1 || !got[0].AskContact || !strings.Contains(got[0].Text, "Share my number") {
		t.Fatalf("asked: %+v", got)
	}
	// Someone else's contact is refused, and the code must be sent again.
	if got := contact("+2348031234567", 9999); len(got) != 1 || !strings.Contains(got[0].Text, "your own number") {
		t.Fatalf("another's contact: %+v", got)
	}
	if got := contact("+2348031234567", owner); len(got) != 1 || !strings.Contains(got[0].Text, "No business is waiting") {
		t.Fatalf("no code waiting: %+v", got)
	}
	// Their own number, but not the one the agent entered.
	if c := w.say(t, owner, "AGREE "+code); !c.AskContact {
		t.Fatalf("again: %+v", c)
	}
	if got := contact("+2348000000000", owner); len(got) != 1 || !strings.Contains(got[0].Text, "No business is waiting") {
		t.Fatalf("another number: %+v", got)
	}
	agentIn := func(want int, method, path string, body any) map[string]any {
		t.Helper()
		st, out := agentDo(t, agent, sid, method, path, body)
		if st != want {
			t.Fatalf("as the agent, %s %s: %d, want %d: %v", method, path, st, want, out)
		}
		return out
	}
	agentIn(403, "GET", "/v1/storefront", nil)
	// The right number, shared as Telegram sends it (no +): agreed.
	w.say(t, owner, "agree "+code)
	if got := contact("2348031234567", owner); len(got) != 1 || !strings.Contains(got[0].Text, "Tunde Bello (AG-TGTST)") {
		t.Fatalf("agreed: %+v", got)
	}
	if user, err := w.tg.UserOf(ctx, owner); err != nil || user == uuid.Nil {
		t.Fatalf("the owner's Telegram is linked: %v %v", user, err)
	}
	agentIn(404, "GET", "/v1/storefront", nil)
	agentIn(200, "PUT", "/v1/storefront", map[string]any{"slug": "mama-bisi", "draft": map[string]any{"name": "Mama Bisi Foods", "phone": "0803 123 4567",
		"template": "bold", "accent": "orange"}})
	if st, out := agentRaw(t, agent, "/v1/agent/sessions/"+sid+"/attest?lat=6.4541&lng=3.3947", photo(t)); st != 201 {
		t.Fatalf("attest: %d %v", st, out)
	}
	// Published (the business is agent onboarded, so it goes live): the
	// owner hears on Telegram.
	before := len(w.bot.Sent(owner))
	if pub := agentIn(200, "POST", "/v1/storefront/publish", nil); pub["status"] != "published" {
		t.Fatalf("published: %v", pub)
	}
	sent := w.bot.Sent(owner)[before:]
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "https://mama-bisi.shops.taskiem.test") {
		t.Fatalf("the owner told: %+v", sent)
	}
	if c := w.say(t, owner, "STOP AGENT"); !strings.Contains(c.Text, "No agent is working") {
		t.Fatalf("stop: %+v", c)
	}
}

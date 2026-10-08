package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// WhatsApp milestone A2: Flows, the PIN, own numbers, template costs and
// public menus.

// flows turns WhatsApp Flows on (the endpoint's key pair).
func (w *waWorld) flows() { w.wa.Config.FlowKey = whatsapp.NewFlowKey(waFlowKey()) }

// flowCall posts a request to the Flows endpoint at path, encrypted as the
// WhatsApp client does and signed with secret; it returns the status and,
// on 200, the decrypted answer.
func (w *waWorld) flowCall(t *testing.T, path, secret, action, screen, token string, data map[string]any) (int, map[string]any) {
	t.Helper()
	body, open := whatsapptest.FlowRequest(t, &waFlowKey().PublicKey, action, screen, token, data)
	st, raw := w.post(t, path, body, whatsapp.Sign(secret, body))
	if st != 200 {
		return st, nil
	}
	return st, open(raw)
}

func (w *waWorld) post(t *testing.T, path string, body []byte, sig string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest("POST", strings.TrimSuffix(w.hook, "/channels/whatsapp")+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// sendTo delivers a message from a number to a business number through a
// webhook path, signed with secret, and returns what was sent back.
func (w *waWorld) sendTo(t *testing.T, path, pnid, secret, from string, msg map[string]any) []whatsapptest.Sent {
	t.Helper()
	before := len(w.graph.Sent(from))
	body := whatsapptest.Delivery(pnid, from, fmt.Sprintf("wamid.in%d", w.seq.Add(1)), msg)
	if st, raw := w.post(t, path, body, whatsapp.Sign(secret, body)); st != 200 {
		t.Fatalf("delivery to %s: %d %s", path, st, raw)
	}
	return w.graph.Sent(from)[before:]
}

func screenData(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	d, _ := resp["data"].(map[string]any)
	if d == nil {
		t.Fatalf("no data: %v", resp)
	}
	return d
}

func flowToken(t *testing.T, resp map[string]any) string {
	t.Helper()
	if resp["screen"] != "SUCCESS" {
		t.Fatalf("not completed: %v", resp)
	}
	return resp["data"].(map[string]any)["extension_message_response"].(map[string]any)["params"].(map[string]any)["flow_token"].(string)
}

func userID(t *testing.T, c *client) uuid.UUID {
	t.Helper()
	return uuid.MustParse(c.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)["id"].(string))
}

func tenantID(t *testing.T, c *client) uuid.UUID {
	t.Helper()
	return uuid.MustParse(c.must(200, "GET", "/v1/me", nil)["tenant_id"].(string))
}

func TestWhatsAppFlowInputs(t *testing.T) {
	w := newWAWorld(t)
	w.flows()
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on = "+2348010000009"
	w.bind(t, owner, on)
	out := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Pay supplier", "definition": json.RawMessage(waPayFlow)})
	wf := out["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)

	// The inputs come as a form, generated from the schema.
	got := w.send(t, on, whatsapptest.Text("run pay"))
	if len(got) != 1 || got[0].Flow != whatsapp.FlowInputs || got[0].FlowScreen != "INPUTS" || got[0].FlowAction != "navigate" || !strings.HasPrefix(got[0].Text, "[Acme] Pay supplier needs 3 input(s)") {
		t.Fatalf("form: %+v", got)
	}
	f := got[0]
	if f.FlowData["n1_on"] != true || f.FlowData["n1_label"] != "Amount" || f.FlowData["t2_on"] != true || f.FlowData["c3_on"] != true || f.FlowData["t1_on"] != false {
		t.Errorf("form data: %v", f.FlowData)
	}
	const ep = "/channels/whatsapp/flows"
	// Opening asks the endpoint for the screen.
	st, resp := w.flowCall(t, ep, waSecret, "INIT", "", f.FlowToken, nil)
	if st != 200 || resp["screen"] != "INPUTS" || screenData(t, resp)["c3_on"] != true {
		t.Fatalf("init: %d %v", st, resp)
	}
	// A health check, encrypted or not.
	if st, resp := w.flowCall(t, ep, waSecret, "ping", "", "", nil); st != 200 || screenData(t, resp)["status"] != "active" {
		t.Errorf("ping: %d %v", st, resp)
	}
	ping := []byte(`{"version":"3.0","action":"ping"}`)
	if st, raw := w.post(t, ep, ping, whatsapp.Sign(waSecret, ping)); st != 200 || !strings.Contains(string(raw), `"active"`) {
		t.Errorf("plain ping: %d %s", st, raw)
	}
	// Refused: no or a wrong signature (432), what does not decrypt (421),
	// an unknown token (427).
	body, _ := whatsapptest.FlowRequest(t, &waFlowKey().PublicKey, "INIT", "", f.FlowToken, nil)
	if st, _ := w.post(t, ep, body, ""); st != 432 {
		t.Errorf("unsigned: %d", st)
	}
	if st, _ := w.post(t, ep, body, whatsapp.Sign("other-secret", body)); st != 432 {
		t.Errorf("wrong secret: %d", st)
	}
	garbage := []byte(`{"encrypted_flow_data":"AAAA","encrypted_aes_key":"AAAA","initial_vector":"AAAAAAAAAAAAAAAAAAAAAA=="}`)
	if st, _ := w.post(t, ep, garbage, whatsapp.Sign(waSecret, garbage)); st != 421 {
		t.Errorf("garbage: %d", st)
	}
	tenant := tenantID(t, owner)
	forged, _ := whatsapp.NewFlowToken(tenant)
	if st, _ := w.flowCall(t, ep, waSecret, "data_exchange", "INPUTS", forged, map[string]any{"n1": "5"}); st != 427 {
		t.Errorf("unknown token: %d", st)
	}
	if st, _ := w.flowCall(t, ep, waSecret, "data_exchange", "INPUTS", "tk1.nope", nil); st != 427 {
		t.Errorf("bad token: %d", st)
	}
	// Answers are checked against the schema, field by field.
	st, resp = w.flowCall(t, ep, waSecret, "data_exchange", "INPUTS", f.FlowToken, map[string]any{"n1": "0", "t2": "123", "c3": "0"})
	d := screenData(t, resp)
	if st != 200 || resp["screen"] != "INPUTS" || d["has_error"] != true || d["n1_err"] == "" || d["t2_err"] == "" || d["c3_err"] != "" {
		t.Fatalf("bad answers: %d %v", st, resp)
	}
	st, resp = w.flowCall(t, ep, waSecret, "data_exchange", "INPUTS", f.FlowToken, map[string]any{"n1": "5000", "t2": "0123456789", "c3": "0"})
	if st != 200 || flowToken(t, resp) != f.FlowToken {
		t.Fatalf("good answers: %d %v", st, resp)
	}
	// Once submitted, the token is spent.
	if st, _ := w.flowCall(t, ep, waSecret, "data_exchange", "INPUTS", f.FlowToken, map[string]any{"n1": "1"}); st != 427 {
		t.Errorf("resubmitted: %d", st)
	}
	// The account number waits sealed.
	var raw string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT data::text FROM whatsapp_flows`).Scan(&raw); err != nil || strings.Contains(raw, "0123456789") {
		t.Errorf("flow data: %v %s", err, raw)
	}
	// The completed form arrives in the chat: the confirmation, masked.
	got = w.send(t, on, whatsapptest.FlowCompleted(f.FlowToken))
	if len(got) != 1 || len(got[0].Buttons) != 2 || !strings.Contains(got[0].Text, "account_number: ••••6789") || !strings.Contains(got[0].Text, "currency: NGN") {
		t.Fatalf("confirmation: %+v", got)
	}
	if runs := owner.must(200, "GET", "/v1/runs", nil)["runs"].([]any); len(runs) != 0 {
		t.Fatal("started before confirmation")
	}
	if r := w.send(t, on, whatsapptest.ButtonReply("yes", "Yes, run it")); len(r) != 1 || !strings.Contains(r[0].Text, "Started Pay supplier") {
		t.Fatalf("start: %+v", r)
	}
	runs := owner.must(200, "GET", "/v1/runs", nil)["runs"].([]any)
	if len(runs) != 1 || !strings.Contains(toJSON(owner.must(200, "GET", "/v1/runs/"+runs[0].(map[string]any)["id"].(string)+"?reveal=true", nil)), "0123456789") {
		t.Fatalf("run: %v", runs)
	}
	// The same completion again does nothing.
	if r := w.say(t, on, "x"); r == "" {
		t.Fatal("no reply")
	}
	if r := w.send(t, on, whatsapptest.FlowCompleted(f.FlowToken)); len(r) != 1 || !strings.Contains(r[0].Text, "no longer needed") {
		t.Errorf("replayed completion: %+v", r)
	}

	// Writing instead of using the form: chat asks field by field.
	got = w.send(t, on, whatsapptest.Text("run pay"))
	if len(got) != 1 || got[0].Flow == "" {
		t.Fatalf("form: %+v", got)
	}
	if r := w.say(t, on, "5000"); !strings.Contains(r, "answer here instead") || !strings.Contains(r, "Enter Amount") {
		t.Errorf("fallback: %q", r)
	}
	if st, _ := w.flowCall(t, ep, waSecret, "INIT", "", got[0].FlowToken, nil); st != 427 {
		t.Errorf("a dropped form still works: %d", st)
	}
	w.say(t, on, "cancel")

	// Another person's completion of this token is refused.
	other := w.member(t, owner, "vic@acme.test", "operator")
	w.bind(t, other, "+2348010000005")
	got = w.send(t, on, whatsapptest.Text("run pay"))
	if r := w.send(t, "+2348010000005", whatsapptest.FlowCompleted(got[0].FlowToken)); len(r) != 1 || !strings.Contains(r[0].Text, "not valid") {
		t.Errorf("someone else's form: %+v", r)
	}

	// Inputs that do not fit a form stay in chat.
	var props, req []string
	for i := range whatsapp.FlowSlots + 1 {
		props = append(props, fmt.Sprintf(`"f%d":{"type":"string"}`, i))
		req = append(req, fmt.Sprintf(`"f%d"`, i))
	}
	w.say(t, on, "cancel")
	big := `{"schema":"wd/v1","id":"wf_big","version":1,"name":"Big form","trigger":{"type":"manual"},"inputs":{"schema":{"type":"object","required":[` +
		strings.Join(req, ",") + `],"properties":{` + strings.Join(props, ",") + `}}},"steps":[{"id":"done","type":"transform","config":{"output":"=trigger.body"}}]}`
	b := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Big form", "definition": json.RawMessage(big)})
	owner.must(200, "POST", "/v1/workflows/"+b["id"].(string)+"/versions/1/publish", nil)
	if got := w.send(t, on, whatsapptest.Text("run big form")); len(got) != 1 || got[0].Flow != "" || !strings.Contains(got[0].Text, "Enter f0") {
		t.Errorf("big form: %+v", got)
	}
}

const waPinPolicyFlow = `{"schema":"wd/v1","id":"wf_pinpay","version":1,"name":"pin pay","trigger":{"type":"manual"},
  "steps":[{"id":"ok","type":"approval","config":{"policy":"%s","subject":{"amount":"=trigger.body.amount"}}},
           {"id":"done","type":"transform","needs":["ok"],"config":{"output":"=steps.ok.output.decision"}}]}`

func TestWhatsAppPin(t *testing.T) {
	w := newWAWorld(t)
	w.flows()
	owner := w.tenant(t, "Acme", "owner@acme.test")
	alice := w.member(t, owner, "alice@acme.test", "approver", "credit_officer")
	const an = "+2348010000001"

	// Setting a PIN needs a bound number and a passkey or authenticator.
	alice.must(409, "PUT", "/v1/me/whatsapp/pin", map[string]any{"pin": "482915", "password": testPassword})
	w.bind(t, alice, an)
	w.say(t, an, "hi")
	alice.must(409, "PUT", "/v1/me/whatsapp/pin", map[string]any{"pin": "482915", "password": testPassword})
	secret := enrol(t, alice)
	if st, body := alice.do("PUT", "/v1/me/whatsapp/pin", map[string]any{"pin": "482915"}); st != 403 || body["reauth"] == nil {
		t.Errorf("without proof: %d %v", st, body)
	}
	alice.must(400, "PUT", "/v1/me/whatsapp/pin", map[string]any{"pin": "123456", "totp": codeAfter(secret, 1)})
	alice.must(400, "PUT", "/v1/me/whatsapp/pin", map[string]any{"pin": "1234", "totp": codeAfter(secret, 1)})
	if got := alice.must(200, "PUT", "/v1/me/whatsapp/pin", map[string]any{"pin": "482915", "totp": codeAfter(secret, 1)}); got["set"] != true {
		t.Fatalf("set: %v", got)
	}
	if p := alice.must(200, "GET", "/v1/me/whatsapp", nil)["pin"].(map[string]any); p["set"] != true {
		t.Errorf("binding view: %v", p)
	}
	var hash string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT pin_hash FROM whatsapp_pins`).Scan(&hash); err != nil || !strings.HasPrefix(hash, "$argon2id$") || strings.Contains(hash, "482915") {
		t.Errorf("stored: %v %q", err, hash)
	}

	owner.must(201, "PUT", "/v1/policies/pin_ok", map[string]any{"document": json.RawMessage(
		`{"rules":[{"levels":[{"role":"credit_officer"}],"step_up":"whatsapp_pin"}]}`)})
	owner.must(201, "PUT", "/v1/policies/strict", map[string]any{"document": json.RawMessage(
		`{"rules":[{"levels":[{"role":"credit_officer"}],"step_up":"totp"}]}`)})
	pinWF := publishFlow(t, owner, fmt.Sprintf(waPinPolicyFlow, "pin_ok"))
	strictWF := publishFlow(t, owner, strings.Replace(fmt.Sprintf(waPinPolicyFlow, "strict"), "wf_pinpay", "wf_strict", 1))
	start := func(wf string) string {
		return owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"amount": 10}})["run_id"].(string)
	}
	const ep = "/channels/whatsapp/flows"
	tapApprove := func() whatsapptest.Sent {
		t.Helper()
		w.tick(t)
		am := w.graph.Last(t, an)
		got := w.send(t, an, whatsapptest.ButtonReply(am.Buttons[0].ID, "Approve"))
		if len(got) != 1 {
			t.Fatalf("tap: %+v", got)
		}
		return got[0]
	}

	// A policy allowing the PIN: a PIN form bound to the decision.
	run := start(pinWF)
	f := tapApprove()
	if f.Flow != whatsapp.FlowPin || f.FlowScreen != "PIN" || f.FlowData["title"] != "Approve ok" {
		t.Fatalf("PIN form: %+v", f)
	}
	st, resp := w.flowCall(t, ep, waSecret, "data_exchange", "PIN", f.FlowToken, map[string]any{"pin": "000000"})
	if st != 200 || !strings.Contains(fmt.Sprint(screenData(t, resp)["error"]), "4 tries left") {
		t.Fatalf("wrong PIN: %d %v", st, resp)
	}
	st, resp = w.flowCall(t, ep, waSecret, "data_exchange", "PIN", f.FlowToken, map[string]any{"pin": "482915"})
	if st != 200 || flowToken(t, resp) != f.FlowToken {
		t.Fatalf("right PIN: %d %v", st, resp)
	}
	if st, _ := w.flowCall(t, ep, waSecret, "data_exchange", "PIN", f.FlowToken, map[string]any{"pin": "482915"}); st != 427 {
		t.Errorf("replayed: %d", st)
	}
	if got := w.send(t, an, whatsapptest.FlowCompleted(f.FlowToken)); len(got) != 1 || !strings.Contains(got[0].Text, "Recorded with your PIN: you approved ok") {
		t.Errorf("completion: %+v", got)
	}
	w.env.Drain(t)
	res := owner.must(200, "GET", "/v1/runs/"+run, nil)
	if res["run"].(map[string]any)["status"] != "completed" || !strings.Contains(toJSON(res["events"]), "whatsapp_pin") {
		t.Errorf("run: %v %s", res["run"], toJSON(res["events"]))
	}
	audit := toJSON(owner.must(200, "GET", "/v1/audit", nil))
	for _, a := range []string{`"whatsapp.pin.set"`, `"whatsapp.pin.fail"`, `"via":"flow_pin"`} {
		if !strings.Contains(audit, a) {
			t.Errorf("audit lacks %s", a)
		}
	}

	// A policy needing an authenticator code: never the PIN.
	strictRun := start(strictWF)
	if m := tapApprove(); m.Flow != "" || !strings.Contains(m.Text, "/handoff#tk1.") {
		t.Errorf("strict policy: %+v", m)
	}
	_, err := w.env.Store.VoteApproval(context.Background(), runtime.RunRef{ID: uuid.MustParse(strictRun), TenantID: tenantID(t, owner)}, "ok",
		runtime.Vote{UserID: userID(t, alice), Roles: []string{"credit_officer"}, Decision: "approved", Channel: "whatsapp", StepUp: runtime.StepUpWhatsAppPIN})
	var su *runtime.StepUpError
	if !errors.As(err, &su) || su.Method != "totp" {
		t.Errorf("a PIN on a totp policy: %v", err)
	}

	// Five wrong PINs lock it; then step-up falls back to the web link.
	start(pinWF)
	f = tapApprove()
	for i := range 5 {
		st, resp = w.flowCall(t, ep, waSecret, "data_exchange", "PIN", f.FlowToken, map[string]any{"pin": fmt.Sprintf("00000%d", i)})
		if st != 200 {
			t.Fatalf("wrong %d: %d", i, st)
		}
	}
	if e := fmt.Sprint(screenData(t, resp)["error"]); !strings.Contains(e, "locked") {
		t.Errorf("locked: %v", resp)
	}
	if st, _ := w.flowCall(t, ep, waSecret, "data_exchange", "PIN", f.FlowToken, map[string]any{"pin": "482915"}); st != 427 {
		t.Errorf("a locked form still works: %d", st)
	}
	if p := alice.must(200, "GET", "/v1/me/whatsapp", nil)["pin"].(map[string]any); p["locked_until"] == nil {
		t.Errorf("not shown locked: %v", p)
	}
	start(pinWF)
	if m := tapApprove(); m.Flow != "" || !strings.Contains(m.Text, "/handoff#tk1.") {
		t.Errorf("locked PIN: %+v", m)
	}
	if !strings.Contains(toJSON(owner.must(200, "GET", "/v1/audit", nil)), `"whatsapp.pin.locked"`) {
		t.Error("audit lacks the lock")
	}

	// Removing it; unlinking the number removes it too.
	alice.must(204, "DELETE", "/v1/me/whatsapp/pin", nil)
	alice.must(404, "DELETE", "/v1/me/whatsapp/pin", nil)
	if _, err := w.env.DB.Admin.Exec(context.Background(), `SELECT taskiem_wa_pin_set($1, $2)`, userID(t, alice), hash); err != nil {
		t.Fatal(err)
	}
	alice.must(204, "DELETE", "/v1/me/whatsapp", nil)
	if p := alice.must(200, "GET", "/v1/me/whatsapp", nil)["pin"].(map[string]any); p["set"] != false {
		t.Errorf("after unlinking: %v", p)
	}
}

const (
	pnA, tokA, secA, vtA = "200000000000001", "EAAalpha", "alpha-app-secret-0001", "alpha-verify-token-01"
	pnB, tokB            = "200000000000002", "EAAbeta"
)

func TestWhatsAppOwnNumbers(t *testing.T) {
	w := newWAWorld(t)
	w.flows()
	w.graph.AddNumber(pnA, tokA, "+234 800 000 0001")
	w.graph.AddNumber(pnB, tokB, "+234 800 000 0002")
	alpha := w.tenant(t, "Alpha", "owner@alpha.test")
	beta := w.tenant(t, "Beta", "owner@beta.test")
	gamma := w.tenant(t, "Gamma", "owner@gamma.test")

	connectA := map[string]any{"phone_number_id": pnA, "waba_id": "300000000000001", "access_token": tokA, "app_secret": secA, "verify_token": vtA}
	// The token must reach the number; only secret.manage; one organisation per number.
	bad := map[string]any{"phone_number_id": pnA, "waba_id": "300000000000001", "access_token": "wrong", "app_secret": secA, "verify_token": vtA}
	alpha.must(400, "PUT", "/v1/whatsapp/number", bad)
	viewer := w.member(t, alpha, "viewer@alpha.test", "viewer")
	viewer.must(403, "PUT", "/v1/whatsapp/number", connectA)
	v := alpha.must(200, "PUT", "/v1/whatsapp/number", connectA)
	if v["connected"] != true || v["display_number"] != "+2348000000001" || v["webhook_path"] != "/channels/whatsapp/n/"+pnA || strings.Contains(toJSON(v), tokA) || strings.Contains(toJSON(v), secA) {
		t.Fatalf("connected: %v", v)
	}
	if !strings.Contains(w.graph.FlowsKey(pnA), "PUBLIC KEY") {
		t.Error("the Flows key was not uploaded")
	}
	gamma.must(409, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": "300000000000009", "access_token": tokA, "app_secret": secA, "verify_token": vtA})
	// Beta's number is on Taskiem's app (as after embedded signup).
	beta.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnB, "waba_id": "300000000000002", "access_token": tokB, "own_app": false})
	var raw string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT string_agg(to_jsonb(n)::text, ' ') FROM whatsapp_numbers n`).Scan(&raw); err != nil || strings.Contains(raw, tokA) || strings.Contains(raw, secA) {
		t.Errorf("credentials outside the vault: %v %s", err, raw)
	}
	if !strings.Contains(toJSON(alpha.must(200, "GET", "/v1/audit", nil)), `"whatsapp.number.connect"`) {
		t.Error("connection not audited")
	}

	// Binding codes come from the tenant's number.
	const na, nb, nc = "+2348011110001", "+2348011110002", "+2348011110003"
	if alpha.must(200, "GET", "/v1/me/whatsapp", nil)["platform_number"] != "+2348000000001" {
		t.Error("Alpha's members see the shared number")
	}
	w.bind(t, alpha, na)
	w.bind(t, beta, nb)
	w.bind(t, gamma, nc)
	for n, from := range map[string]string{na: pnA, nb: pnB, nc: waPNID} {
		if s := w.graph.Sent(n)[0]; s.Template != "taskiem_otp" || s.From != from {
			t.Errorf("code to %s from %s, want %s", n, s.From, from)
		}
	}

	// Inbound: Alpha's number on its own app, verified with its secret.
	got := w.sendTo(t, "/channels/whatsapp/n/"+pnA, pnA, secA, na, whatsapptest.Text("status"))
	if len(got) != 1 || got[0].From != pnA || !strings.HasPrefix(got[0].Text, "No runs") {
		t.Fatalf("alpha's number: %+v", got)
	}
	body := whatsapptest.Delivery(pnA, na, "wamid.x1", whatsapptest.Text("status"))
	if st, _ := w.post(t, "/channels/whatsapp/n/"+pnA, body, whatsapp.Sign(waSecret, body)); st != 401 {
		t.Errorf("Alpha's path with Taskiem's secret: %d", st)
	}
	// Alpha's messages are not taken through Taskiem's app.
	before := len(w.graph.Sent(na))
	if st, _ := w.post(t, "/channels/whatsapp", body, whatsapp.Sign(waSecret, body)); st != 200 || len(w.graph.Sent(na)) != before {
		t.Errorf("Alpha's number through the shared path: %d", st)
	}
	// Beta's number through Taskiem's app.
	got = w.sendTo(t, "/channels/whatsapp", pnB, waSecret, nb, whatsapptest.Text("status"))
	if len(got) != 1 || got[0].From != pnB || strings.HasPrefix(got[0].Text, "[") {
		t.Fatalf("beta's number: %+v", got)
	}
	// Someone not in Alpha writing to Alpha's number.
	if got := w.sendTo(t, "/channels/whatsapp/n/"+pnA, pnA, secA, nc, whatsapptest.Text("status")); len(got) != 1 || !strings.Contains(got[0].Text, "not a member") {
		t.Errorf("gamma on alpha's number: %+v", got)
	}
	// The shared number still works, with the prefix.
	if r := w.say(t, nc, "status"); !strings.HasPrefix(r, "[Gamma] ") {
		t.Errorf("shared: %q", r)
	}
	// Handshakes: Alpha's verify token on Alpha's path only.
	hs := func(path, vt string) int {
		resp, err := http.Get(strings.TrimSuffix(w.hook, "/channels/whatsapp") + path + "?hub.mode=subscribe&hub.verify_token=" + vt + "&hub.challenge=4242")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if hs("/channels/whatsapp/n/"+pnA, vtA) != 200 || hs("/channels/whatsapp/n/"+pnA, waVerify) != 403 || hs("/channels/whatsapp/n/"+pnB, waVerify) != 403 {
		t.Error("handshakes")
	}

	// Alerts go from each tenant's number: Alpha's without the prefix (its
	// window is open), Gamma's from the shared number with it.
	ctx := context.Background()
	if err := w.wa.Notify(ctx, tenantID(t, alpha), []uuid.UUID{userID(t, alpha)}, "run_failed", "payroll failed in prod", "x", "", map[string]any{"workflow": "payroll", "environment": "prod"}); err != nil {
		t.Fatal(err)
	}
	if last := w.graph.Last(t, na); last.From != pnA || last.Type != "text" || !strings.HasPrefix(last.Text, "payroll failed") {
		t.Errorf("alpha's alert: %+v", last)
	}
	if err := w.wa.Notify(ctx, tenantID(t, gamma), []uuid.UUID{userID(t, gamma)}, "run_failed", "payroll failed in prod", "x", "", map[string]any{"workflow": "payroll", "environment": "prod"}); err != nil {
		t.Fatal(err)
	}
	if last := w.graph.Last(t, nc); last.From != waPNID || !strings.HasPrefix(last.Text, "[Gamma] ") {
		t.Errorf("gamma's alert: %+v", last)
	}
	// Beta's member wrote to Beta's number: text from it, without the prefix.
	if err := w.wa.Notify(ctx, tenantID(t, beta), []uuid.UUID{userID(t, beta)}, "run_failed", "t", "b", "", map[string]any{"workflow": "w", "environment": "prod"}); err != nil {
		t.Fatal(err)
	}
	if last := w.graph.Last(t, nb); last.From != pnB || last.Type != "text" {
		t.Errorf("beta's alert: %+v", last)
	}

	// Disconnecting: back to the shared number.
	alpha.must(204, "DELETE", "/v1/whatsapp/number", nil)
	if err := w.wa.Notify(ctx, tenantID(t, alpha), []uuid.UUID{userID(t, alpha)}, "run_failed", "t", "b", "", map[string]any{"workflow": "w", "environment": "prod"}); err != nil {
		t.Fatal(err)
	}
	if last := w.graph.Last(t, na); last.From != waPNID {
		t.Errorf("after disconnecting: %+v", last)
	}
	if hs("/channels/whatsapp/n/"+pnA, vtA) != 403 {
		t.Error("a disconnected number's path answers")
	}
	if v := alpha.must(200, "GET", "/v1/whatsapp/number", nil); v["connected"] != false {
		t.Errorf("view: %v", v)
	}
}

func TestWhatsAppTemplateCosts(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	tenant, me := tenantID(t, owner), userID(t, owner)
	ctx := context.Background()
	lim := owner.must(200, "GET", "/v1/limits", nil)
	if lim["limits"].(map[string]any)["whatsapp_templates_monthly"] != float64(runtime.DefaultWhatsAppTemplatesMonthly) {
		t.Errorf("default allowance: %v", lim["limits"])
	}
	if err := w.env.Store.SetLimits(ctx, tenant, map[string]any{"whatsapp_templates_monthly": 2}, "test"); err != nil {
		t.Fatal(err)
	}
	w.env.Store.ForgetLimits(tenant)
	const on = "+2348010000009"
	w.bind(t, owner, on) // the code: an authentication template
	detail := map[string]any{"workflow": "payroll", "environment": "prod"}
	for range 3 { // the window is closed: utility templates
		if err := w.wa.Notify(ctx, tenant, []uuid.UUID{me}, "run_failed", "payroll failed", "b", "", detail); err != nil {
			t.Fatal(err)
		}
	}
	// Beyond the allowance, approvals, codes and alerts still go.
	if n := len(w.graph.Sent(on)); n != 4 {
		t.Fatalf("sent %d", n)
	}
	// Marketing ones are held back.
	promo := whatsapp.Template{Name: "taskiem_promo", Category: "MARKETING", Body: "{{1}}", Vars: []string{"x"}}
	if _, err := w.wa.SendTemplate(ctx, tenant, on, promo, map[string]string{"x": "y"}, nil); !errors.Is(err, whatsapp.ErrTemplateBlocked) {
		t.Errorf("marketing: %v", err)
	}
	// Text inside the window is free and not counted.
	w.say(t, on, "hi")
	if err := w.wa.Notify(ctx, tenant, []uuid.UUID{me}, "run_failed", "payroll failed", "b", "", detail); err != nil {
		t.Fatal(err)
	}
	u := owner.must(200, "GET", "/v1/limits", nil)["usage"].(map[string]any)["whatsapp_templates_this_month"].(map[string]any)
	by := u["by_category"].(map[string]any)
	if u["sent"] != float64(4) || by["authentication"] != float64(1) || by["utility"] != float64(3) || u["overage"] != float64(2) || u["blocked"] != float64(1) {
		t.Errorf("usage: %v", u)
	}
	// Another tenant's counts are its own.
	other := w.tenant(t, "Other", "owner@other.test")
	if u := owner.must(200, "GET", "/v1/limits", nil)["usage"]; u == nil {
		t.Fatal("no usage")
	}
	if u := other.must(200, "GET", "/v1/limits", nil)["usage"].(map[string]any)["whatsapp_templates_this_month"].(map[string]any); u["sent"] != float64(0) {
		t.Errorf("other tenant: %v", u)
	}
}

func TestWhatsAppPublicMenu(t *testing.T) {
	w := newWAWorld(t)
	w.flows()
	w.graph.AddNumber(pnA, tokA, "+234 800 000 0001")
	w.graph.AddNumber(pnB, tokB, "+234 800 000 0002")
	alpha := w.tenant(t, "Alpha", "owner@alpha.test")
	beta := w.tenant(t, "Beta", "owner@beta.test")
	pay := alpha.must(201, "POST", "/v1/workflows", map[string]any{"name": "Pay supplier", "definition": json.RawMessage(waPayFlow)})["id"].(string)
	alpha.must(200, "POST", "/v1/workflows/"+pay+"/versions/1/publish", nil)
	hidden := publishFlow(t, alpha, waLoanFlow)

	// Only with the tenant's own number; workflow.publish.
	menu := map[string]any{"enabled": true, "workflows": []any{map[string]any{"workflow_id": pay, "label": "Pay a supplier"}}}
	alpha.must(409, "PUT", "/v1/whatsapp/public-menu", menu)
	alpha.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": "300000000000001", "access_token": tokA, "own_app": false})
	beta.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnB, "waba_id": "300000000000002", "access_token": tokB, "own_app": false})
	w.member(t, alpha, "viewer@alpha.test", "viewer").must(403, "PUT", "/v1/whatsapp/public-menu", menu)
	if v := alpha.must(200, "PUT", "/v1/whatsapp/public-menu", menu); v["public_menu"] != true || len(v["public_workflows"].([]any)) != 1 {
		t.Fatalf("menu: %v", v)
	}

	const stranger = "+2348099990001"
	say := func(pnid, from, text string) []whatsapptest.Sent {
		return w.sendTo(t, "/channels/whatsapp", pnid, waSecret, from, whatsapptest.Text(text))
	}
	got := say(pnA, stranger, "hello")
	if len(got) != 1 || got[0].From != pnA || !strings.Contains(got[0].Text, "1. Pay a supplier") || strings.Contains(got[0].Text, "loan") {
		t.Fatalf("menu: %+v", got)
	}
	got = say(pnA, stranger, "1")
	if len(got) != 1 || got[0].Flow != whatsapp.FlowInputs || got[0].From != pnA {
		t.Fatalf("form: %+v", got)
	}
	tok := got[0].FlowToken
	// The form's answers through the endpoint of Taskiem's app.
	st, resp := w.flowCall(t, "/channels/whatsapp/flows", waSecret, "data_exchange", "INPUTS", tok, map[string]any{"n1": "700", "t2": "0123456789", "c3": "1"})
	if st != 200 || flowToken(t, resp) != tok {
		t.Fatalf("answers: %d %v", st, resp)
	}
	got = w.sendTo(t, "/channels/whatsapp", pnA, waSecret, stranger, whatsapptest.FlowCompleted(tok))
	if len(got) != 1 || len(got[0].Buttons) != 2 || !strings.Contains(got[0].Text, "••••6789") {
		t.Fatalf("confirmation: %+v", got)
	}
	got = say(pnA, stranger, "yes")
	if len(got) != 1 || !strings.Contains(got[0].Text, "has started") {
		t.Fatalf("start: %+v", got)
	}
	runs := alpha.must(200, "GET", "/v1/runs", nil)["runs"].([]any)
	if len(runs) != 1 || !strings.Contains(toJSON(runs[0]), "whatsapp_public:"+stranger) {
		t.Fatalf("runs: %s", toJSON(runs))
	}
	if !strings.Contains(toJSON(alpha.must(200, "GET", "/v1/audit", nil)), `"whatsapp_public:+234•`) {
		t.Error("the public start is not audited with the number masked")
	}
	// Only the public workflows: "2" is not on the menu.
	if got := say(pnA, stranger, "2"); len(got) != 1 || !strings.Contains(got[0].Text, "Reply with a number") {
		t.Errorf("not on the menu: %+v", got)
	}
	_ = hidden

	// Beta offers no menu: the stranger is told how to link.
	if got := say(pnB, stranger, "hello"); len(got) != 1 || !strings.Contains(got[0].Text, "not linked") || got[0].From != pnB {
		t.Errorf("beta: %+v", got)
	}
	// Alpha's form token is not Beta's.
	got = say(pnA, stranger, "1")
	if len(got) != 1 || got[0].Flow == "" {
		t.Fatalf("second form: %+v", got)
	}
	if got := w.sendTo(t, "/channels/whatsapp", pnB, waSecret, stranger, whatsapptest.FlowCompleted(got[0].FlowToken)); len(got) != 0 && !strings.Contains(got[0].Text, "not linked") {
		t.Errorf("alpha's form completed on beta's number: %+v", got)
	}
	// The shared number offers no menu.
	if got := say(waPNID, "+2348099990003", "1"); len(got) != 1 || !strings.Contains(got[0].Text, "not linked") || got[0].From != waPNID {
		t.Errorf("shared: %+v", got)
	}

	// Strict limits: a flood from one number is mostly dropped.
	const flooder = "+2348099990002"
	n := 0
	for range 12 {
		n += len(say(pnA, flooder, "hi"))
	}
	if n > 6 {
		t.Errorf("%d replies to 12 messages", n)
	}
}

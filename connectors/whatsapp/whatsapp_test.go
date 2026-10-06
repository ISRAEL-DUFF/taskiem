package whatsapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
)

const wamid = "wamid.HBgLMTY0NjcwNDM1OTUVAgARGBI1RjQyNUE3NEYxMzAzMzQ5MkEA"

var creds = map[string]string{"access_token": "EAAtest", "phone_number_id": "106540352242922", "app_secret": "app-secret-1", "verify_token": "vt-taskiem-7"}

func call(t *testing.T, action string, input map[string]any, exchanges ...string) (connector.Response, error) {
	t.Helper()
	return callWith(t, creds, action, input, exchanges...)
}

func callWith(t *testing.T, cr map[string]string, action string, input map[string]any, exchanges ...string) (connector.Response, error) {
	t.Helper()
	var exs []fixture.Exchange
	for _, n := range exchanges {
		exs = append(exs, fixture.Load(t, n))
	}
	srv := fixture.Serve(t, exs...)
	c := New(Options{BaseURL: srv.URL})
	return c.Actions[action].Execute(context.Background(), connector.Request{Input: input, HTTP: srv.Client(), Credentials: cr, Attempt: 1})
}

func out(r connector.Response) map[string]any { return r.Output.(map[string]any) }

func TestRegisters(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, ok := reg.Get("whatsapp@1")
	if !ok {
		t.Fatal("whatsapp@1 not registered")
	}
	if h := c.Manifest.Hosts(); len(h) != 1 || h[0] != "graph.facebook.com" {
		t.Errorf("hosts %v", h)
	}
	if c.Manifest.BaseURL != "https://graph.facebook.com/v25.0" {
		t.Errorf("base %s", c.Manifest.BaseURL)
	}
	for _, a := range []string{"send_text", "send_template", "send_media", "send_interactive"} {
		if c.Manifest.Actions[a].Class != effects.UnsafeWrite {
			t.Errorf("%s is %s", a, c.Manifest.Actions[a].Class)
		}
	}
}

func TestPhoneNumberAndAuth(t *testing.T) {
	r, err := call(t, "get_phone_number", nil, "get_phone_number")
	if err != nil || out(r)["verified_name"] != "Acme Payroll" || out(r)["quality_rating"] != "GREEN" {
		t.Errorf("%v %v", r.Output, err)
	}
	_, err = call(t, "get_phone_number", nil, "get_phone_number_expired")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "code 190") || !strings.Contains(err.Error(), "fbtrace_id A1b2C3d4") {
		t.Errorf("expired token: %v", err)
	}
	if _, err := callWith(t, map[string]string{"phone_number_id": "1"}, "get_phone_number", nil); effects.Classify(err) != effects.KindFatal {
		t.Errorf("no token: %v", err)
	}
	if _, err := callWith(t, map[string]string{"access_token": "x"}, "send_text", map[string]any{"to": "+1", "body": "x"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("no phone number id: %v", err)
	}
}

func TestSends(t *testing.T) {
	r, err := call(t, "send_text", map[string]any{"to": "+2348012345678", "body": "Your payslip is ready: https://acme.example/p/17", "preview_url": true,
		"reply_to": "wamid.IN1", "biz_opaque_callback_data": "run-17"}, "send_text")
	if err != nil || out(r)["message_id"] != wamid || out(r)["wa_id"] != "2348012345678" || out(r)["input"] != "+2348012345678" {
		t.Errorf("text %v %v", r.Output, err)
	}
	comps := []any{map[string]any{"type": "body", "parameters": []any{
		map[string]any{"type": "text", "parameter_name": "first_name", "text": "Ada"},
		map[string]any{"type": "text", "parameter_name": "month", "text": "October"},
	}}}
	r, err = call(t, "send_template", map[string]any{"to": "+2348012345678", "name": "payslip_ready", "language": "en_US", "components": comps}, "send_template")
	if err != nil || out(r)["message_status"] != "accepted" {
		t.Errorf("template %v %v", r.Output, err)
	}
	r, err = call(t, "send_media", map[string]any{"to": "+2348012345678", "media_type": "document", "link": "https://acme.example/p/17.pdf",
		"caption": "October payslip", "filename": "payslip-october.pdf"}, "send_media_document")
	if err != nil || out(r)["message_id"] != wamid {
		t.Errorf("document %v %v", r.Output, err)
	}
	if _, err := call(t, "send_media", map[string]any{"to": "+2348012345678", "media_type": "image", "media_id": "1013859600285441"}, "send_media_image"); err != nil {
		t.Errorf("image by id: %v", err)
	}
	ia := map[string]any{"type": "button", "body": map[string]any{"text": "Approve the October payroll?"}, "action": map[string]any{"buttons": []any{
		map[string]any{"type": "reply", "reply": map[string]any{"id": "approve:run-17", "title": "Approve"}},
		map[string]any{"type": "reply", "reply": map[string]any{"id": "reject:run-17", "title": "Reject"}},
	}}}
	if _, err := call(t, "send_interactive", map[string]any{"to": "+2348012345678", "interactive": ia}, "send_interactive"); err != nil {
		t.Errorf("interactive: %v", err)
	}
	r, err = call(t, "mark_as_read", map[string]any{"message_id": "wamid.HBgLMTY1MDM4Nzk0MzkVAgARGBJDQjZCMzlEQUE4OTJBMTE4RTUA"}, "mark_as_read")
	if err != nil || out(r)["success"] != true {
		t.Errorf("read %v %v", r.Output, err)
	}
	_, err = call(t, "mark_as_read", map[string]any{"message_id": "wamid.bogus"}, "mark_as_read_invalid")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "131009") {
		t.Errorf("invalid wamid: %v", err)
	}
}

func TestInputsCheckedBeforeSending(t *testing.T) {
	for action, in := range map[string]map[string]any{
		"send_text":        {"to": "+1"},
		"send_template":    {"to": "+1", "name": "x"},
		"send_interactive": {"to": "+1", "interactive": map[string]any{"type": "button"}},
		"mark_as_read":     {},
	} {
		if _, err := call(t, action, in); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%s %v: %v", action, in, err)
		}
	}
	for _, in := range []map[string]any{
		{"to": "+1", "media_type": "gif", "link": "https://x"},
		{"to": "+1", "media_type": "image"},
		{"to": "+1", "media_type": "image", "link": "https://x", "media_id": "1"},
		{"to": "+1", "media_type": "audio", "link": "https://x", "caption": "no"},
		{"to": "+1", "media_type": "image", "link": "https://x", "filename": "a.png"},
		{"media_type": "image", "link": "https://x"},
	} {
		if _, err := call(t, "send_media", in); effects.Classify(err) != effects.KindFatal {
			t.Errorf("send_media %v: %v", in, err)
		}
	}
}

func TestErrorClassification(t *testing.T) {
	in := map[string]any{"to": "+2348012345678", "body": "hi"}
	// Limits and temporary conditions are refusals: nothing was sent, so a
	// send may go again.
	for _, f := range []string{"error_throughput", "error_pair_rate", "error_app_rate", "error_temporary"} {
		_, err := call(t, "send_text", in, f)
		if !errors.Is(err, effects.ErrRetryable) || effects.AfterError(effects.UnsafeWrite, effects.Classify(err)) != effects.Retry {
			t.Errorf("%s: %v (%v)", f, err, effects.Classify(err))
		}
	}
	for f, want := range map[string]string{"error_window_closed": "131047", "error_permission": "code 10", "error_template_missing": "132001"} {
		_, err := call(t, "send_text", in, f)
		if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", f, err)
		}
	}
	// Meta may have accepted the message: a send parks.
	for _, f := range []string{"error_unknown", "error_server_plain", "accepted_without_id"} {
		_, err := call(t, "send_text", in, f)
		if effects.Classify(err) != effects.KindUnknownOutcome || effects.AfterError(effects.UnsafeWrite, effects.Classify(err)) != effects.Park {
			t.Errorf("%s: %v", f, err)
		}
	}
}

func sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestWebhookSignature(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["messages"].Verify
	body := []byte(`{"object":"whatsapp_business_account","entry":[{"id":"102290129340398","changes":[{"value":{"messaging_product":"whatsapp"},"field":"messages"}]}]}`)
	h := http.Header{}
	h.Set("X-Hub-Signature-256", sign("app-secret-1", body))
	if err := connector.VerifyWebhook(spec, "app-secret-1", h, body); err != nil {
		t.Errorf("valid: %v", err)
	}
	if connector.VerifyWebhook(spec, "another-secret", h, body) == nil {
		t.Error("wrong app secret accepted")
	}
	if connector.VerifyWebhook(spec, "app-secret-1", h, append(body, ' ')) == nil {
		t.Error("tampered body accepted")
	}
	if connector.VerifyWebhook(spec, "app-secret-1", http.Header{}, body) == nil {
		t.Error("unsigned delivery accepted")
	}
}

func TestVerificationHandshake(t *testing.T) {
	// Meta's endpoint check: GET ?hub.mode=subscribe&hub.verify_token=...&hub.challenge=...
	hs := New(Options{}).Manifest.Triggers["messages"].Handshake
	if hs == nil || hs.Method != "GET" || hs.TokenQuery != "hub.verify_token" || hs.SecretField != "verify_token" {
		t.Fatalf("handshake %+v", hs)
	}
	e := expr.MustNewWithRoots("body", "headers", "query")
	act := map[string]any{"body": nil, "headers": map[string]any{},
		"query": map[string]any{"hub.mode": "subscribe", "hub.verify_token": "vt-taskiem-7", "hub.challenge": "1158201444"}}
	v, err := e.Eval(hs.Respond, act)
	if err != nil || v != "1158201444" {
		t.Errorf("respond %v %v", v, err)
	}
	if creds[hs.SecretField] == "" {
		t.Error("the handshake's secret is not a connection field")
	}
}

func TestTriggerExpressions(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["messages"]
	e := expr.MustNewWithRoots("body", "headers", "query")
	wrap := func(field, value string) string {
		return `{"object":"whatsapp_business_account","entry":[{"id":"102290129340398","changes":[{"value":` + value + `,"field":"` + field + `"}]}]}`
	}
	meta := `"messaging_product":"whatsapp","metadata":{"display_phone_number":"15550783881","phone_number_id":"106540352242922"}`
	for _, c := range []struct{ name, body, event, dedup, correlation string }{
		{"text", wrap("messages", `{`+meta+`,"contacts":[{"profile":{"name":"Sheena Nelson"},"wa_id":"16505551234"}],"messages":[{"from":"16505551234","id":"wamid.IN1","timestamp":"1749416383","type":"text","text":{"body":"Does it come in another color?"}}]}`),
			"message", "wamid.IN1", "16505551234"},
		{"button reply", wrap("messages", `{`+meta+`,"contacts":[{"profile":{"name":"Sheena Nelson"},"wa_id":"16505551234"}],"messages":[{"context":{"from":"15550783881","id":"`+wamid+`"},"from":"16505551234","id":"wamid.IN2","timestamp":"1749854575","type":"interactive","interactive":{"type":"button_reply","button_reply":{"id":"approve:run-17","title":"Approve"}}}]}`),
			"message", "wamid.IN2", "16505551234"},
		{"delivered", wrap("messages", `{`+meta+`,"statuses":[{"id":"`+wamid+`","status":"delivered","timestamp":"1750263773","recipient_id":"16505551234","pricing":{"billable":true,"pricing_model":"PMP","type":"regular","category":"utility"}}]}`),
			"status.delivered", wamid + ":delivered", wamid},
		{"failed", wrap("messages", `{`+meta+`,"statuses":[{"id":"`+wamid+`","status":"failed","timestamp":"1751142888","recipient_id":"16505551234","errors":[{"code":131049,"title":"This message was not delivered to maintain healthy ecosystem engagement."}]}]}`),
			"status.failed", wamid + ":failed", wamid},
		{"system error", wrap("messages", `{`+meta+`,"errors":[{"code":130429,"title":"Rate limit hit","message":"Rate limit hit"}]}`),
			"error", "", ""},
		{"another field", wrap("message_template_status_update", `{"event":"APPROVED","message_template_id":12345678,"message_template_name":"payslip_ready","message_template_language":"en_US"}`),
			"message_template_status_update", "", ""},
	} {
		body, err := expr.DecodeJSON([]byte(c.body))
		if err != nil {
			t.Fatal(err)
		}
		act := map[string]any{"body": body, "headers": map[string]any{}, "query": map[string]any{}}
		for _, f := range []struct{ name, src, want string }{{"event_type", spec.EventType, c.event}, {"dedup", spec.Dedup, c.dedup}, {"correlation", spec.Correlation, c.correlation}} {
			v, err := e.Eval(f.src, act)
			if err != nil {
				t.Errorf("%s %s: %v", c.name, f.name, err)
				continue
			}
			if got, _ := v.(string); got != f.want {
				t.Errorf("%s %s = %v, want %q", c.name, f.name, v, f.want)
			}
		}
	}
	// Other fields of the app's subscription are acknowledged, not delivered.
	for _, ev := range spec.Events {
		if ev == "message_template_status_update" {
			t.Error("other fields must not be declared events")
		}
	}
}

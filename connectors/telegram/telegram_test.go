package telegram

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
)

const token = "123456:TEST-token"

var creds = map[string]string{"bot_token": token, "webhook_secret": "tsk_Hook-Secret_42"}

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
	return c.Actions[action].Execute(context.Background(), connector.Request{Input: input, HTTP: srv.Client(), Credentials: cr, Attempt: 1, IdempotencyKey: "tsk_unused"})
}

func out(r connector.Response) map[string]any { return r.Output.(map[string]any) }

func TestRegisters(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, ok := reg.Get("telegram@1")
	if !ok {
		t.Fatal("telegram@1 not registered")
	}
	if h := c.Manifest.Hosts(); len(h) != 1 || h[0] != "api.telegram.org" {
		t.Errorf("hosts %v", h)
	}
	for name, want := range map[string]effects.Class{"send_message": effects.UnsafeWrite, "send_photo": effects.UnsafeWrite, "send_document": effects.UnsafeWrite,
		"edit_message_text": effects.IdempotentWrite, "set_webhook": effects.IdempotentWrite, "get_me": effects.Read} {
		if got := c.Manifest.Actions[name].Class; got != want {
			t.Errorf("%s class %s, want %s", name, got, want)
		}
	}
}

func TestReads(t *testing.T) {
	r, err := call(t, "get_me", nil, "get_me")
	if err != nil || out(r)["id"] != "7000000001" || out(r)["username"] != "taskiem_ops_bot" || out(r)["is_bot"] != true {
		t.Errorf("get_me %v %v", r.Output, err)
	}
	r, err = call(t, "get_chat", map[string]any{"chat_id": "-1001234567890"}, "get_chat")
	if err != nil || out(r)["id"] != "-1001234567890" || out(r)["type"] != "supergroup" || out(r)["username"] != "ops_approvals" {
		t.Errorf("get_chat %v %v", r.Output, err)
	}
	r, err = call(t, "get_webhook_info", nil, "get_webhook_info")
	if err != nil || out(r)["pending_update_count"] != int64(3) || !strings.Contains(out(r)["last_error_message"].(string), "401") || len(out(r)["allowed_updates"].([]any)) != 2 {
		t.Errorf("get_webhook_info %v %v", r.Output, err)
	}
}

func TestAuth(t *testing.T) {
	_, err := call(t, "get_me", nil, "get_me_bad_token")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "401") {
		t.Errorf("401: %v", err)
	}
	if _, err := callWith(t, map[string]string{}, "get_me", nil); effects.Classify(err) != effects.KindFatal {
		t.Errorf("no token: %v", err)
	}
}

func TestSendMessage(t *testing.T) {
	kb := map[string]any{"inline_keyboard": []any{[]any{
		map[string]any{"text": "Approve", "callback_data": "approve:run-17"},
		map[string]any{"text": "Reject", "callback_data": "reject:run-17"},
	}}}
	in := map[string]any{"chat_id": "-1001234567890", "text": "Approve payroll for October?", "parse_mode": "HTML", "reply_markup": kb,
		"reply_to_message_id": float64(4200), "allow_sending_without_reply": true, "disable_notification": true, "disable_link_preview": true}
	r, err := call(t, "send_message", in, "send_message")
	if err != nil {
		t.Fatal(err)
	}
	if o := out(r); o["message_id"] != int64(4242) || o["chat_id"] != "-1001234567890" || o["date"] != int64(1791320000) {
		t.Errorf("output %v", o)
	}
	// A channel by @username goes as a string.
	r, err = call(t, "send_message", map[string]any{"chat_id": "@ops_channel", "text": "Deploy finished"}, "send_message_username")
	if err != nil || out(r)["chat_id"] != "-1009876543210" {
		t.Errorf("username %v %v", r.Output, err)
	}
	// Missing inputs never reach Telegram.
	for _, bad := range []map[string]any{{"text": "x"}, {"chat_id": 1}, {"chat_id": 1, "text": "x", "reply_to_message_id": "abc"}} {
		if _, err := call(t, "send_message", bad); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%v: %v", bad, err)
		}
	}
}

func TestErrorClassification(t *testing.T) {
	in := map[string]any{"chat_id": 42, "text": "hi"}
	// Flood control refused the send: retryable, and nothing was sent, so
	// even an unsafe write may go again after retry_after.
	_, err := call(t, "send_message", in, "send_message_flood")
	var he *connector.HTTPError
	if !errors.Is(err, effects.ErrRetryable) || effects.Classify(err) != effects.KindNotSent || !errors.As(err, &he) || he.RetryAfter != 17*time.Second {
		t.Errorf("429: %v (%v)", err, effects.Classify(err))
	}
	if effects.AfterError(effects.UnsafeWrite, effects.Classify(err)) != effects.Retry {
		t.Error("a 429 on a send should be retried")
	}
	_, err = call(t, "send_message", in, "send_message_chat_not_found")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("400: %v", err)
	}
	_, err = call(t, "send_message", in, "send_message_migrated")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "-1001234567890") {
		t.Errorf("migrated: %v", err)
	}
	_, err = call(t, "send_message", in, "send_message_forbidden")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "blocked") {
		t.Errorf("403: %v", err)
	}
	// Telegram may have sent before failing: a send parks.
	_, err = call(t, "send_message", in, "send_message_bad_gateway")
	if effects.Classify(err) != effects.KindUnknownOutcome || effects.AfterError(effects.UnsafeWrite, effects.Classify(err)) != effects.Park {
		t.Errorf("502: %v", err)
	}
	_, err = call(t, "send_message", in, "send_message_ok_false")
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("ok false: %v", err)
	}
}

func TestTokenNeverInErrors(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	c := New(Options{BaseURL: base})
	_, err := c.Actions["send_message"].Execute(context.Background(), connector.Request{Input: map[string]any{"chat_id": 1, "text": "x"},
		HTTP: http.DefaultClient, Credentials: creds})
	if err == nil || strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), "<bot_token>") {
		t.Errorf("error %v", err)
	}
	if effects.Classify(err) != effects.KindNotSent {
		t.Errorf("connection refused should prove nothing was sent: %v", err)
	}
}

func TestSendMedia(t *testing.T) {
	r, err := call(t, "send_photo", map[string]any{"chat_id": -1001234567890, "photo": "https://example.com/receipt.png", "caption": "Receipt for order 17"}, "send_photo")
	if err != nil || out(r)["message_id"] != int64(4242) {
		t.Errorf("photo %v %v", r.Output, err)
	}
	r, err = call(t, "send_document", map[string]any{"chat_id": "-1001234567890", "document": "BQACAgQAAxkBAAIBY2", "caption": "October payslips", "reply_to_message_id": 4200}, "send_document")
	if err != nil || out(r)["message"].(map[string]any)["document"] == nil {
		t.Errorf("document %v %v", r.Output, err)
	}
	if _, err := call(t, "send_document", map[string]any{"chat_id": 1}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("no document: %v", err)
	}
}

func TestEditMessageText(t *testing.T) {
	in := map[string]any{"chat_id": -1001234567890, "message_id": 4242, "text": "Payroll approved by Ada", "reply_markup": map[string]any{"inline_keyboard": []any{}}}
	r, err := call(t, "edit_message_text", in, "edit_message_text")
	if err != nil || out(r)["modified"] != true || out(r)["message_id"] != int64(4242) || out(r)["chat_id"] != "-1001234567890" {
		t.Errorf("edit %v %v", r.Output, err)
	}
	// A retry after the edit landed: already as asked.
	r, err = call(t, "edit_message_text", in, "edit_message_not_modified")
	if err != nil || out(r)["modified"] != false || out(r)["message_id"] != int64(4242) {
		t.Errorf("not modified %v %v", r.Output, err)
	}
	r, err = call(t, "edit_message_text", map[string]any{"inline_message_id": "AAEAAAB", "text": "Done"}, "edit_inline")
	if err != nil || out(r)["modified"] != true {
		t.Errorf("inline %v %v", r.Output, err)
	}
	if _, err := call(t, "edit_message_text", map[string]any{"chat_id": 1, "text": "x"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("no message id: %v", err)
	}
}

func TestCallbackAndWebhookManagement(t *testing.T) {
	r, err := call(t, "answer_callback_query", map[string]any{"callback_query_id": "4382bfdwdsb323b2d9", "text": "Approved", "show_alert": false}, "answer_callback_query")
	if err != nil || out(r)["answered"] != true {
		t.Errorf("answer %v %v", r.Output, err)
	}
	url := "https://taskiem.example.com/hooks/7f0c/connectors/telegram@1/update?env=prod"
	// secret_token is always the connection's webhook_secret.
	r, err = call(t, "set_webhook", map[string]any{"url": url, "allowed_updates": []any{"message", "callback_query"}, "drop_pending_updates": true}, "set_webhook")
	if err != nil || out(r)["set"] != true {
		t.Errorf("set %v %v", r.Output, err)
	}
	for _, secret := range []string{"", "has space", strings.Repeat("a", 257)} {
		if _, err := callWith(t, map[string]string{"bot_token": token, "webhook_secret": secret}, "set_webhook", map[string]any{"url": url}); effects.Classify(err) != effects.KindFatal {
			t.Errorf("secret %q: %v", secret, err)
		}
	}
	if _, err := call(t, "set_webhook", map[string]any{"url": "http://insecure.example.com"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("http url: %v", err)
	}
	r, err = call(t, "delete_webhook", map[string]any{"drop_pending_updates": true}, "delete_webhook")
	if err != nil || out(r)["deleted"] != true {
		t.Errorf("delete %v %v", r.Output, err)
	}
}

func TestWebhookSecretToken(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["update"].Verify
	body := []byte(`{"update_id":10000,"message":{"message_id":1,"date":1,"chat":{"id":5,"type":"private"},"text":"hi"}}`)
	h := http.Header{}
	h.Set("X-Telegram-Bot-Api-Secret-Token", "tsk_Hook-Secret_42")
	if err := connector.VerifyWebhook(spec, "tsk_Hook-Secret_42", h, body); err != nil {
		t.Errorf("valid: %v", err)
	}
	if connector.VerifyWebhook(spec, "other", h, body) == nil {
		t.Error("wrong secret accepted")
	}
	if connector.VerifyWebhook(spec, "tsk_Hook-Secret_42", http.Header{}, body) == nil {
		t.Error("missing header accepted")
	}
	if connector.VerifyWebhook(spec, "", http.Header{}, body) == nil {
		t.Error("a connection without a webhook_secret accepted a delivery")
	}
}

func TestTriggerExpressions(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["update"]
	e := expr.MustNewTriggerEngine("body", "headers", "query")
	for _, c := range []struct{ body, event, dedup, correlation string }{
		{`{"update_id":10000,"message":{"message_id":1365,"from":{"id":1111111,"is_bot":false,"first_name":"Ada"},"chat":{"id":1111111,"type":"private","first_name":"Ada"},"date":1791320000,"text":"/start"}}`,
			"message", "10000", "1111111"},
		{`{"update_id":10001,"callback_query":{"id":"4382bfdwdsb323b2d9","from":{"id":1111111,"is_bot":false,"first_name":"Ada"},"message":{"message_id":4242,"chat":{"id":-1001234567890,"type":"supergroup"},"date":1791320000,"text":"Approve?"},"chat_instance":"-12345","data":"approve:run-17"}}`,
			"callback_query", "10001", "-1001234567890"},
		{`{"update_id":10002,"edited_message":{"message_id":1365,"chat":{"id":1111111,"type":"private"},"date":1,"edit_date":2,"text":"/start now"}}`,
			"edited_message", "10002", "1111111"},
		{`{"update_id":10003,"channel_post":{"message_id":7,"chat":{"id":-1009876543210,"type":"channel","title":"Ops"},"date":1,"text":"deployed"}}`,
			"channel_post", "10003", "-1009876543210"},
		{`{"update_id":10004,"inline_query":{"id":"99","from":{"id":1111111,"is_bot":false,"first_name":"Ada"},"query":"pay","offset":""}}`,
			"inline_query", "10004", ""},
		{`{"update_id":10005,"my_chat_member":{"chat":{"id":1111111,"type":"private"},"from":{"id":1111111,"is_bot":false,"first_name":"Ada"},"date":1,"old_chat_member":{"status":"member"},"new_chat_member":{"status":"kicked"}}}`,
			"my_chat_member", "10005", "1111111"},
		{`{"update_id":10006,"callback_query":{"id":"5","from":{"id":1,"is_bot":false,"first_name":"A"},"inline_message_id":"AAEAAAB","chat_instance":"1","data":"x"}}`,
			"callback_query", "10006", ""},
	} {
		body, err := expr.DecodeJSON([]byte(c.body))
		if err != nil {
			t.Fatal(err)
		}
		act := map[string]any{"body": body, "headers": map[string]any{}, "query": map[string]any{}}
		for _, f := range []struct{ name, src, want string }{{"event_type", spec.EventType, c.event}, {"dedup", spec.Dedup, c.dedup}, {"correlation", spec.Correlation, c.correlation}} {
			v, err := e.Eval(f.src, act)
			if err != nil {
				t.Errorf("%s %s: %v", c.event, f.name, err)
				continue
			}
			if got, _ := v.(string); got != f.want {
				t.Errorf("%s %s = %v, want %q", c.event, f.name, v, f.want)
			}
		}
		if ev, _ := e.Eval(spec.EventType, act); !contains(spec.Events, ev.(string)) {
			t.Errorf("event %v is not declared", ev)
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

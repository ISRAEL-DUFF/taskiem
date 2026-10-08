package africastalking

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
)

const key = "tsk_ab12cd34ef56gh78ij90kl12mn34op56"

var creds = map[string]string{"username": "acme", "api_key": "atsk_test_key", "sender_id": "ACME"}

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
	return c.Actions[action].Execute(context.Background(), connector.Request{Input: input, HTTP: srv.Client(), Credentials: cr, IdempotencyKey: key, Attempt: 1})
}

func out(r connector.Response) map[string]any { return r.Output.(map[string]any) }

func TestRegistersBothHosts(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, ok := reg.Get("africastalking@1")
	if !ok {
		t.Fatal("not registered")
	}
	if h := c.Manifest.Hosts(); len(h) != 2 || h[0] != "api.africastalking.com" || h[1] != "api.sandbox.africastalking.com" {
		t.Errorf("hosts %v", h)
	}
	cl := &client{live: "L", sandbox: "S"}
	if cl.base(connector.Request{Credentials: map[string]string{"environment": " Sandbox "}}) != "S" || cl.base(connector.Request{}) != "L" {
		t.Error("environment selection")
	}
}

func TestBulkSMS(t *testing.T) {
	in := map[string]any{"to": []any{"+254711000001", "+254711000002", "+25471100"}, "message": "Your payslip is ready"}
	r, err := call(t, "send_sms", in, "send_sms_bulk")
	if err != nil {
		t.Fatal(err)
	}
	o := out(r)
	if o["accepted"] != int64(2) || o["rejected"] != int64(1) || !strings.Contains(o["summary"].(string), "2/3") {
		t.Errorf("output %v", o)
	}
	first := o["recipients"].([]any)[0].(map[string]any)
	if first["message_id"] != "ATXid_a1" || first["status_code"] != int64(101) || first["cost"] != "KES 0.8000" {
		t.Errorf("recipient %v", first)
	}
	// The step's sender ID beats the connection's; enqueue is sent as 1.
	if _, err := call(t, "send_sms", map[string]any{"to": []any{"+254711000001"}, "message": "Hi", "sender_id": "OTHER", "enqueue": true}, "send_sms_bulk_enqueue"); err != nil {
		t.Errorf("enqueue: %v", err)
	}
}

func TestSMSRefusals(t *testing.T) {
	in := map[string]any{"to": []any{"+254711000001"}, "message": "Hi"}
	_, err := call(t, "send_sms", in, "send_sms_all_refused")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "InsufficientBalance") {
		t.Errorf("all refused: %v", err)
	}
	_, err = call(t, "send_sms", in, "send_sms_gateway")
	if effects.Classify(err) != effects.KindIndeterminate {
		t.Errorf("gateway error: %v", err)
	}
	_, err = call(t, "send_sms", in, "send_sms_no_recipients")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "InvalidSenderId") {
		t.Errorf("request refused: %v", err)
	}
	_, err = call(t, "send_sms", in, "send_sms_bad_key")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "supplied authentication is invalid") {
		t.Errorf("401: %v", err)
	}
	// Africa's Talking may have sent: the step parks.
	_, err = call(t, "send_sms", in, "send_sms_server_error")
	if effects.AfterError(effects.UnsafeWrite, effects.Classify(err)) != effects.Park {
		t.Errorf("500: %v", err)
	}
	// A rate limit refused the request: nothing was sent.
	_, err = call(t, "send_sms", in, "send_sms_rate_limited")
	if !errors.Is(err, effects.ErrRetryable) || effects.AfterError(effects.UnsafeWrite, effects.Classify(err)) != effects.Retry {
		t.Errorf("429: %v", err)
	}
	// Checked before sending.
	for _, bad := range []map[string]any{{"message": "x"}, {"to": []any{}, "message": "x"}, {"to": []any{"+1"}}, {"to": []any{3}, "message": "x"}} {
		if _, err := call(t, "send_sms", bad); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if _, err := callWith(t, map[string]string{"username": "acme", "api_key": "k"}, "send_sms", in); effects.Classify(err) != effects.KindFatal {
		t.Errorf("live without a sender ID: %v", err)
	}
	if _, err := callWith(t, map[string]string{"username": "acme"}, "send_sms", in); effects.Classify(err) != effects.KindFatal {
		t.Errorf("no api key: %v", err)
	}
}

func TestSandboxSendsTheForm(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version1/messaging" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.Header.Get("apiKey") != "sbx_key" {
			t.Errorf("request %s %s %v", r.Method, r.URL.Path, r.Header)
		}
		_ = r.ParseForm()
		got = r.PostForm
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"SMSMessageData":{"Message":"Sent to 2/2 Total Cost: KES 1.6000","Recipients":[{"statusCode":101,"number":"+254711000001","status":"Success","cost":"KES 0.8000","messageId":"ATXid_s1"},{"statusCode":101,"number":"+254711000002","status":"Success","cost":"KES 0.8000","messageId":"ATXid_s2"}]}}`))
	}))
	defer srv.Close()
	c := New(Options{BaseURL: srv.URL})
	r, err := c.Actions["send_sms"].Execute(context.Background(), connector.Request{HTTP: srv.Client(),
		Credentials: map[string]string{"username": "sandbox", "api_key": "sbx_key", "environment": "sandbox"},
		Input:       map[string]any{"to": []any{"+254711000001", "+254711000002"}, "message": "Test", "enqueue": true}})
	if err != nil || out(r)["accepted"] != int64(2) {
		t.Fatalf("%v %v", r.Output, err)
	}
	if got.Get("username") != "sandbox" || got.Get("to") != "+254711000001,+254711000002" || got.Get("message") != "Test" || got.Get("enqueue") != "1" || got.Has("from") {
		t.Errorf("form %v", got)
	}
}

func TestFetchMessagesAndBalance(t *testing.T) {
	r, err := call(t, "fetch_messages", map[string]any{"last_received_id": float64(5)}, "fetch_messages")
	if err != nil {
		t.Fatal(err)
	}
	m := out(r)["messages"].([]any)[0].(map[string]any)
	if m["id"] != "15071" || m["from"] != "+254711000001" || m["link_id"] != "SampleLinkId123" || m["text"] != "STOP" {
		t.Errorf("message %v", m)
	}
	r, err = call(t, "get_balance", nil, "balance")
	if err != nil || out(r)["currency"] != "KES" || out(r)["amount"] != int64(178550) || out(r)["balance"] != "KES 1785.5050" {
		t.Errorf("balance %v %v", r.Output, err)
	}
}

func TestAirtime(t *testing.T) {
	in := map[string]any{"recipients": []any{map[string]any{"phone_number": "+254711000001", "currency_code": "kes", "amount": float64(10050)}}, "max_num_retry": 2}
	// The engine's key goes as Idempotency-Key; the amount as "KES 100.50".
	r, err := call(t, "send_airtime", in, "send_airtime")
	if err != nil {
		t.Fatal(err)
	}
	resp := out(r)["responses"].([]any)[0].(map[string]any)
	if out(r)["num_sent"] != int64(1) || resp["request_id"] != "ATQid_1be914ac47845eef1a1dab5d89ec50ff" || resp["status"] != "Sent" {
		t.Errorf("output %v", r.Output)
	}
	_, err = call(t, "send_airtime", in, "send_airtime_refused")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "Insufficient Credit") {
		t.Errorf("refused: %v", err)
	}
	for _, bad := range []map[string]any{
		{},
		{"recipients": []any{map[string]any{"phone_number": "+1", "currency_code": "KES", "amount": 10.5}}},
		{"recipients": []any{map[string]any{"phone_number": "+1", "currency_code": "KSH1", "amount": 100}}},
		{"recipients": []any{map[string]any{"currency_code": "KES", "amount": 100}}},
	} {
		if _, err := call(t, "send_airtime", bad); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%v: %v", bad, err)
		}
	}
	r, err = call(t, "get_airtime_status", map[string]any{"transaction_id": "ATQid_1be914ac47845eef1a1dab5d89ec50ff"}, "airtime_status")
	if err != nil || out(r)["status"] != "Success" {
		t.Errorf("status %v %v", r.Output, err)
	}
}

// Africa's Talking signs nothing: each callback URL carries the
// connection's callback_token, and deliveries without it are refused.
func TestCallbacksCarryAToken(t *testing.T) {
	m := New(Options{}).Manifest
	for name, tr := range m.Triggers {
		if tr.Verify == nil || tr.Verify.Scheme != "query_secret" || tr.Verify.Query != "token" || tr.Verify.SecretField != "callback_token" {
			t.Errorf("%s verify %+v", name, tr.Verify)
			continue
		}
		if err := connector.VerifyQuerySecret(tr.Verify, "tok", url.Values{"token": {"tok"}}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		for _, q := range []url.Values{{}, {"token": {"other"}}} {
			if connector.VerifyQuerySecret(tr.Verify, "tok", q) == nil {
				t.Errorf("%s accepted %v", name, q)
			}
		}
	}
	found := false
	for _, f := range m.Auth.Fields {
		found = found || (f.Key == "callback_token" && f.Secret)
	}
	if !found {
		t.Error("no secret callback_token field")
	}
}

// Callbacks are form posts; ingest exposes the fields as body.<field>.
func TestTriggerExpressions(t *testing.T) {
	m := New(Options{}).Manifest
	e := expr.MustNewTriggerEngine("body", "headers", "query")
	for _, c := range []struct{ trigger, form, event, dedup, correlation string }{
		{"delivery_report", "id=ATXid_a1&status=Success&phoneNumber=%2B254711000001&networkCode=63902&retryCount=0",
			"Success", "ATXid_a1:Success", "ATXid_a1"},
		{"delivery_report", "id=ATXid_a2&status=Failed&phoneNumber=%2B254711000002&networkCode=63902&failureReason=UserInBlacklist",
			"Failed", "ATXid_a2:Failed", "ATXid_a2"},
		{"incoming_sms", "date=2026-10-06+11%3A20%3A46&from=%2B254711000001&id=15071&linkId=SampleLinkId123&text=STOP&to=28901&networkCode=63902",
			"incoming_sms", "15071", "+254711000001"},
		{"airtime_status", "phoneNumber=%2B254711000001&description=Airtime+Delivered+Successfully&status=Success&requestId=ATQid_1be9&discount=KES+4.0200&value=KES+100.5000",
			"Success", "ATQid_1be9:Success", "ATQid_1be9"},
	} {
		vals, err := url.ParseQuery(c.form)
		if err != nil {
			t.Fatal(err)
		}
		body := map[string]any{}
		for k, v := range vals {
			body[k] = v[0]
		}
		act := map[string]any{"body": body, "headers": map[string]any{}, "query": map[string]any{}}
		spec := m.Triggers[c.trigger]
		for _, f := range []struct{ name, src, want string }{{"event_type", spec.EventType, c.event}, {"dedup", spec.Dedup, c.dedup}, {"correlation", spec.Correlation, c.correlation}} {
			if f.src == "" {
				if f.want != "" {
					t.Errorf("%s has no %s", c.trigger, f.name)
				}
				continue
			}
			v, err := e.Eval(f.src, act)
			if err != nil {
				t.Errorf("%s %s: %v", c.trigger, f.name, err)
				continue
			}
			if got, _ := v.(string); got != f.want {
				t.Errorf("%s %s = %v, want %q", c.trigger, f.name, v, f.want)
			}
		}
	}
}

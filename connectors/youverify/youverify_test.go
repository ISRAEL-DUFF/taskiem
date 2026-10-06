package youverify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
)

func call(t *testing.T, action string, input map[string]any, exchanges ...string) (connector.Response, error) {
	t.Helper()
	var exs []fixture.Exchange
	for _, n := range exchanges {
		exs = append(exs, fixture.Load(t, n))
	}
	srv := fixture.Serve(t, exs...)
	return New(Options{BaseURL: srv.URL}).Actions[action].Execute(context.Background(), connector.Request{Input: input, HTTP: srv.Client(),
		Credentials: map[string]string{"secret_key": "yv_test_key"}})
}

func out(r connector.Response) map[string]any { return r.Output.(map[string]any) }

func kind(t *testing.T, err error, want effects.ErrorKind, what string) {
	t.Helper()
	if got := effects.Classify(err); err == nil || got != want {
		t.Errorf("%s: want %s, got %v (%v)", what, want, got, err)
	}
}

func TestRegisters(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, _ := reg.Get("youverify@1")
	if h := c.Manifest.Hosts(); len(h) != 2 || h[0] != "api.youverify.co" || h[1] != "api.sandbox.youverify.co" {
		t.Errorf("hosts %v", h)
	}
	if len(c.Actions) != len(c.Manifest.Actions) {
		t.Errorf("%d handlers for %d actions", len(c.Actions), len(c.Manifest.Actions))
	}
	for name, a := range c.Manifest.Actions {
		if c.Actions[name] == nil {
			t.Errorf("%s has no handler", name)
		}
		write := name == "create_address_candidate" || name == "request_address_verification"
		if write != (a.Class == effects.UnsafeWrite) || (!write && a.Class != effects.Read) {
			t.Errorf("%s is %s", name, a.Class)
		}
	}
	cl := &client{live: "L", sandbox: "S"}
	if cl.base(connector.Request{Credentials: map[string]string{"environment": "Sandbox"}}) != "S" || cl.base(connector.Request{}) != "L" {
		t.Error("environment selection")
	}
}

func TestPIIDeclared(t *testing.T) {
	m := New(Options{}).Manifest
	want := map[string]map[string]string{
		"verify_bvn":                   {"bvn": "bvn", "first_name": "name", "last_name": "name", "date_of_birth": "other"},
		"verify_nin":                   {"nin": "nin", "selfie_image": "other", "first_name": "name"},
		"verify_drivers_license":       {"license_number": "other"},
		"verify_phone":                 {"phone": "phone"},
		"search_phone":                 {"phone": "phone"},
		"resolve_bank_account":         {"account_number": "account_number"},
		"compare_faces":                {"image1": "other", "image2": "other"},
		"screen_aml":                   {"query": "name", "first_name": "name", "last_name": "name"},
		"create_address_candidate":     {"first_name": "name", "last_name": "name", "mobile": "phone", "email": "email", "image": "other", "date_of_birth": "other"},
		"request_address_verification": {"street": "address", "building_number": "address", "landmark": "address"},
	}
	for action, fields := range want {
		got := map[string]string{}
		for _, p := range m.Actions[action].PII {
			got[p.Field] = p.Category
		}
		for f, cat := range fields {
			if got[f] != cat {
				t.Errorf("%s.%s: pii %q, want %q", action, f, got[f], cat)
			}
		}
	}
}

func TestIdentity(t *testing.T) {
	r, err := call(t, "verify_nin", map[string]any{"nin": "11111111111", "subject_consent": true, "first_name": "Sarah", "last_name": "Doe",
		"date_of_birth": "1988-04-04", "selfie_image": "https://cdn.example.com/s.jpg"}, "nin")
	if err != nil {
		t.Fatal(err)
	}
	o := out(r)
	if o["found"] != true || o["last_name"] != "Doe" || o["address"] != "13B Fake Street, Ilupeju Niger State" || o["all_validation_passed"] != true ||
		o["id"] != "6491edda239381d3be87a5fb" || o["image"] != nil {
		t.Errorf("nin %v", o)
	}
	if rec := o["record"].(map[string]any); rec["image"] != nil || rec["signature"] != nil || rec["birthState"] != "Edo" {
		t.Errorf("record %v", rec)
	}
	r, err = call(t, "verify_bvn", map[string]any{"bvn": "11111111111", "subject_consent": true, "premium": true, "include_image": true}, "bvn")
	if err != nil || out(r)["first_name"] != "John" || out(r)["image"] == "" {
		t.Errorf("bvn %v %v", r.Output, err)
	}
	r, err = call(t, "verify_drivers_license", map[string]any{"license_number": "AAA00000AA00", "subject_consent": true}, "drivers_license")
	if err != nil || out(r)["record"].(map[string]any)["stateOfIssuance"] != "BENUE" {
		t.Errorf("licence %v %v", r.Output, err)
	}
	r, err = call(t, "verify_phone", map[string]any{"phone": "08036000000", "subject_consent": true}, "phone")
	if err != nil || len(out(r)["record"].(map[string]any)["phoneDetails"].([]any)) != 1 {
		t.Errorf("phone %v %v", r.Output, err)
	}
	r, err = call(t, "search_phone", map[string]any{"phone": "08000000000", "subject_consent": true}, "nin_phone")
	if err != nil || out(r)["first_name"] != "Sarah" {
		t.Errorf("search phone %v %v", r.Output, err)
	}
}

func TestResults(t *testing.T) {
	in := map[string]any{"bvn": "11111111112", "subject_consent": true}
	r, err := call(t, "verify_bvn", in, "bvn_not_found")
	if err != nil || out(r)["found"] != false || out(r)["status"] != "not_found" {
		t.Errorf("not found is a result: %v %v", r.Output, err)
	}
	r, err = call(t, "verify_bvn", in, "bvn_pending")
	if err != nil || out(r)["status"] != "pending" || out(r)["id"] != "646b5608427a7e688e3ef635" {
		t.Errorf("pending %v %v", r.Output, err)
	}
	_, err = call(t, "verify_bvn", in, "bvn_failed")
	kind(t, err, effects.KindRetryable, "failed check")
	_, err = call(t, "verify_bvn", in, "bvn_no_funds")
	kind(t, err, effects.KindFatal, "402")
	if !strings.Contains(err.Error(), "PaymentRequiredError") {
		t.Errorf("name: %v", err)
	}
	_, err = call(t, "verify_bvn", in, "bvn_unauthorised")
	kind(t, err, effects.KindFatal, "401")
	_, err = call(t, "verify_bvn", in, "bvn_rate_limited")
	kind(t, err, effects.KindRetryable, "429")
	_, err = call(t, "verify_bvn", in, "bvn_server_error")
	kind(t, err, effects.KindUnknownOutcome, "500")
	_, err = call(t, "verify_bvn", map[string]any{"bvn": "11111111111"})
	kind(t, err, effects.KindFatal, "no consent")
	srv := fixture.Serve(t)
	_, err = New(Options{BaseURL: srv.URL}).Actions["verify_bvn"].Execute(context.Background(), connector.Request{Input: in, HTTP: srv.Client()})
	kind(t, err, effects.KindFatal, "no key")
}

func TestBankFaceBusiness(t *testing.T) {
	r, err := call(t, "resolve_bank_account", map[string]any{"account_number": "1000000000", "bank_code": "058", "subject_consent": true}, "bank_account")
	if err != nil || out(r)["account_name"] != "MICHAEL JOHN DOE" || out(r)["found"] != true {
		t.Errorf("bank %v %v", r.Output, err)
	}
	r, err = call(t, "compare_faces", map[string]any{"image1": "https://cdn.youverify.co/a.jpg", "image2": "https://cdn.youverify.co/b.jpg", "subject_consent": true}, "compare_no_match")
	if err != nil || out(r)["match"] != false || out(r)["confidence"] != float64(47) || out(r)["threshold"] != float64(80) {
		t.Errorf("faces %v %v", r.Output, err)
	}
	r, err = call(t, "verify_business", map[string]any{"registration_number": "RC00000000", "subject_consent": true}, "kyb_basic")
	if err != nil || out(r)["name"] != "John Doe Inc" || out(r)["company_status"] != "ACTIVE" {
		t.Errorf("kyb %v %v", r.Output, err)
	}
	r, err = call(t, "verify_business", map[string]any{"registration_number": "RC99999999", "subject_consent": true}, "kyb_not_found")
	if err != nil || out(r)["found"] != false {
		t.Errorf("kyb not found %v %v", r.Output, err)
	}
	r, err = call(t, "search_businesses", map[string]any{"query": "John Doe", "country_code": "NG", "limit": 5}, "search_companies")
	if err != nil || out(r)["businesses"].([]any)[0].(map[string]any)["country_code"] != "NG" {
		t.Errorf("search %v %v", r.Output, err)
	}
	r, err = call(t, "get_business_details", map[string]any{"verification_id": "6266b83130b6f4cba3a5a2dc"}, "company_details")
	if err != nil || out(r)["record"].(map[string]any)["tin"] != "00000000-0000" {
		t.Errorf("details %v %v", r.Output, err)
	}
	_, err = call(t, "get_business_details", map[string]any{"verification_id": "nope"}, "company_details_missing")
	kind(t, err, effects.KindFatal, "missing details")
}

func TestAML(t *testing.T) {
	r, err := call(t, "screen_aml", map[string]any{"query": "Muhammed Gudaji", "subject_consent": true, "country": "Nigeria"}, "aml_name")
	if err != nil || out(r)["status"] != "review_required" || out(r)["total_hits"] != int64(1) || out(r)["category_count"].(map[string]any)["pep"] != int64(1) {
		t.Errorf("aml %v %v", r.Output, err)
	}
	r, err = call(t, "get_aml_check", map[string]any{"verification_id": "64d5c3dfdf93fda73eeff33e"}, "aml_get")
	if err != nil || out(r)["id"] != "64d5c3dfdf93fda73eeff33e" {
		t.Errorf("aml get %v %v", r.Output, err)
	}
}

func TestAddressVerification(t *testing.T) {
	r, err := call(t, "create_address_candidate", map[string]any{"first_name": "Famous", "last_name": "Ehichioya", "mobile": "08036000000",
		"image": "https://cdn.youverify.co/p.jpg"}, "candidate")
	if err != nil || out(r)["candidate_id"] != "618e539d540fb2e1d076427a" {
		t.Errorf("candidate %v %v", r.Output, err)
	}
	in := map[string]any{"candidate_id": "618e539d540fb2e1d076427a", "subject_consent": true, "description": "Verify the candidate",
		"building_number": "350", "street": "Borno way", "landmark": "Police Station", "city": "Yaba", "state": "Lagos"}
	r, err = call(t, "request_address_verification", in, "address_requested")
	if err != nil || out(r)["reference_id"] != "61883f9ebc2c9" || out(r)["task_status"] != "PENDING" {
		t.Errorf("request %v %v", r.Output, err)
	}
	// An unknown outcome on an unsafe write must park, never resend.
	_, err = call(t, "request_address_verification", in, "address_request_server_error")
	kind(t, err, effects.KindUnknownOutcome, "502")
	r, err = call(t, "get_address_verification", map[string]any{"id": "61882ca9a7422a684a3d0cdf"}, "address_get")
	if err != nil || out(r)["task_status"] != "VERIFIED" || out(r)["download_url"] == "" {
		t.Errorf("get %v %v", r.Output, err)
	}
}

func TestWebhookSignature(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["event"].Verify
	body := []byte(`{"event":"identity.verification.completed","apiVersion":"v2","data":{"id":"v1","status":"found"}}`)
	m := hmac.New(sha256.New, []byte("yv_test_key"))
	m.Write(body)
	h := http.Header{}
	h.Set("x-yv-signature", hex.EncodeToString(m.Sum(nil)))
	if err := connector.VerifyWebhook(spec, "yv_test_key", h, body); err != nil {
		t.Errorf("valid: %v", err)
	}
	if connector.VerifyWebhook(spec, "other", h, body) == nil {
		t.Error("wrong key accepted")
	}
	if connector.VerifyWebhook(spec, "yv_test_key", h, append(body, ' ')) == nil {
		t.Error("tampered body accepted")
	}
}

func TestTriggerExpressions(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["event"]
	e := expr.MustNewTriggerEngine("body", "headers", "query")
	for _, c := range []struct{ body, event, dedup, correlation string }{
		{`{"event":"identity.verification.completed","apiVersion":"v2","data":{"id":"646b","status":"found","type":"bvn","createdAt":"2024-03-27T08:30:03.367Z"}}`,
			"identity.verification.completed", "identity.verification.completed:646b:found::2024-03-27T08:30:03.367Z", "646b"},
		{`{"event":"identity.verification.completed","apiVersion":"v2","data":{"status":"found","type":"nin","createdAt":"2024-03-27T08:30:03.367Z"}}`,
			"identity.verification.completed", "identity.verification.completed::found::2024-03-27T08:30:03.367Z", ""},
		{`{"event":"address.verification.completed","apiVersion":"v2","data":{"status":"awaiting_qa","taskStatus":"VERIFIED","referenceId":"620865a2cae6f","verificationId":"620865a2cae6f","createdAt":"2022-02-13T01:58:07.159Z"}}`,
			"address.verification.completed", "address.verification.completed:620865a2cae6f:awaiting_qa:VERIFIED:2022-02-13T01:58:07.159Z", "620865a2cae6f"},
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
	}
}

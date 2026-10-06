package prembly

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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
		Credentials: map[string]string{"api_key": "sk_test_prembly", "public_key": "pk_test_prembly"}})
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
	c, _ := reg.Get("prembly@1")
	if len(c.Actions) != len(c.Manifest.Actions) {
		t.Errorf("%d handlers for %d actions", len(c.Actions), len(c.Manifest.Actions))
	}
	for name, a := range c.Manifest.Actions {
		if c.Actions[name] == nil {
			t.Errorf("%s has no handler", name)
		}
		if a.Class != effects.Read {
			t.Errorf("%s is %s; every Prembly check is a read", name, a.Class)
		}
	}
}

func TestPIIDeclared(t *testing.T) {
	m := New(Options{}).Manifest
	want := map[string]map[string]string{
		"lookup_bvn": {"bvn": "bvn"}, "lookup_bvn_basic": {"bvn": "bvn"}, "verify_bvn_with_face": {"bvn": "bvn", "image": "other"},
		"lookup_bvn_by_phone": {"phone_number": "phone"}, "lookup_nin": {"nin": "nin"}, "lookup_nin_basic": {"nin": "nin"},
		"verify_nin_with_face": {"nin": "nin", "image": "other", "date_of_birth": "other"}, "lookup_phone": {"phone_number": "phone"},
		"lookup_phone_basic": {"phone_number": "phone"}, "verify_drivers_license": {"license_number": "other", "first_name": "name", "last_name": "name"},
		"verify_passport":      {"passport_number": "other", "date_of_birth": "other", "nin": "nin"},
		"verify_voters_card":   {"vin": "other", "last_name": "name", "date_of_birth": "other"},
		"resolve_bank_account": {"account_number": "account_number"}, "compare_faces": {"image_one": "other", "image_two": "other"},
		"check_liveness": {"image": "other"},
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

func TestBVN(t *testing.T) {
	r, err := call(t, "lookup_bvn", map[string]any{"bvn": "22418321365"}, "bvn_advance")
	if err != nil {
		t.Fatal(err)
	}
	o := out(r)
	if o["found"] != true || o["verified"] != true || o["first_name"] != "RONALD" || o["last_name"] != "EKOM" || o["phone_number"] != "07058690798" ||
		o["watchlisted"] != true || o["reference"] != "9a7cb406-c59b-4e09-bde4-c0ea65f40d2f" || o["address"] == "" {
		t.Errorf("output %v", o)
	}
	if _, ok := o["image"]; ok {
		t.Error("photo returned without include_image")
	}
	if _, ok := o["record"].(map[string]any)["base64Image"]; ok {
		t.Error("photo left in record")
	}
	r, err = call(t, "lookup_bvn", map[string]any{"bvn": "22418321365", "include_image": true}, "bvn_advance")
	if err != nil || out(r)["image"] == "" {
		t.Errorf("include_image: %v %v", r.Output, err)
	}
	r, err = call(t, "lookup_bvn_basic", map[string]any{"bvn": "22451333604"}, "bvn_basic")
	if err != nil || out(r)["first_name"] != "John" || out(r)["phone_number"] != "08012345678" || out(r)["watchlisted"] != false {
		t.Errorf("basic %v %v", r.Output, err)
	}
	r, err = call(t, "verify_bvn_with_face", map[string]any{"bvn": "22229512456", "image": "https://example.com/face.jpg"}, "bvn_face")
	if err != nil || out(r)["face_match"] != true || out(r)["face_confidence"].(float64) < 99 {
		t.Errorf("face %v %v", r.Output, err)
	}
	r, err = call(t, "lookup_bvn_by_phone", map[string]any{"phone_number": "0703000002"}, "bvn_by_phone")
	if err != nil || out(r)["bvn"] != "22280005737" {
		t.Errorf("by phone %v %v", r.Output, err)
	}
}

func TestNoRecordIsAResult(t *testing.T) {
	r, err := call(t, "lookup_bvn", map[string]any{"bvn": "22000000000"}, "bvn_not_found")
	if err != nil || out(r)["found"] != false || out(r)["blocked"] != false || !strings.Contains(out(r)["message"].(string), "Record not found") {
		t.Errorf("not found %v %v", r.Output, err)
	}
	r, err = call(t, "lookup_bvn", map[string]any{"bvn": "22000000000"}, "bvn_blocked")
	if err != nil || out(r)["found"] != false || out(r)["blocked"] != true {
		t.Errorf("blocked %v %v", r.Output, err)
	}
	r, err = call(t, "lookup_nin", map[string]any{"nin": "12345678901"}, "nin_suspended")
	if err != nil || out(r)["blocked"] != true || out(r)["verification_status"] != "PENDING" {
		t.Errorf("suspended %v %v", r.Output, err)
	}
}

func TestFailures(t *testing.T) {
	_, err := call(t, "lookup_bvn", map[string]any{"bvn": "22000000000"}, "bvn_unavailable")
	kind(t, err, effects.KindRetryable, "code 02")
	_, err = call(t, "lookup_bvn", map[string]any{"bvn": "22000000000"}, "bvn_wallet_empty")
	kind(t, err, effects.KindFatal, "code 03")
	_, err = call(t, "lookup_bvn", map[string]any{"bvn": "22000000000"}, "bvn_bad_key")
	kind(t, err, effects.KindFatal, "401")
	if !strings.Contains(err.Error(), "valid api-key") {
		t.Errorf("message: %v", err)
	}
	_, err = call(t, "lookup_bvn", map[string]any{"bvn": "22000000000"}, "bvn_server_error")
	kind(t, err, effects.KindUnknownOutcome, "500")
	_, err = call(t, "lookup_bvn", map[string]any{})
	kind(t, err, effects.KindFatal, "no number")
	srv := fixture.Serve(t)
	_, err = New(Options{BaseURL: srv.URL}).Actions["lookup_bvn"].Execute(context.Background(), connector.Request{Input: map[string]any{"bvn": "1"}, HTTP: srv.Client()})
	kind(t, err, effects.KindFatal, "no key")
}

func TestNINAndPhone(t *testing.T) {
	r, err := call(t, "lookup_nin", map[string]any{"nin": "12345678901"}, "nin_advance")
	if err != nil {
		t.Fatal(err)
	}
	o := out(r)
	if o["last_name"] != "JOHNSON" || o["date_of_birth"] != "22-05-1998" || o["phone_number"] != "08092097506" || o["image"] != nil {
		t.Errorf("nin %v", o)
	}
	rec := o["record"].(map[string]any)
	if rec["signature"] != nil || rec["photo"] != nil || rec["nin_suspension_message"] != "NOT SUSPENDED" {
		t.Errorf("record %v", rec)
	}
	r, err = call(t, "lookup_nin_basic", map[string]any{"nin": "56182742701"}, "nin_basic")
	if err != nil || out(r)["first_name"] != "GRACE" {
		t.Errorf("nin basic %v %v", r.Output, err)
	}
	r, err = call(t, "verify_nin_with_face", map[string]any{"nin": "12345678901", "image": "https://example.com/face.jpg", "date_of_birth": "1990-01-01"}, "nin_face")
	if err != nil || out(r)["found"] != true || out(r)["face_match"] != false || out(r)["last_name"] != "DOE" {
		t.Errorf("nin face %v %v", r.Output, err)
	}
	r, err = call(t, "lookup_phone", map[string]any{"phone_number": "08082838283"}, "phone_advance")
	if err != nil || out(r)["nin"] != "67291452100" {
		t.Errorf("phone %v %v", r.Output, err)
	}
	r, err = call(t, "lookup_phone_basic", map[string]any{"phone_number": "08082838283"}, "phone_basic")
	if err != nil || out(r)["last_name"] != "ZAYNE" {
		t.Errorf("phone basic %v %v", r.Output, err)
	}
}

func TestDocuments(t *testing.T) {
	r, err := call(t, "verify_drivers_license", map[string]any{"license_number": "ABC12345YZ00", "first_name": "john", "last_name": "doe"}, "drivers_license")
	if err != nil || out(r)["verified"] != true || out(r)["last_name"] != "DOE" || out(r)["record"].(map[string]any)["expiry_date"] != "01-01-2029" {
		t.Errorf("licence %v %v", r.Output, err)
	}
	r, err = call(t, "verify_passport", map[string]any{"passport_number": "B50000947", "date_of_birth": "1996-09-30"}, "passport")
	if err != nil || out(r)["first_name"] != "JUDAH" {
		t.Errorf("passport %v %v", r.Output, err)
	}
	r, err = call(t, "verify_voters_card", map[string]any{"vin": "987F545AJ67890", "last_name": "smith", "date_of_birth": "1995-02-27", "state": "lagos"}, "voters_card_mismatch")
	if err != nil || out(r)["found"] != true || out(r)["verified"] != false {
		t.Errorf("voters %v %v", r.Output, err)
	}
	r, err = call(t, "lookup_cac", map[string]any{"rc_number": "092932"}, "cac_basic")
	if err != nil || out(r)["company_name"] != "TEST COMPANY" || out(r)["company_status"] != "Active" || out(r)["email"] != "test@test.com" {
		t.Errorf("cac %v %v", r.Output, err)
	}
	r, err = call(t, "lookup_cac", map[string]any{"rc_number": "000000", "company_type": "BN", "advanced": true}, "cac_advance_not_found")
	if err != nil || out(r)["found"] != false {
		t.Errorf("cac not found %v %v", r.Output, err)
	}
	r, err = call(t, "resolve_bank_account", map[string]any{"account_number": "4444444444", "bank_code": "214"}, "bank_account")
	if err != nil || out(r)["account_name"] != "Test Account" || out(r)["found"] != true {
		t.Errorf("bank %v %v", r.Output, err)
	}
}

func TestBiometrics(t *testing.T) {
	in := map[string]any{"image_one": "https://example.com/a.jpg", "image_two": "https://example.com/b.jpg"}
	r, err := call(t, "compare_faces", in, "face_match")
	if err != nil || out(r)["match"] != true || out(r)["confidence"] != float64(100) {
		t.Errorf("match %v %v", r.Output, err)
	}
	r, err = call(t, "compare_faces", in, "face_no_match")
	if err != nil || out(r)["match"] != false || out(r)["confidence"] != 21.4 {
		t.Errorf("no match is a result: %v %v", r.Output, err)
	}
	r, err = call(t, "check_liveness", map[string]any{"image": "https://example.com/a.jpg"}, "liveness")
	if err != nil || out(r)["live"] != true || out(r)["confidence"].(float64) < 99 {
		t.Errorf("liveness %v %v", r.Output, err)
	}
	r, err = call(t, "get_wallet_balance", nil, "wallet")
	if err != nil || out(r)["wallet"].(map[string]any)["data"].(map[string]any)["balance"] != "1500.00" {
		t.Errorf("wallet %v %v", r.Output, err)
	}
}

func TestWebhookSignature(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["verification"].Verify
	body := []byte(`{"status":true,"response_code":"00","data":{"widget_info":{"user_ref":"u1"}},"verification":{"status":"VERIFIED"}}`)
	m := hmac.New(sha256.New, []byte("pk_test_prembly"))
	m.Write(body)
	h := http.Header{}
	h.Set("x-prembly-signature", base64.StdEncoding.EncodeToString(m.Sum(nil)))
	if err := connector.VerifyWebhook(spec, "pk_test_prembly", h, body); err != nil {
		t.Errorf("valid: %v", err)
	}
	if connector.VerifyWebhook(spec, "pk_other", h, body) == nil {
		t.Error("wrong key accepted")
	}
	if connector.VerifyWebhook(spec, "pk_test_prembly", h, append(body, ' ')) == nil {
		t.Error("tampered body accepted")
	}
}

func TestTriggerExpressions(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["verification"]
	e := expr.MustNewTriggerEngine("body", "headers", "query")
	for _, c := range []struct {
		body                      string
		headers                   map[string]any
		event, dedup, correlation string
	}{
		{`{"status":true,"response_code":"00","data":{"widget_info":{"user_ref":"5846498649586495"}},"verification":{"status":"VERIFIED"}}`,
			map[string]any{"token": "tok_1"}, "VERIFIED", "tok_1", "5846498649586495"},
		{`{"status":true,"response_code":"00","data":{"firstName":"A"},"verification":{"status":"NOT-VERIFIED","reference":"9a7c"}}`,
			map[string]any{}, "NOT-VERIFIED", "9a7c", "9a7c"},
		{`{"status":true,"response_code":"00","confidence":100}`, map[string]any{"token": "tok_2"}, "UNSPECIFIED", "tok_2", ""},
	} {
		body, err := expr.DecodeJSON([]byte(c.body))
		if err != nil {
			t.Fatal(err)
		}
		act := map[string]any{"body": body, "headers": c.headers, "query": map[string]any{}}
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

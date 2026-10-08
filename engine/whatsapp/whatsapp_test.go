package whatsapp

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTokens(t *testing.T) {
	s := Signer{Key: bytes.Repeat([]byte{7}, 32)}
	now := time.Now()
	c := Claims{Tenant: uuid.New(), Nonce: NewNonce(), Purpose: PurposeDecide, Decision: "approved", Expires: time.Unix(now.Add(time.Hour).Unix(), 0),
		Run: uuid.New(), Step: "ok", Level: 1, User: uuid.New()}
	tok := s.Sign(c)
	if len(tok) > 128 {
		t.Fatalf("token is %d characters, more than a quick-reply payload takes", len(tok))
	}
	h, mac, err := ParseToken(tok)
	if err != nil || h.Tenant != c.Tenant || h.Nonce != c.Nonce || h.Decision != "approved" || h.Purpose != PurposeDecide || !h.Expires.Equal(c.Expires) {
		t.Fatalf("parse: %+v %v", h, err)
	}
	full := h
	full.Run, full.Step, full.Level, full.User = c.Run, c.Step, c.Level, c.User
	if err := s.Verify(full, mac, now); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Every bound claim matters.
	for name, alter := range map[string]func(*Claims){
		"run":      func(x *Claims) { x.Run = uuid.New() },
		"step":     func(x *Claims) { x.Step = "other" },
		"level":    func(x *Claims) { x.Level = 0 },
		"user":     func(x *Claims) { x.User = uuid.New() },
		"decision": func(x *Claims) { x.Decision = "rejected" },
		"tenant":   func(x *Claims) { x.Tenant = uuid.New() },
		"purpose":  func(x *Claims) { x.Purpose = PurposeHandoff },
		"expiry":   func(x *Claims) { x.Expires = x.Expires.Add(time.Hour) },
	} {
		x := full
		alter(&x)
		if s.Verify(x, mac, now) == nil {
			t.Errorf("altered %s still verifies", name)
		}
	}
	if s.Verify(full, mac, c.Expires) == nil {
		t.Error("expired token verifies")
	}
	if (Signer{Key: bytes.Repeat([]byte{8}, 32)}).Verify(full, mac, now) == nil {
		t.Error("another key verifies")
	}
	// A token re-signed with an altered decision byte fails.
	raw := []byte(tok)
	i := strings.Index(tok, ".") + 1
	raw[i+2] ^= 1
	if h2, mac2, err := ParseToken(string(raw)); err == nil {
		h2.Run, h2.Step, h2.Level, h2.User = c.Run, c.Step, c.Level, c.User
		if s.Verify(h2, mac2, now) == nil {
			t.Error("tampered header verifies")
		}
	}
	for _, bad := range []string{"", "tk1.", "tk1.abc.def", "tk2." + tok[4:], tok + "x"} {
		if _, _, err := ParseToken(bad); err == nil && bad != tok+"x" {
			t.Errorf("%q parses", bad)
		}
	}
}

func TestSignatureAndParse(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account","entry":[{"changes":[{"field":"messages","value":{"metadata":{"phone_number_id":"111"},
	  "messages":[{"from":"2348012345678","id":"wamid.1","type":"text","text":{"body":"status"}},
	              {"from":"2348012345678","id":"wamid.2","type":"interactive","interactive":{"type":"button_reply","button_reply":{"id":"tk1.x","title":"Approve"}}},
	              {"from":"2348012345678","id":"wamid.3","type":"button","button":{"payload":"tk1.y","text":"Reject"}}],
	  "statuses":[{"id":"wamid.9","status":"read"}]}},{"field":"account_update","value":{}}]}]}`)
	sig := Sign("s3cret", body)
	if !VerifySignature("s3cret", body, sig) {
		t.Fatal("own signature refused")
	}
	if VerifySignature("other", body, sig) || VerifySignature("s3cret", append(body, ' '), sig) || VerifySignature("s3cret", body, "sha1=00") || VerifySignature("", body, sig) {
		t.Fatal("bad signature accepted")
	}
	msgs, err := Parse(body)
	if err != nil || len(msgs) != 3 {
		t.Fatalf("%v %v", msgs, err)
	}
	if msgs[0].Text != "status" || msgs[1].Reply != "tk1.x" || msgs[2].Reply != "tk1.y" || msgs[2].PhoneNumberID != "111" {
		t.Errorf("%+v", msgs)
	}
}

func TestNumbersAndMasking(t *testing.T) {
	for in, want := range map[string]string{"+234 801 234 5678": "+2348012345678", "08012345678": "+2348012345678", "2348012345678": "+2348012345678",
		"00447700900123": "+447700900123", "(0801) 234-5678": "+2348012345678"} {
		if got, err := Normalise(in, "234"); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "abc", "+0123", "+12"} {
		if _, err := Normalise(bad, "234"); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if m := MaskNumber("+2348012345678"); m != "+234••••••5678" {
		t.Errorf("mask %q", m)
	}
	subject := map[string]any{
		"amount_kobo": 240000000, "recipient_name": "Adebayo Ogunleye", "account_number": "0123456789", "bank_code": "058",
		"bvn": "22212345678", "email": "ade@example.com", "phone": "+2348012345678", "api_token": "sk_live_abc",
		"note":   "call 08098765432 about card 4111111111111111",
		"sealed": map[string]any{"$pii": "account_number", "subject": "x", "ct": "..."},
	}
	lines := SummaryLines(subject, func(map[string]any) (any, error) { return "9876543210", nil }, 20)
	out := strings.Join(lines, "\n")
	for _, leak := range []string{"Adebayo", "0123456789", "22212345678", "ade@example", "8012345678", "sk_live", "08098765432", "4111111111111111", "9876543210"} {
		if strings.Contains(out, leak) {
			t.Errorf("summary leaks %q:\n%s", leak, out)
		}
	}
	for _, want := range []string{"amount_kobo: 240000000", "account_number: ••••6789", "sealed: ••••3210", "recipient_name: A•••", "api_token: [hidden]", "bvn: [bvn]"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
	if l := SummaryLines(subject, nil, 3); len(l) != 4 || !strings.HasPrefix(l[3], "… and") {
		t.Errorf("max: %v", l)
	}
}

func TestTemplates(t *testing.T) {
	seen := map[string]bool{}
	for _, tpl := range AllTemplates {
		if seen[tpl.Name] {
			t.Errorf("duplicate %s", tpl.Name)
		}
		seen[tpl.Name] = true
		vars := map[string]string{}
		for _, v := range tpl.Vars {
			vars[v] = "line one\nline two"
		}
		var payloads []string
		for _, b := range tpl.Buttons {
			if b.Type == "quick_reply" {
				payloads = append(payloads, "p")
			}
		}
		p, err := tpl.Payload("en", vars, payloads)
		if err != nil {
			t.Fatalf("%s: %v", tpl.Name, err)
		}
		raw, _ := json.Marshal(p)
		if strings.Contains(string(raw), `\n`) {
			t.Errorf("%s: a parameter has a new line: %s", tpl.Name, raw)
		}
		if strings.Contains(tpl.Render(vars), "{{") {
			t.Errorf("%s renders with a placeholder left", tpl.Name)
		}
	}
	if _, err := TplApprovalRequest.Payload("en", map[string]string{"tenant": "a", "title": "b", "summary": "c", "environment": "d"}, nil); err == nil {
		t.Error("approval template without payloads")
	}
}

func TestInputFields(t *testing.T) {
	schema := json.RawMessage(`{"$ref":"#/types/Pay"}`)
	types := map[string]json.RawMessage{"Pay": json.RawMessage(`{"type":"object","required":["amount","currency","account_number","urgent"],"properties":{
	  "amount":{"type":"integer","minimum":1,"title":"Amount (kobo)"},"currency":{"type":"string","enum":["NGN","USD"]},
	  "account_number":{"type":"string","pattern":"^[0-9]{10}$","x-pii":"account_number"},"urgent":{"type":"boolean"},"memo":{"type":"string"}}}`)}
	fs, err := InputFields(schema, types)
	if err != nil || len(fs) != 4 || fs[0].Name != "amount" || fs[2].PII != "account_number" {
		t.Fatalf("%+v %v", fs, err)
	}
	if _, err := fs[0].Parse("0"); err == nil {
		t.Error("amount 0 accepted")
	}
	if v, err := fs[0].Parse("5,000"); err != nil || v != int64(5000) {
		t.Errorf("amount: %v %v", v, err)
	}
	if v, err := fs[1].Parse("ngn"); err != nil || v != "NGN" {
		t.Errorf("enum: %v %v", v, err)
	}
	if _, err := fs[1].Parse("EUR"); err == nil {
		t.Error("EUR accepted")
	}
	if _, err := fs[2].Parse("123"); err == nil {
		t.Error("short account accepted")
	}
	if v, err := fs[3].Parse("Yes"); err != nil || v != true {
		t.Errorf("bool: %v %v", v, err)
	}
	if _, err := InputFields(json.RawMessage(`{"type":"object","required":["rows"],"properties":{"rows":{"type":"array"}}}`), nil); !errors.Is(err, ErrInputsUnsupported) {
		t.Errorf("array: %v", err)
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := map[string]string{}
	look := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	if c, err := ConfigFromEnv(look); c != nil || err != nil {
		t.Fatalf("off: %v %v", c, err)
	}
	env["TASKIEM_WHATSAPP_PHONE_NUMBER_ID"] = "111"
	if _, err := ConfigFromEnv(look); err == nil || !strings.Contains(err.Error(), "TASKIEM_WHATSAPP_ACCESS_TOKEN") {
		t.Fatalf("missing: %v", err)
	}
	env["TASKIEM_WHATSAPP_ACCESS_TOKEN"], env["TASKIEM_WHATSAPP_APP_SECRET"], env["TASKIEM_WHATSAPP_VERIFY_TOKEN"] = "t", "s", "v"
	env["TASKIEM_WHATSAPP_TOKEN_KEY"] = "c2hvcnQ="
	if _, err := ConfigFromEnv(look); err == nil {
		t.Fatal("short key accepted")
	}
	env["TASKIEM_WHATSAPP_TOKEN_KEY"] = "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="
	if c, err := ConfigFromEnv(look); err != nil || len(c.TokenKey) != 32 {
		t.Fatalf("%v %v", c, err)
	}
}

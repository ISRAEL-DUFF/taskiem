package ingest_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"github.com/israel-duff/taskiem/connectors/prembly"
)

const premblyFlow = `{"schema":"wd/v1","id":"wf_kyc","version":1,"name":"kyc",
  "trigger":{"type":"connector_event","config":{"connector":"prembly@1","trigger":"verification","events":["VERIFIED"]}},
  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.body.verification.status"}}]}`

// A Prembly delivery signed with the business's public key proves nothing
// (the widget is given that key in the browser), so it starts no run
// without the token Taskiem made for the connection in its URL (F55).
func TestPremblyVerificationNeedsURLToken(t *testing.T) {
	w := newWorld(t)
	if err := w.Registry.Register(prembly.New(prembly.Options{})); err != nil {
		t.Fatal(err)
	}
	const pk, tok = "pk_test_prembly", "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
	// The public key is stored too, so a signature with it would verify
	// if it were still trusted: it must not be enough.
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "prembly", "main", "api_key",
		map[string]string{"api_key": "sk_test_prembly", "public_key": pk, "callback_token": tok}, "test"); err != nil {
		t.Fatal(err)
	}
	wf := w.publish(t, premblyFlow)
	post := func(body []byte, query string) int {
		m := hmac.New(sha256.New, []byte(pk))
		m.Write(body)
		st, _ := w.post(t, "/connectors/prembly@1/verification?env=prod&connection=main"+query, body,
			"x-prembly-signature", base64.StdEncoding.EncodeToString(m.Sum(nil)))
		return st
	}
	forged := []byte(`{"status":true,"response_code":"00","data":{"widget_info":{"user_ref":"attacker"}},"verification":{"status":"VERIFIED","reference":"f1"}}`)
	if st := post(forged, ""); st != 401 {
		t.Errorf("signed with the public key, no token: %d, want 401", st)
	}
	if st := post(forged, "&token=guess"); st != 401 {
		t.Errorf("wrong token: %d, want 401", st)
	}
	if n := w.runCount(t, wf); n != 0 {
		t.Fatalf("%d runs started by refused deliveries", n)
	}
	genuine := []byte(`{"status":true,"response_code":"00","data":{"widget_info":{"user_ref":"u1"}},"verification":{"status":"VERIFIED","reference":"r1"}}`)
	if st := post(genuine, "&token="+tok); st != 202 {
		t.Errorf("right token: %d, want 202", st)
	}
	if n := w.runCount(t, wf); n != 1 {
		t.Errorf("runs: %d, want 1", n)
	}
}

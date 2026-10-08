package whatsapp

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

var update = flag.Bool("update", false, "rewrite the Flow JSON in docs/whatsapp-flows")

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestFlowCrypto(t *testing.T) {
	priv := testKey(t)
	key := NewFlowKey(priv)
	req := map[string]any{"version": "3.0", "action": "data_exchange", "screen": "INPUTS", "flow_token": "wf1.x", "data": map[string]any{"t1": "hello"}}
	body, open, err := EncryptFlowRequest(&priv.PublicKey, req)
	if err != nil {
		t.Fatal(err)
	}
	got, sess, err := key.Decrypt(body)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got.Action != "data_exchange" || got.Screen != "INPUTS" || got.FlowToken != "wf1.x" || got.Data["t1"] != "hello" || got.Version != "3.0" {
		t.Fatalf("request: %+v", got)
	}
	resp, err := sess.Encrypt(map[string]any{"screen": "SUCCESS"})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := open([]byte(resp))
	if err != nil || string(plain) != `{"screen":"SUCCESS"}` {
		t.Fatalf("response: %s %v", plain, err)
	}
	// The response is under the flipped IV: the request's IV does not open it.
	var env FlowEnvelope
	_ = json.Unmarshal(body, &env)
	iv, _ := base64.StdEncoding.DecodeString(env.InitialVector)
	if bytes.Equal(iv, flipped(iv)) || !bytes.Equal(iv, flipped(flipped(iv))) {
		t.Fatal("flipping")
	}

	// Tampering, another key, garbage: all refused as undecryptable.
	other := NewFlowKey(testKey(t))
	if _, _, err := other.Decrypt(body); !errors.Is(err, ErrFlowDecrypt) {
		t.Errorf("another key: %v", err)
	}
	ct, _ := base64.StdEncoding.DecodeString(env.EncryptedFlowData)
	ct[0] ^= 1
	bad := env
	bad.EncryptedFlowData = base64.StdEncoding.EncodeToString(ct)
	raw, _ := json.Marshal(bad)
	if _, _, err := key.Decrypt(raw); !errors.Is(err, ErrFlowDecrypt) {
		t.Errorf("altered ciphertext: %v", err)
	}
	for _, b := range []string{`{}`, `not json`, `{"encrypted_flow_data":"!!","encrypted_aes_key":"AA==","initial_vector":"AA=="}`, `{"version":"3.0","action":"ping"}`} {
		if _, _, err := key.Decrypt([]byte(b)); !errors.Is(err, ErrFlowDecrypt) {
			t.Errorf("%s: %v", b, err)
		}
	}
	if s := key.String() + key.GoString(); strings.Contains(s, priv.D.String()[:10]) || !strings.Contains(s, "redacted") {
		t.Errorf("the key prints: %s", s)
	}
}

func TestParseFlowKey(t *testing.T) {
	priv := testKey(t)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(priv)
	p8 := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))
	p1 := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}))
	for name, s := range map[string]string{"pkcs8": p8, "pkcs1": p1, "escaped": strings.ReplaceAll(p8, "\n", `\n`)} {
		k, err := ParseFlowKey(s)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(k.PublicPEM(), "BEGIN PUBLIC KEY") {
			t.Errorf("%s: public %q", name, k.PublicPEM())
		}
	}
	small, _ := rsa.GenerateKey(rand.Reader, 1024)
	sp := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(small)}))
	enc := "-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: DES-EDE3-CBC,0000000000000000\n\nAAAA\n-----END RSA PRIVATE KEY-----\n"
	for name, s := range map[string]string{"small": sp, "garbage": "nope", "encrypted": enc, "public": strings.ReplaceAll(NewFlowKey(priv).PublicPEM(), "PUBLIC", "PUBLIC")} {
		if _, err := ParseFlowKey(s); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// From the environment.
	env := map[string]string{"TASKIEM_WHATSAPP_PHONE_NUMBER_ID": "1", "TASKIEM_WHATSAPP_ACCESS_TOKEN": "t", "TASKIEM_WHATSAPP_APP_SECRET": "s",
		"TASKIEM_WHATSAPP_VERIFY_TOKEN": "v", "TASKIEM_WHATSAPP_TOKEN_KEY": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)),
		"TASKIEM_WHATSAPP_FLOWS_PRIVATE_KEY": p8}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	c, err := ConfigFromEnv(lookup)
	if err != nil || c.FlowKey == nil {
		t.Fatalf("config: %v", err)
	}
	env["TASKIEM_WHATSAPP_FLOWS_PRIVATE_KEY"] = "nope"
	if _, err := ConfigFromEnv(lookup); err == nil || strings.Contains(err.Error(), "nope") {
		t.Errorf("bad key: %v", err)
	}
}

func TestFlowForm(t *testing.T) {
	schema := `{"type":"object","required":["amount","account","currency","urgent","note"],"properties":{
	  "amount":{"type":"integer","minimum":1,"title":"Amount in naira"},
	  "account":{"type":"string","pattern":"^[0-9]{10}$","x-pii":"account_number","description":"Ten digits"},
	  "currency":{"type":"string","enum":["NGN","USD"]},
	  "urgent":{"type":"boolean"},
	  "note":{"type":"string","maxLength":5}}}`
	fields, err := InputFields(json.RawMessage(schema), nil)
	if err != nil {
		t.Fatal(err)
	}
	d, err := FlowForm("Pay supplier", fields, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Each field in its slot by order and kind; the rest hidden.
	want := map[string]any{"n1_on": true, "n1_label": "Amount in naira", "t2_on": true, "t2_help": "Ten digits", "c3_on": true, "c4_on": true, "t5_on": true,
		"t1_on": false, "c1_on": false, "n2_on": false, "t6_on": false, "has_error": false, "n1_req": true, "t1_req": false}
	for k, v := range want {
		if d[k] != v {
			t.Errorf("%s = %v, want %v", k, d[k], v)
		}
	}
	if opts := d["c3_opts"].([]FlowOption); len(opts) != 2 || opts[1] != (FlowOption{ID: "1", Title: "USD"}) {
		t.Errorf("enum options: %v", opts)
	}
	if opts := d["c4_opts"].([]FlowOption); len(opts) != 2 || opts[0].ID != "yes" {
		t.Errorf("boolean options: %v", opts)
	}
	// Every key the Flow JSON declares is in the data, and back.
	var flow struct {
		Screens []struct {
			Data map[string]any `json:"data"`
		} `json:"screens"`
	}
	if err := json.Unmarshal(InputsFlowJSON(), &flow); err != nil {
		t.Fatal(err)
	}
	for k := range flow.Screens[0].Data {
		if _, ok := d[k]; !ok {
			t.Errorf("declared but not sent: %s", k)
		}
	}
	for k := range d {
		if _, ok := flow.Screens[0].Data[k]; !ok {
			t.Errorf("sent but not declared: %s", k)
		}
	}

	// Submissions: parsed and checked against each field's schema.
	vals, errs := FlowValues(fields, map[string]any{"n1": "5000", "t2": "0123456789", "c3": "1", "c4": "no", "t5": "hi"})
	if len(errs) != 0 || vals["amount"] != int64(5000) || vals["account"] != "0123456789" || vals["currency"] != "USD" || vals["urgent"] != false || vals["note"] != "hi" {
		t.Fatalf("good: %v %v", vals, errs)
	}
	_, errs = FlowValues(fields, map[string]any{"n1": "0", "t2": "123", "c3": "7", "t5": "too long"})
	for _, f := range []string{"amount", "account", "currency", "urgent", "note"} {
		if errs[f] == "" {
			t.Errorf("no error for %s: %v", f, errs)
		}
	}
	// Numbers may come as numbers.
	if vals, errs := FlowValues(fields[:1], map[string]any{"n1": float64(12)}); len(errs) != 0 || vals["amount"] != int64(12) {
		t.Errorf("numeric: %v %v", vals, errs)
	}
	d, _ = FlowForm("x", fields, errs)
	if d["has_error"] != true || d["n1_err"] == "" {
		t.Errorf("errors shown: %v", d)
	}

	// Too many fields: chat instead.
	many := make([]Field, FlowSlots+1)
	if _, err := FlowForm("x", many, nil); !errors.Is(err, ErrFlowUnsupported) {
		t.Errorf("too many: %v", err)
	}
}

func TestFlowTokens(t *testing.T) {
	tenant := uuid.New()
	tok, hash := NewFlowToken(tenant)
	got, h2, err := ParseFlowToken(tok)
	if err != nil || got != tenant || !bytes.Equal(hash, h2) || len(hash) != 32 {
		t.Fatalf("parse: %v %v", got, err)
	}
	tok2, hash2 := NewFlowToken(tenant)
	if tok2 == tok || bytes.Equal(hash, hash2) {
		t.Error("tokens repeat")
	}
	for _, bad := range []string{"", "wf1.", "tk1." + tok[4:], "wf1.!!!", "wf1." + strings.Repeat("A", 80)} {
		if _, _, err := ParseFlowToken(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestFlowJSONDocumented(t *testing.T) {
	for name, gen := range map[string][]byte{FlowInputs: InputsFlowJSON(), FlowPin: PinFlowJSON()} {
		var v struct {
			Version string         `json:"version"`
			Routing map[string]any `json:"routing_model"`
			DataAPI string         `json:"data_api_version"`
			Screens []struct {
				ID       string `json:"id"`
				Terminal bool   `json:"terminal"`
				Layout   struct {
					Children []map[string]any `json:"children"`
				} `json:"layout"`
			} `json:"screens"`
		}
		if err := json.Unmarshal(gen, &v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if v.Version != FlowJSONVersion || v.DataAPI != "3.0" || len(v.Screens) != 1 || !v.Screens[0].Terminal || len(v.Screens[0].Layout.Children) > 50 {
			t.Errorf("%s: %+v", name, v)
		}
		last := v.Screens[0].Layout.Children[len(v.Screens[0].Layout.Children)-1]
		if last["type"] != "Footer" || last["on-click-action"].(map[string]any)["name"] != "data_exchange" {
			t.Errorf("%s: footer %v", name, last)
		}
		path := filepath.Join("..", "..", "docs", "whatsapp-flows", name+".json")
		if *update {
			if err := os.WriteFile(path, gen, 0o644); err != nil { //nolint:gosec // documentation
				t.Fatal(err)
			}
		}
		doc, err := os.ReadFile(path) //nolint:gosec // a fixed path
		if err != nil || !bytes.Equal(doc, gen) {
			t.Errorf("%s differs from the generated Flow JSON: go test ./engine/whatsapp -run TestFlowJSONDocumented -update", path)
		}
	}
}

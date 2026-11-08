package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/whatsapp"
	"github.com/israel-duff/taskiem/engine/media"
)

// A workflow that starts on WhatsApp messages needs its connection like a
// step does (decision 0062 C9), is told exactly what to set in Meta (C8),
// and the connection's check says when messages cannot be received for
// want of the app secret (C17).
func TestWhatsAppTriggerReadiness(t *testing.T) {
	w := newWorld(t)
	if err := w.env.Registry.Register(whatsapp.New(whatsapp.Options{})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Mama Put", "owner@mamaput.test")
	doc := `{"schema":"wd/v1","id":"wf_wa","version":1,"name":"wa",
	  "trigger":{"type":"connector_event","config":{"connector":"whatsapp@1","trigger":"messages","events":["message"]}},
	  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.item.message.from"}}]}`
	r := owner.must(200, "POST", "/v1/readiness", map[string]any{"definition": json.RawMessage(doc)})
	needs := needsOf(t, r["needs"])
	if c := needs["connection//prod"]; c == nil || c["problem"] != "missing" || c["blocking"] != true || c["connector"] != "whatsapp" {
		t.Fatalf("the trigger's connection: %v (all: %v)", c, r["needs"])
	}
	cb := needs["setup/whatsapp_callback/prod"]
	if cb == nil || cb["blocking"] != false || !strings.Contains(cb["message"].(string), "/connectors/whatsapp@1/messages?env=prod") {
		t.Fatalf("the callback note: %v", cb)
	}

	// Connected without the app secret: the check says messages cannot be
	// received; with it, nothing is said.
	conn := owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "whatsapp@1", "name": "main",
		"credentials": map[string]string{"access_token": "EAA1", "phone_number_id": "1065"}})
	// Said when it is saved, in plain words: the field's name, not its
	// explanation (review gaps 6, 15).
	if saved, _ := conn["warnings"].([]any); len(saved) != 1 || !strings.Contains(saved[0].(string), `"App secret"`) || strings.Contains(strings.ToLower(saved[0].(string)), "webhook") {
		t.Fatalf("saving warned %v", conn["warnings"])
	}
	r = owner.must(200, "POST", "/v1/readiness", map[string]any{"definition": json.RawMessage(doc)})
	if c := needsOf(t, r["needs"])["connection//prod"]; c != nil {
		t.Fatalf("still asks for the connection: %v", c)
	}
	chk := owner.must(200, "POST", "/v1/connections/"+conn["id"].(string)+"/check", nil)
	warn, _ := chk["warnings"].([]any)
	if len(warn) != 1 || !strings.Contains(warn[0].(string), "Messages from customers cannot be received yet") || !strings.Contains(warn[0].(string), "App secret") {
		t.Fatalf("warnings %v", chk)
	}
	if put := owner.must(200, "PUT", "/v1/connections/"+conn["id"].(string)+"/credentials", map[string]any{"credentials": map[string]string{"app_secret": "s3cret"}}); put["ok"] != true || put["warnings"] != nil {
		t.Fatalf("adding the secret answered %v", put)
	}
	if chk := owner.must(200, "POST", "/v1/connections/"+conn["id"].(string)+"/check", nil); chk["warnings"] != nil {
		t.Fatalf("warned with the secret set: %v", chk["warnings"])
	}
}

// A workflow that fetches files customers send cannot run on a Taskiem
// with no malware scanner: readiness says so, blocking, in plain words.
func TestFileScannerReadiness(t *testing.T) {
	w := newWorld(t)
	if err := w.env.Registry.Register(whatsapp.New(whatsapp.Options{})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Mama Put", "owner@mamaput.test")
	doc := `{"schema":"wd/v1","id":"wf_wa","version":1,"name":"wa","trigger":{"type":"manual"},
	  "steps":[{"id":"fetch","type":"connector","connector":"whatsapp@1","action":"get_media","input":{"media_id":"m1"}}]}`
	w.srv.Media = nil
	need := needsOf(t, owner.must(200, "POST", "/v1/readiness", map[string]any{"definition": json.RawMessage(doc)})["needs"])["setup/file_scanner/prod"]
	if need == nil || need["blocking"] != true || !strings.Contains(need["message"].(string), "cannot check files customers send") {
		t.Fatalf("no scanner: %v", need)
	}
	w.srv.Media = &media.Service{Scanner: okScanner{}}
	if need := needsOf(t, owner.must(200, "POST", "/v1/readiness", map[string]any{"definition": json.RawMessage(doc)})["needs"])["setup/file_scanner/prod"]; need != nil {
		t.Fatalf("with a scanner: %v", need)
	}
}

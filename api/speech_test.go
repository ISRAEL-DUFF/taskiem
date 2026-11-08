package api_test

import (
	"encoding/json"
	"testing"

	speechconn "github.com/israel-duff/taskiem/connectors/speech"
	"github.com/israel-duff/taskiem/engine/speech"
	"github.com/israel-duff/taskiem/engine/voice"
)

// A workflow that hears and speaks (speech@1, decision 0062 T7) needs the
// service on this Taskiem and the owner's Voice in workflows switch,
// which is separate from voice notes in Taskiem's own chat and cannot be
// turned on without a service.
func TestVoiceInWorkflows(t *testing.T) {
	w := newWorld(t)
	if err := w.env.Registry.Register(speechconn.New(nil)); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Mama Put", "owner@mamaput.test")
	doc := `{"schema":"wd/v1","id":"wf_voice","version":1,"name":"voice","trigger":{"type":"manual"},
	  "steps":[{"id":"hear","type":"connector","connector":"speech@1","action":"transcribe","input":{"file":"=trigger.body.file"}},
	    {"id":"say","type":"connector","connector":"speech@1","action":"speak","needs":["hear"],"input":{"text":"=steps.hear.output.text"}},
	    {"id":"sorry","type":"connector","connector":"speech@1","action":"cannot_hear","input":{"reason":"unavailable"}}]}`
	ready := func() map[string]map[string]any {
		return needsOf(t, owner.must(200, "POST", "/v1/readiness", map[string]any{"definition": json.RawMessage(doc)})["needs"])
	}
	n := ready()
	for _, k := range []string{"setup/speech_hear/prod", "setup/speech_speak/prod", "setting/voice_in_workflows/prod"} {
		if n[k] == nil || n[k]["blocking"] != true {
			t.Fatalf("%s: %v (all %v)", k, n[k], n)
		}
	}
	if steps := n["setting/voice_in_workflows/prod"]["steps"].([]any); len(steps) != 2 {
		t.Fatalf("cannot_hear needs no switch: %v", steps)
	}

	// No service: the owner cannot turn it on.
	body := map[string]any{"languages": []string{}, "default_language": "en", "voice_notes": false, "voice_in_workflows": true}
	owner.must(409, "PUT", "/v1/languages", body)
	v := owner.must(200, "GET", "/v1/languages", nil)
	if v["voice_in_workflows"] != false || v["speech_hear"] != false || v["speech_speak"] != false {
		t.Fatalf("view %v", v)
	}

	w.srv.Speech = &speech.Service{Pool: w.srv.Store.Pool, Hear: &voice.Fake{}, Say: &voice.FakeSpeaker{}, Voices: []string{"sage", "coral"}}
	v = owner.must(200, "PUT", "/v1/languages", body)
	if v["voice_in_workflows"] != true || v["voice_notes"] != false || v["speech_hear"] != true {
		t.Fatalf("on: %v", v)
	}
	if vs, _ := v["voices"].([]any); len(vs) != 2 || vs[0] != "sage" {
		t.Fatalf("voices %v", v["voices"])
	}
	// Changing languages without saying leaves it on.
	if v := owner.must(200, "PUT", "/v1/languages", map[string]any{"languages": []string{}, "default_language": "en", "voice_notes": false}); v["voice_in_workflows"] != true {
		t.Fatalf("left out, it went off: %v", v)
	}
	n = ready()
	for _, k := range []string{"setup/speech_hear/prod", "setup/speech_speak/prod", "setting/voice_in_workflows/prod"} {
		if n[k] != nil {
			t.Fatalf("still needs %s: %v", k, n[k])
		}
	}
	// Turning it off needs no service.
	w.srv.Speech = nil
	body["voice_in_workflows"] = false
	if v := owner.must(200, "PUT", "/v1/languages", body); v["voice_in_workflows"] != false {
		t.Fatalf("off: %v", v)
	}
}

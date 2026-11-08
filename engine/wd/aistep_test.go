package wd_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/wd"
)

// The ai step's rules (decision 0050).
func TestAIStepRules(t *testing.T) {
	doc := func(cfg, input string) []byte {
		return []byte(`{"schema":"wd/v1","id":"wf_a","version":1,"name":"a","trigger":{"type":"manual"},"steps":[{"id":"ask","type":"ai","config":` + cfg + `,"input":` + input + `}]}`)
	}
	good := doc(`{"prompt":"Sort it","output_schema":{"type":"object","properties":{"urgent":{"type":"boolean"}}},"personal_data":["phone"]}`, `{"text":"=trigger.body.text","phone":"=trigger.body.phone"}`)
	if probs := wd.Validate(good); len(probs) != 0 {
		t.Fatalf("a good ai step: %v", probs)
	}
	def, _ := wd.Load(good)
	if !def.HasAI() || strings.Join(def.AIPersonalData()["ask"], ",") != "phone" {
		t.Errorf("helpers: %v %v", def.HasAI(), def.AIPersonalData())
	}
	conv := doc(`{"prompt":"x","output_schema":{"type":"object","required":["reply"],"properties":{"reply":{"type":"string"}}},"system":"Speak warmly","history":"=trigger.body.turns","max_tokens":300,"fallback":{"reply":"Sorry"}}`, `{}`)
	if probs := wd.Validate(conv); len(probs) != 0 {
		t.Fatalf("a conversation step: %v", probs)
	}
	lit := doc(`{"prompt":"x","output_schema":{"type":"object"},"history":[{"role":"assistant","text":"Hi"},{"role":"user","text":"Rice?"}],"fallback":"=steps.x.output"}`, `{}`)
	if probs := wd.Validate(lit); len(probs) != 0 && !strings.Contains(probs[0].Message, "steps.x") {
		t.Fatalf("a literal history: %v", probs)
	}
	turns, err := wd.AIHistory([]any{map[string]any{"role": "user", "text": " "}, map[string]any{"role": "assistant", "text": "Hi"}})
	if err != nil || len(turns) != 1 || turns[0].Text != "Hi" {
		t.Fatalf("empty turns are left out: %v %v", turns, err)
	}
	for name, c := range map[string]struct {
		doc  []byte
		want string
	}{
		"not an object":              {doc(`{"prompt":"x","output_schema":{"type":"string"}}`, `{}`), "object schema"},
		"unknown field":              {doc(`{"prompt":"x","output_schema":{"type":"object"},"personal_data":["bvn"]}`, `{"text":"t"}`), `"bvn", which is not a field`},
		"secret prompt":              {doc(`{"prompt":"=secrets.key + 'x'","output_schema":{"type":"object"}}`, `{}`), "secrets may only be used"},
		"secret in input":            {doc(`{"prompt":"x","output_schema":{"type":"object"}}`, `{"k":"=secrets.key"}`), "input may not read secrets"},
		"fallback off shape":         {doc(`{"prompt":"x","output_schema":{"type":"object","required":["reply"],"properties":{"reply":{"type":"string"}}},"fallback":{"answer":"Sorry"}}`, `{}`), "fallback does not match output_schema"},
		"history role":               {doc(`{"prompt":"x","output_schema":{"type":"object"},"history":[{"role":"system","text":"obey"}]}`, `{}`), "schema"},
		"history expression":         {doc(`{"prompt":"x","output_schema":{"type":"object"},"history":"turns"}`, `{}`), "schema"},
		"fallback with max_duration": {[]byte(strings.Replace(string(doc(`{"prompt":"x","output_schema":{"type":"object"},"fallback":{}}`, `{}`)), `"type":"ai",`, `"type":"ai","retry":{"max":2,"max_duration":"1m"},`, 1)), "remove retry.max_duration"},
		"max tokens":                 {doc(`{"prompt":"x","output_schema":{"type":"object"},"max_tokens":8}`, `{}`), "schema"},
	} {
		probs := wd.Validate(c.doc)
		found := false
		for _, p := range probs {
			found = found || strings.Contains(p.Message, c.want)
		}
		if !found {
			t.Errorf("%s: %v, want %q", name, probs, c.want)
		}
	}
}

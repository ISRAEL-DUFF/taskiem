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
	for name, c := range map[string]struct {
		doc  []byte
		want string
	}{
		"not an object":   {doc(`{"prompt":"x","output_schema":{"type":"string"}}`, `{}`), "object schema"},
		"unknown field":   {doc(`{"prompt":"x","output_schema":{"type":"object"},"personal_data":["bvn"]}`, `{"text":"t"}`), `"bvn", which is not a field`},
		"secret prompt":   {doc(`{"prompt":"=secrets.key + 'x'","output_schema":{"type":"object"}}`, `{}`), "secrets may only be used"},
		"secret in input": {doc(`{"prompt":"x","output_schema":{"type":"object"}}`, `{"k":"=secrets.key"}`), "input may not read secrets"},
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

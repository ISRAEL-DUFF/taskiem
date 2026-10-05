package wd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDogfoodFlowsAreValid(t *testing.T) {
	files, _ := filepath.Glob("../../flows/**/*.wd.json")
	more, _ := filepath.Glob("../../flows/*.wd.json")
	files = append(files, more...)
	if len(files) < 3 {
		t.Fatalf("expected the three dogfood flows, found %d", len(files))
	}
	for _, f := range files {
		doc, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if probs := Validate(doc); len(probs) > 0 {
			t.Errorf("%s:\n  %v", f, probs)
		}
	}
}

// The example in the architecture spec must stay valid against the contract.
func TestSpecExampleIsValid(t *testing.T) {
	spec, err := os.ReadFile("../../docs/spec/architecture.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile("(?s)```json\n(\\{\\s*\"schema\": \"wd/v1\".*?)```").FindSubmatch(spec)
	if m == nil {
		t.Fatal("wd/v1 example not found in spec section 3.1")
	}
	doc := strings.Replace(string(m[1]), `"wf_01J9..."`, `"wf_01J9EXAMPLE"`, 1)
	if probs := Validate([]byte(doc)); len(probs) > 0 {
		t.Errorf("spec example: %v", probs)
	}
}

const base = `{"schema":"wd/v1","id":"wf_t","version":1,"name":"t",
 "trigger":{"type":"manual"},
 "steps":[%s]}`

func doc(steps string) []byte { return []byte(strings.Replace(base, "%s", steps, 1)) }

func TestValidMinimal(t *testing.T) {
	ok := doc(`{"id":"a","type":"transform","config":{"output":{"x":"=trigger.body.x"}}},
	           {"id":"b","type":"http","needs":["a"],"config":{"method":"POST","url":"https://x","headers":{"Authorization":"=secrets.token"},"class":"idempotent_write"}}`)
	if p := Validate(ok); len(p) > 0 {
		t.Fatalf("unexpected problems: %v", p)
	}
}

func TestInvalid(t *testing.T) {
	cases := map[string]struct {
		steps string
		want  string // substring of some problem
	}{
		"duplicate id": {
			`{"id":"a","type":"wait","config":{"duration":"1s"}},{"id":"a","type":"wait","config":{"duration":"1s"}}`,
			`step id "a" already defined`},
		"duplicate id nested": {
			`{"id":"a","type":"foreach","config":{"items":"=trigger.body.xs","steps":[{"id":"a","type":"wait","config":{"duration":"1s"}}]}}`,
			`step id "a" already defined`},
		"unknown need": {
			`{"id":"a","type":"wait","needs":["zz"],"config":{"duration":"1s"}}`,
			`unknown step "zz"`},
		"need out of scope": {
			`{"id":"a","type":"wait","config":{"duration":"1s"}},
			 {"id":"f","type":"foreach","config":{"items":"=trigger.body.xs","steps":[{"id":"b","type":"wait","needs":["a"],"config":{"duration":"1s"}}]}}`,
			`not in the same scope`},
		"self need": {
			`{"id":"a","type":"wait","needs":["a"],"config":{"duration":"1s"}}`,
			`needs itself`},
		"cycle": {
			`{"id":"a","type":"wait","needs":["c"],"config":{"duration":"1s"}},
			 {"id":"b","type":"wait","needs":["a"],"config":{"duration":"1s"}},
			 {"id":"c","type":"wait","needs":["b"],"config":{"duration":"1s"}}`,
			`cycle`},
		"secret in when": {
			`{"id":"a","type":"wait","when":"=secrets.x == 'y'","config":{"duration":"1s"}}`,
			`secrets may only be used`},
		"secret in transform output": {
			`{"id":"a","type":"transform","config":{"output":{"leak":"=secrets.key"}}}`,
			`secrets may only be used`},
		"secret in approval subject": {
			`{"id":"a","type":"approval","config":{"role":"x","subject":{"k":"=secrets.k"}}}`,
			`secrets may only be used`},
		"secret in effect seed": {
			`{"id":"a","type":"connector","connector":"paystack@1","action":"transfer","effect":{"idempotency_seed":"=secrets.k"}}`,
			`secrets may only be used`},
		"duplicate branch path": {
			`{"id":"b","type":"branch","config":{"paths":[
			   {"name":"p","when":"=true","steps":[{"id":"x","type":"wait","config":{"duration":"1s"}}]},
			   {"name":"p","when":"=true","steps":[{"id":"y","type":"wait","config":{"duration":"1s"}}]}]}}`,
			`duplicate path name`},
		"unclassified http write": {
			`{"id":"a","type":"http","config":{"method":"POST","url":"https://x"}}`,
			`schema:`},
		"http write classed as read": {
			`{"id":"a","type":"http","config":{"method":"DELETE","url":"https://x","class":"read"}}`,
			`schema:`},
		"unknown step field": {
			`{"id":"a","type":"wait","config":{"duration":"1s"},"bogus":1}`,
			`schema:`},
		"bad step id": {
			`{"id":"Bad-Id","type":"wait","config":{"duration":"1s"}}`,
			`schema:`},
		"connector without major": {
			`{"id":"a","type":"connector","connector":"paystack","action":"transfer"}`,
			`schema:`},
		"wait with both": {
			`{"id":"a","type":"wait","config":{"duration":"1s","until":"=run.started_at"}}`,
			`schema:`},
		"approval without policy or role": {
			`{"id":"a","type":"approval","config":{"timeout":"1h"}}`,
			`schema:`},
		"parallel with one branch": {
			`{"id":"p","type":"parallel","config":{"branches":[{"name":"x","steps":[{"id":"y","type":"wait","config":{"duration":"1s"}}]}]}}`,
			`schema:`},
		"bad duration": {
			`{"id":"a","type":"wait","config":{"duration":"5 minutes"}}`,
			`schema:`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			probs := Validate(doc(c.steps))
			for _, p := range probs {
				if strings.Contains(p.String(), c.want) {
					return
				}
			}
			t.Errorf("want a problem containing %q, got %v", c.want, probs)
		})
	}
}

func TestSecretInTriggerRejected(t *testing.T) {
	d := []byte(`{"schema":"wd/v1","id":"wf_t","version":1,"name":"t",
	  "trigger":{"type":"webhook","config":{"path":"/x","auth":"hmac","dedup":"=secrets.k"}},
	  "steps":[{"id":"a","type":"wait","config":{"duration":"1s"}}]}`)
	probs := Validate(d)
	if len(probs) == 0 || !strings.Contains(probs[0].Message, "secrets") {
		t.Errorf("got %v", probs)
	}
}

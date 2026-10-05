package connector

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestShippedManifestsAreValid(t *testing.T) {
	files, _ := filepath.Glob("../../connectors/*/manifest.yaml")
	if len(files) == 0 {
		t.Fatal("no manifests found")
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, probs := Parse(src); len(probs) > 0 {
			t.Errorf("%s:\n  %s", f, strings.Join(probs, "\n  "))
		}
	}
}

func TestSpecExampleIsValid(t *testing.T) {
	spec, err := os.ReadFile("../../docs/spec/architecture.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile("(?s)```yaml\n(manifest: connector/v1.*?)```").FindSubmatch(spec)
	if m == nil {
		t.Fatal("connector example not found in spec section 6.1")
	}
	// The spec example omits the check_balance action its auth test names.
	src := strings.Replace(string(m[1]), "actions:\n", "actions:\n  check_balance:\n    title: Check balance\n    class: read\n    input: { type: object }\n", 1)
	if _, probs := Parse([]byte(src)); len(probs) > 0 {
		t.Errorf("spec example:\n  %s", strings.Join(probs, "\n  "))
	}
}

const head = `manifest: connector/v1
id: demo
version: 1.0.0
name: Demo
category: other
auth: { type: none }
actions:
`

func TestInvalid(t *testing.T) {
	cases := map[string]struct{ actions, want string }{
		"write without class": {`
  pay: { title: Pay, input: { type: object } }`, "schema:"},
		"idempotent without idempotency": {`
  pay: { title: Pay, class: idempotent_write, input: { type: object } }`, "schema:"},
		"reconcilable without reconcile": {`
  pay: { title: Pay, class: reconcilable_write, input: { type: object } }`, "schema:"},
		"reconcile is not a read": {`
  pay: { title: Pay, class: reconcilable_write, reconcile: undo, input: { type: object } }
  undo: { title: Undo, class: unsafe_write, input: { type: object } }`, "must be a read action"},
		"reconcile unknown": {`
  pay: { title: Pay, class: reconcilable_write, reconcile: nope, input: { type: object } }`, `unknown action "nope"`},
		"compensate is a read": {`
  pay: { title: Pay, class: unsafe_write, compensate: get, input: { type: object } }
  get: { title: Get, class: read, input: { type: object } }`, "must be a write action"},
		"read with compensate": {`
  get: { title: Get, class: read, compensate: undo, input: { type: object } }
  undo: { title: Undo, class: unsafe_write, input: { type: object } }`, "cannot declare compensate"},
		"key too long for provider": {`
  pay:
    title: Pay
    class: idempotent_write
    idempotency: { field: ref, encoding: hex_lower, length: 64, limits: { max_length: 50 } }
    input: { type: object }`, "provider maximum"},
		"key alphabet outside provider charset": {`
  pay:
    title: Pay
    class: idempotent_write
    idempotency: { field: ref, encoding: base64url, length: 22, limits: { charset: "a-z0-9" } }
    input: { type: object }`, "alphabet"},
		"pii field missing": {`
  get: { title: Get, class: read, input: { type: object, properties: { a: { type: string } } }, pii: [b] }`, `"b" is not an input property`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, probs := Parse([]byte(head + strings.TrimPrefix(c.actions, "\n") + "\n"))
			for _, p := range probs {
				if strings.Contains(p, c.want) {
					return
				}
			}
			t.Errorf("want a problem containing %q, got %v", c.want, probs)
		})
	}
}

func TestWebhookTriggerNeedsVerify(t *testing.T) {
	src := head + "  get: { title: Get, class: read, input: { type: object } }\ntriggers:\n  ev: { type: webhook }\n"
	if _, probs := Parse([]byte(src)); len(probs) == 0 {
		t.Error("webhook trigger without verify accepted")
	}
}

package decide

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Decide records where the author's secret expressions are, and nothing
// for a value that only reads like one.
func TestSecretPathsAreTheAuthorsOnly(t *testing.T) {
	s := newSim(t, wdDoc(`
	  {"id":"pay","type":"connector","connector":"paystack@1","action":"transfer",
	   "input":{"auth":"=secrets.key","note":"=trigger.body.note","all":"=trigger.body","list":["=trigger.body.note",{"k":"='Bearer ' + secrets.key"}]},
	   "compensate":{"action":"refund","input":{"note":"=trigger.body.note"}}},
	  {"id":"post","type":"http","needs":["pay"],"config":{"method":"POST","url":"https://example.com","class":"unsafe_write","headers":{"a/b":"=secrets.key"},"body":{"n":"=trigger.body.note"}}},
	  {"id":"plain","type":"connector","connector":"paystack@1","action":"transfer","needs":["post"],"input":{"note":"=trigger.body.note"}}`, ""),
		map[string]any{"body": map[string]any{"note": "=secrets.key"}})

	p := s.payload(history.StepScheduled, "pay")
	if got := p["secret_paths"]; !reflect.DeepEqual(got, []any{"/auth", "/list/1/k"}) {
		t.Errorf("pay secret paths = %v", got)
	}
	in := p["input"].(map[string]any)
	if in["note"] != "=secrets.key" || in["all"].(map[string]any)["note"] != "=secrets.key" {
		t.Errorf("pay input = %v", in)
	}
	s.complete("pay", map[string]any{})
	// A key with a slash is escaped (RFC 6901).
	if got := s.payload(history.StepScheduled, "post")["secret_paths"]; !reflect.DeepEqual(got, []any{"/headers/a~1b"}) {
		t.Errorf("post secret paths = %v", got)
	}
	s.complete("post", map[string]any{})
	// No secret expression: no field at all.
	if _, ok := s.payload(history.StepScheduled, "plain")["secret_paths"]; ok {
		t.Errorf("a step without secret expressions records secret paths")
	}
	// A compensation's input may not read secrets (wd validation): its
	// data is data as well.
	s.failStep("plain", "fatal", "fail")
	c := s.payload(history.StepScheduled, history.CompensationPrefix+"pay")
	if _, ok := c["secret_paths"]; ok || c["input"].(map[string]any)["note"] != "=secrets.key" {
		t.Errorf("compensation payload = %v: %s", c, s.types())
	}
}

// A history written before secret paths were recorded still verifies, and
// only that difference is let through.
func TestVerifyAcceptsHistoriesBeforeSecretPaths(t *testing.T) {
	s := newSim(t, wdDoc(`
	  {"id":"pay","type":"connector","connector":"paystack@1","action":"transfer","input":{"auth":"=secrets.key","note":"=trigger.body.note"}}`, ""),
		map[string]any{"body": map[string]any{"note": "=secrets.key"}})
	s.complete("pay", map[string]any{})
	if err := Verify(s.def, s.h); err != nil {
		t.Fatalf("the history as written: %v", err)
	}
	strip := func(mutate func(m map[string]any)) []history.Event {
		h := append([]history.Event(nil), s.h...)
		for i, e := range h {
			if e.Type == history.StepScheduled && e.StepID == "pay" {
				var m map[string]any
				if err := json.Unmarshal(e.Payload, &m); err != nil {
					t.Fatal(err)
				}
				mutate(m)
				h[i].Payload, _ = json.Marshal(m)
			}
		}
		return h
	}
	if err := Verify(s.def, strip(func(m map[string]any) { delete(m, "secret_paths") })); err != nil {
		t.Errorf("a history without secret paths: %v", err)
	}
	if err := Verify(s.def, strip(func(m map[string]any) { m["secret_paths"] = []any{"/auth", "/note"} })); err == nil {
		t.Errorf("a history with a place decide did not record verifies")
	}
	if err := Verify(s.def, strip(func(m map[string]any) {
		delete(m, "secret_paths")
		m["input"].(map[string]any)["note"] = "other"
	})); err == nil {
		t.Errorf("a history without secret paths and another input verifies")
	}
}

// A capability's mapping moves its input about. The places follow the
// author's expressions and never a value that reads the same.
func TestMappedSecretPaths(t *testing.T) {
	mapping := map[string]any{
		"auth":    "=input.token",
		"ref":     "=input.note",
		"nested":  map[string]any{"again": "=input.token", "list": []any{"=input.note", "=input.token"}},
		"joined":  "='Bearer ' + input.token",
		"literal": "x",
		"step":    "=steps.a.output.v",
	}
	// The note is data that reads exactly as the author's expression does.
	in := map[string]any{"token": "=secrets.key", "note": "=secrets.key"}
	steps := map[string]any{"a": map[string]any{"output": map[string]any{"v": "=secrets.key"}}}
	s := &wd.Step{ID: "pay", Type: "connector", Input: map[string]any{"token": "=secrets.key", "note": "=trigger.body.note"},
		Capability: &wd.Expanded{Uses: "payout", Input: mapping, Steps: map[string]string{"a": "a"}}}
	out, paths, cs, err := capabilityInput(s, in, []string{"/token"}, map[string]any{"steps": steps})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/auth", "/nested/again", "/nested/list/1"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
	if m := out.(map[string]any); m["ref"] != "=secrets.key" || m["joined"] != "Bearer =secrets.key" || m["step"] != "=secrets.key" {
		t.Errorf("mapped input = %v", out)
	}
	// An older payload: the same places, found from the step as authored.
	old := history.ScheduledPayload{Kind: history.KindTask, Input: out, Capability: cs}
	if got := AuthoredSecretPaths(s, old); !reflect.DeepEqual(got, want) {
		t.Errorf("authored paths = %v, want %v", got, want)
	}
	// No kept expression, no places, whatever the data reads like.
	if _, paths, _, _ := capabilityInput(s, in, nil, map[string]any{"steps": steps}); paths != nil {
		t.Errorf("paths without a kept expression = %v", paths)
	}
}

// For a payload without the list, the places come from the step as
// authored and only where the payload still holds the same string.
func TestAuthoredSecretPaths(t *testing.T) {
	def, err := wd.Load([]byte(wdDoc(`
	  {"id":"pay","type":"connector","connector":"paystack@1","action":"transfer",
	   "input":{"auth":"=secrets.key","note":"=trigger.body.note","list":["=secrets.other"]},
	   "compensate":{"action":"refund","input":{"note":"=trigger.body.note"}}},
	  {"id":"post","type":"http","needs":["pay"],"config":{"method":"POST","url":"='https://' + secrets.host","class":"unsafe_write","headers":{"Authorization":"=secrets.key"},"query":{"k":"=secrets.key"},"body":{"n":"=trigger.body.note","t":"=secrets.key"}}},
	  {"id":"ask","type":"ai","needs":["post"],"config":{"prompt":"=trigger.body.note","output_schema":{"type":"object"}},"input":{"note":"=trigger.body.note"}}`, "")))
	if err != nil {
		t.Fatal(err)
	}
	data := "=secrets.key"
	for _, c := range []struct {
		step string
		p    history.ScheduledPayload
		want []string
	}{
		{"pay", history.ScheduledPayload{Input: map[string]any{"auth": "=secrets.key", "note": data, "list": []any{"=secrets.other"}, "extra": data}}, []string{"/auth", "/list/0"}},
		// A place that no longer holds the authored string is not one.
		{"pay", history.ScheduledPayload{Input: map[string]any{"auth": "=secrets.other", "note": data}}, nil},
		// A compensation is read from its own input, not the step's.
		{"pay", history.ScheduledPayload{Compensates: "pay", Input: map[string]any{"note": data, "auth": "=secrets.key"}}, nil},
		{"post", history.ScheduledPayload{Input: map[string]any{"method": "POST", "url": "='https://' + secrets.host", "headers": map[string]any{"Authorization": "=secrets.key"},
			"query": map[string]any{"k": "=secrets.key"}, "body": map[string]any{"n": data, "t": "=secrets.key"}}}, []string{"/body/t", "/headers/Authorization", "/query/k", "/url"}},
		{"ask", history.ScheduledPayload{Input: map[string]any{"prompt": data, "input": map[string]any{"note": data}}}, nil},
	} {
		if got := AuthoredSecretPaths(def.Step(c.step), c.p); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: paths = %v, want %v", c.step, got, c.want)
		}
	}
	if got := AuthoredSecretPaths(nil, history.ScheduledPayload{Input: map[string]any{"a": data}}); got != nil {
		t.Errorf("no step: %v", got)
	}
}

// What decide writes for a step without secret expressions is byte for
// byte what it wrote before the field existed.
func TestNoSecretPathsFieldWithoutSecrets(t *testing.T) {
	s := newSim(t, wdDoc(`{"id":"pay","type":"connector","connector":"paystack@1","action":"transfer","input":{"note":"=trigger.body.note"}}`, ""),
		map[string]any{"body": map[string]any{"note": "=secrets.key"}})
	for _, e := range s.h {
		if e.Type == history.StepScheduled && bytes.Contains(e.Payload, []byte("secret_paths")) {
			t.Errorf("payload = %s", strings.TrimSpace(string(e.Payload)))
		}
	}
}

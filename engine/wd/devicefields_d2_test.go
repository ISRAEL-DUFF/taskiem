package wd_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/wd"
)

// countByScan is a workflow the phone's home-screen scan can start (device
// fields §7, D2): a scan among its top-level inputs, started by hand.
const countByScan = `{"schema":"wd/v1","id":"wf_count","version":1,"name":"Count an item",
 "trigger":{"type":"manual"},
 "inputs":{"schema":{"type":"object","required":["item","count"],"properties":{
  "item":{"type":"scan","why":"the code on the item's pack","formats":["ean_13","upc_a"]},
  "count":{"type":"integer","minimum":0}}}},
 "steps":[{"id":"n","type":"transform","config":{"output":{}}}]}`

func TestScanInput(t *testing.T) {
	if p := wd.Validate([]byte(countByScan)); len(p) != 0 {
		t.Fatalf("a scan input: %v", p)
	}
	d, err := wd.Load([]byte(countByScan))
	if err != nil {
		t.Fatal(err)
	}
	name, p, ok := wd.ScanInput(d)
	if !ok || name != "item" || !wd.ScanTakes(p, "ean_13") || wd.ScanTakes(p, "qr") || !wd.ScanTakes(p, "") {
		t.Fatalf("ScanInput: %q %v %v", name, p, ok)
	}
	// As a tool for members, too.
	tool := strings.Replace(countByScan, `"trigger":{"type":"manual"},`, `"trigger":{"type":"manual"},"tool":{"command":"count","name":"Count an item","about":"Count an item by its code"},`, 1)
	if p := wd.Validate([]byte(tool)); len(p) != 0 {
		t.Fatalf("a tool's scan input: %v", p)
	}

	for _, c := range []struct{ from, to, want string }{
		{`"why":"the code on the item's pack",`, ``, `"item" asks for a scan: say why`},
		{`"formats":["ean_13","upc_a"]`, `"formats":["pdf417"]`, "pdf417 is not a code format"},
		{`"count":{"type":"integer","minimum":0}`, `"count":{"type":"scan","why":"again"}`, "at most one scan"},
		{`"count":{"type":"integer","minimum":0}`, `"count":{"type":"object","properties":{"inner":{"type":"scan","why":"x"}}}`, "one of its top-level inputs"},
		{`"count":{"type":"integer","minimum":0}`, `"count":{"type":"signature"}`, `"count" asks for a signature: say why`},
		{`"trigger":{"type":"manual"},`, `"trigger":{"type":"manual"},"tool":{"command":"count","name":"Count","about":"Count","for":"customers"},`, "a tool for customers cannot take a scan"},
		{`"trigger":{"type":"manual"},`, `"trigger":{"type":"form","config":{"title":"Count"}},`, "one of its top-level inputs, in a workflow started by hand"},
	} {
		doc := strings.Replace(countByScan, c.from, c.to, 1)
		if doc == countByScan {
			t.Fatalf("%q not in the fixture", c.from)
		}
		if p := fmtProblems(wd.Validate([]byte(doc))); !strings.Contains(p, c.want) {
			t.Errorf("%s → %s: %s, want %q", c.from, c.to, p, c.want)
		}
	}
}

func TestScanInputValues(t *testing.T) {
	d, err := wd.Load([]byte(countByScan))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ body, want string }{
		{`{"item":{"text":"6151100093217","format":"ean_13"},"count":3}`, ""},
		{`{"item":"6151100093217","count":3}`, ""},
		{`{"item":{"state":"refused","reason":"no camera"},"count":3}`, ""},
		{`{"item":{"text":"6151100093217","format":"qr"},"count":3}`, "does not take qr codes"},
		{`{"item":{"text":"61511\u000100093217","format":"ean_13"},"count":3}`, "no control characters"},
		{`{"item":{"text":"6151100093217","format":"ean_13","extra":1},"count":3}`, "item"},
		{`{"item":"` + strings.Repeat("7", 2001) + `","count":3}`, "item"},
	} {
		got := strings.Join(d.ValidateInputs([]byte(c.body)), "; ")
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", c.body, got, c.want)
		}
	}
	// allow_typed false: a code typed is refused.
	strict, _ := wd.Load([]byte(strings.Replace(countByScan, `"formats":["ean_13","upc_a"]`, `"formats":["ean_13"],"allow_typed":false`, 1)))
	if got := strings.Join(strict.ValidateInputs([]byte(`{"item":"6151100093217","count":3}`)), "; "); !strings.Contains(got, "a code typed is not taken") {
		t.Errorf("typed: %q", got)
	}

	// Read as the run keeps it: the source from whether a format came,
	// never from the body.
	body := map[string]any{"item": map[string]any{"text": "6151100093217", "format": "ean_13", "source": "typed"}, "count": 3}
	if !d.ReadScanInput(body) {
		t.Fatal("not read")
	}
	if it := body["item"].(map[string]any); it["source"] != "camera" || it["state"] != "answered" || it["format"] != "ean_13" {
		t.Fatalf("scanned: %v", it)
	}
	body = map[string]any{"item": "6151100093217"}
	d.ReadScanInput(body)
	if it := body["item"].(map[string]any); it["source"] != "typed" || it["text"] != "6151100093217" || it["format"] != nil {
		t.Fatalf("typed: %v", it)
	}
	body = map[string]any{"item": map[string]any{"text": "x", "source": "camera"}}
	d.ReadScanInput(body)
	if it := body["item"].(map[string]any); it["source"] != "typed" {
		t.Fatalf("a source in the body: %v", it)
	}
}

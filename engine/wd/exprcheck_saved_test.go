package wd

import (
	"strings"
	"testing"
)

// A saved thing is read by its literal name, by steps and outputs only
// (saved objects S3).
func TestSavedExpressionsChecked(t *testing.T) {
	doc := func(settings, step string) []byte {
		return []byte(`{"schema":"wd/v1","id":"wf_x","version":1,"name":"x","trigger":{"type":"manual"},"settings":` + settings + `,
		  "steps":[{"id":"a","type":"transform","config":{"output":{"v":"` + step + `"}}}]}`)
	}
	problems := func(b []byte) string {
		d, err := Load(b)
		if err != nil {
			return err.Error()
		}
		var out []string
		for _, p := range checkExpressions(d) {
			out = append(out, p.Path+": "+p.Message)
		}
		return strings.Join(out, "\n")
	}
	if p := problems(doc(`{}`, `=saved.price_list.body`)); p != "" {
		t.Errorf("named: %s", p)
	}
	if p := problems(doc(`{}`, `=saved[trigger.body.n].body`)); !strings.Contains(p, "read by its name") {
		t.Errorf("unnamed: %q", p)
	}
	if p := problems(doc(`{"concurrency_key":"=saved.x.k"}`, `=1`)); !strings.Contains(p, "settings.concurrency_key") {
		t.Errorf("concurrency key: %q", p)
	}
}

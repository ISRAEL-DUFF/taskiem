package wdtest_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtest"
)

// A workflow that reads saved things (saved objects S3) runs in a test:
// with the case's own, or a sample of each it does not give.
func TestCaseReadsSaved(t *testing.T) {
	def, err := wd.Load([]byte(`{"schema":"wd/v1","id":"wf_s","version":1,"name":"s","trigger":{"type":"manual"},
	  "steps":[{"id":"a","type":"transform","config":{"output":{"text":"=saved.price_list.body","to":"=saved.mum.phone"}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	c := wdtest.Case{Name: "given", Trigger: map[string]any{"body": map[string]any{}},
		Saved:  map[string]any{"price_list": map[string]any{"body": "Rice: 78,000"}},
		Expect: wdtest.Expect{Status: "completed", Outputs: map[string]any{"a": map[string]any{"text": "Rice: 78,000", "to": "sample-phone"}}}}
	if r := wdtest.RunCase(def, nil, nil, c); r.Status != "completed" || len(r.Failures) > 0 {
		t.Fatalf("%+v", r)
	}
	if got := wdtest.SampleSaved("x"); !strings.HasPrefix(got["title"].(string), "Sample") {
		t.Errorf("sample: %v", got)
	}
}

package wdmerge_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/wdmerge"
)

const base = `{"schema":"wd/v1","id":"wf_m","version":1,"name":"M","trigger":{"type":"manual"},"steps":[
  {"id":"a","type":"transform","config":{"output":1}},
  {"id":"b","type":"transform","needs":["a"],"config":{"output":2}},
  {"id":"c","type":"transform","needs":["b"],"config":{"output":3}}]}`

// edit applies replacements to the base document.
func edit(pairs ...string) []byte {
	s := base
	for i := 0; i < len(pairs); i += 2 {
		if !strings.Contains(s, pairs[i]) {
			panic("no " + pairs[i])
		}
		s = strings.Replace(s, pairs[i], pairs[i+1], 1)
	}
	return []byte(s)
}

func steps(t *testing.T, doc []byte) map[string]any {
	t.Helper()
	var d struct {
		Name  string           `json:"name"`
		Steps []map[string]any `json:"steps"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		t.Fatal(err)
	}
	out := map[string]any{"name": d.Name}
	var ids []string
	for _, s := range d.Steps {
		out[s["id"].(string)] = s["config"].(map[string]any)["output"]
		ids = append(ids, s["id"].(string))
	}
	out["order"] = strings.Join(ids, ",")
	return out
}

func TestEditsToDifferentStepsMerge(t *testing.T) {
	ours := edit(`"output":1}`, `"output":10}`, `"name":"M"`, `"name":"M2"`)
	theirs := edit(`"output":3}`, `"output":30}`,
		`{"id":"c"`, `{"id":"d","type":"transform","config":{"output":4}},{"id":"c"`)
	merged, conflicts, err := wdmerge.Merge([]byte(base), ours, theirs, nil)
	if err != nil || len(conflicts) > 0 {
		t.Fatal(err, conflicts)
	}
	got := steps(t, merged)
	if got["a"] != 10.0 || got["c"] != 30.0 || got["d"] != 4.0 || got["name"] != "M2" || got["order"] != "a,b,d,c" {
		t.Errorf("merged: %v", got)
	}
}

func TestDeletionAndAdditionMerge(t *testing.T) {
	ours := edit(`,
  {"id":"c","type":"transform","needs":["b"],"config":{"output":3}}`, ``) // ours deletes c
	theirs := edit(`{"id":"b"`, `{"id":"x","type":"transform","config":{"output":9}},{"id":"b"`)
	merged, conflicts, err := wdmerge.Merge([]byte(base), ours, theirs, nil)
	if err != nil || len(conflicts) > 0 {
		t.Fatal(err, conflicts)
	}
	if got := steps(t, merged); got["order"] != "a,x,b" {
		t.Errorf("merged: %v", got)
	}
}

func TestSameStepChangedBothWaysConflicts(t *testing.T) {
	ours := edit(`"output":2}`, `"output":20}`)
	theirs := edit(`"output":2}`, `"output":200}`, `"trigger":{"type":"manual"}`, `"trigger":{"type":"manual","config":{}}`)
	_, conflicts, err := wdmerge.Merge([]byte(base), ours, theirs, nil)
	if err != nil || len(conflicts) != 1 || conflicts[0].Path != "steps/b" || conflicts[0].Kind != "both_changed" {
		t.Fatalf("%v %+v", err, conflicts)
	}
	if !strings.Contains(string(conflicts[0].Ours), `"output":20`) || !strings.Contains(string(conflicts[0].Theirs), `"output":200`) {
		t.Errorf("conflict: %s / %s", conflicts[0].Ours, conflicts[0].Theirs)
	}
	merged, conflicts, err := wdmerge.Merge([]byte(base), ours, theirs, map[string]wdmerge.Resolution{"steps/b": wdmerge.Ours})
	if err != nil || len(conflicts) != 0 || steps(t, merged)["b"] != 20.0 || !strings.Contains(string(merged), `"config":{}`) {
		t.Errorf("resolved: %s %v %v", merged, conflicts, err)
	}
}

func TestChangedAndDeletedConflicts(t *testing.T) {
	ours := edit(`"output":3}`, `"output":33}`)
	theirs := edit(`,
  {"id":"c","type":"transform","needs":["b"],"config":{"output":3}}`, ``)
	_, conflicts, _ := wdmerge.Merge([]byte(base), ours, theirs, nil)
	if len(conflicts) != 1 || conflicts[0].Kind != "changed_and_deleted" || conflicts[0].Theirs != nil {
		t.Errorf("%+v", conflicts)
	}
	merged, _, _ := wdmerge.Merge([]byte(base), ours, theirs, map[string]wdmerge.Resolution{"steps/c": wdmerge.Theirs})
	if steps(t, merged)["order"] != "a,b" {
		t.Errorf("taking the deletion: %s", merged)
	}
}

func TestSameEditOnBothSidesIsNotAConflict(t *testing.T) {
	both := edit(`"output":2}`, `"output":5}`)
	merged, conflicts, err := wdmerge.Merge([]byte(base), both, both, nil)
	if err != nil || len(conflicts) > 0 || steps(t, merged)["b"] != 5.0 {
		t.Errorf("%s %v %v", merged, conflicts, err)
	}
}

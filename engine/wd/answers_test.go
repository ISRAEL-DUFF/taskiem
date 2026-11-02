package wd_test

import (
	"slices"
	"testing"

	"github.com/israel-duff/taskiem/engine/wd"
)

// AnswerFields: the questions of the storefront action's form a definition
// reads, by select or index, at any depth; reading trigger.answers whole,
// or trigger.body, reads none (fronts F1e).
func TestAnswerFields(t *testing.T) {
	d, err := wd.Load([]byte(`{"schema":"wd/v1","id":"wf_food","version":1,"name":"Food",
 "trigger":{"type":"storefront","config":{"action":"order"}},
 "steps":[{"id":"note","type":"transform","config":{"output":{"size":"=trigger.answers.size","all":"=trigger.answers","ref":"=trigger.body.reference"}}},
  {"id":"spicy","type":"branch","config":{"paths":[{"name":"hot","when":"=trigger.answers[\"pepper\"] == 'hot'","steps":[
   {"id":"tell","type":"transform","config":{"output":{"t":"=trigger.submission.reference + trigger.answers.note"}}}]}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := wd.AnswerFields(d); !slices.Equal(got, []string{"note", "pepper", "size"}) {
		t.Fatalf("answer fields: %v", got)
	}
}

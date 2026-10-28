package api

import (
	"encoding/json"
	"errors"
	"testing"
)

// A draft may be incomplete, but it is a document the builder can draw: keys
// as written, and step lists of steps at every depth.
func TestDraftDocShapes(t *testing.T) {
	ok := []string{
		`{"steps":[]}`,
		`{"id":"wf_a","steps":[{"id":"a","type":"transform"}]}`,
		`{"steps":[{"id":"c","type":"branch","config":{"paths":[{"name":"yes","steps":[{"id":"d","type":"transform"}]}],"default":{"steps":[{"id":"e","type":"transform"}]}}}]}`,
		`{"steps":[{"id":"p","type":"parallel","config":{"branches":[{"steps":[{"id":"q","type":"wait","needs":[]}]}]}}]}`,
		`{"steps":[{"id":"f","type":"foreach","config":{"steps":[{"id":"g","type":"transform"}]}}]}`,
		`{"steps":[{"id":"c","type":"branch","config":{"paths":[{"name":"yes"}]}}]}`,
	}
	for _, doc := range ok {
		if _, err := draftDoc(json.RawMessage(doc)); err != nil {
			t.Errorf("%s: refused: %v", doc, err)
		}
	}
	bad := []string{
		`[]`,
		`{}`,
		`{"Steps":[]}`,
		`{"steps":[],"Trigger":{"type":"manual"}}`,
		`{"steps":{}}`,
		`{"steps":[1]}`,
		`{"steps":[{"type":"transform"}]}`,
		`{"steps":[{"ID":"a","type":"transform"}]}`,
		`{"steps":[{"id":"a"}]}`,
		`{"steps":[{"id":"a","type":null}]}`,
		`{"steps":null}`,
		`{"steps":[{"id":"a","Type":"transform"}]}`,
		`{"steps":[{"id":"a","type":"transform","needs":"b"}]}`,
		`{"steps":[{"id":"a","type":"transform","config":[]}]}`,
		`{"steps":[{"id":"c","type":"branch","config":{"paths":{}}}]}`,
		`{"steps":[{"id":"c","type":"branch","config":{"paths":[null]}}]}`,
		`{"steps":[{"id":"c","type":"branch","config":{"paths":[{"name":"yes","steps":[null]}]}}]}`,
		`{"steps":[{"id":"c","type":"branch","config":{"default":[]}}]}`,
		`{"steps":[{"id":"c","type":"branch","config":{"default":{"steps":[{"type":"x"}]}}}]}`,
		`{"steps":[{"id":"p","type":"parallel","config":{"branches":[{"steps":"x"}]}}]}`,
		`{"steps":[{"id":"f","type":"foreach","config":{"steps":[{"id":"g"}]}}]}`,
		`{"steps":[{"id":"f","type":"foreach","config":{"steps":[{"id":"h","type":"foreach","config":{"steps":[7]}}]}}]}`,
	}
	for _, doc := range bad {
		if _, err := draftDoc(json.RawMessage(doc)); !errors.Is(err, errBadRequest) {
			t.Errorf("%s: accepted (err %v)", doc, err)
		}
	}
}

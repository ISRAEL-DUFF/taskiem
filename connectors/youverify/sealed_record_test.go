package youverify_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/connectors/youverify"
	"github.com/israel-duff/taskiem/engine/connector"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// The provider's whole record is declared personal (output.record, an
// object). Declaring an object used to seal nothing, so the record (names,
// date of birth, phone, ID numbers) was stored in run history readable but
// for what the detectors happened to catch. Run through the engine with
// the recorded answer: nothing under the record is readable as stored,
// and a reader with the key gets the record back whole.
func TestTheRecordIsSealedInRunHistory(t *testing.T) {
	srv := fixture.Serve(t, fixture.Load(t, "bvn"))
	e := rt.New(t)
	run := e.RunConnectorStep(t, youverify.New(youverify.Options{BaseURL: srv.URL}), map[string]string{"secret_key": "yv_test_key"},
		"verify_bvn", map[string]any{"bvn": "11111111111", "subject_consent": true, "premium": true})
	if run.Status != "completed" {
		t.Fatalf("status %s: %+v", run.Status, run.Failures)
	}
	stored, opened := e.StepOutputs(t, run)
	rec, _ := stored.(map[string]any)["record"].(map[string]any)
	if len(rec) == 0 {
		t.Fatalf("no record stored: %v", stored)
	}
	if open := rt.Readable(rec); len(open) > 0 {
		t.Errorf("readable in the stored record without a key: %v", open)
	}
	// The declared fields beside it are sealed as before.
	for _, k := range []string{"first_name", "last_name", "date_of_birth", "mobile"} {
		if v, ok := stored.(map[string]any)[k]; ok && v != nil && len(rt.Readable(v)) > 0 {
			t.Errorf("%s is readable as stored: %v", k, v)
		}
	}
	// Opened, it is the record the connector answered.
	srv2 := fixture.Serve(t, fixture.Load(t, "bvn"))
	c := youverify.New(youverify.Options{BaseURL: srv2.URL})
	direct, err := c.Actions["verify_bvn"].Execute(context.Background(), connector.Request{Input: map[string]any{"bvn": "11111111111", "subject_consent": true, "premium": true}, HTTP: srv2.Client(),
		Credentials: map[string]string{"secret_key": "yv_test_key"}})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(direct.Output.(map[string]any)["record"])
	got, _ := json.Marshal(opened.(map[string]any)["record"])
	var w, g any
	_ = json.Unmarshal(want, &w)
	_ = json.Unmarshal(got, &g)
	if !reflect.DeepEqual(w, g) {
		t.Errorf("the opened record %s is not the connector's %s", got, want)
	}
}

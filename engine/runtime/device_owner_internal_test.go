package runtime

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/wd"
)

// A tool's location input is sealed under the member who started it; any
// other location its inputs declare (a customer's address) is not
// (build-plan-gaps H3, review fix 6).
func TestDeviceLocationsOwnedOnlyTheToolsOwn(t *testing.T) {
	def, err := wd.Load([]byte(`{"schema":"wd/v1","id":"wf_visit","version":1,"name":"Visit","trigger":{"type":"manual"},
	 "inputs":{"schema":{"type":"object","properties":{
	  "where":{"type":"location","why":"so the visit is recorded where it was"},
	  "drop_at":{"type":"string","x-pii":"location"},
	  "phone":{"type":"string","x-pii":"phone"}}}},
	 "steps":[{"id":"n","type":"transform","config":{"output":{}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	by := &pii.Owner{Category: "member", Value: "u1"}
	owned := map[string]bool{}
	for _, p := range deviceLocationsOwned(def, triggerPaths(def), by) {
		key := ""
		for _, s := range p.Segments {
			key += "/" + s
		}
		owned[key] = p.By != nil
	}
	for path, want := range map[string]bool{
		"/trigger/body/where/lat": true, "/trigger/body/where/lng": true, "/trigger/body/where/address": true,
		"/trigger/body/drop_at": false, "/trigger/body/phone": false,
	} {
		got, ok := owned[path]
		if !ok || got != want {
			t.Errorf("%s: owned %v (present %v), want %v", path, got, ok, want)
		}
	}
}

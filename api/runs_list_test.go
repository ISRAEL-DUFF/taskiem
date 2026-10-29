package api_test

import (
	"testing"
)

const waitFlow = `{"schema":"wd/v1","id":"wf_wait","version":1,"name":"wait","trigger":{"type":"manual"},
  "steps":[{"id":"settle","type":"signal","config":{"event":"fakepay@1:transfer","correlation":"=trigger.body.ref","timeout":"1h"}}]}`

// Runs are found by their correlation key (spec 15.1), within the
// caller's tenant only.
func TestListRunsByCorrelationKey(t *testing.T) {
	w := newWorld(t)
	acme := w.tenant(t, "Acme", "owner@acme.test")
	other := w.tenant(t, "Other", "owner@other.test")
	start := func(c *client, ref string) string {
		wf := publishFlow(t, c, waitFlow)
		return c.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"ref": ref}})["run_id"].(string)
	}
	ids := func(c *client, query string) []string {
		var out []string
		for _, r := range c.must(200, "GET", "/v1/runs"+query, nil)["runs"].([]any) {
			out = append(out, r.(map[string]any)["id"].(string))
		}
		return out
	}
	a1 := start(acme, "ORDER-1")
	start(acme, "ORDER-2")
	b1 := start(other, "ORDER-1")

	if got := ids(acme, "?correlation_key=ORDER-1"); len(got) != 1 || got[0] != a1 {
		t.Errorf("Acme's ORDER-1: %v, want [%s]", got, a1)
	}
	if got := ids(other, "?correlation_key=ORDER-1"); len(got) != 1 || got[0] != b1 {
		t.Errorf("Other's ORDER-1: %v, want only its own run [%s]", got, b1)
	}
	if got := ids(acme, "?correlation_key=ORDER"); len(got) != 0 {
		t.Errorf("a prefix matched %v: the key must match exactly", got)
	}
	if got := ids(acme, ""); len(got) != 2 {
		t.Errorf("unfiltered: %v, want Acme's 2 runs", got)
	}
}

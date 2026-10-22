package api

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/israel-duff/taskiem/engine/capability"
	"github.com/israel-duff/taskiem/engine/wd"
)

type capabilityInfo struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Input       json.RawMessage `json:"input"`
	Output      json.RawMessage `json:"output,omitempty"`
	// Rails are the connectors the tenant can use that declare it.
	Rails []string `json:"rails"`
}

// listCapabilities describes the capability set (schemas/capabilities-v1.json)
// and the waits, with the rails that can do each, for the canvas's
// capability steps.
func (s *Server) listCapabilities(w http.ResponseWriter, r *http.Request) {
	reg, err := s.Registry.For(r.Context(), principalFrom(r.Context()).TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	railsOf := func(name string) []string {
		out := []string{}
		for _, c := range reg.List() {
			if c.Manifest.KindOf() != "rail" {
				continue
			}
			ok := false
			for n, m := range c.Manifest.Capabilities {
				ok = ok || n == name || (wd.IsWait(name) && m.SettlesBy != nil && m.SettlesBy.Trigger != "")
			}
			if ok {
				out = append(out, c.Ref())
			}
		}
		slices.Sort(out)
		return out
	}
	var out []capabilityInfo
	for _, n := range capability.Names() {
		spec, _ := capability.Get(n)
		out = append(out, capabilityInfo{Name: n, Title: spec.Title, Description: spec.Description, Input: spec.Input, Output: spec.Output, Rails: railsOf(n)})
	}
	for _, n := range wd.Waits {
		out = append(out, capabilityInfo{Name: n, Title: capability.WaitTitle(n), Rails: railsOf(n),
			Input: json.RawMessage(`{"type":"object","required":["payout_id"],"properties":{"payout_id":{"type":"string","description":"The payment step's payout_id"}}}`)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": out})
}

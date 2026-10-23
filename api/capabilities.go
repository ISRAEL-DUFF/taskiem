package api

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/israel-duff/taskiem/engine/capability"
	"github.com/israel-duff/taskiem/engine/expansion"
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
	// Kind is "event" for a normalised payment event a capability_event
	// trigger starts on (its input is the event's fields); a capability
	// step's own capabilities and waits have none.
	Kind string `json:"kind,omitempty"`
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
				ok = ok || n == name || (wd.IsWait(name) && name != "await_payment" && m.SettlesBy != nil && m.SettlesBy.Trigger != "")
			}
			if name == "await_payment" {
				ok = expansion.CanWait(c.Manifest, name)
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
		in := json.RawMessage(`{"type":"object","required":["payout_id"],"properties":{"payout_id":{"type":"string","description":"The payment step's payout_id"}}}`)
		if n == "await_payment" {
			in = json.RawMessage(`{"type":"object","required":["payment_id"],"properties":{"payment_id":{"type":"string","description":"The collect_link step's payment_id"}}}`)
		}
		out = append(out, capabilityInfo{Name: n, Title: capability.WaitTitle(n), Rails: railsOf(n), Input: in})
	}
	for _, n := range capability.EventNames() {
		ev, _ := capability.GetEvent(n)
		rails := []string{}
		for _, c := range reg.List() {
			if _, ok := c.Manifest.Events[n]; ok && c.Manifest.KindOf() == "rail" {
				rails = append(rails, c.Ref())
			}
		}
		slices.Sort(rails)
		out = append(out, capabilityInfo{Name: n, Title: ev.Title, Input: ev.Fields, Rails: rails, Kind: "event"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": out})
}

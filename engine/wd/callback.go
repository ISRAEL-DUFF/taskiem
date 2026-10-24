package wd

import (
	"fmt"
	"sort"
	"time"
)

// CallbackTimeout is how long a callback step waits unless it says
// otherwise: as a form's one-time link, a week.
const CallbackTimeout = 7 * 24 * time.Hour

// checkCallbacks enforces the callback and respond step rules of the
// wd-v1 contract: a callback step's timeout parses; a respond step is
// only in a workflow whose webhook trigger has respond: true, and such a
// trigger has at least one respond step to answer with.
func checkCallbacks(d *Definition) []Problem {
	var out []Problem
	ids := make([]string, 0, len(d.byID))
	for id := range d.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	responds := 0
	for _, id := range ids {
		s := d.byID[id]
		add := func(format string, args ...any) {
			out = append(out, Problem{Path: "step " + s.ID, Message: fmt.Sprintf(format, args...)})
		}
		switch s.Type {
		case "callback":
			if s.Callback != nil && s.Callback.Timeout != "" {
				if _, err := ParseDuration(s.Callback.Timeout); err != nil {
					add("timeout: %v", err)
				}
			}
		case "respond":
			responds++
			if !d.Responds() {
				add("a respond step answers a webhook call: the trigger must be a webhook with respond: true")
			}
			if s.OnError != nil {
				add("a respond step takes no on_error: it fails only on a bad expression")
			}
		}
	}
	if d.Responds() && responds == 0 {
		out = append(out, Problem{Path: "/trigger/config/respond", Message: "a webhook that responds needs a respond step to answer with"})
	}
	return out
}

// Responds reports whether the workflow answers the webhook call that
// starts it (a webhook trigger with respond: true).
func (d *Definition) Responds() bool {
	return d.Trigger.Type == "webhook" && d.Trigger.Config["respond"] == true
}

// HasCallbacks reports whether a definition has callback steps.
func (d *Definition) HasCallbacks() bool {
	found := false
	d.Walk(func(s *Step) { found = found || s.Type == "callback" })
	return found
}

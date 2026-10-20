package formguard

import (
	"net/url"
	"strings"
)

// HoneypotField is the name of the honeypot input. A person never sees it,
// so never fills it in; a bot that fills every field does. Render it as an
// ordinary text input with autocomplete="off" and tabindex="-1", inside an
// element hidden by a class in the page's stylesheet, not by an inline
// style attribute: the public host's CSP forbids inline styles. Avoid
// type="hidden" and the hidden attribute, which bots skip.
const HoneypotField = "website"

// HoneypotTripped reports whether the honeypot field has any non-blank
// value.
func HoneypotTripped(values url.Values) bool {
	for _, v := range values[HoneypotField] {
		if strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

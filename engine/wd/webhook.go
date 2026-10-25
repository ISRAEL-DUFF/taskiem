package wd

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// WebhookMethods is what a webhook trigger takes unless it lists methods.
var WebhookMethods = []string{"POST"}

// Bodiless reports whether requests of a method carry no body to sign or
// read, so a webhook taking it reads its query parameters as the body.
func Bodiless(method string) bool { return method == "GET" || method == "DELETE" }

// checkWebhook enforces the webhook trigger rules of the wd-v1 contract
// (webhook triggers) that the schema cannot say: an HMAC signs the body,
// so a webhook taking a bodiless method uses another auth; jwt settings
// go with auth jwt; CORS origins are origins, and a browser page cannot
// keep a bearer token, a password or an HMAC key secret, so CORS goes
// with auth jwt or none.
func checkWebhook(d *Definition) []Problem {
	if d.Trigger.Type != "webhook" {
		return nil
	}
	c := d.Trigger.Config
	var out []Problem
	add := func(path, format string, args ...any) {
		out = append(out, Problem{Path: "/trigger/config/" + path, Message: fmt.Sprintf(format, args...)})
	}
	auth, _ := c["auth"].(string)
	for _, m := range WebhookConfigMethods(c) {
		if auth == "hmac" && Bodiless(m) {
			add("methods", "an hmac signature signs the body, and a %s has none: use bearer, basic or jwt", m)
		}
	}
	if _, ok := c["jwt"]; ok && auth != "jwt" {
		add("jwt", "jwt settings need auth jwt")
	}
	cors, _ := c["cors"].(map[string]any)
	if cors == nil {
		return out
	}
	switch auth {
	case "hmac", "bearer", "basic":
		add("cors", "a web page cannot keep a webhook's %s secret from its visitors: use jwt (a short-lived token your server issues) or none", map[string]string{"hmac": "signing key", "bearer": "token", "basic": "password"}[auth])
	}
	origins, _ := cors["origins"].([]any)
	for i, o := range origins {
		s, _ := o.(string)
		if s == "*" {
			if len(origins) > 1 {
				add(fmt.Sprintf("cors/origins/%d", i), "* allows every origin, so it stands alone")
			}
			continue
		}
		if err := CheckOrigin(s); err != nil {
			add(fmt.Sprintf("cors/origins/%d", i), "%v", err)
		}
	}
	return out
}

// WebhookConfigMethods is the methods a webhook trigger's config takes.
func WebhookConfigMethods(c map[string]any) []string {
	ms, _ := c["methods"].([]any)
	if len(ms) == 0 {
		return WebhookMethods
	}
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		if s, ok := m.(string); ok && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// CheckOrigin reports why s is not an origin a webhook may allow: a
// scheme and host with an optional port and nothing else, https unless
// the host is the developer's own machine.
func CheckOrigin(s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return fmt.Errorf("%q is not an origin such as https://shop.example.com", s)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("%q: an origin is a scheme and host only, with no path, query or trailing slash", s)
	}
	if strings.ToLower(s) != s {
		return fmt.Errorf("%q: write the origin in lower case, as browsers send it", s)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if h := u.Hostname(); h != "localhost" && h != "127.0.0.1" {
			return fmt.Errorf("%q: only https origins, or http on localhost while developing", s)
		}
	default:
		return fmt.Errorf("%q: only https origins, or http on localhost while developing", s)
	}
	return nil
}

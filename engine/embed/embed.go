// Package embed holds what embedding Taskiem in partners' products needs
// beyond the API (spec 13.4; decision 0015): which connectors a definition
// uses (an embed app allows only some), validation of an app's origins and
// branding tokens, the permissions end users may ever hold, and the signed
// partner webhooks.
package embed

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// EndUserCeiling is every permission an end-user token may ever carry. An
// embed app allows a subset to its end users, and each token a subset of
// that. Secrets, connections, members, roles, policies, audit, personal
// data, approvals and the tenant's own connectors stay with the partner:
// end users are lightweight principals with no platform login, so nothing
// they can do should need one.
var EndUserCeiling = []string{"workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start", "run.cancel"}

// DefaultEndUserPermissions are an app's end users' permissions unless it
// says otherwise: build and run, but not publish or cancel.
var DefaultEndUserPermissions = []string{"workflow.read", "workflow.edit", "run.read", "run.start"}

// Step types an app must allow by name, like connectors: they reach the
// network or run code without a connector's declared actions.
var gatedStepTypes = []string{"http", "code", "container", "ai"}

// Events are the partner webhook events.
var Events = []string{"run.completed", "run.failed", "workflow.published", "usage.threshold", EventDomainUnverified}

// Uses lists what a definition needs its embed app to allow, sorted:
// connector ids (a reference's part before "@") and "http", "code", "container" or "ai"
// for steps of those types. It reads the trigger and the steps (nested ones
// too), and drafts as well as valid definitions: anything that is JSON.
func Uses(doc []byte) ([]string, error) {
	var root map[string]any
	if err := json.Unmarshal(doc, &root); err != nil {
		return nil, fmt.Errorf("definition is not a JSON object: %w", err)
	}
	set := map[string]bool{}
	walk(root["trigger"], set)
	walk(root["steps"], set)
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out, nil
}

func walk(v any, set map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		if c, ok := x["connector"].(string); ok && c != "" {
			id, _, _ := strings.Cut(c, "@")
			set[id] = true
		}
		if t, ok := x["type"].(string); ok && slices.Contains(gatedStepTypes, t) {
			if _, step := x["id"]; step {
				set[t] = true
			}
		}
		for _, c := range x {
			walk(c, set)
		}
	case []any:
		for _, c := range x {
			walk(c, set)
		}
	}
}

// Disallowed returns the uses not in allowed, sorted.
func Disallowed(uses, allowed []string) []string {
	var out []string
	for _, u := range uses {
		if !slices.Contains(allowed, u) {
			out = append(out, u)
		}
	}
	return out
}

// Origin checks an allowed origin and returns it canonical: an exact
// scheme://host[:port], https (http only for localhost, for development),
// with no path, query, credentials or wildcard.
func Origin(s string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || strings.Contains(s, "*") {
		return "", fmt.Errorf("origin %q must be scheme://host[:port], without a path or wildcard", s)
	}
	scheme, host := strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	if scheme != "https" && (scheme != "http" || !local(u.Hostname())) {
		return "", fmt.Errorf("origin %q must use https (http only for localhost)", s)
	}
	return scheme + "://" + host, nil
}

// WebhookURL checks a partner webhook URL: https (http only for localhost).
func WebhookURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.User != nil || len(s) > 2048 {
		return fmt.Errorf("webhook_url %q is not a URL", s)
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !local(u.Hostname())) {
		return errors.New("webhook_url must use https (http only for localhost)")
	}
	return nil
}

// Partner capabilities an operator grants (taskiem tenants partner
// --capabilities): plan gates on what a partner's apps may do.
const (
	CapWhiteLabel    = "white_label"    // apps may leave out the platform's branding
	CapCustomDomains = "custom_domains" // apps may be served on the partner's own domains
)

// Capabilities are every partner capability.
var Capabilities = []string{CapWhiteLabel, CapCustomDomains}

var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

// Domain checks a custom domain for an embed app and returns it in lower
// case: a fully qualified host name with at least two labels, no port,
// scheme, path, wildcard or IP address.
func Domain(s string) (string, error) {
	d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
	if len(d) > 253 || !domainRe.MatchString(d) || net.ParseIP(d) != nil {
		return "", fmt.Errorf("domain %q must be a host name like automations.example.com, without a scheme, port, path or wildcard", s)
	}
	return d, nil
}

func local(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Branding is an app's theming tokens (spec 13.4): what the embedded
// builder (C2) renders with. Every value is checked, so a token can never
// carry markup, script or a style injection.
type Branding struct {
	Colours    map[string]string `json:"colours,omitempty"`
	FontFamily string            `json:"font_family,omitempty"`
	FontURL    string            `json:"font_url,omitempty"`
	LogoURL    string            `json:"logo_url,omitempty"`
	Radius     string            `json:"radius,omitempty"`
	Mode       string            `json:"mode,omitempty"` // light, dark or auto
}

// ColourTokens are the colour names a theme may set.
var ColourTokens = []string{"primary", "on_primary", "background", "surface", "text", "muted", "border", "accent", "danger", "success"}

var (
	colourRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)
	fontRe   = regexp.MustCompile(`^[A-Za-z0-9 ,-]{1,100}$`)
	radiusRe = regexp.MustCompile(`^[0-9]{1,2}px$`)
)

// ParseBranding decodes and checks branding tokens; unknown fields are refused.
func ParseBranding(raw json.RawMessage) (Branding, error) {
	var b Branding
	if len(raw) == 0 || string(raw) == "null" {
		return b, nil
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return b, fmt.Errorf("branding: %w", err)
	}
	for k, v := range b.Colours {
		if !slices.Contains(ColourTokens, k) {
			return b, fmt.Errorf("branding: unknown colour %q (one of %s)", k, strings.Join(ColourTokens, ", "))
		}
		if !colourRe.MatchString(v) {
			return b, fmt.Errorf("branding: colour %s must be #rgb, #rrggbb or #rrggbbaa", k)
		}
	}
	if b.FontFamily != "" && !fontRe.MatchString(b.FontFamily) {
		return b, errors.New("branding: font_family may hold letters, digits, spaces, commas and hyphens only")
	}
	for name, u := range map[string]string{"font_url": b.FontURL, "logo_url": b.LogoURL} {
		if u == "" {
			continue
		}
		p, err := url.Parse(u)
		if err != nil || p.Scheme != "https" || p.Host == "" || p.User != nil || len(u) > 2048 {
			return b, fmt.Errorf("branding: %s must be an https URL", name)
		}
	}
	if b.Radius != "" && !radiusRe.MatchString(b.Radius) {
		return b, errors.New("branding: radius must be like 8px")
	}
	if b.Mode != "" && b.Mode != "light" && b.Mode != "dark" && b.Mode != "auto" {
		return b, errors.New("branding: mode must be light, dark or auto")
	}
	return b, nil
}

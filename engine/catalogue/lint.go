// Package catalogue is the public connector catalogue (spec 6, Phase 4
// P4-6; decision 0020): the strict manifest lint the SDK and the
// submission pipeline share, the automated checks a submitted package
// must pass before a person reviews it, publisher namespaces, and the
// manifest diff an installing tenant consents to on upgrade.
package catalogue

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

// SlugRe is a publisher namespace: lower-case letters and digits, no
// underscore, so p_<slug>_<name> splits one way only.
var SlugRe = regexp.MustCompile(`^[a-z][a-z0-9]{1,29}$`)

// nameRe is the part of a catalogue id after the publisher.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// MaxIDLength is the longest connector id a workflow can name (wd/v1).
const MaxIDLength = 63

// Reserved are slugs no publisher may take: they would read as Taskiem's
// own, or as a built-in.
var Reserved = map[string]bool{"taskiem": true, "official": true, "builtin": true, "system": true, "admin": true, "platform": true, "verified": true, "support": true}

// ParseID splits a catalogue id p_<publisher>_<name>.
func ParseID(id string) (publisher, name string, ok bool) {
	rest, ok := strings.CutPrefix(id, connector.CataloguePrefix)
	if !ok {
		return "", "", false
	}
	publisher, name, ok = strings.Cut(rest, "_")
	if !ok || !SlugRe.MatchString(publisher) || !nameRe.MatchString(name) || len(id) > MaxIDLength {
		return "", "", false
	}
	return publisher, name, true
}

// Finding is one lint result. Errors block an upload or a submission;
// warnings are shown to the author and to reviewers.
type Finding struct {
	Level   string `json:"level"` // error or warning
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (f Finding) String() string { return f.Level + ": " + f.Path + ": " + f.Message }

// Errors returns the findings that block.
func Errors(fs []Finding) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Level == "error" {
			out = append(out, f)
		}
	}
	return out
}

// LintOptions say what a manifest is for.
type LintOptions struct {
	// Publisher is the namespace a catalogue manifest must be in; empty
	// for a tenant's own connector (x_).
	Publisher string
}

// writeVerbs start action names that change something at the provider.
var writeVerbs = []string{"create", "send", "transfer", "pay", "initiate", "submit", "post", "update", "delete", "remove", "cancel",
	"refund", "charge", "debit", "credit", "disburse", "approve", "register", "add", "set", "reverse", "void", "capture", "issue", "upload", "write"}

// personal are input and output field names that hold personal data
// whatever the provider: they must be declared under pii.
var personal = map[string]bool{
	"name": true, "first_name": true, "last_name": true, "middle_name": true, "full_name": true, "surname": true, "account_name": true,
	"customer_name": true, "sender_name": true, "recipient_name": true, "beneficiary_name": true,
	"email": true, "email_address": true, "phone": true, "phone_number": true, "msisdn": true, "mobile": true, "mobile_number": true,
	"bvn": true, "nin": true, "national_id": true, "id_number": true, "passport": true, "passport_number": true, "ssn": true,
	"address": true, "street": true, "dob": true, "date_of_birth": true, "birth_date": true,
	"account_number": true, "card_number": true, "pan": true, "iban": true,
}

// Lint applies the strict rules on top of the connector/v1 contract:
// classes honest, hosts declared and public, personal data declared.
func Lint(raw []byte, opt LintOptions) (*connector.Manifest, []Finding) {
	m, probs := connector.Parse(raw)
	if len(probs) > 0 {
		out := make([]Finding, 0, len(probs))
		for _, p := range probs {
			out = append(out, Finding{Level: "error", Path: "/", Message: p})
		}
		return nil, out
	}
	var out []Finding
	add := func(level, path, format string, args ...any) {
		out = append(out, Finding{Level: level, Path: path, Message: fmt.Sprintf(format, args...)})
	}
	catalogue := opt.Publisher != ""
	strict := "warning"
	if catalogue {
		strict = "error"
	}

	// Identity.
	switch {
	case catalogue:
		pub, _, ok := ParseID(m.ID)
		if !ok || pub != opt.Publisher {
			add("error", "/id", "a catalogue connector's id is p_%s_<name> (lower-case letters, digits and underscores, at most %d characters), not %q", opt.Publisher, MaxIDLength, m.ID)
		}
	case !strings.HasPrefix(m.ID, connector.TenantPrefix):
		add("error", "/id", "your own connector's id starts with %q; a catalogue connector's with p_<publisher>_", connector.TenantPrefix)
	}
	if strings.TrimSpace(m.Description) == "" {
		add(strict, "/description", "say in a sentence what the connector does; builders and reviewers read it")
	}
	if m.Auth.Test == nil {
		add("warning", "/auth/test", "no test action: connections cannot be checked when they are added")
	}

	// Hosts.
	hosts := m.Hosts()
	if len(hosts) == 0 {
		add("error", "/base_url", "declare base_url or egress_hosts: a connector reaches only the hosts it declares")
	}
	if m.BaseURL != "" {
		if u, err := url.Parse(m.BaseURL); err != nil || u.Scheme != "https" {
			add("error", "/base_url", "base_url must be an https URL")
		}
	}
	for _, h := range hosts {
		if msg := badHost(h, catalogue); msg != "" {
			add("error", "/egress_hosts", "%s: %s", h, msg)
		}
	}

	// Actions: classes honest, personal data declared.
	names := make([]string, 0, len(m.Actions))
	for n := range m.Actions {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		a := m.Actions[name]
		p := "/actions/" + name
		switch a.Class {
		case effects.Read:
			if v := writeVerb(name, a.Title); v != "" {
				add("error", p+"/class", "declared read, but %q reads like a change at the provider: a read is retried freely. Classify it as a write, or rename it if it changes nothing", v)
			}
		case effects.UnsafeWrite:
			add("warning", p+"/class", "unsafe_write: after a timeout Taskiem parks the run for a person. If the provider deduplicates by a value you send, declare idempotent_write; if you can look the effect up, reconcilable_write")
		case effects.IdempotentWrite, effects.ReconcilableWrite:
			if a.Idempotency != nil && !hasProperty(a.Input, a.Idempotency.Field) {
				add("warning", p+"/idempotency/field", "the engine puts its key in input field %q, which the input schema does not declare", a.Idempotency.Field)
			}
		}
		if a.Class == effects.ReconcilableWrite && a.Idempotency == nil {
			add("warning", p+"/idempotency", "a reconcilable write without an idempotency block: the reconcile action must find the effect by something the engine controls")
		}
		declared := map[string]bool{}
		for _, f := range a.PII {
			declared[normPII(f.Field)] = true
		}
		var missing []string
		walk(a.Input, "", func(path, key string) {
			if personal[key] && !declared[path] {
				missing = append(missing, path)
			}
		})
		walk(a.Output, "output.", func(path, key string) {
			if personal[key] && !declared[path] {
				missing = append(missing, path)
			}
		})
		for _, f := range missing {
			add("error", p+"/pii", "%q holds personal data: declare it under pii so Taskiem seals it in run history", f)
		}
	}
	for name, t := range m.Triggers {
		if t.Verify == nil || t.Verify.Scheme == "none" {
			add("error", "/triggers/"+name+"/verify", "a trigger must verify its deliveries with a signature or secret scheme")
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Level < out[j].Level })
	return m, out
}

func writeVerb(name, title string) string {
	for _, s := range []string{name, strings.ToLower(strings.ReplaceAll(title, " ", "_"))} {
		first, _, _ := strings.Cut(s, "_")
		for _, v := range writeVerbs {
			if first == v {
				return v
			}
		}
	}
	return ""
}

// badHost says what is wrong with a declared host, or "".
func badHost(h string, catalogue bool) string {
	host := strings.ToLower(strings.TrimSuffix(h, "."))
	if rest, ok := strings.CutPrefix(host, "*."); ok {
		if catalogue {
			return "wildcards are not accepted in the catalogue: name each host, so installing tenants know where their data goes"
		}
		host = rest
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return "name a host, not an IP address"
	}
	if host == "localhost" || !strings.Contains(host, ".") {
		return "not a public host name"
	}
	for _, s := range []string{".localhost", ".local", ".internal", ".lan", ".home", ".corp", ".test", ".invalid", ".example"} {
		if strings.HasSuffix(host, s) && catalogue {
			return "not a public host name"
		}
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return "name the host without a port"
	}
	return ""
}

func normPII(field string) string {
	if strings.HasPrefix(field, "output.") {
		return field
	}
	return strings.TrimPrefix(field, "input.")
}

func hasProperty(schema json.RawMessage, field string) bool {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(schema, &s) != nil {
		return false
	}
	_, ok := s.Properties[field]
	return ok
}

// walk calls fn for every property a schema describes, with its pii-style
// path ("a.b", "list.*.name", prefixed as given) and its own key.
func walk(schema json.RawMessage, prefix string, fn func(path, key string)) {
	var visit func(raw json.RawMessage, path string, depth int)
	visit = func(raw json.RawMessage, path string, depth int) {
		if depth > 16 {
			return
		}
		var s struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Items      json.RawMessage            `json:"items"`
		}
		if json.Unmarshal(raw, &s) != nil {
			return
		}
		for k, sub := range s.Properties {
			p := path + k
			fn(p, k)
			visit(sub, p+".", depth+1)
		}
		if len(s.Items) > 0 {
			visit(s.Items, path+"*.", depth+1)
		}
	}
	visit(schema, prefix, 0)
}

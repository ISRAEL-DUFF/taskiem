// Package connector validates connector manifests against the connector/v1
// contract: the JSON Schema plus the registration rules in
// docs/contracts/connector-v1.md.
package connector

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"slices"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/internal/schemacheck"
	"github.com/israel-duff/taskiem/schemas"
)

var schema = schemacheck.New("https://schemas.taskiem.dev/connector/v1.json", schemas.ConnectorV1)

// Manifest is the subset of a connector/v1 manifest the engine reads.
type Manifest struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Name     string `json:"name"`
	Category string `json:"category"`
	// Kind is rail, product or tool (decision 0044); empty is tool.
	Kind string `json:"kind,omitempty"`
	// Description is the manifest's own summary, shown to builders.
	Description string                 `json:"description"`
	Auth        Auth                   `json:"auth"`
	BaseURL     string                 `json:"base_url"`
	Egress      []string               `json:"egress_hosts"`
	Actions     map[string]ActionSpec  `json:"actions"`
	Triggers    map[string]TriggerSpec `json:"triggers"`
	// Capabilities and Events say what a rail can do in Taskiem's words
	// (rule 16, engine/capability).
	Capabilities map[string]CapabilityMap `json:"capabilities,omitempty"`
	Events       map[string]EventMap      `json:"events,omitempty"`
}

type Auth struct {
	Type   string      `json:"type"`
	Fields []AuthField `json:"fields"`
	// OAuth2, when set, lets a connection be made by OAuth 2.0
	// authorization code grant with PKCE instead of pasted credentials
	// (docs/connector-sdk.md, decision 0034). The pasted fields stay.
	OAuth2 *OAuth2 `json:"oauth2,omitempty"`
	Test   *struct {
		Action string `json:"action"`
	} `json:"test"`
}

// OAuth2 is how a connector's provider issues tokens to Taskiem. The URLs
// are absolute, or start with ${server}/ to be resolved from the
// connector's base URL (the platform's server for that provider) when a
// connection is linked: see Resolve.
type OAuth2 struct {
	AuthorizeURL string   `json:"authorize_url"`
	TokenURL     string   `json:"token_url"`
	RevokeURL    string   `json:"revoke_url,omitempty"`
	Scopes       []string `json:"scopes"`
	PKCE         bool     `json:"pkce"`
	// FieldsFromToken fills auth fields from the token answer: the value
	// names a field of the answer (access_token, org_id), one element of a
	// list field (project_ids[0]: the list must then hold exactly one), or
	// ${server} for the server the token was issued by.
	FieldsFromToken map[string]string `json:"fields_from_token,omitempty"`
	// AuthorizeParams are extra query parameters on the consent URL
	// (Google: access_type=offline and prompt=consent for a refresh token).
	AuthorizeParams map[string]string `json:"authorize_params,omitempty"`
	// PlatformClientFields fill auth fields from Taskiem's registration
	// with the provider ("id" or "secret"), for connectors that refresh
	// their own tokens with the client that was granted them.
	PlatformClientFields map[string]string `json:"platform_client_fields,omitempty"`
}

// ServerPlaceholder starts an OAuth URL resolved from the connector's base
// URL at runtime.
const ServerPlaceholder = "${server}"

// Resolve returns the authorize, token and revoke URLs for a provider at
// server (the connector's base URL; empty when the platform has none). A
// URL that needs the server is an error when there is none: such a
// connector is not linkable on this platform.
func (o *OAuth2) Resolve(server string) (authorize, token, revoke string, err error) {
	server = strings.TrimRight(server, "/")
	one := func(u string) (string, error) {
		if !strings.HasPrefix(u, ServerPlaceholder+"/") {
			return u, nil
		}
		if server == "" {
			return "", fmt.Errorf("%s needs the provider's server, which this platform does not name", u)
		}
		return server + strings.TrimPrefix(u, ServerPlaceholder), nil
	}
	if authorize, err = one(o.AuthorizeURL); err != nil {
		return "", "", "", err
	}
	if token, err = one(o.TokenURL); err != nil {
		return "", "", "", err
	}
	if o.RevokeURL != "" {
		if revoke, err = one(o.RevokeURL); err != nil {
			return "", "", "", err
		}
	}
	return authorize, token, revoke, nil
}

// TokenField is one FieldsFromToken value, parsed: the answer field, and
// whether one element of a list is wanted.
func TokenField(v string) (field string, element bool) {
	if f, ok := strings.CutSuffix(v, "[0]"); ok {
		return f, true
	}
	return v, false
}

// AuthField is one credential a connection supplies. Help, Where and Link
// guide the person connecting (no-technical-steps N2); Prefixes and Refuse
// check a pasted value before anything is saved.
type AuthField struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Secret   bool   `json:"secret"`
	Required *bool  `json:"required"`
	Help     string `json:"help,omitempty"`
	Where    string `json:"where,omitempty"`
	Link     string `json:"link,omitempty"`
	// Prefixes are what a right value starts with; a Mode is a choice the
	// person makes (live or test) instead of knowing a prefix.
	Prefixes []KeyPrefix `json:"prefixes,omitempty"`
	// Refuse are values that are the wrong one, with a plain message.
	Refuse []KeyRefusal `json:"refuse,omitempty"`
	// Multiline: the value spans lines (a PEM key), so the person pastes
	// it into a text area.
	Multiline bool `json:"multiline,omitempty"`
	// Generate names a value Taskiem can make for the person instead of
	// asking them to: rsa_key_pair makes an RSA key pair in their browser,
	// keeps the private key as the value and shows the public key to paste
	// into the provider's dashboard.
	Generate string `json:"generate,omitempty"`
}

// KeyPrefix is a start a right value has, for one mode.
type KeyPrefix struct {
	Prefix string `json:"prefix"`
	Mode   string `json:"mode,omitempty"`
	Label  string `json:"label,omitempty"`
}

// KeyRefusal is a start of a wrong value, and what to tell the person.
type KeyRefusal struct {
	Prefix  string `json:"prefix"`
	Message string `json:"message"`
}

// IsRequired reports whether the field must be given.
func (f AuthField) IsRequired() bool { return f.Required == nil || *f.Required }

// CheckValue checks a pasted value against the field's prefixes, for the
// mode the person chose ("" for any), in plain words.
func (f AuthField) CheckValue(v, mode string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	for _, r := range f.Refuse {
		if strings.HasPrefix(v, r.Prefix) {
			return errors.New(r.Message)
		}
	}
	if len(f.Prefixes) == 0 {
		return nil
	}
	var other *KeyPrefix
	for i, p := range f.Prefixes {
		if !strings.HasPrefix(v, p.Prefix) {
			continue
		}
		if mode == "" || p.Mode == "" || p.Mode == mode {
			return nil
		}
		other = &f.Prefixes[i]
	}
	if other != nil {
		want := mode
		for _, p := range f.Prefixes {
			if p.Mode == mode && p.Label != "" {
				want = p.Label
			}
		}
		got := other.Label
		if got == "" {
			got = other.Mode
		}
		return fmt.Errorf("that is a %s %s, but you chose %s; copy the %s %s instead", strings.ToLower(got), strings.ToLower(f.Label), strings.ToLower(want), strings.ToLower(want), strings.ToLower(f.Label))
	}
	var starts []string
	for _, p := range f.Prefixes {
		if mode == "" || p.Mode == "" || p.Mode == mode {
			starts = append(starts, p.Prefix)
		}
	}
	return fmt.Errorf("that does not look like your %s: it starts with %s", strings.ToLower(f.Label), strings.Join(starts, " or "))
}

// ModeOf is the mode credentials are for, read from the values' starts
// (sk_live_ is live), or "" when the fields say nothing.
func (a Auth) ModeOf(creds map[string]string) string {
	for _, f := range a.Fields {
		v := strings.TrimSpace(creds[f.Key])
		for _, p := range f.Prefixes {
			if p.Mode != "" && v != "" && strings.HasPrefix(v, p.Prefix) {
				return p.Mode
			}
		}
	}
	return ""
}

// Modes are the choices a connector's fields offer (live, test), in order,
// with their labels.
func (a Auth) Modes() []KeyPrefix {
	var out []KeyPrefix
	for _, f := range a.Fields {
		for _, p := range f.Prefixes {
			if p.Mode != "" && !slices.ContainsFunc(out, func(x KeyPrefix) bool { return x.Mode == p.Mode }) {
				out = append(out, KeyPrefix{Mode: p.Mode, Label: p.Label})
			}
		}
	}
	return out
}

type ActionSpec struct {
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Class       effects.Class   `json:"class"`
	Idempotency *effects.Spec   `json:"idempotency"`
	Reconcile   string          `json:"reconcile"`
	Compensate  *string         `json:"compensate"`
	Input       json.RawMessage `json:"input"`
	Output      json.RawMessage `json:"output"`
	PII         []PIIField      `json:"pii"`
}

// PIIField accepts either "field" or {field, category}.
type PIIField struct {
	Field    string `json:"field"`
	Category string `json:"category"`
}

func (p *PIIField) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		p.Field = s
		return nil
	}
	type plain PIIField
	return json.Unmarshal(b, (*plain)(p))
}

// Parse validates a YAML or JSON manifest and returns it, or every problem found.
func Parse(src []byte) (*Manifest, []string) {
	j, err := yaml.YAMLToJSON(src)
	if err != nil {
		return nil, []string{"yaml: " + err.Error()}
	}
	if msgs := schema.Validate(j); len(msgs) > 0 {
		for i := range msgs {
			msgs[i] = "schema: " + msgs[i]
		}
		return nil, msgs
	}
	var m Manifest
	if err := json.Unmarshal(j, &m); err != nil {
		return nil, []string{err.Error()}
	}
	if probs := m.check(); len(probs) > 0 {
		return nil, probs
	}
	return &m, nil
}

// KindOf is the manifest's kind: rail, product or tool.
func (m *Manifest) KindOf() string {
	if m.Kind == "" {
		return "tool"
	}
	return m.Kind
}

func (m *Manifest) check() []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	names := make([]string, 0, len(m.Actions))
	for n := range m.Actions {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		a := m.Actions[name]
		p := "/actions/" + name
		if a.Class == effects.Read && a.Compensate != nil {
			add("%s: read actions cannot declare compensate", p)
		}
		if a.Reconcile != "" {
			if r, ok := m.Actions[a.Reconcile]; !ok {
				add("%s/reconcile: unknown action %q", p, a.Reconcile)
			} else if r.Class != effects.Read {
				add("%s/reconcile: %q must be a read action, is %s", p, a.Reconcile, r.Class)
			}
		}
		if a.Compensate != nil {
			if c, ok := m.Actions[*a.Compensate]; !ok {
				add("%s/compensate: unknown action %q", p, *a.Compensate)
			} else if !c.Class.IsWrite() {
				add("%s/compensate: %q must be a write action", p, *a.Compensate)
			}
		}
		if a.Idempotency != nil {
			if err := a.Idempotency.Validate(); err != nil {
				add("%s/idempotency: %v", p, strings.TrimPrefix(err.Error(), "effects: "))
			}
		}
		for _, f := range a.PII {
			if segs, ok := OutputPIIPath(f.Field); ok {
				if !schemaHasPath(a.Output, segs) {
					add("%s/pii: %q is not in the output schema", p, f.Field)
				}
			} else if !schemaHasPath(a.Input, InputPIIPath(f.Field)) {
				add("%s/pii: %q is not in the input schema", p, f.Field)
			}
		}
	}
	tnames := make([]string, 0, len(m.Triggers))
	for n := range m.Triggers {
		tnames = append(tnames, n)
	}
	sort.Strings(tnames)
	for _, name := range tnames {
		// A polled trigger lists with a read action, and its deliveries are
		// signed the way the provider signs them.
		if pl := m.Triggers[name].Poll; pl != nil {
			if a, ok := m.Actions[pl.Action]; !ok {
				add("/triggers/%s/poll/action: unknown action %q", name, pl.Action)
			} else if a.Class.IsWrite() {
				add("/triggers/%s/poll/action: %q must be a read action", name, pl.Action)
			}
			if v := m.Triggers[name].Verify; v == nil || (v.Scheme != "hmac_sha256" && v.Scheme != "hmac_sha512") {
				add("/triggers/%s/poll: only a trigger verified by plain HMAC can be polled", name)
			}
		}
		// An ack is served from the hooks origin: never as a page.
		if a := m.Triggers[name].Ack; a != nil && a.ContentType != "" {
			if mt, _, err := mime.ParseMediaType(a.ContentType); err != nil || (mt != "text/plain" && mt != "application/json") {
				add("/triggers/%s/ack/content_type: must be text/plain or application/json, not %q", name, a.ContentType)
			}
		}
		// A remote trigger is verified with the secret the provider returned
		// when Taskiem created the subscription, never a connection field.
		if t := m.Triggers[name]; t.Remote() {
			switch {
			case t.Type != "webhook":
				add("/triggers/%s/registration: only webhook triggers are registered remotely", name)
			case t.Verify == nil:
			case t.Verify.SecretField != "":
				add("/triggers/%s/verify/secret_field: a remote trigger is verified with the secret the provider returned", name)
			case !slices.Contains([]string{"hmac_sha256", "hmac_sha512", "hmac_sha1", "hmac_sha256_timestamped", "slack_v0", "header_secret", "bearer"}, t.Verify.Scheme):
				add("/triggers/%s/verify/scheme: %s cannot verify a remotely registered trigger", name, t.Verify.Scheme)
			}
		}
	}
	for _, name := range tnames {
		rt := m.Triggers[name].Route
		if rt == nil {
			continue
		}
		if m.Triggers[name].Remote() {
			add("/triggers/%s/route: a remotely registered trigger belongs to one workflow and cannot be routed", name)
		}
		i := slices.IndexFunc(m.Auth.Fields, func(f AuthField) bool { return f.Key == rt.Field })
		switch {
		case i < 0:
			add("/triggers/%s/route/field: %q is not an auth field", name, rt.Field)
		case m.Auth.Fields[i].Secret:
			add("/triggers/%s/route/field: %q is secret; routing keys are stored in the clear", name, rt.Field)
		}
	}
	m.checkCapabilities(add)
	if m.Auth.Test != nil {
		if a, ok := m.Actions[m.Auth.Test.Action]; !ok {
			add("/auth/test: unknown action %q", m.Auth.Test.Action)
		} else if a.Class != effects.Read {
			add("/auth/test: %q must be a read action", m.Auth.Test.Action)
		}
	}
	if o := m.Auth.OAuth2; o != nil {
		for name, u := range map[string]string{"authorize_url": o.AuthorizeURL, "token_url": o.TokenURL, "revoke_url": o.RevokeURL} {
			if u == "" {
				continue
			}
			if _, _, _, err := o.Resolve("https://provider.example"); err != nil {
				add("/auth/oauth2/%s: %v", name, err)
			} else if p, err := url.Parse(strings.Replace(u, ServerPlaceholder, "https://provider.example", 1)); err != nil || p.Scheme != "https" || p.Hostname() == "" || p.Fragment != "" {
				add("/auth/oauth2/%s: must be an https URL without a fragment", name)
			}
		}
		if len(o.Scopes) == 0 {
			add("/auth/oauth2/scopes: name at least one scope")
		}
		if !o.PKCE {
			add("/auth/oauth2/pkce: Taskiem always sends a PKCE challenge; declare pkce: true")
		}
		keys := make([]string, 0, len(o.FieldsFromToken))
		for k := range o.FieldsFromToken {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !slices.ContainsFunc(m.Auth.Fields, func(f AuthField) bool { return f.Key == k }) {
				add("/auth/oauth2/fields_from_token/%s: not an auth field", k)
			}
		}
		for _, f := range m.Auth.Fields {
			if _, ok := o.FieldsFromToken[f.Key]; !ok && (f.Required == nil || *f.Required) {
				add("/auth/oauth2/fields_from_token: required field %q is not filled from the token; a linked connection would be missing it", f.Key)
			}
		}
	}
	return out
}

// ListValues reads a connection field holding a list: values separated by
// commas or white space, empty ones dropped, duplicates removed, in order.
func ListValues(s string) []string {
	var out []string
	for _, v := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' }) {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// RouteKeys returns, for each connection field a trigger routes by, the
// keys a connection's credentials list in it (decision 0031). Nil when
// the connector routes nothing or the connection lists no keys.
func (m *Manifest) RouteKeys(creds map[string]string) map[string][]string {
	var out map[string][]string
	for _, t := range m.Triggers {
		if t.Route == nil {
			continue
		}
		if _, done := out[t.Route.Field]; done {
			continue
		}
		if vs := ListValues(creds[t.Route.Field]); len(vs) > 0 {
			if out == nil {
				out = map[string][]string{}
			}
			out[t.Route.Field] = vs
		}
	}
	return out
}

// InputPIIPath reads a pii field naming a place in the action's input:
// "<key>[.<key>|.*]...", optionally prefixed "input.", "*" standing for
// every array element (transfers.*.account_name).
func InputPIIPath(field string) []string {
	return strings.Split(strings.TrimPrefix(field, "input."), ".")
}

// OutputPIIPath reads a pii field naming a place in the action's output:
// "output.<key>[.<key>|.*]...", "*" standing for every array element.
func OutputPIIPath(field string) ([]string, bool) {
	rest, ok := strings.CutPrefix(field, "output.")
	if !ok || rest == "" {
		return nil, false
	}
	return strings.Split(rest, "."), true
}

// schemaHasPath reports whether a JSON Schema describes the path: object
// keys through properties, "*" through items. A schema that leaves an
// object open (no properties) accepts any key below it.
func schemaHasPath(schema json.RawMessage, segs []string) bool {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Items      json.RawMessage            `json:"items"`
		Ref        string                     `json:"$ref"`
	}
	if len(segs) == 0 {
		return true
	}
	if json.Unmarshal(schema, &s) != nil {
		return false
	}
	if s.Ref != "" {
		return true // a reference to another action's output: trust it
	}
	if segs[0] == "*" {
		return len(s.Items) > 0 && schemaHasPath(s.Items, segs[1:])
	}
	if s.Properties == nil {
		return len(s.Items) == 0 // an open object
	}
	sub, ok := s.Properties[segs[0]]
	return ok && schemaHasPath(sub, segs[1:])
}

// MustParse parses a manifest or panics; for connectors compiled into the binary.
func MustParse(src []byte) *Manifest {
	m, probs := Parse(src)
	if len(probs) > 0 {
		panic("connector manifest: " + strings.Join(probs, "; "))
	}
	return m
}

// Hosts are the hosts this connector may reach: egress_hosts, or else the
// base_url host.
func (m *Manifest) Hosts() []string {
	if len(m.Egress) > 0 {
		return m.Egress
	}
	if u, err := url.Parse(m.BaseURL); err == nil && u.Hostname() != "" {
		return []string{u.Hostname()}
	}
	return nil
}

// HostsFor are the hosts this connector may reach for a connection: Hosts,
// with "${connection.<field>}" standing for the connection's field (a
// database host, or the host of a server URL the tenant configured, such
// as a self-hosted provider's). A field left empty adds nothing.
func (m *Manifest) HostsFor(creds map[string]string) []string {
	var out []string
	for _, h := range m.Hosts() {
		if f, ok := strings.CutPrefix(h, "${connection."); ok && strings.HasSuffix(f, "}") {
			h = strings.TrimSpace(creds[strings.TrimSuffix(f, "}")])
			if u, err := url.Parse(h); err == nil && u.Hostname() != "" && strings.Contains(h, "://") {
				h = u.Hostname()
			}
		}
		if h != "" && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	return out
}

// OverrideBaseURL points the connector at another endpoint (a provider
// sandbox, a test server) and makes that endpoint's host the only one it may
// reach. It is platform configuration, never tenant input.
func (m *Manifest) OverrideBaseURL(base string) {
	if base == "" {
		return
	}
	m.BaseURL = base
	if u, err := url.Parse(base); err == nil && u.Hostname() != "" {
		m.Egress = []string{u.Hostname()}
	}
}

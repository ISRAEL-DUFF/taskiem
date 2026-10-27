// Package connector validates connector manifests against the connector/v1
// contract: the JSON Schema plus the registration rules in
// docs/contracts/connector-v1.md.
package connector

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
	Description string   `json:"description"`
	Auth        Auth     `json:"auth"`
	BaseURL     string   `json:"base_url"`
	Egress      []string `json:"egress_hosts"`
	// ConnectionHosts are the hosts a connection's fields may name, where
	// the provider keeps each account in one place (Zoho's data centres:
	// *.zoho.com, *.zoho.eu…; rule 19): a ${connection.<field>} egress host
	// or OAuth URL naming any other host is refused. Empty: any public host.
	ConnectionHosts []string               `json:"connection_hosts,omitempty"`
	Actions         map[string]ActionSpec  `json:"actions"`
	Triggers        map[string]TriggerSpec `json:"triggers"`
	// Capabilities and Events say what a rail can do in Taskiem's words
	// (rule 16, engine/capability).
	Capabilities map[string]CapabilityMap `json:"capabilities,omitempty"`
	// Pickers are lists a person chooses from instead of typing an id (a
	// Mailchimp audience, a Brevo list), each a read action and how its
	// output reads as choices: a template parameter names one (picker
	// choice).
	Pickers map[string]PickerSpec `json:"pickers,omitempty"`
	Events  map[string]EventMap   `json:"events,omitempty"`
}

type Auth struct {
	Type   string      `json:"type"`
	Fields []AuthField `json:"fields"`
	// OAuth2, when set, lets a connection be made by OAuth 2.0
	// authorization code grant with PKCE instead of pasted credentials
	// (docs/connector-sdk.md, decision 0034). The pasted fields stay.
	OAuth2 *OAuth2 `json:"oauth2,omitempty"`
	// HostModes are live and test as separate hosts (rule 17): the person
	// chooses one when connecting, the connection keeps it, and its calls go
	// to that mode's base URL. Keys that say their mode use prefixes.
	HostModes []HostMode `json:"modes,omitempty"`
	Test      *struct {
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
	// Client is the platform app Taskiem registered with the provider
	// (TASKIEM_OAUTH_<CLIENT>_*; rule 18): one app may serve several
	// connectors. Empty: the connector's id.
	Client string `json:"client,omitempty"`
	// AccountField is the auth field holding the provider account a
	// connection is for (Slack's team id): the app's events for that
	// account go to the connections that hold it.
	AccountField string `json:"account_field,omitempty"`
	// FieldsFromCallback fills auth fields from the query parameters the
	// provider sends the browser back with (QuickBooks' realmId).
	FieldsFromCallback map[string]string `json:"fields_from_callback,omitempty"`
	// ScopeSeparator joins the scopes on the consent URL: a space by
	// default (RFC 6749), a comma for Slack.
	ScopeSeparatorChar string `json:"scope_separator,omitempty"`
	// ClientAuth is how Taskiem's client proves itself at the token URL:
	// "body" (default: client_id and client_secret in the form) or "basic"
	// (HTTP Basic, as Intuit requires; RFC 6749 section 2.3.1).
	ClientAuth string `json:"client_auth,omitempty"`
	// RevokeEncoding is the revoke request's body: "form" (default, RFC
	// 7009) or "json" ({"token": …}, Intuit's).
	RevokeEncoding string `json:"revoke_encoding,omitempty"`
	// TokenMethod is how the code is exchanged: "post" (default, RFC
	// 6749) or "get" (the parameters in the query, Meta's Graph API).
	TokenMethod string `json:"token_method,omitempty"`
	// LinkedFields are auth fields the connector's Link fills when the
	// connection is made (Meta's Page and its token: rule 21).
	LinkedFields []string `json:"linked_fields,omitempty"`
}

// ScopeSeparator is what joins the scopes on the consent URL.
func (o *OAuth2) ScopeSeparator() string {
	if o.ScopeSeparatorChar == "" {
		return " "
	}
	return o.ScopeSeparatorChar
}

// ClientName is the platform app a connector's OAuth uses: Client, or the
// connector's id.
func (m *Manifest) ClientName() string {
	if m.Auth.OAuth2 != nil && m.Auth.OAuth2.Client != "" {
		return m.Auth.OAuth2.Client
	}
	return m.ID
}

// ClientFor is the platform app for connections in a mode: the mode's own
// (Intuit's development keys reach only sandbox companies: rule 20), or
// ClientName.
func (m *Manifest) ClientFor(mode string) string {
	for _, h := range m.Auth.HostModes {
		if h.Mode == mode && h.Client != "" {
			return h.Client
		}
	}
	return m.ClientName()
}

// Clients is every platform app a connector's OAuth may use: ClientName,
// then the modes' own.
func (m *Manifest) Clients() []string {
	out := []string{m.ClientName()}
	for _, h := range m.Auth.HostModes {
		if h.Client != "" && !slices.Contains(out, h.Client) {
			out = append(out, h.Client)
		}
	}
	return out
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
// HostMode is one of a connector's modes and where its calls go. An empty
// BaseURL is a mode only the deployment can point somewhere (a production
// address issued at go-live): it is offered once SetModeURL gives it one.
type HostMode struct {
	Mode    string `json:"mode"`
	Label   string `json:"label"`
	BaseURL string `json:"base_url,omitempty"`
	// Client is the platform app connections in this mode are linked with
	// by OAuth, where it is not the connector's (rule 20).
	Client string `json:"client,omitempty"`
}

// ModeKey is where a connection's mode reaches its connector: the vault
// adds it to the credentials it hands out (it is never stored with them).
const ModeKey = "$mode"

// Mode is the mode of the connection the request is for: live, test, or
// "" for a connection made before its connector had modes.
func (r Request) Mode() string { return r.Credentials[ModeKey] }

// ModeURL is where a mode's calls go, and whether this deployment offers
// the mode (it has an address).
func (m *Manifest) ModeURL(mode string) (string, bool) {
	for _, h := range m.Auth.HostModes {
		if h.Mode == mode {
			return h.BaseURL, h.BaseURL != ""
		}
	}
	return "", false
}

// BaseFor is where a connection's calls go: its mode's address, or the
// base URL for a connection made before its connector had modes. A mode
// this deployment has no address for is fatal: nothing is sent.
func (m *Manifest) BaseFor(creds map[string]string) (string, error) {
	mode := creds[ModeKey]
	if mode == "" || len(m.Auth.HostModes) == 0 {
		return strings.TrimRight(m.BaseURL, "/"), nil
	}
	base, ok := m.ModeURL(mode)
	if !ok {
		return "", fmt.Errorf("%s: this connection is in %s mode, which this Taskiem has no address for: %w", m.Name, mode, effects.ErrFatal)
	}
	return strings.TrimRight(base, "/"), nil
}

// SetModeURL points a mode at another address (the production address
// issued at go-live, a test server). It is platform configuration, never
// tenant input; an empty base leaves the mode as it is.
func (m *Manifest) SetModeURL(mode, base string) {
	for i := range m.Auth.HostModes {
		if m.Auth.HostModes[i].Mode == mode && base != "" {
			m.Auth.HostModes[i].BaseURL = base
		}
	}
}

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
	for _, h := range a.HostModes {
		out = append(out, KeyPrefix{Mode: h.Mode, Label: h.Label})
	}
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

	if o := m.Auth.OAuth2; o != nil {
		for _, u := range []string{o.AuthorizeURL, o.TokenURL, o.RevokeURL} {
			if strings.HasPrefix(u, connectionPlaceholder) && len(m.ConnectionHosts) == 0 {
				add("/auth/oauth2: %s takes its server from a connection field, so connection_hosts must list the provider's servers", u)
			}
		}
		if strings.HasPrefix(o.AuthorizeURL, connectionPlaceholder) {
			add("/auth/oauth2/authorize_url: the consent page is reached before the connection has fields")
		}
	}
	for _, tname := range slices.Sorted(maps.Keys(m.Triggers)) {
		t := m.Triggers[tname]
		p := "/triggers/" + tname
		if hs := t.Handshake; hs != nil && hs.Method == "GET" && hs.SecretField == "" && !hs.Open && t.Delivery != "app" {
			add("%s/handshake: a GET check needs secret_field, the connection's token it is compared with", p)
		}
		if !t.AppDelivered() {
			if t.AccountKey != "" || t.Revokes != "" {
				add("%s: account_key and revokes are for delivery: app or both", p)
			}
			continue
		}
		if t.Delivery == "both" && (t.Verify == nil || t.Verify.SecretField == "") {
			add("%s: delivery both is also verified with the connection's secret: verify needs secret_field", p)
		}
		switch {
		case m.Auth.OAuth2 == nil || m.Auth.OAuth2.AccountField == "":
			add("%s: delivery app needs auth.oauth2.account_field, the account a connection is for", p)
		case t.AccountKey == "":
			add("%s: delivery app needs account_key, the account an event is for", p)
		case t.Registered() || t.Route != nil || t.Poll != nil:
			add("%s: delivery app takes no remote registration, route or poll", p)
		case t.Verify == nil || slices.Contains([]string{"query_secret", "path_secret", "none"}, t.Verify.Scheme):
			add("%s: delivery app is verified with the app's secret: a signature scheme, not %v", p, t.Verify)
		}
	}
	for i, h := range m.Auth.HostModes {
		if slices.ContainsFunc(m.Auth.HostModes[:i], func(x HostMode) bool { return x.Mode == h.Mode }) {
			add("/auth/modes/%d: mode %q is named twice", i, h.Mode)
		}
	}
	if len(m.Auth.HostModes) > 0 {
		for _, f := range m.Auth.Fields {
			if slices.ContainsFunc(f.Prefixes, func(p KeyPrefix) bool { return p.Mode != "" }) {
				add("/auth/modes: a connector's modes are hosts or key prefixes, not both (field %s)", f.Key)
			}
		}
	}

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
		if v := m.Triggers[name].Verify; v != nil && v.Scheme == "body_token" && v.Field == "" {
			add("/triggers/%s/verify/field: body_token names the body field carrying the token", name)
		}
		// An ack is served from the hooks origin: never as a page.
		if a := m.Triggers[name].Ack; a != nil && a.ContentType != "" {
			if mt, _, err := mime.ParseMediaType(a.ContentType); err != nil || (mt != "text/plain" && mt != "application/json") {
				add("/triggers/%s/ack/content_type: must be text/plain or application/json, not %q", name, a.ContentType)
			}
		}
		// A remote trigger is verified with the secret the provider returned
		// when Taskiem created the subscription, never a connection field.
		// A connection's subscription is too, and a subscription made by
		// hand with the connection's secret_field.
		if t := m.Triggers[name]; t.Registered() {
			switch {
			case t.Type != "webhook":
				add("/triggers/%s/registration: only webhook triggers are registered remotely", name)
			case t.PerConnection() && (t.Verify == nil || t.Verify.SecretField == ""):
				add("/triggers/%s/verify/secret_field: a connection's subscription may also be made by hand, verified with this connection field", name)
			case t.Verify == nil:
			case t.Remote() && t.Verify.SecretField != "":
				add("/triggers/%s/verify/secret_field: a remote trigger is verified with the secret the provider returned", name)
			case !slices.Contains([]string{"hmac_sha256", "hmac_sha512", "hmac_sha1", "hmac_sha256_timestamped", "slack_v0", "header_secret", "bearer", "body_token", "svix"}, t.Verify.Scheme):
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
			} else if p, err := url.Parse(exampleOAuthURL(u)); err != nil || p.Scheme != "https" || p.Hostname() == "" || p.Fragment != "" {
				add("/auth/oauth2/%s: must be an https URL without a fragment", name)
			}
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
		for _, k := range slices.Sorted(maps.Keys(o.FieldsFromCallback)) {
			if !slices.ContainsFunc(m.Auth.Fields, func(f AuthField) bool { return f.Key == k }) {
				add("/auth/oauth2/fields_from_callback/%s: not an auth field", k)
			}
		}
		for _, k := range o.LinkedFields {
			if !slices.ContainsFunc(m.Auth.Fields, func(f AuthField) bool { return f.Key == k }) {
				add("/auth/oauth2/linked_fields: %q is not an auth field", k)
			}
		}
		if o.AccountField != "" && !slices.ContainsFunc(m.Auth.Fields, func(f AuthField) bool { return f.Key == o.AccountField }) {
			add("/auth/oauth2/account_field: %q is not an auth field", o.AccountField)
		}
		for _, f := range m.Auth.Fields {
			_, fromToken := o.FieldsFromToken[f.Key]
			_, fromCallback := o.FieldsFromCallback[f.Key]
			_, fromClient := o.PlatformClientFields[f.Key]
			fromLink := slices.Contains(o.LinkedFields, f.Key)
			if !fromToken && !fromCallback && !fromClient && !fromLink && (f.Required == nil || *f.Required) {
				add("/auth/oauth2/fields_from_token: required field %q is not filled from the token or the callback; a linked connection would be missing it", f.Key)
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(m.Pickers)) {
		pk := m.Pickers[name]
		if a, ok := m.Actions[pk.Action]; !ok || a.Class.IsWrite() {
			add("/pickers/%s/action: %q is not a read action", name, pk.Action)
		}
	}
	for i, h := range m.Auth.HostModes {
		if h.Client != "" && m.Auth.OAuth2 == nil {
			add("/auth/modes/%d/client: a mode's platform app needs auth.oauth2", i)
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
	var out []string
	if len(m.Egress) > 0 {
		out = slices.Clone(m.Egress)
	} else if u, err := url.Parse(m.BaseURL); err == nil && u.Hostname() != "" {
		out = []string{u.Hostname()}
	}
	// Each mode's host, so a connection reaches the one it chose.
	for _, h := range m.Auth.HostModes {
		if u, err := url.Parse(h.BaseURL); err == nil && u.Hostname() != "" && !slices.Contains(out, u.Hostname()) {
			out = append(out, u.Hostname())
		}
	}
	return out
}

// ConnectionHostAllowed reports whether a host a connection's fields name
// is one the connector allows: an exact entry, or a subdomain of a *.
// entry. A connector that lists none allows any.
func (m *Manifest) ConnectionHostAllowed(host string) bool {
	if len(m.ConnectionHosts) == 0 {
		return true
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, p := range m.ConnectionHosts {
		if sfx, ok := strings.CutPrefix(p, "*."); ok {
			if strings.HasSuffix(host, "."+sfx) {
				return true
			}
		} else if host == p {
			return true
		}
	}
	return false
}

// exampleOAuthURL is an OAuth URL with its placeholders filled, to check
// its shape.
func exampleOAuthURL(u string) string {
	u = strings.Replace(u, ServerPlaceholder, "https://provider.example", 1)
	if rest, ok := strings.CutPrefix(u, connectionPlaceholder); ok {
		if _, path, ok := strings.Cut(rest, "}"); ok {
			u = "https://account.provider.example" + path
		}
	}
	return u
}

// connectionPlaceholder starts an OAuth URL taken from a connection's
// field: ${connection.accounts_server}/oauth/v2/token.
const connectionPlaceholder = "${connection."

// OAuthURL resolves an OAuth URL that starts with ${connection.<field>}
// from fields (Zoho's accounts server, which the callback names): the
// field must be an https URL on a host connection_hosts allows. Other URLs
// are returned as they are.
func (m *Manifest) OAuthURL(u string, fields map[string]string) (string, error) {
	rest, ok := strings.CutPrefix(u, connectionPlaceholder)
	if !ok {
		return u, nil
	}
	field, path, ok := strings.Cut(rest, "}")
	if !ok {
		return "", fmt.Errorf("%s: no closing }", u)
	}
	base := strings.TrimRight(strings.TrimSpace(fields[field]), "/")
	p, err := url.Parse(base)
	if base == "" || err != nil || p.Scheme != "https" || p.Hostname() == "" || (p.Path != "" && p.Path != "/") {
		return "", fmt.Errorf("%s: %q is not an https server address", field, base)
	}
	if len(m.ConnectionHosts) == 0 || !m.ConnectionHostAllowed(p.Hostname()) {
		return "", fmt.Errorf("%s: %s is not one of %s's servers", field, p.Hostname(), m.Name)
	}
	return base + path, nil
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
			if !m.ConnectionHostAllowed(h) {
				continue // a field naming a host the connector does not allow
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

// PickerSpec is one of a connector's pickers: a read action, and
// expressions over its output (items) and each item (id, label).
type PickerSpec struct {
	Action string `json:"action"`
	Items  string `json:"items"`
	ID     string `json:"id"`
	Label  string `json:"label"`
}

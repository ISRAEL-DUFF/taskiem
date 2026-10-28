package connector

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // some providers sign webhooks with HMAC-SHA1
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrBadSignature means a webhook failed verification (spec 8.2: reject
// with 401 before touching the database).
var ErrBadSignature = errors.New("webhook signature invalid")

// TriggerSpec is a manifest trigger's verification block.
type TriggerSpec struct {
	Type   string      `json:"type"`
	Verify *VerifySpec `json:"verify"`
	Events []string    `json:"events"`
	// Split, when set, turns one delivery into several events: a list
	// expression whose elements the other expressions see as item.
	Split       string `json:"split"`
	EventType   string `json:"event_type"`
	Dedup       string `json:"dedup"`
	Correlation string `json:"correlation"`
	// Handshake answers a provider's endpoint check (Slack's
	// url_verification, Meta's GET challenge).
	Handshake *HandshakeSpec `json:"handshake"`
	// Ack replaces the default 202 JSON answer to an accepted delivery.
	Ack *AckSpec `json:"ack"`
	// Registration remote: Taskiem creates, updates and deletes the
	// provider's subscription itself (decision 0021), and verifies
	// deliveries with the secret the provider returned, kept in the vault.
	Registration string `json:"registration"`
	// Options is the JSON Schema of the workflow trigger's options
	// (tables, columns...), given to the registrar and the enricher.
	Options json.RawMessage `json:"options"`
	// TestEvent, an expression, marks a delivery the provider sends to
	// test the endpoint: it is verified and acknowledged, and starts only
	// workflows that subscribe to its event by name.
	TestEvent string `json:"test_event"`
	// Route, when set, lets a partner hold the provider's one subscription
	// and deliver each event into the sub-tenants whose connection lists
	// the event's key (decision 0031).
	Route *RouteSpec `json:"route"`
	// Poll, when set, lets Taskiem ask the provider itself for what the
	// webhook would carry, for businesses that never set the webhook up in
	// the provider's dashboard (no-technical-steps N4).
	Poll *PollSpec `json:"poll"`
	// Title and Where are shown with the trigger's address on the
	// Connections page: what it hears, and where in the provider's
	// dashboard the business pastes it.
	Title string `json:"title,omitempty"`
	Where string `json:"where,omitempty"`
	// Delivery app: the provider sends every business's events to the one
	// URL of Taskiem's platform app (rule 18; decision 0051); AccountKey
	// says which account each is for, and the connections holding it get
	// it. Revokes marks an event that takes the app away (Slack's
	// app_uninstalled): those connections are marked expired.
	Delivery   string `json:"delivery,omitempty"`
	AccountKey string `json:"account_key,omitempty"`
	Revokes    string `json:"revokes,omitempty"`
	// PII is the personal data a delivery carries, as paths under the
	// run's trigger (body.…, item.…; * for every element): sealed in
	// history when a run starts (rule 21: a message's sender and text).
	PII []PIIField `json:"pii,omitempty"`
	// Example is a delivery as a run sees it ({body, item}), for dry runs
	// and the docs: a split trigger's item cannot be guessed.
	Example *TriggerExample `json:"example,omitempty"`
}

// TriggerExample is an example delivery: its body and, for a split
// trigger, one item.
type TriggerExample struct {
	Body any `json:"body,omitempty"`
	Item any `json:"item,omitempty"`
}

// AppDelivered reports whether the trigger's deliveries come (also) to
// Taskiem's platform app URL: delivery app, or both.
func (t TriggerSpec) AppDelivered() bool { return t.Delivery == "app" || t.Delivery == "both" }

// PollSpec is how a webhook trigger's events are also polled: Action, a
// read action, takes Since (an input field) set to a time and answers
// records; Items (over output) lists them; Event (over item) names each
// one's event. Each becomes the body {event, data: item}, signed as the
// provider signs, and is delivered through the webhook path.
type PollSpec struct {
	Action string `json:"action"`
	Since  string `json:"since"`
	Items  string `json:"items"`
	Event  string `json:"event"`
	Every  string `json:"every"`
}

// Interval is how often to poll (default five minutes).
func (p *PollSpec) Interval() time.Duration {
	if d, err := time.ParseDuration(p.Every); err == nil && d >= time.Minute {
		return d
	}
	return 5 * time.Minute
}

// SignBody signs body as a provider using v would, for a trigger Taskiem
// polls: the header and its value. Only plain HMAC schemes can be.
func SignBody(v *VerifySpec, secret string, body []byte) (header, value string, err error) {
	if v == nil || secret == "" {
		return "", "", fmt.Errorf("%w: nothing to sign with", ErrBadSignature)
	}
	var mac func() hash.Hash
	switch v.Scheme {
	case "hmac_sha256":
		mac = sha256.New
	case "hmac_sha512":
		mac = sha512.New
	default:
		return "", "", fmt.Errorf("%w: %s deliveries cannot be signed by Taskiem", ErrBadSignature, v.Scheme)
	}
	if v.KeyDerivation != "" && v.KeyDerivation != "none" {
		return "", "", fmt.Errorf("%w: key derivation %s is not supported for polling", ErrBadSignature, v.KeyDerivation)
	}
	h := hmac.New(mac, []byte(secret))
	h.Write(body)
	sum := h.Sum(nil)
	switch v.Encoding {
	case "", "hex":
		return v.Header, hex.EncodeToString(sum), nil
	case "base64":
		return v.Header, base64.StdEncoding.EncodeToString(sum), nil
	}
	return "", "", fmt.Errorf("%w: encoding %s is not supported for polling", ErrBadSignature, v.Encoding)
}

// RouteSpec routes a partner's delivery to its sub-tenants: Key, an
// expression over the delivery (and item), names the account the event is
// about; Field names the connection field (a list) holding the accounts a
// connection may use.
type RouteSpec struct {
	Key   string `json:"key"`
	Field string `json:"field"`
}

// Remote reports whether Taskiem registers this trigger at the provider
// for each workflow deployed with it.
func (t TriggerSpec) Remote() bool { return t.Registration == "remote" }

// PerConnection reports whether Taskiem registers this trigger at the
// provider once for each connection, whichever workflows use it.
func (t TriggerSpec) PerConnection() bool { return t.Registration == "connection" }

// Registered reports whether Taskiem registers this trigger at the
// provider at all, so the connector supplies a registrar for it.
func (t TriggerSpec) Registered() bool { return t.Remote() || t.PerConnection() }

// HandshakeSpec is a trigger's endpoint check. GET: the query parameter
// TokenQuery must equal the connection's SecretField, and Respond (over
// query) is returned as text. POST: a delivery that passed verification
// and for which When holds is answered with Respond instead of delivered.
type HandshakeSpec struct {
	Method      string `json:"method"`
	When        string `json:"when"`
	Respond     string `json:"respond"`
	TokenQuery  string `json:"token_query"`
	SecretField string `json:"secret_field"`
	// Open: a GET check that only asks whether the address answers
	// (Mailchimp's before it saves a webhook): 200, with no token, nothing
	// read and nothing started.
	Open bool `json:"open,omitempty"`
}

type VerifySpec struct {
	Scheme          string `json:"scheme"`
	Header          string `json:"header"`
	SecretField     string `json:"secret_field"`
	TimestampHeader string `json:"timestamp_header"`
	Tolerance       string `json:"tolerance"`
	Encoding        string `json:"encoding"`       // hex (default), base64, base64_of_hex
	KeyDerivation   string `json:"key_derivation"` // none (default), sha256_hex
	// Query names the URL parameter carrying the secret (query_secret).
	Query string `json:"query"`
	// Field names the JSON body's top-level field carrying the secret
	// (body_token: Zoho CRM's channel token).
	Field string `json:"field"`
	// SignatureFormat t_v1 (hmac_sha256_timestamped): the header carries
	// "t=<unix>,v1=<hex>[,v1=<hex>...]" instead of a timestamp header; any
	// v1 value may match, so a provider can sign with an old and a new
	// secret while it rotates.
	SignatureFormat string `json:"signature_format"`
}

// AckSpec is the answer a provider expects to a delivery it made.
type AckSpec struct {
	Status      int    `json:"status"`
	Body        string `json:"body"`
	ContentType string `json:"content_type"`
}

// VerifyQuerySecret checks a query_secret delivery: the URL parameter
// named by v.Query must equal the secret.
func VerifyQuerySecret(v *VerifySpec, secret string, q url.Values) error {
	got := q.Get(v.Query)
	if v.Query == "" || secret == "" || got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		return ErrBadSignature
	}
	return nil
}

// VerifyPathSecret checks a path_secret delivery: the last segment of the
// path form (/{env}/{connection}/{token}) must equal the secret.
func VerifyPathSecret(secret, token string) error {
	if secret == "" || token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(secret)) != 1 {
		return ErrBadSignature
	}
	return nil
}

// DefaultTolerance is how far a timestamped signature may be from now.
const DefaultTolerance = 5 * time.Minute

// Now is the clock for timestamped signatures; tests replace it.
var Now = time.Now

// VerifyWebhook checks a delivery against the manifest's scheme. secret is
// the connection credential named by secret_field.
func VerifyWebhook(v *VerifySpec, secret string, h http.Header, body []byte) error {
	if v == nil {
		return fmt.Errorf("%w: trigger has no verification", ErrBadSignature)
	}
	got := strings.TrimSpace(h.Get(v.Header))
	var mac func() hash.Hash
	signed := body
	switch v.Scheme {
	case "hmac_sha256_timestamped":
		mac = sha256.New
		ts := strings.TrimSpace(h.Get(v.TimestampHeader))
		if v.SignatureFormat == "t_v1" {
			var sigs []string
			ts, sigs = parseTV1(got)
			if len(sigs) == 0 {
				return fmt.Errorf("%w: no v1 signature in %s", ErrBadSignature, v.Header)
			}
			if err := checkTimestamp(v, ts); err != nil {
				return err
			}
			return verifyAny(secret, append([]byte(ts+"."), body...), sigs)
		}
		sec, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			return fmt.Errorf("%w: missing or bad %s", ErrBadSignature, v.TimestampHeader)
		}
		tol := DefaultTolerance
		if v.Tolerance != "" {
			if d, err := time.ParseDuration(v.Tolerance); err == nil {
				tol = d
			}
		}
		if skew := Now().Sub(time.Unix(sec, 0)); skew > tol || skew < -tol {
			return fmt.Errorf("%w: timestamp outside %s", ErrBadSignature, tol)
		}
		signed = append([]byte(ts+"."), body...)
	case "slack_v0":
		mac = sha256.New
		ts := strings.TrimSpace(h.Get(v.TimestampHeader))
		sec, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			return fmt.Errorf("%w: missing or bad %s", ErrBadSignature, v.TimestampHeader)
		}
		tol := DefaultTolerance
		if v.Tolerance != "" {
			if d, err := time.ParseDuration(v.Tolerance); err == nil {
				tol = d
			}
		}
		if skew := Now().Sub(time.Unix(sec, 0)); skew > tol || skew < -tol {
			return fmt.Errorf("%w: timestamp outside %s", ErrBadSignature, tol)
		}
		signed = append([]byte("v0:"+ts+":"), body...)
		got = strings.TrimPrefix(got, "v0=")
	case "hmac_sha256":
		mac = sha256.New
	case "hmac_sha512":
		mac = sha512.New
	case "hmac_sha1":
		mac = sha1.New
	case "header_secret":
		if secret == "" || got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
			return ErrBadSignature
		}
		return nil
	case "bearer":
		want := "Bearer " + secret
		if secret == "" || subtle.ConstantTimeCompare([]byte(h.Get("Authorization")), []byte(want)) != 1 {
			return ErrBadSignature
		}
		return nil
	case "none":
		return nil
	case "body_token":
		// The provider echoes the token it was given in the body (Zoho
		// CRM's notifications): nothing is signed, so it is compared in
		// constant time and only over a body that parses.
		var b map[string]any
		if secret == "" || v.Field == "" || json.Unmarshal(body, &b) != nil {
			return ErrBadSignature
		}
		got, _ := b[v.Field].(string)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
			return ErrBadSignature
		}
		return nil
	case "svix":
		return verifySvix(v, secret, h, body)
	case "basic":
		// The secret is "user:password", sent as HTTP Basic credentials.
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte(secret))
		if secret == "" || subtle.ConstantTimeCompare([]byte(h.Get("Authorization")), []byte(want)) != 1 {
			return ErrBadSignature
		}
		return nil
	case "query_secret", "path_secret":
		return fmt.Errorf("%w: %s is checked against the URL", ErrBadSignature, v.Scheme)
	case "connector":
		return fmt.Errorf("%w: the connector verifies this trigger itself", ErrBadSignature)
	default:
		return fmt.Errorf("%w: unsupported scheme %q", ErrBadSignature, v.Scheme)
	}
	if secret == "" || got == "" {
		return ErrBadSignature
	}
	key := []byte(secret)
	if v.KeyDerivation == "sha256_hex" {
		sum := sha256.Sum256(key)
		key = []byte(hex.EncodeToString(sum[:]))
	}
	m := hmac.New(mac, key)
	m.Write(signed)
	digest := m.Sum(nil)
	var want string
	switch v.Encoding {
	case "base64":
		want = base64.StdEncoding.EncodeToString(digest)
	case "base64_of_hex":
		want = base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(digest)))
	default:
		want = hex.EncodeToString(digest)
		got = strings.TrimPrefix(strings.ToLower(got), "sha256=")
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return ErrBadSignature
	}
	return nil
}

// parseTV1 reads a "t=<unix>,v1=<hex>,v1=<hex>" signature header: the
// timestamp and every v1 signature (several while the provider rotates its
// secret). Unknown parts (other versions) are ignored.
func parseTV1(header string) (ts string, sigs []string) {
	for _, part := range strings.Split(header, ",") {
		k, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts = val
		case "v1":
			if val != "" {
				sigs = append(sigs, strings.ToLower(val))
			}
		}
	}
	return ts, sigs
}

func checkTimestamp(v *VerifySpec, ts string) error {
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: missing or bad timestamp", ErrBadSignature)
	}
	tol := DefaultTolerance
	if v.Tolerance != "" {
		if d, err := time.ParseDuration(v.Tolerance); err == nil {
			tol = d
		}
	}
	if skew := Now().Sub(time.Unix(sec, 0)); skew > tol || skew < -tol {
		return fmt.Errorf("%w: timestamp outside %s", ErrBadSignature, tol)
	}
	return nil
}

// verifyAny accepts signed when any of sigs is its hex HMAC-SHA256 under
// secret. Every candidate is compared, in constant time each.
func verifyAny(secret string, signed []byte, sigs []string) error {
	if secret == "" {
		return ErrBadSignature
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(signed)
	want := []byte(hex.EncodeToString(m.Sum(nil)))
	ok := 0
	for _, s := range sigs {
		ok |= subtle.ConstantTimeCompare([]byte(s), want)
	}
	if ok != 1 {
		return ErrBadSignature
	}
	return nil
}

// verifySvix checks a delivery signed by Svix, as Resend's are (the
// Standard Webhooks scheme): an HMAC-SHA256, base64-encoded, of
// "<id>.<timestamp>.<raw body>" under the secret's key (the base64 after
// "whsec_"). The id, timestamp and signatures come in svix-id,
// svix-timestamp and svix-signature (or the webhook-* names); the last is
// space-separated "v1,<base64>" values, any of which may match while the
// sender rotates its secret.
func verifySvix(v *VerifySpec, secret string, h http.Header, body []byte) error {
	get := func(name string) string {
		if s := strings.TrimSpace(h.Get("svix-" + name)); s != "" {
			return s
		}
		return strings.TrimSpace(h.Get("webhook-" + name))
	}
	id, ts, sigs := get("id"), get("timestamp"), get("signature")
	if id == "" || ts == "" || sigs == "" {
		return fmt.Errorf("%w: missing svix-id, svix-timestamp or svix-signature", ErrBadSignature)
	}
	if err := checkTimestamp(v, ts); err != nil {
		return err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if secret == "" || err != nil || len(key) == 0 {
		return fmt.Errorf("%w: the secret is not a whsec_ key", ErrBadSignature)
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(id + "." + ts + "."))
	m.Write(body)
	want := []byte(base64.StdEncoding.EncodeToString(m.Sum(nil)))
	ok := 0
	for _, part := range strings.Fields(sigs) {
		if version, sig, found := strings.Cut(part, ","); found && version == "v1" {
			ok |= subtle.ConstantTimeCompare([]byte(sig), want)
		}
	}
	if ok != 1 {
		return ErrBadSignature
	}
	return nil
}

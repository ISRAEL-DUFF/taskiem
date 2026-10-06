package connector

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // some providers sign webhooks with HMAC-SHA1
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"net/http"
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
}

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
}

type VerifySpec struct {
	Scheme          string `json:"scheme"`
	Header          string `json:"header"`
	SecretField     string `json:"secret_field"`
	TimestampHeader string `json:"timestamp_header"`
	Tolerance       string `json:"tolerance"`
	Encoding        string `json:"encoding"`       // hex (default), base64, base64_of_hex
	KeyDerivation   string `json:"key_derivation"` // none (default), sha256_hex
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

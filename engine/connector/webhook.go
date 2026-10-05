package connector

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"net/http"
	"strings"
)

// ErrBadSignature means a webhook failed verification (spec 8.2: reject
// with 401 before touching the database).
var ErrBadSignature = errors.New("webhook signature invalid")

// TriggerSpec is a manifest trigger's verification block.
type TriggerSpec struct {
	Type        string      `json:"type"`
	Verify      *VerifySpec `json:"verify"`
	Events      []string    `json:"events"`
	EventType   string      `json:"event_type"`
	Dedup       string      `json:"dedup"`
	Correlation string      `json:"correlation"`
}

type VerifySpec struct {
	Scheme      string `json:"scheme"`
	Header      string `json:"header"`
	SecretField string `json:"secret_field"`
}

// VerifyWebhook checks a delivery against the manifest's scheme. secret is
// the connection credential named by secret_field.
func VerifyWebhook(v *VerifySpec, secret string, h http.Header, body []byte) error {
	if v == nil {
		return fmt.Errorf("%w: trigger has no verification", ErrBadSignature)
	}
	got := strings.TrimSpace(h.Get(v.Header))
	var mac func() hash.Hash
	switch v.Scheme {
	case "hmac_sha256":
		mac = sha256.New
	case "hmac_sha512":
		mac = sha512.New
	case "bearer":
		want := "Bearer " + secret
		if secret == "" || subtle.ConstantTimeCompare([]byte(h.Get("Authorization")), []byte(want)) != 1 {
			return ErrBadSignature
		}
		return nil
	case "none":
		return nil
	default:
		return fmt.Errorf("%w: unsupported scheme %q", ErrBadSignature, v.Scheme)
	}
	if secret == "" || got == "" {
		return ErrBadSignature
	}
	m := hmac.New(mac, []byte(secret))
	m.Write(body)
	want := hex.EncodeToString(m.Sum(nil))
	got = strings.TrimPrefix(strings.ToLower(got), "sha256=")
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return ErrBadSignature
	}
	return nil
}

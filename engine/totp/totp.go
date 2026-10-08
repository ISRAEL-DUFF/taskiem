// Package totp implements time-based one-time passwords (RFC 6238 over
// RFC 4226: HMAC-SHA1, 30-second steps, 6 digits), the step-up factor for
// approvals that a policy marks as needing one (spec 9.1).
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 authenticator apps use HMAC-SHA1
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	period = 30
	digits = 6
	// skew accepts the previous and next code, for clock drift.
	skew = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a random 160-bit secret, base32-encoded.
func NewSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b32.EncodeToString(b), nil
}

// URI is the otpauth:// link authenticator apps scan as a QR code.
func URI(secret, issuer, account string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {fmt.Sprint(digits)}, "period": {fmt.Sprint(period)}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// Code returns the code for a time step.
func Code(secret string, step int64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("totp: secret is not base32: %w", err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // time steps are positive
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1_000_000), nil
}

// Step is the time step containing t.
func Step(t time.Time) int64 { return t.Unix() / period }

// Verify checks code against the steps around now, refusing any step at or
// before lastUsed so a code works once. It returns the step matched.
func Verify(secret, code string, now time.Time, lastUsed int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != digits {
		return 0, false
	}
	cur := Step(now)
	for s := cur - skew; s <= cur+skew; s++ {
		if s <= lastUsed {
			continue
		}
		want, err := Code(secret, s)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}

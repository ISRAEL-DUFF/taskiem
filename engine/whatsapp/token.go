package whatsapp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Signed decision tokens (spec 9.1): an approval button or a step-up link
// carries a token bound to the tenant, run, step, approval level, the
// approver, the decision and an expiry, with a random nonce. The token
// holds the tenant, nonce, purpose, decision and expiry, and an HMAC-SHA256
// under the platform key over those and the rest (run, step, level,
// approver), which are kept in the tenant's whatsapp_tokens row under the
// nonce. A token verifies only with the key and the row, and the row makes
// it single use. Tokens fit a quick-reply payload (under 128 characters).

// Token purposes.
const (
	PurposeDecide  = "decide"  // an Approve or Reject button
	PurposeHandoff = "handoff" // a step-up link to the web app
)

// ErrBadToken: a token is malformed, forged, altered or expired.
var ErrBadToken = errors.New("whatsapp: invalid decision token")

// Claims is what a token is bound to.
type Claims struct {
	Tenant   uuid.UUID
	Nonce    [12]byte
	Purpose  string
	Decision string // approved | rejected
	Expires  time.Time
	Run      uuid.UUID
	Step     string
	Level    int
	User     uuid.UUID
}

// NewNonce returns a random nonce.
func NewNonce() [12]byte {
	var n [12]byte
	_, _ = rand.Read(n[:])
	return n
}

// Signer signs and checks tokens with the platform key.
type Signer struct{ Key []byte }

const tokenPrefix = "tk1."

var b64 = base64.RawURLEncoding

func purposeByte(p string) byte {
	if p == PurposeHandoff {
		return 'h'
	}
	return 'd'
}

func decisionByte(d string) byte {
	if d == "rejected" {
		return 'r'
	}
	return 'a'
}

func header(c Claims) []byte {
	h := make([]byte, 0, 34)
	h = append(h, c.Tenant[:]...)
	h = append(h, c.Nonce[:]...)
	h = append(h, purposeByte(c.Purpose), decisionByte(c.Decision))
	return binary.BigEndian.AppendUint32(h, uint32(c.Expires.Unix())) //nolint:gosec // seconds until 2106
}

func (s Signer) mac(c Claims) []byte {
	m := hmac.New(sha256.New, s.Key)
	m.Write([]byte("taskiem/whatsapp-token/v1\x00"))
	m.Write(header(c))
	m.Write(c.Run[:])
	m.Write(binary.BigEndian.AppendUint32(nil, uint32(len(c.Step)))) //nolint:gosec // step ids are short
	m.Write([]byte(c.Step))
	m.Write(binary.BigEndian.AppendUint32(nil, uint32(c.Level))) //nolint:gosec // small level index
	m.Write(c.User[:])
	return m.Sum(nil)
}

// Sign returns the token for c.
func (s Signer) Sign(c Claims) string {
	return tokenPrefix + b64.EncodeToString(header(c)) + "." + b64.EncodeToString(s.mac(c))
}

// ParseToken reads a token's header: tenant, nonce, purpose, decision and
// expiry, which say where to find the rest. Nothing is trusted until
// Verify.
func ParseToken(tok string) (Claims, []byte, error) {
	var c Claims
	rest, ok := strings.CutPrefix(tok, tokenPrefix)
	if !ok || len(tok) > 200 {
		return c, nil, ErrBadToken
	}
	h64, m64, ok := strings.Cut(rest, ".")
	if !ok {
		return c, nil, ErrBadToken
	}
	h, err1 := b64.DecodeString(h64)
	mac, err2 := b64.DecodeString(m64)
	if err1 != nil || err2 != nil || len(h) != 34 || len(mac) != sha256.Size {
		return c, nil, ErrBadToken
	}
	copy(c.Tenant[:], h[:16])
	copy(c.Nonce[:], h[16:28])
	switch h[28] {
	case 'd':
		c.Purpose = PurposeDecide
	case 'h':
		c.Purpose = PurposeHandoff
	default:
		return c, nil, ErrBadToken
	}
	switch h[29] {
	case 'a':
		c.Decision = "approved"
	case 'r':
		c.Decision = "rejected"
	default:
		return c, nil, ErrBadToken
	}
	c.Expires = time.Unix(int64(binary.BigEndian.Uint32(h[30:34])), 0)
	return c, mac, nil
}

// Verify checks a token's MAC against the full claims (the header from
// ParseToken with the run, step, level and approver from the stored row),
// and its expiry.
func (s Signer) Verify(c Claims, mac []byte, now time.Time) error {
	if len(s.Key) < 32 || !hmac.Equal(mac, s.mac(c)) {
		return ErrBadToken
	}
	if !now.Before(c.Expires) {
		return ErrBadToken
	}
	return nil
}

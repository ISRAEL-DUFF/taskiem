// Package formguard defends the public storefront pages and hosted forms
// against bots and double submissions without cookies or third-party
// captchas (docs/spec/storefronts.md 6.4 and 7.5, docs/spec/forms.md 7.3).
//
// Visitors are mostly on cheap Android phones on 3G, and Nigerian mobile
// networks put thousands of phones behind one address (carrier-grade
// NAT), so limits per IP are generous and the real defences are a signed
// token per form (which also makes a submission idempotent), a minimum
// fill time, a honeypot field and an optional proof-of-work.
package formguard

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// Errors of Verify and VerifyPoW, for errors.Is.
var (
	// ErrInvalid: the token is malformed, forged, altered or for another
	// form.
	ErrInvalid = errors.New("formguard: invalid token")
	// ErrExpired: the token is older than MaxAge; the page should be
	// reloaded.
	ErrExpired = errors.New("formguard: token expired")
	// ErrTooFast: the form came back sooner than a person could fill it
	// in. Callers answer softly (show the form again, keep the answers),
	// since a person who types fast must not lose an order.
	ErrTooFast = errors.New("formguard: submitted too fast")
	// ErrPoW: the proof-of-work counter is missing or wrong.
	ErrPoW = errors.New("formguard: proof-of-work failed")
)

const (
	// KeySize is the size of every HMAC key.
	KeySize = 32
	// MaxForm bounds a form id, in bytes.
	MaxForm = 128
	// MaxPoW bounds the difficulty: the counter is a uint32, so beyond 32
	// bits a solution may not exist.
	MaxPoW = 32
	// DefaultMinFill is the shortest time a person takes to fill a form.
	DefaultMinFill = 3 * time.Second
	// DefaultMaxAge is how long a form page stays usable.
	DefaultMaxAge = 2 * time.Hour

	tokenVersion = 1
	nonceSize    = 16
	tagSize      = 16 // HMAC-SHA256 truncated: 128 bits is ample against forgery
	// version, issued-at, nonce, difficulty, form length, then the form.
	headerSize = 1 + 8 + nonceSize + 1 + 1
	maxToken   = headerSize + MaxForm + tagSize
)

// Guard issues and checks form tokens. It is safe for concurrent use.
type Guard struct {
	keys    [][]byte // keys[0] signs; all verify
	now     func() time.Time
	minFill time.Duration
	maxAge  time.Duration
}

// Option configures a Guard.
type Option func(*Guard)

// WithClock sets the clock used when Issue or Verify is given a zero time.
func WithClock(now func() time.Time) Option { return func(g *Guard) { g.now = now } }

// WithMinFill sets the minimum fill time (default DefaultMinFill).
func WithMinFill(d time.Duration) Option { return func(g *Guard) { g.minFill = d } }

// WithMaxAge sets how long a token is valid (default DefaultMaxAge).
func WithMaxAge(d time.Duration) Option { return func(g *Guard) { g.maxAge = d } }

// WithPreviousKeys adds keys that still verify but no longer sign, so a
// key can be rotated without failing the forms open in visitors' browsers.
// Keep a previous key for at least MaxAge after rotating.
func WithPreviousKeys(keys ...[]byte) Option {
	return func(g *Guard) {
		for _, k := range keys {
			g.keys = append(g.keys, bytes.Clone(k))
		}
	}
}

// New makes a Guard that signs with key. Every key must be KeySize bytes
// and not all zero.
func New(key []byte, opts ...Option) (*Guard, error) {
	g := &Guard{keys: [][]byte{bytes.Clone(key)}, now: time.Now, minFill: DefaultMinFill, maxAge: DefaultMaxAge}
	for _, o := range opts {
		o(g)
	}
	for i, k := range g.keys {
		if len(k) != KeySize {
			return nil, fmt.Errorf("formguard: key %d is %d bytes, want %d", i, len(k), KeySize)
		}
		if bytes.Equal(k, make([]byte, KeySize)) {
			return nil, fmt.Errorf("formguard: key %d is all zero", i)
		}
	}
	if g.now == nil {
		g.now = time.Now
	}
	if g.minFill < 0 || g.maxAge <= g.minFill {
		return nil, fmt.Errorf("formguard: need 0 <= MinFill (%s) < MaxAge (%s)", g.minFill, g.maxAge)
	}
	return g, nil
}

// Token is a signed form token: base64url without padding, safe in a URL,
// a hidden input or a data attribute. It holds the version, the issue time
// (unix seconds), a random 16-byte nonce, the proof-of-work difficulty, the
// form id, and an HMAC-SHA256 tag over them truncated to 16 bytes. It is
// not secret, but is never stored: store Claims.IdempotencyKey instead.
type Token string

// Claims are what a verified token says.
type Claims struct {
	Form     string
	IssuedAt time.Time // to the second
	Nonce    [nonceSize]byte
	PoW      int // difficulty in leading zero bits; 0: none

	token string // the token verified, binding VerifyPoW to it
	idem  [32]byte
}

// IdempotencyKey identifies the submission: HMAC(key, "idem"||nonce||form)
// under the key that verified the token. Callers store it under a unique
// index so a double tap or a retry on a bad connection makes one
// submission.
func (c Claims) IdempotencyKey() [32]byte { return c.idem }

// Issue signs a token for form with the current key. pow is the
// proof-of-work difficulty in bits, 0 to MaxPoW (see VerifyPoW). A zero now
// is the Guard's clock.
func (g *Guard) Issue(form string, now time.Time, pow int) (Token, error) {
	if form == "" || len(form) > MaxForm {
		return "", fmt.Errorf("formguard: form id must be 1 to %d bytes", MaxForm)
	}
	if pow < 0 || pow > MaxPoW {
		return "", fmt.Errorf("formguard: difficulty %d is outside 0 to %d", pow, MaxPoW)
	}
	if now.IsZero() {
		now = g.now()
	}
	b := make([]byte, headerSize, headerSize+len(form)+tagSize)
	b[0] = tokenVersion
	binary.BigEndian.PutUint64(b[1:9], uint64(now.Unix())) // #nosec G115 -- a time after 1970
	if _, err := rand.Read(b[9 : 9+nonceSize]); err != nil {
		return "", fmt.Errorf("formguard: nonce: %w", err)
	}
	b[9+nonceSize] = byte(pow)
	b[10+nonceSize] = byte(len(form)) // #nosec G115 -- at most MaxForm
	b = append(b, form...)
	b = append(b, tag(g.keys[0], b)...)
	return Token(base64.RawURLEncoding.EncodeToString(b)), nil
}

// Verify checks token for form at now (zero: the Guard's clock): the
// version, the tag under each key in turn (in constant time), the form,
// then the age. Too old is ErrExpired, too young ErrTooFast, anything else
// ErrInvalid. The issue time has one-second resolution, so the minimum
// fill time may be short by up to a second.
func (g *Guard) Verify(token, form string, now time.Time) (Claims, error) {
	if len(token) > base64.RawURLEncoding.EncodedLen(maxToken) {
		return Claims{}, ErrInvalid
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) < headerSize+1+tagSize || b[0] != tokenVersion {
		return Claims{}, ErrInvalid
	}
	n := int(b[10+nonceSize])
	if n == 0 || n > MaxForm || len(b) != headerSize+n+tagSize {
		return Claims{}, ErrInvalid
	}
	body, sum := b[:headerSize+n], b[headerSize+n:]
	var key []byte
	for _, k := range g.keys {
		if hmac.Equal(sum, tag(k, body)) {
			key = k
			break
		}
	}
	if key == nil {
		return Claims{}, ErrInvalid
	}
	c := Claims{
		Form:     string(body[headerSize:]),
		IssuedAt: time.Unix(int64(binary.BigEndian.Uint64(body[1:9])), 0), // #nosec G115 -- signed by us
		PoW:      int(body[9+nonceSize]),
		token:    token,
	}
	copy(c.Nonce[:], body[9:9+nonceSize])
	if c.Form != form || c.PoW > MaxPoW {
		return Claims{}, ErrInvalid
	}
	if now.IsZero() {
		now = g.now()
	}
	switch age := now.Sub(c.IssuedAt); {
	case age > g.maxAge:
		return Claims{}, ErrExpired
	case age < g.minFill:
		return Claims{}, ErrTooFast
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte("idem"))
	m.Write(c.Nonce[:])
	m.Write([]byte(c.Form))
	copy(c.idem[:], m.Sum(nil))
	return c, nil
}

// tag is the token's truncated HMAC, under a context of its own so a key
// shared with IdempotencyKey cannot be made to sign one for the other.
func tag(key, body []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("tok"))
	m.Write(body)
	return m.Sum(nil)[:tagSize]
}

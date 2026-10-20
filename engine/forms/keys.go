// Package forms runs hosted forms (docs/spec/forms.md) on the api side:
// it takes submissions the form host left in the inbox, checks them
// again, keeps their files in the organisation's Xenibase storage, seals
// the personal answers, and starts the form's run or answers the waiting
// form step. It also sends form steps' links, reminders and verification
// codes, and holds the keys the form host and the api role share.
package forms

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// Keys are derived from the public host's secret (TASKIEM_PUBLIC_KEY),
// which the form host and the api role both hold: the form host seals
// what it receives, the api role opens it.
type Keys struct {
	// Inbox seals submissions, uploads, code requests and the public view
	// of a form step's request.
	Inbox []byte
	// Token derives form steps' link tokens, so the token is never stored.
	Token []byte
	// Guard signs the form host's one-time form tokens (formguard).
	Guard []byte
}

// ErrSealed is a sealed value that does not open.
var ErrSealed = errors.New("forms: sealed value does not open")

// NewKeys derives the keys from the 32-byte public key.
func NewKeys(master []byte) (Keys, error) {
	if len(master) != 32 {
		return Keys{}, fmt.Errorf("forms: the public key is %d bytes, want 32", len(master))
	}
	derive := func(info string) ([]byte, error) { return hkdf.Key(sha256.New, master, nil, info, 32) }
	var k Keys
	var err error
	if k.Inbox, err = derive("taskiem forms inbox"); err != nil {
		return k, err
	}
	if k.Token, err = derive("taskiem forms link token"); err != nil {
		return k, err
	}
	k.Guard, err = derive("taskiem forms guard")
	return k, err
}

// Seal encrypts v as JSON under the inbox key, bound to what (an
// AAD naming the record it belongs to).
func (k Keys) Seal(what string, v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return k.SealBytes(what, raw)
}

// SealBytes encrypts raw bytes (a file) under the inbox key.
func (k Keys) SealBytes(what string, raw []byte) ([]byte, error) {
	g, err := gcm(k.Inbox)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, raw, []byte(what)), nil
}

// Open decrypts what Seal sealed into v.
func (k Keys) Open(what string, ct []byte, v any) error {
	raw, err := k.OpenBytes(what, ct)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// OpenBytes decrypts what SealBytes sealed.
func (k Keys) OpenBytes(what string, ct []byte) ([]byte, error) {
	g, err := gcm(k.Inbox)
	if err != nil {
		return nil, err
	}
	if len(ct) < g.NonceSize() {
		return nil, ErrSealed
	}
	raw, err := g.Open(nil, ct[:g.NonceSize()], ct[g.NonceSize():], []byte(what))
	if err != nil {
		return nil, ErrSealed
	}
	return raw, nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// RequestToken is the token in a form step's link: derived from the
// request id, so a reminder carries the same link and nothing secret is
// stored (only TokenHash).
func (k Keys) RequestToken(request string) string {
	m := hmac.New(sha256.New, k.Token)
	m.Write([]byte("form request\x00" + request))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:24])
}

// TokenHash is how a link token is stored and looked up.
func TokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// NewLinkToken is a one-time link's token for a link form: 24 random
// bytes, shown once to whoever minted it.
func NewLinkToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// AddressHash keys an address (a normalised phone or a lowercased email)
// for verification codes and once_per, under the inbox key, so the
// database holds no address.
func (k Keys) AddressHash(scope, address string) []byte {
	m := hmac.New(sha256.New, k.Inbox)
	m.Write([]byte("form address\x00" + scope + "\x00" + address))
	return m.Sum(nil)
}

// CodeHash is how a verification code is stored.
func (k Keys) CodeHash(scope, code string) []byte {
	m := hmac.New(sha256.New, k.Inbox)
	m.Write([]byte("form code\x00" + scope + "\x00" + code))
	return m.Sum(nil)
}

// Sealing contexts: what a sealed value belongs to.
func SubmissionAAD(reference string) string { return "form submission " + reference }
func UploadAAD(reference, field string, n int) string {
	return fmt.Sprintf("form upload %s %s %d", reference, field, n)
}
func CodeAAD(scope string) string          { return "form code " + scope }
func RequestViewAAD(request string) string { return "form request view " + request }
func LinkViewAAD(link string) string       { return "form link view " + link }

// NewReference is a submission's reference: "F-" and 8 letters and
// digits (no 0, 1, l or o).
func NewReference() string {
	const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return "F-" + string(b)
}

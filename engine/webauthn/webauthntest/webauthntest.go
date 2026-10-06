// Package webauthntest is a software passkey for tests: it answers
// registration and sign-in challenges as a platform authenticator would.
package webauthntest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"sort"
)

// Authenticator holds one ES256 passkey.
type Authenticator struct {
	RPID, Origin string
	Key          *ecdsa.PrivateKey
	CredID       []byte
	UserHandle   []byte
	Count        uint32 // incremented before each signature; 0 stays 0 (a synced passkey)
	Synced       bool
	// Faults, for negative tests.
	SkipUV bool
}

// New returns an authenticator for rpID, used from origin.
func New(rpID, origin string) *Authenticator {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &Authenticator{RPID: rpID, Origin: origin, Key: k, CredID: id, Count: 1}
}

func (a *Authenticator) clientData(typ string, challenge []byte) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": base64.RawURLEncoding.EncodeToString(challenge), "origin": a.Origin, "crossOrigin": false})
	return b
}

func (a *Authenticator) authData(attested bool) []byte {
	h := sha256.Sum256([]byte(a.RPID))
	flags := byte(0x01 | 0x04)
	if a.SkipUV {
		flags = 0x01
	}
	if a.Synced {
		flags |= 0x08 | 0x10
	}
	if attested {
		flags |= 0x40
	}
	out := append(h[:], flags)
	out = binary.BigEndian.AppendUint32(out, a.Count)
	if attested {
		out = append(out, make([]byte, 16)...)                          // AAGUID
		out = binary.BigEndian.AppendUint16(out, uint16(len(a.CredID))) //nolint:gosec // test credential ids are short
		out = append(out, a.CredID...)
		out = append(out, a.coseKey()...)
	}
	return out
}

func (a *Authenticator) coseKey() []byte {
	pub, _ := a.Key.PublicKey.Bytes() // 0x04 || X || Y
	return Encode(map[int64]any{1: int64(2), 3: int64(-7), -1: int64(1), -2: pub[1:33], -3: pub[33:65]})
}

// Registration is what navigator.credentials.create() returns.
type Registration struct {
	ID                []byte
	ClientDataJSON    []byte
	AttestationObject []byte
}

// Register answers a registration challenge.
func (a *Authenticator) Register(challenge []byte) Registration {
	obj := Encode(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": a.authData(true)})
	return Registration{ID: a.CredID, ClientDataJSON: a.clientData("webauthn.create", challenge), AttestationObject: obj}
}

// Assertion is what navigator.credentials.get() returns.
type Assertion struct {
	ID                []byte
	ClientDataJSON    []byte
	AuthenticatorData []byte
	Signature         []byte
	UserHandle        []byte
}

// Assert answers a sign-in challenge.
func (a *Authenticator) Assert(challenge []byte) Assertion {
	if a.Count != 0 && !a.Synced {
		a.Count++
	}
	cd := a.clientData("webauthn.get", challenge)
	ad := a.authData(false)
	sum := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte(nil), ad...), sum[:]...))
	sig, _ := ecdsa.SignASN1(rand.Reader, a.Key, digest[:])
	return Assertion{ID: a.CredID, ClientDataJSON: cd, AuthenticatorData: ad, Signature: sig, UserHandle: a.UserHandle}
}

// JSON renders a response as the browser's PublicKeyCredential.toJSON().
func (r Registration) JSON() map[string]any {
	b64 := base64.RawURLEncoding.EncodeToString
	return map[string]any{"id": b64(r.ID), "rawId": b64(r.ID), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64(r.ClientDataJSON), "attestationObject": b64(r.AttestationObject)}}
}

// JSON renders an assertion as PublicKeyCredential.toJSON().
func (s Assertion) JSON() map[string]any {
	b64 := base64.RawURLEncoding.EncodeToString
	resp := map[string]any{"clientDataJSON": b64(s.ClientDataJSON), "authenticatorData": b64(s.AuthenticatorData), "signature": b64(s.Signature)}
	if s.UserHandle != nil {
		resp["userHandle"] = b64(s.UserHandle)
	}
	return map[string]any{"id": b64(s.ID), "rawId": b64(s.ID), "type": "public-key", "response": resp}
}

// Encode is a small CBOR encoder (canonical key order) for test fixtures.
func Encode(v any) []byte {
	var out []byte
	head := func(major byte, n uint64) {
		switch {
		case n < 24:
			out = append(out, major<<5|byte(n))
		case n < 1<<8:
			out = append(out, major<<5|24, byte(n))
		case n < 1<<16:
			out = append(out, major<<5|25)
			out = binary.BigEndian.AppendUint16(out, uint16(n))
		default:
			out = append(out, major<<5|26)
			out = binary.BigEndian.AppendUint32(out, uint32(n)) //nolint:gosec // test fixtures are small
		}
	}
	var enc func(v any)
	enc = func(v any) {
		switch x := v.(type) {
		case int64:
			if x >= 0 {
				head(0, uint64(x))
			} else {
				head(1, uint64(-1-x))
			}
		case []byte:
			head(2, uint64(len(x)))
			out = append(out, x...)
		case string:
			head(3, uint64(len(x)))
			out = append(out, x...)
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			head(5, uint64(len(x)))
			for _, k := range keys {
				enc(k)
				enc(x[k])
			}
		case map[int64]any:
			keys := make([]int64, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
			head(5, uint64(len(x)))
			for _, k := range keys {
				enc(k)
				enc(x[k])
			}
		default:
			panic("webauthntest: cannot encode")
		}
	}
	enc(v)
	return out
}

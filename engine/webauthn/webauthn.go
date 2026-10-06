// Package webauthn verifies passkey registrations and sign-ins (W3C Web
// Authentication Level 2), implemented from the specification. It accepts
// what the platform asks for and nothing more:
//
//   - attestation "none": the key is trusted because the signed-in user
//     registered it, not because of who made the authenticator;
//   - user verification required (biometric or device PIN), so a passkey
//     is a second factor by itself;
//   - ES256, EdDSA and RS256 (2048 bits or more) keys;
//   - origins and the relying-party id from configuration only.
package webauthn

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// Config is the relying party.
type Config struct {
	RPID    string   // the registrable domain, e.g. taskiem.example
	RPName  string   // shown by the authenticator
	Origins []string // exact origins pages are served from, e.g. https://app.taskiem.example
}

// Credential is a registered passkey.
type Credential struct {
	ID        []byte
	PublicKey []byte // COSE_Key, as the authenticator gave it
	Alg       int
	SignCount uint32
	AAGUID    []byte
	// BackupEligible marks a synced passkey (iCloud Keychain, Google
	// Password Manager), which exists on more than one device.
	BackupEligible bool
}

// Algorithms offered at registration, in order of preference.
var Algorithms = []int{AlgES256, AlgEdDSA, AlgRS256}

const (
	AlgES256 = -7
	AlgEdDSA = -8
	AlgRS256 = -257
)

// Errors.
var (
	ErrInvalid = errors.New("webauthn: invalid response")
	// ErrCloned means the signature counter went backwards: two copies of
	// one authenticator exist.
	ErrCloned = errors.New("webauthn: signature counter did not advance (possible cloned authenticator)")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, args...)...)
}

type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

func (c Config) checkClientData(raw []byte, typ string, challenge []byte) error {
	var cd clientData
	if err := json.Unmarshal(raw, &cd); err != nil {
		return invalid("client data is not JSON")
	}
	if cd.Type != typ {
		return invalid("client data type %q, want %q", cd.Type, typ)
	}
	got, err := base64.RawURLEncoding.DecodeString(cd.Challenge)
	if err != nil || len(challenge) == 0 || subtle.ConstantTimeCompare(got, challenge) != 1 {
		return invalid("challenge does not match")
	}
	if !slices.Contains(c.Origins, cd.Origin) {
		return invalid("origin %q is not this site", cd.Origin)
	}
	if cd.CrossOrigin {
		return invalid("cross-origin request")
	}
	return nil
}

// Flags in authenticator data.
const (
	flagUP = 0x01 // user present
	flagUV = 0x04 // user verified
	flagBE = 0x08 // backup eligible
	flagAT = 0x40 // attested credential data included
)

type authData struct {
	rpIDHash  []byte
	flags     byte
	signCount uint32
	credID    []byte
	aaguid    []byte
	coseKey   []byte
}

func parseAuthData(b []byte, attested bool) (authData, error) {
	if len(b) < 37 {
		return authData{}, invalid("authenticator data too short")
	}
	ad := authData{rpIDHash: b[:32], flags: b[32], signCount: binary.BigEndian.Uint32(b[33:37])}
	if !attested {
		return ad, nil
	}
	if ad.flags&flagAT == 0 {
		return ad, invalid("no attested credential data")
	}
	rest := b[37:]
	if len(rest) < 18 {
		return ad, invalid("attested credential data too short")
	}
	ad.aaguid = rest[:16]
	n := int(binary.BigEndian.Uint16(rest[16:18]))
	rest = rest[18:]
	if n == 0 || n > 1023 || len(rest) < n {
		return ad, invalid("bad credential id length")
	}
	ad.credID, rest = rest[:n], rest[n:]
	_, after, err := decodeCBOR(rest)
	if err != nil {
		return ad, invalid("credential public key: %v", err)
	}
	ad.coseKey = rest[:len(rest)-len(after)]
	return ad, nil
}

func (c Config) checkAuthData(ad authData) error {
	want := sha256.Sum256([]byte(c.RPID))
	if !bytes.Equal(ad.rpIDHash, want[:]) {
		return invalid("authenticator data is for another site")
	}
	if ad.flags&flagUP == 0 || ad.flags&flagUV == 0 {
		return invalid("the authenticator did not verify the user")
	}
	return nil
}

// VerifyRegistration checks a navigator.credentials.create() response for
// challenge and returns the new credential.
func (c Config) VerifyRegistration(challenge, clientDataJSON, attestationObject []byte) (*Credential, error) {
	if err := c.checkClientData(clientDataJSON, "webauthn.create", challenge); err != nil {
		return nil, err
	}
	obj, rest, err := decodeCBOR(attestationObject)
	if err != nil || len(rest) != 0 {
		return nil, invalid("attestation object is not CBOR")
	}
	m, ok := obj.(map[any]any)
	if !ok {
		return nil, invalid("attestation object is not a map")
	}
	// The attestation statement is not checked: "none" was asked for, and
	// trust comes from the signed-in user registering the key.
	raw, ok := m["authData"].([]byte)
	if !ok {
		return nil, invalid("attestation object has no authData")
	}
	ad, err := parseAuthData(raw, true)
	if err != nil {
		return nil, err
	}
	if err := c.checkAuthData(ad); err != nil {
		return nil, err
	}
	key, err := parseCOSE(ad.coseKey)
	if err != nil {
		return nil, err
	}
	return &Credential{ID: bytes.Clone(ad.credID), PublicKey: bytes.Clone(ad.coseKey), Alg: key.alg, SignCount: ad.signCount,
		AAGUID: bytes.Clone(ad.aaguid), BackupEligible: ad.flags&flagBE != 0}, nil
}

// VerifyAssertion checks a navigator.credentials.get() response for
// challenge, signed by cred, and returns the new signature counter.
func (c Config) VerifyAssertion(challenge []byte, cred Credential, clientDataJSON, authenticatorData, signature []byte) (uint32, error) {
	if err := c.checkClientData(clientDataJSON, "webauthn.get", challenge); err != nil {
		return 0, err
	}
	ad, err := parseAuthData(authenticatorData, false)
	if err != nil {
		return 0, err
	}
	if err := c.checkAuthData(ad); err != nil {
		return 0, err
	}
	key, err := parseCOSE(cred.PublicKey)
	if err != nil {
		return 0, err
	}
	sum := sha256.Sum256(clientDataJSON)
	signed := append(bytes.Clone(authenticatorData), sum[:]...)
	if !key.verify(signed, signature) {
		return 0, invalid("signature does not verify")
	}
	// Synced passkeys report 0 throughout; a counter that is used must
	// increase.
	if (ad.signCount != 0 || cred.SignCount != 0) && ad.signCount <= cred.SignCount {
		return 0, ErrCloned
	}
	return ad.signCount, nil
}

// NewChallenge returns 32 random bytes.
func NewChallenge() []byte { return randomBytes(32) }

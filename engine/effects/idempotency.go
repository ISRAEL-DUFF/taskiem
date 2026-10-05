package effects

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const keyDomain = "taskiem/idem/v1"

// KeyInput holds the derivation inputs for one logical effect.
type KeyInput struct {
	TenantID     string // canonical lowercase UUID
	Seed         string // effect.idempotency_seed value, or the run id
	StepID       string // WD step id; see ForeachStepID
	AttemptGroup int    // 0, incremented only on reconcile not_found or manual re-issue
}

// ForeachStepID is the step id used for a foreach body step without its own seed.
func ForeachStepID(stepID string, index int) string {
	return stepID + "[" + strconv.Itoa(index) + "]"
}

// Digest returns SHA-256 over the domain-separated key material.
func (k KeyInput) Digest() ([32]byte, error) {
	if k.TenantID == "" || k.Seed == "" || k.StepID == "" {
		return [32]byte{}, errors.New("effects: tenant, seed and step id are required")
	}
	if k.AttemptGroup < 0 {
		return [32]byte{}, errors.New("effects: negative attempt group")
	}
	for _, f := range []string{k.TenantID, k.Seed, k.StepID} {
		if strings.IndexByte(f, 0) >= 0 {
			return [32]byte{}, errors.New("effects: key fields may not contain NUL")
		}
	}
	material := strings.Join([]string{keyDomain, k.TenantID, k.Seed, k.StepID, strconv.Itoa(k.AttemptGroup)}, "\x00")
	return sha256.Sum256([]byte(material)), nil
}

// Encoding is how a digest is rendered for a provider field.
type Encoding string

const (
	Base32Lower Encoding = "base32_lower"
	HexLower    Encoding = "hex_lower"
	Base64URL   Encoding = "base64url"
)

func (e Encoding) bitsPerChar() int {
	switch e {
	case Base32Lower:
		return 5
	case HexLower:
		return 4
	case Base64URL:
		return 6
	}
	return 0
}

func (e Encoding) alphabet() string {
	switch e {
	case Base32Lower:
		return "abcdefghijklmnopqrstuvwxyz234567"
	case HexLower:
		return "0123456789abcdef"
	case Base64URL:
		return "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	}
	return ""
}

func (e Encoding) encode(b []byte) string {
	switch e {
	case Base32Lower:
		return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
	case HexLower:
		return hex.EncodeToString(b)
	case Base64URL:
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return ""
}

// Limits are the provider's constraints on the field carrying the key.
type Limits struct {
	MinLength int    `json:"min_length,omitempty"`
	MaxLength int    `json:"max_length,omitempty"`
	Charset   string `json:"charset,omitempty"` // regex character-class body, e.g. "a-z0-9_-"
}

// Spec is a manifest's idempotency block.
type Spec struct {
	Field    string   `json:"field"`
	Encoding Encoding `json:"encoding"`
	Length   int      `json:"length"`
	Prefix   string   `json:"prefix,omitempty"`
	Limits   *Limits  `json:"limits,omitempty"`
}

// MinKeyBits is the least entropy an encoded key may carry.
const MinKeyBits = 128

// Validate applies the registration rules in docs/contracts/connector-v1.md.
func (s Spec) Validate() error {
	bits := s.Encoding.bitsPerChar()
	if bits == 0 {
		return fmt.Errorf("effects: unknown encoding %q", s.Encoding)
	}
	if s.Length*bits < MinKeyBits {
		return fmt.Errorf("effects: %d %s chars carry %d bits; need at least %d", s.Length, s.Encoding, s.Length*bits, MinKeyBits)
	}
	if max := len(s.Encoding.encode(make([]byte, sha256.Size))); s.Length > max {
		return fmt.Errorf("effects: length %d exceeds the %d chars a SHA-256 digest encodes to", s.Length, max)
	}
	if s.Limits == nil {
		return nil
	}
	total := len(s.Prefix) + s.Length
	if s.Limits.MinLength > 0 && total < s.Limits.MinLength {
		return fmt.Errorf("effects: key is %d chars; provider minimum is %d", total, s.Limits.MinLength)
	}
	if s.Limits.MaxLength > 0 && total > s.Limits.MaxLength {
		return fmt.Errorf("effects: key is %d chars; provider maximum is %d", total, s.Limits.MaxLength)
	}
	if s.Limits.Charset != "" {
		re, err := regexp.Compile("^[" + s.Limits.Charset + "]*$")
		if err != nil {
			return fmt.Errorf("effects: bad charset %q: %w", s.Limits.Charset, err)
		}
		if !re.MatchString(s.Prefix) {
			return fmt.Errorf("effects: prefix %q has characters outside [%s]", s.Prefix, s.Limits.Charset)
		}
		if !re.MatchString(s.Encoding.alphabet()) {
			return fmt.Errorf("effects: %s alphabet has characters outside [%s]", s.Encoding, s.Limits.Charset)
		}
	}
	return nil
}

// Key derives and encodes the provider-facing idempotency key.
func (s Spec) Key(in KeyInput) (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	d, err := in.Digest()
	if err != nil {
		return "", err
	}
	return s.Prefix + s.Encoding.encode(d[:])[:s.Length], nil
}

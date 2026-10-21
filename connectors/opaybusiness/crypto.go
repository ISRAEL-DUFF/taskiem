package opaybusiness

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// OPay's offline APIs authenticate with RSA (documentation.opayweb.com,
// Authentication): a request's plaintext JSON is encrypted with OPay's
// public key in PKCS #1 v1.5 blocks, Base64-encoded as paramContent, and
// signed (SHA256withRSA, the merchant's private key) over paramContent
// followed by the millisecond timestamp. OPay's answers carry data
// encrypted the same way with the merchant's public key and a signature
// by OPay over "code=…&data=…&message=…&timestamp=…" (the fields other
// than sign, sorted, nulls left out). A webhook delivery is the request
// envelope in reverse: signed by OPay over paramContent + timestamp,
// encrypted with the merchant's public key.

const (
	version    = "V1.0.1"
	bodyFormat = "JSON"
)

// ParsePublicKey reads an RSA public key as PEM (PUBLIC KEY or RSA PUBLIC
// KEY) or as the Base64 DER OPay's dashboard also shows.
func ParsePublicKey(s string) (*rsa.PublicKey, error) {
	der, err := keyBytes(s)
	if err != nil {
		return nil, err
	}
	if k, err := x509.ParsePKIXPublicKey(der); err == nil {
		if pub, ok := k.(*rsa.PublicKey); ok {
			return pub, nil
		}
		return nil, errors.New("the public key is not an RSA key")
	}
	if pub, err := x509.ParsePKCS1PublicKey(der); err == nil {
		return pub, nil
	}
	return nil, errors.New("the public key cannot be read: paste the whole block from BEGIN to END")
}

// ParsePrivateKey reads an unencrypted RSA private key as PEM (PRIVATE KEY
// or RSA PRIVATE KEY) or Base64 DER.
func ParsePrivateKey(s string) (*rsa.PrivateKey, error) {
	der, err := keyBytes(s)
	if err != nil {
		return nil, err
	}
	if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		if priv, ok := k.(*rsa.PrivateKey); ok {
			return priv, nil
		}
		return nil, errors.New("the private key is not an RSA key")
	}
	if priv, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return priv, nil
	}
	return nil, errors.New("the private key cannot be read: it must be an unencrypted RSA key, the whole block from BEGIN to END")
}

func keyBytes(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("the key is empty")
	}
	if strings.HasPrefix(s, "-----BEGIN") {
		block, _ := pem.Decode([]byte(s))
		if block == nil {
			return nil, errors.New("the key's PEM block cannot be read")
		}
		return block.Bytes, nil
	}
	der, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		return nil, errors.New("the key is neither a PEM block nor Base64")
	}
	return der, nil
}

// PublicKeyPEM renders a public key as the PEM block OPay's dashboard
// takes.
func PublicKeyPEM(pub *rsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
}

// canonicalJSON is the plaintext OPay signs and encrypts: compact JSON
// with object keys sorted (encoding/json sorts map keys), nothing escaped
// for HTML.
func canonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Encrypt returns plaintext encrypted with pub in PKCS #1 v1.5 blocks of
// the key's size, Base64-encoded, as OPay's paramContent and data are.
func Encrypt(pub *rsa.PublicKey, plaintext []byte) (string, error) {
	chunk := pub.Size() - 11
	var out []byte
	for len(plaintext) > 0 {
		n := min(chunk, len(plaintext))
		block, err := rsa.EncryptPKCS1v15(rand.Reader, pub, plaintext[:n]) //nolint:staticcheck // OPay's protocol is PKCS #1 v1.5 (Authentication, documentation.opayweb.com)
		if err != nil {
			return "", err
		}
		out = append(out, block...)
		plaintext = plaintext[n:]
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt reverses Encrypt with the matching private key.
func Decrypt(priv *rsa.PrivateKey, b64 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(b64), ""))
	if err != nil {
		return nil, errors.New("the encrypted content is not Base64")
	}
	size := priv.Size()
	if len(raw) == 0 || len(raw)%size != 0 {
		return nil, fmt.Errorf("the encrypted content is %d bytes, not whole blocks of %d: the wrong key, or not for this business", len(raw), size)
	}
	var out []byte
	for i := 0; i < len(raw); i += size {
		block, err := rsa.DecryptPKCS1v15(rand.Reader, priv, raw[i:i+size]) //nolint:staticcheck // as above; the key is the business's own and the content OPay's
		if err != nil {
			return nil, errors.New("the encrypted content could not be decrypted with the business's private key")
		}
		out = append(out, block...)
	}
	return out, nil
}

// Sign is SHA256withRSA (PKCS #1 v1.5) over s, Base64.
func Sign(priv *rsa.PrivateKey, s string) (string, error) {
	sum := sha256.Sum256([]byte(s))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// Verify checks a Base64 SHA256withRSA signature over s.
func Verify(pub *rsa.PublicKey, s, b64sig string) error {
	sig, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(b64sig), ""))
	if err != nil {
		return errors.New("the signature is not Base64")
	}
	sum := sha256.Sum256([]byte(s))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return errors.New("the signature does not match")
	}
	return nil
}

// ResponseSigned is the string OPay signs on an answer: every field but
// sign, sorted by name, as name=value joined with &, null values left out.
func ResponseSigned(fields map[string]any) string {
	keys := make([]string, 0, len(fields))
	for k, v := range fields {
		if k == "sign" || v == nil {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+fmt.Sprint(fields[k]))
	}
	return strings.Join(parts, "&")
}

package whatsapp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// The WhatsApp Flows data endpoint's encryption (Meta's "Implementing
// Endpoints for Flows"): the WhatsApp client picks a fresh AES key and IV
// per request, encrypts the request JSON with AES-GCM (the 16-byte tag
// appended), and encrypts the AES key with the business's RSA public key
// (OAEP, SHA-256 for the hash and MGF1). The response is encrypted with
// the same AES key and the IV with every bit flipped, and returned as
// base64 text. The private key is the operator's (never logged, never in
// the database); its public half is uploaded to each phone number that
// sends Flows.

// ErrFlowDecrypt: a request could not be decrypted (answer 421: the client
// fetches the public key again and retries).
var ErrFlowDecrypt = errors.New("whatsapp: the Flows request cannot be decrypted")

// FlowKey is the business's private key for the Flows endpoint.
type FlowKey struct{ priv *rsa.PrivateKey }

// ParseFlowKey reads an unencrypted PEM private key (PKCS#1 "RSA PRIVATE
// KEY" or PKCS#8 "PRIVATE KEY"), RSA of at least 2048 bits.
func ParseFlowKey(pemText string) (*FlowKey, error) {
	// A key passed through an environment variable may have its new lines
	// written as \n.
	pemText = strings.ReplaceAll(strings.TrimSpace(pemText), `\n`, "\n")
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("whatsapp: the Flows private key is not PEM")
	}
	if block.Headers["Proc-Type"] != "" {
		return nil, errors.New("whatsapp: the Flows private key is passphrase-protected; store it unencrypted in the Secret (openssl pkey -in key.pem -out plain.pem)")
	}
	var priv *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("whatsapp: the Flows private key cannot be read")
		}
		priv = k
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("whatsapp: the Flows private key cannot be read")
		}
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("whatsapp: the Flows private key must be RSA")
		}
		priv = rk
	default:
		return nil, fmt.Errorf("whatsapp: the Flows private key is a %q block, not a private key", block.Type)
	}
	if priv.N.BitLen() < 2048 {
		return nil, errors.New("whatsapp: the Flows private key must be RSA of at least 2048 bits")
	}
	return &FlowKey{priv: priv}, nil
}

// NewFlowKey wraps a key (tests).
func NewFlowKey(priv *rsa.PrivateKey) *FlowKey { return &FlowKey{priv: priv} }

// PublicPEM is the public half, as uploaded to a phone number
// (POST /{phone-number-id}/whatsapp_business_encryption).
func (k *FlowKey) PublicPEM() string {
	der, _ := x509.MarshalPKIXPublicKey(&k.priv.PublicKey)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// String never shows the key.
func (k *FlowKey) String() string { return "FlowKey(redacted)" }

// GoString never shows the key.
func (k *FlowKey) GoString() string { return k.String() }

// FlowEnvelope is the request body Meta posts to the endpoint.
type FlowEnvelope struct {
	EncryptedFlowData string `json:"encrypted_flow_data"`
	EncryptedAESKey   string `json:"encrypted_aes_key"`
	InitialVector     string `json:"initial_vector"`
}

// FlowRequest is a decrypted request.
type FlowRequest struct {
	Version   string         `json:"version"`
	Action    string         `json:"action"` // INIT, BACK, data_exchange or ping
	Screen    string         `json:"screen"`
	Data      map[string]any `json:"data"`
	FlowToken string         `json:"flow_token"`
}

// FlowSession answers one request: the request's AES key and IV.
type FlowSession struct {
	key, iv []byte
}

// Decrypt opens a request.
func (k *FlowKey) Decrypt(body []byte) (FlowRequest, *FlowSession, error) {
	var req FlowRequest
	var env FlowEnvelope
	if err := json.Unmarshal(body, &env); err != nil || env.EncryptedFlowData == "" || env.EncryptedAESKey == "" || env.InitialVector == "" {
		return req, nil, ErrFlowDecrypt
	}
	wrapped, err1 := base64.StdEncoding.DecodeString(env.EncryptedAESKey)
	data, err2 := base64.StdEncoding.DecodeString(env.EncryptedFlowData)
	iv, err3 := base64.StdEncoding.DecodeString(env.InitialVector)
	if err1 != nil || err2 != nil || err3 != nil || len(iv) == 0 || len(iv) > 64 {
		return req, nil, ErrFlowDecrypt
	}
	aesKey, err := rsa.DecryptOAEP(sha256.New(), nil, k.priv, wrapped, nil)
	if err != nil {
		return req, nil, ErrFlowDecrypt
	}
	gcm, err := newGCM(aesKey, len(iv))
	if err != nil {
		return req, nil, ErrFlowDecrypt
	}
	plain, err := gcm.Open(nil, iv, data, nil)
	if err != nil {
		return req, nil, ErrFlowDecrypt
	}
	if err := json.Unmarshal(plain, &req); err != nil {
		return req, nil, ErrFlowDecrypt
	}
	return req, &FlowSession{key: aesKey, iv: iv}, nil
}

func newGCM(key []byte, ivLen int) (cipher.AEAD, error) {
	switch len(key) {
	case 16, 24, 32:
	default:
		return nil, ErrFlowDecrypt
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithNonceSize(b, ivLen)
}

func flipped(iv []byte) []byte {
	out := make([]byte, len(iv))
	for i, b := range iv {
		out[i] = ^b
	}
	return out
}

// Encrypt seals a response: AES-GCM under the request's key with the IV's
// bits flipped, base64.
func (s *FlowSession) Encrypt(v any) (string, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(s.key, len(s.iv))
	if err != nil {
		return "", err
	}
	// The flipped IV is what the protocol prescribes; the key is fresh per
	// request, chosen by the client.
	return base64.StdEncoding.EncodeToString(gcm.Seal(nil, flipped(s.iv), plain, nil)), nil //nolint:gosec // see above
}

// EncryptFlowRequest encrypts a request as the WhatsApp client does, with a
// fresh 128-bit key and IV; open decrypts the endpoint's answer (tests, the
// fake).
func EncryptFlowRequest(pub *rsa.PublicKey, req any) (body []byte, open func(resp []byte) ([]byte, error), err error) {
	plain, err := json.Marshal(req)
	if err != nil {
		return nil, nil, err
	}
	key, iv := make([]byte, 16), make([]byte, 16)
	_, _ = rand.Read(key)
	_, _ = rand.Read(iv)
	wrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, key, nil)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := newGCM(key, len(iv))
	if err != nil {
		return nil, nil, err
	}
	body, _ = json.Marshal(FlowEnvelope{
		EncryptedFlowData: base64.StdEncoding.EncodeToString(gcm.Seal(nil, iv, plain, nil)),
		EncryptedAESKey:   base64.StdEncoding.EncodeToString(wrapped),
		InitialVector:     base64.StdEncoding.EncodeToString(iv),
	})
	open = func(resp []byte) ([]byte, error) {
		ct, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(resp)))
		if err != nil {
			return nil, err
		}
		return gcm.Open(nil, flipped(iv), ct, nil)
	}
	return body, open, nil
}

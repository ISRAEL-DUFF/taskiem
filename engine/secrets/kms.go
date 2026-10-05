// Package secrets stores tenant secrets and connection credentials with
// envelope encryption (spec 14.1): each value is sealed with its own data
// key (AES-256-GCM), the data key is sealed with the tenant's key-encryption
// key, and the KEK is stored only wrapped by a root key held in a KMS.
package secrets

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// KMS wraps and unwraps key material under a named root key.
type KMS interface {
	Encrypt(ctx context.Context, key string, plaintext []byte) (string, error)
	Decrypt(ctx context.Context, key string, ciphertext string) ([]byte, error)
}

// LocalKMS keeps root keys in process memory. For development, tests, and
// single-node installs without OpenBao; production uses OpenBaoTransit.
type LocalKMS struct {
	keys map[string][]byte
}

// NewLocalKMS returns a KMS with one 32-byte root key per name.
func NewLocalKMS(keys map[string][]byte) (*LocalKMS, error) {
	for name, k := range keys {
		if len(k) != 32 {
			return nil, fmt.Errorf("local kms: root key %q must be 32 bytes", name)
		}
	}
	return &LocalKMS{keys: keys}, nil
}

const localPrefix = "local:v1:"

func (l *LocalKMS) Encrypt(_ context.Context, key string, plaintext []byte) (string, error) {
	k, ok := l.keys[key]
	if !ok {
		return "", fmt.Errorf("local kms: unknown key %q", key)
	}
	sealed, err := seal(k, plaintext, []byte(key))
	if err != nil {
		return "", err
	}
	return localPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func (l *LocalKMS) Decrypt(_ context.Context, key string, ciphertext string) ([]byte, error) {
	k, ok := l.keys[key]
	if !ok {
		return nil, fmt.Errorf("local kms: unknown key %q", key)
	}
	raw, ok := strings.CutPrefix(ciphertext, localPrefix)
	if !ok {
		return nil, errors.New("local kms: not a local ciphertext")
	}
	sealed, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	return open(k, sealed, []byte(key))
}

// OpenBaoTransit uses the OpenBao (or Vault-compatible) transit engine, so
// root keys never leave it.
type OpenBaoTransit struct {
	Addr  string // e.g. http://127.0.0.1:8200
	Token string
	Mount string // default "transit"
	HTTP  *http.Client
}

func (o *OpenBaoTransit) call(ctx context.Context, op, key string, body map[string]string) (map[string]any, error) {
	mount := o.Mount
	if mount == "" {
		mount = "transit"
	}
	client := o.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.Addr, "/")+"/v1/"+mount+"/"+op+"/"+key, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Vault-Token", o.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openbao %s: %w", op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openbao %s: http %d: %s", op, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (o *OpenBaoTransit) Encrypt(ctx context.Context, key string, plaintext []byte) (string, error) {
	d, err := o.call(ctx, "encrypt", key, map[string]string{"plaintext": base64.StdEncoding.EncodeToString(plaintext)})
	if err != nil {
		return "", err
	}
	ct, _ := d["ciphertext"].(string)
	if ct == "" {
		return "", errors.New("openbao encrypt: no ciphertext")
	}
	return ct, nil
}

func (o *OpenBaoTransit) Decrypt(ctx context.Context, key string, ciphertext string) ([]byte, error) {
	d, err := o.call(ctx, "decrypt", key, map[string]string{"ciphertext": ciphertext})
	if err != nil {
		return nil, err
	}
	pt, _ := d["plaintext"].(string)
	return base64.StdEncoding.DecodeString(pt)
}

// seal encrypts with AES-256-GCM and returns nonce || ciphertext.
func seal(key, plaintext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, aad), nil
}

// open reverses seal; it fails if the ciphertext or its context was altered.
func open(key, sealed, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	return gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], aad)
}

func newKey() ([]byte, error) {
	k := make([]byte, 32)
	_, err := rand.Read(k)
	return k, err
}

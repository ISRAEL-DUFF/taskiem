// Package byoktest provides in-process fakes of the customer key services
// engine/byok talks to, built from the same public API descriptions. They
// serve TLS (httptest), so tests exercise the trusted-CA path rather than
// turning verification off.
package byoktest

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/engine/byok"
	"github.com/israel-duff/taskiem/engine/egress"
)

// Factory returns a provider factory that trusts the fakes' certificates
// and may reach loopback (where httptest listens).
func Factory(servers ...*httptest.Server) *byok.Factory {
	var b strings.Builder
	for _, s := range servers {
		b.WriteString(CACert(s))
	}
	return &byok.Factory{CACert: b.String(), Guard: &egress.Guard{Blocked: func(a netip.Addr) bool { return !a.IsLoopback() && egress.BlockedAddr(a) }}}
}

// CACert is a TLS test server's certificate, PEM.
func CACert(s *httptest.Server) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}))
}

// Transit fakes an OpenBao/Vault transit engine at mount "transit".
type Transit struct {
	Server *httptest.Server
	// Token is the static token accepted; RoleID and SecretID log in.
	Token, RoleID, SecretID string

	mu       sync.Mutex
	keys     map[string][]byte
	disabled map[string]bool
	revoked  bool
	down     bool
	issued   map[string]bool
	calls    int
}

// NewTransit starts a fake transit engine with one key, "customer".
func NewTransit(t testing.TB) *Transit {
	f := &Transit{Token: "s.customer-token", RoleID: "role-1", SecretID: "secret-1",
		keys: map[string][]byte{}, disabled: map[string]bool{}, issued: map[string]bool{}}
	f.AddKey("customer")
	f.Server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Server.Close)
	return f
}

// AddKey creates a transit key.
func (f *Transit) AddKey(name string) {
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	f.mu.Lock()
	f.keys[name] = k
	f.mu.Unlock()
}

// Config is the configuration for key on this server.
func (f *Transit) Config(key string) byok.Config {
	return byok.Config{Provider: byok.VaultTransit, Address: f.Server.URL, Key: key, CACert: CACert(f.Server)}
}

// Disable refuses (or, false, allows again) every operation with key, as
// a customer disabling decryption on it would.
func (f *Transit) Disable(key string, on bool) {
	f.mu.Lock()
	f.disabled[key] = on
	f.mu.Unlock()
}

// Revoke refuses every token (the customer revoked Taskiem's access).
func (f *Transit) Revoke(on bool) {
	f.mu.Lock()
	f.revoked = on
	f.mu.Unlock()
}

// Down makes the server answer 503 (sealed or unreachable).
func (f *Transit) Down(on bool) {
	f.mu.Lock()
	f.down = on
	f.mu.Unlock()
}

// Calls counts encrypt and decrypt calls.
func (f *Transit) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func vaultErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": []string{msg}})
}

func (f *Transit) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		vaultErr(w, http.StatusServiceUnavailable, "Vault is sealed")
		return
	}
	if r.Method != http.MethodPost {
		vaultErr(w, http.StatusMethodNotAllowed, "method")
		return
	}
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		vaultErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if r.URL.Path == "/v1/auth/approle/login" {
		if body["role_id"] != f.RoleID || body["secret_id"] != f.SecretID || f.revoked {
			vaultErr(w, http.StatusBadRequest, "invalid role or secret ID")
			return
		}
		tok := "s.approle-" + base64.RawURLEncoding.EncodeToString(randBytes(8))
		f.issued[tok] = true
		_ = json.NewEncoder(w).Encode(map[string]any{"auth": map[string]any{"client_token": tok, "lease_duration": 3600}})
		return
	}
	tok := r.Header.Get("X-Vault-Token")
	if f.revoked || (tok != f.Token && !f.issued[tok]) {
		vaultErr(w, http.StatusForbidden, "permission denied")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/"), "/")
	if len(parts) != 3 || parts[0] != "transit" {
		vaultErr(w, http.StatusNotFound, "no handler for route")
		return
	}
	key, ok := f.keys[parts[2]]
	if !ok {
		vaultErr(w, http.StatusBadRequest, "encryption key not found")
		return
	}
	if f.disabled[parts[2]] {
		vaultErr(w, http.StatusForbidden, "permission denied")
		return
	}
	f.calls++
	gcm := newGCM(key)
	switch parts[1] {
	case "encrypt":
		pt, err := base64.StdEncoding.DecodeString(body["plaintext"])
		if err != nil {
			vaultErr(w, http.StatusBadRequest, "plaintext must be base64")
			return
		}
		nonce := randBytes(gcm.NonceSize())
		ct := gcm.Seal(nonce, nonce, pt, []byte(parts[2]))
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"ciphertext": "vault:v1:" + base64.StdEncoding.EncodeToString(ct), "key_version": 1}})
	case "decrypt":
		raw, ok := strings.CutPrefix(body["ciphertext"], "vault:v1:")
		ct, err := base64.StdEncoding.DecodeString(raw)
		if !ok || err != nil || len(ct) < gcm.NonceSize() {
			vaultErr(w, http.StatusBadRequest, "invalid ciphertext")
			return
		}
		pt, err := gcm.Open(nil, ct[:gcm.NonceSize()], ct[gcm.NonceSize():], []byte(parts[2]))
		if err != nil {
			vaultErr(w, http.StatusBadRequest, "cipher: message authentication failed")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"plaintext": base64.StdEncoding.EncodeToString(pt)}})
	default:
		vaultErr(w, http.StatusNotFound, "no handler for route")
	}
}

func newGCM(key []byte) cipher.AEAD {
	b, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		panic(err)
	}
	return g
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

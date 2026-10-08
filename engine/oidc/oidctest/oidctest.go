// Package oidctest is a fake OpenID Connect provider for tests. It serves
// discovery and keys, accepts any sign-in at /authorize by issuing a code
// for the claims set on it, checks PKCE and client credentials at /token,
// and signs ID tokens with RS256.
package oidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"time"
)

// Provider is a running fake.
type Provider struct {
	*httptest.Server
	ClientID, ClientSecret string
	Key                    *rsa.PrivateKey
	KeyID                  string
	// Claims are what the next sign-in asserts (iss, aud, exp, iat and
	// nonce are filled in unless set).
	Claims map[string]any

	mu    sync.Mutex
	codes map[string]grant
}

type grant struct {
	challenge, nonce, redirect string
	claims                     map[string]any
}

// New starts a provider; close it with Close.
func New() *Provider {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	p := &Provider{ClientID: "taskiem", ClientSecret: "s3cret", Key: k, KeyID: "k1", codes: map[string]grant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": p.URL, "authorization_endpoint": p.URL + "/authorize",
			"token_endpoint": p.URL + "/token", "jwks_uri": p.URL + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		b64 := base64.RawURLEncoding.EncodeToString
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": p.KeyID, "use": "sig", "alg": "RS256",
			"n": b64(p.Key.N.Bytes()), "e": b64(big.NewInt(int64(p.Key.E)).Bytes())}}})
	})
	mux.HandleFunc("/authorize", p.authorize)
	mux.HandleFunc("/token", p.token)
	p.Server = httptest.NewServer(mux)
	return p
}

// authorize "signs the person in" and redirects back with a code.
func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("client_id") != p.ClientID || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	code := base64.RawURLEncoding.EncodeToString(randomBytes(16))
	p.mu.Lock()
	claims := map[string]any{}
	for k, v := range p.Claims {
		claims[k] = v
	}
	p.codes[code] = grant{challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), redirect: q.Get("redirect_uri"), claims: claims}
	p.mu.Unlock()
	u, _ := url.Parse(q.Get("redirect_uri"))
	back := u.Query()
	back.Set("code", code)
	back.Set("state", q.Get("state"))
	u.RawQuery = back.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound) //nolint:gosec // a test provider redirecting where the test asked
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, secret, _ := r.BasicAuth()
	id, _ = url.QueryUnescape(id)
	secret, _ = url.QueryUnescape(secret)
	if id != p.ClientID || secret != p.ClientSecret {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_client"})
		return
	}
	p.mu.Lock()
	g, ok := p.codes[r.Form.Get("code")]
	delete(p.codes, r.Form.Get("code"))
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge || r.Form.Get("redirect_uri") != g.redirect {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
		return
	}
	c := map[string]any{"iss": p.URL, "aud": p.ClientID, "exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Unix(), "nonce": g.nonce}
	for k, v := range g.claims {
		c[k] = v
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"id_token": p.Sign(c), "token_type": "Bearer"})
}

// Sign makes an RS256 JWT with the provider's key.
func (p *Provider) Sign(claims map[string]any) string {
	b64 := base64.RawURLEncoding.EncodeToString
	head, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": p.KeyID, "typ": "JWT"})
	body, _ := json.Marshal(claims)
	signed := b64(head) + "." + b64(body)
	sum := sha256.Sum256([]byte(signed))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, p.Key, crypto.SHA256, sum[:])
	return signed + "." + b64(sig)
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

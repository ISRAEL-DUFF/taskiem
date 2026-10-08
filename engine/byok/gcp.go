package byok

// Google Cloud KMS, from the public REST reference: POST
// https://cloudkms.googleapis.com/v1/{name}:encrypt with a base64
// "plaintext" and "additionalAuthenticatedData" answers "ciphertext"; POST
// {name}:decrypt with "ciphertext" and the same additional data answers
// "plaintext". Errors are {"error":{"code":N,"message":"...","status":"..."}}
// (PERMISSION_DENIED, FAILED_PRECONDITION for a disabled or destroyed
// version, NOT_FOUND). Access tokens come from a service account key: an
// RS256-signed JWT (iss, scope, aud, iat, exp) exchanged at
// https://oauth2.googleapis.com/token with the JWT bearer grant.

import (
	"bytes"
	"context"
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
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	gcpTokenURL = "https://oauth2.googleapis.com/token" //nolint:gosec // a public endpoint, not a credential
	gcpKMSURL   = "https://cloudkms.googleapis.com"
	gcpScope    = "https://www.googleapis.com/auth/cloudkms"
)

type gcpKMS struct {
	f        *Factory
	c        Config
	client   *http.Client
	baseURL  string
	tokenURL string
	email    string
	keyID    string
	key      *rsa.PrivateKey
	aad      []byte
	tok      tokenCache
}

func newGCP(f *Factory, c Config, creds map[string]string, o Options) (*gcpKMS, error) {
	var sa struct {
		Type         string `json:"type"`
		ClientEmail  string `json:"client_email"`
		PrivateKey   string `json:"private_key"`
		PrivateKeyID string `json:"private_key_id"`
	}
	if err := json.Unmarshal([]byte(creds["service_account_json"]), &sa); err != nil || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, fmt.Errorf("%w: service_account_json must be a service account key file (client_email, private_key)", ErrInvalid)
	}
	if sa.Type != "" && sa.Type != "service_account" {
		return nil, fmt.Errorf("%w: service_account_json is a %q key, not a service account key", ErrInvalid, sa.Type)
	}
	key, err := parseRSAKey(sa.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("%w: service_account_json: %w", ErrInvalid, err)
	}
	base := f.endpoint(GCPKMS, gcpKMSURL)
	tokenURL := f.endpoint("gcp_token", gcpTokenURL)
	hc, err := f.client(o.Tenant, "byok:gcp_kms", []string{hostOf(base), hostOf(tokenURL)}, "")
	if err != nil {
		return nil, err
	}
	aad := []byte("taskiem/tenant-key")
	if o.Tenant != "" {
		aad = []byte("taskiem/tenant-key/" + o.Tenant)
	}
	return &gcpKMS{f: f, c: c, client: hc, baseURL: base, tokenURL: tokenURL, email: sa.ClientEmail, keyID: sa.PrivateKeyID, key: key, aad: aad}, nil
}

func parseRSAKey(p string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(p))
	if block == nil {
		return nil, errors.New("private_key is not PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("private_key is not an RSA key")
		}
		return rk, nil
	}
	k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("private_key is neither PKCS #8 nor PKCS #1 RSA")
	}
	return k, nil
}

// assertion signs the JWT the token endpoint exchanges for a token. Its
// audience is always Google's token URL.
func (g *gcpKMS) assertion(now time.Time) (string, error) {
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	if g.keyID != "" {
		header["kid"] = g.keyID
	}
	claims := map[string]any{"iss": g.email, "scope": gcpScope, "aud": gcpTokenURL, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()}
	h, _ := json.Marshal(header)
	cl, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(h) + "." + enc.EncodeToString(cl)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, g.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

func (g *gcpKMS) token(ctx context.Context) (string, error) {
	return g.tok.get(g.f.now(), func() (string, time.Duration, error) {
		a, err := g.assertion(g.f.now())
		if err != nil {
			return "", 0, unavailable(GCPKMS, "signing the token request: %v", err)
		}
		form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {a}}
		return mintToken(ctx, g.client, GCPKMS, g.tokenURL, form)
	})
}

func (g *gcpKMS) call(ctx context.Context, op string, body, out any) error {
	tok, err := g.token(ctx)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/v1/"+g.c.Key+":"+op, bytes.NewReader(raw))
	if err != nil {
		return unavailable(GCPKMS, "%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := g.client.Do(req)
	if err != nil {
		return unavailable(GCPKMS, "cannot reach Cloud KMS: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			g.tok.drop(tok)
		}
		var e struct {
			Error struct {
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		return unavailable(GCPKMS, "%s: http %d: %s %s", op, resp.StatusCode, e.Error.Status, clip(e.Error.Message))
	}
	if err := json.Unmarshal(b, out); err != nil {
		return unavailable(GCPKMS, "%s: unreadable answer", op)
	}
	return nil
}

func (g *gcpKMS) Wrap(ctx context.Context, plaintext []byte) (string, error) {
	var out struct {
		Ciphertext string `json:"ciphertext"`
	}
	if err := g.call(ctx, "encrypt", map[string]string{"plaintext": base64.StdEncoding.EncodeToString(plaintext), "additionalAuthenticatedData": base64.StdEncoding.EncodeToString(g.aad)}, &out); err != nil {
		return "", err
	}
	if out.Ciphertext == "" {
		return "", unavailable(GCPKMS, "encrypt returned no ciphertext")
	}
	return "gcp:" + out.Ciphertext, nil
}

func (g *gcpKMS) Unwrap(ctx context.Context, ciphertext string) ([]byte, error) {
	ct, ok := strings.CutPrefix(ciphertext, "gcp:")
	if !ok {
		return nil, fmt.Errorf("%s: not a Cloud KMS ciphertext: %w", GCPKMS, ErrUnavailable)
	}
	var out struct {
		Plaintext string `json:"plaintext"`
	}
	if err := g.call(ctx, "decrypt", map[string]string{"ciphertext": ct, "additionalAuthenticatedData": base64.StdEncoding.EncodeToString(g.aad)}, &out); err != nil {
		return nil, err
	}
	pt, err := base64.StdEncoding.DecodeString(out.Plaintext)
	if err != nil {
		return nil, unavailable(GCPKMS, "decrypt returned unreadable plaintext")
	}
	return pt, nil
}

// tokenCache holds one OAuth access token until shortly before it ends.
type tokenCache struct {
	mu    sync.Mutex
	token string
	exp   time.Time
}

func (t *tokenCache) get(now time.Time, mint func() (string, time.Duration, error)) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && now.Before(t.exp) {
		return t.token, nil
	}
	tok, ttl, err := mint()
	if err != nil {
		return "", err
	}
	t.token, t.exp = tok, now.Add(ttl-min(time.Minute, ttl/2))
	return tok, nil
}

func (t *tokenCache) drop(tok string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token == tok {
		t.token = ""
	}
}

// mintToken posts an OAuth 2.0 token request and reads access_token and
// expires_in (RFC 6749 section 5.1).
func mintToken(ctx context.Context, hc *http.Client, provider, tokenURL string, form url.Values) (string, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, unavailable(provider, "%v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", 0, unavailable(provider, "cannot reach the token endpoint: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var body struct {
		AccessToken string      `json:"access_token"`
		ExpiresIn   json.Number `json:"expires_in"`
		Error       string      `json:"error"`
		Description string      `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &body)
	if resp.StatusCode != http.StatusOK || body.AccessToken == "" {
		msg := strings.TrimSpace(body.Error + ": " + body.Description)
		return "", 0, unavailable(provider, "token request refused: http %d: %s", resp.StatusCode, clip(strings.Trim(msg, ": ")))
	}
	ttl := 5 * time.Minute
	if n, err := body.ExpiresIn.Int64(); err == nil && n > 0 {
		ttl = time.Duration(n) * time.Second
	}
	return body.AccessToken, ttl, nil
}

// Package google holds what the Google connectors (Gmail, Sheets) share:
// access tokens minted from a connection's credentials, cached until they
// expire, and calls to Google APIs with errors classified for the engine.
//
// Two kinds of connection are supported, as Google documents them:
//
//   - A service account key (the JSON file from the Cloud console). The
//     connector signs a JWT with the key (RS256) and exchanges it for an
//     access token with the JWT bearer grant
//     (developers.google.com/identity/protocols/oauth2/service-account).
//     A subject makes the token act as that Workspace user, which needs
//     domain-wide delegation; Gmail always needs it.
//   - An OAuth client and a refresh token obtained once with the user's
//     consent, exchanged with the refresh_token grant
//     (developers.google.com/identity/protocols/oauth2/web-server).
//
// Tokens are cached per credential set (and subject and scopes) until a
// minute before they expire. A 401 from the API drops the cached token and
// the call is repeated once with a fresh one: a 401 means Google refused the
// call before acting on it.
package google

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
)

// DefaultTokenURL is Google's token endpoint. It is also the audience of
// every service-account assertion, whatever endpoint the token is minted
// at (tests).
const DefaultTokenURL = "https://oauth2.googleapis.com/token" //nolint:gosec // a public endpoint, not a credential

// TokenHost is DefaultTokenURL's host, for connector egress lists.
const TokenHost = "oauth2.googleapis.com" //nolint:gosec // a host name, not a credential

const (
	jwtBearerGrant = "urn:ietf:params:oauth:grant-type:jwt-bearer" //nolint:gosec // a grant type name, not a credential
	// assertionLifetime is the maximum Google allows.
	assertionLifetime = time.Hour
	// expiryMargin renews a token this long before Google says it expires,
	// so a call never starts with a token about to lapse.
	expiryMargin = time.Minute
	// maxCached bounds the cache; expired entries are dropped past it.
	maxCached = 1024
	// maxResponse bounds what is read from Google (a message body, a range).
	maxResponse = 32 << 20
)

// Connection fields, shared by the Google connectors' manifests.
const (
	FieldServiceAccount = "service_account_json"
	FieldSubject        = "subject"
	FieldClientID       = "client_id"
	FieldClientSecret   = "client_secret"
	FieldRefreshToken   = "refresh_token"
	FieldScopes         = "scopes"
)

// Credentials are one connection's way of getting a token.
type Credentials struct {
	// Service account.
	ClientEmail  string
	PrivateKeyID string
	Key          *rsa.PrivateKey
	Subject      string
	Scopes       []string
	// OAuth client and refresh token.
	ClientID, ClientSecret, RefreshToken string

	cacheKey string
}

// ServiceAccount reports whether the credentials are a service account key.
func (c Credentials) ServiceAccount() bool { return c.Key != nil }

// serviceAccountKey is the part of Google's JSON key file the connector uses.
type serviceAccountKey struct {
	Type         string `json:"type"`
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id"`
}

// FromConnection reads a connection's credentials: a service account key
// (service_account_json, optional subject and scopes) or an OAuth client
// with a refresh token (client_id, client_secret, refresh_token). Scopes
// default to defaultScopes; they are only sent for service accounts (a
// refresh token carries the scopes the user granted). Problems are fatal:
// the connection must be fixed.
func FromConnection(conn map[string]string, defaultScopes []string) (Credentials, error) {
	sa := strings.TrimSpace(conn[FieldServiceAccount])
	rt := strings.TrimSpace(conn[FieldRefreshToken])
	switch {
	case sa != "" && rt != "":
		return Credentials{}, fmt.Errorf("google: the connection has both a service account key and a refresh token; keep one: %w", effects.ErrFatal)
	case sa != "":
		var k serviceAccountKey
		if err := json.Unmarshal([]byte(sa), &k); err != nil {
			return Credentials{}, fmt.Errorf("google: service_account_json is not a JSON key file: %w", effects.ErrFatal)
		}
		if k.Type != "" && k.Type != "service_account" {
			return Credentials{}, fmt.Errorf("google: service_account_json is a %q key, not a service account key: %w", k.Type, effects.ErrFatal)
		}
		if k.ClientEmail == "" || k.PrivateKey == "" {
			return Credentials{}, fmt.Errorf("google: service_account_json has no client_email or private_key: %w", effects.ErrFatal)
		}
		key, err := ParsePrivateKey(k.PrivateKey)
		if err != nil {
			return Credentials{}, fmt.Errorf("google: service_account_json: %w: %w", err, effects.ErrFatal)
		}
		scopes := defaultScopes
		if s := strings.Fields(strings.ReplaceAll(conn[FieldScopes], ",", " ")); len(s) > 0 {
			scopes = s
		}
		scopes = append([]string(nil), scopes...)
		sort.Strings(scopes)
		c := Credentials{ClientEmail: k.ClientEmail, PrivateKeyID: k.PrivateKeyID, Key: key, Subject: strings.TrimSpace(conn[FieldSubject]), Scopes: scopes}
		h := sha256.New()
		for _, p := range []string{"sa", k.ClientEmail, k.PrivateKeyID, k.PrivateKey, c.Subject, strings.Join(scopes, " ")} {
			h.Write([]byte(p))
			h.Write([]byte{0})
		}
		c.cacheKey = hex.EncodeToString(h.Sum(nil))
		return c, nil
	case rt != "":
		c := Credentials{ClientID: strings.TrimSpace(conn[FieldClientID]), ClientSecret: strings.TrimSpace(conn[FieldClientSecret]), RefreshToken: rt}
		if c.ClientID == "" {
			return Credentials{}, fmt.Errorf("google: a refresh token needs the OAuth client_id it was issued to: %w", effects.ErrFatal)
		}
		h := sha256.New()
		for _, p := range []string{"rt", c.ClientID, c.ClientSecret, c.RefreshToken} {
			h.Write([]byte(p))
			h.Write([]byte{0})
		}
		c.cacheKey = hex.EncodeToString(h.Sum(nil))
		return c, nil
	}
	return Credentials{}, fmt.Errorf("google: the connection needs service_account_json, or client_id, client_secret and refresh_token: %w", effects.ErrFatal)
}

// ParsePrivateKey reads the PEM RSA key of a service account key file
// (PKCS #8, as Google issues them, or PKCS #1).
func ParsePrivateKey(p string) (*rsa.PrivateKey, error) {
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

// Tokens mints and caches access tokens. It is safe for concurrent use.
type Tokens struct {
	// TokenURL is where tokens are minted; DefaultTokenURL unless a test
	// overrides it.
	TokenURL string
	// Now is the clock (tests).
	Now func() time.Time

	mu    sync.Mutex
	cache map[string]*entry
}

type entry struct {
	mu     sync.Mutex // one mint at a time per credential set
	token  string
	expiry time.Time
}

// NewTokens returns a token cache minting at tokenURL (DefaultTokenURL when
// empty).
func NewTokens(tokenURL string) *Tokens {
	if tokenURL == "" {
		tokenURL = DefaultTokenURL
	}
	return &Tokens{TokenURL: tokenURL, Now: time.Now, cache: map[string]*entry{}}
}

func (t *Tokens) entry(key string) *entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.cache[key]
	if !ok {
		if len(t.cache) >= maxCached {
			now := t.Now()
			for k, old := range t.cache {
				if old.mu.TryLock() {
					if !now.Before(old.expiry) {
						delete(t.cache, k)
					}
					old.mu.Unlock()
				}
			}
		}
		e = &entry{}
		t.cache[key] = e
	}
	return e
}

// Token returns a valid access token for the credentials, minting one with
// hc when none is cached. Failures wrap effects.ErrNotSent (the API call
// was never made) and, when retrying cannot help, effects.ErrFatal.
func (t *Tokens) Token(ctx context.Context, hc *http.Client, c Credentials) (string, error) {
	if c.cacheKey == "" {
		return "", fmt.Errorf("google: credentials not read with FromConnection: %w", effects.ErrFatal)
	}
	e := t.entry(c.cacheKey)
	e.mu.Lock()
	defer e.mu.Unlock()
	now := t.Now()
	if e.token != "" && now.Before(e.expiry) {
		return e.token, nil
	}
	form := url.Values{}
	if c.ServiceAccount() {
		assertion, err := SignAssertion(c, now)
		if err != nil {
			return "", fmt.Errorf("google: signing the assertion: %w: %w", err, effects.ErrFatal)
		}
		form.Set("grant_type", jwtBearerGrant)
		form.Set("assertion", assertion)
	} else {
		form.Set("grant_type", "refresh_token")
		form.Set("client_id", c.ClientID)
		if c.ClientSecret != "" {
			form.Set("client_secret", c.ClientSecret)
		}
		form.Set("refresh_token", c.RefreshToken)
	}
	tok, ttl, err := t.mint(ctx, hc, form)
	if err != nil {
		return "", err
	}
	e.token, e.expiry = tok, now.Add(ttl-expiryMargin)
	return tok, nil
}

// Invalidate drops token from the cache if it is still the cached one.
func (t *Tokens) Invalidate(c Credentials, token string) {
	e := t.entry(c.cacheKey)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.token == token {
		e.token, e.expiry = "", time.Time{}
	}
}

func (t *Tokens) mint(ctx context.Context, hc *http.Client, form url.Values) (string, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("google token: %w: %w", err, effects.ErrFatal)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if errors.Is(err, egress.ErrDenied) {
		return "", 0, fmt.Errorf("google token: %w: %w", err, effects.ErrFatal)
	}
	if err != nil {
		// Whatever happened to the token request, the API was not called.
		return "", 0, fmt.Errorf("google token: %w: %w", connector.ClassifyTransport(err), effects.ErrNotSent)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", 0, fmt.Errorf("google token: reading the response: %w: %w: %w", err, effects.ErrRetryable, effects.ErrNotSent)
	}
	var body struct {
		AccessToken string      `json:"access_token"`
		ExpiresIn   json.Number `json:"expires_in"`
		Error       string      `json:"error"`
		Description string      `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &body)
	if resp.StatusCode != http.StatusOK {
		msg := body.Error
		if body.Description != "" {
			msg += ": " + body.Description
		}
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
			if len(msg) > 200 {
				msg = msg[:200]
			}
		}
		kind := effects.ErrFatal // invalid_grant, invalid_client, a client not allowed, ...
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			kind = effects.ErrRetryable
		}
		return "", 0, fmt.Errorf("google token %d: %s: %w: %w", resp.StatusCode, msg, kind, effects.ErrNotSent)
	}
	if body.AccessToken == "" {
		return "", 0, fmt.Errorf("google token: no access_token in the response: %w: %w", effects.ErrRetryable, effects.ErrNotSent)
	}
	ttl := 5 * time.Minute // not documented as optional, but be safe without it
	if n, err := body.ExpiresIn.Int64(); err == nil && n > 0 {
		ttl = time.Duration(n) * time.Second
	}
	if ttl <= expiryMargin {
		ttl = expiryMargin + time.Second
	}
	return body.AccessToken, ttl, nil
}

// SignAssertion builds the service account's signed JWT for the token
// request: header {alg: RS256, typ: JWT, kid}, claims iss, scope, aud, iat,
// exp (one hour, the maximum) and sub when impersonating a user.
func SignAssertion(c Credentials, now time.Time) (string, error) {
	if c.Key == nil {
		return "", errors.New("no private key")
	}
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	if c.PrivateKeyID != "" {
		header["kid"] = c.PrivateKeyID
	}
	claims := map[string]any{
		"iss":   c.ClientEmail,
		"scope": strings.Join(c.Scopes, " "),
		"aud":   DefaultTokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(assertionLifetime).Unix(),
	}
	if c.Subject != "" {
		claims["sub"] = c.Subject
	}
	h, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	cl, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(h) + "." + enc.EncodeToString(cl)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.Key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

// APIError is a refusal from a Google API, from its standard error body.
type APIError struct {
	Status     int
	Message    string
	State      string // e.g. NOT_FOUND, PERMISSION_DENIED, RESOURCE_EXHAUSTED
	Reason     string // e.g. rateLimitExceeded, notFound
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "google api %d", e.Status)
	if e.State != "" {
		fmt.Fprintf(&b, " %s", e.State)
	}
	if e.Reason != "" && e.Reason != e.State {
		fmt.Fprintf(&b, " (%s)", e.Reason)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	return b.String()
}

// IsNotFound reports whether err is Google's 404.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// rateReasons are 403 reasons Google uses for quota refusals: the request
// was refused before it was processed, so it may be sent again.
var rateReasons = map[string]bool{
	"rateLimitExceeded": true, "userRateLimitExceeded": true, "dailyLimitExceeded": true,
	"quotaExceeded": true, "limitExceeded": true,
}

func parseAPIError(status int, raw []byte, h http.Header) *APIError {
	var body struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
			Errors  []struct {
				Reason  string `json:"reason"`
				Message string `json:"message"`
			} `json:"errors"`
			Details []struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	ae := &APIError{Status: status}
	if json.Unmarshal(raw, &body) == nil {
		ae.Message, ae.State = body.Error.Message, body.Error.Status
		if len(body.Error.Errors) > 0 {
			ae.Reason = body.Error.Errors[0].Reason
		}
		if ae.Reason == "" {
			for _, d := range body.Error.Details {
				if d.Reason != "" {
					ae.Reason = d.Reason
					break
				}
			}
		}
	}
	if ae.Message == "" && ae.State == "" {
		m := strings.TrimSpace(string(raw))
		if len(m) > 300 {
			m = m[:300]
		}
		ae.Message = m
	}
	if s, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After"))); err == nil && s >= 0 {
		ae.RetryAfter = time.Duration(s) * time.Second
	}
	return ae
}

// classify wraps an API refusal with the engine's error class.
func classify(ae *APIError) error {
	switch {
	case ae.Status == http.StatusTooManyRequests,
		ae.Status == http.StatusForbidden && (rateReasons[ae.Reason] || ae.State == "RESOURCE_EXHAUSTED"):
		// Quota: refused before processing, so even an unsafe write may
		// be sent again (action-classes.md: "4xx before body").
		return fmt.Errorf("%w: %w: %w", ae, effects.ErrRetryable, effects.ErrNotSent)
	case ae.Status == http.StatusServiceUnavailable:
		return fmt.Errorf("%w: %w", ae, effects.ErrRetryable)
	case ae.Status >= 500:
		// 500, 502, 504: Google may have acted before failing.
		return fmt.Errorf("%w: %w", ae, effects.ErrUnknownOutcome)
	}
	return fmt.Errorf("%w: %w", ae, effects.ErrFatal)
}

// Do calls a Google API with a token for c: body (when not nil) is sent as
// JSON and the JSON response decoded into out. Errors are classified for
// the engine; refusals carry *APIError.
func (t *Tokens) Do(ctx context.Context, hc *http.Client, c Credentials, method, endpoint string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("google: encoding the request: %w: %w", err, effects.ErrFatal)
		}
	}
	for attempt := 0; ; attempt++ {
		tok, err := t.Token(ctx, hc, c)
		if err != nil {
			return err
		}
		var rd io.Reader
		if payload != nil {
			rd = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, rd)
		if err != nil {
			return fmt.Errorf("google: %w: %w", err, effects.ErrFatal)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json; charset=utf-8")
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := hc.Do(req)
		if err != nil {
			return connector.ClassifyTransport(err)
		}
		raw, rerr := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
		_ = resp.Body.Close()
		if rerr != nil {
			return fmt.Errorf("google: reading the response: %w: %w", rerr, effects.ErrUnknownOutcome)
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			// The token was refused (revoked, or expired early); the call
			// did nothing. Mint a new one and try once more.
			t.Invalidate(c, tok)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return classify(parseAPIError(resp.StatusCode, raw, resp.Header))
		}
		if len(raw) > maxResponse {
			return fmt.Errorf("google: response larger than %d bytes: %w", maxResponse, effects.ErrUnknownOutcome)
		}
		if out != nil && len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, out); err != nil {
				return fmt.Errorf("google: unreadable response: %w: %w", err, effects.ErrUnknownOutcome)
			}
		}
		return nil
	}
}

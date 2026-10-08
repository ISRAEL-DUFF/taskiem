// Package oidc signs people in with an OpenID Connect provider: the
// authorization code flow with PKCE, and ID token verification against
// the provider's published keys (OpenID Connect Core 1.0, RFC 7636,
// RFC 7515/7518). Only RS256, PS256 and ES256 are accepted, never "none"
// or HMAC, and the algorithm must match the key's type.
package oidc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// ErrInvalid marks a token or response that must not be trusted.
var ErrInvalid = errors.New("oidc: invalid")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, args...)...)
}

// Provider is one identity provider for one client.
type Provider struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	// AuthMethod is client_secret_basic (default) or client_secret_post.
	AuthMethod string
	HTTP       *http.Client

	once      sync.Mutex
	discovery *Discovery
	keys      map[string]any // kid -> *rsa.PublicKey | *ecdsa.PublicKey
	keysAt    time.Time
}

// Discovery is the provider's published configuration.
type Discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

func (p *Provider) get(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oidc: GET %s: %s", u, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// Discover reads (once) the provider's configuration, which must name the
// configured issuer exactly.
func (p *Provider) Discover(ctx context.Context) (*Discovery, error) {
	p.once.Lock()
	defer p.once.Unlock()
	if p.discovery != nil {
		return p.discovery, nil
	}
	var d Discovery
	if err := p.get(ctx, strings.TrimRight(p.Issuer, "/")+"/.well-known/openid-configuration", &d); err != nil {
		return nil, err
	}
	if d.Issuer != p.Issuer {
		return nil, invalid("discovery names issuer %q, not %q", d.Issuer, p.Issuer)
	}
	for _, u := range []string{d.AuthorizationEndpoint, d.TokenEndpoint, d.JWKSURI} {
		pu, err := url.Parse(u)
		if err != nil || (pu.Scheme != "https" && pu.Hostname() != "127.0.0.1" && pu.Hostname() != "localhost") {
			return nil, invalid("endpoint %q is not https", u)
		}
	}
	p.discovery = &d
	return &d, nil
}

// NewVerifier returns a PKCE code verifier (RFC 7636).
func NewVerifier() string { return random(32) }

// Challenge is the S256 code challenge for a verifier.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// NewNonce returns a random nonce or state value.
func NewNonce() string { return random(24) }

func random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// AuthURL is where to send the person to sign in.
func (p *Provider) AuthURL(ctx context.Context, redirect, state, nonce, verifier string, scopes []string) (string, error) {
	d, err := p.Discover(ctx)
	if err != nil {
		return "", err
	}
	q := url.Values{"response_type": {"code"}, "client_id": {p.ClientID}, "redirect_uri": {redirect}, "scope": {strings.Join(scopes, " ")},
		"state": {state}, "nonce": {nonce}, "code_challenge": {Challenge(verifier)}, "code_challenge_method": {"S256"}}
	sep := "?"
	if strings.Contains(d.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return d.AuthorizationEndpoint + sep + q.Encode(), nil
}

// Exchange trades an authorization code for the ID token.
func (p *Provider) Exchange(ctx context.Context, code, redirect, verifier string) (string, error) {
	d, err := p.Discover(ctx)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}}
	if p.AuthMethod == "client_secret_post" {
		form.Set("client_id", p.ClientID)
		form.Set("client_secret", p.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if p.AuthMethod != "client_secret_post" {
		req.SetBasicAuth(url.QueryEscape(p.ClientID), url.QueryEscape(p.ClientSecret))
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var tok struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok); err != nil {
		return "", fmt.Errorf("oidc: token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || tok.IDToken == "" {
		return "", fmt.Errorf("oidc: the provider refused the code: %s %s", tok.Error, tok.Desc)
	}
	return tok.IDToken, nil
}

// Claims are the ID token's claims that sign-in uses.
type Claims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Groups        []string
}

// Verify checks an ID token's signature, issuer, audience, times and nonce.
// groupsClaim names the claim listing the person's groups ("" for none).
func (p *Provider) Verify(ctx context.Context, raw, nonce, groupsClaim string, now time.Time) (Claims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Claims{}, invalid("not a signed JWT")
	}
	var head struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeSegment(parts[0], &head); err != nil {
		return Claims{}, invalid("header: %v", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, invalid("signature encoding")
	}
	key, err := p.key(ctx, head.Kid)
	if err != nil {
		return Claims{}, err
	}
	if !verifySig(head.Alg, key, []byte(parts[0]+"."+parts[1]), sig) {
		return Claims{}, invalid("signature does not verify (alg %q)", head.Alg)
	}
	var c map[string]any
	if err := decodeSegment(parts[1], &c); err != nil {
		return Claims{}, invalid("claims: %v", err)
	}
	if iss, _ := c["iss"].(string); iss != p.Issuer {
		return Claims{}, invalid("issuer %q", iss)
	}
	aud := audiences(c["aud"])
	if !slices.Contains(aud, p.ClientID) {
		return Claims{}, invalid("not issued for this client")
	}
	if azp, ok := c["azp"].(string); (len(aud) > 1 || ok) && azp != p.ClientID {
		return Claims{}, invalid("authorized party %q", azp)
	}
	const skew = 2 * time.Minute
	exp, ok := numeric(c["exp"])
	if !ok || now.After(time.Unix(exp, 0).Add(skew)) {
		return Claims{}, invalid("expired")
	}
	if iat, ok := numeric(c["iat"]); !ok || time.Unix(iat, 0).After(now.Add(skew)) || now.Sub(time.Unix(iat, 0)) > time.Hour {
		return Claims{}, invalid("issued at an implausible time")
	}
	if nbf, ok := numeric(c["nbf"]); ok && time.Unix(nbf, 0).After(now.Add(skew)) {
		return Claims{}, invalid("not valid yet")
	}
	if got, _ := c["nonce"].(string); nonce == "" || got != nonce {
		return Claims{}, invalid("nonce does not match")
	}
	out := Claims{}
	out.Subject, _ = c["sub"].(string)
	out.Email, _ = c["email"].(string)
	out.Name, _ = c["name"].(string)
	switch v := c["email_verified"].(type) {
	case bool:
		out.EmailVerified = v
	case string:
		out.EmailVerified = v == "true"
	}
	if groupsClaim != "" {
		switch g := c[groupsClaim].(type) {
		case []any:
			for _, x := range g {
				if s, ok := x.(string); ok {
					out.Groups = append(out.Groups, s)
				}
			}
		case string:
			out.Groups = []string{g}
		}
	}
	if out.Subject == "" {
		return Claims{}, invalid("no subject")
	}
	return out, nil
}

func decodeSegment(s string, out any) error {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	return dec.Decode(out)
}

func audiences(v any) []string {
	switch a := v.(type) {
	case string:
		return []string{a}
	case []any:
		var out []string
		for _, x := range a {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func numeric(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			f, ferr := n.Float64()
			return int64(f), ferr == nil
		}
		return i, true
	case float64:
		return int64(n), true
	}
	return 0, false
}

func verifySig(alg string, key any, signed, sig []byte) bool {
	sum := sha256.Sum256(signed)
	switch k := key.(type) {
	case *rsa.PublicKey:
		switch alg {
		case "RS256":
			return rsa.VerifyPKCS1v15(k, crypto.SHA256, sum[:], sig) == nil
		case "PS256":
			return rsa.VerifyPSS(k, crypto.SHA256, sum[:], sig, nil) == nil
		}
	case *ecdsa.PublicKey:
		if alg == "ES256" && len(sig) == 64 {
			r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
			return ecdsa.Verify(k, sum[:], r, s)
		}
	}
	return false
}

// key finds the signing key by id, refetching the key set (at most once a
// minute) when the provider has rotated to a key not yet seen.
func (p *Provider) key(ctx context.Context, kid string) (any, error) {
	p.once.Lock()
	k, ok := p.keys[kid]
	stale := time.Since(p.keysAt) > time.Minute
	p.once.Unlock()
	if ok {
		return k, nil
	}
	if !stale && p.keys != nil {
		return nil, invalid("unknown signing key %q", kid)
	}
	d, err := p.Discover(ctx)
	if err != nil {
		return nil, err
	}
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
		} `json:"keys"`
	}
	if err := p.get(ctx, d.JWKSURI, &set); err != nil {
		return nil, err
	}
	keys := map[string]any{}
	for _, j := range set.Keys {
		if j.Use != "" && j.Use != "sig" {
			continue
		}
		dec := base64.RawURLEncoding.DecodeString
		switch j.Kty {
		case "RSA":
			n, err1 := dec(j.N)
			e, err2 := dec(j.E)
			if err1 != nil || err2 != nil || len(n)*8 < 2048 || len(e) > 4 {
				continue
			}
			keys[j.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		case "EC":
			x, err1 := dec(j.X)
			y, err2 := dec(j.Y)
			if err1 != nil || err2 != nil || j.Crv != "P-256" || len(x) != 32 || len(y) != 32 {
				continue
			}
			pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
			if err != nil {
				continue
			}
			keys[j.Kid] = pub
		}
	}
	p.once.Lock()
	p.keys, p.keysAt = keys, time.Now()
	p.once.Unlock()
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, invalid("unknown signing key %q", kid)
}

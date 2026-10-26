package ingest

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/wd"
)

// webhookOptions are a webhook trigger's settings besides its path, auth
// and dedup (wd-v1 webhook triggers), kept in triggers.options.
type webhookOptions struct {
	Methods []string     `json:"methods,omitempty"`
	JWT     *jwtSettings `json:"jwt,omitempty"`
	CORS    []string     `json:"cors,omitempty"` // allowed origins; "*" for any
}

// jwtSettings are what a token must say besides being signed and current.
type jwtSettings struct {
	Issuer     string   `json:"issuer,omitempty"`
	Audience   string   `json:"audience,omitempty"`
	Algorithms []string `json:"algorithms,omitempty"`
}

func parseWebhookOptions(c map[string]any) (webhookOptions, error) {
	o := webhookOptions{Methods: wd.WebhookConfigMethods(c)}
	if j, ok := c["jwt"]; ok {
		raw, _ := json.Marshal(j)
		o.JWT = &jwtSettings{}
		if err := json.Unmarshal(raw, o.JWT); err != nil {
			return o, fmt.Errorf("jwt: %w", err)
		}
	}
	if cors, ok := c["cors"].(map[string]any); ok {
		origins, _ := cors["origins"].([]any)
		for _, v := range origins {
			s, _ := v.(string)
			if s != "*" {
				if err := wd.CheckOrigin(s); err != nil {
					return o, fmt.Errorf("cors: %w", err)
				}
			}
			o.CORS = append(o.CORS, s)
		}
	}
	return o, nil
}

// stored is the options as triggers.options keeps them: nil when they are
// all the defaults, so a POST-only webhook's row is what it always was.
func (o webhookOptions) stored() []byte {
	if slices.Equal(o.Methods, wd.WebhookMethods) && o.JWT == nil && len(o.CORS) == 0 {
		return nil
	}
	raw, _ := json.Marshal(o)
	return raw
}

func loadWebhookOptions(raw []byte) webhookOptions {
	var o webhookOptions
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &o)
	}
	if len(o.Methods) == 0 {
		o.Methods = wd.WebhookMethods
	}
	return o
}

// origin is what to answer a browser request's Origin with: allowed and
// the Access-Control-Allow-Origin value, or not allowed. A request with
// no Origin is not a browser's cross-origin call and needs no answer.
func (o webhookOptions) origin(origin string) (string, bool) {
	for _, a := range o.CORS {
		if a == "*" {
			return "*", true
		}
		if a == origin {
			return origin, true
		}
	}
	return "", false
}

// corsHeaders sets the headers that let the page at origin read the
// answer, when the webhook allows it; ok is false when it does not.
func (o webhookOptions) corsHeaders(w http.ResponseWriter, origin string) bool {
	if origin == "" || len(o.CORS) == 0 {
		return true // not a browser's call, or a webhook that ignores Origin as before
	}
	allow, ok := o.origin(origin)
	if !ok {
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", allow)
	w.Header().Set("Access-Control-Expose-Headers", "X-Taskiem-Run")
	w.Header().Add("Vary", "Origin")
	return true
}

// preflight answers a browser's OPTIONS before a cross-origin call: the
// methods and headers the webhook takes, cached for ten minutes. A call
// from an origin the webhook does not allow gets 403 and no CORS headers,
// so the browser never sends it.
func (o webhookOptions) preflight(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	want := r.Header.Get("Access-Control-Request-Method")
	allow, ok := o.origin(origin)
	if origin == "" || !ok || !slices.Contains(o.Methods, want) {
		replyErr(w, http.StatusForbidden, "this webhook does not take that call from this origin")
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", allow)
	w.Header().Set("Access-Control-Allow-Methods", strings.Join(o.Methods, ", "))
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.Header().Add("Vary", "Origin")
	w.WriteHeader(http.StatusNoContent)
}

// errCallRefused is a delivery whose auth does not check out.
var errCallRefused = errors.New("signature or token invalid")

// verifyCall checks a webhook delivery's auth with the environment secret
// it names. A refusal wraps errCallRefused and says why in words a person
// testing the webhook can act on; any other error means the secret could
// not be read and the delivery should be retried.
func (h *Handler) verifyCall(ctx context.Context, tenant uuid.UUID, env, auth string, secretName *string, o webhookOptions, hdr http.Header, body []byte) error {
	if auth == "none" {
		return nil
	}
	secret := ""
	if secretName != nil {
		use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindWebhook, Purpose: secrets.PurposeIngestVerify})
		var err error
		secret, err = h.Secrets.Get(use, tenant, env, *secretName)
		if err != nil && !errors.Is(err, secrets.ErrNotFound) {
			return err
		}
		if secret == "" {
			return fmt.Errorf("%w: %s has no secret %s yet", errCallRefused, env, *secretName)
		}
	}
	switch auth {
	case "hmac", "bearer":
		scheme := map[string]string{"hmac": "hmac_sha256", "bearer": "bearer"}[auth]
		if err := connector.VerifyWebhook(&connector.VerifySpec{Scheme: scheme, Header: signatureHeader}, secret, hdr, body); err != nil {
			return fmt.Errorf("%w: %w", errCallRefused, err)
		}
		return nil
	case "basic":
		return checkBasic(secret, hdr.Get("Authorization"))
	case "jwt":
		var s jwtSettings
		if o.JWT != nil {
			s = *o.JWT
		}
		return checkJWT(secret, s, hdr.Get("Authorization"), time.Now())
	}
	return fmt.Errorf("%w: auth %q is not checked here", errCallRefused, auth)
}

// checkBasic checks HTTP basic auth against a secret holding
// "user:password". Both are hashed before comparing, so the time taken
// says nothing of their lengths.
func checkBasic(secret, header string) error {
	if !strings.Contains(secret, ":") {
		return fmt.Errorf("%w: the secret must be user:password", errCallRefused)
	}
	enc, ok := strings.CutPrefix(header, "Basic ")
	if !ok {
		return fmt.Errorf("%w: no Authorization: Basic header", errCallRefused)
	}
	got, err := base64.StdEncoding.DecodeString(strings.TrimSpace(enc))
	if err != nil {
		return fmt.Errorf("%w: the Basic credentials are not base64", errCallRefused)
	}
	a, b := sha256.Sum256(got), sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
		return fmt.Errorf("%w: wrong user name or password", errCallRefused)
	}
	return nil
}

// jwtLeeway is how far clocks may disagree on a token's times.
const jwtLeeway = time.Minute

// checkJWT checks a bearer JWT (RFC 7519) against the secret: a shared
// key for HS256/384/512, or a PEM public key (or certificate) for RSA or
// ECDSA. The token must be signed by an allowed algorithm (never "none"),
// carry exp and be current, and name the issuer and audience the
// settings ask for.
func checkJWT(secret string, s jwtSettings, header string, now time.Time) error {
	tok, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return fmt.Errorf("%w: no Authorization: Bearer header", errCallRefused)
	}
	parts := strings.Split(strings.TrimSpace(tok), ".")
	if len(parts) != 3 {
		return fmt.Errorf("%w: the bearer token is not a JWT", errCallRefused)
	}
	var head struct {
		Alg string `json:"alg"`
	}
	if err := decodeSegment(parts[0], &head); err != nil {
		return fmt.Errorf("%w: the JWT header: %w", errCallRefused, err)
	}
	key, fits, err := jwtKey(secret)
	if err != nil {
		return fmt.Errorf("%w: %w", errCallRefused, err)
	}
	allowed := fits
	if len(s.Algorithms) > 0 {
		allowed = slices.DeleteFunc(slices.Clone(s.Algorithms), func(a string) bool { return !slices.Contains(fits, a) })
	}
	if !slices.Contains(allowed, head.Alg) {
		return fmt.Errorf("%w: the JWT is signed with %q; this webhook takes %s", errCallRefused, head.Alg, strings.Join(allowed, ", "))
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("%w: the JWT signature is not base64url", errCallRefused)
	}
	if !verifyJWTSignature(head.Alg, key, []byte(parts[0]+"."+parts[1]), sig) {
		return fmt.Errorf("%w: the JWT signature does not match the key", errCallRefused)
	}
	var claims map[string]any
	if err := decodeSegment(parts[1], &claims); err != nil {
		return fmt.Errorf("%w: the JWT claims: %w", errCallRefused, err)
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		return fmt.Errorf("%w: the JWT has no exp: give tokens an expiry", errCallRefused)
	}
	if now.After(time.Unix(int64(exp), 0).Add(jwtLeeway)) {
		return fmt.Errorf("%w: the JWT has expired", errCallRefused)
	}
	if nbf, ok := claims["nbf"].(float64); ok && now.Add(jwtLeeway).Before(time.Unix(int64(nbf), 0)) {
		return fmt.Errorf("%w: the JWT is not valid yet (nbf)", errCallRefused)
	}
	if s.Issuer != "" && claims["iss"] != s.Issuer {
		return fmt.Errorf("%w: the JWT's iss is not %q", errCallRefused, s.Issuer)
	}
	if s.Audience != "" && !hasAudience(claims["aud"], s.Audience) {
		return fmt.Errorf("%w: the JWT's aud does not include %q", errCallRefused, s.Audience)
	}
	return nil
}

func decodeSegment(seg string, v any) error {
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return errors.New("not base64url")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return errors.New("not a JSON object")
	}
	return nil
}

func hasAudience(aud any, want string) bool {
	switch a := aud.(type) {
	case string:
		return a == want
	case []any:
		return slices.Contains(a, any(want))
	}
	return false
}

// jwtKey reads the secret as a key and the algorithms it can check: a PEM
// public key or certificate (RSA: RS256/384/512; ECDSA P-256: ES256;
// P-384: ES384), otherwise a shared HMAC key.
func jwtKey(secret string) (any, []string, error) {
	if !strings.HasPrefix(strings.TrimSpace(secret), "-----BEGIN") {
		return []byte(secret), []string{"HS256", "HS384", "HS512"}, nil
	}
	block, _ := pem.Decode([]byte(strings.TrimSpace(secret)))
	if block == nil {
		return nil, nil, errors.New("the secret looks like PEM but does not parse")
	}
	var pub any
	switch block.Type {
	case "PUBLIC KEY":
		k, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, nil, fmt.Errorf("the public key: %w", err)
		}
		pub = k
	case "CERTIFICATE":
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, nil, fmt.Errorf("the certificate: %w", err)
		}
		pub = c.PublicKey
	default:
		return nil, nil, fmt.Errorf("the secret is a PEM %s; give the public key (PUBLIC KEY) or certificate, never a private key", block.Type)
	}
	switch k := pub.(type) {
	case *rsa.PublicKey:
		return k, []string{"RS256", "RS384", "RS512"}, nil
	case *ecdsa.PublicKey:
		switch k.Curve {
		case elliptic.P256():
			return k, []string{"ES256"}, nil
		case elliptic.P384():
			return k, []string{"ES384"}, nil
		}
	}
	return nil, nil, errors.New("the key is neither RSA nor ECDSA P-256 or P-384")
}

func verifyJWTSignature(alg string, key any, signed, sig []byte) bool {
	var hf crypto.Hash
	switch alg[2:] {
	case "256":
		hf = crypto.SHA256
	case "384":
		hf = crypto.SHA384
	case "512":
		hf = crypto.SHA512
	default:
		return false
	}
	switch alg[:2] {
	case "HS":
		k, ok := key.([]byte)
		if !ok {
			return false
		}
		m := hmac.New(map[crypto.Hash]func() hash.Hash{crypto.SHA256: sha256.New, crypto.SHA384: sha512.New384, crypto.SHA512: sha512.New}[hf], k)
		m.Write(signed)
		return hmac.Equal(m.Sum(nil), sig)
	case "RS":
		k, ok := key.(*rsa.PublicKey)
		if !ok {
			return false
		}
		d := hf.New()
		d.Write(signed)
		return rsa.VerifyPKCS1v15(k, hf, d.Sum(nil), sig) == nil
	case "ES":
		k, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return false
		}
		size := (k.Curve.Params().BitSize + 7) / 8
		if len(sig) != 2*size {
			return false
		}
		d := hf.New()
		d.Write(signed)
		r, s := new(big.Int).SetBytes(sig[:size]), new(big.Int).SetBytes(sig[size:])
		return ecdsa.Verify(k, d.Sum(nil), r, s)
	}
	return false
}

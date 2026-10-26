package ingest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"
)

// jwtOf makes a token: the header and claims given, signed by sign.
func jwtOf(t *testing.T, head, claims map[string]any, sign func([]byte) []byte) string {
	t.Helper()
	enc := func(v any) string {
		raw, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	signed := enc(head) + "." + enc(claims)
	return signed + "." + base64.RawURLEncoding.EncodeToString(sign([]byte(signed)))
}

func hs256(key string) func([]byte) []byte {
	return func(b []byte) []byte {
		m := hmac.New(sha256.New, []byte(key))
		m.Write(b)
		return m.Sum(nil)
	}
}

func pemOf(t *testing.T, pub any) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func TestCheckJWT(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	exp := float64(now.Add(5 * time.Minute).Unix())
	key := "whsec_shared"
	good := map[string]any{"exp": exp, "iss": "shop", "aud": []any{"taskiem", "other"}}
	hs := map[string]any{"alg": "HS256", "typ": "JWT"}

	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	rs := func(b []byte) []byte {
		d := sha256.Sum256(b)
		sig, _ := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, d[:])
		return sig
	}
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	es := func(b []byte) []byte {
		d := sha256.Sum256(b)
		r, s, _ := ecdsa.Sign(rand.Reader, ecKey, d[:])
		out := make([]byte, 64)
		r.FillBytes(out[:32])
		s.FillBytes(out[32:])
		return out
	}
	rsaPEM, ecPEM := pemOf(t, &rsaKey.PublicKey), pemOf(t, &ecKey.PublicKey)
	settings := jwtSettings{Issuer: "shop", Audience: "taskiem"}

	for _, c := range []struct {
		name, secret, token, want string
		s                         jwtSettings
	}{
		{"hs256", key, jwtOf(t, hs, good, hs256(key)), "", settings},
		{"rs256", rsaPEM, jwtOf(t, map[string]any{"alg": "RS256"}, good, rs), "", settings},
		{"es256", ecPEM, jwtOf(t, map[string]any{"alg": "ES256"}, good, es), "", settings},
		{"wrong key", key, jwtOf(t, hs, good, hs256("other")), "does not match", settings},
		{"alg none", key, jwtOf(t, map[string]any{"alg": "none"}, good, func([]byte) []byte { return nil }), `signed with "none"`, settings},
		// A shared key never checks an RSA token, nor a public key an HMAC one
		// (the key-confusion attack: an HS256 token "signed" with the PEM text).
		{"hs on a public key", rsaPEM, jwtOf(t, hs, good, hs256(rsaPEM)), `signed with "HS256"`, settings},
		{"algorithm not allowed", key, jwtOf(t, map[string]any{"alg": "HS512"}, good, hs256(key)), `signed with "HS512"`, jwtSettings{Algorithms: []string{"HS256"}}},
		{"expired", key, jwtOf(t, hs, map[string]any{"exp": float64(now.Add(-2 * time.Minute).Unix())}, hs256(key)), "expired", jwtSettings{}},
		{"within leeway", key, jwtOf(t, hs, map[string]any{"exp": float64(now.Add(-30 * time.Second).Unix())}, hs256(key)), "", jwtSettings{}},
		{"no exp", key, jwtOf(t, hs, map[string]any{"iss": "shop"}, hs256(key)), "no exp", jwtSettings{}},
		{"not yet", key, jwtOf(t, hs, map[string]any{"exp": exp, "nbf": float64(now.Add(5 * time.Minute).Unix())}, hs256(key)), "not valid yet", jwtSettings{}},
		{"wrong issuer", key, jwtOf(t, hs, map[string]any{"exp": exp, "iss": "elsewhere", "aud": "taskiem"}, hs256(key)), "iss", settings},
		{"wrong audience", key, jwtOf(t, hs, map[string]any{"exp": exp, "iss": "shop", "aud": "other"}, hs256(key)), "aud", settings},
		{"not a jwt", key, "abc", "not a JWT", settings},
	} {
		err := checkJWT(c.secret, c.s, "Bearer "+c.token, now)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want) || !errors.Is(err, errCallRefused)):
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
	if err := checkJWT(key, jwtSettings{}, "", now); err == nil {
		t.Error("no header passed")
	}
	priv, _ := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err := checkJWT(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv})), jwtSettings{}, "Bearer "+jwtOf(t, map[string]any{"alg": "RS256"}, good, rs), now); err == nil || !strings.Contains(err.Error(), "never a private key") {
		t.Errorf("a private key as the secret: %v", err)
	}
}

func TestCheckBasic(t *testing.T) {
	basic := func(s string) string { return "Basic " + base64.StdEncoding.EncodeToString([]byte(s)) }
	for _, c := range []struct {
		secret, header string
		ok             bool
	}{
		{"shop:pw", basic("shop:pw"), true},
		{"shop:pw", basic("shop:other"), false},
		{"shop:pw", basic("shop:pw2"), false},
		{"shop:pw", "Bearer shop:pw", false},
		{"shop:pw", "Basic !!!", false},
		{"nocolon", basic("nocolon"), false},
	} {
		if err := checkBasic(c.secret, c.header); (err == nil) != c.ok {
			t.Errorf("%q with %q: %v", c.secret, c.header, err)
		}
	}
}

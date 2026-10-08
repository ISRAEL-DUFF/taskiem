package oidc

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/oidc/oidctest"
)

func signIn(t *testing.T, idp *oidctest.Provider, p *Provider) (Claims, error) {
	t.Helper()
	ctx := context.Background()
	state, nonce, verifier := NewNonce(), NewNonce(), NewVerifier()
	u, err := p.AuthURL(ctx, "https://app.test/cb", state, nonce, verifier, []string{"openid", "email", "groups"})
	if err != nil {
		t.Fatal(err)
	}
	c := idp.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	back, _ := url.Parse(resp.Header.Get("Location"))
	if back.Query().Get("state") != state {
		t.Fatal("state lost")
	}
	raw, err := p.Exchange(ctx, back.Query().Get("code"), "https://app.test/cb", verifier)
	if err != nil {
		return Claims{}, err
	}
	return p.Verify(ctx, raw, nonce, "groups", time.Now())
}

func TestCodeFlow(t *testing.T) {
	idp := oidctest.New()
	defer idp.Close()
	idp.Claims = map[string]any{"sub": "u1", "email": "ada@bank.test", "email_verified": true, "name": "Ada", "groups": []any{"ops", "treasury"}}
	p := &Provider{Issuer: idp.URL, ClientID: idp.ClientID, ClientSecret: idp.ClientSecret, HTTP: idp.Client()}
	c, err := signIn(t, idp, p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Email != "ada@bank.test" || !c.EmailVerified || len(c.Groups) != 2 || c.Subject != "u1" {
		t.Errorf("claims %+v", c)
	}
	// A wrong client secret is refused by the provider.
	bad := &Provider{Issuer: idp.URL, ClientID: idp.ClientID, ClientSecret: "nope", HTTP: idp.Client()}
	if _, err := signIn(t, idp, bad); err == nil {
		t.Error("wrong secret accepted")
	}
}

func TestVerifyRefusals(t *testing.T) {
	idp := oidctest.New()
	defer idp.Close()
	p := &Provider{Issuer: idp.URL, ClientID: idp.ClientID, HTTP: idp.Client()}
	now := time.Now()
	base := func() map[string]any {
		return map[string]any{"iss": idp.URL, "aud": idp.ClientID, "sub": "u1", "exp": now.Add(time.Minute).Unix(), "iat": now.Unix(), "nonce": "n1"}
	}
	ctx := context.Background()
	if _, err := p.Verify(ctx, idp.Sign(base()), "n1", "", now); err != nil {
		t.Fatalf("good token: %v", err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"other issuer":           func(c map[string]any) { c["iss"] = "https://evil.test" },
		"other audience":         func(c map[string]any) { c["aud"] = "someone-else" },
		"expired":                func(c map[string]any) { c["exp"] = now.Add(-10 * time.Minute).Unix() },
		"wrong nonce":            func(c map[string]any) { c["nonce"] = "n2" },
		"no nonce":               func(c map[string]any) { delete(c, "nonce") },
		"from the future":        func(c map[string]any) { c["iat"] = now.Add(time.Hour).Unix() },
		"multi-audience, no azp": func(c map[string]any) { c["aud"] = []any{idp.ClientID, "other"} },
	} {
		c := base()
		mutate(c)
		if _, err := p.Verify(ctx, idp.Sign(c), "n1", "", now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// alg "none" and a tampered payload.
	tok := idp.Sign(base())
	parts := strings.Split(tok, ".")
	none := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"k1"}`)) + "." + parts[1] + "."
	if _, err := p.Verify(ctx, none, "n1", "", now); !errors.Is(err, ErrInvalid) {
		t.Errorf("alg none: %v", err)
	}
	evil := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + idp.URL + `","aud":"taskiem","sub":"admin","exp":9999999999,"iat":` + itoa(now.Unix()) + `,"nonce":"n1"}`))
	if _, err := p.Verify(ctx, parts[0]+"."+evil+"."+parts[2], "n1", "", now); !errors.Is(err, ErrInvalid) {
		t.Errorf("tampered claims: %v", err)
	}
	// HS256 signed with the public key (algorithm confusion).
	hs := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","kid":"k1"}`)) + "." + parts[1] + "." + parts[2]
	if _, err := p.Verify(ctx, hs, "n1", "", now); !errors.Is(err, ErrInvalid) {
		t.Errorf("HS256: %v", err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

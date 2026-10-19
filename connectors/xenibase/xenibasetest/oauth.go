package xenibasetest

// OAuth for third-party apps (Xenibase docs/oauth-apps.md, api/openapi.yaml
// /api/v1/oauth/token and /revoke, read 17 October 2026): the exchange of
// an authorization code for a token, with PKCE S256 and the client's
// secret, and RFC 7009 revocation. There is no consent page here: a test
// registers the client, adds a code with what the person "approved"
// (organisation, scopes, projects, expiry) and the challenge it was asked
// for, and drives Taskiem's callback with it. A code works once; a second
// exchange fails with invalid_grant and revokes the token it issued.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// OAuthClient is an app a platform admin registered.
type OAuthClient struct {
	ID, Secret  string
	RedirectURI string
}

// OAuthCode is an authorization code the consent page issued.
type OAuthCode struct {
	Code       string
	Challenge  string   // the S256 code_challenge the authorize request carried
	Scopes     []string // as narrowed by the person
	ProjectIDs []string // nil: every project
	OrgID      string   // default: the fake's organisation
	ExpiresIn  int      // seconds; default 90 days
	used       bool
	token      string // the token the first exchange issued
}

// RegisterOAuthClient registers the app the exchange authenticates.
func (f *Fake) RegisterOAuthClient(c OAuthClient) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.oauthClient = &c
}

// AddOAuthCode records a code the consent page would have issued.
func (f *Fake) AddOAuthCode(c OAuthCode) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c.OrgID == "" {
		c.OrgID = f.OrgID
	}
	if c.ExpiresIn == 0 {
		c.ExpiresIn = 90 * 24 * 60 * 60
	}
	if f.oauthCodes == nil {
		f.oauthCodes = map[string]*OAuthCode{}
	}
	f.oauthCodes[c.Code] = &c
}

// RevokeToken revokes a token from Xenibase's side (Account → Connected
// apps → Disconnect): it answers 401 invalid_token from then on.
func (f *Fake) RevokeToken(secret string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.tokens, secret)
}

// HasToken reports whether a token is still accepted.
func (f *Fake) HasToken(secret string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.tokens[secret]
	return ok
}

type oauthErr struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

// oauthParams reads a form-encoded or JSON body into a flat map.
func oauthParams(r *http.Request) map[string]string {
	out := map[string]string{}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		for k, v := range m {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
		return out
	}
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	_ = r.ParseForm()
	for k := range r.PostForm {
		out[k] = r.PostForm.Get(k)
	}
	return out
}

// clientAuth returns the client credentials from the body or HTTP Basic.
func clientAuth(r *http.Request, p map[string]string) (id, secret string) {
	if u, pw, ok := r.BasicAuth(); ok {
		return u, pw
	}
	return p["client_id"], p["client_secret"]
}

// serveOAuth handles /api/v1/oauth/token and /api/v1/oauth/revoke, which
// take no bearer token; false when the path is not one of them.
func (f *Fake) serveOAuth(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/api/v1/oauth/token":
		f.serveOAuthToken(w, r)
	case "/api/v1/oauth/revoke":
		f.serveOAuthRevoke(w, r)
	default:
		return false
	}
	return true
}

func (f *Fake) serveOAuthToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		reply(w, http.StatusMethodNotAllowed, oauthErr{"invalid_request", "POST only"})
		return
	}
	p := oauthParams(r)
	id, secret := clientAuth(r, p)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.oauthClient == nil || id != f.oauthClient.ID || secret != f.oauthClient.Secret {
		reply(w, http.StatusUnauthorized, oauthErr{"invalid_client", "unknown client or wrong secret"})
		return
	}
	if p["grant_type"] != "authorization_code" {
		reply(w, http.StatusBadRequest, oauthErr{"unsupported_grant_type", "only authorization_code"})
		return
	}
	if p["code_verifier"] == "" || len(p["code_verifier"]) < 43 {
		reply(w, http.StatusBadRequest, oauthErr{"invalid_request", "code_verifier is missing or short"})
		return
	}
	c := f.oauthCodes[p["code"]]
	if c == nil {
		reply(w, http.StatusBadRequest, oauthErr{"invalid_grant", "unknown code"})
		return
	}
	if c.used {
		// RFC 6749 section 4.1.2: a replayed code revokes what it issued.
		delete(f.tokens, c.token)
		reply(w, http.StatusBadRequest, oauthErr{"invalid_grant", "the code was already used; its token is revoked"})
		return
	}
	sum := sha256.Sum256([]byte(p["code_verifier"]))
	switch {
	case p["redirect_uri"] != f.oauthClient.RedirectURI:
		reply(w, http.StatusBadRequest, oauthErr{"invalid_grant", "redirect_uri differs"})
		return
	case base64.RawURLEncoding.EncodeToString(sum[:]) != c.Challenge:
		reply(w, http.StatusBadRequest, oauthErr{"invalid_grant", "code_verifier does not match"})
		return
	}
	c.used = true
	c.token = "xb_" + newID()
	tok := Token{Secret: c.token, ID: "tok_" + newID(), Name: f.oauthClient.ID, OrgID: c.OrgID, Scopes: c.Scopes, ProjectIDs: c.ProjectIDs,
		ExpiresAt: f.Now().Add(time.Duration(c.ExpiresIn) * time.Second)}
	f.tokens[tok.Secret] = tok
	var projects any
	if c.ProjectIDs != nil {
		projects = c.ProjectIDs
	}
	reply(w, http.StatusOK, map[string]any{"access_token": tok.Secret, "token_type": "Bearer", "expires_in": c.ExpiresIn,
		"scope": strings.Join(c.Scopes, " "), "org_id": c.OrgID, "project_ids": projects, "token_id": tok.ID})
}

func (f *Fake) serveOAuthRevoke(w http.ResponseWriter, r *http.Request) {
	p := oauthParams(r)
	id, secret := clientAuth(r, p)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.oauthClient == nil || id != f.oauthClient.ID || secret != f.oauthClient.Secret {
		reply(w, http.StatusUnauthorized, oauthErr{"invalid_client", "unknown client or wrong secret"})
		return
	}
	delete(f.tokens, p["token"]) // 200 whether or not it was still valid
	f.Revoked = append(f.Revoked, p["token"])
	reply(w, http.StatusOK, nil)
}

// Package zoho is what every Zoho connector shares (Books, Mail, CRM):
// the connection's tokens, refreshed at the account's own accounts server
// and kept with its credentials, and the account's data centre, held to
// Zoho's hosts (connector/v1 rule 19). One Zoho client serves them all
// (decision 0051).
//
// Zoho issues at most ten access tokens every ten minutes per refresh
// token, so the one a connector gets is kept with the credentials and
// refreshed under the connection's lock (connector.Request.RotateCredentials).
// Zoho's refresh tokens do not rotate.
package zoho

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

// Client is one Zoho connector's side: its manifest (for the hosts it
// allows), its name in messages, and the platform's overrides.
type Client struct {
	Manifest *connector.Manifest
	// Name is the product in messages ("Zoho Books").
	Name string
	// AccountsServer replaces each connection's own (tests, a proxy).
	AccountsServer string
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Fatalf is a fatal error in the product's words.
func (c *Client) Fatalf(format string, a ...any) error {
	return fmt.Errorf(strings.ToLower(c.Name)+": "+format+": %w", append(a, effects.ErrFatal)...)
}

// Session is one action's use of a connection.
type Session struct {
	c     *Client
	Req   connector.Request
	Creds map[string]string
}

// Open starts a session on a request's connection.
func (c *Client) Open(req connector.Request) *Session {
	return &Session{c: c, Req: req, Creds: req.Credentials}
}

// Server is a connection field naming one of the account's servers
// (api_domain, accounts_server), checked to be Zoho's and https.
func (s *Session) Server(field string) (string, error) {
	d := strings.TrimRight(strings.TrimSpace(s.Creds[field]), "/")
	u, err := url.Parse(d)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || !s.c.Manifest.ConnectionHostAllowed(u.Hostname()) {
		return "", s.c.Fatalf("the connection's %s %q is not Zoho's: connect %s again", strings.ReplaceAll(field, "_", " "), d, s.c.Name)
	}
	return d, nil
}

func (s *Session) accountsServer() (string, error) {
	if s.c.AccountsServer != "" {
		return strings.TrimRight(s.c.AccountsServer, "/"), nil
	}
	return s.Server("accounts_server")
}

func (c *Client) fresh(creds map[string]string, stale string) bool {
	tok := creds["access_token"]
	if tok == "" || tok == stale {
		return false
	}
	exp, err := time.Parse(time.RFC3339, creds["access_expires_at"])
	return err == nil && c.now().Add(time.Minute).Before(exp)
}

// Rotate stores what fn makes of the connection's credentials, once across
// workers. With credentials that are not a stored connection's it is
// refused, rather than spend an access token from Zoho's small budget
// unseen.
func (s *Session) Rotate(ctx context.Context, fn connector.RotateFunc) error {
	creds, err := s.Req.RotateCredentials(ctx, fn)
	if errors.Is(err, connector.ErrNoRotate) {
		return s.c.Fatalf("%s tokens are refreshed and kept by Taskiem: connect %s with the Connect button rather than pasting its details", s.c.Name, s.c.Name)
	}
	if err != nil {
		return err
	}
	s.Creds = creds
	return nil
}

// Token is an access token to send: the kept one while it lasts, or a new
// one. stale is one the API just refused.
func (s *Session) Token(ctx context.Context, stale string) (string, error) {
	if s.c.fresh(s.Creds, stale) {
		return s.Creds["access_token"], nil
	}
	err := s.Rotate(ctx, func(cur map[string]string) (map[string]string, error) {
		if s.c.fresh(cur, stale) {
			return cur, nil // another worker refreshed it
		}
		return s.refresh(ctx, cur)
	})
	return s.Creds["access_token"], err
}

// refresh gets a new access token at the account's accounts server.
func (s *Session) refresh(ctx context.Context, cur map[string]string) (map[string]string, error) {
	name := strings.ToLower(s.c.Name)
	id, secret, rt := cur["client_id"], cur["client_secret"], cur["refresh_token"]
	if id == "" || secret == "" || rt == "" {
		return nil, fmt.Errorf("%s: the connection is missing its client or refresh token: connect %s again: %w: %w", name, s.c.Name, connector.ErrCredentialsRejected, effects.ErrFatal)
	}
	server, err := s.accountsServer()
	if err != nil {
		return nil, err
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {id}, "client_secret": {secret}}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/oauth/v2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %w", name, err, effects.ErrFatal)
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/json")
	resp, err := s.Req.HTTP.Do(r)
	if err != nil {
		return nil, fmt.Errorf("%s: refreshing the token: %w: %w", name, connector.RedactURLError(err), effects.ErrRetryable)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%s: reading the token answer: %w: %w", name, err, effects.ErrRetryable)
	}
	var t struct {
		AccessToken string  `json:"access_token"`
		ExpiresIn   float64 `json:"expires_in"`
		Error       string  `json:"error"`
	}
	_ = json.Unmarshal(body, &t)
	switch {
	case resp.StatusCode == http.StatusOK && t.AccessToken != "":
	case t.Error == "invalid_code" || t.Error == "invalid_token":
		// Zoho answers a revoked or unknown refresh token with 200 and
		// {"error": "invalid_code"}.
		return nil, fmt.Errorf("%s: Zoho refused the refresh token (%s): connect %s again: %w: %w", name, t.Error, s.c.Name, connector.ErrCredentialsRejected, effects.ErrFatal)
	case t.Error == "Access Denied" || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		// Too many access tokens asked for in ten minutes, or Zoho's side.
		return nil, fmt.Errorf("%s: refreshing the token: http %d %s: %w", name, resp.StatusCode, t.Error, effects.ErrRetryable)
	default:
		// invalid_client and the like: Taskiem's Zoho client, not the
		// business's connection.
		return nil, s.c.Fatalf("Zoho refused the token refresh: http %d %s", resp.StatusCode, t.Error)
	}
	out := make(map[string]string, len(cur)+2)
	for k, v := range cur {
		out[k] = v
	}
	if t.ExpiresIn <= 0 {
		t.ExpiresIn = 3600
	}
	out["access_token"] = t.AccessToken
	out["access_expires_at"] = s.c.now().UTC().Add(time.Duration(t.ExpiresIn) * time.Second).Format(time.RFC3339)
	return out, nil
}

// Keep stores fields a connector chose for the connection (Books'
// organisation), unless another worker already did.
func (s *Session) Keep(ctx context.Context, fields map[string]string) error {
	return s.Rotate(ctx, func(cur map[string]string) (map[string]string, error) {
		out := make(map[string]string, len(cur)+len(fields))
		for k, v := range cur {
			out[k] = v
		}
		for k, v := range fields {
			if out[k] == "" {
				out[k] = v
			}
		}
		return out, nil
	})
}

// Do sends a JSON request with the access token, refreshing it once when
// Zoho refuses it; the error is the HTTPError for the caller to classify.
func (s *Session) Do(ctx context.Context, method, u string, body, out any) error {
	tok, err := s.Token(ctx, "")
	if err != nil {
		return err
	}
	for try := 0; ; try++ {
		err = connector.DoJSON(ctx, s.Req.HTTP, method, u, map[string]string{"Authorization": "Zoho-oauthtoken " + tok}, body, out)
		var he *connector.HTTPError
		if try == 0 && errors.As(err, &he) && he.Status == http.StatusUnauthorized {
			if tok, err = s.Token(ctx, tok); err != nil {
				return err
			}
			continue
		}
		return err
	}
}

// ID is a Zoho id, which the APIs give as a string or, in some answers, a
// number.
type ID string

func (z *ID) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*z = ID(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*z = ID(n.String())
	return nil
}

// Time is a time as Zoho's filters take it (2026-10-26T10:00:00+0000).
func Time(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05-0700") }

// ParseTime reads Zoho's times (2026-10-26T10:00:00+0100, or RFC 3339).
func ParseTime(s string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02T15:04:05-0700", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

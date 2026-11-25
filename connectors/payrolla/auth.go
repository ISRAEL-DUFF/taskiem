package payrolla

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

// Connecting in one step (Payrolla's Phase C, decision 0074; P4b-1 of
// docs/spec/chat-pay-people.md). Taskiem's OAuth machinery
// (api/oauthconnections.go, decision 0034) sends the owner to Payrolla's
// consent page with the state and a PKCE S256 challenge, exchanges the
// code at ${server}/oauth/token (the client in HTTP Basic) and revokes at
// ${server}/oauth/revoke, as the manifest's auth.oauth2 says. What is
// Payrolla's own is here: reading the connection's id once it is made
// (link), and refreshing.
//
// Refreshing. Access tokens last an hour. Each refresh token works once
// and comes back replaced; an old one used again ends the whole token
// chain. So a refresh runs only under the connection's lock
// (connector.Request.RotateCredentials): the worker that holds it spends
// the refresh token, and what Payrolla hands back is stored before the
// lock goes and before the new access token is used. A worker that
// waited finds the new access token and spends nothing. A refused refresh
// (invalid_grant) wraps connector.ErrCredentialsRejected and
// ErrConnectionEnded: the connection is marked expired and its owner
// reconnects.

// ErrConnectionEnded is a connection Payrolla no longer honours: a
// refresh token refused (revoked, expired, or spent twice), a key
// revoked, or company.disconnected. The owner connects again.
var ErrConnectionEnded = errors.New("payrolla: the connection has ended: connect Payrolla again")

// accessTTL is how long a new access token is used for when Payrolla does
// not say: its documented hour, less a margin.
const accessTTL = 55 * time.Minute

// session is one call's credentials, refreshed in place.
type session struct {
	c     *client
	req   connector.Request
	creds map[string]string
}

// oauth says whether the connection was made in one step (it holds
// Taskiem's tokens, not a key).
func (s *session) oauth() bool { return strings.TrimSpace(s.creds["api_key"]) == "" }

// fresh says whether creds hold an access token good for another minute,
// other than stale (one Payrolla just refused).
func (c *client) fresh(creds map[string]string, stale string) bool {
	tok := creds["access_token"]
	if tok == "" || tok == stale {
		return false
	}
	exp, err := time.Parse(time.RFC3339, creds["access_expires_at"])
	return err == nil && c.now().Add(time.Minute).Before(exp)
}

// bearer is what to send as the bearer token: the key, or an access token
// (refreshed when it lapses, or when it is stale).
func (s *session) bearer(ctx context.Context, stale string) (string, error) {
	if key := strings.TrimSpace(s.creds["api_key"]); key != "" {
		return key, nil
	}
	if strings.TrimSpace(s.creds["refresh_token"]) == "" {
		return "", fmt.Errorf("payrolla: the connection has no key and no sign-in: connect Payrolla again: %w", effects.ErrFatal)
	}
	if s.c.fresh(s.creds, stale) {
		return s.creds["access_token"], nil
	}
	creds, err := s.req.RotateCredentials(ctx, func(cur map[string]string) (map[string]string, error) {
		if s.c.fresh(cur, stale) {
			return cur, nil // another worker refreshed it
		}
		return s.c.refresh(ctx, s.req.HTTP, cur)
	})
	if errors.Is(err, connector.ErrNoRotate) {
		return "", fmt.Errorf("payrolla: a connection made with Connect Payrolla is refreshed and kept by Taskiem; it cannot be used here: %w", effects.ErrFatal)
	}
	if err != nil {
		return "", err
	}
	s.creds = creds
	return creds["access_token"], nil
}

// tokenAnswer is Payrolla's /oauth/token answer (RFC 6749, not wrapped).
type tokenAnswer struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresIn    float64 `json:"expires_in"`
	Scope        string  `json:"scope"`
	CompanyID    string  `json:"company_id"`
	CompanyName  string  `json:"company_name"`
	Error        string  `json:"error"`
	Description  string  `json:"error_description"`
}

// refresh spends cur's refresh token (grant_type refresh_token, Taskiem's
// client in HTTP Basic) and returns cur with what Payrolla handed back:
// the new refresh token, the new access token and when it lapses.
func (c *client) refresh(ctx context.Context, hc *http.Client, cur map[string]string) (map[string]string, error) {
	id, secret, rt := cur["client_id"], cur["client_secret"], cur["refresh_token"]
	if id == "" || secret == "" || rt == "" {
		return nil, fmt.Errorf("payrolla: the connection is missing its client or refresh token: %w: %w: %w", ErrConnectionEnded, connector.ErrCredentialsRejected, effects.ErrFatal)
	}
	base, err := c.m.BaseFor(cur)
	if err != nil {
		return nil, err
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("payrolla: %w: %w", err, effects.ErrFatal)
	}
	r.SetBasicAuth(id, secret)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/json")
	resp, err := hc.Do(r)
	if err != nil {
		// Nothing was stored: the refresh token held is still the latest
		// Taskiem saw. Whether Payrolla spent it is unknown; if it did,
		// the next refresh is refused and the owner reconnects.
		return nil, fmt.Errorf("payrolla: refreshing the token: %w: %w", connector.RedactURLError(err), effects.ErrRetryable)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("payrolla: reading the token answer: %w: %w", err, effects.ErrRetryable)
	}
	var t tokenAnswer
	_ = json.Unmarshal(body, &t)
	switch {
	case resp.StatusCode == http.StatusOK && t.AccessToken != "" && t.RefreshToken != "":
	case resp.StatusCode == http.StatusBadRequest && t.Error == "invalid_grant":
		return nil, fmt.Errorf("payrolla: Payrolla refused the refresh token (%s): %w: %w: %w", strings.TrimSpace(t.Error+" "+t.Description), ErrConnectionEnded, connector.ErrCredentialsRejected, effects.ErrFatal)
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, fmt.Errorf("payrolla: refreshing the token: http %d: %w", resp.StatusCode, effects.ErrRetryable)
	default:
		// invalid_client and the like: Taskiem's registration, not the
		// business's connection.
		return nil, fmt.Errorf("payrolla: Payrolla refused the token refresh: http %d %s: %w", resp.StatusCode, strings.TrimSpace(t.Error+" "+t.Description), effects.ErrFatal)
	}
	next := make(map[string]string, len(cur)+2)
	for k, v := range cur {
		next[k] = v
	}
	next["refresh_token"] = t.RefreshToken
	next["access_token"] = t.AccessToken
	next["access_expires_at"] = c.expiry(t.ExpiresIn)
	if t.Scope != "" {
		next["scopes"] = t.Scope
	}
	return next, nil
}

// expiry is when an access token given for seconds (0: unsaid) stops
// being used, a minute early.
func (c *client) expiry(seconds float64) string {
	ttl := accessTTL
	if seconds > 0 {
		ttl = time.Duration(seconds)*time.Second - time.Minute
	}
	return c.now().Add(ttl).UTC().Format(time.RFC3339)
}

// link completes a connection made in one step (connector.Connector.Link):
// it reads Payrolla's id for the connection with the new access token,
// so company.disconnected for an old key is told apart, and when the
// token lapses.
func (c *client) link(ctx context.Context, l connector.LinkCall) (map[string]string, error) {
	out := make(map[string]string, len(l.Credentials)+2)
	for k, v := range l.Credentials {
		out[k] = v
	}
	out["access_expires_at"] = c.expiry(0)
	var view struct {
		Company struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"company"`
		Key struct {
			ID     string   `json:"id"`
			Scopes []string `json:"scopes"`
		} `json:"key"`
	}
	req := connector.Request{Credentials: map[string]string{"api_key": l.Token, connector.ModeKey: l.Credentials[connector.ModeKey]}, HTTP: l.HTTP}
	if err := c.do(ctx, req, http.MethodGet, "/connect/company", nil, nil, &view); err != nil {
		return nil, err
	}
	out["connection_key_id"] = view.Key.ID
	if out["company_id"] == "" {
		out["company_id"] = view.Company.ID
	}
	if out["company_name"] == "" {
		out["company_name"] = view.Company.Name
	}
	if out["scopes"] == "" {
		out["scopes"] = strings.Join(view.Key.Scopes, " ")
	}
	return out, nil
}

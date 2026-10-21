package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/remote"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// Connections made by OAuth 2.0 authorization code grant with PKCE
// (decision 0034; docs/connector-sdk.md#connecting-by-oauth). A connector
// whose manifest declares auth.oauth2 can be linked instead of having a
// token pasted: start answers the provider's consent URL, the browser goes
// there, the provider sends it back to the callback with a code, and the
// API exchanges the code for a token and saves the connection. The state
// and the PKCE verifier never leave the server; the client secret never
// leaves the process (docs/operations.md: TASKIEM_OAUTH_<CONNECTOR>_*).

// OAuthClient is Taskiem's registration with one provider.
type OAuthClient struct {
	ID     string
	Secret string
}

// oauthCallbackPath is where providers send the browser back; the full
// URL, under PublicURL, is what they must have registered.
const oauthCallbackPath = "/v1/connections/oauth/callback"

// oauthBrowserCookie binds a linking request to the browser that started
// it: the callback is taken only with it, so a consent link sent to
// someone else cannot put their account into this organisation. Sent only
// to the callback; Lax, so it travels on the provider's redirect back.
const oauthBrowserCookie = "taskiem_oauth"

// errOAuthUnavailable: the connector cannot be linked on this platform
// (no client registered, no public URL, no provider server). 503 with
// code oauth_unavailable, and the reason.
var errOAuthUnavailable = errors.New("oauth unavailable")

// oauthProvider is a connector's OAuth endpoints, resolved for this
// platform, with Taskiem's client at it.
type oauthProvider struct {
	authorize, token, revoke string
	server                   string // the provider's server the URLs were resolved from
	client                   OAuthClient
}

// oauthFor says how connector c is linked here, or why it cannot be.
func (s *Server) oauthFor(c *connector.Connector) (oauthProvider, error) {
	o := c.Manifest.Auth.OAuth2
	if o == nil {
		return oauthProvider{}, fmt.Errorf("%w: %s does not connect by OAuth; add a connection with its credentials", errBadRequest, c.Manifest.Name)
	}
	client, ok := s.OAuthClients[c.Manifest.ID]
	if !ok || client.ID == "" {
		return oauthProvider{}, fmt.Errorf("%w: Taskiem is not registered as an OAuth client with %s on this platform (TASKIEM_OAUTH_%s_CLIENT_ID); add a connection with its credentials instead",
			errOAuthUnavailable, c.Manifest.Name, strings.ToUpper(c.Manifest.ID))
	}
	if s.PublicURL == "" {
		return oauthProvider{}, fmt.Errorf("%w: connecting by OAuth needs TASKIEM_PUBLIC_URL, which is not set", errOAuthUnavailable)
	}
	authorize, token, revoke, err := o.Resolve(c.Manifest.BaseURL)
	if err != nil {
		return oauthProvider{}, fmt.Errorf("%w: %s cannot be linked on this platform: %w", errOAuthUnavailable, c.Manifest.Name, err)
	}
	return oauthProvider{authorize: authorize, token: token, revoke: revoke, server: strings.TrimRight(c.Manifest.BaseURL, "/"), client: client}, nil
}

// oauthLinkable is listConnectors' answer to whether a connector can be
// linked here: nil for a connector without OAuth.
func (s *Server) oauthLinkable(c *connector.Connector) *bool {
	if c.Manifest.Auth.OAuth2 == nil {
		return nil
	}
	_, err := s.oauthFor(c)
	ok := err == nil
	return &ok
}

func (s *Server) oauthRedirectURI() string { return s.PublicURL + oauthCallbackPath }

// connectorByID finds a tenant's connector by its manifest id (what the
// connections table stores), as a connection names it.
func connectorByID(reg connector.Lookup, id string) (*connector.Connector, bool) {
	for _, c := range reg.List() {
		if c.Manifest.ID == id {
			return c, true
		}
	}
	return nil, false
}

type oauthStartReq struct {
	Connector   string `json:"connector"` // "xenibase@1"
	Environment string `json:"environment"`
	Name        string `json:"name"`
}

// startOAuthConnection records the request and answers the provider's
// consent URL with the state and the PKCE challenge (S256).
func (s *Server) startOAuthConnection(w http.ResponseWriter, r *http.Request) {
	var req oauthStartReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	env, err := envParam(r, req.Environment)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	reg, err := s.Registry.For(r.Context(), p.TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	c, ok := reg.Get(req.Connector)
	if !ok {
		s.fail(w, r, fmt.Errorf("%w: connector %q is not available", errBadRequest, req.Connector))
		return
	}
	prov, err := s.oauthFor(c)
	if err != nil {
		s.failOAuth(w, r, err)
		return
	}
	var state, browser, verifier string
	err = s.tx(r, func(tx pgx.Tx) error {
		if err := s.checkCount(r.Context(), tx, p.TenantID, "max_connections"); err != nil {
			return err
		}
		if req.Name == "" {
			var err error
			if req.Name, err = freeConnectionName(r.Context(), tx, env, c.Manifest.ID); err != nil {
				return err
			}
		}
		var exists bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM connections WHERE environment = $1 AND connector = $2 AND name = $3)`, env, c.Manifest.ID, req.Name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("%w: a %s connection named %q already exists in %s", errConflict, c.Manifest.ID, req.Name, env)
		}
		state, browser, verifier, err = s.Vault.BeginOAuthRequestTx(r.Context(), tx, p.TenantID, c.Ref(), env, req.Name, p.Actor())
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id":             {prov.client.ID},
		"redirect_uri":          {s.oauthRedirectURI()},
		"scope":                 {strings.Join(c.Manifest.Auth.OAuth2.Scopes, " ")},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
	for k, v := range c.Manifest.Auth.OAuth2.AuthorizeParams {
		q.Set(k, v)
	}
	http.SetCookie(w, &http.Cookie{Name: oauthBrowserCookie, Value: browser, Path: oauthCallbackPath, MaxAge: int(secrets.OAuthRequestTTL.Seconds()), //nolint:gosec // Secure is TASKIEM_SECURE_COOKIES (on by default), as for the session cookie
		HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, map[string]any{"url": prov.authorize + "?" + q.Encode(), "expires_in": int(secrets.OAuthRequestTTL.Seconds())})
}

// failOAuth answers a linking refusal: 503 oauth_unavailable for a
// platform that cannot link this connector, otherwise as fail does.
func (s *Server) failOAuth(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errOAuthUnavailable) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": strings.TrimPrefix(err.Error(), "oauth unavailable: "), "code": "oauth_unavailable"})
		return
	}
	s.fail(w, r, err)
}

// oauthConnectionCallback is where the provider sends the browser back.
// It is unauthenticated: the state and the browser's binding cookie name
// the request, which works once and lasts ten minutes. Whatever happens,
// the browser ends up on the Connections page, with the new connection or
// with what went wrong.
func (s *Server) oauthConnectionCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ctx := r.Context()
	cookie, _ := r.Cookie(oauthBrowserCookie)
	http.SetCookie(w, &http.Cookie{Name: oauthBrowserCookie, Value: "", Path: oauthCallbackPath, MaxAge: -1, HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode}) //nolint:gosec // as when it is set
	if cookie == nil {
		s.oauthDone(w, r, "", fmt.Errorf("%w: finish linking in the browser where you started it (start again from Connections)", errOAuthGrant))
		return
	}
	req, err := s.Vault.TakeOAuthRequest(ctx, q.Get("state"), cookie.Value)
	if err != nil {
		s.oauthDone(w, r, "", err)
		return
	}
	reg, err := s.Registry.For(ctx, req.Tenant.String())
	if err != nil {
		s.oauthDone(w, r, "", err)
		return
	}
	c, ok := reg.Get(req.Connector)
	if !ok {
		s.oauthDone(w, r, "", fmt.Errorf("%w: connector %s is no longer available", errBadRequest, req.Connector))
		return
	}
	if e := q.Get("error"); e != "" {
		if e == "access_denied" {
			s.oauthDone(w, r, "", fmt.Errorf("%w: you did not approve the connection in %s", errOAuthGrant, c.Manifest.Name))
		} else {
			s.oauthDone(w, r, "", fmt.Errorf("%w: %s refused the request (%s: %s)", errOAuthProvider, c.Manifest.Name, e, q.Get("error_description")))
		}
		return
	}
	code := q.Get("code")
	if code == "" {
		s.oauthDone(w, r, "", fmt.Errorf("%w: %s sent the browser back without a code", errOAuthProvider, c.Manifest.Name))
		return
	}
	prov, err := s.oauthFor(c)
	if err != nil {
		s.oauthDone(w, r, "", err)
		return
	}
	var verifier string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{req.Tenant}, func(tx pgx.Tx) error {
		verifier, err = s.Vault.OAuthVerifierTx(ctx, tx, req)
		return err
	}); err != nil {
		s.oauthDone(w, r, "", err)
		return
	}
	answer, err := s.oauthExchange(ctx, req.Tenant, c, prov, code, verifier)
	if err != nil {
		s.oauthDone(w, r, "", err)
		return
	}
	// A token that makes no connection is given back to the provider.
	token := asString(answer["access_token"])
	creds, expiresAt, err := oauthCredentials(c.Manifest, answer, prov.server)
	applyPlatformClient(c.Manifest, creds, prov.client)
	if err != nil {
		s.oauthRevoke(ctx, req.Tenant, c, prov, token)
		s.oauthDone(w, r, "", err)
		return
	}
	if err := checkCredentials(c, creds); err != nil {
		s.oauthRevoke(ctx, req.Tenant, c, prov, token)
		s.oauthDone(w, r, "", err)
		return
	}
	var id uuid.UUID
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{req.Tenant}, func(tx pgx.Tx) error {
		var err error
		if id, err = s.Vault.CreateOAuthConnectionTx(ctx, tx, req.Tenant, req.Environment, c.Manifest.ID, req.Name, creds, expiresAt, req.Person); err != nil {
			return err
		}
		return secrets.SetRouteKeysTx(ctx, tx, req.Tenant, id, c.Manifest.RouteKeys(creds))
	})
	if err != nil {
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
			err = fmt.Errorf("%w: a %s connection named %q already exists in %s", errConflict, c.Manifest.ID, req.Name, req.Environment)
		}
		s.oauthRevoke(ctx, req.Tenant, c, prov, token)
		s.oauthDone(w, r, "", err)
		return
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{req.Tenant}, func(tx pgx.Tx) error { return remote.Retry(ctx, tx, req.Environment, c.Ref()) }); err != nil {
		s.Logger.Warn("remote subscriptions: retrying after a new connection", "err", err)
	}
	s.oauthDone(w, r, id.String(), nil)
}

// oauthDone sends the browser to the Connections page: ?linked=<id>, or
// ?error=<what went wrong>, in words meant for the person.
func (s *Server) oauthDone(w http.ResponseWriter, r *http.Request, linked string, err error) {
	q := url.Values{}
	if err != nil {
		s.Logger.Warn("oauth connection refused", "err", err, "ip", clientIP(r))
		// Refusals are shown as they are; anything else (the database,
		// the vault) is not the person's to read.
		msg := "the connection could not be saved; try again"
		for _, known := range []error{secrets.ErrOAuthRequest, errBadRequest, errConflict, errOAuthUnavailable, errOAuthProvider, errOAuthGrant} {
			if errors.Is(err, known) {
				msg = err.Error()
			}
		}
		for _, p := range []string{"bad request: ", "conflict: ", "oauth unavailable: ", "provider: ", "grant: "} {
			msg = strings.TrimPrefix(msg, p)
		}
		q.Set("error", strings.ToUpper(msg[:1])+msg[1:])
	} else {
		q.Set("linked", linked)
	}
	http.Redirect(w, r, s.PublicURL+"/connections?"+q.Encode(), http.StatusFound)
}

var (
	// errOAuthProvider: the provider did not answer, or not as published.
	errOAuthProvider = errors.New("provider")
	// errOAuthGrant: what the provider granted does not make a connection.
	errOAuthGrant = errors.New("grant")
)

func (s *Server) egressGuard() *egress.Guard {
	if s.Egress != nil {
		return s.Egress
	}
	return &egress.Guard{Logger: s.Logger}
}

// oauthHTTP is an egress-guarded client for one provider URL.
func (s *Server) oauthHTTP(tenant uuid.UUID, c *connector.Connector, target string) (*http.Client, error) {
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("%w: %s's OAuth URL %q is not a URL", errOAuthProvider, c.Manifest.Name, target)
	}
	return s.egressGuard().Client(egress.Policy{Tenant: tenant.String(), Hosts: []string{u.Hostname()}, Purpose: "oauth:" + c.Manifest.ID}, 30*time.Second), nil
}

// oauthExchange trades the code for a token at the provider (RFC 6749
// section 4.1.3, form-encoded, the client secret in the body) and returns
// the answer's fields.
func (s *Server) oauthExchange(ctx context.Context, tenant uuid.UUID, c *connector.Connector, prov oauthProvider, code, verifier string) (map[string]any, error) {
	hc, err := s.oauthHTTP(tenant, c, prov.token)
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {s.oauthRedirectURI()},
		"client_id":     {prov.client.ID},
		"client_secret": {prov.client.Secret},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, prov.token, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s did not answer the token exchange: %w", errOAuthProvider, c.Manifest.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: reading %s's token answer: %w", errOAuthProvider, c.Manifest.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("http %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("%w: %s refused the token exchange: %s", errOAuthProvider, c.Manifest.Name, strings.TrimSpace(e.Error+" "+e.Description))
	}
	var answer map[string]any
	if err := json.Unmarshal(body, &answer); err != nil || answer["access_token"] == nil {
		return nil, fmt.Errorf("%w: %s's token answer is not the published shape", errOAuthProvider, c.Manifest.Name)
	}
	return answer, nil
}

// oauthCredentials builds a connection's fields from the token answer as
// the manifest's fields_from_token says, and reads expires_in.
func oauthCredentials(m *connector.Manifest, answer map[string]any, server string) (map[string]string, *time.Time, error) {
	creds := map[string]string{}
	for field, from := range m.Auth.OAuth2.FieldsFromToken {
		if from == connector.ServerPlaceholder {
			creds[field] = server
			continue
		}
		name, one := connector.TokenField(from)
		v := answer[name]
		if !one {
			if s := asString(v); s != "" {
				creds[field] = s
			}
			continue
		}
		list, _ := v.([]any)
		what := strings.TrimSuffix(name, "_ids")
		switch {
		case v == nil || len(list) == 0:
			return nil, nil, fmt.Errorf("%w: %s granted access to every %s; a connection is one %s: connect again and choose exactly one", errOAuthGrant, m.Name, what, what)
		case len(list) > 1:
			return nil, nil, fmt.Errorf("%w: %s granted %d %ss; a connection is one %s: connect again and choose exactly one", errOAuthGrant, m.Name, len(list), what, what)
		}
		creds[field] = asString(list[0])
	}
	// A connection that keeps a refresh token does not expire with its
	// access token: the connector refreshes it.
	keepsRefresh := slices.Contains(slices.Collect(maps.Values(m.Auth.OAuth2.FieldsFromToken)), "refresh_token")
	var expiresAt *time.Time
	if n, ok := answer["expires_in"].(float64); ok && n > 0 && !keepsRefresh {
		t := time.Now().Add(time.Duration(n) * time.Second).UTC()
		expiresAt = &t
	}
	return creds, expiresAt, nil
}

// applyPlatformClient fills the fields a connector that refreshes its own
// tokens takes from the client they were granted to: Taskiem's.
func applyPlatformClient(m *connector.Manifest, creds map[string]string, client OAuthClient) {
	if creds == nil || m.Auth.OAuth2 == nil {
		return
	}
	for field, which := range m.Auth.OAuth2.PlatformClientFields {
		switch which {
		case "id":
			creds[field] = client.ID
		case "secret":
			creds[field] = client.Secret
		}
	}
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strings.TrimSuffix(fmt.Sprintf("%f", t), ".000000")
	case bool:
		return fmt.Sprint(t)
	}
	return ""
}

// oauthToken is the token to give back among a connection's credentials:
// the field the manifest fills from refresh_token (revoking it ends the
// grant), else from access_token.
func oauthToken(c *connector.Connector, creds map[string]string) string {
	access := ""
	for field, from := range c.Manifest.Auth.OAuth2.FieldsFromToken {
		switch from {
		case "refresh_token":
			return creds[field]
		case "access_token":
			access = creds[field]
		}
	}
	return access
}

// oauthRevoke gives a token back to the provider (RFC 7009) when it will
// not be kept, or when its connection is deleted. Best effort: the
// provider answers 200 whether or not the token was still valid, and a
// failure is logged, not answered.
func (s *Server) oauthRevoke(ctx context.Context, tenant uuid.UUID, c *connector.Connector, prov oauthProvider, token string) {
	if prov.revoke == "" || token == "" {
		return
	}
	hc, err := s.oauthHTTP(tenant, c, prov.revoke)
	if err != nil {
		s.Logger.Warn("oauth: token not revoked", "connector", c.Manifest.ID, "err", err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, prov.revoke, strings.NewReader(url.Values{"token": {token}}.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(prov.client.ID, prov.client.Secret)
	resp, err := hc.Do(req)
	if err != nil {
		s.Logger.Warn("oauth: token not revoked", "connector", c.Manifest.ID, "err", err)
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.Logger.Warn("oauth: token not revoked", "connector", c.Manifest.ID, "status", resp.StatusCode)
	}
}

// --- adding fields, and removing a connection ---

// mergeConnectionCredentials adds or replaces some of a connection's
// fields, keeping the rest: the secret key and data API URL a linked
// Xenibase connection gets afterwards. An empty value clears a field. The
// result must still satisfy the connector's auth (every required field
// present, no unknown one).
func (s *Server) mergeConnectionCredentials(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: not a connection id", errBadRequest))
		return
	}
	var req struct {
		Credentials map[string]string `json:"credentials"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(req.Credentials) == 0 {
		s.fail(w, r, fmt.Errorf("%w: name at least one credential field", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	reg, err := s.Registry.For(r.Context(), p.TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := secrets.WithUse(r.Context(), secrets.Use{Kind: secrets.KindConnection, Purpose: "connection.credentials_merge", Actor: p.Actor()})
	err = s.tx(r, func(tx pgx.Tx) error {
		var connID, env, status string
		var managed *string
		err := tx.QueryRow(ctx, `SELECT connector, environment, status, provisioned_by FROM connections WHERE id = $1`, id).Scan(&connID, &env, &status, &managed)
		if errors.Is(err, pgx.ErrNoRows) {
			return secrets.ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := envParam(r, env); err != nil {
			return err
		}
		if managed != nil {
			return fmt.Errorf("%w: this connection is kept by Taskiem (%s); its credentials cannot be changed", errConflict, *managed)
		}
		if status == "expired" {
			return fmt.Errorf("%w: the connection has expired; connect again rather than adding to it", errConflict)
		}
		c, ok := connectorByID(reg, connID)
		if !ok {
			return fmt.Errorf("%w: connector %s is no longer available", errConflict, connID)
		}
		existing, err := s.Vault.ConnectionCredentialsTx(ctx, tx, p.TenantID, id)
		if err != nil {
			return err
		}
		merged := map[string]string{}
		for k, v := range existing {
			merged[k] = v
		}
		for k, v := range req.Credentials {
			if v == "" {
				delete(merged, k)
			} else {
				merged[k] = v
			}
		}
		if err := checkCredentials(c, merged); err != nil {
			return err
		}
		if err := s.Vault.ReplaceCredentialsTx(ctx, tx, p.TenantID, id, merged, p.Actor()); err != nil {
			return err
		}
		return secrets.SetRouteKeysTx(ctx, tx, p.TenantID, id, c.Manifest.RouteKeys(merged))
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// connectionUse is a deployed workflow whose steps name a connection.
type connectionUse struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Environment string    `json:"environment"`
}

// connectionUses lists the deployed workflows in the connection's
// environment with a step on its connector that names it, or names no
// connection (which the only connection of that connector serves).
func connectionUses(ctx context.Context, tx pgx.Tx, env, connectorID, name string) ([]connectionUse, error) {
	rows, err := tx.Query(ctx, `SELECT w.id, w.name, d.environment FROM deployments d
		JOIN workflows w ON w.id = d.workflow_id
		JOIN workflow_versions wv ON wv.workflow_id = d.workflow_id AND wv.version = d.version
		WHERE d.environment = $1 AND jsonb_path_exists(wv.definition, '$.** ? (@.connector starts with $c && (@.connection == $n || !exists(@.connection)))', jsonb_build_object('c', $2::text, 'n', $3::text))
		ORDER BY w.name`, env, connectorID+"@", name)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[connectionUse])
}

// deleteConnection removes a connection. One that a deployed workflow
// names is refused (409 in_use, with the workflows) unless ?force=true;
// one Taskiem keeps (data tables) is refused. An OAuth connection's token
// is also revoked at the provider, best effort, so it stops working there
// too (decision 0034).
func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: not a connection id", errBadRequest))
		return
	}
	force := r.URL.Query().Get("force") == "true"
	p := principalFrom(r.Context())
	reg, err := s.Registry.For(r.Context(), p.TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := secrets.WithUse(r.Context(), secrets.Use{Kind: secrets.KindConnection, Purpose: "connection.revoke", Actor: p.Actor()})
	var uses []connectionUse
	var revoke func()
	err = s.tx(r, func(tx pgx.Tx) error {
		var connID, name, env, authType string
		var managed *string
		err := tx.QueryRow(ctx, `SELECT connector, name, environment, auth_type, provisioned_by FROM connections WHERE id = $1`, id).Scan(&connID, &name, &env, &authType, &managed)
		if errors.Is(err, pgx.ErrNoRows) {
			return secrets.ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := envParam(r, env); err != nil {
			return err
		}
		if managed != nil {
			return fmt.Errorf("%w: this connection is kept by Taskiem (%s) and cannot be deleted", errConflict, *managed)
		}
		if uses, err = connectionUses(ctx, tx, env, connID, name); err != nil {
			return err
		}
		if len(uses) > 0 && !force {
			return errConnectionInUse
		}
		if authType == "oauth2" {
			if c, ok := connectorByID(reg, connID); ok && c.Manifest.Auth.OAuth2 != nil {
				if prov, err := s.oauthFor(c); err == nil && prov.revoke != "" {
					creds, err := s.Vault.ConnectionCredentialsTx(ctx, tx, p.TenantID, id)
					if err != nil {
						return err
					}
					tenant := p.TenantID
					revoke = func() { s.oauthRevoke(context.WithoutCancel(ctx), tenant, c, prov, oauthToken(c, creds)) }
				}
			}
		}
		return s.Vault.DeleteConnectionTx(ctx, tx, p.TenantID, id, p.Actor(), map[string]any{"force": force, "workflows": len(uses), "ip": clientIP(r)})
	})
	switch {
	case errors.Is(err, errConnectionInUse):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "deployed workflows use this connection; disconnect with force=true to delete it anyway", "code": "in_use", "workflows": uses})
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	if revoke != nil {
		revoke()
	}
	w.WriteHeader(http.StatusNoContent)
}

var errConnectionInUse = errors.New("connection in use")

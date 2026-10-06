package api

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/oidc"
	"github.com/israel-duff/taskiem/engine/saml"
)

// Single sign-on (spec 13.2) with a tenant's OIDC or SAML identity
// provider. A connection serves email domains the tenant has proven with a
// DNS TXT record, and signs in only people whose email is on them. It can
// create members on first sign-in (JIT) and keep their roles in step with
// their IdP groups; it manages only the roles it granted. With enforce on,
// the tenant's members on those domains cannot use passwords or passkeys.

type oidcConfig struct {
	Issuer      string   `json:"issuer"`
	ClientID    string   `json:"client_id"`
	AuthMethod  string   `json:"auth_method,omitempty"`
	Scopes      []string `json:"scopes,omitempty"`
	GroupsClaim string   `json:"groups_claim,omitempty"`
	Hosts       []string `json:"hosts"` // the provider's hosts, found by discovery when saved
}

type samlConfig struct {
	IdPEntityID string   `json:"idp_entity_id"`
	IdPSSOURL   string   `json:"idp_sso_url"`
	IdPCerts    []string `json:"idp_certs"` // base64 DER
	EmailAttr   string   `json:"email_attr,omitempty"`
	NameAttr    string   `json:"name_attr,omitempty"`
	GroupsAttr  string   `json:"groups_attr,omitempty"`
}

type ssoConnection struct {
	ID           uuid.UUID           `json:"id"`
	Protocol     string              `json:"protocol"`
	Name         string              `json:"name"`
	Config       json.RawMessage     `json:"config"`
	DefaultRoles []string            `json:"default_roles"`
	GroupRoles   map[string][]string `json:"group_roles"`
	JIT          bool                `json:"jit"`
	Enforce      bool                `json:"enforce"`
	Disabled     bool                `json:"disabled"`
	Domains      []ssoDomain         `json:"domains"`
	// For the IdP's administrator.
	RedirectURI string `json:"redirect_uri,omitempty"` // OIDC
	EntityID    string `json:"entity_id,omitempty"`    // SAML
	ACSURL      string `json:"acs_url,omitempty"`      // SAML
	MetadataURL string `json:"metadata_url,omitempty"` // SAML
}

type ssoDomain struct {
	Domain   string     `json:"domain"`
	Record   string     `json:"txt_record"` // where the token goes
	Token    string     `json:"txt_value"`
	Verified *time.Time `json:"verified_at"`
}

func ssoSecretName(id uuid.UUID) string { return "sso_" + strings.ReplaceAll(id.String(), "-", "") }

func (s *Server) ssoOn(w http.ResponseWriter) bool {
	if s.PublicURL == "" {
		writeErr(w, http.StatusNotImplemented, "single sign-on needs TASKIEM_PUBLIC_URL")
		return false
	}
	return true
}

func (s *Server) oidcRedirect() string { return s.PublicURL + "/v1/auth/sso/oidc/callback" }
func (s *Server) samlACS() string      { return s.PublicURL + "/v1/auth/sso/saml/acs" }
func (s *Server) samlEntity(id uuid.UUID) string {
	return s.PublicURL + "/v1/auth/sso/saml/" + id.String() + "/metadata"
}

// ssoHTTP reaches an identity provider through the egress guard.
func (s *Server) ssoHTTP(tenant uuid.UUID, hosts []string) *http.Client {
	g := s.Egress
	if g == nil {
		g = &egress.Guard{}
	}
	return g.Client(egress.Policy{Tenant: tenant.String(), Hosts: hosts, Purpose: "sso"}, 15*time.Second)
}

// --- configuration ---

type ssoReq struct {
	Protocol string `json:"protocol"`
	Name     string `json:"name"`
	OIDC     *struct {
		Issuer       string   `json:"issuer"`
		ClientID     string   `json:"client_id"`
		ClientSecret string   `json:"client_secret"`
		AuthMethod   string   `json:"auth_method"`
		Scopes       []string `json:"scopes"`
		GroupsClaim  string   `json:"groups_claim"`
	} `json:"oidc"`
	SAML *struct {
		MetadataXML string `json:"metadata_xml"`
		EmailAttr   string `json:"email_attr"`
		NameAttr    string `json:"name_attr"`
		GroupsAttr  string `json:"groups_attr"`
	} `json:"saml"`
	DefaultRoles []string            `json:"default_roles"`
	GroupRoles   map[string][]string `json:"group_roles"`
	JIT          *bool               `json:"jit"`
	Enforce      bool                `json:"enforce"`
}

func hostOf(u string) string {
	pu, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return pu.Hostname()
}

// configure checks a request and returns the stored config (and the
// client secret to keep in the vault, for OIDC).
func (s *Server) configureSSO(ctx context.Context, tenant uuid.UUID, req ssoReq) (json.RawMessage, string, error) {
	switch req.Protocol {
	case "oidc":
		o := req.OIDC
		if o == nil || o.Issuer == "" || o.ClientID == "" {
			return nil, "", fmt.Errorf("%w: oidc needs issuer and client_id", errBadRequest)
		}
		if u, err := url.Parse(o.Issuer); err != nil || u.Scheme != "https" && u.Hostname() != "127.0.0.1" {
			return nil, "", fmt.Errorf("%w: the issuer must be an https URL", errBadRequest)
		}
		if o.AuthMethod != "" && o.AuthMethod != "client_secret_basic" && o.AuthMethod != "client_secret_post" {
			return nil, "", fmt.Errorf("%w: auth_method is client_secret_basic or client_secret_post", errBadRequest)
		}
		// Discover now: it proves the settings and finds the hosts sign-ins
		// will reach.
		p := &oidc.Provider{Issuer: o.Issuer, ClientID: o.ClientID, HTTP: s.ssoHTTP(tenant, []string{hostOf(o.Issuer)})}
		d, err := p.Discover(ctx)
		if err != nil {
			return nil, "", fmt.Errorf("%w: discovery at %s failed: %w", errBadRequest, o.Issuer, err)
		}
		hosts := []string{}
		for _, u := range []string{o.Issuer, d.AuthorizationEndpoint, d.TokenEndpoint, d.JWKSURI} {
			if h := hostOf(u); h != "" && !slices.Contains(hosts, h) {
				hosts = append(hosts, h)
			}
		}
		scopes := o.Scopes
		if len(scopes) == 0 {
			scopes = []string{"openid", "email", "profile"}
		}
		if !slices.Contains(scopes, "openid") {
			scopes = append([]string{"openid"}, scopes...)
		}
		raw, _ := json.Marshal(oidcConfig{Issuer: o.Issuer, ClientID: o.ClientID, AuthMethod: o.AuthMethod, Scopes: scopes, GroupsClaim: o.GroupsClaim, Hosts: hosts})
		return raw, o.ClientSecret, nil
	case "saml":
		if req.SAML == nil || req.SAML.MetadataXML == "" {
			return nil, "", fmt.Errorf("%w: saml needs the identity provider's metadata_xml", errBadRequest)
		}
		md, err := saml.ParseMetadata([]byte(req.SAML.MetadataXML))
		if err != nil {
			return nil, "", fmt.Errorf("%w: %w", errBadRequest, err)
		}
		c := samlConfig{IdPEntityID: md.EntityID, IdPSSOURL: md.SSOURL, EmailAttr: req.SAML.EmailAttr, NameAttr: req.SAML.NameAttr, GroupsAttr: req.SAML.GroupsAttr}
		for _, cert := range md.Certs {
			c.IdPCerts = append(c.IdPCerts, base64.StdEncoding.EncodeToString(cert.Raw))
		}
		raw, _ := json.Marshal(c)
		return raw, "", nil
	}
	return nil, "", fmt.Errorf("%w: protocol is oidc or saml", errBadRequest)
}

func (s *Server) checkRoleMapping(r *http.Request, tx pgx.Tx, req ssoReq) error {
	roles := slices.Clone(req.DefaultRoles)
	for _, rs := range req.GroupRoles {
		roles = append(roles, rs...)
	}
	if slices.Contains(roles, "owner") && !slices.Contains(principalFrom(r.Context()).Roles, "owner") {
		return fmt.Errorf("%w: only an owner can map a group to owner", errForbidden)
	}
	return canGrant(r.Context(), tx, principalFrom(r.Context()), roles)
}

func (s *Server) createSSO(w http.ResponseWriter, r *http.Request) {
	if !s.ssoOn(w) {
		return
	}
	var req ssoReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	cfg, secret, err := s.configureSSO(r.Context(), p.TenantID, req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Protocol == "oidc" && secret == "" {
		s.fail(w, r, fmt.Errorf("%w: oidc needs client_secret", errBadRequest))
		return
	}
	id := uuid.Must(uuid.NewV7())
	jit := req.JIT == nil || *req.JIT
	if req.GroupRoles == nil {
		req.GroupRoles = map[string][]string{}
	}
	groups, _ := json.Marshal(req.GroupRoles)
	err = s.tx(r, func(tx pgx.Tx) error {
		if err := s.checkRoleMapping(r, tx, req); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO sso_connections (id, tenant_id, protocol, name, config, default_roles, group_roles, jit, enforce, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, id, p.TenantID, req.Protocol, req.Name, cfg, nonNil(req.DefaultRoles), groups, jit, req.Enforce, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "sso.create", id.String(), map[string]any{"protocol": req.Protocol, "default_roles": req.DefaultRoles, "group_roles": req.GroupRoles, "jit": jit, "enforce": req.Enforce})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if secret != "" {
		if _, err := s.Vault.Put(r.Context(), p.TenantID, identityEnv, ssoSecretName(id), []byte(secret), p.Actor()); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusCreated, s.describeSSO(id, req.Protocol))
}

func (s *Server) describeSSO(id uuid.UUID, protocol string) ssoConnection {
	c := ssoConnection{ID: id, Protocol: protocol}
	if protocol == "oidc" {
		c.RedirectURI = s.oidcRedirect()
	} else {
		c.EntityID, c.ACSURL, c.MetadataURL = s.samlEntity(id), s.samlACS(), s.samlEntity(id)
	}
	return c
}

func (s *Server) listSSO(w http.ResponseWriter, r *http.Request) {
	var out []ssoConnection
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id, protocol, name, config, default_roles, group_roles, jit, enforce, disabled_at IS NOT NULL FROM sso_connections ORDER BY created_at`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c ssoConnection
			var groups []byte
			if err := rows.Scan(&c.ID, &c.Protocol, &c.Name, &c.Config, &c.DefaultRoles, &groups, &c.JIT, &c.Enforce, &c.Disabled); err != nil {
				rows.Close()
				return err
			}
			_ = json.Unmarshal(groups, &c.GroupRoles)
			d := s.describeSSO(c.ID, c.Protocol)
			c.RedirectURI, c.EntityID, c.ACSURL, c.MetadataURL = d.RedirectURI, d.EntityID, d.ACSURL, d.MetadataURL
			out = append(out, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for i := range out {
			rows, err := tx.Query(r.Context(), `SELECT domain, token, verified_at FROM sso_domains WHERE connection_id = $1 ORDER BY domain`, out[i].ID)
			if err != nil {
				return err
			}
			for rows.Next() {
				var d ssoDomain
				if err := rows.Scan(&d.Domain, &d.Token, &d.Verified); err != nil {
					rows.Close()
					return err
				}
				d.Record = "_taskiem-verify." + d.Domain
				out[i].Domains = append(out[i].Domains, d)
			}
			rows.Close()
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": nonNil(out)})
}

// updateSSO changes the role mapping, JIT and enforcement, or disables.
func (s *Server) updateSSO(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var req struct {
		ssoReq
		Disabled bool `json:"disabled"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.GroupRoles == nil {
		req.GroupRoles = map[string][]string{}
	}
	groups, _ := json.Marshal(req.GroupRoles)
	jit := req.JIT == nil || *req.JIT
	err = s.tx(r, func(tx pgx.Tx) error {
		if err := s.checkRoleMapping(r, tx, req.ssoReq); err != nil {
			return err
		}
		tag, err := tx.Exec(r.Context(), `UPDATE sso_connections SET default_roles = $2, group_roles = $3, jit = $4, enforce = $5,
			disabled_at = CASE WHEN $6 THEN COALESCE(disabled_at, now()) END, updated_at = now() WHERE id = $1`,
			id, nonNil(req.DefaultRoles), groups, jit, req.Enforce, req.Disabled)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return auditTx(r, tx, "sso.update", id.String(), map[string]any{"default_roles": req.DefaultRoles, "group_roles": req.GroupRoles, "jit": jit, "enforce": req.Enforce, "disabled": req.Disabled})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// addSSODomain claims a domain for a connection; it routes nobody until
// verified.
func (s *Server) addSSODomain(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var req struct {
		Domain string `json:"domain"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	token := "taskiem-verify=" + newToken()[:32]
	p := principalFrom(r.Context())
	err = s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO sso_domains (domain, connection_id, tenant_id, token) VALUES ($1, $2, $3, $4)`, domain, id, p.TenantID, token); err != nil {
			var pgErr interface{ SQLState() string }
			if errors.As(err, &pgErr) {
				switch pgErr.SQLState() {
				case "23505":
					return fmt.Errorf("%w: %s is already claimed", errConflict, domain)
				case "23514":
					return fmt.Errorf("%w: %q is not a domain", errBadRequest, domain)
				case "23503":
					return pgx.ErrNoRows
				}
			}
			return err
		}
		return auditTx(r, tx, "sso.domain_add", domain, map[string]any{"connection": id})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, ssoDomain{Domain: domain, Record: "_taskiem-verify." + domain, Token: token})
}

// LookupTXT is how domains are verified; tests replace it.
var LookupTXT = net.DefaultResolver.LookupTXT

func (s *Server) verifySSODomain(w http.ResponseWriter, r *http.Request) {
	domain := strings.ToLower(chi.URLParam(r, "domain"))
	var token string
	err := s.tx(r, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT token FROM sso_domains WHERE domain = $1`, domain).Scan(&token)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	records, _ := LookupTXT(ctx, "_taskiem-verify."+domain)
	if !slices.Contains(records, token) {
		s.fail(w, r, fmt.Errorf("%w: no TXT record _taskiem-verify.%s with value %s yet (DNS can take a while)", errConflict, domain, token))
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `UPDATE sso_domains SET verified_at = now() WHERE domain = $1`, domain); err != nil {
			return err
		}
		return auditTx(r, tx, "sso.domain_verify", domain, nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- sign-in ---

// ssoDiscover tells the sign-in page whether an email signs in with SSO.
func (s *Server) ssoDiscover(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	var id uuid.UUID
	var tenant uuid.UUID
	var enforce bool
	err := s.Store.Pool.QueryRow(r.Context(), `SELECT connection_id, tenant_id, enforce FROM taskiem_auth_sso_for_email($1)`, req.Email).Scan(&id, &tenant, &enforce)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"sso": false})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sso": true, "connection_id": id, "required": enforce, "start": "/v1/auth/sso/" + id.String() + "/start"})
}

type ssoConn struct {
	id       uuid.UUID
	tenant   uuid.UUID
	protocol string
	config   json.RawMessage
	domains  []string
}

func (s *Server) loadSSO(ctx context.Context, id uuid.UUID) (ssoConn, error) {
	c := ssoConn{id: id}
	err := s.Store.Pool.QueryRow(ctx, `SELECT tenant_id, protocol, config, domains FROM taskiem_auth_sso_connection($1)`, id).Scan(&c.tenant, &c.protocol, &c.config, &c.domains)
	return c, err
}

func (s *Server) oidcProvider(ctx context.Context, c ssoConn) (*oidc.Provider, oidcConfig, error) {
	var cfg oidcConfig
	if err := json.Unmarshal(c.config, &cfg); err != nil {
		return nil, cfg, err
	}
	secret, err := s.Vault.Get(ctx, c.tenant, identityEnv, ssoSecretName(c.id))
	if err != nil {
		return nil, cfg, err
	}
	return &oidc.Provider{Issuer: cfg.Issuer, ClientID: cfg.ClientID, ClientSecret: secret, AuthMethod: cfg.AuthMethod, HTTP: s.ssoHTTP(c.tenant, cfg.Hosts)}, cfg, nil
}

func (s *Server) samlSP(c ssoConn) (saml.SP, samlConfig, error) {
	var cfg samlConfig
	if err := json.Unmarshal(c.config, &cfg); err != nil {
		return saml.SP{}, cfg, err
	}
	sp := saml.SP{EntityID: s.samlEntity(c.id), ACSURL: s.samlACS(), IdPEntityID: cfg.IdPEntityID, IdPSSOURL: cfg.IdPSSOURL}
	for _, b := range cfg.IdPCerts {
		der, err := base64.StdEncoding.DecodeString(b)
		if err != nil {
			return saml.SP{}, cfg, err
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return saml.SP{}, cfg, err
		}
		sp.IdPCerts = append(sp.IdPCerts, cert)
	}
	return sp, cfg, nil
}

// safeReturn keeps the post-sign-in redirect on our own pages.
func safeReturn(v string) string {
	if !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") || strings.Contains(v, "\\\\") {
		return "/"
	}
	return v
}

func (s *Server) ssoStart(w http.ResponseWriter, r *http.Request) {
	if !s.ssoOn(w) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	c, err := s.loadSSO(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "no such single sign-on connection")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	state := randomState()
	ret := safeReturn(r.URL.Query().Get("return_to"))
	var target, nonce, verifier string
	switch c.protocol {
	case "oidc":
		p, cfg, err := s.oidcProvider(r.Context(), c)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		nonce, verifier = oidc.NewNonce(), oidc.NewVerifier()
		scopes := cfg.Scopes
		if target, err = p.AuthURL(r.Context(), s.oidcRedirect(), b64.EncodeToString(state), nonce, verifier, scopes); err != nil {
			s.fail(w, r, err)
			return
		}
	case "saml":
		sp, _, err := s.samlSP(c)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		var reqID string
		if target, reqID, err = sp.AuthURL(b64.EncodeToString(state)); err != nil {
			s.fail(w, r, err)
			return
		}
		nonce, verifier = "-", reqID
	}
	if _, err := s.Store.Pool.Exec(r.Context(), `SELECT taskiem_auth_sso_begin($1, $2, $3, $4, $5, $6)`, state, c.id, c.tenant, nonce, verifier, ret); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, target, http.StatusFound) //nolint:gosec // the tenant's own identity provider, from its saved configuration
}

func randomState() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b
}

type ssoRequest struct {
	conn     uuid.UUID
	tenant   uuid.UUID
	nonce    string
	verifier string
	returnTo string
}

func (s *Server) takeSSO(ctx context.Context, state string) (ssoRequest, error) {
	raw, err := b64.DecodeString(state)
	if err != nil || len(raw) != 32 {
		return ssoRequest{}, errSSOFailed
	}
	var q ssoRequest
	err = s.Store.Pool.QueryRow(ctx, `SELECT connection_id, tenant_id, nonce, verifier, return_to FROM taskiem_auth_sso_take($1)`, raw).
		Scan(&q.conn, &q.tenant, &q.nonce, &q.verifier, &q.returnTo)
	if errors.Is(err, pgx.ErrNoRows) {
		return ssoRequest{}, errSSOFailed
	}
	return q, err
}

var errSSOFailed = errors.New("single sign-on did not complete; start again")

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req, err := s.takeSSO(r.Context(), q.Get("state"))
	if err != nil {
		s.ssoFail(w, r, err)
		return
	}
	if e := q.Get("error"); e != "" {
		s.ssoFail(w, r, fmt.Errorf("%w (%s)", errSSOFailed, e))
		return
	}
	c, err := s.loadSSO(r.Context(), req.conn)
	if err != nil {
		s.ssoFail(w, r, err)
		return
	}
	p, cfg, err := s.oidcProvider(r.Context(), c)
	if err != nil {
		s.ssoFail(w, r, err)
		return
	}
	raw, err := p.Exchange(r.Context(), q.Get("code"), s.oidcRedirect(), req.verifier)
	if err != nil {
		s.ssoFail(w, r, err)
		return
	}
	claims, err := p.Verify(r.Context(), raw, req.nonce, cfg.GroupsClaim, time.Now())
	if err != nil {
		s.ssoFail(w, r, err)
		return
	}
	if !claims.EmailVerified {
		s.ssoFail(w, r, errors.New("the identity provider has not verified this email address"))
		return
	}
	s.finishSSO(w, r, c, req, claims.Email, claims.Name, claims.Groups, cfg.GroupsClaim != "")
}

func (s *Server) samlACSHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.ssoFail(w, r, err)
		return
	}
	req, err := s.takeSSO(r.Context(), r.PostForm.Get("RelayState"))
	if err != nil {
		s.ssoFail(w, r, err)
		return
	}
	c, err := s.loadSSO(r.Context(), req.conn)
	if err != nil || c.protocol != "saml" {
		s.ssoFail(w, r, errSSOFailed)
		return
	}
	sp, cfg, err := s.samlSP(c)
	if err != nil {
		s.ssoFail(w, r, err)
		return
	}
	id, err := sp.Verify(r.PostForm.Get("SAMLResponse"), req.verifier, saml.Attributes{Email: cfg.EmailAttr, Name: cfg.NameAttr, Groups: cfg.GroupsAttr})
	if err != nil {
		s.ssoFail(w, r, err)
		return
	}
	s.finishSSO(w, r, c, req, id.Email, id.Name, id.Groups, cfg.GroupsAttr != "")
}

func (s *Server) samlMetadata(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	c, err := s.loadSSO(r.Context(), id)
	if err != nil || c.protocol != "saml" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	sp, _, err := s.samlSP(c)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	md, err := sp.SPMetadata()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/samlmetadata+xml")
	_, _ = w.Write(md) //nolint:gosec // XML we generate from our own URLs
}

// ssoFail sends the person back to the sign-in page with a reason; the
// detail goes to the log only.
func (s *Server) ssoFail(w http.ResponseWriter, r *http.Request, err error) {
	if s.Logger != nil {
		s.Logger.Warn("single sign-on refused", "err", err, "ip", clientIP(r))
	}
	msg := "Single sign-on did not complete. Try again, or ask your administrator."
	switch {
	case errors.Is(err, errNoMembership), errors.Is(err, errNotMember), errors.Is(err, errSSONotMember):
		msg = "You are not a member of this organisation in Taskiem. Ask your administrator."
	case errors.Is(err, errSSODomain):
		msg = "This sign-in is not for your email domain."
	}
	http.Redirect(w, r, "/login?sso_error="+url.QueryEscape(msg), http.StatusFound)
}

var (
	errSSODomain    = errors.New("email not on the connection's verified domains")
	errSSONotMember = errors.New("not a member, and the connection does not create members")
)

// finishSSO turns a verified identity into a member and a session.
func (s *Server) finishSSO(w http.ResponseWriter, r *http.Request, c ssoConn, req ssoRequest, email, name string, groups []string, groupsMapped bool) {
	ctx := r.Context()
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndex(email, "@")
	if at < 1 || !slices.Contains(c.domains, email[at+1:]) {
		s.ssoFail(w, r, errSSODomain)
		return
	}
	var settings struct {
		DefaultRoles []string
		GroupRoles   map[string][]string
		JIT          bool
	}
	var user uuid.UUID
	var granted, revoked []string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant}, func(tx pgx.Tx) error {
		var groupsRaw []byte
		if err := tx.QueryRow(ctx, `SELECT default_roles, group_roles, jit FROM sso_connections WHERE id = $1`, c.id).Scan(&settings.DefaultRoles, &groupsRaw, &settings.JIT); err != nil {
			return err
		}
		_ = json.Unmarshal(groupsRaw, &settings.GroupRoles)
		err := s.Store.Pool.QueryRow(ctx, `SELECT user_id FROM taskiem_auth_find_user($1)`, email).Scan(&user)
		if errors.Is(err, pgx.ErrNoRows) {
			if !settings.JIT {
				return errSSONotMember
			}
			user = uuid.Must(uuid.NewV7())
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, user, email, name); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if off, err := scimDeactivated(ctx, tx, user); err != nil {
			return err
		} else if off {
			return errSSONotMember // deprovisioned by the tenant's SCIM provider
		}
		want := slices.Clone(settings.DefaultRoles)
		for _, g := range groups {
			want = append(want, settings.GroupRoles[g]...)
		}
		slices.Sort(want)
		want = slices.Compact(want)
		rows, err := tx.Query(ctx, `SELECT role, source FROM memberships WHERE user_id = $1`, user)
		if err != nil {
			return err
		}
		have := map[string]string{}
		for rows.Next() {
			var role, src string
			if err := rows.Scan(&role, &src); err != nil {
				rows.Close()
				return err
			}
			have[role] = src
		}
		rows.Close()
		if len(have) == 0 && !settings.JIT {
			return errSSONotMember
		}
		for _, role := range want {
			if _, ok := have[role]; !ok {
				if _, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role, source) VALUES ($1, $2, $3, 'sso')`, c.tenant, user, role); err != nil {
					return err
				}
				granted = append(granted, role)
			}
		}
		// Roles single sign-on granted follow the groups; others stay.
		if groupsMapped || len(settings.DefaultRoles) > 0 {
			for role, src := range have {
				if src == "sso" && !slices.Contains(want, role) {
					if _, err := tx.Exec(ctx, `DELETE FROM memberships WHERE user_id = $1 AND role = $2 AND source = 'sso'`, user, role); err != nil {
						return err
					}
					revoked = append(revoked, role)
				}
			}
		}
		_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'auth.sso', $3, jsonb_build_object('connection', $4::text, 'groups', $5::jsonb, 'granted', $6::jsonb, 'revoked', $7::jsonb))`,
			c.tenant, user.String(), user.String(), c.id.String(), toJSONB(groups), toJSONB(granted), toJSONB(revoked))
		return err
	})
	if err != nil {
		s.ssoFail(w, r, err)
		return
	}
	sess, err := s.createSession(r, user, c.tenant, "sso")
	if err != nil {
		s.ssoFail(w, r, err)
		return
	}
	s.setSessionCookie(w, sess.token)
	http.Redirect(w, r, safeReturn(req.returnTo), http.StatusFound)
}

func toJSONB(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

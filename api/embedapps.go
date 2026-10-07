package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/embed"
)

// Embed apps (spec 13.4 step 1) and end-user tokens (step 2), on the
// partner admin API.

const (
	endUserTokenPrefix = "tsk_eut_" //nolint:gosec // a token prefix, not a credential
	webhookSecretPfx   = "whsec_"   //nolint:gosec // a secret prefix, not a credential
	defaultTokenTTL    = 15 * time.Minute
	maxTokenTTL        = time.Hour
	maxOrigins         = 20
)

var (
	connectorIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
	externalIDRe  = regexp.MustCompile(`^[A-Za-z0-9._:@|-]{1,128}$`)
)

type embedAppReq struct {
	Name               string          `json:"name"`
	AllowedOrigins     []string        `json:"allowed_origins"`
	Branding           json.RawMessage `json:"branding,omitempty"`
	AllowedConnectors  []string        `json:"allowed_connectors"`
	AllowedTemplates   []string        `json:"allowed_templates,omitempty"`
	EndUserPermissions []string        `json:"end_user_permissions,omitempty"`
	Headless           bool            `json:"headless,omitempty"`
	WebhookURL         string          `json:"webhook_url,omitempty"`
	WebhookEvents      []string        `json:"webhook_events,omitempty"`
	Status             string          `json:"status,omitempty"`
}

type embedApp struct {
	ID                 uuid.UUID       `json:"id"`
	Name               string          `json:"name"`
	AllowedOrigins     []string        `json:"allowed_origins"`
	Branding           json.RawMessage `json:"branding"`
	AllowedConnectors  []string        `json:"allowed_connectors"`
	AllowedTemplates   []string        `json:"allowed_templates"`
	EndUserPermissions []string        `json:"end_user_permissions"`
	Headless           bool            `json:"headless"`
	WebhookURL         *string         `json:"webhook_url"`
	WebhookEvents      []string        `json:"webhook_events"`
	Status             string          `json:"status"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	WebhookSecretSet   bool            `json:"webhook_secret_set"`
}

const embedAppColumns = `a.id, a.name, a.allowed_origins, a.branding, a.allowed_connectors, a.allowed_templates, a.end_user_permissions, a.headless,
	a.webhook_url, a.webhook_events, a.status, a.created_at, a.updated_at,
	EXISTS (SELECT 1 FROM secrets s WHERE s.environment = '` + embed.VaultEnv + `' AND s.name = 'app_' || a.id::text || '_webhook')`

// check validates and normalises an embed app's settings.
func (s *Server) checkEmbedApp(r *http.Request, req *embedAppReq) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 100 {
		return fmt.Errorf("%w: a name of up to 100 characters is required", errBadRequest)
	}
	if len(req.AllowedOrigins) > maxOrigins {
		return fmt.Errorf("%w: at most %d allowed origins", errBadRequest, maxOrigins)
	}
	origins := []string{}
	for _, o := range req.AllowedOrigins {
		c, err := embed.Origin(o)
		if err != nil {
			return fmt.Errorf("%w: %w", errBadRequest, err)
		}
		if !slices.Contains(origins, c) {
			origins = append(origins, c)
		}
	}
	req.AllowedOrigins = origins
	if len(origins) == 0 && !req.Headless {
		return fmt.Errorf("%w: an app needs allowed origins, or headless for server-side use only", errBadRequest)
	}
	b, err := embed.ParseBranding(req.Branding)
	if err != nil {
		return fmt.Errorf("%w: %w", errBadRequest, err)
	}
	req.Branding, _ = json.Marshal(b)
	// Connectors must exist for the partner's sub-tenants: the platform's
	// catalogue (a sub-tenant has no connectors of its own), plus the gated
	// step types by name.
	known := []string{"http", "code", "ai"}
	for _, c := range s.Registry.List() {
		known = append(known, c.Manifest.ID)
	}
	conns := []string{}
	for _, c := range req.AllowedConnectors {
		if !connectorIDRe.MatchString(c) || !slices.Contains(known, c) {
			return fmt.Errorf("%w: unknown connector %q", errBadRequest, c)
		}
		if !slices.Contains(conns, c) {
			conns = append(conns, c)
		}
	}
	req.AllowedConnectors = conns
	tpls := []string{}
	for _, t := range req.AllowedTemplates {
		if !connectorIDRe.MatchString(t) {
			return fmt.Errorf("%w: template ids are lowercase letters, digits, '.', '_' and '-': %q", errBadRequest, t)
		}
		if !slices.Contains(tpls, t) {
			tpls = append(tpls, t)
		}
	}
	req.AllowedTemplates = tpls
	if req.EndUserPermissions == nil {
		req.EndUserPermissions = embed.DefaultEndUserPermissions
	}
	for _, perm := range req.EndUserPermissions {
		if !slices.Contains(embed.EndUserCeiling, perm) {
			return fmt.Errorf("%w: end users cannot hold %q (allowed: %s)", errBadRequest, perm, strings.Join(embed.EndUserCeiling, ", "))
		}
	}
	if req.WebhookURL != "" {
		if err := embed.WebhookURL(req.WebhookURL); err != nil {
			return fmt.Errorf("%w: %w", errBadRequest, err)
		}
	}
	if req.WebhookEvents == nil {
		req.WebhookEvents = embed.Events
	}
	for _, e := range req.WebhookEvents {
		if !slices.Contains(embed.Events, e) {
			return fmt.Errorf("%w: unknown webhook event %q (one of %s)", errBadRequest, e, strings.Join(embed.Events, ", "))
		}
	}
	switch req.Status {
	case "":
		req.Status = "active"
	case "active", "disabled":
	default:
		return fmt.Errorf("%w: status is active or disabled", errBadRequest)
	}
	return nil
}

func (s *Server) loadEmbedApp(r *http.Request, tx pgx.Tx, id uuid.UUID) (embedApp, error) {
	rows, err := tx.Query(r.Context(), `SELECT `+embedAppColumns+` FROM embed_apps a WHERE a.id = $1`, id)
	if err != nil {
		return embedApp{}, err
	}
	return pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[embedApp])
}

func appParam(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "app"))
	if err != nil {
		return uuid.Nil, pgx.ErrNoRows
	}
	return id, nil
}

// putWebhookSecret makes a new signing secret for an app; it is shown once.
func (s *Server) putWebhookSecret(r *http.Request, app uuid.UUID) (string, error) {
	p := principalFrom(r.Context())
	secret := webhookSecretPfx + newToken()
	_, err := s.Vault.Put(r.Context(), p.TenantID, embed.VaultEnv, embed.SecretName(app), []byte(secret), p.Actor())
	return secret, err
}

func (s *Server) createEmbedApp(w http.ResponseWriter, r *http.Request) {
	var req embedAppReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.checkEmbedApp(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	id := uuid.Must(uuid.NewV7())
	err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO embed_apps (id, tenant_id, name, allowed_origins, branding, allowed_connectors, allowed_templates,
			end_user_permissions, headless, webhook_url, webhook_events, status, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''), $11, $12, $13)`,
			id, p.TenantID, req.Name, req.AllowedOrigins, req.Branding, req.AllowedConnectors, req.AllowedTemplates,
			req.EndUserPermissions, req.Headless, req.WebhookURL, req.WebhookEvents, req.Status, p.Actor()); err != nil {
			if isUnique(err) {
				return fmt.Errorf("%w: an app named %q exists", errConflict, req.Name)
			}
			return err
		}
		return auditTx(r, tx, "embed_app.create", id.String(), map[string]any{"name": req.Name, "allowed_origins": req.AllowedOrigins,
			"allowed_connectors": req.AllowedConnectors, "end_user_permissions": req.EndUserPermissions, "headless": req.Headless})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]any{"id": id}
	if req.WebhookURL != "" {
		secret, err := s.putWebhookSecret(r, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out["webhook_secret"] = secret
	}
	_ = s.tx(r, func(tx pgx.Tx) error {
		app, err := s.loadEmbedApp(r, tx, id)
		out["app"] = app
		return err
	})
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) listEmbedApps(w http.ResponseWriter, r *http.Request) {
	var out []embedApp
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+embedAppColumns+` FROM embed_apps a ORDER BY a.name`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[embedApp])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"embed_apps": nonNil(out)})
}

func (s *Server) getEmbedApp(w http.ResponseWriter, r *http.Request) {
	id, err := appParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var app embedApp
	err = s.tx(r, func(tx pgx.Tx) error {
		app, err = s.loadEmbedApp(r, tx, id)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, app)
}

// updateEmbedApp replaces an app's settings. Narrowing its connectors or
// permissions applies at once to tokens already minted (they are checked
// against the app on every request); disabling it stops all its tokens.
func (s *Server) updateEmbedApp(w http.ResponseWriter, r *http.Request) {
	id, err := appParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req embedAppReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.checkEmbedApp(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	var app embedApp
	err = s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE embed_apps SET name = $2, allowed_origins = $3, branding = $4, allowed_connectors = $5, allowed_templates = $6,
			end_user_permissions = $7, headless = $8, webhook_url = NULLIF($9, ''), webhook_events = $10, status = $11, updated_at = now() WHERE id = $1`,
			id, req.Name, req.AllowedOrigins, req.Branding, req.AllowedConnectors, req.AllowedTemplates, req.EndUserPermissions, req.Headless,
			req.WebhookURL, req.WebhookEvents, req.Status)
		if err != nil {
			if isUnique(err) {
				return fmt.Errorf("%w: an app named %q exists", errConflict, req.Name)
			}
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		if app, err = s.loadEmbedApp(r, tx, id); err != nil {
			return err
		}
		return auditTx(r, tx, "embed_app.update", id.String(), map[string]any{"name": req.Name, "allowed_origins": req.AllowedOrigins,
			"allowed_connectors": req.AllowedConnectors, "end_user_permissions": req.EndUserPermissions, "headless": req.Headless, "status": req.Status})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]any{"app": app}
	if req.WebhookURL != "" && !app.WebhookSecretSet {
		secret, err := s.putWebhookSecret(r, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out["webhook_secret"] = secret
		app.WebhookSecretSet = true
		out["app"] = app
	}
	writeJSON(w, http.StatusOK, out)
}

// rotateWebhookSecret replaces an app's webhook signing secret and shows the
// new one once. Deliveries sign with the new secret from then on.
func (s *Server) rotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	id, err := appParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		if _, err := s.loadEmbedApp(r, tx, id); err != nil {
			return err
		}
		return auditTx(r, tx, "embed_app.webhook_secret.rotate", id.String(), nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	secret, err := s.putWebhookSecret(r, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhook_secret": secret})
}

type mintReq struct {
	EndUserID   string    `json:"end_user_id"`
	SubTenant   uuid.UUID `json:"sub_tenant"`
	Permissions []string  `json:"permissions"`
	TTL         int       `json:"ttl,omitempty"` // seconds; default 900, at most 3600
	Origin      string    `json:"origin,omitempty"`
}

// mintEndUserToken issues a short-lived token for one end user of an app in
// one of the partner's sub-tenants (spec 13.4 step 2). The partner's server
// calls it and hands the token to its own front end; it is shown once.
func (s *Server) mintEndUserToken(w http.ResponseWriter, r *http.Request) {
	appID, err := appParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req mintReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	ttl := time.Duration(req.TTL) * time.Second
	switch {
	case req.TTL == 0:
		ttl = defaultTokenTTL
	case req.TTL < 0 || ttl > maxTokenTTL:
		s.fail(w, r, fmt.Errorf("%w: ttl is 1 to %d seconds", errBadRequest, int(maxTokenTTL.Seconds())))
		return
	}
	if !externalIDRe.MatchString(req.EndUserID) || req.SubTenant == uuid.Nil || len(req.Permissions) == 0 {
		s.fail(w, r, fmt.Errorf("%w: end_user_id (letters, digits and ._:@|-, up to 128), sub_tenant and permissions are required", errBadRequest))
		return
	}
	origin := ""
	if req.Origin != "" {
		if origin, err = embed.Origin(req.Origin); err != nil {
			s.fail(w, r, fmt.Errorf("%w: %w", errBadRequest, err))
			return
		}
	}
	p := principalFrom(r.Context())
	tok := endUserTokenPrefix + newToken()
	tokenID := uuid.Must(uuid.NewV7())
	var endUser uuid.UUID
	var expires time.Time
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		app, err := s.loadEmbedApp(r, tx, appID)
		if err != nil {
			return err
		}
		if app.Status != "active" {
			return fmt.Errorf("%w: the app is disabled", errConflict)
		}
		for _, perm := range req.Permissions {
			if !slices.Contains(app.EndUserPermissions, perm) {
				return fmt.Errorf("%w: the app does not allow its end users %q", errForbidden, perm)
			}
		}
		if origin != "" && !slices.Contains(app.AllowedOrigins, origin) {
			return fmt.Errorf("%w: %s is not one of the app's allowed origins", errBadRequest, origin)
		}
		detail, _ := json.Marshal(map[string]any{"app": appID, "end_user": req.EndUserID, "permissions": req.Permissions,
			"ttl_seconds": int(ttl.Seconds()), "origin": origin, "token_id": tokenID, "ip": clientIP(r)})
		if _, err := tx.Exec(ctx, `SELECT taskiem_partner_enter($1, $2, $3, 'partner.end_user_token.mint', $4)`, req.SubTenant, p.ActorType(), p.Actor(), detail); err != nil {
			return err
		}
		// Inside the sub-tenant alone from here.
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM tenants WHERE id = $1`, req.SubTenant).Scan(&status); err != nil {
			return err
		}
		if status != "active" {
			return fmt.Errorf("%w: the sub-tenant is %s", errConflict, status)
		}
		if err := tx.QueryRow(ctx, `INSERT INTO end_users (id, tenant_id, app_id, external_id, last_token_at) VALUES ($1, $2, $3, $4, now())
			ON CONFLICT (tenant_id, app_id, external_id) DO UPDATE SET last_token_at = now() RETURNING id`,
			uuid.Must(uuid.NewV7()), req.SubTenant, appID, req.EndUserID).Scan(&endUser); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO end_user_tokens (token_hash, id, tenant_id, app_id, end_user_id, permissions, origin, created_by, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, now() + make_interval(secs => $9)) RETURNING expires_at`,
			hashToken(tok), tokenID, req.SubTenant, appID, endUser, req.Permissions, origin, p.Actor(), ttl.Seconds()).Scan(&expires)
	})
	if err != nil {
		s.fail(w, r, partnerErr(err))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": tok, "token_id": tokenID, "expires_at": expires, "app_id": appID,
		"sub_tenant": req.SubTenant, "end_user": map[string]any{"id": endUser, "external_id": req.EndUserID}, "permissions": req.Permissions})
}

type revokeReq struct {
	SubTenant uuid.UUID `json:"sub_tenant"`
	EndUserID string    `json:"end_user_id"`
}

// revokeEndUserTokens revokes every live token of one end user of an app.
func (s *Server) revokeEndUserTokens(w http.ResponseWriter, r *http.Request) {
	appID, err := appParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req revokeReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.SubTenant == uuid.Nil || !externalIDRe.MatchString(req.EndUserID) {
		s.fail(w, r, fmt.Errorf("%w: sub_tenant and end_user_id are required", errBadRequest))
		return
	}
	var n int64
	err = s.partnerTx(r, req.SubTenant, "partner.end_user_token.revoke", map[string]any{"app": appID, "end_user": req.EndUserID}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE end_user_tokens k SET revoked_at = now() FROM end_users u
			WHERE u.id = k.end_user_id AND u.app_id = $1 AND u.external_id = $2 AND k.revoked_at IS NULL`, appID, req.EndUserID)
		n = tag.RowsAffected()
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": n})
}

type webhookDelivery struct {
	ID          uuid.UUID       `json:"id"`
	AppID       uuid.UUID       `json:"app_id"`
	Event       string          `json:"event"`
	SubTenantID *uuid.UUID      `json:"sub_tenant_id"`
	Payload     json.RawMessage `json:"payload"`
	Status      string          `json:"status"`
	Attempts    int             `json:"attempts"`
	NextAttempt time.Time       `json:"next_attempt_at"`
	LastStatus  *int            `json:"last_status"`
	LastError   *string         `json:"last_error"`
	CreatedAt   time.Time       `json:"created_at"`
	DeliveredAt *time.Time      `json:"delivered_at"`
}

// listWebhookDeliveries is the delivery log, newest first. Filters: app,
// status, event; paging with before and limit.
func (s *Server) listWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var app *uuid.UUID
	if v := q.Get("app"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			s.fail(w, r, fmt.Errorf("%w: bad app id", errBadRequest))
			return
		}
		app = &id
	}
	before := time.Now().Add(time.Hour)
	if b := q.Get("before"); b != "" {
		var err error
		if before, err = time.Parse(time.RFC3339Nano, b); err != nil {
			s.fail(w, r, fmt.Errorf("%w: before must be an RFC 3339 time", errBadRequest))
			return
		}
	}
	var out []webhookDelivery
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id, app_id, event, sub_tenant_id, payload, status, attempts, next_attempt_at, last_status, last_error, created_at, delivered_at
			FROM partner_webhook_deliveries WHERE created_at < $1 AND ($2::uuid IS NULL OR app_id = $2) AND ($3 = '' OR status = $3) AND ($4 = '' OR event = $4)
			ORDER BY created_at DESC LIMIT $5`, before, app, q.Get("status"), q.Get("event"), limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[webhookDelivery])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": nonNil(out)})
}

// retryWebhookDelivery sends a failed (or pending) delivery again soon,
// with a fresh set of attempts.
func (s *Server) retryWebhookDelivery(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE partner_webhook_deliveries SET status = 'pending', attempts = 0, next_attempt_at = now()
			WHERE id = $1 AND status <> 'delivered'`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return auditTx(r, tx, "partner.webhook.retry", id.String(), nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "status": "pending"})
}

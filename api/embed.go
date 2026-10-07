package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/embed"
)

// The embed API (spec 13.4; decision 0015): what an embed app's end users
// call, from the partner's pages (CORS, from the app's allowed origins) or,
// for a headless app, from the partner's servers. It lives at
// /v1/embed/{app}/..., takes only end-user tokens, and offers exactly the
// capabilities an embedded builder and run view need: workflows within the
// app's connectors, publishing (when allowed), and runs. Nothing else of
// the API accepts an end-user token. The embedded builder (the
// <taskiem-builder> element and the frame page, embedframe.go) calls these
// same routes.

// EndUser is an embed app's end user acting through a token: a lightweight
// principal in one sub-tenant, with no platform login.
type EndUser struct {
	ID                uuid.UUID // end_users row: what created_by and published_by record
	ExternalID        string    // the partner's id for them
	AppID             uuid.UUID
	PartnerID         uuid.UUID
	TokenID           uuid.UUID
	Origin            string // the token's bound origin, if any
	AllowedConnectors []string
	AllowedTemplates  []string
	Branding          json.RawMessage
	ExpiresAt         time.Time
	// WhiteLabel: the app leaves out the platform's branding (the app's
	// flag, while its partner holds the white_label capability).
	WhiteLabel bool
}

// Actor is how audit and run records name an end user.
func (e *EndUser) Actor() string { return "end_user:" + e.AppID.String() + "/" + e.ExternalID }

// embedRoutes mounts /v1/embed/{app}.
func (s *Server) embedRoutes(r chi.Router) {
	r.Use(s.embedCORS)
	r.Group(func(r chi.Router) {
		r.Use(s.embedAuth)
		r.Get("/me", s.embedMe)
		r.Get("/connectors", s.embedConnectors)
		r.With(s.need(PermWorkflowRead)).Get("/workflows", s.listWorkflows)
		r.With(s.need(PermWorkflowEdit), s.embedGuardBody).Post("/workflows", s.createWorkflow)
		r.With(s.need(PermWorkflowRead)).Get("/workflows/{wf}", s.getWorkflow)
		r.With(s.need(PermWorkflowRead)).Get("/workflows/{wf}/versions/{v}", s.getVersion)
		r.With(s.need(PermWorkflowEdit), s.embedGuardBody).Post("/workflows/{wf}/versions", s.createVersion)
		r.With(s.need(PermWorkflowEdit)).Put("/workflows/{wf}/versions/{v}/layout", s.putLayout)
		r.With(s.need(PermWorkflowPublish), s.embedGuardVersion).Post("/workflows/{wf}/versions/{v}/publish", s.publish)
		r.With(s.need(PermWorkflowEdit), s.embedGuardBody).Post("/validate", s.validate)
		r.With(s.need(PermRunStart), s.embedGuardRun).Post("/workflows/{wf}/runs", s.startRun)
		r.With(s.need(PermRunRead)).Get("/runs", s.listRuns)
		r.With(s.need(PermRunRead)).Get("/runs/{run}", s.getRun)
		r.With(s.need(PermRunRead)).Get("/runs/{run}/stream", s.streamRun)
		r.With(s.need(PermRunCancel)).Post("/runs/{run}/cancel", s.cancelRun)
	})
}

// embedCORS answers preflights and sets CORS headers for the app's allowed
// origins only. A request from any other origin gets no CORS headers, so
// browsers withhold the answer from the page; embedAuth refuses it as well.
func (s *Server) embedCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := false
		if origin != "" {
			if app, err := uuid.Parse(chi.URLParam(r, "app")); err == nil {
				var origins []string
				if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_embed_app_origins($1)`, app).Scan(&origins); err == nil {
					allowed = slices.Contains(origins, origin)
				}
			}
		}
		h := w.Header()
		h.Add("Vary", "Origin")
		if allowed {
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Expose-Headers", "Retry-After")
		}
		if r.Method == http.MethodOptions {
			if !allowed {
				writeErr(w, http.StatusForbidden, "origin not allowed")
				return
			}
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, Last-Event-ID")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// embedAuth resolves an end-user token. The token must be for the app in
// the path (its audience); a request with an Origin must come from one of
// the app's allowed origins (and the token's bound origin, if any); one
// without an Origin, from a server, is accepted only for a headless app.
func (s *Server) embedAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		eu, perms, tenant, err := s.resolveEndUser(r)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "a valid end-user token for this app is required")
			return
		}
		origin, err := s.embedOrigin(r)
		if err != nil {
			writeErr(w, http.StatusForbidden, err.Error())
			return
		}
		switch {
		case origin != "" && !slices.Contains(eu.allowedOrigins, origin):
			writeErr(w, http.StatusForbidden, "this origin is not allowed for the app")
			return
		case origin != "" && eu.Origin != "" && origin != eu.Origin:
			writeErr(w, http.StatusForbidden, "the token is bound to another origin")
			return
		case origin == "" && !eu.headless:
			writeErr(w, http.StatusForbidden, "this app's tokens are used from its allowed origins (the app is not headless)")
			return
		}
		p := &Principal{TenantID: tenant, Permissions: perms, EndUser: &eu.EndUser}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	})
}

// embedOrigin is the origin a request to the embed API acts for: its
// Origin header; or, from the frame page (the platform's own origin, or
// the app's custom domain, where only the platform's script runs), the
// partner page that framed it, which the frame names in
// X-Taskiem-Embed-Parent after checking the page's origin itself. A
// same-origin GET carries no Origin header; Sec-Fetch-Site tells it apart
// from a server's call.
func (s *Server) embedOrigin(r *http.Request) (string, error) {
	origin := r.Header.Get("Origin")
	self := s.selfOrigin(r)
	if origin == "" && r.Header.Get("Sec-Fetch-Site") == "same-origin" {
		origin = self
	}
	if origin == "" || origin != self {
		return origin, nil
	}
	parent := r.Header.Get(embedParentHeader)
	if parent == "" || parent == self {
		return "", errors.New("the frame page must name the partner page that framed it")
	}
	return parent, nil
}

type resolvedEndUser struct {
	EndUser
	allowedOrigins []string
	headless       bool
}

func (s *Server) resolveEndUser(r *http.Request) (*resolvedEndUser, map[string]bool, uuid.UUID, error) {
	h := r.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || !strings.HasPrefix(tok, endUserTokenPrefix) {
		return nil, nil, uuid.Nil, errors.New("no end-user token")
	}
	app, err := uuid.Parse(chi.URLParam(r, "app"))
	if err != nil {
		return nil, nil, uuid.Nil, err
	}
	var eu resolvedEndUser
	var tenant uuid.UUID
	var permissions, appPermissions []string
	var origin *string
	err = s.Store.Pool.QueryRow(r.Context(), `SELECT token_id, tenant_id, partner_id, app_id, end_user_id, external_id, permissions, origin,
		allowed_origins, allowed_connectors, allowed_templates, app_permissions, headless, branding, expires_at, white_label
		FROM taskiem_auth_end_user_token($1)`, hashToken(tok)).
		Scan(&eu.TokenID, &tenant, &eu.PartnerID, &eu.AppID, &eu.ID, &eu.ExternalID, &permissions, &origin,
			&eu.allowedOrigins, &eu.AllowedConnectors, &eu.AllowedTemplates, &appPermissions, &eu.headless, &eu.Branding, &eu.ExpiresAt, &eu.WhiteLabel)
	if err != nil {
		return nil, nil, uuid.Nil, err
	}
	if eu.AppID != app {
		return nil, nil, uuid.Nil, errors.New("token for another app")
	}
	if origin != nil {
		eu.Origin = *origin
	}
	// The token's permissions, within what the app allows now (a partner
	// narrowing its app narrows live tokens at once) and the platform's
	// ceiling for end users (checked again here, whatever the rows say).
	perms := map[string]bool{}
	for _, x := range permissions {
		if slices.Contains(appPermissions, x) && slices.Contains(embed.EndUserCeiling, x) {
			perms[x] = true
		}
	}
	return &eu, perms, tenant, nil
}

func (s *Server) embedMe(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	eu := p.EndUser
	perms := make([]string, 0, len(p.Permissions))
	for k := range p.Permissions {
		perms = append(perms, k)
	}
	slices.Sort(perms)
	writeJSON(w, http.StatusOK, map[string]any{
		"end_user":           map[string]any{"id": eu.ID, "external_id": eu.ExternalID},
		"actor":              eu.Actor(),
		"app_id":             eu.AppID,
		"sub_tenant_id":      p.TenantID,
		"permissions":        perms,
		"allowed_connectors": nonNil(eu.AllowedConnectors),
		"allowed_templates":  nonNil(eu.AllowedTemplates),
		"branding":           eu.Branding,
		"white_label":        eu.WhiteLabel,
		"expires_at":         eu.ExpiresAt,
	})
}

// embedConnectors lists the connectors the app allows its end users.
func (s *Server) embedConnectors(w http.ResponseWriter, r *http.Request) {
	allowed := principalFrom(r.Context()).EndUser.AllowedConnectors
	rec := &bufferedWriter{header: http.Header{}}
	s.listConnectors(rec, r)
	if rec.status != http.StatusOK {
		w.WriteHeader(rec.status)
		_, _ = w.Write(rec.body.Bytes())
		return
	}
	var all struct {
		Connectors []connectorInfo `json:"connectors"`
	}
	if err := json.Unmarshal(rec.body.Bytes(), &all); err != nil {
		s.fail(w, r, err)
		return
	}
	out := []connectorInfo{}
	for _, c := range all.Connectors {
		if slices.Contains(allowed, c.ID) {
			out = append(out, c)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"connectors": out, "step_types": embedStepTypes(allowed)})
}

// embedStepTypes are the gated step types (http, code, ai) the app allows.
func embedStepTypes(allowed []string) []string {
	out := []string{}
	for _, t := range []string{"http", "code", "ai"} {
		if slices.Contains(allowed, t) {
			out = append(out, t)
		}
	}
	return out
}

type bufferedWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedWriter) Header() http.Header         { return b.header }
func (b *bufferedWriter) Write(p []byte) (int, error) { return b.body.Write(p) }
func (b *bufferedWriter) WriteHeader(status int)      { b.status = status }

// notAllowed refuses a definition using connectors or step types the app
// does not allow its end users.
func notAllowed(w http.ResponseWriter, bad []string) {
	writeJSON(w, http.StatusForbidden, map[string]any{"error": "the app does not allow these connectors or step types: " + strings.Join(bad, ", "), "not_allowed": bad})
}

// checkUses reports what of a definition the end user's app does not allow.
func checkUses(eu *EndUser, doc []byte) ([]string, error) {
	uses, err := embed.Uses(doc)
	if err != nil {
		return nil, err
	}
	return embed.Disallowed(uses, eu.AllowedConnectors), nil
}

// embedGuardBody checks a request carrying a definition (create, save,
// validate) against the app's connectors, and an optional "template"
// against its templates (the field is then removed: the handlers do not
// take it).
func (s *Server) embedGuardBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		eu := principalFrom(r.Context()).EndUser
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err != nil {
			s.fail(w, r, errors.Join(errBadRequest, err))
			return
		}
		var body map[string]json.RawMessage
		if json.Unmarshal(raw, &body) == nil {
			if t, ok := body["template"]; ok {
				var tpl string
				if json.Unmarshal(t, &tpl) != nil || !slices.Contains(eu.AllowedTemplates, tpl) {
					writeErr(w, http.StatusForbidden, "the app does not allow this template")
					return
				}
				delete(body, "template")
				raw, _ = json.Marshal(body)
			}
			if def, ok := body["definition"]; ok {
				bad, err := checkUses(eu, def)
				if err != nil {
					s.fail(w, r, fmt.Errorf("%w: %w", errBadRequest, err))
					return
				}
				if len(bad) > 0 {
					notAllowed(w, bad)
					return
				}
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		r.ContentLength = int64(len(raw))
		next.ServeHTTP(w, r)
	})
}

// embedGuardVersion checks the version being published.
func (s *Server) embedGuardVersion(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wf, v, err := versionParams(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if s.guardStored(w, r, wf, func(tx pgx.Tx) (int, error) { return v, nil }) {
			next.ServeHTTP(w, r)
		}
	})
}

// embedGuardRun checks the version a run would start: the one named, or the
// one deployed in the environment. An app's connectors can narrow after a
// workflow was published, and a run must not use what the app no longer
// allows.
func (s *Server) embedGuardRun(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wf, err := uuid.Parse(chi.URLParam(r, "wf"))
		if err != nil {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err != nil {
			s.fail(w, r, errors.Join(errBadRequest, err))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var req startReq
		_ = json.Unmarshal(raw, &req)
		env := req.Environment
		if env == "" {
			env = "prod"
		}
		if s.guardStored(w, r, wf, func(tx pgx.Tx) (int, error) {
			if req.Version > 0 {
				return req.Version, nil
			}
			return deployedVersion(r.Context(), tx, wf, env)
		}) {
			r.Body = io.NopCloser(bytes.NewReader(raw))
			next.ServeHTTP(w, r)
		}
	})
}

// guardStored checks a stored version against the end user's app; it
// answers and returns false when the version uses what the app does not
// allow. A version that cannot be found is left to the handler.
func (s *Server) guardStored(w http.ResponseWriter, r *http.Request, wf uuid.UUID, version func(pgx.Tx) (int, error)) bool {
	eu := principalFrom(r.Context()).EndUser
	var def []byte
	err := s.tx(r, func(tx pgx.Tx) error {
		v, err := version(tx)
		if err != nil || v == 0 {
			return err
		}
		return tx.QueryRow(r.Context(), `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, v).Scan(&def)
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, r, err)
		return false
	}
	if def == nil {
		return true
	}
	bad, err := checkUses(eu, def)
	if err != nil {
		s.fail(w, r, fmt.Errorf("stored definition: %w", err))
		return false
	}
	if len(bad) > 0 {
		notAllowed(w, bad)
		return false
	}
	return true
}

// Package api is Taskiem's public REST API (spec 2.1, role "api"): used by
// the web app, the CLI, SDKs, and partners.
package api

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/telemetry"
	"github.com/israel-duff/taskiem/engine/wasmconn"
)

// Server serves the API.
type Server struct {
	Store    *runtime.Store
	Vault    *secrets.Vault
	Registry *connector.Registry
	Logger   *slog.Logger
	// AllowSignup enables POST /v1/signup (self-serve tenants; off by default
	// until Phase 4).
	AllowSignup bool
	// SecureCookies sets the Secure flag on session cookies (production).
	SecureCookies bool
	// Ingest receives inbound webhooks (role "edge"); nil omits /hooks.
	Ingest http.Handler
	// TrustProxy takes the client address from the last X-Forwarded-For
	// hop, which the load balancer in front of the API appends. Leave it off
	// when clients connect directly, or they could spoof their address.
	TrustProxy bool
	// Static serves the web app; nil omits it.
	Static http.Handler
	// Egress guards calls to Git hosts; nil uses a default guard.
	Egress *egress.Guard
	// Connectors loads tenants' own WebAssembly connectors; nil refuses
	// uploads.
	Connectors *wasmconn.Source
	// AnchorKey is the public key audit anchors are signed with, published
	// to tenants so they can check anchors themselves.
	AnchorKey ed25519.PublicKey

	limiters sync.Map // ip -> *rate.Limiter, for login attempts
	defs     sync.Map // "workflow/version" -> *wd.Definition
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID, s.realIP, observe, s.recoverer, securityHeaders)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	r.Get("/readyz", s.ready)
	r.Mount("/git-hooks", s.GitHooks())
	if s.Ingest != nil {
		r.Mount("/hooks", s.Ingest)
	}
	r.Route("/v1", func(r chi.Router) {
		r.Use(middleware.SetHeader("Cache-Control", "no-store"))
		r.Post("/auth/login", s.login)
		if s.AllowSignup {
			r.Post("/signup", s.signup)
		}
		r.Group(func(r chi.Router) {
			r.Use(s.authenticate)
			r.Post("/auth/logout", s.logout)
			r.Get("/me", s.me)
			r.Get("/connectors", s.listConnectors)
			r.With(s.need(PermWorkflowRead)).Get("/connector-drift", s.listDrift)
			r.With(s.need(PermConnectionManage)).Post("/connector-drift/acknowledge", s.acknowledgeDrift)
			r.With(s.need(PermWorkflowRead)).Get("/tenant-connectors", s.listTenantConnectors)
			r.With(s.need(PermConnectorManage)).Post("/tenant-connectors", s.uploadTenantConnector)
			r.With(s.need(PermConnectorManage)).Post("/tenant-connectors/{id}/{version}/disable", s.disableTenantConnector)

			r.With(s.need(PermWorkflowRead)).Get("/workflows", s.listWorkflows)
			r.With(s.need(PermWorkflowEdit)).Post("/workflows", s.createWorkflow)
			r.With(s.need(PermWorkflowRead)).Get("/workflows/{wf}", s.getWorkflow)
			r.With(s.need(PermWorkflowRead)).Get("/workflows/{wf}/versions/{v}", s.getVersion)
			r.With(s.need(PermWorkflowEdit)).Post("/workflows/{wf}/versions", s.createVersion)
			r.With(s.need(PermWorkflowEdit)).Put("/workflows/{wf}/versions/{v}/layout", s.putLayout)
			r.With(s.need(PermWorkflowPublish)).Post("/workflows/{wf}/versions/{v}/publish", s.publish)
			r.With(s.need(PermWorkflowRead)).Get("/workflows/{wf}/triggers", s.listTriggers)
			r.With(s.need(PermWorkflowEdit)).Post("/validate", s.validate)
			r.With(s.need(PermWorkflowRead)).Post("/code/generate", s.generateCode)
			r.With(s.need(PermWorkflowEdit)).Post("/code/compile", s.compileCode)

			r.With(s.need(PermRunStart)).Post("/workflows/{wf}/runs", s.startRun)
			r.With(s.need(PermRunRead)).Get("/runs", s.listRuns)
			r.With(s.need(PermRunRead)).Get("/runs/{run}", s.getRun)
			r.With(s.need(PermRunCancel)).Post("/runs/{run}/cancel", s.cancelRun)
			r.With(s.need(PermRunResolve)).Post("/runs/{run}/steps/{step}/resolve", s.resolveStep)

			r.Get("/approvals", s.listApprovals)
			r.With(s.need(PermApprovalDecide)).Post("/approvals/{run}/{step}", s.decide)

			r.With(s.need(PermConnectionManage)).Get("/connections", s.listConnections)
			r.With(s.need(PermConnectionManage)).Post("/connections", s.createConnection)
			r.With(s.need(PermSecretManage)).Get("/secrets", s.listSecrets)
			r.With(s.need(PermSecretManage)).Put("/secrets/{env}/{name}", s.putSecret)
			r.With(s.need(PermSecretManage)).Delete("/secrets/{env}/{name}", s.deleteSecret)
			r.With(s.need(PermWorkflowRead)).Get("/variables", s.listVariables)
			r.With(s.need(PermSecretManage)).Put("/variables/{env}/{name}", s.putVariable)
			r.With(s.need(PermSecretManage)).Get("/egress", s.listEgress)
			r.With(s.need(PermSecretManage)).Post("/egress", s.allowEgress)

			r.With(s.need(PermMemberManage)).Get("/members", s.listMembers)
			r.With(s.need(PermMemberManage)).Post("/members", s.addMember)
			r.With(s.need(PermMemberManage)).Delete("/members/{user}/roles/{role}", s.revokeRole)
			r.Get("/permissions", s.listPermissions)
			r.With(s.need(PermMemberManage)).Get("/roles", s.listRoles)
			r.With(s.need(PermRoleManage)).Put("/roles/{name}", s.putRole)
			r.With(s.need(PermRoleManage)).Delete("/roles/{name}", s.deleteRole)
			r.With(s.need(PermMemberManage)).Get("/api-keys", s.listKeys)
			r.With(s.need(PermMemberManage)).Post("/api-keys", s.createKey)
			r.With(s.need(PermMemberManage)).Delete("/api-keys/{id}", s.revokeKey)

			r.With(s.need(PermAuditRead)).Get("/audit", s.listAudit)
			r.With(s.need(PermAuditRead)).Get("/audit/verify", s.verifyAudit)
			r.With(s.need(PermAuditRead)).Get("/audit/export", s.exportAudit)
			r.With(s.need(PermAuditRead)).Get("/audit/anchors", s.listAnchors)
			r.With(s.need(PermAuditRead)).Get("/reports/{kind}", s.getReport)
			r.With(s.need(PermPIIErase)).Post("/pii/erase", s.erase)

			r.Get("/governance", s.getGovernance)
			r.Put("/governance", s.putGovernance)
			r.With(s.need(PermWorkflowRead)).Get("/policies", s.listPolicies)
			r.With(s.need(PermWorkflowRead)).Get("/policies/{name}", s.getPolicy)
			r.With(s.need(PermPolicyManage)).Put("/policies/{name}", s.putPolicy)
			r.With(s.need(PermPolicyManage)).Post("/policies/{name}/versions/{v}/approve", s.decidePolicy(true))
			r.With(s.need(PermPolicyManage)).Post("/policies/{name}/versions/{v}/reject", s.decidePolicy(false))
			r.Get("/delegations", s.listDelegations)
			r.With(s.need(PermApprovalDecide)).Post("/delegations", s.createDelegation)
			r.Delete("/delegations/{id}", s.revokeDelegation)
			r.Post("/me/totp", s.beginTOTP)
			r.Post("/me/totp/confirm", s.confirmTOTP)
			r.Delete("/me/totp", s.removeTOTP)
			r.With(s.need(PermWorkflowPublish)).Get("/publish-requests", s.listPublishRequests)
			r.With(s.need(PermWorkflowPublish)).Post("/workflows/{wf}/versions/{v}/publish/approve", s.decidePublish(true))
			r.With(s.need(PermWorkflowPublish)).Post("/workflows/{wf}/versions/{v}/publish/reject", s.decidePublish(false))

			r.With(s.need(PermGitManage)).Get("/git", s.listGit)
			r.With(s.need(PermGitManage)).Put("/git/{env}", s.putGit)
			r.With(s.need(PermGitManage)).Delete("/git/{env}", s.deleteGit)
			r.With(s.need(PermWorkflowPublish)).Post("/git/{env}/sync", s.syncNow)
			r.With(s.need(PermWorkflowRead)).Get("/git/{env}/syncs", s.listSyncs)
			r.With(s.need(PermWorkflowPublish)).Post("/workflows/{wf}/versions/{v}/git-request", s.retryProposal)
		})
	})
	if s.Static != nil {
		r.Handle("/*", s.Static)
	}
	return r
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Store.Pool.Ping(ctx); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) realIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.TrustProxy {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				hops := strings.Split(xff, ",")
				if ip := net.ParseIP(strings.TrimSpace(hops[len(hops)-1])); ip != nil {
					r.RemoteAddr = net.JoinHostPort(ip.String(), "0")
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// observe records a span and metrics per request, labelled by route
// pattern (never by raw path, which carries ids).
func observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx, span := telemetry.Tracer().Start(r.Context(), "http "+r.Method)
		defer span.End()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r.WithContext(ctx))
		route := "unmatched"
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		span.SetName(r.Method + " " + route)
		span.SetAttributes(attribute.Int("http.status_code", ww.Status()))
		telemetry.HTTPRequests.WithLabelValues(route, r.Method, strconv.Itoa(ww.Status())).Inc()
		telemetry.HTTPSeconds.WithLabelValues(route).Observe(time.Since(start).Seconds())
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.Logger.Error("panic in handler", "path", r.URL.Path, "panic", v)
				writeErr(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// --- JSON helpers ---

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

// fail maps engine errors to responses without leaking internals.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, runtime.ErrNotFound), errors.Is(err, secrets.ErrNotFound), errors.Is(err, pgx.ErrNoRows):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, errBadRequest):
		writeErr(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "bad request: "))
	case errors.Is(err, errForbidden):
		writeErr(w, http.StatusForbidden, strings.TrimPrefix(err.Error(), "forbidden: "))
	case errors.Is(err, errConflict):
		writeErr(w, http.StatusConflict, strings.TrimPrefix(err.Error(), "conflict: "))
	default:
		s.Logger.Error("request failed", "path", r.URL.Path, "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
	}
}

var (
	errBadRequest = errors.New("bad request")
	errForbidden  = errors.New("forbidden")
	errConflict   = errors.New("conflict")
)

func decodeBody(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.Join(errBadRequest, err)
	}
	return nil
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) loginLimiter(ip string) *rate.Limiter {
	l, _ := s.limiters.LoadOrStore(ip, rate.NewLimiter(rate.Every(6*time.Second), 10))
	return l.(*rate.Limiter)
}

// tx runs fn in a transaction scoped to the caller's tenant.
func (s *Server) tx(r *http.Request, fn func(pgx.Tx) error) error {
	p := principalFrom(r.Context())
	return db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{p.TenantID}, fn)
}

// auditTx appends an audit entry for the caller.
func auditTx(r *http.Request, tx pgx.Tx, action, target string, detail map[string]any) error {
	p := principalFrom(r.Context())
	if detail == nil {
		detail = map[string]any{}
	}
	detail["ip"] = clientIP(r)
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(r.Context(), `SELECT taskiem_audit_append($1, $2, $3, $4, $5, $6)`, p.TenantID, p.ActorType(), p.Actor(), action, target, raw)
	return err
}

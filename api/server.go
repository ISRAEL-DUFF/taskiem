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
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/billing"
	"github.com/israel-duff/taskiem/engine/catalogue"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/httpsec"
	"github.com/israel-duff/taskiem/engine/lru"
	"github.com/israel-duff/taskiem/engine/remote"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/telemetry"
	"github.com/israel-duff/taskiem/engine/wasmconn"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/webauthn"
	"github.com/israel-duff/taskiem/engine/whatsapp"
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
	// SignupPerAddress is how many signups one client address may make a
	// day, across every replica (TASKIEM_SIGNUP_PER_ADDRESS); 0 is the
	// default (5), negative is no limit.
	SignupPerAddress int
	// SignupBlockedDomains are email domains (and their subdomains) that
	// may not sign up, besides the built-in throwaway services
	// (TASKIEM_SIGNUP_BLOCKED_DOMAINS).
	SignupBlockedDomains []string
	// DocsURL is where the web app's help links point (TASKIEM_DOCS_URL);
	// empty shows the in-product help only.
	DocsURL string
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
	// EmbedDir holds the embedded builder's bundle (taskiem.js, served at
	// /embed/v1/taskiem.js; docs/embedding.md); empty serves none.
	EmbedDir string
	// Egress guards calls to Git hosts; nil uses a default guard.
	Egress *egress.Guard
	// Connectors loads tenants' own WebAssembly connectors; nil refuses
	// uploads.
	Connectors *wasmconn.Source
	// Catalogue runs the automated checks on connector packages submitted
	// to the public catalogue; nil refuses submissions (browsing and
	// installing still work).
	Catalogue *catalogue.Checker
	// PublicURL is where people reach the web app (TASKIEM_PUBLIC_URL);
	// single sign-on callbacks are built from it.
	PublicURL string
	// LoginBurst is how many sign-in attempts an address may make at once
	// (then one every six seconds); default 10.
	LoginBurst int
	// WebAuthn is the relying party for passkeys; empty turns them off.
	WebAuthn webauthn.Config
	// RequireAdminPasskeys holds members with administrative permissions to
	// passkeys (spec 13.2: on by default in production).
	RequireAdminPasskeys bool
	// Done, when closed, ends long-lived streams so shutdown is prompt.
	Done <-chan struct{}
	// Alerts sends channels' test messages; nil refuses them.
	Alerts *alerts.Alerter
	// AI drafts workflows from goals (spec 12, docs/ai.md); nil turns AI
	// building off (503 on /v1/ai/build).
	AI *AISettings
	// AnchorKey is the public key audit anchors are signed with, published
	// to tenants so they can check anchors themselves.
	AnchorKey ed25519.PublicKey
	// WhatsApp is the platform number (spec 11); nil turns the channel off.
	WhatsApp *whatsapp.Platform
	// WhatsAppPublic answers numbers bound to no one; nil sends how to link.
	WhatsAppPublic WhatsAppPublic
	// USSD tunes the USSD fast path (ussd.go).
	USSD USSDSettings
	// Billing is plans, subscriptions and payments (billing.go); nil or
	// not enabled is billing off: the internal plan, every feature.
	Billing *billing.Service
	// Remote applies remotely registered triggers' subscriptions at their
	// providers right after a publish or undeploy (decision 0021); the
	// scheduler retries what it cannot finish. Nil leaves it all to the
	// scheduler.
	Remote *remote.Reconciler
	// CodeLimits bound tenant code compiled or checked in this process
	// (codegate.go).
	CodeLimits CodeLimits
	// HSTS is the Strict-Transport-Security value sent on the platform's
	// own host (TASKIEM_HSTS); empty sends none.
	HSTS string

	code     codeGate                          // tenant code admitted at once (codegate.go)
	ussd     ussdState                         // USSD channels, sessions and menus (ussd.go)
	limiters limiterSet                        // sign-in and other unauthenticated attempts
	hosts    hostCache                         // custom domains: Host to embed app
	defs     lru.Cache[string, *wd.Definition] // "workflow/version"; versions are immutable, bounded (S34)
	bg       sync.WaitGroup
	bgActive atomic.Int64 // work running after its request was answered (reset emails)
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID, s.realIP, observe, s.recoverer, securityHeaders, s.hsts, s.customDomains)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	r.Get("/readyz", s.ready)
	r.Mount("/git-hooks", s.GitHooks())
	r.Mount("/scim/v2", s.SCIM())
	r.Route("/embed", s.embedPages) // the embedded builder's bundle and frame page (embedframe.go)
	if s.Ingest != nil {
		r.Mount("/hooks", s.Ingest)
		if s.WhatsApp != nil {
			r.Mount("/channels/whatsapp", s.WhatsAppHooks())
		}
		r.Mount("/channels/ussd", s.USSDHooks())
	}
	r.Route("/v1", func(r chi.Router) {
		r.Use(middleware.SetHeader("Cache-Control", "no-store"))
		r.Post("/auth/login", s.login)
		r.Post("/auth/passkey/options", s.passkeyLoginOptions)
		r.Post("/auth/passkey", s.passkeyLogin)
		r.Post("/auth/password/forgot", s.forgotPassword)
		r.Post("/auth/password/reset", s.resetPassword)
		r.Post("/auth/sso/discover", s.ssoDiscover)
		r.Get("/auth/sso/{id}/start", s.ssoStart)
		r.Get("/auth/sso/oidc/callback", s.oidcCallback)
		r.Post("/auth/sso/saml/acs", s.samlACSHandler)
		r.Get("/auth/sso/saml/{id}/metadata", s.samlMetadata)
		r.Get("/signup", s.signupOptions)
		if s.AllowSignup {
			r.Post("/signup", s.signup) // self-serve signup (onboarding.go)
		}
		r.Post("/billing/webhooks/{provider}", s.billingWebhook) // payment providers (billing.go)
		// End users of embed apps: their own tokens, CORS (embed.go).
		r.Route("/embed/{app}", s.embedRoutes)
		r.Group(func(r chi.Router) {
			r.Use(s.authenticate)
			r.With(s.partnerPlan).Route("/partner", s.partnerRoutes) // partner admin API (partner.go)
			r.Route("/billing", s.billingRoutes)                     // plans and subscriptions (billing.go)
			r.Route("/keys", s.keyRoutes)                            // encryption keys and BYOK (keys.go)
			r.Post("/auth/logout", s.logout)
			r.Get("/me", s.me)
			r.Post("/me/password", s.changePassword)
			r.Post("/me/email/verify", s.verifyEmail)
			r.Post("/me/email/verify/resend", s.resendVerification)
			r.Get("/onboarding", s.getOnboarding) // the getting-started checklist (onboarding.go)
			r.With(s.need(PermWorkflowEdit)).Post("/onboarding/dismiss", s.dismissOnboarding)
			r.Get("/me/invitations", s.myInvitations)
			r.Post("/me/invitations/{tenant}/accept", s.acceptInvitation)
			r.Post("/me/invitations/{tenant}/decline", s.declineInvitation)
			r.Get("/me/passkeys", s.listPasskeys)
			r.Post("/me/passkeys/options", s.passkeyRegisterOptions)
			r.Post("/me/passkeys", s.passkeyRegister)
			r.Delete("/me/passkeys/{id}", s.removePasskey)
			r.Post("/me/step-up/options", s.stepUpOptions)
			r.Get("/me/whatsapp", s.getWhatsApp)
			r.Post("/me/whatsapp", s.startWhatsAppBinding)
			r.Post("/me/whatsapp/verify", s.verifyWhatsAppBinding)
			r.Delete("/me/whatsapp", s.unbindWhatsApp)
			r.Put("/me/whatsapp/pin", s.setWhatsAppPin)
			r.Delete("/me/whatsapp/pin", s.removeWhatsAppPin)
			r.With(s.need(PermSecretManage)).Get("/whatsapp/number", s.getWhatsAppNumber)
			r.With(s.need(PermSecretManage), s.tenantWide).Put("/whatsapp/number", s.putWhatsAppNumber)
			r.With(s.need(PermSecretManage), s.tenantWide).Delete("/whatsapp/number", s.deleteWhatsAppNumber)
			r.With(s.need(PermWorkflowPublish), s.tenantWide).Put("/whatsapp/public-menu", s.putWhatsAppPublicMenu)
			r.With(s.need(PermAlertManage)).Get("/whatsapp/outbox", s.listWhatsAppOutbox)
			r.With(s.need(PermAlertManage), s.tenantWide).Post("/whatsapp/outbox/{id}/retry", s.retryWhatsAppOutbox)
			r.With(s.need(PermApprovalDecide)).Get("/whatsapp/handoff/{token}", s.getHandoff)
			r.With(s.need(PermApprovalDecide)).Post("/whatsapp/handoff/{token}", s.completeHandoff)
			r.With(s.need(PermSecretManage)).Get("/ussd", s.getUSSD)
			r.With(s.need(PermSecretManage), s.tenantWide).Put("/ussd/channels/{provider}", s.putUSSDChannel)
			r.With(s.need(PermSecretManage), s.tenantWide).Delete("/ussd/channels/{provider}", s.deleteUSSDChannel)
			r.With(s.need(PermMemberManage)).Delete("/members/{user}/passkeys", s.resetPasskeys)
			r.With(s.need(PermMemberManage)).Get("/scim", s.getSCIM)
			r.With(s.need(PermMemberManage), s.feature(billing.FeatureSCIM, false)).Put("/scim", s.putSCIM)
			r.With(s.need(PermMemberManage)).Get("/sso", s.listSSO)
			r.With(s.need(PermMemberManage), s.feature(billing.FeatureSSO, false)).Post("/sso", s.createSSO)
			r.With(s.need(PermMemberManage), s.feature(billing.FeatureSSO, false)).Put("/sso/{id}", s.updateSSO)
			r.With(s.need(PermMemberManage), s.feature(billing.FeatureSSO, false)).Post("/sso/{id}/domains", s.addSSODomain)
			r.With(s.need(PermMemberManage)).Post("/sso/domains/{domain}/verify", s.verifySSODomain)
			r.Get("/connectors", s.listConnectors)
			r.With(s.need(PermWorkflowRead)).Get("/connector-drift", s.listDrift)
			r.With(s.need(PermConnectionManage)).Post("/connector-drift/acknowledge", s.acknowledgeDrift)
			r.With(s.need(PermWorkflowRead)).Get("/tenant-connectors", s.listTenantConnectors)
			r.With(s.need(PermConnectorManage), s.tenantCode).Post("/tenant-connectors", s.uploadTenantConnector)
			r.With(s.need(PermConnectorManage)).Post("/tenant-connectors/{id}/{version}/disable", s.disableTenantConnector)
			// The public connector catalogue (docs/connector-submissions.md).
			r.With(s.need(PermWorkflowRead)).Get("/catalogue", s.listCatalogue)
			r.With(s.need(PermWorkflowRead)).Get("/catalogue/connectors/{id}/{version}", s.getCatalogueVersion)
			r.With(s.need(PermWorkflowRead)).Get("/catalogue/installs", s.listInstalls)
			r.With(s.need(PermConnectorManage), s.tenantWide).Post("/catalogue/installs", s.installConnector)
			r.With(s.need(PermConnectorManage)).Get("/catalogue/installs/{id}/{major}/upgrade", s.getUpgrade)
			r.With(s.need(PermConnectorManage), s.tenantWide).Post("/catalogue/installs/{id}/{major}/upgrade", s.upgradeInstall)
			r.With(s.need(PermConnectorManage), s.tenantWide).Delete("/catalogue/installs/{id}/{major}", s.uninstallConnector)
			r.With(s.need(PermConnectorManage)).Get("/catalogue/publisher", s.getPublisher)
			r.With(s.need(PermConnectorManage), s.tenantWide).Put("/catalogue/publisher", s.putPublisher)
			r.With(s.need(PermConnectorManage)).Get("/catalogue/submissions", s.listSubmissions)
			r.With(s.need(PermConnectorManage), s.tenantWide, s.tenantCode).Post("/catalogue/submissions", s.submitPackage)
			r.With(s.need(PermConnectorManage)).Get("/catalogue/submissions/{id}", s.getSubmission)
			r.With(s.need(PermConnectorManage), s.tenantWide).Post("/catalogue/submissions/{id}/withdraw", s.moveSubmission("withdrawn"))
			r.With(s.need(PermConnectorManage), s.tenantWide).Post("/catalogue/submissions/{id}/publish", s.moveSubmission("published"))
			r.With(s.need(PermConnectorManage), s.tenantWide).Post("/catalogue/submissions/{id}/revoke", s.moveSubmission("revoked"))

			r.With(s.need(PermWorkflowRead)).Get("/workflows", s.listWorkflows)
			r.With(s.need(PermWorkflowEdit), s.tenantCode).Post("/workflows", s.createWorkflow)
			r.With(s.need(PermWorkflowRead)).Get("/workflows/{wf}", s.getWorkflow)
			r.With(s.need(PermWorkflowRead), s.tenantCode).Get("/workflows/{wf}/versions/{v}", s.getVersion)
			r.With(s.need(PermWorkflowEdit), s.tenantCode).Post("/workflows/{wf}/versions", s.createVersion)
			r.With(s.need(PermWorkflowEdit)).Put("/workflows/{wf}/versions/{v}/layout", s.putLayout)
			r.With(s.need(PermWorkflowPublish), s.tenantWide, s.tenantCode).Post("/workflows/{wf}/versions/{v}/publish", s.publish)
			r.With(s.need(PermWorkflowRead)).Get("/workflows/{wf}/triggers", s.listTriggers)
			r.With(s.need(PermWorkflowPublish), s.tenantCode).Post("/workflows/{wf}/promote", s.promote)
			r.With(s.need(PermWorkflowPublish)).Delete("/workflows/{wf}/deployments/{env}", s.undeploy)
			r.With(s.need(PermWorkflowRead)).Get("/environments", s.listEnvironments)
			r.With(s.need(PermAlertManage)).Get("/alerts", s.listAlerts)
			r.With(s.need(PermAlertManage)).Get("/alerts/channels", s.listAlertChannels)
			r.With(s.need(PermAlertManage)).Post("/alerts/channels", s.createAlertChannel)
			r.With(s.need(PermAlertManage)).Delete("/alerts/channels/{id}", s.deleteAlertChannel)
			r.With(s.need(PermAlertManage)).Post("/alerts/channels/{id}/test", s.testAlertChannel)
			r.With(s.need(PermAlertManage)).Get("/alerts/rules", s.listAlertRules)
			r.With(s.need(PermAlertManage)).Post("/alerts/rules", s.createAlertRule)
			r.With(s.need(PermAlertManage)).Put("/alerts/rules/{id}", s.updateAlertRule)
			r.With(s.need(PermAlertManage)).Delete("/alerts/rules/{id}", s.deleteAlertRule)
			r.With(s.need(PermWorkflowPublish), s.tenantWide).Post("/environments", s.createEnvironment)
			r.With(s.need(PermWorkflowPublish), s.tenantWide).Put("/environments/{env}", s.putEnvironment)
			r.With(s.need(PermWorkflowEdit), s.tenantCode).Post("/validate", s.validate)
			r.With(s.feature(billing.FeatureAI, true)).Route("/ai", s.aiRoutes)
			r.Get("/templates", s.listTemplates) // the SME template library (templates.go)
			r.With(s.tenantCode).Get("/templates/{id}", s.getTemplate)
			r.With(s.need(PermWorkflowEdit), s.tenantCode).Post("/templates/{id}/instantiate", s.instantiateTemplate)
			r.With(s.need(PermWorkflowRead), s.tenantCode).Post("/code/generate", s.generateCode)
			r.With(s.need(PermWorkflowEdit), s.tenantCode).Post("/code/compile", s.compileCode)

			r.With(s.need(PermRunStart)).Post("/workflows/{wf}/runs", s.startRun)
			r.With(s.need(PermRunRead)).Get("/runs", s.listRuns)
			r.With(s.need(PermRunRead)).Get("/dashboard", s.dashboard)
			r.With(s.need(PermRunRead)).Get("/runs/{run}", s.getRun)
			r.With(s.need(PermRunRead)).Get("/runs/{run}/stream", s.streamRun)
			r.With(s.need(PermRunCancel)).Post("/runs/{run}/cancel", s.cancelRun)
			r.With(s.need(PermRunResolve)).Post("/runs/{run}/steps/{step}/resolve", s.resolveStep)
			r.Group(s.repairRoutes) // self-repair proposals (repair.go)

			r.Get("/approvals", s.listApprovals)
			r.With(s.need(PermApprovalDecide)).Post("/approvals/{run}/{step}", s.decide)

			r.With(s.need(PermConnectionManage)).Get("/connections", s.listConnections)
			r.With(s.need(PermConnectionManage)).Post("/connections", s.createConnection)
			r.With(s.need(PermSecretManage)).Get("/secrets", s.listSecrets)
			r.With(s.need(PermAuditRead)).Get("/secrets/reads", s.listSecretReads)
			r.With(s.need(PermSecretManage)).Put("/secrets/{env}/{name}", s.putSecret)
			r.With(s.need(PermSecretManage)).Delete("/secrets/{env}/{name}", s.deleteSecret)
			r.With(s.need(PermWorkflowRead)).Get("/variables", s.listVariables)
			r.With(s.need(PermSecretManage)).Put("/variables/{env}/{name}", s.putVariable)
			r.With(s.need(PermSecretManage)).Get("/egress", s.listEgress)
			r.With(s.need(PermSecretManage)).Post("/egress", s.allowEgress)

			r.With(s.need(PermMemberManage)).Get("/members", s.listMembers)
			r.With(s.need(PermMemberManage), s.tenantWide).Post("/members", s.addMember)
			r.With(s.need(PermMemberManage)).Get("/invitations", s.listInvitations)
			r.With(s.need(PermMemberManage), s.tenantWide).Delete("/invitations/{user}", s.cancelInvitation)
			r.With(s.need(PermMemberManage), s.tenantWide).Delete("/members/{user}/roles/{role}", s.revokeRole)
			r.Get("/permissions", s.listPermissions)
			r.Get("/limits", s.getLimits) // read-only: operators set limits from the CLI
			r.With(s.need(PermMemberManage)).Get("/roles", s.listRoles)
			r.With(s.need(PermRoleManage), s.tenantWide, s.feature(billing.FeatureCustomRoles, false)).Put("/roles/{name}", s.putRole)
			r.With(s.need(PermRoleManage), s.tenantWide).Delete("/roles/{name}", s.deleteRole)
			r.With(s.need(PermMemberManage)).Get("/api-keys", s.listKeys)
			r.With(s.need(PermMemberManage)).Post("/api-keys", s.createKey)
			r.With(s.need(PermMemberManage)).Delete("/api-keys/{id}", s.revokeKey)

			r.With(s.need(PermAuditRead), s.tenantWide).Get("/audit", s.listAudit)
			r.With(s.need(PermAuditRead), s.tenantWide).Get("/audit/verify", s.verifyAudit)
			r.With(s.need(PermAuditRead), s.tenantWide).Get("/audit/export", s.exportAudit)
			r.With(s.need(PermAuditRead), s.tenantWide).Get("/audit/anchors", s.listAnchors)
			r.With(s.need(PermAuditRead), s.tenantWide).Get("/reports/{kind}", s.getReport)
			r.With(s.need(PermPIIErase), s.tenantWide).Post("/pii/erase", s.erase)

			r.Get("/governance", s.getGovernance)
			r.Put("/governance", s.putGovernance)
			r.With(s.need(PermWorkflowRead)).Get("/policies", s.listPolicies)
			r.With(s.need(PermWorkflowRead)).Get("/policies/{name}", s.getPolicy)
			r.With(s.need(PermPolicyManage), s.tenantWide).Put("/policies/{name}", s.putPolicy)
			r.With(s.need(PermPolicyManage)).Post("/policies/{name}/versions/{v}/approve", s.decidePolicy(true))
			r.With(s.need(PermPolicyManage)).Post("/policies/{name}/versions/{v}/reject", s.decidePolicy(false))
			r.Get("/delegations", s.listDelegations)
			r.With(s.need(PermApprovalDecide)).Post("/delegations", s.createDelegation)
			r.Delete("/delegations/{id}", s.revokeDelegation)
			r.Post("/me/totp", s.beginTOTP)
			r.Post("/me/totp/confirm", s.confirmTOTP)
			r.Delete("/me/totp", s.removeTOTP)
			r.With(s.need(PermWorkflowPublish)).Get("/publish-requests", s.listPublishRequests)
			r.With(s.need(PermWorkflowPublish), s.tenantWide).Post("/workflows/{wf}/versions/{v}/publish/approve", s.decidePublish(true))
			r.With(s.need(PermWorkflowPublish)).Post("/workflows/{wf}/versions/{v}/publish/reject", s.decidePublish(false))

			r.With(s.need(PermGitManage)).Get("/git", s.listGit)
			r.With(s.need(PermGitManage), s.tenantWide, s.feature(billing.FeatureGit, false)).Put("/git/{env}", s.putGit)
			r.With(s.need(PermGitManage), s.tenantWide).Delete("/git/{env}", s.deleteGit)
			r.With(s.need(PermGitManage), s.tenantWide).Post("/git/{env}/approve", s.decideGit(true))
			r.With(s.need(PermGitManage), s.tenantWide).Post("/git/{env}/reject", s.decideGit(false))
			r.With(s.need(PermWorkflowPublish), s.tenantWide).Post("/git/{env}/sync", s.syncNow)
			r.With(s.need(PermWorkflowRead)).Get("/git/{env}/syncs", s.listSyncs)
			r.With(s.need(PermWorkflowPublish), s.tenantWide).Post("/workflows/{wf}/versions/{v}/git-request", s.retryProposal)
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

func securityHeaders(next http.Handler) http.Handler { return httpsec.Headers(next) }

// hsts sends Strict-Transport-Security (S35) on the platform's own host:
// the public URL's host, or any host when no public URL is set. A
// partner's custom domain gets none; it is the partner's to commit.
func (s *Server) hsts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.HSTS != "" {
			host := ""
			if u, err := url.Parse(s.PublicURL); err == nil {
				host = u.Hostname()
			}
			h, _, err := net.SplitHostPort(r.Host)
			if err != nil {
				h = r.Host
			}
			if host == "" || strings.EqualFold(h, host) {
				w.Header().Set("Strict-Transport-Security", s.HSTS)
			}
		}
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
	if le, ok := runtime.IsLimit(err); ok && le.Code == "billing_degraded" {
		// Unpaid or cancelled: payment, not waiting, lifts it.
		writeJSON(w, http.StatusPaymentRequired, map[string]string{"error": le.Message, "code": le.Code, "limit": le.Limit})
		return
	}
	if le, ok := runtime.IsLimit(err); ok {
		// A plan limit: 429 with a code clients can act on, and Retry-After
		// when waiting helps (a quota resets, a backlog drains).
		if le.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(le.RetryAfter.Round(time.Second).Seconds())))
		}
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": le.Message, "code": le.Code, "limit": le.Limit})
		return
	}
	switch {
	case errors.Is(err, runtime.ErrNotFound), errors.Is(err, secrets.ErrNotFound), errors.Is(err, pgx.ErrNoRows):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, runtime.ErrTenantSuspended):
		writeErr(w, http.StatusLocked, err.Error())
	case errors.Is(err, secrets.ErrKeyUnavailable):
		// The tenant's customer key is revoked, disabled or unreachable (or
		// the KMS is down): nothing can be encrypted or decrypted for it.
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the tenant's encryption key is unavailable: " + err.Error(), "code": "key_unavailable"})
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

// loginLimiter paces sign-in attempts by key: "login:" and an address, or
// "login-email:" and an email.
func (s *Server) loginLimiter(key string) *rate.Limiter {
	burst := s.LoginBurst
	if burst <= 0 {
		burst = 10
	}
	return s.limiter(key, 6*time.Second, burst)
}

// limiterSet holds token buckets by key while they matter: a bucket idle
// long enough to have refilled is dropped (a new one is the same), and
// once maxLimiters are in use, new keys of a kind share one bucket, so a
// flood of keys can neither exhaust memory nor refill anyone's bucket.
type limiterSet struct {
	mu     sync.Mutex
	m      map[string]*limiterEntry
	swept  time.Time
	shared map[string]*rate.Limiter // kind (the key up to ':') -> overflow bucket
}

type limiterEntry struct {
	l    *rate.Limiter
	last time.Time
	full time.Duration // how long an empty bucket takes to refill
}

const maxLimiters = 100_000

// limiter is a token bucket per key: one token every interval, up to burst.
func (s *Server) limiter(key string, every time.Duration, burst int) *rate.Limiter {
	ls := &s.limiters
	ls.mu.Lock()
	defer ls.mu.Unlock()
	now := time.Now()
	if e, ok := ls.m[key]; ok {
		e.last = now
		return e.l
	}
	if ls.m == nil {
		ls.m, ls.shared = map[string]*limiterEntry{}, map[string]*rate.Limiter{}
	}
	if len(ls.m) >= maxLimiters || now.Sub(ls.swept) > time.Minute {
		for k, e := range ls.m {
			if now.Sub(e.last) > e.full {
				delete(ls.m, k)
			}
		}
		ls.swept = now
	}
	if len(ls.m) >= maxLimiters {
		kind, _, _ := strings.Cut(key, ":")
		l, ok := ls.shared[kind]
		if !ok {
			l = rate.NewLimiter(rate.Every(every), burst)
			ls.shared[kind] = l
		}
		return l
	}
	e := &limiterEntry{l: rate.NewLimiter(rate.Every(every), burst), last: now, full: max(every*time.Duration(burst), time.Minute)}
	ls.m[key] = e
	return e.l
}

// tx runs fn in a transaction scoped to the caller's tenant.
func (s *Server) tx(r *http.Request, fn func(pgx.Tx) error) error {
	p := principalFrom(r.Context())
	return db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{p.TenantID}, fn)
}

// readTx runs fn read-only for the caller's tenant on the read replica
// when there is one keeping up (decision 0024): only for views that may be
// seconds stale, never before a write. fn may run twice.
func (s *Server) readTx(r *http.Request, fn func(pgx.Tx) error) error {
	return s.Store.ReadTx(r.Context(), principalFrom(r.Context()).TenantID, fn)
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

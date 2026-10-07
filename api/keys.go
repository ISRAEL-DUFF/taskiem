package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/israel-duff/taskiem/engine/billing"
	"github.com/israel-duff/taskiem/engine/byok"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// Encryption keys (spec 14.1; decision 0019; docs/byok.md): the tenant key
// hierarchy, rotation, and bring your own key. Credentials for a customer
// key go in and are never returned; they are sealed by the platform's KMS
// key, outside the tenant vault, so no workflow can name them.

// PermKeyManage lets a member see the tenant's keys, rotate the tenant key,
// and bring, check, replace or remove a customer key. Owners hold it.
const PermKeyManage = "key.manage"

func init() {
	allPermissions = append(allPermissions, PermKeyManage)
	rolePermissions["owner"] = allPermissions
	permissionInfo[PermKeyManage] = "Manage encryption keys: rotate the tenant key, bring your own key (customer KMS), check and replace its credentials"
}

// keyRoutes mounts /v1/keys inside the authenticated group.
func (s *Server) keyRoutes(r chi.Router) {
	r.Use(s.need(PermKeyManage))
	r.Get("/", s.getKeys)
	r.With(s.tenantWide).Post("/rotate", s.rotateKey)
	// Onboarding needs the plan feature; keeping an existing key working
	// (credentials, checks) and leaving it do not, so a tenant can always
	// reach its data and return to the platform key.
	r.With(s.tenantWide, s.feature(billing.FeatureBYOK, false)).Put("/byok", s.putBYOK)
	r.With(s.tenantWide).Put("/byok/credentials", s.putBYOKCredentials)
	r.With(s.tenantWide).Post("/byok/check", s.checkBYOK)
	r.With(s.tenantWide).Delete("/byok", s.deleteBYOK)
}

func (s *Server) getKeys(w http.ResponseWriter, r *http.Request) {
	st, err := s.Vault.KeyStatus(r.Context(), principalFrom(r.Context()).TenantID)
	if err != nil {
		s.keyFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": st, "providers": byok.Providers,
		"plan_allows_byok": s.Billing.Require(r.Context(), principalFrom(r.Context()).TenantID, billing.FeatureBYOK) == nil})
}

func (s *Server) rotateKey(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	ver, err := s.Vault.Rotate(r.Context(), p.TenantID, p.Actor())
	if err != nil {
		s.keyFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"version": ver, "rewrap": "queued"})
}

// byokRequest is a customer key's location and the credentials to reach it.
type byokRequest struct {
	byok.Config
	Credentials map[string]string `json:"credentials"`
}

func (s *Server) putBYOK(w http.ResponseWriter, r *http.Request) {
	var req byokRequest
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	key, ver, err := s.Vault.EnableBYOK(r.Context(), p.TenantID, req.Config, req.Credentials, p.Actor())
	if err != nil {
		s.keyFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"byok": key, "version": ver, "rewrap": "queued"})
}

func (s *Server) putBYOKCredentials(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Credentials map[string]string `json:"credentials"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	key, err := s.Vault.ReplaceBYOKCredentials(r.Context(), p.TenantID, req.Credentials, p.Actor())
	if err != nil {
		s.keyFail(w, r, err)
		return
	}
	// New credentials may be what brings a key back.
	s.checkAndResume(w, r, map[string]any{"byok": key})
}

func (s *Server) checkBYOK(w http.ResponseWriter, r *http.Request) {
	s.checkAndResume(w, r, map[string]any{})
}

// checkAndResume checks the tenant's key and, when it works, resumes steps
// parked while it did not (the key job would within a minute).
func (s *Server) checkAndResume(w http.ResponseWriter, r *http.Request, out map[string]any) {
	p := principalFrom(r.Context())
	h, err := s.Vault.CheckKey(r.Context(), p.TenantID, p.Actor())
	if err != nil {
		s.keyFail(w, r, err)
		return
	}
	out["health"] = h
	if h.OK {
		n, err := s.Store.ResumeKeyParked(r.Context(), p.TenantID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out["resumed_steps"] = n
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteBYOK(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	ver, err := s.Vault.DisableBYOK(r.Context(), p.TenantID, p.Actor())
	if err != nil {
		s.keyFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": ver, "rewrap": "queued"})
}

// keyFail maps key errors: 400 for a configuration problem, 422 when the
// customer key did not complete its round trip, 404 with no customer key,
// 503 when the tenant key cannot be used.
func (s *Server) keyFail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, byok.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": strings.TrimPrefix(err.Error(), byok.ErrInvalid.Error()+": "), "code": "invalid_key_config"})
	case errors.Is(err, secrets.ErrVerify):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "code": "key_verification_failed"})
	case errors.Is(err, secrets.ErrNoBYOK):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no customer key is in use", "code": "no_customer_key"})
	default:
		s.fail(w, r, err)
	}
}

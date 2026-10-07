package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/embed"
)

// Custom domains for embed apps (spec 13.4; docs/embedding.md#custom-domains):
// a partner serves the embedded builder, its bundle and the embed API on
// its own host name, proven by a DNS TXT record as SSO domains are. TLS
// for the host is the ingress's (cert-manager); the application never
// issues certificates.

type appDomain struct {
	Domain     string     `json:"domain"`
	Record     string     `json:"record"`    // the TXT record's name
	TXTValue   string     `json:"txt_value"` // its value
	VerifiedAt *time.Time `json:"verified_at"`
}

// partnerCapabilities are the capabilities the operator granted the
// partner in scope.
func partnerCapabilities(r *http.Request, tx pgx.Tx) ([]string, error) {
	var caps []string
	err := tx.QueryRow(r.Context(), `SELECT capabilities FROM partners WHERE tenant_id = $1`, principalFrom(r.Context()).TenantID).Scan(&caps)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return caps, err
}

// addAppDomain claims a domain for an app. It serves nothing until
// verified.
func (s *Server) addAppDomain(w http.ResponseWriter, r *http.Request) {
	app, err := appParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req struct {
		Domain string `json:"domain"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	domain, err := embed.Domain(req.Domain)
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: %w", errBadRequest, err))
		return
	}
	if s.platformHost(domain) {
		s.fail(w, r, fmt.Errorf("%w: %s is the platform's own host", errBadRequest, domain))
		return
	}
	if u, err := url.Parse(s.PublicURL); err == nil && u.Hostname() != "" && strings.HasSuffix(domain, "."+strings.ToLower(u.Hostname())) {
		s.fail(w, r, fmt.Errorf("%w: %s is under the platform's own domain", errBadRequest, domain))
		return
	}
	p := principalFrom(r.Context())
	token := "taskiem-verify=" + newToken()[:32]
	err = s.tx(r, func(tx pgx.Tx) error {
		caps, err := partnerCapabilities(r, tx)
		if err != nil {
			return err
		}
		if !slices.Contains(caps, embed.CapCustomDomains) {
			return fmt.Errorf("%w: custom domains need the partner's plan to include custom_domains (ask the operator)", errForbidden)
		}
		if _, err := s.loadEmbedApp(r, tx, app); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM embed_app_domains WHERE app_id = $1`, app).Scan(&n); err != nil {
			return err
		}
		if n >= 10 {
			return fmt.Errorf("%w: an app may have at most 10 domains", errBadRequest)
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO embed_app_domains (tenant_id, domain, app_id, token, created_by) VALUES ($1, $2, $3, $4, $5)`,
			p.TenantID, domain, app, token, p.Actor()); err != nil {
			if isUnique(err) {
				return fmt.Errorf("%w: %s is already claimed by one of your apps", errConflict, domain)
			}
			return err
		}
		return auditTx(r, tx, "embed_app.domain_add", domain, map[string]any{"app": app})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, appDomain{Domain: domain, Record: "_taskiem-verify." + domain, TXTValue: token})
}

// verifyAppDomain checks the TXT record and, when it holds the token,
// marks the domain verified: from then on the Host serves the app.
func (s *Server) verifyAppDomain(w http.ResponseWriter, r *http.Request) {
	app, err := appParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	domain := strings.ToLower(chi.URLParam(r, "domain"))
	var token string
	err = s.tx(r, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT token FROM embed_app_domains WHERE app_id = $1 AND domain = $2`, app, domain).Scan(&token)
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
		caps, err := partnerCapabilities(r, tx)
		if err != nil {
			return err
		}
		if !slices.Contains(caps, embed.CapCustomDomains) {
			return fmt.Errorf("%w: custom domains need the partner's plan to include custom_domains (ask the operator)", errForbidden)
		}
		if _, err := tx.Exec(r.Context(), `UPDATE embed_app_domains SET verified_at = now() WHERE app_id = $1 AND domain = $2`, app, domain); err != nil {
			if isUnique(err) {
				return fmt.Errorf("%w: another partner has verified %s", errConflict, domain)
			}
			return err
		}
		return auditTx(r, tx, "embed_app.domain_verify", domain, map[string]any{"app": app})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.hosts.forget(domain)
	writeJSON(w, http.StatusOK, map[string]any{"domain": domain, "verified": true})
}

// removeAppDomain stops a domain serving the app at once (on this replica;
// others within the host cache's TTL).
func (s *Server) removeAppDomain(w http.ResponseWriter, r *http.Request) {
	app, err := appParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	domain := strings.ToLower(chi.URLParam(r, "domain"))
	err = s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM embed_app_domains WHERE app_id = $1 AND domain = $2`, app, domain)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return auditTx(r, tx, "embed_app.domain_remove", domain, map[string]any{"app": app})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.hosts.forget(domain)
	w.WriteHeader(http.StatusNoContent)
}

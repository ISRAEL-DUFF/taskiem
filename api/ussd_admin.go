package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// USSD channels (docs/ussd.md). An admin holding secret.manage connects
// an aggregator for the tenant: Taskiem makes a long random token, shows
// it once inside the callback URL to register with the aggregator, and
// keeps only its hash. Optionally the callbacks must also come from listed
// address ranges. Which workflows answer is decided by publishing: a
// workflow with a ussd trigger serves its service code in each
// environment it is deployed to.

var ussdProvider = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

type ussdChannelView struct {
	Provider     string    `json:"provider"`
	Environment  string    `json:"environment"`
	AllowedCIDRs []string  `json:"allowed_cidrs"`
	Status       string    `json:"status"`
	CallbackPath string    `json:"callback_path"`
	CreatedAt    time.Time `json:"created_at"`
	// Token is returned once, when made or rotated.
	Token       string `json:"token,omitempty"`
	CallbackURL string `json:"callback_url,omitempty"`
}

type ussdRouteView struct {
	ServiceCode string    `json:"service_code"`
	Environment string    `json:"environment"`
	WorkflowID  uuid.UUID `json:"workflow_id"`
	Workflow    string    `json:"workflow"`
	Version     int       `json:"version"`
}

func ussdCallbackPath(tenant uuid.UUID, provider string) string {
	return "/channels/ussd/" + tenant.String() + "/" + provider
}

// getUSSD lists the tenant's channels and the service codes it serves.
func (s *Server) getUSSD(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	out := struct {
		Channels []ussdChannelView `json:"channels"`
		Routes   []ussdRouteView   `json:"routes"`
	}{Channels: []ussdChannelView{}, Routes: []ussdRouteView{}}
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT provider, environment, allowed_cidrs, status, created_at FROM ussd_channels WHERE tenant_id = $1 ORDER BY provider`, p.TenantID)
		if err != nil {
			return err
		}
		chs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ussdChannelView, error) {
			var v ussdChannelView
			var cidrs []netip.Prefix
			err := row.Scan(&v.Provider, &v.Environment, &cidrs, &v.Status, &v.CreatedAt)
			v.AllowedCIDRs = []string{}
			for _, c := range cidrs {
				v.AllowedCIDRs = append(v.AllowedCIDRs, c.String())
			}
			v.CallbackPath = ussdCallbackPath(p.TenantID, v.Provider)
			return v, err
		})
		if err != nil {
			return err
		}
		out.Channels = append(out.Channels, chs...)
		rows, err = tx.Query(r.Context(), `SELECT t.service_code, t.environment, t.workflow_id, w.name, t.version FROM triggers t
			JOIN workflows w ON w.id = t.workflow_id WHERE t.type = 'ussd' ORDER BY t.environment, t.service_code`)
		if err != nil {
			return err
		}
		routes, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ussdRouteView, error) {
			var v ussdRouteView
			return v, row.Scan(&v.ServiceCode, &v.Environment, &v.WorkflowID, &v.Workflow, &v.Version)
		})
		out.Routes = append(out.Routes, routes...)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// putUSSDChannel connects or changes a channel. A new channel, or
// rotate_token, makes a new token, shown once.
func (s *Server) putUSSDChannel(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	provider := chi.URLParam(r, "provider")
	if !ussdProvider.MatchString(provider) || s.ussdAdapter(provider) == nil {
		s.fail(w, r, fmt.Errorf("%w: unknown USSD provider %q", errBadRequest, provider))
		return
	}
	var req struct {
		Environment  string   `json:"environment"`
		AllowedCIDRs []string `json:"allowed_cidrs"`
		RotateToken  bool     `json:"rotate_token"`
		Disabled     bool     `json:"disabled"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Environment == "" {
		req.Environment = "prod"
	}
	cidrs := []netip.Prefix{}
	for _, c := range req.AllowedCIDRs {
		pfx, err := netip.ParsePrefix(c)
		if err != nil {
			a, aerr := netip.ParseAddr(c)
			if aerr != nil {
				s.fail(w, r, fmt.Errorf("%w: allowed_cidrs: %q is not an address or range", errBadRequest, c))
				return
			}
			pfx = netip.PrefixFrom(a, a.BitLen())
		}
		cidrs = append(cidrs, pfx.Masked())
	}
	if len(cidrs) > 32 {
		s.fail(w, r, fmt.Errorf("%w: at most 32 allowed_cidrs", errBadRequest))
		return
	}
	status := "active"
	if req.Disabled {
		status = "disabled"
	}
	v := ussdChannelView{Provider: provider, Environment: req.Environment, Status: status, CallbackPath: ussdCallbackPath(p.TenantID, provider), AllowedCIDRs: []string{}}
	for _, c := range cidrs {
		v.AllowedCIDRs = append(v.AllowedCIDRs, c.String())
	}
	err := s.tx(r, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM environments WHERE name = $1)`, req.Environment).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: no environment %q", errBadRequest, req.Environment)
		}
		var had bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM ussd_channels WHERE tenant_id = $1 AND provider = $2)`, p.TenantID, provider).Scan(&had); err != nil {
			return err
		}
		if !had || req.RotateToken {
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				return err
			}
			v.Token = hex.EncodeToString(b)
		}
		if !had {
			if _, err := tx.Exec(r.Context(), `INSERT INTO ussd_channels (tenant_id, provider, environment, token_hash, allowed_cidrs, status, created_by)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`, p.TenantID, provider, req.Environment, ussdTokenHash(v.Token), cidrs, status, p.Actor()); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(r.Context(), `UPDATE ussd_channels SET environment = $3, allowed_cidrs = $4, status = $5, updated_at = now(),
				token_hash = CASE WHEN $6 THEN $7 ELSE token_hash END WHERE tenant_id = $1 AND provider = $2`,
				p.TenantID, provider, req.Environment, cidrs, status, v.Token != "", ussdTokenHash(v.Token)); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(r.Context(), `SELECT created_at FROM ussd_channels WHERE tenant_id = $1 AND provider = $2`, p.TenantID, provider).Scan(&v.CreatedAt); err != nil {
			return err
		}
		return auditTx(r, tx, "ussd.channel.set", provider, map[string]any{"environment": req.Environment, "allowed_cidrs": v.AllowedCIDRs,
			"status": status, "token_rotated": v.Token != "" && had, "created": !had})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ussdForgetChannel(p.TenantID, provider)
	if v.Token != "" {
		v.CallbackURL = v.CallbackPath + "?token=" + v.Token
		if s.PublicURL != "" {
			v.CallbackURL = s.PublicURL + v.CallbackURL
		}
	}
	writeJSON(w, http.StatusOK, v)
}

// deleteUSSDChannel disconnects an aggregator: its callbacks get 404.
func (s *Server) deleteUSSDChannel(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	provider := chi.URLParam(r, "provider")
	err := s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM ussd_channels WHERE tenant_id = $1 AND provider = $2`, p.TenantID, provider)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errNotFoundUSSD
		}
		return auditTx(r, tx, "ussd.channel.delete", provider, nil)
	})
	if errors.Is(err, errNotFoundUSSD) {
		writeErr(w, http.StatusNotFound, "no such channel")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ussdForgetChannel(p.TenantID, provider)
	w.WriteHeader(http.StatusNoContent)
}

var errNotFoundUSSD = errors.New("no such USSD channel")

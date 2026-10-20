package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/embedsites"
)

// Websites allowed to embed the organisation's storefront and forms
// (docs/embed-sites.md): added, then proven by a DNS TXT record or a file,
// then re-proven by the scheduler (embedsites.Checker).

type embedSite struct {
	embedsites.Site
	Prove map[string]string `json:"prove"`
}

func siteOut(s embedsites.Site) embedSite { return embedSite{Site: s, Prove: s.Instructions()} }

// siteErr maps the package's refusals to the API's.
func siteErr(err error) error {
	switch {
	case errors.Is(err, embedsites.ErrInvalid), errors.Is(err, embedsites.ErrLimit):
		return fmt.Errorf("%w: %s", errBadRequest, plainError(err))
	case errors.Is(err, embedsites.ErrExists), errors.Is(err, embedsites.ErrNotProven):
		return fmt.Errorf("%w: %s", errConflict, plainError(err))
	case errors.Is(err, embedsites.ErrNotFound):
		return pgx.ErrNoRows
	}
	return err
}

func (s *Server) listEmbedSites(w http.ResponseWriter, r *http.Request) {
	var sites []embedsites.Site
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		sites, err = embedsites.List(r.Context(), tx, principalFrom(r.Context()).TenantID)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]embedSite, 0, len(sites))
	for _, x := range sites {
		out = append(out, siteOut(x))
	}
	lim, err := s.Store.LimitsFor(r.Context(), principalFrom(r.Context()).TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": out, "max": lim.EmbedSites})
}

func (s *Server) addEmbedSite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Origin string `json:"origin"`
		Method string `json:"method"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Method == "" {
		req.Method = embedsites.DNS
	}
	var platform []string
	if u, err := url.Parse(s.PublicURL); err == nil && u.Hostname() != "" {
		platform = append(platform, u.Hostname())
	}
	platform = append(platform, s.PublicHosts...)
	origin, err := embedsites.NormaliseOrigin(req.Origin, platform)
	if err != nil {
		s.fail(w, r, siteErr(err))
		return
	}
	p := principalFrom(r.Context())
	lim, err := s.Store.LimitsFor(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var site embedsites.Site
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if site, err = embedsites.Add(r.Context(), tx, p.TenantID, origin, req.Method, p.Actor(), lim.EmbedSites); err != nil {
			return siteErr(err)
		}
		return auditTx(r, tx, "embed_site.add", origin, map[string]any{"method": req.Method})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, siteOut(site))
}

// verifyEmbedSite checks the site's record or file now and, when it holds
// the token, marks it verified: from then on it may frame /embed/ pages
// (the public host's cache within 30 seconds).
func (s *Server) verifyEmbedSite(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	tenant := principalFrom(r.Context()).TenantID
	var site embedsites.Site
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		site, err = embedsites.Get(r.Context(), tx, tenant, id)
		return siteErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	prover := s.SiteProver
	if prover == nil {
		prover = &embedsites.Prover{LookupTXT: LookupTXT}
	}
	ok, _, perr := prover.Prove(r.Context(), site)
	if !ok {
		if perr == nil {
			perr = errors.New("the proof was not found")
		}
		s.fail(w, r, fmt.Errorf("%w: %s (DNS and caches can take a while)", errConflict, plainError(fmt.Errorf("%w: %w", embedsites.ErrNotProven, perr))))
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		if err := embedsites.MarkVerified(r.Context(), tx, tenant, id); err != nil {
			return siteErr(err)
		}
		return auditTx(r, tx, "embed_site.verify", site.Origin, map[string]any{"method": site.Method})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "origin": site.Origin, "verified": true})
}

func (s *Server) removeEmbedSite(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		site, err := embedsites.Remove(r.Context(), tx, principalFrom(r.Context()).TenantID, id)
		if err != nil {
			return siteErr(err)
		}
		return auditTx(r, tx, "embed_site.remove", site.Origin, nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

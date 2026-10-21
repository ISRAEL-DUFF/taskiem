package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/embedsites"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// A business's own domain for its storefront (Phase 5, S6;
// docs/storefronts.md#your-own-domain): added, proven by a DNS TXT record
// (the embed sites' lookup, s.SiteProver), then served by the public host
// as a public_hosts row; proven again by the scheduler
// (storefront.DomainChecker).

type storefrontDomain struct {
	storefront.Domain
	// Status is pending (not proven yet), live or unverified (it failed
	// the checks; LastCheckError says why).
	Status  string              `json:"status"`
	Records []storefront.Record `json:"records"`
}

func (s *Server) domainOut(d storefront.Domain) storefrontDomain {
	status := "pending"
	switch {
	case d.VerifiedAt != nil:
		status = "live"
	case d.UnverifiedAt != nil:
		status = "unverified"
	}
	return storefrontDomain{Domain: d, Status: status, Records: d.Records(s.domainTarget())}
}

// domainTarget is where domains point: the operator's, or by default
// pages.<preview domain>, which the preview domain's wildcard record
// already sends to the public host.
func (s *Server) domainTarget() storefront.DomainTarget {
	t := s.DomainTarget
	if t.CNAME == "" && s.previewDomain() != "" {
		t.CNAME = "pages." + s.previewDomain()
	}
	return t
}

// platformHosts are names no business may bring: the web app's and the
// public host's own.
func (s *Server) platformHosts() []string {
	var out []string
	if u, err := url.Parse(s.PublicURL); err == nil && u.Hostname() != "" {
		out = append(out, u.Hostname())
	}
	return append(out, s.PublicHosts...)
}

func domainErr(err error) error {
	switch {
	case errors.Is(err, storefront.ErrInvalid), errors.Is(err, storefront.ErrDomainLimit):
		return fmt.Errorf("%w: %s", errBadRequest, plainError(err))
	case errors.Is(err, storefront.ErrDomainExists), errors.Is(err, storefront.ErrDomainTaken), errors.Is(err, storefront.ErrNotProven):
		return fmt.Errorf("%w: %s", errConflict, plainError(err))
	case errors.Is(err, storefront.ErrDomainNotFound):
		return pgx.ErrNoRows
	case errors.Is(err, storefront.ErrNotFound):
		return fmt.Errorf("%w: save the storefront first", errConflict)
	}
	return err
}

func (s *Server) listStorefrontDomains(w http.ResponseWriter, r *http.Request) {
	var ds []storefront.Domain
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		ds, err = storefront.ListDomains(r.Context(), tx, principalFrom(r.Context()).TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]storefrontDomain, 0, len(ds))
	for _, d := range ds {
		out = append(out, s.domainOut(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"domains": out, "max": storefront.MaxDomains, "target": s.domainTarget()})
}

func (s *Server) addStorefrontDomain(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain string `json:"domain"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	host, twin, err := storefront.NormaliseDomain(req.Domain, s.platformHosts())
	if err != nil {
		s.fail(w, r, domainErr(err))
		return
	}
	p := principalFrom(r.Context())
	var d storefront.Domain
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if d, err = storefront.AddDomain(r.Context(), tx, p.TenantID, host, twin, p.Actor()); err != nil {
			return domainErr(err)
		}
		return auditTx(r, tx, "storefront_domain.add", host, map[string]any{"twin": twin})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.domainOut(d))
}

func domainID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, pgx.ErrNoRows
	}
	return id, nil
}

// verifyStorefrontDomain checks the TXT record now and, when it holds the
// token, serves the domain: the public host within its cache's 30
// seconds, and the proxy may then obtain its certificate.
func (s *Server) verifyStorefrontDomain(w http.ResponseWriter, r *http.Request) {
	id, err := domainID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tenant := principalFrom(r.Context()).TenantID
	var d storefront.Domain
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		d, err = storefront.GetDomain(r.Context(), tx, tenant, id)
		return domainErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	prover := s.SiteProver
	if prover == nil {
		prover = &embedsites.Prover{LookupTXT: LookupTXT}
	}
	ok, _, perr := prover.Prove(r.Context(), d.Site())
	if !ok {
		if perr == nil {
			perr = errors.New("the record was not found")
		}
		s.fail(w, r, fmt.Errorf("%w: %s (DNS and caches can take a while)", errConflict, plainError(fmt.Errorf("%w: %w", storefront.ErrNotProven, perr))))
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if d, err = storefront.VerifyDomain(r.Context(), tx, tenant, id); err != nil {
			return domainErr(err)
		}
		return auditTx(r, tx, "storefront_domain.verify", d.Host, map[string]any{"primary": d.Primary})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.domainOut(d))
}

// primaryStorefrontDomain makes a verified domain the one the page's
// other addresses redirect to.
func (s *Server) primaryStorefrontDomain(w http.ResponseWriter, r *http.Request) {
	id, err := domainID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var d storefront.Domain
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if d, err = storefront.SetPrimaryDomain(r.Context(), tx, principalFrom(r.Context()).TenantID, id); err != nil {
			return domainErr(err)
		}
		return auditTx(r, tx, "storefront_domain.primary", d.Host, nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.domainOut(d))
}

// removeStorefrontDomain stops serving a domain at once (the public host
// within its cache's 30 seconds).
func (s *Server) removeStorefrontDomain(w http.ResponseWriter, r *http.Request) {
	id, err := domainID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		d, err := storefront.RemoveDomain(r.Context(), tx, principalFrom(r.Context()).TenantID, id)
		if err != nil {
			return domainErr(err)
		}
		return auditTx(r, tx, "storefront_domain.remove", d.Host, nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// checkStorefrontDomain answers POST /v1/storefront/domains/{id}/check
// (no-technical-steps N9): each record the owner was asked to create, what
// DNS has for it now and, in plain words, what to fix. It changes
// nothing; verify serves the domain once the proof is there.
func (s *Server) checkStorefrontDomain(w http.ResponseWriter, r *http.Request) {
	id, err := domainID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var d storefront.Domain
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		d, err = storefront.GetDomain(r.Context(), tx, principalFrom(r.Context()).TenantID, id)
		return domainErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	checks := storefront.CheckRecords(r.Context(), d, s.domainTarget(), storefront.Resolver{
		TXT:   func(ctx context.Context, n string) ([]string, error) { return LookupTXT(ctx, n) },
		CNAME: func(ctx context.Context, n string) (string, error) { return LookupCNAME(ctx, n) },
		Host:  func(ctx context.Context, n string) ([]string, error) { return LookupHost(ctx, n) },
	})
	all, proof := true, false
	for _, c := range checks {
		all = all && c.OK
		if c.Purpose == "proof" {
			proof = c.OK
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"checks": checks, "all_ok": all, "proven": proof})
}

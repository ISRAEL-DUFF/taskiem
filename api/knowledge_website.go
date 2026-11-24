package api

import (
	"context"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/embedsites"
	"github.com/israel-duff/taskiem/engine/knowledge"
	"github.com/israel-duff/taskiem/engine/knowledge/extract"
)

// readWebsite reads the business's own website (K6): only a site it has
// verified as its own (Settings, Websites), from its first page, only
// links on that origin, at most the pages the source allows, through the
// egress guard to that one host. A page each section, linked back.
//
// A site's first page often sends visitors to its www (or bare) twin: the
// crawl follows that only when the twin is verified as the business's own
// too, and then reads the twin only.
func (s *Server) readWebsite(ctx context.Context, tenant uuid.UUID, src knowledge.Source) ([]knowledge.Section, error) {
	var cfg struct {
		Site     string `json:"site"`
		Start    string `json:"start"`
		MaxPages int    `json:"max_pages"`
	}
	if err := knowledgeConfig(src, &cfg); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(cfg.Site)
	if err != nil {
		return nil, knowledge.Plainly(knowledge.ErrGone, "the source names no website")
	}
	var site embedsites.Site
	var all []embedsites.Site
	if err := s.tx0(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if site, err = embedsites.Get(ctx, tx, tenant, id); err != nil {
			return err
		}
		all, err = embedsites.List(ctx, tx, tenant)
		return err
	}); err != nil {
		return nil, knowledge.Plainly(knowledge.ErrGone, "the website is no longer one of the business's")
	}
	// Verified now, not only when the source was made: a site that lost
	// its proof is not read, and what it said is no longer used.
	if !siteVerified(site) {
		return nil, knowledge.Plainly(knowledge.ErrGone, "%s is no longer verified as the business's own", site.Origin)
	}
	hosts := []string{site.Host()}
	var twins []string
	for _, o := range all {
		if o.ID != site.ID && siteVerified(o) && twinHosts(o.Host(), site.Host()) {
			hosts = append(hosts, o.Host())
			twins = append(twins, o.Origin)
		}
	}
	u, err := url.Parse(cfg.Start)
	if err != nil || u.Scheme+"://"+u.Host != site.Origin {
		u, _ = url.Parse(site.Origin + "/")
	}
	g := s.Egress
	if g == nil {
		g = &egress.Guard{Logger: s.Logger}
	}
	hc := g.Client(egress.Policy{Tenant: tenant.String(), Hosts: hosts, Purpose: "knowledge:website"}, 20*time.Second)
	if s.KnowledgeWebClient != nil {
		hc = s.KnowledgeWebClient
	}
	pages := cfg.MaxPages
	if pages <= 0 {
		pages = 20
	}
	read, errs := extract.CrawlWith(ctx, hc, u.String(), pages, extract.CrawlOptions{AllowOrigins: twins})
	if len(read) == 0 {
		var cause error
		if len(errs) > 0 {
			cause = errs[0]
		}
		return nil, knowledge.Plainly(cause, "no page of %s could be read: check that the site is up and its first page opens", site.Origin)
	}
	out := make([]knowledge.Section, 0, len(read))
	for _, p := range read {
		if p.Text == "" {
			continue
		}
		out = append(out, knowledge.Section{Title: p.Title, Text: p.Text, Link: p.URL})
	}
	return out, nil
}

func siteVerified(s embedsites.Site) bool { return s.VerifiedAt != nil && s.UnverifiedAt == nil }

// twinHosts: one host is the other with www. in front.
func twinHosts(a, b string) bool {
	return a == "www."+b || b == "www."+a
}

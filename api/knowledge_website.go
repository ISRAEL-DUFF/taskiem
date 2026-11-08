package api

import (
	"context"
	"errors"
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
		return nil, errors.New("the source names no website")
	}
	var site embedsites.Site
	if err := s.tx0(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		site, err = embedsites.Get(ctx, tx, tenant, id)
		return err
	}); err != nil {
		return nil, errors.New("the website is no longer one of the business's")
	}
	// Verified now, not only when the source was made: a site that lost
	// its proof is not read.
	if site.VerifiedAt == nil || site.UnverifiedAt != nil {
		return nil, errors.New(site.Origin + " is no longer verified as the business's own")
	}
	u, err := url.Parse(cfg.Start)
	if err != nil || u.Scheme+"://"+u.Host != site.Origin {
		u, _ = url.Parse(site.Origin + "/")
	}
	g := s.Egress
	if g == nil {
		g = &egress.Guard{Logger: s.Logger}
	}
	hc := g.Client(egress.Policy{Tenant: tenant.String(), Hosts: []string{u.Hostname()}, Purpose: "knowledge:website"}, 20*time.Second)
	if s.KnowledgeWebClient != nil {
		hc = s.KnowledgeWebClient
	}
	pages := cfg.MaxPages
	if pages <= 0 {
		pages = 20
	}
	read, errs := extract.Crawl(ctx, hc, u.String(), pages)
	if len(read) == 0 {
		if len(errs) > 0 {
			return nil, errs[0]
		}
		return nil, errors.New("no page of the website could be read")
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

package api

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/pages"
)

// Pages H5, g-pages-runs (docs/spec/pages.md §13, §16 Q3): a tool that
// feeds a part runs as any run, so the page's settings estimate how many
// it starts a day: the page's views a day over the last seven days times
// the tools its parts start, beside the plan's run limits.

// pageViewSeen keeps, for a minute, that a viewer opened a page: what a
// tool answered is kept sixty seconds for a viewer (page_source_cache), so
// a page read again within the minute (a live notice, a part still
// working, filters changed) starts nothing new and is not another view.
var (
	pageViewSeen  sync.Map // tenant/page/viewer -> time.Time
	pageViewCount atomic.Uint64
)

// countPageView counts one view of a published page for the estimate. It
// never fails the view: a count lost is an estimate a little low.
func (s *Server) countPageView(ctx context.Context, p *Principal, page uuid.UUID) {
	now := time.Now()
	key := p.TenantID.String() + "/" + page.String() + "/" + p.Actor()
	if at, ok := pageViewSeen.Load(key); ok && now.Sub(at.(time.Time)) < time.Minute {
		return
	}
	pageViewSeen.Store(key, now)
	if pageViewCount.Add(1)%1000 == 0 {
		pageViewSeen.Range(func(k, v any) bool {
			if now.Sub(v.(time.Time)) >= time.Minute {
				pageViewSeen.Delete(k)
			}
			return true
		})
	}
	day := now.In(lagosTZ).Format("2006-01-02")
	_ = db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		var first bool
		if err := tx.QueryRow(ctx, `INSERT INTO page_views (tenant_id, page_id, day, views) VALUES ($1, $2, $3, 1)
			ON CONFLICT (tenant_id, page_id, day) DO UPDATE SET views = page_views.views + 1
			RETURNING xmax = 0`, p.TenantID, page, day).Scan(&first); err != nil {
			return err
		}
		if first { // a new day: only the last eight are kept
			_, err := tx.Exec(ctx, `DELETE FROM page_views WHERE page_id = $1 AND day < $2::date - 8`, page, day)
			return err
		}
		return nil
	})
}

// pageToolRuns is how many runs one view of the page may start: the tools
// its parts are fed by, two parts one tool feeds with the same values
// counted once (they share a run).
func pageToolRuns(p *pages.Page) int {
	seen := map[string]bool{}
	for _, b := range p.Blocks {
		if b.Source == nil || b.Source.Tool == "" {
			continue
		}
		in, _ := json.Marshal(b.Source.Inputs)
		seen[b.Source.Tool+"\x00"+string(in)] = true
	}
	return len(seen)
}

// pageRunsEstimate is the estimate on a page's settings: views a day over
// the last seven days, the tools a view starts, about how many runs a day
// that is, and the plan's limits (0: none).
func (s *Server) pageRunsEstimate(ctx context.Context, tx pgx.Tx, tenant, page uuid.UUID, doc *pages.Page) (map[string]any, error) {
	var views int64
	today := time.Now().In(lagosTZ).Format("2006-01-02")
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(views), 0) FROM page_views WHERE page_id = $1 AND day > $2::date - 7 AND day <= $2::date`,
		page, today).Scan(&views); err != nil {
		return nil, err
	}
	tools := 0
	if doc != nil {
		tools = pageToolRuns(doc)
	}
	perDay := float64(views) / 7
	out := map[string]any{
		"views_a_day": int64(math.Round(perDay)),
		"tools":       tools,
		"runs_a_day":  int64(math.Ceil(perDay * float64(tools))),
	}
	if lim, err := s.Store.LimitsFor(ctx, tenant); err == nil {
		out["runs_per_day"], out["runs_per_month"] = lim.RunsPerDay, lim.RunsPerMonth
	}
	return out, nil
}

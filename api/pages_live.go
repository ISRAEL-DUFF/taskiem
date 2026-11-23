package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pages"
)

// A page kept up to date (docs/spec/pages.md §9, "Kept up to date"): GET
// /v1/pages/{name}/live, as server-sent events, tells an open page when
// what its parts read has changed, so a change made on the web shows on
// the phone without a reload. It follows the change notices the chat
// tools and the Chat page use (chat_changes.go's lines reach the Chat
// page's stream, chat_stream.go): each connection asks, from indexes
// (migration 00555), in the business's own transaction, what changed
// since it last looked; nothing is pushed through the database.
//
// A notice carries no data: only the page's name and the parts that read
// what changed. The client reads those parts again (GET .../view, or a
// part's first rows), as a pull to refresh would; a big table never
// streams its rows.
//
//	ready    on connecting: {"page", "blocks": [the parts watched]}
//	changed  some parts may have changed: {"page", "blocks": [ids]}; every
//	         part when the page itself changed (published again, its draft)
//	gone     the viewer may no longer open the page (its roles changed, or
//	         it was deleted); the stream ends
//	ping     at least every ten seconds
//
// Debounced: a connection asks every two seconds (pageLiveEvery), and all
// that changed in between is one notice, so a page is told at most once
// in two seconds however many rows a burst changes.
//
// What is watched: a data table's rows written through Taskiem (the API,
// the chat's tools, a workflow's step, the shop's stock: table_row_changes),
// and the business's orders and invoices (updated_at), for the parts the
// viewer may see (a record view needs the area's read permission). Only
// a page the viewer may open, and only their business's rows: the stream
// opens as the page's view does (mayOpenPage) and reads in the
// business's scope.
const (
	pageLiveEvery = 2 * time.Second
	// A record changed by a transaction that commits a little after it
	// wrote (its statement's clock) is read again within this look-back;
	// what was told is remembered, so it is told once.
	pageLiveLookBack = 10 * time.Second
	pageLiveMax      = 30 * time.Minute
	pageLivePing     = 10 * time.Second
	// How many page streams one business may hold open (its people's open
	// pages); the server's cap is the Chat page's (chatStreamsTotal).
	pageLivePerTenant = 40
	// How many changed records one look reads; past it, everything the
	// view feeds is told and the look-back moves on.
	pageLiveBatch = 200
)

// pageWatch is what an open page watches, and what it has seen.
type pageWatch struct {
	tables  map[string][]string // a data table's name: the parts reading it
	records map[string][]string // a record view's name: the parts reading it
	// tableAt is when each table's rows were last written, as last seen.
	tableAt map[string]time.Time
	// seen are the records changed within the look-back already told,
	// by view, row and when.
	seen    map[string]map[string]time.Time
	started bool
}

// newPageWatch is what a page's parts read, for this viewer: a record
// view only when they may see it and its area is on.
func newPageWatch(page *pages.Page, p *Principal, areaOn func(string) bool) *pageWatch {
	w := &pageWatch{tables: map[string][]string{}, records: map[string][]string{}, tableAt: map[string]time.Time{}, seen: map[string]map[string]time.Time{}}
	for _, b := range page.Blocks {
		if b.Source == nil {
			continue
		}
		if b.Source.Records != "" {
			v, ok := pages.RecordViewNamed(b.Source.Records)
			if ok && p.Can(v.Perm) && (areaOn == nil || areaOn(string(v.Area))) {
				w.records[v.Name] = append(w.records[v.Name], b.ID)
			}
			continue
		}
		if b.Source.Table != "" {
			w.tables[b.Source.Table] = append(w.tables[b.Source.Table], b.ID)
		}
	}
	return w
}

// blocks are every part watched, in the page's order of first mention.
func (w *pageWatch) blocks() []string {
	var out []string
	for _, ids := range w.tables {
		out = append(out, ids...)
	}
	for _, ids := range w.records {
		out = append(out, ids...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// poll is the parts whose sources changed since the last look (none on
// the first, which only sees where things are). tx is the business's.
func (w *pageWatch) poll(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, now time.Time) ([]string, error) {
	first := !w.started
	w.started = true
	var changed []string
	if len(w.tables) > 0 {
		names := make([]string, 0, len(w.tables))
		for n := range w.tables {
			names = append(names, n)
		}
		at, err := datatables.RowsChanged(ctx, tx, names)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if t, ok := at[n]; ok && !t.Equal(w.tableAt[n]) {
				w.tableAt[n] = t
				if !first {
					changed = append(changed, w.tables[n]...)
				}
			}
		}
	}
	since := now.Add(-pageLiveLookBack)
	for name, ids := range w.records {
		v, _ := pages.RecordViewNamed(name)
		alias := v.Tenant[:len(v.Tenant)-len(".tenant_id")]
		rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s.id::text, %s FROM %s WHERE %s = $1 AND %s >= $2 ORDER BY %s LIMIT %d`,
			alias, v.Updated, v.From, v.Tenant, v.Updated, v.Updated, pageLiveBatch), tenant, since)
		if err != nil {
			return nil, err
		}
		seen := w.seen[name]
		if seen == nil {
			seen = map[string]time.Time{}
			w.seen[name] = seen
		}
		fresh, n := false, 0
		for rows.Next() {
			var id string
			var at time.Time
			if err := rows.Scan(&id, &at); err != nil {
				rows.Close()
				return nil, err
			}
			n++
			if t, ok := seen[id]; ok && t.Equal(at) {
				continue
			}
			seen[id] = at
			fresh = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if (fresh || n == pageLiveBatch) && !first {
			changed = append(changed, ids...)
		}
		for id, at := range seen {
			if at.Before(since) {
				delete(seen, id)
			}
		}
	}
	slices.Sort(changed)
	return slices.Compact(changed), nil
}

// pageLiveState is the page as the stream last read it.
type pageLiveState struct {
	updated time.Time
	page    *pages.Page
	watch   *pageWatch
}

// errPageGone ends a page's stream: the viewer may no longer open it.
var errPageGone = errors.New("pages: the page cannot be opened")

// loadPageLive reads the page for its stream: what the viewer may open,
// and what it watches.
func loadPageLive(ctx context.Context, tx pgx.Tx, p *Principal, name string, draft bool) (pageLiveState, error) {
	pg, err := loadPage(ctx, tx, name)
	if errors.Is(err, errNoPage) || err == nil && !mayOpenPage(p, pg) {
		return pageLiveState{}, errPageGone
	}
	if err != nil {
		return pageLiveState{}, err
	}
	doc := pg.Published
	if draft && p.Can(PermPageEdit) && pg.Draft != nil {
		doc = pg.Draft
	}
	if doc == nil {
		return pageLiveState{}, errPageGone
	}
	page, _ := pages.Parse(doc)
	if page == nil {
		return pageLiveState{}, errPageGone
	}
	areaOn, err := pageAreas(ctx, tx)
	if err != nil {
		return pageLiveState{}, err
	}
	return pageLiveState{updated: pg.UpdatedAt, page: page, watch: newPageWatch(page, p, areaOn)}, nil
}

func (s *Server) pageLiveEvery() time.Duration {
	if s.PageLiveEvery > 0 {
		return s.PageLiveEvery
	}
	return pageLiveEvery
}

// pageLive is GET /v1/pages/{name}/live.
func (s *Server) pageLive(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	name := chi.URLParam(r, "name")
	draft := r.URL.Query().Get("draft") == "1"
	var st pageLiveState
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) (err error) {
		st, err = loadPageLive(ctx, tx, p, name, draft)
		return err
	}); err != nil {
		if errors.Is(err, errPageGone) {
			err = errNoPage
		}
		s.fail(w, r, err)
		return
	}
	release, ok := s.pageGate.acquireN(p.TenantID, pageLivePerTenant, "business")
	if !ok {
		w.Header().Set("Retry-After", "30")
		writeErr(w, http.StatusTooManyRequests, "too many open pages are kept up to date: pull to refresh this one")
		return
	}
	defer release()
	ping := s.ChatStreamPing
	if ping <= 0 {
		ping = pageLivePing
	}
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	write := func(event string, data any) error {
		b, _ := json.Marshal(data)
		_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return err
		}
		return rc.Flush()
	}
	if _, err := fmt.Fprint(w, "retry: 2000\n\n"); err != nil {
		return
	}
	// The first look only sees where things are.
	poll := func() ([]string, bool, error) {
		var changed []string
		all := false
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
			var updated time.Time
			err := tx.QueryRow(ctx, `SELECT updated_at FROM pages WHERE name = $1`, name).Scan(&updated)
			if errors.Is(err, pgx.ErrNoRows) {
				return errPageGone
			}
			if err != nil {
				return err
			}
			if !updated.Equal(st.updated) {
				// The page itself changed (published, its roles, its
				// draft): read again, and every part told.
				next, err := loadPageLive(ctx, tx, p, name, draft)
				if err != nil {
					return err
				}
				st, all = next, true
			}
			changed, err = st.watch.poll(ctx, tx, p.TenantID, time.Now())
			return err
		})
		return changed, all, err
	}
	if _, _, err := poll(); err != nil {
		return
	}
	if write("ready", map[string]any{"page": name, "blocks": nonNil(st.watch.blocks())}) != nil {
		return
	}
	lastWrite := time.Now()
	deadline := time.Now().Add(pageLiveMax)
	tick := time.NewTicker(s.pageLiveEvery())
	defer tick.Stop()
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-s.done():
			return
		case <-tick.C:
		}
		changed, all, err := poll()
		if errors.Is(err, errPageGone) {
			_ = write("gone", map[string]any{"page": name})
			return
		}
		if err != nil {
			if ctx.Err() == nil {
				s.Logger.Error("page live", "tenant", p.TenantID, "err", err)
			}
			return
		}
		if all {
			changed = []string{}
			for _, b := range st.page.Blocks {
				changed = append(changed, b.ID)
			}
		}
		if len(changed) > 0 {
			if write("changed", map[string]any{"page": name, "blocks": changed, "all": all}) != nil {
				return
			}
			lastWrite = time.Now()
		}
		if time.Since(lastWrite) >= ping {
			if write("ping", map[string]any{}) != nil {
				return
			}
			lastWrite = time.Now()
		}
	}
}
